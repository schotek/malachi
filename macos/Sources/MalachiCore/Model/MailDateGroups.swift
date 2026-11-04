// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// Inbox presentation; the GTK counterpart is ui/internal/maildate.
public enum MailDateGroup: Hashable, Sendable {
    case flagged, today, yesterday, thisWeek, lastWeek, thisMonth, lastMonth, thisYear
    case year(Int)

    public var title: String {
        // Shared GTK msgids from ui/internal/maildate/groups.go.
        switch self {
        case .flagged: return L10n.T("Flagged")
        case .today: return L10n.T("Today")
        case .yesterday: return L10n.T("Yesterday")
        case .thisWeek: return L10n.T("This week")
        case .lastWeek: return L10n.T("Last week")
        case .thisMonth: return L10n.T("This month")
        case .lastMonth: return L10n.T("Last month")
        case .thisYear: return L10n.T("This year")
        case .year(let year): return String(year)
        }
    }

    public static func containing(_ date: Date, now: Date, calendar: Calendar) -> Self {
        let today = calendar.startOfDay(for: now)
        if date >= today { return .today }
        let yesterday = calendar.date(byAdding: .day, value: -1, to: today)!
        if date >= yesterday { return .yesterday }
        let week = calendar.dateInterval(of: .weekOfYear, for: now)!.start
        if date >= week { return .thisWeek }
        let lastWeek = calendar.date(byAdding: .weekOfYear, value: -1, to: week)!
        if date >= lastWeek { return .lastWeek }
        let month = calendar.dateInterval(of: .month, for: now)!.start
        if date >= month { return .thisMonth }
        let lastMonth = calendar.date(byAdding: .month, value: -1, to: month)!
        if date >= lastMonth { return .lastMonth }
        let year = calendar.dateInterval(of: .year, for: now)!.start
        if date >= year { return .thisYear }
        return .year(calendar.component(.year, from: date))
    }
}

public enum MailDateItem: Hashable, Sendable {
    case heading(MailDateGroup)
    case message(ListKey)

    public var messageKey: ListKey? {
        if case .message(let key) = self { return key }
        return nil
    }
}

public enum MailDateGroups {
    /// Members stay beneath their parent even when their own date is older.
    public static func items(
        rows: [ListRow], collapsed: Set<MailDateGroup> = [],
        now: Date = Date(), calendar: Calendar = .autoupdatingCurrent
    ) -> [MailDateItem] {
        var order: [MailDateGroup] = []
        var groups: [MailDateGroup: [ListKey]] = [:]
        var parent: MailDateGroup?
        for row in rows {
            let ownGroup: MailDateGroup = hasFlag(row.summary?.flags ?? row.message.flags, .flagged)
                ? .flagged : .containing(row.summary?.latestDate ?? row.message.date, now: now, calendar: calendar)
            let group = row.member ? (parent ?? ownGroup) : ownGroup
            if !row.member { parent = group }
            if groups[group] == nil { order.append(group) }
            groups[group, default: []].append(row.key)
        }
        // An old starred message belongs before Today, even when fetched on a later page.
        if groups[.flagged] != nil {
            order.removeAll { $0 == .flagged }
            order.insert(.flagged, at: 0)
        }
        return order.flatMap { group in
            [.heading(group)] + (collapsed.contains(group) ? [] : groups[group, default: []].map(MailDateItem.message))
        }
    }
}
