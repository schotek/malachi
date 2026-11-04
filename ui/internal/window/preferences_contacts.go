// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/contacts"
	"github.com/schotek/malachi/ui/internal/contactsctl"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settings"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Preferences → Contacts (docs/plans/address-book.md W3; the macOS
// ContactsPaneViewController): a group per account with the contacts
// capability in account order over contactsctl.PreferencesController —
// its name with the kind capsule, the switch Sync Contacts, the consent
// row with Allow… (or the Online Accounts advice), the books with their
// checkboxes and, for a CardDAV server, the note that its connection is
// edited on the Calendar page — then the empty state and the group
// Recipient Suggestions (contacts-suggest-scope). The groups are built
// again on the next idle after each change (a change arrives inside a
// row's own handler). Every text is ui/internal/contacts'; every name
// from a server is plain text.

// contactsPrefs is the page's state while the dialog is open.
type contactsPrefs struct {
	d   *PreferencesDialog
	rpc *client.Client
	ctl *contactsctl.PreferencesController

	page     *adw.PreferencesPage
	empty    *adw.PreferencesGroup
	emptyRow *adw.ActionRow
	suggest  *adw.PreferencesGroup
	scopes   []*gtk.CheckButton

	groups []*adw.PreferencesGroup
	// key is what the groups were built for (contactsPrefsKey).
	key string
	// lines describe the groups for the smoke test.
	lines []string

	cancel  func()
	syncing bool
	pending bool
	closed  bool
	lastErr string
}

// bindContacts fills the Contacts page and keeps it with the daemon; s
// keeps Recipient Suggestions (nil: the default, not stored). Without a
// daemon (and without the samples) the page is hidden.
func (d *PreferencesDialog) bindContacts(b *gtk.Builder, c *client.Client, s *settings.Store) (unbind func()) {
	get := func(id string) any { return b.GetObject(id).Cast() }
	page := get("contacts_page").(*adw.PreferencesPage)
	caller := contactsCaller(c)
	if caller == nil {
		page.SetVisible(false)
		return func() {}
	}
	tr := i18n.Tr
	opts := contactsctl.Options{Caller: caller, Loop: glibLoop{}, Tr: tr, Format: widget.Strftime,
		RPCText: widget.RPCErrorText, Log: d.log}
	p := &contactsPrefs{d: d, rpc: c, ctl: contactsctl.NewPreferencesController(opts),
		page:     page,
		empty:    get("contacts_empty_group").(*adw.PreferencesGroup),
		emptyRow: get("contacts_empty_row").(*adw.ActionRow),
		suggest:  get("contacts_suggest_group").(*adw.PreferencesGroup),
	}
	if contactsctl.SamplesOn() {
		p.rpc = nil
	}
	if s != nil {
		p.ctl.Scope = func() api.ContactSearchScope { return api.ContactSearchScope(s.ContactsSuggestScope()) }
		p.ctl.SetScope = func(v api.ContactSearchScope) { s.SetContactsSuggestScope(string(v)) }
	}
	p.ctl.OnChange = p.schedule
	p.ctl.OnSignIn = func(id api.AccountID) { contactsSignIn(p.d, p.rpc, p.d.log, id, p.toast) }
	page.SetTitle(contacts.WindowTitle(tr))
	p.emptyRow.SetTitle(contacts.NoContactAccounts(tr))
	p.buildSuggest()
	p.cancel = listenContacts(func(method string, params json.RawMessage) { p.ctl.Handle(method, params) })
	d.contactsPage = p
	p.ctl.Start()
	p.render()
	return p.stop
}

func (p *contactsPrefs) stop() {
	p.closed = true
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	p.ctl.Stop()
}

// toast shows text on the dialog.
func (p *contactsPrefs) toast(text string) {
	if text != "" && !p.closed {
		p.d.AddToast(widget.PlainToast(text))
	}
}

// schedule renders on the next idle.
func (p *contactsPrefs) schedule() {
	if p.pending || p.closed {
		return
	}
	p.pending = true
	glib.IdleAdd(func() {
		p.pending = false
		if !p.closed {
			p.render()
		}
	})
}

// buildSuggest builds Recipient Suggestions: a check row per scope, one
// group of radio buttons.
func (p *contactsPrefs) buildSuggest() {
	tr := i18n.Tr
	p.suggest.SetTitle(glib.MarkupEscapeText(contacts.SuggestionsGroup(tr)))
	var first *gtk.CheckButton
	for _, sc := range contacts.Scopes {
		row := adw.NewActionRow()
		row.SetUseMarkup(false)
		row.SetTitle(contacts.ScopeText(sc, tr))
		check := gtk.NewCheckButton()
		check.SetVAlign(gtk.AlignCenter)
		if first == nil {
			first = check
		} else {
			check.SetGroup(first)
		}
		scope := sc
		check.ConnectToggled(func() {
			if !p.syncing && check.Active() {
				p.ctl.SetSuggestScope(scope)
			}
		})
		row.AddPrefix(check)
		row.SetActivatableWidget(check)
		p.suggest.Add(row)
		p.scopes = append(p.scopes, check)
	}
}

// contactsPrefsKey is everything the groups show.
func contactsPrefsKey(accounts []contactsctl.PrefsAccount, saving func(api.AccountID) bool) string {
	var b strings.Builder
	for _, a := range accounts {
		fmt.Fprintf(&b, "%s|%s|%s|%v|%v|%v|%v|%v;", a.Account.ID, a.Title, a.Badge, a.SyncOn, a.Consent, a.SignIn,
			a.CardDAV, saving(a.Account.ID))
		for _, bk := range a.Books {
			fmt.Fprintf(&b, "%s|%s|%v|%d;", bk.ID, bk.Name, bk.Visible, bk.Count)
		}
	}
	return b.String()
}

// render shows the controller's state: the groups when they changed, the
// empty state, the scope, the last error as a toast.
func (p *contactsPrefs) render() {
	p.syncing = true
	defer func() { p.syncing = false }()
	if e := p.ctl.Error(); e != p.lastErr {
		p.lastErr = e
		p.toast(e)
	}
	scope := p.ctl.SuggestScope()
	for i, sc := range contacts.Scopes {
		if i < len(p.scopes) {
			p.scopes[i].SetActive(sc == scope)
		}
	}
	if !p.ctl.Loaded() {
		return
	}
	accounts := p.ctl.Accounts()
	key := contactsPrefsKey(accounts, p.ctl.Saving)
	if key == p.key && p.groups != nil {
		return
	}
	p.key = key
	for _, g := range p.groups {
		p.page.Remove(g)
	}
	p.groups = p.groups[:0]
	p.lines = p.lines[:0]
	// Adw.PreferencesPage only appends: the empty state and Recipient
	// Suggestions go again after the accounts.
	p.page.Remove(p.empty)
	p.page.Remove(p.suggest)
	for _, a := range accounts {
		g := p.accountGroup(a)
		p.groups = append(p.groups, g)
		p.page.Add(g)
	}
	p.empty.SetVisible(p.ctl.Empty())
	p.page.Add(p.empty)
	p.page.Add(p.suggest)
}

// accountGroup is an account's group.
func (p *contactsPrefs) accountGroup(a contactsctl.PrefsAccount) *adw.PreferencesGroup {
	tr := i18n.Tr
	id := a.Account.ID
	g := adw.NewPreferencesGroup()
	g.SetTitle(glib.MarkupEscapeText(a.Title)) // the account's address: plain text
	g.SetHeaderSuffix(widget.NewKindBadge(a.Badge))
	line := fmt.Sprintf("%s [%s] sync=%v", a.Title, a.Badge, a.SyncOn)

	sync := adw.NewSwitchRow()
	sync.SetUseMarkup(false)
	sync.SetTitle(contacts.SyncContacts(tr))
	sync.SetActive(a.SyncOn)
	sync.SetSensitive(!p.ctl.Saving(id))
	sync.NotifyProperty("active", func() {
		if !p.syncing {
			p.ctl.SetSync(id, sync.Active())
		}
	})
	g.Add(sync)

	if a.Consent {
		row := adw.NewActionRow()
		row.SetUseMarkup(false)
		row.SetTitle(contacts.ConsentBanner(a.Title, tr))
		row.AddPrefix(gtk.NewImageFromIconName("dialog-warning-symbolic"))
		if a.SignIn {
			allow := gtk.NewButtonWithLabel(contacts.Allow(tr))
			allow.SetVAlign(gtk.AlignCenter)
			allow.AddCSSClass("suggested-action")
			allow.ConnectClicked(func() { p.ctl.Allow(id) })
			row.AddSuffix(allow)
		} else {
			row.SetSubtitle(contacts.ConsentOnline(tr))
		}
		g.Add(row)
		line += " consent"
	}

	for _, bk := range a.Books {
		row := adw.NewActionRow()
		row.SetUseMarkup(false)
		row.SetTitle(contacts.BookName(bk, tr)) // the server's text
		row.SetSubtitle(contacts.ContactCount(max(bk.Count, 0), tr))
		check := gtk.NewCheckButton()
		check.SetVAlign(gtk.AlignCenter)
		check.SetActive(bk.Visible)
		book := bk.ID
		check.ConnectToggled(func() {
			if !p.syncing {
				p.ctl.SetVisible(book, check.Active())
			}
		})
		row.AddPrefix(check)
		row.SetActivatableWidget(check)
		g.Add(row)
		line += fmt.Sprintf(" book=%s:%v", bk.ID, bk.Visible)
	}
	if a.CardDAV {
		g.SetDescription(glib.MarkupEscapeText(contacts.CardDAVNote(tr)))
	}
	p.lines = append(p.lines, line)
	return g
}
