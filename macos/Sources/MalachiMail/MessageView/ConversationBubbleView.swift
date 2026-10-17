// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// A bubble of the chat layout (docs/plans/chat-view.md D3, D6, D14, D15):
/// a message of the conversation, or the user's draft, as plain text only.
/// The header has the unread dot (only of someone else's message), the
/// sender, a paperclip with the number of attachments once message.get told
/// it (the paperclip alone before), the Draft and Internal badges of a
/// draft, the Internal badge, who relayed it ("via …") and Edited of an
/// issue's comment, and at the trailing end the date (the list's short form
/// in a narrow pane, the full one in the tooltip) or, for a draft, when it
/// was saved. Under it the text: the summary's snippet, dimmed, until the
/// body is there (`Conversation.bubbleBody`), then the message's text
/// without its quoted history, or the draft's.
///
/// Every text is plain (`stringValue`) and none can be selected: a double
/// click anywhere in the bubble opens the message in its own window, a
/// draft in the compose window (`onDoubleClick`), where everything else is.
/// The bubble is a card on the pane's grey (a hairline, rounded corners);
/// the user's own is tinted with the accent colour and has a border of it
/// (`ConversationTint.mineFill`). It hugs its text, never wider than the
/// row allows (`setMaxWidth`, `ConversationLayout.chatBubbleMax`) and never
/// narrower than its header, whose sender is truncated first when the row
/// is too narrow for it. The bubble is one accessibility group named by its
/// sender and date, as a card is.
@MainActor
final class ConversationBubbleView: NSView {
    /// The user's own message or draft.
    let mine: Bool
    /// A double click anywhere in the bubble.
    var onDoubleClick: (@MainActor () -> Void)?

    /// The short date of the list instead of the full date and time (a
    /// narrow pane, `ConversationLayout.compactDates`).
    var compactDate = false {
        didSet {
            if compactDate != oldValue {
                renderDate()
            }
        }
    }

    private let header = NSStackView()
    private let unread = UnreadDot()
    private let senderLabel = NSTextField(labelWithString: "")
    private let clipIcon = NSImageView()
    private let clipCount = NSTextField(labelWithString: "")
    private let draftPill = PillLabel()
    private let internalPill = PillLabel()
    private let viaLabel = NSTextField(labelWithString: "")
    private let editedLabel = NSTextField(labelWithString: "")
    private let dateLabel = NSTextField(labelWithString: "")
    private let bodyLabel = NSTextField(wrappingLabelWithString: "")
    private var widthLimit: NSLayoutConstraint!

    /// What the header names for accessibility and the date it shows.
    private var sender = ""
    private var date: Date?
    /// A draft's "Draft saved …" in the date's place.
    private var saved: String?
    /// The text shows a body (not the placeholder): a let-go entry keeps
    /// it (`showBody`).
    private(set) var showsText = false
    private var shownBody: Conversation.BubbleBody?
    private var fonts: (normal: NSFont, monospace: NSFont)
    /// The text shows in the monospaced font.
    private var monospace = false

    private static let border: CGFloat = 1
    /// How strongly the bubble keeps to the width of its content: below
    /// every part's resistance to compression (the sender's is the
    /// lowest), so it shrinks to the text and the whole header, and only
    /// the row's limit (`setMaxWidth`) truncates the sender.
    private static let hugging = NSLayoutConstraint.Priority(200)
    /// The padding inside the bubble, from its edge.
    static let paddingH: CGFloat = 13
    static let paddingV: CGFloat = 9

    /// `fonts`: the body's font, and the monospaced one for a plain-text
    /// body (`Conversation.bubbleMonospace`).
    init(mine: Bool, fonts: (normal: NSFont, monospace: NSFont)) {
        self.mine = mine
        self.fonts = fonts
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        build(fonts.normal)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    private func build(_ bodyFont: NSFont) {
        wantsLayer = true
        layer?.cornerRadius = ConversationMetrics.cardRadius
        layer?.cornerCurve = .continuous
        layer?.borderWidth = Self.border

        senderLabel.font = Typo.bodyMedium
        senderLabel.textColor = .labelColor
        dateLabel.font = Typo.caption
        dateLabel.textColor = Tint.secondary
        for l in [viaLabel, editedLabel, clipCount] {
            l.font = Typo.caption
            l.textColor = Tint.secondary
        }
        for l in [senderLabel, viaLabel, editedLabel, dateLabel, clipCount] {
            l.isSelectable = false
            l.lineBreakMode = .byTruncatingTail
            l.maximumNumberOfLines = 1
        }
        // The sender gives way first, then the badges; the date last.
        senderLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        for l in [viaLabel, editedLabel] {
            l.setContentCompressionResistancePriority(NSLayoutConstraint.Priority(300), for: .horizontal)
        }
        dateLabel.setContentCompressionResistancePriority(NSLayoutConstraint.Priority(749), for: .horizontal)
        dateLabel.setContentHuggingPriority(.required, for: .horizontal)

        clipIcon.image = NSImage(systemSymbolName: "paperclip", accessibilityDescription: nil)?
            .withSymbolConfiguration(NSImage.SymbolConfiguration(pointSize: Typo.captionSize, weight: .regular))
        clipIcon.contentTintColor = Tint.secondary
        clipIcon.setAccessibilityElement(true)
        clipIcon.setAccessibilityRole(.image)
        clipIcon.setAccessibilityLabel(L10n.T("Has attachments"))
        for v in [clipIcon, clipCount] as [NSView] {
            v.setContentHuggingPriority(.required, for: .horizontal)
            v.isHidden = true
        }

        draftPill.font = Typo.caption
        draftPill.stringValue = Conversation.draftBadge()
        IssuePill.paint(draftPill, IssuePill.draftColours, emphasized: false)
        draftPill.isHidden = true
        internalPill.font = Typo.caption
        IssuePill.paint(internalPill, IssuePill.internalColours, emphasized: false)
        internalPill.isHidden = true
        for p in [draftPill, internalPill] {
            p.setContentCompressionResistancePriority(NSLayoutConstraint.Priority(600), for: .horizontal)
        }
        unread.isHidden = true

        let spacer = NSView()
        spacer.translatesAutoresizingMaskIntoConstraints = false
        spacer.setContentHuggingPriority(.init(1), for: .horizontal)
        spacer.setContentCompressionResistancePriority(.init(1), for: .horizontal)
        for v in [
            unread, senderLabel, clipIcon, clipCount, draftPill, internalPill, viaLabel, editedLabel, spacer, dateLabel,
        ] as [NSView] {
            header.addArrangedSubview(v)
        }
        header.orientation = .horizontal
        header.alignment = .centerY
        header.spacing = 6
        header.setCustomSpacing(2, after: clipIcon)
        header.setCustomSpacing(12, after: spacer)
        header.setHuggingPriority(Self.hugging, for: .horizontal)

        bodyLabel.font = bodyFont
        bodyLabel.textColor = .labelColor
        bodyLabel.isSelectable = false
        bodyLabel.maximumNumberOfLines = 0
        bodyLabel.lineBreakMode = .byWordWrapping
        bodyLabel.isHidden = true

        let column = NSStackView(views: [header, bodyLabel])
        column.orientation = .vertical
        column.alignment = .leading
        column.spacing = 6
        column.edgeInsets = NSEdgeInsets(
            top: Self.paddingV, left: Self.paddingH, bottom: Self.paddingV, right: Self.paddingH)
        column.setHuggingPriority(Self.hugging, for: .horizontal)
        column.translatesAutoresizingMaskIntoConstraints = false
        addSubview(column)
        widthLimit = widthAnchor.constraint(lessThanOrEqualToConstant: 10_000)
        NSLayoutConstraint.activate([
            column.topAnchor.constraint(equalTo: topAnchor),
            column.bottomAnchor.constraint(equalTo: bottomAnchor),
            column.leadingAnchor.constraint(equalTo: leadingAnchor),
            column.trailingAnchor.constraint(equalTo: trailingAnchor),
            header.widthAnchor.constraint(equalTo: column.widthAnchor, constant: -2 * Self.paddingH),
            widthLimit,
        ])

        // A double click anywhere opens; a single click does nothing and
        // is not held back.
        let doubleClick = NSClickGestureRecognizer(target: self, action: #selector(doubleClicked(_:)))
        doubleClick.numberOfClicksRequired = 2
        doubleClick.delaysPrimaryMouseButtonEvents = false
        addGestureRecognizer(doubleClick)

        setAccessibilityElement(true)
        setAccessibilityRole(.group)
    }

    override var wantsUpdateLayer: Bool { true }

    override func updateLayer() {
        if mine {
            layer?.backgroundColor = ConversationTint.mineFill(in: self).cgColor
            var border = ConversationTint.mineBorder.cgColor
            effectiveAppearance.performAsCurrentDrawingAppearance {
                border = ConversationTint.mineBorder.cgColor
            }
            layer?.borderColor = border
        } else {
            layer?.backgroundColor = ConversationTint.cardFill.cgColor
            layer?.borderColor = ConversationTint.cardBorder(in: self).cgColor
        }
    }

    /// The widest the bubble may be (`ConversationLayout.chatBubbleMax`):
    /// the text wraps inside it.
    func setMaxWidth(_ width: CGFloat) {
        let w = max(width, 2 * Self.paddingH + 1)
        guard widthLimit.constant != w else { return }
        widthLimit.constant = w
        bodyLabel.preferredMaxLayoutWidth = w - 2 * Self.paddingH
        bodyLabel.invalidateIntrinsicContentSize()
    }

    // MARK: Content

    /// The header of a message (`item`, as the model has it now);
    /// `attachments` is how many the message has once message.get told it
    /// (nil: not yet, the paperclip shows alone).
    func showMessage(_ item: Conversation.Item, attachments: Int?) {
        sender = item.sender
        senderLabel.stringValue = item.sender
        senderLabel.toolTip = item.sender.isEmpty ? nil : item.sender
        unread.isHidden = !item.unread || mine
        let clip = item.message?.hasAttachments == true
        clipIcon.isHidden = !clip
        if clip, let n = attachments, n > 0 {
            clipCount.stringValue = String(n)
            clipCount.isHidden = false
        } else {
            clipCount.isHidden = true
        }
        draftPill.isHidden = true
        internalPill.stringValue = item.internalLabel
        internalPill.isHidden = !item.internal
        viaLabel.stringValue = item.via
        viaLabel.toolTip = item.via.isEmpty ? nil : item.via
        viaLabel.isHidden = item.via.isEmpty
        editedLabel.stringValue = item.edited
        editedLabel.isHidden = item.edited.isEmpty
        date = item.message.map(\.date)
        saved = nil
        renderDate()
    }

    /// A draft of the user's (D14): its header with the Draft badge, the
    /// Internal one of an internal comment and when it was saved, its text.
    func showDraft(_ card: Conversation.DraftCard) {
        sender = card.sender
        senderLabel.stringValue = card.sender
        senderLabel.toolTip = card.sender.isEmpty ? nil : card.sender
        unread.isHidden = true
        clipIcon.isHidden = true
        clipCount.isHidden = true
        draftPill.isHidden = false
        internalPill.stringValue = card.internalLabel
        internalPill.isHidden = !card.internal
        viaLabel.isHidden = true
        editedLabel.isHidden = true
        date = nil
        saved = card.draft.updatedAt <= .goZero ? "" : Conversation.draftSaved(formatTime(card.draft.updatedAt))
        renderDate()
        showBody(.text(card.text), monospace: draftMonospace)
    }

    /// A draft's text follows the setting for plain text, as the
    /// timeline's draft card does.
    var draftMonospace = false

    /// The text under the header: a placeholder (dimmed), or the text. An
    /// empty one hides the label. A placeholder does not replace a text
    /// shown already (the controller let go of the body, or fetches it
    /// again: the bubble keeps what it read). `monospace` picks the
    /// monospaced font for the text (never for a placeholder).
    func showBody(_ body: Conversation.BubbleBody, monospace: Bool) {
        if case .placeholder = body, showsText {
            return
        }
        var mono = monospace
        if case .placeholder = body {
            mono = false
        }
        if mono != self.monospace {
            self.monospace = mono
            bodyLabel.font = mono ? fonts.monospace : fonts.normal
            bodyLabel.invalidateIntrinsicContentSize()
        }
        guard body != shownBody else { return }
        shownBody = body
        switch body {
        case .placeholder(let s):
            showsText = false
            bodyLabel.stringValue = s
            bodyLabel.textColor = Tint.secondary
            bodyLabel.isHidden = s.isEmpty
        case .text(let s):
            showsText = true
            bodyLabel.stringValue = s
            bodyLabel.textColor = .labelColor
            bodyLabel.isHidden = s.isEmpty
        }
    }

    /// The text shows in the monospaced font.
    var isMonospace: Bool { monospace }

    /// The monospace setting changed: the text in the font it asks for
    /// (never a placeholder's).
    func setMonospace(_ on: Bool) {
        let mono = on && showsText
        guard mono != monospace else { return }
        monospace = mono
        bodyLabel.font = mono ? fonts.monospace : fonts.normal
        bodyLabel.invalidateIntrinsicContentSize()
    }

    /// The text-zoom setting changed: both fonts anew, the text in the one
    /// it shows in.
    func setFonts(normal: NSFont, monospace: NSFont) {
        fonts = (normal, monospace)
        bodyLabel.font = self.monospace ? monospace : normal
        bodyLabel.invalidateIntrinsicContentSize()
    }

    /// The date (or a draft's saved time) at the header's end, and the
    /// accessibility name: the sender and the full date.
    private func renderDate() {
        if let saved {
            dateLabel.stringValue = saved
            dateLabel.toolTip = nil
            dateLabel.isHidden = saved.isEmpty
            setAccessibilityLabel([sender, Conversation.draftBadge(), saved].filter { !$0.isEmpty }
                .joined(separator: ", "))
            return
        }
        let full = date.map { $0.isGoZero ? "" : formatDateTime($0) } ?? ""
        dateLabel.stringValue = compactDate && !full.isEmpty ? date.map { formatDate($0, now: Date()) } ?? "" : full
        dateLabel.toolTip = compactDate && !full.isEmpty ? full : nil
        dateLabel.isHidden = full.isEmpty
        setAccessibilityLabel([sender, full].filter { !$0.isEmpty }.joined(separator: ", "))
    }

    @objc private func doubleClicked(_ g: NSClickGestureRecognizer) {
        guard g.state == .ended else { return }
        onDoubleClick?()
    }
}
