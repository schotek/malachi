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
        /// Opens the Settings window on the Integrations page: the
        /// assistant panel's "Set Up in Integrations…" over an `ai` account
        /// (docs/plans/ai-api.md §7.4).
        var openIntegrationsSettings: (@MainActor () -> Void)?
        /// Opens the dialog of an AI provider account (kind `ai`); the Bool
        /// starts it with Replace Key… (a refused key: the status bar's
        /// Sign In, the triage's control, GTK `editAIAccount`).
        var editAIAccount: (@MainActor (Account, Bool) -> Void)?
        /// Opens the dialog that adds an AI provider account of the kind
        /// (the App Store build's "Add Anthropic API…",
        /// `WebsiteFeaturesDialog`).
        var addAIAccount: (@MainActor (AIProvider) -> Void)?
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
    let chatGPT: ChatGPTConnection
    let codex: CodexProvider
    private var providerSettingsTokens: [Settings.ChangeToken] = []
    let claudeCode: ClaudeCodeLocator
    /// Claude Desktop around a change of its "Register with Claude": the offer
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
    /// Builds every prompt of the assistant (docs/api.md §4.18
    /// assistant.prompt): the panel, the compose window's rewrite and
    /// writing bar, the search in the user's own words, the board's triage
    /// and suggested reply ask through it, whichever provider then runs
    /// the model (Claude Code, Codex, or the daemon itself over an `ai`
    /// account).
    let prompter: DaemonPrompter
    /// The calendar (Integration+Calendar): the windows' sources, the
    /// alerts, the routing of the calendar's notifications.
    let calendar: AppCalendar
    /// eWay-CRM (docs/eway.md): the accounts, the messages a save or a
    /// removal runs for, shared by every window's `CRMController`
    /// (Integration+CRM wires it).
    let crm = CRMState()
    /// The address books (Integration+Contacts): the Contacts window, the
    /// address chips' Show Contact / Add to Contacts…, the suggestions'
    /// scope.
    private(set) var contacts: AppContacts!
    /// The mail rules (Integration+Rules): Settings → Rules, the rule
    /// editor, Create Rule from Message… and the folders' Apply Rules….
    private(set) var rules: AppRules!
    /// The user's profile for the assistant (docs/api.md §4.18): every
    /// prompt carries it, Settings → AI → About You edits it, the panel's
    /// proposal cards resolve through it. Read when the connection comes
    /// and on notify.assistantProfileChanged and notify.accountsChanged.
    let profile: ProfileStore
    private var profileTokens: [NotificationHub.Token] = []
    /// The update check (docs/api.md §4.20, AppState+Updates): the main
    /// window's banner, Settings → General, the About panel and the
    /// question of a security release. Read when the connection comes and
    /// on notify.updateChanged.
    let updates: UpdateController
    var updateTokens: [NotificationHub.Token] = []
    /// Signature trust (docs/api.md §4.22): the number every
    /// notify.signaturesChanged raises, which the details popover and
    /// Settings → Security follow (Integration+Signatures wires it).
    let signatures = SignatureState()
    private var boardTriageTokens: [NotificationHub.Token] = []
    /// The daemon's notifications and the connection for the AI provider
    /// accounts of the assistant (`AssistantController+API.swift`, GTK
    /// window/notify.go and window.go `showConnectionState`).
    private var assistantAPITokens: [NotificationHub.Token] = []
    private var assistantAPIToken: AssistantController.Token?
    /// The assistant's runtime generation as last seen by
    /// `assistantRuntimeChanged`, and whether the change under way came
    /// from a settings key or the ChatGPT connection, which the triage and
    /// Suggest Reply follow themselves.
    private var seenProviderGeneration = 0
    private var providerChangeFromKeys = false

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
        calendar = AppCalendar(client: client, settings: settings, hub: notifications)
        profile = ProfileStore(client: client)
        updates = UpdateController(client: client)
        let prompter = DaemonPrompter(client: client)
        self.prompter = prompter
        let claudeCode = ClaudeCodeLocator(settings: settings, directory: ClaudeCodeLocator.defaultDirectory)
        self.claudeCode = claudeCode
        // The daemon's client for the `ai` accounts: they are listed when
        // the connection comes (`wireAssistantAPI`), not here.
        let assistant = AssistantController(
            bridge: paths.mcpBridge?.path, daemonBridge: paths.daemonBridge?.path, settings: settings,
            locator: claudeCode, client: client, handler: Self.handlesScheme)
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
        let support = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("Malachi Mail/ChatGPT", isDirectory: true)
        let cache = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("io.github.schotek.Malachi/assistant/chatgpt", isDirectory: true)
        chatGPT = ChatGPTConnection(store: ChatGPTKeychain(directory: support), browser: ChatGPTSystemBrowser(), validator: ChatGPTAppleSignature())
        codex = CodexProvider(connection: chatGPT, settings: settings, directory: cache, prompter: prompter)
        assistant.chatGPT = codex
        assistant.provider = { [weak self] in self?.selectedProvider }
        configureRequest(triage.request)
        configureRequest(boardReply.request)
        chatGPT.onChange = { [weak self] in self?.chatGPTConnectionChanged() }
        // The triage and Suggest Reply follow these keys themselves, each
        // only for the provider in effect (`AssistantRequest
        // .providerChangeConcernsActive`); the application ends Codex's
        // sessions the same way and lets the assistant's handlers follow.
        for key in [Settings.Key.assistantProvider, .assistantTarget, .assistantCodexPath, .assistantChatGPTModel, .boardTriageChatGPTModel] {
            providerSettingsTokens.append(settings.onChange(key) { [weak self] in self?.providerSettingChanged(key) })
        }
        providerSettingsTokens.append(settings.onChange(.assistantChatGPTConsentVersion) { [weak self] in
            guard let self, self.settings.assistantChatGPTConsentVersion != 1 else { return }
            self.providerSettingChanged(.assistantChatGPTConsentVersion)
        })
        // The App Store build never runs Codex, so it never reads the
        // ChatGPT sign-in from the keychain either.
        if settings.distribution.allowsLocalAgents {
            Task { @MainActor [weak self] in try? await self?.chatGPT.load() }
        }
        wireBoardTriage()
        wireAssistantAPI()
        wireProfile()
        wireUpdates()
        EventEditorManager.shared.state = self
        contacts = AppContacts(state: self)
        rules = AppRules(state: self)
    }

    /// The provider the panel, the one-shot requests, the triage and
    /// Suggest Reply run on (GTK `Provider()` / `boardProvider()`): Codex
    /// for ChatGPT, the daemon's assistant over the chosen `ai` account
    /// (or its stand-in while the account is not listed or has no client:
    /// `apiProviderOrUnavailable`), nil for the user's Claude Code.
    var selectedProvider: (any AssistantProvider)? {
        switch assistant.source {
        case .chatgpt: return codex
        case .account: return assistant.apiProviderOrUnavailable()
        case .claude: return nil
        }
    }

    /// The assistant's consent of the provider in effect, as the compose
    /// window asks it before its popover: the settings' key for Claude
    /// Code and ChatGPT, the account's own for an `ai` account
    /// (`AssistantController.apiConsent`; GTK `HasAssistantConsent`).
    var hasAssistantConsent: Bool {
        assistant.usesAPI ? assistant.apiConsent : settings.selectedAssistantConsent
    }

    /// Records an accepted consent for the provider in effect: the key, or
    /// the account's through the daemon (`acceptAPIConsent`; never a key).
    func recordAssistantConsent() {
        if assistant.usesAPI {
            assistant.acceptAPIConsent()
        } else {
            settings.selectedAssistantConsent = true
        }
    }

    /// The assistant panel's footer: where the mail it is asked about goes
    /// (ChatGPT's, an `ai` account's destination, else Claude's; GTK
    /// assistant_panel.go `syncShown`).
    var assistantFooter: String {
        switch assistant.source {
        case .chatgpt: return ChatGPTText.footer
        case .account:
            guard let acc = assistant.apiAccount else { return Assistant.panelTexts().footer }
            return AssistantController.apiFooter(acc)
        case .claude: return Assistant.panelTexts().footer
        }
    }

    func configureRequest(_ request: AssistantRequest) {
        request.provider = { [weak self] in self?.selectedProvider }
        request.prompter = prompter
    }
    /// A settings key of the providers changed: Codex's sessions end only
    /// when it concerns the provider in effect (a change for ChatGPT while
    /// Claude is chosen leaves everything running; its model applies from
    /// the next request).
    private func providerSettingChanged(_ key: Settings.Key) {
        // A model applies from the next request: nothing under way ends, the
        // panel's conversation neither (`providerChanged` would start a new
        // one).
        if key == .assistantChatGPTModel || key == .boardTriageChatGPTModel {
            return
        }
        if AssistantRequest.providerChangeConcernsActive(key, settings: settings) {
            codex.cancelAll()
        }
        // A change to or from an `ai` account is a runtime generation too
        // (the provider over the account is built anew).
        runtimeChanged(fromKeys: true)
    }

    /// The ChatGPT connection changed (connected, signed out, reconnected):
    /// what runs over it ends only while ChatGPT is the provider; the
    /// triage and Suggest Reply learn it here, as no settings key changed.
    private func chatGPTConnectionChanged() {
        if settings.assistantProvider == Assistant.providerChatGPT {
            codex.cancelAll()
            triage.providerChanged()
            boardReply.providerChanged()
        }
        runtimeChanged(fromKeys: true)
    }

    /// A new runtime generation of the assistant from a settings key or
    /// the ChatGPT connection: `assistantRuntimeChanged` must not tell the
    /// triage and Suggest Reply again (they follow those themselves).
    private func runtimeChanged(fromKeys: Bool) {
        providerChangeFromKeys = fromKeys
        defer { providerChangeFromKeys = false }
        assistant.providerChanged()
    }

    /// The AI provider accounts of the assistant (`AssistantController
    /// +API.swift`; GTK window/notify.go, window.go `showConnectionState`,
    /// main.go): the sessions of the daemon's assistant get its events, the
    /// accounts are listed again on notify.accountsChanged, on a usage
    /// change (the account's state may have moved with the turn) and on
    /// every connection made, and the sessions end with the connection.
    private func wireAssistantAPI() {
        let assistant = assistant
        assistantAPITokens.append(notifications.addAssistantEvent { e in
            assistant.apiSessions.handle(e)
        })
        assistantAPITokens.append(notifications.addAssistantUsageChanged { _ in
            assistant.accountsChanged()
        })
        assistantAPITokens.append(notifications.addAccountsChanged {
            assistant.accountsChanged()
        })
        assistantAPITokens.append(notifications.addConnectionState { s in
            if Self.isConnected(s) {
                assistant.accountsChanged()
            } else {
                assistant.apiDisconnected()
            }
        })
        if Self.isConnected(notifications.connectionState) {
            assistant.accountsChanged()
        }
        // A runtime generation the accounts bring (the chosen account's
        // configuration or switch changed, the account listed or gone,
        // GTK `runtimeChanged`): a triage run or a suggested reply under
        // way over the old provider stops, as for a settings key.
        seenProviderGeneration = assistant.providerGeneration
        assistantAPIToken = assistant.onChange { [weak self] in self?.assistantRuntimeChanged() }
    }

    private func assistantRuntimeChanged() {
        guard assistant.providerGeneration != seenProviderGeneration else { return }
        seenProviderGeneration = assistant.providerGeneration
        guard !providerChangeFromKeys else { return }
        triage.providerChanged()
        boardReply.providerChanged()
    }

    /// "Send Mail to Claude?" (the panel's consent) on `window` for the
    /// provider in effect: ChatGPT's words, an `ai` account's by its
    /// provider kind (`AIAccount.consentTexts`, the account dialog's; GTK
    /// `AskAssistantConsent` / `askAPIConsent`), else Claude's; true
    /// allows. Whoever asked records it (`recordAssistantConsent`, the
    /// controllers' own through the provider). An answer for another
    /// provider or runtime generation than the one asked for does not count.
    func assistantConsent(on window: NSWindow?) async -> Bool {
        let t = Assistant.panelTexts()
        let generation = assistant.providerGeneration
        let selected = settings.assistantProvider
        let heading: String
        let body: String
        switch assistant.source {
        case .chatgpt:
            heading = ChatGPTText.consentHeading
            body = ChatGPTText.consentBody
        case .account:
            let c = AIAccount.consentTexts(for: assistant.apiAccount?.config.ai?.provider ?? .openaiCompatible)
            heading = c.heading
            body = c.body
        case .claude:
            heading = t.consentHeading
            body = t.consentBody
        }
        let allowed = await alerts.confirm(
            on: window, heading: heading, body: body, confirmLabel: t.allow, declineLabel: t.cancel)
        return allowed && generation == assistant.providerGeneration && selected == settings.assistantProvider
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
            return await self.assistantConsent(on: self.mainWindow?.window)
        }
    }

    /// The profile follows the connection and the daemon's notifications;
    /// the triage and Suggest Reply carry it in their prompts.
    private func wireProfile() {
        let store = profile
        profileTokens.append(notifications.addConnectionState { s in
            if Self.isConnected(s) {
                store.refresh()
            }
        })
        profileTokens.append(notifications.addAssistantProfileChanged { store.refresh() })
        profileTokens.append(notifications.addAccountsChanged { store.refresh() })
        if Self.isConnected(notifications.connectionState) {
            store.refresh()
        }
    }

    /// Whether the daemon can be asked in connection state `s`.
    static func isConnected(_ s: ConnectionController.ConnectionState) -> Bool {
        switch s {
        case .connected, .infoFailed: return true
        case .connecting, .protocolMismatch, .otherDaemon, .unavailable, .stopping: return false
        }
    }

    /// "Let the Assistant Triage the Board?" as a sheet on `window`, the
    /// way the panel asks for its own consent (the panel's Allow and
    /// Cancel); true allows. The triage's manual run asks on the main
    /// window, Settings → AI on its own before it gives the consent.
    func confirmTriageConsent(on window: NSWindow?) async -> Bool {
        let t = Assistant.panelTexts()
        let generation = assistant.providerGeneration
        let selected = settings.assistantProvider
        let heading: String
        let body: String
        switch assistant.source {
        case .chatgpt:
            heading = ChatGPTText.boardHeading
            body = ChatGPTText.boardBody
        case .account:
            // The board's heading with the sentence naming where the mail
            // goes (GTK `askAPIBoardConsent`); the triage records the
            // consent on the account through the provider.
            heading = Board.Text.triageConsentHeading
            if let acc = assistant.apiAccount {
                body = AssistantController.apiBoardConsentBody(acc)
            } else {
                body = L10n.format(Assistant.apiText().boardConsentBody, [assistant.apiBrand])
            }
        case .claude:
            heading = Board.Text.triageConsentHeading
            body = Board.Text.triageConsentBody
        }
        let allowed = await alerts.confirm(
            on: window, heading: heading, body: body, confirmLabel: t.allow, declineLabel: t.cancel)
        return allowed && generation == assistant.providerGeneration && selected == settings.assistantProvider
    }

    /// Quitting: the schedule stops and a run under way ends as cancelled
    /// (its Claude Code is stopped), then waits for `board.runEnd` while the
    /// connection still stands — at most `BoardTriageController.endWait`,
    /// so a daemon that does not answer never holds the quit (it ends the
    /// run itself later).
    func stopBoardTriage() async {
        codex.cancelAll()
        chatGPT.cancel()
        autoTriage.stop()
        // The suggested reply under way stops too, and deletes a draft it
        // created and did not link yet, within the same bound.
        async let reply: Void = boardReply.cancelAndCleanUp()
        await triage.cancelAndEnd()
        await reply
        // The daemon's assistant gets the sessionStop of every session
        // ended above (the panel's, the requests', the board's) while the
        // connection still stands (ui/main.go `WaitAPISessions`; at most
        // `AssistantController.apiSessionStopWait`).
        await assistant.waitAPISessions()
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
        if let main = mainWindow as? MainWindowController {
            main.showMainWindow()
        } else {
            mainWindow?.showWindow(nil)
            mainWindow?.window?.makeKeyAndOrderFront(nil)
            NSApp.activate()
        }
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
    private var draftsChanged = HandlerList<DraftsChangedNotification>()
    private var assistantEvent = HandlerList<AssistantEvent>()
    private var assistantUsageChanged = HandlerList<AssistantUsageChangedNotification>()
    private var calendarNotification = HandlerList<CalendarNotification>()
    private var assistantProfileChanged = HandlerList<Void>()
    private var updateChanged = HandlerList<Void>()
    private var rulesChanged = HandlerList<Void>()
    private var signaturesChanged = HandlerList<Void>()
    private var contactsChanged = HandlerList<ContactsChangedNotification>()
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

    /// notify.draftsChanged: the mailbox asks the conversations it names
    /// for their drafts again (`MailboxController.handleDraftsChanged`).
    func addDraftsChanged(_ f: @escaping @MainActor (DraftsChangedNotification) -> Void) -> Token {
        draftsChanged.add(f)
    }

    /// notify.assistantEvent: an event of a session of the daemon's
    /// assistant over an `ai` account (docs/api.md §4.18); the session's
    /// owner matches `sessionId`.
    func addAssistantEvent(_ f: @escaping @MainActor (AssistantEvent) -> Void) -> Token {
        assistantEvent.add(f)
    }

    /// notify.assistantUsageChanged: an `ai` account's usage of today
    /// changed; whoever shows it asks assistant.usage again.
    func addAssistantUsageChanged(_ f: @escaping @MainActor (AssistantUsageChangedNotification) -> Void) -> Token {
        assistantUsageChanged.add(f)
    }

    /// notify.assistantProfileChanged: the application's `ProfileStore`
    /// reads the profile again.
    func addAssistantProfileChanged(_ f: @escaping @MainActor () -> Void) -> Token {
        assistantProfileChanged.add { _ in f() }
    }

    /// notify.updateChanged: the application's `UpdateController` asks
    /// update.status again.
    func addUpdateChanged(_ f: @escaping @MainActor () -> Void) -> Token {
        updateChanged.add { _ in f() }
    }

    /// notify.rulesChanged: the application's `RulesController` lists the
    /// rules again (`AppRules`).
    func addRulesChanged(_ f: @escaping @MainActor () -> Void) -> Token {
        rulesChanged.add { _ in f() }
    }

    /// notify.signaturesChanged: trust changed (a key imported, confirmed
    /// or removed, a revocation check); the messages on display, an open
    /// details popover and the key list ask again (`wireSignatures`).
    func addSignaturesChanged(_ f: @escaping @MainActor () -> Void) -> Token {
        signaturesChanged.add { _ in f() }
    }

    /// notify.contactsChanged: the Contacts window, the address chips'
    /// lookup and Settings → Contacts load again (`AppContacts`).
    func addContactsChanged(_ f: @escaping @MainActor (ContactsChangedNotification) -> Void) -> Token {
        contactsChanged.add(f)
    }

    /// The calendar's notifications (notify.calendarChanged,
    /// notify.calendarState, notify.eventAlert, notify.invitationsChanged),
    /// decoded next to `DaemonNotification` (`CalendarNotification`).
    func addCalendar(_ f: @escaping @MainActor (CalendarNotification) -> Void) -> Token {
        calendarNotification.add(f)
    }

    /// Fires on every connection state change, after `connectionState`
    /// was updated. A handler added later does not get the current state;
    /// read `connectionState` for that.
    func addConnectionState(_ f: @escaping @MainActor (ConnectionController.ConnectionState) -> Void) -> Token {
        connection.add(f)
    }

    // MARK: Internals

    private func handle(_ raw: RPCNotification) {
        if raw.method == API.Notify.assistantProfileChanged {
            assistantProfileChanged.fire(())
            return
        }
        if raw.method == API.Notify.updateChanged {
            updateChanged.fire(())
            return
        }
        if raw.method == API.Notify.rulesChanged {
            rulesChanged.fire(())
            return
        }
        if raw.method == API.Notify.signaturesChanged {
            signaturesChanged.fire(())
            return
        }
        do {
            if let c = try ContactsChangedNotification.decode(raw) {
                contactsChanged.fire(c)
                return
            }
            if let c = try CalendarNotification(raw) {
                calendarNotification.fire(c)
                return
            }
        } catch {
            log.warning("bad \(raw.method, privacy: .public) payload: \(String(describing: error), privacy: .public)")
            return
        }
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
        case .draftsChanged(let d):
            draftsChanged.fire(d)
        case .assistantEvent(let e):
            assistantEvent.fire(e)
        case .assistantUsageChanged(let u):
            assistantUsageChanged.fire(u)
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
