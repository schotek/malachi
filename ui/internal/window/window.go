// Package window builds the main application window from the Blueprint
// definition in data/ui/window.blp and wires it to the RPC client.
//
// The UI is a thin client: it displays what the backend returns and sends
// commands. No mail logic lives here (see CLAUDE.md rule 1).
package window

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/settings"
	"github.com/schotek/malachi/ui/internal/widget"
)

// reconnectInterval is how often the UI retries a dead backend socket.
const reconnectInterval = 5 // seconds

// Window is the main three-pane window.
type Window struct {
	*adw.ApplicationWindow

	app      *adw.Application
	client   *client.Client
	log      *slog.Logger
	settings *settings.Store

	// openMessages tracks stand-alone message windows by message index so a
	// second double-click raises the existing window instead of opening
	// another one. TODO(phase-1): key by api.MessageID.
	openMessages map[int]*MessageWindow

	// rows are the message list rows, kept so appearance settings can be
	// re-applied to them when they change.
	rows []*widget.MessageRow

	outerSplit *adw.NavigationSplitView
	innerSplit *adw.NavigationSplitView
	listPage   *adw.NavigationPage

	folderList  *gtk.ListBox
	messageList *gtk.ListBox
	banner      *adw.Banner

	messageStack   *gtk.Stack
	messageSubject *gtk.Label
	messageFrom    *gtk.Label
	messageBody    *gtk.Label

	connIcon   *gtk.Image
	connStatus *gtk.Label
}

// New builds the window, populates placeholder data and starts connecting
// to the backend. Appearance settings from s are applied now and whenever
// they change.
func New(app *adw.Application, c *client.Client, log *slog.Logger, s *settings.Store) *Window {
	b := gtk.NewBuilderFromString(data.MustUI("window.ui"))

	w := &Window{
		ApplicationWindow: b.GetObject("main_window").Cast().(*adw.ApplicationWindow),
		app:               app,
		client:            c,
		log:               log.With("component", "window"),
		settings:          s,
		openMessages:      make(map[int]*MessageWindow),
		outerSplit:        b.GetObject("outer_split").Cast().(*adw.NavigationSplitView),
		innerSplit:        b.GetObject("inner_split").Cast().(*adw.NavigationSplitView),
		listPage:          b.GetObject("list_page").Cast().(*adw.NavigationPage),
		folderList:        b.GetObject("folder_list").Cast().(*gtk.ListBox),
		messageList:       b.GetObject("message_list").Cast().(*gtk.ListBox),
		banner:            b.GetObject("backend_banner").Cast().(*adw.Banner),
		messageStack:      b.GetObject("message_stack").Cast().(*gtk.Stack),
		messageSubject:    b.GetObject("message_subject").Cast().(*gtk.Label),
		messageFrom:       b.GetObject("message_from").Cast().(*gtk.Label),
		messageBody:       b.GetObject("message_body").Cast().(*gtk.Label),
		connIcon:          b.GetObject("connection_icon").Cast().(*gtk.Image),
		connStatus:        b.GetObject("connection_status").Cast().(*gtk.Label),
	}
	w.SetApplication(&app.Application)

	w.populateFolders()
	w.populateMessages()
	w.messageStack.SetVisibleChildName("empty")

	// Settings callbacks arrive on the main loop; no IdleAdd needed. The main
	// window lives as long as the application, so the handlers are never
	// removed. Body zoom and font are handled globally by internal/style.
	for _, key := range []string{settings.KeyDensity, settings.KeyShowPreviewLine, settings.KeyShowAvatars} {
		s.OnChanged(key, w.applyListAppearance)
	}

	w.folderList.ConnectRowSelected(func(row *gtk.ListBoxRow) {
		if row == nil {
			return
		}
		w.listPage.SetTitle(dummyFolders[row.Index()].Name)
		w.outerSplit.SetShowContent(true)
	})
	w.messageList.ConnectRowSelected(func(row *gtk.ListBoxRow) {
		if row == nil {
			w.messageStack.SetVisibleChildName("empty")
			return
		}
		w.showMessage(dummyMessages[row.Index()])
		w.innerSplit.SetShowContent(true)
	})
	// Fires on double-click or Enter (activate-on-single-click is off).
	w.messageList.ConnectRowActivated(func(row *gtk.ListBoxRow) {
		w.log.Debug("message row activated", "index", row.Index())
		w.openMessageWindow(row.Index())
	})
	w.banner.ConnectButtonClicked(w.reconnect)

	// Client callbacks arrive on a background goroutine; hop to the main loop.
	c.OnStateChange = func(s client.State, err error) {
		glib.IdleAdd(func() { w.showConnectionState(s, err) })
	}
	c.OnNotification = func(method string, params json.RawMessage) {
		glib.IdleAdd(func() {
			// TODO(phase-1): dispatch notify.newMessage / notify.syncState /
			// notify.authRequired to the relevant views.
			w.log.Info("notification", "method", method)
		})
	}

	w.reconnect()
	glib.TimeoutSecondsAdd(reconnectInterval, func() bool {
		if w.client.State() == client.Disconnected {
			w.reconnect()
		}
		return true // keep the timer
	})

	return w
}

func (w *Window) populateFolders() {
	for _, f := range dummyFolders {
		row := adw.NewActionRow()
		row.SetUseMarkup(false) // folder names are untrusted server data
		row.SetTitle(f.Name)
		row.AddPrefix(gtk.NewImageFromIconName(f.Icon))
		if f.Unread > 0 {
			count := gtk.NewLabel(strconv.Itoa(f.Unread))
			count.AddCSSClass("caption")
			count.AddCSSClass("dim-label")
			row.AddSuffix(count)
		}
		w.folderList.Append(row)
	}
	if first := w.folderList.RowAtIndex(0); first != nil {
		w.folderList.SelectRow(first)
	}
}

func (w *Window) populateMessages() {
	for _, m := range dummyMessages {
		row := widget.NewMessageRow()
		row.SetMessage(widget.Message{
			From:    m.From,
			Subject: m.Subject,
			Snippet: m.Snippet,
			Date:    m.Date,
			Unread:  m.Unread,
		})
		w.rows = append(w.rows, row)
		w.messageList.Append(row)
	}
	w.applyListAppearance()
}

// applyListAppearance pushes the current list settings to every row.
func (w *Window) applyListAppearance() {
	compact := w.settings.Density() == settings.DensityCompact
	preview := w.settings.ShowPreviewLine()
	avatars := w.settings.ShowAvatars()
	for _, r := range w.rows {
		r.SetCompact(compact)
		r.SetShowPreview(preview)
		r.SetShowAvatar(avatars)
	}
}

// openMessageWindow opens message idx in its own window, or raises the
// window that already shows it.
func (w *Window) openMessageWindow(idx int) {
	if idx < 0 || idx >= len(dummyMessages) {
		return
	}
	if mw, ok := w.openMessages[idx]; ok {
		mw.Present()
		return
	}
	mw := newMessageWindow(w.app, dummyMessages[idx])
	w.openMessages[idx] = mw
	mw.ConnectCloseRequest(func() bool {
		delete(w.openMessages, idx)
		return false // let the window close
	})
	mw.Present()
}

func (w *Window) showMessage(m dummyMessage) {
	w.messageSubject.SetLabel(m.Subject)
	w.messageFrom.SetLabel(widget.FormatAddress(m.From))
	w.messageBody.SetLabel(m.Body)
	w.messageStack.SetVisibleChildName("message")
}

// reconnect starts a connection attempt off the main loop.
func (w *Window) reconnect() {
	go func() { _ = w.client.Connect() }()
}

// showConnectionState runs on the main loop.
func (w *Window) showConnectionState(s client.State, err error) {
	switch s {
	case client.Connecting:
		w.connIcon.SetFromIconName("network-idle-symbolic")
		w.connStatus.SetLabel("Connecting to backend…")
	case client.Connected:
		w.connIcon.SetFromIconName("network-transmit-receive-symbolic")
		w.connStatus.SetLabel("Connected")
		w.banner.SetRevealed(false)
		go w.fetchSystemInfo()
	default:
		w.connIcon.SetFromIconName("network-offline-symbolic")
		w.connStatus.SetLabel("Backend unavailable")
		w.banner.SetRevealed(true)
		if err != nil {
			// Repeated dial failures while the daemon is down are expected;
			// keep them at debug so the log stays readable.
			w.log.Debug("backend unavailable", "err", err)
		}
	}
}

func (w *Window) fetchSystemInfo() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var info api.SystemInfoResult
	err := w.client.Call(ctx, api.MethodSystemInfo, api.SystemInfoParams{}, &info)
	glib.IdleAdd(func() {
		if err != nil {
			w.connStatus.SetLabel("Connected, but system.info failed")
			w.log.Error("system.info", "err", err)
			return
		}
		if info.ProtocolVersion != api.ProtocolVersion {
			w.connStatus.SetLabel(fmt.Sprintf("Protocol mismatch: UI %d, backend %d",
				api.ProtocolVersion, info.ProtocolVersion))
			return
		}
		w.connStatus.SetLabel(fmt.Sprintf("Connected to malachid %s (pid %d)", info.Version, info.PID))
	})
}
