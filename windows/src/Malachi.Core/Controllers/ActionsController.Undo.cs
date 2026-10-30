// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/window/undo.go (recordUndo, recordBoardUndo,
// showUndoToast, dismissUndoToast, undoLast, runUndo, afterUndo) and the
// recording halves of actions.go (trashIDs, moveListTo) and
// context_menus.go (emptyFolder's forgetting); no Swift port exists yet.
//
// Move to Trash, Archive, Junk, Move To and the board's Archive and Delete
// push what they moved once the daemon took it (Undo.History), with a
// toast whose button takes that move back; Ctrl+Z (UndoLast) takes back the
// newest move left. Taking back is message.move of each message to the
// folder it came from (a moved message keeps its id); the board's moves go
// through the board (UndoBoard: BoardController.UndoArchive, which also
// puts the case back). Afterwards the account's folders are read again for
// their counts and the folder on display is listed again, the messages
// that came back to it selected (IActionsList.ReloadSelecting).
//
// Windows: the toast with a button is a Toast with a ToastButton on the
// window's overlay (IActionToasts, Malachi.App ToastHost); without one
// (a controller of the tests) the move is recorded and no toast shows. The shortcut on the
// button is Windows' Ctrl+Z, as GTK's.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.ContextMenus;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Malachi.Core.Undos;

namespace Malachi.Core.Controllers;

public sealed partial class ActionsController
{
    /// <summary>
    /// The shortcut shown on the toast's button (undo.go
    /// <c>undoShortcut</c>); a key name, never translated.
    /// </summary>
    public const string UndoShortcut = "Ctrl+Z";

    /// <summary>How long the toast of a move stays (undo.go <c>undoToastSeconds</c>); Ctrl+Z works after it.</summary>
    public const int UndoToastSeconds = 8;

    // The toast of the newest move (undo.go undoToast), taken away when
    // another move or an Undo replaces it.
    private Toast? undoToast;

    /// <summary>The session's moves of mail Undo takes back (undo.go <c>w.undo</c>).</summary>
    public Undo.History UndoHistory { get; } = new();

    /// <summary>
    /// The window's overlay for a toast with a button (the main window's
    /// ToastHost); without one a move is recorded without a toast.
    /// </summary>
    public IActionToasts? ActionToasts { get; set; }

    /// <summary>
    /// Takes back a board's move (undo.go <c>runUndo</c>'s board half:
    /// <c>Controller.UndoArchive</c>, which moves the messages back and
    /// puts the case back on the board, and says itself when that failed).
    /// Without one Undo of a board move says it failed.
    /// </summary>
    public Action<Undo.Entry>? UndoBoard { get; set; }

    /// <summary>
    /// The board's Archive or Delete (undo.go <c>recordBoardUndo</c>, the
    /// board controller's <c>ArchiveDone</c>): pushed with a toast whose
    /// button takes it back; a board move without moved messages or Undo
    /// gets the plain toast (only the case was marked done).
    /// </summary>
    public void RecordBoardUndo(Board.ArchiveOutcome outcome)
    {
        ArgumentNullException.ThrowIfNull(outcome);
        Mailbox.Scope.VerifyAccess();
        if (outcome.UndoLabel is null || Undo.FromBoard(outcome.Trash, outcome.Account, outcome.Case, outcome.Moved) is not { } e)
        {
            toast(outcome.Text);
            return;
        }
        var role = outcome.Trash ? FolderRole.Trash : FolderRole.Archive;
        if (Mailbox.Model.FolderByRole(outcome.Account, role) is { } f)
        {
            e = e with { Target = f.Id };
        }
        e = UndoHistory.Push(e);
        ShowUndoToast(e, outcome.Text);
    }

    /// <summary>
    /// Ctrl+Z (undo.go <c>undoLast</c>, win.undo): the newest move is taken
    /// back; with none left, a toast says so.
    /// </summary>
    public void UndoLast()
    {
        Mailbox.Scope.VerifyAccess();
        if (UndoHistory.Pop() is not { } e)
        {
            toast(Undo.Nothing());
            return;
        }
        RunUndo(e);
    }

    // actions.go's trashIDs and moveListTo once the daemon took the move
    // (undo.go recordUndo): the move of list (summaries taken before their
    // rows went) to target is pushed and its toast shown; nothing moved,
    // nothing recorded.
    private void RecordUndo(Undo.Kind kind, FolderId target, IReadOnlyList<MessageSummary> list)
    {
        if (Undo.Record(kind, target, list) is not { } e)
        {
            return;
        }
        if (kind == Undo.Kind.Move && Mailbox.Model.Folder(new FolderKey(e.Account, target)) is { } f)
        {
            e = e with { TargetName = f.Name };
        }
        e = UndoHistory.Push(e);
        ShowUndoToast(e, Undo.Done(e));
    }

    // The kind of a move to the folder with role (MoveToRole).
    private static Undo.Kind UndoKindOf(FolderRole role) =>
        role == FolderRole.Archive ? Undo.Kind.Archive : role == FolderRole.Junk ? Undo.Kind.Junk : Undo.Kind.Move;

    // undo.go showUndoToast: text with the button that takes e back, in
    // place of the toast of an earlier move.
    private void ShowUndoToast(Undo.Entry e, string text)
    {
        DismissUndoToast();
        if (ActionToasts is not { } toasts)
        {
            return;
        }
        var id = e.Id;
        undoToast = toasts.Show(new Toast(text, UndoToastSeconds, new ToastButton(Undo.Button(UndoShortcut), () =>
        {
            if (UndoHistory.Take(id) is { } taken)
            {
                RunUndo(taken);
            }
        })));
    }

    // undo.go dismissUndoToast: the toast of the last move goes.
    private void DismissUndoToast()
    {
        if (undoToast is { } t)
        {
            undoToast = null;
            ActionToasts?.Dismiss(t);
        }
    }

    // undo.go runUndo: e is taken back. The board's moves through the board
    // (which puts the case back and says itself when that failed), the
    // mail's by message.move per source folder, each failure toasted as
    // every failed move is; then the account's counts and the list follow
    // (AfterUndo).
    private void RunUndo(Undo.Entry e)
    {
        Mailbox.Scope.VerifyAccess();
        DismissUndoToast();
        if (Mailbox.Model.Account(e.Account) is not { Enabled: true })
        {
            toast(Undo.Failed());
            return;
        }
        if (e.Kind.IsBoard())
        {
            if (UndoBoard is not { } board)
            {
                toast(Undo.Failed());
                return;
            }
            board(e);
            AfterUndo(e);
            return;
        }
        var calls = Undo.Calls(e);
        var left = calls.Count;
        var failed = false;
        if (left == 0)
        {
            AfterUndo(e);
            return;
        }
        void Finish()
        {
            if (--left > 0)
            {
                return;
            }
            if (!failed)
            {
                toast(Undo.Undone(e));
            }
            AfterUndo(e);
        }
        foreach (var c in calls)
        {
            Call(
                API.MessageMove,
                c,
                ContextMenu.MovingText(c.MessageIds.Count),
                onError: _ =>
                {
                    failed = true;
                    Finish();
                },
                onOk: _ => Finish());
        }
    }

    // undo.go afterUndo: the account's folders are read again for their
    // counts, and the folder on display is listed again when it is one the
    // messages left or came back to, selecting those that came back to it.
    // Not while a search shows its results.
    private void AfterUndo(Undo.Entry e)
    {
        Mailbox.LoadFolders(e.Account, Mailbox.Model.FoldersGen);
        if (Mailbox.Model.Selected is not { } sel || sel.Account != e.Account || Mailbox.Model.Search.Active)
        {
            return;
        }
        var back = Undo.Reselect(e, sel.Account, sel.Folder);
        if (back.Count == 0 && sel.Folder != e.Target)
        {
            return;
        }
        List.ReloadSelecting(back);
    }
}
