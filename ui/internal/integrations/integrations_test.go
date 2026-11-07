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
	if len(c) != 8 || c[0].Kind != api.AccountJira || c[1].Kind != api.AccountCalDAV || c[2].Kind != api.AccountEway {
		t.Fatalf("catalog %+v", c)
	}
	if c[0].Name != "Jira" || c[0].Icon != JiraIcon || c[1].Name != "CalDAV Calendar" || c[1].Icon != CalDAVIcon ||
		c[2].Name != "eWay-CRM" || c[2].Icon != EwayIcon ||
		c[2].Description != "Save messages to your CRM with their contacts, companies and deals" {
		t.Errorf("catalog %+v", c)
	}
	for i, s := range c {
		if s.Description == "" {
			t.Errorf("%d: no description", i)
		}
		if want := Section(0); i >= 3 {
			want = SectionAI
		} else if s.Section != want {
			t.Errorf("%d: section %v, want %v", i, s.Section, want)
		}
		if (s.Kind == "") != (s.Local != LocalNone) {
			t.Errorf("%d: kind %q with local %v", i, s.Kind, s.Local)
		}
		if (s.Kind == api.AccountAI) != (s.Provider != "") {
			t.Errorf("%d: kind %q with provider %q", i, s.Kind, s.Provider)
		}
	}
	// The AI section (docs/plans/ai-api.md §7.2): the local integrations,
	// then the three provider kinds; brands are not translated.
	ai := c[3:]
	if ai[0].Local != LocalClaude || ai[0].Name != "Claude Code / Claude Desktop" ||
		ai[1].Local != LocalChatGPT || ai[1].Name != "ChatGPT (Codex)" ||
		ai[2].Provider != api.AIAnthropic || ai[2].Name != "Anthropic API" ||
		ai[3].Provider != api.AIOpenAI || ai[3].Name != "OpenAI API" ||
		ai[4].Provider != api.AIOpenAICompatible || ai[4].Name != "OpenAI-compatible server" {
		t.Errorf("ai section %+v", ai)
	}
	for i, s := range ai {
		if s.Icon != AIIcon || s.Section != SectionAI {
			t.Errorf("ai %d: %+v", i, s)
		}
	}
	if !strings.Contains(ai[4].Description, "Ollama") || !strings.Contains(ai[4].Description, "OpenRouter") {
		t.Errorf("compatible description %q", ai[4].Description)
	}
	if got := CatalogSection(msgids{}, SectionServices); len(got) != 3 || got[2].Kind != api.AccountEway {
		t.Errorf("services section %+v", got)
	}
	if got := CatalogSection(msgids{}, SectionAI); len(got) != 5 || got[0].Local != LocalClaude {
		t.Errorf("ai section %+v", got)
	}
}

func aiAccount(id, name string, p api.AIProvider, model string) api.Account {
	return api.Account{ID: api.AccountID(id), Enabled: true,
		Config:       api.AccountConfig{Kind: api.AccountAI, Name: name, AI: &api.AIConfig{Provider: p, Model: model}},
		Capabilities: []api.AccountCapability{api.CapabilityAssistant}}
}

func TestRowForAI(t *testing.T) {
	tr := msgids{}
	a := aiAccount("acc_1", "Work", api.AIAnthropic, "claude-opus-5-5")
	r := RowFor(a, tr)
	if r.ID != "acc_1" || r.Kind != api.AccountAI || r.Title != "Work" || r.Subtitle != "Anthropic API · claude-opus-5-5" ||
		!r.OffersSwitch || r.Editor != EditorAI || r.EditLabel != "Edit Account" || r.Icon != AIIcon || r.Badge != "API key" {
		t.Errorf("anthropic %+v", r)
	}
	if r.RemoveBody != "Work will be removed from Malachi Mail, and its API key with it." {
		t.Errorf("remove %q", r.RemoveBody)
	}
	if !ShowsInIntegrations(a) || ShowsInAccounts(a) {
		t.Error("an ai account belongs on Integrations")
	}
	if h := host(a); h != "api.anthropic.com" {
		t.Errorf("host %q", h)
	}

	o := aiAccount("acc_2", "", api.AIOpenAI, "")
	if r = RowFor(o, tr); r.Title != "OpenAI API" || r.Subtitle != "OpenAI API" {
		t.Errorf("unnamed openai without a model %+v", r)
	}
	if h := host(o); h != "api.openai.com" {
		t.Errorf("host %q", h)
	}

	c := aiAccount("acc_3", "Home", api.AIOpenAICompatible, "llama3.3\x01\n")
	c.Config.AI.BaseURL = "http://Ollama.lan:11434/v1"
	if r = RowFor(c, tr); r.Title != "Home" || r.Subtitle != "OpenAI-compatible server · llama3.3" {
		t.Errorf("compatible %+v", r)
	}
	if h := host(c); h != "ollama.lan" {
		t.Errorf("host %q", h)
	}

	bare := api.Account{ID: "acc_4", Config: api.AccountConfig{Kind: api.AccountAI}}
	if r = RowFor(bare, tr); r.Title != "OpenAI-compatible server" || r.Subtitle != "OpenAI-compatible server" || r.Editor != EditorAI {
		t.Errorf("without config %+v", r)
	}
	if h := host(bare); h != "" {
		t.Errorf("host %q", h)
	}
}

func TestLocalRows(t *testing.T) {
	tr := msgids{}
	// Nothing connected: no rows, both local catalogue entries offered.
	if rows := LocalRows(ClaudeState{}, ChatGPTState{}, tr); len(rows) != 0 {
		t.Fatalf("rows while nothing is connected %+v", rows)
	}
	for _, s := range CatalogSection(tr, SectionAI) {
		if !Offered(s, ClaudeState{}, ChatGPTState{}) {
			t.Errorf("%s not offered while nothing is connected", s.Name)
		}
		// The capsule: the local ones use this computer's apps, the
		// provider kinds an API key.
		if want := map[bool]string{true: "This computer", false: "API key"}[s.Local != LocalNone]; s.Badge != want {
			t.Errorf("%s: badge %q, want %q", s.Name, s.Badge, want)
		}
	}
	for _, s := range CatalogSection(tr, SectionServices) {
		if s.Badge != "" {
			t.Errorf("%s: badge %q", s.Name, s.Badge)
		}
	}
	// The row of an unconnected integration exists for a view that keeps a
	// widget per kind, hidden, without Remove.
	if r, shown := LocalRowFor(LocalClaude, ClaudeState{Present: true, Version: "2.1.284"}, ChatGPTState{}, tr); shown || r.CanRemove ||
		r.Title != "Claude Code / Claude Desktop" || r.Editor != EditorClaude || r.Icon != AIIcon || r.RemoveLabel != "Unregister" || r.Badge != "This computer" {
		t.Errorf("claude unconnected %+v shown %v", r, shown)
	}
	if r, shown := LocalRowFor(LocalChatGPT, ClaudeState{}, ChatGPTState{CodexFound: true}, tr); shown || r.CanRemove ||
		r.Title != "ChatGPT (Codex)" || r.Editor != EditorChatGPT || r.Icon != AIIcon || r.RemoveLabel != "Disconnect" || r.Badge != "This computer" {
		t.Errorf("chatgpt unconnected %+v shown %v", r, shown)
	}
	if _, shown := LocalRowFor(LocalNone, ClaudeState{Registered: true}, ChatGPTState{Connected: true}, tr); shown {
		t.Error("LocalNone is never shown")
	}

	// Both connected: both rows, Claude first, neither in the catalogue.
	both := LocalRows(ClaudeState{Registered: true}, ChatGPTState{Connected: true, Account: "jana@example.invalid", CodexFound: true}, tr)
	if len(both) != 2 || both[0].Kind != LocalClaude || both[1].Kind != LocalChatGPT || !both[0].CanRemove || !both[1].CanRemove {
		t.Fatalf("rows %+v", both)
	}
	for _, s := range CatalogSection(tr, SectionAI) {
		if offered := Offered(s, ClaudeState{Registered: true}, ChatGPTState{Connected: true}); offered != (s.Local == LocalNone) {
			t.Errorf("%s offered %v while both are connected", s.Name, offered)
		}
	}
	// One connected: its row alone, the other still in the catalogue.
	if rows := LocalRows(ClaudeState{}, ChatGPTState{Connected: true}, tr); len(rows) != 1 || rows[0].Kind != LocalChatGPT {
		t.Errorf("chatgpt alone %+v", rows)
	}
	if !Offered(Service{Local: LocalClaude}, ClaudeState{}, ChatGPTState{Connected: true}) ||
		Offered(Service{Local: LocalChatGPT}, ClaudeState{}, ChatGPTState{Connected: true}) {
		t.Error("offered with ChatGPT connected")
	}

	for _, c := range []struct {
		name  string
		state ClaudeState
		want  string
	}{
		{"signed in", ClaudeState{Registered: true, Present: true, Version: "2.1.284", SignInKnown: true, SignedIn: true},
			"Registered in Claude Desktop and Claude Code · Claude Code 2.1.284 · signed in"},
		{"not signed in", ClaudeState{Registered: true, Present: true, Version: "2.1.284", SignInKnown: true},
			"Registered in Claude Desktop and Claude Code · Claude Code 2.1.284 · not signed in"},
		{"sign-in unknown", ClaudeState{Registered: true, Present: true, Version: "2.1.284"},
			"Registered in Claude Desktop and Claude Code · Claude Code 2.1.284"},
		{"path without a version", ClaudeState{Registered: true, Present: true, Path: "/usr/local/bin/claude"},
			"Registered in Claude Desktop and Claude Code · /usr/local/bin/claude"},
		{"no claude code", ClaudeState{Registered: true, SignInKnown: true, SignedIn: true},
			"Registered in Claude Desktop and Claude Code · Claude Code not found"},
		{"cleaned version", ClaudeState{Registered: true, Present: true, Version: " 2.1.284\x00\n"},
			"Registered in Claude Desktop and Claude Code · Claude Code 2.1.284"},
	} {
		rows := LocalRows(c.state, ChatGPTState{}, tr)
		if len(rows) != 1 || rows[0].Kind != LocalClaude {
			t.Fatalf("%s: rows %+v", c.name, rows)
		}
		if r := rows[0]; r.Subtitle != c.want || !r.CanRemove {
			t.Errorf("%s: %q, want %q (can remove %v)", c.name, r.Subtitle, c.want, r.CanRemove)
		}
	}

	for _, c := range []struct {
		name  string
		state ChatGPTState
		want  string
	}{
		{"connected", ChatGPTState{Connected: true, Account: "jana@example.invalid", CodexFound: true}, "Connected as jana@example.invalid"},
		{"connected, no codex", ChatGPTState{Connected: true, Account: "jana@example.invalid"}, "Connected as jana@example.invalid · Codex not found"},
		{"cleaned account", ChatGPTState{Connected: true, Account: "jana@example.invalid\x00", CodexFound: true}, "Connected as jana@example.invalid"},
	} {
		rows := LocalRows(ClaudeState{}, c.state, tr)
		if len(rows) != 1 || rows[0].Kind != LocalChatGPT {
			t.Fatalf("%s: rows %+v", c.name, rows)
		}
		if r := rows[0]; r.Subtitle != c.want || !r.CanRemove {
			t.Errorf("%s: %q, want %q (can remove %v)", c.name, r.Subtitle, c.want, r.CanRemove)
		}
	}
}

func TestFullOrderSkipsNothingOfTheDaemon(t *testing.T) {
	// Local rows are not accounts: FullOrder sees only the daemon's
	// accounts, an ai account among the integrations.
	all := []api.Account{account("m1", ""), aiAccount("acc_1", "AI", api.AIAnthropic, "m"), account("j1", api.AccountJira)}
	got := FullOrder(all, PageIntegrations, []api.AccountID{"j1", "acc_1"})
	if !slices.Equal(got, []api.AccountID{"m1", "j1", "acc_1"}) {
		t.Errorf("%v", got)
	}
}

func TestRowForByKind(t *testing.T) {
	tr := msgids{}
	mail := account("m", "")
	mail.Config.Name = "Work"
	r := RowFor(mail, tr)
	if r.ID != "m" || r.Kind != api.AccountIMAP || r.Title != "Work" || r.Subtitle != "m@example.invalid" ||
		!r.OffersSwitch || r.Editor != EditorMailWizard || r.EditLabel != "Edit Account" || r.Icon != MailIcon || r.Badge != "" {
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
	for _, s := range []string{x.Title, x.Description, x.ConnectedTitle, x.AvailableTitle, x.EmptyTitle, x.EmptySubtitle, x.Add, x.Settings, x.CalendarSettings,
		x.UnregisterHeading, x.UnregisterBody, x.DisconnectHeading, x.DisconnectBody} {
		if s == "" {
			t.Errorf("empty text in %+v", x)
		}
	}
	if x.UnregisterHeading != "Unregister from Claude?" || x.DisconnectHeading != "Disconnect ChatGPT?" {
		t.Errorf("%+v", x)
	}
	if x.Title != "Integrations" || x.Add != "Add…" || x.CalendarSettings != "Calendar Settings" || x.Settings != "Settings" ||
		x.Description != "Services beside your mail: issue trackers, calendars, CRM and AI" {
		t.Errorf("%+v", x)
	}
}
