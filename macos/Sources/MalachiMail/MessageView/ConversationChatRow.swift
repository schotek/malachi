// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// One bubble of the chat across the column (docs/plans/chat-view.md D4,
/// D5): the gap above it, the sender's avatar on the bubble's outer side
/// (leading for someone else's message, trailing for the user's own, top
/// aligned with the bubble), then the bubble at that edge, as wide as its
/// text up to `ConversationLayout.chatBubbleMax` of the column; the rest of
/// the row is the empty side. The avatar is decoration: the bubble names
/// its sender to accessibility.
@MainActor
final class ConversationChatRow: NSView {
    /// The member shown (nil for a draft's row).
    let id: MessageID?
    /// The draft shown (nil for a message's row).
    let draftID: DraftID?
    /// The bubble stands on the right: the user's own.
    let right: Bool
    let bubble: ConversationBubbleView
    private let avatar = AvatarView(size: ConversationMetrics.avatar)
    private var column: CGFloat = -1

    init(id: MessageID?, draftID: DraftID?, right: Bool, fonts: (normal: NSFont, monospace: NSFont)) {
        self.id = id
        self.draftID = draftID
        self.right = right
        bubble = ConversationBubbleView(mine: right, fonts: fonts)
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        avatar.setAccessibilityElement(false)
        avatar.translatesAutoresizingMaskIntoConstraints = false
        addSubview(avatar)
        addSubview(bubble)
        let gap = ConversationMetrics.gutterGap
        var constraints = [
            avatar.topAnchor.constraint(equalTo: topAnchor, constant: ConversationMetrics.itemGap),
            bottomAnchor.constraint(greaterThanOrEqualTo: avatar.bottomAnchor),
            bubble.topAnchor.constraint(equalTo: topAnchor, constant: ConversationMetrics.itemGap),
            bubble.bottomAnchor.constraint(equalTo: bottomAnchor),
        ]
        if right {
            constraints += [
                avatar.trailingAnchor.constraint(equalTo: trailingAnchor),
                bubble.trailingAnchor.constraint(equalTo: avatar.leadingAnchor, constant: -gap),
                bubble.leadingAnchor.constraint(greaterThanOrEqualTo: leadingAnchor),
            ]
        } else {
            constraints += [
                avatar.leadingAnchor.constraint(equalTo: leadingAnchor),
                bubble.leadingAnchor.constraint(equalTo: avatar.trailingAnchor, constant: gap),
                bubble.trailingAnchor.constraint(lessThanOrEqualTo: trailingAnchor),
            ]
        }
        NSLayoutConstraint.activate(constraints)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// The avatar of `name` (hostile text, only ever drawn as initials):
    /// monochrome as the list's setting says, the user's own tinted with
    /// the accent colour.
    /// `photo` is whose contact photo replaces the initials (F10); a bubble
    /// on the right, the user's own, never shows one.
    func showAvatar(name: String, monochrome: Bool, photo: ContactBook.Avatar? = nil) {
        avatar.photoSource = right ? nil : photo
        avatar.text = name
        avatar.monochrome = monochrome
        avatar.accent = right
    }

    /// The column is `width` wide (the clamp less its side insets): the
    /// bubble's widest.
    func setColumn(_ width: CGFloat) {
        guard width > 0, abs(width - column) >= 0.5 else { return }
        column = width
        bubble.setMaxWidth(CGFloat(ConversationLayout.chatBubbleMax(column: Double(width))))
    }

    override func layout() {
        setColumn(bounds.width)
        super.layout()
    }
}

/// A row of the chat without a bubble (D1): an issue's status or assignee
/// change, or the row of older messages left out, centred in the column no
/// wider than a bubble, in small secondary text: the lines of the change
/// (`Conversation.Item.eventLines`) and under them who made it and when
/// (the list's short date in a narrow pane, the full one in the tooltip),
/// as `ConversationEventRow` composes them; the row of older messages its
/// sentence. Plain text only. One accessibility element, as the event row
/// of the timeline.
@MainActor
final class ConversationChatCenterRow: NSView {
    private let lines = NSTextField(wrappingLabelWithString: "")
    private let meta = NSTextField(labelWithString: "")
    private var widthLimit: NSLayoutConstraint!
    private var item: Conversation.Item?
    private var column: CGFloat = -1

    /// The short date of the list instead of the full date and time.
    var compactDate = false {
        didSet {
            if compactDate != oldValue, let item {
                showEvent(item)
            }
        }
    }

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        for l in [lines, meta] {
            l.font = Typo.caption
            l.textColor = Tint.secondary
            l.isSelectable = false
            l.alignment = .center
        }
        lines.maximumNumberOfLines = 0
        meta.maximumNumberOfLines = 1
        meta.lineBreakMode = .byTruncatingMiddle
        meta.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        meta.isHidden = true
        let stack = NSStackView(views: [lines, meta])
        stack.orientation = .vertical
        stack.alignment = .centerX
        stack.spacing = 2
        // As narrow as its text, the name and date in full when they fit.
        stack.setHuggingPriority(NSLayoutConstraint.Priority(200), for: .horizontal)
        stack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(stack)
        widthLimit = stack.widthAnchor.constraint(lessThanOrEqualToConstant: 10_000)
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: topAnchor, constant: ConversationMetrics.itemGap),
            stack.bottomAnchor.constraint(equalTo: bottomAnchor),
            stack.centerXAnchor.constraint(equalTo: centerXAnchor),
            stack.leadingAnchor.constraint(greaterThanOrEqualTo: leadingAnchor),
            widthLimit,
        ])
        setAccessibilityElement(true)
        setAccessibilityRole(.group)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// An issue event (`item`, as the model has it now).
    func showEvent(_ item: Conversation.Item) {
        self.item = item
        lines.stringValue = item.eventLines.joined(separator: "\n")
        let full = item.message.map { $0.date.isGoZero ? "" : formatDateTime($0.date) } ?? ""
        let shortDate = item.message.map { formatDate($0.date, now: Date()) } ?? ""
        let when = compactDate && !full.isEmpty ? shortDate : full
        meta.stringValue = [item.sender, when].filter { !$0.isEmpty }.joined(separator: " · ")
        meta.toolTip = compactDate && !full.isEmpty ? full : nil
        meta.isHidden = meta.stringValue.isEmpty
        setAccessibilityLabel(item.eventText)
        setAccessibilityValue([item.sender, full].filter { !$0.isEmpty }.joined(separator: ", "))
    }

    /// The row of older messages: its sentence.
    func showText(_ text: String) {
        item = nil
        lines.stringValue = text
        meta.isHidden = true
        setAccessibilityLabel(text)
        setAccessibilityValue(nil)
    }

    /// The column is `width` wide: the row's text no wider than a bubble.
    func setColumn(_ width: CGFloat) {
        guard width > 0, abs(width - column) >= 0.5 else { return }
        column = width
        let w = max(CGFloat(ConversationLayout.chatBubbleMax(column: Double(width))), 1)
        widthLimit.constant = w
        lines.preferredMaxLayoutWidth = w
        lines.invalidateIntrinsicContentSize()
    }

    override func layout() {
        setColumn(bounds.width)
        super.layout()
    }
}
