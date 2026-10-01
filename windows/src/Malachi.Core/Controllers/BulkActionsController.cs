// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/window/bulk.go (unsubscribe, bulkDialog, openBulkPage,
// callUnsubscribe, bulkFallback, refreshBulk); macOS keeps the same flow in
// the message actions (Actions/MessageActionsController.swift) over
// macos/Sources/MalachiCore/Bulk/BulkMail.swift.
//
// The Swift callbacks are events and hooks: the redraw of every view
// showing a message is Changed (the entry as it is now: its Unsubscribing
// flag and its message's offer), the confirmation dialog the Confirm hook
// (Controllers never create a dialog, docs/windows-port.md §7.5) and the
// browser OpenPage. The request goes through the scope and is dropped once
// the window closed (the daemon has the message either way, as GTK's idle
// callback finds the window gone).

using System;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Bulk;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Model;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>
/// The Unsubscribe button of the strip above a bulk message, without the
/// widgets: <see cref="Unsubscribe"/> asks the user (<see cref="Confirm"/>;
/// nothing happens without the answer), then opens the sender's page for a
/// web-page offer or calls <c>message.unsubscribe</c>, and shows what came
/// of it: the cached message's offer turns into "Unsubscribed on …"
/// (<see cref="BulkMail.Applied"/>), a queued request toasts, a page the
/// daemon could not verify a one-click request for is offered in a dialog.
/// Every view showing the message follows <see cref="Changed"/>.
/// </summary>
/// <remarks>
/// A request runs at most once per message at a time
/// (<see cref="LoadedMessage.Unsubscribing"/>, which the strip shows as a
/// waiting button). Its answer goes to the entry the cache holds for the
/// message when it arrives, which a newer <c>message.get</c> may have
/// replaced. Create it, and call it, on the UI thread; every event is
/// raised there.
/// </remarks>
public sealed partial class BulkActionsController : IDisposable
{
    private readonly ControllerScope scope;
    private readonly RpcClient client;
    private readonly IReaderCache cache;
    private readonly Action<string> toast;
    private readonly TimeProvider time;
    private readonly ILogger logger;

    /// <summary>The controller of a window over <paramref name="client"/>, on the calling (UI) thread.</summary>
    /// <param name="client">The daemon.</param>
    /// <param name="cache">The loaded messages: the entry whose offer is acted on.</param>
    /// <param name="toast">Shows a transient message where nothing says better (the main window's overlay).</param>
    /// <param name="logger">Receives method names and error codes, never mail content.</param>
    /// <param name="pending">Counts the background work; one of its own when null.</param>
    /// <param name="timeProvider">The time an offer is marked unsubscribed at when the daemon gave none.</param>
    public BulkActionsController(
        RpcClient client,
        IReaderCache cache,
        Action<string> toast,
        ILogger<BulkActionsController>? logger = null,
        PendingWork? pending = null,
        TimeProvider? timeProvider = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        ArgumentNullException.ThrowIfNull(cache);
        ArgumentNullException.ThrowIfNull(toast);
        this.client = client;
        this.cache = cache;
        this.toast = toast;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        time = timeProvider ?? TimeProvider.System;
        scope = new ControllerScope(pending);
    }

    /// <summary>
    /// The confirmation dialog (bulk.go <c>bulkDialog</c>): the window it
    /// goes on (opaque to Core; null is the main window, which the hook also
    /// takes for a window that has closed since the click, whose answer asks
    /// for the sender's page) and the texts, which
    /// are plain text; a non-destructive question whose confirming button is
    /// the default. True when confirmed. Without it nothing is done.
    /// </summary>
    public Func<object?, UnsubscribeConfirmation, Task<bool>>? Confirm { get; set; }

    /// <summary>Opens the sender's page in the browser (bulk.go <c>openBulkPage</c>), given an https address <see cref="BulkMail.OpenableUrl"/> passed.</summary>
    public Func<object?, string, Task>? OpenPage { get; set; }

    /// <summary>
    /// The entry of a message changed (a request started or ended, or its
    /// offer was applied): every view showing it redraws its strip
    /// (bulk.go <c>refreshBulk</c>).
    /// </summary>
    public event EventHandler<MessageCacheEntry>? Changed;

    /// <summary>Whether a request runs for message <paramref name="id"/> right now.</summary>
    public bool IsBusy(MessageId id) => cache.Loaded(id)?.Unsubscribing == true;

    /// <summary>
    /// The strip's button for message <paramref name="id"/> (bulk.go
    /// <c>unsubscribe</c>): the confirmation first, then the page for a
    /// web-page offer, otherwise <c>message.unsubscribe</c>.
    /// <paramref name="parent"/> hosts the dialogs; <paramref name="say"/>
    /// shows toasts in the window the click came from (the controller's own
    /// toast when null). Nothing happens for a message whose full headers are
    /// not loaded, without an offer, already unsubscribed from, or with a
    /// request running.
    /// </summary>
    public void Unsubscribe(MessageId id, object? parent, Action<string>? say = null)
    {
        scope.VerifyAccess();
        if (cache.Loaded(id) is not { Msg: { } msg } lm || lm.Unsubscribing || msg.Unsubscribe is not { UnsubscribedAt: null })
        {
            return;
        }
        var m = BulkReading.MessageFor(msg.Summary, lm);
        if (BulkMail.Confirm(m) is not { } conf || m.Unsubscribe is not { } offer)
        {
            return;
        }
        var request = new Request(parent, m.Summary.AccountId, id, lm, say ?? toast);
        scope.Run(ct => AskAsync(request, conf, offer, ct));
    }

    /// <summary>The window went away: late replies are dropped.</summary>
    public void Close() => scope.Close();

    /// <inheritdoc/>
    public void Dispose() => Close();

    // One click on the button: where it came from and what it is about.
    private sealed record Request(object? Parent, AccountId Account, MessageId Id, LoadedMessage Entry, Action<string> Say);

    // bulkDialog: the question, then what the offer's method says.
    private async Task AskAsync(Request r, UnsubscribeConfirmation conf, UnsubscribeOffer offer, CancellationToken cancellationToken)
    {
        if (!await ConfirmedAsync(r.Parent, conf) || scope.IsClosed || cancellationToken.IsCancellationRequested)
        {
            return;
        }
        if (offer.Method.Value == UnsubscribeMethod.Url)
        {
            await OpenAsync(r.Parent, offer.Url);
            return;
        }
        if (r.Entry.Unsubscribing)
        {
            return; // another click was confirmed first
        }
        Call(r);
    }

    // Shows the dialog; a dialog that cannot be shown (another one is open)
    // or no hook at all means no: nothing is sent unasked.
    private async Task<bool> ConfirmedAsync(object? parent, UnsubscribeConfirmation conf)
    {
        if (Confirm is not { } confirm)
        {
            LogNoConfirmationHook(logger);
            return false;
        }
        try
        {
            return await confirm(parent, conf);
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            LogConfirmationFailed(logger, e.GetType().Name);
            return false;
        }
    }

    // openBulkPage: https only, never anything else the mail carries.
    private async Task OpenAsync(object? parent, string? raw)
    {
        if (BulkMail.OpenableUrl(raw) is not { } uri || OpenPage is not { } open)
        {
            return;
        }
        try
        {
            await open(parent, uri);
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            // The exception may name the address: its type only.
            LogOpenFailed(logger, e.GetType().Name);
        }
    }

    // callUnsubscribe: message.unsubscribe, and what came of it on display.
    private void Call(Request r)
    {
        r.Entry.Unsubscribing = true;
        scope.Raise(Changed, this, new MessageCacheEntry(r.Id, r.Entry));
        var parameters = new MessageUnsubscribeParams { AccountId = r.Account, MessageId = r.Id };
        scope.Perform(client, API.MessageUnsubscribe, parameters, outcome =>
        {
            r.Entry.Unsubscribing = false;
            // A newer message.get may have replaced the entry while the
            // request ran (the cache let the old one go): the answer is about
            // the message, so it goes to the entry the views read now, whose
            // button waited as well.
            var entry = cache.Loaded(r.Id) ?? r.Entry;
            entry.Unsubscribing = false;
            if (!outcome.TryGetValue(out var res, out var error))
            {
                LogFailed(logger, RpcErrorText.DaemonError(error)?.Code.Name ?? error!.GetType().Name);
                r.Say(RpcErrorText.Text(BulkMail.ErrorWhat(), error));
                scope.Raise(Changed, this, new MessageCacheEntry(r.Id, entry));
                return;
            }
            switch (res.Outcome.Value)
            {
                case UnsubscribeOutcome.Unsubscribed or UnsubscribeOutcome.Queued:
                    if (entry.Msg is { } msg)
                    {
                        entry.Msg = msg with { Unsubscribe = BulkMail.Applied(msg.Unsubscribe, res, time) };
                    }
                    if (res.Outcome.Value == UnsubscribeOutcome.Queued)
                    {
                        r.Say(BulkMail.Queued());
                    }
                    break;
                case UnsubscribeOutcome.OpenUrl:
                    var shown = r with { Entry = entry };
                    scope.Run(ct => FallbackAsync(shown, res, ct));
                    break;
                default:
                    break;
            }
            scope.Raise(Changed, this, new MessageCacheEntry(r.Id, entry));
        });
    }

    // bulkFallback: the daemon sent nothing and offers the sender's page
    // instead (a one-click offer that could not be verified, or an answer
    // that only names a page).
    private async Task FallbackAsync(Request r, MessageUnsubscribeResult res, CancellationToken cancellationToken)
    {
        if (BulkMail.OpenableUrl(res.Url) is null)
        {
            return;
        }
        var conf = BulkMail.Fallback(r.Entry.Msg, res);
        if (await ConfirmedAsync(r.Parent, conf) && !scope.IsClosed && !cancellationToken.IsCancellationRequested)
        {
            await OpenAsync(r.Parent, res.Url);
        }
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "message.unsubscribe failed: {Code}")]
    private static partial void LogFailed(ILogger logger, string code);

    [LoggerMessage(Level = LogLevel.Error, Message = "no confirmation hook is installed; nothing was sent")]
    private static partial void LogNoConfirmationHook(ILogger logger);

    [LoggerMessage(Level = LogLevel.Warning, Message = "the confirmation failed; nothing was sent: {Kind}")]
    private static partial void LogConfirmationFailed(ILogger logger, string kind);

    [LoggerMessage(Level = LogLevel.Warning, Message = "open the unsubscribe page failed: {Kind}")]
    private static partial void LogOpenFailed(ILogger logger, string kind);
}
