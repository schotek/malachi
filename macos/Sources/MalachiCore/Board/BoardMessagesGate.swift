// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// The board's source as the window's board controller sees it: every call
/// goes to the real source (the daemon's, or the samples), except that the
/// selected case's conversation (`loadMessages`, `board.get`) is asked for
/// only while the board shows. In Mail the controller still follows the
/// source (the automatic triage, Settings → AI list it), but nothing
/// fetches a conversation nobody looks at; the last case asked for while
/// closed is asked for once the board shows again (`isOpen`). The GTK
/// window's `boardMessagesGate` does the same. The window opens it only
/// after `BoardController.boardWillShow`, so the case the board shows
/// first is the one asked for, once.
@MainActor
public final class BoardMessagesGate: BoardSource, BoardArchiveUndoer {
    public let inner: any BoardSource
    /// The case whose conversation was asked for while closed.
    private var held: Board.CaseID?

    /// Whether the board shows; opening asks for the held conversation.
    public var isOpen = false {
        didSet {
            guard isOpen, !oldValue, let id = held else { return }
            held = nil
            inner.loadMessages(of: id)
        }
    }

    public init(_ inner: any BoardSource) {
        self.inner = inner
    }

    public var snapshot: Board.Snapshot { inner.snapshot }

    public var onChange: (@MainActor () -> Void)? {
        get { inner.onChange }
        set { inner.onChange = newValue }
    }

    public var onError: (@MainActor (String) -> Void)? {
        get { inner.onError }
        set { inner.onError = newValue }
    }

    public var onNotice: (@MainActor (String) -> Void)? {
        get { inner.onNotice }
        set { inner.onNotice = newValue }
    }

    public var onArchived: (@MainActor (Board.ArchiveOutcome) -> Void)? {
        get { inner.onArchived }
        set { inner.onArchived = newValue }
    }

    public func setState(_ state: Board.State?, of id: Board.CaseID) {
        inner.setState(state, of: id)
    }

    public func setDone(_ done: Bool, of id: Board.CaseID) {
        inner.setDone(done, of: id)
    }

    public func remind(until: Date?, of id: Board.CaseID) {
        inner.remind(until: until, of: id)
    }

    public func archive(_ id: Board.CaseID) {
        inner.archive(id)
    }

    public func setCommitmentDone(_ done: Bool, of id: String) {
        inner.setCommitmentDone(done, of: id)
    }

    public func discardDraft(of id: Board.CaseID) {
        inner.discardDraft(of: id)
    }

    public func discardDraft(_ draft: DraftID, account: AccountID, of id: Board.CaseID) async throws {
        try await inner.discardDraft(draft, account: account, of: id)
    }

    public func unflag(_ id: Board.CaseID) {
        inner.unflag(id)
    }

    public func loadMessages(of id: Board.CaseID) {
        guard isOpen else {
            held = id
            return
        }
        held = nil
        inner.loadMessages(of: id)
    }

    public func refresh() {
        inner.refresh()
    }

    /// The source's own undo when it has one, else the case back on the
    /// board (as `BoardController.undoArchive` does without one).
    public func undoArchive(_ o: Board.ArchiveOutcome) {
        if let u = inner as? any BoardArchiveUndoer {
            u.undoArchive(o)
        } else {
            inner.setDone(false, of: o.caseID)
        }
    }
}
