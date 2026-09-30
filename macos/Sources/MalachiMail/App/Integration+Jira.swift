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
/// settings, or the assistant for a new token), and the messages the
/// daemon changed without arriving or leaving (notify.messagesChanged:
/// mail hidden or shown again for one, its own messages rebuilt in place).
extension Integration {
    /// Wires `hooks.addJiraAccount` and `hooks.canComposeNew`, and
    /// notify.messagesChanged to the mailbox, which lets the message cache
    /// go of the account's messages before the pane fetches them again.
    func wireJira() {
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
