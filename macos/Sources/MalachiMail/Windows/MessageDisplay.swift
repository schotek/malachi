// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// A view that shows several messages at once (the conversation view of the
/// reading pane), reached by the fan-out of `MessageWindows` beside the
/// single-message views: what the cache learns about a message goes to
/// every display that shows it (remote.go `showLoaded`,
/// `refreshRemoteBar`, download.go `refreshChips`, outbox.go
/// `showOutboxState`).
@MainActor
protocol MessageDisplay: AnyObject {
    /// The actions of the display's messages (set by `MessageWindows`).
    var delegate: (any MessageActionDelegate)? { get set }
    /// A chip's View: the attached message in its own window.
    var onOpenEmbedded: (@MainActor (_ containing: MessageSummary, _ attachment: Attachment, _ remote: Bool, _ chip: NSView?) -> Void)? { get set }

    /// Whether message `id` is on display here.
    func displays(_ id: MessageID) -> Bool
    /// The cache settled a half of message `id`, or its images arrived.
    func showLoaded(_ id: MessageID, _ lm: LoadedMessage)
    /// Only the bars of message `id` changed.
    func refreshRemoteBar(_ id: MessageID, _ lm: LoadedMessage)
    /// The chips of message `id` are drawn again (a download began to show
    /// its spinner, or ended); nil when the cache no longer holds it.
    func refreshChips(_ id: MessageID, _ lm: LoadedMessage?)
    /// The delivery state of message `id` changed (`m` is the cached
    /// message, nil while message.get has not answered).
    func showOutboxState(_ id: MessageID, _ m: Message?)
}
