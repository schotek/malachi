// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

extension PrefsWrappingLabel {
    /// A wrapping label of the board's detail: selectable, in `font`, at
    /// most `lines` lines (0 for all) with the last one cut. The wrapping
    /// width follows the width the label is given, so inside a stack
    /// pinned at both edges the height is the wrapped one, in a narrow
    /// panel and a wide pane alike. Text is `stringValue` only.
    static func board(_ text: String, font: NSFont = Typo.body, color: NSColor = .labelColor, lines: Int = 0) -> PrefsWrappingLabel {
        let label = PrefsWrappingLabel(text, color: color)
        label.font = font
        label.isSelectable = true
        label.translatesAutoresizingMaskIntoConstraints = false
        if lines > 0 {
            label.maximumNumberOfLines = lines
            label.lineBreakMode = .byTruncatingTail
            label.cell?.wraps = true
            label.cell?.truncatesLastVisibleLine = true
        }
        return label
    }
}

/// One plain message of the detail's conversation, as the prototype draws
/// it: a tinted card with a hairline border (`BoardPalette.messageFill`,
/// the user's own messages `ownMessageFill`), the sender in semibold at
/// the start and the time at the end, then the whole text the daemon gave
/// (`board.get`, at most `API.Limits.maxBoardMessageTextBytes`) with its
/// line breaks. Nothing in the card scrolls or clips: the card is as tall
/// as its text and the detail's scroll view scrolls the column, wheel
/// events included (no view here handles them).
///
/// An older message of a conversation can start folded (`foldable`):
/// a preview of its text (the lines joined, as Mail's folded card shows
/// its snippet) of at most `foldedLines` lines, not selectable (a
/// selectable field shows its whole text while it is being selected),
/// with Mail's fold arrow and its Expand / Collapse before the sender.
/// The arrow shows only when the whole text is longer than that; it opens
/// the card in place.
/// The fill and the border are resolved in `updateLayer`, so they follow
/// the appearance.
@MainActor
final class BoardMessageCardView: NSView {
    /// The lines a folded card keeps.
    static let foldedLines = 3

    private let mine: Bool
    private let foldable: Bool
    private(set) var folded: Bool
    /// The user opened or folded the card.
    var onFold: ((Bool) -> Void)?

    private let body: PrefsWrappingLabel
    private let text: String
    /// The text's lines joined by spaces: what a folded card shows.
    private let preview: String
    private let foldButton = CardFoldButton()
    /// The text is longer than `foldedLines` at the current width; nil
    /// until measured.
    private var long: Bool?
    private var measuredWidth: CGFloat = -1

    init(_ message: Board.MessageCard, foldable: Bool = false, folded: Bool = false) {
        mine = message.mine
        self.foldable = foldable
        self.folded = foldable && folded
        text = message.text
        preview = message.text.split(whereSeparator: \.isNewline).joined(separator: " ")
        body = PrefsWrappingLabel.board(message.text)
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        wantsLayer = true
        layer?.cornerRadius = ConversationMetrics.cardRadius
        layer?.cornerCurve = .continuous
        layer?.borderWidth = BoardMetrics.cardBorderWidth

        let from = NSTextField(labelWithString: message.from)
        from.font = .systemFont(ofSize: Typo.bodySize, weight: .semibold)
        from.lineBreakMode = .byTruncatingTail
        from.maximumNumberOfLines = 1
        from.isSelectable = true
        from.setContentCompressionResistancePriority(NSLayoutConstraint.Priority(250), for: .horizontal)
        from.setContentHuggingPriority(.defaultLow, for: .horizontal)
        let when = NSTextField(labelWithString: message.when)
        when.font = Typo.caption
        when.textColor = Tint.secondary
        when.lineBreakMode = .byClipping
        when.maximumNumberOfLines = 1
        when.isSelectable = false
        when.setContentHuggingPriority(.required, for: .horizontal)
        when.setContentCompressionResistancePriority(.defaultHigh, for: .horizontal)
        foldButton.target = self
        foldButton.action = #selector(foldClicked(_:))
        foldButton.isHidden = true
        foldButton.setContentHuggingPriority(.required, for: .horizontal)
        let head = NSStackView(views: [foldButton, from, when])
        head.orientation = .horizontal
        head.alignment = .firstBaseline
        head.spacing = 8
        head.setCustomSpacing(4, after: foldButton)

        let column = FillStackView(fillingViews: [head, body])
        column.spacing = 5
        column.edgeInsets = NSEdgeInsets(
            top: ConversationMetrics.cardPaddingV, left: ConversationMetrics.cardPaddingH,
            bottom: ConversationMetrics.cardPaddingV, right: ConversationMetrics.cardPaddingH)
        addSubview(column)
        NSLayoutConstraint.activate([
            column.leadingAnchor.constraint(equalTo: leadingAnchor),
            column.trailingAnchor.constraint(equalTo: trailingAnchor),
            column.topAnchor.constraint(equalTo: topAnchor),
            column.bottomAnchor.constraint(equalTo: bottomAnchor),
        ])
        showFold()
        // A group named after the sender; the text is read once, from the
        // labels inside it.
        setAccessibilityElement(true)
        setAccessibilityRole(.group)
        setAccessibilityLabel(message.from)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    // MARK: Fold

    /// The height `text` needs in the body's font at `width` (0: one line).
    private func height(of text: String, width: CGFloat) -> CGFloat {
        let probe = NSTextField(wrappingLabelWithString: text)
        probe.font = body.font
        let w = width > 0 ? width : .greatestFiniteMagnitude
        return probe.cell?.cellSize(forBounds: NSRect(x: 0, y: 0, width: w, height: .greatestFiniteMagnitude)).height ?? 0
    }

    /// The whole text or the preview, the arrow shown or not, as `folded`
    /// and the measured length say. The preview is one paragraph, so the
    /// line limit ends it with an ellipsis on its last whole line; the
    /// label keeps its full vertical compression resistance either way,
    /// so nothing squeezes a line in half.
    private func showFold() {
        let cut = folded && long != false
        if cut {
            if body.stringValue != preview {
                body.stringValue = preview
            }
            body.maximumNumberOfLines = Self.foldedLines
            body.lineBreakMode = .byTruncatingTail
            body.cell?.truncatesLastVisibleLine = true
            body.isSelectable = false
        } else {
            if body.stringValue != text {
                body.stringValue = text
            }
            body.maximumNumberOfLines = 0
            body.lineBreakMode = .byWordWrapping
            body.cell?.truncatesLastVisibleLine = false
            body.isSelectable = true
        }
        body.invalidateIntrinsicContentSize()
        let arrow = foldable && long == true
        if foldButton.isHidden == arrow {
            foldButton.isHidden = !arrow
        }
        let tip = folded ? L10n.T("Expand") : L10n.T("Collapse")
        foldButton.image = Icon.image(folded ? "pan-end" : "pan-down", size: .small)
        foldButton.toolTip = tip
        foldButton.setAccessibilityLabel(tip)
    }

    /// The text's width follows the card's (the column's insets inside
    /// it), so the length is measured whenever the card's width changes,
    /// whatever lays the text out.
    override func setFrameSize(_ newSize: NSSize) {
        super.setFrameSize(newSize)
        measure()
    }

    override func layout() {
        super.layout()
        measure()
    }

    /// Whether the whole text is longer than `foldedLines` at the card's
    /// text width; shows the arrow (and the preview) when that changes.
    private func measure() {
        guard foldable else { return }
        let width = bounds.width - 2 * ConversationMetrics.cardPaddingH
        guard width > 0, width != measuredWidth else { return }
        measuredWidth = width
        let isLong = height(of: text, width: width) > height(of: "X", width: 0) * CGFloat(Self.foldedLines) + 1
        if isLong != long {
            long = isLong
            showFold()
        }
    }

    @objc private func foldClicked(_ sender: Any?) {
        folded.toggle()
        showFold()
        onFold?(folded)
    }

    /// DEVELOPMENT AID (`MALACHI_START`): the card's frame, the text's
    /// frame and the height the whole text needs at that width.
    var developmentMetrics: String {
        let need = height(of: body.stringValue, width: body.bounds.width)
        return String(format: "card %.0fx%.0f text %.0fx%.0f needs %.0f (whole text %.0f, a line %.0f) cut %.0f foldable %d folded %d arrow %d",
                      frame.width, frame.height, body.frame.width, body.frame.height, need, height(of: text, width: body.bounds.width),
                      height(of: "X", width: 0), max(0, need - body.frame.height),
                      foldable ? 1 : 0, folded ? 1 : 0, foldButton.isHidden ? 0 : 1)
    }

    /// DEVELOPMENT AID: the body label, for the wheel check.
    var developmentBody: NSTextField { body }

    override var wantsUpdateLayer: Bool { true }

    override func updateLayer() {
        effectiveAppearance.performAsCurrentDrawingAppearance {
            layer?.backgroundColor = (mine ? BoardPalette.ownMessageFill : BoardPalette.messageFill).cgColor
            layer?.borderColor = BoardPalette.cardBorder(in: self).cgColor
        }
    }
}
