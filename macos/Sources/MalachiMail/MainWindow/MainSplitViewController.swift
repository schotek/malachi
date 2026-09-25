// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The three panes of the main window (window.blp `outer_split` and
/// `inner_split`, flattened into one vertical split): the folder sidebar,
/// the message list and the message. Each pane is a container whose
/// content the sidebar, list and reader phases replace.
///
/// The GTK breakpoints (900 and 600 sp) become collapses here: below 900 pt
/// the sidebar folds away, below 600 the list too; widening brings back
/// only what folded automatically, never what the user hid.
@MainActor
final class MainSplitViewController: NSSplitViewController {
    enum Pane: Hashable, CaseIterable {
        case sidebar, list, message
    }

    static let sidebarBreakpoint: CGFloat = 900
    static let listBreakpoint: CGFloat = 600
    static let listMinimum: CGFloat = 280
    /// Where the pane widths the user set are kept. NSSplitView's own
    /// autosave is not used: it restores before the window has its
    /// frame back and, with the holding priorities, ends up recording the
    /// panes at their minimums; the widths here are written only from a
    /// visible window with nothing collapsed, and restored after the frame.
    static let sidebarWidthKey = "main-sidebar-width"
    static let listWidthKey = "main-list-width"

    let sidebarContainer = PaneContainerViewController(pane: .sidebar)
    let listContainer = PaneContainerViewController(pane: .list)
    let messageContainer = PaneContainerViewController(pane: .message)

    private(set) var sidebarItem: NSSplitViewItem!
    private(set) var listItem: NSSplitViewItem!
    private(set) var messageItem: NSSplitViewItem!

    /// Panes collapsed by a breakpoint, to be brought back by widening.
    private var autoCollapsed: Set<Pane> = []
    /// 0 wide, 1 below the sidebar breakpoint, 2 below the list one.
    private var widthClass = -1
    private var appliedDefaultLayout = false

    /// The GTK width fractions (window.blp `sidebar-width-fraction` 0.2 of
    /// the window; 0.38 of the rest for the list), within the min/max.
    static let sidebarFraction: CGFloat = 0.2
    static let listFraction: CGFloat = 0.38
    init() {
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func viewDidLoad() {
        super.viewDidLoad()
        splitView.isVertical = true
        splitView.dividerStyle = .thin

        let sidebar = NSSplitViewItem(sidebarWithViewController: sidebarContainer)
        sidebar.minimumThickness = 200
        sidebar.maximumThickness = 320
        sidebar.preferredThicknessFraction = 0.2
        sidebar.holdingPriority = NSLayoutConstraint.Priority(260)
        sidebar.canCollapse = true
        // A collapse gives its width to the siblings, never to the window:
        // the default behaviour shrinks the window like Finder's sidebar
        // toggle does, which the breakpoints below would cascade (800 →
        // 600 → 360). GTK never resizes the window either.
        sidebar.collapseBehavior = .preferResizingSiblingsWithFixedSplitView
        sidebar.allowsFullHeightLayout = true
        sidebar.titlebarSeparatorStyle = .none
        sidebarItem = sidebar

        let list = NSSplitViewItem(contentListWithViewController: listContainer)
        list.minimumThickness = Self.listMinimum
        list.maximumThickness = 460
        list.preferredThicknessFraction = 0.304
        list.holdingPriority = NSLayoutConstraint.Priority(255)
        list.canCollapse = true
        list.collapseBehavior = .preferResizingSiblingsWithFixedSplitView
        listItem = list

        let message = NSSplitViewItem(viewController: messageContainer)
        message.minimumThickness = 300
        message.holdingPriority = NSLayoutConstraint.Priority(250)
        message.canCollapse = false
        messageItem = message

        addSplitViewItem(sidebar)
        addSplitViewItem(list)
        addSplitViewItem(message)

        // A selector observer unregisters itself when the controller goes.
        NotificationCenter.default.addObserver(
            self, selector: #selector(splitViewDidResize(_:)),
            name: NSSplitView.didResizeSubviewsNotification, object: splitView)
    }

    override func viewDidLayout() {
        super.viewDidLayout()
        applyBreakpoints()
        noteCollapseState()
    }

    /// The first time the window shows, the panes get the widths of the
    /// last session, or the GTK proportions when there are none:
    /// `preferredThicknessFraction` is not honoured once the toolbar's
    /// tracking separators have had their say, and the list would sit at
    /// its maximum. This runs after the window frame was restored, so the
    /// widths apply to the real width.
    override func viewDidAppear() {
        super.viewDidAppear()
        guard !appliedDefaultLayout else { return }
        let width = splitView.bounds.width
        guard width > 0 else { return }
        appliedDefaultLayout = true
        let defaults = UserDefaults.standard
        let savedSidebar = defaults.object(forKey: Self.sidebarWidthKey) as? Double
        let savedList = defaults.object(forKey: Self.listWidthKey) as? Double
        // Positioning a divider expands a collapsed pane, so a pane the
        // breakpoints folded (a narrow restored window) is left alone.
        var sidebar: CGFloat = 0
        if !sidebarItem.isCollapsed {
            let wanted = savedSidebar.map { CGFloat($0) } ?? width * Self.sidebarFraction
            sidebar = min(max(wanted, sidebarItem.minimumThickness), sidebarItem.maximumThickness)
            splitView.setPosition(sidebar, ofDividerAt: 0)
            sidebar += splitView.dividerThickness
        }
        if !listItem.isCollapsed {
            let wanted = savedList.map { CGFloat($0) } ?? (width - sidebar) * Self.listFraction
            let list = min(max(wanted, listItem.minimumThickness), listItem.maximumThickness)
            splitView.setPosition(sidebar + list, ofDividerAt: 1)
        }
    }

    /// Records the sidebar and list widths after a divider drag (or any
    /// other resize) of a visible, uncollapsed layout; a squeeze by a
    /// narrow window is recorded too, like the panes themselves keep it.
    @objc private func splitViewDidResize(_ note: Foundation.Notification) {
        rememberPaneWidths()
    }

    private func rememberPaneWidths() {
        guard appliedDefaultLayout, view.window?.isVisible == true, widthClass == 0,
              !sidebarItem.isCollapsed, !listItem.isCollapsed else { return }
        let defaults = UserDefaults.standard
        defaults.set(Double(sidebarContainer.view.frame.width), forKey: Self.sidebarWidthKey)
        defaults.set(Double(listContainer.view.frame.width), forKey: Self.listWidthKey)
    }

    // MARK: Collapsing

    var isSidebarCollapsed: Bool { sidebarItem.isCollapsed }
    var isListCollapsed: Bool { listItem.isCollapsed }

    /// Called when the list pane folds or unfolds, from a breakpoint, the
    /// View menu or a divider drag. A collapsed pane keeps its frame while
    /// hidden, so the toolbar separator tracking its divider would stay
    /// where the pane was and keep that width reserved in the toolbar (the
    /// window could not get narrower than the list's minimum plus the
    /// message section); the window takes the separator out meanwhile.
    var onListCollapseChanged: (@MainActor (Bool) -> Void)?
    private var lastListCollapsed: Bool?

    private func noteCollapseState() {
        let collapsed = listItem.isCollapsed
        guard collapsed != lastListCollapsed else { return }
        lastListCollapsed = collapsed
        // Unlike a sidebar item, a collapsed content-list item keeps its
        // minimum thickness in the window's minimum width; it is lifted
        // while the pane is away so the window can go down to its own
        // minimum (the GTK 360).
        listItem.minimumThickness = collapsed ? 0 : Self.listMinimum
        onListCollapseChanged?(collapsed)
    }

    /// The native toggle (⌃⌘S, the toolbar's sidebar item). A pane the user
    /// toggles is theirs from then on: a breakpoint no longer restores it.
    override func toggleSidebar(_ sender: Any?) {
        super.toggleSidebar(sender)
        autoCollapsed.remove(.sidebar)
    }

    /// View ▸ Show/Hide Message List (⌥⌘L).
    @objc func toggleMessageList(_ sender: Any?) {
        NSAnimationContext.runAnimationGroup { ctx in
            ctx.allowsImplicitAnimation = true
            listItem.animator().isCollapsed.toggle()
        }
        autoCollapsed.remove(.list)
        noteCollapseState()
    }

    private func applyBreakpoints() {
        let width = view.bounds.width
        guard width > 0, isViewLoaded else { return }
        let cls: Int
        if width < Self.listBreakpoint {
            cls = 2
        } else if width < Self.sidebarBreakpoint {
            cls = 1
        } else {
            cls = 0
        }
        guard cls != widthClass else { return }
        let previous = widthClass
        widthClass = cls
        // No animation for the first pass or before the view is on screen
        // (a layout at the view's initial size happens before the window
        // shows; an animation started then can leave a pane half-way).
        let animated = previous >= 0 && view.window != nil
        // Narrowing: fold what the class demands and remember it. The list
        // goes first: every collapse re-solves the layout, and with the
        // sidebar gone but the list still open the minimums (280 + 300)
        // would exceed a window under 581 pt and widen it for good.
        if cls >= 2, !listItem.isCollapsed {
            autoCollapsed.insert(.list)
            setCollapsed(listItem, true, animated: animated)
        }
        if cls >= 1, !sidebarItem.isCollapsed {
            autoCollapsed.insert(.sidebar)
            setCollapsed(sidebarItem, true, animated: animated)
        }
        // Widening: only what folded on its own comes back.
        if cls <= 1, autoCollapsed.contains(.list) {
            autoCollapsed.remove(.list)
            setCollapsed(listItem, false, animated: animated)
        }
        if cls == 0, autoCollapsed.contains(.sidebar) {
            autoCollapsed.remove(.sidebar)
            setCollapsed(sidebarItem, false, animated: animated)
        }
    }

    private func setCollapsed(_ item: NSSplitViewItem, _ collapsed: Bool, animated: Bool) {
        guard item.isCollapsed != collapsed else { return }
        if animated {
            NSAnimationContext.runAnimationGroup { ctx in
                ctx.allowsImplicitAnimation = true
                item.animator().isCollapsed = collapsed
            }
        } else {
            item.isCollapsed = collapsed
        }
        noteCollapseState()
    }
}

extension MainSplitViewController {
    // NSSplitViewController validates toggleSidebar: itself; the titles
    // alternate between Show and Hide here.
    override func validateUserInterfaceItem(_ item: any NSValidatedUserInterfaceItem) -> Bool {
        guard let action = item.action else { return false }
        let menuItem = item as? NSMenuItem
        switch action {
        case Action.toggleSidebar:
            // macOS-only strings
            menuItem?.title = isSidebarCollapsed ? "Show Sidebar" : "Hide Sidebar"
            return super.validateUserInterfaceItem(item)
        case Action.toggleMessageList:
            // macOS-only strings
            menuItem?.title = isListCollapsed ? "Show Message List" : "Hide Message List"
            return true
        default:
            return super.validateUserInterfaceItem(item)
        }
    }
}

/// One pane of the split view: hosts whatever view controller the pane's
/// owner installs, and keeps an optional overlay (the toast presenter of
/// the message pane) above it.
@MainActor
final class PaneContainerViewController: NSViewController {
    let pane: MainSplitViewController.Pane
    private(set) var content: NSViewController?
    /// A view kept on top of the content across installs.
    private(set) var overlay: NSView?

    init(pane: MainSplitViewController.Pane) {
        self.pane = pane
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func loadView() {
        let v = NSView()
        v.translatesAutoresizingMaskIntoConstraints = false
        view = v
    }

    /// Replaces the pane's content.
    func install(_ vc: NSViewController) {
        if let old = content {
            old.view.removeFromSuperview()
            old.removeFromParent()
        }
        content = vc
        addChild(vc)
        let sub = vc.view
        sub.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(sub, positioned: .below, relativeTo: overlay)
        NSLayoutConstraint.activate([
            sub.topAnchor.constraint(equalTo: view.topAnchor),
            sub.bottomAnchor.constraint(equalTo: view.bottomAnchor),
            sub.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            sub.trailingAnchor.constraint(equalTo: view.trailingAnchor),
        ])
    }

    /// Installs `view` above the content, filling the pane.
    func setOverlay(_ overlayView: NSView) {
        overlay?.removeFromSuperview()
        overlay = overlayView
        overlayView.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(overlayView, positioned: .above, relativeTo: nil)
        NSLayoutConstraint.activate([
            overlayView.topAnchor.constraint(equalTo: view.topAnchor),
            overlayView.bottomAnchor.constraint(equalTo: view.bottomAnchor),
            overlayView.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            overlayView.trailingAnchor.constraint(equalTo: view.trailingAnchor),
        ])
    }
}

/// A pane's placeholder until its real content arrives: a status page.
@MainActor
final class StatusPageViewController: NSViewController {
    let statusPage: StatusPageView

    init(illustration: StatusPageView.Illustration, title: String, description: String) {
        statusPage = StatusPageView(illustration: illustration, title: title, description: description)
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func loadView() {
        view = statusPage
    }
}
