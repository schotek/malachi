// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/conversation"
)

// The conversation host and the chat layout without GTK
// (conversation_fanout.go, conversation_chat_layout.go), over the fake
// window of conversation_controller_test.go: the host is the controller's
// only subscriber and hands every change to both layouts, the news of a
// body to the layout shown, arms the mark-read once per selection and
// trims the bodies only with the near set of the layout shown; switching
// layouts asks the daemon nothing; the chat asks for the bodies of the
// bubbles near its viewport only, opens at its end, and a double click on
// a bubble opens the message, on a draft the draft. The widgets themselves
// are TestConversationChatGTKSmoke's.

// convWindowOpened are the messages the fake window was asked to open
// (convOpenMessage) and the drafts to edit (convEditDraft), by window: the
// fake's struct is conversation_controller_test.go's, which these tests
// leave alone.
var (
	convWindowOpened = map[*convWindow][]api.MessageSummary{}
	convWindowEdited = map[*convWindow][]api.Draft{}
)

func (w *convWindow) convOpenMessage(s api.MessageSummary) {
	convWindowOpened[w] = append(convWindowOpened[w], s)
}

func (w *convWindow) convEditDraft(d api.Draft) {
	convWindowEdited[w] = append(convWindowEdited[w], d)
}

// fakeConvLayout is a layout that records what the host asks of it; log
// is shared by both layouts, so that the order shows.
type fakeConvLayout struct {
	name    string
	log     *[]string
	changes []convChange
	loads   []api.MessageID
	shown   bool
	pages   int
}

func (l *fakeConvLayout) changed(ch convChange) { l.changes = append(l.changes, ch) }

func (l *fakeConvLayout) loaded(id api.MessageID, _ *loadedMessage) { l.loads = append(l.loads, id) }

func (l *fakeConvLayout) setShown(on bool) {
	if on == l.shown {
		return
	}
	l.shown = on
	verb := "hide"
	if on {
		verb = "show"
	}
	*l.log = append(*l.log, l.name+":"+verb)
}

func (l *fakeConvLayout) pageBy(bool) bool {
	l.pages++
	return true
}

// newConvFanout is a host's fan-out over w's controller with two fake
// layouts, the timeline shown; marked are the members armed for marking
// read.
func newConvFanout(w *convWindow) (f *convFanout, timeline, chat *fakeConvLayout, log *[]string, marked *[]api.MessageID) {
	log = &[]string{}
	marked = &[]api.MessageID{}
	timeline = &fakeConvLayout{name: "timeline", log: log}
	chat = &fakeConvLayout{name: "chat", log: log}
	f = &convFanout{ctrl: w.ctrl, timelineLayout: timeline, chatLayout: chat,
		markRead: func(id api.MessageID) { *marked = append(*marked, id) }}
	f.attach()
	f.showLayout(false)
	return f, timeline, chat, log, marked
}

func TestConversationHostFansOutToBothLayouts(t *testing.T) {
	w := newConvWindow(t, convMessages())
	f, timeline, chat, log, marked := newConvFanout(w)
	if !reflect.DeepEqual(*log, []string{"timeline:show"}) {
		t.Fatalf("first layout: %v", *log)
	}
	w.selectRow(w.row("t1", "", false))
	want := []convChange{convLoading, convOpened}
	if !reflect.DeepEqual(timeline.changes, want) || !reflect.DeepEqual(chat.changes, want) {
		t.Fatalf("changes: timeline %v, chat %v", timeline.changes, chat.changes)
	}
	// The newest unread member, once per selection.
	if w.ctrl.model == nil || !reflect.DeepEqual(*marked, []api.MessageID{w.ctrl.model.MarkRead}) || (*marked)[0] != "a3" {
		t.Fatalf("marked %v", *marked)
	}

	// A body's news goes to the layout shown only.
	w.ctrl.needsBody("a1", false)
	w.flush()
	if !reflect.DeepEqual(timeline.loads, []api.MessageID{"a1"}) || len(chat.loads) != 0 {
		t.Errorf("loads: timeline %v, chat %v", timeline.loads, chat.loads)
	}

	// Switching: the timeline hidden first, then the chat shown; nothing
	// asked of the daemon, nothing marked again, no change announced.
	gets, bodies := w.caller.count(api.MethodThreadGet), w.caller.count(api.MethodMessageBody)
	if !f.showLayout(true) {
		t.Fatal("the chat did not show")
	}
	if f.showLayout(true) {
		t.Error("the chat shown again")
	}
	if !reflect.DeepEqual(*log, []string{"timeline:show", "timeline:hide", "chat:show"}) {
		t.Errorf("order %v", *log)
	}
	if w.caller.count(api.MethodThreadGet) != gets || w.caller.count(api.MethodMessageBody) != bodies {
		t.Errorf("a switch asked the daemon: thread.get %d → %d, message.body %d → %d",
			gets, w.caller.count(api.MethodThreadGet), bodies, w.caller.count(api.MethodMessageBody))
	}
	if len(*marked) != 1 || len(timeline.changes) != 2 || len(chat.changes) != 2 {
		t.Errorf("a switch marked or announced: %v, %v, %v", *marked, timeline.changes, chat.changes)
	}
	w.ctrl.needsBody("a2", false)
	w.flush()
	if !reflect.DeepEqual(chat.loads, []api.MessageID{"a2"}) || len(timeline.loads) != 1 {
		t.Errorf("loads after the switch: timeline %v, chat %v", timeline.loads, chat.loads)
	}

	// Space pages the layout shown.
	f.pageBy(false)
	if chat.pages != 1 || timeline.pages != 0 {
		t.Errorf("pages: timeline %d, chat %d", timeline.pages, chat.pages)
	}

	// Another conversation reaches both layouts, the hidden one too, and
	// marks once more at most.
	w.selectRow(w.row("t3", "", false))
	want = append(want, convLoading, convOpened)
	if !reflect.DeepEqual(timeline.changes, want) || !reflect.DeepEqual(chat.changes, want) {
		t.Errorf("changes after t3: timeline %v, chat %v", timeline.changes, chat.changes)
	}
	if len(*marked) > 2 || (len(*marked) == 2 && (*marked)[1] != w.ctrl.model.MarkRead) {
		t.Errorf("marked after t3: %v", *marked)
	}
}

// The host trims the controller's bodies only with the near set of the
// layout shown: the hidden layout's report is ignored.
func TestConversationHostTrimsWithTheShownLayoutsNearSet(t *testing.T) {
	w := newConvWindow(t, convMessages())
	f, timeline, chat, _, _ := newConvFanout(w)
	f.heldBytes = 1 // every entry is above budget
	w.selectRow(w.row("t1", "", false))
	for _, id := range []api.MessageID{"a1", "a2", "a3"} {
		w.ctrl.needsBody(id, false)
	}
	w.flush()
	if len(w.ctrl.loaded) != 3 {
		t.Fatalf("held %d entries", len(w.ctrl.loaded))
	}
	f.showLayout(true)
	// The hidden timeline keeps nothing near: ignored.
	f.near(timeline, map[api.MessageID]bool{})
	if len(w.ctrl.loaded) != 3 {
		t.Errorf("the hidden layout trimmed: %d left", len(w.ctrl.loaded))
	}
	// The chat keeps a3 near: the others go.
	f.near(chat, map[api.MessageID]bool{"a3": true})
	if len(w.ctrl.loaded) != 1 || w.ctrl.loaded["a3"] == nil {
		t.Errorf("after the chat's trim: %v", w.ctrl.loaded)
	}
}

// convBigThread is a conversation of n members in the folder, every third
// with attachments, an hour apart.
func convBigThread(n int) []api.MessageSummary {
	out := make([]api.MessageSummary, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, convMsg(fmt.Sprintf("m%02d", i), i, "big", "bob", i%3 == 0, api.FlagSeen))
	}
	return out
}

// convChatIDs are the members of the chat's message rows, top to bottom.
func convChatIDs(rows []conversation.ChatRow) []api.MessageID {
	var out []api.MessageID
	for _, r := range rows {
		if r.Kind == conversation.ChatBubble {
			out = append(out, r.Item.Message.ID)
		}
	}
	return out
}

// The chat opens at its end and asks for the bodies of the bubbles near
// it only (two heights of the viewport), message.get as well for a
// message with attachments (its paperclip's number); the bubbles far up
// ask for nothing until they come near.
func TestConversationChatAsksOnlyForTheBodiesNearItsEnd(t *testing.T) {
	w := newConvWindow(t, convBigThread(30))
	w.selectRow(w.row("big", "", false))
	if w.ctrl.model == nil {
		t.Fatal("no conversation")
	}
	rows := conversation.ChatRows(*w.ctrl.model, w.ctrl.drafts)
	ids := convChatIDs(rows)
	if len(ids) != 30 || ids[0] != "m00" || ids[29] != "m29" {
		t.Fatalf("rows %v", ids)
	}
	if got := conversation.ChatScrollTo(rows); got != len(rows)-1 {
		t.Errorf("the chat opens at %d of %d", got, len(rows))
	}
	frames := convStacked(len(ids), 100)
	viewport, document := 400.0, 3000.0
	top := convChatEndTop(viewport, document)
	if top != 2600 || !convChatAtEnd(top, viewport, document, convChatEndSlack) {
		t.Fatalf("end top %v", top)
	}
	near := chatNear(w.ctrl, ids, frames, convSpanOf(top, top+viewport))
	w.flush()
	// Two heights of 400 above the viewport's top at 2600: from 1800, the
	// row ending there included (touching counts, liveCards).
	var got []string
	for id := range near {
		got = append(got, string(id))
	}
	sort.Strings(got)
	var want []string
	for _, id := range ids[17:] {
		want = append(want, string(id))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("near %v, want %v", got, want)
	}
	if n := w.caller.count(api.MethodMessageBody); n != len(want) {
		t.Errorf("message.body %d, want %d", n, len(want))
	}
	gets := 0
	for _, id := range ids[17:] {
		if s, _ := w.ctrl.member(id); s.HasAttachments {
			gets++
		}
	}
	if n := w.caller.count(api.MethodMessageGet); n != gets || gets == 0 {
		t.Errorf("message.get %d, want %d", n, gets)
	}
	for _, id := range ids[:17] {
		if w.ctrl.loaded[id] != nil {
			t.Errorf("%s far up was asked for", id)
		}
	}

	// Scrolled to the top: the bubbles there ask now, those held stay.
	before := w.caller.count(api.MethodMessageBody)
	chatNear(w.ctrl, ids, frames, convSpanOf(0, viewport))
	w.flush()
	if n := w.caller.count(api.MethodMessageBody) - before; n != 13 {
		t.Errorf("at the top: %d more bodies, want 13", n)
	}
}

// A double click on a bubble opens its message as a double click on its
// row in the list does; on a draft, the draft; the centred rows and a
// member no longer shown open nothing.
func TestConversationChatDoubleClickOpensTheMessageOrTheDraft(t *testing.T) {
	w := newConvWindow(t, convMessages())
	w.drafts["t1"] = []api.Draft{{ID: "d1", AccountID: "a", Version: 1, TextBody: "Draft reply", InReplyTo: "a3",
		UpdatedAt: convBase.Add(9 * time.Hour)}}
	w.list()
	w.selectRow(w.row("t1", "", false))
	if w.ctrl.model == nil || len(w.ctrl.drafts) != 1 {
		t.Fatalf("no conversation with its draft: %+v", w.ctrl.drafts)
	}
	rows := conversation.ChatRows(*w.ctrl.model, w.ctrl.drafts)
	var shape []string
	for _, r := range rows {
		switch r.Kind {
		case conversation.ChatBubble:
			shape = append(shape, string(r.Item.Message.ID))
		case conversation.ChatDraft:
			shape = append(shape, "draft:"+string(r.Draft.Draft.ID))
		}
	}
	if !reflect.DeepEqual(shape, []string{"a1", "a2", "a3", "draft:d1"}) {
		t.Fatalf("rows %v", shape)
	}
	chatActivate(w.ctrl, rows[1])
	if got := convWindowOpened[w]; len(got) != 1 || got[0].ID != "a2" || got[0].AccountID != "a" || got[0].ThreadID != "t1" {
		t.Errorf("opened %+v", got)
	}
	chatActivate(w.ctrl, rows[3])
	if got := convWindowEdited[w]; len(got) != 1 || got[0].ID != "d1" {
		t.Errorf("edited %+v", got)
	}
	// A centred row, and a member that went.
	chatActivate(w.ctrl, conversation.ChatRow{Kind: conversation.ChatTruncated, Side: conversation.ChatCenter})
	chatActivate(w.ctrl, conversation.ChatRow{Kind: conversation.ChatBubble, Item: conversation.Item{Message: api.MessageSummary{ID: "gone"}}})
	if len(convWindowOpened[w]) != 1 || len(convWindowEdited[w]) != 1 {
		t.Errorf("opened %v, edited %v", convWindowOpened[w], convWindowEdited[w])
	}
	delete(convWindowOpened, w)
	delete(convWindowEdited, w)
}

// Where the chat keeps its viewport (convChatScroll, D2): open at the end;
// an arrival keeps the end in view while it shows; scrolled away, the row
// read keeps its place; back at the end, the end stays again.
func TestConversationChatScrollStaysAtTheEnd(t *testing.T) {
	var s convChatScroll
	if s.toEnd() {
		t.Error("nothing shown is at the end")
	}
	s.open()
	if !s.toEnd() {
		t.Error("a chat does not open at its end")
	}
	// The user scrolls up: the end is no longer kept.
	s.scrolled()
	s.measure(100, 400, 1000)
	if s.toEnd() {
		t.Error("scrolled up, an arrival moves the viewport to the end")
	}
	// Back at the end (within the slack): kept again.
	s.measure(598, 400, 1000)
	if !s.toEnd() {
		t.Error("back at the end, an arrival does not keep it")
	}
	// Pinned, the end holds whatever was measured (heights settling).
	s.open()
	s.measure(0, 400, 1000)
	if !s.toEnd() {
		t.Error("the pinned end let go before the user scrolled")
	}
	s.clear()
	if s.toEnd() {
		t.Error("cleared, still at the end")
	}
}

// The bubbles of the chat read the trimmed body the controller holds for
// the timeline too (one cache for both layouts): with the quoted history
// revealed in the timeline, the chat still reads the variant without it.
func TestConversationChatReadsTheControllersBodies(t *testing.T) {
	w := newConvWindow(t, convMessages())
	w.selectRow(w.row("t1", "", false))
	w.ctrl.needsBody("a1", false)
	w.flush()
	text, placeholder := bubbleBody(w.ctrl.loaded["a1"], "p-a1")
	if placeholder || text != "body of a1" {
		t.Fatalf("a1 reads %q (placeholder %v)", text, placeholder)
	}
	// Revealed in the timeline: the whole body is fetched, the bubble
	// keeps the trimmed one kept aside.
	w.ctrl.setQuoted("a1", true, false)
	w.flush()
	if lm := w.ctrl.loaded["a1"]; lm == nil || !lm.quotedShown || !strings.Contains(lm.body.Text, "> old") {
		t.Fatalf("not revealed: %+v", w.ctrl.loaded["a1"])
	}
	if text, _ := bubbleBody(w.ctrl.loaded["a1"], "p-a1"); text != "body of a1" {
		t.Errorf("revealed, the bubble reads %q", text)
	}
	// Not asked for yet: the snippet as the placeholder.
	if text, placeholder := bubbleBody(w.ctrl.loaded["a2"], "p-a2"); !placeholder || text != "p-a2" {
		t.Errorf("a2 reads %q (placeholder %v)", text, placeholder)
	}
}

// convChatHTMLFlags are the bubbles of ids whose held body renders as HTML
// with the preference at setting (bubbleHTML), as updateNear counts them.
func convChatHTMLFlags(ctrl *conversationController, ids []api.MessageID, setting bool) []bool {
	out := make([]bool, len(ids))
	for i, id := range ids {
		out[i] = bubbleHTML(ctrl.loaded[id], setting) != nil
	}
	return out
}

// convChatWeb is the sorted ids that got a web view.
func convChatWeb(live chatLiveSet) []string {
	var out []string
	for id, on := range live.web {
		if on {
			out = append(out, string(id))
		}
	}
	sort.Strings(out)
	return out
}

// With the preference conversation-chat-html the bubbles near the viewport
// whose body has HTML get a web view, at most convMaxLiveWebViews, the
// newest first (the chat measures from its end); a text-only or withheld
// body, a body not loaded and a bubble far from the viewport read as text;
// the preference off gives no bubble a view.
func TestConversationChatHTMLBubblesNearTheEnd(t *testing.T) {
	w := newConvWindow(t, convBigThread(30))
	// Every even member has HTML, m26 is withheld, the odd ones text only.
	w.caller.handlers[api.MethodMessageBody] = func(p any) (any, error) {
		id := p.(api.MessageBodyParams).MessageID
		res := api.MessageBodyResult{MessageID: id, BodyState: api.BodyFetched, Text: "body of " + string(id), HasHTML: true}
		var n int
		fmt.Sscanf(string(id), "m%d", &n)
		switch {
		case id == "m26":
			res.HTMLWithheld = true
		case n%2 == 0:
			res.HTML = "<p>" + string(id) + "</p>"
		default:
			res.HasHTML = false
		}
		return res, nil
	}
	w.selectRow(w.row("big", "", false))
	if w.ctrl.model == nil {
		t.Fatal("no conversation")
	}
	ids := convChatIDs(conversation.ChatRows(*w.ctrl.model, w.ctrl.drafts))
	frames := convStacked(len(ids), 100)
	viewport, document := 400.0, 3000.0
	end := convSpanOf(convChatEndTop(viewport, document), document)

	// Nothing loaded yet: no HTML, no view.
	if got := convChatWeb(chatLive(w.ctrl, ids, frames, convChatHTMLFlags(w.ctrl, ids, true), end)); len(got) != 0 {
		t.Fatalf("views before any body: %v", got)
	}
	w.flush()

	// The preference off: no view ever.
	if got := convChatWeb(chatLive(w.ctrl, ids, frames, convChatHTMLFlags(w.ctrl, ids, false), end)); len(got) != 0 {
		t.Errorf("preference off: views %v", got)
	}

	// On: the near HTML bubbles (m17…m29 are near; the even ones but the
	// withheld m26), at most the cap, the newest first.
	live := chatLive(w.ctrl, ids, frames, convChatHTMLFlags(w.ctrl, ids, true), end)
	want := []string{"m18", "m20", "m22", "m24", "m28"}
	if got := convChatWeb(live); !reflect.DeepEqual(got, want) {
		t.Errorf("views at the end %v, want %v", got, want)
	}
	if !live.near["m17"] || live.near["m16"] {
		t.Errorf("near %v", live.near)
	}

	// A taller viewport sees more HTML bubbles than the cap: the newest
	// keep a view.
	all := convSpanOf(0, document)
	chatNear(w.ctrl, ids, frames, all)
	w.flush()
	html := convChatHTMLFlags(w.ctrl, ids, true)
	live = chatLive(w.ctrl, ids, frames, html, all)
	want = []string{"m12", "m14", "m16", "m18", "m20", "m22", "m24", "m28"}
	if got := convChatWeb(live); !reflect.DeepEqual(got, want) || len(got) != convMaxLiveWebViews {
		t.Errorf("views of the whole chat %v, want %v", got, want)
	}

	// Scrolled to the top: the views go to the bubbles there; those at
	// the end read as text again.
	live = chatLive(w.ctrl, ids, frames, html, convSpanOf(0, viewport))
	want = []string{"m00", "m02", "m04", "m06", "m08", "m10", "m12"}
	if got := convChatWeb(live); !reflect.DeepEqual(got, want) {
		t.Errorf("views at the top %v, want %v", got, want)
	}
}

// A bubble whose view went while it still wants its HTML (far, beyond the
// cap) keeps the document's last height as its text's minimum, as a
// released card keeps its paper; the preference off (no HTML wanted) or no
// height reported yet keeps none, nor does a bubble showing its view.
func TestConversationChatReleasedHTMLBubbleKeepsItsHeight(t *testing.T) {
	w := newConvWindow(t, convBigThread(30))
	w.caller.handlers[api.MethodMessageBody] = func(p any) (any, error) {
		id := p.(api.MessageBodyParams).MessageID
		return api.MessageBodyResult{MessageID: id, BodyState: api.BodyFetched, Text: "t", HTML: "<p>x</p>", HasHTML: true}, nil
	}
	w.selectRow(w.row("big", "", false))
	ids := convChatIDs(conversation.ChatRows(*w.ctrl.model, w.ctrl.drafts))
	frames := convStacked(len(ids), 100)
	end := convSpanOf(2600, 3000)
	chatNear(w.ctrl, ids, frames, end)
	w.flush()
	live := chatLive(w.ctrl, ids, frames, convChatHTMLFlags(w.ctrl, ids, true), end)
	if !live.web["m29"] {
		t.Fatal("m29 has no view at the end")
	}
	wants := bubbleHTML(w.ctrl.loaded["m29"], true) != nil
	if h := bubbleFrozenHeight(wants, true, true, 340); h != 0 {
		t.Errorf("a live view frozen at %d", h)
	}
	// Scrolled to the top: m29 lets its view go and keeps 340.
	live = chatLive(w.ctrl, ids, frames, convChatHTMLFlags(w.ctrl, ids, true), convSpanOf(0, 400))
	if live.web["m29"] {
		t.Fatal("m29 kept its view far up")
	}
	if h := bubbleFrozenHeight(wants, live.web["m29"], true, 340); h != 340 {
		t.Errorf("released far row: %d, want 340", h)
	}
	// Never reported: nothing to keep.
	if h := bubbleFrozenHeight(wants, false, false, 340); h != 0 {
		t.Errorf("unreported: %d", h)
	}
	// The preference off: the bubble wants no HTML, the height goes.
	off := bubbleHTML(w.ctrl.loaded["m29"], false) != nil
	if h := bubbleFrozenHeight(off, false, true, 340); h != 0 {
		t.Errorf("preference off: %d", h)
	}
}

// Shortened bubbles: the preference shortens every message and draft
// bubble (never a centred row) until the user expands it; expanding is
// kept while the conversation stays (a layout switch keeps it) and
// forgotten on another; the preference off shows every bubble whole.
func TestConversationChatShortenedBubbles(t *testing.T) {
	w := newConvWindow(t, convMessages())
	w.drafts["t1"] = []api.Draft{{ID: "d1", AccountID: "a", Version: 1, TextBody: "Draft reply", InReplyTo: "a3",
		UpdatedAt: convBase.Add(9 * time.Hour)}}
	w.list()
	w.selectRow(w.row("t1", "", false))
	rows := conversation.ChatRows(*w.ctrl.model, w.ctrl.drafts)
	var e conversation.Expanded
	scope := ""
	chatExpandedScope(&e, &scope, string(w.ctrl.model.Thread))
	// Each preference shortens its side: the user's own (on the right)
	// with mine, the others' (on the left) with others; a draft always,
	// both preferences off too.
	for _, r := range rows {
		isDraft := r.Kind == conversation.ChatDraft
		if isDraft {
			for _, on := range [][2]bool{{false, false}, {true, false}, {false, true}, {true, true}} {
				if !chatRowShortened(on[0], on[1], &e, r) {
					t.Errorf("a draft not shortened with %v", on)
				}
			}
			if !chatClickOpens(r) || chatExpandRow(&e, r) {
				t.Error("a click on a draft expands it instead of opening it")
			}
			continue
		}
		if chatClickOpens(r) {
			t.Errorf("a click on %v opens it", r.Kind)
		}
		bubble := r.Kind == conversation.ChatBubble
		mine := bubble && r.Side == conversation.ChatRight
		if got := chatRowShortened(true, true, &e, r); got != bubble {
			t.Errorf("%v shortened %v with both on", r.Kind, got)
		}
		if got := chatRowShortened(true, false, &e, r); got != (bubble && mine) {
			t.Errorf("%v (mine %v) shortened %v with mine on", r.Kind, mine, got)
		}
		if got := chatRowShortened(false, true, &e, r); got != (bubble && !mine) {
			t.Errorf("%v (mine %v) shortened %v with others on", r.Kind, mine, got)
		}
		if chatRowShortened(false, false, &e, r) {
			t.Errorf("%v shortened with both off", r.Kind)
		}
	}
	if chatRowShortened(true, true, &e, conversation.ChatRow{Kind: conversation.ChatTruncated}) {
		t.Error("a centred row is shortened")
	}
	// A click expands a message; a draft stays shortened (it opens).
	var msg, draft conversation.ChatRow
	for _, r := range rows {
		switch r.Kind {
		case conversation.ChatBubble:
			msg = r
		case conversation.ChatDraft:
			draft = r
		}
	}
	if !chatExpandRow(&e, msg) || chatExpandRow(&e, draft) {
		t.Fatal("the message did not expand, or the draft did")
	}
	if chatRowShortened(true, true, &e, msg) || !chatRowShortened(false, false, &e, draft) {
		t.Error("the expanded message is shortened, or the draft is not")
	}
	// The click on a draft opens it in the compose window.
	chatActivate(w.ctrl, draft)
	if got := convWindowEdited[w]; len(got) != 1 || got[0].ID != "d1" {
		t.Errorf("a click on the draft edited %+v", got)
	}
	delete(convWindowEdited, w)
	if chatExpandRow(&e, conversation.ChatRow{Kind: conversation.ChatEvent}) {
		t.Error("an event row expanded")
	}
	// The same conversation again (a layout switch, a refresh): kept.
	chatExpandedScope(&e, &scope, string(w.ctrl.model.Thread))
	if chatRowShortened(true, true, &e, msg) {
		t.Error("the same conversation forgot the expanded bubble")
	}
	// Another conversation: forgotten.
	w.selectRow(w.row("t3", "", false))
	chatExpandedScope(&e, &scope, string(w.ctrl.model.Thread))
	if !chatRowShortened(true, true, &e, msg) {
		t.Error("another conversation kept the expanded bubble")
	}

	// The HTML bubble's clipped height and its overflow.
	preview := chatPreviewHeight(17.2)
	if preview != 52 {
		t.Fatalf("preview %d", preview)
	}
	if h := chatShownHeight(300, preview, true); h != preview {
		t.Errorf("shortened document %d", h)
	}
	if h := chatShownHeight(40, preview, true); h != 40 {
		t.Errorf("a short document clipped to %d", h)
	}
	if h := chatShownHeight(300, preview, false); h != 300 {
		t.Errorf("whole document %d", h)
	}
	if !chatHTMLOverflows(true, 300, preview, true) || chatHTMLOverflows(true, 40, preview, true) ||
		chatHTMLOverflows(false, 300, preview, true) || chatHTMLOverflows(true, 300, preview, false) {
		t.Error("overflow rule")
	}
	// A released shortened bubble keeps the clipped height.
	if h := bubbleFrozenHeight(true, false, true, chatShownHeight(300, preview, true)); h != preview {
		t.Errorf("released shortened bubble keeps %d", h)
	}
}
