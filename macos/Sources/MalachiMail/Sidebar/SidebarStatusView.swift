// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// The sidebar's status page (window.blp `folder_status_page`, an
/// Adw.StatusPage): an icon, a title and a description, centred, for the
/// times there is nothing to list. Private to the sidebar and sized for its
/// width; the app shell's shared `StatusPageView` may replace it.
@MainActor
final class SidebarStatusView: NSView {
    private let icon = NSImageView()
    private let title = NSTextField(wrappingLabelWithString: "")
    private let details = NSTextField(wrappingLabelWithString: "")

    init() {
        super.init(frame: .zero)
        icon.imageScaling = .scaleNone
        icon.contentTintColor = .secondaryLabelColor

        title.font = .systemFont(ofSize: 15, weight: .bold)
        title.alignment = .center
        title.isSelectable = false

        details.font = .systemFont(ofSize: 12)
        details.textColor = .secondaryLabelColor
        details.alignment = .center
        details.isSelectable = false

        let stack = NSStackView(views: [icon, title, details])
        stack.orientation = .vertical
        stack.alignment = .centerX
        stack.spacing = 12
        stack.edgeInsets = NSEdgeInsets(top: 24, left: 12, bottom: 24, right: 12)
        stack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(stack)
        NSLayoutConstraint.activate([
            stack.centerXAnchor.constraint(equalTo: centerXAnchor),
            stack.centerYAnchor.constraint(equalTo: centerYAnchor),
            stack.leadingAnchor.constraint(greaterThanOrEqualTo: leadingAnchor),
            stack.trailingAnchor.constraint(lessThanOrEqualTo: trailingAnchor),
            stack.widthAnchor.constraint(lessThanOrEqualToConstant: 280),
            title.widthAnchor.constraint(equalTo: stack.widthAnchor, constant: -24),
            details.widthAnchor.constraint(equalTo: stack.widthAnchor, constant: -24),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Fills the page (folders.go `setStatusPage`). The icon is a GTK name,
    /// empty for none; every text is plain.
    func show(icon name: String, title text: String, description: String) {
        icon.image = SidebarIcons.image(name, pointSize: 40, weight: .light)
        icon.isHidden = icon.image == nil
        title.stringValue = text
        details.stringValue = description
        details.isHidden = description.isEmpty
    }
}
