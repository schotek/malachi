// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The calendar from the daemon (Calendar/DaemonCalendarSource.swift)
// against a fake daemon: the snapshot's calls and params, the range cache
// and its invalidation by notify.calendarChanged ranges, the event cache
// and a conflict's reload, a consentRequired state reaching the sidebar,
// the optimistic visibility and its revert, an old daemon. The fake daemon
// holds calendar.update until the test releases it; "nothing happened" is
// checked once the source is idle.

/// The daemon's side: the samples, the calls with their params.
private actor CalendarScript {
    let data = Cal.sampleData(now: calNow, zone: calZone)
    var calls: [String] = []
    var params: [String: [Data]] = [:]
    var updateFailure: RPCError?
    var listFailure: RPCError?
    var conflictEvent: Event?
    var consent = false
    private var holdUpdates = false
    private var held: [CheckedContinuation<Void, Never>] = []

    func record(_ m: String, _ p: Data) {
        calls.append(m)
        params[m, default: []].append(p)
    }

    func count(_ m: String) -> Int { calls.filter { $0 == m }.count }
    func last<T: Decodable>(_ m: String, _ t: T.Type) throws -> T? {
        guard let d = params[m]?.last else { return nil }
        return try JSONCoding.decoder().decode(t, from: d)
    }

    func set(updateFailure: RPCError?) { self.updateFailure = updateFailure }
    func set(listFailure: RPCError?) { self.listFailure = listFailure }
    func set(conflictEvent: Event?) { self.conflictEvent = conflictEvent }
    func set(consent: Bool) { self.consent = consent }
    func hold(_ on: Bool) {
        holdUpdates = on
        if !on {
            let h = held
            held = []
            for c in h { c.resume() }
        }
    }
    var heldCount: Int { held.count }

    func accounts(_ p: Data) throws -> Data {
        record(API.AccountList.name, p)
        return try JSONCoding.encoder().encode(AccountListResult(accounts: data.accounts))
    }

    func calendars(_ p: Data) throws -> Data {
        record(API.CalendarList.name, p)
        if let listFailure { throw listFailure }
        var r = data.calendars
        if consent {
            r.accounts[0].state.status = .consentRequired
        }
        return try JSONCoding.encoder().encode(r)
    }

    func preferences(_ p: Data) throws -> Data {
        record(API.CalendarPreferencesGet.name, p)
        return try JSONCoding.encoder().encode(CalendarPreferencesResult(preferences: data.preferences))
    }

    func invitations(_ p: Data) throws -> Data {
        record(API.InvitationList.name, p)
        return try JSONCoding.encoder().encode(InvitationListResult(invitations: data.invitations))
    }

    func events(_ p: Data) throws -> Data {
        record(API.EventList.name, p)
        let q = try JSONCoding.decoder().decode(EventListParams.self, from: p)
        let out = data.occurrences.filter { Cal.overlaps($0, from: q.from, to: q.to, calZone) }
        return try JSONCoding.encoder().encode(EventListResult(occurrences: out, truncated: false, complete: true))
    }

    func event(_ p: Data) throws -> Data {
        record(API.EventGet.name, p)
        let q = try JSONCoding.decoder().decode(EventGetParams.self, from: p)
        guard let e = data.events.first(where: { $0.eventId == q.eventId }) else {
            throw RPCError(code: .eventNotFound, message: "no")
        }
        return try JSONCoding.encoder().encode(EventGetResult(event: e))
    }

    func update(_ p: Data) async throws -> Data {
        record(API.CalendarUpdate.name, p)
        if holdUpdates {
            await withCheckedContinuation { held.append($0) }
        }
        if let updateFailure { throw updateFailure }
        let q = try JSONCoding.decoder().decode(CalendarUpdateParams.self, from: p)
        var c = data.calendars.accounts.flatMap(\.calendars).first { $0.id == q.calendarId }!
        c.visible = q.visible ?? c.visible
        return try JSONCoding.encoder().encode(CalendarUpdateResult(calendar: c))
    }

    func eventUpdate(_ p: Data) throws -> Data {
        record(API.EventUpdate.name, p)
        if let e = conflictEvent {
            let raw = try JSONCoding.encoder().encode(ConflictEventData(event: e))
            throw RPCError(code: .conflict, message: "etag", data: try JSONCoding.decoder().decode(JSONValue.self, from: raw))
        }
        throw RPCError(code: .readOnly, message: "no")
    }
}

@MainActor
private final class Harness {
    let fake: FakeDaemon
    let script = CalendarScript()
    let client: RPCClient
    let source: DaemonCalendarSource
    var changes: [Cal.SourceChange] = []
    var token: CalendarObserverToken?

    init() async throws {
        fake = try FakeDaemon()
        let s = script
        await fake.on(API.AccountList.name) { p in try await s.accounts(p) }
        await fake.on(API.CalendarList.name) { p in try await s.calendars(p) }
        await fake.on(API.CalendarPreferencesGet.name) { p in try await s.preferences(p) }
        await fake.on(API.InvitationList.name) { p in try await s.invitations(p) }
        await fake.on(API.EventList.name) { p in try await s.events(p) }
        await fake.on(API.EventGet.name) { p in try await s.event(p) }
        await fake.on(API.CalendarUpdate.name) { p in try await s.update(p) }
        await fake.on(API.EventUpdate.name) { p in try await s.eventUpdate(p) }
        try await fake.start()
        client = RPCClient(socketPath: fake.path)
        try await client.connect()
        source = DaemonCalendarSource(client: client)
        token = source.observe { [weak self] in self?.changes.append($0) }
    }

    func started() async throws -> Harness {
        source.start()
        try await idle()
        return self
    }

    func idle() async throws {
        try await Task.sleep(for: .milliseconds(2))
        try await calWaitUntil { self.source.isIdle }
    }

    func stop() async {
        source.stop()
        await script.hold(false)
        await client.close()
        await fake.stop()
    }
}

@MainActor
private func week() -> Cal.Range {
    Cal.viewRange(.week, anchor: calNow, weekStart: .monday, zone: calZone)
}

@MainActor
@Suite(.serialized) struct DaemonCalendarSourceTests {
    @Test func startLoadsTheSnapshot() async throws {
        let h = try await Harness().started()
        defer { Task { await h.stop() } }
        #expect(h.source.snapshot.phase == .ready)
        #expect(h.source.snapshot.accounts.count == 3 && h.source.snapshot.calendarAccounts.count == 3)
        #expect(h.source.snapshot.unanswered == 3 && h.source.snapshot.invitations.count == 3)
        #expect(await h.script.calls == [
            API.AccountList.name, API.CalendarList.name, API.CalendarPreferencesGet.name, API.InvitationList.name,
        ])
        #expect(try await h.script.last(API.InvitationList.name, InvitationListParams.self)?.state == .unanswered)
        #expect(h.changes.contains(.snapshot))
    }

    @Test func rangesAreCachedAndDroppedByOverlap() async throws {
        let h = try await Harness().started()
        defer { Task { await h.stop() } }
        let r = week()
        async let a = h.source.occurrences(r)
        async let b = h.source.occurrences(r)
        let (x, y) = try await (a, b)
        #expect(x == y && !x.occurrences.isEmpty)
        #expect(await h.script.count(API.EventList.name) == 1)
        let p = try #require(try await h.script.last(API.EventList.name, EventListParams.self))
        #expect(p.from == r.from && p.to == r.to && p.timeZone == "Europe/Prague" && p.includeInvitations == true)
        #expect(h.source.cachedOccurrences(r) == x)
        _ = try await h.source.occurrences(r)
        #expect(await h.script.count(API.EventList.name) == 1)

        // A change next year leaves this week's cache.
        let lists = await h.script.count(API.CalendarList.name)
        h.source.handle(.changed(CalendarChangedNotification(
            calendarIds: [Cal.sampleHolidays], from: Cal.date(2027, 3, 1, 0, 0, in: calZone), to: Cal.date(2027, 3, 2, 0, 0, in: calZone))))
        try await h.idle()
        #expect(h.source.cachedOccurrences(r) != nil)
        #expect(h.changes.last == .occurrences(from: Cal.date(2027, 3, 1, 0, 0, in: calZone), to: Cal.date(2027, 3, 2, 0, 0, in: calZone)))
        // The calendars are listed again, the preferences not (calendars named).
        #expect(await h.script.count(API.CalendarList.name) == lists + 1)
        #expect(await h.script.count(API.CalendarPreferencesGet.name) == 1)
        // A change on Thursday drops it.
        h.source.handle(.changed(CalendarChangedNotification(
            from: Cal.date(2026, 10, 15, 0, 0, in: calZone), to: Cal.date(2026, 10, 15, 12, 0, in: calZone))))
        try await h.idle()
        #expect(h.source.cachedOccurrences(r) == nil)
        #expect(await h.script.count(API.CalendarPreferencesGet.name) == 2)
        _ = try await h.source.occurrences(r)
        #expect(await h.script.count(API.EventList.name) == 2)
        // Without a range: everything.
        h.source.handle(.changed(CalendarChangedNotification()))
        try await h.idle()
        #expect(h.source.cachedOccurrences(r) == nil)
    }

    @Test func eventsAreCachedAndAConflictReplacesThem() async throws {
        let h = try await Harness().started()
        defer { Task { await h.stop() } }
        let e = try await h.source.event("ev_sample_offsite", recurrenceId: nil)
        _ = try await h.source.event("ev_sample_offsite", recurrenceId: nil)
        #expect(await h.script.count(API.EventGet.name) == 1)
        var newer = e
        newer.etag = "\"7\""
        newer.title = "Offsite (changed elsewhere)"
        await h.script.set(conflictEvent: newer)
        await #expect(throws: RPCError.self) {
            _ = try await h.source.updateEvent(EventUpdateParams(
                eventId: "ev_sample_offsite", scope: .all, etag: e.etag, event: EventInput(title: "x", allDay: true)))
        }
        #expect(h.changes.last == .event("ev_sample_offsite"))
        let cached = try await h.source.event("ev_sample_offsite", recurrenceId: nil)
        #expect(cached.etag == "\"7\"" && cached.title == "Offsite (changed elsewhere)")
        #expect(await h.script.count(API.EventGet.name) == 1)
        // A detail over the daemon's source shows the reload inline.
        let d = EventDetailController(source: h.source, eventId: "ev_sample_offsite", recurrenceId: nil, env: calEnv)
        d.start()
        try await calWaitUntil { d.event != nil }
        #expect(d.event?.etag == "\"7\"")
        d.stop()
    }

    @Test func consentRequiredReachesTheSidebar() async throws {
        let h = try await Harness().started()
        defer { Task { await h.stop() } }
        let c = CalendarController(source: h.source, env: calEnv, now: { calNow }, localeFirstDay: .monday)
        c.start()
        try await calWaitUntil { c.isIdle }
        #expect(!c.sidebar.accounts[0].offersSignIn)
        h.source.handle(.state(CalendarSyncState(accountId: Cal.sampleWork, status: .consentRequired, progress: -1)))
        #expect(h.source.snapshot.calendarAccount(Cal.sampleWork)?.state.status == .consentRequired)
        #expect(c.sidebar.accounts[0].stateLine == Cal.Text.consentLine && c.sidebar.accounts[0].offersSignIn)
        #expect(c.statusLine == Cal.Text.consentLine)
        // An alert goes to the observers as it is.
        let a = EventAlert(
            alertId: "al_9", state: .due, eventId: "ev_sample_planning", accountId: Cal.sampleWork,
            calendarId: Cal.sampleWorkCalendar, title: "Q4 planning", allDay: false, minutesBefore: 10)
        h.source.handle(.alert(a))
        #expect(h.changes.last == .alert(a))
        c.stop()
    }

    @Test func visibilityIsOptimisticAndRevertedOnRefusal() async throws {
        let h = try await Harness().started()
        defer { Task { await h.stop() } }
        await h.script.hold(true)
        await h.script.set(updateFailure: RPCError(code: .calendarNotFound, message: "gone"))
        h.source.setVisible(false, calendar: Cal.sampleSport)
        #expect(h.source.snapshot.calendar(Cal.sampleSport)?.visible == false)
        try await calWaitUntil { await h.script.heldCount == 1 }
        // A calendar.list meanwhile does not undo it.
        h.source.refresh()
        try await calWaitUntil { await h.script.count(API.CalendarList.name) == 2 }
        try await Task.sleep(for: .milliseconds(20))
        #expect(h.source.snapshot.calendar(Cal.sampleSport)?.visible == false)
        await h.script.hold(false)
        try await h.idle()
        #expect(h.source.snapshot.calendar(Cal.sampleSport)?.visible == true)
        #expect(h.changes.contains(.error(Cal.Text.errorText(.calendarNotFound)!)))
        let p = try #require(try await h.script.last(API.CalendarUpdate.name, CalendarUpdateParams.self))
        #expect(p.calendarId == Cal.sampleSport && p.visible == false)
        // Accepted: it stays.
        await h.script.set(updateFailure: nil)
        h.source.setVisible(false, calendar: Cal.sampleFamily)
        try await h.idle()
        #expect(h.source.snapshot.calendar(Cal.sampleFamily)?.visible == false)
    }

    @Test func accountsChangedListsTheCalendarsAgain() async throws {
        let h = try await Harness().started()
        defer { Task { await h.stop() } }
        let lists = await h.script.count(API.CalendarList.name)
        let accounts = await h.script.count(API.AccountList.name)
        h.source.accountsChanged()
        try await h.idle()
        #expect(await h.script.count(API.CalendarList.name) == lists + 1)
        #expect(await h.script.count(API.AccountList.name) == accounts + 1)
    }

    @Test func namedCalendarsDropTheirRanges() async throws {
        let h = try await Harness().started()
        defer { Task { await h.stop() } }
        let r = week()
        _ = try await h.source.occurrences(r)
        // A range far away, but naming a calendar this week shows: dropped.
        h.source.handle(.changed(CalendarChangedNotification(
            calendarIds: [Cal.sampleSport], from: Cal.date(2027, 3, 1, 0, 0, in: calZone), to: Cal.date(2027, 3, 2, 0, 0, in: calZone))))
        try await h.idle()
        #expect(h.source.cachedOccurrences(r) == nil)
        #expect(h.changes.last == .occurrences(from: nil, to: nil))
    }

    @Test func oldDaemonIsUnsupported() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        await h.script.set(listFailure: RPCError(code: .methodNotFound, message: "unknown method"))
        _ = try await h.started()
        #expect(h.source.snapshot.phase == .unsupported)
        h.source.connectionChanged(connected: false)
        #expect(h.source.snapshot.phase == .unavailable)
    }
}
