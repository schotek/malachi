// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Port of MalachiCoreTests/BoardConversationCardsTests.swift. These tests
// concern identity and lifecycle, never actual mail, networking or GTK.
func cardMember(n int, text ...string) ConversationMember {
	s := fmt.Sprintf("text %d", n)
	if len(text) > 0 {
		s = text[0]
	}
	return ConversationMember{ID: api.MessageID(fmt.Sprintf("m%d", n)), Text: s}
}

func cardKey(ns ...int) ConversationKey {
	k := ConversationKey{CaseID: "case-a"}
	for _, n := range ns {
		k.Members = append(k.Members, cardMember(n))
	}
	return k
}

func expectCards(t *testing.T, cards *ConversationCards, folds []bool, bodies []ConversationBody) {
	t.Helper()
	for i, want := range folds {
		if got := cards.IsFolded(i); got != want {
			t.Errorf("fold[%d] = %v, want %v", i, got, want)
		}
	}
	for i, want := range bodies {
		if got := cards.Body(i); got != want {
			t.Errorf("body[%d] = %v, want %v", i, got, want)
		}
	}
}

func expectCardChange(t *testing.T, got ConversationChange, kept map[int]int, reload ...int) {
	t.Helper()
	if got.Kind != ConversationChangeMembers || !reflect.DeepEqual(got.Kept, kept) || !slices.Equal(got.Reload, reload) {
		t.Fatalf("change = %+v, want members kept=%v reload=%v", got, kept, reload)
	}
}

func expectCardIndices(t *testing.T, got, want []int) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("indices = %v, want %v", got, want)
	}
}

func TestConversationCardsInitialAndUnchanged(t *testing.T) {
	var cards ConversationCards
	if got := cards.Apply(cardKey(1, 2, 3)); got.Kind != ConversationChangeReset {
		t.Fatal(got)
	}
	if cards.Newest() != 2 || cards.CaseID() != "case-a" {
		t.Fatal("wrong identity/newest")
	}
	expectCards(t, &cards, []bool{true, true, false}, nil)
	if cards.Shows(0, true) != ConversationFolded || cards.Shows(2, true) != ConversationText || cards.Foldable(2) || !cards.Foldable(0) || cards.NeedsBody(0) || !cards.NeedsBody(2) {
		t.Fatal("initial presentation")
	}
	cards.Asked(2)
	cards.Answered(2, true)
	if cards.Apply(cardKey(1, 2, 3)).Kind != ConversationChangeNone || cards.Shows(2, true) != ConversationWeb {
		t.Fatal("same key touched existing cards")
	}
	k := cardKey(1, 2, 3)
	k.CaseID = "case-b"
	cards.SetFolded(0, false)
	if cards.Apply(k).Kind != ConversationChangeReset {
		t.Fatal("other case did not reset")
	}
	expectCards(t, &cards, []bool{true, true, false}, []ConversationBody{ConversationBodyUnknown, ConversationBodyUnknown, ConversationBodyUnknown})
}

func TestConversationCardsKeyIgnoresUnrelatedDetail(t *testing.T) {
	a := Detail{ID: "case", Messages: []MessageCard{{ID: "m1", From: "Ann", When: "09:00", Text: "Hi"}}}
	b := Detail{ID: "case", Draft: "Autosaved draft", Messages: []MessageCard{{ID: "m1", From: "Ann B.", When: "Yesterday", Text: "Hi", Mine: true}}}
	if !reflect.DeepEqual(ConversationKeyOf(a), ConversationKeyOf(b)) {
		t.Fatal("unrelated detail changed conversation key")
	}
}

func TestConversationCardsAppendKeepsBodiesAndOpensNewest(t *testing.T) {
	c := NewConversationCards(0)
	c.Apply(cardKey(1, 2))
	c.Asked(1)
	c.Answered(1, true)
	c.SetFolded(0, false)
	expectCardChange(t, c.Apply(cardKey(1, 2, 3)), map[int]int{0: 0, 1: 1})
	expectCards(t, c, []bool{false, false, false}, []ConversationBody{ConversationBodyUnknown, ConversationBodyHTML, ConversationBodyUnknown})
	if !c.NeedsBody(2) {
		t.Fatal("newest should ask")
	}
}

func TestConversationCardsChangedExcerpts(t *testing.T) {
	c := NewConversationCards(0)
	c.Apply(cardKey(1, 2, 3))
	c.SetFolded(0, false)
	c.Asked(0)
	c.Answered(0, true)
	c.Asked(2)
	c.Answered(2, true)
	k := ConversationKey{CaseID: "case-a", Members: []ConversationMember{cardMember(1, "edited"), cardMember(2, "edited"), cardMember(3)}}
	expectCardChange(t, c.Apply(k), map[int]int{0: 0, 1: 1, 2: 2}, 0)
	expectCards(t, c, []bool{false, true, false}, []ConversationBody{ConversationBodyHTML, ConversationBodyUnknown, ConversationBodyHTML})
	if c.Members()[0].Text != "edited" || c.Shows(0, true) != ConversationWeb || c.NeedsBody(0) {
		t.Fatal("old rendered body must stay until reload answers")
	}
	c.SetFolded(1, false)
	if !c.NeedsBody(1) {
		t.Fatal("folded changed excerpt must fetch on open")
	}
	expectCardIndices(t, c.WebCards(), []int{0, 2})
}

func TestConversationCardsChangedInFlightBodyAsksAgain(t *testing.T) {
	c := NewConversationCards(0)
	c.Apply(cardKey(1, 2))
	c.Asked(1)
	k := cardKey(1, 2)
	k.Members[1].Text = "edited"
	expectCardChange(t, c.Apply(k), map[int]int{0: 0, 1: 1})
	if c.Body(1) != ConversationBodyUnknown || !c.NeedsBody(1) {
		t.Fatal("stale in-flight request hid need for fresh body")
	}
}

func TestConversationCardsChangedAddedRemoved(t *testing.T) {
	c := NewConversationCards(0)
	c.Apply(cardKey(1, 2, 3))
	k := cardKey(1, 3, 4)
	k.Members[0].Text = "edited"
	expectCardChange(t, c.Apply(k), map[int]int{0: 0, 1: 2})
	expectCards(t, c, []bool{true, false, false}, nil)
	if !c.NeedsBody(2) {
		t.Fatal("new member not fetched")
	}
}

func TestConversationCardsRepeatedSamplesKeepOwnIndices(t *testing.T) {
	c := NewConversationCards(0)
	m := ConversationMember{Text: "same"}
	c.Apply(ConversationKey{CaseID: "case-a", Members: []ConversationMember{m, m, cardMember(3)}})
	c.SetFolded(1, false)
	expectCardChange(t, c.Apply(ConversationKey{CaseID: "case-a", Members: []ConversationMember{m, m}}), map[int]int{0: 0, 1: 1})
	expectCards(t, c, []bool{true, false}, nil)
}

func TestConversationCardsRemovedNewestOpensPromotedCard(t *testing.T) {
	c := NewConversationCards(0)
	c.Apply(cardKey(1, 2, 3))
	expectCardChange(t, c.Apply(cardKey(1, 2)), map[int]int{0: 0, 1: 1})
	if c.IsFolded(1) || c.Foldable(1) || !c.NeedsBody(1) || c.Shows(1, true) != ConversationText {
		t.Fatal("promoted newest must open even though its previous card was folded")
	}
}

func TestConversationCardsPromotedCachedHTMLRespectsLimit(t *testing.T) {
	c := NewConversationCards(2)
	c.Apply(cardKey(1, 2, 3, 4))
	for _, i := range []int{0, 1, 2} {
		c.SetFolded(i, false)
		c.Answered(i, true)
	}
	// 0 was evicted with its body cached; newest 3 has only an excerpt.
	expectCardIndices(t, c.WebCards(), []int{1, 2})
	if !c.IsFolded(0) {
		t.Fatal("fixture: oldest was not folded")
	}
	expectCardChange(t, c.Apply(cardKey(2, 3, 1)), map[int]int{0: 1, 1: 2, 2: 0})
	expectCardIndices(t, c.WebCards(), []int{1, 2})
	if c.IsFolded(2) || c.NeedsBody(2) {
		t.Fatal("promoted newest must reuse its cached HTML")
	}
}

func TestConversationCardsSamplesAndArrows(t *testing.T) {
	c := NewConversationCards(0)
	yes, no := true, false
	c.Apply(ConversationKey{CaseID: "case-a", Members: []ConversationMember{{Text: "a"}, {Text: "b"}}})
	if c.NeedsBody(1) || c.Shows(1, true) != ConversationText || c.Arrow(0, &no, true) || !c.Arrow(0, &yes, true) {
		t.Fatal("sample rules")
	}
	c.Apply(cardKey(1, 2))
	if !c.Arrow(0, &no, true) || c.Arrow(0, nil, false) || !c.Arrow(0, &yes, false) || c.Arrow(1, &yes, true) {
		t.Fatal("arrow rules")
	}
}

func TestConversationCardsBodyOutcomes(t *testing.T) {
	c := NewConversationCards(0)
	c.Apply(cardKey(1))
	if !c.NeedsBody(0) {
		t.Fatal("initial fetch")
	}
	c.Asked(0)
	if c.NeedsBody(0) || c.Body(0) != ConversationBodyAsked || c.Shows(0, true) != ConversationText {
		t.Fatal("pending fetch")
	}
	c.Answered(0, false)
	if c.Shows(0, true) != ConversationText {
		t.Fatal("fallback")
	}
	c.Answered(0, true)
	if c.Shows(0, true) != ConversationWeb || c.Shows(0, false) != ConversationText {
		t.Fatal("live HTML")
	}
	c.Asked(0)
	if c.Body(0) != ConversationBodyHTML {
		t.Fatal("second ask overwrote answer")
	}
}

func TestConversationCardsFoldingReusesBody(t *testing.T) {
	c := NewConversationCards(0)
	c.Apply(cardKey(1, 2))
	c.SetFolded(0, false)
	c.Asked(0)
	c.Answered(0, true)
	if c.Shows(0, true) != ConversationWeb {
		t.Fatal("open HTML")
	}
	c.SetFolded(0, true)
	if c.Shows(0, true) != ConversationFolded || len(c.WebCards()) != 0 {
		t.Fatal("folded view must release HTML")
	}
	c.SetFolded(0, false)
	if c.NeedsBody(0) || c.Shows(0, true) != ConversationWeb {
		t.Fatal("reopening must reuse body")
	}
	if len(c.SetFolded(1, true)) != 0 || c.IsFolded(1) {
		t.Fatal("newest cannot fold")
	}
}

func TestConversationCardsLRUEviction(t *testing.T) {
	c := NewConversationCards(0)
	c.Apply(cardKey(1, 2, 3, 4, 5, 6))
	c.Asked(5)
	got := c.Answered(5, true)
	for i := 0; i < 5; i++ {
		got = append(got, c.SetFolded(i, false)...)
		c.Asked(i)
		got = append(got, c.Answered(i, true)...)
	}
	expectCardIndices(t, got, []int{0, 1})
	expectCardIndices(t, c.WebCards(), []int{2, 3, 4, 5})
	expectCards(t, c, []bool{true, true, false, false, false, false}, nil)
}

func TestConversationCardsLimitSparesNewestAndOpened(t *testing.T) {
	c := NewConversationCards(1)
	c.Apply(cardKey(1, 2, 3))
	c.Asked(2)
	expectCardIndices(t, c.Answered(2, true), nil)
	c.SetFolded(0, false)
	c.Asked(0)
	expectCardIndices(t, c.Answered(0, true), nil)
	expectCardIndices(t, c.WebCards(), []int{0, 2})
	c.SetFolded(1, false)
	c.Asked(1)
	expectCardIndices(t, c.Answered(1, true), []int{0})
	// With only newest and protected there are no eviction candidates.
	c = NewConversationCards(1)
	c.Apply(cardKey(1, 2))
	c.Asked(1)
	c.Answered(1, true)
	c.SetFolded(0, false)
	c.Asked(0)
	expectCardIndices(t, c.Answered(0, true), nil)
	expectCardIndices(t, c.WebCards(), []int{0, 1})
}

func TestConversationCardsReopeningUpdatesRecency(t *testing.T) {
	c := NewConversationCards(3)
	c.Apply(cardKey(1, 2, 3, 4))
	c.Asked(3)
	c.Answered(3, true)
	for _, i := range []int{0, 1} {
		c.SetFolded(i, false)
		c.Asked(i)
		c.Answered(i, true)
	}
	c.SetFolded(0, true)
	c.SetFolded(0, false)
	c.SetFolded(2, false)
	c.Asked(2)
	expectCardIndices(t, c.Answered(2, true), []int{1})
}

func TestConversationCardsTextDoesNotCount(t *testing.T) {
	c := NewConversationCards(1)
	c.Apply(cardKey(1, 2, 3))
	c.Asked(2)
	c.Answered(2, false)
	c.SetFolded(0, false)
	c.Asked(0)
	expectCardIndices(t, c.Answered(0, true), nil)
	c.SetFolded(1, false)
	c.Asked(1)
	expectCardIndices(t, c.Answered(1, false), nil)
	expectCardIndices(t, c.WebCards(), []int{0})
}

func TestConversationCardsUnavailableWebFreesSlot(t *testing.T) {
	c := NewConversationCards(2)
	c.Apply(cardKey(1, 2, 3, 4))
	c.Asked(3)
	c.Answered(3, true)
	c.SetFolded(0, false)
	c.Asked(0)
	c.Answered(0, true)
	expectCardIndices(t, c.WebCards(), []int{0, 3})
	expectCardIndices(t, c.Answered(0, false), nil)
	expectCardIndices(t, c.WebCards(), []int{3})
	if c.Shows(0, true) != ConversationText {
		t.Fatal("unavailable HTML should show excerpt")
	}
	c.SetFolded(1, false)
	c.Asked(1)
	expectCardIndices(t, c.Answered(1, true), nil)
	expectCards(t, c, []bool{false, false, true, false}, nil)
	expectCardIndices(t, c.WebCards(), []int{1, 3})
}

func TestConversationCardsBoundsAndOwnedSlices(t *testing.T) {
	c := NewConversationCards(-1)
	if c.MaxLive() != 1 || c.Newest() != -1 {
		t.Fatal("empty bounds")
	}
	for _, i := range []int{-1, 0, 5} {
		c.Asked(i)
		if len(c.Answered(i, true)) != 0 || len(c.SetFolded(i, true)) != 0 || c.IsFolded(i) || c.Foldable(i) || c.NeedsBody(i) || c.Body(i) != ConversationBodyUnknown || c.Shows(i, true) != ConversationText {
			t.Fatal("invalid index changed empty model")
		}
	}
	k := cardKey(1, 2)
	c.Apply(k)
	k.Members[0].Text = "mutated"
	m := c.Members()
	m[1].Text = "mutated"
	if c.Members()[0].Text == "mutated" || c.Members()[1].Text == "mutated" {
		t.Fatal("external slice owns model storage")
	}
	if i, ok := c.Index("m2"); !ok || i != 1 {
		t.Fatal("index")
	}
	if _, ok := c.Index("gone"); ok {
		t.Fatal("missing index")
	}
}

func TestConversationCompensatedTop(t *testing.T) {
	for _, tt := range []struct{ top, bottom, delta, document, viewport, want float64 }{
		{500, 400, 200, 2000, 600, 700}, {500, 500, -100, 2000, 600, 400},
		{500, 800, 200, 2000, 600, 500}, {500, 300, 800, 1000, 600, 400},
		{50, 20, -300, 1000, 600, 0}, {500, 500.5, 200, 2000, 600, 700},
		{500, 500.6, 200, 2000, 600, 500}, {50, 20, 50, 200, 600, 0},
	} {
		if got := ConversationCompensatedTop(tt.top, tt.bottom, tt.delta, tt.document, tt.viewport); got != tt.want {
			t.Errorf("%+v: got %v", tt, got)
		}
	}
}
