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
        /// Opens the Settings window on the AI page: the Assistant menu's
        /// "Set Up the Assistant…".
        var openAISettings: (@MainActor () -> Void)?
        /// Opens the account wizard as a sheet on `window` (`app.add-account`).
        var addAccount: (@MainActor (NSWindow?) -> Void)?
        /// Opens an empty compose window (`app.compose`).
        var composeNew: (@MainActor () -> Void)?
        /// Opens a compose window for a `mailto:` URL (the `open` signal).
        var openMailto: (@MainActor (URL) -> Void)?
        /// Asks the daemon to synchronise now (`win.refresh`).
        var checkForNewMail: (@MainActor () -> Void)?
        /// The message list's filter (window.go `message_filter`): set and read.
        var setMessageFilter: (@MainActor (MessageFilter) -> Void)?
        var messageFilter: (@MainActor () -> MessageFilter)?
        /// Whether the list shows search results (the filter is off then).
        var searchActive: (@MainActor () -> Bool)?
        /// Opens a compose window prepared by the actions (reply, forward,
        /// a mailto: link inside a message); the compose manager wires it
        /// (compose/manager.go `Open`).
        var openCompose: (@MainActor (ComposeParams) -> Void)?
        /// Brings the compose window editing a draft (from draft.open) to
        /// the front; false when none edits it (compose/manager.go
        /// `FindDraft`).
        var raiseDraft: (@MainActor (Draft) -> Bool)?
        /// Opens the Jira account assistant as a sheet on `window` (File ▸
        /// Add Jira Account…, `JiraWizardWindowController`).
        var addJiraAccount: (@MainActor (NSWindow?) -> Void)?
        /// Whether New Message is offered (`Capabilities.canComposeNew`: not
        /// with issue-tracker accounts alone); nil counts as yes.
        var canComposeNew: (@MainActor () -> Bool)?
        /// The Change Status menu's controller (`IssueActionsController`,
        /// Integration+Jira): the menus and the issue cards' pills ask it
        /// when they open; nil offers no status changes.
        var issueActions: (@MainActor () -> IssueActionsController?)?
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
    /// What the Assistant menu may use (ui/internal/assistant): the Claude
    /// apps' link handlers, the user's Claude Code for the panel and the
    /// bridge's registration in them.
    let assistant: AssistantController
    /// Finds the user's Claude Code and asks its version and sign-in, for
    /// the assistant panel, its availability and Settings → AI (one
    /// instance, so their answers are shared).
    let claudeCode: ClaudeCodeLocator
    /// Claude Desktop around a change of "Register with Claude": the offer
    /// to restart it and the change it still has to pick up, for the
    /// application's run (docs/mcp.md; macOS leads, GTK follows).
    let claudeDesktop: ClaudeDesktopController
    /// Whether Claude Desktop runs, quitting and starting it, and its
    /// termination, for `claudeDesktop`.
    private let claudeDesktopService: ClaudeDesktopService
    /// The board's preferences in the daemon (`board.preferences`), read by
    /// the triage, its schedule and Settings → AI; loaded whenever the
    /// connection comes (`wireBoardTriage`).
    let boardPreferences: BoardPreferencesController
    /// The board's triage run by the user's Claude Code (docs/mcp.md
    /// "Triage of the board"): the board toolbars' Triage, the status
    /// strip, Settings → AI's Board group. One run at a time for the
    /// application; the main window feeds it the board's snapshots.
    let triage: BoardTriageController
    /// Starts automatic runs (`board.preferences` `autoTriage`, off by
    /// default) while the application runs: `start()` once it runs,
    /// `stop()` at quit.
    let autoTriage: BoardAutoTriageScheduler
    /// The board's Suggest Reply (docs/mcp.md "A suggested reply on the
    /// board"): one request at a time for the application, started from a
    /// case's detail; the main window lists the board again after it.
    let boardReply: BoardReplyController
    private var boardTriageTokens: [NotificationHub.Token] = []

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
        let claudeCode = ClaudeCodeLocator(settings: settings, directory: ClaudeCodeLocator.defaultDirectory)
        self.claudeCode = claudeCode
        let assistant = AssistantController(
            bridge: paths.mcpBridge?.path, settings: settings, locator: claudeCode, handler: Self.handlesScheme)
        self.assistant = assistant
        let service = ClaudeDesktopService()
        claudeDesktopService = service
        let desktop = ClaudeDesktopController(bridge: paths.mcpBridge?.path, platform: service.platform)
        claudeDesktop = desktop
        // Every status a write of it reports reaches the Assistant menus;
        // Claude Desktop quitting by itself writes what it still has to get.
        desktop.onStatus = { [weak assistant] s in assistant?.apply(s) }
        service.onTerminate = { [weak desktop] in desktop?.terminated() }
        let prefs = BoardPreferencesController(client: client)
        boardPreferences = prefs
        let triage = BoardTriageController(
            client: client, settings: settings, locator: claudeCode, preferences: prefs, assistant: assistant,
            bridge: paths.mcpBridge?.path, socket: paths.socket)
        self.triage = triage
        autoTriage = BoardAutoTriageScheduler(target: triage)
        boardReply = BoardReplyController(
            client: client, settings: settings, locator: claudeCode, assistant: assistant,
            bridge: paths.mcpBridge?.path, socket: paths.socket)
        wireBoardTriage()
    }

    /// The board's triage: its preferences follow the connection and say
    /// a refused write as a toast; a manual run without consent asks with
    /// the sheet on the main window.
    private func wireBoardTriage() {
        let prefs = boardPreferences
        let toasts = toasts
        prefs.onError = { text in toasts.show(text) }
        boardTriageTokens.append(notifications.addConnectionState { s in
            prefs.connectionChanged(connected: Self.isConnected(s))
        })
        if Self.isConnected(notifications.connectionState) {
            prefs.load()
        }
        triage.consent = { [weak self] in
            await self?.confirmTriageConsent(on: self?.mainWindow?.window) ?? false
        }
        // Suggest Reply asks the assistant's own consent, as the panel and
        // the compose rewrite do, not the board's.
        boardReply.consent = { [weak self] in
            guard let self else { return false }
            let t = Assistant.panelTexts()
            return await self.alerts.confirm(
                on: self.mainWindow?.window, heading: t.consentHeading, body: t.consentBody, confirmLabel: t.allow,
                declineLabel: t.cancel)
        }
    }

    /// Whether the daemon can be asked in connection state `s`.
    static func isConnected(_ s: ConnectionController.ConnectionState) -> Bool {
        switch s {
        case .connected, .infoFailed: return true
        case .connecting, .protocolMismatch, .unavailable, .stopping: return false
        }
    }

    /// "Let the Assistant Triage the Board?" as a sheet on `window`, the
    /// way the panel asks for its own consent (the panel's Allow and
    /// Cancel); true allows. The triage's manual run asks on the main
    /// window, Settings → AI on its own before it gives the consent.
    func confirmTriageConsent(on window: NSWindow?) async -> Bool {
        let t = Assistant.panelTexts()
        return await alerts.confirm(
            on: window, heading: Board.Text.triageConsentHeading, body: Board.Text.triageConsentBody,
            confirmLabel: t.allow, declineLabel: t.cancel)
    }

    /// Quitting: the schedule stops and a run under way ends as cancelled
    /// (its Claude Code is stopped), then waits for `board.runEnd` while the
    /// connection still stands — at most `BoardTriageController.endWait`,
    /// so a daemon that does not answer never holds the quit (it ends the
    /// run itself later).
    func stopBoardTriage() async {
        autoTriage.stop()
        // The suggested reply under way stops too, and deletes a draft it
        // created and did not link yet, within the same bound.
        async let reply: Void = boardReply.cancelAndCleanUp()
        await triage.cancelAndEnd()
        await reply
    }

    /// Whether an application handles links of `scheme` (LaunchServices),
    /// for `AssistantController`: Claude Desktop registers `claude:`,
    /// Claude Code `claude-cli:` once it was used in a terminal.
    private static func handlesScheme(_ scheme: String) -> Bool {
        guard let url = URL(string: scheme + "://") else { return false }
        return NSWorkspace.shared.urlForApplication(toOpen: url) != nil
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
/// and window.go `showConnectionState`): the status bar, the banners,
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
    private var messagesChanged = HandlerList<MessagesChangedNotification>()
    private var boardChanged = HandlerList<BoardChangedNotification>()
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

    func addMessagesChanged(_ f: @escaping @MainActor (MessagesChangedNotification) -> Void) -> Token {
        messagesChanged.add(f)
    }

    /// notify.boardChanged: the board's source lists it again.
    func addBoardChanged(_ f: @escaping @MainActor (BoardChangedNotification) -> Void) -> Token {
        boardChanged.add(f)
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
        case .messagesChanged(let m):
            messagesChanged.fire(m)
        case .boardChanged(let b):
            boardChanged.fire(b)
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
