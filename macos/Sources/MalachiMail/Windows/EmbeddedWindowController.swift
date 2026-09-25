// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import os

/// An attached message (a message/rfc822 part, or a part named .eml) in its
/// own window (embedded_window.blp, window/embedded.go): message.embedded
/// renders it read-only from the part's bytes and this window shows the
/// result. The daemon does the parsing and sanitising, and only when
/// asked; its pictures arrive inlined, so the view needs no part server,
/// and its own attachments have no part numbers, so their chips only name
/// them. The window has no actions: the message has no id of its own, so
/// there is nothing to flag, move or reply to. The bar offers Load Images
/// only; trusting a sender is about the containing message's sender and
/// belongs in that message's view.
@MainActor
final class EmbeddedWindowController: NSWindowController, NSWindowDelegate, ToastHosting {
    static let defaultSize = MessageWindowController.defaultSize
    static let minimumSize = MessageWindowController.minimumSize

    let state: AppState
    let cache: MessageCache
    let messageView: MessageViewController
    let toasts: ToastPresenter

    /// The message the shown one was attached to, and the part.
    let containing: MessageSummary
    let part: String

    /// What the window displays, for putting the bar back after a failed
    /// image load.
    private(set) var shown: MessageEmbeddedResult

    /// Set from `windowWillClose`, so a late reply is dropped.
    private(set) var closed = false
    /// Set while a message.embedded call for the images runs; the bar
    /// shows it in place of its button.
    private var loading = false

    private var escape: EscapeCloser?
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "embedded")

    init(state: AppState, cache: MessageCache, containing: MessageSummary, part: String, result: MessageEmbeddedResult) {
        self.state = state
        self.cache = cache
        self.containing = containing
        self.part = part
        shown = result
        messageView = MessageViewController(state: state, cache: cache, mode: .embedded)
        toasts = messageView.toasts ?? ToastPresenter()

        let w = NSWindow(
            contentRect: NSRect(origin: .zero, size: Self.defaultSize),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered, defer: false
        )
        w.title = L10n.T("Attached Message")
        w.minSize = Self.minimumSize
        w.toolbarStyle = .unified
        w.tabbingMode = .disallowed
        w.isReleasedWhenClosed = false
        messageView.view.setFrameSize(Self.defaultSize)
        w.contentViewController = messageView
        super.init(window: w)
        w.delegate = self
        // An empty toolbar: the title bar then matches the message window's.
        let toolbar = NSToolbar(identifier: NSToolbar.Identifier("embedded"))
        toolbar.allowsUserCustomization = false
        toolbar.displayMode = .iconOnly
        w.toolbar = toolbar
        w.setFrame(NSRect(origin: .zero, size: Self.defaultSize), display: false)
        w.center()
        w.initialFirstResponder = messageView.bodyTextView
        escape = EscapeCloser.install(on: w)

        messageView.onEmbeddedLoadImages = { [weak self] in self?.loadImages() }
        show(result)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Renders `res`: the attached message's subject as the title, the
    /// containing message's as the subtitle, then headers, body and chips
    /// through the shared view (embedded.go `show`).
    func show(_ res: MessageEmbeddedResult) {
        shown = res
        window?.title = subjectText(res.message.summary.subject)
        // TRANSLATORS: window subtitle; %s is the subject of the message this one was attached to.
        window?.subtitle = L10n.T("Attached to “%s”", subjectText(containing.subject))
        messageView.showEmbedded(containing, part: part, result: res)
    }

    /// The bar's Load Images: message.embedded again with remote images
    /// allowed for this one call, shown in place of what is on display
    /// (embedded.go `loadImages`).
    private func loadImages() {
        if loading {
            return
        }
        loading = true
        messageView.showRemoteBar(RemoteBarState(visible: true, loading: true))
        let cache = cache
        let containing = containing
        let part = part
        Task { [weak self] in
            let outcome: Result<MessageEmbeddedResult, any Error>
            do {
                outcome = .success(try await cache.fetchEmbedded(
                    accountID: containing.accountId, messageID: containing.id, partID: part, remote: .allow))
            } catch {
                outcome = .failure(error)
            }
            guard let self else { return }
            self.loading = false
            if self.closed {
                return
            }
            switch outcome {
            case .failure(let err):
                self.log.warning("message.embedded (allow): \(String(describing: err), privacy: .public)")
                self.toasts.show(rpcErrorText(L10n.T("Loading the images"), err))
                // The bar offers the images again.
                self.messageView.renderRemoteBar(LoadedMessage(body: self.shown.body))
            case .success(let res):
                self.show(res)
            }
        }
    }

    // MARK: NSWindowDelegate

    func windowWillClose(_ notification: Foundation.Notification) {
        closed = true
        escape?.uninstall()
        escape = nil
        messageView.close()
    }

    func windowDidBecomeKey(_ notification: Foundation.Notification) {
        state.toasts.presenter = toasts
    }
}
