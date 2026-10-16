// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// One New Event / Edit Event window (docs/calendar.md §5.6, artboard 5;
/// 760×800): the toolbar with Attach and ✦ (Find a Free Slot, Ask the
/// Assistant…), the form (`EventEditorViewController`) over
/// `EventEditorController`. Closing with changes asks Save / Discard /
/// Cancel (`Cal.closeQuestion`); nothing is stored before Save (D6), so
/// Discard just closes. A save closes the window; the calendar refreshes by
/// the daemon's notification. The menu bar's Save (⌘S, only without
/// attendees), Send (⌘↩, the primary button) and Attach Files reach the
/// window through the responder chain (`MalachiActions`).
@MainActor
final class EventEditorWindowController: NSWindowController, NSWindowDelegate {
    static let defaultSize = NSSize(width: 760, height: 800)
    static let minimumSize = NSSize(width: 560, height: 520)
    static let toolbarIdentifier = NSToolbar.Identifier("eventEditor")

    let state: AppState
    unowned let manager: EventEditorManager
    let controller: EventEditorController
    let content: EventEditorViewController
    private let toolbarDelegate: EventEditorToolbar

    private var escape: EscapeCloser?
    /// The window may go without asking.
    private var closing = false
    /// The close question is up.
    private var closeQuestionPending = false

    init(state: AppState, manager: EventEditorManager, controller: EventEditorController) {
        self.state = state
        self.manager = manager
        self.controller = controller
        content = EventEditorViewController(state: state, controller: controller)
        toolbarDelegate = EventEditorToolbar()

        let w = NSWindow(
            contentRect: NSRect(origin: .zero, size: Self.defaultSize),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered, defer: false)
        w.title = controller.windowTitle
        w.minSize = Self.minimumSize
        w.toolbarStyle = .unified
        w.tabbingMode = .disallowed
        w.isReleasedWhenClosed = false
        super.init(window: w)

        w.delegate = self
        toolbarDelegate.assistantMenu.delegate = self
        w.toolbar = toolbarDelegate.makeToolbar()
        w.contentViewController = content
        w.setFrame(NSRect(origin: .zero, size: Self.defaultSize), display: false)
        w.initialFirstResponder = content.titleField
        w.autorecalculatesKeyViewLoop = true
        w.defaultButtonCell = content.defaultButtonCell

        content.onCancel = { [weak self] in self?.window?.performClose(nil) }
        content.onReload = { [weak self] in
            guard let self else { return }
            self.manager.reload(self)
        }
        controller.onSaved = { [weak self] _ in
            self?.closeUnasked()
        }
        controller.onClose = { [weak self] in
            self?.closeUnasked()
        }
        controller.onQuestion = { [weak self] q in
            await self?.ask(q) ?? .cancel
        }
        controller.onDeleteQuestion = { [weak self] q in
            await self?.askDelete(q) ?? .cancel
        }
        controller.confirmDelete = { [weak state] in state?.settings.confirmDelete ?? true }
        escape = EscapeCloser.install(on: w)
        escape?.shouldClose = { [weak self] in
            self?.content.popupsHidden ?? true
        }
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Whether this window edits `event` with `scope` (the manager raises
    /// it instead of opening another).
    func edits(_ event: Event, scope: EditScope) -> Bool {
        guard case .edit(let e, let s) = controller.mode, let id = e.eventId else { return false }
        return id == event.eventId && s == scope && (scope == .all || e.recurrenceId == event.recurrenceId)
    }

    func present() {
        showWindow(nil)
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate()
    }

    /// The controller decided (saved, discarded): the window goes.
    func closeUnasked() {
        closing = true
        window?.close()
    }

    /// Save / Discard / Cancel as a sheet: Save is the Return default,
    /// Cancel takes Escape, Discard is destructive (the order of the draft
    /// question, `AppAlerts.saveDraftQuestion`).
    private func ask(_ q: Cal.Question) async -> Cal.Answer {
        guard let window else { return .cancel }
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = q.heading
        alert.informativeText = q.body
        let confirm = alert.addButton(withTitle: q.confirm)
        confirm.keyEquivalent = "\r"
        let cancel = alert.addButton(withTitle: Cal.cancel)
        cancel.keyEquivalent = "\u{1b}"
        if !q.alternative.isEmpty {
            let alternative = alert.addButton(withTitle: q.alternative)
            alternative.hasDestructiveAction = true
            alternative.keyEquivalent = ""
        }
        let response = await withCheckedContinuation { (cont: CheckedContinuation<NSApplication.ModalResponse, Never>) in
            alert.beginSheetModal(for: window) { cont.resume(returning: $0) }
        }
        switch response {
        case .alertFirstButtonReturn: return .confirm
        case .alertThirdButtonReturn: return .alternative
        default: return .cancel
        }
    }

    /// A Delete question as a sheet: Cancel the Return default and Escape,
    /// the confirmation destructive, the alternative (Remove Only) after it.
    private func askDelete(_ q: Cal.Question) async -> Cal.Answer {
        guard let window else { return .cancel }
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = q.heading
        alert.informativeText = q.body
        let cancel = alert.addButton(withTitle: Cal.cancel)
        cancel.keyEquivalent = "\r"
        let confirm = alert.addButton(withTitle: q.confirm)
        confirm.hasDestructiveAction = true
        confirm.keyEquivalent = ""
        if !q.alternative.isEmpty {
            let alternative = alert.addButton(withTitle: q.alternative)
            alternative.keyEquivalent = ""
        }
        // Escape is Cancel (AppKit gives it only to a button titled
        // "Cancel" in English).
        let escape = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak alert, weak window] event in
            let consumed = MainActor.assumeIsolated { () -> Bool in
                guard event.keyCode == 53, let alert, let window, event.window === alert.window else { return false }
                window.endSheet(alert.window, returnCode: .alertFirstButtonReturn)
                return true
            }
            return consumed ? nil : event
        }
        let response = await withCheckedContinuation { (cont: CheckedContinuation<NSApplication.ModalResponse, Never>) in
            alert.beginSheetModal(for: window) { cont.resume(returning: $0) }
        }
        if let escape {
            NSEvent.removeMonitor(escape)
        }
        switch response {
        case .alertSecondButtonReturn: return .confirm
        case .alertThirdButtonReturn: return .alternative
        default: return .cancel
        }
    }

    // MARK: Actions

    /// ✦ Find a Free Slot (and the detail's Find Another Time).
    @objc func findFreeSlot(_ sender: Any?) {
        controller.nextFreeSlot()
    }

    /// ✦ Ask the Assistant…: the main window's assistant panel, where the
    /// window shows it (the Calendar's page or Mail's split view).
    @objc func askAssistantAboutEvent(_ sender: Any?) {
        guard state.assistant.panelShown, let main = state.mainWindow as? MainWindowController else { return }
        main.showWindow(nil)
        main.window?.makeKeyAndOrderFront(nil)
        if main.mode == .calendar {
            if !main.calendarPage.assistantShown {
                main.toggleCalendarAssistant()
            }
        } else {
            main.revealAssistant()
        }
    }

    // MARK: NSWindowDelegate

    /// The primary button is the default one whenever the window is key
    /// (a sheet or popover may have taken Return meanwhile).
    func windowDidBecomeKey(_ notification: Foundation.Notification) {
        window?.defaultButtonCell = content.defaultButtonCell
    }

    func windowShouldClose(_ sender: NSWindow) -> Bool {
        if closing || !controller.dirty {
            return true
        }
        guard !closeQuestionPending else { return false }
        closeQuestionPending = true
        Task { [weak self] in
            guard let self else { return }
            // `close()` calls `onClose` (→ closeUnasked) when allowed.
            _ = await self.controller.close()
            self.closeQuestionPending = false
        }
        return false
    }

    func windowWillClose(_ notification: Foundation.Notification) {
        closing = true
        escape?.uninstall()
        escape = nil
        controller.stop()
        content.cleanup()
        manager.remove(self)
    }
}

// MARK: - The menu bar's Save, Send and Attach

extension EventEditorWindowController: MalachiActions {
    /// ⌘↩: the primary button (Save / Save and Send Invitations).
    @objc func sendMessage(_ sender: Any?) { content.save() }
    /// ⌘S: Save, only while the event has no attendees.
    @objc func saveDraft(_ sender: Any?) {
        guard controller.saveKeyAllowed else { return }
        content.save()
    }
    @objc func attachFiles(_ sender: Any?) { content.attachFiles() }
}

extension EventEditorWindowController: NSUserInterfaceValidations, NSToolbarItemValidation {
    func validateUserInterfaceItem(_ item: any NSValidatedUserInterfaceItem) -> Bool {
        switch item.action {
        case Action.sendMessage?: return !controller.saving
        case Action.saveDraft?: return !controller.saving && controller.saveKeyAllowed
        case Action.attachFiles?: return controller.attachments.count < API.Limits.maxEventAttachments
        case #selector(findFreeSlot(_:))?: return !controller.input.allDay && !controller.calendars.isEmpty
        case #selector(askAssistantAboutEvent(_:))?: return state.assistant.panelShown
        default: return true
        }
    }

    func validateToolbarItem(_ item: NSToolbarItem) -> Bool {
        guard let action = item.action else { return true }
        if action == Action.attachFiles {
            return controller.attachments.count < API.Limits.maxEventAttachments
        }
        return true
    }
}

extension EventEditorWindowController: NSMenuDelegate {
    /// ✦'s menu: Ask the Assistant… only while the assistant panel exists.
    func menuNeedsUpdate(_ menu: NSMenu) {
        toolbarDelegate.askItem.isHidden = !state.assistant.panelShown
    }
}

// MARK: - Toolbar

/// Attach and ✦ (Find a Free Slot; Ask the Assistant…).
@MainActor
private final class EventEditorToolbar: NSObject, NSToolbarDelegate {
    enum ID {
        static let attach = NSToolbarItem.Identifier("eventEditorAttach")
        static let assistant = NSToolbarItem.Identifier("eventEditorAssistant")
    }

    static let items: [NSToolbarItem.Identifier] = [.flexibleSpace, ID.attach, ID.assistant]

    let assistantMenu = NSMenu()
    let askItem: NSMenuItem

    override init() {
        assistantMenu.addItem(
            withTitle: L10n.T("Find a Free Slot"), action: #selector(EventEditorWindowController.findFreeSlot(_:)),
            keyEquivalent: "")
        askItem = assistantMenu.addItem(
            withTitle: L10n.T("Ask the Assistant…"),
            action: #selector(EventEditorWindowController.askAssistantAboutEvent(_:)), keyEquivalent: "")
        super.init()
    }

    func makeToolbar() -> NSToolbar {
        let tb = NSToolbar(identifier: EventEditorWindowController.toolbarIdentifier)
        tb.delegate = self
        tb.displayMode = .iconOnly
        tb.allowsUserCustomization = false
        tb.autosavesConfiguration = false
        return tb
    }

    func toolbarDefaultItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] { Self.items }
    func toolbarAllowedItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] { Self.items }

    func toolbar(
        _ toolbar: NSToolbar, itemForItemIdentifier id: NSToolbarItem.Identifier, willBeInsertedIntoToolbar flag: Bool
    ) -> NSToolbarItem? {
        switch id {
        case ID.attach:
            let it = NSToolbarItem(itemIdentifier: id)
            it.image = Icon.image("mail-attachment", size: .toolbar)
            it.label = Cal.attach
            it.paletteLabel = it.label
            it.toolTip = it.label
            it.isBordered = true
            it.target = nil
            it.action = Action.attachFiles
            return it
        case ID.assistant:
            let label = Assistant.texts().assistant
            let it = NSMenuToolbarItem(itemIdentifier: id)
            it.image = Icon.symbol("sparkles", size: .toolbar, description: label)
            it.label = label
            it.paletteLabel = label
            it.toolTip = label
            it.showsIndicator = true
            it.isBordered = true
            it.menu = assistantMenu
            return it
        default:
            return nil
        }
    }
}
