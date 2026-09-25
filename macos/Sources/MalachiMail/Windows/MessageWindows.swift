// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import os

/// The message windows of the application and the views that show
/// messages (window.go `openMessages`/`openEmbedded`, message_view.go
/// `openMessageWindow`/`closeMessageWindow`, embedded.go
/// `openEmbeddedWindow`/`closeEmbeddedWindows`, remote.go `showLoaded`/
/// `refreshRemoteBar`, outbox.go `showOutboxState`): one window per
/// message, one per attached message, and the fan-out of what the cache
/// learns to every view showing the message. The hub installs itself as
/// the cache's `onLoaded` and `onRemoteBar`.
///
/// The app sets `delegate` once; it reaches every tracked view and every
/// window, open now or later.
@MainActor
final class MessageWindows {
    /// The registry keys (`WindowRegistry`).
    enum Key: Hashable {
        case message(MessageID)
        case embedded(MessageID, String)
    }

    let state: AppState
    let cache: MessageCache

    /// The actions of every view and window.
    weak var delegate: (any MessageActionDelegate)? {
        didSet {
            for v in views {
                v.delegate = delegate
            }
            for wc in messageWindows {
                wc.delegate = delegate
            }
        }
    }

    private var tracked: [WeakView] = []
    private var cascadePoint = NSPoint.zero
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "windows")

    init(state: AppState, cache: MessageCache) {
        self.state = state
        self.cache = cache
        cache.onLoaded = { [weak self] id, lm in self?.showLoaded(id, lm) }
        cache.onRemoteBar = { [weak self] id, lm in self?.refreshRemoteBar(id, lm) }
    }

    // MARK: Views

    /// The main window's message pane, tracked and wired.
    func makePaneView() -> MessageViewController {
        let v = MessageViewController(state: state, cache: cache, mode: .pane)
        track(v)
        return v
    }

    /// Registers a view for the fan-out and gives it the delegate and the
    /// embedded-window opener. Views are held weakly.
    func track(_ v: MessageViewController) {
        v.delegate = delegate
        v.onOpenEmbedded = { [weak self] containing, part, chip in
            self?.openEmbedded(containing: containing, part: part, chipView: chip)
        }
        tracked.append(WeakView(v))
    }

    /// The live tracked views.
    var views: [MessageViewController] {
        tracked.removeAll { $0.view == nil }
        return tracked.compactMap(\.view)
    }

    // MARK: Message windows

    /// Opens message `s` in its own window, or raises the window that
    /// already shows it (message_view.go `openMessageWindow`).
    func openMessage(_ s: MessageSummary) {
        let key = Key.message(s.id)
        if state.windows.present(key: key) {
            return
        }
        let wc = MessageWindowController(state: state, cache: cache, summary: s)
        wc.delegate = delegate
        track(wc.messageView)
        state.windows.register(wc, key: key)
        place(wc)
        wc.showWindow(nil)
        wc.show(s)
    }

    /// Every open message window.
    var messageWindows: [MessageWindowController] {
        state.windows.keys.compactMap { key in
            guard let k = key.base as? Key, case .message = k else { return nil }
            return state.windows.window(for: key) as? MessageWindowController
        }
    }

    /// Closes the stand-alone window of message `id`, if any, and the
    /// windows of the messages attached to it (the message left the
    /// folder; message_view.go `closeMessageWindow`).
    func closeMessageWindow(_ id: MessageID) {
        state.windows.close(key: Key.message(id))
        state.windows.closeAll { key in
            guard let k = key.base as? Key, case .embedded(let m, _) = k else { return false }
            return m == id
        }
    }

    // MARK: Attached messages

    /// Shows the attached message `part` of `containing` in its own
    /// window, or raises the window already showing it (embedded.go
    /// `openEmbeddedWindow`). Nothing changes on screen while the daemon
    /// renders; a failure is a toast where the chip is.
    func openEmbedded(containing: MessageSummary, part: String, chipView: NSView?) {
        let key = Key.embedded(containing.id, part)
        if state.windows.present(key: key) {
            return
        }
        let cache = cache
        let chipWindow = chipView?.window
        Task { [weak self] in
            let outcome: Result<MessageEmbeddedResult, any Error>
            do {
                outcome = .success(try await cache.fetchEmbedded(
                    accountID: containing.accountId, messageID: containing.id, partID: part))
            } catch {
                outcome = .failure(error)
            }
            guard let self else { return }
            switch outcome {
            case .failure(let err):
                self.log.warning("message.embedded: \(String(describing: err), privacy: .public)")
                self.toast(rpcErrorText(L10n.T("Opening the attached message"), err), in: chipWindow)
            case .success(let res):
                if self.state.windows.present(key: key) {
                    return // a second click overtook the first
                }
                let wc = EmbeddedWindowController(
                    state: self.state, cache: self.cache, containing: containing, part: part, result: res)
                // Tracked like the other views, so its links reach the
                // delegate (embedded.go: the shared view's `openLink`); the
                // fan-out skips views of this mode.
                self.track(wc.messageView)
                self.state.windows.register(wc, key: key)
                self.place(wc)
                wc.showWindow(nil)
            }
        }
    }

    // MARK: Fan-out

    /// Re-renders message `id` wherever it is on display (remote.go
    /// `showLoaded`, for every settle of the cache). An attached message's
    /// view carries the containing message's id and is left alone.
    func showLoaded(_ id: MessageID, _ lm: LoadedMessage) {
        for v in views where v.mode != .embedded {
            if let s = v.current, s.id == id {
                v.render(s, lm)
            }
        }
    }

    /// Redraws the bar of message `id` wherever it is on display and
    /// leaves the body alone (remote.go `refreshRemoteBar`).
    func refreshRemoteBar(_ id: MessageID, _ lm: LoadedMessage) {
        for v in views where v.mode != .embedded {
            if let s = v.current, s.id == id {
                v.refreshRemoteBar(lm)
            }
        }
    }

    /// Pushes the cached delivery state of `id` to the banners showing it
    /// (outbox.go `showOutboxState`).
    func showOutboxState(_ id: MessageID) {
        let m = cache.loaded(id)?.msg
        for v in views where v.mode != .embedded {
            if let s = v.current, s.id == id {
                v.renderOutboxBanner(m)
            }
        }
    }

    /// Re-validates the star (and the rest of the toolbar) of every
    /// message window after flags changed (actions.go `setStar` for the
    /// windows).
    func refreshStars() {
        for wc in messageWindows {
            wc.refreshStar()
        }
    }

    // MARK: Helpers

    /// A toast in the window where the click was, when that window has an
    /// overlay of its own; otherwise wherever toasts go now.
    private func toast(_ text: String, in window: NSWindow?) {
        if let host = window?.windowController as? any ToastHosting {
            host.toasts.show(text)
        } else {
            state.toasts.show(text)
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

/// A weak reference to a tracked view.
@MainActor
private struct WeakView {
    weak var view: MessageViewController?

    init(_ view: MessageViewController) {
        self.view = view
    }
}
