// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The sidebar's month (docs/calendar.md §5.3): the month's title with ‹ ›,
/// the weekday headers and the 6 × 7 days of `Cal.MiniMonth`; today's
/// number in a filled accent circle, the shown range banded, days of the
/// other months grey. A click on a day goes to it; the arrows change the
/// month alone.
@MainActor
final class MiniMonthView: NSView {
    static let titleHeight: CGFloat = 28
    static let headerHeight: CGFloat = 18
    static let rowHeight: CGFloat = 24
    static let height = titleHeight + headerHeight + 6 * rowHeight + 6

    var onPick: ((Date) -> Void)?
    var onStep: ((Int) -> Void)?

    private(set) var month: Cal.MiniMonth?
    private let title = calendarLabel(.systemFont(ofSize: Typo.bodySize, weight: .semibold))
    private let previous = NSButton()
    private let next = NSButton()
    private var headers: [NSTextField] = []
    private var numbers: [NSTextField] = []

    override init(frame: NSRect) {
        super.init(frame: frame)
        addSubview(title)
        for (b, symbol, text, dir) in [
            (previous, "chevron.left", Cal.Text.previous, -1), (next, "chevron.right", Cal.Text.next, 1),
        ] {
            b.bezelStyle = .accessoryBarAction
            b.isBordered = false
            b.image = NSImage(systemSymbolName: symbol, accessibilityDescription: text)
            b.imagePosition = .imageOnly
            b.toolTip = text
            b.tag = dir
            b.target = self
            b.action = #selector(step(_:))
            addSubview(b)
        }
        for _ in 0..<7 {
            let l = calendarLabel(CalendarMetrics.smallFont, color: .secondaryLabelColor)
            l.alignment = .center
            headers.append(l)
            addSubview(l)
        }
        for _ in 0..<42 {
            let l = calendarLabel(.monospacedDigitSystemFont(ofSize: Typo.size == .larger ? 12 : 11, weight: .regular))
            l.alignment = .center
            numbers.append(l)
            addSubview(l)
        }
        setAccessibilityElement(false)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var isFlipped: Bool { true }

    override var intrinsicContentSize: NSSize {
        NSSize(width: NSView.noIntrinsicMetric, height: Self.height)
    }

    func set(_ m: Cal.MiniMonth) {
        guard m != month else { return }
        month = m
        title.stringValue = m.title
        for (i, h) in m.headers.prefix(7).enumerated() {
            headers[i].stringValue = h
        }
        for (i, d) in m.days.prefix(42).enumerated() {
            let l = numbers[i]
            l.stringValue = d.number
            l.textColor = d.today ? .white : (d.inMonth ? (d.weekend ? .secondaryLabelColor : .labelColor) : .tertiaryLabelColor)
            l.font = .monospacedDigitSystemFont(ofSize: Typo.size == .larger ? 12 : 11, weight: d.today ? .bold : .regular)
        }
        needsDisplay = true
    }

    private var cellWidth: CGFloat { bounds.width / 7 }

    private func cellRect(_ i: Int) -> NSRect {
        let row = i / 7
        let col = i % 7
        return NSRect(
            x: CGFloat(col) * cellWidth, y: Self.titleHeight + Self.headerHeight + CGFloat(row) * Self.rowHeight,
            width: cellWidth, height: Self.rowHeight)
    }

    override func layout() {
        super.layout()
        let b: CGFloat = 22
        next.frame = NSRect(x: bounds.width - b, y: 3, width: b, height: b)
        previous.frame = NSRect(x: bounds.width - 2 * b - 2, y: 3, width: b, height: b)
        title.frame = NSRect(x: 4, y: 6, width: max(0, previous.frame.minX - 8), height: 18)
        for (i, h) in headers.enumerated() {
            h.frame = NSRect(x: CGFloat(i) * cellWidth, y: Self.titleHeight, width: cellWidth, height: 14)
        }
        for (i, l) in numbers.enumerated() {
            let r = cellRect(i)
            let h = ceil(l.intrinsicContentSize.height)
            l.frame = NSRect(x: r.minX, y: r.midY - h / 2, width: r.width, height: h)
        }
    }

    override func draw(_ dirtyRect: NSRect) {
        guard let m = month else { return }
        for (i, d) in m.days.prefix(42).enumerated() {
            let r = cellRect(i)
            if d.shown {
                // The band: rounded at its row's first and last shown day.
                var band = NSRect(x: r.minX, y: r.minY + 2, width: r.width, height: r.height - 4)
                if d.bandStart { band.origin.x += 2; band.size.width -= 2 }
                if d.bandEnd { band.size.width -= 2 }
                Tint.accent.withAlphaComponent(0.15).setFill()
                let radius = band.height / 2
                let path = NSBezierPath()
                path.appendRoundedRect(band, xRadius: d.bandStart || d.bandEnd ? radius : 0, yRadius: d.bandStart || d.bandEnd ? radius : 0)
                if d.bandStart != d.bandEnd {
                    // Square off the inner side of a band's end.
                    let inner = d.bandStart
                        ? NSRect(x: band.midX, y: band.minY, width: band.width / 2, height: band.height)
                        : NSRect(x: band.minX, y: band.minY, width: band.width / 2, height: band.height)
                    path.appendRect(inner)
                }
                path.fill()
            }
            if d.today {
                Tint.accent.setFill()
                let s = min(r.width, r.height) - 3
                NSBezierPath(ovalIn: NSRect(x: r.midX - s / 2, y: r.midY - s / 2, width: s, height: s)).fill()
            }
        }
    }

    override func mouseDown(with event: NSEvent) {
        guard let m = month else { return }
        let p = convert(event.locationInWindow, from: nil)
        let top = Self.titleHeight + Self.headerHeight
        guard p.y >= top, cellWidth > 0 else { return }
        let row = Int((p.y - top) / Self.rowHeight)
        let col = Int(p.x / cellWidth)
        let i = row * 7 + col
        guard row < 6, col >= 0, col < 7, i < m.days.count else { return }
        onPick?(m.days[i].date)
    }

    @objc private func step(_ sender: NSButton) {
        onStep?(sender.tag)
    }
}
