// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// A count in a disc that grows into a capsule with more digits: the
/// unread count of a sidebar folder (`label.unread-badge`) and the member
/// count of a thread (`label.thread-count`). Drawn by hand rather than as a
/// text field, whose line box would sit the digits off centre and whose
/// insets would stretch a single digit into a capsule: the cap height is
/// what is centred.
@MainActor
final class CountBadgeView: NSView {
    var text = "" {
        didSet { invalidateIntrinsicContentSize(); needsDisplay = true }
    }
    var font: NSFont = Typo.captionNumeric {
        didSet { invalidateIntrinsicContentSize(); needsDisplay = true }
    }
    var fill: NSColor = Tint.fg(alpha: 0.1) {
        didSet { needsDisplay = true }
    }
    var textColor: NSColor = .labelColor {
        didSet { needsDisplay = true }
    }

    /// The space left and right of the digits once they outgrow the disc.
    private let padding: CGFloat

    init(padding: CGFloat) {
        self.padding = padding
        super.init(frame: .zero)
        setContentHuggingPriority(.required, for: .horizontal)
        setContentHuggingPriority(.required, for: .vertical)
        setContentCompressionResistancePriority(.required, for: .horizontal)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    private var attributes: [NSAttributedString.Key: Any] {
        [.font: font, .foregroundColor: textColor]
    }

    /// The disc's diameter: the font's line height and a point of air.
    private var diameter: CGFloat {
        ceil(font.ascender - font.descender) + 2
    }

    override var intrinsicContentSize: NSSize {
        let width = ceil((text as NSString).size(withAttributes: attributes).width) + 2 * padding
        return NSSize(width: max(diameter, width), height: diameter)
    }

    override func draw(_ dirtyRect: NSRect) {
        let radius = bounds.height / 2
        fill.setFill()
        NSBezierPath(roundedRect: bounds, xRadius: radius, yRadius: radius).fill()
        let string = text as NSString
        let width = string.size(withAttributes: attributes).width
        // draw(at:) puts the bottom of the line box at the point; the
        // baseline is the descender above it.
        let baseline = (bounds.midY - font.capHeight / 2).rounded()
        let origin = NSPoint(x: bounds.midX - width / 2, y: baseline + font.descender)
        string.draw(at: origin, withAttributes: attributes)
    }
}
