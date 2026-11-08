// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The Integrations page of the settings (preferences.blp
/// `integrations_page`, ui/internal/window/integrations_page.go,
/// docs/plans/ai-api.md §7.2): the page's description, the accounts that
/// are not mail accounts as Connected Services (the Accounts page's rows,
/// `AccountsPaneViewController` with page `.integrations`: a Jira row
/// edits its settings, a CalDAV row shows the Calendar page, an eWay-CRM
/// row opens its assistant in the edit mode, an AI provider account's row
/// its dialog (`AIAccountSheetController`) with the "API key" capsule and
/// the tokens the daemon's assistant used through it today), the three
/// local AI integrations after them (Claude Desktop, Claude Code and
/// ChatGPT, state of this client rather than accounts of the daemon:
/// `Integrations.localRowFor` over the application's `AssistantController`,
/// the two Claude apps each by the bridge's registration in it, each shown
/// only while it is connected and then gone from the catalogue; Settings
/// opens its sheet, Remove unregisters the bridge from that app only or
/// disconnects ChatGPT after the row's confirmation), and the catalogue of what can be
/// added in one group (Available Services, `Integrations.catalog`), the
/// services and then the AI told apart by their capsules, each with Add…
/// opening its assistant, dialog or, in the App Store build, the dialog
/// that says where the rest of the assistant is (`WebsiteFeaturesDialog`).
@MainActor
final class IntegrationsPaneViewController: AccountsPaneViewController {
    /// The calendar source the Add CalDAV sheet works over (the Calendar
    /// page's); nil without one, and Add… then only says so.
    var calendarSource: (() -> (any CalendarDataSource)?)?
    /// The rows of the catalogue by kind (`MALACHI_START` prefs-integrations);
    /// a local integration's entry carries its `LocalKind` name as the kind.
    private(set) var serviceRows: [(kind: AccountKind, view: NSView)] = []
    /// The App Store build's dialog for Add… of a local integration
    /// (`WebsiteFeaturesDialog.show`); the application gives one that
    /// knows its state, the default only opens the website.
    var websiteFeatures: (@MainActor (NSWindow?) -> Void)?

    /// What the local rows and their dialogs work over (the application's
    /// state; `configure`).
    private var context: ClaudeIntegrationSheetController.Context?
    /// The bridge's status for the Claude rows and their Unregister (each
    /// for its own app, `change(registered:in:)`); shares every status with
    /// the application's assistant.
    private var registration: MCPRegistrationController?
    private var assistantToken: AssistantController.Token?
    private var signInToken: ClaudeCodeLocator.SignInToken?
    private var codexPathToken: Settings.ChangeToken?
    /// Bumped by every look at Claude Code: a late answer is dropped.
    private var localGen = 0
    private let localGroup = PreferencesGroupView()
    private var localRows: [Integrations.LocalKind: LocalIntegrationRowView] = [:]
    /// What each local row shows now (its Remove's confirmation and client).
    private var localSpecs: [Integrations.LocalKind: Integrations.LocalRow] = [:]
    private var localOffers: [Integrations.LocalKind: NSView] = [:]
    private let availableGroup = PreferencesGroupView(title: Integrations.texts().availableTitle)
    /// The tokens the daemon's assistant used today through each AI
    /// provider account, as assistant.usage last answered.
    private var usage: [AccountID: Int64] = [:]
    private var usageToken: NotificationHub.Token?
    private var usageGen: [AccountID: Int] = [:]

    init(
        client: RPCClient, confirmRemoval: @escaping PrefsConfirmRemoval, confirmTrust: @escaping WizardConfirmTrust,
        toast: @escaping @MainActor (String) -> Void
    ) {
        super.init(page: .integrations, client: client, confirmRemoval: confirmRemoval, confirmTrust: confirmTrust, toast: toast)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Supplies what the local rows and the Claude and ChatGPT dialogs
    /// need: the bridge path (`Paths.mcpBridge`), the settings, the
    /// application's assistant, its Claude Desktop controller and the
    /// restart question; may be called before or after the view loaded.
    func configure(
        bridge: String?, settings: Settings, assistant: AssistantController?, claudeDesktop: ClaudeDesktopController?,
        confirmRestart: PrefsConfirmRestart?
    ) {
        guard context == nil else { return }
        context = ClaudeIntegrationSheetController.Context(
            bridge: bridge, settings: settings, assistant: assistant, claudeDesktop: claudeDesktop, confirmRestart: confirmRestart)
        bindLocalIfReady()
    }

    /// The application's notifications: the usage line of an AI provider
    /// account's row follows notify.assistantUsageChanged.
    func configure(notifications hub: NotificationHub) {
        guard usageToken == nil else { return }
        usageToken = hub.addAssistantUsageChanged { [weak self] n in
            self?.refreshUsage(n.accountId)
        }
    }

    /// The page's description under the toolbar title, muted, as an
    /// `AdwPreferencesPage` shows its description.
    override func addLeadingViews() {
        let label = PrefsWrappingLabel(Integrations.texts().description, size: 13, color: .secondaryLabelColor)
        prefsAddFilling(label, to: contentStack)
    }

    /// The local rows (no title of their own, under the accounts) and
    /// Available Services: a row per catalogue entry with its capsule and
    /// Add… button.
    override func addTrailingGroups() {
        let tx = Integrations.texts()
        var local: [NSView] = []
        for kind in Integrations.localKinds {
            guard let spec = Integrations.localRowFor(kind, claude: .init(), chatgpt: .init())?.row else { continue }
            let row = LocalIntegrationRowView(settingsToolTip: tx.settings)
            row.apply(spec)
            row.onSettings = { [weak self] in self?.openLocal(kind) }
            row.onRemove = { [weak self] in self?.removeLocal(kind) }
            row.isHidden = true
            localRows[kind] = row
            local.append(row)
        }
        localGroup.setRows(local)
        localGroup.isHidden = true
        addGroup(localGroup)

        var rows: [NSView] = []
        for s in Integrations.catalog() {
            let button = NSButton(title: tx.add, target: self, action: #selector(addService(_:)))
            button.bezelStyle = .rounded
            let kind = s.kind ?? AccountKind(rawValue: Self.localName(s.local))
            button.identifier = NSUserInterfaceItemIdentifier(s.key)
            let icon = NSImageView(image: wizardSymbol(AccountRowCell.symbol(for: s.icon), pointSize: 15))
            icon.contentTintColor = .labelColor
            let row = PreferenceRowView(
                title: s.name, subtitle: s.description, prefix: icon, trailing: button,
                titleAccessory: s.badge.isEmpty ? nil : prefsSourcePill(s.badge))
            rows.append(row)
            serviceRows.append((kind, row))
            if s.local != .none {
                localOffers[s.local] = row
            }
        }
        availableGroup.setRows(rows)
        addGroup(availableGroup)
        bindLocalIfReady()
    }

    /// The Connected Services placeholder shows only while neither an
    /// account nor a local integration has a row.
    override var showsEmptyRow: Bool {
        accounts.isEmpty && !localRows.values.contains { !$0.isHidden }
    }

    /// The rows of the catalogue and the local integrations are looked at
    /// again whenever the page comes up.
    override func viewDidAppear() {
        super.viewDidAppear()
        context?.assistant?.locator?.refresh()
        registration?.load()
        refreshLocal()
        for a in accounts where a.config.protocolKind == .ai {
            refreshUsage(a.id)
        }
    }

    override func didSetAccounts() {
        super.didSetAccounts()
        let ids = Set(accounts.map(\.id))
        usage = usage.filter { ids.contains($0.key) }
        for a in accounts where a.config.protocolKind == .ai && usage[a.id] == nil {
            refreshUsage(a.id)
        }
    }

    /// The accounts list disposed of a connected service: a token of the
    /// notifications too.
    override func closing() {
        super.closing()
        registration?.close()
        assistantToken?.cancel()
        assistantToken = nil
        signInToken?.cancel()
        signInToken = nil
        codexPathToken?.cancel()
        codexPathToken = nil
        usageToken?.cancel()
        usageToken = nil
    }

    // MARK: The AI provider accounts (ai_editors.go)

    /// The usage line of an AI provider account's row: "%s tokens today"
    /// with grouped digits, once assistant.usage answered.
    override func rowUsage(_ id: AccountID) -> String? {
        guard let tokens = usage[id] else { return nil }
        let f = NumberFormatter()
        f.numberStyle = .decimal
        f.usesGroupingSeparator = true
        let grouped = f.string(from: NSNumber(value: tokens)) ?? String(tokens)
        return L10n.format(Assistant.apiText().usageToday, [grouped])
    }

    /// Asks assistant.usage for the tokens account `id` used today and
    /// shows them on its row. An account without a row, or a failed call,
    /// changes nothing.
    private func refreshUsage(_ id: AccountID) {
        guard !closed, accounts.contains(where: { $0.id == id && $0.config.protocolKind == .ai }) else { return }
        let gen = (usageGen[id] ?? 0) + 1
        usageGen[id] = gen
        let client = client
        Task { [weak self] in
            let res = try? await client.call(API.AssistantUsage.self, AssistantUsageParams(accountId: id, days: 0), timeout: RPCTimeouts.default)
            guard let self, !self.closed, self.usageGen[id] == gen, let res else { return }
            var tokens: Int64 = 0
            for day in res.days {
                let u = day.usage
                tokens += u.inputTokens + u.outputTokens + u.cacheReadInputTokens + u.cacheCreationInputTokens
            }
            self.usage[id] = tokens
            self.reloadRow(id)
        }
    }

    /// The dialog of AI provider account `a` in its edit mode, with the
    /// key field open for `replaceKey` (the application's
    /// `hooks.editAIAccount`); the page reloads once it saved.
    func editAIAccount(_ a: Account, replaceKey: Bool, over window: NSWindow? = nil) {
        guard let window = window ?? view.window, window.attachedSheet == nil else { return }
        AIAccountSheetController.present(
            from: window, client: client, provider: .openaiCompatible, editing: a, replaceKey: replaceKey,
            confirmTrust: confirmTrust
        ) { [weak self] _, cfg in
            guard let self, !self.closed else { return }
            self.loadAccounts()
            self.onAccountsChanged?()
            // TRANSLATORS: %s is the edited account's e-mail address.
            self.toast(L10n.T("Saved %s", cfg.name))
        }
    }

    /// The dialog that adds an account of `provider` (the catalogue's
    /// Add…, the application's `hooks.addAIAccount`).
    func addAIAccount(_ provider: AIProvider, over window: NSWindow? = nil) {
        guard let window = window ?? view.window, window.attachedSheet == nil else { return }
        AIAccountSheetController.present(from: window, client: client, provider: provider, confirmTrust: confirmTrust) { [weak self] id, cfg in
            // The account has no address; the toast names it.
            var named = cfg
            named.email = cfg.name
            self?.added(id, named)
        }
    }

    // MARK: Available Services

    @objc private func addService(_ sender: NSButton) {
        let raw = sender.identifier?.rawValue ?? ""
        guard let s = Integrations.catalog().first(where: { $0.key == raw }) else { return }
        openService(s)
    }

    /// Opens the assistant of a catalogue service, or the sheet of a
    /// local integration (in the App Store build the website dialog for
    /// each of them: it cannot run Claude Code or Codex).
    private func openService(_ s: Integrations.Service) {
        switch (s.local, s.kind) {
        case (.claudeDesktop, _), (.claudeCode, _), (.chatgpt, _):
            if let settings = context?.settings, !settings.distribution.allowsLocalAgents {
                if let websiteFeatures {
                    websiteFeatures(view.window)
                } else {
                    WebsiteFeaturesDialog.show(for: view.window)
                }
                return
            }
            openLocal(s.local)
        case (_, .jira?): addJira()
        case (_, .caldav?): addCalDAV()
        case (_, .eway?): addEway()
        case (_, .ai?): addAIAccount(s.provider ?? .openaiCompatible)
        default: break
        }
    }

    /// The catalogue's name of a local integration (its kind slot).
    private static func localName(_ kind: Integrations.LocalKind) -> String {
        switch kind {
        case .claudeDesktop, .claudeCode: return Integrations.clientID(kind) ?? ""
        case .chatgpt: return "chatgpt"
        case .none: return ""
        }
    }

    /// The Jira account assistant (the File menu's Add Jira Account… too).
    private func addJira() {
        guard let window = view.window, window.attachedSheet == nil else { return }
        JiraWizardWindowController.present(from: window, client: client) { [weak self] id, cfg in
            self?.added(id, cfg)
        }
    }

    /// The eWay-CRM account assistant (docs/eway.md §2.4): only here; the
    /// other entry points are for Mail accounts and Jira.
    private func addEway() {
        guard let window = view.window, window.attachedSheet == nil else { return }
        EWayWizardWindowController.present(from: window, client: client) { [weak self] id, cfg in
            // The daemon fills the address from the eWay-CRM user; the
            // toast names the account until the list has it.
            var named = cfg
            if named.email.isEmpty {
                named.email = named.name.isEmpty ? CRM.brand : named.name
            }
            self?.added(id, named)
        }
    }

    /// The Add CalDAV Calendar sheet over the settings, as the Calendar
    /// page's Add CalDAV Calendar… opens it; the pages reload once it
    /// added the account.
    private func addCalDAV() {
        guard let window = view.window, window.attachedSheet == nil else { return }
        guard let source = calendarSource?() else {
            toast(Cal.Text.previewOnly)
            return
        }
        CalDAVSheetController.present(from: window, source: source, confirmTrust: confirmTrust) { [weak self] _ in
            guard let self, !self.closed else { return }
            self.loadAccounts()
            self.onAccountsChanged?()
        }
    }

    // MARK: The local integrations (integrations_page.go `bindLocalRows`)

    /// Follows the application's assistant for the local rows once the
    /// view and the context are there: the bridge's registration in each
    /// Claude app, Claude
    /// Code (its path, version and sign-in, asked in the background), the
    /// ChatGPT connection and Codex.
    private func bindLocalIfReady() {
        guard registration == nil, isViewLoaded, let context, !closed else { return }
        let c = MCPRegistrationController(bridge: context.bridge)
        registration = c
        c.onRegistered = { [weak self, weak c] _ in
            if let status = c?.status {
                self?.context?.assistant?.apply(status)
            }
            self?.refreshLocal()
        }
        c.onToast = { [weak self] text in self?.toast(text) }
        if let known = context.assistant?.status {
            c.adopt(known)
        }
        assistantToken = context.assistant?.onChange { [weak self] in
            self?.followApplication()
            self?.refreshLocal()
        }
        signInToken = context.assistant?.locator?.onSignInChange { [weak self] in self?.refreshLocal() }
        codexPathToken = context.settings.onChange(.assistantCodexPath) { [weak self] in self?.refreshLocal() }
        refreshLocal()
    }

    /// A status the application learned elsewhere is this page's too.
    private func followApplication() {
        guard let known = context?.assistant?.status else { return }
        registration?.adopt(known)
    }

    /// What the application knows of the Claude apps and ChatGPT now,
    /// without what Claude Code itself is asked for (the version, the
    /// sign-in).
    private func localStates() -> (Integrations.ClaudeState, Integrations.ChatGPTState) {
        let assistant = context?.assistant
        let path = assistant?.locator?.locate() ?? ""
        let status = assistant?.status ?? registration?.status
        let claude = Integrations.ClaudeState(
            desktopRegistered: status?.registered(in: MCPStatus.clientDesktop) ?? false,
            codeRegistered: status?.registered(in: MCPStatus.clientCode) ?? false,
            present: !path.isEmpty, path: path)
        var chatgpt = Integrations.ChatGPTState()
        if let codex = assistant?.chatGPT {
            chatgpt.connected = codex.connection.connected
            chatgpt.account = codex.connection.email
            chatgpt.codexFound = codex.available
        }
        return (claude, chatgpt)
    }

    /// The local rows from the states; the version and the sign-in of
    /// Claude Code come from Claude Code itself, later.
    private func refreshLocal() {
        guard isViewLoaded, !closed else { return }
        localGen += 1
        let gen = localGen
        var (claude, chatgpt) = localStates()
        applyLocal(claude, chatgpt)
        // Only the Claude Code row shows them.
        guard claude.present, claude.codeRegistered, let locator = context?.assistant?.locator else { return }
        Task { @MainActor [weak self] in
            let version = await locator.version()
            let signedIn = await locator.signedIn()
            guard let self, !self.closed, gen == self.localGen else { return }
            claude.version = version ?? ""
            claude.signInKnown = signedIn != nil
            claude.signedIn = signedIn == true
            self.applyLocal(claude, chatgpt)
        }
    }

    /// Shows the rows of the states: a connected integration's row, its
    /// catalogue entry otherwise; the rows' group only with a row in it,
    /// and the Connected Services placeholder only without any row.
    private func applyLocal(_ claude: Integrations.ClaudeState, _ chatgpt: Integrations.ChatGPTState) {
        var any = false
        for kind in Integrations.localKinds {
            guard let (found, shown) = Integrations.localRowFor(kind, claude: claude, chatgpt: chatgpt), let row = localRows[kind] else { continue }
            var spec = found
            if spec.clientID != nil, registration?.readOnly ?? TestBuild.isActive {
                // A test build leaves the registration to the stable app.
                spec.canRemove = false
                spec.subtitle = spec.subtitle.isEmpty ? TestBuild.registrationNote : spec.subtitle + " · " + TestBuild.registrationNote
            }
            row.apply(spec)
            localSpecs[kind] = spec
            localGroup.setRow(row, hidden: !shown)
            if let offer = localOffers[kind] {
                availableGroup.setRow(offer, hidden: shown)
            }
            any = any || shown
        }
        localGroup.isHidden = !any
        refreshEmptyRow()
    }

    /// A local row's Settings, and the catalogue's Add… of a local
    /// integration: its sheet (`MALACHI_START` claude-desktop-integration,
    /// claude-code-integration).
    func openLocal(_ kind: Integrations.LocalKind) {
        guard let window = view.window, window.attachedSheet == nil, let context else { return }
        switch kind {
        case .claudeDesktop, .claudeCode:
            ClaudeIntegrationSheetController.present(from: window, kind: kind, context: context)
        case .chatgpt:
            guard let assistant = context.assistant else { return }
            ChatGPTIntegrationSheetController.present(from: window, settings: context.settings, assistant: assistant)
        case .none:
            break
        }
    }

    /// A local row's Remove: after the row's confirmation, the bridge
    /// unregistered from that Claude app only (the same path as its sheet's
    /// switch: Claude Desktop's with the restart question, Claude Code's at
    /// once), or ChatGPT disconnected (its sign-in cancelled first).
    private func removeLocal(_ kind: Integrations.LocalKind) {
        guard let context, let row = localRows[kind], let spec = localSpecs[kind], let window = view.window else { return }
        switch kind {
        case .claudeDesktop, .claudeCode:
            guard let registration, let client = spec.clientID else { return }
            Task { @MainActor [weak self] in
                guard await Self.confirm(on: window, heading: spec.removeHeading, body: spec.removeBody, confirmLabel: row.removeLabel),
                      let self, !self.closed else { return }
                row.setRemoving(true)
                defer { row.setRemoving(false) }
                guard kind == .claudeDesktop, let desktop = context.claudeDesktop else {
                    _ = await registration.change(registered: false, in: client)
                    return
                }
                let write: ClaudeDesktopController.Write = { want in
                    if !registration.closed {
                        return await registration.change(registered: want, in: client)
                    }
                    return await desktop.writeOwn(registered: want)
                }
                await desktop.change(registered: false, status: registration.status, ask: {
                    guard let confirmRestart = context.confirmRestart else { return .later }
                    return await confirmRestart(window) ? .restart : .later
                }, write: write)
            }
        case .chatgpt:
            guard let connection = context.assistant?.chatGPT?.connection else { return }
            Task { @MainActor [weak self] in
                guard await Self.confirm(on: window, heading: spec.removeHeading, body: spec.removeBody, confirmLabel: row.removeLabel),
                      let self, !self.closed else { return }
                connection.cancel()
                row.setRemoving(true)
                var confirmed = false
                do {
                    confirmed = try await connection.disconnect()
                } catch {
                    confirmed = false
                }
                guard !self.closed else { return }
                row.setRemoving(false)
                if !confirmed {
                    self.toast(L10n.T("Disconnected locally; remote sign-out could not be confirmed."))
                }
                self.refreshLocal()
            }
        case .none:
            break
        }
    }

    /// A destructive confirmation as a sheet on `window`: the heading, the
    /// body, Cancel and the confirming button (red), Escape cancelling.
    private static func confirm(on window: NSWindow, heading: String, body: String, confirmLabel: String) async -> Bool {
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = heading
        alert.informativeText = body
        let cancel = alert.addButton(withTitle: wizardLabel("_Cancel"))
        cancel.keyEquivalent = "\u{1b}"
        let confirm = alert.addButton(withTitle: confirmLabel)
        confirm.hasDestructiveAction = true
        return await alert.beginSheetModal(for: window) == .alertSecondButtonReturn
    }
}

/// The row of a local integration on the Integrations page
/// (integrations_page.go `localRow`): the icon, the title with the "This
/// computer" capsule, the subtitle, Settings and Remove with its label
/// (Unregister, Disconnect). Every text is a translated label of this
/// client, or a path and version of Claude Code (plain).
@MainActor
final class LocalIntegrationRowView: NSView, PrefsGroupMember {
    private let icon = NSImageView()
    private let titleLabel = NSTextField(labelWithString: "")
    private let badge = prefsSourcePill("")
    private let subtitleLabel = PrefsWrappingLabel("", size: 11, color: .secondaryLabelColor)
    private let settingsButton: NSButton
    private let removeButton = NSButton(title: "", target: nil, action: nil)
    private var groupEnabled = true
    private var removing = false

    var onSettings: (() -> Void)?
    var onRemove: (() -> Void)?

    /// The Remove button's label as shown (the confirmation's button).
    var removeLabel: String { removeButton.title }

    init(settingsToolTip: String) {
        settingsButton = NSButton(image: wizardSymbol("gearshape", pointSize: 14), target: nil, action: nil)
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        icon.image = wizardSymbol(AccountRowCell.symbol(for: Integrations.aiIcon), pointSize: 15)
        icon.contentTintColor = .labelColor
        icon.imageScaling = .scaleNone
        titleLabel.font = .systemFont(ofSize: 13)
        titleLabel.lineBreakMode = .byTruncatingTail
        titleLabel.setContentHuggingPriority(.defaultLow, for: .horizontal)
        titleLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        settingsButton.isBordered = false
        settingsButton.imagePosition = .imageOnly
        settingsButton.contentTintColor = .labelColor
        settingsButton.toolTip = settingsToolTip
        settingsButton.target = self
        settingsButton.action = #selector(settingsClicked(_:))
        settingsButton.widthAnchor.constraint(equalToConstant: 28).isActive = true
        settingsButton.heightAnchor.constraint(equalToConstant: 28).isActive = true
        removeButton.bezelStyle = .rounded
        removeButton.target = self
        removeButton.action = #selector(removeClicked(_:))
        let titleRow = NSStackView(views: [titleLabel, badge])
        titleRow.orientation = .horizontal
        titleRow.alignment = .centerY
        titleRow.spacing = 6
        titleRow.detachesHiddenViews = true
        let text = prefsColumn(spacing: 2)
        prefsAddFilling(titleRow, to: text)
        prefsAddFilling(subtitleLabel, to: text)
        let cluster = NSStackView(views: [settingsButton, removeButton])
        cluster.orientation = .horizontal
        cluster.alignment = .centerY
        cluster.spacing = 8
        cluster.setHuggingPriority(.required, for: .horizontal)
        cluster.translatesAutoresizingMaskIntoConstraints = false
        for v in [icon, cluster] as [NSView] {
            v.translatesAutoresizingMaskIntoConstraints = false
            v.setContentHuggingPriority(.required, for: .horizontal)
            v.setContentCompressionResistancePriority(.required, for: .horizontal)
        }
        addSubview(icon)
        addSubview(text)
        addSubview(cluster)
        let inset = PreferenceRowView.inset
        let vInset = PreferenceRowView.verticalInset
        NSLayoutConstraint.activate([
            icon.leadingAnchor.constraint(equalTo: leadingAnchor, constant: inset),
            icon.centerYAnchor.constraint(equalTo: centerYAnchor),
            text.leadingAnchor.constraint(equalTo: icon.trailingAnchor, constant: inset),
            text.centerYAnchor.constraint(equalTo: centerYAnchor),
            text.topAnchor.constraint(greaterThanOrEqualTo: topAnchor, constant: vInset),
            text.bottomAnchor.constraint(lessThanOrEqualTo: bottomAnchor, constant: -vInset),
            text.trailingAnchor.constraint(equalTo: cluster.leadingAnchor, constant: -inset),
            cluster.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -inset),
            cluster.centerYAnchor.constraint(equalTo: centerYAnchor),
            heightAnchor.constraint(greaterThanOrEqualToConstant: PreferenceRowView.minHeightWithSubtitle),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Shows `spec` on the row.
    func apply(_ spec: Integrations.LocalRow) {
        titleLabel.stringValue = spec.title
        badge.stringValue = spec.badge
        badge.isHidden = spec.badge.isEmpty
        subtitleLabel.stringValue = spec.subtitle
        subtitleLabel.isHidden = spec.subtitle.isEmpty
        removeButton.title = spec.removeLabel
        removeButton.isHidden = !spec.canRemove
        toolTip = spec.subtitle
    }

    /// Remove is under way: its button waits.
    func setRemoving(_ on: Bool) {
        removing = on
        applyEnabled()
    }

    func setGroupEnabled(_ enabled: Bool) {
        groupEnabled = enabled
        applyEnabled()
    }

    private func applyEnabled() {
        settingsButton.isEnabled = groupEnabled
        removeButton.isEnabled = groupEnabled && !removing
    }

    @objc private func settingsClicked(_ sender: Any?) {
        onSettings?()
    }

    @objc private func removeClicked(_ sender: Any?) {
        onRemove?()
    }
}
