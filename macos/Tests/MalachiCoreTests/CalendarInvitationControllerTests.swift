// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The invitation card's controller (InvitationController) over the sample
// cards: the card mode per state and switch, the lines, the target
// calendar, the answer with the target, Decline Without Reply, Propose New
// Time, a refusal inline, the cancellation's Remove from Calendar and Show
// in Calendar.

@MainActor
private func card(_ state: InvitationState) -> InvitationCard {
    Cal.sampleData(now: calNow, zone: calZone).cards[Cal.invitationStates.firstIndex(of: state)!]
}

@MainActor
private func invitation(_ src: InMemoryCalendarSource, _ state: InvitationState, cardOn: Bool = true) -> InvitationController {
    InvitationController(
        source: src, accountId: Cal.sampleGmail, messageId: "msg_sample_demo", card: card(state), cardOn: cardOn, env: calEnv)
}

@MainActor
@Suite(.serialized) struct CalendarInvitationControllerTests {
    @Test func modesPerState() {
        let src = calSamples()
        #expect(invitation(src, .unanswered).mode == .full)
        #expect(invitation(src, .answered).mode == .full)
        #expect(invitation(src, .cancelled).mode == .cancelled)
        #expect(invitation(src, .processed).mode == .reply)
        #expect(invitation(src, .invalid).mode == .none)
        #expect(invitation(src, .unanswered, cardOn: false).mode == .none)
    }

    @Test func linesOfAnUnansweredCard() {
        let c = invitation(calSamples(), .unanswered)
        #expect(c.title == "Vendor demo" && c.pillTitle == Cal.pillTitle(.invitation))
        #expect(c.dateTile == Cal.DateTile(weekday: "Fri", day: "16", month: "Oct"))
        #expect(c.showsResponses && !c.showsStateStrip && !c.showsProposeNewTime)
        #expect(c.conflictLine.hasPrefix("Conflicts with Standup"))
        #expect(c.target == Cal.samplePersonal && c.targetIsPopup)
        #expect(c.targets.map(\.title) == ["Gmail · Personal", "Gmail · Family"])
        #expect(c.replyNote.contains("The reply leaves from account Gmail."))
        #expect(c.stateLine.isEmpty)
    }

    @Test func answerWithTheTarget() async throws {
        let src = calSamples()
        let c = invitation(src, .unanswered)
        c.setTarget(Cal.sampleFamily)
        #expect(c.target == Cal.sampleFamily)
        c.setTarget("cal_unknown")
        #expect(c.target == Cal.sampleFamily)
        c.respond(.accepted)
        #expect(c.busy)
        try await calWaitUntil { !c.busy }
        guard case .invitationRespond(let p) = src.writes.last else {
            Issue.record("no respond")
            return
        }
        #expect(p.accountId == Cal.sampleGmail && p.messageId == "msg_sample_demo")
        #expect(p.response == .accepted && p.calendarId == Cal.sampleFamily && p.sendResponse == nil)
        #expect(p.mail?.subject == "Accepted: Vendor demo")
        #expect(c.card.state == .answered && c.showsStateStrip)
        #expect(c.stateStrip == "Accepted · in calendar Gmail · Family")
    }

    @Test func declineWithoutReplySendsNothing() async throws {
        let src = calSamples()
        let c = invitation(src, .unanswered)
        c.respond(.declined, withoutReply: true)
        try await calWaitUntil { !c.busy }
        guard case .invitationRespond(let p) = src.writes.last else {
            Issue.record("no respond")
            return
        }
        #expect(p.calendarId == nil && p.sendResponse == false && p.mail == nil)
    }

    @Test func proposeNewTimeNeedsTheFlag() async throws {
        let src = calSamples()
        var data = card(.unanswered)
        let start = Cal.date(2026, 10, 16, 13, 0, in: calZone)
        let c = invitation(src, .unanswered)
        c.proposeNewTime(start: start, end: start.addingTimeInterval(3600))
        #expect(src.writes.isEmpty)
        data.canProposeNewTime = true
        let p = InvitationController(source: src, accountId: Cal.sampleGmail, messageId: "m", card: data, env: calEnv)
        #expect(p.showsProposeNewTime)
        p.proposeNewTime(start: start, end: start.addingTimeInterval(3600))
        try await calWaitUntil { !p.busy }
        guard case .invitationRespond(let q) = src.writes.last else {
            Issue.record("no respond")
            return
        }
        #expect(q.response == .tentative && q.proposedStart == start && q.proposedEnd == start.addingTimeInterval(3600))
    }

    @Test func refusalShowsInline() async throws {
        let src = calSamples()
        src.failures[API.InvitationRespond.name] = RPCError(code: .calendarConsentRequired, message: "consent")
        let c = invitation(src, .unanswered)
        c.respond(.accepted)
        try await calWaitUntil { !c.busy }
        #expect(c.error == Cal.Text.consentLine && c.card.state == .unanswered)
        let preview = invitation(calSamples(writes: false), .unanswered)
        preview.respond(.accepted)
        try await calWaitUntil { !preview.busy }
        #expect(preview.error == Cal.Text.previewOnly)
    }

    @Test func cancellationAndShowInCalendar() async throws {
        let src = calSamples()
        let c = invitation(src, .cancelled)
        c.applyCancel()
        try await calWaitUntil { !c.busy }
        #expect(c.cancelApplied)
        #expect(src.writes == [.applyCancel(InvitationApplyCancelParams(accountId: Cal.sampleGmail, messageId: "msg_sample_demo"))])
        var shown: [(EventID?, Date?)] = []
        c.onShowInCalendar = { shown.append(($0, $1)) }
        c.showInCalendar()
        #expect(shown.first?.0 == "ev_sample_demo" && shown.first?.1 == c.card.start)
        // A reply's one line.
        #expect(invitation(src, .processed).replyCard == "Jana Nováková accepted (applied to your event)")
    }
}
