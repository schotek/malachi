// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// ui/internal/selection selection_test.go, ui/internal/assistant
/// selection_test.go and contextmenu_test.go `TestMessageItemsSeveral`,
/// ported.
struct SelectionTests {
    /// A selected mail message everything applies to, read and not starred.
    private var mail: ActionFlags {
        ActionFlags(on: true, reply: true, replyAll: true, forward: true, star: true, trash: true, archive: true,
                    junk: true, move: true, markUnread: true, toggleFlag: true, loadImages: true, trustSender: true)
    }

    @Test func combineOneRowIsTheRow() {
        #expect(Selection.combine([]) == ActionFlags())
        var one = mail
        one.comment = true
        var want = one
        want.count = 1
        want.open = true
        #expect(Selection.combine([one]) == want)
    }

    @Test func combineSeveral() {
        var unread = mail
        unread.markUnread = false
        unread.markRead = true
        var starred = mail
        starred.flagged = true
        var inArchive = mail
        inArchive.archive = false
        let got = Selection.combine([mail, unread, starred, inArchive])
        let want = ActionFlags(count: 4, open: true, on: true, forward: true, star: true, trash: true, archive: true,
                               junk: true, move: true, markRead: true, markUnread: true, toggleFlag: true)
        #expect(got == want)
        #expect(Selection.flagTarget(got), "not every row is starred: the star stars them")
        let all = Selection.combine([starred, starred])
        #expect(all.flagged && !Selection.flagTarget(all))
        // Archive only when some row can go; Trash and Move only when all can.
        var noTrash = mail
        noTrash.trash = false
        noTrash.move = false
        noTrash.archive = false
        let c = Selection.combine([inArchive, noTrash])
        #expect(!c.archive && !c.trash && !c.move && c.junk)
    }

    @Test func combineCaps() {
        let overOpen = Selection.combine(Array(repeating: mail, count: Selection.maxOpen + 1))
        #expect(!overOpen.open && overOpen.forward)
        let overForward = Selection.combine(Array(repeating: mail, count: Selection.maxForward + 1))
        #expect(!overForward.open && !overForward.forward)
        let two = Selection.combine([mail, mail])
        #expect(two.open && two.forward)
    }

    @Test func combineOutbox() {
        let queued = ActionFlags(on: true, outbox: true, reply: true, forward: true, trash: true, loadImages: true)
        #expect(Selection.combine([queued, queued]) == ActionFlags(count: 2, open: true, on: true, outbox: true, trash: true))
    }

    /// What the account offers and Comment are the first row's.
    @Test func combineKeepsTheAccountsOffer() {
        var issue = mail
        issue.comment = true
        issue.unsupported = [.archive, .move]
        let c = Selection.combine([issue, issue])
        #expect(c.comment && c.unsupported == [.archive, .move])
    }

    @Test func restrict() {
        let rows: [(key: String, account: AccountID)] = [("a1", "a"), ("b1", "b"), ("a2", "a"), ("b2", "b")]
        #expect(Selection.restrict(rows, latest: "b2") == ["b1", "b2"])
        #expect(Selection.restrict(rows, latest: "a1") == ["a1", "a2"])
        #expect(Selection.restrict(rows, latest: "zz").count == 4)
        #expect(Selection.restrict([(key: String, account: AccountID)](), latest: "x").isEmpty)
    }

    @Test func reconcile() {
        // m2 and m3 are members of conversation t, folded away; m9 is gone.
        let resolve: (String) -> String? = { k in
            switch k {
            case "m2", "m3": "t"
            case "m9": nil
            default: k
            }
        }
        #expect(Selection.reconcile(["m1", "m2", "m9", "m3", "m4"], resolve: resolve, neighbour: { "n" }) == ["m1", "t", "m4"])
        #expect(Selection.reconcile(["m9"], resolve: resolve, neighbour: { "n" }) == ["n"])
        #expect(Selection.reconcile(["m9"], resolve: resolve).isEmpty)
        #expect(Selection.reconcile(["m9"], resolve: resolve, neighbour: { nil }).isEmpty)
        #expect(Selection.reconcile([], resolve: resolve, neighbour: { "n" }).isEmpty)
    }

    @Test func texts() {
        #expect(Selection.selectedText(1) == "1 message selected")
        #expect(Selection.selectedText(5) == "5 messages selected")
        #expect(Selection.cancelSendingQuestion(3) == "Cancel sending 3 messages?")
    }

    @Test func assistantActionsFor() {
        #expect(Assistant.messageActionsFor(0) == Assistant.messageActions)
        #expect(Assistant.messageActionsFor(1) == Assistant.messageActions)
        #expect(Assistant.messageActionsFor(3) == [.summarize, .tasks, .ask])
        #expect(Assistant.labelFor(.ask, count: 2) == "Ask About These Messages…")
        #expect(Assistant.labelFor(.ask, count: 1) == Assistant.label(.ask))
        #expect(Assistant.labelFor(.summarize, count: 4) == Assistant.label(.summarize))
    }

    /// contextmenu_test.go `TestMessageItemsSeveral`.
    @Test func contextMenuOfSeveral() {
        func show(_ gs: [[ContextMenu.Item]]) -> String {
            gs.map { g in g.map { $0.id.rawValue + ($0.enabled ? "" : "-") }.joined(separator: " ") }
                .joined(separator: " | ")
        }
        var s = ContextMenu.MessageState()
        s.offerReply = true; s.offerReplyAll = true; s.offerForward = true
        s.offerArchive = true; s.offerMove = true; s.offerJunk = true; s.offerTrash = true
        s.forward = true; s.markUnread = true; s.toggleFlag = true
        s.archive = true; s.move = true; s.junk = true; s.trash = true; s.assistant = true
        s.count = 3; s.open = true; s.draft = true; s.changeStatus = true
        #expect(show(ContextMenu.messageItems(s)) == "openMessage | forward | markUnread toggleFlag | archive moveTo junk trash | assistant")
        s.open = false
        #expect(show(ContextMenu.messageItems(s)) == "openMessage- | forward | markUnread toggleFlag | archive moveTo junk trash | assistant")
        #expect(ContextMenu.label(.openMessage, .init(count: 3)) == "Open 3 Messages in New Windows")
        #expect(ContextMenu.label(.openMessage, .init(count: 1)) == "Open in New Window")
    }

    /// Save to eWay-CRM for several: every row savable, at most 20, never
    /// the saved-state items; not in the outbox (selection_test.go
    /// `TestCombineCrm`).
    @Test func combineCrm() {
        var savable = mail
        savable.crm = true
        savable.crmSaved = true
        let one = Selection.combine([savable])
        #expect(one.crm && one.crmSaved)
        let two = Selection.combine([savable, savable])
        #expect(two.crm && !two.crmSaved)
        #expect(!Selection.combine([savable, mail]).crm, "one row not savable")
        let rows = Array(repeating: savable, count: API.Limits.maxCrmMessages + 1)
        #expect(!Selection.combine(rows).crm)
        #expect(Selection.combine(Array(rows.prefix(API.Limits.maxCrmMessages))).crm)
        var queued = savable
        queued.outbox = true
        #expect(!Selection.combine([queued, queued]).crm, "outbox")
    }
}
