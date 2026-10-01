// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package conversation

import "testing"

// The button under a trimmed body and what a view revealed, ported with
// the pure halves of the macOS client's QuotedTextTests.swift (labels,
// revealHoldsForTheSelection, offer).

func TestQuotedTextLabel(t *testing.T) {
	tr := recorder{}
	if got := QuotedTextLabel(false, tr); got != "Show Quoted Text" {
		t.Errorf("hidden: %q", got)
	}
	if got := QuotedTextLabel(true, tr); got != "Hide Quoted Text" {
		t.Errorf("shown: %q", got)
	}
	if got := QuotedShow.Label(tr); got != "Show Quoted Text" {
		t.Errorf("show: %q", got)
	}
	if got := QuotedHide.Label(tr); got != "Hide Quoted Text" {
		t.Errorf("hide: %q", got)
	}
	if got := QuotedNone.Label(tr); got != "" {
		t.Errorf("none: %q", got)
	}
}

func TestOfferQuoted(t *testing.T) {
	cases := []struct {
		name                   string
		shown, failed, trimmed bool
		want                   QuotedOffer
	}{
		{"nothing was cut", false, false, false, QuotedNone},
		{"trimmed", false, false, true, QuotedShow},
		{"a failed body", false, true, true, QuotedNone},
		{"the whole body on its way", true, false, false, QuotedHide},
		{"failed: the way back stays", true, true, false, QuotedHide},
	}
	for _, c := range cases {
		if got := OfferQuoted(c.shown, c.failed, c.trimmed); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestQuotedRevealHoldsForTheSelection(t *testing.T) {
	var r QuotedReveal
	r.Show("t1")
	if r.IsRevealed("a") {
		t.Error("revealed before the user asked")
	}
	r.Set("a", true)
	r.Set("b", true)
	r.Set("b", false)
	r.Set("", true)
	if !r.IsRevealed("a") || r.IsRevealed("b") || r.IsRevealed("") {
		t.Errorf("set: %v", r.revealed)
	}
	r.Show("t1")
	if !r.IsRevealed("a") {
		t.Error("an update of the same selection keeps it")
	}
	r.Show("t2")
	if r.IsRevealed("a") {
		t.Error("another selection forgets it")
	}
	r.Set("a", true)
	r.Clear()
	if r.IsRevealed("a") {
		t.Error("cleared")
	}
	r.Show("t2")
	if r.IsRevealed("a") {
		t.Error("cleared, the same selection starts anew")
	}
	// The zero value takes an empty selection as one too.
	var z QuotedReveal
	z.Set("x", true)
	z.Show("")
	if z.IsRevealed("x") {
		t.Error("the first selection starts anew")
	}
}
