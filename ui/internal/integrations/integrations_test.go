// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package integrations

import (
	"slices"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// msgids returns the msgid.
type msgids struct{}

func (msgids) T(msgid string) string { return msgid }

func account(id string, kind api.AccountKind) api.Account {
	a := api.Account{ID: api.AccountID(id), Config: api.AccountConfig{Kind: kind, Email: id + "@example.invalid"}}
	switch kind {
	case api.AccountJira:
		a.Config.Jira = &api.JiraConfig{SiteURL: "https://Acme.atlassian.net/"}
	case api.AccountCalDAV:
		a.Config.CalDAV = &api.CalDAVConfig{ServerURL: "https://Cloud.example.org/remote.php/dav/"}
	}
	return a
}

func ids(accounts []api.Account) []api.AccountID {
	out := make([]api.AccountID, len(accounts))
	for i, a := range accounts {
		out[i] = a.ID
	}
	return out
}

func TestPredicatesByKind(t *testing.T) {
	for _, c := range []struct {
		kind api.AccountKind
		mail bool
	}{
		{"", true}, {api.AccountIMAP, true}, {api.AccountGraph, true},
		{api.AccountJira, false}, {api.AccountCalDAV, false}, {"future", false},
	} {
		a := account("a", c.kind)
		if ShowsInAccounts(a) != c.mail || ShowsInIntegrations(a) == c.mail {
			t.Errorf("kind %q: accounts %v, integrations %v", c.kind, ShowsInAccounts(a), ShowsInIntegrations(a))
		}
	}
}

func TestSplitKeepsTheOrder(t *testing.T) {
	all := []api.Account{
		account("j1", api.AccountJira), account("m1", ""), account("c1", api.AccountCalDAV),
		account("m2", api.AccountGraph), account("j2", api.AccountJira), account("m3", api.AccountIMAP),
	}
	mail, other := Split(all)
	if got := ids(mail); !slices.Equal(got, []api.AccountID{"m1", "m2", "m3"}) {
		t.Errorf("mail %v", got)
	}
	if got := ids(other); !slices.Equal(got, []api.AccountID{"j1", "c1", "j2"}) {
		t.Errorf("integrations %v", got)
	}
	if m, o := Split(nil); m != nil || o != nil {
		t.Error("nil")
	}
}

func TestFullOrder(t *testing.T) {
	all := []api.Account{
		account("j1", api.AccountJira), account("m1", ""), account("c1", api.AccountCalDAV),
		account("m2", api.AccountGraph), account("j2", api.AccountJira), account("m3", api.AccountIMAP),
	}
	for _, c := range []struct {
		name  string
		page  Page
		order []api.AccountID
		want  []api.AccountID
	}{
		{"accounts reordered", PageAccounts, []api.AccountID{"m3", "m1", "m2"},
			[]api.AccountID{"m3", "m1", "m2", "j1", "c1", "j2"}},
		{"integrations reordered", PageIntegrations, []api.AccountID{"j2", "c1", "j1"},
			[]api.AccountID{"m1", "m2", "m3", "j2", "c1", "j1"}},
		{"unknown and other page's ids ignored", PageAccounts, []api.AccountID{"x", "j1", "m2", "m2", "m1", "m3"},
			[]api.AccountID{"m2", "m1", "m3", "j1", "c1", "j2"}},
		{"omitted keep their order", PageIntegrations, []api.AccountID{"j2"},
			[]api.AccountID{"m1", "m2", "m3", "j2", "j1", "c1"}},
		{"empty order", PageAccounts, nil,
			[]api.AccountID{"m1", "m2", "m3", "j1", "c1", "j2"}},
	} {
		got := FullOrder(all, c.page, c.order)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
		seen := map[api.AccountID]int{}
		for _, id := range got {
			seen[id]++
		}
		if len(seen) != len(all) || len(got) != len(all) {
			t.Errorf("%s: not every id once: %v", c.name, got)
		}
	}
	if got := FullOrder(nil, PageAccounts, []api.AccountID{"a"}); len(got) != 0 {
		t.Errorf("no accounts: %v", got)
	}
}

func TestCatalog(t *testing.T) {
	c := Catalog(msgids{})
	if len(c) != 3 || c[0].Kind != api.AccountJira || c[1].Kind != api.AccountCalDAV || c[2].Kind != api.AccountEway {
		t.Fatalf("catalog %+v", c)
	}
	if c[0].Name != "Jira" || c[0].Icon != JiraIcon || c[1].Name != "CalDAV Calendar" || c[1].Icon != CalDAVIcon ||
		c[2].Name != "eWay-CRM" || c[2].Icon != EwayIcon ||
		c[2].Description != "Save messages to your CRM with their contacts, companies and deals" {
		t.Errorf("catalog %+v", c)
	}
	for _, s := range c {
		if s.Description == "" {
			t.Errorf("%s: no description", s.Kind)
		}
	}
}

func TestRowForByKind(t *testing.T) {
	tr := msgids{}
	mail := account("m", "")
	mail.Config.Name = "Work"
	r := RowFor(mail, tr)
	if r.ID != "m" || r.Kind != api.AccountIMAP || r.Title != "Work" || r.Subtitle != "m@example.invalid" ||
		!r.OffersSwitch || r.Editor != EditorMailWizard || r.EditLabel != "Edit Account" || r.Icon != MailIcon {
		t.Errorf("mail %+v", r)
	}
	if !strings.Contains(r.RemoveBody, "Mail on the server") || !strings.HasPrefix(r.RemoveBody, "m@example.invalid ") {
		t.Errorf("mail remove %q", r.RemoveBody)
	}
	unnamed := RowFor(account("g", api.AccountGraph), tr)
	if unnamed.Title != "g@example.invalid" || unnamed.Subtitle != "g@example.invalid" || unnamed.Editor != EditorMailWizard {
		t.Errorf("graph %+v", unnamed)
	}

	j := account("j", api.AccountJira)
	r = RowFor(j, tr)
	if r.Title != "acme.atlassian.net" || r.Subtitle != "j@example.invalid" || !r.OffersSwitch ||
		r.Editor != EditorJira || r.Icon != JiraIcon || r.EditLabel != "Edit Account" {
		t.Errorf("unnamed jira %+v", r)
	}
	if r.RemoveBody != "acme.atlassian.net will be removed from Malachi Mail. Issues on the Jira site are not affected." {
		t.Errorf("jira remove %q", r.RemoveBody)
	}
	j.Config.Name = "Acme"
	if r = RowFor(j, tr); r.Title != "Acme" || r.Subtitle != "acme.atlassian.net" {
		t.Errorf("named jira %+v", r)
	}

	c := account("c", api.AccountCalDAV)
	c.Config.Name = "Nextcloud"
	r = RowFor(c, tr)
	if r.Title != "Nextcloud" || r.Subtitle != "cloud.example.org" || r.OffersSwitch ||
		r.Editor != EditorCalendarPage || r.EditLabel != "Calendar Settings" || r.Icon != CalDAVIcon {
		t.Errorf("caldav %+v", r)
	}
	if r.RemoveBody != "Nextcloud will be removed from Malachi Mail. Calendars on the server are not affected." {
		t.Errorf("caldav remove %q", r.RemoveBody)
	}
	c.Config.CalDAV = nil
	c.Config.Name = ""
	if r = RowFor(c, tr); r.Title != "c@example.invalid" || r.Subtitle != "c@example.invalid" {
		t.Errorf("caldav without server %+v", r)
	}

	e := account("e", api.AccountEway)
	e.Config.Eway = &api.EwayConfig{ServiceURL: "https://Hosting.eway-crm.com/acme", Username: "jana"}
	r = RowFor(e, tr)
	if r.Title != "e@example.invalid" || r.Subtitle != "hosting.eway-crm.com/acme" || !r.OffersSwitch ||
		r.Editor != EditorEway || r.EditLabel != "Edit Account" || r.Icon != EwayIcon {
		t.Errorf("eway %+v", r)
	}
	e.Config.Name = "Acme CRM"
	if r = RowFor(e, tr); r.Title != "Acme CRM" ||
		r.RemoveBody != "Acme CRM will be removed from Malachi Mail. Messages saved in eWay-CRM stay there." {
		t.Errorf("named eway %+v", r)
	}
	if !ShowsInIntegrations(e) || ShowsInAccounts(e) {
		t.Error("an eway account belongs on Integrations")
	}

	f := RowFor(account("f", "future"), tr)
	if f.Editor != EditorNone || !f.OffersSwitch || f.RemoveBody != "f@example.invalid will be removed from Malachi Mail." {
		t.Errorf("future %+v", f)
	}
}

func TestTexts(t *testing.T) {
	x := Texts(msgids{})
	for _, s := range []string{x.Title, x.Description, x.ConnectedTitle, x.AvailableTitle, x.EmptyTitle, x.EmptySubtitle, x.Add, x.CalendarSettings} {
		if s == "" {
			t.Errorf("empty text in %+v", x)
		}
	}
	if x.Title != "Integrations" || x.Add != "Add…" || x.CalendarSettings != "Calendar Settings" ||
		x.Description != "Services beside your mail: issue trackers, calendars and CRM" {
		t.Errorf("%+v", x)
	}
}
