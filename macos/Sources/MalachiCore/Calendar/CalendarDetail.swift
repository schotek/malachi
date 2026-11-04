// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The event detail popover (Go detail.go): its rows from an Event, the
// time line with the duration, the people and their responses, and which
// actions it offers (respond, remove or delete with its question, Show
// Invitation in Mail, Edit).

import Foundation

extension Cal {
    /// How many attendees the list shows before "Show All".
    public static let maxAttendeesShown = 6

    /// detail.go `AttendeeRow`.
    public struct AttendeeRow: Sendable, Equatable {
        public var name: String
        public var address: String
        public var response: EventResponse
        public var responseText: String
        public var organizer: Bool
        public var me: Bool
        public var optional: Bool
    }

    /// detail.go `Detail`: everything the popover shows of an event.
    public struct Detail: Sendable, Equatable {
        /// "Work · Calendar".
        public var calendarLine = ""
        public var title = ""
        /// "Tuesday 14 October 2026 · 10:00–11:30 · 1 h 30 min".
        public var timeLine = ""
        /// The rule's text; "" for a single event.
        public var recurrence = ""
        public var location = ""
        /// The call's link ("" none) and its host.
        public var videoCall = ""
        public var videoHost = ""
        /// "Organiser Jana Nováková · 4 attendees"; "" without attendees.
        public var peopleLine = ""
        public var attendees: [AttendeeRow] = []
        /// More than `maxAttendeesShown` attendees.
        public var collapsed = false
        public var myResponse: EventResponse?
        public var notes = ""
        public var links: [String] = []
        public var canRespond = false
        public var canEdit = false
        public var canRemove = false
        public var showInvitation = false
        public var removeTitle = ""
    }

    /// detail.go `DetailOf`.
    public static func detailOf(_ e: Event, accountName: String, calendarName: String, env: Env) -> Detail {
        var d = Detail()
        d.calendarLine = Text.dots(cleanLine(accountName), cleanLine(calendarName))
        d.title = cleanTitle(e.title)
        d.timeLine = timeLine(e.occurrence, env: env)
        d.location = cleanLine(e.location)
        d.peopleLine = peopleLine(e)
        d.myResponse = e.myResponse
        d.notes = Board.cleanBlock(e.notes ?? "", max: API.Limits.maxEventNotesBytes)
        d.links = e.links ?? []
        d.canRespond = canRespond(e)
        d.canEdit = e.canEdit
        d.canRemove = e.canDelete
        d.removeTitle = removeTitle(e)
        d.showInvitation = showsInvitation(e)
        if let r = e.recurrence, let (s, _) = span(e.occurrence, env.zone) {
            d.recurrence = recurrenceText(r, start: s, env: env)
        }
        if let v = e.videoCallLink {
            d.videoCall = v.url
            d.videoHost = cleanLine(v.host)
        }
        d.attendees = (e.attendees ?? []).map { a in
            AttendeeRow(
                name: cleanLine(a.name), address: cleanLine(a.address), response: a.response,
                responseText: responseLabel(a.response), organizer: a.organizer, me: a.me, optional: a.role == .optional)
        }
        d.collapsed = d.attendees.count > maxAttendeesShown
        return d
    }

    /// detail.go `TimeLine`.
    public static func timeLine(_ o: Occurrence, env: Env) -> String {
        guard let (s, e) = span(o, env.zone) else { return "" }
        return spanLine(s, e, allDay: o.allDay, env: env)
    }

    static func spanLine(_ s: Date, _ e: Date, allDay: Bool, env: Env) -> String {
        let zone = env.zone
        if allDay {
            let days = max(1, daysBetween(s, e, zone))
            if days == 1 {
                return Text.dot(Text.longDate(s, env), Text.allDay)
            }
            return Text.dot(Text.range(Text.longDate(s, env), Text.longDate(addDays(e, -1, zone), env)), daysText(days))
        }
        if occurrenceDays(s, e, zone) <= 1 {
            return Text.dots(Text.longDate(s, env), Text.timeRange(s, e, env), durationText(e.timeIntervalSince(s)))
        }
        return Text.dot(Text.range(Text.dateTime(s, env), Text.dateTime(e, env)), durationText(e.timeIntervalSince(s)))
    }

    /// detail.go `DaysText`: "2 days".
    public static func daysText(_ n: Int) -> String {
        L10n.N("%d day", "%d days", n)
    }

    /// detail.go `DurationText`: "1 h 30 min", "45 min", "2 h"; whole days
    /// of 24 hours "2 days"; rounded down to the minute.
    public static func durationText(_ d: TimeInterval) -> String {
        let total = max(0, Int(d / 60))
        if total >= 1440 && total % 1440 == 0 {
            return daysText(total / 1440)
        }
        let h = total / 60
        let m = total % 60
        let hours = L10n.N("%d h", "%d h", h)
        let minutes = L10n.N("%d min", "%d min", m)
        if h == 0 {
            return minutes
        }
        if m == 0 {
            return hours
        }
        return L10n.T("%s %s", hours, minutes)
    }

    /// detail.go `ResponseLabel`.
    public static func responseLabel(_ r: EventResponse?) -> String {
        switch r {
        case .accepted?: L10n.T("Accepted")
        case .tentative?: L10n.T("Tentative")
        case .declined?: L10n.T("Declined")
        case .delegated?: L10n.T("Delegated")
        default: L10n.T("No Reply")
        }
    }

    /// detail.go `MyResponseText`: "You: Accepted".
    public static func myResponseText(_ r: EventResponse?) -> String {
        L10n.T("You: %s", responseLabel(r))
    }

    /// The answers the popup and the card offer.
    public static let responseChoices: [EventResponse] = [.accepted, .tentative, .declined]

    /// detail.go `ResponseAction`: Accept, Tentative, Decline.
    public static func responseAction(_ r: EventResponse) -> String {
        switch r {
        case .tentative: L10n.C("respond", "Tentative")
        case .declined: L10n.T("Decline")
        default: L10n.T("Accept")
        }
    }

    public static var declineWithoutReply: String { L10n.T("Decline Without Reply") }

    /// detail.go `PeopleLine`.
    public static func peopleLine(_ e: Event) -> String {
        let n = max(e.attendeeCount, e.attendees?.count ?? 0)
        if n == 0 {
            return ""
        }
        if e.organizer {
            return L10n.N("You organise · %d attendee", "You organise · %d attendees", n)
        }
        let org = nameOf(e.organizerAddress)
        if !org.isEmpty {
            return L10n.N("Organiser %s · %d attendee", "Organiser %s · %d attendees", n, org, n)
        }
        return L10n.N("%d attendee", "%d attendees", n)
    }

    public static var organizerLabel: String { L10n.T("organiser") }
    public static var optionalLabel: String { L10n.T("optional") }

    public static func showAllAttendees(_ n: Int) -> String {
        L10n.N("Show All %d Attendee", "Show All %d Attendees", n)
    }

    /// detail.go `CanRespond`: the daemon allows it, the user is an
    /// attendee and not the organiser, and the event is not cancelled.
    public static func canRespond(_ e: Event) -> Bool {
        e.canRespond && !e.organizer && !(e.myResponse?.rawValue ?? "").isEmpty && e.status != .cancelled
    }

    /// detail.go `ShowsInvitation`.
    public static func showsInvitation(_ e: Event) -> Bool { !(e.invitationMessages ?? []).isEmpty }

    public static var showInvitation: String { L10n.T("Show Invitation in Mail") }
    public static var edit: String { L10n.T("Edit") }
    public static var joinVideoCall: String { L10n.T("Join Video Call") }
    public static var askAboutEvent: String { L10n.T("Ask about This Event") }
    public static var findAnotherTime: String { L10n.T("Find Another Time") }

    /// detail.go `RemoveTitle`: Delete for an event the user organises,
    /// else Remove from Calendar.
    public static func removeTitle(_ e: Event) -> String {
        e.removeIsDelete ? L10n.T("Delete") : removeFromCalendar
    }

    public static var removeFromCalendar: String { L10n.T("Remove from Calendar") }

    /// detail.go `Question`: Cancel is always there.
    public struct Question: Sendable, Equatable {
        public var heading = ""
        public var body = ""
        public var confirm = ""
        /// A second way to go ahead ("" none).
        public var alternative = ""

        public init(heading: String = "", body: String = "", confirm: String = "", alternative: String = "") {
            self.heading = heading
            self.body = body
            self.confirm = confirm
            self.alternative = alternative
        }
    }

    /// detail.go `RemoveQuestion`: nil when only the Mail preference
    /// confirm-delete decides whether to ask (`deleteQuestion`).
    public static func removeQuestion(_ e: Event) -> Question? {
        let title = cleanTitle(e.title)
        if !e.removeIsDelete && canRespond(e) && e.myResponse != .declined {
            return Question(
                heading: L10n.T("Also tell the organiser you won’t attend?"),
                body: L10n.T("“%s” will be removed from your calendar.", title),
                confirm: L10n.T("Remove and Decline"),
                alternative: L10n.T("Remove Only"))
        }
        let n = max(e.attendeeCount, e.attendees?.count ?? 0)
        if e.removeIsDelete && n > 0 {
            return Question(
                heading: L10n.T("Delete the Event?"),
                body: L10n.N("“%s” will be cancelled for its %d attendee.", "“%s” will be cancelled for its %d attendees.", n, title, n),
                confirm: L10n.T("Delete"))
        }
        return nil
    }

    /// detail.go `DeleteQuestion`.
    public static func deleteQuestion(_ e: Event) -> Question {
        Question(heading: L10n.T("Remove “%s”?", cleanTitle(e.title)), confirm: removeTitle(e))
    }
}
