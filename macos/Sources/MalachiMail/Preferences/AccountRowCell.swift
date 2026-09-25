// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// An image that lets clicks through to the table row below it, so a press
/// on the drag handle reaches the table and can start the drag.
@MainActor
private final class PrefsPassThroughImageView: NSImageView {
    override func hitTest(_ point: NSPoint) -> NSView? {
        nil
    }
}

/// One account of the Accounts page (accounts_page.go `accountRow`): the
/// drag handle, the provider icon, name and address, the status, the
/// pause switch and the edit and remove buttons. Everything shown comes
/// from the daemon, so nothing is markup.
@MainActor
final class AccountRowCell: NSTableCellView, PrefsGroupMember {
    static let reuseIdentifier = NSUserInterfaceItemIdentifier("AccountRowCell")

    private let handle = PrefsPassThroughImageView()
    private let icon = PrefsPassThroughImageView()
    private let titleLabel = NSTextField(labelWithString: "")
    private let subtitleLabel = NSTextField(labelWithString: "")
    private let statusLabel = NSTextField(labelWithString: "")
    private let toggle = NSSwitch()
    private let editButton: NSButton
    private let removeButton: NSButton
    private var reverting = false
    private var rowEnabled = true
    private var groupEnabled = true

    var onToggle: ((Bool) -> Void)?
    var onEdit: (() -> Void)?
    var onRemove: (() -> Void)?

    /// The handle's frame in the cell's coordinates, for the drag test.
    var handleFrame: NSRect { handle.frame.insetBy(dx: -8, dy: -12) }

    override init(frame: NSRect) {
        editButton = NSButton(image: wizardSymbol("pencil", pointSize: 14), target: nil, action: nil)
        removeButton = NSButton(image: wizardSymbol("trash", pointSize: 14), target: nil, action: nil)
        super.init(frame: frame)
        identifier = AccountRowCell.reuseIdentifier

        handle.image = wizardSymbol("line.3.horizontal", pointSize: 14)
        handle.contentTintColor = NSColor.labelColor.withAlphaComponent(0.55)
        handle.imageScaling = .scaleNone
        handle.toolTip = "Drag to reorder, or press \u{2325}\u{2318}\u{2191} and \u{2325}\u{2318}\u{2193}" // macOS-only string
        icon.image = wizardSymbol("envelope", pointSize: 15)
        icon.contentTintColor = .labelColor
        icon.imageScaling = .scaleNone

        titleLabel.font = .systemFont(ofSize: 13)
        titleLabel.lineBreakMode = .byTruncatingTail
        subtitleLabel.font = .systemFont(ofSize: 11)
        subtitleLabel.textColor = .secondaryLabelColor
        subtitleLabel.lineBreakMode = .byTruncatingTail
        statusLabel.font = .preferredFont(forTextStyle: .caption1)
        statusLabel.textColor = .secondaryLabelColor
        statusLabel.lineBreakMode = .byTruncatingTail

        toggle.controlSize = .regular
        toggle.toolTip = L10n.T("Enabled")
        toggle.target = self
        toggle.action = #selector(toggled(_:))
        editButton.isBordered = false
        editButton.toolTip = L10n.T("Edit Account")
        editButton.target = self
        editButton.action = #selector(editClicked(_:))
        removeButton.isBordered = false
        removeButton.toolTip = L10n.T("Remove Account")
        removeButton.target = self
        removeButton.action = #selector(removeClicked(_:))
        for button in [editButton, removeButton] {
            button.imagePosition = .imageOnly
            button.contentTintColor = .labelColor
            button.widthAnchor.constraint(equalToConstant: 28).isActive = true
            button.heightAnchor.constraint(equalToConstant: 28).isActive = true
        }

        // The text column takes the width between the icon and the trailing
        // cluster; the labels truncate rather than push the cluster out.
        let text = prefsColumn(spacing: 2)
        prefsAddFilling(titleLabel, to: text)
        prefsAddFilling(subtitleLabel, to: text)
        for label in [titleLabel, subtitleLabel] {
            label.setContentHuggingPriority(.defaultLow, for: .horizontal)
            label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        }
        for v in [handle, icon, statusLabel, toggle, editButton, removeButton] as [NSView] {
            v.translatesAutoresizingMaskIntoConstraints = false
            v.setContentHuggingPriority(.required, for: .horizontal)
            v.setContentCompressionResistancePriority(.required, for: .horizontal)
        }

        // Status, switch and the two buttons, packed at the trailing edge;
        // a hidden status label leaves the layout.
        let cluster = NSStackView(views: [statusLabel, toggle, editButton, removeButton])
        cluster.orientation = .horizontal
        cluster.alignment = .centerY
        cluster.spacing = 12
        cluster.setCustomSpacing(6, after: toggle)
        cluster.setCustomSpacing(2, after: editButton)
        cluster.setHuggingPriority(.required, for: .horizontal)
        cluster.translatesAutoresizingMaskIntoConstraints = false

        addSubview(handle)
        addSubview(icon)
        addSubview(text)
        addSubview(cluster)
        NSLayoutConstraint.activate([
            handle.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 12),
            handle.centerYAnchor.constraint(equalTo: centerYAnchor),
            icon.leadingAnchor.constraint(equalTo: handle.trailingAnchor, constant: 12),
            icon.centerYAnchor.constraint(equalTo: centerYAnchor),
            text.leadingAnchor.constraint(equalTo: icon.trailingAnchor, constant: 12),
            text.centerYAnchor.constraint(equalTo: centerYAnchor),
            text.trailingAnchor.constraint(equalTo: cluster.leadingAnchor, constant: -12),
            cluster.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -12),
            cluster.centerYAnchor.constraint(equalTo: centerYAnchor),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Shows the account's current state (accounts_page.go `accountRow.apply`).
    func apply(_ a: Account) {
        titleLabel.stringValue = accountRowTitle(a)
        subtitleLabel.stringValue = a.config.email
        reverting = true
        toggle.state = a.enabled ? .on : .off
        reverting = false
        let status = accountStatusText(a.state.status)
        statusLabel.stringValue = status
        statusLabel.isHidden = status.isEmpty
        toolTip = a.config.email
    }

    /// The row's own sensitivity (while its call runs).
    func setRowEnabled(_ enabled: Bool) {
        rowEnabled = enabled
        applyEnabled()
    }

    func setGroupEnabled(_ enabled: Bool) {
        groupEnabled = enabled
        applyEnabled()
    }

    private func applyEnabled() {
        let on = rowEnabled && groupEnabled
        toggle.isEnabled = on
        editButton.isEnabled = on
        removeButton.isEnabled = on
        alphaValue = on ? 1 : 0.5
    }

    @objc private func toggled(_ sender: Any?) {
        guard !reverting else { return }
        onToggle?(toggle.state == .on)
    }

    @objc private func editClicked(_ sender: Any?) {
        onEdit?()
    }

    @objc private func removeClicked(_ sender: Any?) {
        onRemove?()
    }
}
