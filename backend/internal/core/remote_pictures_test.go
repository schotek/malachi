// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// pngSignature starts every picture of pictureMessage, so that it sniffs
// as image/png.
var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// largePhoto is a picture of more than api.LargeAttachmentMinBytes,
// smallPhoto one of less.
var (
	largePhoto = append(slices.Clone(pngSignature), bytes.Repeat([]byte("photo data "), 12<<10)...)
	smallPhoto = append(slices.Clone(pngSignature), bytes.Repeat([]byte("icon data "), 1<<10)...)
)

// pictureMessage is a message whose HTML shows two pictures:
//
//	1.1 the HTML body, showing 1.2 and 1.3
//	1.2 photo (cid:photo@example.org)
//	1.3 a small picture (cid:logo@example.org)
//	2   report.pdf, bigPDF, when withPDF
func pictureMessage(messageID string, photo []byte, withPDF bool) []byte {
	lines := []string{
		"From: Alice <alice@example.org>",
		"To: me@example.invalid",
		"Subject: Holiday",
		"Date: Mon, 01 Sep 2025 10:00:00 +0000",
		"Message-ID: <" + messageID + ">",
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="mix"`,
		"",
		"--mix",
		`Content-Type: multipart/related; boundary="rel"`,
		"",
		"--rel",
		"Content-Type: text/html; charset=utf-8",
		"",
		`<p>The view. <img src="cid:photo@example.org" alt="photo"> <img src="cid:logo@example.org" alt="logo"></p>`,
		"--rel",
		"Content-Type: image/png",
		"Content-ID: <photo@example.org>",
		"Content-Transfer-Encoding: base64",
		"",
		base64Lines(photo),
		"--rel",
		"Content-Type: image/png",
		"Content-ID: <logo@example.org>",
		"Content-Transfer-Encoding: base64",
		"",
		base64Lines(pngSignature),
		"--rel--",
		"",
	}
	if withPDF {
		lines = append(lines, "--mix", "Content-Type: application/pdf",
			`Content-Disposition: attachment; filename="report.pdf"`, "Content-Transfer-Encoding: base64", "",
			base64Lines(bigPDF))
	}
	lines = append(lines, "--mix--", "")
	return []byte(strings.Join(lines, "\r\n"))
}

// seedPictures stores raw in the inbox under uid as the syncer does
// (ingest.Store) under pol, received a year ago.
func (m *mailbox) seedPictures(t *testing.T, uid uint32, raw []byte, pol ingest.Policy) store.Message {
	t.Helper()
	ctx := context.Background()
	parsed, err := mime.Parse(bytes.NewReader(raw), mime.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	received := time.Now().AddDate(-1, 0, 0)
	row := &store.Message{AccountID: string(m.acc), FolderID: string(m.inbox), UID: uid, Subject: parsed.Subject,
		Date: received, InternalDate: received, RFCMessageID: parsed.MessageID, Size: int64(len(raw))}
	if err := m.b.store.UpsertMessages(ctx, []*store.Message{row}); err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Store(ctx, m.b.store, ingest.Request{
		Target: ingest.Target{AccountID: string(m.acc), MessageID: row.ID, Role: api.RoleInbox, HasServerCopy: true,
			InternalDate: received, Date: received},
		Body: bytes.NewReader(raw), Policy: pol, Now: time.Now(),
		Expect: store.RawExpect{BodyState: store.BodyNone},
	}, nil); err != nil {
		t.Fatal(err)
	}
	m.msgs = append(m.msgs, api.MessageID(row.ID))
	return m.row(t, row.ID)
}

// body is message.body of a message, which must show its HTML.
func (m *mailbox) body(t *testing.T, id string) *api.MessageBodyResult {
	t.Helper()
	res, err := m.b.Messages().Body(context.Background(), api.MessageBodyParams{AccountID: m.acc, MessageID: api.MessageID(id)})
	if err != nil || res.HTMLWithheld {
		t.Fatalf("message.body: %+v, %v", res, err)
	}
	return res
}

var neverStorePolicy = ingest.Policy{NeverStore: true}

// Under neverStoreAttachments the large picture the HTML shows is on the
// server only: message.body keeps pointing at it and counts it
// (remotePictures), while message.part answers partNotDownloaded, and
// showing the message never contacts the server; once message.download
// holds the message the count is 0 and the picture is served from memory,
// and once the copy goes it is 1 again. A message stored whole counts
// none, and a partial one counts its picture whatever the preference,
// until a download outside the mode stores it whole.
func TestMessageBodyRemotePictures(t *testing.T) {
	m := seedMailbox(t)
	srv := m.fakeServer()
	ctx := context.Background()
	raw := pictureMessage("holiday@example.org", largePhoto, false)
	m.setNeverStore(t, true)
	msg := m.seedPictures(t, 7, raw, neverStorePolicy)
	srv.put("INBOX", 7, raw)
	if msg.RawState != store.RawPartial || !slices.Equal(msg.RemoteParts, []string{"1.2"}) || msg.StrippableBytes != 0 {
		t.Fatalf("seeded %+v", msg)
	}

	body := m.body(t, msg.ID)
	if !maps.Equal(body.InlineParts, map[string]string{"photo@example.org": "1.2", "logo@example.org": "1.3"}) ||
		body.RemotePictures != 1 {
		t.Fatalf("body: inline parts %v, remote pictures %d", body.InlineParts, body.RemotePictures)
	}
	for _, part := range []string{"1.2", "1.3"} {
		if u := "malachi-cid:" + string(m.acc) + "/" + msg.ID + "/" + part; !strings.Contains(body.HTML, u) {
			t.Errorf("the html does not point at %s:\n%s", u, body.HTML)
		}
	}
	if _, err := m.part(msg.ID, "1.2"); errCode(t, err) != api.CodePartNotDownloaded {
		t.Fatalf("the large picture: %v", err)
	}
	if logo, err := m.part(msg.ID, "1.3"); err != nil || !bytes.Equal(logo.Data, pngSignature) {
		t.Fatalf("the small picture: %v", err)
	}
	if n := len(srv.calls()); n != 0 {
		t.Fatalf("showing the message contacted the server: %d fetches", n)
	}

	if _, err := m.download(ctx, msg.ID); err != nil {
		t.Fatal(err)
	}
	if body := m.body(t, msg.ID); body.RemotePictures != 0 || len(body.InlineParts) != 2 {
		t.Fatalf("held: inline parts %v, remote pictures %d", body.InlineParts, body.RemotePictures)
	}
	if pic, err := m.part(msg.ID, "1.2"); err != nil || !bytes.Equal(pic.Data, largePhoto) {
		t.Fatalf("the large picture from memory: %v", err)
	}
	if row := m.row(t, msg.ID); row.RawState != store.RawPartial || !slices.Equal(row.RemoteParts, []string{"1.2"}) {
		t.Fatalf("row after the download %+v", row)
	}

	m.b.mem.clear()
	if body := m.body(t, msg.ID); body.RemotePictures != 1 {
		t.Fatalf("the copy gone: remote pictures %d", body.RemotePictures)
	}
	if _, err := m.part(msg.ID, "1.2"); errCode(t, err) != api.CodePartNotDownloaded {
		t.Fatalf("the large picture, the copy gone: %v", err)
	}
	if n := len(srv.calls()); n != 1 {
		t.Fatalf("%d fetches, want the download's only", n)
	}

	whole := m.seedPictures(t, 8, pictureMessage("whole@example.org", largePhoto, false), ingest.Policy{})
	if body := m.body(t, whole.ID); body.RemotePictures != 0 || len(body.InlineParts) != 2 {
		t.Fatalf("stored whole: inline parts %v, remote pictures %d", body.InlineParts, body.RemotePictures)
	}

	m.setNeverStore(t, false)
	if body := m.body(t, msg.ID); body.RemotePictures != 1 {
		t.Fatalf("the preference off: remote pictures %d", body.RemotePictures)
	}
	if _, err := m.download(ctx, msg.ID); err != nil {
		t.Fatal(err)
	}
	if row := m.row(t, msg.ID); row.RawState != store.RawFull || len(row.RemoteParts) != 0 {
		t.Fatalf("downloaded outside the mode: %+v", row)
	}
	if body := m.body(t, msg.ID); body.RemotePictures != 0 {
		t.Fatalf("stored whole again: remote pictures %d", body.RemotePictures)
	}
	if pic, err := m.part(msg.ID, "1.2"); err != nil || !bytes.Equal(pic.Data, largePhoto) {
		t.Fatalf("the large picture stored: %v", err)
	}
}

// A picture the stored file lacks although the row calls it stored (a
// skeleton the row does not describe) counts as on the server, and the row
// learns it, so that message.download fetches the message again.
func TestMessageBodyLostPicture(t *testing.T) {
	m := seedMailbox(t)
	srv := m.fakeServer()
	ctx := context.Background()
	raw := pictureMessage("holiday@example.org", largePhoto, false)
	msg := m.seedPictures(t, 7, raw, ingest.Policy{})
	srv.put("INBOX", 7, raw)
	var skel bytes.Buffer
	if _, err := mime.Skeleton(bytes.NewReader(raw), &skel, map[string]bool{"1.2": true}, mime.DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.b.store.PutMessageRaw(ctx, string(m.acc), msg.ID, store.RawWrite{}, func(w io.Writer) error {
		_, err := w.Write(skel.Bytes())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if row := m.row(t, msg.ID); row.RawState != store.RawFull {
		t.Fatalf("seeded %+v", row)
	}

	if body := m.body(t, msg.ID); body.RemotePictures != 1 || len(body.InlineParts) != 2 {
		t.Fatalf("lost picture: inline parts %v, remote pictures %d", body.InlineParts, body.RemotePictures)
	}
	if row := m.row(t, msg.ID); row.RawState != store.RawPartial || !slices.Equal(row.RemoteParts, []string{"1.2"}) {
		t.Fatalf("row after message.body %+v", row)
	}
	if _, err := m.download(ctx, msg.ID); err != nil {
		t.Fatal(err)
	}
	if body := m.body(t, msg.ID); body.RemotePictures != 0 {
		t.Fatalf("downloaded: remote pictures %d", body.RemotePictures)
	}
	if pic, err := m.part(msg.ID, "1.2"); err != nil || !bytes.Equal(pic.Data, largePhoto) {
		t.Fatalf("the picture after the download: %v", err)
	}
}

// A reply quotes the pictures the HTML shows; one on the server only is
// left out of the quote and reported (skipped, remote, with its real
// size), unless message.download holds the message, when it is copied
// from memory. Quoting never contacts the server.
func TestDraftCreateReplyRemotePicture(t *testing.T) {
	m := seedMailbox(t)
	srv := m.fakeServer()
	ctx := context.Background()
	raw := pictureMessage("holiday@example.org", largePhoto, false)
	m.setNeverStore(t, true)
	msg := m.seedPictures(t, 7, raw, neverStorePolicy)
	srv.put("INBOX", 7, raw)
	inline := func(d api.Draft) []api.DraftAttachment {
		var out []api.DraftAttachment
		for _, a := range d.Attachments {
			if a.Inline {
				out = append(out, a)
			}
		}
		return out
	}

	res := m.create(t, api.ComposeReply, api.MessageID(msg.ID), "Alice wrote:")
	if res.Quoted != api.QuoteHTML || len(res.Skipped) != 1 || res.Skipped[0].PartID != "1.2" || !res.Skipped[0].Remote ||
		res.Skipped[0].Size != int64(len(largePhoto)) {
		t.Fatalf("quoted %s, skipped %+v", res.Quoted, res.Skipped)
	}
	if pics := inline(res.Draft); len(pics) != 1 || pics[0].Size != int64(len(pngSignature)) {
		t.Fatalf("inline copies %+v", pics)
	}
	if refs := cidRefs.FindAllString(res.Draft.HTMLBody, -1); len(refs) != 1 {
		t.Errorf("the quote references %v", refs)
	}
	checkQuoteClean(t, res.Draft)
	if n := len(srv.calls()); n != 0 {
		t.Fatalf("quoting contacted the server: %d fetches", n)
	}

	if _, err := m.download(ctx, msg.ID); err != nil {
		t.Fatal(err)
	}
	res = m.create(t, api.ComposeReply, api.MessageID(msg.ID), "Alice wrote:")
	pics := inline(res.Draft)
	if len(res.Skipped) != 0 || len(pics) != 2 || !slices.ContainsFunc(pics, func(a api.DraftAttachment) bool { return a.Size == int64(len(largePhoto)) }) {
		t.Fatalf("held: skipped %+v, inline copies %+v", res.Skipped, pics)
	}
	if refs := cidRefs.FindAllString(res.Draft.HTMLBody, -1); len(refs) != 2 {
		t.Errorf("the quote references %v", refs)
	}
	checkQuoteClean(t, res.Draft)
}

// A store already in neverStoreAttachments under rule 2 (every attachment
// the HTML does not show) is judged again once under rule 3: a message
// settled while its file holds a picture the HTML shows of
// api.LargeAttachmentMinBytes and more loses it, whole or partial; one
// whose pictures are small keeps them, and one never to be reduced keeps
// everything. From rule 2 only a message holding so large a part is read
// again: a small attachment settled at 0 (the probe; rule 2 would have
// removed it, so no store has it) stays. A second pass, and a message
// settled after the first, are left alone: the mark records rule 3.
func TestAttachmentStepNeverStoreRule(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	step := newAttachmentStep(m.b).(*attachmentStep)
	m.setNeverStore(t, true)
	settle := func(msg store.Message) store.Message {
		t.Helper()
		if err := m.b.store.SetStrippableBytes(ctx, msg.ID, 0); err != nil {
			t.Fatal(err)
		}
		return m.row(t, msg.ID)
	}
	// As rule 2 leaves them.
	whole := settle(m.seedPictures(t, 7, pictureMessage("whole@example.org", largePhoto, false), ingest.Policy{}))
	partial := settle(m.seedPictures(t, 8, pictureMessage("partial@example.org", largePhoto, true), smallOnly))
	small := settle(m.seedPictures(t, 9, pictureMessage("small@example.org", smallPhoto, false), ingest.Policy{}))
	probe := m.seedWhole(t, 10, notesMessage("probe@example.org"), 0)
	never := m.seedWhole(t, 11, pictureMessage("never@example.org", largePhoto, false), store.StrippableNever)
	if partial.RawState != store.RawPartial || !slices.Equal(partial.RemoteParts, []string{"2"}) || whole.RawState != store.RawFull {
		t.Fatalf("seeded %+v / %+v", whole, partial)
	}
	if err := m.b.store.SetMeta(ctx, metaReevaluated, reevaluatedRule2); err != nil {
		t.Fatal(err)
	}
	if key, err := step.Key(ctx, time.Now()); err != nil || key != "3:never:"+time.Now().Format(time.DateOnly) {
		t.Fatalf("key %q %v", key, err)
	}

	runStep(t, step)
	for _, c := range []struct {
		name       string
		id         string
		remote     []string
		strippable int64
	}{
		{"whole", whole.ID, []string{"1.2"}, 0},
		{"partial", partial.ID, []string{"1.2", "2"}, 0},
		{"small", small.ID, nil, 0},
		{"probe", probe.ID, nil, 0},
		{"never", never.ID, nil, store.StrippableNever},
	} {
		got := m.row(t, c.id)
		if !slices.Equal(got.RemoteParts, c.remote) || (len(c.remote) > 0) != (got.RawState == store.RawPartial) ||
			got.StrippableBytes != c.strippable {
			t.Errorf("%s: %s %v strippable %d, want %v %d", c.name, got.RawState, got.RemoteParts, got.StrippableBytes, c.remote, c.strippable)
		}
	}
	if v := metaOf(t, m.b, metaReevaluated); v != "3" {
		t.Errorf("re-evaluation mark %q", v)
	}
	if body := m.body(t, whole.ID); body.RemotePictures != 1 {
		t.Errorf("reduced message: remote pictures %d", body.RemotePictures)
	}
	if logo, err := m.part(whole.ID, "1.3"); err != nil || !bytes.Equal(logo.Data, pngSignature) {
		t.Errorf("the small picture after the pass: %v", err)
	}
	if pic, err := m.part(small.ID, "1.2"); err != nil || !bytes.Equal(pic.Data, smallPhoto) {
		t.Errorf("a small picture after the pass: %v", err)
	}

	file := messageFiles(t, m.b.store)
	late := settle(m.seedPictures(t, 12, pictureMessage("late@example.org", largePhoto, false), ingest.Policy{}))
	runStep(t, step)
	if got := m.row(t, late.ID); got.RawState != store.RawFull || got.StrippableBytes != 0 {
		t.Fatalf("judged again in the same switch-on: %+v", got)
	}
	for path, content := range file {
		if now := messageFiles(t, m.b.store)[path]; now != content {
			t.Fatalf("the second pass changed %s", path)
		}
	}
}

// A daemon with rule 3 over a store whose pass under rule 2 finished today
// runs the pass again at once (the key names the rule) and judges the
// settled messages again.
func TestNeverStoreRuleUpgrade(t *testing.T) {
	fastRawLoop(t, time.Millisecond, 10*time.Millisecond, time.Hour)
	m := seedMailbox(t)
	ctx := context.Background()
	stepMeta := rawStepMetaPrefix + attachmentStepName
	m.setNeverStore(t, true)
	m.b.takeRawRestart(attachmentStepName) // the switch-on's
	for key, value := range map[string]string{
		stepMeta:        "2:never:" + time.Now().Format(time.DateOnly) + "|done",
		metaReevaluated: reevaluatedRule2,
	} {
		if err := m.b.store.SetMeta(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	msg := m.seedPictures(t, 7, pictureMessage("holiday@example.org", largePhoto, false), ingest.Policy{})
	if err := m.b.store.SetStrippableBytes(ctx, msg.ID, 0); err != nil {
		t.Fatal(err)
	}

	startRawLoop(t, m.b)
	eventually(t, "the pass under rule 3", func() bool { return metaOf(t, m.b, stepMeta) == neverKey()+"|done" })
	if got := m.row(t, msg.ID); got.RawState != store.RawPartial || !slices.Equal(got.RemoteParts, []string{"1.2"}) {
		t.Fatalf("after the pass %+v", got)
	}
	if v := metaOf(t, m.b, metaReevaluated); v != neverRule {
		t.Errorf("re-evaluation mark %q", v)
	}
}
