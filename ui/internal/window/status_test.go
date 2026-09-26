// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"fmt"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/signin"
)

func TestAccountStatuses(t *testing.T) {
	now := time.Date(2026, time.September, 2, 15, 30, 0, 0, time.Local)
	at := func(t time.Time) *time.Time { return &t }
	folderName := func(acc api.AccountID, id api.FolderID) string {
		if acc == "a1" && id == "f_inbox" {
			return "Inbox"
		}
		return ""
	}
	work := api.Account{ID: "a1", Config: api.AccountConfig{Name: "Work", Email: "w@example.invalid"}, Enabled: true}
	cases := []struct {
		name   string
		state  api.SyncState
		detail string
		action statusAction
		failed int
	}{
		// Syncing names the folder and its progress, never the account,
		// whose name is the row's title.
		{"syncing folder with progress", api.SyncState{Status: api.SyncSyncing, FolderID: "f_inbox", Progress: 42}, "Syncing Inbox… 42 %", statusActionCheck, 0},
		{"syncing folder without progress", api.SyncState{Status: api.SyncSyncing, FolderID: "f_inbox", Progress: -1}, "Syncing Inbox…", statusActionCheck, 0},
		{"syncing the whole account", api.SyncState{Status: api.SyncSyncing, Progress: 30}, "Syncing…", statusActionCheck, 0},
		{"syncing an unknown folder", api.SyncState{Status: api.SyncSyncing, FolderID: "f_gone", Progress: 30}, "Syncing…", statusActionCheck, 0},
		{"syncing beats sign-in", api.SyncState{Status: api.SyncSyncing, Progress: -1, PendingOutbox: 1}, "Syncing…", statusActionCheck, 0},
		{"sign-in required", api.SyncState{Status: api.SyncAuthRequired, PendingOutbox: 1}, "Sign-in required", statusActionSignIn, 0},
		// A refused or changed certificate says why and leads to the
		// account's settings, where it can be trusted.
		{"refused certificate", tlsState("a1", api.SyncOffline, api.TLSUntrusted), "The server's certificate is not from a trusted authority", statusActionEdit, 0},
		{"changed certificate", tlsState("a1", api.SyncError, api.TLSPinMismatch), "The server presented a different certificate than the one you trust", statusActionEdit, 0},
		{"certificate beats sending", withOutbox(tlsState("a1", api.SyncOffline, api.TLSExpired), 1, 0), "The server's certificate has expired", statusActionEdit, 0},
		{"sending one", api.SyncState{Status: api.SyncIdle, PendingOutbox: 1}, "Sending 1 message…", statusActionCheck, 0},
		{"sending beats error", api.SyncState{Status: api.SyncError, PendingOutbox: 2}, "Sending 2 messages…", statusActionCheck, 0},
		// Error and offline give the reason when there is one, and a retry.
		{"error with reason", api.SyncState{Status: api.SyncError, Error: api.NewError(api.CodeServerError, "x")}, "The server returned an error", statusActionRetry, 0},
		{"error without reason", api.SyncState{Status: api.SyncError}, "Sync error", statusActionRetry, 0},
		{"offline with reason", api.SyncState{Status: api.SyncOffline, Error: api.NewError(api.CodeNetworkError, "dial")}, "The server could not be reached", statusActionRetry, 0},
		{"offline without reason", api.SyncState{Status: api.SyncOffline}, "Offline, retrying", statusActionRetry, 0},
		{"handshake failure is offline", tlsState("a1", api.SyncOffline, api.TLSHandshake), "The secure connection could not be established", statusActionRetry, 0},
		{"backend detail in a reason", api.SyncState{Status: api.SyncError, Error: api.NewError(api.CodeStorageError, "<b>boom</b>")}, "Failed: <b>boom</b>", statusActionRetry, 0},
		// Idle names the last check, as a time today and a date before.
		{"idle, checked today", api.SyncState{Status: api.SyncIdle, LastSync: at(now.Add(-2 * time.Hour))}, "Last synced 13:30", statusActionCheck, 0},
		{"idle, checked yesterday", api.SyncState{Status: api.SyncIdle, LastSync: at(now.AddDate(0, 0, -1))}, "Last synced 1 Sep", statusActionCheck, 0},
		{"idle, never checked", api.SyncState{Status: api.SyncIdle}, "Up to date", statusActionCheck, 0},
		// Unsent messages are counted whatever the state.
		{"failed while idle", api.SyncState{Status: api.SyncIdle, FailedOutbox: 2}, "Up to date", statusActionCheck, 2},
		{"failed while offline", api.SyncState{Status: api.SyncOffline, FailedOutbox: 1}, "Offline, retrying", statusActionRetry, 1},
		{"failed while sending", api.SyncState{Status: api.SyncIdle, PendingOutbox: 1, FailedOutbox: 1}, "Sending 1 message…", statusActionCheck, 1},
	}
	for _, c := range cases {
		c.state.AccountID = work.ID
		got := accountStatuses(map[api.AccountID]api.SyncState{work.ID: c.state}, []api.Account{work}, folderName, now)
		if len(got) != 1 {
			t.Fatalf("%s: %d rows", c.name, len(got))
		}
		g := got[0]
		if g.Account != "a1" || g.Title != "Work" || g.Detail != c.detail || g.Action != c.action || g.Failed != c.failed {
			t.Errorf("%s: got %+v, want %q action %d failed %d", c.name, g, c.detail, c.action, c.failed)
		}
	}
	// A nil folderName must not crash.
	if got := accountStatuses(map[api.AccountID]api.SyncState{"a1": {AccountID: "a1", Status: api.SyncSyncing, FolderID: "f_inbox", Progress: 5}}, []api.Account{work}, nil, now); got[0].Detail != "Syncing…" {
		t.Errorf("nil folderName: %q", got[0].Detail)
	}
}

// withOutbox sets the outbox counts of a state.
func withOutbox(s api.SyncState, pending, failed int) api.SyncState {
	s.PendingOutbox, s.FailedOutbox = pending, failed
	return s
}

func TestAccountStatusesAccounts(t *testing.T) {
	now := time.Date(2026, time.September, 2, 15, 30, 0, 0, time.Local)
	accounts := []api.Account{
		{ID: "a1", Config: api.AccountConfig{Name: "Work"}, Enabled: true,
			State: api.SyncState{AccountID: "a1", Status: api.SyncOffline, FailedOutbox: 3}},
		{ID: "a2", Config: api.AccountConfig{Email: "home@example.invalid"}, Enabled: false,
			State: api.SyncState{AccountID: "a2", Status: api.SyncDisabled, FailedOutbox: 1}},
		{ID: "a3", Config: api.AccountConfig{Name: "Paused, fresh"}, Enabled: false,
			State: api.SyncState{AccountID: "a3", Status: api.SyncDisabled, FailedOutbox: 5}},
		{ID: "a4", Config: api.AccountConfig{Name: "Graph", Kind: api.AccountGraph}, Enabled: true},
		{ID: "a5", Config: api.AccountConfig{Name: "GOA", Kind: api.AccountGraph, Graph: &api.GraphConfig{Source: api.GraphSourceGOA}}, Enabled: true},
	}
	states := map[api.AccountID]api.SyncState{
		// a1 has no cached state: account.list's is used.
		// a2 was paused while in error; the state from before the pause
		// is stale and must not show.
		"a2": {AccountID: "a2", Status: api.SyncError, FailedOutbox: 7},
		// a3 changed its outbox after the pause: that state is newer.
		"a3": {AccountID: "a3", Status: api.SyncDisabled, FailedOutbox: 0},
		"a4": {AccountID: "a4", Status: api.SyncAuthRequired},
		"a5": {AccountID: "a5", Status: api.SyncAuthRequired},
		// A state for an account that is not listed makes no row.
		"zzz": {AccountID: "zzz", Status: api.SyncError},
	}
	got := accountStatuses(states, accounts, nil, now)
	want := []accountStatus{
		{Account: "a1", Title: "Work", Detail: "Offline, retrying", Action: statusActionRetry, SignIn: signin.Password, Failed: 3},
		{Account: "a2", Title: "home@example.invalid", Detail: "Paused", Action: statusActionNone, SignIn: signin.Password, Failed: 1},
		{Account: "a3", Title: "Paused, fresh", Detail: "Paused", Action: statusActionNone, SignIn: signin.Password, Failed: 0},
		{Account: "a4", Title: "Graph", Detail: "Sign-in required", Action: statusActionSignIn, SignIn: signin.OAuth},
		{Account: "a5", Title: "GOA", Detail: "Sign-in required", Action: statusActionSignIn, SignIn: signin.GOA},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
	if got := accountStatuses(states, nil, nil, now); len(got) != 0 {
		t.Errorf("no accounts: %+v", got)
	}
}

func TestStatusButtonLabel(t *testing.T) {
	for _, c := range []struct {
		st   accountStatus
		want string
	}{
		{accountStatus{Action: statusActionNone}, ""},
		{accountStatus{Action: statusActionCheck}, ""}, // an icon of its own
		{accountStatus{Action: statusActionRetry}, "Try Again"},
		{accountStatus{Action: statusActionEdit}, "_Edit Account…"},
		{accountStatus{Action: statusActionSignIn, SignIn: signin.Password}, "Open Preferences"},
		{accountStatus{Action: statusActionSignIn, SignIn: signin.GOA}, "Open Online Accounts"},
		{accountStatus{Action: statusActionSignIn, SignIn: signin.OAuth}, "Sign In"},
	} {
		if got := statusButtonLabel(c.st); got != c.want {
			t.Errorf("statusButtonLabel(%+v) = %q, want %q", c.st, got, c.want)
		}
	}
}

func TestStatusLineFor(t *testing.T) {
	info := &api.SystemInfoResult{Version: "1.2.3", PID: 42, ProtocolVersion: api.ProtocolVersion}
	other := &api.SystemInfoResult{Version: "9", PID: 7, ProtocolVersion: api.ProtocolVersion + 1}
	mismatch := fmt.Sprintf("Protocol mismatch: UI %d, backend %d", api.ProtocolVersion, api.ProtocolVersion+1)
	for _, c := range []struct {
		name     string
		conn     connView
		text     string
		spinning bool
		want     statusLine
	}{
		{"connecting", connView{State: client.Connecting}, "Up to date", false,
			statusLine{Text: "Connecting to backend…", Icon: "network-idle-symbolic"}},
		{"unavailable", connView{State: client.Disconnected}, "Syncing Inbox…", true,
			statusLine{Text: "Backend unavailable", Icon: "network-offline-symbolic"}},
		// What the daemon said before it went away does not count.
		{"unavailable forgets", connView{State: client.Disconnected, Info: info, SyncFailed: true}, "Up to date", false,
			statusLine{Text: "Backend unavailable", Icon: "network-offline-symbolic"}},
		{"connected, system.info pending", connView{State: client.Connected}, "Syncing Inbox…", true,
			statusLine{Text: "Syncing Inbox…", Spinning: true, Active: true}},
		{"connected", connView{State: client.Connected, Info: info}, "Up to date · 15:04", false,
			statusLine{Text: "Up to date · 15:04", Active: true, Daemon: "Connected to malachid 1.2.3 (pid 42)"}},
		{"system.info failed", connView{State: client.Connected, InfoFailed: true}, "Up to date", false,
			statusLine{Text: "Up to date", Active: true, Daemon: "Connected, but system.info failed"}},
		{"protocol mismatch", connView{State: client.Connected, Info: other}, "Syncing Inbox…", true,
			statusLine{Text: mismatch, Active: true}},
		{"protocol mismatch beats sync.status", connView{State: client.Connected, Info: other, SyncFailed: true}, "Up to date", false,
			statusLine{Text: mismatch, Active: true}},
		{"sync.status failed", connView{State: client.Connected, Info: info, SyncFailed: true}, "Syncing Inbox…", true,
			statusLine{Text: "Not syncing", Active: true, Daemon: "Connected to malachid 1.2.3 (pid 42)"}},
		{"no account", connView{State: client.Connected, Info: info}, "", false,
			statusLine{Daemon: "Connected to malachid 1.2.3 (pid 42)"}},
		{"no account, sync.status failed", connView{State: client.Connected, SyncFailed: true}, "", false,
			statusLine{Text: "Not syncing", Active: true}},
	} {
		if got := statusLineFor(c.conn, c.text, c.spinning); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestSameAccounts(t *testing.T) {
	list := []accountStatus{{Account: "a1"}, {Account: "a2"}}
	for _, c := range []struct {
		order []api.AccountID
		want  bool
	}{
		{[]api.AccountID{"a1", "a2"}, true},
		{[]api.AccountID{"a2", "a1"}, false}, // reordered
		{[]api.AccountID{"a1"}, false},       // added
		{[]api.AccountID{"a1", "a2", "a3"}, false},
		{nil, false},
	} {
		if got := sameAccounts(c.order, list); got != c.want {
			t.Errorf("sameAccounts(%v) = %v, want %v", c.order, got, c.want)
		}
	}
	if !sameAccounts(nil, nil) {
		t.Error("no accounts, no rows: nothing to rebuild")
	}
}
