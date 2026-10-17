// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The time-zone link's popover (docs/calendar.md §5.6): a search field
/// over the system's IANA zones and the matching list; a click picks.
@MainActor
final class EventEditorTimeZoneViewController: NSViewController, NSTableViewDataSource, NSTableViewDelegate,
    NSSearchFieldDelegate
{
    var onPick: (@MainActor (TimeZone) -> Void)?

    private let all = TimeZone.knownTimeZoneIdentifiers.sorted()
    private var shown: [String] = []
    private let current: String
    private let search = NSSearchField()
    private let table = NSTableView()

    init(current: String) {
        self.current = current
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func loadView() {
        let root = NSView(frame: NSRect(x: 0, y: 0, width: 300, height: 320))
        search.placeholderString = Cal.zoneSearch
        search.delegate = self
        search.translatesAutoresizingMaskIntoConstraints = false
        let column = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("zone"))
        table.addTableColumn(column)
        table.headerView = nil
        table.style = .plain
        table.rowHeight = 22
        table.dataSource = self
        table.delegate = self
        table.target = self
        table.action = #selector(rowClicked(_:))
        let scroll = NSScrollView()
        scroll.documentView = table
        scroll.hasVerticalScroller = true
        scroll.autohidesScrollers = true
        scroll.translatesAutoresizingMaskIntoConstraints = false
        root.addSubview(search)
        root.addSubview(scroll)
        NSLayoutConstraint.activate([
            search.topAnchor.constraint(equalTo: root.topAnchor, constant: 10),
            search.leadingAnchor.constraint(equalTo: root.leadingAnchor, constant: 10),
            search.trailingAnchor.constraint(equalTo: root.trailingAnchor, constant: -10),
            scroll.topAnchor.constraint(equalTo: search.bottomAnchor, constant: 8),
            scroll.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            scroll.trailingAnchor.constraint(equalTo: root.trailingAnchor),
            scroll.bottomAnchor.constraint(equalTo: root.bottomAnchor),
        ])
        view = root
        filter("")
    }

    override func viewDidAppear() {
        super.viewDidAppear()
        view.window?.makeFirstResponder(search)
    }

    /// Case-insensitive; "new york" finds America/New_York.
    private func filter(_ text: String) {
        let q = text.trimmingCharacters(in: .whitespaces).lowercased().replacingOccurrences(of: " ", with: "_")
        shown = q.isEmpty ? all : all.filter { $0.lowercased().contains(q) }
        table.reloadData()
        if let i = shown.firstIndex(of: current) {
            table.selectRowIndexes(IndexSet(integer: i), byExtendingSelection: false)
            table.scrollRowToVisible(i)
        }
    }

    func controlTextDidChange(_ obj: Foundation.Notification) {
        filter(search.stringValue)
    }

    /// Return in the field picks the first match.
    func control(_ control: NSControl, textView: NSTextView, doCommandBy sel: Selector) -> Bool {
        guard sel == #selector(NSResponder.insertNewline(_:)), let first = shown.first,
            let z = TimeZone(identifier: first)
        else { return false }
        onPick?(z)
        return true
    }

    func numberOfRows(in tableView: NSTableView) -> Int { shown.count }

    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        let id = NSUserInterfaceItemIdentifier("zoneCell")
        let cell = tableView.makeView(withIdentifier: id, owner: nil) as? NSTextField ?? {
            let f = NSTextField(labelWithString: "")
            f.identifier = id
            f.lineBreakMode = .byTruncatingTail
            return f
        }()
        cell.stringValue = shown[row].replacingOccurrences(of: "_", with: " ")
        return cell
    }

    @objc private func rowClicked(_ sender: Any?) {
        let r = table.clickedRow
        guard shown.indices.contains(r), let z = TimeZone(identifier: shown[r]) else { return }
        onPick?(z)
    }
}

/// Repeat ▸ Custom…'s popover: "Every [n] [unit ▾]" and, for weeks, the
/// days from Monday (`Cal.customRecurrence`); Done applies it.
@MainActor
final class EventEditorRepeatViewController: NSViewController {
    var onApply: (@MainActor (Recurrence) -> Void)?

    private static let frequencies: [RecurrenceFrequency] = [.daily, .weekly, .monthly, .yearly]

    private let start: Date
    private let zone: TimeZone
    private let env: Cal.Env
    private let interval = NSTextField()
    private let stepper = NSStepper()
    private let unit = NSPopUpButton(frame: .zero, pullsDown: false)
    private let daysRow = NSStackView()
    private var dayButtons: [(Cal.Weekday, NSButton)] = []

    init(current: Recurrence?, start: Date, zone: TimeZone, env: Cal.Env) {
        self.start = start
        self.zone = zone
        self.env = env
        super.init(nibName: nil, bundle: nil)
        let f = current?.frequency ?? .weekly
        let n = min(max(1, current?.interval ?? 1), API.Limits.maxRecurrenceInterval)
        stepper.minValue = 1
        stepper.maxValue = Double(API.Limits.maxRecurrenceInterval)
        stepper.integerValue = n
        interval.integerValue = n
        // A week's days: the rule's, else the start's.
        let codes = Set(current?.byDay ?? [])
        let first = Cal.startOfDay(start, zone)
        let startDay = Cal.parts(start, zone).weekday
        var days: [(Cal.Weekday, Date)] = []
        for k in 0..<7 {
            let d = Cal.addDays(first, k, zone)
            days.append((Cal.parts(d, zone).weekday, d))
        }
        days.sort { ($0.0.rawValue + 6) % 7 < ($1.0.rawValue + 6) % 7 }
        for (w, d) in days {
            let b = NSButton(checkboxWithTitle: Cal.Text.shortWeekday(d, env), target: nil, action: nil)
            b.state = (codes.isEmpty ? w == startDay : codes.contains(Cal.dayCode(w))) ? .on : .off
            dayButtons.append((w, b))
        }
        fillUnits(selected: Self.frequencies.contains(f) ? f : .weekly)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    private func fillUnits(selected: RecurrenceFrequency) {
        let n = max(1, interval.integerValue)
        unit.removeAllItems()
        for f in Self.frequencies {
            unit.addItem(withTitle: Cal.intervalUnit(f, n))
            unit.lastItem?.representedObject = f.rawValue
        }
        unit.selectItem(at: Self.frequencies.firstIndex(of: selected) ?? 1)
    }

    private var frequency: RecurrenceFrequency {
        Self.frequencies[max(0, unit.indexOfSelectedItem)]
    }

    override func loadView() {
        interval.alignment = .right
        interval.target = self
        interval.action = #selector(intervalChanged(_:))
        interval.widthAnchor.constraint(equalToConstant: 44).isActive = true
        stepper.target = self
        stepper.action = #selector(stepped(_:))
        unit.target = self
        unit.action = #selector(unitChanged(_:))
        let every = NSTextField(labelWithString: Cal.everyLabel)
        let line = NSStackView(views: [every, interval, stepper, unit])
        line.spacing = 6
        daysRow.orientation = .horizontal
        daysRow.spacing = 6
        for (_, b) in dayButtons {
            daysRow.addArrangedSubview(b)
        }
        daysRow.isHidden = frequency != .weekly
        let done = NSButton(title: L10n.T("Done"), target: self, action: #selector(apply(_:)))
        done.keyEquivalent = "\r"
        done.bezelColor = .controlAccentColor
        let stack = NSStackView(views: [line, daysRow, done])
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 10
        stack.edgeInsets = NSEdgeInsets(top: 12, left: 14, bottom: 12, right: 14)
        stack.setCustomSpacing(14, after: daysRow)
        view = stack
    }

    @objc private func stepped(_ sender: Any?) {
        interval.integerValue = stepper.integerValue
        fillUnits(selected: frequency)
    }

    @objc private func intervalChanged(_ sender: Any?) {
        let n = min(max(1, interval.integerValue), API.Limits.maxRecurrenceInterval)
        interval.integerValue = n
        stepper.integerValue = n
        fillUnits(selected: frequency)
    }

    @objc private func unitChanged(_ sender: Any?) {
        daysRow.isHidden = frequency != .weekly
    }

    @objc private func apply(_ sender: Any?) {
        intervalChanged(nil)
        let days = dayButtons.filter { $0.1.state == .on }.map(\.0)
        onApply?(Cal.customRecurrence(frequency, interval: interval.integerValue, days: days, start: start, zone: zone))
    }
}
