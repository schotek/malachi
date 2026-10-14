// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/internal/sanitize"
	"github.com/schotek/malachi/backend/pkg/api"
)

// bodyTrim is message.body for the mailbox's account, trimmed or not.
func (m *mailbox) bodyTrim(t *testing.T, id api.MessageID, trim bool) *api.MessageBodyResult {
	t.Helper()
	res, err := m.b.Messages().Body(context.Background(), api.MessageBodyParams{AccountID: m.acc, MessageID: id, TrimQuoted: trim})
	if err != nil {
		t.Fatalf("message.body: %v", err)
	}
	return res
}

// message.body trimQuoted: the quoted history goes, and with it everything
// the result derives from the body; without the parameter the result is
// what it always was.
func TestMessageBodyTrimQuoted(t *testing.T) {
	m := seedMailbox(t)

	t.Run("outlook desktop, czech", func(t *testing.T) {
		id := m.seedQuoted(t, "quote-outlook-desktop-cs.eml")
		whole := m.bodyTrim(t, id, false)
		cut := m.bodyTrim(t, id, true)
		if whole.QuotedTrimmed || !cut.QuotedTrimmed {
			t.Fatalf("quotedTrimmed: whole %v, cut %v", whole.QuotedTrimmed, cut.QuotedTrimmed)
		}
		if !strings.Contains(whole.HTML, "Odesláno") || strings.Contains(cut.HTML, "Odesláno") || strings.Contains(cut.HTML, "posílám fakturu") ||
			!strings.Contains(cut.HTML, "fakturu jsem zaplatil") {
			t.Fatalf("trimmed html %q", cut.HTML)
		}
		// The links, the blocked banner and the pictures of the quote are gone.
		if len(whole.Links) != 2 || len(cut.Links) != 0 {
			t.Errorf("links: whole %+v, cut %+v", whole.Links, cut.Links)
		}
		if whole.Blocked.RemoteImages != 1 || cut.Blocked.RemoteImages != 0 {
			t.Errorf("blocked: whole %+v, cut %+v", whole.Blocked, cut.Blocked)
		}
		if len(whole.InlineParts) != 2 || len(cut.InlineParts) != 1 || cut.InlineParts["image001.png@01DC0000.00000000"] == "" {
			t.Errorf("inlineParts: whole %v, cut %v", whole.InlineParts, cut.InlineParts)
		}
		// Outlook's text alternative has the header block without a
		// separator line, which the text rules cut too.
		if strings.Contains(cut.Text, "Odesláno") || strings.Contains(cut.Text, "posílám") || !strings.Contains(cut.Text, "fakturu jsem zaplatil") {
			t.Errorf("text %q", cut.Text)
		}
		if !strings.Contains(whole.Text, "Odesláno") {
			t.Errorf("whole text %q", whole.Text)
		}
	})

	t.Run("gmail, text alternative cut by the text rules", func(t *testing.T) {
		id := m.seedQuoted(t, "quote-gmail.eml")
		cut := m.bodyTrim(t, id, true)
		if !cut.QuotedTrimmed || cut.Text != "Sounds good, see you at noon." || strings.Contains(cut.HTML, "Lunch") {
			t.Fatalf("trimmed = %+v", cut)
		}
	})

	t.Run("outlook on the web, text alternative after underscores", func(t *testing.T) {
		id := m.seedQuoted(t, "quote-owa.eml")
		cut := m.bodyTrim(t, id, true)
		if !cut.QuotedTrimmed || cut.Text != "Yes, Thursday works. Calendar<https://calendar.example.org/t>" ||
			len(cut.Links) != 1 || cut.Blocked != (api.BlockedContent{}) {
			t.Fatalf("trimmed = %+v", cut)
		}
		if whole := m.bodyTrim(t, id, false); whole.Blocked.TrackingPixels != 1 || len(whole.Links) != 2 {
			t.Fatalf("whole = %+v", whole)
		}
	})

	t.Run("plain text", func(t *testing.T) {
		id := m.seedQuoted(t, "quote-plain-text-cs.eml")
		whole := m.bodyTrim(t, id, false)
		cut := m.bodyTrim(t, id, true)
		if whole.HasHTML || whole.QuotedTrimmed || !strings.Contains(whole.Text, "Původní zpráva") {
			t.Fatalf("whole = %+v", whole)
		}
		if !cut.QuotedTrimmed || cut.Text != "Dobrý den,\n\nsmlouvu podepíšu zítra.\n\nKarel" {
			t.Fatalf("trimmed text %q (%v)", cut.Text, cut.QuotedTrimmed)
		}
	})

	t.Run("nothing to trim", func(t *testing.T) {
		for _, name := range []string{"quote-interleaved.eml", "quote-forward-only.eml", "html-only.eml"} {
			id := m.seedQuoted(t, name)
			whole := m.bodyTrim(t, id, false)
			cut := m.bodyTrim(t, id, true)
			if !reflect.DeepEqual(whole, cut) {
				t.Errorf("%s: trimQuoted changed an untrimmable body:\n got %+v\nwant %+v", name, cut, whole)
			}
		}
	})

	t.Run("withheld html, text trimmed", func(t *testing.T) {
		id := m.seedQuoted(t, "quote-gmail.eml")
		m.b.Sanitize = func(sanitize.Input) (sanitize.Output, error) {
			return sanitize.Output{Version: sanitize.Version}, api.NewError(api.CodeSanitizeFailed, "refused")
		}
		defer func() { m.b.Sanitize = sanitize.Sanitize }()
		cut := m.bodyTrim(t, id, true)
		if !cut.HTMLWithheld || cut.HTML != "" || !cut.QuotedTrimmed || cut.Text != "Sounds good, see you at noon." {
			t.Fatalf("withheld = %+v", cut)
		}
		whole := m.bodyTrim(t, id, false)
		if whole.QuotedTrimmed || !strings.Contains(whole.Text, "Lunch tomorrow?") {
			t.Fatalf("withheld whole = %+v", whole)
		}
	})

	t.Run("wire names", func(t *testing.T) {
		id := m.seedQuoted(t, "quote-apple-mail.eml")
		whole, err := json.Marshal(m.bodyTrim(t, id, false))
		if err != nil {
			t.Fatal(err)
		}
		cut, err := json.Marshal(m.bodyTrim(t, id, true))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(whole), "quotedTrimmed") || !strings.Contains(string(cut), `"quotedTrimmed":true`) {
			t.Fatalf("whole %s\ncut %s", whole, cut)
		}
		p, _ := json.Marshal(api.MessageBodyParams{AccountID: "a", MessageID: "m"})
		q, _ := json.Marshal(api.MessageBodyParams{AccountID: "a", MessageID: "m", TrimQuoted: true})
		if strings.Contains(string(p), "trimQuoted") || !strings.Contains(string(q), `"trimQuoted":true`) {
			t.Fatalf("params %s / %s", p, q)
		}
	})
}
