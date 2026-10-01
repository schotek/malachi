// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// A click on a new-message notification: ui/internal/window/notify_open.go.
extension Integration {
    /// Opens message `id` of account `account`, whose desktop notification
    /// the user clicked, in its own window (or raises the window that
    /// already shows it) and marks it read, as a double-click in the list
    /// does (`OpenNotifiedMessage`). The main window stays as it is: hidden
    /// when it runs in the background, behind the message window
    /// otherwise. The message need not be in any list the window shows;
    /// what the cache does not know, message.get tells. A message the
    /// daemon no longer has presents the main window instead, as every
    /// click did before.
    ///
    /// A click that started the application comes before the connection
    /// to the daemon: the message waits for it (`openPendingNotified`), the
    /// latest click replacing an earlier one, and the main window shows
    /// meanwhile (with the banner, should the daemon not come).
    func openNotifiedMessage(account: AccountID, id: MessageID) {
        mailbox.withdrawNotifications([id])
        guard case .connected = state.notifications.connectionState else {
            notifiedPending = (account, id)
            state.showMainWindow()
            return
        }
        if let s = actions.summary(id) {
            openNotified(s)
            return
        }
        cache.lookUp(accountId: account, id: id) { [weak self] s in
            guard let self else { return }
            guard let s else {
                self.state.showMainWindow()
                return
            }
            self.openNotified(s)
        }
    }

    /// Opens the message of a notification clicked before the connection
    /// was up, once it is (the connection handler of `wireConnection`).
    func openPendingNotified() {
        guard let p = notifiedPending else { return }
        notifiedPending = nil
        openNotifiedMessage(account: p.account, id: p.message)
    }

    /// Opens the window of message `s` and marks it read (`setSeen` leaves
    /// a read or outbox message alone).
    private func openNotified(_ s: MessageSummary) {
        windows.openMessage(s)
        NSApp.activate()
        actions.markRead(s.id)
    }
}
