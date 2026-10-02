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
/// Under it the Board group (macOS-only, Swift-first like the board; shown
/// while triage is offered, Core's one rule that the board toolbars'
/// Triage follows too: `BoardTriageController.view.offered` — the
/// Assistant shown with the In App target, and a board the daemon has and
/// has not turned off): "Let the assistant refine the board", the consent
/// of the board's triage (on: the consent sheet first, then
/// `BoardTriageController.giveConsent`; off: `withdrawConsent`, which turns
/// automatic triage off too), "Model" (`board-triage-model`, the triage's
/// own, apart from the Assistant group's; enabled whenever the group is
/// shown), "Triage new mail automatically" with "At
/// most every" and the daily cap (the daemon's `board.preferences` through
/// the application's `BoardPreferencesController`; a value outside the
/// lists shows as an extra item), and a status row from the triage view
/// (`Board.triageSettingsStatus`: last run, today's count, or why
/// automatic triage pauses; Core publishes the view again while it names
/// a relative time, and this page observes it), and under it "Tokens in
/// the Last 24 Hours" from the same view (`usageValue`, the split and the
/// runs as `usageDetail`; shown once a board.list said them, the board
/// asked to list again whenever the page comes up). Triage needs the In App
/// target, under which the Claude Code and Model rows above show; while
/// Claude Code is missing, signed out or signing in, the bridge is
/// missing or the daemon did not answer the board's preferences, the
/// group's description says why (`Board.triageSettingsDescription`) and
/// the rows only let a consent or automatic triage be turned off. All
/// texts are Core's (`Board.Text`).
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

    let boardConsentSwitch = NSSwitch()
    let boardAutoSwitch = NSSwitch()
    /// The triage's own model (`board-triage-model`), apart from the
    /// panel's.
    let boardModel = NSPopUpButton(frame: .zero, pullsDown: false)
    let boardInterval = NSPopUpButton(frame: .zero, pullsDown: false)
    let boardDaily = NSPopUpButton(frame: .zero, pullsDown: false)
    let boardGroup = PreferencesGroupView(title: Board.texts().board)
    /// The application's triage (its view, consent and preferences); nil
    /// hides the Board group.
    private var triage: BoardTriageController?
    /// The consent sheet on this window, before the consent is given.
    private var confirmTriage: PrefsConfirmRestart?
    private var triageToken: BoardObserverToken?
    private var boardConsentRow: PreferenceRowView?
    private var boardAutoRow: PreferenceRowView?
    private var boardIntervalRow: PreferenceRowView?
    private var boardDailyRow: PreferenceRowView?
    private var boardStatusRow: PreferenceRowView?
    /// "Tokens in the Last 24 Hours" and its value (dim, numeric).
    private var boardUsageRow: PreferenceRowView?
    let boardUsageValue = NSTextField(labelWithString: "")
    /// The consent sheet is up or the consent is being stored: the switch
    /// shows the user's choice until then.
    private var givingConsent = false

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
    /// `triage` is the application's board triage (nil: no Board group)
    /// and `confirmTriage` its consent sheet on a window.
    func configure(
        bridge: String?, settings: Settings, assistant: AssistantController?,
        claudeDesktop: ClaudeDesktopController? = nil, confirmRestart: PrefsConfirmRestart? = nil,
        triage: BoardTriageController? = nil, confirmTriage: PrefsConfirmRestart? = nil,
        toast: @escaping @MainActor (String) -> Void
    ) {
        guard registration == nil else { return }
        self.triage = triage
        self.confirmTriage = confirmTriage
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
        addBoardGroup()
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
        // The tokens of the last 24 hours age out without a notification.
        triage?.relistBoard()
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
        bindBoard()
        bindings.onClose = { [weak self] in
            guard let self else { return }
            self.registration?.close()
            self.triageToken?.cancel()
            self.triageToken = nil
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
        updateBoardGroup()
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

// MARK: The Board group (the board's triage)

extension AIPaneViewController {
    /// "At most every": minutes between automatic runs.
    static let boardIntervals = [15, 30, 60, 180]
    /// The daily caps of conversations automatic runs annotate.
    static let boardDailyCaps = [20, 60, 150]

    /// Builds the group's rows (hidden until the triage says it is
    /// offered).
    fileprivate func addBoardGroup() {
        let consent = PreferenceRowView(
            title: Board.Text.triageSettingsConsent, subtitle: Board.Text.triageSettingsConsentSubtitle,
            trailing: boardConsentSwitch)
        boardConsentRow = consent
        // The Assistant group's Model row, for the triage's own model.
        boardModel.addItems(withTitles: Assistant.models.map(Assistant.modelName))
        let model = PreferenceRowView(title: Assistant.panelTexts().model, trailing: boardModel)
        let auto = PreferenceRowView(title: Board.Text.triageSettingsAutomatic, trailing: boardAutoSwitch)
        boardAutoRow = auto
        let interval = PreferenceRowView(title: Board.Text.triageSettingsInterval, trailing: boardInterval)
        boardIntervalRow = interval
        let daily = PreferenceRowView(title: Board.Text.triageSettingsDaily, trailing: boardDaily)
        boardDailyRow = daily
        let status = PreferenceRowView(title: L10n.T("Status"), subtitle: "")
        boardStatusRow = status
        boardUsageValue.textColor = .secondaryLabelColor
        boardUsageValue.font = .monospacedDigitSystemFont(ofSize: 13, weight: .regular)
        boardUsageValue.lineBreakMode = .byTruncatingTail
        boardUsageValue.isSelectable = false
        let usage = PreferenceRowView(title: Board.Text.triageSettingsUsage, subtitle: "", trailing: boardUsageValue)
        boardUsageRow = usage
        boardGroup.setRows([consent, model, auto, interval, daily, status, usage])
        boardGroup.isHidden = true
        addGroup(boardGroup)
    }

    /// Wires the controls and follows the triage (its view, consent and
    /// preferences: `observe` reports all of them, and the view again once
    /// a minute while it names a relative time).
    fileprivate func bindBoard() {
        guard let triage else { return }
        boardConsentSwitch.target = self
        boardConsentSwitch.action = #selector(boardConsentChanged(_:))
        boardAutoSwitch.target = self
        boardAutoSwitch.action = #selector(boardAutoChanged(_:))
        boardInterval.target = self
        boardInterval.action = #selector(boardIntervalChosen(_:))
        boardDaily.target = self
        boardDaily.action = #selector(boardDailyChosen(_:))
        // A client setting like the panel's model, usable whenever the
        // group is shown (choosing it needs no consent); the next run takes
        // it.
        if let settings {
            bindings.add(.bind(boardModel, to: settings, .boardTriageModel, choices: Assistant.models, \.boardTriageModel))
        }
        triageToken = triage.observe { [weak self] in
            self?.updateBoardGroup()
        }
        // The daemon's preferences, should none have come yet.
        if triage.preferences.preferences == nil {
            triage.preferences.load()
        }
        updateBoardGroup()
    }

    /// The group from the triage's view and the board's preferences.
    fileprivate func updateBoardGroup() {
        guard let triage, !closed else {
            boardGroup.isHidden = true
            return
        }
        let v = triage.view
        guard v.offered else {
            boardGroup.isHidden = true
            return
        }
        boardGroup.isHidden = false
        // Why triage cannot run now ("" when it can); the Claude Code row
        // above offers what Claude Code needs.
        boardGroup.descriptionText = Board.triageSettingsDescription(v)
        let ready = v.control == .triage || v.control == .stop
        let prefs = triage.preferences.preferences
        let consent = givingConsent ? boardConsentSwitch.state == .on : triage.consentGiven
        boardConsentSwitch.state = consent ? .on : .off
        // Turning off is always possible; turning on needs a runnable triage.
        boardConsentRow?.isEnabled = prefs != nil && !givingConsent && (ready || consent)
        let auto = prefs?.autoTriage ?? false
        boardAutoSwitch.state = auto ? .on : .off
        boardAutoRow?.isEnabled = prefs != nil && !givingConsent && (auto || (consent && ready))
        let minutes = prefs?.autoTriageMinutes ?? API.Limits.defaultBoardAutoTriageMinutes
        let cap = prefs?.autoTriageDailyCases ?? API.Limits.defaultBoardAutoTriageDailyCases
        Self.fill(boardInterval, values: Self.boardIntervals, selected: minutes) { Board.Text.triageInterval(minutes: $0) }
        Self.fill(boardDaily, values: Self.boardDailyCaps, selected: cap, title: Board.Text.triageDailyCap)
        let schedule = prefs != nil && consent && auto && ready && !givingConsent
        boardIntervalRow?.isEnabled = schedule
        boardDailyRow?.isEnabled = schedule
        boardStatusRow?.subtitle = Board.triageSettingsStatus(v)
        // The tokens of the last 24 hours, from the same view: refreshed
        // with the status row (each board.list, the view's clock).
        if let usage = boardUsageRow {
            boardGroup.setRow(usage, hidden: !v.usageShown)
            boardUsageValue.stringValue = v.usageValue
            usage.subtitle = v.usageDetail
            usage.toolTip = v.usageToolTip
        }
    }

    /// The pop-up's items (`values`, and `selected` as an extra item when
    /// it is not one of them), each tagged with its value, `selected`
    /// chosen.
    static func fill(_ popUp: NSPopUpButton, values: [Int], selected: Int, title: (Int) -> String) {
        let wanted = values.contains(selected) ? values : values + [selected]
        if popUp.itemArray.map(\.tag) != wanted {
            popUp.removeAllItems()
            for value in wanted {
                popUp.addItem(withTitle: title(value))
                popUp.lastItem?.tag = value
            }
        }
        popUp.selectItem(withTag: selected)
    }

    // MARK: Actions

    /// On: the consent sheet on this window, then the consent given (the
    /// board's assistant preference on, once the daemon stored it); a
    /// declined sheet leaves the switch off. Off: withdrawn (a run under
    /// way stops; the panel's own consent stays).
    @objc fileprivate func boardConsentChanged(_ sender: Any?) {
        guard let triage, !closed, !givingConsent else { return }
        guard boardConsentSwitch.state == .on else {
            triage.withdrawConsent()
            updateBoardGroup()
            return
        }
        givingConsent = true
        updateBoardGroup()
        let confirm = confirmTriage
        Task { @MainActor [weak self] in
            // Without a sheet to ask, a consent that is needed is not given.
            var allowed = !triage.needsConsent
            if triage.needsConsent, let confirm {
                allowed = await confirm(self?.view.window)
            }
            if allowed {
                _ = await triage.giveConsent()
            }
            guard let self else { return }
            self.givingConsent = false
            self.updateBoardGroup()
        }
    }

    @objc fileprivate func boardAutoChanged(_ sender: Any?) {
        guard let triage, !closed else { return }
        let on = boardAutoSwitch.state == .on
        // Optimistic; a refused write is taken back and toasted by the
        // application (`BoardPreferencesController.onError`).
        triage.preferences.update({ $0.autoTriage = on }, completion: nil)
    }

    @objc fileprivate func boardIntervalChosen(_ sender: Any?) {
        guard let triage, !closed, let minutes = boardInterval.selectedItem?.tag else { return }
        guard triage.preferences.preferences?.autoTriageMinutes != minutes else { return }
        triage.preferences.update({ $0.autoTriageMinutes = minutes }, completion: nil)
    }

    @objc fileprivate func boardDailyChosen(_ sender: Any?) {
        guard let triage, !closed, let cases = boardDaily.selectedItem?.tag else { return }
        guard triage.preferences.preferences?.autoTriageDailyCases != cases else { return }
        triage.preferences.update({ $0.autoTriageDailyCases = cases }, completion: nil)
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
