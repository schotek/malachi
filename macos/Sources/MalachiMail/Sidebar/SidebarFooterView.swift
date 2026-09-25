// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The sidebar's bottom box (window.blp lines 88–138): the sync line with
/// its spinner (sync.go `refreshSyncLabel`) over the connection line with
/// its icon (window.go `showConnectionState`). Both labels are captions in
/// the secondary colour, truncated at the end; the texts come from the core.
@MainActor
final class SidebarFooterView: NSView {
    private let spinner = NSProgressIndicator()
    private let syncLabel = NSTextField(labelWithString: "")
    private let connectionIcon = NSImageView()
    private let connectionLabel = NSTextField(labelWithString: "")

    init() {
        super.init(frame: .zero)
        spinner.style = .spinning
        spinner.controlSize = .small
        spinner.isDisplayedWhenStopped = false
        spinner.isHidden = true
        spinner.translatesAutoresizingMaskIntoConstraints = false

        connectionIcon.imageScaling = .scaleNone
        connectionIcon.contentTintColor = .secondaryLabelColor
        connectionIcon.translatesAutoresizingMaskIntoConstraints = false

        for label in [syncLabel, connectionLabel] {
            label.font = .preferredFont(forTextStyle: .caption1)
            label.textColor = .secondaryLabelColor
            label.lineBreakMode = .byTruncatingTail
            label.maximumNumberOfLines = 1
            label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
            label.setContentHuggingPriority(.defaultLow, for: .horizontal)
        }

        let syncLine = NSStackView(views: [spinner, syncLabel])
        let connectionLine = NSStackView(views: [connectionIcon, connectionLabel])
        for line in [syncLine, connectionLine] {
            line.orientation = .horizontal
            line.distribution = .fill
            line.alignment = .centerY
            line.spacing = SidebarMetrics.footerLineSpacing
        }

        let stack = NSStackView(views: [syncLine, connectionLine])
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = SidebarMetrics.footerSpacing
        stack.edgeInsets = SidebarMetrics.footerInsets
        stack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(stack)
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: topAnchor),
            stack.bottomAnchor.constraint(equalTo: bottomAnchor),
            stack.leadingAnchor.constraint(equalTo: leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: trailingAnchor),
            syncLine.widthAnchor.constraint(equalTo: stack.widthAnchor, constant: -(SidebarMetrics.footerInsets.left + SidebarMetrics.footerInsets.right)),
            connectionLine.widthAnchor.constraint(equalTo: syncLine.widthAnchor),
            spinner.widthAnchor.constraint(equalToConstant: SidebarMetrics.footerIconSize),
            spinner.heightAnchor.constraint(equalToConstant: SidebarMetrics.footerIconSize),
            connectionIcon.widthAnchor.constraint(equalToConstant: SidebarMetrics.footerIconSize),
            connectionIcon.heightAnchor.constraint(equalToConstant: SidebarMetrics.footerIconSize),
        ])
        show(connection: .connecting)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// The sync line: text and spinner (window.blp `sync_label`,
    /// `sync_spinner`).
    func show(_ footer: SyncController.FooterState) {
        syncLabel.stringValue = footer.text
        spinner.isHidden = !footer.spinning
        if footer.spinning {
            spinner.startAnimation(nil)
        } else {
            spinner.stopAnimation(nil)
        }
    }

    /// The connection line (window.blp `connection_icon`,
    /// `connection_status`), with the texts of `connectionStatusLine`.
    func show(connection state: ConnectionController.ConnectionState) {
        let line = connectionStatusLine(state)
        connectionIcon.image = SidebarIcons.image(line.icon, pointSize: SidebarMetrics.iconPointSize)
        connectionLabel.stringValue = line.text
    }
}
