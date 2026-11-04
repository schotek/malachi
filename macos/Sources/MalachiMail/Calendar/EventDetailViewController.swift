// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The content of the event detail popover (docs/calendar.md §5.5,
/// artboard 3) over an `EventDetailController`: the header line (the
/// calendar's colour, "Work · Calendar", the account's kind capsule), the
/// title, rows with SF Symbols (time, recurrence, place, video call,
/// people with the attendee list and the "You: …" pull-down, notes with
/// their links, Show Invitation in Mail), the footer (Edit, Remove from
/// Calendar or Delete, ✦ ▾) and the inline error under it.
///
/// Every string from the server goes in through `stringValue` (the title
/// is selectable) or as the plain string of the notes' text view, whose
/// only attributes are the daemon's links (`Detail.links`), which go to the
/// controller's `openLink` and so through "Open This Link?". The view keeps
/// the keyboard: Escape closes, Return is Edit, ⌫ Remove (`Cal.keyFor`).
@MainActor
final class EventDetailViewController: NSViewController, NSTextViewDelegate {
    static let width: CGFloat = 380
    static let inset: CGFloat = 16
    static let rowSpacing: CGFloat = 10
    static let iconWidth: CGFloat = 18
    static let iconGap: CGFloat = 10
    /// The width of a row's text.
    static var contentWidth: CGFloat { width - 2 * inset - iconWidth - iconGap }
    /// The notes scroll beyond this height.
    static let notesMaxHeight: CGFloat = 160

    let controller: EventDetailController
    private let snapshot: @MainActor () -> Cal.Snapshot?
    private let canAsk: @MainActor () -> Bool

    var onEdit: (@MainActor () -> Void)?
    var onFindAnotherTime: (@MainActor () -> Void)?
    var onClose: (@MainActor () -> Void)?

    private let stack = NSStackView()
    private var rendered: Rendered?

    init(
        controller: EventDetailController, snapshot: @escaping @MainActor () -> Cal.Snapshot?,
        canAsk: @escaping @MainActor () -> Bool
    ) {
        self.controller = controller
        self.snapshot = snapshot
        self.canAsk = canAsk
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func loadView() {
        let root = EventDetailRootView()
        root.onKey = { [weak self] event in
            self?.handleKey(event) ?? false
        }
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = Self.rowSpacing
        stack.edgeInsets = NSEdgeInsets(top: Self.inset, left: Self.inset, bottom: Self.inset, right: Self.inset)
        stack.detachesHiddenViews = true
        stack.translatesAutoresizingMaskIntoConstraints = false
        root.addSubview(stack)
        NSLayoutConstraint.activate([
            root.widthAnchor.constraint(equalToConstant: Self.width),
            stack.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: root.trailingAnchor),
            stack.topAnchor.constraint(equalTo: root.topAnchor),
            stack.bottomAnchor.constraint(equalTo: root.bottomAnchor),
        ])
        view = root
    }

    /// Puts the keyboard on the popover's content (Escape, Return, ⌫).
    func takeKeyboard() {
        view.window?.makeFirstResponder(view)
    }

    // MARK: Building

    /// What the view shows; the rows are built again only when it changes.
    private struct Rendered: Equatable {
        var detail: Cal.Detail?
        var occurrence: Occurrence?
        var color: String?
        var badge: String
        var shown: Int
        var showAll: String
        var loading: Bool
        var busy: Bool
        var error: String
        var canAsk: Bool
    }

    /// After every change of the controller.
    func update() {
        _ = view
        let c = controller
        let d = c.detail
        let o = c.event?.occurrence ?? c.occurrence
        let snap = snapshot()
        let color = o?.calendarId.flatMap { snap?.calendar($0)?.color }
        let badge = o.flatMap { snap?.account($0.accountId) }.map(accountHeaderBadge) ?? ""
        let r = Rendered(
            detail: d, occurrence: o, color: color, badge: badge, shown: c.shownAttendees.count,
            showAll: c.showAllAttendees, loading: c.loading, busy: c.busy, error: c.error, canAsk: canAsk())
        guard r != rendered else { return }
        rendered = r
        rebuild(r)
        let fitting = view.fittingSize
        preferredContentSize = NSSize(width: Self.width, height: fitting.height)
    }

    private func rebuild(_ r: Rendered) {
        for v in stack.arrangedSubviews {
            stack.removeArrangedSubview(v)
            v.removeFromSuperview()
        }
        let env = controller.env
        guard let d = r.detail else {
            // Until the event arrives: the occurrence's title and time.
            if let o = r.occurrence {
                add(header(line: "", color: r.color, badge: r.badge))
                add(title(Board.cleanLine(o.title, max: API.Limits.maxEventTitleBytes)))
                add(row("clock", texts: timeLines(o, fallback: Cal.timeLine(o, env: env))))
            }
            if r.loading {
                let spinner = NSProgressIndicator()
                spinner.style = .spinning
                spinner.controlSize = .small
                spinner.startAnimation(nil)
                add(spinner)
            }
            addError(r.error)
            return
        }
        add(header(line: d.calendarLine, color: r.color, badge: r.badge))
        add(title(d.title))
        if let o = r.occurrence {
            add(row("clock", texts: timeLines(o, fallback: d.timeLine)))
        } else if !d.timeLine.isEmpty {
            add(row("clock", texts: [d.timeLine]))
        }
        if !d.recurrence.isEmpty {
            add(row("repeat", texts: [d.recurrence]))
        }
        if !d.location.isEmpty {
            add(row("mappin.and.ellipse", texts: [d.location]))
        }
        if !d.videoCall.isEmpty {
            add(row("link", content: videoCallView(d)))
        }
        if !d.peopleLine.isEmpty || d.canRespond {
            add(row("person.2", content: peopleView(d, r)))
        }
        if !d.notes.isEmpty {
            add(row("note.text", content: notesView(d)))
        }
        if d.showInvitation {
            let b = linkButton(Cal.showInvitation, action: #selector(showInvitation(_:)))
            add(row("envelope", content: b))
        }
        add(footer(d, r))
        addError(r.error)
    }

    private func add(_ v: NSView) {
        stack.addArrangedSubview(v)
    }

    private func addError(_ text: String) {
        guard !text.isEmpty else { return }
        let l = wrapping(text, font: .systemFont(ofSize: NSFont.smallSystemFontSize), color: .systemRed)
        l.preferredMaxLayoutWidth = Self.width - 2 * Self.inset
        add(l)
    }

    // MARK: Pieces

    /// The colour square, "Work · Calendar" and the kind capsule.
    private func header(line: String, color: String?, badge: String) -> NSView {
        let square = ColorSquare(color: CalendarColor.color(color))
        let label = calendarLabel(.systemFont(ofSize: NSFont.smallSystemFontSize), color: .secondaryLabelColor)
        label.stringValue = line
        label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        let kind = PillLabel()
        kind.font = Typo.sidebarKindBadge
        kind.textColor = .secondaryLabelColor
        kind.stringValue = badge
        kind.isHidden = badge.isEmpty
        let row = NSStackView(views: [square, label, kind])
        row.orientation = .horizontal
        row.alignment = .centerY
        row.spacing = 6
        row.detachesHiddenViews = true
        return row
    }

    /// The title: bold 17 pt, selectable, wrapping.
    private func title(_ s: String) -> NSView {
        let l = wrapping(s.isEmpty ? Cal.Text.untitled : s, font: .systemFont(ofSize: 17, weight: .bold))
        l.isSelectable = true
        l.preferredMaxLayoutWidth = Self.width - 2 * Self.inset
        return l
    }

    /// The time row's lines: a timed event within one day as its date and
    /// "10:00–11:30 · 1 h 30 min", anything else as the detail's one line.
    private func timeLines(_ o: Occurrence, fallback: String) -> [String] {
        let env = controller.env
        guard !o.allDay, let (s, e) = Cal.span(o, env.zone),
              Cal.startOfDay(s, env.zone) == Cal.startOfDay(e.addingTimeInterval(-1), env.zone) || s == e
        else {
            return [fallback]
        }
        let times = Cal.Text.dot(Cal.Text.timeRange(s, e, env), Cal.durationText(e.timeIntervalSince(s)))
        return [Cal.Text.longDate(s, env), times]
    }

    /// Join Video Call and the host in grey.
    private func videoCallView(_ d: Cal.Detail) -> NSView {
        let b = linkButton(Cal.joinVideoCall, action: #selector(joinVideoCall(_:)))
        let host = calendarLabel(.systemFont(ofSize: NSFont.systemFontSize), color: .secondaryLabelColor)
        host.stringValue = d.videoHost
        host.isHidden = d.videoHost.isEmpty
        host.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        let row = NSStackView(views: [b, host])
        row.orientation = .horizontal
        row.alignment = .firstBaseline
        row.spacing = 6
        row.detachesHiddenViews = true
        return row
    }

    /// "Organiser Jana Nováková · 4 attendees", the attendees (collapsed
    /// beyond `Cal.maxAttendeesShown`) and "You: Accepted ▾".
    private func peopleView(_ d: Cal.Detail, _ r: Rendered) -> NSView {
        let col = NSStackView()
        col.orientation = .vertical
        col.alignment = .leading
        col.spacing = 4
        if !d.peopleLine.isEmpty {
            col.addArrangedSubview(wrapping(d.peopleLine, font: .systemFont(ofSize: NSFont.systemFontSize)))
        }
        for a in controller.shownAttendees {
            col.addArrangedSubview(EventDetailAttendeeRow(a, width: Self.contentWidth))
        }
        if !r.showAll.isEmpty {
            col.addArrangedSubview(linkButton(r.showAll, action: #selector(showAllAttendees(_:)), small: true))
        }
        if d.canRespond {
            col.setCustomSpacing(8, after: col.arrangedSubviews.last ?? col)
            col.addArrangedSubview(responsePullDown(d, busy: r.busy))
        }
        return col
    }

    /// "You: Accepted ▾": Accept, Tentative, Decline, Decline Without Reply.
    private func responsePullDown(_ d: Cal.Detail, busy: Bool) -> NSView {
        let p = NSPopUpButton(frame: .zero, pullsDown: true)
        p.controlSize = .small
        p.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
        p.addItem(withTitle: Cal.myResponseText(d.myResponse))
        for (i, choice) in Cal.responseChoices.enumerated() {
            let item = NSMenuItem(title: Cal.responseAction(choice), action: #selector(respond(_:)), keyEquivalent: "")
            item.target = self
            item.tag = i
            item.state = choice == d.myResponse ? .on : .off
            p.menu?.addItem(item)
        }
        p.menu?.addItem(.separator())
        let without = NSMenuItem(title: Cal.declineWithoutReply, action: #selector(declineWithoutReply(_:)), keyEquivalent: "")
        without.target = self
        p.menu?.addItem(without)
        p.isEnabled = !busy
        return p
    }

    /// The notes: plain text in a grey, non-editable text view; the
    /// daemon's links clickable.
    private func notesView(_ d: Cal.Detail) -> NSView {
        let v = EventDetailNotesView(text: d.notes, links: d.links, width: Self.contentWidth, maxHeight: Self.notesMaxHeight)
        v.onLink = { [weak self] url in
            self?.controller.openLink(url)
        }
        return v
    }

    /// Edit, Remove from Calendar / Delete, a spacer and ✦ ▾.
    private func footer(_ d: Cal.Detail, _ r: Rendered) -> NSView {
        let edit = NSButton(title: Cal.edit, target: self, action: #selector(edit(_:)))
        edit.bezelStyle = .push
        edit.isEnabled = d.canEdit && !r.busy
        edit.isHidden = !d.canEdit
        let remove = NSButton(title: d.removeTitle, target: self, action: #selector(remove(_:)))
        remove.bezelStyle = .push
        remove.isEnabled = d.canRemove && !r.busy
        remove.isHidden = !d.canRemove
        let spacer = NSView()
        spacer.setContentHuggingPriority(.init(1), for: .horizontal)
        let assistant = NSPopUpButton(frame: .zero, pullsDown: true)
        assistant.bezelStyle = .push
        // The pull-down's title item: the symbol alone (NSMenuItem() would
        // be titled "NSMenuItem").
        let head = NSMenuItem(title: "", action: nil, keyEquivalent: "")
        head.image = NSImage(systemSymbolName: "sparkles", accessibilityDescription: L10n.T("Assistant"))
        assistant.menu?.addItem(head)
        let ask = NSMenuItem(title: Cal.askAboutEvent, action: #selector(askAssistant(_:)), keyEquivalent: "")
        ask.target = self
        // The panel exists only with the Assistant shown and In App chosen.
        ask.isHidden = !r.canAsk
        assistant.menu?.addItem(ask)
        let find = NSMenuItem(title: Cal.findAnotherTime, action: #selector(findAnotherTime(_:)), keyEquivalent: "")
        find.target = self
        find.isHidden = !d.canEdit
        assistant.menu?.addItem(find)
        assistant.imagePosition = .imageOnly
        assistant.isHidden = !r.canAsk && !d.canEdit
        assistant.isEnabled = !r.busy
        assistant.setAccessibilityLabel(L10n.T("Assistant"))
        let row = NSStackView(views: [edit, remove, spacer, assistant])
        row.orientation = .horizontal
        row.alignment = .centerY
        row.spacing = 8
        row.detachesHiddenViews = true
        row.translatesAutoresizingMaskIntoConstraints = false
        row.widthAnchor.constraint(equalToConstant: Self.width - 2 * Self.inset).isActive = true
        return row
    }

    /// A row: the symbol in its column, then the content.
    private func row(_ symbol: String, content: NSView) -> NSView {
        let icon = calendarSymbol(symbol, pointSize: 13)
        icon.translatesAutoresizingMaskIntoConstraints = false
        let column = NSView()
        column.translatesAutoresizingMaskIntoConstraints = false
        column.addSubview(icon)
        NSLayoutConstraint.activate([
            column.widthAnchor.constraint(equalToConstant: Self.iconWidth),
            column.heightAnchor.constraint(equalToConstant: 17),
            icon.centerXAnchor.constraint(equalTo: column.centerXAnchor),
            icon.centerYAnchor.constraint(equalTo: column.centerYAnchor),
        ])
        let h = NSStackView(views: [column, content])
        h.orientation = .horizontal
        h.alignment = .top
        h.spacing = Self.iconGap
        return h
    }

    /// A row of wrapping lines.
    private func row(_ symbol: String, texts: [String]) -> NSView {
        let col = NSStackView()
        col.orientation = .vertical
        col.alignment = .leading
        col.spacing = 2
        for (i, t) in texts.enumerated() where !t.isEmpty {
            col.addArrangedSubview(wrapping(
                t, font: .systemFont(ofSize: NSFont.systemFontSize), color: i == 0 ? .labelColor : .secondaryLabelColor))
        }
        return row(symbol, content: col)
    }

    private func wrapping(_ s: String, font: NSFont, color: NSColor = .labelColor) -> NSTextField {
        let l = NSTextField(wrappingLabelWithString: "")
        l.stringValue = s
        l.font = font
        l.textColor = color
        l.isSelectable = false
        l.preferredMaxLayoutWidth = Self.contentWidth
        return l
    }

    private func linkButton(_ title: String, action: Selector, small: Bool = false) -> NSButton {
        let b = NSButton(title: title, target: self, action: action)
        b.isBordered = false
        b.bezelStyle = .inline
        b.contentTintColor = .linkColor
        b.font = .systemFont(ofSize: small ? NSFont.smallSystemFontSize : NSFont.systemFontSize)
        b.attributedTitle = NSAttributedString(
            string: title, attributes: [.foregroundColor: NSColor.linkColor, .font: b.font as Any])
        return b
    }

    // MARK: Actions

    @objc private func edit(_ sender: Any?) {
        onEdit?()
    }

    @objc private func remove(_ sender: Any?) {
        controller.remove()
    }

    @objc private func respond(_ sender: NSMenuItem) {
        guard Cal.responseChoices.indices.contains(sender.tag) else { return }
        controller.respond(Cal.responseChoices[sender.tag])
    }

    @objc private func declineWithoutReply(_ sender: Any?) {
        controller.respond(.declined, withoutReply: true)
    }

    @objc private func showAllAttendees(_ sender: Any?) {
        controller.expandAttendees()
    }

    @objc private func joinVideoCall(_ sender: Any?) {
        controller.joinVideoCall()
    }

    @objc private func showInvitation(_ sender: Any?) {
        controller.showInvitation()
    }

    @objc private func askAssistant(_ sender: Any?) {
        controller.askAssistant()
    }

    @objc private func findAnotherTime(_ sender: Any?) {
        onFindAnotherTime?()
    }

    // MARK: Keys

    /// Escape closes, Return is Edit, ⌫ Remove from Calendar / Delete, by
    /// the calendar's key rules (an occurrence is selected: the popover's).
    private func handleKey(_ event: NSEvent) -> Bool {
        guard let action = Cal.keyFor(Self.press(event), context: Cal.KeyContext(calendarMode: true, selected: true)) else {
            return false
        }
        switch action {
        case .escape:
            onClose?()
        case .openDetail:
            guard controller.detail?.canEdit == true else { return false }
            onEdit?()
        case .remove:
            guard controller.detail?.canRemove == true else { return false }
            controller.remove()
        default:
            return false
        }
        return true
    }

    /// The key as the calendar's rules read it.
    static func press(_ e: NSEvent) -> Cal.Press {
        let flags = e.modifierFlags.intersection(.deviceIndependentFlagsMask)
        var special: Cal.Special = .none
        switch e.keyCode {
        case 36, 76: special = .return
        case 51, 117: special = .delete
        case 53: special = .escape
        case 123: special = .left
        case 124: special = .right
        case 125: special = .down
        case 126: special = .up
        default: break
        }
        let rune = special == .none ? e.charactersIgnoringModifiers?.first : nil
        return Cal.Press(
            rune: rune, special: special, primary: flags.contains(.command), alt: flags.contains(.option),
            control: flags.contains(.control), shift: flags.contains(.shift))
    }
}

/// The popover's root view: takes the keyboard and hands keys to the view
/// controller first.
@MainActor
final class EventDetailRootView: NSView {
    var onKey: ((NSEvent) -> Bool)?

    override var acceptsFirstResponder: Bool { true }

    override func keyDown(with event: NSEvent) {
        if onKey?(event) == true {
            return
        }
        super.keyDown(with: event)
    }

    override func cancelOperation(_ sender: Any?) {
        // Escape from a button or the notes, which do not take it.
        let esc = NSEvent.keyEvent(
            with: .keyDown, location: .zero, modifierFlags: [], timestamp: 0, windowNumber: 0, context: nil,
            characters: "\u{1b}", charactersIgnoringModifiers: "\u{1b}", isARepeat: false, keyCode: 53)
        if let esc, onKey?(esc) == true {
            return
        }
        super.cancelOperation(sender)
    }
}
