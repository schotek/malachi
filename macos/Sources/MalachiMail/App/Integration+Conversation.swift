// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The shell's part of the conversation view: the reading pane that holds
/// the single-message view and the conversation (its host, with the
/// timeline and the chat) takes the message pane's place, the
/// conversation's cards and bubbles hear what the message cache learns
/// (`MessageWindows`' fan-out, through the host), Space in the list
/// pages through the conversation, and the pane's quick-reply bar (under
/// a single message and under the chat) acts through the window's message
/// actions, Escape in its field gives the keyboard back to the list, and
/// its Assistant button is the main window's toolbar ✦.
extension Integration {
    /// Installs the reading pane in place of the single-message view the
    /// init installed first (no account list has arrived yet, so the "No
    /// Accounts" page is not up), and wires the conversation view.
    func wireConversation() {
        windows.track(display: readingPane.conversation)
        mainWindow?.install(message: readingPane)
        listView.onPageReader = { [weak self] up in
            self?.readingPane.page(up: up) ?? false
        }
        readingPane.quickReplyDelegate = messageActions
        readingPane.onFocusList = { [weak listView] in
            listView?.focusList()
        }
        readingPane.onAssistant = { [weak self] button in
            self?.mainWindow?.showAssistant(from: button)
        }
    }
}
