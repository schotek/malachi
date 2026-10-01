// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package conversation

import "github.com/schotek/malachi/backend/pkg/api"

// The body of a message shows its new text: the daemon cuts the quoted
// history under it (message.body with trimQuoted; quotedTrimmed says it
// cut something), and a small button under the body ("•••") shows it, then
// hides it again. What the user revealed holds until another conversation
// or message is shown, like the folds. Wherever a body shows: the cards of
// the conversation, the message alone in the pane, the message window.

// QuotedTextLabel is the tooltip and accessible name of the button under a
// trimmed body: shown is whether the quoted history shows now.
func QuotedTextLabel(shown bool, tr Translator) string {
	if shown {
		// TRANSLATORS: the tooltip of the three-dot button under a message whose quoted earlier messages show; hides them.
		return tr.T("Hide Quoted Text")
	}
	// TRANSLATORS: the tooltip of the three-dot button under a message whose quoted earlier messages are hidden; shows them.
	return tr.T("Show Quoted Text")
}

// QuotedOffer is what the button under a body offers.
type QuotedOffer int

// The offers.
const (
	// QuotedNone: no button (nothing was cut, nothing shows yet, an event,
	// an attached message).
	QuotedNone QuotedOffer = iota
	// QuotedShow: the daemon cut the quoted history from the body on
	// display (Show Quoted Text).
	QuotedShow
	// QuotedHide: the whole body shows, is on its way or failed: the way
	// back to the trimmed one (Hide Quoted Text).
	QuotedHide
)

// OfferQuoted is the button for a body: shown is whether the view asked
// for the whole body (the user revealed the quoted history), failed
// whether the body on display failed, trimmed whether the body on display
// came with quotedTrimmed. While the whole body is asked for the button
// always offers the way back, even before it arrives or when it failed.
func OfferQuoted(shown, failed, trimmed bool) QuotedOffer {
	switch {
	case shown:
		return QuotedHide
	case !failed && trimmed:
		return QuotedShow
	}
	return QuotedNone
}

// Label is the button's tooltip and accessible name (QuotedTextLabel); ""
// for QuotedNone.
func (o QuotedOffer) Label(tr Translator) string {
	if o == QuotedNone {
		return ""
	}
	return QuotedTextLabel(o == QuotedHide, tr)
}

// QuotedReveal is the messages whose quoted history the user revealed in
// one view, for what that view shows now (a conversation, or a message
// alone): another selection forgets them, an update of the same one keeps
// them. A body asked for again (the cache let it go, the daemon rebuilt
// the message) comes back as the user left it. The zero value is ready.
// A port of the macOS client's QuotedReveal (ConversationQuoted.swift).
type QuotedReveal struct {
	shown    string
	set      bool
	revealed map[api.MessageID]bool
}

// Show is called whenever the view shows selection (a thread or message
// id), anew or updated: the messages revealed for another selection are
// forgotten, those for this one kept.
func (r *QuotedReveal) Show(selection string) {
	if !r.set || selection != r.shown {
		r.shown, r.set = selection, true
		r.revealed = nil
	}
}

// Clear forgets the selection and what was revealed for it.
func (r *QuotedReveal) Clear() {
	r.shown, r.set = "", false
	r.revealed = nil
}

// IsRevealed reports whether the quoted history of message id shows.
func (r *QuotedReveal) IsRevealed(id api.MessageID) bool {
	return r.revealed[id]
}

// Set records the user's Show Quoted Text (on) or Hide Quoted Text of
// message id.
func (r *QuotedReveal) Set(id api.MessageID, on bool) {
	if id == "" {
		return
	}
	if !on {
		delete(r.revealed, id)
		return
	}
	if r.revealed == nil {
		r.revealed = map[api.MessageID]bool{}
	}
	r.revealed[id] = true
}
