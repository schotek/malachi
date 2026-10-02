// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The small tags of a case, over `PillLabel`. Every text is a cleaned
/// plain string (the Core's `cleanLine`) set through `stringValue`. The
/// detail's tags (the factories with a text) carry what a cut tag does not
/// show in a tooltip; the rows' tags (the setters) do not: a tooltip rect
/// takes no notice of the sliding panel over a row and would show through
/// it, and the detail has the full text.
@MainActor
enum BoardTag {
    /// The account's name, neutral.
    static func account(_ text: String = "") -> PillLabel {
        let pill = make()
        setAccount(pill, text, toolTip: true)
        return pill
    }

    static func setAccount(_ pill: PillLabel, _ text: String, toolTip: Bool = false) {
        pill.setText(text, maxCharacters: 24)
        pill.toolTip = toolTip && !text.isEmpty ? text : nil
        pill.isHidden = text.isEmpty
    }

    /// "Due 3 Oct" chip; `text` is the label of the view model.
    static func due(_ text: String = "") -> PillLabel {
        let pill = make()
        pill.font = .systemFont(ofSize: Typo.captionSize, weight: .semibold)
        setDue(pill, text)
        return pill
    }

    static func setDue(_ pill: PillLabel, _ text: String) {
        pill.setText(text, maxCharacters: 24)
        pill.toolTip = nil
        pill.isHidden = text.isEmpty
    }

    /// "KEY · Status" in the colours of the issue's status; hidden without
    /// a key.
    static func issue(key: String = "", status: String = "") -> PillLabel {
        let pill = make()
        setIssue(pill, key: key, status: status, toolTip: true)
        return pill
    }

    static func setIssue(_ pill: PillLabel, key: String, status: String, toolTip: Bool = false) {
        let text = status.isEmpty ? key : "\(key) · \(status)"
        pill.setText(text, maxCharacters: 40)
        pill.toolTip = toolTip && !key.isEmpty ? text : nil
        pill.isHidden = key.isEmpty
    }

    /// The message count's text ("3 messages") in the caption.
    static func count(_ text: String = "") -> NSTextField {
        let label = NSTextField(labelWithString: text)
        label.font = Typo.caption
        label.textColor = Tint.secondary
        label.lineBreakMode = .byClipping
        label.maximumNumberOfLines = 1
        label.isSelectable = false
        label.isHidden = text.isEmpty
        label.setContentHuggingPriority(.required, for: .horizontal)
        label.setContentCompressionResistancePriority(.required, for: .horizontal)
        return label
    }

    /// Paints a tag; on a selected row (`emphasized`) in the selection's
    /// colours.
    static func paint(_ pill: PillLabel, _ c: IssuePill.Colours, emphasized: Bool) {
        IssuePill.paint(pill, c, emphasized: emphasized)
    }

    private static func make() -> PillLabel {
        let pill = PillLabel()
        pill.font = Typo.caption
        return pill
    }
}

/// The bar on the leading edge of a case: a rounded stripe in the state's
/// colour, resolved when it draws.
@MainActor
final class BoardBarView: NSView {
    var color: NSColor = .secondaryLabelColor {
        didSet { needsDisplay = true }
    }

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        widthAnchor.constraint(equalToConstant: BoardMetrics.barWidth).isActive = true
        setAccessibilityElement(false)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func draw(_ dirtyRect: NSRect) {
        color.setFill()
        let r = BoardMetrics.barWidth / 2
        NSBezierPath(roundedRect: bounds, xRadius: r, yRadius: r).fill()
    }
}

/// The content of one case in a list, a column or the Today page, from a
/// `Board.Row` (`configure(_:)`): the state's bar on the leading edge, the
/// person (semibold, bold while unread) with the time trailing, the title,
/// the snippet and the meta line (account tag, issue tag, due chip, the
/// attachment mark, the message count). `compact` is the Today line: the
/// title with the due chip and the time, and under it "person · account ·
/// snippet". Text the assistant wrote (the title, the summary as the
/// snippet) puts the assistant's mark in front of the title
/// (`Row.marksAssistant`). Every string is set through `stringValue`; the
/// cell is one accessibility element, labelled with `row.spoken`.
///
/// The cell has a fixed height (`BoardMetrics`): the tables of the board
/// do not size their rows automatically. `cardInset` leaves room for the
/// card `BoardCardRowView` draws around it.
@MainActor
final class BoardCaseContentView: NSTableCellView {
    static let reuseIdentifier = NSUserInterfaceItemIdentifier("BoardCaseContent")

    let titleLines: Int
    let snippetLines: Int
    let compact: Bool
    /// The cell sits in a card (`BoardCardRowView`).
    let card: Bool

    private let bar = BoardBarView()
    private let person = NSTextField(labelWithString: "")
    private let time = NSTextField(labelWithString: "")
    private let title = NSTextField(wrappingLabelWithString: "")
    /// The assistant's mark in front of the title.
    private let mark = BoardAssistantMark.label(font: BoardFonts.title)
    private let snippet = NSTextField(wrappingLabelWithString: "")
    private let account = BoardTag.account()
    private let issue = BoardTag.issue()
    private let due = BoardTag.due()
    /// When a snoozed case comes back (`Row.remind`), in the meta line.
    private let remind = NSTextField(labelWithString: "")
    private let attachment = NSImageView()
    private let count = BoardTag.count()
    private let column = FillStackView()

    private var state = Board.State.info
    private var issueStyle = Jira.StatusStyle.todo
    private var emphasized = false
    private var unread = false

    init(titleLines: Int = 1, snippetLines: Int = 2, compact: Bool = false, card: Bool = false) {
        self.titleLines = titleLines
        self.snippetLines = snippetLines
        self.compact = compact
        self.card = card
        super.init(frame: .zero)
        identifier = BoardCaseContentView.reuseIdentifier
        build()
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    private func build() {
        for label in [person, time] {
            label.lineBreakMode = .byTruncatingTail
            label.maximumNumberOfLines = 1
            label.isSelectable = false
        }
        person.font = BoardFonts.person(unread: false)
        person.setContentHuggingPriority(.defaultLow, for: .horizontal)
        person.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        time.font = Typo.caption
        time.setContentHuggingPriority(.required, for: .horizontal)
        time.setContentCompressionResistancePriority(.required, for: .horizontal)
        time.alignment = .right

        for label in [title, snippet] {
            label.isSelectable = false
            label.setContentHuggingPriority(.defaultLow, for: .horizontal)
            label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        }
        title.font = BoardFonts.title
        snippet.font = BoardFonts.snippet
        Self.setLines(title, compact ? 1 : max(titleLines, 1))
        Self.setLines(snippet, compact ? 1 : snippetLines)
        snippet.isHidden = !compact && snippetLines == 0

        attachment.image = Icon.image("mail-attachment", size: .small)
        attachment.imageScaling = .scaleNone
        attachment.setContentHuggingPriority(.required, for: .horizontal)
        attachment.setContentCompressionResistancePriority(.required, for: .horizontal)
        attachment.isHidden = true

        mark.isHidden = true
        let top: NSStackView
        let lines: [NSView]
        let titleLine: NSView
        if compact {
            // Title (with the chip) and time, then the detail line.
            top = NSStackView(views: [mark, title, due, time])
            top.setCustomSpacing(BoardAssistantMark.gap, after: mark)
            lines = [top, snippet]
            titleLine = top
        } else {
            top = NSStackView(views: [person, time])
            remind.font = Typo.caption
            remind.lineBreakMode = .byTruncatingTail
            remind.isSelectable = false
            remind.isHidden = true
            remind.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
            let meta = NSStackView(views: [account, issue, due, remind, attachment, count])
            meta.orientation = .horizontal
            meta.alignment = .centerY
            meta.spacing = BoardMetrics.itemGap
            meta.setHuggingPriority(.defaultLow, for: .horizontal)
            meta.setClippingResistancePriority(.defaultLow, for: .horizontal)
            let line = NSStackView(views: [mark, title])
            line.orientation = .horizontal
            line.alignment = .firstBaseline
            line.spacing = BoardAssistantMark.gap
            line.distribution = .fill
            line.setHuggingPriority(.defaultLow, for: .horizontal)
            titleLine = line
            lines = [top, line, snippet, meta]
        }
        top.orientation = .horizontal
        top.alignment = .firstBaseline
        top.spacing = BoardMetrics.itemGap
        top.distribution = .fill
        top.setHuggingPriority(.defaultLow, for: .horizontal)

        column.spacing = BoardMetrics.lineGap
        column.setContentHuggingPriority(.defaultLow, for: .horizontal)
        column.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        for v in lines {
            column.addArrangedSubview(v)
        }
        if !compact {
            column.setCustomSpacing(BoardMetrics.metaGap, after: snippetLines > 0 ? snippet : titleLine)
        }

        let content = NSStackView(views: [bar, column])
        content.orientation = .horizontal
        content.alignment = .top
        content.distribution = .fill
        content.spacing = BoardMetrics.barGap
        content.translatesAutoresizingMaskIntoConstraints = false
        addSubview(content)
        bar.setContentHuggingPriority(.required, for: .horizontal)

        let padV = card ? BoardMetrics.cardPaddingV + BoardMetrics.cardInsetV : BoardMetrics.listPaddingV
        let padH = card ? BoardMetrics.cardPaddingH + BoardMetrics.cardInsetH : BoardMetrics.listPaddingH
        let padLeading = card ? BoardMetrics.cardBarPaddingH + BoardMetrics.cardInsetH : BoardMetrics.listPaddingH
        // The bar is as tall as the text beside it: a stack view sets
        // its height by the column (the bar has no intrinsic height).
        NSLayoutConstraint.activate([
            content.leadingAnchor.constraint(equalTo: leadingAnchor, constant: padLeading),
            content.topAnchor.constraint(equalTo: topAnchor, constant: padV),
            trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: padH),
            bar.heightAnchor.constraint(equalTo: column.heightAnchor),
        ])
        // The title and the snippet keep the height of all their lines,
        // a short snippet included: the meta line stays where the row's
        // fixed height (`BoardMetrics.contentHeight`) expects it.
        if !compact {
            title.heightAnchor.constraint(equalToConstant: BoardMetrics.titleLine * CGFloat(max(titleLines, 1))).isActive = true
            if snippetLines > 0 {
                snippet.heightAnchor.constraint(equalToConstant: BoardMetrics.snippetLine * CGFloat(snippetLines)).isActive = true
            }
        }

        // One accessibility element per row, labelled with `spoken` (the
        // row view around it is the row).
        setAccessibilityElement(true)
        for v in [bar, person, time, mark, title, titleLine, snippet, account, issue, due, remind, attachment, count, column, content]
            as [NSView]
        {
            v.setAccessibilityElement(false)
        }
    }

    /// One line: truncated at its end. More: word-wrapped, the last line
    /// truncated. A truncating `lineBreakMode` alone keeps an `NSTextField`
    /// on one line whatever `maximumNumberOfLines` says (the cell stops
    /// wrapping), so wrapping and the cut are set on the cell.
    private static func setLines(_ label: NSTextField, _ lines: Int) {
        if lines <= 1 {
            label.usesSingleLineMode = true
            label.cell?.wraps = false
            label.lineBreakMode = .byTruncatingTail
            label.maximumNumberOfLines = 1
        } else {
            label.usesSingleLineMode = false
            label.lineBreakMode = .byWordWrapping
            label.cell?.wraps = true
            label.cell?.truncatesLastVisibleLine = true
            label.maximumNumberOfLines = lines
        }
    }

    // MARK: Content

    func configure(_ row: Board.Row) {
        state = row.state
        issueStyle = row.issueStyle
        unread = row.unread
        time.stringValue = row.time
        title.stringValue = row.title
        mark.isHidden = !row.marksAssistant
        BoardTag.setDue(due, row.due)
        if compact {
            // "person · account · snippet", the empty parts left out.
            snippet.stringValue = [row.person, row.account, row.snippet].filter { !$0.isEmpty }.joined(separator: " · ")
        } else {
            person.stringValue = row.person
            person.font = BoardFonts.person(unread: row.unread)
            snippet.stringValue = row.snippet
            snippet.isHidden = snippetLines == 0
            BoardTag.setAccount(account, row.account)
            BoardTag.setIssue(issue, key: row.issueKey, status: row.issueStatus)
            remind.stringValue = row.remind
            remind.isHidden = row.remind.isEmpty
            attachment.isHidden = !row.attachments
            count.stringValue = row.countText
            count.isHidden = row.countText.isEmpty
        }
        setAccessibilityLabel(row.spoken)
        applyColours()
    }

    override var backgroundStyle: NSView.BackgroundStyle {
        didSet {
            emphasized = backgroundStyle == .emphasized
            applyColours()
        }
    }

    /// The colours for the selection state: the selection's text colour on
    /// the accent highlight, the board's colours otherwise.
    private func applyColours() {
        let primary: NSColor = emphasized ? .alternateSelectedControlTextColor : .labelColor
        let secondary: NSColor = emphasized ? .alternateSelectedControlTextColor : Tint.secondary
        bar.color = emphasized ? NSColor.alternateSelectedControlTextColor.withAlphaComponent(0.8) : BoardPalette.accent(state)
        person.textColor = primary
        title.textColor = primary
        mark.textColor = emphasized ? .alternateSelectedControlTextColor : BoardPalette.assistant
        snippet.textColor = secondary
        time.textColor = secondary
        count.textColor = secondary
        remind.textColor = secondary
        attachment.contentTintColor = secondary
        BoardTag.paint(account, BoardPalette.accountColours, emphasized: emphasized)
        BoardTag.paint(issue, IssuePill.colours(issueStyle), emphasized: emphasized)
        BoardTag.paint(due, BoardPalette.dueColours, emphasized: emphasized)
    }

    override func layout() {
        super.layout()
        // A wrapping label needs the width it will get to know its
        // height; the row's height is fixed, so only the cut matters.
        for label in [title, snippet] where label.maximumNumberOfLines > 1 {
            let w = label.frame.width
            if w > 0, abs(label.preferredMaxLayoutWidth - w) > 0.5 {
                label.preferredMaxLayoutWidth = w
                needsLayout = true
            }
        }
    }

    override func prepareForReuse() {
        super.prepareForReuse()
        emphasized = false
    }
}

/// The row view of a card in a column: an inset rounded card (the card's
/// fill, the hot tint over it for a hot case, the border) with a 2 pt
/// accent border for the selection instead of the standard highlight, so
/// the cell's text keeps its colours (`interiorBackgroundStyle`). The
/// background of the table is clear.
@MainActor
final class BoardCardRowView: NSTableRowView {
    static let reuseIdentifier = NSUserInterfaceItemIdentifier("BoardCardRow")

    enum Style {
        case plain
        /// A hot case: the card is tinted red.
        case hot
        /// A commitment: tinted with the assistant's colour.
        case assistant
        /// The placeholder of an empty column: a dashed outline, no fill,
        /// never selected.
        case placeholder
    }

    var style = Style.plain {
        didSet {
            if style != oldValue {
                needsDisplay = true
            }
        }
    }

    init() {
        super.init(frame: .zero)
        identifier = BoardCardRowView.reuseIdentifier
        selectionHighlightStyle = .regular
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var interiorBackgroundStyle: NSView.BackgroundStyle { .normal }

    private var cardRect: NSRect {
        bounds.insetBy(dx: BoardMetrics.cardInsetH, dy: BoardMetrics.cardInsetV)
    }

    private func path(_ rect: NSRect, inset: CGFloat) -> NSBezierPath {
        let r = rect.insetBy(dx: inset, dy: inset)
        let radius = max(BoardMetrics.cardRadius - inset, 0)
        return NSBezierPath(roundedRect: r, xRadius: radius, yRadius: radius)
    }

    override func drawBackground(in dirtyRect: NSRect) {
        let rect = cardRect
        switch style {
        case .placeholder:
            let p = path(rect, inset: 0.5)
            p.lineWidth = BoardMetrics.cardBorderWidth
            p.setLineDash([4, 3], count: 2, phase: 0)
            BoardPalette.cardBorder(in: self).setStroke()
            p.stroke()
        case .plain, .hot, .assistant:
            let fill = path(rect, inset: 0)
            BoardPalette.cardFill.setFill()
            fill.fill()
            if style == .hot {
                BoardPalette.hotCardFill.setFill()
                fill.fill()
            } else if style == .assistant {
                BoardPalette.assistantFill.setFill()
                fill.fill()
            }
            let border = path(rect, inset: 0.5)
            border.lineWidth = BoardMetrics.cardBorderWidth
            switch style {
            case .hot: BoardPalette.hotCardBorder.setStroke()
            case .assistant: BoardPalette.assistantBorder.setStroke()
            default: BoardPalette.cardBorder(in: self).setStroke()
            }
            border.stroke()
        }
    }

    override func drawSelection(in dirtyRect: NSRect) {
        guard style != .placeholder else { return }
        let w = BoardMetrics.selectedBorderWidth
        let p = path(cardRect, inset: w / 2)
        p.lineWidth = w
        BoardPalette.selectedBorder(emphasized: isEmphasized).setStroke()
        p.stroke()
    }

    override func drawSeparator(in dirtyRect: NSRect) {
        // Cards have no hairline.
    }

    override func prepareForReuse() {
        super.prepareForReuse()
        style = .plain
    }
}

/// The commitment of the user's: a circle, what was promised, the sentence
/// it comes from in italics, the due chip and the case it belongs to.
/// `card` is the commitment in a column (inside a `BoardCardRowView` of
/// the `.assistant` style).
@MainActor
final class BoardCommitmentContentView: NSTableCellView {
    static let reuseIdentifier = NSUserInterfaceItemIdentifier("BoardCommitmentContent")

    let card: Bool

    private let ring = BoardRingView()
    private let text = NSTextField(labelWithString: "")
    private let quote = NSTextField(labelWithString: "")
    private let source = NSTextField(labelWithString: "")
    /// The assistant's mark in front of `source` when it is the
    /// assistant's title of the case.
    private let sourceMark = BoardAssistantMark.label(font: Typo.caption)
    private let due = BoardTag.due()
    private var emphasized = false

    /// The ring as a checkbox: ticks the promise off (`BoardController
    /// .setCommitmentDone`); nil leaves it a plain circle. VoiceOver offers
    /// the same as the row's action.
    var onTick: (@MainActor () -> Void)? {
        didSet {
            ring.onClick = onTick
            ring.toolTip = onTick == nil ? nil : Self.tickTitle
            setAccessibilityCustomActions(onTick == nil ? nil : [
                NSAccessibilityCustomAction(name: Self.tickTitle) { [weak self] in
                    self?.onTick?()
                    return self?.onTick != nil
                },
            ])
        }
    }

    private static var tickTitle: String { Board.Text.markPromiseDone }

    init(card: Bool = false) {
        self.card = card
        super.init(frame: .zero)
        identifier = BoardCommitmentContentView.reuseIdentifier
        build()
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    private func build() {
        for label in [text, quote, source] {
            label.lineBreakMode = .byTruncatingTail
            label.maximumNumberOfLines = 1
            label.isSelectable = false
            label.setContentHuggingPriority(.defaultLow, for: .horizontal)
            label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        }
        text.font = BoardFonts.title
        quote.font = BoardFonts.quote
        source.font = Typo.caption

        // Both stacks fill: with the default gravity areas a view's
        // trailing edge is only "≤ the stack's", every label hugs and
        // resists at the same priority, and the layout was free to leave
        // the column (and so every line of it) at any narrow width.
        due.setContentHuggingPriority(.required, for: .horizontal)
        due.setContentCompressionResistancePriority(.required, for: .horizontal)
        let first = NSStackView(views: [text, due])
        first.orientation = .horizontal
        first.alignment = .firstBaseline
        first.distribution = .fill
        first.spacing = BoardMetrics.itemGap
        first.setHuggingPriority(.defaultLow, for: .horizontal)
        sourceMark.isHidden = true
        let sourceLine = NSStackView(views: [sourceMark, source])
        sourceLine.orientation = .horizontal
        sourceLine.alignment = .firstBaseline
        sourceLine.distribution = .fill
        sourceLine.spacing = BoardAssistantMark.gap
        sourceLine.setHuggingPriority(.defaultLow, for: .horizontal)
        let column = FillStackView(fillingViews: [first, quote, sourceLine])
        column.spacing = BoardMetrics.lineGap

        ring.setContentHuggingPriority(.required, for: .horizontal)
        let content = NSStackView(views: [ring, column])
        content.orientation = .horizontal
        content.alignment = .top
        content.distribution = .fill
        content.spacing = BoardMetrics.barGap + 1
        content.translatesAutoresizingMaskIntoConstraints = false
        addSubview(content)
        let padV = card ? BoardMetrics.cardPaddingV + BoardMetrics.cardInsetV : BoardMetrics.listPaddingV
        let padH = card ? BoardMetrics.cardPaddingH + BoardMetrics.cardInsetH : BoardMetrics.listPaddingH
        NSLayoutConstraint.activate([
            content.leadingAnchor.constraint(equalTo: leadingAnchor, constant: padH),
            content.topAnchor.constraint(equalTo: topAnchor, constant: padV),
            trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: padH),
        ])

        setAccessibilityElement(true)
        for v in [ring, text, quote, source, sourceMark, sourceLine, due, first, column, content] as [NSView] {
            v.setAccessibilityElement(false)
        }
    }

    func configure(_ row: Board.CommitmentRow) {
        text.stringValue = row.text
        quote.stringValue = row.quote.isEmpty ? "" : Board.Text.quoted(row.quote)
        quote.isHidden = row.quote.isEmpty
        source.stringValue = row.from
        sourceMark.isHidden = !row.fromIsAssistant || row.from.isEmpty
        BoardTag.setDue(due, row.due)
        // The case's title is the commitment's source, not part of it.
        var spoken = row.text
        if !row.due.isEmpty {
            spoken += ". " + Board.Text.spokenDue(row.due)
        }
        if !row.from.isEmpty {
            spoken += ". " + row.spokenFrom
        }
        setAccessibilityLabel(spoken)
        applyColours()
    }

    override var backgroundStyle: NSView.BackgroundStyle {
        didSet {
            emphasized = backgroundStyle == .emphasized
            applyColours()
        }
    }

    private func applyColours() {
        let primary: NSColor = emphasized ? .alternateSelectedControlTextColor : .labelColor
        let secondary: NSColor = emphasized ? .alternateSelectedControlTextColor : Tint.secondary
        text.textColor = primary
        quote.textColor = secondary
        source.textColor = secondary
        sourceMark.textColor = emphasized ? .alternateSelectedControlTextColor : BoardPalette.assistant
        ring.color = emphasized ? .alternateSelectedControlTextColor : BoardPalette.assistant
        BoardTag.paint(due, BoardPalette.dueColours, emphasized: emphasized)
    }

    override func prepareForReuse() {
        super.prepareForReuse()
        emphasized = false
        onTick = nil
    }
}

/// The open circle of a commitment, in the assistant's colour; a click on
/// it runs `onClick` (the row's selection stays as it is).
@MainActor
private final class BoardRingView: NSView {
    var color: NSColor = BoardPalette.assistant {
        didSet { needsDisplay = true }
    }

    var onClick: (@MainActor () -> Void)?

    override func mouseDown(with event: NSEvent) {
        guard let onClick else {
            super.mouseDown(with: event)
            return
        }
        onClick()
    }

    override func acceptsFirstMouse(for event: NSEvent?) -> Bool {
        onClick != nil
    }

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            widthAnchor.constraint(equalToConstant: BoardMetrics.ringSize),
            heightAnchor.constraint(equalToConstant: BoardMetrics.ringSize),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func draw(_ dirtyRect: NSRect) {
        let w = BoardMetrics.ringWidth
        let p = NSBezierPath(ovalIn: bounds.insetBy(dx: w / 2, dy: w / 2))
        p.lineWidth = w
        color.setStroke()
        p.stroke()
    }
}

/// The header of a list section, a column or a group of the Today page:
/// its title in the colour of the state and the count beside it. A plain
/// view, not selectable; `BoardMetrics.sectionHeader` is its row height.
@MainActor
final class BoardSectionHeaderView: NSView {
    static let reuseIdentifier = NSUserInterfaceItemIdentifier("BoardSectionHeader")

    private let title = NSTextField(labelWithString: "")
    private let count = NSTextField(labelWithString: "")

    init(horizontalPadding: CGFloat = BoardMetrics.listPaddingH) {
        super.init(frame: .zero)
        identifier = BoardSectionHeaderView.reuseIdentifier
        title.font = BoardFonts.sectionTitle
        title.lineBreakMode = .byTruncatingTail
        title.maximumNumberOfLines = 1
        title.isSelectable = false
        title.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        count.font = Typo.body
        count.textColor = Tint.secondary
        count.maximumNumberOfLines = 1
        count.isSelectable = false
        count.setContentHuggingPriority(.required, for: .horizontal)
        count.setContentCompressionResistancePriority(.required, for: .horizontal)
        let stack = NSStackView(views: [title, count])
        stack.orientation = .horizontal
        stack.alignment = .firstBaseline
        stack.spacing = BoardMetrics.itemGap
        stack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(stack)
        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo: leadingAnchor, constant: horizontalPadding),
            trailingAnchor.constraint(greaterThanOrEqualTo: stack.trailingAnchor, constant: horizontalPadding),
            stack.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -6),
        ])
        setAccessibilityElement(true)
        setAccessibilityRole(.staticText)
        title.setAccessibilityElement(false)
        count.setAccessibilityElement(false)
        stack.setAccessibilityElement(false)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// `count` < 0 shows no count. `spokenCount` is what VoiceOver says
    /// for the count; nil says it in cases ("3 cases").
    func configure(title text: String, count n: Int, color: NSColor, spokenCount: String? = nil) {
        title.stringValue = text
        title.textColor = color
        count.stringValue = n >= 0 ? String(n) : ""
        count.isHidden = n < 0
        setAccessibilityLabel(n >= 0 ? "\(text), \(spokenCount ?? Board.Text.caseCount(n))" : text)
    }

    /// "From the Assistant" over `n` commitments: its count is spoken in
    /// promises, not in cases.
    func configureCommitments(count n: Int) {
        configure(title: Board.Text.fromAssistant, count: n, color: BoardPalette.assistant, spokenCount: Self.promiseCount(n))
    }

    /// "1 promise", "3 promises".
    private static func promiseCount(_ n: Int) -> String {
        Board.Text.promiseCount(n)
    }

    func configure(_ column: Board.Column) {
        configure(title: column.title, count: column.rows.count, color: BoardPalette.accent(column.state))
    }
}

/// The assistant's mark (`Board.Text.assistantMark`, the ✦ of the summary's
/// heading) in front of text the assistant wrote, in the assistant's
/// colour; VoiceOver hears "Assistant:" from the text's own label instead.
@MainActor
enum BoardAssistantMark {
    /// The space between the mark and its text.
    static let gap: CGFloat = 4

    static func label(font: NSFont) -> NSTextField {
        let l = NSTextField(labelWithString: Board.Text.assistantMark)
        l.font = font
        l.textColor = BoardPalette.assistant
        l.isSelectable = false
        l.setContentHuggingPriority(.required, for: .horizontal)
        l.setContentCompressionResistancePriority(.required, for: .horizontal)
        l.setAccessibilityElement(false)
        return l
    }
}
