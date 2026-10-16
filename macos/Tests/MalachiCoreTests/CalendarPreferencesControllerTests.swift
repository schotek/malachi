// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// General → Calendar (CalendarPreferencesController): the rows' choices,
// a change shown at once and written, the answer kept, a refusal undone.

@MainActor
@Suite(.serialized) struct CalendarPreferencesControllerTests {
    @Test func roundTrip() async throws {
        let src = calSamples()
        let p = CalendarPreferencesController(source: src, env: calEnv)
        p.start()
        #expect(p.weekStartChoices.map(\.value) == [.locale, .monday, .sunday, .saturday])
        #expect(p.weekStartChoices.first?.title == "System")
        #expect(p.durationChoices.map(\.value) == [15, 30, 45, 60, 90, 120])
        #expect(p.alertChoices.first?.title == Cal.alertTitle(-1))
        #expect(p.workingHoursText == "Every weekday · 08:00–17:00")
        p.setWeekStart(.sunday)
        #expect(p.preferences.weekStart == .sunday && p.saving)
        try await calWaitUntil { !p.saving }
        #expect(src.writes == [.setPreferences(CalendarPreferencesUpdate(weekStart: .sunday))])
        #expect(src.snapshot.preferences.weekStart == .sunday)
        p.setShowDeclined(true)
        p.setDefaultDuration(30)
        try await calWaitUntil { !p.saving }
        #expect(p.preferences.showDeclined && p.preferences.defaultDurationMinutes == 30)
        // A value that is not a choice is listed.
        p.setDefaultAlert(20)
        try await calWaitUntil { !p.saving }
        #expect(p.alertChoices.map(\.value).contains(20))
    }

    @Test func refusalIsUndone() async throws {
        let src = calSamples()
        src.failures[API.CalendarSetPreferences.name] = RPCError(code: .invalidArgument, message: "range")
        let p = CalendarPreferencesController(source: src, env: calEnv)
        p.start()
        p.setDefaultDuration(90)
        #expect(p.preferences.defaultDurationMinutes == 90)
        try await calWaitUntil { !p.saving }
        #expect(p.preferences.defaultDurationMinutes == 60 && !p.error.isEmpty)
    }

    @Test func workingHours() async throws {
        let src = calSamples()
        let p = CalendarPreferencesController(source: src, env: calEnv)
        p.start()
        #expect(p.workingDayChoices.map(\.value) == ["MO", "TU", "WE", "TH", "FR", "SA", "SU"])
        #expect(p.workingDayChoices.first?.title == "Mon")
        #expect(p.isWorkingDay("MO") && !p.isWorkingDay("SA"))
        p.setWorkingDay("SA", true)
        try await calWaitUntil { !p.saving }
        #expect(p.preferences.workingHours.days == ["MO", "TU", "WE", "TH", "FR", "SA"])
        let times = p.workingTimeChoices(including: "08:15")
        #expect(times.count == 49 && times.contains { $0.value == "08:15" } && times.first?.title == "00:00")
        p.setWorkingStart("18:00")
        #expect(p.preferences.workingHours.start == "08:00")
        p.setWorkingStart("09:30")
        try await calWaitUntil { !p.saving }
        p.setWorkingEnd("09:00")
        #expect(p.preferences.workingHours.end == "17:00")
        p.setWorkingEnd("18:30")
        try await calWaitUntil { !p.saving }
        #expect(src.snapshot.preferences.workingHours.start == "09:30" && src.snapshot.preferences.workingHours.end == "18:30")
        for d in ["MO", "TU", "WE", "TH", "FR"] {
            p.setWorkingDay(d, false)
            try await calWaitUntil { !p.saving }
        }
        // The last day stays.
        p.setWorkingDay("SA", false)
        #expect(p.preferences.workingHours.days == ["SA"])
    }
}
