// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The Contacts window's list (W1, W5 b, c): Microsoft's consent banners on
/// top (Allow…, or the Online Accounts advice), then the contacts of the
/// selected book under their index letters (`ContactsListController.rows`;
/// a search is flat), each with its initials, its name and its company or
/// address; the next page is asked near the end. An empty book or search
/// says so (`ContactBook.emptyText`). Names are a server's text, shown
/// through `stringValue`.
@MainActor
final class ContactsListViewController: NSViewController, NSTableViewDataSource, NSTableViewDelegate {
    weak var window: ContactsWindowController?

    private let table = NSTableView()
    private let scroll = NSScrollView()
    private let banners = FillStackView()
    private let empty = StatusPageView(illustration: .symbol("person.crop.circle"), title: "", description: "")
    private var rows: [ContactBook.Row] = []
    private var shownConsent: [ContactsConsentNeed] = []
    private var applying = false

    static let rowHeight: CGFloat = 48
    static let headingHeight: CGFloat = 24
    /// How close to the end the next page is asked.
    static let pageAhead = 15

    override func loadView() {
        let root = NSView()
        let column = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("contact"))
        column.resizingMask = .autoresizingMask
        table.addTableColumn(column)
        table.headerView = nil
        table.style = .plain
        table.usesAutomaticRowHeights = false
        table.dataSource = self
        table.delegate = self
        table.floatsGroupRows = true
        table.backgroundColor = .clear
        table.intercellSpacing = NSSize(width: 0, height: 0)
        table.setAccessibilityLabel(ContactBook.windowTitle)
        scroll.documentView = table
        scroll.drawsBackground = false
        scroll.hasVerticalScroller = true
        scroll.autohidesScrollers = true
        banners.spacing = 0
        for v in [banners, scroll, empty] as [NSView] {
            v.translatesAutoresizingMaskIntoConstraints = false
            root.addSubview(v)
        }
        empty.isHidden = true
        NSLayoutConstraint.activate([
            banners.topAnchor.constraint(equalTo: root.safeAreaLayoutGuide.topAnchor),
            banners.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            banners.trailingAnchor.constraint(equalTo: root.trailingAnchor),
            scroll.topAnchor.constraint(equalTo: banners.bottomAnchor),
            scroll.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            scroll.trailingAnchor.constraint(equalTo: root.trailingAnchor),
            scroll.bottomAnchor.constraint(equalTo: root.bottomAnchor),
            empty.topAnchor.constraint(equalTo: banners.bottomAnchor),
            empty.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            empty.trailingAnchor.constraint(equalTo: root.trailingAnchor),
            empty.bottomAnchor.constraint(equalTo: root.bottomAnchor),
        ])
        view = root
    }

    /// Reads the controller's rows, banners and empty state.
    func update() {
        guard let c = window?.list else { return }
        _ = view
        let new = c.rows
        if new != rows {
            rows = new
            table.reloadData()
        }
        showSelection(c.selected)
        if c.consent != shownConsent {
            shownConsent = c.consent
            rebuildBanners(c.consent)
        }
        let e = c.listEmpty
        let (title, body) = ContactBook.emptyText(e)
        empty.isHidden = e == .none
        empty.illustration = .symbol(e == .search ? "magnifyingglass" : "person.crop.circle")
        empty.title = title
        empty.descriptionText = body
        scroll.isHidden = e != .none
    }

    /// Selects the row of `id` (none for nil) without telling the window.
    func showSelection(_ id: ContactID?) {
        _ = view
        applying = true
        defer { applying = false }
        if let id, let i = rows.firstIndex(where: { $0.contact?.id == id }) {
            if table.selectedRow != i {
                table.selectRowIndexes([i], byExtendingSelection: false)
                table.scrollRowToVisible(i)
            }
        } else if table.selectedRow >= 0 {
            table.deselectAll(nil)
        }
    }

    private func rebuildBanners(_ needs: [ContactsConsentNeed]) {
        for v in banners.arrangedSubviews {
            banners.removeArrangedSubview(v)
            v.removeFromSuperview()
        }
        for n in needs {
            let b = ContactsConsentBanner(need: n)
            b.onAllow = { [weak self] in self?.window?.list.allowConsent(n.accountId) }
            banners.addArrangedSubview(b)
        }
    }

    // MARK: Table

    func numberOfRows(in tableView: NSTableView) -> Int { rows.count }

    func tableView(_ tableView: NSTableView, isGroupRow row: Int) -> Bool { rows[row].isHeading }

    func tableView(_ tableView: NSTableView, shouldSelectRow row: Int) -> Bool { !rows[row].isHeading }

    func tableView(_ tableView: NSTableView, heightOfRow row: Int) -> CGFloat {
        rows[row].isHeading ? Self.headingHeight : Self.rowHeight
    }

    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        if row >= rows.count - Self.pageAhead, let c = window?.list, c.hasMore {
            // The next page, once this pass of the table is done.
            DispatchQueue.main.async { [weak c] in c?.loadMore() }
        }
        let r = rows[row]
        if r.isHeading {
            return ContactsSectionHeading(letter: r.heading)
        }
        return ContactsListCell(row: r)
    }

    func tableViewSelectionDidChange(_ notification: Foundation.Notification) {
        guard !applying else { return }
        let i = table.selectedRow
        let id = i >= 0 && i < rows.count ? rows[i].contact?.id : nil
        window?.choose(id)
    }
}

/// An index letter of the list (W1 "A", "B", "N").
@MainActor
final class ContactsSectionHeading: NSTableCellView {
    init(letter: String) {
        super.init(frame: .zero)
        let label = NSTextField(labelWithString: letter)
        label.font = .systemFont(ofSize: Typo.small, weight: .semibold)
        label.textColor = .secondaryLabelColor
        let line = NSBox()
        line.boxType = .separator
        let row = NSStackView(views: [label, line])
        row.orientation = .horizontal
        row.alignment = .centerY
        row.spacing = 8
        row.translatesAutoresizingMaskIntoConstraints = false
        addSubview(row)
        NSLayoutConstraint.activate([
            row.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 14),
            row.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -10),
            row.centerYAnchor.constraint(equalTo: centerYAnchor),
        ])
        setAccessibilityElement(true)
        setAccessibilityRole(.staticText)
        setAccessibilityLabel(letter)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }
}

/// A contact of the list: initials, name, company or address.
@MainActor
final class ContactsListCell: NSTableCellView {
    static let avatarSize: CGFloat = 32

    init(row: ContactBook.Row) {
        super.init(frame: .zero)
        let avatar = ContactInitialsView(size: Self.avatarSize)
        avatar.initials = row.initials
        let title = NSTextField(labelWithString: row.title)
        title.font = Typo.bodyMedium
        title.lineBreakMode = .byTruncatingTail
        title.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        textField = title
        let detail = NSTextField(labelWithString: row.detail)
        detail.font = Typo.caption
        detail.textColor = .secondaryLabelColor
        detail.lineBreakMode = .byTruncatingTail
        detail.isHidden = row.detail.isEmpty
        detail.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        let text = NSStackView(views: [title, detail])
        text.orientation = .vertical
        text.alignment = .leading
        text.spacing = 1
        text.detachesHiddenViews = true
        let line = NSStackView(views: [avatar, text])
        line.orientation = .horizontal
        line.alignment = .centerY
        line.spacing = 10
        line.translatesAutoresizingMaskIntoConstraints = false
        addSubview(line)
        NSLayoutConstraint.activate([
            line.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 14),
            line.trailingAnchor.constraint(lessThanOrEqualTo: trailingAnchor, constant: -10),
            line.centerYAnchor.constraint(equalTo: centerYAnchor),
        ])
        setAccessibilityLabel(row.detail.isEmpty ? row.title : ContactBook.twoParts(row.title, row.detail))
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }
}

/// A contact's avatar: its initials in a grey disc (no photos in stage 1,
/// A13), as the wireframes draw them.
@MainActor
final class ContactInitialsView: NSView {
    var initials = "" {
        didSet { if initials != oldValue { needsDisplay = true } }
    }

    private let size: CGFloat

    init(size: CGFloat) {
        self.size = size
        super.init(frame: NSRect(x: 0, y: 0, width: size, height: size))
        translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([widthAnchor.constraint(equalToConstant: size), heightAnchor.constraint(equalToConstant: size)])
        setContentHuggingPriority(.required, for: .horizontal)
        setContentCompressionResistancePriority(.required, for: .horizontal)
        setAccessibilityElement(false)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var intrinsicContentSize: NSSize { NSSize(width: size, height: size) }

    override func draw(_ dirtyRect: NSRect) {
        Tint.fg(alpha: 0.1).setFill()
        NSBezierPath(ovalIn: bounds).fill()
        let text = initials.isEmpty ? "?" : initials
        let font = NSFont.systemFont(ofSize: size * 0.38, weight: .semibold)
        let attrs: [NSAttributedString.Key: Any] = [.font: font, .foregroundColor: Tint.fg(alpha: 0.7)]
        let s = NSAttributedString(string: text, attributes: attrs)
        let b = s.size()
        s.draw(at: NSPoint(x: (bounds.width - b.width) / 2, y: (bounds.height - b.height) / 2))
    }
}

/// Microsoft's consent missing for an account's contacts (W5 b): the
/// sentence with the lock and Allow…, or the Online Accounts advice.
@MainActor
final class ContactsConsentBanner: NSView {
    var onAllow: (@MainActor () -> Void)?

    init(need: ContactsConsentNeed) {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        let symbol = CalloutCard.symbolView()
        CalloutCard.show("lock", .info, in: symbol)
        let text = NSTextField(wrappingLabelWithString: ContactBook.consentBanner(need.account))
        text.font = Typo.body
        text.isSelectable = false
        text.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        // The sentence over Allow… (W5 b): the list column is narrow.
        let column = NSStackView(views: [text])
        column.orientation = .vertical
        column.alignment = .trailing
        column.spacing = 6
        text.widthAnchor.constraint(equalTo: column.widthAnchor).isActive = true
        if need.signIn {
            let allow = NSButton(title: ContactBook.allow, target: self, action: #selector(allow(_:)))
            allow.bezelStyle = .push
            column.addArrangedSubview(allow)
        } else {
            text.stringValue = ContactBook.consentBanner(need.account) + "\n" + ContactBook.consentOnline
        }
        let row = NSStackView(views: [symbol, column])
        row.orientation = .horizontal
        row.alignment = .top
        row.spacing = CalloutCard.spacing
        _ = CalloutCard.install(row, in: self)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    @objc private func allow(_ sender: Any?) {
        onAllow?()
    }
}
