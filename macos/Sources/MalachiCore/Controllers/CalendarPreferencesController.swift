// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Preferences → General → Calendar (docs/calendar.md §5.8) over
// calendar.preferences / calendar.setPreferences: Week Starts On, Default
// Duration, Default Alert, Show Declined Events, Working Hours. A change
// shows at once and is written; a refusal takes it back and says why
// (`error`). The daemon keeps the preferences (`meta`), not Settings.

import Foundation

@MainActor
public final class CalendarPreferencesController {
    /// A popup choice: its value and title.
    public struct Choice<T: Sendable & Equatable>: Sendable, Equatable {
        public var value: T
        public var title: String
    }

    public let source: any CalendarDataSource
    public let env: Cal.Env

    public var onChange: (@MainActor () -> Void)?

    /// The preferences as shown (the written change before its answer).
    public private(set) var preferences: CalendarPreferences
    public private(set) var saving = false
    /// The last refusal ("" none).
    public private(set) var error = ""

    private var observer: CalendarObserverToken?
    private var writes = 0

    public init(source: any CalendarDataSource, env: Cal.Env = Cal.Env(zone: .current, locale: .current)) {
        self.source = source
        self.env = env
        preferences = source.snapshot.preferences
    }

    public func start() {
        guard observer == nil else { return }
        observer = source.observe { [weak self] c in
            guard let self, c == .snapshot, self.writes == 0, self.preferences != self.source.snapshot.preferences else { return }
            self.preferences = self.source.snapshot.preferences
            self.onChange?()
        }
    }

    public func stop() {
        observer?.cancel()
        observer = nil
    }

    // MARK: Rows

    public var weekStartChoices: [Choice<WeekStart>] {
        Cal.Prefs.weekStarts.map { Choice(value: $0, title: Cal.Prefs.weekStartTitle($0, env: env)) }
    }

    /// The duration choices, with the stored value when it is not one.
    public var durationChoices: [Choice<Int>] {
        var v = Cal.durationChoices
        if !v.contains(preferences.defaultDurationMinutes) {
            v.append(preferences.defaultDurationMinutes)
            v.sort()
        }
        return v.map { Choice(value: $0, title: Cal.durationTitle($0)) }
    }

    /// The alert choices (-1 None), with the stored value when it is not one.
    public var alertChoices: [Choice<Int>] {
        var v = Cal.alertChoices
        if !v.contains(preferences.defaultAlertMinutes) {
            v.append(preferences.defaultAlertMinutes)
            v.sort()
        }
        return v.map { Choice(value: $0, title: Cal.alertTitle($0)) }
    }

    /// "Every weekday · 08:00–17:00".
    public var workingHoursText: String { Cal.Prefs.workingHoursText(preferences.workingHours, env: env) }

    // MARK: Changes

    public func setWeekStart(_ w: WeekStart) {
        guard w != preferences.weekStart else { return }
        write(CalendarPreferencesUpdate(weekStart: w)) { $0.weekStart = w }
    }

    public func setDefaultDuration(_ minutes: Int) {
        guard minutes != preferences.defaultDurationMinutes else { return }
        write(CalendarPreferencesUpdate(defaultDurationMinutes: minutes)) { $0.defaultDurationMinutes = minutes }
    }

    public func setDefaultAlert(_ minutes: Int) {
        guard minutes != preferences.defaultAlertMinutes else { return }
        write(CalendarPreferencesUpdate(defaultAlertMinutes: minutes)) { $0.defaultAlertMinutes = minutes }
    }

    public func setShowDeclined(_ on: Bool) {
        guard on != preferences.showDeclined else { return }
        write(CalendarPreferencesUpdate(showDeclined: on)) { $0.showDeclined = on }
    }

    public func setWorkingHours(_ wh: WorkingHours) {
        guard wh != preferences.workingHours else { return }
        write(CalendarPreferencesUpdate(workingHours: wh)) { $0.workingHours = wh }
    }


    // MARK: Working hours

    /// The working days' toggles from Monday: the day code and the short
    /// weekday name.
    public var workingDayChoices: [Choice<String>] {
        // 2026-10-05 is a Monday.
        (0..<7).map { i in
            let d = Cal.Weekday(rawValue: (i + 1) % 7) ?? .monday
            return Choice(value: Cal.dayCode(d), title: Cal.Text.shortWeekday(Cal.date(2026, 10, 5 + i, 12, 0, in: env.zone), env))
        }
    }

    public func isWorkingDay(_ code: String) -> Bool { preferences.workingHours.days.contains(code) }

    /// A day's toggle; the last working day stays.
    public func setWorkingDay(_ code: String, _ on: Bool) {
        guard Cal.parseDayCode(code)?.ordinal == 0, on != isWorkingDay(code) else { return }
        var wh = preferences.workingHours
        if on {
            wh.days.append(code)
            let order = workingDayChoices.map(\.value)
            wh.days.sort { (order.firstIndex(of: $0) ?? 7) < (order.firstIndex(of: $1) ?? 7) }
        } else {
            guard wh.days.count > 1 else { return }
            wh.days.removeAll { $0 == code }
        }
        setWorkingHours(wh)
    }

    /// The start and end choices: every half hour, with the stored value
    /// when it is not one.
    public func workingTimeChoices(including current: String) -> [Choice<String>] {
        var minutes = Array(stride(from: 0, to: 24 * 60, by: 30))
        if let m = Cal.parseClock(current), !minutes.contains(m) {
            minutes.append(m)
            minutes.sort()
        }
        return minutes.map { m in
            Choice(value: Self.clock(m), title: Cal.Text.time(Cal.date(2026, 10, 5, m / 60, m % 60, in: env.zone), env))
        }
    }

    static func clock(_ m: Int) -> String { String(format: "%02d:%02d", m / 60, m % 60) }

    /// Start; refused when it is not before the end.
    public func setWorkingStart(_ s: String) {
        guard let a = Cal.parseClock(s), let b = Cal.parseClock(preferences.workingHours.end), a < b else { return }
        var wh = preferences.workingHours
        wh.start = s
        setWorkingHours(wh)
    }

    /// End; refused when it is not after the start.
    public func setWorkingEnd(_ s: String) {
        guard let b = Cal.parseClock(s), let a = Cal.parseClock(preferences.workingHours.start), a < b else { return }
        var wh = preferences.workingHours
        wh.end = s
        setWorkingHours(wh)
    }

    private func write(_ u: CalendarPreferencesUpdate, _ local: (inout CalendarPreferences) -> Void) {
        let before = preferences
        local(&preferences)
        error = ""
        saving = true
        writes += 1
        onChange?()
        let source = source
        Task { [weak self] in
            var answer: CalendarPreferences?
            var failure: (any Error)?
            do {
                answer = try await source.setPreferences(u)
            } catch {
                failure = error
            }
            guard let self else { return }
            self.writes -= 1
            if let failure {
                self.preferences = self.writes == 0 ? self.source.snapshot.preferences : before
                self.error = Cal.errorMessage(failure)
            } else if let answer, self.writes == 0 {
                self.preferences = answer
            }
            self.saving = self.writes > 0
            self.onChange?()
        }
    }
}
