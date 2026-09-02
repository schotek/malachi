package window

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/data"
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
}

// newMessageWindow creates the window and fills it with m. The window is
// owned by app (so the application stays alive while it is open) but is not
// transient for the main window: the user asked for independent windows.
func newMessageWindow(app *adw.Application, m dummyMessage) *MessageWindow {
	b := gtk.NewBuilderFromString(data.MustUI("message_window.ui"))

	w := &MessageWindow{
		Window:  b.GetObject("message_window").Cast().(*adw.Window),
		title:   b.GetObject("window_title").Cast().(*adw.WindowTitle),
		subject: b.GetObject("message_subject").Cast().(*gtk.Label),
		from:    b.GetObject("message_from").Cast().(*gtk.Label),
		body:    b.GetObject("message_body").Cast().(*gtk.Label),
	}
	w.SetApplication(&app.Application)
	w.show(m)
	return w
}

func (w *MessageWindow) show(m dummyMessage) {
	// Subject and sender are untrusted data: plain labels, no markup.
	w.title.SetTitle(m.Subject)
	w.title.SetSubtitle(m.From)
	w.subject.SetLabel(m.Subject)
	w.from.SetLabel(m.From)
	w.body.SetLabel(m.Body)
}
