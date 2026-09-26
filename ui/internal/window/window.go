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
	"github.com/schotek/malachi/ui/internal/signin"
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
	starter  Starter
	log      *slog.Logger
	settings *settings.Store
	compose  *compose.Manager

	// model caches what the backend returned; the widgets are built from it.
	model mailModel

	// rows are the message list rows by key (a message, or a conversation
	// in grouped mode), kept so appearance settings and flag changes can be
	// pushed to them.
	rows map[listKey]*widget.MessageRow

	// folderRows are the sidebar rows by folder and section (header rows
	// are not kept).
	folderRows map[rowKey]*folderRow

	// reselecting is set while Go code selects or removes list rows itself
	// (rebuilds, restoring the selection); the row-selected handlers ignore
	// those signals so they only react to the user.
	reselecting bool

	// savingCollapse and savingFavourites are set while this window writes
	// the sidebar's fold state or its pinned folders, so it does not treat
	// its own settings change as somebody else's.
	savingCollapse   bool
	savingFavourites bool

	// loaded caches message.get / message.body results (bounded; see
	// message_view.go).
	loaded map[api.MessageID]*loadedMessage

	// composing holds the messages whose reply or forward the backend is
	// preparing (compose_open.go), so a second click opens no second
	// window.
	composing map[api.MessageID]bool

	// openMessages tracks stand-alone message windows so a second
	// double-click raises the existing window instead of opening another.
	openMessages map[api.MessageID]*MessageWindow

	// openEmbedded tracks the windows of attached messages (embedded.go),
	// by containing message and part, for the same reason.
	openEmbedded map[embeddedKey]*EmbeddedWindow

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
	// authBannerKind says where the banner's account signs in, and so what
	// the button does: the preferences (a password), Settings → Online
	// Accounts, or the browser (the backend's own sign-in).
	authBannerKind signin.Kind
	// authBannerURL is the notification's authUrl: the sign-in page to
	// open when the daemon cannot start a fresh one.
	authBannerURL string
	// authBannerReason is the notification's reason; with a password
	// account it decides whether the button asks for the password
	// (editsPassword).
	authBannerReason api.ErrorCode
	// certBannerAccount is the account cert_banner is shown for, empty
	// when the banner is hidden.
	certBannerAccount api.AccountID

	// conn is what the status line knows of the connection (statusLineFor).
	conn       connView
	connWarned map[string]bool // handshake refusals already logged at Warn (logConnection)
	// statusRows are the status popover's rows by account, and statusOrder
	// the accounts they were built for, in order: the rows are updated in
	// place and only rebuilt when the accounts change (status.go).
	statusRows  map[api.AccountID]*statusRow
	statusOrder []api.AccountID

	// actions are the win.* actions by name (without the prefix).
	actions map[string]*gio.SimpleAction

	outerSplit *adw.NavigationSplitView
	innerSplit *adw.NavigationSplitView
	listPage   *adw.NavigationPage
	listTitle  *adw.WindowTitle

	folderStack      *gtk.Stack
	folderList       *gtk.ListBox
	folderStatusPage *adw.StatusPage
	statusButton     *gtk.MenuButton
	statusPopover    *gtk.Popover
	statusAccounts   *gtk.ListBox
	statusDaemon     *gtk.Label
	syncSpinner      *adw.Spinner
	syncLabel        *gtk.Label
	connIcon         *gtk.Image

	refreshButton   *gtk.Button
	searchButton    *gtk.ToggleButton
	banner          *adw.Banner
	authBanner      *adw.Banner
	certBanner      *adw.Banner
	messageFilter   *adw.ToggleGroup
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
	draftBanner    *adw.Banner // a message of the Drafts folder (drafts.go)
}

// Starter brings the daemon up before the window dials its socket
// (ui/internal/daemon). Its error only says why no daemon answers; the
// window keeps retrying either way.
type Starter interface {
	Ensure() error
}

// New builds the window, registers its actions and starts connecting to
// the backend, asking starter for a daemon first. Settings from s are
// applied now and whenever they change.
func New(app *adw.Application, c *client.Client, log *slog.Logger, s *settings.Store, cm *compose.Manager, starter Starter) *Window {
	b := data.Builder("window.ui")

	w := &Window{
		ApplicationWindow: b.GetObject("main_window").Cast().(*adw.ApplicationWindow),
		app:               app,
		client:            c,
		starter:           starter,
		log:               log.With("component", "window"),
		settings:          s,
		compose:           cm,
		hasAccounts:       true,
		rows:              make(map[listKey]*widget.MessageRow),
		folderRows:        make(map[rowKey]*folderRow),
		loaded:            make(map[api.MessageID]*loadedMessage),
		composing:         make(map[api.MessageID]bool),
		openMessages:      make(map[api.MessageID]*MessageWindow),
		openEmbedded:      make(map[embeddedKey]*EmbeddedWindow),
		syncStates:        make(map[api.AccountID]api.SyncState),
		actions:           make(map[string]*gio.SimpleAction),
		// Until the client reports a state, the first attempt is underway.
		conn:       connView{State: client.Connecting},
		statusRows: make(map[api.AccountID]*statusRow),

		outerSplit: b.GetObject("outer_split").Cast().(*adw.NavigationSplitView),
		innerSplit: b.GetObject("inner_split").Cast().(*adw.NavigationSplitView),
		listPage:   b.GetObject("list_page").Cast().(*adw.NavigationPage),
		listTitle:  b.GetObject("list_title").Cast().(*adw.WindowTitle),

		folderStack:      b.GetObject("folder_stack").Cast().(*gtk.Stack),
		folderList:       b.GetObject("folder_list").Cast().(*gtk.ListBox),
		folderStatusPage: b.GetObject("folder_status_page").Cast().(*adw.StatusPage),
		statusButton:     b.GetObject("status_button").Cast().(*gtk.MenuButton),
		statusPopover:    b.GetObject("status_popover").Cast().(*gtk.Popover),
		statusAccounts:   b.GetObject("status_accounts").Cast().(*gtk.ListBox),
		statusDaemon:     b.GetObject("status_daemon").Cast().(*gtk.Label),
		syncSpinner:      b.GetObject("sync_spinner").Cast().(*adw.Spinner),
		syncLabel:        b.GetObject("sync_label").Cast().(*gtk.Label),
		connIcon:         b.GetObject("connection_icon").Cast().(*gtk.Image),

		refreshButton:   b.GetObject("refresh_button").Cast().(*gtk.Button),
		searchButton:    b.GetObject("search_button").Cast().(*gtk.ToggleButton),
		banner:          b.GetObject("backend_banner").Cast().(*adw.Banner),
		authBanner:      b.GetObject("auth_banner").Cast().(*adw.Banner),
		certBanner:      b.GetObject("cert_banner").Cast().(*adw.Banner),
		messageFilter:   b.GetObject("message_filter").Cast().(*adw.ToggleGroup),
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
		draftBanner:    b.GetObject("draft_banner").Cast().(*adw.Banner),
	}
	w.SetApplication(&app.Application)
	w.pane = newMessageView(w, &w.ApplicationWindow.Window, b)
	w.pane.load = func() {
		if s, ok := w.selectedMessage(); ok {
			w.loadRemoteImages(s.ID)
		}
	}
	w.pane.trust = func() {
		if s, ok := w.selectedMessage(); ok {
			w.trustSender(s.ID)
		}
	}
	w.pane.toast = w.Toast
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
		for _, ew := range w.openEmbedded {
			ew.view.setZoom(z)
		}
	})

	// Settings callbacks arrive on the main loop; no IdleAdd needed. The main
	// window lives as long as the application, so the handlers are never
	// removed. Body zoom and font are handled globally by internal/style.
	for _, key := range []string{settings.KeyDensity, settings.KeyShowPreviewLine, settings.KeyShowAvatars} {
		s.OnChanged(key, w.applyListAppearance)
	}
	// Grouping is a different listing (thread.list): loadMessages notices
	// the mode change and starts the folder over.
	s.OnChanged(settings.KeyGroupByConversation, w.loadMessages)

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
	// The folded-away parts of the sidebar and the pinned folders are
	// restored from the last session, and follow along when another window
	// folds or pins something.
	w.model.collapsed = loadCollapse(s)
	for _, key := range []string{settings.KeyCollapsedFolders, settings.KeyCollapsedAccounts} {
		s.OnChanged(key, w.onCollapseChanged)
	}
	w.model.favourites = loadFavourites(s)
	s.OnChanged(settings.KeyFavouriteFolders, w.onFavouritesChanged)
	// The filter is deliberately not persisted: a window that came back
	// still hiding most of the mailbox would be read as lost mail.
	w.model.listFilter = api.FilterAll

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
		// Remember which of a pinned folder's two rows was clicked, so the
		// highlight stays on it across rebuilds.
		w.model.selectedFav = e.Favourite
		w.selectFolder(folderKey{Account: e.Account.ID, Folder: e.Folder.ID})
		w.outerSplit.SetShowContent(true)
	})

	// List rows mirror model.messages one to one (rebuildMessageRows).
	w.messageList.ConnectRowSelected(func(row *gtk.ListBoxRow) {
		if !w.reselecting {
			w.onMessageRowSelected(row)
		}
	})
	// Fires on double-click or Enter (activate-on-single-click is off): a
	// conversation row folds or unfolds, a message opens in a window — a
	// draft in the compose window.
	w.messageList.ConnectRowActivated(func(row *gtk.ListBoxRow) {
		if r, ok := w.model.rowAt(row.Index()); ok {
			switch {
			case r.Thread:
				w.toggleThread(r.Key.Thread)
			case w.model.inDrafts(r.Message):
				w.openDraft(r.Message.ID)
			default:
				w.openMessageWindow(r.Message.ID)
			}
		}
	})
	w.loadMoreButton.ConnectClicked(w.loadMore)
	w.listScroller.ConnectEdgeReached(func(pos gtk.PositionType) {
		if pos == gtk.PosBottom {
			w.loadMore()
		}
	})
	w.listRetryButton.ConnectClicked(w.loadMessages)
	// Unlike "clicked" on a button, notify::active-name also fires for a
	// SetActiveName from Go, so setListFilter guards against the reload
	// its own write-back would otherwise trigger.
	w.messageFilter.NotifyProperty("active-name", func() {
		w.setListFilter(api.MessageFilter(w.messageFilter.ActiveName()))
	})

	// "clicked" fires for user clicks only, not for SetActive from Go. On a
	// conversation row the star acts on every member (flagTarget).
	w.starButton.ConnectClicked(func() {
		w.selectedIDs(func(row listRow, ids []api.MessageID) {
			w.setFlaggedIDs(ids, w.model.flagTarget(row))
		})
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
	w.certBanner.ConnectButtonClicked(w.onCertBannerButton)
	// The only button the outbox banner ever has is Retry (outbox.go).
	w.outboxBanner.ConnectButtonClicked(func() {
		if s, ok := w.selectedMessage(); ok {
			w.retryOutbox(s.ID)
		}
	})
	w.draftBanner.ConnectButtonClicked(func() {
		if s, ok := w.selectedMessage(); ok {
			w.openDraft(s.ID)
		}
	})

	// The popover's rows are brought up to date as it opens and, while it
	// is open, with every refresh of the status line (status.go).
	w.statusPopover.ConnectShow(w.refreshStatusPopover)
	// The line names the time of the last check ("Up to date · 15:04"): it
	// is redrawn every minute so that a day later it shows the date.
	glib.TimeoutSecondsAdd(statusRefreshSeconds, func() bool {
		w.refreshSyncLabel()
		return true // keep the timer
	})
	w.refreshSyncLabel()

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
		w.draftBanner.SetRevealed(false)
		w.setMessageActionsSensitive(false)
		w.scheduleMarkRead("")
		return
	}
	r, ok := w.model.rowAt(row.Index())
	if !ok {
		return
	}
	// A conversation row shows (and marks read) its newest folder member.
	s := r.Message
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
	// forRows acts on every message the selected row stands for: one, or
	// all the folder members of a conversation row.
	forRows := func(fn func(listRow, []api.MessageID)) func() {
		return func() { w.selectedIDs(fn) }
	}
	w.addAction("refresh", true, w.triggerSync)
	w.addAction("trash", false, forRows(func(row listRow, ids []api.MessageID) { w.trashIDs(w, ids, rowSubject(row)) }))
	w.addAction("archive", false, forRows(func(_ listRow, ids []api.MessageID) { w.archiveIDs(ids) }))
	w.addAction("junk", false, forRows(func(row listRow, ids []api.MessageID) { w.junkIDs(w, ids, rowSubject(row)) }))
	w.addAction("mark-read", false, forRows(func(_ listRow, ids []api.MessageID) { w.setSeenIDs(ids, true) }))
	w.addAction("mark-unread", false, forRows(func(_ listRow, ids []api.MessageID) { w.setSeenIDs(ids, false) }))
	w.addAction("toggle-flag", false, forRows(func(row listRow, ids []api.MessageID) { w.setFlaggedIDs(ids, w.model.flagTarget(row)) }))
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

// reconnect starts a connection attempt off the main loop: first the
// daemon is brought up (or found running), then the socket is dialled.
// Ensure blocks while a freshly started daemon opens its socket; the
// client's own state guard makes the overlapping attempts of the retry
// timer harmless.
func (w *Window) reconnect() {
	go func() {
		if w.starter != nil {
			if err := w.starter.Ensure(); err != nil {
				// The supervisor logs a daemon's exit itself; what is left
				// here is the backoff and "nothing to start", both routine.
				w.log.Debug("backend not started", "err", err)
			}
		}
		_ = w.client.Connect()
	}()
}

// showConnectionState runs on the main loop. The state is kept for the
// status line (refreshSyncLabel), which names it while there is no
// connection; what system.info and sync.status said is forgotten with
// every change and asked again on connecting. A protocol mismatch stays
// until an attempt ends otherwise (nextConnView); how the attempts end is
// logged (logConnection).
func (w *Window) showConnectionState(s client.State, err error) {
	w.conn = nextConnView(w.conn, s, err)
	w.logConnection(s, err)
	defer w.refreshSyncLabel()
	switch s {
	case client.Connecting:
		// Only the status line tells.
	case client.Connected:
		w.banner.SetRevealed(false)
		go w.fetchSystemInfo()
		w.loadAccounts()
		w.loadSyncStatus()
	default:
		// A daemon of another protocol version does run (the status line
		// names it): the banner saying that none does would be wrong.
		w.banner.SetRevealed(w.conn.Mismatch == 0)
		// Late replies of in-flight calls are dropped; what is shown stays
		// until the reconnect reloads it. A conversation waiting for its
		// members folds back, so no spinner outlives its reply.
		w.model.bumpAll()
		if w.model.grouped {
			w.model.collapseLoading()
			w.syncRows()
		}
		w.showLoadMore()
	}
}

// refreshListTitle shows the selected folder above the message list, with
// its counts (folderCountsText) under the name, or "Messages" while no
// folder is selected. The page takes the name as well: it is the back
// button's tooltip, and the page's accessible name, while the split view is
// collapsed. Called wherever the selection or the cached counts change:
// selectFolder, updateFolderRow and rebuildFolderList.
func (w *Window) refreshListTitle() {
	title, subtitle := i18n.T("Messages"), ""
	if f, ok := w.model.folder(w.model.selected); ok {
		title, subtitle = folderTitle(f), folderCountsText(f)
	}
	w.listPage.SetTitle(title)
	w.listTitle.SetTitle(title)
	w.listTitle.SetSubtitle(subtitle)
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

// fetchSystemInfo runs system.info off the main loop and keeps the answer
// (or its failure) for the status line: a protocol mismatch takes the line
// over, the daemon's version and pid go to the foot of its popover.
func (w *Window) fetchSystemInfo() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var info api.SystemInfoResult
	err := w.client.Call(ctx, api.MethodSystemInfo, api.SystemInfoParams{}, &info)
	glib.IdleAdd(func() {
		if w.conn.State != client.Connected {
			return // the connection this answer was for is gone
		}
		if err != nil {
			w.log.Error("system.info", "err", err)
			w.conn.InfoFailed = true
		} else {
			w.conn.Info = &info
		}
		w.refreshSyncLabel()
	})
}
