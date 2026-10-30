// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The Contacts window (docs/plans/address-book.md W1, W4, W5): one for the
/// application (`AppContacts.showWindow`), never a mode of the main window.
/// A three-column `NSSplitViewController` — the sidebar of address books
/// (`ContactsSidebarViewController`), the list (`ContactsListViewController`)
/// and the contact's detail or form (`ContactsDetailViewController`) — under
/// a unified toolbar with the sidebar toggle, New Contact (+) and the
/// search, the window's title "Contacts" with the list's subtitle ("All
/// Contacts · 412"), and the status bar across the bottom ("Contacts
/// synchronized 3 minutes ago"). Below 720 pt the sidebar folds away (the
/// toggle shows it again), below 520 pt the detail covers the list with
/// ‹ Contacts (`ContactBook.foldFor`, W5 d). The state is the Core's
/// `ContactsListController` and `ContactsDetailController`; this class
/// only wires them to the views and asks the questions (an unsaved form
/// before another contact, Delete, the conflict).
@MainActor
final class ContactsWindowController: NSWindowController, NSWindowDelegate, NSToolbarDelegate {
    let contacts: AppContacts
    let list: ContactsListController
    let detail: ContactsDetailController
    var onClose: (@MainActor () -> Void)?

    let split = ContactsSplitViewController()
    let sidebarPane: ContactsSidebarViewController
    let listPane: ContactsListViewController
    let detailPane: ContactsDetailViewController
    private let statusBar = ContactsStatusBar()
    private let toasts = WizardToastPresenter()
    private var sidebarItem: NSSplitViewItem!
    private var listItem: NSSplitViewItem!
    private var detailItem: NSSplitViewItem!
    private weak var searchItem: NSSearchToolbarItem?
    private weak var newItem: NSToolbarItem?
    private var fold = ContactBook.Fold()
    /// The user showed the folded sidebar with the toggle.
    private var sidebarShownByUser = false
    /// Show Contact waiting for the window's first listing (the query),
    /// and for the list to hold the contact (the id).
    private var pendingQuery: String?
    private var pendingSelect: ContactID?
    /// More Details… waiting for the books.
    private var pendingForm: ContactBook.Form?
    private var accountsToken: NotificationHub.Token?
    private var statusTimer: Timer?

    static let sidebarWidth: CGFloat = 240
    static let listWidth: CGFloat = 300
    static let toolbarNew = NSToolbarItem.Identifier("contacts.new")
    static let toolbarSearch = NSToolbarItem.Identifier("contacts.search")

    init(contacts: AppContacts) {
        self.contacts = contacts
        list = ContactsListController(options: contacts.options)
        detail = ContactsDetailController(options: contacts.options)
        sidebarPane = ContactsSidebarViewController()
        listPane = ContactsListViewController()
        detailPane = ContactsDetailViewController()
        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 980, height: 640),
            styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
            backing: .buffered, defer: false)
        window.title = ContactBook.windowTitle
        window.toolbarStyle = .unified
        window.titleVisibility = .visible
        window.isReleasedWhenClosed = false
        window.minSize = NSSize(width: 380, height: 360)
        window.setFrameAutosaveName("Contacts")
        window.tabbingMode = .disallowed
        if !window.setFrameUsingName("Contacts") {
            window.center()
        }
        super.init(window: window)
        window.delegate = self

        detail.books = { [weak list] id in list?.book(id) }
        sidebarPane.window = self
        listPane.window = self
        detailPane.window = self

        sidebarItem = NSSplitViewItem(sidebarWithViewController: sidebarPane)
        sidebarItem.minimumThickness = 200
        sidebarItem.maximumThickness = 300
        sidebarItem.canCollapse = true
        sidebarItem.preferredThicknessFraction = 0.24
        listItem = NSSplitViewItem(contentListWithViewController: listPane)
        listItem.minimumThickness = 240
        listItem.maximumThickness = 420
        listItem.canCollapse = true
        detailItem = NSSplitViewItem(viewController: detailPane)
        detailItem.minimumThickness = 300
        detailItem.canCollapse = true
        split.splitViewItems = [sidebarItem, listItem, detailItem]
        split.onToggle = { [weak self] shows in self?.sidebarToggled(shows) }
        let firstRun = UserDefaults.standard.object(forKey: "NSSplitView Subview Frames ContactsSplit") == nil
        split.splitView.autosaveName = "ContactsSplit"

        let root = NSViewController()
        let rootView = NSView()
        root.view = rootView
        root.addChild(split)
        split.view.translatesAutoresizingMaskIntoConstraints = false
        statusBar.translatesAutoresizingMaskIntoConstraints = false
        rootView.addSubview(split.view)
        rootView.addSubview(statusBar)
        NSLayoutConstraint.activate([
            split.view.topAnchor.constraint(equalTo: rootView.topAnchor),
            split.view.leadingAnchor.constraint(equalTo: rootView.leadingAnchor),
            split.view.trailingAnchor.constraint(equalTo: rootView.trailingAnchor),
            split.view.bottomAnchor.constraint(equalTo: statusBar.topAnchor),
            statusBar.leadingAnchor.constraint(equalTo: rootView.leadingAnchor),
            statusBar.trailingAnchor.constraint(equalTo: rootView.trailingAnchor),
            statusBar.bottomAnchor.constraint(equalTo: rootView.bottomAnchor),
        ])
        window.contentViewController = root
        window.setContentSize(NSSize(width: 980, height: 640))
        toasts.attach(to: rootView)

        let toolbar = NSToolbar(identifier: "ContactsToolbar")
        toolbar.delegate = self
        toolbar.displayMode = .iconOnly
        toolbar.allowsUserCustomization = false
        window.toolbar = toolbar

        if firstRun {
            // The wireframe's columns (W1): sidebar 240, list 300, the
            // detail the rest; afterwards the user's, as autosaved.
            split.view.layoutSubtreeIfNeeded()
            split.splitView.setPosition(Self.sidebarWidth, ofDividerAt: 0)
            split.splitView.setPosition(Self.sidebarWidth + split.splitView.dividerThickness + Self.listWidth, ofDividerAt: 1)
        }
        wire()
        list.start()
        accountsToken = contacts.state.notifications.addAccountsChanged { [weak self] in
            self?.list.handle(API.Notify.accountsChanged)
        }
        // The status line says "3 minutes ago": it ages with the clock.
        statusTimer = Timer.scheduledTimer(withTimeInterval: 30, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated { self?.updateStatus() }
        }
        update()
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    private func wire() {
        list.onChange = { [weak self] in self?.update() }
        list.onSelect = { [weak self] id in
            guard let self else { return }
            self.detail.open(id)
            self.listPane.showSelection(id)
            self.applyFold(animated: true)
        }
        list.onSignIn = { [weak self] id in
            guard let self else { return }
            self.contacts.signIn(id) { [weak self] text in self?.toasts.show(text) }
        }
        detail.onChange = { [weak self] in
            guard let self else { return }
            self.detailPane.update()
            self.updateToolbar()
            self.applyFold(animated: false)
        }
        detail.onQuestion = { [weak self] q in
            guard let self else { return false }
            return await self.ask(q)
        }
        detail.onCreated = { [weak self] id in
            guard let self else { return }
            self.pendingSelect = id
            self.list.reload()
        }
        detail.onDeleted = { [weak self] _ in
            guard let self else { return }
            self.list.select(nil)
            self.list.reload()
        }
    }

    /// notify.contactsChanged of the application's hub.
    func handle(_ n: ContactsChangedNotification) {
        list.handle(API.Notify.contactsChanged)
        detail.contactsChanged(n)
    }

    // MARK: Updates

    /// The list controller changed: every pane reads it anew.
    private func update() {
        applyPending()
        if let id = pendingSelect, list.contacts.contains(where: { $0.id == id }) {
            pendingSelect = nil
            if list.selected != id {
                list.select(id)
            }
        }
        sidebarPane.update()
        listPane.update()
        detailPane.update()
        window?.subtitle = list.subtitle
        updateToolbar()
        updateStatus()
    }

    private func updateStatus() {
        let line = list.error.isEmpty ? list.status : list.error
        statusBar.set(line, warning: !list.error.isEmpty)
    }

    private func updateToolbar() {
        newItem?.isEnabled = list.canCreate && !detail.saving
    }

    /// Show Contact and More Details… arriving before the first listing
    /// wait for it: the first answer resets the search.
    private func applyPending() {
        guard list.loaded else { return }
        if let q = pendingQuery {
            pendingQuery = nil
            if let all = list.items.first(where: { $0.kind == .all })?.book, list.selectedBook != all {
                list.selectBook(all)
            }
            searchItem?.searchField.stringValue = q
            list.setQuery(q)
        }
        if let f = pendingForm {
            pendingForm = nil
            begin(new: f)
        }
    }

    // MARK: Entry points

    /// Show Contact of an address chip: the address searched in All
    /// Contacts and the contact selected once listed.
    func reveal(_ id: ContactID, address: String) {
        confirmLeaving { [weak self] in
            guard let self else { return }
            self.pendingQuery = address
            self.pendingSelect = id
            self.update()
        }
    }

    /// More Details… of Add to Contacts: the form, prefilled.
    func beginNew(_ form: ContactBook.Form) {
        confirmLeaving { [weak self] in
            guard let self else { return }
            self.pendingForm = form
            self.update()
        }
    }

    private func begin(new form: ContactBook.Form?) {
        list.select(nil)
        detail.beginNew(book: form?.book ?? list.newContactBook, form: form)
        applyFold(animated: false)
        detailPane.focusFirstField()
    }

    /// New Contact (+, File ▸ New Contact has none on purpose).
    @objc func newContact(_ sender: Any?) {
        guard list.canCreate else { return }
        confirmLeaving { [weak self] in self?.begin(new: nil) }
    }

    @objc private func searchChanged(_ sender: NSSearchField) {
        list.setQuery(sender.stringValue)
    }

    /// The list's click on a contact: an unsaved form asks first; Cancel
    /// keeps the form and the list's selection on what it shows.
    func choose(_ id: ContactID?) {
        guard id != list.selected || detail.editing else { return }
        confirmLeaving { [weak self] in
            guard let self else { return }
            if id == self.list.selected {
                self.detail.open(id)
            } else {
                self.list.select(id)
            }
        } cancelled: { [weak self] in
            guard let self else { return }
            self.listPane.showSelection(self.list.selected)
        }
    }

    /// A sidebar row.
    func chooseBook(_ id: AddressBookID) {
        guard id != list.selectedBook else { return }
        confirmLeaving { [weak self] in
            guard let self else { return }
            if self.detail.editing {
                self.detail.cancelEdit()
            }
            self.list.selectBook(id)
        } cancelled: { [weak self] in
            self?.sidebarPane.update()
        }
    }

    /// Runs `go` when no unsaved form is in the way, else after "Discard
    /// Changes?" answered with Discard (the form closes unsaved).
    func confirmLeaving(_ go: @escaping @MainActor () -> Void, cancelled: (@MainActor () -> Void)? = nil) {
        guard detail.dirty else {
            go()
            return
        }
        Task { @MainActor [weak self] in
            guard let self else { return }
            let discard = await self.ask(ContactBook.discardQuestion)
            if discard {
                self.detail.cancelEdit()
                go()
            } else {
                cancelled?()
            }
        }
    }

    /// The quit's question (`AppContacts.confirmQuit`): only with a
    /// changed form; the window comes forward first so the sheet shows
    /// on the form it is about.
    func confirmQuit() async -> Bool {
        guard detail.dirty else { return true }
        showWindow(nil)
        window?.makeKeyAndOrderFront(nil)
        guard await ask(ContactBook.discardQuestion) else { return false }
        detail.cancelEdit()
        return true
    }

    /// A question of the detail controller: Delete as a destructive alert,
    /// the conflict with Reload as its default.
    private func ask(_ q: ContactBook.Question) async -> Bool {
        if q.destructive {
            return await contacts.state.alerts.confirmDestructive(
                on: window, heading: q.heading, body: q.body, confirmLabel: q.confirm)
        }
        return await contacts.state.alerts.confirm(
            on: window, heading: q.heading, body: q.body, confirmLabel: q.confirm, declineLabel: ContactBook.cancel)
    }

    func toast(_ text: String) {
        toasts.show(text)
    }

    // MARK: Folding (W5 d)

    /// ‹ Contacts of the narrow window: back to the list.
    func backToList() {
        confirmLeaving { [weak self] in
            guard let self else { return }
            if self.detail.editing {
                self.detail.cancelEdit()
            }
            self.list.select(nil)
            self.applyFold(animated: true)
        }
    }

    /// Whether the detail covers the list (the narrow window with a contact
    /// or a form).
    var detailCoversList: Bool {
        fold.detailOverList && (list.selected != nil || detail.editing)
    }

    func windowDidResize(_ notification: Foundation.Notification) {
        applyFold(animated: false)
    }

    private func applyFold(animated: Bool) {
        guard let w = window else { return }
        let f = ContactBook.foldFor(width: Double(w.frame.width))
        if f.sidebarCollapsed != fold.sidebarCollapsed {
            sidebarShownByUser = false
        }
        fold = f
        let sidebarCollapsed = (f.sidebarCollapsed && !sidebarShownByUser) || f.detailOverList
        if sidebarItem.isCollapsed != sidebarCollapsed {
            sidebarItem.isCollapsed = sidebarCollapsed
        }
        let covers = detailCoversList
        let listCollapsed = f.detailOverList && covers
        let detailCollapsed = f.detailOverList && !covers
        if listItem.isCollapsed != listCollapsed {
            listItem.isCollapsed = listCollapsed
        }
        if detailItem.isCollapsed != detailCollapsed {
            detailItem.isCollapsed = detailCollapsed
        }
        detailPane.setBackButton(covers)
    }

    /// The toolbar's sidebar toggle: in the folded window it shows the
    /// sidebar until the window is wide again (or folds further).
    fileprivate func sidebarToggled(_ shows: Bool) {
        sidebarShownByUser = shows
    }

    // MARK: Window

    func windowShouldClose(_ sender: NSWindow) -> Bool {
        guard detail.dirty else { return true }
        confirmLeaving { [weak self] in self?.window?.close() }
        return false
    }

    func windowWillClose(_ notification: Foundation.Notification) {
        statusTimer?.invalidate()
        statusTimer = nil
        accountsToken?.cancel()
        list.stop()
        detail.stop()
        onClose?()
    }

    // MARK: Toolbar

    func toolbarDefaultItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        [.toggleSidebar, .sidebarTrackingSeparator, .flexibleSpace, Self.toolbarNew, Self.toolbarSearch]
    }

    func toolbarAllowedItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        toolbarDefaultItemIdentifiers(toolbar)
    }

    func toolbar(
        _ toolbar: NSToolbar, itemForItemIdentifier id: NSToolbarItem.Identifier, willBeInsertedIntoToolbar flag: Bool
    ) -> NSToolbarItem? {
        switch id {
        case Self.toolbarNew:
            let item = NSToolbarItem(itemIdentifier: id)
            item.label = ContactBook.newContact
            item.toolTip = ContactBook.newContact
            item.image = NSImage(systemSymbolName: "plus", accessibilityDescription: ContactBook.newContact)
            item.isBordered = true
            item.target = self
            item.action = #selector(newContact(_:))
            item.autovalidates = false
            item.isEnabled = list.canCreate
            newItem = item
            return item
        case Self.toolbarSearch:
            let item = NSSearchToolbarItem(itemIdentifier: id)
            item.searchField.placeholderString = ContactBook.searchContacts
            item.searchField.target = self
            item.searchField.action = #selector(searchChanged(_:))
            item.searchField.sendsSearchStringImmediately = true
            item.preferredWidthForSearchField = 220
            searchItem = item
            return item
        default:
            return nil
        }
    }
}

/// The status bar across the bottom of the Contacts window (W1 ⑦): the
/// synchronisation's age, or the last failure.
@MainActor
final class ContactsStatusBar: NSView {
    private let icon = NSImageView()
    private let label = NSTextField(labelWithString: "")
    static let height: CGFloat = 28

    init() {
        super.init(frame: .zero)
        let line = NSBox()
        line.boxType = .separator
        line.translatesAutoresizingMaskIntoConstraints = false
        icon.image = NSImage(systemSymbolName: "arrow.triangle.2.circlepath", accessibilityDescription: nil)
        icon.symbolConfiguration = .init(pointSize: 11, weight: .regular)
        icon.contentTintColor = .secondaryLabelColor
        label.font = Typo.caption
        label.textColor = .secondaryLabelColor
        label.lineBreakMode = .byTruncatingTail
        label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        let row = NSStackView(views: [icon, label])
        row.orientation = .horizontal
        row.spacing = 6
        row.translatesAutoresizingMaskIntoConstraints = false
        addSubview(line)
        addSubview(row)
        NSLayoutConstraint.activate([
            heightAnchor.constraint(equalToConstant: Self.height),
            line.topAnchor.constraint(equalTo: topAnchor),
            line.leadingAnchor.constraint(equalTo: leadingAnchor),
            line.trailingAnchor.constraint(equalTo: trailingAnchor),
            row.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 12),
            row.trailingAnchor.constraint(lessThanOrEqualTo: trailingAnchor, constant: -12),
            row.centerYAnchor.constraint(equalTo: centerYAnchor),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// The line ("" hides the icon too); a failure has the warning symbol.
    func set(_ text: String, warning: Bool) {
        label.stringValue = text
        icon.isHidden = text.isEmpty
        icon.image = NSImage(
            systemSymbolName: warning ? "exclamationmark.triangle" : "arrow.triangle.2.circlepath", accessibilityDescription: nil)
        icon.contentTintColor = warning ? .systemOrange : .secondaryLabelColor
    }
}

/// The window's split view controller; tells the window when the toolbar's
/// toggle showed or hid the sidebar.
@MainActor
final class ContactsSplitViewController: NSSplitViewController {
    /// True: the toggle shows the sidebar.
    var onToggle: (@MainActor (Bool) -> Void)?

    override func toggleSidebar(_ sender: Any?) {
        let shows = splitViewItems.first?.isCollapsed ?? false
        super.toggleSidebar(sender)
        onToggle?(shows)
    }
}
