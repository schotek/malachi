// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import os

/// The update check of the application (docs/api.md §4.20, the flow of
/// ui/internal/updates): the status follows the connection and
/// notify.updateChanged; the actions of the main window's banner, the What's
/// New dialog and the question a security release asks once. Nothing here
/// downloads anything: the browser opens the package's page, and only a URL
/// `Updates.openableURL` accepts. The notes and the notice are the
/// manifest's plain text and are shown as plain text only.
extension AppState {
    /// The status follows the connection (also after a reconnect) and the
    /// daemon's notification; a security release asks on the main window.
    func wireUpdates() {
        let updates = updates
        updateTokens.append(notifications.addConnectionState { s in
            if Self.isConnected(s) {
                updates.refresh()
            }
        })
        updateTokens.append(notifications.addUpdateChanged { updates.refresh() })
        if Self.isConnected(notifications.connectionState) {
            updates.refresh()
        }
        updates.onPrompt = { [weak self] p in
            self?.askSecurityUpdate(p)
        }
    }

    /// A button of the banner `banner` (`Updates.Banner.buttons`).
    func performUpdate(_ action: Updates.Action, banner: Updates.Banner, from window: NSWindow?) {
        switch action {
        case .download, .moreInfo:
            openUpdateLink(banner.url)
        case .whatsNew:
            showWhatsNew(on: window)
        case .later:
            updates.dismiss(banner.version, .later)
        case .skip:
            updates.dismiss(banner.version, .skip)
        case .close:
            updates.dismissNotice(banner.noticeID)
        }
    }

    /// Opens `raw` in the browser when `Updates.openableURL` accepts it;
    /// true once the browser took it. A refusal or a failure is logged (the
    /// status carries what the user needs to know).
    @discardableResult
    func openUpdateURL(_ raw: String) async -> Bool {
        let log = Logger(subsystem: "io.github.schotek.Malachi", category: "updates")
        guard let ok = Updates.openableURL(raw), let url = URL(string: ok) else {
            log.warning("update link refused")
            return false
        }
        do {
            _ = try await NSWorkspace.shared.open(url, configuration: NSWorkspace.OpenConfiguration())
            return true
        } catch {
            let ns = error as NSError
            log.warning("open update link: \(ns.domain, privacy: .public) \(ns.code, privacy: .public)")
            return false
        }
    }

    func openUpdateLink(_ raw: String) {
        Task { await openUpdateURL(raw) }
    }

    /// The What's New dialog of the offered version (`Updates.notesFor`):
    /// the title, the release date, the notes as plain text in a scrolling
    /// text view that is never rich, and Download, More Information and
    /// Close (Download the default one when there is a package).
    func showWhatsNew(on window: NSWindow?) {
        guard let n = Updates.notesFor(updates.status, date: Self.updateDay) else { return }
        let alert = NSAlert()
        alert.alertStyle = .informational
        alert.messageText = n.title
        alert.informativeText = n.subtitle
        let (notes, text) = Self.notesView(n.blocks)
        alert.accessoryView = notes
        var actions: [String] = []
        if !n.url.isEmpty {
            alert.addButton(withTitle: n.download).keyEquivalent = "\r"
            actions.append(n.url)
        }
        if !n.infoURL.isEmpty {
            alert.addButton(withTitle: n.moreInfo)
            actions.append(n.infoURL)
        }
        let close = alert.addButton(withTitle: n.close)
        close.keyEquivalent = actions.isEmpty ? "\r" : "\u{1b}"
        alert.layout()
        text.scroll(.zero)
        Task { @MainActor in
            let r = await Self.run(alert, on: window ?? self.mainWindow?.window)
            let i = r.rawValue - NSApplication.ModalResponse.alertFirstButtonReturn.rawValue
            if i >= 0, i < actions.count {
                await self.openUpdateURL(actions[i])
            }
        }
    }

    /// The question of a security release (`Updates.promptFor`), asked
    /// once: Download (the default) and Cancel, with the check box to quit
    /// after the download starts, checked. Whatever the answer the daemon
    /// records it (update.dismiss "prompted"); Download opens the package
    /// in the browser and, with the box checked, quits the way Quit in the
    /// menu does (`NSApp.terminate`, through the delegate's questions about
    /// unsaved work). The banner stays either way.
    func askSecurityUpdate(_ p: Updates.Prompt) {
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = p.title
        alert.informativeText = p.body
        alert.addButton(withTitle: p.download).keyEquivalent = "\r"
        alert.addButton(withTitle: p.cancel).keyEquivalent = "\u{1b}"
        alert.showsSuppressionButton = true
        alert.suppressionButton?.title = p.quit
        alert.suppressionButton?.state = .on
        let updates = updates
        Task { @MainActor in
            let window = self.mainWindow?.window
            if window?.isVisible != true {
                NSApp.activate()
            }
            let download = await Self.run(alert, on: window) == .alertFirstButtonReturn
            let quit = alert.suppressionButton?.state == .on
            await updates.promptAnswered(p)
            guard download, await self.openUpdateURL(p.url), quit else { return }
            NSApp.terminate(nil)
        }
    }

    /// A release date in the user's format.
    static func updateDay(_ d: Date) -> String {
        DateFormatter.localizedString(from: d, dateStyle: .medium, timeStyle: .none)
    }

    /// The time of the last check in the user's format.
    static func updateTime(_ d: Date) -> String {
        DateFormatter.localizedString(from: d, dateStyle: .medium, timeStyle: .short)
    }

    /// The notes read as Markdown and drawn like the assistant's answers
    /// (links as plain text), selectable, not editable, scrolling past a
    /// height.
    private static func notesView(_ blocks: [Assistant.Block]) -> (NSView, NSTextView) {
        // TextKit 1: TextKit 2 lays the text out lazily, estimates the
        // heights first and moved the view a second after it opened.
        let scroll = NotesScrollView(frame: NSRect(x: 0, y: 0, width: 420, height: 240))
        scroll.hasVerticalScroller = true
        scroll.borderType = .bezelBorder
        let tv = NSTextView(usingTextLayoutManager: false)
        tv.minSize = .zero
        tv.maxSize = NSSize(width: CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
        tv.isVerticallyResizable = true
        tv.isHorizontallyResizable = false
        tv.autoresizingMask = [.width]
        tv.textContainer?.widthTracksTextView = true
        scroll.documentView = tv
        do {
            // The final width before the text: laid out at width 0, the
            // text is very tall and the view keeps a position far down.
            tv.frame = NSRect(origin: .zero, size: scroll.contentSize)
            tv.isRichText = true
            tv.importsGraphics = false
            tv.isEditable = false
            tv.isSelectable = true
            tv.usesFontPanel = false
            tv.isAutomaticLinkDetectionEnabled = false
            tv.textContainerInset = NSSize(width: 4, height: 4)
            tv.textStorage?.setAttributedString(AssistantMarkdownRenderer.render(blocks: blocks))
            // Open at the start: the alert would otherwise show the place
            // of the selection AppKit picks.
            if let lm = tv.layoutManager, let tc = tv.textContainer {
                lm.ensureLayout(for: tc)
            }
            tv.setSelectedRange(NSRange(location: 0, length: 0))
            tv.scroll(.zero)
        }
        return (scroll, tv)
    }

    private static func run(_ alert: NSAlert, on window: NSWindow?) async -> NSApplication.ModalResponse {
        if let window, window.isVisible, window.attachedSheet == nil {
            return await alert.beginSheetModal(for: window)
        }
        return alert.runModal()
    }
}

/// The scroll view of the notes. Something in the alert moves the text a
/// moment after the sheet opened (not the text's layout: TextKit 1 lays it
/// out whole, and it still moved), so until the user scrolls, every move
/// goes back to the start.
@MainActor
private final class NotesScrollView: NSScrollView {
    private var userScrolled = false

    override init(frame frameRect: NSRect) {
        super.init(frame: frameRect)
        contentView.postsBoundsChangedNotifications = true
        NotificationCenter.default.addObserver(
            self, selector: #selector(boundsChanged), name: NSView.boundsDidChangeNotification, object: contentView)
    }

    @objc private func boundsChanged() {
        guard !userScrolled, contentView.bounds.origin.y != 0 else { return }
        contentView.scroll(to: .zero)
        reflectScrolledClipView(contentView)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func scrollWheel(with event: NSEvent) {
        userScrolled = true
        super.scrollWheel(with: event)
    }

    /// The scroller's knob and the keyboard count as the user too.
    override func mouseDown(with event: NSEvent) {
        userScrolled = true
        super.mouseDown(with: event)
    }

    override func keyDown(with event: NSEvent) {
        userScrolled = true
        super.keyDown(with: event)
    }
}
