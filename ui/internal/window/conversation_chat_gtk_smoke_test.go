// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/conversation"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/quickreply"
	"github.com/schotek/malachi/ui/internal/settings"
)

// smokeReplyHost is the quick-reply bar's host in the smoke test: the
// calls go to a fake caller of their own (draft.create, draft.save and
// message.send answer by method name; Send calls it off the main loop, so
// behind a lock), the answers come back on GTK's main loop, and the
// compose window is a record (OpenCompose opens at once).
type smokeReplyHost struct {
	mu     sync.Mutex
	caller *convCaller

	opened []smokeOpened
	sent   int
	failed []error
}

type smokeOpened struct {
	mode    conversation.QuickReplyMode
	id      api.MessageID
	ownHTML string
}

func newSmokeReplyHost() *smokeReplyHost {
	return &smokeReplyHost{caller: &convCaller{handlers: map[string]func(any) (any, error){
		api.MethodDraftCreate: func(p any) (any, error) {
			params := p.(api.DraftCreateParams)
			return api.DraftCreateResult{Draft: api.Draft{
				ID: "q1", AccountID: params.AccountID, Version: 1,
				To:      []api.Address{{Name: "carol", Address: "carol@example.invalid"}},
				Subject: "Re: s-a3", InReplyTo: params.MessageID,
				HTMLBody: "<div>On Monday, carol wrote:</div><blockquote>the original</blockquote>",
			}, Quoted: api.QuoteHTML}, nil
		},
		api.MethodDraftSave: func(p any) (any, error) {
			return api.DraftSaveResult{DraftID: p.(api.DraftSaveParams).Draft.ID, Version: 2}, nil
		},
		api.MethodMessageSend: func(any) (any, error) {
			return api.MessageSendResult{OutboxID: "o1"}, nil
		},
	}}}
}

func (h *smokeReplyHost) Call(ctx context.Context, method string, params, result any) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.caller.Call(ctx, method, params, result)
}

// calls are the calls so far.
func (h *smokeReplyHost) calls() []convCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]convCall(nil), h.caller.calls...)
}

func (*smokeReplyHost) Post(f func()) { glib.IdleAdd(f) }

func (*smokeReplyHost) Attribution(conversation.QuickReplyMode, api.MessageID) string {
	return "On Monday, carol wrote:"
}

func (*smokeReplyHost) BeforeCreate(_ api.MessageID, done func()) { done() }

func (h *smokeReplyHost) OpenCompose(mode conversation.QuickReplyMode, id api.MessageID, ownHTML string, opened func()) {
	h.opened = append(h.opened, smokeOpened{mode, id, ownHTML})
	opened()
}

func (h *smokeReplyHost) Sent(bool)        { h.sent++ }
func (h *smokeReplyHost) Failed(err error) { h.failed = append(h.failed, err) }

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
// switches back to find the fold kept; LAYOUT: lines log the allocations.
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

	// The reading pane's column: the pages over the quick-reply bar, as
	// window.blp's reading_column holds them.
	column := gtk.NewBox(gtk.OrientationVertical, 0)
	column.SetHExpand(true)
	w.messageStack.SetVExpand(true)
	column.Append(w.messageStack)
	w.attachQuickReply(column)

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
	// The quick-reply bar over a fake host (the window's client is never
	// connected).
	replyHost := newSmokeReplyHost()
	w.useQuickReply(quickreply.New(replyHost))
	bar := w.quickReplyBar

	// The list beside the reading pane, so that it is on screen (Escape in
	// the quick-reply field gives it the keyboard).
	w.messageList.Append(gtk.NewLabel("row"))
	w.messageList.SetSizeRequest(200, -1)
	content := gtk.NewBox(gtk.OrientationHorizontal, 0)
	content.Append(w.messageList)
	content.Append(column)
	w.SetContent(content)
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
	if bar.root.Visible() {
		t.Error("the quick-reply bar shows in the timeline")
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

	quickReplySmoke(t, w, replyHost, run)

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

	// Back to the timeline: the fold kept, the chat lets go.
	w.settings.SetConversationChatLayout(false)
	run(500 * time.Millisecond)
	if h.chatShown || h.layouts.VisibleChildName() != convTimelinePage || h.toggle.Active() {
		t.Fatalf("the timeline did not come back: %q active %v", h.layouts.VisibleChildName(), h.toggle.Active())
	}
	if bar.root.Visible() {
		t.Error("the quick-reply bar stays in the timeline")
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

// quickReplySmoke checks the quick-reply bar under t1 shown as chat: shown,
// its mode button (Reply, a tooltip naming carol, the menu's modes),
// typing enables Send, Ctrl+Enter sends through draft.create, draft.save
// and message.send while the field refuses edits, the text clears; a blank
// text sends nothing; Reply All from the menu; Open in a Window and
// Forward… move the text into the compose window; Escape gives the
// keyboard to the list only while it is on screen; the Assistant button
// shows as the Assistant does.
func quickReplySmoke(t *testing.T, w *Window, host *smokeReplyHost, run func(time.Duration)) {
	t.Helper()
	bar := w.quickReplyBar
	buf := bar.text.Buffer()
	if !bar.root.Visible() {
		t.Fatal("the quick-reply bar does not show in the chat")
	}
	if !bar.root.HasCSSClass("conversation-pane") {
		t.Error("the bar under the chat lacks the conversation's background")
	}
	st := w.quickReply.State()
	if st.Target.Message.ID != "a3" || st.Mode != conversation.QuickReply || len(st.Modes) != 3 {
		t.Errorf("state: target %q mode %v modes %v", st.Target.Message.ID, st.Mode, st.Modes)
	}
	if got, want := bar.modeContent.Label(), i18n.T("Reply"); got != want && !bar.iconOnly {
		t.Errorf("mode label %q, want %q", got, want)
	}
	if got, want := bar.mode.TooltipText(), conversation.QuickReplyTargetTooltip(conversation.QuickReply, "carol", i18n.Tr); got != want {
		t.Errorf("mode tooltip %q, want %q", got, want)
	}
	if bar.mode.MenuModel() == nil || bar.comment.Visible() {
		t.Error("the mode button has no menu, or the comment label shows")
	}
	if !bar.placeholder.Visible() || bar.placeholder.Text() != conversation.QuickReplyPlaceholder(false, i18n.Tr) {
		t.Errorf("placeholder %q shown %v", bar.placeholder.Text(), bar.placeholder.Visible())
	}
	if bar.send.Sensitive() {
		t.Error("Send is enabled with nothing typed")
	}
	t.Logf("LAYOUT: reply bar=%dx%d field=%dx%d iconOnly=%v", bar.root.Width(), bar.root.Height(),
		bar.text.Width(), bar.text.Height(), bar.iconOnly)

	// Typing (the buffer's own change, as a key would make it).
	buf.SetText("Thanks, see you\nat <five>")
	run(50 * time.Millisecond)
	if !bar.send.Sensitive() || bar.placeholder.Visible() || w.quickReply.State().Text != "Thanks, see you\nat <five>" {
		t.Errorf("typing: Send %v, placeholder %v, text %q", bar.send.Sensitive(), bar.placeholder.Visible(), w.quickReply.State().Text)
	}
	// Return alone is the field's (a new line).
	if bar.key(gdk.KEY_Return, gdk.NoModifierMask) {
		t.Error("Return was taken from the field")
	}

	// Ctrl+Enter sends; while it is on its way the field refuses edits.
	if !bar.key(gdk.KEY_Return, gdk.ControlMask) {
		t.Error("Ctrl+Enter was not used")
	}
	if !w.quickReply.State().Sending || bar.send.Sensitive() || bar.open.Sensitive() {
		t.Errorf("sending: %v, Send %v, Open %v", w.quickReply.State().Sending, bar.send.Sensitive(), bar.open.Sensitive())
	}
	buf.InsertAtCursor("zzz")
	if got := bar.textOf(); got != "Thanks, see you\nat <five>" {
		t.Errorf("the field took an edit while sending: %q", got)
	}
	for end := time.Now().Add(3 * time.Second); host.sent == 0 && len(host.failed) == 0 && time.Now().Before(end); {
		run(20 * time.Millisecond)
	}
	if host.sent != 1 || len(host.failed) != 0 {
		t.Fatalf("sent %d, failed %v", host.sent, host.failed)
	}
	calls := host.calls()
	var methods []string
	for _, c := range calls {
		methods = append(methods, c.method)
	}
	if strings.Join(methods, " ") != api.MethodDraftCreate+" "+api.MethodDraftSave+" "+api.MethodMessageSend {
		t.Fatalf("calls %v", methods)
	}
	create := calls[0].params.(api.DraftCreateParams)
	if create.AccountID != "a" || create.MessageID != "a3" || create.Mode != api.ComposeReply || create.Attribution == "" {
		t.Errorf("draft.create %+v", create)
	}
	saved := calls[1].params.(api.DraftSaveParams).Draft
	if !strings.HasPrefix(saved.HTMLBody, "<p>Thanks, see you<br>at &lt;five&gt;</p>") ||
		!strings.Contains(saved.HTMLBody, "<blockquote>the original</blockquote>") || saved.ID != "q1" {
		t.Errorf("draft.save body %q", saved.HTMLBody)
	}
	if send := calls[2].params.(api.MessageSendParams); send.DraftID != "q1" || send.Version != 2 || send.AccountID != "a" {
		t.Errorf("message.send %+v", send)
	}
	if bar.textOf() != "" || bar.send.Sensitive() || !bar.placeholder.Visible() || w.quickReply.State().Sending {
		t.Errorf("after sending: text %q, Send %v", bar.textOf(), bar.send.Sensitive())
	}

	// A blank text sends nothing.
	buf.SetText("  \n\t ")
	run(20 * time.Millisecond)
	if bar.send.Sensitive() {
		t.Error("Send is enabled for a blank text")
	}
	if !bar.key(gdk.KEY_KP_Enter, gdk.ControlMask) {
		t.Error("Ctrl+Enter on a blank text was not used")
	}
	run(50 * time.Millisecond)
	if n := len(host.calls()); n != 3 {
		t.Errorf("a blank text called the daemon: %d calls", n)
	}

	// Reply All from the menu, and back.
	bar.modeAction.Activate(glib.NewVariantString(quickReplyModeReplyAll))
	run(20 * time.Millisecond)
	if w.quickReply.State().Mode != conversation.QuickReplyAll ||
		bar.mode.TooltipText() != conversation.QuickReplyTargetTooltip(conversation.QuickReplyAll, "carol", i18n.Tr) {
		t.Errorf("Reply All: mode %v, tooltip %q", w.quickReply.State().Mode, bar.mode.TooltipText())
	}
	bar.modeAction.Activate(glib.NewVariantString(quickReplyModeReply))
	run(20 * time.Millisecond)

	// Open in a Window: the compose window with the text as the user's
	// own; the bar empties once it opened.
	buf.SetText("Longer text")
	run(20 * time.Millisecond)
	bar.open.Emit("clicked")
	run(20 * time.Millisecond)
	if len(host.opened) != 1 || host.opened[0] != (smokeOpened{conversation.QuickReply, "a3", "<p>Longer text</p>"}) {
		t.Errorf("opened %+v", host.opened)
	}
	if bar.textOf() != "" {
		t.Errorf("Open in a Window left %q", bar.textOf())
	}
	// Forward… from the menu.
	buf.SetText("FYI")
	run(20 * time.Millisecond)
	bar.forwardAction.Activate(nil)
	run(20 * time.Millisecond)
	if len(host.opened) != 2 || host.opened[1] != (smokeOpened{conversation.QuickForward, "a3", "<p>FYI</p>"}) {
		t.Errorf("forwarded %+v", host.opened)
	}

	// Escape: the list takes the keyboard while it is on screen.
	inList := func() bool {
		f := w.Focus()
		if f == nil {
			return false
		}
		return coreglib.BaseObject(f).Eq(w.messageList) || gtk.BaseWidget(f).IsAncestor(w.messageList)
	}
	bar.text.GrabFocus()
	run(20 * time.Millisecond)
	if !bar.key(gdk.KEY_Escape, gdk.NoModifierMask) {
		t.Error("Escape was not used with the list on screen")
	}
	run(20 * time.Millisecond)
	if !inList() {
		t.Errorf("Escape did not focus the list: %T", w.Focus())
	}
	w.messageList.SetVisible(false)
	run(50 * time.Millisecond)
	bar.text.GrabFocus()
	if bar.key(gdk.KEY_Escape, gdk.NoModifierMask) {
		t.Error("Escape was used with the list hidden")
	}
	w.messageList.SetVisible(true)
	run(50 * time.Millisecond)

	// The Assistant button shows as the Assistant does (the toolbar's
	// binding).
	if bar.assistant.Visible() != w.assist.shown() {
		t.Errorf("Assistant button %v, Assistant shown %v", bar.assistant.Visible(), w.assist.shown())
	}
	w.settings.SetAssistantMenu(true)
	w.assist.status = ptr(statusWith(true, true))
	w.assist.notify()
	run(20 * time.Millisecond)
	if !w.assist.shown() || !bar.assistant.Visible() || bar.assistant.IconName() != assistantIcon {
		t.Errorf("Assistant shown %v, button %v %q", w.assist.shown(), bar.assistant.Visible(), bar.assistant.IconName())
	}
	w.settings.SetAssistantMenu(false)
	w.assist.notify()
	run(20 * time.Millisecond)
	if w.assist.shown() || bar.assistant.Visible() {
		t.Errorf("Assistant hidden: shown %v, button %v", w.assist.shown(), bar.assistant.Visible())
	}
}
