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

// boardReply is win.board-reply and the detail header's own Reply (or
// Comment) button: opens the case's reply or comment (openReply,
// compose_open.go), or gives the keyboard to the inline editor a later
// agent's ui/internal/boardreply fills into reply_slot when the case
// already links a suggested draft (Detail.DraftID) — never a second
// compose window over the one being edited there. boardFocusReply is nil
// until that editor exists.
func (w *Window) boardReply() {
	p := w.boardPage
	if p == nil {
		return
	}
	d := p.ctl.View().Detail
	if d == nil || d.Reply == nil {
		return
	}
	if d.DraftID != "" {
		if w.boardFocusReply != nil {
			w.boardFocusReply()
		}
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

// apiBoardState is a convenience cast where the action's string parameter
// (one of the four nicks board.State.API uses) becomes api.BoardState.
func apiBoardState(s string) boardAPIState { return boardAPIState(s) }

// applyActionsSensitivity enables the win.board-* actions for the current
// selection: nothing while the window is not in Board, or no case is
// selected.
func (p *boardPage) applyActionsSensitivity(vm board.ViewModel) {
	on := p.w.mode == board.ModeBoard && vm.Detail != nil
	for _, name := range []string{"board-mark-done", "board-reopen", "board-archive", "board-unflag", "board-reply", "board-show-in-mail"} {
		if a := p.w.actions[name]; a != nil {
			a.SetEnabled(on)
		}
	}
	if on {
		d := *vm.Detail
		p.w.actions["board-mark-done"].SetEnabled(!d.IsDone)
		p.w.actions["board-reopen"].SetEnabled(d.IsDone)
		p.w.actions["board-archive"].SetEnabled(!d.IsDone)
		p.w.actions["board-unflag"].SetEnabled(d.CanUnstar)
		p.w.boardMoveToAction.SetEnabled(!d.IsDone)
		p.w.boardMoveToAction.SetState(glib.NewVariantString(string(d.State.API())))
	} else {
		p.w.boardMoveToAction.SetEnabled(false)
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
		remindMenu.Append(choice.Title+" "+choice.When, "boardctx."+name)
	}
	if _, snoozed := d.RemindText, d.IsSnoozed; snoozed {
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
	if !d.IsDone {
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
	pm.SetParent(anchor)
	rect := gdkRectangleAt(x, y)
	pm.SetPointingTo(&rect)
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
