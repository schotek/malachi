// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The Calendar mode's page (docs/calendar.md §5.3): a split view with the
/// sidebar (240 pt; the mini month, the invitations and the calendars, M5)
/// and the content (the view: Day, Week, Month, Agenda or the search
/// results, M5), and at its end the assistant panel while it is shown here
/// (D28: the window's one panel moves in from the mail's split view).
///
/// It is the window's sidebar split view while the calendar shows, so the
/// toolbar's `.sidebarTrackingSeparator` follows its first divider.
///
/// The views (M5): `CalendarSidebarViewController` in the sidebar slot and
/// `CalendarContentViewController` in the content slot (Week/Day, Month,
/// Agenda, the Unanswered filter and the search results, or the empty
/// state). A click on an occurrence selects it and opens
/// `EventDetailPopover` anchored to its view; `anchor(for:)` finds the
/// view of an occurrence on screen for other callers.
///
/// Entry points for the views (M5–M10): `show(view:)` with the controller's
/// view, `controllerChanged()` after every change of the window's
/// `CalendarController`, `sidebarContainer` and `contentContainer` to
/// `install(_:)` their view controllers into (each replaces the
/// placeholder), `assistantSlot` for the window's panel.
@MainActor
final class CalendarPageViewController: NSSplitViewController {
    static let sidebarDefault: CGFloat = 240
    static let sidebarMinimum: CGFloat = 200
    static let sidebarMaximum: CGFloat = 320
    static let contentMinimum: CGFloat = 360

    /// The sidebar's slot: an empty view with the sidebar's background
    /// until M5 installs the sidebar.
    let sidebarContainer = CalendarPaneContainer(placeholder: "")
    /// The content's slot: a label naming the view until M5 installs the
    /// views.
    let contentContainer = CalendarPaneContainer(placeholder: Cal.Text.viewTitle(Cal.defaultView))
    /// The assistant panel's slot, empty unless the panel moved in
    /// (`MainWindowController+Calendar`).
    let assistantSlot = CalendarPaneContainer(placeholder: nil)

    private(set) var sidebarItem: NSSplitViewItem!
    private(set) var contentItem: NSSplitViewItem!
    private(set) var assistantItem: NSSplitViewItem!
    /// The view shown, as last set by `show(view:)`.
    private(set) var shownView: Cal.View = Cal.defaultView
    private var appliedDefaultWidth = false
    /// The sidebar folded because the window was too narrow for it (not by
    /// the user): it comes back when there is room again.
    private var sidebarAutoCollapsed = false

    /// The window's calendar, set before the page shows; the views read it.
    weak var controller: CalendarController? {
        didSet {
            sidebar.controller = controller
            content.controller = controller
        }
    }

    /// The sidebar (M5), installed into `sidebarContainer`.
    let sidebar = CalendarSidebarViewController()
    /// The content (M5), installed into `contentContainer`.
    let content = CalendarContentViewController()

    override func viewDidLoad() {
        super.viewDidLoad()
        splitView.isVertical = true
        splitView.dividerStyle = .thin
        sidebarContainer.view.setFrameSize(NSSize(width: Self.sidebarDefault, height: 400))
        let sidebar = NSSplitViewItem(sidebarWithViewController: sidebarContainer)
        sidebar.minimumThickness = Self.sidebarMinimum
        sidebar.maximumThickness = Self.sidebarMaximum
        sidebar.holdingPriority = NSLayoutConstraint.Priority(260)
        sidebar.canCollapse = true
        sidebarItem = sidebar
        let content = NSSplitViewItem(viewController: contentContainer)
        content.minimumThickness = Self.contentMinimum
        content.holdingPriority = NSLayoutConstraint.Priority(250)
        contentItem = content
        assistantSlot.view.setFrameSize(NSSize(width: MainSplitViewController.assistantDefault, height: 400))
        let assistant = NSSplitViewItem(inspectorWithViewController: assistantSlot)
        assistant.minimumThickness = MainSplitViewController.assistantMinimum
        assistant.maximumThickness = MainSplitViewController.assistantMaximum
        assistant.holdingPriority = NSLayoutConstraint.Priority(258)
        assistant.canCollapse = true
        assistant.canCollapseFromWindowResize = false
        assistant.isCollapsed = true
        assistantItem = assistant
        addSplitViewItem(sidebar)
        addSplitViewItem(content)
        addSplitViewItem(assistant)
        sidebarContainer.install(self.sidebar)
        contentContainer.install(self.content)
        self.content.onOpen = { [weak self] o, anchor in
            self?.open(o, anchoredTo: anchor)
        }
    }

    override func viewDidLayout() {
        super.viewDidLayout()
        fold(width: view.bounds.width)
        // The sidebar's 240 pt once, at the first layout with a width.
        guard !appliedDefaultWidth, splitView.bounds.width > 0, !sidebarItem.isCollapsed else { return }
        appliedDefaultWidth = true
        splitView.setPosition(Self.sidebarDefault, ofDividerAt: 0)
    }

    /// Before the window takes `width` (a resize, the page coming into the
    /// window): what does not fit folds first, so the panes' minimums never
    /// hold the window wider (the board's List does the same).
    func willResize(toWidth width: CGFloat) {
        _ = view
        fold(width: width)
    }

    /// Folds the sidebar, then the assistant panel, when the content would
    /// get less than its minimum; a sidebar folded by the width comes back
    /// at its default width once there is room.
    private func fold(width: CGFloat) {
        guard width > 0, let sidebarItem, let assistantItem else { return }
        let divider = splitView.dividerThickness
        let assistant = assistantItem.isCollapsed ? 0 : MainSplitViewController.assistantMinimum + divider
        if !sidebarItem.isCollapsed, width < Self.sidebarMinimum + divider + Self.contentMinimum + assistant {
            sidebarItem.isCollapsed = true
            sidebarAutoCollapsed = true
        } else if sidebarItem.isCollapsed, sidebarAutoCollapsed,
                  width >= Self.sidebarDefault + divider + Self.contentMinimum + assistant
        {
            sidebarItem.isCollapsed = false
            sidebarAutoCollapsed = false
            appliedDefaultWidth = false
        }
        if !assistantItem.isCollapsed, width < Self.contentMinimum + assistant {
            assistantItem.isCollapsed = true
        }
    }

    /// The toolbar's sidebar toggle, View ▸ Show/Hide Sidebar: the user's
    /// choice from then on.
    override func toggleSidebar(_ sender: Any?) {
        super.toggleSidebar(sender)
        sidebarAutoCollapsed = false
    }

    /// Shows `view` (the controller's) in the content.
    func show(view: Cal.View) {
        shownView = view
        contentContainer.setPlaceholder(Cal.Text.viewTitle(view))
        guard isViewLoaded, let controller else { return }
        content.update(controller: controller, view: view)
    }

    /// After every change of the window's `CalendarController` (its
    /// `onChange`): the sidebar and the content read it again.
    func controllerChanged() {
        guard let controller else { return }
        _ = self.view
        shownView = controller.view
        sidebar.update()
        content.update(controller: controller, view: controller.view)
    }

    /// A click on an occurrence (a block, a bar, a pill, a row): it becomes
    /// the selection and its detail opens anchored to `anchor`. An
    /// invitation not in a calendar (no event id) is only selected.
    func open(_ o: Occurrence, anchoredTo anchor: NSView) {
        controller?.select(Cal.occurrenceKey(o))
        guard let id = o.eventId else { return }
        // The view may have been rebuilt by the selection's change.
        let v = self.anchor(for: Cal.occurrenceKey(o)) ?? anchor
        EventDetailPopover.show(eventID: id, recurrenceID: o.recurrenceId, relativeTo: v.bounds, of: v)
    }

    /// The view of the occurrence `key` on screen in the content (a block,
    /// a bar, a month pill or an agenda row), for a popover's anchor; nil
    /// when it is not shown.
    func anchor(for key: String) -> NSView? {
        guard isViewLoaded else { return nil }
        return content.view(for: key)
    }

    // MARK: The assistant panel (D28)

    /// Whether the panel is in this page's split view and unfolded.
    var assistantShown: Bool {
        assistantSlot.content != nil && !assistantItem.isCollapsed
    }

    /// Takes the window's assistant panel in and unfolds it.
    func showAssistant(_ panel: NSViewController) {
        _ = view
        if assistantSlot.content !== panel {
            assistantSlot.install(panel)
        }
        guard assistantItem.isCollapsed else { return }
        assistantItem.animator().isCollapsed = false
    }

    /// Folds the panel (it stays here until `releaseAssistant`).
    func hideAssistant() {
        guard isViewLoaded, !assistantItem.isCollapsed else { return }
        assistantItem.animator().isCollapsed = true
    }

    /// Gives the panel back (the window returns it to the mail's split
    /// view); the slot folds without animation.
    func releaseAssistant() -> NSViewController? {
        guard isViewLoaded else { return nil }
        assistantItem.isCollapsed = true
        return assistantSlot.release()
    }

    // MARK: Sidebar toggle

    /// View ▸ Show/Hide Sidebar says what it does, as for the mail's.
    override func validateUserInterfaceItem(_ item: any NSValidatedUserInterfaceItem) -> Bool {
        if item.action == Action.toggleSidebar, let menuItem = item as? NSMenuItem {
            // macOS-only strings
            menuItem.title = sidebarItem?.isCollapsed == true ? "Show Sidebar" : "Hide Sidebar"
        }
        return super.validateUserInterfaceItem(item)
    }

    /// The main frames, for the development hook.
    var developmentFrames: [String] {
        guard isViewLoaded else { return [] }
        func r(_ v: NSView) -> String {
            NSStringFromRect(v.convert(v.bounds, to: nil))
        }
        return [
            "  calendar page \(r(view)) view \(shownView.nick)",
            "  calendar sidebar \(r(sidebarContainer.view)) collapsed \(sidebarItem.isCollapsed)",
            "  calendar content \(r(contentContainer.view))",
            "  calendar assistant collapsed \(assistantItem.isCollapsed) panel \(assistantSlot.content != nil)",
        ] + sidebar.developmentFrames + content.developmentFrames
    }
}

/// The content of the calendar page: the view the controller shows (Week
/// and Day share `CalendarWeekView`, Month, Agenda), the agenda table for
/// the Unanswered filter and the search results in place of any view, or
/// the empty state (`emptyKind`).
@MainActor
final class CalendarContentViewController: NSViewController {
    weak var controller: CalendarController?
    var onOpen: ((Occurrence, NSView) -> Void)?

    let week = CalendarWeekView()
    let month = CalendarMonthView()
    let agenda = CalendarAgendaViewController()
    private let emptyView = CalendarEmptyView()
    private var current: NSView?

    override func loadView() {
        let root = NSView()
        addChild(agenda)
        // The content runs under the unified toolbar (`.fullSizeContentView`):
        // the views start at the safe area's top, the toolbar's bottom, as
        // the board's pages do.
        let safe = root.safeAreaLayoutGuide
        for v in [week, month, agenda.view, emptyView] {
            v.translatesAutoresizingMaskIntoConstraints = false
            v.isHidden = true
            root.addSubview(v)
            NSLayoutConstraint.activate([
                v.topAnchor.constraint(equalTo: safe.topAnchor),
                v.bottomAnchor.constraint(equalTo: root.bottomAnchor),
                v.leadingAnchor.constraint(equalTo: root.leadingAnchor),
                v.trailingAnchor.constraint(equalTo: root.trailingAnchor),
            ])
        }
        let open: (Occurrence, NSView) -> Void = { [weak self] o, v in self?.onOpen?(o, v) }
        week.onOpen = open
        month.onOpen = open
        agenda.onOpen = open
        week.onNewEvent = { [weak self] t, origin in self?.controller?.newEvent(at: t, origin: origin) }
        month.onNewEvent = { [weak self] d in self?.controller?.newEvent(at: d, origin: .monthCell) }
        view = root
    }

    /// Reads the controller: `searchResults`, `filter` and `emptyKind`
    /// decide what shows, then that view reads its part.
    func update(controller c: CalendarController, view v: Cal.View) {
        _ = view
        agenda.controller = c
        let shown: NSView
        if CalendarSearchController.active(c) || c.filter == .unanswered {
            agenda.update(controller: c)
            shown = agenda.view
        } else if let kind = c.emptyKind {
            emptyView.set(kind)
            shown = emptyView
        } else {
            switch v {
            case .day, .week:
                week.update(controller: c)
                shown = week
            case .month:
                month.update(controller: c)
                shown = month
            case .agenda:
                agenda.update(controller: c)
                shown = agenda.view
            }
        }
        guard shown !== current else { return }
        current?.isHidden = true
        shown.isHidden = false
        current = shown
    }

    /// The view of occurrence `key` in what shows.
    func view(for key: String) -> NSView? {
        switch current {
        case week?: week.view(for: key)
        case month?: month.view(for: key)
        case agenda.view?: agenda.view(for: key)
        default: nil
        }
    }

    var developmentFrames: [String] {
        guard isViewLoaded else { return [] }
        switch current {
        case week?: return week.developmentFrames
        case month?: return month.developmentFrames
        case agenda.view?: return agenda.developmentFrames
        case emptyView?: return ["  calendar empty state"]
        default: return ["  calendar content nothing shown"]
        }
    }
}

/// The page's empty state: why there is nothing to show
/// (`Cal.Text.emptyTitle`/`emptyBody`), with Open Preferences where the
/// remedy is there.
@MainActor
final class CalendarEmptyView: NSView {
    private let title = NSTextField(labelWithString: "")
    private let body = NSTextField(wrappingLabelWithString: "")
    private let button = NSButton(title: "", target: nil, action: nil)

    override init(frame: NSRect) {
        super.init(frame: frame)
        title.font = Typo.title1
        title.alignment = .center
        body.font = .systemFont(ofSize: Typo.bodySize)
        body.textColor = .secondaryLabelColor
        body.alignment = .center
        body.preferredMaxLayoutWidth = 360
        button.title = Cal.Text.openPreferences
        button.bezelStyle = .push
        button.target = self
        button.action = #selector(openPreferences(_:))
        let col = NSStackView(views: [title, body, button])
        col.orientation = .vertical
        col.alignment = .centerX
        col.spacing = 10
        col.translatesAutoresizingMaskIntoConstraints = false
        addSubview(col)
        NSLayoutConstraint.activate([
            col.centerXAnchor.constraint(equalTo: centerXAnchor),
            col.centerYAnchor.constraint(equalTo: centerYAnchor),
            col.widthAnchor.constraint(lessThanOrEqualToConstant: 380),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Open Preferences: the Calendar tab.
    @objc private func openPreferences(_ sender: Any?) {
        AppState.current?.showCalendarPreferences()
    }

    func set(_ kind: Cal.EmptyKind) {
        title.stringValue = Cal.Text.emptyTitle(kind)
        body.stringValue = Cal.Text.emptyBody(kind)
        body.isHidden = body.stringValue.isEmpty
        button.isHidden = !(kind == .syncOff || kind == .noAccounts)
    }
}

/// A slot of the calendar page: holds one view controller filling it, or
/// a placeholder label (nil: nothing). `release()` gives the controller up
/// without a trace, so that it can go back to another container (the
/// assistant panel between Mail and Calendar).
@MainActor
final class CalendarPaneContainer: NSViewController {
    private(set) var content: NSViewController?
    private let label = NSTextField(labelWithString: "")

    init(placeholder: String?) {
        super.init(nibName: nil, bundle: nil)
        label.stringValue = placeholder ?? ""
        label.isHidden = placeholder?.isEmpty ?? true
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func loadView() {
        let v = NSView()
        v.translatesAutoresizingMaskIntoConstraints = false
        label.font = .systemFont(ofSize: NSFont.systemFontSize + 4, weight: .regular)
        label.textColor = .tertiaryLabelColor
        label.translatesAutoresizingMaskIntoConstraints = false
        v.addSubview(label)
        NSLayoutConstraint.activate([
            label.centerXAnchor.constraint(equalTo: v.centerXAnchor),
            label.centerYAnchor.constraint(equalTo: v.centerYAnchor),
        ])
        view = v
    }

    /// The placeholder's text while nothing is installed.
    func setPlaceholder(_ text: String) {
        label.stringValue = text
        label.isHidden = text.isEmpty || content != nil
    }

    /// Replaces the slot's content.
    func install(_ vc: NSViewController) {
        _ = view
        if let old = content {
            old.view.removeFromSuperview()
            old.removeFromParent()
        }
        content = vc
        addChild(vc)
        let sub = vc.view
        sub.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(sub, positioned: .below, relativeTo: nil)
        NSLayoutConstraint.activate([
            sub.topAnchor.constraint(equalTo: view.topAnchor),
            sub.bottomAnchor.constraint(equalTo: view.bottomAnchor),
            sub.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            sub.trailingAnchor.constraint(equalTo: view.trailingAnchor),
        ])
        label.isHidden = true
    }

    /// Puts the window's toasts over the slot (they move between the
    /// modes' pages).
    func setOverlay(_ overlayView: NSView) {
        _ = view
        overlayView.removeFromSuperview()
        overlayView.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(overlayView, positioned: .above, relativeTo: nil)
        NSLayoutConstraint.activate([
            overlayView.topAnchor.constraint(equalTo: view.topAnchor),
            overlayView.bottomAnchor.constraint(equalTo: view.bottomAnchor),
            overlayView.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            overlayView.trailingAnchor.constraint(equalTo: view.trailingAnchor),
        ])
    }

    /// Takes the content out and forgets it.
    func release() -> NSViewController? {
        guard let vc = content else { return nil }
        content = nil
        vc.view.removeFromSuperview()
        vc.removeFromParent()
        label.isHidden = label.stringValue.isEmpty
        return vc
    }
}
