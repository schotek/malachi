// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// MalachiCore/Calendar/CalendarDetail.swift, the cases of Go detail_test.go.

private typealias F = CalFixture

@Suite struct CalendarDetailTests {
    @Test func timeLine() {
        let env = F.env()
        let cases: [(Occurrence, String)] = [
            (F.timed("a", F.at(2026, 10, 13, 10), F.at(2026, 10, 13, 11, 30)), "Tuesday 13 October 2026 · 10:00–11:30 · 1 h 30 min"),
            (F.timed("a", F.at(2026, 10, 13, 22), F.at(2026, 10, 14)), "Tuesday 13 October 2026 · 22:00–00:00 · 2 h"),
            (F.timed("a", F.at(2026, 10, 13, 10), F.at(2026, 10, 15, 12)), "Tue 13 Oct 2026 10:00–Thu 15 Oct 2026 12:00 · 50 h"),
            (F.allDayOcc("a", F.at(2026, 10, 13), 1), "Tuesday 13 October 2026 · All Day"),
            (F.allDayOcc("a", F.at(2026, 10, 13), 2), "Tuesday 13 October 2026–Wednesday 14 October 2026 · 2 days"),
            // The autumn change: 3 real hours, 2 on the clock.
            (F.timed("a", F.at(2026, 10, 25, 1), F.at(2026, 10, 25, 3)), "Sunday 25 October 2026 · 01:00–03:00 · 3 h"),
            (F.occ(), ""),
        ]
        for (o, want) in cases {
            #expect(Cal.timeLine(o, env: env) == want)
        }
    }

    @Test func durationText() {
        let cases: [(TimeInterval, String)] = [
            (0, "0 min"), (30, "0 min"), (60, "1 min"), (45 * 60, "45 min"), (3600, "1 h"), (90 * 60, "1 h 30 min"),
            (24 * 3600, "1 day"), (25 * 3600, "25 h"), (48 * 3600, "2 days"), (-3600, "0 min"),
        ]
        for (d, want) in cases {
            #expect(Cal.durationText(d) == want, "\(d)")
        }
    }

    @Test func responseLabels() {
        let cases: [(EventResponse?, String)] = [
            (.accepted, "Accepted"), (.tentative, "Tentative"), (.declined, "Declined"), (.needsAction, "No Reply"),
            (.delegated, "Delegated"), ("x", "No Reply"), (nil, "No Reply"),
        ]
        for (r, want) in cases {
            #expect(Cal.responseLabel(r) == want)
        }
        #expect(Cal.myResponseText(.accepted) == "You: Accepted")
        #expect(Cal.responseAction(.accepted) == "Accept" && Cal.responseAction(.declined) == "Decline"
            && Cal.responseAction(.tentative) == "Tentative")
    }

    @Test func detailRules() {
        var attendee = F.event()
        attendee.title = "Planning"
        attendee.myResponse = .accepted
        attendee.attendeeCount = 4
        attendee.canRespond = true
        attendee.canDelete = true
        attendee.organizerAddress = Address(name: "Jana Nováková", address: "jana@example.org")
        func with(_ e: Event, _ f: (inout Event) -> Void) -> Event {
            var c = e
            f(&c)
            return c
        }
        var mine = F.event()
        mine.title = "Mine"
        mine.organizer = true
        mine.attendeeCount = 1
        mine.removeIsDelete = true
        mine.canRespond = true
        mine.invitationMessages = [MessageRef(accountId: "", messageId: "")]
        var own = F.event()
        own.title = "Dentist"
        own.removeIsDelete = true
        var noOrg = F.event()
        noOrg.attendeeCount = 2
        let org = "Organiser Jana Nováková · 4 attendees"
        let cases: [(String, Event, Bool, String, Bool, String, String, Bool)] = [
            ("attendee", attendee, true, "Remove from Calendar", true, "Also tell the organiser you won’t attend?", org, false),
            ("declined attendee", with(attendee) { $0.myResponse = .declined }, true, "Remove from Calendar", false, "", org, false),
            ("cancelled", with(attendee) { $0.status = .cancelled }, false, "Remove from Calendar", false, "", org, false),
            ("daemon says no", with(attendee) { $0.canRespond = false }, false, "Remove from Calendar", false, "", org, false),
            ("organiser with attendees", mine, false, "Delete", true, "Delete the Event?", "You organise · 1 attendee", true),
            ("own event", own, false, "Delete", false, "", "", false),
            ("no organiser address", noOrg, false, "Remove from Calendar", false, "", "2 attendees", false),
        ]
        for (name, e, respond, remove, ask, heading, people, invitation) in cases {
            #expect(Cal.canRespond(e) == respond, "\(name)")
            #expect(Cal.removeTitle(e) == remove, "\(name)")
            let q = Cal.removeQuestion(e)
            #expect((q != nil) == ask && (q?.heading ?? "") == heading, "\(name)")
            #expect(Cal.peopleLine(e) == people, "\(name)")
            #expect(Cal.showsInvitation(e) == invitation, "\(name)")
        }
        let q = Cal.removeQuestion(attendee)
        #expect(q?.confirm == "Remove and Decline" && q?.alternative == "Remove Only"
            && q?.body == "“Planning” will be removed from your calendar.")
        let d = Cal.deleteQuestion(F.event())
        #expect(d.heading == "Remove “Untitled Event”?" && d.confirm == "Remove from Calendar")
    }

    @Test func detailOf() {
        let env = F.env()
        let s = Cal.sampleData(now: F.at(2026, 10, 14, 12), zone: F.prague)
        var planning = s.events.first { $0.eventId == "ev_sample_planning" }!
        planning.attendees! += [planning.attendees![3], planning.attendees![3], planning.attendees![3]]
        let d = Cal.detailOf(planning, accountName: "Work", calendarName: "Calendar", env: env)
        #expect(d.calendarLine == "Work · Calendar" && d.title == "Q4 planning")
        #expect(d.timeLine == "Tuesday 13 October 2026 · 10:00–11:30 · 1 h 30 min" && d.videoHost == "teams.microsoft.com")
        #expect(d.peopleLine == "Organiser Jana Nováková · 7 attendees" && d.collapsed && d.canRespond && !d.canEdit)
        #expect(d.removeTitle == "Remove from Calendar" && d.showInvitation && d.attendees[0].responseText == "Accepted")
        #expect(d.attendees[0].organizer && d.attendees[3].optional && d.recurrence == "")
        let standup = Cal.detailOf(s.events[0], accountName: "Work", calendarName: "", env: env)
        #expect(standup.recurrence == "Every weekday" && standup.calendarLine == "Work" && standup.peopleLine == "")
    }
}
