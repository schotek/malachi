// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestTouchCollectedAddresses(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	day := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	// One send: To with a name, the same address again in Cc (counts once),
	// a Bcc without a name, and junk that must be skipped, not fail.
	err := s.TouchCollectedAddresses(ctx, []api.Address{
		{Name: "Alice Example", Address: "Alice@Example.org"},
		{Name: "Alice again", Address: "alice@example.org"},
		{Address: "bob@example.org"},
		{Name: "Broken", Address: "not an address"},
		{Name: "Ctrl\x00Name", Address: "ctrl@example.org"},
		{Name: strings.Repeat("x", maxCollectedNameBytes+1), Address: "long@example.org"},
		{Address: ""},
	}, day)
	if err != nil {
		t.Fatal(err)
	}
	got := search(t, s, "example.org", 10)
	if len(got) != 4 {
		t.Fatalf("collected %d rows: %+v", len(got), got)
	}
	byAddr := map[string]CollectedAddress{}
	for _, c := range got {
		byAddr[c.Address] = c
	}
	if a := byAddr["alice@example.org"]; a.Name != "Alice Example" || a.Uses != 1 || !a.FirstUsed.Equal(day) || !a.LastUsed.Equal(day) {
		t.Errorf("alice = %+v", a)
	}
	if b := byAddr["bob@example.org"]; b.Name != "" || b.Uses != 1 {
		t.Errorf("bob = %+v", b)
	}
	if c := byAddr["ctrl@example.org"]; c.Name != "" {
		t.Errorf("control characters kept: %q", c.Name)
	}
	if l := byAddr["long@example.org"]; l.Name != "" {
		t.Errorf("overlong name kept: %d bytes", len(l.Name))
	}

	// A later send without a name keeps the name and bumps the count; an
	// earlier one (the backfill) with another name loses to the newer
	// name but still moves first_used back.
	if err := s.TouchCollectedAddresses(ctx, []api.Address{{Address: "alice@example.org"}}, day.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchCollectedAddresses(ctx, []api.Address{{Name: "A. Example", Address: "alice@example.org"}}, day.Add(-72*time.Hour)); err != nil {
		t.Fatal(err)
	}
	a := search(t, s, "alice", 10)[0]
	if a.Name != "Alice Example" || a.Uses != 3 || !a.FirstUsed.Equal(day.Add(-72*time.Hour)) || !a.LastUsed.Equal(day.Add(48*time.Hour)) {
		t.Errorf("after out-of-order touches: %+v", a)
	}
	// A newer name replaces the stored one.
	if err := s.TouchCollectedAddresses(ctx, []api.Address{{Name: "Alice Renamed", Address: "alice@example.org"}}, day.Add(96*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if a := search(t, s, "alice", 10)[0]; a.Name != "Alice Renamed" || a.Uses != 4 {
		t.Errorf("after rename: %+v", a)
	}
	if err := s.TouchCollectedAddresses(ctx, nil, day); err != nil {
		t.Errorf("empty touch: %v", err)
	}
}

func TestSearchCollectedAddresses(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	seed := []struct {
		name, address string
		at            time.Time
	}{
		{"Jan Novák", "jan@example.cz", day},
		{"Alice Example", "alice@example.org", day.Add(time.Hour)},
		{"", "carol@example.net", day.Add(2 * time.Hour)},
		{"100% Sure", "sure@example.org", day.Add(3 * time.Hour)},
		{"under_score", "u_s@example.org", day.Add(4 * time.Hour)},
	}
	for _, r := range seed {
		if err := s.TouchCollectedAddresses(ctx, []api.Address{{Name: r.name, Address: r.address}}, r.at); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		q    string
		want []string // addresses, in order
	}{
		{"ali", []string{"alice@example.org"}},
		{"nov", []string{"jan@example.cz"}},
		{"NOVÁK", []string{"jan@example.cz"}}, // folded beyond ASCII
		{"carol", []string{"carol@example.net"}},
		{"example.org", []string{"u_s@example.org", "sure@example.org", "alice@example.org"}}, // newest first
		{"100%", []string{"sure@example.org"}},                                                // % is literal
		{"r_s", []string{"u_s@example.org"}},                                                  // _ is literal
		{"%", []string{"sure@example.org"}},                                                   // a lone % too
		{"   ", nil},
		{"nobody", nil},
	}
	for _, c := range cases {
		got := search(t, s, c.q, 10)
		var addrs []string
		for _, g := range got {
			addrs = append(addrs, g.Address)
		}
		if strings.Join(addrs, ",") != strings.Join(c.want, ",") {
			t.Errorf("search %q = %v, want %v", c.q, addrs, c.want)
		}
	}
	if got := search(t, s, "example", 2); len(got) != 2 {
		t.Errorf("limit: %d rows", len(got))
	}
}

func search(t *testing.T, s *Store, q string, limit int) []CollectedAddress {
	t.Helper()
	got, err := s.SearchCollectedAddresses(context.Background(), q, limit)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
