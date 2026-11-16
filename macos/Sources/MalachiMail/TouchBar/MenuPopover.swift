// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// A Touch Bar popover that mirrors a menu of the window
/// (docs/plans/touch-bar.md §4.2, T9): Move To's folders, the Assistant
/// menu's message actions, Change Status's transitions. The menu is the
/// one the toolbar or the More Actions menu shows, built by the same code,
/// so the popover offers what the menu offers: the items are read when the
/// popover opens (the menu's delegate fills it first, `menuNeedsUpdate`),
/// validated as a menu validates them (the window may retitle or hide an
/// item, `validate`), and a chosen item is sent as the menu would send it
/// (its action to its target, the item as the sender). A menu that fills
/// itself later (Change Status loads the transitions from the site) is
/// followed: every item it adds shows. Separators, section headers and
/// hidden items are left out; an item without an action (a problem, a
/// "Loading…") is a label.
///
/// `scrubber` lays the items out in a scrolling `NSScrubber` (the folders,
/// each named with its parents: "Projects / Budget"); otherwise each is a
/// button. The popover closes after a choice.
@MainActor
final class MenuPopover: NSObject, NSTouchBarDelegate, NSScrubberDataSource, NSScrubberDelegate, @preconcurrency NSScrubberFlowLayoutDelegate {
    let item: NSPopoverTouchBarItem
    /// The collapsed button on the bar (its title follows the status).
    let button: NSButton

    /// The menu to show, made or filled anew on each opening.
    var menu: @MainActor () -> NSMenu? = { nil }
    /// Whether a menu item is enabled; may retitle or hide it.
    var validate: @MainActor (NSMenuItem) -> Bool = { $0.isEnabled }
    /// Whether a menu item belongs on the popover at all.
    var includes: @MainActor (NSMenuItem) -> Bool = { _ in true }
    /// Runs instead of the popover when it returns true (the Assistant
    /// with In App opens the panel, as the toolbar's ✦ does).
    var instead: @MainActor () -> Bool = { false }

    private let usesScrubber: Bool
    private let content = NSTouchBar()
    /// The menu shown and the entries taken from it.
    private var shown: NSMenu?
    private var entries: [Entry] = []
    /// Changes the identifiers of each new set of items: AppKit keeps an
    /// item made for an identifier.
    private var generation = 0
    private var observer: NSObjectProtocol?
    private var rebuildQueued = false

    private struct Entry {
        let menuItem: NSMenuItem
        let title: String
        let enabled: Bool
        /// A label, not a choice.
        let info: Bool
    }

    private static let scrubberID = NSTouchBarItem.Identifier("io.github.schotek.Malachi.touchbar.popover.scrubber")
    private static let textItem = NSUserInterfaceItemIdentifier("text")

    init(_ identifier: NSTouchBarItem.Identifier, image: NSImage?, title: String, label: String, scrubber: Bool) {
        usesScrubber = scrubber
        item = NSPopoverTouchBarItem(identifier: identifier)
        if let image {
            button = title.isEmpty ? NSButton(image: image, target: nil, action: nil)
                : NSButton(title: title, image: image, target: nil, action: nil)
        } else {
            button = NSButton(title: title, target: nil, action: nil)
        }
        super.init()
        // An icon alone: no "Button" title, the label for VoiceOver.
        if title.isEmpty {
            button.title = ""
        }
        button.setAccessibilityLabel(label)
        button.target = self
        button.action = #selector(open(_:))
        item.collapsedRepresentation = button
        item.customizationLabel = label
        item.popoverTouchBar = content
        content.delegate = self
    }

    // MARK: Opening

    @objc private func open(_ sender: Any?) {
        if instead() {
            return
        }
        close()
        guard let m = menu() else { return }
        shown = m
        m.delegate?.menuNeedsUpdate?(m)
        observer = NotificationCenter.default.addObserver(
            forName: NSMenu.didAddItemNotification, object: m, queue: .main
        ) { [weak self] _ in
            MainActor.assumeIsolated { self?.queueRebuild() }
        }
        rebuild()
        item.showPopover(sender)
    }

    /// The menu filled itself later (several items at once): one rebuild.
    private func queueRebuild() {
        guard !rebuildQueued else { return }
        rebuildQueued = true
        DispatchQueue.main.async { [weak self] in
            MainActor.assumeIsolated {
                self?.rebuildQueued = false
                self?.rebuild()
            }
        }
    }

    /// Ends what the last opening started: the menu is told it closed
    /// (Change Status drops a late answer).
    private func close() {
        if let observer {
            NotificationCenter.default.removeObserver(observer)
        }
        observer = nil
        if let m = shown {
            m.delegate?.menuDidClose?(m)
        }
        shown = nil
    }

    private func rebuild() {
        guard let m = shown else { return }
        entries = Self.entries(of: m, scrubber: usesScrubber, includes: includes, validate: validate)
        generation += 1
        content.defaultItemIdentifiers = usesScrubber
            ? [Self.scrubberID]
            : entries.indices.map { NSTouchBarItem.Identifier("io.github.schotek.Malachi.touchbar.popover.\(generation).\($0)") }
        if usesScrubber, let s = content.item(forIdentifier: Self.scrubberID)?.view as? NSScrubber {
            s.reloadData()
        }
    }

    /// The menu's items as the popover shows them. With `scrubber` a
    /// folder is named with its parents, from the items' indentation.
    private static func entries(
        of m: NSMenu, scrubber: Bool, includes: @MainActor (NSMenuItem) -> Bool, validate: @MainActor (NSMenuItem) -> Bool
    ) -> [Entry] {
        var out: [Entry] = []
        var parents: [String] = []
        for it in m.items where !it.isSeparatorItem && !it.isSectionHeader && includes(it) {
            let enabled = it.action != nil && validate(it)
            if it.isHidden {
                continue
            }
            // A checked item (the case's current state) shows its mark.
            var title = it.state == .on ? "✓ " + it.title : it.title
            if scrubber {
                let depth = max(0, it.indentationLevel)
                parents = Array(parents.prefix(depth))
                title = (parents + [it.title]).joined(separator: " / ")
                parents.append(it.title)
            }
            out.append(Entry(menuItem: it, title: title, enabled: enabled, info: it.action == nil))
        }
        return out
    }

    private func choose(_ index: Int) {
        guard entries.indices.contains(index) else { return }
        let e = entries[index]
        guard e.enabled, !e.info, let action = e.menuItem.action else { return }
        NSApp.sendAction(action, to: e.menuItem.target, from: e.menuItem)
        item.dismissPopover(nil)
        close()
    }

    // MARK: NSTouchBarDelegate

    func touchBar(_ touchBar: NSTouchBar, makeItemForIdentifier identifier: NSTouchBarItem.Identifier) -> NSTouchBarItem? {
        if identifier == Self.scrubberID {
            let s = NSScrubber()
            s.register(NSScrubberTextItemView.self, forItemIdentifier: Self.textItem)
            let layout = NSScrubberFlowLayout()
            layout.itemSpacing = 6
            s.scrubberLayout = layout
            s.mode = .free
            s.selectionBackgroundStyle = .roundedBackground
            s.dataSource = self
            s.delegate = self
            let it = NSCustomTouchBarItem(identifier: identifier)
            it.view = s
            return it
        }
        guard let index = Int(identifier.rawValue.split(separator: ".").last ?? ""), entries.indices.contains(index) else {
            return nil
        }
        let e = entries[index]
        if e.info {
            let label = NSTextField(labelWithString: e.title)
            label.textColor = .secondaryLabelColor
            label.lineBreakMode = .byTruncatingTail
            let it = NSCustomTouchBarItem(identifier: identifier)
            it.view = label
            return it
        }
        let b = NSButtonTouchBarItem(identifier: identifier, title: e.title, target: self, action: #selector(chooseButton(_:)))
        b.isEnabled = e.enabled
        return b
    }

    @objc private func chooseButton(_ sender: Any?) {
        guard let b = sender as? NSButtonTouchBarItem,
              let index = Int(b.identifier.rawValue.split(separator: ".").last ?? "") else { return }
        choose(index)
    }

    // MARK: Scrubber

    func numberOfItems(for scrubber: NSScrubber) -> Int {
        entries.count
    }

    func scrubber(_ scrubber: NSScrubber, viewForItemAt index: Int) -> NSScrubberItemView {
        let view = scrubber.makeItem(withIdentifier: Self.textItem, owner: nil) as? NSScrubberTextItemView
            ?? NSScrubberTextItemView()
        let e = entries[index]
        view.title = e.title
        view.textField.textColor = e.enabled ? .labelColor : .disabledControlTextColor
        return view
    }

    func scrubber(_ scrubber: NSScrubber, layout: NSScrubberFlowLayout, sizeForItemAt itemIndex: Int) -> NSSize {
        guard entries.indices.contains(itemIndex) else { return NSSize(width: 60, height: 30) }
        let width = (entries[itemIndex].title as NSString).size(withAttributes: [.font: NSFont.systemFont(ofSize: 0)]).width
        return NSSize(width: ceil(width) + 28, height: 30)
    }

    func scrubber(_ scrubber: NSScrubber, didSelectItemAt selectedIndex: Int) {
        // A disabled folder (the current one, a container) is not chosen;
        // the selection goes again so that it does not look chosen.
        scrubber.selectedIndex = -1
        choose(selectedIndex)
    }
}
