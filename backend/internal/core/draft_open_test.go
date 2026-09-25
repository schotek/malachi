// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// draftsFolder adds a Drafts folder to the mailbox and returns it.
func (m *mailbox) draftsFolder(t *testing.T) store.Folder {
	t.Helper()
	folders := seedFolders(t, m.b, string(m.acc), []store.Folder{
		{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Subscribed: true, Selectable: true},
		{Mailbox: "Trash", Name: "Trash", Path: "Trash", Role: api.RoleTrash, Subscribed: true, Selectable: true},
		{Mailbox: "Container", Name: "Container", Path: "Container", Subscribed: true, Selectable: false},
		{Mailbox: "Drafts", Name: "Drafts", Path: "Drafts", Role: api.RoleDrafts, Subscribed: true, Selectable: true},
	})
	return folders["Drafts"]
}

// seedIn stores a testdata/mime sample in folder f under uid, the way the
// syncer does (the envelope, the parsed body, the raw file); fetched false
// leaves the body undownloaded.
func (m *mailbox) seedIn(t *testing.T, f store.Folder, uid uint32, name string, fetched bool) store.Message {
	t.Helper()
	ctx := context.Background()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "mime", name))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := mime.Parse(bytes.NewReader(raw), mime.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	row := &store.Message{AccountID: string(m.acc), FolderID: f.ID, UID: uid, Flags: []api.Flag{api.FlagDraft, api.FlagSeen},
		Subject: parsed.Subject, Date: time.Date(2026, 9, 8, 8, 0, 0, 0, time.UTC), From: parsed.From, To: parsed.To, CC: parsed.CC,
		BCC: parsed.BCC, RFCMessageID: parsed.MessageID, InReplyTo: parsed.InReplyTo, References: parsed.References, Size: int64(len(raw))}
	if err := m.b.store.UpsertMessages(ctx, []*store.Message{row}); err != nil {
		t.Fatal(err)
	}
	if !fetched {
		return *row
	}
	if err := m.b.store.SetMessageBody(ctx, row.ID, store.BodyUpdate{
		Text: parsed.Text, HasHTML: parsed.HasHTML, Snippet: parsed.Snippet,
		Attachments: parsed.Attachments, HasAttachments: parsed.HasAttachments,
		Headers: parsed.Headers, State: store.BodyFetched,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.b.store.WriteMessageRaw(ctx, string(m.acc), row.ID, bytes.NewReader(raw), 25<<20); err != nil {
		t.Fatal(err)
	}
	got, err := m.b.store.GetMessage(ctx, string(m.acc), row.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// seedParent stores the message the fixture drafts answer.
func (m *mailbox) seedParent(t *testing.T) string {
	t.Helper()
	row := &store.Message{AccountID: string(m.acc), FolderID: string(m.inbox), UID: 50, Subject: "Plans",
		Date: time.Date(2026, 9, 7, 8, 0, 0, 0, time.UTC), RFCMessageID: "one@example.invalid",
		References: []string{"zero@example.invalid"}}
	if err := m.b.store.UpsertMessages(context.Background(), []*store.Message{row}); err != nil {
		t.Fatal(err)
	}
	return row.ID
}

func (m *mailbox) open(t *testing.T, id string) *api.DraftOpenResult {
	t.Helper()
	res, err := m.b.Drafts().Open(context.Background(), api.DraftOpenParams{AccountID: m.acc, MessageID: api.MessageID(id)})
	if err != nil {
		t.Fatalf("draft.open: %v", err)
	}
	return res
}

func addresses(list []api.Address) string {
	var out []string
	for _, a := range list {
		out = append(out, a.Address)
	}
	return strings.Join(out, ",")
}

// headerLines is the unfolded header block of a raw message.
func headerLines(raw []byte) []string {
	head, _, _ := strings.Cut(string(raw), "\r\n\r\n")
	head = strings.ReplaceAll(head, "\r\n ", " ")
	return strings.Split(head, "\r\n")
}

// TestDraftOpenOtherClientsDraft: a draft another client left in the
// Drafts folder opens with its recipients (Bcc too), its picture and file
// copied into the store, the parent resolved, and replaces set; saving it
// links the draft to that message and its upload keeps Bcc and threading.
func TestDraftOpenOtherClientsDraft(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	drafts := m.draftsFolder(t)
	parent := m.seedParent(t)
	msg := m.seedIn(t, drafts, 7, "draft-other-client.eml", true)

	res := m.open(t, msg.ID)
	d := res.Draft
	if d.ID != "" || d.Version != 0 || d.Replaces != api.MessageID(msg.ID) {
		t.Fatalf("draft identity: id=%q version=%d replaces=%q", d.ID, d.Version, d.Replaces)
	}
	if addresses(d.To) != "alice@example.invalid" || addresses(d.CC) != "bob@example.invalid" || addresses(d.BCC) != "hidden@example.invalid" {
		t.Fatalf("recipients: to=%v cc=%v bcc=%v", d.To, d.CC, d.BCC)
	}
	if d.Subject != "Re: Plans" || d.InReplyTo != api.MessageID(parent) || d.Forwarding != "" {
		t.Fatalf("subject %q, inReplyTo %q", d.Subject, d.InReplyTo)
	}
	if !strings.Contains(d.HTMLBody, "<b>picture</b>") || strings.Contains(d.HTMLBody, "<blockquote") {
		t.Fatalf("html = %s", d.HTMLBody)
	}
	checkQuoteClean(t, d)
	var inline, files int
	for _, a := range d.Attachments {
		if a.Inline {
			inline++
		} else if a.Filename == "plan.pdf" {
			files++
		}
	}
	if inline != 1 || files != 1 || len(res.Skipped) != 0 || res.Blocked != (api.BlockedContent{}) {
		t.Fatalf("attachments %+v skipped %+v blocked %+v", d.Attachments, res.Skipped, res.Blocked)
	}

	saved := saveRoundTrip(t, m, d)
	row, err := m.b.store.GetDraft(ctx, string(m.acc), string(saved.DraftID))
	if err != nil {
		t.Fatal(err)
	}
	if row.Copy.RFCMessageID != "webmail-draft-1@example.invalid" || row.Copy.UID != 7 || row.Copy.FolderID != drafts.ID {
		t.Fatalf("draft not linked to the message: %+v", row.Copy)
	}
	if row.ReplyRFCID != "one@example.invalid" || strings.Join(row.References, " ") != "zero@example.invalid one@example.invalid" {
		t.Fatalf("threading kept: %q %v", row.ReplyRFCID, row.References)
	}

	// The message is the saved draft's copy now: it opens as that draft.
	again := m.open(t, msg.ID)
	if again.Draft.ID != saved.DraftID || again.Draft.Version != saved.Version || again.Draft.Replaces != "" {
		t.Fatalf("reopened = %+v", again.Draft)
	}

	// Its server copy: Bcc kept, threaded, under a fresh Message-ID.
	up, err := m.b.buildDraft(ctx, string(m.acc), string(saved.DraftID))
	if err != nil {
		t.Fatal(err)
	}
	head := strings.Join(headerLines(up.Raw), "\n")
	for _, want := range []string{`Bcc: "Hidden" <hidden@example.invalid>`, "In-Reply-To: <one@example.invalid>",
		"References: <zero@example.invalid> <one@example.invalid>", "Message-Id: <" + up.RFCMessageID + ">"} {
		if !strings.Contains(head, want) {
			t.Errorf("header lacks %q:\n%s", want, head)
		}
	}
	if up.RFCMessageID == "webmail-draft-1@example.invalid" || up.Version != saved.Version {
		t.Errorf("upload = %q v%d", up.RFCMessageID, up.Version)
	}

	// With the parent gone from the store the kept headers still thread.
	if err := m.b.store.DeleteMessages(ctx, string(m.acc), []string{parent}); err != nil {
		t.Fatal(err)
	}
	up, err = m.b.buildDraft(ctx, string(m.acc), string(saved.DraftID))
	if err != nil {
		t.Fatal(err)
	}
	if head := strings.Join(headerLines(up.Raw), "\n"); !strings.Contains(head, "In-Reply-To: <one@example.invalid>") {
		t.Errorf("threading lost with the parent:\n%s", head)
	}
}

// TestDraftOpenHostileDraft: whatever a draft's header and HTML try, the
// opened draft is clean, loses what the sanitiser removed and therefore
// does not replace the original; its upload smuggles no header.
func TestDraftOpenHostileDraft(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	drafts := m.draftsFolder(t)
	msg := m.seedIn(t, drafts, 8, "draft-hostile.eml", true)

	res := m.open(t, msg.ID)
	d := res.Draft
	checkQuoteClean(t, d)
	if d.Replaces != "" {
		t.Fatalf("lossy draft replaces the original")
	}
	if res.Blocked == (api.BlockedContent{}) {
		t.Errorf("nothing reported blocked")
	}
	if strings.ContainsAny(d.Subject, "\r\n") {
		t.Errorf("subject = %q", d.Subject)
	}
	for _, a := range append(append(append([]api.Address{}, d.To...), d.CC...), d.BCC...) {
		if strings.ContainsAny(a.Name+a.Address, "\r\n") || !strings.Contains(a.Address, "@") {
			t.Errorf("recipient %+v", a)
		}
	}

	saved, err := m.b.Drafts().Save(ctx, api.DraftSaveParams{Draft: d})
	if err != nil {
		t.Fatal(err)
	}
	row, _ := m.b.store.GetDraft(ctx, string(m.acc), string(saved.DraftID))
	if !row.Copy.IsZero() {
		t.Fatalf("lossy draft linked to the original: %+v", row.Copy)
	}
	up, err := m.b.buildDraft(ctx, string(m.acc), string(saved.DraftID))
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range headerLines(up.Raw) {
		if strings.HasPrefix(strings.ToLower(l), "x-injected") {
			t.Fatalf("header smuggled into the upload: %q", l)
		}
	}
}

// TestDraftOpenStates covers what draft.open refuses or returns as it is.
func TestDraftOpenStates(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	drafts := m.draftsFolder(t)

	pending := m.seedIn(t, drafts, 9, "draft-other-client.eml", false)
	if _, err := m.b.Drafts().Open(ctx, api.DraftOpenParams{AccountID: m.acc, MessageID: api.MessageID(pending.ID)}); errCode(t, err) != api.CodeUnavailable {
		t.Fatalf("undownloaded body: %v", err)
	}
	if _, err := m.b.Drafts().Open(ctx, api.DraftOpenParams{AccountID: m.acc, MessageID: m.msgs[0]}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("inbox message: %v", err)
	}
	if _, err := m.b.Drafts().Open(ctx, api.DraftOpenParams{AccountID: m.acc, MessageID: "m_nope"}); errCode(t, err) != api.CodeMessageNotFound {
		t.Fatalf("unknown message: %v", err)
	}
	if _, err := m.b.Drafts().Save(ctx, api.DraftSaveParams{Draft: api.Draft{AccountID: m.acc, Replaces: m.msgs[0]}}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("replaces an inbox message: %v", err)
	}

	// A truncated message never replaces its original.
	cut := m.seedIn(t, drafts, 10, "truncated.eml", true)
	if res := m.open(t, cut.ID); res.Draft.Replaces != "" {
		t.Errorf("truncated draft replaces the original")
	}
}

// TestDraftOpenNewerCopy: a copy another client stored after Malachi's
// last upload opens as the draft's new text under the draft's id and
// version; a draft with changes still to upload wins.
func TestDraftOpenNewerCopy(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	drafts := m.draftsFolder(t)

	saved, err := m.b.Drafts().Save(ctx, api.DraftSaveParams{Draft: api.Draft{AccountID: m.acc, Subject: "mine", TextBody: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	c := store.DraftCopy{FolderID: drafts.ID, UID: 5, RFCMessageID: "webmail-draft-1@example.invalid"}
	if _, err := m.b.store.MarkDraftSynced(ctx, string(m.acc), string(saved.DraftID), saved.Version, c, false); err != nil {
		t.Fatal(err)
	}
	newer := m.seedIn(t, drafts, 12, "draft-other-client.eml", true)
	res := m.open(t, newer.ID)
	if res.Draft.ID != saved.DraftID || res.Draft.Version != saved.Version || res.Draft.Subject != "Re: Plans" ||
		res.Draft.Replaces != api.MessageID(newer.ID) {
		t.Fatalf("newer copy = %+v", res.Draft)
	}

	// Pending local changes win over the server's copy.
	if _, err := m.b.Drafts().Save(ctx, api.DraftSaveParams{Draft: api.Draft{ID: saved.DraftID, AccountID: m.acc, Version: saved.Version, Subject: "edited", TextBody: "y"}}); err != nil {
		t.Fatal(err)
	}
	if res := m.open(t, newer.ID); res.Draft.Subject != "edited" || res.Draft.Replaces != "" {
		t.Fatalf("pending draft lost to the server copy: %+v", res.Draft)
	}
}

// TestDraftCopyGoesWithSend: sending a draft queues the delete of its copy
// and wakes the syncer for the Drafts folder.
func TestDraftCopyGoesWithSend(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	drafts := m.draftsFolder(t)
	saved, err := m.b.Drafts().Save(ctx, api.DraftSaveParams{Draft: api.Draft{AccountID: m.acc, Subject: "s", TextBody: "x",
		To: []api.Address{{Address: "alice@example.invalid"}}}})
	if err != nil {
		t.Fatal(err)
	}
	copyRow := m.seedIn(t, drafts, 30, "simple-text.eml", true)
	c := store.CopyOf(copyRow, drafts)
	if _, err := m.b.store.MarkDraftSynced(ctx, string(m.acc), string(saved.DraftID), saved.Version, c, false); err != nil {
		t.Fatal(err)
	}
	m.f.reset()
	if _, err := m.b.Messages().Send(ctx, api.MessageSendParams{AccountID: m.acc, DraftID: saved.DraftID, Version: saved.Version}); err != nil {
		t.Fatal(err)
	}
	ops, _ := m.b.store.NextOps(ctx, string(m.acc), time.Now(), 10)
	if len(ops) != 1 || ops[0].Kind != store.OpDelete || ops[0].UID != 30 {
		t.Fatalf("ops = %+v", ops)
	}
	if _, err := m.b.store.GetMessage(ctx, string(m.acc), copyRow.ID); err == nil {
		t.Error("copy still listed after send")
	}
	if got := m.f.recorded(); len(got) != 1 || !strings.Contains(got[0], drafts.ID) {
		t.Errorf("syncer not woken for the drafts folder: %v", got)
	}
}
