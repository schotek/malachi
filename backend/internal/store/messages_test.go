// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestUpsertMessagesConflictAndBatch(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)

	m := &Message{
		AccountID: "acc", FolderID: inbox.ID, UID: 1, ModSeq: 10,
		Flags: []api.Flag{api.FlagFlagged, "$Custom", api.FlagFlagged, ""},
		From:  []api.Address{{Name: "A <b>", Address: "a@example.invalid"}},
		To:    []api.Address{{Address: "me@example.invalid"}},
		CC:    []api.Address{{Address: "cc@example.invalid"}}, BCC: []api.Address{{Address: "bcc@example.invalid"}},
		ReplyTo: []api.Address{{Address: "reply@example.invalid"}},
		Subject: "Subject \x00 with ‮ control", Date: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
		InternalDate: time.Date(2026, 9, 1, 12, 0, 1, 0, time.UTC), RFCMessageID: "<x@y>", InReplyTo: "<w@y>",
		References: []string{"<w@y>", "<v@y>"}, Size: 1234, Snippet: "hi", HasAttachments: true,
		Attachments: []api.Attachment{{PartID: "2", Filename: "a.pdf", ContentType: "application/pdf", Size: 10}},
		Headers:     map[string]string{"List-Id": "<l.example>"}, HasHTML: true, ThreadID: "t1",
	}
	if err := s.UpsertMessages(ctx, []*Message{m}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(m.ID, "m_") || m.CreatedAt.IsZero() || m.UpdatedAt.IsZero() {
		t.Fatalf("insert: %+v", m)
	}
	got, err := s.GetMessage(ctx, "acc", m.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantFlags := []api.Flag{"$Custom", api.FlagFlagged}
	if fmt.Sprint(got.Flags) != fmt.Sprint(wantFlags) {
		t.Errorf("flags = %v, want %v", got.Flags, wantFlags)
	}
	if got.Subject != m.Subject || got.From[0].Name != "A <b>" || len(got.To) != 1 || len(got.CC) != 1 || len(got.BCC) != 1 ||
		got.ReplyTo[0].Address != "reply@example.invalid" || !got.Date.Equal(m.Date) || !got.InternalDate.Equal(m.InternalDate) ||
		got.RFCMessageID != "<x@y>" || got.InReplyTo != "<w@y>" || len(got.References) != 2 || got.Size != 1234 ||
		got.Snippet != "hi" || !got.HasAttachments || len(got.Attachments) != 1 || got.Attachments[0].Filename != "a.pdf" ||
		got.Headers["List-Id"] != "<l.example>" || !got.HasHTML || got.BodyState != BodyNone || got.ThreadID != "t1" ||
		got.ModSeq != 10 || got.UID != 1 || got.FolderID != inbox.ID || got.AccountID != "acc" {
		t.Errorf("round trip: %+v", got)
	}
	if _, err := s.GetMessage(ctx, "other", m.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign account: %v", err)
	}

	// Same (folder, uid) again: only flags/modseq change, the id is returned.
	dup := &Message{AccountID: "acc", FolderID: inbox.ID, UID: 1, ModSeq: 11, Flags: []api.Flag{api.FlagSeen}, Subject: "changed"}
	if err := s.UpsertMessages(ctx, []*Message{dup}); err != nil {
		t.Fatal(err)
	}
	if dup.ID != m.ID {
		t.Fatalf("conflict id = %s, want %s", dup.ID, m.ID)
	}
	got, _ = s.GetMessage(ctx, "acc", m.ID)
	if got.Subject != m.Subject || got.ModSeq != 11 || fmt.Sprint(got.Flags) != fmt.Sprint([]api.Flag{api.FlagSeen}) {
		t.Errorf("after conflict: %+v", got)
	}
	if unread, total, _ := s.RecountFolder(ctx, inbox.ID); unread != 0 || total != 1 {
		t.Errorf("unread derived from seen: %d/%d", unread, total)
	}

	// A batch of 1000 in one transaction, half of them unread.
	batch := make([]*Message, 1000)
	for i := range batch {
		var flags []api.Flag
		if i%2 == 0 {
			flags = []api.Flag{api.FlagSeen}
		}
		batch[i] = &Message{AccountID: "acc", FolderID: inbox.ID, UID: uint32(100 + i), Flags: flags,
			Subject: fmt.Sprint("n", i), Date: time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC)}
	}
	if err := s.UpsertMessages(ctx, batch); err != nil {
		t.Fatal(err)
	}
	ids := make(map[string]bool)
	for _, b := range batch {
		if b.ID == "" || ids[b.ID] {
			t.Fatalf("bad id in batch: %q", b.ID)
		}
		ids[b.ID] = true
	}
	if uids, _ := s.ListUIDs(ctx, inbox.ID); len(uids) != 1001 || uids[0] != 1 || uids[1000] != 1099 {
		t.Errorf("ListUIDs: len=%d first=%d last=%d", len(uids), uids[0], uids[len(uids)-1])
	}
	if unread, total, _ := s.RecountFolder(ctx, inbox.ID); unread != 500 || total != 1001 {
		t.Errorf("recount after batch: %d/%d", unread, total)
	}
	// A failing row rolls the whole batch back.
	bad := []*Message{
		{AccountID: "acc", FolderID: inbox.ID, UID: 5000},
		{AccountID: "acc", FolderID: "f_missing", UID: 1},
	}
	if err := s.UpsertMessages(ctx, bad); err == nil {
		t.Error("dangling folder accepted")
	}
	if _, total, _ := s.RecountFolder(ctx, inbox.ID); total != 1001 {
		t.Errorf("partial batch committed: %d", total)
	}
}

func TestListMessagesCursors(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	other := seedFolder(t, s, "acc", "Other", api.RoleNone)

	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	msgs := make([]*Message, 7)
	for i := range msgs {
		var flags []api.Flag
		if i < 3 {
			flags = []api.Flag{api.FlagSeen}
		}
		// Two messages share a date so the id tiebreak is exercised.
		d := base.Add(time.Duration(i/2) * time.Hour)
		msgs[i] = &Message{AccountID: "acc", FolderID: inbox.ID, UID: uint32(i + 1), Subject: fmt.Sprint("s", i), Date: d, Flags: flags}
	}
	if err := s.UpsertMessages(ctx, msgs); err != nil {
		t.Fatal(err)
	}
	seedMessage(t, s, other, 1, "elsewhere", base)

	walk := func(sort api.SortOrder, unreadOnly bool, limit int) (subjects []string, total int) {
		t.Helper()
		cursor := ""
		for {
			items, next, tot, err := s.ListMessages(ctx, "acc", inbox.ID, cursor, limit, sort, unreadOnly)
			if err != nil {
				t.Fatal(err)
			}
			total = tot
			for _, m := range items {
				subjects = append(subjects, m.Subject)
			}
			if next == "" {
				return
			}
			if len(items) != limit {
				t.Fatalf("short page with cursor: %d", len(items))
			}
			cursor = next
		}
	}
	desc, total := walk(api.SortDateDesc, false, 2)
	if total != 7 || len(desc) != 7 {
		t.Fatalf("desc: total=%d %v", total, desc)
	}
	asc, _ := walk(api.SortDateAsc, false, 3)
	for i := range asc {
		if asc[i] != desc[len(desc)-1-i] {
			t.Fatalf("asc is not the reverse of desc: %v vs %v", asc, desc)
		}
	}
	dates := make(map[string]time.Time)
	for _, m := range msgs {
		dates[m.Subject] = m.Date
	}
	for i := 1; i < len(asc); i++ {
		if dates[asc[i]].Before(dates[asc[i-1]]) {
			t.Fatalf("asc out of order: %v", asc)
		}
	}
	unread, total := walk("", true, 2)
	if total != 4 || len(unread) != 4 {
		t.Fatalf("unreadOnly: total=%d %v", total, unread)
	}
	for _, sub := range unread {
		if sub < "s3" {
			t.Errorf("seen message %s in unread list", sub)
		}
	}

	// Cursors are bound to their sort order; garbage is rejected.
	_, next, _, err := s.ListMessages(ctx, "acc", inbox.ID, "", 2, api.SortDateDesc, false)
	if err != nil || next == "" {
		t.Fatal(err, next)
	}
	if _, _, _, err := s.ListMessages(ctx, "acc", inbox.ID, next, 2, api.SortDateAsc, false); !errors.Is(err, ErrBadCursor) {
		t.Errorf("cross-sort cursor: %v", err)
	}
	if _, _, _, err := s.ListMessages(ctx, "acc", inbox.ID, next, 2, api.SortDateDesc, false); err != nil {
		t.Errorf("own cursor: %v", err)
	}
	for _, bad := range []string{"!!!", base64.RawURLEncoding.EncodeToString([]byte("d\x00x")),
		base64.RawURLEncoding.EncodeToString([]byte("d\x00not a date\x00m_1")),
		base64.RawURLEncoding.EncodeToString([]byte("\x00" + zeroStamp + "\x00m_1")),
		base64.RawURLEncoding.EncodeToString([]byte("d\x00" + zeroStamp + "\x00"))} {
		if _, _, _, err := s.ListMessages(ctx, "acc", inbox.ID, bad, 2, api.SortDateDesc, false); !errors.Is(err, ErrBadCursor) {
			t.Errorf("cursor %q: %v", bad, err)
		}
	}
	if _, _, _, err := s.ListMessages(ctx, "acc", inbox.ID, "", 2, "bogus", false); err == nil {
		t.Error("bogus sort accepted")
	}
	if _, _, _, err := s.ListMessages(ctx, "other", inbox.ID, "", 2, "", false); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign folder: %v", err)
	}
	if items, next, total, err := s.ListMessages(ctx, "acc", other.ID, "", 0, "", false); err != nil || len(items) != 1 || next != "" || total != 1 {
		t.Errorf("other folder: %d %q %d %v", len(items), next, total, err)
	}
	// Zero dates still page (never an empty stamp in the cursor).
	zero := &Message{AccountID: "acc", FolderID: other.ID, UID: 2, Subject: "nodate"}
	if err := s.UpsertMessages(ctx, []*Message{zero}); err != nil {
		t.Fatal(err)
	}
	items, next, _, err := s.ListMessages(ctx, "acc", other.ID, "", 1, api.SortDateAsc, false)
	if err != nil || len(items) != 1 || items[0].Subject != "nodate" || !items[0].Date.IsZero() || next == "" {
		t.Fatalf("zero date page: %+v %q %v", items, next, err)
	}
	if items, _, _, err = s.ListMessages(ctx, "acc", other.ID, next, 1, api.SortDateAsc, false); err != nil || len(items) != 1 || items[0].Subject != "elsewhere" {
		t.Fatalf("after zero date: %+v %v", items, err)
	}
}

func TestApplyServerFlagsAndPendingOp(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	m := seedMessage(t, s, inbox, 1, "one", time.Now())

	changed, err := s.ApplyServerFlags(ctx, inbox.ID, 1, []api.Flag{api.FlagSeen, api.FlagAnswered}, 5)
	if err != nil || !changed {
		t.Fatalf("first apply: %v %v", changed, err)
	}
	got, _ := s.GetMessage(ctx, "acc", m.ID)
	if fmt.Sprint(got.Flags) != fmt.Sprint([]api.Flag{api.FlagAnswered, api.FlagSeen}) || got.ModSeq != 5 {
		t.Errorf("after apply: %+v", got)
	}
	changed, err = s.ApplyServerFlags(ctx, inbox.ID, 1, []api.Flag{api.FlagAnswered, api.FlagSeen}, 6)
	if err != nil || changed {
		t.Fatalf("same flags: %v %v", changed, err)
	}
	if got, _ = s.GetMessage(ctx, "acc", m.ID); got.ModSeq != 6 {
		t.Errorf("modseq not refreshed: %d", got.ModSeq)
	}
	if _, err := s.ApplyServerFlags(ctx, inbox.ID, 99, nil, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown uid: %v", err)
	}

	// A pending local flag op wins until it is pushed.
	if err := s.FlagMessages(ctx, "acc", []string{m.ID}, nil, []api.Flag{api.FlagSeen}); err != nil {
		t.Fatal(err)
	}
	changed, err = s.ApplyServerFlags(ctx, inbox.ID, 1, []api.Flag{api.FlagSeen, api.FlagAnswered}, 7)
	if err != nil || changed {
		t.Fatalf("with pending op: %v %v", changed, err)
	}
	got, _ = s.GetMessage(ctx, "acc", m.ID)
	if fmt.Sprint(got.Flags) != fmt.Sprint([]api.Flag{api.FlagAnswered}) || got.ModSeq != 6 {
		t.Errorf("server flags applied over pending op: %+v", got)
	}
	ops, _ := s.NextOps(ctx, "acc", time.Now(), 10)
	if err := s.MarkOpDone(ctx, ops[0].ID); err != nil {
		t.Fatal(err)
	}
	if changed, err = s.ApplyServerFlags(ctx, inbox.ID, 1, []api.Flag{api.FlagSeen, api.FlagAnswered}, 7); err != nil || !changed {
		t.Fatalf("after op done: %v %v", changed, err)
	}
}

func TestAssignUIDPatchesOps(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	archive := seedFolder(t, s, "acc", "Archive", api.RoleArchive)
	m := seedMessage(t, s, inbox, 1, "moved", time.Now())

	if err := s.MoveMessages(ctx, "acc", []string{m.ID}, archive.ID); err != nil {
		t.Fatal(err)
	}
	// A flag change while the move is unreconciled is blocked on uid 0.
	if err := s.FlagMessages(ctx, "acc", []string{m.ID}, []api.Flag{api.FlagFlagged}, nil); err != nil {
		t.Fatal(err)
	}
	ops, _ := s.NextOps(ctx, "acc", time.Now(), 10)
	if len(ops) != 2 || ops[0].Kind != OpMove || ops[0].UID != 1 || ops[0].FolderID != inbox.ID || ops[0].TargetFolderID != archive.ID ||
		ops[1].Kind != OpFlag || ops[1].UID != 0 || ops[1].FolderID != archive.ID {
		t.Fatalf("ops = %+v", ops)
	}
	pending, err := s.FindPendingMessage(ctx, archive.ID, m.RFCMessageID)
	if err != nil || pending.ID != m.ID || pending.UID != 0 {
		t.Fatalf("find pending: %+v %v", pending, err)
	}
	if _, err := s.FindPendingMessage(ctx, archive.ID, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("empty message-id matched: %v", err)
	}
	if _, err := s.FindPendingMessage(ctx, inbox.ID, m.RFCMessageID); !errors.Is(err, ErrNotFound) {
		t.Errorf("pending found in source folder: %v", err)
	}

	// The target folder was synced first and produced a duplicate row.
	dupRow := &Message{AccountID: "acc", FolderID: archive.ID, UID: 77, Subject: "dup"}
	if err := s.UpsertMessages(ctx, []*Message{dupRow}); err != nil {
		t.Fatal(err)
	}
	if err := s.AssignUID(ctx, m.ID, 77, 9, []api.Flag{api.FlagSeen}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMessage(ctx, "acc", m.ID)
	if err != nil || got.UID != 77 || got.ModSeq != 9 || got.FolderID != archive.ID || fmt.Sprint(got.Flags) != fmt.Sprint([]api.Flag{api.FlagSeen}) {
		t.Fatalf("after assign: %+v %v", got, err)
	}
	if _, err := s.GetMessage(ctx, "acc", dupRow.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("duplicate row kept: %v", err)
	}
	ops, _ = s.NextOps(ctx, "acc", time.Now(), 10)
	if len(ops) != 2 || ops[1].UID != 77 || ops[0].UID != 1 {
		t.Fatalf("ops after assign = %+v", ops)
	}
	if err := s.AssignUID(ctx, "m_nope", 1, 1, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}
	if err := s.AssignUID(ctx, m.ID, 0, 1, nil); err == nil {
		t.Error("uid 0 accepted")
	}

	// Double move: the row has moved on to a third folder; the reconciled
	// UID belongs to the middle folder, so only the ops there are patched.
	third := seedFolder(t, s, "acc", "Third", api.RoleNone)
	m2 := seedMessage(t, s, inbox, 2, "twice", time.Now())
	if err := s.MoveMessages(ctx, "acc", []string{m2.ID}, archive.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveMessages(ctx, "acc", []string{m2.ID}, third.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AssignUID(ctx, m2.ID, 78, 1, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetMessage(ctx, "acc", m2.ID)
	if got.UID != 0 || got.FolderID != third.ID {
		t.Errorf("row of a double move changed: %+v", got)
	}
	ops, _ = s.NextOps(ctx, "acc", time.Now(), 10)
	var second Op
	for _, op := range ops {
		if op.MessageID == m2.ID && op.TargetFolderID == third.ID {
			second = op
		}
	}
	if second.UID != 78 || second.FolderID != archive.ID {
		t.Errorf("second move op not patched: %+v", second)
	}
	// The message was deleted locally while its move was pending: the
	// blocked delete op still gets its uid.
	m3 := seedMessage(t, s, inbox, 3, "thrice", time.Now())
	if err := s.MoveMessages(ctx, "acc", []string{m3.ID}, archive.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMessages(ctx, "acc", []string{m3.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.AssignUID(ctx, m3.ID, 79, 1, nil); err != nil {
		t.Fatal(err)
	}
	ops, _ = s.NextOps(ctx, "acc", time.Now(), 20)
	for _, op := range ops {
		if op.MessageID == m3.ID && op.Kind == OpDelete && op.UID != 79 {
			t.Errorf("delete op of removed row not patched: %+v", op)
		}
	}
}

func TestDeleteStalePendingAndByUID(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	archive := seedFolder(t, s, "acc", "Archive", api.RoleArchive)
	stale := seedMessage(t, s, inbox, 1, "stale", time.Now())
	blocked := seedMessage(t, s, inbox, 2, "blocked", time.Now())
	fresh := seedMessage(t, s, inbox, 3, "fresh", time.Now())
	if err := s.MoveMessages(ctx, "acc", []string{stale.ID, blocked.ID, fresh.ID}, archive.ID); err != nil {
		t.Fatal(err)
	}
	ops, _ := s.NextOps(ctx, "acc", time.Now(), 10)
	for _, op := range ops {
		if op.MessageID != blocked.ID {
			if err := s.MarkOpDone(ctx, op.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	past := time.Now().Add(-2 * time.Hour).UTC().Format(timeLayout)
	for _, id := range []string{stale.ID, blocked.ID} {
		if _, err := s.db.Exec(`UPDATE messages SET updated_at = ? WHERE id = ?`, past, id); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.DeleteStalePending(ctx, archive.ID, time.Now().Add(-time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("stale: %d %v", n, err)
	}
	if _, err := s.GetMessage(ctx, "acc", stale.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("stale row kept: %v", err)
	}
	if fileExists(t, s.MessageRawPath("acc", stale.ID)) {
		t.Error("stale file kept")
	}
	for _, id := range []string{blocked.ID, fresh.ID} {
		if _, err := s.GetMessage(ctx, "acc", id); err != nil {
			t.Errorf("%s removed: %v", id, err)
		}
	}

	// Server expunge: rows, files and ops go; unknown uids are ignored.
	a := seedMessage(t, s, inbox, 10, "a", time.Now())
	b := seedMessage(t, s, inbox, 11, "b", time.Now())
	c := seedMessage(t, s, inbox, 12, "c", time.Now())
	if err := s.FlagMessages(ctx, "acc", []string{a.ID}, []api.Flag{api.FlagSeen}, nil); err != nil {
		t.Fatal(err)
	}
	before, _ := s.CountPendingOps(ctx, "acc")
	if err := s.DeleteMessagesByUID(ctx, inbox.ID, []uint32{10, 11, 0, 999}); err != nil {
		t.Fatal(err)
	}
	for _, m := range []*Message{a, b} {
		if _, err := s.GetMessage(ctx, "acc", m.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s survived expunge: %v", m.Subject, err)
		}
		if fileExists(t, s.MessageRawPath("acc", m.ID)) {
			t.Errorf("%s file survived expunge", m.Subject)
		}
	}
	if _, err := s.GetMessage(ctx, "acc", c.ID); err != nil {
		t.Errorf("c removed: %v", err)
	}
	if after, _ := s.CountPendingOps(ctx, "acc"); after != before-1 {
		t.Errorf("ops: %d → %d", before, after)
	}
	if err := s.DeleteMessagesByUID(ctx, inbox.ID, nil); err != nil {
		t.Errorf("empty: %v", err)
	}
}

func TestSetMessageBodyAndUnfetched(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	old := seedMessage(t, s, inbox, 1, "old", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	newer := seedMessage(t, s, inbox, 2, "new", time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	bare := &Message{AccountID: "acc", FolderID: inbox.ID, UID: 3}
	if err := s.UpsertMessages(ctx, []*Message{bare}); err != nil {
		t.Fatal(err)
	}
	pending := &Message{AccountID: "acc", FolderID: inbox.ID, UID: 0, Subject: "pending"}
	if err := s.UpsertMessages(ctx, []*Message{pending}); err != nil {
		t.Fatal(err)
	}

	refs, err := s.ListUnfetched(ctx, inbox.ID, 2)
	if err != nil || len(refs) != 2 || refs[0].ID != newer.ID || refs[0].UID != 2 || refs[0].Size != 100 || refs[1].ID != old.ID {
		t.Fatalf("unfetched: %+v %v", refs, err)
	}
	if refs, _ := s.ListUnfetched(ctx, inbox.ID, 0); len(refs) != 3 {
		t.Errorf("unfetched all: %d", len(refs))
	}

	when := time.Date(2026, 3, 3, 3, 3, 3, 0, time.UTC)
	upd := BodyUpdate{Text: "hello", HasHTML: true, Snippet: "hello", Attachments: []api.Attachment{{PartID: "2", Filename: "x"}},
		HasAttachments: true, Headers: map[string]string{"Precedence": "bulk"}, References: []string{"<r@y>"},
		Subject: "parsed", From: []api.Address{{Address: "p@example.invalid"}}, Date: when, RFCMessageID: "<p@y>", InReplyTo: "<q@y>"}
	if err := s.SetMessageBody(ctx, old.ID, upd); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMessageBody(ctx, bare.ID, upd); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetMessage(ctx, "acc", old.ID)
	if got.Subject != "old" || got.From[0].Address != "alice@example.invalid" || !got.Date.Equal(old.Date) ||
		got.RFCMessageID != old.RFCMessageID {
		t.Errorf("envelope overwritten by fill-in: %+v", got)
	}
	if got.InReplyTo != "<q@y>" { // was empty → filled in
		t.Errorf("empty column not filled in: %+v", got)
	}
	if got.BodyState != BodyFetched || !got.HasHTML || got.Snippet != "hello" || len(got.Attachments) != 1 ||
		!got.HasAttachments || got.Headers["Precedence"] != "bulk" || len(got.References) != 1 {
		t.Errorf("body fields not stored: %+v", got)
	}
	got, _ = s.GetMessage(ctx, "acc", bare.ID)
	if got.Subject != "parsed" || got.From[0].Address != "p@example.invalid" || !got.Date.Equal(when) ||
		got.RFCMessageID != "<p@y>" || got.InReplyTo != "<q@y>" {
		t.Errorf("fill-in skipped: %+v", got)
	}
	text, html, state, err := s.GetMessageText(ctx, "acc", old.ID)
	if err != nil || text != "hello" || !html || state != BodyFetched {
		t.Errorf("text: %q %v %s %v", text, html, state, err)
	}
	if _, _, _, err := s.GetMessageText(ctx, "other", old.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign text: %v", err)
	}
	if err := s.MarkBodyState(ctx, newer.ID, BodyTooBig); err != nil {
		t.Fatal(err)
	}
	if _, _, state, _ := s.GetMessageText(ctx, "acc", newer.ID); state != BodyTooBig {
		t.Errorf("state = %s", state)
	}
	if refs, _ := s.ListUnfetched(ctx, inbox.ID, 10); len(refs) != 0 {
		t.Errorf("still unfetched: %+v", refs)
	}
	if err := s.SetMessageBody(ctx, "m_nope", upd); !errors.Is(err, ErrNotFound) {
		t.Errorf("body unknown: %v", err)
	}
	if err := s.MarkBodyState(ctx, "m_nope", BodyFailed); !errors.Is(err, ErrNotFound) {
		t.Errorf("state unknown: %v", err)
	}
	if err := s.MarkBodyState(ctx, old.ID, "bogus"); err == nil {
		t.Error("bogus state accepted (CHECK)")
	}
}

func TestWriteMessageRaw(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	n, err := s.WriteMessageRaw(ctx, "acc", "m_1", strings.NewReader("raw"), 10)
	if err != nil || n != 3 {
		t.Fatalf("write: %d %v", n, err)
	}
	path := s.MessageRawPath("acc", "m_1")
	if path != filepath.Join(s.MessageDir(), "acc", "m_1") {
		t.Errorf("path = %s", path)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file: %v %v", info, err)
	}
	dir, _ := os.Stat(filepath.Dir(path))
	if dir.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v", dir.Mode().Perm())
	}
	f, err := s.OpenMessageRaw(ctx, "acc", "m_1")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	buf.ReadFrom(f)
	f.Close()
	if buf.String() != "raw" {
		t.Errorf("content = %q", buf.String())
	}
	// Overwrite is fine; exactly limit bytes is fine; limit+1 is ErrTooBig
	// and leaves nothing behind (the earlier file included).
	if _, err := s.WriteMessageRaw(ctx, "acc", "m_1", strings.NewReader("0123456789"), 10); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteMessageRaw(ctx, "acc", "m_2", bytes.NewReader(make([]byte, 11)), 10); !errors.Is(err, ErrTooBig) {
		t.Fatalf("over limit: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(s.MessageDir(), "acc"))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") || e.Name() == "m_2" {
			t.Errorf("left behind: %s", e.Name())
		}
	}
	// A stale tmp from a crash does not block the next write.
	os.WriteFile(s.MessageRawPath("acc", "m_3")+".tmp", []byte("x"), 0o600)
	if _, err := s.WriteMessageRaw(ctx, "acc", "m_3", strings.NewReader("ok"), 10); err != nil {
		t.Errorf("stale tmp: %v", err)
	}
	if _, err := s.OpenMessageRaw(ctx, "acc", "m_nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("open unknown: %v", err)
	}
	for _, bad := range []string{"", ".", "..", "../x", "a/b"} {
		if _, err := s.WriteMessageRaw(ctx, bad, "m_1", strings.NewReader("x"), 10); err == nil {
			t.Errorf("account %q accepted", bad)
		}
		if _, err := s.OpenMessageRaw(ctx, "acc", bad); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("id %q: %v", bad, err)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(s.MessageDir()), "x")); err == nil {
		t.Error("escaped the message directory")
	}
}
