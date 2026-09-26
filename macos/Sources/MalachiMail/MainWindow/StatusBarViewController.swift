// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The status bar across the bottom of the main window: the counterpart of
/// window.blp `status_button` (a spinner, the connection icon and one line
/// for the state of every account) and of sync.go `refreshSyncLabel`. The
/// line is a borderless button that opens the status popover
/// (`StatusPopoverViewController`, window.blp `status_popover`) with each
/// account's state and action and the daemon at the foot.
///
/// The sync controller computes everything (`SyncController.line`, sync.go
/// `syncStatusText` under status.go `statusLineFor`); this view shows it.
/// Where GTK has the line at the bottom of the sidebar, it spans the window
/// here so that it stays in sight with the sidebar folded away (the
/// deviation table in macos/README.md). The actions of the popover's rows
/// go to the app (`onAction`, `onShowOutbox`), which owns the sign-in, the
/// account assistant and the selection.
@MainActor
final class StatusBarViewController: NSViewController, NSPopoverDelegate {
    /// The bar's height, its 1 pt top line included.
    static let height: CGFloat = 26
    /// The inset of the line from the window's edges: the Blueprint's
    /// margin of 6 plus the button's own padding of 6 (style.go
    /// `menubutton.status-line > button`).
    static let inset: CGFloat = 12
    /// Between the spinner, the icon and the text (window.blp `spacing: 8`).
    static let spacing: CGFloat = 8

    let sync: SyncController
    let mailbox: MailboxController

    /// The action of an account's row (status.go `onStatusAction`), run
    /// after the popover closed: a dialog, the browser or the list it leads
    /// to takes over.
    var onAction: (@MainActor (AccountStatus) -> Void)?
    /// The row of an account's unsent messages: its outbox (status.go
    /// `showOutbox`), after the popover closed.
    var onShowOutbox: (@MainActor (AccountID) -> Void)?

    private let spinner = Spinner(size: 16)
    private let connectionIcon = NSImageView()
    private let statusButton = NSButton(title: "", target: nil, action: nil)
    private let popover = NSPopover()
    private let content = StatusPopoverViewController()
    /// When a click on the line closed the transient popover on its
    /// mouse-down, the time of that mouse-down: the click's own action must
    /// not open the popover again.
    private var closedByLineClick: TimeInterval?

    init(sync: SyncController, mailbox: MailboxController) {
        self.sync = sync
        self.mailbox = mailbox
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    // MARK: View

    override func loadView() {
        let bar = StatusBarView()

        // The top line between the panes and the bar.
        let separator = NSBox()
        separator.boxType = .separator
        separator.translatesAutoresizingMaskIntoConstraints = false

        spinner.isHidden = true

        connectionIcon.imageScaling = .scaleNone
        connectionIcon.contentTintColor = .secondaryLabelColor
        connectionIcon.isHidden = true
        // The line beside it says the same; VoiceOver would read the GTK
        // icon name.
        connectionIcon.setAccessibilityElement(false)
        connectionIcon.translatesAutoresizingMaskIntoConstraints = false

        // The line itself: a text that reads as a caption and opens the
        // popover. A screen reader reads the line, not the tooltip.
        statusButton.isBordered = false
        statusButton.imagePosition = .noImage
        statusButton.alignment = .left
        statusButton.lineBreakMode = .byTruncatingTail
        statusButton.font = Typo.caption
        statusButton.contentTintColor = .secondaryLabelColor
        // window.blp's tooltip of the status line.
        statusButton.toolTip = L10n.T("Sync Status")
        statusButton.target = self
        statusButton.action = #selector(toggleStatusPopover(_:))
        statusButton.isEnabled = false
        statusButton.setContentHuggingPriority(.defaultLow, for: .horizontal)
        statusButton.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)

        let row = NSStackView(views: [spinner, connectionIcon, statusButton])
        row.orientation = .horizontal
        row.alignment = .centerY
        row.distribution = .fill
        row.spacing = Self.spacing
        row.translatesAutoresizingMaskIntoConstraints = false

        bar.addSubview(separator)
        bar.addSubview(row)
        NSLayoutConstraint.activate([
            separator.topAnchor.constraint(equalTo: bar.topAnchor),
            separator.leadingAnchor.constraint(equalTo: bar.leadingAnchor),
            separator.trailingAnchor.constraint(equalTo: bar.trailingAnchor),
            row.topAnchor.constraint(equalTo: separator.bottomAnchor),
            row.bottomAnchor.constraint(equalTo: bar.bottomAnchor),
            row.leadingAnchor.constraint(equalTo: bar.leadingAnchor, constant: Self.inset),
            row.trailingAnchor.constraint(equalTo: bar.trailingAnchor, constant: -Self.inset),
            connectionIcon.widthAnchor.constraint(equalToConstant: 16),
            connectionIcon.heightAnchor.constraint(equalToConstant: 16),
        ])
        view = bar

        popover.behavior = .transient
        popover.contentViewController = content
        popover.delegate = self
        content.onAction = { [weak self] st in
            guard let self else { return }
            self.popover.performClose(nil)
            self.onAction?(st)
        }
        content.onShowOutbox = { [weak self] acc in
            guard let self else { return }
            self.popover.performClose(nil)
            self.onShowOutbox?(acc)
        }
    }

    override func viewDidLoad() {
        super.viewDidLoad()
        sync.onStatusLine = { [weak self] line in self?.show(line) }
        // What the controller holds already, for a bar attached late.
        show(sync.line)
    }

    // MARK: Line

    /// Shows the line (sync.go `refreshSyncLabel`): the text, the spinner,
    /// the connection icon while there is no connection, the foot of the
    /// popover. Without a connection the line cannot be clicked, and an open
    /// popover closes; while it is open its rows follow every change.
    private func show(_ line: StatusLine) {
        statusButton.title = line.text
        statusButton.setAccessibilityLabel(line.text)
        spinner.isHidden = !line.spinning
        if line.spinning {
            spinner.start()
        } else {
            spinner.stop()
        }
        if !line.icon.isEmpty {
            connectionIcon.image = Icon.image(line.icon, size: .regular)
        }
        connectionIcon.isHidden = line.icon.isEmpty
        if !line.active, popover.isShown {
            popover.performClose(nil)
        }
        statusButton.isEnabled = line.active
        content.setDaemon(line.daemon)
        if popover.isShown {
            refreshStatusPopover()
        }
    }

    /// Brings the popover's rows up to date (status.go
    /// `refreshStatusPopover`), as it opens and while it is open.
    private func refreshStatusPopover() {
        let mailbox = mailbox
        content.update(sync.accountStatuses()) { mailbox.model.outboxKey($0) != nil }
    }

    // MARK: Popover

    @objc private func toggleStatusPopover(_ sender: Any?) {
        if let closed = closedByLineClick {
            closedByLineClick = nil
            if let event = NSApp.currentEvent, event.type == .leftMouseUp, event.timestamp - closed < 2 {
                return
            }
        }
        if popover.isShown {
            popover.performClose(sender)
            return
        }
        refreshStatusPopover()
        // Above the line, as the GTK menu button opens upwards. The edge is
        // in the button's coordinates, and a flipped view's top is minY.
        popover.show(relativeTo: statusButton.bounds, of: statusButton,
                     preferredEdge: statusButton.isFlipped ? .minY : .maxY)
    }

    /// A transient popover closes on the mouse-down of any click outside
    /// it; when that click is on the line, its mouse-up would open the
    /// popover again. The time of such a mouse-down is noted here.
    func popoverWillClose(_ notification: Notification) {
        closedByLineClick = nil
        guard let event = NSApp.currentEvent, event.type == .leftMouseDown,
              event.window === statusButton.window else { return }
        let point = statusButton.convert(event.locationInWindow, from: nil)
        if statusButton.bounds.contains(point) {
            closedByLineClick = event.timestamp
        }
    }
}

/// The bar's background: the window's, so the line reads as part of the
/// frame under the panes.
@MainActor
private final class StatusBarView: NSView {
    override func draw(_ dirtyRect: NSRect) {
        NSColor.windowBackgroundColor.setFill()
        dirtyRect.fill()
    }
}
