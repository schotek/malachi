// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/toolbar"
)

// The Mail mode's customizable header bars (toolbar_host.go): the
// folders' (section sidebar), the list's (list) and the message's
// (message); More Actions offers the actions the arrangement does not
// show and what the overflow left out (T8).

// mailToolbar is the Mail mode's toolbar state.
type mailToolbar struct {
	set                      *toolbarSet
	folders, list, message   *tbHeader
	faces                    []*tbFace
	markRead                 *gtk.Button
	moveTo                   *gtk.MenuButton
	contacts                 *gtk.Button
	junk, undo, loadPictures *gtk.Button
	print                    *gtk.Button
	// more is More Actions' model: extra, then message_menu_model.
	more, extra *gio.Menu
}

// toolbarItemsBuilder is a fresh toolbar_items.blp: each part of the
// window takes its own widgets.
func toolbarItemsBuilder() *gtk.Builder { return data.Builder("toolbar_items.ui") }

// mailToolbarActions maps the items of the Mail toolbar to their action
// in More Actions; an item without one is not offered there.
var mailToolbarActions = map[toolbar.ID]string{
	toolbar.NewMessage:   "app.compose",
	toolbar.Contacts:     "app.contacts",
	toolbar.Refresh:      "win.refresh",
	toolbar.Search:       "win.search",
	toolbar.Reply:        "win.reply",
	toolbar.ReplyAll:     "win.reply-all",
	toolbar.Forward:      "win.forward",
	toolbar.Archive:      "win.archive",
	toolbar.Trash:        "win.trash",
	toolbar.Star:         "win.toggle-flag",
	toolbar.Junk:         "win.junk",
	toolbar.Undo:         "win.undo",
	toolbar.LoadPictures: "win.load-images",
	toolbar.Print:        "win.print",
}

// setupMailToolbar registers the Mail mode's items and header bars from
// window.blp's builder and packs the stored arrangement; called once
// from New, after the Assistant's and eWay-CRM's buttons are bound.
func (w *Window) setupMailToolbar(b *gtk.Builder) {
	ib := toolbarItemsBuilder()
	t := &mailToolbar{
		set:          newToolbarSet(),
		markRead:     ib.GetObject("toolbar_mark_read").Cast().(*gtk.Button),
		moveTo:       ib.GetObject("toolbar_move_to").Cast().(*gtk.MenuButton),
		contacts:     ib.GetObject("toolbar_contacts").Cast().(*gtk.Button),
		junk:         ib.GetObject("toolbar_junk").Cast().(*gtk.Button),
		undo:         ib.GetObject("toolbar_undo").Cast().(*gtk.Button),
		loadPictures: ib.GetObject("toolbar_load_pictures").Cast().(*gtk.Button),
		print:        ib.GetObject("toolbar_print").Cast().(*gtk.Button),
	}
	w.mailBar = t
	s := t.set
	newMessage := b.GetObject("new_message_button").Cast().(*gtk.Button)
	crmSlot := b.GetObject("crm_slot").Cast().(*gtk.Box)
	s.add(toolbar.Mode, w.modeSwitchMail)
	faced := []struct {
		id toolbar.ID
		w  gtk.Widgetter
	}{
		{toolbar.NewMessage, newMessage}, {toolbar.Contacts, t.contacts},
		{toolbar.Refresh, w.refreshButton}, {toolbar.Search, w.searchButton},
		{toolbar.Reply, w.replyButton}, {toolbar.ReplyAll, w.replyAllButton},
		{toolbar.Forward, w.forwardButton}, {toolbar.Archive, w.archiveButton},
		{toolbar.Trash, w.trashButton}, {toolbar.Star, w.starButton},
		{toolbar.Assistant, w.assistButton}, {toolbar.MoreActions, w.messageMenu},
		{toolbar.MarkRead, t.markRead}, {toolbar.Junk, t.junk}, {toolbar.MoveTo, t.moveTo},
		{toolbar.Undo, t.undo}, {toolbar.LoadPictures, t.loadPictures},
		{toolbar.Print, t.print},
	}
	for _, f := range faced {
		s.add(f.id, f.w)
		label := toolbar.ItemLabel(i18n.Tr, toolbar.BarMail, f.id)
		t.faces = append(t.faces, addFace(f.w, label))
	}
	s.add(toolbar.CRM, crmSlot)
	s.selfSpaced[toolbar.CRM] = true
	for _, x := range []struct {
		b  gtk.Widgetter
		id toolbar.ID
	}{{t.contacts, toolbar.Contacts}, {t.junk, toolbar.Junk}, {t.undo, toolbar.Undo}, {t.loadPictures, toolbar.LoadPictures}, {t.moveTo, toolbar.MoveTo}, {t.markRead, toolbar.MarkRead}, {t.print, toolbar.Print}} {
		gtk.BaseWidget(x.b).SetTooltipText(toolbar.ItemLabel(i18n.Tr, toolbar.BarMail, x.id))
	}
	t.moveTo.SetCreatePopupFunc(func(*gtk.MenuButton) {
		rows := w.selectedRows()
		if len(rows) == 0 {
			t.moveTo.SetMenuModel(gio.NewMenu())
			return
		}
		t.moveTo.SetMenuModel(w.moveToMenu(rows[0].Message.AccountID, commonFolder(rows)))
	})
	t.moveTo.SetSensitive(false)

	t.folders = s.addHeader(b.GetObject("folders_header").Cast().(*adw.HeaderBar)).on(toolbar.BarMail, toolbar.SectionSidebar)
	t.list = s.addHeader(b.GetObject("list_header").Cast().(*adw.HeaderBar)).on(toolbar.BarMail, toolbar.SectionList)
	t.message = s.addHeader(b.GetObject("message_header").Cast().(*adw.HeaderBar)).on(toolbar.BarMail, toolbar.SectionMessage)
	t.folders.column = func() int { f, _, _ := w.mailColumnWidths(); return f }
	t.list.column = func() int { _, l, _ := w.mailColumnWidths(); return l }
	t.message.column = func() int { _, _, m := w.mailColumnWidths(); return m }
	for _, h := range []*tbHeader{t.folders, t.list, t.message} {
		s.watchFit(h)
		wireCustomizeMenu(h.header, func() toolbar.Bar { return toolbar.BarMail }, w.openToolbarEditor)
	}

	// More Actions: the missing and overflowed actions first.
	t.more, t.extra = gio.NewMenu(), gio.NewMenu()
	t.more.AppendSection("", t.extra)
	if m, ok := b.GetObject("message_menu_model").Cast().(*gio.Menu); ok {
		t.more.AppendSection("", m)
	}
	w.messageMenu.SetMenuModel(t.more)
	w.messageMenu.SetCreatePopupFunc(func(*gtk.MenuButton) { w.fillMoreActions() })

	toolbarHome = func() (toolbar.Bar, func() toolbar.Features) { return w.currentToolbarBar(), w.toolbarFeatures }
	w.applyMailToolbar()
	if st := w.settings; st != nil {
		st.OnChanged(toolbar.Key(toolbar.BarMail), w.applyMailToolbar)
		st.OnChanged(toolbar.DisplayModeKey, w.applyToolbarDisplayMode)
	}
	w.applyToolbarDisplayMode()
}

// mailColumnWidths are the widths the Mail header bars' columns would get
// (mailColumns) in the window as it is now.
func (w *Window) mailColumnWidths() (folders, list, message int) {
	width := w.Width()
	panel := 0
	if w.assistantPanel != nil && w.assistantPanel.split != nil {
		sp := w.assistantPanel.split
		if sp.ShowSidebar() && !sp.Collapsed() {
			panel = assistantPanelWidth(width)
		}
	}
	return mailColumns(width, panel, w.outerSplit.Collapsed(), w.innerSplit.Collapsed())
}

// toolbarLayout is bar's arrangement as GTK reads the stored key.
func (w *Window) toolbarLayout(bar toolbar.Bar) toolbar.Layout {
	var stored []string
	if w.settings != nil {
		stored = w.settings.ToolbarLayout(bar)
	}
	return toolbar.NormalizeFor(gtkVariant, bar, stored)
}

// toolbarDisplayMode is the stored display mode.
func (w *Window) toolbarDisplayMode() toolbar.DisplayMode {
	if w.settings == nil {
		return toolbar.DisplayIcon
	}
	return w.settings.ToolbarDisplayMode()
}

// toolbarFeatures are the conditional items' features as the window has
// them (T7): an eWay-CRM account, the Assistant, the board's triage (the
// In App target) and printing (win.print, print.go).
func (w *Window) toolbarFeatures() toolbar.Features {
	have := map[toolbar.Feature]bool{toolbar.FeaturePrint: true}
	if _, ok := w.crmAccount(); ok {
		have[toolbar.FeatureCRM] = true
	}
	if w.assist != nil && w.assist.shown() {
		have[toolbar.FeatureAssistant] = true
	}
	if w.assist != nil && w.assist.panelShown() {
		have[toolbar.FeatureTriage] = true
	}
	return toolbar.Features{Have: have}
}

// applyMailToolbar packs the Mail toolbar's arrangement again.
func (w *Window) applyMailToolbar() {
	if w.mailBar == nil {
		return
	}
	w.mailBar.set.apply(toolbar.BarMail, w.toolbarLayout(toolbar.BarMail), w.toolbarFeatures())
}

// applyToolbarDisplayMode shows every faced button of the window's
// toolbars in the stored display mode.
func (w *Window) applyToolbarDisplayMode() {
	m := w.toolbarDisplayMode()
	if w.mailBar != nil {
		applyFaces(w.mailBar.faces, m)
	}
	if p := w.boardPage; p != nil && p.toolbar != nil {
		applyFaces(p.toolbar.faces, m)
	}
	if p := w.cal.page; p != nil && p.toolbar != nil {
		applyFaces(p.toolbar.faces, m)
	}
}

// moreActionItems are More Actions' first section for the arrangement l,
// the features f and the items the overflow left out: the actions l does
// not show (toolbar.MissingActionsFor), then the overflowed ones, each
// once and only with an action; eWay-CRM has its own section already.
func moreActionItems(l toolbar.Layout, f toolbar.Features, overflow []toolbar.ID) []toolbar.ID {
	seen := map[toolbar.ID]bool{toolbar.CRM: true}
	var out []toolbar.ID
	for _, id := range append(toolbar.MissingActionsFor(gtkVariant, toolbar.BarMail, l, f), overflow...) {
		if seen[id] {
			continue
		}
		seen[id] = true
		if _, ok := mailToolbarActions[id]; ok || id == toolbar.MarkRead || id == toolbar.MoveTo {
			out = append(out, id)
		}
	}
	return out
}

// fillMoreActions refills More Actions' first section as it opens.
func (w *Window) fillMoreActions() {
	t := w.mailBar
	if t == nil {
		return
	}
	t.extra.RemoveAll()
	ids := moreActionItems(w.toolbarLayout(toolbar.BarMail), w.toolbarFeatures(), t.set.overflowed())
	for _, id := range ids {
		label := toolbar.ItemLabel(i18n.Tr, toolbar.BarMail, id)
		switch id {
		case toolbar.MarkRead:
			// Both, each while it applies, as message_menu_model has them.
			for _, x := range []struct{ msgid, action string }{
				{"Mark as _Read", "win.mark-read"}, {"Mark as _Unread", "win.mark-unread"},
			} {
				item := gio.NewMenuItem(toolbar.StripMnemonic(i18n.T(x.msgid)), x.action)
				item.SetAttributeValue("hidden-when", glib.NewVariantString("action-disabled"))
				t.extra.AppendItem(item)
			}
		case toolbar.MoveTo:
			rows := w.selectedRows()
			item := gio.NewMenuItem(label, "")
			if len(rows) > 0 && w.actions["move-to"] != nil && w.actions["move-to"].Enabled() {
				item.SetSubmenu(w.moveToMenu(rows[0].Message.AccountID, commonFolder(rows)))
			}
			t.extra.AppendItem(item)
		default:
			t.extra.Append(label, mailToolbarActions[id])
		}
	}
}

// updateMailToolbarItems follows the selection with the items that have
// no action of their own to follow: Mark as Read/Unread as one button and
// Move To (called by setMessageActionsSensitive).
func (w *Window) updateMailToolbarItems(st actionState) {
	t := w.mailBar
	if t == nil {
		return
	}
	icon, action, msgid := "mail-read-symbolic", "win.mark-read", "Mark as _Read"
	if !st.markRead && st.markUnread {
		icon, action, msgid = "mail-unread-symbolic", "win.mark-unread", "Mark as _Unread"
	}
	label := toolbar.StripMnemonic(i18n.T(msgid))
	if t.markRead.ActionName() != action {
		t.markRead.SetActionName(action)
	}
	t.markRead.SetTooltipText(label)
	setFace(t.markRead, icon, label)
	t.moveTo.SetSensitive(st.move)
}
