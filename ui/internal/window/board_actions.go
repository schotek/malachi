// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/jira"
)

// The board's actions (win.board-*, enabled only in Board mode with a case
// selected; action_rules.go's mail equivalent is messageActionState), the
// Remind popover (a dynamic set of presets, like the detail header's
// Remind… button) and the case row's context menu (right-click or long
// press: Move To, Mark as Done / Move Back to Board, Remind Me, Archive,
// Unstar, Reply, Show in Mail — the same targets as the header bar's
// buttons, acting on the selection a right-click makes first).

// registerBoardActions adds the win.board-* actions, once. Their enabled
// state follows the mode and the selection (applyActionsSensitivity).
func (w *Window) registerBoardActions() {
	move := gio.NewSimpleActionStateful("board-move-to", glib.NewVariantType("s"), glib.NewVariantString(""))
	move.ConnectActivate(func(v *glib.Variant) {
		if v == nil || w.boardPage == nil {
			return
		}
		if st, ok := board.StateFromAPI(apiBoardState(v.String())); ok {
			if id := w.boardPage.ctl.State().Selection; id != "" {
				w.boardPage.ctl.SetState(id, st)
			}
		}
	})
	w.AddAction(move)
	w.actions["board-move-to"] = move
	w.boardMoveToAction = move

	forSelection := func(fn func(*boardPage, board.CaseID)) func() {
		return func() {
			p := w.boardPage
			if p == nil {
				return
			}
			if id := p.ctl.State().Selection; id != "" {
				fn(p, id)
			}
		}
	}
	w.addAction("board-mark-done", false, forSelection(func(p *boardPage, id board.CaseID) { p.ctl.MarkDone(id) }))
	w.addAction("board-reopen", false, forSelection(func(p *boardPage, id board.CaseID) { p.ctl.Reopen(id) }))
	w.addAction("board-archive", false, forSelection(func(p *boardPage, id board.CaseID) { p.ctl.Archive(id) }))
	w.addAction("board-unflag", false, forSelection(func(p *boardPage, id board.CaseID) { p.ctl.Unflag(id) }))
	w.addAction("board-reply", false, func() { w.boardReply() })
	w.addAction("board-show-in-mail", false, func() { w.boardShowInMailSelected() })
}

// boardReply opens a reply or focuses the linked draft's inline editor
// (enabled only when the case has either, boardCanReply).
func (w *Window) boardReply() {
	p := w.boardPage
	if p == nil {
		return
	}
	d := p.ctl.View().Detail
	if d == nil {
		return
	}
	if d.DraftID != "" {
		p.focusBoardReply(d.ID)
		return
	}
	if d.Reply == nil {
		return
	}
	w.openReply(d.AccountID, d.Reply.Message, w.boardCaseComments(*d), d.Person)
}

// boardShowInMailSelected is win.board-show-in-mail and the detail
// header's "…" menu: Show in Mail on the current selection
// (board_show_in_mail.go boardPage.showInMail).
func (w *Window) boardShowInMailSelected() {
	p := w.boardPage
	if p == nil {
		return
	}
	if d := p.ctl.View().Detail; d != nil {
		p.showInMail(*d)
	}
}

// boardActionGates is which win.board-* actions a detail allows, pure and
// tested without GTK (the macOS client's gating, BoardActions.swift):
// Archive while the case is not done or the source still can archive it
// (Detail.CanArchive); Reply with a message to answer or a linked draft
// to edit; Show in Mail with a message to show (the reply target, else the
// newest), or on the samples, which only say they cannot.
type boardActionGates struct {
	markDone, reopen, archive, unflag, reply, showInMail, moveTo bool
}

func boardGatesFor(d board.Detail, samples bool) boardActionGates {
	return boardActionGates{
		markDone:   !d.IsDone,
		reopen:     d.IsDone,
		archive:    d.CanArchive || !d.IsDone,
		unflag:     d.CanUnstar,
		reply:      d.Reply != nil || d.DraftID != "",
		showInMail: samples || d.Reply != nil || d.LatestMessage != "",
		moveTo:     !d.IsDone,
	}
}

// apiBoardState is a convenience cast where the action's string parameter
// (one of the four nicks board.State.API uses) becomes api.BoardState.
func apiBoardState(s string) boardAPIState { return boardAPIState(s) }

// applyActionsSensitivity enables the win.board-* actions for the current
// selection: nothing while the window is not in Board, or no case is
// selected; otherwise boardGatesFor.
func (p *boardPage) applyActionsSensitivity(vm board.ViewModel) {
	var g boardActionGates
	on := p.w.mode == board.ModeBoard && vm.Detail != nil
	if on {
		g = boardGatesFor(*vm.Detail, p.daemon == nil)
	}
	for name, enabled := range map[string]bool{
		"board-mark-done": g.markDone, "board-reopen": g.reopen, "board-archive": g.archive,
		"board-unflag": g.unflag, "board-reply": g.reply, "board-show-in-mail": g.showInMail,
	} {
		if a := p.w.actions[name]; a != nil {
			a.SetEnabled(enabled)
		}
	}
	p.w.boardMoveToAction.SetEnabled(g.moveTo)
	// Remind… is a MenuButton (its popover is built on open), not an
	// action: its sensitivity follows the selection directly.
	p.remindButton.SetSensitive(on)
	if on {
		p.w.boardMoveToAction.SetState(glib.NewVariantString(string(vm.Detail.State.API())))
	}
}

// boardStateMenu is the state pill's and the context menu's "Move To": the
// four states on win.board-move-to, which GTK renders as a radio group
// (same stateful action, one target each) with a check on the current one.
func (w *Window) boardStateMenu() *gio.Menu {
	m := gio.NewMenu()
	for _, st := range board.States {
		m.Append(board.StateName(st, i18n.Tr), "win.board-move-to::"+string(st.API()))
	}
	return m
}

// boardStatePillClass is the state pill's colour class (internal/style);
// "" for Info, the neutral pill.
func boardStatePillClass(s board.State) string {
	switch s {
	case board.StateHot:
		return "board-state-hot"
	case board.StateYou:
		return "board-state-you"
	case board.StateThem:
		return "board-state-them"
	}
	return ""
}

// boardDetailMenu is the detail header's "…" menu: Show in Mail.
func boardDetailMenu() *gio.Menu {
	m := gio.NewMenu()
	m.Append(board.ShowInMail(i18n.Tr), "win.board-show-in-mail")
	return m
}

// wireDetailHeaderMenus fills the Remind button's popover when it opens
// (RemindPresets depend on "now", like the macOS and GTK Jira transition
// menus fill on open).
func (p *boardPage) wireRemindButton() {
	p.remindButton.SetCreatePopupFunc(func(mb *gtk.MenuButton) {
		if mb.Popover() == nil {
			mb.SetPopover(p.remindPopoverFor(func() board.CaseID { return p.ctl.State().Selection }))
		}
	})
}

// remindPopoverFor is a Remind… popover: the presets for now, then Don't
// Remind Me when the case is already snoozed. caseID is read when the
// popover opens, not when it is built, so the same popover instance (kept
// by the button) always acts on the selection of the moment.
func (p *boardPage) remindPopoverFor(caseID func() board.CaseID) *gtk.Popover {
	pop := gtk.NewPopover()
	box := gtk.NewBox(gtk.OrientationVertical, 0)
	pop.SetChild(box)
	pop.ConnectShow(func() {
		removeAllChildren(box)
		id := caseID()
		for _, choice := range p.ctl.RemindPresets() {
			choice := choice
			row := gtk.NewButton()
			row.AddCSSClass("flat")
			inner := gtk.NewBox(gtk.OrientationHorizontal, 12)
			title := gtk.NewLabel(choice.Title)
			title.SetUseMarkup(false)
			title.SetXAlign(0)
			title.SetHExpand(true)
			inner.Append(title)
			when := gtk.NewLabel(choice.When)
			when.SetUseMarkup(false)
			when.AddCSSClass("dim-label")
			inner.Append(when)
			row.SetChild(inner)
			row.ConnectClicked(func() {
				pop.Popdown()
				at := choice.Date
				p.ctl.Remind(id, &at)
			})
			box.Append(row)
		}
		if k, ok := p.ctl.Source().Snapshot().Case(id); ok {
			if _, snoozed := k.Visibility.RemindAt(); snoozed {
				stop := gtk.NewButton()
				stop.AddCSSClass("flat")
				stop.SetLabel(board.RemindNoMore(i18n.Tr))
				stop.ConnectClicked(func() {
					pop.Popdown()
					p.ctl.Remind(id, nil)
				})
				box.Append(stop)
			}
		}
	})
	return pop
}

// showCaseContextMenu is a right-click or long press on row's case id:
// selects it first (the menu then acts on "the selection", the same win.*
// actions the header bar uses), then shows the same actions as a
// PopoverMenu, with its own Remind Me submenu (an ephemeral action group,
// since the presets are built fresh from "now").
func (p *boardPage) showCaseContextMenu(anchor *gtk.Widget, id board.CaseID, x, y float64) {
	p.ctl.Select(id)
	d := p.ctl.View().Detail
	if d == nil || d.ID != id {
		return
	}

	group := gio.NewSimpleActionGroup()
	remindMenu := gio.NewMenu()
	for i, choice := range p.ctl.RemindPresets() {
		name := "remind" + itoa10(i)
		a := gio.NewSimpleAction(name, nil)
		at := choice.Date
		a.ConnectActivate(func(*glib.Variant) { p.ctl.Remind(id, &at) })
		group.AddAction(a)
		remindMenu.Append(choice.Label, "boardctx."+name)
	}
	if d.IsSnoozed {
		a := gio.NewSimpleAction("remind-none", nil)
		a.ConnectActivate(func(*glib.Variant) { p.ctl.Remind(id, nil) })
		group.AddAction(a)
		remindMenu.Append(board.RemindNoMore(i18n.Tr), "boardctx.remind-none")
	}

	top := gio.NewMenu()
	moveItem := gio.NewMenuItem(board.MoveTo(i18n.Tr), "")
	moveItem.SetSubmenu(p.w.boardStateMenu())
	top.AppendItem(moveItem)
	if d.IsDone {
		top.Append(board.NotDone(i18n.Tr), "win.board-reopen")
	} else {
		top.Append(board.MarkAsDone(i18n.Tr), "win.board-mark-done")
	}
	remindItem := gio.NewMenuItem(board.RemindMe(i18n.Tr), "")
	remindItem.SetSubmenu(remindMenu)
	top.AppendItem(remindItem)
	if boardGatesFor(*d, p.daemon == nil).archive {
		top.Append(board.Archive(i18n.Tr), "win.board-archive")
	}
	if d.CanUnstar {
		top.Append(board.Unstar(i18n.Tr), "win.board-unflag")
	}

	bottom := gio.NewMenu()
	bottom.Append(jiraReplyOrComment(p.w, *d), "win.board-reply")
	bottom.Append(board.ShowInMail(i18n.Tr), "win.board-show-in-mail")

	m := gio.NewMenu()
	m.AppendSection("", top)
	m.AppendSection("", bottom)

	pm := gtk.NewPopoverMenuFromModel(m)
	pm.InsertActionGroup("boardctx", group)
	// The menu hangs on the list that holds the row, not on the row: a
	// refresh may take the row out of its list (renderList) while the menu
	// is open, and a row going with a popover still parented to it warns
	// "still has children" and leaves the menu on a freed parent. The list
	// stays; the click's point is translated into it.
	parent := gtk.BaseWidget(p.root)
	if list := anchor.Ancestor(gtk.GTypeListBox); list != nil {
		parent = gtk.BaseWidget(list)
	}
	if px, py, ok := anchor.TranslateCoordinates(parent, x, y); ok {
		x, y = px, py
	}
	pm.SetParent(parent)
	rect := gdkRectangleAt(x, y)
	pm.SetPointingTo(&rect)
	// A popover set on a widget stays its child until unparented: a menu
	// per right-click would pile up on the list. Closed, it lets go of the
	// list once its own closing has run (idle: an activated item's action
	// runs first).
	pm.ConnectClosed(func() {
		glib.IdleAdd(func() {
			if pm.Parent() != nil {
				pm.Unparent()
			}
		})
	})
	pm.Popup()
}

// jiraReplyOrComment is the context menu's bottom section label: "Comment"
// on an issue tracker account, "Reply" otherwise (jira.ReplyLabel), the
// same decision the detail header's own button makes
// (w.boardCaseComments, board_detail.go).
func jiraReplyOrComment(w *Window, d board.Detail) string {
	return jira.ReplyLabel(w.boardCaseComments(d), i18n.Tr)
}

// gdkRectangleAt is a 1×1 rectangle at a click's coordinates, for a
// PopoverMenu's SetPointingTo (as compose's suggestions popover points at
// its field, compose/suggest.go).
func gdkRectangleAt(x, y float64) gdk.Rectangle {
	return gdk.NewRectangle(int(x), int(y), 1, 1)
}

// itoa10 avoids pulling in strconv just for a loop counter in an ephemeral
// action name.
func itoa10(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
