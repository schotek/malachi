// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package capabilities

import (
	"reflect"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Accounts as the daemon sends them.
var (
	// oldMail is a mail account of a daemon from before capabilities.
	oldMail = api.Account{ID: "a1", Enabled: true, Config: api.AccountConfig{Email: "jana@acme.example"}}
	mail    = api.Account{ID: "a2", Enabled: true, Capabilities: append([]api.AccountCapability{}, api.MailCapabilities...)}
	// jiraM1 reads and flags only.
	jiraM1 = api.Account{ID: "j1", Enabled: true, Config: api.AccountConfig{Kind: api.AccountJira}, Capabilities: []api.AccountCapability{}}
	// jiraM2 comments and forwards into a mail account.
	jiraM2 = api.Account{
		ID: "j2", Enabled: true, Config: api.AccountConfig{Kind: api.AccountJira},
		Capabilities: []api.AccountCapability{api.CapabilityComment, api.CapabilityForward},
	}
)

func TestCan(t *testing.T) {
	all := []api.AccountCapability{
		api.CapabilityCompose, api.CapabilityReply, api.CapabilityReplyAll, api.CapabilityForward,
		api.CapabilityComment, api.CapabilityMove, api.CapabilityDelete, "unknown",
	}
	tests := []struct {
		name string
		acc  api.Account
		want map[api.AccountCapability]bool
	}{
		{"nil means mail", oldMail, map[api.AccountCapability]bool{
			api.CapabilityCompose: true, api.CapabilityReply: true, api.CapabilityReplyAll: true,
			api.CapabilityForward: true, api.CapabilityMove: true, api.CapabilityDelete: true,
		}},
		{"mail", mail, map[api.AccountCapability]bool{
			api.CapabilityCompose: true, api.CapabilityReply: true, api.CapabilityReplyAll: true,
			api.CapabilityForward: true, api.CapabilityMove: true, api.CapabilityDelete: true,
		}},
		{"empty means none", jiraM1, map[api.AccountCapability]bool{}},
		{"jira comments and forwards", jiraM2, map[api.AccountCapability]bool{api.CapabilityComment: true, api.CapabilityForward: true}},
		{"unknown values", api.Account{Capabilities: []api.AccountCapability{"transition"}}, map[api.AccountCapability]bool{"transition": true}},
	}
	for _, tt := range tests {
		for _, c := range append(all, "transition") {
			if got := Can(tt.acc, c); got != tt.want[c] {
				t.Errorf("%s: Can(%q) = %v, want %v", tt.name, c, got, tt.want[c])
			}
		}
	}
}

func TestSupported(t *testing.T) {
	everything := Actions{Reply: true, ReplyAll: true, Forward: true, Move: true, Trash: true, Archive: true, Junk: true}
	tests := []struct {
		name string
		s    Situation
		want Actions
	}{
		{"no folder listed", Situation{}, everything},
		{"old daemon", Situation{Account: oldMail}, everything},
		{"mail", Situation{Account: mail}, everything},
		{"mail without another compose account", Situation{Account: mail, ComposeAccount: false}, everything},
		{"jira M1", Situation{Account: jiraM1, ComposeAccount: true}, Actions{}},
		{"jira M1 outbox", Situation{Account: jiraM1, Outbox: true}, Actions{Trash: true}},
		{"jira M2", Situation{Account: jiraM2, ComposeAccount: true}, Actions{Reply: true, Forward: true, Comment: true}},
		{"jira M2 without a mail account", Situation{Account: jiraM2}, Actions{Reply: true, Comment: true}},
		{"reply only", Situation{Account: api.Account{Capabilities: []api.AccountCapability{api.CapabilityReply}}}, Actions{Reply: true}},
		{"move without delete", Situation{Account: api.Account{Capabilities: []api.AccountCapability{api.CapabilityMove}}}, Actions{Move: true, Archive: true, Junk: true}},
		{"forward with compose of its own", Situation{Account: api.Account{Capabilities: []api.AccountCapability{api.CapabilityForward, api.CapabilityCompose}}}, Actions{Forward: true}},
	}
	for _, tt := range tests {
		if got := Supported(tt.s); got != tt.want {
			t.Errorf("%s: Supported = %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

func TestAvailable(t *testing.T) {
	tests := []struct {
		name string
		s    Situation
		want Actions
	}{
		{"nothing selected", Situation{Account: mail, Archive: true, Junk: true, ComposeAccount: true}, Actions{}},
		{"nothing selected in jira keeps the label", Situation{Account: jiraM2, ComposeAccount: true}, Actions{Comment: true}},
		{"mail message", Situation{Account: mail, Selected: true, Archive: true, Junk: true, ComposeAccount: true},
			Actions{Reply: true, ReplyAll: true, Forward: true, Move: true, Trash: true, Archive: true, Junk: true}},
		{"mail without archive and junk folders", Situation{Account: mail, Selected: true},
			Actions{Reply: true, ReplyAll: true, Forward: true, Move: true, Trash: true}},
		{"old daemon", Situation{Account: oldMail, Selected: true, Archive: true},
			Actions{Reply: true, ReplyAll: true, Forward: true, Move: true, Trash: true, Archive: true}},
		{"queued message", Situation{Account: mail, Selected: true, Outbox: true, Archive: true, Junk: true},
			Actions{Reply: true, ReplyAll: true, Forward: true, Trash: true}},
		{"jira M1", Situation{Account: jiraM1, Selected: true, Archive: true, Junk: true, ComposeAccount: true}, Actions{}},
		{"jira M1 queued comment", Situation{Account: jiraM1, Selected: true, Outbox: true}, Actions{Trash: true}},
		{"jira M2", Situation{Account: jiraM2, Selected: true, ComposeAccount: true},
			Actions{Reply: true, Forward: true, Comment: true}},
		{"jira M2 without a mail account", Situation{Account: jiraM2, Selected: true},
			Actions{Reply: true, Comment: true}},
	}
	for _, tt := range tests {
		if got := Available(tt.s); got != tt.want {
			t.Errorf("%s: Available = %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

func TestForwardAccounts(t *testing.T) {
	paused := api.Account{ID: "p", Enabled: false}
	accounts := []api.Account{jiraM2, paused, mail, jiraM1, oldMail}
	got := ForwardAccounts(accounts)
	if want := []api.Account{mail, oldMail}; !reflect.DeepEqual(got, want) {
		t.Errorf("ForwardAccounts = %v, want %v", ids(got), ids(want))
	}
	if got := ForwardAccounts([]api.Account{jiraM1, paused}); len(got) != 0 {
		t.Errorf("ForwardAccounts without mail = %v", ids(got))
	}

	tests := []struct {
		from api.AccountID
		want api.AccountID
		ok   bool
	}{
		{"a1", "a1", true}, // a mail message goes out from its own account
		{"a2", "a2", true},
		{"j2", "a2", true}, // an issue from the first mail account
		{"p", "a2", true},  // a paused account's message too
		{"missing", "a2", true},
	}
	for _, tt := range tests {
		acc, ok := ForwardFrom(accounts, tt.from)
		if acc.ID != tt.want || ok != tt.ok {
			t.Errorf("ForwardFrom(%q) = %q, %v; want %q, %v", tt.from, acc.ID, ok, tt.want, tt.ok)
		}
	}
	if acc, ok := ForwardFrom([]api.Account{jiraM2, paused}, "j2"); ok || acc.ID != "" {
		t.Errorf("ForwardFrom without mail = %q, %v", acc.ID, ok)
	}
}

func ids(list []api.Account) []api.AccountID {
	var out []api.AccountID
	for _, a := range list {
		out = append(out, a.ID)
	}
	return out
}

func TestComposeAccounts(t *testing.T) {
	paused := api.Account{ID: "p", Enabled: false}
	accounts := []api.Account{jiraM2, paused, mail, jiraM1, oldMail}
	got := ComposeAccounts(accounts)
	if want := []api.Account{paused, mail, oldMail}; !reflect.DeepEqual(got, want) {
		t.Errorf("ComposeAccounts = %v, want %v", ids(got), ids(want))
	}
	if got := ComposeAccounts([]api.Account{jiraM1, jiraM2}); len(got) != 0 {
		t.Errorf("ComposeAccounts of issue trackers = %v", ids(got))
	}

	tests := []struct {
		name     string
		accounts []api.Account
		want     bool
	}{
		{"nothing known yet", nil, true},
		{"mail", []api.Account{mail}, true},
		{"old daemon", []api.Account{oldMail}, true},
		{"a paused mail account", []api.Account{jiraM2, paused}, true},
		{"issue trackers alone", []api.Account{jiraM2, jiraM1}, false},
		{"reply only", []api.Account{{ID: "r", Enabled: true, Capabilities: []api.AccountCapability{api.CapabilityReply}}}, false},
	}
	for _, tt := range tests {
		if got := CanComposeNew(tt.accounts); got != tt.want {
			t.Errorf("%s: CanComposeNew = %v, want %v", tt.name, got, tt.want)
		}
	}
}
