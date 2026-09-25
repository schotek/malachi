// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The glue between the shell and the parts that plug into it: the mailbox
/// controller and its sidebar, the settings window, the account wizard and
/// the desktop notifications. The counterpart of what window.New wires up
/// in Go, plus the list, the reader, the actions and compose.
@MainActor
final class Integration {
    let state: AppState
    let sync: SyncController
    let mailbox: MailboxController
    let sidebar: FolderSidebarViewController
    let notifications: NotificationService
    /// Reading: the message list, the loaded-message cache, the reader
    /// pane and the message windows (window.go's list and pane halves).
    let list: ListController
    let listView: MessageListViewController
    let cache: MessageCache
    let windows: MessageWindows
    let reader: MessageViewController
    /// The actions: the RPC half (actions.go, outbox.go, remote.go,
    /// compose_open.go) and its AppKit half behind `MessageActions` /
    /// `MessageActionDelegate`.
    let actions: ActionsController
    let messageActions: MessageActionsController
    /// Compose: windows, drafts, the recipient completion
    /// (compose/manager.go), with the WebKit editor behind `EditorView`.
    let compose: ComposeManager

    private weak var mainWindow: MainWindowController?
    private var tokens: [NotificationHub.Token] = []
    /// Whether the message pane shows the "No Accounts" page (window.blp
    /// `no-accounts`) instead of the reader.
    private var showingNoAccounts = false

    init(state: AppState, mainWindow: MainWindowController) {
        self.state = state
        self.mainWindow = mainWindow
        // The controllers' toasts go over the main window's message pane,
        // whatever window is key (window.go `Toast`); a window-local
        // action toasts in its own window through the router.
        let toasts = state.toasts
        let mainToast: @MainActor (String) -> Void = { [weak mainWindow] text in
            if let mainWindow {
                mainWindow.toasts.show(text)
            } else {
                toasts.show(text)
            }
        }
        sync = SyncController()
        mailbox = MailboxController(client: state.client, settings: state.settings, sync: sync, toast: mainToast)
        sidebar = FolderSidebarViewController(mailbox: mailbox, sync: sync, connection: state.connection)
        notifications = NotificationService(settings: state.settings) { [weak mainWindow] in
            mainWindow?.window?.isKeyWindow ?? false
        }
        notifications.onActivate = { [weak state] in state?.showMainWindow() }

        list = ListController(mailbox: mailbox, settings: state.settings)
        listView = MessageListViewController(
            list: list, mailbox: mailbox, connection: state.connection, settings: state.settings)
        cache = MessageCache(client: state.client, toast: mainToast)
        windows = MessageWindows(state: state, cache: cache)
        reader = windows.makePaneView()
        actions = ActionsController(mailbox: mailbox, list: list, cache: cache, settings: state.settings, toast: mainToast)
        messageActions = MessageActionsController(
            state: state, actions: actions, list: list, cache: cache, windows: windows
        ) { [weak mainWindow] in mainWindow?.window }
        compose = ComposeManager(state: state) { ComposeEditorView() }

        mainWindow.install(sidebar: sidebar)
        mainWindow.install(list: listView)
        mainWindow.install(message: reader)
        wireConnection()
        wireNotifications()
        wireMailbox()
        wireReading()
        wireActions()
        wireHooks()
        compose.install(into: state)
    }

    /// window.go 337-360 and 411-433: the toolbar and menu act on the
    /// selection, every message view (pane and windows) on its message;
    /// the list's mark-read timer and its action-flag changes feed back.
    private func wireActions() {
        windows.delegate = messageActions
        mainWindow?.messageActions = messageActions
        list.onMarkRead = { [weak self] id in
            self?.actions.markRead(id)
        }
        list.onActionFlagsChanged = { [weak self] _ in
            self?.mainWindow?.window?.toolbar?.validateVisibleItems()
        }
    }

    // MARK: Wiring

    /// window.go `showConnectionState`: the footer line and the mailbox
    /// (which loads accounts and the sync status once connected).
    private func wireConnection() {
        let hub = state.notifications
        sidebar.showConnectionState(hub.connectionState)
        tokens.append(hub.addConnectionState { [weak self] s in
            guard let self else { return }
            self.sidebar.showConnectionState(s)
            self.mailbox.handleConnection(s)
            // After the mailbox: the list's banner and its own reaction
            // (collapse loading rows, drop late replies) follow the model.
            self.listView.showConnectionState(s)
        })
    }

    /// window.go: the selection drives the reader, activation opens a
    /// window, the auth banner's button opens the settings, and an outbox
    /// change re-fetches every view showing a message of that account
    /// (outbox.go `refreshOutboxViews`).
    private func wireReading() {
        listView.onSelectedMessageChanged = { [weak self] summary in
            guard let self else { return }
            if let summary {
                self.reader.show(summary)
            } else {
                self.reader.clear()
            }
        }
        listView.onActivateMessage = { [weak self] summary in
            self?.windows.openMessage(summary)
        }
        listView.onAuthBannerButton = { [weak self] in
            self?.state.hooks.openPreferences?()
        }
        list.onOutboxRefreshed = { [weak self] account in
            guard let self else { return }
            for view in self.windows.views {
                guard let current = view.current, current.accountId == account,
                      self.mailbox.model.inOutbox(current) else { continue }
                self.cache.refetch(current) { _ in }
            }
        }
    }

    /// window/notify.go `handleNotification`.
    private func wireNotifications() {
        let hub = state.notifications
        tokens.append(hub.addNewMessage { [weak self] n in
            guard let self else { return }
            // GTK order: the desktop notification first, then the list.
            self.notifications.deliver(n)
            self.mailbox.handleNewMessage(n)
        })
        tokens.append(hub.addSyncState { [weak self] s in
            self?.mailbox.handleSyncState(s)
        })
        tokens.append(hub.addAuthRequired { [weak self] a in
            self?.mailbox.handleAuthRequired(a)
        })
        tokens.append(hub.addAccountsChanged { [weak self] in
            self?.mailbox.handleAccountsChanged()
        })
    }

    private func wireMailbox() {
        mailbox.onFolderSelected = { [weak self] _, _ in
            guard let self else { return }
            self.mainWindow?.folderTitle = self.mailbox.selectedFolderTitle
        }
        mailbox.onAccountsLoaded = { [weak self] accounts in
            self?.showNoAccountsPage(accounts.isEmpty)
        }
    }

    /// The application-level entry points (ui/main.go `addActions`).
    private func wireHooks() {
        let state = state
        state.hooks.checkForNewMail = { [weak self] in
            self?.mailbox.triggerSync()
        }
        state.hooks.openPreferences = { [weak state] in
            guard let state else { return }
            PreferencesWindowController.show(
                client: state.client, settings: state.settings, bridge: state.paths.mcpBridge?.path
            ) { window, c in
                let answer = await state.alerts.confirmDestructiveExtra(
                    on: window, heading: c.heading, body: c.body, confirmLabel: c.confirmLabel,
                    extraLabel: c.extraLabel, extraDefault: c.extraDefault)
                return (confirmed: answer.confirmed, deleteLocalData: answer.extra)
            }
        }
        state.hooks.addAccount = { [weak self] window in
            guard let self, let parent = window ?? self.mainWindow?.window else { return }
            // The sidebar reloads on notify.accountsChanged; nothing to do here.
            AccountWizardController.present(from: parent, client: self.state.client) { _, _ in }
        }
    }

    // MARK: No Accounts page

    /// window.blp `no-accounts`: shown in the message pane instead of
    /// "No Message Selected" while account.list is empty.
    private func showNoAccountsPage(_ show: Bool) {
        guard show != showingNoAccounts, let mainWindow else { return }
        showingNoAccounts = show
        if show {
            let page = StatusPageViewController(
                illustration: .icon("system-users-symbolic"),
                title: L10n.T("No Accounts"),
                description: L10n.T("Add a mail account to start reading and sending mail."))
            let button = NSButton(title: mn(L10n.T("_Add Account…")), target: self, action: #selector(addAccount(_:)))
            button.bezelStyle = .rounded
            button.controlSize = .large
            button.bezelColor = .controlAccentColor
            button.keyEquivalent = "\r"
            page.statusPage.setChild(button)
            mainWindow.install(message: page)
        } else {
            // The reader shows "No Message Selected" itself while nothing
            // is selected.
            mainWindow.install(message: reader)
        }
    }

    @objc private func addAccount(_ sender: Any?) {
        state.hooks.addAccount?(mainWindow?.window)
    }
}
