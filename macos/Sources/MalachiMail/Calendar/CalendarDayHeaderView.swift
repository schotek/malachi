// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The day header row of Week and Day (docs/calendar.md §5.3), 40 pt: per
/// column the weekday's abbreviation and the day's number; today's number
/// in a filled accent circle, weekends grey. The hour axis's width stays
/// empty at the left so the columns line up with the grid below.
@MainActor
final class CalendarDayHeaderView: NSView {
    struct Day: Equatable {
        var weekday: String
        var number: String
        var today: Bool
        var weekend: Bool
    }

    private(set) var days: [Day] = []
    private var labels: [(NSTextField, NSTextField)] = []
    /// The width the grid's vertical scroller takes at the right, so that
    /// the columns stay over the grid's.
    var trailingInset: CGFloat = 0 {
        didSet { needsLayout = true }
    }

    override var isFlipped: Bool { true }

    override var intrinsicContentSize: NSSize {
        NSSize(width: NSView.noIntrinsicMetric, height: CalendarMetrics.dayHeaderHeight)
    }

    func set(days new: [Day]) {
        guard new != days else { return }
        days = new
        labels.forEach { $0.0.removeFromSuperview(); $0.1.removeFromSuperview() }
        labels = new.map { d in
            let w = calendarLabel(CalendarMetrics.smallFont, color: d.weekend ? .tertiaryLabelColor : .secondaryLabelColor)
            w.stringValue = d.weekday
            w.alignment = .center
            let n = calendarLabel(
                .systemFont(ofSize: Typo.bodySize + 2, weight: d.today ? .bold : .regular),
                color: d.today ? .white : (d.weekend ? .tertiaryLabelColor : .labelColor))
            n.stringValue = d.number
            n.alignment = .center
            addSubview(w)
            addSubview(n)
            return (w, n)
        }
        setAccessibilityElement(false)
        needsLayout = true
        needsDisplay = true
    }

    private func columnRect(_ i: Int) -> NSRect {
        let n = max(1, days.count)
        let w = (bounds.width - CalendarMetrics.axisWidth - trailingInset) / CGFloat(n)
        return NSRect(x: CalendarMetrics.axisWidth + CGFloat(i) * w, y: 0, width: w, height: bounds.height)
    }

    /// The circle behind today's number.
    private func circleRect(_ i: Int) -> NSRect {
        let c = columnRect(i)
        let d: CGFloat = 24
        return NSRect(x: c.midX - d / 2, y: 15, width: d, height: d)
    }

    override func layout() {
        super.layout()
        for (i, pair) in labels.enumerated() {
            let c = columnRect(i)
            pair.0.frame = NSRect(x: c.minX, y: 2, width: c.width, height: 14)
            let circle = circleRect(i)
            let h = ceil(pair.1.intrinsicContentSize.height)
            pair.1.frame = NSRect(x: c.minX, y: circle.midY - h / 2, width: c.width, height: h)
        }
    }

    override func draw(_ dirtyRect: NSRect) {
        for (i, d) in days.enumerated() where d.today {
            Tint.accent.setFill()
            NSBezierPath(ovalIn: circleRect(i)).fill()
        }
        NSColor.separatorColor.setFill()
        NSRect(x: 0, y: bounds.height - 1, width: bounds.width, height: 1).fill()
    }
}
