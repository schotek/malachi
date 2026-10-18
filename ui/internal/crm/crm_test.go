// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package crm

import (
	"slices"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Fictional accounts and items only.

// msgids is the Translator of the tests: every text untranslated.
type msgids struct{}

func (msgids) T(msgid string) string { return msgid }
func (msgids) N(msgid, plural string, n int) string {
	if n == 1 {
		return msgid
	}
	return plural
}
func (msgids) C(_, msgid string) string { return msgid }

var (
	imapAcc  = api.Account{ID: "m1", Enabled: true, Config: api.AccountConfig{Email: "jana@acme.example"}}
	graphAcc = api.Account{ID: "g1", Enabled: true, Config: api.AccountConfig{Kind: api.AccountGraph}}
	jiraAcc  = api.Account{ID: "j1", Enabled: true, Config: api.AccountConfig{Kind: api.AccountJira}}
	ewayOff  = api.Account{ID: "e0", Enabled: false, Config: api.AccountConfig{Kind: api.AccountEway,
		Eway: &api.EwayConfig{ServiceURL: "https://hosting.eway-crm.com/old", Username: "jana"}}}
	ewayA = api.Account{ID: "e1", Enabled: true, Config: api.AccountConfig{Kind: api.AccountEway,
		Eway: &api.EwayConfig{ServiceURL: "https://hosting.eway-crm.com/acme", Username: "jana"}}}
	ewayB = api.Account{ID: "e2", Enabled: true, Config: api.AccountConfig{Kind: api.AccountEway,
		Eway: &api.EwayConfig{ServiceURL: "https://crm.example.org", Username: "jana"}}}
)

// The first enabled eway account in list order (D11, §5 Q12).
func TestAvailable(t *testing.T) {
	all := []api.Account{imapAcc, ewayOff, jiraAcc, ewayA, ewayB}
	if got := Accounts(all); len(got) != 2 || got[0].ID != "e1" || got[1].ID != "e2" {
		t.Errorf("accounts %v", got)
	}
	if a, ok := Available(all); !ok || a.ID != "e1" {
		t.Errorf("available %v %v", a, ok)
	}
	if _, ok := Available([]api.Account{imapAcc, ewayOff}); ok {
		t.Error("a paused account is available")
	}
	if _, ok := Available(nil); ok {
		t.Error("available out of nothing")
	}
}

// D6: mail accounts only, never Outbox, Drafts or a local draft; Junk is
// allowed.
func TestCanSave(t *testing.T) {
	cases := []struct {
		name  string
		acc   api.Account
		role  api.FolderRole
		local bool
		want  bool
	}{
		{"inbox of imap", imapAcc, api.RoleInbox, false, true},
		{"folder of graph", graphAcc, api.RoleNone, false, true},
		{"sent", imapAcc, api.RoleSent, false, true},
		{"junk", imapAcc, api.RoleJunk, false, true},
		{"archive", imapAcc, api.RoleArchive, false, true},
		{"outbox", imapAcc, api.RoleOutbox, false, false},
		{"drafts", imapAcc, api.RoleDrafts, false, false},
		{"local draft", imapAcc, api.RoleInbox, true, false},
		{"jira", jiraAcc, api.RoleNone, false, false},
		{"eway itself", ewayA, api.RoleNone, false, false},
		{"caldav", api.Account{Config: api.AccountConfig{Kind: api.AccountCalDAV}}, api.RoleNone, false, false},
	}
	for _, c := range cases {
		if got := CanSave(c.acc, c.role, c.local); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func rows(n int, r Row) []Row {
	out := make([]Row, n)
	for i := range out {
		out[i] = r
	}
	return out
}

// One rule for the button, the context menu and the multi-selection.
func TestOfferFor(t *testing.T) {
	accs := []api.Account{imapAcc, ewayA}
	inbox := Row{Account: imapAcc, Role: api.RoleInbox}
	saved := inbox
	saved.Saved = true
	outbox := Row{Account: imapAcc, Role: api.RoleOutbox}
	cases := []struct {
		name     string
		accounts []api.Account
		rows     []Row
		want     Offer
	}{
		{"no eway account", []api.Account{imapAcc}, []Row{inbox}, Offer{}},
		{"nothing selected", accs, nil, Offer{}},
		{"one message", accs, []Row{inbox}, Offer{Shown: true, Enabled: true, Count: 1}},
		{"one saved", accs, []Row{saved}, Offer{Shown: true, Enabled: true, Count: 1, Saved: true}},
		{"one in the outbox", accs, []Row{outbox}, Offer{}},
		{"one of jira", accs, []Row{{Account: jiraAcc}}, Offer{}},
		{"several", accs, []Row{inbox, saved}, Offer{Shown: true, Enabled: true, Count: 2}},
		{"several, one a draft", accs, []Row{inbox, {Account: imapAcc, Role: api.RoleDrafts}}, Offer{Shown: true, Count: 2}},
		{"the most", accs, rows(MaxMessages, inbox), Offer{Shown: true, Enabled: true, Count: MaxMessages}},
		{"too many", accs, rows(MaxMessages+1, inbox), Offer{Shown: true, Count: MaxMessages + 1}},
		{"several of jira", accs, rows(2, Row{Account: jiraAcc}), Offer{}},
	}
	for _, c := range cases {
		if got := OfferFor(c.accounts, c.rows); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestButton(t *testing.T) {
	accs := []api.Account{imapAcc, ewayA}
	tr := msgids{}
	inbox := Row{Account: imapAcc, Role: api.RoleInbox}
	if s := ButtonStateFor(accs, inbox, false); s != ButtonNotSaved {
		t.Errorf("not saved: %v", s)
	}
	if s := ButtonStateFor(accs, inbox, true); s != ButtonSaving {
		t.Errorf("busy: %v", s)
	}
	inbox.Saved = true
	if s := ButtonStateFor(accs, inbox, false); s != ButtonSaved {
		t.Errorf("saved: %v", s)
	}
	if s := ButtonStateFor([]api.Account{imapAcc}, inbox, false); s != ButtonHidden {
		t.Errorf("no account: %v", s)
	}
	if s := ButtonStateFor(accs, Row{Account: imapAcc, Role: api.RoleOutbox}, true); s != ButtonHidden {
		t.Errorf("outbox: %v", s)
	}
	want := map[ButtonState]Button{
		ButtonHidden:   {State: ButtonHidden},
		ButtonNotSaved: {State: ButtonNotSaved, Label: "Save to eWay-CRM", Tooltip: "Save to eWay-CRM", Enabled: true},
		ButtonSaving:   {State: ButtonSaving, Label: "Save to eWay-CRM", Tooltip: "Saving to eWay-CRM…"},
		ButtonSaved:    {State: ButtonSaved, Label: "In eWay-CRM", Tooltip: "Saved in eWay-CRM", Enabled: true, Menu: true},
	}
	for s, w := range want {
		if got := ButtonFor(s, tr); got != w {
			t.Errorf("%v: %+v, want %+v", s, got, w)
		}
	}
	menu := SavedMenu(tr)
	ids := []MenuID{}
	for _, it := range menu {
		ids = append(ids, it.ID)
	}
	if !slices.Equal(ids, []MenuID{MenuOpen, MenuEditLinks, MenuRemove}) || !menu[2].Destructive || menu[0].Destructive ||
		menu[0].Label != "Open in eWay-CRM" || menu[1].Label != "Edit Links…" || menu[2].Label != "Remove from eWay-CRM" {
		t.Errorf("menu %+v", menu)
	}
	if MenuLabel("other", tr) != "" {
		t.Error("label of an unknown item")
	}
}

func TestIsSaved(t *testing.T) {
	s := api.MessageSummary{ID: "x", AccountID: "m1", CrmSaved: true}
	if !IsSaved(s, nil, "e1") {
		t.Error("summary says saved")
	}
	m := &api.Message{MessageSummary: api.MessageSummary{ID: "x", AccountID: "m1"}}
	if IsSaved(s, m, "e1") {
		t.Error("the loaded message has no record")
	}
	m.Crm = []api.CrmRecord{{AccountID: "e2", ItemGUID: "a"}, {AccountID: "e1", ItemGUID: "b"}}
	if !IsSaved(api.MessageSummary{ID: "x", AccountID: "m1"}, m, "e1") {
		t.Error("the loaded message has a record")
	}
	if r := RecordOf(m, "e1"); r == nil || r.ItemGUID != "b" {
		t.Errorf("record %+v", r)
	}
	if r := RecordOf(m, ""); r == nil || r.ItemGUID != "a" {
		t.Errorf("first record %+v", r)
	}
	if RecordOf(m, "e9") != nil || RecordOf(nil, "") != nil {
		t.Error("record of another account")
	}
	other := &api.Message{MessageSummary: api.MessageSummary{ID: "y", AccountID: "m1"}}
	if !IsSaved(s, other, "e1") {
		t.Error("another message's record decides")
	}
}

func TestInFlight(t *testing.T) {
	var f InFlight
	if f.Has("m1", "a") || f.Any("m1", []api.MessageID{"a"}) {
		t.Error("empty set has")
	}
	f.Begin("m1", []api.MessageID{"a", "b"})
	f.Begin("m1", []api.MessageID{"a"})
	if !f.Has("m1", "a") || !f.Has("m1", "b") || f.Has("m2", "a") || !f.Any("m1", []api.MessageID{"z", "b"}) {
		t.Error("begin")
	}
	f.End("m1", []api.MessageID{"a", "b"})
	if !f.Has("m1", "a") || f.Has("m1", "b") {
		t.Error("nested end")
	}
	f.End("m1", []api.MessageID{"a", "a"})
	if f.Has("m1", "a") {
		t.Error("end")
	}
}

func TestErrorData(t *testing.T) {
	// As the RPC client decodes it: a map.
	nd := &api.Error{Code: api.CodePartNotDownloaded, Data: map[string]any{"messageIds": []any{"m_1", "m_2", "m_1", ""}}}
	if ids, ok := NotDownloaded(nd); !ok || !slices.Equal(ids, []api.MessageID{"m_1", "m_2"}) {
		t.Errorf("not downloaded %v %v", ids, ok)
	}
	typed := &api.Error{Code: api.CodePartNotDownloaded, Data: api.MessagesNotDownloadedData{MessageIDs: []api.MessageID{"m_3"}}}
	if ids, ok := NotDownloaded(typed); !ok || !slices.Equal(ids, []api.MessageID{"m_3"}) {
		t.Errorf("typed %v %v", ids, ok)
	}
	for _, e := range []error{nil, &api.Error{Code: api.CodePartNotDownloaded}, &api.Error{Code: api.CodeServerError, Data: nd.Data},
		&api.Error{Code: api.CodePartNotDownloaded, Data: map[string]any{"messageIds": []any{}}}} {
		if _, ok := NotDownloaded(e); ok {
			t.Errorf("not downloaded of %v", e)
		}
	}
	ms := &api.Error{Code: api.CodeCrmSignInUnsupported, Data: map[string]any{"reason": "microsoft"}}
	if r, ok := SignInReason(ms); !ok || r != api.CrmSignInMicrosoft {
		t.Errorf("microsoft %v", r)
	}
	if r, ok := SignInReason(&api.Error{Code: api.CodeCrmSignInUnsupported, Data: api.CrmSignInUnsupportedData{Reason: api.CrmSignInPlan}}); !ok || r != api.CrmSignInPlan {
		t.Errorf("plan %v", r)
	}
	if _, ok := SignInReason(&api.Error{Code: api.CodeCrmSignInUnsupported, Data: map[string]any{"reason": "other"}}); ok {
		t.Error("an unknown reason")
	}
	if _, ok := SignInReason(&api.Error{Code: api.CodeCrmSignInUnsupported}); ok {
		t.Error("no data")
	}
}

func TestURLsAndNames(t *testing.T) {
	for u, want := range map[string]string{
		"https://open.eway-crm.com/?ws=aA&l=bB": "https://open.eway-crm.com/?ws=aA&l=bB",
		"HTTPS://open.eway-crm.com/":            "HTTPS://open.eway-crm.com/",
		"http://open.eway-crm.com/":             "",
		"javascript:alert(1)":                   "",
		"https://user@open.eway-crm.com/":       "",
		"https:///nohost":                       "",
		"":                                      "",
	} {
		if got := OpenURL(u); got != want {
			t.Errorf("OpenURL(%q) = %q", u, got)
		}
	}
	for u, want := range map[string]string{
		"https://hosting.eway-crm.com/acme":     "acme",
		"https://hosting.eway-crm.com/a/b%20c/": "b c",
		"https://CRM.Example.org":               "crm.example.org",
		"https://crm.example.org/\u202eevil":    "evil",
		"not a url":                             "",
	} {
		if got := DefaultName(u); got != want {
			t.Errorf("DefaultName(%q) = %q, want %q", u, got, want)
		}
	}
	if got := ServiceHost(ewayA); got != "hosting.eway-crm.com/acme" {
		t.Errorf("host %q", got)
	}
	if got := ServiceHost(imapAcc); got != "" {
		t.Errorf("host of mail %q", got)
	}
}

// A conversation's row stands for its members, not for one message: it
// offers nothing, alone or among the selected rows (owner, 2026-10-17);
// its members, unfolded, are rows like any.
func TestConversationRowOffersNothing(t *testing.T) {
	accs := []api.Account{imapAcc, ewayA}
	inbox := Row{Account: imapAcc, Role: api.RoleInbox}
	conv := Row{Account: imapAcc, Role: api.RoleInbox, Conversation: true}
	if got := OfferFor(accs, []Row{conv}); got != (Offer{}) {
		t.Errorf("one conversation row: %+v", got)
	}
	if got := OfferFor(accs, []Row{inbox, conv}); got != (Offer{}) {
		t.Errorf("a conversation row among others: %+v", got)
	}
	if s := ButtonStateFor(accs, conv, false); s != ButtonHidden {
		t.Errorf("button on a conversation row: %v", s)
	}
	if got := OfferFor(accs, []Row{inbox}); !got.Shown {
		t.Errorf("a plain row: %+v", got)
	}
}
