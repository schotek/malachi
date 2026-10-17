// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// A line of the agenda table: a day's heading or an occurrence with the
/// text of its time column.
enum AgendaEntry: Equatable {
    case group(String)
    case item(Cal.AgendaItem, time: String)
}

/// The Agenda view, the Unanswered filter and the search results
/// (docs/calendar.md §5.4): an `NSTableView` with a group row per day
/// (`Cal.agenda`; the results come without groups,
/// `CalendarSearchController`), rows of time (or All Day), colour dot,
/// title, place and the response badge, a heading above the table in
/// place of the view ("Results", "Invitations"), the controller's empty
/// text. Scrolling near the end of the Agenda asks the controller for the
/// next 30 days (`loadMoreAgenda`).
@MainActor
final class CalendarAgendaViewController: NSViewController, NSTableViewDataSource, NSTableViewDelegate {
    weak var controller: CalendarController?
    var onOpen: ((Occurrence, NSView) -> Void)?

    private let heading = calendarLabel(.systemFont(ofSize: Typo.bodySize + 4, weight: .bold))
    private let empty = NSTextField(wrappingLabelWithString: "")
    private let table = NSTableView()
    private let scroll = NSScrollView()
    private var headingHeight: NSLayoutConstraint!
    private(set) var entries: [AgendaEntry] = []
    private var colors: [CalendarID: NSColor] = [:]
    private var paging = false
    /// A next page was asked for and has not arrived yet.
    private var pageRequested = false
    private var env = Cal.Env(zone: .current, locale: .current)

    override func loadView() {
        let root = NSView()
        heading.translatesAutoresizingMaskIntoConstraints = false
        empty.font = .systemFont(ofSize: Typo.bodySize)
        empty.textColor = .secondaryLabelColor
        empty.alignment = .center
        empty.translatesAutoresizingMaskIntoConstraints = false
        let column = NSTableColumn(identifier: .init("agenda"))
        column.resizingMask = .autoresizingMask
        table.addTableColumn(column)
        table.headerView = nil
        table.style = .plain
        table.rowSizeStyle = .custom
        table.intercellSpacing = NSSize(width: 0, height: 0)
        table.usesAutomaticRowHeights = false
        table.dataSource = self
        table.delegate = self
        table.target = self
        table.action = #selector(clicked)
        table.columnAutoresizingStyle = .uniformColumnAutoresizingStyle
        scroll.documentView = table
        scroll.hasVerticalScroller = true
        scroll.drawsBackground = false
        scroll.translatesAutoresizingMaskIntoConstraints = false
        scroll.contentView.postsBoundsChangedNotifications = true
        NotificationCenter.default.addObserver(
            self, selector: #selector(scrolled), name: NSView.boundsDidChangeNotification, object: scroll.contentView)
        root.addSubview(heading)
        root.addSubview(scroll)
        root.addSubview(empty)
        headingHeight = heading.heightAnchor.constraint(equalToConstant: 0)
        NSLayoutConstraint.activate([
            heading.topAnchor.constraint(equalTo: root.topAnchor, constant: 10),
            heading.leadingAnchor.constraint(equalTo: root.leadingAnchor, constant: 20),
            heading.trailingAnchor.constraint(lessThanOrEqualTo: root.trailingAnchor, constant: -20),
            headingHeight,
            scroll.topAnchor.constraint(equalTo: heading.bottomAnchor, constant: 4),
            scroll.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            scroll.trailingAnchor.constraint(equalTo: root.trailingAnchor),
            scroll.bottomAnchor.constraint(equalTo: root.bottomAnchor),
            empty.centerYAnchor.constraint(equalTo: scroll.centerYAnchor),
            empty.leadingAnchor.constraint(equalTo: root.leadingAnchor, constant: 40),
            empty.trailingAnchor.constraint(equalTo: root.trailingAnchor, constant: -40),
        ])
        view = root
    }

    /// Reads the controller's state (after its `onChange`): in search mode
    /// `searchResults` (through `CalendarSearchController`), else `agenda`
    /// (the Agenda view's pages or the Unanswered filter); `listHeading`,
    /// `emptyListText`, `selection`, `loading` and the colours of the
    /// snapshot's calendars.
    func update(controller c: CalendarController) {
        _ = view
        env = c.env
        var map: [CalendarID: NSColor] = [:]
        for a in c.snapshot.calendarAccounts {
            for cal in a.calendars {
                map[cal.id] = CalendarColor.color(cal.color)
            }
        }
        colors = map
        let new: [AgendaEntry]
        if CalendarSearchController.active(c) {
            new = CalendarSearchController.entries(c)
        } else {
            new = (c.agenda ?? []).flatMap { day -> [AgendaEntry] in
                [.group(day.heading)] + day.items.map { .item($0, time: Cal.itemTime($0, env: c.env)) }
            }
        }
        paging = c.view == .agenda && c.filter == .none && c.searchResults == nil
        heading.stringValue = c.listHeading
        headingHeight.constant = c.listHeading.isEmpty ? 0 : ceil(heading.intrinsicContentSize.height)
        heading.isHidden = c.listHeading.isEmpty
        empty.stringValue = c.emptyListText
        empty.isHidden = c.emptyListText.isEmpty || c.loading
        if new != entries {
            entries = new
            pageRequested = false
            table.reloadData()
        } else if !c.loading {
            pageRequested = false
        }
        select(c.selection)
    }

    private func select(_ key: String?) {
        let row = entries.firstIndex {
            if case .item(let it, _) = $0 { return Cal.occurrenceKey(it.occurrence) == key }
            return false
        }
        if let row {
            if table.selectedRow != row {
                table.selectRowIndexes([row], byExtendingSelection: false)
            }
        } else if table.selectedRow >= 0 {
            table.deselectAll(nil)
        }
    }

    // MARK: Paging

    @objc private func scrolled() {
        guard paging, !pageRequested, let c = controller else { return }
        let visible = scroll.contentView.bounds
        guard table.frame.height > 0, visible.maxY >= table.frame.height - 200 else { return }
        pageRequested = true
        c.loadMoreAgenda()
    }

    // MARK: Table

    func numberOfRows(in tableView: NSTableView) -> Int { entries.count }

    func tableView(_ tableView: NSTableView, isGroupRow row: Int) -> Bool {
        if case .group = entries[row] { return true }
        return false
    }

    func tableView(_ tableView: NSTableView, heightOfRow row: Int) -> CGFloat {
        if case .group = entries[row] { return 30 }
        return 28
    }

    func tableView(_ tableView: NSTableView, shouldSelectRow row: Int) -> Bool {
        if case .group = entries[row] { return false }
        return true
    }

    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        switch entries[row] {
        case .group(let text):
            let v = AgendaGroupCell()
            v.label.stringValue = text
            return v
        case .item(let it, let time):
            let v = AgendaItemCell()
            let color = it.occurrence.calendarId.flatMap { colors[$0] } ?? Tint.accent
            v.configure(it, time: time, color: color, label: Cal.blockLabel(it.occurrence, env: env))
            return v
        }
    }

    @objc private func clicked() {
        let row = table.clickedRow
        guard row >= 0, row < entries.count, case .item(let it, _) = entries[row] else { return }
        let anchor = table.view(atColumn: 0, row: row, makeIfNecessary: false) ?? table
        onOpen?(it.occurrence, anchor)
    }

    /// The row view of the occurrence `key` on screen.
    func view(for key: String) -> NSView? {
        guard let row = entries.firstIndex(where: {
            if case .item(let it, _) = $0 { return Cal.occurrenceKey(it.occurrence) == key }
            return false
        }) else { return nil }
        return table.view(atColumn: 0, row: row, makeIfNecessary: false)
    }

    var developmentFrames: [String] {
        guard isViewLoaded else { return [] }
        func r(_ v: NSView) -> String { NSStringFromRect(v.convert(v.bounds, to: nil)) }
        return [
            "  calendar agenda \(r(view)) rows \(entries.count) heading '\(heading.stringValue)' empty '\(empty.isHidden ? "" : empty.stringValue)'",
        ]
    }
}

/// A day's heading row of the agenda.
@MainActor
private final class AgendaGroupCell: NSTableCellView {
    let label = calendarLabel(.systemFont(ofSize: Typo.bodySize, weight: .semibold))

    init() {
        super.init(frame: .zero)
        label.translatesAutoresizingMaskIntoConstraints = false
        addSubview(label)
        NSLayoutConstraint.activate([
            label.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 20),
            label.trailingAnchor.constraint(lessThanOrEqualTo: trailingAnchor, constant: -20),
            label.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -4),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }
}

/// An occurrence's row of the agenda: time, colour dot, title, place and
/// the response badge (unanswered, tentative, declined; "Day 2 of 3" for a
/// part of a longer one).
@MainActor
private final class AgendaItemCell: NSTableCellView {
    private let time = calendarLabel(.monospacedDigitSystemFont(ofSize: Typo.bodySize - 1, weight: .regular), color: .secondaryLabelColor)
    private let dot = DotView()
    private let title = calendarLabel(.systemFont(ofSize: Typo.bodySize, weight: .medium))
    private let place = calendarLabel(.systemFont(ofSize: Typo.bodySize - 1), color: .secondaryLabelColor)
    private let badge = PillLabel()

    init() {
        super.init(frame: .zero)
        badge.font = CalendarMetrics.smallFont
        badge.textColor = .secondaryLabelColor
        title.setContentCompressionResistancePriority(.init(260), for: .horizontal)
        place.setContentCompressionResistancePriority(.init(250), for: .horizontal)
        for v in [time, dot, title, place, badge] as [NSView] {
            v.translatesAutoresizingMaskIntoConstraints = false
            addSubview(v)
        }
        NSLayoutConstraint.activate([
            time.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 20),
            time.widthAnchor.constraint(equalToConstant: 170),
            time.centerYAnchor.constraint(equalTo: centerYAnchor),
            dot.leadingAnchor.constraint(equalTo: time.trailingAnchor, constant: 8),
            dot.centerYAnchor.constraint(equalTo: centerYAnchor),
            dot.widthAnchor.constraint(equalToConstant: 8),
            dot.heightAnchor.constraint(equalToConstant: 8),
            title.leadingAnchor.constraint(equalTo: dot.trailingAnchor, constant: 8),
            title.centerYAnchor.constraint(equalTo: centerYAnchor),
            place.leadingAnchor.constraint(equalTo: title.trailingAnchor, constant: 10),
            place.centerYAnchor.constraint(equalTo: centerYAnchor),
            badge.leadingAnchor.constraint(greaterThanOrEqualTo: place.trailingAnchor, constant: 10),
            badge.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -20),
            badge.centerYAnchor.constraint(equalTo: centerYAnchor),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    func configure(_ it: Cal.AgendaItem, time t: String, color: NSColor, label: String) {
        let o = it.occurrence
        time.stringValue = t
        dot.color = color
        title.attributedStringValue = BlockPainter.title(
            CalendarText.title(o.title), font: .systemFont(ofSize: Typo.bodySize, weight: .medium), color: .labelColor,
            state: it.state)
        place.stringValue = CalendarText.line(o.location)
        var b = ""
        switch it.state {
        case .unanswered, .tentative, .declined:
            b = Cal.responseLabel(o.myResponse)
        case .cancelled:
            b = Cal.blockStateText(.cancelled)
        case .normal:
            break
        }
        if b.isEmpty, it.days > 1 {
            b = Cal.dayOfText(it.dayOf, of: it.days)
        }
        badge.stringValue = b
        badge.isHidden = b.isEmpty
        if it.state == .unanswered {
            badge.fill = color.withAlphaComponent(0.2)
        }
        setAccessibilityLabel(label)
    }
}
