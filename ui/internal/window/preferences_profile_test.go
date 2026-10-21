// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestProfileNotePreview(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "No Note",
		"  \n ":                  "No Note",
		"Podepisuj\njako studio": "Podepisuj jako studio",
		strings.Repeat("ž", 40):  strings.Repeat("ž", 40),
		strings.Repeat("ž", 41):  strings.Repeat("ž", 40) + "…",
	} {
		if got := profileNotePreview(in, "No Note"); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestProfileNeedsSave(t *testing.T) {
	if profileNeedsSave("Píšu stručně.  \n", "Píšu stručně.") {
		t.Error("trailing space alone saved")
	}
	if !profileNeedsSave("Píšu stručně!", "Píšu stručně.") || !profileNeedsSave("", "x") {
		t.Error("a change not saved")
	}
}

func TestCapRunes(t *testing.T) {
	if s, ok := capRunes("žluť", 4); !ok || s != "žluť" {
		t.Errorf("%q %v", s, ok)
	}
	if s, ok := capRunes("žlutý", 4); ok || s != "žlut" {
		t.Errorf("%q %v", s, ok)
	}
}

func TestProfileNoteKind(t *testing.T) {
	for kind, want := range map[api.AccountKind]bool{"": true, api.AccountIMAP: true, api.AccountGraph: true, api.AccountJira: true, api.AccountEway: false, api.AccountCalDAV: false} {
		if got := profileNoteKind(api.Account{Config: api.AccountConfig{Kind: kind}}); got != want {
			t.Errorf("%q: %v", kind, got)
		}
	}
}
