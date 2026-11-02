// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The attachment chips of a compose window (compose.blp `attachments_box`,
/// compose.go `addChip`): a shaded band of tiles under the header fields,
/// each the same tile as under the headers of a message
/// (`AttachmentTileView`) with the type icon, the name, the size and a
/// remove button. Hidden while there is nothing to show. Names come from
/// the backend, sanitised, and are shown as plain text; the whole name is
/// the tooltip.
@MainActor
final class AttachmentChipsView: NSView {
    /// The remove button of a chip (`removeAttachment`).
    var onRemove: (@MainActor (String) -> Void)?
    /// A click on a chip: Quick Look of the attachment, the tile being the
    /// view the panel zooms out of. Nil leaves the tiles inert.
    var onPreview: (@MainActor (DraftAttachment, NSView) -> Void)?

    private let band = NSView()
    private let flow = FlowView(spacing: 8, lineSpacing: 8)
    private var chips: [String: NSView] = [:]

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        wantsLayer = true
        band.translatesAutoresizingMaskIntoConstraints = false
        band.wantsLayer = true
        band.layer?.cornerRadius = 8
        addSubview(band)
        band.addSubview(flow)
        NSLayoutConstraint.activate([
            band.topAnchor.constraint(equalTo: topAnchor, constant: 2),
            bottomAnchor.constraint(equalTo: band.bottomAnchor, constant: 6),
            band.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 12),
            trailingAnchor.constraint(equalTo: band.trailingAnchor, constant: 12),
            flow.topAnchor.constraint(equalTo: band.topAnchor, constant: 8),
            band.bottomAnchor.constraint(equalTo: flow.bottomAnchor, constant: 8),
            flow.leadingAnchor.constraint(equalTo: band.leadingAnchor, constant: 8),
            band.trailingAnchor.constraint(equalTo: flow.trailingAnchor, constant: 8),
        ])
        // The tiles are never squeezed by the editor below them: they
        // would be cut off at the bottom.
        setContentCompressionResistancePriority(.required, for: .vertical)
        flow.setContentCompressionResistancePriority(.required, for: .vertical)
        isHidden = true
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func updateLayer() {
        super.updateLayer()
        band.layer?.backgroundColor = NSColor.quaternarySystemFill.cgColor
    }

    override var wantsUpdateLayer: Bool { true }

    override func viewDidChangeEffectiveAppearance() {
        super.viewDidChangeEffectiveAppearance()
        needsDisplay = true
    }

    /// The ids shown, in order.
    var ids: [String] {
        flow.views.compactMap { ($0 as? ComposeTileView)?.attachmentID }
    }

    /// compose.go `addChip`.
    func add(_ a: DraftAttachment) {
        let chip = ComposeTileView(attachment: a) { [weak self] id in
            self?.onRemove?(id)
        }
        if onPreview != nil {
            chip.onClick = { [weak self, weak chip] in
                guard let self, let chip else { return }
                self.onPreview?(a, chip)
            }
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

/// One attachment of a compose window: the tile with the ✕.
@MainActor
private final class ComposeTileView: AttachmentTileView {
    let attachmentID: String
    private let remove: @MainActor (String) -> Void

    init(attachment a: DraftAttachment, remove: @escaping @MainActor (String) -> Void) {
        attachmentID = a.id
        self.remove = remove
        let close = NSButton(image: Icon.image("window-close", size: .small), target: nil, action: nil)
        close.toolTip = L10n.T("Remove")
        close.setAccessibilityLabel(L10n.T("Remove"))
        super.init(icon: Self.icon(filename: a.filename, contentType: a.contentType),
                   name: a.filename, size: formatSize(a.size), accessory: close)
        close.target = self
        close.action = #selector(removeClicked(_:))
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    @objc private func removeClicked(_ sender: Any?) {
        remove(attachmentID)
    }
}
