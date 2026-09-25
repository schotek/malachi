// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import os

/// What the application owns once, handed to every window and controller:
/// the daemon connection, the settings, the dialogs, the window registry
/// and the notification fan-out. The counterpart of the values ui/main.go
/// threads through `window.New`.
@MainActor
final class AppState {
    /// Entry points other parts of the application register once they
    /// exist (the compose manager, the preferences window, the wizard, the
    /// sync controller). Nil until wired; the menu items stay disabled.
    struct Hooks {
        /// Opens the Settings window (`app.preferences`).
        var openPreferences: (@MainActor () -> Void)?
        /// Opens the account wizard as a sheet on `window` (`app.add-account`).
        var addAccount: (@MainActor (NSWindow?) -> Void)?
        /// Opens an empty compose window (`app.compose`).
        var composeNew: (@MainActor () -> Void)?
        /// Opens a compose window for a `mailto:` URL (the `open` signal).
        var openMailto: (@MainActor (URL) -> Void)?
        /// Asks the daemon to synchronise now (`win.refresh`).
        var checkForNewMail: (@MainActor () -> Void)?
        /// Opens a compose window prepared by the actions (reply, forward,
        /// a mailto: link inside a message); the compose manager wires it
        /// (compose/manager.go `Open`).
        var openCompose: (@MainActor (ComposeParams) -> Void)?
        /// Brings the compose window editing a draft (from draft.open) to
        /// the front; false when none edits it (compose/manager.go
        /// `FindDraft`).
        var raiseDraft: (@MainActor (Draft) -> Bool)?
    }

    let client: RPCClient
    let connection: ConnectionController
    let settings: Settings
    let paths: Paths
    /// Toasts go to whichever window is key and has a presenter, else to
    /// the main window's.
    let toasts: ToastRouter
    let alerts: any Alerts
    let windows: WindowRegistry
    let notifications: NotificationHub

    var hooks = Hooks()

    /// The main window, for `showMainWindow`; set by the delegate.
    weak var mainWindow: NSWindowController?

    init(client: RPCClient, connection: ConnectionController, settings: Settings, paths: Paths) {
        self.client = client
        self.connection = connection
        self.settings = settings
        self.paths = paths
        toasts = ToastRouter()
        alerts = AppAlerts()
        windows = WindowRegistry()
        notifications = NotificationHub()
        notifications.attach(to: connection)
    }

    /// Presents the main window (a hidden one comes back: "Run in
    /// Background") and brings the application to the front, like the
    /// GTK `app.show` action.
    func showMainWindow() {
        mainWindow?.showWindow(nil)
        mainWindow?.window?.makeKeyAndOrderFront(nil)
        NSApp.activate()
    }
}

/// The `Toasts` the application hands out: it forwards to the presenter
/// of the window that last became key, so a toast from a window-local
/// action lands over the pane the user is looking at; when that window is
/// gone the main window's overlay takes it (window.go `Toast`: the GTK
/// window's overlay, for the application's life). Without either the text
/// is logged and dropped.
@MainActor
final class ToastRouter: Toasts {
    /// The current target; window controllers set it in `windowDidBecomeKey`.
    weak var presenter: (any Toasts)?
    /// The main window's presenter, for when the key window's is gone.
    weak var fallback: (any Toasts)?

    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "toast")

    func show(_ text: String) {
        show(text, seconds: ToastPresenter.defaultSeconds)
    }

    func show(_ text: String, seconds: Int) {
        guard let target = presenter ?? fallback else {
            // Toasts may carry addresses or file names: private in the log.
            log.info("toast without a presenter: \(text, privacy: .private)")
            return
        }
        target.show(text, seconds: seconds)
    }
}

/// Decodes the daemon's notifications and the connection state and fans
/// them out to every interested view (window/notify.go `handleNotification`
/// and window.go `showConnectionState`): the sidebar footer, the banners,
/// the list, the desktop notifications all register here.
@MainActor
final class NotificationHub {
    /// Removes a handler; dropping the token does not.
    @MainActor
    final class Token {
        private var cancelled = false
        private let remove: @MainActor () -> Void

        fileprivate init(remove: @escaping @MainActor () -> Void) {
            self.remove = remove
        }

        func cancel() {
            guard !cancelled else { return }
            cancelled = true
            remove()
        }
    }

    /// The last state the connection reported.
    private(set) var connectionState: ConnectionController.ConnectionState = .connecting

    private var newMessage = HandlerList<NewMessageNotification>()
    private var syncState = HandlerList<SyncState>()
    private var authRequired = HandlerList<AuthRequiredNotification>()
    private var accountsChanged = HandlerList<Void>()
    private var connection = HandlerList<ConnectionController.ConnectionState>()

    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "notify")

    /// Installs the hub as the connection's `onNotification` and `onState`.
    func attach(to c: ConnectionController) {
        connectionState = c.state
        c.onNotification = { [weak self] raw in
            self?.handle(raw)
        }
        c.onState = { [weak self] s in
            guard let self else { return }
            self.connectionState = s
            self.connection.fire(s)
        }
    }

    func addNewMessage(_ f: @escaping @MainActor (NewMessageNotification) -> Void) -> Token {
        newMessage.add(f)
    }

    func addSyncState(_ f: @escaping @MainActor (SyncState) -> Void) -> Token {
        syncState.add(f)
    }

    func addAuthRequired(_ f: @escaping @MainActor (AuthRequiredNotification) -> Void) -> Token {
        authRequired.add(f)
    }

    func addAccountsChanged(_ f: @escaping @MainActor () -> Void) -> Token {
        accountsChanged.add { _ in f() }
    }

    /// Fires on every connection state change, after `connectionState`
    /// was updated. A handler added later does not get the current state;
    /// read `connectionState` for that.
    func addConnectionState(_ f: @escaping @MainActor (ConnectionController.ConnectionState) -> Void) -> Token {
        connection.add(f)
    }

    // MARK: Internals

    private func handle(_ raw: RPCNotification) {
        let n: DaemonNotification
        do {
            n = try DaemonNotification(raw)
        } catch {
            log.warning("bad \(raw.method, privacy: .public) payload: \(String(describing: error), privacy: .public)")
            return
        }
        switch n {
        case .newMessage(let m):
            newMessage.fire(m)
        case .syncState(let s):
            syncState.fire(s)
        case .authRequired(let a):
            authRequired.fire(a)
        case .accountsChanged:
            accountsChanged.fire(())
        case .unknown(let method):
            log.info("notification \(method, privacy: .public)")
        }
    }
}

/// An ordered handler table with removable entries; a handler may remove
/// itself while being called.
@MainActor
private final class HandlerList<T> {
    private var handlers: [(id: Int, f: @MainActor (T) -> Void)] = []
    private var nextID = 0

    func add(_ f: @escaping @MainActor (T) -> Void) -> NotificationHub.Token {
        let id = nextID
        nextID += 1
        handlers.append((id, f))
        return NotificationHub.Token { [weak self] in
            self?.handlers.removeAll { $0.id == id }
        }
    }

    func fire(_ value: T) {
        for h in handlers {
            h.f(value)
        }
    }
}
