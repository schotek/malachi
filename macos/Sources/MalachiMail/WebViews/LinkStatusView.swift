// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The link under the pointer in a stack of cards whose web views are in
/// the sized mode (`MessageWebView.onHover`): one small box for the whole
/// stack, plain text with the middle elided, as the single-message view
/// shows it (Mail's conversation view, the board's detail). The owner pins
/// it at the bottom-left corner of its area (`pin(in:)`) and hands it every
/// hover (`show(_:)`, "" hides it). The text is content of the mail:
/// `stringValue` only, capped at `MessageWebView.statusMaxChars`.
@MainActor
final class LinkStatusView: NSView {
    private let label = NSTextField(labelWithString: "")

    init() {
        super.init(frame: .zero)
        label.font = Typo.caption
        label.textColor = .labelColor
        label.lineBreakMode = .byTruncatingMiddle
        label.maximumNumberOfLines = 1
        label.isSelectable = false
        label.setContentCompressionResistancePriority(NSLayoutConstraint.Priority(240), for: .horizontal)
        label.setContentHuggingPriority(.defaultLow, for: .horizontal)
        label.translatesAutoresizingMaskIntoConstraints = false
        translatesAutoresizingMaskIntoConstraints = false
        wantsLayer = true
        layer?.backgroundColor = NSColor.windowBackgroundColor.withAlphaComponent(0.92).cgColor
        layer?.cornerRadius = 4
        layer?.borderWidth = 1
        layer?.borderColor = NSColor.separatorColor.cgColor
        isHidden = true
        addSubview(label)
        NSLayoutConstraint.activate([
            label.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 6),
            trailingAnchor.constraint(equalTo: label.trailingAnchor, constant: 6),
            label.topAnchor.constraint(equalTo: topAnchor, constant: 2),
            bottomAnchor.constraint(equalTo: label.bottomAnchor, constant: 2),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// The constraints that keep the box 6 in from the bottom-left corner
    /// of `container` (its subview), at most 80 % of its width.
    func pin(in container: NSView) -> [NSLayoutConstraint] {
        [
            leadingAnchor.constraint(equalTo: container.leadingAnchor, constant: 6),
            bottomAnchor.constraint(equalTo: container.bottomAnchor, constant: -6),
            widthAnchor.constraint(lessThanOrEqualTo: container.widthAnchor, multiplier: 0.8),
        ]
    }

    /// The link under the pointer; "" hides the box.
    func show(_ href: String) {
        let text = String(href.prefix(MessageWebView.statusMaxChars))
        label.stringValue = text
        isHidden = text.isEmpty
    }

    /// The text on display ("" while hidden).
    var text: String { label.stringValue }
}
