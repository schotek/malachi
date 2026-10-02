// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Remind…: the presets the button's menu and the case menu offer. A remind
// hides a case until its time (`board.remind`); the daemon takes any time
// in the future up to a year ahead, so every preset lies after `now`.
//
// Swift-first: written here first; the GTK window ports it to
// ui/internal/board with the board itself.

import Foundation

extension Board {
    /// A remind time the menu offers.
    public struct RemindPreset: Sendable, Equatable {
        public enum Kind: Sendable, CaseIterable {
            /// Three hours from now rounded up to the hour, or 18:00 when
            /// that comes first and is an hour away or more; none when
            /// neither is today.
            case laterToday
            /// 09:00 tomorrow.
            case tomorrow
            /// 09:00 next Monday (a week ahead on a Monday).
            case nextWeek
        }

        public var kind: Kind
        public var date: Date
        /// "Later Today".
        public var title: String
        /// When: "Thu 18:00".
        public var when: String
    }

    /// The hour of the morning presets.
    static let remindMorningHour = 9
    /// The hour Later Today does not go past while it is an hour away.
    static let remindEveningHour = 18

    /// The presets for `now`, in menu order; Later Today is left out late
    /// in the evening, Next Week on a Sunday (it would be Tomorrow).
    public static func remindPresets(now: Date, calendar: Calendar) -> [RemindPreset] {
        let locale = calendar.locale ?? .current
        var out: [RemindPreset] = []
        func add(_ kind: RemindPreset.Kind, _ date: Date?) {
            // A time an earlier preset offers already (Next Week on a
            // Sunday is Tomorrow) is offered once, under the first.
            guard let date, date > now, !out.contains(where: { $0.date == date }) else { return }
            let weekday = Strftime.formatter("%a", locale: locale)
            weekday.calendar = calendar
            weekday.timeZone = calendar.timeZone
            out.append(
                RemindPreset(
                    kind: kind, date: date, title: Text.remindPreset(kind),
                    when: weekday.string(from: date) + " " + formatTime(date, locale: locale, calendar: calendar)))
        }
        add(.laterToday, laterToday(now: now, calendar: calendar))
        let today = calendar.startOfDay(for: now)
        let tomorrow = calendar.date(byAdding: .day, value: 1, to: today)
        add(.tomorrow, tomorrow.flatMap { at(remindMorningHour, on: $0, calendar) })
        add(.nextWeek, nextMonday(after: today, calendar: calendar).flatMap { at(remindMorningHour, on: $0, calendar) })
        return out
    }

    private static func laterToday(now: Date, calendar: Calendar) -> Date? {
        let today = calendar.startOfDay(for: now)
        guard let plus3 = calendar.date(byAdding: .hour, value: 3, to: now) else { return nil }
        // Rounded up to the hour: an exact hour stays.
        var rounded = calendar.dateInterval(of: .hour, for: plus3)?.start ?? plus3
        if rounded < plus3, let next = calendar.date(byAdding: .hour, value: 1, to: rounded) {
            rounded = next
        }
        var pick = rounded
        if let evening = at(remindEveningHour, on: today, calendar), evening < rounded,
           evening >= now.addingTimeInterval(3600)
        {
            pick = evening
        }
        return calendar.isDate(pick, inSameDayAs: now) ? pick : nil
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
