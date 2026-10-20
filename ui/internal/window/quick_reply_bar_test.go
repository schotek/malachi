// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/conversation"
)

// The quick-reply bar's rules that need no widgets (quick_reply_bar.go,
// conversation_chat_layout.go): the icon-only mode button, ported from the
// macOS client's ConversationReplyTests `quickReplyIconOnly`, and the
// targets of the mode menu's action.

func TestQuickReplyIconOnly(t *testing.T) {
	for _, c := range []struct {
		pane float64
		want bool
	}{
		{1200, false},
		{642, false},
		{641, true},
		{400, true},
		{0, true},
	} {
		if got := convQuickReplyIconOnly(c.pane); got != c.want {
			t.Errorf("convQuickReplyIconOnly(%v) = %v, want %v", c.pane, got, c.want)
		}
		// The bubbles' short dates and the icon-only button switch at once.
		if got := convChatCompactDates(convChatColumn(c.pane)); got != c.want {
			t.Errorf("compact dates at %v = %v", c.pane, got)
		}
	}
}

func TestQuickReplyModeNames(t *testing.T) {
	for _, m := range []conversation.QuickReplyMode{conversation.QuickReply, conversation.QuickReplyAll} {
		got, ok := quickReplyModeOf(quickReplyModeName(m))
		if !ok || got != m {
			t.Errorf("mode %v round trip: %v %v", m, got, ok)
		}
	}
	if quickReplyModeName(conversation.QuickForward) != "" {
		t.Error("Forward… has a qr.mode target")
	}
	for _, bad := range []string{"", "forward", "Reply", "reply-all "} {
		if _, ok := quickReplyModeOf(bad); ok {
			t.Errorf("%q is a mode", bad)
		}
	}
	if got := quickReplyModeNames([]conversation.QuickReplyMode{conversation.QuickReply, conversation.QuickReplyAll, conversation.QuickForward}); got != "reply,reply-all,forward" {
		t.Errorf("names %q", got)
	}
	if quickReplyModeNames(nil) != "" {
		t.Error("no modes have a name")
	}
}

// The conversation controller hands the bar the model, the actions of the
// card the reply goes to (the newest message of someone else, not the
// user's reply in Sent) and its account; nothing while no model is built.
func TestQuickReplyInput(t *testing.T) {
	w := newConvWindow(t, append(convMessages(), convReply("r1", 4, "t1")))
	if _, _, _, ok := w.ctrl.quickReplyInput(); ok {
		t.Error("an input without a conversation")
	}
	if !w.selectRow(w.row("t1", "", false)) {
		t.Fatal("t1 is not shown as a conversation")
	}
	m, actions, account, ok := w.ctrl.quickReplyInput()
	if !ok || m.Thread != "t1" {
		t.Fatalf("input %v for %q", ok, m.Thread)
	}
	target, _ := conversation.QuickReplyTarget(m)
	if target.Message.ID != "a3" {
		t.Errorf("target %q", target.Message.ID)
	}
	if !actions.Reply || !actions.ReplyAll || !actions.Forward || actions.Comment {
		t.Errorf("actions %+v", actions)
	}
	if account.ID != "a" {
		t.Errorf("account %q", account.ID)
	}
	if got := conversation.QuickReplyModes(actions); len(got) != 3 {
		t.Errorf("modes %v", got)
	}
	// An unknown account offers nothing: the bar hides.
	w.accounts = nil
	if _, actions, _, ok := w.ctrl.quickReplyInput(); !ok || actions.Reply {
		t.Errorf("unknown account: %v %+v", ok, actions)
	}
}

// A single message on the pane's page "message" gives the bar its model of
// one member, the actions of its card, its account and whether the page
// has the bar (mailModel.singleQuickReplyInput): not in the Outbox or
// Drafts, a reply to the user's own message in Sent, nothing to answer for
// an event of an issue or an unknown account.
func TestSingleQuickReplyInput(t *testing.T) {
	m := &mailModel{
		accounts: []api.Account{{ID: "a", Enabled: true, Config: api.AccountConfig{Name: "Work", Email: "a@example.invalid"}}},
		folders: map[api.AccountID][]api.Folder{"a": {
			{ID: "in", Role: api.RoleInbox}, {ID: "sent", Role: api.RoleSent},
			{ID: "out", Role: api.RoleOutbox}, {ID: "dr", Role: api.RoleDrafts},
		}},
	}
	msg := func(id, folder, from string) api.MessageSummary {
		return api.MessageSummary{ID: api.MessageID(id), AccountID: "a", FolderID: api.FolderID(folder), ThreadID: "t1",
			From: []api.Address{{Name: from, Address: from + "@example.invalid"}}}
	}

	model, actions, account, shown := m.singleQuickReplyInput(msg("m1", "in", "carol"))
	target, ok := conversation.QuickReplyTarget(model)
	if !shown || !ok || target.Message.ID != "m1" || target.Mine || target.Sender != "carol" || model.Thread != "t1" {
		t.Errorf("inbox: shown %v, target %+v", shown, target)
	}
	if !actions.Reply || !actions.ReplyAll || !actions.Forward || actions.Comment || account.ID != "a" {
		t.Errorf("inbox: actions %+v, account %q", actions, account.ID)
	}

	// The user's own message in Sent: answered all the same, mine.
	model, _, _, shown = m.singleQuickReplyInput(msg("r1", "sent", "a"))
	if target, ok := conversation.QuickReplyTarget(model); !shown || !ok || target.Message.ID != "r1" || !target.Mine {
		t.Errorf("sent: shown %v, target %+v", shown, target)
	}
	// Sent from an alias: mine by the folder.
	if model, _, _, _ := m.singleQuickReplyInput(msg("r2", "sent", "alias")); !model.Items[0].Mine {
		t.Error("a message of Sent is not mine")
	}

	// No bar in the Outbox (by folder or by the daemon's mark) or Drafts.
	for _, s := range []api.MessageSummary{msg("o1", "out", "a"), msg("d1", "dr", "a")} {
		if _, _, _, shown := m.singleQuickReplyInput(s); shown {
			t.Errorf("%s has a bar", s.ID)
		}
	}
	queued := msg("o2", "in", "a")
	queued.Outbox = &api.OutboxInfo{}
	if _, _, _, shown := m.singleQuickReplyInput(queued); shown {
		t.Error("a queued message has a bar")
	}

	// An unknown account offers nothing; no message has no place.
	other := msg("x1", "in", "carol")
	other.AccountID = "gone"
	if _, actions, _, _ := m.singleQuickReplyInput(other); actions.Reply {
		t.Errorf("unknown account: %+v", actions)
	}
	if _, _, _, shown := m.singleQuickReplyInput(api.MessageSummary{}); shown {
		t.Error("no message has a bar")
	}

	// An event of an issue: no target, no actions.
	event := msg("e1", "in", "jira")
	event.Issue = &api.MessageIssue{Item: api.IssueItemEvent}
	model, actions, _, _ = m.singleQuickReplyInput(event)
	if _, ok := conversation.QuickReplyTarget(model); ok || actions.Reply {
		t.Errorf("event: target %v, actions %+v", ok, actions)
	}
}
