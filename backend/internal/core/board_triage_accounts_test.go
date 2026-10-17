// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"testing"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// A calendar-only CalDAV account has no mail and no cases: triage never
// reads it, whether the preferences name no account (every enabled mail
// account) or name it (docs/calendar.md D16, docs/api.md §4.13).
func TestBoardTriageAccountsSkipCalendarOnly(t *testing.T) {
	x := newBoardBox(t)
	cal := store.Account{Name: "Calendar", Enabled: true, Config: api.AccountConfig{
		Name: "Calendar", Email: "cal@example.invalid", Kind: api.AccountCalDAV,
		CalDAV:   &api.CalDAVConfig{ServerURL: "https://dav.example.invalid/", Username: "me"},
		Calendar: &api.CalendarConfig{Sync: true},
	}}
	if err := x.b.store.AddAccount(x.ctx, &cal); err != nil {
		t.Fatal(err)
	}
	prefs, err := x.b.boardPrefs(x.ctx)
	if err != nil {
		t.Fatal(err)
	}
	only := func(name string, p api.BoardPreferences, want ...string) {
		t.Helper()
		accs, err := x.b.triageAccounts(x.ctx, p)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := sortedAccountIDs(accs)
		if len(got) != len(want) {
			t.Fatalf("%s: triage reads %v, want %v", name, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%s: triage reads %v, want %v", name, got, want)
			}
		}
	}
	// Nothing named: every enabled mail account, not the calendar.
	only("default", prefs, x.acc)
	// Named along a mail account: still left out.
	prefs.TriageAccounts = []api.AccountID{api.AccountID(x.acc), api.AccountID(cal.ID)}
	only("named", prefs, x.acc)
	// Named alone: triage reads nothing.
	prefs.TriageAccounts = []api.AccountID{api.AccountID(cal.ID)}
	only("named alone", prefs)
	// The preferences keep the id (the account exists), as any named one.
	set, err := x.svc.SetPreferences(x.ctx, api.BoardSetPreferencesParams{Preferences: prefs})
	if err != nil || len(set.Preferences.TriageAccounts) != 1 || set.Preferences.TriageAccounts[0] != api.AccountID(cal.ID) {
		t.Fatalf("set: %+v %v", set, err)
	}
}
