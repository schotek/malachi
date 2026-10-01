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
final class MainToolbar: NSObject, NSToolbarDelegate, NSSearchFieldDelegate, NSMenuItemValidation {
    enum ID {
        static let toolbar = NSToolbar.Identifier("main")
        static let newMessage = NSToolbarItem.Identifier("newMessage")
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
        static let moreActions = NSToolbarItem.Identifier("moreActions")
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
        .flexibleSpace, .toggleSidebar,
        .sidebarTrackingSeparator,
        ID.newMessage, ID.filter, ID.refresh, .flexibleSpace,
        ID.listSeparator,
        ID.reply, ID.replyAll, ID.forward, .flexibleSpace,
        ID.archive, ID.trash, .space, ID.star, ID.assistant, ID.moreActions, ID.search,
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
        ID.archive, ID.trash, .space, ID.star, ID.assistant, ID.moreActions,
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

    /// - Parameters:
    ///   - splitView: the split view whose dividers 0 and 1 the tracking
    ///     separators follow; nil for a toolbar without sections.
    ///   - assistantMenu: the Assistant button's menu, nil for none.
    ///   - showsAssistant: whether the button starts in the toolbar.
    ///   - showsPanel: whether the inspector section (the assistant
    ///     panel's toggle) starts in the toolbar.
    init(splitView: NSSplitView?, assistantMenu: AssistantMenu? = nil, showsAssistant: Bool = false, showsPanel: Bool = false) {
        self.splitView = splitView
        self.assistantMenu = assistantMenu
        self.showsAssistant = showsAssistant
        self.showsPanel = showsPanel && splitView != nil
        super.init()
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
        return tb
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
        let current = toolbar.items.firstIndex { $0.itemIdentifier == ID.listSeparator }
        if visible {
            guard current == nil else { return }
            let at = toolbar.items.firstIndex { $0.itemIdentifier == ID.reply } ?? toolbar.items.count
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
    }

    @objc private func assistantPanelClicked(_ sender: Any?) {
        onOpenAssistantPanel?()
    }

    /// Puts the Assistant button into `toolbar` (right before More
    /// Actions) or takes it out, as the `assistant-menu` setting changes.
    func setAssistant(visible: Bool, in toolbar: NSToolbar) {
        showsAssistant = visible
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
        allItems
    }

    func toolbar(
        _ toolbar: NSToolbar, itemForItemIdentifier id: NSToolbarItem.Identifier, willBeInsertedIntoToolbar flag: Bool
    ) -> NSToolbarItem? {
        if let cached = items[id] {
            return cached
        }
        let item = make(id)
        if let item {
            items[id] = item
        }
        return item
    }

    private func make(_ id: NSToolbarItem.Identifier) -> NSToolbarItem? {
        switch id {
        case ID.newMessage:
            let it = button(id, image: Icon.newMessage, label: mn(L10n.T("_New Message")), action: Action.newMessage)
            it.toolTip = mn(L10n.T("_New Message")) + " (⌘N)"
            // Ahead of the window title (the folder's name), like Finder's
            // back and forward buttons.
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
        case ID.moreActions:
            let it = NSMenuToolbarItem(itemIdentifier: id)
            it.image = Icon.moreActions
            it.label = L10n.T("More Actions")
            it.toolTip = L10n.T("More Actions")
            it.showsIndicator = false
            it.isBordered = true
            it.menu = messageMenu()
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
            self.swapSearchItems()
        }
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
