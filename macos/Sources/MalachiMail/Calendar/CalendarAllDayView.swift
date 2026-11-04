// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The all-day row of Week and Day (docs/calendar.md §5.3): the all-day
/// occurrences as bars spanning their days, in the lanes Core gives
/// (`Cal.allDayLanes`); at least 36 pt, growing a lane at a time up to
/// `CalendarMetrics.allDayMaxLanes`. A double click on its empty part asks
/// for an all-day event that day.
@MainActor
final class CalendarAllDayView: NSView {
    /// The days of the range (midnights), as the grid's columns.
    private(set) var days: [Date] = []
    private(set) var lanes = 0
    private(set) var bars: [AllDayBarView] = []
    /// "All Day" in the axis: up to two lines, smaller when a word is
    /// still too long, never cut ("Celý den").
    private let caption = NSTextField(wrappingLabelWithString: "")
    var trailingInset: CGFloat = 0 {
        didSet { needsLayout = true }
    }

    /// A click on a bar.
    var onClick: ((AllDayBarView) -> Void)?
    /// A double click on the empty part of day `i`.
    var onDoubleClickDay: ((Int) -> Void)?

    override var isFlipped: Bool { true }

    override init(frame: NSRect) {
        super.init(frame: frame)
        caption.stringValue = Cal.Text.allDay
        caption.font = .systemFont(ofSize: 10)
        caption.textColor = .secondaryLabelColor
        caption.alignment = .right
        caption.maximumNumberOfLines = 2
        caption.lineBreakMode = .byWordWrapping
        caption.allowsDefaultTighteningForTruncation = true
        addSubview(caption)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// The row's height for `lanes` lanes.
    static func height(lanes: Int) -> CGFloat {
        let shown = CGFloat(min(lanes, CalendarMetrics.allDayMaxLanes))
        return max(CalendarMetrics.allDayMinimum, 8 + shown * (CalendarMetrics.laneHeight + CalendarMetrics.laneGap))
    }

    func set(days: [Date], bars new: [Cal.Bar], lanes: Int, color: (Occurrence) -> NSColor, label: (Occurrence) -> String,
             selection: String?) {
        self.days = days
        self.lanes = lanes
        bars.forEach { $0.removeFromSuperview() }
        bars = new.map { b in
            let v = AllDayBarView(bar: b, color: color(b.occurrence), label: label(b.occurrence))
            v.selected = v.key == selection
            v.onClick = { [weak self] v in self?.onClick?(v) }
            addSubview(v)
            return v
        }
        needsLayout = true
        needsDisplay = true
    }

    func setSelection(_ key: String?) {
        for b in bars {
            b.selected = b.key == key
        }
    }

    private var columnWidth: CGFloat {
        (bounds.width - CalendarMetrics.axisWidth - trailingInset) / CGFloat(max(1, days.count))
    }

    override func layout() {
        super.layout()
        let width = CalendarMetrics.axisWidth - 8
        // Shrinks the font until the longest word fits the axis's width.
        var size: CGFloat = 10
        while size > 7, Cal.Text.allDay.split(separator: " ").contains(where: {
            (String($0) as NSString).size(withAttributes: [.font: NSFont.systemFont(ofSize: size)]).width > width - 4
        }) {
            size -= 0.5
        }
        caption.font = .systemFont(ofSize: size)
        caption.preferredMaxLayoutWidth = width
        caption.frame = NSRect(x: 2, y: 4, width: width, height: 28)
        let w = columnWidth
        for b in bars {
            let lane = b.bar.lane
            let x = CalendarMetrics.axisWidth + CGFloat(b.bar.first) * w + 2
            let width = CGFloat(b.bar.last - b.bar.first + 1) * w - 4
            let y = 4 + CGFloat(lane) * (CalendarMetrics.laneHeight + CalendarMetrics.laneGap)
            b.frame = NSRect(x: x, y: y, width: max(0, width), height: CalendarMetrics.laneHeight)
            b.isHidden = lane >= CalendarMetrics.allDayMaxLanes
        }
    }

    override func draw(_ dirtyRect: NSRect) {
        NSColor.separatorColor.setFill()
        NSRect(x: 0, y: bounds.height - 1, width: bounds.width, height: 1).fill()
        let w = columnWidth
        for i in 0...days.count {
            NSRect(x: CalendarMetrics.axisWidth + CGFloat(i) * w, y: 0, width: 1, height: bounds.height).fill()
        }
    }

    override func mouseDown(with event: NSEvent) {
        guard event.clickCount == 2 else { return }
        let p = convert(event.locationInWindow, from: nil)
        guard p.x >= CalendarMetrics.axisWidth else { return }
        let i = Int((p.x - CalendarMetrics.axisWidth) / columnWidth)
        if i >= 0, i < days.count {
            onDoubleClickDay?(i)
        }
    }
}

/// One all-day occurrence in the all-day row: a filled bar in its
/// calendar's colour tint with the title, drawn by its state.
@MainActor
final class AllDayBarView: NSView {
    let bar: Cal.Bar
    let key: String
    var onClick: ((AllDayBarView) -> Void)?
    var selected = false {
        didSet { needsDisplay = true }
    }

    private let color: NSColor
    private let title = calendarLabel(CalendarMetrics.titleFont)

    init(bar: Cal.Bar, color: NSColor, label: String) {
        self.bar = bar
        self.color = color
        key = Cal.occurrenceKey(bar.occurrence)
        super.init(frame: .zero)
        title.attributedStringValue = BlockPainter.title(
            CalendarText.title(bar.occurrence.title), font: CalendarMetrics.titleFont, color: .labelColor, state: bar.state)
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
        let h = ceil(title.intrinsicContentSize.height)
        title.frame = NSRect(x: 6, y: (bounds.height - h) / 2, width: max(0, bounds.width - 10), height: h)
    }

    override func draw(_ dirtyRect: NSRect) {
        BlockPainter.paint(bounds.insetBy(dx: 0.5, dy: 0.5), radius: 4, color: color, state: bar.state, selected: selected)
    }

    override func mouseDown(with event: NSEvent) {
        onClick?(self)
    }

    override func accessibilityPerformPress() -> Bool {
        onClick?(self)
        return true
    }
}
