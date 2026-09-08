// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"errors"
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
