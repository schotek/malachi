// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/fsretry"
	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// setAttachmentDays stores Preferences.AttachmentOfflineDays.
func (m *mailbox) setAttachmentDays(t *testing.T, days int) {
	t.Helper()
	if _, err := m.b.Config().Set(context.Background(), api.ConfigSetParams{Preferences: api.Preferences{
		SyncIntervalSeconds: 300, RemoteContent: api.RemoteBlock, OfflineDays: 0,
		AttachmentOfflineDays: api.Ptr(days),
	}}); err != nil {
		t.Fatal(err)
	}
}

// runStep runs the step's batches until it reports done.
func runStep(t *testing.T, s RawStep) int {
	t.Helper()
	cursor := ""
	for batches := 1; batches <= 20; batches++ {
		next, err := s.Batch(context.Background(), cursor)
		if err != nil {
			t.Fatalf("batch %d: %v", batches, err)
		}
		if next == "" {
			return batches
		}
		cursor = next
	}
	t.Fatal("the step does not finish")
	return 0
}

func TestAttachmentStepKey(t *testing.T) {
	m := seedMailbox(t)
	step := newAttachmentStep(m.b)
	now := time.Date(2026, 9, 27, 23, 30, 0, 0, time.UTC)
	if step.Name() != "attachments" {
		t.Fatalf("name %q", step.Name())
	}
	for _, c := range []struct {
		days int
		want string
	}{{0, "1:0"}, {30, "1:30:2026-09-27"}, {api.AttachmentOfflineNone, "1:-1:2026-09-27"}} {
		m.setAttachmentDays(t, c.days)
		if key, err := step.Key(context.Background(), now); err != nil || key != c.want {
			t.Errorf("key for %d days: %q %v, want %q", c.days, key, err, c.want)
		}
	}
}

// The step reduces what aged past the policy, oldest first and locally
// only; recent mail, Drafts and messages downloaded on demand within their
// grace stay whole, and messages with nothing large are settled without
// being read.
func TestAttachmentStep(t *testing.T) {
	m := seedMailbox(t)
	srv := m.fakeServer()
	ctx := context.Background()
	step := newAttachmentStep(m.b).(*attachmentStep)
	now := time.Now()
	keepAll := ingest.Policy{}

	old := m.seedLarge(t, 7, now.AddDate(0, 0, -60), keepAll)
	recent := m.seedLarge(t, 8, now.AddDate(0, 0, -2), keepAll)
	drafts := m.draftsFolder(t)
	draft := &store.Message{AccountID: string(m.acc), FolderID: drafts.ID, UID: 3, Subject: "Draft",
		InternalDate: now.AddDate(0, 0, -90), RFCMessageID: "report@example.org", Size: 1}
	if err := m.b.store.UpsertMessages(ctx, []*store.Message{draft}); err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Store(ctx, m.b.store, ingest.Request{
		Target: ingest.Target{AccountID: string(m.acc), MessageID: draft.ID, Role: api.RoleDrafts, HasServerCopy: true, InternalDate: draft.InternalDate},
		Body:   bytes.NewReader(largeMessage("report@example.org")), Expect: store.RawExpect{BodyState: store.BodyNone},
	}, nil); err != nil {
		t.Fatal(err)
	}
	// The seeded HTML message was stored the way the daemon did before
	// this pass existed: never evaluated, and nothing in it is large.
	if legacy := m.row(t, string(m.msgs[0])); legacy.StrippableBytes != -1 {
		t.Fatalf("legacy row %+v", legacy)
	}

	// Keeping everything: no pass.
	if n := runStep(t, step); n != 1 {
		t.Fatalf("%d batches under keep-all", n)
	}
	if got := m.row(t, old.ID); got.RawState != store.RawFull {
		t.Fatalf("reduced under keep-all: %+v", got)
	}

	m.setAttachmentDays(t, 30)
	runStep(t, step)
	if got := m.row(t, old.ID); got.RawState != store.RawPartial || got.RemoteBytes == 0 {
		t.Fatalf("old message %+v", got)
	}
	if _, err := m.part(old.ID, "3"); errCode(t, err) != api.CodePartNotDownloaded {
		t.Fatalf("old message's pdf: %v", err)
	}
	if notes, err := m.part(old.ID, "2"); err != nil || string(notes.Data) != "short notes" {
		t.Fatalf("old message's notes: %v", err)
	}
	if text, _, _, _ := m.b.store.GetMessageText(ctx, string(m.acc), old.ID); !strings.Contains(text, "The numbers.") {
		t.Fatalf("old message's text %q", text)
	}
	for _, id := range []string{recent.ID, draft.ID} {
		if got := m.row(t, id); got.RawState != store.RawFull {
			t.Errorf("%s reduced: %+v", got.Subject, got)
		}
	}
	if got := m.row(t, string(m.msgs[0])); got.StrippableBytes != 0 {
		t.Errorf("small message not settled: %d", got.StrippableBytes)
	}
	if n := len(srv.calls()); n != 0 {
		t.Fatalf("the step fetched %d times", n)
	}

	// Downloaded on demand: whole for the grace, then reduced again.
	srv.put("INBOX", 7, largeMessage("report@example.org"))
	if _, err := m.download(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	runStep(t, step)
	if got := m.row(t, old.ID); got.RawState != store.RawFull {
		t.Fatalf("reduced within the grace: %+v", got)
	}
	step.now = func() time.Time { return now.Add(ingest.HydratedKeep + time.Hour) }
	runStep(t, step)
	if got := m.row(t, old.ID); got.RawState != store.RawPartial {
		t.Fatalf("not reduced after the grace: %+v", got)
	}
	if got := m.row(t, recent.ID); got.RawState != store.RawFull {
		t.Fatalf("recent message reduced: %+v", got)
	}

	// Small attachments only: every message but the draft.
	m.setAttachmentDays(t, api.AttachmentOfflineNone)
	runStep(t, step)
	if got := m.row(t, recent.ID); got.RawState != store.RawPartial {
		t.Fatalf("recent message under small-only %+v", got)
	}
	if got := m.row(t, draft.ID); got.RawState != store.RawFull {
		t.Fatalf("draft under small-only %+v", got)
	}

	// Loosening brings nothing back: the step does nothing at all.
	m.setAttachmentDays(t, 0)
	runStep(t, step)
	if got := m.row(t, recent.ID); got.RawState != store.RawPartial {
		t.Fatalf("keep-all fetched back %+v", got)
	}
}

// A reader that keeps a message's file open while the step reduces it (a
// message.part streaming a large attachment) holds the reduction up on
// Windows, which refuses to replace an open file: once the store's retries
// are over the step passes the message over, not as a failure, and the row
// still says what the file holds. On Linux and macOS the reader stops
// nothing. Either way the message is reduced once the reader is done.
func TestAttachmentStepPassesOverAFileInUse(t *testing.T) {
	saved := fsretry.Waits
	fsretry.Waits = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { fsretry.Waits = saved })
	m := seedMailbox(t)
	ctx := context.Background()
	step := newAttachmentStep(m.b).(*attachmentStep)
	old := m.seedLarge(t, 7, time.Now().AddDate(0, 0, -60), ingest.Policy{})
	m.setAttachmentDays(t, 30)

	r, err := m.b.store.OpenMessageRaw(ctx, string(m.acc), old.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	runStep(t, step)
	switch got := m.row(t, old.ID); got.RawState {
	case store.RawFull:
		t.Log("the file was in use: passed over")
		if got.StrippableBytes == store.StrippableNever || len(got.RemoteParts) != 0 || step.failed[old.ID] {
			t.Fatalf("a message whose file was in use counted as a failure: %+v, failed %v", got, step.failed[old.ID])
		}
	case store.RawPartial:
		t.Log("the reader stopped nothing: reduced at once")
	default:
		t.Fatalf("message %+v", got)
	}
	r.Close()
	runStep(t, step)
	if got := m.row(t, old.ID); got.RawState != store.RawPartial {
		t.Fatalf("not reduced once the reader was done: %+v", got)
	}
}

// Messages of a paused account are left alone.
func TestAttachmentStepSkipsPausedAccounts(t *testing.T) {
	m := seedMailbox(t)
	step := newAttachmentStep(m.b)
	old := m.seedLarge(t, 7, time.Now().AddDate(-1, 0, 0), ingest.Policy{})
	m.setAttachmentDays(t, api.AttachmentOfflineNone)
	if _, err := m.b.Accounts().SetEnabled(context.Background(), api.AccountSetEnabledParams{AccountID: m.acc, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	runStep(t, step)
	if got := m.row(t, old.ID); got.RawState != store.RawFull {
		t.Fatalf("paused account's message reduced: %+v", got)
	}
}

// A batch ends the pass when it can do nothing with what it finds, so a
// message that cannot be settled never keeps the loop busy.
func TestAttachmentStepFinishesWithoutProgress(t *testing.T) {
	m := seedMailbox(t)
	step := newAttachmentStep(m.b).(*attachmentStep)
	ctx := context.Background()
	for i := range 3 {
		m.seedLarge(t, uint32(10+i), time.Now().AddDate(-1, 0, 0), ingest.Policy{})
	}
	m.setAttachmentDays(t, api.AttachmentOfflineNone)
	// The first pass reduces all three; the listing is then empty.
	if n := runStep(t, step); n > 2 {
		t.Fatalf("%d batches for three messages", n)
	}
	cands, err := m.b.store.ListStripCandidates(ctx, store.StripQuery{All: true, Limit: 10})
	if err != nil || len(cands) != 0 {
		t.Fatalf("candidates left: %d %v", len(cands), err)
	}
	if next, err := step.Batch(ctx, "x"); err != nil || next != "" {
		t.Fatalf("empty listing: %q %v", next, err)
	}
}
