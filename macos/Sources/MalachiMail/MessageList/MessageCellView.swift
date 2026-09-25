// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The list settings a row is laid out with (messages.go
/// `applyRowAppearance`, style.go "monochrome-avatars").
struct RowAppearance: Equatable {
    var compact: Bool
    var showPreview: Bool
    var showAvatar: Bool
    var monochromeAvatars: Bool

    @MainActor
    init(settings: Settings) {
        compact = settings.density == .compact
        showPreview = settings.showPreviewLine
        showAvatar = settings.showAvatars
        monochromeAvatars = settings.monochromeAvatars
    }
}

/// The geometry of a row (widget/message_row.go constants, message_row.blp
/// and style.go `.thread-twisty`, `.thread-count`, `.unread-dot`).
enum RowMetrics {
    /// The start margin of a plain row, and the tighter one of the rows of
    /// a grouped list, where the fold arrow (or its kept place) sits in
    /// front of the avatar.
    static let rowMarginStart: CGFloat = 6
    static let threadRowMarginStart: CGFloat = 2
    static let rowMarginEnd: CGFloat = 6
    /// The gap between the arrow and the avatar (`lead_box` spacing).
    static let leadSpacing: CGFloat = 4
    /// The indent of a member row when no avatar is shown.
    static let memberIndent: CGFloat = 24
    static let avatarComfortable: CGFloat = 40
    static let avatarCompact: CGFloat = 28
    static let marginComfortable: CGFloat = 8
    static let marginCompact: CGFloat = 3
    /// `content_box` spacing, the column's spacing, the first line's.
    static let contentSpacing: CGFloat = 12
    static let columnSpacing: CGFloat = 2
    static let lineSpacing: CGFloat = 6
    /// `button.thread-twisty { min-width: 20px; min-height: 20px }`.
    static let expanderSize: CGFloat = 20
    /// `.unread-dot`: 8 × 8, radius 4.
    static let unreadDotSize: CGFloat = 8
    /// `label.thread-count { padding: 0 6px }`.
    static let badgePadding: CGFloat = 6
}

/// One row of the message list (message_row.blp, widget/message_row.go): the
/// fold arrow or spinner and the avatar in front, then the sender line with
/// the badge, the icons, the date and the unread dot, the subject and the
/// preview. Every string from the mail goes through `stringValue` only.
@MainActor
final class MessageCellView: NSTableCellView {
    static let reuseIdentifier = NSUserInterfaceItemIdentifier("MessageCell")

    /// Runs when the fold arrow is clicked (threads.go `toggleThread`).
    var onToggle: (@MainActor () -> Void)?

    private let expander = NSButton()
    private let spinner = Spinner(size: 16)
    private let avatar = AvatarView(size: RowMetrics.avatarComfortable)
    private let from = NSTextField(labelWithString: "")
    private let badge = PillLabel()
    private let attachment = NSImageView()
    private let star = NSImageView()
    private let date = NSTextField(labelWithString: "")
    private let unreadDot = DotView()
    private let subject = NSTextField(labelWithString: "")
    private let preview = NSTextField(labelWithString: "")
    private let content = NSStackView()

    private var leadingConstraint: NSLayoutConstraint!
    private var topConstraint: NSLayoutConstraint!
    private var bottomConstraint: NSLayoutConstraint!

    /// `thread` says the row shows a conversation (the arrow is live),
    /// `loading` that its members are on their way (the spinner instead),
    /// `reserve` that a plain row keeps the arrow's place so every avatar
    /// of a grouped list lines up, `member` that the row is indented under
    /// its conversation (without an avatar of its own).
    private var thread = false
    private var loading = false
    private var reserve = false
    private var member = false
    private var showAvatar = true
    private var avatarSize = RowMetrics.avatarComfortable

    init() {
        super.init(frame: .zero)
        identifier = MessageCellView.reuseIdentifier
        build()
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    private func build() {
        expander.isBordered = false
        expander.setButtonType(.momentaryChange)
        expander.imagePosition = .imageOnly
        expander.imageScaling = .scaleNone
        expander.refusesFirstResponder = true
        expander.target = self
        expander.action = #selector(expanderClicked)
        expander.image = Icon.image("pan-end", size: .small)
        expander.translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            expander.widthAnchor.constraint(equalToConstant: RowMetrics.expanderSize),
            expander.heightAnchor.constraint(equalToConstant: RowMetrics.expanderSize),
        ])

        let lead = NSStackView(views: [expander, spinner, avatar])
        lead.orientation = .horizontal
        lead.alignment = .centerY
        lead.spacing = RowMetrics.leadSpacing
        lead.setContentHuggingPriority(.required, for: .horizontal)
        lead.setContentCompressionResistancePriority(.required, for: .horizontal)

        for label in [from, subject, preview] {
            label.lineBreakMode = .byTruncatingTail
            label.maximumNumberOfLines = 1
            label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
            label.setContentHuggingPriority(.defaultLow, for: .horizontal)
            label.isSelectable = false
        }
        from.font = Typo.body
        subject.font = Typo.body
        preview.font = Typo.caption
        preview.textColor = Tint.secondary

        badge.font = Typo.captionNumeric
        badge.textColor = .labelColor
        badge.isHidden = true

        attachment.image = Icon.image("mail-attachment", size: .small)
        attachment.contentTintColor = Tint.secondary
        attachment.imageScaling = .scaleNone
        attachment.isHidden = true
        star.image = Icon.image("starred", size: .small)
        star.contentTintColor = Tint.accent
        star.imageScaling = .scaleNone
        star.isHidden = true
        for icon in [attachment, star] {
            icon.setContentHuggingPriority(.required, for: .horizontal)
            icon.setContentCompressionResistancePriority(.required, for: .horizontal)
        }

        date.font = Typo.caption
        date.textColor = Tint.secondary
        date.alignment = .right
        date.isSelectable = false
        date.setContentHuggingPriority(.required, for: .horizontal)
        date.setContentCompressionResistancePriority(.required, for: .horizontal)
        unreadDot.isHidden = true

        let line = NSStackView(views: [from, badge, attachment, star, date, unreadDot])
        line.orientation = .horizontal
        line.alignment = .centerY
        line.spacing = RowMetrics.lineSpacing

        let column = FillStackView(fillingViews: [line, subject, preview])
        column.spacing = RowMetrics.columnSpacing
        column.setContentHuggingPriority(.defaultLow, for: .horizontal)
        column.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)

        content.addArrangedSubview(lead)
        content.addArrangedSubview(column)
        content.orientation = .horizontal
        content.alignment = .top
        content.spacing = RowMetrics.contentSpacing
        content.translatesAutoresizingMaskIntoConstraints = false
        addSubview(content)
        leadingConstraint = content.leadingAnchor.constraint(equalTo: leadingAnchor, constant: RowMetrics.rowMarginStart)
        topConstraint = content.topAnchor.constraint(equalTo: topAnchor, constant: RowMetrics.marginComfortable)
        bottomConstraint = bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: RowMetrics.marginComfortable)
        NSLayoutConstraint.activate([
            leadingConstraint,
            topConstraint,
            bottomConstraint,
            trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: RowMetrics.rowMarginEnd),
        ])
    }

    // MARK: Content

    /// Shows `row` with the current settings (threads.go `renderRow`,
    /// messages.go `newMessageRow`): a conversation row with its arrow and
    /// badge, or a message; in a grouped list a plain row keeps the arrow's
    /// place so the avatars line up.
    func configure(_ row: ListRow, reserveExpander: Bool, appearance: RowAppearance) {
        applyAppearance(appearance)
        if row.thread, let summary = row.summary {
            setThread(summaryThread(summary, expanded: row.expanded, loading: row.loading))
        } else {
            setMessage(summaryMessage(row.message))
            reserve = reserveExpander
            applyLead()
        }
        setMember(row.member)
    }

    /// Displays a message (message_row.go `SetMessage`). The first sender
    /// is shown; a message without one gets an empty name.
    private func setMessage(_ m: RowMessage) {
        let sender = m.from.first ?? Address(address: "")
        let name = displayName(sender)
        avatar.text = name
        from.stringValue = name
        from.toolTip = formatAddress(sender)
        fill(subject: m.subject, snippet: m.snippet, date: m.date, unread: m.unread, flagged: m.flagged, attachments: m.hasAttachments)
        badge.isHidden = true
        thread = false
        loading = false
        applyLead()
    }

    /// Displays a conversation (message_row.go `SetThread`): the
    /// participants where the sender goes, the member count in a badge,
    /// the fold arrow (or the spinner while the members load).
    private func setThread(_ t: RowThread) {
        let first = t.participants.first ?? Address(address: "")
        avatar.text = displayName(first)
        from.stringValue = formatParticipants(t.participants)
        from.toolTip = t.participants.map(formatAddress).joined(separator: "\n")
        fill(subject: t.subject, snippet: t.snippet, date: t.date, unread: t.unread > 0, flagged: t.flagged, attachments: t.hasAttachments)
        badge.stringValue = threadCountText(t.count)
        // TRANSLATORS: tooltip of the member count of a conversation row.
        badge.toolTip = L10n.N("%d message", "%d messages", t.count)
        badge.isHidden = t.count < 2
        thread = true
        loading = t.loading
        reserve = false
        applyLead()
        setExpanded(t.expanded)
    }

    /// The parts a message and a conversation row share (message_row.go
    /// `fill`).
    private func fill(subject text: String, snippet: String, date when: Date, unread: Bool, flagged: Bool, attachments: Bool) {
        date.stringValue = formatDate(when, now: Date())
        subject.stringValue = subjectText(text)
        preview.stringValue = snippet
        attachment.isHidden = !attachments
        star.isHidden = !flagged
        unreadDot.isHidden = !unread
        let font = unread ? Typo.heading : Typo.body
        from.font = font
        subject.font = font
    }

    /// Turns the fold arrow of a conversation row (message_row.go
    /// `SetExpanded`).
    private func setExpanded(_ on: Bool) {
        expander.image = Icon.image(on ? "pan-down" : "pan-end", size: .small)
        expander.toolTip = on ? L10n.T("Collapse") : L10n.T("Expand")
    }

    /// Indents the row as a member of an unfolded conversation
    /// (message_row.go `SetMember`).
    private func setMember(_ on: Bool) {
        member = on
        applyLead()
    }

    /// Lays the start of the row out (message_row.go `applyLead`): on a
    /// conversation row the arrow (or the spinner while loading), on a
    /// plain row of a grouped list the arrow's place kept empty (invisible
    /// and inert, so a click reaches the row), on a flat list's row
    /// nothing. A member row shows no avatar; it is indented so that its
    /// text lines up with its conversation's (the avatar's width and gap),
    /// or by a fixed step when avatars are off.
    private func applyLead() {
        let live = thread && !loading
        let spinning = thread && loading
        spinner.isHidden = !spinning
        if spinning {
            spinner.start()
        } else {
            spinner.stop()
        }
        expander.isHidden = !(live || (!thread && reserve))
        expander.isEnabled = live
        expander.alphaValue = live ? 1 : 0
        if !live {
            expander.toolTip = nil
        }
        avatar.isHidden = !(showAvatar && !member)
        var margin = RowMetrics.rowMarginStart
        if thread || reserve {
            margin = RowMetrics.threadRowMarginStart
        }
        if member {
            margin += showAvatar ? avatarSize + RowMetrics.leadSpacing : RowMetrics.memberIndent
        }
        leadingConstraint.constant = margin
    }

    /// The density, the preview line, the avatars (message_row.go
    /// `SetCompact`, `SetShowPreview`, `SetShowAvatar`).
    private func applyAppearance(_ a: RowAppearance) {
        let margin = a.compact ? RowMetrics.marginCompact : RowMetrics.marginComfortable
        avatarSize = a.compact ? RowMetrics.avatarCompact : RowMetrics.avatarComfortable
        topConstraint.constant = margin
        bottomConstraint.constant = margin
        avatar.size = avatarSize
        avatar.monochrome = a.monochromeAvatars
        preview.isHidden = !a.showPreview
        showAvatar = a.showAvatar
    }

    /// The row's colours follow the selection: white on the accent
    /// highlight, the label colours otherwise.
    override var backgroundStyle: NSView.BackgroundStyle {
        didSet {
            let emphasized = backgroundStyle == .emphasized
            let primary: NSColor = emphasized ? .alternateSelectedControlTextColor : .labelColor
            let secondary: NSColor = emphasized ? .alternateSelectedControlTextColor : Tint.secondary
            from.textColor = primary
            subject.textColor = primary
            date.textColor = secondary
            preview.textColor = secondary
            attachment.contentTintColor = secondary
            star.contentTintColor = emphasized ? .alternateSelectedControlTextColor : Tint.accent
            unreadDot.color = emphasized ? .alternateSelectedControlTextColor : Tint.accent
            badge.textColor = primary
            badge.fill = emphasized ? NSColor.alternateSelectedControlTextColor.withAlphaComponent(0.2) : Tint.fg(alpha: 0.1)
            expander.contentTintColor = emphasized ? .alternateSelectedControlTextColor : nil
        }
    }

    override func prepareForReuse() {
        super.prepareForReuse()
        onToggle = nil
    }

    @objc private func expanderClicked() {
        onToggle?()
    }
}

/// `label.thread-count`: a caption in a capsule, padded 0/6
/// (style.go).
@MainActor
final class PillLabel: NSTextField {
    var fill: NSColor = Tint.fg(alpha: 0.1) {
        didSet { needsDisplay = true }
    }

    init() {
        super.init(frame: .zero)
        isEditable = false
        isSelectable = false
        isBezeled = false
        drawsBackground = false
        lineBreakMode = .byClipping
        maximumNumberOfLines = 1
        alignment = .center
        setContentHuggingPriority(.required, for: .horizontal)
        setContentCompressionResistancePriority(.required, for: .horizontal)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var intrinsicContentSize: NSSize {
        var s = super.intrinsicContentSize
        s.width += 2 * RowMetrics.badgePadding
        s.height += 2
        return s
    }

    override func draw(_ dirtyRect: NSRect) {
        let radius = bounds.height / 2
        fill.setFill()
        NSBezierPath(roundedRect: bounds, xRadius: radius, yRadius: radius).fill()
        super.draw(dirtyRect)
    }
}

/// `.unread-dot`: an 8 × 8 disc in the accent colour (style.go).
@MainActor
final class DotView: NSView {
    var color: NSColor = Tint.accent {
        didSet { needsDisplay = true }
    }

    init() {
        super.init(frame: NSRect(x: 0, y: 0, width: RowMetrics.unreadDotSize, height: RowMetrics.unreadDotSize))
        translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            widthAnchor.constraint(equalToConstant: RowMetrics.unreadDotSize),
            heightAnchor.constraint(equalToConstant: RowMetrics.unreadDotSize),
        ])
        setContentHuggingPriority(.required, for: .horizontal)
        setContentCompressionResistancePriority(.required, for: .horizontal)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var intrinsicContentSize: NSSize {
        NSSize(width: RowMetrics.unreadDotSize, height: RowMetrics.unreadDotSize)
    }

    override func draw(_ dirtyRect: NSRect) {
        color.setFill()
        NSBezierPath(ovalIn: bounds).fill()
    }
}
