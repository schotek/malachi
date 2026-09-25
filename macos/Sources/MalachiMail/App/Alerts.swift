// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The confirmation dialogs as `NSAlert` sheets (widget/rpc.go
/// `ConfirmDestructive`, compose/draft.go, window/remote.go). NSAlert puts
/// the first button on the right; a destructive dialog leads with Cancel,
/// which is the default (Return, as the GTK dialog's default response) and
/// takes Escape as well (its close response; AppKit gives Escape only to a
/// button titled "Cancel" in English, so a monitor does it here for a
/// translated title), and the destructive button follows with
/// `hasDestructiveAction` and no key. The draft question keeps Save Draft
/// as its Return default.
@MainActor
final class AppAlerts: Alerts {
    func confirmDestructive(on window: NSWindow?, heading: String, body: String, confirmLabel: String) async -> Bool {
        let (alert, cancel) = destructiveAlert(heading: heading, body: body, confirmLabel: confirmLabel)
        return await run(alert, on: window, escape: cancel) == .alertSecondButtonReturn
    }

    func confirmDestructiveExtra(
        on window: NSWindow?, heading: String, body: String, confirmLabel: String,
        extraLabel: String, extraDefault: Bool
    ) async -> (confirmed: Bool, extra: Bool) {
        let (alert, cancel) = destructiveAlert(heading: heading, body: body, confirmLabel: confirmLabel)
        let check = NSButton(checkboxWithTitle: extraLabel, target: nil, action: nil)
        check.state = extraDefault ? .on : .off
        check.sizeToFit()
        alert.accessoryView = check
        let response = await run(alert, on: window, escape: cancel)
        return (response == .alertSecondButtonReturn, check.state == .on)
    }

    func saveDraftQuestion(on window: NSWindow?) async -> SaveDraftAnswer {
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = L10n.T("Save changes to this draft?")
        let save = alert.addButton(withTitle: mn(L10n.T("_Save Draft")))
        save.keyEquivalent = "\r"
        let cancel = alert.addButton(withTitle: mn(L10n.T("_Cancel")))
        cancel.keyEquivalent = "\u{1b}"
        let discard = alert.addButton(withTitle: mn(L10n.T("_Discard")))
        discard.hasDestructiveAction = true
        discard.keyEquivalent = ""
        switch await run(alert, on: window) {
        case .alertFirstButtonReturn:
            return .save
        case .alertThirdButtonReturn:
            return .discard
        default:
            return .cancel
        }
    }

    func openLinkQuestion(on window: NSWindow?, text: String, href: String) async -> Bool {
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = L10n.T("Open This Link?")
        // TRANSLATORS: %s are the link's visible text and its real destination.
        alert.informativeText = L10n.T("The link is shown as “%s” but leads to %s.", text, href)
        let cancel = alert.addButton(withTitle: mn(L10n.T("_Cancel")))
        cancel.keyEquivalent = "\u{1b}"
        let open = alert.addButton(withTitle: mn(L10n.T("_Open Link")))
        open.keyEquivalent = ""
        return await run(alert, on: window) == .alertSecondButtonReturn
    }

    // MARK: Internals

    /// The alert and its Cancel button (the default; Escape is added by
    /// `run(_:on:escape:)`).
    private func destructiveAlert(heading: String, body: String, confirmLabel: String) -> (NSAlert, NSButton) {
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = heading
        alert.informativeText = body
        let cancel = alert.addButton(withTitle: mn(L10n.T("_Cancel")))
        cancel.keyEquivalent = "\r"
        let confirm = alert.addButton(withTitle: mn(confirmLabel))
        confirm.hasDestructiveAction = true
        confirm.keyEquivalent = ""
        return (alert, cancel)
    }

    /// A sheet on `window`, or application-modal without one (or when the
    /// window already carries a sheet, where a second one would queue up
    /// invisibly).
    private func run(_ alert: NSAlert, on window: NSWindow?) async -> NSApplication.ModalResponse {
        if let window, window.isVisible, window.attachedSheet == nil {
            return await alert.beginSheetModal(for: window)
        }
        return alert.runModal()
    }

    /// `run`, with Escape clicking `escape` while the alert is up (a button
    /// carries one key equivalent, and Cancel's is Return). The monitor
    /// sees only unmodified Escape in the alert's own window.
    private func run(_ alert: NSAlert, on window: NSWindow?, escape button: NSButton) async -> NSApplication.ModalResponse {
        let panel = alert.window
        let monitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak panel, weak button] event in
            // The monitor's closure is called on the main thread by AppKit.
            let consumed = MainActor.assumeIsolated {
                guard let panel, let button, event.window === panel, event.keyCode == Self.escapeKeyCode,
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
        }
        return await run(alert, on: window)
    }

    private static let escapeKeyCode: UInt16 = 53
}
