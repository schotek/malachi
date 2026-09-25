// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The headers above the body (window.blp lines 465–539): subject, sender,
/// recipients, date, the attachment chips, the hint that only the plain
/// text is shown, and a separator. Everything is server data shown as plain
/// text; the labels are selectable but never take the focus by themselves
/// (a focused selectable label selects all of its text, see
/// message_window.blp), and they select through `PlainFieldEditor`, so
/// the selection can be copied and nothing else (no Look Up, Services or
/// Share on a sender's name or a subject).
@MainActor
final class MessageHeaderView: NSView {
    static let spacing: CGFloat = 12
    static let inset: CGFloat = 24

    var subject: String {
        get { subjectLabel.stringValue }
        set { subjectLabel.stringValue = newValue }
    }

    var from: String {
        get { fromLabel.stringValue }
        set { fromLabel.stringValue = newValue }
    }

    /// The To / Cc block; hidden when empty.
    var recipients: String {
        get { recipientsLabel.stringValue }
        set {
            recipientsLabel.stringValue = newValue
            recipientsLabel.isHidden = newValue.isEmpty
        }
    }

    var date: String {
        get { dateLabel.stringValue }
        set { dateLabel.stringValue = newValue }
    }

    /// Why only the plain text is shown; hidden otherwise.
    var hintVisible: Bool {
        get { !hintLabel.isHidden }
        set { hintLabel.isHidden = !newValue }
    }

    /// The attachment chips (`message_attachments`); hidden when empty.
    let chips = FlowView(spacing: 6, lineSpacing: 6)

    var chipsVisible: Bool {
        get { !chips.isHidden }
        set { chips.isHidden = !newValue }
    }

    private let subjectLabel = PlainLabel(wrappingLabelWithString: "")
    private let fromLabel = PlainLabel(wrappingLabelWithString: "")
    private let recipientsLabel = PlainLabel(wrappingLabelWithString: "")
    private let dateLabel = PlainLabel(labelWithString: "")
    private let hintLabel = PlainLabel(wrappingLabelWithString: "")

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false

        for l in [subjectLabel, fromLabel, recipientsLabel, hintLabel] {
            l.isSelectable = true
            l.isEditable = false
            l.allowsEditingTextAttributes = false
            l.refusesFirstResponder = true
            l.maximumNumberOfLines = 0
            l.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
            l.setContentHuggingPriority(.defaultLow, for: .horizontal)
        }
        subjectLabel.font = Typo.title2Bold
        fromLabel.font = Typo.body
        fromLabel.textColor = Tint.secondary
        recipientsLabel.font = Typo.body
        recipientsLabel.textColor = Tint.secondary
        recipientsLabel.isHidden = true
        dateLabel.font = Typo.caption
        dateLabel.textColor = Tint.secondary
        dateLabel.isSelectable = false
        dateLabel.lineBreakMode = .byTruncatingTail
        dateLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        hintLabel.font = Typo.caption
        hintLabel.textColor = Tint.secondary
        hintLabel.isSelectable = false
        hintLabel.stringValue = L10n.T("The formatted version of this message could not be shown safely; this is its plain text.")
        hintLabel.isHidden = true
        chips.isHidden = true

        let separator = NSBox()
        separator.boxType = .separator
        separator.translatesAutoresizingMaskIntoConstraints = false

        let stack = FillStackView(fillingViews: [subjectLabel, fromLabel, recipientsLabel, dateLabel, chips, hintLabel, separator])
        stack.spacing = Self.spacing
        stack.edgeInsets = NSEdgeInsets(top: Self.inset, left: Self.inset, bottom: 0, right: Self.inset)
        stack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(stack)
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: topAnchor),
            stack.bottomAnchor.constraint(equalTo: bottomAnchor),
            stack.leadingAnchor.constraint(equalTo: leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: trailingAnchor),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }
}

/// A label whose selection lives in a `PlainFieldEditor` rather than the
/// window's field editor: its cell hands out one of its own.
@MainActor
private final class PlainLabel: NSTextField {
    override class var cellClass: AnyClass? {
        get { PlainLabelCell.self }
        set {}
    }

    override init(frame: NSRect) {
        super.init(frame: frame)
        // Should the label convenience initialisers ever bypass
        // `cellClass`, the cell is replaced before they configure it.
        if !(cell is PlainLabelCell) {
            cell = PlainLabelCell(textCell: "")
        }
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }
}

/// The cell of `PlainLabel`: one `PlainFieldEditor` per cell, since a
/// cell is edited by at most one editor at a time and windows do not
/// share editors.
@MainActor
private final class PlainLabelCell: NSTextFieldCell {
    private var editor: PlainFieldEditor?

    override func fieldEditor(for controlView: NSView) -> NSTextView? {
        if let editor {
            return editor
        }
        let e = PlainFieldEditor()
        editor = e
        return e
    }
}
