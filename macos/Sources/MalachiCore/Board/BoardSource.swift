// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Where the board's cases come from. The board reads a snapshot and is
// told when it changes; what the user decides about a case (its state,
// done, a remind, archive, a discarded draft, a promise ticked off) is case
// data and goes back to the source. The daemon's source
// (`DaemonBoardSource`) starts empty and publishes when its data arrives;
// the in-memory one holds invented sample cases or none, for the dummy
// board and the tests.
//
// Swift-first: written here first; the GTK window ports it to
// ui/internal/board with the board itself.

import Foundation

/// The board's cases and the writes the user's decisions make. Writes are
/// fire-and-forget: the source calls `onChange` once its snapshot holds
/// them (at once for both sources: the daemon's writes optimistically and
/// undoes a write the daemon refused, with `onError`).
@MainActor
public protocol BoardSource: AnyObject {
    var snapshot: Board.Snapshot { get }
    /// Called after `snapshot` changed. One observer: the board controller.
    var onChange: (@MainActor () -> Void)? { get set }
    /// Called with a short sentence for a toast when a write or a load
    /// failed (a write is undone by then). One observer: the controller.
    var onError: (@MainActor (String) -> Void)? { get set }
    /// Called with a short sentence for a toast about what a write did
    /// (Archive). One observer: the controller.
    var onNotice: (@MainActor (String) -> Void)? { get set }
    /// Moves the case to `state`; nil = back to automatic (the assistant's
    /// or the rules' state).
    func setState(_ state: Board.State?, of id: Board.CaseID)
    /// Done takes the case off the board (and ends a remind); not done
    /// puts it back.
    func setDone(_ done: Bool, of id: Board.CaseID)
    /// Hides the case until `until` (in the future, within a year); nil
    /// puts a snoozed case back on the board. Ends done.
    func remind(until: Date?, of id: Board.CaseID)
    /// Moves the case's inbox messages to the archive (where the account
    /// can) and marks it done; `onNotice` says what it did.
    func archive(_ id: Board.CaseID)
    /// Ticks a promise off (or reopens it).
    func setCommitmentDone(_ done: Bool, of id: String)
    /// Drops the suggested reply (the draft itself, too).
    func discardDraft(of id: Board.CaseID)
    /// Discard of the inline reply editor: deletes draft `draft` of case
    /// `id` and answers when that is done. While the case links it, the
    /// link goes with it (`board.discardDraft`); a draft the case no longer
    /// links is deleted alone. Throws when the daemon refused (the editor
    /// says so and keeps its text); a source without drafts (the samples)
    /// just drops the link.
    func discardDraft(_ draft: DraftID, account: AccountID, of id: Board.CaseID) async throws
    /// Removes the star from the messages that keep the case hot
    /// (`Detail.canUnstar`); the rules then decide where the case goes.
    func unflag(_ id: Board.CaseID)
    /// Loads the case's conversation into `Case.messages` unless it is
    /// there for the case's current version already.
    func loadMessages(of id: Board.CaseID)
    /// Asks for the data anew.
    func refresh()
}

extension BoardSource {
    /// How far the data is (`snapshot.phase`).
    public var phase: Board.Phase { snapshot.phase }

    /// No drafts behind the cases: the link goes, nothing can fail.
    public func discardDraft(_ draft: DraftID, account: AccountID, of id: Board.CaseID) async throws {
        discardDraft(of: id)
    }
}

/// A source over a snapshot held in memory: the dummy board, and the tests.
/// Writes are emulated locally (Archive marks the case done and says it
/// moved its messages when the case can archive). A write to an unknown
/// case, or one that changes nothing, is ignored and calls no one.
@MainActor
public final class InMemoryBoardSource: BoardSource {
    public private(set) var snapshot: Board.Snapshot
    public var onChange: (@MainActor () -> Void)?
    public var onError: (@MainActor (String) -> Void)?
    public var onNotice: (@MainActor (String) -> Void)?

    public init(_ snapshot: Board.Snapshot) {
        self.snapshot = snapshot
    }

    /// The dummy board: the invented sample cases (`Board.sampleSnapshot`),
    /// or none.
    public static func dummy(samples: Bool, now: Date = Date(), calendar: Calendar = .current) -> InMemoryBoardSource {
        InMemoryBoardSource(samples ? Board.sampleSnapshot(now: now, calendar: calendar) : .empty)
    }

    /// Replaces the whole snapshot, as a source does when its data arrives.
    public func replace(_ snapshot: Board.Snapshot) {
        guard snapshot != self.snapshot else { return }
        self.snapshot = snapshot
        onChange?()
    }

    public func setState(_ state: Board.State?, of id: Board.CaseID) {
        update(id) { $0.userState = state }
    }

    public func setDone(_ done: Bool, of id: Board.CaseID) {
        update(id) { $0.done = done }
    }

    public func remind(until: Date?, of id: Board.CaseID) {
        update(id) { c in
            if let until {
                c.visibility = .snoozed(until: until)
            } else if c.visibility.remindAt != nil {
                c.visibility = .live
            }
        }
    }

    public func archive(_ id: Board.CaseID) {
        guard let c = snapshot.cases.first(where: { $0.id == id }) else { return }
        let moved = c.canArchive ? max(1, c.messageCount) : 0
        update(id) { c in
            c.visibility = .done(at: nil)
            c.canArchive = false
        }
        onNotice?(Board.Text.archived(moved, noArchive: !c.canArchive))
    }

    public func setCommitmentDone(_ done: Bool, of id: String) {
        guard let i = snapshot.commitments.firstIndex(where: { $0.id == id }) else { return }
        let state: Board.CommitmentState = done ? .done : .open
        guard snapshot.commitments[i].state != state else { return }
        snapshot.commitments[i].state = state
        onChange?()
    }

    public func discardDraft(of id: Board.CaseID) {
        update(id) { $0.draft = nil }
    }

    /// The samples have no message to unflag: a placeholder toast.
    public func unflag(_ id: Board.CaseID) {
        guard snapshot.cases.contains(where: { $0.id == id }) else { return }
        onNotice?(Board.Text.later)
    }

    /// The samples carry their messages; a case without any gets none.
    public func loadMessages(of id: Board.CaseID) {
        update(id) { c in
            if c.messages == nil {
                c.messages = []
            }
        }
    }

    public func refresh() {}

    private func update(_ id: Board.CaseID, _ change: (inout Board.Case) -> Void) {
        guard let i = snapshot.cases.firstIndex(where: { $0.id == id }) else { return }
        var c = snapshot.cases[i]
        change(&c)
        guard c != snapshot.cases[i] else { return }
        snapshot.cases[i] = c
        onChange?()
    }
}
