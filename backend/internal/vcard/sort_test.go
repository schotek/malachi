// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package vcard

import (
	"slices"
	"testing"
)

func TestSortKey(t *testing.T) {
	cases := []struct {
		c       *Card
		key, sc string
	}{
		{&Card{Name: Name{Family: "Šťastná", Given: "Žofie"}}, "stastna zofie", "S"},
		{&Card{Name: Name{Given: "Madonna"}}, "madonna", "M"},
		{&Card{Org: "Ørsted A/S", DisplayName: "x"}, "orsted a/s", "O"},
		{&Card{DisplayName: "  \"Quoted\"  Name "}, "quoted\" name", "Q"},
		{&Card{Emails: []Email{{"", "Straße@x.example"}}}, "strasse@x.example", "S"},
		{&Card{Nickname: "Łukasz"}, "lukasz", "L"},
		{&Card{DisplayName: "123 Corp"}, "123 corp", "#"},
		{&Card{DisplayName: "Иван"}, "иван", "И"},
		{&Card{}, "", "#"},
		{nil, "", "#"},
	}
	for _, tc := range cases {
		k := SortKey(tc.c)
		if k != tc.key || Section(k) != tc.sc {
			t.Errorf("SortKey(%+v) = %q %q, want %q %q", tc.c, k, Section(k), tc.key, tc.sc)
		}
	}
	keys := []string{
		SortKey(&Card{Name: Name{Family: "Čech"}}),
		SortKey(&Card{Name: Name{Family: "Cejnar"}}),
		SortKey(&Card{Name: Name{Family: "Dvořák"}}),
		SortKey(&Card{Name: Name{Family: "Smith", Given: "John"}}),
		SortKey(&Card{Name: Name{Family: "Smithson", Given: "Al"}}),
	}
	if !slices.IsSorted(keys) {
		t.Errorf("order: %q", keys)
	}
}

func TestDisplayNameOf(t *testing.T) {
	cases := []struct {
		c    *Card
		want string
	}{
		{&Card{DisplayName: "FN", Org: "O"}, "FN"},
		{&Card{Name: Name{Prefix: "Dr.", Given: "A", Middle: "B", Family: "C", Suffix: "Jr."}}, "Dr. A B C Jr."},
		{&Card{Nickname: "Nick", Org: "O"}, "Nick"},
		{&Card{Org: "Org"}, "Org"},
		{&Card{Emails: []Email{{"", " "}, {"", "e@x.example"}}}, "e@x.example"},
		{&Card{DisplayName: "\u202e\u200b"}, ""},
		{nil, ""},
	}
	for _, tc := range cases {
		if got := DisplayNameOf(tc.c); got != tc.want {
			t.Errorf("DisplayNameOf(%+v) = %q, want %q", tc.c, got, tc.want)
		}
	}
}
