// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import os

/// `QuickReplyHost` over the main window (GTK window/quick_reply.go
/// `quickReplyHost`): the daemon's client for Send's calls, the main actor
/// for their outcome, and what the window knows to reply as its compose
/// window would — the attribution line, the download before a reply and
/// the compose window itself, all through the window's message actions
/// (`MessageActionDelegate`, whose `ActionsController` does them for the
/// compose window too). The texts are the compose window's ("Message queued
/// for sending" or a comment's, the failed send's), so this file adds no
/// msgid.
@MainActor
final class QuickReplyHostAdapter: QuickReplyHost {
    private let state: AppState
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "quick-reply")
    /// The window's message actions (the conversation host's delegate).
    weak var delegate: (any MessageActionDelegate)?
    /// The window the compose window's questions go on.
    var window: (@MainActor () -> NSWindow?)?

    init(state: AppState) {
        self.state = state
    }

    func call<M: RPCMethod>(_ method: M.Type, _ params: M.Params, timeout: Duration) async throws -> M.Result {
        try await state.client.call(method, params, timeout: timeout)
    }

    func post(_ f: @escaping @MainActor () -> Void) {
        Task { @MainActor in f() }
    }

    func attribution(mode: Conversation.QuickReplyMode, id: MessageID) -> String {
        delegate?.quickReplyAttribution(mode.composeKind, id) ?? ""
    }

    func beforeCreate(id: MessageID, done: @escaping @MainActor () -> Void) {
        guard let delegate else {
            done()
            return
        }
        delegate.prepareQuickReply(id, done: done)
    }

    func openCompose(
        mode: Conversation.QuickReplyMode, id: MessageID, ownHTML: String, opened: @escaping @MainActor () -> Void
    ) {
        delegate?.openCompose(mode.composeKind, id, ownHTML: ownHTML, from: window?(), opened: opened)
    }

    /// The compose window's confirmation, short, on the main window
    /// (ComposeManager's `onSent`).
    func sent(comment: Bool) {
        toasts.show(comment ? Jira.commentQueued() : L10n.T("Message queued for sending"), seconds: 2)
    }

    /// The compose window's toast for a send that did not go through; the
    /// text stays in the bar. Logged as Go logs it (quick_reply.go
    /// `Failed`); the error may name a recipient, so it is private.
    func failed(_ error: any Error) {
        log.warning("quick reply: \(String(describing: error), privacy: .private)")
        toasts.show(rpcErrorText(L10n.T("Sending"), error))
    }

    /// The bar's window's toasts (the main window's), else wherever the
    /// application shows them.
    private var toasts: any Toasts {
        if let main = (window?()?.windowController ?? state.mainWindow) as? MainWindowController {
            return main.toasts
        }
        return state.toasts
    }
}
