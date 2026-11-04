// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// One window per key: a message window per message id, an embedded
/// message window per (message, part), a compose window per draft. A
/// second request for the same key brings the existing window to the front
/// instead of opening another (window.go `openMessages`/`openEmbedded`).
/// Windows unregister themselves when they close.
@MainActor
final class WindowRegistry: NSObject {
    private var controllers: [AnyHashable: NSWindowController] = [:]

    /// Registers `wc` under `key`; a controller already there is replaced
    /// (and left open). The registration ends when the window closes.
    func register(_ wc: NSWindowController, key: AnyHashable) {
        if let old = controllers[key], old !== wc, let w = old.window {
            NotificationCenter.default.removeObserver(self, name: NSWindow.willCloseNotification, object: w)
        }
        controllers[key] = wc
        if let w = wc.window {
            NotificationCenter.default.addObserver(
                self, selector: #selector(windowWillClose(_:)), name: NSWindow.willCloseNotification, object: w)
        }
    }

    func window(for key: AnyHashable) -> NSWindowController? {
        controllers[key]
    }

    /// Brings the window for `key` to the front. False when there is none.
    @discardableResult
    func present(key: AnyHashable) -> Bool {
        guard let wc = controllers[key] else { return false }
        wc.showWindow(nil)
        wc.window?.makeKeyAndOrderFront(nil)
        return true
    }

    /// Closes the window for `key`, if any.
    func close(key: AnyHashable) {
        controllers[key]?.close()
        unregister(key: key)
    }

    /// Closes every window whose key matches.
    func closeAll(where matches: (AnyHashable) -> Bool) {
        for key in controllers.keys where matches(key) {
            close(key: key)
        }
    }

    /// Forgets `key` without closing its window.
    func unregister(key: AnyHashable) {
        guard let wc = controllers.removeValue(forKey: key) else { return }
        if let w = wc.window {
            NotificationCenter.default.removeObserver(self, name: NSWindow.willCloseNotification, object: w)
        }
    }

    /// The keys of the open windows.
    var keys: [AnyHashable] {
        Array(controllers.keys)
    }

    @objc private func windowWillClose(_ note: Foundation.Notification) {
        guard let w = note.object as? NSWindow else { return }
        for (key, wc) in controllers where wc.window === w {
            unregister(key: key)
        }
    }
}
