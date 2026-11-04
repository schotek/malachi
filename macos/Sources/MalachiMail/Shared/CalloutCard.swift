// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// How serious a banner is: the colour of its symbol.
enum CalloutSeverity {
    /// A state to know about (a draft, a message waiting to be sent,
    /// blocked images): the symbol in the secondary label colour.
    case info
    /// Something to act on (no backend, a sign-in, a certificate, a failed
    /// send): the symbol in orange.
    case warning

    var tint: NSColor {
        switch self {
        case .info: return .secondaryLabelColor
        case .warning: return .systemOrange
        }
    }
}

/// The card of the Mac's banners (BannerView, RemoteBarView,
/// WizardBannerView): a rounded rectangle in a subtle system fill, inset
/// from the edges of the pane it sits in, in place of Adw.Banner's
/// accent-tinted strip across the whole width (a deviation, macos/README.md).
/// The fill is a dynamic colour, set in `updateLayer`, so it follows the
/// appearance.
@MainActor
final class CalloutCard: NSView {
    nonisolated static let cornerRadius: CGFloat = 10
    /// Around the card, inside the banner's own frame.
    nonisolated static let margins = NSEdgeInsets(top: 6, left: 8, bottom: 2, right: 8)
    /// Between the card's edge and its content.
    nonisolated static let padding = NSEdgeInsets(top: 6, left: 10, bottom: 6, right: 8)
    /// Between the symbol, the text and the buttons.
    nonisolated static let spacing: CGFloat = 8

    override init(frame frameRect: NSRect) {
        super.init(frame: frameRect)
        translatesAutoresizingMaskIntoConstraints = false
        wantsLayer = true
        layer?.cornerRadius = Self.cornerRadius
        layer?.cornerCurve = .continuous
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var wantsUpdateLayer: Bool { true }

    override func updateLayer() {
        layer?.backgroundColor = NSColor.tertiarySystemFill.cgColor
    }

    /// Puts `content` into a new card inside `host`, `margins` from its
    /// edges, the content `padding` from the card's.
    @discardableResult
    static func install(_ content: NSView, in host: NSView, margins: NSEdgeInsets = CalloutCard.margins) -> CalloutCard {
        let card = CalloutCard()
        content.translatesAutoresizingMaskIntoConstraints = false
        card.addSubview(content)
        host.addSubview(card)
        NSLayoutConstraint.activate([
            card.topAnchor.constraint(equalTo: host.topAnchor, constant: margins.top),
            host.bottomAnchor.constraint(equalTo: card.bottomAnchor, constant: margins.bottom),
            card.leadingAnchor.constraint(equalTo: host.leadingAnchor, constant: margins.left),
            host.trailingAnchor.constraint(equalTo: card.trailingAnchor, constant: margins.right),
            content.topAnchor.constraint(equalTo: card.topAnchor, constant: padding.top),
            card.bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: padding.bottom),
            content.leadingAnchor.constraint(equalTo: card.leadingAnchor, constant: padding.left),
            card.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: padding.right),
        ])
        return card
    }

    /// The symbol at the start of a card: an SF Symbol of the body's size,
    /// decorative (the text beside it says the same).
    static func symbolView() -> NSImageView {
        let v = NSImageView()
        v.imageScaling = .scaleNone
        v.setAccessibilityElement(false)
        v.setContentHuggingPriority(.required, for: .horizontal)
        v.setContentCompressionResistancePriority(.required, for: .horizontal)
        return v
    }

    /// Shows `symbol` in `view`, coloured for `severity`.
    static func show(_ symbol: String, _ severity: CalloutSeverity, in view: NSImageView) {
        let config = NSImage.SymbolConfiguration(pointSize: 14, weight: .medium)
        view.image = NSImage(systemSymbolName: symbol, accessibilityDescription: nil)?.withSymbolConfiguration(config)
        view.contentTintColor = severity.tint
    }
}
