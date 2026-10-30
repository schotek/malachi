// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// What Undo (⌘Z, and the button of the toast after a move) takes back: the
// moves of mail the user made in this session — Move to Trash, Archive,
// Junk, Move To, and the board's Archive and Delete — newest first. The
// daemon has no undo of its own: a moved message keeps its id (docs/api.md,
// message.move), so taking a move back is message.move of each message to
// the folder it came from (`Undo.calls`).
//
// What cannot be taken back never gets an entry: a message expunged from
// Trash, a cancelled send of the outbox, a move to a folder the daemon does
// not keep (Gmail's All Mail drops the row). The stack lives in memory only
// and ends with the application.
//
// Port of ui/internal/undo/undo.go (the reference), with its tests. A
// namespace of its own, `Undo`, not to be mistaken for Foundation's
// `UndoManager`, which the window's text keeps for typing.

import Foundation

/// The moves of mail Undo takes back (Go package undo).
public enum Undo {
    /// How many moves the stack keeps; a push past it forgets the oldest.
    public static let limit = 20

    /// What the move was, for its texts.
    public enum Kind: Sendable, Equatable {
        /// Move to Trash (message.delete of messages not in Trash).
        case trash
        /// Archive (message.move to the archive role).
        case archive
        /// Junk (message.move to the junk role).
        case junk
        /// Move To (message.move to a folder the user picked).
        case move
        /// The board's Archive (board.archive moved).
        case boardArchive
        /// The board's Delete (board.trash moved).
        case boardTrash

        /// Whether the move was the board's: taking it back also puts the
        /// case back on the board (board.setDone done false).
        public var board: Bool { self == .boardArchive || self == .boardTrash }
    }

    /// One message moved and the folder it came from, with its
    /// conversation (nil when not known: the board's moves, an older
    /// daemon) for a list grouped by conversation to select again.
    public struct Moved: Sendable, Equatable {
        public var messageId: MessageID
        public var fromFolderId: FolderID
        public var threadId: ThreadID?

        public init(messageId: MessageID, fromFolderId: FolderID, threadId: ThreadID? = nil) {
            self.messageId = messageId
            self.fromFolderId = fromFolderId
            self.threadId = threadId
        }
    }

    /// One move the user made, all of one account.
    public struct Entry: Sendable, Equatable {
        /// The entry's number in the stack, given by `Stack.push`; the
        /// toast's button takes back its own entry by it (`Stack.take`).
        public var id: Int = 0
        public var kind: Kind
        public var account: AccountID
        /// The folder the messages went to; nil for the board's moves when
        /// the daemon did not say.
        public var target: FolderID?
        /// The folder's name for the text of Move To.
        public var targetName: String = ""
        /// The messages and where they came from, in their order.
        public var moves: [Moved]
        /// The board's case of a board move (`Kind.board`).
        public var caseID: String = ""

        public init(
            id: Int = 0, kind: Kind, account: AccountID, target: FolderID? = nil, targetName: String = "",
            moves: [Moved] = [], caseID: String = ""
        ) {
            self.id = id
            self.kind = kind
            self.account = account
            self.target = target
            self.targetName = targetName
            self.moves = moves
            self.caseID = caseID
        }

        /// How many messages the entry moved.
        public var count: Int { moves.count }
    }

    /// The entry of moving `list` (summaries taken before their rows went)
    /// to `target` with `kind`: the account is the first message's;
    /// messages of another account, without a folder or already in
    /// `target` are left out (they did not move). Nil when nothing moved.
    public static func record(_ kind: Kind, target: FolderID, _ list: [MessageSummary]) -> Entry? {
        guard let first = list.first else { return nil }
        var e = Entry(kind: kind, account: first.accountId, target: target)
        for s in list {
            if s.accountId != e.account || s.id.rawValue.isEmpty || s.folderId.rawValue.isEmpty || s.folderId == target {
                continue
            }
            if e.moves.contains(where: { $0.messageId == s.id }) {
                continue
            }
            e.moves.append(Moved(messageId: s.id, fromFolderId: s.folderId, threadId: s.threadId))
        }
        return e.moves.isEmpty ? nil : e
    }

    /// The entry of the board's Archive (`trash` false) or Delete of case
    /// `id` in `account`, from what the daemon moved (board.archive and
    /// board.trash `moved`). Nil when it moved nothing: the board then
    /// offers no Undo either.
    public static func fromBoard(trash: Bool, account: AccountID, id: String, moved: [BoardMoved]) -> Entry? {
        var e = Entry(kind: trash ? .boardTrash : .boardArchive, account: account, caseID: id)
        for m in moved where !m.messageId.rawValue.isEmpty && !m.fromFolderId.rawValue.isEmpty {
            e.moves.append(Moved(messageId: m.messageId, fromFolderId: m.fromFolderId))
        }
        return e.moves.isEmpty ? nil : e
    }

    /// The message.move calls that take `e` back: one per folder the
    /// messages came from, in the order the folders first appear, the
    /// messages in their order, each once.
    public static func calls(_ e: Entry) -> [MessageMoveParams] {
        var moves: [MessageMoveParams] = []
        var index: [FolderID: Int] = [:]
        for m in e.moves where !m.messageId.rawValue.isEmpty && !m.fromFolderId.rawValue.isEmpty {
            let i: Int
            if let known = index[m.fromFolderId] {
                i = known
            } else {
                i = moves.count
                index[m.fromFolderId] = i
                moves.append(MessageMoveParams(accountId: e.account, messageIds: [], targetFolderId: m.fromFolderId))
            }
            if !moves[i].messageIds.contains(m.messageId) {
                moves[i].messageIds.append(m.messageId)
            }
        }
        return moves
    }

    /// The moves of `e` that came back to `folder` of `account`, the
    /// folder the window shows: after Undo the window selects their rows
    /// again (a message's, or its conversation's). None when the window
    /// shows another folder.
    public static func reselect(_ e: Entry, account: AccountID, folder: FolderID?) -> [Moved] {
        guard account == e.account, let folder, !folder.rawValue.isEmpty else { return [] }
        var back: [Moved] = []
        for m in e.moves where m.fromFolderId == folder && !back.contains(where: { $0.messageId == m.messageId }) {
            back.append(m)
        }
        return back
    }

    /// The moves of the session, newest last. A new stack is empty.
    public struct Stack: Sendable {
        private var entries: [Entry] = []
        private var next = 0

        public init() {}

        /// Adds `e` as the newest move and returns it with its id; past
        /// `limit` the oldest is forgotten.
        @discardableResult
        public mutating func push(_ e: Entry) -> Entry {
            next += 1
            var e = e
            e.id = next
            entries.append(e)
            let over = entries.count - Undo.limit
            if over > 0 {
                entries.removeFirst(over)
            }
            return e
        }

        /// Takes the newest move off the stack (⌘Z).
        public mutating func pop() -> Entry? {
            entries.popLast()
        }

        /// The newest move, left on the stack.
        public func peek() -> Entry? {
            entries.last
        }

        /// Takes the move with `id` off the stack wherever it is (the
        /// button of its toast); nil when it is gone (taken back already,
        /// forgotten).
        public mutating func take(_ id: Int) -> Entry? {
            guard let i = entries.firstIndex(where: { $0.id == id }) else { return nil }
            return entries.remove(at: i)
        }

        /// How many moves the stack holds.
        public var count: Int { entries.count }

        /// Whether the stack holds no move.
        public var isEmpty: Bool { entries.isEmpty }

        /// Drops the moves of `account` (removed or paused: its messages
        /// are gone from the store).
        public mutating func forget(_ account: AccountID) {
            entries.removeAll { $0.account == account }
        }

        /// Drops the moves of `account` into `folder` (Empty Trash and
        /// Empty Junk deleted the messages for good).
        public mutating func forgetFolder(_ account: AccountID, _ folder: FolderID) {
            entries.removeAll { $0.account == account && $0.target == folder }
        }
    }
}
