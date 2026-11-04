// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// Closes a window on Escape, the way the GTK message, embedded and compose
/// windows do with a window-level shortcut. A local event monitor sees the
/// key before the view hierarchy does, which is what a window whose first
/// responder is a WKWebView needs: the web view would swallow the event.
///
/// The key is passed on (and nothing closes) while a sheet, a modal panel
/// or something the owner vetoes through `shouldClose` is up; menu key
/// equivalents are matched by AppKit before the monitor sees anything.
@MainActor
final class EscapeCloser {
    private static let escapeKeyCode: UInt16 = 53

    private weak var window: NSWindow?
    private var monitor: Any?

    /// Vetoes the close (the compose window's recipient suggestions are
    /// showing, say). Default: always close.
    var shouldClose: (@MainActor () -> Bool)?

    private init(window: NSWindow) {
        self.window = window
    }

    /// Installs the monitor for `window`. Keep the returned object alive as
    /// long as the window; call `uninstall()` from `windowWillClose`.
    static func install(on window: NSWindow) -> EscapeCloser {
        let closer = EscapeCloser(window: window)
        closer.monitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak closer] event in
            // The monitor's closure is called on the main thread by AppKit.
            let consumed = MainActor.assumeIsolated {
                closer?.handle(event) ?? false
            }
            return consumed ? nil : event
        }
        return closer
    }

    /// Removes the monitor; the closer is inert afterwards.
    func uninstall() {
        if let monitor {
            NSEvent.removeMonitor(monitor)
        }
        monitor = nil
    }

    /// True when the event was Escape for this window and the window is
    /// closing (the event is then swallowed).
    private func handle(_ event: NSEvent) -> Bool {
        guard let window,
              event.keyCode == Self.escapeKeyCode,
              event.modifierFlags.intersection(.deviceIndependentFlagsMask).subtracting(.function).isEmpty,
              event.window === window,
              window.attachedSheet == nil,
              NSApp.modalWindow == nil,
              shouldClose?() ?? true
        else {
            return false
        }
        window.performClose(nil)
        return true
    }
}
