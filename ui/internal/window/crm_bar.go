// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/crm"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// The bar under the header of a message saved in eWay-CRM (crm.BarFor;
// the macOS CRMBarView): a check mark, "Saved in eWay-CRM" over the names
// of the linked items (and the private hint when the saved item is
// private), and Open in eWay-CRM. Shown in the reading pane and the
// message window (crm_bar in window.blp and message_window.blp) and on
// the conversation's cards (made when first needed), never for an
// attached message. Every name is plain text.
//
// Open in eWay-CRM opens the saved item's URL in the browser at once,
// without the "Open this link?" question the mail's links get: the URL is
// the daemon's, built from the account's stored service address and an
// item GUID of fixed shape (docs/eway.md D15), never taken from mail or
// from the CRM, and crm.OpenURL lets https through only.

// crmBar is the bar's widgets.
type crmBar struct {
	box                *gtk.Box
	title, names, note *gtk.Label
	open               *gtk.Button
	url                string
	// shown is the bar on display, to leave the widgets alone when
	// nothing changed.
	shown *crm.Bar
}

// bindCrmBar finds the bar of a builder that has one (window.blp,
// message_window.blp); nil otherwise.
func bindCrmBar(b *gtk.Builder, launch func(string)) *crmBar {
	obj := b.GetObject("crm_bar")
	if obj == nil {
		return nil
	}
	label := func(id string) *gtk.Label { return b.GetObject(id).Cast().(*gtk.Label) }
	bar := &crmBar{
		box:   obj.Cast().(*gtk.Box),
		title: label("crm_bar_title"),
		names: label("crm_bar_names"),
		note:  label("crm_bar_note"),
		open:  b.GetObject("crm_bar_open").Cast().(*gtk.Button),
	}
	bar.wire(launch)
	return bar
}

// newCrmBar builds the bar of a conversation card as window.blp's crm_bar.
func newCrmBar(launch func(string)) *crmBar {
	bar := &crmBar{
		box:   gtk.NewBox(gtk.OrientationHorizontal, 12),
		title: plainLabel(""),
		names: plainLabel("", "caption", "dim-label"),
		note:  plainLabel("", "caption", "dim-label"),
		open:  gtk.NewButton(),
	}
	bar.box.AddCSSClass("toolbar")
	bar.box.AddCSSClass("remote-bar")
	bar.box.AddCSSClass("crm-bar")
	bar.box.SetVisible(false)
	icon := gtk.NewImageFromIconName("object-select-symbolic")
	icon.SetVAlign(gtk.AlignCenter)
	text := gtk.NewBox(gtk.OrientationVertical, 1)
	text.SetHExpand(true)
	text.SetVAlign(gtk.AlignCenter)
	for _, l := range []*gtk.Label{bar.title, bar.names, bar.note} {
		l.SetWrap(true)
		l.SetWrapMode(pango.WrapWordChar)
		text.Append(l)
	}
	bar.open.SetVAlign(gtk.AlignCenter)
	bar.box.Append(icon)
	bar.box.Append(text)
	bar.box.Append(bar.open)
	bar.wire(launch)
	return bar
}

func (bar *crmBar) wire(launch func(string)) {
	for _, l := range []*gtk.Label{bar.title, bar.names, bar.note} {
		l.SetUseMarkup(false)
	}
	bar.open.SetLabel(crm.MenuLabel(crm.MenuOpen, i18n.Tr))
	bar.open.ConnectClicked(func() {
		if u := crm.OpenURL(bar.url); u != "" {
			launch(u)
		}
	})
}

// show shows the bar of rec (crm.BarFor), or hides it for none. The focus
// leaves the bar before it goes away, for the reason given in
// newMessageView; focusTo takes it.
func (bar *crmBar) show(rec *api.CrmRecord, focusTo gtk.Widgetter) {
	b, ok := crm.BarFor(rec, i18n.Tr)
	if !ok {
		if bar.shown != nil && bar.box.FocusChild() != nil && focusTo != nil {
			gtk.BaseWidget(focusTo).GrabFocus()
		}
		bar.shown = nil
		bar.box.SetVisible(false)
		return
	}
	if bar.shown != nil && sameCrmBar(*bar.shown, b) && bar.box.Visible() {
		return
	}
	bar.shown = &b
	bar.title.SetText(b.Title)
	bar.names.SetText(b.Names)
	bar.names.SetTooltipText(b.Names)
	bar.names.SetVisible(b.Names != "")
	bar.note.SetText(b.Note)
	bar.note.SetVisible(b.Note != "")
	bar.url = b.URL
	bar.open.SetVisible(b.URL != "")
	bar.box.SetVisible(true)
}

// sameCrmBar reports two bars that look the same.
func sameCrmBar(a, b crm.Bar) bool {
	return a.Title == b.Title && a.Names == b.Names && a.URL == b.URL && a.Note == b.Note && a.OpenLabel == b.OpenLabel
}

// renderCrm shows the bar of message s (m its full message, nil when not
// loaded) on a view that has the bar.
func (v *messageView) renderCrm(s api.MessageSummary, m *api.Message) {
	if v.crmBar == nil {
		return
	}
	var rec *api.CrmRecord
	if s.ID != "" {
		rec = v.win.crmRecord(s, m)
	}
	v.crmBar.show(rec, v.stack)
}

// showCrmBar hides the view's bar (nil) — the pane showing nothing or a
// conversation.
func (v *messageView) showCrmBar(rec *api.CrmRecord) {
	if v.crmBar != nil {
		v.crmBar.show(rec, v.stack)
	}
}

// renderCrm redraws the card's bar; the bar is made when first needed.
func (c *convCard) renderCrm(m *api.Message) {
	w := c.cv.w
	rec := w.crmRecord(c.s, m)
	if rec == nil && c.mv.crmBar == nil {
		return
	}
	if c.mv.crmBar == nil {
		c.mv.crmBar = newCrmBar(func(u string) { w.launchURI(&w.ApplicationWindow.Window, u) })
		c.bars.Append(c.mv.crmBar.box)
	}
	c.mv.crmBar.show(rec, c.body)
}

// conversationRefreshCrm redraws the bars of the conversation's cards.
func (w *Window) conversationRefreshCrm() {
	if w.conv == nil {
		return
	}
	for id, c := range w.conv.cards {
		if c.folded {
			continue
		}
		var fallback *loadedMessage
		if w.conv.ctrl != nil {
			fallback = w.conv.ctrl.loaded[id]
		}
		c.renderCrm(w.crmMessage(id, fallback))
	}
}
