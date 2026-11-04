// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The colours of a verdict (ui/internal/signature `StyleOf`: libadwaita's
/// success, warning, error and dim-label; plan §11.3): green, orange, red,
/// secondary. The same for the badge, the heading of the details and the
/// small icon of a message row.
@MainActor
enum SignatureTint {
    static func color(_ s: SignatureView.Severity) -> NSColor {
        switch s {
        case .good, .goodSeen: return .systemGreen
        case .warning: return .systemOrange
        case .bad: return .systemRed
        case .neutral, .none: return Tint.secondary
        }
    }

    /// The SF Symbol of a verdict's icon at `size`; `description` is its
    /// accessible name.
    static func image(_ icon: String, size: IconSize = .small, description: String? = nil) -> NSImage {
        Icon.symbol(SignatureView.symbolName(icon), size: size, description: description)
    }
}

/// The badge in the header of a signed message (docs/plans/signatures.md
/// §6, `SignatureView.badge`): a borderless button with the verdict's icon
/// and its sentence, in the verdict's colour, which opens the details
/// popover. In the reading pane, the message window and the conversation's
/// cards; not in a chat bubble or an attached message. The sentence names
/// the signer from the certificate or key: plain text, set as the button's
/// title and never interpreted.
@MainActor
final class SignatureBadgeView: NSView {
    /// The badge was clicked; `anchor` is the button, for the popover.
    var onClick: (@MainActor (_ anchor: NSView) -> Void)?

    /// What the badge shows; hidden while it has no verdict.
    private(set) var badge = SignatureView.Badge()

    private let button = NSButton(title: "", target: nil, action: nil)

    /// - Parameter insets: around the button, inside the row.
    init(insets: NSEdgeInsets = NSEdgeInsets()) {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        button.isBordered = false
        button.bezelStyle = .inline
        button.imagePosition = .imageLeading
        button.imageHugsTitle = true
        button.alignment = .left
        button.lineBreakMode = .byTruncatingTail
        button.font = Typo.caption
        button.target = self
        button.action = #selector(clicked(_:))
        button.translatesAutoresizingMaskIntoConstraints = false
        button.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        addSubview(button)
        NSLayoutConstraint.activate([
            button.topAnchor.constraint(equalTo: topAnchor, constant: insets.top),
            button.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -insets.bottom),
            button.leadingAnchor.constraint(equalTo: leadingAnchor, constant: insets.left),
            button.trailingAnchor.constraint(lessThanOrEqualTo: trailingAnchor, constant: -insets.right),
        ])
        setContentHuggingPriority(.required, for: .vertical)
        isHidden = true
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Shows the verdict of `summary` (message.get's `signature`); nil, or
    /// a message without one, hides the row.
    func show(_ summary: SignatureSummary?) {
        let b = SignatureView.badge(for: summary)
        guard b != badge else { return }
        badge = b
        guard b.visible else {
            isHidden = true
            return
        }
        let colour = SignatureTint.color(b.severity)
        let style = NSMutableParagraphStyle()
        style.lineBreakMode = .byTruncatingTail
        // Fonts, colours and a paragraph style only: the text is the
        // sentence as it is.
        button.attributedTitle = NSAttributedString(string: b.text, attributes: [
            .font: Typo.caption, .foregroundColor: colour, .paragraphStyle: style,
        ])
        button.image = SignatureTint.image(b.icon, description: b.text)
        button.contentTintColor = colour
        button.toolTip = b.tooltip
        button.setAccessibilityLabel(b.text)
        button.setAccessibilityHelp(b.tooltip)
        isHidden = false
    }

    @objc private func clicked(_ sender: Any?) {
        onClick?(button)
    }
}
