// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The Month view (docs/calendar.md §5.4): the weekday header, six rows of
/// seven days (`Cal.monthCells`) with the week numbers at the left, and
/// in each day the pills that fit. The capacity comes from Core
/// (`Cal.cellCapacity` with the cell's height) and is recomputed whenever
/// the cells change size; the view never measures what fits itself.
@MainActor
final class CalendarMonthView: NSView {
    var onOpen: ((Occurrence, NSView) -> Void)?
    var onNewEvent: ((Date) -> Void)?

    private var headers: [NSTextField] = []
    private var weekNumbers: [NSTextField] = []
    private(set) var cells: [MonthCellView] = []
    /// The spanning bars of the six rows (`Cal.monthLanes`), above the cells.
    private(set) var spans: [MonthSpanBarView] = []
    /// The lanes each row's bars use (`Cal.laneCount`).
    private var rowLanes: [Int] = []
    private var grid: Cal.MonthGrid?
    /// The pills each row's cells hold (`Cal.cellCapacity` with its lanes).
    private var capacities: [Int] = []
    private var occurrences: [Occurrence] = []
    private var showDeclined = false
    private var selection: String?
    private var env = Cal.Env(zone: .current, locale: .current)
    private var color: (Occurrence) -> NSColor = { _ in Tint.accent }
    private var label: (Occurrence) -> String = { _ in "" }

    private struct Input: Equatable {
        var grid: Cal.MonthGrid
        var headers: [String]
        var occurrences: [Occurrence]
        var showDeclined: Bool
        var colors: [String]
    }

    private var input: Input?

    override var isFlipped: Bool { true }

    /// Reads the controller's state (after its `onChange`): the anchor, the
    /// week start, the range's occurrences, `showDeclined`, the selection
    /// and the calendars' colours.
    func update(controller c: CalendarController) {
        let env = c.env
        let snapshot = c.snapshot
        let g = Cal.monthCells(anchor: c.anchor, weekStart: c.weekStart, now: Date(), zone: env.zone)
        let heads = Cal.weekdayHeaders(c.weekStart, env: env)
        let colors = snapshot.calendarAccounts.flatMap { $0.calendars.map { $0.id.rawValue + $0.color } }
        let new = Input(grid: g, headers: heads, occurrences: c.occurrences, showDeclined: c.showDeclined, colors: colors)
        selection = c.selection
        guard new != input else {
            cells.forEach { $0.setSelection(c.selection) }
            spans.forEach { $0.selected = $0.key == c.selection }
            return
        }
        input = new
        self.env = env
        grid = g
        occurrences = c.occurrences
        showDeclined = c.showDeclined
        color = { CalendarColor.of($0, in: snapshot) }
        label = { Cal.blockLabel($0, env: env) }
        rebuild(headers: heads)
    }

    private func rebuild(headers heads: [String]) {
        guard let g = grid else { return }
        (headers + weekNumbers).forEach { $0.removeFromSuperview() }
        cells.forEach { $0.removeFromSuperview() }
        spans.forEach { $0.removeFromSuperview() }
        headers = heads.map { h in
            let l = calendarLabel(CalendarMetrics.smallFont, color: .secondaryLabelColor)
            l.stringValue = h
            l.alignment = .center
            addSubview(l)
            return l
        }
        weekNumbers = g.weekNumbers.map { n in
            let l = calendarLabel(CalendarMetrics.smallFont, color: .tertiaryLabelColor)
            l.stringValue = String(n)
            l.alignment = .center
            l.setAccessibilityLabel(Cal.weekLabel(n))
            addSubview(l)
            return l
        }
        cells = g.cells.map { cell in
            let v = MonthCellView(cell: cell, env: env)
            v.onOpen = { [weak self] o, view in self?.onOpen?(o, view) }
            v.onNewEvent = { [weak self] d in self?.onNewEvent?(d) }
            addSubview(v)
            return v
        }
        // One bar per all-day or multi-day occurrence and week row.
        let zone = env.zone
        rowLanes = []
        spans = []
        for row in 0..<6 {
            let days = g.cells[row * 7..<row * 7 + 7].map(\.day)
            let list = Cal.monthLanes(occurrences, weekDays: Array(days), zone: zone, showDeclined: showDeclined)
            rowLanes.append(Cal.laneCount(list))
            for s in list {
                let v = MonthSpanBarView(
                    occurrence: s.occurrence, key: s.key, lane: s.lane, startCol: s.startCol, endCol: s.endCol,
                    continuesBefore: s.continuesBefore, continuesAfter: s.continuesAfter, state: s.state,
                    color: color(s.occurrence), label: label(s.occurrence))
                v.row = row
                v.selected = s.key == selection
                v.onClick = { [weak self] v in self?.onOpen?(v.occurrence, v) }
                addSubview(v, positioned: .above, relativeTo: nil)
                spans.append(v)
            }
        }
        capacities = []
        needsLayout = true
    }

    private var cellSize: NSSize {
        let w = (bounds.width - CalendarMetrics.weekNumberWidth) / 7
        let h = (bounds.height - CalendarMetrics.weekdayHeaderHeight) / 6
        return NSSize(width: max(0, w), height: max(0, h))
    }

    override func layout() {
        super.layout()
        let s = cellSize
        let top = CalendarMetrics.weekdayHeaderHeight
        for (i, h) in headers.enumerated() {
            h.frame = NSRect(x: CalendarMetrics.weekNumberWidth + CGFloat(i) * s.width, y: 5, width: s.width, height: 14)
        }
        for (i, l) in weekNumbers.enumerated() {
            l.frame = NSRect(x: 0, y: top + CGFloat(i) * s.height + 5, width: CalendarMetrics.weekNumberWidth, height: 14)
        }
        for (i, v) in cells.enumerated() {
            v.frame = NSRect(
                x: CalendarMetrics.weekNumberWidth + CGFloat(i % 7) * s.width, y: top + CGFloat(i / 7) * s.height,
                width: s.width, height: s.height)
        }
        for (i, v) in cells.enumerated() {
            let lanes = i / 7 < rowLanes.count ? rowLanes[i / 7] : 0
            v.pillTop = CalendarMetrics.cellNumberLine + CGFloat(lanes) * CalendarMetrics.pillLine
        }
        for v in spans {
            let rowTop = top + CGFloat(v.row) * s.height
            let y = rowTop + CalendarMetrics.cellNumberLine + CGFloat(v.lane) * CalendarMetrics.pillLine
            let x0 = CalendarMetrics.weekNumberWidth + CGFloat(v.startCol) * s.width + 3
            let x1 = CalendarMetrics.weekNumberWidth + CGFloat(v.endCol + 1) * s.width - 3
            v.frame = NSRect(x: x0, y: y, width: max(0, x1 - x0), height: CalendarMetrics.pillLine - 1)
            // A lane the row has no room for is not drawn.
            v.isHidden = y + CalendarMetrics.pillLine > rowTop + s.height
        }
        // The pills that fit under the row's lanes, from Core, whenever the
        // cells' height changes what fits.
        let caps = rowLanes.map { lanes in
            Cal.cellCapacity(
                cellHeight: Double(s.height - CalendarMetrics.cellNumberLine), lineHeight: Double(CalendarMetrics.pillLine),
                lanes: lanes, laneHeight: Double(CalendarMetrics.pillLine))
        }
        if caps != capacities {
            capacities = caps
            fill()
        }
    }

    private func fill() {
        let zone = env.zone
        for (i, v) in cells.enumerated() {
            let capacity = i / 7 < capacities.count ? capacities[i / 7] : 0
            let shown = Cal.cellItems(occurrences, day: v.cell.day, capacity: capacity, zone: zone, showDeclined: showDeclined)
            let all = shown.more > 0
                ? Cal.cellItems(occurrences, day: v.cell.day, capacity: Int.max, zone: zone, showDeclined: showDeclined).pills
                : shown.pills
            v.set(shown: shown.pills, more: shown.more, all: all, color: color, label: label, selection: selection)
        }
    }

    override func draw(_ dirtyRect: NSRect) {
        NSColor.separatorColor.setFill()
        NSRect(x: 0, y: CalendarMetrics.weekdayHeaderHeight - 1, width: bounds.width, height: 1).fill()
        NSRect(x: CalendarMetrics.weekNumberWidth - 1, y: 0, width: 1, height: bounds.height).fill()
    }

    /// The view of the occurrence `key` on screen (the first day showing it).
    func view(for key: String) -> NSView? {
        if let s = spans.first(where: { $0.key == key && !$0.isHidden }) {
            return s
        }
        for c in cells {
            if let p = c.pills.first(where: { $0.key == key }) {
                return p
            }
        }
        return nil
    }

    var developmentFrames: [String] {
        func r(_ v: NSView) -> String { NSStringFromRect(v.convert(v.bounds, to: nil)) }
        var lines = ["  calendar month \(r(self)) cells \(cells.count) lanes \(rowLanes) capacities \(capacities) cell \(NSStringFromSize(cellSize))"]
        if let p = cells.lazy.flatMap(\.pills).first {
            lines.append("  calendar first pill \(r(p)) '\(p.accessibilityLabel() ?? "")'")
        }
        return lines
    }
}
