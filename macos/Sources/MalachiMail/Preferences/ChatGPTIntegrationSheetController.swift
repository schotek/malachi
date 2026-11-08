// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The settings of ChatGPT (Codex) (Settings → Integrations → ChatGPT,
/// its Settings and the catalogue's Add…; GTK chatgpt_integration.blp,
/// ui/internal/window/chatgpt_integration.go), as a sheet on the settings
/// window: where Codex is (Choose…, Get Codex…), the ChatGPT connection
/// (Continue with ChatGPT, Disconnect), the panel's model and Manage
/// usage. They were the ChatGPT group of Settings → AI until the AI
/// providers through their API arrived (docs/plans/ai-api.md §7.2); their
/// logic moved here unchanged, but for the In-app provider pop-up, which
/// Assistant Uses on the AI page replaced, and the board's model, which
/// the Board group's Model row shows for the provider in effect.
/// Credentials and the provider's lifecycle stay in Core
/// (`CodexProvider`, `ChatGPTConnection`).
@MainActor
final class ChatGPTIntegrationSheetController: NSWindowController {
    private static var active: [ObjectIdentifier: ChatGPTIntegrationSheetController] = [:]

    private let root: ChatGPTIntegrationViewController

    /// Opens the sheet on `parent`.
    static func present(from parent: NSWindow, settings: Settings, assistant: AssistantController) {
        guard parent.attachedSheet == nil else { return }
        let c = ChatGPTIntegrationSheetController(settings: settings, assistant: assistant)
        guard let sheet = c.window else { return }
        active[ObjectIdentifier(c)] = c
        parent.beginSheet(sheet) { _ in
            MainActor.assumeIsolated {
                c.root.close()
                active[ObjectIdentifier(c)] = nil
            }
        }
    }

    private init(settings: Settings, assistant: AssistantController) {
        root = ChatGPTIntegrationViewController(settings: settings, assistant: assistant)
        let window = WizardSheetWindow(
            contentRect: NSRect(origin: .zero, size: ChatGPTIntegrationViewController.size),
            styleMask: [.titled, .fullSizeContentView],
            backing: .buffered, defer: false
        )
        window.title = ChatGPTText.name
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

/// The page of the ChatGPT sheet; see the window controller.
@MainActor
final class ChatGPTIntegrationViewController: NSViewController {
    static let size = NSSize(width: 560, height: 400)

    var onClose: (() -> Void)?

    private let settings: Settings
    private weak var assistant: AssistantController?
    private let toasts = WizardToastPresenter()
    private let titleLabel = NSTextField(labelWithString: "")
    private let closeButton = NSButton(title: L10n.T("Close"), target: nil, action: nil)
    private let group = PreferencesGroupView(description: L10n.T("Connect ChatGPT to use your plan with the in-app assistant."))
    private let models = NSPopUpButton(frame: .zero, pullsDown: false)
    private let signIn = NSButton(title: L10n.T("Continue with ChatGPT"), target: nil, action: nil)
    private let disconnect = NSButton(title: L10n.T("Disconnect"), target: nil, action: nil)
    private let choose = NSButton(title: Assistant.panelTexts().choose, target: nil, action: nil)
    private let install = NSButton(title: L10n.T("Get Codex…"), target: nil, action: nil)
    private let usage = NSButton(title: L10n.T("Manage usage"), target: nil, action: nil)
    private var executableRow: PreferenceRowView!
    private var connectionRow: PreferenceRowView!
    private var modelRow: PreferenceRowView!
    private var token: AssistantController.Token?
    private var pathToken: Settings.ChangeToken?
    private var catalog: [CodexModel] = []
    private var catalogTask: Task<Void, Never>?
    private var catalogKey = ""
    private var versionPath = ""
    private var version = ""
    private var generation = 0
    private var updateQueued = false
    private(set) var closed = false

    init(settings: Settings, assistant: AssistantController) {
        self.settings = settings
        self.assistant = assistant
        super.init(nibName: nil, bundle: nil)
        title = ChatGPTText.name
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func loadView() {
        let root = NSView(frame: NSRect(origin: .zero, size: Self.size))
        preferredContentSize = Self.size

        let header = NSView()
        header.translatesAutoresizingMaskIntoConstraints = false
        titleLabel.stringValue = ChatGPTText.name
        titleLabel.font = .systemFont(ofSize: 13, weight: .bold)
        titleLabel.alignment = .center
        titleLabel.lineBreakMode = .byTruncatingTail
        titleLabel.translatesAutoresizingMaskIntoConstraints = false
        header.addSubview(titleLabel)
        let divider = PrefsDivider()
        header.addSubview(divider)

        for b in [signIn, disconnect, choose, install, usage] {
            b.bezelStyle = .rounded
        }
        choose.target = self
        choose.action = #selector(chooseExecutable(_:))
        install.target = self
        install.action = #selector(openInstall(_:))
        signIn.target = self
        signIn.action = #selector(connect(_:))
        disconnect.target = self
        disconnect.action = #selector(disconnectAccount(_:))
        usage.target = self
        usage.action = #selector(openUsage(_:))
        models.target = self
        models.action = #selector(modelChanged(_:))
        executableRow = PreferenceRowView(title: L10n.T("Codex"), subtitle: "", trailing: buttons([choose, install]))
        executableRow.setSubtitleSelectable()
        connectionRow = PreferenceRowView(title: L10n.T("Account"), subtitle: "", trailing: buttons([signIn, disconnect]))
        modelRow = PreferenceRowView(title: Assistant.panelTexts().model, trailing: models)
        let usageRow = PreferenceRowView(title: L10n.T("Manage usage"), trailing: usage)
        group.setRows([executableRow, connectionRow, modelRow, usageRow])

        let content = prefsColumn(spacing: 24, insets: NSEdgeInsets(top: 24, left: 24, bottom: 24, right: 24))
        prefsAddFilling(group, to: content)
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
        token = assistant?.onChange { [weak self] in self?.scheduleUpdate() }
        pathToken = settings.onChange(.assistantCodexPath) { [weak self] in self?.scheduleUpdate() }
        update()
    }

    private func buttons(_ views: [NSView]) -> NSStackView {
        let stack = NSStackView(views: views)
        stack.orientation = .horizontal
        stack.spacing = 6
        stack.detachesHiddenViews = true
        return stack
    }

    func close() {
        guard !closed else { return }
        closed = true
        token?.cancel()
        token = nil
        pathToken?.cancel()
        pathToken = nil
        catalogTask?.cancel()
        catalogTask = nil
    }

    private func toast(_ text: String) {
        guard !closed else { return }
        toasts.show(text)
    }

    @objc private func closeClicked(_ sender: Any?) {
        onClose?()
    }

    override func cancelOperation(_ sender: Any?) {
        onClose?()
    }

    // MARK: The rows (chatgpt_integration.go `bind`)

    private func scheduleUpdate() {
        guard !updateQueued, !closed else { return }
        updateQueued = true
        DispatchQueue.main.async { [weak self] in
            guard let self else { return }
            self.updateQueued = false
            self.update()
        }
    }

    private func update() {
        guard !closed, isViewLoaded else { return }
        guard let codex = assistant?.chatGPT else {
            group.descriptionText = L10n.T("Could not connect to ChatGPT.")
            group.isEnabled = false
            return
        }
        group.isEnabled = true
        let path = codex.executable ?? ""
        if path != versionPath {
            versionPath = path
            version = ""
            if !path.isEmpty {
                Task { @MainActor [weak self] in
                    let version = await codex.version()
                    guard let self, !self.closed, self.versionPath == path else { return }
                    self.version = version ?? ""
                    self.update()
                }
            }
        }
        executableRow.subtitle = path.isEmpty
            ? L10n.T("Codex was not found. Choose a native Codex executable.")
            : [path, version].filter { !$0.isEmpty }.joined(separator: " · ")
        let connection = codex.connection
        connectionRow.subtitle = connection.connecting ? L10n.T("Connecting…") : connection.connected
            ? L10n.T("Connected as %s", connection.email)
            : L10n.T("Not connected")
        signIn.isEnabled = !connection.connecting
        signIn.isHidden = connection.connected
        disconnect.isHidden = !connection.connected && !connection.connecting
        disconnect.isEnabled = connection.connected || connection.connecting
        usage.isEnabled = connection.connected
        modelRow.isEnabled = connection.connected
        install.isHidden = codex.available
        fillModels()
        let key = connection.connected && codex.available ? (codex.executable ?? "") + connection.email : ""
        if key != catalogKey {
            catalogKey = key
            generation += 1
            catalogTask?.cancel()
            catalog = []
            fillModels()
            if !key.isEmpty {
                loadModels(codex, generation: generation)
            }
        }
    }

    private func fillModels() {
        let id = settings.assistantChatGPTModel
        models.removeAllItems()
        models.addItem(withTitle: L10n.T("Use the provider’s default model"))
        models.lastItem?.representedObject = ""
        for model in catalog {
            models.addItem(withTitle: model.name)
            models.lastItem?.representedObject = model.id
        }
        if !id.isEmpty, !catalog.contains(where: { $0.id == id }) {
            models.addItem(withTitle: id)
            models.lastItem?.representedObject = id
        }
        models.select(models.itemArray.first { $0.representedObject as? String == id })
    }

    private func loadModels(_ codex: CodexProvider, generation my: Int) {
        catalogTask = Task { @MainActor [weak self] in
            do {
                let models = try await codex.models()
                guard let self, !self.closed, self.generation == my, !Task.isCancelled else { return }
                self.catalog = models
                self.modelRow.subtitle = ""
                self.fillModels()
            } catch {
                guard let self, !self.closed, self.generation == my, !Task.isCancelled else { return }
                self.modelRow.subtitle = L10n.T("Model catalog unavailable. You can keep the provider’s default model.")
            }
        }
    }

    @objc private func modelChanged(_ sender: Any?) {
        settings.assistantChatGPTModel = models.selectedItem?.representedObject as? String ?? ""
    }

    @objc private func chooseExecutable(_ sender: Any?) {
        guard let window = view.window else { return }
        let panel = NSOpenPanel()
        panel.canChooseDirectories = false
        panel.allowsMultipleSelection = false
        Task { @MainActor [weak self] in
            let response = await panel.beginSheetModal(for: window)
            guard response == .OK, let self, !self.closed, let path = panel.url?.path else { return }
            guard CodexProvider.nativeExecutable(path) else {
                self.toast(L10n.T("Codex was not found. Choose a native Codex executable."))
                return
            }
            self.settings.assistantCodexPath = path
            self.update()
        }
    }

    @objc private func connect(_ sender: Any?) {
        guard let connection = assistant?.chatGPT?.connection else { return }
        Task { @MainActor [weak self] in
            do {
                try await connection.signIn()
            } catch {
                self?.toast(L10n.T("Could not connect to ChatGPT."))
            }
            self?.update()
        }
    }

    @objc private func disconnectAccount(_ sender: Any?) {
        guard let connection = assistant?.chatGPT?.connection else { return }
        Task { @MainActor [weak self] in
            do {
                if !(try await connection.disconnect()) {
                    self?.toast(L10n.T("Disconnected locally; remote sign-out could not be confirmed."))
                }
            } catch {
                self?.toast(L10n.T("Could not connect to ChatGPT."))
            }
            self?.update()
        }
    }

    @objc private func openInstall(_ sender: Any?) {
        openInBrowser("https://developers.openai.com/codex/cli") { [weak self] in self?.toast($0) }
    }

    @objc private func openUsage(_ sender: Any?) {
        openInBrowser("https://chatgpt.com/settings/usage") { [weak self] in self?.toast($0) }
    }
}
