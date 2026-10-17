// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The calendar views' metrics (docs/calendar.md §5.3, the wireframe at
/// 1280 × 820): what the views lay out with. Nothing here decides what an
/// occurrence is; Core does (`Cal.layoutDay`, `Cal.allDayLanes`, …).
@MainActor
enum CalendarMetrics {
    /// The height of an hour in the time grid.
    static let hourHeight: CGFloat = 60
    /// The hour axis left of the day columns.
    static let axisWidth: CGFloat = 60
    /// The day header row of Week and Day.
    static let dayHeaderHeight: CGFloat = 40
    /// The all-day row at its smallest (one lane and air).
    static let allDayMinimum: CGFloat = 36
    /// One lane of the all-day row and its gap.
    static let laneHeight: CGFloat = 18
    static let laneGap: CGFloat = 2
    /// The most lanes the all-day row grows to (bars in further lanes are
    /// not drawn; the Agenda and the Month list them).
    static let allDayMaxLanes = 5
    /// The coloured bar at a block's left edge.
    static let blockBar: CGFloat = 3
    /// The tint of a block's fill (≈ 18 % of the calendar's colour).
    static let blockTint: CGFloat = 0.18
    /// A month cell's pill line.
    static let pillLine: CGFloat = 17
    /// A month cell's day number line.
    static let cellNumberLine: CGFloat = 22
    /// The week-number column of the month.
    static let weekNumberWidth: CGFloat = 28
    /// The month's weekday header.
    static let weekdayHeaderHeight: CGFloat = 24

    static var titleFont: NSFont { .systemFont(ofSize: Typo.size == .larger ? 12 : 11, weight: .semibold) }
    static var smallFont: NSFont { .systemFont(ofSize: Typo.size == .larger ? 11 : 10) }
}

/// The colour of a calendar from its `#rrggbb` (the daemon always sends
/// one; anything else is the accent colour).
@MainActor
enum CalendarColor {
    static func color(_ hex: String?) -> NSColor {
        guard let hex else { return Tint.accent }
        let b = Array(hex.utf8)
        guard b.count == 7, b[0] == UInt8(ascii: "#") else { return Tint.accent }
        var v: UInt32 = 0
        for c in b[1...] {
            let d: UInt32
            switch c {
            case 0x30...0x39: d = UInt32(c - 0x30)
            case 0x61...0x66: d = UInt32(c - 0x61 + 10)
            case 0x41...0x46: d = UInt32(c - 0x41 + 10)
            default: return Tint.accent
            }
            v = v << 4 | d
        }
        return NSColor(
            srgbRed: CGFloat(v >> 16 & 0xff) / 255, green: CGFloat(v >> 8 & 0xff) / 255, blue: CGFloat(v & 0xff) / 255,
            alpha: 1)
    }

    /// The colour of an occurrence's calendar in `snapshot` (an invitation
    /// not in a calendar: the accent colour).
    static func of(_ o: Occurrence, in snapshot: Cal.Snapshot) -> NSColor {
        guard let id = o.calendarId, let c = snapshot.calendar(id) else { return Tint.accent }
        return color(c.color)
    }
}

/// Draws the fill and the outline of a block, a bar or a pill by its
/// `Cal.BlockState`: the tinted fill, stripes for tentative, a dashed
/// outline for an unanswered invitation, fainter for declined and
/// cancelled. Every fill is opaque: the calendar's colour blended over the
/// views' background in the current appearance (light or dark), so that
/// no grid line shows through a block.
@MainActor
enum BlockPainter {
    /// What the calendar's views are drawn on (the content's background).
    static var background: NSColor { .windowBackgroundColor }

    /// `color` at `fraction` over the background, opaque; call inside
    /// `draw(_:)`, where the view's appearance is the drawing one.
    static func tint(_ color: NSColor, _ fraction: CGFloat) -> NSColor {
        let bg = background.usingColorSpace(.sRGB) ?? .white
        let c = color.usingColorSpace(.sRGB) ?? color
        return bg.blended(withFraction: fraction, of: c) ?? bg
    }

    static func paint(
        _ rect: NSRect, radius: CGFloat, color: NSColor, state: Cal.BlockState, selected: Bool, filled: Bool = true
    ) {
        let path = NSBezierPath(roundedRect: rect, xRadius: radius, yRadius: radius)
        let faded = state == .cancelled || state == .declined
        let tint = selected ? 0.42 : CalendarMetrics.blockTint
        if state != .unanswered, filled {
            Self.tint(color, faded ? tint / 2 : tint).setFill()
            path.fill()
        }
        if state == .tentative, filled {
            NSGraphicsContext.saveGraphicsState()
            path.addClip()
            let stripes = NSBezierPath()
            var x = rect.minX - rect.height
            while x < rect.maxX {
                stripes.move(to: NSPoint(x: x, y: rect.minY))
                stripes.line(to: NSPoint(x: x + rect.height, y: rect.maxY))
                x += 8
            }
            stripes.lineWidth = 2.5
            Self.tint(color, tint + 0.18).setStroke()
            stripes.stroke()
            NSGraphicsContext.restoreGraphicsState()
        }
        if state == .unanswered {
            Self.tint(color, selected ? 0.2 : 0.06).setFill()
            path.fill()
            let outline = NSBezierPath(roundedRect: rect.insetBy(dx: 0.5, dy: 0.5), xRadius: radius, yRadius: radius)
            outline.lineWidth = 1
            outline.setLineDash([4, 3], count: 2, phase: 0)
            color.setStroke()
            outline.stroke()
        } else if selected {
            let outline = NSBezierPath(roundedRect: rect.insetBy(dx: 0.5, dy: 0.5), xRadius: radius, yRadius: radius)
            outline.lineWidth = 1
            color.setStroke()
            outline.stroke()
        }
    }

    /// The title's text: struck through when declined or cancelled. Plain
    /// text with attributes, never markup.
    static func title(_ s: String, font: NSFont, color: NSColor, state: Cal.BlockState) -> NSAttributedString {
        var a: [NSAttributedString.Key: Any] = [.font: font, .foregroundColor: color]
        if state == .declined || state == .cancelled {
            a[.strikethroughStyle] = NSUnderlineStyle.single.rawValue
        }
        return NSAttributedString(string: s, attributes: a)
    }
}

/// A server's strings for the calendar's views, cleaned the way Core
/// cleans them (control, bidi and invisible characters out, one line).
enum CalendarText {
    /// An event's title, Untitled Event when empty.
    static func title(_ s: String?) -> String {
        let t = Board.cleanLine(s ?? "", max: API.Limits.maxEventTitleBytes)
        return t.isEmpty ? Cal.Text.untitled : t
    }

    /// A place or a name.
    static func line(_ s: String?) -> String {
        Board.cleanLine(s ?? "", max: API.Limits.maxEventLocationBytes)
    }
}

/// A non-editable one-line label for the calendar's views; server strings
/// go in through `stringValue` (or a plain attributed string) only.
@MainActor
func calendarLabel(_ font: NSFont, color: NSColor = .labelColor) -> NSTextField {
    let l = NSTextField(labelWithString: "")
    l.font = font
    l.textColor = color
    l.lineBreakMode = .byTruncatingTail
    l.maximumNumberOfLines = 1
    l.cell?.truncatesLastVisibleLine = true
    return l
}

/// A symbol image view of `pointSize`.
@MainActor
func calendarSymbol(_ name: String, pointSize: CGFloat, color: NSColor = .secondaryLabelColor) -> NSImageView {
    let v = NSImageView()
    v.image = NSImage(systemSymbolName: name, accessibilityDescription: nil)?
        .withSymbolConfiguration(.init(pointSize: pointSize, weight: .regular))
    v.contentTintColor = color
    v.imageScaling = .scaleNone
    return v
}

/// One timed occurrence in the Week or Day grid (docs/calendar.md §5.3):
/// the tinted fill of its calendar's colour with a 3 pt bar at the left,
/// the title in bold, the time, the recurring symbol and the attendee
/// count; in the Day view also the place and the video-call symbol. An
/// unanswered invitation is a dashed outline with a "?" badge, tentative
/// is striped, declined and cancelled are struck through. The block is an
/// accessibility element named by `Cal.blockLabel`.
@MainActor
final class EventBlockView: NSView {
    let block: Cal.Block
    let key: String
    /// A click: the page selects it and opens its detail anchored here.
    var onClick: ((EventBlockView) -> Void)?

    var selected = false {
        didSet { needsDisplay = true }
    }

    private let color: NSColor
    private let title = calendarLabel(CalendarMetrics.titleFont)
    private let time = calendarLabel(CalendarMetrics.smallFont, color: .secondaryLabelColor)
    private let place = calendarLabel(CalendarMetrics.smallFont, color: .secondaryLabelColor)
    private let symbols = NSStackView()
    private let question = calendarSymbol("questionmark.circle.fill", pointSize: 11)
    private let wide: Bool

    /// `wide`: the Day view, with room for the place and the video call.
    init(block: Cal.Block, color: NSColor, label: String, env: Cal.Env, wide: Bool) {
        self.block = block
        self.color = color
        self.wide = wide
        key = Cal.occurrenceKey(block.occurrence)
        super.init(frame: .zero)
        let o = block.occurrence
        title.attributedStringValue = BlockPainter.title(
            CalendarText.title(o.title), font: CalendarMetrics.titleFont, color: .labelColor, state: block.state)
        time.stringValue = Cal.Text.timeRange(block.start, block.end, env)
        place.stringValue = wide ? CalendarText.line(o.location) : ""
        symbols.orientation = .horizontal
        symbols.spacing = 3
        symbols.alignment = .centerY
        if o.recurring {
            symbols.addArrangedSubview(calendarSymbol("repeat", pointSize: 9))
        }
        if wide, o.videoCall {
            symbols.addArrangedSubview(calendarSymbol("video", pointSize: 9))
        }
        if o.attendeeCount > 0 {
            symbols.addArrangedSubview(calendarSymbol("person.2", pointSize: 9))
            let n = calendarLabel(CalendarMetrics.smallFont, color: .secondaryLabelColor)
            n.stringValue = String(o.attendeeCount)
            symbols.addArrangedSubview(n)
        }
        question.contentTintColor = color
        question.isHidden = block.state != .unanswered
        for v in [title, time, place, symbols, question] as [NSView] {
            addSubview(v)
        }
        if block.state == .cancelled {
            // Opaque: fainter text, never a see-through block.
            title.textColor = .secondaryLabelColor
        }
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
        let inset = CalendarMetrics.blockBar + 4
        let w = bounds.width - inset - 3
        var y: CGFloat = 2
        let line = ceil(CalendarMetrics.titleFont.ascender - CalendarMetrics.titleFont.descender) + 1
        let small = ceil(CalendarMetrics.smallFont.ascender - CalendarMetrics.smallFont.descender) + 1
        let qw: CGFloat = question.isHidden ? 0 : 14
        title.frame = NSRect(x: inset, y: y, width: max(0, w - qw), height: line)
        question.frame = NSRect(x: bounds.width - 15, y: y, width: 13, height: line)
        y += line
        // What does not fit the block's height is left out, never squeezed.
        let room = bounds.height - 2
        time.isHidden = y + small > room
        time.frame = NSRect(x: inset, y: y, width: max(0, w), height: small)
        if !time.isHidden { y += small }
        place.isHidden = place.stringValue.isEmpty || y + small > room
        place.frame = NSRect(x: inset, y: y, width: max(0, w), height: small)
        if !place.isHidden { y += small }
        let fit = symbols.fittingSize
        symbols.isHidden = symbols.arrangedSubviews.isEmpty || y + small > room
        symbols.frame = NSRect(x: inset, y: y, width: min(fit.width, max(0, w)), height: small)
    }

    override func draw(_ dirtyRect: NSRect) {
        let r = bounds.insetBy(dx: 0.5, dy: 0.5)
        BlockPainter.paint(r, radius: 4, color: color, state: block.state, selected: selected)
        if block.state != .unanswered {
            color.setFill()
            NSBezierPath(
                roundedRect: NSRect(x: r.minX, y: r.minY, width: CalendarMetrics.blockBar, height: r.height),
                xRadius: 1.5, yRadius: 1.5
            ).fill()
        }
    }

    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    override func mouseDown(with event: NSEvent) {
        onClick?(self)
    }

    override func accessibilityPerformPress() -> Bool {
        onClick?(self)
        return true
    }
}
