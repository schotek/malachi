// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/contextmenu"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/undo"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Undo of moves (ui/internal/undo): Move to Trash, Archive, Junk, Move To
// and the board's Archive and Delete push what they moved once the daemon
// took it, with a toast whose button takes that move back; Ctrl+Z
// (win.undo) takes back the newest move left. Taking back is message.move
// of each message to the folder it came from (a moved message keeps its
// id); the board's moves also put the case back (Controller.UndoArchive).

// undoShortcut is the shortcut shown on the toast's button; a key name,
// never translated.
const undoShortcut = "Ctrl+Z"

// undoToastSeconds is how long the toast stays; Ctrl+Z works after it.
const undoToastSeconds = 8

// undoAccelAllowed is the gate of win.undo's Ctrl+Z: in Mail and Board,
// and never while the keyboard is in a text field or an editor, whose own
// Ctrl+Z takes back typing.
func undoAccelAllowed(m board.Mode, typingAllows bool) bool {
	return m != board.ModeCalendar && typingAllows
}

// recordUndo pushes the move of list (summaries taken before their rows
// went) to target with kind, once the daemon took it, and shows its toast.
// Nothing moved, nothing recorded.
func (w *Window) recordUndo(kind undo.Kind, target api.FolderID, list []api.MessageSummary) {
	e, ok := undo.Record(kind, target, list)
	if !ok {
		return
	}
	if kind == undo.Move {
		if f, ok := w.model.folder(folderKey{Account: e.Account, Folder: target}); ok {
			e.TargetName = f.Name
		}
	}
	e = w.undo.Push(e)
	w.showUndoToast(e, undo.Done(e, i18n.Tr))
}

// recordBoardUndo pushes the board's Archive or Delete (o) and shows its
// toast with Undo; a board move without moved messages gets the plain
// toast (only the case was marked done).
func (w *Window) recordBoardUndo(o board.ArchiveOutcome) {
	e, ok := undo.FromBoard(o.Trash, o.Account, string(o.Case), o.Moved)
	if !ok || o.UndoLabel == "" {
		w.Toast(o.Text)
		return
	}
	role := api.RoleArchive
	if o.Trash {
		role = api.RoleTrash
	}
	if f, ok := w.model.folderByRole(o.Account, role); ok {
		e.Target = f.ID
	}
	e = w.undo.Push(e)
	w.showUndoToast(e, o.Text)
}

// showUndoToast shows text with the button that takes e back, in place of
// the toast of an earlier move.
func (w *Window) showUndoToast(e undo.Entry, text string) {
	w.dismissUndoToast()
	t := widget.PlainToast(text)
	t.SetTimeout(undoToastSeconds)
	t.SetButtonLabel(undo.Button(undoShortcut, i18n.Tr))
	id := e.ID
	t.ConnectButtonClicked(func() {
		if e, ok := w.undo.Take(id); ok {
			w.runUndo(e)
		}
	})
	t.ConnectDismissed(func() {
		if w.undoToast == t {
			w.undoToast = nil
		}
	})
	w.undoToast = t
	w.toasts.AddToast(t)
}

// dismissUndoToast takes the toast of the last move away.
func (w *Window) dismissUndoToast() {
	if t := w.undoToast; t != nil {
		w.undoToast = nil
		t.Dismiss()
	}
}

// undoLast is win.undo (Ctrl+Z): the newest move is taken back.
func (w *Window) undoLast() {
	e, ok := w.undo.Pop()
	if !ok {
		w.Toast(undo.Nothing(i18n.Tr))
		return
	}
	w.runUndo(e)
}

// runUndo takes e back: the board's moves through the board (which puts
// the case back and says itself when that failed), the mail's by
// message.move per source folder; then the account's counts and the list
// are read again and the messages that came back to the folder on display
// are selected.
func (w *Window) runUndo(e undo.Entry) {
	w.dismissUndoToast()
	if a, ok := w.model.account(e.Account); !ok || !a.Enabled {
		w.Toast(undo.Failed(i18n.Tr))
		return
	}
	if e.Kind.Board() {
		w.ensureBoard()
		moved := make([]api.BoardMoved, 0, len(e.Moves))
		for _, m := range e.Moves {
			moved = append(moved, api.BoardMoved{MessageID: m.MessageID, FromFolderID: m.FromFolderID})
		}
		w.boardPage.ctl.UndoArchive(board.ArchiveOutcome{
			Case: board.CaseID(e.Case), Account: e.Account, Moved: moved, Trash: e.Kind == undo.BoardTrash,
		})
		w.afterUndo(e)
		return
	}
	calls := undo.Calls(e)
	left, failed := len(calls), false
	finish := func() {
		if left--; left > 0 {
			return
		}
		if !failed {
			w.Toast(undo.Undone(e, i18n.Tr))
		}
		w.afterUndo(e)
	}
	for _, c := range calls {
		w.callThen(contextmenu.MovingText(i18n.Tr, len(c.MessageIDs)), api.MethodMessageMove, c,
			func() { failed = true; finish() }, finish)
	}
}

// afterUndo reads the account's folders again for their counts and lists
// the folder on display again when it is one the messages left or came
// back to, selecting those that came back to it (takeUndoReselect).
func (w *Window) afterUndo(e undo.Entry) {
	w.loadFolders(e.Account, w.model.foldersGen)
	sel := w.model.selected
	if sel.Account != e.Account || w.model.search.active {
		return
	}
	back := undo.Reselect(e, sel.Account, sel.Folder)
	if len(back) == 0 && sel.Folder != e.Target {
		return
	}
	w.undoReselect = back
	w.loadMessages()
}

// takeUndoReselect selects the rows of the messages Undo brought back to
// the folder just listed again (a message's row, or its conversation's),
// once; called when a first page of the list arrived.
func (w *Window) takeUndoReselect() {
	back := w.undoReselect
	w.undoReselect = nil
	if len(back) == 0 {
		return
	}
	var keys []listKey
	for _, m := range back {
		for _, k := range []listKey{{Message: m.MessageID}, {Thread: m.ThreadID, Message: m.MessageID}, {Thread: m.ThreadID}} {
			if k.Thread == "" && k.Message == "" {
				continue
			}
			if w.rows[k] != nil && !containsKey(keys, k) {
				keys = append(keys, k)
				break
			}
		}
	}
	if len(keys) == 0 {
		return
	}
	w.reselecting = true
	w.selectKeys(keys[1:])
	w.reselecting = false
	// The first row selected last runs the selection handler for them all.
	w.messageList.SelectRow(w.rows[keys[0]].ListBoxRow)
}

func containsKey(keys []listKey, k listKey) bool {
	for _, x := range keys {
		if x == k {
			return true
		}
	}
	return false
}
