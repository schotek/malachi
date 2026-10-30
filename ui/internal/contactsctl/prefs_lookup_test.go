// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package contactsctl

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/contacts"
)

func TestLookup(t *testing.T) {
	d, l := newDaemon(), &loop{}
	d.on(api.MethodContactLookup, func(raw json.RawMessage) (any, error) {
		var p api.ContactLookupParams
		_ = json.Unmarshal(raw, &p)
		known := map[string]api.ContactID{}
		for _, a := range p.Addresses {
			if a == "jana@firma.cz" {
				known[a] = "p_1"
			}
		}
		return api.ContactLookupResult{Known: known}, nil
	})
	lk := NewLookup(options(d, l))
	var changed []string
	lk.OnChange = func(k string) { changed = append(changed, k) }
	lk.Ask("m1", []string{" Jana@Firma.cz ", "petr@example.net", "JANA@firma.cz", ""})
	lk.Ask("m1", []string{"x@y.cz"})
	if it, _ := lk.Item("m1", "jana@firma.cz", books()); it != contacts.ChipNone {
		t.Fatal("an item before the answer")
	}
	l.run()
	var p api.ContactLookupParams
	d.last(api.MethodContactLookup, &p)
	if !slices.Equal(p.Addresses, []string{"jana@firma.cz", "petr@example.net"}) || d.count(api.MethodContactLookup) != 1 {
		t.Fatalf("params %v", p.Addresses)
	}
	if it, id := lk.Item("m1", "JANA@firma.cz", books()); it != contacts.ChipShow || id != "p_1" {
		t.Fatal("show")
	}
	if it, _ := lk.Item("m1", "petr@example.net", books()); it != contacts.ChipAdd {
		t.Fatal("add")
	}
	if it, _ := lk.Item("m1", "petr@example.net", books()[3:]); it != contacts.ChipNone {
		t.Fatal("no writable book")
	}
	if !slices.Equal(changed, []string{"m1"}) {
		t.Fatal("change")
	}
	// Invalidate drops answers, also those on their way.
	lk.Ask("m2", []string{"a@b.cz"})
	lk.Invalidate()
	l.run()
	if _, ok := lk.Known("m2", "a@b.cz"); ok {
		t.Fatal("a stale answer stored")
	}
	if _, ok := lk.Known("m1", "jana@firma.cz"); ok {
		t.Fatal("not forgotten")
	}
	// Many addresses go in chunks; an empty message is answered at once.
	var many []string
	for i := range 450 {
		many = append(many, fmt.Sprintf("a%d@x.cz", i))
	}
	n := d.count(api.MethodContactLookup)
	lk.Ask("big", many)
	l.run()
	if d.count(api.MethodContactLookup) != n+3 {
		t.Fatalf("chunks %d", d.count(api.MethodContactLookup)-n)
	}
	if _, ok := lk.Known("big", "a1@x.cz"); !ok {
		t.Fatal("chunks not stored")
	}
	lk.Ask("empty", nil)
	if _, ok := lk.Known("empty", "x"); !ok {
		t.Fatal("empty")
	}
	// Only LookupKept messages stay.
	for i := range LookupKept + 5 {
		lk.Ask(fmt.Sprintf("k%d", i), nil)
	}
	if _, ok := lk.Known("big", "a1@x.cz"); ok {
		t.Fatal("old answers kept")
	}
	// A failed lookup stores nothing; it is asked again.
	d.fail(api.MethodContactLookup, &api.Error{Code: api.CodeStorageError, Message: "x"})
	lk.Ask("f", []string{"a@b.cz"})
	l.run()
	if _, ok := lk.Known("f", "a@b.cz"); ok {
		t.Fatal("failure stored")
	}
	lk.Ask("f", []string{"a@b.cz"})
	l.run()
	if d.count(api.MethodContactLookup) != n+5 {
		t.Fatal("not asked again")
	}
}

func startPrefs(t *testing.T) (*PreferencesController, *daemon, *loop) {
	t.Helper()
	d, l := standard()
	p := NewPreferencesController(options(d, l))
	p.Start()
	l.run()
	return p, d, l
}

func TestPreferencesSections(t *testing.T) {
	p, _, _ := startPrefs(t)
	secs := p.Accounts()
	var got []string
	for _, s := range secs {
		var bs []string
		for _, b := range s.Books {
			bs = append(bs, fmt.Sprintf("%s:%v", b.Name, b.Visible))
		}
		got = append(got, fmt.Sprintf("%s/%s sync=%v consent=%v carddav=%v %v", s.Title, s.Badge, s.SyncOn, s.Consent, s.CardDAV, bs))
	}
	want := []string{
		"jana@firma.cz/M365 sync=true consent=false carddav=false [Contacts:true Archive:false]",
		"petr@firma2.cz/M365 sync=true consent=true carddav=false []",
		"Nextcloud/CARDDAV sync=true consent=false carddav=true [Personal:true]",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("sections\n%q", got)
	}
	if !secs[1].SignIn || p.Empty() {
		t.Fatal("sign in")
	}
	signed := api.AccountID("")
	p.OnSignIn = func(id api.AccountID) { signed = id }
	p.Allow("acc_p")
	if signed != "acc_p" {
		t.Fatal("allow")
	}
}

func TestPreferencesSyncSwitch(t *testing.T) {
	p, d, l := startPrefs(t)
	d.answer(api.MethodAccountUpdate, api.AccountUpdateResult{})
	p.SetSync("acc_m", false)
	if p.Accounts()[0].SyncOn || !p.Saving("acc_m") {
		t.Fatal("not shown at once")
	}
	p.SetSync("acc_m", true) // while saving: nothing
	l.run()
	var u api.AccountUpdateParams
	d.last(api.MethodAccountUpdate, &u)
	if u.AccountID != "acc_m" || u.Config.Graph.Contacts.Sync || u.Credentials != (api.Credentials{}) || d.count(api.MethodAccountUpdate) != 1 {
		t.Fatalf("update %+v", u)
	}
	// The original configuration was not changed in place.
	if !accounts()[0].Config.Graph.Contacts.Sync {
		t.Fatal("shared config")
	}
	// CardDAV and a refusal undone.
	d.fail(api.MethodAccountUpdate, &api.Error{Code: api.CodeAccountNotFound, Message: "x"})
	p.SetSync("acc_d", false)
	l.run()
	d.last(api.MethodAccountUpdate, &u)
	if u.Config.CalDAV.Contacts || u.Config.CalDAV.ServerURL != "https://cloud.example.cz" {
		t.Fatalf("caldav update %+v", u.Config.CalDAV)
	}
	if !p.Accounts()[2].SyncOn || p.Error() == "" {
		t.Fatal("refusal not undone")
	}
	p.SetSync("acc_i", true) // no contacts capability
	if d.count(api.MethodAccountUpdate) != 2 {
		t.Fatal("imap account updated")
	}
	if c := withSync(api.AccountConfig{Kind: api.AccountGraph}, true); !SyncOn(c) {
		t.Fatal("graph without config")
	}
	if c := withSync(api.AccountConfig{Kind: api.AccountCalDAV}, true); !SyncOn(c) || SyncOn(api.AccountConfig{}) {
		t.Fatal("caldav without config")
	}
}

func TestPreferencesBookCheckbox(t *testing.T) {
	p, d, l := startPrefs(t)
	d.on(api.MethodAddressBookSet, func(raw json.RawMessage) (any, error) {
		var s api.AddressBookSetParams
		_ = json.Unmarshal(raw, &s)
		b := books()[1]
		b.Visible = s.Visible
		b.Count = 10
		return api.AddressBookSetResult{Book: b}, nil
	})
	p.SetVisible("b_m2", true)
	if !p.Accounts()[0].Books[1].Visible {
		t.Fatal("not at once")
	}
	l.run()
	if p.Accounts()[0].Books[1].Count != 10 {
		t.Fatal("answer not taken")
	}
	d.fail(api.MethodAddressBookSet, &api.Error{Code: api.CodeAddressBookNotFound, Message: "x"})
	p.SetVisible("b_m1", false)
	l.run()
	if !p.Accounts()[0].Books[0].Visible || p.Error() != "The address book no longer exists." {
		t.Fatal("refusal")
	}
	p.SetVisible("b_none", true)
}

func TestPreferencesScope(t *testing.T) {
	p, _, _ := startPrefs(t)
	if p.SuggestScope() != api.ContactScopeAccount {
		t.Fatal("default")
	}
	stored := api.ContactSearchScope("")
	p.Scope = func() api.ContactSearchScope { return stored }
	p.SetScope = func(s api.ContactSearchScope) { stored = s }
	p.SetSuggestScope("bogus")
	p.SetSuggestScope(api.ContactScopeAll)
	if stored != api.ContactScopeAll || p.SuggestScope() != api.ContactScopeAll {
		t.Fatal("set")
	}
	stored = "weird"
	if p.SuggestScope() != api.ContactScopeAccount {
		t.Fatal("unknown stored value")
	}
}

func TestPreferencesEmptyAndReload(t *testing.T) {
	d, l := newDaemon(), &loop{}
	d.answer(api.MethodAccountList, api.AccountListResult{Accounts: accounts()[2:3]})
	d.answer(api.MethodAddressBookList, api.AddressBookListResult{Books: books()[3:]})
	p := NewPreferencesController(options(d, l))
	p.Start()
	l.run()
	if !p.Empty() || !p.Loaded() {
		t.Fatal("empty")
	}
	n := d.count(api.MethodAccountList)
	p.Handle(api.NotifyAccountsChanged, nil)
	p.Handle(api.NotifyNewMessage, nil)
	l.run()
	if d.count(api.MethodAccountList) != n+1 {
		t.Fatal("reload")
	}
}

func TestHelpers(t *testing.T) {
	if _, ok := ConflictCard(&api.Error{Code: api.CodeContactConflict, Data: api.ContactConflictData{Server: api.ContactCard{ID: "p"}}}); !ok {
		t.Fatal("typed data")
	}
	if _, ok := ConflictCard(&api.Error{Code: api.CodeOffline}); ok {
		t.Fatal("other code")
	}
	if _, ok := ConsentData(nil); ok {
		t.Fatal("nil")
	}
	raw, _ := json.Marshal(api.ContactsChangedNotification{AccountID: "a", BookIDs: []api.AddressBookID{"b"}})
	if n, ok := ContactsChanged(api.NotifyContactsChanged, raw); !ok || n.AccountID != "a" {
		t.Fatal("notification")
	}
	if _, ok := ContactsChanged(api.NotifyNewMessage, raw); ok {
		t.Fatal("other notification")
	}
	if AsError(nil) != nil || AsError(fmt.Errorf("x")).Code != api.CodeInternalError {
		t.Fatal("as error")
	}
	o := options(newDaemon(), nil)
	if o.ErrorMessage(&api.Error{Code: api.CodeNetworkError}, "", "") != "Contacts" || o.ErrorMessage(nil, "", "") != "" {
		t.Fatal("error message")
	}
}
