// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestBlockTitleAndPlace(t *testing.T) {
	cases := []struct{ title, wantTitle, place, wantPlace string }{
		{"Standup", "Standup", "Room B", "Room B"},
		{"", "Untitled Event", "", ""},
		{" ‮​evil\nline ", "evil line", "a\x00b⁦", "ab"},
	}
	for _, c := range cases {
		o := api.Occurrence{Title: c.title, Location: c.place}
		if got := BlockTitle(o, en{}); got != c.wantTitle {
			t.Errorf("BlockTitle(%q) = %q, want %q", c.title, got, c.wantTitle)
		}
		if got := BlockPlace(o); got != c.wantPlace {
			t.Errorf("BlockPlace(%q) = %q, want %q", c.place, got, c.wantPlace)
		}
	}
	long := api.Occurrence{Title: strings.Repeat("x", 4*api.MaxEventTitleBytes)}
	if got := BlockTitle(long, en{}); len(got) > api.MaxEventTitleBytes {
		t.Errorf("BlockTitle of a long title is %d bytes", len(got))
	}
}
