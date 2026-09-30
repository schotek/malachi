// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The tags of the items that send `addAccount(_:)`: which assistant opens
/// (`AppDelegate.addAccount`). A button or item without a tag is the mail
/// account wizard.
enum AddAccountTag {
    static let mail = 0
    /// File ▸ Add Jira Account…
    static let jira = 1
}

/// The shell's part of issue-tracker (Jira) accounts: the assistant that
/// adds one (File ▸ Add Jira Account…), what the "edit account" routes of
/// the main window open for one (`accountEditor`, `jiraEditor`: its
/// settings, or the assistant for a new token), the messages the daemon
/// changed without arriving or leaving (notify.messagesChanged: mail
/// hidden or shown again for one, its own messages rebuilt in place), and
/// the Change Status menu of an issue (`IssueActionsController`, wired to
/// the cards' pills and the menus by `wireIssueActions`).
extension Integration {
    /// Wires `hooks.addJiraAccount` and `hooks.canComposeNew`, and
    /// notify.messagesChanged to the mailbox, which lets the message cache
    /// go of the account's messages before the pane fetches them again.
    func wireJira() {
        wireIssueActions()
        // The handler lives as long as the hub, like this object; the
        // token is not needed.
        _ = state.notifications.addMessagesChanged { [weak self] n in
            self?.mailbox.handleMessagesChanged(n)
        }
        mailbox.onMessagesChanged = { [weak self] n in
            self?.cache.evict(account: n.accountId)
        }
        state.hooks.addJiraAccount = { [weak self] window in
            guard let self, let parent = window ?? self.mainWindow?.window else { return }
            // The sidebar reloads on notify.accountsChanged; nothing to do here.
            JiraWizardWindowController.present(from: parent, client: self.state.client) { _, _ in }
        }
        // New Message needs an account that writes mail: an issue
        // tracker's only comments (in the compose window's comment mode).
        state.hooks.canComposeNew = { [weak self] in
            guard let self else { return true }
            return Capabilities.canComposeNew(self.mailbox.model.accounts)
        }
    }

    /// The Change Status menu (ui/internal/jira transitions.go): one
    /// controller for the application, reached through
    /// `hooks.issueActions` by the pills and the menus when they open; its
    /// outcome fans out to every view showing the issue (the pane, the
    /// conversation, the message windows), so the card changes at once. The
    /// submenu goes into the Message menu (after Forward) and the More
    /// Actions menus of the main window and of every message window, which
    /// are built without it (MainMenu, MainToolbar); the toolbars are fixed
    /// (no customisation), so the item is there to find.
    private func wireIssueActions() {
        let issueActions = IssueActionsController(
            client: state.client,
            account: { [weak self] id in self?.mailbox.model.account(id) },
            toast: { [weak self] text in self?.state.toasts.show(text) }
        )
        issueActions.onIssueChanged = { [weak self] account, info in
            guard let self else { return }
            for v in self.windows.views {
                v.applyIssue(info, account: account)
            }
            for d in self.windows.displays {
                (d as? ConversationViewController)?.applyIssue(info, account: account)
            }
        }
        issueActions.onBusy = { [weak self] account, key, busy in
            guard let self else { return }
            for v in self.windows.views {
                v.setIssueBusy(busy, account: account, key: key)
            }
            for d in self.windows.displays {
                (d as? ConversationViewController)?.setIssueBusy(busy, account: account, key: key)
            }
        }
        // The hook keeps the controller alive with the application.
        state.hooks.issueActions = { issueActions }

        ChangeStatusMenus.install(intoMenuBar: NSApp.mainMenu, state: state)
        if let mainWindow {
            ChangeStatusMenus.install(intoToolbar: mainWindow.window?.toolbar, state: state) { [weak mainWindow] in
                mainWindow?.transitionSubject
            }
        }
        windows.onMessageWindowOpened = { [weak self] wc in
            guard let self else { return }
            ChangeStatusMenus.install(intoToolbar: wc.window?.toolbar, state: self.state) { [weak wc] in
                wc?.transitionSubject
            }
        }
    }

    /// What edits a Jira account (`jiraEditor`), as a sheet on `parent`:
    /// its settings, or, when a route asks for the token (`requestToken`,
    /// the sign-in banner's reason), the assistant in edit mode, which
    /// says why it is needed. The sidebar and the banners follow
    /// notify.accountsChanged and notify.syncState after the save.
    func editJiraAccount(_ account: Account, parent: NSWindow, requestToken: ErrorCode?) {
        switch jiraEditor(requestToken: requestToken) {
        case .settings:
            JiraAccountWindowController.present(from: parent, client: state.client, account: account) { _, _ in }
        case .token:
            JiraWizardWindowController.present(
                from: parent, client: state.client, editing: account, requestToken: requestToken
            ) { _, _ in }
        }
    }
}
