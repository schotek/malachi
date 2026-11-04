// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The user's arrangement of one of the main window's toolbars
/// (docs/plans/toolbar-customization.md, M2): the part of the delegates
/// (`MainToolbar`, `BoardToolbar`, `CalendarToolbar`) that AppKit's
/// customization palette needs, over the pure model `Toolbar`
/// (ui/internal/toolbar). AppKit does not save the arrangement
/// (`autosavesConfiguration` stays off): the items the palette leaves in
/// the toolbar are written to the toolbar's settings key
/// (`Settings.setToolbarLayout`) and the toolbar is built from it
/// (`defaultIdentifiers`, which AppKit asks for while it builds a toolbar
/// with no saved configuration).
///
/// Only from macOS 15 (T6): the delegate's `toolbarImmovableItemIdentifiers`
/// and `toolbar(_:itemIdentifier:canBeInsertedAt:)` (macOS 13) keep the
/// fixed items and the sections' separators in place, but a conditional
/// item can keep its place hidden only with `NSToolbarItem.isHidden`, and
/// the palette offers the display mode only with
/// `allowsDisplayModeCustomization`, both macOS 15. On macOS 14 the
/// toolbar is the default one and cannot be customized, as before.
///
/// From macOS 15 a conditional item (✦, Triage; T7) stays in the toolbar
/// and is hidden while its feature is missing, so that saving what the
/// toolbar holds keeps its place; on macOS 14 it comes and goes as before.
/// While a section's separator or a whole section is out of the toolbar
/// (Mail's list pane folded, the Board List without its detail) the
/// toolbar cannot be customized: what it shows is not the arrangement.
@MainActor
final class ToolbarCustomization {
    /// Whether this Mac's AppKit can keep the fixed items in place (macOS 15+).
    static var isAvailable: Bool {
        if #available(macOS 15, *) {
            return true
        }
        return false
    }

    let bar: Toolbar.Bar
    private let settings: Settings
    /// The toolbar's own identifiers of the model's tokens that are not
    /// spelled the same (AppKit's items, the sections' separators).
    private let native: [Toolbar.ID: NSToolbarItem.Identifier]
    private let model: [NSToolbarItem.Identifier: Toolbar.ID]
    /// Items the toolbar may hold that are not the user's (the assistant
    /// panel's inspector separator and toggle): allowed and immovable; the
    /// first of them starts the toolbar's last section, which is never
    /// saved and takes no item of the user's.
    private let extras: [NSToolbarItem.Identifier]
    /// What the window has now; the delegate installs it.
    var features: @MainActor () -> Toolbar.Features = { Toolbar.Features() }
    /// Items the toolbar holds for now only (a search field ⌘F put in), never saved.
    var transient: @MainActor () -> Set<NSToolbarItem.Identifier> = { [] }
    /// The palette closed (the window rebinds the tracking separators).
    var onPaletteEnd: (@MainActor (NSToolbar) -> Void)?

    private var saveScheduled = false
    /// The palette changed the items since it opened (`itemsChanged`).
    private var dirty = false
    private var sheetObserver: (any NSObjectProtocol)?
    private var displayToken: Settings.ChangeToken?
    private var layoutToken: Settings.ChangeToken?
    /// The arrangement is being written from these toolbars (no reload).
    private var saving = false
    private var toolbars: [NSToolbar] = []

    /// - Parameters:
    ///   - separators: the toolbar's identifier of each section's separator.
    ///   - extras: items of the toolbar that are not the user's.
    init(bar: Toolbar.Bar, settings: Settings, separators: [Toolbar.Section: NSToolbarItem.Identifier],
         extras: [NSToolbarItem.Identifier] = []) {
        self.bar = bar
        self.settings = settings
        self.extras = extras
        var native: [Toolbar.ID: NSToolbarItem.Identifier] = [
            .space: .space, .flexibleSpace: .flexibleSpace, .toggleSidebar: .toggleSidebar,
        ]
        for (s, id) in separators {
            native[Toolbar.separator(s)] = id
        }
        self.native = native
        var model: [NSToolbarItem.Identifier: Toolbar.ID] = [:]
        for (k, v) in native {
            model[v] = k
        }
        self.model = model
    }

    /// The toolbar's identifier of a model token.
    func nativeID(_ id: Toolbar.ID) -> NSToolbarItem.Identifier {
        native[id] ?? NSToolbarItem.Identifier(id.rawValue)
    }

    /// The model's token of a toolbar item; nil for one that is not the
    /// user's (`extras`) or not in the catalog.
    func modelID(_ id: NSToolbarItem.Identifier) -> Toolbar.ID? {
        if let m = model[id] {
            return m
        }
        let m = Toolbar.ID(id.rawValue)
        return Toolbar.lookup(bar, m) == nil ? nil : m
    }

    /// The arrangement the toolbar is built from: the user's from macOS
    /// 15, else the default.
    var layout: Toolbar.Layout {
        Self.isAvailable ? settings.toolbarLayout(bar) : Toolbar.defaultLayout(bar)
    }

    /// `toolbarDefaultItemIdentifiers`: what AppKit builds the toolbar from
    /// (the user's arrangement) or, while the palette runs, its default set.
    /// From macOS 15 an item of a missing feature is in it (hidden by the
    /// delegate), on macOS 14 it is left out.
    func defaultIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        let l = toolbar.customizationPaletteIsRunning ? Toolbar.defaultLayout(bar) : layout
        return Toolbar.present(bar, l, features())
            .filter { Self.isAvailable || !$0.hidden }
            .map { nativeID($0.id) }
    }

    /// Whether `id` is shown in the toolbar for now (a conditional item's
    /// feature); the delegate hides the others (`isHidden`).
    func isShown(_ id: NSToolbarItem.Identifier) -> Bool {
        guard let m = modelID(id), let feature = Toolbar.lookup(bar, m)?.feature else { return true }
        return features().have.contains(feature)
    }

    /// `toolbarAllowedItemIdentifiers`: the palette's items (a missing
    /// feature's left out), what the toolbar holds, the separators and
    /// `extras`.
    func allowedIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        var ids = Toolbar.allowed(bar, features()).map(nativeID)
        ids += [.space, .flexibleSpace]
        ids += Toolbar.sections(bar).dropFirst().map { nativeID(Toolbar.separator($0)) }
        ids += extras
        for item in toolbar.items where !ids.contains(item.itemIdentifier) {
            ids.append(item.itemIdentifier)
        }
        // The default set may name an item the palette does not offer (a
        // conditional one): AppKit wants it allowed.
        for id in Toolbar.defaultLayout(bar).map(nativeID) where !ids.contains(id) {
            ids.append(id)
        }
        return ids
    }

    /// Where `token` (an item the app puts back, a separator) goes into
    /// `toolbar` so that it stands where the arrangement has it: before the
    /// first item after it in the arrangement that the toolbar holds
    /// (spaces aside, they are not told apart), else before the extras or
    /// at the end.
    func insertionIndex(of token: Toolbar.ID, in toolbar: NSToolbar) -> Int {
        let l = layout
        let ids = toolbar.items.map(\.itemIdentifier)
        if let at = l.firstIndex(of: token) {
            for next in l[(at + 1)...] where !next.isSpace {
                if let i = ids.firstIndex(of: nativeID(next)) {
                    return i
                }
            }
        }
        return ids.firstIndex { extras.contains($0) } ?? ids.count
    }

    /// `toolbarImmovableItemIdentifiers`.
    func immovableIdentifiers() -> Set<NSToolbarItem.Identifier> {
        Set(Toolbar.immovable(bar).map(nativeID) + extras)
    }

    /// `toolbar(_:itemIdentifier:canBeInsertedAt:)`: the section of the
    /// place, from the separators before it; never into the extras'
    /// section (the inspector's, at the end).
    func canInsert(_ id: NSToolbarItem.Identifier, at index: Int, in toolbar: NSToolbar) -> Bool {
        guard let m = modelID(id) else { return false }
        var section = Toolbar.sections(bar).first ?? .main
        for item in toolbar.items.prefix(index) {
            let iid = item.itemIdentifier
            if extras.contains(iid) {
                return false
            }
            if let t = model[iid], t.isSeparator, let s = Toolbar.Section(rawValue: String(t.rawValue.dropFirst())) {
                section = s
            }
        }
        return Toolbar.canInsert(bar, m, section)
    }

    /// Makes `toolbar` customizable (from macOS 15) and keeps it in step
    /// with the settings: the display mode now and on every change, the
    /// arrangement saved when the palette changes it.
    func attach(_ toolbar: NSToolbar) {
        toolbars.append(toolbar)
        toolbar.displayMode = Self.displayMode(settings.toolbarDisplayMode)
        guard Self.isAvailable else { return }
        if #available(macOS 15, *) {
            toolbar.allowsDisplayModeCustomization = true
        }
        refreshAllowsCustomization(toolbar)
        // The palette is a sheet of the window: its end is the end of
        // the customization (AppKit documents no KVO for
        // `customizationPaletteIsRunning`).
        if sheetObserver == nil {
            sheetObserver = NotificationCenter.default.addObserver(
                forName: NSWindow.didEndSheetNotification, object: nil, queue: .main
            ) { [weak self] note in
                let window = note.object as? NSWindow
                MainActor.assumeIsolated {
                    self?.sheetEnded(window?.toolbar)
                }
            }
        }
        // The arrangement changed elsewhere (another process, a
        // development step): the toolbar is built again from it.
        if layoutToken == nil {
            layoutToken = settings.onChange(Settings.key(bar)) { [weak self] in
                guard let self, !self.saving else { return }
                for tb in self.toolbars {
                    self.reloadIfChanged(tb)
                }
            }
        }
        if displayToken == nil {
            displayToken = settings.onChange(.toolbarDisplayMode) { [weak self] in
                guard let self else { return }
                let mode = Self.displayMode(self.settings.toolbarDisplayMode)
                for tb in self.toolbars where tb.displayMode != mode {
                    tb.displayMode = mode
                }
            }
        }
    }

    /// The toolbar can be customized only while it shows the whole
    /// arrangement (no section or separator out of it).
    func refreshAllowsCustomization(_ toolbar: NSToolbar) {
        let f = features()
        let allows = Self.isAvailable && f.hiddenSections.isEmpty && f.hiddenSeparators.isEmpty
        if toolbar.allowsUserCustomization != allows {
            toolbar.allowsUserCustomization = allows
        }
    }

    /// `toolbarWillAddItem` / `toolbarDidRemoveItem`: a change the palette
    /// made is saved once the run of changes is over; the app's own
    /// changes (the search field unfolding, the panel's section) are not.
    func itemsChanged(_ toolbar: NSToolbar) {
        guard Self.isAvailable, toolbar.customizationPaletteIsRunning else { return }
        dirty = true
        guard !saveScheduled else { return }
        saveScheduled = true
        DispatchQueue.main.async { [weak self, weak toolbar] in
            guard let self else { return }
            self.saveScheduled = false
            if let toolbar {
                self.save(toolbar)
            }
        }
    }

    /// A sheet of a window ended; for one of these toolbars' window, the
    /// palette's: its display mode is saved (and goes to the other
    /// toolbars), the arrangement saved once more if the palette changed
    /// it, and the window rebinds the separators.
    private func sheetEnded(_ toolbar: NSToolbar?) {
        guard let toolbar, toolbars.contains(where: { $0 === toolbar }), toolbar.allowsUserCustomization else { return }
        var changed = dirty
        let mode = Self.displayMode(toolbar.displayMode)
        if mode != settings.toolbarDisplayMode {
            settings.toolbarDisplayMode = mode
            changed = true
        }
        if dirty {
            dirty = false
            save(toolbar)
        }
        if changed {
            onPaletteEnd?(toolbar)
        }
    }

    /// Writes what `toolbar` holds as the arrangement (the extras left out).
    private func save(_ toolbar: NSToolbar) {
        let f = features()
        guard f.hiddenSections.isEmpty, f.hiddenSeparators.isEmpty else { return }
        // The extras' section ends the toolbar: nothing from its first item on is the user's.
        let skip = transient()
        let mine = toolbar.items.prefix { !extras.contains($0.itemIdentifier) }.filter { !skip.contains($0.itemIdentifier) }
        saving = true
        settings.setToolbarLayout(bar, mine.compactMap { modelID($0.itemIdentifier) })
        saving = false
    }

    /// What `toolbar` holds of the arrangement (extras and transient items left out).
    private func shownLayout(_ toolbar: NSToolbar) -> Toolbar.Layout {
        let skip = transient()
        return toolbar.items.prefix { !extras.contains($0.itemIdentifier) }
            .filter { !skip.contains($0.itemIdentifier) }
            .compactMap { modelID($0.itemIdentifier) }
    }

    /// Builds `toolbar` again from the stored arrangement when it differs
    /// from what the toolbar holds; never while the palette runs or a
    /// section is out (what it holds is not the arrangement then).
    func reloadIfChanged(_ toolbar: NSToolbar) {
        guard Self.isAvailable, !toolbar.customizationPaletteIsRunning else { return }
        let f = features()
        guard f.hiddenSections.isEmpty, f.hiddenSeparators.isEmpty else { return }
        let want = Toolbar.present(bar, layout, f).map(\.id)
        guard Toolbar.normalize(bar, Toolbar.tokens(shownLayout(toolbar))) != Toolbar.normalize(bar, Toolbar.tokens(want))
        else { return }
        while !toolbar.items.isEmpty {
            toolbar.removeItem(at: 0)
        }
        for (i, id) in (toolbar.delegate?.toolbarDefaultItemIdentifiers?(toolbar) ?? []).enumerated() {
            toolbar.insertItem(withItemIdentifier: id, at: i)
        }
        onPaletteEnd?(toolbar)
    }

    static func displayMode(_ m: Toolbar.DisplayMode) -> NSToolbar.DisplayMode {
        switch m {
        case .icon: .iconOnly
        case .iconAndLabel: .iconAndLabel
        case .label: .labelOnly
        }
    }

    static func displayMode(_ m: NSToolbar.DisplayMode) -> Toolbar.DisplayMode {
        switch m {
        case .iconAndLabel, .default: .iconAndLabel
        case .labelOnly: .label
        default: .icon
        }
    }
}
