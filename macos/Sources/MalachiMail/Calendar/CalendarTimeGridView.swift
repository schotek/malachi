// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The Week and Day views (docs/calendar.md §5.3): the day header row
/// (40 pt), the all-day row (36 pt and up) and a scroll view over the time
/// grid of the whole day (60 pt an hour), scrolled to the working hours at
/// the first show. Day is the same with one column and room for the place
/// and the video call. Everything placed comes from Core: the columns of
/// overlapping blocks (`Cal.layoutDay`), the all-day lanes
/// (`Cal.allDayLanes`), the now line (`Cal.nowLine`), the states
/// (`Cal.stateOf`) and the spoken labels (`Cal.blockLabel`).
@MainActor
final class CalendarWeekView: NSView {
    /// A click on a block or a bar: the page selects the occurrence and
    /// opens its detail anchored to `view`.
    var onOpen: ((Occurrence, NSView) -> Void)?
    /// A double click on empty time or on the all-day row: New Event with
    /// these defaults' origin at that instant.
    var onNewEvent: ((Date, Cal.Origin) -> Void)?

    let header = CalendarDayHeaderView()
    let allDay = CalendarAllDayView()
    let grid = CalendarTimeGridView()
    let scroll = NSScrollView()
    private var allDayHeight: NSLayoutConstraint!
    /// The grid is to scroll to the working hours at the next layout with
    /// a size: the first show, and a range of other days.
    private var scrollPending = true
    /// The offset the grid was put at for the working hours and the clip
    /// height it was computed for; kept until the user scrolls, so that a
    /// later size of the clip view (the window settling, the safe area,
    /// the all-day row growing) puts it back instead of leaving the clamp
    /// of a provisional height.
    private var autoScroll: (y: CGFloat, clipHeight: CGFloat)?
    private var applyingScroll = false
    private var scrollMinute = 7 * 60
    private var timer: Timer?

    /// What the views were last built from: they are rebuilt only when it
    /// changes, so that a refresh of the same data keeps the views.
    private struct Input: Equatable {
        var days: [Date]
        var occurrences: [Occurrence]
        var showDeclined: Bool
        var colors: [String]
        var zone: TimeZone
    }

    private var input: Input?

    override init(frame: NSRect) {
        super.init(frame: frame)
        scroll.documentView = grid
        scroll.hasVerticalScroller = true
        scroll.hasHorizontalScroller = false
        scroll.drawsBackground = false
        scroll.autohidesScrollers = true
        scroll.contentView.postsBoundsChangedNotifications = true
        // The scroll view sits below the toolbar (the safe area): no
        // automatic insets, so that an offset is a minute of the grid.
        scroll.automaticallyAdjustsContentInsets = false
        scroll.contentInsets = NSEdgeInsets()
        NotificationCenter.default.addObserver(
            self, selector: #selector(clipScrolled), name: NSView.boundsDidChangeNotification, object: scroll.contentView)
        for v in [header, allDay, scroll] as [NSView] {
            v.translatesAutoresizingMaskIntoConstraints = false
            addSubview(v)
        }
        allDayHeight = allDay.heightAnchor.constraint(equalToConstant: CalendarMetrics.allDayMinimum)
        NSLayoutConstraint.activate([
            header.topAnchor.constraint(equalTo: topAnchor),
            header.leadingAnchor.constraint(equalTo: leadingAnchor),
            header.trailingAnchor.constraint(equalTo: trailingAnchor),
            header.heightAnchor.constraint(equalToConstant: CalendarMetrics.dayHeaderHeight),
            allDay.topAnchor.constraint(equalTo: header.bottomAnchor),
            allDay.leadingAnchor.constraint(equalTo: leadingAnchor),
            allDay.trailingAnchor.constraint(equalTo: trailingAnchor),
            allDayHeight,
            scroll.topAnchor.constraint(equalTo: allDay.bottomAnchor),
            scroll.leadingAnchor.constraint(equalTo: leadingAnchor),
            scroll.trailingAnchor.constraint(equalTo: trailingAnchor),
            scroll.bottomAnchor.constraint(equalTo: bottomAnchor),
        ])
        grid.onOpen = { [weak self] o, v in self?.onOpen?(o, v) }
        grid.onDoubleClick = { [weak self] t in self?.onNewEvent?(t, .slot) }
        allDay.onClick = { [weak self] v in self?.onOpen?(v.bar.occurrence, v) }
        allDay.onDoubleClickDay = { [weak self] i in
            guard let self, i < self.allDay.days.count else { return }
            self.onNewEvent?(self.allDay.days[i], .allDay)
        }
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        timer?.invalidate()
        timer = nil
        guard window != nil else { return }
        // The now line moves each minute.
        let t = Timer(timeInterval: 60, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated {
                self?.grid.now = Date()
            }
        }
        RunLoop.main.add(t, forMode: .common)
        timer = t
        grid.now = Date()
    }

    /// Reads the controller's state (after its `onChange`): the range's
    /// days, the occurrences, `showDeclined`, the selection, the colours
    /// of the snapshot's calendars and the working hours.
    func update(controller c: CalendarController) {
        let snapshot = c.snapshot
        let env = c.env
        let zone = env.zone
        let days = c.range.days
        let colors = snapshot.calendarAccounts.flatMap { $0.calendars.map { $0.id.rawValue + $0.color } }
        let new = Input(days: days, occurrences: c.occurrences, showDeclined: c.showDeclined, colors: colors, zone: zone)
        scrollMinute = Cal.scrollMinute(snapshot.preferences.workingHours)
        if new != input {
            if input?.days != days {
                scrollPending = true
                autoScroll = nil
            }
            input = new
            let today = Cal.startOfDay(Date(), zone)
            header.set(days: days.map { d in
                let p = Cal.parts(d, zone)
                return CalendarDayHeaderView.Day(
                    weekday: Cal.Text.shortWeekday(d, env), number: Cal.Text.dayOfMonth(d, env), today: d == today,
                    weekend: p.weekday == .saturday || p.weekday == .sunday)
            })
            let color: (Occurrence) -> NSColor = { CalendarColor.of($0, in: snapshot) }
            let label: (Occurrence) -> String = { Cal.blockLabel($0, env: env) }
            let lanes = Cal.allDayLanes(c.occurrences, days: days, zone: zone, showDeclined: c.showDeclined)
            allDay.set(days: days, bars: lanes.bars, lanes: lanes.lanes, color: color, label: label, selection: c.selection)
            allDayHeight.constant = CalendarAllDayView.height(lanes: lanes.lanes)
            let blocks = days.map { Cal.layoutDay(c.occurrences, day: $0, zone: zone, showDeclined: c.showDeclined) }
            grid.set(days: days, blocks: blocks, env: env, color: color, label: label, selection: c.selection)
        } else {
            grid.setSelection(c.selection)
            allDay.setSelection(c.selection)
        }
        needsLayout = true
    }

    override func layout() {
        super.layout()
        // The header and the all-day row leave the scroller's width at the
        // right so that their columns stay over the grid's.
        let inset = scroll.frame.width - scroll.contentView.frame.width
        header.trailingInset = max(0, inset)
        allDay.trailingInset = max(0, inset)
        let width = scroll.contentView.bounds.width
        if grid.frame.width != width {
            grid.frame = NSRect(x: 0, y: 0, width: width, height: CalendarTimeGridView.height)
        }
        if let a = autoScroll, !scrollPending, abs(scroll.contentView.bounds.height - a.clipHeight) > 0.5 {
            DispatchQueue.main.async { [weak self] in
                self?.scrollToWorkingHours()
            }
        }
        if scrollPending, window != nil, !isHiddenOrHasHiddenAncestor, scroll.contentView.bounds.height > 0 {
            scrollPending = false
            // After this layout pass: the clip view and the document have
            // their final sizes then, and nothing resets the position.
            DispatchQueue.main.async { [weak self] in
                self?.scrollToWorkingHours()
            }
        }
    }

    override func viewDidUnhide() {
        super.viewDidUnhide()
        needsLayout = true
    }

    /// Puts the working hours' start (`Cal.scrollMinute`) at the top of the
    /// visible grid.
    private func scrollToWorkingHours() {
        let clip = scroll.contentView
        guard clip.bounds.height > 0 else {
            scrollPending = true
            return
        }
        // The hour labels are centred on their lines (14 pt high): the first
        // line sits that half and a few points below the top, its label whole.
        let y = max(0, CGFloat(scrollMinute) * CalendarMetrics.hourHeight / 60 - 10)
        let maxY = max(0, CalendarTimeGridView.height - clip.bounds.height)
        applyingScroll = true
        clip.scroll(to: NSPoint(x: 0, y: min(y, maxY)))
        scroll.reflectScrolledClipView(clip)
        applyingScroll = false
        autoScroll = (clip.bounds.minY, clip.bounds.height)
    }

    /// The user scrolled (not a resize, not ours): the offset is theirs.
    @objc private func clipScrolled() {
        guard !applyingScroll, let a = autoScroll else { return }
        let clip = scroll.contentView
        if abs(clip.bounds.height - a.clipHeight) <= 0.5, abs(clip.bounds.minY - a.y) > 0.5 {
            autoScroll = nil
        }
    }

    /// The view of the occurrence `key` on screen, for the popover's
    /// anchor (nil: not shown).
    func view(for key: String) -> NSView? {
        if let b = grid.blocks.first(where: { $0.key == key }) {
            return b
        }
        return allDay.bars.first { $0.key == key && !$0.isHidden }
    }

    var developmentFrames: [String] {
        func r(_ v: NSView) -> String { NSStringFromRect(v.convert(v.bounds, to: nil)) }
        var lines = [
            "  calendar day header \(r(header)) days \(header.days.count)",
            "  calendar all-day \(r(allDay)) lanes \(allDay.lanes) bars \(allDay.bars.count)",
            "  calendar grid scroll \(r(scroll)) document \(NSStringFromRect(grid.frame)) visible y \(Int(scroll.contentView.bounds.minY))",
            "  calendar blocks \(grid.blocks.count)",
        ]
        if let b = grid.blocks.first {
            lines.append("  calendar first block \(r(b)) '\(b.accessibilityLabel() ?? "")'")
        }
        return lines
    }
}

/// The time grid inside the Week and Day views' scroll view: the hour
/// axis, the hour and half-hour lines, the day columns (today's tinted),
/// the event blocks as subviews and the red now line with its dot. A
/// double click on empty time asks for New Event at that half hour.
@MainActor
final class CalendarTimeGridView: NSView {
    static let height = CGFloat(Cal.dayMinutes) * CalendarMetrics.hourHeight / 60

    var onOpen: ((Occurrence, NSView) -> Void)?
    var onDoubleClick: ((Date) -> Void)?

    private(set) var days: [Date] = []
    private(set) var blocks: [EventBlockView] = []
    private var dayBlocks: [[Cal.Block]] = []
    private var env = Cal.Env(zone: .current, locale: .current)
    private var hourLabels: [NSTextField] = []
    /// The now line, a subview above the blocks.
    private let nowLine = NowLineView()
    var now = Date() {
        didSet { needsLayout = true }
    }

    override var isFlipped: Bool { true }

    func set(
        days: [Date], blocks new: [[Cal.Block]], env: Cal.Env, color: (Occurrence) -> NSColor,
        label: (Occurrence) -> String, selection: String?
    ) {
        self.days = days
        self.env = env
        dayBlocks = new
        blocks.forEach { $0.removeFromSuperview() }
        let wide = days.count == 1
        blocks = new.flatMap { $0 }.map { b in
            let v = EventBlockView(block: b, color: color(b.occurrence), label: label(b.occurrence), env: env, wide: wide)
            v.selected = v.key == selection
            v.onClick = { [weak self] v in self?.onOpen?(v.block.occurrence, v) }
            addSubview(v)
            return v
        }
        // Above every block.
        addSubview(nowLine, positioned: .above, relativeTo: nil)
        makeHourLabels()
        needsLayout = true
        needsDisplay = true
    }

    func setSelection(_ key: String?) {
        for b in blocks {
            b.selected = b.key == key
        }
    }

    /// The hours' labels "01:00" … "23:00" in the axis, in the user's
    /// time format.
    private func makeHourLabels() {
        guard hourLabels.isEmpty, let day = days.first else { return }
        let p = Cal.parts(day, env.zone)
        for h in 1..<24 {
            let l = calendarLabel(CalendarMetrics.smallFont, color: .secondaryLabelColor)
            l.alignment = .right
            l.stringValue = Cal.Text.time(Cal.date(p.year, p.month, p.day, h, in: env.zone), env)
            addSubview(l)
            hourLabels.append(l)
        }
    }

    private var columnWidth: CGFloat {
        (bounds.width - CalendarMetrics.axisWidth) / CGFloat(max(1, days.count))
    }

    private func y(_ minute: Int) -> CGFloat { CGFloat(minute) * CalendarMetrics.hourHeight / 60 }

    override func layout() {
        super.layout()
        for (i, l) in hourLabels.enumerated() {
            let yy = y((i + 1) * 60)
            l.frame = NSRect(x: 0, y: yy - 7, width: CalendarMetrics.axisWidth - 8, height: 14)
        }
        let cw = columnWidth
        var index = 0
        for (d, list) in dayBlocks.enumerated() {
            for b in list {
                guard index < blocks.count else { break }
                let v = blocks[index]
                index += 1
                let inner = cw - 4
                let w = inner / CGFloat(max(1, b.columns))
                let x = CalendarMetrics.axisWidth + CGFloat(d) * cw + 1 + CGFloat(b.column) * w
                v.frame = NSRect(x: x, y: y(b.top) + 1, width: max(4, w - 2), height: max(4, y(b.bottom) - y(b.top) - 2))
            }
        }
        // The now line, red, with a dot at its left, over today's column.
        let zone = env.zone
        if let first = days.first, let last = days.last,
           let line = Cal.nowLine(now, Cal.Range(from: first, to: Cal.addDays(last, 1, zone), zone: zone), zone: zone)
        {
            nowLine.isHidden = false
            nowLine.frame = NSRect(
                x: CalendarMetrics.axisWidth + CGFloat(line.day) * cw - 4, y: y(line.minute) - 4, width: cw + 4, height: 8)
        } else {
            nowLine.isHidden = true
        }
    }

    override func draw(_ dirtyRect: NSRect) {
        let cw = columnWidth
        let zone = env.zone
        let today = Cal.startOfDay(now, zone)
        for (i, d) in days.enumerated() where d == today {
            Tint.accent.withAlphaComponent(0.05).setFill()
            NSRect(x: CalendarMetrics.axisWidth + CGFloat(i) * cw, y: 0, width: cw, height: bounds.height).fill()
        }
        NSColor.separatorColor.setFill()
        for h in 1..<24 {
            NSRect(x: CalendarMetrics.axisWidth - 4, y: y(h * 60), width: bounds.width, height: 1).fill()
        }
        NSColor.separatorColor.withAlphaComponent(0.4).setFill()
        for h in 0..<24 {
            NSRect(x: CalendarMetrics.axisWidth, y: y(h * 60 + 30), width: bounds.width, height: 1).fill()
        }
        NSColor.separatorColor.setFill()
        for i in 0..<max(1, days.count) {
            NSRect(x: CalendarMetrics.axisWidth + CGFloat(i) * cw, y: 0, width: 1, height: bounds.height).fill()
        }
    }

    override func mouseDown(with event: NSEvent) {
        guard event.clickCount == 2, !days.isEmpty else { return }
        let p = convert(event.locationInWindow, from: nil)
        guard p.x >= CalendarMetrics.axisWidth else { return }
        let i = min(days.count - 1, Int((p.x - CalendarMetrics.axisWidth) / columnWidth))
        let minute = max(0, min(Cal.dayMinutes - 1, Int(p.y / CalendarMetrics.hourHeight * 60)))
        let d = Cal.parts(days[i], env.zone)
        // `.slot` rounds it down to the half hour.
        onDoubleClick?(Cal.date(d.year, d.month, d.day, minute / 60, minute % 60, in: env.zone))
    }
}

/// The red now line with its dot at the left, drawn above the blocks.
@MainActor
private final class NowLineView: NSView {
    override func draw(_ dirtyRect: NSRect) {
        NSColor.systemRed.setFill()
        NSRect(x: 4, y: bounds.midY - 0.75, width: bounds.width - 4, height: 1.5).fill()
        NSBezierPath(ovalIn: NSRect(x: 0, y: bounds.midY - 4, width: 8, height: 8)).fill()
    }

    override func hitTest(_ point: NSPoint) -> NSView? { nil }
}
