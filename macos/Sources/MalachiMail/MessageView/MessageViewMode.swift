// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

/// Where a `MessageViewController` lives. The one view of message_view.go
/// is shared by the main window's pane (window.blp), a stand-alone message
/// window (message_window.blp) and the window of an attached message
/// (embedded_window.blp); the three differ in a few places only.
enum MessageViewMode: Sendable, Equatable {
    /// The main window's message pane: has the "No Message Selected" page.
    case pane
    /// A stand-alone message window.
    case window
    /// An attached message (embedded.go): no outbox banner, no trust
    /// button, the chips only name the parts, Load Images re-renders the
    /// part through message.embedded.
    case embedded
}
