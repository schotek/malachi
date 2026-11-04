// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// A draft card of the conversation view (`Conversation.DraftCard`,
/// window/conversation_draft.go): the user's unsent reply to the
/// conversation, on an issue the unsent comment, right under what opened
/// the conversation (`Conversation.draftsFirst`). A native card, never a
/// web view, in the shape of the message cards: the header has the sender
/// (the user), the Draft badge (and the Internal one of an internal
/// comment), when it was saved, Edit and Discard; the body is the draft's
/// plain text (`stringValue`). A double click on the header is Edit. The
/// avatar on the timeline is the user's (`ConversationRow`).
@MainActor
final class ConversationDraftCardView: NSView {
    private static let border: CGFloat = 1
    private static let paddingH = ConversationMetrics.cardPaddingH - border
    private static let paddingV = ConversationMetrics.cardPaddingV - border

    /// Edit and Discard, wired by the conversation view.
    var onEdit: (@MainActor (Draft) -> Void)?
    var onDiscard: (@MainActor (Draft, NSWindow?) -> Void)?

    private(set) var card: Conversation.DraftCard
    private let header = NSStackView()
    private let senderLabel = NSTextField(labelWithString: "")
    private let badge = PillLabel()
    private let internalPill = PillLabel()
    private let savedLabel = NSTextField(labelWithString: "")
    private let editButton = NSButton(title: "", target: nil, action: nil)
    private let discardButton = NSButton(title: "", target: nil, action: nil)
    private let textLabel = NSTextField(wrappingLabelWithString: "")
    private var bodyFont: NSFont

    init(_ card: Conversation.DraftCard, bodyFont: NSFont) {
        self.card = card
        self.bodyFont = bodyFont
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        build()
        update(card)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    private func build() {
        wantsLayer = true
        layer?.cornerRadius = ConversationMetrics.cardRadius
        layer?.cornerCurve = .continuous
        layer?.borderWidth = Self.border

        senderLabel.font = Typo.bodyMedium
        senderLabel.textColor = .labelColor
        savedLabel.font = Typo.caption
        savedLabel.textColor = Tint.secondary
        for l in [senderLabel, savedLabel] {
            l.isSelectable = false
            l.lineBreakMode = .byTruncatingTail
            l.maximumNumberOfLines = 1
            l.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        }
        badge.font = Typo.caption
        badge.stringValue = Conversation.draftBadge()
        IssuePill.paint(badge, IssuePill.draftColours, emphasized: false)
        internalPill.font = Typo.caption
        IssuePill.paint(internalPill, IssuePill.internalColours, emphasized: false)
        internalPill.isHidden = true

        for (b, title, action) in [
            (editButton, Conversation.draftEditLabel(), #selector(editClicked(_:))),
            (discardButton, mn(Conversation.draftDiscardLabel()), #selector(discardClicked(_:))),
        ] {
            b.title = title
            b.bezelStyle = .push
            b.controlSize = .small
            b.font = Typo.caption
            b.target = self
            b.action = action
            b.setContentHuggingPriority(.required, for: .horizontal)
            b.setContentCompressionResistancePriority(.required, for: .horizontal)
        }

        let spacer = NSView()
        spacer.setContentHuggingPriority(.init(1), for: .horizontal)
        for v in [senderLabel, badge, internalPill, savedLabel, spacer, editButton, discardButton] as [NSView] {
            header.addArrangedSubview(v)
        }
        header.orientation = .horizontal
        header.alignment = .centerY
        header.spacing = 8
        header.setCustomSpacing(4, after: editButton)

        textLabel.font = bodyFont
        textLabel.textColor = .labelColor
        textLabel.isSelectable = true
        textLabel.allowsEditingTextAttributes = false
        textLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)

        let column = NSStackView(views: [header, textLabel])
        column.orientation = .vertical
        column.alignment = .leading
        column.spacing = 8
        column.edgeInsets = NSEdgeInsets(
            top: Self.paddingV + Self.border, left: Self.paddingH + Self.border, bottom: Self.paddingV + Self.border,
            right: Self.paddingH + Self.border)
        column.translatesAutoresizingMaskIntoConstraints = false
        addSubview(column)
        NSLayoutConstraint.activate([
            column.topAnchor.constraint(equalTo: topAnchor),
            column.bottomAnchor.constraint(equalTo: bottomAnchor),
            column.leadingAnchor.constraint(equalTo: leadingAnchor),
            column.trailingAnchor.constraint(equalTo: trailingAnchor),
            header.widthAnchor.constraint(
                equalTo: column.widthAnchor, constant: -2 * (Self.paddingH + Self.border)),
            textLabel.widthAnchor.constraint(equalTo: header.widthAnchor),
        ])

        // A double click on the header is Edit, as on a message card's
        // header (`headerDoubleClicked`); the buttons keep their clicks.
        let doubleClick = NSClickGestureRecognizer(target: self, action: #selector(headerDoubleClicked(_:)))
        doubleClick.numberOfClicksRequired = 2
        doubleClick.delaysPrimaryMouseButtonEvents = false
        header.addGestureRecognizer(doubleClick)

        setAccessibilityElement(true)
        setAccessibilityRole(.group)
    }

    override var wantsUpdateLayer: Bool { true }

    override func updateLayer() {
        layer?.backgroundColor = ConversationTint.cardFill.cgColor
        layer?.borderColor = ConversationTint.cardBorder(in: self).cgColor
    }

    /// Shows `card` (a newer version of the draft, or the same).
    func update(_ card: Conversation.DraftCard) {
        self.card = card
        senderLabel.stringValue = card.sender
        internalPill.stringValue = card.internalLabel
        internalPill.isHidden = !card.internal
        if card.draft.updatedAt <= .goZero {
            savedLabel.isHidden = true
        } else {
            savedLabel.stringValue = Conversation.draftSaved(formatTime(card.draft.updatedAt))
            savedLabel.isHidden = false
        }
        textLabel.stringValue = card.text
        textLabel.isHidden = card.text.isEmpty
        setAccessibilityLabel([card.sender, Conversation.draftBadge(), savedLabel.stringValue].filter { !$0.isEmpty }
            .joined(separator: ", "))
    }

    /// The text-zoom or monospace setting changed.
    func setBodyFont(_ font: NSFont) {
        bodyFont = font
        textLabel.font = font
    }

    @objc private func editClicked(_ sender: Any?) {
        onEdit?(card.draft)
    }

    @objc private func discardClicked(_ sender: Any?) {
        onDiscard?(card.draft, window)
    }

    @objc private func headerDoubleClicked(_ g: NSClickGestureRecognizer) {
        let p = g.location(in: header)
        for b in [editButton, discardButton] where b.frame.contains(p) {
            return
        }
        onEdit?(card.draft)
    }
}
