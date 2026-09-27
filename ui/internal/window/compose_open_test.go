// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/client"
)

// The source of a reply is the summary until the full message and the
// body are loaded; a body that is not fetched contributes no text.
func TestComposeSource(t *testing.T) {
	date := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	s := api.MessageSummary{ID: "m_1", From: []api.Address{{Address: "a@example.invalid"}}, To: []api.Address{{Address: "me@example.invalid"}}, Subject: "s", Date: date}

	src := composeSource("m_1", s, nil)
	if src.ID != "m_1" || len(src.From) != 1 || len(src.To) != 1 || src.Subject != "s" || !src.Date.Equal(date) || src.Text != "" || src.ReplyTo != nil {
		t.Errorf("from summary: %+v", src)
	}

	full := &api.Message{MessageSummary: s}
	full.From = []api.Address{{Name: "A", Address: "a@example.invalid"}}
	full.ReplyTo = []api.Address{{Address: "r@example.invalid"}}
	full.CC = []api.Address{{Address: "c@example.invalid"}}
	full.Subject = "full"
	lm := &loadedMessage{msg: full, body: &api.MessageBodyResult{BodyState: api.BodyPending, Text: "not yet"}}
	src = composeSource("m_1", s, lm)
	if src.From[0].Name != "A" || len(src.ReplyTo) != 1 || len(src.CC) != 1 || src.Subject != "full" || src.Text != "" {
		t.Errorf("from headers: %+v", src)
	}
	lm.body = &api.MessageBodyResult{BodyState: api.BodyFetched, Text: "hello"}
	if src = composeSource("m_1", s, lm); src.Text != "hello" {
		t.Errorf("from body: %+v", src)
	}
}

// A forward downloads first when the daemon would otherwise leave
// something of the original behind, or when the cache cannot tell
// (message.download answers at once when nothing is missing).
func TestForwardNeedsDownload(t *testing.T) {
	fetched := &api.MessageBodyResult{BodyState: api.BodyFetched}
	msg := func(atts ...api.Attachment) *api.Message { return &api.Message{Attachments: atts} }
	local := api.Attachment{PartID: "2", Filename: "a.pdf"}
	remote := api.Attachment{PartID: "3", Filename: "b.pdf", Remote: true}
	cases := []struct {
		name string
		lm   *loadedMessage
		want bool
	}{
		{"nothing loaded", nil, true},
		{"no full message yet", &loadedMessage{body: &api.MessageBodyResult{BodyState: api.BodyPending}}, true},
		{"message.get failed", &loadedMessage{body: fetched}, true},
		{"all local", &loadedMessage{msg: msg(local), body: fetched}, false},
		{"one on the server", &loadedMessage{msg: msg(local, remote), body: fetched}, true},
		{"on the server, body not loaded here", &loadedMessage{msg: msg(remote)}, true},
		{"body not downloaded yet", &loadedMessage{msg: msg(), body: &api.MessageBodyResult{BodyState: api.BodyPending}}, true},
		{"an inline picture is never left behind", &loadedMessage{msg: msg(api.Attachment{PartID: "1.2", Inline: true, Remote: true}), body: fetched}, false},
		{"too big to download anyway", &loadedMessage{msg: msg(local), body: &api.MessageBodyResult{BodyState: api.BodyTooBig}}, false},
	}
	for _, c := range cases {
		if got := forwardNeedsDownload(c.lm); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

// A failed download asks, unless asking would change nothing: no daemon,
// one that cannot download at all, or a message it can never download.
func TestAskForwardWithout(t *testing.T) {
	if askForwardWithout(nil) {
		t.Error("no error, no question")
	}
	for _, err := range []error{
		api.NewError(api.CodeMethodNotFound, "x"), api.ErrNotImplemented,
		client.ErrDisconnected, fmt.Errorf("call: %w", client.ErrDisconnected),
		api.NewError(api.CodeAttachmentTooBig, "over the cap"),
	} {
		if askForwardWithout(err) {
			t.Errorf("%v: should forward at once", err)
		}
	}
	for _, err := range []error{
		api.NewError(api.CodeOffline, "x"), api.NewError(api.CodeMessageGone, "x"),
		api.NewError(api.CodeUnavailable, "x"), api.NewError(api.CodeServerTimeout, "x"),
		api.NewError(api.CodeCancelled, "x"), context.DeadlineExceeded, errors.New("boom"),
	} {
		if !askForwardWithout(err) {
			t.Errorf("%v: should ask", err)
		}
	}
}

// No toast when there is no backend to ask or it lacks the call; a
// sentence for everything else.
func TestComposeFallbackText(t *testing.T) {
	if got := composeFallbackText("Preparing the reply", client.ErrDisconnected); got != "" {
		t.Errorf("disconnected: %q", got)
	}
	if got := composeFallbackText("Preparing the reply", api.ErrNotImplemented); got != "" {
		t.Errorf("not implemented: %q", got)
	}
	for _, err := range []error{context.DeadlineExceeded, api.NewError(api.CodeMessageNotFound, "gone"), errors.New("boom")} {
		if got := composeFallbackText("Preparing the reply", err); got == "" {
			t.Errorf("%v: no toast", err)
		}
	}
}
