// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package toolbar

import (
	"reflect"
	"testing"
)

func shownOf(l Layout) []Shown {
	out := make([]Shown, len(l))
	for i, id := range l {
		out[i] = Shown{ID: id}
	}
	return out
}

func TestFitEverythingFits(t *testing.T) {
	sh := shownOf(ids("reply replyAll forward flexibleSpace archive trash"))
	w := []int{30, 30, 30, 0, 30, 30}
	got := Fit(GTK, BarMail, sh, w, 150)
	if !reflect.DeepEqual(got.Visible, []bool{true, true, true, true, true, true}) || got.Overflow != nil {
		t.Errorf("Fit = %+v", got)
	}
}

func TestFitLowestPriorityRightmostFirst(t *testing.T) {
	// moreActions is high, the others standard, junk and undo low.
	sh := shownOf(ids("reply junk forward moreActions undo"))
	w := []int{30, 30, 30, 30, 30}
	got := Fit(Variant{}, BarMail, sh, w, 100)
	// 150 > 100: undo (low, rightmost) goes, then junk; 90 fits.
	if want := []bool{true, false, true, true, false}; !reflect.DeepEqual(got.Visible, want) {
		t.Errorf("Visible = %v, want %v", got.Visible, want)
	}
	if want := []ID{Junk, Undo}; !reflect.DeepEqual(got.Overflow, want) {
		t.Errorf("Overflow = %v, want %v", got.Overflow, want)
	}
	// Narrower: of the standard ones the rightmost goes first.
	got = Fit(Variant{}, BarMail, sh, w, 40)
	if want := []bool{false, false, false, true, false}; !reflect.DeepEqual(got.Visible, want) {
		t.Errorf("Visible = %v, want %v", got.Visible, want)
	}
	if want := []ID{Reply, Junk, Forward, Undo}; !reflect.DeepEqual(got.Overflow, want) {
		t.Errorf("Overflow = %v (layout order), want %v", got.Overflow, want)
	}
}

func TestFitNeverDropsFixedSpacesOrSeparators(t *testing.T) {
	sh := shownOf(ids("mode space |list refresh flexibleSpace"))
	w := []int{80, 12, 1, 30, 0}
	got := Fit(Variant{}, BarMail, sh, w, 0)
	if want := []bool{true, true, true, false, true}; !reflect.DeepEqual(got.Visible, want) {
		t.Errorf("Visible = %v, want %v", got.Visible, want)
	}
	if want := []ID{Refresh}; !reflect.DeepEqual(got.Overflow, want) {
		t.Errorf("Overflow = %v", got.Overflow)
	}
}

func TestFitHiddenTakesNoRoom(t *testing.T) {
	sh := []Shown{{ID: Reply}, {ID: CRM, Hidden: true}, {ID: Star}}
	got := Fit(Variant{}, BarMail, sh, []int{30, 500, 30}, 60)
	if want := []bool{true, false, true}; !reflect.DeepEqual(got.Visible, want) || got.Overflow != nil {
		t.Errorf("Fit = %+v", got)
	}
}

func TestFitPathological(t *testing.T) {
	// Missing and negative widths count as nothing; unknown ids are
	// standard; nothing at all is fine.
	sh := shownOf(ids("reply bogus forward"))
	got := Fit(Variant{}, BarMail, sh, []int{-50, 40}, 30)
	if want := []bool{true, false, true}; !reflect.DeepEqual(got.Visible, want) {
		t.Errorf("Visible = %v", got.Visible)
	}
	if got := Fit(GTK, BarMail, nil, nil, -1); len(got.Visible) != 0 || got.Overflow != nil {
		t.Errorf("empty Fit = %+v", got)
	}
	// An omitted item (on GTK) counts as standard if a client passes one.
	got = Fit(GTK, BarMail, shownOf(ids("filter refresh")), []int{30, 30}, 30)
	if !reflect.DeepEqual(got.Overflow, []ID{Refresh}) {
		t.Errorf("Overflow = %v", got.Overflow)
	}
}
