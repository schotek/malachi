// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The calendar page of one main window (docs/calendar.md §5.3–5.4): the
// view (Day, Week, Month, Agenda; Settings `calendar-view`), the anchor
// date and its range, the occurrences of the range, the selection, the
// search (from 2 characters, 300 ms after the last keystroke), the
// Unanswered filter (the unanswered invitations as an agenda), the
// sidebar's model and the status bar's texts. The views read the state
// after `onChange` and send what the user did; nothing here knows AppKit.
// What the views must present (the detail popover, the editor, a
// re-sign-in) leaves through the hand-off callbacks.
//
// Swift-first: macOS is the reference client of the calendar.

import Foundation

extension Cal {
    /// The answer to a `Cal.Question`: its confirm button, its alternative
    /// (when it has one) or Cancel.
    public enum Answer: Sendable, Equatable {
        case confirm, alternative, cancel
    }

    /// Waits `Duration` (a debounce); the tests put a clock of their own
    /// here.
    public typealias Sleep = @Sendable (Duration) async -> Void

    public static let taskSleep: Sleep = { d in
        try? await Task.sleep(for: d)
    }

    /// The occurrence of an unanswered invitation not in a calendar, for
    /// the Unanswered agenda.
    public static func occurrenceOf(_ i: InvitationListItem) -> Occurrence {
        Occurrence(
            accountId: i.accountId, title: i.summary.title, start: i.summary.start, end: i.summary.end,
            startDate: i.summary.startDate, allDay: i.summary.allDay, status: .confirmed, showAs: .tentative,
            myResponse: .needsAction, invitation: MessageRef(accountId: i.accountId, messageId: i.messageId))
    }
}

@MainActor
public final class CalendarController {
    /// What the content shows besides the view.
    public enum Filter: Sendable, Equatable {
        case none
        /// The unanswered invitations as an agenda (the sidebar's
        /// INVITATIONS row).
        case unanswered
    }

    /// A calendar row of the sidebar.
    public struct SidebarCalendar: Sendable, Equatable {
        public var id: CalendarID
        public var name: String
        /// "#rrggbb".
        public var color: String
        public var visible: Bool
        /// "default" / "read only" / "shared · %s" (`Prefs.calendarRowNote`).
        public var note: String
    }

    /// An account of the sidebar: its header (the view picks the capsule
    /// with `accountHeaderBadge(account)`), its calendars and the state row.
    public struct SidebarAccount: Sendable, Equatable {
        public var accountId: AccountID
        public var account: Account?
        public var name: String
        public var source: CalendarSource
        public var calendars: [SidebarCalendar]
        /// The consent or state row ("" none).
        public var stateLine: String
        /// The state row offers Sign In… (`signIn(_:)`).
        public var offersSignIn: Bool
    }

    /// The sidebar's model.
    public struct Sidebar: Sendable, Equatable {
        public var miniMonth: Cal.MiniMonth
        /// The INVITATIONS heading, its "Unanswered" row and that row's
        /// count badge (0: no badge).
        public var invitationsTitle: String
        public var unansweredTitle: String
        public var unanswered: Int
        public var accounts: [SidebarAccount]
        /// "Add Calendar…" and its menu items.
        public var addCalendar: String
        public var addCalDAV: String
        public var syncMicrosoft: String
    }

    /// The minimum of characters the search starts at.
    public static let searchMinimum = 2
    public static let searchDebounce: Duration = .milliseconds(300)
    /// The agenda's page in days (`Cal.agendaDays`); `loadMoreAgenda` adds
    /// one.
    public static let agendaPage = Cal.agendaDays

    public let source: any CalendarDataSource
    public let env: Cal.Env
    private let settings: Settings?
    private let now: @MainActor () -> Date
    private let localeFirstDay: Cal.Weekday
    private let sleep: Cal.Sleep

    // MARK: Outputs

    /// The state changed; the views read it again.
    public var onChange: (@MainActor () -> Void)?
    /// A short sentence for a toast (a refused visibility change, a failed
    /// load or search).
    public var onError: (@MainActor (String) -> Void)?
    /// Return on the selection, a click on a block: the detail popover of
    /// this occurrence (`EventDetailPopover.show`).
    public var onOpenDetail: (@MainActor (Occurrence) -> Void)?
    /// ⌫ on the selection: the detail's remove flow for this occurrence
    /// (`EventDetailController.remove()`).
    public var onRemove: (@MainActor (Occurrence) -> Void)?
    /// New Event (⌥⌘N, "+", a double click): the editor with these defaults.
    public var onNewEvent: (@MainActor (Cal.Defaults) -> Void)?
    /// Escape with a popover open: close it.
    public var onClosePopover: (@MainActor () -> Void)?
    /// Sign In… of a state row: the existing re-sign-in flow of the
    /// account with `calendar: true` (`account.oauthStart`).
    public var onSignIn: (@MainActor (AccountID) -> Void)?

    // MARK: State

    public private(set) var view: Cal.View
    /// A day in the shown range (the range is computed from it).
    public private(set) var anchor: Date
    /// The month the sidebar shows (its arrows change it alone).
    public private(set) var miniMonthAnchor: Date
    public private(set) var filter: Filter = .none
    /// The occurrences of the range (of the visible calendars).
    public private(set) var occurrences: [Occurrence] = []
    /// event.list said `complete: false` for a part of the range.
    public private(set) var incomplete = false
    public private(set) var truncated = false
    /// An event.list is on its way.
    public private(set) var loading = false
    /// The occurrence key of the selection (`Cal.occurrenceKey`).
    public private(set) var selection: String?
    /// The search field as typed.
    public private(set) var searchText = ""
    /// event.search's answer for `searchText`; nil while not searching.
    public private(set) var searchResults: [Occurrence]?
    public private(set) var searchTotal = 0
    /// The agenda's pages (from the anchor, `agendaPage` days each).
    public private(set) var agendaPages = 1

    private var observer: CalendarObserverToken?
    private var loadGeneration = 0
    private var searchTask: Task<Void, Never>?
    private var searchGeneration = 0

    public init(
        source: any CalendarDataSource, settings: Settings? = nil, env: Cal.Env = Cal.Env(zone: .current, locale: .current),
        now: @escaping @MainActor () -> Date = { Date() },
        localeFirstDay: Cal.Weekday = Cal.Weekday(firstWeekday: Foundation.Calendar.current.firstWeekday),
        sleep: @escaping Cal.Sleep = Cal.taskSleep
    ) {
        self.source = source
        self.settings = settings
        self.env = env
        self.now = now
        self.localeFirstDay = localeFirstDay
        self.sleep = sleep
        view = settings?.calendarView ?? Cal.defaultView
        let today = Cal.startOfDay(now(), env.zone)
        anchor = today
        miniMonthAnchor = today
    }

    /// Starts following the source and loads the range.
    public func start() {
        guard observer == nil else { return }
        observer = source.observe { [weak self] change in
            self?.sourceChanged(change)
        }
        reload()
    }

    /// Stops following the source; no answer is taken any more.
    public func stop() {
        observer?.cancel()
        observer = nil
        loadGeneration += 1
        searchTask?.cancel()
        searchTask = nil
    }

    // MARK: Derived state

    public var snapshot: Cal.Snapshot { source.snapshot }

    public var weekStart: Cal.Weekday {
        Cal.weekStart(snapshot.preferences.weekStart, localeFirstDay: localeFirstDay)
    }

    /// The view's range (`Cal.viewRange`); the agenda's covers its pages.
    public var range: Cal.Range {
        var r = Cal.viewRange(view, anchor: anchor, weekStart: weekStart, zone: env.zone)
        if view == .agenda && agendaPages > 1 {
            r.to = Cal.addDays(r.from, Self.agendaPage * agendaPages, env.zone)
        }
        return r
    }

    /// The window's title and subtitle.
    public var title: String { Cal.title(view, range, env: env) }
    public var subtitle: String { Cal.subtitle(view, range, weekStart: weekStart, env: env) }

    public var showDeclined: Bool { snapshot.preferences.showDeclined }

    /// The search field has a query the search runs for.
    public var searching: Bool { Cal.trimSpace(searchText).count >= Self.searchMinimum }

    public var selectedOccurrence: Occurrence? {
        guard let selection else { return nil }
        return shownOccurrences.first { Cal.occurrenceKey($0) == selection }
    }

    /// What the content lists: the search results, the unanswered
    /// invitations, else the range's occurrences.
    public var shownOccurrences: [Occurrence] {
        if let searchResults {
            return searchResults
        }
        if filter == .unanswered {
            return snapshot.invitations.map(Cal.occurrenceOf)
        }
        return occurrences
    }

    /// The agenda of the Agenda view and of the Unanswered filter (`nil`
    /// otherwise); the search results are listed in their own order.
    public var agenda: [Cal.AgendaDay]? {
        if filter == .unanswered {
            let from = Cal.startOfDay(now(), env.zone)
            return Cal.agenda(
                shownOccurrences, from: from, days: API.Limits.calendarFutureDays, now: now(), env: env, showDeclined: true)
        }
        guard view == .agenda else { return nil }
        let r = range
        return Cal.agenda(
            occurrences, from: r.from, days: Cal.daysBetween(r.from, r.to, env.zone), now: now(), env: env, showDeclined: showDeclined)
    }

    /// The heading above the list in place of the view ("Results",
    /// "Invitations"; "" none).
    public var listHeading: String {
        if searchResults != nil { return Cal.resultsHeading }
        if filter == .unanswered { return Cal.unansweredHeading }
        return ""
    }

    /// The text of an empty list ("" when there is something to show).
    public var emptyListText: String {
        if let r = searchResults {
            return r.isEmpty ? Cal.noResults : ""
        }
        if view == .agenda || filter == .unanswered {
            return (agenda ?? []).isEmpty ? Cal.agendaEmpty : ""
        }
        return ""
    }

    /// Why the page has nothing to show (nil: it has).
    public var emptyKind: Cal.EmptyKind? {
        let s = snapshot
        switch s.phase {
        case .unavailable, .unsupported:
            if s.calendarAccounts.isEmpty { return .noBackend }
        case .loading:
            return .loading
        default:
            break
        }
        if s.calendarAccounts.isEmpty {
            return s.phase == .failed ? .noBackend : .noAccounts
        }
        if s.calendarAccounts.allSatisfy({ !$0.sync }) {
            return .syncOff
        }
        return nil
    }

    /// The sidebar.
    public var sidebar: Sidebar {
        let s = snapshot
        // Disabled calendars are only in Preferences → Calendar.
        let accounts = s.activeCalendarAccounts.map { a -> SidebarAccount in
            let calendars = a.calendars.map {
                SidebarCalendar(id: $0.id, name: Cal.cleanLine($0.name), color: $0.color, visible: $0.visible, note: Cal.Prefs.calendarRowNote($0))
            }
            var line = ""
            var signIn = false
            switch a.state.status {
            case .consentRequired:
                line = Cal.Text.consentLine
                signIn = true
            case .authRequired:
                line = Cal.Text.errorText(.authRequired) ?? rpcErrorText(Cal.Text.modeName, RPCError(code: .authRequired, message: ""))
                signIn = true
            case .offline:
                line = Cal.Text.offlineLine
            case .error:
                if let e = a.state.error {
                    line = Cal.errorMessage(e)
                }
            default:
                break
            }
            return SidebarAccount(
                accountId: a.accountId, account: s.account(a.accountId), name: s.accountName(a.accountId), source: a.source,
                calendars: calendars, stateLine: line, offersSignIn: signIn)
        }
        return Sidebar(
            miniMonth: Cal.MiniMonth(anchor: miniMonthAnchor, weekStart: weekStart, now: now(), shown: range, env: env),
            invitationsTitle: Cal.unansweredHeading, unansweredTitle: Cal.Text.unanswered, unanswered: s.unanswered, accounts: accounts,
            addCalendar: Cal.Prefs.addCalendar, addCalDAV: Cal.Prefs.addCalDAV, syncMicrosoft: Cal.Prefs.syncMicrosoft)
    }

    /// The status bar's left text: the calendars' state.
    public var statusLine: String { Cal.Text.statusLine(snapshot.states, now: now(), zone: env.zone) }

    /// The status bar's right text: the unanswered count ("" for none).
    public var unansweredLine: String {
        snapshot.unanswered > 0 ? Cal.Text.unansweredCount(snapshot.unanswered) : ""
    }

    // MARK: Navigation

    /// Day, Week, Month or Agenda (stored in Settings); ends the Unanswered
    /// filter.
    public func setView(_ v: Cal.View) {
        guard v != view || filter != .none else { return }
        // The Agenda starts at today when switched to; every other switch
        // keeps the anchor (`Cal.switchAnchor`).
        anchor = Cal.switchAnchor(from: view, to: v, anchor: anchor, today: now(), zone: env.zone)
        miniMonthAnchor = anchor
        view = v
        filter = .none
        agendaPages = 1
        settings?.calendarView = v
        reload()
    }

    /// ⌘T: the range of today.
    public func today() {
        goTo(day: now())
    }

    /// ‹ › and ⌘← / ⌘→: the previous or next range.
    public func step(_ dir: Int) {
        anchor = Cal.step(view, anchor: anchor, dir < 0 ? -1 : 1, zone: env.zone)
        miniMonthAnchor = anchor
        agendaPages = 1
        filter = .none
        reload()
    }

    /// A day of the mini month, the Today strip's Open Calendar, a
    /// notification: the range of that day in the current view.
    public func goTo(day: Date) {
        anchor = Cal.startOfDay(day, env.zone)
        miniMonthAnchor = anchor
        agendaPages = 1
        filter = .none
        reload()
    }

    /// Shows `day` in the Day view and selects `key` there (Show in
    /// Calendar, an alert's click).
    public func show(day: Date, view v: Cal.View = .day, select key: String? = nil) {
        let target = v == .agenda ? Cal.switchAnchor(from: view, to: v, anchor: day, today: now(), zone: env.zone) : day
        view = v
        settings?.calendarView = v
        selection = key
        goTo(day: target)
    }

    /// The mini month's arrows: its month alone.
    public func stepMiniMonth(_ dir: Int) {
        miniMonthAnchor = Cal.MiniMonth.step(miniMonthAnchor, dir < 0 ? -1 : 1, zone: env.zone)
        changed()
    }

    /// Agenda: the next page (infinite scrolling).
    public func loadMoreAgenda() {
        guard view == .agenda, filter == .none, searchResults == nil else { return }
        agendaPages += 1
        reload()
    }

    /// The INVITATIONS row: the unanswered invitations as an agenda.
    public func showUnanswered() {
        filter = .unanswered
        clearSearchQuietly()
        selection = nil
        changed()
    }

    /// Back from the Unanswered filter.
    public func clearFilter() {
        guard filter != .none else { return }
        filter = .none
        changed()
    }

    // MARK: Calendars

    /// A calendar's checkbox (calendar.update `visible`).
    public func setVisible(_ visible: Bool, calendar id: CalendarID) {
        source.setVisible(visible, calendar: id)
    }

    /// Sign In… of a state row.
    public func signIn(_ account: AccountID) {
        onSignIn?(account)
    }

    // MARK: Selection

    /// Selects an occurrence by key (nil clears).
    public func select(_ key: String?) {
        guard key != selection else { return }
        selection = key
        changed()
    }

    /// The arrows: the previous or next occurrence in time order.
    public func moveSelection(_ delta: Int) {
        let order = Cal.selectionOrder(shownOccurrences, zone: env.zone)
        let next = Cal.moveSelection(order, current: selection ?? "", delta: delta)
        select(next.isEmpty ? nil : next)
    }

    /// Opens the detail of an occurrence (a click, Return).
    public func openDetail(_ o: Occurrence) {
        select(Cal.occurrenceKey(o))
        onOpenDetail?(o)
    }

    // MARK: New events

    /// New Event: defaults for a start at `at` from `origin` (the next
    /// half hour from now for `.now`).
    public func newEvent(at: Date? = nil, origin: Cal.Origin = .now) {
        onNewEvent?(newEventDefaults(at: at, origin: origin))
    }

    public func newEventDefaults(at: Date? = nil, origin: Cal.Origin = .now) -> Cal.Defaults {
        Cal.newEventDefaults(at: at ?? now(), origin: origin, preferences: snapshot.preferences, zone: env.zone)
    }

    // MARK: Search

    /// The search field changed: event.search 300 ms after the last change
    /// from 2 characters; fewer end the search.
    public func setSearch(_ text: String) {
        guard text != searchText else { return }
        searchText = text
        searchTask?.cancel()
        searchTask = nil
        searchGeneration += 1
        let q = Cal.trimSpace(text)
        guard q.count >= Self.searchMinimum else {
            if searchResults != nil {
                searchResults = nil
                searchTotal = 0
            }
            changed()
            return
        }
        let generation = searchGeneration
        let sleep = sleep
        let source = source
        searchTask = Task { [weak self] in
            await sleep(Self.searchDebounce)
            guard !Task.isCancelled, let self, generation == self.searchGeneration else { return }
            do {
                let r = try await source.search(EventSearchParams(query: q, limit: API.Limits.maxEventSearchResults))
                guard generation == self.searchGeneration else { return }
                self.searchResults = r.occurrences
                self.searchTotal = r.total
                self.selection = nil
            } catch {
                guard generation == self.searchGeneration else { return }
                self.searchResults = []
                self.searchTotal = 0
                self.onError?(Cal.errorMessage(error))
            }
            self.searchTask = nil
            self.changed()
        }
        changed()
    }

    /// Escape after the popover, or the field's ×.
    public func clearSearch() {
        guard !searchText.isEmpty || searchResults != nil else { return }
        clearSearchQuietly()
        changed()
    }

    private func clearSearchQuietly() {
        searchTask?.cancel()
        searchTask = nil
        searchGeneration += 1
        searchText = ""
        searchResults = nil
        searchTotal = 0
    }

    /// A search is waiting for its debounce or its answer (the tests).
    var searchPending: Bool { searchTask != nil }

    // MARK: Keys

    /// A key in Calendar mode (`Cal.keyFor`); true when it was the
    /// calendar's. ⌘3 (Show Calendar) is the window's and returns false.
    @discardableResult
    public func handleKey(_ p: Cal.Press, inText: Bool = false, popoverOpen: Bool = false) -> Bool {
        let ctx = Cal.KeyContext(calendarMode: true, inText: inText, selected: selectedOccurrence != nil)
        guard let action = Cal.keyFor(p, context: ctx) else { return false }
        switch action {
        case .showCalendar:
            return false
        case .today: today()
        case .previous: step(-1)
        case .next: step(1)
        case .dayView: setView(.day)
        case .weekView: setView(.week)
        case .monthView: setView(.month)
        case .agendaView: setView(.agenda)
        case .newEvent: newEvent()
        case .openDetail:
            guard let o = selectedOccurrence else { return false }
            onOpenDetail?(o)
        case .remove:
            guard let o = selectedOccurrence else { return false }
            onRemove?(o)
        case .escape:
            switch Cal.escapeFor(popoverOpen: popoverOpen, searching: !searchText.isEmpty || searchResults != nil) {
            case .closePopover: onClosePopover?()
            case .clearSearch: clearSearch()
            case .nothing:
                guard filter != .none else { return false }
                clearFilter()
            }
        case .selectPrevious: moveSelection(-1)
        case .selectNext: moveSelection(1)
        }
        return true
    }

    // MARK: Loading

    /// Loads the range's occurrences (the cached ones show at once).
    public func reload() {
        loadGeneration += 1
        let generation = loadGeneration
        let r = range
        if let hit = source.cachedOccurrences(r) {
            take(hit)
        }
        loading = true
        changed()
        let source = source
        Task { [weak self] in
            let outcome: Result<EventListResult, any Error>
            do {
                outcome = .success(try await source.occurrences(r))
            } catch {
                outcome = .failure(error)
            }
            guard let self, generation == self.loadGeneration else { return }
            self.loading = false
            switch outcome {
            case .success(let res):
                self.take(res)
            case .failure(let err):
                self.onError?(Cal.errorMessage(err))
            }
            self.changed()
        }
    }

    private func take(_ r: EventListResult) {
        let visible = snapshot.visibleCalendars
        occurrences = r.occurrences.filter { o in o.calendarId.map(visible.contains) ?? true }
        incomplete = !r.complete
        truncated = r.truncated
        if let s = selection, filter == .none, searchResults == nil,
           !occurrences.contains(where: { Cal.occurrenceKey($0) == s }) {
            selection = nil
        }
    }

    /// A load is on its way (the tests).
    var isIdle: Bool { !loading && searchTask == nil }

    private func sourceChanged(_ c: Cal.SourceChange) {
        switch c {
        case .snapshot:
            // A calendar shown or hidden: the cached list without it at
            // once, the daemon's answer after its notification.
            if let hit = source.cachedOccurrences(range) {
                take(hit)
            } else {
                let visible = snapshot.visibleCalendars
                occurrences = occurrences.filter { o in o.calendarId.map(visible.contains) ?? true }
            }
            changed()
        case .occurrences(let from, let to):
            let r = range
            if Cal.rangesOverlap(r.from, r.to, from: from, to: to) {
                reload()
            }
        case .error(let text):
            onError?(text)
        case .event, .alert:
            break
        }
    }

    private func changed() {
        onChange?()
    }
}
