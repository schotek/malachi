// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The invitation card's mini day strip (docs/calendar.md §5.7,
/// `Cal.dayStripOf`): a horizontal axis over the working hours of the
/// invitation's day with its whole hours labelled underneath, the busy
/// spans of the visible calendars as grey blocks, and the invitation's own
/// slot as a dashed blue block with its time above it. Everything drawn is
/// geometry and the app's own time strings; nothing of the part is text
/// here. VoiceOver reads the card's clock and conflict lines instead.
@MainActor
final class InvitationDayStripView: NSView {
    private var strip: Cal.DayStrip?

    /// The bar's height, the gap for the slot's label above it and the
    /// hours' labels below it.
    private static let barHeight: CGFloat = 14
    private static var labelFont: NSFont { .monospacedDigitSystemFont(ofSize: max(9, Typo.captionSize - 1), weight: .regular) }
    private static var labelHeight: CGFloat { ceil(labelFont.ascender - labelFont.descender) + 1 }

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        setContentHuggingPriority(.required, for: .vertical)
        setAccessibilityElement(false)
        isHidden = true
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var isFlipped: Bool { true }

    override var intrinsicContentSize: NSSize {
        NSSize(width: NSView.noIntrinsicMetric, height: 2 * Self.labelHeight + Self.barHeight + 4)
    }

    /// Shows `strip`, or hides the view for none.
    func show(_ strip: Cal.DayStrip?) {
        self.strip = strip
        isHidden = strip == nil
        invalidateIntrinsicContentSize()
        needsDisplay = true
    }

    override func draw(_ dirtyRect: NSRect) {
        guard let strip else { return }
        let inset: CGFloat = 1
        let width = bounds.width - 2 * inset
        guard width > 20 else { return }
        let barTop = Self.labelHeight + 2
        let bar = NSRect(x: inset, y: barTop, width: width, height: Self.barHeight)
        func x(_ f: Double) -> CGFloat { inset + CGFloat(f) * width }

        // The axis: the day's span in the faintest fill.
        Tint.fg(alpha: 0.06).setFill()
        NSBezierPath(roundedRect: bar, xRadius: 3, yRadius: 3).fill()

        // The hours: a hairline across the bar and the label under it.
        let attrs: [NSAttributedString.Key: Any] = [.font: Self.labelFont, .foregroundColor: NSColor.tertiaryLabelColor]
        var lastEnd: CGFloat = -.greatestFiniteMagnitude
        for t in strip.ticks {
            let px = x(t.at)
            NSColor.separatorColor.setFill()
            NSRect(x: px - 0.5, y: bar.minY, width: 1, height: bar.height).fill()
            let text = NSAttributedString(string: t.label, attributes: attrs)
            let size = text.size()
            var lx = px - size.width / 2
            lx = min(max(lx, 0), bounds.width - size.width)
            guard lx > lastEnd + 4 else { continue }
            text.draw(at: NSPoint(x: lx, y: bar.maxY + 2))
            lastEnd = lx + size.width
        }

        // What is busy already.
        NSColor.systemGray.withAlphaComponent(0.45).setFill()
        for b in strip.busy {
            let r = NSRect(x: x(b.from), y: bar.minY + 2, width: max(2, x(b.to) - x(b.from)), height: bar.height - 4)
            NSBezierPath(roundedRect: r, xRadius: 2, yRadius: 2).fill()
        }

        // The invitation's slot, dashed, and its time above.
        guard let p = strip.proposed else { return }
        let r = NSRect(x: x(p.from), y: bar.minY + 0.5, width: max(3, x(p.to) - x(p.from)), height: bar.height - 1)
        NSColor.systemBlue.withAlphaComponent(0.15).setFill()
        let path = NSBezierPath(roundedRect: r, xRadius: 3, yRadius: 3)
        path.fill()
        NSColor.systemBlue.setStroke()
        path.lineWidth = 1.5
        path.setLineDash([4, 2], count: 2, phase: 0)
        path.stroke()
        guard !strip.proposedLabel.isEmpty else { return }
        let label = NSAttributedString(
            string: strip.proposedLabel, attributes: [.font: Self.labelFont, .foregroundColor: NSColor.systemBlue])
        let size = label.size()
        let lx = min(max(r.midX - size.width / 2, 0), bounds.width - size.width)
        label.draw(at: NSPoint(x: lx, y: 0))
    }

    override func viewDidChangeEffectiveAppearance() {
        super.viewDidChangeEffectiveAppearance()
        needsDisplay = true
    }
}
