// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// One day of the Month view (docs/calendar.md §5.4): the day's number
/// (today's in a filled accent circle, other months grey, weekends
/// shaded), the pills Core chose for the cell's capacity
/// (`Cal.cellItems`) and "+N more", a button with the day's whole list in
/// a popover. A double click on its empty part asks for New Event that day.
@MainActor
final class MonthCellView: NSView {
    let cell: Cal.Cell
    var onOpen: ((Occurrence, NSView) -> Void)?
    var onNewEvent: ((Date) -> Void)?

    private let number = calendarLabel(.monospacedDigitSystemFont(ofSize: Typo.bodySize - 1, weight: .regular))
    private(set) var pills: [MonthPillView] = []
    private var moreButton: NSButton?
    /// Everything of the day, for the "+N more" popover.
    private var all: [Cal.Pill] = []
    private var env: Cal.Env
    private var color: (Occurrence) -> NSColor = { _ in Tint.accent }
    private var label: (Occurrence) -> String = { _ in "" }
    private var popover: NSPopover?
    /// Where the pills start: under the day number and the row's lanes of
    /// spanning bars (`CalendarMonthView`).
    var pillTop = CalendarMetrics.cellNumberLine {
        didSet { needsLayout = true }
    }

    init(cell: Cal.Cell, env: Cal.Env) {
        self.cell = cell
        self.env = env
        super.init(frame: .zero)
        number.stringValue = Cal.Text.dayOfMonth(cell.day, env)
        number.alignment = .center
        number.textColor = cell.today ? .white : (cell.inMonth ? .labelColor : .tertiaryLabelColor)
        if cell.today {
            number.font = .monospacedDigitSystemFont(ofSize: Typo.bodySize - 1, weight: .bold)
        }
        addSubview(number)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var isFlipped: Bool { true }

    /// Fills the cell: `shown` pills and `more` behind the "+N more"
    /// button (both from `Cal.cellItems`), `all` for its popover.
    func set(
        shown: [Cal.Pill], more: Int, all: [Cal.Pill], color: @escaping (Occurrence) -> NSColor,
        label: @escaping (Occurrence) -> String, selection: String?
    ) {
        self.all = all
        self.color = color
        self.label = label
        pills.forEach { $0.removeFromSuperview() }
        pills = shown.map { p in
            let v = MonthPillView(pill: p, color: color(p.occurrence), label: label(p.occurrence), env: env)
            v.selected = v.key == selection
            v.onClick = { [weak self] v in self?.onOpen?(v.pill.occurrence, v) }
            addSubview(v)
            return v
        }
        moreButton?.removeFromSuperview()
        moreButton = nil
        if more > 0 {
            let b = NSButton(title: Cal.more(more), target: self, action: #selector(showMore(_:)))
            b.isBordered = false
            b.font = CalendarMetrics.smallFont
            b.contentTintColor = .secondaryLabelColor
            b.alignment = .left
            addSubview(b)
            moreButton = b
        }
        needsLayout = true
    }

    func setSelection(_ key: String?) {
        for p in pills {
            p.selected = p.key == key
        }
    }

    override func layout() {
        super.layout()
        // Room for two digits and the label's own insets; the circle of
        // today is drawn around the number's centre.
        let w = ceil(number.intrinsicContentSize.width) + 6
        let h = ceil(number.intrinsicContentSize.height)
        let centreX = bounds.width - 4 - 10
        number.frame = NSRect(x: centreX - w / 2, y: 2 + (20 - h) / 2, width: w, height: h)
        var y = pillTop
        for p in pills {
            p.frame = NSRect(x: 3, y: y, width: max(0, bounds.width - 6), height: CalendarMetrics.pillLine - 1)
            y += CalendarMetrics.pillLine
        }
        moreButton?.frame = NSRect(x: 4, y: y, width: max(0, bounds.width - 8), height: CalendarMetrics.pillLine - 1)
    }

    override func draw(_ dirtyRect: NSRect) {
        if cell.weekend || !cell.inMonth {
            Tint.fg(alpha: cell.inMonth ? 0.03 : 0.05).setFill()
            bounds.fill()
        }
        if cell.today {
            Tint.accent.setFill()
            let r = number.frame
            let s: CGFloat = 20
            NSBezierPath(ovalIn: NSRect(x: r.midX - s / 2, y: r.midY - s / 2, width: s, height: s)).fill()
        }
        NSColor.separatorColor.setFill()
        NSRect(x: 0, y: bounds.height - 1, width: bounds.width, height: 1).fill()
        NSRect(x: bounds.width - 1, y: 0, width: 1, height: bounds.height).fill()
    }

    override func mouseDown(with event: NSEvent) {
        guard event.clickCount == 2 else { return }
        onNewEvent?(cell.day)
    }

    @objc private func showMore(_ sender: NSButton) {
        let list = FillStackView()
        list.spacing = 1
        list.edgeInsets = NSEdgeInsets(top: 8, left: 8, bottom: 8, right: 8)
        let heading = calendarLabel(.systemFont(ofSize: Typo.bodySize, weight: .semibold))
        heading.stringValue = Cal.Text.weekdayDate(cell.day, env)
        list.addArrangedSubview(heading)
        let pop = NSPopover()
        for p in all {
            let v = MonthPillView(pill: p, color: color(p.occurrence), label: label(p.occurrence), env: env)
            v.onClick = { [weak self, weak pop] v in
                pop?.performClose(nil)
                guard let self else { return }
                self.onOpen?(v.pill.occurrence, self)
            }
            v.heightAnchor.constraint(equalToConstant: CalendarMetrics.pillLine).isActive = true
            list.addArrangedSubview(v)
        }
        let vc = NSViewController()
        vc.view = list
        list.widthAnchor.constraint(equalToConstant: 240).isActive = true
        pop.contentViewController = vc
        pop.behavior = .transient
        popover = pop
        pop.show(relativeTo: sender.bounds, of: sender, preferredEdge: .maxY)
    }
}

/// A pill of the Month view: the colour dot, the time and the title; an
/// all-day pill is filled with its calendar's colour tint instead of the
/// dot; unanswered dashed, tentative striped, declined struck through.
@MainActor
final class MonthPillView: NSView {
    let pill: Cal.Pill
    let key: String
    var onClick: ((MonthPillView) -> Void)?
    var selected = false {
        didSet { needsDisplay = true }
    }

    private let color: NSColor
    private let text = calendarLabel(CalendarMetrics.smallFont)
    private let time = calendarLabel(
        .monospacedDigitSystemFont(ofSize: CalendarMetrics.smallFont.pointSize, weight: .regular), color: .secondaryLabelColor)

    init(pill: Cal.Pill, color: NSColor, label: String, env: Cal.Env) {
        self.pill = pill
        self.color = color
        key = Cal.occurrenceKey(pill.occurrence)
        super.init(frame: .zero)
        let o = pill.occurrence
        text.attributedStringValue = BlockPainter.title(
            CalendarText.title(o.title), font: CalendarMetrics.smallFont, color: .labelColor, state: pill.state)
        // The time and the title side by side, never one sentence.
        time.stringValue = o.allDay || pill.continuesBefore ? "" : Cal.Text.time(pill.start, env)
        time.isHidden = time.stringValue.isEmpty
        time.setContentCompressionResistancePriority(.required, for: .horizontal)
        addSubview(time)
        addSubview(text)
        setAccessibilityElement(true)
        setAccessibilityRole(.button)
        setAccessibilityLabel(label)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var isFlipped: Bool { true }

    private var filled: Bool { pill.occurrence.allDay || pill.state == .unanswered || selected }

    override func layout() {
        super.layout()
        var x: CGFloat = pill.occurrence.allDay ? 5 : 12
        let h = ceil(text.intrinsicContentSize.height)
        if !time.isHidden {
            // At least the width of "00:00" in the tabular digits, and the
            // label's insets, so that no time is cut.
            let font = time.font ?? CalendarMetrics.smallFont
            let digits = ("00:00" as NSString).size(withAttributes: [.font: font]).width
            let w = ceil(max(time.intrinsicContentSize.width, digits + 4))
            time.frame = NSRect(x: x, y: (bounds.height - h) / 2, width: w, height: h)
            x += w + 3
        }
        text.frame = NSRect(x: x, y: (bounds.height - h) / 2, width: max(0, bounds.width - x - 2), height: h)
    }

    override func draw(_ dirtyRect: NSRect) {
        let r = bounds.insetBy(dx: 0.5, dy: 0.5)
        if filled || pill.state == .tentative {
            BlockPainter.paint(r, radius: 3, color: color, state: pill.state, selected: selected, filled: filled || pill.state == .tentative)
        }
        if !pill.occurrence.allDay {
            color.setFill()
            NSBezierPath(ovalIn: NSRect(x: 3, y: bounds.midY - 3, width: 6, height: 6)).fill()
        }
    }

    override func mouseDown(with event: NSEvent) {
        onClick?(self)
    }

    override func accessibilityPerformPress() -> Bool {
        onClick?(self)
        return true
    }
}

/// An all-day or multi-day occurrence as one bar across the days it
/// covers in a week row of the Month view (`Cal.monthLanes`): the opaque
/// tint of its calendar with the colour bar at the left, rounded only at
/// the ends where it does not continue, the title once, clipped.
@MainActor
final class MonthSpanBarView: NSView {
    let occurrence: Occurrence
    let key: String
    let lane: Int
    let startCol: Int
    let endCol: Int
    /// The week row of the month (0–5).
    var row = 0
    var onClick: ((MonthSpanBarView) -> Void)?
    var selected = false {
        didSet { needsDisplay = true }
    }

    private let continuesBefore: Bool
    private let continuesAfter: Bool
    private let state: Cal.BlockState
    private let color: NSColor
    private let title = calendarLabel(CalendarMetrics.smallFont)

    init(
        occurrence: Occurrence, key: String, lane: Int, startCol: Int, endCol: Int, continuesBefore: Bool,
        continuesAfter: Bool, state: Cal.BlockState, color: NSColor, label: String
    ) {
        self.occurrence = occurrence
        self.key = key
        self.lane = lane
        self.startCol = startCol
        self.endCol = endCol
        self.continuesBefore = continuesBefore
        self.continuesAfter = continuesAfter
        self.state = state
        self.color = color
        super.init(frame: .zero)
        title.attributedStringValue = BlockPainter.title(
            CalendarText.title(occurrence.title), font: CalendarMetrics.smallFont, color: .labelColor, state: state)
        addSubview(title)
        setAccessibilityElement(true)
        setAccessibilityRole(.button)
        setAccessibilityLabel(label)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var isFlipped: Bool { true }

    override func layout() {
        super.layout()
        let x: CGFloat = continuesBefore ? 5 : CalendarMetrics.blockBar + 5
        let h = ceil(title.intrinsicContentSize.height)
        title.frame = NSRect(x: x, y: (bounds.height - h) / 2, width: max(0, bounds.width - x - 4), height: h)
    }

    override func draw(_ dirtyRect: NSRect) {
        // The ends that continue run square to the view's edge: drawn
        // wider and clipped.
        var r = bounds.insetBy(dx: 0, dy: 0.5)
        if continuesBefore { r.origin.x -= 6; r.size.width += 6 }
        if continuesAfter { r.size.width += 6 }
        NSGraphicsContext.saveGraphicsState()
        NSBezierPath(rect: bounds).addClip()
        BlockPainter.paint(r, radius: 3, color: color, state: state, selected: selected)
        if !continuesBefore, state != .unanswered {
            color.setFill()
            NSBezierPath(
                roundedRect: NSRect(x: r.minX, y: r.minY, width: CalendarMetrics.blockBar, height: r.height),
                xRadius: 1.5, yRadius: 1.5
            ).fill()
        }
        NSGraphicsContext.restoreGraphicsState()
    }

    override func mouseDown(with event: NSEvent) {
        onClick?(self)
    }

    override func accessibilityPerformPress() -> Bool {
        onClick?(self)
        return true
    }
}
