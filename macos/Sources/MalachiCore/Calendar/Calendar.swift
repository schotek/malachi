// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The calendar's pure UI logic: a 1:1 port of the Go reference package
// ui/internal/calendar (docs/calendar.md §5.1), which holds the msgids. This
// file is format.go and view.go: the views and their ranges, the day
// arithmetic, how an occurrence spans a zone, and the strftime writer the
// date texts go through.
//
// The namespace is `Cal`, not `Calendar`: a module-level `Calendar` would
// shadow Foundation's in the rest of MalachiCore. Go's `time.Time` carries
// its zone; a `Date` does not, so every function that needs a day takes the
// zone explicitly (never the current one). Days are midnights made from
// wall-clock parts in that zone, so a day of 23 or 25 hours (a
// daylight-saving change) is still one day.

import Foundation

/// The calendar's pure logic and texts (Go package `calendar`).
public enum Cal {}

// MARK: - Weekday

extension Cal {
    /// Go's `time.Weekday`: Sunday is 0.
    public enum Weekday: Int, Sendable, CaseIterable, Comparable {
        case sunday = 0, monday, tuesday, wednesday, thursday, friday, saturday

        public static func < (a: Weekday, b: Weekday) -> Bool { a.rawValue < b.rawValue }

        /// The weekday of Foundation's `Calendar.firstWeekday` (1 Sunday).
        public init(firstWeekday: Int) {
            self = Weekday(rawValue: ((firstWeekday - 1) % 7 + 7) % 7) ?? .monday
        }

        /// Position in the week from Monday (Monday 0, Sunday 6), the order
        /// people read weekdays in.
        var fromMonday: Int { (rawValue + 6) % 7 }
    }
}

// MARK: - Env and strftime

extension Cal {
    /// The strftime writer: `t` written in `zone` with a strftime format
    /// (a msgid already translated). GTK passes GLib's, macOS
    /// `DateFormatter` through `Strftime`, the tests `englishStrftime`.
    public typealias StrftimeWriter = @Sendable (_ t: Date, _ format: String, _ zone: TimeZone) -> String

    /// format.go `Env`: what the texts with dates are written with. The
    /// translator is the global `L10n`; `zone` decides what a day is.
    public struct Env: Sendable {
        public var zone: TimeZone
        public var format: StrftimeWriter

        public init(zone: TimeZone, format: @escaping StrftimeWriter) {
            self.zone = zone
            self.format = format
        }

        /// The user's locale through `DateFormatter` (Strftime patterns).
        public init(zone: TimeZone, locale: Locale) {
            self.zone = zone
            self.format = { t, f, z in
                let df = Strftime.formatter(f, locale: locale)
                var cal = Foundation.Calendar(identifier: .gregorian)
                cal.timeZone = z
                df.calendar = cal
                df.timeZone = z
                return df.string(from: t)
            }
        }

        /// The C-locale writer of the tests and the samples.
        public static func english(_ zone: TimeZone) -> Env {
            Env(zone: zone, format: { t, f, z in Cal.englishStrftime(t, f, zone: z) })
        }

        /// Writes `t` in the env's zone with a translated format.
        func date(_ t: Date, _ format: String) -> String {
            self.format(t, format, zone)
        }
    }

    private static let englishDays = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"]
    private static let englishMonths = [
        "January", "February", "March", "April", "May", "June", "July",
        "August", "September", "October", "November", "December",
    ]

    /// format.go `EnglishStrftime`: `t` in `zone` with `format` in the C
    /// locale. It knows %a %A %b %B %OB %d %e %H %I %M %m %p %Y %y %% and the
    /// "-" flag that drops the padding; anything else is copied.
    public static func englishStrftime(_ t: Date, _ format: String, zone: TimeZone) -> String {
        let p = parts(t, zone)
        let f = Array(format.utf8)
        var out: [UInt8] = []
        func put(_ s: String) { out.append(contentsOf: Array(s.utf8)) }
        var i = 0
        while i < f.count {
            let c = f[i]
            if c != UInt8(ascii: "%") || i + 1 == f.count {
                out.append(c)
                i += 1
                continue
            }
            i += 1
            if f[i] == UInt8(ascii: "O") && i + 1 < f.count && f[i + 1] == UInt8(ascii: "B") {
                // %OB: the standalone month name; the same in English.
                put(englishMonths[p.month - 1])
                i += 2
                continue
            }
            var pad = true
            if f[i] == UInt8(ascii: "-") && i + 1 < f.count {
                pad = false
                i += 1
            }
            // A C length modifier ("%ld", "%-ld") a catalogue generator may
            // have added: the directive is the letter after it.
            var lengths = 0
            while lengths < 2 && f[i] == UInt8(ascii: "l") && i + 1 < f.count {
                lengths += 1
                i += 1
            }
            func num(_ n: Int) { put(pad ? String(format: "%02d", n) : String(n)) }
            switch f[i] {
            case UInt8(ascii: "a"): put(String(englishDays[p.weekday.rawValue].prefix(3)))
            case UInt8(ascii: "A"): put(englishDays[p.weekday.rawValue])
            case UInt8(ascii: "b"): put(String(englishMonths[p.month - 1].prefix(3)))
            case UInt8(ascii: "B"): put(englishMonths[p.month - 1])
            case UInt8(ascii: "d"): num(p.day)
            case UInt8(ascii: "e"): put(pad ? String(format: "%2d", p.day) : String(p.day))
            case UInt8(ascii: "H"): num(p.hour)
            case UInt8(ascii: "I"):
                let h = p.hour % 12
                num(h == 0 ? 12 : h)
            case UInt8(ascii: "M"): num(p.minute)
            case UInt8(ascii: "m"): num(p.month)
            case UInt8(ascii: "p"): put(p.hour < 12 ? "AM" : "PM")
            case UInt8(ascii: "Y"): put(String(p.year))
            case UInt8(ascii: "y"): put(String(format: "%02d", p.year % 100))
            case UInt8(ascii: "%"): out.append(UInt8(ascii: "%"))
            default:
                out.append(UInt8(ascii: "%"))
                if !pad { out.append(UInt8(ascii: "-")) }
                out.append(f[i])
            }
            i += 1
        }
        return String(decoding: out, as: UTF8.self)
    }
}

// MARK: - Wall-clock arithmetic

extension Cal {
    /// The wall-clock parts of an instant in a zone.
    public struct Parts: Sendable, Equatable {
        public var year, month, day, hour, minute, second: Int
        public var weekday: Weekday
    }

    static func gregorian(_ zone: TimeZone) -> Foundation.Calendar {
        var c = Foundation.Calendar(identifier: .gregorian)
        c.timeZone = zone
        return c
    }

    /// The wall-clock parts of `t` in `zone`.
    public static func parts(_ t: Date, _ zone: TimeZone) -> Parts {
        let c = gregorian(zone).dateComponents([.year, .month, .day, .hour, .minute, .second, .weekday], from: t)
        return Parts(
            year: c.year ?? 1, month: c.month ?? 1, day: c.day ?? 1, hour: c.hour ?? 0, minute: c.minute ?? 0,
            second: c.second ?? 0, weekday: Weekday(rawValue: ((c.weekday ?? 1) - 1) % 7) ?? .sunday)
    }

    /// Days since 1970-01-01 of a proleptic Gregorian date (H. Hinnant).
    static func daysFromCivil(_ y0: Int, _ m: Int, _ d: Int) -> Int {
        let y = m <= 2 ? y0 - 1 : y0
        let era = (y >= 0 ? y : y - 399) / 400
        let yoe = y - era * 400
        let doy = (153 * (m > 2 ? m - 3 : m + 9) + 2) / 5 + d - 1
        let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy
        return era * 146_097 + doe - 719_468
    }

    /// The date of a day number of `daysFromCivil`.
    static func civilFromDays(_ z0: Int) -> (year: Int, month: Int, day: Int) {
        let z = z0 + 719_468
        let era = (z >= 0 ? z : z - 146_096) / 146_097
        let doe = z - era * 146_097
        let yoe = (doe - doe / 1460 + doe / 36524 - doe / 146_096) / 365
        let y = yoe + era * 400
        let doy = doe - (365 * yoe + yoe / 4 - yoe / 100)
        let mp = (5 * doy + 2) / 153
        let d = doy - (153 * mp + 2) / 5 + 1
        let m = mp < 10 ? mp + 3 : mp - 9
        return (m <= 2 ? y + 1 : y, m, d)
    }

    private static func floorDiv(_ a: Int, _ b: Int) -> (q: Int, r: Int) {
        var q = a / b
        var r = a % b
        if r < 0 {
            q -= 1
            r += b
        }
        return (q, r)
    }

    /// Go's `time.Date`: the instant of a wall-clock time in `zone`, every
    /// part normalised the way Go does (month 13 is January of the next
    /// year, day 0 the last day of the month before, minute 60 the next
    /// hour). A time the clocks skip is moved on by the gap; a repeated one
    /// is its first instance.
    public static func date(
        _ year: Int, _ month: Int, _ day: Int, _ hour: Int = 0, _ minute: Int = 0, _ second: Int = 0, in zone: TimeZone
    ) -> Date {
        let ym = floorDiv(year * 12 + (month - 1), 12)
        let sec = floorDiv(second, 60)
        var carry = sec.q
        let s = sec.r
        let mi: Int
        (carry, mi) = floorDiv(minute + carry, 60)
        let h: Int
        (carry, h) = floorDiv(hour + carry, 24)
        let civil = civilFromDays(daysFromCivil(ym.q, ym.r + 1, 1) + day - 1 + carry)
        var c = DateComponents()
        c.year = civil.year
        c.month = civil.month
        c.day = civil.day
        c.hour = h
        c.minute = mi
        c.second = s
        if let d = gregorian(zone).date(from: c) {
            return d
        }
        // Not reachable for a Gregorian date; keep a usable instant.
        let secs = daysFromCivil(civil.year, civil.month, civil.day) * 86400 + h * 3600 + mi * 60 + s
        return Date(timeIntervalSince1970: TimeInterval(secs - zone.secondsFromGMT()))
    }

    /// view.go `StartOfDay`: the midnight of `t`'s day in `zone`.
    public static func startOfDay(_ t: Date, _ zone: TimeZone) -> Date {
        let p = parts(t, zone)
        return date(p.year, p.month, p.day, in: zone)
    }

    /// view.go `AddDays`: the midnight `n` calendar days after `day`'s.
    public static func addDays(_ day: Date, _ n: Int, _ zone: TimeZone) -> Date {
        let p = parts(day, zone)
        return date(p.year, p.month, p.day + n, in: zone)
    }

    /// view.go `SameDay`.
    public static func sameDay(_ a: Date, _ b: Date, _ zone: TimeZone) -> Bool {
        startOfDay(a, zone) == startOfDay(b, zone)
    }

    /// view.go `DaysBetween`: calendar days from `a`'s day to `b`'s.
    public static func daysBetween(_ a: Date, _ b: Date, _ zone: TimeZone) -> Int {
        let x = parts(a, zone)
        let y = parts(b, zone)
        return daysFromCivil(y.year, y.month, y.day) - daysFromCivil(x.year, x.month, x.day)
    }

    /// view.go `StartOfWeek`: the midnight of the first day of `t`'s week.
    public static func startOfWeek(_ t: Date, _ weekStart: Weekday, _ zone: TimeZone) -> Date {
        let d = startOfDay(t, zone)
        let back = (parts(d, zone).weekday.rawValue - weekStart.rawValue + 7) % 7
        return addDays(d, -back, zone)
    }
}

// MARK: - Views and ranges (view.go)

extension Cal {
    /// view.go `View`: what the calendar page shows (key calendar-view).
    public enum View: Int, Sendable, CaseIterable {
        case day, week, month, agenda

        /// The view's value of the key calendar-view.
        public var nick: String {
            switch self {
            case .day: "day"
            case .week: "week"
            case .month: "month"
            case .agenda: "agenda"
            }
        }
    }

    /// The views in the toolbar's order.
    public static let views: [View] = [.day, .week, .month, .agenda]

    /// The key calendar-view's default.
    public static let defaultView: View = .week

    /// The span of an agenda page.
    public static let agendaDays = 30

    /// view.go `ParseView`: an unknown nick is the default with false.
    public static func parseView(_ nick: String) -> (View, Bool) {
        if let v = views.first(where: { $0.nick == nick }) {
            return (v, true)
        }
        return (defaultView, false)
    }

    /// view.go `Range`: [from, to), both midnights in `zone`.
    public struct Range: Sendable, Equatable {
        public var from: Date
        public var to: Date
        public var zone: TimeZone

        public init(from: Date, to: Date, zone: TimeZone) {
            self.from = from
            self.to = to
            self.zone = zone
        }

        /// The midnights of the range.
        public var days: [Date] {
            var out: [Date] = []
            var d = from
            while d < to {
                out.append(d)
                d = Cal.addDays(d, 1, zone)
            }
            return out
        }

        /// `t` within [from, to).
        public func contains(_ t: Date) -> Bool { t >= from && t < to }
    }

    /// view.go `ViewRange`: the day, the week from `weekStart`, the month's
    /// 6×7 grid, or `agendaDays` days from the anchor's day.
    public static func viewRange(_ v: View, anchor: Date, weekStart: Weekday, zone: TimeZone) -> Range {
        switch v {
        case .day:
            let d = startOfDay(anchor, zone)
            return Range(from: d, to: addDays(d, 1, zone), zone: zone)
        case .month:
            let first = gridStart(anchor, weekStart, zone)
            return Range(from: first, to: addDays(first, 42, zone), zone: zone)
        case .agenda:
            let d = startOfDay(anchor, zone)
            return Range(from: d, to: addDays(d, agendaDays, zone), zone: zone)
        case .week:
            let w = startOfWeek(anchor, weekStart, zone)
            return Range(from: w, to: addDays(w, 7, zone), zone: zone)
        }
    }

    /// view.go `Step`: the anchor after ‹ (-1) or › (+1); a month step
    /// clamps the day to the new month's length.
    public static func step(_ v: View, anchor: Date, _ dir: Int, zone: TimeZone) -> Date {
        let d = startOfDay(anchor, zone)
        switch v {
        case .day:
            return addDays(d, dir, zone)
        case .month:
            let p = parts(d, zone)
            let first = parts(date(p.year, p.month + dir, 1, in: zone), zone)
            let last = parts(addDays(date(first.year, first.month + 1, 1, in: zone), -1, zone), zone).day
            return date(first.year, first.month, min(p.day, last), in: zone)
        case .agenda:
            return addDays(d, dir * agendaDays, zone)
        case .week:
            return addDays(d, 7 * dir, zone)
        }
    }

    /// view.go `SwitchAnchor`: the anchor after switching from one view to
    /// another. Switching to the Agenda starts it at today's midnight,
    /// whatever day the other view showed; switching from the Agenda, and
    /// every other switch, keeps the anchor.
    public static func switchAnchor(from: View, to: View, anchor: Date, today: Date, zone: TimeZone) -> Date {
        if to == .agenda && from != .agenda {
            return startOfDay(today, zone)
        }
        return anchor
    }

    /// view.go `WeekStart`: the preference, or the locale's first day.
    public static func weekStart(_ pref: WeekStart, localeFirstDay: Weekday) -> Weekday {
        switch pref {
        case .monday: .monday
        case .sunday: .sunday
        case .saturday: .saturday
        default: localeFirstDay
        }
    }

    /// view.go `WeekNumber`: ISO 8601 when weeks start on Monday, else the
    /// US rule (week 1 holds 1 January).
    public static func weekNumber(_ day: Date, weekStart: Weekday, zone: TimeZone) -> Int {
        let d = startOfDay(day, zone)
        if weekStart == .monday {
            let p = parts(d, zone)
            let n = daysFromCivil(p.year, p.month, p.day)
            let thursday = n + 3 - p.weekday.fromMonday
            let ty = civilFromDays(thursday).year
            return (thursday - daysFromCivil(ty, 1, 1)) / 7 + 1
        }
        let first = startOfWeek(d, weekStart, zone)
        let last = addDays(first, 6, zone)
        let jan1 = date(parts(last, zone).year, 1, 1, in: zone)
        let week1 = startOfWeek(jan1, weekStart, zone)
        return daysBetween(week1, first, zone) / 7 + 1
    }

    /// view.go `Title`: the day's date, or the month and year (a week
    /// across two months names both).
    public static func title(_ v: View, _ r: Range, env: Env) -> String {
        switch v {
        case .day:
            return Text.dayMonthYear(r.from, env)
        case .month:
            return Text.monthYear(addDays(r.from, 14, r.zone), env)
        case .agenda:
            return Text.monthYear(r.from, env)
        case .week:
            break
        }
        let last = addDays(r.to, -1, r.zone)
        let a = parts(r.from, r.zone)
        let b = parts(last, r.zone)
        if a.month == b.month && a.year == b.year {
            return Text.monthYear(r.from, env)
        }
        if a.year == b.year {
            return Text.range(Text.month(r.from, env), Text.monthYear(last, env))
        }
        return Text.range(Text.monthYear(r.from, env), Text.monthYear(last, env))
    }

    /// view.go `Subtitle`: "12–18 October · week 42"; "" for Month.
    public static func subtitle(_ v: View, _ r: Range, weekStart: Weekday, env: Env) -> String {
        let zone = env.zone
        switch v {
        case .day:
            return Text.dot(Text.weekday(r.from, env), weekLabel(weekNumber(r.from, weekStart: weekStart, zone: zone)))
        case .month:
            return ""
        case .agenda:
            return Text.range(Text.dayMonth(r.from, env), Text.dayMonthYear(addDays(r.to, -1, r.zone), env))
        case .week:
            break
        }
        let last = addDays(r.to, -1, r.zone)
        let a = parts(r.from, r.zone)
        let b = parts(last, r.zone)
        let days: String
        if a.month == b.month && a.year == b.year {
            days = Text.range(Text.dayOfMonth(r.from, env), Text.dayMonth(last, env))
        } else if a.year == b.year {
            days = Text.range(Text.dayMonth(r.from, env), Text.dayMonth(last, env))
        } else {
            days = Text.range(Text.dayMonthYear(r.from, env), Text.dayMonthYear(last, env))
        }
        return Text.dot(days, weekLabel(weekNumber(r.from, weekStart: weekStart, zone: zone)))
    }

    /// view.go `WeekLabel`: "week 42".
    public static func weekLabel(_ n: Int) -> String {
        // TRANSLATORS: the number of a week of the year, in the calendar's
        // subtitle "12–18 October · week 42".
        L10n.T("week %d", n)
    }
}

// MARK: - Occurrences in a zone (view.go)

extension Cal {
    /// view.go `ParseDate`: the midnight of an API date in `zone`.
    public static func parseDate(_ d: CalendarDay?, _ zone: TimeZone) -> Date? {
        guard let p = d?.parts else { return nil }
        return date(p.year, p.month, p.day, in: zone)
    }

    /// view.go `DateOf`: `t`'s calendar date in `zone`.
    public static func dateOf(_ t: Date, _ zone: TimeZone) -> CalendarDay {
        let p = parts(t, zone)
        return CalendarDay(String(format: "%04d-%02d-%02d", p.year, p.month, p.day))
    }

    /// view.go `Span`: an occurrence's [start, end) in `zone` (an all-day
    /// one's midnights, an end not after the start the next day); nil
    /// without a usable time.
    public static func span(_ o: Occurrence, _ zone: TimeZone) -> (start: Date, end: Date)? {
        if o.allDay {
            guard let s = parseDate(o.startDate, zone) else { return nil }
            var e = parseDate(o.endDate, zone) ?? s
            if e <= s {
                e = addDays(s, 1, zone)
            }
            return (s, e)
        }
        guard let s = o.start else { return nil }
        var e = s
        if let end = o.end, end > s {
            e = end
        }
        return (s, e)
    }

    /// view.go `OccurrenceKey`: the event and its recurrence id, or the
    /// invitation message of one not in a calendar.
    public static func occurrenceKey(_ o: Occurrence) -> String {
        let event = o.eventId?.rawValue ?? ""
        if event.isEmpty, let inv = o.invitation {
            return "inv:" + inv.accountId.rawValue + "/" + inv.messageId.rawValue
        }
        return event + "|" + (o.recurrenceId?.wire ?? "")
    }

    /// view.go `Overlaps`: touches [from, to); one of no length counts at
    /// its start.
    public static func overlaps(_ o: Occurrence, from: Date, to: Date, _ zone: TimeZone) -> Bool {
        guard let (s, e) = span(o, zone) else { return false }
        if e == s {
            return s >= from && s < to
        }
        return s < to && e > from
    }
}

// MARK: - Calendars (calendars.go)

extension Cal {
    /// calendars.go `ActiveCalendars`: the calendars of calendar.list that
    /// are not disabled, in the order given. It applies wherever calendars
    /// appear outside Preferences (sidebar, visible set, editor popup,
    /// detail, free/busy and search scopes, counts); only Preferences →
    /// Calendar lists the disabled ones, so their checkbox can turn them
    /// back on.
    public static func activeCalendars(_ cals: [CalendarRecord]) -> [CalendarRecord] {
        cals.filter { !$0.disabled }
    }
}

// MARK: - Helpers shared by the files

extension Cal {
    /// Go's `cmp.Compare` of strings: by their bytes.
    static func compareBytes(_ a: String, _ b: String) -> Int {
        if a == b { return 0 }
        return a.utf8.lexicographicallyPrecedes(b.utf8) ? -1 : 1
    }

    static func compare<T: Comparable>(_ a: T, _ b: T) -> Int {
        a < b ? -1 : (a > b ? 1 : 0)
    }

    /// Go's `cmp.Or`: the first non-zero comparison.
    static func or(_ cs: Int...) -> Int {
        cs.first { $0 != 0 } ?? 0
    }

    /// Go's `slices.SortStableFunc`.
    static func stableSorted<T>(_ a: [T], by cmp: (T, T) -> Int) -> [T] {
        a.enumerated().sorted { x, y in
            let c = cmp(x.element, y.element)
            return c != 0 ? c < 0 : x.offset < y.offset
        }.map(\.element)
    }

    /// An event's title for display: cleaned, Untitled when empty.
    static func cleanTitle(_ s: String?) -> String {
        let t = Board.cleanLine(s ?? "", max: API.Limits.maxEventTitleBytes)
        return t.isEmpty ? Text.untitled : t
    }

    /// A server's one-line string for display.
    static func cleanLine(_ s: String?) -> String {
        Board.cleanLine(s ?? "", max: API.Limits.maxEventLocationBytes)
    }

    /// A person's name, else the address.
    static func nameOf(_ a: Address?) -> String {
        guard let a else { return "" }
        let n = cleanLine(a.name)
        return n.isEmpty ? cleanLine(a.address) : n
    }

    /// Go's `strings.TrimSpace` (Unicode White_Space).
    static func trimSpace(_ s: String) -> String {
        let u = s.unicodeScalars
        guard let first = u.firstIndex(where: { !$0.properties.isWhitespace }),
              let last = u.lastIndex(where: { !$0.properties.isWhitespace }) else { return "" }
        return String(u[first...last])
    }
}
