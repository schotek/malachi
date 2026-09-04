// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestSyncStatusText(t *testing.T) {
	accounts := []api.Account{
		{ID: "a1", Config: api.AccountConfig{Name: "Work", Email: "w@example.invalid"}, Enabled: true},
		{ID: "a2", Config: api.AccountConfig{Email: "home@example.invalid"}, Enabled: true},
		{ID: "a3", Config: api.AccountConfig{Name: "Paused"}, Enabled: false},
	}
	folderName := func(acc api.AccountID, id api.FolderID) string {
		if acc == "a1" && id == "f_inbox" {
			return "Inbox"
		}
		return ""
	}
	st := func(states ...api.SyncState) map[api.AccountID]api.SyncState {
		m := make(map[api.AccountID]api.SyncState, len(states))
		for _, s := range states {
			m[s.AccountID] = s
		}
		return m
	}

	cases := []struct {
		name     string
		states   map[api.AccountID]api.SyncState
		accounts []api.Account
		want     string
		spinning bool
	}{
		{"no accounts", st(), nil, "", false},
		{"only disabled", st(api.SyncState{AccountID: "a3", Status: api.SyncSyncing}), accounts[2:], "", false},
		{"no states, idle from account.list", st(), accounts, "Up to date", false},
		{"idle", st(api.SyncState{AccountID: "a1", Status: api.SyncIdle, Progress: -1}, api.SyncState{AccountID: "a2", Status: api.SyncIdle, Progress: -1}), accounts, "Up to date", false},
		{"syncing folder with progress", st(api.SyncState{AccountID: "a1", Status: api.SyncSyncing, FolderID: "f_inbox", Progress: 42}), accounts, "Syncing Inbox… 42 %", true},
		{"syncing folder without progress", st(api.SyncState{AccountID: "a1", Status: api.SyncSyncing, FolderID: "f_inbox", Progress: -1}), accounts, "Syncing Inbox…", true},
		{"syncing unknown folder falls back to the account", st(api.SyncState{AccountID: "a1", Status: api.SyncSyncing, FolderID: "f_gone", Progress: 0}), accounts, "Syncing Work… 0 %", true},
		{"syncing whole account, unnamed account uses the address", st(api.SyncState{AccountID: "a2", Status: api.SyncSyncing, Progress: -1}), accounts, "Syncing home@example.invalid…", true},
		{"syncing beats authRequired", st(api.SyncState{AccountID: "a1", Status: api.SyncAuthRequired}, api.SyncState{AccountID: "a2", Status: api.SyncSyncing, Progress: -1}), accounts, "Syncing home@example.invalid…", true},
		{"authRequired beats error", st(api.SyncState{AccountID: "a1", Status: api.SyncError}, api.SyncState{AccountID: "a2", Status: api.SyncAuthRequired}), accounts, "Sign-in required", false},
		{"error beats offline", st(api.SyncState{AccountID: "a1", Status: api.SyncOffline}, api.SyncState{AccountID: "a2", Status: api.SyncError}), accounts, "Sync error", false},
		{"offline beats idle", st(api.SyncState{AccountID: "a1", Status: api.SyncIdle}, api.SyncState{AccountID: "a2", Status: api.SyncOffline}), accounts, "Offline, retrying", false},
		{"disabled account state is ignored", st(api.SyncState{AccountID: "a3", Status: api.SyncError}, api.SyncState{AccountID: "a1", Status: api.SyncIdle}), accounts, "Up to date", false},
		{"state of an unknown account is ignored", st(api.SyncState{AccountID: "zzz", Status: api.SyncError}), accounts, "Up to date", false},
		{"account.list state used when uncached", st(), []api.Account{{ID: "a9", Enabled: true, State: api.SyncState{AccountID: "a9", Status: api.SyncOffline}}}, "Offline, retrying", false},
		// Sending: the outbox count, summed over the enabled accounts, with
		// the spinner on. Precedence: syncing > authRequired > sending >
		// error > offline.
		{"sending one", st(api.SyncState{AccountID: "a1", Status: api.SyncIdle, PendingOutbox: 1}), accounts, "Sending 1 message…", true},
		{"sending several", st(api.SyncState{AccountID: "a1", Status: api.SyncIdle, PendingOutbox: 3}), accounts, "Sending 3 messages…", true},
		{"sending summed over accounts", st(api.SyncState{AccountID: "a1", Status: api.SyncIdle, PendingOutbox: 1}, api.SyncState{AccountID: "a2", Status: api.SyncIdle, PendingOutbox: 1}), accounts, "Sending 2 messages…", true},
		{"syncing beats sending", st(api.SyncState{AccountID: "a1", Status: api.SyncSyncing, FolderID: "f_inbox", Progress: -1, PendingOutbox: 2}), accounts, "Syncing Inbox…", true},
		{"authRequired beats sending", st(api.SyncState{AccountID: "a1", Status: api.SyncAuthRequired}, api.SyncState{AccountID: "a2", Status: api.SyncIdle, PendingOutbox: 1}), accounts, "Sign-in required", false},
		{"sending beats error", st(api.SyncState{AccountID: "a1", Status: api.SyncError, PendingOutbox: 1}), accounts, "Sending 1 message…", true},
		{"sending beats offline", st(api.SyncState{AccountID: "a1", Status: api.SyncOffline}, api.SyncState{AccountID: "a2", Status: api.SyncIdle, PendingOutbox: 1}), accounts, "Sending 1 message…", true},
		{"disabled account outbox is ignored", st(api.SyncState{AccountID: "a3", Status: api.SyncIdle, PendingOutbox: 4}, api.SyncState{AccountID: "a1", Status: api.SyncIdle}), accounts, "Up to date", false},
		{"uncached account outbox from account.list", st(), []api.Account{{ID: "a9", Enabled: true, State: api.SyncState{AccountID: "a9", Status: api.SyncIdle, PendingOutbox: 1}}}, "Sending 1 message…", true},
	}
	for _, c := range cases {
		text, spinning := syncStatusText(c.states, c.accounts, folderName)
		if text != c.want || spinning != c.spinning {
			t.Errorf("%s: got %q/%v, want %q/%v", c.name, text, spinning, c.want, c.spinning)
		}
	}

	// A nil folderName must not crash and falls back to the account name.
	if text, _ := syncStatusText(st(api.SyncState{AccountID: "a1", Status: api.SyncSyncing, FolderID: "f_inbox", Progress: -1}), accounts, nil); text != "Syncing Work…" {
		t.Errorf("nil folderName: got %q", text)
	}
}

func TestAuthBannerText(t *testing.T) {
	cases := map[api.ErrorCode]string{
		api.CodeAuthRequired: "Sign in to Work again",
		api.CodeAuthFailed:   "Sign in to Work again",
		api.CodeKeyringError: "The system keyring is unavailable; Work cannot sign in",
		api.CodeNetworkError: "Work needs attention",
		0:                    "Work needs attention",
	}
	for reason, want := range cases {
		if got := authBannerText(reason, "Work"); got != want {
			t.Errorf("authBannerText(%d) = %q, want %q", reason, got, want)
		}
	}
}
