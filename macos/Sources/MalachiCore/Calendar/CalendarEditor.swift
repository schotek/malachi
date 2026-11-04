// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The event editor, New Event / Edit Event (Go editor.go): where a new
// event starts, what makes the input invalid, the primary button, the
// footer note, the alert and duration choices, the time-zone link, the
// iMIP subjects and texts the daemon sends (it is language-neutral), the
// close question and the question of a recurring event's scope.

import Foundation

extension Cal {
    /// editor.go `Origin`: where a new event was asked for.
    public enum Origin: Sendable, Equatable {
        /// "+", ⌥⌘N, the File menu: the next half hour from now.
        case now
        /// A double click on empty time of the grid: that half hour.
        case slot
        /// A double click on a month cell: 9:00 that day.
        case monthCell
        /// A double click on the all-day row: an all-day event that day.
        case allDay
    }

    /// The start of an event made from a month cell.
    public static let monthCellHour = 9

    /// editor.go `Defaults`.
    public struct Defaults: Sendable, Equatable {
        public var allDay = false
        /// In the editor's zone; an all-day event's are the midnights of
        /// its first day and of the day after its last.
        public var start: Date
        public var end: Date
        /// The default alert, or none (empty).
        public var alerts: [Int] = []
    }

    /// editor.go `DefaultDuration`: 60 minutes when out of 5–1440.
    public static func defaultDuration(_ p: CalendarPreferences) -> TimeInterval {
        var m = p.defaultDurationMinutes
        if m < 5 || m > 1440 {
            m = 60
        }
        return TimeInterval(m * 60)
    }

    /// editor.go `NewEventDefaults`.
    public static func newEventDefaults(at: Date, origin: Origin, preferences p: CalendarPreferences, zone: TimeZone) -> Defaults {
        let t = parts(at, zone)
        var d = Defaults(start: at, end: at)
        if p.defaultAlertMinutes >= 0 && p.defaultAlertMinutes <= API.Limits.maxAlertMinutes {
            d.alerts = [p.defaultAlertMinutes]
        }
        switch origin {
        case .allDay:
            if !d.alerts.isEmpty {
                d.alerts = [allDayAlertDefault]
            }
            d.allDay = true
            d.start = startOfDay(at, zone)
            d.end = addDays(d.start, 1, zone)
            return d
        case .monthCell:
            d.start = date(t.year, t.month, t.day, monthCellHour, 0, in: zone)
        case .slot:
            d.start = date(t.year, t.month, t.day, t.hour, t.minute / 30 * 30, in: zone)
        case .now:
            var half = date(t.year, t.month, t.day, t.hour, t.minute / 30 * 30, in: zone)
            if half < at {
                half = date(t.year, t.month, t.day, t.hour, t.minute / 30 * 30 + 30, in: zone)
            }
            d.start = half
        }
        d.end = d.start.addingTimeInterval(defaultDuration(p))
        return d
    }

    /// editor.go `Input`: what the editor holds. `zone` is the editor's
    /// zone (Go's times carry it).
    public struct Input: Sendable, Equatable {
        public var title = ""
        public var allDay = false
        public var start: Date
        public var end: Date
        public var zone: TimeZone
        public var location = ""
        public var notes = ""
        /// The recipient field's resolved addresses.
        public var attendees: [EventInputAttendee] = []
        /// nil is sent as none (`[]`).
        public var alerts: [Int]?
        /// The chosen calendar (nil none).
        public var calendarId: CalendarID?

        public init(
            title: String = "", allDay: Bool = false, start: Date, end: Date, zone: TimeZone, location: String = "",
            notes: String = "", attendees: [EventInputAttendee] = [], alerts: [Int]? = nil, calendarId: CalendarID? = nil
        ) {
            self.title = title
            self.allDay = allDay
            self.start = start
            self.end = end
            self.zone = zone
            self.location = location
            self.notes = notes
            self.attendees = attendees
            self.alerts = alerts
            self.calendarId = calendarId
        }
    }

    /// editor.go `Problem`, in the order `validate` reports them.
    public enum Problem: Int, Sendable, CaseIterable {
        case noCalendar, endBeforeStart, titleTooLong, locationTooLong, notesTooLong, tooManyAttendees
        case invalidAttendee, tooManyAlerts
    }

    /// editor.go `Validate`.
    public static func validate(_ input: Input) -> [Problem] {
        var out: [Problem] = []
        if (input.calendarId?.rawValue ?? "").isEmpty {
            out.append(.noCalendar)
        }
        let z = input.zone
        if input.allDay && !(startOfDay(input.end, z) > startOfDay(input.start, z)) || !input.allDay && !(input.end > input.start) {
            out.append(.endBeforeStart)
        }
        if trimSpace(input.title).utf8.count > API.Limits.maxEventTitleBytes {
            out.append(.titleTooLong)
        }
        if trimSpace(input.location).utf8.count > API.Limits.maxEventLocationBytes {
            out.append(.locationTooLong)
        }
        if input.notes.utf8.count > API.Limits.maxEventNotesBytes {
            out.append(.notesTooLong)
        }
        if input.attendees.count > API.Limits.maxEventAttendeesIn {
            out.append(.tooManyAttendees)
        }
        if input.attendees.contains(where: { !isBareAddress($0.address) }) {
            out.append(.invalidAttendee)
        }
        if (input.alerts ?? []).count > API.Limits.maxEventAlerts {
            out.append(.tooManyAlerts)
        }
        return out
    }

    /// Go's `mail.ParseAddress(s)` succeeding with `Address == s`: a bare
    /// addr-spec, a dot-atom local part (a quoted one comes back unquoted,
    /// so never equal), "@", a dot-atom domain or a domain literal, and
    /// nothing around it.
    static func isBareAddress(_ s: String) -> Bool {
        let u = Array(s.unicodeScalars)
        func atext(_ c: Unicode.Scalar) -> Bool {
            if c.value >= 0x80 {
                return true
            }
            switch c {
            case "(", ")", "<", ">", "[", "]", ":", ";", "@", "\\", ",", "\"", ".": return false
            default: return c.value >= 0x21 && c.value <= 0x7E
            }
        }
        func dotAtom(_ a: ArraySlice<Unicode.Scalar>) -> Bool {
            guard let f = a.first, let l = a.last, f != ".", l != "." else { return false }
            var prevDot = false
            for c in a {
                if c == "." {
                    if prevDot { return false }
                    prevDot = true
                } else {
                    guard atext(c) else { return false }
                    prevDot = false
                }
            }
            return true
        }
        guard let at = u.firstIndex(of: "@"), dotAtom(u[..<at]) else { return false }
        let domain = u[(at + 1)...]
        if domain.first == "[" {
            guard domain.count >= 2, domain.last == "]" else { return false }
            return domain.dropFirst().dropLast().allSatisfy { c in
                (c.value >= 33 && c.value <= 90) || (c.value >= 94 && c.value <= 126)
            }
        }
        return dotAtom(domain)
    }

    /// editor.go `ProblemText`.
    public static func problemText(_ p: Problem) -> String {
        switch p {
        case .noCalendar: return L10n.T("Choose a calendar for the event.")
        case .endBeforeStart: return L10n.T("The event must end after it starts.")
        case .titleTooLong: return L10n.T("The title is too long.")
        case .locationTooLong: return L10n.T("The location is too long.")
        case .notesTooLong: return L10n.T("The notes are too long.")
        case .tooManyAttendees:
            return L10n.N(
                "An event can have at most %d attendee.", "An event can have at most %d attendees.", API.Limits.maxEventAttendeesIn)
        case .invalidAttendee: return L10n.T("An attendee’s address is not valid.")
        case .tooManyAlerts:
            return L10n.N("An event can have at most %d alert.", "An event can have at most %d alerts.", API.Limits.maxEventAlerts)
        }
    }

    /// editor.go `EventInput`: timed in `zone` (its IANA name), all-day as
    /// dates with the exclusive end.
    public static func eventInput(_ input: Input, zone: TimeZone) -> EventInput {
        let title = trimSpace(input.title)
        let location = trimSpace(input.location)
        var out = EventInput(
            title: title, allDay: input.allDay, location: location.isEmpty ? nil : location,
            notes: input.notes.isEmpty ? nil : input.notes, alerts: input.alerts ?? [],
            attendees: input.attendees.isEmpty ? nil : input.attendees)
        if input.allDay {
            let first = startOfDay(input.start, zone)
            var end = startOfDay(input.end, zone)
            if !(end > first) {
                end = addDays(first, 1, zone)
            }
            out.startDate = dateOf(input.start, zone)
            out.endDate = dateOf(end, zone)
            return out
        }
        func local(_ t: Date) -> LocalDateTime {
            let p = parts(t, zone)
            return LocalDateTime(String(format: "%04d-%02d-%02dT%02d:%02d:%02d", p.year, p.month, p.day, p.hour, p.minute, p.second))
        }
        out.start = local(input.start)
        out.end = local(input.end)
        out.timeZone = TimeZoneID(zone.identifier)
        return out
    }

    /// editor.go `SendsInvitations`: attendees, and someone delivers them
    /// (a Graph account always does).
    public static func sendsInvitations(attendees: Int, source: CalendarSource, features f: CalendarFeatures) -> Bool {
        attendees > 0 && (source == .graph || f.invitations != .none)
    }

    /// editor.go `PrimaryLabel`.
    public static func primaryLabel(attendees: Int, source: CalendarSource, features f: CalendarFeatures) -> String {
        sendsInvitations(attendees: attendees, source: source, features: f)
            ? L10n.T("Save and Send Invitations") : L10n.T("Save")
    }

    public static var cancel: String { L10n.T("Cancel") }

    /// editor.go `Footer`: who sends the invitations (only with
    /// attendees), then the attachments.
    public static func footer(
        attendees: Int, features f: CalendarFeatures, mailAccount: String, mailAddress: String, attachments: [String]
    ) -> [String] {
        var lines: [String] = []
        if attendees > 0 {
            switch f.invitations {
            case .mail:
                lines.append(L10n.T(
                    "Invitations leave by e-mail from account %s (%s) through the Outbox.", cleanLine(mailAccount),
                    cleanLine(mailAddress)))
            case .server:
                lines.append(L10n.T("The server sends the invitations."))
            default:
                lines.append(L10n.T("No account can send invitations from this calendar."))
            }
        }
        for a in attachments {
            lines.append(L10n.T("Attachment: %s", cleanLine(a)))
        }
        return lines
    }

    /// The Alert popup's values in minutes before the start; -1 is None.
    public static let alertChoices = [-1, 0, 5, 10, 15, 30, 60, 1440]

    /// editor.go `AlertTitle` (any value, not only the choices).
    public static func alertTitle(_ minutes: Int) -> String {
        if minutes < 0 {
            return L10n.C("alert", "None")
        }
        if minutes == 0 {
            return L10n.T("At time of event")
        }
        if minutes % 1440 == 0 {
            return L10n.N("%d day before", "%d days before", minutes / 1440)
        }
        if minutes % 60 == 0 {
            return L10n.N("%d hour before", "%d hours before", minutes / 60)
        }
        return L10n.N("%d minute before", "%d minutes before", minutes)
    }

    /// editor.go `AllDayAlertChoices`: an all-day event's Alert popup:
    /// None, then days before at 9:00, as minutes before its midnight (1
    /// day before at 9:00 is 15 hours before; nothing negative is stored).
    public static let allDayAlertChoices = [-1, 900, 2340, 9540]

    /// An all-day event's alert when it has one: 1 day before at 9:00.
    public static let allDayAlertDefault = 900

    /// The hour of the all-day alerts, in minutes after midnight.
    private static let allDayAlertHour = 9 * 60

    /// editor.go `AlertChoicesFor`.
    public static func alertChoices(allDay: Bool) -> [Int] {
        allDay ? allDayAlertChoices : alertChoices
    }

    /// editor.go `AlertTitleFor`: an all-day value of whole days before
    /// 9:00 reads "1 day before (9:00)", "1 week before (9:00)"; any other
    /// value, and every timed one, is `alertTitle(_:)`'s.
    public static func alertTitle(_ minutes: Int, allDay: Bool) -> String {
        guard allDay, minutes > 0, (minutes + allDayAlertHour) % 1440 == 0 else { return alertTitle(minutes) }
        let d = (minutes + allDayAlertHour) / 1440
        if d == 7 {
            return L10n.T("1 week before (9:00)")
        }
        return L10n.N("%d day before (9:00)", "%d days before (9:00)", d)
    }

    /// editor.go `AlertOnAllDayToggle`: the alert after the All Day switch
    /// changed (`on`: now all-day). On keeps None and an all-day choice,
    /// else the all-day default; off keeps None, else the preference's
    /// default alert.
    public static func alertOnAllDayToggle(_ current: Int, on: Bool, preferences p: CalendarPreferences) -> Int {
        if current < 0 {
            return -1
        }
        if on {
            return allDayAlertChoices.contains(current) ? current : allDayAlertDefault
        }
        if p.defaultAlertMinutes < 0 || p.defaultAlertMinutes > API.Limits.maxAlertMinutes {
            return -1
        }
        return p.defaultAlertMinutes
    }

    /// The Default Duration popup's values in minutes.
    public static let durationChoices = [15, 30, 45, 60, 90, 120]

    public static func durationTitle(_ minutes: Int) -> String { durationText(TimeInterval(minutes * 60)) }

    /// "Time zone: Europe/Prague".
    public static func timeZoneLink(_ zone: String) -> String { L10n.T("Time zone: %s", zone) }

    public static var zoneSearch: String { L10n.T("Search Time Zones") }
    public static var titlePlaceholder: String { L10n.T("Title") }

    /// editor.go `Field`, in the form's order.
    public enum Field: Int, Sendable, CaseIterable {
        case calendar, allDay, start, end, `repeat`, location, attendees, availability, alert, notes
    }

    public static let fields: [Field] = Field.allCases

    public static func fieldLabel(_ f: Field) -> String {
        switch f {
        case .calendar: L10n.T("Calendar:")
        case .start: L10n.T("Starts:")
        case .end: L10n.T("Ends:")
        case .repeat: L10n.T("Repeat:")
        case .location: L10n.T("Location:")
        case .attendees: L10n.T("Attendees:")
        case .availability: L10n.T("Availability:")
        case .alert: L10n.T("Alert:")
        case .notes: L10n.T("Notes:")
        case .allDay: L10n.T("All Day:")
        }
    }

    public static var addVideoCall: String { L10n.T("Add Video Call") }
    public static var pasteLink: String { L10n.T("Paste Link…") }

    /// editor.go `VideoProviderName`: brand names are not translated.
    public static func videoProviderName(_ p: String) -> String {
        switch p {
        case "teamsForBusiness", "teamsForConsumer": "Microsoft Teams"
        case "skypeForBusiness": "Skype for Business"
        case "skypeForConsumer": "Skype"
        default: L10n.T("Online Meeting")
        }
    }

    public static var attach: String { L10n.T("Attach") }

    /// editor.go `EditorDelete`: the footer's Delete button, offered only
    /// while editing an event the daemon lets the user delete, titled by
    /// `removeTitle`; nil when there is none.
    public static func editorDelete(editing: Bool, _ e: Event) -> String? {
        guard editing, e.canDelete else { return nil }
        return removeTitle(e)
    }

    /// editor.go `CloseQuestion` (Save / Discard / Cancel).
    public static var closeQuestion: Question {
        Question(
            heading: L10n.T("Save the Changes to This Event?"),
            body: L10n.T("If you don’t save, your changes are lost."),
            confirm: L10n.T("Save"),
            alternative: L10n.T("Discard"))
    }

    /// editor.go `MailKind`: an iMIP message the daemon sends.
    public enum MailKind: Int, Sendable, CaseIterable {
        case invitation, update, cancel, accepted, tentative, declined
    }

    /// editor.go `MailKindOf`.
    public static func mailKindOf(_ r: EventResponse) -> MailKind {
        switch r {
        case .tentative: .tentative
        case .declined: .declined
        default: .accepted
        }
    }

    /// editor.go `MailTexts`: the subject and text of an iMIP message about
    /// `o`; `me` is the user's name or address.
    public static func mailTexts(_ kind: MailKind, _ o: Occurrence, me: String, env: Env) -> MailText {
        let title = cleanTitle(o.title)
        let me = cleanLine(me)
        let subject: String
        let sentence: String
        switch kind {
        case .update:
            subject = L10n.T("Updated invitation: %s", title)
            sentence = L10n.T("The event “%s” has changed.", title)
        case .cancel:
            subject = L10n.T("Cancelled: %s", title)
            sentence = L10n.T("The event “%s” has been cancelled.", title)
        case .accepted:
            subject = L10n.T("Accepted: %s", title)
            sentence = L10n.T("%s has accepted the invitation to “%s”.", me, title)
        case .tentative:
            subject = L10n.T("Tentative: %s", title)
            sentence = L10n.T("%s has tentatively accepted the invitation to “%s”.", me, title)
        case .declined:
            subject = L10n.T("Declined: %s", title)
            sentence = L10n.T("%s has declined the invitation to “%s”.", me, title)
        case .invitation:
            subject = L10n.T("Invitation: %s", title)
            sentence = L10n.T("You are invited to “%s”.", title)
        }
        var lines = [sentence, ""]
        let when = timeLine(o, env: env)
        if !when.isEmpty {
            lines.append(L10n.T("When: %s", when))
        }
        let place = cleanLine(o.location)
        if !place.isEmpty {
            lines.append(L10n.T("Where: %s", place))
        }
        var text = lines.joined(separator: "\n")
        while text.hasSuffix("\n") {
            text.removeLast()
        }
        return MailText(subject: capBytes(subject, API.Limits.maxMailTextBytes), text: capBytes(text, API.Limits.maxMailTextBytes))
    }

    /// Cuts `s` to `n` UTF-8 bytes on a character boundary.
    static func capBytes(_ s: String, _ n: Int) -> String {
        let b = Array(s.utf8)
        guard b.count > n else { return s }
        var n = n
        while n > 0 && b[n] & 0xC0 == 0x80 {
            n -= 1
        }
        return String(decoding: b[..<n], as: UTF8.self)
    }

    /// editor.go `ScopeAction`.
    public enum ScopeAction: Sendable, Equatable {
        case edit, delete, respond
    }

    /// editor.go `ScopeChoice`.
    public struct ScopeChoice: Sendable, Equatable {
        public var scope: EditScope
        public var title: String
    }

    /// editor.go `ScopeQuestion`: Cancel is always there.
    public struct ScopeQuestion: Sendable, Equatable {
        public var heading = ""
        public var body = ""
        public var choices: [ScopeChoice] = []
    }

    /// editor.go `ScopeTitle`.
    public static func scopeTitle(_ s: EditScope) -> String {
        switch s {
        case .thisAndFuture: L10n.T("This and Future Events")
        case .all: L10n.T("All Events")
        default: L10n.T("This Event Only")
        }
    }

    /// editor.go `ScopeQuestionFor`: an answer offers this and all only; a
    /// complex rule is edited and deleted only as a whole series.
    public static func scopeQuestionFor(_ a: ScopeAction, complex: Bool) -> ScopeQuestion {
        var scopes: [EditScope] = [.this, .thisAndFuture, .all]
        var q = ScopeQuestion()
        switch a {
        case .delete:
            q.heading = L10n.T("Delete a Repeating Event?")
            q.body = L10n.T("Do you want to delete only this occurrence, or also the ones after it?")
        case .respond:
            q.heading = L10n.T("Answer for the Whole Series?")
            q.body = L10n.T("Do you want to answer for this occurrence only, or for every occurrence?")
            scopes = [.this, .all]
        case .edit:
            q.heading = L10n.T("Change a Repeating Event?")
            q.body = L10n.T("Do you want to change only this occurrence, or also the ones after it?")
        }
        if complex && a != .respond {
            scopes = [.all]
            q.body = L10n.T("This event repeats in a way that can be changed only for the whole series.")
        }
        q.choices = scopes.map { ScopeChoice(scope: $0, title: scopeTitle($0)) }
        return q
    }
}
