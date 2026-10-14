// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The main window's Board mode (MalachiCore `Board`, `BoardController`):
/// in place of the split view and above the status bar. It shows the
/// current style's view controller (List, Columns, Today) as its child, a
/// status page instead while the account scope has no case at all, and the
/// panel with the selected case's detail sliding in from the trailing edge
/// over Columns, Today and the narrow List (`View.showsPanel`). The window's
/// content runs under the unified toolbar (`.fullSizeContentView`), so
/// everything starts at the safe area's top. While the page shows, the
/// window's toast overlay covers it (`setOverlay`), above the panel.
///
/// The page is the controller's only observer: it hands every change to
/// the inline reply editor (`replyHost`, first), the style shown and the
/// panel's detail (`apply(_:)`), then to the window (`onChange`: the
/// toolbar, the subtitle). It owns the window's `BoardReplyEditorHost`,
/// which both details share, and finishes its pane when the page leaves
/// the window (Mail mode) or the window closes. Swift-first, like `Board`.
@MainActor
final class BoardPageViewController: NSViewController {
    /// The panel's slide.
    static let slideDuration: TimeInterval = 0.2
    /// The panel's preferred width; it leaves at least `panelMargin` of the
    /// page beside it.
    static let panelWidth: CGFloat = 640
    static let panelMargin: CGFloat = 40

    let controller: BoardController
    /// After the page applied a change of the controller: the window's
    /// toolbar and subtitle follow.
    var onChange: (@MainActor (BoardController.Changes) -> Void)?

    /// One controller per style, made on first use and kept, so a style
    /// shown again finds its scroll positions.
    private var styles: [Board.Style: any BoardStyleContent] = [:]
    private var current: (any BoardStyleContent)?
    /// Holds the current style's view.
    private let contentSlot = NSView()
    /// The page without cases: `View.emptyTitle` and `emptyBody` for the
    /// phase (loading, preparing, unavailable, off, or simply empty).
    private let emptyPage = StatusPageView(
        illustration: .symbol("square.grid.2x2"), title: Board.Text.emptyTitle, description: Board.Text.emptyBody)
    /// `View.notice` above Columns and Today (the List has its own, in its
    /// list pane, `BoardListViewController`).
    private let notice = BannerView(symbol: "info.circle", severity: .info)
    private let noticeHost = FillStackView()
    private let panel = BoardPanelView()
    private let detail: BoardDetailViewController
    /// The panel's trailing edge against the page's: 0 while it shows, its
    /// width and the shadow's room while it is out of sight.
    private var panelTrailing: NSLayoutConstraint?
    /// The panel's cap by the page, active only while the panel shows: a
    /// hidden panel's content (its action bar) must not set the window's
    /// minimum width.
    private var panelCap: NSLayoutConstraint?
    /// Whether the panel is in (or sliding in).
    private var panelShown = false
    private weak var toasts: ToastPresenter?
    /// What Archive did, with Undo; above the window's toasts.
    private let undoToast = BoardUndoToast()

    /// The case actions, shared by the styles, their menus and the detail.
    let actions: BoardActions
    /// The inline reply editor of the selected case's suggested reply, for
    /// the List's detail and the panel's.
    let replyHost: BoardReplyEditorHost
    private var windowCloseObserver: NSObjectProtocol?

    init(actions: BoardActions) {
        self.actions = actions
        let controller = actions.controller
        self.controller = controller
        let host = BoardReplyEditorHost(actions: actions)
        replyHost = host
        actions.replyHost = host
        detail = BoardDetailViewController(actions: actions, presentation: .panel)
        super.init(nibName: nil, bundle: nil)
        host.window = { [weak self] in self?.viewIfLoaded?.window }
        host.onToast = { [weak self] text in
            self?.showToast(text)
        }
        controller.onChange = { [weak self] changes in
            self?.apply(changes)
        }
        // A refused write, what Archive did, a placeholder.
        controller.onToast = { [weak self] text in
            self?.showToast(text)
        }
        // Archive's toast offers Undo (the messages back to their folders,
        // the case back on the board).
        controller.onArchived = { [weak self] outcome in
            self?.showArchived(outcome)
        }
        actions.onToast = { [weak self] text in
            self?.showToast(text)
        }
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func loadView() {
        let v = ContentBackgroundView()
        v.translatesAutoresizingMaskIntoConstraints = false
        contentSlot.translatesAutoresizingMaskIntoConstraints = false
        v.addSubview(contentSlot)
        noticeHost.addArrangedSubview(notice)
        v.addSubview(noticeHost)
        v.addSubview(emptyPage)

        // The panel exists from the start, so the toast overlay that
        // `setOverlay` adds later lies above it.
        addChild(detail)
        detail.onToast = { [weak self] text in
            self?.showToast(text)
        }
        detail.onClose = { [weak self] in
            self?.closePanel()
        }
        let detailView = detail.view
        detailView.translatesAutoresizingMaskIntoConstraints = false
        panel.translatesAutoresizingMaskIntoConstraints = false
        panel.addSubview(detailView)
        panel.isHidden = true
        v.addSubview(panel)

        let safe = v.safeAreaLayoutGuide
        // The panel's width is preferred only, below every holding
        // priority, and capped by the page: it never makes the window wider.
        // Above content hugging (250), or the action bar's hugging could
        // win and the panel open at the width of its buttons.
        let preferred = panel.widthAnchor.constraint(equalToConstant: Self.panelWidth)
        preferred.priority = NSLayoutConstraint.Priority(300)
        let trailing = panel.trailingAnchor.constraint(equalTo: v.trailingAnchor, constant: Self.panelWidth + Self.panelMargin)
        panelTrailing = trailing
        let cap = panel.widthAnchor.constraint(lessThanOrEqualTo: v.widthAnchor, constant: -Self.panelMargin)
        panelCap = cap
        NSLayoutConstraint.activate([
            // From the very top: the List's sidebar runs up behind the
            // toolbar; the other styles start under it (`install`).
            contentSlot.topAnchor.constraint(equalTo: v.topAnchor),
            contentSlot.bottomAnchor.constraint(equalTo: v.bottomAnchor),
            contentSlot.leadingAnchor.constraint(equalTo: v.leadingAnchor),
            contentSlot.trailingAnchor.constraint(equalTo: v.trailingAnchor),
            noticeHost.topAnchor.constraint(equalTo: safe.topAnchor),
            noticeHost.leadingAnchor.constraint(equalTo: v.leadingAnchor),
            noticeHost.trailingAnchor.constraint(equalTo: v.trailingAnchor),
            emptyPage.topAnchor.constraint(equalTo: safe.topAnchor),
            emptyPage.bottomAnchor.constraint(equalTo: v.bottomAnchor),
            emptyPage.leadingAnchor.constraint(equalTo: v.leadingAnchor),
            emptyPage.trailingAnchor.constraint(equalTo: v.trailingAnchor),
            panel.topAnchor.constraint(equalTo: safe.topAnchor),
            panel.bottomAnchor.constraint(equalTo: v.bottomAnchor),
            preferred,
            trailing,
            detailView.topAnchor.constraint(equalTo: panel.topAnchor),
            detailView.bottomAnchor.constraint(equalTo: panel.bottomAnchor),
            detailView.leadingAnchor.constraint(equalTo: panel.leadingAnchor),
            detailView.trailingAnchor.constraint(equalTo: panel.trailingAnchor),
        ])
        view = v
        showStyle(controller.state.style)
        updateEmpty()
        updatePanel(animated: false)
    }

    /// Back in the window (Board mode again, the window shown again): the
    /// selected case's reply loads again.
    override func viewWillAppear() {
        super.viewWillAppear()
        replyHost.resume()
        observeWindowClose()
    }

    /// Out of the window (Mail mode): the inline reply is saved and its
    /// pane goes.
    override func viewDidDisappear() {
        super.viewDidDisappear()
        replyHost.suspend()
    }

    /// The main window closes (not only hides): the inline reply is saved
    /// while the window still exists.
    private func observeWindowClose() {
        if let windowCloseObserver {
            NotificationCenter.default.removeObserver(windowCloseObserver)
            self.windowCloseObserver = nil
        }
        guard let window = view.window else { return }
        windowCloseObserver = NotificationCenter.default.addObserver(
            forName: NSWindow.willCloseNotification, object: window, queue: .main
        ) { [weak self] _ in
            MainActor.assumeIsolated {
                self?.replyHost.suspend()
            }
        }
    }

    /// Moves `overlayView` above the page, filling it (as
    /// `PaneContainerViewController.setOverlay` does in a pane, which takes
    /// it back for Mail). The page's toasts (placeholder actions) go to it
    /// while it is a `ToastPresenter`.
    func setOverlay(_ overlayView: NSView) {
        overlayView.removeFromSuperview()
        overlayView.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(overlayView, positioned: .above, relativeTo: nil)
        NSLayoutConstraint.activate([
            overlayView.topAnchor.constraint(equalTo: view.topAnchor),
            overlayView.bottomAnchor.constraint(equalTo: view.bottomAnchor),
            overlayView.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            overlayView.trailingAnchor.constraint(equalTo: view.trailingAnchor),
        ])
        if let presenter = overlayView as? ToastPresenter {
            toasts = presenter
            // Any toast, from the page or from elsewhere (the triage, the
            // application), takes the Undo capsule away: never both.
            presenter.onShow = { [weak self] in
                self?.undoToast.dismiss()
            }
        }
        undoToast.removeFromSuperview()
        view.addSubview(undoToast, positioned: .above, relativeTo: nil)
        NSLayoutConstraint.activate([
            undoToast.topAnchor.constraint(equalTo: view.topAnchor),
            undoToast.bottomAnchor.constraint(equalTo: view.bottomAnchor),
            undoToast.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            undoToast.trailingAnchor.constraint(equalTo: view.trailingAnchor),
        ])
    }

    /// The List's split view, once the List was made: the List's toolbar
    /// cuts its sections at its dividers.
    var listSplitView: NSSplitView? {
        (styles[.list] as? BoardListViewController)?.splitView
    }

    /// The List while it is in the window: the sidebar toggle (the
    /// toolbar's, View ▸ Hide Sidebar) folds its navigation column.
    var sidebarTarget: NSSplitViewController? {
        shownList
    }

    /// The List, while it shows: the window tells it of its toolbar and of
    /// a coming resize.
    var shownList: BoardListViewController? {
        guard let list = current as? BoardListViewController, list.isViewLoaded, list.view.window != nil else { return nil }
        return list
    }

    /// Shows a toast over the page (the toolbar's Triage too); Archive's
    /// Undo gives way to it.
    func showToast(_ text: String) {
        undoToast.dismiss()
        toasts?.show(text)
    }

    /// What Archive did, with Undo while the page is in a window and the
    /// archive can be taken back (else the text alone: no `undoLabel` when
    /// the daemon moved nothing it can move back).
    private func showArchived(_ o: Board.ArchiveOutcome) {
        guard let undoLabel = o.undoLabel, isViewLoaded, view.window != nil, undoToast.superview === view else {
            showToast(o.text)
            return
        }
        toasts?.dismissCurrent()
        undoToast.show(o.text, undo: undoLabel) { [weak self] in
            self?.controller.undoArchive(o)
        }
    }

    /// Where the key R opens Remind…: in the panel while it shows.
    var remindAnchor: NSView? {
        panelShown ? detail.remindAnchor : nil
    }

    /// Gives the keyboard to the board: the current style's table, or the
    /// window itself on the empty board.
    func focusContent() {
        _ = view
        if controller.view.isEmpty {
            view.window?.makeFirstResponder(nil)
            return
        }
        current?.focusContent()
    }

    // MARK: Changes

    /// One change of the controller: the inline reply editor, the style
    /// shown, the empty page, the panel, then the children, then the window.
    private func apply(_ changes: BoardController.Changes) {
        // Before the details render: they ask it what their reply slot shows.
        replyHost.update()
        if isViewLoaded {
            var installed = false
            if changes.contains(.style) {
                installed = showStyle(controller.state.style)
            }
            // A style just put into the window renders the whole view model
            // in its `viewWillAppear`; one that is not in the window yet
            // does so when the page comes back.
            if !installed {
                current?.apply(changes)
            }
            detail.apply(changes)
            updateEmpty()
            if !changes.isDisjoint(with: [.selection, .style, .content]) {
                updatePanel(animated: true)
            }
        }
        onChange?(changes)
    }

    /// Puts `style`'s controller into the slot; true when it was not the
    /// one shown. The keyboard moves to the new one first when it was in
    /// the old one, or AppKit would hand it to the window.
    @discardableResult
    private func showStyle(_ style: Board.Style) -> Bool {
        let next = content(for: style)
        guard next !== current else { return false }
        let old = current
        let hadFocus = old.map { owns(focusIn: $0.view) } ?? false
        current = next
        addChild(next)
        if !controller.view.isEmpty {
            install(next)
        }
        if hadFocus {
            next.focusContent()
        }
        if let old {
            old.view.removeFromSuperview()
            old.removeFromParent()
        }
        return true
    }

    /// Puts `content`'s view into the slot: from the page's top when it
    /// runs behind the toolbar, else from under the toolbar.
    private func install(_ content: any BoardStyleContent) {
        let v = content.view
        guard v.superview !== contentSlot else { return }
        v.translatesAutoresizingMaskIntoConstraints = false
        contentSlot.addSubview(v)
        // Under the notice, which takes no room while hidden.
        let top = content.extendsUnderToolbar ? contentSlot.topAnchor : noticeHost.bottomAnchor
        NSLayoutConstraint.activate([
            v.topAnchor.constraint(equalTo: top),
            v.bottomAnchor.constraint(equalTo: contentSlot.bottomAnchor),
            v.leadingAnchor.constraint(equalTo: contentSlot.leadingAnchor),
            v.trailingAnchor.constraint(equalTo: contentSlot.trailingAnchor),
        ])
    }

    private func content(for style: Board.Style) -> any BoardStyleContent {
        if let made = styles[style] {
            return made
        }
        let made: any BoardStyleContent
        switch style {
        case .list: made = BoardListViewController(actions: actions)
        case .columns: made = BoardColumnsViewController(actions: actions)
        case .today: made = BoardTodayViewController(actions: actions)
        }
        made.onToast = { [weak self] text in
            self?.showToast(text)
        }
        styles[style] = made
        return made
    }

    /// The status page in place of the style while the account scope has
    /// no case at all. The style's view leaves the window meanwhile: a
    /// hidden List would still be the window's sidebar split view, which
    /// the toolbar would keep its section for.
    private func updateEmpty() {
        let model = controller.view
        let empty = model.isEmpty
        emptyPage.illustration = model.phase == .loading || model.phase == .preparing ? .spinner : .symbol("square.grid.2x2")
        emptyPage.title = model.emptyTitle
        emptyPage.descriptionText = model.emptyBody
        let showsNotice = !empty && !model.notice.isEmpty && current?.extendsUnderToolbar == false
        notice.title = model.notice
        notice.reveal(showsNotice, animated: false)
        emptyPage.isHidden = !empty
        if contentSlot.isHidden != empty {
            if empty, owns(focusIn: contentSlot) {
                view.window?.makeFirstResponder(nil)
            }
            contentSlot.isHidden = empty
            if let current {
                if empty {
                    current.view.removeFromSuperview()
                } else {
                    install(current)
                }
            }
            // Cases came back while the window itself had the keyboard
            // (the empty board's): the table takes it again, so the arrow
            // keys and Escape reach the page.
            if !empty, let window = view.window, window.firstResponder === window {
                current?.focusContent()
            }
        }
    }

    // MARK: The panel

    /// Slides the panel in or out after `View.showsPanel`; without
    /// animation under Reduce Motion. The keyboard leaves a panel that
    /// slides out for the style's table.
    private func updatePanel(animated: Bool) {
        let shows = controller.view.showsPanel && !controller.view.isEmpty
        panel.setAccessibilityLabel(controller.view.detail?.spokenTitle ?? "")
        guard shows != panelShown, let trailing = panelTrailing else { return }
        panelShown = shows
        let animate = animated && view.window != nil && !NSWorkspace.shared.accessibilityDisplayShouldReduceMotion
        if !shows, owns(focusIn: panel) {
            current?.focusContent()
        }
        if shows {
            panelCap?.isActive = true
            panel.isHidden = false
            // Start out of sight at the width the panel will have.
            view.layoutSubtreeIfNeeded()
            trailing.constant = panel.frame.width + Self.panelMargin
            view.layoutSubtreeIfNeeded()
        }
        let target = shows ? 0 : panel.frame.width + Self.panelMargin
        guard animate else {
            trailing.constant = target
            panel.isHidden = !shows
            panelCap?.isActive = shows
            return
        }
        NSAnimationContext.runAnimationGroup({ ctx in
            ctx.duration = Self.slideDuration
            ctx.timingFunction = CAMediaTimingFunction(name: .easeInEaseOut)
            trailing.animator().constant = target
        }, completionHandler: { [weak self] in
            MainActor.assumeIsolated {
                guard let self, !self.panelShown else { return }
                self.panel.isHidden = true
                self.panelCap?.isActive = false
            }
        })
    }

    /// The panel's Close button and Escape: nothing selected, and the
    /// keyboard back on the table that held the case.
    private func closePanel() {
        let target = current?.focusTarget
        controller.select(nil)
        if let window = view.window, let target, target.window === window, !target.isHiddenOrHasHiddenAncestor,
           window.makeFirstResponder(target)
        {
            return
        }
        current?.focusContent()
    }

    /// Escape in two steps (`Board.escapeFor`): an open popup of the inline
    /// reply editor (its recipient suggestions, the link popover) closes
    /// first, by itself; the keyboard in that editor or its recipient
    /// fields goes to the detail's state pill, closing nothing; anywhere
    /// else on the page the panel closes (its inline reply moves on to
    /// the List's pane or is saved and finished). Without a panel Escape
    /// goes on up the chain.
    override func cancelOperation(_ sender: Any?) {
        let pane = replyHost.keyboardInLivePane()
        switch Board.escapeFor(
            focusInEditorOrRecipients: pane != nil, popupOpen: pane.map { !$0.popupsHidden } ?? false,
            panelOpen: panelShown)
        {
        case .closePopup:
            return
        case .focusStatePill:
            if let pane {
                replyHost.focusStatePill(holding: pane)
            }
        case .closePanel:
            closePanel()
        case .nothing:
            nextResponder?.tryToPerform(#selector(cancelOperation(_:)), with: sender)
        }
    }

    /// Whether the keyboard is in `container` (a field's editor counts as
    /// its field).
    private func owns(focusIn container: NSView) -> Bool {
        guard var responder = view.window?.firstResponder else { return false }
        if let editor = responder as? NSTextView, editor.isFieldEditor, let field = editor.delegate as? NSResponder {
            responder = field
        }
        guard let v = responder as? NSView else { return false }
        return v.isDescendant(of: container)
    }
}

/// The panel's surface: the content background with a shadow towards the
/// page, a group labelled with the case's title for VoiceOver. Colours
/// resolve in `updateLayer`, so they follow the appearance.
@MainActor
private final class BoardPanelView: NSView {
    override init(frame: NSRect) {
        super.init(frame: frame)
        wantsLayer = true
        layer?.masksToBounds = false
        setAccessibilityElement(true)
        setAccessibilityRole(.group)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var wantsUpdateLayer: Bool { true }

    override func updateLayer() {
        guard let layer else { return }
        layer.backgroundColor = NSColor.controlBackgroundColor.cgColor
        layer.borderWidth = 0
        layer.shadowColor = NSColor.shadowColor.cgColor
        layer.shadowOpacity = 0.18
        layer.shadowRadius = 17
        layer.shadowOffset = CGSize(width: -14, height: 0)
    }
}
