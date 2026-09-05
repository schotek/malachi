// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/compose"
)

// MessageWindow shows one message in its own top-level window. It is built
// from data/ui/message_window.blp and, like the main window, only displays
// what it is given. Its actions live in the window-local "msg" group and
// call back into the main window, which owns the model and the RPCs.
type MessageWindow struct {
	*adw.Window

	id api.MessageID

	// closed is set from close-request so late fetch replies are dropped.
	closed bool

	title  *adw.WindowTitle
	view   *messageView
	star   *gtk.ToggleButton
	menu   *gtk.MenuButton
	toasts *adw.ToastOverlay
	banner *adw.Banner // delivery state of an outbox message (outbox.go)
}

// messageShortcuts are the msg.* accelerators of a message window; the
// main window's win.* equivalents are set in main.go.
var messageShortcuts = []struct{ trigger, action string }{
	{"Delete", "msg.trash"},
	{"a", "msg.archive"},
	{"j", "msg.junk"},
	{"u", "msg.mark-unread"},
	{"s", "msg.toggle-flag"},
}

// newMessageWindow creates the window for message s of the main window w
// and shows what is cached; openMessageWindow fetches the rest. The window
// is owned by the application (so it stays alive while open) but is not
// transient for the main window: the user asked for independent windows.
func newMessageWindow(w *Window, s api.MessageSummary) *MessageWindow {
	b := data.Builder("message_window.ui")
	id := s.ID

	mw := &MessageWindow{
		Window: b.GetObject("message_window").Cast().(*adw.Window),
		id:     id,
		title:  b.GetObject("window_title").Cast().(*adw.WindowTitle),
		star:   b.GetObject("star_button").Cast().(*gtk.ToggleButton),
		menu:   b.GetObject("message_menu").Cast().(*gtk.MenuButton),
		toasts: b.GetObject("toast_overlay").Cast().(*adw.ToastOverlay),
		banner: b.GetObject("outbox_banner").Cast().(*adw.Banner),
	}
	mw.SetApplication(&w.app.Application)
	mw.view = newMessageView(w, &mw.Window.Window, b)
	mw.view.load = func() { w.loadRemoteImages(id) }
	mw.view.trust = func() { w.trustSender(id) }

	// The "msg" action group: the header buttons and the menu bind to it,
	// so their sensitivity follows the actions. Moves and trash close the
	// window through closeMessageWindow once the row is gone. An outbox
	// message takes no flags or moves (the daemon refuses them); its trash
	// button cancels the send.
	outbox := w.model.inOutbox(s)
	g := gio.NewSimpleActionGroup()
	add := func(name string, enabled bool, fn func()) {
		a := gio.NewSimpleAction(name, nil)
		a.SetEnabled(enabled)
		a.ConnectActivate(func(*glib.Variant) { fn() })
		g.AddAction(a)
	}
	add("mark-read", !outbox, func() { w.markRead(id) })
	add("mark-unread", !outbox, func() { w.markUnread(id) })
	add("toggle-flag", !outbox, func() { w.toggleFlagged(id) })
	add("trash", true, func() { w.trashFrom(mw, id) })
	add("archive", !outbox && w.canMoveToRole(s, api.RoleArchive), func() { w.archive(id) })
	add("junk", !outbox && w.canMoveToRole(s, api.RoleJunk), func() { w.junkFrom(mw, id) })
	add("load-images", true, func() { w.loadRemoteImages(id) })
	add("trust-sender", !outbox, func() { w.trustSender(id) })
	mw.InsertActionGroup("msg", g)
	for name, obj := range map[string]string{"trash": "trash_button", "archive": "archive_button", "junk": "junk_button"} {
		b.GetObject(obj).Cast().(*gtk.Button).SetActionName("msg." + name)
	}
	b.GetObject("trash_button").Cast().(*gtk.Button).SetTooltipText(trashTooltip(outbox))
	mw.star.SetSensitive(!outbox)
	// "clicked" fires for user clicks only, not for SetActive from Go.
	mw.star.ConnectClicked(func() { w.toggleFlagged(id) })
	mw.banner.ConnectButtonClicked(func() { w.retryOutbox(id) })
	for _, r := range []struct {
		id   string
		kind compose.Kind
	}{{"reply_button", compose.KindReply}, {"reply_all_button", compose.KindReplyAll}, {"forward_button", compose.KindForward}} {
		r := r
		b.GetObject(r.id).Cast().(*gtk.Button).ConnectClicked(func() { w.openCompose(r.kind, id) })
	}

	// Single-letter shortcuts are safe here: the window has no text entry,
	// and selectable labels do not consume plain keys. Capture phase so the
	// HTML view, when it has the focus, does not see them first.
	sc := gtk.NewShortcutController()
	sc.SetPropagationPhase(gtk.PhaseCapture)
	for _, short := range messageShortcuts {
		sc.AddShortcut(gtk.NewShortcut(gtk.NewShortcutTriggerParseString(short.trigger), gtk.NewNamedAction(short.action)))
	}
	mw.AddController(sc)

	mw.show(s, w.loaded[id])
	return mw
}

// show displays the summary headers and, when lm is not nil, the full
// headers, the body (or the error) and the outbox banner. Everything is
// untrusted data: plain labels, no markup, and the HTML view gets the
// sanitiser's output only. Body zoom and font are applied globally by
// internal/style.
func (mw *MessageWindow) show(s api.MessageSummary, lm *loadedMessage) {
	subject := subjectText(s.Subject)
	var msg *api.Message
	if lm != nil && lm.msg != nil {
		msg = lm.msg
		subject = subjectText(msg.Subject)
	}
	mw.title.SetTitle(subject)
	mw.SetTitle(subject)
	setStar(mw.star, hasFlag(s.Flags, api.FlagFlagged))
	mw.view.render(s, lm)
	renderOutboxBanner(mw.banner, msg)
}
