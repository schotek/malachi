// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// The small pure formatting helpers of ui/internal/widget/format.go. Every
// input is attacker-controlled text; callers show the results as plain
// text.

/// The short form of an address for lists and avatars: the name if the
/// backend parsed one, otherwise the bare address (format.go `DisplayName`).
public func displayName(_ a: Address) -> String {
    let name = (a.name ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
    if !name.isEmpty {
        return name
    }
    return a.address.trimmingCharacters(in: .whitespacesAndNewlines)
}

/// The long form: "Name <addr>", or just the address (format.go
/// `FormatAddress`).
public func formatAddress(_ a: Address) -> String {
    let name = (a.name ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
    let addr = a.address.trimmingCharacters(in: .whitespacesAndNewlines)
    if name.isEmpty {
        return addr
    }
    if addr.isEmpty {
        return name
    }
    return name + " <" + addr + ">"
}

/// Joins the display names of a conversation's participants in the order
/// given (newest first), each address once, compared case-insensitively;
/// entries with neither name nor address are skipped (format.go
/// `FormatParticipants`).
public func formatParticipants(_ list: [Address]) -> String {
    var seen = Set<String>()
    var names: [String] = []
    for a in list {
        var key = a.address.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        if key.isEmpty {
            key = "name:" + (a.name ?? "").trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        }
        if key == "name:" || seen.contains(key) {
            continue
        }
        seen.insert(key)
        names.append(displayName(a))
    }
    // TRANSLATORS: put between the names of a conversation's participants ("Alice, Bob").
    return names.joined(separator: L10n.C("participant list separator", ", "))
}

/// The badge of a conversation row: the member count from two on, nothing
/// below (format.go `ThreadCountText`).
public func threadCountText(_ n: Int) -> String {
    n < 2 ? "" : String(n)
}

/// Renders a message date for the list, relative to `now` (format.go
/// `FormatDate`): the time for today, day and month for the current year,
/// the full date otherwise. Go's zero time renders as an empty string.
/// `calendar` decides what "today" is (its time zone) and `locale` the
/// names of months.
public func formatDate(_ t: Date, now: Date, locale: Locale = .current, calendar: Calendar = .current) -> String {
    if t.isGoZero {
        return ""
    }
    let a = calendar.dateComponents([.year, .month, .day], from: t)
    let b = calendar.dateComponents([.year, .month, .day], from: now)
    if a.year == b.year, a.month == b.month, a.day == b.day {
        return formatTime(t, locale: locale, calendar: calendar)
    }
    if a.year == b.year {
        // TRANSLATORS: strftime format for a date in the current year in
        // the message list, e.g. "2 Sep".
        return strftime(t, L10n.T("%-d %b"), locale: locale, calendar: calendar)
    }
    // TRANSLATORS: strftime format for a date in another year in the
    // message list, e.g. "2025-09-02".
    return strftime(t, L10n.T("%Y-%m-%d"), locale: locale, calendar: calendar)
}

/// Renders a wall-clock time (format.go `FormatTime`).
public func formatTime(_ t: Date, locale: Locale = .current, calendar: Calendar = .current) -> String {
    // TRANSLATORS: strftime format for a time of day, e.g. "15:04".
    strftime(t, L10n.T("%H:%M"), locale: locale, calendar: calendar)
}

/// Renders a full date with time (quote headers, "draft saved" status;
/// format.go `FormatDateTime`).
public func formatDateTime(_ t: Date, locale: Locale = .current, calendar: Calendar = .current) -> String {
    // TRANSLATORS: strftime format for a full date and time, e.g.
    // "Wed, 2 Sep 2026 at 15:04".
    strftime(t, L10n.T("%a, %-d %b %Y at %H:%M"), locale: locale, calendar: calendar)
}

/// Renders a byte count for attachment chips: MiB, KiB or B (format.go
/// `FormatSize`).
public func formatSize(_ n: Int) -> String {
    if n >= 1 << 20 {
        // TRANSLATORS: file size in mebibytes.
        return L10n.T("%.1f MiB", Double(n) / Double(1 << 20))
    }
    if n >= 1 << 10 {
        // TRANSLATORS: file size in kibibytes.
        return L10n.T("%.0f KiB", Double(n) / Double(1 << 10))
    }
    // TRANSLATORS: file size in bytes.
    return L10n.T("%d B", n)
}

/// Formats through `DateFormatter` so month and day names follow the
/// locale, in the calendar's time zone (format.go `strftime`).
private func strftime(_ t: Date, _ format: String, locale: Locale, calendar: Calendar) -> String {
    let f = Strftime.formatter(format, locale: locale)
    f.calendar = calendar
    f.timeZone = calendar.timeZone
    return f.string(from: t)
}
