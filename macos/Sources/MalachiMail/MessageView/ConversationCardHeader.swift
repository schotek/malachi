// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The header of a card of the conversation view: the fold arrow of the
/// card that opened the conversation (conversation_card.go `setFold`;
/// hidden on the others), an unread dot, the sender, the disclosure of the
/// recipients, who relayed the comment ("via …"), the Internal and Edited
/// badges of an issue's comment, and at the trailing edge the date, with
/// Reply (Comment on an issue), Reply All and Forward before it while the
/// pointer is over the card or one of them has the focus.
///
/// The parts are placed by `ConversationHeaderLayout` for the width the
/// card has (the sender is truncated, the badges go to a second line, the
/// buttons take their room only while they show), so the header's height
/// follows its width, as a `FlowView`'s does. The layout is one of
/// alignment rectangles, as Auto Layout's is: a label's text starts where
/// its place does, in line with the body under it. Every text is plain
/// (`stringValue`); the names come from the message.
@MainActor
final class ConversationCardHeader: NSView {
    let foldButton = CardFoldButton()
    let unread = UnreadDot()
    let senderLabel = NSTextField(labelWithString: "")
    let disclosure = NSButton()
    let viaLabel = NSTextField(labelWithString: "")
    let internalPill = PillLabel()
    let editedLabel = NSTextField(labelWithString: "")
    let dateLabel = NSTextField(labelWithString: "")
    let replyButton = CardActionButton()
    let replyAllButton = CardActionButton()
    let forwardButton = CardActionButton()

    /// The hover buttons show.
    var buttonsShown = false {
        didSet {
            guard buttonsShown != oldValue else { return }
            buttonsBox.alphaValue = buttonsShown ? 1 : 0
            needsLayout = true
        }
    }

    /// One of the hover buttons has the keyboard focus.
    var buttonsFocused: Bool {
        guard let fr = window?.firstResponder as? NSView else { return false }
        return fr.isDescendant(of: buttonsBox)
    }

    private let buttons = NSStackView()
    private let buttonsBox = CardButtonsBox()
    private var lastWidth: CGFloat = -1

    /// Between the two lines.
    private static let lineSpacing: CGFloat = 4
    /// The first line is at least as tall as a hover button.
    private static let minLineHeight: CGFloat = 20

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        setContentHuggingPriority(.defaultLow, for: .horizontal)
        setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        setContentHuggingPriority(.required, for: .vertical)
        setContentCompressionResistancePriority(.required, for: .vertical)

        senderLabel.font = .systemFont(ofSize: 13, weight: .medium)
        senderLabel.textColor = .labelColor
        dateLabel.font = Typo.caption
        dateLabel.textColor = Tint.secondary
        for l in [viaLabel, editedLabel] {
            l.font = Typo.caption
            l.textColor = Tint.secondary
        }
        for l in [senderLabel, viaLabel, editedLabel, dateLabel] {
            l.isSelectable = false
            l.lineBreakMode = .byTruncatingTail
            l.maximumNumberOfLines = 1
        }
        internalPill.font = Typo.caption
        IssuePill.paint(internalPill, IssuePill.internalColours, emphasized: false)

        foldButton.isHidden = true

        disclosure.bezelStyle = .disclosure
        disclosure.setButtonType(.pushOnPushOff)
        disclosure.title = ""
        disclosure.state = .off

        buttons.orientation = .horizontal
        buttons.spacing = 2
        buttons.translatesAutoresizingMaskIntoConstraints = false
        for b in [replyButton, replyAllButton, forwardButton] {
            buttons.addArrangedSubview(b)
        }
        buttonsBox.addSubview(buttons)
        NSLayoutConstraint.activate([
            buttons.leadingAnchor.constraint(equalTo: buttonsBox.leadingAnchor),
            buttons.centerYAnchor.constraint(equalTo: buttonsBox.centerYAnchor),
        ])
        buttonsBox.alphaValue = 0

        // The buttons last: they lie over what they cover.
        for v in [foldButton, unread, senderLabel, disclosure, viaLabel, internalPill, editedLabel, dateLabel, buttonsBox] as [NSView] {
            v.translatesAutoresizingMaskIntoConstraints = true
            addSubview(v)
        }
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// A text, or which parts show, changed.
    func contentChanged() {
        invalidateIntrinsicContentSize()
        needsLayout = true
    }

    // MARK: Layout

    override var isFlipped: Bool { true }

    override var intrinsicContentSize: NSSize {
        NSSize(width: NSView.noIntrinsicMetric, height: arrange(width: bounds.width, place: false))
    }

    override func setFrameSize(_ newSize: NSSize) {
        super.setFrameSize(newSize)
        if newSize.width != lastWidth {
            lastWidth = newSize.width
            invalidateIntrinsicContentSize()
            needsLayout = true
        }
    }

    override func layout() {
        super.layout()
        arrange(width: bounds.width, place: true)
    }

    private var badgeViews: [NSView] { [viaLabel, internalPill, editedLabel] }

    private func naturalWidth(_ v: NSView) -> Double {
        v.isHidden ? 0 : Double(ceil(v.intrinsicContentSize.width))
    }

    private func naturalHeight(_ v: NSView) -> CGFloat {
        ceil(v.intrinsicContentSize.height)
    }

    private var anyButton: Bool {
        !replyButton.isHidden || !replyAllButton.isHidden || !forwardButton.isHidden
    }

    /// Places the parts for `width` (when `place` is set) and returns the
    /// header's height.
    @discardableResult
    private func arrange(width: CGFloat, place: Bool) -> CGFloat {
        let buttonsSize = anyButton ? buttons.fittingSize : .zero
        let parts = ConversationHeaderLayout.Parts(
            fold: naturalWidth(foldButton), dot: naturalWidth(unread),
            sender: senderLabel.stringValue.isEmpty ? 0 : naturalWidth(senderLabel),
            disclosure: naturalWidth(disclosure), badges: badgeViews.map(naturalWidth), date: naturalWidth(dateLabel),
            buttons: Double(ceil(buttonsSize.width)))
        let plan = ConversationHeaderLayout(parts, width: Double(width), buttonsShown: buttonsShown)

        let first = max(Self.minLineHeight, naturalHeight(senderLabel), naturalHeight(dateLabel))
        let second = plan.lines > 1 ? badgeViews.filter { !$0.isHidden }.map(naturalHeight).max() ?? 0 : 0
        let height = first + (second > 0 ? Self.lineSpacing + second : 0)
        guard place else { return height }

        func put(_ v: NSView, _ slot: ConversationHeaderLayout.Slot?, height h: CGFloat? = nil) {
            guard let slot, slot.width > 0 else {
                // Out of the way; whether the part shows is the card's to say.
                v.frame = NSRect(x: 0, y: 0, width: 0, height: 0)
                return
            }
            let h = h ?? naturalHeight(v)
            let line = slot.line == 0 ? (y: CGFloat(0), height: first) : (y: first + Self.lineSpacing, height: second)
            var r = NSRect(x: CGFloat(slot.x), y: line.y + (line.height - h) / 2, width: CGFloat(slot.width), height: h)
            if userInterfaceLayoutDirection == .rightToLeft {
                r.origin.x = width - r.maxX
            }
            v.frame = v.frame(forAlignmentRect: backingAlignedRect(r, options: .alignAllEdgesNearest))
        }
        put(foldButton, plan.fold)
        put(unread, plan.dot)
        put(senderLabel, plan.sender)
        put(disclosure, plan.disclosure)
        for (v, slot) in zip(badgeViews, plan.badges) {
            put(v, slot)
        }
        put(dateLabel, plan.date)
        put(buttonsBox, plan.buttons, height: max(ceil(buttonsSize.height), Self.minLineHeight))
        return height
    }
}

/// The unread mark before a card's sender.
@MainActor
final class UnreadDot: NSView {
    static let size: CGFloat = 8

    init() {
        super.init(frame: NSRect(x: 0, y: 0, width: Self.size, height: Self.size))
        wantsLayer = true
        layer?.cornerRadius = Self.size / 2
        setAccessibilityElement(false)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var intrinsicContentSize: NSSize {
        NSSize(width: Self.size, height: Self.size)
    }

    override var wantsUpdateLayer: Bool { true }

    override func updateLayer() {
        layer?.backgroundColor = NSColor.controlAccentColor.cgColor
    }
}

/// The fold arrow of the card that opened the conversation: borderless, a
/// chevron that points to the side while the card is folded and down while
/// it is open, as the arrow of a conversation row in the list.
@MainActor
final class CardFoldButton: NSButton {
    private static let size = NSSize(width: 16, height: 20)

    init() {
        super.init(frame: NSRect(origin: .zero, size: Self.size))
        isBordered = false
        bezelStyle = .inline
        imagePosition = .imageOnly
        imageScaling = .scaleNone
        title = ""
        setButtonType(.momentaryChange)
        contentTintColor = Tint.secondary
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var intrinsicContentSize: NSSize { Self.size }
}

/// What the hover buttons of a card lie on: the card's own background, so
/// that at a narrow width they cover the badges behind them cleanly.
@MainActor
private final class CardButtonsBox: NSView {
    init() {
        super.init(frame: .zero)
        wantsLayer = true
        layer?.cornerRadius = 5
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var wantsUpdateLayer: Bool { true }

    override func updateLayer() {
        layer?.backgroundColor = ConversationTint.cardFill.cgColor
    }
}

/// A hover button of a card: borderless, a symbol, reachable by Tab; the
/// card shows its buttons while one of them has the focus.
@MainActor
final class CardActionButton: NSButton {
    var onFocus: (@MainActor (Bool) -> Void)?

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        isBordered = false
        bezelStyle = .inline
        imagePosition = .imageOnly
        title = ""
        setButtonType(.momentaryChange)
        setContentHuggingPriority(.required, for: .horizontal)
        NSLayoutConstraint.activate([
            widthAnchor.constraint(equalToConstant: 24),
            heightAnchor.constraint(equalToConstant: 20),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func becomeFirstResponder() -> Bool {
        let ok = super.becomeFirstResponder()
        if ok {
            onFocus?(true)
        }
        return ok
    }

    override func resignFirstResponder() -> Bool {
        let ok = super.resignFirstResponder()
        if ok {
            onFocus?(false)
        }
        return ok
    }
}
