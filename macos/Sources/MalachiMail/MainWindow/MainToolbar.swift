// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The unified toolbar of the main window: the three GTK header bars
/// (window.blp) as one `NSToolbar`, cut into the panes' sections by two
/// `NSTrackingSeparatorToolbarItem`s on the split view's dividers. Items
/// act through the responder chain and are validated by whichever
/// responder owns the action (the window controller). The search field
/// reports to the window, which hands the text to the list.
/// The eWay-CRM button (`CRMToolbarItem`, docs/eway.md) sits right before
/// the Assistant, in the main window's and the message windows' toolbars.
/// The Assistant button (ui/internal/assistant; GTK `assistant_button`) sits
/// right before More Actions while the `assistant-menu` setting is on;
/// its menu is the window's `AssistantMenu`. While the assistant panel
/// exists (In App chosen), the main window's toolbar ends with AppKit's
/// inspector section after the search field: the tracking separator on
/// the panel's divider and the inspector toggle.
/// While the assistant's one-shot requests can run
/// (`AssistantController.canRunInApp`), the search field's magnifier has a
/// menu with "Search in Your Own Words", which ⌥↩ in the field does too
/// (`onSearchOwnWords`); while the words are converted the field shows
/// "Converting the search…" and takes no typing (`beginConverting`,
/// `endConverting`).
@MainActor
final class MainToolbar: NSObject, NSToolbarDelegate, NSSearchFieldDelegate, NSMenuItemValidation, NSMenuDelegate {
    enum ID {
        static let toolbar = NSToolbar.Identifier("main")
        /// The Mail/Board switch (`ModeSwitch`), the main window's only.
        static let mode = NSToolbarItem.Identifier("mode")
        static let newMessage = NSToolbarItem.Identifier("newMessage")
        /// Contacts (Window ▸ Contacts), right after New Message; the
        /// board's and the Calendar's toolbars have it too (`contactsItem`).
        static let contacts = NSToolbarItem.Identifier("contacts")
        static let refresh = NSToolbarItem.Identifier("refresh")
        static let filter = NSToolbarItem.Identifier("filter")
        static let search = NSToolbarItem.Identifier("search")
        static let listSeparator = NSToolbarItem.Identifier("listSeparator")
        static let reply = NSToolbarItem.Identifier("reply")
        static let replyAll = NSToolbarItem.Identifier("replyAll")
        static let forward = NSToolbarItem.Identifier("forward")
        static let trash = NSToolbarItem.Identifier("trash")
        static let archive = NSToolbarItem.Identifier("archive")
        static let star = NSToolbarItem.Identifier("star")
        static let assistant = NSToolbarItem.Identifier("assistant")
        /// eWay-CRM's button (`CRMToolbarItem`), before the Assistant;
        /// hidden without an eWay-CRM account.
        static let crm = NSToolbarItem.Identifier("crm")
        static let moreActions = NSToolbarItem.Identifier("moreActions")
        // The palette's items (T10), not in the default toolbar.
        /// Mark as Read or Unread, whichever the selection allows (O3).
        static let markRead = NSToolbarItem.Identifier("markRead")
        static let junk = NSToolbarItem.Identifier("junk")
        /// Move To: the account's folders, as the context menu's.
        static let moveTo = NSToolbarItem.Identifier("moveTo")
        /// Undo the newest move of mail (Edit ▸ Undo, `MainWindow`).
        static let undo = NSToolbarItem.Identifier("undo")
        static let loadPictures = NSToolbarItem.Identifier("loadPictures")
        /// Print (`printMail:`, §8 of the plan).
        static let print = NSToolbarItem.Identifier("print")
    }

    /// The order of the plan: sidebar section, list section, message
    /// section. The sidebar's separator is AppKit's own
    /// `.sidebarTrackingSeparator` (it follows the split view controller's
    /// sidebar divider by itself): only that one moves the window title
    /// into the section after it, the way Mail shows the mailbox name over
    /// the list; a custom `NSTrackingSeparatorToolbarItem` on divider 0
    /// keeps the title in the sidebar section, which then cannot shrink to
    /// the sidebar. The list/message separator is a custom one on divider 1.
    /// GTK's primary menu has no button here: its items (New Message,
    /// Settings…, About) are in the menu bar, the Mac's main menu. New
    /// Message opens the list section, before the folder's name: a
    /// navigational item, which the system places ahead of the title. The
    /// sidebar toggle sits at the sidebar section's trailing end, by the
    /// divider it folds. The search field closes the message section, at
    /// the toolbar's trailing end, where Mail has it.
    /// The Mail/Board switch comes first, right after the window's buttons;
    /// the board's toolbars (`BoardToolbar`) start with it too, so it stays
    /// put when the window swaps the toolbars.
    /// The message actions are two groups (window.blp: the gap is
    /// `trash_button`'s `margin-end`):
    /// Archive and Trash, then Star, Assistant and More Actions. A `.space`
    /// between them splits the run of bordered items into two Liquid Glass
    /// capsules; an `NSToolbarItemGroup` is not used, because its subitems
    /// are no toolbar items of their own (the Assistant button comes and
    /// goes by `setAssistant`, `ActionPresentation` hides items one by one,
    /// the star is a view item). Mark as Junk has no button: it is in the
    /// menus only.
    static let defaultItems: [NSToolbarItem.Identifier] = [
        // The mode switch alone in the sidebar's section: with three
        // segments (134 pt) and the window's buttons there is no room for
        // the sidebar toggle, which comes right after the separator
        // (navigational, `BoardToolbar.keepSidebarToggle`).
        ID.mode, .flexibleSpace,
        .sidebarTrackingSeparator, .toggleSidebar,
        ID.newMessage, ID.contacts, ID.filter, ID.refresh, .flexibleSpace,
        ID.listSeparator,
        ID.reply, ID.replyAll, ID.forward, .flexibleSpace,
        ID.archive, ID.trash, .space, ID.crm, .space, ID.star, ID.assistant, ID.moreActions, ID.search,
    ]

    /// The inspector section after the search field while the assistant
    /// panel exists: AppKit's own items, which follow the split view
    /// controller's inspector and send `toggleInspector:`.
    static let panelItems: [NSToolbarItem.Identifier] = [
        .inspectorTrackingSeparator, .flexibleSpace, .toggleInspector,
    ]

    /// The items of the message section, for the message window's toolbar.
    static let messageSectionItems: [NSToolbarItem.Identifier] = [
        ID.reply, ID.replyAll, ID.forward, .flexibleSpace,
        ID.archive, ID.trash, .space, ID.crm, .space, ID.star, ID.assistant, ID.moreActions,
    ]

    private weak var splitView: NSSplitView?
    private var items: [NSToolbarItem.Identifier: NSToolbarItem] = [:]
    /// The Assistant button's menu; nil for a toolbar without one.
    private let assistantMenu: AssistantMenu?
    /// Whether the Assistant button is in the toolbar (`assistant-menu`).
    private var showsAssistant: Bool
    /// Whether the Assistant button opens the assistant panel instead of
    /// its menu (`Assistant.buttonOpensPanel`: In App chosen, the main
    /// window's toolbar); a click then calls `onOpenAssistantPanel`, which
    /// the window installs.
    private var assistantOpensPanel = false
    var onOpenAssistantPanel: (@MainActor () -> Void)?
    /// Whether the inspector section is in the toolbar (the assistant
    /// panel exists); only a toolbar with sections has one.
    private var showsPanel: Bool

    /// The search field's text once typing pauses, "" at once when it is
    /// cleared (search.go `onSearchChanged`); Return in the field
    /// (`onSearchActivate`). The window installs both.
    var onSearchText: (@MainActor (String) -> Void)?
    var onSearchReturn: (@MainActor (String) -> Void)?
    private var searchWork: DispatchWorkItem?
    /// How long typing pauses before the text is searched (window.blp
    /// `search-delay: 300`).
    static let searchDelay: TimeInterval = 0.3

    /// "Search in Your Own Words" (the magnifier's menu, ⌥↩ in the field)
    /// with the field's text; the window installs it.
    var onSearchOwnWords: (@MainActor (String) -> Void)?
    /// The magnifier's menu, built once and set as the field's template
    /// only while the one-shot requests can run (`setOwnWords`).
    private let ownWordsMenu = NSMenu()
    /// Whether "Search in Your Own Words" is offered.
    private var ownWords = false
    /// The words are being converted: the field takes no typing, and what
    /// was typed is kept for when the conversion fails.
    private(set) var converting = false
    private var typedWords = ""

    /// The search field folds to a magnifier button while no search is on
    /// (a deviation, macos/README.md). `NSSearchToolbarItem` folds only
    /// when the toolbar runs out of room and has no public way to stay
    /// folded, so `ID.search` is one of two items in the same place:
    /// `searchLens`, a plain button, or `searchItem`, the field, which
    /// unfolds on a click on the lens, ⌘F and text put into it by the app
    /// (`expandSearch`) and folds again once it is empty and has lost the
    /// keyboard, never while it holds text or converts (`collapseSearchIfIdle`).
    private var searchItem: NSSearchToolbarItem?
    private var searchLens: NSToolbarItem?
    private var searchExpanded = false
    /// The toolbar `makeToolbar` made, where the two search items swap.
    private weak var toolbar: NSToolbar?
    /// The window's eWay-CRM controller for the CRM item (`CRMToolbarItem`),
    /// set by the window controller; nil until the application installed
    /// one, and then the item stays hidden.
    var crmController: (@MainActor () -> CRMController?)?
    /// Move To's menu for the selection (`MessageContextMenu.moveToMenu`),
    /// nil without one; the window installs it.
    var moveToMenu: (@MainActor () -> NSMenu?)?
    /// Whether Undo can take a move back (or text its typing); the window
    /// installs it.
    var undoEnabled: (@MainActor () -> Bool)?
    /// Whether the window allows an action now (Move To's item, which has
    /// no action of its own); the window installs it.
    var actionEnabled: (@MainActor (Selector) -> Bool)?
    /// The search field was put into the toolbar for this search only,
    /// the user's arrangement having none (⌘F, O5); it goes again once idle.
    private var temporarySearch = false
    /// The user's arrangement (the main window's toolbar only; nil for a
    /// message window's, which is fixed). Docs/plans/toolbar-customization.md.
    let customization: ToolbarCustomization?
    /// Whether the list/message separator is in the toolbar
    /// (`setListSeparator`).
    private var showsListSeparator = true

    /// - Parameters:
    ///   - splitView: the split view whose dividers 0 and 1 the tracking
    ///     separators follow; nil for a toolbar without sections.
    ///   - assistantMenu: the Assistant button's menu, nil for none.
    ///   - showsAssistant: whether the button starts in the toolbar.
    ///   - showsPanel: whether the inspector section (the assistant
    ///     panel's toggle) starts in the toolbar.
    ///   - settings: the settings of the user's arrangement; nil for a
    ///     toolbar that cannot be customized (a message window's).
    init(splitView: NSSplitView?, assistantMenu: AssistantMenu? = nil, showsAssistant: Bool = false, showsPanel: Bool = false,
         settings: Settings? = nil) {
        self.splitView = splitView
        self.assistantMenu = assistantMenu
        self.showsAssistant = showsAssistant
        self.showsPanel = showsPanel && splitView != nil
        if let settings, splitView != nil {
            customization = ToolbarCustomization(
                bar: .mail, settings: settings,
                separators: [.list: .sidebarTrackingSeparator, .message: ID.listSeparator],
                extras: [.inspectorTrackingSeparator, .toggleInspector])
        } else {
            customization = nil
        }
        super.init()
        customization?.features = { [weak self] in self?.features ?? Toolbar.Features() }
        customization?.transient = { [weak self] in self?.temporarySearch == true ? [ID.search] : [] }
        let item = NSMenuItem(
            title: Assistant.searchTexts().ownWords, action: #selector(searchInOwnWords(_:)), keyEquivalent: "")
        item.target = self
        // ⌥↩ in the field (the field's own key, shown for discovery).
        item.keyEquivalent = "\r"
        item.keyEquivalentModifierMask = .option
        ownWordsMenu.addItem(item)
    }

    /// A configured toolbar with this object as its delegate.
    func makeToolbar(identifier: NSToolbar.Identifier = ID.toolbar) -> NSToolbar {
        let tb = NSToolbar(identifier: identifier)
        tb.delegate = self
        tb.displayMode = .iconOnly
        tb.allowsUserCustomization = false
        tb.autosavesConfiguration = false
        toolbar = tb
        customization?.attach(tb)
        return tb
    }

    /// What the window has for the user's arrangement: eWay-CRM's item
    /// hides itself (`CRMToolbarItem`), so it counts as there; printing
    /// does not exist yet.
    private var features: Toolbar.Features {
        var f = Toolbar.Features(have: [.crm, .print])
        if showsAssistant, assistantMenu != nil {
            f.have.insert(.assistant)
        }
        if !showsListSeparator {
            f.hiddenSeparators.insert(Toolbar.separator(.message))
        }
        return f
    }

    /// The More Actions item, made if the toolbar has not asked for it
    /// yet (the user may have taken it off): Jira adds Change Status to its
    /// menu once (`ChangeStatusMenus`), and the palette puts this very item
    /// back.
    var moreActionsItem: NSMenuToolbarItem? {
        if let it = items[ID.moreActions] as? NSMenuToolbarItem {
            return it
        }
        guard let it = make(ID.moreActions) as? NSMenuToolbarItem else { return nil }
        items[ID.moreActions] = it
        return it
    }

    /// The Mail/Board switch, once the toolbar made it; the window keeps
    /// its selected segment in step with the mode.
    var modeItem: NSToolbarItemGroup? {
        items[ID.mode] as? NSToolbarItemGroup
    }

    /// Shows whether the list is filtered: the filter icon filled in the
    /// accent colour while it is, outlined for All.
    func setFilter(active: Bool) {
        items[ID.filter]?.image = Self.filterImage(active: active)
    }

    private static func filterImage(active: Bool) -> NSImage {
        guard active else { return Icon.symbol("line.3.horizontal.decrease.circle", size: .toolbar) }
        let img = Icon.symbol("line.3.horizontal.decrease.circle.fill", size: .toolbar)
        let accent = NSImage.SymbolConfiguration(paletteColors: [.controlAccentColor])
        return img.withSymbolConfiguration(img.symbolConfiguration.applying(accent)) ?? img
    }

    /// Puts the list/message separator into `toolbar` or takes it out. A
    /// collapsed list pane keeps its frame while hidden, so a separator
    /// tracking its divider would stay put and keep the pane's width
    /// reserved in the toolbar; the window removes it while the pane is
    /// away and its items merge into the message section (D3 of the plan).
    func setListSeparator(visible: Bool, in toolbar: NSToolbar) {
        showsListSeparator = visible
        defer { customization?.refreshAllowsCustomization(toolbar) }
        let current = toolbar.items.firstIndex { $0.itemIdentifier == ID.listSeparator }
        if visible {
            guard current == nil else { return }
            let at = customization?.insertionIndex(of: Toolbar.separator(.message), in: toolbar)
                ?? toolbar.items.firstIndex { $0.itemIdentifier == ID.reply } ?? toolbar.items.count
            toolbar.insertItem(withItemIdentifier: ID.listSeparator, at: at)
        } else if let current {
            toolbar.removeItem(at: current)
        }
    }

    /// Makes the Assistant button the panel's opener or its menu, as the
    /// `assistant-target` preference changes (`Assistant.buttonOpensPanel`;
    /// only the main window's toolbar has the panel). The button is one of
    /// two items in its place, a plain button or an `NSMenuToolbarItem`,
    /// and swaps where it stands.
    func setAssistant(opensPanel: Bool, in toolbar: NSToolbar) {
        let opens = opensPanel && splitView != nil
        guard opens != assistantOpensPanel else { return }
        assistantOpensPanel = opens
        items[ID.assistant] = nil
        guard let current = toolbar.items.firstIndex(where: { $0.itemIdentifier == ID.assistant }) else { return }
        toolbar.removeItem(at: current)
        toolbar.insertItem(withItemIdentifier: ID.assistant, at: current)
        hideIfMissing(ID.assistant)
    }

    /// From macOS 15 with the user's arrangement: an item whose feature is
    /// missing stays in its place, hidden (T7).
    private func hideIfMissing(_ id: NSToolbarItem.Identifier) {
        // eWay-CRM's item hides itself (`CRMToolbarItem.apply`).
        guard let customization, ToolbarCustomization.isAvailable, let item = items[id], !(item is CRMToolbarItem)
        else { return }
        if #available(macOS 15, *) {
            let hidden = !customization.isShown(id)
            if item.isHidden != hidden {
                item.isHidden = hidden
            }
        }
    }

    @objc private func assistantPanelClicked(_ sender: Any?) {
        onOpenAssistantPanel?()
    }

    /// Puts the Assistant button into `toolbar` (right before More
    /// Actions) or takes it out, as the `assistant-menu` setting changes.
    func setAssistant(visible: Bool, in toolbar: NSToolbar) {
        showsAssistant = visible
        // From macOS 15 the button keeps its place in the user's arrangement.
        if customization != nil, ToolbarCustomization.isAvailable {
            hideIfMissing(ID.assistant)
            return
        }
        let current = toolbar.items.firstIndex { $0.itemIdentifier == ID.assistant }
        if visible {
            guard current == nil, assistantMenu != nil else { return }
            let at = toolbar.items.firstIndex { $0.itemIdentifier == ID.moreActions } ?? toolbar.items.count
            toolbar.insertItem(withItemIdentifier: ID.assistant, at: at)
        } else if let current {
            toolbar.removeItem(at: current)
        }
    }

    /// Puts the inspector section (the tracking separator, a flexible
    /// space and the inspector toggle) after the search field, or takes it
    /// out, as the assistant panel comes and goes. Only the main window's
    /// toolbar has one.
    func setAssistantPanel(visible: Bool, in toolbar: NSToolbar) {
        guard splitView != nil else { return }
        showsPanel = visible
        let current = toolbar.items.firstIndex { $0.itemIdentifier == .inspectorTrackingSeparator }
        if visible {
            guard current == nil else { return }
            var at = toolbar.items.firstIndex { $0.itemIdentifier == ID.search }.map { $0 + 1 } ?? toolbar.items.count
            for id in Self.panelItems {
                toolbar.insertItem(withItemIdentifier: id, at: at)
                at += 1
            }
        } else if let current {
            // The section is the toolbar's end: the separator and what follows.
            for _ in current..<min(current + Self.panelItems.count, toolbar.items.count) {
                toolbar.removeItem(at: current)
            }
        }
    }

    // MARK: NSToolbarDelegate

    /// Every item this toolbar can hold, the Assistant button and the
    /// inspector section included.
    private var allItems: [NSToolbarItem.Identifier] {
        splitView == nil ? Self.messageSectionItems : Self.defaultItems + Self.panelItems
    }

    func toolbarDefaultItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        if let customization {
            return customization.defaultIdentifiers(toolbar) + (showsPanel ? Self.panelItems : [])
        }
        var ids = splitView == nil ? Self.messageSectionItems : Self.defaultItems
        if !showsAssistant || assistantMenu == nil {
            ids.removeAll { $0 == ID.assistant }
        }
        if showsPanel {
            ids += Self.panelItems
        }
        return ids
    }

    func toolbarAllowedItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        if let customization {
            return customization.allowedIdentifiers(toolbar) + Self.panelItems.filter { $0 != .flexibleSpace }
        }
        return allItems
    }

    func toolbarImmovableItemIdentifiers(_ toolbar: NSToolbar) -> Set<NSToolbarItem.Identifier> {
        customization?.immovableIdentifiers() ?? []
    }

    func toolbar(_ toolbar: NSToolbar, itemIdentifier: NSToolbarItem.Identifier, canBeInsertedAt index: Int) -> Bool {
        customization?.canInsert(itemIdentifier, at: index, in: toolbar) ?? true
    }

    func toolbarWillAddItem(_ notification: Notification) {
        guard let tb = notification.object as? NSToolbar else { return }
        customization?.itemsChanged(tb)
    }

    func toolbarDidRemoveItem(_ notification: Notification) {
        guard let tb = notification.object as? NSToolbar else { return }
        customization?.itemsChanged(tb)
    }

    func toolbar(
        _ toolbar: NSToolbar, itemForItemIdentifier id: NSToolbarItem.Identifier, willBeInsertedIntoToolbar flag: Bool
    ) -> NSToolbarItem? {
        // The palette's own copy: never the toolbar's item, never cached.
        if !flag, customization != nil {
            return paletteItem(id)
        }
        if let cached = items[id] {
            return cached
        }
        let item = make(id)
        if let item {
            items[id] = item
            hideIfMissing(id)
        }
        return item
    }

    /// An item for the customization palette: the items whose making has
    /// effects on this toolbar (the search field, the Assistant's menu)
    /// or whose view is empty until used (eWay-CRM) as a plain image.
    private func paletteItem(_ id: NSToolbarItem.Identifier) -> NSToolbarItem? {
        func plain(_ symbol: NSImage, _ label: String) -> NSToolbarItem {
            let it = NSToolbarItem(itemIdentifier: id)
            it.image = symbol
            it.label = label
            it.paletteLabel = label
            it.isBordered = true
            return it
        }
        switch id {
        case ID.search:
            return plain(Icon.symbol("magnifyingglass", size: .toolbar, description: L10n.T("Search")), L10n.T("Search"))
        case ID.assistant:
            let label = Assistant.texts().assistant
            return plain(Icon.symbol("sparkles", size: .toolbar, description: label), label)
        case ID.crm:
            return plain(Icon.crm, CRM.brand)
        case ID.mode:
            return ModeSwitch.paletteItem(id, selected: .mail)
        default:
            return make(id)
        }
    }

    private func make(_ id: NSToolbarItem.Identifier) -> NSToolbarItem? {
        switch id {
        case ID.mode:
            guard splitView != nil else { return nil }
            return ModeSwitch.item(id)
        case ID.newMessage:
            let it = button(id, image: Icon.newMessage, label: mn(L10n.T("_New Message")), action: Action.newMessage)
            it.toolTip = mn(L10n.T("_New Message")) + " (⌘N)"
            // Ahead of the window title (the folder's name), like Finder's
            // back and forward buttons.
            it.isNavigational = true
            return it
        case ID.contacts:
            // Navigational like New Message, so it stays beside it ahead of
            // the title.
            let it = Self.contactsItem(id)
            it.isNavigational = true
            return it
        case ID.filter:
            // Mail's filter button: All, Unread, Flagged in its menu, the
            // icon filled while the list is narrowed (window.blp
            // `message_filter`, a toggle group above the list in GTK).
            let it = NSMenuToolbarItem(itemIdentifier: id)
            it.image = Self.filterImage(active: false)
            it.label = "Filter" // macOS-only string
            it.toolTip = "Filter" // macOS-only string
            it.showsIndicator = false
            it.isBordered = true
            let menu = NSMenu()
            FilterMenu.items().forEach { menu.addItem($0) }
            it.menu = menu
            return it
        case ID.listSeparator:
            guard let splitView else { return nil }
            return NSTrackingSeparatorToolbarItem(identifier: id, splitView: splitView, dividerIndex: 1)
        case ID.refresh:
            return button(id, image: Icon.refresh, label: L10n.T("Check for New Mail"), action: Action.checkForNewMail)
        case ID.search:
            // Mail's search field (window.blp `search_bar`, a bar over the
            // list in GTK): a search is on while it holds text, and the
            // scope bar appears over the list (a deviation, macos/README.md).
            // The field reports every change; the pause is timed here.
            // Folded to the lens while no search is on (`searchExpanded`).
            let it = NSSearchToolbarItem(itemIdentifier: id)
            it.label = L10n.T("Search")
            it.searchField.placeholderString = L10n.T("Search Mail")
            it.searchField.sendsSearchStringImmediately = true
            it.searchField.sendsWholeSearchString = false
            it.searchField.delegate = self
            it.searchField.target = self
            it.searchField.action = #selector(searchFieldChanged(_:))
            it.searchField.searchMenuTemplate = ownWords ? ownWordsMenu : nil
            searchItem = it
            let lens = NSToolbarItem(itemIdentifier: id)
            lens.image = Icon.symbol("magnifyingglass", size: .toolbar, description: L10n.T("Search"))
            lens.label = L10n.T("Search")
            lens.paletteLabel = L10n.T("Search")
            lens.toolTip = L10n.T("Search Mail") + " (⌘F)"
            lens.isBordered = true
            lens.target = self
            lens.action = #selector(searchLensClicked(_:))
            searchLens = lens
            return searchExpanded ? it : lens
        case ID.reply:
            return button(id, image: Icon.reply, label: L10n.T("Reply"), action: Action.reply)
        case ID.replyAll:
            return button(id, image: Icon.replyAll, label: L10n.T("Reply All"), action: Action.replyAll)
        case ID.forward:
            return button(id, image: Icon.forward, label: L10n.T("Forward"), action: Action.forward)
        case ID.trash:
            return button(id, image: Icon.trash, label: L10n.T("Move to Trash"), action: Action.moveToTrash)
        case ID.archive:
            return button(id, image: Icon.archive, label: L10n.T("Archive"), action: Action.archive)
        case ID.star:
            return StarToolbarItem(itemIdentifier: id)
        case ID.crm:
            let it = CRMToolbarItem(itemIdentifier: id)
            it.controllerProvider = crmController
            return it
        case ID.assistant:
            // The Assistant menu (ui/internal/assistant), filled on open.
            guard let assistantMenu else { return nil }
            let label = Assistant.texts().assistant
            let it = NSMenuToolbarItem(itemIdentifier: id)
            it.image = Icon.symbol("sparkles", size: .toolbar, description: label)
            it.label = label
            it.paletteLabel = label
            it.toolTip = label
            it.showsIndicator = false
            it.isBordered = true
            it.menu = assistantMenu.menu
            if assistantOpensPanel {
                // In App (`setAssistant(opensPanel:in:)`): the same look,
                // a plain button that opens the panel instead of the menu.
                let button = NSToolbarItem(itemIdentifier: id)
                button.image = it.image
                button.label = it.label
                button.paletteLabel = it.paletteLabel
                button.toolTip = it.toolTip
                button.isBordered = it.isBordered
                button.target = self
                button.action = #selector(assistantPanelClicked(_:))
                return button
            }
            return it
        case ID.markRead:
            // Mark as Read or Unread, set by the window's validation
            // (`MainWindowController.validateToolbarItem`).
            let it = button(id, image: Icon.symbol("envelope.open", size: .toolbar), label: mn(L10n.T("Mark as _Read")),
                            action: Action.markAsRead)
            it.visibilityPriority = .low
            return it
        case ID.junk:
            let it = button(id, image: Icon.symbol("xmark.bin", size: .toolbar), label: L10n.T("Mark as Junk"),
                            action: Action.markAsJunk)
            it.visibilityPriority = .low
            return it
        case ID.moveTo:
            let it = MoveToToolbarItem(itemIdentifier: id)
            let label = mn(L10n.T("Move To"))
            it.image = Icon.symbol("folder", size: .toolbar, description: label)
            it.label = label
            it.paletteLabel = label
            it.toolTip = label
            it.showsIndicator = false
            it.isBordered = true
            it.provider = { [weak self] in self?.moveToMenu?() }
            it.enabledProvider = { [weak self] in self?.actionEnabled?(Action.moveToFolder) ?? false }
            it.visibilityPriority = .low
            return it
        case ID.undo:
            let it = ProvidedToolbarItem(itemIdentifier: id)
            let label = L10n.T("Undo")
            it.image = Icon.symbol("arrow.uturn.backward", size: .toolbar, description: label)
            it.label = label
            it.paletteLabel = label
            it.toolTip = label + " (⌘Z)"
            it.isBordered = true
            it.target = nil
            it.action = Selector(("undo:"))
            it.enabledProvider = { [weak self] in self?.undoEnabled?() ?? false }
            it.visibilityPriority = .low
            return it
        case ID.print:
            let it = button(id, image: Icon.symbol("printer", size: .toolbar), label: Printing.itemLabel,
                            action: Action.printMail)
            it.toolTip = Printing.itemLabel + " (⌘P)"
            it.visibilityPriority = .low
            return it
        case ID.loadPictures:
            let it = button(id, image: Icon.symbol("photo", size: .toolbar), label: mn(L10n.T("Load _Images")),
                            action: Action.loadImages)
            it.visibilityPriority = .low
            return it
        case ID.moreActions:
            let it = NSMenuToolbarItem(itemIdentifier: id)
            it.image = Icon.moreActions
            it.label = L10n.T("More Actions")
            it.toolTip = L10n.T("More Actions")
            it.showsIndicator = false
            it.isBordered = true
            it.menu = messageMenu()
            // The actions the user took off the toolbar come first (M6).
            if customization != nil {
                it.menu.delegate = self
            }
            return it
        default:
            return nil
        }
    }

    // MARK: Search field

    private var searchField: NSSearchField? {
        searchItem?.searchField
    }

    /// ⌘F (Edit → Find…): the search field unfolds and takes the keyboard.
    func focusSearch() {
        expandSearch(focus: true)
    }

    /// Puts `text` into the search field without its typing pause; the
    /// caller searches for it (as Return would).
    func setSearchText(_ text: String) {
        searchWork?.cancel()
        searchWork = nil
        searchField?.stringValue = text
        if text.isEmpty {
            scheduleSearchCollapse()
        } else {
            expandSearch(focus: false)
        }
    }

    /// The lens: the field unfolds and takes the keyboard.
    @objc private func searchLensClicked(_ sender: Any?) {
        expandSearch(focus: true)
    }

    /// Unfolds the lens into the field (in its place in the toolbar) and,
    /// with `focus`, gives the field the keyboard.
    private func expandSearch(focus: Bool) {
        if !toolbarHasSearch, insertTemporarySearch() {
            if focus {
                DispatchQueue.main.async { [weak self] in
                    self?.searchItem?.beginSearchInteraction()
                }
            }
            return
        }
        guard let item = searchItem else { return }
        guard !searchExpanded else {
            if focus {
                item.beginSearchInteraction()
            }
            return
        }
        searchExpanded = true
        swapSearchItems()
        guard focus else { return }
        // The field is laid out in the window once the toolbar has placed it.
        DispatchQueue.main.async { [weak item] in
            item?.beginSearchInteraction()
        }
    }

    /// Folds the field back to the lens once it is idle: empty, without
    /// the keyboard and not converting. A field with text is a search on
    /// show, which the lens would hide. Run after the event that may have
    /// ended the search, never from inside the field's own callbacks.
    private func scheduleSearchCollapse() {
        DispatchQueue.main.async { [weak self] in
            guard let self, self.searchExpanded, !self.converting,
                  let field = self.searchField, field.stringValue.isEmpty, field.currentEditor() == nil
            else { return }
            self.searchExpanded = false
            if self.temporarySearch {
                self.removeTemporarySearch()
            } else {
                self.swapSearchItems()
            }
        }
    }

    /// Whether the toolbar holds the search field or its lens.
    private var toolbarHasSearch: Bool {
        toolbar?.items.contains { $0.itemIdentifier == ID.search } ?? false
    }

    /// ⌘F (or text the app puts in) while the user's arrangement has no
    /// search: the field goes, unfolded, at the end of the message
    /// section for this search (`Toolbar.searchIndex`); the stored
    /// arrangement stays as it is. False for a toolbar without the user's
    /// arrangement.
    private func insertTemporarySearch() -> Bool {
        guard let toolbar, let customization else { return false }
        temporarySearch = true
        searchExpanded = true
        items[ID.search] = nil
        let at = customization.insertionIndex(of: Toolbar.ID.search, in: toolbar)
        toolbar.insertItem(withItemIdentifier: ID.search, at: at)
        return true
    }

    /// The temporary field leaves the toolbar; the lens is what the
    /// palette puts back later.
    private func removeTemporarySearch() {
        temporarySearch = false
        if let at = toolbar?.items.firstIndex(where: { $0.itemIdentifier == ID.search }) {
            toolbar?.removeItem(at: at)
        }
        items[ID.search] = searchLens
    }

    /// Puts the item `searchExpanded` asks for in `ID.search`'s place.
    private func swapSearchItems() {
        items[ID.search] = searchExpanded ? searchItem as NSToolbarItem? : searchLens
        guard let toolbar, let at = toolbar.items.firstIndex(where: { $0.itemIdentifier == ID.search }) else { return }
        toolbar.removeItem(at: at)
        toolbar.insertItem(withItemIdentifier: ID.search, at: at)
    }

    /// Offers "Search in Your Own Words" (the magnifier's menu and ⌥↩) or
    /// takes it away; a conversion under way is the window's to end.
    func setOwnWords(available: Bool) {
        ownWords = available
        searchField?.searchMenuTemplate = available ? ownWordsMenu : nil
    }

    /// The words are being converted: the field shows "Converting the
    /// search…" and takes no typing; what was typed is kept.
    func beginConverting() {
        guard let field = searchField, !converting else { return }
        converting = true
        searchWork?.cancel()
        searchWork = nil
        typedWords = field.stringValue
        if field.currentEditor() != nil {
            field.window?.makeFirstResponder(nil)
        }
        field.isEditable = false
        field.placeholderString = Assistant.searchTexts().converting
        field.stringValue = ""
    }

    /// The conversion is over: the field takes typing again and shows
    /// `text`, or the words typed before it (a failure).
    func endConverting(text: String?) {
        guard let field = searchField, converting else { return }
        converting = false
        field.isEditable = true
        field.placeholderString = L10n.T("Search Mail")
        field.stringValue = text ?? typedWords
        typedWords = ""
        if field.stringValue.isEmpty {
            scheduleSearchCollapse()
        } else {
            expandSearch(focus: false)
        }
    }

    /// DEVELOPMENT AID (MALACHI_START `search-own`): the field unfolded
    /// with the words in it, as typed.
    func developmentSetSearch(_ words: String) {
        searchField?.stringValue = words
        if words.isEmpty {
            scheduleSearchCollapse()
        } else {
            expandSearch(focus: false)
        }
    }

    /// The magnifier's "Search in Your Own Words".
    @objc private func searchInOwnWords(_ sender: Any?) {
        guard ownWords, !converting, let field = searchField else { return }
        onSearchOwnWords?(field.stringValue)
    }

    func validateMenuItem(_ menuItem: NSMenuItem) -> Bool {
        guard menuItem.action == #selector(searchInOwnWords(_:)) else { return true }
        let words = searchField?.stringValue.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return ownWords && !converting && !words.isEmpty
    }

    /// Every change of the field's text: an emptied field ends the search
    /// at once, anything else waits for typing to pause.
    @objc private func searchFieldChanged(_ sender: NSSearchField) {
        guard !converting else { return }
        let text = sender.stringValue
        searchWork?.cancel()
        searchWork = nil
        guard !text.isEmpty else {
            onSearchText?("")
            return
        }
        let work = DispatchWorkItem { [weak self] in
            guard let self else { return }
            self.searchWork = nil
            self.onSearchText?(text)
        }
        searchWork = work
        DispatchQueue.main.asyncAfter(deadline: .now() + Self.searchDelay, execute: work)
    }

    /// Return in the field selects the first result, without waiting for
    /// the pause; ⌥↩ searches in the user's own words while that is
    /// offered.
    func control(_ control: NSControl, textView: NSTextView, doCommandBy commandSelector: Selector) -> Bool {
        guard let field = control as? NSSearchField else { return false }
        if commandSelector == #selector(NSResponder.insertNewlineIgnoringFieldEditor(_:)), ownWords {
            if !converting {
                onSearchOwnWords?(field.stringValue)
            }
            return true
        }
        if commandSelector == #selector(NSResponder.cancelOperation(_:)) {
            // Escape ends the search at once: the field empties, gives up
            // the keyboard and folds to the lens.
            guard !converting else { return true }
            searchWork?.cancel()
            searchWork = nil
            let hadText = !field.stringValue.isEmpty
            field.stringValue = ""
            if hadText {
                onSearchText?("")
            }
            searchItem?.endSearchInteraction()
            scheduleSearchCollapse()
            return true
        }
        guard commandSelector == #selector(NSResponder.insertNewline(_:)) else {
            return false
        }
        guard !converting else { return true }
        searchWork?.cancel()
        searchWork = nil
        onSearchReturn?(field.stringValue)
        return true
    }

    /// The field was cleared (its ✕, or Escape).
    func searchFieldDidEndSearching(_ sender: NSSearchField) {
        guard !converting else { return }
        searchWork?.cancel()
        searchWork = nil
        onSearchText?("")
        // The ✕ gives up the keyboard as it clears; a field emptied by
        // typing keeps it and folds only once it loses it.
        scheduleSearchCollapse()
    }

    /// The field lost the keyboard: an empty field folds to the lens.
    func controlTextDidEndEditing(_ obj: Notification) {
        scheduleSearchCollapse()
    }

    /// The Contacts button of the main window's toolbars in every mode
    /// (a macOS deviation, macos/README.md; GTK and Windows keep it in
    /// their menus): opens the one Contacts window through the responder
    /// chain, as Window ▸ Contacts does (`Action.showContacts`, the
    /// application delegate validates it). No shortcut. It gives way to
    /// the overflow menu before the mode's own items.
    static func contactsItem(_ id: NSToolbarItem.Identifier) -> NSToolbarItem {
        let label = ContactBook.windowTitle
        let it = NSToolbarItem(itemIdentifier: id)
        it.image = Icon.symbol("person.text.rectangle", size: .toolbar, description: label)
        it.label = label
        it.paletteLabel = label
        it.toolTip = label
        it.isBordered = true
        it.target = nil
        it.action = Action.showContacts
        it.visibilityPriority = .low
        return it
    }

    private func button(_ id: NSToolbarItem.Identifier, image: NSImage, label: String, action: Selector) -> NSToolbarItem {
        let it = NSToolbarItem(itemIdentifier: id)
        it.image = image
        it.label = label
        it.paletteLabel = label
        it.toolTip = label
        it.isBordered = true
        it.target = nil
        it.action = action
        return it
    }

    /// Marks the items More Actions adds for what the toolbar lacks.
    private static let missingMarker = NSUserInterfaceItemIdentifier("malachi.toolbar.missing")

    /// More Actions opens: the actions of the default toolbar that the
    /// user's arrangement does not show come first
    /// (`Toolbar.missingActions`, T8), validated by the window like the
    /// menu bar's. AppKit's own overflow menu (») lists what did not fit.
    func menuNeedsUpdate(_ menu: NSMenu) {
        guard let customization, let toolbar,
              menu === (items[ID.moreActions] as? NSMenuToolbarItem)?.menu else { return }
        while let first = menu.items.first, first.identifier == Self.missingMarker {
            menu.removeItem(first)
        }
        let layout = toolbar.items.compactMap { customization.modelID($0.itemIdentifier) }
        // eWay-CRM only while its button would show (an enabled account).
        var f = features
        if crmController?()?.offer.shown != true {
            f.have.remove(.crm)
        }
        var added: [NSMenuItem] = []
        for id in Toolbar.missingActions(.mail, layout, f) {
            guard let item = missingItem(id) else { continue }
            item.identifier = Self.missingMarker
            added.append(item)
        }
        guard !added.isEmpty else { return }
        let sep = NSMenuItem.separator()
        sep.identifier = Self.missingMarker
        for (i, item) in (added + [sep]).enumerated() {
            menu.insertItem(item, at: i)
        }
    }

    private func missingItem(_ id: Toolbar.ID) -> NSMenuItem? {
        func item(_ title: String, _ action: Selector) -> NSMenuItem {
            NSMenuItem(title: title, action: action, keyEquivalent: "")
        }
        switch id {
        case .reply: return item(L10n.T("Reply"), Action.reply)
        case .replyAll: return item(L10n.T("Reply All"), Action.replyAll)
        case .forward: return item(L10n.T("Forward"), Action.forward)
        case .archive: return item(L10n.T("Archive"), Action.archive)
        case .trash: return item(L10n.T("Move to Trash"), Action.moveToTrash)
        case .star: return item(L10n.T("Star"), Action.toggleFlag)
        case .crm: return item(CRM.saveMenuLabel(1), Action.crmSave)
        default: return nil
        }
    }

    /// The hamburger of the folder header bar (window.blp `primary_menu`),
    /// minus Keyboard Shortcuts, which the GTK UI never implemented.
    /// The More Actions menu (window.blp `message_menu_model`).
    private func messageMenu() -> NSMenu {
        let m = NSMenu()
        // Validated like the Message menu's item (disabled where there is
        // no junk folder to go to; ActionPresentation hides nothing in menus).
        m.addItem(withTitle: L10n.T("Mark as Junk"), action: Action.markAsJunk, keyEquivalent: "")
        m.addItem(.separator())
        m.addItem(withTitle: mn(L10n.T("Mark as _Unread")), action: Action.markAsUnread, keyEquivalent: "")
        m.addItem(withTitle: mn(L10n.T("Mark as _Read")), action: Action.markAsRead, keyEquivalent: "")
        m.addItem(.separator())
        m.addItem(withTitle: mn(L10n.T("Load _Images")), action: Action.loadImages, keyEquivalent: "")
        m.addItem(withTitle: mn(L10n.T("Always Load Images From This _Sender")), action: Action.trustSender, keyEquivalent: "")
        return m
    }
}

/// The star of the message header bar (window.blp `star_button`): a
/// plain push button whose image (yellow and filled when flagged) and
/// tooltip follow the flagged state
/// (actions.go `setStar`). Validation goes through the responder chain like
/// an image item's would.
@MainActor
final class StarToolbarItem: NSToolbarItem {
    let button: NSButton

    /// The state the button shows; set from validation.
    var flagged = false {
        didSet {
            guard flagged != oldValue else { return }
            applyState()
        }
    }

    override init(itemIdentifier: NSToolbarItem.Identifier) {
        button = NSButton(image: Icon.star, target: nil, action: Action.toggleFlag)
        super.init(itemIdentifier: itemIdentifier)
        // A plain push button: an "on" state would fill the Liquid Glass
        // bezel with the accent colour, as for a primary action; the
        // flagged state is the image's alone (macos/README.md).
        button.setButtonType(.momentaryPushIn)
        button.bezelStyle = .toolbar
        button.imageScaling = .scaleProportionallyDown
        button.imagePosition = .imageOnly
        button.setAccessibilityLabel(L10n.T("Star"))
        view = button
        label = L10n.T("Star")
        paletteLabel = L10n.T("Star")
        target = nil
        action = Action.toggleFlag
        applyState()
    }

    override func validate() {
        guard let action,
              let target = NSApp.target(forAction: action, to: target, from: self),
              let validator = target as? any NSToolbarItemValidation
        else {
            isEnabled = false
            return
        }
        isEnabled = validator.validateToolbarItem(self)
    }

    private func applyState() {
        button.image = flagged ? Icon.starFilled : Icon.star
        button.contentTintColor = flagged ? .systemYellow : nil
        let t = flagged ? L10n.T("Unstar") : L10n.T("Star")
        toolTip = t
        button.toolTip = t
        label = t
    }
}

/// A toolbar item whose enabled state comes from its own provider, not
/// from its action's target (Undo: `MainWindow` answers `undo:` but
/// validates only menu items).
@MainActor
final class ProvidedToolbarItem: NSToolbarItem {
    var enabledProvider: (@MainActor () -> Bool)?

    override func validate() {
        guard let enabledProvider else {
            super.validate()
            return
        }
        isEnabled = enabledProvider()
    }
}

/// Move To in the toolbar: the selection's folders, built when the menu
/// opens (`MessageContextMenu.moveToMenu`); disabled without them.
@MainActor
final class MoveToToolbarItem: NSMenuToolbarItem, NSMenuDelegate {
    var provider: (@MainActor () -> NSMenu?)?
    var enabledProvider: (@MainActor () -> Bool)?

    override init(itemIdentifier: NSToolbarItem.Identifier) {
        super.init(itemIdentifier: itemIdentifier)
        let m = NSMenu()
        m.autoenablesItems = false
        m.delegate = self
        menu = m
    }

    override func validate() {
        isEnabled = enabledProvider?() ?? false
    }

    func menuNeedsUpdate(_ menu: NSMenu) {
        menu.removeAllItems()
        guard let source = provider?() else { return }
        for item in source.items {
            source.removeItem(item)
            menu.addItem(item)
        }
    }
}
