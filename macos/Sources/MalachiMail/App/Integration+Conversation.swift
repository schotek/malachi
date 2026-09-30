// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The shell's part of the conversation view: the reading pane that holds
/// the single-message view and the conversation view takes the message
/// pane's place, the conversation's cards hear what the message cache
/// learns (`MessageWindows`' fan-out), and Space in the list pages through
/// the conversation.
extension Integration {
    /// Installs the reading pane in place of the single-message view the
    /// init installed first (no account list has arrived yet, so the "No
    /// Accounts" page is not up), and wires the conversation view.
    func wireConversation() {
        windows.track(display: readingPane.conversationView)
        mainWindow?.install(message: readingPane)
        listView.onPageReader = { [weak self] up in
            self?.readingPane.page(up: up) ?? false
        }
    }
}
