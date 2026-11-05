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
/// Like GTK's (window.blp `status_bar`), it spans the window so that it
/// stays in sight with the sidebar folded away. The actions of the popover's rows
/// go to the app (`onAction`, `onShowOutbox`), which owns the sign-in, the
/// account assistant and the selection.
///
/// At the bar's trailing end a second, quieter line may follow (`setNote`):
/// the board's triage (its progress, when the assistant last refined the
/// board, why automatic triage pauses), which the main window sets for its
/// mode. It is never the sync line's: it gives way first when the bar is
/// too narrow for both (lower compression resistance), and is hidden when
/// empty. macOS-only, like the board.
///
/// At the very end, Report an Issue (window.blp) opens the website's
/// report form, the About panel's link (`AppLinks.reportIssue`); it keeps
/// its width, the note and then the line give way.
///
/// In the Calendar mode (`setCalendar`) the line's text is the calendars'
/// state ("Calendars synced 2 min ago", an error, a sign-in needed) and
/// the note leads with the unanswered invitations; the spinner, the
/// connection icon and the popover (the accounts, the Outbox) stay the
/// sync line's.
@MainActor
final class StatusBarViewController: NSViewController {
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
    /// The board's triage line at the trailing end (`setNote`).
    private let noteLabel = NSTextField(labelWithString: "")
    /// Report an Issue at the trailing end.
    private let reportButton = NSButton(title: "", target: nil, action: nil)
    /// More AI Features: only in the App Store build.
    private let websiteButton = NSButton(title: "", target: nil, action: nil)
    private let content = StatusPopoverViewController()
    /// The popover's window: the Windows client's flyout, without an arrow
    /// (`StatusFlyout`).
    private lazy var popover = StatusFlyout(content: content)
    /// When a click on the line closed the flyout on its mouse-down, the time of that mouse-down: the click's own action must
    /// not open the popover again.
    private var closedByLineClick: TimeInterval?
    /// The calendars' state line while the window shows the Calendar, nil
    /// otherwise (`setCalendar`).
    private var calendarLine: String?
    /// The calendar's part of the note (the unanswered count) and the
    /// triage's (`setNote`).
    private var calendarNote = ""
    private var triageNote = ""

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

        noteLabel.font = Typo.caption
        noteLabel.textColor = .secondaryLabelColor
        noteLabel.alignment = .right
        noteLabel.lineBreakMode = .byTruncatingTail
        noteLabel.maximumNumberOfLines = 1
        noteLabel.isHidden = noteLabel.stringValue.isEmpty
        noteLabel.setContentHuggingPriority(.defaultHigh, for: .horizontal)
        // Below the sync line's: the note truncates first.
        noteLabel.setContentCompressionResistancePriority(NSLayoutConstraint.Priority(rawValue: 200), for: .horizontal)

        // window.blp's Report an Issue.
        reportButton.isBordered = false
        reportButton.title = L10n.T("Report an Issue")
        reportButton.image = Icon.image("malachi-report-symbolic", size: .small)
        reportButton.imagePosition = .imageLeading
        reportButton.imageHugsTitle = true
        reportButton.font = Typo.caption
        reportButton.contentTintColor = .secondaryLabelColor
        reportButton.target = self
        reportButton.action = #selector(reportIssue(_:))
        reportButton.setContentHuggingPriority(.required, for: .horizontal)
        reportButton.setContentCompressionResistancePriority(.defaultHigh, for: .horizontal)

        // The App Store build's pointer at the version from the website,
        // styled like Report an Issue.
        websiteButton.isBordered = false
        websiteButton.title = Assistant.websiteFeaturesTexts().statusLink
        websiteButton.image = Icon.image("malachi-assistant-symbolic", size: .small)
        websiteButton.imagePosition = .imageLeading
        websiteButton.imageHugsTitle = true
        websiteButton.font = Typo.caption
        websiteButton.contentTintColor = .secondaryLabelColor
        websiteButton.target = self
        websiteButton.action = #selector(showWebsiteFeatures(_:))
        websiteButton.setContentHuggingPriority(.required, for: .horizontal)
        websiteButton.setContentCompressionResistancePriority(.defaultHigh, for: .horizontal)
        websiteButton.isHidden = !WebsiteFeaturesDialog.shown

        let row = NSStackView(views: [spinner, connectionIcon, statusButton, noteLabel, websiteButton, reportButton])
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

        popover.onWillClose = { [weak self] event in self?.flyoutWillClose(event) }
        content.onAction = { [weak self] st in
            guard let self else { return }
            self.popover.close()
            self.onAction?(st)
        }
        content.onShowOutbox = { [weak self] acc in
            guard let self else { return }
            self.popover.close()
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

    /// The triage's line at the bar's trailing end; "" hides it. Only the
    /// counts and classes of the Core's triage view, never a run's output.
    func setNote(_ text: String) {
        triageNote = text
        applyNote()
    }

    /// The Calendar mode's texts: `line` in place of the sync line's text
    /// (nil: the sync line again), `note` (the unanswered count) ahead of
    /// the triage's note.
    func setCalendar(line: String?, note: String) {
        calendarNote = line == nil ? "" : note
        if calendarLine != line {
            calendarLine = line
            if isViewLoaded {
                show(sync.line)
            }
        }
        applyNote()
    }

    private func applyNote() {
        let text = calendarNote.isEmpty || triageNote.isEmpty
            ? calendarNote + triageNote : Cal.Text.dot(calendarNote, triageNote)
        guard noteLabel.stringValue != text else { return }
        noteLabel.stringValue = text
        noteLabel.toolTip = text.isEmpty ? nil : text
        noteLabel.isHidden = text.isEmpty
    }

    /// The sync line and the note as shown (the development hook prints them).
    var shownTexts: (line: String, note: String) {
        (statusButton.title, noteLabel.stringValue)
    }

    /// Shows the line (sync.go `refreshSyncLabel`): the text, the spinner,
    /// the connection icon while there is no connection, the foot of the
    /// popover. Without a connection the line cannot be clicked, and an open
    /// popover closes; while it is open its rows follow every change.
    private func show(_ line: StatusLine) {
        let text = calendarLine ?? line.text
        statusButton.title = text
        statusButton.setAccessibilityLabel(text)
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
            popover.close()
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

    // MARK: Report an Issue

    @objc private func showWebsiteFeatures(_ sender: Any?) {
        WebsiteFeaturesDialog.show(for: view.window)
    }

    /// main.go app.report-issue: the report form in the browser.
    @objc private func reportIssue(_ sender: Any?) {
        NSWorkspace.shared.open(AppLinks.reportIssue)
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
            popover.close()
            return
        }
        refreshStatusPopover()
        // Above the bar with its leading edge on the text's, as the Windows
        // flyout (TopEdgeAlignedLeft) and GTK's menu button open: as far
        // above the bar's top as the text is from the window's edge.
        let text = view.convert(statusTextRect, from: statusButton)
        let anchor = NSRect(x: text.minX, y: view.bounds.minY, width: text.width, height: view.bounds.height)
        popover.show(above: anchor, of: view, gap: Self.inset)
    }

    /// The part of the line its text covers. The button spans the whole
    /// bar; the flyout lines up with the text at the bar's leading end.
    private var statusTextRect: NSRect {
        let bounds = statusButton.bounds
        let text = statusButton.cell?.cellSize.width ?? bounds.width
        return NSRect(x: bounds.minX, y: bounds.minY,
                      width: min(bounds.width, max(text, 1)), height: bounds.height)
    }

    /// The flyout closes on the mouse-down of any click outside it; when
    /// that click is on the line, its mouse-up would open the flyout again.
    /// The time of such a mouse-down is noted here.
    private func flyoutWillClose(_ event: NSEvent?) {
        closedByLineClick = nil
        guard let event, event.type == .leftMouseDown,
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
        // Only the view's own area: since macOS 14 a view does not clip
        // to its bounds by default and dirtyRect can reach past them, so
        // filling it painted over the views beside and below this one.
        dirtyRect.intersection(bounds).fill()
    }
}
