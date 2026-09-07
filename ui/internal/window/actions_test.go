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

func TestLoadableImages(t *testing.T) {
	html := &api.MessageBodyResult{HTML: "<p>x</p>", Blocked: api.BlockedContent{RemoteImages: 3}}
	cases := []struct {
		name string
		body *api.MessageBodyResult
		want int
	}{
		{"nil body", nil, 0},
		{"blocked html", withPolicy(html, api.RemoteBlock), 3},
		// Already allowed: the daemon fetched what it could, and what the
		// counter still holds (CSS url(), srcset, plain http:, a failed
		// download) no button can bring back.
		{"allowed html", withPolicy(html, api.RemoteAllow), 0},
		{"text only", withPolicy(&api.MessageBodyResult{Blocked: api.BlockedContent{RemoteImages: 2}}, api.RemoteBlock), 0},
		{"nothing blocked", withPolicy(&api.MessageBodyResult{HTML: "<p>x</p>"}, api.RemoteBlock), 0},
	}
	for _, c := range cases {
		if got := loadableImages(c.body); got != c.want {
			t.Errorf("%s: loadableImages = %d, want %d", c.name, got, c.want)
		}
	}
}

// The bar shows the wait from the click until the daemon answers, whatever
// the body says meanwhile (issue #1: on a slow connection the button used
// to sit there for twenty seconds as if the click had been lost).
func TestRemoteBarState(t *testing.T) {
	blocked := withPolicy(&api.MessageBodyResult{HTML: "<p>x</p>", Blocked: api.BlockedContent{RemoteImages: 2}}, api.RemoteBlock)
	allowed := withPolicy(blocked, api.RemoteAllow)
	cases := []struct {
		name string
		lm   *loadedMessage
		want remoteBarState
	}{
		{"nothing loaded", nil, remoteBarState{}},
		{"body on its way", &loadedMessage{}, remoteBarState{}},
		{"blocked", &loadedMessage{body: blocked}, remoteBarState{visible: true, blocked: 2}},
		{"loading", &loadedMessage{body: blocked, loadingImages: true}, remoteBarState{visible: true, loading: true}},
		{"loading before the body", &loadedMessage{loadingImages: true}, remoteBarState{visible: true, loading: true}},
		{"images in", &loadedMessage{body: allowed}, remoteBarState{}},
	}
	for _, c := range cases {
		if got := remoteBarStateFor(c.lm); got != c.want {
			t.Errorf("%s: remoteBarStateFor = %+v, want %+v", c.name, got, c.want)
		}
	}
}

// withPolicy copies b with the applied remote-content policy set.
func withPolicy(b *api.MessageBodyResult, p api.RemoteContentPolicy) *api.MessageBodyResult {
	c := *b
	c.RemoteContent = p
	return &c
}
