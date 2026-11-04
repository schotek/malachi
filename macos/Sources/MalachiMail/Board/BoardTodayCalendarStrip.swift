// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The Board → Today strip "Today in Calendar" (docs/calendar.md §5.9,
/// Go today.go through `Cal.todayRows`): today's occurrences of the
/// window's calendar source in the agenda's order, each with its time
/// range (or All Day), its calendar's colour dot and its title, then
/// "took place" for one that is over, Respond for an unanswered invitation
/// or the place; "Open Calendar ›" at the right of the heading. Hidden
/// (`isShown` false) while no account has the calendar capability.
///
/// Respond selects the invitation's message in Mail (its card answers
/// there) rather than a popover with the card's actions: the simpler of
/// the two the plan allows. Every text is a cleaned plain string of
/// `Cal`, shown through `stringValue`.
@MainActor
final class BoardTodayCalendarStrip: NSView {
    /// Visibility or the number of rows changed: the table measures the
    /// strip again.
    var onChange: (@MainActor () -> Void)?
    /// Open Calendar ›.
    var onOpenCalendar: (@MainActor () -> Void)?
    /// Respond of an unanswered invitation's row.
    var onRespond: (@MainActor (MessageRef) -> Void)?

    /// Some account has the calendar capability.
    private(set) var isShown = false

    private weak var source: (any CalendarDataSource)?
    private var token: CalendarObserverToken?
    private var rows: [Cal.TodayRow] = []
    private var loadedDay: Date?
    private var loading = false
    private var dayTimer: Timer?
    private let env = Cal.Env(zone: .current, locale: .current)

    private let heading = NSTextField(labelWithString: "")
    private let openButton = NSButton()
    private let stack = NSStackView()

    private static let padding = BoardMetrics.listPaddingH
    private static var headingHeight: CGFloat { BoardMetrics.lineHeight(Typo.caption) + 14 }
    private static var rowHeight: CGFloat { max(BoardMetrics.lineHeight(Typo.body), 20) + 6 }
    private static let bottom: CGFloat = 10
    private static let timeWidth: CGFloat = 96

    init() {
        super.init(frame: .zero)
        heading.font = .systemFont(ofSize: Typo.captionSize, weight: .semibold)
        heading.textColor = Tint.secondary
        heading.isSelectable = false
        heading.stringValue = Cal.todayHeading.uppercased(with: .current)
        heading.setAccessibilityLabel(Cal.todayHeading)
        heading.translatesAutoresizingMaskIntoConstraints = false

        openButton.title = Cal.openCalendar + " ›"
        openButton.isBordered = false
        openButton.bezelStyle = .inline
        openButton.font = .systemFont(ofSize: Typo.captionSize, weight: .medium)
        openButton.contentTintColor = Tint.accent
        openButton.setAccessibilityLabel(Cal.openCalendar)
        openButton.target = self
        openButton.action = #selector(openCalendar(_:))
        openButton.translatesAutoresizingMaskIntoConstraints = false

        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 0
        stack.translatesAutoresizingMaskIntoConstraints = false

        addSubview(heading)
        addSubview(openButton)
        addSubview(stack)
        NSLayoutConstraint.activate([
            heading.leadingAnchor.constraint(equalTo: leadingAnchor, constant: Self.padding),
            heading.topAnchor.constraint(equalTo: topAnchor, constant: 8),
            openButton.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -Self.padding),
            openButton.centerYAnchor.constraint(equalTo: heading.centerYAnchor),
            openButton.leadingAnchor.constraint(greaterThanOrEqualTo: heading.trailingAnchor, constant: 8),
            stack.topAnchor.constraint(equalTo: topAnchor, constant: Self.headingHeight),
            stack.leadingAnchor.constraint(equalTo: leadingAnchor, constant: Self.padding),
            stack.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -Self.padding),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// MALACHI_START (the development script) is set: the strip prints its
    /// frame on stderr after each layout that moved it.
    private static let traces = ProcessInfo.processInfo.environment["MALACHI_START"] != nil
    private var traced = ""

    override func layout() {
        super.layout()
        trace()
    }

    /// `LAYOUT: today-strip <frame in window> shown <bool> rows <n>`.
    func trace() {
        guard Self.traces else { return }
        let frame = window == nil ? "no window" : NSStringFromRect(convert(bounds, to: nil))
        let line = "LAYOUT: today-strip \(frame) shown \(isShown) rows \(rows.count) source \(source == nil ? "none" : "attached")"
        guard line != traced else { return }
        traced = line
        FileHandle.standardError.write(Data((line + "\n").utf8))
    }

    /// The height the table gives the strip's row (0 while hidden).
    var preferredHeight: CGFloat {
        guard isShown else { return 0 }
        return Self.headingHeight + CGFloat(max(rows.count, 1)) * Self.rowHeight + Self.bottom
    }

    /// Follows `source` (the window's calendar source) from now on.
    func attach(_ source: any CalendarDataSource) {
        guard self.source !== source else { return }
        token?.cancel()
        self.source = source
        loadedDay = nil
        token = source.observe { [weak self] change in
            self?.sourceChanged(change)
        }
        if dayTimer == nil {
            // Midnight brings another day, and "took place" moves on.
            dayTimer = Timer.scheduledTimer(withTimeInterval: 60, repeats: true) { [weak self] _ in
                MainActor.assumeIsolated {
                    self?.tick()
                }
            }
        }
        update()
    }

    /// The board shows Today again: the rows as they are now.
    func refresh() {
        update()
    }

    private func sourceChanged(_ change: Cal.SourceChange) {
        switch change {
        case .snapshot:
            // The accounts, the visible calendars, the invitations
            // (notify.invitationsChanged reloads the snapshot).
            loadedDay = nil
            update()
        case .occurrences(let from, let to):
            let day = Cal.startOfDay(Date(), env.zone)
            let end = Cal.addDays(day, 1, env.zone)
            if (to.map { day < $0 } ?? true) && (from.map { end > $0 } ?? true) {
                loadedDay = nil
                update()
            }
        case .event, .alert, .error:
            break
        }
    }

    private func tick() {
        guard isShown else { return }
        if loadedDay != Cal.startOfDay(Date(), env.zone) {
            loadedDay = nil
        }
        update()
    }

    /// Shows or hides the strip, and loads today's occurrences when they
    /// are not loaded for today.
    private func update() {
        guard let source else { return }
        let shown = Cal.showsTodayStrip(source.snapshot.accounts)
        if shown != isShown {
            isShown = shown
            isHidden = !shown
            onChange?()
        }
        trace()
        guard shown else { return }
        let now = Date()
        let day = Cal.startOfDay(now, env.zone)
        let range = Cal.Range(from: day, to: Cal.addDays(day, 1, env.zone), zone: env.zone)
        if let hit = source.cachedOccurrences(range) {
            loadedDay = day
            show(hit, now: now)
            return
        }
        if loadedDay == day {
            show(nil, now: now)
            return
        }
        guard !loading else { return }
        loading = true
        Task { [weak self] in
            let result = try? await source.occurrences(range)
            guard let self else { return }
            self.loading = false
            guard let result else { return }
            self.loadedDay = day
            self.show(result, now: Date())
        }
    }

    private var lastResult: EventListResult?

    private func show(_ result: EventListResult?, now: Date) {
        if let result {
            lastResult = result
        }
        guard let source, let r = lastResult else { return }
        let snapshot = source.snapshot
        let visible = snapshot.visibleCalendars
        let occurrences = r.occurrences.filter { o in o.calendarId.map(visible.contains) ?? true }
        let next = Cal.todayRows(occurrences, now: now, env: env, showDeclined: snapshot.preferences.showDeclined)
        let countChanged = next.count != rows.count
        rows = next
        rebuild(snapshot)
        if countChanged {
            onChange?()
        }
    }

    private func rebuild(_ snapshot: Cal.Snapshot) {
        for v in stack.arrangedSubviews {
            stack.removeArrangedSubview(v)
            v.removeFromSuperview()
        }
        guard !rows.isEmpty else {
            let empty = NSTextField(labelWithString: Cal.nothingToday)
            empty.font = Typo.body
            empty.textColor = Tint.secondary
            empty.isSelectable = false
            empty.heightAnchor.constraint(equalToConstant: Self.rowHeight).isActive = true
            stack.addArrangedSubview(empty)
            return
        }
        for (i, row) in rows.enumerated() {
            let v = makeRow(row, index: i, color: row.occurrence.calendarId.flatMap { snapshot.calendar($0) }?.color)
            stack.addArrangedSubview(v)
            v.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
        }
    }

    private func makeRow(_ row: Cal.TodayRow, index: Int, color: String?) -> NSView {
        let time = NSTextField(labelWithString: row.time)
        time.font = .monospacedDigitSystemFont(ofSize: Typo.bodySize, weight: .regular)
        time.textColor = row.tookPlace ? Tint.secondary : .labelColor
        time.lineBreakMode = .byTruncatingTail
        time.isSelectable = false
        time.widthAnchor.constraint(equalToConstant: Self.timeWidth).isActive = true

        let dot = BoardTodayCalendarDot(color: CalendarColor.color(color))

        let title = NSTextField(labelWithString: row.title)
        title.font = Typo.body
        title.textColor = row.tookPlace ? Tint.secondary : .labelColor
        title.lineBreakMode = .byTruncatingTail
        title.maximumNumberOfLines = 1
        title.isSelectable = false
        title.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        title.setContentHuggingPriority(.defaultLow, for: .horizontal)

        var views: [NSView] = [time, dot, title]
        if row.tookPlace {
            views.append(trailingLabel(Cal.tookPlace))
        } else if row.respond, row.occurrence.invitation != nil {
            let b = NSButton(title: Cal.respond, target: self, action: #selector(respond(_:)))
            b.bezelStyle = .push
            b.controlSize = .small
            b.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
            b.tag = index
            b.setContentHuggingPriority(.required, for: .horizontal)
            b.setContentCompressionResistancePriority(.required, for: .horizontal)
            views.append(b)
        } else if !row.place.isEmpty {
            let place = trailingLabel(row.place)
            place.setContentCompressionResistancePriority(.defaultLow - 1, for: .horizontal)
            views.append(place)
        }
        let h = NSStackView(views: views)
        h.orientation = .horizontal
        h.alignment = .centerY
        h.spacing = 8
        h.distribution = .fill
        h.heightAnchor.constraint(equalToConstant: Self.rowHeight).isActive = true
        h.setAccessibilityElement(true)
        h.setAccessibilityRole(.group)
        h.setAccessibilityLabel([row.time, row.title, row.tookPlace ? Cal.tookPlace : row.place]
            .filter { !$0.isEmpty }.joined(separator: ", "))
        return h
    }

    private func trailingLabel(_ text: String) -> NSTextField {
        let l = NSTextField(labelWithString: text)
        l.font = Typo.caption
        l.textColor = Tint.secondary
        l.lineBreakMode = .byTruncatingTail
        l.maximumNumberOfLines = 1
        l.isSelectable = false
        l.alignment = .right
        return l
    }

    @objc private func openCalendar(_ sender: Any?) {
        onOpenCalendar?()
    }

    @objc private func respond(_ sender: NSButton) {
        guard rows.indices.contains(sender.tag), let ref = rows[sender.tag].occurrence.invitation else { return }
        onRespond?(ref)
    }
}

/// A calendar's colour dot.
@MainActor
private final class BoardTodayCalendarDot: NSView {
    private let color: NSColor

    init(color: NSColor) {
        self.color = color
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            widthAnchor.constraint(equalToConstant: 8),
            heightAnchor.constraint(equalToConstant: 8),
        ])
        setAccessibilityElement(false)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func draw(_ dirtyRect: NSRect) {
        color.setFill()
        NSBezierPath(ovalIn: bounds).fill()
    }
}
