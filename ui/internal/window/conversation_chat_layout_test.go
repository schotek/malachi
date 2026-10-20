// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// The chat layout's rules that need no GTK (conversation_chat_layout.go),
// ported one to one with the macOS client's ConversationChatBodyTests
// (ConversationChatTests.swift) and ConversationLayoutTests
// `chatBubbleKeepsAnEmptySide`: the width of a bubble, the short dates,
// the end of the chat, a change of the viewport's size, which variant of a
// body a bubble reads, its text in every state, its monospace font and its
// paperclip.

func convNear(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// ConversationLayoutTests.chatBubbleKeepsAnEmptySide (D4): a row of the
// chat is the avatar's gutter, the bubble and an empty side of at least 24
// or a fifth of the column.
func TestChatBubbleKeepsAnEmptySide(t *testing.T) {
	if convChatGapMin != 24 || convChatGapRatio != 0.2 {
		t.Fatalf("gap %v, ratio %v", convChatGapMin, convChatGapRatio)
	}
	cases := []struct{ column, gap, bubble float64 }{
		{868, 173.6, 654.4}, // the clamp's 900 less the insets
		{300, 60, 200},
		{100, 24, 36},
		{50, 24, 0}, // never negative
		{0, 24, 0},
	}
	for _, c := range cases {
		if g := convChatGap(c.column); !convNear(g, c.gap) {
			t.Errorf("gap at %v = %v, want %v", c.column, g, c.gap)
		}
		if b := convChatBubbleMax(c.column); !convNear(b, c.bubble) {
			t.Errorf("bubble at %v = %v, want %v", c.column, b, c.bubble)
		}
	}
	// The widest column: the pane's clamp less its two insets.
	widest := float64(convMaxWidth - 2*convSideInset)
	if b := convChatBubbleMax(widest); !convNear(b, widest-convAvatar-convGutterGap-0.2*widest) {
		t.Errorf("widest bubble %v", b)
	}

	// Continuous where the floor meets the share (a column of 120): no
	// jump either side.
	edge := convChatGapMin / convChatGapRatio
	if !convNear(edge, 120) || !convNear(convChatGap(edge), 24) {
		t.Fatalf("edge %v, gap %v", edge, convChatGap(edge))
	}
	for _, d := range []float64{0.001, 0.01, 0.1} {
		if math.Abs(convChatGap(edge-d)-24) > d || math.Abs(convChatGap(edge+d)-24) > d {
			t.Errorf("gap jumps at %v", d)
		}
		if math.Abs(convChatBubbleMax(edge+d)-convChatBubbleMax(edge-d)) > 2*d {
			t.Errorf("bubble jumps at %v", d)
		}
	}
	if !convNear(convChatBubbleMax(edge), 120-40-24) {
		t.Errorf("bubble at the edge %v", convChatBubbleMax(edge))
	}
	// A wider column never gives a narrower bubble.
	last := -1.0
	for c := 0.0; c <= 1000; c += 0.5 {
		b := convChatBubbleMax(c)
		if b < last {
			t.Fatalf("bubble narrower at %v: %v < %v", c, b, last)
		}
		last = b
	}
}

// ConversationChatBodyTests.chatEnd.
func TestChatEnd(t *testing.T) {
	if !convChatAtEnd(600, 400, 1000, convChatEndSlack) || !convChatAtEnd(597, 400, 1000, convChatEndSlack) {
		t.Error("at the end (within the slack) is not the end")
	}
	if convChatAtEnd(590, 400, 1000, convChatEndSlack) {
		t.Error("10 above the end counts as the end")
	}
	if !convChatAtEnd(0, 400, 300, convChatEndSlack) {
		t.Error("a chat shorter than the view is not at its end")
	}
	if got := convChatEndTop(400, 1000); got != 600 {
		t.Errorf("end top %v", got)
	}
	if got := convChatEndTop(400, 300); got != 0 {
		t.Errorf("end top of a short chat %v", got)
	}
	for _, c := range []struct{ pane, column float64 }{{1200, 868}, {500, 468}, {20, 0}} {
		if got := convChatColumn(c.pane); got != c.column {
			t.Errorf("column of %v = %v, want %v", c.pane, got, c.column)
		}
	}
}

// ConversationChatBodyTests.chatCompactDates.
func TestChatCompactDates(t *testing.T) {
	// The bubble's widest is column - 40 - max(24, column / 5).
	if wide := convChatColumn(1200); convChatCompactDates(wide) { // 868: bubble 654.4
		t.Error("a wide column shows the short date")
	}
	// 600: bubble 440, less 28 of padding: 412 < 420.
	if !convChatCompactDates(600) {
		t.Error("600 shows the full date")
	}
	// The cards of the same pane (632) still show the full date.
	if convCompactDates(632) {
		t.Error("the cards of 632 show the short date")
	}
	// The boundary: 0.8 * c - 40 - 28 == 420 at c = 610.
	if convChatCompactDates(610) || !convChatCompactDates(609) || !convChatCompactDates(0) {
		t.Error("the boundary is not at 610")
	}
}

// ConversationChatBodyTests.chatResize.
func TestChatResize(t *testing.T) {
	if _, ok := convChatResize(convSize{500, 400}, convSize{500.2, 400.3}, true, true); ok {
		t.Error("less than half a pixel is a change")
	}
	cases := []struct {
		to           convSize
		pinned, end  bool
		width, toEnd bool
	}{
		// Only the height: no reflow, the end stays in view when it was.
		{convSize{500, 300}, false, true, false, true},
		{convSize{500, 300}, false, false, false, false},
		{convSize{500, 500}, true, false, false, true},
		// The width: a reflow.
		{convSize{700, 400}, false, false, true, false},
	}
	for _, c := range cases {
		got, ok := convChatResize(convSize{500, 400}, c.to, c.pinned, c.end)
		if !ok || got != (convChatResizing{Width: c.width, ToEnd: c.toEnd}) {
			t.Errorf("to %v (pinned %v, at end %v) = %+v, %v", c.to, c.pinned, c.end, got, ok)
		}
	}
}

// chatTestBody is a fetched plain-text body (MessageBodyResult as
// ConversationChatBodyTests' body()).
func chatTestBody(text string, state api.BodyState, trimmed bool) *api.MessageBodyResult {
	return &api.MessageBodyResult{MessageID: "m1", BodyState: state, Text: text, QuotedTrimmed: trimmed}
}

// ConversationChatBodyTests.chatBodyReadsTheTrimmedVariant.
func TestChatBodyReadsTheTrimmedVariant(t *testing.T) {
	// Trimmed and shown: the body.
	lm := &loadedMessage{}
	lm.store(chatTestBody("Hi", api.BodyFetched, true), false, false)
	if b := chatBody(lm); b == nil || b.Text != "Hi" {
		t.Fatalf("trimmed body %+v", b)
	}

	// The user revealed the quoted history: the trimmed variant kept
	// aside, not the whole body shown.
	lm.showQuoted(true)
	lm.store(chatTestBody("Hi\n> quoted", api.BodyFetched, false), true, false)
	if !lm.quotedShown {
		t.Fatal("not revealed")
	}
	if b := chatBody(lm); b == nil || b.Text != "Hi" {
		t.Errorf("revealed: %+v", b)
	}
	if lm.body.Text != "Hi\n> quoted" {
		t.Errorf("the whole body is not the one shown: %q", lm.body.Text)
	}

	// Revealed before the trimmed variant was ever fetched: the whole
	// body is all there is.
	whole := &loadedMessage{}
	whole.showQuoted(true)
	whole.store(chatTestBody("Hi\n> quoted", api.BodyFetched, false), true, false)
	if b := chatBody(whole); b == nil || b.Text != "Hi\n> quoted" {
		t.Errorf("whole: %+v", b)
	}

	// Nothing yet.
	if chatBody(&loadedMessage{}) != nil || chatBody(nil) != nil {
		t.Error("a body without one")
	}
}

// ConversationChatBodyTests.bubbleBodyStates.
func TestBubbleBodyStates(t *testing.T) {
	snippet := "  See the\r\nfigures  "
	check := func(name string, lm *loadedMessage, want string, wantPlaceholder bool) {
		t.Helper()
		got, placeholder := bubbleBody(lm, snippet)
		if got != want || placeholder != wantPlaceholder {
			t.Errorf("%s: %q (placeholder %v), want %q (placeholder %v)", name, got, placeholder, want, wantPlaceholder)
		}
	}
	check("nil", nil, "See the\nfigures", true)
	check("empty entry", &loadedMessage{}, "See the\nfigures", true)

	lm := &loadedMessage{}
	lm.store(chatTestBody("Hello\r\n\r\n\r\nBye\r\n", api.BodyFetched, false), false, false)
	check("text", lm, "Hello\n\nBye", false)

	lm.store(chatTestBody(" \n\t ", api.BodyFetched, false), false, false)
	check("blank", lm, i18n.T("(Empty message)"), false)
	lm.store(chatTestBody("", api.BodyPending, false), false, false)
	check("pending", lm, i18n.T("Downloading…"), false)
	lm.store(chatTestBody("", api.BodyTooBig, false), false, false)
	check("too big", lm, i18n.T("This message is too large to download."), false)
	lm.store(chatTestBody("", api.BodyFailed, false), false, false)
	check("failed", lm, i18n.T("This message could not be read."), false)

	// The call failed: the cards' sentence.
	timeout := errors.New("message.body: timeout")
	failed := &loadedMessage{err: timeout}
	check("error", failed, loadingMessageError(timeout), false)

	// The whole body failed after a reveal: the trimmed variant kept
	// aside still reads.
	aside := &loadedMessage{}
	aside.store(chatTestBody("Short", api.BodyFetched, false), false, false)
	aside.showQuoted(true)
	aside.err = timeout
	check("aside", aside, "Short", false)
}

// ConversationChatBodyTests.bubbleAttachmentBadge.
func TestBubbleAttachmentBadge(t *testing.T) {
	summary := func(has bool) api.MessageSummary {
		return api.MessageSummary{ID: "m1", AccountID: "a1", FolderID: "f1", HasAttachments: has}
	}
	file := func(part, cid string) api.Attachment {
		return api.Attachment{PartID: part, Filename: part + ".pdf", ContentType: "application/pdf", Size: 1000, Inline: cid != "", ContentID: cid}
	}
	some := bubbleAttachments{Shown: true}
	none := bubbleAttachments{}
	// Nothing known but the summary: the pill alone, or none.
	if got := bubbleAttachmentBadge(summary(true), nil); got != some {
		t.Errorf("summary only: %+v", got)
	}
	if got := bubbleAttachmentBadge(summary(false), nil); got != none {
		t.Errorf("no attachments: %+v", got)
	}
	// The body alone (message.get not answered, or failed): still the
	// summary's word.
	bodyOnly := &loadedMessage{}
	bodyOnly.store(chatTestBody("Hi", api.BodyFetched, false), false, false)
	if got := bubbleAttachmentBadge(summary(true), bodyOnly); got != some {
		t.Errorf("body only, attachments: %+v", got)
	}
	if got := bubbleAttachmentBadge(summary(false), bodyOnly); got != none {
		t.Errorf("body only, none: %+v", got)
	}

	// message.get answered: the number of the timeline's chips.
	two := &loadedMessage{}
	two.store(chatTestBody("Hi", api.BodyFetched, false), false, false)
	two.msg = &api.Message{MessageSummary: summary(true), Attachments: []api.Attachment{file("2", ""), file("3", "")}}
	if got := bubbleAttachmentBadge(summary(true), two); got != (bubbleAttachments{Shown: true, Count: 2}) {
		t.Errorf("two: %+v", got)
	}
	if got := bubbleAttachmentBadge(summary(true), two); got.Count != len(chipAttachments(two.msg.Attachments, two.body)) {
		t.Errorf("not the chips' number: %+v", got)
	}

	// A part the daemon calls inline (a Content-ID) leaves the summary's
	// flag false, and the timeline still shows its chip (a plain-text body
	// shows no part itself): so does the bubble.
	inline := &loadedMessage{}
	inline.store(chatTestBody("See the attached", api.BodyFetched, false), false, false)
	inline.msg = &api.Message{MessageSummary: summary(false), Attachments: []api.Attachment{file("2", "scan@x")}}
	if got := bubbleAttachmentBadge(summary(false), inline); got != (bubbleAttachments{Shown: true, Count: 1}) {
		t.Errorf("inline: %+v", got)
	}

	// An image the HTML body shows through cid: no chip, no pill.
	shown := &loadedMessage{}
	shown.store(&api.MessageBodyResult{
		MessageID: "m1", BodyState: api.BodyFetched, HasHTML: true, HTML: `<img src="cid:logo@x">`,
		InlineParts: map[string]string{"logo@x": "2"},
	}, false, false)
	shown.msg = &api.Message{MessageSummary: summary(false), Attachments: []api.Attachment{file("2", "logo@x")}}
	if got := bubbleAttachmentBadge(summary(true), shown); got != none {
		t.Errorf("shown by the HTML: %+v", got)
	}
}

// ConversationChatBodyTests.bubbleMonospace.
func TestBubbleMonospace(t *testing.T) {
	body := func(state api.BodyState, html bool) *api.MessageBodyResult {
		return &api.MessageBodyResult{MessageID: "m1", BodyState: state, HasHTML: html, Text: "x"}
	}
	plain := &loadedMessage{}
	plain.store(body(api.BodyFetched, false), false, false)
	if !bubbleMonospace(plain, true) || bubbleMonospace(plain, false) {
		t.Error("plain text does not follow the setting")
	}
	html := &loadedMessage{}
	html.store(body(api.BodyFetched, true), false, false)
	if bubbleMonospace(html, true) {
		t.Error("text derived from HTML is monospaced")
	}
	pending := &loadedMessage{}
	pending.store(body(api.BodyPending, false), false, false)
	if bubbleMonospace(pending, true) {
		t.Error("a body on its way is monospaced")
	}
	if bubbleMonospace(nil, true) || bubbleMonospace(&loadedMessage{}, true) {
		t.Error("the placeholder is monospaced")
	}
	if bubbleMonospace(&loadedMessage{err: errors.New("timeout")}, true) {
		t.Error("an error is monospaced")
	}
}

// The GTK client's own small rules of the chat's texts: the date of a
// header in full or short, the second line of an event, the accessible
// name of a bubble.
func TestChatTexts(t *testing.T) {
	if text, tip := chatDateText("1 Sep 2026 10:00", "10:00", false); text != "1 Sep 2026 10:00" || tip != "" {
		t.Errorf("full: %q, %q", text, tip)
	}
	if text, tip := chatDateText("1 Sep 2026 10:00", "10:00", true); text != "10:00" || tip != "1 Sep 2026 10:00" {
		t.Errorf("compact: %q, %q", text, tip)
	}
	if text, tip := chatDateText("", "", true); text != "" || tip != "" {
		t.Errorf("no date: %q, %q", text, tip)
	}
	for _, c := range []struct{ sender, when, want string }{
		{"Ann", "10:00", "Ann · 10:00"}, {"", "10:00", "10:00"}, {"Ann", "", "Ann"}, {"", "", ""},
	} {
		if got := chatEventMeta(c.sender, c.when); got != c.want {
			t.Errorf("meta(%q, %q) = %q", c.sender, c.when, got)
		}
	}
	if got := chatAccessibleName("Ann", "", "1 Sep"); got != "Ann, 1 Sep" {
		t.Errorf("name %q", got)
	}
	if got := chatAccessibleName("", ""); got != "" {
		t.Errorf("empty name %q", got)
	}
}

// bubbleHTML: the HTML body of the variant the chat reads, only with the
// preference on and only for a fetched body with HTML that is not
// withheld; everything else reads as text.
func TestBubbleHTML(t *testing.T) {
	html := func(state api.BodyState, doc string, withheld bool) *loadedMessage {
		lm := &loadedMessage{}
		lm.store(&api.MessageBodyResult{MessageID: "m1", BodyState: state, Text: "t", HTML: doc, HasHTML: true, HTMLWithheld: withheld, QuotedTrimmed: true}, false, false)
		return lm
	}
	if b := bubbleHTML(html(api.BodyFetched, "<p>x</p>", false), true); b == nil || b.HTML != "<p>x</p>" {
		t.Errorf("html: %+v", b)
	}
	cases := map[string]struct {
		lm      *loadedMessage
		setting bool
	}{
		"off":         {html(api.BodyFetched, "<p>x</p>", false), false},
		"nil":         {nil, true},
		"not loaded":  {&loadedMessage{}, true},
		"withheld":    {html(api.BodyFetched, "", true), true},
		"text only":   {html(api.BodyFetched, "", false), true},
		"pending":     {html(api.BodyPending, "<p>x</p>", false), true},
		"failed body": {html(api.BodyFailed, "<p>x</p>", false), true},
		"error":       {&loadedMessage{err: errors.New("timeout")}, true},
	}
	for name, c := range cases {
		if b := bubbleHTML(c.lm, c.setting); b != nil {
			t.Errorf("%s: html %+v", name, b)
		}
	}

	// Revealed in the timeline: the trimmed variant kept aside is the
	// document, as for the text.
	lm := html(api.BodyFetched, "<p>short</p>", false)
	lm.showQuoted(true)
	lm.store(&api.MessageBodyResult{MessageID: "m1", BodyState: api.BodyFetched, HTML: "<p>short</p><blockquote>old</blockquote>", HasHTML: true}, true, false)
	if b := bubbleHTML(lm, true); b == nil || b.HTML != "<p>short</p>" {
		t.Errorf("revealed: %+v", b)
	}
}

// chatLiveRows, the board chat's rule: the near HTML rows within the
// limit, measured from the end (the newest of rows equally near first);
// text rows and far rows get no view.
func TestChatLiveRowsBoardCap(t *testing.T) {
	frames := convStacked(8, 100)
	html := []bool{true, true, true, true, true, false, true, true}
	live := chatLiveRows(frames, html, convSpanOf(0, 800), board.ConversationMaxLiveWebViews)
	var got []int
	for i := range frames {
		if live.Web[i] {
			got = append(got, i)
		}
	}
	if want := []int{3, 4, 6, 7}; !reflect.DeepEqual(got, want) {
		t.Errorf("views %v, want %v", got, want)
	}
	// A short viewport at the top: the rows near it only.
	live = chatLiveRows(frames, html, convSpanOf(0, 100), board.ConversationMaxLiveWebViews)
	got = nil
	for i := range frames {
		if live.Web[i] {
			got = append(got, i)
		}
		if live.Near[i] != (i <= 3) {
			t.Errorf("row %d near %v", i, live.Near[i])
		}
	}
	if want := []int{0, 1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Errorf("views at the top %v, want %v", got, want)
	}
	// Nothing is HTML (the preference off): no view.
	live = chatLiveRows(frames, nil, convSpanOf(0, 800), board.ConversationMaxLiveWebViews)
	if len(convSet(live.Web)) != 0 {
		t.Errorf("views without HTML: %v", convSet(live.Web))
	}
}
