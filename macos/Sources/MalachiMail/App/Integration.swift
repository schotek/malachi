// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The glue between the shell and the parts that plug into it: the mailbox
/// controller and its sidebar, the status bar, the settings window, the
/// account wizard and the desktop notifications. The counterpart of what
/// window.New wires up in Go, plus the list, the reader, the actions and
/// compose.
@MainActor
final class Integration {
    let state: AppState
    let sync: SyncController
    let mailbox: MailboxController
    let sidebar: FolderSidebarViewController
    /// The status line and its popover (window.blp `status_button`, at the
    /// bottom of the window here).
    let statusBar: StatusBarViewController
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
    /// Toasts over the main window's message pane (window.go `Toast`).
    private let mainToast: @MainActor (String) -> Void
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
        self.mainToast = mainToast
        sync = SyncController()
        mailbox = MailboxController(client: state.client, settings: state.settings, sync: sync, toast: mainToast)
        sidebar = FolderSidebarViewController(mailbox: mailbox)
        statusBar = StatusBarViewController(sync: sync, mailbox: mailbox)
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
        mainWindow.install(statusBar: statusBar)
        wireConnection()
        wireNotifications()
        wireMailbox()
        wireStatusBar()
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

    /// window.go `showConnectionState`: the mailbox, which tells the status
    /// line first and loads accounts and the sync status once connected.
    /// Until the connection reports anything the line says the first
    /// attempt is underway; a window made later starts from the state the
    /// hub knows.
    private func wireConnection() {
        let hub = state.notifications
        sync.setConnection(hub.connectionState)
        // Every minute, so "Up to date · 15:04" becomes a date the next day.
        sync.startRefreshing()
        tokens.append(hub.addConnectionState { [weak self] s in
            guard let self else { return }
            self.mailbox.handleConnection(s)
            // After the mailbox: the list's banner and its own reaction
            // (collapse loading rows, drop late replies) follow the model.
            self.listView.showConnectionState(s)
        })
    }

    /// window.go: the selection drives the reader, activation opens a
    /// window, the auth banner's button opens the settings or signs the
    /// account in again, and an outbox
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
        list.onActivateDraft = { [weak self] summary in
            self?.actions.openDraft(summary.id)
        }
        listView.onAuthBannerButton = { [weak self] in
            self?.authBannerButton()
        }
        listView.onCertBannerButton = { [weak self] in
            self?.certBannerButton()
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

    /// The sign-in banner's button (sync.go `onAuthBannerButton`):
    /// `signInAgain` for the banner's account and its reason; the
    /// preferences for an account the banner has no route for (GNOME Online
    /// Accounts, whose panel does not exist on macOS, a keyring failure).
    private func authBannerButton() {
        switch sync.authBannerAction {
        case .editAccount(let id, let reason)?:
            signInAgain(.password, reason: reason, accountId: id)
        case .signInAgain(let id, _)?:
            signInAgain(.oauth, reason: 0, accountId: id)
        default:
            state.hooks.openPreferences?()
        }
    }

    /// Repairs the sign-in of account `id` the way it signs in, after a
    /// failure with `reason` (the banner's notify.authRequired reason, or
    /// the code of the error of the account's authRequired state; 0 when
    /// unknown) (sync.go `signInAgain`): the account's edit wizard asking
    /// for the password when it is missing or refused (`editsPassword`),
    /// the browser for the daemon's own sign-in (the sign-in banner's page
    /// as the fallback when it is up for this account), the preferences
    /// otherwise (GNOME Online Accounts has no panel here).
    private func signInAgain(_ kind: SignInKind, reason: ErrorCode, accountId id: AccountID) {
        if editsPassword(kind, reason), mailbox.model.account(id) != nil {
            editAccount(id, requestPassword: reason)
            return
        }
        guard kind == .oauth else {
            state.hooks.openPreferences?()
            return
        }
        var fallback: String?
        if case .signInAgain(let banner, let url)? = sync.authBannerAction, banner == id {
            fallback = url
        }
        signIn(accountId: id, fallback: fallback)
    }

    /// Signs account `id` of the browser sign-in in again (sync.go
    /// `signInAgain` / `signInInBrowser`): a fresh session and its page in
    /// the browser, or `fallback` (the notification's page) when the daemon
    /// cannot start one. The daemon completes the sign-in by itself.
    private func signIn(accountId id: AccountID, fallback: String?) {
        let client = state.client
        Task { [weak self] in
            guard let self else { return }
            switch await self.sync.requestSignInURL(client: client, accountId: id, fallbackURL: fallback) {
            case .open(let url):
                openInBrowser(url) { [weak self] text in self?.mainToast(text) }
            case .failed(let text):
                self.mainToast(text)
            }
        }
    }

    /// The certificate banner's "Edit Account…" (sync.go
    /// `onCertBannerButton`): `editAccount` for the banner's account.
    private func certBannerButton() {
        guard let id = sync.certBannerAccount else { return }
        editAccount(id)
    }

    /// The wizard in edit mode for account `id` (sync.go `editAccount`: the
    /// banners, an account's row in the status popover), as a sheet on the
    /// main window, where the connection test shows a refused certificate
    /// and offers to trust it. With `requestPassword` it opens on the
    /// identity page asking for the password (`WizardController
    /// .requestPassword`). The sidebar and the banners follow
    /// notify.accountsChanged and notify.syncState after the save.
    private func editAccount(_ id: AccountID, requestPassword: ErrorCode? = nil) {
        guard let account = mailbox.model.account(id), let parent = mainWindow?.window else { return }
        AccountWizardController.present(
            from: parent, client: state.client, editing: account, requestPassword: requestPassword,
            confirmTrust: Self.confirmTrust(state.alerts)
        ) { _, _ in }
    }

    /// The status popover's rows (status.go `onStatusAction`, `showOutbox`):
    /// the popover has closed already; a dialog, the browser or the list
    /// takes over.
    private func wireStatusBar() {
        statusBar.onAction = { [weak self] st in
            self?.statusAction(st)
        }
        statusBar.onShowOutbox = { [weak self] acc in
            guard let self, self.mailbox.showOutbox(acc) else { return }
            // GTK shows the content pane (outerSplit.SetShowContent); here a
            // list folded by a narrow window unfolds, or only the title
            // would change.
            if let split = self.mainWindow?.split, split.isListCollapsed {
                split.toggleMessageList(nil)
            }
        }
    }

    /// Runs the action of an account's row in the status popover (status.go
    /// `onStatusAction`): check or try again, sign in (`signInAgain`, as the
    /// banner does: the edit wizard asking for a missing or refused
    /// password, the browser, or the preferences), or the account
    /// assistant.
    private func statusAction(_ st: AccountStatus) {
        switch st.action {
        case .check, .retry:
            mailbox.triggerSync(accountId: st.account)
        case .signIn:
            signInAgain(st.signIn, reason: st.reason, accountId: st.account)
        case .edit:
            editAccount(st.account)
        case .noAction:
            break
        }
    }

    /// The account wizard's "Trust This Certificate?" through the shell's
    /// alerts.
    static func confirmTrust(_ alerts: any Alerts) -> WizardConfirmTrust {
        { window, p in
            await alerts.confirmTrustCertificate(
                on: window, heading: p.heading, body: p.body, details: p.details, confirmLabel: p.confirmLabel)
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

    /// window.go `refreshListTitle`: the selected folder is the window's
    /// title and its counts the subtitle.
    private func wireMailbox() {
        mailbox.onListTitleChanged = { [weak self] title, subtitle in
            guard let self else { return }
            self.mainWindow?.folderTitle = title
            self.mainWindow?.folderSubtitle = subtitle
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
                client: state.client, settings: state.settings, bridge: state.paths.mcpBridge?.path,
                confirmRemoval: { window, c in
                    let answer = await state.alerts.confirmDestructiveExtra(
                        on: window, heading: c.heading, body: c.body, confirmLabel: c.confirmLabel,
                        extraLabel: c.extraLabel, extraDefault: c.extraDefault)
                    return (confirmed: answer.confirmed, deleteLocalData: answer.extra)
                },
                confirmTrust: Integration.confirmTrust(state.alerts)
            )
        }
        state.hooks.addAccount = { [weak self] window in
            guard let self, let parent = window ?? self.mainWindow?.window else { return }
            // The sidebar reloads on notify.accountsChanged; nothing to do here.
            AccountWizardController.present(
                from: parent, client: self.state.client, confirmTrust: Self.confirmTrust(self.state.alerts)
            ) { _, _ in }
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
