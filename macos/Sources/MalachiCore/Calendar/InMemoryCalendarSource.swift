// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// A calendar source over data held in memory: the sample calendar of
// MALACHI_CALENDAR_SAMPLES (`InMemoryCalendarSource.samples()`) and the
// controllers' tests. Reads answer from the data (event.list by overlap
// with the visible calendars and the unanswered invitations, event.get by
// id, a plain substring search, the user's free/busy from the
// occurrences); writes answer `Cal.SourceError.preview`, unless a test
// turns `emulatesWrites` on: then each write is recorded in `writes`,
// fails with the error a test put in `failures` (by method name), or
// changes the data the simplest way (an answer sets the response, a
// delete removes the occurrence, a create adds one). The calendars'
// visibility always works: it only changes what is shown.

import Foundation

@MainActor
public final class InMemoryCalendarSource: CalendarDataSource {
    /// A write as the source received it.
    public enum Write: Sendable, Equatable {
        case visible(CalendarID, Bool)
        case sync(CalendarSyncParams)
        case setPreferences(CalendarPreferencesUpdate)
        case create(EventCreateParams)
        case update(EventUpdateParams)
        case delete(EventDeleteParams)
        case respond(EventRespondParams)
        case snooze(EventSnoozeAlertParams)
        case dismiss(EventDismissAlertParams)
        case invitationRespond(InvitationRespondParams)
        case applyCancel(InvitationApplyCancelParams)
        case discover(AccountDiscoverCalDAVParams)
        case listCalDAV(AccountListCalDAVCalendarsParams)
        case addAccount(AccountAddParams)
        case updateAccount(AccountUpdateParams)
        case importAttachment(AttachmentImportParams)
    }

    public private(set) var snapshot: Cal.Snapshot
    public private(set) var data: Cal.SampleData

    /// Writes change the data instead of answering the preview's error.
    public var emulatesWrites = false
    /// The writes received, in order (with `emulatesWrites`).
    public private(set) var writes: [Write] = []
    /// The reads received: the method names, in order.
    public private(set) var reads: [String] = []
    /// The error a method answers (by its name, `API.EventRespond.name`…).
    public var failures: [String: any Error] = [:]
    /// What the CalDAV calls, free/busy, findFreeTime and account.add answer
    /// (with `emulatesWrites`).
    public var discoverResult = AccountDiscoverCalDAVResult(serverUrl: "", foundVia: .url, needsCredentials: true)
    public var listResult = AccountListCalDAVCalendarsResult(principalUrl: "", homeSetUrl: "", scheduling: .server)
    public var addedAccount: AccountID = "acc_added"
    public var freeBusyResult: CalendarFreeBusyResult?
    public var findFreeTimeResult = CalendarFindFreeTimeResult()
    /// The invitation card invitation.respond answers (the asked card with
    /// the answer when nil).
    public var respondedCard: InvitationCard?
    /// Holds every async call until `release()` (tests of "busy").
    public var holding = false

    /// The technical English of the errors the emulated writes answer
    /// (`RPCError.message` is never shown).
    private static let noEvent = "no event"
    private static let staleEtag = "etag"

    private let observers = CalendarObservers()
    private var held: [CheckedContinuation<Void, Never>] = []
    private var nextEvent = 1

    public init(_ data: Cal.SampleData) {
        self.data = data
        snapshot = Cal.sampleSnapshot(data)
    }

    /// An empty calendar.
    public convenience init() {
        self.init(Cal.SampleData())
    }

    public func observe(_ f: @escaping @MainActor (Cal.SourceChange) -> Void) -> CalendarObserverToken {
        observers.add(f)
    }

    /// Replaces the data, as a source does when its data arrives.
    public func replace(_ data: Cal.SampleData, phase: Cal.Phase = .ready) {
        self.data = data
        var s = Cal.sampleSnapshot(data)
        s.phase = phase
        // The visibility the user chose stays.
        for ai in s.calendarAccounts.indices {
            for ci in s.calendarAccounts[ai].calendars.indices {
                let id = s.calendarAccounts[ai].calendars[ci].id
                if let v = snapshot.calendar(id)?.visible {
                    s.calendarAccounts[ai].calendars[ci].visible = v
                }
            }
        }
        snapshot = s
        observers.notify(.snapshot)
        observers.notify(.occurrences(from: nil, to: nil))
    }

    /// Changes the snapshot (a state, the count) and tells the observers.
    public func update(_ change: (inout Cal.Snapshot) -> Void) {
        var s = snapshot
        change(&s)
        guard s != snapshot else { return }
        snapshot = s
        observers.notify(.snapshot)
    }

    /// Lets the held calls go on.
    public func release() {
        holding = false
        let h = held
        held = []
        for c in h {
            c.resume()
        }
    }

    /// The calls waiting in `holding`.
    public var heldCount: Int { held.count }

    private func wait() async {
        guard holding else { return }
        await withCheckedContinuation { held.append($0) }
    }

    public func refresh() {}

    public func handle(_ n: CalendarNotification) {
        switch n {
        case .changed(let c):
            observers.notify(.occurrences(from: c.from, to: c.to))
        case .state(let s):
            update { snap in
                if let i = snap.calendarAccounts.firstIndex(where: { $0.accountId == s.accountId }) {
                    snap.calendarAccounts[i].state = s
                }
            }
        case .alert(let a):
            observers.notify(.alert(a))
        case .invitationsChanged:
            break
        }
    }

    // MARK: Reads

    private func read(_ method: String) async throws {
        reads.append(method)
        await wait()
        if let f = failures[method] {
            throw f
        }
    }

    public func cachedOccurrences(_ range: Cal.Range) -> EventListResult? { nil }

    public func occurrences(_ range: Cal.Range) async throws -> EventListResult {
        try await read(API.EventList.name)
        let visible = snapshot.visibleCalendars
        let out = data.occurrences.filter { o in
            (o.calendarId.map(visible.contains) ?? true) && Cal.overlaps(o, from: range.from, to: range.to, range.zone)
        }
        return EventListResult(occurrences: out, truncated: false, complete: true)
    }

    public func event(_ id: EventID, recurrenceId: DateOrTime?, reload: Bool) async throws -> Event {
        try await read(API.EventGet.name)
        if let e = data.events.first(where: { $0.eventId == id && (recurrenceId == nil || $0.recurrenceId == recurrenceId) }) {
            return e
        }
        throw RPCError(code: .eventNotFound, message: Self.noEvent)
    }

    public func search(_ params: EventSearchParams) async throws -> EventSearchResult {
        try await read(API.EventSearch.name)
        let q = params.query.lowercased()
        var seen = Set<String>()
        var out: [Occurrence] = []
        for o in data.occurrences where o.title.lowercased().contains(q) || (o.location ?? "").lowercased().contains(q) {
            let k = o.eventId?.rawValue ?? Cal.occurrenceKey(o)
            if seen.insert(k).inserted {
                out.append(o)
            }
        }
        return EventSearchResult(occurrences: out, total: out.count)
    }

    public func freeBusy(_ params: CalendarFreeBusyParams) async throws -> CalendarFreeBusyResult {
        try await read(API.CalendarFreeBusy.name)
        if let r = freeBusyResult {
            return r
        }
        let busy = data.occurrences.compactMap { o -> BusyInterval? in
            guard !o.allDay, o.showAs != .free, o.myResponse != .declined, let s = o.start, let e = o.end,
                  s < params.to, e > params.from else { return nil }
            return BusyInterval(start: s, end: e, showAs: o.showAs)
        }
        return CalendarFreeBusyResult(
            me: Availability(address: snapshot.myAddress(params.accountId), known: true, busy: busy),
            attendees: params.attendees.map { Availability(address: $0, known: false) })
    }

    public func findFreeTime(_ params: CalendarFindFreeTimeParams) async throws -> CalendarFindFreeTimeResult {
        try await read(API.CalendarFindFreeTime.name)
        return findFreeTimeResult
    }

    // MARK: Writes

    /// Records the write and answers the preview's error, a test's failure
    /// or nothing (go on).
    private func write(_ w: Write, _ method: String) async throws {
        guard emulatesWrites else {
            throw Cal.SourceError.preview
        }
        writes.append(w)
        await wait()
        if let f = failures[method] {
            throw f
        }
    }

    public func setVisible(_ visible: Bool, calendar id: CalendarID) {
        guard let c = snapshot.calendar(id), c.visible != visible else { return }
        if emulatesWrites {
            writes.append(.visible(id, visible))
        }
        if let f = failures[API.CalendarUpdate.name] {
            observers.notify(.error(Cal.errorMessage(f)))
            return
        }
        update { s in
            for ai in s.calendarAccounts.indices {
                if let ci = s.calendarAccounts[ai].calendars.firstIndex(where: { $0.id == id }) {
                    s.calendarAccounts[ai].calendars[ci].visible = visible
                }
            }
        }
        observers.notify(.occurrences(from: nil, to: nil))
    }

    public func sync(_ params: CalendarSyncParams) async throws {
        try await write(.sync(params), API.CalendarSync.name)
    }

    public func setPreferences(_ u: CalendarPreferencesUpdate) async throws -> CalendarPreferences {
        try await write(.setPreferences(u), API.CalendarSetPreferences.name)
        var p = data.preferences
        if let v = u.weekStart { p.weekStart = v }
        if let v = u.defaultDurationMinutes { p.defaultDurationMinutes = v }
        if let v = u.defaultAlertMinutes { p.defaultAlertMinutes = v }
        if let v = u.showDeclined { p.showDeclined = v }
        if let v = u.workingHours { p.workingHours = v }
        data.preferences = p
        update { $0.preferences = p }
        return p
    }

    public func createEvent(_ params: EventCreateParams) async throws -> EventCreateResult {
        try await write(.create(params), API.EventCreate.name)
        let id = EventID("ev_created_\(nextEvent)")
        nextEvent += 1
        let accountId = snapshot.calendar(params.calendarId)?.accountId ?? AccountID("")
        let o = Occurrence(
            eventId: id, calendarId: params.calendarId, accountId: accountId, title: params.event.title,
            startDate: params.event.startDate, endDate: params.event.endDate, allDay: params.event.allDay,
            location: params.event.location, status: .confirmed, showAs: .busy,
            attendeeCount: params.event.attendees?.count ?? 0)
        let e = Event(
            occurrence: o, etag: "\"1\"", notes: params.event.notes, attendeesTruncated: false, responseRequested: false,
            canEdit: true, canDelete: true, canRespond: false, canProposeNewTime: false, removeIsDelete: true)
        data.events.append(e)
        data.occurrences.append(o)
        observers.notify(.occurrences(from: nil, to: nil))
        let n = params.event.attendees?.count ?? 0
        return EventCreateResult(event: e, invitations: InvitationsSent(via: n > 0 ? .server : .none, count: n))
    }

    public func updateEvent(_ params: EventUpdateParams) async throws -> EventUpdateResult {
        try await write(.update(params), API.EventUpdate.name)
        guard let i = data.events.firstIndex(where: { $0.eventId == params.eventId }) else {
            throw RPCError(code: .eventNotFound, message: Self.noEvent)
        }
        if data.events[i].etag != params.etag {
            throw RPCError(code: .conflict, message: Self.staleEtag)
        }
        data.events[i].title = params.event.title
        data.events[i].location = params.event.location
        data.events[i].notes = params.event.notes
        data.events[i].etag += "+"
        observers.notify(.event(params.eventId))
        return EventUpdateResult(event: data.events[i], droppedExceptions: 0, invitations: InvitationsSent(via: .none, count: 0))
    }

    public func deleteEvent(_ params: EventDeleteParams) async throws -> EventDeleteResult {
        try await write(.delete(params), API.EventDelete.name)
        data.events.removeAll { $0.eventId == params.eventId && (params.scope != .this || $0.recurrenceId == params.recurrenceId) }
        data.occurrences.removeAll {
            $0.eventId == params.eventId && (params.scope != .this || $0.recurrenceId == params.recurrenceId)
        }
        observers.notify(.event(params.eventId))
        observers.notify(.occurrences(from: nil, to: nil))
        return EventDeleteResult(invitations: InvitationsSent(via: .none, count: 0))
    }

    public func respond(_ params: EventRespondParams) async throws -> EventRespondResult {
        try await write(.respond(params), API.EventRespond.name)
        guard let i = data.events.firstIndex(where: {
            $0.eventId == params.eventId && (params.recurrenceId == nil || $0.recurrenceId == params.recurrenceId)
        }) else {
            throw RPCError(code: .eventNotFound, message: Self.noEvent)
        }
        data.events[i].myResponse = params.response
        data.events[i].etag += "+"
        for j in data.occurrences.indices where data.occurrences[j].eventId == params.eventId {
            data.occurrences[j].myResponse = params.response
        }
        observers.notify(.event(params.eventId))
        return EventRespondResult(event: data.events[i], via: params.sendResponse == false ? .none : .server)
    }

    public func snoozeAlert(_ params: EventSnoozeAlertParams) async throws {
        try await write(.snooze(params), API.EventSnoozeAlert.name)
    }

    public func dismissAlert(_ params: EventDismissAlertParams) async throws {
        try await write(.dismiss(params), API.EventDismissAlert.name)
    }

    public func respondToInvitation(_ params: InvitationRespondParams) async throws -> InvitationRespondResult {
        try await write(.invitationRespond(params), API.InvitationRespond.name)
        var card = respondedCard ?? data.cards.first ?? InvitationCard(
            summary: InvitationSummary(method: .request, kind: .invitation, state: .unanswered, title: ""),
            partId: "", uid: "", sequence: 0, organizerMismatch: false, attendeeCount: 0, conflicts: [],
            day: InvitationDay(from: Date(), to: Date()), targets: [], fixedTarget: false, canRespond: true,
            canProposeNewTime: false, replyVia: .none, replyApplied: false)
        if respondedCard == nil {
            card.state = .answered
            card.response = params.response
            card.calendarId = params.calendarId
        }
        return InvitationRespondResult(invitation: card, eventId: card.eventId)
    }

    public func applyCancel(_ params: InvitationApplyCancelParams) async throws -> InvitationApplyCancelResult {
        try await write(.applyCancel(params), API.InvitationApplyCancel.name)
        return InvitationApplyCancelResult(removed: true)
    }

    public func discoverCalDAV(_ params: AccountDiscoverCalDAVParams) async throws -> AccountDiscoverCalDAVResult {
        try await write(.discover(params), API.AccountDiscoverCalDAV.name)
        return discoverResult
    }

    public func listCalDAVCalendars(_ params: AccountListCalDAVCalendarsParams) async throws -> AccountListCalDAVCalendarsResult {
        try await write(.listCalDAV(params), API.AccountListCalDAVCalendars.name)
        return listResult
    }

    public func addAccount(_ params: AccountAddParams) async throws -> AccountAddResult {
        try await write(.addAccount(params), API.AccountAdd.name)
        return AccountAddResult(accountId: addedAccount)
    }

    public func updateAccount(_ params: AccountUpdateParams) async throws {
        try await write(.updateAccount(params), API.AccountUpdate.name)
        if let i = data.accounts.firstIndex(where: { $0.id == params.accountId }) {
            data.accounts[i].config = params.config
            update { s in
                if let j = s.accounts.firstIndex(where: { $0.id == params.accountId }) {
                    s.accounts[j].config = params.config
                }
            }
        }
    }

    public func importAttachment(_ params: AttachmentImportParams) async throws -> AttachmentImportResult {
        try await write(.importAttachment(params), API.AttachmentImport.name)
        let name = params.filename ?? (params.path.map { ($0 as NSString).lastPathComponent } ?? "file")
        return AttachmentImportResult(
            attachment: DraftAttachment(id: "att_\(writes.count)", filename: name, contentType: "application/octet-stream", size: 1, inline: false))
    }
}
