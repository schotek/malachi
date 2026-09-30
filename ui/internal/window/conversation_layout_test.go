// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"math"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/conversation"
)

// The arithmetic of the conversation view (conversation_layout.go), ported
// with the macOS client's ConversationLayoutTests: which cards are near the
// viewport and which of them get a web view, where the viewport goes to
// keep an item in place and for a page of Space, the timeline beside the
// cards, and how a card's web view follows its document's height.

// convIdentity is the translator of the tests: every msgid is its own
// translation (the singular for n == 1).
type convIdentity struct{}

func (convIdentity) T(msgid string) string { return msgid }

func (convIdentity) N(msgid, plural string, n int) string {
	if n == 1 {
		return msgid
	}
	return plural
}

func (convIdentity) C(_, msgid string) string { return msgid }

// convStacked is n items of height each, stacked from 0.
func convStacked(n int, height float64) []convSpan {
	out := make([]convSpan, n)
	for i := range out {
		out[i] = convSpanOf(float64(i)*height, float64(i+1)*height)
	}
	return out
}

// convSet is the keys of m, sorted.
func convSet(m map[int]bool) []int {
	var out []int
	for k, v := range m {
		if v {
			out = append(out, k)
		}
	}
	sort.Ints(out)
	return out
}

func convRange(from, to int) []int {
	var out []int
	for i := from; i <= to; i++ {
		out = append(out, i)
	}
	return out
}

func TestConvSpanDistance(t *testing.T) {
	s := convSpanOf(100, 200)
	for _, c := range []struct {
		o    convSpan
		want float64
	}{
		{convSpanOf(150, 400), 0},
		{convSpanOf(200, 300), 0}, // touching counts as overlapping
		{convSpanOf(250, 300), 50},
		{convSpanOf(0, 40), 60},
	} {
		if got := s.distance(c.o); got != c.want {
			t.Errorf("distance to %v: %v, want %v", c.o, got, c.want)
		}
	}
	if got := convSpanOf(10, 5); got.Max != 10 {
		t.Errorf("an inverted span is empty at its top: %v", got)
	}
}

func TestLiveWindowIsTwoScreensEachWay(t *testing.T) {
	// 40 items of 100 in a viewport of 300 at 1000..1300: near is 400..1900
	// (two screens each way), items 3 to 19 (touching counts).
	frames := convStacked(40, 100)
	live := liveCards(frames, make([]bool, 40), convSpanOf(1000, 1300), convLiveScreens, convMaxLiveWebViews)
	if got := convSet(live.Near); !reflect.DeepEqual(got, convRange(3, 19)) {
		t.Errorf("near: %v", got)
	}
	if len(live.Web) != 0 {
		t.Errorf("no HTML, no web view: %v", convSet(live.Web))
	}
	// At the top.
	top := liveCards(frames, nil, convSpanOf(0, 300), convLiveScreens, convMaxLiveWebViews)
	if got := convSet(top.Near); !reflect.DeepEqual(got, convRange(0, 9)) {
		t.Errorf("near at the top: %v", got)
	}
	// Screens as asked.
	tight := liveCards(frames, nil, convSpanOf(1000, 1300), 0, convMaxLiveWebViews)
	if got := convSet(tight.Near); !reflect.DeepEqual(got, convRange(9, 13)) {
		t.Errorf("near without screens: %v", got)
	}
}

func TestWebViewsGoToTheNearestHTMLCardsUpToTheCap(t *testing.T) {
	frames := convStacked(40, 100)
	html := make([]bool, 40)
	for i := range html {
		html[i] = true
	}
	live := liveCards(frames, html, convSpanOf(1000, 1300), convLiveScreens, convMaxLiveWebViews)
	if len(live.Web) != convMaxLiveWebViews {
		t.Fatalf("web views: %v", convSet(live.Web))
	}
	// The visible ones (9–13, touching counts) first, then the nearest by
	// distance, the earlier one on a tie.
	if got := convSet(live.Web); !reflect.DeepEqual(got, []int{7, 8, 9, 10, 11, 12, 13, 14}) {
		t.Errorf("web: %v", got)
	}
	for i := range live.Web {
		if !live.Near[i] {
			t.Errorf("web view %d is not near", i)
		}
	}

	// Only HTML cards; a cap of two.
	mixed := make([]bool, 40)
	mixed[2], mixed[11], mixed[30] = true, true, true
	few := liveCards(frames, mixed, convSpanOf(1000, 1300), convLiveScreens, 2)
	if got := convSet(few.Web); !reflect.DeepEqual(got, []int{11}) {
		t.Errorf("items 2 and 30 are not near: %v", got)
	}
	if none := liveCards(frames, mixed, convSpanOf(1000, 1300), convLiveScreens, 0); len(none.Web) != 0 {
		t.Errorf("a cap of none: %v", convSet(none.Web))
	}
	if empty := liveCards(nil, nil, convSpanOf(0, 300), convLiveScreens, convMaxLiveWebViews); len(empty.Near)+len(empty.Web) != 0 {
		t.Errorf("no items: %+v", empty)
	}
}

func TestAnchoredTopKeepsTheItemAndStaysInTheDocument(t *testing.T) {
	for _, c := range []struct {
		itemTop, offset, doc, viewport, want float64
	}{
		// The item that was 30 below the viewport's top now starts at 500.
		{500, 30, 2000, 400, 530},
		// Clamped to the end and to the start.
		{1900, 0, 2000, 400, 1600},
		{10, -50, 2000, 400, 0},
		// A document shorter than the viewport stays at the top.
		{200, 0, 300, 400, 0},
	} {
		if got := anchoredTop(c.itemTop, c.offset, c.doc, c.viewport); got != c.want {
			t.Errorf("anchoredTop(%v, %v, %v, %v) = %v, want %v", c.itemTop, c.offset, c.doc, c.viewport, got, c.want)
		}
	}
}

func TestPageTopStepsAViewportLessTheOverlap(t *testing.T) {
	for _, c := range []struct {
		from    float64
		up      bool
		overlap float64
		want    float64
	}{
		{0, false, 40, 360},
		{360, true, 40, 0},
		{1500, false, 40, 1600},
		{100, true, 40, 0},
		// An overlap as large as the viewport still moves half a page.
		{0, false, 400, 200},
	} {
		if got := pageTop(c.from, c.up, 400, 2000, c.overlap); got != c.want {
			t.Errorf("pageTop(%v, up %v, overlap %v) = %v, want %v", c.from, c.up, c.overlap, got, c.want)
		}
	}
}

// The timeline.

// railT0 is 2026-09-01T09:00:00Z.
var railT0 = time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

func railMember(id string, min int, from string, issue *api.MessageIssue) api.MessageSummary {
	if from == "" {
		from = "jana@acme.example"
	}
	return api.MessageSummary{
		ID: api.MessageID(id), AccountID: "a1", FolderID: "f1", ThreadID: "t1",
		From: []api.Address{{Name: "Jana Dvořáková", Address: from}}, Subject: "Quarterly report",
		Date: railT0.Add(time.Duration(min) * time.Minute), Flags: []api.Flag{api.FlagSeen}, Issue: issue,
	}
}

var railAccount = api.Account{ID: "a1", Enabled: true, Config: api.AccountConfig{Name: "Work", Email: "petr@acme.example"}}

var railIssue = api.IssueInfo{
	Key: "WEB-12", URL: "https://acme.atlassian.net/browse/WEB-12", Summary: "Footer overlaps the form",
	Status: "To Do", StatusCategory: api.StatusCategoryTodo,
}

func railThread(count int) api.ThreadSummary {
	return api.ThreadSummary{ID: "t1", AccountID: "a1", Subject: "Quarterly report", MessageCount: count, Latest: railMember("latest", 0, "", nil)}
}

func TestConvRailsMessagesHaveAvatarsEventsAndTheOlderRowDots(t *testing.T) {
	status := &api.MessageIssue{IssueInfo: railIssue, Item: api.IssueItemEvent,
		Changes: []api.IssueChange{{Field: api.IssueFieldStatus, From: "To Do", To: "Done"}}, Mine: true}
	m := conversation.Build(railThread(7), []api.MessageSummary{
		railMember("d", 0, "", &api.MessageIssue{IssueInfo: railIssue, Item: api.IssueItemDescription}),
		railMember("c1", 10, "", &api.MessageIssue{IssueInfo: railIssue, Item: api.IssueItemComment, Mine: true}),
		railMember("e1", 20, "", status),
		railMember("c2", 30, "", &api.MessageIssue{IssueInfo: railIssue, Item: api.IssueItemComment, Via: "Issue Sync", Mine: true}),
	}, railAccount, convIdentity{})
	var kinds []conversation.ItemKind
	for _, it := range m.Items {
		kinds = append(kinds, it.Kind)
	}
	if want := []conversation.ItemKind{conversation.ItemTruncated, conversation.ItemMessage, conversation.ItemMessage, conversation.ItemEvent, conversation.ItemMessage}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("kinds: %v", kinds)
	}
	want := []convRail{
		// The line starts at the first mark and ends at the last.
		{Marker: markDot, Below: true},
		{Marker: markAvatar, Above: true, Below: true},
		{Marker: markAvatar, Accent: true, Above: true, Below: true},
		// The user's own change: a dot is never tinted.
		{Marker: markDot, Above: true, Below: true},
		// A relayed comment is never the user's.
		{Marker: markAvatar, Above: true},
	}
	if got := convRails(m.Items); !reflect.DeepEqual(got, want) {
		t.Errorf("rails:\n got %+v\nwant %+v", got, want)
	}
}

func TestConvRailsOwnMailIsTinted(t *testing.T) {
	members := []api.MessageSummary{railMember("m1", 0, "", nil), railMember("m2", 10, "Petr@acme.example", nil), railMember("m3", 20, "", nil)}
	m := conversation.Build(railThread(3), members, railAccount, convIdentity{})
	want := []convRail{
		{Marker: markAvatar, Below: true},
		{Marker: markAvatar, Accent: true, Above: true, Below: true},
		{Marker: markAvatar, Above: true},
	}
	if got := convRails(m.Items); !reflect.DeepEqual(got, want) {
		t.Errorf("rails:\n got %+v\nwant %+v", got, want)
	}
	// Without the account's address nothing is.
	plain := conversation.Build(railThread(3), members[:2], api.Account{ID: "a1"}, convIdentity{})
	for i, r := range convRails(plain.Items) {
		if r.Accent {
			t.Errorf("item %d tinted without an address", i)
		}
	}
	if plain.Items[0].Kind != conversation.ItemTruncated {
		t.Errorf("the third member is left out: %v", plain.Items[0].Kind)
	}
}

func TestConvRailsOneItemHasNoLine(t *testing.T) {
	only := conversation.Item{Kind: conversation.ItemMessage, Message: railMember("m1", 0, "", nil), Mine: true}
	if got := convRails([]conversation.Item{only}); !reflect.DeepEqual(got, []convRail{{Marker: markAvatar, Accent: true}}) {
		t.Errorf("one item: %+v", got)
	}
	if got := convRails(nil); len(got) != 0 {
		t.Errorf("no items: %+v", got)
	}
	two := convRails([]conversation.Item{only, {Kind: conversation.ItemEvent, Message: railMember("e1", 1, "", nil)}})
	if want := []convRail{{Marker: markAvatar, Accent: true, Below: true}, {Marker: markDot, Above: true}}; !reflect.DeepEqual(two, want) {
		t.Errorf("two items: %+v", two)
	}
}

func TestConvColumnKeepsToTheClampWithTheGutterInIt(t *testing.T) {
	beside := float64(2*convSideInset + convAvatar + convGutterGap)
	for _, c := range []struct{ pane, want float64 }{
		{600, 600 - beside},
		{convMaxWidth, convMaxWidth - beside},
		{1600, convMaxWidth - beside}, // the gutter is inside the 900
		{40, 0},
		{0, 0},
	} {
		if got := convCardWidth(c.pane); got != c.want {
			t.Errorf("convCardWidth(%v) = %v, want %v", c.pane, got, c.want)
		}
	}
	// The short date while the header is narrower than the threshold.
	edge := float64(convCompactHeader+2*convCardPaddingH) + beside
	if !convCompactDates(edge - 1) {
		t.Error("one below the edge: compact")
	}
	if convCompactDates(edge) {
		t.Error("at the edge: full dates")
	}
	if !convCompactDates(300) {
		t.Error("the pane at its narrowest: compact")
	}
	if convCompactDates(2000) {
		t.Error("a wide pane: full dates")
	}
}

// A card's web view height.

func mustReport(t *testing.T, g *webHeightGovernor, css float64, viewport bool, want float64) {
	t.Helper()
	got, ok := g.report(css, viewport)
	if !ok || got != want {
		t.Errorf("report(%v, viewport %v) = %v, %v; want %v", css, viewport, got, ok, want)
	}
}

func mustKeep(t *testing.T, g *webHeightGovernor, css float64, viewport bool) {
	t.Helper()
	if got, ok := g.report(css, viewport); ok {
		t.Errorf("report(%v, viewport %v) = %v; want the height kept", css, viewport, got)
	}
}

func TestWebHeightFollowsTheDocumentAtTheZoom(t *testing.T) {
	g := newWebHeightGovernor(1.25)
	if !g.fits() {
		t.Error("nothing reported: the wheel goes on")
	}
	mustReport(t, &g, 400, false, 500)
	if c, _ := g.content(); g.applied != 500 || c != 500 || !g.fits() {
		t.Errorf("applied %v, content %v, fits %v", g.applied, c, g.fits())
	}
	mustKeep(t, &g, 400, false)          // the same height again
	mustReport(t, &g, 300.2, false, 376) // pixels round up
	mustReport(t, &g, 0, false, 1)       // never below one pixel
	mustKeep(t, &g, math.NaN(), false)
	mustKeep(t, &g, math.Inf(1), false)
	mustKeep(t, &g, -5, false)
}

func TestWebHeightIsCappedAndScrollsInside(t *testing.T) {
	g := newWebHeightGovernor(1)
	mustReport(t, &g, 9000, false, webMaxHeight)
	if g.fits() {
		t.Error("beyond the cap the card scrolls inside")
	}
	mustReport(t, &g, 800, false, 800)
	if !g.fits() {
		t.Error("back under the cap: fits")
	}
}

func TestWebHeightFreezesContentThatGrowsWithTheView(t *testing.T) {
	// A 100vh document reports the view's height plus the column's padding
	// every time the view grows.
	g := newWebHeightGovernor(1)
	mustReport(t, &g, 156, false, 156)
	mustReport(t, &g, 192, true, 192)
	mustReport(t, &g, 228, true, 228)
	mustReport(t, &g, 264, true, 264)
	mustKeep(t, &g, 300, true) // the fourth growth in a row freezes
	if !g.frozen || g.applied != 264 || g.fits() {
		t.Errorf("frozen %v, applied %v, fits %v", g.frozen, g.applied, g.fits())
	}
	mustKeep(t, &g, 100, false) // frozen until something changes

	// A new width measures again.
	g.widthChanged()
	if g.frozen {
		t.Error("still frozen after a new width")
	}
	mustReport(t, &g, 100, false, 100)

	// Growth that the view's own growth did not cause (pictures arriving)
	// never freezes, and resets the count.
	p := newWebHeightGovernor(1)
	for i, h := range []float64{200, 260, 330, 420, 500, 610, 700} {
		mustReport(t, &p, h, i%3 == 2, h)
	}
	if p.frozen {
		t.Error("pictures froze the height")
	}

	// A shrink in answer to the view resets the count too.
	s := newWebHeightGovernor(1)
	s.report(100, false)
	s.report(200, true)
	s.report(300, true)
	mustReport(t, &s, 250, true, 250)
	mustReport(t, &s, 350, true, 350)
	if s.frozen {
		t.Error("a shrink did not reset the count")
	}
}

func TestWebHeightZoomAndNewDocumentsStartOver(t *testing.T) {
	g := newWebHeightGovernor(1)
	g.report(400, false)
	if h, ok := g.setZoom(1.5); !ok || h != 600 {
		t.Errorf("the last height at the new zoom at once: %v, %v", h, ok)
	}
	if _, ok := g.setZoom(1.5); ok {
		t.Error("the same zoom again")
	}
	if h, ok := g.setZoom(0); !ok || h != 400 {
		t.Errorf("zero is 100 %%: %v, %v", h, ok)
	}
	g.report(100, false)
	for _, h := range []float64{136, 172, 208, 244} {
		g.report(h, true)
	}
	if !g.frozen {
		t.Fatal("not frozen")
	}
	// The last report (the frozen document's own height) at the new zoom:
	// a zoom measures again.
	if h, ok := g.setZoom(2); !ok || h != 488 {
		t.Errorf("zoom of a frozen document: %v, %v", h, ok)
	}
	if g.frozen {
		t.Error("still frozen after a zoom")
	}

	g.reset()
	if _, ok := g.content(); ok || g.frozen {
		t.Error("a new document keeps the old report")
	}
	if g.applied != 488 {
		t.Errorf("the view keeps its height until the new document reports: %v", g.applied)
	}
	mustReport(t, &g, 50, false, 100)

	fresh := newWebHeightGovernor(1)
	if _, ok := fresh.setZoom(2); ok {
		t.Error("nothing reported yet")
	}
}

// A card reloads its document only when the pictures of the body it shows
// were downloaded: the same message, a new answer, pictures counted on the
// server before.
func TestPicturesArrived(t *testing.T) {
	before := &api.MessageBodyResult{MessageID: "m1", HTML: "<img>", RemotePictures: 2}
	now := &api.MessageBodyResult{MessageID: "m1", HTML: "<img>"}
	if !picturesArrived(before, now) {
		t.Error("the pictures arrived")
	}
	for name, c := range map[string][2]*api.MessageBodyResult{
		"first body":      {nil, now},
		"same answer":     {before, before},
		"no pictures":     {now, {MessageID: "m1"}},
		"another message": {before, {MessageID: "m2"}},
	} {
		if picturesArrived(c[0], c[1]) {
			t.Errorf("%s: reloads", name)
		}
	}
}

// The status label shows at most so many characters of a link.
func TestClampText(t *testing.T) {
	if got := clampText("https://example.org/", 512); got != "https://example.org/" {
		t.Errorf("short: %q", got)
	}
	if got := clampText("žluťoučký", 4); got != "žluť" {
		t.Errorf("cut by characters: %q", got)
	}
	if got := clampText("ab", 1); got != "a" {
		t.Errorf("cut: %q", got)
	}
}

// The pane shows what opened the conversation first, then the rest
// newest first and the row of older members last; the model keeps its own
// order, and the timeline follows the order shown.
func TestConvDisplayOrder(t *testing.T) {
	ids := func(items []conversation.Item) []string {
		var out []string
		for _, it := range items {
			if it.Kind == conversation.ItemTruncated {
				out = append(out, "…")
				continue
			}
			out = append(out, string(it.Message.ID))
		}
		return out
	}
	msg := func(id string) conversation.Item {
		return conversation.Item{Kind: conversation.ItemMessage, Message: api.MessageSummary{ID: api.MessageID(id)}}
	}
	event := func(id string) conversation.Item {
		return conversation.Item{Kind: conversation.ItemEvent, Message: api.MessageSummary{ID: api.MessageID(id)}}
	}
	issue := func(id string, kind api.IssueItemKind) conversation.Item {
		it := msg(id)
		it.Message.Issue = &api.MessageIssue{Item: kind}
		return it
	}
	truncated := conversation.Item{Kind: conversation.ItemTruncated, Text: "2 earlier messages are not shown"}

	cases := []struct {
		name   string
		items  []conversation.Item
		want   []string
		root   int
		folded bool
	}{
		{"mail: the first message on top, folded", []conversation.Item{msg("a"), msg("b"), msg("c")}, []string{"a", "c", "b"}, 0, true},
		{"issue: the description on top", []conversation.Item{
			issue("desc", api.IssueItemDescription), event("e1"), issue("c1", api.IssueItemComment), event("e2"),
		}, []string{"desc", "e2", "c1", "e1"}, 0, true},
		{"issue with only status changes: the description stays open", []conversation.Item{
			issue("desc", api.IssueItemDescription), event("e1"), event("e2"),
		}, []string{"desc", "e2", "e1"}, 0, false},
		{"cut mail: no first message, older ones last", []conversation.Item{truncated, msg("x"), msg("y")}, []string{"y", "x", "…"}, -1, false},
		{"cut issue: the description when it is there", []conversation.Item{
			truncated, issue("desc", api.IssueItemDescription), issue("c1", api.IssueItemComment),
		}, []string{"desc", "c1", "…"}, 0, true},
		{"an event first is no opening", []conversation.Item{event("e"), msg("m")}, []string{"m", "e"}, -1, false},
		{"nothing", nil, nil, -1, false},
	}
	for _, c := range cases {
		before := append([]conversation.Item(nil), c.items...)
		d := convDisplayOrder(c.items)
		if got := ids(d.items); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: order %v, want %v", c.name, got, c.want)
		}
		if d.root != c.root || d.rootFolded != c.folded {
			t.Errorf("%s: root %d folded %v, want %d %v", c.name, d.root, d.rootFolded, c.root, c.folded)
		}
		if !reflect.DeepEqual(before, c.items) {
			t.Errorf("%s: the model's items were changed", c.name)
		}
	}

	// The timeline runs from the opening card down to the row of older
	// members.
	shown := convDisplayOrder([]conversation.Item{truncated, msg("x"), msg("y")}).items
	rails := convRails(shown)
	if rails[0].Above || !rails[0].Below || rails[0].Marker != markAvatar {
		t.Errorf("top: %+v", rails[0])
	}
	if last := rails[len(rails)-1]; !last.Above || last.Below || last.Marker != markDot {
		t.Errorf("older row at the bottom: %+v", last)
	}
}
