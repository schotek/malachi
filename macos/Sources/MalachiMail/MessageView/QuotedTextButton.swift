// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The small "•••" button under a body whose quoted history the daemon cut
/// (quoted.go, `quotedTextOffer`): Show Quoted Text, then Hide Quoted Text
/// in its tooltip and accessible name. A row of its own, the button at the
/// start, hidden while there is nothing to offer. The conversation cards,
/// the pane's message and the message window each have one.
@MainActor
final class QuotedTextButton: NSView {
    /// The button was clicked: the view switches the body to the other
    /// variant.
    var onToggle: (@MainActor () -> Void)?

    /// What the button offers now; nil hides the row.
    private(set) var offer: QuotedTextOffer?

    private let button = NSButton(title: "•••", target: nil, action: nil)

    /// - Parameter insets: around the button, inside the row.
    init(insets: NSEdgeInsets) {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        button.bezelStyle = .inline
        button.controlSize = .small
        button.font = Typo.caption
        button.target = self
        button.action = #selector(clicked(_:))
        button.translatesAutoresizingMaskIntoConstraints = false
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

    /// Shows `offer` (nil: no row).
    func show(_ offer: QuotedTextOffer?) {
        self.offer = offer
        isHidden = offer == nil
        guard let offer else { return }
        let label = offer.label
        button.toolTip = label
        button.setAccessibilityLabel(label)
    }

    @objc private func clicked(_ sender: Any?) {
        onToggle?()
    }
}
