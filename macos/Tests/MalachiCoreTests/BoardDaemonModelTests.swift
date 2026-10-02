// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The board's model with what the daemon adds (docs/api.md §4.13): the
// reason codes and their texts, stale annotations, the linked draft,
// visibility (done, snoozed), commitments' states, the conversation loaded
// on demand and the phases. Swift-first.

private typealias F = BoardFixture

@Suite struct BoardReasonTextTests {
    /// Every code of the contract has a text of its own; nothing else does.
    @Test func everyKnownCodeHasItsOwnText() {
        let codes = BoardReason.known
        var seen = Set<String>()
        for code in codes {
            let t = Board.Text.reason(code)
            #expect(!t.isEmpty && t != Board.Text.reasonUnknown, "\(code.rawValue)")
            #expect(seen.insert(t).inserted, "\(code.rawValue) repeats another code's text")
        }
        #expect(BoardReason.known.count == 16)
    }

    @Test func unknownCodesGetTheGenericText() {
        for code: BoardReason in ["", "hot.someday", "HOT.IMPORTANT", "you.addressed ", "rule c1"] {
            #expect(Board.Text.reason(code) == Board.Text.reasonUnknown, "\(code.rawValue)")
        }
    }
}

@Suite struct BoardStaleTests {
    private func staleCase() -> Board.Case {
        F.mk(
            "c1", .you, subject: "Raw subject",
            annotation: Board.Annotation(
                state: .hot, title: "Old title", summary: "Old summary", why: "Old why", due: F.day(16, 10),
                dueQuote: "by tomorrow", tasks: ["t1"], stale: true),
            draft: "Hi")
    }

    /// A stale annotation counts for nothing; its draft stays.
    @Test func staleAnnotationCountsForNothing() {
        let c = staleCase()
        #expect(Board.state(of: c, annotated: true) == .you)
        #expect(Board.stateSource(of: c, annotated: true) == .rules)
        #expect(Board.annotation(of: c, annotated: true) == nil)
        let v = F.view([c], annotated: true)
        let d = try! #require(v.detail)
        #expect(d.title == "Raw subject" && d.summary == "" && d.tasks.isEmpty && d.due == "" && d.dueQuote == "")
        #expect(d.why == Board.Text.reasonUnknown)
        #expect(d.staleNote == Board.Text.staleNotes)
        #expect(d.draft == "Hi" && d.draftID != nil)
        #expect(v.today.dueGroups.isEmpty)
        #expect(v.sections[0].rows[0].due == "")
        // Without the assistant no note: nothing of it was shown anyway.
        #expect(F.view([c], annotated: false).detail?.staleNote == "")
    }

    /// An annotation that leaves the state to the rules kept it.
    @Test func annotationWithoutAState() {
        let c = F.mk("c1", .them, annotation: Board.Annotation(state: nil, title: "T"))
        #expect(Board.state(of: c, annotated: true) == .them)
        #expect(Board.stateSource(of: c, annotated: true) == .assistantKept)
        #expect(F.view([c], annotated: true).detail?.title == "T")
    }
}

@Suite struct BoardVisibilityTests {
    private let back = F.day(16, 9)

    private func cases() -> [Board.Case] {
        [
            F.mk("l1", .you, hours: 1), F.mk("d1", .info, hours: 2, done: true),
            F.mk("s1", .you, hours: 3, visibility: .snoozed(until: F.day(20, 9))),
            F.mk("s2", .hot, hours: 4, visibility: .snoozed(until: back)),
        ]
    }

    /// Snoozed cases are off the board and listed under Done, soonest back
    /// first, before the done ones.
    @Test func snoozedAreUnderDone() {
        let all = F.view(cases())
        #expect(all.sections.flatMap(\.rows).map(\.id.rawValue) == ["l1"])
        #expect(all.columns.flatMap(\.rows).map(\.id.rawValue) == ["l1"])
        #expect(all.nav.first { $0.filter == .done }?.count == 3)
        #expect(all.nav.first { $0.filter == .all }?.count == 1)
        #expect(all.accounts.first?.count == 1)  // live only
        let done = F.view(cases()) { $0.filter = .done }
        #expect(done.sections.map(\.kind) == [.snoozed, .done])
        #expect(done.sections.map(\.title) == ["Snoozed", "Done"])
        #expect(F.ids(done.sections[0].rows) == ["s2", "s1"])
        #expect(done.sections[0].rows[0].remind == "Tomorrow 09:00")
        #expect(done.sections[1].rows[0].remind == "")
        #expect(done.selection == F.id("s2"))
        let d = try! #require(done.detail)
        #expect(d.isSnoozed && !d.isDone && d.remindText == "Back on the board Tomorrow 09:00")
        // Only snoozed: not empty.
        #expect(!F.view([F.mk("s1", visibility: .snoozed(until: back))]).isEmpty)
    }

    @Test func selectionAfterDoneWalksTheSnoozedToo() {
        var v = Board.ViewState()
        v.filter = .done
        let s = Board.Snapshot(accounts: F.accounts, cases: cases())
        #expect(Board.selectionAfterDone(F.id("s1"), s, v) == F.id("d1"))
        #expect(Board.selectionAfterDone(F.id("d1"), s, v) == F.id("s1"))
    }

    @Test func doneIsTheVisibility() {
        var c = F.mk("c1")
        #expect(!c.done && c.visibility == .live)
        c.done = true
        #expect(c.visibility == .done(at: nil))
        c.visibility = .done(at: F.ago(1))
        c.done = true  // stays as it is, date and all
        #expect(c.visibility == .done(at: F.ago(1)))
        c.visibility = .snoozed(until: back)
        #expect(!c.done)
        c.done = false  // not done already: the remind stays
        #expect(c.visibility.remindAt == back)
        c.done = true
        #expect(c.visibility == .done(at: nil))
    }
}

@Suite struct BoardDetailFieldsTests {
    @Test func detailCarriesTheTargets() {
        var c = F.mk("c1")
        c.thread = "t_9"
        c.reply = Board.ReplyTarget(message: "m_5", folder: "f_inbox")
        c.latestMessage = "m_7"
        c.canArchive = true
        let d = try! #require(F.view([c]).detail)
        #expect(d.accountID == F.accountA && d.thread == "t_9" && d.latestMessage == "m_7")
        #expect(d.reply == Board.ReplyTarget(message: "m_5", folder: "f_inbox"))
        #expect(d.canArchive)
        #expect(d.draft == "" && d.draftID == nil)
    }

    /// The conversation: loading until it arrives, a note when it failed,
    /// the cards once there.
    @Test func messagesOnDemand() {
        let loading = try! #require(F.view([F.mk("c1", messages: nil)]).detail)
        #expect(loading.messagesLoading && loading.messagesNote == Board.Text.messagesLoading && loading.messages.isEmpty)
        var failed = F.mk("c1", messages: nil)
        failed.messagesFailed = true
        let f = try! #require(F.view([failed]).detail)
        #expect(!f.messagesLoading && f.messagesNote == Board.Text.messagesFailed)
        let m = Board.CaseMessage(id: "m_1", from: "Ann", date: F.ago(2), text: "hello")
        let loaded = try! #require(F.view([F.mk("c1", messages: [m])]).detail)
        #expect(!loaded.messagesLoading && loaded.messagesNote == "" && loaded.messages.map(\.id) == ["m_1"])
        let none = try! #require(F.view([F.mk("c1", messages: [])]).detail)
        #expect(!none.messagesLoading && none.messagesNote == "" && none.messages.isEmpty)
    }

    /// Only open promises are shown.
    @Test func onlyOpenCommitments() {
        let ks = [
            Board.Commitment(id: "k1", caseID: F.id("c1"), text: "Open"),
            Board.Commitment(id: "k2", caseID: F.id("c1"), text: "Ticked", state: .done),
            Board.Commitment(id: "k3", caseID: F.id("c1"), text: "Closed", state: .closed),
        ]
        let v = F.view([F.mk("c1")], annotated: true, commitments: ks)
        #expect(v.commitments.map(\.id) == ["k1"])
        #expect(v.today.tiles.last == Board.Tile(kind: .commitments, count: 1, title: Board.Text.commitments))
    }
}

@Suite struct BoardPhaseTests {
    private func view(_ phase: Board.Phase, cases: [Board.Case] = [], truncated: Bool = false) -> Board.View {
        let s = Board.Snapshot(accounts: F.accounts, cases: cases, phase: phase, truncated: truncated)
        return Board.view(s, Board.ViewState(), now: F.now, calendar: F.calendar)
    }

    @Test func emptyTextsFollowThePhase() {
        let ready = view(.ready)
        #expect(ready.phase == .ready && ready.isEmpty)
        #expect(ready.emptyTitle == Board.Text.emptyTitle && ready.emptyBody == Board.Text.emptyBody)
        #expect(ready.notice == "")
        var titles = Set<String>()
        for p in [Board.Phase.loading, .preparing, .ready, .unavailable, .off] {
            let v = view(p)
            #expect(v.phase == p && !v.emptyTitle.isEmpty)
            titles.insert(v.emptyTitle)
        }
        #expect(titles.count == 5)
        #expect(view(.loading).emptyBody == "")
    }

    /// The notice says when the cases shown are partial or old.
    @Test func notice() {
        let c = [F.mk("c1")]
        #expect(view(.preparing, cases: c).notice.contains("first time"))
        #expect(view(.unavailable, cases: c).notice.contains("not running"))
        #expect(view(.ready, cases: c).notice == "")
        #expect(view(.ready, cases: c, truncated: true).notice.contains("1,000"))
        #expect(view(.off).notice == "")
    }

    @Test func triageIsCarried() {
        let run = Board.Run(model: "Claude", date: F.ago(1), annotated: 4, running: true)
        let s = Board.Snapshot(run: run, triage: Board.Triage(queue: 7, annotatedToday: 12))
        let v = Board.view(s, Board.ViewState(), now: F.now, calendar: F.calendar)
        #expect(v.triage == Board.Triage(queue: 7, annotatedToday: 12) && v.run == run)
    }
}

@Suite struct BoardFailureTextTests {
    /// What failed, and why when the error says; never the daemon's message.
    @Test func failureTexts() {
        let secret = "SELECT * FROM cases -- internal detail"
        let cases: [(any Error, String)] = [
            (RPCClient.ClientError.notConnected, "Moving the case failed: the mail backend is not running."),
            (RPCClient.ClientError.timeout(method: "board.setState"), "Moving the case failed: the mail backend did not answer in time."),
            (RPCError(code: .caseNotFound, message: secret), "Moving the case failed: the case is no longer on the board."),
            (RPCError(code: .invalidArgument, message: secret), "Moving the case failed: the board did not accept it."),
            (RPCError(code: .methodNotFound, message: secret), "Moving the case failed: this mail backend has no board."),
            (RPCError(code: .internalError, message: secret), "Moving the case failed."),
        ]
        for (err, want) in cases {
            let got = Board.Text.failed(.move, err)
            #expect(got == want)
            #expect(!got.contains("SELECT"))
        }
        var seen = Set<String>()
        for a in Board.Text.Action.allCases {
            #expect(seen.insert(Board.Text.failed(a, RPCError(code: .internalError, message: ""))).inserted)
        }
    }

    @Test func archivedTexts() {
        #expect(Board.Text.archived(1, noArchive: false) == "Archived 1 message.")
        #expect(Board.Text.archived(3, noArchive: false) == "Archived 3 messages.")
        #expect(Board.Text.archived(0, noArchive: false) == "Marked as done. No message was in the inbox.")
        #expect(Board.Text.archived(0, noArchive: true) == "Marked as done. This account has no archive.")
    }
}

@Suite struct BoardFailurePhaseTests {
    /// A board that could not be listed, and a backend without the board,
    /// say so in texts of their own, apart from a backend not running.
    @Test func failurePhasesHaveTheirOwnTexts() {
        let s = { (p: Board.Phase, cases: [Board.Case]) in
            Board.view(
                Board.Snapshot(accounts: F.accounts, cases: cases, phase: p), Board.ViewState(), now: F.now,
                calendar: F.calendar)
        }
        let phases: [Board.Phase] = [.unavailable, .failed, .unsupported]
        #expect(Set(phases.map { s($0, []).emptyBody }).count == 3)
        #expect(Set(phases.map { s($0, [F.mk("c1")]).notice }).count == 3)
        #expect(s(.failed, [F.mk("c1")]).notice.contains("could not be loaded"))
        #expect(s(.unsupported, []).emptyBody.contains("no board"))
        let failures = [Board.Phase.loading, .preparing, .ready, .unavailable, .failed, .unsupported, .off]
            .filter { $0.isFailure }
        #expect(failures == phases)
    }

    /// board.setCommitment's caseNotFound is a promise that is gone.
    @Test func aMissingPromiseIsNamed() {
        let e = RPCError(code: .caseNotFound, message: "x")
        #expect(Board.Text.failed(.commitment, e) == "Changing the promise failed: the promise no longer exists.")
        #expect(Board.Text.failed(.done, e) == "Marking the case done failed: the case is no longer on the board.")
    }
}

/// Text the assistant wrote is marked as the assistant's wherever it shows
/// (docs/api.md §4.13): flags in the view model, "Assistant:" for VoiceOver.
@Suite struct BoardAssistantMarkTests {
    private func annotated(_ title: String, summary: String = "", why: String = "") -> Board.Case {
        F.mk(
            "c1", .you, subject: "Raw subject", snippet: "Raw snippet",
            annotation: Board.Annotation(
                state: nil, title: title, summary: summary, why: why, due: F.day(16, 10), dueQuote: "by tomorrow"))
    }

    @Test func theAssistantsTitleIsMarked() throws {
        let c = annotated("Approve the budget", summary: "Anna asks", why: "She waits")
        let k = Board.Commitment(id: "k1", caseID: c.id, text: "Send it")
        let v = F.view([c], annotated: true, commitments: [k])
        let row = v.sections[0].rows[0]
        #expect(row.title == "Approve the budget" && row.titleIsAssistant)
        #expect(row.snippet == "Anna asks" && row.snippetIsAssistant && row.marksAssistant)
        #expect(row.spoken.contains("Assistant: Approve the budget"))
        let d = try #require(v.detail)
        #expect(d.titleIsAssistant && d.spokenTitle == "Assistant: Approve the budget")
        #expect(d.why == "She waits" && d.whyIsAssistant)
        let due = try #require(v.today.dueGroups.first?.items.first)
        #expect(due.titleIsAssistant && due.spokenTitle == "Assistant: Approve the budget")
        let commitment = try #require(v.commitments.first)
        #expect(commitment.from == "Approve the budget" && commitment.fromIsAssistant)
        #expect(commitment.spokenFrom == "Assistant: Approve the budget")
    }

    /// The subject, the message's snippet and the rules' reason are not
    /// the assistant's.
    @Test func theDaemonsTextIsNot() throws {
        for (c, annotatedOn) in [(annotated(""), true), (annotated("Title", summary: "S", why: "W"), false)] {
            let v = F.view([c], annotated: annotatedOn)
            let row = v.sections[0].rows[0]
            #expect(row.title == "Raw subject" && !row.titleIsAssistant)
            #expect(row.spoken.contains("Raw subject") && !row.spoken.contains("Assistant:"))
            let d = try #require(v.detail)
            #expect(!d.titleIsAssistant && d.spokenTitle == "Raw subject" && !d.whyIsAssistant)
            #expect(!row.snippetIsAssistant && !row.marksAssistant)
        }
        // A summary without a title marks the row, not the title.
        let row = F.view([annotated("", summary: "Anna asks")], annotated: true).sections[0].rows[0]
        #expect(!row.titleIsAssistant && row.snippetIsAssistant && row.marksAssistant)
    }
}
