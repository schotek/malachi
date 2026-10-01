// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The card of header fields of a compose window (compose.blp
/// `header_rows`): one line per field, a label of uniform width and the
/// field beside it, separated by hairlines: From (a pop-up of identities),
/// To with the Cc/Bcc button, Cc and Bcc (hidden until asked for),
/// Subject. The recipient rows are `RecipientTokenField`s (finished
/// addresses as badges; an unparsable entry is a red-tinted badge, D8 of
/// the plan) and grow by lines. Everything typed here is plain text.
@MainActor
final class ComposeHeaderView: NSBox {
    static let rowHeight: CGFloat = 30
    static let horizontalInset: CGFloat = 12

    let fromPopup = NSPopUpButton(frame: .zero, pullsDown: false)
    let toField = RecipientTokenField()
    let ccField = RecipientTokenField()
    let bccField = RecipientTokenField()
    let subjectField = NSTextField()
    let ccBccButton = NSButton(title: L10n.T("Cc/Bcc"), target: nil, action: nil)

    /// The Cc/Bcc button.
    var onShowCcBcc: (@MainActor () -> Void)?
    /// The From pop-up changed by the user.
    var onFromChanged: (@MainActor () -> Void)?

    private let grid: NSGridView
    private let ccRow: NSGridRow
    private let ccSeparatorRow: NSGridRow
    private let bccRow: NSGridRow
    private let bccSeparatorRow: NSGridRow

    init() {
        let fromLabel = Self.label(L10n.T("From"))
        let toLabel = Self.label(L10n.T("To"))
        let ccLabel = Self.label(L10n.T("Cc"))
        let bccLabel = Self.label(L10n.T("Bcc"))
        let toLabelBox = Self.firstLine(toLabel)
        let ccLabelBox = Self.firstLine(ccLabel)
        let bccLabelBox = Self.firstLine(bccLabel)
        let subjectLabel = Self.label(L10n.T("Subject"))

        fromPopup.isBordered = false
        fromPopup.font = Typo.body
        fromPopup.lineBreakMode = .byTruncatingTail
        fromPopup.translatesAutoresizingMaskIntoConstraints = false
        fromPopup.setContentHuggingPriority(.defaultLow, for: .horizontal)
        fromPopup.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)

        Self.configureField(subjectField)
        ccBccButton.isBordered = false
        ccBccButton.controlSize = .small
        ccBccButton.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
        ccBccButton.contentTintColor = .secondaryLabelColor
        ccBccButton.setContentHuggingPriority(.required, for: .horizontal)

        let subjectContainer = Self.fieldContainer(subjectField)
        // The button stays level with the first line of a row that wraps.
        ccBccButton.translatesAutoresizingMaskIntoConstraints = false
        ccBccButton.heightAnchor.constraint(equalToConstant: RecipientTokenField.rowHeight).isActive = true

        let toLine = NSStackView(views: [toField, ccBccButton])
        toLine.orientation = .horizontal
        toLine.distribution = .fill
        ccBccButton.setContentHuggingPriority(.required, for: .horizontal)
        ccBccButton.setContentCompressionResistancePriority(.required, for: .horizontal)
        toLine.spacing = 12
        toLine.alignment = .top
        toLine.translatesAutoresizingMaskIntoConstraints = false

        grid = NSGridView(views: [
            [fromLabel, fromPopup],
            [Self.separator()],
            [toLabelBox, toLine],
            [Self.separator()],
            [ccLabelBox, ccField],
            [Self.separator()],
            [bccLabelBox, bccField],
            [Self.separator()],
            [subjectLabel, subjectContainer],
        ])
        grid.translatesAutoresizingMaskIntoConstraints = false
        grid.rowSpacing = 0
        grid.columnSpacing = 12
        grid.xPlacement = .fill
        grid.yPlacement = .center
        grid.column(at: 0).xPlacement = .leading
        grid.column(at: 1).xPlacement = .fill
        // NSGridView shares spare width between the columns; the label
        // column is the GTK SizeGroup: as wide as its widest label.
        grid.column(at: 0).width = [fromLabel, toLabel, ccLabel, bccLabel, subjectLabel]
            .map { $0.intrinsicContentSize.width }.max() ?? 0
        // The recipient rows (To, Cc, Bcc) take their height from the badges
        // and grow with the lines they wrap onto; the others are one line.
        for i in stride(from: 0, to: grid.numberOfRows, by: 2) {
            if (2...6).contains(i) {
                grid.row(at: i).yPlacement = .top
            } else {
                grid.row(at: i).height = Self.rowHeight
            }
        }
        for i in stride(from: 1, to: grid.numberOfRows, by: 2) {
            grid.row(at: i).height = 1
            grid.row(at: i).mergeCells(in: NSRange(location: 0, length: 2))
            grid.cell(atColumnIndex: 0, rowIndex: i).xPlacement = .fill
        }
        ccSeparatorRow = grid.row(at: 3)
        ccRow = grid.row(at: 4)
        bccSeparatorRow = grid.row(at: 5)
        bccRow = grid.row(at: 6)

        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        boxType = .custom
        titlePosition = .noTitle
        cornerRadius = 12
        borderWidth = 1
        borderColor = Tint.cardBorder
        fillColor = Tint.cardFill
        contentViewMargins = .zero
        guard let content = contentView else { return }
        content.addSubview(grid)
        NSLayoutConstraint.activate([
            grid.topAnchor.constraint(equalTo: content.topAnchor),
            grid.bottomAnchor.constraint(equalTo: content.bottomAnchor),
            grid.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: Self.horizontalInset),
            content.trailingAnchor.constraint(equalTo: grid.trailingAnchor, constant: Self.horizontalInset),
        ])

        // The Cc and Bcc lines start hidden; the button reveals them.
        ccRow.isHidden = true
        ccSeparatorRow.isHidden = true
        bccRow.isHidden = true
        bccSeparatorRow.isHidden = true

        ccBccButton.target = self
        ccBccButton.action = #selector(ccBccClicked(_:))
        fromPopup.target = self
        fromPopup.action = #selector(fromChanged(_:))
        setAccessibility()
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    // MARK: Rows

    /// compose.go `setCcBccVisible`: reveals the lines asked for and keeps
    /// the Cc/Bcc button only while one of them is still hidden. A reply
    /// carrying only a Cc therefore does not open an empty Bcc line as well.
    func setCcBccVisible(cc: Bool, bcc: Bool) {
        if cc {
            ccRow.isHidden = false
            ccSeparatorRow.isHidden = false
        }
        if bcc {
            bccRow.isHidden = false
            bccSeparatorRow.isHidden = false
        }
        ccBccButton.isHidden = !(ccRow.isHidden || bccRow.isHidden)
    }

    /// The recipient rows, in order.
    var recipientFields: [RecipientTokenField] { [toField, ccField, bccField] }

    // MARK: From

    /// Fills the From pop-up (compose.go `setAccounts`): the titles, the
    /// selected index, and whether there is a choice at all.
    func setAccounts(labels: [String], selected: Int, enabled: Bool) {
        fromPopup.removeAllItems()
        for (i, label) in labels.enumerated() {
            // Titles are the accounts' own text: plain, never a menu keyword.
            let item = NSMenuItem(title: label, action: nil, keyEquivalent: "")
            item.tag = i
            fromPopup.menu?.addItem(item)
        }
        if selected >= 0, selected < labels.count {
            fromPopup.selectItem(at: selected)
        }
        fromPopup.isEnabled = enabled
    }

    /// The selected identity's index (`from.Selected()`).
    var selectedAccountIndex: Int { max(fromPopup.indexOfSelectedItem, 0) }

    // MARK: Internals

    @objc private func ccBccClicked(_ sender: Any?) {
        onShowCcBcc?()
    }

    @objc private func fromChanged(_ sender: Any?) {
        onFromChanged?()
    }

    private static func label(_ text: String) -> NSTextField {
        let l = NSTextField(labelWithString: text)
        l.font = Typo.body
        l.textColor = Tint.secondary
        l.alignment = .left
        l.setContentHuggingPriority(.required, for: .horizontal)
        l.setContentCompressionResistancePriority(.required, for: .horizontal)
        return l
    }

    private static func configureField(_ f: NSTextField) {
        f.isBezeled = false
        f.isBordered = false
        f.drawsBackground = false
        f.focusRingType = .none
        f.font = Typo.body
        f.textColor = .labelColor
        f.usesSingleLineMode = true
        f.cell?.wraps = false
        f.cell?.isScrollable = true
        f.lineBreakMode = .byClipping
        f.translatesAutoresizingMaskIntoConstraints = false
        f.setContentHuggingPriority(.defaultLow, for: .horizontal)
        f.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    }

    /// The subject field in a row of the height of one line.
    private static func fieldContainer(_ field: NSTextField) -> NSView {
        let container = NSView()
        container.translatesAutoresizingMaskIntoConstraints = false
        container.addSubview(field)
        NSLayoutConstraint.activate([
            field.leadingAnchor.constraint(equalTo: container.leadingAnchor),
            field.trailingAnchor.constraint(equalTo: container.trailingAnchor),
            field.centerYAnchor.constraint(equalTo: container.centerYAnchor),
            container.heightAnchor.constraint(equalToConstant: rowHeight),
        ])
        return container
    }

    /// A row label as tall as one line of the row, so it stays level with
    /// the first line however many the recipients wrap onto.
    private static func firstLine(_ label: NSTextField) -> NSView {
        let box = NSView()
        box.translatesAutoresizingMaskIntoConstraints = false
        label.translatesAutoresizingMaskIntoConstraints = false
        box.addSubview(label)
        NSLayoutConstraint.activate([
            box.heightAnchor.constraint(equalToConstant: rowHeight),
            label.centerYAnchor.constraint(equalTo: box.centerYAnchor),
            label.leadingAnchor.constraint(equalTo: box.leadingAnchor),
            label.trailingAnchor.constraint(equalTo: box.trailingAnchor),
        ])
        return box
    }

    private static func separator() -> NSBox {
        let b = NSBox()
        b.boxType = .separator
        b.translatesAutoresizingMaskIntoConstraints = false
        return b
    }

    /// `accessibility { labelled-by: … }` of the Blueprint.
    private func setAccessibility() {
        fromPopup.setAccessibilityLabel(L10n.T("From"))
        toField.setAccessibilityLabel(L10n.T("To"))
        ccField.setAccessibilityLabel(L10n.T("Cc"))
        bccField.setAccessibilityLabel(L10n.T("Bcc"))
        subjectField.setAccessibilityLabel(L10n.T("Subject"))
    }
}
