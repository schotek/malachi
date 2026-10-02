// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// What the user can do with a case, in one place for the three ways to
/// ask: the List's toolbar (through the window's validated actions), the
/// panel's action bar and the detail's buttons, and the context menus of
/// rows and cards. Done, Remind, Archive, Unstar and Discard go to the
/// board controller; Reply and Show in Mail need the mail, so the window
/// installs them (`onReply`, `onShowInMail`, set from the application's
/// integration). Reply on a case with a suggested reply edits it inline
/// instead (`replyHost`, the page's `BoardReplyEditorHost`). The invented
/// sample cases (`samples`) have no message behind them: those show
/// `Board.Text.later` there, as the placeholders did. Swift-first, like
/// `Board`.
@MainActor
final class BoardActions {
    let controller: BoardController
    /// The board runs on the invented samples (`MALACHI_BOARD_SAMPLES`).
    let samples: Bool

    /// Reply to `target` in account `account` (a comment on an issue
    /// tracker's account).
    var onReply: (@MainActor (AccountID, Board.ReplyTarget) -> Void)?
    /// Switch to Mail and show the case's conversation.
    var onShowInMail: (@MainActor (Board.Case) -> Void)?
    /// Asks before Discard deletes the draft; nil asks nothing.
    var confirmDiscard: (@MainActor () async -> Bool)?
    /// A short message over the page.
    var onToast: (@MainActor (String) -> Void)?
    /// The board's Suggest Reply (the application's one); nil offers none.
    var suggestion: BoardReplyController?
    /// What the inline reply editor needs of the application (set by the
    /// window, also for the samples, whose development hook uses it).
    var inlineReply: BoardReplyEditorHost.Environment?
    /// The page's inline reply editor (the page owns it).
    weak var replyHost: BoardReplyEditorHost?

    init(controller: BoardController, samples: Bool) {
        self.controller = controller
        self.samples = samples
    }

    /// The case as the source has it.
    func boardCase(_ id: Board.CaseID) -> Board.Case? {
        controller.source.snapshot.cases.first { $0.id == id }
    }

    // MARK: What can be done

    /// Reply answers in a compose window, or edits the case's suggested
    /// reply inline.
    func canReply(_ id: Board.CaseID) -> Bool {
        guard let c = boardCase(id) else { return false }
        return samples || (c.reply != nil && onReply != nil) || replyHost?.editsInline(id) == true
    }

    /// Unstar is offered (`Board.canUnstar`: on the board because of a star,
    /// not done).
    func canUnstar(_ id: Board.CaseID) -> Bool {
        guard let c = boardCase(id) else { return false }
        return Board.canUnstar(c)
    }

    /// Archive moves the inbox messages where the account can and marks the
    /// case done; without anything to move it only marks it done (the
    /// toast says so, `Board.Text.archived`), which a done case is already.
    func canArchive(_ id: Board.CaseID) -> Bool {
        guard let c = boardCase(id) else { return false }
        return c.canArchive || !c.visibility.isDone
    }

    func canShowInMail(_ id: Board.CaseID) -> Bool {
        guard let c = boardCase(id) else { return false }
        return samples || ((c.reply != nil || c.latestMessage != nil) && onShowInMail != nil)
    }

    func isSnoozed(_ id: Board.CaseID) -> Bool {
        boardCase(id)?.visibility.remindAt != nil
    }

    // MARK: Doing it

    /// Done, or Move Back to Board for a done case.
    func toggleDone(_ id: Board.CaseID) {
        guard let c = boardCase(id) else { return }
        if c.visibility.isDone {
            controller.reopen(id)
        } else {
            controller.markDone(id)
        }
    }

    func remind(_ id: Board.CaseID, until: Date?) {
        controller.remind(id, until: until)
    }

    func archive(_ id: Board.CaseID) {
        guard canArchive(id) else { return }
        controller.archive(id)
    }

    /// With a suggested reply its inline editor takes the keyboard (the
    /// case is selected for it); otherwise a compose window answers.
    func reply(_ id: Board.CaseID) {
        guard let c = boardCase(id) else { return }
        if let replyHost, replyHost.editsInline(id) {
            replyHost.focusReply(id)
            return
        }
        guard let target = c.reply, let onReply else {
            if samples {
                onToast?(Board.Text.later)
            }
            return
        }
        onReply(c.account, target)
    }

    /// Removes the star that keeps the case hot (`board.unflag`).
    func unstar(_ id: Board.CaseID) {
        guard canUnstar(id) else { return }
        controller.unflag(id)
    }

    /// Deletes the samples' suggested reply, after the question the compose
    /// window asks before discarding a message (the daemon's board discards
    /// from the inline editor's own Discard).
    func discardDraft(_ id: Board.CaseID) {
        guard let confirmDiscard else {
            controller.discardDraft(id)
            return
        }
        Task { @MainActor [weak self] in
            guard await confirmDiscard(), let self else { return }
            self.controller.discardDraft(id)
        }
    }

    func showInMail(_ id: Board.CaseID) {
        guard let c = boardCase(id) else { return }
        guard !samples, let onShowInMail, c.reply != nil || c.latestMessage != nil else {
            if samples {
                onToast?(Board.Text.later)
            }
            return
        }
        onShowInMail(c)
    }

    // MARK: Suggest Reply

    /// The detail's Suggest Reply control for case `id` (Core's view).
    func suggestReplyView(_ id: Board.CaseID) -> Board.SuggestReplyView {
        guard let suggestion, let c = boardCase(id) else { return .hidden }
        return suggestion.view(for: c, in: controller.source.snapshot, samples: samples)
    }

    /// ✦ Suggest Reply for case `id` with the user's `instruction`.
    func suggestReply(_ id: Board.CaseID, instruction: String) {
        guard let suggestion, let c = boardCase(id) else { return }
        suggestion.start(c, instruction: instruction)
    }

    /// Stop of the suggested reply under way.
    func stopSuggestedReply() {
        suggestion?.cancel()
    }

    // MARK: Menus

    /// Remind…'s menu: the presets with their time under the title, and
    /// for a snoozed case the item that ends the remind.
    func remindMenu(for id: Board.CaseID) -> NSMenu {
        let menu = NSMenu()
        for preset in controller.remindPresets() {
            let item = BoardMenuAction.item(preset.title) { [weak self] in
                self?.remind(id, until: preset.date)
            }
            if #available(macOS 14.4, *) {
                item.subtitle = preset.when
            } else {
                item.toolTip = preset.when
            }
            menu.addItem(item)
        }
        if isSnoozed(id) {
            menu.addItem(.separator())
            menu.addItem(BoardMenuAction.item(Board.Text.remindNoMore) { [weak self] in
                self?.remind(id, until: nil)
            })
        }
        return menu
    }

    /// Pops `menu` up under `view`, as the state pill's menu does.
    static func popUp(_ menu: NSMenu, under view: NSView) {
        menu.popUp(positioning: nil, at: NSPoint(x: 0, y: view.isFlipped ? view.bounds.maxY + 4 : -4), in: view)
    }
}
