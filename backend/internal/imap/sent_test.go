// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// queueSent seeds a draft, turns it into an outbox message with the given
// Message-ID local part and date, and marks it delivered (state sent).
func (h *harness) queueSent(id string, at time.Time) store.Message {
	h.t.Helper()
	ctx := context.Background()
	d := store.Draft{AccountID: h.acc.ID, Subject: "Sent " + id, To: []api.Address{{Address: "bob@example.test"}}, TextBody: "my text"}
	if err := h.st.SaveDraft(ctx, &d, nil); err != nil {
		h.t.Fatal(err)
	}
	raw := fmt.Sprintf("From: me@example.test\r\nTo: bob@example.test\r\nSubject: Sent %s\r\nDate: %s\r\nMessage-ID: <%s@example.test>\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nmy text\r\n",
		id, at.Format(time.RFC1123Z), id)
	m, err := h.st.EnqueueOutbox(ctx, store.EnqueueInput{
		DraftID: d.ID, DraftVersion: d.Version,
		Message: store.Message{
			AccountID:    h.acc.ID,
			From:         []api.Address{{Address: "me@example.test"}},
			To:           []api.Address{{Address: "bob@example.test"}},
			Subject:      "Sent " + id,
			Date:         at,
			RFCMessageID: id + "@example.test",
			Snippet:      "my text",
		},
		Text:         "my text",
		EnvelopeFrom: "me@example.test",
		Recipients:   []string{"bob@example.test"},
		Build: func(w io.Writer) error {
			_, err := io.WriteString(w, raw)
			return err
		},
	})
	if err != nil {
		h.t.Fatal(err)
	}
	if err := h.st.MarkOutboxSending(ctx, m.ID); err != nil {
		h.t.Fatal(err)
	}
	if err := h.st.MarkOutboxSent(ctx, m.ID); err != nil {
		h.t.Fatal(err)
	}
	return m
}

// serverInternalDate reads the INTERNALDATE of one message.
func (h *harness) serverInternalDate(mailbox string, uid uint32) time.Time {
	h.t.Helper()
	c := h.client()
	defer func() { c.Logout().Wait() }()
	if _, err := c.Select(mailbox, nil).Wait(); err != nil {
		h.t.Fatal(err)
	}
	msgs, err := c.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{InternalDate: true}).Collect()
	if err != nil {
		h.t.Fatal(err)
	}
	if len(msgs) == 0 {
		h.t.Fatalf("no message %d in %s", uid, mailbox)
	}
	return msgs[0].InternalDate
}

// serverMailboxes lists the mailbox names on the server.
func (h *harness) serverMailboxes() []string {
	h.t.Helper()
	c := h.client()
	defer func() { c.Logout().Wait() }()
	list, err := c.List("", "*", nil).Collect()
	if err != nil {
		h.t.Fatal(err)
	}
	var out []string
	for _, ld := range list {
		out = append(out, ld.Mailbox)
	}
	return out
}

// outboxMessages lists the local outbox pseudo-folder.
func (h *harness) outboxMessages() []store.Message {
	h.t.Helper()
	f, err := h.st.OutboxFolder(context.Background(), h.acc.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	return h.messages(f.ID)
}

// clock is a settable time source for the syncer.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// TestSentCopyAppended covers the happy path on an incremental pass: the
// delivered message is uploaded to Sent with \Seen and its date, the local
// outbox copy goes away and the Sent folder's pass fetches the copy back —
// silently, since the LIST-STATUS taken before the APPEND is stale.
func TestSentCopyAppended(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	if err := h.user.Create("Sent", nil); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	h.start()
	h.waitIdle(start)
	sent := h.folder("sent")
	if sent.Mailbox != "Sent" || sent.UIDNext == 0 {
		t.Fatalf("sent folder = %+v", sent)
	}

	at := time.Now().Add(-time.Hour).Truncate(time.Second)
	m := h.queueSent("s1", at)
	if got := h.outboxMessages(); len(got) != 1 || got[0].ID != m.ID {
		t.Fatalf("outbox before = %+v", got)
	}
	h.syncer.Trigger(api.FolderID(sent.ID), false)

	waitFor(t, "sent copy fetched locally", func() bool {
		msgs := h.messages(sent.ID)
		return len(msgs) == 1 && msgs[0].RFCMessageID == "s1@example.test" && msgs[0].BodyState == store.BodyFetched
	})
	h.waitStatus(api.SyncIdle)
	uids := h.serverUIDs("Sent")
	if len(uids) != 1 {
		t.Fatalf("server Sent = %v", uids)
	}
	if fl := h.serverFlags("Sent", uids[0]); !hasIMAPFlag(fl, imap.FlagSeen) {
		t.Fatalf("server flags = %v", fl)
	}
	if got := h.serverInternalDate("Sent", uids[0]); got.Unix() != at.Unix() {
		t.Fatalf("internal date = %v, want %v", got, at)
	}
	local := h.messages(sent.ID)[0]
	if local.ID == m.ID || local.UID != uids[0] || !hasFlag(local.Flags, api.FlagSeen) || local.Subject != "Sent s1" {
		t.Fatalf("local copy = %+v", local)
	}
	if _, err := h.st.GetOutbox(context.Background(), h.acc.ID, m.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("outbox entry: %v", err)
	}
	if _, err := h.st.GetMessage(context.Background(), h.acc.ID, m.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("outbox message: %v", err)
	}
	if got := h.outboxMessages(); len(got) != 0 {
		t.Fatalf("outbox after = %+v", got)
	}
	if n := h.notes.newMessages(); len(n) != 0 {
		t.Fatalf("sent copy must not notify, got %+v", n)
	}
}

// TestSentCopyDroppedWithoutSentFolder: an account without a sent folder
// keeps no local copy and uploads nothing.
func TestSentCopyDroppedWithoutSentFolder(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	m := h.queueSent("s2", time.Now().Truncate(time.Second))
	start := time.Now()
	h.start()
	h.waitIdle(start)

	if _, err := h.st.GetOutbox(context.Background(), h.acc.ID, m.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("outbox entry: %v", err)
	}
	if got := h.outboxMessages(); len(got) != 0 {
		t.Fatalf("outbox after = %+v", got)
	}
	if names := h.serverMailboxes(); len(names) != 1 || names[0] != "INBOX" {
		t.Fatalf("server mailboxes = %v", names)
	}
	if _, err := h.st.FolderByRole(context.Background(), h.acc.ID, api.RoleSent); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("sent folder: %v", err)
	}
}

// TestSentCopyRefusedKeepsEntry: the server lists only Sent/Sub, so the
// store's sent folder is the non-existent placeholder "Sent" and APPEND is
// refused with NO. The entry stays sent with one more attempt and a retry
// time; it is not retried before that, and after maxAppendAttempts the
// local copy is dropped.
func TestSentCopyRefusedKeepsEntry(t *testing.T) {
	clk := &clock{t: time.Now()}
	h := newHarness(t, harnessOptions{now: clk.now})
	if err := h.user.Create("Sent/Sub", nil); err != nil {
		t.Fatal(err)
	}
	m := h.queueSent("s3", time.Now().Truncate(time.Second))
	before, err := h.st.GetOutbox(context.Background(), h.acc.ID, m.ID)
	if err != nil || before.Attempts != 1 {
		t.Fatalf("entry before = %+v %v", before, err)
	}
	start := time.Now()
	h.start()
	h.waitIdle(start)

	if f := h.folder("sent"); f.Mailbox != "Sent" || f.Selectable {
		t.Fatalf("sent folder = %+v", f)
	}
	e, err := h.st.GetOutbox(context.Background(), h.acc.ID, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if e.State != store.OutboxSent || e.Attempts != 2 || e.LastError == "" || !e.NextAttemptAt.After(clk.now()) {
		t.Fatalf("entry after refusal = %+v", e)
	}
	if _, err := h.st.GetMessage(context.Background(), h.acc.ID, m.ID); err != nil {
		t.Fatalf("local copy gone: %v", err)
	}
	if got := h.serverUIDs("Sent/Sub"); len(got) != 0 {
		t.Fatalf("server Sent/Sub = %v", got)
	}

	// Not due yet: another pass leaves it alone.
	h.syncer.Trigger("", false)
	h.waitIdle(time.Now())
	if e2, _ := h.st.GetOutbox(context.Background(), h.acc.ID, m.ID); e2.Attempts != 2 {
		t.Fatalf("retried before due: %+v", e2)
	}

	// Every due pass counts one more attempt until the copy is dropped.
	for want := 3; want < maxAppendAttempts; want++ {
		clk.advance(2 * opBackoffMax)
		h.syncer.Trigger("", false)
		waitFor(t, fmt.Sprintf("attempt %d", want), func() bool {
			e, err := h.st.GetOutbox(context.Background(), h.acc.ID, m.ID)
			return err == nil && e.Attempts == want
		})
	}
	clk.advance(2 * opBackoffMax)
	h.syncer.Trigger("", false)
	waitFor(t, "local copy dropped", func() bool {
		_, err := h.st.GetOutbox(context.Background(), h.acc.ID, m.ID)
		return errors.Is(err, store.ErrNotFound)
	})
	if got := h.outboxMessages(); len(got) != 0 {
		t.Fatalf("outbox after drop = %+v", got)
	}
	h.waitStatus(api.SyncIdle)
	if st := h.syncer.State(); st.Error != nil {
		t.Fatalf("refusal must not fail the sync: %+v", st)
	}
}

// TestSentFolderNeverNotifies: a message appearing in Sent from the server
// side is fetched but produces no notify.newMessage, unlike one in INBOX.
func TestSentFolderNeverNotifies(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	if err := h.user.Create("Sent", nil); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	h.start()
	h.waitIdle(start)
	sent, inbox := h.folder("sent"), h.folder("inbox")

	h.append("Sent", rawMessage("srv1", "Sent elsewhere", "body"), time.Now())
	h.append("INBOX", rawMessage("in1", "Incoming", "body"), time.Now())
	h.syncer.Trigger("", false)

	n := h.waitNewMessage()
	if n.FolderID != api.FolderID(inbox.ID) || n.Message.Subject != "Incoming" {
		t.Fatalf("notification = %+v", n)
	}
	waitFor(t, "sent message fetched", func() bool {
		msgs := h.messages(sent.ID)
		return len(msgs) == 1 && msgs[0].BodyState == store.BodyFetched
	})
	h.waitStatus(api.SyncIdle)
	for _, n := range h.notes.newMessages() {
		if n.FolderID != api.FolderID(inbox.ID) {
			t.Fatalf("notification for sent folder: %+v", n)
		}
	}
}
