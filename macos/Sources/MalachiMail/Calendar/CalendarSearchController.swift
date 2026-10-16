// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The search results in the agenda table (docs/calendar.md §5.4). The
/// toolbar's field hands the text to `CalendarController.setSearch` (M4),
/// which runs `event.search` from 2 characters 300 ms after the last key
/// and owns Escape (`handleKey`: the popover first, then the search);
/// this only lays the answer out: under the heading "Results", in the
/// daemon's order (upcoming by start, then past, latest first), one row
/// per event with its day in the time column, since the results are not
/// one range of days.
@MainActor
enum CalendarSearchController {
    /// Whether the content shows the results (the controller is searching
    /// and has an answer).
    static func active(_ c: CalendarController) -> Bool {
        c.searchResults != nil
    }

    /// The rows of the results: no day groups, each row's day before its
    /// time ("Tomorrow · 10:00–10:30").
    static func entries(_ c: CalendarController, now: Date = Date()) -> [AgendaEntry] {
        let env = c.env
        return (c.searchResults ?? []).compactMap { o -> AgendaEntry? in
            guard let (s, e) = Cal.span(o, env.zone) else { return nil }
            let item = Cal.AgendaItem(occurrence: o, start: s, end: e, dayOf: 1, days: 1, state: Cal.stateOf(o))
            let day = Cal.dayHeading(Cal.startOfDay(s, env.zone), now: now, env: env)
            return .item(item, time: Cal.Text.dot(day, Cal.itemTime(item, env: env)))
        }
    }
}
