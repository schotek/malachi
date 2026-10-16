// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The Calendar's sidebar (docs/calendar.md §5.3), 240 pt: the mini month,
/// the section Invitations with the row Unanswered and its count badge
/// (the controller's Unanswered filter), then per account a heading with
/// the kind capsule (`accountHeaderBadge`: M365, GOOGLE, CALDAV) and its
/// calendars, each with a checkbox (`calendar.update visible` through the
/// controller) and its colour, a state row with Sign In… when the
/// account's state needs it, and at the bottom Add Calendar… with its
/// menu. Everything comes from `CalendarController.sidebar`; account and
/// calendar names are shown through `stringValue` only.
@MainActor
final class CalendarSidebarViewController: NSViewController {
    weak var controller: CalendarController?
    /// Add CalDAV Calendar…: the window controller sets the app's wizard
    /// entry; unset, the action `addCalDAVAccount:` goes up the responder
    /// chain (harmless when nobody answers it).
    var onAddCalDAV: (() -> Void)?
    /// "Sync a Microsoft 365 account's calendars…": unset, Settings opens
    /// (its first page is Accounts).
    var onSyncMicrosoft: (() -> Void)?

    private let miniMonth = MiniMonthView()
    private let stack = FillStackView()
    private let scroll = NSScrollView()
    private var shown: CalendarController.Sidebar?
    private var shownFilter: CalendarController.Filter?
    private var unansweredRow: SidebarRowButton?

    override func loadView() {
        let root = NSView()
        scroll.drawsBackground = false
        scroll.hasVerticalScroller = true
        scroll.autohidesScrollers = true
        scroll.translatesAutoresizingMaskIntoConstraints = false
        let document = FlippedClipDocument()
        document.translatesAutoresizingMaskIntoConstraints = false
        stack.spacing = 2
        stack.edgeInsets = NSEdgeInsets(top: 8, left: 12, bottom: 12, right: 12)
        document.addSubview(stack)
        scroll.documentView = document
        root.addSubview(scroll)
        NSLayoutConstraint.activate([
            scroll.topAnchor.constraint(equalTo: root.topAnchor),
            scroll.bottomAnchor.constraint(equalTo: root.bottomAnchor),
            scroll.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            scroll.trailingAnchor.constraint(equalTo: root.trailingAnchor),
            document.topAnchor.constraint(equalTo: scroll.contentView.topAnchor),
            document.leadingAnchor.constraint(equalTo: scroll.contentView.leadingAnchor),
            document.widthAnchor.constraint(equalTo: scroll.contentView.widthAnchor),
            stack.topAnchor.constraint(equalTo: document.topAnchor),
            stack.leadingAnchor.constraint(equalTo: document.leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: document.trailingAnchor),
            stack.bottomAnchor.constraint(equalTo: document.bottomAnchor),
        ])
        miniMonth.translatesAutoresizingMaskIntoConstraints = false
        miniMonth.heightAnchor.constraint(equalToConstant: MiniMonthView.height).isActive = true
        miniMonth.onPick = { [weak self] d in self?.controller?.goTo(day: d) }
        miniMonth.onStep = { [weak self] dir in self?.controller?.stepMiniMonth(dir) }
        view = root
    }

    /// After the controller's `onChange`: reads `sidebar` (mini month,
    /// unanswered count, accounts with calendars and state rows) and
    /// `filter`; rebuilds only what changed.
    func update() {
        guard let c = controller else { return }
        _ = view
        var s = c.sidebar
        miniMonth.set(s.miniMonth)
        // The mini month redraws by itself; the rows only when they change
        // (not on every step through the weeks).
        s.miniMonth = shown?.miniMonth ?? s.miniMonth
        if shown != s || shownFilter != c.filter {
            shown = s
            shownFilter = c.filter
            rebuild(s, filter: c.filter)
        }
    }

    private func rebuild(_ s: CalendarController.Sidebar, filter: CalendarController.Filter) {
        for v in stack.arrangedSubviews {
            stack.removeArrangedSubview(v)
            v.removeFromSuperview()
        }
        stack.addArrangedSubview(miniMonth)
        stack.setCustomSpacing(10, after: miniMonth)

        // Invitations ▸ Unanswered.
        stack.addArrangedSubview(SidebarSectionHeading(text: s.invitationsTitle, badge: ""))
        let row = SidebarRowButton(symbol: "envelope.badge", text: s.unansweredTitle, count: s.unanswered)
        row.selected = filter == .unanswered
        row.onClick = { [weak self] in self?.controller?.showUnanswered() }
        unansweredRow = row
        stack.addArrangedSubview(row)
        stack.setCustomSpacing(10, after: row)

        for a in s.accounts {
            let badge = a.account.map(accountHeaderBadge) ?? ""
            stack.addArrangedSubview(SidebarSectionHeading(text: a.name, badge: badge))
            for cal in a.calendars {
                let r = SidebarCalendarRow(calendar: cal)
                let id = cal.id
                r.onToggle = { [weak self] on in self?.controller?.setVisible(on, calendar: id) }
                stack.addArrangedSubview(r)
            }
            if !a.stateLine.isEmpty {
                let account = a.accountId
                let r = SidebarStateRow(text: a.stateLine, signIn: a.offersSignIn)
                r.onSignIn = { [weak self] in self?.controller?.signIn(account) }
                stack.addArrangedSubview(r)
            }
            if let last = stack.arrangedSubviews.last {
                stack.setCustomSpacing(10, after: last)
            }
        }

        let add = NSButton(title: s.addCalendar, target: self, action: #selector(showAddMenu(_:)))
        add.bezelStyle = .accessoryBarAction
        add.isBordered = false
        add.image = NSImage(systemSymbolName: "plus", accessibilityDescription: nil)
        add.imagePosition = .imageLeading
        add.contentTintColor = .secondaryLabelColor
        add.alignment = .left
        let menu = NSMenu()
        let caldav = NSMenuItem(title: s.addCalDAV, action: #selector(addCalDAV(_:)), keyEquivalent: "")
        caldav.target = self
        let ms = NSMenuItem(title: s.syncMicrosoft, action: #selector(syncMicrosoft(_:)), keyEquivalent: "")
        ms.target = self
        menu.addItem(caldav)
        menu.addItem(ms)
        add.menu = menu
        let holder = NSStackView(views: [add, NSView()])
        holder.orientation = .horizontal
        stack.addArrangedSubview(holder)
    }

    @objc private func showAddMenu(_ sender: NSButton) {
        guard let menu = sender.menu else { return }
        menu.popUp(positioning: nil, at: NSPoint(x: 0, y: sender.bounds.height + 4), in: sender)
    }

    @objc private func addCalDAV(_ sender: Any?) {
        if let onAddCalDAV {
            onAddCalDAV()
        } else {
            NSApp.sendAction(NSSelectorFromString("addCalDAVAccount:"), to: nil, from: sender)
        }
    }

    @objc private func syncMicrosoft(_ sender: Any?) {
        if let onSyncMicrosoft {
            onSyncMicrosoft()
        } else {
            NSApp.sendAction(Action.showPreferences, to: nil, from: sender)
        }
    }

    var developmentFrames: [String] {
        guard isViewLoaded else { return [] }
        func r(_ v: NSView) -> String { NSStringFromRect(v.convert(v.bounds, to: nil)) }
        var lines = ["  calendar mini month \(r(miniMonth)) '\(miniMonth.month?.title ?? "")'"]
        if let unansweredRow {
            lines.append("  calendar unanswered row \(r(unansweredRow))")
        }
        lines.append("  calendar sidebar rows \(stack.arrangedSubviews.count) height \(Int(stack.fittingSize.height))")
        return lines
    }
}

/// The sidebar's scrolling document, top-down.
@MainActor
private final class FlippedClipDocument: NSView {
    override var isFlipped: Bool { true }
}

/// A heading of the sidebar: a section's or an account's name, with the
/// kind capsule after an account's (as the Mail sidebar's headings).
@MainActor
final class SidebarSectionHeading: NSView {
    init(text: String, badge: String) {
        super.init(frame: .zero)
        let label = calendarLabel(Typo.sidebarHeading, color: .secondaryLabelColor)
        label.stringValue = text
        label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        let kind = PillLabel()
        kind.font = Typo.sidebarKindBadge
        kind.textColor = .secondaryLabelColor
        kind.stringValue = badge
        kind.isHidden = badge.isEmpty
        let row = NSStackView(views: [label, kind])
        row.orientation = .horizontal
        row.alignment = .centerY
        row.spacing = 6
        row.detachesHiddenViews = true
        row.translatesAutoresizingMaskIntoConstraints = false
        addSubview(row)
        NSLayoutConstraint.activate([
            heightAnchor.constraint(equalToConstant: SidebarMetrics.headerRowHeight),
            row.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 2),
            row.trailingAnchor.constraint(lessThanOrEqualTo: trailingAnchor),
            row.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -3),
        ])
        setAccessibilityElement(true)
        setAccessibilityRole(.staticText)
        setAccessibilityLabel(badge.isEmpty ? text : Cal.Text.dot(text, badge))
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }
}

/// A clickable row with a symbol, a text and a count badge (the
/// Unanswered row), highlighted while its filter shows.
@MainActor
final class SidebarRowButton: NSView {
    var onClick: (() -> Void)?
    var selected = false {
        didSet { needsDisplay = true }
    }

    init(symbol: String, text: String, count: Int) {
        super.init(frame: .zero)
        let icon = calendarSymbol(symbol, pointSize: SidebarMetrics.iconPointSize, color: .controlAccentColor)
        let label = calendarLabel(.systemFont(ofSize: SidebarMetrics.titleFontSize))
        label.stringValue = text
        label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        let badge = CountBadgeView(padding: 4)
        badge.font = .monospacedDigitSystemFont(ofSize: SidebarMetrics.captionFontSize, weight: .bold)
        badge.fill = .controlAccentColor
        badge.textColor = .white
        badge.text = String(count)
        badge.isHidden = count <= 0
        let spacer = NSView()
        spacer.setContentHuggingPriority(.init(1), for: .horizontal)
        let row = NSStackView(views: [icon, label, spacer, badge])
        row.orientation = .horizontal
        row.alignment = .centerY
        row.spacing = SidebarMetrics.cellSpacing
        row.translatesAutoresizingMaskIntoConstraints = false
        addSubview(row)
        NSLayoutConstraint.activate([
            heightAnchor.constraint(equalToConstant: SidebarMetrics.rowHeight),
            row.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 4),
            row.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -4),
            row.centerYAnchor.constraint(equalTo: centerYAnchor),
        ])
        setAccessibilityElement(true)
        setAccessibilityRole(.button)
        setAccessibilityLabel(count > 0 ? Cal.Text.dot(text, String(count)) : text)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func draw(_ dirtyRect: NSRect) {
        guard selected else { return }
        Tint.fg(alpha: 0.1).setFill()
        NSBezierPath(roundedRect: bounds, xRadius: 5, yRadius: 5).fill()
    }

    override func mouseDown(with event: NSEvent) {
        onClick?()
    }

    override func accessibilityPerformPress() -> Bool {
        onClick?()
        return true
    }
}

/// A calendar of the sidebar: its checkbox, its colour square, its name and
/// its note (default, read only, shared).
@MainActor
final class SidebarCalendarRow: NSView {
    var onToggle: ((Bool) -> Void)?
    private let check = NSButton(checkboxWithTitle: "", target: nil, action: nil)

    init(calendar c: CalendarController.SidebarCalendar) {
        super.init(frame: .zero)
        check.state = c.visible ? .on : .off
        check.target = self
        check.action = #selector(toggled)
        check.setAccessibilityLabel(c.name)
        let square = ColorSquare(color: CalendarColor.color(c.color))
        let name = calendarLabel(.systemFont(ofSize: SidebarMetrics.titleFontSize))
        name.stringValue = c.name
        // When the row is short the note gives way first, the name after it.
        name.setContentCompressionResistancePriority(.init(300), for: .horizontal)
        name.setContentHuggingPriority(.init(300), for: .horizontal)
        let note = calendarLabel(.systemFont(ofSize: SidebarMetrics.captionFontSize), color: .tertiaryLabelColor)
        note.stringValue = c.note
        note.isHidden = c.note.isEmpty
        note.setContentCompressionResistancePriority(.init(150), for: .horizontal)
        note.setContentHuggingPriority(.init(150), for: .horizontal)
        toolTip = c.note.isEmpty ? c.name : Cal.Text.dot(c.name, c.note)
        let row = NSStackView(views: [check, square, name, note])
        row.orientation = .horizontal
        row.alignment = .centerY
        row.spacing = 5
        row.detachesHiddenViews = true
        row.translatesAutoresizingMaskIntoConstraints = false
        addSubview(row)
        NSLayoutConstraint.activate([
            heightAnchor.constraint(equalToConstant: SidebarMetrics.rowHeight),
            row.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 2),
            row.trailingAnchor.constraint(lessThanOrEqualTo: trailingAnchor),
            row.centerYAnchor.constraint(equalTo: centerYAnchor),
        ])
        check.setContentCompressionResistancePriority(.required, for: .horizontal)
        square.setContentCompressionResistancePriority(.required, for: .horizontal)
        row.setClippingResistancePriority(.init(300), for: .horizontal)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    @objc private func toggled() {
        onToggle?(check.state == .on)
    }
}

/// A calendar's colour: a 10 pt rounded square.
@MainActor
final class ColorSquare: NSView {
    private let color: NSColor

    init(color: NSColor, size: CGFloat = 10) {
        self.color = color
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            widthAnchor.constraint(equalToConstant: size),
            heightAnchor.constraint(equalToConstant: size),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func draw(_ dirtyRect: NSRect) {
        color.setFill()
        NSBezierPath(roundedRect: bounds, xRadius: 2.5, yRadius: 2.5).fill()
    }
}

/// An account's state row ("Calendar access needs a new sign-in"), with
/// Sign In… when the controller offers it.
@MainActor
final class SidebarStateRow: NSView {
    var onSignIn: (() -> Void)?

    init(text: String, signIn: Bool) {
        super.init(frame: .zero)
        let icon = calendarSymbol("exclamationmark.triangle.fill", pointSize: 11, color: .systemOrange)
        let label = NSTextField(wrappingLabelWithString: "")
        label.stringValue = text
        label.font = .systemFont(ofSize: SidebarMetrics.captionFontSize)
        label.textColor = .secondaryLabelColor
        label.preferredMaxLayoutWidth = 180
        let top = NSStackView(views: [icon, label])
        top.orientation = .horizontal
        top.alignment = .top
        top.spacing = 5
        let col = NSStackView(views: [top])
        col.orientation = .vertical
        col.alignment = .leading
        col.spacing = 4
        if signIn {
            let b = NSButton(title: Cal.Text.signIn, target: self, action: #selector(signInClicked))
            b.controlSize = .small
            b.bezelStyle = .push
            col.addArrangedSubview(b)
        }
        col.translatesAutoresizingMaskIntoConstraints = false
        addSubview(col)
        NSLayoutConstraint.activate([
            col.topAnchor.constraint(equalTo: topAnchor, constant: 4),
            col.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -4),
            col.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 4),
            col.trailingAnchor.constraint(lessThanOrEqualTo: trailingAnchor),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    @objc private func signInClicked() {
        onSignIn?()
    }
}
