// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// Withdrawing the desktop notifications of new messages once they are
/// outdated (ui/internal/window/notify.go `withdrawNotifications` and the
/// functions after it). The notifications themselves are the AppKit half's
/// (`NotificationService`), which records each one it posts here
/// (`recordNotification`) and removes the delivered ones through
/// `withdrawDelivered`. Log lines carry the method and error only.
extension MailboxController {
    /// A notification for `n` was posted (the tail of notify.go
    /// `notifyNewMessage`): it is remembered, and one pushed out beyond
    /// `notifiedMax` is withdrawn.
    public func recordNotification(_ n: NewMessageNotification) {
        withdrawDelivered(notified.add(n.message.id, FolderKey(account: n.accountId, folder: n.folderId)))
    }

    /// Withdraws the notifications of the given messages that the app
    /// posted and still remembers; the others are left alone
    /// (notify.go `withdrawNotifications`). Called when the actions read,
    /// moved or trashed them and when the daemon says they are outdated
    /// (`verifyNotifications`).
    public func withdrawNotifications(_ ids: [MessageID]) {
        withdrawDelivered(notified.remove(ids))
    }

    /// Removes the notifications of messages the set has just let go of
    /// (notify.go `withdraw`).
    func withdrawDelivered(_ ids: [MessageID]) {
        guard !ids.isEmpty else { return }
        onWithdrawNotifications?(ids.map(notificationID))
    }

    /// Withdraws the notifications of the selected folder's messages while
    /// the main window is key (it became key, or the folder was selected in
    /// it) (notify.go `withdrawViewedNotifications`): the folder's list
    /// shows those messages now, so their notifications have nothing left
    /// to announce. Notifications of other folders stay until their folder
    /// is viewed or their message is read, moved or deleted.
    public func withdrawViewedNotifications() {
        guard let sel = model.selected, isMainWindowKey?() ?? false else { return }
        withdrawDelivered(notified.removeFolder(sel))
    }

    /// Withdraws the notifications of accounts that are gone or paused
    /// (after account.list; notify.go `withdrawAccountNotifications`):
    /// their messages are shown nowhere, and no sync pass of theirs will
    /// check them.
    func withdrawAccountNotifications() {
        withdrawDelivered(notified.removeAccounts(except: Set(model.enabledAccounts.map(\.id))))
    }

    /// Runs after a sync pass of account `acc` (notify.go
    /// `verifyNotifications`): asks the daemon (message.get) about every
    /// message of the account whose notification may still show and
    /// withdraws the outdated ones (`notificationOutdated`) — read, moved
    /// or deleted on another device, by another client of the daemon or by
    /// a server rule. One check per account runs at a time; a pass that
    /// ends meanwhile gets one more check once it is done.
    func verifyNotifications(_ acc: AccountID) {
        let entries = notified.ofAccount(acc)
        guard !entries.isEmpty else { return }
        if verifyingNotifications[acc] != nil {
            verifyingNotifications[acc] = true
            return
        }
        verifyingNotifications[acc] = false
        let client = client
        Task { [weak self] in
            var outdated: [MessageID] = []
            for e in entries {
                let answer: Result<MessageSummary, any Error>
                do {
                    let res = try await client.call(API.MessageGet.self, MessageGetParams(accountId: acc, messageId: e.id))
                    answer = .success(res.message.summary)
                } catch {
                    answer = .failure(error)
                }
                guard let stale = notificationOutdated(e, answer) else {
                    if case .failure(let err) = answer {
                        self?.log.debug("message.get: \(String(describing: err), privacy: .public)")
                    }
                    break // the next pass asks again
                }
                if stale {
                    outdated.append(e.id)
                }
            }
            guard let self, !self.closed else { return }
            let again = self.verifyingNotifications[acc] ?? false
            self.verifyingNotifications[acc] = nil
            self.withdrawNotifications(outdated)
            if again {
                self.verifyNotifications(acc)
            }
        }
    }
}
