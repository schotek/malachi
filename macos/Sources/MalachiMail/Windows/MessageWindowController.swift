// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import Quartz

/// A window controller that carries a toast overlay of its own
/// (message_window.blp and embedded_window.blp `toast_overlay`), so a
/// toast can be shown in the window where the click was.
@MainActor
protocol ToastHosting: AnyObject {
    var toasts: ToastPresenter { get }
}

/// One message in its own top-level window (message_window.blp,
/// window/message_window.go), opened by double-clicking (or Return on) a
/// row of the list. The toolbar is the message section of the main
/// window's, acting on this window's message through the delegate; the
/// menu bar's per-message items reach the controller through the responder
/// chain and are validated from `delegate.flags(for:)`. Escape closes the
/// window; the body has the initial focus, so the selectable subject is
/// not focused and select-all'd on open.
@MainActor
final class MessageWindowController: NSWindowController, NSWindowDelegate, ToastHosting {
    static let defaultSize = NSSize(width: 820, height: 620)
    static let minimumSize = NSSize(width: 360, height: 294)
    static let toolbarIdentifier = NSToolbar.Identifier("message")

    let state: AppState
    let messageView: MessageViewController
    let toasts: ToastPresenter

    /// The message this window shows.
    private(set) var summary: MessageSummary

    var id: MessageID { summary.id }

    /// The actions; the toolbar and menu items stay disabled without one.
    weak var delegate: (any MessageActionDelegate)? {
        didSet {
            messageView.delegate = delegate
            refreshStar()
        }
    }

    /// Set from `windowWillClose`, so a late reply is dropped
    /// (message_window.go `closed`).
    private(set) var closed = false

    private let toolbarDelegate: MainToolbar
    private var escape: EscapeCloser?

    init(state: AppState, cache: MessageCache, summary: MessageSummary) {
        self.state = state
        self.summary = summary
        messageView = MessageViewController(state: state, cache: cache, mode: .window)
        // A window mode view always has one.
        toasts = messageView.toasts ?? ToastPresenter()
        toolbarDelegate = MainToolbar(splitView: nil)

        let w = NSWindow(
            contentRect: NSRect(origin: .zero, size: Self.defaultSize),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered, defer: false
        )
        w.title = L10n.T("Message")
        w.minSize = Self.minimumSize
        w.toolbarStyle = .unified
        w.tabbingMode = .disallowed
        w.isReleasedWhenClosed = false
        messageView.view.setFrameSize(Self.defaultSize)
        w.contentViewController = messageView
        super.init(window: w)
        w.delegate = self
        w.toolbar = toolbarDelegate.makeToolbar(identifier: Self.toolbarIdentifier)
        w.setFrame(NSRect(origin: .zero, size: Self.defaultSize), display: false)
        w.center()
        w.initialFirstResponder = messageView.bodyTextView
        escape = EscapeCloser.install(on: w)

        messageView.onRender = { [weak self] s, lm in
            self?.rendered(s, lm)
        }
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Displays the summary headers and, once the cache has it, the full
    /// message (message_window.go `show`).
    func show(_ s: MessageSummary) {
        summary = s
        messageView.show(s)
    }

    /// The title follows the full message's subject, the star the flags;
    /// a reply arriving after the close is dropped (message_window.go
    /// `closed`).
    private func rendered(_ s: MessageSummary, _ lm: LoadedMessage?) {
        guard !closed else { return }
        let subject = subjectText(lm?.msg?.summary.subject ?? s.subject)
        window?.title = subject
        refreshStar()
    }

    /// Re-validates the toolbar (the star's state, the trash tooltip) after
    /// the flags changed.
    func refreshStar() {
        window?.toolbar?.validateVisibleItems()
    }

    // MARK: NSWindowDelegate

    func windowWillClose(_ notification: Foundation.Notification) {
        closed = true
        escape?.uninstall()
        escape = nil
        // The only timer that would outlive the window.
        messageView.close()
    }

    func windowDidBecomeKey(_ notification: Foundation.Notification) {
        state.toasts.presenter = toasts
    }

    // MARK: Actions (message_window.go, the "msg" group)

    /// What the message allows right now.
    private var flags: ActionFlags {
        delegate?.flags(for: messageView.current ?? summary) ?? .none
    }

    /// True while an editable text view (a field editor) has the keyboard:
    /// the bare-letter menu items must not fire then. The read-only body
    /// does not consume plain keys.
    private var isTyping: Bool {
        (window?.firstResponder as? NSTextView)?.isEditable == true
    }

    @objc func reply(_ sender: Any?) {
        delegate?.reply(id)
    }

    @objc func replyAll(_ sender: Any?) {
        delegate?.replyAll(id)
    }

    @objc func forward(_ sender: Any?) {
        delegate?.forward(id)
    }

    @objc func markAsRead(_ sender: Any?) {
        delegate?.markRead(id)
    }

    @objc func markAsUnread(_ sender: Any?) {
        delegate?.markUnread(id)
    }

    @objc func toggleFlag(_ sender: Any?) {
        delegate?.toggleFlag(id)
    }

    @objc func archive(_ sender: Any?) {
        delegate?.archive(id)
    }

    @objc func markAsJunk(_ sender: Any?) {
        delegate?.junk(id, from: window)
    }

    @objc func moveToTrash(_ sender: Any?) {
        delegate?.trash(id, from: window)
    }

    @objc func loadImages(_ sender: Any?) {
        delegate?.loadImages(id)
    }

    @objc func trustSender(_ sender: Any?) {
        delegate?.trustSender(id)
    }

    // MARK: Validation

    private func allows(_ action: Selector, _ f: ActionFlags) -> Bool? {
        switch action {
        case Action.reply: return f.reply
        case Action.replyAll: return f.replyAll
        case Action.forward: return f.forward
        case Action.markAsRead: return f.markRead
        case Action.markAsUnread: return f.markUnread
        case Action.toggleFlag: return f.star
        case Action.archive: return f.archive
        case Action.markAsJunk: return f.junk
        case Action.moveToTrash: return f.trash
        case Action.loadImages: return f.loadImages
        case Action.trustSender: return f.trustSender
        default: return nil
        }
    }

    private func trashTitle(_ f: ActionFlags) -> String {
        trashTooltip(outbox: f.outbox)
    }

    private func starTitle(_ f: ActionFlags) -> String {
        f.flagged ? L10n.T("Unstar") : L10n.T("Star")
    }

    // MARK: Quick Look

    // An attachment chip's preview (AttachmentPreview): the panel asks the
    // key window's responder chain for a controller, and a chip never
    // becomes first responder, so this controller answers for it.
    nonisolated override func acceptsPreviewPanelControl(_ panel: QLPreviewPanel!) -> Bool {
        MainActor.assumeIsolated { AttachmentPreview.shared.accepts }
    }

    nonisolated override func beginPreviewPanelControl(_ panel: QLPreviewPanel!) {
        MainActor.assumeIsolated { AttachmentPreview.shared.begin(panel) }
    }

    nonisolated override func endPreviewPanelControl(_ panel: QLPreviewPanel!) {
        MainActor.assumeIsolated { AttachmentPreview.shared.end(panel) }
    }
}

extension MessageWindowController: NSUserInterfaceValidations {
    func validateUserInterfaceItem(_ item: any NSValidatedUserInterfaceItem) -> Bool {
        guard let action = item.action else { return false }
        let f = flags
        if let menuItem = item as? NSMenuItem {
            switch action {
            case Action.toggleFlag: menuItem.title = starTitle(f)
            case Action.moveToTrash: menuItem.title = trashTitle(f)
            default: break
            }
            if Action.bareKeyActions.contains(action), isTyping {
                return false
            }
        }
        return allows(action, f) ?? true
    }
}

extension MessageWindowController: NSToolbarItemValidation {
    func validateToolbarItem(_ item: NSToolbarItem) -> Bool {
        guard let action = item.action else { return true }
        let f = flags
        switch action {
        case Action.toggleFlag:
            (item as? StarToolbarItem)?.flagged = f.flagged
        case Action.moveToTrash:
            item.toolTip = trashTitle(f)
            item.label = trashTitle(f)
        default:
            break
        }
        return allows(action, f) ?? true
    }
}

extension MessageWindowController: MalachiActions {}
