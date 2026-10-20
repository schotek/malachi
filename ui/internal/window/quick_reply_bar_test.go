// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"

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
