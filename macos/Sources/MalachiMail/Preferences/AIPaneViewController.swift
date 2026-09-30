// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The AI page of the settings (preferences.blp `ai_page`,
/// ui/internal/window/preferences.go `bindMCP`): the MCP group with its one
/// switch, "Register with Claude", which puts the bundled `malachi-mcp`
/// bridge into the MCP configuration of Claude Desktop and Claude Code (or
/// takes it out) through `MCPRegistrationController`. The row is
/// insensitive until the bridge answered `status` and while a call runs;
/// the switch shows what the bridge last confirmed and flips back when a
/// call fails, with a toast that says why.
///
/// Claude Desktop overwrites a change of its MCP servers while it runs and
/// reads them only when it starts (docs/mcp.md), so a flip while it runs
/// and is a present client asks "Restart Claude Desktop?" first
/// (`ClaudeDesktopController`, the application's): Restart quits it, writes
/// the change through this page's controller and starts it again; Later
/// writes the change now and leaves it pending. While something is pending
/// a row under the switch says Claude Desktop picks it up when it restarts,
/// with Restart; the switch and that button wait while a restart runs.
/// GTK has no equivalent yet: this client leads.
///
/// Under it the Assistant group (ui/internal/assistant; GTK preferences.blp
/// `assistant_group`, preferences.go `bindAssistant`, without the In App
/// rows): "Show the Assistant Menu" (`assistant-menu`) and
/// "Open In" with Claude Desktop, Claude Code and In App (Experimental)
/// (`assistant-target`). While In App is chosen two more rows follow:
/// "Claude Code", the executable the panel runs (its path, version and
/// whether it is signed in, from the application's `ClaudeCodeLocator`,
/// or that it was not found), with "Choose…" for one of the user's own
/// (`assistant-claude-path`; choosing the one found automatically goes
/// back to looking), and "Model" (`assistant-model`). In front of
/// "Choose…" the Claude Code row has the button of what it offers (GTK
/// preferences.go `bindClaudeCode`): "Sign In…" while Claude Code says it
/// is signed out, which runs Claude Code's own sign-in in the browser (the
/// application's locator, shared with the panel: the row says "Waiting for
/// the sign-in in your browser…" whoever started it, and a second click
/// starts it afresh), and "Get Claude Code…" while there is none, which
/// opens Anthropic's page with the installers. The
/// group depends on "Register with Claude" (`Assistant.shown`): while the
/// bridge is registered in no client both rows are insensitive, the switch
/// shows off whatever the preference holds and says why, so the Assistant
/// cannot be turned on without the bridge; the preference keeps its value
/// for when the bridge is registered again. Otherwise the switch shows the
/// preference and "Open In" says why the chosen app cannot run the message
/// actions (`Assistant.problem` over the application's
/// `AssistantController`), nothing when it can. Every status the MCP switch
/// gets goes to that controller too, so the menus and this group know at
/// once.
///
/// The bridge path (or its absence), the settings, the assistant and the
/// toast sink come through `configure`; the controller exists once they
/// and the view are there, and the status is asked every time the page
/// comes up. Everything ends when the window closes (the GTK dialog's
/// `closed`), which the pane notices itself.
@MainActor
final class AIPaneViewController: PreferencesPaneViewController {
    let registerSwitch = NSSwitch()
    let mcpGroup = PreferencesGroupView(
        title: L10n.T("MCP"),
        description: L10n.T("Lets AI assistants read your mail and prepare drafts through the Model Context Protocol.")
    )

    let assistantMenuSwitch = NSSwitch()
    let assistantTarget = NSPopUpButton(frame: .zero, pullsDown: false)
    let assistantModel = NSPopUpButton(frame: .zero, pullsDown: false)
    let claudeCodeChoose = NSButton(title: Assistant.panelTexts().choose, target: nil, action: nil)
    /// What the Claude Code row offers: Sign In… or Get Claude Code….
    let claudeCodeOfferButton = NSButton(title: "", target: nil, action: nil)
    let assistantGroup = PreferencesGroupView(
        title: Assistant.texts().assistant,
        description: Assistant.texts().description
    )

    /// The MCP controller; nil until `configure` was called and the view
    /// loaded.
    private(set) var registration: MCPRegistrationController?
    /// The window closed (or the lead said so): late replies dropped.
    var closed: Bool {
        get { bindings.closed }
        set {
            if newValue {
                bindings.close()
            }
        }
    }

    private var bridge: String?
    private var settings: Settings?
    private var assistant: AssistantController?
    private var assistantToken: AssistantController.Token?
    private var targetToken: Settings.ChangeToken?
    private var targetRow: PreferenceRowView?
    /// The panel's rows (In App): Claude Code and the model.
    private var claudeCodeRow: PreferenceRowView?
    private var modelRow: PreferenceRowView?
    private var claudePathToken: Settings.ChangeToken?
    /// A sign-in started or ended, here or in the panel.
    private var signInToken: ClaudeCodeLocator.SignInToken?
    /// Bumped by every look at Claude Code: a late answer is dropped.
    private var claudeCodeGen = 0
    /// What the Claude Code row's second button does.
    private var claudeCodeOffer: AssistantPanelController.Offer = .none
    /// The open panel's filter while "Choose…" is up.
    private var chooseFilter: ExecutableFilter?
    /// The application's Claude Desktop controller and its question; nil
    /// writes every change at once.
    private var claudeDesktop: ClaudeDesktopController?
    private var confirmRestart: PrefsConfirmRestart?
    /// "Claude Desktop picks up the change when it restarts" with Restart,
    /// under the switch while a change is pending.
    private var pendingRow: PreferenceRowView?
    let restartButton = NSButton(title: Assistant.restartTexts().restartNow, target: nil, action: nil)
    private var menuRow: PreferenceRowView?
    private var toast: (@MainActor (String) -> Void)?
    private var configured = false
    private let bindings = PreferenceBindingSet()
    private var registerRow: PreferenceRowView?
    /// The switch is being set from the controller, not by the user.
    private var syncing = false
    /// "Restart Claude Desktop?" is up: the switch shows the user's choice
    /// until it is written, whatever the application learns meanwhile.
    private var asking = false

    init() {
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Supplies the bridge path (`Paths.mcpBridge`; nil when there is none
    /// beside the application), the settings, the application's assistant
    /// (nil leaves "Open In" without its subtitle), its Claude Desktop
    /// controller with the question (nil: every change is written at once)
    /// and the toast sink; may be called before or after the view loaded. A
    /// second call after the controller started is ignored.
    func configure(
        bridge: String?, settings: Settings, assistant: AssistantController?,
        claudeDesktop: ClaudeDesktopController? = nil, confirmRestart: PrefsConfirmRestart? = nil,
        toast: @escaping @MainActor (String) -> Void
    ) {
        guard registration == nil else { return }
        self.bridge = bridge
        self.settings = settings
        self.assistant = assistant
        self.claudeDesktop = claudeDesktop
        self.confirmRestart = confirmRestart
        self.toast = toast
        configured = true
        bindIfReady()
    }

    override func loadView() {
        super.loadView()
        let row = PreferenceRowView(
            title: L10n.T("Register with Claude"),
            subtitle: L10n.T("Adds the malachi-mcp bridge to Claude Desktop and Claude Code on this computer"),
            trailing: registerSwitch
        )
        row.isEnabled = false
        registerRow = row
        let restart = Assistant.restartTexts()
        restartButton.target = self
        restartButton.action = #selector(restartClaudeDesktop(_:))
        let pending = PreferenceRowView(
            title: Assistant.targetName(.desktop), subtitle: restart.pending, trailing: restartButton)
        pendingRow = pending
        mcpGroup.setRows([row, pending])
        mcpGroup.setRow(pending, hidden: true)
        addGroup(mcpGroup)

        let texts = Assistant.texts()
        assistantTarget.addItems(withTitles: AssistantController.targets.map(Assistant.targetName))
        let target = PreferenceRowView(title: texts.openIn, trailing: assistantTarget)
        targetRow = target
        let menu = PreferenceRowView(title: texts.showMenu, trailing: assistantMenuSwitch)
        menuRow = menu
        let panelTexts = Assistant.panelTexts()
        claudeCodeChoose.target = self
        claudeCodeChoose.action = #selector(chooseClaudeCode(_:))
        claudeCodeOfferButton.target = self
        claudeCodeOfferButton.action = #selector(claudeCodeOfferClicked(_:))
        claudeCodeOfferButton.isHidden = true
        // What the row offers, then Choose…; the hidden button leaves the
        // row.
        let claudeCodeButtons = NSStackView(views: [claudeCodeOfferButton, claudeCodeChoose])
        claudeCodeButtons.orientation = .horizontal
        claudeCodeButtons.alignment = .centerY
        claudeCodeButtons.spacing = 6
        let claudeCode = PreferenceRowView(title: Assistant.targetName(.code), trailing: claudeCodeButtons)
        claudeCode.setSubtitleSelectable()
        claudeCodeRow = claudeCode
        assistantModel.addItems(withTitles: Assistant.models.map(Assistant.modelName))
        let model = PreferenceRowView(title: panelTexts.model, trailing: assistantModel)
        modelRow = model
        assistantGroup.setRows([menu, target, claudeCode, model])
        assistantGroup.setRow(claudeCode, hidden: true)
        assistantGroup.setRow(model, hidden: true)
        addGroup(assistantGroup)
    }

    override func viewDidLoad() {
        super.viewDidLoad()
        bindIfReady()
    }

    /// The controller ends with the window.
    override func viewWillAppear() {
        super.viewWillAppear()
        bindings.watch(view.window)
    }

    /// The status is asked whenever the page comes up; the Claude apps'
    /// handlers are looked up again.
    override func viewDidAppear() {
        super.viewDidAppear()
        registration?.load()
        // Claude Code may have been installed or signed in meanwhile.
        assistant?.locator?.refresh()
        assistant?.refreshHandlers()
        updateAssistantGroup()
    }

    // MARK: Binding (preferences.go `bindMCP`)

    private func bindIfReady() {
        guard registration == nil, isViewLoaded, !closed, configured else { return }
        let c = MCPRegistrationController(bridge: bridge)
        registration = c
        // The row's sensitivity covers the switch inside it.
        c.onEnabled = { [weak self] _ in
            self?.updateRegisterRow()
        }
        c.onRegistered = { [weak self, weak c] on in
            self?.setSwitch(on)
            // The Assistant menus know at once (and this page's group).
            if let status = c?.status {
                self?.assistant?.apply(status)
            }
            self?.updateAssistantGroup()
        }
        c.onToast = { [weak self] text in
            self?.toast?(text)
        }
        setSwitch(c.isRegistered)
        registerSwitch.target = self
        registerSwitch.action = #selector(registerChanged(_:))
        // The application's last status, at once: the switch does not show
        // "off" only because this page has not asked yet (it asks in
        // `viewDidAppear`).
        if let known = assistant?.status {
            c.adopt(known)
        }
        bindClaudeDesktop()
        bindAssistant()
        bindings.onClose = { [weak self] in
            guard let self else { return }
            self.registration?.close()
            // The controller is the application's: it goes on without the page.
            self.claudeDesktop?.onChange = nil
            self.claudeDesktop?.onToast = nil
            self.assistantToken?.cancel()
            self.assistantToken = nil
            self.targetToken?.cancel()
            self.targetToken = nil
            self.claudePathToken?.cancel()
            self.claudePathToken = nil
            // A sign-in under way goes on: the user is in the browser.
            self.signInToken?.cancel()
            self.signInToken = nil
        }
    }

    // MARK: The Assistant group (ui/internal/assistant)

    /// Binds the choice to its key; the switch is set by hand, because
    /// what it shows depends on the bridge too (`updateAssistantGroup`).
    /// The group follows the preferences and what the assistant knows
    /// (whose changes include `assistant-menu`'s).
    private func bindAssistant() {
        guard let settings else { return }
        assistantMenuSwitch.target = self
        assistantMenuSwitch.action = #selector(assistantMenuChanged(_:))
        bindings.add(.bind(assistantTarget, to: settings, .assistantTarget, choices: AssistantController.targets, \.assistantTarget))
        bindings.add(.bind(assistantModel, to: settings, .assistantModel, choices: Assistant.models, \.assistantModel))
        claudePathToken = settings.onChange(.assistantClaudePath) { [weak self] in self?.claudePathChanged() }
        // A sign-in started or ended, here or in the panel: the row looks
        // again.
        signInToken = assistant?.locator?.onSignInChange { [weak self] in
            guard let self, !self.closed, let settings = self.settings, settings.assistantTarget == .app else { return }
            self.showClaudeCode()
        }
        assistantToken = assistant?.onChange { [weak self] in
            self?.followApplication()
            self?.updateAssistantGroup()
        }
        targetToken = settings.onChange(.assistantTarget) { [weak self] in self?.updateAssistantGroup() }
        updateAssistantGroup()
    }

    /// A status the application learned (at launch, when it became
    /// active, when a menu opened, after a write of Claude Desktop's
    /// restart) is shown by the MCP switch too, so both switches follow one
    /// state. Not while the dialog is up or a write or restart runs: the
    /// switch shows the user's choice until its answer comes.
    private func followApplication() {
        guard !asking, !(claudeDesktop?.busy ?? false), let known = assistant?.status else { return }
        registration?.adopt(known)
    }

    /// Whether the bridge is registered in at least one client: what the
    /// application's assistant last knew, else this page's own status.
    private var bridgeRegistered: Bool {
        assistant?.registered ?? registration?.isRegistered ?? false
    }

    /// Whether any status is known: the application's or this page's.
    private var bridgeKnown: Bool {
        assistant?.status != nil || registration?.status != nil
    }

    /// The group for the bridge's state (`Assistant.shown`): registered,
    /// the switch shows the preference and "Open In" why the chosen app
    /// cannot run the message actions, nothing when it can; known not to
    /// be registered, both rows are insensitive and the switch is off and
    /// says why; not known yet (no status answered anywhere), both rows are
    /// insensitive and the switch shows the preference without a reason,
    /// rather than an "off" that may not be true.
    private func updateAssistantGroup() {
        guard let settings, !closed else { return }
        let registered = bridgeRegistered
        let unknown = !registered && !bridgeKnown
        menuRow?.isEnabled = registered
        targetRow?.isEnabled = registered
        let on = unknown ? settings.assistantMenu : Assistant.shown(menu: settings.assistantMenu, registered: registered)
        assistantMenuSwitch.state = on ? .on : .off
        menuRow?.subtitle = registered || unknown ? "" : Assistant.texts().registerFirst
        // In App: the Claude Code row says what is wrong with it.
        let app = settings.assistantTarget == .app
        targetRow?.subtitle = registered && !app ? (assistant?.problem(settings.assistantTarget) ?? "") : ""
        for row in [claudeCodeRow, modelRow].compactMap({ $0 }) {
            assistantGroup.setRow(row, hidden: !app)
            row.isEnabled = registered
        }
        if app {
            showClaudeCode()
        }
    }

    // MARK: Claude Code (the In App target)

    /// The Claude Code row's subtitle: the executable the panel runs, its
    /// version and whether it is signed in (asked once, then kept by the
    /// locator until the page comes up again, the path changes or a
    /// sign-in starts or ends), or that none was found; and what its second
    /// button offers.
    private func showClaudeCode() {
        guard let row = claudeCodeRow, !closed else { return }
        claudeCodeGen += 1
        let gen = claudeCodeGen
        guard let locator = assistant?.locator, let path = locator.locate() else {
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
        // A hidden button is out of the row (the stack detaches it) and
        // misses the row's sensitivity meanwhile.
        claudeCodeOfferButton.isEnabled = (claudeCodeRow?.isEnabled ?? true) && assistantGroup.isEnabled
    }

    /// The row's second button: Sign In… runs Claude Code's own sign-in in
    /// the browser (the locator's, which the row follows through
    /// `onSignInChange`; a failure or a timeout is said as the page says
    /// its other errors), Get Claude Code… opens the page with its
    /// installers.
    @objc private func claudeCodeOfferClicked(_ sender: Any?) {
        guard !closed else { return }
        switch claudeCodeOffer {
        case .install:
            openInBrowser(Assistant.installURL) { [weak self] text in
                guard let self, !self.closed else { return }
                self.toast?(text)
            }
        case .signIn:
            guard let locator = assistant?.locator else { return }
            let run = locator.startSignIn()
            Task { @MainActor [weak self] in
                let result = await run.result
                guard let self, !self.closed else { return }
                switch result {
                case .failed(let reason):
                    self.toast?(Assistant.signInFailedText(reason))
                case .timedOut:
                    self.toast?(Assistant.signInTexts().timedOut)
                case .done, .cancelled, .notFound:
                    break
                }
            }
        case .none:
            break
        }
    }

    /// `assistant-claude-path` changed: Claude Code is looked for again,
    /// and the Assistant menus learn whether the panel can run.
    private func claudePathChanged() {
        assistant?.locator?.refresh()
        assistant?.refreshHandlers()
        updateAssistantGroup()
    }

    /// "Choose…": the claude executable the panel should run, in an open
    /// panel that offers executable files only (and keeps a symbolic link
    /// as it is: an nvm `claude` is a link whose `node` sits beside it).
    /// The file found automatically stores nothing, so choosing it goes
    /// back to looking in the usual places.
    @objc private func chooseClaudeCode(_ sender: Any?) {
        guard let settings, let window = view.window, !closed else { return }
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
        if let current = assistant?.locator?.locate() {
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

    /// The user flipped "Show the Assistant Menu"; the row is insensitive
    /// while the bridge is not registered, so this only writes the
    /// preference while it is.
    @objc private func assistantMenuChanged(_ sender: Any?) {
        guard let settings, bridgeRegistered else {
            updateAssistantGroup()
            return
        }
        settings.assistantMenu = assistantMenuSwitch.state == .on
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
        // writes through this page's controller.
        let status = registration.status
        let write = pageWrite(desktop)
        Task { @MainActor [weak self] in
            await desktop.change(registered: want, status: status, ask: { [weak self] in
                await self?.askRestart() ?? .later
            }, write: write)
        }
    }

    // MARK: Claude Desktop (ClaudeDesktopController)

    /// The pending row and the switch follow the controller.
    private func bindClaudeDesktop() {
        guard let desktop = claudeDesktop else {
            updateRegisterRow()
            return
        }
        desktop.onChange = { [weak self] in self?.updateClaudeDesktop() }
        desktop.onToast = { [weak self] text in self?.toast?(text) }
        updateClaudeDesktop()
    }

    /// The pending row while Claude Desktop still has to pick up a change;
    /// the switch and Restart wait while a write or a restart runs.
    private func updateClaudeDesktop() {
        guard !closed else { return }
        if let pendingRow {
            mcpGroup.setRow(pendingRow, hidden: claudeDesktop?.pending == nil)
            pendingRow.isEnabled = !(claudeDesktop?.busy ?? false)
        }
        updateRegisterRow()
    }

    /// The switch's row: a bridge, a known status, no call in flight
    /// (`MCPRegistrationController.isEnabled`) and no restart running.
    private func updateRegisterRow() {
        registerRow?.isEnabled = (registration?.isEnabled ?? false) && !(claudeDesktop?.busy ?? false)
    }

    /// "Restart Claude Desktop?" as a sheet on this window.
    private func askRestart() async -> ClaudeDesktopController.Answer {
        guard let confirmRestart else { return .later }
        asking = true
        defer { asking = false }
        return await confirmRestart(view.window) ? .restart : .later
    }

    /// Writes through this page's controller, so its switch and row follow;
    /// once the window closed (a restart may wait up to 20 s for the quit)
    /// through the application's controller instead.
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
