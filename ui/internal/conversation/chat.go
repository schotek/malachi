// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package conversation

import (
	"slices"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
)

// The conversation shows in one of two layouts, which a toggle above it
// switches for every conversation (a settings key that outlives a
// restart): the timeline (the cards stacked with the avatars and the line
// in the left margin, the card that opened the conversation on top, the
// others newest first, open at the top; the default) and the chat. The
// chat reads like a messaging app: the others' messages as bubbles on the
// left, the user's on the right, oldest first from the top, open at the
// end (ChatScrollTo). A bubble is plain text only: the sender, the date,
// the unread dot, a paperclip, the Jira badges and the message's text as
// the daemon derived it, with the quoted history cut (message.body's text
// with trimQuoted; BubbleText). It has no buttons, no attachment chips, no
// web view and folds never; its text cannot be selected, so that a double
// click anywhere in it opens the message in its own window (a draft in the
// compose window), where everything else is. Events and the row of older
// messages are centred rows without a bubble; the drafts come last.
//
// The model does not change: the chat is a second layout over the same
// conversation controller and its cache of bodies, so that switching
// shows the same conversation without asking thread.get again. A bubble
// shows the summary's snippet, dimmed, until its body is loaded, and the
// chat asks for the bodies of the bubbles near the visible part only (two
// heights of the view, counted from the end), as the timeline does for its
// cards. Where the bubbles stand, how wide they are and what is near are
// the client's measures, not this package's.

// ChatSide is where a row of the chat stands.
type ChatSide int

// The sides.
const (
	// ChatLeft: a message of someone else.
	ChatLeft ChatSide = iota
	// ChatRight: the user's own message, sent reply or draft.
	ChatRight
	// ChatCenter: a row without a bubble (an issue event, the row of older
	// messages).
	ChatCenter
)

// ChatRowKind is what a row of the chat is.
type ChatRowKind int

// The kinds.
const (
	// ChatBubble is a message: a mail message, a sent reply, or the
	// description or a comment of an issue.
	ChatBubble ChatRowKind = iota
	// ChatDraft is an unsent reply or comment (a DraftCard).
	ChatDraft
	// ChatEvent is an issue's status or assignee changes.
	ChatEvent
	// ChatTruncated is the row that says how many older messages are left
	// out.
	ChatTruncated
)

// ChatRow is one row of the chat.
type ChatRow struct {
	Kind ChatRowKind
	Side ChatSide
	// Item is the model's item of a ChatBubble, ChatEvent or ChatTruncated
	// (whose Text is the row's sentence); the zero value for a ChatDraft.
	Item Item
	// Draft is the draft card of a ChatDraft; the zero value otherwise.
	Draft DraftCard
}

// ChatRows are the rows of the chat of m, top to bottom: the row of older
// messages first when there is one (centred), then the items of m in the
// model's order (oldest first), a message as a bubble and an event
// centred, then the drafts (Drafts' cards) at the end, on the right, the
// oldest saved first (equal UpdatedAt in the order given).
//
// A bubble stands on the right when the user wrote it (Item.Mine) or it is
// the user's reply in Sent (Item.Sent): a reply sent from another identity
// of the user, whose address is not the account's, has Mine false and
// still stands on the right. A comment an integration relayed (Item.Via)
// is never Mine, whatever the site says, so it stands on the left. The
// issue card is no row: the client puts it above them, across the column.
func ChatRows(m Model, drafts []DraftCard) []ChatRow {
	rows := make([]ChatRow, 0, len(m.Items)+len(drafts))
	for _, it := range m.Items {
		if it.Kind == ItemTruncated {
			rows = append(rows, ChatRow{Kind: ChatTruncated, Side: ChatCenter, Item: it})
		}
	}
	for _, it := range m.Items {
		switch it.Kind {
		case ItemMessage:
			side := ChatLeft
			if it.Mine || it.Sent {
				side = ChatRight
			}
			rows = append(rows, ChatRow{Kind: ChatBubble, Side: side, Item: it})
		case ItemEvent:
			rows = append(rows, ChatRow{Kind: ChatEvent, Side: ChatCenter, Item: it})
		}
	}
	oldest := slices.Clone(drafts)
	slices.SortStableFunc(oldest, func(a, b DraftCard) int {
		return a.Draft.UpdatedAt.Compare(b.Draft.UpdatedAt)
	})
	for _, d := range oldest {
		rows = append(rows, ChatRow{Kind: ChatDraft, Side: ChatRight, Draft: d})
	}
	return rows
}

// ChatScrollTo is the index into rows the chat scrolls to when it opens a
// conversation: the last row (the newest draft, else the newest message);
// -1 when there are no rows. It is not Model.ScrollTo, which indexes
// Model.Items.
func ChatScrollTo(rows []ChatRow) int {
	return len(rows) - 1
}

// BubbleText is the text of a message bubble from the plain text of its
// loaded body (message.body's text, the variant without the quoted
// history): cleaned as a draft card's text is (board.CleanBlock, at most
// draftTextBytes: line breaks kept, at most one empty line in a row,
// control and format characters gone, trimmed), so that a message bubble
// and a draft bubble read alike; "" for an empty text. The states of a
// body (on its way, too large, unreadable, empty) and the errors of the
// call are the view's to show, with the texts of the timeline's cards;
// until a body is asked for, the bubble shows the summary's snippet.
func BubbleText(text string) string {
	if text == "" {
		return ""
	}
	return board.CleanBlock(text, draftTextBytes)
}

// ChatToggleLabel is the label of the toggle button above the conversation
// that switches its layout: it names the layout a click shows, so it
// follows the state (on: the chat is shown, "Show as Thread" goes back to
// the timeline; off: "Show as Chat").
func ChatToggleLabel(on bool, tr Translator) string {
	if !on {
		// TRANSLATORS: a toggle button above a conversation in the reading pane, off; a click shows the messages as chat bubbles.
		return tr.T("Show as Chat")
	}
	// TRANSLATORS: the same toggle button above a conversation, on (the messages show as chat bubbles); a click goes back to the conversation as cards.
	return tr.T("Show as Thread")
}

// ChatToggleTooltip is the tooltip of the toggle, which follows its state
// as the label does: off, what the chat shows; on, what a click goes back
// to.
func ChatToggleTooltip(on bool, tr Translator) string {
	if !on {
		// TRANSLATORS: the tooltip of the "Show as Chat" toggle above a conversation.
		return tr.T("Others on the left, your messages on the right; double-click opens the message")
	}
	// TRANSLATORS: the tooltip of the "Show as Thread" toggle above a conversation shown as chat bubbles.
	return tr.T("Back to the timeline: the messages as cards, newest first")
}

// BubbleRendersHTML says whether a bubble of the chat layout shows the
// message's sanitized HTML in the locked web view the timeline cards use
// (sized mode, the same document as the card: the trimQuoted variant)
// instead of its plain text: the preference conversation-chat-html is
// on, the loaded body has HTML and it is not withheld. A text-only
// message, a withheld or failed body and a body not loaded yet read as
// text (the snippet as placeholder) as without the preference.
func BubbleRendersHTML(setting, hasHTML, withheld bool) bool {
	return setting && hasHTML && !withheld
}

// ChatHTMLRowTitle is the title of the preference's row in Preferences →
// Message list, next to Group by conversation.
func ChatHTMLRowTitle(tr Translator) string {
	// TRANSLATORS: a switch in Preferences → Message list; on, a message in the chat layout of a conversation shows its formatting (HTML) instead of plain text.
	return tr.T("Render HTML in Chat")
}

// ChatHTMLRowSubtitle is the subtitle of the preference's row in
// Preferences → Message list, under ChatHTMLRowTitle.
func ChatHTMLRowSubtitle(tr Translator) string {
	// TRANSLATORS: the subtitle of the "Render HTML in Chat" switch in Preferences → Message list.
	return tr.T("Messages in the chat layout as they were sent, in a locked web view, instead of their plain text")
}

// ChatPreviewLines is how many lines of a message a shortened bubble
// shows: a text bubble that many lines of its text with an ellipsis, an
// HTML bubble its document clipped to that many lines' height with a
// fade. A shortened message bubble (BubbleShortened) has a "Show More"
// hint under the body, and a click anywhere in it (not a double-click,
// which opens the message) shows the whole message.
//
// A draft bubble (ChatDraft) is always shortened to these lines, whatever
// the two preferences and Expanded say (they govern message bubbles
// only): it shows no "Show More" hint, it has a light red tint (the error
// colour at the same strength as the accent tint of the user's own
// bubbles), and a single click opens the draft in its window (the compose
// window, as the assistant's "Open draft" card does), the same action as
// its double-click (editThreadDraft); it never expands.
const ChatPreviewLines = 3

// BubbleShortened says whether a message bubble (ChatBubble) shows the
// shortened preview: the preference of the bubble's side is on (mine:
// the user's own message, the bubble on the right,
// conversation-chat-truncate-mine; else the other party's,
// conversation-chat-truncate-others) and the user has not expanded that
// message. It is not asked for a draft bubble, which is always shortened
// and never expanded (see ChatPreviewLines).
func BubbleShortened(mineOn, othersOn, mine, expanded bool) bool {
	if expanded {
		return false
	}
	if mine {
		return mineOn
	}
	return othersOn
}

// Expanded is the set of messages the user expanded in the conversation
// shown; it is forgotten when another conversation or case is selected
// (as Folds are), never persisted. Zero value usable. An expanded bubble
// stays expanded: there is no collapsing back. It holds message bubbles
// only: a draft bubble is always shortened and a click on it opens the
// draft (see ChatPreviewLines).
type Expanded struct {
	messages map[api.MessageID]bool
}

// Expand records that the user expanded the bubble of message id; an
// empty id is ignored.
func (e *Expanded) Expand(id api.MessageID) {
	if id == "" {
		return
	}
	if e.messages == nil {
		e.messages = map[api.MessageID]bool{}
	}
	e.messages[id] = true
}

// Is says whether the user expanded the bubble of message id.
func (e *Expanded) Is(id api.MessageID) bool {
	return id != "" && e.messages[id]
}

// Reset forgets every expanded bubble: another conversation or case is
// selected.
func (e *Expanded) Reset() {
	e.messages = nil
}

// ChatShowMore is the hint under a shortened message bubble (never under
// a draft bubble).
func ChatShowMore(tr Translator) string {
	// TRANSLATORS: the hint under a shortened message in the chat layout of a conversation; a click on the message shows all of it.
	return tr.T("Show More")
}

// ChatPreferencesGroupTitle is the title of the group in Preferences →
// Message list holding Group by conversation, Render HTML in Chat and the
// two shortening switches.
func ChatPreferencesGroupTitle(tr Translator) string {
	// TRANSLATORS: the title of a group of switches in Preferences → Message list about conversations (grouping, the chat layout).
	return tr.T("Conversations")
}

// ChatTruncateMineRowTitle is the title of the row of
// conversation-chat-truncate-mine in Preferences → Message list, in the
// group ChatPreferencesGroupTitle, after "Render HTML in Chat".
func ChatTruncateMineRowTitle(tr Translator) string {
	// TRANSLATORS: a switch in Preferences → Message list; on, the user's own messages in the chat layout of a conversation show only their first lines until clicked.
	return tr.T("Shorten My Messages in Chat")
}

// ChatTruncateOthersRowTitle is the title of the row of
// conversation-chat-truncate-others, right after ChatTruncateMineRowTitle.
func ChatTruncateOthersRowTitle(tr Translator) string {
	// TRANSLATORS: a switch in Preferences → Message list; on, the other party's messages in the chat layout of a conversation show only their first lines until clicked.
	return tr.T("Shorten Others' Messages in Chat")
}

// ChatTruncateRowSubtitle is the subtitle of both shortening rows in
// Preferences → Message list, under ChatTruncateMineRowTitle and
// ChatTruncateOthersRowTitle.
func ChatTruncateRowSubtitle(tr Translator) string {
	// TRANSLATORS: the subtitle of the "Shorten My Messages in Chat" and "Shorten Others' Messages in Chat" switches in Preferences → Message list.
	return tr.T("About three lines of each message in its bubble; a click shows the whole message")
}
