// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// MalachiCore/Calendar/CalendarEditor.swift, the cases of Go editor_test.go.

private typealias F = CalFixture

@Suite struct CalendarEditorTests {
    private let p = F.prague

    @Test func newEventDefaults() {
        let prefs = Cal.defaultPreferences
        var noAlert = prefs
        noAlert.defaultAlertMinutes = -1
        noAlert.defaultDurationMinutes = 30
        var badDuration = prefs
        badDuration.defaultDurationMinutes = 0
        let cases: [(String, Date, Cal.Origin, CalendarPreferences, Date, Date, Bool, [Int])] = [
            ("next half hour", F.at(2026, 10, 14, 10, 7), .now, prefs, F.at(2026, 10, 14, 10, 30), F.at(2026, 10, 14, 11, 30), false, [15]),
            ("an exact half hour stays", F.at(2026, 10, 14, 10, 30), .now, prefs, F.at(2026, 10, 14, 10, 30), F.at(2026, 10, 14, 11, 30), false, [15]),
            ("seconds past", Cal.date(2026, 10, 14, 10, 30, 1, in: p), .now, prefs, F.at(2026, 10, 14, 11), F.at(2026, 10, 14, 12), false, [15]),
            ("into tomorrow", F.at(2026, 10, 14, 23, 45), .now, noAlert, F.at(2026, 10, 15), F.at(2026, 10, 15, 0, 30), false, []),
            ("spring gap", F.at(2026, 3, 29, 1, 45), .now, prefs, F.at(2026, 3, 29, 3), F.at(2026, 3, 29, 4), false, [15]),
            ("slot", F.at(2026, 10, 14, 14, 40), .slot, prefs, F.at(2026, 10, 14, 14, 30), F.at(2026, 10, 14, 15, 30), false, [15]),
            ("month cell", F.at(2026, 10, 20), .monthCell, badDuration, F.at(2026, 10, 20, 9), F.at(2026, 10, 20, 10), false, [15]),
            ("all-day row", F.at(2026, 10, 25, 13), .allDay, prefs, F.at(2026, 10, 25), F.at(2026, 10, 26), true, [900]),
            ("all-day row without an alert", F.at(2026, 10, 25, 13), .allDay, noAlert, F.at(2026, 10, 25), F.at(2026, 10, 26), true, []),
        ]
        for (name, at, origin, pr, start, end, allDay, alerts) in cases {
            let d = Cal.newEventDefaults(at: at, origin: origin, preferences: pr, zone: p)
            #expect(d.start == start && d.end == end && d.allDay == allDay && d.alerts == alerts, "\(name)")
        }
    }

    @Test func validate() {
        let s = F.at(2026, 10, 14, 10)
        let ok = Cal.Input(
            title: "T", start: s, end: s.addingTimeInterval(3600), zone: p,
            attendees: [EventInputAttendee(address: "jana@example.org")], alerts: [15], calendarId: "cal")
        func with(_ f: (inout Cal.Input) -> Void) -> Cal.Input {
            var c = ok
            f(&c)
            return c
        }
        let cases: [(String, Cal.Input, [Cal.Problem])] = [
            ("valid", ok, []),
            ("no calendar", with { $0.calendarId = nil }, [.noCalendar]),
            ("end at start", with { $0.end = $0.start }, [.endBeforeStart]),
            ("end before start", with { $0.end = $0.start.addingTimeInterval(-60) }, [.endBeforeStart]),
            ("all-day one day", with { $0.allDay = true; $0.end = Cal.addDays(Cal.startOfDay($0.start, F.prague), 1, F.prague) }, []),
            ("all-day same day", with { $0.allDay = true; $0.end = $0.start }, [.endBeforeStart]),
            ("long title", with { $0.title = String(repeating: "x", count: API.Limits.maxEventTitleBytes + 1) }, [.titleTooLong]),
            ("title of spaces", with { $0.title = String(repeating: " ", count: API.Limits.maxEventTitleBytes + 10) }, []),
            ("long location", with { $0.location = String(repeating: "x", count: API.Limits.maxEventLocationBytes + 1) }, [.locationTooLong]),
            ("long notes", with { $0.notes = String(repeating: "x", count: API.Limits.maxEventNotesBytes + 1) }, [.notesTooLong]),
            ("bad attendee", with { $0.attendees = [EventInputAttendee(address: "jana"), EventInputAttendee(address: "x y@z")] }, [.invalidAttendee]),
            ("too many attendees", with { c in
                c.attendees = (0...API.Limits.maxEventAttendeesIn).map { EventInputAttendee(address: "a\($0)@example.org") }
            }, [.tooManyAttendees]),
            ("too many alerts", with { $0.alerts = [1, 2, 3, 4, 5] }, [.tooManyAlerts]),
            ("several", with { $0.calendarId = nil; $0.end = $0.start }, [.noCalendar, .endBeforeStart]),
        ]
        for (name, input, want) in cases {
            #expect(Cal.validate(input) == want, "\(name)")
        }
        #expect(Cal.problemText(.tooManyAttendees) == "An event can have at most 100 attendees.")
        #expect(Cal.problemText(.tooManyAlerts) == "An event can have at most 4 alerts.")
    }

    /// Go's mail.ParseAddress with Address equal to the input.
    @Test func bareAddresses() {
        for good in ["jana@example.org", "a.b+c@sub.example.org", "žluť@example.cz", "x@[127.0.0.1]", "o'brien@example.ie"] {
            #expect(Cal.isBareAddress(good), "\(good)")
        }
        for bad in ["jana", "x y@z", " a@b", "a@b ", ".a@b", "a.@b", "a..b@c", "a@", "@b", "\"a\"@b", "Jana <j@x.org>", "a@b@c",
                    "a@b.", "a@[x"] {
            #expect(!Cal.isBareAddress(bad), "\(bad)")
        }
    }

    @Test func eventInput() {
        var input = Cal.Input(title: "  Planning ", start: F.at(2026, 10, 14, 10), end: F.at(2026, 10, 14, 11, 30), zone: p, location: " B ")
        var got = Cal.eventInput(input, zone: p)
        #expect(got.title == "Planning" && got.start == "2026-10-14T10:00:00" && got.end == "2026-10-14T11:30:00")
        #expect(got.timeZone == "Europe/Prague" && got.location == "B" && got.alerts == [] && got.startDate == nil)
        input.allDay = true
        input.end = F.at(2026, 10, 16)
        got = Cal.eventInput(input, zone: p)
        #expect(got.startDate == "2026-10-14" && got.endDate == "2026-10-16" && got.start == nil && got.timeZone == nil)
        input.end = input.start
        #expect(Cal.eventInput(input, zone: p).endDate == "2026-10-15")
    }

    @Test func primaryLabelAndFooter() {
        func features(_ v: InvitationsVia) -> CalendarFeatures {
            CalendarFeatures(freeBusy: false, proposeNewTime: false, invitations: v, onlineMeetings: false)
        }
        let mail = features(.mail)
        let none = features(.none)
        let server = features(.server)
        let cases: [(Int, CalendarSource, CalendarFeatures, String)] = [
            (0, .graph, server, "Save"),
            (2, .graph, none, "Save and Send Invitations"),
            (2, .caldav, mail, "Save and Send Invitations"),
            (2, .caldav, server, "Save and Send Invitations"),
            (2, .caldav, none, "Save"),
        ]
        for (n, source, f, want) in cases {
            #expect(Cal.primaryLabel(attendees: n, source: source, features: f) == want)
        }
        #expect(Cal.footer(attendees: 1, features: mail, mailAccount: "Gmail", mailAddress: "me@example.com", attachments: ["agenda.pdf"])
            == ["Invitations leave by e-mail from account Gmail (me@example.com) through the Outbox.", "Attachment: agenda.pdf"])
        #expect(Cal.footer(attendees: 1, features: server, mailAccount: "", mailAddress: "", attachments: [])
            == ["The server sends the invitations."])
        #expect(Cal.footer(attendees: 0, features: mail, mailAccount: "Gmail", mailAddress: "x", attachments: []).isEmpty)
    }

    @Test func alertAndDurationTitles() {
        let alerts = [
            -1: "None", 0: "At time of event", 1: "1 minute before", 5: "5 minutes before", 60: "1 hour before",
            120: "2 hours before", 90: "90 minutes before", 1440: "1 day before", 2880: "2 days before",
        ]
        for (m, want) in alerts {
            #expect(Cal.alertTitle(m) == want)
        }
        #expect(Cal.durationChoices.map(Cal.durationTitle) == ["15 min", "30 min", "45 min", "1 h", "1 h 30 min", "2 h"])
    }

    @Test func allDayAlerts() {
        let titles: [(Int, Bool, String)] = [
            (900, true, "1 day before (9:00)"), (2340, true, "2 days before (9:00)"), (9540, true, "1 week before (9:00)"),
            (1080, true, "18 hours before"), (-1, true, "None"), (0, true, "At time of event"),
            (900, false, "15 hours before"), (3780, true, "3 days before (9:00)"),
        ]
        for (m, allDay, want) in titles {
            #expect(Cal.alertTitle(m, allDay: allDay) == want, "\(m) \(allDay)")
        }
        #expect(Cal.alertChoices(allDay: true) == Cal.allDayAlertChoices && Cal.alertChoices(allDay: false) == Cal.alertChoices)
        let prefs = Cal.defaultPreferences
        var none = prefs
        none.defaultAlertMinutes = -1
        let toggles: [(Int, Bool, CalendarPreferences, Int)] = [
            (15, true, prefs, 900), (-1, true, prefs, -1), (2340, true, prefs, 2340), (0, true, prefs, 900),
            (900, false, prefs, 15), (-1, false, prefs, -1), (9540, false, none, -1),
        ]
        for (cur, on, p, want) in toggles {
            #expect(Cal.alertOnAllDayToggle(cur, on: on, preferences: p) == want, "\(cur) \(on)")
        }
    }

    @Test func editorDelete() {
        var own = F.event()
        own.canDelete = true
        own.removeIsDelete = true
        var invitation = F.event(F.occ(myResponse: .accepted))
        invitation.canDelete = true
        var noDelete = F.event()
        noDelete.removeIsDelete = true
        let cases: [(String, Bool, Event, String?)] = [
            ("new event", false, own, nil), ("no canDelete", true, noDelete, nil), ("own event", true, own, "Delete"),
            ("invitation", true, invitation, "Remove from Calendar"),
        ]
        for (name, editing, e, want) in cases {
            #expect(Cal.editorDelete(editing: editing, e) == want, "\(name)")
        }
    }

    @Test func mailTexts() {
        let env = F.env()
        var o = F.timed("x", F.at(2026, 10, 13, 10), F.at(2026, 10, 13, 11))
        o.title = "Q4 planning"
        o.location = "Room B"
        let subjects: [Cal.MailKind: String] = [
            .invitation: "Invitation: Q4 planning", .update: "Updated invitation: Q4 planning", .cancel: "Cancelled: Q4 planning",
            .accepted: "Accepted: Q4 planning", .tentative: "Tentative: Q4 planning", .declined: "Declined: Q4 planning",
        ]
        for (k, want) in subjects {
            #expect(Cal.mailTexts(k, o, me: "Me", env: env).subject == want)
        }
        #expect(Cal.mailTexts(.accepted, o, me: "Jana", env: env).text
            == "Jana has accepted the invitation to “Q4 planning”.\n\nWhen: Tuesday 13 October 2026 · 10:00–11:00 · 1 h\nWhere: Room B")
        let bare = Cal.mailTexts(.invitation, F.occ(), me: "", env: env)
        #expect(bare.subject == "Invitation: Untitled Event" && bare.text == "You are invited to “Untitled Event”.")
        var long = o
        long.title = String(repeating: "é", count: API.Limits.maxMailTextBytes)
        let l = Cal.mailTexts(.invitation, long, me: "", env: env)
        #expect(l.subject.utf8.count <= API.Limits.maxMailTextBytes && l.subject.hasPrefix("Invitation: "))
        #expect(Cal.mailKindOf(.declined) == .declined && Cal.mailKindOf(.tentative) == .tentative
            && Cal.mailKindOf(.accepted) == .accepted)
        #expect(Cal.capBytes("aé", 2) == "a" && Cal.capBytes("abc", 5) == "abc")
    }

    @Test func scopeQuestion() {
        let all3: [EditScope] = [.this, .thisAndFuture, .all]
        let cases: [(Cal.ScopeAction, Bool, [EditScope], String)] = [
            (.edit, false, all3, "Change a Repeating Event?"),
            (.delete, false, all3, "Delete a Repeating Event?"),
            (.respond, false, [.this, .all], "Answer for the Whole Series?"),
            (.edit, true, [.all], "Change a Repeating Event?"),
            (.delete, true, [.all], "Delete a Repeating Event?"),
            (.respond, true, [.this, .all], "Answer for the Whole Series?"),
        ]
        for (a, complex, want, heading) in cases {
            let q = Cal.scopeQuestionFor(a, complex: complex)
            #expect(q.choices.map(\.scope) == want && q.heading == heading, "\(a) \(complex)")
        }
        let q = Cal.scopeQuestionFor(.edit, complex: false)
        #expect(q.choices.map(\.title) == ["This Event Only", "This and Future Events", "All Events"])
        let c = Cal.closeQuestion
        #expect(c.confirm == "Save" && c.alternative == "Discard")
    }
}
