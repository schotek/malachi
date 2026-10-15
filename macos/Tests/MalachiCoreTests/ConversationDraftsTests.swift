// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// ui/internal/conversation/drafts_test.go (English catalogue: the msgids
// come back verbatim) and the layout's place for the draft cards
// (conversation_layout.go `convWithDrafts`).

/// 2026-09-01T09:00:00Z.
private let t0 = Date(timeIntervalSince1970: 1_788_253_200)

private func at(_ min: Int) -> Date {
    t0.addingTimeInterval(Double(min) * 60)
}

private let mailAccount = Account(
    id: "a1", config: AccountConfig(name: "Work", email: "petr@acme.example"), enabled: true,
    state: SyncState(accountId: "a1", status: .idle))
private let jiraAccount = Account(
    id: "j1", config: AccountConfig(name: "Acme Jira", email: "petr@acme.example", kind: .jira), enabled: true,
    state: SyncState(accountId: "j1", status: .idle), capabilities: [.comment, .forward])

private func draft(_ id: String, _ min: Int, _ text: String, account: AccountID = "a1") -> Draft {
    Draft(id: DraftID(id), accountId: account, version: 1, textBody: text, inReplyTo: "m1", updatedAt: at(min))
}

private func ids(_ cards: [Conversation.DraftCard]) -> [String] {
    cards.map { $0.draft.id?.rawValue ?? "" }
}

private func summary(count: Int, sent: Int = 0, drafts: Int = 0) -> ThreadSummary {
    let latest = MessageSummary(
        id: "m1", accountId: "a1", folderId: "f1", threadId: "t1", from: [], to: [], subject: "", date: t0,
        snippet: "", flags: [], hasAttachments: false, size: 0)
    return ThreadSummary(
        id: "t1", accountId: "a1", subject: "", participants: [], messageCount: count, unreadCount: 0, latestDate: t0,
        latest: latest, snippet: "", flags: [], hasAttachments: false, folderIds: [], sentCount: sent,
        draftCount: drafts)
}

@MainActor
struct ConversationDraftsTests {
    @Test func isConversationRowCountsDrafts() {
        let cases: [(Int, Int, Int, Bool)] = [
            (1, 0, 0, false), (1, 0, 1, true), (1, 1, 1, true), (0, 0, 2, true), (1, 0, -3, false), (2, 0, 0, true),
        ]
        for (count, sent, drafts, want) in cases {
            #expect(
                Conversation.isConversationRow(summary(count: count, sent: sent, drafts: drafts)) == want,
                "\(count) \(sent) \(drafts)")
        }
    }

    @Test func drafts() {
        let input = [
            draft("d1", 10, "first"),
            draft("d3", 30, "third"),
            draft("d2", 30, "second"),  // a tie: the greater id first
            draft("d1", 40, "repeated"),
            Draft(id: DraftID(""), accountId: "a1", updatedAt: at(50)),
            Draft(id: nil, accountId: "a1", updatedAt: at(55)),
            draft("dx", 60, "other", account: "a2"),  // another account's
        ]
        let got = Conversation.drafts(input, account: mailAccount)
        #expect(ids(got) == ["d3", "d2", "d1"])
        #expect(got[2].text == "first" && got[0].sender == "petr@acme.example" && !got[0].comment && !got[0].internal)

        var named = mailAccount
        named.config.displayName = "  Petr\u{202E} Svoboda\n"
        #expect(Conversation.drafts([input[0]], account: named)[0].sender == "Petr Svoboda")

        // A comment draft: internal shows the badge, public none.
        let issue = IssueInfo(
            key: "ITSD-42", url: "", summary: "", status: "", statusCategory: .inProgress, type: "", priority: "",
            assignee: "", reporter: "")
        var internalDraft = draft("c1", 1, "", account: "j1")
        internalDraft.comment = DraftComment(issue: issue, visibility: .internal)
        var publicDraft = draft("c2", 0, "", account: "j1")
        publicDraft.comment = DraftComment(issue: issue, visibility: .public)
        let cc = Conversation.drafts([internalDraft, publicDraft], account: jiraAccount)
        #expect(cc.count == 2)
        #expect(cc[0].comment && cc[0].internal && cc[0].internalLabel == "Internal")
        #expect(cc[1].comment && !cc[1].internal && cc[1].internalLabel == "")

        // The cap: the newest `API.Limits.maxThreadDrafts`.
        let many = (0..<(API.Limits.maxThreadDrafts + 5)).map { draft(String(format: "d%02d", $0), $0, "x") }
        let capped = Conversation.drafts(many, account: mailAccount)
        #expect(capped.count == API.Limits.maxThreadDrafts)
        #expect(capped.first?.draft.id == DraftID(String(format: "d%02d", API.Limits.maxThreadDrafts + 4)))
        #expect(Conversation.drafts([], account: mailAccount).isEmpty)
    }

    /// The text is plain and cleaned: control and format characters go,
    /// line breaks stay, at most `draftTextBytes`.
    @Test func draftsTextPathological() {
        let text = "Hi\u{202E} there\u{0}\r\n\r\n\r\n\r\nBye \u{200B}<b>not markup</b>\u{2028}end"
        let c = Conversation.drafts([draft("d", 0, text)], account: mailAccount)
        #expect(c[0].text == "Hi there\n\nBye <b>not markup</b>\nend")
        let long = String(repeating: "é", count: Conversation.draftTextBytes)
        let l = Conversation.drafts([draft("d", 0, long)], account: mailAccount)
        #expect(l[0].text.utf8.count <= Conversation.draftTextBytes && !l[0].text.isEmpty)
    }

    @Test func sameDrafts() {
        let a = Conversation.drafts([draft("d1", 0, "x")], account: mailAccount)
        let b = Conversation.drafts([draft("d1", 0, "x")], account: mailAccount)
        #expect(Conversation.sameDrafts(a, b) && Conversation.sameDrafts([], []))
        var newer = draft("d1", 1, "y")
        newer.version = 2
        let c = Conversation.drafts([newer], account: mailAccount)
        #expect(!Conversation.sameDrafts(a, c) && !Conversation.sameDrafts(a, []))
    }

    /// thread_model.go: a conversation the listing says has drafts is not
    /// complete before thread.get answered, a change of the count changes
    /// its shape, and its row shows the Draft badge.
    @Test func listingKnowsOfDrafts() {
        #expect(membersFromListing(summary(count: 1)).complete)
        #expect(!membersFromListing(summary(count: 1, drafts: 1)).complete)
        #expect(sameShape(summary(count: 1, drafts: 1), summary(count: 1, drafts: 1)))
        #expect(!sameShape(summary(count: 1), summary(count: 1, drafts: 1)))
        #expect(summaryThread(summary(count: 1, drafts: 2), expanded: false, loading: false).draft == "Draft")
        #expect(summaryThread(summary(count: 2), expanded: false, loading: false).draft == "")
    }

    @Test func draftsFirst() {
        #expect(Conversation.draftsFirst(0) == 1 && Conversation.draftsFirst(-1) == 0)
    }

    @Test func draftTexts() {
        #expect(Conversation.draftBadge() == "Draft")
        #expect(Conversation.draftEditLabel() == "Edit")
        #expect(Conversation.draftDiscardLabel() == "_Discard")
        #expect(Conversation.discardQuestion() == "Discard this message?")
        #expect(Conversation.draftGone() == "The draft is no longer there")
        #expect(Conversation.draftSaved("10:00") == "Draft saved 10:00")
    }

    /// conversation_layout.go `convWithDrafts`: the drafts at their place,
    /// each the user's own message card on the timeline.
    @Test func withDraftsPlacesThemForTheRails() {
        let a = Conversation.Item(kind: .message)
        var e = Conversation.Item(kind: .event)
        e.sender = "x"
        let out = ConversationLayout.withDrafts([a, e], at: 1, count: 2)
        #expect(out.map(\.kind) == [.message, .message, .message, .event])
        #expect(out[1].mine && out[2].mine && !out[0].mine)
        let rails = ConversationLayout.rails(out)
        #expect(rails[1].marker == .avatar && rails[1].accent)
        #expect(ConversationLayout.withDrafts([a], at: 5, count: 1).count == 2)
        #expect(ConversationLayout.withDrafts([a], at: -1, count: 1)[0].mine)
    }
}
