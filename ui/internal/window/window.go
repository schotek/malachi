// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

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
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/compose"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settings"
	"github.com/schotek/malachi/ui/internal/sound"
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
	compose  *compose.Manager

	// model caches what the backend returned; the widgets are built from it.
	model mailModel

	// rows are the message list rows by message ID, kept so appearance
	// settings and flag changes can be pushed to them.
	rows map[api.MessageID]*widget.MessageRow

	// folderRows are the sidebar rows by folder (header rows are not kept).
	folderRows map[folderKey]*folderRow

	// reselecting is set while Go code selects or removes list rows itself
	// (rebuilds, restoring the selection); the row-selected handlers ignore
	// those signals so they only react to the user.
	reselecting bool

	// savingCollapse is set while this window writes the sidebar's fold
	// state, so it does not treat its own settings change as somebody else's.
	savingCollapse bool

	// loaded caches message.get / message.body results (bounded; see
	// message_view.go).
	loaded map[api.MessageID]*loadedMessage

	// openMessages tracks stand-alone message windows so a second
	// double-click raises the existing window instead of opening another.
	openMessages map[api.MessageID]*MessageWindow

	// markReadSource is the pending mark-as-read timer, 0 when none;
	// markReadID is the message it will mark.
	markReadSource glib.SourceHandle
	markReadID     api.MessageID

	// hasAccounts mirrors account.list. It starts true: "unknown" must not
	// show the No Accounts page before the daemon has answered.
	hasAccounts bool

	// syncStates is the last notify.syncState / sync.status per account.
	syncStates map[api.AccountID]api.SyncState
	// outboxSeen is the last known size of each account's outbox folder and
	// outboxCancelled the drops the user caused (outbox.go: sent toast).
	outboxSeen      map[api.AccountID]int
	outboxCancelled map[api.AccountID]int

	// authBannerAccount is the account auth_banner is shown for, empty when
	// the banner is hidden.
	authBannerAccount api.AccountID
	// authBannerGraph says the banner's account signs in through GNOME
	// Online Accounts (the button opens that panel).
	authBannerGraph bool

	// actions are the win.* actions by name (without the prefix).
	actions map[string]*gio.SimpleAction

	outerSplit *adw.NavigationSplitView
	innerSplit *adw.NavigationSplitView
	listPage   *adw.NavigationPage

	folderStack      *gtk.Stack
	folderList       *gtk.ListBox
	folderStatusPage *adw.StatusPage
	syncSpinner      *adw.Spinner
	syncLabel        *gtk.Label
	connIcon         *gtk.Image
	connStatus       *gtk.Label

	refreshButton   *gtk.Button
	searchButton    *gtk.ToggleButton
	banner          *adw.Banner
	authBanner      *adw.Banner
	listStack       *gtk.Stack
	listScroller    *gtk.ScrolledWindow
	messageList     *gtk.ListBox
	loadMoreButton  *gtk.Button
	loadMoreSpinner *adw.Spinner
	listStatusPage  *adw.StatusPage
	listRetryButton *gtk.Button

	toasts         *adw.ToastOverlay
	messageStack   *gtk.Stack
	pane           *messageView // the message pane's display (message_view.go)
	starButton     *gtk.ToggleButton
	archiveButton  *gtk.Button
	junkButton     *gtk.Button
	trashButton    *gtk.Button
	messageMenu    *gtk.MenuButton
	replyButton    *gtk.Button
	replyAllButton *gtk.Button
	forwardButton  *gtk.Button
	outboxBanner   *adw.Banner
}

// New builds the window, registers its actions and starts connecting to
// the backend. Settings from s are applied now and whenever they change.
func New(app *adw.Application, c *client.Client, log *slog.Logger, s *settings.Store, cm *compose.Manager) *Window {
	b := data.Builder("window.ui")

	w := &Window{
		ApplicationWindow: b.GetObject("main_window").Cast().(*adw.ApplicationWindow),
		app:               app,
		client:            c,
		log:               log.With("component", "window"),
		settings:          s,
		compose:           cm,
		hasAccounts:       true,
		rows:              make(map[api.MessageID]*widget.MessageRow),
		folderRows:        make(map[folderKey]*folderRow),
		loaded:            make(map[api.MessageID]*loadedMessage),
		openMessages:      make(map[api.MessageID]*MessageWindow),
		syncStates:        make(map[api.AccountID]api.SyncState),
		actions:           make(map[string]*gio.SimpleAction),

		outerSplit: b.GetObject("outer_split").Cast().(*adw.NavigationSplitView),
		innerSplit: b.GetObject("inner_split").Cast().(*adw.NavigationSplitView),
		listPage:   b.GetObject("list_page").Cast().(*adw.NavigationPage),

		folderStack:      b.GetObject("folder_stack").Cast().(*gtk.Stack),
		folderList:       b.GetObject("folder_list").Cast().(*gtk.ListBox),
		folderStatusPage: b.GetObject("folder_status_page").Cast().(*adw.StatusPage),
		syncSpinner:      b.GetObject("sync_spinner").Cast().(*adw.Spinner),
		syncLabel:        b.GetObject("sync_label").Cast().(*gtk.Label),
		connIcon:         b.GetObject("connection_icon").Cast().(*gtk.Image),
		connStatus:       b.GetObject("connection_status").Cast().(*gtk.Label),

		refreshButton:   b.GetObject("refresh_button").Cast().(*gtk.Button),
		searchButton:    b.GetObject("search_button").Cast().(*gtk.ToggleButton),
		banner:          b.GetObject("backend_banner").Cast().(*adw.Banner),
		authBanner:      b.GetObject("auth_banner").Cast().(*adw.Banner),
		listStack:       b.GetObject("list_stack").Cast().(*gtk.Stack),
		listScroller:    b.GetObject("list_scroller").Cast().(*gtk.ScrolledWindow),
		messageList:     b.GetObject("message_list").Cast().(*gtk.ListBox),
		loadMoreButton:  b.GetObject("load_more_button").Cast().(*gtk.Button),
		loadMoreSpinner: b.GetObject("load_more_spinner").Cast().(*adw.Spinner),
		listStatusPage:  b.GetObject("list_status_page").Cast().(*adw.StatusPage),
		listRetryButton: b.GetObject("list_retry_button").Cast().(*gtk.Button),

		toasts:         b.GetObject("toast_overlay").Cast().(*adw.ToastOverlay),
		messageStack:   b.GetObject("message_stack").Cast().(*gtk.Stack),
		starButton:     b.GetObject("star_button").Cast().(*gtk.ToggleButton),
		archiveButton:  b.GetObject("archive_button").Cast().(*gtk.Button),
		junkButton:     b.GetObject("junk_button").Cast().(*gtk.Button),
		trashButton:    b.GetObject("trash_button").Cast().(*gtk.Button),
		messageMenu:    b.GetObject("message_menu").Cast().(*gtk.MenuButton),
		replyButton:    b.GetObject("reply_button").Cast().(*gtk.Button),
		replyAllButton: b.GetObject("reply_all_button").Cast().(*gtk.Button),
		forwardButton:  b.GetObject("forward_button").Cast().(*gtk.Button),
		outboxBanner:   b.GetObject("outbox_banner").Cast().(*adw.Banner),
	}
	w.SetApplication(&app.Application)
	w.pane = newMessageView(w, &w.ApplicationWindow.Window, b)
	w.pane.banner.ConnectButtonClicked(func() {
		if s, ok := w.selectedMessage(); ok {
			w.loadRemoteImages(s.ID)
		}
	})
	w.registerActions()
	w.messageStack.SetVisibleChildName(w.emptyPageName())
	// The HTML views scale with the text-zoom setting; the plain-text label
	// follows it through internal/style.
	s.OnChanged(settings.KeyTextZoom, func() {
		z := s.TextZoom()
		w.pane.setZoom(z)
		for _, mw := range w.openMessages {
			mw.view.setZoom(z)
		}
	})

	// Settings callbacks arrive on the main loop; no IdleAdd needed. The main
	// window lives as long as the application, so the handlers are never
	// removed. Body zoom and font are handled globally by internal/style.
	for _, key := range []string{settings.KeyDensity, settings.KeyShowPreviewLine, settings.KeyShowAvatars} {
		s.OnChanged(key, w.applyListAppearance)
	}

	// "Run in Background": closing hides the window instead of destroying
	// it. A hidden window still keeps the GtkApplication alive, so no
	// explicit hold is needed; app.show / activation presents it again.
	w.ConnectCloseRequest(func() bool {
		if !w.settings.RunInBackground() {
			return false // destroy; the application exits with its last window
		}
		w.SetVisible(false)
		return true
	})

	// Sidebar rows mirror model.entries one to one (rebuildFolderList).
	// Programmatic selection (w.reselecting) is handled by selectFolder
	// itself and must not navigate a collapsed split view to the list.
	// The folded-away parts of the sidebar are restored from the last
	// session, and follow along when another window folds something.
	w.model.collapsed = loadCollapse(s)
	for _, key := range []string{settings.KeyCollapsedFolders, settings.KeyCollapsedAccounts} {
		s.OnChanged(key, w.onCollapseChanged)
	}

	w.folderList.ConnectRowSelected(func(row *gtk.ListBoxRow) {
		if row == nil || w.reselecting {
			return
		}
		idx := row.Index()
		if idx < 0 || idx >= len(w.model.entries) {
			return
		}
		e := w.model.entries[idx]
		if e.Header {
			return
		}
		w.selectFolder(folderKey{Account: e.Account.ID, Folder: e.Folder.ID})
		w.outerSplit.SetShowContent(true)
	})

	// List rows mirror model.messages one to one (rebuildMessageRows).
	w.messageList.ConnectRowSelected(func(row *gtk.ListBoxRow) {
		if !w.reselecting {
			w.onMessageRowSelected(row)
		}
	})
	// Fires on double-click or Enter (activate-on-single-click is off).
	w.messageList.ConnectRowActivated(func(row *gtk.ListBoxRow) {
		if s, ok := w.model.messageAt(row.Index()); ok {
			w.openMessageWindow(s.ID)
		}
	})
	w.loadMoreButton.ConnectClicked(w.loadMore)
	w.listScroller.ConnectEdgeReached(func(pos gtk.PositionType) {
		if pos == gtk.PosBottom {
			w.loadMore()
		}
	})
	w.listRetryButton.ConnectClicked(w.loadMessages)

	// "clicked" fires for user clicks only, not for SetActive from Go.
	w.starButton.ConnectClicked(func() {
		if s, ok := w.selectedMessage(); ok {
			w.toggleFlagged(s.ID)
		}
	})
	for _, r := range []struct {
		b    *gtk.Button
		kind compose.Kind
	}{{w.replyButton, compose.KindReply}, {w.replyAllButton, compose.KindReplyAll}, {w.forwardButton, compose.KindForward}} {
		r := r
		r.b.ConnectClicked(func() {
			if s, ok := w.selectedMessage(); ok {
				w.openCompose(r.kind, s.ID)
			}
		})
	}
	w.banner.ConnectButtonClicked(w.reconnect)
	w.authBanner.ConnectButtonClicked(w.onAuthBannerButton)
	// The only button the outbox banner ever has is Retry (outbox.go).
	w.outboxBanner.ConnectButtonClicked(func() {
		if s, ok := w.selectedMessage(); ok {
			w.retryOutbox(s.ID)
		}
	})

	// Client callbacks arrive on a background goroutine; hop to the main loop.
	c.OnStateChange = func(s client.State, err error) {
		glib.IdleAdd(func() { w.showConnectionState(s, err) })
	}
	c.OnNotification = func(method string, params json.RawMessage) {
		glib.IdleAdd(func() { w.handleNotification(method, params) })
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

// onMessageRowSelected shows the message behind row in the pane, or the
// placeholder when row is nil (selection cleared). The list code calls it
// directly when it changes the selection on the user's behalf.
func (w *Window) onMessageRowSelected(row *gtk.ListBoxRow) {
	if row == nil {
		w.messageStack.SetVisibleChildName(w.emptyPageName())
		w.outboxBanner.SetRevealed(false)
		w.setMessageActionsSensitive(false)
		w.scheduleMarkRead("")
		return
	}
	s, ok := w.model.messageAt(row.Index())
	if !ok {
		return
	}
	w.showMessage(s.ID)
	w.setMessageActionsSensitive(true)
	w.innerSplit.SetShowContent(true)
	if w.model.inOutbox(s) {
		w.scheduleMarkRead("") // the daemon refuses flags on outbox messages
	} else {
		w.scheduleMarkRead(s.ID)
	}
}

// registerActions adds the win.* actions. All but refresh start disabled;
// setMessageActionsSensitive enables them while a message is selected.
// Accelerators are assigned in main.go.
func (w *Window) registerActions() {
	forSelected := func(fn func(api.MessageID)) func() {
		return func() {
			if s, ok := w.selectedMessage(); ok {
				fn(s.ID)
			}
		}
	}
	w.addAction("refresh", true, w.triggerSync)
	w.addAction("trash", false, forSelected(w.trash))
	w.addAction("archive", false, forSelected(w.archive))
	w.addAction("junk", false, forSelected(w.junk))
	w.addAction("mark-read", false, forSelected(w.markRead))
	w.addAction("mark-unread", false, forSelected(w.markUnread))
	w.addAction("toggle-flag", false, forSelected(w.toggleFlagged))
	w.addAction("load-images", false, forSelected(w.loadRemoteImages))
	w.addAction("trust-sender", false, forSelected(w.trustSender))
}

// addAction registers one stateless win.<name> action.
func (w *Window) addAction(name string, enabled bool, fn func()) {
	a := gio.NewSimpleAction(name, nil)
	a.SetEnabled(enabled)
	a.ConnectActivate(func(*glib.Variant) { fn() })
	w.AddAction(a)
	w.actions[name] = a
}

// Toast shows a transient message over the message pane.
func (w *Window) Toast(text string) {
	w.toasts.AddToast(widget.PlainToast(text))
}

// ToastFor is Toast with an explicit timeout in seconds (0 = stays until
// dismissed) instead of libadwaita's default 5 s.
func (w *Window) ToastFor(text string, seconds uint) {
	t := widget.PlainToast(text)
	t.SetTimeout(seconds)
	w.toasts.AddToast(t)
}

// playNewMailSound plays the theme's new-mail event; failures are logged
// once at debug level (no sound theme or server is a normal desktop state).
func (w *Window) playNewMailSound() {
	if err := sound.Play("message-new-email", i18n.T("New mail")); err != nil {
		w.log.Debug("notification sound", "err", err)
	}
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
		w.connStatus.SetLabel(i18n.T("Connecting to backend…"))
	case client.Connected:
		w.connIcon.SetFromIconName("network-transmit-receive-symbolic")
		w.connStatus.SetLabel(i18n.T("Connected"))
		w.banner.SetRevealed(false)
		go w.fetchSystemInfo()
		w.loadAccounts()
		w.loadSyncStatus()
	default:
		w.connIcon.SetFromIconName("network-offline-symbolic")
		w.connStatus.SetLabel(i18n.T("Backend unavailable"))
		w.banner.SetRevealed(true)
		// Late replies of in-flight calls are dropped; what is shown stays
		// until the reconnect reloads it.
		w.model.bumpAll()
		w.showLoadMore()
		if err != nil {
			// Repeated dial failures while the daemon is down are expected;
			// keep them at debug so the log stays readable.
			w.log.Debug("backend unavailable", "err", err)
		}
	}
}

// emptyPageName is the message pane's placeholder: the No Accounts call to
// action while the daemon has no account, otherwise the usual empty page.
func (w *Window) emptyPageName() string {
	if !w.hasAccounts {
		return "no-accounts"
	}
	return "empty"
}

// checkAccounts re-runs account.list (loadAccounts, which also toggles the
// No Accounts placeholder). Safe to call from any goroutine.
//
// Deprecated: call loadAccounts on the main loop instead.
func (w *Window) checkAccounts() {
	glib.IdleAdd(w.loadAccounts)
}

func (w *Window) fetchSystemInfo() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var info api.SystemInfoResult
	err := w.client.Call(ctx, api.MethodSystemInfo, api.SystemInfoParams{}, &info)
	glib.IdleAdd(func() {
		if err != nil {
			w.connStatus.SetLabel(i18n.T("Connected, but system.info failed"))
			w.log.Error("system.info", "err", err)
			return
		}
		if info.ProtocolVersion != api.ProtocolVersion {
			w.connStatus.SetLabel(fmt.Sprintf(i18n.T("Protocol mismatch: UI %d, backend %d"),
				api.ProtocolVersion, info.ProtocolVersion))
			return
		}
		w.connStatus.SetLabel(fmt.Sprintf(i18n.T("Connected to malachid %s (pid %d)"), info.Version, info.PID))
	})
}
