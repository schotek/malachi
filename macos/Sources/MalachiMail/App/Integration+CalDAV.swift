// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The application's entry to the Add CalDAV Calendar sheet
/// (docs/calendar.md §5.8): Settings → Accounts "+", the inset of an
/// account without a calendar, the Calendar sidebar's Add Calendar… and
/// any menu item call `showAddCalDAVCalendar(from:)`. The sheet works over
/// the calendar source of the window it opens on (its snapshot lists the
/// mail accounts invitations can leave from); the sidebars follow
/// notify.accountsChanged after account.add.
extension AppState {
    /// The running application's state (nil in tests and before launch).
    static var current: AppState? {
        (NSApp.delegate as? AppDelegate)?.state
    }

    /// The calendar source for a window controller that is not a main
    /// window (the settings), routed like a main window's and started.
    func startedCalendarSource(for window: NSWindowController) -> any CalendarDataSource {
        let source = calendarSource(for: window)
        (source as? DaemonCalendarSource)?.start()
        return source
    }

    /// Opens the Add CalDAV Calendar sheet on `window` (nil: the main
    /// window, else the key window). `onDone` runs after account.add.
    func showAddCalDAVCalendar(from window: NSWindow?, onDone: @escaping @MainActor (AccountID) -> Void = { _ in }) {
        guard let parent = window ?? mainWindow?.window ?? NSApp.keyWindow else { return }
        guard parent.attachedSheet == nil else { return }
        let owner = parent.windowController ?? mainWindow
        let source: any CalendarDataSource
        if let owner {
            source = startedCalendarSource(for: owner)
        } else {
            let own = DaemonCalendarSource(client: client)
            own.start()
            source = own
        }
        CalDAVSheetController.present(
            from: parent, source: source, confirmTrust: Integration.confirmTrust(alerts), onDone: onDone)
    }

    /// Opens the settings on their Calendar page (the calendar page's
    /// empty state, the sidebar's Sync a Microsoft 365 Account's Calendars…).
    func showCalendarPreferences() {
        hooks.openPreferences?()
        PreferencesWindowController.open?.select(.calendar)
    }
}
