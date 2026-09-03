package window

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/compose"
	"github.com/schotek/malachi/ui/internal/widget"
)

// MessageWindow shows one message in its own top-level window. It is built
// from data/ui/message_window.blp and, like the main window, only displays
// what it is given.
type MessageWindow struct {
	*adw.Window

	title   *adw.WindowTitle
	subject *gtk.Label
	from    *gtk.Label
	body    *gtk.Label
	trash   *gtk.Button
	toasts  *adw.ToastOverlay
}

// newMessageWindow creates the window for message idx of the main window
// w. The window is owned by the application (so it stays alive while the
// window is open) but is not transient for the main window: the user asked
// for independent windows.
func newMessageWindow(w *Window, idx int) *MessageWindow {
	b := gtk.NewBuilderFromString(data.MustUI("message_window.ui"))

	mw := &MessageWindow{
		Window:  b.GetObject("message_window").Cast().(*adw.Window),
		title:   b.GetObject("window_title").Cast().(*adw.WindowTitle),
		subject: b.GetObject("message_subject").Cast().(*gtk.Label),
		from:    b.GetObject("message_from").Cast().(*gtk.Label),
		body:    b.GetObject("message_body").Cast().(*gtk.Label),
		trash:   b.GetObject("trash_button").Cast().(*gtk.Button),
		toasts:  b.GetObject("toast_overlay").Cast().(*adw.ToastOverlay),
	}
	mw.SetApplication(&w.app.Application)
	mw.trash.ConnectClicked(func() { w.trashMessage(idx, mw, mw.toasts) })
	for _, r := range []struct {
		id   string
		kind compose.Kind
	}{{"reply_button", compose.KindReply}, {"reply_all_button", compose.KindReplyAll}, {"forward_button", compose.KindForward}} {
		r := r
		b.GetObject(r.id).Cast().(*gtk.Button).ConnectClicked(func() { w.openCompose(r.kind, idx) })
	}
	mw.show(dummyMessages[idx])
	return mw
}

func (mw *MessageWindow) show(m dummyMessage) {
	// Subject and sender are untrusted data: plain labels, no markup.
	// Body zoom and font are applied globally by internal/style.
	from := widget.FormatAddress(m.From)
	mw.title.SetTitle(m.Subject)
	mw.title.SetSubtitle(from)
	mw.subject.SetLabel(m.Subject)
	mw.from.SetLabel(from)
	mw.body.SetLabel(m.Body)
}
