// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The unified toolbar of the main window: the three GTK header bars
/// (window.blp) as one `NSToolbar`, cut into the panes' sections by two
/// `NSTrackingSeparatorToolbarItem`s on the split view's dividers. Items
/// act through the responder chain and are validated by whichever
/// responder owns the action (the window controller).
@MainActor
final class MainToolbar: NSObject, NSToolbarDelegate {
    enum ID {
        static let toolbar = NSToolbar.Identifier("main")
        static let newMessage = NSToolbarItem.Identifier("newMessage")
        static let refresh = NSToolbarItem.Identifier("refresh")
        static let search = NSToolbarItem.Identifier("search")
        static let listSeparator = NSToolbarItem.Identifier("listSeparator")
        static let reply = NSToolbarItem.Identifier("reply")
        static let replyAll = NSToolbarItem.Identifier("replyAll")
        static let forward = NSToolbarItem.Identifier("forward")
        static let trash = NSToolbarItem.Identifier("trash")
        static let junk = NSToolbarItem.Identifier("junk")
        static let archive = NSToolbarItem.Identifier("archive")
        static let star = NSToolbarItem.Identifier("star")
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
    /// divider it folds.
    static let defaultItems: [NSToolbarItem.Identifier] = [
        .flexibleSpace, .toggleSidebar,
        .sidebarTrackingSeparator,
        ID.newMessage, ID.refresh, .flexibleSpace,
        ID.listSeparator,
        ID.reply, ID.replyAll, ID.forward, .flexibleSpace,
        ID.trash, ID.junk, ID.archive, ID.star, ID.moreActions,
    ]

    /// The items of the message section, for the message window's toolbar.
    static let messageSectionItems: [NSToolbarItem.Identifier] = [
        ID.reply, ID.replyAll, ID.forward, .flexibleSpace,
        ID.trash, ID.junk, ID.archive, ID.star, ID.moreActions,
    ]

    private weak var splitView: NSSplitView?
    private var items: [NSToolbarItem.Identifier: NSToolbarItem] = [:]

    /// - Parameter splitView: the split view whose dividers 0 and 1 the
    ///   tracking separators follow; nil for a toolbar without sections.
    init(splitView: NSSplitView?) {
        self.splitView = splitView
    }

    /// A configured toolbar with this object as its delegate.
    func makeToolbar(identifier: NSToolbar.Identifier = ID.toolbar) -> NSToolbar {
        let tb = NSToolbar(identifier: identifier)
        tb.delegate = self
        tb.displayMode = .iconOnly
        tb.allowsUserCustomization = false
        tb.autosavesConfiguration = false
        return tb
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

    // MARK: NSToolbarDelegate

    func toolbarDefaultItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        splitView == nil ? Self.messageSectionItems : Self.defaultItems
    }

    func toolbarAllowedItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        toolbarDefaultItemIdentifiers(toolbar) + [ID.search]
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
        case ID.listSeparator:
            guard let splitView else { return nil }
            return NSTrackingSeparatorToolbarItem(identifier: id, splitView: splitView, dividerIndex: 1)
        case ID.refresh:
            return button(id, image: Icon.refresh, label: L10n.T("Check for New Mail"), action: Action.checkForNewMail)
        case ID.search:
            // Reserved until the daemon implements search.query.
            let it = NSSearchToolbarItem(itemIdentifier: id)
            it.label = L10n.T("Search")
            return it
        case ID.reply:
            return button(id, image: Icon.reply, label: L10n.T("Reply"), action: Action.reply)
        case ID.replyAll:
            return button(id, image: Icon.replyAll, label: L10n.T("Reply All"), action: Action.replyAll)
        case ID.forward:
            return button(id, image: Icon.forward, label: L10n.T("Forward"), action: Action.forward)
        case ID.trash:
            return button(id, image: Icon.trash, label: L10n.T("Move to Trash"), action: Action.moveToTrash)
        case ID.junk:
            return button(id, image: Icon.junk, label: L10n.T("Mark as Junk"), action: Action.markAsJunk)
        case ID.archive:
            return button(id, image: Icon.archive, label: L10n.T("Archive"), action: Action.archive)
        case ID.star:
            return StarToolbarItem(itemIdentifier: id)
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
        m.addItem(withTitle: mn(L10n.T("Mark as _Unread")), action: Action.markAsUnread, keyEquivalent: "")
        m.addItem(withTitle: mn(L10n.T("Mark as _Read")), action: Action.markAsRead, keyEquivalent: "")
        m.addItem(.separator())
        m.addItem(withTitle: mn(L10n.T("Load _Images")), action: Action.loadImages, keyEquivalent: "")
        m.addItem(withTitle: mn(L10n.T("Always Load Images From This _Sender")), action: Action.trustSender, keyEquivalent: "")
        return m
    }
}

/// The star of the message header bar (window.blp `star_button`): a
/// push-on/push-off button whose image and tooltip follow the flagged state
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
        button.setButtonType(.pushOnPushOff)
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
        button.state = flagged ? .on : .off
        let t = flagged ? L10n.T("Unstar") : L10n.T("Star")
        toolTip = t
        button.toolTip = t
        label = t
    }
}
