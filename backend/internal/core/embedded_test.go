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

	"github.com/schotek/malachi/backend/internal/remoteimg"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// seedRaw adds a message to the inbox whose raw form is the testdata/mime
// sample and returns its id. Only the raw file matters to message.embedded
// and message.part; the row exists so the message can be looked up.
func (m *mailbox) seedRaw(t *testing.T, name string) api.MessageID {
	t.Helper()
	ctx := context.Background()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "mime", name))
	if err != nil {
		t.Fatal(err)
	}
	row := &store.Message{AccountID: string(m.acc), FolderID: string(m.inbox), UID: uint32(100 + len(m.msgs)),
		Subject: name, Date: time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC),
		From: []api.Address{{Address: "alice@example.org"}}, Size: int64(len(raw))}
	if err := m.b.store.UpsertMessages(ctx, []*store.Message{row}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.b.store.WriteMessageRaw(ctx, string(m.acc), row.ID, bytes.NewReader(raw), 25<<20); err != nil {
		t.Fatal(err)
	}
	m.msgs = append(m.msgs, api.MessageID(row.ID))
	return api.MessageID(row.ID)
}

// message.embedded renders an attached message from the part's bytes: the
// headers as message.get would, the body as message.body would, with the
// pictures inlined and the attachments named but unaddressable.
func TestMessageEmbedded(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	svc := m.b.Messages()
	id := m.seedRaw(t, "attached-html-message.eml")

	res, err := svc.Embedded(ctx, api.MessageEmbeddedParams{AccountID: m.acc, MessageID: id, PartID: "2"})
	if err != nil {
		t.Fatal(err)
	}
	msg, body := res.Message, res.Body
	if res.PartID != "2" || msg.ID != id || msg.AccountID != m.acc || msg.FolderID != m.inbox {
		t.Fatalf("addressed as %+v", msg.MessageSummary)
	}
	if len(msg.From) != 1 || msg.From[0].Address != "carol@example.net" || msg.From[0].Name != "Carol" ||
		msg.Subject != "report" || len(msg.CC) != 1 || msg.RFCMessageID != "report@example.net" ||
		msg.Date.Year() != 2025 || !msg.HasAttachments || msg.Flags == nil || len(msg.Flags) != 0 || msg.References == nil {
		t.Fatalf("headers = %+v", msg)
	}
	if body.MessageID != id || body.BodyState != api.BodyFetched || !body.HasHTML || body.HTMLWithheld ||
		body.RemoteContent != api.RemoteBlock || body.Links == nil {
		t.Fatalf("body = %+v", body)
	}
	// The real picture goes in as data:, the part that lies about its type,
	// the SVG and the dangling reference fall away, the remote image is
	// blocked like anywhere else.
	if strings.Count(body.HTML, `src="data:image/png;base64,iVBORw0KGgo="`) != 1 {
		t.Errorf("html = %q, want the png inlined once", body.HTML)
	}
	if strings.Contains(body.HTML, "cid:") || strings.Contains(body.HTML, "remote.invalid") || strings.Contains(body.HTML, "<script") {
		t.Errorf("html = %q: a cid:, remote or script reference survived", body.HTML)
	}
	if body.Blocked.DangerousURLs != 3 || body.Blocked.RemoteImages != 1 || body.Blocked.Scripts != 0 {
		t.Errorf("blocked = %+v, want 3 dangerous urls and 1 remote image", body.Blocked)
	}
	if len(body.InlineParts) != 0 {
		t.Errorf("inlineParts = %v, want none: the pictures are inlined", body.InlineParts)
	}
	if !strings.Contains(body.Text, "Inner") {
		t.Errorf("text = %q", body.Text)
	}
	if len(body.Links) != 1 || body.Links[0].Href != "https://example.invalid/r" {
		t.Errorf("links = %+v", body.Links)
	}
	// The shown picture is not listed again; the rest is, by name only, and
	// the .eml inside stays a named attachment.
	var names []string
	for _, a := range msg.Attachments {
		names = append(names, a.Filename)
		if a.PartID != "" {
			t.Errorf("attachment %q carries part id %q", a.Filename, a.PartID)
		}
	}
	if want := "fake.png logo.svg inner.pdf deep.eml"; strings.Join(names, " ") != want {
		t.Errorf("attachments = %v, want %s", names, want)
	}

	// The containing message's senders decide about remote images; under
	// allow the daemon fetches them and both pictures end up inlined.
	var asked []string
	m.b.FetchRemoteImages = func(_ context.Context, urls []string) map[string]remoteimg.Image {
		asked = append(asked, urls...)
		return map[string]remoteimg.Image{"https://remote.invalid/x.png": {MediaType: "image/png", Data: []byte("PNG")}}
	}
	allow, err := svc.Embedded(ctx, api.MessageEmbeddedParams{AccountID: m.acc, MessageID: id, PartID: "2", RemoteContent: api.RemoteAllow})
	if err != nil {
		t.Fatal(err)
	}
	if allow.Body.RemoteContent != api.RemoteAllow || allow.Body.Blocked.RemoteImages != 0 ||
		strings.Count(allow.Body.HTML, "data:image/png;base64,") != 2 || strings.Contains(allow.Body.HTML, "remote.invalid") {
		t.Errorf("allow body = %+v", allow.Body)
	}
	if len(asked) != 1 || asked[0] != "https://remote.invalid/x.png" {
		t.Errorf("fetcher asked for %v", asked)
	}

	// A part that is only named .eml (a message saved to a file by some
	// client) counts as an attached message too.
	fwd, err := svc.Embedded(ctx, api.MessageEmbeddedParams{AccountID: m.acc, MessageID: id, PartID: "4"})
	if err != nil {
		t.Fatal(err)
	}
	if fwd.Message.Subject != "plain" || fwd.Message.From[0].Address != "frank@example.org" || fwd.Message.HasAttachments ||
		len(fwd.Message.Attachments) != 0 || fwd.Body.HasHTML || !strings.Contains(fwd.Body.Text, "Just text") {
		t.Errorf("forwarded.eml = %+v / %+v", fwd.Message, fwd.Body)
	}

	cases := []struct {
		name string
		p    api.MessageEmbeddedParams
		code api.ErrorCode
	}{
		{"missing ids", api.MessageEmbeddedParams{AccountID: m.acc, MessageID: id}, api.CodeInvalidArgument},
		{"junk part id", api.MessageEmbeddedParams{AccountID: m.acc, MessageID: id, PartID: "../2"}, api.CodeInvalidArgument},
		{"knownSenders per call", api.MessageEmbeddedParams{AccountID: m.acc, MessageID: id, PartID: "2", RemoteContent: api.RemoteKnownSenders}, api.CodeInvalidArgument},
		{"not an attached message", api.MessageEmbeddedParams{AccountID: m.acc, MessageID: id, PartID: "3"}, api.CodeInvalidArgument},
		{"a container", api.MessageEmbeddedParams{AccountID: m.acc, MessageID: m.msgs[0], PartID: "1"}, api.CodePartNotFound},
		{"no such part", api.MessageEmbeddedParams{AccountID: m.acc, MessageID: id, PartID: "9"}, api.CodePartNotFound},
		{"no raw message", api.MessageEmbeddedParams{AccountID: m.acc, MessageID: m.msgs[1], PartID: "1"}, api.CodePartNotFound},
		{"unknown message", api.MessageEmbeddedParams{AccountID: m.acc, MessageID: "m_nope", PartID: "2"}, api.CodeMessageNotFound},
		{"unknown account", api.MessageEmbeddedParams{AccountID: "acc_nope", MessageID: id, PartID: "2"}, api.CodeAccountNotFound},
	}
	for _, c := range cases {
		_, err := svc.Embedded(ctx, c.p)
		if got := errCode(t, err); got != c.code {
			t.Errorf("%s: code %v, want %v", c.name, got, c.code)
		}
	}
}

// The hostile shapes: a file named .eml that is no message, and the
// message/rfc822 bomb. Neither may hang or panic; each ends in a result or
// malformedMessage.
func TestMessageEmbeddedHostile(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	svc := m.b.Messages()

	id := m.seedRaw(t, "attached-html-message.eml")
	junk, err := svc.Embedded(ctx, api.MessageEmbeddedParams{AccountID: m.acc, MessageID: id, PartID: "5"})
	if err != nil {
		if errCode(t, err) != api.CodeMalformedMessage {
			t.Errorf("junk.eml: %v", err)
		}
	} else if junk.Body.HasHTML || junk.Body.HTML != "" || len(junk.Message.Attachments) != 0 {
		t.Errorf("junk.eml = %+v / %+v", junk.Message, junk.Body)
	}

	bomb := m.seedRaw(t, "rfc822-bomb.eml")
	done := make(chan struct{})
	go func() {
		defer close(done)
		res, err := svc.Embedded(ctx, api.MessageEmbeddedParams{AccountID: m.acc, MessageID: bomb, PartID: "1"})
		if err != nil {
			if errCode(t, err) != api.CodeMalformedMessage {
				t.Errorf("bomb: %v", err)
			}
			return
		}
		if res.Body.HasHTML || res.Body.HTML != "" {
			t.Errorf("bomb rendered html: %+v", res.Body)
		}
		for _, a := range res.Message.Attachments {
			if a.PartID != "" {
				t.Errorf("bomb attachment carries part id %q", a.PartID)
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the bomb did not finish")
	}
}
