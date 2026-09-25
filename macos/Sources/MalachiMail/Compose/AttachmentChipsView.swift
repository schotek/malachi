// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The attachment chips of a compose window (compose.blp `attachments_box`,
/// compose.go `addChip`): a wrapping row of cards, each with the type icon
/// (a paperclip, or a picture for an inline image), the name, the size and
/// a remove button. Hidden while there is nothing to show. Names come from
/// the backend, sanitised, and are shown as plain text; the whole name is
/// the tooltip.
@MainActor
final class AttachmentChipsView: NSView {
    /// The remove button of a chip (`removeAttachment`).
    var onRemove: (@MainActor (String) -> Void)?

    private let flow = FlowView(spacing: 6, lineSpacing: 6)
    private var chips: [String: NSView] = [:]

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        addSubview(flow)
        NSLayoutConstraint.activate([
            flow.topAnchor.constraint(equalTo: topAnchor, constant: 6),
            bottomAnchor.constraint(equalTo: flow.bottomAnchor, constant: 6),
            flow.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 12),
            trailingAnchor.constraint(equalTo: flow.trailingAnchor, constant: 12),
        ])
        isHidden = true
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// The ids shown, in order.
    var ids: [String] {
        flow.views.compactMap { ($0 as? ChipView)?.attachmentID }
    }

    /// compose.go `addChip`.
    func add(_ a: DraftAttachment) {
        let chip = ChipView(attachment: a) { [weak self] id in
            self?.onRemove?(id)
        }
        flow.addView(chip)
        chips[a.id] = chip
        isHidden = false
    }

    /// Removes the chip of `id`, if shown; hides the row when it was the last.
    func remove(id: String) {
        guard let chip = chips.removeValue(forKey: id) else { return }
        flow.removeView(chip)
        isHidden = chips.isEmpty
    }

    func removeAll() {
        flow.removeAllViews()
        chips.removeAll()
        isHidden = true
    }
}

/// One chip: a card with the icon, the name (tail-elided at 24 characters),
/// the size and the ✕.
@MainActor
private final class ChipView: NSBox {
    static let nameChars = 24

    let attachmentID: String
    private let remove: @MainActor (String) -> Void

    init(attachment a: DraftAttachment, remove: @escaping @MainActor (String) -> Void) {
        attachmentID = a.id
        self.remove = remove
        super.init(frame: .zero)
        boxType = .custom
        titlePosition = .noTitle
        cornerRadius = 8
        borderWidth = 1
        borderColor = Tint.cardBorder
        fillColor = Tint.cardFill
        contentViewMargins = .zero

        let icon = NSImageView(image: Icon.image(a.inline ? "image-x-generic" : "mail-attachment", size: .regular))
        icon.contentTintColor = Tint.secondary
        icon.setContentHuggingPriority(.required, for: .horizontal)

        let name = NSTextField(labelWithString: Self.tailEllipsis(a.filename, max: Self.nameChars))
        name.font = Typo.body
        name.lineBreakMode = .byTruncatingTail
        name.toolTip = a.filename
        name.setContentCompressionResistancePriority(.required, for: .horizontal)

        let size = NSTextField(labelWithString: formatSize(a.size))
        size.font = Typo.caption
        size.textColor = Tint.secondary

        let close = NSButton(image: Icon.image("window-close", size: .small), target: self, action: #selector(removeClicked(_:)))
        close.isBordered = false
        close.imagePosition = .imageOnly
        close.toolTip = L10n.T("Remove")
        close.setAccessibilityLabel(L10n.T("Remove"))
        close.refusesFirstResponder = true

        let row = NSStackView(views: [icon, name, size, close])
        row.orientation = .horizontal
        row.spacing = 6
        row.alignment = .centerY
        row.edgeInsets = NSEdgeInsets(top: 2, left: 8, bottom: 2, right: 2)
        row.translatesAutoresizingMaskIntoConstraints = false
        guard let content = contentView else { return }
        content.addSubview(row)
        NSLayoutConstraint.activate([
            row.topAnchor.constraint(equalTo: content.topAnchor),
            row.bottomAnchor.constraint(equalTo: content.bottomAnchor),
            row.leadingAnchor.constraint(equalTo: content.leadingAnchor),
            row.trailingAnchor.constraint(equalTo: content.trailingAnchor),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    @objc private func removeClicked(_ sender: Any?) {
        remove(attachmentID)
    }

    /// Cuts `s` to at most `max` characters with a trailing ellipsis
    /// (Pango's EllipsizeEnd with `max-width-chars`, by characters).
    static func tailEllipsis(_ s: String, max: Int) -> String {
        let chars = Array(s)
        guard max >= 2, chars.count > max else { return s }
        return String(chars[..<(max - 1)]) + "\u{2026}"
    }
}
