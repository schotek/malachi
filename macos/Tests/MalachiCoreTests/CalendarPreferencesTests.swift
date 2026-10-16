// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// MalachiCore/Calendar/CalendarPreferences.swift and CalendarText.swift,
// the cases of Go preferences_test.go plus the status line and errors.

private typealias F = CalFixture

private func record(isDefault: Bool = false, access: CalendarAccess, kind: CalendarKind = "", owner: Address? = nil) -> CalendarRecord {
    CalendarRecord(id: "c", accountId: "a", name: "", color: "", visible: true, access: access, kind: kind, owner: owner, isDefault: isDefault)
}

@Suite struct CalendarPreferencesTests {
    private typealias P = Cal.Prefs

    @Test func preferenceTexts() {
        let env = F.env(F.utc)
        let titles: [(WeekStart, String)] = [(.locale, "System"), (.monday, "Monday"), (.sunday, "Sunday"), (.saturday, "Saturday")]
        for (w, want) in titles {
            #expect(P.weekStartTitle(w, env: env) == want)
        }
        let hours: [(WorkingHours, String)] = [
            (Cal.defaultPreferences.workingHours, "Every weekday · 08:00–17:00"),
            (WorkingHours(days: ["WE", "MO", "MO", "2TU"], start: "09:30", end: "12:00"), "Monday and Wednesday · 09:30–12:00"),
            (WorkingHours(days: ["SU"], start: "x", end: "12:00"), "Sunday"),
            (WorkingHours(days: [], start: "", end: ""), ""),
        ]
        for (wh, want) in hours {
            #expect(P.workingHoursText(wh, env: env) == want)
        }
        let keep = [30: "1 month", 90: "3 months", 180: "6 months", 365: "1 year", 730: "2 years", 7: "7 days"]
        for (d, want) in keep {
            #expect(P.keepTitle(d) == want)
        }
        let notes: [(CalendarRecord, String)] = [
            (record(isDefault: true, access: .owner), "default"),
            (record(access: .read), "read only"),
            (record(access: .write, owner: Address(name: "Petr", address: "p@example.org")), "shared · Petr"),
            (record(isDefault: true, access: .read, owner: Address(address: "p@example.org")), "default · shared · p@example.org"),
            (record(access: .owner), ""),
        ]
        for (c, want) in notes {
            #expect(P.calendarRowNote(c) == want)
        }
        let writable: [(CalendarRecord, Bool)] = [
            (record(access: .owner, kind: .events), true), (record(access: .write), true), (record(access: .read), false),
            (record(access: .owner, kind: .birthdays), false), (record(access: .owner, kind: .holidays), false),
        ]
        for (c, want) in writable {
            #expect(P.writable(c) == want)
        }
        #expect(P.replacePassword == "Replace Password…" && P.replacePasswordTitle == "Replace the Password?")
        #expect(Cal.Text.reloadEvent == "Reload Event")
        #expect(P.afterAnswerTitle(.archive) == "Archive" && P.afterAnswerTitle(nil) == "Keep in Inbox")
    }

    @Test func statusLine() {
        let now = F.at(2026, 10, 14, 12)
        func st(_ s: CalendarSyncStatus, _ ago: TimeInterval?) -> CalendarSyncState {
            CalendarSyncState(accountId: "a", status: s, progress: -1, lastSync: ago.map { now.addingTimeInterval(-$0) })
        }
        let z = F.prague
        #expect(Cal.Text.statusLine([], now: now, zone: z) == "")
        #expect(Cal.Text.statusLine([st(.idle, 120), st(.idle, 600)], now: now, zone: z) == "Calendars synced 10 minutes ago")
        #expect(Cal.Text.statusLine([st(.idle, 60), st(.syncing, nil)], now: now, zone: z) == "Syncing calendars…")
        #expect(Cal.Text.statusLine([st(.offline, nil), st(.offline, 60)], now: now, zone: z) == "Calendars are offline")
        #expect(Cal.Text.statusLine([st(.syncing, nil), st(.consentRequired, nil)], now: now, zone: z)
            == "Calendar access needs a new sign-in")
        #expect(Cal.Text.unansweredCount(1) == "1 unanswered invitation" && Cal.Text.unansweredCount(3) == "3 unanswered invitations")
        #expect(Cal.Text.errorText(.eventGone) == "The event no longer exists." && Cal.Text.errorText(.authFailed) == nil)
        #expect(Cal.Text.errorText(.calendarConsentRequired) == Cal.Text.consentLine)
        #expect(Cal.Text.emptyBody(.loading) == "" && Cal.Text.emptyTitle(.noAccounts) == "No Calendars")
        #expect(Cal.Text.viewTitle(.agenda) == "Agenda" && Cal.Text.modeName == "Calendar")
        #expect(Cal.Text.unanswered == "Unanswered" && Cal.unansweredHeading == "Invitations")
    }
}
