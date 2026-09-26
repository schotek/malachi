// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/certtrust"
	"github.com/schotek/malachi/ui/internal/signin"
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
	now := time.Date(2026, time.September, 2, 15, 30, 0, 0, time.Local)
	at := func(t time.Time) *time.Time { return &t }
	idle := func(id api.AccountID, last time.Time) api.SyncState {
		return api.SyncState{AccountID: id, Status: api.SyncIdle, Progress: -1, LastSync: at(last)}
	}
	// single is the first account alone: a state never names it.
	single := accounts[:1]

	cases := []struct {
		name     string
		states   map[api.AccountID]api.SyncState
		accounts []api.Account
		want     string
		spinning bool
	}{
		{"no accounts", st(), nil, "", false},
		{"only disabled", st(api.SyncState{AccountID: "a3", Status: api.SyncSyncing}), accounts[2:], "Paused", false},
		{"no states, idle from account.list", st(), accounts, "Up to date", false},
		{"idle", st(api.SyncState{AccountID: "a1", Status: api.SyncIdle, Progress: -1}, api.SyncState{AccountID: "a2", Status: api.SyncIdle, Progress: -1}), accounts, "Up to date", false},
		{"syncing folder with progress", st(api.SyncState{AccountID: "a1", Status: api.SyncSyncing, FolderID: "f_inbox", Progress: 42}), accounts, "Syncing Inbox… 42 %", true},
		{"syncing folder without progress", st(api.SyncState{AccountID: "a1", Status: api.SyncSyncing, FolderID: "f_inbox", Progress: -1}), accounts, "Syncing Inbox…", true},
		{"syncing unknown folder falls back to the account", st(api.SyncState{AccountID: "a1", Status: api.SyncSyncing, FolderID: "f_gone", Progress: 0}), accounts, "Syncing Work… 0 %", true},
		{"syncing whole account, unnamed account uses the address", st(api.SyncState{AccountID: "a2", Status: api.SyncSyncing, Progress: -1}), accounts, "Syncing home@example.invalid…", true},
		{"syncing beats authRequired", st(api.SyncState{AccountID: "a1", Status: api.SyncAuthRequired}, api.SyncState{AccountID: "a2", Status: api.SyncSyncing, Progress: -1}), accounts, "Syncing home@example.invalid…", true},
		{"account.list state used when uncached", st(), []api.Account{{ID: "a9", Enabled: true, State: api.SyncState{AccountID: "a9", Status: api.SyncOffline}}}, "Offline, retrying", false},
		{"disabled account state is ignored", st(api.SyncState{AccountID: "a3", Status: api.SyncError}, api.SyncState{AccountID: "a1", Status: api.SyncIdle}), accounts, "Up to date", false},
		{"state of an unknown account is ignored", st(api.SyncState{AccountID: "zzz", Status: api.SyncError}), accounts, "Up to date", false},

		// Sign-in required, error and offline name the one account in that
		// state among several enabled ones, by name or else by address.
		// Precedence: syncing > authRequired > error > offline.
		{"authRequired beats error, one of two named", st(api.SyncState{AccountID: "a1", Status: api.SyncError}, api.SyncState{AccountID: "a2", Status: api.SyncAuthRequired}), accounts, "Sign-in required: home@example.invalid", false},
		{"error beats offline, one of two named", st(api.SyncState{AccountID: "a1", Status: api.SyncOffline}, api.SyncState{AccountID: "a2", Status: api.SyncError}), accounts, "Sync error: home@example.invalid", false},
		{"offline beats idle, one of two named", st(api.SyncState{AccountID: "a1", Status: api.SyncIdle}, api.SyncState{AccountID: "a2", Status: api.SyncOffline}), accounts, "Offline: home@example.invalid", false},
		{"offline named by account name", st(api.SyncState{AccountID: "a1", Status: api.SyncOffline}), accounts, "Offline: Work", false},
		{"two signing in: no name", st(api.SyncState{AccountID: "a1", Status: api.SyncAuthRequired}, api.SyncState{AccountID: "a2", Status: api.SyncAuthRequired}), accounts, "Sign-in required", false},
		{"two in error: no name", st(api.SyncState{AccountID: "a1", Status: api.SyncError}, api.SyncState{AccountID: "a2", Status: api.SyncError}), accounts, "Sync error", false},
		{"two offline: no name", st(api.SyncState{AccountID: "a1", Status: api.SyncOffline}, api.SyncState{AccountID: "a2", Status: api.SyncOffline}), accounts, "Offline, retrying", false},
		{"single account signing in: no name", st(api.SyncState{AccountID: "a1", Status: api.SyncAuthRequired}), single, "Sign-in required", false},
		{"single account in error: no name", st(api.SyncState{AccountID: "a1", Status: api.SyncError}), single, "Sync error", false},
		{"single account offline: no name", st(api.SyncState{AccountID: "a1", Status: api.SyncOffline}), single, "Offline, retrying", false},
		// A paused account neither counts as a second one nor gets named.
		{"paused account does not count", st(api.SyncState{AccountID: "a1", Status: api.SyncError}), []api.Account{accounts[0], accounts[2]}, "Sync error", false},
		{"paused account in error is not named", st(api.SyncState{AccountID: "a3", Status: api.SyncError}, api.SyncState{AccountID: "a2", Status: api.SyncError}), accounts, "Sync error: home@example.invalid", false},

		// Sending: the outbox count, summed over the enabled accounts, with
		// the spinner on. Precedence: syncing > authRequired > sending >
		// failed > error > offline.
		{"sending one", st(api.SyncState{AccountID: "a1", Status: api.SyncIdle, PendingOutbox: 1}), accounts, "Sending 1 message…", true},
		{"sending several", st(api.SyncState{AccountID: "a1", Status: api.SyncIdle, PendingOutbox: 3}), accounts, "Sending 3 messages…", true},
		{"sending summed over accounts", st(api.SyncState{AccountID: "a1", Status: api.SyncIdle, PendingOutbox: 1}, api.SyncState{AccountID: "a2", Status: api.SyncIdle, PendingOutbox: 1}), accounts, "Sending 2 messages…", true},
		{"syncing beats sending", st(api.SyncState{AccountID: "a1", Status: api.SyncSyncing, FolderID: "f_inbox", Progress: -1, PendingOutbox: 2}), accounts, "Syncing Inbox…", true},
		{"authRequired beats sending", st(api.SyncState{AccountID: "a1", Status: api.SyncAuthRequired}, api.SyncState{AccountID: "a2", Status: api.SyncIdle, PendingOutbox: 1}), accounts, "Sign-in required: Work", false},
		{"sending beats error", st(api.SyncState{AccountID: "a1", Status: api.SyncError, PendingOutbox: 1}), accounts, "Sending 1 message…", true},
		{"sending beats offline", st(api.SyncState{AccountID: "a1", Status: api.SyncOffline}, api.SyncState{AccountID: "a2", Status: api.SyncIdle, PendingOutbox: 1}), accounts, "Sending 1 message…", true},
		{"disabled account outbox is ignored", st(api.SyncState{AccountID: "a3", Status: api.SyncIdle, PendingOutbox: 4}, api.SyncState{AccountID: "a1", Status: api.SyncIdle}), accounts, "Up to date", false},
		{"uncached account outbox from account.list", st(), []api.Account{{ID: "a9", Enabled: true, State: api.SyncState{AccountID: "a9", Status: api.SyncIdle, PendingOutbox: 1}}}, "Sending 1 message…", true},

		// Not sent: the failed outbox messages, summed like the pending
		// ones, without the spinner.
		{"failed one", st(api.SyncState{AccountID: "a1", Status: api.SyncIdle, FailedOutbox: 1}), accounts, "1 message not sent", false},
		{"failed summed over accounts", st(api.SyncState{AccountID: "a1", Status: api.SyncIdle, FailedOutbox: 2}, api.SyncState{AccountID: "a2", Status: api.SyncIdle, FailedOutbox: 1}), accounts, "3 messages not sent", false},
		{"sending beats failed", st(api.SyncState{AccountID: "a1", Status: api.SyncIdle, PendingOutbox: 1, FailedOutbox: 2}), accounts, "Sending 1 message…", true},
		{"authRequired beats failed", st(api.SyncState{AccountID: "a1", Status: api.SyncIdle, FailedOutbox: 1}, api.SyncState{AccountID: "a2", Status: api.SyncAuthRequired}), accounts, "Sign-in required: home@example.invalid", false},
		{"certificate beats failed", st(api.SyncState{AccountID: "a1", Status: api.SyncIdle, FailedOutbox: 1}, tlsState("a2", api.SyncOffline, api.TLSUntrusted)), accounts, "Certificate problem", false},
		{"failed beats error", st(api.SyncState{AccountID: "a1", Status: api.SyncError, FailedOutbox: 1}), accounts, "1 message not sent", false},
		{"failed beats offline", st(api.SyncState{AccountID: "a1", Status: api.SyncOffline}, api.SyncState{AccountID: "a2", Status: api.SyncIdle, FailedOutbox: 1}), accounts, "1 message not sent", false},
		{"disabled account failed is ignored", st(api.SyncState{AccountID: "a3", Status: api.SyncDisabled, FailedOutbox: 4}, api.SyncState{AccountID: "a1", Status: api.SyncIdle}), accounts, "Up to date", false},
		{"uncached account failed from account.list", st(), []api.Account{{ID: "a9", Enabled: true, State: api.SyncState{AccountID: "a9", Status: api.SyncIdle, FailedOutbox: 2}}}, "2 messages not sent", false},

		// Idle names the newest last check of the enabled accounts, as a
		// time today and as a date before.
		{"last check today", st(idle("a1", now.Add(-2*time.Hour))), accounts, "Up to date · 13:30", false},
		{"newest last check wins", st(idle("a1", now.Add(-2*time.Hour)), idle("a2", now.Add(-time.Hour))), accounts, "Up to date · 14:30", false},
		{"last check yesterday", st(idle("a1", now.AddDate(0, 0, -1))), accounts, "Up to date · 1 Sep", false},
		{"disabled account last check is ignored", st(idle("a1", now.Add(-2*time.Hour)), idle("a3", now.Add(-time.Minute))), accounts, "Up to date · 13:30", false},
		{"last check from account.list", st(), []api.Account{{ID: "a9", Enabled: true, State: idle("a9", now.Add(-30*time.Minute))}}, "Up to date · 15:00", false},
		{"a last check is no excuse for offline", st(idle("a1", now.Add(-time.Hour)), api.SyncState{AccountID: "a2", Status: api.SyncOffline, LastSync: at(now)}), accounts, "Offline: home@example.invalid", false},

		// Certificates: a refused or changed certificate is named instead
		// of offline, right after authRequired; a handshake failure stays
		// offline. The account's name is cert_banner's to say.
		// Precedence: authRequired > changed > problem > sending.
		{"refused certificate", st(tlsState("a1", api.SyncOffline, api.TLSUntrusted), api.SyncState{AccountID: "a2", Status: api.SyncIdle}), accounts, "Certificate problem", false},
		{"changed certificate", st(tlsState("a2", api.SyncOffline, api.TLSPinMismatch)), accounts, "Certificate changed", false},
		{"changed beats refused", st(tlsState("a1", api.SyncOffline, api.TLSExpired), tlsState("a2", api.SyncOffline, api.TLSPinMismatch)), accounts, "Certificate changed", false},
		{"authRequired beats a certificate", st(tlsState("a1", api.SyncOffline, api.TLSUntrusted), api.SyncState{AccountID: "a2", Status: api.SyncAuthRequired}), accounts, "Sign-in required: home@example.invalid", false},
		{"certificate beats sending", st(tlsState("a1", api.SyncOffline, api.TLSUntrusted), api.SyncState{AccountID: "a2", Status: api.SyncIdle, PendingOutbox: 1}), accounts, "Certificate problem", false},
		{"certificate beats error", st(tlsState("a1", api.SyncOffline, api.TLSUntrusted), api.SyncState{AccountID: "a2", Status: api.SyncError}), accounts, "Certificate problem", false},
		{"single account certificate", st(tlsState("a1", api.SyncError, api.TLSExpired)), single, "Certificate problem", false},
		{"handshake stays offline", st(tlsState("a1", api.SyncOffline, api.TLSHandshake)), accounts, "Offline: Work", false},
		{"tlsError without details stays offline", st(api.SyncState{AccountID: "a1", Status: api.SyncOffline, Error: api.NewError(api.CodeTLSError, "x")}), accounts, "Offline: Work", false},
		{"disabled account certificate is ignored", st(tlsState("a3", api.SyncOffline, api.TLSUntrusted)), accounts, "Up to date", false},
	}
	for _, c := range cases {
		text, spinning := syncStatusText(c.states, c.accounts, folderName, now)
		if text != c.want || spinning != c.spinning {
			t.Errorf("%s: got %q/%v, want %q/%v", c.name, text, spinning, c.want, c.spinning)
		}
	}

	// A nil folderName must not crash and falls back to the account name.
	if text, _ := syncStatusText(st(api.SyncState{AccountID: "a1", Status: api.SyncSyncing, FolderID: "f_inbox", Progress: -1}), accounts, nil, now); text != "Syncing Work…" {
		t.Errorf("nil folderName: got %q", text)
	}
}

// tlsState is an account state after a tlsError with the given reason, as
// the client decodes it (data is a map).
func tlsState(id api.AccountID, status api.SyncStatus, reason api.TLSErrorReason) api.SyncState {
	return api.SyncState{AccountID: id, Status: status, Progress: -1,
		Error: &api.Error{Code: api.CodeTLSError, Message: "tls", Data: map[string]any{"reason": string(reason)}}}
}

func TestCertBanner(t *testing.T) {
	accounts := []api.Account{
		{ID: "a1", Config: api.AccountConfig{Name: "Paused"}, Enabled: false},
		{ID: "a2", Config: api.AccountConfig{Name: "Work"}, Enabled: true},
		{ID: "a3", Config: api.AccountConfig{Email: "bridge@example.invalid"}, Enabled: true},
	}
	states := map[api.AccountID]api.SyncState{
		"a1": tlsState("a1", api.SyncOffline, api.TLSUntrusted),
		"a2": tlsState("a2", api.SyncOffline, api.TLSHandshake),
		"a3": tlsState("a3", api.SyncOffline, api.TLSPinMismatch),
	}
	a, p, ok := certProblemAccount(states, accounts)
	if !ok || a.ID != "a3" || p.Category() != certtrust.Changed {
		t.Fatalf("got %s %+v %v", a.ID, p, ok)
	}
	if got := certBannerText(p.Category(), accountRowTitle(a)); got != "The certificate of bridge@example.invalid has changed" {
		t.Errorf("changed: %q", got)
	}
	if got := certBannerText(certtrust.Certificate, "Work"); got != "The certificate of Work is not trusted" {
		t.Errorf("refused: %q", got)
	}
	// The first account in order wins; an uncached one uses account.list.
	states["a2"] = tlsState("a2", api.SyncError, api.TLSExpired)
	if a, _, ok := certProblemAccount(states, accounts); !ok || a.ID != "a2" {
		t.Errorf("order: %s %v", a.ID, ok)
	}
	delete(states, "a2")
	accounts[1].State = tlsState("a2", api.SyncOffline, api.TLSUntrusted)
	if a, _, ok := certProblemAccount(states, accounts); !ok || a.ID != "a2" {
		t.Errorf("uncached: %s %v", a.ID, ok)
	}
	// Connected again: no banner.
	states["a2"] = api.SyncState{AccountID: "a2", Status: api.SyncSyncing, Error: accounts[1].State.Error}
	states["a3"] = api.SyncState{AccountID: "a3", Status: api.SyncIdle}
	if a, _, ok := certProblemAccount(states, accounts); ok {
		t.Errorf("reconnected: %s", a.ID)
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

func TestAuthBannerTitleByKind(t *testing.T) {
	for _, c := range []struct {
		kind   signin.Kind
		reason api.ErrorCode
		title  string
		button string
	}{
		{signin.Password, api.CodeAuthFailed, "Sign in to Work again", "Open Preferences"},
		{signin.GOA, api.CodeAuthRequired, "Sign in to Work again in Settings → Online Accounts", "Open Online Accounts"},
		{signin.GOA, api.CodeUnavailable, "GNOME Online Accounts is not available; Work cannot sign in", "Open Online Accounts"},
		{signin.OAuth, api.CodeAuthRequired, "Sign in to Work again in your browser", "Sign In"},
		{signin.OAuth, api.CodeAuthFailed, "Sign in to Work again in your browser", "Sign In"},
		{signin.OAuth, api.CodeNetworkError, "Sign in to Work again in your browser", "Sign In"},
		{signin.OAuth, api.CodeKeyringError, "The system keyring is unavailable; Work cannot sign in", "Sign In"},
	} {
		if got := authBannerTitle(c.kind, c.reason, "Work"); got != c.title {
			t.Errorf("authBannerTitle(%d, %d) = %q, want %q", c.kind, c.reason, got, c.title)
		}
		if got := authBannerButton(c.kind); got != c.button {
			t.Errorf("authBannerButton(%d) = %q, want %q", c.kind, got, c.button)
		}
	}
}
