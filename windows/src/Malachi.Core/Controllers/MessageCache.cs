// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/MessageCache.swift; GTK:
// ui/internal/window/message_view.go (fetchMessage, settleLoaded,
// loadedFor, storeLoaded), remote.go (fetchPart, loadRemoteImages,
// fetchRemoteImages, imagesDone, refreshRemoteBar, showLoaded,
// downloadPictures, pictureFailed, lostPicture, reloadPictures), outbox.go
// (refetchMessage), attachments.go (fetchAttachment), embedded.go
// (fetchEmbedded) and download.go (download, beginDownload, endDownload,
// refreshChips, partData, embeddedData).
//
// Every fire-and-forget call goes through ControllerScope.Perform, which
// starts the call after the caller's turn as Swift's Task does
// (docs/windows-port.md §7.2): fetch starts both halves before either can
// settle, so a transport that fails at once (not connected) still finds the
// other half in flight and keeps the waiters for it. Swift keys its waiters
// by ObjectIdentifier; here a dictionary compares the entries by reference.
// The calls that the caller awaits (message.part, message.embedded) are
// made directly. message.download is one task per message that every
// caller awaits: it starts after the caller's turn as well, so that it is
// known as the message's download before it can end, and it ends the
// cache's side (EndDownload) before any caller resumes, as Swift's does.
// The spinner's delay runs on the TimeProvider, so the tests control it.
// The cache has no closed flag, as in Swift: it lives as long as the app.
// Swift's callbacks cannot throw; a view's handler or waiter here can, and
// is isolated (ControllerEvents), so that one view's failure strands
// neither the other views nor the request it came with.

using System;
using System.Collections.Generic;
using System.Runtime.ExceptionServices;
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
    /// <summary>
    /// How long a download runs before the chips show a spinner (download.go,
    /// message_view.go <c>bodySpinnerDelay</c>): a download that finds
    /// nothing missing answers at once and never flashes one.
    /// </summary>
    public static readonly TimeSpan DownloadSpinnerDelay = TimeSpan.FromMilliseconds(400);

    private readonly ControllerScope scope;
    private readonly Action<string> toast;
    private readonly ILogger logger;
    private readonly TimeProvider time;

    // Who wants to hear about the in-flight halves of an entry
    // (loadedMessage.waiters), kept beside the entry so that an entry evicted
    // while in flight keeps its own waiters.
    private readonly Dictionary<LoadedMessage, List<Action<LoadedMessage>>> waiters = new(ReferenceEqualityComparer.Instance);

    // The message.download calls in flight, one per message
    // (Window.downloads): a second request joins the first.
    private readonly Dictionary<MessageId, Task<Message>> downloads = [];

    // The messages whose chips show the download spinner (Window.spinning).
    private readonly HashSet<MessageId> spinning = [];

    /// <summary>A cache over <paramref name="client"/>.</summary>
    /// <param name="client">The transport; calls fail with notConnected until the connection controller reports a connection.</param>
    /// <param name="toast">Shows a transient message (the window's toast overlay).</param>
    /// <param name="logger">Receives methods and codes, never mail data.</param>
    /// <param name="pending">Counts the controller's background work (a tracker of its own when null).</param>
    /// <param name="timeProvider">The clock of the download spinner's delay (the system's when null).</param>
    public MessageCache(
        RpcClient client, Action<string> toast, ILogger<MessageCache>? logger = null, PendingWork? pending = null, TimeProvider? timeProvider = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        ArgumentNullException.ThrowIfNull(toast);
        Client = client;
        this.toast = toast;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        time = timeProvider ?? TimeProvider.System;
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

    /// <summary>
    /// Fired when the attachment chips of a message have to be drawn again:
    /// its download began to show the spinner, or ended (Swift
    /// <c>onChips</c>; download.go <c>refreshChips</c>, the pane and the open
    /// windows). The entry is null when the cache no longer holds the
    /// message.
    /// </summary>
    public event EventHandler<MessageCacheChips>? ChipsChanged;

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
    /// Redraws the bars of message <paramref name="id"/> (the remote-image bar
    /// and the pictures bar) wherever it is on display and leaves the body
    /// alone (remote.go <c>refreshRemoteBar</c>, <c>refreshPicturesBar</c>).
    /// </summary>
    public void RefreshRemoteBar(MessageId id, LoadedMessage lm)
    {
        ArgumentNullException.ThrowIfNull(lm);
        scope.VerifyAccess();
        scope.Raise(RemoteBarChanged, this, new MessageCacheEntry(id, lm));
    }

    // Pictures on the server

    /// <summary>
    /// Downloads the pictures of <paramref name="s"/> kept on the mail server
    /// only (<see cref="MessageBodyResult.RemotePictures"/>) and shows the
    /// message again wherever it is on display (remote.go
    /// <c>downloadPictures</c>): <c>message.download</c> (joining one already
    /// running, <see cref="DownloadAsync"/>, so the chips spin too), then
    /// <c>message.body</c> again under <see cref="RemoteBar.PicturesPolicy"/>,
    /// so remote images loaded for it stay. The bars show the wait from the
    /// click on (<see cref="RemoteBarChanged"/>,
    /// <see cref="LoadedMessage.LoadingPictures"/>). A request already running
    /// is left alone and <paramref name="done"/> is not called. A failure of
    /// either call is toasted through <paramref name="say"/> (the window the
    /// click came from; the cache's own when null) and the bar offers the
    /// pictures again. <paramref name="done"/> gets the entry with the new
    /// body, or the error.
    /// </summary>
    public void DownloadPictures(MessageSummary s, Action<string>? say, Action<Outcome<LoadedMessage>> done)
    {
        ArgumentNullException.ThrowIfNull(s);
        ArgumentNullException.ThrowIfNull(done);
        scope.VerifyAccess();
        var id = s.Id;
        var lm = Cache.LoadedFor(id);
        if (lm.LoadingPictures)
        {
            return;
        }
        lm.LoadingPictures = true;
        RefreshRemoteBar(id, lm);
        scope.Run(ct => DownloadPicturesAsync(s, lm, say ?? toast, done, ct));
    }

    // downloadPictures after the click: the download, the body again, and
    // the outcome on display.
    private async Task DownloadPicturesAsync(
        MessageSummary s, LoadedMessage lm, Action<string> say, Action<Outcome<LoadedMessage>> done, CancellationToken ct)
    {
        var id = s.Id;
        Outcome<MessageBodyResult> outcome;
        try
        {
            await DownloadAsync(s.AccountId, id);
            // Back on the UI thread after the download: the policy of the
            // body on display now.
            var parameters = new MessageBodyParams { AccountId = s.AccountId, MessageId = id, RemoteContent = RemoteBar.PicturesPolicy(lm) };
            outcome = Outcome.Success(await Client.CallAsync(API.MessageBody, parameters, API.MessageBody.Timeout, ct));
        }
#pragma warning disable CA1031 // Every failure of either call is the request's outcome, toasted below.
        catch (Exception e)
#pragma warning restore CA1031
        {
            outcome = Outcome.Failure<MessageBodyResult>(e);
        }
        if (scope.IsClosed)
        {
            return;
        }
        lm.LoadingPictures = false;
        if (!outcome.TryGetValue(out var res, out var err))
        {
            LogPicturesFailed(logger, err!);
            say(RpcErrorText.Text(L10n.T("Downloading the pictures"), err));
            RefreshRemoteBar(id, lm);
            done(Outcome.Failure<LoadedMessage>(err!));
            return;
        }
        lm.Body = res;
        lm.Err = null;
        if (Cache[id] is null)
        {
            Cache.Store(id, lm);
        }
        scope.Raise(MessageLoaded, this, new MessageCacheEntry(id, lm));
        done(Outcome.Success(lm));
    }

    // The count going out of date. A body cached while the daemon held the
    // message in memory counts no picture on the server; once the daemon has
    // dropped that copy (30 minutes unused, its memory cap, the switch turned
    // off, a restart) the same body shown again asks for pictures that
    // message.part answers partNotDownloaded for, and without a new count
    // there would be no bar to get them back. The first such answer for a
    // picture the body lists asks for the body again (store and memory only,
    // never the mail server), whose count brings the bar back. The other way
    // round, a download of the message for anything else (a chip, a reply, a
    // forward) asks again for a body that counts pictures on the server, so
    // that they show and the bar goes (EndDownload).

    /// <summary>
    /// Asks for the body of message <paramref name="id"/> again when picture
    /// <paramref name="partId"/>, which the cached body counts as here, turned
    /// out to be on the mail server only (remote.go <c>lostPicture</c>,
    /// <see cref="RemoteBar.RecheckPictures"/>).
    /// </summary>
    internal void LostPicture(AccountId accountId, MessageId id, string partId)
    {
        scope.VerifyAccess();
        if (Cache[id] is not { } lm || !RemoteBar.RecheckPictures(lm, partId))
        {
            return;
        }
        lm.PicturesRechecked = true;
        ReloadPictures(accountId, id, lm);
    }

    // remote.go reloadPictures: the body of message id again, under the
    // policy of the body on display (remote images the user loaded stay),
    // shown wherever the message is on display. The daemon answers from its
    // store and memory. A body that replaced the one on display meanwhile,
    // or Download Pictures started meanwhile, wins over the answer; a failure
    // is only logged and the body on display stays.
    private void ReloadPictures(AccountId accountId, MessageId id, LoadedMessage lm)
    {
        var shown = lm.Body;
        var parameters = new MessageBodyParams { AccountId = accountId, MessageId = id, RemoteContent = RemoteBar.PicturesPolicy(lm) };
        scope.Perform(Client, API.MessageBody, parameters, outcome =>
        {
            if (!outcome.TryGetValue(out var res, out var err))
            {
                LogReloadFailed(logger, err!);
                return;
            }
            if (!ReferenceEquals(lm.Body, shown) || lm.LoadingPictures)
            {
                return;
            }
            lm.Body = res;
            lm.Err = null;
            if (Cache[id] is null)
            {
                Cache.Store(id, lm);
            }
            scope.Raise(MessageLoaded, this, new MessageCacheEntry(id, lm));
        });
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
    /// (<c>isImageType</c>). A picture the daemon answers partNotDownloaded
    /// for goes to <see cref="LostPicture"/> on the UI thread before the
    /// error is thrown (remote.go <c>pictureFailed</c>).
    /// </summary>
    public async Task<(string ContentType, byte[] Data)> FetchPartAsync(
        AccountId accountId, MessageId messageId, string partId, CancellationToken cancellationToken = default)
    {
        try
        {
            var res = await FetchAttachmentAsync(accountId, messageId, partId, cancellationToken);
            return (res.ContentType, res.Data);
        }
        catch (RpcException e) when (Download.IsPartNotDownloaded(e))
        {
            OnUiThread(() => LostPicture(accountId, messageId, partId));
            throw;
        }
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
    /// The data of attachment <paramref name="a"/> of message
    /// <paramref name="messageId"/> (download.go <c>partData</c>):
    /// <c>message.part</c>, after <c>message.download</c> when the chip showed
    /// the part on the server (<paramref name="onServer"/>), or once when
    /// <c>message.part</c> answers partNotDownloaded
    /// (<see cref="Download.WithDownloadAsync{T}"/>). The result's
    /// <c>PartId</c> is the part actually fetched. On the UI thread.
    /// </summary>
    public Task<MessagePartResult> PartDataAsync(AccountId accountId, MessageId messageId, Attachment a, bool onServer)
    {
        ArgumentNullException.ThrowIfNull(a);
        scope.VerifyAccess();
        return Download.WithDownloadAsync(
            a, onServer, p => FetchAttachmentAsync(accountId, messageId, p.PartId), () => DownloadedAsync(accountId, messageId));
    }

    /// <summary>
    /// The attached message <paramref name="a"/> of message
    /// <paramref name="messageId"/> rendered by the daemon (download.go
    /// <c>embeddedData</c>): <c>message.embedded</c> under
    /// <paramref name="policy"/>, downloading the containing message first or
    /// after a partNotDownloaded, as <see cref="PartDataAsync"/>. On the UI
    /// thread.
    /// </summary>
    public Task<MessageEmbeddedResult> EmbeddedDataAsync(
        AccountId accountId, MessageId messageId, Attachment a, bool onServer, RemoteContentPolicy? policy = null)
    {
        ArgumentNullException.ThrowIfNull(a);
        scope.VerifyAccess();
        return Download.WithDownloadAsync(
            a, onServer, p => FetchEmbeddedAsync(accountId, messageId, p.PartId, policy), () => DownloadedAsync(accountId, messageId));
    }

    // Downloads

    /// <summary>
    /// Whether the chips of message <paramref name="id"/> show the download
    /// spinner (download.go <c>spinning</c>): a download of it has run for
    /// <see cref="DownloadSpinnerDelay"/> and not ended yet.
    /// </summary>
    public bool ShowsDownload(MessageId id) => spinning.Contains(id);

    /// <summary>Whether a download of message <paramref name="id"/> is running.</summary>
    public bool Downloading(MessageId id) => downloads.ContainsKey(id);

    /// <summary>
    /// <c>message.download</c> for message <paramref name="id"/> (download.go
    /// <c>download</c>): makes the parts kept on the mail server, and a body
    /// not downloaded yet, available, and answers the message as the daemon
    /// reports it afterwards. One call per message: a request while one runs
    /// gets the same task. The chips show a spinner once the call has taken
    /// <see cref="DownloadSpinnerDelay"/> (<see cref="ShowsDownload"/>,
    /// <see cref="ChipsChanged"/>); when it ends the cached message is
    /// replaced with the answer (Microsoft 365 may move part ids), a body that
    /// was not fetched is dropped and fetched again, one that counts pictures
    /// on the mail server is asked for again, and the chips are drawn again.
    /// A failure is thrown to every caller, for its own toast; the daemon
    /// finishes a download its caller gave up on, so the call has the long
    /// <see cref="RpcTimeouts.Download"/> and no caller's cancellation. On the
    /// UI thread.
    /// </summary>
    public Task<Message> DownloadAsync(AccountId accountId, MessageId id)
    {
        scope.VerifyAccess();
        if (downloads.TryGetValue(id, out var running))
        {
            return running;
        }
        var task = RunDownloadAsync(accountId, id);
        downloads[id] = task;
        // Counted until it ends; its failure is every caller's to say, not a
        // fault of the cache's background work.
        scope.Pending.Track(task.ContinueWith(
            static t => _ = t.Exception, CancellationToken.None, TaskContinuationOptions.ExecuteSynchronously, TaskScheduler.Default));
        BeginDownload(id, task);
        return task;
    }

    // DownloadAsync as the answer WithDownloadAsync wants.
    private async Task<Message?> DownloadedAsync(AccountId accountId, MessageId id) => await DownloadAsync(accountId, id);

    // The call of one download: after the caller's turn, so that it is in
    // downloads before it can end, and the cache's side done before any
    // caller resumes, so every one of them finds the cache as the download
    // left it.
    private async Task<Message> RunDownloadAsync(AccountId accountId, MessageId id)
    {
        await Task.Yield();
        Outcome<Message> outcome;
        try
        {
            var parameters = new MessageDownloadParams { AccountId = accountId, MessageId = id };
            outcome = Outcome.Success((await Client.CallAsync(API.MessageDownload, parameters, RpcTimeouts.Download, scope.Lifetime)).Message);
        }
#pragma warning disable CA1031 // Every failure is the download's outcome, thrown to every caller below.
        catch (Exception e)
#pragma warning restore CA1031
        {
            outcome = Outcome.Failure<Message>(e);
        }
        EndDownload(accountId, id, outcome);
        if (!outcome.TryGetValue(out var m, out var err))
        {
            ExceptionDispatchInfo.Throw(err!);
        }
        return m;
    }

    // download.go beginDownload: the spinner on the chips of id once task
    // has run for the delay and is still the message's download.
    private void BeginDownload(MessageId id, Task<Message> task) => scope.RunDetached(async ct =>
    {
        await Task.Delay(DownloadSpinnerDelay, time, ct);
        if (!downloads.TryGetValue(id, out var running) || running != task || !spinning.Add(id))
        {
            return;
        }
        scope.Raise(ChipsChanged, this, new MessageCacheChips(id, Cache[id]));
    });

    // download.go endDownload: no spinner, the cached message replaced with
    // the downloaded one (under neverStoreAttachments its parts stay remote,
    // held in the daemon's memory for a while, and the next action downloads
    // again), a body that was not fetched dropped and fetched again (every
    // view re-renders through MessageLoaded), and so is one that counts
    // pictures on the mail server only (RemoteBar.ReloadAfterDownload), which
    // the daemon holds now: they show and the pictures bar goes, whatever the
    // download was for. The chips are drawn again.
    private void EndDownload(AccountId accountId, MessageId id, Outcome<Message> outcome)
    {
        downloads.Remove(id);
        spinning.Remove(id);
        if (scope.IsClosed)
        {
            return;
        }
        var lm = Cache[id];
        if (!outcome.TryGetValue(out var m, out var err))
        {
            LogDownloadFailed(logger, err!);
        }
        else if (lm is not null)
        {
            lm.Msg = m;
            // Pictures that go missing from now on may ask for the body once
            // more (RemoteBar.RecheckPictures).
            lm.PicturesRechecked = false;
            if (lm.Body is { } b && b.BodyState != BodyState.Fetched && !lm.Fetching)
            {
                lm.Body = null;
                lm.Err = null;
                Fetch(m.Summary, static _ => { });
            }
            else if (RemoteBar.ReloadAfterDownload(lm))
            {
                ReloadPictures(accountId, id, lm);
            }
        }
        scope.Raise(ChipsChanged, this, new MessageCacheChips(id, lm));
    }

    // Runs step on the cache's (UI) thread: at once there, posted from
    // elsewhere (remote.go pictureFailed's glib.IdleAdd).
    private void OnUiThread(Action step)
    {
        if (scope.Thread.CheckAccess())
        {
            scope.Guard(step);
        }
        else if (scope.Thread.Context is { } context)
        {
            context.Post(_ => scope.Guard(step), null);
        }
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

    [LoggerMessage(Level = LogLevel.Warning, Message = "message.download failed")]
    private static partial void LogDownloadFailed(ILogger logger, Exception error);

    [LoggerMessage(Level = LogLevel.Warning, Message = "download pictures failed")]
    private static partial void LogPicturesFailed(ILogger logger, Exception error);

    [LoggerMessage(Level = LogLevel.Warning, Message = "message.body (pictures again) failed")]
    private static partial void LogReloadFailed(ILogger logger, Exception error);
}
