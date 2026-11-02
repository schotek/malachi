// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The application's mail rules (docs/api.md §4.21, docs/plans/rules.md
/// R14): the one `RulesController`, which lists the rules when the
/// connection comes and on notify.rulesChanged and notify.accountsChanged
/// (with MALACHI_RULES_SAMPLES=1 over the invented samples,
/// `SampleRulesCaller`), and the ways into it: Settings → Rules, the rule
/// editor sheet (Add Rule…, Edit Rule…, Create Rule from Message…) and a
/// folder's Apply Rules…. Every question is an alert on the window the
/// action came from; every result a toast.
@MainActor
final class AppRules {
    unowned let state: AppState
    let samples = SampleRulesCaller.requested
    let caller: any RulesCaller
    let controller: RulesController
    private var tokens: [NotificationHub.Token] = []
    /// The open editor sheets, kept alive while they show.
    private var editors: [ObjectIdentifier: RuleEditorWindowController] = [:]

    init(state: AppState) {
        self.state = state
        let caller: any RulesCaller = samples ? SampleRulesCaller() : state.client
        self.caller = caller
        let controller = RulesController(caller: caller)
        self.controller = controller
        let samples = samples
        tokens = [
            state.notifications.addRulesChanged { controller.handle(API.Notify.rulesChanged) },
            state.notifications.addAccountsChanged { controller.handle(API.Notify.accountsChanged) },
            state.notifications.addConnectionState { s in
                guard !samples else { return }
                if AppState.isConnected(s) {
                    controller.start()
                } else {
                    controller.stop()
                }
            },
        ]
        if samples || AppState.isConnected(state.notifications.connectionState) {
            controller.start()
        }
    }

    /// The questions of the controllers as alerts on `window` (a sheet when
    /// there is one, else application-modal); Cancel is the other button.
    func confirm(on window: NSWindow?) -> Rules.Confirm {
        let alerts = state.alerts
        return { q in
            if q.destructive {
                return await alerts.confirmDestructive(on: window, heading: q.heading, body: q.body, confirmLabel: q.confirm)
            }
            return await alerts.confirm(
                on: window, heading: q.heading, body: q.body, confirmLabel: q.confirm, declineLabel: L10n.T("_Cancel"))
        }
    }

    // MARK: The editor

    /// Add Rule…: a new rule for `account` (nil: every mail account).
    func addRule(account: AccountID?, on window: NSWindow?) {
        present(RuleEditorController(caller: caller, newFor: account, rules: controller), on: window)
    }

    /// Edit Rule… of a listed rule.
    func editRule(_ id: String, on window: NSWindow?) {
        guard let r = controller.rule(id) else { return }
        present(RuleEditorController(caller: caller, editing: r, rules: controller), on: window)
    }

    /// Create Rule from Message…: the editor prefilled from `message`
    /// (`Rules.fromMessage`), for its account.
    func createRule(from message: MessageSummary, on window: NSWindow?) {
        let form = Rules.fromMessage(message, accountId: message.accountId)
        present(RuleEditorController(
            caller: caller, form: form, stored: nil, accounts: controller.accounts, folders: controller.folders), on: window)
    }

    /// Whether Create Rule from Message… is offered for `message` (one row
    /// of a mail account, not in the outbox, drafts or sent mail).
    func canCreateRule(from message: MessageSummary, role: FolderRole, account: Account?) -> Bool {
        guard let account, Rules.isMailAccount(account), message.issue == nil else { return false }
        return Rules.canCreateFrom(role)
    }

    private func present(_ editor: RuleEditorController, on window: NSWindow?) {
        guard let parent = window ?? state.mainWindow?.window else { return }
        let c = RuleEditorWindowController(editor: editor, rules: self)
        let key = ObjectIdentifier(c)
        editors[key] = c
        c.onClosed = { [weak self] in self?.editors[key] = nil }
        c.present(on: parent)
    }

    /// The editor sheets that show (`MALACHI_START` rules-editor).
    var openEditors: [RuleEditorWindowController] { Array(editors.values) }

    /// A rule the editor saved.
    func saved(_ r: Rule) {
        controller.saved(r)
    }

    // MARK: Apply Rules…

    /// Apply Rules… of a folder: the question on `window`, the progress
    /// line and the result as toasts.
    func apply(_ k: FolderKey, folderName: String, on window: NSWindow?) {
        let toasts = state.toasts
        let controller = controller
        let ask = confirm(on: window)
        Task { @MainActor in
            let text = await controller.apply(k, folderName: folderName) { q in
                guard await ask(q) else { return false }
                // The progress until the result replaces it.
                toasts.show(Rules.applyingText, seconds: 0)
                return true
            }
            if let text {
                toasts.show(text)
            }
        }
    }
}
