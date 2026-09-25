// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// seedCopy inserts a message of a Drafts folder with the given Message-ID.
func seedCopy(t *testing.T, s *Store, f Folder, uid uint32, remoteID, rfcID string) *Message {
	t.Helper()
	m := &Message{
		AccountID: f.AccountID, FolderID: f.ID, UID: uid, RemoteID: remoteID, Flags: []api.Flag{api.FlagDraft, api.FlagSeen},
		Subject: "draft " + rfcID, Date: time.Now(), RFCMessageID: rfcID, Size: 10,
	}
	if err := s.UpsertMessages(context.Background(), []*Message{m}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteMessageRaw(context.Background(), f.AccountID, m.ID, strings.NewReader("Subject: x\r\n\r\nbody"), 1<<20); err != nil {
		t.Fatal(err)
	}
	return m
}

// dueIDs lists the ids DueDraftUploads returns at at.
func dueIDs(t *testing.T, s *Store, at time.Time) []string {
	t.Helper()
	due, err := s.DueDraftUploads(context.Background(), "acc", at, time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, d := range due {
		ids = append(ids, d.ID)
	}
	return ids
}

func TestDraftUploadLifecycle(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	drafts := seedFolder(t, s, "acc", "Drafts", api.RoleDrafts)
	later := time.Now().Add(2 * time.Minute)

	d := Draft{AccountID: "acc", Subject: "hi", TextBody: "one"}
	if err := s.SaveDraft(ctx, &d, nil); err != nil {
		t.Fatal(err)
	}
	if got := dueIDs(t, s, time.Now()); len(got) != 0 {
		t.Fatalf("due before it rested: %v", got)
	}
	if got := dueIDs(t, s, later); len(got) != 1 || got[0] != d.ID {
		t.Fatalf("due after resting = %v", got)
	}
	if due, _ := s.NextDraftUpload(ctx, "acc", time.Minute); due.IsZero() || due.Before(d.UpdatedAt) {
		t.Errorf("next upload = %v", due)
	}

	// Version 1 stored as UID 10.
	c1 := DraftCopy{FolderID: drafts.ID, UID: 10, RFCMessageID: "v1@x"}
	if found, err := s.MarkDraftSynced(ctx, "acc", d.ID, 1, c1, false); err != nil || !found {
		t.Fatalf("mark synced: %v %v", found, err)
	}
	if got := dueIDs(t, s, later); len(got) != 0 {
		t.Fatalf("still due: %v", got)
	}
	got, _ := s.GetDraft(ctx, "acc", d.ID)
	if got.Copy != c1 || got.SyncedVersion != 1 || got.SyncedAt.IsZero() || got.Version != 1 {
		t.Fatalf("after sync: %+v", got)
	}
	row1 := seedCopy(t, s, drafts, 10, "", "v1@x")
	if linked, err := s.DraftForMessage(ctx, "acc", *row1); err != nil || linked.ID != d.ID {
		t.Fatalf("draft for message: %+v %v", linked, err)
	}

	// A failure backs off; a save restarts the upload at once.
	d.TextBody = "two"
	if err := s.SaveDraft(ctx, &d, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDraftSyncFailed(ctx, d.ID, "NO quota", later.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := dueIDs(t, s, later); len(got) != 0 {
		t.Fatalf("due during backoff: %v", got)
	}
	// Another draft without a retry time does not hide this one's.
	other := Draft{AccountID: "acc", TextBody: "other"}
	if err := s.SaveDraft(ctx, &other, nil); err != nil {
		t.Fatal(err)
	}
	if next, _ := s.NextDraftUpload(ctx, "acc", time.Minute); next.After(other.UpdatedAt.Add(time.Minute)) {
		t.Errorf("next upload %v after the other draft rested", next)
	}
	if err := s.DeleteDraft(ctx, "acc", other.ID); err != nil {
		t.Fatal(err)
	}
	if next, _ := s.NextDraftUpload(ctx, "acc", time.Minute); !next.Equal(later.Add(time.Hour).UTC().Truncate(time.Millisecond)) {
		t.Errorf("next upload %v, want the retry time", next)
	}
	if err := s.SaveDraft(ctx, &d, nil); err != nil {
		t.Fatal(err)
	}
	if got := dueIDs(t, s, later); len(got) != 1 {
		t.Fatalf("save did not restart the upload: %v", got)
	}

	// Version 3 replaces the copy: its row goes, a delete is queued.
	c2 := DraftCopy{FolderID: drafts.ID, UID: 11, RFCMessageID: "v3@x"}
	if found, err := s.MarkDraftSynced(ctx, "acc", d.ID, 3, c2, false); err != nil || !found {
		t.Fatalf("mark synced: %v %v", found, err)
	}
	if _, err := s.GetMessage(ctx, "acc", row1.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("replaced copy still listed: %v", err)
	}
	ops, _ := s.NextOps(ctx, "acc", time.Now(), 10)
	if len(ops) != 1 || ops[0].Kind != OpDelete || ops[0].UID != 10 || ops[0].FolderID != drafts.ID {
		t.Fatalf("ops = %+v", ops)
	}
	if f, _ := s.GetFolder(ctx, "acc", drafts.ID); f.Total != 0 {
		t.Errorf("drafts folder total = %d", f.Total)
	}

	// A copy never synchronised down is addressed by its UID.
	d.TextBody = "four"
	if err := s.SaveDraft(ctx, &d, nil); err != nil {
		t.Fatal(err)
	}
	c3 := DraftCopy{FolderID: drafts.ID, UID: 12, RFCMessageID: "v4@x"}
	if _, err := s.MarkDraftSynced(ctx, "acc", d.ID, 4, c3, false); err != nil {
		t.Fatal(err)
	}
	ops, _ = s.NextOps(ctx, "acc", time.Now(), 10)
	if len(ops) != 2 || ops[1].Kind != OpDelete || ops[1].UID != 11 || ops[1].MessageID == "" {
		t.Fatalf("ops = %+v", ops)
	}

	// keepPrevious leaves the old copy alone.
	d.TextBody = "five"
	if err := s.SaveDraft(ctx, &d, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkDraftSynced(ctx, "acc", d.ID, 5, DraftCopy{FolderID: drafts.ID, UID: 13, RFCMessageID: "v5@x"}, true); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountPendingOps(ctx, "acc"); n != 2 {
		t.Errorf("keepPrevious queued a delete: %d ops", n)
	}

	// Deleting the draft deletes its copy.
	if err := s.DeleteDraft(ctx, "acc", d.ID); err != nil {
		t.Fatal(err)
	}
	ops, _ = s.NextOps(ctx, "acc", time.Now(), 10)
	if len(ops) != 3 || ops[2].UID != 13 {
		t.Fatalf("ops after delete = %+v", ops)
	}

	// An upload that finishes after the draft went removes its own copy.
	if found, err := s.MarkDraftSynced(ctx, "acc", d.ID, 6, DraftCopy{FolderID: drafts.ID, UID: 14, RFCMessageID: "v6@x"}, false); err != nil || found {
		t.Fatalf("gone draft: found=%v err=%v", found, err)
	}
	ops, _ = s.NextOps(ctx, "acc", time.Now(), 10)
	if len(ops) != 4 || ops[3].UID != 14 {
		t.Fatalf("orphan copy not deleted: %+v", ops)
	}
}

func TestDraftUploadAttemptsCap(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	d := Draft{AccountID: "acc", TextBody: "x"}
	if err := s.SaveDraft(ctx, &d, nil); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	for i := 0; i < MaxDraftSyncAttempts; i++ {
		if err := s.MarkDraftSyncFailed(ctx, d.ID, "refused", past); err != nil {
			t.Fatal(err)
		}
	}
	if got := dueIDs(t, s, time.Now().Add(2*time.Minute)); len(got) != 0 {
		t.Fatalf("due after the cap: %v", got)
	}
	if due, _ := s.NextDraftUpload(ctx, "acc", time.Minute); !due.IsZero() {
		t.Errorf("next upload after the cap = %v", due)
	}
	if err := s.MarkDraftSyncFailed(ctx, "d_nope", "x", past); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown draft: %v", err)
	}
}

func TestDraftCopyGoesWithTrashAndMove(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	drafts := seedFolder(t, s, "acc", "Drafts", api.RoleDrafts)
	trash := seedFolder(t, s, "acc", "Trash", api.RoleTrash)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)

	att := importTestAttachment(t, s, "acc", "a.txt", "data")
	d := Draft{AccountID: "acc", TextBody: "x", Attachments: nil}
	if err := s.SaveDraft(ctx, &d, []string{att.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkDraftSynced(ctx, "acc", d.ID, 1, DraftCopy{FolderID: drafts.ID, UID: 5, RFCMessageID: "c@x"}, false); err != nil {
		t.Fatal(err)
	}
	row := seedCopy(t, s, drafts, 5, "", "c@x")
	if err := s.TrashMessages(ctx, "acc", []string{row.ID}, trash.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDraft(ctx, "acc", d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("draft of a trashed copy kept: %v", err)
	}
	// Its attachment is released, not deleted: a compose window still
	// holding it can save the text again as a new draft.
	again := Draft{AccountID: "acc", TextBody: "x"}
	if err := s.SaveDraft(ctx, &again, []string{att.ID}); err != nil {
		t.Fatalf("attachment of the dropped draft: %v", err)
	}

	// A move out of the Drafts folder does the same, by server identity.
	if _, err := s.MarkDraftSynced(ctx, "acc", again.ID, 1, DraftCopy{FolderID: drafts.ID, UID: 6, RFCMessageID: "other@x"}, false); err != nil {
		t.Fatal(err)
	}
	row2 := seedCopy(t, s, drafts, 6, "", "")
	if err := s.MoveMessages(ctx, "acc", []string{row2.ID}, inbox.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDraft(ctx, "acc", again.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("draft of a moved copy kept: %v", err)
	}

	// Messages of other folders leave drafts alone.
	keep := Draft{AccountID: "acc", TextBody: "y"}
	if err := s.SaveDraft(ctx, &keep, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkDraftSynced(ctx, "acc", keep.ID, 1, DraftCopy{FolderID: drafts.ID, UID: 7, RFCMessageID: "keep@x"}, false); err != nil {
		t.Fatal(err)
	}
	stray := seedCopy(t, s, inbox, 7, "", "keep@x")
	if err := s.DeleteMessages(ctx, "acc", []string{stray.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDraft(ctx, "acc", keep.ID); err != nil {
		t.Fatalf("inbox message took a draft along: %v", err)
	}
}

func TestDraftAdoptsCopy(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	drafts := seedFolder(t, s, "acc", "Drafts", api.RoleDrafts)
	foreign := seedCopy(t, s, drafts, 20, "", "foreign@x")
	c := CopyOf(*foreign, drafts)

	d := Draft{AccountID: "acc", TextBody: "taken over", Adopt: &c}
	if err := s.SaveDraft(ctx, &d, nil); err != nil {
		t.Fatal(err)
	}
	if d.Adopt == nil {
		t.Fatal("adoption refused")
	}
	got, _ := s.GetDraft(ctx, "acc", d.ID)
	if got.Copy != c || got.SyncedVersion != 0 {
		t.Fatalf("adopted draft: %+v", got)
	}
	if linked, err := s.DraftForMessage(ctx, "acc", *foreign); err != nil || linked.ID != d.ID {
		t.Fatalf("draft for message: %v %v", linked.ID, err)
	}
	// The first upload replaces the adopted message.
	if _, err := s.MarkDraftSynced(ctx, "acc", d.ID, 1, DraftCopy{FolderID: drafts.ID, UID: 21, RFCMessageID: "mine@x"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMessage(ctx, "acc", foreign.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("adopted message kept: %v", err)
	}

	// A second draft adopting the copy of a fully uploaded one replaces it.
	mine := seedCopy(t, s, drafts, 21, "", "mine@x")
	c2 := CopyOf(*mine, drafts)
	second := Draft{AccountID: "acc", TextBody: "newer", Adopt: &c2}
	if err := s.SaveDraft(ctx, &second, nil); err != nil || second.Adopt == nil {
		t.Fatalf("adopt from an uploaded draft: %v", err)
	}
	if _, err := s.GetDraft(ctx, "acc", d.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("superseded draft kept: %v", err)
	}

	// One with changes left to upload keeps it: both survive.
	third := Draft{AccountID: "acc", TextBody: "third", Adopt: &c2}
	if err := s.SaveDraft(ctx, &third, nil); err != nil {
		t.Fatal(err)
	}
	if third.Adopt != nil {
		t.Error("adopted from a draft with pending changes")
	}
	if _, err := s.GetDraft(ctx, "acc", second.ID); err != nil {
		t.Errorf("pending draft dropped: %v", err)
	}
}

func TestMessageIDByRFCSkipsDrafts(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	drafts := seedFolder(t, s, "acc", "Drafts", api.RoleDrafts)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	seedCopy(t, s, drafts, 1, "", "p@x")
	if _, err := s.MessageIDByRFC(ctx, "acc", "p@x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("draft copy found as parent: %v", err)
	}
	parent := seedCopy(t, s, inbox, 1, "", "p@x")
	if id, err := s.MessageIDByRFC(ctx, "acc", "p@x"); err != nil || id != parent.ID {
		t.Fatalf("parent = %q %v", id, err)
	}
	if _, err := s.MessageIDByRFC(ctx, "acc", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty id: %v", err)
	}
}

// A store from before draft synchronisation: its drafts count as uploaded
// (many are autosaves the user discarded) until they are saved again.
func TestMigration0012OldDraftsStayLocal(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(0)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')))`); err != nil {
		t.Fatal(err)
	}
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migs {
		if m.version > 11 {
			continue
		}
		if _, err := db.Exec(m.sql); err != nil {
			t.Fatalf("migration %d: %v", m.version, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, m.version, m.name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO drafts (id, account_id, version, updated_at) VALUES ('d_old', 'acc', 3, '2020-01-01T00:00:00.000Z')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, path, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := dueIDs(t, s, time.Now()); len(got) != 0 {
		t.Fatalf("old draft due: %v", got)
	}
	d, err := s.GetDraft(ctx, "acc", "d_old")
	if err != nil || d.SyncedVersion != 3 || !d.Copy.IsZero() || len(d.References) != 0 {
		t.Fatalf("migrated draft: %+v %v", d, err)
	}
	if err := s.SaveDraft(ctx, &d, nil); err != nil {
		t.Fatal(err)
	}
	if got := dueIDs(t, s, time.Now().Add(2*time.Minute)); len(got) != 1 {
		t.Fatalf("saved old draft not due: %v", got)
	}
}
