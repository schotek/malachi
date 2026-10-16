// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Invitations in Mail (Go invitation.go): the list pill of a message with a
// calendar part, and the invitation card above the message header, built
// from the parsed part (never from HTML): the date tile, the time and
// people lines, the conflict line, "Add to:", the reply note, the state
// strip after answering, a reply's one-line card and the lines of invalid,
// outdated and cancelled invitations.

import Foundation

extension Cal {
    /// invitation.go `InvitationPill`: the list pill of a message.
    public enum InvitationPill: Int, Sendable, CaseIterable {
        case none, invitation, update, cancelled, reply
    }

    /// invitation.go `PillKind`: none without an invitation, for an
    /// unreadable one and for kind other.
    public static func pillKind(_ s: InvitationSummary?) -> InvitationPill {
        guard let s, s.state != .invalid else { return .none }
        switch s.kind {
        case .invitation: return .invitation
        case .update: return .update
        case .cancelled: return .cancelled
        case .reply: return .reply
        default: return .none
        }
    }

    /// invitation.go `PillTitle` ("" for none).
    public static func pillTitle(_ p: InvitationPill) -> String {
        switch p {
        case .invitation: L10n.C("pill", "Invitation")
        case .update: L10n.C("pill", "Update")
        case .cancelled: L10n.C("pill", "Cancelled")
        case .reply: L10n.C("pill", "Reply")
        case .none: ""
        }
    }

    /// A row whose subject is struck through: a cancellation.
    public static func strikesSubject(_ p: InvitationPill) -> Bool { p == .cancelled }

    /// invitation.go `CardMode`: which card a message shows.
    public enum CardMode: Int, Sendable, CaseIterable {
        /// No card (no invitation, invalid, the account's switch off, other).
        case none
        /// The invitation card with its actions or state strip.
        case full
        /// The one-line card of an attendee's answer.
        case reply
        /// The card of a cancellation with Remove from Calendar.
        case cancelled
    }

    /// invitation.go `CardModeOf`; `cardOn` is the account's switch.
    public static func cardModeOf(_ c: InvitationCard?, cardOn: Bool) -> CardMode {
        guard let c, cardOn, c.state != .invalid else { return .none }
        switch c.kind {
        case .reply: return .reply
        case .cancelled: return .cancelled
        case .invitation, .update: return .full
        default: return .none
        }
    }

    /// The Accept / Tentative / Decline row.
    public static func showsResponses(_ c: InvitationCard) -> Bool {
        c.state == .unanswered && c.canRespond
    }

    /// The strip after answering.
    public static func showsStateStrip(_ c: InvitationCard) -> Bool {
        c.state == .answered && !(c.response?.rawValue ?? "").isEmpty
    }

    /// Propose New Time….
    public static func showsProposeNewTime(_ c: InvitationCard) -> Bool {
        c.canProposeNewTime && (showsResponses(c) || showsStateStrip(c))
    }

    /// invitation.go `DateTile`.
    public struct DateTile: Sendable, Equatable {
        public var weekday: String
        public var day: String
        public var month: String

        public init(weekday: String, day: String, month: String) {
            self.weekday = weekday
            self.day = day
            self.month = month
        }
    }

    static func cardStart(_ s: InvitationSummary, _ zone: TimeZone) -> Date? {
        s.allDay ? parseDate(s.startDate, zone) : s.start
    }

    /// invitation.go `DateTileOf`: nil without a start.
    public static func dateTileOf(_ s: InvitationSummary, env: Env) -> DateTile? {
        guard let t = cardStart(s, env.zone) else { return nil }
        return DateTile(weekday: Text.shortWeekday(t, env), day: Text.dayOfMonth(t, env), month: Text.shortMonth(t, env))
    }

    /// An occurrence of only these times (for `timeLine`).
    static func bareOccurrence(
        title: String = "", start: Date? = nil, end: Date? = nil, startDate: CalendarDay? = nil, endDate: CalendarDay? = nil,
        allDay: Bool = false, location: String? = nil
    ) -> Occurrence {
        Occurrence(
            accountId: AccountID(""), title: title, start: start, end: end, startDate: startDate, endDate: endDate,
            allDay: allDay, location: location, status: .confirmed, showAs: .busy)
    }

    static func occurrenceOf(_ c: InvitationCard) -> Occurrence {
        bareOccurrence(
            title: c.title, start: c.start, end: c.end, startDate: c.startDate, endDate: c.endDate, allDay: c.allDay,
            location: c.location)
    }

    /// The card's clock line.
    public static func cardTimeLine(_ c: InvitationCard, env: Env) -> String { timeLine(occurrenceOf(c), env: env) }

    /// The card's bold title.
    public static func cardTitle(_ s: InvitationSummary) -> String { cleanTitle(s.title) }

    /// invitation.go `CardPeopleLine`.
    public static func cardPeopleLine(_ c: InvitationCard) -> String {
        let n = max(c.attendeeCount, c.attendees?.count ?? 0)
        let org = nameOf(c.organizer)
        if !org.isEmpty && n > 0 {
            return L10n.N("Organiser %s · %d attendee", "Organiser %s · %d attendees", n, org, n)
        }
        if !org.isEmpty {
            return L10n.T("Organiser %s", org)
        }
        if n > 0 {
            return L10n.N("%d attendee", "%d attendees", n)
        }
        return ""
    }

    /// invitation.go `PreviousLines`: an update's former values, each only
    /// when the daemon sent it: the time ("Previously Tuesday 14 October
    /// 2026 · 10:00–11:00 · 1 h"), the location ("Previously at Room B"),
    /// the recurrence ("Previously repeated: Every week on Tuesday");
    /// empty without one.
    public static func previousLines(_ c: InvitationCard, env: Env) -> [String] {
        guard let prev = c.previous else { return [] }
        var lines: [String] = []
        var start = cardStart(c.summary, env.zone)
        if let ps = prev.start {
            var o = bareOccurrence()
            if case .time(let s) = ps {
                o.start = s
                if case .time(let e)? = prev.end {
                    o.end = e
                }
            } else {
                o.allDay = true
                o.startDate = CalendarDay(ps.wire)
                o.endDate = CalendarDay(prev.end?.wire ?? "")
            }
            let line = timeLine(o, env: env)
            if !line.isEmpty {
                lines.append(L10n.T("Previously %s", line))
                start = span(o, env.zone)?.start
            }
        }
        let place = cleanLine(prev.location)
        if !place.isEmpty {
            lines.append(L10n.T("Previously at %s", place))
        }
        if let r = prev.recurrence, let start {
            let rule = recurrenceText(r, start: start, env: env)
            if !rule.isEmpty {
                lines.append(L10n.T("Previously repeated: %s", rule))
            }
        }
        return lines
    }

    /// `previousLines` joined line by line, for a caller that shows one
    /// label (InvitationController); prefer `previousLines`.
    public static func previousLine(_ c: InvitationCard, env: Env) -> String {
        previousLines(c, env: env).joined(separator: "\n")
    }

    /// invitation.go `ConflictLine`; `calendarName` names a calendar by id.
    public static func conflictLine(
        _ conflicts: [InvitationConflict], calendarName: (CalendarID) -> String, env: Env
    ) -> String {
        switch conflicts.count {
        case 0:
            return ""
        case 1:
            let c = conflicts[0]
            let when = Text.timeRange(c.start, c.end, env)
            let cal = cleanLine(calendarName(c.calendarId))
            let title = cleanLine(c.title)
            if !title.isEmpty {
                return L10n.T("Conflicts with %s (%s) in calendar %s", title, when, cal)
            }
            return L10n.T("Conflicts with a private event (%s) in calendar %s", when, cal)
        default:
            return L10n.N("Conflicts with %d event", "Conflicts with %d events", conflicts.count)
        }
    }

    public static var noConflict: String { L10n.T("No conflicts in your calendars") }

    /// The label of the target-calendar popup.
    public static var addTo: String { L10n.T("Add to:") }

    /// invitation.go `ReplyNote`; `accountName` names the reply account.
    public static func replyNote(_ c: InvitationCard, accountName: String) -> [String] {
        var lines: [String] = []
        var to = nameOf(c.replyTo)
        if !to.isEmpty, let r = c.replyTo, !cleanLine(r.name).isEmpty {
            to = L10n.T("%s <%s>", cleanLine(r.name), cleanLine(r.address))
        }
        switch c.replyVia {
        case .mail:
            let a = cleanLine(accountName)
            if !a.isEmpty {
                lines.append(L10n.T("The reply leaves from account %s.", a))
            }
            if !to.isEmpty {
                lines.append(L10n.T("The reply goes to %s.", to))
            }
        case .server:
            if !to.isEmpty {
                lines.append(L10n.T("The reply goes to %s.", to))
            }
        default:
            lines.append(L10n.T("No reply will be sent."))
        }
        if c.organizerMismatch {
            lines.append(L10n.T("The organiser’s address is not from the sender’s domain. Check the invitation before you answer."))
        }
        return lines
    }

    /// invitation.go `StateStrip`: "Accepted · in calendar Work".
    public static func stateStrip(_ c: InvitationCard, calendarName: String) -> String {
        let answer = responseLabel(c.response)
        let cal = cleanLine(calendarName)
        if !cal.isEmpty && c.response != .declined {
            return L10n.T("%s · in calendar %s", answer, cal)
        }
        return answer
    }

    public static var changeResponse: String { L10n.T("Change Response") }
    public static var showInCalendar: String { L10n.T("Show in Calendar") }
    public static var proposeNewTime: String { L10n.T("Propose New Time…") }
    public static var propose: String { L10n.T("Propose") }

    /// invitation.go `ReplyCard`: "Jana Nováková accepted".
    public static func replyCard(_ c: InvitationCard) -> String {
        var who = nameOf(c.replyFrom)
        if who.isEmpty {
            who = nameOf(c.organizer)
        }
        let line: String
        switch c.response {
        case .accepted?: line = L10n.T("%s accepted", who)
        case .tentative?: line = L10n.T("%s tentatively accepted", who)
        case .declined?: line = L10n.T("%s declined", who)
        default: line = L10n.T("%s replied", who)
        }
        return c.replyApplied ? L10n.T("%s (applied to your event)", line) : line
    }

    /// invitation.go `ShowsInCalendar`: Show in Calendar on the state strip
    /// or line: the invitation is linked to a stored event, and it is the
    /// user's own event or one the user answered.
    public static func showsInCalendar(_ c: InvitationCard) -> Bool {
        !(c.eventId?.rawValue ?? "").isEmpty && (c.ownEvent || c.state == .answered)
    }

    /// invitation.go `StateLine`: "You organise this event." for the user's
    /// own request or update, else the line of one that cannot be answered.
    public static func stateLine(_ c: InvitationCard) -> String {
        if c.ownEvent && (c.kind == .invitation || c.kind == .update) {
            return L10n.T("You organise this event.")
        }
        switch c.state {
        case .invalid: return invalidLine
        case .outdated: return L10n.T("This invitation is out of date: a newer one arrived, or the event is over.")
        case .cancelled: return cancelledLine
        case .unanswered where !c.canRespond: return L10n.T("This invitation cannot be answered from here.")
        default: return ""
        }
    }

    public static var invalidLine: String { L10n.T("The invitation in this message could not be read.") }
    public static var cancelledLine: String { L10n.T("This event was cancelled.") }
}
