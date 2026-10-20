// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/conversation"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settings"
)

// Run in a dedicated test process with a display (xvfb-run, Broadway):
// MALACHI_GTK_SMOKE=1 go test ./internal/window -run
// '^TestConversationChatGTKSmoke$' -count=1 -v. The conversation page of
// the reading pane with its real Blueprint (conversation_view.blp) over an
// invented conversation: the RPC client is never connected, the settings
// are a memory store, and the controller's host is the fake window of
// conversation_controller_test.go (its members, the user's reply in Sent
// and a draft, bodies "body of <id>"). It shows the timeline, folds a card,
// switches to the chat with the toggle (through the setting), checks the
// strip, the bubbles' sides, their bodies near the end, the end in view,
// the width of a short and a long bubble, a double click's plumbing, and
// switches back to find the fold kept; with the preference
// conversation-chat-html on, a2's HTML bubble holds a live web view (real
// WebKit: in the container with WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS=1)
// at the bubble's maximum width, the text-only bubbles none, and the
// preference off or the chat hidden lets it go; LAYOUT: lines log the
// allocations.
func TestConversationChatGTKSmoke(t *testing.T) {
	if os.Getenv("MALACHI_GTK_SMOKE") != "1" {
		t.Skip("set MALACHI_GTK_SMOKE=1 with a GTK display to run the widget smoke test")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	adw.Init()
	rpc := client.New(filepath.Join(t.TempDir(), "absent.sock"))
	defer rpc.Close()
	st := settings.NewMemory()
	w := &Window{
		ApplicationWindow: data.Builder("window.ui").GetObject("main_window").Cast().(*adw.ApplicationWindow),
		log:               slog.Default(), mode: board.ModeMail,
		client:   rpc,
		settings: st,
		// The attachment chips of a timeline card bind their "Ask the
		// Assistant" item to the window's Assistant, which the real window
		// always has.
		assist:        NewAssistant(st, slog.Default()),
		actions:       make(map[string]*gio.SimpleAction),
		toasts:        adw.NewToastOverlay(),
		messageStack:  gtk.NewStack(),
		messageList:   gtk.NewListBox(),
		loaded:        map[api.MessageID]*loadedMessage{},
		findingDrafts: map[api.DraftID]bool{},
		openMessages:  map[api.MessageID]*MessageWindow{},
	}
	defer w.Destroy()

	h := w.conversationPane()
	var marked []api.MessageID
	h.markRead = func(id api.MessageID) { marked = append(marked, id) }

	// The fake window as the controller's host: t1 is bob's a1, alice's
	// a2 and carol's a3 (with attachments) in the folder, the user's reply
	// r1 in Sent and a draft reply d1. a1's body is long, r1's short.
	msgs := append(convMessages(), convReply("r1", 4, "t1"))
	fake := newConvWindow(t, msgs)
	long := strings.Repeat("A long line of the first message that wraps in its bubble. ", 40)
	body := fake.caller.handlers[api.MethodMessageBody]
	fake.caller.handlers[api.MethodMessageBody] = func(p any) (any, error) {
		switch p.(api.MessageBodyParams).MessageID {
		case "a1":
			return api.MessageBodyResult{MessageID: "a1", BodyState: api.BodyFetched, Text: long}, nil
		case "r1":
			return api.MessageBodyResult{MessageID: "r1", BodyState: api.BodyFetched, Text: "Ok"}, nil
		case "a2":
			return api.MessageBodyResult{MessageID: "a2", BodyState: api.BodyFetched, Text: "body of a2",
				HTML: "<p>body of <b>a2</b></p><p>second</p><p>third</p><p>fourth</p><p>fifth</p>", HasHTML: true, QuotedTrimmed: true}, nil
		}
		return body(p)
	}
	get := fake.caller.handlers[api.MethodMessageGet]
	fake.caller.handlers[api.MethodMessageGet] = func(p any) (any, error) {
		res, err := get(p)
		if r, ok := res.(api.MessageGetResult); ok && r.Message.ID == "a3" {
			r.Message.Attachments = []api.Attachment{{PartID: "2", Filename: "report.pdf", ContentType: "application/pdf", Size: 1000}}
			return r, err
		}
		return res, err
	}
	fake.drafts["t1"] = []api.Draft{{ID: "d1", AccountID: "a", Version: 1, TextBody: "Draft reply", InReplyTo: "a3",
		UpdatedAt: convBase.Add(9 * time.Hour)}}
	fake.list()
	fake.ctrl = h.ctrl
	h.ctrl.host = fake

	w.SetContent(w.messageStack)
	w.messageStack.SetVisibleChildName(convPageName)
	w.SetDefaultSize(1000, 800)
	w.Present()
	ctx := glib.MainContextDefault()
	// run runs both main loops (the fake's answers, GTK's idles and
	// frames) for d.
	run := func(d time.Duration) {
		end := time.Now().Add(d)
		for time.Now().Before(end) {
			fake.flush()
			for i := 0; i < 100 && ctx.Pending(); i++ {
				ctx.Iteration(false)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	fake.selectRow(fake.row("t1", "", false))
	run(500 * time.Millisecond)

	// The timeline first (the setting's default).
	if h.chatShown || h.layouts.VisibleChildName() != convTimelinePage {
		t.Fatalf("the timeline does not show: %q", h.layouts.VisibleChildName())
	}
	if len(h.timeline.cards) != 4 || len(h.timeline.drafts) != 1 {
		t.Fatalf("timeline: %d cards, %d drafts", len(h.timeline.cards), len(h.timeline.drafts))
	}
	if h.toggleContent.Label() != conversation.ChatToggleLabel(false, i18n.Tr) || h.toggle.Active() {
		t.Errorf("toggle off: %q active %v", h.toggleContent.Label(), h.toggle.Active())
	}
	if len(marked) != 1 || marked[0] != "a3" {
		t.Errorf("marked %v", marked)
	}
	// Fold a card the user chose to fold: kept through a visit to the chat.
	a2 := h.timeline.cards["a2"]
	h.timeline.setCardFolded(a2, true)
	run(100 * time.Millisecond)
	t.Logf("LAYOUT: timeline page=%dx%d strip foldAll=%v %q", h.page.Width(), h.page.Height(),
		h.foldAllButton.Visible(), h.foldAllButton.Label())

	// The toggle switches through the setting.
	gets := fake.caller.count(api.MethodThreadGet)
	h.toggle.SetActive(true)
	run(800 * time.Millisecond)
	if !w.settings.ConversationChatLayout() {
		t.Fatal("the toggle did not write the setting")
	}
	if !h.chatShown || h.layouts.VisibleChildName() != convChatPage {
		t.Fatalf("the chat does not show: %q", h.layouts.VisibleChildName())
	}
	if fake.caller.count(api.MethodThreadGet) != gets {
		t.Error("switching asked thread.get again")
	}
	if h.toggleContent.Label() != conversation.ChatToggleLabel(true, i18n.Tr) ||
		h.toggle.TooltipText() != conversation.ChatToggleTooltip(true, i18n.Tr) {
		t.Errorf("toggle on: %q, %q", h.toggleContent.Label(), h.toggle.TooltipText())
	}
	if h.foldAllButton.Visible() {
		t.Error("Collapse All shows in the chat")
	}
	for _, c := range h.timeline.cards {
		if c.web != nil {
			t.Error("the hidden timeline keeps a web view")
		}
	}

	// The rows: the others on the left, the reply in Sent on the right,
	// the draft last on the right.
	chat := h.chat
	if len(chat.bubbles) != 4 || len(chat.drafts) != 1 {
		t.Fatalf("chat: %d bubbles, %d drafts", len(chat.bubbles), len(chat.drafts))
	}
	for id, right := range map[api.MessageID]bool{"a1": false, "a2": false, "a3": false, "r1": true} {
		if chat.bubbles[id].right != right {
			t.Errorf("%s on the right: %v", id, chat.bubbles[id].right)
		}
	}
	if !chat.drafts["d1"].right || !chat.drafts["d1"].bubble.draftPill.Visible() {
		t.Error("the draft is not a draft bubble on the right")
	}
	if last := chat.order[len(chat.order)-1]; last != gtk.Widgetter(chat.drafts["d1"]) {
		t.Error("the draft is not the last row")
	}
	// Bodies near the end, the end in view.
	for _, id := range []api.MessageID{"a1", "r1", "a3"} {
		b := chat.bubbles[id].bubble
		if !b.showsText || b.text.Text() == "" {
			t.Errorf("%s shows no body: %q", id, b.text.Text())
		}
	}
	if got := chat.bubbles["r1"].bubble.text.Text(); got != "Ok" {
		t.Errorf("r1 reads %q", got)
	}
	if b := chat.bubbles["a3"].bubble; !b.clip.Visible() || b.clipCount.Text() != "1" {
		t.Errorf("a3's paperclip: shown %v, %q", b.clip.Visible(), b.clipCount.Text())
	}
	adj := chat.scroller.VAdjustment()
	if !convChatAtEnd(adj.Value(), adj.PageSize(), adj.Upper(), convChatEndSlack) {
		t.Errorf("the chat is not at its end: value %v page %v upper %v", adj.Value(), adj.PageSize(), adj.Upper())
	}
	// The widths: the long bubble stops at the maximum, the short one
	// hugs its header and text. The bubble is its frame's border box
	// (AllocatedWidth: the CSS padding and border in); Width is the content
	// alone, 28 px less.
	max := chat.bubbleMax()
	longW := chat.bubbles["a1"].bubble.frame.AllocatedWidth()
	shortW := chat.bubbles["r1"].bubble.frame.AllocatedWidth()
	t.Logf("LAYOUT: chat column=%v max=%d long=%d short=%d compact=%v value=%v page=%v upper=%v",
		chat.columnWidth, max, longW, shortW, chat.compact, adj.Value(), adj.PageSize(), adj.Upper())
	if max <= 0 || longW > max || longW < max-2 {
		t.Errorf("the long bubble is %d wide, the maximum %d", longW, max)
	}
	if shortW >= max || shortW <= 0 {
		t.Errorf("the short bubble is %d wide, the maximum %d", shortW, max)
	}
	if chat.bubbles["a1"].bubble.text.Selectable() {
		t.Error("a bubble's text can be selected")
	}

	// A double click's plumbing: the message, the draft.
	chat.activate(chat.bubbles["a2"])
	chat.activate(chat.drafts["d1"])
	if got := convWindowOpened[fake]; len(got) != 1 || got[0].ID != "a2" {
		t.Errorf("opened %+v", got)
	}
	if got := convWindowEdited[fake]; len(got) != 1 || got[0].ID != "d1" {
		t.Errorf("edited %+v", got)
	}
	delete(convWindowOpened, fake)
	delete(convWindowEdited, fake)

	// Space pages the chat (up from the end, when it is taller than the
	// view).
	before := adj.Value()
	if !h.pageBy(true) || (adj.Upper() > adj.PageSize() && adj.Value() >= before) {
		t.Errorf("Space up did not page the chat: %v → %v", before, adj.Value())
	}
	run(100 * time.Millisecond)

	// HTML in the chat: off by default, no bubble has a view.
	for id, row := range chat.bubbles {
		if row.bubble.web != nil {
			t.Errorf("%s has a web view with the preference off", id)
		}
	}
	st.SetConversationChatHTML(true)
	run(1500 * time.Millisecond)
	a2b := chat.bubbles["a2"]
	if a2b.bubble.web == nil || !a2b.bubble.html.Visible() || a2b.bubble.text.Visible() || !a2b.fill {
		t.Errorf("a2 is no HTML bubble: web %v html %v text %v fill %v", a2b.bubble.web != nil,
			a2b.bubble.html.Visible(), a2b.bubble.text.Visible(), a2b.fill)
	}
	for _, id := range []api.MessageID{"a1", "a3", "r1"} {
		if chat.bubbles[id].bubble.web != nil {
			t.Errorf("text-only %s has a web view", id)
		}
	}
	htmlW := a2b.bubble.frame.AllocatedWidth()
	t.Logf("LAYOUT: chat html bubble width=%d max=%d webHeight=%d host=%dx%d", htmlW, chat.bubbleMax(),
		a2b.bubble.webHeight, a2b.bubble.html.Width(), a2b.bubble.html.Height())
	if !a2b.bubble.reported || a2b.bubble.html.Height() != a2b.bubble.webHeight {
		t.Errorf("a2's view is %d tall, its document %d (reported %v)", a2b.bubble.html.Height(),
			a2b.bubble.webHeight, a2b.bubble.reported)
	}
	if max := chat.bubbleMax(); htmlW > max || htmlW < max-2 {
		t.Errorf("the HTML bubble is %d wide, the maximum %d", htmlW, max)
	}
	// Off: the view goes, the text is back.
	st.SetConversationChatHTML(false)
	run(300 * time.Millisecond)
	if a2b.bubble.web != nil || a2b.bubble.html.Visible() || !a2b.bubble.text.Visible() || a2b.fill {
		t.Errorf("preference off, a2 keeps its view: web %v text %v", a2b.bubble.web != nil, a2b.bubble.text.Visible())
	}
	if _, h := a2b.bubble.text.SizeRequest(); h != -1 {
		t.Errorf("preference off, a2's text keeps a frozen height %d", h)
	}
	if got := a2b.bubble.text.Text(); got != "body of a2" {
		t.Errorf("a2 reads %q", got)
	}
	st.SetConversationChatHTML(true)
	run(800 * time.Millisecond)
	if a2b.bubble.web == nil {
		t.Error("preference on again, a2 has no view")
	}

	// Shortened bubbles: the long text to three lines with Show More, the
	// HTML bubble clipped to the preview height; a click shows it whole;
	// the preference off shows every bubble whole.
	st.SetConversationChatTruncateMine(true)
	st.SetConversationChatTruncateOthers(true)
	run(800 * time.Millisecond)
	a1b := chat.bubbles["a1"].bubble
	if !a1b.short || a1b.text.Lines() != conversation.ChatPreviewLines || !a1b.more.Visible() {
		t.Errorf("a1 not shortened: short %v lines %d more %v", a1b.short, a1b.text.Lines(), a1b.more.Visible())
	}
	if !a2b.bubble.short || a2b.bubble.html.Height() != a2b.bubble.shownHeight() ||
		a2b.bubble.shownHeight() != chatShownHeight(a2b.bubble.webHeight, a2b.bubble.preview, true) {
		t.Errorf("a2 not clipped: host %d shown %d preview %d document %d", a2b.bubble.html.Height(),
			a2b.bubble.shownHeight(), a2b.bubble.preview, a2b.bubble.webHeight)
	}
	if a2b.bubble.webHeight <= a2b.bubble.preview || !a2b.bubble.fade.Visible() || !a2b.bubble.more.Visible() ||
		a2b.bubble.html.Height() != a2b.bubble.preview {
		t.Errorf("a2's clipped document shows no fade or Show More: host %d preview %d document %d fade %v more %v",
			a2b.bubble.html.Height(), a2b.bubble.preview, a2b.bubble.webHeight, a2b.bubble.fade.Visible(), a2b.bubble.more.Visible())
	}
	t.Logf("LAYOUT: shortened a1 lines=%d more=%v; a2 preview=%d document=%d host=%d fade=%v",
		a1b.text.Lines(), a1b.more.Visible(), a2b.bubble.preview, a2b.bubble.webHeight, a2b.bubble.html.Height(), a2b.bubble.fade.Visible())
	a1b.onExpand()
	run(300 * time.Millisecond)
	if a1b.short || a1b.text.Lines() != -1 || a1b.more.Visible() || !chat.expanded.Is("a1") {
		t.Errorf("a1 did not expand: short %v lines %d more %v", a1b.short, a1b.text.Lines(), a1b.more.Visible())
	}
	// Others' off: only the user's own (r1, on the right) stays shortened.
	st.SetConversationChatTruncateOthers(false)
	run(300 * time.Millisecond)
	if a2b.bubble.short || chat.bubbles["a3"].bubble.short || !chat.bubbles["r1"].bubble.short {
		t.Errorf("others off: a2 %v a3 %v r1 %v", a2b.bubble.short, chat.bubbles["a3"].bubble.short, chat.bubbles["r1"].bubble.short)
	}
	st.SetConversationChatTruncateMine(false)
	run(300 * time.Millisecond)
	if chat.bubbles["r1"].bubble.short || chat.drafts["d1"].bubble.short {
		t.Error("both off left bubbles shortened")
	}

	// Back to the timeline: the fold kept, the chat lets go.
	w.settings.SetConversationChatLayout(false)
	run(500 * time.Millisecond)
	for id, row := range chat.bubbles {
		if row.bubble.web != nil {
			t.Errorf("the hidden chat keeps %s's web view", id)
		}
	}
	// Released while it still wants its HTML: the text keeps the
	// document's height.
	if _, h := a2b.bubble.text.SizeRequest(); a2b.bubble.reported && h != a2b.bubble.webHeight {
		t.Errorf("released a2's text is %d tall, the document was %d", h, a2b.bubble.webHeight)
	}
	if h.chatShown || h.layouts.VisibleChildName() != convTimelinePage || h.toggle.Active() {
		t.Fatalf("the timeline did not come back: %q active %v", h.layouts.VisibleChildName(), h.toggle.Active())
	}
	if c := h.timeline.cards["a2"]; c != a2 || !c.folded {
		t.Error("the timeline lost the card the user folded")
	}
	if len(marked) != 1 {
		t.Errorf("a switch marked read again: %v", marked)
	}

	// Another conversation while the chat shows: built in the chat, the
	// hidden timeline only notes it.
	w.settings.SetConversationChatLayout(true)
	run(300 * time.Millisecond)
	fake.selectRow(fake.row("t3", "", false))
	run(500 * time.Millisecond)
	if len(chat.bubbles) != 2 || len(h.timeline.cards) != 0 || h.timeline.stale != convStaleOpen {
		t.Errorf("t3: %d bubbles, %d timeline cards, stale %v", len(chat.bubbles), len(h.timeline.cards), h.timeline.stale)
	}
	w.settings.SetConversationChatLayout(false)
	run(300 * time.Millisecond)
	if len(h.timeline.cards) != 2 {
		t.Errorf("the timeline did not catch up with t3: %d cards", len(h.timeline.cards))
	}
}
