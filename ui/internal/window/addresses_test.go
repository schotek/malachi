// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"fmt"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// people returns n addresses a0@example.invalid … with names.
func people(n int) []api.Address {
	out := make([]api.Address, n)
	for i := range out {
		out[i] = api.Address{Name: fmt.Sprintf("Person %d", i), Address: fmt.Sprintf("a%d@example.invalid", i)}
	}
	return out
}

func TestFoldAddresses(t *testing.T) {
	tests := []struct {
		name      string
		list      []api.Address
		expanded  bool
		wantShown int
		wantMore  int
	}{
		{"none", nil, false, 0, 0},
		{"few", people(3), false, 3, 0},
		{"exactly the fold", people(addressChipsFolded), false, addressChipsFolded, 0},
		// "+1 more" would take the room of the chip it hides.
		{"one over", people(addressChipsFolded + 1), false, addressChipsFolded + 1, 0},
		{"two over", people(addressChipsFolded + 2), false, addressChipsFolded, 2},
		{"many", people(17), false, addressChipsFolded, 17 - addressChipsFolded},
		{"many, unfolded", people(17), true, 17, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			shown, more := foldAddresses(tc.list, tc.expanded)
			if len(shown) != tc.wantShown || more != tc.wantMore {
				t.Errorf("got %d shown, %d more; want %d, %d", len(shown), more, tc.wantShown, tc.wantMore)
			}
			for i, a := range shown {
				if a != tc.list[i] {
					t.Errorf("shown[%d] = %+v, want the list's order", i, a)
				}
			}
		})
	}
}

func TestFoldAddressesSkipsBlankEntries(t *testing.T) {
	// A group with no members, or an entry that parsed to nothing, has
	// nothing to put on a chip; a name alone or an address alone does.
	list := []api.Address{{}, {Name: "  "}, {Name: "Only a name"}, {Address: "only@example.invalid"}}
	shown, more := foldAddresses(list, false)
	if len(shown) != 2 || more != 0 || shown[0].Name != "Only a name" || shown[1].Address != "only@example.invalid" {
		t.Errorf("got %+v, %d more", shown, more)
	}
	if shown, _ := foldAddresses([]api.Address{{}, {}}, false); len(shown) != 0 {
		t.Errorf("blank list: got %+v", shown)
	}
}

func TestAddressKey(t *testing.T) {
	base := addressKey("acc", people(2), 0)
	if addressKey("acc", people(2), 0) != base {
		t.Error("the same row gave two keys")
	}
	renamed := people(2)
	renamed[1].Name = "Someone else"
	for name, k := range map[string]string{
		"account": addressKey("other", people(2), 0),
		"name":    addressKey("acc", renamed, 0),
		"fold":    addressKey("acc", people(2), 3),
		"count":   addressKey("acc", people(3), 0),
		// Name and address must not run together: "a" + "bc" is not "ab" + "c".
		"boundary": addressKey("acc", []api.Address{{Name: "Person 0a", Address: "0@example.invalid"}, people(2)[1]}, 0),
	} {
		if k == base {
			t.Errorf("a different %s gave the same key", name)
		}
	}
}
