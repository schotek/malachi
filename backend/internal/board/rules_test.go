// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package board

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

var now = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// hoursAgo is a time h hours before now.
func hoursAgo(h int) time.Time { return now.Add(-time.Duration(h) * time.Hour) }

// me knows bob and carol (the user has written to them), nobody else.
var me = NewIdentity("", []string{"Me@Example.org", "me@work.example"}, []string{"Bob@Example.com", "carol@example.com"})

func addrs(list ...string) []api.Address {
	out := make([]api.Address, 0, len(list))
	for _, a := range list {
		out = append(out, api.Address{Address: a})
	}
	return out
}

// inbound is a classified, not bulk message in the inbox, h hours old.
func inbound(id string, h int, from string, to ...string) Member {
	return Member{
		ID: api.MessageID(id), FolderID: "f_inbox", FolderRole: api.RoleInbox,
		MessageID: "<" + id + "@mail.example>", From: api.Address{Name: "Sender " + id, Address: from},
		To: addrs(to...), Subject: "Lunch", InternalDate: hoursAgo(h), Date: hoursAgo(h),
		StoredAt: hoursAgo(h).Add(time.Minute), Bulk: BulkNone, BodyState: "fetched", Unread: true,
	}
}

// sent is a message of the user's in the sent folder, h hours old.
func sent(id string, h int, text string, to ...string) Member {
	return Member{
		ID: api.MessageID(id), FolderID: "f_sent", FolderRole: api.RoleSent, Mine: true,
		MessageID: "<" + id + "@mail.example>", From: api.Address{Address: "me@example.org"},
		To: addrs(to...), Subject: "Re: Lunch", InternalDate: hoursAgo(h), Date: hoursAgo(h),
		StoredAt: hoursAgo(h).Add(time.Minute), BodyState: "fetched", Text: text,
	}
}

func fixture(t testing.TB, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "board", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func with(m Member, f func(*Member)) Member { f(&m); return m }

type ruleCase struct {
	name    string
	members []Member
	state   api.BoardState
	reason  api.BoardReason
}

func runRules(t *testing.T, cases []ruleCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := Evaluate(Thread{Members: c.members}, me, now)
			if v.State != c.state || v.Reason != c.reason {
				t.Fatalf("got %q/%q, want %q/%q", v.State, v.Reason, c.state, c.reason)
			}
			if (v.State == "") != (v.Reason == "") {
				t.Fatalf("state %q with reason %q", v.State, v.Reason)
			}
			checkTextMembers(t, Thread{Members: c.members}, me, v)
		})
	}
}

// checkTextMembers fails when Evaluate reads the text of a member
// TextMembers does not name: the thread is evaluated without any text, the
// texts of the members that names are put back (as core does), and the
// verdict must be the one with every text.
func checkTextMembers(t *testing.T, th Thread, id Identity, want Verdict) {
	t.Helper()
	bare := Thread{Issue: th.Issue, Members: slices.Clone(th.Members)}
	for i := range bare.Members {
		bare.Members[i].Text, bare.Members[i].OwnText, bare.Members[i].OwnTextSet = "", "", false
	}
	names := Evaluate(bare, id, now).TextMembers()
	if !slices.Equal(names, want.TextMembers()) {
		t.Fatalf("text members depend on text: %v without, %v with", names, want.TextMembers())
	}
	for i, m := range th.Members {
		if slices.Contains(names, m.ID) {
			bare.Members[i] = m
		}
	}
	got := Evaluate(bare, id, now)
	if got.State != want.State || got.Reason != want.Reason || got.Pending != want.Pending {
		t.Fatalf("with the texts of %v only: %q/%q, with every text %q/%q", names, got.State, got.Reason, want.State, want.Reason)
	}
}

func TestTextMembers(t *testing.T) {
	q := fixture(t, "question-own.txt")
	bob := inbound("a", 50, "bob@example.com", "me@example.org")
	cases := []struct {
		name    string
		members []Member
		want    []api.MessageID
	}{
		{"inbound newest", []Member{bob}, nil},
		{"reply to inbound: its forward shape", []Member{bob, sent("b", 2, "Sure.", "bob@example.com")}, []api.MessageID{"b"}},
		{"pending", []Member{with(bob, func(m *Member) { m.Bulk = "" }), sent("b", 2, "Sure.", "bob@example.com")}, nil},
		{"only mine", []Member{sent("a", 3, q, "bob@example.com"), sent("b", 2, "?", "bob@example.com")}, []api.MessageID{"b", "a"}},
	}
	for _, c := range cases {
		if got := Evaluate(Thread{Members: c.members}, me, now).TextMembers(); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	// The ask scan: the question in the 11th newest message of the user's
	// is not read.
	ms := []Member{sent("q", 40, q, "bob@example.com")}
	for i := range maxAskScan {
		ms = append(ms, sent(fmt.Sprintf("n%02d", i), 30-i, "Following up.", "bob@example.com"))
	}
	v := Evaluate(Thread{Members: ms}, me, now)
	if len(v.TextMembers()) != maxAskScan || slices.Contains(v.TextMembers(), "q") || v.State != "" {
		t.Fatalf("ask scan: %v %q", v.TextMembers(), v.State)
	}
	checkTextMembers(t, Thread{Members: ms}, me, v)
	v = Evaluate(Thread{Members: ms[:maxAskScan]}, me, now)
	if v.State != api.BoardThem || !slices.Contains(v.TextMembers(), "q") {
		t.Fatalf("within the scan: %v %q", v.TextMembers(), v.State)
	}
	// A jira thread reads no text.
	jv := Evaluate(Thread{Issue: &Issue{}, Members: []Member{with(sent("c", 2, q, "bob@example.com"), func(m *Member) { m.AuthorID = "me" })}}, NewIdentity("me", nil, nil), now)
	if jv.TextMembers() != nil {
		t.Fatalf("jira: %v", jv.TextMembers())
	}
}

func TestInboundRules(t *testing.T) {
	runRules(t, []ruleCase{
		{"empty", nil, "", ""},
		{"addressed", []Member{inbound("a", 2, "bob@example.com", "me@example.org")}, api.BoardYou, api.BoardReasonYouAddressed},
		{"addressed to an alias, other case", []Member{inbound("a", 2, "bob@example.com", "ME@WORK.EXAMPLE")}, api.BoardYou, api.BoardReasonYouAddressed},
		{"cc only", []Member{with(inbound("a", 2, "bob@example.com", "carol@example.com"), func(m *Member) { m.Cc = addrs("me@example.org") })}, api.BoardInfo, api.BoardReasonInfoCcOnly},
		{"not addressed", []Member{inbound("a", 2, "bob@example.com", "team@example.com")}, api.BoardInfo, api.BoardReasonInfoNotAddressed},
		{"importance high", []Member{with(inbound("a", 2, "bob@example.com", "me@example.org"), func(m *Member) { m.Importance = " High " })}, api.BoardHot, api.BoardReasonHotImportant},
		{"x-priority 1", []Member{with(inbound("a", 2, "bob@example.com", "me@example.org"), func(m *Member) { m.XPriority = "1 (Highest)" })}, api.BoardHot, api.BoardReasonHotImportant},
		{"x-priority 2", []Member{with(inbound("a", 2, "bob@example.com", "me@example.org"), func(m *Member) { m.XPriority = "2" })}, api.BoardHot, api.BoardReasonHotImportant},
		{"x-priority 3", []Member{with(inbound("a", 2, "bob@example.com", "me@example.org"), func(m *Member) { m.XPriority = "3 (Normal)" })}, api.BoardYou, api.BoardReasonYouAddressed},
		{"x-priority 12", []Member{with(inbound("a", 2, "bob@example.com", "me@example.org"), func(m *Member) { m.XPriority = "12" })}, api.BoardYou, api.BoardReasonYouAddressed},
		{"importance low", []Member{with(inbound("a", 2, "bob@example.com", "me@example.org"), func(m *Member) { m.Importance = "low" })}, api.BoardYou, api.BoardReasonYouAddressed},
		{"importance but only cc", []Member{with(inbound("a", 2, "bob@example.com", "x@example.com"), func(m *Member) { m.Importance = "high"; m.Cc = addrs("me@example.org") })}, api.BoardInfo, api.BoardReasonInfoCcOnly},
		{"importance 32 KiB", []Member{with(inbound("a", 2, "bob@example.com", "me@example.org"), func(m *Member) { m.Importance = strings.Repeat("high ", 32<<10/5) })}, api.BoardYou, api.BoardReasonYouAddressed},
		{"importance repeated in one value", []Member{with(inbound("a", 2, "bob@example.com", "me@example.org"), func(m *Member) { m.Importance = "high, high" })}, api.BoardYou, api.BoardReasonYouAddressed},
		{"importance folded", []Member{with(inbound("a", 2, "bob@example.com", "me@example.org"), func(m *Member) { m.Importance = "\r\n high\r\n" })}, api.BoardHot, api.BoardReasonHotImportant},
		{"importance only in quoted text", []Member{with(inbound("a", 2, "bob@example.com", "me@example.org"), func(m *Member) {
			m.Text = "See below.\n\nOn Tue, Alice wrote:\n> Importance: high\n> X-Priority: 1\n"
		})}, api.BoardYou, api.BoardReasonYouAddressed},
		{"flagged older member", []Member{
			with(inbound("a", 5, "bob@example.com", "team@example.com"), func(m *Member) { m.Flagged = true }),
			inbound("b", 2, "carol@example.com", "team@example.com"),
		}, api.BoardHot, api.BoardReasonHotFlagged},
		{"flagged but mine newest", []Member{
			with(inbound("a", 5, "bob@example.com", "me@example.org"), func(m *Member) { m.Flagged = true }),
			sent("b", 2, "Sure.", "bob@example.com"),
		}, api.BoardThem, api.BoardReasonThemReplied},
		{"replied to you", []Member{
			sent("a", 5, "Plan attached.", "team@example.com"),
			with(inbound("b", 2, "bob@example.com", "team@example.com"), func(m *Member) { m.InReplyTo = "<a@mail.example>" }),
		}, api.BoardYou, api.BoardReasonYouRepliedToYou},
		{"replied to you beats addressed", []Member{
			sent("a", 5, "Plan attached.", "bob@example.com"),
			with(inbound("b", 2, "bob@example.com", "me@example.org"), func(m *Member) { m.InReplyTo = " a@mail.example " }),
		}, api.BoardYou, api.BoardReasonYouRepliedToYou},
		{"forged in-reply-to", []Member{
			with(inbound("b", 2, "mallory@example.net", "team@example.com"), func(m *Member) { m.InReplyTo = "<nobody@mail.example>" }),
		}, api.BoardInfo, api.BoardReasonInfoNotAddressed},
		{"empty in-reply-to matches no empty message-id", []Member{
			with(sent("a", 5, "x", "team@example.com"), func(m *Member) { m.MessageID = "" }),
			with(inbound("b", 2, "bob@example.com", "team@example.com"), func(m *Member) { m.InReplyTo = "<>" }),
		}, api.BoardInfo, api.BoardReasonInfoNotAddressed},
		{"spoofed from me to bob in the inbox is not mine", []Member{
			inbound("a", 2, "me@example.org", "bob@example.com"),
		}, api.BoardInfo, api.BoardReasonInfoNotAddressed},
		{"spoofed from me to me is a note, never mine", []Member{
			inbound("a", 2, "me@example.org", "me@example.org"),
		}, api.BoardInfo, api.BoardReasonInfoYourNote},
		{"spoofed from me answering bob is not them", []Member{
			inbound("a", 5, "bob@example.com", "me@example.org"),
			inbound("b", 2, "ME@example.org", "bob@example.com"),
		}, api.BoardInfo, api.BoardReasonInfoNotAddressed},
		{"note from another device to my aliases", []Member{
			with(inbound("a", 2, "me@work.example", "me@example.org"), func(m *Member) { m.Cc = addrs("me@work.example") }),
		}, api.BoardInfo, api.BoardReasonInfoYourNote},
	})
}

func TestMineRules(t *testing.T) {
	q := fixture(t, "question-own.txt")
	runRules(t, []ruleCase{
		{"replied to the sender", []Member{
			inbound("a", 5, "bob@example.com", "me@example.org"),
			sent("b", 2, "Sure, Thursday works.", "bob@example.com"),
		}, api.BoardThem, api.BoardReasonThemReplied},
		{"replied to reply-to", []Member{
			with(inbound("a", 5, "noreply@shop.example", "me@example.org"), func(m *Member) { m.ReplyTo = addrs("Help@Shop.example") }),
			sent("b", 2, "Order 42 is missing.", "help@shop.example"),
		}, api.BoardThem, api.BoardReasonThemReplied},
		{"replied to somebody else", []Member{
			inbound("a", 5, "bob@example.com", "me@example.org"),
			sent("b", 2, "Carol, see below.", "carol@example.com"),
		}, "", ""},
		{"reply forwarded in the thread", []Member{
			inbound("a", 5, "bob@example.com", "me@example.org"),
			with(sent("b", 2, "FYI", "bob@example.com"), func(m *Member) { m.Subject = "Fwd: Lunch" }),
		}, "", ""},
		{"reply with an attached message", []Member{
			inbound("a", 5, "bob@example.com", "me@example.org"),
			with(sent("b", 2, "See attached", "bob@example.com"), func(m *Member) { m.HasMessagePart = true }),
		}, "", ""},
		{"reply quoting below its own text", []Member{
			inbound("a", 5, "bob@example.com", "me@example.org"),
			with(sent("b", 2, fixture(t, "reply-with-quote.txt"), "bob@example.com"), func(m *Member) { m.InReplyTo = "<a@mail.example>" }),
		}, api.BoardThem, api.BoardReasonThemReplied},
		{"short own text above a quote, answering nothing, is a forward", []Member{
			inbound("a", 5, "bob@example.com", "me@example.org"),
			sent("b", 2, fixture(t, "reply-with-quote.txt"), "bob@example.com"),
		}, "", ""},
		{"bulk inbound does not make a reply", []Member{
			with(inbound("a", 5, "news@shop.example", "me@example.org"), func(m *Member) { m.Bulk = string(api.BulkNewsletter) }),
			sent("b", 2, "Unsubscribe me", "news@shop.example"),
		}, "", ""},
		{"asked", []Member{sent("a", 2, q, "bob@example.com")}, api.BoardThem, api.BoardReasonThemAsked},
		{"asked full width", []Member{sent("a", 2, "資料を送っていただけますか？", "bob@example.com")}, api.BoardThem, api.BoardReasonThemAsked},
		{"asked arabic", []Member{sent("a", 2, "هل يمكنك إرسال التقرير؟", "bob@example.com")}, api.BoardThem, api.BoardReasonThemAsked},
		{"statement", []Member{sent("a", 2, "Here are the minutes.", "bob@example.com")}, "", ""},
		{"question only in quoted text", []Member{sent("a", 2, fixture(t, "question-in-quote.txt"), "alice@example.com")}, "", ""},
		{"question only in the signature", []Member{sent("a", 2, fixture(t, "question-in-signature.txt"), "bob@example.com")}, "", ""},
		{"question only in urls and addresses", []Member{sent("a", 2, fixture(t, "question-in-url.txt"), "bob@example.com")}, "", ""},
		{"question to myself", []Member{sent("a", 2, q, "me@work.example")}, "", ""},
		{"question with me only in to and bob in cc", []Member{with(sent("a", 2, q, "me@example.org"), func(m *Member) { m.Cc = addrs("bob@example.com") })}, "", ""},
		{"question, then a nudge", []Member{
			sent("a", 30, q, "bob@example.com"),
			sent("b", 2, "Just following up.", "bob@example.com"),
		}, api.BoardThem, api.BoardReasonThemAsked},
		{"forward subject Fwd", []Member{with(sent("a", 2, q, "bob@example.com"), func(m *Member) { m.Subject = "Fwd: Contract" })}, "", ""},
		{"forward subject FW", []Member{with(sent("a", 2, q, "bob@example.com"), func(m *Member) { m.Subject = "FW: Contract" })}, "", ""},
		{"forward subject WG", []Member{with(sent("a", 2, q, "bob@example.com"), func(m *Member) { m.Subject = "WG: Vertrag" })}, "", ""},
		{"forward subject TR", []Member{with(sent("a", 2, q, "bob@example.com"), func(m *Member) { m.Subject = "TR: Contrat" })}, "", ""},
		{"forward subject RV", []Member{with(sent("a", 2, q, "bob@example.com"), func(m *Member) { m.Subject = "RV: Contrato" })}, "", ""},
		{"forward subject ENC", []Member{with(sent("a", 2, q, "bob@example.com"), func(m *Member) { m.Subject = "ENC: Contrato" })}, "", ""},
		{"forward subject PD", []Member{with(sent("a", 2, q, "bob@example.com"), func(m *Member) { m.Subject = "PD: Umowa" })}, "", ""},
		{"forward under a reply marker", []Member{with(sent("a", 2, q, "bob@example.com"), func(m *Member) { m.Subject = "Re: Fwd[2]: Contract" })}, "", ""},
		{"reply marker is no forward", []Member{with(sent("a", 2, q, "bob@example.com"), func(m *Member) { m.Subject = "Re: Contract" })}, api.BoardThem, api.BoardReasonThemAsked},
		{"forward word inside the subject is no marker", []Member{with(sent("a", 2, q, "bob@example.com"), func(m *Member) { m.Subject = "Fwd tracking: help?" })}, api.BoardThem, api.BoardReasonThemAsked},
		{"forward as attachment", []Member{with(sent("a", 2, q, "bob@example.com"), func(m *Member) { m.HasMessagePart = true })}, "", ""},
		{"forward with gmail separator", []Member{sent("a", 2, fixture(t, "gmail-forward.txt"), "bob@example.com")}, "", ""},
		{"forward with short own text", []Member{sent("a", 2, fixture(t, "outlook-short-forward.txt")+"\nAny news?\n", "bob@example.com")}, "", ""},
		{"apple forward without marker is a miss", []Member{sent("a", 2, fixture(t, "apple-forward.txt"), "bob@example.com")}, api.BoardThem, api.BoardReasonThemAsked},
		{"twin of a sent message in the inbox", []Member{
			sent("a", 2, "note", "me@example.org"),
			with(inbound("b", 2, "me@example.org", "me@example.org"), func(m *Member) { m.MessageID = "<a@mail.example>" }),
		}, "", ""},
		{"outbox counts as mine", []Member{
			inbound("a", 5, "bob@example.com", "me@example.org"),
			with(sent("b", 0, "On my way.", "bob@example.com"), func(m *Member) { m.FolderRole = api.RoleOutbox; m.InternalDate = time.Time{} }),
		}, api.BoardThem, api.BoardReasonThemReplied},
	})
}

func TestRelevantMembers(t *testing.T) {
	bob := inbound("a", 5, "bob@example.com", "me@example.org")
	runRules(t, []ruleCase{
		{"bulk newest leaves the personal message", []Member{bob,
			with(inbound("b", 2, "news@shop.example", "me@example.org"), func(m *Member) { m.Bulk = string(api.BulkNewsletter) }),
		}, api.BoardYou, api.BoardReasonYouAddressed},
		{"every member bulk", []Member{
			with(inbound("a", 5, "list@lists.example", "me@example.org"), func(m *Member) { m.Bulk = string(api.BulkList) }),
			with(inbound("b", 2, "noreply@ci.example", "me@example.org"), func(m *Member) { m.Bulk = string(api.BulkAutomated) }),
		}, "", ""},
		{"unknown bulk value is bulk", []Member{with(bob, func(m *Member) { m.Bulk = "somethingNew" })}, "", ""},
		{"unclassified newest is pending", []Member{bob, with(inbound("b", 2, "carol@example.com", "me@example.org"), func(m *Member) { m.Bulk = "" })}, "", ""},
		{"unclassified older is pending too", []Member{with(bob, func(m *Member) { m.Bulk = ""; m.Flagged = true }), inbound("b", 2, "carol@example.com", "team@example.com")}, "", ""},
		{"unclassified older under the user's reply is pending", []Member{with(bob, func(m *Member) { m.Bulk = "" }), sent("b", 2, "Sure.", "bob@example.com")}, "", ""},
		{"unclassified in the trash does not hold", []Member{with(bob, func(m *Member) { m.Bulk = ""; m.FolderRole = api.RoleTrash }), inbound("b", 2, "carol@example.com", "me@example.org")}, api.BoardYou, api.BoardReasonYouAddressed},
		{"hidden newest", []Member{bob, with(inbound("b", 2, "jira@site.example", "team@example.com"), func(m *Member) { m.Hidden = true })}, api.BoardYou, api.BoardReasonYouAddressed},
		{"trash, junk and drafts", []Member{
			with(bob, func(m *Member) { m.FolderRole = api.RoleTrash }),
			with(inbound("b", 4, "x@example.com", "me@example.org"), func(m *Member) { m.FolderRole = api.RoleJunk }),
			with(sent("c", 2, "Any news?", "bob@example.com"), func(m *Member) { m.FolderRole = api.RoleDrafts }),
		}, "", ""},
		{"virtual copy", []Member{with(bob, func(m *Member) { m.Virtual = true })}, "", ""},
	})

	v := Evaluate(Thread{Members: []Member{bob, with(inbound("b", 2, "carol@example.com", "me@example.org"), func(m *Member) { m.Bulk = "" })}}, me, now)
	if !v.Pending || v.State != "" {
		t.Fatalf("unclassified newest: %+v", v)
	}
	// While mail is being classified again (a new bulk rules version), an
	// older unclassified member keeps the thread pending instead of
	// flipping it: the caller keeps the case as it was.
	v = Evaluate(Thread{Members: []Member{with(bob, func(m *Member) { m.Bulk = "" }), sent("b", 2, "Sure.", "bob@example.com")}}, me, now)
	if !v.Pending || v.State != "" || v.Count != 1 {
		t.Fatalf("unclassified older: %+v", v)
	}
}

func TestVerdictFields(t *testing.T) {
	a := inbound("a", 5, "bob@example.com", "me@example.org")
	archive := with(a, func(m *Member) {
		m.ID = "a2"
		m.FolderID = "f_archive"
		m.FolderRole = api.RoleArchive
		m.Unread = false
	})
	b := with(sent("b", 2, "Sure.", "bob@example.com"), func(m *Member) { m.HasAttachments = true })
	v := Evaluate(Thread{Members: []Member{b, archive, a}}, me, now)
	if v.State != api.BoardThem || v.Count != 2 || v.LatestID != "b" || v.ReplyID != "a" || v.ReplyFolderID != "f_inbox" {
		t.Fatalf("verdict %+v", v)
	}
	if v.Person.Address != "bob@example.com" || !v.Unread || !v.HasAttachments || v.Subject != "Lunch" {
		t.Fatalf("derived %+v", v)
	}
	if !v.Date.Equal(hoursAgo(2)) || !v.NewestInboundAt.Equal(hoursAgo(5)) {
		t.Fatalf("dates %v %v", v.Date, v.NewestInboundAt)
	}
	if len(v.Members) != 2 || v.Members[0].ID != "a" || v.Members[1].ID != "b" {
		t.Fatalf("members %+v", v.Members)
	}
	for _, m := range v.Members {
		if m.Text != "" || m.OwnText != "" {
			t.Fatalf("member text copied: %+v", m)
		}
	}

	// Only the user's: the person is the first recipient not the user's.
	q := Evaluate(Thread{Members: []Member{sent("a", 2, "Lunch?", "me@work.example", "bob@example.com")}}, me, now)
	if q.Person.Address != "bob@example.com" || q.ReplyID != "a" || !q.NewestInboundAt.IsZero() {
		t.Fatalf("asked %+v", q)
	}

	// Hostile strings come out cleaned.
	h := inbound("h", 1, "bob@example.com", "me@example.org")
	h.Subject = "Re: \u202eInvoice\x00 \xff  due\r\n now"
	h.From.Name = "Bob\u200b \u2066Admin\u2069"
	hv := Evaluate(Thread{Members: []Member{h}}, me, now)
	if hv.Subject != "Invoice \ufffd due now" || hv.Person.Name != "Bob Admin" {
		t.Fatalf("cleaned %q %q", hv.Subject, hv.Person.Name)
	}
}

func TestDates(t *testing.T) {
	// A forged Date far in the future without an internal date is held
	// to when the row was stored, so it does not become the newest.
	future := with(inbound("f", 0, "mallory@example.net", "team@example.com"), func(m *Member) {
		m.InternalDate = time.Time{}
		m.Date = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
		m.StoredAt = hoursAgo(10)
	})
	bob := inbound("b", 2, "bob@example.com", "me@example.org")
	v := Evaluate(Thread{Members: []Member{bob, future}}, me, now)
	if v.LatestID != "b" || v.State != api.BoardYou {
		t.Fatalf("future date: %+v", v)
	}
	// Without a stored time, never later than now.
	future.StoredAt = time.Time{}
	if got := Arrival(future, now); !got.Equal(now) {
		t.Fatalf("arrival %v", got)
	}
	old := with(inbound("o", 0, "bob@example.com", "me@example.org"), func(m *Member) {
		m.InternalDate = time.Time{}
		m.Date = time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
	})
	v = Evaluate(Thread{Members: []Member{old}}, me, now)
	if v.State != api.BoardYou || v.Date.Year() != 1900 {
		t.Fatalf("1900: %+v", v)
	}
	// Nothing known at all: the zero time, not a panic.
	if got := Arrival(Member{}, now); !got.IsZero() {
		t.Fatalf("zero arrival %v", got)
	}
}

func TestNewestInbound(t *testing.T) {
	doneAt := hoursAgo(24)
	cases := []struct {
		name string
		m    Member
		want bool
	}{
		{"new inbound", inbound("n", 2, "bob@example.com", "me@example.org"), true},
		{"backfill of old mail", with(inbound("n", 24*90, "bob@example.com", "me@example.org"), func(m *Member) { m.StoredAt = hoursAgo(1) }), false},
		{"moved by another client, arrived before done", with(inbound("n", 30, "bob@example.com", "me@example.org"), func(m *Member) { m.StoredAt = hoursAgo(1) }), true},
		{"forged recent Date, old arrival", with(inbound("n", 24*90, "bob@example.com", "me@example.org"), func(m *Member) { m.Date = hoursAgo(1); m.StoredAt = hoursAgo(1) }), false},
		{"stored before done", inbound("n", 30, "bob@example.com", "me@example.org"), false},
		{"my own reply", sent("n", 2, "ok", "bob@example.com"), false},
		{"bulk", with(inbound("n", 2, "news@x.example", "me@example.org"), func(m *Member) { m.Bulk = "newsletter" }), false},
		{"no stored time", with(inbound("n", 2, "bob@example.com", "me@example.org"), func(m *Member) { m.StoredAt = time.Time{} }), false},
		{"a later copy of the old message", with(inbound("a", 2, "bob@example.com", "me@example.org"), func(m *Member) { m.ID = "a9" }), false},
	}
	old := inbound("a", 48, "bob@example.com", "me@example.org")
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := Evaluate(Thread{Members: []Member{old, c.m}}, me, now)
			stored, at, id := v.NewestInbound(doneAt, nil)
			reopens := stored.After(doneAt) && at.After(doneAt.Add(-24*time.Hour))
			if reopens != c.want {
				t.Fatalf("got %v (%v, %v)", reopens, stored, at)
			}
			if at.IsZero() || id == "" {
				t.Fatalf("no newest inbound: %v %v %q", stored, at, id)
			}
			if reopens && id != c.m.MessageID {
				t.Fatalf("reopened by %q, want %q", id, c.m.MessageID)
			}
		})
	}
	// Without a done time: the newest inbound message's; nothing inbound:
	// zero.
	v := Evaluate(Thread{Members: []Member{old, sent("b", 2, "ok", "bob@example.com")}}, me, now)
	if stored, at, id := v.NewestInbound(time.Time{}, nil); !stored.Equal(old.StoredAt) || !at.Equal(old.InternalDate) || id != old.MessageID {
		t.Fatalf("newest %v %v %q", stored, at, id)
	}
	v = Evaluate(Thread{Members: []Member{sent("b", 2, "Lunch?", "bob@example.com")}}, me, now)
	if stored, at, id := v.NewestInbound(doneAt, nil); !stored.IsZero() || !at.IsZero() || id != "" {
		t.Fatalf("mine only %v %v %q", stored, at, id)
	}
}

// What the case had when it was marked done never reopens it, and a row
// that does not count still says when its message was first stored.
func TestNewestInboundSeenAtDone(t *testing.T) {
	doneAt := hoursAgo(24)
	old := inbound("a", 48, "bob@example.com", "me@example.org")
	// Another client moved b (a member at done) after done: stored anew,
	// arrived before done.
	moved := with(inbound("b", 30, "bob@example.com", "me@example.org"), func(m *Member) { m.StoredAt = hoursAgo(1) })
	seen := func(id string) bool { return id == moved.MessageID }
	v := Evaluate(Thread{Members: []Member{old, moved}}, me, now)
	// Nothing else is new: the newest inbound message's, which the store
	// does not reopen for, as it was seen at done.
	stored, at, id := v.NewestInbound(doneAt, seen)
	if id != moved.MessageID || !seen(id) || !at.Equal(moved.InternalDate) || !stored.Equal(moved.StoredAt) {
		t.Fatalf("seen at done: %v %v %q", stored, at, id)
	}
	// Unseen, the same move reopens (TestNewestInbound).
	if stored, _, id := v.NewestInbound(doneAt, func(string) bool { return false }); !stored.After(doneAt) || id != moved.MessageID {
		t.Fatalf("unseen: %v %q", stored, id)
	}
	// A seen message newer than a new one is passed over: the new one
	// reopens.
	fresh := inbound("c", 20, "bob@example.com", "me@example.org")
	movedLate := with(inbound("b", 10, "bob@example.com", "me@example.org"), func(m *Member) { m.StoredAt = hoursAgo(1) })
	v = Evaluate(Thread{Members: []Member{old, fresh, movedLate}}, me, now)
	if stored, _, id := v.NewestInbound(doneAt, seen); !stored.After(doneAt) || id != fresh.MessageID {
		t.Fatalf("fresh past the seen one: %v %q", stored, id)
	}
	if _, _, id := v.NewestInbound(doneAt, nil); id != movedLate.MessageID {
		t.Fatalf("without seen: %q", id)
	}
	// The newest one seen, the older one not new: the newest's, no reopen.
	v = Evaluate(Thread{Members: []Member{old, moved}}, me, now)
	if _, _, id := v.NewestInbound(doneAt, func(id string) bool { return true }); id != moved.MessageID {
		t.Fatalf("all seen: %q", id)
	}
	// The trash still holds the copy stored before done: not new either.
	trashed := with(moved, func(m *Member) { m.ID = "b0"; m.FolderRole = api.RoleTrash; m.StoredAt = hoursAgo(29) })
	v = Evaluate(Thread{Members: []Member{old, moved, trashed}}, me, now)
	if stored, _, _ := v.NewestInbound(doneAt, nil); !stored.Equal(trashed.StoredAt) {
		t.Fatalf("copy in the trash: %v", stored)
	}
}

func TestLargeThreads(t *testing.T) {
	var ms []Member
	for i := range 500 {
		if i%2 == 0 {
			ms = append(ms, inbound(fmt.Sprintf("in%03d", i), 1000-i, "bob@example.com", "me@example.org"))
		} else {
			ms = append(ms, sent(fmt.Sprintf("out%03d", i), 1000-i, strings.Repeat("Why? ", 2000), "bob@example.com"))
		}
	}
	v := Evaluate(Thread{Members: ms}, me, now)
	if v.State != api.BoardThem || v.Reason != api.BoardReasonThemReplied || v.Count != 500 {
		t.Fatalf("500 members: %s/%s %d", v.State, v.Reason, v.Count)
	}

	to := make([]string, 0, 5000)
	for i := range 4999 {
		to = append(to, fmt.Sprintf("r%d@example.com", i))
	}
	to = append(to, "me@example.org")
	m := inbound("a", 1, "bob@example.com", to...)
	m.Cc = addrs(to...)
	v = Evaluate(Thread{Members: []Member{m}}, me, now)
	if v.State != api.BoardYou {
		t.Fatalf("5000 recipients: %+v", v.State)
	}
	// The user's question to 5000 people, all but the last someone else.
	v = Evaluate(Thread{Members: []Member{sent("q", 1, "Lunch?", to...)}}, me, now)
	if v.Reason != api.BoardReasonThemAsked || v.Person.Address != "r0@example.com" {
		t.Fatalf("5000 recipients asked: %+v", v.Reason)
	}
}

func TestHostileTextInputs(t *testing.T) {
	inputs := map[string]string{
		"invalid utf-8":       "Lunch\xff\xfe?\xc3",
		"nul":                 "Lunch\x00?\x00",
		"bidi":                "\u202eLunch?\u202c \u2066x\u2069",
		"megabyte whitespace": strings.Repeat(" \t\n", 1<<20/3) + "Lunch?",
		"megabyte of lines":   strings.Repeat("\n", 1<<20),
		"megabyte of quotes":  "x\nOn Tue, A wrote:\n" + strings.Repeat("> ?\n", 1<<18),
		"signature only":      "-- \nWhy?",
		"just a question":     "?",
	}
	for name, text := range inputs {
		t.Run(name, func(t *testing.T) {
			m := sent("a", 1, text, "bob@example.com")
			m.Subject = text
			m.To = append(m.To, api.Address{Name: text, Address: text})
			v := Evaluate(Thread{Members: []Member{m}}, me, now)
			if v.State != "" && v.State != api.BoardThem {
				t.Fatalf("state %q", v.State)
			}
			for _, s := range []string{v.Subject, v.Person.Name, v.Person.Address} {
				if strings.ContainsAny(s, "\x00\u202e\u202c\u2066\u2069\n\r\t") || len(s) > maxSubjectBytes {
					t.Fatalf("not cleaned: %q", s)
				}
			}
			_ = MessageExcerpt(text, api.MaxBoardMessageTextBytes)
			_ = QuoteInOwnText(m, "Lunch? Lunch? Lunch?")
		})
	}
}

func TestJira(t *testing.T) {
	const meID = "u-me"
	jme := NewIdentity(meID, []string{"me@example.org"}, nil)
	item := func(id string, h int, kind api.IssueItemKind, author string) Member {
		return Member{
			ID: api.MessageID(id), FolderID: "f_space", FolderRole: api.RoleNone,
			MessageID: "<" + id + ".issue@site.example.malachi.invalid>",
			From:      api.Address{Name: author, Address: author + "@users.jira.invalid"},
			Subject:   "ITSD-42: Printer", InternalDate: hoursAgo(h), StoredAt: hoursAgo(h),
			BodyState: "fetched", IssueKind: kind, AuthorID: author,
		}
	}
	desc := item("i", 50, api.IssueItemDescription, "u-bob")
	theirs := item("c1", 10, api.IssueItemComment, "u-bob")
	mine := item("c2", 5, api.IssueItemComment, meID)
	event := item("h1", 1, api.IssueItemEvent, "u-bob")
	open := Issue{StatusCategory: api.StatusCategoryInProgress}
	cases := []struct {
		name    string
		id      Identity
		issue   Issue
		members []Member
		state   api.BoardState
		reason  api.BoardReason
	}{
		{"user id unknown", NewIdentity("", []string{"me@example.org"}, nil), Issue{AssigneeID: meID}, []Member{desc, theirs}, "", ""},
		{"done category", jme, Issue{StatusCategory: api.StatusCategoryDone, AssigneeID: meID}, []Member{desc, theirs}, "", ""},
		{"closed status", jme, Issue{Closed: true, AssigneeID: meID}, []Member{desc, theirs}, "", ""},
		{"only events", jme, Issue{AssigneeID: meID}, []Member{event}, "", ""},
		{"my comment last, events after it", jme, open, []Member{desc, theirs, mine, event}, api.BoardThem, api.BoardReasonJiraYourComment},
		{"my description only", jme, open, []Member{item("i", 5, api.IssueItemDescription, meID)}, api.BoardThem, api.BoardReasonJiraYourComment},
		{"assigned", jme, Issue{AssigneeID: meID, ReporterID: meID}, []Member{desc, theirs}, api.BoardYou, api.BoardReasonJiraAssigned},
		{"reporter", jme, Issue{ReporterID: meID}, []Member{desc, theirs}, api.BoardYou, api.BoardReasonJiraReporter},
		{"commented before", jme, open, []Member{desc, mine, with(theirs, func(m *Member) { m.InternalDate, m.StoredAt = hoursAgo(1), hoursAgo(1) })}, api.BoardYou, api.BoardReasonJiraCommented},
		{"commented outside the members", jme, Issue{Commented: true, Watching: true}, []Member{desc, theirs}, api.BoardYou, api.BoardReasonJiraCommented},
		{"watching", jme, Issue{Watching: true}, []Member{desc, theirs}, api.BoardInfo, api.BoardReasonJiraWatching},
		{"nothing to do with me", jme, open, []Member{desc, theirs}, "", ""},
		{"virtual copies never count", jme, Issue{Watching: true}, []Member{with(desc, func(m *Member) { m.Virtual = true })}, "", ""},
		{"author named like me by from only", jme, Issue{Watching: true}, []Member{with(theirs, func(m *Member) { m.From.Address = "me@example.org" })}, api.BoardInfo, api.BoardReasonJiraWatching},
		{"comment waiting in the outbox", jme, open, []Member{desc, theirs, with(item("o", 1, api.IssueItemComment, ""), func(m *Member) { m.FolderRole = api.RoleOutbox; m.Mine = true })}, api.BoardThem, api.BoardReasonJiraYourComment},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			is := c.issue
			v := Evaluate(Thread{Members: c.members, Issue: &is}, c.id, now)
			if v.State != c.state || v.Reason != c.reason {
				t.Fatalf("got %q/%q, want %q/%q", v.State, v.Reason, c.state, c.reason)
			}
		})
	}
	// Bulk is never looked at on jira (its rows stay unclassified).
	is := Issue{AssigneeID: meID}
	v := Evaluate(Thread{Members: []Member{desc, theirs}, Issue: &is}, jme, now)
	if v.Pending || v.ReplyID != "c1" || v.Person.Address != "u-bob@users.jira.invalid" || v.Subject != "ITSD-42: Printer" {
		t.Fatalf("jira verdict %+v", v)
	}
}

func TestWindows(t *testing.T) {
	w := api.DefaultBoardPreferences().Windows
	for _, c := range []struct {
		s    api.BoardState
		days int
	}{{api.BoardHot, 90}, {api.BoardYou, 30}, {api.BoardThem, 30}, {api.BoardInfo, 14}, {"bogus", 90}} {
		if got := WindowDays(c.s, w); got != c.days {
			t.Fatalf("%s: %d", c.s, got)
		}
	}
	if WindowDays(api.BoardInfo, api.BoardWindows{Info: 0}) != 14 || WindowDays(api.BoardInfo, api.BoardWindows{Info: 366}) != 14 || WindowDays(api.BoardInfo, api.BoardWindows{Info: 3}) != 3 {
		t.Fatal("out of range windows")
	}
}

func TestIdentity(t *testing.T) {
	var many []string
	for i := range 100 {
		many = append(many, fmt.Sprintf("a%d@example.org", i))
	}
	long := strings.Repeat("x", 400) + "@example.org"
	known := make([]string, 0, MaxKnownCorrespondents+10)
	for i := range MaxKnownCorrespondents + 10 {
		known = append(known, fmt.Sprintf("K%d@example.com", i))
	}
	id := NewIdentity(" u1 ", append([]string{"", "  ", long}, many...), append([]string{"", long}, known...))
	if id.JiraUserID != "u1" || len(id.addresses) != MaxIdentityAddresses || id.Owns("") || id.Owns(long) {
		t.Fatalf("identity %d", len(id.addresses))
	}
	if !id.Owns(" A0@EXAMPLE.ORG ") || id.Owns("a99@example.org") {
		t.Fatal("addresses")
	}
	if len(id.known) != MaxKnownCorrespondents || !id.Knows(" k0@example.COM") || id.Knows(fmt.Sprintf("k%d@example.com", MaxKnownCorrespondents)) || id.Knows("") || id.Knows(long) {
		t.Fatalf("known %d", len(id.known))
	}
	// Known correspondents are not the user's addresses.
	if id.Owns("k0@example.com") || id.Knows("a0@example.org") {
		t.Fatal("sets mixed")
	}
}
