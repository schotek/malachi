// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package conversation is the view logic of a whole conversation in the
// reading pane. Selecting a folded conversation row of the grouped list
// (two or more members in the folder; a Jira folder is always grouped)
// shows every member the folder holds, stacked oldest first with full
// bodies, instead of only the newest one; a member row and a
// single-message row keep the single-message view.
//
// The package turns the answer of a folder-scoped thread.get (the summary
// and the members, oldest first, at most api.MaxThreadMessages, the
// newest) into the items the pane stacks: a card per message (a mail
// message, or the description or a comment of an issue, with the Jira
// badges), a compact row per status or assignee change of an issue, and on
// top a row that says how many older members are left out. It also picks
// the one member opening the conversation marks read (the newest that is
// not an event) and the item the pane scrolls to (the newest), and keeps
// the items in step when a member arrives or goes while the conversation
// is shown.
//
// The pane stacks native cards, each body in its own locked view, never
// one composed document: a message's CSS could restyle or forge the
// headers of the others. Headers are plain text only; every string here
// that comes from a message is attacker-controlled.
//
// The package is pure (no GTK, no gettext, no cgo: the caller passes a
// Translator) so that its rules are tested without a display; macOS
// MalachiCore ports it one to one first, later GTK (with an adapter over
// ui/internal/i18n) and Windows Malachi.Core.
package conversation

import (
	"fmt"
	"slices"
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/capabilities"
	"github.com/schotek/malachi/ui/internal/jira"
)

// Translator translates a msgid of the malachi domain (jira.Translator:
// T a plain one, N a plural form for n, C one with a context).
type Translator = jira.Translator

// IsConversationRow reports a listed conversation whose selection shows
// the whole conversation: two or more members in the folder. The outbox is
// never grouped (the caller's rule, as for the list).
func IsConversationRow(t api.ThreadSummary) bool {
	return t.MessageCount >= 2
}

// ItemKind is what an item of the stack is.
type ItemKind int

// The kinds.
const (
	// ItemMessage is a message card: a mail message, or the description or
	// a comment of an issue. Its body is shown in full.
	ItemMessage ItemKind = iota
	// ItemEvent is a compact row of an issue's status or assignee changes:
	// native text only (never a web view), never unread, never marked read.
	ItemEvent
	// ItemTruncated is the row on top that says how many older members
	// are left out (thread.get returns the newest api.MaxThreadMessages).
	ItemTruncated
)

// Item is one entry of the stack.
type Item struct {
	Kind ItemKind
	// Message is the member (ItemMessage and ItemEvent); the zero value for
	// ItemTruncated.
	Message api.MessageSummary
	// Sender is the name the card's compact header shows: the display
	// name (else the address) of the first sender that has one, cleaned
	// for one line (jira.Clean); "" when there is none.
	Sender string
	// Unread marks an unread message card; an event is never unread,
	// whatever its flags.
	Unread bool
	// Mine marks a member the account's own user wrote: the pane tints
	// its avatar with the accent colour and changes nothing else (the name
	// stays the sender's). An item of an issue says so itself
	// (api.MessageIssue.Mine), and a comment an integration relayed (Via)
	// is never the user's, whatever the site says; a mail message is the
	// user's when the address of its first sender is the account's,
	// compared without case and surrounding space (no sender, no address
	// or an account without one: not the user's). An address is what the
	// sender wrote: a forged From looks like the user's own message.
	Mine bool
	// Internal marks an internal comment of a service-desk issue, which
	// shows the badge InternalLabel ("" when not internal). Via names the
	// integration that posted a comment for its author ("via Issue
	// Sync"); Edited is the badge of a comment changed after it was
	// posted. "" when not; always "" for mail. The texts are those of
	// jira.IssueCard.
	Internal      bool
	InternalLabel string
	Via, Edited   string
	// EventLines are the sentences of an event, one per change
	// (jira.EventLines); EventText is them on one line (jira.EventText),
	// for an accessible name. Empty for other kinds.
	EventLines []string
	EventText  string
	// Text is the sentence of ItemTruncated ("112 earlier messages are not
	// shown"); "" for other kinds.
	Text string
}

// Model is what the reading pane shows of one conversation.
type Model struct {
	// Thread is the conversation; Merge ignores a message of another one.
	Thread api.ThreadID
	// Items are the stack, oldest first by (date, id): an ItemTruncated on
	// top when Earlier > 0, then the members. Empty when the conversation
	// has no member to show: the pane shows its empty page then, or, when
	// Remove took the last shown member and Earlier > 0, loads the
	// conversation again (Build never leaves Earlier > 0 without items).
	Items []Item
	// Issue is the issue card shown once above the stack of a Jira
	// conversation (jira.IssueCard without an item: no badges); nil for
	// mail and for an empty model.
	Issue *jira.Card
	// Earlier is how many older members of the conversation in the folder
	// are not in Items.
	Earlier int
	// MarkRead is the member opening the conversation marks read (after
	// the usual delay): the newest message card that is not a queued
	// message of the outbox, when it is unread; "" when it is read or
	// there is none. Older unread members stay unread, and events are
	// never marked. The model recomputes it after every change; a client
	// acts on it when the conversation is opened (and may when a member
	// arrives while the pane shows the end), never after a flag change: a
	// member the user marked unread stays unread.
	MarkRead api.MessageID
	// ScrollTo is the index into Items the pane scrolls to when it opens
	// the conversation: the newest item; -1 when Items is empty. Whether
	// an arrival scrolls (only when the pane was at the end) is the
	// client's decision.
	ScrollTo int
}

// Index is the position in Items of the member id; -1 when it is not
// shown.
func (m Model) Index(id api.MessageID) int {
	if id == "" {
		return -1
	}
	for i, it := range m.Items {
		if it.Kind != ItemTruncated && it.Message.ID == id {
			return i
		}
	}
	return -1
}

// Build makes the model of a conversation from a folder-scoped thread.get:
// thread is its summary, members its folder members, account the account
// they belong to (its address tells the user's own mail, Item.Mine). The
// members are ordered oldest first by (date, id) whatever order they come
// in; a member without an id and a repeated id (the first is kept) are
// dropped; beyond api.MaxThreadMessages only the newest are kept. An
// event whose changes this client does not know at all (the field is an
// open enum) is left out, as jira.EventLines leaves out such a change.
// Earlier counts the members of thread.MessageCount that are not among
// the members. The issue card is the thread's issue, else the newest
// member's. No member to show makes an empty model with Earlier 0 (the
// conversation left the folder: nothing to load again).
func Build(thread api.ThreadSummary, members []api.MessageSummary, account api.Account, tr Translator) Model {
	m := Model{Thread: thread.ID, ScrollTo: -1}
	list := sortedUnique(members)
	shown := list
	if len(shown) > api.MaxThreadMessages {
		shown = shown[len(shown)-api.MaxThreadMessages:]
	}
	m.Earlier = max(thread.MessageCount, len(list)) - len(shown)
	items := make([]Item, 0, len(shown))
	for _, s := range shown {
		if it, ok := memberItem(s, account, tr); ok {
			items = append(items, it)
		}
	}
	if len(items) == 0 {
		return Model{Thread: thread.ID, ScrollTo: -1}
	}
	info := thread.Issue
	for i := len(shown) - 1; info == nil && i >= 0; i-- {
		if shown[i].Issue != nil {
			info = &shown[i].Issue.IssueInfo
		}
	}
	var card *jira.Card
	if info != nil {
		c := jira.IssueCard(*info, nil, tr)
		card = &c
	}
	return assemble(m, items, card, tr)
}

// Merge puts a member that arrived while the conversation is shown in its
// place by (date, id), or replaces the shown member with the same id by
// arrived (its flags, delivery state or issue changed) and moves it if
// its date changed. A message without an id, or of another conversation
// (a thread id that differs), leaves the model as it is; which folder it
// is in is the caller's check, as for the list. A member with an issue
// refreshes the issue card (an event has changed the status). account is
// the conversation's account, as for Build. MarkRead and ScrollTo follow
// the rules of Build; the model given is not modified.
func Merge(m Model, arrived api.MessageSummary, account api.Account, tr Translator) Model {
	if arrived.ID == "" || (m.Thread != "" && arrived.ThreadID != "" && arrived.ThreadID != m.Thread) {
		return m
	}
	items := make([]Item, 0, len(m.Items)+1)
	for _, it := range m.Items {
		if it.Kind != ItemTruncated && it.Message.ID != arrived.ID {
			items = append(items, it)
		}
	}
	if it, ok := memberItem(arrived, account, tr); ok {
		at := len(items)
		for i, x := range items {
			if before(arrived, x.Message) {
				at = i
				break
			}
		}
		items = slices.Insert(items, at, it)
	}
	card := m.Issue
	if arrived.Issue != nil {
		c := jira.IssueCard(arrived.Issue.IssueInfo, nil, tr)
		card = &c
	}
	return assemble(m, items, card, tr)
}

// Remove drops the member id (it was moved, deleted or left the folder).
// An id that is not shown leaves the model as it is. When the last shown
// member goes, the model becomes empty and keeps Earlier: a conversation
// with older members is loaded again. MarkRead and ScrollTo follow the
// rules of Build; the model given is not modified.
func Remove(m Model, id api.MessageID) Model {
	at := m.Index(id)
	if at < 0 {
		return m
	}
	items := make([]Item, 0, len(m.Items)-1)
	members := 0
	for i, it := range m.Items {
		if i == at {
			continue
		}
		items = append(items, it)
		if it.Kind != ItemTruncated {
			members++
		}
	}
	if members == 0 {
		return Model{Thread: m.Thread, Earlier: m.Earlier, ScrollTo: -1}
	}
	m.Items = items
	m.MarkRead = markRead(items)
	m.ScrollTo = len(items) - 1
	return m
}

// CardActions are the buttons a card offers on hover: Reply (labelled
// Comment when Comment is set), Reply All and Forward, as the message
// toolbar would offer them for this member alone (capabilities.Available
// with the member selected). composeAccount says some enabled account can
// compose (capabilities.Situation.ComposeAccount): the forward of an issue
// goes out from a mail account. An event offers none; the other actions
// (move, trash, archive, junk) are never set here.
func CardActions(account api.Account, m api.MessageSummary, composeAccount bool) capabilities.Actions {
	if jira.IsEvent(m.Issue) {
		return capabilities.Actions{}
	}
	a := capabilities.Available(capabilities.Situation{
		Account:        account,
		Selected:       true,
		Outbox:         m.Outbox != nil,
		ComposeAccount: composeAccount,
	})
	return capabilities.Actions{Reply: a.Reply, ReplyAll: a.ReplyAll, Forward: a.Forward, Comment: a.Comment}
}

// assemble completes m from its member items, oldest first: the row of
// older members on top when Earlier > 0, the issue card, MarkRead and
// ScrollTo. No member items make an empty model (Thread and Earlier
// kept).
func assemble(m Model, items []Item, card *jira.Card, tr Translator) Model {
	out := Model{Thread: m.Thread, Earlier: m.Earlier, ScrollTo: -1}
	if len(items) == 0 {
		return out
	}
	if out.Earlier > 0 {
		out.Items = make([]Item, 0, len(items)+1)
		out.Items = append(out.Items, truncatedItem(out.Earlier, tr))
	}
	out.Items = append(out.Items, items...)
	out.Issue = card
	out.MarkRead = markRead(out.Items)
	out.ScrollTo = len(out.Items) - 1
	return out
}

// truncatedItem is the row that says n older members are left out.
func truncatedItem(n int, tr Translator) Item {
	return Item{
		Kind: ItemTruncated,
		// TRANSLATORS: at the top of a conversation in the reading pane; only its newest messages are shown.
		Text: fmt.Sprintf(tr.N("%d earlier message is not shown", "%d earlier messages are not shown", n), n),
	}
}

// memberItem is the item of member s of an account's conversation; false
// for an event without a change this client knows.
func memberItem(s api.MessageSummary, account api.Account, tr Translator) (Item, bool) {
	it := Item{Kind: ItemMessage, Message: s, Sender: sender(s.From), Mine: mine(s, account)}
	if jira.IsEvent(s.Issue) {
		lines := jira.EventLines(s.Issue.Changes, tr)
		if len(lines) == 0 {
			return Item{}, false
		}
		it.Kind = ItemEvent
		it.EventLines = lines
		it.EventText = jira.EventText(s.Issue.Changes, tr)
		return it, true
	}
	it.Unread = !hasFlag(s.Flags, api.FlagSeen)
	if s.Issue != nil {
		c := jira.IssueCard(s.Issue.IssueInfo, s.Issue, tr)
		it.Internal, it.InternalLabel = c.Internal, c.InternalLabel
		it.Via, it.Edited = c.Via, c.Edited
	}
	return it, true
}

// markRead is the newest message card that is not queued in the outbox
// when it is unread, else "".
func markRead(items []Item) api.MessageID {
	for i := len(items) - 1; i >= 0; i-- {
		it := items[i]
		if it.Kind != ItemMessage || it.Message.Outbox != nil {
			continue
		}
		if it.Unread {
			return it.Message.ID
		}
		return ""
	}
	return ""
}

// mine reports whether the user of account wrote member s (Item.Mine).
func mine(s api.MessageSummary, account api.Account) bool {
	if s.Issue != nil {
		return s.Issue.Mine && s.Issue.Via == ""
	}
	if len(s.From) == 0 {
		return false
	}
	own := strings.TrimSpace(account.Config.Email)
	from := strings.TrimSpace(s.From[0].Address)
	return own != "" && from != "" && strings.EqualFold(own, from)
}

// sender is the cleaned display name (else address) of the first address
// that has one.
func sender(from []api.Address) string {
	for _, a := range from {
		if name := jira.Clean(a.Name); name != "" {
			return name
		}
		if addr := jira.Clean(a.Address); addr != "" {
			return addr
		}
	}
	return ""
}

// sortedUnique is members without empty and repeated ids (the first
// kept), oldest first by (date, id).
func sortedUnique(members []api.MessageSummary) []api.MessageSummary {
	seen := make(map[api.MessageID]bool, len(members))
	out := make([]api.MessageSummary, 0, len(members))
	for _, s := range members {
		if s.ID == "" || seen[s.ID] {
			continue
		}
		seen[s.ID] = true
		out = append(out, s)
	}
	slices.SortStableFunc(out, func(a, b api.MessageSummary) int {
		switch {
		case before(a, b):
			return -1
		case before(b, a):
			return 1
		}
		return 0
	})
	return out
}

// before orders members oldest first: by date, then by id (thread.get's
// order).
func before(a, b api.MessageSummary) bool {
	if !a.Date.Equal(b.Date) {
		return a.Date.Before(b.Date)
	}
	return a.ID < b.ID
}

func hasFlag(flags []api.Flag, f api.Flag) bool {
	return slices.Contains(flags, f)
}
