// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// One folder row (folders.go `newFolderRow`): the role icon, the name with
/// the account under it for a pinned folder, the unread badge and the pin
/// star at the far right. Folder and account names are hostile input and go
/// through `stringValue` only.
@MainActor
final class FolderCellView: NSTableCellView {
    static let reuseIdentifier = NSUserInterfaceItemIdentifier("FolderCell")

    /// Runs when the star is clicked (favourites.go `toggleFavourite`).
    var onToggleFavourite: (() -> Void)?

    private let icon = NSImageView()
    private let title = NSTextField(labelWithString: "")
    private let subtitle = NSTextField(labelWithString: "")
    private let badge = NSTextField(labelWithString: "")
    private let star = NSButton()
    /// The star stays visible: a filled star in the tree is what says the
    /// folder is pinned (`.folder-star.starred` outside `.favourite`).
    private var starPinned = false
    /// The pointer or the selection is on the row.
    private var revealed = false

    init() {
        super.init(frame: .zero)
        identifier = FolderCellView.reuseIdentifier
        build()
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    private func build() {
        icon.imageScaling = .scaleNone
        icon.setContentHuggingPriority(.required, for: .horizontal)
        icon.setContentCompressionResistancePriority(.required, for: .horizontal)

        title.font = .systemFont(ofSize: SidebarMetrics.titleFontSize)
        title.lineBreakMode = .byTruncatingTail
        title.maximumNumberOfLines = 1
        title.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        title.setContentHuggingPriority(.defaultLow, for: .horizontal)
        // The outline adjusts the text field's colour with the selection.
        textField = title

        subtitle.font = .systemFont(ofSize: SidebarMetrics.captionFontSize)
        subtitle.textColor = .secondaryLabelColor
        subtitle.lineBreakMode = .byTruncatingTail
        subtitle.maximumNumberOfLines = 1
        subtitle.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        subtitle.setContentHuggingPriority(.defaultLow, for: .horizontal)

        let text = NSStackView(views: [title, subtitle])
        text.orientation = .vertical
        text.alignment = .leading
        text.spacing = 0
        text.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        text.setContentHuggingPriority(.defaultLow, for: .horizontal)

        badge.font = .monospacedDigitSystemFont(ofSize: SidebarMetrics.captionFontSize, weight: .regular)
        badge.textColor = .secondaryLabelColor
        badge.alignment = .right
        badge.setContentHuggingPriority(.required, for: .horizontal)
        badge.setContentCompressionResistancePriority(.required, for: .horizontal)

        star.isBordered = false
        star.setButtonType(.momentaryChange)
        star.imagePosition = .imageOnly
        star.imageScaling = .scaleNone
        star.refusesFirstResponder = true
        star.target = self
        star.action = #selector(starClicked)
        star.alphaValue = 0
        star.translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            star.widthAnchor.constraint(equalToConstant: SidebarMetrics.starSize),
            star.heightAnchor.constraint(equalToConstant: SidebarMetrics.starSize),
        ])

        let row = NSStackView(views: [icon, text, badge, star])
        row.orientation = .horizontal
        row.distribution = .fill
        text.setHuggingPriority(.defaultLow, for: .horizontal)
        text.setClippingResistancePriority(.defaultLow, for: .horizontal)
        icon.setContentHuggingPriority(.required, for: .horizontal)
        star.setContentHuggingPriority(.required, for: .horizontal)
        row.alignment = .centerY
        row.spacing = SidebarMetrics.cellSpacing
        row.translatesAutoresizingMaskIntoConstraints = false
        addSubview(row)
        NSLayoutConstraint.activate([
            row.leadingAnchor.constraint(equalTo: leadingAnchor),
            row.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -2),
            row.centerYAnchor.constraint(equalTo: centerYAnchor),
            row.heightAnchor.constraint(lessThanOrEqualTo: heightAnchor),
        ])
    }

    /// Fills the row for an entry. `subtitle` goes under the name (the
    /// account of a pinned folder when there are several).
    func configure(entry: FolderEntry, subtitle text: String) {
        guard let folder = entry.folder else { return }
        icon.image = SidebarIcons.image(roleIcon(folder.role), pointSize: SidebarMetrics.iconPointSize)
        title.stringValue = folderTitle(folder)
        subtitle.stringValue = text
        subtitle.isHidden = text.isEmpty
        setUnread(entry.badge)

        // A container that cannot be opened cannot be pinned either, and its
        // row is dimmed.
        let selectable = folder.selectable
        star.isHidden = !selectable
        title.textColor = selectable ? .labelColor : .tertiaryLabelColor
        icon.contentTintColor = selectable ? nil : .tertiaryLabelColor

        let starred = entry.starred
        star.image = SidebarIcons.image(
            starred ? "starred-symbolic" : "non-starred-symbolic", pointSize: SidebarMetrics.iconPointSize
        )
        star.toolTip = starred ? L10n.T("Remove from Favourites") : L10n.T("Add to Favourites")
        // In the Favourites section every row is pinned by definition, so a
        // column of stars would say nothing and the star waits there too.
        starPinned = starred && !entry.favourite
        applyStar(animated: false)
    }

    /// Shows `n` on the badge, hiding it at zero (folders.go `setUnread`).
    func setUnread(_ n: Int) {
        badge.isHidden = n <= 0
        badge.stringValue = String(n)
    }

    /// The pointer or the selection arrived on the row, or left it
    /// (`row.folder-row:hover`, `:focus-within`).
    func setStarRevealed(_ on: Bool) {
        guard on != revealed else { return }
        revealed = on
        applyStar(animated: true)
    }

    private func applyStar(animated: Bool) {
        let alpha: CGFloat = revealed || starPinned ? 1 : 0
        guard animated else {
            star.alphaValue = alpha
            return
        }
        NSAnimationContext.runAnimationGroup { context in
            context.duration = SidebarMetrics.starFade
            star.animator().alphaValue = alpha
        }
    }

    override func prepareForReuse() {
        super.prepareForReuse()
        revealed = false
        starPinned = false
        star.alphaValue = 0
        onToggleFavourite = nil
    }

    @objc private func starClicked() {
        onToggleFavourite?()
    }
}

/// A heading row (folders.go `newHeaderRow`): the Favourites section or an
/// account. The outline's source-list style draws it as a group row; the
/// text is plain.
@MainActor
final class SidebarHeaderCellView: NSTableCellView {
    static let reuseIdentifier = NSUserInterfaceItemIdentifier("SidebarHeaderCell")

    private let label = NSTextField(labelWithString: "")

    init() {
        super.init(frame: .zero)
        identifier = SidebarHeaderCellView.reuseIdentifier
        label.font = .systemFont(ofSize: 11, weight: .bold)
        label.textColor = .secondaryLabelColor
        label.lineBreakMode = .byTruncatingTail
        label.maximumNumberOfLines = 1
        label.translatesAutoresizingMaskIntoConstraints = false
        textField = label
        addSubview(label)
        NSLayoutConstraint.activate([
            label.leadingAnchor.constraint(equalTo: leadingAnchor),
            label.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -2),
            label.centerYAnchor.constraint(equalTo: centerYAnchor),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    func configure(text: String) {
        label.stringValue = text
    }
}

/// The row behind a folder cell: tracks the pointer so the cell can reveal
/// its star (`row.folder-row:hover`), and the selection likewise.
@MainActor
final class FolderRowView: NSTableRowView {
    private var tracking: NSTrackingArea?
    private var hovered = false {
        didSet { updateStar() }
    }

    override var isSelected: Bool {
        didSet { updateStar() }
    }

    override func updateTrackingAreas() {
        super.updateTrackingAreas()
        if let tracking {
            removeTrackingArea(tracking)
        }
        let area = NSTrackingArea(
            rect: bounds, options: [.mouseEnteredAndExited, .activeInKeyWindow, .inVisibleRect], owner: self, userInfo: nil
        )
        addTrackingArea(area)
        tracking = area
    }

    override func mouseEntered(with event: NSEvent) {
        super.mouseEntered(with: event)
        hovered = true
    }

    override func mouseExited(with event: NSEvent) {
        super.mouseExited(with: event)
        hovered = false
    }

    override func prepareForReuse() {
        super.prepareForReuse()
        hovered = false
    }

    private func updateStar() {
        for view in subviews {
            if let cell = view as? FolderCellView {
                cell.setStarRevealed(hovered || isSelected)
            }
        }
    }
}
