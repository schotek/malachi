// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The event detail popover (docs/calendar.md §5.5, artboard 3): an
/// `NSPopover` (transient, 380 pt wide) anchored to the event's block,
/// month pill or agenda row, with an `EventDetailViewController` over an
/// `EventDetailController`. One per application: showing another closes the
/// one open.
///
/// The popover presents the controller's questions as sheets on its
/// window (the main window), keeping itself open meanwhile, and does its
/// hand-offs: Edit and Find Another Time to `EventEditorManager`, Join
/// Video Call and the notes' links to the actions' `openLink` with no
/// listed links (so "Open This Link?" shows the destination), Show
/// Invitation in Mail to Mail with the message revealed (the board's
/// `boardMail.reveal`), Ask about This Event to the window's assistant
/// panel, moved into the Calendar page, with a question naming the event's
/// id in its field.
@MainActor
enum EventDetailPopover {
    /// The open popover.
    private static var current: Presenter?

    /// Shows the detail of the occurrence `eventID` / `recurrenceID`
    /// (nil: a single event or the series) anchored to `rect` in `view`'s
    /// coordinates. The window's `CalendarController` is
    /// `(view.window?.windowController as? MainWindowController)?.calendarController`.
    static func show(eventID: EventID, recurrenceID: DateOrTime?, relativeTo rect: NSRect, of view: NSView) {
        let wc = view.window?.windowController as? MainWindowController
        guard let wc else { return }
        let calendar = wc.calendarController
        let shown = calendar.selectedOccurrence.flatMap { o in
            o.eventId == eventID && o.recurrenceId == recurrenceID ? o : nil
        }
        let settings = wc.state.settings
        let c = EventDetailController(
            source: calendar.source, eventId: eventID, recurrenceId: recurrenceID, occurrence: shown, env: calendar.env,
            confirmDelete: { settings.confirmDelete })
        present(c, window: wc, relativeTo: rect, of: view)
    }

    /// Shows the detail of occurrence `o` (one with an event id; an
    /// invitation not in a calendar shows nothing) anchored to `rect` in
    /// `view`'s coordinates. The occurrence's title and time show while the
    /// event loads.
    static func show(occurrence o: Occurrence, relativeTo rect: NSRect, of view: NSView) {
        guard let wc = view.window?.windowController as? MainWindowController else { return }
        let calendar = wc.calendarController
        let settings = wc.state.settings
        guard let c = EventDetailController(
            source: calendar.source, occurrence: o, env: calendar.env, confirmDelete: { settings.confirmDelete })
        else { return }
        present(c, window: wc, relativeTo: rect, of: view)
    }

    /// Whether a detail popover is open (Escape's `Cal.escapeFor`).
    static var isShown: Bool { current?.popover.isShown ?? false }

    /// Closes the open popover.
    static func close() {
        current?.popover.performClose(nil)
    }

    private static func present(
        _ c: EventDetailController, window wc: MainWindowController, relativeTo rect: NSRect, of view: NSView
    ) {
        current?.popover.close()
        let p = Presenter(controller: c, window: wc)
        current = p
        p.show(relativeTo: rect, of: view)
    }

    fileprivate static func closed(_ p: Presenter) {
        if current === p {
            current = nil
        }
    }

    // MARK: The presenter

    /// One open popover: its controller's questions and hand-offs.
    @MainActor
    fileprivate final class Presenter: NSObject, NSPopoverDelegate {
        let popover = NSPopover()
        let controller: EventDetailController
        let viewController: EventDetailViewController
        private weak var mainWindow: MainWindowController?
        /// The next Edit was Find Another Time (the editor opens on Next
        /// Free Slot).
        private var findingTime = false

        init(controller: EventDetailController, window: MainWindowController) {
            self.controller = controller
            self.mainWindow = window
            viewController = EventDetailViewController(
                controller: controller, snapshot: { [weak window] in window?.calendarController.source.snapshot },
                canAsk: { [weak window] in window?.state.assistant.panelShown ?? false })
            super.init()
            popover.behavior = .transient
            popover.animates = true
            popover.contentViewController = viewController
            popover.delegate = self
            wire()
        }

        func show(relativeTo rect: NSRect, of view: NSView) {
            controller.start()
            viewController.update()
            popover.show(relativeTo: rect, of: view, preferredEdge: .maxX)
        }

        private func wire() {
            let c = controller
            c.onChange = { [weak self] in
                self?.viewController.update()
            }
            c.onQuestion = { [weak self] q in
                await self?.ask(q) ?? .cancel
            }
            c.onScope = { [weak self] q in
                await self?.askScope(q)
            }
            c.onClose = { [weak self] in
                self?.popover.performClose(nil)
            }
            c.onEdit = { [weak self] event, scope in
                guard let self else { return }
                let edit = EventEditorManager.Edit(event: event, scope: scope, findFreeSlot: self.findingTime)
                self.findingTime = false
                self.popover.performClose(nil)
                EventEditorManager.shared.openEdit(edit)
            }
            c.onOpenLink = { [weak self] url in
                guard let self, let wc = self.mainWindow else { return }
                let window = wc.window
                self.popover.performClose(nil)
                (wc.messageActions as? any MessageActionDelegate)?.openLink(url, links: [], from: window)
            }
            c.onShowInvitation = { [weak self] ref in
                guard let self else { return }
                self.popover.performClose(nil)
                self.mainWindow?.showInvitationInMail(ref)
            }
            c.onAsk = { [weak self] event in
                guard let self else { return }
                self.popover.performClose(nil)
                self.mainWindow?.askAssistant(about: event)
            }
            viewController.onEdit = { [weak self] in
                self?.findingTime = false
                self?.controller.edit()
            }
            viewController.onFindAnotherTime = { [weak self] in
                // The editor's Next Free Slot: Edit, with the series question
                // first, and `Edit.findFreeSlot`.
                self?.findingTime = true
                self?.controller.edit()
            }
            viewController.onClose = { [weak self] in
                self?.popover.performClose(nil)
            }
        }

        // MARK: Questions

        /// The window that carries the questions: the main window.
        private var sheetWindow: NSWindow? { mainWindow?.window }

        /// A question of the controller as a sheet: Cancel leads (the
        /// default, Escape too), the confirmation follows as the destructive
        /// button, an alternative last. The popover stays open meanwhile.
        private func ask(_ q: Cal.Question) async -> Cal.Answer {
            let alert = NSAlert()
            alert.alertStyle = .warning
            alert.messageText = q.heading
            alert.informativeText = q.body
            let cancel = alert.addButton(withTitle: mn(L10n.T("_Cancel")))
            cancel.keyEquivalent = "\r"
            let confirm = alert.addButton(withTitle: mn(q.confirm))
            confirm.hasDestructiveAction = true
            confirm.keyEquivalent = ""
            if !q.alternative.isEmpty {
                let alt = alert.addButton(withTitle: mn(q.alternative))
                alt.keyEquivalent = ""
            }
            switch await run(alert, escape: cancel) {
            case .alertSecondButtonReturn: return .confirm
            case .alertThirdButtonReturn: return .alternative
            default: return .cancel
            }
        }

        /// The series question: Cancel leads, then the choices in their
        /// order (This Event Only, This and Future Events, All Events).
        private func askScope(_ q: Cal.ScopeQuestion) async -> EditScope? {
            let alert = NSAlert()
            alert.alertStyle = .informational
            alert.messageText = q.heading
            alert.informativeText = q.body
            let cancel = alert.addButton(withTitle: mn(L10n.T("_Cancel")))
            cancel.keyEquivalent = "\u{1b}"
            for (i, choice) in q.choices.enumerated() {
                let b = alert.addButton(withTitle: choice.title)
                b.keyEquivalent = i == 0 ? "\r" : ""
            }
            let r = await run(alert, escape: cancel)
            let i = r.rawValue - NSApplication.ModalResponse.alertSecondButtonReturn.rawValue
            return q.choices.indices.contains(i) ? q.choices[i].scope : nil
        }

        /// A sheet on the main window (application-modal without one), the
        /// popover kept open while it is up; Escape clicks `escape` (a
        /// button carries one key equivalent).
        private func run(_ alert: NSAlert, escape button: NSButton) async -> NSApplication.ModalResponse {
            popover.behavior = .applicationDefined
            let panel = alert.window
            let monitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak panel, weak button] event in
                let consumed = MainActor.assumeIsolated {
                    guard let panel, let button, event.window === panel, event.keyCode == 53,
                          event.modifierFlags.intersection(.deviceIndependentFlagsMask).subtracting(.function).isEmpty
                    else {
                        return false
                    }
                    button.performClick(nil)
                    return true
                }
                return consumed ? nil : event
            }
            defer {
                if let monitor {
                    NSEvent.removeMonitor(monitor)
                }
                popover.behavior = .transient
                if popover.isShown {
                    viewController.takeKeyboard()
                }
            }
            if let window = sheetWindow, window.isVisible, window.attachedSheet == nil {
                return await alert.beginSheetModal(for: window)
            }
            return alert.runModal()
        }

        // MARK: NSPopoverDelegate

        func popoverDidShow(_ notification: Notification) {
            viewController.takeKeyboard()
        }

        func popoverDidClose(_ notification: Notification) {
            controller.stop()
            EventDetailPopover.closed(self)
        }
    }
}

// MARK: The window's hand-offs

extension MainWindowController {
    /// Show Invitation in Mail: the window goes to Mail and the list
    /// selects the invitation in its folder (as the board's Show in Mail),
    /// or the message opens in its own window when the list cannot show it.
    func showInvitationInMail(_ ref: MessageRef) {
        guard let reveal = boardMail.reveal else { return }
        setMode(.mail)
        // A refused change (a field that keeps the keyboard) stays.
        guard mode == .mail else { return }
        reveal(ref.accountId, ref.messageId, nil, nil) { [weak self] in
            guard let self, self.mode == .mail, self.split.isListCollapsed else { return }
            self.split.toggleMessageList(nil)
        }
    }

    /// Ask about This Event: the assistant panel unfolds in the Calendar
    /// page with a question naming the event in its field, for the user to
    /// finish and send (the panel's conversation reads it through the
    /// bridge's `get_event`).
    func askAssistant(about event: Event) {
        guard mode == .calendar, state.assistant.panelShown, let id = event.eventId else { return }
        if !calendarPage.assistantShown {
            toggleCalendarAssistant()
        }
        guard let panel = assistantPanel else { return }
        panel.input.text = Assistant.askAboutEventPrompt(eventID: id.rawValue, recurrenceID: event.recurrenceId?.wire ?? "")
        panel.focusInput()
    }
}
