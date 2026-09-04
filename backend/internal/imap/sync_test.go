// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imap

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

func TestInitialSyncWithinWindow(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	if err := h.user.Create("Sent", nil); err != nil {
		t.Fatal(err)
	}
	h.append("INBOX", rawMessage("old", "Old news", "ancient body"), daysAgo(60))
	h.append("INBOX", rawMessage("new", "Fresh news", "hello world"), daysAgo(2), imap.FlagSeen)
	h.append("Sent", rawMessage("sent", "I wrote this", "my text"), daysAgo(1))

	start := time.Now()
	h.start()
	h.waitIdle(start)

	inbox := h.folder("inbox")
	if inbox.Mailbox != "INBOX" || !inbox.Selectable || inbox.UIDValidity == 0 || inbox.UIDNext == 0 {
		t.Fatalf("inbox = %+v", inbox)
	}
	if sent := h.folder("Sent"); sent.Role != api.RoleSent {
		t.Fatalf("Sent role = %q", sent.Role)
	}
	msgs := h.messages(inbox.ID)
	if len(msgs) != 1 || msgs[0].Subject != "Fresh news" || !hasFlag(msgs[0].Flags, api.FlagSeen) {
		t.Fatalf("inbox messages = %+v", msgs)
	}
	m := msgs[0]
	if m.BodyState != store.BodyFetched || m.Snippet != "hello world" || m.RFCMessageID != "new@example.test" || m.Size == 0 {
		t.Fatalf("message = %+v", m)
	}
	if len(m.From) != 1 || m.From[0].Address != "alice@example.test" || m.From[0].Name != "Alice" {
		t.Fatalf("from = %+v", m.From)
	}
	text, hasHTML, state, err := h.st.GetMessageText(context.Background(), h.acc.ID, m.ID)
	if err != nil || text != "hello world" || hasHTML || state != store.BodyFetched {
		t.Fatalf("text = %q %v %v %v", text, hasHTML, state, err)
	}
	if _, err := os.Stat(h.st.MessageRawPath(h.acc.ID, m.ID)); err != nil {
		t.Fatalf("raw file: %v", err)
	}
	if inbox.Unread != 0 || inbox.Total != 1 {
		t.Fatalf("counts = %d/%d", inbox.Unread, inbox.Total)
	}
	if sentMsgs := h.messages(h.folder("Sent").ID); len(sentMsgs) != 1 {
		t.Fatalf("sent messages = %d", len(sentMsgs))
	}
	if n := h.notes.newMessages(); len(n) != 0 {
		t.Fatalf("initial sync must not notify, got %+v", n)
	}
	if !h.notes.sawStatus(api.SyncSyncing) {
		t.Fatal("no syncing state emitted")
	}
}

func TestNewMessageViaIdle(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	start := time.Now()
	h.start()
	h.waitIdle(start)

	h.append("INBOX", rawMessage("n1", "Ping", "first line of the body"), time.Now())
	n := h.waitNewMessage()
	inbox := h.folder("inbox")
	if n.AccountID != api.AccountID(h.acc.ID) || n.FolderID != api.FolderID(inbox.ID) {
		t.Fatalf("notification = %+v", n)
	}
	if n.Message.Subject != "Ping" || n.Message.Snippet != "first line of the body" || hasFlag(n.Message.Flags, api.FlagSeen) {
		t.Fatalf("summary = %+v", n.Message)
	}
	m, err := h.st.GetMessage(context.Background(), h.acc.ID, string(n.Message.ID))
	if err != nil || m.BodyState != store.BodyFetched {
		t.Fatalf("stored = %+v %v", m, err)
	}
	waitFor(t, "unread count", func() bool { return h.folder("inbox").Unread == 1 })
}

func TestServerFlagChangeAppliedLocally(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	uid := h.append("INBOX", rawMessage("f1", "Flag me", "body"), daysAgo(1))
	start := time.Now()
	h.start()
	h.waitIdle(start)
	inbox := h.folder("inbox")
	id := h.messages(inbox.ID)[0].ID

	c := h.client()
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	if err := c.Store(imap.UIDSetNum(imap.UID(uid)), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagSeen, imap.FlagFlagged}}, nil).Close(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "server flags locally", func() bool {
		m, err := h.st.GetMessage(context.Background(), h.acc.ID, id)
		return err == nil && hasFlag(m.Flags, api.FlagSeen) && hasFlag(m.Flags, api.FlagFlagged)
	})
}

func TestLocalFlagPushed(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	uid := h.append("INBOX", rawMessage("l1", "Star me", "body"), daysAgo(1), imap.FlagSeen)
	start := time.Now()
	h.start()
	h.waitIdle(start)
	id := h.messages(h.folder("inbox").ID)[0].ID

	if err := h.st.FlagMessages(context.Background(), h.acc.ID, []string{id}, []api.Flag{api.FlagFlagged}, []api.Flag{api.FlagSeen}); err != nil {
		t.Fatal(err)
	}
	h.syncer.Trigger("", false)
	waitFor(t, "flags on server", func() bool {
		fl := h.serverFlags("INBOX", uid)
		return hasIMAPFlag(fl, imap.FlagFlagged) && !hasIMAPFlag(fl, imap.FlagSeen)
	})
	waitFor(t, "op queue drained", func() bool {
		n, _ := h.st.CountPendingOps(context.Background(), h.acc.ID)
		return n == 0
	})
	// The local state survives the next flag pass (server-wins only after the push).
	h.syncer.Trigger("", true)
	h.waitIdle(time.Now())
	m, _ := h.st.GetMessage(context.Background(), h.acc.ID, id)
	if !hasFlag(m.Flags, api.FlagFlagged) || hasFlag(m.Flags, api.FlagSeen) {
		t.Fatalf("flags after resync = %v", m.Flags)
	}
}

func testMove(t *testing.T, caps imap.CapSet) {
	h := newHarness(t, harnessOptions{caps: caps})
	if err := h.user.Create("Archive", nil); err != nil {
		t.Fatal(err)
	}
	h.append("INBOX", rawMessage("m1", "Move me", "body"), daysAgo(1))
	h.append("INBOX", rawMessage("m2", "Stay", "body"), daysAgo(1))
	start := time.Now()
	h.start()
	h.waitIdle(start)
	inbox, archive := h.folder("inbox"), h.folder("Archive")
	if archive.Role != api.RoleArchive {
		t.Fatalf("archive role = %q", archive.Role)
	}
	var id string
	for _, m := range h.messages(inbox.ID) {
		if m.Subject == "Move me" {
			id = m.ID
		}
	}
	if err := h.st.MoveMessages(context.Background(), h.acc.ID, []string{id}, archive.ID); err != nil {
		t.Fatal(err)
	}
	m, _ := h.st.GetMessage(context.Background(), h.acc.ID, id)
	if m.UID != 0 || m.FolderID != archive.ID {
		t.Fatalf("after local move = %+v", m)
	}
	h.syncer.Trigger("", false)
	waitFor(t, "uid assigned after move", func() bool {
		m, err := h.st.GetMessage(context.Background(), h.acc.ID, id)
		return err == nil && m.FolderID == archive.ID && m.UID != 0
	})
	if got := h.serverUIDs("Archive"); len(got) != 1 {
		t.Fatalf("archive on server = %v", got)
	}
	if got := h.serverUIDs("INBOX"); len(got) != 1 {
		t.Fatalf("inbox on server = %v", got)
	}
	h.waitStatus(api.SyncIdle)
	if msgs := h.messages(archive.ID); len(msgs) != 1 || msgs[0].ID != id {
		t.Fatalf("archive locally = %+v", msgs)
	}
	if msgs := h.messages(inbox.ID); len(msgs) != 1 || msgs[0].Subject != "Stay" {
		t.Fatalf("inbox locally = %+v", msgs)
	}
	if n, _ := h.st.CountPendingOps(context.Background(), h.acc.ID); n != 0 {
		t.Fatalf("pending ops = %d", n)
	}
}

func TestMoveWithMoveAndUIDPlus(t *testing.T) { testMove(t, fullCaps) }

func TestMoveRev1Fallback(t *testing.T) { testMove(t, rev1Caps) }

func TestTrashAndPermanentDelete(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	if err := h.user.Create("Trash", nil); err != nil {
		t.Fatal(err)
	}
	h.append("INBOX", rawMessage("t1", "Trash me", "body"), daysAgo(1))
	h.append("INBOX", rawMessage("t2", "Kill me", "body"), daysAgo(1))
	start := time.Now()
	h.start()
	h.waitIdle(start)
	inbox, trash := h.folder("inbox"), h.folder("trash")
	var trashID, killID string
	for _, m := range h.messages(inbox.ID) {
		switch m.Subject {
		case "Trash me":
			trashID = m.ID
		case "Kill me":
			killID = m.ID
		}
	}

	if err := h.st.TrashMessages(context.Background(), h.acc.ID, []string{trashID}, trash.ID); err != nil {
		t.Fatal(err)
	}
	h.syncer.Trigger("", false)
	waitFor(t, "message in trash with uid", func() bool {
		m, err := h.st.GetMessage(context.Background(), h.acc.ID, trashID)
		return err == nil && m.FolderID == trash.ID && m.UID != 0
	})
	if got := h.serverUIDs("Trash"); len(got) != 1 {
		t.Fatalf("trash on server = %v", got)
	}

	// Trashing again from the trash deletes permanently.
	h.waitStatus(api.SyncIdle)
	if err := h.st.TrashMessages(context.Background(), h.acc.ID, []string{trashID}, trash.ID); err != nil {
		t.Fatal(err)
	}
	h.syncer.Trigger("", false)
	waitFor(t, "trash expunged on server", func() bool { return len(h.serverUIDs("Trash")) == 0 })

	h.waitStatus(api.SyncIdle)
	if err := h.st.DeleteMessages(context.Background(), h.acc.ID, []string{killID}); err != nil {
		t.Fatal(err)
	}
	h.syncer.Trigger("", false)
	waitFor(t, "inbox expunged on server", func() bool { return len(h.serverUIDs("INBOX")) == 0 })
	waitFor(t, "op queue drained", func() bool {
		n, _ := h.st.CountPendingOps(context.Background(), h.acc.ID)
		return n == 0
	})
}

func TestServerExpungeRemovesLocally(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	uid := h.append("INBOX", rawMessage("e1", "Gone soon", "body"), daysAgo(1))
	h.append("INBOX", rawMessage("e2", "Stays", "body"), daysAgo(1))
	start := time.Now()
	h.start()
	h.waitIdle(start)
	inbox := h.folder("inbox")
	if len(h.messages(inbox.ID)) != 2 {
		t.Fatal("expected two messages")
	}
	c := h.client()
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	if err := c.Store(imap.UIDSetNum(imap.UID(uid)), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagDeleted}, Silent: true}, nil).Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Expunge().Close(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "local removal", func() bool {
		msgs := h.messages(inbox.ID)
		return len(msgs) == 1 && msgs[0].Subject == "Stays"
	})
}

func TestUIDValidityChangeResets(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	h.append("INBOX", rawMessage("v1", "Before", "body"), daysAgo(1))
	start := time.Now()
	h.start()
	h.waitIdle(start)
	inbox := h.folder("inbox")
	oldID := h.messages(inbox.ID)[0].ID
	oldValidity := inbox.UIDValidity

	if err := h.user.Delete("INBOX"); err != nil {
		t.Fatal(err)
	}
	if err := h.user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	h.append("INBOX", rawMessage("v2", "After", "body"), daysAgo(1))
	h.syncer.Trigger("", false)
	waitFor(t, "folder reset", func() bool {
		msgs := h.messages(inbox.ID)
		return len(msgs) == 1 && msgs[0].Subject == "After" && msgs[0].BodyState == store.BodyFetched
	})
	if f := h.folder("inbox"); f.UIDValidity == oldValidity || f.ID != inbox.ID {
		t.Fatalf("folder after reset = %+v", f)
	}
	if _, err := h.st.GetMessage(context.Background(), h.acc.ID, oldID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("old message still present: %v", err)
	}
	if _, err := os.Stat(h.st.MessageRawPath(h.acc.ID, oldID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old raw file: %v", err)
	}
}

func TestPollingWithoutIdle(t *testing.T) {
	h := newHarness(t, harnessOptions{noIdle: true, prefs: SyncPrefs{IntervalSeconds: 1, OfflineDays: 30}})
	start := time.Now()
	h.start()
	h.waitIdle(start)
	h.append("INBOX", rawMessage("p1", "Polled", "body"), time.Now())
	n := h.waitNewMessage()
	if n.Message.Subject != "Polled" {
		t.Fatalf("notification = %+v", n)
	}
}

func TestOfflineBackoffRecovery(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	h.append("INBOX", rawMessage("o1", "Before outage", "body"), daysAgo(1))
	start := time.Now()
	h.start()
	h.waitIdle(start)

	h.proxy.block(true)
	h.waitStatus(api.SyncOffline)
	st := h.syncer.State()
	if st.Error == nil || (st.Error.Code != api.CodeNetworkError && st.Error.Code != api.CodeServerTimeout && st.Error.Code != api.CodeServerError) {
		t.Fatalf("offline state = %+v", st)
	}
	if st.LastSync == nil {
		t.Fatal("lastSync lost on failure")
	}
	// Let a few reconnect attempts fail.
	time.Sleep(200 * time.Millisecond)
	if h.syncer.State().Status != api.SyncOffline {
		t.Fatalf("state during outage = %+v", h.syncer.State())
	}
	h.append("INBOX", rawMessage("o2", "During outage", "body"), time.Now())

	h.proxy.block(false)
	h.waitIdle(time.Now())
	st = h.syncer.State()
	if st.Error != nil {
		t.Fatalf("error not cleared: %+v", st)
	}
	n := h.waitNewMessage()
	if n.Message.Subject != "During outage" {
		t.Fatalf("notification = %+v", n)
	}
}

func TestWrongPasswordThenWake(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	h.setPassword("wrong", nil)
	h.start()
	h.waitStatus(api.SyncAuthRequired)
	select {
	case n := <-h.notes.authCh:
		if n.AccountID != api.AccountID(h.acc.ID) || n.Reason != api.CodeAuthFailed || strings.Contains(n.Message, "wrong") {
			t.Fatalf("auth notification = %+v", n)
		}
	case <-time.After(waitTimeout):
		t.Fatal("no authRequired notification")
	}
	time.Sleep(150 * time.Millisecond)
	if h.syncer.State().Status != api.SyncAuthRequired || h.notes.authCount() != 1 {
		t.Fatalf("state = %+v, auth notifications = %d", h.syncer.State(), h.notes.authCount())
	}
	h.setPassword(password, nil)
	h.syncer.Wake()
	h.waitIdle(time.Time{})
	if h.notes.authCount() != 1 {
		t.Fatalf("auth notifications = %d", h.notes.authCount())
	}
}

func TestMissingPasswordAndKeyringError(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	h.setPassword("", api.NewError(api.CodeAuthRequired, "no stored password"))
	h.start()
	h.waitStatus(api.SyncAuthRequired)
	n := <-h.notes.authCh
	if n.Reason != api.CodeAuthRequired {
		t.Fatalf("reason = %v", n.Reason)
	}
	h.setPassword("", api.NewError(api.CodeKeyringError, "keyring down"))
	h.syncer.Wake()
	h.waitStatus(api.SyncError)
	n = <-h.notes.authCh
	if n.Reason != api.CodeKeyringError {
		t.Fatalf("reason = %v", n.Reason)
	}
	// The keyring error retries with backoff on its own.
	h.setPassword(password, nil)
	h.waitIdle(time.Time{})
	if h.notes.authCount() != 2 {
		t.Fatalf("auth notifications = %d", h.notes.authCount())
	}
}

func TestRetentionShrinkAndGrow(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	h.append("INBOX", rawMessage("r1", "Two days", "body"), daysAgo(2))
	h.append("INBOX", rawMessage("r2", "Ten days", "body"), daysAgo(10))
	h.append("INBOX", rawMessage("r3", "Sixty days", "body"), daysAgo(60))
	start := time.Now()
	h.start()
	h.waitIdle(start)
	inbox := h.folder("inbox")
	if msgs := h.messages(inbox.ID); len(msgs) != 2 {
		t.Fatalf("30-day window: %d messages", len(msgs))
	}
	var tenID string
	for _, m := range h.messages(inbox.ID) {
		if m.Subject == "Ten days" {
			tenID = m.ID
		}
	}
	rawTen := h.st.MessageRawPath(h.acc.ID, tenID)
	if _, err := os.Stat(rawTen); err != nil {
		t.Fatal(err)
	}

	h.setPrefs(SyncPrefs{OfflineDays: 5})
	h.syncer.Wake()
	waitFor(t, "shrink purge", func() bool {
		msgs := h.messages(inbox.ID)
		return len(msgs) == 1 && msgs[0].Subject == "Two days"
	})
	if _, err := os.Stat(rawTen); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("purged raw file: %v", err)
	}

	h.setPrefs(SyncPrefs{OfflineDays: 0})
	h.syncer.Wake()
	waitFor(t, "growth backfill", func() bool {
		msgs := h.messages(inbox.ID)
		if len(msgs) != 3 {
			return false
		}
		for _, m := range msgs {
			if m.BodyState != store.BodyFetched {
				return false
			}
		}
		return true
	})
	if n := h.notes.newMessages(); len(n) != 0 {
		t.Fatalf("backfill must be silent, got %+v", n)
	}
}

func TestTooBigMarked(t *testing.T) {
	if testing.Short() {
		t.Skip("appends a 25 MiB message")
	}
	h := newHarness(t, harnessOptions{})
	big := rawMessage("big", "Huge", strings.Repeat("x", maxRawMessageBytes+1))
	h.append("INBOX", big, daysAgo(1))
	h.append("INBOX", rawMessage("small", "Small", "body"), daysAgo(1))
	start := time.Now()
	h.start()
	h.waitIdle(start)
	inbox := h.folder("inbox")
	msgs := h.messages(inbox.ID)
	if len(msgs) != 2 {
		t.Fatalf("messages = %d", len(msgs))
	}
	for _, m := range msgs {
		switch m.Subject {
		case "Huge":
			if m.BodyState != store.BodyTooBig || m.Size <= maxRawMessageBytes {
				t.Fatalf("huge = %+v", m)
			}
			if _, err := os.Stat(h.st.MessageRawPath(h.acc.ID, m.ID)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("huge raw file: %v", err)
			}
		case "Small":
			if m.BodyState != store.BodyFetched {
				t.Fatalf("small = %+v", m)
			}
		}
	}
}

func TestTriggerSingleFolderAndProgress(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	if err := h.user.Create("Other", nil); err != nil {
		t.Fatal(err)
	}
	h.append("Other", rawMessage("x1", "Elsewhere", "body"), daysAgo(1))
	start := time.Now()
	h.start()
	h.waitIdle(start)
	other := h.folder("Other")
	h.append("Other", rawMessage("x2", "Elsewhere again", "body"), time.Now())
	h.syncer.Trigger(api.FolderID(other.ID), false)
	n := h.waitNewMessage()
	if n.FolderID != api.FolderID(other.ID) || n.Message.Subject != "Elsewhere again" {
		t.Fatalf("notification = %+v", n)
	}
	h.notes.mu.Lock()
	var sawFolder, sawProgress bool
	for _, st := range h.notes.states {
		if st.Status == api.SyncSyncing && st.FolderID != "" {
			sawFolder = true
		}
		if st.Status == api.SyncSyncing && st.Progress > 0 && st.Progress <= 100 {
			sawProgress = true
		}
		if st.Status == api.SyncIdle && (st.FolderID != "" || st.Progress != -1) {
			t.Errorf("idle state carries pass fields: %+v", st)
		}
	}
	h.notes.mu.Unlock()
	if !sawFolder || !sawProgress {
		t.Fatalf("folder/progress never reported: %v %v", sawFolder, sawProgress)
	}
}

func TestSupervisorLifecycle(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	h.append("INBOX", rawMessage("s1", "Supervised", "body"), daysAgo(1))
	sv := NewSupervisor(SupervisorDeps{
		Store: h.st,
		Password: func(_ context.Context, id string) (string, error) {
			if id != h.acc.ID {
				return "", api.NewError(api.CodeAccountNotFound, "unknown")
			}
			return password, nil
		},
		Notifier: h.notes,
		Prefs:    func() SyncPrefs { return SyncPrefs{OfflineDays: 30} },
	})
	if sv.Trigger(h.acc.ID, "", false) {
		t.Fatal("trigger before start must be false")
	}
	sv.Start(h.acc) // queued until Run
	sv.Start(h.acc) // idempotent
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sv.Run(ctx); close(done) }()

	waitFor(t, "queued account running", func() bool {
		st, ok := sv.State(h.acc.ID)
		return ok && st.Status == api.SyncIdle && st.LastSync != nil
	})
	if states := sv.States(); len(states) != 1 || states[0].AccountID != api.AccountID(h.acc.ID) {
		t.Fatalf("states = %+v", states)
	}
	if !sv.Trigger(h.acc.ID, "", false) {
		t.Fatal("trigger must find the syncer")
	}
	first, _ := sv.State(h.acc.ID)
	sv.Reload()
	waitFor(t, "reload ran a pass", func() bool {
		st, ok := sv.State(h.acc.ID)
		return ok && st.Status == api.SyncIdle && st.LastSync != nil && st.LastSync.After(*first.LastSync)
	})

	sv.Stop(h.acc.ID)
	if _, ok := sv.State(h.acc.ID); ok {
		t.Fatal("state after stop")
	}
	if sv.Trigger(h.acc.ID, "", false) {
		t.Fatal("trigger after stop must be false")
	}
	sv.Stop("nope") // unknown ids are no-ops
	sv.Restart(h.acc)
	waitFor(t, "restart", func() bool {
		st, ok := sv.State(h.acc.ID)
		return ok && st.Status == api.SyncIdle
	})
	if msgs := h.messages(h.folder("inbox").ID); len(msgs) != 1 {
		t.Fatalf("messages = %d", len(msgs))
	}

	cancel()
	select {
	case <-done:
	case <-time.After(waitTimeout):
		t.Fatal("Run did not return")
	}
	if states := sv.States(); len(states) != 0 {
		t.Fatalf("states after Run = %+v", states)
	}
	sv.Start(h.acc) // after Run: ignored
	if _, ok := sv.State(h.acc.ID); ok {
		t.Fatal("start after Run must be ignored")
	}
}

// TestRetentionWithoutSearchSince covers servers that reject SEARCH SINCE:
// the window is applied from INTERNALDATE instead and shrinking/growing it
// behaves exactly like the server-side search.
func TestRetentionWithoutSearchSince(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	h.syncer.deps.NoSinceSearch = true
	h.append("INBOX", rawMessage("n1", "Two days", "body"), daysAgo(2))
	h.append("INBOX", rawMessage("n2", "Ten days", "body"), daysAgo(10))
	h.append("INBOX", rawMessage("n3", "Sixty days", "body"), daysAgo(60))
	start := time.Now()
	h.start()
	h.waitIdle(start)
	inbox := h.folder("inbox")
	if msgs := h.messages(inbox.ID); len(msgs) != 2 {
		t.Fatalf("30-day window without SINCE: %d messages", len(msgs))
	}

	h.setPrefs(SyncPrefs{OfflineDays: 5})
	h.syncer.Wake()
	waitFor(t, "shrink purge without SINCE", func() bool {
		msgs := h.messages(inbox.ID)
		return len(msgs) == 1 && msgs[0].Subject == "Two days"
	})

	h.setPrefs(SyncPrefs{OfflineDays: 0})
	h.syncer.Wake()
	waitFor(t, "growth backfill without SINCE", func() bool {
		return len(h.messages(inbox.ID)) == 3
	})
	if got := h.serverUIDs("INBOX"); len(got) != 3 {
		t.Fatalf("server lost messages: %v", got)
	}
}
