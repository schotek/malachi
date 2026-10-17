// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The fixed strip above a conversation (docs/plans/chat-view.md D8, D9):
/// it does not scroll with the cards or bubbles under it, and its content
/// keeps to their column (a clamp of the same width and the same side
/// insets), at the trailing end: Collapse All / Expand All of the timeline
/// (`setFoldAll`; hidden in the chat and while fewer than two cards fold),
/// then the Show as Chat toggle, which is always there while a conversation
/// is shown. The toggle only reports clicks (`onToggle`); its state is the
/// setting's (`setChat`), so a change from elsewhere shows too.
@MainActor
final class ConversationStripView: NSView {
    /// Collapse All / Expand All was clicked.
    var onFoldAll: (@MainActor () -> Void)?
    /// The toggle was clicked: the chat on (true) or off.
    var onToggle: (@MainActor (Bool) -> Void)?

    private let clamp: ClampView
    private let foldAllButton = NSButton(title: "", target: nil, action: nil)
    private let toggle = NSButton(title: "", target: nil, action: nil)

    /// Above and below the strip's controls.
    private static let top: CGFloat = 6
    private static let bottom: CGFloat = 2

    override init(frame: NSRect) {
        let widest = CGFloat(ConversationLayout.Metrics.maxWidth)
        clamp = ClampView(maximum: widest, tight: widest)
        super.init(frame: frame)
        translatesAutoresizingMaskIntoConstraints = false
        build()
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    private func build() {
        // Collapse All / Expand All: a small borderless text button, as it
        // was above the timeline's cards.
        foldAllButton.isBordered = false
        foldAllButton.bezelStyle = .inline
        foldAllButton.setButtonType(.momentaryChange)
        foldAllButton.font = Typo.caption
        foldAllButton.contentTintColor = .linkColor
        foldAllButton.target = self
        foldAllButton.action = #selector(foldAllClicked(_:))
        foldAllButton.isHidden = true

        // Show as Chat: a toggle with the bubbles symbol, its on state
        // pressed in and tinted with the accent colour.
        let label = Conversation.chatToggleLabel()
        toggle.title = label
        toggle.setButtonType(.pushOnPushOff)
        toggle.bezelStyle = .recessed
        toggle.showsBorderOnlyWhileMouseInside = false
        toggle.controlSize = .small
        toggle.font = Typo.caption
        toggle.image = NSImage(systemSymbolName: "bubble.left.and.bubble.right", accessibilityDescription: nil)?
            .withSymbolConfiguration(NSImage.SymbolConfiguration(pointSize: Typo.captionSize, weight: .regular))
        toggle.imagePosition = .imageLeading
        toggle.imageHugsTitle = true
        toggle.toolTip = Conversation.chatToggleTooltip()
        toggle.setAccessibilityLabel(label)
        toggle.setAccessibilityHelp(Conversation.chatToggleTooltip())
        toggle.target = self
        toggle.action = #selector(toggleClicked(_:))

        for b in [foldAllButton, toggle] {
            b.setContentHuggingPriority(.required, for: .horizontal)
            b.setContentCompressionResistancePriority(.required, for: .horizontal)
        }
        let row = NSStackView(views: [foldAllButton, toggle])
        row.orientation = .horizontal
        row.alignment = .centerY
        row.spacing = 12
        row.translatesAutoresizingMaskIntoConstraints = false

        let column = NSView()
        column.translatesAutoresizingMaskIntoConstraints = false
        column.addSubview(row)
        NSLayoutConstraint.activate([
            row.topAnchor.constraint(equalTo: column.topAnchor, constant: Self.top),
            row.bottomAnchor.constraint(equalTo: column.bottomAnchor, constant: -Self.bottom),
            row.trailingAnchor.constraint(equalTo: column.trailingAnchor, constant: -ConversationMetrics.sideInset),
            row.leadingAnchor.constraint(
                greaterThanOrEqualTo: column.leadingAnchor, constant: ConversationMetrics.sideInset),
        ])
        clamp.setChild(column)
        addSubview(clamp)
        NSLayoutConstraint.activate([
            clamp.topAnchor.constraint(equalTo: topAnchor),
            clamp.bottomAnchor.constraint(equalTo: bottomAnchor),
            clamp.leadingAnchor.constraint(equalTo: leadingAnchor),
            clamp.trailingAnchor.constraint(equalTo: trailingAnchor),
        ])
    }

    /// What Collapse All / Expand All offers (`.none` hides it: the chat
    /// is shown, or fewer than two cards fold).
    func setFoldAll(_ offer: Conversation.FoldAll) {
        let label = offer.label
        foldAllButton.title = label
        foldAllButton.setAccessibilityLabel(label)
        foldAllButton.isHidden = offer == .none
    }

    /// The toggle shows the chat on or off.
    func setChat(_ on: Bool) {
        toggle.state = on ? .on : .off
        toggle.contentTintColor = on ? .controlAccentColor : nil
    }

    @objc private func foldAllClicked(_ sender: Any?) {
        onFoldAll?()
    }

    @objc private func toggleClicked(_ sender: Any?) {
        let on = toggle.state == .on
        toggle.contentTintColor = on ? .controlAccentColor : nil
        onToggle?(on)
    }
}
