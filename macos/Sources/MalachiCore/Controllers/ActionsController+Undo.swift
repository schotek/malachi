// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// Undo of moves (`Undo`; ui/internal/window/undo.go): Move to Trash,
// Archive, Junk, Move To and the board's Archive and Delete push what they
// moved once the daemon took it, with a toast whose button takes that move
// back; ⌘Z (Edit ▸ Undo, MainWindow) takes back the newest move left.
// Taking back is message.move of each message to the folder it came from (a
// moved message keeps its id); the board's moves also put the case back
// (`undoBoard`, `BoardController.undoArchive`).

extension ActionsController {
    /// The shortcut shown on the toast's button; a key name, never
    /// translated.
    public static let undoShortcut = "⌘Z"

    /// Whether ⌘Z has a move to take back.
    public var canUndo: Bool { !undoStack.isEmpty }

    /// Pushes the move of `list` (summaries taken before their rows went)
    /// to `target` with `kind`, once the daemon took it, and shows its
    /// toast (undo.go `recordUndo`). Nothing moved, nothing recorded.
    func recordUndo(_ kind: Undo.Kind, target: FolderID, _ list: [MessageSummary]) {
        guard var e = Undo.record(kind, target: target, list) else { return }
        if kind == .move, let f = mailbox.model.folder(FolderKey(account: e.account, folder: target)) {
            e.targetName = f.name
        }
        e = undoStack.push(e)
        showUndo(e, Undo.done(e))
    }

    /// Pushes the board's Archive or Delete (`o`) and shows its toast with
    /// Undo; a board move without moved messages gets the plain toast (only
    /// the case was marked done; undo.go `recordBoardUndo`).
    public func recordBoardUndo(_ o: Board.ArchiveOutcome) {
        guard o.undoLabel != nil,
              var e = Undo.fromBoard(trash: o.trash, account: o.account, id: o.caseID.rawValue, moved: o.moved)
        else {
            toast(o.text)
            return
        }
        if let f = mailbox.model.folderByRole(o.account, o.trash ? .trash : .archive) {
            e.target = f.id
        }
        e = undoStack.push(e)
        showUndo(e, o.text)
    }

    /// Shows `text` with the button that takes `e` back, in place of the
    /// toast of an earlier move (undo.go `showUndoToast`); without the hook
    /// nothing shows, ⌘Z still takes the move back.
    private func showUndo(_ e: Undo.Entry, _ text: String) {
        guard let showUndoToast else { return }
        let id = e.id
        showUndoToast(text, Undo.button(Self.undoShortcut)) { [weak self] in
            guard let self, let e = self.undoStack.take(id) else { return }
            self.runUndo(e)
        }
    }

    /// ⌘Z: the newest move is taken back (undo.go `undoLast`).
    public func undoLast() {
        guard let e = undoStack.pop() else {
            toast(Undo.nothing)
            return
        }
        runUndo(e)
    }

    /// Takes `e` back (undo.go `runUndo`): the board's moves through the
    /// board (which puts the case back and says itself when that failed),
    /// the mail's by message.move per source folder; then the account's
    /// counts and the list are read again and the messages that came back
    /// to the folder on display are selected.
    func runUndo(_ e: Undo.Entry) {
        dismissUndoToast?()
        guard let a = mailbox.model.account(e.account), a.enabled else {
            toast(Undo.failed)
            return
        }
        if e.kind.board {
            guard let undoBoard else {
                toast(Undo.failed)
                return
            }
            undoBoard(Board.ArchiveOutcome(
                caseID: Board.CaseID(rawValue: e.caseID), account: e.account,
                moved: e.moves.map { BoardMoved(messageId: $0.messageId, fromFolderId: $0.fromFolderId) },
                text: "", undoLabel: nil, trash: e.kind == .boardTrash))
            afterUndo(e)
            return
        }
        let calls = Undo.calls(e)
        guard !calls.isEmpty else { return }
        final class Tally {
            var left: Int
            var failed = false
            init(_ n: Int) { left = n }
        }
        let tally = Tally(calls.count)
        let finish: @MainActor () -> Void = { [weak self] in
            tally.left -= 1
            guard tally.left == 0, let self else { return }
            if !tally.failed {
                self.toast(Undo.undone(e))
            }
            self.afterUndo(e)
        }
        for c in calls {
            call(API.MessageMove.self, c, what: ContextMenu.movingText(c.messageIds.count), onError: { _ in
                tally.failed = true
                finish()
            }, onOK: { _ in
                finish()
            })
        }
    }

    /// Reads the account's folders again for their counts and lists the
    /// folder on display again when it is one the messages left or came
    /// back to, selecting those that came back to it (undo.go `afterUndo`,
    /// `ListController.relistAfterUndo`).
    private func afterUndo(_ e: Undo.Entry) {
        mailbox.loadFolders(e.account, mailbox.model.foldersGen)
        guard let sel = mailbox.model.selected, sel.account == e.account, !mailbox.model.search.active else { return }
        let back = Undo.reselect(e, account: sel.account, folder: sel.folder)
        if back.isEmpty, sel.folder != e.target {
            return
        }
        list.relistAfterUndo(back)
    }
}
