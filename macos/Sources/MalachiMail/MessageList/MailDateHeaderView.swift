// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// A plain, keyboard-accessible disclosure button spanning the section header.
@MainActor
final class MailDateHeaderView: NSTableCellView {
    static let reuseIdentifier = NSUserInterfaceItemIdentifier("MailDateHeader")
    private let button = NSButton()
    var onToggle: (() -> Void)?

    init() {
        super.init(frame: .zero)
        identifier = Self.reuseIdentifier
        button.isBordered = false
        button.alignment = .left
        button.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
        button.contentTintColor = .secondaryLabelColor
        button.imagePosition = .imageLeading
        button.target = self
        button.action = #selector(toggle)
        button.translatesAutoresizingMaskIntoConstraints = false
        addSubview(button)
        NSLayoutConstraint.activate([
            button.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 12),
            button.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -12),
            button.topAnchor.constraint(equalTo: topAnchor, constant: 4),
            button.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -4),
            heightAnchor.constraint(equalToConstant: 30),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) { fatalError("not used") }

    func configure(_ group: MailDateGroup, collapsed: Bool) {
        button.title = group.title
        button.image = NSImage(systemSymbolName: collapsed ? "chevron.right" : "chevron.down", accessibilityDescription: nil)
        button.setAccessibilityLabel(group.title)
        button.setAccessibilityValue(collapsed ? L10n.T("Collapsed") : L10n.T("Expanded"))
    }

    @objc private func toggle() { onToggle?() }
}
