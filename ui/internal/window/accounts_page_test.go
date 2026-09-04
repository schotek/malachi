// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestAccountRowTitle(t *testing.T) {
	a := api.Account{Config: api.AccountConfig{Name: "Work", Email: "me@example.invalid"}}
	if got := accountRowTitle(a); got != "Work" {
		t.Errorf("named: %q", got)
	}
	a.Config.Name = ""
	if got := accountRowTitle(a); got != "me@example.invalid" {
		t.Errorf("unnamed: %q", got)
	}
}

func TestAccountStatusText(t *testing.T) {
	cases := map[api.SyncStatus]string{
		api.SyncIdle:         "",
		api.SyncDisabled:     "Paused",
		api.SyncSyncing:      "Syncing…",
		api.SyncOffline:      "Offline",
		api.SyncAuthRequired: "Sign-in required",
		api.SyncError:        "Error",
		"unknown":            "",
	}
	for status, want := range cases {
		if got := accountStatusText(status); got != want {
			t.Errorf("%s: got %q, want %q", status, got, want)
		}
	}
}

func TestInsertIndex(t *testing.T) {
	// A list a b c d: the index the dragged row ends up at, once it is taken
	// out of the list. from == result means "no change".
	cases := []struct {
		name        string
		from, tgt   int
		above, want int
	}{
		{"onto itself, upper half", 1, 1, 1, 1},
		{"onto itself, lower half", 1, 1, 0, 1},
		{"onto the row above, upper half", 2, 1, 1, 1},
		// Below the row above is where it already is: a no-op (want == from).
		{"onto the row above, lower half", 2, 1, 0, 2},
		{"onto the row below, upper half", 1, 2, 1, 1},
		{"onto the row below, lower half", 1, 2, 0, 2},
		{"first onto last, lower half", 0, 3, 0, 3},
		{"last onto first, upper half", 3, 0, 1, 0},
		{"down two rows", 0, 2, 0, 2},
		{"up two rows", 3, 1, 1, 1},
	}
	for _, tc := range cases {
		if got := insertIndex(tc.from, tc.tgt, tc.above == 1); got != tc.want {
			t.Errorf("%s: insertIndex(%d, %d, %v) = %d, want %d",
				tc.name, tc.from, tc.tgt, tc.above == 1, got, tc.want)
		}
	}
}

func TestMoveAccount(t *testing.T) {
	list := func(names ...string) []api.Account {
		out := make([]api.Account, 0, len(names))
		for _, n := range names {
			out = append(out, api.Account{ID: api.AccountID(n)})
		}
		return out
	}
	ids := func(accounts []api.Account) string {
		s := ""
		for _, a := range accounts {
			s += string(a.ID)
		}
		return s
	}
	cases := []struct {
		name     string
		from, to int
		want     string
	}{
		{"down one", 0, 1, "bacd"},
		{"down to the end", 0, 3, "bcda"},
		{"up one", 3, 2, "abdc"},
		{"up to the head", 3, 0, "dabc"},
		{"middle to middle", 1, 2, "acbd"},
		{"no move", 2, 2, "abcd"},
		{"out of range low", 0, -1, "abcd"},
		{"out of range high", 0, 4, "abcd"},
	}
	for _, tc := range cases {
		in := list("a", "b", "c", "d")
		got := moveAccount(in, tc.from, tc.to)
		if ids(got) != tc.want {
			t.Errorf("%s: moveAccount(%d, %d) = %s, want %s", tc.name, tc.from, tc.to, ids(got), tc.want)
		}
		if ids(in) != "abcd" {
			t.Errorf("%s: the input was modified: %s", tc.name, ids(in))
		}
	}
	if got := moveAccount(list("a"), 0, 0); len(got) != 1 || got[0].ID != "a" {
		t.Errorf("single element: %v", got)
	}
	if got := moveAccount(nil, 0, 1); len(got) != 0 {
		t.Errorf("empty: %v", got)
	}
}
