// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import os
import Quartz

/// The main three-pane window (window.blp, window/window.go): the split
/// view with the status bar under it (`MainContentViewController`), the
/// unified toolbar, the per-message actions of the menu bar and toolbar,
/// and the toast overlay. The panes' content is installed by the sidebar,
/// list and reader parts (`install(sidebar:)`, `install(list:)`,
/// `install(message:)`), the status bar by the app (`install(statusBar:)`),
/// and so are the message actions (`messageActions`).
/// The window is one instance for the application's life: with "Run in
/// Background" it hides instead of closing.
@MainActor
final class MainWindowController: NSWindowController, NSWindowDelegate {
    static let defaultSize = NSSize(width: 1200, height: 760)
    static let minimumSize = NSSize(width: 360, height: 294)
    static let frameAutosaveName = "Main"

    let state: AppState
    let split: MainSplitViewController
    /// The window's content: the split view over the status bar.
    let content: MainContentViewController
    /// The toast overlay of the message pane (window.blp `toast_overlay`).
    let toasts: ToastPresenter

    /// The selection-based actions; nil (everything disabled) until the
    /// app installs them.
    var messageActions: (any MessageActions)?

    /// The window title: the selected folder's name (D1 of the plan).
    var folderTitle: String {
        didSet { window?.title = folderTitle.isEmpty ? MainMenu.appName : folderTitle }
    }

    /// The window subtitle: the selected folder's unread and total counts
    /// (window.go `refreshListTitle`, `folderCountsText`; the subtitle of
    /// the list's Adw.WindowTitle in GTK), empty for none, as in Mail.
    var folderSubtitle: String {
        didSet { window?.subtitle = folderSubtitle }
    }

    private let toolbarDelegate: MainToolbar
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "window")

    init(state: AppState) {
        self.state = state
        folderTitle = ""
        folderSubtitle = ""
        split = MainSplitViewController()
        content = MainContentViewController(split: split)
        toasts = ToastPresenter()

        // fullSizeContentView is what lets the sidebar run the full height
        // under the unified toolbar (its section then holds only its own
        // items, and the window title moves to the list section as in
        // Mail); the split view controller insets the other panes itself.
        let w = NSWindow(
            contentRect: NSRect(origin: .zero, size: Self.defaultSize),
            styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
            backing: .buffered, defer: false
        )
        w.title = MainMenu.appName
        w.minSize = Self.minimumSize
        w.toolbarStyle = .unified
        w.tabbingMode = .disallowed
        // The controller owns the window for the application's life; a
        // hidden window ("Run in Background") must not be released.
        w.isReleasedWhenClosed = false
        // Setting contentViewController sizes the window to the view's
        // frame, so the default size goes on the view first (not on
        // preferredContentSize, which would override a restored frame).
        content.view.setFrameSize(Self.defaultSize)
        w.contentViewController = content
        // The toolbar's tracking separators need the split view, so the
        // toolbar comes after the content (the plan's ordering).
        toolbarDelegate = MainToolbar(splitView: split.splitView)
        super.init(window: w)
        w.delegate = self
        w.toolbar = toolbarDelegate.makeToolbar()
        // The GTK sizes are window sizes, header bars included: the frame
        // is set once the toolbar is part of it. With Auto Layout content
        // the window's minimum comes from the content's constraints, so the
        // GTK minimum is stated there; a restored frame cannot go below it.
        // The status bar is part of the content, as the status line is part
        // of the GTK window: the window keeps the GTK minimum, and the panes
        // above the bar keep at least 200 pt.
        w.setFrame(NSRect(origin: .zero, size: Self.defaultSize), display: false)
        let chrome = w.frame.height - w.contentRect(forFrameRect: w.frame).height
        NSLayoutConstraint.activate([
            content.view.widthAnchor.constraint(greaterThanOrEqualToConstant: Self.minimumSize.width),
            content.view.heightAnchor.constraint(
                greaterThanOrEqualToConstant: max(Self.minimumSize.height - chrome, 200 + StatusBarViewController.height)),
        ])
        w.center()
        split.onListCollapseChanged = { [weak self] collapsed in
            self?.setListSeparator(visible: !collapsed)
        }
        setListSeparator(visible: !split.isListCollapsed)
        w.setFrameAutosaveName(Self.frameAutosaveName)

        split.listContainer.install(StatusPageViewController(
            illustration: .icon("folder-symbolic"),
            title: L10n.T("Select a folder"),
            description: L10n.T("Choose a folder in the sidebar to see its messages.")))
        split.messageContainer.install(StatusPageViewController(
            illustration: .icon("mail-unread-symbolic"),
            title: L10n.T("No Message Selected"),
            description: L10n.T("Select a message to read it here.")))
        split.messageContainer.setOverlay(toasts)
        state.toasts.presenter = toasts
        state.toasts.fallback = toasts
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    // MARK: Pane slots

    func install(sidebar vc: NSViewController) {
        split.sidebarContainer.install(vc)
    }

    func install(list vc: NSViewController) {
        split.listContainer.install(vc)
    }

    func install(message vc: NSViewController) {
        split.messageContainer.install(vc)
    }

    func install(statusBar vc: NSViewController) {
        content.install(statusBar: vc)
    }

    private func setListSeparator(visible: Bool) {
        guard let toolbar = window?.toolbar else { return }
        toolbarDelegate.setListSeparator(visible: visible, in: toolbar)
    }

    /// Presents the window (a hidden one comes back) and activates the app.
    func showMainWindow() {
        showWindow(nil)
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate()
    }

    // MARK: NSWindowDelegate

    /// "Run in Background": closing hides the window; the application and
    /// the daemon live on until Quit (window.go `ConnectCloseRequest`).
    func windowShouldClose(_ sender: NSWindow) -> Bool {
        guard state.settings.runInBackground else { return true }
        sender.orderOut(nil)
        return false
    }

    func windowDidBecomeKey(_ notification: Foundation.Notification) {
        state.toasts.presenter = toasts
    }

    // MARK: Actions

    /// The split view's actions (⌃⌘S, ⌥⌘L) also while no view of the window
    /// has the keyboard focus: the responder chain then starts at the window
    /// and reaches this controller, not the split view controller.
    override func supplementalTarget(forAction action: Selector, sender: Any?) -> Any? {
        if let target = MainContentViewController.splitTarget(split, forAction: action) {
            return target
        }
        return super.supplementalTarget(forAction: action, sender: sender)
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

    /// True while an editable text view (a field editor) has the keyboard:
    /// the bare-letter menu items must not fire then. The read-only body
    /// does not consume plain keys, as GTK's selectable labels do not.
    private var isTyping: Bool {
        (window?.firstResponder as? NSTextView)?.isEditable == true
    }

    private var flags: ActionFlags {
        messageActions?.flags ?? .none
    }

    @objc func checkForNewMail(_ sender: Any?) {
        state.hooks.checkForNewMail?()
    }

    /// The toolbar search field's text (after the typing pause; "" when
    /// cleared) and Return in it; the app hands both to the list.
    var onSearchText: (@MainActor (String) -> Void)? {
        get { toolbarDelegate.onSearchText }
        set { toolbarDelegate.onSearchText = newValue }
    }

    var onSearchReturn: (@MainActor (String) -> Void)? {
        get { toolbarDelegate.onSearchReturn }
        set { toolbarDelegate.onSearchReturn = newValue }
    }

    /// Edit → Find… (⌘F; window.go `win.search`): the search field takes
    /// the keyboard.
    @objc func findMessages(_ sender: Any?) {
        toolbarDelegate.focusSearch()
    }

    /// A filter from the toolbar's menu or the View menu (the item's tag).
    @objc func setMessageFilter(_ sender: Any?) {
        guard let tag = (sender as? NSMenuItem)?.tag else { return }
        let f = FilterMenu.filter(tag: tag)
        state.hooks.setMessageFilter?(f)
        toolbarDelegate.setFilter(active: f != .all)
    }

    @objc func reply(_ sender: Any?) {
        messageActions?.reply()
    }

    @objc func replyAll(_ sender: Any?) {
        messageActions?.replyAll()
    }

    @objc func forward(_ sender: Any?) {
        messageActions?.forward()
    }

    @objc func markAsRead(_ sender: Any?) {
        messageActions?.markRead()
    }

    @objc func markAsUnread(_ sender: Any?) {
        messageActions?.markUnread()
    }

    @objc func toggleFlag(_ sender: Any?) {
        messageActions?.toggleFlag()
    }

    @objc func archive(_ sender: Any?) {
        messageActions?.archive()
    }

    @objc func markAsJunk(_ sender: Any?) {
        messageActions?.junk()
    }

    @objc func moveToTrash(_ sender: Any?) {
        messageActions?.trash()
    }

    @objc func loadImages(_ sender: Any?) {
        messageActions?.loadImages()
    }

    @objc func trustSender(_ sender: Any?) {
        messageActions?.trustSender()
    }

    // MARK: Validation

    /// Whether `action` is allowed for the selection (actions.go
    /// `setMessageActionsSensitive`); nil for an action not handled here.
    private func allows(_ action: Selector, _ f: ActionFlags) -> Bool? {
        switch action {
        case Action.checkForNewMail: return state.hooks.checkForNewMail != nil
        // The filter narrows a folder's listing; search results have none
        // (GTK hides the toggle group while searching).
        case Action.setMessageFilter: return state.hooks.setMessageFilter != nil && !(state.hooks.searchActive?() ?? false)
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

    /// The trash label: Cancel Sending for an outbox message (outbox.go
    /// `trashTooltip`).
    private func trashTitle(_ f: ActionFlags) -> String {
        f.outbox ? L10n.T("Cancel Sending") : L10n.T("Move to Trash")
    }

    private func starTitle(_ f: ActionFlags) -> String {
        f.flagged ? L10n.T("Unstar") : L10n.T("Star")
    }
}

extension MainWindowController: NSUserInterfaceValidations {
    func validateUserInterfaceItem(_ item: any NSValidatedUserInterfaceItem) -> Bool {
        guard let action = item.action else { return false }
        let f = flags
        if let menuItem = item as? NSMenuItem {
            switch action {
            case Action.toggleFlag: menuItem.title = starTitle(f)
            case Action.moveToTrash: menuItem.title = trashTitle(f)
            case Action.setMessageFilter:
                let current = state.hooks.messageFilter?() ?? .all
                menuItem.state = FilterMenu.tag(current) == menuItem.tag ? .on : .off
            default: break
            }
            if Action.bareKeyActions.contains(action), isTyping {
                return false
            }
        }
        return allows(action, f) ?? true
    }
}

extension MainWindowController: NSToolbarItemValidation {
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

extension MainWindowController: MalachiActions {}
