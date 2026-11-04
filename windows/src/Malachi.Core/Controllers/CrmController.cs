// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/CRMController.swift
// (CRMState, CRMDialogSession, CRMController, CRM.sampleLookup); GTK: the
// window's CRM actions of docs/eway.md §2.6 (window/crm_actions.go, which
// T7 writes; the rules are ui/internal/crm).
//
// The CRM actions of one window: the main window acts on the selection, a
// message window on its message (Subjects). The button and the menus follow
// from them (Button, Offer); Save runs crm.lookup into the dialog (a
// CrmDialogSession the window shows as a ContentDialog), then crm.save in
// the background with the partNotDownloaded round (message.download for
// each message the error names, then crm.save again, at most twice); Remove
// asks first; Open opens the daemon's URL. The messages a call runs for are
// busy in the shared CrmState, so every window's button reads Saving
// meanwhile; the outcome is a toast.
//
// Windows: Swift's closures present, toast and openURL are the events
// PresentRequested and ToastRequested and the injected ILauncher
// (docs/windows-port.md §10); confirm stays a hook (Confirm). The search's
// pause in typing runs on the injected TimeProvider. A save or removal ends
// its busy mark whatever the window does meanwhile (Swift skips the end
// when the window's controller is gone, which would leave the messages
// Saving for ever), and its call is sent even when the window closes right
// after (ControllerScope.PerformPastClose).

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Crm;
using Malachi.Core.I18n;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Model;
using Malachi.Core.Platform;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>
/// What the application knows of eWay-CRM once (docs/eway.md §2.7): the
/// accounts (the first enabled eway account is the one used, D11), the
/// messages a save or removal runs for (<see cref="CrmInFlight"/>), and for
/// a few seconds the record a finished call reported, so that the button
/// turns before the daemon's notify.messagesChanged has refreshed the list
/// and the message (the daemon's record is the truth; this only bridges the
/// gap). Every window's <see cref="CrmController"/> shares it.
/// </summary>
/// <remarks>Create and use it on the UI thread.</remarks>
public sealed class CrmState
{
    /// <summary>How long a call's own answer outranks the list and the cache.</summary>
    public static readonly TimeSpan OverrideTime = TimeSpan.FromSeconds(10);

    private readonly Dictionary<CrmKey, (CrmRecord? Record, DateTimeOffset Until)> overrides = [];

    /// <summary>A state over the given clock (the system's when null).</summary>
    public CrmState(TimeProvider? time = null) => Time = time ?? TimeProvider.System;

    /// <summary>A call started or ended: the windows refresh their CRM controls.</summary>
    public event EventHandler? Changed;

    /// <summary>The accounts of account.list (the mailbox model's).</summary>
    public Func<IReadOnlyList<Account>> Accounts { get; set; } = () => [];

    /// <summary>The role of the folder a message lies in.</summary>
    public Func<MessageSummary, FolderRole> RoleOf { get; set; } = _ => FolderRole.None;

    /// <summary>The full message (<c>message.get</c>) when the cache holds it.</summary>
    public Func<MessageId, Message?> Loaded { get; set; } = _ => null;

    /// <summary>The clock of the overrides.</summary>
    public TimeProvider Time { get; }

    /// <summary>The messages a call runs for.</summary>
    public CrmInFlight InFlight { get; } = new();

    /// <summary>The eWay-CRM account the client saves to (<see cref="CrmRules.Available"/>).</summary>
    public Account? Account => CrmRules.Available(Accounts());

    /// <summary>The message's record in the account used: a call's own answer for a few seconds, else the loaded message's.</summary>
    public CrmRecord? Record(MessageSummary s)
    {
        ArgumentNullException.ThrowIfNull(s);
        if (overrides.TryGetValue(new CrmKey(s.AccountId, s.Id), out var o) && o.Until > Time.GetUtcNow())
        {
            return o.Record;
        }
        return CrmRules.RecordOf(Loaded(s.Id), Account?.Id);
    }

    /// <summary><see cref="CrmRules.IsSaved"/> with a call's own answer first.</summary>
    public bool IsSaved(MessageSummary s)
    {
        ArgumentNullException.ThrowIfNull(s);
        if (overrides.TryGetValue(new CrmKey(s.AccountId, s.Id), out var o) && o.Until > Time.GetUtcNow())
        {
            return o.Record is not null;
        }
        return CrmRules.IsSaved(s, Loaded(s.Id), Account?.Id);
    }

    /// <summary>The rule's row of a message; null when its account is unknown.</summary>
    public CrmRow? Row(MessageSummary s, bool conversation = false)
    {
        ArgumentNullException.ThrowIfNull(s);
        var a = Accounts().FirstOrDefault(x => x.Id == s.AccountId);
        return a is null ? null : new CrmRow(a, RoleOf(s), Saved: IsSaved(s), Conversation: conversation);
    }

    /// <summary>
    /// <see cref="CrmRules.OfferFor"/> of the messages (the selected rows'
    /// shown messages); nothing when one of them has an unknown account.
    /// <paramref name="conversations"/> are those shown by a conversation's row.
    /// </summary>
    public CrmOffer Offer(IReadOnlyList<MessageSummary> messages, IReadOnlySet<MessageId>? conversations = null)
    {
        ArgumentNullException.ThrowIfNull(messages);
        var rows = new List<CrmRow>(messages.Count);
        foreach (var m in messages)
        {
            if (Row(m, conversations?.Contains(m.Id) == true) is not { } r)
            {
                return default;
            }
            rows.Add(r);
        }
        return CrmRules.OfferFor(Accounts(), rows);
    }

    /// <summary>
    /// The button of the messages: one message by
    /// <see cref="CrmRules.ButtonStateFor"/>; for several the Save button
    /// with their count ("Save 3 Messages to eWay-CRM…", as the context menu
    /// has it), Saving while any of them is busy.
    /// </summary>
    public CrmButton Button(IReadOnlyList<MessageSummary> messages, IReadOnlySet<MessageId>? conversations = null)
    {
        ArgumentNullException.ThrowIfNull(messages);
        var busy = messages.Any(m => InFlight.Has(m.AccountId, m.Id));
        if (messages.Count == 1 && Row(messages[0], conversations?.Contains(messages[0].Id) == true) is { } r)
        {
            return CrmText.ButtonFor(CrmRules.ButtonStateFor(Accounts(), r, busy));
        }
        var o = Offer(messages, conversations);
        if (!o.Shown)
        {
            return CrmText.ButtonFor(CrmButtonState.Hidden);
        }
        var b = CrmText.ButtonFor(busy ? CrmButtonState.Saving : CrmButtonState.NotSaved);
        var label = CrmText.SaveMenuLabel(o.Count);
        return b with { Label = label, Tooltip = busy ? b.Tooltip : label, Enabled = o.Enabled && !busy };
    }

    /// <summary>Tells the windows to refresh their CRM controls (an account or a message changed).</summary>
    public void NotifyChanged() => Changed?.Invoke(this, EventArgs.Empty);

    /// <summary>Marks the messages busy (a save or removal starts) and tells the windows.</summary>
    public void Begin(AccountId account, IReadOnlyList<MessageId> ids)
    {
        InFlight.Begin(account, ids);
        NotifyChanged();
    }

    /// <summary>Releases what <see cref="Begin"/> marked and tells the windows.</summary>
    public void End(AccountId account, IReadOnlyList<MessageId> ids)
    {
        InFlight.End(account, ids);
        NotifyChanged();
    }

    // Remembers what a call reported for the message (null: not saved).
    internal void SetOverride(AccountId account, MessageId id, CrmRecord? record)
    {
        var now = Time.GetUtcNow();
        overrides[new CrmKey(account, id)] = (record, now + OverrideTime);
        foreach (var k in overrides.Where(o => o.Value.Until <= now).Select(o => o.Key).ToList())
        {
            overrides.Remove(k);
        }
    }
}

/// <summary>
/// One open Save to eWay-CRM dialog (docs/eway.md §2.2): crm.lookup while it
/// shows a spinner, then the <see cref="CrmDialog"/> over its answer with the
/// search field (crm.search after a pause in typing, from 2 bytes), Save and
/// Cancel. Save hands the choice to the controller, which runs crm.save in
/// the background, and closes the dialog.
/// </summary>
/// <remarks>Use it on the UI thread.</remarks>
public sealed class CrmDialogSession
{
    /// <summary>How long typing pauses before the search field asks the CRM.</summary>
    public static readonly TimeSpan SearchDelay = TimeSpan.FromMilliseconds(300);

    private readonly CrmController? controller;
    private CancellationTokenSource? searchStop;
    private int searchGeneration;

    internal CrmDialogSession(CrmController? controller, AccountId messageAccount, IReadOnlyList<MessageId> messageIds, string summary)
    {
        this.controller = controller;
        MessageAccount = messageAccount;
        MessageIds = messageIds;
        Summary = summary;
    }

    /// <summary>Anything below changed: the view draws the dialog again.</summary>
    public event EventHandler? Changed;

    /// <summary>The session ended (saved or cancelled): the dialog closes.</summary>
    public event EventHandler? Closed;

    /// <summary>Where the session is.</summary>
    public enum SessionPhase
    {
        /// <summary>crm.lookup runs.</summary>
        Loading,

        /// <summary><see cref="Dialog"/> is set.</summary>
        Ready,

        /// <summary>crm.lookup failed; <see cref="Failure"/> says why (only Cancel is left).</summary>
        Failed,
    }

    /// <summary>Where the session is.</summary>
    public SessionPhase Phase { get; private set; } = SessionPhase.Loading;

    /// <summary>The sentence of a failed lookup; null otherwise.</summary>
    public string? Failure { get; private set; }

    /// <summary>The dialog's model once crm.lookup answered.</summary>
    public CrmDialog? Dialog { get; private set; }

    /// <summary>The mail account of the messages the dialog saves.</summary>
    public AccountId MessageAccount { get; }

    /// <summary>The messages the dialog saves.</summary>
    public IReadOnlyList<MessageId> MessageIds { get; }

    /// <summary>
    /// Plain text: the subject and sender of the first message, with the
    /// number of messages when there are several ("subject · sender · 3
    /// messages").
    /// </summary>
    public string Summary { get; }

    /// <summary>A crm.search runs.</summary>
    public bool Searching { get; private set; }

    /// <summary>Why the last search failed; null when it did not.</summary>
    public string? SearchProblem { get; private set; }

    /// <summary>The session ended (saved or cancelled).</summary>
    public bool Finished { get; private set; }

    /// <summary>The fixed texts for the dialog in its current state.</summary>
    public CrmDialogStrings Texts => CrmText.DialogTexts(Dialog?.Editing ?? false);

    /// <summary>The messages and items Save would send now; null when it cannot.</summary>
    public CrmSaveParams? PendingSave => Dialog?.SaveParams(MessageAccount, MessageIds);

    /// <summary>
    /// DEVELOPMENT AID (Swift <c>CRMDialogSession.sample()</c>): a session
    /// over invented suggestions without a daemon; Save and Cancel only close
    /// it, the search finds nothing.
    /// </summary>
    public static CrmDialogSession Sample()
    {
        var s = new CrmDialogSession(null, "sample", ["m_sample"], "Offer for the fleet renewal · Jan Novák");
        s.Loaded(Outcome.Success(SampleLookup()));
        return s;
    }

    /// <summary>Invented suggestions for the dialog's look (fictional people and companies).</summary>
    public static CrmLookupResult SampleLookup()
    {
        static string G(int n) => $"00000000-0000-4000-8000-{n:D12}";
        static CrmItem Item(string kind, int n, string name, string? detail, string reason, bool superior = false) => new()
        {
            Kind = kind,
            ItemGuid = G(n),
            Name = name,
            Detail = detail,
            Reason = reason,
            Superior = superior,
        };
        return new CrmLookupResult
        {
            AccountId = "sample",
            Saved = [new CrmSavedMessage { MessageId = "m_sample" }],
            Suggestions =
            [
                Item(CrmItemKind.Contact, 1, "Novák, Jan", "jan.novak@acme.example", CrmReason.Address),
                Item(CrmItemKind.Contact, 2, "Dvořáková, Jana", "jana@acme.example", CrmReason.Address),
                Item(CrmItemKind.Company, 3, "Acme s.r.o.", "acme.example", CrmReason.Domain),
                Item(CrmItemKind.Company, 4, "Acme Holding a.s.", null, CrmReason.Contact),
                Item(CrmItemKind.Deal, 5, "Fleet renewal 2027", "Open", CrmReason.Contact, superior: true),
                Item(CrmItemKind.Deal, 6, "Service contract", "Open", CrmReason.Company),
                Item(CrmItemKind.Project, 7, "Rollout Brno", null, CrmReason.Company),
            ],
            Partial = true,
        };
    }

    /// <summary>An item's check box; false when refused (the view unchecks it).</summary>
    public bool Toggle(CrmItemRef r, bool on)
    {
        var changed = Dialog?.Toggle(r, on) ?? false;
        RaiseChanged();
        return changed;
    }

    /// <summary>The Superior Item marker moved to <paramref name="r"/>.</summary>
    public bool SetSuperior(CrmItemRef r)
    {
        var changed = Dialog?.SetSuperior(r) ?? false;
        RaiseChanged();
        return changed;
    }

    /// <summary>The Private check box.</summary>
    public void SetPrivate(bool on)
    {
        if (Dialog is { } d)
        {
            d.Private = on;
        }
        RaiseChanged();
    }

    /// <summary>
    /// The search field as typed: crm.search once typing pauses
    /// (<see cref="SearchDelay"/>), nothing below 2 bytes; the results join
    /// the groups unchecked (<see cref="CrmDialog.Merge"/>).
    /// </summary>
    public void Search(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        searchStop?.Cancel();
        searchStop = null;
        var generation = ++searchGeneration;
        SearchProblem = null;
        if (Finished || Dialog?.SearchParams(text) is not { } parameters || controller is null)
        {
            if (Searching)
            {
                Searching = false;
                RaiseChanged();
            }
            return;
        }
        var stop = new CancellationTokenSource();
        searchStop = stop;
        controller.Scope.RunDetached(async ct =>
        {
            using var linked = CancellationTokenSource.CreateLinkedTokenSource(ct, stop.Token);
            try
            {
                await Task.Delay(SearchDelay, controller.Time, linked.Token);
            }
            catch (OperationCanceledException)
            {
                return;
            }
            if (generation != searchGeneration || Finished)
            {
                return;
            }
            Searching = true;
            RaiseChanged();
            controller.Search(parameters, outcome =>
            {
                if (generation != searchGeneration || Finished)
                {
                    return;
                }
                Searching = false;
                if (outcome.TryGetValue(out var res, out var error))
                {
                    Dialog?.Merge(res);
                }
                else
                {
                    var t = CrmText.ErrorText(error);
                    SearchProblem = t.Length > 0 ? t : RpcErrorText.Text(CrmText.ActionText(CrmAction.Search), error);
                }
                RaiseChanged();
            });
        });
    }

    /// <summary>Save: the dialog closes and the controller runs crm.save.</summary>
    public void Save()
    {
        if (Finished || Phase != SessionPhase.Ready || Dialog is not { CanSave: true } || PendingSave is not { } parameters)
        {
            return;
        }
        Finish();
        controller?.RunSave(parameters);
    }

    /// <summary>Cancel (or Escape): nothing is saved.</summary>
    public void Cancel()
    {
        if (!Finished)
        {
            Finish();
        }
    }

    internal void Loaded(Outcome<CrmLookupResult> outcome)
    {
        if (Finished)
        {
            return;
        }
        if (outcome.TryGetValue(out var res, out var error))
        {
            Dialog = new CrmDialog(res);
            Phase = SessionPhase.Ready;
        }
        else
        {
            var t = CrmText.ErrorText(error);
            Failure = t.Length > 0 ? t : RpcErrorText.Text(CrmText.ActionText(CrmAction.Lookup), error);
            Phase = SessionPhase.Failed;
        }
        RaiseChanged();
    }

    private void Finish()
    {
        Finished = true;
        searchStop?.Cancel();
        searchStop = null;
        searchGeneration++;
        Closed?.Invoke(this, EventArgs.Empty);
    }

    private void RaiseChanged() => Changed?.Invoke(this, EventArgs.Empty);
}

/// <summary>
/// The CRM actions of one window (docs/eway.md §2.7): the button and menus
/// from <see cref="Subjects"/>, the dialog over crm.lookup, crm.save in the
/// background with the partNotDownloaded round, crm.remove after the
/// question, and Open of the saved item.
/// </summary>
/// <remarks>
/// Create it, and call it, on the UI thread; every event is raised there.
/// The list's pill, the bar and the button follow the daemon's
/// notify.messagesChanged (the window refreshes them on
/// <see cref="CrmState.Changed"/> and on its own changes).
/// </remarks>
public sealed partial class CrmController : IDisposable
{
    private readonly ILauncher launcher;
    private readonly ILogger logger;

    /// <summary>A controller of one window over <paramref name="client"/> and the application's <paramref name="state"/>.</summary>
    /// <param name="client">The daemon.</param>
    /// <param name="state">The application's CRM state, shared by every window.</param>
    /// <param name="launcher">Opens the saved item in the browser.</param>
    /// <param name="logger">Receives error classes, never an address or a name.</param>
    /// <param name="pending">Counts the background work; one of its own when null.</param>
    /// <param name="time">The clock of the search's pause; the system's when null.</param>
    public CrmController(
        RpcClient client,
        CrmState state,
        ILauncher launcher,
        ILogger<CrmController>? logger = null,
        PendingWork? pending = null,
        TimeProvider? time = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        ArgumentNullException.ThrowIfNull(state);
        ArgumentNullException.ThrowIfNull(launcher);
        Client = client;
        State = state;
        this.launcher = launcher;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        Scope = new ControllerScope(pending);
        Time = time ?? TimeProvider.System;
    }

    /// <summary>A dialog to show (a ContentDialog over the window).</summary>
    public event EventHandler<CrmDialogSession>? PresentRequested;

    /// <summary>A transient message in the window.</summary>
    public event EventHandler<string>? ToastRequested;

    /// <summary>The daemon.</summary>
    public RpcClient Client { get; }

    /// <summary>The application's CRM state.</summary>
    public CrmState State { get; }

    /// <summary>The messages the window acts on: the shown message of each selected row, or the window's message.</summary>
    public Func<IReadOnlyList<MessageSummary>> Subjects { get; set; } = () => [];

    /// <summary>
    /// Of the subjects, the messages shown by a conversation's row (its newest
    /// folder member standing for the thread): such a row offers nothing
    /// (<see cref="CrmRow.Conversation"/>).
    /// </summary>
    public Func<IReadOnlySet<MessageId>> Conversations { get; set; } = () => new HashSet<MessageId>();

    /// <summary>Asks before crm.remove; true goes ahead. Without it nothing is removed.</summary>
    public Func<CrmQuestion, Task<bool>>? Confirm { get; set; }

    /// <summary>The window the browser's own dialogs belong to, 0 for none.</summary>
    public nint Owner { get; set; }

    /// <summary>What the window's button and menus offer now.</summary>
    public CrmOffer Offer => State.Offer(Subjects(), Conversations());

    /// <summary>The window's button now.</summary>
    public CrmButton Button => State.Button(Subjects(), Conversations());

    /// <summary>The one subject's record, when exactly one is shown and saved.</summary>
    public CrmRecord? Record => Subjects() is [var only] ? State.Record(only) : null;

    /// <summary>The window went away: late answers are dropped.</summary>
    public bool IsClosed => Scope.IsClosed;

    internal ControllerScope Scope { get; }

    internal TimeProvider Time { get; }

    /// <summary>
    /// Save to eWay-CRM (the button, the menus): the dialog over crm.lookup
    /// of the subjects, when they can be saved and none is busy.
    /// </summary>
    public void Save()
    {
        Scope.VerifyAccess();
        var s = Subjects();
        var o = State.Offer(s, Conversations());
        if (IsClosed || !o.Shown || !o.Enabled || s.Count == 0 || s.Any(m => State.InFlight.Has(m.AccountId, m.Id)) || State.Account is not { } crm)
        {
            return;
        }
        var first = s[0];
        IReadOnlyList<MessageId> ids = [.. s.Select(m => m.Id)];
        var session = new CrmDialogSession(this, first.AccountId, ids, SummaryOf(s));
        Scope.Raise(PresentRequested, this, session);
        var parameters = new CrmLookupParams { AccountId = crm.Id, MessageAccountId = first.AccountId, MessageIds = ids };
        Scope.Perform(Client, API.CrmLookup, parameters, session.Loaded);
    }

    /// <summary>Edit Links… of a saved message: the same dialog, which edits once crm.lookup returns the message's record.</summary>
    public void EditLinks() => Save();

    /// <summary>Open in eWay-CRM: the saved item's URL (the daemon's), in the browser without a question.</summary>
    public void Open()
    {
        Scope.VerifyAccess();
        if (Record is not { } rec || CrmRules.OpenUrl(rec.Url) is not { Length: > 0 } url)
        {
            return;
        }
        OpenUrl(url);
    }

    /// <summary>Opens an item's URL of the bar (the daemon's), in the browser without a question; "" or a URL that is not https does nothing.</summary>
    public void OpenUrl(string url)
    {
        Scope.VerifyAccess();
        var u = CrmRules.OpenUrl(url);
        if (u.Length == 0)
        {
            return;
        }
        var owner = Owner;
        Scope.Perform(
            ct => launcher.OpenUrlAsync(u, owner, ct),
            outcome =>
            {
                if (outcome.Error is not { } error)
                {
                    return;
                }
                LogLaunchFailed(logger, error.GetType().Name);
                // TRANSLATORS: %s is a technical error message.
                Scope.Raise(ToastRequested, this, L10n.T("The link could not be opened: %s", error.Message));
            });
    }

    /// <summary>
    /// Remove from eWay-CRM: the question (<see cref="CrmText.RemoveQuestion"/>),
    /// then crm.remove of the one saved subject.
    /// </summary>
    public void Remove()
    {
        Scope.VerifyAccess();
        if (Subjects() is not [var m] || !State.IsSaved(m) || State.InFlight.Has(m.AccountId, m.Id) || State.Account is not { } crm || Confirm is not { } confirm)
        {
            return;
        }
        var q = CrmText.RemoveQuestion(m.Subject);
        Scope.Run(async _ =>
        {
            if (!await confirm(q) || IsClosed)
            {
                return;
            }
            RunRemove(crm.Id, m);
        });
    }

    /// <summary>The window went away: its late answers are dropped (a save or removal under way still ends).</summary>
    public void Close() => Scope.Close();

    /// <inheritdoc/>
    public void Dispose() => Close();

    // crm.search for the session.
    internal void Search(CrmSearchParams parameters, Action<Outcome<CrmSearchResult>> done) =>
        Scope.Perform(Client, API.CrmSearch, parameters, done);

    // crm.save of a dialog's choice, in the background: the messages are
    // busy meanwhile; a partNotDownloaded downloads the messages it names
    // (the status as a toast) and asks again, at most twice.
    internal void RunSave(CrmSaveParams parameters)
    {
        var account = parameters.MessageAccountId;
        var ids = parameters.MessageIds;
        State.Begin(account, ids);
        Scope.PerformPastClose(
            async () =>
            {
                try
                {
                    var outcome = await SaveAsync(Client, parameters, n =>
                    {
                        if (!IsClosed)
                        {
                            Scope.Raise(ToastRequested, this, CrmText.DownloadingText(n));
                        }
                    });
                    if (outcome.Result is { } res)
                    {
                        foreach (var o in res.Results.Where(o => o.Error is null))
                        {
                            State.SetOverride(account, o.MessageId, o.Record);
                        }
                    }
                    return outcome;
                }
                finally
                {
                    State.End(account, ids);
                }
            },
            o =>
            {
                var (result, error, action) = o.IsSuccess ? o.Value : (null, o.Error, CrmAction.Save);
                if (result is not null)
                {
                    var (text, err) = CrmText.SaveToast(CrmText.Summarize(result));
                    Scope.Raise(ToastRequested, this, text ?? RpcErrorText.Text(CrmText.ActionText(CrmAction.Save), err));
                    return;
                }
                LogSaveFailed(logger, RpcErrorText.DaemonError(error)?.Code.Value ?? 0);
                var t = CrmText.ErrorText(error);
                Scope.Raise(ToastRequested, this, t.Length > 0 ? t : RpcErrorText.Text(CrmText.ActionText(action), error));
            });
    }

    /// <summary>
    /// The partNotDownloaded round of crm.save (docs/eway.md D6): crm.save,
    /// and on partNotDownloaded message.download of each message it names
    /// (after <paramref name="downloading"/> with their number), then
    /// crm.save again, at most twice. Never throws: a failure is the error
    /// with the action that failed.
    /// </summary>
    internal static async Task<(CrmSaveResult? Result, Exception? Error, CrmAction Action)> SaveAsync(
        RpcClient client, CrmSaveParams parameters, Action<int> downloading)
    {
        var attempt = 0;
        while (true)
        {
            Exception failure;
            try
            {
                return (await client.CallAsync(API.CrmSave, parameters), null, CrmAction.Save);
            }
            catch (Exception e) when (e is RpcException or RpcClientException or TimeoutException or OperationCanceledException)
            {
                failure = e;
            }
            if (CrmRules.NotDownloaded(failure) is not { } missing || attempt >= 2)
            {
                return (null, failure, CrmAction.Save);
            }
            attempt++;
            downloading(missing.Count);
            foreach (var id in missing)
            {
                try
                {
                    await client.CallAsync(API.MessageDownload, new MessageDownloadParams { AccountId = parameters.MessageAccountId, MessageId = id });
                }
                catch (Exception e) when (e is RpcException or RpcClientException or TimeoutException or OperationCanceledException)
                {
                    return (null, e, CrmAction.Download);
                }
            }
        }
    }

    // crm.remove of the one saved subject: busy meanwhile; its answer
    // turns the button at once.
    private void RunRemove(AccountId crm, MessageSummary m)
    {
        IReadOnlyList<MessageId> ids = [m.Id];
        State.Begin(m.AccountId, ids);
        var parameters = new CrmRemoveParams { AccountId = crm, MessageAccountId = m.AccountId, MessageId = m.Id };
        Scope.PerformPastClose(
            async () =>
            {
                try
                {
                    await Client.CallAsync(API.CrmRemove, parameters);
                    State.SetOverride(m.AccountId, m.Id, null);
                    return true;
                }
                catch (RpcException e) when (e.Code == ErrorCode.CrmItemNotFound)
                {
                    // The daemon dropped its record too.
                    State.SetOverride(m.AccountId, m.Id, null);
                    throw;
                }
                finally
                {
                    State.End(m.AccountId, ids);
                }
            },
            outcome =>
            {
                if (outcome.Error is not { } error)
                {
                    Scope.Raise(ToastRequested, this, CrmText.RemovedToast());
                    return;
                }
                var t = CrmText.ErrorText(error);
                Scope.Raise(ToastRequested, this, t.Length > 0 ? t : RpcErrorText.Text(CrmText.ActionText(CrmAction.Remove), error));
            });
    }

    // The dialog's one line: the first message's subject and sender, and the
    // number of messages when there are several. Plain text.
    internal static string SummaryOf(IReadOnlyList<MessageSummary> messages)
    {
        if (messages.Count == 0)
        {
            return "";
        }
        var first = messages[0];
        var parts = new List<string> { Jira.Clean(LoadedMessageText.SubjectText(first.Subject)) };
        if (first.From is [var from, ..] && Jira.Clean(Format.DisplayName(from)) is { Length: > 0 } sender)
        {
            parts.Add(sender);
        }
        if (messages.Count > 1)
        {
            parts.Add(L10n.N("%d message", "%d messages", messages.Count));
        }
        return string.Join(" · ", parts.Where(p => p.Length > 0));
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "crm save failed: code {Code}")]
    private static partial void LogSaveFailed(ILogger logger, int code);

    [LoggerMessage(Level = LogLevel.Warning, Message = "open crm item failed: {Failure}")]
    private static partial void LogLaunchFailed(ILogger logger, string failure);
}
