// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

// Show in Calendar of an invitation card (docs/calendar.md §5.7): the main
// window comes forward in Calendar mode, the Day view of the event's day
// with the event selected, and its detail opens over the content.

extension MainWindowController {
    /// Calendar mode at `day` with the event `eventID` (instance
    /// `recurrenceID`) selected and its detail shown; without an event the
    /// day alone.
    func showInCalendar(eventID: EventID?, recurrenceID: DateOrTime?, day: Date?) {
        window?.makeKeyAndOrderFront(nil)
        setMode(.calendar)
        guard mode == .calendar else { return }
        let key = eventID.map { $0.rawValue + "|" + (recurrenceID?.wire ?? "") }
        calendarController.show(day: day ?? Date(), view: .day, select: key)
        guard let eventID else { return }
        // After the page laid the day out.
        DispatchQueue.main.async { [weak self] in
            guard let self, self.mode == .calendar else { return }
            let anchor = self.calendarPage.contentContainer.view
            let rect = NSRect(x: anchor.bounds.midX - 1, y: anchor.bounds.midY - 1, width: 2, height: 2)
            EventDetailPopover.show(eventID: eventID, recurrenceID: recurrenceID, relativeTo: rect, of: anchor)
        }
    }
}
