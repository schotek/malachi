// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import os

/// The AppKit side of ui/internal/compose/manager.go: owns the
/// `ComposeController`, makes the windows it asks for, and plugs the
/// compose entry points into the application (`hooks.composeNew`,
/// `hooks.openMailto`, `hooks.openCompose`, notify.accountsChanged). Every
/// `open` is a new window, as in GTK.
@MainActor
final class ComposeManager {
    let state: AppState
    let controller: ComposeController

    private let makeEditor: @MainActor () -> any EditorView
    private var token: NotificationHub.Token?
    private var cascadePoint = NSPoint.zero
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "compose")

    /// - Parameters:
    ///   - state: the application state.
    ///   - makeEditor: builds the formatted-text editor of a new window
    ///     (`ComposeEditorView()`).
    init(state: AppState, makeEditor: @escaping @MainActor () -> any EditorView) {
        self.state = state
        self.makeEditor = makeEditor
        controller = ComposeController(client: state.client, settings: state.settings)
        controller.makeWindow = { [unowned self] p in
            self.newWindow(p)
        }
        controller.onSent = { [weak state] text in
            // A short confirmation on the main window: the outbox folder
            // and the "sent" toast that follows carry the rest (ui/main.go).
            guard let state else { return }
            if let main = state.mainWindow as? MainWindowController {
                main.toasts.show(text, seconds: 2)
            } else {
                state.toasts.show(text, seconds: 2)
            }
        }
    }

    /// Registers the compose entry points with the application state and
    /// follows notify.accountsChanged (`Manager.Invalidate`).
    func install(into state: AppState) {
        state.hooks.composeNew = { [weak self] in
            self?.open(ComposeParams(kind: .new))
        }
        state.hooks.openMailto = { [weak self] url in
            self?.openMailto(url)
        }
        state.hooks.openCompose = { [weak self] p in
            self?.open(p)
        }
        token = state.notifications.addAccountsChanged { [weak self] in
            self?.controller.invalidate()
        }
    }

    /// Manager.Open: a new compose window prefilled from `p`.
    func open(_ p: ComposeParams) {
        controller.open(p)
    }

    /// The `open` signal of ui/main.go: the UI only splits the URI
    /// (`parseMailto`); everything else about the message is the
    /// backend's business.
    func openMailto(_ url: URL) {
        do {
            open(try parseMailto(url.absoluteString))
        } catch {
            log.warning("ignoring non-mailto URI: \(String(describing: error), privacy: .public)")
        }
    }

    /// Manager.remove: a window closed.
    func remove(_ wc: ComposeWindowController) {
        controller.remove(wc)
    }

    /// compose.newWindow + Present.
    private func newWindow(_ p: ComposeParams) -> ComposeWindowController {
        let wc = ComposeWindowController(state: state, manager: self, params: p, editor: makeEditor())
        place(wc)
        wc.showWindow(nil)
        wc.window?.makeKeyAndOrderFront(nil)
        NSApp.activate()
        return wc
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
