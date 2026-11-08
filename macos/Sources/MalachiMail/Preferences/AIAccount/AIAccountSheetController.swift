// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The dialog that adds or edits an AI provider account (kind `ai`,
/// docs/plans/ai-api.md §7.2) as a sheet: what Settings → Integrations
/// opens from the catalogue's Add… of Anthropic API, OpenAI API and
/// OpenAI-compatible server, from an `ai` row's Edit and its Sign In…, and
/// what the application's `hooks.editAIAccount` / `hooks.addAIAccount`
/// open (the status bar's sign-in, the board's Triage button). The page
/// is `AIAccountViewController` over `AIAccountController`, the pattern of
/// the Jira account sheet (`JiraAccountWindowController`; GTK
/// ui/internal/aiaccount/dialog.go).
@MainActor
final class AIAccountSheetController: NSWindowController {
    /// The open pages, kept alive while their sheets are up.
    private static var active: [ObjectIdentifier: AIAccountSheetController] = [:]

    let controller: AIAccountController
    private let root: AIAccountViewController
    private var onDone: (@MainActor (AccountID, AccountConfig) -> Void)?

    /// Opens the dialog as a sheet on `parent`: adding an account of
    /// `provider` when `editing` is nil, else editing that account (its
    /// provider wins); `replaceKey` opens the key field of an existing
    /// account at once (a route that says the provider refused the key).
    /// `confirmTrust` renders "Trust This Certificate?" for an https server
    /// the test refused. `onDone` runs after account.add or account.update
    /// succeeded, before the sheet closes (a page that had nothing to save
    /// closes without it).
    static func present(
        from parent: NSWindow, client: RPCClient, provider: AIProvider, editing: Account? = nil, replaceKey: Bool = false,
        confirmTrust: @escaping WizardConfirmTrust, onDone: @escaping @MainActor (AccountID, AccountConfig) -> Void
    ) {
        let c = AIAccountSheetController(client: client, provider: provider, editing: editing, confirmTrust: confirmTrust)
        c.onDone = onDone
        guard let sheet = c.window else { return }
        active[ObjectIdentifier(c)] = c
        parent.beginSheet(sheet) { _ in
            MainActor.assumeIsolated {
                c.controller.close()
                active[ObjectIdentifier(c)] = nil
            }
        }
        c.controller.start()
        if replaceKey {
            c.root.replaceKey()
        }
    }

    private init(client: RPCClient, provider: AIProvider, editing: Account?, confirmTrust: @escaping WizardConfirmTrust) {
        let controller = AIAccountController(client: client, provider: provider, editing: editing)
        self.controller = controller
        root = AIAccountViewController(controller: controller, confirmTrust: confirmTrust)
        let window = WizardSheetWindow(
            contentRect: NSRect(origin: .zero, size: AIAccountViewController.size),
            styleMask: [.titled, .fullSizeContentView],
            backing: .buffered, defer: false
        )
        window.title = controller.title
        window.titleVisibility = .hidden
        window.titlebarAppearsTransparent = true
        window.isReleasedWhenClosed = false
        window.autorecalculatesKeyViewLoop = true
        window.contentViewController = root
        window.initialFirstResponder = root.initialFirstResponder
        super.init(window: window)
        root.onCancel = { [weak self] in self?.dismiss() }
        window.onCancel = { [weak self] in self?.dismiss() }
        controller.onChange = { [weak self] in self?.root.refresh() }
        controller.onBusy = { [weak self] text in self?.root.showBusy(text) }
        controller.onBanner = { [weak self] text in self?.root.showBanner(text) }
        controller.onProblem = { [weak self] field in self?.root.flag(field) }
        controller.onClose = { [weak self] in self?.dismiss() }
        controller.onDone = { [weak self] id, cfg in
            guard let self else { return }
            self.onDone?(id, cfg)
            self.dismiss()
        }
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Closes the sheet; late replies are dropped from now on.
    func dismiss() {
        controller.close()
        guard let window else { return }
        if let parent = window.sheetParent {
            parent.endSheet(window)
        } else {
            window.close()
        }
    }
}
