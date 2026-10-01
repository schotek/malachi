// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// ui/internal/conversation/conversation_test.go (English catalogue: the
// msgids come back verbatim). The Czech plural forms of the truncated row,
// which the Go test checks with a fake catalogue, are checked here against
// the generated one (`MALACHI_LOCALE_DIR`), and po_test.go's intent against
// the Swift port's source. Go's "" ids are nil here.

/// Characters the cleaning must drop, as scalar constants: the source
/// stays free of invisible characters.
private let rlo = jiraScalar(0x202E) // RIGHT-TO-LEFT OVERRIDE
private let zwsp = jiraScalar(0x200B) // ZERO WIDTH SPACE

/// 2026-09-01T09:00:00Z.
private let t0 = Date(timeIntervalSince1970: 1_788_253_200)

private func at(_ min: Int) -> Date {
    t0.addingTimeInterval(Double(min) * 60)
}

private let jana = Address(name: "Jana Dvořáková", address: "jana@acme.example")
private let petr = Address(name: "Petr Svoboda", address: "petr@acme.example")

/// The accounts of the two conversations: Petr's mailbox (the mail of the
/// tests is Jana's, written to him) and his account on the Jira site.
private let mailAccount = Account(
    id: "a1", config: AccountConfig(name: "Work", email: "petr@acme.example"), enabled: true,
    state: SyncState(accountId: "a1", status: .idle))
private let jiraAccount = Account(
    id: "j1", config: AccountConfig(name: "Acme Jira", email: "petr@acme.example", kind: .jira), enabled: true,
    state: SyncState(accountId: "j1", status: .idle), capabilities: [.comment, .forward])

/// The account of a conversation of the tests.
private func accountOf(_ t: ThreadSummary) -> Account {
    t.accountId == jiraAccount.id ? jiraAccount : mailAccount
}

/// A member of the mail conversation t1 in folder f1.
private func mail(_ id: String, _ min: Int, _ seen: Bool) -> MessageSummary {
    MessageSummary(
        id: MessageID(id), accountId: "a1", folderId: "f1", threadId: "t1", from: [jana], to: [petr],
        subject: "Re: Quarterly report", date: at(min), snippet: "See the figures", flags: seen ? [.seen] : [],
        hasAttachments: false, size: 0)
}

private let issue = IssueInfo(
    key: "ITSD-42", url: "https://acme.atlassian.net/browse/ITSD-42", summary: "Printer on the third floor jams",
    status: "In Progress", statusCategory: .inProgress, type: "Incident", priority: "High", assignee: "Petr Svoboda",
    reporter: "Jana Dvořáková", commentVisibilities: [.public, .internal])

/// A member of the issue ITSD-42 (thread tj of account j1).
private func issueMsg(_ id: String, _ min: Int, _ seen: Bool, _ item: MessageIssue) -> MessageSummary {
    var item = item
    item.info = issue
    return MessageSummary(
        id: MessageID(id), accountId: "j1", folderId: "space-itsd", threadId: "tj",
        from: [Address(name: "Jana Dvořáková", address: "")], subject: "ITSD-42: Printer on the third floor jams",
        date: at(min), snippet: "", flags: seen ? [.seen] : [], hasAttachments: false, size: 0, issue: item)
}

private func item(
    _ kind: IssueItemKind, visibility: CommentVisibility? = nil, changes: [IssueChange] = [], via: String? = nil,
    edited: Bool? = nil, mine: Bool? = nil
) -> MessageIssue {
    MessageIssue(info: issue, item: kind, visibility: visibility, changes: changes, via: via, edited: edited, mine: mine)
}

private func statusEvent(_ id: String, _ min: Int, _ from: String, _ to: String) -> MessageSummary {
    issueMsg(id, min, false, item(.event, changes: [IssueChange(field: .status, from: from, to: to)]))
}

private func mailThread(_ count: Int) -> ThreadSummary {
    ThreadSummary(
        id: "t1", accountId: "a1", subject: "Quarterly report", participants: [], messageCount: count, unreadCount: 0,
        latestDate: .goZero, latest: mail("latest", 0, true), snippet: "", flags: [], hasAttachments: false,
        folderIds: [])
}

private func jiraThread(_ count: Int) -> ThreadSummary {
    ThreadSummary(
        id: "tj", accountId: "j1", subject: "", participants: [], messageCount: count, unreadCount: 0,
        latestDate: .goZero, latest: statusEvent("latest", 0, "", ""), snippet: "", flags: [], hasAttachments: false,
        folderIds: [], issue: issue)
}

/// An item as "kind:id" ("more" for the truncated row).
private func shape(_ m: Conversation.Model) -> [String] {
    m.items.map { it in
        switch it.kind {
        case .message: return "msg:" + (it.message?.id.rawValue ?? "")
        case .event: return "event:" + (it.message?.id.rawValue ?? "")
        case .truncated: return "more"
        }
    }
}

private struct BuildCase {
    var name: String
    var thread: ThreadSummary
    var members: [MessageSummary]
    var shape: [String]
    var markRead: MessageID?
    var earlier = 0
    var issueKey = ""
}

struct ConversationTests {
    @Test func isConversationRow() {
        for (n, want) in [(0, false), (1, false), (2, true), (3, true), (612, true)] {
            var t = mailThread(n)
            t.messageCount = n
            #expect(Conversation.isConversationRow(t) == want, "count \(n)")
        }
    }

    /// sent_test.go TestIsConversationRowCountsSent.
    @Test func isConversationRowCountsSent() {
        for (count, sent, want) in [(1, 0, false), (1, 1, true), (0, 2, true), (2, 0, true), (0, 1, false), (1, -5, false)] {
            var t = mailThread(count)
            t.sentCount = sent
            #expect(Conversation.isConversationRow(t) == want, "count \(count), sent \(sent)")
        }
    }

    @Test func build() {
        var queued = mail("m4", 40, false)
        queued.outbox = OutboxInfo(state: .queued, attempts: 0)
        let unknownEvent = issueMsg("e9", 50, false, item(.event, changes: [IssueChange(field: "priority", from: "Low", to: "High")]))
        let sameDateB = mail("b", 10, true)
        let sameDateA = mail("a", 10, true)
        let cases: [BuildCase] = [
            BuildCase(
                name: "mail thread, newest unread", thread: mailThread(3),
                members: [mail("m1", 0, true), mail("m2", 10, false), mail("m3", 20, false)],
                shape: ["msg:m1", "msg:m2", "msg:m3"], markRead: "m3"),
            BuildCase(
                name: "all read", thread: mailThread(2), members: [mail("m1", 0, true), mail("m2", 10, true)],
                shape: ["msg:m1", "msg:m2"]),
            BuildCase(
                name: "newest read, an older one unread stays unread", thread: mailThread(3),
                members: [mail("m1", 0, false), mail("m2", 10, false), mail("m3", 20, true)],
                shape: ["msg:m1", "msg:m2", "msg:m3"]),
            BuildCase(
                name: "jira: description, comments, newest an event", thread: jiraThread(4),
                members: [
                    issueMsg("d", 0, true, item(.description)),
                    issueMsg("c1", 10, false, item(.comment, visibility: .internal)),
                    issueMsg("c2", 20, false, item(.comment, visibility: .public)),
                    statusEvent("e1", 30, "To Do", "In Progress"),
                ],
                shape: ["msg:d", "msg:c1", "msg:c2", "event:e1"], markRead: "c2", issueKey: "ITSD-42"),
            BuildCase(
                name: "jira: the newest comment read, events never count", thread: jiraThread(3),
                members: [
                    issueMsg("c1", 10, false, item(.comment)),
                    issueMsg("c2", 20, true, item(.comment)),
                    statusEvent("e1", 30, "To Do", "Done"),
                ],
                shape: ["msg:c1", "msg:c2", "event:e1"], issueKey: "ITSD-42"),
            BuildCase(
                name: "jira: only events", thread: jiraThread(2),
                members: [statusEvent("e1", 0, "", "To Do"), statusEvent("e2", 10, "To Do", "Done")],
                shape: ["event:e1", "event:e2"], issueKey: "ITSD-42"),
            BuildCase(
                name: "jira: an event of an unknown field is left out", thread: jiraThread(2),
                members: [issueMsg("d", 0, false, item(.description)), unknownEvent],
                shape: ["msg:d"], markRead: "d", issueKey: "ITSD-42"),
            BuildCase(
                name: "a queued outbox member is shown but never marked", thread: mailThread(4),
                members: [mail("m1", 0, true), mail("m2", 10, false), queued],
                shape: ["msg:m1", "msg:m2", "msg:m4"], markRead: "m2", earlier: 1),
            BuildCase(
                name: "duplicates keep the first, empty ids are dropped", thread: mailThread(2),
                members: [mail("m1", 0, true), mail("m2", 10, false), mail("m1", 30, false), mail("", 40, false), mail("m2", 50, true)],
                shape: ["msg:m1", "msg:m2"], markRead: "m2"),
            BuildCase(
                name: "unsorted input, equal dates by id", thread: mailThread(4),
                members: [mail("m3", 20, false), sameDateB, mail("m1", 0, true), sameDateA],
                shape: ["msg:m1", "msg:a", "msg:b", "msg:m3"], markRead: "m3"),
            BuildCase(
                name: "a stale count below the members adds no row", thread: mailThread(1),
                members: [mail("m1", 0, true), mail("m2", 10, true)], shape: ["msg:m1", "msg:m2"]),
            BuildCase(name: "nothing to show: empty, no older members", thread: jiraThread(3), members: [unknownEvent], shape: []),
            BuildCase(name: "no members: empty, even with an issue", thread: jiraThread(3), members: [], shape: []),
        ]
        for tc in cases {
            let m = Conversation.build(tc.thread, tc.members, account: accountOf(tc.thread))
            var wantShape = tc.shape
            if tc.earlier > 0 {
                wantShape.insert("more", at: 0)
            }
            #expect(shape(m) == wantShape, "\(tc.name)")
            #expect(m.markRead == tc.markRead, "\(tc.name): MarkRead")
            #expect(m.earlier == tc.earlier, "\(tc.name): Earlier")
            #expect(m.scrollTo == wantShape.count - 1, "\(tc.name): ScrollTo")
            if tc.issueKey.isEmpty {
                #expect(m.issue == nil, "\(tc.name): Issue")
            } else {
                #expect(m.issue?.key == tc.issueKey, "\(tc.name): Issue")
            }
            #expect(m.thread == tc.thread.id, "\(tc.name): Thread")
            for (i, it) in m.items.enumerated() {
                if it.kind == .event {
                    #expect(!it.unread, "\(tc.name): item \(i): an event is unread")
                }
                if it.kind == .message, let s = it.message {
                    #expect(it.unread == !hasFlag(s.flags, .seen), "\(tc.name): item \(i): Unread against flags")
                }
            }
        }
    }

    @Test func buildItems() throws {
        let members = [
            issueMsg("d", 0, true, item(.description)),
            issueMsg("c1", 10, false, item(.comment, visibility: .internal, via: "Issue Sync", edited: true)),
            issueMsg("e1", 20, false, item(.event, changes: [
                IssueChange(field: .status, from: "To Do", to: "In Progress"),
                IssueChange(field: "priority", from: "Low", to: "High"),
                IssueChange(field: .assignee, to: "Petr Svoboda"),
            ])),
        ]
        let m = Conversation.build(jiraThread(3), members, account: jiraAccount)
        var d = Conversation.Item(kind: .message, message: members[0])
        d.sender = "Jana Dvořáková"
        var c1 = Conversation.Item(kind: .message, message: members[1])
        c1.sender = "Jana Dvořáková"
        c1.unread = true
        c1.internal = true
        c1.internalLabel = "Internal"
        c1.via = "via Issue Sync"
        c1.edited = "Edited"
        var e1 = Conversation.Item(kind: .event, message: members[2])
        e1.sender = "Jana Dvořáková"
        e1.eventLines = ["Status: To Do → In Progress", "Assignee: Unassigned → Petr Svoboda"]
        e1.eventText = "Status: To Do → In Progress; Assignee: Unassigned → Petr Svoboda"
        #expect(m.items == [d, c1, e1])
        let c = try #require(m.issue)
        #expect(c.key == "ITSD-42" && c.summary == "Printer on the third floor jams" && c.status == "In Progress"
            && c.url == "https://acme.atlassian.net/browse/ITSD-42")
        #expect(!c.internal && c.via == "" && c.edited == "", "the issue card carries an item's badges")
    }

    @Test func buildIssueFallsBackToNewestMember() {
        let older = issueMsg("c1", 0, true, item(.comment))
        var newer = issueMsg("c2", 10, true, item(.comment))
        newer.issue?.info.status = "Done"
        var thread = jiraThread(2)
        thread.issue = nil
        let m = Conversation.build(thread, [newer, older], account: jiraAccount)
        #expect(m.issue?.status == "Done", "the newest member's")
        #expect(Conversation.build(mailThread(2), [mail("m1", 0, true), mail("m2", 1, true)], account: mailAccount).issue == nil)
    }

    @Test func buildTruncated() {
        let n = API.Limits.maxThreadMessages
        let members = (0..<n).map { i in mail(String(format: "m%03d", i), i, i != n - 1) }
        let m = Conversation.build(mailThread(612), members, account: mailAccount)
        #expect(m.items.count == 501)
        #expect(m.items.first?.kind == .truncated)
        #expect(m.items.first?.text == "112 earlier messages are not shown")
        #expect(m.items.first?.message == nil && m.items.first?.sender == "", "the truncated row carries no message")
        #expect(m.earlier == 112 && m.scrollTo == 500 && m.markRead == "m499")
        #expect(m.index("m000") == 1 && m.index("m499") == 500 && m.index(MessageID("")) == -1 && m.index("m999") == -1)
        #expect(m.index(nil) == -1)

        let one = Conversation.build(mailThread(3), Array(members[..<2]), account: mailAccount)
        #expect(one.items.first?.text == "1 earlier message is not shown")
    }

    @Test func buildCapsMembers() {
        let n = API.Limits.maxThreadMessages + 10
        let members = (0..<n).map { i in mail(String(format: "m%03d", i), i, true) }
        let m = Conversation.build(mailThread(n), members, account: mailAccount)
        #expect(m.items.count == API.Limits.maxThreadMessages + 1)
        #expect(m.earlier == 10)
        #expect(m.items[1].message?.id == "m010", "the oldest shown")
        #expect(m.items[0].text == "10 earlier messages are not shown")
    }

    @Test func mine() {
        func from(_ id: String, _ min: Int, _ list: Address...) -> MessageSummary {
            var s = mail(id, min, true)
            s.from = list
            return s
        }
        var noAddress = mailAccount
        noAddress.config.email = "  "
        var ownAddress = issueMsg("c4", 16, true, item(.comment))
        ownAddress.from = [petr]
        let cases: [(String, Account, MessageSummary, Bool)] = [
            ("mail of the account's address", mailAccount, from("m1", 0, petr), true),
            ("mail of another sender", mailAccount, from("m2", 1, jana), false),
            ("case and surrounding space do not count", mailAccount,
             from("m3", 2, Address(name: "Petr", address: " \tPetr@ACME.Example\n")), true),
            ("the address decides, not the name", mailAccount,
             from("m4", 3, Address(name: "Petr Svoboda", address: "petr@other.example")), false),
            ("a name that is the address does not count", mailAccount,
             from("m5", 4, Address(name: "petr@acme.example", address: "jana@acme.example")), false),
            ("only the first sender counts", mailAccount, from("m6", 5, jana, petr), false),
            ("the first sender without an address", mailAccount,
             from("m7", 6, Address(name: "Petr Svoboda", address: ""), petr), false),
            ("a longer address that holds the account's", mailAccount,
             from("m8", 7, Address(name: nil, address: "petr@acme.example.invalid")), false),
            ("an invisible character makes another address", mailAccount,
             from("m9", 8, Address(name: nil, address: "petr" + zwsp + "@acme.example")), false),
            ("missing From", mailAccount, from("m10", 9), false),
            ("an account without an address", noAddress, from("m11", 10, Address(name: nil, address: "  ")), false),
            ("an account without an address, a sender with one", noAddress, from("m12", 11, petr), false),
            ("jira: the user's comment", jiraAccount, issueMsg("c1", 12, true, item(.comment, mine: true)), true),
            ("jira: the user's description", jiraAccount, issueMsg("d", 13, true, item(.description, mine: true)), true),
            ("jira: someone else's comment", jiraAccount, issueMsg("c2", 14, true, item(.comment)), false),
            ("jira: said not to be the user's", jiraAccount, issueMsg("c5", 14, true, item(.comment, mine: false)), false),
            ("jira: a comment relayed by an integration is never the user's", jiraAccount,
             issueMsg("c3", 15, true, item(.comment, via: "Issue Sync", mine: true)), false),
            ("jira: the site decides, not the sender's address", jiraAccount, ownAddress, false),
            ("jira: a change the user made", jiraAccount,
             issueMsg("e1", 17, false, item(.event, changes: [IssueChange(field: .status, from: "To Do", to: "Done")], mine: true)),
             true),
        ]
        for (name, account, msg, want) in cases {
            let other = msg.issue != nil ? issueMsg("c0", -10, true, item(.comment)) : mail("m0", -10, true)
            var thread = msg.issue != nil ? jiraThread(2) : mailThread(2)
            thread.issue = nil
            let m = Conversation.build(thread, [other, msg], account: account)
            let at = m.index(msg.id)
            #expect(at >= 0, "\(name): the member is not shown")
            guard at >= 0 else { continue }
            #expect(m.items[at].mine == want, "\(name): build")
            #expect(!m.items[m.index(other.id)].mine, "\(name): the other member is the user's")
            // The same member arriving while the conversation is shown.
            let merged = Conversation.merge(Conversation.remove(m, msg.id), msg, account: account)
            #expect(merged.items[merged.index(msg.id)].mine == want, "\(name): merge")
            // Without the account nobody's mail is the user's; an issue's item says so itself.
            #expect(Conversation.build(thread, [other, msg]).items[at].mine == (want && msg.issue != nil), "\(name): no account")
        }

        // The name shown stays the sender's.
        let own = Conversation.build(mailThread(2), [mail("m0", -10, true), from("m1", 0, petr)], account: mailAccount)
        #expect(own.items.map(\.sender) == ["Jana Dvořáková", "Petr Svoboda"])
        #expect(own.items.map(\.mine) == [false, true])

        // The truncated row is nobody's.
        let cut = Conversation.build(mailThread(5), [from("m1", 0, petr), from("m2", 1, petr)], account: mailAccount)
        #expect(cut.items.map(\.kind) == [.truncated, .message, .message])
        #expect(cut.items.map(\.mine) == [false, true, true])
    }

    @Test func senderIsCleaned() {
        var hostile = mail("m1", 0, true)
        hostile.from = [
            Address(name: " " + zwsp + " ", address: ""),
            Address(name: "Jana" + rlo + "\nDvořáková" + zwsp, address: "jana@acme.example"),
        ]
        var long = mail("m2", 1, true)
        long.from = [Address(name: String(repeating: "ř", count: 400), address: "")]
        var bare = mail("m3", 2, true)
        bare.from = [Address(name: nil, address: " petr@acme.example ")]
        var none = mail("m4", 3, true)
        none.from = []
        let m = Conversation.build(mailThread(4), [hostile, long, bare, none], account: mailAccount)
        #expect(m.items[0].sender == "Jana Dvořáková")
        let s = m.items[1].sender
        #expect(s.utf8.count <= 512 && s.hasPrefix("řř") && !s.unicodeScalars.contains("\u{FFFD}"), "long sender: \(s.utf8.count) bytes")
        #expect(m.items[2].sender == "petr@acme.example")
        #expect(m.items[3].sender == "")
    }

    @Test func merge() {
        let base = Conversation.build(mailThread(3), [mail("m1", 0, true), mail("m2", 10, true), mail("m3", 20, true)], account: mailAccount)
        let before = shape(base)

        // Newest arrival.
        var m = Conversation.merge(base, mail("m4", 30, false), account: mailAccount)
        #expect(shape(m) == ["msg:m1", "msg:m2", "msg:m3", "msg:m4"])
        #expect(m.markRead == "m4" && m.scrollTo == 3 && m.index("m4") == 3)

        // An older arrival goes in its place.
        m = Conversation.merge(base, mail("m2b", 10, false), account: mailAccount)
        #expect(shape(m) == ["msg:m1", "msg:m2", "msg:m2b", "msg:m3"])
        #expect(m.markRead == nil, "the newest is read")

        // A shown member is replaced.
        let unread = Conversation.merge(base, mail("m3", 20, false), account: mailAccount)
        #expect(shape(unread) == before)
        #expect(unread.items[2].unread && unread.markRead == "m3")
        let moved = Conversation.merge(base, mail("m1", 25, true), account: mailAccount)
        #expect(shape(moved) == ["msg:m2", "msg:m3", "msg:m1"])

        // Another conversation or no id is ignored.
        var other = mail("x1", 30, false)
        other.threadId = "t2"
        for s in [other, mail("", 30, false)] {
            #expect(Conversation.merge(base, s, account: mailAccount) == base, "\(s.id.rawValue) in \(s.threadId?.rawValue ?? "")")
        }
        var unlinked = mail("x2", 30, false)
        unlinked.threadId = nil
        #expect(Conversation.merge(base, unlinked, account: mailAccount).index("x2") == 3, "a message without a thread id is taken")
        var emptyThread = mail("x3", 30, false)
        emptyThread.threadId = ThreadID("")
        #expect(Conversation.merge(base, emptyThread, account: mailAccount).index("x3") == 3, "an empty thread id is taken")

        // The truncated row stays on top.
        let cut = Conversation.build(mailThread(5), [mail("m2", 10, true), mail("m3", 20, true)], account: mailAccount)
        m = Conversation.merge(cut, mail("m0", -10, false), account: mailAccount)
        #expect(shape(m) == ["more", "msg:m0", "msg:m2", "msg:m3"])
        #expect(m.items[0].text == "3 earlier messages are not shown" && m.earlier == 3)

        // An event refreshes the issue card and is not marked.
        let j = Conversation.build(jiraThread(2), [
            issueMsg("d", 0, true, item(.description)),
            issueMsg("c1", 10, false, item(.comment)),
        ], account: jiraAccount)
        var ev = statusEvent("e1", 20, "In Progress", "Done")
        ev.issue?.info.status = "Done"
        ev.issue?.info.statusCategory = .done
        m = Conversation.merge(j, ev, account: jiraAccount)
        #expect(m.issue?.status == "Done" && j.issue?.status == "In Progress")
        #expect(m.markRead == "c1" && m.scrollTo == 2 && m.items[2].kind == .event)
        let mailArrival = Conversation.merge(j, mail("m9", 30, true), account: jiraAccount)
        #expect(mailArrival.issue?.status == "In Progress", "a member without an issue changed the card")

        // Into an empty model.
        let empty = Conversation.build(mailThread(0), [], account: mailAccount)
        m = Conversation.merge(empty, mail("m1", 0, false), account: mailAccount)
        #expect(shape(m) == ["msg:m1"] && m.markRead == "m1" && m.scrollTo == 0)

        #expect(shape(base) == before && !base.items[2].unread, "Merge modified the model it was given")
    }

    @Test func remove() {
        let base = Conversation.build(mailThread(5), [mail("m1", 0, false), mail("m2", 10, false), mail("m3", 20, false)], account: mailAccount)
        let before = shape(base)

        let m = Conversation.remove(base, "m3")
        #expect(shape(m) == ["more", "msg:m1", "msg:m2"])
        #expect(m.markRead == "m2" && m.scrollTo == 2 && m.earlier == 2)
        #expect(shape(Conversation.remove(base, "m2")) == ["more", "msg:m1", "msg:m3"])
        #expect(Conversation.remove(base, "nope") == base)
        #expect(Conversation.remove(base, MessageID("")) == base)
        let last = Conversation.remove(Conversation.remove(m, "m1"), "m2")
        #expect(last.items.isEmpty && last.issue == nil && last.markRead == nil && last.scrollTo == -1)
        #expect(last.earlier == 2 && last.thread == "t1", "the empty model keeps Earlier and Thread")
        #expect(shape(base) == before, "Remove modified the model it was given")

        let j = Conversation.build(jiraThread(2), [
            issueMsg("c1", 0, false, item(.comment)),
            statusEvent("e1", 10, "To Do", "Done"),
        ], account: jiraAccount)
        let noEvent = Conversation.remove(j, "e1")
        #expect(noEvent.issue != nil && noEvent.markRead == "c1" && noEvent.scrollTo == 0)
        let noComment = Conversation.remove(j, "c1")
        #expect(noComment.markRead == nil && shape(noComment) == ["event:e1"])
    }

    @Test func cardActions() {
        let oldMail = Account(id: "a1", config: AccountConfig(name: "", email: ""), enabled: true, state: SyncState(accountId: "a1", status: .idle))
        let jiraM1 = Account(
            id: "j1", config: AccountConfig(name: "", email: "", kind: .jira), enabled: true, state: SyncState(accountId: "j1", status: .idle),
            capabilities: [])
        let jiraM2 = Account(
            id: "j1", config: AccountConfig(name: "", email: "", kind: .jira), enabled: true, state: SyncState(accountId: "j1", status: .idle),
            capabilities: [.comment, .forward])
        let full = Account(
            id: "a2", config: AccountConfig(name: "", email: ""), enabled: false, state: SyncState(accountId: "a2", status: .idle),
            capabilities: API.mailCapabilities)
        var queued = mail("m2", 10, true)
        queued.outbox = OutboxInfo(state: .failed, attempts: 1)
        let comment = issueMsg("c1", 0, true, item(.comment))
        let event = statusEvent("e1", 10, "To Do", "Done")
        func acts(reply: Bool = false, replyAll: Bool = false, forward: Bool = false, comment: Bool = false) -> Capabilities.Actions {
            Capabilities.Actions(reply: reply, replyAll: replyAll, forward: forward, comment: comment)
        }
        let cases: [(String, Account, MessageSummary, Bool, Capabilities.Actions)] = [
            ("mail", oldMail, mail("m1", 0, true), false, acts(reply: true, replyAll: true, forward: true)),
            ("mail with the full list", full, mail("m1", 0, true), true, acts(reply: true, replyAll: true, forward: true)),
            ("queued mail keeps reply and forward", oldMail, queued, false, acts(reply: true, replyAll: true, forward: true)),
            ("jira without capabilities", jiraM1, comment, true, acts()),
            ("jira comment and forward", jiraM2, comment, true, acts(reply: true, forward: true, comment: true)),
            ("jira forward needs a mail account", jiraM2, comment, false, acts(reply: true, comment: true)),
            ("an event offers nothing", jiraM2, event, true, acts()),
        ]
        for (name, account, msg, compose, want) in cases {
            #expect(Conversation.cardActions(account, msg, composeAccount: compose) == want, "\(name)")
        }
    }
}

// MARK: The user's replies in Sent (sent_test.go)

/// Petr's reply in his Sent folder fs (thread.get's sent). Unread on
/// purpose: a sent card is never unread.
private func reply(_ id: String, _ min: Int) -> MessageSummary {
    MessageSummary(
        id: MessageID(id), accountId: "a1", folderId: "fs", threadId: "t1", from: [petr], to: [jana],
        subject: "Re: Quarterly report", date: at(min), snippet: "Thanks", flags: [], hasAttachments: false, size: 0)
}

/// `shape` with the sent cards marked "sent:".
private func sentShape(_ m: Conversation.Model) -> [String] {
    zip(shape(m), m.items).map { s, it in it.sent ? "sent:" + (it.message?.id.rawValue ?? "") : s }
}

struct ConversationSentTests {
    @Test func buildWithSent() {
        var thread = mailThread(2)
        thread.sentCount = 2
        let members = [mail("m2", 20, false), mail("m1", 0, true)]
        let sent = [reply("r2", 30), reply("r1", 10), reply("r1", 10), reply("", 5)]
        let m = Conversation.build(thread, members, sent: sent, account: mailAccount)
        #expect(sentShape(m) == ["msg:m1", "sent:r1", "msg:m2", "sent:r2"])
        // The newest folder member is marked read, not the newer reply; a
        // sent card is never unread and is the user's own.
        #expect(m.markRead == "m2" && m.scrollTo == 3 && m.earlier == 0)
        let r = m.items[m.index("r2")]
        #expect(!r.unread && r.mine && r.sender == "Petr Svoboda" && r.kind == .message)

        // Only replies, all of them unread: the newest member is marked.
        let only = Conversation.build(mailThread(1), [mail("m1", 0, false)], sent: [reply("r1", 10)], account: mailAccount)
        #expect(sentShape(only) == ["msg:m1", "sent:r1"] && only.markRead == "m1")
    }

    @Test func buildWithSentPathological() {
        // A sent message with a member's id is the member.
        let dup = Conversation.build(mailThread(1), [mail("m1", 0, true)], sent: [reply("m1", 5)], account: mailAccount)
        #expect(sentShape(dup) == ["msg:m1"])
        // Sent alone is no conversation of the folder.
        for (name, members) in [("none", [MessageSummary]()), ("no id", [mail("", 0, true)])] {
            let m = Conversation.build(mailThread(0), members, sent: [reply("r1", 0), reply("r2", 1)], account: mailAccount)
            #expect(m.items.isEmpty && m.earlier == 0 && m.scrollTo == -1 && m.markRead == nil && m.thread == "t1", "\(name)")
        }
        // A sent event (never from a mail account) is left out.
        let j = Conversation.build(
            jiraThread(1), [issueMsg("d", 0, true, item(.description))], sent: [statusEvent("e1", 5, "To Do", "Done")],
            account: jiraAccount)
        #expect(sentShape(j) == ["msg:d"])
        // Equal dates order by id, as members do.
        let same = Conversation.build(mailThread(1), [mail("m", 10, true)], sent: [reply("a", 10), reply("z", 10)], account: mailAccount)
        #expect(sentShape(same) == ["sent:a", "msg:m", "sent:z"])
        // Beyond the cap only the newest replies stay.
        let many = (0..<(API.Limits.maxThreadMessages + 3)).map { reply(String(format: "r%04d", $0), $0 + 1) }
        let capped = Conversation.build(mailThread(1), [mail("m", 0, true)], sent: many, account: mailAccount)
        #expect(capped.items.count == API.Limits.maxThreadMessages + 1)
        #expect(capped.index("r0002") < 0 && capped.index("r0003") == 1)
    }

    /// When older members are left out, a reply older than the oldest
    /// member shown is too: it would sit among the members the row says are
    /// missing.
    @Test func buildWithSentCut() {
        let m = Conversation.build(
            mailThread(5), [mail("m4", 40, true), mail("m5", 50, true)], sent: [reply("r1", 10), reply("r4", 45)],
            account: mailAccount)
        #expect(sentShape(m) == ["more", "msg:m4", "sent:r4", "msg:m5"] && m.earlier == 3)
        let merged = Conversation.mergeSent(m, reply("r0", 5), account: mailAccount)
        #expect(sentShape(merged) == ["more", "msg:m4", "sent:r4", "msg:m5"], "older reply merged into a cut conversation")
    }

    @Test func mergeSent() {
        let base = Conversation.build(mailThread(2), [mail("m1", 0, true), mail("m2", 20, false)], account: mailAccount)
        let m = Conversation.mergeSent(base, reply("r1", 10), account: mailAccount)
        #expect(sentShape(m) == ["msg:m1", "sent:r1", "msg:m2"] && m.markRead == "m2" && m.scrollTo == 2)
        #expect(base.items.count == 2, "MergeSent changed the model given")
        // A changed reply moves to its date; a newer one is last, and still
        // not marked.
        var moved = reply("r1", 30)
        moved.flags = [.flagged]
        let m2 = Conversation.mergeSent(m, moved, account: mailAccount)
        #expect(sentShape(m2) == ["msg:m1", "msg:m2", "sent:r1"] && m2.markRead == "m2" && m2.scrollTo == 2)
        #expect(m2.items[2].message?.flags.count == 1, "the reply was not replaced")
        // Left alone: no id, another conversation, a member's id, an event.
        var other = reply("r9", 5)
        other.threadId = "t2"
        var event = statusEvent("e", 5, "A", "B")
        event.threadId = "t1"
        for (name, s) in [("no id", reply("", 5)), ("other thread", other), ("member id", reply("m1", 5)), ("event", event)] {
            #expect(sentShape(Conversation.mergeSent(m, s, account: mailAccount)) == sentShape(m), "\(name)")
        }
        // An empty model stays empty.
        let empty = Conversation.build(mailThread(0), [], account: mailAccount)
        #expect(Conversation.mergeSent(empty, reply("r1", 0), account: mailAccount).items.isEmpty)
        // A member arriving with a reply's id takes its place.
        let m3 = Conversation.merge(m, mail("r1", 10, false), account: mailAccount)
        #expect(sentShape(m3) == ["msg:m1", "msg:r1", "msg:m2"] && !m3.items[1].sent)
    }

    @Test func removeWithSent() {
        let m = Conversation.build(
            mailThread(2), [mail("m1", 0, true), mail("m2", 20, false)], sent: [reply("r1", 10), reply("r2", 30)],
            account: mailAccount)
        let r = Conversation.remove(m, "r2")
        #expect(sentShape(r) == ["msg:m1", "sent:r1", "msg:m2"] && r.scrollTo == 2 && r.markRead == "m2")
        // The members go, the replies stay behind: no conversation of the
        // folder is left.
        let gone = Conversation.remove(Conversation.remove(m, "m1"), "m2")
        #expect(gone.items.isEmpty && gone.scrollTo == -1 && gone.markRead == nil)
        let cut = Conversation.build(mailThread(4), [mail("m3", 20, true)], sent: [reply("r3", 30)], account: mailAccount)
        let cutGone = Conversation.remove(cut, "m3")
        #expect(cutGone.items.isEmpty && cutGone.earlier == 3)
    }
}

// MARK: Folding (fold_test.go)

/// A card dated by its id's length, as fold_test.go's `card`.
private func card(_ id: String, sent: Bool) -> Conversation.Item {
    var it = Conversation.Item(kind: .message, message: mail(id, id.count, true))
    it.sent = sent
    return it
}

struct ConversationFoldTests {
    @Test func defaultFolds() {
        let event = Conversation.Item(kind: .event, message: mail("e", 0, true))
        var more = Conversation.Item(kind: .truncated)
        more.text = "2 earlier messages are not shown"
        let noID = Conversation.Item(kind: .message, message: mail("", 0, true))
        let cases: [(String, [Conversation.Item], MessageID?, [MessageID: Bool])] = [
            ("nothing", [], nil, [:]),
            ("one message", [card("a", sent: false)], "a", ["a": false]),
            ("opening folded while another follows", [card("a", sent: false), card("bb", sent: false)], "a",
             ["a": true, "bb": false]),
            ("a message and its status changes", [card("a", sent: false), event, more], "a", ["a": false]),
            ("a message and the user's replies", [card("a", sent: false), card("rr", sent: true), card("rrr", sent: true)], "a",
             ["a": false, "rr": true, "rrr": true]),
            ("replies among messages", [card("a", sent: false), card("rr", sent: true), card("bbb", sent: false)], "a",
             ["a": true, "rr": true, "bbb": false]),
            ("the user's reply opened it", [card("r", sent: true), card("aa", sent: false)], "r", ["r": true, "aa": false]),
            ("no opening", [more, card("a", sent: false), card("bb", sent: false)], nil, ["a": false, "bb": false]),
            // Pathological: every card a reply. The newest opens.
            ("all sent", [card("r", sent: true), card("rrr", sent: true), card("rr", sent: true)], "r",
             ["r": true, "rr": true, "rrr": false]),
            ("a repeated id keeps its first state", [card("a", sent: false), card("bb", sent: true), card("bb", sent: false)], "a",
             ["a": false, "bb": true]),
            ("no id", [noID, card("a", sent: false)], nil, ["a": false]),
        ]
        for (name, items, opening, want) in cases {
            #expect(Conversation.defaultFolds(items, opening: opening) == want, "\(name)")
        }
    }

    @Test func folds() {
        let items = [card("a", sent: false), card("rr", sent: true), card("bbb", sent: false)]
        var f = Conversation.Folds()
        f.show("t1")
        #expect(f.state(items, opening: "a") == ["a": true, "rr": true, "bbb": false], "defaults")
        f.set("a", false)
        f.set("bbb", true)
        f.set("", true)
        var want: [MessageID: Bool] = ["a": false, "rr": true, "bbb": true]
        #expect(f.state(items, opening: "a") == want, "choices")
        // An update of the same conversation keeps the choices; a card that
        // arrives starts as its default.
        f.show("t1")
        let more = items + [card("cccc", sent: false)]
        want["cccc"] = false
        #expect(f.state(more, opening: "a") == want, "after an arrival")
        // Collapse All, then a card arrives: it starts open, and the button
        // offers Collapse All again.
        f.setAll(items, Conversation.FoldAll.collapse.folded)
        #expect(Conversation.foldAllOffer(f.state(items, opening: "a")) == .expand, "after Collapse All")
        #expect(Conversation.foldAllOffer(f.state(more, opening: "a")) == .collapse, "after an arrival")
        f.setAll(more, Conversation.FoldAll.expand.folded)
        #expect(f.state(more, opening: "a") == ["a": false, "rr": false, "bbb": false, "cccc": false], "after Expand All")
        // Another conversation forgets them.
        f.show("t2")
        let other = f.state(items, opening: "a")
        #expect(other["a"] == true && other["rr"] == true, "another conversation")
    }

    @Test func foldAllOffer() {
        let cases: [([MessageID: Bool], Conversation.FoldAll, String)] = [
            ([:], .none, ""),
            (["a": false], .none, ""),
            (["a": true, "b": false], .collapse, "Collapse All"),
            (["a": false, "b": false], .collapse, "Collapse All"),
            (["a": true, "b": true], .expand, "Expand All"),
        ]
        for (state, want, label) in cases {
            let got = Conversation.foldAllOffer(state)
            #expect(got == want && got.label == label, "\(state)")
        }
        #expect(Conversation.FoldAll.collapse.folded && !Conversation.FoldAll.expand.folded && !Conversation.FoldAll.none.folded)
    }
}

// MARK: The catalogue

/// The generated catalogues (`MALACHI_LOCALE_DIR`, as `make test-macos`
/// exports it); nil skips the Czech test.
private let localeDir: URL? = {
    guard let dir = ProcessInfo.processInfo.environment[Catalogue.localeDirEnv], !dir.isEmpty else { return nil }
    let url = URL(fileURLWithPath: dir, isDirectory: true)
    return Catalogue.availableLanguages(in: url).contains("cs") ? url : nil
}()

/// The repository root, from this file's place in macos/Tests/MalachiCoreTests.
private let repoRoot = URL(fileURLWithPath: #filePath)
    .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
private let template = repoRoot.appendingPathComponent("po/malachi.pot")
private let port = repoRoot.appendingPathComponent("macos/Sources/MalachiCore/Model/Conversation.swift")
private let foldPort = repoRoot.appendingPathComponent("macos/Sources/MalachiCore/Model/ConversationFold.swift")
private let quotedPort = repoRoot.appendingPathComponent("macos/Sources/MalachiCore/Model/ConversationQuoted.swift")
private let haveSources = FileManager.default.fileExists(atPath: template.path)
    && FileManager.default.fileExists(atPath: port.path) && FileManager.default.fileExists(atPath: foldPort.path)
    && FileManager.default.fileExists(atPath: quotedPort.path)

struct ConversationTranslationTests {
    /// conversation_test.go TestBuildTruncated in Czech: the three plural
    /// forms of the truncated row.
    @Test(.enabled(if: localeDir != nil, "run `make -C macos locale` and export MALACHI_LOCALE_DIR"))
    func czechPluralForms() {
        let cs = Catalogue.load(from: localeDir!, languages: ["cs"])
        let one = "%d earlier message is not shown"
        let other = "%d earlier messages are not shown"
        #expect(cs.plural(one, other, 1) == "1 starší zpráva není zobrazena")
        #expect(cs.plural(one, other, 3) == "3 starší zprávy nejsou zobrazeny")
        #expect(cs.plural(one, other, 7) == "7 starších zpráv není zobrazeno")
    }

    /// po_test.go TestMsgidsInTemplate for the port: its msgids are the
    /// template's five entries that name ui/internal/conversation (the
    /// plural of the older members' row, conversation.go; Collapse All and
    /// Expand All, fold.go; Show and Hide Quoted Text, quoted.go), and the
    /// port asks for each the same way.
    @Test(.enabled(if: haveSources, "po/malachi.pot or the port's source is not next to this file"))
    func portUsesTheTemplatesEntries() throws {
        let pot = try String(contentsOf: template, encoding: .utf8)
        let entries = pot.components(separatedBy: "\n\n").filter { $0.contains("ui/internal/conversation/") }
        #expect(entries.count == 5, "po/malachi.pot names ui/internal/conversation in \(entries.count) entries")
        let all = entries.joined(separator: "\n\n")
        #expect(all.contains("msgid \"%d earlier message is not shown\"\nmsgid_plural \"%d earlier messages are not shown\""))
        #expect(all.contains("msgid \"Collapse All\"\nmsgstr"))
        #expect(all.contains("msgid \"Expand All\"\nmsgstr"))
        let src = try String(contentsOf: port, encoding: .utf8)
        let calls = src.components(separatedBy: "L10n.").dropFirst().map { $0.prefix(90) }
        #expect(calls.count == 1, "the port translates \(calls.count) strings of its own")
        #expect(calls.first?.hasPrefix("N(\"%d earlier message is not shown\", \"%d earlier messages are not shown\"") == true)
        let fold = try String(contentsOf: foldPort, encoding: .utf8)
        let foldCalls = Array(fold.components(separatedBy: "L10n.").dropFirst())
        #expect(foldCalls.count == 2, "the fold port translates \(foldCalls.count) strings")
        #expect(foldCalls.first?.hasPrefix("T(\"Collapse All\")") == true)
        #expect(foldCalls.last?.hasPrefix("T(\"Expand All\")") == true)
        #expect(all.contains("msgid \"Show Quoted Text\"\nmsgstr"))
        #expect(all.contains("msgid \"Hide Quoted Text\"\nmsgstr"))
        let quoted = try String(contentsOf: quotedPort, encoding: .utf8)
        let quotedCalls = Array(quoted.components(separatedBy: "L10n.").dropFirst())
        #expect(quotedCalls.count == 2, "the quoted port translates \(quotedCalls.count) strings")
        #expect(quotedCalls.first?.hasPrefix("T(\"Hide Quoted Text\")") == true)
        #expect(quotedCalls.last?.hasPrefix("T(\"Show Quoted Text\")") == true)
    }
}
