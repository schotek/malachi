// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The settings of one Claude app on this computer (Settings →
/// Integrations → Claude Desktop or Claude Code, its row's Settings and its
/// catalogue entry's Add…; GTK claude_integration.blp,
/// ui/internal/window/claude_integration.go), one sheet on the settings
/// window parametrised by the kind (`Integrations.LocalKind.claudeDesktop`,
/// `.claudeCode`): the MCP group with its switch, "Register with Claude",
/// which puts the bundled `malachi-mcp` bridge into the MCP configuration
/// of that app only (or takes it out) through `MCPRegistrationController`
/// for its client (`Integrations.clientID`, `--client` of the bridge), and,
/// for Claude Code, the Claude Code row the assistant panel runs (its path,
/// version and sign-in, with Choose…, Sign In… and Get Claude Code…). Both
/// were rows of Settings → AI until the AI providers through their API
/// arrived (docs/plans/ai-api.md §7.2), and one sheet with one switch for
/// both apps before the two Claude apps were split.
///
/// The switch's row is insensitive until the bridge answered `status` and
/// while a call runs, and while the bridge reports the app as not installed
/// (`Integrations.registerSwitchFor`: its subtitle then says so); the
/// switch shows what the bridge last confirmed for the app and flips back
/// when a call fails, with a toast that says why. Claude Desktop overwrites
/// a change of its MCP servers while it runs and reads them only when it
/// starts (docs/mcp.md), so a flip of Claude Desktop's switch while it runs
/// and is a present client asks "Restart Claude Desktop?" first
/// (`ClaudeDesktopController`, the application's): Restart quits it, writes
/// the change through this sheet's controller and starts it again; Later
/// writes the change now and leaves it pending. While something is pending
/// a row under the switch says Claude Desktop picks it up when it restarts,
/// with Restart; the switch and that button wait while a restart runs. GTK
/// has no equivalent: this client leads (the table in macos/README.md).
/// Claude Code's switch writes at once.
///
/// The Claude Code row (Claude Code's sheet only): the executable the panel
/// runs (its path, version and whether it is signed in, from the
/// application's `ClaudeCodeLocator`, or that it was not found), with
/// "Choose…" for one of the user's own (`assistant-claude-path`; choosing
/// the one found automatically goes back to looking) and, in front of it,
/// the button of what the row offers: "Sign In…" while Claude Code says it
/// is signed out, which runs Claude Code's own sign-in in the browser (the
/// application's locator, shared with the panel), and "Get Claude Code…"
/// while there is none, which opens Anthropic's page with the installers.
/// Every status the switch gets goes to the application's
/// `AssistantController`, so the menus and the pages know at once.
@MainActor
final class ClaudeIntegrationSheetController: NSWindowController {
    /// The open sheets, kept alive while they are up.
    private static var active: [ObjectIdentifier: ClaudeIntegrationSheetController] = [:]

    /// What the sheet works over: the application's state the AI page
    /// used to get through `configure`.
    struct Context {
        var bridge: String?
        var settings: Settings
        var assistant: AssistantController?
        /// The application's Claude Desktop controller and its question;
        /// nil writes every change at once.
        var claudeDesktop: ClaudeDesktopController?
        var confirmRestart: PrefsConfirmRestart?
    }

    private let root: ClaudeIntegrationViewController

    /// Opens the sheet of Claude app `kind` (`.claudeDesktop`,
    /// `.claudeCode`) on `parent`.
    static func present(from parent: NSWindow, kind: Integrations.LocalKind, context: Context) {
        guard parent.attachedSheet == nil else { return }
        let c = ClaudeIntegrationSheetController(kind: kind, context: context)
        guard let sheet = c.window else { return }
        active[ObjectIdentifier(c)] = c
        parent.beginSheet(sheet) { _ in
            MainActor.assumeIsolated {
                c.root.close()
                active[ObjectIdentifier(c)] = nil
            }
        }
    }

    private init(kind: Integrations.LocalKind, context: Context) {
        root = ClaudeIntegrationViewController(kind: kind, context: context)
        let window = WizardSheetWindow(
            contentRect: NSRect(origin: .zero, size: ClaudeIntegrationViewController.size(kind)),
            styleMask: [.titled, .fullSizeContentView],
            backing: .buffered, defer: false
        )
        window.title = root.title ?? ""
        window.titleVisibility = .hidden
        window.titlebarAppearsTransparent = true
        window.isReleasedWhenClosed = false
        window.autorecalculatesKeyViewLoop = true
        window.contentViewController = root
        super.init(window: window)
        root.onClose = { [weak self] in self?.dismiss() }
        window.onCancel = { [weak self] in self?.dismiss() }
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Closes the sheet; late replies are dropped from now on.
    func dismiss() {
        root.close()
        guard let window else { return }
        if let parent = window.sheetParent {
            parent.endSheet(window)
        } else {
            window.close()
        }
    }
}

/// The page of a Claude app's sheet; see the window controller.
@MainActor
final class ClaudeIntegrationViewController: NSViewController {
    /// Claude Code's sheet has the Claude Code group under the MCP one;
    /// Claude Desktop's only the MCP group (GTK sets 260 for one group,
    /// here a little more room for the pending row).
    static func size(_ kind: Integrations.LocalKind) -> NSSize {
        NSSize(width: 560, height: kind == .claudeCode ? 420 : 300)
    }

    /// Close or Escape.
    var onClose: (() -> Void)?

    /// Which Claude app the sheet is for.
    let kind: Integrations.LocalKind
    private let texts: Integrations.ClaudeAppTexts
    private let context: ClaudeIntegrationSheetController.Context
    /// The application's Claude Desktop controller, for Claude Desktop's
    /// sheet only: Claude Code's switch writes at once.
    private var claudeDesktop: ClaudeDesktopController? {
        kind == .claudeDesktop ? context.claudeDesktop : nil
    }
    private let toasts = WizardToastPresenter()
    private let titleLabel = NSTextField(labelWithString: "")
    private let closeButton = NSButton(title: L10n.T("Close"), target: nil, action: nil)

    let registerSwitch = NSSwitch()
    private let mcpGroup = PreferencesGroupView(
        title: L10n.T("MCP"),
        description: L10n.T("Lets AI assistants read your mail and prepare drafts through the Model Context Protocol.")
    )
    private var registerRow: PreferenceRowView?
    /// "Claude Desktop picks up the change when it restarts" with Restart,
    /// under the switch while a change is pending.
    private var pendingRow: PreferenceRowView?
    let restartButton = NSButton(title: Assistant.restartTexts().restartNow, target: nil, action: nil)

    private let claudeCodeGroup = PreferencesGroupView()
    private var claudeCodeRow: PreferenceRowView?
    let claudeCodeChoose = NSButton(title: Assistant.panelTexts().choose, target: nil, action: nil)
    /// What the Claude Code row offers: Sign In… or Get Claude Code….
    let claudeCodeOfferButton = NSButton(title: "", target: nil, action: nil)

    /// The MCP controller of this sheet.
    private(set) var registration: MCPRegistrationController?
    private(set) var closed = false
    private var assistantToken: AssistantController.Token?
    private var claudePathToken: Settings.ChangeToken?
    /// A sign-in started or ended, here or in the panel.
    private var signInToken: ClaudeCodeLocator.SignInToken?
    /// Bumped by every look at Claude Code: a late answer is dropped.
    private var claudeCodeGen = 0
    /// What the Claude Code row's second button does.
    private var claudeCodeOffer: AssistantPanelController.Offer = .none
    /// The open panel's filter while "Choose…" is up.
    private var chooseFilter: ExecutableFilter?
    /// The switch is being set from the controller, not by the user.
    private var syncing = false
    /// "Restart Claude Desktop?" is up: the switch shows the user's choice
    /// until it is written, whatever the application learns meanwhile.
    private var asking = false

    init(kind: Integrations.LocalKind, context: ClaudeIntegrationSheetController.Context) {
        self.kind = kind
        texts = Integrations.claudeAppText(kind)
        self.context = context
        super.init(nibName: nil, bundle: nil)
        // The brand (not translated).
        title = texts.title
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    // MARK: Building

    override func loadView() {
        let root = NSView(frame: NSRect(origin: .zero, size: Self.size(kind)))
        preferredContentSize = Self.size(kind)

        let header = NSView()
        header.translatesAutoresizingMaskIntoConstraints = false
        titleLabel.stringValue = title ?? ""
        titleLabel.font = .systemFont(ofSize: 13, weight: .bold)
        titleLabel.alignment = .center
        titleLabel.lineBreakMode = .byTruncatingTail
        titleLabel.translatesAutoresizingMaskIntoConstraints = false
        header.addSubview(titleLabel)
        let divider = PrefsDivider()
        header.addSubview(divider)

        let row = PreferenceRowView(title: texts.switchTitle, subtitle: texts.switchSubtitle, trailing: registerSwitch)
        row.isEnabled = false
        if TestBuild.isActive {
            // The switch stays insensitive (MCPRegistrationController.readOnly).
            row.subtitle = TestBuild.registrationNote
        }
        registerRow = row
        let restart = Assistant.restartTexts()
        restartButton.bezelStyle = .rounded
        restartButton.target = self
        restartButton.action = #selector(restartClaudeDesktop(_:))
        let pending = PreferenceRowView(title: Assistant.targetName(.desktop), subtitle: restart.pending, trailing: restartButton)
        pendingRow = pending
        mcpGroup.setRows([row, pending])
        mcpGroup.setRow(pending, hidden: true)

        claudeCodeChoose.bezelStyle = .rounded
        claudeCodeChoose.target = self
        claudeCodeChoose.action = #selector(chooseClaudeCode(_:))
        claudeCodeOfferButton.bezelStyle = .rounded
        claudeCodeOfferButton.target = self
        claudeCodeOfferButton.action = #selector(claudeCodeOfferClicked(_:))
        claudeCodeOfferButton.isHidden = true
        // What the row offers, then Choose…; the hidden button leaves the
        // row.
        let buttons = NSStackView(views: [claudeCodeOfferButton, claudeCodeChoose])
        buttons.orientation = .horizontal
        buttons.alignment = .centerY
        buttons.spacing = 6
        buttons.detachesHiddenViews = true
        let claudeCode = PreferenceRowView(title: Assistant.targetName(.code), subtitle: "", trailing: buttons)
        claudeCode.setSubtitleSelectable()
        claudeCodeRow = claudeCode
        claudeCodeGroup.setRows([claudeCode])

        let content = prefsColumn(spacing: 24, insets: NSEdgeInsets(top: 24, left: 24, bottom: 24, right: 24))
        prefsAddFilling(mcpGroup, to: content)
        if kind == .claudeCode {
            prefsAddFilling(claudeCodeGroup, to: content)
        }
        let scroll = prefsScrollView(clampMaximum: 520, content: content)
        scroll.setContentHuggingPriority(.defaultLow, for: .vertical)

        closeButton.bezelStyle = .rounded
        closeButton.controlSize = .large
        closeButton.keyEquivalent = "\r"
        closeButton.target = self
        closeButton.action = #selector(closeClicked(_:))
        let bar = wizardButtonBar([closeButton])

        let stack = prefsColumn(spacing: 0)
        prefsAddFilling(header, to: stack)
        prefsAddFilling(scroll, to: stack)
        prefsAddFilling(bar, to: stack)
        root.addSubview(stack)
        NSLayoutConstraint.activate([
            header.heightAnchor.constraint(equalToConstant: WizardRootViewController.headerHeight),
            titleLabel.centerXAnchor.constraint(equalTo: header.centerXAnchor),
            titleLabel.centerYAnchor.constraint(equalTo: header.centerYAnchor),
            titleLabel.leadingAnchor.constraint(greaterThanOrEqualTo: header.leadingAnchor, constant: 44),
            titleLabel.trailingAnchor.constraint(lessThanOrEqualTo: header.trailingAnchor, constant: -44),
            divider.leadingAnchor.constraint(equalTo: header.leadingAnchor),
            divider.trailingAnchor.constraint(equalTo: header.trailingAnchor),
            divider.bottomAnchor.constraint(equalTo: header.bottomAnchor),
            stack.topAnchor.constraint(equalTo: root.topAnchor),
            stack.bottomAnchor.constraint(equalTo: root.bottomAnchor),
            stack.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: root.trailingAnchor),
        ])
        toasts.attach(to: root)
        view = root
        bind()
    }

    override func viewDidAppear() {
        super.viewDidAppear()
        // Claude Code may have been installed or signed in meanwhile.
        context.assistant?.locator?.refresh()
        context.assistant?.refreshHandlers()
        registration?.load()
        if kind == .claudeCode {
            showClaudeCode()
        }
    }

    private func toast(_ text: String) {
        guard !closed else { return }
        toasts.show(text)
    }

    /// The sheet went away: late replies are dropped, the application's
    /// controller goes on without it.
    func close() {
        guard !closed else { return }
        closed = true
        registration?.close()
        claudeDesktop?.onChange = nil
        claudeDesktop?.onToast = nil
        assistantToken?.cancel()
        assistantToken = nil
        claudePathToken?.cancel()
        claudePathToken = nil
        // A sign-in under way goes on: the user is in the browser.
        signInToken?.cancel()
        signInToken = nil
    }

    @objc private func closeClicked(_ sender: Any?) {
        onClose?()
    }

    override func cancelOperation(_ sender: Any?) {
        onClose?()
    }

    // MARK: Binding (preferences.go `bindMCP`, now claude_integration.go)

    private func bind() {
        let c = MCPRegistrationController(bridge: context.bridge, client: Integrations.clientID(kind))
        registration = c
        let assistant = context.assistant
        // The row's sensitivity covers the switch inside it.
        c.onEnabled = { [weak self] _ in
            self?.updateRegisterRow()
        }
        c.onRegistered = { [weak self, weak c] on in
            self?.setSwitch(on)
            self?.updateRegisterRow()
            // The Assistant menus and the pages know at once.
            if let status = c?.status {
                assistant?.apply(status)
            }
        }
        c.onToast = { [weak self] text in
            self?.toast(text)
        }
        setSwitch(c.isRegistered)
        registerSwitch.target = self
        registerSwitch.action = #selector(registerChanged(_:))
        // The application's last status, at once: the switch does not show
        // "off" only because this sheet has not asked yet.
        if let known = assistant?.status {
            c.adopt(known)
        }
        bindClaudeDesktop()
        assistantToken = assistant?.onChange { [weak self] in
            self?.followApplication()
        }
        guard kind == .claudeCode else { return }
        claudePathToken = context.settings.onChange(.assistantClaudePath) { [weak self] in self?.claudePathChanged() }
        // A sign-in started or ended, here or in the panel: the row looks
        // again.
        signInToken = assistant?.locator?.onSignInChange { [weak self] in
            self?.showClaudeCode()
        }
        showClaudeCode()
    }

    /// A status the application learned elsewhere is shown by the switch
    /// too, so both follow one state. Not while the dialog is up or a write
    /// or restart runs: the switch shows the user's choice until its
    /// answer comes.
    private func followApplication() {
        guard !asking, !(claudeDesktop?.busy ?? false), let known = context.assistant?.status else { return }
        registration?.adopt(known)
    }

    private func setSwitch(_ on: Bool) {
        syncing = true
        registerSwitch.state = on ? .on : .off
        syncing = false
    }

    @objc private func registerChanged(_ sender: Any?) {
        guard !syncing, let registration else { return }
        let want = registerSwitch.state == .on
        guard let desktop = claudeDesktop else {
            registration.set(registered: want)
            return
        }
        // Asks first while Claude Desktop runs (`offersRestart`), then
        // writes through this sheet's controller.
        let status = registration.status
        let write = pageWrite(desktop)
        Task { @MainActor [weak self] in
            await desktop.change(registered: want, status: status, ask: { [weak self] in
                await self?.askRestart() ?? .later
            }, write: write)
        }
    }

    // MARK: Claude Desktop (ClaudeDesktopController)

    /// The pending row and the switch follow the controller (Claude
    /// Desktop's sheet; Claude Code's has no pending row).
    private func bindClaudeDesktop() {
        guard let desktop = claudeDesktop else {
            updateRegisterRow()
            return
        }
        desktop.onChange = { [weak self] in self?.updateClaudeDesktop() }
        desktop.onToast = { [weak self] text in self?.toast(text) }
        updateClaudeDesktop()
    }

    /// The pending row while Claude Desktop still has to pick up a change;
    /// the switch and Restart wait while a write or a restart runs.
    private func updateClaudeDesktop() {
        guard !closed else { return }
        if let pendingRow {
            mcpGroup.setRow(pendingRow, hidden: claudeDesktop?.pending == nil)
            pendingRow.isEnabled = !(claudeDesktop?.busy ?? false)
            // The App Store build cannot quit Claude Desktop: the row only says so.
            restartButton.isHidden = !(claudeDesktop?.canRestart ?? true)
        }
        updateRegisterRow()
    }

    /// The switch's row: a bridge, a known status, no call in flight
    /// (`MCPRegistrationController.isEnabled`), no restart running, and the
    /// app installed or the bridge registered in it
    /// (`Integrations.registerSwitchFor`, whose subtitle says when it is
    /// not installed).
    private func updateRegisterRow() {
        guard let row = registerRow else { return }
        let r = registration
        let sw = Integrations.registerSwitchFor(
            kind, known: r?.status != nil, present: r?.isPresent ?? false, registered: r?.isRegistered ?? false)
        if !TestBuild.isActive {
            row.subtitle = sw.subtitle
        }
        // Before the first status the row waits as it always did; once one
        // is known, an app that is neither present nor registered stays
        // insensitive.
        let sensitive = r?.status == nil || sw.sensitive
        row.isEnabled = (r?.isEnabled ?? false) && !(claudeDesktop?.busy ?? false) && sensitive
    }

    /// "Restart Claude Desktop?" as a sheet on this window.
    private func askRestart() async -> ClaudeDesktopController.Answer {
        guard let confirmRestart = context.confirmRestart else { return .later }
        asking = true
        defer { asking = false }
        return await confirmRestart(view.window) ? .restart : .later
    }

    /// Writes through this sheet's controller, so its switch and row
    /// follow; once the sheet closed (a restart may wait up to 20 s for the
    /// quit) through the application's controller instead.
    private func pageWrite(_ desktop: ClaudeDesktopController) -> ClaudeDesktopController.Write {
        { [weak self] want in
            if let registration = self?.registration, !registration.closed {
                return await registration.change(registered: want)
            }
            return await desktop.writeOwn(registered: want)
        }
    }

    /// The pending row's Restart: the same restart, with the pending state.
    @objc private func restartClaudeDesktop(_ sender: Any?) {
        guard let desktop = claudeDesktop, !closed else { return }
        let write = pageWrite(desktop)
        Task { @MainActor in
            await desktop.restartPending(write: write)
        }
    }

    // MARK: Claude Code (claude_integration.go `bindClaudeCode`)

    /// The Claude Code row's subtitle: the executable the panel runs, its
    /// version and whether it is signed in (asked once, then kept by the
    /// locator until the sheet comes up again, the path changes or a
    /// sign-in starts or ends), or that none was found; and what its second
    /// button offers.
    private func showClaudeCode() {
        guard let row = claudeCodeRow, !closed else { return }
        claudeCodeGen += 1
        let gen = claudeCodeGen
        guard let locator = context.assistant?.locator, let path = locator.locate() else {
            row.subtitle = Assistant.problem(.app, Assistant.Availability())
            setClaudeCodeOffer(.install)
            return
        }
        if !row.subtitle.hasPrefix(path) {
            row.subtitle = path
            setClaudeCodeOffer(.none)
        }
        Task { @MainActor [weak self] in
            let version = await locator.version()
            let signedIn = await locator.signedIn()
            guard let self, !self.closed, gen == self.claudeCodeGen else { return }
            self.claudeCodeRow?.subtitle = Self.claudeCodeState(
                path: path, version: version, signedIn: signedIn, signingIn: locator.signingIn)
            self.setClaudeCodeOffer(signedIn == false ? .signIn : .none)
        }
    }

    /// "path · version · Signed in"; what is not known is left out, and
    /// while a sign-in is under way (`signingIn`) the row says that it
    /// waits for the browser instead of "Not signed in".
    static func claudeCodeState(path: String, version: String?, signedIn: Bool?, signingIn: Bool = false) -> String {
        let texts = Assistant.panelTexts()
        var parts = [path]
        if let version {
            parts.append(version)
        }
        if signedIn == true {
            parts.append(texts.signedIn)
        } else if signingIn {
            parts.append(Assistant.signInTexts().waiting)
        } else if signedIn == false {
            parts.append(texts.notSignedInShort)
        }
        return parts.joined(separator: " · ")
    }

    /// The row's second button: its title, and hidden while the row offers
    /// nothing.
    private func setClaudeCodeOffer(_ offer: AssistantPanelController.Offer) {
        claudeCodeOffer = offer
        if offer != .none {
            claudeCodeOfferButton.title = AssistantMessageLineView.offerLabel(offer)
        }
        claudeCodeOfferButton.isHidden = offer == .none
    }

    /// Sign In… runs Claude Code's own sign-in in the browser (the
    /// locator's, which the row follows through `onSignInChange`; a
    /// failure or a timeout is said as a toast), Get Claude Code… opens
    /// the page with its installers.
    @objc private func claudeCodeOfferClicked(_ sender: Any?) {
        guard !closed else { return }
        switch claudeCodeOffer {
        case .install:
            openInBrowser(Assistant.installURL) { [weak self] text in
                self?.toast(text)
            }
        case .signIn:
            guard let locator = context.assistant?.locator else { return }
            let run = locator.startSignIn()
            Task { @MainActor [weak self] in
                let result = await run.result
                guard let self, !self.closed else { return }
                switch result {
                case .failed(let reason):
                    self.toast(Assistant.signInFailedText(reason))
                case .timedOut:
                    self.toast(Assistant.signInTexts().timedOut)
                case .done, .cancelled, .notFound:
                    break
                }
            }
        case .none, .reconnectProvider, .setUpInIntegrations:
            break
        }
    }

    /// `assistant-claude-path` changed: Claude Code is looked for again,
    /// and the Assistant menus learn whether the panel can run.
    private func claudePathChanged() {
        guard !closed else { return }
        context.assistant?.locator?.refresh()
        context.assistant?.refreshHandlers()
        showClaudeCode()
    }

    /// "Choose…": the claude executable the panel should run, in an open
    /// panel that offers executable files only (and keeps a symbolic link
    /// as it is: an nvm `claude` is a link whose `node` sits beside it).
    /// The file found automatically stores nothing, so choosing it goes
    /// back to looking in the usual places.
    @objc private func chooseClaudeCode(_ sender: Any?) {
        guard let window = view.window, !closed else { return }
        let settings = context.settings
        let panel = NSOpenPanel()
        panel.canChooseFiles = true
        panel.canChooseDirectories = false
        panel.allowsMultipleSelection = false
        panel.resolvesAliases = false
        panel.showsHiddenFiles = true
        panel.treatsFilePackagesAsDirectories = true
        let filter = ExecutableFilter()
        chooseFilter = filter
        panel.delegate = filter
        if let current = context.assistant?.locator?.locate() {
            panel.directoryURL = URL(fileURLWithPath: current).deletingLastPathComponent()
        }
        Task { @MainActor [weak self] in
            let response = await panel.beginSheetModal(for: window)
            guard let self else { return }
            self.chooseFilter = nil
            guard response == .OK, let url = panel.url, url.isFileURL, !self.closed else { return }
            let chosen = url.path
            let value = chosen == Self.automaticClaudePath() ? "" : chosen
            if settings.assistantClaudePath == value {
                self.claudePathChanged()
            } else {
                // The change handler looks again.
                settings.assistantClaudePath = value
            }
        }
    }

    /// The claude executable found without the setting, as
    /// `ClaudeCodeLocator` looks: the usual places, then the PATH.
    private static func automaticClaudePath() -> String? {
        let env = ProcessInfo.processInfo.environment
        let home = env["HOME"].flatMap { $0.isEmpty ? nil : $0 } ?? NSHomeDirectory()
        let nvm = (try? FileManager.default.contentsOfDirectory(atPath: home + "/.nvm/versions/node")) ?? []
        return Assistant.candidatePaths(home: home, pathEnv: env["PATH"] ?? "", nvmVersions: nvm)
            .first(where: ClaudeCodeLocator.isExecutableFile)
    }
}

/// The open panel of "Choose…" offers folders to walk through and
/// executable files to pick, nothing else.
final class ExecutableFilter: NSObject, NSOpenSavePanelDelegate {
    func panel(_ sender: Any, shouldEnable url: URL) -> Bool {
        var directory: ObjCBool = false
        if FileManager.default.fileExists(atPath: url.path, isDirectory: &directory), directory.boolValue {
            return true
        }
        return ClaudeCodeLocator.isExecutableFile(url.path)
    }
}
