// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// Port of ui/internal/undo/undo_test.go.

private func summary(_ id: String, _ account: String, _ folder: String, thread: String? = nil) -> MessageSummary {
    MessageSummary(
        id: MessageID(rawValue: id), accountId: AccountID(rawValue: account), folderId: FolderID(rawValue: folder),
        threadId: thread.map { ThreadID(rawValue: $0) }, from: [], subject: "", date: Date(timeIntervalSince1970: 0), snippet: "", flags: [],
        hasAttachments: false, size: 0)
}

private func moved(_ id: String, _ folder: String, _ thread: String? = nil) -> Undo.Moved {
    Undo.Moved(
        messageId: MessageID(rawValue: id), fromFolderId: FolderID(rawValue: folder),
        threadId: thread.map { ThreadID(rawValue: $0) })
}

@Suite struct UndoTests {
    @Test func record() throws {
        let list = [
            summary("m1", "a", "inbox", thread: "t1"),
            summary("m2", "a", "trash"), // already there
            summary("m3", "b", "inbox"), // another account
            summary("m4", "a", "work"),
            summary("m1", "a", "inbox"), // twice
            summary("", "a", "inbox"),
            summary("m5", "a", ""),
        ]
        let e = try #require(Undo.record(.trash, target: "trash", list))
        #expect(e.account == "a" && e.target == "trash" && e.kind == .trash)
        #expect(e.moves == [moved("m1", "inbox", "t1"), moved("m4", "work")])
        #expect(Undo.record(.trash, target: "trash", [summary("m2", "a", "trash")]) == nil, "a message already in the target")
        #expect(Undo.record(.archive, target: "arch", []) == nil, "an empty move")
    }

    @Test func fromBoard() throws {
        let e = try #require(Undo.fromBoard(trash: true, account: "a", id: "c_1", moved: [
            BoardMoved(messageId: "m1", fromFolderId: "inbox"), BoardMoved(messageId: "", fromFolderId: "inbox"),
        ]))
        #expect(e.kind == .boardTrash && e.caseID == "c_1" && e.moves.count == 1 && e.kind.board)
        #expect(Undo.fromBoard(trash: false, account: "a", id: "c_1", moved: []) == nil, "a board move without messages")
        #expect(!Undo.Kind.trash.board && !Undo.Kind.move.board, "mail kinds are not the board's")
    }

    @Test func callsGroupByFolder() {
        let e = Undo.Entry(kind: .trash, account: "a", moves: [
            moved("m1", "inbox"), moved("m2", "work"), moved("m3", "inbox"), moved("m1", "inbox"), moved("", "inbox"),
            moved("m4", ""),
        ])
        #expect(Undo.calls(e) == [
            MessageMoveParams(accountId: "a", messageIds: ["m1", "m3"], targetFolderId: "inbox"),
            MessageMoveParams(accountId: "a", messageIds: ["m2"], targetFolderId: "work"),
        ])
        #expect(Undo.calls(Undo.Entry(kind: .trash, account: "")).isEmpty, "calls of an empty entry")
    }

    @Test func reselect() {
        let e = Undo.Entry(kind: .trash, account: "a", moves: [
            moved("m1", "inbox", "t1"), moved("m2", "work"), moved("m3", "inbox"), moved("m1", "inbox", "t1"),
        ])
        #expect(Undo.reselect(e, account: "a", folder: "inbox") == [moved("m1", "inbox", "t1"), moved("m3", "inbox")])
        #expect(Undo.reselect(e, account: "b", folder: "inbox").isEmpty, "another account")
        #expect(Undo.reselect(e, account: "a", folder: "trash").isEmpty, "another folder")
        #expect(Undo.reselect(e, account: "a", folder: "").isEmpty, "no folder")
        #expect(Undo.reselect(e, account: "a", folder: nil).isEmpty, "no folder")
    }

    @Test func stack() throws {
        var s = Undo.Stack()
        let none = s.pop()
        #expect(none == nil, "pop of an empty stack")
        let a = s.push(Undo.Entry(kind: .trash, account: "a", target: "trash"))
        let b = s.push(Undo.Entry(kind: .archive, account: "a", target: "arch"))
        let c = s.push(Undo.Entry(kind: .trash, account: "b", target: "trash"))
        #expect(a.id != b.id && b.id != c.id && s.count == 3)
        #expect(s.peek()?.id == c.id && s.count == 3, "peek")
        let fromBottom = s.take(a.id)
        let taken = try #require(fromBottom, "take from the bottom")
        #expect(taken.id == a.id && s.count == 2)
        let twice = s.take(a.id)
        #expect(twice == nil, "taken twice")
        let newest = s.pop()
        #expect(newest?.id == c.id, "pop is not the newest")
        let next = s.pop()
        #expect(next?.id == b.id, "pop order")
        #expect(s.isEmpty)
    }

    @Test func stackLimit() {
        var s = Undo.Stack()
        let first = s.push(Undo.Entry(kind: .trash, account: ""))
        for _ in 0..<Undo.limit {
            s.push(Undo.Entry(kind: .trash, account: ""))
        }
        #expect(s.count == Undo.limit)
        let oldest = s.take(first.id)
        #expect(oldest == nil, "the oldest move stayed past the limit")
    }

    @Test func forget() {
        var s = Undo.Stack()
        s.push(Undo.Entry(kind: .trash, account: "a", target: "trash"))
        s.push(Undo.Entry(kind: .archive, account: "a", target: "arch"))
        s.push(Undo.Entry(kind: .trash, account: "b", target: "trash"))
        s.forgetFolder("a", "trash")
        #expect(s.count == 2, "forget folder")
        s.forget("b")
        let e = s.pop()
        #expect(s.count == 0 && e?.target == "arch", "forget account")
    }

    @Test func texts() {
        let one = [moved("m1", "inbox")]
        let two = [moved("m1", "inbox"), moved("m2", "inbox")]
        let cases: [(Undo.Kind, String, [Undo.Moved], String)] = [
            (.trash, "", one, "Moved 1 message to Trash."),
            (.boardTrash, "", two, "Moved 2 messages to Trash."),
            (.archive, "", one, "Archived 1 message."),
            (.boardArchive, "", two, "Archived 2 messages."),
            (.junk, "", two, "Moved 2 messages to Junk."),
            (.move, "Work", one, "Moved 1 message to “Work”."),
            (.move, "", two, "Moved 2 messages."),
        ]
        for (kind, name, moves, want) in cases {
            let e = Undo.Entry(kind: kind, account: "a", targetName: name, moves: moves)
            #expect(Undo.done(e) == want, "\(kind)")
        }
        #expect(Undo.button("") == "Undo")
        #expect(Undo.button("⌘Z") == "Undo (⌘Z)")
        #expect(Undo.undone(Undo.Entry(kind: .trash, account: "a", moves: two)) == "Moved 2 messages back.")
        #expect(Undo.undone(Undo.Entry(kind: .trash, account: "a", moves: one)) == "Moved 1 message back.")
        #expect(!Undo.failed.isEmpty && !Undo.nothing.isEmpty)
    }
}
