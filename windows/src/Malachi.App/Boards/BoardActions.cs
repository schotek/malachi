// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Board/BoardActions.swift (BoardActions)
// and the hooks MainWindowController+Board.swift's makeBoardActions sets;
// GTK: window/board_actions.go (the win.board-* actions, boardReply,
// remindPopoverFor). What the user can do with a case, in one place for the
// three ways to ask: the detail's bar, its "…" menu and the context menus of
// rows and cards. Done, Remind, Archive and Unstar go to the board
// controller; Reply and Show in Mail need the mail, so the window installs
// them (OnReply, OnShowInMail, from Integration.Board.cs). Reply on a case
// whose suggested reply the inline editor edits gives that editor the
// keyboard instead (InlineReply, stage 5's BoardReplyEditorHost). The
// invented sample cases (MALACHI_BOARD_SAMPLES) have no message behind
// them: Reply and Show in Mail say Board.Text.Later there.

using System;
using System.Collections.Generic;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Controllers;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Boards;

/// <summary>What the user can do with a case of the board.</summary>
public sealed class BoardActions
{
    /// <summary>The actions over <paramref name="controller"/>; <paramref name="samples"/>: the board runs on the invented samples.</summary>
    public BoardActions(BoardController controller, bool samples)
    {
        ArgumentNullException.ThrowIfNull(controller);
        Controller = controller;
        Samples = samples;
    }

    /// <summary>The board controller.</summary>
    public BoardController Controller { get; }

    /// <summary>The board runs on the invented samples (<c>MALACHI_BOARD_SAMPLES</c>).</summary>
    public bool Samples { get; }

    /// <summary>Reply to the case's newest message (a comment on an issue tracker's account).</summary>
    public Action<Board.Case>? OnReply { get; set; }

    /// <summary>Switch to Mail and show the case's message.</summary>
    public Action<Board.Case>? OnShowInMail { get; set; }

    /// <summary>A short message over the page.</summary>
    public Action<string>? OnToast { get; set; }

    /// <summary>Asks before Discard deletes the samples' suggested reply; null asks nothing.</summary>
    public Func<Task<bool>>? ConfirmDiscard { get; set; }

    /// <summary>Whether a reply in account <paramref name="account"/> is a comment (an issue tracker's account); null: never.</summary>
    public Func<AccountId, bool>? CommentsOn { get; set; }

    /// <summary>
    /// The page's inline reply editor (stage 5, BoardReplyEditorHost): Reply
    /// gives it the keyboard when it edits the case's suggested reply. Null
    /// until then: Reply opens a compose window.
    /// </summary>
    public IBoardInlineReply? InlineReply { get; set; }

    /// <summary>The case as the source has it.</summary>
    public Board.Case? Case(BoardCaseId id) => Controller.Source.Snapshot.FindCase(id);

    // What can be done

    /// <summary>Reply answers in a compose window, or edits the case's suggested reply inline.</summary>
    public bool CanReply(BoardCaseId id) =>
        Case(id) is { } c && (Samples || (c.Reply is not null && OnReply is not null) || InlineReply?.EditsInline(id) == true);

    /// <summary>Unstar is offered (<see cref="Board.CanUnstar"/>).</summary>
    public bool CanUnstar(BoardCaseId id) => Case(id) is { } c && Board.CanUnstar(c);

    /// <summary>
    /// Archive moves the inbox messages where the account can and marks the
    /// case done; without anything to move it only marks it done, which a
    /// done case is already.
    /// </summary>
    public bool CanArchive(BoardCaseId id) => Case(id) is { } c && (c.CanArchive || !c.Visibility.IsDone);

    /// <summary>Show in Mail has a message to show.</summary>
    public bool CanShowInMail(BoardCaseId id) =>
        Case(id) is { } c && (Samples || ((c.Reply is not null || c.LatestMessage is not null) && OnShowInMail is not null));

    /// <summary>The case is snoozed (Don't Remind Me is offered).</summary>
    public bool IsSnoozed(BoardCaseId id) => Case(id)?.Visibility.RemindAt is not null;

    /// <summary>Reply's label: Comment on an issue tracker's account (jira.ReplyLabel).</summary>
    public string ReplyLabel(AccountId account) => Core.IssueTrackers.Jira.ReplyLabel(CommentsOn?.Invoke(account) == true);

    // Doing it

    /// <summary>Done, or Move Back to Board for a done case.</summary>
    public void ToggleDone(BoardCaseId id)
    {
        if (Case(id) is not { } c)
        {
            return;
        }
        if (c.Visibility.IsDone)
        {
            Controller.Reopen(id);
        }
        else
        {
            Controller.MarkDone(id);
        }
    }

    /// <summary>Hides the case until <paramref name="until"/>; null puts a snoozed one back.</summary>
    public void Remind(BoardCaseId id, DateTimeOffset? until) => Controller.Remind(id, until);

    /// <summary>Archive, where it can do anything.</summary>
    public void Archive(BoardCaseId id)
    {
        if (CanArchive(id))
        {
            Controller.Archive(id);
        }
    }

    /// <summary>Moves the case to <paramref name="state"/> (the state pill's and Move To's items).</summary>
    public void SetState(BoardCaseId id, Board.State state) => Controller.SetState(state, id);

    /// <summary>
    /// With a suggested reply its inline editor takes the keyboard;
    /// otherwise a compose window answers.
    /// </summary>
    public void Reply(BoardCaseId id)
    {
        if (Case(id) is not { } c)
        {
            return;
        }
        if (InlineReply is { } inline && inline.EditsInline(id))
        {
            inline.FocusReply(id);
            return;
        }
        if (Samples || c.Reply is null || OnReply is not { } reply)
        {
            if (Samples)
            {
                OnToast?.Invoke(Board.Text.Later);
            }
            return;
        }
        reply(c);
    }

    /// <summary>Removes the star that keeps the case hot (<c>board.unflag</c>).</summary>
    public void Unstar(BoardCaseId id)
    {
        if (CanUnstar(id))
        {
            Controller.Unflag(id);
        }
    }

    /// <summary>
    /// Drops the samples' suggested reply after the question the compose
    /// window asks before discarding a message (the daemon's board discards
    /// from the inline editor's own Discard).
    /// </summary>
    public async Task DiscardDraftAsync(BoardCaseId id)
    {
        if (ConfirmDiscard is { } confirm && !await confirm())
        {
            return;
        }
        Controller.DiscardDraft(id);
    }

    /// <summary>Show in Mail; the samples have no message to show.</summary>
    public void ShowInMail(BoardCaseId id)
    {
        if (Case(id) is not { } c)
        {
            return;
        }
        if (Samples || OnShowInMail is not { } show || (c.Reply is null && c.LatestMessage is null))
        {
            if (Samples)
            {
                OnToast?.Invoke(Board.Text.Later);
            }
            return;
        }
        show(c);
    }

    // Menus

    /// <summary>
    /// Remind…'s items for case <paramref name="id"/> into
    /// <paramref name="items"/>: the presets for now, the time beside each,
    /// and Don't Remind Me for a snoozed case.
    /// </summary>
    public void FillRemindMenu(IList<MenuFlyoutItemBase> items, BoardCaseId id)
    {
        ArgumentNullException.ThrowIfNull(items);
        items.Clear();
        var n = 0;
        foreach (var preset in Controller.RemindPresets())
        {
            var at = preset.Date;
            var item = BoardMenuItem.Make(preset.Title, () => Remind(id, at), automationId: "BoardRemindPreset" + n++);
            // The time at the trailing edge, as macOS's subtitle and GTK's dim label.
            item.KeyboardAcceleratorTextOverride = preset.When;
            items.Add(item);
        }
        if (IsSnoozed(id))
        {
            items.Add(new MenuFlyoutSeparator());
            items.Add(BoardMenuItem.Make(Board.Text.RemindNoMore, () => Remind(id, null), automationId: "BoardRemindNoMore"));
        }
    }

    /// <summary>The four states for the state pill's and Move To's menus, the current one checked.</summary>
    public void FillStateMenu(IList<MenuFlyoutItemBase> items, BoardCaseId id)
    {
        ArgumentNullException.ThrowIfNull(items);
        items.Clear();
        var snapshot = Controller.Source.Snapshot;
        Board.State? current = snapshot.FindCase(id) is { } c ? Board.StateOf(c, snapshot.Annotated) : null;
        foreach (var s in Board.States)
        {
            var state = s;
            items.Add(BoardMenuItem.Radio(
                Board.Text.StateName(state), "BoardState", state == current, () => SetState(id, state), "BoardMoveTo" + state));
        }
    }
}
