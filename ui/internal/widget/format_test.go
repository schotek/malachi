// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package widget

import (
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestDisplayName(t *testing.T) {
	cases := []struct {
		in   api.Address
		want string
	}{
		{api.Address{Name: "Alice Example", Address: "alice@example.invalid"}, "Alice Example"},
		{api.Address{Address: "bob@example.invalid"}, "bob@example.invalid"},
		{api.Address{Name: "   ", Address: " carol@example.invalid "}, "carol@example.invalid"},
		{api.Address{Name: "<b>bold</b>", Address: "x@y"}, "<b>bold</b>"}, // passed through, never markup
		{api.Address{}, ""},
	}
	for _, c := range cases {
		if got := DisplayName(c.in); got != c.want {
			t.Errorf("DisplayName(%+v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatAddress(t *testing.T) {
	cases := []struct {
		in   api.Address
		want string
	}{
		{api.Address{Name: "Alice", Address: "alice@example.invalid"}, "Alice <alice@example.invalid>"},
		{api.Address{Address: "bob@example.invalid"}, "bob@example.invalid"},
		{api.Address{Name: "Nameless"}, "Nameless"},
		{api.Address{}, ""},
	}
	for _, c := range cases {
		if got := FormatAddress(c.in); got != c.want {
			t.Errorf("FormatAddress(%+v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatSize(t *testing.T) {
	cases := map[int64]string{5: "5 B", 2048: "2 KiB", 3 << 20: "3.0 MiB"}
	for in, want := range cases {
		if got := FormatSize(in); got != want {
			t.Errorf("FormatSize(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatDate(t *testing.T) {
	now := time.Date(2026, time.September, 2, 15, 30, 0, 0, time.Local)
	cases := []struct {
		in   time.Time
		want string
	}{
		{time.Time{}, ""},
		{now.Add(-2 * time.Hour), "13:30"},
		{now.AddDate(0, 0, -1), "1 Sep"},
		{now.AddDate(0, -3, 0), "2 Jun"},
		{now.AddDate(-1, 0, 0), "2025-09-02"},
		{time.Date(2026, time.January, 1, 0, 0, 1, 0, time.Local), "1 Jan"},
	}
	for _, c := range cases {
		if got := FormatDate(c.in, now); got != c.want {
			t.Errorf("FormatDate(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
