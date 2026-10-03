// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// Thin preferences binding; credentials and provider lifecycle stay in Core.
@MainActor final class ChatGPTPreferences: NSObject {
    let group = PreferencesGroupView(title: ChatGPTText.name,
        description: L10n.T("Connect ChatGPT to use your plan with the in-app assistant."))
    private let provider = NSPopUpButton(frame: .zero, pullsDown: false)
    private let models = NSPopUpButton(frame: .zero, pullsDown: false)
    private let boardModels = NSPopUpButton(frame: .zero, pullsDown: false)
    lazy var providerRow = PreferenceRowView(title: L10n.T("In-app provider"), trailing: provider)
    lazy var boardModelRow = PreferenceRowView(title: Assistant.panelTexts().model, trailing: boardModels)
    private let signIn = NSButton(title: L10n.T("Continue with ChatGPT"), target: nil, action: nil)
    private let disconnect = NSButton(title: L10n.T("Disconnect"), target: nil, action: nil)
    private let choose = NSButton(title: Assistant.panelTexts().choose, target: nil, action: nil)
    private let install = NSButton(title: L10n.T("Get Codex…"), target: nil, action: nil)
    private let usage = NSButton(title: L10n.T("Manage usage"), target: nil, action: nil)
    private let settings: Settings
    private weak var assistant: AssistantController?
    private let toast: @MainActor (String) -> Void
    private var token: AssistantController.Token?
    private var executableRow: PreferenceRowView!
    private var connectionRow: PreferenceRowView!
    private var modelRow: PreferenceRowView!
    private var catalog: [CodexModel] = []
    private var catalogTask: Task<Void, Never>?
    private var catalogKey = ""
    private var versionPath = ""
    private var version = ""
    private var generation = 0
    private var updateQueued = false
    init(settings: Settings, assistant: AssistantController, toast: @escaping @MainActor (String) -> Void) {
        self.settings = settings; self.assistant = assistant; self.toast = toast
        super.init()
        provider.addItems(withTitles: [Assistant.targetName(.code), ChatGPTText.name])
        provider.target = self; provider.action = #selector(providerChanged)
        choose.target = self; choose.action = #selector(chooseExecutable)
        install.target = self; install.action = #selector(openInstall)
        signIn.target = self; signIn.action = #selector(connect)
        disconnect.target = self; disconnect.action = #selector(disconnectAccount)
        usage.target = self; usage.action = #selector(openUsage)
        models.target = self; models.action = #selector(modelChanged)
        boardModels.target = self; boardModels.action = #selector(boardModelChanged)
        executableRow = PreferenceRowView(title: L10n.T("Codex"), trailing: buttons([choose, install]))
        executableRow.setSubtitleSelectable()
        connectionRow = PreferenceRowView(title: L10n.T("Account"), trailing: buttons([signIn, disconnect]))
        modelRow = PreferenceRowView(title: Assistant.panelTexts().model, trailing: models)
        let usageRow = PreferenceRowView(title: L10n.T("Manage usage"), trailing: usage)
        group.setRows([executableRow, connectionRow, modelRow, usageRow])
        token = assistant.onChange { [weak self] in self?.scheduleUpdate() }
        update()
    }
    private func buttons(_ views: [NSView]) -> NSStackView {
        let stack = NSStackView(views: views); stack.orientation = .horizontal; stack.spacing = 6; return stack
    }
    func scheduleUpdate() {
        guard !updateQueued else { return }
        updateQueued = true
        DispatchQueue.main.async { [weak self] in
            guard let self else { return }
            self.updateQueued = false
            self.update()
        }
    }
    private func update() {
        let selected = settings.assistantProvider == .chatgpt
        provider.selectItem(at: selected ? 1 : 0)
        group.isHidden = !selected || settings.assistantTarget != .app
        guard let codex = assistant?.chatGPT else { return }
        let path = codex.executable ?? ""
        if path != versionPath {
            versionPath = path; version = ""
            Task { @MainActor [weak self] in
                let version = await codex.version()
                guard let self, self.versionPath == path else { return }
                self.version = version ?? ""; self.update()
            }
        }
        executableRow.subtitle = path.isEmpty ? L10n.T("Codex was not found. Choose a native Codex executable.") : [path, version].filter { !$0.isEmpty }.joined(separator: " · ")
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
            catalogKey = key; generation += 1; catalogTask?.cancel(); catalog = []; fillModels()
            if !key.isEmpty { loadModels(codex, generation: generation) }
        }
    }
    private func fillModels() {
        for (popup, id) in [(models, settings.assistantChatGPTModel), (boardModels, settings.boardTriageChatGPTModel)] {
            popup.removeAllItems(); popup.addItem(withTitle: L10n.T("Use the provider’s default model")); popup.lastItem?.representedObject = ""
            for model in catalog { popup.addItem(withTitle: model.name); popup.lastItem?.representedObject = model.id }
            if !id.isEmpty, !catalog.contains(where: { $0.id == id }) { popup.addItem(withTitle: id); popup.lastItem?.representedObject = id }
            popup.select(popup.itemArray.first { $0.representedObject as? String == id })
        }
    }
    private func loadModels(_ codex: CodexProvider, generation my: Int) {
        catalogTask = Task { @MainActor [weak self] in
            do {
                let models = try await codex.models()
                guard let self, self.generation == my, !Task.isCancelled else { return }
                self.catalog = models; self.modelRow.subtitle = ""; self.fillModels()
            } catch {
                guard let self, self.generation == my, !Task.isCancelled else { return }
                self.modelRow.subtitle = L10n.T("Model catalog unavailable. You can keep the provider’s default model.")
            }
        }
    }
    @objc private func providerChanged() { settings.assistantProvider = provider.indexOfSelectedItem == 1 ? .chatgpt : .claude; scheduleUpdate() }
    @objc private func modelChanged() { settings.assistantChatGPTModel = models.selectedItem?.representedObject as? String ?? "" }
    @objc private func boardModelChanged() { settings.boardTriageChatGPTModel = boardModels.selectedItem?.representedObject as? String ?? "" }
    @objc private func chooseExecutable() {
        let panel = NSOpenPanel(); panel.canChooseDirectories = false; panel.allowsMultipleSelection = false
        panel.begin { [weak self] result in
            Task { @MainActor in
                guard result == .OK, let self, let path = panel.url?.path else { return }
                guard CodexProvider.nativeExecutable(path) else { self.toast(L10n.T("Codex was not found. Choose a native Codex executable.")); return }
                self.settings.assistantCodexPath = path; self.update()
            }
        }
    }
    @objc private func connect() {
        guard let connection = assistant?.chatGPT?.connection else { return }
        Task { @MainActor [weak self] in
            do { try await connection.signIn() }
            catch { self?.toast(L10n.T("Could not connect to ChatGPT.")) }
            self?.update()
        }
    }
    @objc private func disconnectAccount() {
        guard let connection = assistant?.chatGPT?.connection else { return }
        Task { @MainActor [weak self] in
            do { if !(try await connection.disconnect()) { self?.toast(L10n.T("Disconnected locally; remote sign-out could not be confirmed.")) } }
            catch { self?.toast(L10n.T("Could not connect to ChatGPT.")) }
            self?.update()
        }
    }
    @objc private func openInstall() { openInBrowser("https://developers.openai.com/codex/cli", onError: toast) }
    @objc private func openUsage() { openInBrowser("https://chatgpt.com/settings/usage", onError: toast) }
}
