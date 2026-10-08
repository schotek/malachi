// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The board's ways into the mail (MainWindowController.BoardMail): Reply
/// through the actions (a comment on an issue tracker's account, as the
/// capability check decides), Show in Mail through the list
/// (`ListController.reveal`), with the message's own window when the list
/// cannot show it, the compose manager's controller for the inline
/// reply editor's account list, and the message cache and the message
/// windows' fan-out for the HTML of the detail's conversation cards.
/// Swift-first, like `Board`.
extension Integration {
    func wireBoard() {
        guard let mainWindow else { return }
        mainWindow.boardMail = MainWindowController.BoardMail(
            reply: { [weak self, weak mainWindow] account, message in
                self?.actions.openCompose(.reply, account: account, message: message, from: mainWindow?.window)
            },
            composer: compose.controller,
            reveal: { [weak self] account, message, folder, thread, found in
                self?.revealInMail(account: account, message: message, folder: folder, thread: thread, found: found)
            },
            leftMail: { [weak self] in
                // Nothing may select a row of the hidden panes.
                self?.list.cancelReveal()
            }
        )
        // The detail's conversation cards show a message's HTML as Mail's
        // conversation cards do: the same cache, the same web view, the
        // same link policy through the message windows' fan-out.
        let windows = windows
        mainWindow.boardActions.mailBodies = BoardMailBodies(
            cache: cache, settings: state.settings, track: { windows.track(display: $0) })
        // Open at Launch and Show the Board, now that the board can act.
        mainWindow.setupModeMemory()
    }

    /// Selects message `message` in `folder` (its own folder, looked up,
    /// when nil), calling `found` before the row is selected; opens it in
    /// its own window when the list cannot show it. An answer that arrives
    /// after the window went back to the board does nothing.
    private func revealInMail(
        account: AccountID, message: MessageID, folder: FolderID?, thread: ThreadID?, found: @escaping @MainActor () -> Void
    ) {
        guard let folder else {
            cache.lookUpOutcome(accountId: account, id: message) { [weak self] outcome in
                guard let self, self.mainWindow?.mode == .mail else { return }
                guard case .found(let s) = outcome else {
                    self.revealFailed(outcome)
                    return
                }
                self.list.reveal(
                    account: account, folder: s.folderId, message: message, thread: thread ?? s.threadId, found: found
                ) { [weak self] in
                    self?.windows.openMessage(s)
                }
            }
            return
        }
        list.reveal(account: account, folder: folder, message: message, thread: thread, found: found) { [weak self] in
            guard let self else { return }
            self.cache.lookUpOutcome(accountId: account, id: message) { [weak self] outcome in
                guard let self, self.mainWindow?.mode == .mail else { return }
                guard case .found(let s) = outcome else {
                    self.revealFailed(outcome)
                    return
                }
                self.windows.openMessage(s)
            }
        }
    }

    /// Show in Mail found no message: gone, or not to be asked about.
    private func revealFailed(_ outcome: MessageCache.LookUp) {
        if case .gone = outcome {
            mainWindow?.toasts.show(Board.Text.showInMailGone)
        } else {
            mainWindow?.toasts.show(Board.Text.showInMailFailed)
        }
    }
}
