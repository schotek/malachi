// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Recurrence texts (Go recurrence.go): "Every 2 weeks on Monday and
// Wednesday", "Monthly on the second Tuesday", "Yearly on 14 October,
// until 1 March 2027", and the editor's Repeat choices with their
// Recurrence values. A rule outside the editor's shape is "Custom".

import Foundation

extension Cal {
    private static let dayCodes = ["SU", "MO", "TU", "WE", "TH", "FR", "SA"]

    /// recurrence.go `DayCode`: "MO".
    public static func dayCode(_ d: Weekday) -> String { dayCodes[d.rawValue] }

    /// recurrence.go `ParseDayCode`: "MO", "2TU", "-1FR": the weekday and
    /// the ordinal (0 none).
    public static func parseDayCode(_ s: String) -> (day: Weekday, ordinal: Int)? {
        let b = Array(s.utf8)
        guard b.count >= 2 else { return nil }
        let code = String(decoding: b[(b.count - 2)...], as: UTF8.self)
        var ord = 0
        if b.count > 2 {
            var n = String(decoding: b[..<(b.count - 2)], as: UTF8.self)
            if n.hasPrefix("+") {
                n.removeFirst()
            }
            guard let v = Int(n), v != 0, v >= -5, v <= 5 else { return nil }
            ord = v
        }
        guard let i = dayCodes.firstIndex(of: code), let d = Weekday(rawValue: i) else { return nil }
        return (d, ord)
    }

    /// A weekday's name in the locale, through a reference date.
    static func weekdayName(_ d: Weekday, _ env: Env) -> String {
        // 2026-10-04 is a Sunday.
        Text.weekday(date(2026, 10, 4 + d.rawValue, 12, 0, in: env.zone), env)
    }

    /// recurrence.go `ListText`: "Monday", "Monday and Wednesday", "Monday,
    /// Wednesday and Friday".
    public static func listText(_ items: [String]) -> String {
        switch items.count {
        case 0: return ""
        case 1: return items[0]
        default: break
        }
        var out = items[0]
        for it in items[1..<(items.count - 1)] {
            out = L10n.T("%s, %s", out, it)
        }
        return L10n.T("%s and %s", out, items[items.count - 1])
    }

    /// "second Tuesday", "last Friday".
    static func ordinalWeekday(_ ord: Int, _ d: Weekday, _ env: Env) -> String {
        let day = weekdayName(d, env)
        switch ord {
        case 1: return L10n.T("first %s", day)
        case 2: return L10n.T("second %s", day)
        case 3: return L10n.T("third %s", day)
        case 4: return L10n.T("fourth %s", day)
        case 5: return L10n.T("fifth %s", day)
        case -1: return L10n.T("last %s", day)
        default: return L10n.T("second-to-last %s", day)
        }
    }

    /// The text of a rule the editor cannot show.
    public static var custom: String { L10n.T("Custom") }

    /// recurrence.go `RecurrenceText`: `start` is the series' first start
    /// (it gives the day where the rule names none). "" for nil.
    public static func recurrenceText(_ r: Recurrence?, start: Date, env: Env) -> String {
        guard let r else { return "" }
        if r.complex {
            return custom
        }
        guard let text = ruleText(r, start, env) else { return custom }
        if r.count > 0 {
            return L10n.N("%s, %d time", "%s, %d times", r.count, text, r.count)
        }
        if let until = r.until, !until.rawValue.isEmpty, let u = parseDate(until, env.zone) {
            return L10n.T("%s, until %s", text, Text.dayMonthYear(u, env))
        }
        return text
    }

    static func ruleText(_ r: Recurrence, _ start: Date, _ env: Env) -> String? {
        let n = max(1, r.interval)
        switch r.frequency {
        case .daily:
            if !(r.byDay ?? []).isEmpty || !(r.byMonthDay ?? []).isEmpty || !(r.byMonth ?? []).isEmpty {
                return nil
            }
            return n == 1 ? L10n.T("Every day") : L10n.N("Every %d day", "Every %d days", n)
        case .weekly: return weeklyText(r, n, start, env)
        case .monthly: return monthlyText(r, n, start, env)
        case .yearly: return yearlyText(r, n, start, env)
        default: return nil
        }
    }

    /// Weekdays in the week's order from Monday.
    static func fromMonday(_ days: [Weekday]) -> [Weekday] {
        stableSorted(days) { compare($0.fromMonday, $1.fromMonday) }
    }

    private static func weeklyText(_ r: Recurrence, _ n: Int, _ start: Date, _ env: Env) -> String? {
        if !(r.byMonthDay ?? []).isEmpty || !(r.byMonth ?? []).isEmpty {
            return nil
        }
        var days = [parts(start, env.zone).weekday]
        if let by = r.byDay, !by.isEmpty {
            days = []
            for c in by {
                guard let (d, ord) = parseDayCode(c), ord == 0 else { return nil }
                if !days.contains(d) {
                    days.append(d)
                }
            }
        }
        days = fromMonday(days)
        if n == 1 && days == [.monday, .tuesday, .wednesday, .thursday, .friday] {
            return L10n.T("Every weekday")
        }
        if n == 1 && days.count == 7 {
            return L10n.T("Every day")
        }
        let list = listText(days.map { weekdayName($0, env) })
        if n == 1 {
            return L10n.T("Every week on %s", list)
        }
        return L10n.N("Every %d week on %s", "Every %d weeks on %s", n, n, list)
    }

    private enum MonthPart {
        case weekday(ordinal: Int, day: Weekday)
        case monthDay(Int)
    }

    /// The "on …" of a monthly or yearly rule.
    private static func monthPart(_ r: Recurrence, _ start: Date, _ env: Env) -> MonthPart? {
        let byDay = r.byDay ?? []
        let byMonthDay = r.byMonthDay ?? []
        if byDay.count == 1 && byMonthDay.isEmpty {
            guard let (d, o) = parseDayCode(byDay[0]), o != 0 else { return nil }
            return .weekday(ordinal: o, day: d)
        }
        if byDay.isEmpty && byMonthDay.count == 1 {
            return byMonthDay[0] != 0 ? .monthDay(byMonthDay[0]) : nil
        }
        if byDay.isEmpty && byMonthDay.isEmpty {
            return .monthDay(parts(start, env.zone).day)
        }
        return nil
    }

    private static func monthlyText(_ r: Recurrence, _ n: Int, _ start: Date, _ env: Env) -> String? {
        if !(r.byMonth ?? []).isEmpty {
            return nil
        }
        guard let part = monthPart(r, start, env) else { return nil }
        let on: String
        switch part {
        case .weekday(let ord, let wd):
            on = L10n.T("on the %s", ordinalWeekday(ord, wd, env))
        case .monthDay(let mday):
            if mday == -1 {
                on = L10n.T("on the last day")
            } else if mday < 0 {
                return nil
            } else {
                on = L10n.T("on day %d", mday)
            }
        }
        if n == 1 {
            return L10n.T("Monthly %s", on)
        }
        return L10n.N("Every %d month %s", "Every %d months %s", n, n, on)
    }

    private static func yearlyText(_ r: Recurrence, _ n: Int, _ start: Date, _ env: Env) -> String? {
        var month = parts(start, env.zone).month
        let byMonth = r.byMonth ?? []
        switch byMonth.count {
        case 0: break
        case 1:
            guard (1...12).contains(byMonth[0]) else { return nil }
            month = byMonth[0]
        default: return nil
        }
        guard let part = monthPart(r, start, env) else { return nil }
        let on: String
        switch part {
        case .weekday(let ord, let wd):
            let monthName = Text.month(date(2026, month, 1, 12, 0, in: env.zone), env)
            on = L10n.T("on the %s of %s", ordinalWeekday(ord, wd, env), monthName)
        case .monthDay(let mday):
            guard mday >= 1 else { return nil }
            // A leap year, so that 29 February has a date.
            let ref = date(2028, month, mday, 12, 0, in: env.zone)
            guard parts(ref, env.zone).month == month else { return nil }
            on = L10n.T("on %s", Text.dayMonth(ref, env))
        }
        if n == 1 {
            return L10n.T("Yearly %s", on)
        }
        return L10n.N("Every %d year %s", "Every %d years %s", n, n, on)
    }

    /// recurrence.go `Repeat`: a choice of the editor's Repeat popup.
    public enum Repeat: Int, Sendable, CaseIterable {
        case never, daily, weekly, biweekly, monthly, yearly
        /// Opens the small popover with interval and days; also the choice
        /// of a rule none of the others is.
        case custom
    }

    /// The choices in the popup's order.
    public static let repeats: [Repeat] = Repeat.allCases

    /// recurrence.go `RepeatTitle`.
    public static func repeatTitle(_ c: Repeat) -> String {
        switch c {
        case .daily: L10n.T("Every Day")
        case .weekly: L10n.T("Every Week")
        case .biweekly: L10n.T("Every 2 Weeks")
        case .monthly: L10n.T("Every Month")
        case .yearly: L10n.T("Every Year")
        case .custom: L10n.T("Custom…")
        case .never: L10n.T("Never")
        }
    }

    /// recurrence.go `RepeatRecurrence`: the rule of a choice for an event
    /// starting at `start` in its `zone`; nil for Never and Custom.
    public static func repeatRecurrence(_ c: Repeat, start: Date, zone: TimeZone) -> Recurrence? {
        let p = parts(start, zone)
        let day = [dayCode(p.weekday)]
        switch c {
        case .daily: return Recurrence(frequency: .daily, interval: 1)
        case .weekly: return Recurrence(frequency: .weekly, interval: 1, byDay: day)
        case .biweekly: return Recurrence(frequency: .weekly, interval: 2, byDay: day)
        case .monthly: return Recurrence(frequency: .monthly, interval: 1, byMonthDay: [p.day])
        case .yearly: return Recurrence(frequency: .yearly, interval: 1, byMonthDay: [p.day], byMonth: [p.month])
        case .never, .custom: return nil
        }
    }

    /// recurrence.go `RepeatOf`: Never for nil, the choice whose rule `r`
    /// is (without an end), else Custom.
    public static func repeatOf(_ r: Recurrence?, start: Date, zone: TimeZone) -> Repeat {
        guard let r else { return .never }
        if r.complex || r.count > 0 || !(r.until?.rawValue ?? "").isEmpty {
            return .custom
        }
        for c in repeats.dropFirst().dropLast() {
            if let rule = repeatRecurrence(c, start: start, zone: zone), sameRule(r, rule) {
                return c
            }
        }
        // A weekly or monthly rule without days takes them from the start.
        if (r.byDay ?? []).isEmpty && (r.byMonthDay ?? []).isEmpty && (r.byMonth ?? []).isEmpty {
            switch (r.frequency, max(1, r.interval)) {
            case (.daily, 1): return .daily
            case (.weekly, 1): return .weekly
            case (.weekly, 2): return .biweekly
            case (.monthly, 1): return .monthly
            case (.yearly, 1): return .yearly
            default: break
            }
        }
        return .custom
    }

    static func sameRule(_ a: Recurrence, _ b: Recurrence) -> Bool {
        a.frequency == b.frequency && max(1, a.interval) == max(1, b.interval) && (a.byDay ?? []) == (b.byDay ?? [])
            && (a.byMonthDay ?? []) == (b.byMonthDay ?? []) && (a.byMonth ?? []) == (b.byMonth ?? [])
    }

    /// recurrence.go `CustomRecurrence`: a frequency, an interval clamped to
    /// 1–`maxRecurrenceInterval` and, for weekly, the days (none: start's)
    /// from Monday.
    public static func customRecurrence(
        _ f: RecurrenceFrequency, interval: Int, days: [Weekday], start: Date, zone: TimeZone
    ) -> Recurrence {
        var r = Recurrence(frequency: f, interval: min(max(1, interval), API.Limits.maxRecurrenceInterval))
        let p = parts(start, zone)
        switch f {
        case .weekly:
            var ds: [Weekday] = []
            for d in fromMonday(days.isEmpty ? [p.weekday] : days) where ds.last != d {
                ds.append(d)
            }
            r.byDay = ds.map(dayCode)
        case .monthly:
            r.byMonthDay = [p.day]
        case .yearly:
            r.byMonth = [p.month]
            r.byMonthDay = [p.day]
        default:
            break
        }
        return r
    }

    /// recurrence.go `IntervalUnit`: "day"/"days", "week"/"weeks", ….
    public static func intervalUnit(_ f: RecurrenceFrequency, _ n: Int) -> String {
        switch f {
        case .weekly: L10n.N("week", "weeks", n)
        case .monthly: L10n.N("month", "months", n)
        case .yearly: L10n.N("year", "years", n)
        default: L10n.N("day", "days", n)
        }
    }

    /// The Custom… popover's label before the interval field.
    public static var everyLabel: String { L10n.T("Every") }
}
