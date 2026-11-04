// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Where the calendar's data comes from (docs/calendar.md §5.2 "Source
// seam"). The controllers read a snapshot (the accounts with their
// calendars and states, the unanswered invitations, the preferences),
// ask for the occurrences of a range and for single events, and write
// through the source; they are told what changed through `observe`. The
// daemon's source (`DaemonCalendarSource`) caches the occurrences per
// range and the events by id and drops them when `notify.calendarChanged`
// names an overlapping range; the in-memory one (`InMemoryCalendarSource`)
// holds the invented samples of MALACHI_CALENDAR_SAMPLES, or what a test
// puts in.
//
// The protocol is named `CalendarDataSource`, not `CalendarSource`: the
// API mirror has `CalendarSource` already (where an account's calendars
// come from, `graph` / `caldav`).
//
// Notifications reach a source through `handle(_:)`: the application's
// notification hub decodes `CalendarNotification(raw)` (API/CalendarAPI.
// swift) next to `DaemonNotification` and hands it to every calendar
// source and to the `CalendarAlertsController`. `DaemonNotification` is not
// extended, so the switch over it in `MailboxController` and every test of
// it stay as they are.
//
// Swift-first: macOS is the reference client of the calendar (§5).

import Foundation

extension Cal {
    /// How far a source's data is.
    public enum Phase: Sendable, Equatable {
        /// Nothing has arrived yet.
        case loading
        /// The snapshot holds the daemon's (or the samples') data.
        case ready
        /// No connection; the last snapshot stays.
        case unavailable
        /// A load failed; the last snapshot stays.
        case failed
        /// A daemon without the calendar (methodNotFound).
        case unsupported
    }

    /// What the calendar shows outside a range: the accounts and their
    /// calendars, the unanswered invitations and the preferences.
    public struct Snapshot: Sendable, Equatable {
        public var phase: Phase = .loading
        /// account.list: every account of the daemon (names, kinds, the
        /// calendar settings of `config.calendar`).
        public var accounts: [Account] = []
        /// calendar.list: the accounts with the calendar capability, in
        /// the accounts' order, with their calendars and states.
        public var calendarAccounts: [CalendarAccount] = []
        /// calendar.list `unanswered`.
        public var unanswered = 0
        /// invitation.list with state unanswered.
        public var invitations: [InvitationListItem] = []
        public var preferences = Cal.defaultPreferences

        public init(
            phase: Phase = .loading, accounts: [Account] = [], calendarAccounts: [CalendarAccount] = [],
            unanswered: Int = 0, invitations: [InvitationListItem] = [], preferences: CalendarPreferences = Cal.defaultPreferences
        ) {
            self.phase = phase
            self.accounts = accounts
            self.calendarAccounts = calendarAccounts
            self.unanswered = unanswered
            self.invitations = invitations
            self.preferences = preferences
        }

        public func account(_ id: AccountID) -> Account? { accounts.first { $0.id == id } }

        public func calendarAccount(_ id: AccountID) -> CalendarAccount? {
            calendarAccounts.first { $0.accountId == id }
        }

        /// The calendar accounts with only their active calendars
        /// (`Cal.activeCalendars`): what every place but Preferences →
        /// Calendar shows. `calendarAccounts` and `calendarAccount(_:)` keep
        /// the disabled ones for Preferences.
        public var activeCalendarAccounts: [CalendarAccount] {
            calendarAccounts.map { a in
                var a = a
                a.calendars = Cal.activeCalendars(a.calendars)
                return a
            }
        }

        /// An active calendar (nil for an unknown or disabled one).
        public func calendar(_ id: CalendarID) -> CalendarRecord? {
            for a in calendarAccounts {
                if let c = a.calendars.first(where: { $0.id == id }) {
                    return c.disabled ? nil : c
                }
            }
            return nil
        }

        /// The account's name ("" for an unknown id).
        public func accountName(_ id: AccountID) -> String {
            Cal.cleanLine(account(id)?.config.name)
        }

        /// The calendar's name ("" for an unknown id).
        public func calendarName(_ id: CalendarID) -> String {
            Cal.cleanLine(calendar(id)?.name)
        }

        /// The states of the accounts, for the status line.
        public var states: [CalendarSyncState] { calendarAccounts.map(\.state) }

        /// The visible calendars.
        public var visibleCalendars: Set<CalendarID> {
            Set(activeCalendarAccounts.flatMap { $0.calendars.filter(\.visible).map(\.id) })
        }

        /// The calendars a new event can go to (`Prefs.writable`), each
        /// account's default first, accounts in their order.
        public var writableCalendars: [CalendarRecord] {
            activeCalendarAccounts.flatMap { a in
                let w = a.calendars.filter { Cal.Prefs.writable($0) }
                return w.filter(\.isDefault) + w.filter { !$0.isDefault }
            }
        }

        /// The user's own address in an account ("" unknown).
        public func myAddress(_ id: AccountID) -> String {
            Cal.cleanLine(account(id)?.config.email)
        }

        /// The user's name in an account, else the address: `me` of
        /// `Cal.mailTexts`.
        public func myName(_ id: AccountID) -> String {
            let n = Cal.cleanLine(account(id)?.config.displayName)
            return n.isEmpty ? myAddress(id) : n
        }
    }

    /// What changed in a source, for its observers.
    public enum SourceChange: Sendable, Equatable {
        /// The snapshot (accounts, calendars, states, invitations,
        /// preferences).
        case snapshot
        /// The occurrences of ranges overlapping [from, to) were dropped
        /// (both nil: every range); a controller showing one asks again.
        case occurrences(from: Date?, to: Date?)
        /// A cached event was replaced or dropped (a write's answer, a
        /// conflict's current version).
        case event(EventID)
        /// `notify.eventAlert`.
        case alert(EventAlert)
        /// A fire-and-forget write (a calendar's visibility) was refused
        /// and undone; a short sentence for a toast.
        case error(String)
    }

    /// The error of a source that cannot write (the samples).
    public enum SourceError: Error, Sendable, Equatable {
        /// "The preview cannot do that."
        case preview
    }

    /// The sentence for a failed calendar call: the calendar's own by code
    /// (`Text.errorText`), the preview's, else the generic one with `what`
    /// (already translated; the mode's name when nil).
    public static func errorMessage(_ error: any Error, what: String? = nil) -> String {
        if let e = error as? SourceError, e == .preview {
            return Text.previewOnly
        }
        if let e = error as? RPCError, let t = Text.errorText(e.code) {
            return t
        }
        return rpcErrorText(what ?? Text.modeName, error)
    }

    /// The key of a range in the occurrence caches.
    static func rangeKey(_ r: Range) -> String {
        "\(r.from.timeIntervalSince1970)|\(r.to.timeIntervalSince1970)|\(r.zone.identifier)"
    }

    /// The key of an event in the event caches.
    static func eventKey(_ id: EventID, _ recurrenceId: DateOrTime?) -> String {
        id.rawValue + "|" + (recurrenceId?.wire ?? "")
    }

    /// [a, b) and [from, to) overlap; a nil bound is open.
    static func rangesOverlap(_ a: Date, _ b: Date, from: Date?, to: Date?) -> Bool {
        (to.map { a < $0 } ?? true) && (from.map { b > $0 } ?? true)
    }
}

/// Removes a handler installed by `CalendarDataSource.observe`; dropping
/// the token does not.
public typealias CalendarObserverToken = BoardObserverToken

/// The calendar's data and its writes. Reads and writes are async and
/// throw the daemon's `RPCError` (or `Cal.SourceError.preview`); a write's
/// answer also lands in the caches. Every observer is called on the main
/// actor, in the order it was added.
@MainActor
public protocol CalendarDataSource: AnyObject {
    var snapshot: Cal.Snapshot { get }
    /// Calls `f` after every change; the token removes it.
    func observe(_ f: @escaping @MainActor (Cal.SourceChange) -> Void) -> CalendarObserverToken
    /// Asks for the snapshot anew.
    func refresh()
    /// A calendar notification of the daemon (§4.7).
    func handle(_ n: CalendarNotification)

    // MARK: Reads

    /// event.list of `range` (in its zone, with the unanswered invitations),
    /// cached per range until a notification drops it.
    func occurrences(_ range: Cal.Range) async throws -> EventListResult
    /// The cached answer for `range`, if any.
    func cachedOccurrences(_ range: Cal.Range) -> EventListResult?
    /// event.get, cached by id and recurrence id until a write or a
    /// notification replaces it; `reload` asks the daemon anyway.
    func event(_ id: EventID, recurrenceId: DateOrTime?, reload: Bool) async throws -> Event
    func search(_ params: EventSearchParams) async throws -> EventSearchResult
    func freeBusy(_ params: CalendarFreeBusyParams) async throws -> CalendarFreeBusyResult
    func findFreeTime(_ params: CalendarFindFreeTimeParams) async throws -> CalendarFindFreeTimeResult

    // MARK: Writes

    /// calendar.update `visible`: shown in the snapshot at once, undone
    /// with `.error` when the daemon refuses.
    func setVisible(_ visible: Bool, calendar: CalendarID)
    func sync(_ params: CalendarSyncParams) async throws
    func setPreferences(_ update: CalendarPreferencesUpdate) async throws -> CalendarPreferences
    func createEvent(_ params: EventCreateParams) async throws -> EventCreateResult
    /// A `conflict` stores the current version from the error's data
    /// (`.event`) before it is rethrown.
    func updateEvent(_ params: EventUpdateParams) async throws -> EventUpdateResult
    func deleteEvent(_ params: EventDeleteParams) async throws -> EventDeleteResult
    func respond(_ params: EventRespondParams) async throws -> EventRespondResult
    func snoozeAlert(_ params: EventSnoozeAlertParams) async throws
    func dismissAlert(_ params: EventDismissAlertParams) async throws
    func respondToInvitation(_ params: InvitationRespondParams) async throws -> InvitationRespondResult
    func applyCancel(_ params: InvitationApplyCancelParams) async throws -> InvitationApplyCancelResult
    func discoverCalDAV(_ params: AccountDiscoverCalDAVParams) async throws -> AccountDiscoverCalDAVResult
    func listCalDAVCalendars(_ params: AccountListCalDAVCalendarsParams) async throws -> AccountListCalDAVCalendarsResult
    func addAccount(_ params: AccountAddParams) async throws -> AccountAddResult
    func updateAccount(_ params: AccountUpdateParams) async throws
    /// attachment.import for the event editor.
    func importAttachment(_ params: AttachmentImportParams) async throws -> AttachmentImportResult
}

extension CalendarDataSource {
    public func event(_ id: EventID, recurrenceId: DateOrTime?) async throws -> Event {
        try await event(id, recurrenceId: recurrenceId, reload: false)
    }
}
