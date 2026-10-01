// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package conversation

import (
	"reflect"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func card(id string, sent bool) Item {
	return Item{Kind: ItemMessage, Message: api.MessageSummary{ID: api.MessageID(id), Date: at(len(id))}, Sent: sent}
}

func TestDefaultFolds(t *testing.T) {
	event := Item{Kind: ItemEvent, Message: api.MessageSummary{ID: "e"}}
	more := Item{Kind: ItemTruncated, Text: "2 earlier messages are not shown"}
	cases := []struct {
		name    string
		items   []Item
		opening api.MessageID
		want    map[api.MessageID]bool
	}{
		{"nothing", nil, "", map[api.MessageID]bool{}},
		{"one message", []Item{card("a", false)}, "a", map[api.MessageID]bool{"a": false}},
		{"opening folded while another follows", []Item{card("a", false), card("bb", false)}, "a",
			map[api.MessageID]bool{"a": true, "bb": false}},
		{"a message and its status changes", []Item{card("a", false), event, more}, "a", map[api.MessageID]bool{"a": false}},
		{"a message and the user's replies", []Item{card("a", false), card("rr", true), card("rrr", true)}, "a",
			map[api.MessageID]bool{"a": false, "rr": true, "rrr": true}},
		{"replies among messages", []Item{card("a", false), card("rr", true), card("bbb", false)}, "a",
			map[api.MessageID]bool{"a": true, "rr": true, "bbb": false}},
		{"the user's reply opened it", []Item{card("r", true), card("aa", false)}, "r",
			map[api.MessageID]bool{"r": true, "aa": false}},
		{"no opening", []Item{more, card("a", false), card("bb", false)}, "",
			map[api.MessageID]bool{"a": false, "bb": false}},
		// Pathological: every card a reply. The newest opens.
		{"all sent", []Item{card("r", true), card("rrr", true), card("rr", true)}, "r",
			map[api.MessageID]bool{"r": true, "rr": true, "rrr": false}},
		{"a repeated id keeps its first state", []Item{card("a", false), card("bb", true), card("bb", false)}, "a",
			map[api.MessageID]bool{"a": false, "bb": true}},
		{"no id", []Item{{Kind: ItemMessage}, card("a", false)}, "", map[api.MessageID]bool{"a": false}},
	}
	for _, c := range cases {
		if got := DefaultFolds(c.items, c.opening); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestFolds(t *testing.T) {
	items := []Item{card("a", false), card("rr", true), card("bbb", false)}
	var f Folds
	f.Show("t1")
	if got := f.State(items, "a"); !reflect.DeepEqual(got, map[api.MessageID]bool{"a": true, "rr": true, "bbb": false}) {
		t.Fatalf("defaults: %v", got)
	}
	f.Set("a", false)
	f.Set("bbb", true)
	f.Set("", true)
	want := map[api.MessageID]bool{"a": false, "rr": true, "bbb": true}
	if got := f.State(items, "a"); !reflect.DeepEqual(got, want) {
		t.Errorf("choices: %v", got)
	}
	// An update of the same conversation keeps the choices; a card that
	// arrives starts as its default.
	f.Show("t1")
	more := append(append([]Item(nil), items...), card("cccc", false))
	want["cccc"] = false
	if got := f.State(more, "a"); !reflect.DeepEqual(got, want) {
		t.Errorf("after an arrival: %v", got)
	}
	// Collapse All, then a card arrives: it starts open, and the button
	// offers Collapse All again.
	f.SetAll(items, FoldAllCollapse.Folded())
	if got := FoldAllOffer(f.State(items, "a")); got != FoldAllExpand {
		t.Errorf("after Collapse All: %v", got)
	}
	if got := FoldAllOffer(f.State(more, "a")); got != FoldAllCollapse {
		t.Errorf("after an arrival: %v", got)
	}
	f.SetAll(more, FoldAllExpand.Folded())
	if got := f.State(more, "a"); !reflect.DeepEqual(got, map[api.MessageID]bool{"a": false, "rr": false, "bbb": false, "cccc": false}) {
		t.Errorf("after Expand All: %v", got)
	}
	// Another conversation forgets them.
	f.Show("t2")
	if got := f.State(items, "a"); got["a"] != true || got["rr"] != true {
		t.Errorf("another conversation: %v", got)
	}
}

func TestFoldAllOffer(t *testing.T) {
	cases := []struct {
		state map[api.MessageID]bool
		want  FoldAll
		label string
	}{
		{nil, FoldAllNone, ""},
		{map[api.MessageID]bool{"a": false}, FoldAllNone, ""},
		{map[api.MessageID]bool{"a": true, "b": false}, FoldAllCollapse, "Collapse All"},
		{map[api.MessageID]bool{"a": false, "b": false}, FoldAllCollapse, "Collapse All"},
		{map[api.MessageID]bool{"a": true, "b": true}, FoldAllExpand, "Expand All"},
	}
	for _, c := range cases {
		got := FoldAllOffer(c.state)
		if got != c.want || got.Label(tr) != c.label {
			t.Errorf("%v: %v %q, want %v %q", c.state, got, got.Label(tr), c.want, c.label)
		}
	}
	if !FoldAllCollapse.Folded() || FoldAllExpand.Folded() || FoldAllNone.Folded() {
		t.Error("Folded")
	}
}
