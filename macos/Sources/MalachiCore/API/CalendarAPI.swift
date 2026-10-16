// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Calendars, events and invitations (docs/api.md §4.14–§4.16, §5;
// backend/pkg/api/calendar.go; design in docs/calendar.md). Every string of
// these types that comes from a server, an .ics or another person (titles,
// locations, notes, names, calendar names) is untrusted display text: show
// it with `stringValue` / plain text only. No HTML of an event crosses the
// API.
//
// Go's `omitempty` booleans and counters read as false / 0 when absent
// (`DefaultFalse`, `DefaultZero`); an omitted string, time or object is an
// Optional; a slice Go never writes as `null` is read leniently
// (`NullAsEmpty`).

import Foundation

// MARK: Property wrappers

/// A Go `omitempty` bool: absent reads as false.
@propertyWrapper
public struct DefaultFalse: Codable, Sendable, Hashable {
    public var wrappedValue: Bool

    public init() { wrappedValue = false }
    public init(wrappedValue: Bool) { self.wrappedValue = wrappedValue }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.singleValueContainer()
        wrappedValue = c.decodeNil() ? false : try c.decode(Bool.self)
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.singleValueContainer()
        try c.encode(wrappedValue)
    }
}

/// A Go `omitempty` int: absent reads as 0.
@propertyWrapper
public struct DefaultZero: Codable, Sendable, Hashable {
    public var wrappedValue: Int

    public init() { wrappedValue = 0 }
    public init(wrappedValue: Int) { self.wrappedValue = wrappedValue }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.singleValueContainer()
        wrappedValue = c.decodeNil() ? 0 : try c.decode(Int.self)
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.singleValueContainer()
        try c.encode(wrappedValue)
    }
}

extension KeyedEncodingContainer {
    /// Go's `omitempty`: the default value is left out.
    public mutating func encode(_ value: DefaultFalse, forKey key: Key) throws {
        if value.wrappedValue { try encode(true, forKey: key) }
    }

    public mutating func encode(_ value: DefaultZero, forKey key: Key) throws {
        if value.wrappedValue != 0 { try encode(value.wrappedValue, forKey: key) }
    }
}

extension KeyedDecodingContainer {
    public func decode(_ type: DefaultFalse.Type, forKey key: Key) throws -> DefaultFalse {
        try decodeIfPresent(type, forKey: key) ?? DefaultFalse()
    }

    public func decode(_ type: DefaultZero.Type, forKey key: Key) throws -> DefaultZero {
        try decodeIfPresent(type, forKey: key) ?? DefaultZero()
    }
}

// MARK: Scalars and identifiers

/// api.CalendarID: a stored calendar (`cal_…`).
public struct CalendarID: OpaqueID {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }
}

/// api.EventID: a stored event (`ev_…`); a series' instances share the id
/// of their master and differ by recurrence id.
public struct EventID: OpaqueID {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }
}

/// api.AlertID: one alert of one occurrence (`al_…`).
public struct AlertID: OpaqueID {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }
}

/// api.Date: a calendar date without a zone, `2026-10-14`. Kept as the wire
/// string (named apart from Foundation's `Date`); `parts` reads it.
public struct CalendarDay: StringWireValue {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// Year, month and day when the string is `YYYY-MM-DD` with a real
    /// calendar date; nil otherwise.
    public var parts: (year: Int, month: Int, day: Int)? {
        let b = Array(rawValue.utf8)
        guard b.count == 10, b[4] == 0x2D, b[7] == 0x2D,
              let y = CalendarDay.number(b[0..<4]), let m = CalendarDay.number(b[5..<7]),
              let d = CalendarDay.number(b[8..<10]), (1...12).contains(m), d >= 1 else { return nil }
        let leap = (y % 4 == 0 && y % 100 != 0) || y % 400 == 0
        let days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31]
        guard d <= days[m - 1] else { return nil }
        return (y, m, d)
    }

    static func number(_ bytes: ArraySlice<UInt8>) -> Int? {
        var n = 0
        for byte in bytes {
            guard byte >= 0x30, byte <= 0x39 else { return nil }
            n = n * 10 + Int(byte - 0x30)
        }
        return n
    }
}

/// api.LocalDateTime: a wall-clock time, `2026-10-14T10:00:00`, always sent
/// with a separate `TimeZoneID`.
public struct LocalDateTime: StringWireValue {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }
}

/// api.TimeZone: an IANA zone name (`Europe/Prague`); empty means floating.
/// Named apart from Foundation's `TimeZone`.
public struct TimeZoneID: StringWireValue {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }
}

/// api.DateOrTime: either an RFC 3339 UTC instant (a timed event) or a date
/// (an all-day event): a recurrence id, or the previous time of an update.
/// Decodes either form; a string that is neither fails.
public enum DateOrTime: Codable, Sendable, Hashable, CustomStringConvertible {
    case time(Date)
    case date(CalendarDay)

    public init(from decoder: any Decoder) throws {
        let c = try decoder.singleValueContainer()
        let s = try c.decode(String.self)
        if let t = RFC3339.parse(s) {
            self = .time(t)
        } else if CalendarDay(s).parts != nil {
            self = .date(CalendarDay(s))
        } else {
            throw DecodingError.dataCorruptedError(in: c, debugDescription: "neither an RFC 3339 instant nor a date")
        }
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.singleValueContainer()
        try c.encode(wire)
    }

    /// The wire string.
    public var wire: String {
        switch self {
        case .time(let t): RFC3339.format(t)
        case .date(let d): d.rawValue
        }
    }

    public var description: String { wire }
}

// MARK: Enumerations

/// api.CalendarSource: where an account's calendars come from (open enum;
/// an unknown value is treated as absent).
public struct CalendarSource: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let graph: CalendarSource = "graph"
    public static let caldav: CalendarSource = "caldav"
    /// Reserved, not produced yet.
    public static let google: CalendarSource = "google"
}

/// api.CalendarAccess: what the user may do with a calendar.
public struct CalendarAccess: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let owner: CalendarAccess = "owner"
    public static let write: CalendarAccess = "write"
    public static let read: CalendarAccess = "read"
}

/// api.CalendarKind: what a calendar holds (open enum).
public struct CalendarKind: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let events: CalendarKind = "events"
    public static let birthdays: CalendarKind = "birthdays"
    public static let holidays: CalendarKind = "holidays"
    public static let other: CalendarKind = "other"
}

/// api.EventResponse: an attendee's answer (iCalendar PARTSTAT).
public struct EventResponse: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let needsAction: EventResponse = "needsAction"
    public static let accepted: EventResponse = "accepted"
    public static let tentative: EventResponse = "tentative"
    public static let declined: EventResponse = "declined"
    public static let delegated: EventResponse = "delegated"
}

/// api.ShowAs: how an event counts for free/busy; an unknown value is
/// treated as `.unknown`.
public struct ShowAs: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let free: ShowAs = "free"
    public static let tentative: ShowAs = "tentative"
    public static let busy: ShowAs = "busy"
    public static let oof: ShowAs = "oof"
    public static let workingElsewhere: ShowAs = "workingElsewhere"
    public static let unknown: ShowAs = "unknown"
}

/// api.EventStatus: an event's iCalendar STATUS.
public struct EventStatus: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let confirmed: EventStatus = "confirmed"
    public static let tentative: EventStatus = "tentative"
    public static let cancelled: EventStatus = "cancelled"
}

/// api.EditScope: what a change of a recurring event applies to.
public struct EditScope: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// The instance: an override / a Graph exception.
    public static let this: EditScope = "this"
    /// The series is split at the instance.
    public static let thisAndFuture: EditScope = "thisAndFuture"
    /// The master.
    public static let all: EditScope = "all"
}

/// api.InvitationMethod: the iTIP METHOD of an invitation's calendar part.
public struct InvitationMethod: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let request: InvitationMethod = "request"
    public static let reply: InvitationMethod = "reply"
    public static let cancel: InvitationMethod = "cancel"
    public static let counter: InvitationMethod = "counter"
    public static let declineCounter: InvitationMethod = "declineCounter"
    public static let refresh: InvitationMethod = "refresh"
    public static let add: InvitationMethod = "add"
    public static let publish: InvitationMethod = "publish"
    public static let other: InvitationMethod = "other"
}

/// api.InvitationKind: the list pill of a message with an invitation.
public struct InvitationKind: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let invitation: InvitationKind = "invitation"
    public static let update: InvitationKind = "update"
    public static let cancelled: InvitationKind = "cancelled"
    public static let reply: InvitationKind = "reply"
    /// Everything else: no pill.
    public static let other: InvitationKind = "other"
}

/// api.InvitationState: what is left to do about an invitation.
public struct InvitationState: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let unanswered: InvitationState = "unanswered"
    public static let answered: InvitationState = "answered"
    public static let outdated: InvitationState = "outdated"
    public static let cancelled: InvitationState = "cancelled"
    public static let processed: InvitationState = "processed"
    /// The part could not be read: no card.
    public static let invalid: InvitationState = "invalid"
}

/// api.CalendarSyncStatus: the status of an account's calendar sync.
public struct CalendarSyncStatus: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let idle: CalendarSyncStatus = "idle"
    public static let syncing: CalendarSyncStatus = "syncing"
    public static let offline: CalendarSyncStatus = "offline"
    public static let authRequired: CalendarSyncStatus = "authRequired"
    /// Graph only: the sign-in lacks calendar access.
    public static let consentRequired: CalendarSyncStatus = "consentRequired"
    public static let error: CalendarSyncStatus = "error"
    public static let disabled: CalendarSyncStatus = "disabled"
}

/// api.InvitationsVia: who delivers the invitations, updates and replies of
/// an event.
public struct InvitationsVia: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let server: InvitationsVia = "server"
    /// The daemon, by iMIP mail through the outbox.
    public static let mail: InvitationsVia = "mail"
    public static let none: InvitationsVia = "none"
}

/// api.VideoProvider: the service of a video-call link (open enum).
public struct VideoProvider: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let teams: VideoProvider = "teams"
    public static let zoom: VideoProvider = "zoom"
    public static let meet: VideoProvider = "meet"
    public static let webex: VideoProvider = "webex"
    public static let jitsi: VideoProvider = "jitsi"
    public static let whereby: VideoProvider = "whereby"
    public static let other: VideoProvider = "other"
}

/// api.CalDAVScheduling: who sends the invitations and replies of a caldav
/// account's events.
public struct CalDAVScheduling: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// Follows the server's calendar-auto-schedule (the default).
    public static let auto: CalDAVScheduling = "auto"
    public static let server: CalDAVScheduling = "server"
    public static let client: CalDAVScheduling = "client"
}

/// api.AfterAnswer: what happens to an invitation message once answered.
public struct AfterAnswer: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let keep: AfterAnswer = "keep"
    public static let archive: AfterAnswer = "archive"
}

/// api.WeekStart: the first day of the week of the calendar views.
public struct WeekStart: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let locale: WeekStart = "locale"
    public static let monday: WeekStart = "monday"
    public static let sunday: WeekStart = "sunday"
    public static let saturday: WeekStart = "saturday"
}

/// api.RecurrenceFrequency: the FREQ of a `Recurrence`.
public struct RecurrenceFrequency: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let daily: RecurrenceFrequency = "daily"
    public static let weekly: RecurrenceFrequency = "weekly"
    public static let monthly: RecurrenceFrequency = "monthly"
    public static let yearly: RecurrenceFrequency = "yearly"
}

/// api.AttendeeRole: an attendee's iCalendar ROLE.
public struct AttendeeRole: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let required: AttendeeRole = "required"
    public static let optional: AttendeeRole = "optional"
    public static let chair: AttendeeRole = "chair"
    public static let nonParticipant: AttendeeRole = "nonParticipant"
}

/// api.AttendeeType: an attendee's iCalendar CUTYPE; an unknown value is
/// treated as `.unknown`.
public struct AttendeeType: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let person: AttendeeType = "person"
    public static let room: AttendeeType = "room"
    public static let resource: AttendeeType = "resource"
    public static let group: AttendeeType = "group"
    public static let unknown: AttendeeType = "unknown"
}

/// api.EventAlertState: the state `notify.eventAlert` announces.
public struct EventAlertState: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// Fired, or a snooze ended.
    public static let due: EventAlertState = "due"
    /// A client dismissed it: withdraw the notification.
    public static let dismissed: EventAlertState = "dismissed"
}

/// api.VideoCallMode: what `EventInput.videoCall` asks for.
public struct VideoCallMode: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let none: VideoCallMode = "none"
    /// The source creates a meeting (`CalendarFeatures.onlineMeetings`).
    public static let create: VideoCallMode = "create"
    /// A link the user typed.
    public static let link: VideoCallMode = "link"
}

/// api.CalDAVFoundVia: how `account.discoverCalDAV` found the server.
public struct CalDAVFoundVia: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let wellKnown: CalDAVFoundVia = "wellKnown"
    public static let srv: CalDAVFoundVia = "srv"
    public static let url: CalDAVFoundVia = "url"
}

// MARK: Accounts (docs/calendar.md §4.3)

/// api.CalDAVConfig: the connection of a caldav account. The password is
/// `Credentials.password`, kept in the keyring.
public struct CalDAVConfig: Codable, Sendable, Equatable {
    /// Normalised (`account.discoverCalDAV`).
    public var serverUrl: String
    /// From `account.listCalDAVCalendars`.
    public var principalUrl: String?
    public var homeSetUrl: String?
    public var username: String
    /// Pins the server's leaf certificate; not allowed with an http URL.
    public var certificateSha256: String?
    /// The mail account invitations and replies leave from when the daemon
    /// schedules; empty: events with attendees answer
    /// `invitationsUnavailable`.
    public var mailAccountId: AccountID?
    /// nil = auto.
    public var scheduling: CalDAVScheduling?

    public init(
        serverUrl: String,
        principalUrl: String? = nil,
        homeSetUrl: String? = nil,
        username: String,
        certificateSha256: String? = nil,
        mailAccountId: AccountID? = nil,
        scheduling: CalDAVScheduling? = nil
    ) {
        self.serverUrl = serverUrl
        self.principalUrl = principalUrl
        self.homeSetUrl = homeSetUrl
        self.username = username
        self.certificateSha256 = certificateSha256
        self.mailAccountId = mailAccountId
        self.scheduling = scheduling
    }
}

/// api.CalendarConfig: the calendar part of a graph or caldav account.
public struct CalendarConfig: Codable, Sendable, Equatable {
    /// Synchronise the calendars. Graph default false; caldav always true.
    public var sync: Bool
    /// Remote ids (graph) or hrefs (caldav) not synchronised.
    public var disabledCalendars: [String]?
    /// The remote id or href new events go to; nil = the server's default.
    public var defaultCalendar: String?
    /// How far back events are kept: 30, 90, 180, 365 or 730; nil/0 = 365.
    public var keepDays: Int?
    /// Draw unanswered invitations dashed; nil = true.
    public var showUnanswered: Bool?
    /// Show the invitation card in Mail; nil = true.
    public var invitationCard: Bool?
    /// nil = keep.
    public var afterAnswer: AfterAnswer?

    /// api.CalendarConfig.ShowsUnanswered.
    public var showsUnanswered: Bool { showUnanswered ?? true }
    /// api.CalendarConfig.ShowsInvitationCard.
    public var showsInvitationCard: Bool { invitationCard ?? true }

    public init(
        sync: Bool,
        disabledCalendars: [String]? = nil,
        defaultCalendar: String? = nil,
        keepDays: Int? = nil,
        showUnanswered: Bool? = nil,
        invitationCard: Bool? = nil,
        afterAnswer: AfterAnswer? = nil
    ) {
        self.sync = sync
        self.disabledCalendars = disabledCalendars
        self.defaultCalendar = defaultCalendar
        self.keepDays = keepDays
        self.showUnanswered = showUnanswered
        self.invitationCard = invitationCard
        self.afterAnswer = afterAnswer
    }
}

/// api.CalDAVCalendar: a VEVENT collection of a CalDAV server.
public struct CalDAVCalendar: Codable, Sendable, Equatable {
    public var href: String
    public var name: String
    /// `#rrggbb`.
    public var color: String?
    public var access: CalendarAccess
    /// The server's schedule-default-calendar-URL.
    @DefaultFalse public var isDefault: Bool

    public init(href: String, name: String, color: String? = nil, access: CalendarAccess, isDefault: Bool = false) {
        self.href = href
        self.name = name
        self.color = color
        self.access = access
        self.isDefault = isDefault
    }
}

/// api.AccountDiscoverCalDAVParams: a host or a full URL; https assumed.
public struct AccountDiscoverCalDAVParams: Codable, Sendable, Equatable {
    public var url: String
    /// Pins the server's leaf certificate (SHA-256); nil = not pinned.
    public var certificateSha256: String?

    public init(url: String, certificateSha256: String? = nil) {
        self.url = url
        self.certificateSha256 = certificateSha256
    }
}

/// api.AccountDiscoverCalDAVResult. `serverUrl` may name another host than
/// the one typed (a .well-known redirect).
public struct AccountDiscoverCalDAVResult: Codable, Sendable, Equatable {
    public var serverUrl: String
    public var foundVia: CalDAVFoundVia
    public var needsCredentials: Bool
    public var certificate: CertificateInfo?

    public init(
        serverUrl: String,
        foundVia: CalDAVFoundVia,
        needsCredentials: Bool,
        certificate: CertificateInfo? = nil
    ) {
        self.serverUrl = serverUrl
        self.foundVia = foundVia
        self.needsCredentials = needsCredentials
        self.certificate = certificate
    }
}

/// api.AccountListCalDAVCalendarsParams: with `accountId` and no password
/// the stored one is used.
public struct AccountListCalDAVCalendarsParams: Codable, Sendable, Equatable {
    public var accountId: AccountID?
    public var config: AccountConfig
    public var credentials: Credentials

    public init(accountId: AccountID? = nil, config: AccountConfig, credentials: Credentials) {
        self.accountId = accountId
        self.config = config
        self.credentials = credentials
    }
}

public struct AccountListCalDAVCalendarsResult: Codable, Sendable, Equatable {
    public var principalUrl: String
    public var homeSetUrl: String
    public var displayName: String?
    /// The mailto: values of calendar-user-address-set.
    @NullAsEmpty public var addresses: [String]
    /// `.server` or `.client`.
    public var scheduling: CalDAVScheduling
    @NullAsEmpty public var calendars: [CalDAVCalendar]

    public init(
        principalUrl: String,
        homeSetUrl: String,
        displayName: String? = nil,
        addresses: [String] = [],
        scheduling: CalDAVScheduling,
        calendars: [CalDAVCalendar] = []
    ) {
        self.principalUrl = principalUrl
        self.homeSetUrl = homeSetUrl
        self.displayName = displayName
        self.addresses = addresses
        self.scheduling = scheduling
        self.calendars = calendars
    }
}

// MARK: Common types (docs/calendar.md §4.2)

/// api.Calendar: one stored calendar. (Named apart from Foundation's
/// `Calendar`.)
public struct CalendarRecord: Codable, Sendable, Equatable {
    public var id: CalendarID
    public var accountId: AccountID
    /// The server's id (graph) or href (caldav).
    public var remoteId: String
    public var name: String
    /// The user's colour if set, else the server's, else a palette colour
    /// by id; `#rrggbb`, lower case.
    public var color: String
    public var serverColor: String?
    public var userColor: String?
    /// Shown in the views (`calendar.update`).
    public var visible: Bool
    public var access: CalendarAccess
    public var kind: CalendarKind
    /// Set for a calendar shared with the user.
    public var owner: Address?
    /// The account's calendar for new events.
    @DefaultFalse public var isDefault: Bool
    public var timeZone: TimeZoneID?
    /// Graph allowedOnlineMeetingProviders, an open enum
    /// (`teamsForBusiness`).
    public var onlineMeetingProviders: [String]?
    /// Switched off in `CalendarConfig.disabledCalendars`: not synchronised.
    @DefaultFalse public var disabled: Bool

    public init(
        id: CalendarID,
        accountId: AccountID,
        remoteId: String = "",
        name: String,
        color: String,
        serverColor: String? = nil,
        userColor: String? = nil,
        visible: Bool,
        access: CalendarAccess,
        kind: CalendarKind,
        owner: Address? = nil,
        isDefault: Bool = false,
        timeZone: TimeZoneID? = nil,
        onlineMeetingProviders: [String]? = nil,
        disabled: Bool = false
    ) {
        self.id = id
        self.accountId = accountId
        self.remoteId = remoteId
        self.name = name
        self.color = color
        self.serverColor = serverColor
        self.userColor = userColor
        self.visible = visible
        self.access = access
        self.kind = kind
        self.owner = owner
        self.isDefault = isDefault
        self.timeZone = timeZone
        self.onlineMeetingProviders = onlineMeetingProviders
        self.disabled = disabled
    }
}

/// api.CalendarFeatures: what an account's source can do.
public struct CalendarFeatures: Codable, Sendable, Equatable {
    /// `calendar.freeBusy` can ask about other people.
    public var freeBusy: Bool
    /// Some events may offer proposing a new time.
    public var proposeNewTime: Bool
    public var invitations: InvitationsVia
    /// Invitations `.mail`: the account they leave from.
    public var mailAccountId: AccountID?
    /// "Add video call" can create a meeting.
    public var onlineMeetings: Bool

    public init(
        freeBusy: Bool,
        proposeNewTime: Bool,
        invitations: InvitationsVia,
        mailAccountId: AccountID? = nil,
        onlineMeetings: Bool
    ) {
        self.freeBusy = freeBusy
        self.proposeNewTime = proposeNewTime
        self.invitations = invitations
        self.mailAccountId = mailAccountId
        self.onlineMeetings = onlineMeetings
    }
}

/// api.CalendarSyncState: an account's calendar synchronisation state
/// (also the payload of `notify.calendarState`). A Graph account's is
/// separate from its mail `SyncState`.
public struct CalendarSyncState: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var status: CalendarSyncStatus
    /// 0–100, -1 unknown.
    public var progress: Int
    public var lastSync: Date?
    public var error: RPCError?

    public init(
        accountId: AccountID,
        status: CalendarSyncStatus,
        progress: Int,
        lastSync: Date? = nil,
        error: RPCError? = nil
    ) {
        self.accountId = accountId
        self.status = status
        self.progress = progress
        self.lastSync = lastSync
        self.error = error
    }
}

/// api.CalendarWindow: the span of an account's stored events.
public struct CalendarWindow: Codable, Sendable, Equatable {
    public var from: CalendarDay
    public var to: CalendarDay

    public init(from: CalendarDay, to: CalendarDay) {
        self.from = from
        self.to = to
    }
}

/// api.CalendarAccount: an account with the `calendar` capability.
public struct CalendarAccount: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var source: CalendarSource
    /// `CalendarConfig.sync` (always true for caldav).
    public var sync: Bool
    public var state: CalendarSyncState
    public var features: CalendarFeatures
    public var window: CalendarWindow
    /// In server order; empty while sync is off.
    @NullAsEmpty public var calendars: [CalendarRecord]

    public init(
        accountId: AccountID,
        source: CalendarSource,
        sync: Bool,
        state: CalendarSyncState,
        features: CalendarFeatures,
        window: CalendarWindow,
        calendars: [CalendarRecord] = []
    ) {
        self.accountId = accountId
        self.source = source
        self.sync = sync
        self.state = state
        self.features = features
        self.window = window
        self.calendars = calendars
    }
}

/// api.Recurrence: a recurrence rule in the shape the editor shows.
public struct Recurrence: Codable, Sendable, Equatable {
    public var frequency: RecurrenceFrequency
    /// 1...`API.Limits.maxRecurrenceInterval` in input; up to 1000 read.
    public var interval: Int
    /// Weekly: the days (`MO`); monthly/yearly with an ordinal (`2TU`,
    /// `-1FR`).
    public var byDay: [String]?
    /// -31...31, not 0.
    public var byMonthDay: [Int]?
    /// 1–12.
    public var byMonth: [Int]?
    /// At most one of `until` and `count`; neither = no end.
    public var until: CalendarDay?
    @DefaultZero public var count: Int
    /// `MO`…`SU`.
    public var weekStart: String?
    /// Read only: the rule has more than this shape; shown as "Custom",
    /// edited only with `EditScope.all` and kept unless replaced.
    @DefaultFalse public var complex: Bool
    /// Read only: modified or removed instances in the window.
    @DefaultZero public var exceptions: Int

    public init(
        frequency: RecurrenceFrequency,
        interval: Int,
        byDay: [String]? = nil,
        byMonthDay: [Int]? = nil,
        byMonth: [Int]? = nil,
        until: CalendarDay? = nil,
        count: Int = 0,
        weekStart: String? = nil,
        complex: Bool = false,
        exceptions: Int = 0
    ) {
        self.frequency = frequency
        self.interval = interval
        self.byDay = byDay
        self.byMonthDay = byMonthDay
        self.byMonth = byMonth
        self.until = until
        self.count = count
        self.weekStart = weekStart
        self.complex = complex
        self.exceptions = exceptions
    }
}

/// api.Attendee: one attendee of an event or an invitation.
public struct Attendee: Codable, Sendable, Equatable {
    public var name: String?
    public var address: String
    public var role: AttendeeRole
    public var response: EventResponse
    public var type: AttendeeType
    @DefaultFalse public var me: Bool
    @DefaultFalse public var organizer: Bool

    public init(
        name: String? = nil,
        address: String,
        role: AttendeeRole,
        response: EventResponse,
        type: AttendeeType,
        me: Bool = false,
        organizer: Bool = false
    ) {
        self.name = name
        self.address = address
        self.role = role
        self.response = response
        self.type = type
        self.me = me
        self.organizer = organizer
    }
}

/// api.VideoCall: a video-call link found in an event; opened only through
/// the client's "Open this link?".
public struct VideoCall: Codable, Sendable, Equatable {
    public var url: String
    public var provider: VideoProvider
    public var host: String

    public init(url: String, provider: VideoProvider, host: String) {
        self.url = url
        self.provider = provider
        self.host = host
    }
}

/// api.MessageRef: a stored message: an invitation of an occurrence, or the
/// invitation messages of an event.
public struct MessageRef: Codable, Sendable, Hashable {
    public var accountId: AccountID
    public var messageId: MessageID

    public init(accountId: AccountID, messageId: MessageID) {
        self.accountId = accountId
        self.messageId = messageId
    }
}

/// api.Occurrence: one instance in a view (`event.list`, `event.search`).
/// Timed: `start` and `end` are UTC instants; all-day: `startDate` and
/// `endDate` (exclusive) instead.
public struct Occurrence: Codable, Sendable, Equatable {
    /// Absent for an invitation not in any calendar.
    public var eventId: EventID?
    /// Instance of a series: its original start.
    public var recurrenceId: DateOrTime?
    /// The master of a series instance.
    public var seriesId: EventID?
    public var calendarId: CalendarID?
    public var accountId: AccountID
    /// Empty when private and not the user's.
    public var title: String
    public var start: Date?
    public var end: Date?
    public var startDate: CalendarDay?
    public var endDate: CalendarDay?
    public var allDay: Bool
    @DefaultFalse public var floating: Bool
    public var timeZone: TimeZoneID?
    public var location: String?
    public var status: EventStatus
    public var showAs: ShowAs
    /// Absent when the user is not an attendee.
    public var myResponse: EventResponse?
    /// The user organises it.
    @DefaultFalse public var organizer: Bool
    @DefaultZero public var attendeeCount: Int
    @DefaultFalse public var recurring: Bool
    @DefaultFalse public var videoCall: Bool
    @DefaultFalse public var `private`: Bool
    /// An alert is set.
    @DefaultFalse public var alert: Bool
    @DefaultFalse public var readOnly: Bool
    /// An unanswered invitation shown tentatively, not in a calendar.
    public var invitation: MessageRef?

    public init(
        eventId: EventID? = nil,
        recurrenceId: DateOrTime? = nil,
        seriesId: EventID? = nil,
        calendarId: CalendarID? = nil,
        accountId: AccountID,
        title: String,
        start: Date? = nil,
        end: Date? = nil,
        startDate: CalendarDay? = nil,
        endDate: CalendarDay? = nil,
        allDay: Bool,
        floating: Bool = false,
        timeZone: TimeZoneID? = nil,
        location: String? = nil,
        status: EventStatus,
        showAs: ShowAs,
        myResponse: EventResponse? = nil,
        organizer: Bool = false,
        attendeeCount: Int = 0,
        recurring: Bool = false,
        videoCall: Bool = false,
        `private`: Bool = false,
        alert: Bool = false,
        readOnly: Bool = false,
        invitation: MessageRef? = nil
    ) {
        self.eventId = eventId
        self.recurrenceId = recurrenceId
        self.seriesId = seriesId
        self.calendarId = calendarId
        self.accountId = accountId
        self.title = title
        self.start = start
        self.end = end
        self.startDate = startDate
        self.endDate = endDate
        self.allDay = allDay
        self.floating = floating
        self.timeZone = timeZone
        self.location = location
        self.status = status
        self.showAs = showAs
        self.myResponse = myResponse
        self.organizer = organizer
        self.attendeeCount = attendeeCount
        self.recurring = recurring
        self.videoCall = videoCall
        self.private = `private`
        self.alert = alert
        self.readOnly = readOnly
        self.invitation = invitation
    }
}

/// api.Event: an occurrence with everything the detail shows (`event.get`
/// and the results of the writes). Go embeds `Occurrence`, so on the wire
/// its fields sit beside these; here they are `occurrence`, the coding
/// flattens, and the occurrence's fields read through (`event.title`).
@dynamicMemberLookup
public struct Event: Codable, Sendable, Equatable {
    public var occurrence: Occurrence
    /// Pass back on update/delete/respond.
    public var etag: String
    /// Plain text.
    public var notes: String?
    /// https links found in `notes`.
    public var links: [String]?
    public var videoCallLink: VideoCall?
    public var organizerAddress: Address?
    public var attendees: [Attendee]?
    public var attendeesTruncated: Bool
    /// Of the series (on any instance).
    public var recurrence: Recurrence?
    /// Minutes before start.
    public var alerts: [Int]?
    public var responseRequested: Bool
    public var canEdit: Bool
    public var canDelete: Bool
    public var canRespond: Bool
    public var canProposeNewTime: Bool
    /// Remove from Calendar deletes the event for everyone (the user
    /// organises it).
    public var removeIsDelete: Bool
    /// "Show Invitation in Mail".
    public var invitationMessages: [MessageRef]?
    public var created: Date?
    public var updated: Date?

    public init(
        occurrence: Occurrence,
        etag: String,
        notes: String? = nil,
        links: [String]? = nil,
        videoCallLink: VideoCall? = nil,
        organizerAddress: Address? = nil,
        attendees: [Attendee]? = nil,
        attendeesTruncated: Bool,
        recurrence: Recurrence? = nil,
        alerts: [Int]? = nil,
        responseRequested: Bool,
        canEdit: Bool,
        canDelete: Bool,
        canRespond: Bool,
        canProposeNewTime: Bool,
        removeIsDelete: Bool,
        invitationMessages: [MessageRef]? = nil,
        created: Date? = nil,
        updated: Date? = nil
    ) {
        self.occurrence = occurrence
        self.etag = etag
        self.notes = notes
        self.links = links
        self.videoCallLink = videoCallLink
        self.organizerAddress = organizerAddress
        self.attendees = attendees
        self.attendeesTruncated = attendeesTruncated
        self.recurrence = recurrence
        self.alerts = alerts
        self.responseRequested = responseRequested
        self.canEdit = canEdit
        self.canDelete = canDelete
        self.canRespond = canRespond
        self.canProposeNewTime = canProposeNewTime
        self.removeIsDelete = removeIsDelete
        self.invitationMessages = invitationMessages
        self.created = created
        self.updated = updated
    }

    public subscript<T>(dynamicMember keyPath: WritableKeyPath<Occurrence, T>) -> T {
        get { occurrence[keyPath: keyPath] }
        set { occurrence[keyPath: keyPath] = newValue }
    }

    private enum CodingKeys: String, CodingKey {
        case etag, notes, links, videoCallLink, organizerAddress, attendees, attendeesTruncated, recurrence, alerts
        case responseRequested, canEdit, canDelete, canRespond, canProposeNewTime, removeIsDelete
        case invitationMessages, created, updated
    }

    public init(from decoder: any Decoder) throws {
        occurrence = try Occurrence(from: decoder)
        let c = try decoder.container(keyedBy: CodingKeys.self)
        etag = try c.decode(String.self, forKey: .etag)
        notes = try c.decodeIfPresent(String.self, forKey: .notes)
        links = try c.decodeIfPresent([String].self, forKey: .links)
        videoCallLink = try c.decodeIfPresent(VideoCall.self, forKey: .videoCallLink)
        organizerAddress = try c.decodeIfPresent(Address.self, forKey: .organizerAddress)
        attendees = try c.decodeIfPresent([Attendee].self, forKey: .attendees)
        attendeesTruncated = try c.decodeIfPresent(Bool.self, forKey: .attendeesTruncated) ?? false
        recurrence = try c.decodeIfPresent(Recurrence.self, forKey: .recurrence)
        alerts = try c.decodeIfPresent([Int].self, forKey: .alerts)
        responseRequested = try c.decodeIfPresent(Bool.self, forKey: .responseRequested) ?? false
        canEdit = try c.decode(Bool.self, forKey: .canEdit)
        canDelete = try c.decode(Bool.self, forKey: .canDelete)
        canRespond = try c.decode(Bool.self, forKey: .canRespond)
        canProposeNewTime = try c.decode(Bool.self, forKey: .canProposeNewTime)
        removeIsDelete = try c.decode(Bool.self, forKey: .removeIsDelete)
        invitationMessages = try c.decodeIfPresent([MessageRef].self, forKey: .invitationMessages)
        created = try c.decodeIfPresent(Date.self, forKey: .created)
        updated = try c.decodeIfPresent(Date.self, forKey: .updated)
    }

    public func encode(to encoder: any Encoder) throws {
        try occurrence.encode(to: encoder)
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(etag, forKey: .etag)
        try c.encodeIfPresent(notes, forKey: .notes)
        try c.encodeIfPresent(links, forKey: .links)
        try c.encodeIfPresent(videoCallLink, forKey: .videoCallLink)
        try c.encodeIfPresent(organizerAddress, forKey: .organizerAddress)
        try c.encodeIfPresent(attendees, forKey: .attendees)
        if attendeesTruncated { try c.encode(true, forKey: .attendeesTruncated) }
        try c.encodeIfPresent(recurrence, forKey: .recurrence)
        try c.encodeIfPresent(alerts, forKey: .alerts)
        if responseRequested { try c.encode(true, forKey: .responseRequested) }
        try c.encode(canEdit, forKey: .canEdit)
        try c.encode(canDelete, forKey: .canDelete)
        try c.encode(canRespond, forKey: .canRespond)
        try c.encode(canProposeNewTime, forKey: .canProposeNewTime)
        try c.encode(removeIsDelete, forKey: .removeIsDelete)
        try c.encodeIfPresent(invitationMessages, forKey: .invitationMessages)
        try c.encodeIfPresent(created, forKey: .created)
        try c.encodeIfPresent(updated, forKey: .updated)
    }
}

/// api.EventInputAttendee: an attendee as the editor gives it.
public struct EventInputAttendee: Codable, Sendable, Equatable {
    public var name: String?
    public var address: String
    /// `.required` or `.optional`; nil = required.
    public var role: AttendeeRole?

    public init(name: String? = nil, address: String, role: AttendeeRole? = nil) {
        self.name = name
        self.address = address
        self.role = role
    }
}

/// api.VideoCallInput: the editor's video call.
public struct VideoCallInput: Codable, Sendable, Equatable {
    public var mode: VideoCallMode
    /// `.create`: an `onlineMeetingProviders` value.
    public var provider: String?
    /// `.link`: https.
    public var url: String?

    public init(mode: VideoCallMode, provider: String? = nil, url: String? = nil) {
        self.mode = mode
        self.provider = provider
        self.url = url
    }
}

/// api.EventInput: an event as `event.create` and `event.update` take it:
/// timed (`start`, `end`, `timeZone` required) or all-day (`startDate`,
/// `endDate` exclusive and after `startDate`).
public struct EventInput: Codable, Sendable, Equatable {
    /// At most `API.Limits.maxEventTitleBytes` after cleaning; may be empty.
    public var title: String
    public var allDay: Bool
    public var start: LocalDateTime?
    public var end: LocalDateTime?
    public var timeZone: TimeZoneID?
    public var startDate: CalendarDay?
    public var endDate: CalendarDay?
    public var location: String?
    /// Plain text.
    public var notes: String?
    /// Without `complex` and `exceptions`; nil = none.
    public var recurrence: Recurrence?
    /// An update of a complex rule keeps it as it is (`recurrence` ignored).
    @DefaultFalse public var keepRecurrence: Bool
    /// Minutes before start. nil = the default alert of
    /// `calendar.preferences` (the key is left out); empty = none (sent as
    /// `[]`).
    public var alerts: [Int]?
    public var showAs: ShowAs?
    @DefaultFalse public var `private`: Bool
    public var attendees: [EventInputAttendee]?
    public var videoCall: VideoCallInput?
    /// `attachment.import` ids.
    public var attachments: [String]?

    public init(
        title: String,
        allDay: Bool,
        start: LocalDateTime? = nil,
        end: LocalDateTime? = nil,
        timeZone: TimeZoneID? = nil,
        startDate: CalendarDay? = nil,
        endDate: CalendarDay? = nil,
        location: String? = nil,
        notes: String? = nil,
        recurrence: Recurrence? = nil,
        keepRecurrence: Bool = false,
        alerts: [Int]? = nil,
        showAs: ShowAs? = nil,
        `private`: Bool = false,
        attendees: [EventInputAttendee]? = nil,
        videoCall: VideoCallInput? = nil,
        attachments: [String]? = nil
    ) {
        self.title = title
        self.allDay = allDay
        self.start = start
        self.end = end
        self.timeZone = timeZone
        self.startDate = startDate
        self.endDate = endDate
        self.location = location
        self.notes = notes
        self.recurrence = recurrence
        self.keepRecurrence = keepRecurrence
        self.alerts = alerts
        self.showAs = showAs
        self.private = `private`
        self.attendees = attendees
        self.videoCall = videoCall
        self.attachments = attachments
    }
}

/// api.MailText: a client's localised iMIP subject and body (the daemon is
/// language-neutral); each at most `API.Limits.maxMailTextBytes`.
public struct MailText: Codable, Sendable, Equatable {
    public var subject: String
    public var text: String

    public init(subject: String, text: String) {
        self.subject = subject
        self.text = text
    }
}

/// api.BusyInterval: a busy span of an `Availability`.
public struct BusyInterval: Codable, Sendable, Equatable {
    public var start: Date
    public var end: Date
    public var showAs: ShowAs

    public init(start: Date, end: Date, showAs: ShowAs) {
        self.start = start
        self.end = end
        self.showAs = showAs
    }
}

/// api.Availability: one person's free/busy (`calendar.freeBusy`).
public struct Availability: Codable, Sendable, Equatable {
    public var address: String
    /// False: no free/busy for this person.
    public var known: Bool
    @NullAsEmpty public var busy: [BusyInterval]

    public init(address: String, known: Bool, busy: [BusyInterval] = []) {
        self.address = address
        self.known = known
        self.busy = busy
    }
}

/// api.TimeSlot: a free slot (`calendar.findFreeTime`).
public struct TimeSlot: Codable, Sendable, Hashable {
    public var start: Date
    public var end: Date

    public init(start: Date, end: Date) {
        self.start = start
        self.end = end
    }
}

/// api.EventAlert: an alert `notify.eventAlert` announces.
public struct EventAlert: Codable, Sendable, Equatable {
    public var alertId: AlertID
    public var state: EventAlertState
    public var eventId: EventID
    public var recurrenceId: DateOrTime?
    public var accountId: AccountID
    public var calendarId: CalendarID
    public var title: String
    public var start: Date?
    public var end: Date?
    public var startDate: CalendarDay?
    public var allDay: Bool
    public var location: String?
    @DefaultFalse public var videoCall: Bool
    public var minutesBefore: Int

    public init(
        alertId: AlertID,
        state: EventAlertState,
        eventId: EventID,
        recurrenceId: DateOrTime? = nil,
        accountId: AccountID,
        calendarId: CalendarID,
        title: String,
        start: Date? = nil,
        end: Date? = nil,
        startDate: CalendarDay? = nil,
        allDay: Bool,
        location: String? = nil,
        videoCall: Bool = false,
        minutesBefore: Int
    ) {
        self.alertId = alertId
        self.state = state
        self.eventId = eventId
        self.recurrenceId = recurrenceId
        self.accountId = accountId
        self.calendarId = calendarId
        self.title = title
        self.start = start
        self.end = end
        self.startDate = startDate
        self.allDay = allDay
        self.location = location
        self.videoCall = videoCall
        self.minutesBefore = minutesBefore
    }
}

/// api.WorkingHours: the user's working days and hours, `HH:MM` in the
/// daemon's local zone.
public struct WorkingHours: Codable, Sendable, Equatable {
    /// `MO`…`SU`, at least one.
    public var days: [String]
    public var start: String
    /// After `start`.
    public var end: String

    public init(days: [String], start: String, end: String) {
        self.days = days
        self.start = start
        self.end = end
    }
}

/// api.CalendarPreferences: the calendar's daemon-side preferences.
public struct CalendarPreferences: Codable, Sendable, Equatable {
    public var weekStart: WeekStart
    /// 5–1440.
    public var defaultDurationMinutes: Int
    /// -1 none, 0–`API.Limits.maxAlertMinutes`.
    public var defaultAlertMinutes: Int
    public var showDeclined: Bool
    public var workingHours: WorkingHours

    public init(
        weekStart: WeekStart,
        defaultDurationMinutes: Int,
        defaultAlertMinutes: Int,
        showDeclined: Bool,
        workingHours: WorkingHours
    ) {
        self.weekStart = weekStart
        self.defaultDurationMinutes = defaultDurationMinutes
        self.defaultAlertMinutes = defaultAlertMinutes
        self.showDeclined = showDeclined
        self.workingHours = workingHours
    }
}

/// api.CalendarPreferencesUpdate: a missing field keeps its value;
/// `workingHours`, when given, is replaced whole.
public struct CalendarPreferencesUpdate: Codable, Sendable, Equatable {
    public var weekStart: WeekStart?
    public var defaultDurationMinutes: Int?
    public var defaultAlertMinutes: Int?
    public var showDeclined: Bool?
    public var workingHours: WorkingHours?

    public init(
        weekStart: WeekStart? = nil,
        defaultDurationMinutes: Int? = nil,
        defaultAlertMinutes: Int? = nil,
        showDeclined: Bool? = nil,
        workingHours: WorkingHours? = nil
    ) {
        self.weekStart = weekStart
        self.defaultDurationMinutes = defaultDurationMinutes
        self.defaultAlertMinutes = defaultAlertMinutes
        self.showDeclined = showDeclined
        self.workingHours = workingHours
    }
}

// MARK: Error data

/// api.CalendarConsentRequiredData: `error.data` of
/// `calendarConsentRequired`. `signIn`: `account.oauthStart` with
/// `calendar` can fix it; false for GOA.
public struct CalendarConsentRequiredData: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var signIn: Bool

    public init(accountId: AccountID, signIn: Bool) {
        self.accountId = accountId
        self.signIn = signIn
    }
}

/// api.ConflictEventData: `error.data` of `conflict` for an event write
/// whose etag is not the server's current one.
public struct ConflictEventData: Codable, Sendable, Equatable {
    public var event: Event

    public init(event: Event) {
        self.event = event
    }
}

extension RPCError {
    /// The data of a `calendarConsentRequired` error; nil for any other
    /// code or data of another shape.
    public var calendarConsentRequired: CalendarConsentRequiredData? {
        guard code == .calendarConsentRequired else { return nil }
        return decodeData(CalendarConsentRequiredData.self)
    }

    /// The current event of a `conflict` on an event write; nil for any
    /// other code, a draft conflict (no data) or data of another shape.
    public var conflictEvent: ConflictEventData? {
        guard code == .conflict else { return nil }
        return decodeData(ConflictEventData.self)
    }

    private func decodeData<T: Decodable>(_ type: T.Type) -> T? {
        guard let data, data.objectValue != nil, let raw = try? JSONCoding.encoder().encode(data) else { return nil }
        return try? JSONCoding.decoder().decode(type, from: raw)
    }
}

// MARK: calendar.* (docs/api.md §4.14)

public struct CalendarListParams: Codable, Sendable, Equatable {
    /// nil = every account with the capability.
    public var accountId: AccountID?

    public init(accountId: AccountID? = nil) {
        self.accountId = accountId
    }
}

public struct CalendarListResult: Codable, Sendable, Equatable {
    @NullAsEmpty public var accounts: [CalendarAccount]
    /// The invitations of enabled accounts waiting for an answer.
    public var unanswered: Int

    public init(accounts: [CalendarAccount] = [], unanswered: Int) {
        self.accounts = accounts
        self.unanswered = unanswered
    }
}

/// The user's own settings of one calendar; nil fields are left as they
/// are, `userColor` "" clears it.
public struct CalendarUpdateParams: Codable, Sendable, Equatable {
    public var calendarId: CalendarID
    public var visible: Bool?
    public var userColor: String?

    public init(calendarId: CalendarID, visible: Bool? = nil, userColor: String? = nil) {
        self.calendarId = calendarId
        self.visible = visible
        self.userColor = userColor
    }
}

public struct CalendarUpdateResult: Codable, Sendable, Equatable {
    public var calendar: CalendarRecord

    public init(calendar: CalendarRecord) {
        self.calendar = calendar
    }
}

public struct CalendarSyncParams: Codable, Sendable, Equatable {
    /// nil = every account.
    public var accountId: AccountID?
    /// nil = the account's calendars.
    public var calendarId: CalendarID?

    public init(accountId: AccountID? = nil, calendarId: CalendarID? = nil) {
        self.accountId = accountId
        self.calendarId = calendarId
    }
}

public struct CalendarPreferencesParams: Codable, Sendable, Equatable {
    public init() {}
}

public struct CalendarPreferencesResult: Codable, Sendable, Equatable {
    public var preferences: CalendarPreferences

    public init(preferences: CalendarPreferences) {
        self.preferences = preferences
    }
}

public struct CalendarSetPreferencesParams: Codable, Sendable, Equatable {
    public var preferences: CalendarPreferencesUpdate

    public init(preferences: CalendarPreferencesUpdate) {
        self.preferences = preferences
    }
}

public struct CalendarSetPreferencesResult: Codable, Sendable, Equatable {
    /// As stored.
    public var preferences: CalendarPreferences

    public init(preferences: CalendarPreferences) {
        self.preferences = preferences
    }
}

/// At most `API.Limits.maxFreeBusyAttendees`, `to - from` at most
/// `maxFreeBusyDays`; `intervalMinutes` 15, 30 or 60 (nil = 30).
public struct CalendarFreeBusyParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var attendees: [String]
    public var from: Date
    public var to: Date
    public var intervalMinutes: Int?

    public init(accountId: AccountID, attendees: [String], from: Date, to: Date, intervalMinutes: Int? = nil) {
        self.accountId = accountId
        self.attendees = attendees
        self.from = from
        self.to = to
        self.intervalMinutes = intervalMinutes
    }
}

/// `attendees` in the order asked.
public struct CalendarFreeBusyResult: Codable, Sendable, Equatable {
    public var me: Availability
    @NullAsEmpty public var attendees: [Availability]

    public init(me: Availability, attendees: [Availability] = []) {
        self.me = me
        self.attendees = attendees
    }
}

/// `workingHoursOnly` nil = true; `limit` nil = 5, at most
/// `API.Limits.maxFreeSlots`.
public struct CalendarFindFreeTimeParams: Codable, Sendable, Equatable {
    public var accountId: AccountID?
    public var attendees: [String]?
    public var durationMinutes: Int
    public var from: Date
    public var to: Date
    public var workingHoursOnly: Bool?
    public var limit: Int?

    public init(
        accountId: AccountID? = nil,
        attendees: [String]? = nil,
        durationMinutes: Int,
        from: Date,
        to: Date,
        workingHoursOnly: Bool? = nil,
        limit: Int? = nil
    ) {
        self.accountId = accountId
        self.attendees = attendees
        self.durationMinutes = durationMinutes
        self.from = from
        self.to = to
        self.workingHoursOnly = workingHoursOnly
        self.limit = limit
    }
}

public struct CalendarFindFreeTimeResult: Codable, Sendable, Equatable {
    @NullAsEmpty public var slots: [TimeSlot]
    /// Attendees without free/busy.
    @NullAsEmpty public var unknown: [String]

    public init(slots: [TimeSlot] = [], unknown: [String] = []) {
        self.slots = slots
        self.unknown = unknown
    }
}

// MARK: event.* (docs/api.md §4.15)

/// `to - from` at most `API.Limits.maxEventListDays`; `limit` nil =
/// `defaultEventListResults`, at most `maxEventListResults`.
/// `includeDeclined` nil = the showDeclined preference;
/// `includeInvitations` nil = true.
public struct EventListParams: Codable, Sendable, Equatable {
    public var from: Date
    public var to: Date
    public var timeZone: TimeZoneID
    public var calendarIds: [CalendarID]?
    public var accountIds: [AccountID]?
    @DefaultFalse public var includeHidden: Bool
    public var includeDeclined: Bool?
    public var includeInvitations: Bool?
    public var limit: Int?

    public init(
        from: Date,
        to: Date,
        timeZone: TimeZoneID,
        calendarIds: [CalendarID]? = nil,
        accountIds: [AccountID]? = nil,
        includeHidden: Bool = false,
        includeDeclined: Bool? = nil,
        includeInvitations: Bool? = nil,
        limit: Int? = nil
    ) {
        self.from = from
        self.to = to
        self.timeZone = timeZone
        self.calendarIds = calendarIds
        self.accountIds = accountIds
        self.includeHidden = includeHidden
        self.includeDeclined = includeDeclined
        self.includeInvitations = includeInvitations
        self.limit = limit
    }
}

public struct EventListResult: Codable, Sendable, Equatable {
    @NullAsEmpty public var occurrences: [Occurrence]
    /// `limit` reached.
    public var truncated: Bool
    /// False: a part of the range outside an account's window could not be
    /// fetched; what is stored is returned.
    public var complete: Bool

    public init(occurrences: [Occurrence] = [], truncated: Bool, complete: Bool) {
        self.occurrences = occurrences
        self.truncated = truncated
        self.complete = complete
    }
}

public struct EventGetParams: Codable, Sendable, Equatable {
    public var eventId: EventID
    public var recurrenceId: DateOrTime?
    public var timeZone: TimeZoneID?

    public init(eventId: EventID, recurrenceId: DateOrTime? = nil, timeZone: TimeZoneID? = nil) {
        self.eventId = eventId
        self.recurrenceId = recurrenceId
        self.timeZone = timeZone
    }
}

public struct EventGetResult: Codable, Sendable, Equatable {
    public var event: Event

    public init(event: Event) {
        self.event = event
    }
}

/// `query` 1–256 bytes; `limit` nil = 50, at most
/// `API.Limits.maxEventSearchResults`.
public struct EventSearchParams: Codable, Sendable, Equatable {
    public var query: String
    public var from: Date?
    public var to: Date?
    public var calendarIds: [CalendarID]?
    public var limit: Int?

    public init(
        query: String,
        from: Date? = nil,
        to: Date? = nil,
        calendarIds: [CalendarID]? = nil,
        limit: Int? = nil
    ) {
        self.query = query
        self.from = from
        self.to = to
        self.calendarIds = calendarIds
        self.limit = limit
    }
}

public struct EventSearchResult: Codable, Sendable, Equatable {
    @NullAsEmpty public var occurrences: [Occurrence]
    /// At most 1000.
    public var total: Int

    public init(occurrences: [Occurrence] = [], total: Int) {
        self.occurrences = occurrences
        self.total = total
    }
}

/// api.InvitationsSent: how the invitations, updates or cancellations of a
/// write left.
public struct InvitationsSent: Codable, Sendable, Equatable {
    public var via: InvitationsVia
    public var count: Int

    public init(via: InvitationsVia, count: Int) {
        self.via = via
        self.count = count
    }
}

/// `sendInvitations` nil = true.
public struct EventCreateParams: Codable, Sendable, Equatable {
    public var calendarId: CalendarID
    public var event: EventInput
    public var sendInvitations: Bool?
    public var mail: MailText?

    public init(calendarId: CalendarID, event: EventInput, sendInvitations: Bool? = nil, mail: MailText? = nil) {
        self.calendarId = calendarId
        self.event = event
        self.sendInvitations = sendInvitations
        self.mail = mail
    }
}

public struct EventCreateResult: Codable, Sendable, Equatable {
    public var event: Event
    public var invitations: InvitationsSent

    public init(event: Event, invitations: InvitationsSent) {
        self.event = event
        self.invitations = invitations
    }
}

/// `event` replaces every field of the scope's target; `sendUpdates` nil =
/// true.
public struct EventUpdateParams: Codable, Sendable, Equatable {
    public var eventId: EventID
    public var recurrenceId: DateOrTime?
    public var scope: EditScope
    public var etag: String
    public var event: EventInput
    public var sendUpdates: Bool?
    public var mail: MailText?

    public init(
        eventId: EventID,
        recurrenceId: DateOrTime? = nil,
        scope: EditScope,
        etag: String,
        event: EventInput,
        sendUpdates: Bool? = nil,
        mail: MailText? = nil
    ) {
        self.eventId = eventId
        self.recurrenceId = recurrenceId
        self.scope = scope
        self.etag = etag
        self.event = event
        self.sendUpdates = sendUpdates
        self.mail = mail
    }
}

public struct EventUpdateResult: Codable, Sendable, Equatable {
    public var event: Event
    /// Overrides that no longer fit the changed rule.
    public var droppedExceptions: Int
    public var invitations: InvitationsSent

    public init(event: Event, droppedExceptions: Int, invitations: InvitationsSent) {
        self.event = event
        self.droppedExceptions = droppedExceptions
        self.invitations = invitations
    }
}

/// `notify` nil = true; `comment` at most `API.Limits.maxCommentBytes`.
public struct EventDeleteParams: Codable, Sendable, Equatable {
    public var eventId: EventID
    public var recurrenceId: DateOrTime?
    public var scope: EditScope
    public var etag: String
    public var notify: Bool?
    public var comment: String?
    public var mail: MailText?

    public init(
        eventId: EventID,
        recurrenceId: DateOrTime? = nil,
        scope: EditScope,
        etag: String,
        notify: Bool? = nil,
        comment: String? = nil,
        mail: MailText? = nil
    ) {
        self.eventId = eventId
        self.recurrenceId = recurrenceId
        self.scope = scope
        self.etag = etag
        self.notify = notify
        self.comment = comment
        self.mail = mail
    }
}

public struct EventDeleteResult: Codable, Sendable, Equatable {
    public var invitations: InvitationsSent

    public init(invitations: InvitationsSent) {
        self.invitations = invitations
    }
}

/// `scope` this or all; `response` accepted, tentative or declined;
/// `sendResponse` nil = true; a proposed time needs both ends and
/// `Event.canProposeNewTime`.
public struct EventRespondParams: Codable, Sendable, Equatable {
    public var eventId: EventID
    public var recurrenceId: DateOrTime?
    public var scope: EditScope
    public var etag: String
    public var response: EventResponse
    public var sendResponse: Bool?
    public var comment: String?
    public var proposedStart: Date?
    public var proposedEnd: Date?
    public var mail: MailText?

    public init(
        eventId: EventID,
        recurrenceId: DateOrTime? = nil,
        scope: EditScope,
        etag: String,
        response: EventResponse,
        sendResponse: Bool? = nil,
        comment: String? = nil,
        proposedStart: Date? = nil,
        proposedEnd: Date? = nil,
        mail: MailText? = nil
    ) {
        self.eventId = eventId
        self.recurrenceId = recurrenceId
        self.scope = scope
        self.etag = etag
        self.response = response
        self.sendResponse = sendResponse
        self.comment = comment
        self.proposedStart = proposedStart
        self.proposedEnd = proposedEnd
        self.mail = mail
    }
}

public struct EventRespondResult: Codable, Sendable, Equatable {
    public var event: Event
    public var via: InvitationsVia

    public init(event: Event, via: InvitationsVia) {
        self.event = event
        self.via = via
    }
}

/// `minutes` 1–1440.
public struct EventSnoozeAlertParams: Codable, Sendable, Equatable {
    public var alertId: AlertID
    public var minutes: Int

    public init(alertId: AlertID, minutes: Int) {
        self.alertId = alertId
        self.minutes = minutes
    }
}

public struct EventDismissAlertParams: Codable, Sendable, Equatable {
    public var alertId: AlertID

    public init(alertId: AlertID) {
        self.alertId = alertId
    }
}

// MARK: invitation.* (docs/api.md §4.16)

/// api.InvitationSummary: rides on a listed message
/// (`MessageSummary.invitation`).
public struct InvitationSummary: Codable, Sendable, Equatable {
    public var method: InvitationMethod
    public var kind: InvitationKind
    public var state: InvitationState
    public var title: String
    public var start: Date?
    public var end: Date?
    public var startDate: CalendarDay?
    @DefaultFalse public var allDay: Bool
    public var organizer: Address?
    /// A reply: the attendee who answered.
    public var replyFrom: Address?
    /// The organiser is one of the account's own addresses: the request is
    /// `processed`, its card has no response path and may carry `eventId`
    /// (Show in Calendar).
    @DefaultFalse public var ownEvent: Bool

    public init(
        method: InvitationMethod,
        kind: InvitationKind,
        state: InvitationState,
        title: String,
        start: Date? = nil,
        end: Date? = nil,
        startDate: CalendarDay? = nil,
        allDay: Bool = false,
        organizer: Address? = nil,
        replyFrom: Address? = nil,
        ownEvent: Bool = false
    ) {
        self.method = method
        self.kind = kind
        self.state = state
        self.title = title
        self.start = start
        self.end = end
        self.startDate = startDate
        self.allDay = allDay
        self.organizer = organizer
        self.replyFrom = replyFrom
        self.ownEvent = ownEvent
    }
}

/// api.InvitationMe: the user on an invitation's attendee list.
public struct InvitationMe: Codable, Sendable, Equatable {
    public var address: String
    public var response: EventResponse

    public init(address: String, response: EventResponse) {
        self.address = address
        self.response = response
    }
}

/// api.InvitationPrevious: what the stored event had before an update. Each
/// field is present only when it changed (`start`/`end` when the time did);
/// an older daemon sends `start` and `end` always.
///
/// `InvitationCard.response` may be set with `respondedAt` absent: an answer
/// given in another client, read from the linked event.
public struct InvitationPrevious: Codable, Sendable, Equatable {
    public var start: DateOrTime?
    public var end: DateOrTime?
    /// The previous location, when it changed.
    public var location: String?
    /// The previous rule, when it changed.
    public var recurrence: Recurrence?

    public init(start: DateOrTime? = nil, end: DateOrTime? = nil, location: String? = nil, recurrence: Recurrence? = nil) {
        self.start = start
        self.end = end
        self.location = location
        self.recurrence = recurrence
    }
}

/// api.InvitationConflict: a stored occurrence overlapping an invitation.
public struct InvitationConflict: Codable, Sendable, Equatable {
    /// Absent for a private event of a shared calendar.
    public var title: String?
    public var start: Date
    public var end: Date
    public var calendarId: CalendarID
    public var eventId: EventID

    public init(title: String? = nil, start: Date, end: Date, calendarId: CalendarID, eventId: EventID) {
        self.title = title
        self.start = start
        self.end = end
        self.calendarId = calendarId
        self.eventId = eventId
    }
}

/// api.DayBusy: a busy span of an invitation's mini day strip.
public struct DayBusy: Codable, Sendable, Equatable {
    public var start: Date
    public var end: Date
    public var calendarId: CalendarID?

    public init(start: Date, end: Date, calendarId: CalendarID? = nil) {
        self.start = start
        self.end = end
        self.calendarId = calendarId
    }
}

/// api.InvitationDay: the mini day strip of an invitation card.
public struct InvitationDay: Codable, Sendable, Equatable {
    public var from: Date
    public var to: Date
    @NullAsEmpty public var busy: [DayBusy]

    public init(from: Date, to: Date, busy: [DayBusy] = []) {
        self.from = from
        self.to = to
        self.busy = busy
    }
}

/// api.InvitationTarget: a calendar the invitation's event can go to.
public struct InvitationTarget: Codable, Sendable, Equatable {
    public var calendarId: CalendarID
    @DefaultFalse public var isDefault: Bool

    public init(calendarId: CalendarID, isDefault: Bool = false) {
        self.calendarId = calendarId
        self.isDefault = isDefault
    }
}

/// api.InvitationCard: the invitation card of a message
/// (`Message.invitation`), rendered by a client from these fields, never
/// from HTML. Go embeds `InvitationSummary`; here it is `summary`, the
/// coding flattens, and its fields read through (`card.kind`).
@dynamicMemberLookup
public struct InvitationCard: Codable, Sendable, Equatable {
    public var summary: InvitationSummary
    public var partId: String
    public var uid: String
    public var recurrenceId: DateOrTime?
    public var sequence: Int
    public var endDate: CalendarDay?
    public var timeZone: TimeZoneID?
    public var recurrence: Recurrence?
    public var location: String?
    public var videoCall: VideoCall?
    /// Plain text.
    public var notes: String?
    /// The organiser's domain is not the From domain.
    public var organizerMismatch: Bool
    public var attendeeCount: Int
    /// At most `API.Limits.maxInvitationAttendees`.
    public var attendees: [Attendee]?
    /// Absent: the user is not on the list.
    public var me: InvitationMe?
    /// The user's answer.
    public var response: EventResponse?
    public var respondedAt: Date?
    /// The stored event it is linked to.
    public var eventId: EventID?
    public var calendarId: CalendarID?
    public var previous: InvitationPrevious?
    /// At most 3.
    public var conflicts: [InvitationConflict]
    public var day: InvitationDay
    /// "Add to:", the account's default first; empty: the answer only.
    public var targets: [InvitationTarget]
    /// The server chose the calendar (Graph).
    public var fixedTarget: Bool
    public var canRespond: Bool
    public var canProposeNewTime: Bool
    public var replyVia: InvitationsVia
    /// The account the answer leaves from.
    public var replyAccountId: AccountID?
    /// The organiser it goes to.
    public var replyTo: Address?
    /// A reply: the attendee's answer was applied to the user's event.
    public var replyApplied: Bool

    public init(
        summary: InvitationSummary,
        partId: String,
        uid: String,
        recurrenceId: DateOrTime? = nil,
        sequence: Int,
        endDate: CalendarDay? = nil,
        timeZone: TimeZoneID? = nil,
        recurrence: Recurrence? = nil,
        location: String? = nil,
        videoCall: VideoCall? = nil,
        notes: String? = nil,
        organizerMismatch: Bool,
        attendeeCount: Int,
        attendees: [Attendee]? = nil,
        me: InvitationMe? = nil,
        response: EventResponse? = nil,
        respondedAt: Date? = nil,
        eventId: EventID? = nil,
        calendarId: CalendarID? = nil,
        previous: InvitationPrevious? = nil,
        conflicts: [InvitationConflict],
        day: InvitationDay,
        targets: [InvitationTarget],
        fixedTarget: Bool,
        canRespond: Bool,
        canProposeNewTime: Bool,
        replyVia: InvitationsVia,
        replyAccountId: AccountID? = nil,
        replyTo: Address? = nil,
        replyApplied: Bool
    ) {
        self.summary = summary
        self.partId = partId
        self.uid = uid
        self.recurrenceId = recurrenceId
        self.sequence = sequence
        self.endDate = endDate
        self.timeZone = timeZone
        self.recurrence = recurrence
        self.location = location
        self.videoCall = videoCall
        self.notes = notes
        self.organizerMismatch = organizerMismatch
        self.attendeeCount = attendeeCount
        self.attendees = attendees
        self.me = me
        self.response = response
        self.respondedAt = respondedAt
        self.eventId = eventId
        self.calendarId = calendarId
        self.previous = previous
        self.conflicts = conflicts
        self.day = day
        self.targets = targets
        self.fixedTarget = fixedTarget
        self.canRespond = canRespond
        self.canProposeNewTime = canProposeNewTime
        self.replyVia = replyVia
        self.replyAccountId = replyAccountId
        self.replyTo = replyTo
        self.replyApplied = replyApplied
    }

    public subscript<T>(dynamicMember keyPath: WritableKeyPath<InvitationSummary, T>) -> T {
        get { summary[keyPath: keyPath] }
        set { summary[keyPath: keyPath] = newValue }
    }

    private enum CodingKeys: String, CodingKey {
        case partId, uid, recurrenceId, sequence, endDate, timeZone, recurrence, location, videoCall, notes
        case organizerMismatch, attendeeCount, attendees, me, response, respondedAt, eventId, calendarId, previous
        case conflicts, day, targets, fixedTarget, canRespond, canProposeNewTime, replyVia, replyAccountId
        case replyTo, replyApplied
    }

    public init(from decoder: any Decoder) throws {
        summary = try InvitationSummary(from: decoder)
        let c = try decoder.container(keyedBy: CodingKeys.self)
        partId = try c.decode(String.self, forKey: .partId)
        uid = try c.decode(String.self, forKey: .uid)
        recurrenceId = try c.decodeIfPresent(DateOrTime.self, forKey: .recurrenceId)
        sequence = try c.decode(Int.self, forKey: .sequence)
        endDate = try c.decodeIfPresent(CalendarDay.self, forKey: .endDate)
        timeZone = try c.decodeIfPresent(TimeZoneID.self, forKey: .timeZone)
        recurrence = try c.decodeIfPresent(Recurrence.self, forKey: .recurrence)
        location = try c.decodeIfPresent(String.self, forKey: .location)
        videoCall = try c.decodeIfPresent(VideoCall.self, forKey: .videoCall)
        notes = try c.decodeIfPresent(String.self, forKey: .notes)
        organizerMismatch = try c.decodeIfPresent(Bool.self, forKey: .organizerMismatch) ?? false
        attendeeCount = try c.decode(Int.self, forKey: .attendeeCount)
        attendees = try c.decodeIfPresent([Attendee].self, forKey: .attendees)
        me = try c.decodeIfPresent(InvitationMe.self, forKey: .me)
        response = try c.decodeIfPresent(EventResponse.self, forKey: .response)
        respondedAt = try c.decodeIfPresent(Date.self, forKey: .respondedAt)
        eventId = try c.decodeIfPresent(EventID.self, forKey: .eventId)
        calendarId = try c.decodeIfPresent(CalendarID.self, forKey: .calendarId)
        previous = try c.decodeIfPresent(InvitationPrevious.self, forKey: .previous)
        conflicts = try c.decodeIfPresent([InvitationConflict].self, forKey: .conflicts) ?? []
        day = try c.decode(InvitationDay.self, forKey: .day)
        targets = try c.decodeIfPresent([InvitationTarget].self, forKey: .targets) ?? []
        fixedTarget = try c.decodeIfPresent(Bool.self, forKey: .fixedTarget) ?? false
        canRespond = try c.decode(Bool.self, forKey: .canRespond)
        canProposeNewTime = try c.decode(Bool.self, forKey: .canProposeNewTime)
        replyVia = try c.decode(InvitationsVia.self, forKey: .replyVia)
        replyAccountId = try c.decodeIfPresent(AccountID.self, forKey: .replyAccountId)
        replyTo = try c.decodeIfPresent(Address.self, forKey: .replyTo)
        replyApplied = try c.decodeIfPresent(Bool.self, forKey: .replyApplied) ?? false
    }

    public func encode(to encoder: any Encoder) throws {
        try summary.encode(to: encoder)
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(partId, forKey: .partId)
        try c.encode(uid, forKey: .uid)
        try c.encodeIfPresent(recurrenceId, forKey: .recurrenceId)
        try c.encode(sequence, forKey: .sequence)
        try c.encodeIfPresent(endDate, forKey: .endDate)
        try c.encodeIfPresent(timeZone, forKey: .timeZone)
        try c.encodeIfPresent(recurrence, forKey: .recurrence)
        try c.encodeIfPresent(location, forKey: .location)
        try c.encodeIfPresent(videoCall, forKey: .videoCall)
        try c.encodeIfPresent(notes, forKey: .notes)
        if organizerMismatch { try c.encode(true, forKey: .organizerMismatch) }
        try c.encode(attendeeCount, forKey: .attendeeCount)
        try c.encodeIfPresent(attendees, forKey: .attendees)
        try c.encodeIfPresent(me, forKey: .me)
        try c.encodeIfPresent(response, forKey: .response)
        try c.encodeIfPresent(respondedAt, forKey: .respondedAt)
        try c.encodeIfPresent(eventId, forKey: .eventId)
        try c.encodeIfPresent(calendarId, forKey: .calendarId)
        try c.encodeIfPresent(previous, forKey: .previous)
        try c.encode(conflicts, forKey: .conflicts)
        try c.encode(day, forKey: .day)
        try c.encode(targets, forKey: .targets)
        if fixedTarget { try c.encode(true, forKey: .fixedTarget) }
        try c.encode(canRespond, forKey: .canRespond)
        try c.encode(canProposeNewTime, forKey: .canProposeNewTime)
        try c.encode(replyVia, forKey: .replyVia)
        try c.encodeIfPresent(replyAccountId, forKey: .replyAccountId)
        try c.encodeIfPresent(replyTo, forKey: .replyTo)
        if replyApplied { try c.encode(true, forKey: .replyApplied) }
    }
}

/// `limit` nil = 200, at most `API.Limits.maxInvitationList`.
public struct InvitationListParams: Codable, Sendable, Equatable {
    public var accountIds: [AccountID]?
    public var state: InvitationState?
    public var limit: Int?

    public init(accountIds: [AccountID]? = nil, state: InvitationState? = nil, limit: Int? = nil) {
        self.accountIds = accountIds
        self.state = state
        self.limit = limit
    }
}

/// api.InvitationListItem: one invitation (the newest message of its UID).
public struct InvitationListItem: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var messageId: MessageID
    public var summary: InvitationSummary

    public init(accountId: AccountID, messageId: MessageID, summary: InvitationSummary) {
        self.accountId = accountId
        self.messageId = messageId
        self.summary = summary
    }
}

public struct InvitationListResult: Codable, Sendable, Equatable {
    @NullAsEmpty public var invitations: [InvitationListItem]

    public init(invitations: [InvitationListItem] = []) {
        self.invitations = invitations
    }
}

/// `response` accepted, tentative or declined; `calendarId` one of the
/// card's targets; `sendResponse` nil = true.
public struct InvitationRespondParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var messageId: MessageID
    public var response: EventResponse
    public var calendarId: CalendarID?
    public var sendResponse: Bool?
    public var comment: String?
    public var proposedStart: Date?
    public var proposedEnd: Date?
    public var mail: MailText?

    public init(
        accountId: AccountID,
        messageId: MessageID,
        response: EventResponse,
        calendarId: CalendarID? = nil,
        sendResponse: Bool? = nil,
        comment: String? = nil,
        proposedStart: Date? = nil,
        proposedEnd: Date? = nil,
        mail: MailText? = nil
    ) {
        self.accountId = accountId
        self.messageId = messageId
        self.response = response
        self.calendarId = calendarId
        self.sendResponse = sendResponse
        self.comment = comment
        self.proposedStart = proposedStart
        self.proposedEnd = proposedEnd
        self.mail = mail
    }
}

public struct InvitationRespondResult: Codable, Sendable, Equatable {
    public var invitation: InvitationCard
    public var eventId: EventID?
    /// The message was archived (`CalendarConfig.afterAnswer`).
    public var movedTo: FolderID?

    public init(invitation: InvitationCard, eventId: EventID? = nil, movedTo: FolderID? = nil) {
        self.invitation = invitation
        self.eventId = eventId
        self.movedTo = movedTo
    }
}

public struct InvitationApplyCancelParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var messageId: MessageID

    public init(accountId: AccountID, messageId: MessageID) {
        self.accountId = accountId
        self.messageId = messageId
    }
}

public struct InvitationApplyCancelResult: Codable, Sendable, Equatable {
    /// False: no stored event was linked.
    public var removed: Bool

    public init(removed: Bool) {
        self.removed = removed
    }
}

// MARK: Notifications (docs/api.md §5)

/// api.CalendarChangedNotification (`notify.calendarChanged`): stored
/// calendars or occurrences changed; `from`/`to` bound the changed
/// occurrences when known. Without calendars: the preferences changed.
public struct CalendarChangedNotification: Codable, Sendable, Equatable {
    public var accountIds: [AccountID]?
    public var calendarIds: [CalendarID]?
    public var from: Date?
    public var to: Date?

    public init(
        accountIds: [AccountID]? = nil,
        calendarIds: [CalendarID]? = nil,
        from: Date? = nil,
        to: Date? = nil
    ) {
        self.accountIds = accountIds
        self.calendarIds = calendarIds
        self.from = from
        self.to = to
    }
}

/// api.CalendarStateNotification (`notify.calendarState`).
public struct CalendarStateNotification: Codable, Sendable, Equatable {
    public var state: CalendarSyncState

    public init(state: CalendarSyncState) {
        self.state = state
    }
}

/// api.EventAlertNotification (`notify.eventAlert`).
public struct EventAlertNotification: Codable, Sendable, Equatable {
    public var alert: EventAlert

    public init(alert: EventAlert) {
        self.alert = alert
    }
}

/// api.InvitationsChangedNotification (`notify.invitationsChanged`): the
/// unanswered invitations of these accounts (absent = any) changed.
public struct InvitationsChangedNotification: Codable, Sendable, Equatable {
    public var accountIds: [AccountID]?

    public init(accountIds: [AccountID]? = nil) {
        self.accountIds = accountIds
    }

    /// True when the change concerns `account`.
    public func concerns(_ account: AccountID) -> Bool {
        (accountIds?.isEmpty ?? true) || accountIds?.contains(account) == true
    }
}

// MARK: Methods

extension API {
    // Calendar accounts (docs/api.md §4.1).

    public enum AccountDiscoverCalDAV: RPCMethod {
        public typealias Params = AccountDiscoverCalDAVParams
        public typealias Result = AccountDiscoverCalDAVResult
        public static let name = "account.discoverCalDAV"
        public static let timeout = RPCTimeouts.discover
    }

    public enum AccountListCalDAVCalendars: RPCMethod {
        public typealias Params = AccountListCalDAVCalendarsParams
        public typealias Result = AccountListCalDAVCalendarsResult
        public static let name = "account.listCalDAVCalendars"
        public static let timeout = RPCTimeouts.test
    }

    // Calendars (§4.14).

    public enum CalendarList: RPCMethod {
        public typealias Params = CalendarListParams
        public typealias Result = CalendarListResult
        public static let name = "calendar.list"
    }

    public enum CalendarUpdate: RPCMethod {
        public typealias Params = CalendarUpdateParams
        public typealias Result = CalendarUpdateResult
        public static let name = "calendar.update"
    }

    /// Asks for a pass and answers at once; progress arrives as
    /// `notify.calendarState`.
    public enum CalendarSync: RPCMethod {
        public typealias Params = CalendarSyncParams
        public typealias Result = EmptyResult
        public static let name = "calendar.sync"
    }

    public enum CalendarPreferencesGet: RPCMethod {
        public typealias Params = CalendarPreferencesParams
        public typealias Result = CalendarPreferencesResult
        public static let name = "calendar.preferences"
    }

    public enum CalendarSetPreferences: RPCMethod {
        public typealias Params = CalendarSetPreferencesParams
        public typealias Result = CalendarSetPreferencesResult
        public static let name = "calendar.setPreferences"
    }

    /// Asks the source about other people: a network call.
    public enum CalendarFreeBusy: RPCMethod {
        public typealias Params = CalendarFreeBusyParams
        public typealias Result = CalendarFreeBusyResult
        public static let name = "calendar.freeBusy"
        public static let timeout: Duration = .seconds(30)
    }

    public enum CalendarFindFreeTime: RPCMethod {
        public typealias Params = CalendarFindFreeTimeParams
        public typealias Result = CalendarFindFreeTimeResult
        public static let name = "calendar.findFreeTime"
        public static let timeout: Duration = .seconds(30)
    }

    // Events (§4.15). The reads answer from the local store; the writes
    // wait for the server.

    public enum EventList: RPCMethod {
        public typealias Params = EventListParams
        public typealias Result = EventListResult
        public static let name = "event.list"
    }

    public enum EventGet: RPCMethod {
        public typealias Params = EventGetParams
        public typealias Result = EventGetResult
        public static let name = "event.get"
    }

    public enum EventSearch: RPCMethod {
        public typealias Params = EventSearchParams
        public typealias Result = EventSearchResult
        public static let name = "event.search"
    }

    public enum EventCreate: RPCMethod {
        public typealias Params = EventCreateParams
        public typealias Result = EventCreateResult
        public static let name = "event.create"
        public static let timeout: Duration = .seconds(45)
    }

    public enum EventUpdate: RPCMethod {
        public typealias Params = EventUpdateParams
        public typealias Result = EventUpdateResult
        public static let name = "event.update"
        public static let timeout: Duration = .seconds(45)
    }

    public enum EventDelete: RPCMethod {
        public typealias Params = EventDeleteParams
        public typealias Result = EventDeleteResult
        public static let name = "event.delete"
        public static let timeout: Duration = .seconds(45)
    }

    public enum EventRespond: RPCMethod {
        public typealias Params = EventRespondParams
        public typealias Result = EventRespondResult
        public static let name = "event.respond"
        public static let timeout: Duration = .seconds(45)
    }

    public enum EventSnoozeAlert: RPCMethod {
        public typealias Params = EventSnoozeAlertParams
        public typealias Result = EmptyResult
        public static let name = "event.snoozeAlert"
    }

    public enum EventDismissAlert: RPCMethod {
        public typealias Params = EventDismissAlertParams
        public typealias Result = EmptyResult
        public static let name = "event.dismissAlert"
    }

    // Invitations (§4.16).

    public enum InvitationList: RPCMethod {
        public typealias Params = InvitationListParams
        public typealias Result = InvitationListResult
        public static let name = "invitation.list"
    }

    public enum InvitationRespond: RPCMethod {
        public typealias Params = InvitationRespondParams
        public typealias Result = InvitationRespondResult
        public static let name = "invitation.respond"
        public static let timeout: Duration = .seconds(45)
    }

    public enum InvitationApplyCancel: RPCMethod {
        public typealias Params = InvitationApplyCancelParams
        public typealias Result = InvitationApplyCancelResult
        public static let name = "invitation.applyCancel"
    }

    /// The calendar methods, in the order of methods.go.
    static let calendarMethods: [any RPCMethod.Type] = [
        AccountDiscoverCalDAV.self, AccountListCalDAVCalendars.self,
        CalendarList.self, CalendarUpdate.self, CalendarSync.self, CalendarPreferencesGet.self,
        CalendarSetPreferences.self, CalendarFreeBusy.self, CalendarFindFreeTime.self,
        EventList.self, EventGet.self, EventSearch.self, EventCreate.self, EventUpdate.self,
        EventDelete.self, EventRespond.self, EventSnoozeAlert.self, EventDismissAlert.self,
        InvitationList.self, InvitationRespond.self, InvitationApplyCancel.self,
    ]
}

// MARK: Limits

extension API.Limits {
    /// One iCalendar object or calendar part.
    public static let maxICSBytes = 1 << 20
    public static let maxEventTitleBytes = 1024
    public static let maxEventLocationBytes = 1024
    public static let maxEventNotesBytes = 32 << 10
    /// `MailText.subject` and `.text`, each.
    public static let maxMailTextBytes = 4 << 10
    public static let maxCommentBytes = 2 << 10
    /// `EventInput.attendees`.
    public static let maxEventAttendeesIn = 100
    /// `Event.attendees` (`attendeeCount` counts all).
    public static let maxEventAttendeesStored = 500
    /// `InvitationCard.attendees`.
    public static let maxInvitationAttendees = 50
    public static let maxEventAlerts = 4
    /// 4 weeks.
    public static let maxAlertMinutes = 40320
    public static let maxEventAttachments = 10
    public static let maxEventAttachmentBytes = 3 << 20
    public static let maxEventListDays = 400
    public static let maxEventListResults = 5000
    public static let defaultEventListResults = 2000
    public static let maxEventSearchResults = 200
    /// Per series per query window.
    public static let maxSeriesOccurrences = 1000
    public static let maxCalendarsPerAccount = 200
    public static let maxEventsPerCalendar = 50000
    public static let maxFreeBusyAttendees = 20
    public static let maxFreeBusyDays = 62
    public static let maxFreeSlots = 50
    public static let maxInvitationList = 500
    /// The window's forward edge.
    public static let calendarFutureDays = 400
    public static let defaultCalendarKeepDays = 365
    /// Input.
    public static let maxRecurrenceInterval = 99
    /// Input.
    public static let maxRecurrenceCount = 999
}

/// A decoded calendar notification. `DaemonNotification` reads these four
/// as `.unknown` (its switch is exhaustive in the mail code), so the
/// calendar controller decodes the raw notification with this.
public enum CalendarNotification: Sendable, Equatable {
    /// Stored calendars or occurrences changed.
    case changed(CalendarChangedNotification)
    /// An account's calendar sync state changed.
    case state(CalendarSyncState)
    /// An alert is due or was dismissed (`EventAlert.state`).
    case alert(EventAlert)
    /// The unanswered invitations of the accounts named (none = any) changed.
    case invitationsChanged(InvitationsChangedNotification)

    /// Decodes the params of a raw notification by its method; nil for a
    /// method that is not a calendar one. Throws when a calendar method
    /// carries params of the wrong shape.
    public init?(_ raw: RPCNotification) throws {
        switch raw.method {
        case API.Notify.calendarChanged:
            // Missing or null params mean "everything".
            let e = try JSONCoding.decoder().decode(OptionalParamsEnvelope<CalendarChangedNotification>.self, from: raw.line)
            self = .changed(e.params ?? CalendarChangedNotification())
        case API.Notify.calendarState:
            self = .state(try raw.params(CalendarStateNotification.self).state)
        case API.Notify.eventAlert:
            self = .alert(try raw.params(EventAlertNotification.self).alert)
        case API.Notify.invitationsChanged:
            let e = try JSONCoding.decoder().decode(OptionalParamsEnvelope<InvitationsChangedNotification>.self, from: raw.line)
            self = .invitationsChanged(e.params ?? InvitationsChangedNotification())
        default:
            return nil
        }
    }
}
