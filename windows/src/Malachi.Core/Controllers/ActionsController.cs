// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ActionsController.swift
// (ActionsController); GTK: ui/internal/window/actions.go (markRead,
// markUnread, setSeenIDs, toggleFlagged, setFlaggedIDs, trashFrom, trashIDs,
// confirmTrash, trackMoves, archiveIDs, junkFrom, junkIDs, moveIDsToRole,
// call, callThen), outbox.go (retryOutbox, cancelSendFrom), remote.go
// (loadRemoteImages, trustSender, ensureKnownSendersPolicy), compose_open.go
// (openCompose) and drafts.go (openDraft).
//
// The per-message actions of the main window and the message windows,
// minus the widgets: an optimistic change in the model and the rows,
// message.flag / message.move / message.delete in the background, and the
// change put back on failure. Every action takes a list of messages: one
// from a message window or a plain row, all the folder members from a
// conversation row of the grouped list.
//
// Swift holds the mailbox, list and cache controllers; here they are the
// members the actions use (IActionsMailbox, IActionsList, IActionsCache),
// which those controllers implement. Every RPC runs through the mailbox's
// ControllerScope, so it starts after the caller's turn and its outcome
// comes back on the UI thread unless the mailbox closed (Swift
// mailbox.perform). What needs WinUI is a hook or an event the application
// installs: the confirmation dialog (Confirm), the compose window
// (OpenComposeRequested, RaiseDraft), the message windows that follow a
// removed message (WindowsClose, OpenMessageWindowRequested) and the views
// that show a star, the seen state or an outbox banner (StarChanged,
// SeenChanged, OutboxStateChanged). SeenChanged is GTK's refreshSeen, which
// macOS leaves to AppKit's menu validation; WinUI commands are not asked
// when a menu opens, so Windows follows GTK (docs/windows-port.md §3). Log
// lines carry method names and errors only, never subjects or addresses.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Platform;
using Malachi.Core.Settings;
using Malachi.Core.Text;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>
/// The per-message actions (actions.go and the RPC halves of outbox.go,
/// remote.go, compose_open.go and drafts.go). Lives and dies with the
/// mailbox it acts on; UI-thread-affine (docs/windows-port.md §7.1).
/// </summary>
public sealed partial class ActionsController
{
    private readonly Action<string> toast;
    private readonly ILogger logger;

    // The messages a draft.create or draft.open is being prepared for
    // (compose_open.go composing): a second click while it runs does
    // nothing.
    private readonly HashSet<MessageId> composing = [];

    /// <param name="mailbox">The folder half (the model, the badges, the RPC plumbing).</param>
    /// <param name="list">The list half (the rows).</param>
    /// <param name="cache">The loaded messages (outbox state, remote images, the compose source).</param>
    /// <param name="settings"><c>ConfirmDelete</c>.</param>
    /// <param name="toast">Shows a transient message (the window's toast overlay).</param>
    /// <param name="logger">Method names and errors only, never mail content.</param>
    public ActionsController(
        IActionsMailbox mailbox, IActionsList list, IActionsCache cache, SettingsStore settings, Action<string> toast, ILogger<ActionsController>? logger = null)
    {
        ArgumentNullException.ThrowIfNull(mailbox);
        ArgumentNullException.ThrowIfNull(list);
        ArgumentNullException.ThrowIfNull(cache);
        ArgumentNullException.ThrowIfNull(settings);
        ArgumentNullException.ThrowIfNull(toast);
        Mailbox = mailbox;
        List = list;
        Cache = cache;
        Settings = settings;
        this.toast = toast;
        this.logger = logger ?? (ILogger)NullLogger.Instance;
    }

    // Hooks and events (the WinUI half)

    /// <summary>
    /// Swift <c>openCompose</c>: opens a compose window with the prepared
    /// parameters (compose/manager.go <c>Open</c>).
    /// </summary>
    public event EventHandler<ComposeParams>? OpenComposeRequested;

    /// <summary>
    /// Swift <c>openMessageWindow</c>: opens a message in its own window
    /// (message_view.go <c>openMessageWindow</c>), a draft when the daemon
    /// cannot open drafts.
    /// </summary>
    public event EventHandler<MessageSummary>? OpenMessageWindowRequested;

    /// <summary>
    /// Swift <c>onWindowsClose</c>: a message left its folder, its window
    /// closes (message_view.go <c>closeMessageWindow</c>).
    /// </summary>
    public event EventHandler<MessageId>? WindowsClose;

    /// <summary>
    /// Swift <c>onStarChanged</c>: the flagged state of a message changed,
    /// every star showing it follows (actions.go <c>refreshStars</c>).
    /// </summary>
    public event EventHandler<(MessageId Id, bool Flagged)>? StarChanged;

    /// <summary>
    /// GTK <c>refreshSeen</c> (actions.go): the seen flag of a message
    /// changed (from the list, the mark-as-read timer or a message window,
    /// or back after a refused change), the actions of its message window
    /// follow (Mark as Read / Mark as Unread, the U key; message_window.go
    /// <c>setSeen</c>). macOS has no such callback: AppKit asks
    /// <c>flags(for:)</c> whenever a menu opens, which WinUI commands do not.
    /// </summary>
    public event EventHandler<(MessageId Id, bool Seen)>? SeenChanged;

    /// <summary>
    /// Swift <c>onOutboxStateChanged</c>: the cached delivery state of an
    /// outbox message changed, every banner showing it follows (outbox.go
    /// <c>showOutboxState</c>).
    /// </summary>
    public event EventHandler<MessageId>? OutboxStateChanged;

    /// <summary>The folder half.</summary>
    public IActionsMailbox Mailbox { get; }

    /// <summary>The list half.</summary>
    public IActionsList List { get; }

    /// <summary>The loaded messages.</summary>
    public IActionsCache Cache { get; }

    /// <summary>The settings (<c>ConfirmDelete</c>).</summary>
    public SettingsStore Settings { get; }

    /// <summary>
    /// The confirmation dialog. Without one a destructive action that must
    /// ask is refused (and logged), never run unasked.
    /// </summary>
    public ConfirmDestructive? Confirm { get; set; }

    /// <summary>
    /// Raises the compose window already editing the draft, if any
    /// (compose/manager.go <c>FindDraft</c>); true when there was one.
    /// </summary>
    public Func<Draft, bool>? RaiseDraft { get; set; }

    // Lookup

    /// <summary>
    /// What the window knows of message <paramref name="id"/>
    /// (message_view.go <c>summary</c>): the list's summary, or the cached
    /// full message's for a message window outliving the folder it was
    /// opened from.
    /// </summary>
    public MessageSummary? Summary(MessageId id) => Mailbox.Model.Message(id)?.Summary ?? Cache.Summary(id);

    // The known ones of the given messages, in order (actions.go summaries).
    private List<MessageSummary> Summaries(IEnumerable<MessageId> ids) => [.. ids.Select(Summary).OfType<MessageSummary>()];

    /// <summary>
    /// Swift <c>actionFlags(for:)</c>: what the per-message buttons and
    /// commands allow for the selected row (actions.go
    /// <c>setMessageActionsSensitive</c>).
    /// </summary>
    public ActionFlags ActionFlagsFor(ListRow? row) =>
        ActionRules.MessageActionState(row, Mailbox.Model.InOutbox, Mailbox.Model.CanMoveToRole);

    /// <summary>Swift <c>flags(for:)</c>: <see cref="ActionFlagsFor"/> for a single message (a message window's commands).</summary>
    public ActionFlags FlagsFor(MessageSummary s)
    {
        ArgumentNullException.ThrowIfNull(s);
        return ActionFlagsFor(new ListRow { Key = new ListKey(Message: s.Id), Message = s });
    }

    // Seen

    /// <summary>
    /// Sets the seen flag on message <paramref name="id"/> (actions.go
    /// <c>markRead</c>; the mark-as-read timer's target). A message read
    /// already is left alone.
    /// </summary>
    public void MarkRead(MessageId id) => SetSeen([id], true);

    /// <summary>Clears the seen flag on message <paramref name="id"/> (actions.go <c>markUnread</c>).</summary>
    public void MarkUnread(MessageId id) => SetSeen([id], false);

    /// <summary>
    /// Changes the seen flag of the messages that do not have it so yet,
    /// optimistically (rows, unread badge of the folder, commands, the
    /// message windows through <see cref="SeenChanged"/>), and sends one
    /// <c>message.flag</c>; a failure puts everything back (actions.go
    /// <c>setSeenIDs</c>). The messages are of one folder (a conversation
    /// row's members are).
    /// </summary>
    public void SetSeen(IReadOnlyList<MessageId> ids, bool seen)
    {
        ArgumentNullException.ThrowIfNull(ids);
        Mailbox.Scope.VerifyAccess();
        var todo = new List<MessageId>();
        FolderKey? key = null;
        foreach (var id in ids)
        {
            if (Summary(id) is not { } s || FolderTree.HasFlag(s.Flags, Flag.Seen) == seen || Mailbox.Model.InOutbox(s))
            {
                continue; // accelerators bypass the disabled commands
            }
            todo.Add(id);
            key = new FolderKey(s.AccountId, s.FolderId);
        }
        if (key is not { } k || todo.Count == 0)
        {
            return;
        }
        MessageId[] changing = [.. todo];
        void Apply(bool on)
        {
            var (set, clear) = ActionRules.FlagChange(Flag.Seen, on);
            var changed = List.ApplyFlags(changing, set, clear);
            if (changed.Count == 0)
            {
                return;
            }
            foreach (var id in changed)
            {
                SeenChanged?.Invoke(this, (id, on));
            }
            // Marking unread raises the unread count.
            Mailbox.AdjustCounts(k, on ? -changed.Count : changed.Count, 0);
        }
        Apply(seen);

        var n = changing.Length;
        var what = (seen, n) switch
        {
            (true, 1) => L10n.T("Marking the message as read"),
            (true, _) => L10n.N("Marking %d message as read", "Marking %d messages as read", n),
            (false, 1) => L10n.T("Marking the message as unread"),
            (false, _) => L10n.N("Marking %d message as unread", "Marking %d messages as unread", n),
        };
        var change = ActionRules.FlagChange(Flag.Seen, seen);
        Call(
            API.MessageFlag,
            new MessageFlagParams { AccountId = k.Account, MessageIds = changing, Set = change.Set, Clear = change.Clear },
            what,
            onError: _ => Apply(!seen));
    }

    // Flagged

    /// <summary>Stars or unstars message <paramref name="id"/> (actions.go <c>toggleFlagged</c>).</summary>
    public void ToggleFlagged(MessageId id)
    {
        if (Summary(id) is not { } s)
        {
            return;
        }
        SetFlagged([id], !FolderTree.HasFlag(s.Flags, Flag.Flagged));
    }

    /// <summary>
    /// Stars or unstars the messages that are not so yet, like
    /// <see cref="SetSeen"/> (actions.go <c>setFlaggedIDs</c>); every star
    /// showing a changed message hears about it through
    /// <see cref="StarChanged"/>.
    /// </summary>
    public void SetFlagged(IReadOnlyList<MessageId> ids, bool on)
    {
        ArgumentNullException.ThrowIfNull(ids);
        Mailbox.Scope.VerifyAccess();
        var todo = new List<MessageId>();
        AccountId? account = null;
        foreach (var id in ids)
        {
            if (Summary(id) is not { } s || FolderTree.HasFlag(s.Flags, Flag.Flagged) == on || Mailbox.Model.InOutbox(s))
            {
                continue; // accelerators bypass the disabled commands
            }
            todo.Add(id);
            account = s.AccountId;
        }
        if (account is not { } acc || todo.Count == 0)
        {
            return;
        }
        MessageId[] changing = [.. todo];
        void Apply(bool flagged)
        {
            var (set, clear) = ActionRules.FlagChange(Flag.Flagged, flagged);
            foreach (var id in List.ApplyFlags(changing, set, clear))
            {
                StarChanged?.Invoke(this, (id, flagged));
            }
        }
        Apply(on);

        var n = changing.Length;
        var what = (on, n) switch
        {
            (true, 1) => L10n.T("Starring the message"),
            (true, _) => L10n.N("Starring %d message", "Starring %d messages", n),
            (false, 1) => L10n.T("Removing the star"),
            (false, _) => L10n.N("Removing the star from %d message", "Removing the star from %d messages", n),
        };
        var change = ActionRules.FlagChange(Flag.Flagged, on);
        Call(
            API.MessageFlag,
            new MessageFlagParams { AccountId = acc, MessageIds = changing, Set = change.Set, Clear = change.Clear },
            what,
            onError: _ => Apply(!on));
    }

    // Trash

    /// <summary>
    /// Moves message <paramref name="id"/> to Trash after the optional
    /// confirmation shown over <paramref name="parent"/> (actions.go
    /// <c>trashFrom</c>); its subject is the question's body.
    /// </summary>
    public void Trash(MessageId id, object? parent = null)
    {
        if (Summary(id) is not { } s)
        {
            return;
        }
        Trash([id], LoadedMessageText.SubjectText(s.Subject), parent);
    }

    /// <summary>
    /// Moves messages to Trash (<c>message.delete</c>) after the optional
    /// confirmation shown over <paramref name="parent"/>, with
    /// <paramref name="subject"/> as its body (actions.go <c>trashIDs</c>).
    /// For a single outbox message it cancels the send instead
    /// (<see cref="CancelSend"/>).
    /// </summary>
    public void Trash(IReadOnlyList<MessageId> ids, string subject, object? parent = null)
    {
        ArgumentNullException.ThrowIfNull(ids);
        ArgumentNullException.ThrowIfNull(subject);
        Mailbox.Scope.VerifyAccess();
        if (ids.Count == 1 && Summary(ids[0]) is { } single && Mailbox.Model.InOutbox(single))
        {
            CancelSend(ids[0], parent);
            return;
        }
        var msgs = Summaries(ids);
        if (msgs.Count == 0)
        {
            return;
        }
        ConfirmTrash(parent, msgs.Count, subject, () => MoveToTrash(msgs));
    }

    // Asks before moving n messages to Trash when the setting is on, then
    // runs proceed (actions.go confirmTrash).
    private void ConfirmTrash(object? parent, int n, string subject, Action proceed)
    {
        if (!Settings.ConfirmDelete)
        {
            proceed();
            return;
        }
        var heading = L10n.T("Move to Trash?");
        if (n > 1)
        {
            // TRANSLATORS: %d is the number of messages of a conversation.
            heading = L10n.N("Move %d message to Trash?", "Move %d messages to Trash?", n);
        }
        Ask(parent, heading, subject, L10n.T("Move to _Trash"), proceed);
    }

    // The confirmed half of Trash: the rows go at once, the windows close,
    // the folder counts follow, message.delete runs and a failure puts
    // everything back.
    private void MoveToTrash(List<MessageSummary> msgs)
    {
        MessageId[] ids = [.. msgs.Select(s => s.Id)];
        var restore = List.RemoveRows(ids);
        foreach (var s in msgs)
        {
            WindowsClose?.Invoke(this, s.Id);
        }
        var acc = msgs[0].AccountId;
        // message.delete moves to the Trash role folder; a message already
        // there is expunged instead (no target count to credit).
        FolderKey? target = null;
        if (Mailbox.Model.FolderByRole(acc, FolderRole.Trash) is { } trash && trash.Id != msgs[0].FolderId)
        {
            target = new FolderKey(acc, trash.Id);
        }
        var undo = TrackMoves(msgs, target);
        var n = msgs.Count;
        var what = n == 1
            ? L10n.T("Moving the message to Trash")
            : L10n.N("Moving %d message to Trash", "Moving %d messages to Trash", n);
        Call(API.MessageDelete, new MessageDeleteParams { AccountId = acc, MessageIds = ids }, what, onError: _ =>
        {
            restore();
            undo();
        });
    }

    /// <summary>
    /// Adjusts the cached folder counts (the unread badges, the counts under
    /// the list title) for messages leaving their folder for
    /// <paramref name="target"/> (null: leaving the store) and returns the
    /// reverse, for a failed move (actions.go <c>trackMoves</c>). Read
    /// messages move only the totals; <see cref="MailModel.MoveCounts"/> says
    /// where even those stay put. Public for the tests (Swift's is internal).
    /// </summary>
    public Action TrackMoves(IReadOnlyList<MessageSummary> msgs, FolderKey? target)
    {
        ArgumentNullException.ThrowIfNull(msgs);
        if (msgs.Count == 0)
        {
            return static () => { };
        }
        var unread = msgs.Count(s => !FolderTree.HasFlag(s.Flags, Flag.Seen));
        var n = msgs.Count;
        var src = new FolderKey(msgs[0].AccountId, msgs[0].FolderId);
        void Shift(int sign) => Mailbox.MoveCounts(src, target, sign * unread, sign * n);
        Shift(1);
        return () => Shift(-1);
    }

    // Archive and junk

    /// <summary>Moves messages to the account's Archive folder (actions.go <c>archiveIDs</c>).</summary>
    public void Archive(IReadOnlyList<MessageId> ids) =>
        MoveToRole(ids, FolderRole.Archive, L10n.T("This account has no archive folder"), n => n == 1
            ? L10n.T("Archiving the message")
            : L10n.N("Archiving %d message", "Archiving %d messages", n));

    /// <summary>
    /// Moves message <paramref name="id"/> to the account's Junk folder after
    /// a confirmation shown over <paramref name="parent"/> (actions.go
    /// <c>junkFrom</c>).
    /// </summary>
    public void Junk(MessageId id, object? parent = null)
    {
        if (Summary(id) is not { } s)
        {
            return;
        }
        Junk([id], LoadedMessageText.SubjectText(s.Subject), parent);
    }

    /// <summary>
    /// Moves messages to the account's Junk folder after a confirmation shown
    /// over <paramref name="parent"/> (actions.go <c>junkIDs</c>). Unlike
    /// Trash the question is always asked: the move feeds the server's spam
    /// filter and is not undone by moving back.
    /// </summary>
    public void Junk(IReadOnlyList<MessageId> ids, string subject, object? parent = null)
    {
        ArgumentNullException.ThrowIfNull(ids);
        ArgumentNullException.ThrowIfNull(subject);
        Mailbox.Scope.VerifyAccess();
        var msgs = Summaries(ids);
        if (msgs.Count == 0)
        {
            return;
        }
        var missing = L10n.T("This account has no junk folder");
        if (Mailbox.Model.FolderByRole(msgs[0].AccountId, FolderRole.Junk) is null)
        {
            toast(missing);
            return;
        }
        var heading = L10n.T("Mark as junk?");
        if (msgs.Count > 1)
        {
            // TRANSLATORS: %d is the number of messages of a conversation.
            heading = L10n.N("Mark %d message as junk?", "Mark %d messages as junk?", msgs.Count);
        }
        MessageId[] moving = [.. msgs.Select(s => s.Id)];
        Ask(parent, heading, subject, L10n.T("Mark as _Junk"), () =>
            MoveToRole(moving, FolderRole.Junk, missing, n => n == 1
                ? L10n.T("Marking the message as junk")
                : L10n.N("Marking %d message as junk", "Marking %d messages as junk", n)));
    }

    /// <summary>
    /// Moves messages to their account's folder with the given role
    /// (<c>message.move</c>; actions.go <c>moveIDsToRole</c>).
    /// <paramref name="what"/> names the action in progressive form for the
    /// error toast, for the number moved; <paramref name="missing"/> is the
    /// toast when the account has no such folder. The rows go at once and
    /// come back on failure; the folder counts follow the messages to the
    /// target (<see cref="TrackMoves"/>). Outbox messages are left out (the
    /// daemon refuses moves on them), and a message already in the target
    /// folder is a no-op.
    /// </summary>
    public void MoveToRole(IReadOnlyList<MessageId> ids, FolderRole role, string missing, Func<int, string> what)
    {
        ArgumentNullException.ThrowIfNull(ids);
        ArgumentNullException.ThrowIfNull(missing);
        ArgumentNullException.ThrowIfNull(what);
        Mailbox.Scope.VerifyAccess();
        var msgs = Summaries(ids).Where(s => !Mailbox.Model.InOutbox(s)).ToList(); // accelerators bypass the disabled commands
        if (msgs.Count == 0)
        {
            return;
        }
        var acc = msgs[0].AccountId;
        if (Mailbox.Model.FolderByRole(acc, role) is not { } target)
        {
            toast(missing);
            return;
        }
        if (target.Id == msgs[0].FolderId)
        {
            return;
        }
        MessageId[] moving = [.. msgs.Select(s => s.Id)];
        var restore = List.RemoveRows(moving);
        foreach (var s in msgs)
        {
            WindowsClose?.Invoke(this, s.Id);
        }
        var undo = TrackMoves(msgs, new FolderKey(acc, target.Id));
        Call(
            API.MessageMove,
            new MessageMoveParams { AccountId = acc, MessageIds = moving, TargetFolderId = target.Id },
            what(msgs.Count),
            onError: _ =>
            {
                restore();
                undo();
            });
    }

    // Outbox

    /// <summary>
    /// Asks (always: there is no Trash to get the message back from) and then
    /// removes the outbox message <paramref name="id"/> for good with
    /// <c>message.delete</c>; the confirmation is shown over
    /// <paramref name="parent"/> (outbox.go <c>cancelSendFrom</c>). The row
    /// goes at once and comes back when the daemon refuses.
    /// </summary>
    public void CancelSend(MessageId id, object? parent = null)
    {
        Mailbox.Scope.VerifyAccess();
        if (Summary(id) is not { } s)
        {
            return;
        }
        // The subject isolated inside the sentence, so that a right-to-left
        // one keeps its quotes and the sentence its order (DisplayText).
        // TRANSLATORS: %s is the subject of the message.
        var body = L10n.T("“%s” will be removed from the outbox and not sent.", DisplayText.Isolate(LoadedMessageText.SubjectText(s.Subject)));
        Ask(parent, L10n.T("Cancel sending this message?"), body, L10n.T("Do Not _Send"), () =>
        {
            var restore = List.RemoveRows([id]);
            WindowsClose?.Invoke(this, id);
            var undo = TrackMoves([s], null);
            // The drop is ours, not a delivery (trackOutbox). It is counted
            // before the call: removing a queued or failed message moves
            // pendingOutbox or failedOutbox, and the notify.syncState that
            // follows reloads the folders, possibly before this reply
            // arrives.
            Mailbox.NoteOutboxCancelled(s.AccountId);
            Call(
                API.MessageDelete,
                new MessageDeleteParams { AccountId = s.AccountId, MessageIds = [id] },
                L10n.T("Cancelling the send"),
                onError: _ =>
                {
                    // Nothing was dropped after all.
                    Mailbox.NoteOutboxCancelFailed(s.AccountId);
                    restore();
                    undo();
                },
                onOk: _ =>
                {
                    // The sidebar is refreshed here as well (an empty outbox
                    // disappears), for a daemon that sends no
                    // notify.syncState on the change; a second reload finds
                    // nothing left to count.
                    Mailbox.OnOutboxChanged(s.AccountId);
                });
        });
    }

    /// <summary>
    /// Re-queues the failed message <paramref name="id"/>
    /// (<c>outbox.retry</c>; outbox.go <c>retryOutbox</c>). The banner shows
    /// "queued" at once; a refused retry fetches the real state back.
    /// </summary>
    public void RetryOutbox(MessageId id)
    {
        Mailbox.Scope.VerifyAccess();
        if (Summary(id) is not { } s)
        {
            return;
        }
        if (Cache.Loaded(id) is { Msg: { Summary.Outbox: { } cached } msg } lm)
        {
            lm.Msg = msg with { Summary = msg.Summary with { Outbox = Queued(cached) } };
        }
        if (Mailbox.Model.Message(id) is { Summary.Outbox: { } listed })
        {
            Mailbox.Model.SetOutbox(id, Queued(listed));
        }
        OutboxStateChanged?.Invoke(this, id);
        Call(
            API.OutboxRetry,
            new OutboxRetryParams { AccountId = s.AccountId, MessageId = id },
            L10n.T("Retrying the send"),
            onError: _ => Cache.Refetch(s, _ => OutboxStateChanged?.Invoke(this, id)));

        static OutboxInfo Queued(OutboxInfo o) => o with { State = OutboxState.Queued, Error = null };
    }

    // Remote content

    /// <summary>
    /// Fetches the body of <paramref name="id"/> again with remote images
    /// allowed for this one call and shows the result wherever the message
    /// is on display (remote.go <c>loadRemoteImages</c>, through the cache:
    /// the bar shows the wait from the click on, a request already running
    /// is left alone, a failure is a toast with the bar back as it was).
    /// </summary>
    public void LoadImages(MessageId id)
    {
        if (Summary(id) is not { } s)
        {
            return;
        }
        Cache.LoadImages(s, static _ => { });
    }

    /// <summary>
    /// Puts the sender of <paramref name="id"/> on the daemon's known-senders
    /// list (<c>sender.add</c>), switches the stored remote-content preference
    /// to "from known senders" when it was "never" (otherwise the list would
    /// change nothing), and loads this message's images now (remote.go
    /// <c>trustSender</c>). The bar shows the wait from the click on, through
    /// all three calls.
    /// </summary>
    public void TrustSender(MessageId id)
    {
        Mailbox.Scope.VerifyAccess();
        if (Summary(id) is not { } s || s.From.Count == 0)
        {
            return;
        }
        var address = s.From[0].Email.Trim();
        if (address.Length == 0 || Cache.BeginLoadingImages(id) is not { } lm)
        {
            return;
        }
        Call(
            API.SenderAdd,
            new SenderAddParams { Address = address },
            L10n.T("Trusting the sender"),
            onError: _ => Cache.ImagesDone(id, lm),
            onOk: _ => EnsureKnownSendersPolicy(() => Cache.FetchRemoteImages(s, lm, static _ => { })));
    }

    /// <summary>
    /// Raises the stored remote-content preference from "block" to
    /// "knownSenders" (<c>config.get</c>, then <c>config.set</c> with the
    /// whole set echoed back) and runs <paramref name="done"/> afterwards, or
    /// at once when nothing needs changing (remote.go
    /// <c>ensureKnownSendersPolicy</c>). A failure is toasted and
    /// <paramref name="done"/> still runs: the sender is trusted either way.
    /// </summary>
    public void EnsureKnownSendersPolicy(Action done)
    {
        ArgumentNullException.ThrowIfNull(done);
        Mailbox.Scope.Perform(Mailbox.Client, API.ConfigGet, new EmptyParams(), outcome =>
        {
            if (!outcome.TryGetValue(out var got, out var error))
            {
                PolicyChangeFailed(error!);
                done();
                return;
            }
            if (got.Preferences.RemoteContent.Value != RemoteContentPolicy.Block)
            {
                done();
                return;
            }
            var want = got.Preferences with { RemoteContent = RemoteContentPolicy.KnownSenders };
            Mailbox.Scope.Perform(Mailbox.Client, API.ConfigSet, new ConfigSetParams { Preferences = want }, set =>
            {
                if (!set.IsSuccess)
                {
                    PolicyChangeFailed(set.Error!);
                }
                done();
            });
        });
    }

    private void PolicyChangeFailed(Exception error)
    {
        LogPolicyChangeFailed(logger, error);
        toast(RpcErrorText.Text(L10n.T("Changing the remote content preference"), error));
    }

    // Compose

    /// <summary>
    /// Opens a reply or forward of message <paramref name="id"/>
    /// (compose_open.go <c>openCompose</c>). The template comes from the
    /// backend (<c>draft.create</c>: recipients, subject, the original quoted
    /// formatted with its pictures copied into the attachment store); a
    /// second click while it is being prepared does nothing (one window will
    /// appear). Only when the backend cannot answer does the window open from
    /// what the pane knows (<see cref="Prefill.Create"/>), with a toast unless
    /// the fallback is the normal course
    /// (<see cref="ComposeSources.ComposeFallbackText"/>).
    /// </summary>
    public void OpenCompose(ComposeKind kind, MessageId id)
    {
        Mailbox.Scope.VerifyAccess();
        if (Summary(id) is not { } s || composing.Contains(id))
        {
            return;
        }
        var src = ComposeSources.ComposeSource(s, Cache.Loaded(id));
        // The account's own address, for Reply All exclusion; the first
        // account's when the message's is unknown (compose.Manager
        // SelfAddress).
        var me = Mailbox.Model.Account(s.AccountId) is { } account
            ? FolderTree.SelfAddress(account)
            : Mailbox.Model.EnabledAccounts is { Count: > 0 } enabled
                ? FolderTree.SelfAddress(enabled[0])
                : new Address { Email = "" };
        void Fallback() => OpenComposeRequested?.Invoke(this, Prefill.Create(kind, src, me) with { AccountId = s.AccountId });

        composing.Add(id);
        var attribution = Prefill.Attribution(kind, src);
        var parameters = new DraftCreateParams
        {
            AccountId = s.AccountId,
            Mode = kind.Mode,
            MessageId = id,
            Attribution = attribution.Length == 0 ? null : attribution,
        };
        Mailbox.Scope.Perform(Mailbox.Client, API.DraftCreate, parameters, outcome =>
        {
            composing.Remove(id);
            if (!outcome.TryGetValue(out var res, out var error))
            {
                LogDraftCreateFailed(logger, parameters.Mode.ToString(), error!);
                var text = ComposeSources.ComposeFallbackText(ComposeSources.ComposeWhat(kind), error);
                if (text.Length > 0)
                {
                    toast(text);
                }
                Fallback();
                return;
            }
            OpenComposeRequested?.Invoke(this, Prefill.FromDraft(kind, res.Draft, res.Blocked) with { AccountId = s.AccountId });
        }, RpcTimeouts.Compose);
    }

    /// <summary>
    /// Opens message <paramref name="id"/> of a Drafts folder in the compose
    /// window, or raises the window already editing it (drafts.go
    /// <c>openDraft</c>). A second request while the first is on its way does
    /// nothing; a daemon without <c>draft.open</c> shows the message instead.
    /// </summary>
    public void OpenDraft(MessageId id)
    {
        Mailbox.Scope.VerifyAccess();
        if (Summary(id) is not { } s || composing.Contains(id))
        {
            return;
        }
        composing.Add(id);
        var parameters = new DraftOpenParams { AccountId = s.AccountId, MessageId = id };
        Mailbox.Scope.Perform(Mailbox.Client, API.DraftOpen, parameters, outcome =>
        {
            composing.Remove(id);
            if (!outcome.TryGetValue(out var res, out var error))
            {
                LogCallFailed(logger, API.DraftOpen.Name, error!);
                if (DraftOpen.DraftOpenUnsupported(error))
                {
                    OpenMessageWindowRequested?.Invoke(this, s);
                    return;
                }
                toast(DraftOpen.DraftOpenErrorText(error));
                return;
            }
            if (RaiseDraft?.Invoke(res.Draft) == true)
            {
                return;
            }
            OpenComposeRequested?.Invoke(this, Prefill.FromDraft(ComposeKind.Edit, res.Draft, res.Blocked));
            if (res.Skipped is { Count: > 0 } skipped)
            {
                toast(DraftOpen.DraftSkippedText(skipped.Count));
            }
        }, RpcTimeouts.Compose);
    }

    // Plumbing

    // The window's callThen (actions.go): on failure the error is logged,
    // toasted as RpcErrorText.Text(what, err) and handed to onError (which
    // reverts the optimistic change); on success onOk runs with the result.
    // Either callback may be null. Nothing runs once the mailbox closed.
    private void Call<TParams, TResult>(
        RpcMethod<TParams, TResult> method, TParams parameters, string what, Action<Exception>? onError = null, Action<TResult>? onOk = null)
    {
        Mailbox.Scope.Perform(Mailbox.Client, method, parameters, outcome =>
        {
            if (outcome.TryGetValue(out var res, out var error))
            {
                onOk?.Invoke(res);
                return;
            }
            LogCallFailed(logger, method.Name, error!);
            toast(RpcErrorText.Text(what, error));
            onError?.Invoke(error!);
        });
    }

    // Shows the destructive confirmation over parent and runs proceed when
    // the user confirms. Without a hook the action is refused: a destructive
    // step the user asked to be questioned about never runs unasked.
    private void Ask(object? parent, string heading, string body, string label, Action proceed)
    {
        if (Confirm is not { } confirm)
        {
            LogNoConfirmationHook(logger);
            return;
        }
        Mailbox.Scope.Perform(_ => confirm(parent, heading, body, label), outcome =>
        {
            if (!outcome.TryGetValue(out var confirmed, out var error))
            {
                // The dialog could not be shown (another one is open): the
                // action is not run, and the reason is not lost.
                LogConfirmationFailed(logger, error!);
                return;
            }
            if (confirmed)
            {
                proceed();
            }
        });
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "{Method} failed")]
    private static partial void LogCallFailed(ILogger logger, string method, Exception error);

    [LoggerMessage(Level = LogLevel.Warning, Message = "draft.create {Mode} failed")]
    private static partial void LogDraftCreateFailed(ILogger logger, string mode, Exception error);

    [LoggerMessage(Level = LogLevel.Warning, Message = "remote content preference not changed")]
    private static partial void LogPolicyChangeFailed(ILogger logger, Exception error);

    [LoggerMessage(Level = LogLevel.Error, Message = "no confirmation hook is installed; the action was refused")]
    private static partial void LogNoConfirmationHook(ILogger logger);

    [LoggerMessage(Level = LogLevel.Warning, Message = "the confirmation failed; the action was not run")]
    private static partial void LogConfirmationFailed(ILogger logger, Exception error);
}
