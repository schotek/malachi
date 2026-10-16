// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

// The Calendar mode's part of the window controller (docs/calendar.md
// §5.3, D27, D28): the window's `CalendarController` over the source the
// application gives it (`AppState.calendarSource(for:)`: the daemon's, or
// the samples with MALACHI_CALENDAR_SAMPLES=1), started on the first
// entry; the page (`CalendarPageViewController`) and the toolbar
// (`CalendarToolbar`), made on first use; the window's title, subtitle and
// status bar following the controller; New Event (⌥⌘N, the toolbar's +);
// the assistant panel moving into the page's split view while ✦ shows it
// there (D28) and back to the mail's when the window leaves the Calendar.
//
// Entry points for the views (M5–M10): `calendarController` (the window's
// controller; its `onChange` belongs to `calendarDidChange`, which calls
// `CalendarPageViewController.controllerChanged()`), `calendarPage`
// (`show(view:)`, `sidebarContainer`, `contentContainer`),
// `calendarToolbar`.

/// What the Calendar mode keeps beside the window controller.
@MainActor
final class CalendarWindowExtras {
    var controller: CalendarController?
    var page: CalendarPageViewController?
    var toolbar: CalendarToolbar?
    /// Whether the controller (and the daemon's source) was started.
    var started = false
    /// What had the keyboard in the Calendar when the window left it.
    weak var responder: NSResponder?
    /// Refreshes the status bar's "synced … ago" while the Calendar shows.
    var statusTimer: Timer?
}

extension MainWindowController {
    /// The window's calendar, made on first use (also for New Event in
    /// Mail, whose defaults come from the calendar's preferences).
    var calendarController: CalendarController {
        if let c = calendarExtras.controller {
            return c
        }
        let c = CalendarController(source: state.calendarSource(for: self), settings: state.settings)
        calendarExtras.controller = c
        c.onChange = { [weak self] in
            self?.calendarDidChange()
        }
        c.onError = { [weak self] text in
            self?.toasts.show(text)
        }
        c.onNewEvent = { defaults in
            EventEditorManager.shared.openNew(defaults)
        }
        c.onOpenDetail = { [weak self] o in
            self?.showEventDetail(o)
        }
        c.onSignIn = { [weak self] account in
            self?.calendarSignIn(account)
        }
        return c
    }

    /// The Calendar's page, made on first use.
    var calendarPage: CalendarPageViewController {
        if let p = calendarExtras.page {
            return p
        }
        let p = CalendarPageViewController()
        p.controller = calendarController
        // The sidebar's Add Calendar… menu: the CalDAV sheet on this
        // window, and Settings → Calendar for a Microsoft 365 account.
        p.sidebar.onAddCalDAV = { [weak self] in
            self?.state.showAddCalDAVCalendar(from: self?.window)
        }
        p.sidebar.onSyncMicrosoft = { [weak self] in
            self?.state.showCalendarPreferences()
        }
        calendarExtras.page = p
        return p
    }

    /// The Calendar's toolbar, made on first use.
    var calendarToolbar: CalendarToolbar {
        if let t = calendarExtras.toolbar {
            return t
        }
        let t = CalendarToolbar(controller: calendarController, showsAssistant: state.assistant.panelShown)
        t.onAssistant = { [weak self] in
            self?.toggleCalendarAssistant()
        }
        calendarExtras.toolbar = t
        return t
    }

    /// Starts the calendar the first time it shows; it keeps running from
    /// then on (the hub's notifications reach a started source only).
    func startCalendar() {
        guard !calendarExtras.started else { return }
        calendarExtras.started = true
        (calendarController.source as? DaemonCalendarSource)?.start()
        calendarController.start()
    }

    /// Setting the Calendar mode up in the window (`setMode`'s part).
    func showCalendar(in window: NSWindow) {
        startCalendar()
        let page = calendarPage
        page.controllerChanged()
        // What does not fit the window's width folds before the page comes in.
        page.willResize(toWidth: window.contentLayoutRect.width)
        content.setMode(.calendar, page: page)
        page.contentContainer.setOverlay(toasts)
        installCalendarToolbar()
        calendarToolbar.update()
        restoreCalendarFocus(in: window)
    }

    /// Puts the Calendar's toolbar into the window and makes its sidebar
    /// separator anew: AppKit binds it to a split view once, and the page's
    /// split view has just come (back) into the window.
    func installCalendarToolbar() {
        guard let window else { return }
        let tb = calendarToolbar.toolbar
        if window.toolbar !== tb {
            window.toolbar = tb
        }
        BoardToolbar.rebindSidebarSeparator(in: tb)
        BoardToolbar.keepSidebarToggle(in: tb)
        tb.validateVisibleItems()
    }

    /// After every change of the controller: the page, the toolbar, the
    /// title and the status bar follow.
    func calendarDidChange() {
        calendarExtras.page?.controllerChanged()
        calendarExtras.toolbar?.update()
        guard mode == .calendar else { return }
        applyTitle()
        updateCalendarStatus()
    }

    /// The status bar's texts for the mode: in Calendar the calendars'
    /// state and the unanswered count, else the sync line as always.
    func updateCalendarStatus() {
        let bar = content.statusBar as? StatusBarViewController
        guard mode == .calendar else {
            calendarExtras.statusTimer?.invalidate()
            calendarExtras.statusTimer = nil
            bar?.setCalendar(line: nil, note: "")
            return
        }
        let c = calendarController
        bar?.setCalendar(line: c.statusLine, note: c.unansweredLine)
        guard calendarExtras.statusTimer == nil else { return }
        calendarExtras.statusTimer = Timer.scheduledTimer(withTimeInterval: 60, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated {
                self?.updateCalendarStatus()
            }
        }
    }

    /// File ▸ New Event (⌥⌘N), the toolbar's +: a new event with the
    /// calendar's defaults (`Board.allows(.newEvent)`: Mail and Calendar).
    @objc func newEvent(_ sender: Any?) {
        guard Board.allows(.newEvent, in: mode) else { return }
        EventEditorManager.shared.openNew(calendarController.newEventDefaults())
    }

    /// The sidebar's "Sign In…" of an account whose calendar needs consent:
    /// the re-sign-in of the auth banner (`SyncController.requestSignInURL`)
    /// with the calendar scopes, its page in the browser. The daemon
    /// completes the sign-in by itself; its notify.accountsChanged and
    /// notify.calendarState reach the source through the hub
    /// (`AppCalendar`), which loads the account's calendars again.
    private func calendarSignIn(_ account: AccountID) {
        guard let sync = (content.statusBar as? StatusBarViewController)?.sync else { return }
        let client = state.client
        Task { [weak self] in
            switch await sync.requestSignInURL(client: client, accountId: account, fallbackURL: nil, calendar: true) {
            case .open(let url):
                openInBrowser(url) { [weak self] text in self?.toasts.show(text) }
            case .failed(let text):
                self?.toasts.show(text)
            }
        }
    }

    /// The controller asks for an occurrence's detail without a view to
    /// anchor it (a key): the popover opens over the content.
    private func showEventDetail(_ o: Occurrence) {
        guard mode == .calendar, let id = o.eventId else { return }
        let anchor = calendarPage.contentContainer.view
        let rect = NSRect(x: anchor.bounds.midX - 1, y: anchor.bounds.midY - 1, width: 2, height: 2)
        EventDetailPopover.show(eventID: id, recurrenceID: o.recurrenceId, relativeTo: rect, of: anchor)
    }

    /// Gives the keyboard back to what had it in the Calendar, when it is
    /// still in this window and showing; otherwise to the page's content.
    private func restoreCalendarFocus(in window: NSWindow) {
        let saved = calendarExtras.responder as? NSView
        calendarExtras.responder = nil
        if let saved, saved.window === window, !saved.isHiddenOrHasHiddenAncestor, window.makeFirstResponder(saved) {
            return
        }
        let content = calendarPage.contentContainer.content?.view ?? calendarPage.contentContainer.view
        window.makeFirstResponder(content.acceptsFirstResponder ? content : nil)
    }

    // MARK: The assistant panel (D28)

    /// ✦ in the Calendar's toolbar: the window's assistant panel moves into
    /// the page's split view and unfolds with the keyboard in its question
    /// field; shown there, it folds.
    func toggleCalendarAssistant() {
        guard mode == .calendar, state.assistant.panelShown else { return }
        let page = calendarPage
        if page.assistantShown {
            page.hideAssistant()
            return
        }
        guard let panel = split.assistantContainer.content else { return }
        state.assistant.refresh()
        page.showAssistant(panel)
        assistantPanel?.focusInput()
    }

    /// The panel goes back to the mail's split view (the window leaves the
    /// Calendar).
    func returnAssistantToMail() {
        guard let panel = calendarExtras.page?.releaseAssistant() else { return }
        split.assistantContainer.install(panel)
    }

    /// The panel may be shown or not any more (the Assistant shown, In App
    /// chosen): ✦ follows, and a panel no longer allowed folds.
    func calendarAssistantAllowed(_ allowed: Bool) {
        calendarExtras.toolbar?.setAssistant(visible: allowed)
        if !allowed {
            calendarExtras.page?.hideAssistant()
        }
    }
}
