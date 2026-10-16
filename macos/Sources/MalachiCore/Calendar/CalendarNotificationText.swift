// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The calendar's notifications (Go notifications.go): an event's alert
// (notify.eventAlert) with Join and Snooze, a new invitation with Accept
// and Show, a cancellation with Remove from Calendar.

import Foundation

extension Cal {
    /// How long Snooze puts an alert off.
    public static let snoozeMinutes = 5

    /// notifications.go `Notification`.
    public struct Notification: Sendable, Equatable {
        public var title: String
        public var body: String

        public init(title: String, body: String) {
            self.title = title
            self.body = body
        }
    }

    /// notifications.go `AlertWhen`: "now", "in 5 minutes", "at 12:00", the
    /// date and time; an all-day event "today" or its date.
    public static func alertWhen(_ a: EventAlert, now: Date, env: Env) -> String {
        let zone = env.zone
        if a.allDay {
            guard let d = parseDate(a.startDate, zone) else { return "" }
            if sameDay(d, now, zone) {
                return L10n.T("today")
            }
            return Text.weekdayDate(d, env)
        }
        guard let start = a.start else { return "" }
        let m = Int((start.timeIntervalSince(now) / 60).rounded())
        if m <= 0 {
            return L10n.C("alert", "now")
        }
        if m < 60 {
            return L10n.N("in %d minute", "in %d minutes", m)
        }
        if sameDay(start, now, zone) {
            return L10n.T("at %s", Text.time(start, env))
        }
        return Text.dots(Text.weekdayDate(start, env), Text.time(start, env))
    }

    /// notifications.go `AlertNotification`: "in 5 minutes · Room B".
    public static func alertNotification(_ a: EventAlert, now: Date, env: Env) -> Notification {
        Notification(title: cleanTitle(a.title), body: Text.dots(alertWhen(a, now: now, env: env), cleanLine(a.location)))
    }

    public static var join: String { L10n.T("Join") }
    public static var snooze: String { L10n.T("Snooze") }

    /// notifications.go `InvitationNotification`.
    public static func invitationNotification(_ s: InvitationSummary, env: Env) -> Notification {
        let from = nameOf(s.organizer)
        let title: String
        if s.kind == .update && !from.isEmpty {
            title = L10n.T("%s updated the event", from)
        } else if s.kind == .update {
            title = L10n.T("An event was updated")
        } else if !from.isEmpty {
            title = L10n.T("New invitation from %s", from)
        } else {
            title = L10n.T("New invitation")
        }
        return Notification(title: title, body: summaryBody(s, env))
    }

    /// "Q4 planning · Tuesday 14 October 2026 · 10:00–11:30 · 1 h 30 min".
    static func summaryBody(_ s: InvitationSummary, _ env: Env) -> String {
        var o = bareOccurrence(start: s.start, end: s.end, startDate: s.startDate, allDay: s.allDay)
        if s.allDay, let d = parseDate(s.startDate, env.zone) {
            o.endDate = dateOf(addDays(d, 1, env.zone), env.zone)
        }
        return Text.dots(cleanTitle(s.title), timeLine(o, env: env))
    }

    public static var show: String { L10n.T("Show") }

    /// notifications.go `CancellationNotification`.
    public static func cancellationNotification(_ s: InvitationSummary, env: Env) -> Notification {
        let from = nameOf(s.organizer)
        let title = from.isEmpty ? L10n.T("An event was cancelled") : L10n.T("%s cancelled the event", from)
        return Notification(title: title, body: summaryBody(s, env))
    }
}
