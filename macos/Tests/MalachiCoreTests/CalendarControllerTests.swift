// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The calendar page's controller (Controllers/CalendarController.swift)
// over the in-memory source with the invented samples: the views and their
// ranges per week start, the navigation, the persisted view, the search
// with its debounce on a clock the test fires, the selection and its keys,
// the sidebar with the consent row, the Unanswered filter, the empty
// states. Also the helpers of the other calendar controller tests.

struct CalTimeout: Error {}

@MainActor
func calWaitUntil(_ timeout: Duration = .seconds(10), _ cond: @MainActor () async -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while await !cond() {
        if ContinuousClock.now > deadline { throw CalTimeout() }
        try await Task.sleep(for: .milliseconds(2))
    }
}

/// Sleeps that wait until the test fires them.
actor CalManualClock {
    private var waiting: [(Duration, CheckedContinuation<Void, Never>)] = []

    func sleep(_ d: Duration) async {
        await withCheckedContinuation { waiting.append((d, $0)) }
    }

    var pending: [Duration] { waiting.map(\.0) }

    func fire() {
        let w = waiting
        waiting = []
        for (_, c) in w {
            c.resume()
        }
    }
}

let calZone = TimeZone(identifier: "Europe/Prague")!
/// Wednesday 14 October 2026, 10:17 in Prague.
let calNow = Cal.date(2026, 10, 14, 10, 17, in: calZone)
let calEnv = Cal.Env.english(calZone)

@MainActor
func calSamples(writes: Bool = true) -> InMemoryCalendarSource {
    let s = InMemoryCalendarSource(Cal.sampleData(now: calNow, zone: calZone))
    s.emulatesWrites = writes
    return s
}

/// A throwaway Settings domain.
final class CalScratchSettings {
    let suite: String
    let defaults: UserDefaults
    let settings: Settings

    @MainActor init() {
        suite = "io.github.schotek.Malachi.caltest-\(UUID().uuidString)"
        defaults = UserDefaults(suiteName: suite)!
        Settings.register(defaults: defaults)
        settings = Settings(defaults: defaults)
    }

    deinit {
        defaults.removePersistentDomain(forName: suite)
    }
}

@MainActor
private func controller(
    _ source: InMemoryCalendarSource = calSamples(), settings: Settings? = nil, clock: CalManualClock? = nil,
    localeFirstDay: Cal.Weekday = .monday
) -> CalendarController {
    var sleep: Cal.Sleep = Cal.taskSleep
    if let clock {
        sleep = { d in await clock.sleep(d) }
    }
    return CalendarController(
        source: source, settings: settings, env: calEnv, now: { calNow }, localeFirstDay: localeFirstDay, sleep: sleep)
}

@MainActor
private func started(_ c: CalendarController) async throws -> CalendarController {
    c.start()
    try await calWaitUntil { c.isIdle }
    return c
}

@MainActor
@Suite(.serialized) struct CalendarControllerTests {
    @Test func weekRangeFollowsTheWeekStart() async throws {
        let c = try await started(controller())
        #expect(c.view == .week)
        #expect(c.range.from == Cal.date(2026, 10, 12, 0, 0, in: calZone))
        #expect(c.range.to == Cal.date(2026, 10, 19, 0, 0, in: calZone))
        #expect(c.title == "October 2026")
        #expect(!c.subtitle.isEmpty)
        // The locale's first day (System) is Sunday: the week from the 11th.
        let us = try await started(controller(localeFirstDay: .sunday))
        #expect(us.range.from == Cal.date(2026, 10, 11, 0, 0, in: calZone))
        // The preference wins over the locale.
        let src = calSamples()
        src.update { $0.preferences.weekStart = .saturday }
        let sat = try await started(controller(src, localeFirstDay: .monday))
        #expect(sat.range.from == Cal.date(2026, 10, 10, 0, 0, in: calZone))
    }

    @Test func viewsAndStepsAndToday() async throws {
        let scratch = CalScratchSettings()
        let c = try await started(controller(settings: scratch.settings))
        c.setView(.day)
        #expect(scratch.defaults.string(forKey: "calendar-view") == "day")
        #expect(c.range.from == Cal.date(2026, 10, 14, 0, 0, in: calZone))
        #expect(c.range.to == Cal.date(2026, 10, 15, 0, 0, in: calZone))
        c.step(1)
        #expect(c.range.from == Cal.date(2026, 10, 15, 0, 0, in: calZone))
        c.setView(.month)
        #expect(c.range.from == Cal.date(2026, 9, 28, 0, 0, in: calZone))
        #expect(Cal.daysBetween(c.range.from, c.range.to, calZone) == 42)
        c.step(-1)
        #expect(Cal.parts(c.anchor, calZone).month == 9)
        c.today()
        #expect(c.anchor == Cal.date(2026, 10, 14, 0, 0, in: calZone))
        c.setView(.agenda)
        #expect(Cal.daysBetween(c.range.from, c.range.to, calZone) == 30)
        c.loadMoreAgenda()
        #expect(Cal.daysBetween(c.range.from, c.range.to, calZone) == 60)
        // A new window reads the stored view.
        let again = controller(settings: scratch.settings)
        #expect(again.view == .agenda)
    }

    @Test func agendaStartsAtTodayWhenSwitchedTo() async throws {
        let c = try await started(controller())
        let today = Cal.date(2026, 10, 14, 0, 0, in: calZone)
        c.setView(.day)
        c.goTo(day: Cal.date(2026, 10, 13, 9, 0, in: calZone))
        #expect(c.anchor == Cal.date(2026, 10, 13, 0, 0, in: calZone))
        c.setView(.agenda)
        #expect(c.anchor == today && c.range.from == today)
        // ‹ › move the Agenda; leaving it keeps the stepped anchor.
        c.step(1)
        let stepped = c.anchor
        #expect(stepped == Cal.addDays(today, 30, calZone))
        c.setView(.week)
        #expect(c.anchor == stepped)
        // Month → Week keeps the anchor too.
        c.setView(.month)
        c.step(1)
        let month = c.anchor
        c.setView(.week)
        #expect(c.anchor == month)
    }

    @Test func goToDayAndMiniMonth() async throws {
        let c = try await started(controller())
        c.goTo(day: Cal.date(2026, 11, 3, 15, 0, in: calZone))
        #expect(c.range.from == Cal.date(2026, 11, 2, 0, 0, in: calZone))
        #expect(c.sidebar.miniMonth.title == "November 2026")
        c.stepMiniMonth(1)
        #expect(c.sidebar.miniMonth.title == "December 2026")
        // The range did not move with the mini month.
        #expect(c.range.from == Cal.date(2026, 11, 2, 0, 0, in: calZone))
        // The shown week is banded in the mini month of its month.
        c.stepMiniMonth(-1)
        let banded = c.sidebar.miniMonth.days.filter(\.shown)
        #expect(banded.count == 7)
        #expect(banded.first?.bandStart == true && banded.last?.bandEnd == true)
    }

    @Test func occurrencesOfTheRangeAndVisibility() async throws {
        let src = calSamples()
        let c = try await started(controller(src))
        #expect(!c.occurrences.isEmpty)
        #expect(c.occurrences.contains { $0.title == "Climbing" })
        #expect(c.occurrences.contains { $0.invitation != nil })
        c.setVisible(false, calendar: Cal.sampleSport)
        try await calWaitUntil { c.isIdle }
        #expect(!c.occurrences.contains { $0.title == "Climbing" })
        #expect(src.writes.contains(.visible(Cal.sampleSport, false)))
        #expect(c.sidebar.accounts.flatMap(\.calendars).first { $0.id == Cal.sampleSport }?.visible == false)
        // Next week has none of the samples' events.
        c.step(1)
        try await calWaitUntil { c.isIdle }
        #expect(c.occurrences.isEmpty)
    }

    @Test func refusedVisibilityIsReported() async throws {
        let src = calSamples()
        src.failures[API.CalendarUpdate.name] = RPCError(code: .calendarNotFound, message: "gone")
        let c = try await started(controller(src))
        var errors: [String] = []
        c.onError = { errors.append($0) }
        c.setVisible(false, calendar: Cal.sampleSport)
        #expect(errors == [Cal.Text.errorText(.calendarNotFound)!])
        #expect(src.snapshot.calendar(Cal.sampleSport)?.visible == true)
    }

    @Test func searchWaitsForTheDebounceAndTwoCharacters() async throws {
        let clock = CalManualClock()
        let src = calSamples()
        let c = try await started(controller(src, clock: clock))
        c.setSearch("b")
        #expect(!c.searchPending && c.searchResults == nil)
        c.setSearch("bu")
        c.setSearch("bud")
        try await calWaitUntil { await clock.pending.count == 2 }
        #expect(await clock.pending == [.milliseconds(300), .milliseconds(300)])
        #expect(!src.reads.contains(API.EventSearch.name))
        await clock.fire()
        try await calWaitUntil { !c.searchPending }
        // Only the last query ran.
        #expect(src.reads.filter { $0 == API.EventSearch.name }.count == 1)
        #expect(c.searchResults?.map(\.title) == ["Budget review"])
        #expect(c.listHeading == Cal.resultsHeading)
        #expect(c.shownOccurrences.map(\.title) == ["Budget review"])
        // Escape without a popover clears the search.
        #expect(c.handleKey(Cal.Press(special: .escape)))
        #expect(c.searchResults == nil && c.searchText.isEmpty)
        c.setSearch("zz")
        try await calWaitUntil { await clock.pending.count == 1 }
        await clock.fire()
        try await calWaitUntil { !c.searchPending }
        #expect(c.emptyListText == Cal.noResults)
        c.setSearch("z")
        #expect(c.searchResults == nil)
    }

    @Test func selectionAndKeys() async throws {
        let c = try await started(controller())
        var opened: [String] = []
        var removed: [String] = []
        var closed = 0
        var defaults: [Cal.Defaults] = []
        c.onOpenDetail = { opened.append(Cal.occurrenceKey($0)) }
        c.onRemove = { removed.append(Cal.occurrenceKey($0)) }
        c.onClosePopover = { closed += 1 }
        c.onNewEvent = { defaults.append($0) }
        // Return and ⌫ need a selection.
        #expect(!c.handleKey(Cal.Press(special: .return)))
        #expect(c.handleKey(Cal.Press(special: .down)))
        let order = Cal.selectionOrder(c.occurrences, zone: calZone)
        #expect(c.selection == order.first)
        #expect(c.handleKey(Cal.Press(special: .right)))
        #expect(c.selection == order[1])
        #expect(c.handleKey(Cal.Press(special: .left)))
        #expect(c.selection == order[0])
        // Not in a text field.
        #expect(!c.handleKey(Cal.Press(special: .down), inText: true))
        #expect(c.handleKey(Cal.Press(special: .return)))
        #expect(opened == [order[0]])
        #expect(c.handleKey(Cal.Press(special: .delete)))
        #expect(removed == [order[0]])
        #expect(c.handleKey(Cal.Press(special: .escape), popoverOpen: true))
        #expect(closed == 1)
        // ⌃⌘3, ⌘→, ⌘T, ⌥⌘N.
        #expect(c.handleKey(Cal.Press(rune: "3", primary: true, control: true)))
        #expect(c.view == .month)
        #expect(c.handleKey(Cal.Press(special: .right, primary: true)))
        #expect(Cal.parts(c.anchor, calZone).month == 11)
        #expect(c.handleKey(Cal.Press(rune: "t", primary: true)))
        #expect(Cal.parts(c.anchor, calZone).month == 10)
        #expect(c.handleKey(Cal.Press(rune: "n", primary: true, alt: true)))
        #expect(defaults.first?.start == Cal.date(2026, 10, 14, 10, 30, in: calZone))
        // ⌘3 is the window's.
        #expect(!c.handleKey(Cal.Press(rune: "3", primary: true)))
    }

    @Test func newEventDefaultsByOrigin() async throws {
        let c = try await started(controller())
        let cell = c.newEventDefaults(at: Cal.date(2026, 10, 20, 0, 0, in: calZone), origin: .monthCell)
        #expect(cell.start == Cal.date(2026, 10, 20, 9, 0, in: calZone))
        #expect(cell.end == Cal.date(2026, 10, 20, 10, 0, in: calZone))
        let allDay = c.newEventDefaults(at: Cal.date(2026, 10, 20, 13, 0, in: calZone), origin: .allDay)
        #expect(allDay.allDay)
        let slot = c.newEventDefaults(at: Cal.date(2026, 10, 20, 13, 40, in: calZone), origin: .slot)
        #expect(slot.start == Cal.date(2026, 10, 20, 13, 30, in: calZone))
    }

    @Test func sidebarStatusAndConsent() async throws {
        let src = calSamples()
        let c = try await started(controller(src))
        let s = c.sidebar
        #expect(s.accounts.map(\.name) == ["Work", "Gmail", "Nextcloud"])
        #expect(s.accounts.map(\.source) == [.graph, .google, .caldav])
        #expect(s.unanswered == 3)
        #expect(s.invitationsTitle == "Invitations" && s.unansweredTitle == "Unanswered")
        #expect(s.accounts.allSatisfy { $0.stateLine.isEmpty && !$0.offersSignIn })
        #expect(c.statusLine == "Calendars synced 2 minutes ago")
        #expect(c.unansweredLine == "3 unanswered invitations")
        var signIn: [AccountID] = []
        c.onSignIn = { signIn.append($0) }
        src.handle(.state(CalendarSyncState(accountId: Cal.sampleWork, status: .consentRequired, progress: -1)))
        let work = c.sidebar.accounts[0]
        #expect(work.stateLine == Cal.Text.consentLine && work.offersSignIn)
        #expect(c.statusLine == Cal.Text.consentLine)
        c.signIn(work.accountId)
        #expect(signIn == [Cal.sampleWork])
    }

    @Test func disabledCalendarsAreOnlyInPreferences() async throws {
        let src = calSamples()
        src.update { s in
            s.calendarAccounts[1].calendars[1].disabled = true
        }
        let c = try await started(controller(src))
        #expect(c.sidebar.accounts[1].calendars.map(\.id) == [Cal.samplePersonal])
        #expect(!src.snapshot.visibleCalendars.contains(Cal.sampleFamily))
        #expect(!src.snapshot.writableCalendars.map(\.id).contains(Cal.sampleFamily))
        #expect(src.snapshot.calendar(Cal.sampleFamily) == nil && src.snapshot.calendarName(Cal.sampleFamily).isEmpty)
        #expect(!c.occurrences.contains { $0.calendarId == Cal.sampleFamily })
        let e = EventEditorController(source: src, env: calEnv, now: { calNow })
        #expect(!e.calendars.map(\.id).contains(Cal.sampleFamily))
        // Preferences → Calendar still lists it, switched off.
        let a = CalendarAccountController(source: src, accountId: Cal.sampleGmail)
        #expect(a.calendarAccount?.calendars.map(\.id) == [Cal.samplePersonal, Cal.sampleFamily])
        #expect(a.rows.map(\.name) == ["Personal", "Family"])
    }

    @Test func unansweredFilterListsTheInvitations() async throws {
        let c = try await started(controller())
        c.showUnanswered()
        #expect(c.filter == .unanswered)
        #expect(c.listHeading == Cal.unansweredHeading)
        let titles = (c.agenda ?? []).flatMap(\.items).map(\.occurrence.title)
        #expect(Set(titles) == ["Budget review", "Vendor demo", "Club meeting"])
        #expect((c.agenda ?? []).flatMap(\.items).allSatisfy { $0.state == .unanswered })
        // Escape ends the filter; so does a view change.
        #expect(c.handleKey(Cal.Press(special: .escape)))
        #expect(c.filter == .none)
        c.showUnanswered()
        c.setView(.week)
        #expect(c.filter == .none)
    }

    @Test func emptyStates() async throws {
        let empty = InMemoryCalendarSource()
        let c = try await started(controller(empty))
        #expect(c.emptyKind == .noAccounts)
        empty.update { $0.phase = .unavailable }
        #expect(c.emptyKind == .noBackend)
        let src = calSamples()
        src.update { s in
            for i in s.calendarAccounts.indices {
                s.calendarAccounts[i].sync = false
            }
        }
        #expect(try await started(controller(src)).emptyKind == .syncOff)
        #expect(try await started(controller()).emptyKind == nil)
    }

    @Test func notificationsReloadAnOverlappingRange() async throws {
        let src = calSamples()
        let c = try await started(controller(src))
        let before = src.reads.filter { $0 == API.EventList.name }.count
        // Next year: not this week.
        src.handle(.changed(CalendarChangedNotification(
            from: Cal.date(2027, 1, 1, 0, 0, in: calZone), to: Cal.date(2027, 1, 2, 0, 0, in: calZone))))
        try await calWaitUntil { c.isIdle }
        #expect(src.reads.filter { $0 == API.EventList.name }.count == before)
        src.handle(.changed(CalendarChangedNotification(
            from: Cal.date(2026, 10, 15, 0, 0, in: calZone), to: Cal.date(2026, 10, 16, 0, 0, in: calZone))))
        try await calWaitUntil { c.isIdle }
        #expect(src.reads.filter { $0 == API.EventList.name }.count == before + 1)
        src.handle(.changed(CalendarChangedNotification()))
        try await calWaitUntil { c.isIdle }
        #expect(src.reads.filter { $0 == API.EventList.name }.count == before + 2)
    }

    @Test func samplesAnswerThePreview() async throws {
        let src = calSamples(writes: false)
        await #expect(throws: Cal.SourceError.preview) {
            _ = try await src.sync(CalendarSyncParams())
        }
        #expect(Cal.errorMessage(Cal.SourceError.preview) == Cal.Text.previewOnly)
        // The checkboxes work.
        src.setVisible(false, calendar: Cal.sampleFamily)
        #expect(src.snapshot.calendar(Cal.sampleFamily)?.visible == false)
        #expect(InMemoryCalendarSource.samples(now: calNow, zone: calZone).snapshot.phase == .ready)
    }
}
