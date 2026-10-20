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

// Run in a dedicated test process with a display (xvfb-run, Broadway):
// MALACHI_GTK_SMOKE=1 go test ./internal/window -run
// '^TestQuickReplyMessageGTKSmoke$' -count=1 -v. The quick-reply bar of
// the reading pane (docs/plans/chat-view.md §12) in the real main window
// Blueprint (window.blp's reading_column, quick_reply_bar.blp) over
// invented mail: the RPC client is never connected, the settings are a
// memory store, the bar's controller has the fake host of
// conversation_chat_gtk_smoke_test.go and the conversation controller the
// fake window of conversation_controller_test.go. It checks the bar's
// place under message_stack, the bar under a single message (the sender in
// the mode button's tooltip, Ctrl+Enter answering the message shown), the
// text of the thread kept through its conversation in the chat and back
// (the cleared conversation does not hide the bar), the empty page, the
// page of several rows, the Outbox and Drafts without it, Sent with it for
// the user's own message, a message without a thread without it, the
// keyboard leaving a field that hides, and the icon-only rule followed
// from the bar's own width; LAYOUT: lines log the allocations.
func TestQuickReplyMessageGTKSmoke(t *testing.T) {
	if os.Getenv("MALACHI_GTK_SMOKE") != "1" {
		t.Skip("set MALACHI_GTK_SMOKE=1 with a GTK display to run the widget smoke test")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	adw.Init()
	rpc := client.New(filepath.Join(t.TempDir(), "absent.sock"))
	defer rpc.Close()
	st := settings.NewMemory()
	b := data.Builder("window.ui")
	w := &Window{
		ApplicationWindow: b.GetObject("main_window").Cast().(*adw.ApplicationWindow),
		log:               slog.Default(), mode: board.ModeMail,
		client:        rpc,
		settings:      st,
		assist:        NewAssistant(st, slog.Default()),
		actions:       make(map[string]*gio.SimpleAction),
		toasts:        b.GetObject("toast_overlay").Cast().(*adw.ToastOverlay),
		messageStack:  b.GetObject("message_stack").Cast().(*gtk.Stack),
		messageList:   b.GetObject("message_list").Cast().(*gtk.ListBox),
		loaded:        map[api.MessageID]*loadedMessage{},
		findingDrafts: map[api.DraftID]bool{},
		openMessages:  map[api.MessageID]*MessageWindow{},
	}
	defer w.Destroy()
	column := b.GetObject("reading_column").Cast().(*gtk.Box)
	w.attachQuickReply(column)
	w.messageStack.AddNamed(adw.NewStatusPage(), selectionPageName)
	host := newSmokeReplyHost()
	w.useQuickReply(quickreply.New(host))
	bar := w.quickReplyBar
	buf := bar.text.Buffer()

	// The bar is the column's child right under message_stack.
	if p := bar.root.Parent(); p == nil || !coreglib.BaseObject(p).Eq(column) {
		t.Fatal("the bar is not in reading_column")
	}
	if next := w.messageStack.NextSibling(); next == nil || !coreglib.BaseObject(next).Eq(bar.root) {
		t.Fatal("the bar does not follow message_stack")
	}
	if bar.root.Visible() {
		t.Error("the bar shows on the empty page")
	}

	// t1 is bob's a1, alice's a2 and carol's a3 in the inbox and the
	// user's reply r1 in Sent; the window knows the account and its
	// folders.
	fake := newConvWindow(t, append(convMessages(), convReply("r1", 4, "t1")))
	w.model.accounts = fake.accounts
	w.model.folders = map[api.AccountID][]api.Folder{"a": {
		{ID: "in", Role: api.RoleInbox}, {ID: convSentFolder, Role: api.RoleSent},
		{ID: "out", Role: api.RoleOutbox}, {ID: "dr", Role: api.RoleDrafts},
	}}

	w.SetDefaultSize(1200, 800)
	w.Present()
	ctx := glib.MainContextDefault()
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
	run(300 * time.Millisecond)
	inBar := func() bool {
		f := w.Focus()
		return f != nil && (coreglib.BaseObject(f).Eq(bar.root) || gtk.BaseWidget(f).IsAncestor(bar.root))
	}

	// A single message: the bar under it, carol named, the stack shorter
	// by the bar.
	a3 := fake.messages["a3"]
	w.showPaneMessage(a3)
	run(300 * time.Millisecond)
	if !bar.root.Visible() {
		t.Fatal("the bar does not show under a single message")
	}
	s := w.quickReply.State()
	if s.Target.Message.ID != "a3" || s.Account != "a" || s.Thread != "t1" || len(s.Modes) != 3 {
		t.Errorf("state: target %q, %q/%q, modes %v", s.Target.Message.ID, s.Account, s.Thread, s.Modes)
	}
	if got, want := bar.mode.TooltipText(), conversation.QuickReplyTargetTooltip(conversation.QuickReply, "carol", i18n.Tr); got != want {
		t.Errorf("mode tooltip %q, want %q", got, want)
	}
	if bar.root.HasCSSClass("conversation-pane") {
		t.Error("the bar under a message has the conversation's background")
	}
	t.Logf("LAYOUT: message column=%dx%d stack=%d bar=%dx%d iconOnly=%v", column.Width(), column.Height(),
		w.messageStack.Height(), bar.root.Width(), bar.root.Height(), bar.iconOnly)
	if d := column.Height() - w.messageStack.Height() - bar.root.Height(); bar.root.Height() <= 0 || d < -1 || d > 1 {
		t.Errorf("the stack and the bar do not share the column: %d - %d - %d", column.Height(), w.messageStack.Height(), bar.root.Height())
	}
	// The icon-only rule follows the bar's own width (the field's
	// adjustment reported its allocation).
	if want := convQuickReplyIconOnly(float64(bar.root.Width())); bar.iconOnly != want {
		t.Errorf("icon only %v at %d px, want %v", bar.iconOnly, bar.root.Width(), want)
	}

	// Ctrl+Enter answers the message shown.
	buf.SetText("Thanks")
	run(20 * time.Millisecond)
	if !bar.key(gdk.KEY_Return, gdk.ControlMask) {
		t.Error("Ctrl+Enter was not used")
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
	if create := calls[0].params.(api.DraftCreateParams); create.MessageID != "a3" || create.AccountID != "a" || create.Mode != api.ComposeReply {
		t.Errorf("draft.create %+v", create)
	}
	if bar.textOf() != "" {
		t.Errorf("after sending: %q", bar.textOf())
	}

	// The text of the thread goes with its conversation in the chat
	// (showConversation's order: the page, then the conversation) and back.
	buf.SetText("Kept text")
	run(20 * time.Millisecond)
	st.SetConversationChatLayout(true)
	h := w.conversationPane()
	h.markRead = func(api.MessageID) {}
	fake.ctrl = h.ctrl
	h.ctrl.host = fake
	w.messageStack.SetVisibleChildName(convPageName)
	if !bar.root.Visible() || bar.textOf() != "Kept text" {
		t.Errorf("the page's switch moved the bar: shown %v, %q", bar.root.Visible(), bar.textOf())
	}
	fake.selectRow(fake.row("t1", "", false))
	run(500 * time.Millisecond)
	s = w.quickReply.State()
	if !h.chatShown || !bar.root.Visible() || bar.textOf() != "Kept text" || s.Target.Message.ID != "a3" {
		t.Errorf("chat: shown %v/%v, %q, target %q", h.chatShown, bar.root.Visible(), bar.textOf(), s.Target.Message.ID)
	}
	if !bar.root.HasCSSClass("conversation-pane") {
		t.Error("the bar under the chat lacks the conversation's background")
	}
	// Back to the message: the cleared conversation leaves the bar alone,
	// the message shows it with the text.
	w.hideConversation()
	if !bar.root.Visible() || bar.textOf() != "Kept text" {
		t.Errorf("the cleared conversation moved the bar: shown %v, %q", bar.root.Visible(), bar.textOf())
	}
	w.showPaneMessage(a3)
	run(100 * time.Millisecond)
	if !bar.root.Visible() || bar.textOf() != "Kept text" || bar.root.HasCSSClass("conversation-pane") {
		t.Errorf("back to the message: shown %v, %q", bar.root.Visible(), bar.textOf())
	}

	// The keyboard leaves a field that hides, on another page or under a
	// message without the bar.
	bar.text.GrabFocus()
	run(20 * time.Millisecond)
	if !inBar() {
		t.Errorf("the field did not take the keyboard: %T", w.Focus())
	}
	w.messageStack.SetVisibleChildName("empty")
	run(50 * time.Millisecond)
	if bar.root.Visible() || inBar() {
		t.Errorf("empty page: shown %v, keyboard in the bar %v", bar.root.Visible(), inBar())
	}
	w.showPaneMessage(a3)
	run(50 * time.Millisecond)
	bar.text.GrabFocus()
	run(20 * time.Millisecond)
	outbox := a3
	outbox.ID, outbox.FolderID = "o1", "out"
	w.showPaneMessage(outbox)
	run(50 * time.Millisecond)
	if bar.root.Visible() || inBar() {
		t.Errorf("outbox: shown %v, keyboard in the bar %v", bar.root.Visible(), inBar())
	}
	t.Logf("LAYOUT: hidden column=%d stack=%d focus=%T", column.Height(), w.messageStack.Height(), w.Focus())
	if column.Height() != w.messageStack.Height() {
		t.Errorf("the hidden bar takes room: %d of %d", column.Height()-w.messageStack.Height(), column.Height())
	}

	// The page of several rows has no bar.
	w.showPaneMessage(a3)
	run(20 * time.Millisecond)
	w.messageStack.SetVisibleChildName(selectionPageName)
	run(20 * time.Millisecond)
	if bar.root.Visible() {
		t.Error("the bar shows with several rows selected")
	}

	// Drafts: none; Sent: the user's own message answered; no thread: none.
	draft := a3
	draft.ID, draft.FolderID = "d1", "dr"
	w.showPaneMessage(draft)
	run(20 * time.Millisecond)
	if bar.root.Visible() {
		t.Error("the bar shows under a message of Drafts")
	}
	w.showPaneMessage(fake.messages["r1"])
	run(20 * time.Millisecond)
	if s := w.quickReply.State(); !bar.root.Visible() || s.Target.Message.ID != "r1" || !s.Target.Mine {
		t.Errorf("sent: shown %v, target %q mine %v", bar.root.Visible(), s.Target.Message.ID, s.Target.Mine)
	}
	if bar.textOf() != "Kept text" {
		t.Errorf("the thread's text did not follow to r1: %q", bar.textOf())
	}
	lone := a3
	lone.ID, lone.ThreadID = "l1", ""
	w.showPaneMessage(lone)
	run(20 * time.Millisecond)
	if bar.root.Visible() {
		t.Error("the bar shows under a message without a thread")
	}
}
