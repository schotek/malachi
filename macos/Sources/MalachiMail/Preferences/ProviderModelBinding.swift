// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// A Model pop-up of Settings → AI (the panel's, and the board's in the
/// Board group) that follows the provider in effect (docs/plans/ai-api.md
/// §7.3; GTK ui/internal/window/preferences_chatgpt.go
/// `bindProviderModel`): Claude Code offers the fixed models of
/// `Assistant.models` (`assistant-model`, `board-triage-model`), ChatGPT
/// the catalogue Codex lists, with the provider's default first
/// (`assistant-chatgpt-model`, `board-triage-chatgpt-model`), and an AI
/// provider account the models the daemon lists for it
/// (assistant.providerModels) with the account's own stored model: a
/// choice is written to the account (account.update with its
/// configuration and the changed model, never credentials; the board's
/// first entry, empty, uses the account's model). A catalogue is asked
/// once per key (the provider, the connection, the account); a choice
/// does not ask again. The row's subtitle says that an account's model is
/// saved to the account, or that ChatGPT's catalogue is unavailable.
@MainActor
final class ProviderModelBinding {
    /// Which model the row chooses: the panel's (and the one-shot
    /// requests'), or the board's triage.
    enum Purpose {
        case panel
        case board
    }

    private let popup: NSPopUpButton
    private weak var row: PreferenceRowView?
    private let purpose: Purpose
    private let settings: Settings
    private weak var assistant: AssistantController?
    private let client: RPCClient?
    private let toast: @MainActor (String) -> Void
    /// The values of the items shown; "" is the provider's default
    /// (ChatGPT) or the account's model (the board over an account).
    private var ids: [String] = []
    /// The catalogue of the provider as last listed, and what it was
    /// listed for (nil: not yet).
    private var catalog: [(id: String, name: String)] = []
    private var catalogKey: String?
    private var generation = 0
    /// A choice written to the daemon and not listed back yet (an
    /// account's model).
    private var pending = ""
    private var syncing = false
    private var queued = false
    private var closed = false
    private var tokens: [Settings.ChangeToken] = []
    private var assistantToken: AssistantController.Token?

    init(
        popup: NSPopUpButton, row: PreferenceRowView?, purpose: Purpose, settings: Settings, assistant: AssistantController?,
        client: RPCClient?, toast: @escaping @MainActor (String) -> Void
    ) {
        self.popup = popup
        self.row = row
        self.purpose = purpose
        self.settings = settings
        self.assistant = assistant
        self.client = client
        self.toast = toast
        popup.autoenablesItems = false
        popup.target = self
        popup.action = #selector(chosen(_:))
        for key in [Settings.Key.assistantModel, .boardTriageModel, .assistantChatGPTModel, .boardTriageChatGPTModel, .assistantProvider] {
            tokens.append(settings.onChange(key) { [weak self] in self?.scheduleUpdate() })
        }
        assistantToken = assistant?.onChange { [weak self] in self?.scheduleUpdate() }
        update()
    }

    /// Stops following; the pop-up keeps what it shows.
    func cancel() {
        closed = true
        generation += 1
        for t in tokens {
            t.cancel()
        }
        tokens = []
        assistantToken?.cancel()
        assistantToken = nil
    }

    /// Selection actions and settings writes run inside the pop-up's own
    /// update; the items are replaced once, afterwards.
    func scheduleUpdate() {
        guard !queued, !closed else { return }
        queued = true
        DispatchQueue.main.async { [weak self] in
            guard let self else { return }
            self.queued = false
            self.update()
        }
    }

    // MARK: Showing

    private var source: Assistant.ProviderSource {
        assistant?.source ?? Assistant.sourceOf(settings.assistantProvider)
    }

    private var chatGPTModel: String {
        purpose == .board ? settings.boardTriageChatGPTModel : settings.assistantChatGPTModel
    }

    private func accountModel(_ a: Account) -> String {
        guard let ai = a.config.ai else { return "" }
        return purpose == .board ? ai.boardModel : ai.model
    }

    /// Shows the catalogue with `current`, which it adds when the catalogue
    /// lacks it; `defaultName` (not nil) puts the empty value first.
    private func fill(current: String, defaultName: String?) {
        syncing = true
        defer { syncing = false }
        ids = []
        popup.removeAllItems()
        if let defaultName {
            ids.append("")
            popup.addItem(withTitle: defaultName)
        }
        for m in catalog where !m.id.isEmpty && !ids.contains(m.id) {
            ids.append(m.id)
            popup.addItem(withTitle: m.name.isEmpty ? m.id : m.name)
        }
        if !ids.contains(current) {
            ids.append(current)
            popup.addItem(withTitle: current)
        }
        popup.selectItem(at: ids.firstIndex(of: current) ?? 0)
    }

    /// Shows `current`, or fills again when it is not listed.
    private func reselect(current: String, defaultName: String?) {
        if let i = ids.firstIndex(of: current) {
            syncing = true
            popup.selectItem(at: i)
            syncing = false
            return
        }
        fill(current: current, defaultName: defaultName)
    }

    private func update() {
        guard !closed else { return }
        let x = Assistant.apiText()
        switch source {
        case .claude:
            if catalogKey != "claude" {
                catalogKey = "claude"
                generation += 1
                catalog = Assistant.models.map { ($0.rawValue, Assistant.modelName($0)) }
            }
            row?.subtitle = ""
            let current = purpose == .board ? settings.boardTriageModel.rawValue : settings.assistantModel.rawValue
            reselect(current: current, defaultName: nil)
        case .chatgpt:
            let codex = assistant?.chatGPT
            let ready = codex.map { $0.available && $0.connected } ?? false
            let key = "chatgpt|\(codex?.executable ?? "")|\(ready)|\(codex?.connection.email ?? "")"
            let defaultName = L10n.T("Use the provider’s default model")
            if catalogKey == key {
                reselect(current: chatGPTModel, defaultName: defaultName)
                return
            }
            catalogKey = key
            catalog = []
            row?.subtitle = ""
            generation += 1
            let my = generation
            fill(current: chatGPTModel, defaultName: defaultName)
            guard let codex, ready else { return }
            Task { @MainActor [weak self] in
                let listed: [CodexModel]?
                do {
                    listed = try await codex.models()
                } catch {
                    listed = nil
                }
                guard let self, !self.closed, self.generation == my, self.source == .chatgpt else { return }
                guard let listed else {
                    self.row?.subtitle = L10n.T("Model catalog unavailable. You can keep the provider’s default model.")
                    return
                }
                self.catalog = listed.map { ($0.id, $0.name) }
                self.fill(current: self.chatGPTModel, defaultName: defaultName)
            }
        case .account:
            guard let acc = assistant?.apiAccount else {
                // The account the setting names is not listed (yet): the
                // row shows nothing to choose.
                row?.subtitle = purpose == .board ? x.boardModelSubtitle : x.modelSubtitle
                catalogKey = nil
                catalog = []
                fill(current: "", defaultName: purpose == .board ? x.boardModelDefault : nil)
                return
            }
            var current = accountModel(acc)
            if !pending.isEmpty {
                if pending == current {
                    pending = ""
                } else {
                    current = pending
                }
            }
            let defaultName: String? = purpose == .board ? x.boardModelDefault : nil
            row?.subtitle = purpose == .board ? x.boardModelSubtitle : x.modelSubtitle
            let key = "account|\(acc.id.rawValue)|\(acc.enabled)"
            if catalogKey == key {
                reselect(current: current, defaultName: defaultName)
                return
            }
            catalogKey = key
            catalog = []
            generation += 1
            let my = generation
            fill(current: current, defaultName: defaultName)
            guard let client else { return }
            let params = AssistantProviderModelsParams(accountId: acc.id)
            Task { @MainActor [weak self] in
                let res = try? await client.call(API.AssistantProviderModels.self, params, timeout: RPCTimeouts.assistantModels)
                guard let self, !self.closed, self.generation == my, let res, self.source == .account else { return }
                self.catalog = res.models.map { (Jira.clean($0.id), Jira.clean($0.displayName ?? "")) }
                guard let acc = self.assistant?.apiAccount else { return }
                var current = self.accountModel(acc)
                if !self.pending.isEmpty {
                    current = self.pending
                }
                self.fill(current: current, defaultName: defaultName)
            }
        }
    }

    // MARK: Choosing

    @objc private func chosen(_ sender: Any?) {
        guard !syncing, !closed else { return }
        let i = popup.indexOfSelectedItem
        guard ids.indices.contains(i) else { return }
        let chosen = ids[i]
        switch source {
        case .claude:
            if purpose == .board {
                settings.boardTriageModel = Assistant.parseModel(chosen)
            } else {
                settings.assistantModel = Assistant.parseModel(chosen)
            }
        case .chatgpt:
            if purpose == .board {
                settings.boardTriageChatGPTModel = chosen
            } else {
                settings.assistantChatGPTModel = chosen
            }
        case .account:
            chooseAccountModel(chosen)
        }
    }

    /// Writes `model` to the chosen account: account.update with the
    /// account's configuration and the changed model, no credentials.
    private func chooseAccountModel(_ model: String) {
        guard let acc = assistant?.apiAccount, let client, var ai = acc.config.ai, accountModel(acc) != model else { return }
        var cfg = acc.config
        if purpose == .board {
            ai.boardModel = model
        } else {
            ai.model = model
        }
        cfg.ai = ai
        pending = model
        let what = Assistant.apiText().modelRow
        Task { @MainActor [weak self] in
            do {
                _ = try await client.call(API.AccountUpdate.self, AccountUpdateParams(accountId: acc.id, config: cfg), timeout: RPCTimeouts.save)
            } catch {
                guard let self, !self.closed else { return }
                self.pending = ""
                self.toast(rpcErrorText(what, error))
                self.update()
            }
        }
    }
}
