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

// The pictures bar counts only what an HTML body on display misses, and
// shows the wait from the click until the body is back, whatever the body
// says meanwhile; it stands beside the remote-image bar, not instead of it.
func TestPicturesBarState(t *testing.T) {
	html := &api.MessageBodyResult{BodyState: api.BodyFetched, HTML: "<p>x</p>", RemotePictures: 2}
	withBlocked := *html
	withBlocked.RemoteContent = api.RemoteBlock
	withBlocked.Blocked = api.BlockedContent{RemoteImages: 3}
	cases := []struct {
		name string
		lm   *loadedMessage
		want picturesBarState
	}{
		{"nothing loaded", nil, picturesBarState{}},
		{"body on its way", &loadedMessage{}, picturesBarState{}},
		{"two on the server", &loadedMessage{body: html}, picturesBarState{visible: true, remote: 2}},
		{"with remote images blocked too", &loadedMessage{body: &withBlocked}, picturesBarState{visible: true, remote: 2}},
		{"downloading", &loadedMessage{body: html, loadingPictures: true}, picturesBarState{visible: true, loading: true}},
		{"remote images loading instead", &loadedMessage{body: html, loadingImages: true}, picturesBarState{visible: true, remote: 2}},
		{"all in", &loadedMessage{body: &api.MessageBodyResult{BodyState: api.BodyFetched, HTML: "<p>x</p>"}}, picturesBarState{}},
		{"text only", &loadedMessage{body: &api.MessageBodyResult{BodyState: api.BodyFetched, Text: "x", RemotePictures: 2}}, picturesBarState{}},
		{"html withheld", &loadedMessage{body: &api.MessageBodyResult{BodyState: api.BodyFetched, HTMLWithheld: true, RemotePictures: 2}}, picturesBarState{}},
		{"not fetched", &loadedMessage{body: &api.MessageBodyResult{BodyState: api.BodyPending, HTML: "<p>x</p>", RemotePictures: 2}}, picturesBarState{}},
		{"nonsense count", &loadedMessage{body: &api.MessageBodyResult{BodyState: api.BodyFetched, HTML: "<p>x</p>", RemotePictures: -1}}, picturesBarState{}},
	}
	for _, c := range cases {
		if got := picturesBarStateFor(c.lm); got != c.want {
			t.Errorf("%s: picturesBarStateFor = %+v, want %+v", c.name, got, c.want)
		}
	}
	// The remote-image bar is unaffected by the pictures.
	if got := remoteBarStateFor(&loadedMessage{body: &withBlocked, loadingPictures: true}); got != (remoteBarState{visible: true, blocked: 3}) {
		t.Errorf("remote bar while pictures download: %+v", got)
	}
}

// The body asked for after the pictures keeps the remote images the user
// loaded, or is loading, and otherwise leaves the policy to the daemon.
func TestPicturesPolicy(t *testing.T) {
	body := &api.MessageBodyResult{BodyState: api.BodyFetched, HTML: "<p>x</p>", RemotePictures: 1}
	cases := []struct {
		name string
		lm   *loadedMessage
		want api.RemoteContentPolicy
	}{
		{"no body", &loadedMessage{}, ""},
		{"blocked", &loadedMessage{body: withPolicy(body, api.RemoteBlock)}, ""},
		{"loaded", &loadedMessage{body: withPolicy(body, api.RemoteAllow)}, api.RemoteAllow},
		{"being loaded", &loadedMessage{body: withPolicy(body, api.RemoteBlock), loadingImages: true}, api.RemoteAllow},
	}
	for _, c := range cases {
		if got := picturesPolicy(c.lm); got != c.want {
			t.Errorf("%s: picturesPolicy = %q, want %q", c.name, got, c.want)
		}
	}
}

// A picture the daemon no longer serves asks for the body again only when
// the cached body lists it and counts none on the server, nothing newer is
// on its way, and it has not asked since the last download.
func TestRecheckPictures(t *testing.T) {
	body := &api.MessageBodyResult{BodyState: api.BodyFetched, HTML: "<p>x</p>", InlineParts: map[string]string{"p@x": "1.2", "q@x": "1.3"}}
	counted := *body
	counted.RemotePictures = 1
	text := &api.MessageBodyResult{BodyState: api.BodyFetched, Text: "x", InlineParts: body.InlineParts}
	cases := []struct {
		name string
		lm   *loadedMessage
		part string
		want bool
	}{
		{"not cached", nil, "1.2", false},
		{"no body", &loadedMessage{}, "1.2", false},
		{"listed, none counted", &loadedMessage{body: body}, "1.2", true},
		{"another listed one", &loadedMessage{body: body}, "1.3", true},
		{"not a picture of the body", &loadedMessage{body: body}, "2", false},
		{"asked already", &loadedMessage{body: body, picturesRechecked: true}, "1.2", false},
		{"the bar is up already", &loadedMessage{body: &counted}, "1.2", false},
		{"body on its way", &loadedMessage{body: body, fetching: true}, "1.2", false},
		{"remote images on their way", &loadedMessage{body: body, loadingImages: true}, "1.2", false},
		{"pictures on their way", &loadedMessage{body: body, loadingPictures: true}, "1.2", false},
		{"text shown", &loadedMessage{body: text}, "1.2", false},
	}
	for _, c := range cases {
		if got := recheckPictures(c.lm, c.part); got != c.want {
			t.Errorf("%s: recheckPictures = %v", c.name, got)
		}
	}
}

// A download asks for the body again when it counts pictures on the
// server, unless Download Pictures does that itself or a body is on its
// way.
func TestReloadAfterDownload(t *testing.T) {
	counted := &api.MessageBodyResult{BodyState: api.BodyFetched, HTML: "<p>x</p>", RemotePictures: 2}
	none := &api.MessageBodyResult{BodyState: api.BodyFetched, HTML: "<p>x</p>"}
	cases := []struct {
		name string
		lm   *loadedMessage
		want bool
	}{
		{"not cached", nil, false},
		{"no body", &loadedMessage{}, false},
		{"pictures on the server", &loadedMessage{body: counted}, true},
		{"remote images on their way", &loadedMessage{body: counted, loadingImages: true}, true},
		{"none on the server", &loadedMessage{body: none}, false},
		{"Download Pictures asks itself", &loadedMessage{body: counted, loadingPictures: true}, false},
		{"body on its way", &loadedMessage{body: counted, fetching: true}, false},
		{"text shown", &loadedMessage{body: &api.MessageBodyResult{BodyState: api.BodyFetched, Text: "x", RemotePictures: 2}}, false},
	}
	for _, c := range cases {
		if got := reloadAfterDownload(c.lm); got != c.want {
			t.Errorf("%s: reloadAfterDownload = %v", c.name, got)
		}
	}
}

// withPolicy copies b with the applied remote-content policy set.
func withPolicy(b *api.MessageBodyResult, p api.RemoteContentPolicy) *api.MessageBodyResult {
	c := *b
	c.RemoteContent = p
	return &c
}
