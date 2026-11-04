// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package contactsctl

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/calendar"
)

// The fake daemon and loop of the tests: calls run in place (Spawn), their
// answers wait on the loop until run, timers until fire.

type en struct{}

func (en) T(msgid string) string { return msgid }
func (en) N(msgid, plural string, n int) string {
	if n == 1 {
		return msgid
	}
	return plural
}
func (en) C(_, msgid string) string { return msgid }

type loop struct {
	queue  []func()
	timers []timer
}

type timer struct {
	d time.Duration
	f func()
}

func (l *loop) Post(f func())                   { l.queue = append(l.queue, f) }
func (l *loop) After(d time.Duration, f func()) { l.timers = append(l.timers, timer{d, f}) }

// run runs what was posted, and what that posted, until nothing is left.
func (l *loop) run() {
	for len(l.queue) > 0 {
		f := l.queue[0]
		l.queue = l.queue[1:]
		f()
	}
}

// fire runs the timers set so far, then what they posted.
func (l *loop) fire() {
	ts := l.timers
	l.timers = nil
	for _, t := range ts {
		t.f()
	}
	l.run()
}

type handler func(params json.RawMessage) (any, error)

type daemon struct {
	handlers map[string]handler
	calls    []string
	params   map[string][]json.RawMessage
}

func newDaemon() *daemon {
	return &daemon{handlers: map[string]handler{}, params: map[string][]json.RawMessage{}}
}

func (d *daemon) on(method string, h handler) { d.handlers[method] = h }

func (d *daemon) answer(method string, v any) {
	d.on(method, func(json.RawMessage) (any, error) { return v, nil })
}

func (d *daemon) fail(method string, e *api.Error) {
	d.on(method, func(json.RawMessage) (any, error) { return nil, e })
}

func (d *daemon) Call(_ context.Context, method string, params, result any) error {
	raw, _ := json.Marshal(params)
	d.calls = append(d.calls, method)
	d.params[method] = append(d.params[method], raw)
	h, ok := d.handlers[method]
	if !ok {
		return &api.Error{Code: api.CodeMethodNotFound, Message: method}
	}
	v, err := h(raw)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(v)
	return json.Unmarshal(b, result)
}

// count is how many times method was called.
func (d *daemon) count(method string) int {
	n := 0
	for _, m := range d.calls {
		if m == method {
			n++
		}
	}
	return n
}

// last decodes method's last params into v.
func (d *daemon) last(method string, v any) {
	ps := d.params[method]
	if len(ps) == 0 {
		panic("no call of " + method)
	}
	if err := json.Unmarshal(ps[len(ps)-1], v); err != nil {
		panic(err)
	}
}

func options(d *daemon, l *loop) Options {
	return Options{Caller: d, Loop: l, Tr: en{}, Format: calendar.EnglishStrftime,
		Now:   func() time.Time { return time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC) },
		Spawn: func(f func()) { f() }}
}

func accounts() []api.Account {
	on := &api.GraphConfig{Contacts: &api.ContactsConfig{Sync: true}}
	contactsCaps := []api.AccountCapability{api.CapabilityContacts}
	return []api.Account{
		{ID: "acc_m", Enabled: true, Capabilities: contactsCaps, Config: api.AccountConfig{Name: "Work", Email: "jana@firma.cz", Kind: api.AccountGraph, Graph: on}},
		{ID: "acc_p", Enabled: true, Capabilities: contactsCaps, Config: api.AccountConfig{Name: "Petr", Email: "petr@firma2.cz", Kind: api.AccountGraph,
			Graph: &api.GraphConfig{Contacts: &api.ContactsConfig{Sync: true}}}},
		{ID: "acc_i", Enabled: true, Config: api.AccountConfig{Name: "Home", Email: "jana@example.org"}},
		{ID: "acc_d", Enabled: true, Capabilities: []api.AccountCapability{api.CapabilityCalendar, api.CapabilityContacts},
			Config: api.AccountConfig{Name: "Nextcloud", Kind: api.AccountCalDAV, CalDAV: &api.CalDAVConfig{ServerURL: "https://cloud.example.cz", Contacts: true}}},
	}
}

func books() []api.AddressBook {
	t := time.Date(2026, 10, 30, 11, 57, 0, 0, time.UTC)
	return []api.AddressBook{
		{ID: "b_m1", AccountID: "acc_m", Name: "Contacts", Kind: api.AddressBookKindGraph, Visible: true, Count: 3, LastSyncAt: &t},
		{ID: "b_m2", AccountID: "acc_m", Name: "Archive", Kind: api.AddressBookKindGraph, Visible: false, Count: 9},
		{ID: "b_d", AccountID: "acc_d", Name: "Personal", Kind: api.AddressBookKindCardDAV, Visible: true, Count: 2},
		{ID: api.AddressBookRecent, Name: "recent", Kind: api.AddressBookKindRecent, ReadOnly: true, Visible: true, Count: 5},
	}
}

// people are n contacts of a book, sorted, sections by the first letter.
func people(book api.AddressBookID, names ...string) []api.ContactSummary {
	var out []api.ContactSummary
	for i, n := range names {
		out = append(out, api.ContactSummary{ID: api.ContactID(fmt.Sprintf("p_%s_%d", book, i)), BookID: book, DisplayName: n,
			SortKey: strings.ToLower(n), Section: strings.ToUpper(string([]rune(n)[0]))})
	}
	return out
}

// serveList answers contact.list from all by book, query (a substring)
// and paging.
func serveList(d *daemon, all map[api.AddressBookID][]api.ContactSummary) {
	d.on(api.MethodContactList, func(raw json.RawMessage) (any, error) {
		var p api.ContactListParams
		_ = json.Unmarshal(raw, &p)
		var src []api.ContactSummary
		if p.BookID == api.AddressBookAll {
			for _, id := range []api.AddressBookID{"b_m1", "b_d"} {
				src = append(src, all[id]...)
			}
			slices.SortFunc(src, func(a, b api.ContactSummary) int { return strings.Compare(a.SortKey, b.SortKey) })
		} else {
			src = all[p.BookID]
		}
		var match []api.ContactSummary
		for _, c := range src {
			if p.Query == "" || strings.Contains(strings.ToLower(c.DisplayName), strings.ToLower(p.Query)) {
				match = append(match, c)
			}
		}
		limit := p.Limit
		if limit == 0 {
			limit = api.DefaultContactListLimit
		}
		end := min(p.Offset+limit, len(match))
		page := []api.ContactSummary{}
		if p.Offset < end {
			page = match[p.Offset:end]
		}
		return api.ContactListResult{Contacts: page, Total: len(match)}, nil
	})
}

// standard is a daemon with the accounts, the books, a consent refusal
// for acc_p and the contacts.
func standard() (*daemon, *loop) {
	d, l := newDaemon(), &loop{}
	d.answer(api.MethodAccountList, api.AccountListResult{Accounts: accounts()})
	d.on(api.MethodAddressBookList, func(raw json.RawMessage) (any, error) {
		var p api.AddressBookListParams
		_ = json.Unmarshal(raw, &p)
		if p.AccountID == "acc_p" {
			return nil, &api.Error{Code: api.CodeContactsConsentRequired, Message: "consent",
				Data: map[string]any{"accountId": "acc_p", "signIn": true}}
		}
		return api.AddressBookListResult{Books: books()}, nil
	})
	serveList(d, map[api.AddressBookID][]api.ContactSummary{
		"b_m1":                people("b_m1", "Adámek", "Beneš", "Nováková"),
		"b_d":                 people("b_d", "Bílá", "Pokorný"),
		api.AddressBookRecent: people(api.AddressBookRecent, "a@x.cz", "b@x.cz"),
	})
	return d, l
}
