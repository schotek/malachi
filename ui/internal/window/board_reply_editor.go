// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/boardreply"
	"github.com/schotek/malachi/ui/internal/compose"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// The GTK counterpart of BoardReplyEditorHost: lifecycle decisions belong
// to boardreply.Panes; this host only makes compose panes and places their
// widgets. The detail tree moves intact between List, Columns and Today,
// so neither a style change nor an autosave rebuilds a live editor.
func (p *boardPage) initBoardReplies() {
	if p.daemon == nil || p.w.compose == nil {
		return
	}
	p.replyEditor = boardreply.NewEditor(p.w.client, glibLoop{}, p.w.log)
	panes := boardreply.NewPanes[*compose.Pane](p.replyEditor, boardreply.Timing{}, glibLoop{}, i18n.Tr)
	p.replyPanes = panes
	panes.Make = p.makeBoardReply
	panes.Detach = p.releaseBoardReply
	panes.OnToast = p.w.Toast
	panes.OnChange = p.boardReplySlotChanged
	panes.OnAdopt = func(pane *compose.Pane) {
		id := p.replyFocusPending
		if id == "" || panes.Slot(id).Pane != pane {
			return
		}
		p.replyFocusPending = ""
		glib.IdleAdd(func() { p.focusBoardPane(pane, 0) })
	}
	if p.w.mode != board.ModeBoard {
		p.suspendBoardReplies()
	}
}

func (p *boardPage) updateBoardReplies() {
	if p.replyPanes == nil || p.replySuspended || p.replyClosed {
		return
	}
	var selected *board.Case
	if d := p.ctl.View().Detail; d != nil {
		if c, ok := p.src.Snapshot().Case(d.ID); ok {
			selected = &c
		}
	}
	if selected == nil || p.replyFocusPending != selected.ID {
		p.replyFocusPending = ""
	}
	p.replyPanes.Show(selected)
}

func (p *boardPage) makeBoardReply(key boardreply.Key, params boardreply.Params) (*compose.Pane, bool) {
	pane := compose.NewPane(p.w.compose, compose.FromBoardParams(params), compose.PaneOptions{
		Layout: compose.LayoutInline, Owner: compose.OwnerBoard,
	})
	pane.SetDialogParent(&p.w.Window)
	pane.OnToast = p.w.Toast
	pane.OnSendFailed = func() { p.replyPanes.SendFailed(pane) }
	pane.OnLost = func() { p.endBoardReply(pane, boardreply.End{Kind: boardreply.EndLost}) }
	pane.OnEnd = func(end compose.End) {
		p.endBoardReply(pane, boardReplyEnd(end))
	}
	pane.DiscardStored = func(account api.AccountID, draft api.DraftID, done func(error)) {
		p.replyPanes.Discarding(pane, true)
		p.ctl.DiscardStoredDraft(key.CaseID, draft, account, func(err error) {
			if err != nil {
				p.replyPanes.Discarding(pane, false)
			}
			done(err)
		})
	}
	var previous float64
	pane.OnHeight = func(height float64) {
		grew := height > previous
		previous = height
		if grew && pane.EditorHasFocus() {
			// Wait for GTK to allocate the new document height before scrolling.
			glib.IdleAdd(func() { p.revealBoardReply(pane, true) })
		}
	}
	// The scroller's adjustment page size changes with every allocation,
	// unlike GtkWidget's read-only height, which has no notify signal.
	if scroller := p.boardReplyScroller(); scroller != nil {
		adj := scroller.VAdjustment()
		signal := adj.ConnectChanged(func() { pane.SetVisibleHeight(adj.PageSize()) })
		if p.replyHeightDisconnect == nil {
			p.replyHeightDisconnect = make(map[*compose.Pane]func())
		}
		p.replyHeightDisconnect[pane] = func() { adj.HandlerDisconnect(signal) }
		pane.SetVisibleHeight(adj.PageSize())
	}
	p.w.compose.RegisterInline(pane)
	return pane, true
}

func boardReplyEnd(end compose.End) boardreply.End {
	kind := boardreply.EndClosed
	switch end.Kind {
	case compose.EndSent:
		kind = boardreply.EndSent
	case compose.EndDiscarded:
		kind = boardreply.EndDiscarded
	}
	return boardreply.End{Kind: kind, Text: end.Text}
}

func (p *boardPage) releaseBoardReply(pane *compose.Pane) {
	hadFocus := p.keyboardInBoardReply(pane)
	p.w.compose.RemoveInline(pane)
	if disconnect := p.replyHeightDisconnect[pane]; disconnect != nil {
		disconnect()
		delete(p.replyHeightDisconnect, pane)
	}
	// Retired panes can finish after another pane has occupied the slot.
	if p.replyShown == pane {
		removeAllChildren(p.replySlot)
		p.replyShown, p.replyUnsaved = nil, nil
	}
	pane.OnHeight, pane.OnToast, pane.OnSendFailed, pane.OnLost, pane.OnEnd = nil, nil, nil, nil, nil
	if hadFocus {
		p.restoreBoardReplyFocus()
	}
}

// Remember focus before Close terminates WebKit and releases its focus.
func (p *boardPage) endBoardReply(pane *compose.Pane, end boardreply.End) {
	hadFocus := p.keyboardInBoardReply(pane)
	p.replyPanes.Ended(pane, end)
	if hadFocus {
		p.restoreBoardReplyFocus()
	}
}

func (p *boardPage) restoreBoardReplyFocus() {
	glib.IdleAdd(func() {
		if !p.replyClosed && p.w.Visible() && p.w.mode == board.ModeBoard && p.w.Focus() == nil && p.statePill != nil {
			p.statePill.GrabFocus()
		}
	})
}

func (p *boardPage) keyboardInBoardReply(pane *compose.Pane) bool {
	f := p.w.Focus()
	return f != nil && gtk.BaseWidget(f).IsAncestor(pane.Widget())
}

func (p *boardPage) boardReplySlotChanged() {
	if p.replyClosed {
		return
	}
	if d := p.ctl.View().Detail; d != nil {
		p.renderDetailReplySlot(*d)
	}
}

func (p *boardPage) renderDetailReplySlot(d board.Detail) {
	slot := boardreply.Slot[*compose.Pane]{}
	if p.replyPanes != nil && !p.replySuspended {
		slot = p.replyPanes.Slot(d.ID)
	}
	if slot.Kind == boardreply.SlotPane && p.replyShown == slot.Pane {
		p.replyUnsaved.SetVisible(slot.Unsaved)
		p.replySlot.SetVisible(true)
		return
	}
	p.replyShown, p.replyUnsaved = nil, nil
	if slot.Kind == boardreply.SlotNone {
		p.replySlot.SetVisible(p.renderSuggestReply(d))
		return
	}
	removeAllChildren(p.replySlot)
	box := gtk.NewBox(gtk.OrientationVertical, 6)
	box.AddCSSClass("card")
	box.SetMarginTop(4)
	contents := gtk.NewBox(gtk.OrientationVertical, 6)
	for _, set := range []func(int){contents.SetMarginTop, contents.SetMarginBottom, contents.SetMarginStart, contents.SetMarginEnd} {
		set(12)
	}
	box.Append(contents)
	label := func(text string) *gtk.Label {
		l := gtk.NewLabel(text)
		l.SetUseMarkup(false)
		l.SetWrap(true)
		l.SetXAlign(0)
		return l
	}
	heading := label(board.DraftHeading(i18n.Tr))
	heading.AddCSSClass("heading")
	contents.Append(heading)
	switch slot.Kind {
	case boardreply.SlotLoading:
		row := gtk.NewBox(gtk.OrientationHorizontal, 6)
		spinner := gtk.NewSpinner()
		spinner.Start()
		row.Append(spinner)
		row.Append(label(board.ReplyLoading(i18n.Tr)))
		contents.Append(row)
	case boardreply.SlotFailed:
		contents.Append(label(board.ReplyLoadFailed(i18n.Tr)))
		if slot.Retry {
			retry := gtk.NewButtonWithLabel(board.TryAgain(i18n.Tr))
			retry.SetHAlign(gtk.AlignStart)
			retry.AddCSSClass("flat")
			retry.ConnectClicked(p.replyEditor.Retry)
			contents.Append(retry)
		}
	case boardreply.SlotPane:
		note := label(board.DraftNote(i18n.Tr))
		note.AddCSSClass("dim-label")
		contents.Append(note)
		unsaved := label(board.ReplyNotSaved(i18n.Tr))
		unsaved.AddCSSClass("warning")
		unsaved.SetVisible(slot.Unsaved)
		contents.Append(unsaved)
		// An unsaved parked pane may still belong to its old, detached card.
		root := gtk.BaseWidget(slot.Pane.Widget())
		if parent := root.Parent(); parent != nil {
			gtk.BaseWidget(parent).Cast().(*gtk.Box).Remove(root)
		}
		contents.Append(slot.Pane.Widget())
		p.replyShown, p.replyUnsaved = slot.Pane, unsaved
	}
	p.replySlot.Append(box)
	p.replySlot.SetVisible(true)
}

func (p *boardPage) focusBoardReply(id board.CaseID) {
	if p.replyPanes == nil || p.replyClosed {
		return
	}
	if p.ctl.State().Selection != id {
		p.ctl.Select(id)
	}
	slot := p.replyPanes.Slot(id)
	if slot.Kind == boardreply.SlotPane {
		p.focusBoardPane(slot.Pane, 0)
		return
	}
	p.replyFocusPending = id
}

func (p *boardPage) focusBoardPane(pane *compose.Pane, tries int) {
	if p.replyClosed || p.replySuspended || p.replyShown != pane || !p.w.Visible() {
		return
	}
	if !pane.FocusEditorStart() && tries < 30 {
		glib.TimeoutAdd(100, func() bool { p.focusBoardPane(pane, tries+1); return false })
	}
	p.revealBoardReply(pane, false)
}

// boardReplyScroller finds the fixed detail scroller through the slot's
// ancestors, also when the entire detail tree has moved to the panel.
func (p *boardPage) boardReplyScroller() *gtk.ScrolledWindow {
	for parent := p.replySlot.Parent(); parent != nil; parent = gtk.BaseWidget(parent).Parent() {
		if s, ok := gtk.BaseWidget(parent).Cast().(*gtk.ScrolledWindow); ok {
			return s
		}
	}
	return nil
}

func (p *boardPage) revealBoardReply(pane *compose.Pane, bottom bool) {
	if p.replyClosed || p.replySuspended || p.replyShown != pane {
		return
	}
	scroller := p.boardReplyScroller()
	if scroller == nil {
		return
	}
	root := gtk.BaseWidget(pane.Widget())
	bounds, ok := root.ComputeBounds(scroller)
	if !ok {
		return
	}
	adj := scroller.VAdjustment()
	y := float64(bounds.Y()) + adj.Value()
	if bottom {
		y += float64(bounds.Height()) - 1
	}
	adj.ClampPage(y, y+1)
}

func (p *boardPage) suspendBoardReplies() {
	if p.conversation != nil {
		p.conversation.refresh()
	}
	p.replySuspended, p.replyFocusPending = true, ""
	if p.replyPanes != nil {
		p.replyPanes.Suspend()
	}
}

// FinishBoardReplies settles while the daemon is still connected. The
// application asks before closing when the bounded wait could not save
// text or finish an in-flight send/discard.
func (w *Window) FinishBoardReplies(done func(bool)) {
	p := w.boardPage
	if p == nil || p.replyPanes == nil {
		done(true)
		return
	}
	p.replySuspended, p.replyFocusPending = true, ""
	p.replyPanes.QuitWait(5*time.Second, func() { done(!p.replyPanes.HasUnsavedOrUnsent()) })
}

// ResumeBoardReplies restores a reply after returning to Board or cancelling quit.
func (w *Window) ResumeBoardReplies() {
	p := w.boardPage
	if p == nil || p.replyClosed || w.mode != board.ModeBoard {
		return
	}
	p.replySuspended = false
	if p.replyPanes != nil {
		p.replyPanes.Resume()
		p.updateBoardReplies()
	}
}

// CloseBoardReplies releases the closed main window, after Finish or the
// user's explicit decision to leave unsaved replies behind.
func (w *Window) CloseBoardReplies() {
	if w.boardAutoStartRemove != nil {
		w.boardAutoStartRemove()
		w.boardAutoStartRemove = nil
	}
	p := w.boardPage
	if p == nil || p.replyClosed {
		return
	}
	p.replyClosed, p.replySuspended = true, true
	if p.conversation != nil {
		p.conversation.close()
	}
	p.closeBoardSuggestReply()
	p.ctl.OnChange, p.ctl.OnToast = nil, nil
	if p.daemon != nil {
		p.daemon.Stop()
	}
	if p.triageRemoveObserve != nil {
		p.triageRemoveObserve()
	}
	if p.triageRemoveEnded != nil {
		p.triageRemoveEnded()
	}
	if p.replyPanes != nil {
		p.replyPanes.OnChange, p.replyPanes.OnToast, p.replyPanes.OnAdopt = nil, nil, nil
		p.replyPanes.Close()
		for _, pane := range p.replyPanes.All() {
			pane.Abandon()
			p.replyPanes.Ended(pane, boardreply.End{Kind: boardreply.EndLost})
		}
	}
}
