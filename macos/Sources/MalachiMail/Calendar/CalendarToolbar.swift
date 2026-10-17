// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The Calendar mode's actions through the responder chain (the window
/// controller handles and validates them; `MainWindowController+Calendar`).
@MainActor
enum CalendarAction {
    /// File ▸ New Event (⌥⌘N), the toolbar's +.
    static let newEvent = #selector(MainWindowController.newEvent(_:))
}

/// The Calendar mode's toolbar (docs/calendar.md §5.3, artboard 1). The
/// sidebar section, up to `.sidebarTrackingSeparator` on the calendar
/// page's first divider, has the Mail/Board/Calendar switch; then the
/// sidebar toggle, Today, ‹ ›, the view's segments (Day, Week, Month, Agenda)
/// ahead of the window's title and subtitle (`CalendarController.title` /
/// `subtitle`, set by the window), room, the search field "Search Events",
/// + (New Event) and ✦ (the assistant panel, while the Assistant is shown
/// and In App is chosen).
///
/// Navigational items flow ahead of the title in a unified toolbar, so
/// Today, ‹ › and the segments are navigational; the mode switch is not
/// (it sits in the sidebar's section, as in the board's List). The overflow
/// takes ✦ first, then the search field, then the view's segments.
///
/// Today, ‹ ›, the segments and the search act on the controller here;
/// + goes through the responder chain (`CalendarAction.newEvent`), ✦ calls
/// `onAssistant`. `update()` follows the controller.
@MainActor
final class CalendarToolbar: NSObject, NSToolbarDelegate, NSSearchFieldDelegate {
    static let identifier = NSToolbar.Identifier("calendar")

    enum ID {
        static let today = NSToolbarItem.Identifier("calendarToday")
        static let step = NSToolbarItem.Identifier("calendarStep")
        static let view = NSToolbarItem.Identifier("calendarView")
        static let search = NSToolbarItem.Identifier("calendarSearch")
        static let newEvent = NSToolbarItem.Identifier("calendarNewEvent")
        static let assistant = NSToolbarItem.Identifier("calendarAssistant")
    }

    static let items: [NSToolbarItem.Identifier] = [
        // The sidebar toggle after the separator: the three-segment switch
        // (134 pt) and the window's buttons fill the sidebar's 240 pt, and
        // the toggle went to the overflow menu there (measured 2026-10-16).
        MainToolbar.ID.mode, .flexibleSpace, .sidebarTrackingSeparator, .toggleSidebar,
        ID.today, ID.step, ID.view, .flexibleSpace, ID.search, ID.newEvent, ID.assistant,
    ]

    /// The order in which items give way to the overflow menu: ✦, then the
    /// search field, then the view's segments; the rest stays.
    static let searchPriority = NSToolbarItem.VisibilityPriority(rawValue: -500)
    /// The search field's width range.
    static let searchWidth: ClosedRange<CGFloat> = 100...150

    private weak var controller: CalendarController?
    private(set) lazy var toolbar: NSToolbar = {
        let tb = NSToolbar(identifier: Self.identifier)
        tb.delegate = self
        tb.displayMode = .iconOnly
        tb.allowsUserCustomization = false
        tb.autosavesConfiguration = false
        return tb
    }()
    private var made: [NSToolbarItem.Identifier: NSToolbarItem] = [:]
    /// Whether ✦ is in the toolbar (`setAssistant(visible:)`).
    private var showsAssistant: Bool
    /// ✦ was clicked.
    var onAssistant: (@MainActor () -> Void)?

    init(controller: CalendarController, showsAssistant: Bool) {
        self.controller = controller
        self.showsAssistant = showsAssistant
        super.init()
    }

    /// The mode switch, once made (the window keeps its selection).
    var modeItem: NSToolbarItemGroup? {
        made[MainToolbar.ID.mode] as? NSToolbarItemGroup
    }

    /// The items made so far, by identifier (the development hook).
    var madeItems: [NSToolbarItem.Identifier: NSToolbarItem] { made }

    /// Brings the items in step with the controller: the selected view and
    /// the search field's text.
    func update() {
        guard let controller else { return }
        if let control = made[ID.view]?.view as? NSSegmentedControl,
           let i = Cal.View.allCases.firstIndex(of: controller.view), control.selectedSegment != i
        {
            control.selectedSegment = i
        }
        if let field = (made[ID.search] as? NSSearchToolbarItem)?.searchField, field.currentEditor() == nil,
           field.stringValue != controller.searchText
        {
            field.stringValue = controller.searchText
        }
    }

    /// Puts ✦ into the toolbar (at its end) or takes it out.
    func setAssistant(visible: Bool) {
        showsAssistant = visible
        let current = toolbar.items.firstIndex { $0.itemIdentifier == ID.assistant }
        if visible, current == nil {
            toolbar.insertItem(withItemIdentifier: ID.assistant, at: toolbar.items.count)
        } else if !visible, let current {
            toolbar.removeItem(at: current)
        }
    }

    /// Gives the search field the keyboard (⌘F in Calendar, M5).
    func focusSearch() {
        guard let item = made[ID.search] as? NSSearchToolbarItem else { return }
        item.beginSearchInteraction()
    }

    // MARK: NSToolbarDelegate

    func toolbarDefaultItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        showsAssistant ? Self.items : Self.items.filter { $0 != ID.assistant }
    }

    func toolbarAllowedItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        Self.items
    }

    func toolbar(
        _ toolbar: NSToolbar, itemForItemIdentifier id: NSToolbarItem.Identifier, willBeInsertedIntoToolbar flag: Bool
    ) -> NSToolbarItem? {
        if let item = made[id] {
            return item
        }
        let item = make(id)
        if let item {
            made[id] = item
            if id == ID.view || id == ID.search {
                update()
            }
        }
        return item
    }

    private func make(_ id: NSToolbarItem.Identifier) -> NSToolbarItem? {
        switch id {
        case MainToolbar.ID.mode:
            let it = ModeSwitch.item(id, selected: .calendar)
            it.visibilityPriority = .user
            return it
        case ID.today:
            let it = NSToolbarItem(itemIdentifier: id)
            it.title = Cal.Text.today
            it.label = Cal.Text.today
            it.paletteLabel = Cal.Text.today
            it.toolTip = Cal.Text.today + " (⌘T)"
            it.isBordered = true
            it.target = self
            it.action = #selector(todayClicked(_:))
            it.isNavigational = true
            it.visibilityPriority = .high
            return it
        case ID.step:
            // A compact segmented control of its own (an item group adds
            // padding around each segment, and its frames cannot be read).
            let labels = [Cal.Text.previous, Cal.Text.next]
            let control = NSSegmentedControl(
                images: [
                    Icon.symbol("chevron.left", size: .toolbar, description: labels[0]),
                    Icon.symbol("chevron.right", size: .toolbar, description: labels[1]),
                ], trackingMode: .momentary, target: self, action: #selector(stepClicked(_:)))
            control.setToolTip(labels[0] + " (⌘←)", forSegment: 0)
            control.setToolTip(labels[1] + " (⌘→)", forSegment: 1)
            let it = segmentsItem(id, control: control, labels: labels, action: #selector(stepClicked(_:)),
                                  label: labels.joined(separator: " / "))
            it.visibilityPriority = .high
            return it
        case ID.view:
            let titles = Cal.View.allCases.map(Cal.Text.viewTitle)
            let control = NSSegmentedControl(
                labels: titles, trackingMode: .selectOne, target: self, action: #selector(viewChosen(_:)))
            for (i, t) in titles.enumerated() {
                control.setToolTip(t, forSegment: i)
            }
            let it = segmentsItem(id, control: control, labels: titles, action: #selector(viewChosen(_:)),
                                  label: Cal.Text.viewTitle(Cal.defaultView))
            it.visibilityPriority = .standard
            return it
        case ID.search:
            let it = NSSearchToolbarItem(itemIdentifier: id)
            it.label = Cal.Text.searchEvents
            it.paletteLabel = Cal.Text.searchEvents
            it.searchField.placeholderString = Cal.Text.searchEvents
            it.searchField.sendsSearchStringImmediately = true
            it.searchField.sendsWholeSearchString = false
            it.searchField.delegate = self
            it.searchField.target = self
            it.searchField.action = #selector(searchChanged(_:))
            // `searchWidth`: AppKit's default of 256 pt left no room for ✦
            // at the wireframe's 1280 pt.
            it.preferredWidthForSearchField = Self.searchWidth.upperBound
            let minimum = it.searchField.widthAnchor.constraint(greaterThanOrEqualToConstant: Self.searchWidth.lowerBound)
            minimum.isActive = true
            it.visibilityPriority = Self.searchPriority
            return it
        case ID.newEvent:
            let it = NSToolbarItem(itemIdentifier: id)
            it.image = Icon.symbol("plus", size: .toolbar, description: Cal.Text.newEvent)
            it.label = Cal.Text.newEvent
            it.paletteLabel = Cal.Text.newEvent
            it.toolTip = Cal.Text.newEvent + " (⌥⌘N)"
            it.isBordered = true
            it.target = nil
            it.action = CalendarAction.newEvent
            it.visibilityPriority = .high
            return it
        case ID.assistant:
            let label = Assistant.texts().assistant
            let it = NSToolbarItem(itemIdentifier: id)
            it.image = Icon.symbol("sparkles", size: .toolbar, description: label)
            it.label = label
            it.paletteLabel = label
            it.toolTip = label
            it.isBordered = true
            it.target = self
            it.action = #selector(assistantClicked(_:))
            it.visibilityPriority = .low
            return it
        default:
            return nil
        }
    }

    /// An item whose view is `control` (compact segments, sized to their
    /// text), with a menu form that chooses by index (the overflow menu).
    private func segmentsItem(
        _ id: NSToolbarItem.Identifier, control: NSSegmentedControl, labels: [String], action: Selector, label: String
    ) -> NSToolbarItem {
        control.segmentStyle = .automatic
        control.controlSize = .regular
        control.sizeToFit()
        let it = NSToolbarItem(itemIdentifier: id)
        it.view = control
        it.label = label
        it.paletteLabel = label
        it.isNavigational = true
        let menu = NSMenu()
        for (i, title) in labels.enumerated() {
            let m = NSMenuItem(title: title, action: action, keyEquivalent: "")
            m.tag = i
            m.target = self
            menu.addItem(m)
        }
        let form = NSMenuItem(title: label, action: nil, keyEquivalent: "")
        form.submenu = menu
        it.menuFormRepresentation = form
        return it
    }

    // MARK: Actions

    @objc private func todayClicked(_ sender: Any?) {
        controller?.today()
    }

    @objc private func stepClicked(_ sender: Any?) {
        guard let i = MainWindowController.chosenIndex(sender) else { return }
        controller?.step(i == 0 ? -1 : 1)
    }

    @objc private func viewChosen(_ sender: Any?) {
        guard let i = MainWindowController.chosenIndex(sender), Cal.View.allCases.indices.contains(i) else {
            update()
            return
        }
        controller?.setView(Cal.View.allCases[i])
        update()
    }

    @objc private func searchChanged(_ sender: NSSearchField) {
        controller?.setSearch(sender.stringValue)
    }

    @objc private func assistantClicked(_ sender: Any?) {
        onAssistant?()
    }

    /// Escape in the field ends the search (`CalendarController.clearSearch`).
    func control(_ control: NSControl, textView: NSTextView, doCommandBy selector: Selector) -> Bool {
        guard selector == #selector(NSResponder.cancelOperation(_:)) else { return false }
        (control as? NSSearchField)?.stringValue = ""
        controller?.clearSearch()
        return true
    }
}
