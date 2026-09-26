// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The sender and the recipients above a message (addresses.go
/// `addressHeader`, the `message_addresses` grid of window.blp): the From,
/// To and Cc rows, each a label and a `FlowView` of chips; a row without
/// anybody to show is hidden. A chip shows the display name alone, the
/// whole address is its tooltip, and a click opens a menu that names it
/// again and offers Copy Address and New Message. A long row shows its
/// first chips and "+N more", which unfolds every row of the message;
/// another message starts folded again.
@MainActor
final class AddressHeaderView: NSView {
    /// A chip's Copy Address and New Message, the latter from the account
    /// of the message on display (installed by the view controller).
    var onCopy: (@MainActor (Address) -> Void)?
    var onWrite: (@MainActor (Address, AccountID) -> Void)?

    /// One line of the grid, and what its chips were built from: a render
    /// that changes nothing leaves the row (and a menu open on it) alone.
    @MainActor
    private final class Row {
        let label: NSTextField
        let flow = FlowView(spacing: 4, lineSpacing: 4)
        var key: String?

        init(_ title: String) {
            label = NSTextField(labelWithString: title)
        }
    }

    private let rows: [Row]
    private let grid: NSGridView
    private var message: MessageID?
    private var account: AccountID = ""
    private var lists: [[Address]] = [[], [], []]
    private var expanded = false

    init() {
        let rows = [Row(L10n.T("From")), Row(L10n.T("To")), Row(L10n.T("Cc"))]
        for r in rows {
            r.label.font = Typo.chip
            r.label.textColor = Tint.secondary
            r.label.setContentHuggingPriority(.required, for: .horizontal)
            r.label.setContentCompressionResistancePriority(.required, for: .horizontal)
        }
        self.rows = rows
        grid = NSGridView(views: rows.map { [AddressHeaderView.labelBox($0.label), $0.flow] })
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        grid.translatesAutoresizingMaskIntoConstraints = false
        grid.rowSpacing = 6
        grid.columnSpacing = 12
        grid.rowAlignment = .none
        grid.xPlacement = .fill
        grid.column(at: 0).xPlacement = .leading
        grid.column(at: 1).xPlacement = .fill
        // NSGridView shares spare width between the columns (see
        // ComposeHeaderView): the label column is as wide as its widest
        // label, the chips get the rest.
        grid.column(at: 0).width = rows.map { $0.label.intrinsicContentSize.width }.max() ?? 0
        for i in rows.indices {
            grid.row(at: i).yPlacement = .top
            grid.row(at: i).isHidden = true
        }
        addSubview(grid)
        NSLayoutConstraint.activate([
            grid.topAnchor.constraint(equalTo: topAnchor),
            grid.bottomAnchor.constraint(equalTo: bottomAnchor),
            grid.leadingAnchor.constraint(equalTo: leadingAnchor),
            grid.trailingAnchor.constraint(equalTo: trailingAnchor),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// A row's label, as tall as a chip and centred on it, so From, To
    /// and Cc sit level with the first line of chips however many follow.
    private static func labelBox(_ label: NSTextField) -> NSView {
        let box = NSView()
        box.translatesAutoresizingMaskIntoConstraints = false
        label.translatesAutoresizingMaskIntoConstraints = false
        box.addSubview(label)
        NSLayoutConstraint.activate([
            box.heightAnchor.constraint(equalToConstant: PillButton.height),
            label.centerYAnchor.constraint(equalTo: box.centerYAnchor),
            label.leadingAnchor.constraint(equalTo: box.leadingAnchor),
            label.trailingAnchor.constraint(equalTo: box.trailingAnchor),
        ])
        return box
    }

    /// Renders the three rows for message `id` (addresses.go `show`).
    func show(_ id: MessageID, account: AccountID, from: [Address], to: [Address]?, cc: [Address]?) {
        if id != message {
            message = id
            expanded = false
        }
        self.account = account
        lists = [from, to ?? [], cc ?? []]
        render()
    }

    /// Rebuilds the rows whose content changed.
    private func render() {
        for (i, row) in rows.enumerated() {
            fill(row, at: i)
        }
    }

    /// Rebuilds one row, or hides it when the list has nobody to show.
    private func fill(_ row: Row, at index: Int) {
        let (shown, more) = foldAddresses(lists[index], expanded: expanded)
        let key = addressKey(account, shown, more)
        guard key != row.key else { return }
        row.key = key
        row.flow.removeAllViews()
        for a in shown {
            row.flow.addView(chip(a))
        }
        if more > 0 {
            row.flow.addView(moreChip(row, more: more, at: shown.count))
        }
        grid.row(at: index).isHidden = row.flow.views.isEmpty
    }

    /// One address (addresses.go `chip`); the actions close over it and
    /// the account it would be written from.
    private func chip(_ a: Address) -> NSView {
        let chip = AddressChipView(address: a)
        let account = account
        chip.onCopy = { [weak self] in self?.onCopy?(a) }
        chip.onWrite = { [weak self] in self?.onWrite?(a, account) }
        return chip
    }

    /// "+N more" at the end of folded `row`, standing in for `n` addresses
    /// from chip number `at` on; it unfolds every row. From the keyboard
    /// the focus moves on to the first chip the unfold revealed; a click
    /// never focuses a button on the Mac, so it leaves the focus alone.
    /// The unfold waits for the next turn of the main queue: it removes
    /// this very button, which is still inside its own click.
    private func moreChip(_ row: Row, more n: Int, at: Int) -> NSView {
        let button = PillButton(title: L10n.N("+%d more", "+%d more", n), filled: false)
        button.onClick = { [weak self, weak button, weak row] in
            let keyboard = button.map { $0.window?.firstResponder === $0 } ?? false
            DispatchQueue.main.async { [weak self, weak row] in
                guard let self else { return }
                self.expanded = true
                self.render()
                if keyboard, let row, at < row.flow.views.count {
                    row.flow.window?.makeFirstResponder(row.flow.views[at])
                }
            }
        }
        return button
    }
}

/// The pill of an address chip (menubutton.address-chip in
/// ui/internal/style): a borderless button with the title a size down on
/// a faint tint that deepens under the pointer and while pressed. Without
/// `filled` it is the "+N more" pill, tinted only under the pointer. The
/// tint is drawn, not set on a layer, so it follows the appearance.
@MainActor
class PillButton: NSButton {
    static let height: CGFloat = 24
    static let padding: CGFloat = 10

    var onClick: (@MainActor () -> Void)?

    private let filled: Bool
    private var tracking: NSTrackingArea?
    private var hovering = false {
        didSet {
            if hovering != oldValue {
                needsDisplay = true
            }
        }
    }

    init(title: String, filled: Bool) {
        self.filled = filled
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        isBordered = false
        // The cell draws nothing different while pressed (no alternate
        // title, no darkened text); the pressed tint is drawn below.
        (cell as? NSButtonCell)?.highlightsBy = []
        imagePosition = .noImage
        attributedTitle = NSAttributedString(string: title, attributes: [
            .font: Typo.chip,
            .foregroundColor: filled ? NSColor.labelColor : Tint.secondary,
        ])
        target = self
        action = #selector(clicked(_:))
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var intrinsicContentSize: NSSize {
        NSSize(width: ceil(attributedTitle.size().width) + 2 * Self.padding, height: Self.height)
    }

    private var capsule: NSBezierPath {
        let r = bounds.height / 2
        return NSBezierPath(roundedRect: bounds, xRadius: r, yRadius: r)
    }

    override func draw(_ dirtyRect: NSRect) {
        let alpha: CGFloat
        if isHighlighted {
            alpha = 0.18
        } else if hovering {
            alpha = 0.12
        } else {
            alpha = filled ? 0.07 : 0
        }
        if alpha > 0 {
            Tint.fg(alpha: alpha).setFill()
            capsule.fill()
        }
        super.draw(dirtyRect)
    }

    override func drawFocusRingMask() {
        capsule.fill()
    }

    override var focusRingMaskBounds: NSRect { bounds }

    override func updateTrackingAreas() {
        super.updateTrackingAreas()
        if let tracking {
            removeTrackingArea(tracking)
        }
        let area = NSTrackingArea(
            rect: .zero, options: [.mouseEnteredAndExited, .activeInActiveApp, .inVisibleRect], owner: self, userInfo: nil)
        addTrackingArea(area)
        tracking = area
    }

    override func mouseEntered(with event: NSEvent) {
        super.mouseEntered(with: event)
        hovering = true
    }

    override func mouseExited(with event: NSEvent) {
        super.mouseExited(with: event)
        hovering = false
    }

    /// Whether the pointer is on the pill now: a menu that ran in between
    /// swallowed the exit.
    func refreshHover() {
        guard let window else { return }
        hovering = bounds.contains(convert(window.mouseLocationOutsideOfEventStream, from: nil))
    }

    @objc private func clicked(_ sender: Any?) {
        onClick?()
    }
}

/// One address (addresses.go `chip`): the name on a pill, tail-elided at
/// `addressNameChars`, the whole address as the tooltip. A click opens the
/// menu: the name as its heading, the address, then Copy Address and New
/// Message. Names and addresses are server data, shown as plain text.
@MainActor
final class AddressChipView: PillButton {
    let address: Address

    var onCopy: (@MainActor () -> Void)?
    var onWrite: (@MainActor () -> Void)?

    init(address: Address) {
        self.address = address
        let name = displayName(address)
        super.init(title: AddressChipView.tailEllipsis(name, max: addressNameChars), filled: true)
        let full = formatAddress(address)
        if full != name || name.count > addressNameChars {
            toolTip = full
        }
        onClick = { [weak self] in self?.showMenu() }
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Cuts `s` to at most `max` characters, the end replaced by an
    /// ellipsis (Pango's EllipsizeEnd, by characters).
    static func tailEllipsis(_ s: String, max: Int) -> String {
        guard max >= 1, s.count > max else { return s }
        return String(s.prefix(max - 1)) + "\u{2026}"
    }

    /// The chip's menu (addresses.go `addressMenu`). The address item is
    /// text, not an action, and is middle-elided so a pathological one
    /// cannot make the menu wider than the screen.
    private func showMenu() {
        let name = (address.name ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        let addr = address.address.trimmingCharacters(in: .whitespacesAndNewlines)
        let menu = NSMenu()
        menu.autoenablesItems = false
        if !name.isEmpty {
            menu.addItem(.sectionHeader(title: Self.tailEllipsis(name, max: 60)))
        }
        if !addr.isEmpty {
            let item = NSMenuItem(title: AttachmentChipView.middleEllipsis(addr, max: 60), action: nil, keyEquivalent: "")
            item.isEnabled = false
            menu.addItem(item)
        }
        if menu.numberOfItems > 0 {
            menu.addItem(.separator())
        }
        let copy = NSMenuItem(title: mn(L10n.T("_Copy Address")), action: #selector(copyItem(_:)), keyEquivalent: "")
        copy.target = self
        copy.isEnabled = !addr.isEmpty
        menu.addItem(copy)
        let write = NSMenuItem(title: mn(L10n.T("_New Message")), action: #selector(writeItem(_:)), keyEquivalent: "")
        write.target = self
        write.isEnabled = !addr.isEmpty
        menu.addItem(write)
        menu.popUp(positioning: nil, at: NSPoint(x: 0, y: isFlipped ? bounds.height + 4 : -4), in: self)
        refreshHover()
    }

    @objc private func copyItem(_ sender: Any?) {
        onCopy?()
    }

    @objc private func writeItem(_ sender: Any?) {
        onWrite?()
    }
}
