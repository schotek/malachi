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
