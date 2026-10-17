// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The New Event / Edit Event windows (docs/calendar.md §5.6, artboard 5),
/// a registry like `ComposeManager`: every `openNew` is a new window; an
/// event already being edited (same event, occurrence and scope) raises its
/// window instead. The windows work over the main window's calendar source
/// (`AppState.calendarSource(for:)`), so a save lands in the caches the
/// views read and the views refresh by the daemon's notification.
/// `state` is set by `AppState` when it is made.
@MainActor
final class EventEditorManager {
    static let shared = EventEditorManager()

    /// The application (its client, settings, windows), set at start.
    weak var state: AppState?

    /// An existing event to edit and what the change applies to (a
    /// recurring event's scope, `EventDetailController.onEdit`).
    struct Edit {
        var event: Event
        var scope: EditScope
        /// ✦ Find Another Time of the detail: Next Free Slot runs as soon
        /// as the window is open.
        var findFreeSlot = false
    }

    /// How long a window waits for a source that has not loaded yet (New
    /// Event from Mail before the Calendar was ever shown).
    private static let loadWait: Duration = .seconds(5)

    private var windows: [EventEditorWindowController] = []
    private var cascadePoint = NSPoint.zero

    /// Opens a New Event window with `defaults` (`CalendarController
    /// .newEventDefaults`, File ▸ New Event, ⌥⌘N, the toolbar's +).
    func openNew(_ defaults: Cal.Defaults) {
        open(mode: .new, defaults: defaults, findFreeSlot: false, frame: nil)
    }

    /// Opens an Edit Event window (or brings the one editing it forward).
    func openEdit(_ edit: Edit) {
        if let w = windows.first(where: { $0.edits(edit.event, scope: edit.scope) }) {
            w.present()
            if edit.findFreeSlot {
                w.findFreeSlot(nil)
            }
            return
        }
        open(mode: .edit(edit.event, edit.scope), defaults: nil, findFreeSlot: edit.findFreeSlot, frame: nil)
    }

    /// A window closed.
    func remove(_ wc: EventEditorWindowController) {
        windows.removeAll { $0 === wc }
    }

    /// Reload after a conflict: the event as the daemon has it now, in a
    /// new window where the old one was (the old one closes unasked; the
    /// user chose to drop the edits).
    func reload(_ wc: EventEditorWindowController) {
        guard case .edit(let e, let scope) = wc.controller.mode, let id = e.eventId else { return }
        let source = wc.controller.source
        let frame = wc.window?.frame
        Task { [weak self, weak wc] in
            guard let fresh = try? await source.event(id, recurrenceId: e.recurrenceId, reload: true) else { return }
            wc?.closeUnasked()
            self?.open(mode: .edit(fresh, scope), defaults: nil, findFreeSlot: false, frame: frame)
        }
    }

    private func open(mode: EventEditorController.Mode, defaults: Cal.Defaults?, findFreeSlot: Bool, frame: NSRect?) {
        guard let state, let main = state.mainWindow else { return }
        let source = state.calendarSource(for: main)
        Task { [weak self] in
            await Self.waitForLoad(source)
            guard let self else { return }
            let controller = EventEditorController(source: source, mode: mode, defaults: defaults)
            let wc = EventEditorWindowController(state: state, manager: self, controller: controller)
            self.windows.append(wc)
            if let frame {
                wc.window?.setFrame(frame, display: false)
            } else {
                self.place(wc)
            }
            wc.present()
            controller.start()
            if findFreeSlot {
                wc.findFreeSlot(nil)
            }
        }
    }

    /// A source that has nothing yet is started and given a moment, so that
    /// the Calendar popup has the writable calendars.
    private static func waitForLoad(_ source: any CalendarDataSource) async {
        guard source.snapshot.phase == .loading else { return }
        if let d = source as? DaemonCalendarSource {
            d.start()
        } else {
            source.refresh()
        }
        var token: CalendarObserverToken?
        await withCheckedContinuation { (cont: CheckedContinuation<Void, Never>) in
            var done = false
            let finish = {
                guard !done else { return }
                done = true
                token?.cancel()
                cont.resume()
            }
            token = source.observe { _ in
                if source.snapshot.phase != .loading {
                    finish()
                }
            }
            Task { @MainActor in
                try? await Task.sleep(for: loadWait)
                finish()
            }
        }
    }

    /// Cascades new windows from the top left, as document windows do.
    private func place(_ wc: NSWindowController) {
        guard let w = wc.window else { return }
        if cascadePoint == .zero {
            w.center()
            cascadePoint = NSPoint(x: w.frame.minX, y: w.frame.maxY)
        }
        cascadePoint = w.cascadeTopLeft(from: cascadePoint)
    }
}
