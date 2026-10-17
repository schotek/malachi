// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The invented sample data of MALACHI_CALENDAR_SAMPLES (Go samples.go,
// docs/calendar.md §5.10), for layout checks without a daemon: three
// accounts (Work on Microsoft 365, Gmail, Nextcloud over CalDAV) with their
// calendars, the events of now's week, three unanswered invitations and
// one invitation card per state. Every name and address is invented. The
// data only; `CalendarSamples.swift` wraps it into a source.

import Foundation

extension Cal {
    /// The sample account ids.
    public static let sampleWork: AccountID = "acc_sample_work"
    public static let sampleGmail: AccountID = "acc_sample_gmail"
    public static let sampleNextcloud: AccountID = "acc_sample_nextcloud"

    /// The sample calendar ids.
    public static let sampleWorkCalendar: CalendarID = "cal_sample_work"
    public static let sampleBirthdays: CalendarID = "cal_sample_birthdays"
    public static let sampleHolidays: CalendarID = "cal_sample_holidays"
    public static let samplePersonal: CalendarID = "cal_sample_personal"
    public static let sampleFamily: CalendarID = "cal_sample_family"
    public static let sampleSport: CalendarID = "cal_sample_sport"

    /// The states of the sample cards, in their order.
    public static let invitationStates: [InvitationState] = [
        .unanswered, .answered, .outdated, .cancelled, .processed, .invalid,
    ]

    /// samples.go `Samples`: the sample data of one moment.
    public struct SampleData: Sendable, Equatable {
        /// The three accounts as account.list lists them.
        public var accounts: [Account] = []
        /// calendar.list's answer.
        public var calendars = CalendarListResult(unanswered: 0)
        /// The week's events (each instance of the standup on its own).
        public var events: [Event] = []
        /// Their occurrences with the unanswered invitation not in a
        /// calendar.
        public var occurrences: [Occurrence] = []
        /// The three unanswered invitations (invitation.list).
        public var invitations: [InvitationListItem] = []
        /// One invitation card per state, in `invitationStates`' order.
        public var cards: [InvitationCard] = []
        public var preferences = Cal.defaultPreferences

        /// The name of a sample account ("" for another id).
        public func sampleAccountName(_ id: AccountID) -> String {
            accounts.first { $0.id == id }?.config.name ?? ""
        }

        /// The name of a sample calendar ("" for another id).
        public func calendarName(_ id: CalendarID) -> String {
            for a in calendars.accounts {
                if let c = a.calendars.first(where: { $0.id == id }) {
                    return c.name
                }
            }
            return ""
        }
    }

    /// samples.go `SampleData`: the samples of the week of `now` (from
    /// Monday) in `zone`.
    public static func sampleData(now: Date, zone: TimeZone) -> SampleData {
        let monday = startOfWeek(now, .monday, zone)
        func at(_ day: Int, _ h: Int, _ m: Int) -> Date {
            let d = parts(addDays(monday, day, zone), zone)
            return date(d.year, d.month, d.day, h, m, in: zone)
        }
        func day(_ n: Int) -> CalendarDay { dateOf(addDays(monday, n, zone), zone) }
        let synced = now.addingTimeInterval(-120)

        let jana = Address(name: "Jana Nováková", address: "jana@example.org")
        let petr = Address(name: "Petr Svoboda", address: "petr@example.org")
        let club = Address(name: "Climbing Club", address: "club@example.com")
        let me = "me@example.org"
        let mailAndCalendar = API.mailCapabilities + [.calendar]
        // Invented titles used more than once (data, not texts of the UI).
        let demo = "Vendor demo"
        let standup = "Standup"

        var s = SampleData()
        s.accounts = [
            Account(
                id: sampleWork,
                config: AccountConfig(name: "Work", email: me, kind: .graph, calendar: CalendarConfig(sync: true)),
                enabled: true, state: SyncState(accountId: sampleWork, status: .idle), capabilities: mailAndCalendar),
            Account(
                id: sampleGmail,
                config: AccountConfig(
                    name: "Gmail", email: "me@example.com", kind: .imap, oauth2: OAuth2Config(provider: .google)),
                enabled: true, state: SyncState(accountId: sampleGmail, status: .idle), capabilities: mailAndCalendar),
            Account(
                id: sampleNextcloud,
                config: AccountConfig(
                    name: "Nextcloud", email: "me@example.com", kind: .caldav,
                    caldav: CalDAVConfig(serverUrl: "https://cloud.example.com/remote.php/dav", username: "me"),
                    calendar: CalendarConfig(sync: true)),
                enabled: true, state: SyncState(accountId: sampleNextcloud, status: .idle), capabilities: [.calendar]),
        ]
        func state(_ id: AccountID) -> CalendarSyncState {
            CalendarSyncState(accountId: id, status: .idle, progress: -1, lastSync: synced)
        }
        let window = CalendarWindow(
            from: dateOf(addDays(monday, -365, zone), zone), to: dateOf(addDays(monday, API.Limits.calendarFutureDays, zone), zone))
        s.calendars = CalendarListResult(
            accounts: [
                CalendarAccount(
                    accountId: sampleWork, source: .graph, sync: true, state: state(sampleWork),
                    features: CalendarFeatures(freeBusy: true, proposeNewTime: true, invitations: .server, onlineMeetings: true),
                    window: window,
                    calendars: [
                        CalendarRecord(
                            id: sampleWorkCalendar, accountId: sampleWork, remoteId: "AAMkSampleCalendar", name: "Calendar", color: "#1f6feb", visible: true,
                            access: .owner, kind: .events, isDefault: true, timeZone: "Europe/Prague",
                            onlineMeetingProviders: ["teamsForBusiness"]),
                        CalendarRecord(
                            id: sampleBirthdays, accountId: sampleWork, remoteId: "AAMkSampleBirthdays", name: "Birthdays", color: "#8250df", visible: true,
                            access: .read, kind: .birthdays),
                        CalendarRecord(
                            id: sampleHolidays, accountId: sampleWork, remoteId: "AAMkSampleHolidays", name: "Czech holidays", color: "#1a7f37", visible: true,
                            access: .read, kind: .holidays),
                    ]),
                CalendarAccount(
                    accountId: sampleGmail, source: .google, sync: true, state: state(sampleGmail),
                    features: CalendarFeatures(freeBusy: false, proposeNewTime: false, invitations: .server, onlineMeetings: false),
                    window: window,
                    calendars: [
                        CalendarRecord(
                            id: samplePersonal, accountId: sampleGmail, remoteId: "me@example.com", name: "Personal", color: "#e5534b", visible: true,
                            access: .owner, kind: .events, isDefault: true),
                        CalendarRecord(
                            id: sampleFamily, accountId: sampleGmail, remoteId: "family@group.example.com", name: "Family", color: "#d4a72c", visible: true,
                            access: .write, kind: .events, owner: petr),
                    ]),
                CalendarAccount(
                    accountId: sampleNextcloud, source: .caldav, sync: true, state: state(sampleNextcloud),
                    features: CalendarFeatures(
                        freeBusy: false, proposeNewTime: false, invitations: .mail, mailAccountId: sampleGmail, onlineMeetings: false),
                    window: window,
                    calendars: [
                        CalendarRecord(
                            id: sampleSport, accountId: sampleNextcloud, remoteId: "/remote.php/dav/calendars/me/sport/", name: "Sport", color: "#0082c9", visible: true,
                            access: .owner, kind: .events, isDefault: true),
                    ]),
            ],
            unanswered: 3)

        func timed(
            _ id: String, _ cal: CalendarID, _ acc: AccountID, _ title: String, _ d: Int, _ h: Int, _ m: Int, _ eh: Int, _ em: Int
        ) -> Event {
            Event(
                occurrence: Occurrence(
                    eventId: EventID(id), calendarId: cal, accountId: acc, title: title, start: at(d, h, m), end: at(d, eh, em),
                    allDay: false, timeZone: TimeZoneID(zone.identifier), status: .confirmed, showAs: .busy),
                etag: "\"1\"", attendeesTruncated: false, responseRequested: false, canEdit: true, canDelete: true,
                canRespond: false, canProposeNewTime: false, removeIsDelete: true)
        }
        func allDay(_ id: String, _ cal: CalendarID, _ acc: AccountID, _ title: String, _ d: Int, _ days: Int) -> Event {
            Event(
                occurrence: Occurrence(
                    eventId: EventID(id), calendarId: cal, accountId: acc, title: title, startDate: day(d), endDate: day(d + days),
                    allDay: true, status: .confirmed, showAs: .free),
                etag: "\"1\"", attendeesTruncated: false, responseRequested: false, canEdit: false, canDelete: false,
                canRespond: false, canProposeNewTime: false, removeIsDelete: false)
        }
        func attendees(_ mine: EventResponse) -> [Attendee] {
            [
                Attendee(name: jana.name, address: jana.address, role: .chair, response: .accepted, type: .person, organizer: true),
                Attendee(name: "Me", address: me, role: .required, response: mine, type: .person, me: true),
                Attendee(name: petr.name, address: petr.address, role: .required, response: .tentative, type: .person),
                Attendee(address: "eva@example.org", role: .optional, response: .needsAction, type: .person),
            ]
        }
        func invited(_ e0: Event, _ mine: EventResponse) -> Event {
            var e = e0
            e.myResponse = mine
            e.organizer = false
            e.attendeeCount = 4
            e.attendees = attendees(mine)
            e.organizerAddress = jana
            e.canEdit = false
            e.canRespond = true
            e.removeIsDelete = false
            e.responseRequested = true
            return e
        }

        let standupRule = Recurrence(frequency: .weekly, interval: 1, byDay: ["MO", "TU", "WE", "TH", "FR"])
        for d in 0..<5 {
            var e = timed("ev_sample_standup", sampleWorkCalendar, sampleWork, standup, d, 9, 0, 9, 15)
            e.recurrenceId = .time(at(d, 9, 0))
            e.recurring = true
            e.recurrence = standupRule
            e.location = "Room B"
            e.alert = true
            e.alerts = [5]
            s.events.append(e)
        }

        var planning = invited(
            timed("ev_sample_planning", sampleWorkCalendar, sampleWork, "Q4 planning", 1, 10, 0, 11, 30), .accepted)
        planning.videoCall = true
        planning.location = "Room A"
        planning.videoCallLink = VideoCall(
            url: "https://teams.microsoft.com/l/meetup-join/sample", provider: .teams, host: "teams.microsoft.com")
        planning.notes = "Agenda: budget, hiring, the roadmap.\nSlides: https://example.org/q4-slides"
        planning.links = ["https://example.org/q4-slides"]
        planning.canProposeNewTime = true
        planning.invitationMessages = [MessageRef(accountId: sampleWork, messageId: "msg_sample_planning")]

        let supplier = timed("ev_sample_supplier", samplePersonal, sampleGmail, "Call with the supplier", 1, 10, 30, 11, 0)
        var review = invited(timed("ev_sample_review", sampleWorkCalendar, sampleWork, "Design review", 1, 11, 0, 12, 0), .tentative)
        review.showAs = .tentative

        let budget = invited(
            timed("ev_sample_budget", sampleWorkCalendar, sampleWork, "Budget review", 2, 14, 0, 15, 0), .needsAction)
        let allHands = invited(timed("ev_sample_allhands", sampleWorkCalendar, sampleWork, "All-hands", 2, 16, 0, 17, 0), .declined)
        var offsite = allDay("ev_sample_offsite", sampleWorkCalendar, sampleWork, "Team offsite", 3, 2)
        offsite.canEdit = true
        offsite.canDelete = true
        offsite.removeIsDelete = true
        offsite.location = "Hotel Horizont"
        var climbing = timed("ev_sample_climbing", sampleSport, sampleNextcloud, "Climbing", 3, 18, 0, 19, 30)
        climbing.location = "Boulder hall"
        var birthday = allDay("ev_sample_birthday", sampleBirthdays, sampleWork, "Petr Svoboda’s birthday", 5, 1)
        birthday.readOnly = true
        let brunch = timed("ev_sample_brunch", sampleFamily, sampleGmail, "Family brunch", 6, 10, 0, 12, 0)
        s.events += [planning, supplier, review, budget, allHands, offsite, climbing, birthday, brunch]

        s.occurrences = s.events.map(\.occurrence)
        let demoStart = at(4, 15, 0)
        let demoEnd = at(4, 16, 0)
        // The vendor demo is an invitation on the Gmail account not in any
        // calendar: drawn tentatively.
        s.occurrences.append(Occurrence(
            accountId: sampleGmail, title: demo, start: demoStart, end: demoEnd, allDay: false, status: .confirmed,
            showAs: .tentative, myResponse: .needsAction, attendeeCount: 3,
            invitation: MessageRef(accountId: sampleGmail, messageId: "msg_sample_demo")))

        let clubMeeting = "Club meeting"
        let clubStart = at(9, 19, 0)
        let clubEnd = at(9, 21, 0)
        s.invitations = [
            InvitationListItem(
                accountId: sampleWork, messageId: "msg_sample_budget",
                summary: InvitationSummary(
                    method: .request, kind: .invitation, state: .unanswered, title: budget.title, start: budget.start,
                    end: budget.end, organizer: jana)),
            InvitationListItem(
                accountId: sampleGmail, messageId: "msg_sample_demo",
                summary: InvitationSummary(
                    method: .request, kind: .invitation, state: .unanswered, title: demo, start: demoStart,
                    end: demoEnd, organizer: petr)),
            InvitationListItem(
                accountId: sampleGmail, messageId: "msg_sample_club",
                summary: InvitationSummary(
                    method: .request, kind: .update, state: .unanswered, title: clubMeeting, start: clubStart,
                    end: clubEnd, organizer: club)),
        ]

        func card(_ st: InvitationState) -> InvitationCard {
            var c = InvitationCard(
                summary: InvitationSummary(
                    method: .request, kind: .invitation, state: st, title: demo, start: demoStart, end: demoEnd,
                    organizer: petr),
                partId: "2", uid: "sample-demo@example.org", sequence: 0, timeZone: TimeZoneID(zone.identifier),
                location: "Room C", organizerMismatch: false, attendeeCount: 3,
                attendees: [
                    Attendee(name: petr.name, address: petr.address, role: .chair, response: .accepted, type: .person, organizer: true),
                    Attendee(address: "me@example.com", role: .required, response: .needsAction, type: .person, me: true),
                    Attendee(name: jana.name, address: jana.address, role: .optional, response: .needsAction, type: .person),
                ],
                me: InvitationMe(address: "me@example.com", response: .needsAction),
                conflicts: [
                    InvitationConflict(
                        title: standup, start: at(4, 15, 30), end: at(4, 16, 0), calendarId: sampleWorkCalendar,
                        eventId: "ev_sample_conflict"),
                ],
                day: InvitationDay(
                    from: at(4, 8, 0), to: at(4, 17, 0),
                    busy: [
                        DayBusy(start: at(4, 9, 0), end: at(4, 9, 15), calendarId: sampleWorkCalendar),
                        DayBusy(start: at(4, 15, 30), end: at(4, 16, 0), calendarId: sampleWorkCalendar),
                    ]),
                targets: [InvitationTarget(calendarId: samplePersonal, isDefault: true), InvitationTarget(calendarId: sampleFamily)],
                fixedTarget: false, canRespond: true, canProposeNewTime: false, replyVia: .mail, replyAccountId: sampleGmail,
                replyTo: petr, replyApplied: false)
            switch st {
            case .answered:
                c.response = .accepted
                c.calendarId = samplePersonal
                c.eventId = "ev_sample_demo"
                c.respondedAt = now.addingTimeInterval(-3600)
                c.me?.response = .accepted
            case .outdated:
                c.kind = .update
                c.sequence = 1
                c.canRespond = false
                c.previous = InvitationPrevious(start: .time(at(4, 14, 0)), end: .time(at(4, 15, 0)))
            case .cancelled:
                c.method = .cancel
                c.kind = .cancelled
                c.canRespond = false
                c.targets = []
                c.eventId = "ev_sample_demo"
                c.calendarId = samplePersonal
            case .processed:
                c.method = .reply
                c.kind = .reply
                c.canRespond = false
                c.targets = []
                c.organizer = Address(name: "Me", address: "me@example.com")
                c.replyFrom = jana
                c.response = .accepted
                c.replyApplied = true
                c.conflicts = []
            case .invalid:
                c = InvitationCard(
                    summary: InvitationSummary(method: .other, kind: .other, state: .invalid, title: ""),
                    partId: "2", uid: "", sequence: 0, organizerMismatch: false, attendeeCount: 0, conflicts: [],
                    day: InvitationDay(from: Date(timeIntervalSince1970: -62_135_596_800), to: Date(timeIntervalSince1970: -62_135_596_800)),
                    targets: [], fixedTarget: false, canRespond: false, canProposeNewTime: false, replyVia: InvitationsVia(""),
                    replyApplied: false)
            default:
                break
            }
            return c
        }
        s.cards = invitationStates.map(card)
        return s
    }
}
