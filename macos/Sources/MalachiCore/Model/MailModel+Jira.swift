// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// The message list of an issue-tracker (Jira) account: its folders are
// always listed as conversations, one per issue, whatever the "group by
// conversation" setting (`Jira.alwaysThreaded`), and an event (a status or
// assignee change) never counts as unread. Swift-first: the GTK list
// (ui/internal/window/model.go, thread_model.go) follows with the Jira
// widgets.

extension MailModel {
    /// Whether folder `k` is listed as conversations whatever the setting:
    /// a folder of a Jira account (model.go `alwaysGrouped`). False for no
    /// folder or an unknown account. The outbox stays flat all the same
    /// (the caller's rule, as for mail).
    public func alwaysGrouped(_ k: FolderKey?) -> Bool {
        guard let k, let a = account(k.account) else { return false }
        return Jira.alwaysThreaded(a.config)
    }
}

/// Whether a notified message raises its conversation's unread count: an
/// unseen message that is not an event of an issue (the daemon stores
/// events seen; this holds even when one arrives unseen).
func countsUnread(_ s: MessageSummary) -> Bool {
    !hasFlag(s.flags, .seen) && !Jira.isEvent(s.issue)
}
