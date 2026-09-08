// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/thread"
	"github.com/schotek/malachi/backend/pkg/api"
)

var threadBase = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

// seedThread stores one header-only message; identifiers are bare, as the
// parsers store them. The date follows the UID unless set.
func seedThread(t *testing.T, s *Store, f Folder, m Message) *Message {
	t.Helper()
	m.AccountID, m.FolderID = f.AccountID, f.ID
	if len(m.From) == 0 {
		m.From = []api.Address{{Name: "Alice", Address: "alice@example.invalid"}}
	}
	if m.Subject == "" {
		m.Subject = m.RFCMessageID
	}
	if m.Date.IsZero() {
		m.Date = threadBase.Add(time.Duration(m.UID) * time.Minute)
	}
	if m.Size == 0 {
		m.Size = 10
	}
	if err := s.UpsertMessages(context.Background(), []*Message{&m}); err != nil {
		t.Fatal(err)
	}
	return &m
}

// linked is seedThread with just the threading headers.
func linked(t *testing.T, s *Store, f Folder, uid uint32, id, inReplyTo string, refs ...string) *Message {
	t.Helper()
	return seedThread(t, s, f, Message{UID: uid, RFCMessageID: id, InReplyTo: inReplyTo, References: refs})
}

func threadOf(t *testing.T, s *Store, m *Message) string {
	t.Helper()
	got, err := s.GetMessage(context.Background(), m.AccountID, m.ID)
	if err != nil {
		t.Fatalf("get %s: %v", m.ID, err)
	}
	if got.ThreadID == "" {
		t.Fatalf("%s has no thread id", m.ID)
	}
	return got.ThreadID
}

// sameThread asserts the messages share one local thread and returns it.
func sameThread(t *testing.T, s *Store, msgs ...*Message) string {
	t.Helper()
	tid := threadOf(t, s, msgs[0])
	if !thread.IsLocalID(tid) {
		t.Fatalf("%s: not a local thread id", tid)
	}
	for _, m := range msgs[1:] {
		if got := threadOf(t, s, m); got != tid {
			t.Fatalf("%s is in %s, %s in %s", m.RFCMessageID, got, msgs[0].RFCMessageID, tid)
		}
	}
	return tid
}

func distinctThreads(t *testing.T, s *Store, msgs ...*Message) {
	t.Helper()
	seen := map[string]string{}
	for _, m := range msgs {
		tid := threadOf(t, s, m)
		if other, dup := seen[tid]; dup {
			t.Fatalf("%s and %s share thread %s", other, m.RFCMessageID, tid)
		}
		seen[tid] = m.RFCMessageID
	}
}

func refsCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM message_refs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func withPolicy(t *testing.T, p thread.Policy) {
	t.Helper()
	old := linkPolicy
	linkPolicy = p
	t.Cleanup(func() { linkPolicy = old })
}

func TestUpsertAssignsThreadID(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)

	m := linked(t, s, inbox, 1, "a", "")
	if !thread.IsLocalID(m.ThreadID) || len(m.ThreadID) != len(thread.IDPrefix)+32 {
		t.Fatalf("thread id after insert: %q", m.ThreadID)
	}
	if got := threadOf(t, s, m); got != m.ThreadID {
		t.Fatalf("stored %q, returned %q", got, m.ThreadID)
	}
	// A matched IMAP row keeps its thread id whatever the batch says.
	again := &Message{AccountID: "acc", FolderID: inbox.ID, UID: 1, ThreadID: "t_other", Flags: []api.Flag{api.FlagSeen}}
	if err := s.UpsertMessages(ctx, []*Message{again}); err != nil {
		t.Fatal(err)
	}
	if again.ID != m.ID || again.ThreadID != m.ThreadID {
		t.Fatalf("matched row: id %s thread %q, want %s %q", again.ID, again.ThreadID, m.ID, m.ThreadID)
	}
	// A server id is stored as given and never linked.
	g := seedThread(t, s, inbox, Message{RemoteID: "r1", RFCMessageID: "b", InReplyTo: "a", ThreadID: "conv-1"})
	if g.ThreadID != "conv-1" || threadOf(t, s, g) != "conv-1" {
		t.Fatalf("server thread id: %q", g.ThreadID)
	}
	if threadOf(t, s, m) == "conv-1" {
		t.Fatal("a server id absorbed a local message it never linked")
	}
}

func TestLinkLinearChain(t *testing.T) {
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	a := linked(t, s, inbox, 1, "a", "")
	b := linked(t, s, inbox, 2, "b", "a", "a")
	c := linked(t, s, inbox, 3, "c", "b", "a", "b")
	sameThread(t, s, a, b, c)
	if n := refsCount(t, s); n != 3 {
		t.Errorf("refs = %d, want 3", n)
	}
}

func TestLinkOutOfOrder(t *testing.T) {
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	c := linked(t, s, inbox, 1, "c", "b", "a", "b")
	b := linked(t, s, inbox, 2, "b", "a", "a")
	if threadOf(t, s, c) != threadOf(t, s, b) {
		t.Fatal("reply did not find its earlier child")
	}
	a := linked(t, s, inbox, 3, "a", "")
	sameThread(t, s, a, b, c)
}

func TestLinkParentAfterChildren(t *testing.T) {
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	var replies []*Message
	for i := uint32(1); i <= 5; i++ {
		replies = append(replies, linked(t, s, inbox, i, fmt.Sprintf("r%d", i), "root"))
	}
	distinctThreads(t, s, replies...)
	root := linked(t, s, inbox, 9, "root", "")
	tid := sameThread(t, s, append(replies, root)...)
	rows, err := s.ThreadMessages(context.Background(), "acc", tid, "", 0)
	if err != nil || len(rows) != 6 || rows[0].RFCMessageID != "r1" || rows[5].RFCMessageID != "root" {
		t.Fatalf("members = %d, %v", len(rows), err)
	}
}

func TestLinkMergePartialThreads(t *testing.T) {
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	a := linked(t, s, inbox, 1, "a", "")
	b := linked(t, s, inbox, 2, "b", "a")
	d := linked(t, s, inbox, 3, "d", "c") // c is not stored yet
	e := linked(t, s, inbox, 4, "e", "d")
	x := sameThread(t, s, a, b)
	y := sameThread(t, s, d, e)
	if x == y {
		t.Fatal("unrelated partial threads merged")
	}
	c := linked(t, s, inbox, 5, "c", "a")
	sameThread(t, s, a, b, c, d, e)
}

func TestLinkCycleAndSelfReference(t *testing.T) {
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	a := linked(t, s, inbox, 1, "a", "b", "b")
	b := linked(t, s, inbox, 2, "b", "a", "a")
	c := linked(t, s, inbox, 3, "c", "c", "c")
	sameThread(t, s, a, b)
	if threadOf(t, s, c) == threadOf(t, s, a) {
		t.Fatal("self-referencing message joined strangers")
	}
	if n := refsCount(t, s); n != 3 {
		t.Errorf("refs = %d, want 3", n)
	}
}

func TestLinkReferencesArriveWithBody(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	x := linked(t, s, inbox, 1, "x", "")
	y := linked(t, s, inbox, 2, "y", "") // the envelope carried nothing
	distinctThreads(t, s, x, y)
	if err := s.SetMessageBody(ctx, y.ID, BodyUpdate{Text: "hi", References: []string{"x"}}); err != nil {
		t.Fatal(err)
	}
	sameThread(t, s, x, y)
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM message_refs WHERE message_id = ?`, y.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("refs of y = %d, %v", n, err)
	}
	// A body update without references drops the stale ones.
	if err := s.SetMessageBody(ctx, y.ID, BodyUpdate{Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM message_refs WHERE message_id = ?`, y.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("refs after rewrite = %d, %v", n, err)
	}
	if err := s.SetMessageBody(ctx, "m_missing", BodyUpdate{Text: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestLinkCrossFolderPerFolderView(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	sent := seedFolder(t, s, "acc", "Sent", api.RoleSent)
	a := linked(t, s, inbox, 1, "a", "")
	r := linked(t, s, sent, 5, "r", "a", "a")
	tt := linked(t, s, inbox, 9, "t", "r", "a", "r")
	tid := sameThread(t, s, a, r, tt)

	items, next, total, err := s.ListThreads(ctx, "acc", inbox.ID, "", 0, "", "")
	if err != nil || next != "" || total != 1 || len(items) != 1 {
		t.Fatalf("inbox threads: %d/%d %q %v", len(items), total, next, err)
	}
	got := items[0]
	if got.ID != tid || got.MessageCount != 2 || got.UnreadCount != 2 || got.Latest.ID != tt.ID ||
		!got.Latest.Date.Equal(tt.Date) || fmt.Sprint(got.FolderIDs) != fmt.Sprint(sortedIDs(inbox.ID, sent.ID)) ||
		got.HasAttachments || len(got.Participants) != 1 || got.Participants[0].Address != "alice@example.invalid" {
		t.Errorf("inbox thread = %+v", got)
	}
	items, _, total, err = s.ListThreads(ctx, "acc", sent.ID, "", 0, "", "")
	if err != nil || total != 1 || len(items) != 1 || items[0].MessageCount != 1 || items[0].Latest.ID != r.ID {
		t.Fatalf("sent threads: %+v %d %v", items, total, err)
	}

	members, err := s.ThreadMessages(ctx, "acc", tid, inbox.ID, 0)
	if err != nil || len(members) != 2 || members[0].ID != a.ID || members[1].ID != tt.ID {
		t.Fatalf("inbox members: %v %v", members, err)
	}
	members, err = s.ThreadMessages(ctx, "acc", tid, "", 0)
	if err != nil || len(members) != 3 || members[1].ID != r.ID {
		t.Fatalf("all members: %d %v", len(members), err)
	}
	members, err = s.ThreadMessages(ctx, "acc", tid, "", 2)
	if err != nil || len(members) != 2 || members[0].ID != r.ID || members[1].ID != tt.ID {
		t.Fatalf("capped members: %v %v", members, err)
	}

	row, err := s.GetThread(ctx, "acc", tid, "")
	if err != nil || row.MessageCount != 3 || row.Latest.ID != tt.ID {
		t.Fatalf("account-wide: %+v %v", row, err)
	}
	if _, err := s.GetThread(ctx, "acc", tid, seedFolder(t, s, "acc", "Trash", api.RoleTrash).ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("empty scope: %v", err)
	}
	if _, err := s.GetThread(ctx, "other", tid, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("other account: %v", err)
	}
	if _, err := s.ThreadMessages(ctx, "acc", "t_nope", "", 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown thread: %v", err)
	}
	if _, _, _, err := s.ListThreads(ctx, "acc", "f_nope", "", 0, "", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown folder: %v", err)
	}
}

func sortedIDs(ids ...string) []string {
	out := append([]string(nil), ids...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func TestLinkThreadSizeCap(t *testing.T) {
	withPolicy(t, thread.Policy{MaxThreadSize: 5, MaxLinkRows: 512})
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	root := linked(t, s, inbox, 1, "root", "")
	var replies []*Message
	for i := uint32(2); i <= 6; i++ {
		replies = append(replies, linked(t, s, inbox, i, fmt.Sprintf("r%d", i), "root"))
	}
	sameThread(t, s, root, replies[0], replies[1], replies[2], replies[3])
	if threadOf(t, s, replies[4]) == threadOf(t, s, root) {
		t.Fatal("the sixth member passed the cap")
	}
	items, _, total, err := s.ListThreads(ctx, "acc", inbox.ID, "", 0, "", "")
	if err != nil || total != 2 || len(items) != 2 || items[0].MessageCount != 1 || items[1].MessageCount != 5 {
		t.Fatalf("threads: %+v %d %v", items, total, err)
	}
}

func TestLinkForgedReferences(t *testing.T) {
	withPolicy(t, thread.Policy{MaxThreadSize: 10, MaxLinkRows: 512})
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	var refs []string
	for i := uint32(1); i <= 20; i++ {
		id := fmt.Sprintf("s%d", i)
		linked(t, s, inbox, i, id, "")
		refs = append(refs, id)
	}
	linked(t, s, inbox, 99, "forged", "", refs...)
	items, _, total, err := s.ListThreads(ctx, "acc", inbox.ID, "", 100, "", "")
	if err != nil {
		t.Fatal(err)
	}
	largest := 0
	for _, it := range items {
		largest = max(largest, it.MessageCount)
	}
	if total != 12 || largest != 10 {
		t.Fatalf("threads = %d, largest = %d; want 12 and 10", total, largest)
	}
}

func TestLinkDuplicateMessageID(t *testing.T) {
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	one := seedThread(t, s, inbox, Message{UID: 1, RFCMessageID: "dup", From: []api.Address{{Address: "eve@example.invalid"}}})
	two := seedThread(t, s, inbox, Message{UID: 2, RFCMessageID: "dup", From: []api.Address{{Address: "bob@example.invalid"}}})
	reply := linked(t, s, inbox, 3, "reply", "dup")
	sameThread(t, s, one, two, reply)
	stranger := linked(t, s, inbox, 4, "", "")
	if threadOf(t, s, stranger) == threadOf(t, s, one) {
		t.Fatal("a message without Message-ID joined the twins")
	}
}

func TestLinkMissingMessageID(t *testing.T) {
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	x := linked(t, s, inbox, 1, "", "")
	y := linked(t, s, inbox, 2, "", "")
	distinctThreads(t, s, x, y)
	p := linked(t, s, inbox, 3, "p", "")
	z := linked(t, s, inbox, 4, "", "", "p")
	sameThread(t, s, p, z)
	distinctThreads(t, s, x, y, p)
	if n := refsCount(t, s); n != 1 {
		t.Errorf("refs = %d, want 1", n)
	}
}

func TestLinkServerThreadsUntouched(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	g1 := seedThread(t, s, inbox, Message{RemoteID: "r1", RFCMessageID: "g1", ThreadID: "conv-1"})
	g2 := seedThread(t, s, inbox, Message{RemoteID: "r2", RFCMessageID: "g2", InReplyTo: "g1", ThreadID: "conv-2"})
	if threadOf(t, s, g1) != "conv-1" || threadOf(t, s, g2) != "conv-2" {
		t.Fatal("server threads were merged locally")
	}
	// A local reply to a server-threaded message adopts the conversation.
	local := linked(t, s, inbox, 7, "l", "g2")
	if got := threadOf(t, s, local); got != "conv-2" {
		t.Fatalf("local reply thread = %q, want conv-2", got)
	}
	// And so does a queued reply from the outbox.
	d := seedDraft(t, s, "acc")
	in := enqueueInput(d, "From: me@example.invalid\r\nSubject: queued\r\n\r\nhello")
	in.Message.InReplyTo, in.Message.References = "g1", []string{"g1"}
	queued, err := s.EnqueueOutbox(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if queued.ThreadID != "conv-1" {
		t.Fatalf("queued reply thread = %q, want conv-1", queued.ThreadID)
	}
}

func TestLinkNeverCrossesAccounts(t *testing.T) {
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	other := seedFolder(t, s, "acc2", "INBOX", api.RoleInbox)
	a := linked(t, s, inbox, 1, "a", "")
	b := linked(t, s, other, 1, "b", "a", "a")
	twin := linked(t, s, other, 2, "a", "")
	if threadOf(t, s, a) == threadOf(t, s, b) || threadOf(t, s, a) == threadOf(t, s, twin) {
		t.Fatal("threads crossed accounts")
	}
	sameThread(t, s, b, twin)
}

func TestMessageRefsCascade(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	trash := seedFolder(t, s, "acc", "Trash", api.RoleTrash)
	a := linked(t, s, inbox, 1, "a", "")
	b := linked(t, s, inbox, 2, "b", "a", "a")
	c := linked(t, s, inbox, 3, "c", "b", "a", "b")
	if n := refsCount(t, s); n != 3 {
		t.Fatalf("refs = %d", n)
	}
	if err := s.DeleteMessagesByUID(ctx, inbox.ID, []uint32{3}); err != nil {
		t.Fatal(err)
	}
	if n := refsCount(t, s); n != 1 {
		t.Errorf("after delete: refs = %d, want 1", n)
	}
	if err := s.TrashMessages(ctx, "acc", []string{b.ID}, trash.ID); err != nil {
		t.Fatal(err)
	}
	if n := refsCount(t, s); n != 1 {
		t.Errorf("after trash (a move): refs = %d, want 1", n)
	}
	if got, _ := s.GetMessage(ctx, "acc", b.ID); got.FolderID != trash.ID || got.ThreadID != threadOf(t, s, a) {
		t.Errorf("moved member: %+v", got)
	}
	if err := s.ResetFolder(ctx, trash.ID, 2); err != nil {
		t.Fatal(err)
	}
	if n := refsCount(t, s); n != 0 {
		t.Errorf("after reset: refs = %d, want 0", n)
	}
	_ = c
}

func TestListThreadsCursor(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	var newest string
	for i := uint32(0); i < 40; i++ {
		root := fmt.Sprintf("root%d", i)
		linked(t, s, inbox, 3*i+1, root, "")
		m := linked(t, s, inbox, 3*i+2, fmt.Sprintf("re%d", i), root)
		if i%2 == 0 {
			m = linked(t, s, inbox, 3*i+3, fmt.Sprintf("rere%d", i), root)
		}
		newest = m.ThreadID
	}
	walk := func(sortOrder api.SortOrder, limit int) []ThreadRow {
		var all []ThreadRow
		cursor := ""
		for {
			items, next, total, err := s.ListThreads(ctx, "acc", inbox.ID, cursor, limit, sortOrder, "")
			if err != nil || total != 40 {
				t.Fatalf("%s page: total %d, %v", sortOrder, total, err)
			}
			all = append(all, items...)
			if next == "" {
				return all
			}
			cursor = next
		}
	}
	desc := walk(api.SortDateDesc, 7)
	asc := walk(api.SortDateAsc, 9)
	if len(desc) != 40 || len(asc) != 40 {
		t.Fatalf("walked %d desc, %d asc", len(desc), len(asc))
	}
	seen := map[string]bool{}
	for i, it := range desc {
		if seen[it.ID] {
			t.Fatalf("thread %s listed twice", it.ID)
		}
		seen[it.ID] = true
		if i > 0 && it.Latest.Date.After(desc[i-1].Latest.Date) {
			t.Fatalf("desc order broken at %d", i)
		}
		if asc[39-i].ID != it.ID {
			t.Fatalf("asc is not the reverse of desc at %d", i)
		}
	}
	if desc[0].ID != newest {
		t.Errorf("first thread is not the newest")
	}
	// Cursors are bound to the sort and to thread listings.
	_, next, _, err := s.ListThreads(ctx, "acc", inbox.ID, "", 5, api.SortDateDesc, "")
	if err != nil || next == "" {
		t.Fatal(err)
	}
	if _, _, _, err := s.ListThreads(ctx, "acc", inbox.ID, next, 5, api.SortDateAsc, ""); !errors.Is(err, ErrBadCursor) {
		t.Errorf("cursor of the other sort: %v", err)
	}
	if _, _, _, err := s.ListMessages(ctx, "acc", inbox.ID, next, 5, api.SortDateDesc, api.FilterAll); !errors.Is(err, ErrBadCursor) {
		t.Errorf("thread cursor in message.list: %v", err)
	}
	_, mnext, _, err := s.ListMessages(ctx, "acc", inbox.ID, "", 5, api.SortDateDesc, api.FilterAll)
	if err != nil || mnext == "" {
		t.Fatal(err)
	}
	if _, _, _, err := s.ListThreads(ctx, "acc", inbox.ID, mnext, 5, api.SortDateDesc, ""); !errors.Is(err, ErrBadCursor) {
		t.Errorf("message cursor in thread.list: %v", err)
	}
	if _, _, _, err := s.ListThreads(ctx, "acc", inbox.ID, "", 5, "sideways", ""); err == nil {
		t.Error("bad sort accepted")
	}
	if _, _, _, err := s.ListThreads(ctx, "acc", inbox.ID, "", 5, "", "starred"); err == nil {
		t.Error("bad filter accepted")
	}
}

func TestListThreadsAggregatesAndFilters(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	bob := []api.Address{{Name: "Bob", Address: "bob@example.invalid"}}
	// Thread A: alice (seen) → bob (unread, flagged, attachment) → BOB again (name-less, unread).
	a1 := seedThread(t, s, inbox, Message{UID: 1, RFCMessageID: "a1", Flags: []api.Flag{api.FlagSeen}})
	seedThread(t, s, inbox, Message{UID: 2, RFCMessageID: "a2", InReplyTo: "a1", From: bob,
		Flags: []api.Flag{api.FlagFlagged, "$label1"}, HasAttachments: true})
	a3 := seedThread(t, s, inbox, Message{UID: 3, RFCMessageID: "a3", InReplyTo: "a2",
		From: []api.Address{{Address: "BOB@example.invalid"}}, Subject: "Re: a1", Snippet: "latest"})
	// Thread B: one seen message.
	seedThread(t, s, inbox, Message{UID: 4, RFCMessageID: "b1", Flags: []api.Flag{api.FlagSeen}})
	// Thread C: unread, with a name-only sender.
	seedThread(t, s, inbox, Message{UID: 5, RFCMessageID: "c1", From: []api.Address{{Name: "Carol"}}})

	items, _, total, err := s.ListThreads(ctx, "acc", inbox.ID, "", 0, "", "")
	if err != nil || total != 3 || len(items) != 3 {
		t.Fatalf("all: %d/%d %v", len(items), total, err)
	}
	if items[0].Latest.RFCMessageID != "c1" || items[1].Latest.RFCMessageID != "b1" || items[2].Latest.RFCMessageID != "a3" {
		t.Fatalf("order: %s %s %s", items[0].Latest.RFCMessageID, items[1].Latest.RFCMessageID, items[2].Latest.RFCMessageID)
	}
	a := items[2]
	if a.MessageCount != 3 || a.UnreadCount != 2 || !a.HasAttachments || a.Latest.ID != a3.ID || a.Latest.Snippet != "latest" ||
		fmt.Sprint(a.Flags) != fmt.Sprint([]api.Flag{"$label1", api.FlagFlagged, api.FlagSeen}) {
		t.Errorf("thread A = %+v", a)
	}
	// Participants: newest first, one entry per address, the name from the newest that carries one.
	if len(a.Participants) != 2 || a.Participants[0].Address != "BOB@example.invalid" || a.Participants[0].Name != "Bob" ||
		a.Participants[1].Address != "alice@example.invalid" {
		t.Errorf("participants = %+v", a.Participants)
	}
	if c := items[0]; len(c.Participants) != 1 || c.Participants[0].Name != "Carol" {
		t.Errorf("name-only participant = %+v", c.Participants)
	}

	items, _, total, err = s.ListThreads(ctx, "acc", inbox.ID, "", 0, "", api.FilterUnread)
	if err != nil || total != 2 || len(items) != 2 || items[0].ID != a3.ThreadID && items[1].ID != a3.ThreadID {
		t.Fatalf("unread: %d/%d %v", len(items), total, err)
	}
	items, _, total, err = s.ListThreads(ctx, "acc", inbox.ID, "", 0, "", api.FilterFlagged)
	if err != nil || total != 1 || len(items) != 1 || items[0].ID != a.ID {
		t.Fatalf("flagged: %d/%d %v", len(items), total, err)
	}
	// A newer member moves its thread to the top.
	seedThread(t, s, inbox, Message{UID: 6, RFCMessageID: "a4", InReplyTo: "a1"})
	items, _, _, err = s.ListThreads(ctx, "acc", inbox.ID, "", 0, "", "")
	if err != nil || items[0].ID != a.ID || items[0].MessageCount != 4 {
		t.Fatalf("after reply: %+v %v", items[0], err)
	}
	_ = a1

	// Participants are capped.
	root := seedThread(t, s, inbox, Message{UID: 10, RFCMessageID: "many", From: []api.Address{{Address: "p0@example.invalid"}}})
	for i := 1; i <= 12; i++ {
		seedThread(t, s, inbox, Message{UID: uint32(10 + i), RFCMessageID: fmt.Sprintf("many%d", i), InReplyTo: "many",
			From: []api.Address{{Address: fmt.Sprintf("p%d@example.invalid", i)}}})
	}
	row, err := s.GetThread(ctx, "acc", threadOf(t, s, root), inbox.ID)
	if err != nil || len(row.Participants) != api.MaxThreadParticipants || row.Participants[0].Address != "p12@example.invalid" {
		t.Fatalf("capped participants: %d %v", len(row.Participants), err)
	}
}

func TestListThreadsUsesIndexes(t *testing.T) {
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	linked(t, s, inbox, 1, "a", "")
	rows, err := s.DB().Query(`EXPLAIN QUERY PLAN `+threadGroupQuery(""), inbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "messages_folder_thread") || strings.Contains(joined, "TEMP B-TREE") {
		t.Errorf("plan:\n%s", joined)
	}
}

// The store's partition must be the connected components of the link
// graph whatever the arrival order: the union rule is order-independent.
func TestLinkThreadsMatchComponents(t *testing.T) {
	withPolicy(t, thread.Policy{MaxThreadSize: 0, MaxLinkRows: 100000})
	ctx := context.Background()
	for seed := uint64(0); seed < 40; seed++ {
		rng := rand.New(rand.NewPCG(seed, 17))
		s := openTestStore(t)
		folders := []Folder{seedFolder(t, s, "acc", "INBOX", api.RoleInbox), seedFolder(t, s, "acc", "Sent", api.RoleSent)}
		n := 5 + rng.IntN(26)
		type spec struct {
			rfc, inReplyTo string
			refs           []string
			folder         int
			id             string
		}
		specs := make([]spec, n)
		for i := range specs {
			sp := spec{rfc: fmt.Sprintf("m%d", i), folder: rng.IntN(2)}
			switch r := rng.IntN(10); {
			case r == 0:
				sp.rfc = "" // no Message-ID
			case r <= 2 && i > 0:
				sp.rfc = fmt.Sprintf("m%d", rng.IntN(i)) // a twin
			}
			if rng.IntN(2) == 0 {
				sp.inReplyTo = fmt.Sprintf("m%d", rng.IntN(n+3)) // may not exist
			}
			for k := rng.IntN(4); k > 0; k-- {
				sp.refs = append(sp.refs, fmt.Sprintf("m%d", rng.IntN(n+3)))
			}
			specs[i] = sp
		}
		order := rng.Perm(n)
		uid := uint32(0)
		for len(order) > 0 {
			batch := min(1+rng.IntN(4), len(order))
			var msgs []*Message
			for _, i := range order[:batch] {
				uid++
				sp := specs[i]
				msgs = append(msgs, &Message{AccountID: "acc", FolderID: folders[sp.folder].ID, UID: uid,
					RFCMessageID: sp.rfc, InReplyTo: sp.inReplyTo, References: sp.refs, Subject: sp.rfc,
					Date: threadBase.Add(time.Duration(uid) * time.Minute), Size: 1,
					From: []api.Address{{Address: "a@example.invalid"}}})
			}
			if err := s.UpsertMessages(ctx, msgs); err != nil {
				t.Fatal(err)
			}
			for k, i := range order[:batch] {
				specs[i].id = msgs[k].ID
			}
			order = order[batch:]
		}

		// Union-find over the same links.
		parent := make([]int, n)
		for i := range parent {
			parent[i] = i
		}
		var find func(int) int
		find = func(i int) int {
			for parent[i] != i {
				parent[i] = parent[parent[i]]
				i = parent[i]
			}
			return i
		}
		union := func(a, b int) { parent[find(a)] = find(b) }
		byRFC := map[string][]int{}
		for i, sp := range specs {
			if sp.rfc != "" {
				byRFC[sp.rfc] = append(byRFC[sp.rfc], i)
			}
		}
		for i, sp := range specs {
			for _, ref := range append([]string{sp.inReplyTo}, sp.refs...) {
				for _, j := range byRFC[ref] {
					union(i, j)
				}
			}
			for _, j := range byRFC[sp.rfc] {
				union(i, j)
			}
		}
		tids := make([]string, n)
		for i, sp := range specs {
			m, err := s.GetMessage(ctx, "acc", sp.id)
			if err != nil {
				t.Fatal(err)
			}
			tids[i] = m.ThreadID
		}
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				if (find(i) == find(j)) != (tids[i] == tids[j]) {
					t.Fatalf("seed %d: messages %d (%+v) and %d (%+v): components %v, threads %v",
						seed, i, specs[i], j, specs[j], find(i) == find(j), tids[i] == tids[j])
				}
			}
		}
		s.Close()
	}
}

func FuzzLinkThreads(f *testing.F) {
	f.Add("a", "", "", "b", "a", "a", "c", "b", "a b")
	f.Add("x", "x", "x x", "", "", "", "<y>", "<x>", "x y")
	f.Add("dup", "", "", "dup", "", "", "r", "dup", "")
	f.Fuzz(func(t *testing.T, id1, re1, refs1, id2, re2, refs2, id3, re3, refs3 string) {
		ctx := context.Background()
		s := openTestStore(t)
		inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
		msgs := []*Message{
			{UID: 1, RFCMessageID: id1, InReplyTo: re1, References: strings.Fields(refs1)},
			{UID: 2, RFCMessageID: id2, InReplyTo: re2, References: strings.Fields(refs2)},
			{UID: 3, RFCMessageID: id3, InReplyTo: re3, References: strings.Fields(refs3)},
		}
		for _, m := range msgs {
			m.AccountID, m.FolderID, m.Size = "acc", inbox.ID, 1
			m.Date = threadBase.Add(time.Duration(m.UID) * time.Minute)
			if err := s.UpsertMessages(ctx, []*Message{m}); err != nil {
				t.Fatal(err)
			}
			if !thread.IsLocalID(m.ThreadID) {
				t.Fatalf("thread id %q", m.ThreadID)
			}
		}
		var empty int
		if err := s.DB().QueryRow(`SELECT COUNT(*) FROM message_refs WHERE ref = ''`).Scan(&empty); err != nil || empty != 0 {
			t.Fatalf("empty refs: %d %v", empty, err)
		}
		for _, m := range msgs {
			tid := threadOf(t, s, m)
			members, err := s.ThreadMessages(ctx, "acc", tid, "", 0)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, x := range members {
				found = found || x.ID == m.ID
			}
			if !found {
				t.Fatalf("%s missing from its own thread", m.ID)
			}
		}
		s.Close()
	})
}
