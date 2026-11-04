// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package conversation

import "github.com/schotek/malachi/backend/pkg/api"

// Every message card of the conversation folds to its header and a preview
// of its text (the summary's snippet) and opens again: its arrow, a click
// on the preview, or the one button above the conversation that folds or
// opens them all. A folded card holds no web view and asks for no body.
// Events and the row of older messages are no cards and do not fold.
//
// Each card starts as DefaultFolds says; what the user chose for a card
// (Folds) holds until another conversation is shown, through every update
// of the model: a card that arrives meanwhile starts as its default.

// Foldable reports an item that folds: a message card.
func Foldable(it Item) bool {
	return it.Kind == ItemMessage && it.Message.ID != ""
}

// DefaultFolds is how each foldable card of items (any order) starts, by
// message id, true for folded. opening is the card that opened the
// conversation (the pane's rule: the description of an issue, else the
// oldest member when no older one is left out; "" for none). A sent card
// starts folded: the user wrote it and knows it. The opening card starts
// folded while another card that is not a sent card follows it (a
// conversation of one message and its status changes, or of one message
// and the user's replies, shows that message whole). Every other card
// starts open. Should that leave every card folded, the newest opens, so
// that a conversation never opens on headers alone.
func DefaultFolds(items []Item, opening api.MessageID) map[api.MessageID]bool {
	// The cards, a repeated id once (its first card).
	cards := make([]Item, 0, len(items))
	seen := map[api.MessageID]bool{}
	others := 0
	for _, it := range items {
		if !Foldable(it) || seen[it.Message.ID] {
			continue
		}
		seen[it.Message.ID] = true
		cards = append(cards, it)
		if !it.Sent && it.Message.ID != opening {
			others++
		}
	}
	out := make(map[api.MessageID]bool, len(cards))
	open := false
	newest := -1
	for i, it := range cards {
		folded := it.Sent || (it.Message.ID == opening && others > 0)
		out[it.Message.ID] = folded
		open = open || !folded
		if newest < 0 || before(cards[newest].Message, it.Message) {
			newest = i
		}
	}
	if !open && newest >= 0 {
		out[cards[newest].Message.ID] = false
	}
	return out
}

// Folds is the fold state of the cards of the conversation on show: the
// user's choices, by message id, over DefaultFolds. The zero value is
// ready.
type Folds struct {
	thread api.ThreadID
	chosen map[api.MessageID]bool
}

// Show is called whenever the pane shows conversation thread, built anew
// or updated: the choices made for another conversation are forgotten,
// those for this one kept.
func (f *Folds) Show(thread api.ThreadID) {
	if thread != f.thread {
		f.thread = thread
		f.chosen = nil
	}
}

// Set records the user's fold (true) or unfold of card id.
func (f *Folds) Set(id api.MessageID, folded bool) {
	if id == "" {
		return
	}
	if f.chosen == nil {
		f.chosen = map[api.MessageID]bool{}
	}
	f.chosen[id] = folded
}

// SetAll records folded for every foldable card of items: the button above
// the conversation (FoldAllOffer). A card that arrives later starts as its
// default.
func (f *Folds) SetAll(items []Item, folded bool) {
	for _, it := range items {
		if Foldable(it) {
			f.Set(it.Message.ID, folded)
		}
	}
}

// State is whether each foldable card of items is folded, by message id:
// the user's choice, else the default (DefaultFolds with opening).
func (f *Folds) State(items []Item, opening api.MessageID) map[api.MessageID]bool {
	out := DefaultFolds(items, opening)
	for id := range out {
		if folded, ok := f.chosen[id]; ok {
			out[id] = folded
		}
	}
	return out
}

// FoldAll is what the button above the conversation offers.
type FoldAll int

// The offers.
const (
	// FoldAllNone: no button (fewer than two cards fold).
	FoldAllNone FoldAll = iota
	// FoldAllCollapse folds every card ("Collapse All").
	FoldAllCollapse
	// FoldAllExpand opens every card ("Expand All").
	FoldAllExpand
)

// FoldAllOffer is the button for the fold state of the cards (State):
// none with fewer than two cards, Collapse All while at least one card is
// open, else Expand All.
func FoldAllOffer(state map[api.MessageID]bool) FoldAll {
	if len(state) < 2 {
		return FoldAllNone
	}
	for _, folded := range state {
		if !folded {
			return FoldAllCollapse
		}
	}
	return FoldAllExpand
}

// Folded is what the offer sets every card to (Folds.SetAll): true for
// Collapse All.
func (a FoldAll) Folded() bool {
	return a == FoldAllCollapse
}

// Label is the button's text; "" for FoldAllNone.
func (a FoldAll) Label(tr Translator) string {
	switch a {
	case FoldAllCollapse:
		// TRANSLATORS: a button above a conversation in the reading pane; folds every message to its header.
		return tr.T("Collapse All")
	case FoldAllExpand:
		// TRANSLATORS: a button above a conversation in the reading pane; shows every message whole.
		return tr.T("Expand All")
	}
	return ""
}
