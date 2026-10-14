// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/BoardController.swift
// (BoardController); GTK: ui/internal/board/controller.go (Controller).
//
// The board, the controller half: what the user chose to look at (the
// style, the filters, the selection, the "Why is this here?" box) and the
// view model built from it and the source's snapshot (Board.View). No
// WinUI: the board page and its styles lay the view model out, ask for
// changes here and redraw what Changed names. What the user decides about
// a case (its state, done, a remind, archive, a discarded draft, a promise
// ticked off) goes to the source, which keeps it; the view model follows
// once the source reports it. Selecting a case asks the source for its
// conversation (IBoardSource.LoadMessages); the detail says it loads until
// it arrives. The source's toasts (a refused write) reach the page through
// ToastRequested, what Archive did through ArchiveDone (a toast with Undo,
// UndoArchive). The board remembers its style and account filter through
// the settings the page writes (board-last-style, board-account-filter):
// the options defaultStyle, lastStyle and savedAccount read them back.
//
// Windows: Swift's onChange and onToast are the events Changed and
// ToastRequested; its now closure and Calendar are the injected
// TimeProvider, culture and time zone (docs/windows-port.md §3.1). It does
// no background work of its own, so it has no ControllerScope and raises
// its events directly, as ConversationController does; a handler that
// throws does not leave a delivery stuck (the flag is reset in finally).
// Every member runs on the UI thread.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Boards;

namespace Malachi.Core.Controllers;

/// <summary>
/// The board's view state and view model (Swift <c>BoardController</c>).
/// One observer (<see cref="Changed"/>): the board page, which fans the
/// changes out to its children.
/// </summary>
public sealed partial class BoardController
{
    private readonly TimeProvider time;
    private readonly CultureInfo? culture;
    private readonly TimeZoneInfo? timeZone;

    // Board View (the settings' board-default-style), asked for each time
    // the board shows until the user picks a style (Board.StyleOnShow).
    private readonly Func<Board.DefaultStyle> defaultStyle;

    // The style used last (board-last-style, which the page writes on every
    // Changes.Style).
    private readonly Func<BoardStyle> lastStyle;

    // The account filter saved (board-account-filter, which the page writes
    // on every Changes.Filters), asked for the first time the board shows.
    private readonly Func<string?> savedAccount;

    // The user chose a style in this run (SetStyle, ShowWaitingForYou); the
    // board then keeps it (Board.StyleOnShow).
    private bool picked;

    // The saved account filter waiting for the accounts to be known
    // (Board.FilterOnShow); null when none waits.
    private string? pendingAccount;

    // The case the user selected explicitly (Select from the page), not the
    // List's automatic first row; PaneLive says whether a reply pane is live
    // for a case. Together they decide whether a selection survives a style
    // switch or a narrowing (KeepsSelection).
    private BoardCaseId? userPicked;

    // Where the selection goes when the selected case leaves what is shown
    // after the user's own write (done, reopened, moved out of the filter):
    // computed before the write, used by the first report of the source that
    // follows it (which a source may send later) and then dropped, whatever
    // that report holds. A write that changes nothing notes none, and any
    // view state the user asks for meanwhile drops it.
    private (BoardCaseId Id, BoardCaseId? Next)? departure;

    // Changes not yet delivered to Changed, and whether a delivery is under
    // way: a listener that calls the controller from Changed gets that change
    // after its own call returns, not inside it.
    private Changes pending;
    private bool notifying;

    // The case and version whose conversation was last asked for, and the
    // phase then: asked again when another case is selected or the case
    // changed, not on every report. A failed load is asked again when the
    // user selects the case again or asks to (RetryMessages), and when the
    // board came back from a failure (a reconnect).
    private (BoardCaseId Id, long Version, Board.Phase Phase)? requested;

    /// <summary>
    /// The controller over <paramref name="source"/>, installed as its
    /// <see cref="IBoardSource.OnChange"/>, <see cref="IBoardSource.OnError"/>,
    /// <see cref="IBoardSource.OnNotice"/> and
    /// <see cref="IBoardSource.OnArchived"/>. Board View
    /// (<paramref name="defaultStyle"/>, <paramref name="lastStyle"/>) gives
    /// the style each time the board shows until the user picks one
    /// (<see cref="BoardWillShow"/>); until the first show the board holds
    /// the List. It reports nothing while it is made, but may ask the source
    /// for the selected case's conversation.
    /// </summary>
    /// <param name="source">The cases and the writes.</param>
    /// <param name="time">The clock of the dates; the system's when null.</param>
    /// <param name="culture">The dates' culture; the current one when null.</param>
    /// <param name="timeZone">The days' time zone; the local one when null.</param>
    /// <param name="defaultStyle">The setting <c>board-default-style</c>, read as the board shows; Last Used when null.</param>
    /// <param name="lastStyle">The setting <c>board-last-style</c>; the List when null.</param>
    /// <param name="savedAccount">The setting <c>board-account-filter</c>, read at the first show; every account when null.</param>
    public BoardController(
        IBoardSource source,
        TimeProvider? time = null,
        CultureInfo? culture = null,
        TimeZoneInfo? timeZone = null,
        Func<Board.DefaultStyle>? defaultStyle = null,
        Func<BoardStyle>? lastStyle = null,
        Func<string?>? savedAccount = null)
    {
        ArgumentNullException.ThrowIfNull(source);
        Source = source;
        this.time = time ?? TimeProvider.System;
        this.culture = culture;
        this.timeZone = timeZone;
        this.defaultStyle = defaultStyle ?? (() => Board.DefaultStyle.Last);
        this.lastStyle = lastStyle ?? (() => BoardStyle.List);
        this.savedAccount = savedAccount ?? (() => null);
        var state = new Board.ViewState();
        state = state with { Selection = Board.ResolveSelection(source.Snapshot, state) };
        State = state;
        View = Board.View(source.Snapshot, state, Now, culture, timeZone);
        source.OnChange = Refresh;
        source.OnError = Toast;
        source.OnNotice = Toast;
        source.OnArchived = OnArchived;
        RequestMessages();
    }

    /// <summary>Called after every change of <see cref="State"/> or <see cref="View"/>, with what changed (Swift <c>onChange</c>).</summary>
    public event EventHandler<Changes>? Changed;

    /// <summary>
    /// Called with a short sentence for a toast: a write the source could not
    /// make (undone by then), or what Archive did when nobody listens to
    /// <see cref="ArchiveDone"/> (Swift <c>onToast</c>).
    /// </summary>
    public event EventHandler<string>? ToastRequested;

    /// <summary>
    /// Called with what Archive did, for a toast with Undo
    /// (<see cref="Board.ArchiveOutcome.Text"/> and
    /// <see cref="Board.ArchiveOutcome.UndoLabel"/>; <see cref="UndoArchive"/>
    /// takes it back). Without a listener, or without Undo (a null
    /// <see cref="Board.ArchiveOutcome.UndoLabel"/>: nothing the daemon can
    /// move back), the text goes to <see cref="ToastRequested"/> (Go
    /// <c>OnArchived</c>).
    /// </summary>
    public event EventHandler<Board.ArchiveOutcome>? ArchiveDone;

    /// <summary>The cases and the writes.</summary>
    public IBoardSource Source { get; }

    /// <summary>What the user looks at; its selection is always the resolved one (<see cref="Board.ViewModel.Selection"/>).</summary>
    public Board.ViewState State { get; private set; }

    /// <summary>The view model.</summary>
    public Board.ViewModel View { get; private set; }

    /// <summary>Whether the board has shown in this run (<see cref="BoardWillShow"/>).</summary>
    public bool HasShown { get; private set; }

    /// <summary>How far the source's data is (<see cref="Board.ViewModel.Phase"/> once built).</summary>
    public Board.Phase Phase => Source.Snapshot.Phase;

    private DateTimeOffset Now => time.GetUtcNow();

    /// <summary>The remind presets for now (<see cref="Board.RemindPresets"/>).</summary>
    public IReadOnlyList<Board.RemindPreset> RemindPresets() => Board.RemindPresets(Now, culture, timeZone);

    // What the user looks at

    /// <summary>
    /// The user's switch of the style. The selected case stays selected
    /// only when <see cref="KeepsSelection"/> (a live reply pane, or an
    /// explicit pick; Columns and Today show it in their panel, so an inline
    /// reply editor moves there); else the selection is cleared: Columns and
    /// Today show no panel and the list selects its first row when its
    /// detail is beside it. The board keeps the style from now on in this
    /// run (<see cref="Board.StyleOnShow"/>).
    /// </summary>
    public void SetStyle(BoardStyle style)
    {
        picked = true;
        ChangeStyle(style);
    }

    /// <summary>Switches the list's filter and clears the selection (the list then selects its first row).</summary>
    public void SetFilter(Board.Filter filter)
    {
        if (filter == State.Filter)
        {
            return;
        }
        Apply(State with { Filter = filter, Selection = null });
    }

    /// <summary>Switches the account filter (null: every account) and clears the selection (the list then selects its first row).</summary>
    public void SetAccount(AccountId? account)
    {
        pendingAccount = null;
        if (account == State.Account)
        {
            return;
        }
        Apply(State with { Account = account, Selection = null });
    }

    /// <summary>
    /// Selects <paramref name="id"/>, or nothing. A case that is not shown
    /// selects nothing; in the list with its detail beside it, nothing is its
    /// first row.
    /// </summary>
    public void Select(BoardCaseId? id)
    {
        if (id is not null && id == State.Selection && FailedLoad(id))
        {
            // Selected again: its conversation is asked for again.
            requested = null;
        }
        userPicked = id;
        Apply(State with { Selection = id });
    }

    /// <summary>
    /// Is a reply pane live for this case (an inline editor with text or a
    /// save in flight)? Set by the reply editor host; decides with the
    /// user's own pick whether a selection survives a style switch or a
    /// narrowing (<see cref="KeepsSelection"/>).
    /// </summary>
    public Func<BoardCaseId, bool>? PaneLive { get; set; }

    /// <summary>
    /// The rule of <see cref="SetStyle"/> and <see cref="SetInlineDetail"/>
    /// (false): the selection is kept only when a reply pane is live for the
    /// case or the user selected that case explicitly (<see cref="Select"/>).
    /// The List's automatic first row is not kept: Columns and Today would
    /// otherwise slide their panel in for a case nobody chose, and the
    /// overview page opens without a panel.
    /// </summary>
    public bool KeepsSelection() =>
        State.Selection is { } id && (id == userPicked || (PaneLive?.Invoke(id) ?? false));

    /// <summary>
    /// Whether the list has room for the detail beside it. Folding the detail
    /// away keeps the selection only when <see cref="KeepsSelection"/> (a
    /// live reply pane or an explicit pick): that case's detail (and an
    /// inline reply editor in it) moves to the panel, otherwise nothing is
    /// selected and no panel slides in; unfolding shows it beside the list
    /// again, or selects the first row.
    /// </summary>
    public void SetInlineDetail(bool on)
    {
        if (on == State.InlineDetail)
        {
            return;
        }
        var next = State with { InlineDetail = on };
        if (!on && !KeepsSelection())
        {
            next = next with { Selection = null };
        }
        Apply(next);
    }

    /// <summary>Opens or closes the detail's "Why is this here?" box. Nothing without a selection.</summary>
    public void ToggleWhy()
    {
        if (State.Selection is null)
        {
            return;
        }
        Apply(State with { RevealsWhy = !State.RevealsWhy });
    }

    /// <summary>The list filtered to the cases waiting for the user (the Today page's "and N more").</summary>
    public void ShowWaitingForYou()
    {
        picked = true;
        var next = State with { Style = BoardStyle.List, Filter = Board.Filter.Of(Board.State.You) };
        if (next.Style != State.Style || next.Filter != State.Filter)
        {
            next = next with { Selection = null };
        }
        Apply(next);
    }

    /// <summary>Builds the view model anew: after the source changed, or when the date may have (a new day moves the deadlines).</summary>
    public void Refresh() => Apply(State, user: false);

    /// <summary>
    /// The board is about to show (the window enters Board mode, before its
    /// page is laid out): until the user picks a style it takes Board View
    /// (<see cref="Board.StyleOnShow"/>), and the first time in a run the
    /// saved account filter (<see cref="Board.FilterOnShow"/>; once the
    /// accounts are known).
    /// </summary>
    public void BoardWillShow()
    {
        var first = !HasShown;
        HasShown = true;
        if (first)
        {
            var saved = savedAccount();
            pendingAccount = string.IsNullOrEmpty(saved) ? null : saved;
        }
        var next = State with { Style = Board.StyleOnShow(defaultStyle(), lastStyle(), State.Style, picked) };
        if (next.Style != State.Style || pendingAccount is not null)
        {
            Apply(next);
        }
    }

    /// <summary>
    /// The toast's Undo: takes back what Archive did. The messages go back to
    /// their folders and the case back on the board
    /// (<see cref="Board.UndoArchive"/>'s calls, through a source that can
    /// move messages, <see cref="IBoardArchiveUndoer"/>); any other source
    /// only reopens the case.
    /// </summary>
    public void UndoArchive(Board.ArchiveOutcome outcome)
    {
        ArgumentNullException.ThrowIfNull(outcome);
        departure = null;
        if (Source is IBoardArchiveUndoer undoer)
        {
            undoer.UndoArchive(outcome);
            return;
        }
        Source.SetDone(false, outcome.Case);
    }

    /// <summary>
    /// The board shows again (the window entered Board mode): a board that
    /// could not be listed is asked for again at once, then the view model is
    /// built anew (<see cref="Refresh"/>).
    /// </summary>
    public void BoardShown()
    {
        if (Phase.IsFailure)
        {
            Source.Refresh();
        }
        Refresh();
    }

    /// <summary>The detail's Try Again: asks for the selected case's conversation again after it could not be loaded.</summary>
    public void RetryMessages()
    {
        if (!FailedLoad(State.Selection))
        {
            return;
        }
        requested = null;
        RequestMessages();
    }

    // What the user decides about a case

    /// <summary>
    /// Moves the case to <paramref name="state"/>. Moving it to the state it
    /// would have by itself (the assistant's, or the rules') puts it back to
    /// automatic.
    /// </summary>
    public void SetState(Board.State state, BoardCaseId id)
    {
        var snapshot = Source.Snapshot;
        if (snapshot.FindCase(id) is not { } c)
        {
            return;
        }
        var current = c.UserState;
        var automatic = Board.StateOf(c with { UserState = null }, snapshot.Annotated);
        Board.State? value = state == automatic ? null : state;
        Write(id, value != current, () => Source.SetState(value, id));
    }

    /// <summary>Marks the case done. The list selects the next row, else the previous one; Columns and Today select nothing.</summary>
    public void MarkDone(BoardCaseId id) =>
        Write(id, Source.Snapshot.FindCase(id) is { Done: false }, () => Source.SetDone(true, id));

    /// <summary>Puts a done case back on the board.</summary>
    public void Reopen(BoardCaseId id) =>
        Write(id, Source.Snapshot.FindCase(id) is { Done: true }, () => Source.SetDone(false, id));

    /// <summary>Hides the case until <paramref name="until"/>; null puts a snoozed case back on the board. The selection moves on as after done.</summary>
    public void Remind(BoardCaseId id, DateTimeOffset? until)
    {
        var c = Source.Snapshot.FindCase(id);
        bool changes;
        if (until is { } u)
        {
            changes = c is not null && c.Visibility != Board.Visibility.Snoozed(u);
        }
        else
        {
            changes = c?.Visibility.RemindAt is not null;
        }
        Write(id, changes, () => Source.Remind(until, id));
    }

    /// <summary>Archives the case (where its account can) and marks it done; the toast says what it did. The selection moves on as after done.</summary>
    public void Archive(BoardCaseId id)
    {
        var c = Source.Snapshot.FindCase(id);
        Write(id, c is not null && !c.Visibility.IsDone, () => Source.Archive(id));
    }

    /// <summary>Ticks a promise off, or reopens it.</summary>
    public void SetCommitmentDone(BoardCommitmentId id, bool done)
    {
        departure = null;
        Source.SetCommitmentDone(done, id);
    }

    /// <summary>Drops the assistant's suggested reply.</summary>
    public void DiscardDraft(BoardCaseId id) => Source.DiscardDraft(id);

    /// <summary>
    /// The inline reply editor's Discard: deletes <paramref name="draft"/>
    /// (the one the editor edits, whatever the case links by now) and
    /// completes when that is done; faults when it was refused
    /// (<see cref="IBoardSource.DiscardDraftAsync"/>).
    /// </summary>
    public Task DiscardDraftAsync(BoardCaseId id, DraftId draft, AccountId account) => Source.DiscardDraftAsync(draft, account, id);

    /// <summary>
    /// Unstar: removes the star that keeps the case hot. The rules then decide
    /// where the case goes, so no departure is noted: the selection stays
    /// while the case is on the board.
    /// </summary>
    public void Unflag(BoardCaseId id) => Source.Unflag(id);

    // Internals

    private void Toast(string text) => ToastRequested?.Invoke(this, text);

    // An outcome without Undo (the daemon moved nothing it can take back)
    // is a plain toast.
    private void OnArchived(Board.ArchiveOutcome outcome)
    {
        if (outcome.UndoLabel is not null && ArchiveDone is { } handler)
        {
            handler(this, outcome);
            return;
        }
        Toast(outcome.Text);
    }

    // The style switch itself, the user's (SetStyle) or Board View's.
    private void ChangeStyle(BoardStyle style)
    {
        if (style == State.Style)
        {
            return;
        }
        var next = State with { Style = style };
        if (!KeepsSelection())
        {
            next = next with { Selection = null };
        }
        Apply(next);
    }

    // Runs the user's write on case id; when id is selected and the write
    // changes the case, notes where the selection goes should the case leave
    // what is shown. A write that changes nothing drops a departure noted
    // before: no report of the source belongs to it.
    private void Write(BoardCaseId id, bool changes, Action body)
    {
        departure = changes && State.Selection == id ? (id, Board.SelectionAfterDone(id, Source.Snapshot, State)) : null;
        body();
    }

    // Makes next the state, its selection resolved, rebuilds the view model
    // and tells the page what changed. user: the user asked for another view
    // state (a pending departure no longer applies).
    private void Apply(Board.ViewState next, bool user = true)
    {
        var snapshot = Source.Snapshot;
        if (pendingAccount is { } saved && snapshot.Accounts.Count > 0)
        {
            // The saved filter, once the accounts are known; an account that
            // went away leaves every account.
            var filter = Board.FilterOnShow(saved, snapshot.Accounts);
            AccountId? savedId = null;
            if (filter.Length != 0)
            {
                savedId = new AccountId(filter);
            }
            if (savedId != next.Account)
            {
                next = next with { Account = savedId, Selection = null };
            }
            pendingAccount = null;
        }
        if (next.Account is { } account && !snapshot.Accounts.Any(a => a.Id == account))
        {
            // The account went away: its filter with it.
            next = next with { Account = null };
        }
        // Only the first report after the user's write may use the
        // departure; a later, unrelated one never does.
        var d = user ? null : departure;
        departure = null;
        var resolved = Board.ResolveSelection(snapshot, next);
        if (d is { } dep && next.Selection == dep.Id && resolved != dep.Id)
        {
            // The selected case left after the user's write.
            next = next with { Selection = dep.Next };
            resolved = Board.ResolveSelection(snapshot, next);
        }
        next = next with { Selection = resolved };
        if (next.Selection != State.Selection)
        {
            next = next with { RevealsWhy = false };
        }
        var view = Board.View(snapshot, next, Now, culture, timeZone);

        var changes = Changes.None;
        if (next.Style != State.Style)
        {
            changes |= Changes.Style;
        }
        if (next.Filter != State.Filter || next.Account != State.Account)
        {
            changes |= Changes.Filters;
        }
        if (next.Selection != State.Selection || next.RevealsWhy != State.RevealsWhy || view.ShowsPanel != View.ShowsPanel)
        {
            changes |= Changes.Selection;
        }
        if (ContentDiffers(view, View))
        {
            changes |= Changes.Content;
        }
        State = next;
        View = view;
        Deliver(changes);
        RequestMessages();
    }

    // Asks the source for the selected case's conversation when another case
    // is selected or the selected one changed since it last asked.
    private void RequestMessages()
    {
        var snapshot = Source.Snapshot;
        if (State.Selection is not { } id || snapshot.FindCase(id) is not { } c)
        {
            requested = null;
            return;
        }
        var phase = snapshot.Phase;
        if (requested is { } r && r.Id == id && r.Version == c.Version)
        {
            // The same case: asked again only when its load failed and the
            // board has since come back from a failure (a reconnect).
            if (!c.MessagesFailed || r.Phase == phase || !r.Phase.IsFailure || phase.IsFailure)
            {
                requested = (id, c.Version, phase);
                return;
            }
        }
        requested = (id, c.Version, phase);
        Source.LoadMessages(id);
    }

    // The conversation of case id could not be loaded.
    private bool FailedLoad(BoardCaseId? id) => id is { } x && Source.Snapshot.FindCase(x) is { MessagesFailed: true };

    // Tells Changed about changes. Called again from inside Changed, it only
    // collects them: the outer delivery hands them over once the listener
    // returns, as many times as new ones came, each time with View as it is
    // then.
    private void Deliver(Changes changes)
    {
        pending |= changes;
        if (notifying)
        {
            return;
        }
        notifying = true;
        try
        {
            while (pending != Changes.None)
            {
                var next = pending;
                pending = Changes.None;
                Changed?.Invoke(this, next);
            }
        }
        finally
        {
            notifying = false;
        }
    }

    // Whether the views differ beyond the selection: another selected case's
    // detail is a selection change, the same case's is content.
    private static bool ContentDiffers(Board.ViewModel a, Board.ViewModel b)
    {
        a = a with { Selection = null, ShowsPanel = false };
        b = b with { Selection = null, ShowsPanel = false };
        if (a.Detail?.Id != b.Detail?.Id)
        {
            a = a with { Detail = null };
            b = b with { Detail = null };
        }
        return a != b;
    }
}
