// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// MalachiCore/Calendar/CalendarInvitation.swift, the cases of Go
// invitation_test.go.

private typealias F = CalFixture

private struct CardRow: Equatable {
    var mode: Cal.CardMode
    var responses, strip, propose: Bool
    var state, reply, conflict: String
}

@Suite struct CalendarInvitationTests {
    @Test func pillKind() {
        let kinds: [(InvitationKind, Cal.InvitationPill)] = [
            (.invitation, .invitation), (.update, .update), (.cancelled, .cancelled), (.reply, .reply), (.other, .none),
            ("future", .none),
        ]
        let methods: [InvitationMethod] = [.request, .reply, .cancel, .counter, .declineCounter, .refresh, .add, .publish, .other]
        for (kind, want) in kinds {
            for st in Cal.invitationStates {
                for m in methods {
                    let got = Cal.pillKind(InvitationSummary(method: m, kind: kind, state: st, title: ""))
                    #expect(got == (st == .invalid ? .none : want), "\(m) \(kind) \(st)")
                }
            }
        }
        #expect(Cal.pillKind(nil) == .none)
        let titles: [Cal.InvitationPill: String] = [.none: "", .invitation: "Invitation", .update: "Update", .cancelled: "Cancelled", .reply: "Reply"]
        for (p, want) in titles {
            #expect(Cal.pillTitle(p) == want)
            #expect(Cal.strikesSubject(p) == (p == .cancelled))
        }
    }

    @Test func sampleCards() {
        let env = F.env()
        let s = Cal.sampleData(now: F.at(2026, 10, 14, 12), zone: F.prague)
        let conflict = "Conflicts with Standup (15:30–16:00) in calendar Calendar"
        let want: [InvitationState: CardRow] = [
            .unanswered: CardRow(mode: .full, responses: true, strip: false, propose: false, state: "", reply: "Petr Svoboda replied", conflict: conflict),
            .answered: CardRow(mode: .full, responses: false, strip: true, propose: false, state: "", reply: "Petr Svoboda accepted", conflict: conflict),
            .outdated: CardRow(
                mode: .full, responses: false, strip: false, propose: false,
                state: "This invitation is out of date: a newer one arrived, or the event is over.", reply: "Petr Svoboda replied",
                conflict: conflict),
            .cancelled: CardRow(
                mode: .cancelled, responses: false, strip: false, propose: false, state: "This event was cancelled.",
                reply: "Petr Svoboda replied", conflict: conflict),
            .processed: CardRow(
                mode: .reply, responses: false, strip: false, propose: false, state: "",
                reply: "Jana Nováková accepted (applied to your event)", conflict: ""),
            .invalid: CardRow(
                mode: .none, responses: false, strip: false, propose: false, state: "The invitation in this message could not be read.",
                reply: " replied", conflict: ""),
        ]
        for c in s.cards {
            let got = CardRow(
                mode: Cal.cardModeOf(c, cardOn: true), responses: Cal.showsResponses(c), strip: Cal.showsStateStrip(c),
                propose: Cal.showsProposeNewTime(c), state: Cal.stateLine(c), reply: Cal.replyCard(c),
                conflict: Cal.conflictLine(c.conflicts, calendarName: s.calendarName, env: env))
            #expect(got == want[c.state], "\(c.state)")
            #expect(Cal.cardModeOf(c, cardOn: false) == .none)
        }
        let c = s.cards[0]
        #expect(Cal.dateTileOf(c.summary, env: env) == Cal.DateTile(weekday: "Fri", day: "16", month: "Oct"))
        #expect(Cal.cardTimeLine(c, env: env) == "Friday 16 October 2026 · 15:00–16:00 · 1 h")
        #expect(Cal.cardPeopleLine(c) == "Organiser Petr Svoboda · 3 attendees")
        #expect(Cal.replyNote(c, accountName: "Gmail") == ["The reply leaves from account Gmail.", "The reply goes to Petr Svoboda <petr@example.org>."])
        #expect(Cal.stateStrip(s.cards[1], calendarName: "Personal") == "Accepted · in calendar Personal")
        #expect(Cal.previousLines(s.cards[2], env: env) == ["Previously Friday 16 October 2026 · 14:00–15:00 · 1 h"])
        #expect(Cal.cardModeOf(nil, cardOn: true) == .none)
        var other = c
        other.kind = .other
        #expect(Cal.cardModeOf(other, cardOn: true) == .none)
        var proposing = c
        proposing.canProposeNewTime = true
        #expect(Cal.showsProposeNewTime(proposing))
    }

    @Test func invitationLines() {
        let env = F.env()
        let s = F.at(2026, 10, 14, 10)
        let e = F.at(2026, 10, 14, 10, 30)
        let name: (CalendarID) -> String = { _ in "Work · Calendar" }
        func conflict(_ title: String?, _ s: Date, _ e: Date) -> InvitationConflict {
            InvitationConflict(title: title, start: s, end: e, calendarId: "", eventId: "")
        }
        let zero = Date(timeIntervalSince1970: 0)
        let cases: [([InvitationConflict], String)] = [
            ([], ""),
            ([conflict("Standup", s, e)], "Conflicts with Standup (10:00–10:30) in calendar Work · Calendar"),
            ([conflict(nil, s, e)], "Conflicts with a private event (10:00–10:30) in calendar Work · Calendar"),
            ([conflict(nil, zero, zero), conflict(nil, zero, zero), conflict(nil, zero, zero)], "Conflicts with 3 events"),
        ]
        for (c, want) in cases {
            #expect(Cal.conflictLine(c, calendarName: name, env: env) == want)
        }
        let org = Address(address: "jana@example.org")
        var viaServer = F.card()
        viaServer.replyVia = .server
        viaServer.replyTo = org
        var viaNone = F.card()
        viaNone.replyVia = .none
        var mismatch = F.card()
        mismatch.replyVia = .mail
        mismatch.organizerMismatch = true
        #expect(Cal.replyNote(viaServer, accountName: "") == ["The reply goes to jana@example.org."])
        #expect(Cal.replyNote(viaNone, accountName: "") == ["No reply will be sent."])
        #expect(Cal.replyNote(mismatch, accountName: "Work") == [
            "The reply leaves from account Work.",
            "The organiser’s address is not from the sender’s domain. Check the invitation before you answer.",
        ])
        let strips: [(EventResponse, String, String)] = [
            (.accepted, "Work", "Accepted · in calendar Work"), (.tentative, "", "Tentative"), (.declined, "Work", "Declined"),
        ]
        for (r, cal, want) in strips {
            var c = F.card()
            c.response = r
            #expect(Cal.stateStrip(c, calendarName: cal) == want)
        }
        let jana = Address(name: "Jana", address: "jana@example.org")
        let replies: [(EventResponse, String)] = [
            (.accepted, "Jana accepted"), (.tentative, "Jana tentatively accepted"), (.declined, "Jana declined"),
            (.delegated, "Jana replied"),
        ]
        for (r, want) in replies {
            var c = F.card()
            c.replyFrom = jana
            c.response = r
            #expect(Cal.replyCard(c) == want)
        }
        var allDay = F.card(InvitationSummary(method: "", kind: "", state: "", title: "", startDate: "2026-10-15", allDay: true))
        allDay.endDate = "2026-10-17"
        allDay.previous = InvitationPrevious(start: .date("2026-10-14"), end: .date("2026-10-15"))
        #expect(Cal.cardTimeLine(allDay, env: env) == "Thursday 15 October 2026–Friday 16 October 2026 · 2 days")
        #expect(Cal.previousLines(allDay, env: env) == ["Previously Wednesday 14 October 2026 · All Day"])
        // Go's "garbage": neither an instant nor a date (DateOrTime cannot
        // hold such a string, so a date that does not exist stands for it).
        var bad = F.card()
        bad.previous = InvitationPrevious(start: .date("2026-02-30"), end: .date(""))
        #expect(Cal.previousLines(bad, env: env).isEmpty)
        #expect(Cal.dateTileOf(InvitationSummary(method: "", kind: "", state: "", title: ""), env: env) == nil)
        let unanswerable = F.card(InvitationSummary(method: "", kind: "", state: .unanswered, title: ""))
        #expect(Cal.stateLine(unanswerable) == "This invitation cannot be answered from here." && !Cal.showsResponses(unanswerable))
    }

    @Test func previousLines() {
        let env = F.env()
        let s = F.at(2026, 10, 20, 10)
        let e = F.at(2026, 10, 20, 11)
        let weekly = Recurrence(frequency: .weekly, interval: 1, until: "2026-10-27")
        func card(_ prev: InvitationPrevious) -> InvitationCard {
            var c = F.card(InvitationSummary(method: "", kind: "", state: "", title: "", start: s, end: e))
            c.previous = prev
            return c
        }
        let when = InvitationPrevious(start: .time(F.at(2026, 10, 13, 9)), end: .time(F.at(2026, 10, 13, 10)))
        let timeLine = "Previously Tuesday 13 October 2026 · 09:00–10:00 · 1 h"
        let rule = "Previously repeated: Every week on Tuesday, until 27 October 2026"
        let cases: [(String, InvitationPrevious, [String])] = [
            ("time", when, [timeLine]),
            ("location", InvitationPrevious(location: "Room B"), ["Previously at Room B"]),
            ("recurrence", InvitationPrevious(recurrence: weekly), [rule]),
            ("all three", InvitationPrevious(start: when.start, end: when.end, location: " Room\nB ", recurrence: weekly),
             [timeLine, "Previously at Room B", rule]),
            ("empty location", InvitationPrevious(location: "\u{200B} "), []),
            ("nothing", InvitationPrevious(), []),
        ]
        for (name, prev, want) in cases {
            #expect(Cal.previousLines(card(prev), env: env) == want, "\(name)")
        }
        #expect(Cal.previousLines(F.card(), env: env).isEmpty)
        // Without a start the rule has no day to describe.
        var bare = F.card()
        bare.previous = InvitationPrevious(recurrence: weekly)
        #expect(Cal.previousLines(bare, env: env).isEmpty)
        #expect(Cal.previousLine(card(InvitationPrevious(start: when.start, end: when.end, location: "B")), env: env)
            == timeLine + "\nPreviously at B")
    }

    /// An answer given in another client arrives without respondedAt: the
    /// strip still shows the answer and the calendar.
    @Test func stateStripWithoutRespondedAt() {
        var c = F.card(InvitationSummary(method: .request, kind: .invitation, state: .answered, title: ""))
        c.response = .tentative
        c.respondedAt = nil
        #expect(Cal.showsStateStrip(c) && Cal.stateStrip(c, calendarName: "Work") == "Tentative · in calendar Work")
    }

    @Test func ownEvent() {
        var own = F.card(InvitationSummary(method: .request, kind: .invitation, state: .processed, title: "", ownEvent: true))
        own.eventId = "ev_1"
        #expect(Cal.stateLine(own) == "You organise this event.")
        #expect(Cal.cardModeOf(own, cardOn: true) == .full && !Cal.showsResponses(own) && Cal.showsInCalendar(own))
        var update = own
        update.kind = .update
        update.state = .outdated
        #expect(Cal.stateLine(update) == "You organise this event.")
        var reply = own
        reply.kind = .reply
        #expect(Cal.stateLine(reply) != "You organise this event.")
        var notStored = own
        notStored.eventId = nil
        var answeredLinked = F.card(InvitationSummary(method: "", kind: "", state: .answered, title: ""))
        answeredLinked.eventId = "e"
        let answered = F.card(InvitationSummary(method: "", kind: "", state: .answered, title: ""))
        var unansweredLinked = F.card(InvitationSummary(method: "", kind: "", state: .unanswered, title: ""))
        unansweredLinked.eventId = "e"
        let cases: [(String, InvitationCard, Bool)] = [
            ("own, linked", own, true), ("own, not stored", notStored, false), ("answered, linked", answeredLinked, true),
            ("answered, not linked", answered, false), ("unanswered, linked", unansweredLinked, false),
        ]
        for (name, c, shows) in cases {
            #expect(Cal.showsInCalendar(c) == shows, "\(name)")
        }
    }
}
