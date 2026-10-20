// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestChatRowsSidesAndOrder(t *testing.T) {
	cards := func(mine ...bool) []MessageCard {
		var ms []MessageCard
		for i, m := range mine {
			ms = append(ms, MessageCard{ID: api.MessageID("m" + string(rune('a'+i))), Mine: m})
		}
		return ms
	}
	for _, tc := range []struct {
		name string
		msgs []MessageCard
		want []ChatBubble
	}{
		{"empty", nil, nil},
		{"mixed", cards(false, true, false, true), []ChatBubble{{0, false}, {1, true}, {2, false}, {3, true}}},
		{"all mine", cards(true, true), []ChatBubble{{0, true}, {1, true}}},
		{"all others", cards(false, false, false), []ChatBubble{{0, false}, {1, false}, {2, false}}},
		{"samples", []MessageCard{{From: "Ann"}, {From: "You", Mine: true}}, []ChatBubble{{0, false}, {1, true}}},
	} {
		eq(t, tc.name, ChatRows(tc.msgs), tc.want)
	}
}

func TestChatOpens(t *testing.T) {
	check(t, ChatOpens(MessageCard{ID: "m1"}), "a member with an id opens")
	check(t, ChatOpens(MessageCard{ID: "m1", Mine: true}), "the user's own opens")
	check(t, !ChatOpens(MessageCard{From: "Ann", Text: "sample"}), "a sample opens nothing")
}

func TestChatToggleTexts(t *testing.T) {
	tr := recorder{}
	eq(t, "label off", ChatToggleLabel(false, tr), "Show as Chat")
	eq(t, "label on", ChatToggleLabel(true, tr), "Show as Thread")
	eq(t, "tooltip off", ChatToggleTooltip(false, tr),
		"Others on the left, your messages on the right; double-click opens the message")
	eq(t, "tooltip on", ChatToggleTooltip(true, tr),
		"Back to the cards: the messages as cards, oldest first, the newest open")
}

func TestDetailMessagesSenderAndDate(t *testing.T) {
	ann, me := ago(5), ago(1)
	c := mk("c1", StateYou, withMessages(
		CaseMessage{ID: "m1", From: "Ann​ Example", Date: ann, Text: "hi"},
		CaseMessage{ID: "m2", From: "Me Myself", Date: me, Text: "hello", Mine: true},
	))
	d := view(casesOf(c)).Detail
	check(t, len(d.Messages) == 2, "messages %+v", d.Messages)
	theirs, mine := d.Messages[0], d.Messages[1]
	eq(t, "their from", theirs.From, "Ann Example")
	eq(t, "their sender", theirs.Sender, "Ann Example")
	eq(t, "my from", mine.From, You(recorder{}))
	eq(t, "my sender", mine.Sender, "Me Myself")
	check(t, theirs.Date.Equal(ann) && mine.Date.Equal(me), "dates %v %v", theirs.Date, mine.Date)
	check(t, theirs.When != "" && mine.When != "", "the cards' short dates stay")
	var zero time.Time
	check(t, ChatRows(d.Messages)[1].Right && !ChatRows(d.Messages)[0].Right && theirs.Date != zero, "sides")
}
