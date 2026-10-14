// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package compose

import (
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/boardreply"
)

// Pane satisfies boardreply.Pane directly (ComposeDraftBoardOwnerTests'
// guarantees, ported to draftController in draft.go): a pane the board
// makes with PaneOptions{Layout: LayoutInline, Owner: OwnerBoard} over
// boardreply.Params is what boardreply.Panes holds and calls Settle,
// Close, Abandon, HasUnsavedText, IsSending, IsLost and ReplyTitle on.
var _ boardreply.Pane = (*Pane)(nil)

// FromBoardParams turns a board reply's params into compose.Params for
// NewPane, like compose.FromDraft does for a window (the draft is already
// what Editor's ready phase holds: the case's linked draft as draft.get
// returned it). Comment and its issue, if any, go with it.
func FromBoardParams(p boardreply.Params) Params {
	out := FromDraft(KindEdit, p.Draft, api.BlockedContent{})
	out.AccountID = p.Account
	return out
}

// Settle saves everything typed and calls done once, on the main loop,
// with whether nothing typed is left unsaved; never cleans up (boardreply.Pane).
func (p *Pane) Settle(done func(ok bool)) { p.dc.settle(done) }

// Finish is Settle followed by cleanup when it succeeded: the board calls
// this, not Settle, whenever the pane really goes (its case is no longer
// selected and nothing links it back). done mirrors Settle's outcome.
func (p *Pane) Finish(done func(ok bool)) {
	p.dc.finish(func(ok bool) {
		if p.dc.draft.closed {
			p.finishTeardown()
		}
		if done != nil {
			done(ok)
		}
	})
}

// Close ends the pane for good once nothing is at stake: the draft
// controller's cleanup, never its finish, and never asks (boardreply.Pane).
// Idempotent.
func (p *Pane) Close() {
	p.dc.cleanup()
	p.finishTeardown()
}

// Abandon forgets the pane without saving: its draft is gone
// (boardreply.Pane). Idempotent.
func (p *Pane) Abandon() {
	p.dc.abandon()
	p.finishTeardown()
}

// HasUnsavedText reports edits not yet saved, or a save under way
// (boardreply.Pane).
func (p *Pane) HasUnsavedText() bool { return p.dc.draft.dirty || p.dc.draft.saving }

// IsSending reports that Send was pressed and has not answered yet
// (boardreply.Pane).
func (p *Pane) IsSending() bool { return p.dc.draft.sending }

// IsLost reports the draft was deleted elsewhere (boardreply.Pane).
func (p *Pane) IsLost() bool { return p.dc.draft.lost }

// finishTeardown releases the widgets once the draft controller has
// closed (cleanup already unregistered the inline pictures' cid:s and
// cancelled the autosave): the pending recipient searches must not touch
// the rows any more, and the editor's web process ends at once (it would
// otherwise live on until the Go wrapper is collected, and a case detail
// whose panes come and go would pile processes up). Idempotent.
func (p *Pane) finishTeardown() {
	if p.tornDown {
		return
	}
	p.tornDown = true
	for _, s := range p.suggest {
		s.hide()
	}
	for _, f := range []*recipientField{p.to, p.cc, p.bcc} {
		f.close()
	}
	p.editor.Close()
}

// SetDialogParent sets the host for file pickers and discard confirmation.
func (p *Pane) SetDialogParent(w *gtk.Window) { p.dialogParent = w }

// FocusEditorStart focuses the beginning once the editor bridge is ready.
// False asks the host to try again after the pane has mapped.
func (p *Pane) FocusEditorStart() bool {
	p.FocusEditor()
	if !p.editor.Ready() {
		return false
	}
	p.editor.FocusStart()
	return true
}

// EditorHasFocus distinguishes typing from focus in a recipient or button.
func (p *Pane) EditorHasFocus() bool {
	if p.dialogParent == nil || p.dialogParent.Focus() == nil {
		return false
	}
	f := gtk.BaseWidget(p.dialogParent.Focus())
	return f == &p.editor.Widget || f.IsAncestor(p.editor)
}

// PopupOpen says whether a popup of the pane is open: a recipient field's
// suggestions, the link popover, or the paragraph-style and alignment
// menus. Escape closes that first (board.EscapeFor's EscapeClosePopup);
// the popup's own handler does, so a host that sees true lets the key go.
func (p *Pane) PopupOpen() bool {
	for _, s := range p.suggest {
		if s.popover.Visible() {
			return true
		}
	}
	if p.linkPopover.Visible() {
		return true
	}
	for _, b := range []*gtk.MenuButton{p.blockButton, p.alignButton} {
		if b.Active() {
			return true
		}
	}
	return false
}

// KeyboardInside says whether the keyboard is in the pane (its editor, a
// recipient field, a button of its own), as the focus of the dialog
// parent (SetDialogParent) says; false without one. Escape then moves the
// keyboard to the host's state pill (board.EscapeFor's
// EscapeFocusStatePill), which the board page does itself.
func (p *Pane) KeyboardInside() bool {
	if p.dialogParent == nil || p.dialogParent.Focus() == nil {
		return false
	}
	// The root box itself takes no focus: inside means below it.
	return gtk.BaseWidget(p.dialogParent.Focus()).IsAncestor(p.root)
}
