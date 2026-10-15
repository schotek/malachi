// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package conversation

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func draftIDs(cards []DraftCard) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = string(c.Draft.ID)
	}
	return out
}

func TestIsConversationRowCountsDrafts(t *testing.T) {
	cases := []struct {
		count, sent, drafts int
		want                bool
	}{
		{1, 0, 0, false}, {1, 0, 1, true}, {1, 1, 1, true}, {0, 0, 2, true}, {1, 0, -3, false}, {2, 0, 0, true},
	}
	for _, c := range cases {
		th := api.ThreadSummary{MessageCount: c.count, SentCount: c.sent, DraftCount: c.drafts}
		if got := IsConversationRow(th); got != c.want {
			t.Errorf("IsConversationRow(%d, sent %d, drafts %d) = %v, want %v", c.count, c.sent, c.drafts, got, c.want)
		}
	}
}

func TestDrafts(t *testing.T) {
	d := func(id string, min int, text string) api.Draft {
		return api.Draft{ID: api.DraftID(id), AccountID: "a1", Version: 1, UpdatedAt: at(min), TextBody: text, InReplyTo: "m1"}
	}
	in := []api.Draft{
		d("d1", 10, "first"),
		d("d3", 30, "third"),
		d("d2", 30, "second"), // a tie: the greater id first
		d("d1", 40, "repeated"),
		{ID: "", UpdatedAt: at(50)},
		{ID: "dx", AccountID: "a2", UpdatedAt: at(60)}, // another account's
	}
	got := Drafts(in, mailAccount, tr)
	if want := []string{"d3", "d2", "d1"}; !reflect.DeepEqual(draftIDs(got), want) {
		t.Fatalf("drafts = %v, want %v", draftIDs(got), want)
	}
	if got[2].Text != "first" || got[0].Sender != "petr@acme.example" || got[0].Comment || got[0].Internal {
		t.Errorf("card = %+v", got[2])
	}
	named := mailAccount
	named.Config.DisplayName = "  Petr‮ Svoboda\n"
	if c := Drafts(in[:1], named, tr); c[0].Sender != "Petr Svoboda" {
		t.Errorf("sender = %q", c[0].Sender)
	}

	// A comment draft: internal shows the badge, public none.
	comments := []api.Draft{
		{ID: "c1", AccountID: "j1", UpdatedAt: at(1), Comment: &api.DraftComment{Visibility: api.CommentInternal}},
		{ID: "c2", AccountID: "j1", UpdatedAt: at(0), Comment: &api.DraftComment{Visibility: api.CommentPublic}},
	}
	cc := Drafts(comments, jiraAccount, tr)
	if len(cc) != 2 || !cc[0].Comment || !cc[0].Internal || cc[0].InternalLabel != "Internal" || !cc[1].Comment || cc[1].Internal || cc[1].InternalLabel != "" {
		t.Errorf("comment cards = %+v", cc)
	}

	// The cap: the newest api.MaxThreadDrafts.
	var many []api.Draft
	for i := range api.MaxThreadDrafts + 5 {
		many = append(many, d(fmt.Sprintf("d%02d", i), i, "x"))
	}
	if c := Drafts(many, mailAccount, tr); len(c) != api.MaxThreadDrafts || c[0].Draft.ID != api.DraftID(fmt.Sprintf("d%02d", api.MaxThreadDrafts+4)) {
		t.Errorf("capped = %v", draftIDs(c))
	}
	if c := Drafts(nil, mailAccount, tr); len(c) != 0 {
		t.Errorf("none = %v", c)
	}
}

// The text is plain and cleaned: control and format characters go, line
// breaks stay, at most draftTextBytes.
func TestDraftsTextPathological(t *testing.T) {
	text := "Hi‮ there\x00\r\n\r\n\r\n\r\nBye ​<b>not markup</b> end"
	c := Drafts([]api.Draft{{ID: "d", UpdatedAt: at(0), TextBody: text}}, mailAccount, tr)
	if got, want := c[0].Text, "Hi there\n\nBye <b>not markup</b>\nend"; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
	long := strings.Repeat("é", draftTextBytes)
	c = Drafts([]api.Draft{{ID: "d", UpdatedAt: at(0), TextBody: long}}, mailAccount, tr)
	if len(c[0].Text) > draftTextBytes || len(c[0].Text) == 0 {
		t.Errorf("long text: %d bytes", len(c[0].Text))
	}
	bad := "ok\xff\xfe"
	c = Drafts([]api.Draft{{ID: "d", UpdatedAt: at(0), TextBody: bad}}, mailAccount, tr)
	if c[0].Text != "ok" {
		t.Errorf("invalid UTF-8: %q", c[0].Text)
	}
}

func TestSameDrafts(t *testing.T) {
	a := Drafts([]api.Draft{{ID: "d1", Version: 1, UpdatedAt: at(0), TextBody: "x"}}, mailAccount, tr)
	b := Drafts([]api.Draft{{ID: "d1", Version: 1, UpdatedAt: at(0), TextBody: "x"}}, mailAccount, tr)
	if !SameDrafts(a, b) || !SameDrafts(nil, nil) {
		t.Error("the same drafts differ")
	}
	c := Drafts([]api.Draft{{ID: "d1", Version: 2, UpdatedAt: at(1), TextBody: "y"}}, mailAccount, tr)
	if SameDrafts(a, c) || SameDrafts(a, nil) {
		t.Error("other drafts are the same")
	}
}

func TestDraftsFirst(t *testing.T) {
	if DraftsFirst(0) != 1 || DraftsFirst(-1) != 0 {
		t.Error("DraftsFirst")
	}
}

func TestDraftTexts(t *testing.T) {
	if DraftBadge(tr) != "Draft" || DraftEditLabel(tr) != "Edit" || DraftDiscardLabel(tr) != "_Discard" ||
		DiscardQuestion(tr) != "Discard this message?" || DraftSaved("10:00", tr) != "Draft saved 10:00" {
		t.Error("draft texts")
	}
}
