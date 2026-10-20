// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package conversation

import (
	"fmt"
	"html"
	"strings"
	"unicode"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/capabilities"
	"github.com/schotek/malachi/ui/internal/composebody"
)

// The chat layout of a conversation in Mail ends with a quick-reply bar,
// as a messaging app does: a fixed row under the bubbles with a button
// for the mode (Reply, Reply All, and Forward…, which opens the compose
// window straight away), a field of one to five lines for plain text, a
// button that opens the compose window with what was typed, and Send.
// The timeline, the board's chat and the message window have no bar.
//
// The reply goes to the newest message of someone else in the
// conversation (the newest message when the user wrote all of them;
// QuickReplyTarget), in a mode the account offers for it, as the card's
// buttons do (QuickReplyModes); an issue's conversation offers only a
// public comment. Send takes the daemon's usual way, as the compose
// window does: draft.create on the target, one draft.save whose body is
// the typed text above the original draft.create quoted (QuickReplyBody:
// what the window saves with "Include" checked), message.send. The text
// is the user's own, never mail: it becomes HTML here, paragraphs and
// line breaks with everything escaped (QuickReplyHTML), no Markdown.
// Nothing is cut: a text over api.MaxDraftBodyBytes is refused by the
// daemon's draft.save, and the client keeps it in the bar. The client
// holds the text typed per conversation in memory until it is sent, moved
// into a window or cleared; it is no daemon draft, so that the chat does
// not show it twice (as a draft bubble next to the bar).

// QuickReplyTarget is the message a quick reply answers: the newest
// message card (Items are oldest first) that is neither the user's own
// nor a sent card; when every message is the user's, the newest message
// card. Events and the row of older messages never are. False when m has
// no message card.
func QuickReplyTarget(m Model) (Item, bool) {
	newest := -1
	for i := len(m.Items) - 1; i >= 0; i-- {
		it := m.Items[i]
		if it.Kind != ItemMessage {
			continue
		}
		if !it.Mine && !it.Sent {
			return it, true
		}
		if newest < 0 {
			newest = i
		}
	}
	if newest < 0 {
		return Item{}, false
	}
	return m.Items[newest], true
}

// QuickReplyMode is what the bar's mode button chose.
type QuickReplyMode int

// The modes.
const (
	// QuickReply answers the sender (on an issue: a public comment, which
	// the view labels with jira.ReplyLabel).
	QuickReply QuickReplyMode = iota
	// QuickReplyAll answers everyone.
	QuickReplyAll
	// QuickForward is the menu's Forward…: it never sends from the bar but
	// opens the compose window with a forward of the target.
	QuickForward
)

// ComposeMode is the draft.create mode of m.
func (m QuickReplyMode) ComposeMode() api.ComposeMode {
	switch m {
	case QuickReplyAll:
		return api.ComposeReplyAll
	case QuickForward:
		return api.ComposeForward
	}
	return api.ComposeReply
}

// QuickReplyModes are the modes the bar offers for actions a, the target's
// card actions (CardActions), in the menu's order: Reply, Reply All,
// Forward, each when a has it. An account that comments (a.Comment) offers
// Reply only: the comment; Reply All and Forward are the window's. Empty
// when a offers none of them (the bar is hidden then).
func QuickReplyModes(a capabilities.Actions) []QuickReplyMode {
	if a.Comment {
		return []QuickReplyMode{QuickReply}
	}
	var out []QuickReplyMode
	if a.Reply {
		out = append(out, QuickReply)
	}
	if a.ReplyAll {
		out = append(out, QuickReplyAll)
	}
	if a.Forward {
		out = append(out, QuickForward)
	}
	return out
}

// QuickReplyHTML is the HTML of the user's own text typed in the bar:
// line ends normalised (CRLF and CR to LF), paragraphs split on one or
// more blank lines (a line of spaces only is blank), each paragraph a
// <p> with its lines joined by <br>, every line escaped
// (html.EscapeString); blank lines before the first and after the last
// paragraph are dropped. "" for text with no line that is not blank. It
// never cuts: the daemon refuses a body over api.MaxDraftBodyBytes in
// draft.save, and the client keeps the text.
func QuickReplyHTML(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	var b strings.Builder
	var para []string
	flush := func() {
		if len(para) == 0 {
			return
		}
		b.WriteString("<p>")
		for i, line := range para {
			if i > 0 {
				b.WriteString("<br>")
			}
			b.WriteString(html.EscapeString(line))
		}
		b.WriteString("</p>")
		para = para[:0]
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		para = append(para, line)
	}
	flush()
	return b.String()
}

// QuickReplyBlank reports a text with nothing to send: spaces, control and
// format (Cf) characters only. Send is disabled then. The same rule as
// jira.SendProblem's, which exports no helper for it (mirrored here
// exactly).
func QuickReplyBlank(text string) bool {
	return strings.TrimFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
	}) == ""
}

// QuickReplyBody is the body draft.save gets for a quick reply in mode
// with the user's own text own on draft d (draft.create's result): byte
// for byte what the compose window saves with "Include" checked, the own
// HTML (QuickReplyHTML), a line break and the original draft.create
// quoted (composebody.DraftBody); a comment draft (and a draft without a
// body) is the own HTML alone.
func QuickReplyBody(mode api.ComposeMode, own string, d api.Draft) string {
	return composebody.New(mode, d.Comment != nil, composebody.DraftBody(d)).HTML(QuickReplyHTML(own), true)
}

// QuickReplyPlaceholder is the placeholder and accessible name of the
// bar's field: comment says the reply is a comment on an issue.
func QuickReplyPlaceholder(comment bool, tr Translator) string {
	if comment {
		// TRANSLATORS: the placeholder of the quick-reply field under an issue's conversation shown as chat; what is typed becomes a comment.
		return tr.T("Write a comment…")
	}
	// TRANSLATORS: the placeholder of the quick-reply field under a conversation shown as chat.
	return tr.T("Write a reply…")
}

// QuickReplyTargetTooltip is the tooltip of the bar's mode button, which
// names the target's sender (Item.Sender, already cleaned for one line).
// "" for QuickForward and when the target has no sender: the button keeps
// only its own label then (Reply, Reply All).
func QuickReplyTargetTooltip(mode QuickReplyMode, sender string, tr Translator) string {
	if sender == "" {
		return ""
	}
	switch mode {
	case QuickReply:
		// TRANSLATORS: the tooltip of the quick reply's mode button; %s is the sender of the message the reply goes to, as it shows in the conversation (no inflection).
		return fmt.Sprintf(tr.T("Reply to: %s"), sender)
	case QuickReplyAll:
		// TRANSLATORS: the tooltip of the quick reply's mode button set to reply to all; %s is the sender of the message the reply goes to, as it shows in the conversation (no inflection).
		return fmt.Sprintf(tr.T("Reply all to: %s"), sender)
	}
	return ""
}

// QuickReplyOpenWindowLabel is the tooltip and accessible name of the
// bar's button that opens the compose window with what was typed.
func QuickReplyOpenWindowLabel(tr Translator) string {
	// TRANSLATORS: the tooltip of the quick-reply bar's button that moves the typed reply into the compose window.
	return tr.T("Open in a Window")
}

// QuickReplyForwardLabel is the item of the mode button's menu that opens
// the compose window with a forward of the target (and the typed text
// above it).
func QuickReplyForwardLabel(tr Translator) string {
	// TRANSLATORS: an item of the quick-reply bar's mode menu; opens the compose window with the message forwarded.
	return tr.T("Forward…")
}

// SingleModel is the model of one message for the quick-reply bar under
// the reading pane's page of a single message (docs/plans/chat-view.md
// §12, S2, S4): the bar answers that message, whoever wrote it (in Sent a
// reply to the user's own message), so Update takes the same model shape
// as for a conversation and QuickReplyTarget needs no rule of its own. The
// model's Thread is the message's (the bar keeps the typed text per
// thread, shared with the message's conversation); its one item is an
// ItemMessage with the cleaned sender (Item.Sender, which names the target
// in the mode button's tooltip) and Mine as the caller says (the rule of
// Item.Mine: the window decides it as for a conversation's member). An event of an issue (a row without a
// body) is an ItemEvent without its sentences, so that the bar finds no
// target and hides; a message without an id makes an empty model. The
// model is for the bar only: no issue card, nothing to mark read, never
// rendered.
func SingleModel(s api.MessageSummary, mine bool) Model {
	m := Model{Thread: s.ThreadID, ScrollTo: -1}
	if s.ID == "" {
		return m
	}
	it := Item{Kind: ItemMessage, Message: s, Sender: sender(s.From), Mine: mine,
		Unread: !hasFlag(s.Flags, api.FlagSeen)}
	if s.Issue != nil && s.Issue.Item == api.IssueItemEvent { // jira.IsEvent
		it.Kind, it.Unread = ItemEvent, false
	}
	m.Items = []Item{it}
	m.ScrollTo = 0
	return m
}

// IsMine reports whether the user of account wrote message s, by the rule
// of Item.Mine (the account's address; on an issue the daemon's mine,
// never a comment an integration relayed): the window's Mine for
// SingleModel. The macOS client's Conversation.isMine.
func IsMine(s api.MessageSummary, account api.Account) bool {
	return mine(s, account)
}
