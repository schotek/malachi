// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The calendar's general texts (Go text.go): the mode, the views, the
// toolbar, the status bar, the empty states, the errors by code, and the
// strftime msgids every other file writes dates with. Keys are the msgids.

import Foundation

extension Cal {
    public enum Text {
        /// The mode switch's segment, the View menu item and the window's
        /// title in Calendar mode (board.CalendarName).
        public static var modeName: String {
            // TRANSLATORS: the window's third mode beside Mail and Board: the calendar.
            L10n.T("Calendar")
        }

        /// A view's segment in the toolbar and its View menu item.
        public static func viewTitle(_ v: View) -> String {
            switch v {
            case .day: L10n.T("Day")
            case .month: L10n.T("Month")
            case .agenda: L10n.T("Agenda")
            case .week: L10n.T("Week")
            }
        }

        public static var today: String { L10n.T("Today") }
        public static var previous: String { L10n.T("Previous") }
        public static var next: String { L10n.T("Next") }
        public static var newEvent: String { L10n.T("New Event") }
        public static var editEvent: String { L10n.T("Edit Event") }
        public static var searchEvents: String { L10n.T("Search Events") }
        public static var untitled: String { L10n.T("Untitled Event") }
        public static var allDay: String { L10n.T("All Day") }

        /// The status bar when every account is synced; `last` is the
        /// oldest last sync.
        public static func syncedLine(_ last: Date, now: Date, zone: TimeZone) -> String {
            L10n.T("Calendars synced %s", Board.Text.relativeTime(last, now: now, calendar: Cal.gregorian(zone)))
        }

        public static var syncingLine: String { L10n.T("Syncing calendars…") }
        public static var offlineLine: String { L10n.T("Calendars are offline") }

        /// The count of the invitations waiting for an answer.
        public static func unansweredCount(_ n: Int) -> String {
            L10n.N("%d unanswered invitation", "%d unanswered invitations", n)
        }

        /// The sidebar's row under the Invitations heading
        /// (`Cal.unansweredHeading`) (Go UnansweredRow).
        public static var unanswered: String { L10n.T("Unanswered") }

        public static var consentLine: String { L10n.T("Calendar access needs a new sign-in") }
        public static var signIn: String { L10n.T("Sign In…") }
        public static var consentOnline: String {
            L10n.T("Allow calendar access in the desktop’s Online Accounts settings.")
        }

        /// text.go `StatusLine`: consent wins, then syncing, then all
        /// offline, then the synced line with the oldest last sync; "".
        public static func statusLine(_ states: [CalendarSyncState], now: Date, zone: TimeZone) -> String {
            var oldest: Date?
            var syncing = false
            var offline = 0
            for s in states {
                switch s.status {
                case .consentRequired: return consentLine
                case .syncing: syncing = true
                case .offline: offline += 1
                default: break
                }
                if let l = s.lastSync, oldest == nil || l < oldest! {
                    oldest = l
                }
            }
            if syncing {
                return syncingLine
            }
            if !states.isEmpty && offline == states.count {
                return offlineLine
            }
            if let oldest {
                return syncedLine(oldest, now: now, zone: zone)
            }
            return ""
        }

        public static func emptyTitle(_ k: EmptyKind) -> String {
            switch k {
            case .syncOff: L10n.T("Calendar Sync Is Off")
            case .loading: L10n.T("Loading Calendars…")
            case .noBackend: L10n.T("Calendar Unavailable")
            case .noAccounts: L10n.T("No Calendars")
            }
        }

        public static func emptyBody(_ k: EmptyKind) -> String {
            switch k {
            case .syncOff:
                L10n.T("Turn on Sync This Account’s Calendars for a Microsoft 365 account in Preferences → Calendar.")
            case .loading: ""
            case .noBackend: L10n.T("The Malachi Mail backend is not running.")
            case .noAccounts:
                L10n.T("Add a CalDAV calendar, or sync the calendars of a Microsoft 365 account in Preferences → Calendar.")
            }
        }

        /// Reloads the event after a conflict or an error.
        public static var reloadEvent: String { L10n.T("Reload Event") }

        public static var openPreferences: String { L10n.T("Open Preferences") }

        /// What a write says over the sample data.
        public static var previewOnly: String { L10n.T("The preview cannot do that.") }

        /// text.go `ErrorText`: nil for a code without a calendar sentence.
        public static func errorText(_ code: ErrorCode) -> String? {
            switch code {
            case .readOnly: return L10n.T("This calendar or event cannot be changed.")
            case .eventGone, .eventNotFound: return L10n.T("The event no longer exists.")
            case .calendarNotFound: return L10n.T("The calendar no longer exists.")
            case .conflict: return L10n.T("The event was changed elsewhere. It has been reloaded.")
            case .invitationsUnavailable:
                return L10n.T(
                    "No account can send the invitations of this calendar. Choose a mail account for invitations in Preferences → Calendar.")
            case .calendarConsentRequired: return consentLine
            case .recurrenceUnsupported:
                return L10n.T(
                    "The event repeats in a way Malachi Mail cannot show. Only its first occurrence is shown, and it can be changed only as a whole series.")
            case .malformedCalendar: return L10n.T("The calendar data could not be read.")
            case .offline: return L10n.T("The calendar cannot be changed while offline.")
            default: return nil
            }
        }

        // MARK: Date formats

        /// "10:00".
        public static func time(_ t: Date, _ env: Env) -> String {
            // TRANSLATORS: strftime format for a time of day, e.g. "15:04".
            env.date(t, L10n.T("%H:%M"))
        }

        /// "Tuesday 14 October 2026".
        public static func longDate(_ t: Date, _ env: Env) -> String { env.date(t, L10n.T("%A %-d %B %Y")) }
        /// "Tuesday 14 October".
        public static func weekdayDate(_ t: Date, _ env: Env) -> String { env.date(t, L10n.T("%A %-d %B")) }
        /// "14 October".
        public static func dayMonth(_ t: Date, _ env: Env) -> String { env.date(t, L10n.T("%-d %B")) }
        /// "14 October 2026".
        public static func dayMonthYear(_ t: Date, _ env: Env) -> String { env.date(t, L10n.T("%-d %B %Y")) }
        /// "14" (Go DayText).
        public static func dayOfMonth(_ t: Date, _ env: Env) -> String { env.date(t, L10n.T("%-d")) }
        /// "October 2026".
        public static func monthYear(_ t: Date, _ env: Env) -> String { env.date(t, L10n.T("%OB %Y")) }
        /// "October".
        public static func month(_ t: Date, _ env: Env) -> String { env.date(t, L10n.T("%OB")) }
        /// "Tuesday".
        public static func weekday(_ t: Date, _ env: Env) -> String { env.date(t, L10n.T("%A")) }
        /// "Tue".
        public static func shortWeekday(_ t: Date, _ env: Env) -> String { env.date(t, L10n.T("%a")) }
        /// "Oct".
        public static func shortMonth(_ t: Date, _ env: Env) -> String { env.date(t, L10n.T("%b")) }
        /// "Tue 14 Oct 2026 10:00".
        public static func dateTime(_ t: Date, _ env: Env) -> String { env.date(t, L10n.T("%a %-d %b %Y %H:%M")) }

        /// "12–18 October", "10:00–11:30".
        public static func range(_ from: String, _ to: String) -> String { L10n.T("%s–%s", from, to) }

        /// "Work · Calendar".
        public static func dot(_ a: String, _ b: String) -> String { L10n.T("%s · %s", a, b) }

        /// The non-empty parts joined with `dot`.
        static func dots(_ parts: String...) -> String { dots(parts) }

        static func dots(_ parts: [String]) -> String {
            var out = ""
            for p in parts where !p.isEmpty {
                out = out.isEmpty ? p : dot(out, p)
            }
            return out
        }

        /// A timed span's times, "10:00–11:30".
        public static func timeRange(_ start: Date, _ end: Date, _ env: Env) -> String {
            range(time(start, env), time(end, env))
        }
    }

    /// text.go `EmptyKind`: why the calendar page has nothing to show.
    public enum EmptyKind: Sendable, Equatable, CaseIterable {
        /// No account has the calendar capability.
        case noAccounts
        /// Accounts could have calendars, none syncs them.
        case syncOff
        /// The first calendar.list has not answered.
        case loading
        /// The daemon is not reachable.
        case noBackend
    }
}
