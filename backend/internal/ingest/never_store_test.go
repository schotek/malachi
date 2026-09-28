// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package ingest

import (
	"bytes"
	"context"
	"errors"
	"io"
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

var neverStore = Policy{NeverStore: true}

// blockStaging puts a file where the store's staging directory is, so
// that staging a message on disk fails from then on: whatever is still
// stored afterwards was staged in memory.
func (f *fixture) blockStaging(t *testing.T) {
	t.Helper()
	dir := filepath.Join(filepath.Dir(f.st.Path()), "staging")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if st, err := f.st.StageRaw(context.Background(), 0); err == nil {
		st.Remove()
		t.Fatal("staged on disk without a staging directory")
	}
}

// smallAttachment is a message whose one attachment is small.
func smallAttachment(messageID string) []byte {
	return []byte(strings.Join([]string{
		"From: Alice <alice@example.org>",
		"To: me@example.test",
		"Subject: Notes",
		"Date: Mon, 01 Sep 2025 10:00:00 +0000",
		"Message-ID: <" + messageID + ">",
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="mix"`,
		"",
		"--mix",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"see the notes",
		"--mix",
		"Content-Type: text/plain; charset=utf-8",
		`Content-Disposition: attachment; filename="notes.txt"`,
		"",
		"the notes themselves",
		"--mix--",
		"",
	}, "\r\n"))
}

// Under NeverStore every attachment the HTML does not show is a candidate,
// whatever its size, and is left on the server whatever the message's
// age, however recently it was downloaded, and on demand too; Drafts, the
// outbox and a message without a server copy keep everything, and a
// truncated or signed one is never reduced.
func TestDecideNeverStore(t *testing.T) {
	p := parsedWith(`<p><img src="cid:logo@example.org"></p>`,
		att("1.2", 10, "logo@example.org"),
		att("2", 1, ""),
		att("3", 0, ""),
		att("4", 5<<20, "stray@example.org"),
	)
	pol := Policy{AttachmentOfflineDays: 30, NeverStore: true} // overrides the days
	want, wantBytes := []string{"2", "4"}, int64(1+5<<20)
	recent := oldTarget()
	recent.InternalDate = policyNow.Add(-time.Hour)
	recent.HydratedAt = policyNow.Add(-time.Minute)
	for _, onDemand := range []bool{false, true} {
		plan := Decide(p, recent, pol, policyNow, onDemand)
		if got := omitted(plan); !slices.Equal(got, want) || plan.OmitBytes != wantBytes || plan.CandidateBytes != wantBytes || plan.Never {
			t.Errorf("on demand %v: omit %v (%d of %d bytes), never %v", onDemand, got, plan.OmitBytes, plan.CandidateBytes, plan.Never)
		}
		if plan.Strippable() != wantBytes {
			t.Errorf("strippable %d", plan.Strippable())
		}
	}
	undated := oldTarget()
	undated.InternalDate = time.Time{}
	if got := omitted(Decide(p, undated, pol, policyNow, false)); !slices.Equal(got, want) {
		t.Errorf("undated: omit %v", got)
	}
	for name, mut := range map[string]func(*Target){
		"drafts":         func(t *Target) { t.Role = api.RoleDrafts },
		"outbox":         func(t *Target) { t.Role = api.RoleOutbox },
		"no server copy": func(t *Target) { t.HasServerCopy = false },
	} {
		tg := oldTarget()
		mut(&tg)
		plan := Decide(p, tg, pol, policyNow, false)
		if len(plan.Omit) != 0 || plan.CandidateBytes != wantBytes || plan.Never {
			t.Errorf("%s: %+v", name, plan)
		}
	}
	truncated := parsedWith("", att("2", 5<<20, ""))
	truncated.Truncated = true
	crypto := parsedWith("", att("2", 5<<20, ""))
	crypto.Crypto = true
	for name, p := range map[string]*mime.Parsed{"truncated": truncated, "signed or encrypted": crypto, "nil": nil} {
		plan := Decide(p, oldTarget(), pol, policyNow, false)
		if plan.Omit != nil || plan.CandidateBytes != 0 || !plan.Never || plan.Strippable() != store.StrippableNever {
			t.Errorf("%s: %+v", name, plan)
		}
	}
	if (Policy{}).MinBytes() != api.LargeAttachmentMinBytes || pol.MinBytes() != 1 {
		t.Errorf("min bytes %d, %d", (Policy{}).MinBytes(), pol.MinBytes())
	}
}

// Under NeverStore a picture the HTML shows is a candidate too when it has
// api.LargeAttachmentMinBytes or more; a smaller one is not, and outside
// NeverStore none is. With an incomplete reference list every part with a
// Content-ID counts as shown: the large ones are candidates all the same,
// the small ones are kept.
func TestDecideNeverStoreShownPictures(t *testing.T) {
	big := int64(api.LargeAttachmentMinBytes)
	p := parsedWith(`<p><img src="cid:Photo@Example.org"> <img src="cid:icon@example.org"></p>`,
		att("1.2", big, "<photo@example.org>"),
		att("1.3", big-1, "icon@example.org"),
		att("2", 10, "stray@example.org"),
		att("3", 5, ""),
	)
	plan := Decide(p, oldTarget(), neverStore, policyNow, false)
	if got := omitted(plan); !slices.Equal(got, []string{"1.2", "2", "3"}) || plan.OmitBytes != big+15 || plan.CandidateBytes != big+15 {
		t.Errorf("never store: omit %v (%d of %d bytes)", got, plan.OmitBytes, plan.CandidateBytes)
	}
	for _, pol := range []Policy{{}, {AttachmentOfflineDays: 30}, {AttachmentOfflineDays: api.AttachmentOfflineNone}} {
		if plan := Decide(p, oldTarget(), pol, policyNow, false); len(plan.Omit) != 0 || plan.CandidateBytes != 0 {
			t.Errorf("days %d: a shown picture is a candidate: %+v", pol.AttachmentOfflineDays, plan)
		}
	}
	drafts := oldTarget()
	drafts.Role = api.RoleDrafts
	if plan := Decide(p, drafts, neverStore, policyNow, false); len(plan.Omit) != 0 || plan.CandidateBytes != big+15 {
		t.Errorf("drafts: %+v", plan)
	}

	// An incomplete reference list (a malachi-cid: URL here).
	p.RawHTML += `<img src="malachi-cid:acc/msg/2">`
	plan = Decide(p, oldTarget(), neverStore, policyNow, false)
	if got := omitted(plan); !slices.Equal(got, []string{"1.2", "3"}) || plan.CandidateBytes != big+5 {
		t.Errorf("incomplete references: omit %v, candidates %d", got, plan.CandidateBytes)
	}
	if plan := Decide(p, oldTarget(), Policy{AttachmentOfflineDays: api.AttachmentOfflineNone}, policyNow, false); len(plan.Omit) != 0 {
		t.Errorf("incomplete references, days -1: omit %v", omitted(plan))
	}
}

// Under NeverStore a body is received into memory, never into the staging
// area: a recent message downloaded on demand keeps no attachment but the
// small picture its HTML shows, and the whole message is handed back.
func TestStoreNeverStore(t *testing.T) {
	for _, codec := range []store.RawCodec{store.RawPlain, store.RawZstd} {
		t.Run(codec.String(), func(t *testing.T) {
			f := newFixture(t)
			f.st.SetRawCodec(codec)
			f.blockStaging(t)
			ctx := context.Background()
			rep := newReportLogo("report@example.org", 200<<10, smallLogo)
			remote := int64(len(rep.pdf) + len(rep.small) + len(rep.stray))

			for i, onDemand := range []bool{false, true} {
				m := f.row(t, f.inbox, uint32(1+i), ingestNow.Add(-time.Hour), "report@example.org")
				req := f.request(m, rep.raw, neverStore)
				req.OnDemand = onDemand
				res, err := Store(ctx, f.st, req, nil)
				if err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(res.RemoteParts, []string{"2", "3", "4"}) || res.RemoteBytes != remote {
					t.Fatalf("on demand %v: result %+v", onDemand, res)
				}
				if !bytes.Equal(res.Whole, rep.raw) || len(res.Attachments) != 4 || res.Attachments[1].Size != int64(len(rep.pdf)) {
					t.Fatalf("on demand %v: whole %d bytes, attachments %+v", onDemand, len(res.Whole), res.Attachments)
				}
				got := f.get(t, m.ID)
				// Nothing more to leave on the server: strippable 0.
				if got.RawState != store.RawPartial || !slices.Equal(got.RemoteParts, res.RemoteParts) ||
					got.StrippableBytes != 0 || !got.HydratedAt.IsZero() || got.BodyState != store.BodyFetched {
					t.Fatalf("on demand %v: row %+v", onDemand, got)
				}
				if !bytes.Equal(f.part(t, m.ID, "1.2"), rep.logo) {
					t.Error("the picture the HTML shows is not stored")
				}
				for _, id := range []string{"2", "3", "4"} {
					if len(f.part(t, m.ID, id)) != 0 {
						t.Errorf("part %s is stored", id)
					}
				}
			}

			// A draft keeps everything, and so does a signed message; the
			// whole message is handed back all the same.
			d := f.row(t, f.drafts, 10, ingestNow.AddDate(-1, 0, 0), "report@example.org")
			req := f.request(d, rep.raw, neverStore)
			req.Role = api.RoleDrafts
			res, err := Store(ctx, f.st, req, nil)
			if err != nil || len(res.RemoteParts) != 0 || !bytes.Equal(res.Whole, rep.raw) {
				t.Fatalf("draft: %+v %v", res, err)
			}
			if got := f.get(t, d.ID); got.RawState != store.RawFull || got.StrippableBytes != remote || !bytes.Equal(f.raw(t, d.ID), rep.raw) {
				t.Fatalf("draft row %+v", got)
			}
			signed := []byte(strings.Join([]string{
				"From: a@example.org", "Subject: signed", "MIME-Version: 1.0",
				`Content-Type: multipart/signed; protocol="application/pgp-signature"; micalg=pgp-sha256; boundary="sig"`,
				"", "--sig", "Content-Type: application/octet-stream", `Content-Disposition: attachment; filename="small.bin"`,
				"", "tiny", "--sig", "Content-Type: application/pgp-signature", "", "sig", "--sig--", "",
			}, "\r\n"))
			sm := f.row(t, f.inbox, 11, ingestNow.AddDate(-1, 0, 0), "")
			if res, err := Store(ctx, f.st, f.request(sm, signed, neverStore), nil); err != nil || len(res.RemoteParts) != 0 {
				t.Fatalf("signed: %+v %v", res, err)
			}
			if got := f.get(t, sm.ID); got.RawState != store.RawFull || got.StrippableBytes != store.StrippableNever || !bytes.Equal(f.raw(t, sm.ID), signed) {
				t.Fatalf("signed row %+v", got)
			}
			// An unparsable body is kept as received, and never reduced.
			bad := f.row(t, f.inbox, 12, ingestNow, "")
			if _, err := Store(ctx, f.st, f.request(bad, []byte{}, neverStore), nil); !errors.Is(err, ErrUnparsable) {
				t.Fatalf("unparsable: %v", err)
			}
			if got := f.get(t, bad.ID); got.StrippableBytes != store.StrippableNever {
				t.Fatalf("unparsable row %+v", got)
			}
		})
	}
}

// Under NeverStore a picture the HTML shows of api.LargeAttachmentMinBytes
// and more stays on the server with the attachments: the stored skeleton
// still has its part, headers and Content-ID, which the HTML keeps
// pointing at, with an empty body; the text and HTML are stored as they
// are. A draft keeps it.
func TestStoreNeverStoreLargePicture(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	rep := newReport("report@example.org", 200<<10)
	m := f.row(t, f.inbox, 1, ingestNow.Add(-time.Hour), "report@example.org")
	res, err := Store(ctx, f.st, f.request(m, rep.raw, neverStore), nil)
	if err != nil {
		t.Fatal(err)
	}
	remote := int64(len(rep.logo) + len(rep.pdf) + len(rep.small) + len(rep.stray))
	if !slices.Equal(res.RemoteParts, []string{"1.2", "2", "3", "4"}) || res.RemoteBytes != remote || !bytes.Equal(res.Whole, rep.raw) {
		t.Fatalf("result: parts %v, %d bytes, whole %d bytes", res.RemoteParts, res.RemoteBytes, len(res.Whole))
	}
	got := f.get(t, m.ID)
	if got.RawState != store.RawPartial || !slices.Equal(got.RemoteParts, res.RemoteParts) || got.StrippableBytes != 0 {
		t.Fatalf("row %+v", got)
	}
	if a := got.Attachments[0]; a.PartID != "1.2" || a.ContentID == "" || a.Size != int64(len(rep.logo)) || !a.Remote {
		t.Errorf("picture as listed: %+v", a)
	}
	if len(f.part(t, m.ID, "1.2")) != 0 {
		t.Error("the large picture is stored")
	}
	skel, err := mime.Parse(bytes.NewReader(f.raw(t, m.ID)), mime.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(skel.RawHTML, `cid:logo@example.org`) || skel.Attachments[0].ContentID != "logo@example.org" {
		t.Errorf("skeleton HTML %q, picture %+v", skel.RawHTML, skel.Attachments[0])
	}

	d := f.row(t, f.drafts, 2, ingestNow.Add(-time.Hour), "report@example.org")
	req := f.request(d, rep.raw, neverStore)
	req.Role = api.RoleDrafts
	if res, err := Store(ctx, f.st, req, nil); err != nil || len(res.RemoteParts) != 0 || !bytes.Equal(f.part(t, d.ID, "1.2"), rep.logo) {
		t.Fatalf("draft: %+v %v", res.RemoteParts, err)
	}
}

// Under the other policies nothing is handed back: the message was staged
// on disk.
func TestStoreHandsBackOnlyUnderNeverStore(t *testing.T) {
	f := newFixture(t)
	rep := newReport("report@example.org", 200<<10)
	m := f.row(t, f.inbox, 1, ingestNow.AddDate(-1, 0, 0), "report@example.org")
	res, err := Store(context.Background(), f.st, f.request(m, rep.raw, smallOnly), nil)
	if err != nil || res.Whole != nil || res.Attachments != nil || len(res.RemoteParts) != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	f.stagingEmpty(t)
}

// Hold receives a stored message into memory, checked as a download is,
// and changes nothing about the stored one.
func TestHold(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	rep := newReport("report@example.org", 200<<10)
	m := f.row(t, f.inbox, 1, ingestNow.AddDate(-1, 0, 0), "report@example.org")
	if _, err := Store(ctx, f.st, f.request(m, rep.raw, smallOnly), nil); err != nil {
		t.Fatal(err)
	}
	m = f.get(t, m.ID)
	before := f.raw(t, m.ID)
	f.blockStaging(t)
	hold := func(body io.Reader, strict bool, limit int64) (Held, error) {
		req := f.request(m, nil, neverStore)
		req.Body, req.Verify, req.Strict, req.Limit = body, &m, strict, limit
		return Hold(ctx, f.st, req, nil)
	}

	held, err := hold(bytes.NewReader(rep.raw), true, 0)
	if err != nil || !bytes.Equal(held.Raw, rep.raw) || len(held.Attachments) != 4 || held.Attachments[1].Size != int64(len(rep.pdf)) {
		t.Fatalf("held %d bytes, %+v, %v", len(held.Raw), held.Attachments, err)
	}
	if _, err := hold(bytes.NewReader(newReport("other@example.org", 200<<10).raw), false, 0); !errors.Is(err, ErrMismatch) {
		t.Errorf("another Message-ID: %v", err)
	}
	resized := newReport("report@example.org", 201<<10).raw
	if _, err := hold(bytes.NewReader(resized), true, 0); !errors.Is(err, ErrMismatch) {
		t.Errorf("other parts under strict: %v", err)
	}
	if _, err := hold(bytes.NewReader(resized), false, 0); err != nil {
		t.Errorf("other parts, lenient: %v", err)
	}
	if _, err := hold(bytes.NewReader(nil), true, 0); !errors.Is(err, ErrUnparsable) {
		t.Errorf("unparsable: %v", err)
	}
	if _, err := hold(bytes.NewReader(rep.raw), true, 1000); !errors.Is(err, ErrTooBig) {
		t.Errorf("over the limit: %v", err)
	}
	broken := io.MultiReader(bytes.NewReader(rep.raw[:1000]), errReader{io.ErrUnexpectedEOF})
	if _, err := hold(broken, true, 0); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("broken download: %v", err)
	}
	req := f.request(m, rep.raw, neverStore)
	if _, err := Hold(ctx, f.st, req, nil); err == nil {
		t.Error("held without a stored message to check against")
	}

	after := f.get(t, m.ID)
	if !bytes.Equal(f.raw(t, m.ID), before) || after.RawState != store.RawPartial || !after.HydratedAt.IsZero() ||
		!after.UpdatedAt.Equal(m.UpdatedAt) || !slices.Equal(after.RemoteParts, m.RemoteParts) {
		t.Fatalf("hold changed the stored message: %+v", after)
	}
}

// Strip under NeverStore reduces a message downloaded on demand within its
// grace, the large picture its HTML shows included, and one whose
// attachments are all small; the skeleton is staged in memory.
func TestStripNeverStore(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	rep := newReport("report@example.org", 200<<10)
	hydrated := f.row(t, f.inbox, 1, ingestNow.AddDate(0, 0, -1), "report@example.org")
	req := f.request(hydrated, rep.raw, smallOnly)
	req.OnDemand = true
	if _, err := Store(ctx, f.st, req, nil); err != nil {
		t.Fatal(err)
	}
	hydrated = f.get(t, hydrated.ID)
	notes := smallAttachment("notes@example.org")
	small := f.row(t, f.inbox, 2, ingestNow.AddDate(-1, 0, 0), "notes@example.org")
	if _, err := Store(ctx, f.st, f.request(small, notes, smallOnly), nil); err != nil {
		t.Fatal(err)
	}
	small = f.get(t, small.ID)
	if hydrated.RawState != store.RawFull || hydrated.HydratedAt.IsZero() || small.RawState != store.RawFull || small.StrippableBytes != 0 {
		t.Fatalf("seeded %+v / %+v", hydrated, small)
	}
	f.blockStaging(t)

	if out, err := Strip(ctx, f.st, hydrated, neverStore, time.Now(), nil); err != nil || out != Reduced {
		t.Fatalf("hydrated: %v %v", out, err)
	}
	if got := f.get(t, hydrated.ID); got.RawState != store.RawPartial || !slices.Equal(got.RemoteParts, []string{"1.2", "2", "3", "4"}) ||
		got.StrippableBytes != 0 {
		t.Fatalf("hydrated row %+v", got)
	}
	if out, err := Strip(ctx, f.st, small, neverStore, time.Now(), nil); err != nil || out != Reduced {
		t.Fatalf("small: %v %v", out, err)
	}
	got := f.get(t, small.ID)
	if got.RawState != store.RawPartial || !slices.Equal(got.RemoteParts, []string{"2"}) || got.StrippableBytes != 0 {
		t.Fatalf("small row %+v", got)
	}
	if text := f.text(t, small.ID); text != "see the notes" {
		t.Errorf("text %q", text)
	}
}

// A message Strip finds it can never reduce is settled as such
// (StrippableNever), under any policy.
func TestStripSettlesNever(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	rep := newReport("report@example.org", 200<<10)
	unterminated := bytes.TrimSuffix(rep.raw, []byte("--mix--\r\n"))
	m := f.row(t, f.inbox, 1, ingestNow.AddDate(-1, 0, 0), "")
	if _, err := Store(ctx, f.st, f.request(m, unterminated, Policy{}), nil); err != nil {
		t.Fatal(err)
	}
	m = f.get(t, m.ID)
	out, err := Strip(ctx, f.st, m, neverStore, ingestNow, nil)
	if err != nil || out != Whole || f.get(t, m.ID).StrippableBytes != store.StrippableNever {
		t.Fatalf("unterminated: %v %v %d", out, err, f.get(t, m.ID).StrippableBytes)
	}
	gone := f.row(t, f.inbox, 2, ingestNow.AddDate(-1, 0, 0), "")
	if _, err := Store(ctx, f.st, f.request(gone, rep.raw, Policy{}), nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.st.MessageRawPath(f.acc.ID, gone.ID)); err != nil {
		t.Fatal(err)
	}
	out, err = Strip(ctx, f.st, f.get(t, gone.ID), neverStore, ingestNow, nil)
	if err != nil || out != Whole || f.get(t, gone.ID).StrippableBytes != store.StrippableNever {
		t.Fatalf("missing file: %v %v", out, err)
	}
}

// Under NeverStore a message the other rule stored partial loses the
// attachments its file still holds too, and the picture its HTML shows
// when that is large: the new skeleton is made from the stored one and
// checked against it, the parts omitted before stay omitted, the row's
// remote set grows by the parts omitted now and the body columns stay as
// they are; with nothing left, the message is settled at 0 and its file
// left alone. Without NeverStore a partial message is not touched.
func TestStripPartialNeverStore(t *testing.T) {
	t.Run("small picture", func(t *testing.T) { testStripPartialNeverStore(t, smallLogo) })
	t.Run("large picture", func(t *testing.T) { testStripPartialNeverStore(t, 150<<10) })
}

func testStripPartialNeverStore(t *testing.T, logoSize int) {
	f := newFixture(t)
	ctx := context.Background()
	rep := newReportLogo("report@example.org", 200<<10, logoSize)
	largeLogo := int64(logoSize) >= api.LargeAttachmentMinBytes
	wantRemote, omittedNow := []string{"2", "3", "4"}, int64(len(rep.small))
	if largeLogo {
		wantRemote, omittedNow = []string{"1.2", "2", "3", "4"}, omittedNow+int64(len(rep.logo))
	}
	m := f.row(t, f.inbox, 1, ingestNow.AddDate(-1, 0, 0), "report@example.org")
	if _, err := Store(ctx, f.st, f.request(m, rep.raw, smallOnly), nil); err != nil {
		t.Fatal(err)
	}
	m = f.get(t, m.ID)
	if m.RawState != store.RawPartial || !slices.Equal(m.RemoteParts, []string{"2", "4"}) || m.StrippableBytes <= 0 ||
		!bytes.Equal(f.part(t, m.ID, "3"), rep.small) {
		t.Fatalf("seeded %+v", m)
	}
	text := f.text(t, m.ID)

	if _, err := Strip(ctx, f.st, m, smallOnly, ingestNow, nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("partial without NeverStore: %v", err)
	}
	f.blockStaging(t)
	if out, err := Strip(ctx, f.st, m, neverStore, ingestNow, nil); err != nil || out != Reduced {
		t.Fatalf("strip: %v %v", out, err)
	}
	got := f.get(t, m.ID)
	if got.RawState != store.RawPartial || !slices.Equal(got.RemoteParts, wantRemote) ||
		got.RemoteBytes != m.RemoteBytes+omittedNow || got.StrippableBytes != 0 ||
		!got.UpdatedAt.Equal(m.UpdatedAt) || got.Snippet != m.Snippet || len(got.Attachments) != len(m.Attachments) {
		t.Fatalf("row %+v", got)
	}
	for i, a := range got.Attachments {
		if b := m.Attachments[i]; a.PartID != b.PartID || a.Filename != b.Filename || a.Size != b.Size ||
			a.Remote != slices.Contains(wantRemote, a.PartID) {
			t.Errorf("attachment %d: %+v, was %+v", i, a, b)
		}
	}
	if f.text(t, m.ID) != text {
		t.Error("the text changed")
	}
	if logo := f.part(t, m.ID, "1.2"); largeLogo != (len(logo) == 0) || (!largeLogo && !bytes.Equal(logo, rep.logo)) {
		t.Errorf("the picture the HTML shows: %d bytes stored", len(logo))
	}
	for _, id := range []string{"2", "3", "4"} {
		if len(f.part(t, m.ID, id)) != 0 {
			t.Errorf("part %s is stored", id)
		}
	}

	// Nothing left to leave on the server: settled, the file as it is.
	file := f.raw(t, m.ID)
	if out, err := Strip(ctx, f.st, got, neverStore, ingestNow, nil); err != nil || out != Whole {
		t.Fatalf("nothing left: %v %v", out, err)
	}
	if again := f.get(t, m.ID); again.StrippableBytes != 0 || !slices.Equal(again.RemoteParts, got.RemoteParts) ||
		!bytes.Equal(f.raw(t, m.ID), file) {
		t.Fatalf("nothing left: row %+v", again)
	}
}

// A partial message the pass cannot reduce further safely (its stored
// skeleton lost its final boundary) or whose file is gone is settled as
// never to be reduced, and left as it is.
func TestStripPartialSettlesNever(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	rep := newReport("report@example.org", 200<<10)
	partial := func(uid uint32) store.Message {
		t.Helper()
		m := f.row(t, f.inbox, uid, ingestNow.AddDate(-1, 0, 0), "report@example.org")
		if _, err := Store(ctx, f.st, f.request(m, rep.raw, smallOnly), nil); err != nil {
			t.Fatal(err)
		}
		return f.get(t, m.ID)
	}

	cut := partial(1)
	skel := bytes.TrimSuffix(f.raw(t, cut.ID), []byte("--mix--\r\n"))
	if err := os.WriteFile(f.st.MessageRawPath(f.acc.ID, cut.ID), skel, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := Strip(ctx, f.st, cut, neverStore, ingestNow, nil)
	if got := f.get(t, cut.ID); err != nil || out != Whole || got.StrippableBytes != store.StrippableNever ||
		!slices.Equal(got.RemoteParts, cut.RemoteParts) || !bytes.Equal(f.raw(t, cut.ID), skel) {
		t.Fatalf("unterminated: %v %v, row %+v", out, err, got)
	}

	gone := partial(2)
	if err := os.Remove(f.st.MessageRawPath(f.acc.ID, gone.ID)); err != nil {
		t.Fatal(err)
	}
	out, err = Strip(ctx, f.st, gone, neverStore, ingestNow, nil)
	if got := f.get(t, gone.ID); err != nil || out != Whole || got.StrippableBytes != store.StrippableNever {
		t.Fatalf("missing file: %v %v, row %+v", out, err, got)
	}
}
