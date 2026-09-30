// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The issue card at the top of a Jira message's headers (`Jira.issueCard`):
/// the key as a link to the issue, the status pill, the Internal badge of an
/// internal service-desk comment, who relayed the comment ("via …") and
/// whether it was edited, then Assignee, Priority, Type and Reporter. The
/// issue's summary is the header's subject. Hidden for a mail message.
/// Everything but the labels comes from the Jira site and is shown as plain
/// text (`stringValue`, or a title built from it with a font and a colour
/// only); the key opens its URL only when `Jira.isIssueURL` accepts it for
/// the account's site (`openable`), otherwise it is plain text.
@MainActor
final class IssueCardView: NSView {
    /// The key was clicked: open `url` (`openIssue`, which checks it again).
    var onOpen: (@MainActor (_ url: String) -> Void)?

    private let keyButton = NSButton()
    private let statusPill = PillLabel()
    private let internalPill = PillLabel()
    private let viaLabel = NSTextField(labelWithString: "")
    private let editedLabel = NSTextField(labelWithString: "")
    private let fields = FlowView(spacing: 16, lineSpacing: 4)
    /// Assignee, Priority, Type and Reporter: a label and its value each.
    private var fieldLabels: [NSTextField] = []
    private var fieldValues: [NSTextField] = []
    private var url = ""

    /// The widest a field's value gets before it is truncated: a name from
    /// the site can be long, and the flow never truncates by itself.
    static let valueMaxWidth: CGFloat = 220

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        build()
        isHidden = true
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    private func build() {
        keyButton.isBordered = false
        keyButton.setButtonType(.momentaryChange)
        keyButton.refusesFirstResponder = true
        keyButton.target = self
        keyButton.action = #selector(keyClicked)
        keyButton.setContentHuggingPriority(.required, for: .horizontal)
        keyButton.setContentCompressionResistancePriority(.required, for: .horizontal)

        for pill in [statusPill, internalPill] {
            pill.font = Typo.caption
            pill.isHidden = true
        }
        for label in [viaLabel, editedLabel] {
            label.font = Typo.caption
            label.textColor = Tint.secondary
            label.isSelectable = false
            label.lineBreakMode = .byTruncatingTail
            label.maximumNumberOfLines = 1
            label.isHidden = true
            label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        }

        // The width that is left goes after the last of them: without a
        // badge to take it (the card of a conversation has none), the
        // status pill would be stretched across the card.
        let rest = NSView()
        rest.translatesAutoresizingMaskIntoConstraints = false
        rest.setContentHuggingPriority(NSLayoutConstraint.Priority(1), for: .horizontal)
        rest.setContentCompressionResistancePriority(NSLayoutConstraint.Priority(1), for: .horizontal)
        let top = NSStackView(views: [keyButton, statusPill, internalPill, viaLabel, editedLabel, rest])
        top.orientation = .horizontal
        top.alignment = .centerY
        top.distribution = .fill
        top.spacing = 8
        top.setHuggingPriority(.defaultLow, for: .horizontal)

        for _ in 0..<4 {
            let label = NSTextField(labelWithString: "")
            label.font = Typo.caption
            label.textColor = Tint.secondary
            label.isSelectable = false
            let value = NSTextField(labelWithString: "")
            value.font = Typo.caption
            value.isSelectable = false
            value.lineBreakMode = .byTruncatingTail
            value.maximumNumberOfLines = 1
            value.widthAnchor.constraint(lessThanOrEqualToConstant: Self.valueMaxWidth).isActive = true
            let pair = NSStackView(views: [label, value])
            pair.orientation = .horizontal
            pair.alignment = .firstBaseline
            pair.spacing = 4
            fields.addView(pair)
            fieldLabels.append(label)
            fieldValues.append(value)
        }

        let stack = FillStackView(fillingViews: [top, fields])
        stack.spacing = 6
        CalloutCard.install(stack, in: self, margins: NSEdgeInsets())
    }

    /// Shows `card`, or hides the view for none (a mail message).
    /// `openable`: the card's URL leads to an issue of the account's site.
    func show(_ card: Jira.Card?, openable: Bool) {
        guard let card else {
            isHidden = true
            url = ""
            return
        }
        isHidden = false
        url = openable ? card.url : ""
        // The key is the card's title: the size of a message's subject
        // (Typo.title2Bold), which the card stands in for.
        let font = NSFont.monospacedDigitSystemFont(ofSize: Typo.title2Bold.pointSize, weight: .bold)
        let colour: NSColor = openable ? .linkColor : .labelColor
        keyButton.attributedTitle = NSAttributedString(string: card.key, attributes: [.font: font, .foregroundColor: colour])
        keyButton.isEnabled = openable
        keyButton.toolTip = openable ? card.openTooltip : nil
        keyButton.setAccessibilityLabel(openable ? card.openTooltip : card.key)
        keyButton.isHidden = card.key.isEmpty

        statusPill.stringValue = card.status
        statusPill.setAccessibilityLabel(card.statusLabel)
        statusPill.isHidden = card.status.isEmpty
        IssuePill.paint(statusPill, IssuePill.colours(card.statusStyle), emphasized: false)
        internalPill.stringValue = card.internalLabel
        internalPill.isHidden = !card.internal
        IssuePill.paint(internalPill, IssuePill.internalColours, emphasized: false)
        viaLabel.stringValue = card.via
        viaLabel.isHidden = card.via.isEmpty
        editedLabel.stringValue = card.edited
        editedLabel.isHidden = card.edited.isEmpty

        for (i, row) in card.rows.prefix(fieldValues.count).enumerated() {
            fieldLabels[i].stringValue = row.label
            fieldValues[i].stringValue = row.value
            fieldValues[i].toolTip = row.missing ? nil : row.value
            fieldValues[i].textColor = row.missing ? Tint.secondary : .labelColor
            fieldLabels[i].superview?.isHidden = false
        }
        for i in card.rows.count..<max(card.rows.count, fieldValues.count) {
            fieldLabels[i].superview?.isHidden = true
        }
        fields.needsLayout = true
        fields.invalidateIntrinsicContentSize()
    }

    @objc private func keyClicked() {
        guard !url.isEmpty else { return }
        onOpen?(url)
    }
}
