// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The layout toggle of a conversation (docs/plans/chat-view.md D9), one
/// look for the strip above Mail's conversation (`ConversationStripView`)
/// and the heading row of the board detail's conversation block
/// (`BoardConversationBlock`): the bubbles symbol and the label of the
/// layout a click shows, its on state pressed in and tinted with the
/// accent colour, its tooltip and accessibility name following the state.
/// The texts come from the owner (`Conversation.chatToggleLabel(on:)` in
/// Mail, `Board.Text.chatToggleLabel(on:)` on the board). The toggle only
/// reports clicks (`onToggle`); its state is the setting's (`setChat`,
/// called by the owner when it shows and whenever the setting changes), so
/// a change from elsewhere shows too.
@MainActor
final class ConversationLayoutToggle: NSButton {
    /// The toggle was clicked: the chat on (true) or off.
    var onToggle: (@MainActor (Bool) -> Void)?

    private let labelFor: @MainActor (Bool) -> String
    private let tooltipFor: @MainActor (Bool) -> String

    init(label: @escaping @MainActor (Bool) -> String, tooltip: @escaping @MainActor (Bool) -> String) {
        labelFor = label
        tooltipFor = tooltip
        super.init(frame: .zero)
        title = ""
        setButtonType(.pushOnPushOff)
        bezelStyle = .recessed
        showsBorderOnlyWhileMouseInside = false
        controlSize = .small
        font = Typo.caption
        image = NSImage(systemSymbolName: "bubble.left.and.bubble.right", accessibilityDescription: nil)?
            .withSymbolConfiguration(NSImage.SymbolConfiguration(pointSize: Typo.captionSize, weight: .regular))
        imagePosition = .imageLeading
        imageHugsTitle = true
        render(chat: false)
        target = self
        action = #selector(clicked(_:))
        setContentHuggingPriority(.required, for: .horizontal)
        setContentCompressionResistancePriority(.required, for: .horizontal)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// The toggle shows the chat on or off: its state, its label, tooltip
    /// and accessibility name.
    func setChat(_ on: Bool) {
        state = on ? .on : .off
        render(chat: on)
    }

    /// The toggle's look for the chat on or off: the label and tooltip of
    /// that state and, on, the accent tint.
    private func render(chat on: Bool) {
        let label = labelFor(on)
        let tooltip = tooltipFor(on)
        if title != label {
            title = label
        }
        toolTip = tooltip
        setAccessibilityLabel(label)
        setAccessibilityHelp(tooltip)
        contentTintColor = on ? .controlAccentColor : nil
    }

    @objc private func clicked(_ sender: Any?) {
        let on = state == .on
        render(chat: on)
        onToggle?(on)
    }
}
