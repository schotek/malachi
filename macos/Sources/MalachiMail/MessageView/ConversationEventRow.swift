// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// A status or assignee change of an issue in the conversation view
/// (`Conversation.ItemKind.event`): not a card but a compact native row,
/// never a web view and never unread: one line per change
/// (`Item.eventLines`) in small secondary text, who made it, and the time
/// at the trailing edge. Its mark is a dot on the timeline beside its first
/// line (`ConversationRow`). The texts keep to the columns of the cards'
/// own: they start and end where a card's padding ends. Everything is
/// plain text (`stringValue`); the lines and the name come from the Jira
/// site, cleaned for one line (`Jira.clean`). In a narrow pane the name
/// gives way before the lines wrap (`ConversationEventLayout`). The row is
/// one accessibility element: the changes on one line (`Item.eventText`),
/// the name and the date.
@MainActor
final class ConversationEventRow: NSView {
    let id: MessageID
    private(set) var item: Conversation.Item

    /// The short date of the list instead of the full date and time (a
    /// narrow pane, `ConversationLayout.compactDates`).
    var compactDate = false {
        didSet {
            if compactDate != oldValue {
                renderDate()
            }
        }
    }

    private let lines = NSStackView()
    private let senderLabel = NSTextField(labelWithString: "")
    private let dateLabel = NSTextField(labelWithString: "")
    private var lineLabels: [NSTextField] = []
    private var senderWidth: NSLayoutConstraint!
    /// The widest line, not wrapped.
    private var linesNatural: CGFloat = 0
    private var plan: ConversationEventLayout?

    init(_ item: Conversation.Item) {
        id = item.id ?? MessageID("")
        self.item = item
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        build()
        update(item)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    private func build() {
        lines.orientation = .vertical
        lines.alignment = .leading
        lines.spacing = 2
        lines.setHuggingPriority(.defaultLow, for: .horizontal)
        lines.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)

        for l in [senderLabel, dateLabel] {
            l.font = Typo.caption
            l.textColor = Tint.secondary
            l.isSelectable = false
            l.lineBreakMode = .byTruncatingTail
            l.maximumNumberOfLines = 1
            l.setContentHuggingPriority(.required, for: .horizontal)
        }
        // The name's width is the plan's (`layout`).
        senderLabel.setContentCompressionResistancePriority(NSLayoutConstraint.Priority(240), for: .horizontal)
        senderWidth = senderLabel.widthAnchor.constraint(
            lessThanOrEqualToConstant: CGFloat(ConversationEventLayout.maxSender))
        senderWidth.isActive = true
        dateLabel.setContentCompressionResistancePriority(.required, for: .horizontal)

        // The lines take the width that is left, so the name and the time
        // keep to the trailing edge.
        let row = NSStackView(views: [lines, senderLabel, dateLabel])
        row.orientation = .horizontal
        row.alignment = .firstBaseline
        row.distribution = .fill
        row.spacing = CGFloat(ConversationEventLayout.spacing)
        row.translatesAutoresizingMaskIntoConstraints = false
        addSubview(row)
        NSLayoutConstraint.activate([
            row.topAnchor.constraint(equalTo: topAnchor),
            row.bottomAnchor.constraint(equalTo: bottomAnchor),
            row.leadingAnchor.constraint(equalTo: leadingAnchor, constant: ConversationMetrics.cardPaddingH),
            row.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -ConversationMetrics.cardPaddingH),
        ])
        setAccessibilityElement(true)
        setAccessibilityRole(.group)
    }

    /// Shows `item` (the same member, its content as the model has it now).
    func update(_ item: Conversation.Item) {
        self.item = item
        for v in lines.arrangedSubviews {
            lines.removeArrangedSubview(v)
            v.removeFromSuperview()
        }
        lineLabels = []
        linesNatural = 0
        let font = Typo.caption
        for text in item.eventLines {
            let l = NSTextField(wrappingLabelWithString: text)
            l.font = font
            l.textColor = Tint.secondary
            l.isSelectable = false
            l.maximumNumberOfLines = 0
            l.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
            lines.addArrangedSubview(l)
            lineLabels.append(l)
            linesNatural = max(linesNatural, ceil((text as NSString).size(withAttributes: [.font: font]).width))
        }
        senderLabel.stringValue = item.sender
        senderLabel.toolTip = item.sender.isEmpty ? nil : item.sender
        renderDate()
    }

    // MARK: Layout

    override func layout() {
        share()
        super.layout()
    }

    /// Shares the row's width between the lines and the name
    /// (`ConversationEventLayout`): the name's width, and where the lines
    /// wrap.
    private func share() {
        let width = bounds.width - 2 * ConversationMetrics.cardPaddingH
        guard width > 0 else { return }
        let name = item.sender.isEmpty ? 0 : senderLabel.intrinsicContentSize.width
        let date = dateLabel.isHidden ? 0 : dateLabel.intrinsicContentSize.width
        let next = ConversationEventLayout(
            width: Double(width), lines: Double(linesNatural), sender: Double(ceil(name)), date: Double(ceil(date)))
        guard next != plan else { return }
        plan = next
        senderLabel.isHidden = next.sender <= 0
        if next.sender > 0 {
            senderWidth.constant = CGFloat(next.sender)
        }
        for l in lineLabels {
            l.preferredMaxLayoutWidth = CGFloat(next.lines)
        }
    }

    /// The date at the row's end: in full, or the list's short form in a
    /// narrow pane, with the full one in the tooltip. Accessibility hears
    /// the full one.
    private func renderDate() {
        let full = item.message.map { $0.date.isGoZero ? "" : formatDateTime($0.date) } ?? ""
        let short = item.message.map { formatDate($0.date, now: Date()) } ?? ""
        dateLabel.stringValue = compactDate ? short : full
        dateLabel.toolTip = compactDate && !full.isEmpty ? full : nil
        dateLabel.isHidden = full.isEmpty
        // The widths are shared again.
        plan = nil
        needsLayout = true
        setAccessibilityLabel(item.eventText)
        setAccessibilityValue([item.sender, full].filter { !$0.isEmpty }.joined(separator: ", "))
    }
}

/// The row at the bottom of a conversation (after its oldest member shown,
/// `ConversationLayout.displayOrder`) whose older members are left out
/// (`Conversation.ItemKind.truncated`): its sentence in small secondary
/// text, in the column of the cards' texts. It sits on the timeline like
/// an event: its mark is a dot (`ConversationRow`), where the line ends.
@MainActor
final class ConversationTruncatedRow: NSView {
    private let label = NSTextField(wrappingLabelWithString: "")

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        label.font = Typo.caption
        label.textColor = Tint.secondary
        label.isSelectable = false
        label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        label.translatesAutoresizingMaskIntoConstraints = false
        addSubview(label)
        NSLayoutConstraint.activate([
            label.topAnchor.constraint(equalTo: topAnchor),
            label.bottomAnchor.constraint(equalTo: bottomAnchor),
            label.leadingAnchor.constraint(equalTo: leadingAnchor, constant: ConversationMetrics.cardPaddingH),
            label.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -ConversationMetrics.cardPaddingH),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    var text: String {
        get { label.stringValue }
        set { label.stringValue = newValue }
    }
}
