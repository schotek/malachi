// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package preview

import "testing"

func TestShowFileParams(t *testing.T) {
	const uri = "file:///run/user/1000/malachi/open/x/report%20Q3.pdf"
	got := showFileParams(uri)
	// The newer signature first (Sushi 50+), then the one Sushi 46 has:
	// D-Bus matches them exactly, so both must be right.
	want := []string{"(ssbs)", "(ssb)"}
	if len(got) != len(want) {
		t.Fatalf("got %d variants, want %d", len(got), len(want))
	}
	for i, v := range got {
		if s := v.TypeString(); s != want[i] {
			t.Errorf("variant %d: type %s, want %s", i, s, want[i])
		}
		if s := v.ChildValue(0).String(); s != uri {
			t.Errorf("variant %d: uri %q, want %q", i, s, uri)
		}
		if v.ChildValue(1).String() != "" || v.ChildValue(2).Boolean() {
			t.Errorf("variant %d: want no parent handle and no close-if-shown", i)
		}
	}
}
