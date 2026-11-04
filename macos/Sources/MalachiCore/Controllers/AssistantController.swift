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

    /// The targets, in the order of the menu and the settings.
    public nonisolated static let targets: [Assistant.Target] = [.desktop, .code, .app]

    public var chatGPT: CodexProvider?
    public private(set) var providerGeneration = 0
    public var provider: (() -> (any AssistantProvider)?)?
    public func providerChanged() { providerGeneration += 1; refreshHandlers(); notify() }
    public let settings: Settings
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
    /// Claude Code; nil without one.
    private let bridge: String?
    /// Finds Claude Code for the panel; nil: the panel is never available.
    public let locator: ClaudeCodeLocator?
    private var menuToken: Settings.ChangeToken?
    fileprivate var observers: [Int: @MainActor () -> Void] = [:]
    private var nextObserver = 0

    /// - Parameters:
    ///   - bridge: the path of `malachi-mcp` (`Paths.mcpBridge`), or nil
    ///     when there is none beside the application.
    ///   - settings: where the `assistant-target` preference is read.
    ///   - runner, timeout: how the bridge is run (tests pass a script).
    ///   - locator: finds Claude Code for the In App target; nil leaves
    ///     it unavailable.
    ///   - handler: looks up whether an app handles a URL scheme.
    public init(
        bridge: String?, settings: Settings, runner: BridgeRunner = BridgeRunner(),
        timeout: Duration = MCPRegistrationController.defaultTimeout, locator: ClaudeCodeLocator? = nil,
        handler: @escaping HandlerLookup
    ) {
        self.settings = settings
        self.bridge = bridge
        self.locator = locator
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
                found[t] = bridge != nil && (settings.assistantProvider == .chatgpt ? provider?()?.available == true : claude)
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
    /// `assistant-menu` preference while the bridge is registered.
    public var shown: Bool {
        settings.assistantProvider == .chatgpt && settings.assistantTarget == .app ? settings.assistantMenu : Assistant.shown(menu: settings.assistantMenu, registered: registered)
    }

    /// What is known about target `t`: an app handles its links, the bridge
    /// is registered in its client (`Assistant.Target.clientID`).
    /// For the panel: Claude Code and the bridge were found, the bridge is
    /// registered in any client.
    public func availability(_ t: Assistant.Target) -> Assistant.Availability {
        let t = Assistant.parseTarget(t.rawValue)
        if t == .app {
            return Assistant.Availability(handler: handlers[t] ?? false, registered: settings.assistantProvider == .chatgpt ? bridge != nil : registered)
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
        panelShown && (settings.assistantProvider == .chatgpt ? provider?()?.available == true : claudeFound)
    }

    /// Why target `t` cannot run the message actions; "" when it can
    /// (`Assistant.problem`).
    public func problem(_ t: Assistant.Target) -> String {
        if t == .app && settings.assistantProvider == .chatgpt {
            return provider?()?.available == true ? "" : L10n.T("Codex was not found. Choose a native Codex executable.")
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

    private func notify() {
        // Copy first: a handler may cancel its own token.
        for f in observers.sorted(by: { $0.key < $1.key }).map(\.value) {
            f()
        }
    }
}
