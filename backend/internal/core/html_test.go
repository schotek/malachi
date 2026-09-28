// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"io"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

func TestMessagePart(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	svc := m.b.Messages()

	png, err := svc.Part(ctx, api.MessagePartParams{AccountID: m.acc, MessageID: m.msgs[0], PartID: "1.2"})
	if err != nil {
		t.Fatal(err)
	}
	wantPNG := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if png.PartID != "1.2" || png.ContentType != "image/png" || png.Filename != "logo.png" || png.Size != int64(len(wantPNG)) || !bytes.Equal(png.Data, wantPNG) {
		t.Fatalf("png part = %+v", png)
	}
	pdf, err := svc.Part(ctx, api.MessagePartParams{AccountID: m.acc, MessageID: m.msgs[0], PartID: "2"})
	if err != nil {
		t.Fatal(err)
	}
	if pdf.ContentType != "application/pdf" || pdf.Filename != "a.pdf" || string(pdf.Data) != "%PDF-1.4\n" {
		t.Fatalf("pdf part = %+v", pdf)
	}

	cases := []struct {
		name string
		p    api.MessagePartParams
		code api.ErrorCode
	}{
		{"missing ids", api.MessagePartParams{AccountID: m.acc, MessageID: m.msgs[0]}, api.CodeInvalidArgument},
		{"junk part id", api.MessagePartParams{AccountID: m.acc, MessageID: m.msgs[0], PartID: "../1"}, api.CodeInvalidArgument},
		{"no such part", api.MessagePartParams{AccountID: m.acc, MessageID: m.msgs[0], PartID: "9"}, api.CodePartNotFound},
		{"container", api.MessagePartParams{AccountID: m.acc, MessageID: m.msgs[0], PartID: "1"}, api.CodePartNotFound},
		{"no raw message", api.MessagePartParams{AccountID: m.acc, MessageID: m.msgs[1], PartID: "1"}, api.CodePartNotFound},
		{"unknown message", api.MessagePartParams{AccountID: m.acc, MessageID: "m_nope", PartID: "1"}, api.CodeMessageNotFound},
		{"unknown account", api.MessagePartParams{AccountID: "acc_nope", MessageID: m.msgs[0], PartID: "1"}, api.CodeAccountNotFound},
	}
	for _, c := range cases {
		_, err := svc.Part(ctx, c.p)
		if got := errCode(t, err); got != c.code {
			t.Errorf("%s: code %v, want %v", c.name, got, c.code)
		}
	}
}

// A part kept on the server answers partNotDownloaded whatever the stored
// file holds, even without a file; the others, and the HTML with the
// picture it shows, are served from the reduced message.
func TestMessagePartRemote(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	svc := m.b.Messages()
	msg := m.seedLarge(t, 7, time.Now().AddDate(-1, 0, 0), smallOnly)

	for _, id := range []string{"3", "4"} {
		if _, err := m.part(msg.ID, id); errCode(t, err) != api.CodePartNotDownloaded {
			t.Errorf("part %s: %v", id, err)
		}
	}
	if notes, err := m.part(msg.ID, "2"); err != nil || string(notes.Data) != "short notes" {
		t.Fatalf("kept part: %v", err)
	}
	body, err := svc.Body(ctx, api.MessageBodyParams{AccountID: m.acc, MessageID: api.MessageID(msg.ID)})
	if err != nil || body.HTMLWithheld || body.InlineParts["logo@example.org"] != "1.2" {
		t.Fatalf("body %+v, %v", body, err)
	}
	if logo, err := m.part(msg.ID, "1.2"); err != nil || !bytes.HasPrefix(logo.Data, []byte("\x89PNG")) {
		t.Fatalf("the picture the HTML shows: %v", err)
	}

	if err := os.Remove(m.b.store.MessageRawPath(string(m.acc), msg.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.part(msg.ID, "3"); errCode(t, err) != api.CodePartNotDownloaded {
		t.Errorf("remote part without a file: %v", err)
	}
	if _, err := m.part(msg.ID, "2"); errCode(t, err) != api.CodePartNotFound {
		t.Errorf("kept part without a file: %v", err)
	}
}

// seedLostParts stores largeMessage whole, then puts its skeleton in place
// of the file behind the row's back, as a power loss after a reduced file
// reached the disk and before the row's commits did would leave it: the
// row calls parts 3 and 4 stored, the file has them empty.
func (m *mailbox) seedLostParts(t *testing.T, uid uint32) store.Message {
	t.Helper()
	msg := m.seedLarge(t, uid, time.Now().AddDate(-1, 0, 0), ingest.Policy{})
	if msg.RawState != store.RawFull || len(msg.RemoteParts) != 0 {
		t.Fatalf("seeded %+v", msg)
	}
	var skel bytes.Buffer
	if _, err := mime.Skeleton(bytes.NewReader(largeMessage("report@example.org")), &skel,
		map[string]bool{"3": true, "4": true}, mime.DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.b.store.PutMessageRaw(context.Background(), string(m.acc), msg.ID, store.RawWrite{}, func(w io.Writer) error {
		_, err := w.Write(skel.Bytes())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return m.row(t, msg.ID)
}

// A file its row does not describe (seedLostParts) never passes an empty
// part off as the attachment: message.part and message.embedded answer
// partNotDownloaded, the row learns that the parts are on the server, and
// message.download makes the message whole again.
func TestMessagePartLostParts(t *testing.T) {
	m := seedMailbox(t)
	srv := m.fakeServer()
	ctx := context.Background()
	msg := m.seedLostParts(t, 7)

	if notes, err := m.part(msg.ID, "2"); err != nil || string(notes.Data) != "short notes" {
		t.Fatalf("kept part: %v", err)
	}
	if _, err := m.part(msg.ID, "3"); errCode(t, err) != api.CodePartNotDownloaded {
		t.Fatalf("lost part: %v", err)
	}
	row := m.row(t, msg.ID)
	if row.RawState != store.RawPartial || !slices.Equal(row.RemoteParts, []string{"3"}) ||
		row.RemoteBytes != int64(len(bigPDF)) || !row.Attachments[2].Remote || row.Attachments[3].Remote {
		t.Fatalf("row after the lost part %+v", row)
	}
	if _, err := m.b.Messages().Embedded(ctx, api.MessageEmbeddedParams{AccountID: m.acc, MessageID: api.MessageID(msg.ID),
		PartID: "4"}); errCode(t, err) != api.CodePartNotDownloaded {
		t.Fatalf("lost attached message: %v", err)
	}
	if row := m.row(t, msg.ID); !slices.Equal(row.RemoteParts, []string{"3", "4"}) || !row.Attachments[3].Remote {
		t.Fatalf("row after the lost attached message %+v", row)
	}

	srv.put("INBOX", 7, largeMessage("report@example.org"))
	if _, err := m.download(ctx, msg.ID); err != nil {
		t.Fatal(err)
	}
	if pdf, err := m.part(msg.ID, "3"); err != nil || !bytes.Equal(pdf.Data, bigPDF) {
		t.Fatalf("part after the download: %v", err)
	}
	if row := m.row(t, msg.ID); row.RawState != store.RawFull || len(row.RemoteParts) != 0 {
		t.Fatalf("row after the download %+v", row)
	}
}
