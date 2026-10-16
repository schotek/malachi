// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// What Mail shows of a message with a calendar part (InvitationReading.swift):
// the row's pill, whether a card shows, the clock line with the repetition
// and the mini day strip's geometry, also for hostile spans.

@MainActor
private func card(_ state: InvitationState) -> InvitationCard {
    Cal.sampleData(now: calNow, zone: calZone).cards[Cal.invitationStates.firstIndex(of: state)!]
}

private func summary(_ inv: InvitationSummary? = nil) -> MessageSummary {
    MessageSummary(
        id: "m1", accountId: "a", folderId: "f", from: [], subject: "Vendor demo", date: .goZero, snippet: "",
        flags: [], hasAttachments: false, size: 0, invitation: inv)
}

@MainActor
@Suite struct InvitationReadingTests {
    @Test func pillOfARow() {
        #expect(invitationPill(summary()) == .none)
        let kinds: [(InvitationKind, Cal.InvitationPill)] = [
            (.invitation, .invitation), (.update, .update), (.cancelled, .cancelled), (.reply, .reply), (.other, .none),
        ]
        for (k, want) in kinds {
            #expect(invitationPill(summary(InvitationSummary(method: .request, kind: k, state: .unanswered, title: ""))) == want)
            #expect(invitationPill(summary(InvitationSummary(method: .request, kind: k, state: .invalid, title: ""))) == .none)
        }
    }

    @Test func cardOnlyFromMessageGet() {
        let c = card(.unanswered)
        #expect(invitationReading(nil, cardOn: true) == nil)
        #expect(invitationReading(Message(summary: summary()), cardOn: true) == nil)
        #expect(invitationReading(Message(summary: summary(), invitation: c), cardOn: true) == c)
        #expect(invitationReading(Message(summary: summary(), invitation: c), cardOn: false) == nil, "the account's switch")
        #expect(invitationReading(Message(summary: summary(), invitation: card(.invalid)), cardOn: true) == nil)
        #expect(invitationReading(Message(summary: summary(), invitation: card(.processed)), cardOn: true) != nil, "a reply")
        #expect(invitationReading(Message(summary: summary(), invitation: card(.cancelled)), cardOn: true) != nil)
    }

    @Test func accountSwitch() {
        #expect(showsInvitationCard(nil))
    }

    @Test func clockLineWithTheRepetition() {
        var c = card(.unanswered)
        let plain = Cal.cardTimeLine(c, env: calEnv)
        #expect(!plain.isEmpty)
        #expect(Cal.cardClockLine(c, env: calEnv) == plain)
        c.recurrence = Recurrence(frequency: .weekly, interval: 2)
        let line = Cal.cardClockLine(c, env: calEnv)
        #expect(line.hasPrefix(plain + " · ") && line.count > plain.count + 3, "\(line)")
    }

    @Test func dayStripOfTheSample() throws {
        let c = card(.unanswered)
        let s = try #require(Cal.dayStripOf(c, env: calEnv))
        // 8:00–17:00: ten whole hours, both ends.
        #expect(s.ticks.count == 10)
        #expect(s.ticks.first == Cal.DayStrip.Tick(at: 0, label: "08:00"))
        #expect(s.ticks.last == Cal.DayStrip.Tick(at: 1, label: "17:00"))
        #expect(s.busy.count == 2)
        #expect(abs(s.busy[0].from - 1.0 / 9) < 1e-9 && abs(s.busy[0].to - 1.25 / 9) < 1e-9)
        let p = try #require(s.proposed)
        #expect(abs(p.from - 7.0 / 9) < 1e-9 && abs(p.to - 8.0 / 9) < 1e-9)
        #expect(s.proposedLabel == "15:00–16:00")
    }

    @Test func dayStripOfHostileSpans() {
        var c = card(.unanswered)
        #expect(Cal.dayStripOf(card(.invalid), env: calEnv) == nil, "the zero span of an unreadable part")
        c.day = InvitationDay(from: c.day.to, to: c.day.from)
        #expect(Cal.dayStripOf(c, env: calEnv) == nil, "backwards")
        c.day = InvitationDay(from: calNow, to: calNow.addingTimeInterval(30 * 86400))
        #expect(Cal.dayStripOf(c, env: calEnv) == nil, "not a day")

        // Busy spans outside, backwards or many: clipped, dropped, capped.
        let from = calNow
        let to = calNow.addingTimeInterval(10 * 3600)
        var busy = [
            DayBusy(start: from.addingTimeInterval(-7200), end: from.addingTimeInterval(1800)),
            DayBusy(start: to.addingTimeInterval(3600), end: to.addingTimeInterval(7200)),
            DayBusy(start: from.addingTimeInterval(3600), end: from.addingTimeInterval(1800)),
        ]
        busy += (0..<500).map { DayBusy(start: from.addingTimeInterval(Double($0) * 60), end: from.addingTimeInterval(Double($0) * 60 + 30)) }
        c.day = InvitationDay(from: from, to: to, busy: busy)
        c.start = from.addingTimeInterval(-3600)
        c.end = from.addingTimeInterval(-60)
        let s = Cal.dayStripOf(c, env: calEnv)
        #expect(s != nil)
        #expect((s?.busy.count ?? 0) <= Cal.DayStrip.maxBusy)
        #expect(s?.busy.allSatisfy { $0.from >= 0 && $0.to <= 1 && $0.from < $0.to } == true)
        #expect(s?.busy.first == Cal.DayStrip.Span(from: 0, to: 0.05), "clipped at the start")
        #expect(s?.proposed == nil && s?.proposedLabel == "", "a slot off the axis")
        #expect((s?.ticks.count ?? 0) <= Cal.DayStrip.maxTicks)

        // A long axis labels fewer hours.
        c.day = InvitationDay(from: from, to: from.addingTimeInterval(40 * 3600))
        #expect((Cal.dayStripOf(c, env: calEnv)?.ticks.count ?? 99) <= Cal.DayStrip.maxTicks)
    }
}

// An answer given in another client (Outlook, a phone): the daemon sends
// the response and the linked event without respondedAt.
@MainActor
@Suite struct InvitationAnsweredElsewhereTests {
    @Test func rowKeepsItsPill() {
        let s = summary(InvitationSummary(method: .request, kind: .invitation, state: .answered, title: ""))
        #expect(invitationPill(s) == .invitation, "the state shows in Unanswered, not in the pill")
    }

    @Test func cardShowsTheStateStrip() {
        var c = card(.unanswered)
        c.state = .answered
        c.response = .accepted
        c.respondedAt = nil
        c.eventId = "ev_sample_demo"
        c.calendarId = Cal.samplePersonal
        let ctl = InvitationController(
            source: calSamples(), accountId: Cal.sampleGmail, messageId: "msg_sample_demo", card: c, env: calEnv)
        #expect(ctl.mode == .full)
        #expect(ctl.showsStateStrip && !ctl.showsResponses, "the strip, no Add to: row")
        #expect(ctl.stateStrip.hasPrefix("Accepted · in calendar "))
    }
}

// The user's own request (`ownEvent`): no answer, the state line with Show
// in Calendar when the event is stored.
@MainActor
@Suite struct InvitationOwnEventTests {
    @Test func ownEventCard() {
        var c = card(.unanswered)
        c.kind = .update
        c.state = .processed
        c.ownEvent = true
        c.canRespond = false
        c.targets = []
        c.eventId = "ev_sample_demo"
        let ctl = InvitationController(
            source: calSamples(), accountId: Cal.sampleGmail, messageId: "msg_sample_demo", card: c, env: calEnv)
        #expect(ctl.mode == .full && ctl.pill == .update, "the pill as the daemon's kind says")
        #expect(!ctl.showsResponses && !ctl.showsStateStrip && !ctl.showsProposeNewTime)
        #expect(ctl.stateLine == "You organise this event.")
        #expect(Cal.showsInCalendar(c))
        c.eventId = nil
        #expect(!Cal.showsInCalendar(c), "not stored: no Show in Calendar")
    }
}
