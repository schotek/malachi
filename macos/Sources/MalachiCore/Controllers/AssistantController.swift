// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// What the Assistant menu (ui/internal/assistant) knows about the two
/// Claude apps, once for the whole application: whether an app handles each
/// target's links (`Assistant.Target.scheme`, looked up through the
/// injected `HandlerLookup`, which the AppKit layer answers with
/// LaunchServices) and whether the `malachi-mcp` bridge is registered in
/// each client (the last `malachi-mcp status --json`, `MCPStatus`). Out of
/// the two comes the `Assistant.Availability` of each target, and `pick`
/// says whether the target of the `assistant-target` preference can be
/// used (never the other one instead).
///
/// The menus never wait for it: they use the last known state. `refresh()`
/// looks the handlers up at once and asks the bridge for its status in the
/// background; the application calls it at launch and every time an
/// Assistant menu opens, and the AI page of the settings hands over the
/// status its own switch got (`apply`), so a change of "Register with
/// Claude" counts at once. A status that is not known (not asked yet, no
/// bridge beside the application, the bridge failed) counts as not
/// registered; a failed status keeps the last known one. The bridge is run
/// by an `MCPRegistrationController` of its own, without its toasts: a
/// status asked while one runs is skipped, a stale reply dropped.
///
/// Whether the Assistant appears at all is `shown` (`Assistant.shown`):
/// the `assistant-menu` preference and the bridge registered in at least
/// one client, so it is off while "Register with Claude" is. `onChange`
/// reports a change of that preference too, so the menus, the toolbars,
/// the attachment chips and the settings follow one source.
///
/// The third target, In App (the assistant panel), handles no link: its
/// "handler" is the user's Claude Code found by the `ClaudeCodeLocator`
/// with the bridge beside the application (the panel hands the bridge to
/// Claude Code itself), and it counts as registered while the bridge is
/// registered in any client, which is what `shown` asks anyway. The panel
/// exists while `panelShown`: the Assistant is shown and In App chosen.
///
/// The panel's provider is the `assistant-provider` preference
/// (`Assistant.sourceOf`): the user's Claude Code, Codex with the user's
/// ChatGPT plan (`chatGPT`), or an AI provider account of the daemon
/// (`AssistantController+API.swift`, GTK ui/internal/window/ai_provider.go):
/// over an account the daemon runs the assistant itself, so no
/// registration of the bridge in the Claude apps is needed, only the
/// bundled bridge (`bridge`, which the daemon starts), and the In App
/// target is "found" while the account exists, is switched on and the
/// provider has not refused its key (`apiReady`).
///
/// No texts and no AppKit here: the menus and the settings read
/// `shown`, `availability`, `pick` and `Assistant.problem`.
@MainActor
public final class AssistantController {
    /// Whether an application handles links of the URL `scheme`.
    public typealias HandlerLookup = @MainActor (_ scheme: String) -> Bool

    /// Removes a handler installed by `onChange`; dropping the token does
    /// not.
    @MainActor
    public final class Token {
        private weak var owner: AssistantController?
        private let id: Int

        fileprivate init(owner: AssistantController, id: Int) {
            self.owner = owner
            self.id = id
        }

        public func cancel() {
            owner?.observers[id] = nil
        }
    }

    /// The targets, in the order of the menu and the settings; without In
    /// App in the App Store build (`Assistant.targets`). The instance's
    /// `targets` adds In App there while an `ai` account is the provider.
    public nonisolated static let targets: [Assistant.Target] = Assistant.targets

    /// The targets this build offers with the provider chosen
    /// (`Settings.assistantTargets`): the menus and the settings list
    /// these.
    public var targets: [Assistant.Target] { settings.assistantTargets }

    public var chatGPT: CodexProvider?
    public private(set) var providerGeneration = 0
    public var provider: (() -> (any AssistantProvider)?)?
    /// The provider in effect changed (the `assistant-provider` key, the
    /// ChatGPT connection, the chosen `ai` account's configuration): a new
    /// runtime generation, the provider over the chosen account built
    /// anew (`rebuildAPI`), the targets looked up again and the observers
    /// told (GTK `runtimeChanged`).
    public func providerChanged() {
        providerGeneration += 1
        rebuildAPI()
        refreshHandlers()
        notify()
    }
    public let settings: Settings
    /// The daemon's RPC client, for the AI provider accounts
    /// (`AssistantController+API.swift`): account.list and the provider
    /// over the chosen account. nil until the application gives it
    /// (`init(client:)` or `attach(client:)`); nothing is listed before.
    public private(set) var client: RPCClient?
    /// The daemon's `ai` accounts as last listed (account.list, again on
    /// notify.accountsChanged and on connecting: `accountsChanged()`), in
    /// the daemon's order; `aiAccountsKnown` once any listing answered,
    /// `aiAccountsGen` drops an older listing's late answer.
    public internal(set) var aiAccounts: [Account] = []
    public internal(set) var aiAccountsKnown = false
    var aiAccountsGen = 0
    /// The provider over the chosen `ai` account for this runtime
    /// generation (`rebuildAPI`); nil while another source is chosen, the
    /// account is not listed, or there is no client.
    public internal(set) var apiProvider: DaemonAssistantProvider?
    /// The sessions of the daemon's assistant, one registry per
    /// controller: the application feeds it the daemon's notifications
    /// (`AssistantSessions.handle`), `apiDisconnected()` ends them with the
    /// connection, `waitAPISessions()` lets their stops reach the daemon
    /// at quit.
    public let apiSessions = AssistantSessions()
    /// The last status the bridge reported; nil until one answered.
    public private(set) var status: MCPStatus?
    /// Whether an app handles each target's links, as last looked up; a
    /// target not looked up yet has none.
    public private(set) var handlers: [Assistant.Target: Bool] = [:]
    /// Whether the `ClaudeCodeLocator` found Claude Code when the handlers
    /// were last looked up (`canRunInApp`).
    public private(set) var claudeFound = false
    /// Nothing is emitted afterwards and replies are dropped.
    public private(set) var closed = false

    private let registration: MCPRegistrationController
    private let lookup: HandlerLookup
    /// The bridge beside the application, which the panel passes to
    /// Claude Code (GTK `bridge`); nil without one.
    let bridge: String?
    /// The bridge the daemon starts for a session over an `ai` account
    /// (GTK `daemonBridge`, `Paths.daemonBridge`): the same in the DMG, the
    /// helper bundle's worker copy in the App Store build; nil without one.
    let daemonBridge: String?
    /// Finds Claude Code for the panel; nil: the panel is never available.
    public let locator: ClaudeCodeLocator?
    private var menuToken: Settings.ChangeToken?
    fileprivate var observers: [Int: @MainActor () -> Void] = [:]
    private var nextObserver = 0

    /// - Parameters:
    ///   - bridge: the path of `malachi-mcp` (`Paths.mcpBridge`), or nil
    ///     when there is none beside the application.
    ///   - daemonBridge: the bridge the daemon may start for an `ai`
    ///     account (`Paths.daemonBridge`); nil takes `bridge`.
    ///   - settings: where the `assistant-target` preference is read.
    ///   - runner, timeout: how the bridge is run (tests pass a script).
    ///   - locator: finds Claude Code for the In App target; nil leaves
    ///     it unavailable.
    ///   - client: the daemon's RPC client for the AI provider accounts
    ///     (`attach(client:)` gives it later); nil lists none.
    ///   - handler: looks up whether an app handles a URL scheme.
    public init(
        bridge: String?, daemonBridge: String? = nil, settings: Settings, runner: BridgeRunner = BridgeRunner(),
        timeout: Duration = MCPRegistrationController.defaultTimeout, locator: ClaudeCodeLocator? = nil,
        client: RPCClient? = nil, handler: @escaping HandlerLookup
    ) {
        self.settings = settings
        self.bridge = bridge
        self.daemonBridge = daemonBridge ?? bridge
        self.locator = locator
        self.client = client
        lookup = handler
        registration = MCPRegistrationController(bridge: bridge, runner: runner, timeout: timeout)
        registration.onRegistered = { [weak self] _ in
            guard let self, let s = self.registration.status else { return }
            self.apply(s)
        }
        menuToken = settings.onChange(.assistantMenu) { [weak self] in
            guard let self, !self.closed else { return }
            self.notify()
        }
    }

    /// Gives the daemon's RPC client (the application's, once it exists):
    /// the provider over the chosen `ai` account is built and the accounts
    /// listed (`refreshAIAccounts`).
    public func attach(client: RPCClient) {
        self.client = client
        rebuildAPI()
        refreshAIAccounts()
    }

    /// Stops listening: late replies are dropped, nothing is emitted.
    public func close() {
        closed = true
        registration.close()
        menuToken?.cancel()
        menuToken = nil
        observers = [:]
    }

    // MARK: Refreshing

    /// Looks the handlers up now and asks the bridge for its status in
    /// the background (skipped while a status call runs). The caller goes
    /// on with the last known state; `onChange` reports what changed.
    public func refresh() {
        guard !closed else { return }
        refreshHandlers()
        registration.load()
    }

    /// Looks up whether an app handles each target's links, and whether
    /// the panel finds Claude Code and the bridge.
    public func refreshHandlers() {
        guard !closed else { return }
        let claude = locator?.locate() != nil
        var found: [Assistant.Target: Bool] = [:]
        for t in Self.targets {
            if t == .app {
                switch source {
                case .chatgpt: found[t] = bridge != nil && provider?()?.available == true
                case .account: found[t] = daemonBridge != nil && apiReady
                case .claude: found[t] = bridge != nil && claude
                }
            } else {
                found[t] = lookup(t.scheme)
            }
        }
        guard found != handlers || claude != claudeFound else { return }
        handlers = found
        claudeFound = claude
        notify()
    }

    /// Takes a status the bridge reported elsewhere (the AI page's
    /// "Register with Claude" after `status`, `install` or `uninstall`).
    public func apply(_ s: MCPStatus) {
        guard !closed, s != status else { return }
        status = s
        notify()
    }

    // MARK: Reading

    /// Whether the bridge is registered in at least one client, as last
    /// reported; false while no status is known.
    public var registered: Bool {
        status?.isRegistered ?? false
    }

    /// Whether the Assistant appears at all (`Assistant.shown`): the
    /// `assistant-menu` preference while the bridge is registered; over
    /// ChatGPT or an `ai` account with the In App target the preference
    /// alone (no registration in a Claude app is needed).
    public var shown: Bool {
        source != .claude && settings.assistantTarget == .app ? settings.assistantMenu : Assistant.shown(menu: settings.assistantMenu, registered: registered)
    }

    /// What is known about target `t`: an app handles its links, the bridge
    /// is registered in its client (`Assistant.Target.clientID`).
    /// For the panel: Claude Code and the bridge were found, the bridge is
    /// registered in any client.
    public func availability(_ t: Assistant.Target) -> Assistant.Availability {
        let t = Assistant.parseTarget(t.rawValue)
        if t == .app {
            switch source {
            case .chatgpt: return Assistant.Availability(handler: handlers[t] ?? false, registered: bridge != nil)
            case .account: return Assistant.Availability(handler: handlers[t] ?? false, registered: true)
            case .claude: return Assistant.Availability(handler: handlers[t] ?? false, registered: registered)
            }
        }
        let client = status?.clients.first { $0.id == t.clientID }
        return Assistant.Availability(handler: handlers[t] ?? false, registered: client?.registered ?? false)
    }

    /// The target of the `assistant-target` preference and whether it can
    /// run the action (`Assistant.pick`, no fallback to another target):
    /// the message actions and Summarize Unread need the bridge, the file
    /// hand-off does not (the panel's reads the attachment through it, so
    /// it asks with `needsBridge`).
    public func pick(needsBridge: Bool) -> (target: Assistant.Target, ok: Bool) {
        Assistant.pick(
            settings.assistantTarget, desktop: availability(.desktop), code: availability(.code),
            app: availability(.app), needsBridge: needsBridge)
    }

    /// Whether the assistant panel exists: the Assistant is shown and the
    /// In App target chosen. Whether it can run is `pick`'s.
    public var panelShown: Bool {
        shown && settings.assistantTarget == .app
    }

    /// Whether the In App target's one-shot requests exist: the compose
    /// window's rewrite and the search in the user's own words, which read
    /// no mail and need no bridge (`AssistantRequest`). The same condition
    /// as the panel's (`panelShown`), and Claude Code found (as last looked
    /// up); whether it is signed in is asked when a request runs. Its
    /// changes come through `onChange` and `Settings.onChange(.assistantTarget)`.
    public var canRunInApp: Bool {
        switch source {
        case .chatgpt: return panelShown && provider?()?.available == true
        case .account: return panelShown && apiReady
        case .claude: return panelShown && claudeFound
        }
    }

    /// Why target `t` cannot run the message actions; "" when it can
    /// (`Assistant.problem`; over an `ai` account `apiProblem`).
    public func problem(_ t: Assistant.Target) -> String {
        if t == .app {
            switch source {
            case .chatgpt: return provider?()?.available == true ? "" : L10n.T("Codex was not found. Choose a native Codex executable.")
            case .account: return apiProblem ?? ""
            case .claude: break
            }
        }
        return Assistant.problem(t, availability(t))
    }

    // MARK: Change notification

    /// Calls `f` after the handlers, the status or the `assistant-menu`
    /// preference changed. The target preference is the settings'
    /// (`Settings.onChange(.assistantTarget)`).
    public func onChange(_ f: @escaping @MainActor () -> Void) -> Token {
        let id = nextObserver
        nextObserver += 1
        observers[id] = f
        return Token(owner: self, id: id)
    }

    func notify() {
        // Copy first: a handler may cancel its own token.
        for f in observers.sorted(by: { $0.key < $1.key }).map(\.value) {
            f()
        }
    }
}
