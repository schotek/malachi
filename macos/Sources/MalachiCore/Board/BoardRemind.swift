// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Remind…: the presets the button's menu and the case menu offer. A remind
// hides a case until its time (`board.remind`); the daemon takes any time
// in the future up to a year ahead, so every preset lies after `now`.
//
// Ported back from the Go reference ui/internal/board (remind.go), which
// leads since the presets of 2026-10-08.

import Foundation

extension Board {
    /// A remind time the menu offers (Go `RemindChoice`).
    public struct RemindPreset: Sendable, Equatable {
        /// The raw order is Go's `RemindKind`: new kinds are appended.
        public enum Kind: Sendable, CaseIterable {
            /// Three hours from now rounded up to the hour, offered only when
            /// that is 20:00 or earlier the same day and now is from 05:00
            /// to before 19:00.
            case laterToday
            /// 09:00 tomorrow.
            case tomorrow
            /// 09:00 next Monday (a week ahead on a Monday).
            case nextWeek
            /// 20:00 today, offered from 17:00 to 18:59.
            case thisEvening
            /// 09:00 today, offered before 05:00 in place of `tomorrow`.
            case thisMorning
        }

        public var kind: Kind
        public var date: Date
        /// "Later Today".
        public var title: String
        /// When: "Thu at 18:00" (`Text.dayAndTime` of the weekday and the
        /// time).
        public var when: String
        /// `title` and `when` as one menu item: "Later Today, Thu at 18:00"
        /// (`Text.remindItem`).
        public var label: String
    }

    /// The hour of This Morning, Tomorrow and Next Week.
    static let remindMorningHour = 9
    /// This Evening's hour, and the latest Later Today may be.
    static let remindEveningHour = 20
    /// The hour from which This Evening is offered (until
    /// `remindNightFrom`).
    static let remindEveningFrom = 17
    /// The hour from which neither Later Today nor This Evening is offered.
    static let remindNightFrom = 19
    /// The hour before which a night still belongs to the day before:
    /// Tomorrow then means this morning (This Morning).
    static let remindDawn = 5

    /// The presets for `now`, in menu order (which is the order of their
    /// times), by the wall clock of `calendar`:
    ///
    /// - Later Today: three hours from now rounded up to the hour, offered
    ///   only when that is 20:00 or earlier the same day (so not from
    ///   17:01, and never from 19:00) and not before dawn (05:00: the
    ///   night belongs to the day before, This Morning answers);
    /// - This Evening: 20:00, offered from 17:00 to 18:59;
    /// - Tomorrow: 09:00 tomorrow; before 05:00 the night still belongs to
    ///   yesterday, so it is 09:00 today, named This Morning;
    /// - Next Week: 09:00 next Monday (a week ahead on a Monday).
    ///
    /// A time an earlier preset offers already (This Evening at 17:00 is
    /// Later Today; Next Week on a Sunday is Tomorrow) is offered once,
    /// under the first. Every preset lies after `now`.
    public static func remindPresets(now: Date, calendar: Calendar) -> [RemindPreset] {
        let locale = calendar.locale ?? .current
        var out: [RemindPreset] = []
        func add(_ kind: RemindPreset.Kind, _ date: Date?) {
            guard let date, date > now, !out.contains(where: { $0.date == date }) else { return }
            let weekday = Strftime.formatter("%a", locale: locale)
            weekday.calendar = calendar
            weekday.timeZone = calendar.timeZone
            let title = Text.remindPreset(kind)
            let when = Text.dayAndTime(weekday.string(from: date), formatTime(date, locale: locale, calendar: calendar))
            out.append(
                RemindPreset(kind: kind, date: date, title: title, when: when, label: Text.remindItem(title, when)))
        }
        let today = calendar.startOfDay(for: now)
        let hour = calendar.component(.hour, from: now)
        add(.laterToday, laterToday(now: now, calendar: calendar))
        if hour >= remindEveningFrom && hour < remindNightFrom {
            add(.thisEvening, at(remindEveningHour, on: today, calendar))
        }
        if hour < remindDawn {
            add(.thisMorning, at(remindMorningHour, on: today, calendar))
        } else {
            let tomorrow = calendar.date(byAdding: .day, value: 1, to: today)
            add(.tomorrow, tomorrow.flatMap { at(remindMorningHour, on: $0, calendar) })
        }
        add(.nextWeek, nextMonday(after: today, calendar: calendar).flatMap { at(remindMorningHour, on: $0, calendar) })
        return out
    }

    /// Later Today's time, nil when it is not offered: three hours from now
    /// rounded up to the hour of the wall clock (an exact hour stays), when
    /// that is the same day at 20:00 or earlier and now is from 05:00
    /// (`remindDawn`) to before 19:00.
    private static func laterToday(now: Date, calendar: Calendar) -> Date? {
        let hour = calendar.component(.hour, from: now)
        guard hour < remindNightFrom, hour >= remindDawn else { return nil }
        let today = calendar.startOfDay(for: now)
        let plus3 = now.addingTimeInterval(3 * 3600)
        var rounded = calendar.dateInterval(of: .hour, for: plus3)?.start ?? plus3
        if rounded < plus3 {
            rounded = rounded.addingTimeInterval(3600)
        }
        guard let evening = at(remindEveningHour, on: today, calendar), rounded <= evening else { return nil }
        return rounded
    }

    /// The Monday after `day` (a week ahead when `day` is a Monday).
    private static func nextMonday(after day: Date, calendar: Calendar) -> Date? {
        let monday = 2 // Gregorian weekday numbers: Sunday is 1.
        let weekday = calendar.component(.weekday, from: day)
        var ahead = (monday - weekday + 7) % 7
        if ahead == 0 {
            ahead = 7
        }
        return calendar.date(byAdding: .day, value: ahead, to: day)
    }

    private static func at(_ hour: Int, on day: Date, _ calendar: Calendar) -> Date? {
        calendar.date(bySettingHour: hour, minute: 0, second: 0, of: day)
    }
}
