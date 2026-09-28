// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/MessageCache.swift; GTK:
// ui/internal/window/message_view.go (fetchMessage, settleLoaded,
// loadedFor, storeLoaded), remote.go (fetchPart, loadRemoteImages,
// fetchRemoteImages, imagesDone, refreshRemoteBar, showLoaded), outbox.go
// (refetchMessage), attachments.go (fetchAttachment) and embedded.go
// (fetchEmbedded).
//
// Every fire-and-forget call goes through ControllerScope.Perform, which
// starts the call after the caller's turn as Swift's Task does
// (docs/windows-port.md §7.2): fetch starts both halves before either can
// settle, so a transport that fails at once (not connected) still finds the
// other half in flight and keeps the waiters for it. Swift keys its waiters
// by ObjectIdentifier; here a dictionary compares the entries by reference.
// The calls that the caller awaits (message.part, message.embedded) are
// made directly. The cache has no closed flag, as in Swift: it lives as long
// as the app. Swift's callbacks cannot throw; a view's handler or waiter
// here can, and is isolated (ControllerEvents), so that one view's failure
// strands neither the other views nor the request it came with.

using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Platform;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>
/// The loaded-message cache and its fetching, minus the widgets. The pane,
/// every message window and the compose prefill share one cache; the views
/// register for the answers they asked for and every view showing a message
/// hears about it through <see cref="MessageLoaded"/>.
/// </summary>
/// <remarks>
/// Every call runs as work of the controller's scope on the UI thread, so
/// the continuation after the call is on the UI thread too and the entries
/// are only ever touched there (the GTK window's <c>glib.IdleAdd</c>
/// discipline). Callers guard staleness themselves (the pane by its current
/// message, a window by its closed flag): the cache is keyed by id and stays
/// valid whatever is on display now. A handler or waiter that throws is
/// reported by <see cref="Pending"/> (logged at error level) and the others
/// are called all the same. UI-thread-affine (docs/windows-port.md §7.1).
/// </remarks>
public sealed partial class MessageCache : IDisposable, IActionsCache, IReaderCache
{
    private readonly ControllerScope scope;
    private readonly Action<string> toast;
    private readonly ILogger logger;

    // Who wants to hear about the in-flight halves of an entry
    // (loadedMessage.waiters), kept beside the entry so that an entry evicted
    // while in flight keeps its own waiters.
    private readonly Dictionary<LoadedMessage, List<Action<LoadedMessage>>> waiters = new(ReferenceEqualityComparer.Instance);

    /// <summary>A cache over <paramref name="client"/>.</summary>
    /// <param name="client">The transport; calls fail with notConnected until the connection controller reports a connection.</param>
    /// <param name="toast">Shows a transient message (the window's toast overlay).</param>
    /// <param name="logger">Receives methods and codes, never mail data.</param>
    /// <param name="pending">Counts the controller's background work (a tracker of its own when null).</param>
    public MessageCache(RpcClient client, Action<string> toast, ILogger<MessageCache>? logger = null, PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        ArgumentNullException.ThrowIfNull(toast);
        Client = client;
        this.toast = toast;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        scope = new ControllerScope(pending);
    }

    /// <summary>
    /// Fired after every settle of a half (<c>message.get</c> or
    /// <c>message.body</c> answered) and after remote images arrived, with the
    /// entry as it is now: every view showing the message re-renders (Swift
    /// <c>onLoaded</c>; remote.go <c>showLoaded</c>, generalised).
    /// </summary>
    public event EventHandler<MessageCacheEntry>? MessageLoaded;

    /// <summary>
    /// Fired when only the remote-image bar of a message changed (the request
    /// for its images started or ended without a new body): the views redraw
    /// the bar and leave the body alone (Swift <c>onRemoteBar</c>; remote.go
    /// <c>refreshRemoteBar</c>, which avoids reloading the web view).
    /// </summary>
    public event EventHandler<MessageCacheEntry>? RemoteBarChanged;

    /// <summary>The transport.</summary>
    public RpcClient Client { get; }

    /// <summary>The entries (<c>Window.loaded</c>).</summary>
    public LoadedCache Cache { get; } = new();

    /// <summary>Counts the controller's background work; tests wait on it.</summary>
    public PendingWork Pending => scope.Pending;

    // Lookup

    /// <summary>The cached entry of <paramref name="id"/>, if any, in whatever state it is.</summary>
    public LoadedMessage? Loaded(MessageId id) => Cache[id];

    /// <summary>
    /// What the cache knows about message <paramref name="id"/> beyond the
    /// list: the summary of the cached full message (the second half of
    /// message_view.go <c>summary</c>, for a message window outliving the
    /// folder it was opened from).
    /// </summary>
    public MessageSummary? Summary(MessageId id) => Cache[id]?.Msg?.Summary;

    /// <summary>
    /// Forgets <paramref name="id"/> (an entry in flight is put back when its
    /// half arrives). For tests; Swift keeps it internal.
    /// </summary>
    public void Evict(MessageId id)
    {
        scope.VerifyAccess();
        Cache.Remove(id);
    }

    /// <summary>Ends the cache's background work; outcomes after it are dropped.</summary>
    public void Dispose() => scope.Dispose();

    // Fetching

    /// <summary>
    /// Runs <c>message.get</c> and <c>message.body</c> for
    /// <paramref name="s"/> (each only when the cache lacks it and no request
    /// is in flight) and calls <paramref name="done"/> on the UI thread after
    /// every answer, with the cache entry so far; a complete entry calls
    /// <paramref name="done"/> at once (message_view.go <c>fetchMessage</c>).
    /// A failed <c>message.get</c> is only logged (the summary headers stay)
    /// and retried the next time; a failed body sets
    /// <see cref="LoadedMessage.Err"/> and is retried the same way.
    /// </summary>
    public void Fetch(MessageSummary s, Action<LoadedMessage> done)
    {
        ArgumentNullException.ThrowIfNull(s);
        ArgumentNullException.ThrowIfNull(done);
        scope.VerifyAccess();
        var id = s.Id;
        var lm = Cache.LoadedFor(id);
        if (lm.Complete)
        {
            done(lm);
            return;
        }
        if (!waiters.TryGetValue(lm, out var list))
        {
            waiters[lm] = list = [];
        }
        list.Add(done);
        if (lm.Msg is null && !lm.Getting)
        {
            lm.Getting = true;
            scope.Perform(Client, API.MessageGet, new MessageGetParams { AccountId = s.AccountId, MessageId = id }, outcome =>
            {
                lm.Getting = false;
                if (outcome.TryGetValue(out var res, out var err))
                {
                    lm.Msg = res.Message;
                }
                else
                {
                    LogGetFailed(logger, err!);
                }
                Settle(id, lm);
            }, RpcTimeouts.Default);
        }
        if (lm.Body is null && !lm.Fetching)
        {
            lm.Fetching = true;
            lm.Err = null; // a retry after a failure
            scope.Perform(Client, API.MessageBody, new MessageBodyParams { AccountId = s.AccountId, MessageId = id }, outcome =>
            {
                lm.Fetching = false;
                if (outcome.TryGetValue(out var res, out var err))
                {
                    lm.Body = res;
                }
                else
                {
                    LogBodyFailed(logger, err!);
                    lm.Err = err;
                }
                Settle(id, lm);
            });
        }
    }

    /// <summary>
    /// Forgets the cached <c>message.get</c> result of <paramref name="s"/>
    /// (unless one is in flight, which then serves) and fetches again; the
    /// body stays cached (outbox.go <c>refetchMessage</c>).
    /// <paramref name="done"/> runs as <see cref="Fetch"/>'s does.
    /// </summary>
    public void Refetch(MessageSummary s, Action<LoadedMessage> done)
    {
        ArgumentNullException.ThrowIfNull(s);
        scope.VerifyAccess();
        if (Cache[s.Id] is { Getting: false } lm)
        {
            lm.Msg = null;
        }
        Fetch(s, done);
    }

    /// <summary>
    /// <see cref="Refetch(MessageSummary, Action{LoadedMessage})"/> for a
    /// message the cache knows the account of (its full message is cached);
    /// nothing happens otherwise.
    /// </summary>
    public void Refetch(MessageId id, Action<LoadedMessage> done)
    {
        scope.VerifyAccess();
        if (Summary(id) is not { } s)
        {
            return;
        }
        Refetch(s, done);
    }

    // Remote images

    /// <summary>
    /// Fetches the body of <paramref name="s"/> again with remote images
    /// allowed for this one call and shows the result wherever the message is
    /// on display (remote.go <c>loadRemoteImages</c>). The daemon does the
    /// fetching; the views only get the inlined pictures. The bar shows the
    /// wait from the click on (<see cref="RemoteBarChanged"/>). A request
    /// already running is left alone and <paramref name="done"/> is not
    /// called. <paramref name="done"/> gets the entry with the new body, or
    /// the error; the error was already toasted and the bar put back
    /// (<see cref="ImagesDone"/>).
    /// </summary>
    public void LoadImages(MessageSummary s, Action<Outcome<LoadedMessage>> done)
    {
        ArgumentNullException.ThrowIfNull(s);
        scope.VerifyAccess();
        if (BeginLoadingImages(s.Id) is not { } lm)
        {
            return;
        }
        FetchRemoteImages(s, lm, done);
    }

    /// <summary>
    /// Marks the entry of <paramref name="id"/> as waiting for its remote
    /// images and redraws the bar (the first step of
    /// <c>loadRemoteImages</c> and <c>trustSender</c>); null when a request is
    /// already running.
    /// </summary>
    public LoadedMessage? BeginLoadingImages(MessageId id)
    {
        scope.VerifyAccess();
        var lm = Cache.LoadedFor(id);
        if (lm.LoadingImages)
        {
            return null;
        }
        lm.LoadingImages = true;
        RefreshRemoteBar(id, lm);
        return lm;
    }

    /// <summary>
    /// The <c>message.body</c> call under allow for a request the bar already
    /// shows as loading (<see cref="LoadedMessage.LoadingImages"/>, set by
    /// <see cref="BeginLoadingImages"/>); it ends the request either way,
    /// with the images on display or the bar back as it was and a toast
    /// (remote.go <c>fetchRemoteImages</c>).
    /// </summary>
    public void FetchRemoteImages(MessageSummary s, LoadedMessage lm, Action<Outcome<LoadedMessage>> done)
    {
        ArgumentNullException.ThrowIfNull(s);
        ArgumentNullException.ThrowIfNull(lm);
        ArgumentNullException.ThrowIfNull(done);
        scope.VerifyAccess();
        var id = s.Id;
        var parameters = new MessageBodyParams { AccountId = s.AccountId, MessageId = id, RemoteContent = RemoteContentPolicy.Allow };
        scope.Perform(Client, API.MessageBody, parameters, outcome =>
        {
            if (!outcome.TryGetValue(out var res, out var err))
            {
                LogImagesFailed(logger, err!);
                toast(RpcErrorText.Text(L10n.T("Loading the images"), err));
                ImagesDone(id, lm);
                done(Outcome.Failure<LoadedMessage>(err!));
                return;
            }
            lm.LoadingImages = false;
            lm.Body = res;
            lm.Err = null;
            if (Cache[id] is null)
            {
                Cache.Store(id, lm);
            }
            scope.Raise(MessageLoaded, this, new MessageCacheEntry(id, lm));
            done(Outcome.Success(lm));
        });
    }

    /// <summary>
    /// Ends a request for the images without a new body: the bar offers them
    /// again (remote.go <c>imagesDone</c>).
    /// </summary>
    public void ImagesDone(MessageId id, LoadedMessage lm)
    {
        ArgumentNullException.ThrowIfNull(lm);
        scope.VerifyAccess();
        lm.LoadingImages = false;
        RefreshRemoteBar(id, lm);
    }

    /// <summary>
    /// Redraws the bar of message <paramref name="id"/> wherever it is on
    /// display and leaves the body alone (remote.go <c>refreshRemoteBar</c>).
    /// </summary>
    public void RefreshRemoteBar(MessageId id, LoadedMessage lm)
    {
        ArgumentNullException.ThrowIfNull(lm);
        scope.VerifyAccess();
        scope.Raise(RemoteBarChanged, this, new MessageCacheEntry(id, lm));
    }

    // Parts and attached messages

    /// <summary>
    /// <c>message.part</c> for one attachment (attachments.go
    /// <c>fetchAttachment</c>): the part may be 16 MiB of base64 on the
    /// socket, hence the long timeout.
    /// </summary>
    public Task<MessagePartResult> FetchAttachmentAsync(
        AccountId accountId, MessageId messageId, string partId, CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(partId);
        var parameters = new MessagePartParams { AccountId = accountId, MessageId = messageId, PartId = partId };
        return Client.CallAsync(API.MessagePart, parameters, RpcTimeouts.Part, cancellationToken);
    }

    /// <summary>
    /// Serves the web view's <c>malachi-cid:</c> pictures through
    /// <c>message.part</c> (remote.go <c>fetchPart</c>): the claimed type and
    /// the bytes; whether the type may be shown is the caller's check
    /// (<c>isImageType</c>).
    /// </summary>
    public async Task<(string ContentType, byte[] Data)> FetchPartAsync(
        AccountId accountId, MessageId messageId, string partId, CancellationToken cancellationToken = default)
    {
        var res = await FetchAttachmentAsync(accountId, messageId, partId, cancellationToken);
        return (res.ContentType, res.Data);
    }

    /// <summary>
    /// <c>message.embedded</c> for one part (embedded.go <c>fetchEmbedded</c>);
    /// the timeout allows for the daemon fetching remote images under allow.
    /// </summary>
    public Task<MessageEmbeddedResult> FetchEmbeddedAsync(
        AccountId accountId, MessageId messageId, string partId, RemoteContentPolicy? remote = null, CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(partId);
        var parameters = new MessageEmbeddedParams { AccountId = accountId, MessageId = messageId, PartId = partId, RemoteContent = remote };
        return Client.CallAsync(API.MessageEmbedded, parameters, RpcTimeouts.Remote, cancellationToken);
    }

    /// <summary>
    /// Runs on the UI thread after one half of <paramref name="lm"/> arrived
    /// (<c>settleLoaded</c>): the entry is put back if it was evicted
    /// meanwhile and the waiters hear about it; once nothing is in flight any
    /// more they are dropped.
    /// </summary>
    private void Settle(MessageId id, LoadedMessage lm)
    {
        if (Cache[id] is null)
        {
            Cache.Store(id, lm);
        }
        // A copy: a waiter may fetch again and add to the list.
        Action<LoadedMessage>[] ws = waiters.TryGetValue(lm, out var list) ? [.. list] : [];
        if (!lm.Getting && !lm.Fetching)
        {
            waiters.Remove(lm);
        }
        foreach (var w in ws)
        {
            scope.Guard(() => w(lm));
        }
        scope.Raise(MessageLoaded, this, new MessageCacheEntry(id, lm));
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "message.get failed")]
    private static partial void LogGetFailed(ILogger logger, Exception error);

    [LoggerMessage(Level = LogLevel.Warning, Message = "message.body failed")]
    private static partial void LogBodyFailed(ILogger logger, Exception error);

    [LoggerMessage(Level = LogLevel.Warning, Message = "message.body (allow) failed")]
    private static partial void LogImagesFailed(ILogger logger, Exception error);
}
