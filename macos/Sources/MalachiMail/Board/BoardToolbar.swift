// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The toolbars of the Board mode, one for the List and one for Columns
/// and Today (the window swaps them with the style, `toolbar(for:)`); each
/// starts with the Mail/Board switch right after the window's buttons,
/// where the mail toolbar has it.
///
/// - List: the sections follow the List's own split view
///   (`BoardListViewController`): the navigation column's (the switch and
///   the sidebar toggle) up to AppKit's `.sidebarTrackingSeparator`; the
///   list's, with the style at its leading edge and the account filter and
///   Triage at its trailing edge; after a separator on the split view's
///   second divider the detail's, with Done, Remind…, Archive at its leading
///   edge and Reply (prominent) at its trailing edge. The detail section is
///   there only while the detail is beside the list (`inlineDetail`); no
///   window title.
/// - Columns and Today: no split view in the window, so the items flow from
///   the window's buttons: the switch and the style (both navigational, so
///   ahead of the title), the title with its subtitle, room, then the
///   account filter and Triage at the trailing end.
///
/// Toolbar identifiers of their own (toolbars that share one share their
/// configuration); no customisation, nothing saved. A toolbar item belongs
/// to one toolbar, so each toolbar has its own items. The style's segments
/// and the detail's actions go through the responder chain
/// (`Action.setBoardStyle`, `Action.boardDone` and the others, and Triage's
/// `Action.boardTriage`; the window controller validates them, Board mode
/// only); the filter's menu acts here, on the board controller. Triage's
/// title, tooltip and visibility come from `triageItem` (the application's
/// `BoardTriageController.view`); a Triage that is not shown is taken out
/// of the toolbars and put back after the filter, as the mail toolbar does
/// with its Assistant button (`isHidden` exists only from macOS 15).
/// `update()` follows the controller (the window calls it after every
/// change the page applied). Swift-first, like `Board`.
@MainActor
final class BoardToolbar: NSObject, NSToolbarDelegate, NSMenuDelegate {
    static let listIdentifier = NSToolbar.Identifier("boardList")
    static let flowIdentifier = NSToolbar.Identifier("board")

    enum ID {
        static let boardStyle = NSToolbarItem.Identifier("boardStyle")
        static let boardAccount = NSToolbarItem.Identifier("boardAccount")
        static let boardTriage = NSToolbarItem.Identifier("boardTriage")
        static let detailSeparator = NSToolbarItem.Identifier("boardDetailSeparator")
        static let boardDone = NSToolbarItem.Identifier("boardDone")
        static let boardRemind = NSToolbarItem.Identifier("boardRemind")
        static let boardArchive = NSToolbarItem.Identifier("boardArchive")
        static let boardReply = NSToolbarItem.Identifier("boardReply")
    }

    /// Columns and Today.
    static let flowItems: [NSToolbarItem.Identifier] = [
        MainToolbar.ID.mode, ID.boardStyle, .flexibleSpace, ID.boardAccount, ID.boardTriage,
    ]
    /// The List's detail section.
    static let detailItems: [NSToolbarItem.Identifier] = [
        ID.detailSeparator, ID.boardDone, ID.boardRemind, ID.boardArchive, .flexibleSpace, ID.boardReply,
    ]
    /// The List: the navigation's section, the list's, the detail's.
    static let listItems: [NSToolbarItem.Identifier] = [
        MainToolbar.ID.mode, .flexibleSpace, .toggleSidebar, .sidebarTrackingSeparator,
        ID.boardStyle, .flexibleSpace, ID.boardAccount, ID.boardTriage,
    ] + detailItems

    /// The colour dot of a state in the Show section.
    static let dotSize: CGFloat = 8
    private weak var controller: BoardController?
    /// The List's split view, whose second divider the detail section
    /// follows; the window hands it over before the List's toolbar shows.
    private let listSplitView: @MainActor () -> NSSplitView?
    /// What the Triage item shows: whether it is there, its title and its
    /// tooltip. Whether it can be clicked is the window's validation.
    struct TriageItem: Equatable {
        var shown: Bool
        var title: String
        var toolTip: String

        static let hidden = TriageItem(shown: false, title: Board.Text.triage, toolTip: "")
    }

    /// Triage's state, asked by every `update()`; the window installs it
    /// (MainWindowController+Triage). Nil: hidden.
    var triageItem: (@MainActor () -> TriageItem)?

    private(set) lazy var listToolbar: NSToolbar = makeToolbar(Self.listIdentifier)
    private(set) lazy var flowToolbar: NSToolbar = makeToolbar(Self.flowIdentifier)
    /// Each toolbar's items, by identifier, as made.
    private var made: [NSToolbar.Identifier: [NSToolbarItem.Identifier: NSToolbarItem]] = [:]
    /// Whether the List's toolbar has its detail section.
    private var showsDetailSection = true

    init(controller: BoardController, listSplitView: @escaping @MainActor () -> NSSplitView?) {
        self.controller = controller
        self.listSplitView = listSplitView
        super.init()
    }

    private func makeToolbar(_ id: NSToolbar.Identifier) -> NSToolbar {
        let tb = NSToolbar(identifier: id)
        tb.delegate = self
        tb.displayMode = .iconOnly
        tb.allowsUserCustomization = false
        tb.autosavesConfiguration = false
        return tb
    }

    /// The toolbar for what the board shows: the List's while the List
    /// shows cases, else the one without sections (the empty board's page
    /// has no split view either).
    func toolbar(for style: Board.Style, empty: Bool) -> NSToolbar {
        style == .list && !empty ? listToolbar : flowToolbar
    }

    /// The switches made so far (MainWindowController+Mode keeps their
    /// selection in step).
    var modeItems: [NSToolbarItemGroup] {
        made.values.compactMap { $0[MainToolbar.ID.mode] as? NSToolbarItemGroup }
    }

    /// Brings the items in step with the controller: the selected style,
    /// the filter's icon and tooltip, the detail section and Done's title.
    /// The filter's menu is built when it opens.
    func update() {
        updateItems()
        setTriage(present: triageItem?().shown ?? false)
        if let controller {
            setDetailSection(visible: controller.state.inlineDetail)
        }
    }

    /// The items' state, without adding or removing any (also while a
    /// toolbar is being filled).
    private func updateItems() {
        guard let controller else { return }
        let style = controller.state.style.rawValue
        let narrowed = controller.state.account != .all || (controller.state.style == .list && controller.state.filter != .all)
        for items in made.values {
            if let styleItem = items[ID.boardStyle] as? NSToolbarItemGroup, styleItem.selectedIndex != style {
                styleItem.selectedIndex = style
            }
            if let accountItem = items[ID.boardAccount] {
                accountItem.image = Self.filterImage(active: narrowed, description: controller.view.accountTitle)
                accountItem.toolTip = controller.view.accountTitle
            }
            if let triage = items[ID.boardTriage] {
                setTriage(triage, triageItem?() ?? .hidden)
            }
        }
        updateDone()
    }

    /// Triage's title and tooltip (also while it is out of the toolbars,
    /// so it comes back with the current ones).
    private func setTriage(_ item: NSToolbarItem, _ t: TriageItem) {
        guard t.shown else { return }
        if item.title != t.title {
            item.title = t.title
            item.label = t.title
            item.paletteLabel = t.title
        }
        if item.toolTip != t.toolTip {
            item.toolTip = t.toolTip.isEmpty ? nil : t.toolTip
        }
    }

    /// Puts Triage into the toolbars made so far (right after the account
    /// filter) or takes it out; not while a toolbar is being filled.
    private func setTriage(present: Bool) {
        for (id, toolbar) in [(Self.listIdentifier, listToolbar), (Self.flowIdentifier, flowToolbar)]
        where made[id] != nil {
            let current = toolbar.items.firstIndex { $0.itemIdentifier == ID.boardTriage }
            if present {
                guard current == nil,
                      let filter = toolbar.items.firstIndex(where: { $0.itemIdentifier == ID.boardAccount })
                else { continue }
                toolbar.insertItem(withItemIdentifier: ID.boardTriage, at: filter + 1)
            } else if let current {
                toolbar.removeItem(at: current)
            }
        }
    }

    /// The Triage items made so far (the development hook prints them).
    var triageItems: [NSToolbarItem] {
        made.values.compactMap { $0[ID.boardTriage] }
    }

    /// Done, or "Move Back to Board" for a done case.
    func updateDone() {
        guard let done = made[Self.listIdentifier]?[ID.boardDone] else { return }
        let title = controller?.view.detail?.isDone == true ? Board.Text.notDone : Board.Text.done
        guard done.title != title else { return }
        done.title = title
        done.label = title
        done.toolTip = title
    }

    /// Puts the detail section into the List's toolbar or takes it out:
    /// while the detail folds away (a narrow List shows it in the page's
    /// panel, with its own bar) its divider is gone too.
    private func setDetailSection(visible: Bool) {
        guard visible != showsDetailSection else { return }
        showsDetailSection = visible
        guard made[Self.listIdentifier] != nil else { return }
        let tb = listToolbar
        let start = tb.items.firstIndex { $0.itemIdentifier == ID.detailSeparator }
        if visible {
            guard start == nil else { return }
            var at = tb.items.count
            for id in Self.detailItems {
                tb.insertItem(withItemIdentifier: id, at: at)
                at += 1
            }
        } else if let start {
            while tb.items.count > start {
                tb.removeItem(at: start)
            }
        }
    }

    /// After the window put `toolbar` in place: AppKit's sidebar separator
    /// binds to a split view once and does not find it again after split
    /// views left and re-entered the window (the toolbar harness of
    /// 2026-10-01: the separator stayed a plain item and the window's title
    /// came before it); a new one binds to the split view now in the
    /// window. Shared with the mail toolbar.
    static func rebindSidebarSeparator(in toolbar: NSToolbar) {
        guard let at = toolbar.items.firstIndex(where: { $0.itemIdentifier == .sidebarTrackingSeparator }) else { return }
        toolbar.removeItem(at: at)
        toolbar.insertItem(withItemIdentifier: .sidebarTrackingSeparator, at: at)
    }

    /// AppKit makes the sidebar toggle itself (the delegate is not asked):
    /// in a narrow List it stays beside the switch while the list's and
    /// the detail's items give way.
    static func keepSidebarToggle(in toolbar: NSToolbar) {
        for item in toolbar.items where item.itemIdentifier == .toggleSidebar {
            item.visibilityPriority = .high
        }
    }

    // MARK: NSToolbarDelegate

    func toolbarDefaultItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        var ids: [NSToolbarItem.Identifier]
        if toolbar.identifier != Self.listIdentifier {
            ids = Self.flowItems
        } else if showsDetailSection {
            ids = Self.listItems
        } else {
            ids = Array(Self.listItems.prefix(Self.listItems.count - Self.detailItems.count))
        }
        // Triage only while it is shown (`setTriage(present:)` follows).
        if triageItem?().shown != true {
            ids.removeAll { $0 == ID.boardTriage }
        }
        return ids
    }

    func toolbarAllowedItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        toolbar.identifier == Self.listIdentifier ? Self.listItems : Self.flowItems
    }

    func toolbar(
        _ toolbar: NSToolbar, itemForItemIdentifier id: NSToolbarItem.Identifier, willBeInsertedIntoToolbar flag: Bool
    ) -> NSToolbarItem? {
        let key = toolbar.identifier
        if let item = made[key]?[id] {
            return item
        }
        let item = make(id, list: key == Self.listIdentifier)
        if let item {
            made[key, default: [:]][id] = item
            if id == ID.boardStyle || id == ID.boardAccount || id == ID.boardDone {
                updateItems()
            }
        }
        return item
    }

    private func make(_ id: NSToolbarItem.Identifier, list: Bool) -> NSToolbarItem? {
        switch id {
        case MainToolbar.ID.mode:
            let it = ModeSwitch.item(id, selected: .board)
            // Without a sidebar section an ordinary item would follow the
            // title to the trailing end.
            it.isNavigational = !list
            // The last to give way in a narrow window, as in the mail's
            // toolbar (where nothing beside it competes for its section).
            it.visibilityPriority = .user
            return it
        case ID.boardStyle:
            let it = makeStyleItem(id, navigational: !list)
            it.visibilityPriority = .high
            return it
        case ID.boardAccount:
            return makeAccountItem(id)
        case ID.boardTriage:
            // A text button, as in the design, through the responder chain
            // (the window validates and acts). It is the first to give way
            // to the overflow menu.
            let it = textItem(id, title: Board.Text.triage, action: Action.boardTriage)
            it.visibilityPriority = .low
            setTriage(it, triageItem?() ?? .hidden)
            return it
        case ID.detailSeparator:
            guard let split = listSplitView() else { return nil }
            return NSTrackingSeparatorToolbarItem(identifier: id, splitView: split, dividerIndex: 1)
        case ID.boardDone:
            let it = textItem(id, title: Board.Text.done, action: Action.boardDone)
            it.visibilityPriority = .high
            return it
        case ID.boardRemind:
            let it = textItem(id, title: Board.Text.remind, action: Action.boardRemind)
            it.visibilityPriority = .low
            return it
        case ID.boardArchive:
            let it = textItem(id, title: Board.Text.archive, action: Action.boardArchive)
            it.visibilityPriority = .low
            return it
        case ID.boardReply:
            let it = textItem(id, title: Board.Text.reply, action: Action.boardReply)
            it.visibilityPriority = .high
            if #available(macOS 26.0, *) {
                it.style = .prominent
            }
            return it
        default:
            return nil
        }
    }

    /// A bordered text button acting through the responder chain (no
    /// target) unless the caller sets one.
    private func textItem(_ id: NSToolbarItem.Identifier, title: String, action: Selector) -> NSToolbarItem {
        let it = NSToolbarItem(itemIdentifier: id)
        it.title = title
        it.label = title
        it.paletteLabel = title
        it.toolTip = title
        it.isBordered = true
        it.target = nil
        it.action = action
        return it
    }

    /// List, Columns, Today as text segments; a segment's index is the
    /// style's raw value (`setBoardStyle(_:)` reads it), also from the
    /// overflow menu.
    private func makeStyleItem(_ id: NSToolbarItem.Identifier, navigational: Bool) -> NSToolbarItemGroup {
        let titles = Board.Style.allCases.map { Board.Text.styleTitle($0) }
        let group = NSToolbarItemGroup(
            itemIdentifier: id, titles: titles, selectionMode: .selectOne, labels: titles, target: nil,
            action: Action.setBoardStyle)
        group.controlRepresentation = .expanded
        // Columns and Today: ahead of the window title, as Mail's New
        // Message; a unified toolbar lays out an ordinary item after the
        // title, at the trailing end with the filter and Triage. The List
        // has no title and puts it at its section's leading edge.
        group.isNavigational = navigational
        ModeSwitch.tagSegments(group, titles: titles, action: Action.setBoardStyle)
        return group
    }

    /// The filter: the accounts, and in List the Show section (the
    /// navigation column's filters, out of reach while it is folded). The
    /// icon fills while either narrows the board, as Mail's filter does.
    private func makeAccountItem(_ id: NSToolbarItem.Identifier) -> NSMenuToolbarItem {
        let label = controller?.view.accountTitle ?? Board.Text.allAccounts
        let it = NSMenuToolbarItem(itemIdentifier: id)
        it.image = Self.filterImage(active: false, description: label)
        it.label = Board.Text.allAccounts
        it.paletteLabel = Board.Text.allAccounts
        it.toolTip = label
        it.showsIndicator = false
        it.isBordered = true
        let menu = NSMenu()
        menu.delegate = self
        menu.autoenablesItems = false
        it.menu = menu
        return it
    }

    private static func filterImage(active: Bool, description: String) -> NSImage {
        Icon.symbol(
            active ? "line.3.horizontal.decrease.circle.fill" : "line.3.horizontal.decrease.circle", size: .toolbar,
            description: description)
    }

    // MARK: The filter's menu

    func menuNeedsUpdate(_ menu: NSMenu) {
        menu.removeAllItems()
        guard let controller else { return }
        let view = controller.view
        for account in view.accounts {
            let it = NSMenuItem(title: account.title, action: #selector(accountChosen(_:)), keyEquivalent: "")
            it.target = self
            it.representedObject = account.filter
            it.state = account.selected ? .on : .off
            if !account.badge.isEmpty {
                it.badge = NSMenuItemBadge(string: account.badge)
            }
            menu.addItem(it)
        }
        // The filters apply to the list's sections only.
        guard controller.state.style == .list else { return }
        menu.addItem(.separator())
        for nav in view.nav {
            let it = NSMenuItem(title: nav.title, action: #selector(filterChosen(_:)), keyEquivalent: "")
            it.target = self
            it.representedObject = nav.filter
            it.state = nav.selected ? .on : .off
            if let dot = nav.dot {
                it.image = Self.dot(BoardPalette.accent(dot))
            }
            if nav.count > 0 {
                it.badge = NSMenuItemBadge(count: nav.count)
            }
            menu.addItem(it)
        }
    }

    @objc private func accountChosen(_ sender: NSMenuItem) {
        guard let filter = sender.representedObject as? Board.AccountFilter else { return }
        controller?.setAccount(filter)
    }

    @objc private func filterChosen(_ sender: NSMenuItem) {
        guard let filter = sender.representedObject as? Board.Filter else { return }
        controller?.setFilter(filter)
    }

    /// A state's dot; the colour resolves when the menu draws it.
    private static func dot(_ color: NSColor) -> NSImage {
        let size = NSSize(width: dotSize, height: dotSize)
        let image = NSImage(size: size, flipped: false) { rect in
            color.setFill()
            NSBezierPath(ovalIn: rect).fill()
            return true
        }
        image.isTemplate = false
        return image
    }
}
