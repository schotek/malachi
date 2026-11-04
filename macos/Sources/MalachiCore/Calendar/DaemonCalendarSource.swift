// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The calendar from the daemon (docs/calendar.md §4.3–4.7): account.list,
// calendar.list, calendar.preferences and invitation.list for the
// snapshot; event.list per range and event.get per event, both cached;
// every write passed through with its answer stored.
//
// It subscribes to nothing itself: the application's notification hub
// calls `handle(_:)` with each decoded `CalendarNotification`,
// `accountsChanged()` on notify.accountsChanged and
// `connectionChanged(connected:)` when the connection comes and goes;
// `start()` loads the first time.
//
// - notify.calendarChanged drops every cached range overlapping its
//   `from`/`to` (absent: every range) and the cached events of that time,
//   tells the observers (`.occurrences`), and lists the calendars again
//   (and, without calendars named, the preferences: §4.4 says so for
//   calendar.setPreferences).
// - notify.calendarState replaces the account's state in the snapshot.
// - notify.eventAlert goes to the observers as it is.
// - notify.invitationsChanged lists the calendars (the count) and the
//   unanswered invitations again.
//
// One snapshot load is on its way at a time: asked for again meanwhile it
// runs once more after the answer. A range asked for twice while its
// event.list is on its way waits for that one call. A range dropped while
// its call is on its way is not stored from that answer.
//
// calendar.update `visible` is optimistic: the snapshot shows the change
// at once (an overlay a calendar.list arriving meanwhile does not undo),
// the call follows, a refusal takes it back and says why (`.error`).
// A write that answers `conflict` stores the event of its data and tells
// the observers (`.event`) before the error is rethrown.

import Foundation
import os

@MainActor
public final class DaemonCalendarSource: CalendarDataSource {
    public private(set) var snapshot = Cal.Snapshot()

    private let client: RPCClient
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "calendar")
    private let observers = CalendarObservers()

    /// The daemon's data as last answered, without the overlays.
    private var base = Cal.Snapshot()
    /// calendar.update `visible` under way, by calendar.
    private var pendingVisible: [CalendarID: (token: Int, visible: Bool)] = [:]
    private var nextToken = 0

    /// event.list answers by range key, with the range.
    private var ranges: [String: (range: Cal.Range, result: EventListResult)] = [:]
    /// event.list calls on their way, by range key, with their generation.
    private var rangeTasks: [String: (generation: Int, task: Task<EventListResult, any Error>)] = [:]
    private var rangeGeneration = 0
    /// event.get answers by event key.
    private var events: [String: Event] = [:]

    private var started = false
    private var loadInFlight = false
    private var loadAgain = false
    /// What the next load asks for besides the calendars.
    private var wantAccounts = true
    private var wantPreferences = true
    private var wantInvitations = true
    private var connectionGeneration = 0
    private var writesInFlight = 0

    public init(client: RPCClient) {
        self.client = client
    }

    /// Nothing is on its way (a load, an event.list, a visibility write):
    /// what the tests wait for.
    var isIdle: Bool { !loadInFlight && rangeTasks.isEmpty && writesInFlight == 0 }

    public func observe(_ f: @escaping @MainActor (Cal.SourceChange) -> Void) -> CalendarObserverToken {
        observers.add(f)
    }

    // MARK: What the application calls

    /// Loads the snapshot.
    public func start() {
        started = true
        refresh()
    }

    /// Stops: no call is made and no answer is taken any more.
    public func stop() {
        started = false
        connectionGeneration += 1
        for (_, t) in rangeTasks {
            t.task.cancel()
        }
        rangeTasks.removeAll()
    }

    /// notify.accountsChanged (an account.update, e.g. calendars switched
    /// off in Preferences): the accounts and calendars again at once.
    public func accountsChanged() {
        guard started else { return }
        refresh()
    }

    /// The connection came (everything again, the caches dropped) or went
    /// (the snapshot stays, in phase `unavailable`).
    public func connectionChanged(connected: Bool) {
        guard started else { return }
        connectionGeneration += 1
        loadInFlight = false
        loadAgain = false
        if connected {
            dropRanges(from: nil, to: nil)
            refresh()
        } else {
            base.phase = .unavailable
            publish()
        }
    }

    public func refresh() {
        guard started else { return }
        wantAccounts = true
        wantPreferences = true
        wantInvitations = true
        load()
    }

    public func handle(_ n: CalendarNotification) {
        guard started else { return }
        switch n {
        case .changed(let c):
            dropRanges(from: c.from, to: c.to, calendars: Set(c.calendarIds ?? []))
            if (c.calendarIds ?? []).isEmpty {
                wantPreferences = true
            }
            load()
        case .state(let s):
            guard let i = base.calendarAccounts.firstIndex(where: { $0.accountId == s.accountId }) else { return }
            guard base.calendarAccounts[i].state != s else { return }
            base.calendarAccounts[i].state = s
            publish()
        case .alert(let a):
            observers.notify(.alert(a))
        case .invitationsChanged:
            wantInvitations = true
            load()
        }
    }

    // MARK: The snapshot

    private func load() {
        guard started else { return }
        guard !loadInFlight else {
            loadAgain = true
            return
        }
        loadInFlight = true
        let accounts = wantAccounts || base.accounts.isEmpty
        let prefs = wantPreferences
        let invitations = wantInvitations
        wantAccounts = false
        wantPreferences = false
        wantInvitations = false
        let generation = connectionGeneration
        let client = client
        Task { [weak self] in
            var outcome = Loaded()
            do {
                if accounts {
                    outcome.accounts = try await client.call(API.AccountList.self, EmptyParams()).accounts
                }
                outcome.calendars = try await client.call(API.CalendarList.self, CalendarListParams())
                if prefs {
                    outcome.preferences = try await client.call(API.CalendarPreferencesGet.self, CalendarPreferencesParams()).preferences
                }
                if invitations {
                    outcome.invitations = try await client.call(
                        API.InvitationList.self, InvitationListParams(state: .unanswered)).invitations
                }
            } catch {
                outcome.error = error
            }
            guard let self, generation == self.connectionGeneration else { return }
            self.loadInFlight = false
            self.apply(outcome, askedAccounts: accounts, askedPreferences: prefs, askedInvitations: invitations)
            if self.loadAgain {
                self.loadAgain = false
                self.load()
            }
        }
    }

    private struct Loaded {
        var accounts: [Account]?
        var calendars: CalendarListResult?
        var preferences: CalendarPreferences?
        var invitations: [InvitationListItem]?
        var error: (any Error)?
    }

    private func apply(_ o: Loaded, askedAccounts: Bool, askedPreferences: Bool, askedInvitations: Bool) {
        if let a = o.accounts { base.accounts = a }
        if let c = o.calendars {
            base.calendarAccounts = c.accounts
            base.unanswered = c.unanswered
        }
        if let p = o.preferences { base.preferences = p }
        if let i = o.invitations { base.invitations = i }
        if let err = o.error {
            log.info("calendar load: \(String(describing: err), privacy: .public)")
            // What was not asked successfully is asked again next time.
            wantAccounts = wantAccounts || (askedAccounts && o.accounts == nil)
            wantPreferences = wantPreferences || (askedPreferences && o.preferences == nil)
            wantInvitations = wantInvitations || (askedInvitations && o.invitations == nil)
            base.phase = Self.phase(of: err)
        } else {
            base.phase = .ready
        }
        publish()
    }

    /// Why a load failed decides the phase.
    static func phase(of error: any Error) -> Cal.Phase {
        if let e = error as? RPCClient.ClientError {
            switch e {
            case .notConnected, .disconnected: return .unavailable
            default: return .failed
            }
        }
        if let e = error as? RPCError, e.code == .methodNotFound || e.code == .notImplemented {
            return .unsupported
        }
        return .failed
    }

    /// `base` with the overlays; tells the observers when it changed.
    private func publish() {
        var s = base
        for (id, p) in pendingVisible {
            for ai in s.calendarAccounts.indices {
                if let ci = s.calendarAccounts[ai].calendars.firstIndex(where: { $0.id == id }) {
                    s.calendarAccounts[ai].calendars[ci].visible = p.visible
                }
            }
        }
        guard s != snapshot else { return }
        snapshot = s
        observers.notify(.snapshot)
    }

    // MARK: Ranges and events

    /// Drops the cached ranges overlapping [from, to) and, when calendars
    /// are named, every cached range holding an occurrence of one of them
    /// (a calendar switched off, emptied or synchronised again); the
    /// observers then hear of every range when one outside the bounds went.
    private func dropRanges(from: Date?, to: Date?, calendars: Set<CalendarID> = []) {
        rangeGeneration += 1
        var outside = false
        for (k, v) in ranges {
            if Cal.rangesOverlap(v.range.from, v.range.to, from: from, to: to) {
                ranges[k] = nil
            } else if !calendars.isEmpty, v.result.occurrences.contains(where: { $0.calendarId.map(calendars.contains) ?? false }) {
                ranges[k] = nil
                outside = true
            }
        }
        // A call on its way when anything was dropped is not stored from
        // that answer (`rangeGeneration`).
        for (k, e) in events {
            if let c = e.calendarId, calendars.contains(c) {
                events[k] = nil
                continue
            }
            guard from != nil || to != nil, let (s, end) = Cal.span(e.occurrence, .current) else {
                events[k] = nil
                continue
            }
            if Cal.rangesOverlap(s, end, from: from, to: to) {
                events[k] = nil
            }
        }
        observers.notify(outside ? .occurrences(from: nil, to: nil) : .occurrences(from: from, to: to))
    }

    public func cachedOccurrences(_ range: Cal.Range) -> EventListResult? {
        ranges[Cal.rangeKey(range)]?.result
    }

    public func occurrences(_ range: Cal.Range) async throws -> EventListResult {
        let key = Cal.rangeKey(range)
        if let hit = ranges[key] {
            return hit.result
        }
        if let running = rangeTasks[key] {
            return try await running.task.value
        }
        let client = client
        let params = EventListParams(
            from: range.from, to: range.to, timeZone: TimeZoneID(range.zone.identifier), includeInvitations: true)
        let task = Task<EventListResult, any Error> {
            try await client.call(API.EventList.self, params, timeout: .seconds(10))
        }
        let generation = rangeGeneration
        rangeTasks[key] = (generation, task)
        defer {
            if rangeTasks[key]?.task == task {
                rangeTasks[key] = nil
            }
        }
        let result = try await task.value
        if rangeTasks[key]?.task == task, generation == rangeGeneration {
            ranges[key] = (range, result)
        }
        return result
    }

    public func event(_ id: EventID, recurrenceId: DateOrTime?, reload: Bool) async throws -> Event {
        let key = Cal.eventKey(id, recurrenceId)
        if !reload, let e = events[key] {
            return e
        }
        let e = try await client.call(
            API.EventGet.self, EventGetParams(eventId: id, recurrenceId: recurrenceId, timeZone: TimeZoneID(TimeZone.current.identifier))
        ).event
        let changed = events[key]?.etag != e.etag
        events[key] = e
        if changed && reload {
            observers.notify(.event(id))
        }
        return e
    }

    /// Stores a write's (or a conflict's) event and tells the observers.
    private func store(_ e: Event, recurrenceId: DateOrTime?) {
        guard let id = e.eventId else { return }
        events[Cal.eventKey(id, e.recurrenceId ?? recurrenceId)] = e
        observers.notify(.event(id))
    }

    private func conflict(_ error: any Error, recurrenceId: DateOrTime?) {
        if let e = error as? RPCError, let c = e.conflictEvent {
            store(c.event, recurrenceId: recurrenceId)
        }
    }

    public func search(_ params: EventSearchParams) async throws -> EventSearchResult {
        try await client.call(API.EventSearch.self, params)
    }

    public func freeBusy(_ params: CalendarFreeBusyParams) async throws -> CalendarFreeBusyResult {
        try await client.call(API.CalendarFreeBusy.self, params, timeout: .seconds(20))
    }

    public func findFreeTime(_ params: CalendarFindFreeTimeParams) async throws -> CalendarFindFreeTimeResult {
        try await client.call(API.CalendarFindFreeTime.self, params, timeout: .seconds(20))
    }

    // MARK: Writes

    public func setVisible(_ visible: Bool, calendar id: CalendarID) {
        guard snapshot.calendar(id) != nil, snapshot.calendar(id)?.visible != visible else { return }
        nextToken += 1
        let token = nextToken
        pendingVisible[id] = (token, visible)
        publish()
        writesInFlight += 1
        let client = client
        Task { [weak self] in
            var failure: (any Error)?
            var answer: CalendarRecord?
            do {
                answer = try await client.call(API.CalendarUpdate.self, CalendarUpdateParams(calendarId: id, visible: visible)).calendar
            } catch {
                failure = error
            }
            guard let self else { return }
            self.writesInFlight -= 1
            guard self.pendingVisible[id]?.token == token else { return }
            self.pendingVisible[id] = nil
            if let answer {
                for ai in self.base.calendarAccounts.indices {
                    if let ci = self.base.calendarAccounts[ai].calendars.firstIndex(where: { $0.id == id }) {
                        self.base.calendarAccounts[ai].calendars[ci] = answer
                    }
                }
            }
            self.publish()
            if let failure {
                self.log.info("calendar.update: \(String(describing: failure), privacy: .public)")
                self.observers.notify(.error(Cal.errorMessage(failure)))
            }
        }
    }

    public func sync(_ params: CalendarSyncParams) async throws {
        _ = try await client.call(API.CalendarSync.self, params)
    }

    public func setPreferences(_ update: CalendarPreferencesUpdate) async throws -> CalendarPreferences {
        let p = try await client.call(API.CalendarSetPreferences.self, CalendarSetPreferencesParams(preferences: update)).preferences
        base.preferences = p
        publish()
        return p
    }

    public func createEvent(_ params: EventCreateParams) async throws -> EventCreateResult {
        let r = try await client.call(API.EventCreate.self, params)
        store(r.event, recurrenceId: nil)
        return r
    }

    public func updateEvent(_ params: EventUpdateParams) async throws -> EventUpdateResult {
        do {
            let r = try await client.call(API.EventUpdate.self, params)
            store(r.event, recurrenceId: params.recurrenceId)
            return r
        } catch {
            conflict(error, recurrenceId: params.recurrenceId)
            throw error
        }
    }

    public func deleteEvent(_ params: EventDeleteParams) async throws -> EventDeleteResult {
        do {
            let r = try await client.call(API.EventDelete.self, params)
            events[Cal.eventKey(params.eventId, params.recurrenceId)] = nil
            observers.notify(.event(params.eventId))
            return r
        } catch {
            conflict(error, recurrenceId: params.recurrenceId)
            throw error
        }
    }

    public func respond(_ params: EventRespondParams) async throws -> EventRespondResult {
        do {
            let r = try await client.call(API.EventRespond.self, params)
            store(r.event, recurrenceId: params.recurrenceId)
            return r
        } catch {
            conflict(error, recurrenceId: params.recurrenceId)
            throw error
        }
    }

    public func snoozeAlert(_ params: EventSnoozeAlertParams) async throws {
        _ = try await client.call(API.EventSnoozeAlert.self, params)
    }

    public func dismissAlert(_ params: EventDismissAlertParams) async throws {
        _ = try await client.call(API.EventDismissAlert.self, params)
    }

    public func respondToInvitation(_ params: InvitationRespondParams) async throws -> InvitationRespondResult {
        try await client.call(API.InvitationRespond.self, params)
    }

    public func applyCancel(_ params: InvitationApplyCancelParams) async throws -> InvitationApplyCancelResult {
        try await client.call(API.InvitationApplyCancel.self, params)
    }

    public func discoverCalDAV(_ params: AccountDiscoverCalDAVParams) async throws -> AccountDiscoverCalDAVResult {
        try await client.call(API.AccountDiscoverCalDAV.self, params, timeout: .seconds(20))
    }

    public func listCalDAVCalendars(_ params: AccountListCalDAVCalendarsParams) async throws -> AccountListCalDAVCalendarsResult {
        try await client.call(API.AccountListCalDAVCalendars.self, params)
    }

    public func addAccount(_ params: AccountAddParams) async throws -> AccountAddResult {
        let r = try await client.call(API.AccountAdd.self, params, timeout: RPCTimeouts.save)
        refresh()
        return r
    }

    public func updateAccount(_ params: AccountUpdateParams) async throws {
        _ = try await client.call(API.AccountUpdate.self, params, timeout: RPCTimeouts.save)
        refresh()
    }

    public func importAttachment(_ params: AttachmentImportParams) async throws -> AttachmentImportResult {
        try await client.call(API.AttachmentImport.self, params, timeout: RPCTimeouts.part)
    }
}

/// The observers of a calendar source, called in the order they were added.
@MainActor
final class CalendarObservers {
    private var handlers: [Int: @MainActor (Cal.SourceChange) -> Void] = [:]
    private var next = 0

    func add(_ f: @escaping @MainActor (Cal.SourceChange) -> Void) -> CalendarObserverToken {
        let id = next
        next += 1
        handlers[id] = f
        return CalendarObserverToken { [weak self] in
            self?.handlers[id] = nil
        }
    }

    func notify(_ c: Cal.SourceChange) {
        for id in handlers.keys.sorted() {
            handlers[id]?(c)
        }
    }
}
