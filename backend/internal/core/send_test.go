// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// saveDraft stores d through the API and returns its id and version.
func saveDraft(t *testing.T, b *Backend, d api.Draft) (api.DraftID, int) {
	t.Helper()
	res, err := b.Drafts().Save(context.Background(), api.DraftSaveParams{Draft: d})
	if err != nil {
		t.Fatal(err)
	}
	return res.DraftID, res.Version
}

// importAttachment stores content as an attachment of the account.
func importAttachment(t *testing.T, b *Backend, acc api.AccountID, name string, content []byte) api.DraftAttachment {
	t.Helper()
	res, err := b.Attachments().Import(context.Background(), api.AttachmentImportParams{AccountID: acc, Data: content, Filename: name})
	if err != nil {
		t.Fatal(err)
	}
	return res.Attachment
}

func outboxFolder(t *testing.T, b *Backend, acc api.AccountID) (api.Folder, bool) {
	t.Helper()
	res, err := b.Folders().List(context.Background(), api.FolderListParams{AccountID: acc})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Folders {
		if f.Role == api.RoleOutbox {
			return f, true
		}
	}
	return api.Folder{}, false
}

func pendingOutboxOf(t *testing.T, b *Backend, acc api.AccountID) int {
	t.Helper()
	res, err := b.Sync().Status(context.Background(), api.SyncStatusParams{AccountID: acc})
	if err != nil {
		t.Fatal(err)
	}
	return res.Accounts[0].PendingOutbox
}

func TestSendValidation(t *testing.T) {
	b, _ := newSyncBackend(t)
	ctx := context.Background()
	acc := api.AccountID(seedAccount(t, b, "me@example.invalid"))
	svc := b.Messages()

	if _, err := svc.Send(ctx, api.MessageSendParams{DraftID: "d"}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("no account: %v", err)
	}
	if _, err := svc.Send(ctx, api.MessageSendParams{AccountID: acc}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("no draft id: %v", err)
	}
	if _, err := svc.Send(ctx, api.MessageSendParams{AccountID: "acc_nope", DraftID: "d", Version: 1}); errCode(t, err) != api.CodeAccountNotFound {
		t.Fatalf("unknown account: %v", err)
	}
	if _, err := svc.Send(ctx, api.MessageSendParams{AccountID: acc, DraftID: "d_nope", Version: 1}); errCode(t, err) != api.CodeDraftNotFound {
		t.Fatalf("unknown draft: %v", err)
	}

	id, version := saveDraft(t, b, api.Draft{AccountID: acc, To: []api.Address{{Address: "to@example.invalid"}}, Subject: "s", TextBody: "t"})
	if _, err := svc.Send(ctx, api.MessageSendParams{AccountID: acc, DraftID: id, Version: version + 1}); errCode(t, err) != api.CodeConflict {
		t.Fatalf("stale version: %v", err)
	}

	// A draft of another account is unknown here.
	other := api.AccountID(seedAccount(t, b, "other@example.invalid"))
	outboxOf(t, b).reset()
	if _, err := svc.Send(ctx, api.MessageSendParams{AccountID: other, DraftID: id, Version: version}); errCode(t, err) != api.CodeDraftNotFound {
		t.Fatalf("foreign draft: %v", err)
	}

	empty, ev := saveDraft(t, b, api.Draft{AccountID: acc, Subject: "nobody", TextBody: "t"})
	if _, err := svc.Send(ctx, api.MessageSendParams{AccountID: acc, DraftID: empty, Version: ev}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("no recipients: %v", err)
	}

	// draft.save refuses a bad address, so plant one through the store.
	bad := store.Draft{AccountID: string(acc), Subject: "bad", To: []api.Address{{Address: "not an address"}}}
	if err := b.store.SaveDraft(ctx, &bad, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Send(ctx, api.MessageSendParams{AccountID: acc, DraftID: api.DraftID(bad.ID), Version: bad.Version}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("bad address: %v", err)
	}

	// Nothing was queued and every draft is still there.
	if _, ok := outboxFolder(t, b, acc); ok {
		t.Fatal("outbox folder created by rejected sends")
	}
	for _, d := range []api.DraftID{id, empty, api.DraftID(bad.ID)} {
		if _, err := b.store.GetDraft(ctx, string(acc), string(d)); err != nil {
			t.Errorf("draft %s after rejected send: %v", d, err)
		}
	}
	if got := outboxOf(t, b).recorded(); len(got) != 0 {
		t.Fatalf("rejected sends reached the outbox supervisor: %v", got)
	}
}

func TestSendTooBig(t *testing.T) {
	b, _ := newSyncBackend(t)
	ctx := context.Background()
	acc := api.AccountID(seedAccount(t, b, "me@example.invalid"))

	old := outgoingLimit
	outgoingLimit = 4 << 10
	t.Cleanup(func() { outgoingLimit = old })

	att := importAttachment(t, b, acc, "big.bin", bytes.Repeat([]byte("x"), 8<<10))
	id, version := saveDraft(t, b, api.Draft{AccountID: acc, To: []api.Address{{Address: "to@example.invalid"}}, Subject: "big",
		TextBody: "t", Attachments: []api.DraftAttachment{{ID: att.ID}}})
	_, err := b.Messages().Send(ctx, api.MessageSendParams{AccountID: acc, DraftID: id, Version: version})
	if errCode(t, err) != api.CodeAttachmentTooBig {
		t.Fatalf("too big: %v", err)
	}
	var apiErr *api.Error
	errorsAs(err, &apiErr)
	data, ok := apiErr.Data.(map[string]int64)
	if !ok || data["limit"] != outgoingLimit || data["size"] <= outgoingLimit {
		t.Fatalf("data = %#v", apiErr.Data)
	}
	// The draft and its attachment survive; nothing was queued.
	d, err := b.store.GetDraft(ctx, string(acc), string(id))
	if err != nil || len(d.Attachments) != 1 {
		t.Fatalf("draft after failed send: %+v, %v", d, err)
	}
	if _, err := b.store.GetAttachments(ctx, string(acc), []string{att.ID}); err != nil {
		t.Fatalf("attachment after failed send: %v", err)
	}
	if _, ok := outboxFolder(t, b, acc); ok {
		t.Fatal("outbox folder created")
	}
	if pendingOutboxOf(t, b, acc) != 0 {
		t.Fatal("pendingOutbox after failed send")
	}
}

func TestSendQueuesMessage(t *testing.T) {
	b, f := newSyncBackend(t)
	o := outboxOf(t, b)
	ctx := context.Background()
	acc := api.AccountID(seedAccount(t, b, "me@example.invalid"))
	if _, err := b.Accounts().Update(ctx, api.AccountUpdateParams{AccountID: acc, Config: func() api.AccountConfig {
		c := validConfig()
		c.DisplayName = "Me Myself"
		return c
	}()}); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	b.SetNotifier(rec)

	// A message to reply to, with its own References chain.
	folders := seedFolders(t, b, string(acc), []store.Folder{
		{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Subscribed: true, Selectable: true},
		{Mailbox: "Zebra", Name: "Zebra", Path: "Zebra", Subscribed: true, Selectable: true},
	})
	parent := &store.Message{AccountID: string(acc), FolderID: folders["INBOX"].ID, UID: 1, Subject: "question", Date: time.Now(),
		From: []api.Address{{Address: "alice@example.invalid"}}, RFCMessageID: "q@example.invalid", References: []string{"root@example.invalid", ""}}
	if err := b.store.UpsertMessages(ctx, []*store.Message{parent}); err != nil {
		t.Fatal(err)
	}

	att := importAttachment(t, b, acc, "notes.txt", []byte("some notes\n"))
	draft := api.Draft{
		AccountID: acc,
		To:        []api.Address{{Name: "To", Address: "to@example.invalid"}},
		CC:        []api.Address{{Address: "cc@example.invalid"}},
		BCC:       []api.Address{{Address: "bcc@example.invalid"}},
		Subject:   "Re: question",
		TextBody:  "> quoted\nAnswer with čeština.\n",
		InReplyTo: api.MessageID(parent.ID),
		Attachments: []api.DraftAttachment{
			{ID: att.ID},
		},
	}
	id, version := saveDraft(t, b, draft)
	f.reset()
	o.reset()

	res, err := b.Messages().Send(ctx, api.MessageSendParams{AccountID: acc, DraftID: id, Version: version})
	if err != nil {
		t.Fatal(err)
	}
	if res.OutboxID == "" {
		t.Fatal("empty outboxId")
	}
	if got := o.recorded(); len(got) != 1 || got[0] != "wake:"+string(acc) {
		t.Fatalf("outbox supervisor calls = %v", got)
	}

	// The draft is gone, with its attachment.
	if _, err := b.store.GetDraft(ctx, string(acc), string(id)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("draft after send: %v", err)
	}
	if _, err := b.store.GetAttachments(ctx, string(acc), []string{att.ID}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("attachment after send: %v", err)
	}
	if _, err := b.Messages().Send(ctx, api.MessageSendParams{AccountID: acc, DraftID: id, Version: version}); errCode(t, err) != api.CodeDraftNotFound {
		t.Fatalf("second send: %v", err)
	}

	// The queued message: headers, threading, outbox state.
	got, err := b.Messages().Get(ctx, api.MessageGetParams{AccountID: acc, MessageID: res.OutboxID})
	if err != nil {
		t.Fatal(err)
	}
	m := got.Message
	if m.Outbox == nil || m.Outbox.State != api.OutboxQueued || m.Outbox.Attempts != 0 || m.Outbox.NextAttemptAt != nil || m.Outbox.Error != nil {
		t.Fatalf("outbox info = %+v", m.Outbox)
	}
	if len(m.From) != 1 || m.From[0].Address != "me@example.invalid" || m.From[0].Name != "Me Myself" {
		t.Fatalf("from = %+v", m.From)
	}
	if len(m.To) != 1 || len(m.CC) != 1 || len(m.BCC) != 1 || m.Subject != "Re: question" || !m.HasAttachments || len(m.Attachments) != 1 {
		t.Fatalf("message = %+v", m)
	}
	if m.Attachments[0].PartID != "2" || m.Attachments[0].Filename != "notes.txt" || m.Attachments[0].Size != 11 {
		t.Fatalf("attachment = %+v", m.Attachments[0])
	}
	if m.InReplyTo != "q@example.invalid" || strings.Join(m.References, " ") != "root@example.invalid q@example.invalid" {
		t.Fatalf("threading = %q %v", m.InReplyTo, m.References)
	}
	if !strings.HasSuffix(m.RFCMessageID, "@example.invalid") || m.Snippet != "Answer with čeština." || !hasFlagE2E(m.Flags, api.FlagSeen) || m.Size <= 0 {
		t.Fatalf("summary = %+v", m)
	}
	outbox, ok := outboxFolder(t, b, acc)
	if !ok || m.FolderID != outbox.ID {
		t.Fatalf("folder = %s, outbox = %+v", m.FolderID, outbox)
	}

	// The raw message: Bcc is envelope-only, the text is quoted-printable.
	raw, err := b.store.OpenMessageRaw(ctx, string(acc), string(res.OutboxID))
	if err != nil {
		t.Fatal(err)
	}
	rawBytes, _ := io.ReadAll(raw)
	raw.Close()
	text := string(rawBytes)
	if int64(len(text)) != m.Size {
		t.Fatalf("size %d, raw %d", m.Size, len(text))
	}
	for _, want := range []string{"From: \"Me Myself\" <me@example.invalid>", "To: \"To\" <to@example.invalid>", "Cc: <cc@example.invalid>",
		"In-Reply-To: <q@example.invalid>", "References: <root@example.invalid> <q@example.invalid>", "Content-Type: multipart/mixed", "notes.txt"} {
		if !strings.Contains(text, want) {
			t.Errorf("raw message lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "bcc@example.invalid") {
		t.Error("raw message leaks the Bcc recipient")
	}
	entry, err := b.store.GetOutbox(ctx, string(acc), string(res.OutboxID))
	if err != nil || entry.EnvelopeFrom != "me@example.invalid" || strings.Join(entry.Recipients, " ") != "to@example.invalid cc@example.invalid bcc@example.invalid" {
		t.Fatalf("entry = %+v, %v", entry, err)
	}

	// Folder list: the outbox last, counting the message.
	list, _ := b.Folders().List(ctx, api.FolderListParams{AccountID: acc})
	if n := len(list.Folders); n != 3 || list.Folders[0].Role != api.RoleInbox || list.Folders[1].Role != api.RoleOutbox || list.Folders[2].Name != "Zebra" {
		t.Fatalf("folder order = %+v", list.Folders)
	}
	if outbox.Total != 1 || outbox.Unread != 0 || outbox.Path != "Outbox" || !outbox.Selectable {
		t.Fatalf("outbox folder = %+v", outbox)
	}

	// message.list of the outbox carries the state; of the inbox it does not.
	ml, err := b.Messages().List(ctx, api.MessageListParams{AccountID: acc, FolderID: outbox.ID})
	if err != nil || len(ml.Messages) != 1 || ml.Messages[0].ID != res.OutboxID || ml.Messages[0].Outbox == nil || ml.Messages[0].Outbox.State != api.OutboxQueued {
		t.Fatalf("outbox listing = %+v, %v", ml, err)
	}
	il, _ := b.Messages().List(ctx, api.MessageListParams{AccountID: acc, FolderID: api.FolderID(folders["INBOX"].ID)})
	if len(il.Messages) != 1 || il.Messages[0].Outbox != nil {
		t.Fatalf("inbox listing = %+v", il)
	}

	// pendingOutbox everywhere: sync.status, account.list, notify.syncState.
	if pendingOutboxOf(t, b, acc) != 1 {
		t.Fatal("sync.status pendingOutbox != 1")
	}
	al, _ := b.Accounts().List(ctx, api.AccountListParams{})
	if al.Accounts[0].State.PendingOutbox != 1 {
		t.Fatalf("account.list state = %+v", al.Accounts[0].State)
	}
	waitFor(t, "syncState with pendingOutbox 1", func() bool {
		states, _ := rec.snapshot()
		for _, st := range states {
			if st.AccountID == acc && st.PendingOutbox == 1 {
				return true
			}
		}
		return false
	})

	// Flag and move are refused; the outbox is no move target either.
	if _, err := b.Messages().Flag(ctx, api.MessageFlagParams{AccountID: acc, MessageIDs: []api.MessageID{res.OutboxID}, Set: []api.Flag{api.FlagFlagged}}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("flag: %v", err)
	}
	if _, err := b.Messages().Move(ctx, api.MessageMoveParams{AccountID: acc, MessageIDs: []api.MessageID{res.OutboxID}, TargetFolderID: api.FolderID(folders["INBOX"].ID)}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("move: %v", err)
	}
	if _, err := b.Messages().Move(ctx, api.MessageMoveParams{AccountID: acc, MessageIDs: []api.MessageID{api.MessageID(parent.ID)}, TargetFolderID: outbox.ID}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("move into outbox: %v", err)
	}
	if got := f.recorded(); len(got) != 0 {
		t.Fatalf("refused mutations reached the syncer: %v", got)
	}

	// message.body works as for any message.
	body, err := b.Messages().Body(ctx, api.MessageBodyParams{AccountID: acc, MessageID: res.OutboxID})
	if err != nil || body.BodyState != api.BodyFetched || body.Text != draft.TextBody {
		t.Fatalf("body = %+v, %v", body, err)
	}

	// Delete removes it for good, without a Trash folder, and cancels it.
	o.reset()
	if _, err := b.Messages().Delete(ctx, api.MessageDeleteParams{AccountID: acc, MessageIDs: []api.MessageID{res.OutboxID}}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := b.Messages().Get(ctx, api.MessageGetParams{AccountID: acc, MessageID: res.OutboxID}); errCode(t, err) != api.CodeMessageNotFound {
		t.Fatalf("get after delete: %v", err)
	}
	if _, err := b.store.OpenMessageRaw(ctx, string(acc), string(res.OutboxID)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("raw file after delete: %v", err)
	}
	if pendingOutboxOf(t, b, acc) != 0 {
		t.Fatal("pendingOutbox after delete")
	}
	if outbox, _ = outboxFolder(t, b, acc); outbox.Total != 0 {
		t.Fatalf("outbox folder after delete = %+v", outbox)
	}
	if got := f.recorded(); len(got) != 0 {
		t.Fatalf("outbox delete triggered the syncer: %v", got)
	}
	waitFor(t, "syncState with pendingOutbox 0", func() bool {
		states, _ := rec.snapshot()
		return len(states) > 0 && states[len(states)-1].PendingOutbox == 0
	})
}

func TestSendFromDisabledAccountQueues(t *testing.T) {
	b, _ := newSyncBackend(t)
	ctx := context.Background()
	acc := api.AccountID(seedAccount(t, b, "me@example.invalid"))
	if _, err := b.Accounts().SetEnabled(ctx, api.AccountSetEnabledParams{AccountID: acc, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	id, version := saveDraft(t, b, api.Draft{AccountID: acc, To: []api.Address{{Address: "to@example.invalid"}}, Subject: "s", TextBody: "t"})
	if _, err := b.Messages().Send(ctx, api.MessageSendParams{AccountID: acc, DraftID: id, Version: version}); err != nil {
		t.Fatal(err)
	}
	st, _ := b.Sync().Status(ctx, api.SyncStatusParams{AccountID: acc})
	if st.Accounts[0].Status != api.SyncDisabled || st.Accounts[0].PendingOutbox != 1 {
		t.Fatalf("state = %+v", st.Accounts[0])
	}
}

func TestOutboxRetryAndBusyMessages(t *testing.T) {
	b, _ := newSyncBackend(t)
	o := outboxOf(t, b)
	ctx := context.Background()
	acc := api.AccountID(seedAccount(t, b, "me@example.invalid"))
	svc := b.Outbox()

	if _, err := svc.Retry(ctx, api.OutboxRetryParams{MessageID: "m"}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("no account: %v", err)
	}
	if _, err := svc.Retry(ctx, api.OutboxRetryParams{AccountID: "acc_nope", MessageID: "m"}); errCode(t, err) != api.CodeAccountNotFound {
		t.Fatalf("unknown account: %v", err)
	}
	if _, err := svc.Retry(ctx, api.OutboxRetryParams{AccountID: acc, MessageID: "m_nope"}); errCode(t, err) != api.CodeMessageNotFound {
		t.Fatalf("unknown message: %v", err)
	}

	id, version := saveDraft(t, b, api.Draft{AccountID: acc, To: []api.Address{{Address: "to@example.invalid"}}, Subject: "s", TextBody: "t"})
	res, err := b.Messages().Send(ctx, api.MessageSendParams{AccountID: acc, DraftID: id, Version: version})
	if err != nil {
		t.Fatal(err)
	}
	mid := res.OutboxID
	// A queued message waiting for its backoff becomes due at once.
	if err := b.store.MarkOutboxSending(ctx, string(mid)); err != nil {
		t.Fatal(err)
	}
	if err := b.store.MarkOutboxRetry(ctx, string(mid), api.CodeNetworkError, "boom", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _ := b.Messages().Get(ctx, api.MessageGetParams{AccountID: acc, MessageID: mid})
	if ob := got.Message.Outbox; ob.State != api.OutboxQueued || ob.Attempts != 1 || ob.NextAttemptAt == nil || ob.Error == nil || ob.Error.Code != api.CodeNetworkError || ob.Error.Message != "boom" {
		t.Fatalf("outbox info after failure = %+v", ob)
	}
	o.reset()
	if _, err := svc.Retry(ctx, api.OutboxRetryParams{AccountID: acc, MessageID: mid}); err != nil {
		t.Fatal(err)
	}
	if calls := o.recorded(); len(calls) != 1 || calls[0] != "wake:"+string(acc) {
		t.Fatalf("retry did not wake the worker: %v", calls)
	}
	got, _ = b.Messages().Get(ctx, api.MessageGetParams{AccountID: acc, MessageID: mid})
	if ob := got.Message.Outbox; ob.State != api.OutboxQueued || ob.NextAttemptAt != nil || ob.Error == nil {
		t.Fatalf("outbox info after retry = %+v", ob)
	}

	// While sending: retry and delete conflict.
	if err := b.store.MarkOutboxSending(ctx, string(mid)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Retry(ctx, api.OutboxRetryParams{AccountID: acc, MessageID: mid}); errCode(t, err) != api.CodeConflict {
		t.Fatalf("retry while sending: %v", err)
	}
	if _, err := b.Messages().Delete(ctx, api.MessageDeleteParams{AccountID: acc, MessageIDs: []api.MessageID{mid}, Permanent: true}); errCode(t, err) != api.CodeConflict {
		t.Fatalf("delete while sending: %v", err)
	}
	got, _ = b.Messages().Get(ctx, api.MessageGetParams{AccountID: acc, MessageID: mid})
	if ob := got.Message.Outbox; ob.State != api.OutboxSending {
		t.Fatalf("outbox info while sending = %+v", ob)
	}

	// Failed: retry re-queues.
	if err := b.store.MarkOutboxFailed(ctx, string(mid), api.CodeServerError, "550 no"); err != nil {
		t.Fatal(err)
	}
	if pendingOutboxOf(t, b, acc) != 0 {
		t.Fatal("a failed message counts as pending")
	}
	got, _ = b.Messages().Get(ctx, api.MessageGetParams{AccountID: acc, MessageID: mid})
	if ob := got.Message.Outbox; ob.State != api.OutboxFailed || ob.NextAttemptAt != nil || ob.Error == nil || ob.Error.Code != api.CodeServerError {
		t.Fatalf("outbox info when failed = %+v", ob)
	}
	if _, err := svc.Retry(ctx, api.OutboxRetryParams{AccountID: acc, MessageID: mid}); err != nil {
		t.Fatal(err)
	}
	if pendingOutboxOf(t, b, acc) != 1 {
		t.Fatal("re-queued message not pending")
	}

	// Sent: nothing to retry, and a delete is fine.
	if err := b.store.MarkOutboxSending(ctx, string(mid)); err != nil {
		t.Fatal(err)
	}
	if err := b.store.MarkOutboxSent(ctx, string(mid)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Retry(ctx, api.OutboxRetryParams{AccountID: acc, MessageID: mid}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("retry when sent: %v", err)
	}
	got, _ = b.Messages().Get(ctx, api.MessageGetParams{AccountID: acc, MessageID: mid})
	if ob := got.Message.Outbox; ob.State != api.OutboxSent || ob.Error != nil || ob.Attempts != 3 {
		t.Fatalf("outbox info when sent = %+v", ob)
	}
	if _, err := b.Messages().Delete(ctx, api.MessageDeleteParams{AccountID: acc, MessageIDs: []api.MessageID{mid}, Permanent: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Retry(ctx, api.OutboxRetryParams{AccountID: acc, MessageID: mid}); errCode(t, err) != api.CodeMessageNotFound {
		t.Fatalf("retry after delete: %v", err)
	}
}
