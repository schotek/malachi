// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package conversation

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
)

// chatShape is a row of the chat as "kind:id@side" ("more@center" for the
// row of older messages, "draft:<id>@right" for a draft).
func chatShape(rows []ChatRow) []string {
	sides := map[ChatSide]string{ChatLeft: "left", ChatRight: "right", ChatCenter: "center"}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		var s string
		switch r.Kind {
		case ChatBubble:
			s = "msg:" + string(r.Item.Message.ID)
		case ChatEvent:
			s = "event:" + string(r.Item.Message.ID)
		case ChatTruncated:
			s = "more"
		case ChatDraft:
			s = "draft:" + string(r.Draft.Draft.ID)
		}
		out = append(out, s+"@"+sides[r.Side])
	}
	return out
}

func TestChatRowsMail(t *testing.T) {
	own := mail("m2", 10, true)
	own.From = []api.Address{petr}
	// The user's reply in Sent from another identity: not the account's
	// address, so not Mine, and still on the right.
	other := reply("r1", 15)
	other.From = []api.Address{{Name: "Petr", Address: "petr@home.example"}}
	members := []api.MessageSummary{mail("m3", 20, false), own, mail("m1", 0, true)}
	sent := []api.MessageSummary{reply("r2", 30), other}
	m := Build(mailThread(5), members, sent, mailAccount, tr)
	if m.Earlier != 2 {
		t.Fatalf("Earlier = %d, want 2", m.Earlier)
	}
	if it := m.Items[m.Index("r1")]; it.Mine || !it.Sent {
		t.Fatalf("the reply from another identity: Mine %v, Sent %v", it.Mine, it.Sent)
	}
	drafts := Drafts([]api.Draft{
		{ID: "d2", UpdatedAt: at(50), TextBody: "newer"},
		{ID: "d1", UpdatedAt: at(40), TextBody: "older"},
		{ID: "d3", UpdatedAt: at(60), TextBody: "newest"},
	}, mailAccount, tr)
	rows := ChatRows(m, drafts)
	want := []string{
		"more@center",
		"msg:m1@left", "msg:m2@right", "msg:r1@right", "msg:m3@left", "msg:r2@right",
		"draft:d1@right", "draft:d2@right", "draft:d3@right",
	}
	if got := chatShape(rows); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	if rows[0].Item.Text != "2 earlier messages are not shown" {
		t.Errorf("the row of older messages = %q", rows[0].Item.Text)
	}
	if !reflect.DeepEqual(rows[2].Item, m.Items[m.Index("m2")]) || rows[6].Draft.Text != "older" {
		t.Errorf("rows carry other items: %+v, %+v", rows[2].Item, rows[6].Draft)
	}
	if !reflect.DeepEqual(rows[7].Item, Item{}) || !reflect.DeepEqual(rows[1].Draft, DraftCard{}) {
		t.Error("a draft row carries an item, or a bubble a draft")
	}
	if got := ChatScrollTo(rows); got != len(rows)-1 || rows[got].Draft.Draft.ID != "d3" {
		t.Errorf("ChatScrollTo = %d, want the newest draft at %d", got, len(rows)-1)
	}

	// Without drafts the chat opens at the newest message.
	rows = ChatRows(m, nil)
	if got := ChatScrollTo(rows); got != len(rows)-1 || rows[got].Item.Message.ID != "r2" {
		t.Errorf("ChatScrollTo without drafts = %d", got)
	}
	// The model is not changed.
	if got := shape(m); !reflect.DeepEqual(got, []string{"more", "msg:m1", "msg:m2", "msg:r1", "msg:m3", "msg:r2"}) {
		t.Errorf("model changed: %v", got)
	}
}

// Drafts saved at the same time keep the order given; the slice given is
// not reordered.
func TestChatRowsDraftOrder(t *testing.T) {
	m := Build(mailThread(1), []api.MessageSummary{mail("m1", 0, true)}, nil, mailAccount, tr)
	drafts := []DraftCard{
		{Draft: api.Draft{ID: "c", UpdatedAt: at(20)}},
		{Draft: api.Draft{ID: "b", UpdatedAt: at(10)}},
		{Draft: api.Draft{ID: "a", UpdatedAt: at(10)}},
		{Draft: api.Draft{ID: "z", UpdatedAt: at(5)}},
	}
	given := draftIDs(drafts)
	rows := ChatRows(m, drafts)
	want := []string{"msg:m1@left", "draft:z@right", "draft:b@right", "draft:a@right", "draft:c@right"}
	if got := chatShape(rows); !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(draftIDs(drafts), given) {
		t.Errorf("the drafts given were reordered: %v", draftIDs(drafts))
	}
}

func TestChatRowsJira(t *testing.T) {
	members := []api.MessageSummary{
		issueMsg("d", 0, true, api.MessageIssue{Item: api.IssueItemDescription, Mine: true}),
		issueMsg("c1", 10, true, api.MessageIssue{Item: api.IssueItemComment}),
		statusEvent("e1", 20, "To Do", "In Progress"),
		issueMsg("c2", 30, true, api.MessageIssue{Item: api.IssueItemComment, Mine: true}),
		// The site says it is the user's, but an integration posted it.
		issueMsg("c3", 40, false, api.MessageIssue{Item: api.IssueItemComment, Mine: true, Via: "Issue Sync"}),
		statusEvent("e2", 50, "In Progress", "Done"),
	}
	m := Build(jiraThread(7), members, nil, jiraAccount, tr)
	drafts := Drafts([]api.Draft{{ID: "k1", AccountID: "j1", UpdatedAt: at(60), TextBody: "On it",
		Comment: &api.DraftComment{Visibility: api.CommentInternal}}}, jiraAccount, tr)
	rows := ChatRows(m, drafts)
	want := []string{
		"more@center",
		"msg:d@right", "msg:c1@left", "event:e1@center", "msg:c2@right", "msg:c3@left", "event:e2@center",
		"draft:k1@right",
	}
	if got := chatShape(rows); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	if rows[5].Item.Via == "" || rows[5].Item.Mine {
		t.Errorf("the relayed comment: Via %q, Mine %v", rows[5].Item.Via, rows[5].Item.Mine)
	}
	if !rows[7].Draft.Internal || !rows[7].Draft.Comment {
		t.Errorf("the comment draft = %+v", rows[7].Draft)
	}
	if ChatScrollTo(rows) != 7 {
		t.Errorf("ChatScrollTo = %d", ChatScrollTo(rows))
	}
}

func TestChatRowsEmptyAndEventsOnly(t *testing.T) {
	if rows := ChatRows(Model{ScrollTo: -1}, nil); len(rows) != 0 || ChatScrollTo(rows) != -1 {
		t.Errorf("empty model: %v, ChatScrollTo %d", chatShape(rows), ChatScrollTo(rows))
	}
	if ChatScrollTo(nil) != -1 {
		t.Error("ChatScrollTo(nil) != -1")
	}
	// An empty model with older members left out (Remove took the last
	// shown one) has no rows either: the pane loads it again.
	gone := Remove(Build(mailThread(3), []api.MessageSummary{mail("m1", 0, true)}, nil, mailAccount, tr), "m1")
	if rows := ChatRows(gone, nil); len(rows) != 0 || gone.Earlier != 2 {
		t.Errorf("emptied model: %v, Earlier %d", chatShape(rows), gone.Earlier)
	}

	events := Build(jiraThread(2), []api.MessageSummary{
		statusEvent("e2", 10, "In Progress", "Done"),
		statusEvent("e1", 0, "To Do", "In Progress"),
	}, nil, jiraAccount, tr)
	rows := ChatRows(events, nil)
	if got, want := chatShape(rows), []string{"event:e1@center", "event:e2@center"}; !reflect.DeepEqual(got, want) {
		t.Errorf("events only: %v, want %v", got, want)
	}
	if ChatScrollTo(rows) != 1 || len(rows[0].Item.EventLines) == 0 {
		t.Errorf("events only: ChatScrollTo %d, lines %v", ChatScrollTo(rows), rows[0].Item.EventLines)
	}
}

// The text of a bubble is cleaned as a draft card's is; these follow
// board.CleanBlock.
func TestBubbleText(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"plain", "Hello", "Hello"},
		{"CRLF", "one\r\ntwo\r\n", "one\ntwo"},
		{"lone CR", "one\rtwo", "one\ntwo"},
		{"NEL and line separator", "one\u0085two three four", "one\ntwo\nthree\nfour"},
		{"control characters", "a\x00b\x07c\x1bd\x7fe", "abcde"},
		{"bidi and invisible", "Hi" + rlo + " there" + zwsp, "Hi there"},
		{"blank lines", "a\n\n\n\n\nb", "a\n\nb"},
		{"blank lines with spaces", "a\n \t\n  \n\nb  \n", "a\n\nb"},
		{"only whitespace", " \n\t\r\n   \n\n", ""},
		{"markup stays text", "<b>bold</b> & <script>x</script>", "<b>bold</b> & <script>x</script>"},
		{"invalid UTF-8", "ok\xff\xfe!", "ok!"},
	}
	for _, c := range cases {
		if got := BubbleText(c.in); got != c.want {
			t.Errorf("%s: BubbleText(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestBubbleTextCapAndParity(t *testing.T) {
	huge := strings.Repeat("Řádek dlouhé zprávy.\r\n", (1<<20)/24+1)
	if len(huge) < 1<<20 {
		t.Fatalf("the input is only %d bytes", len(huge))
	}
	got := BubbleText(huge)
	if len(got) == 0 || len(got) > draftTextBytes || !utf8.ValidString(got) {
		t.Fatalf("1 MiB in: %d bytes out, valid UTF-8 %v", len(got), utf8.ValidString(got))
	}
	if !strings.HasPrefix(got, "Řádek dlouhé zprávy.\nŘádek") {
		t.Errorf("1 MiB in: starts %q", got[:40])
	}

	// A message bubble and a draft bubble read alike.
	for _, s := range []string{huge, "Hi" + rlo + "\r\n\r\n\r\nBye\x00", " \n ", "plain"} {
		card := Drafts([]api.Draft{{ID: "d", UpdatedAt: at(0), TextBody: s}}, mailAccount, tr)[0]
		if BubbleText(s) != card.Text {
			t.Errorf("bubble and draft differ for %q…", s[:min(len(s), 20)])
		}
	}
}

func TestChatTexts(t *testing.T) {
	if ChatToggleLabel(false, tr) != "Show as Chat" ||
		ChatToggleTooltip(false, tr) != "Others on the left, your messages on the right; double-click opens the message" {
		t.Error("chat texts, off")
	}
	if ChatToggleLabel(true, tr) != "Show as Thread" ||
		ChatToggleTooltip(true, tr) != "Back to the timeline: the messages as cards, newest first" {
		t.Error("chat texts, on")
	}
}

func TestBubbleRendersHTML(t *testing.T) {
	for _, c := range []struct {
		setting, hasHTML, withheld, want bool
	}{
		{false, false, false, false},
		{false, true, false, false},
		{false, true, true, false},
		{false, false, true, false},
		{true, false, false, false},
		{true, true, false, true},
		{true, true, true, false},
		{true, false, true, false},
	} {
		if got := BubbleRendersHTML(c.setting, c.hasHTML, c.withheld); got != c.want {
			t.Errorf("BubbleRendersHTML(%v, %v, %v) = %v, want %v", c.setting, c.hasHTML, c.withheld, got, c.want)
		}
	}
}

func TestChatHTMLRowTexts(t *testing.T) {
	if got := ChatHTMLRowTitle(tr); got != "Render HTML in Chat" {
		t.Errorf("title %q", got)
	}
	if got := ChatHTMLRowSubtitle(tr); got != "Messages in the chat layout as they were sent, in a locked web view, instead of their plain text" {
		t.Errorf("subtitle %q", got)
	}
}
