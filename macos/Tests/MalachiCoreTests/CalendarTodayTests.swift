// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// MalachiCore/Calendar/CalendarToday.swift and CalendarNotificationText.swift,
// the cases of Go today_test.go and notifications_test.go.

private typealias F = CalFixture

@Suite struct CalendarTodayTests {
    @Test func todayRows() {
        let env = F.env()
        // Tuesday 13 October 2026 at noon in the samples' week.
        let now = F.at(2026, 10, 13, 12)
        let s = Cal.sampleData(now: now, zone: F.prague)
        let got = Cal.todayRows(s.occurrences, now: now, env: env, showDeclined: s.preferences.showDeclined).map {
            "\($0.time) \($0.title) [\($0.place)] past=\($0.tookPlace) respond=\($0.respond)"
        }
        #expect(got == [
            "09:00–09:15 Standup [Room B] past=true respond=false",
            "10:00–11:30 Q4 planning [Room A] past=true respond=false",
            "10:30–11:00 Call with the supplier [] past=true respond=false",
            "11:00–12:00 Design review [] past=true respond=false",
        ])
        let wed = F.at(2026, 10, 14, 14, 30)
        let rows = Cal.todayRows(s.occurrences, now: wed, env: env, showDeclined: false)
        #expect(rows.count == 2 && rows[1].title == "Budget review" && rows[1].respond && !rows[1].tookPlace && rows[0].tookPlace)
        let withDeclined = Cal.todayRows(s.occurrences, now: wed, env: env, showDeclined: true)
        #expect(withDeclined.count == 3 && withDeclined[2].state == .declined)
        let thu = Cal.todayRows(s.occurrences, now: F.at(2026, 10, 15, 8), env: env, showDeclined: false)
        #expect(!thu.isEmpty && thu[0].time == "All Day" && thu[0].title == "Team offsite" && !thu[0].tookPlace)
        #expect(Cal.todayRows([], now: now, env: env, showDeclined: false).isEmpty)
        let mailOnly = Account(id: "a", config: AccountConfig(name: "", email: ""), enabled: true, state: SyncState(accountId: "a", status: .idle),
                               capabilities: API.mailCapabilities)
        var legacy = mailOnly
        legacy.capabilities = nil
        #expect(Cal.showsTodayStrip(s.accounts) && !Cal.showsTodayStrip([mailOnly]) && !Cal.showsTodayStrip([legacy]))
    }

    @Test func alertNotification() {
        let env = F.env()
        let now = F.at(2026, 10, 14, 9, 50)
        func alert(_ title: String, in d: TimeInterval? = nil, allDay: Bool = false, startDate: CalendarDay? = nil, location: String? = nil)
            -> EventAlert
        {
            EventAlert(
                alertId: "al", state: .due, eventId: "ev", accountId: "acc", calendarId: "cal", title: title,
                start: d.map { now.addingTimeInterval($0) }, startDate: startDate, allDay: allDay, location: location, minutesBefore: 10)
        }
        let cases: [(EventAlert, Cal.Notification)] = [
            (alert("Standup", in: 600, location: "Room B"), .init(title: "Standup", body: "in 10 minutes · Room B")),
            (alert("Standup", in: 60), .init(title: "Standup", body: "in 1 minute")),
            (alert("Standup", in: 20), .init(title: "Standup", body: "now")),
            (alert("Standup", in: -300), .init(title: "Standup", body: "now")),
            (alert("Lunch", in: 2 * 3600 + 600), .init(title: "Lunch", body: "at 12:00")),
            (alert("Trip", in: 24 * 3600), .init(title: "Trip", body: "Thursday 15 October · 09:50")),
            (alert("Offsite", allDay: true, startDate: "2026-10-14"), .init(title: "Offsite", body: "today")),
            (alert("Offsite", allDay: true, startDate: "2026-10-15", location: "Hotel"), .init(title: "Offsite", body: "Thursday 15 October · Hotel")),
            (alert(""), .init(title: "Untitled Event", body: "")),
        ]
        for (a, want) in cases {
            #expect(Cal.alertNotification(a, now: now, env: env) == want, "\(a.title)")
        }
    }

    @Test func invitationNotifications() {
        let env = F.env()
        let s = F.at(2026, 10, 14, 10)
        let e = s.addingTimeInterval(3600)
        let jana = Address(name: "Jana Nováková", address: "jana@example.org")
        var sum = InvitationSummary(method: .request, kind: .invitation, state: .unanswered, title: "Q4 planning", start: s, end: e, organizer: jana)
        #expect(Cal.invitationNotification(sum, env: env)
            == .init(title: "New invitation from Jana Nováková", body: "Q4 planning · Wednesday 14 October 2026 · 10:00–11:00 · 1 h"))
        sum.kind = .update
        #expect(Cal.invitationNotification(sum, env: env).title == "Jana Nováková updated the event")
        sum.organizer = nil
        #expect(Cal.invitationNotification(sum, env: env).title == "An event was updated")
        sum.kind = .invitation
        #expect(Cal.invitationNotification(sum, env: env).title == "New invitation")
        let ad = InvitationSummary(method: "", kind: "", state: "", title: "Offsite", startDate: "2026-10-15", allDay: true, organizer: jana)
        #expect(Cal.cancellationNotification(ad, env: env)
            == .init(title: "Jana Nováková cancelled the event", body: "Offsite · Thursday 15 October 2026 · All Day"))
        #expect(Cal.cancellationNotification(InvitationSummary(method: "", kind: "", state: "", title: ""), env: env)
            == .init(title: "An event was cancelled", body: "Untitled Event"))
    }
}
