// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The colours of the conversation view, system colours all: they follow
/// the appearance by themselves. The views that paint a layer with them do
/// so in `updateLayer`, which runs again when the appearance changes; with
/// Increase Contrast (the appearance is one of the high-contrast ones then)
/// the hairlines take the next stronger colour.
@MainActor
enum ConversationTint {
    /// The pane behind the cards: slightly grey beside their background.
    static var surface: NSColor { .windowBackgroundColor }
    /// A card's background.
    static var cardFill: NSColor { Tint.cardFill }

    /// A card's hairline border, as `view` is shown.
    static func cardBorder(in view: NSView) -> NSColor {
        highContrast(view) ? .secondaryLabelColor : Tint.cardBorder
    }

    /// The timeline's line and dots: stronger than the cards' border.
    static func line(in view: NSView) -> NSColor {
        highContrast(view) ? .secondaryLabelColor : .tertiaryLabelColor
    }

    private static let contrasted: [NSAppearance.Name] = [
        .accessibilityHighContrastAqua, .accessibilityHighContrastDarkAqua,
        .accessibilityHighContrastVibrantLight, .accessibilityHighContrastVibrantDark,
    ]
    private static let plain: [NSAppearance.Name] = [.aqua, .darkAqua, .vibrantLight, .vibrantDark]

    /// `view` is shown with increased contrast.
    static func highContrast(_ view: NSView) -> Bool {
        guard let best = view.effectiveAppearance.bestMatch(from: contrasted + plain) else { return false }
        return contrasted.contains(best)
    }
}

/// The measures of `ConversationLayout.Metrics` as AppKit takes them.
@MainActor
enum ConversationMetrics {
    static let sideInset = CGFloat(ConversationLayout.Metrics.sideInset)
    static let avatar = CGFloat(ConversationLayout.Metrics.avatar)
    static let gutterGap = CGFloat(ConversationLayout.Metrics.gutterGap)
    static let itemGap = CGFloat(ConversationLayout.Metrics.itemGap)
    static let dot = CGFloat(ConversationLayout.Metrics.dot)
    static let line = CGFloat(ConversationLayout.Metrics.line)
    static let lineBreak = CGFloat(ConversationLayout.Metrics.lineBreak)
    static let cardRadius = CGFloat(ConversationLayout.Metrics.cardRadius)
    static let cardPaddingV = CGFloat(ConversationLayout.Metrics.cardPaddingV)
    static let cardPaddingH = CGFloat(ConversationLayout.Metrics.cardPaddingH)
    /// From the column's leading edge to the cards.
    static let gutter = avatar + gutterGap

    /// How far below the top of a caption's first line its middle is: where
    /// the dot of an event sits.
    static var captionMiddle: CGFloat {
        let font = Typo.caption
        return ((font.ascender - font.descender + font.leading) / 2).rounded()
    }
}

/// The pane behind the cards of a conversation.
@MainActor
final class ConversationSurfaceView: NSView {
    override init(frame: NSRect) {
        super.init(frame: frame)
        wantsLayer = true
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var wantsUpdateLayer: Bool { true }

    override func updateLayer() {
        layer?.backgroundColor = ConversationTint.surface.cgColor
    }
}

/// One item of the conversation's stack with its piece of the timeline
/// (`ConversationLayout.rails`): the gap above the item, in the gutter the
/// marker (the sender's avatar at the top of a card, a dot beside the first
/// line of an event or of the row of older messages) with the line above
/// and below it, and beside the gutter the item itself. The rows of the
/// stack touch, so the pieces of the line join: each runs through its own
/// row from edge to edge, the gap included, and stops short of the marker.
/// The line is made of views pinned to the row, so it follows the row's
/// height by itself while a body arrives, a web view measures its document
/// or gives way to a placeholder.
///
/// The gutter is decoration: the card and the event row name their sender
/// to accessibility themselves.
@MainActor
final class ConversationRow: NSView {
    /// What the row marks its item with.
    enum Mark {
        /// The sender's avatar, its top at the top of the item.
        case avatar
        /// A dot whose middle is `below` the top of the item.
        case dot(below: CGFloat)
    }

    let content: NSView

    private let avatar: AvatarView?
    private let dot: RailPiece?
    private let above = RailPiece()
    private let below = RailPiece()

    init(content: NSView, mark: Mark) {
        self.content = content
        let marker: NSView
        let markerTop: CGFloat
        switch mark {
        case .avatar:
            let a = AvatarView(size: ConversationMetrics.avatar)
            a.setAccessibilityElement(false)
            avatar = a
            dot = nil
            marker = a
            markerTop = ConversationMetrics.itemGap
        case .dot(let middle):
            let d = RailPiece(round: true)
            avatar = nil
            dot = d
            marker = d
            markerTop = ConversationMetrics.itemGap + middle - ConversationMetrics.dot / 2
        }
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false

        let gutter = NSLayoutGuide()
        addLayoutGuide(gutter)
        content.translatesAutoresizingMaskIntoConstraints = false
        addSubview(content)
        for v in [above, below, marker] {
            addSubview(v)
        }
        // A row shorter than its marker (it cannot be, but fonts vary)
        // gives the line up rather than the layout.
        let lineEnds = [
            above.bottomAnchor.constraint(equalTo: marker.topAnchor, constant: -ConversationMetrics.lineBreak),
            below.topAnchor.constraint(equalTo: marker.bottomAnchor, constant: ConversationMetrics.lineBreak),
            below.bottomAnchor.constraint(equalTo: bottomAnchor),
        ]
        for c in lineEnds {
            c.priority = .defaultHigh
        }
        var constraints = [
            gutter.leadingAnchor.constraint(equalTo: leadingAnchor),
            gutter.widthAnchor.constraint(equalToConstant: ConversationMetrics.avatar),
            gutter.topAnchor.constraint(equalTo: topAnchor),
            gutter.bottomAnchor.constraint(equalTo: bottomAnchor),

            content.topAnchor.constraint(equalTo: topAnchor, constant: ConversationMetrics.itemGap),
            content.bottomAnchor.constraint(equalTo: bottomAnchor),
            content.leadingAnchor.constraint(equalTo: gutter.trailingAnchor, constant: ConversationMetrics.gutterGap),
            content.trailingAnchor.constraint(equalTo: trailingAnchor),

            marker.centerXAnchor.constraint(equalTo: gutter.centerXAnchor),
            marker.topAnchor.constraint(equalTo: topAnchor, constant: markerTop),

            above.topAnchor.constraint(equalTo: topAnchor),
            above.centerXAnchor.constraint(equalTo: gutter.centerXAnchor),
            above.widthAnchor.constraint(equalToConstant: ConversationMetrics.line),
            below.centerXAnchor.constraint(equalTo: gutter.centerXAnchor),
            below.widthAnchor.constraint(equalToConstant: ConversationMetrics.line),
        ]
        if dot != nil {
            constraints += [
                marker.widthAnchor.constraint(equalToConstant: ConversationMetrics.dot),
                marker.heightAnchor.constraint(equalToConstant: ConversationMetrics.dot),
            ]
        }
        NSLayoutConstraint.activate(constraints + lineEnds)
        above.isHidden = true
        below.isHidden = true
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Shows the row's piece of the timeline. `name` is the sender the
    /// avatar stands for (hostile text, only ever drawn as initials);
    /// `monochrome` is the list's setting for avatars, which the accent
    /// tint of the user's own message overrides.
    func show(_ rail: ConversationLayout.Rail, name: String, monochrome: Bool) {
        above.isHidden = !rail.above
        below.isHidden = !rail.below
        guard let avatar else { return }
        avatar.text = name
        avatar.monochrome = monochrome
        avatar.accent = rail.accent
    }
}

/// A piece of the timeline: a stretch of the line, or a dot.
@MainActor
private final class RailPiece: NSView {
    private let round: Bool

    init(round: Bool = false) {
        self.round = round
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        wantsLayer = true
        setAccessibilityElement(false)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var wantsUpdateLayer: Bool { true }

    override func updateLayer() {
        layer?.backgroundColor = ConversationTint.line(in: self).cgColor
        layer?.cornerRadius = round ? min(bounds.width, bounds.height) / 2 : 0
    }

    override func layout() {
        super.layout()
        if round {
            needsDisplay = true
        }
    }
}
