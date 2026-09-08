// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// seedDraft stores a draft with the given attachments bound.
func seedDraft(t *testing.T, s *Store, account string, attachmentIDs ...string) Draft {
	t.Helper()
	d := Draft{AccountID: account, Subject: "draft", To: []api.Address{{Address: "to@example.invalid"}}, TextBody: "hello"}
	if err := s.SaveDraft(context.Background(), &d, attachmentIDs); err != nil {
		t.Fatal(err)
	}
	return d
}

// enqueueInput is a valid EnqueueOutbox request for the draft.
func enqueueInput(d Draft, raw string) EnqueueInput {
	return EnqueueInput{
		DraftID: d.ID, DraftVersion: d.Version,
		Message: Message{
			AccountID: d.AccountID,
			From:      []api.Address{{Name: "Me", Address: "me@example.invalid"}},
			To:        []api.Address{{Address: "to@example.invalid"}},
			CC:        []api.Address{{Address: "cc@example.invalid"}},
			BCC:       []api.Address{{Address: "bcc@example.invalid"}},
			Subject:   "queued", Date: time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC),
			RFCMessageID: "<q@example.invalid>", InReplyTo: "<p@example.invalid>", References: []string{"<p@example.invalid>"},
			Snippet: "hello", HasAttachments: true,
			Attachments: []api.Attachment{{PartID: "2", Filename: "a.txt", ContentType: "text/plain", Size: 3}},
		},
		Text:         "hello",
		EnvelopeFrom: "me@example.invalid",
		Recipients:   []string{"to@example.invalid", "cc@example.invalid", "bcc@example.invalid", "cc@example.invalid"},
		Build: func(w io.Writer) error {
			_, err := io.WriteString(w, raw)
			return err
		},
		Limit: 1 << 20,
	}
}

// seedOutbox queues one message and returns it with its entry.
func seedOutbox(t *testing.T, s *Store, account string) (Message, OutboxEntry) {
	t.Helper()
	d := seedDraft(t, s, account)
	m, err := s.EnqueueOutbox(context.Background(), enqueueInput(d, "Subject: q\r\n\r\nbody"))
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.GetOutbox(context.Background(), account, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	return m, e
}

func TestOutboxFolder(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	f, err := s.OutboxFolder(ctx, "acc")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(f.ID, "f_") || f.AccountID != "acc" || f.Role != api.RoleOutbox || f.Mailbox != "" ||
		f.Name != "Outbox" || f.Path != "Outbox" || !f.Selectable || !f.Subscribed || f.Position != -1 || f.CreatedAt.IsZero() {
		t.Fatalf("outbox folder: %+v", f)
	}
	again, err := s.OutboxFolder(ctx, "acc")
	if err != nil || again.ID != f.ID {
		t.Fatalf("second call: %+v %v", again, err)
	}
	other, _ := s.OutboxFolder(ctx, "other")
	if other.ID == f.ID {
		t.Error("shared between accounts")
	}
	if got, err := s.FolderByRole(ctx, "acc", api.RoleOutbox); err != nil || got.ID != f.ID {
		t.Errorf("by role: %+v %v", got, err)
	}
	if _, err := s.OutboxFolder(ctx, ""); err == nil {
		t.Error("empty account accepted")
	}

	// A server folder list neither removes nor reports it, and cannot claim
	// its reserved mailbox or role.
	inbox := Folder{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Selectable: true, Subscribed: true}
	stored, removed, err := s.UpsertFolders(ctx, "acc", []Folder{inbox})
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].Mailbox != "INBOX" || len(removed) != 0 {
		t.Fatalf("upsert with outbox present: %+v removed %v", stored, removed)
	}
	if got, err := s.GetFolder(ctx, "acc", f.ID); err != nil || got.Role != api.RoleOutbox {
		t.Errorf("outbox folder after upsert: %+v %v", got, err)
	}
	if list, _ := s.ListFolders(ctx, "acc"); len(list) != 2 {
		t.Errorf("list = %+v", list)
	}
	if _, _, err := s.UpsertFolders(ctx, "acc", []Folder{inbox, {Mailbox: "", Name: "x", Path: "x"}}); err == nil {
		t.Error("empty mailbox accepted")
	}
	if _, _, err := s.UpsertFolders(ctx, "acc", []Folder{inbox, {Mailbox: "Out", Name: "Out", Path: "Out", Role: api.RoleOutbox}}); err == nil {
		t.Error("outbox role accepted")
	}
	if list, _ := s.ListFolders(ctx, "acc"); len(list) != 2 {
		t.Errorf("rejected batches changed the list: %+v", list)
	}
}

func TestEnqueueOutbox(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	a1 := importTestAttachment(t, s, "acc", "one.txt", "one")
	a2 := importTestAttachment(t, s, "acc", "two.txt", "two")
	d := seedDraft(t, s, "acc", a1.ID, a2.ID)
	raw := "From: me@example.invalid\r\nSubject: queued\r\n\r\nhello"

	m, err := s.EnqueueOutbox(ctx, enqueueInput(d, raw))
	if err != nil {
		t.Fatal(err)
	}
	outbox, err := s.OutboxFolder(ctx, "acc")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(m.ID, "m_") || m.AccountID != "acc" || m.FolderID != outbox.ID || m.UID != 0 || m.ModSeq != 0 ||
		fmt.Sprint(m.Flags) != fmt.Sprint([]api.Flag{api.FlagSeen}) || m.Size != int64(len(raw)) || m.HasHTML ||
		m.BodyState != BodyFetched || !strings.HasPrefix(m.ThreadID, "t_") || m.Subject != "queued" || m.From[0].Address != "me@example.invalid" ||
		len(m.To) != 1 || len(m.CC) != 1 || len(m.BCC) != 1 || m.RFCMessageID != "<q@example.invalid>" ||
		m.InReplyTo != "<p@example.invalid>" || len(m.References) != 1 || m.Snippet != "hello" || !m.HasAttachments ||
		len(m.Attachments) != 1 || m.CreatedAt.IsZero() || !m.Date.Equal(time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("stored message: %+v", m)
	}
	got, err := s.GetMessage(ctx, "acc", m.ID)
	if err != nil || got.FolderID != outbox.ID {
		t.Fatalf("get: %+v %v", got, err)
	}
	text, html, state, err := s.GetMessageText(ctx, "acc", m.ID)
	if err != nil || text != "hello" || html || state != BodyFetched {
		t.Errorf("text: %q %v %s %v", text, html, state, err)
	}
	f, err := s.OpenMessageRaw(ctx, "acc", m.ID)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	buf.ReadFrom(f)
	f.Close()
	if buf.String() != raw {
		t.Errorf("raw = %q", buf.String())
	}
	if folder, _ := s.GetFolder(ctx, "acc", outbox.ID); folder.Total != 1 || folder.Unread != 0 {
		t.Errorf("recount: %d/%d", folder.Unread, folder.Total)
	}
	if n, _ := s.CountOutbox(ctx, "acc"); n != 1 {
		t.Errorf("count = %d", n)
	}
	e, err := s.GetOutbox(ctx, "acc", m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if e.MessageID != m.ID || e.AccountID != "acc" || e.EnvelopeFrom != "me@example.invalid" || e.State != OutboxQueued ||
		e.Attempts != 0 || !e.NextAttemptAt.IsZero() || e.LastErrorCode != 0 || e.LastError != "" || e.CreatedAt.IsZero() ||
		fmt.Sprint(e.Recipients) != fmt.Sprint([]string{"to@example.invalid", "cc@example.invalid", "bcc@example.invalid"}) {
		t.Errorf("entry: %+v", e)
	}
	if _, err := s.GetOutbox(ctx, "other", m.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign entry: %v", err)
	}
	entries, err := s.OutboxEntries(ctx, "acc", []string{m.ID, "m_nope"})
	if err != nil || len(entries) != 1 || entries[m.ID].State != OutboxQueued {
		t.Errorf("entries: %+v %v", entries, err)
	}
	if entries, err := s.OutboxEntries(ctx, "acc", nil); err != nil || len(entries) != 0 {
		t.Errorf("no ids: %+v %v", entries, err)
	}

	// The draft and its attachments (rows and files) are gone.
	if _, err := s.GetDraft(ctx, "acc", d.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("draft kept: %v", err)
	}
	for _, a := range []Attachment{a1, a2} {
		if _, err := s.GetAttachments(ctx, "acc", []string{a.ID}); !errors.Is(err, ErrNotFound) {
			t.Errorf("attachment row %s kept: %v", a.ID, err)
		}
		if fileExists(t, s.AttachmentPath(a.ID)) {
			t.Errorf("attachment file %s kept", a.ID)
		}
	}
	// No operation was queued for the outbox row.
	if n, _ := s.CountPendingOps(ctx, "acc"); n != 0 {
		t.Errorf("ops = %d", n)
	}
	// The row does not disturb the sync engine's views of the folder.
	if uids, _ := s.ListUIDs(ctx, outbox.ID); len(uids) != 0 {
		t.Errorf("uids: %v", uids)
	}
	if refs, _ := s.ListUnfetched(ctx, outbox.ID, 10); len(refs) != 0 {
		t.Errorf("unfetched: %+v", refs)
	}
	// Listing the outbox folder works like any other.
	if items, _, total, err := s.ListMessages(ctx, "acc", outbox.ID, "", 10, "", api.FilterAll); err != nil || total != 1 || len(items) != 1 || items[0].ID != m.ID {
		t.Errorf("list: %+v %d %v", items, total, err)
	}
}

func TestEnqueueOutboxFailuresLeaveNothing(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	a := importTestAttachment(t, s, "acc", "one.txt", "one")
	d := seedDraft(t, s, "acc", a.ID)

	rawFiles := func() []string {
		t.Helper()
		var out []string
		filepath.WalkDir(s.MessageDir(), func(path string, e os.DirEntry, err error) error {
			if err == nil && !e.IsDir() {
				out = append(out, path)
			}
			return nil
		})
		return out
	}
	intact := func(what string) {
		t.Helper()
		if files := rawFiles(); len(files) != 0 {
			t.Errorf("%s: raw files left: %v", what, files)
		}
		got, err := s.GetDraft(ctx, "acc", d.ID)
		if err != nil || got.Version != d.Version || len(got.Attachments) != 1 {
			t.Errorf("%s: draft changed: %+v %v", what, got, err)
		}
		if !fileExists(t, s.AttachmentPath(a.ID)) {
			t.Errorf("%s: attachment file gone", what)
		}
		if n, _ := s.CountOutbox(ctx, "acc"); n != 0 {
			t.Errorf("%s: outbox count = %d", what, n)
		}
		var messages int
		s.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&messages)
		if messages != 0 {
			t.Errorf("%s: %d message rows", what, messages)
		}
	}

	built := 0
	in := enqueueInput(d, "raw")
	in.Build = func(w io.Writer) error { built++; _, err := io.WriteString(w, "raw"); return err }

	stale := in
	stale.DraftVersion = d.Version + 1
	if _, err := s.EnqueueOutbox(ctx, stale); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("version conflict: %v", err)
	}
	intact("version conflict")
	unknown := in
	unknown.DraftID = "d_nope"
	if _, err := s.EnqueueOutbox(ctx, unknown); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown draft: %v", err)
	}
	foreign := in
	foreign.Message.AccountID = "other"
	if _, err := s.EnqueueOutbox(ctx, foreign); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign draft: %v", err)
	}
	intact("unknown draft")
	if built != 0 {
		t.Errorf("message built for a bad draft %d times", built)
	}

	// The limit stops a runaway builder early and cleans up.
	writes := 0
	big := in
	big.Limit = 10
	big.Build = func(w io.Writer) error {
		for {
			writes++
			if _, err := w.Write([]byte("0123456789")); err != nil {
				return err
			}
		}
	}
	if _, err := s.EnqueueOutbox(ctx, big); !errors.Is(err, ErrTooBig) {
		t.Fatalf("over limit: %v", err)
	}
	if writes != 2 {
		t.Errorf("builder not stopped early: %d writes", writes)
	}
	intact("too big")
	// A builder that swallows the write error still cannot store a
	// truncated message.
	swallow := big
	swallow.Build = func(w io.Writer) error {
		w.Write([]byte("0123456789"))
		w.Write([]byte("x"))
		return nil
	}
	if _, err := s.EnqueueOutbox(ctx, swallow); !errors.Is(err, ErrTooBig) {
		t.Fatalf("swallowed limit: %v", err)
	}
	intact("swallowed limit")
	// A failing builder.
	broken := in
	broken.Build = func(w io.Writer) error { return errors.New("boom") }
	if _, err := s.EnqueueOutbox(ctx, broken); err == nil || errors.Is(err, ErrTooBig) {
		t.Fatalf("broken builder: %v", err)
	}
	intact("broken builder")
	if _, err := s.EnqueueOutbox(ctx, EnqueueInput{DraftID: d.ID, DraftVersion: d.Version, Build: in.Build}); err == nil {
		t.Error("empty account accepted")
	}
	noBuild := in
	noBuild.Build = nil
	if _, err := s.EnqueueOutbox(ctx, noBuild); err == nil {
		t.Error("nil builder accepted")
	}
	intact("bad input")

	// After all that the draft still enqueues.
	if _, err := s.EnqueueOutbox(ctx, in); err != nil {
		t.Fatal(err)
	}
	if built != 1 {
		t.Errorf("built %d times", built)
	}
}

func TestWriteMessageRawFunc(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	n, err := s.WriteMessageRawFunc(ctx, "acc", "m_1", 10, func(w io.Writer) error {
		for i := 0; i < 10; i++ {
			if _, err := w.Write([]byte("x")); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil || n != 10 {
		t.Fatalf("exactly the limit: %d %v", n, err)
	}
	if _, err := s.WriteMessageRawFunc(ctx, "acc", "m_1", 10, func(w io.Writer) error {
		_, err := w.Write(make([]byte, 11))
		return err
	}); !errors.Is(err, ErrTooBig) {
		t.Fatalf("over limit: %v", err)
	}
	// The earlier file is untouched by the failed rewrite; no tmp remains.
	if info, err := os.Stat(s.MessageRawPath("acc", "m_1")); err != nil || info.Size() != 10 {
		t.Errorf("earlier file: %v %v", info, err)
	}
	entries, _ := os.ReadDir(filepath.Join(s.MessageDir(), "acc"))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("tmp left: %s", e.Name())
		}
	}
	if _, err := s.WriteMessageRawFunc(ctx, "acc", "m_2", 10, nil); err == nil {
		t.Error("nil producer accepted")
	}
	if _, err := s.WriteMessageRawFunc(ctx, "../x", "m_2", 10, func(io.Writer) error { return nil }); err == nil {
		t.Error("bad account accepted")
	}
}

func TestOutboxStateMachine(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	m, _ := seedOutbox(t, s, "acc")
	id := m.ID
	now := time.Now()

	// Only the expected source state transitions.
	for what, err := range map[string]error{
		"sent from queued":          s.MarkOutboxSent(ctx, id),
		"retry from queued":         s.MarkOutboxRetry(ctx, id, api.CodeUnavailable, "x", now),
		"failed from queued":        s.MarkOutboxFailed(ctx, id, api.CodeUnavailable, "x"),
		"append failed from queued": s.MarkOutboxAppendFailed(ctx, id, "x", now),
		"sending unknown":           s.MarkOutboxSending(ctx, "m_nope"),
	} {
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v", what, err)
		}
	}
	if e, _ := s.GetOutbox(ctx, "acc", id); e.State != OutboxQueued || e.Attempts != 0 {
		t.Fatalf("changed by refused transitions: %+v", e)
	}

	if err := s.MarkOutboxSending(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkOutboxSending(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("sending twice: %v", err)
	}
	if e, _ := s.GetOutbox(ctx, "acc", id); e.State != OutboxSending || e.Attempts != 0 {
		t.Fatalf("after sending: %+v", e)
	}
	if n, _ := s.CountOutbox(ctx, "acc"); n != 1 {
		t.Errorf("count while sending = %d", n)
	}
	if err := s.RetryOutbox(ctx, "acc", id); !errors.Is(err, ErrOutboxBusy) {
		t.Errorf("retry while sending: %v", err)
	}

	// Transient failure: back to queued with attempts, error, retry time.
	retry := now.Add(time.Minute).UTC().Truncate(time.Millisecond)
	long := strings.Repeat("é", 150) // 300 bytes
	if err := s.MarkOutboxRetry(ctx, id, api.CodeUnavailable, long, retry); err != nil {
		t.Fatal(err)
	}
	e, _ := s.GetOutbox(ctx, "acc", id)
	if e.State != OutboxQueued || e.Attempts != 1 || !e.NextAttemptAt.Equal(retry) || e.LastErrorCode != api.CodeUnavailable ||
		e.LastError != strings.Repeat("é", 100) || e.UpdatedAt.Before(e.CreatedAt) {
		t.Errorf("after retry: %+v", e)
	}
	if _, ok, _ := s.NextOutbox(ctx, "acc", now); ok {
		t.Error("deferred entry offered before its time")
	}
	if got, ok, _ := s.NextOutbox(ctx, "acc", retry); !ok || got.MessageID != id {
		t.Errorf("deferred entry not offered at its time: %+v %v", got, ok)
	}
	if due, ok, _ := s.NextOutboxDue(ctx, "acc"); !ok || !due.Equal(retry) {
		t.Errorf("next due: %v %v", due, ok)
	}
	// RetryOutbox makes it due now, keeping the last error.
	if err := s.RetryOutbox(ctx, "acc", id); err != nil {
		t.Fatal(err)
	}
	e, _ = s.GetOutbox(ctx, "acc", id)
	if e.State != OutboxQueued || !e.NextAttemptAt.IsZero() || e.LastErrorCode != api.CodeUnavailable || e.LastError == "" || e.Attempts != 1 {
		t.Errorf("after user retry: %+v", e)
	}
	if _, ok, _ := s.NextOutboxDue(ctx, "acc"); ok {
		t.Error("due time left after retry")
	}
	if got, ok, _ := s.NextOutbox(ctx, "acc", now); !ok || got.MessageID != id {
		t.Errorf("not due after retry: %+v %v", got, ok)
	}

	// Permanent failure.
	if err := s.MarkOutboxSending(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkOutboxFailed(ctx, id, api.CodeInvalidArgument, "550 no such user\x00"+string([]byte{0xff})); err != nil {
		t.Fatal(err)
	}
	e, _ = s.GetOutbox(ctx, "acc", id)
	if e.State != OutboxFailed || e.Attempts != 2 || e.LastErrorCode != api.CodeInvalidArgument || e.LastError != "550 no such user\x00" {
		t.Errorf("after failed: %+v", e)
	}
	if n, _ := s.CountOutbox(ctx, "acc"); n != 0 {
		t.Errorf("failed counted as pending: %d", n)
	}
	if _, ok, _ := s.NextOutbox(ctx, "acc", now.Add(time.Hour)); ok {
		t.Error("failed entry offered")
	}
	if list, _ := s.ListOutbox(ctx, "acc", OutboxFailed, now); len(list) != 1 || list[0].MessageID != id {
		t.Errorf("failed list: %+v", list)
	}
	if err := s.RetryOutbox(ctx, "acc", id); err != nil {
		t.Fatal(err)
	}
	if e, _ = s.GetOutbox(ctx, "acc", id); e.State != OutboxQueued || e.Attempts != 2 {
		t.Errorf("after retry of failed: %+v", e)
	}
	if err := s.RetryOutbox(ctx, "other", id); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign retry: %v", err)
	}
	if err := s.RetryOutbox(ctx, "acc", "m_nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown retry: %v", err)
	}

	// Delivery, then the Sent copy.
	if err := s.MarkOutboxSending(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkOutboxSent(ctx, id); err != nil {
		t.Fatal(err)
	}
	e, _ = s.GetOutbox(ctx, "acc", id)
	if e.State != OutboxSent || e.Attempts != 3 || e.LastErrorCode != 0 || e.LastError != "" || !e.NextAttemptAt.IsZero() {
		t.Errorf("after sent: %+v", e)
	}
	if n, _ := s.CountOutbox(ctx, "acc"); n != 0 {
		t.Errorf("sent counted as pending: %d", n)
	}
	if err := s.RetryOutbox(ctx, "acc", id); !errors.Is(err, ErrOutbox) {
		t.Errorf("retry of sent: %v", err)
	}
	if err := s.MarkOutboxAppendFailed(ctx, id, "APPEND failed", retry); err != nil {
		t.Fatal(err)
	}
	e, _ = s.GetOutbox(ctx, "acc", id)
	if e.State != OutboxSent || e.Attempts != 4 || e.LastError != "APPEND failed" || !e.NextAttemptAt.Equal(retry) {
		t.Errorf("after append failed: %+v", e)
	}
	if list, _ := s.ListOutbox(ctx, "acc", OutboxSent, now); len(list) != 0 {
		t.Errorf("sent listed before its retry time: %+v", list)
	}
	if list, _ := s.ListOutbox(ctx, "acc", OutboxSent, retry); len(list) != 1 {
		t.Errorf("sent not listed at its retry time: %+v", list)
	}
	if _, ok, _ := s.NextOutboxDue(ctx, "acc"); ok {
		t.Error("a sent entry's retry time reported as queued due")
	}
	if err := s.MarkOutboxSending(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("sending from sent: %v", err)
	}
	if err := s.DeleteOutboxMessage(ctx, "acc", id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetOutbox(ctx, "acc", id); !errors.Is(err, ErrNotFound) {
		t.Errorf("entry after delete: %v", err)
	}
	if _, err := s.GetMessage(ctx, "acc", id); !errors.Is(err, ErrNotFound) {
		t.Errorf("row after delete: %v", err)
	}
	if fileExists(t, s.MessageRawPath("acc", id)) {
		t.Error("raw file after delete")
	}
	outbox, _ := s.OutboxFolder(ctx, "acc")
	if f, _ := s.GetFolder(ctx, "acc", outbox.ID); f.Total != 0 {
		t.Errorf("recount after delete: %d", f.Total)
	}
	if n, _ := s.CountPendingOps(ctx, "acc"); n != 0 {
		t.Errorf("ops after delete: %d", n)
	}
	if err := s.DeleteOutboxMessage(ctx, "acc", id); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
}

func TestOutboxListOrderDeferReset(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	first, _ := seedOutbox(t, s, "acc")
	time.Sleep(2 * time.Millisecond) // distinct created_at
	second, _ := seedOutbox(t, s, "acc")
	time.Sleep(2 * time.Millisecond)
	third, _ := seedOutbox(t, s, "acc")
	seedOutbox(t, s, "other")
	now := time.Now()

	list, err := s.ListOutbox(ctx, "acc", OutboxQueued, now)
	if err != nil || len(list) != 3 || list[0].MessageID != first.ID || list[1].MessageID != second.ID || list[2].MessageID != third.ID {
		t.Fatalf("queued list: %+v %v", list, err)
	}
	if got, ok, _ := s.NextOutbox(ctx, "acc", now); !ok || got.MessageID != first.ID {
		t.Errorf("next = %+v %v", got, ok)
	}
	if n, _ := s.CountOutbox(ctx, "acc"); n != 3 {
		t.Errorf("count = %d", n)
	}
	if n, _ := s.CountOutbox(ctx, "nobody"); n != 0 {
		t.Errorf("count for unknown account = %d", n)
	}
	if _, ok, _ := s.NextOutboxDue(ctx, "acc"); ok {
		t.Error("due time without deferral")
	}

	// Defer everything queued; a sending one is untouched.
	if err := s.MarkOutboxSending(ctx, third.ID); err != nil {
		t.Fatal(err)
	}
	until := now.Add(10 * time.Minute).UTC().Truncate(time.Millisecond)
	n, err := s.DeferOutbox(ctx, "acc", until, api.CodeAuthRequired, "no credentials")
	if err != nil || n != 2 {
		t.Fatalf("defer: %d %v", n, err)
	}
	if _, ok, _ := s.NextOutbox(ctx, "acc", now); ok {
		t.Error("deferred entry offered")
	}
	if due, ok, _ := s.NextOutboxDue(ctx, "acc"); !ok || !due.Equal(until) {
		t.Errorf("due after defer: %v %v", due, ok)
	}
	e, _ := s.GetOutbox(ctx, "acc", first.ID)
	if e.State != OutboxQueued || e.Attempts != 0 || !e.NextAttemptAt.Equal(until) || e.LastErrorCode != api.CodeAuthRequired || e.LastError != "no credentials" {
		t.Errorf("deferred entry: %+v", e)
	}
	if e, _ := s.GetOutbox(ctx, "acc", third.ID); e.State != OutboxSending || !e.NextAttemptAt.IsZero() {
		t.Errorf("sending entry touched by defer: %+v", e)
	}
	if e, _ := s.GetOutbox(ctx, "other", (mustList(t, s, "other"))[0].MessageID); !e.NextAttemptAt.IsZero() {
		t.Errorf("other account deferred: %+v", e)
	}
	if got, ok, _ := s.NextOutbox(ctx, "acc", until); !ok || got.MessageID != first.ID {
		t.Errorf("not offered at the deferral time: %+v %v", got, ok)
	}

	// Worker restart: sending → queued (one more attempt), everything due.
	if err := s.ResetOutbox(ctx, "acc"); err != nil {
		t.Fatal(err)
	}
	list, _ = s.ListOutbox(ctx, "acc", OutboxQueued, now)
	if len(list) != 3 {
		t.Fatalf("after reset: %+v", list)
	}
	for _, e := range list {
		wantAttempts := 0
		if e.MessageID == third.ID {
			wantAttempts = 1
		}
		if !e.NextAttemptAt.IsZero() || e.Attempts != wantAttempts {
			t.Errorf("after reset: %+v", e)
		}
	}
	if _, ok, _ := s.NextOutboxDue(ctx, "acc"); ok {
		t.Error("due time after reset")
	}
	if n, _ := s.CountOutbox(ctx, "acc"); n != 3 {
		t.Errorf("count after reset = %d", n)
	}
}

func mustList(t *testing.T, s *Store, account string) []OutboxEntry {
	t.Helper()
	list, err := s.ListOutbox(context.Background(), account, OutboxQueued, time.Now())
	if err != nil || len(list) == 0 {
		t.Fatalf("list %s: %+v %v", account, list, err)
	}
	return list
}

func TestOutboxMutationsGuarded(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	trash := seedFolder(t, s, "acc", "Trash", api.RoleTrash)
	normal := seedMessage(t, s, inbox, 1, "normal", time.Now())
	queued, _ := seedOutbox(t, s, "acc")
	sending, _ := seedOutbox(t, s, "acc")
	if err := s.MarkOutboxSending(ctx, sending.ID); err != nil {
		t.Fatal(err)
	}
	outbox, _ := s.OutboxFolder(ctx, "acc")

	// Flags and moves never touch an outbox row; the outbox is no target.
	if err := s.FlagMessages(ctx, "acc", []string{normal.ID, queued.ID}, []api.Flag{api.FlagFlagged}, nil); !errors.Is(err, ErrOutbox) {
		t.Errorf("flag: %v", err)
	}
	if err := s.MoveMessages(ctx, "acc", []string{normal.ID, queued.ID}, trash.ID); !errors.Is(err, ErrOutbox) {
		t.Errorf("move from outbox: %v", err)
	}
	if err := s.MoveMessages(ctx, "acc", []string{normal.ID}, outbox.ID); !errors.Is(err, ErrOutbox) {
		t.Errorf("move into outbox: %v", err)
	}
	if err := s.TrashMessages(ctx, "acc", []string{normal.ID, sending.ID}, trash.ID); !errors.Is(err, ErrOutboxBusy) {
		t.Errorf("trash while sending: %v", err)
	}
	if err := s.DeleteMessages(ctx, "acc", []string{normal.ID, sending.ID}); !errors.Is(err, ErrOutboxBusy) {
		t.Errorf("delete while sending: %v", err)
	}
	got, _ := s.GetMessage(ctx, "acc", normal.ID)
	if got.FolderID != inbox.ID || len(got.Flags) != 0 {
		t.Errorf("normal message changed by refused batch: %+v", got)
	}
	if _, err := s.GetMessage(ctx, "acc", sending.ID); err != nil {
		t.Errorf("sending row changed: %v", err)
	}
	if n, _ := s.CountPendingOps(ctx, "acc"); n != 0 {
		t.Errorf("ops after refused batches: %d", n)
	}

	// Trash deletes an outbox row permanently, the rest moves as usual.
	if err := s.TrashMessages(ctx, "acc", []string{normal.ID, queued.ID}, trash.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMessage(ctx, "acc", queued.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("trashed outbox row kept: %v", err)
	}
	if _, err := s.GetOutbox(ctx, "acc", queued.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("outbox entry kept: %v", err)
	}
	if fileExists(t, s.MessageRawPath("acc", queued.ID)) {
		t.Error("raw file of trashed outbox row kept")
	}
	if got, _ := s.GetMessage(ctx, "acc", normal.ID); got.FolderID != trash.ID {
		t.Errorf("normal message not trashed: %+v", got)
	}
	ops, _ := s.NextOps(ctx, "acc", time.Now(), 10)
	if len(ops) != 1 || ops[0].MessageID != normal.ID || ops[0].Kind != OpMove {
		t.Errorf("ops = %+v (want only the move)", ops)
	}
	if f, _ := s.GetFolder(ctx, "acc", outbox.ID); f.Total != 1 || f.Unread != 0 {
		t.Errorf("outbox recount: %d/%d", f.Unread, f.Total)
	}
	if n, _ := s.CountOutbox(ctx, "acc"); n != 1 {
		t.Errorf("count = %d", n)
	}

	// Once sending is over, delete works and queues nothing.
	if err := s.MarkOutboxFailed(ctx, sending.ID, api.CodeUnavailable, "x"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMessages(ctx, "acc", []string{sending.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMessage(ctx, "acc", sending.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted outbox row kept: %v", err)
	}
	if fileExists(t, s.MessageRawPath("acc", sending.ID)) {
		t.Error("raw file kept")
	}
	if n, _ := s.CountPendingOps(ctx, "acc"); n != 1 {
		t.Errorf("ops after outbox delete: %d", n)
	}
	if f, _ := s.GetFolder(ctx, "acc", outbox.ID); f.Total != 0 {
		t.Errorf("outbox recount: %d", f.Total)
	}
	if err := s.DeleteOutboxMessage(ctx, "acc", normal.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteOutboxMessage on a normal message: %v", err)
	}
	if _, err := s.GetMessage(ctx, "acc", normal.ID); err != nil {
		t.Errorf("normal message removed: %v", err)
	}
}

func TestOutboxSurvivesSyncMaintenance(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	m, _ := seedOutbox(t, s, "acc")
	outbox, _ := s.OutboxFolder(ctx, "acc")
	past := time.Now().Add(-2 * time.Hour).UTC().Format(timeLayout)
	if _, err := s.db.Exec(`UPDATE messages SET updated_at = ? WHERE id = ?`, past, m.ID); err != nil {
		t.Fatal(err)
	}
	n, err := s.DeleteStalePending(ctx, outbox.ID, time.Now())
	if err != nil || n != 0 {
		t.Fatalf("stale pending on outbox: %d %v", n, err)
	}
	if _, err := s.GetMessage(ctx, "acc", m.ID); err != nil {
		t.Errorf("outbox row removed as stale: %v", err)
	}
	if n, err := s.DeleteStalePending(ctx, "f_nope", time.Now()); err != nil || n != 0 {
		t.Errorf("unknown folder: %d %v", n, err)
	}
	// A server folder list refresh keeps the queue.
	if _, _, err := s.UpsertFolders(ctx, "acc", []Folder{{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Selectable: true}}); err != nil {
		t.Fatal(err)
	}
	if e, err := s.GetOutbox(ctx, "acc", m.ID); err != nil || e.State != OutboxQueued {
		t.Errorf("entry after folder refresh: %+v %v", e, err)
	}
	if fileExists(t, s.MessageRawPath("acc", m.ID)) != true {
		t.Error("raw file lost")
	}
}

func TestDeleteAccountRemovesOutbox(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	acc := Account{Name: "Work", Enabled: true, Config: testAccountConfig("me@example.invalid")}
	if err := s.AddAccount(ctx, &acc); err != nil {
		t.Fatal(err)
	}
	m, _ := seedOutbox(t, s, acc.ID)
	keep, _ := seedOutbox(t, s, "other")

	if err := s.DeleteAccount(ctx, acc.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetOutbox(ctx, acc.ID, m.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("entry left: %v", err)
	}
	var rows int
	s.db.QueryRow(`SELECT COUNT(*) FROM outbox WHERE account_id = ?`, acc.ID).Scan(&rows)
	if rows != 0 {
		t.Errorf("outbox rows left: %d", rows)
	}
	if list, _ := s.ListFolders(ctx, acc.ID); len(list) != 0 {
		t.Errorf("outbox folder left: %+v", list)
	}
	if _, err := os.Stat(filepath.Join(s.MessageDir(), acc.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("message directory left: %v", err)
	}
	if n, _ := s.CountOutbox(ctx, acc.ID); n != 0 {
		t.Errorf("count = %d", n)
	}
	if e, err := s.GetOutbox(ctx, "other", keep.ID); err != nil || e.State != OutboxQueued {
		t.Errorf("other account's entry: %+v %v", e, err)
	}
}

func TestCapOutboxError(t *testing.T) {
	for in, want := range map[string]string{
		"":                             "",
		"short":                        "short",
		strings.Repeat("a", 200):       strings.Repeat("a", 200),
		strings.Repeat("a", 201):       strings.Repeat("a", 200),
		strings.Repeat("é", 100) + "x": strings.Repeat("é", 100),
		strings.Repeat("a", 199) + "é": strings.Repeat("a", 199),
		"bad\xffbyte":                  "badbyte",
	} {
		if got := capOutboxError(in); got != want {
			t.Errorf("cap(%q) = %q, want %q", in, got, want)
		}
	}
}
