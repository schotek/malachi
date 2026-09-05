// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"errors"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The pure helpers behind the message pane and the actions. i18n is not
// bound in tests, so the English msgids come back verbatim.

func TestSubjectText(t *testing.T) {
	if got := subjectText("  Hello  "); got != "Hello" {
		t.Errorf("subjectText: got %q", got)
	}
	if got := subjectText(" \t"); got != "(No subject)" {
		t.Errorf("empty subject: got %q", got)
	}
}

func TestRecipientsText(t *testing.T) {
	to := []api.Address{{Name: "A", Address: "a@example.invalid"}, {Address: "b@example.invalid"}}
	cc := []api.Address{{Name: "C, Inc", Address: "c@example.invalid"}}
	got := recipientsText(to, cc)
	want := "To: A <a@example.invalid>, b@example.invalid\nCc: \"C, Inc\" <c@example.invalid>"
	if got != want {
		t.Errorf("recipientsText:\n got %q\nwant %q", got, want)
	}
	if got := recipientsText(nil, nil); got != "" {
		t.Errorf("no recipients: got %q", got)
	}
	if got := recipientsText(nil, cc); !strings.HasPrefix(got, "Cc: ") || strings.Contains(got, "\n") {
		t.Errorf("cc only: got %q", got)
	}
}

func TestAttachmentNames(t *testing.T) {
	atts := []api.Attachment{
		{Filename: " report.pdf "},
		{ContentType: "image/png"},
		{}, // nameless and typeless: counted, not named
	}
	got := attachmentNames(atts)
	if len(got) != 2 || got[0] != "report.pdf" || got[1] != "image/png" {
		t.Errorf("attachmentNames: got %q", got)
	}
}

func TestAttachmentsCaption(t *testing.T) {
	tests := []struct {
		n     int
		names []string
		want  string
	}{
		{0, nil, ""},
		{1, []string{"a.txt"}, "1 attachment: a.txt"},
		{2, []string{"a.txt", "b.png"}, "2 attachments: a.txt, b.png"},
		{3, nil, "3 attachments"},
		{1, nil, "1 attachment"},
	}
	for _, tc := range tests {
		if got := attachmentsCaption(tc.n, tc.names); got != tc.want {
			t.Errorf("attachmentsCaption(%d, %q) = %q, want %q", tc.n, tc.names, got, tc.want)
		}
	}
}

func TestBodyText(t *testing.T) {
	tests := []struct {
		name string
		b    *api.MessageBodyResult
		want string
	}{
		{"nil", nil, "(Empty message)"},
		{"pending", &api.MessageBodyResult{BodyState: api.BodyPending}, "Downloading…"},
		{"tooBig", &api.MessageBodyResult{BodyState: api.BodyTooBig, Text: "ignored"}, "This message is too large to download."},
		{"failed", &api.MessageBodyResult{BodyState: api.BodyFailed}, "This message could not be read."},
		{"fetched", &api.MessageBodyResult{BodyState: api.BodyFetched, Text: "Hi\n\n"}, "Hi"},
		{"empty", &api.MessageBodyResult{BodyState: api.BodyFetched, Text: " \n"}, "(Empty message)"},
		{"tags stay text", &api.MessageBodyResult{BodyState: api.BodyFetched, Text: "<b>x</b>"}, "<b>x</b>"},
	}
	for _, tc := range tests {
		if got := bodyText(tc.b); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestFlagChange(t *testing.T) {
	set, clear := flagChange(api.FlagSeen, true)
	if len(set) != 1 || set[0] != api.FlagSeen || clear != nil {
		t.Errorf("on: set %v clear %v", set, clear)
	}
	set, clear = flagChange(api.FlagFlagged, false)
	if set != nil || len(clear) != 1 || clear[0] != api.FlagFlagged {
		t.Errorf("off: set %v clear %v", set, clear)
	}
}

func TestPruneLoaded(t *testing.T) {
	m := map[api.MessageID]*loadedMessage{}
	for i, id := range []api.MessageID{"a", "b", "c", "d"} {
		m[id] = &loadedMessage{seq: uint64(i + 1)}
	}
	pruneLoaded(m, 2, maxLoadedBytes)
	if len(m) != 2 || m["c"] == nil || m["d"] == nil {
		t.Errorf("pruneLoaded kept %v", m)
	}
	pruneLoaded(m, 2, maxLoadedBytes) // no-op at the limit
	if len(m) != 2 {
		t.Errorf("pruneLoaded at limit: %d entries", len(m))
	}

	// Bodies count too: big HTML evicts older entries before the count
	// limit, but the newest entry always stays, however big.
	big := func(seq uint64, n int) *loadedMessage {
		return &loadedMessage{seq: seq, body: &api.MessageBodyResult{HTML: strings.Repeat("x", n)}}
	}
	m = map[api.MessageID]*loadedMessage{"a": big(1, 600), "b": big(2, 600), "c": big(3, 600)}
	pruneLoaded(m, 10, 1000)
	if len(m) != 1 || m["c"] == nil {
		t.Errorf("byte cap kept %v, want only the newest", m)
	}
	m = map[api.MessageID]*loadedMessage{"a": big(1, 5000)}
	pruneLoaded(m, 10, 1000)
	if len(m) != 1 {
		t.Errorf("the newest entry must survive the byte cap: %v", m)
	}
}

func TestLoadedMessageState(t *testing.T) {
	lm := &loadedMessage{}
	if lm.complete() || lm.bodySettled() {
		t.Error("empty entry reported progress")
	}
	lm.err = errors.New("boom")
	if !lm.bodySettled() || lm.complete() {
		t.Error("body error must settle the body without completing the entry")
	}
	lm.err, lm.body, lm.msg = nil, &api.MessageBodyResult{}, &api.Message{}
	if !lm.complete() {
		t.Error("both halves present but not complete")
	}
}
