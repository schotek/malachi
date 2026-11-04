// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// ui/internal/conversation/invitation_test.go (English catalogue).

private let t0 = Date(timeIntervalSince1970: 1_788_253_200)
private let jana = Address(name: "Jana Dvořáková", address: "jana@acme.example")
private let petr = Address(name: "Petr Svoboda", address: "petr@acme.example")
private let mailAccount = Account(
    id: "a1", config: AccountConfig(name: "Work", email: "petr@acme.example"), enabled: true,
    state: SyncState(accountId: "a1", status: .idle))

private func mail(_ id: String, _ min: Int, _ seen: Bool, from: Address = jana, folder: FolderID = "f1") -> MessageSummary {
    MessageSummary(
        id: MessageID(id), accountId: "a1", folderId: folder, threadId: "t1", from: [from], to: [],
        subject: "Quarterly report", date: t0.addingTimeInterval(Double(min) * 60), snippet: "",
        flags: seen ? [.seen] : [], hasAttachments: false, size: 0)
}

private func invitation(_ method: InvitationMethod, _ r: EventResponse? = nil, from: Address? = nil) -> InvitationSummary {
    InvitationSummary(
        method: method, kind: method == .reply ? .reply : .invitation, state: .processed, title: "Quarterly report",
        replyFrom: from, replyResponse: r)
}

private func thread(_ count: Int) -> ThreadSummary {
    ThreadSummary(
        id: "t1", accountId: "a1", subject: "Quarterly report", participants: [], messageCount: count, unreadCount: 0,
        latestDate: .goZero, latest: mail("latest", 0, true), snippet: "", flags: [], hasAttachments: false,
        folderIds: [])
}

struct ConversationInvitationTests {
    @Test func replyLine() {
        #expect(Conversation.invitationReplyLine(.accepted) == "Invitation accepted")
        #expect(Conversation.invitationReplyLine(.tentative) == "Invitation tentatively accepted")
        #expect(Conversation.invitationReplyLine(.declined) == "Invitation declined")
        #expect(Conversation.invitationReplyLine(.needsAction) == "Replied to the invitation")
        #expect(Conversation.invitationReplyLine(nil) == "Replied to the invitation")
    }

    @Test func rows() {
        var invite = mail("m1", 0, true)
        invite.invitation = invitation(.request)
        #expect(!Conversation.isInvitationReply(invite))
        var mine = mail("s1", 15, true, from: petr, folder: "fs")
        mine.invitation = invitation(.reply, .tentative, from: petr)
        #expect(Conversation.isInvitationReply(mine))

        var m = Conversation.build(thread(1), [invite], sent: [mine], account: mailAccount)
        #expect(m.items.count == 2)
        let r = m.items[1]
        #expect(r.kind == .event && r.sent && !r.unread && r.mine && r.sender == "Petr Svoboda")
        #expect(r.eventLines == ["Invitation tentatively accepted"] && r.eventText == "Invitation tentatively accepted")
        let a = Conversation.cardActions(mailAccount, mine, composeAccount: true)
        #expect(!a.reply && !a.replyAll && !a.forward)
        let rows = Conversation.chatRows(m, drafts: [])
        #expect(rows.count == 2 && rows[1].kind == .event && rows[1].side == .center)
        #expect(m.markRead == nil)

        var theirs = mail("r1", 20, false)
        theirs.invitation = invitation(.reply, .accepted, from: jana)
        m = Conversation.build(thread(2), [invite, theirs], account: mailAccount)
        #expect(m.items[1].kind == .event && m.items[1].unread && !m.items[1].mine)
        #expect(m.items[1].eventLines == ["Invitation accepted"])
        #expect(m.markRead == "r1")
        m = Conversation.merge(m, mail("m2", 30, true), account: mailAccount)
        #expect(m.markRead == nil)
    }
}
