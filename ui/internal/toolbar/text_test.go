// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package toolbar

import (
	"strings"
	"testing"
)

type msgids struct{}

func (msgids) T(m string) string           { return m }
func (msgids) N(m, p string, n int) string { return map[bool]string{true: m, false: p}[n == 1] }
func (msgids) C(ctx, m string) string      { return m }

func TestEveryItemHasALabel(t *testing.T) {
	tr := msgids{}
	for _, bar := range Bars {
		for _, it := range Catalog(bar) {
			l := ItemLabel(tr, bar, it.ID)
			if l == "" || l == string(it.ID) || strings.Contains(l, "_") {
				t.Errorf("%s %s: label %q", bar, it.ID, l)
			}
		}
		for _, s := range Sections(bar) {
			if l := SectionTitle(tr, bar, s); l == "" || l == string(s) {
				t.Errorf("%s %s: section title %q", bar, s, l)
			}
		}
		if l := BarTitle(tr, bar); l == "" || l == string(bar) {
			t.Errorf("%s: bar title %q", bar, l)
		}
	}
	for _, id := range []ID{Space, FlexibleSpace} {
		if l := ItemLabel(tr, BarMail, id); l == string(id) {
			t.Errorf("%s has no label", id)
		}
	}
	if got := ItemLabel(tr, BarMail, "bogus"); got != "bogus" {
		t.Errorf("unknown id label = %q", got)
	}
}

func TestTexts(t *testing.T) {
	tr := msgids{}
	for _, m := range DisplayModes {
		if DisplayModeLabel(tr, m) == "" {
			t.Errorf("%s has no label", m)
		}
	}
	if DisplayModeLabel(tr, DisplayIcon) == DisplayModeLabel(tr, DisplayLabel) {
		t.Errorf("display modes share a label")
	}
	if got := RestoreBody(tr, BarBoardList); got != "Your changes to the “Board – List” toolbar will be lost." {
		t.Errorf("RestoreBody = %q", got)
	}
	for _, s := range []string{GroupTitle(tr), CustomizeTitle(tr), DialogTitle(tr), DisplayModeTitle(tr),
		RestoreTitle(tr), RestoreQuestion(tr), RestoreCancel(tr), AddItemTitle(tr), RemoveTitle(tr), UnavailableSubtitle(tr)} {
		if s == "" {
			t.Errorf("an empty text")
		}
	}
}

func TestStripMnemonic(t *testing.T) {
	for in, want := range map[string]string{
		"Mark as _Read": "Mark as Read", "Load _Images": "Load Images", "a__b": "a_b", "_": "", "plain": "plain", "Před_vo_lby": "Předvolby",
	} {
		if got := StripMnemonic(in); got != want {
			t.Errorf("StripMnemonic(%q) = %q, want %q", in, got, want)
		}
	}
}
