// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package outbox

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// enqueueComment queues a comment of an issue-tracker account: no
// recipients, the issue and the visibility on the entry.
func (h *harness) enqueueComment(issueID string, vis api.CommentVisibility) string {
	h.t.Helper()
	ctx := context.Background()
	d := store.Draft{AccountID: h.account.ID, Subject: "ITSD-7: Printer", TextBody: "Replaced the toner.", CommentVisibility: vis}
	if err := h.s.SaveDraft(ctx, &d, nil); err != nil {
		h.t.Fatal(err)
	}
	m, err := h.s.EnqueueOutbox(ctx, store.EnqueueInput{
		DraftID: d.ID, DraftVersion: d.Version,
		Message: store.Message{AccountID: h.account.ID, From: []api.Address{{Name: "Jana Dvořáková", Address: "me@example.invalid"}},
			Subject: d.Subject, Date: fixedNow, RFCMessageID: "c@example.invalid"},
		Text:         d.TextBody,
		EnvelopeFrom: "me@example.invalid",
		Comment:      &store.OutboxComment{IssueID: issueID, Visibility: vis},
		Build: func(w io.Writer) error {
			_, err := io.WriteString(w, "Subject: ITSD-7: Printer\r\n\r\nReplaced the toner.\r\n")
			return err
		},
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return m.ID
}

// entryDeliveries records DeliverEntry calls.
type entryDeliveries struct {
	mu      sync.Mutex
	entries []store.OutboxEntry
	bodies  []string
}

func (d *entryDeliveries) deliver(_ context.Context, e store.OutboxEntry, r io.Reader, size int64) error {
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.entries = append(d.entries, e)
	d.bodies = append(d.bodies, string(body))
	if int64(len(body)) != size {
		return sendErr(api.CodeStorageError, true)
	}
	return nil
}

func (d *entryDeliveries) snapshot() ([]store.OutboxEntry, []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]store.OutboxEntry(nil), d.entries...), append([]string(nil), d.bodies...)
}

// A comment goes through DeliverEntry with where it goes; nothing of the
// mail path runs: no SMTP session, no known sender, no recipient for
// completion, no Sent copy.
func TestWorkerDeliversComments(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	id := h.enqueueComment("20001", api.CommentInternal)
	entries := &entryDeliveries{}
	deps := h.deps()
	deps.DeliverEntry = entries.deliver
	deps.FilesSentCopy = true
	w := NewWorker(h.account, deps)
	wctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); w.Run(wctx) }()
	t.Cleanup(func() { cancel(); <-done })

	waitFor(t, "comment delivered and dropped", func() bool { return h.gone(id) })
	h.waitRawGone(id)
	got, bodies := entries.snapshot()
	if len(got) != 1 || got[0].MessageID != id || got[0].Comment == nil ||
		got[0].Comment.IssueID != "20001" || got[0].Comment.Visibility != api.CommentInternal || len(got[0].Recipients) != 0 {
		t.Fatalf("delivered entries = %+v", got)
	}
	if bodies[0] != "Subject: ITSD-7: Printer\r\n\r\nReplaced the toner.\r\n" {
		t.Errorf("body = %q", bodies[0])
	}
	if n := h.deliver.count(); n != 0 {
		t.Errorf("%d SMTP sessions for a comment", n)
	}
	if senders, _ := h.s.ListKnownSenders(ctx); len(senders) != 0 {
		t.Errorf("known senders after a comment: %+v", senders)
	}
	if got, _ := h.s.SearchCollectedAddresses(ctx, "example", 10); len(got) != 0 {
		t.Errorf("collected addresses after a comment: %+v", got)
	}
	if got := h.rec.triggered(); len(got) != 0 {
		t.Errorf("triggers after a comment: %v", got)
	}
}

// A comment that reaches a mail worker (an account whose kind changed) is
// never mailed: it fails for good and stays in the outbox.
func TestWorkerCommentWithoutEntryDelivery(t *testing.T) {
	h := newHarness(t)
	id := h.enqueueComment("20001", "")
	h.run()
	waitFor(t, "comment failed", func() bool {
		e, err := h.entry(id)
		return err == nil && e.State == store.OutboxFailed
	})
	e, _ := h.entry(id)
	if e.LastErrorCode != api.CodeInvalidArgument {
		t.Errorf("error = %v %q", e.LastErrorCode, e.LastError)
	}
	if n := h.deliver.count(); n != 0 {
		t.Errorf("%d SMTP sessions for a comment", n)
	}
}

// DeliverEntryFor wins over DeliverFor and Deliver where it gives a
// function; nil leaves the account on mail delivery.
func TestSupervisorDeliverEntryFor(t *testing.T) {
	h := newHarness(t)
	entries := &entryDeliveries{}
	useEntries := true
	var mu sync.Mutex
	sv := NewSupervisor(SupervisorDeps{
		Store:      h.s,
		Password:   func(context.Context, string) (string, error) { return "", nil },
		Notifier:   h.rec,
		DeliverFor: func(store.Account) DeliverFunc { return h.deliver.deliver },
		DeliverEntryFor: func(store.Account) DeliverEntryFunc {
			mu.Lock()
			defer mu.Unlock()
			if useEntries {
				return entries.deliver
			}
			return nil
		},
		FilesSentCopyFor: func(store.Account) bool { return true },
		Trigger:          h.rec.onTrigger,
		Changed:          h.rec.onChanged,
	})
	comment := h.enqueueComment("20002", "")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); sv.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	sv.Start(h.account)
	waitFor(t, "comment delivered", func() bool { return h.gone(comment) })
	if got, _ := entries.snapshot(); len(got) != 1 || h.deliver.count() != 0 {
		t.Fatalf("entries %d, SMTP %d", len(got), h.deliver.count())
	}

	mu.Lock()
	useEntries = false
	mu.Unlock()
	sv.Restart(h.account)
	mail := h.enqueue("mail")
	sv.Wake(h.account.ID)
	waitFor(t, "mail delivered", func() bool { return h.gone(mail) })
	if got, _ := entries.snapshot(); len(got) != 1 || h.deliver.count() != 1 {
		t.Fatalf("entries %d, SMTP %d", len(got), h.deliver.count())
	}
}
