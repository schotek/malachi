// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// MalachiCore/Calendar/CalendarSampleData.swift, the cases of Go
// samples_test.go.

private typealias F = CalFixture

@Suite struct CalendarSampleDataTests {
    @Test func sampleData() {
        let p = F.prague
        for now in [F.at(2026, 10, 14, 12), F.at(2026, 10, 18, 23), F.at(2026, 3, 30, 8)] {
            let s = Cal.sampleData(now: now, zone: p)
            #expect(s.accounts.count == 3 && s.calendars.accounts.count == 3 && s.invitations.count == 3)
            #expect(s.calendars.unanswered == 3 && s.cards.count == Cal.invitationStates.count)
            let week = Cal.viewRange(.week, anchor: now, weekStart: .monday, zone: p)
            var states: [Cal.BlockState: Int] = [:]
            var video = 0, recurring = 0, twoDay = 0, invitations = 0
            for o in s.occurrences {
                #expect(Cal.overlaps(o, from: week.from, to: week.to, p), "\(o.title) is outside the week")
                states[Cal.stateOf(o), default: 0] += 1
                video += o.videoCall ? 1 : 0
                recurring += o.recurring ? 1 : 0
                invitations += o.invitation != nil ? 1 : 0
                if o.allDay, let (a, b) = Cal.span(o, p), Cal.daysBetween(a, b, p) == 2 {
                    twoDay += 1
                }
                if let c = o.calendarId {
                    #expect(!s.calendarName(c).isEmpty, "\(o.title) names no sample calendar")
                }
            }
            #expect(states[.tentative, default: 0] > 0 && states[.unanswered, default: 0] >= 2 && states[.declined] == 1)
            #expect(video == 1 && recurring == 5 && twoDay == 1 && invitations == 1)
            let tue = Cal.layoutDay(s.occurrences, day: Cal.addDays(week.from, 1, p), zone: p, showDeclined: false)
            #expect(tue.filter { $0.columns > 1 }.count >= 3)
            for (i, c) in s.cards.enumerated() {
                #expect(c.state == Cal.invitationStates[i])
            }
            #expect(s.sampleAccountName(Cal.sampleNextcloud) == "Nextcloud" && s.sampleAccountName("x") == "" && s.calendarName("x") == "")
            for a in s.accounts {
                #expect(a.can(.calendar), "\(a.config.name)")
            }
        }
    }
}
