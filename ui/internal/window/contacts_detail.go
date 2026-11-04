// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"fmt"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/compose"
	"github.com/schotek/malachi/ui/internal/contacts"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settings"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The detail of the Contacts window (W1): the heading (initials, name,
// "Job · Company", the source line, Edit or the Read Only capsule), the
// actions Write Email (a choice with several addresses), Show Mail and
// Copy, and the rows of contacts.DetailOf — e-mail rows write to the
// address, a website row opens only through "Open This Link?" and only
// http(s), a phone row copies the number. Plain text throughout.

// renderDetail shows the form, the card, its loading or an empty state.
func (cw *ContactsWindow) renderDetail() {
	tr := i18n.Tr
	c := cw.contact
	editing := c.Editing()
	cw.cancelButton.SetVisible(editing)
	cw.doneButton.SetVisible(editing)
	cw.doneButton.SetSensitive(!c.Saving())
	cw.editButton.SetVisible(!editing && c.Card() != nil && !c.Card().ReadOnly)
	cw.editButton.SetSensitive(c.CanEdit())
	if e := c.Error(); e != cw.lastContactErr {
		cw.lastContactErr = e
		if !editing {
			cw.toast(e)
		}
	}
	if editing {
		cw.renderForm()
		cw.detailStack.SetVisibleChildName("form")
		return
	}
	cw.form = nil
	card := c.Card()
	switch {
	case card != nil:
		cw.buildDetail(*card)
		cw.detailStack.SetVisibleChildName("detail")
	case c.Loading():
		cw.detailStack.SetVisibleChildName("loading")
	default:
		cw.detailKey = ""
		empty := cw.ctl.DetailEmpty()
		if c.ID() != "" || empty == contacts.EmptyNone {
			empty = contacts.EmptySelection
		}
		title, body := contacts.EmptyText(empty, tr)
		if e := c.Error(); e != "" && c.ID() != "" {
			title, body = e, ""
		}
		cw.detailEmpty.SetTitle(glib.MarkupEscapeText(title))
		cw.detailEmpty.SetDescription(glib.MarkupEscapeText(body))
		cw.openPrefs.SetVisible(empty == contacts.EmptyNoBooks)
		cw.detailPg.SetTitle(contacts.WindowTitle(tr))
		cw.detailStack.SetVisibleChildName("empty")
	}
}

// buildDetail fills detailBox with card unless it shows it already.
func (cw *ContactsWindow) buildDetail(card api.ContactCard) {
	tr := i18n.Tr
	key := fmt.Sprintf("%s|%s|%s", card.ID, card.Revision, cw.contact.SourceLine())
	if key == cw.detailKey {
		return
	}
	cw.detailKey = key
	for ch := cw.detailBox.FirstChild(); ch != nil; ch = cw.detailBox.FirstChild() {
		cw.detailBox.Remove(ch)
	}
	d, _ := cw.contact.Detail()
	cw.detailPg.SetTitle(d.Title)

	head := gtk.NewBox(gtk.OrientationHorizontal, 14)
	head.Append(cw.detailAvatar(card, d))
	texts := gtk.NewBox(gtk.OrientationVertical, 2)
	texts.SetVAlign(gtk.AlignCenter)
	texts.SetHExpand(true)
	texts.Append(contactLabel(d.Title, "title-2"))
	if d.Subtitle != "" {
		texts.Append(contactLabel(d.Subtitle, "dim-label"))
	}
	if s := cw.contact.SourceLine(); s != "" {
		texts.Append(contactLabel(s, "dim-label", "caption"))
	}
	head.Append(texts)
	if d.ReadOnly {
		ro := gtk.NewLabel(contacts.ReadOnly(tr))
		ro.SetVAlign(gtk.AlignCenter)
		ro.AddCSSClass("caption")
		ro.AddCSSClass("dim-label")
		head.Append(ro)
	}
	cw.detailBox.Append(head)
	cw.detailBox.Append(cw.detailActions(d))

	if len(d.Rows) > 0 {
		list := gtk.NewListBox()
		list.SetSelectionMode(gtk.SelectionNone)
		list.AddCSSClass("boxed-list")
		for _, r := range d.Rows {
			list.Append(cw.detailRow(d, r))
		}
		cw.detailBox.Append(list)
	}
}

func contactLabel(text string, classes ...string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.SetUseMarkup(false)
	l.SetXAlign(0)
	l.SetWrap(true)
	l.SetWrapMode(pango.WrapWordChar)
	l.SetSelectable(true)
	for _, c := range classes {
		l.AddCSSClass(c)
	}
	return l
}

// detailActions are Write Email, Show Mail and Copy.
func (cw *ContactsWindow) detailActions(d contacts.Detail) *gtk.Box {
	tr := i18n.Tr
	box := gtk.NewBox(gtk.OrientationHorizontal, 6)
	box.SetHomogeneous(false)
	if len(d.Actions.Emails) > 1 {
		// Several addresses: the button is a choice of them.
		mb := gtk.NewMenuButton()
		mb.SetLabel(contacts.WriteEmail(tr))
		list := gtk.NewBox(gtk.OrientationVertical, 0)
		pop := gtk.NewPopover()
		for _, r := range d.Actions.Emails {
			addr := r.Address
			b := gtk.NewButton()
			l := gtk.NewLabel(contacts.TwoParts(r.Label, r.Value, tr))
			l.SetUseMarkup(false)
			l.SetXAlign(0)
			b.SetChild(l)
			b.AddCSSClass("flat")
			b.ConnectClicked(func() {
				pop.Popdown()
				cw.writeEmail(d.Title, addr)
			})
			list.Append(b)
		}
		pop.SetChild(list)
		mb.SetPopover(pop)
		box.Append(mb)
	} else {
		b := gtk.NewButtonWithLabel(contacts.WriteEmail(tr))
		b.SetSensitive(d.Actions.WriteEmail)
		if len(d.Rows) > 0 && d.Rows[0].Kind == contacts.RowEmail {
			addr := d.Rows[0].Address
			b.ConnectClicked(func() { cw.writeEmail(d.Title, addr) })
		}
		box.Append(b)
	}
	show := gtk.NewButtonWithLabel(contacts.ShowMail(tr))
	show.SetSensitive(d.Actions.ShowMail != "")
	q := d.Actions.ShowMail
	show.ConnectClicked(func() { cw.main.showMailSearch(q) })
	box.Append(show)
	cp := gtk.NewButtonWithLabel(contacts.Copy(tr))
	cp.ConnectClicked(func() { cw.copyText(cw.contact.CopyText()) })
	box.Append(cp)
	return box
}

// detailRow is a row of label and value.
func (cw *ContactsWindow) detailRow(d contacts.Detail, r contacts.DetailRow) *adw.ActionRow {
	row := adw.NewActionRow()
	row.AddCSSClass("property")
	row.SetUseMarkup(false)
	row.SetTitle(r.Label)
	row.SetSubtitle(r.Value)
	row.SetSubtitleLines(0)
	row.SetSubtitleSelectable(true)
	icon := ""
	var activate func()
	switch r.Kind {
	case contacts.RowEmail:
		icon = "mail-message-new-symbolic"
		addr := r.Address
		activate = func() { cw.writeEmail(d.Title, addr) }
	case contacts.RowPhone:
		icon = "edit-copy-symbolic"
		v := r.Value
		activate = func() { cw.copyText(v) }
	case contacts.RowURL:
		if r.URL != "" {
			icon = "adw-external-link-symbolic"
			u := r.URL
			activate = func() { cw.openURL(u) }
		}
	}
	if activate != nil {
		row.AddSuffix(gtk.NewImageFromIconName(icon))
		row.SetActivatable(true)
		row.ConnectActivated(activate)
	}
	return row
}

// writeEmail opens New Message to the address, from the card's account
// when it writes mail.
func (cw *ContactsWindow) writeEmail(name, address string) {
	p := compose.Params{To: []api.Address{{Name: name, Address: address}}}
	if name == contacts.NoName(i18n.Tr) {
		p.To[0].Name = ""
	}
	if card := cw.contact.Card(); card != nil {
		if _, a, ok := cw.ctl.Book(card.BookID); ok && a != nil && a.Can(api.CapabilityCompose) {
			p.AccountID = a.ID
		}
	}
	cw.main.compose.Open(p)
}

// copyText puts text on the clipboard.
func (cw *ContactsWindow) copyText(text string) {
	if text == "" {
		return
	}
	gdk.DisplayGetDefault().Clipboard().SetText(text)
	cw.toast(contacts.Copied(i18n.Tr))
}

// openURL opens a website (contacts.OpenURL already allowed only http and
// https) after "Open This Link?" has shown where it leads.
func (cw *ContactsWindow) openURL(uri string) {
	d := adw.NewAlertDialog(i18n.T("Open This Link?"),
		// TRANSLATORS: %s is the link's real destination.
		fmt.Sprintf(i18n.T("This link leads to %s."), uri))
	d.SetHeadingUseMarkup(false)
	d.SetBodyUseMarkup(false)
	d.AddResponse("cancel", i18n.T("_Cancel"))
	d.AddResponse("open", i18n.T("_Open Link"))
	d.SetResponseAppearance("open", adw.ResponseSuggested)
	d.SetDefaultResponse("cancel")
	d.SetCloseResponse("cancel")
	d.ConnectResponse(func(response string) {
		if response == "open" {
			widget.LaunchURI(&cw.Window.Window, uri, func(err error) {
				if err != nil {
					cw.toast(widget.LaunchErrorText(err))
				}
			})
		}
	})
	d.Present(cw)
}

// showMailSearch is Show Mail: the main window in Mail with the search of
// query over every account.
func (w *Window) showMailSearch(query string) {
	if query == "" {
		return
	}
	w.setMode(board.ModeMail)
	w.Present()
	w.startSearch()
	// The scope for this search only: the stored choice stays the user's
	// (onSearchScopeChanged finds the scope already set).
	w.model.search.scope = settings.SearchAll
	w.searchScope.SetActiveName(string(settings.SearchAll))
	w.searchEntry.SetText(query)
	w.searchEntry.SetPosition(-1)
}
