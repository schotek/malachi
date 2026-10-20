// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// A window with eWay-CRM actions: the main window (the selection) and a
/// message window (its message). The toolbar item and the menus ask it.
@MainActor
protocol CRMWindow: AnyObject {
    /// Nil until the application installed it.
    var crm: CRMController? { get }
}

/// The eWay-CRM button of the main window's and the message windows'
/// toolbars, beside the Assistant (docs/eway.md D20, the approved
/// wireframe; GTK `crm_button`): "Save to eWay-CRM" with the CRM's symbol,
/// which opens the dialog; disabled with a spinner while a save or removal
/// runs; "In eWay-CRM" with a check mark and a chevron once saved, a
/// pull-down of `CRM.savedMenu()`. Hidden without an eWay-CRM account or
/// for a message that cannot be saved (`CRM.Button`); for several rows its
/// label counts them. Its window's `CRMController` decides everything; the
/// item asks it on every validation and when the window says the state
/// changed (`refresh`). macOS 14 has no hidden toolbar items: there the
/// item is disabled and empty instead.
@MainActor
final class CRMToolbarItem: NSToolbarItem {
    private let stack = NSStackView()
    private let spinner = NSProgressIndicator()
    private let saveButton = NSButton(title: "", target: nil, action: nil)
    private let savedButton = NSPopUpButton(frame: .zero, pullsDown: true)
    private var shown: CRM.Button?

    override init(itemIdentifier: NSToolbarItem.Identifier) {
        super.init(itemIdentifier: itemIdentifier)
        spinner.style = .spinning
        spinner.controlSize = .small
        spinner.isIndeterminate = true
        spinner.isDisplayedWhenStopped = false
        saveButton.bezelStyle = .toolbar
        saveButton.image = Icon.crm
        saveButton.imagePosition = .imageLeading
        saveButton.target = self
        saveButton.action = #selector(saveClicked(_:))
        savedButton.bezelStyle = .toolbar
        savedButton.target = self
        (savedButton.cell as? NSPopUpButtonCell)?.arrowPosition = .arrowAtBottom
        stack.orientation = .horizontal
        stack.alignment = .centerY
        stack.spacing = 4
        stack.addArrangedSubview(spinner)
        stack.addArrangedSubview(saveButton)
        stack.addArrangedSubview(savedButton)
        view = stack
        label = CRM.brand
        paletteLabel = CRM.brand
        apply(CRM.buttonFor(.hidden))
    }

    /// The window's controller, given by the toolbar that made the item
    /// (`MainToolbar.crmController`): a hidden toolbar item has no view in
    /// the window, so the window could not be found through `stack.window`
    /// and a button hidden at start stayed hidden (Gate B, 2026-10-17).
    /// The view's window is the fallback (not the key window: a message
    /// window's item is its own).
    var controllerProvider: (@MainActor () -> CRMController?)?

    private var controller: CRMController? {
        controllerProvider?() ?? (stack.window?.windowController as? CRMWindow)?.crm
    }

    override func validate() {
        refresh()
    }

    /// Shows what the window's controller says now.
    func refresh() {
        apply(controller?.button ?? CRM.buttonFor(.hidden))
    }

    private func apply(_ b: CRM.Button) {
        guard b != shown else { return }
        shown = b
        let hidden = b.state == .hidden
        if #available(macOS 15, *) {
            if isHidden != hidden {
                isHidden = hidden
            }
        }
        isEnabled = !hidden
        toolTip = b.tooltip.isEmpty ? nil : b.tooltip
        spinner.isHidden = b.state != .saving
        if b.state == .saving {
            spinner.startAnimation(nil)
        } else {
            spinner.stopAnimation(nil)
        }
        saveButton.isHidden = hidden || b.menu
        savedButton.isHidden = hidden || !b.menu
        // Every string here is a msgid's or the count; nothing from mail.
        saveButton.title = b.label
        saveButton.toolTip = b.tooltip
        saveButton.isEnabled = b.enabled
        saveButton.setAccessibilityLabel(b.label)
        if b.menu {
            let menu = NSMenu()
            let head = NSMenuItem(title: b.label, action: nil, keyEquivalent: "")
            head.image = Icon.symbol(Icon.crmSavedSymbol, size: .regular, description: b.label)
            menu.addItem(head)
            for (i, it) in CRM.savedMenu().enumerated() {
                let m = NSMenuItem(title: it.label, action: #selector(menuChosen(_:)), keyEquivalent: "")
                m.target = self
                m.tag = i
                menu.addItem(m)
            }
            savedButton.menu = menu
            savedButton.toolTip = b.tooltip
            savedButton.isEnabled = b.enabled
        }
        label = b.label.isEmpty ? CRM.brand : b.label
    }

    @objc private func saveClicked(_ sender: Any?) {
        controller?.save()
        refresh()
    }

    @objc private func menuChosen(_ sender: NSMenuItem) {
        let items = CRM.savedMenu()
        guard items.indices.contains(sender.tag), let c = controller else { return }
        switch items[sender.tag].id {
        case .open: c.open()
        case .editLinks: c.editLinks()
        case .remove: c.remove()
        }
    }
}

/// The eWay-CRM items of the menu bar's Message menu (after Forward):
/// Save to eWay-CRM…, and for one saved message Open in eWay-CRM, Edit
/// Links… and Remove from eWay-CRM, sent through the responder chain to the
/// key window, which validates them (`CRMMenuItems.validate`). The group
/// and its separator are hidden while no eWay-CRM account is enabled.
@MainActor
enum CRMMenuItems {
    static let separator = NSUserInterfaceItemIdentifier("malachi.menu.crm.separator")

    /// Appends the group to `menu`.
    static func add(to menu: NSMenu) {
        let sep = NSMenuItem.separator()
        sep.identifier = separator
        menu.addItem(sep)
        menu.addItem(NSMenuItem(title: CRM.saveMenuLabel(1), action: Action.crmSave, keyEquivalent: ""))
        for it in CRM.savedMenu() {
            menu.addItem(NSMenuItem(title: it.label, action: selector(it.id), keyEquivalent: ""))
        }
    }

    static func selector(_ id: CRM.MenuID) -> Selector {
        switch id {
        case .open: return Action.crmOpen
        case .editLinks: return Action.crmEditLinks
        case .remove: return Action.crmRemove
        }
    }

    /// Validates an item of the group for `crm` (nil: the window has no
    /// CRM): Save is shown unless one saved message has the saved-state
    /// items, titled with the count; the separator goes with the group.
    static func validate(_ item: NSMenuItem, _ action: Selector, _ crm: CRMController?) -> Bool {
        let offer = crm?.offer ?? CRM.Offer()
        let savedItems = offer.shown && offer.saved && offer.count == 1
        var hidden: Bool
        var enabled: Bool
        switch action {
        case Action.crmSave:
            hidden = !offer.shown || savedItems
            enabled = offer.enabled && crm?.button.state != .saving
            item.title = CRM.saveMenuLabel(offer.count)
        default:
            hidden = !savedItems
            enabled = savedItems && crm?.button.state == .saved
        }
        // Inside a context menu (no menu bar) nothing is hidden here: the
        // context menu holds only what `ContextMenu.messageItems` offers.
        if item.menu?.supermenu != nil {
            if item.isHidden != hidden {
                item.isHidden = hidden
            }
            if let menu = item.menu, let sep = menu.items.first(where: { $0.identifier == separator }) {
                let none = !offer.shown
                if sep.isHidden != none {
                    sep.isHidden = none
                }
            }
        } else {
            hidden = false
        }
        return enabled && !hidden
    }
}

/// The Message menu's delegate for the eWay-CRM group: when the menu opens
/// the group shows for a key window with eWay-CRM actions (the window's
/// validation then hides what does not apply, `CRMMenuItems.validate`) and
/// hides for any other (a compose window answers none of them).
@MainActor
final class CRMMenuDelegate: NSObject, NSMenuDelegate {
    func menuNeedsUpdate(_ menu: NSMenu) {
        let has = (NSApp.keyWindow?.windowController as? CRMWindow)?.crm != nil
        for it in menu.items {
            let ours = it.identifier == CRMMenuItems.separator || it.action.map { Action.crmActions.contains($0) } == true
            if ours, it.isHidden == has {
                it.isHidden = !has
            }
        }
    }
}
