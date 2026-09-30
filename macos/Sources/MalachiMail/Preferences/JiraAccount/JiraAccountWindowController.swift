// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The settings of a Jira account as a sheet: what Settings ▸ Accounts and
/// the main window open to edit such an account (`accountEditor`,
/// `jiraEditor`). The page is `JiraAccountViewController` over
/// `JiraAccountController`; Replace Token… opens the account assistant in
/// its edit mode (`JiraWizardWindowController`) as a sheet on this one,
/// and the page lists the spaces again once the new token is stored.
@MainActor
final class JiraAccountWindowController: NSWindowController {
    /// The open pages, kept alive while their sheets are up.
    private static var active: [ObjectIdentifier: JiraAccountWindowController] = [:]

    let controller: JiraAccountController
    private let root: JiraAccountViewController
    private var onDone: (@MainActor (AccountID, AccountConfig) -> Void)?

    /// Opens the settings of `account` as a sheet on `parent`. `onDone`
    /// runs after account.update succeeded, before the sheet closes (a
    /// page that had nothing to save closes without it), and after the
    /// assistant stored a new token, over the sheet that stays: the
    /// caller's list shows the account as it is now (jira_editors.go
    /// `editJiraAccount` of the preferences).
    static func present(
        from parent: NSWindow, client: RPCClient, account: Account,
        onDone: @escaping @MainActor (AccountID, AccountConfig) -> Void
    ) {
        let c = JiraAccountWindowController(client: client, account: account)
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
    }

    private init(client: RPCClient, account: Account) {
        let controller = JiraAccountController(client: client, account: account)
        self.controller = controller
        root = JiraAccountViewController(controller: controller)
        let window = WizardSheetWindow(
            contentRect: NSRect(origin: .zero, size: JiraAccountViewController.size),
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
        controller.onClose = { [weak self] in self?.dismiss() }
        controller.onDone = { [weak self] id, cfg in
            guard let self else { return }
            self.onDone?(id, cfg)
            self.dismiss()
        }
        controller.onReplaceToken = { [weak self] account in self?.replaceToken(account) }
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// The account assistant for a new token, on top of this sheet. It
    /// stores the account as it is with the token it was given; what the
    /// page has edited stays in the page.
    private func replaceToken(_ account: Account) {
        guard let window else { return }
        JiraWizardWindowController.present(from: window, client: controller.client, editing: account) { [weak self] id, cfg in
            self?.controller.tokenReplaced()
            self?.onDone?(id, cfg)
        }
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
