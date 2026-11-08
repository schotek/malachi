// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The AI page of the settings (preferences.blp `ai_page`,
/// ui/internal/window/preferences.go `bindAssistant`, docs/plans/ai-api.md
/// §7.3): About You at the top (`AboutYouSection`), then the Assistant
/// group: "Assistant Uses" (`assistant-provider`: Claude Code, ChatGPT, or
/// an AI provider account of the daemon — `AssistantController
/// .providerChoices`, an entry that cannot run disabled with why as its
/// tooltip, the chosen one's reason as the row's subtitle), "Show the
/// Assistant Menu" (`assistant-menu`), "Open In" with Claude Desktop,
/// Claude Code and In App (Experimental) (`assistant-target`), and while
/// In App is chosen the "Model" row of the provider in effect
/// (`ProviderModelBinding`: Claude Code's fixed list, ChatGPT's Codex
/// catalogue, or the account's models, saved to the account). The MCP
/// switch, the Claude Code row and the ChatGPT group moved to the
/// Integrations page's dialogs (`ClaudeIntegrationSheetController`,
/// `ChatGPTIntegrationSheetController`).
///
/// Over Claude Code the group depends on "Register with Claude"
/// (`Assistant.shown`): while the bridge is registered in no client both
/// rows are insensitive, the switch shows off whatever the preference
/// holds and says to register under Integrations, so the Assistant cannot
/// be turned on without the bridge; the preference keeps its value for
/// when the bridge is registered again. Over ChatGPT or an account with
/// the In App target the preference alone counts. "Open In" says why the
/// chosen app cannot run the message actions (`AssistantController
/// .problem`), nothing when it can.
///
/// Under it the Board group (macOS-only, Swift-first like the board; shown
/// while triage is offered, Core's one rule that the board toolbars'
/// Triage follows too: `BoardTriageController.view.offered`): "Let the
/// assistant refine the board", the consent of the board's triage (on:
/// the consent sheet first, then `BoardTriageController.giveConsent`;
/// off: `withdrawConsent`, which turns automatic triage off too; its
/// subtitle names where the mail goes: Claude Code's words, ChatGPT's, or
/// the account's destination), "Model" (the triage's own, for the
/// provider in effect: `board-triage-model`, `board-triage-chatgpt-model`
/// or the account's `boardModel` with "The account's model" first),
/// "Triage new mail automatically" with "At most every" and the daily cap
/// (the daemon's `board.preferences` through the application's
/// `BoardPreferencesController`; a value outside the lists shows as an
/// extra item), and a status row from the triage view
/// (`Board.triageSettingsStatus`), and under it "Tokens in the Last 24
/// Hours" from the same view. All texts are Core's (`Board.Text`).
///
/// The settings, the assistant, the daemon's client and the toast sink
/// come through `configure`; the bindings exist once they and the view
/// are there. Everything ends when the window closes (the GTK dialog's
/// `closed`), which the pane notices itself.
@MainActor
final class AIPaneViewController: PreferencesPaneViewController {
    /// About You and the assistant's suggestions (`AboutYouSection`), at
    /// the top of the page once `configureProfile` gave the store.
    private var aboutYou: AboutYouSection?
    private var profileStore: ProfileStore?

    /// Assistant Uses (`assistant-provider`).
    let assistantSource = NSPopUpButton(frame: .zero, pullsDown: false)
    let assistantMenuSwitch = NSSwitch()
    let assistantTarget = NSPopUpButton(frame: .zero, pullsDown: false)
    let assistantModel = NSPopUpButton(frame: .zero, pullsDown: false)
    /// The Assistant group's header button while nothing is set up
    /// (`Assistant.nothingSetUp`): opens Settings → Integrations.
    let assistantSetUpButton = NSButton(title: Assistant.apiText().setUpInIntegrations, target: nil, action: nil)
    lazy var assistantGroup = PreferencesGroupView(
        title: Assistant.texts().assistant,
        description: Assistant.texts().description,
        headerSuffix: assistantSetUpButton
    )

    let boardConsentSwitch = NSSwitch()
    let boardAutoSwitch = NSSwitch()
    /// The triage's own model, apart from the panel's.
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
    fileprivate var boardConsentRow: PreferenceRowView?
    fileprivate var boardModelRow: PreferenceRowView?
    fileprivate var boardAutoRow: PreferenceRowView?
    fileprivate var boardIntervalRow: PreferenceRowView?
    fileprivate var boardDailyRow: PreferenceRowView?
    fileprivate var boardStatusRow: PreferenceRowView?
    /// "Tokens in the Last 24 Hours" and its value (dim, numeric).
    fileprivate var boardUsageRow: PreferenceRowView?
    /// Triage These Accounts: a checkbox per enabled account
    /// (`BoardPreferencesController.triageAccountChecked`).
    fileprivate var boardAccountsRow: PreferenceRowView?
    fileprivate let boardAccountsStack = NSStackView()
    /// The accounts as account.list last answered, and the checkboxes of
    /// the enabled ones, in that order.
    fileprivate var boardAccounts: [Account] = []
    fileprivate var boardAccountBoxes: [(id: AccountID, box: NSButton)] = []
    fileprivate var boardAccountsGeneration = 0
    /// The daemon's client, for Triage These Accounts and the models of an
    /// AI provider account (nil: the rows stay without them).
    fileprivate var client: RPCClient?
    let boardUsageValue = NSTextField(labelWithString: "")
    /// The consent sheet is up or the consent is being stored: the switch
    /// shows the user's choice until then.
    fileprivate var givingConsent = false
    /// The Model rows following the provider in effect.
    private var modelBinding: ProviderModelBinding?
    fileprivate var boardModelBinding: ProviderModelBinding?

    /// The window closed (or the lead said so): late replies dropped.
    var closed: Bool {
        get { bindings.closed }
        set {
            if newValue {
                bindings.close()
            }
        }
    }

    fileprivate var settings: Settings?
    fileprivate var assistant: AssistantController?
    private var assistantToken: AssistantController.Token?
    private var targetToken: Settings.ChangeToken?
    private var providerToken: Settings.ChangeToken?
    private var sourceRow: PreferenceRowView?
    private var targetRow: PreferenceRowView?
    private var modelRow: PreferenceRowView?
    private var menuRow: PreferenceRowView?
    fileprivate var toast: (@MainActor (String) -> Void)?
    private var configured = false
    private var bound = false
    fileprivate let bindings = PreferenceBindingSet()
    /// The entries of Assistant Uses as shown, in the pop-up's order.
    private var choices: [Assistant.ProviderChoice] = []
    /// The pop-up is being filled from the state, not by the user.
    private var syncingSource = false

    init() {
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Supplies the settings, the application's assistant (nil leaves
    /// "Open In" without its subtitle and Assistant Uses with the local
    /// entries only), the board's triage (nil: no Board group) with its
    /// consent sheet, the daemon's client and the toast sink; may be
    /// called before or after the view loaded. A second call after the
    /// bindings started is ignored.
    func configure(
        settings: Settings, assistant: AssistantController?, triage: BoardTriageController? = nil,
        confirmTriage: PrefsConfirmRestart? = nil, client: RPCClient? = nil, toast: @escaping @MainActor (String) -> Void
    ) {
        guard !configured else { return }
        self.triage = triage
        self.client = client
        self.confirmTriage = confirmTriage
        self.settings = settings
        self.assistant = assistant
        self.toast = toast
        configured = true
        bindIfReady()
    }

    override func loadView() {
        super.loadView()
        let texts = Assistant.texts()
        let x = Assistant.apiText()
        assistantSource.autoenablesItems = false
        let source = PreferenceRowView(title: x.usesRow, subtitle: x.usesSubtitle, trailing: assistantSource)
        sourceRow = source
        assistantTarget.addItems(withTitles: AssistantController.targets.map(Assistant.targetName))
        let target = PreferenceRowView(title: texts.openIn, trailing: assistantTarget)
        targetRow = target
        let menu = PreferenceRowView(title: texts.showMenu, trailing: assistantMenuSwitch)
        menuRow = menu
        let model = PreferenceRowView(title: Assistant.panelTexts().model, trailing: assistantModel)
        modelRow = model
        assistantGroup.setRows([source, menu, target, model])
        assistantGroup.setRow(model, hidden: true)
        addGroup(assistantGroup)
    }

    override func viewDidLoad() {
        super.viewDidLoad()
        bindIfReady()
        installAboutYou()
    }

    /// The application's profile for About You; may come before or after
    /// the view loaded.
    func configureProfile(_ store: ProfileStore) {
        guard profileStore == nil else { return }
        profileStore = store
        installAboutYou()
    }

    /// About You and its suggestions go above every other group.
    private func installAboutYou() {
        guard aboutYou == nil, isViewLoaded, let store = profileStore else { return }
        let section = AboutYouSection(store: store, client: client)
        aboutYou = section
        for (i, g) in [section.group, section.proposalsGroup].enumerated() {
            contentStack.insertArrangedSubview(g, at: i)
            prefsFillWidth(g, in: contentStack)
        }
        if view.window != nil {
            section.appeared(in: view.window)
        }
    }

    /// What was typed in About You is written when the page goes.
    override func viewWillDisappear() {
        super.viewWillDisappear()
        aboutYou?.disappearing()
    }

    /// The bindings end with the window.
    override func viewWillAppear() {
        super.viewWillAppear()
        bindings.watch(view.window)
    }

    /// Whenever the page comes up the Claude apps' handlers are looked up
    /// again and the accounts listed again.
    override func viewDidAppear() {
        super.viewDidAppear()
        aboutYou?.appeared(in: view.window)
        // Claude Code may have been installed or signed in meanwhile; the
        // bridge's status is the application's to ask.
        assistant?.locator?.refresh()
        assistant?.refresh()
        assistant?.refreshAIAccounts()
        updateAssistantGroup()
        // The tokens of the last 24 hours age out without a notification.
        triage?.relistBoard()
        // Accounts may have been added or turned off meanwhile.
        reloadTriageAccounts()
    }

    // MARK: Binding

    private func bindIfReady() {
        guard !bound, isViewLoaded, !closed, configured, let settings else { return }
        bound = true
        addBoardGroup()
        bindAssistant(settings)
        bindBoard()
        bindings.onClose = { [weak self] in
            guard let self else { return }
            self.triageToken?.cancel()
            self.triageToken = nil
            self.assistantToken?.cancel()
            self.assistantToken = nil
            self.targetToken?.cancel()
            self.targetToken = nil
            self.providerToken?.cancel()
            self.providerToken = nil
            self.modelBinding?.cancel()
            self.boardModelBinding?.cancel()
        }
    }

    // MARK: The Assistant group (ui/internal/assistant)

    /// Binds the choices to their keys; the switch is set by hand, because
    /// what it shows depends on the bridge too (`updateAssistantGroup`).
    /// The group follows the preferences and what the assistant knows
    /// (whose changes include `assistant-menu`'s).
    private func bindAssistant(_ settings: Settings) {
        assistantMenuSwitch.target = self
        assistantSetUpButton.bezelStyle = .rounded
        assistantSetUpButton.target = self
        assistantSetUpButton.action = #selector(openIntegrations(_:))
        assistantSetUpButton.isHidden = true
        assistantMenuSwitch.action = #selector(assistantMenuChanged(_:))
        assistantSource.target = self
        assistantSource.action = #selector(assistantSourceChosen(_:))
        bindings.add(.bind(assistantTarget, to: settings, .assistantTarget, choices: AssistantController.targets, \.assistantTarget))
        modelBinding = ProviderModelBinding(
            popup: assistantModel, row: modelRow, purpose: .panel, settings: settings, assistant: assistant, client: client,
            toast: { [weak self] in self?.toast?($0) })
        assistantToken = assistant?.onChange { [weak self] in
            self?.updateAssistantGroup()
        }
        targetToken = settings.onChange(.assistantTarget) { [weak self] in self?.updateAssistantGroup() }
        providerToken = settings.onChange(.assistantProvider) { [weak self] in self?.updateAssistantGroup() }
        updateAssistantGroup()
    }

    /// Whether the bridge is registered in at least one client, as the
    /// application last learned.
    private var bridgeRegistered: Bool {
        assistant?.registered ?? false
    }

    /// Whether any status is known.
    private var bridgeKnown: Bool {
        assistant?.status != nil
    }

    /// The provider in effect (`AssistantController.source`).
    private var source: Assistant.ProviderSource {
        assistant?.source ?? Assistant.sourceOf(settings?.assistantProvider ?? "")
    }

    /// The group for the provider and the bridge's state
    /// (`Assistant.shown`, GTK `assistantGroupFor`): over Claude Code,
    /// registered, the switch shows the preference and "Open In" why the
    /// chosen app cannot run the message actions; known not to be
    /// registered, both rows are insensitive and the switch is off and
    /// says to register under Integrations; not known yet, both rows are
    /// insensitive and the switch shows the preference without a reason.
    /// Over ChatGPT or an account with the In App target the preference
    /// alone counts.
    private func updateAssistantGroup() {
        guard let settings, !closed else { return }
        fillSource(settings)
        let source = source
        let local = source == .claude
        let app = settings.assistantTarget == .app
        assistantGroup.descriptionText = source == .chatgpt
            ? L10n.T("Connect ChatGPT to use your plan with the in-app assistant.")
            : Assistant.texts().description
        let registered = bridgeRegistered
        let unknown = !registered && !bridgeKnown
        var sensitive = registered
        var on = unknown ? settings.assistantMenu : Assistant.shown(menu: settings.assistantMenu, registered: registered)
        var registerFirst = !registered && !unknown
        if !local, app {
            sensitive = true
            on = settings.assistantMenu
            registerFirst = false
        }
        menuRow?.isEnabled = sensitive
        targetRow?.isEnabled = sensitive || !local
        assistantMenuSwitch.state = on ? .on : .off
        menuRow?.subtitle = registerFirst ? Assistant.apiText().registerInIntegrations : ""
        targetRow?.subtitle = registered || !local ? (assistant?.problem(settings.assistantTarget) ?? "") : ""
        if let modelRow {
            // An AI provider account chooses its models on its own card in
            // Integrations.
            assistantGroup.setRow(modelRow, hidden: !app || source == .account)
            modelRow.isEnabled = sensitive || !local
        }
        // Nothing to run on: the whole group is grey, Assistant Uses says
        // where an assistant comes from and the header offers the way.
        let nothing = Assistant.nothingSetUp(choices, registered: registered, known: bridgeKnown)
        if nothing {
            sourceRow?.subtitle = Assistant.apiText().usesSubtitle
            menuRow?.isEnabled = false
            targetRow?.isEnabled = false
            modelRow?.isEnabled = false
            menuRow?.subtitle = ""
            targetRow?.subtitle = ""
        }
        sourceRow?.isEnabled = !nothing
        assistantSetUpButton.isHidden = !nothing
        modelBinding?.scheduleUpdate()
        boardModelBinding?.scheduleUpdate()
        updateBoardGroup()
    }

    /// Assistant Uses (GTK `bindAssistantSource`): Claude Code, ChatGPT
    /// and every AI provider account, an entry that cannot run disabled
    /// with why as its tooltip; a chosen account the daemon no longer has
    /// is listed as such; the row's subtitle is the chosen entry's reason,
    /// else the row's own line.
    private func fillSource(_ settings: Settings) {
        let x = Assistant.apiText()
        let current = settings.assistantProvider
        var choices = assistant?.providerChoices()
            ?? Assistant.providerChoices(claude: .init(), chatgpt: .init(), accounts: [])
        if !choices.contains(where: { $0.id == current }) {
            choices.append(Assistant.ProviderChoice(id: current, name: x.accountMissing))
        }
        self.choices = choices
        syncingSource = true
        defer { syncingSource = false }
        assistantSource.removeAllItems()
        var subtitle = x.usesSubtitle
        var selected = 0
        for (i, c) in choices.enumerated() {
            assistantSource.addItem(withTitle: c.name)
            let item = assistantSource.lastItem
            item?.isEnabled = c.available || c.id == current
            item?.toolTip = c.problem.isEmpty ? nil : c.problem
            if c.id == current {
                selected = i
                if !c.available, !c.problem.isEmpty {
                    subtitle = c.problem
                }
            }
        }
        assistantSource.selectItem(at: selected)
        sourceRow?.subtitle = subtitle
    }

    /// Set Up in Integrations…: the Integrations tab of this window.
    @objc private func openIntegrations(_ sender: Any?) {
        (view.window?.windowController as? PreferencesWindowController)?.select(.integrations)
    }

    @objc private func assistantSourceChosen(_ sender: Any?) {
        guard !syncingSource, let settings else { return }
        let i = assistantSource.indexOfSelectedItem
        guard choices.indices.contains(i) else { return }
        let c = choices[i]
        if c.id != settings.assistantProvider {
            settings.assistantProvider = c.id
        }
    }

    /// The user flipped "Show the Assistant Menu"; the row is insensitive
    /// while the bridge is not registered, so this only writes the
    /// preference while it is (or another provider needs none).
    @objc private func assistantMenuChanged(_ sender: Any?) {
        guard let settings, bridgeRegistered || (source != .claude && settings.assistantTarget == .app) else {
            updateAssistantGroup()
            return
        }
        settings.assistantMenu = assistantMenuSwitch.state == .on
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
        // The Assistant group's Model row, for the triage's own model
        // (`ProviderModelBinding`, the board's purpose).
        let model = PreferenceRowView(title: Assistant.panelTexts().model, trailing: boardModel)
        boardModelRow = model
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
        let modelRows: [NSView] = [model]
        boardAccountsStack.orientation = .vertical
        boardAccountsStack.alignment = .leading
        boardAccountsStack.spacing = 4
        let accounts = PreferenceRowView(
            title: Board.Text.triageSettingsAccounts, subtitle: "", trailing: boardAccountsStack)
        boardAccountsRow = accounts
        boardGroup.setRows([consent] + modelRows + [accounts, auto, interval, daily, status, usage])
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
        // A setting like the panel's model, usable whenever the group is
        // shown (choosing it needs no consent); the next run takes it. Over
        // an AI provider account it is the account's `boardModel`.
        if let settings {
            boardModelBinding = ProviderModelBinding(
                popup: boardModel, row: boardModelRow, purpose: .board, settings: settings, assistant: assistant,
                client: client, toast: { [weak self] in self?.toast?($0) })
        }
        triageToken = triage.observe { [weak self] in
            self?.updateBoardGroup()
        }
        // The daemon's preferences, should none have come yet.
        if triage.preferences.preferences == nil {
            triage.preferences.load()
        }
        reloadTriageAccounts()
        updateBoardGroup()
    }

    // MARK: Triage These Accounts (preferences_board.go `bindTriageAccounts`)

    /// Asks account.list; the checkboxes follow the answer.
    fileprivate func reloadTriageAccounts() {
        guard let client, triage != nil, !closed else { return }
        boardAccountsGeneration += 1
        let my = boardAccountsGeneration
        Task { @MainActor [weak self] in
            let result = try? await client.call(API.AccountList.self, EmptyParams())
            guard let self, !self.closed, my == self.boardAccountsGeneration, let result else { return }
            self.rebuildTriageAccounts(result.accounts)
        }
    }

    /// A checkbox per account the triage could read
    /// (`BoardPreferencesController.triageCandidates`: enabled, not
    /// calendar-only), titled as Settings → Accounts names it (plain text).
    private func rebuildTriageAccounts(_ list: [Account]) {
        boardAccounts = list
        for (_, box) in boardAccountBoxes {
            boardAccountsStack.removeArrangedSubview(box)
            box.removeFromSuperview()
        }
        boardAccountBoxes = BoardPreferencesController.triageCandidates(list).map { a in
            let box = NSButton(checkboxWithTitle: accountLabel(a), target: self, action: #selector(triageAccountToggled(_:)))
            box.lineBreakMode = .byTruncatingTail
            boardAccountsStack.addArrangedSubview(box)
            return (a.id, box)
        }
        updateTriageAccounts()
    }

    /// The checkboxes as the daemon decides; the subtitle says that none
    /// listed is every enabled mail account, and that a list naming only
    /// accounts that are gone triages nothing
    /// (`BoardPreferencesController.triageAccountsSubtitle`).
    private func updateTriageAccounts() {
        guard let row = boardAccountsRow else { return }
        let prefs = triage?.preferences.preferences
        row.isEnabled = prefs != nil && !boardAccountBoxes.isEmpty
        let listed = prefs?.triageAccounts ?? []
        if prefs == nil {
            row.subtitle = ""
        } else {
            switch BoardPreferencesController.triageAccountsSubtitle(listed, boardAccounts) {
            case .all: row.subtitle = Board.Text.triageSettingsAccountsAll
            case .none: row.subtitle = Board.Text.triageSettingsAccountsNone
            case .some: row.subtitle = ""
            }
        }
        for (id, box) in boardAccountBoxes {
            guard let a = boardAccounts.first(where: { $0.id == id }) else { continue }
            box.state = BoardPreferencesController.triageAccountChecked(listed, a) ? .on : .off
        }
    }

    /// A checkbox changed: the list without or with that account, written
    /// to the daemon; the last checked account stays checked.
    @objc private func triageAccountToggled(_ sender: NSButton) {
        guard !closed, let prefs = triage?.preferences, let current = prefs.preferences,
              let id = boardAccountBoxes.first(where: { $0.box === sender })?.id
        else {
            updateTriageAccounts()
            return
        }
        guard let next = BoardPreferencesController.toggleTriageAccount(
            current.triageAccounts, accounts: boardAccounts, id: id, on: sender.state == .on)
        else {
            updateTriageAccounts()
            return
        }
        prefs.setTriageAccounts(next)
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
        // An AI provider account chooses the board's model on its card in
        // Integrations, as the Assistant group's Model row.
        if let boardModelRow {
            boardGroup.setRow(boardModelRow, hidden: source == .account)
        }
        // Why triage cannot run now ("" when it can); the Claude Code row
        // above offers what Claude Code needs.
        boardGroup.descriptionText = Board.triageSettingsDescription(v)
        // Where the mail goes: Claude Code's words, ChatGPT's, or the
        // chosen account's destination (`apiBoardConsentBody`).
        switch assistant?.source ?? .claude {
        case .chatgpt:
            boardConsentRow?.subtitle = ChatGPTText.boardBody
        case .account:
            if let acc = assistant?.apiAccount {
                boardConsentRow?.subtitle = AssistantController.apiBoardConsentBody(acc)
            } else {
                boardConsentRow?.subtitle = Board.Text.triageSettingsConsentSubtitle
            }
        case .claude:
            boardConsentRow?.subtitle = Board.Text.triageSettingsConsentSubtitle
        }
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
        updateTriageAccounts()
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
