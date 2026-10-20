// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// ui/internal/contextmenu contextmenu_test.go, ported.
struct ContextMenuTests {
    /// Groups as "id id- | id", "-" after a disabled item (the Go test's
    /// ids "open", "edit" and "empty" are the case names here).
    private func show(_ gs: [[ContextMenu.Item]]) -> String {
        gs.map { g in g.map { $0.id.rawValue + ($0.enabled ? "" : "-") }.joined(separator: " ") }
            .joined(separator: " | ")
    }

    /// A mail message everything applies to.
    private var mail: ContextMenu.MessageState {
        var s = ContextMenu.MessageState()
        s.offerReply = true; s.offerReplyAll = true; s.offerForward = true
        s.offerArchive = true; s.offerMove = true; s.offerJunk = true; s.offerTrash = true
        s.reply = true; s.replyAll = true; s.forward = true; s.markUnread = true; s.toggleFlag = true
        s.archive = true; s.move = true; s.junk = true; s.trash = true; s.assistant = true
        return s
    }

    @Test func messageItems() {
        var unread = mail
        unread.markUnread = false; unread.markRead = true; unread.assistant = false
        var mixed = mail
        mixed.markRead = true
        var inOwnFolders = mail
        inOwnFolders.archive = false; inOwnFolders.junk = false
        var draft = mail
        draft.draft = true
        var outbox = ContextMenu.MessageState()
        outbox.outbox = true; outbox.offerReply = true; outbox.offerForward = true; outbox.offerTrash = true
        outbox.offerArchive = true; outbox.offerMove = true; outbox.offerJunk = true
        outbox.reply = true; outbox.forward = true; outbox.trash = true
        var issue = ContextMenu.MessageState()
        issue.offerReply = true; issue.offerForward = true; issue.reply = true; issue.forward = true
        issue.markRead = true; issue.toggleFlag = true; issue.changeStatus = true; issue.assistant = true
        var queuedComment = ContextMenu.MessageState()
        queuedComment.outbox = true; queuedComment.trash = true
        let cases: [(String, ContextMenu.MessageState, String)] = [
            ("read mail", mail, "openMessage | reply replyAll forward | markUnread toggleFlag | archive moveTo junk trash | assistant"),
            ("unread mail, no Assistant", unread, "openMessage | reply replyAll forward | markRead toggleFlag | archive moveTo junk trash"),
            ("conversation with read and unread members", mixed,
             "openMessage | reply replyAll forward | markRead markUnread toggleFlag | archive moveTo junk trash | assistant"),
            ("in the Archive and the Junk folder of its own", inOwnFolders,
             "openMessage | reply replyAll forward | markUnread toggleFlag | archive- moveTo junk- trash | assistant"),
            ("draft", draft, "editDraft | reply replyAll forward | markUnread toggleFlag | archive moveTo junk trash | assistant"),
            ("outbox", outbox, "openMessage | reply forward | toggleFlag- | archive- moveTo- junk- trash"),
            ("issue tracker", issue, "openMessage | reply forward | markRead toggleFlag changeStatus | assistant"),
            ("queued comment of an issue tracker", queuedComment, "openMessage | toggleFlag- | trash"),
        ]
        for (name, s, want) in cases {
            #expect(show(ContextMenu.messageItems(s)) == want, "\(name)")
        }
    }

    /// eWay-CRM (`CRM.offer(for:rows:)`): Save after the answers, the
    /// saved-state items for one saved message, Save with the count for
    /// several (saved or not), nothing when not offered
    /// (contextmenu_test.go `TestMessageItemsCrm`).
    @Test func messageItemsCrm() {
        var s = mail
        s.setCrm(CRM.Offer(shown: true, enabled: true, count: 1))
        #expect(show(ContextMenu.messageItems(s))
            == "openMessage | reply replyAll forward | crmSave | markUnread toggleFlag | archive moveTo junk trash | assistant")
        s.setCrm(CRM.Offer(shown: true, enabled: true, count: 1, saved: true))
        #expect(show(ContextMenu.messageItems(s))
            == "openMessage | reply replyAll forward | crmOpen crmEditLinks crmRemove | markUnread toggleFlag | archive moveTo junk trash | assistant")
        s.count = 3
        s.open = true
        s.reply = false
        s.replyAll = false
        s.setCrm(CRM.Offer(shown: true, count: 3, saved: true))
        #expect(show(ContextMenu.messageItems(s))
            == "openMessage | forward | crmSave- | markUnread toggleFlag | archive moveTo junk trash | assistant")
        s.setCrm(CRM.Offer())
        #expect(!show(ContextMenu.messageItems(s)).contains("crm"))
        let labels: [(ContextMenu.ItemID, ContextMenu.Labels, String)] = [
            (.crmOpen, ContextMenu.Labels(), "Open in eWay-CRM"),
            (.crmEditLinks, ContextMenu.Labels(), "Edit Links…"),
            (.crmRemove, ContextMenu.Labels(), "Remove from eWay-CRM"),
            (.crmSave, ContextMenu.Labels(), "Save to eWay-CRM…"),
            (.crmSave, ContextMenu.Labels(count: 4), "Save 4 Messages to eWay-CRM…"),
        ]
        for (id, l, want) in labels {
            #expect(ContextMenu.label(id, l) == want, "\(id)")
        }
    }

    /// The state taken from the window's action flags.
    @Test func messageStateOfFlags() {
        let f = ActionFlags(on: true, outbox: false, flagged: true, reply: true, replyAll: false, forward: true,
                            trash: true, archive: true, junk: false, move: true, markRead: true, toggleFlag: true,
                            unsupported: [.replyAll, .junk])
        let s = ContextMenu.MessageState(flags: f, draft: true, changeStatus: true, assistant: false)
        #expect(s.draft && s.changeStatus && !s.assistant && !s.outbox)
        #expect(s.offerReply && !s.offerReplyAll && s.offerForward && s.offerArchive && s.offerMove && !s.offerJunk && s.offerTrash)
        #expect(s.reply && !s.replyAll && s.forward && s.trash && s.archive && !s.junk && s.move && s.markRead && !s.markUnread && s.toggleFlag)
    }

    @Test func folderItems() {
        let cases: [(String, ContextMenu.FolderState, String)] = [
            ("inbox with unread mail", .init(selectable: true, role: .inbox, unread: 3, total: 9, canDelete: true, foldable: true),
             "checkMail | markAllRead | favourite fold"),
            ("all read", .init(selectable: true, role: .none, total: 9, canDelete: true), "checkMail | markAllRead- | favourite"),
            ("trash", .init(selectable: true, role: .trash, unread: 1, total: 4, canDelete: true),
             "checkMail | markAllRead emptyFolder | favourite"),
            ("empty junk", .init(selectable: true, role: .junk, canDelete: true), "checkMail | markAllRead- emptyFolder- | favourite"),
            ("trash of an account that cannot delete", .init(selectable: true, role: .trash, total: 4),
             "checkMail | markAllRead- | favourite"),
            ("outbox", .init(selectable: true, role: .outbox, total: 2, canDelete: true), "favourite"),
            ("container", .init(role: .none, foldable: true), "fold"),
            ("container that does not fold", .init(role: .trash, total: 3, canDelete: true), ""),
        ]
        for (name, f, want) in cases {
            #expect(show(ContextMenu.folderItems(f)) == want, "\(name)")
        }
        #expect(show(ContextMenu.accountItems(foldable: true)) == "checkMail | fold")
        #expect(show(ContextMenu.accountItems(foldable: false)) == "checkMail")
    }

    private func folder(_ id: String, _ role: FolderRole, _ selectable: Bool) -> Folder {
        Folder(id: FolderID(id), accountId: "a", name: id, path: id, role: role, subscribed: true,
               selectable: selectable, synced: true, unread: 0, total: 0)
    }

    @Test func moveTargets() {
        var view = folder("openMessage", .none, true)
        view.virtual = .open
        let entries: [ContextMenu.Entry] = [
            .init(folder: folder("", .none, false), depth: 0), // a heading
            .init(folder: folder("inbox", .inbox, true), depth: 0),
            .init(folder: folder("drafts", .drafts, true), depth: 0),
            .init(folder: folder("sent", .sent, true), depth: 0),
            .init(folder: folder("all", .all, true), depth: 0),
            .init(folder: folder("trash", .trash, true), depth: 0),
            .init(folder: folder("outbox", .outbox, true), depth: 0),
            .init(folder: view, depth: 0),
            .init(folder: folder("projects", .none, false), depth: 0),
            .init(folder: folder("client", .none, true), depth: 1),
            .init(folder: folder("empty", .none, false), depth: 0),
            .init(folder: folder("emptier", .none, false), depth: 1),
            .init(folder: folder("lists", .none, false), depth: 0),
            .init(folder: folder("deep", .none, false), depth: 1),
            .init(folder: folder("current", .none, true), depth: 2),
        ]
        let got = ContextMenu.moveTargets(entries, current: "current")
            .map { "\($0.folder.id.rawValue)/\($0.depth)" + ($0.enabled ? "" : "-") }.joined(separator: " ")
        #expect(got == "inbox/0 sent/0 trash/0 projects/0- client/1 lists/0- deep/1- current/2-")
        #expect(ContextMenu.moveTargets([], current: "x").isEmpty)
    }

    @Test func labels() {
        typealias L = ContextMenu.Labels
        let cases: [(ContextMenu.ItemID, L, String)] = [
            (.openMessage, L(), "Open in New Window"),
            (.editDraft, L(), "Edit"),
            (.reply, L(), "Reply"),
            (.reply, L(comment: true), "Comment"),
            (.replyAll, L(), "Reply All"),
            (.forward, L(), "Forward"),
            (.markRead, L(), "Mark as _Read"),
            (.markUnread, L(), "Mark as _Unread"),
            (.toggleFlag, L(), "Star"),
            (.toggleFlag, L(flagged: true), "Unstar"),
            (.changeStatus, L(), "Change Status"),
            (.archive, L(), "Archive"),
            (.moveTo, L(), "Move To"),
            (.junk, L(), "Mark as Junk"),
            (.trash, L(), "Move to Trash"),
            (.trash, L(outbox: true), "Cancel Sending"),
            (.assistant, L(), "Assistant"),
            (.checkMail, L(), "Check for New Mail"),
            (.markAllRead, L(), "Mark All as Read"),
            (.emptyFolder, L(), "Empty Trash"),
            (.emptyFolder, L(junk: true), "Empty Junk"),
            (.favourite, L(), "Add to Favourites"),
            (.favourite, L(favourite: true), "Remove from Favourites"),
            (.fold, L(), "Collapse"),
            (.fold, L(collapsed: true), "Expand"),
        ]
        for (id, l, want) in cases {
            #expect(ContextMenu.label(id, l) == want, "\(id) \(l)")
        }
    }

    @Test func emptyQuestion() {
        let q = ContextMenu.emptyQuestion(junk: false, count: 1)
        #expect(q.heading == "Empty the Trash?" && q.confirm == "_Empty")
        #expect(q.body == "1 message will be deleted for good. Older messages that are only on the mail server stay there.")
        let j = ContextMenu.emptyQuestion(junk: true, count: 12)
        #expect(j.heading == "Empty the Junk Folder?" && j.body.hasPrefix("12 messages will be deleted for good."))
    }

    @Test func progressTexts() {
        #expect(ContextMenu.movingText(1) == "Moving the message" && ContextMenu.movingText(3) == "Moving 3 messages")
        #expect(ContextMenu.markingReadText() == "Marking the folder as read")
        #expect(ContextMenu.emptyingText(junk: false) == "Emptying the Trash")
        #expect(ContextMenu.emptyingText(junk: true) == "Emptying the Junk folder")
    }
}
