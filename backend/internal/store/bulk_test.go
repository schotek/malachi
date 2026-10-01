// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/bulk"
	"github.com/schotek/malachi/backend/pkg/api"
)

func bulkRow(t *testing.T, s *Store, f Folder, uid uint32, headers map[string]string) *Message {
	t.Helper()
	m := &Message{AccountID: f.AccountID, FolderID: f.ID, UID: uid, Subject: "b", Headers: headers,
		From: []api.Address{{Address: "news@news.example"}}, Date: time.Now(), RFCMessageID: "<b@news.example>"}
	if err := s.UpsertMessages(context.Background(), []*Message{m}); err != nil {
		t.Fatal(err)
	}
	return m
}

// A row starts unclassified, keeps what the envelope classified, and the
// body ingest classifies the curated headers of the whole message.
func TestBulkClassificationAtEnvelopeAndBody(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	seedAccount(t, s, "acc")
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)

	plain := bulkRow(t, s, inbox, 1, nil)
	if got, _ := s.GetMessage(ctx, "acc", plain.ID); got.Bulk != "" || got.ListID != "" {
		t.Fatalf("new row: %q %q", got.Bulk, got.ListID)
	}

	env := &Message{AccountID: "acc", FolderID: inbox.ID, UID: 2, Subject: "e", Bulk: "list", ListID: "go.example.org",
		Headers: map[string]string{"List-Id": "<go.example.org>"}, Date: time.Now()}
	if err := s.UpsertMessages(ctx, []*Message{env}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetMessage(ctx, "acc", env.ID)
	if got.Bulk != "list" || got.ListID != "go.example.org" || got.Headers["List-Id"] != "<go.example.org>" {
		t.Fatalf("envelope: %+v", got)
	}
	// A second envelope pass over the row (flags sync) leaves it alone.
	again := &Message{AccountID: "acc", FolderID: inbox.ID, UID: 2, Flags: []api.Flag{api.FlagSeen}}
	if err := s.UpsertMessages(ctx, []*Message{again}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetMessage(ctx, "acc", env.ID); got.Bulk != "list" {
		t.Errorf("conflict upsert changed the classification: %q", got.Bulk)
	}

	if err := s.SetMessageBody(ctx, plain.ID, BodyUpdate{Text: "x", Headers: map[string]string{
		"List-Unsubscribe": "<https://news.example/u>", "List-Id": "<News.Example>"}}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetMessage(ctx, "acc", plain.ID)
	if got.Bulk != "newsletter" || got.ListID != "news.example" {
		t.Errorf("body: %q %q", got.Bulk, got.ListID)
	}
	if err := s.SetMessageBody(ctx, plain.ID, BodyUpdate{Text: "x"}); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.GetMessage(ctx, "acc", plain.ID); got.Bulk != "none" || got.ListID != "" {
		t.Errorf("personal mail: %q %q", got.Bulk, got.ListID)
	}
}

// Issue-tracker accounts are never classified.
func TestBulkNeverForIssueAccounts(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	if err := s.AddAccount(ctx, &Account{ID: "jira", Config: jiraConfig("jana@example.invalid", "https://acme.atlassian.net")}); err != nil {
		t.Fatal(err)
	}
	space, _, _ := jiraFolders(t, s, "jira")
	m := seedItem(t, s, space, "10", "i:10", "ITSD-1: x", time.Now())
	hdr := map[string]string{"List-Unsubscribe": "<https://acme.example/u>", "Auto-Submitted": "auto-generated"}
	if err := s.SetMessageBody(ctx, m.ID, BodyUpdate{Text: "x", Headers: hdr}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetMessage(ctx, "jira", m.ID); got.Bulk != "none" {
		t.Errorf("body: %q", got.Bulk)
	}
	// And not by the upgrade pass either.
	if _, err := s.db.ExecContext(ctx, `UPDATE messages SET bulk = '' WHERE id = ?`, m.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBulkBatch(ctx, []BulkVerdict{{ID: m.ID, Bulk: "newsletter", ListID: "x"}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetMessage(ctx, "jira", m.ID); got.Bulk != "none" || got.ListID != "" {
		t.Errorf("pass: %q %q", got.Bulk, got.ListID)
	}
}

func TestBulkUpgradePassStore(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	seedAccount(t, s, "acc")
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	a := bulkRow(t, s, inbox, 1, map[string]string{"Auto-Submitted": "auto-generated"})
	b := bulkRow(t, s, inbox, 2, nil)
	c := bulkRow(t, s, inbox, 3, nil)
	if err := s.SetMessageBody(ctx, c.ID, BodyUpdate{Text: "x", Headers: map[string]string{"List-Unsubscribe": "<mailto:u@x.example>"}}); err != nil {
		t.Fatal(err)
	}

	todo, err := s.ListUnclassified(ctx, 10)
	if err != nil || len(todo) != 2 {
		t.Fatalf("todo = %+v, %v", todo, err)
	}
	if todo[0].ID > todo[1].ID {
		t.Error("not in id order")
	}
	var verdicts []BulkVerdict
	for _, cand := range todo {
		r := bulk.Classify(cand.Headers)
		verdicts = append(verdicts, BulkVerdict{ID: cand.ID, Bulk: r.Stored(), ListID: r.ListID})
	}
	// The body of c arrived meanwhile with another verdict: not overwritten.
	verdicts = append(verdicts, BulkVerdict{ID: c.ID, Bulk: "automated"})
	if err := s.SetBulkBatch(ctx, verdicts); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{a.ID: "automated", b.ID: "none", c.ID: "newsletter"} {
		if got, _ := s.GetMessage(ctx, "acc", id); got.Bulk != want {
			t.Errorf("%s = %q, want %q", id, got.Bulk, want)
		}
	}
	if todo, _ := s.ListUnclassified(ctx, 10); len(todo) != 0 {
		t.Errorf("left: %+v", todo)
	}
	if err := s.ResetBulk(ctx); err != nil {
		t.Fatal(err)
	}
	if todo, _ := s.ListUnclassified(ctx, 10); len(todo) != 3 {
		t.Errorf("after reset: %d", len(todo))
	}
}

func TestUnsubscriptions(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	seedAccount(t, s, "acc")
	seedAccount(t, s, "other")
	if _, ok, err := s.GetUnsubscription(ctx, "acc", "list:x"); ok || err != nil {
		t.Fatalf("empty: %v %v", ok, err)
	}
	at := time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)
	if err := s.RememberUnsubscription(ctx, "acc", "list:x", "mailto", at); err != nil {
		t.Fatal(err)
	}
	u, ok, err := s.GetUnsubscription(ctx, "acc", "list:x")
	if err != nil || !ok || u.Method != "mailto" || !u.At.Equal(at) {
		t.Fatalf("%+v %v %v", u, ok, err)
	}
	later := at.Add(time.Hour)
	if err := s.RememberUnsubscription(ctx, "acc", "list:x", "oneClick", later); err != nil {
		t.Fatal(err)
	}
	if u, _, _ = s.GetUnsubscription(ctx, "acc", "list:x"); u.Method != "oneClick" || !u.At.Equal(later) {
		t.Errorf("renewed: %+v", u)
	}
	if err := s.RememberUnsubscription(ctx, "other", "list:x", "mailto", at); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberUnsubscription(ctx, "acc", "list:x", "url", at); err == nil {
		t.Error("method url accepted")
	}
	if err := s.DeleteAccount(ctx, "acc", true); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.GetUnsubscription(ctx, "acc", "list:x"); ok {
		t.Error("kept after the account was removed")
	}
	if _, ok, _ := s.GetUnsubscription(ctx, "other", "list:x"); !ok {
		t.Error("another account's row removed")
	}
}

// A message that comes from no draft is queued without one.
func TestEnqueueOutboxWithoutDraft(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	seedAccount(t, s, "acc")
	in := enqueueInput(Draft{AccountID: "acc"}, "Subject: unsubscribe\r\n\r\n")
	in.DraftID, in.DraftVersion = "", 0
	in.Message.Attachments, in.Message.HasAttachments = nil, false
	m, err := s.EnqueueOutbox(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.GetOutbox(ctx, "acc", m.ID)
	if err != nil || e.State != OutboxQueued {
		t.Fatalf("%+v %v", e, err)
	}
	r, err := s.OpenMessageRaw(ctx, "acc", m.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if b, _ := io.ReadAll(r); string(b) != "Subject: unsubscribe\r\n\r\n" {
		t.Errorf("file %q", b)
	}
}
