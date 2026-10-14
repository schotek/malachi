// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package ingest

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// payload is n deterministic bytes that do not compress to nothing.
func payload(seed uint32, n int) []byte {
	b := make([]byte, n)
	x := seed + 1
	for i := range b {
		x = x*1664525 + 1013904223
		b[i] = byte(x >> 24)
	}
	return b
}

// base64Lines encodes data in 76-character CRLF lines.
func base64Lines(data []byte) string {
	enc := base64.StdEncoding.EncodeToString(data)
	var sb strings.Builder
	for len(enc) > 76 {
		sb.WriteString(enc[:76])
		sb.WriteString("\r\n")
		enc = enc[76:]
	}
	sb.WriteString(enc)
	return sb.String()
}

// report is a message with every kind of part the rule tells apart:
//
//	1.1 the HTML body, showing 1.2 through cid:
//	1.2 a 150 KiB picture the HTML shows (kept; under NeverStore a candidate)
//	2   a 200 KiB PDF (a candidate)
//	3   a small text file (kept; under NeverStore a candidate)
//	4   a 120 KiB picture with an Outlook-style Content-ID nothing shows (a candidate)
type report struct {
	raw                     []byte
	logo, pdf, small, stray []byte
}

func newReport(messageID string, pdfSize int) report {
	return newReportLogo(messageID, pdfSize, 150<<10)
}

// smallLogo is the size of a picture the HTML shows that NeverStore keeps.
const smallLogo = 20 << 10

// newReportLogo is newReport with a picture 1.2 of logoSize bytes.
func newReportLogo(messageID string, pdfSize, logoSize int) report {
	r := report{
		logo:  payload(1, logoSize),
		pdf:   payload(2, pdfSize),
		small: []byte("a small text attachment"),
		stray: payload(4, 120<<10),
	}
	r.raw = []byte(strings.Join([]string{
		"From: Alice <alice@example.org>",
		"To: me@example.test",
		"Subject: Quarterly report",
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
		`<p>Numbers attached. <img src="cid:logo@example.org"></p>`,
		"--rel",
		"Content-Type: image/png",
		"Content-ID: <logo@example.org>",
		"Content-Transfer-Encoding: base64",
		"",
		base64Lines(r.logo),
		"--rel--",
		"",
		"--mix",
		"Content-Type: application/pdf",
		`Content-Disposition: attachment; filename="report.pdf"`,
		"Content-Transfer-Encoding: base64",
		"",
		base64Lines(r.pdf),
		"--mix",
		"Content-Type: text/plain; charset=utf-8",
		`Content-Disposition: attachment; filename="small.txt"`,
		"",
		string(r.small),
		"--mix",
		"Content-Type: image/png",
		"Content-ID: <image001.png@01DB0000.00000000>",
		`Content-Disposition: inline; filename="image001.png"`,
		"Content-Transfer-Encoding: base64",
		"",
		base64Lines(r.stray),
		"--mix--",
		"",
	}, "\r\n"))
	return r
}

type fixture struct {
	st     *store.Store
	acc    store.Account
	inbox  store.Folder
	drafts store.Folder
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "store.db"), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	acc := store.Account{Enabled: true, Config: api.AccountConfig{Name: "Test", Email: "me@example.test",
		IMAP: &api.ServerConfig{Host: "imap.example.test", Port: 993, Security: api.SecurityTLS, Username: "me", AuthMethod: api.AuthPassword}}}
	if err := st.AddAccount(ctx, &acc); err != nil {
		t.Fatal(err)
	}
	folders, _, err := st.UpsertFolders(ctx, acc.ID, []store.Folder{
		{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Subscribed: true, Selectable: true},
		{Mailbox: "Drafts", Name: "Drafts", Path: "Drafts", Role: api.RoleDrafts, Subscribed: true, Selectable: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{st: st, acc: acc}
	for _, fo := range folders {
		switch fo.Role {
		case api.RoleInbox:
			f.inbox = fo
		case api.RoleDrafts:
			f.drafts = fo
		}
	}
	return f
}

// row adds a message row whose body is not downloaded yet.
func (f *fixture) row(t *testing.T, folder store.Folder, uid uint32, received time.Time, messageID string) store.Message {
	t.Helper()
	m := &store.Message{AccountID: f.acc.ID, FolderID: folder.ID, UID: uid, Subject: "Quarterly report",
		Date: received, InternalDate: received, RFCMessageID: messageID, Size: 1}
	if err := f.st.UpsertMessages(context.Background(), []*store.Message{m}); err != nil {
		t.Fatal(err)
	}
	return f.get(t, m.ID)
}

func (f *fixture) get(t *testing.T, id string) store.Message {
	t.Helper()
	m, err := f.st.GetMessage(context.Background(), f.acc.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (f *fixture) raw(t *testing.T, id string) []byte {
	t.Helper()
	r, err := f.st.OpenMessageRaw(context.Background(), f.acc.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// part extracts one part's decoded bytes from the stored file.
func (f *fixture) part(t *testing.T, id, partID string) []byte {
	t.Helper()
	p, err := mime.ExtractPart(bytes.NewReader(f.raw(t, id)), partID, mime.DefaultLimits(), 32<<20)
	if err != nil {
		t.Fatalf("part %s: %v", partID, err)
	}
	return p.Body
}

// text is the stored text body.
func (f *fixture) text(t *testing.T, id string) string {
	t.Helper()
	text, _, _, err := f.st.GetMessageText(context.Background(), f.acc.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

// stagingEmpty checks that no staged file was left behind.
func (f *fixture) stagingEmpty(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(f.st.Path()), "staging"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("%d staged files left", len(entries))
	}
}

var ingestNow = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

func (f *fixture) request(m store.Message, body []byte, pol Policy) Request {
	return Request{
		Target: Target{AccountID: f.acc.ID, MessageID: m.ID, Role: api.RoleInbox, HasServerCopy: m.UID > 0,
			InternalDate: m.InternalDate, Date: m.Date, HydratedAt: m.HydratedAt},
		Body: bytes.NewReader(body), Policy: pol, Now: ingestNow,
		Expect: store.RawExpect{BodyState: store.BodyNone},
	}
}

var smallOnly = Policy{AttachmentOfflineDays: api.AttachmentOfflineNone}

// An old message loses its large, unshown attachments at ingest; the row
// still describes the whole message, and every kept part reads back byte
// for byte.
func TestStoreLeavesOldAttachmentsOnServer(t *testing.T) {
	for _, codec := range []store.RawCodec{store.RawPlain, store.RawZstd} {
		t.Run(codec.String(), func(t *testing.T) {
			f := newFixture(t)
			f.st.SetRawCodec(codec)
			ctx := context.Background()
			rep := newReport("report@example.org", 200<<10)
			old := f.row(t, f.inbox, 1, ingestNow.AddDate(-1, 0, 0), "report@example.org")
			whole := f.row(t, f.inbox, 2, ingestNow.AddDate(-1, 0, 0), "report@example.org")

			res, err := Store(ctx, f.st, f.request(old, rep.raw, smallOnly), nil)
			if err != nil {
				t.Fatal(err)
			}
			if res.Size != int64(len(rep.raw)) || !slices.Equal(res.RemoteParts, []string{"2", "4"}) ||
				res.RemoteBytes != int64(len(rep.pdf)+len(rep.stray)) {
				t.Fatalf("result %+v", res)
			}
			// The same message stored whole, for comparison.
			if _, err := Store(ctx, f.st, f.request(whole, rep.raw, Policy{}), nil); err != nil {
				t.Fatal(err)
			}
			got, want := f.get(t, old.ID), f.get(t, whole.ID)
			if got.RawState != store.RawPartial || !slices.Equal(got.RemoteParts, []string{"2", "4"}) ||
				got.RemoteBytes != res.RemoteBytes || got.StrippableBytes != res.RemoteBytes || !got.HydratedAt.IsZero() {
				t.Fatalf("partial row %+v", got)
			}
			if want.RawState != store.RawFull || want.StrippableBytes != res.RemoteBytes {
				t.Fatalf("whole row %+v", want)
			}
			if got.Size != int64(len(rep.raw)) || got.BodyState != store.BodyFetched || got.Snippet != want.Snippet ||
				f.text(t, old.ID) != f.text(t, whole.ID) || !got.HasHTML {
				t.Fatalf("body columns differ: %+v", got)
			}
			if len(got.Attachments) != 4 || len(want.Attachments) != 4 {
				t.Fatalf("attachments %+v / %+v", got.Attachments, want.Attachments)
			}
			for i, a := range got.Attachments {
				w := want.Attachments[i]
				remote := a.PartID == "2" || a.PartID == "4"
				if a.PartID != w.PartID || a.Size != w.Size || a.Filename != w.Filename || a.Remote != remote || w.Remote {
					t.Errorf("attachment %d: %+v, whole %+v", i, a, w)
				}
			}
			if !bytes.Equal(f.part(t, old.ID, "1.2"), rep.logo) || !bytes.Equal(f.part(t, old.ID, "3"), rep.small) {
				t.Error("a kept part changed")
			}
			if len(f.part(t, old.ID, "2")) != 0 || len(f.part(t, old.ID, "4")) != 0 {
				t.Error("an omitted part is still stored")
			}
			if stored := f.raw(t, old.ID); len(stored) >= len(rep.raw)/2 {
				t.Errorf("skeleton of %d bytes for a message of %d", len(stored), len(rep.raw))
			}
			if !bytes.Equal(f.raw(t, whole.ID), rep.raw) {
				t.Error("the whole message is not stored as received")
			}
			f.stagingEmpty(t)
		})
	}
}

// Nothing is left on the server for a recent message, a draft, a
// download on demand or a message without a server copy; the candidates
// are recorded for the background pass all the same.
func TestStoreKeepsWhole(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	rep := newReport("report@example.org", 200<<10)
	candidates := int64(len(rep.pdf) + len(rep.stray))
	cases := []struct {
		name string
		mut  func(*Request)
		pol  Policy
	}{
		{"recent", func(r *Request) { r.InternalDate = ingestNow.AddDate(0, 0, -3) }, Policy{AttachmentOfflineDays: 30}},
		{"drafts", func(r *Request) { r.Role = api.RoleDrafts }, smallOnly},
		{"on demand", func(r *Request) { r.OnDemand = true }, smallOnly},
		{"no server copy", func(r *Request) { r.HasServerCopy = false }, smallOnly},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := f.row(t, f.inbox, uint32(10+i), ingestNow.AddDate(-1, 0, 0), "report@example.org")
			req := f.request(m, rep.raw, c.pol)
			c.mut(&req)
			res, err := Store(ctx, f.st, req, nil)
			if err != nil || len(res.RemoteParts) != 0 {
				t.Fatalf("%+v %v", res, err)
			}
			got := f.get(t, m.ID)
			if got.RawState != store.RawFull || got.StrippableBytes != candidates || !bytes.Equal(f.raw(t, m.ID), rep.raw) {
				t.Fatalf("row %+v", got)
			}
			if onDemand := c.name == "on demand"; onDemand == got.HydratedAt.IsZero() {
				t.Errorf("hydrated at %v", got.HydratedAt)
			}
		})
	}
}

// A message whose split is doubtful (here: no final boundary) is stored
// whole and never looked at again, under any policy; a signed one too.
func TestStoreFallsBackWhole(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	rep := newReport("report@example.org", 200<<10)
	unterminated := bytes.TrimSuffix(rep.raw, []byte("--mix--\r\n"))
	signed := []byte(strings.Join([]string{
		"From: a@example.org", "Subject: signed", "MIME-Version: 1.0",
		`Content-Type: multipart/signed; protocol="application/pgp-signature"; micalg=pgp-sha256; boundary="sig"`,
		"", "--sig", "Content-Type: application/octet-stream", `Content-Disposition: attachment; filename="big.bin"`,
		"Content-Transfer-Encoding: base64", "", base64Lines(payload(9, 300<<10)),
		"--sig", "Content-Type: application/pgp-signature", "", "sig", "--sig--", "",
	}, "\r\n"))
	for i, raw := range [][]byte{unterminated, signed} {
		m := f.row(t, f.inbox, uint32(20+i), ingestNow.AddDate(-1, 0, 0), "")
		res, err := Store(ctx, f.st, f.request(m, raw, smallOnly), nil)
		if err != nil || len(res.RemoteParts) != 0 {
			t.Fatalf("message %d: %+v %v", i, res, err)
		}
		got := f.get(t, m.ID)
		if got.RawState != store.RawFull || got.StrippableBytes != store.StrippableNever || got.BodyState != store.BodyFetched || !bytes.Equal(f.raw(t, m.ID), raw) {
			t.Fatalf("message %d: row %+v", i, got)
		}
	}
	f.stagingEmpty(t)
}

func TestStoreErrors(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	rep := newReport("report@example.org", 200<<10)

	// Over the cap: nothing stored.
	m := f.row(t, f.inbox, 30, ingestNow, "")
	req := f.request(m, rep.raw, Policy{})
	req.Limit = 1000
	if _, err := Store(ctx, f.st, req, nil); !errors.Is(err, ErrTooBig) || !errors.Is(err, store.ErrTooBig) {
		t.Fatalf("too big: %v", err)
	}
	if _, err := f.st.OpenMessageRaw(ctx, f.acc.ID, m.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("file of a message over the cap: %v", err)
	}

	// Unparsable: a new body is kept as received for the caller to mark.
	junk := []byte{}
	if _, err := Store(ctx, f.st, f.request(m, junk, Policy{}), nil); !errors.Is(err, ErrUnparsable) {
		t.Fatalf("empty body: %v", err)
	}
	if got := f.get(t, m.ID); got.StrippableBytes != store.StrippableNever || got.BodyState != store.BodyNone {
		t.Errorf("unparsable row %+v", got)
	}

	// The row changed meanwhile: store.ErrConflict, nothing replaced.
	done := f.row(t, f.inbox, 31, ingestNow, "")
	if _, err := Store(ctx, f.st, f.request(done, rep.raw, Policy{}), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Store(ctx, f.st, f.request(done, []byte("Subject: other\r\n\r\nx"), Policy{}), nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second download: %v", err)
	}
	if !bytes.Equal(f.raw(t, done.ID), rep.raw) {
		t.Error("a conflicting download replaced the file")
	}

	// The row is gone.
	gone := f.row(t, f.inbox, 32, ingestNow, "")
	if _, err := f.st.DeleteMessages(ctx, f.acc.ID, []string{gone.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := Store(ctx, f.st, f.request(gone, rep.raw, Policy{}), nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted row: %v", err)
	}

	// A body that breaks off: the reader's error, nothing stored.
	broken := f.row(t, f.inbox, 33, ingestNow, "")
	req = f.request(broken, nil, Policy{})
	req.Body = io.MultiReader(bytes.NewReader(rep.raw[:1000]), errReader{io.ErrUnexpectedEOF})
	if _, err := Store(ctx, f.st, req, nil); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("broken body: %v", err)
	}
	if got := f.get(t, broken.ID); got.BodyState != store.BodyNone {
		t.Errorf("broken download stored: %+v", got)
	}
	f.stagingEmpty(t)
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

// Reduced by the background pass, then downloaded on demand: the stored
// message is byte for byte the original again, and the grace starts.
func TestStripThenDownloadRestoresOriginal(t *testing.T) {
	for _, codec := range []store.RawCodec{store.RawPlain, store.RawZstd} {
		t.Run(codec.String(), func(t *testing.T) {
			f := newFixture(t)
			f.st.SetRawCodec(codec)
			ctx := context.Background()
			rep := newReport("report@example.org", 300<<10)
			m := f.row(t, f.inbox, 1, ingestNow.AddDate(0, -2, 0), "<report@example.org>")
			if _, err := Store(ctx, f.st, f.request(m, rep.raw, Policy{AttachmentOfflineDays: 90}), nil); err != nil {
				t.Fatal(err)
			}
			m = f.get(t, m.ID)
			if m.RawState != store.RawFull || m.StrippableBytes == 0 {
				t.Fatalf("recent message reduced: %+v", m)
			}

			// Not old enough yet for 90 days: nothing to do but wait.
			out, err := Strip(ctx, f.st, m, Policy{AttachmentOfflineDays: 90}, ingestNow, nil)
			if err != nil || out != Later {
				t.Fatalf("strip under 90 days: %v %v", out, err)
			}
			out, err = Strip(ctx, f.st, m, Policy{AttachmentOfflineDays: 30}, ingestNow, nil)
			if err != nil || out != Reduced {
				t.Fatalf("strip under 30 days: %v %v", out, err)
			}
			reduced := f.get(t, m.ID)
			if reduced.RawState != store.RawPartial || !slices.Equal(reduced.RemoteParts, []string{"2", "4"}) ||
				reduced.UpdatedAt != m.UpdatedAt || f.text(t, m.ID) != f.text(t, reduced.ID) {
				t.Fatalf("reduced row %+v", reduced)
			}
			// A second pass over the old view of the row conflicts.
			if _, err := Strip(ctx, f.st, m, smallOnly, ingestNow, nil); !errors.Is(err, store.ErrConflict) {
				t.Fatalf("stale strip: %v", err)
			}

			// The download of the reduced message.
			req := f.request(reduced, rep.raw, smallOnly)
			req.OnDemand, req.Verify, req.Strict = true, &reduced, true
			req.Expect = store.RawExpect{BodyState: store.BodyFetched, RawState: store.RawPartial, HydratedAt: &reduced.HydratedAt}
			res, err := Store(ctx, f.st, req, nil)
			if err != nil || len(res.RemoteParts) != 0 {
				t.Fatalf("download: %+v %v", res, err)
			}
			if !bytes.Equal(f.raw(t, m.ID), rep.raw) {
				t.Fatal("the downloaded message differs from the original")
			}
			whole := f.get(t, m.ID)
			if whole.RawState != store.RawFull || len(whole.RemoteParts) != 0 || whole.HydratedAt.IsZero() ||
				whole.Attachments[1].Remote || whole.StrippableBytes == 0 {
				t.Fatalf("row after the download %+v", whole)
			}
			// The grace keeps it whole, even under the strictest policy.
			if out, err := Strip(ctx, f.st, whole, smallOnly, time.Now(), nil); err != nil || out != Later {
				t.Fatalf("strip in the grace: %v %v", out, err)
			}
			if out, err := Strip(ctx, f.st, whole, smallOnly, time.Now().Add(HydratedKeep+time.Minute), nil); err != nil || out != Reduced {
				t.Fatalf("strip after the grace: %v %v", out, err)
			}
			f.stagingEmpty(t)
		})
	}
}

// A message the background pass listed in the inbox but that the user
// moved into Drafts before its turn keeps every part: the decision is
// taken on the row as it is under the lock, in the folder it is in then,
// whether the move has reached the server (a UID in Drafts) or not.
func TestStripSkipsMessageMovedToDrafts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	rep := newReport("report@example.org", 200<<10)
	m := f.row(t, f.inbox, 1, ingestNow.AddDate(-1, 0, 0), "report@example.org")
	if _, err := Store(ctx, f.st, f.request(m, rep.raw, Policy{}), nil); err != nil {
		t.Fatal(err)
	}
	listed := f.get(t, m.ID)
	if listed.StrippableBytes == 0 || listed.RawState != store.RawFull {
		t.Fatalf("stored %+v", listed)
	}
	if _, err := f.st.MoveMessages(ctx, f.acc.ID, []string{m.ID}, f.drafts.ID); err != nil {
		t.Fatal(err)
	}
	for _, reconciled := range []bool{false, true} {
		if reconciled {
			if err := f.st.AssignUID(ctx, m.ID, 7, 0, nil); err != nil {
				t.Fatal(err)
			}
		}
		out, err := Strip(ctx, f.st, listed, smallOnly, ingestNow, nil)
		if err != nil || out != Later {
			t.Fatalf("reconciled %v: %v %v", reconciled, out, err)
		}
		got := f.get(t, m.ID)
		if got.FolderID != f.drafts.ID || got.RawState != store.RawFull || len(got.RemoteParts) != 0 || !bytes.Equal(f.raw(t, m.ID), rep.raw) {
			t.Fatalf("reconciled %v: a draft was reduced: %+v", reconciled, got)
		}
	}
	f.stagingEmpty(t)
}

// A download that is not the stored message is refused: another
// Message-ID, or, for IMAP, other parts.
func TestStoreRefusesMismatch(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	rep := newReport("report@example.org", 200<<10)
	m := f.row(t, f.inbox, 1, ingestNow.AddDate(-1, 0, 0), "report@example.org")
	if _, err := Store(ctx, f.st, f.request(m, rep.raw, smallOnly), nil); err != nil {
		t.Fatal(err)
	}
	m = f.get(t, m.ID)
	before := f.raw(t, m.ID)
	download := func(raw []byte, strict bool) error {
		req := f.request(m, raw, smallOnly)
		req.OnDemand, req.Verify, req.Strict = true, &m, strict
		req.Expect = store.RawExpect{BodyState: store.BodyFetched, RawState: store.RawPartial}
		_, err := Store(ctx, f.st, req, nil)
		return err
	}
	if err := download(newReport("other@example.org", 200<<10).raw, false); !errors.Is(err, ErrMismatch) {
		t.Fatalf("another Message-ID: %v", err)
	}
	resized := newReport("REPORT@example.org", 201<<10).raw // case of the id does not matter
	if err := download(resized, true); !errors.Is(err, ErrMismatch) {
		t.Fatalf("other parts under strict: %v", err)
	}
	if err := download([]byte{}, true); !errors.Is(err, ErrUnparsable) {
		t.Fatalf("unparsable download: %v", err)
	}
	if !bytes.Equal(f.raw(t, m.ID), before) || f.get(t, m.ID).RawState != store.RawPartial {
		t.Fatal("a refused download changed the message")
	}
	// Graph rebuilds the MIME: without strict the part list may change.
	if err := download(resized, false); err != nil {
		t.Fatalf("lenient download: %v", err)
	}
	if got := f.get(t, m.ID); got.RawState != store.RawFull || got.Attachments[1].Size != 201<<10 {
		t.Fatalf("row after a lenient download %+v", got)
	}
	f.stagingEmpty(t)
}

// The stored Message-ID and the parser's name the same message despite
// brackets, folding and case, and despite a stray bracket that an older
// parser left in the stored one.
func TestMessageIDKey(t *testing.T) {
	cases := []struct {
		stored, parsed string
		same           bool
	}{
		{"a@x", "A@X", true},
		{"<a@x>", "a@x", true},
		{"0>0", "0", true},
		{"x>y@example.org", "x", true},
		{"0>0", "1", false},
		{"a@x", "b@x", false},
	}
	for _, c := range cases {
		if same := messageIDKey(c.stored) == messageIDKey(c.parsed); same != c.same {
			t.Errorf("%q vs %q: same = %v", c.stored, c.parsed, same)
		}
	}
	// Downloaded again, the message whose stored id an older parser took
	// as "0>0" is still the stored one.
	p, err := mime.Parse(strings.NewReader("Message-ID: <0>0>\r\n\r\nbody\r\n"), mime.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if err := verify(&store.Message{RFCMessageID: "0>0"}, p, false); err != nil {
		t.Errorf("legacy id: %v", err)
	}
}

// Strip settles what it cannot or need not reduce, so the background pass
// does not come back to it.
func TestStripSettles(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	old := ingestNow.AddDate(-1, 0, 0)

	plain := f.row(t, f.inbox, 1, old, "")
	if _, err := Store(ctx, f.st, f.request(plain, []byte("Subject: hi\r\n\r\nno attachments\r\n"), Policy{}), nil); err != nil {
		t.Fatal(err)
	}
	plain = f.get(t, plain.ID)
	if plain.StrippableBytes != 0 {
		t.Fatalf("message without attachments: strippable %d", plain.StrippableBytes)
	}

	// A file damaged on disk is left alone.
	rep := newReport("report@example.org", 200<<10)
	damaged := f.row(t, f.inbox, 2, old, "")
	if _, err := Store(ctx, f.st, f.request(damaged, rep.raw, Policy{}), nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.st.MessageRawPath(f.acc.ID, damaged.ID), rep.raw[:len(rep.raw)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := Strip(ctx, f.st, f.get(t, damaged.ID), smallOnly, ingestNow, nil)
	if err != nil || out != Whole || f.get(t, damaged.ID).StrippableBytes != 0 {
		t.Fatalf("damaged: %v %v", out, err)
	}

	// A message whose file is gone.
	missing := f.row(t, f.inbox, 3, old, "")
	if _, err := Store(ctx, f.st, f.request(missing, rep.raw, Policy{}), nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.st.MessageRawPath(f.acc.ID, missing.ID)); err != nil {
		t.Fatal(err)
	}
	out, err = Strip(ctx, f.st, f.get(t, missing.ID), smallOnly, ingestNow, nil)
	if err != nil || out != Whole {
		t.Fatalf("missing file: %v %v", out, err)
	}
	f.stagingEmpty(t)
}
