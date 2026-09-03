// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package compose

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestParseAddressList(t *testing.T) {
	addrs, invalid := ParseAddressList(`Alice <alice@example.invalid>, bob@example.invalid; "Doe, Jane" <jane@example.invalid>, Jörg Müller <j@example.invalid>,`)
	if len(invalid) != 0 {
		t.Fatalf("invalid = %q", invalid)
	}
	want := []api.Address{
		{Name: "Alice", Address: "alice@example.invalid"},
		{Address: "bob@example.invalid"},
		{Name: "Doe, Jane", Address: "jane@example.invalid"},
		{Name: "Jörg Müller", Address: "j@example.invalid"},
	}
	if len(addrs) != len(want) {
		t.Fatalf("got %+v", addrs)
	}
	for i := range want {
		if addrs[i] != want[i] {
			t.Errorf("%d: got %+v, want %+v", i, addrs[i], want[i])
		}
	}

	addrs, invalid = ParseAddressList("foo, a@b c@d, ok@example.invalid, ,")
	if len(addrs) != 1 || addrs[0].Address != "ok@example.invalid" {
		t.Errorf("valid part = %+v", addrs)
	}
	if len(invalid) != 2 || invalid[0] != "foo" || invalid[1] != "a@b c@d" {
		t.Errorf("invalid = %q", invalid)
	}
	if a, inv := ParseAddressList(""); len(a) != 0 || len(inv) != 0 {
		t.Errorf("empty: %+v %q", a, inv)
	}
}

func TestFormatAddressList(t *testing.T) {
	in := []api.Address{
		{Name: "Alice", Address: "alice@example.invalid"},
		{Address: "bob@example.invalid"},
		{Name: `Doe, Jane "JD"`, Address: "jane@example.invalid"},
	}
	got := FormatAddressList(in)
	want := `Alice <alice@example.invalid>, bob@example.invalid, "Doe, Jane \"JD\"" <jane@example.invalid>`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	// Round trip.
	back, invalid := ParseAddressList(got)
	if len(invalid) != 0 || len(back) != 3 || back[2].Name != `Doe, Jane "JD"` {
		t.Errorf("round trip: %+v %q", back, invalid)
	}
}
