// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package conversation

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/capabilities"
)

// identity is the translator of the tests: every msgid is its own
// translation (the singular for n == 1).
type identity struct{}

func (identity) T(msgid string) string { return msgid }

func (identity) N(msgid, plural string, n int) string {
	if n == 1 {
		return msgid
	}
	return plural
}

func (identity) C(_, msgid string) string { return msgid }

// czech picks the Czech plural forms of the truncated row, as the
// catalogue does (nplurals=3).
type czech struct{ identity }

func (czech) N(msgid, plural string, n int) string {
	if msgid != "%d earlier message is not shown" {
		return identity{}.N(msgid, plural, n)
	}
	switch {
	case n == 1:
		return "%d starší zpráva není zobrazena"
	case n >= 2 && n <= 4:
		return "%d starší zprávy nejsou zobrazeny"
	}
	return "%d starších zpráv není zobrazeno"
}

var tr = identity{}

// Characters the cleaning must drop, as rune constants: the source stays
// free of invisible characters.
var (
	rlo  = string(rune(0x202E)) // RIGHT-TO-LEFT OVERRIDE
	zwsp = string(rune(0x200B)) // ZERO WIDTH SPACE
)

var t0 = time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

func at(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }

var (
	jana = api.Address{Name: "Jana Dvořáková", Address: "jana@acme.example"}
	petr = api.Address{Name: "Petr Svoboda", Address: "petr@acme.example"}
)

// The accounts of the two conversations: Petr's mailbox (the mail of the
// tests is Jana's, written to him) and his account on the Jira site.
var (
	mailAccount = api.Account{
		ID: "a1", Enabled: true,
		Config: api.AccountConfig{Name: "Work", Email: "petr@acme.example"},
	}
	jiraAccount = api.Account{
		ID: "j1", Enabled: true,
		Config:       api.AccountConfig{Name: "Acme Jira", Email: "petr@acme.example", Kind: api.AccountJira},
		Capabilities: []api.AccountCapability{api.CapabilityComment, api.CapabilityForward},
	}
)

// accountOf is the account of a conversation of the tests.
func accountOf(t api.ThreadSummary) api.Account {
	if t.AccountID == jiraAccount.ID {
		return jiraAccount
	}
	return mailAccount
}

// mail is a member of the mail conversation t1 in folder f1.
func mail(id string, min int, seen bool) api.MessageSummary {
	s := api.MessageSummary{
		ID: api.MessageID(id), AccountID: "a1", FolderID: "f1", ThreadID: "t1",
		From: []api.Address{jana}, To: []api.Address{petr},
		Subject: "Re: Quarterly report", Date: at(min), Snippet: "See the figures",
	}
	if seen {
		s.Flags = []api.Flag{api.FlagSeen}
	}
	return s
}

var issue = api.IssueInfo{
	Key: "ITSD-42", URL: "https://acme.atlassian.net/browse/ITSD-42",
	Summary: "Printer on the third floor jams", Status: "In Progress",
	StatusCategory: api.StatusCategoryInProgress, Type: "Incident", Priority: "High",
	Assignee: "Petr Svoboda", Reporter: "Jana Dvořáková",
	CommentVisibilities: []api.CommentVisibility{api.CommentPublic, api.CommentInternal},
}

// issueMsg is a member of the issue ITSD-42 (thread j1 of account j1).
func issueMsg(id string, min int, seen bool, item api.MessageIssue) api.MessageSummary {
	item.IssueInfo = issue
	s := api.MessageSummary{
		ID: api.MessageID(id), AccountID: "j1", FolderID: "space-itsd", ThreadID: "tj",
		From:    []api.Address{{Name: "Jana Dvořáková"}},
		Subject: "ITSD-42: Printer on the third floor jams", Date: at(min), Issue: &item,
	}
	if seen {
		s.Flags = []api.Flag{api.FlagSeen}
	}
	return s
}

func statusEvent(id string, min int, from, to string) api.MessageSummary {
	return issueMsg(id, min, false, api.MessageIssue{
		Item:    api.IssueItemEvent,
		Changes: []api.IssueChange{{Field: api.IssueFieldStatus, From: from, To: to}},
	})
}

func mailThread(count int) api.ThreadSummary {
	return api.ThreadSummary{ID: "t1", AccountID: "a1", Subject: "Quarterly report", MessageCount: count}
}

func jiraThread(count int) api.ThreadSummary {
	info := issue
	return api.ThreadSummary{ID: "tj", AccountID: "j1", MessageCount: count, Issue: &info}
}

// shape is an item as "kind:id" ("more" for the truncated row).
func shape(m Model) []string {
	out := make([]string, 0, len(m.Items))
	for _, it := range m.Items {
		switch it.Kind {
		case ItemMessage:
			out = append(out, "msg:"+string(it.Message.ID))
		case ItemEvent:
			out = append(out, "event:"+string(it.Message.ID))
		case ItemTruncated:
			out = append(out, "more")
		}
	}
	return out
}

func TestIsConversationRow(t *testing.T) {
	for n, want := range map[int]bool{0: false, 1: false, 2: true, 3: true, 612: true} {
		if got := IsConversationRow(api.ThreadSummary{MessageCount: n}); got != want {
			t.Errorf("IsConversationRow(count %d) = %v, want %v", n, got, want)
		}
	}
}

func TestBuild(t *testing.T) {
	queued := mail("m4", 40, false)
	queued.Outbox = &api.OutboxInfo{State: api.OutboxQueued}
	unknownEvent := issueMsg("e9", 50, false, api.MessageIssue{
		Item:    api.IssueItemEvent,
		Changes: []api.IssueChange{{Field: "priority", From: "Low", To: "High"}},
	})
	sameDateB := mail("b", 10, true)
	sameDateA := mail("a", 10, true)
	tests := []struct {
		name     string
		thread   api.ThreadSummary
		members  []api.MessageSummary
		shape    []string
		markRead api.MessageID
		earlier  int
		issueKey string // "" = no issue card
	}{
		{
			name:     "mail thread, newest unread",
			thread:   mailThread(3),
			members:  []api.MessageSummary{mail("m1", 0, true), mail("m2", 10, false), mail("m3", 20, false)},
			shape:    []string{"msg:m1", "msg:m2", "msg:m3"},
			markRead: "m3",
		},
		{
			name:    "all read",
			thread:  mailThread(2),
			members: []api.MessageSummary{mail("m1", 0, true), mail("m2", 10, true)},
			shape:   []string{"msg:m1", "msg:m2"},
		},
		{
			name:    "newest read, an older one unread stays unread",
			thread:  mailThread(3),
			members: []api.MessageSummary{mail("m1", 0, false), mail("m2", 10, false), mail("m3", 20, true)},
			shape:   []string{"msg:m1", "msg:m2", "msg:m3"},
		},
		{
			name:   "jira: description, comments, newest an event",
			thread: jiraThread(4),
			members: []api.MessageSummary{
				issueMsg("d", 0, true, api.MessageIssue{Item: api.IssueItemDescription}),
				issueMsg("c1", 10, false, api.MessageIssue{Item: api.IssueItemComment, Visibility: api.CommentInternal}),
				issueMsg("c2", 20, false, api.MessageIssue{Item: api.IssueItemComment, Visibility: api.CommentPublic}),
				statusEvent("e1", 30, "To Do", "In Progress"),
			},
			shape:    []string{"msg:d", "msg:c1", "msg:c2", "event:e1"},
			markRead: "c2",
			issueKey: "ITSD-42",
		},
		{
			name:   "jira: the newest comment read, events never count",
			thread: jiraThread(3),
			members: []api.MessageSummary{
				issueMsg("c1", 10, false, api.MessageIssue{Item: api.IssueItemComment}),
				issueMsg("c2", 20, true, api.MessageIssue{Item: api.IssueItemComment}),
				statusEvent("e1", 30, "To Do", "Done"),
			},
			shape:    []string{"msg:c1", "msg:c2", "event:e1"},
			issueKey: "ITSD-42",
		},
		{
			name:     "jira: only events",
			thread:   jiraThread(2),
			members:  []api.MessageSummary{statusEvent("e1", 0, "", "To Do"), statusEvent("e2", 10, "To Do", "Done")},
			shape:    []string{"event:e1", "event:e2"},
			issueKey: "ITSD-42",
		},
		{
			name:     "jira: an event of an unknown field is left out",
			thread:   jiraThread(2),
			members:  []api.MessageSummary{issueMsg("d", 0, false, api.MessageIssue{Item: api.IssueItemDescription}), unknownEvent},
			shape:    []string{"msg:d"},
			markRead: "d",
			issueKey: "ITSD-42",
		},
		{
			name:     "a queued outbox member is shown but never marked",
			thread:   mailThread(4),
			members:  []api.MessageSummary{mail("m1", 0, true), mail("m2", 10, false), queued},
			shape:    []string{"msg:m1", "msg:m2", "msg:m4"},
			markRead: "m2",
			earlier:  1,
		},
		{
			name:     "duplicates keep the first, empty ids are dropped",
			thread:   mailThread(2),
			members:  []api.MessageSummary{mail("m1", 0, true), mail("m2", 10, false), mail("m1", 30, false), mail("", 40, false), mail("m2", 50, true)},
			shape:    []string{"msg:m1", "msg:m2"},
			markRead: "m2",
		},
		{
			name:     "unsorted input, equal dates by id",
			thread:   mailThread(4),
			members:  []api.MessageSummary{mail("m3", 20, false), sameDateB, mail("m1", 0, true), sameDateA},
			shape:    []string{"msg:m1", "msg:a", "msg:b", "msg:m3"},
			markRead: "m3",
		},
		{
			name:    "a stale count below the members adds no row",
			thread:  mailThread(1),
			members: []api.MessageSummary{mail("m1", 0, true), mail("m2", 10, true)},
			shape:   []string{"msg:m1", "msg:m2"},
		},
		{
			name:    "nothing to show: empty, no older members",
			thread:  jiraThread(3),
			members: []api.MessageSummary{unknownEvent},
			shape:   []string{},
		},
		{
			name:     "no members: empty, even with an issue",
			thread:   jiraThread(3),
			members:  nil,
			shape:    []string{},
			issueKey: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := Build(tc.thread, tc.members, nil, accountOf(tc.thread), tr)
			wantShape := tc.shape
			if tc.earlier > 0 {
				wantShape = append([]string{"more"}, wantShape...)
			}
			if got := shape(m); !reflect.DeepEqual(got, wantShape) {
				t.Errorf("items = %v, want %v", got, wantShape)
			}
			if m.MarkRead != tc.markRead {
				t.Errorf("MarkRead = %q, want %q", m.MarkRead, tc.markRead)
			}
			if m.Earlier != tc.earlier {
				t.Errorf("Earlier = %d, want %d", m.Earlier, tc.earlier)
			}
			if want := len(wantShape) - 1; m.ScrollTo != want {
				t.Errorf("ScrollTo = %d, want %d", m.ScrollTo, want)
			}
			switch {
			case tc.issueKey == "" && m.Issue != nil:
				t.Errorf("Issue = %+v, want none", *m.Issue)
			case tc.issueKey != "" && (m.Issue == nil || m.Issue.Key != tc.issueKey):
				t.Errorf("Issue = %+v, want key %s", m.Issue, tc.issueKey)
			}
			if m.Thread != tc.thread.ID {
				t.Errorf("Thread = %q, want %q", m.Thread, tc.thread.ID)
			}
			for i, it := range m.Items {
				if it.Kind == ItemEvent && it.Unread {
					t.Errorf("item %d: an event is unread", i)
				}
				if it.Kind == ItemMessage && it.Unread != !hasFlag(it.Message.Flags, api.FlagSeen) {
					t.Errorf("item %d: Unread = %v against flags %v", i, it.Unread, it.Message.Flags)
				}
			}
		})
	}
}

func TestBuildItems(t *testing.T) {
	members := []api.MessageSummary{
		issueMsg("d", 0, true, api.MessageIssue{Item: api.IssueItemDescription}),
		issueMsg("c1", 10, false, api.MessageIssue{
			Item: api.IssueItemComment, Visibility: api.CommentInternal, Via: "Issue Sync", Edited: true,
		}),
		issueMsg("e1", 20, false, api.MessageIssue{
			Item: api.IssueItemEvent,
			Changes: []api.IssueChange{
				{Field: api.IssueFieldStatus, From: "To Do", To: "In Progress"},
				{Field: "priority", From: "Low", To: "High"},
				{Field: api.IssueFieldAssignee, To: "Petr Svoboda"},
			},
		}),
	}
	m := Build(jiraThread(3), members, nil, jiraAccount, tr)
	want := []Item{
		{Kind: ItemMessage, Message: members[0], Sender: "Jana Dvořáková"},
		{
			Kind: ItemMessage, Message: members[1], Sender: "Jana Dvořáková", Unread: true,
			Internal: true, InternalLabel: "Internal", Via: "via Issue Sync", Edited: "Edited",
		},
		{
			Kind: ItemEvent, Message: members[2], Sender: "Jana Dvořáková",
			EventLines: []string{"Status: To Do → In Progress", "Assignee: Unassigned → Petr Svoboda"},
			EventText:  "Status: To Do → In Progress; Assignee: Unassigned → Petr Svoboda",
		},
	}
	if !reflect.DeepEqual(m.Items, want) {
		t.Errorf("items:\n got %+v\nwant %+v", m.Items, want)
	}
	if m.Issue == nil {
		t.Fatal("no issue card")
	}
	c := *m.Issue
	if c.Key != "ITSD-42" || c.Summary != "Printer on the third floor jams" || c.Status != "In Progress" ||
		c.URL != "https://acme.atlassian.net/browse/ITSD-42" {
		t.Errorf("issue card = %+v", c)
	}
	if c.Internal || c.Via != "" || c.Edited != "" {
		t.Errorf("the issue card carries an item's badges: %+v", c)
	}
}

func TestBuildIssueFallsBackToNewestMember(t *testing.T) {
	older := issueMsg("c1", 0, true, api.MessageIssue{Item: api.IssueItemComment})
	newer := issueMsg("c2", 10, true, api.MessageIssue{Item: api.IssueItemComment})
	newer.Issue.Status = "Done"
	thread := jiraThread(2)
	thread.Issue = nil
	m := Build(thread, []api.MessageSummary{newer, older}, nil, jiraAccount, tr)
	if m.Issue == nil || m.Issue.Status != "Done" {
		t.Errorf("Issue = %+v, want the newest member's (status Done)", m.Issue)
	}
	if m := Build(mailThread(2), []api.MessageSummary{mail("m1", 0, true), mail("m2", 1, true)}, nil, mailAccount, tr); m.Issue != nil {
		t.Errorf("mail conversation has an issue card: %+v", *m.Issue)
	}
}

func TestBuildTruncated(t *testing.T) {
	members := make([]api.MessageSummary, 0, api.MaxThreadMessages)
	for i := range api.MaxThreadMessages {
		members = append(members, mail(fmt.Sprintf("m%03d", i), i, i != api.MaxThreadMessages-1))
	}
	m := Build(mailThread(612), members, nil, mailAccount, tr)
	if len(m.Items) != 501 || m.Items[0].Kind != ItemTruncated {
		t.Fatalf("items = %d, first %v; want 501 with the truncated row first", len(m.Items), m.Items[0].Kind)
	}
	if got, want := m.Items[0].Text, "112 earlier messages are not shown"; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(m.Items[0].Message, api.MessageSummary{}) || m.Items[0].Sender != "" {
		t.Errorf("the truncated row carries a message: %+v", m.Items[0])
	}
	if m.Earlier != 112 || m.ScrollTo != 500 || m.MarkRead != "m499" {
		t.Errorf("Earlier %d, ScrollTo %d, MarkRead %q; want 112, 500, m499", m.Earlier, m.ScrollTo, m.MarkRead)
	}
	if m.Index("m000") != 1 || m.Index("m499") != 500 || m.Index("") != -1 || m.Index("m999") != -1 {
		t.Errorf("Index: m000 %d, m499 %d", m.Index("m000"), m.Index("m499"))
	}

	one := Build(mailThread(3), members[:2], nil, mailAccount, tr)
	if got, want := one.Items[0].Text, "1 earlier message is not shown"; got != want {
		t.Errorf("singular text = %q, want %q", got, want)
	}
	for n, want := range map[int]string{
		1: "1 starší zpráva není zobrazena",
		3: "3 starší zprávy nejsou zobrazeny",
		7: "7 starších zpráv není zobrazeno",
	} {
		m := Build(mailThread(2+n), members[:2], nil, mailAccount, czech{})
		if got := m.Items[0].Text; got != want {
			t.Errorf("Czech, %d older: %q, want %q", n, got, want)
		}
	}
}

func TestBuildCapsMembers(t *testing.T) {
	n := api.MaxThreadMessages + 10
	members := make([]api.MessageSummary, 0, n)
	for i := range n {
		members = append(members, mail(fmt.Sprintf("m%03d", i), i, true))
	}
	m := Build(mailThread(n), members, nil, mailAccount, tr)
	if len(m.Items) != api.MaxThreadMessages+1 || m.Earlier != 10 {
		t.Fatalf("items %d, Earlier %d; want %d and 10", len(m.Items), m.Earlier, api.MaxThreadMessages+1)
	}
	if got := m.Items[1].Message.ID; got != "m010" {
		t.Errorf("oldest shown = %s, want m010", got)
	}
	if got := m.Items[0].Text; got != "10 earlier messages are not shown" {
		t.Errorf("text = %q", got)
	}
}

func TestMine(t *testing.T) {
	from := func(id string, min int, list ...api.Address) api.MessageSummary {
		s := mail(id, min, true)
		s.From = list
		return s
	}
	noAddress := mailAccount
	noAddress.Config.Email = "  "
	relayed := api.MessageIssue{Item: api.IssueItemComment, Via: "Issue Sync", Mine: true}
	tests := []struct {
		name    string
		account api.Account
		msg     api.MessageSummary
		want    bool
	}{
		{"mail of the account's address", mailAccount, from("m1", 0, petr), true},
		{"mail of another sender", mailAccount, from("m2", 1, jana), false},
		{"case and surrounding space do not count", mailAccount,
			from("m3", 2, api.Address{Name: "Petr", Address: " \tPetr@ACME.Example\n"}), true},
		{"the address decides, not the name", mailAccount,
			from("m4", 3, api.Address{Name: "Petr Svoboda", Address: "petr@other.example"}), false},
		{"a name that is the address does not count", mailAccount,
			from("m5", 4, api.Address{Name: "petr@acme.example", Address: "jana@acme.example"}), false},
		{"only the first sender counts", mailAccount, from("m6", 5, jana, petr), false},
		{"the first sender without an address", mailAccount, from("m7", 6, api.Address{Name: "Petr Svoboda"}, petr), false},
		{"a longer address that holds the account's", mailAccount,
			from("m8", 7, api.Address{Address: "petr@acme.example.invalid"}), false},
		{"an invisible character makes another address", mailAccount,
			from("m9", 8, api.Address{Address: "petr" + zwsp + "@acme.example"}), false},
		{"missing From", mailAccount, from("m10", 9), false},
		{"an account without an address", noAddress, from("m11", 10, api.Address{Address: "  "}), false},
		{"an account without an address, a sender with one", noAddress, from("m12", 11, petr), false},
		{"jira: the user's comment", jiraAccount,
			issueMsg("c1", 12, true, api.MessageIssue{Item: api.IssueItemComment, Mine: true}), true},
		{"jira: the user's description", jiraAccount,
			issueMsg("d", 13, true, api.MessageIssue{Item: api.IssueItemDescription, Mine: true}), true},
		{"jira: someone else's comment", jiraAccount,
			issueMsg("c2", 14, true, api.MessageIssue{Item: api.IssueItemComment}), false},
		{"jira: a comment relayed by an integration is never the user's", jiraAccount,
			issueMsg("c3", 15, true, relayed), false},
		{"jira: the site decides, not the sender's address", jiraAccount, func() api.MessageSummary {
			s := issueMsg("c4", 16, true, api.MessageIssue{Item: api.IssueItemComment})
			s.From = []api.Address{petr}
			return s
		}(), false},
		{"jira: a change the user made", jiraAccount, func() api.MessageSummary {
			s := statusEvent("e1", 17, "To Do", "Done")
			s.Issue.Mine = true
			return s
		}(), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			other := mail("m0", -10, true)
			if tc.msg.Issue != nil {
				other = issueMsg("c0", -10, true, api.MessageIssue{Item: api.IssueItemComment})
			}
			m := Build(api.ThreadSummary{ID: tc.msg.ThreadID, AccountID: tc.account.ID, MessageCount: 2},
				[]api.MessageSummary{other, tc.msg}, nil, tc.account, tr)
			at := m.Index(tc.msg.ID)
			if at < 0 {
				t.Fatalf("the member is not shown: %v", shape(m))
			}
			if got := m.Items[at].Mine; got != tc.want {
				t.Errorf("Build: Mine = %v, want %v", got, tc.want)
			}
			if m.Items[m.Index(other.ID)].Mine {
				t.Errorf("Build: the other member is the user's")
			}
			// The same member arriving while the conversation is shown.
			merged := Merge(Remove(m, tc.msg.ID), tc.msg, tc.account, tr)
			if got := merged.Items[merged.Index(tc.msg.ID)].Mine; got != tc.want {
				t.Errorf("Merge: Mine = %v, want %v", got, tc.want)
			}
			// The name shown stays the sender's.
			if got, want := m.Items[at].Sender, sender(tc.msg.From); got != want {
				t.Errorf("Sender = %q, want %q", got, want)
			}
		})
	}

	// The truncated row is nobody's.
	cut := Build(mailThread(5), []api.MessageSummary{from("m1", 0, petr), from("m2", 1, petr)}, nil, mailAccount, tr)
	if cut.Items[0].Kind != ItemTruncated || cut.Items[0].Mine || !cut.Items[1].Mine {
		t.Errorf("truncated conversation: %+v", cut.Items)
	}
}

func TestSenderIsCleaned(t *testing.T) {
	hostile := mail("m1", 0, true)
	hostile.From = []api.Address{
		{Name: " " + zwsp + " ", Address: ""},
		{Name: "Jana" + rlo + "\nDvořáková" + zwsp, Address: "jana@acme.example"},
	}
	long := mail("m2", 1, true)
	long.From = []api.Address{{Name: strings.Repeat("ř", 400)}}
	bare := mail("m3", 2, true)
	bare.From = []api.Address{{Address: " petr@acme.example "}}
	none := mail("m4", 3, true)
	none.From = nil
	m := Build(mailThread(4), []api.MessageSummary{hostile, long, bare, none}, nil, mailAccount, tr)
	if got := m.Items[0].Sender; got != "Jana Dvořáková" {
		t.Errorf("hostile sender = %q", got)
	}
	if got := m.Items[1].Sender; len(got) > 512 || !strings.HasPrefix(got, "řř") || strings.ContainsRune(got, 0xFFFD) {
		t.Errorf("long sender: %d bytes", len(got))
	}
	if got := m.Items[2].Sender; got != "petr@acme.example" {
		t.Errorf("bare address = %q", got)
	}
	if got := m.Items[3].Sender; got != "" {
		t.Errorf("no sender = %q", got)
	}
}

func TestMerge(t *testing.T) {
	base := Build(mailThread(3), []api.MessageSummary{mail("m1", 0, true), mail("m2", 10, true), mail("m3", 20, true)}, nil, mailAccount, tr)
	before := shape(base)

	t.Run("newest arrival", func(t *testing.T) {
		m := Merge(base, mail("m4", 30, false), mailAccount, tr)
		if got, want := shape(m), []string{"msg:m1", "msg:m2", "msg:m3", "msg:m4"}; !reflect.DeepEqual(got, want) {
			t.Errorf("items = %v, want %v", got, want)
		}
		if m.MarkRead != "m4" || m.ScrollTo != 3 || m.Index("m4") != 3 {
			t.Errorf("MarkRead %q, ScrollTo %d, Index %d", m.MarkRead, m.ScrollTo, m.Index("m4"))
		}
	})
	t.Run("older arrival goes in its place", func(t *testing.T) {
		m := Merge(base, mail("m2b", 10, false), mailAccount, tr)
		if got, want := shape(m), []string{"msg:m1", "msg:m2", "msg:m2b", "msg:m3"}; !reflect.DeepEqual(got, want) {
			t.Errorf("items = %v, want %v", got, want)
		}
		if m.MarkRead != "" {
			t.Errorf("MarkRead = %q; the newest is read", m.MarkRead)
		}
	})
	t.Run("a shown member is replaced", func(t *testing.T) {
		unread := Merge(base, mail("m3", 20, false), mailAccount, tr)
		if got := shape(unread); !reflect.DeepEqual(got, before) {
			t.Errorf("items = %v, want %v", got, before)
		}
		if !unread.Items[2].Unread || unread.MarkRead != "m3" {
			t.Errorf("replaced member: Unread %v, MarkRead %q", unread.Items[2].Unread, unread.MarkRead)
		}
		moved := Merge(base, mail("m1", 25, true), mailAccount, tr)
		if got, want := shape(moved), []string{"msg:m2", "msg:m3", "msg:m1"}; !reflect.DeepEqual(got, want) {
			t.Errorf("moved: items = %v, want %v", got, want)
		}
	})
	t.Run("another conversation or no id is ignored", func(t *testing.T) {
		other := mail("x1", 30, false)
		other.ThreadID = "t2"
		for _, s := range []api.MessageSummary{other, mail("", 30, false)} {
			if m := Merge(base, s, mailAccount, tr); !reflect.DeepEqual(m, base) {
				t.Errorf("Merge(%q in %q) changed the model: %v", s.ID, s.ThreadID, shape(m))
			}
		}
		unlinked := mail("x2", 30, false)
		unlinked.ThreadID = ""
		if m := Merge(base, unlinked, mailAccount, tr); m.Index("x2") != 3 {
			t.Errorf("a message without a thread id was not taken: %v", shape(m))
		}
	})
	t.Run("the truncated row stays on top", func(t *testing.T) {
		cut := Build(mailThread(5), []api.MessageSummary{mail("m2", 10, true), mail("m3", 20, true)}, nil, mailAccount, tr)
		m := Merge(cut, mail("m0", -10, false), mailAccount, tr)
		if got, want := shape(m), []string{"more", "msg:m0", "msg:m2", "msg:m3"}; !reflect.DeepEqual(got, want) {
			t.Errorf("items = %v, want %v", got, want)
		}
		if m.Items[0].Text != "3 earlier messages are not shown" || m.Earlier != 3 {
			t.Errorf("truncated row %q, Earlier %d", m.Items[0].Text, m.Earlier)
		}
	})
	t.Run("an event refreshes the issue card and is not marked", func(t *testing.T) {
		j := Build(jiraThread(2), []api.MessageSummary{
			issueMsg("d", 0, true, api.MessageIssue{Item: api.IssueItemDescription}),
			issueMsg("c1", 10, false, api.MessageIssue{Item: api.IssueItemComment}),
		}, nil, jiraAccount, tr)
		ev := statusEvent("e1", 20, "In Progress", "Done")
		ev.Issue.Status = "Done"
		ev.Issue.StatusCategory = api.StatusCategoryDone
		m := Merge(j, ev, jiraAccount, tr)
		if m.Issue == nil || m.Issue.Status != "Done" || j.Issue.Status != "In Progress" {
			t.Errorf("issue status: merged %+v, original %+v", m.Issue, j.Issue)
		}
		if m.MarkRead != "c1" || m.ScrollTo != 2 || m.Items[2].Kind != ItemEvent {
			t.Errorf("MarkRead %q, ScrollTo %d, items %v", m.MarkRead, m.ScrollTo, shape(m))
		}
		mailArrival := Merge(j, mail("m9", 30, true), jiraAccount, tr)
		if mailArrival.Issue == nil || mailArrival.Issue.Status != "In Progress" {
			t.Errorf("a member without an issue changed the card: %+v", mailArrival.Issue)
		}
	})
	t.Run("into an empty model", func(t *testing.T) {
		empty := Build(mailThread(0), nil, nil, mailAccount, tr)
		m := Merge(empty, mail("m1", 0, false), mailAccount, tr)
		if got := shape(m); !reflect.DeepEqual(got, []string{"msg:m1"}) || m.MarkRead != "m1" || m.ScrollTo != 0 {
			t.Errorf("items %v, MarkRead %q, ScrollTo %d", got, m.MarkRead, m.ScrollTo)
		}
	})
	if got := shape(base); !reflect.DeepEqual(got, before) || base.Items[2].Unread {
		t.Errorf("Merge modified the model it was given: %v", got)
	}
}

func TestRemove(t *testing.T) {
	base := Build(mailThread(5), []api.MessageSummary{mail("m1", 0, false), mail("m2", 10, false), mail("m3", 20, false)}, nil, mailAccount, tr)
	before := shape(base)

	m := Remove(base, "m3")
	if got, want := shape(m), []string{"more", "msg:m1", "msg:m2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("items = %v, want %v", got, want)
	}
	if m.MarkRead != "m2" || m.ScrollTo != 2 || m.Earlier != 2 {
		t.Errorf("MarkRead %q, ScrollTo %d, Earlier %d", m.MarkRead, m.ScrollTo, m.Earlier)
	}
	if got := shape(Remove(base, "m2")); !reflect.DeepEqual(got, []string{"more", "msg:m1", "msg:m3"}) {
		t.Errorf("middle removed: %v", got)
	}
	if got := Remove(base, "nope"); !reflect.DeepEqual(got, base) {
		t.Errorf("unknown id changed the model: %v", shape(got))
	}
	if got := Remove(base, ""); !reflect.DeepEqual(got, base) {
		t.Errorf("empty id changed the model: %v", shape(got))
	}
	last := Remove(Remove(m, "m1"), "m2")
	if len(last.Items) != 0 || last.Issue != nil || last.MarkRead != "" || last.ScrollTo != -1 {
		t.Errorf("last member removed: %+v", last)
	}
	if last.Earlier != 2 || last.Thread != "t1" {
		t.Errorf("empty model lost Earlier %d or Thread %q", last.Earlier, last.Thread)
	}
	if got := shape(base); !reflect.DeepEqual(got, before) {
		t.Errorf("Remove modified the model it was given: %v", got)
	}

	j := Build(jiraThread(2), []api.MessageSummary{
		issueMsg("c1", 0, false, api.MessageIssue{Item: api.IssueItemComment}),
		statusEvent("e1", 10, "To Do", "Done"),
	}, nil, jiraAccount, tr)
	if m := Remove(j, "e1"); m.Issue == nil || m.MarkRead != "c1" || m.ScrollTo != 0 {
		t.Errorf("event removed: Issue %v, MarkRead %q, ScrollTo %d", m.Issue, m.MarkRead, m.ScrollTo)
	}
	if m := Remove(j, "c1"); m.MarkRead != "" || !reflect.DeepEqual(shape(m), []string{"event:e1"}) {
		t.Errorf("comment removed: MarkRead %q, items %v", m.MarkRead, shape(m))
	}
}

func TestCardActions(t *testing.T) {
	oldMail := api.Account{ID: "a1", Enabled: true}
	jiraM1 := api.Account{ID: "j1", Enabled: true, Config: api.AccountConfig{Kind: api.AccountJira}, Capabilities: []api.AccountCapability{}}
	jiraM2 := api.Account{
		ID: "j1", Enabled: true, Config: api.AccountConfig{Kind: api.AccountJira},
		Capabilities: []api.AccountCapability{api.CapabilityComment, api.CapabilityForward},
	}
	queued := mail("m2", 10, true)
	queued.Outbox = &api.OutboxInfo{State: api.OutboxFailed}
	comment := issueMsg("c1", 0, true, api.MessageIssue{Item: api.IssueItemComment})
	event := statusEvent("e1", 10, "To Do", "Done")
	tests := []struct {
		name    string
		account api.Account
		msg     api.MessageSummary
		compose bool
		want    capabilities.Actions
	}{
		{"mail", oldMail, mail("m1", 0, true), false, capabilities.Actions{Reply: true, ReplyAll: true, Forward: true}},
		{"mail with the full list", api.Account{ID: "a2", Capabilities: api.MailCapabilities}, mail("m1", 0, true), true,
			capabilities.Actions{Reply: true, ReplyAll: true, Forward: true}},
		{"queued mail keeps reply and forward", oldMail, queued, false, capabilities.Actions{Reply: true, ReplyAll: true, Forward: true}},
		{"jira without capabilities", jiraM1, comment, true, capabilities.Actions{}},
		{"jira comment and forward", jiraM2, comment, true, capabilities.Actions{Reply: true, Forward: true, Comment: true}},
		{"jira forward needs a mail account", jiraM2, comment, false, capabilities.Actions{Reply: true, Comment: true}},
		{"an event offers nothing", jiraM2, event, true, capabilities.Actions{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CardActions(tc.account, tc.msg, tc.compose); got != tc.want {
				t.Errorf("CardActions = %+v, want %+v", got, tc.want)
			}
		})
	}
}
