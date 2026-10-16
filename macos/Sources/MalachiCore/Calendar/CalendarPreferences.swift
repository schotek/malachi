// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Preferences → Calendar (Go preferences.go): the group Display (the
// daemon's calendar.preferences) and an account's Calendar and
// Invitations groups (its CalendarConfig).

import Foundation

extension Cal {
    /// api.DefaultCalendarPreferences: the daemon's defaults.
    public static var defaultPreferences: CalendarPreferences {
        CalendarPreferences(
            weekStart: .locale, defaultDurationMinutes: 60, defaultAlertMinutes: 15, showDeclined: false,
            workingHours: WorkingHours(days: ["MO", "TU", "WE", "TH", "FR"], start: "08:00", end: "17:00"))
    }

    public enum Prefs {
        /// The heading of General → Calendar.
        public static var generalGroup: String { Text.modeName }
        public static var weekStartSetting: String { L10n.T("Week Starts On") }

        /// The Week Starts On choices in order.
        public static let weekStarts: [WeekStart] = [.locale, .monday, .sunday, .saturday]

        public static func weekStartTitle(_ w: WeekStart, env: Env) -> String {
            switch w {
            case .monday: weekdayName(.monday, env)
            case .sunday: weekdayName(.sunday, env)
            case .saturday: weekdayName(.saturday, env)
            default: L10n.T("System")
            }
        }

        public static var durationSetting: String { L10n.T("Default Duration") }
        public static var alertSetting: String { L10n.T("Default Alert") }
        public static var declinedSetting: String { L10n.T("Show Declined Events") }
        public static var workingHoursSetting: String { L10n.T("Working Hours") }
        public static var workingHoursSubtitle: String {
            L10n.T("Where the day and week views open, and the hours Find a Free Slot looks in.")
        }

        /// preferences.go `WorkingDaysText`: "Every weekday" for Monday to
        /// Friday, else the list.
        public static func workingDaysText(_ wh: WorkingHours, env: Env) -> String {
            let days = sortedDays(wh.days)
            if days == ["MO", "TU", "WE", "TH", "FR"] {
                return L10n.T("Every weekday")
            }
            return listText(days.compactMap { parseDayCode($0).map { weekdayName($0.day, env) } })
        }

        /// The valid day codes without duplicates from Monday.
        static func sortedDays(_ codes: [String]) -> [String] {
            var days: [Weekday] = []
            for c in codes {
                if let (d, ord) = parseDayCode(c), ord == 0, !days.contains(d) {
                    days.append(d)
                }
            }
            return fromMonday(days).map(dayCode)
        }

        /// "Every weekday · 08:00–17:00".
        public static func workingHoursText(_ wh: WorkingHours, env: Env) -> String {
            let days = workingDaysText(wh, env: env)
            guard let from = parseClock(wh.start), let to = parseClock(wh.end) else { return days }
            // Any day without a clock change will do.
            func at(_ m: Int) -> Date { date(2026, 10, 5, m / 60, m % 60, in: env.zone) }
            return Text.dots(days, Text.range(Text.time(at(from), env), Text.time(at(to), env)))
        }

        public static var accountsGroup: String { Text.modeName }
        public static var syncSetting: String { L10n.T("Sync This Account’s Calendars") }
        public static var syncConsentNote: String {
            L10n.T("Microsoft asks you to allow Malachi Mail to read and change your calendars. Your mail keeps working if you don’t.")
        }

        /// preferences.go `CalendarRowNote`: "default", "read only",
        /// "shared · Jana Nováková"; "".
        public static func calendarRowNote(isDefault: Bool, access: CalendarAccess, owner: Address?) -> String {
            var notes: [String] = []
            if isDefault {
                notes.append(L10n.T("default"))
            }
            if let owner {
                notes.append(L10n.T("shared · %s", nameOf(owner)))
            } else if access == .read {
                notes.append(L10n.T("read only"))
            }
            return Text.dots(notes)
        }

        public static func calendarRowNote(_ c: CalendarRecord) -> String {
            calendarRowNote(isDefault: c.isDefault, access: c.access, owner: c.owner)
        }

        public static var defaultCalendarSetting: String { L10n.T("Default Calendar for New Events") }

        /// preferences.go `Writable`: a calendar new events may go to.
        public static func writable(_ c: CalendarRecord) -> Bool {
            (c.access == .owner || c.access == .write) && c.kind != .birthdays && c.kind != .holidays
        }

        public static var keepSetting: String { L10n.T("Keep Events") }

        /// Keep Events in days.
        public static let keepChoices = [30, 90, 180, 365, 730]

        /// "1 month", "1 year", other values in days.
        public static func keepTitle(_ days: Int) -> String {
            if days > 0 && days % 365 == 0 {
                return L10n.N("%d year", "%d years", days / 365)
            }
            if days > 0 && days % 30 == 0 {
                return L10n.N("%d month", "%d months", days / 30)
            }
            return daysText(days)
        }

        public static var invitationsGroup: String { unansweredHeading }
        public static var cardSetting: String { L10n.T("Show the Invitation Card above the Message in Mail") }
        public static var tentativeSetting: String { L10n.T("Show Unanswered Invitations Tentatively in the Calendar") }
        public static var tentativeSubtitle: String {
            L10n.T("They appear dashed until you answer, and are not in your calendar before you accept.")
        }
        public static var afterAnswerSetting: String { L10n.T("After Answering, Move the Invitation Message") }

        public static let afterAnswers: [AfterAnswer] = [.keep, .archive]

        public static func afterAnswerTitle(_ a: AfterAnswer?) -> String {
            a == .archive ? L10n.T("Archive") : L10n.T("Keep in Inbox")
        }

        public static var noCalendar: String { L10n.T("This account has no calendar.") }
        public static var addCalDAV: String { L10n.T("Add CalDAV Calendar…") }
        public static var addCalendar: String { L10n.T("Add Calendar…") }
        public static var syncMicrosoft: String { L10n.T("Sync a Microsoft 365 Account’s Calendars…") }
        /// The CalDAV account's button that asks for a new password.
        public static var replacePassword: String { L10n.T("Replace Password…") }
        /// The heading of its question.
        public static var replacePasswordTitle: String { L10n.T("Replace the Password?") }
        /// preferences.go `TabTitle`: the Preferences tab.
        public static var tabTitle: String { Text.modeName }
        /// preferences.go `DisplayGroup`: the Calendar tab's first group.
        public static var display: String { L10n.C("calendar preferences", "Display") }
        /// preferences.go `NoCalendarsTitle` / `NoCalendarsBody`.
        public static var noCalendars: String { L10n.T("No calendars yet.") }
        public static var noCalendarsHint: String {
            L10n.T("Add a CalDAV calendar, or switch on calendar sync for a Microsoft 365 account in its section.")
        }
        /// preferences.go `RemoveAccount` (the Accounts tab's msgid).
        public static var removeAccount: String { L10n.T("Remove Account") }
        /// preferences.go `ShowsInAccountsTab`: every kind but caldav.
        public static func showsInAccountsTab(_ a: Account) -> Bool { a.config.kind != .caldav }
        public static var mailAccountSetting: String { L10n.T("Mail Account for Invitations") }
        public static var noMailAccount: String { L10n.C("mail account", "None") }
    }
}
