// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The fixed strip above a conversation (docs/plans/chat-view.md D8, D9):
/// it does not scroll with the cards or bubbles under it, and its content
/// keeps to their column (a clamp of the same width and the same side
/// insets), at the trailing end: Collapse All / Expand All of the timeline
/// (`setFoldAll`; hidden in the chat and while fewer than two cards fold),
/// then the layout toggle, which is always there while a conversation is
/// shown. The toggle names the layout a click shows: Show as Chat while the
/// timeline shows, Show as Thread while the chat shows (pressed in and
/// tinted with the accent colour), its tooltip and accessibility name
/// following (`Conversation.chatToggleLabel(on:)`). The toggle only reports
/// clicks (`onToggle`); its state is the setting's (`setChat`, called by the
/// host when it shows and whenever the setting changes), so a change from
/// elsewhere shows too.
@MainActor
final class ConversationStripView: NSView {
    /// Collapse All / Expand All was clicked.
    var onFoldAll: (@MainActor () -> Void)?
    /// The toggle was clicked: the chat on (true) or off.
    var onToggle: (@MainActor (Bool) -> Void)?

    private let clamp: ClampView
    private let foldAllButton = NSButton(title: "", target: nil, action: nil)
    private let toggle = ConversationLayoutToggle(
        label: { Conversation.chatToggleLabel(on: $0) }, tooltip: { Conversation.chatToggleTooltip(on: $0) })

    /// Above and below the strip's controls, so that the toggle's bezel
    /// touches neither the strip's top nor the content under it.
    private static let top: CGFloat = 8
    private static let bottom: CGFloat = 6

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

        // The layout toggle (`ConversationLayoutToggle`, the same look as
        // on the board).
        toggle.onToggle = { [weak self] on in self?.onToggle?(on) }

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

    /// The toggle shows the chat on or off: its state, its label, tooltip
    /// and accessibility name.
    func setChat(_ on: Bool) {
        toggle.setChat(on)
    }

    @objc private func foldAllClicked(_ sender: Any?) {
        onFoldAll?()
    }
}
