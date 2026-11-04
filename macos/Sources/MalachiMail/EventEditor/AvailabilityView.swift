// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The editor's availability rows (docs/calendar.md §5.6, Go
/// availability.go via `EventEditorController.availabilityRows`): per
/// person a name and a bar over the working day 8:00–18:00 with the busy
/// spans grey and the proposed time green; a person without free/busy reads
/// "availability unknown". The hours along the top are the axis. Names are
/// plain text (`Cal.cleanLine` in Core), drawn as strings, never markup.
@MainActor
final class AvailabilityView: NSView {
    static let axisStartHour = 8
    static let axisEndHour = 18
    static let rowHeight: CGFloat = 20
    static let axisHeight: CGFloat = 16
    static let nameWidth: CGFloat = 120

    struct Content {
        var rows: [Cal.AvailabilityRow] = []
        /// The day the rows' fractions span (`availabilityDay`).
        var from = Date()
        var to = Date()
        /// The proposed time (the event's start and end).
        var start = Date()
        var end = Date()
        var env = Cal.Env(zone: .current, locale: .current)
    }

    var content = Content() {
        didSet {
            invalidateIntrinsicContentSize()
            needsDisplay = true
            updateAccessibility()
        }
    }

    override var isFlipped: Bool { true }

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        setContentHuggingPriority(.defaultLow, for: .horizontal)
        setAccessibilityElement(true)
        setAccessibilityRole(.group)
        setAccessibilityLabel(Cal.fieldLabel(.availability))
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var intrinsicContentSize: NSSize {
        NSSize(width: NSView.noIntrinsicMetric, height: Self.axisHeight + CGFloat(content.rows.count) * Self.rowHeight)
    }

    /// The axis's instants: 8:00 and 18:00 of the day, in its zone.
    private var axis: (from: Date, to: Date) {
        let p = Cal.parts(content.from, content.env.zone)
        return (
            Cal.date(p.year, p.month, p.day, Self.axisStartHour, 0, in: content.env.zone),
            Cal.date(p.year, p.month, p.day, Self.axisEndHour, 0, in: content.env.zone)
        )
    }

    /// An instant as an x of the bar area (clamped).
    private func x(_ t: Date, in bar: NSRect) -> CGFloat {
        let a = axis
        let total = a.to.timeIntervalSince(a.from)
        guard total > 0 else { return bar.minX }
        let f = min(max(t.timeIntervalSince(a.from) / total, 0), 1)
        return bar.minX + CGFloat(f) * bar.width
    }

    /// A fraction of the whole day as an instant.
    private func instant(_ f: Double) -> Date {
        content.from.addingTimeInterval(f * content.to.timeIntervalSince(content.from))
    }

    override func draw(_ dirtyRect: NSRect) {
        let bar = NSRect(x: Self.nameWidth, y: 0, width: max(0, bounds.width - Self.nameWidth), height: bounds.height)
        let small = NSFont.systemFont(ofSize: NSFont.smallSystemFontSize)
        let tickAttrs: [NSAttributedString.Key: Any] = [.font: small, .foregroundColor: NSColor.tertiaryLabelColor]
        let nameAttrs: [NSAttributedString.Key: Any] = [.font: small, .foregroundColor: NSColor.labelColor]
        let unknownAttrs: [NSAttributedString.Key: Any] = [.font: small, .foregroundColor: NSColor.secondaryLabelColor]

        // The hours along the top, every two hours, and their hairlines.
        let p = Cal.parts(content.from, content.env.zone)
        for h in stride(from: Self.axisStartHour, through: Self.axisEndHour, by: 1) {
            let t = Cal.date(p.year, p.month, p.day, h, 0, in: content.env.zone)
            let tx = x(t, in: bar)
            NSColor.separatorColor.setFill()
            NSRect(x: tx, y: Self.axisHeight, width: 1 / max(1, window?.backingScaleFactor ?? 2), height: bounds.height - Self.axisHeight).fill()
            if (h - Self.axisStartHour) % 2 == 0 {
                let s = Cal.Text.time(t, content.env) as NSString
                let w = s.size(withAttributes: tickAttrs).width
                let lx = min(max(tx - w / 2, bar.minX), bar.maxX - w)
                s.draw(at: NSPoint(x: lx, y: 1), withAttributes: tickAttrs)
            }
        }

        // The proposed time, behind the rows.
        let sx = x(content.start, in: bar)
        let ex = x(content.end, in: bar)
        if ex > sx {
            NSColor.systemGreen.withAlphaComponent(0.22).setFill()
            NSRect(x: sx, y: Self.axisHeight, width: ex - sx, height: bounds.height - Self.axisHeight).fill()
            NSColor.systemGreen.setFill()
            NSRect(x: sx, y: Self.axisHeight, width: 1.5, height: bounds.height - Self.axisHeight).fill()
            NSRect(x: ex - 1.5, y: Self.axisHeight, width: 1.5, height: bounds.height - Self.axisHeight).fill()
        }

        for (i, row) in content.rows.enumerated() {
            let y = Self.axisHeight + CGFloat(i) * Self.rowHeight
            let nameRect = NSRect(x: 0, y: y + 3, width: Self.nameWidth - 8, height: Self.rowHeight - 4)
            let style = NSMutableParagraphStyle()
            style.lineBreakMode = .byTruncatingTail
            var attrs = nameAttrs
            attrs[.paragraphStyle] = style
            (row.label as NSString).draw(in: nameRect, withAttributes: attrs)
            let lane = NSRect(x: bar.minX, y: y + 4, width: bar.width, height: Self.rowHeight - 8)
            NSColor.quaternaryLabelColor.withAlphaComponent(0.25).setFill()
            NSBezierPath(roundedRect: lane, xRadius: 3, yRadius: 3).fill()
            guard row.known else {
                (Cal.unknown as NSString).draw(at: NSPoint(x: lane.minX + 6, y: y + 3), withAttributes: unknownAttrs)
                continue
            }
            for span in row.busy {
                let bx = x(instant(span.from), in: bar)
                let bw = x(instant(span.to), in: bar) - bx
                guard bw > 0 else { continue }
                let color: NSColor = span.showAs == .tentative ? .tertiaryLabelColor : .secondaryLabelColor
                color.setFill()
                NSBezierPath(roundedRect: NSRect(x: bx, y: lane.minY, width: bw, height: lane.height), xRadius: 2, yRadius: 2).fill()
            }
        }
    }

    private func updateAccessibility() {
        let lines = content.rows.map { r in
            r.known ? r.label : Cal.Text.dot(r.label, Cal.unknown)
        }
        setAccessibilityValue(lines.joined(separator: "\n"))
    }
}
