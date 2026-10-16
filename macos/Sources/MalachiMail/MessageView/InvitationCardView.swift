// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The invitation card above a message's header (docs/calendar.md §5.7,
/// artboard 4), in the reading pane, a message window and a conversation
/// card, drawn from an `InvitationController` (the parsed part of
/// message.get, never its HTML): the date tile at the start; the pill and
/// the bold title; the clock line (with the repetition), the previous time
/// of an update, the location with Join Video Call (through the link
/// confirmation of the host, `InvitationController.onOpenLink`), the people
/// line, the conflict strip, the mini day strip; then "Add to:" (a popup of
/// the targets, a label when the server chose the calendar, nothing
/// without targets) with Accept / Tentative / Decline (its menu has Decline
/// Without Reply), the reply note and Propose New Time…. After answering
/// the state strip with Change Response and Show in Calendar replaces the
/// buttons. A reply message is one line ("Jana Nováková accepted"), a
/// cancellation says so and offers Remove from Calendar. Every string is a
/// label's `stringValue` (or a title of a control); the card is hidden for
/// none.
@MainActor
final class InvitationCardView: NSView {
    private weak var controller: InvitationController?

    private let tile = InvitationDateTile()
    private let pill = PillLabel()
    private let titleLabel = NSTextField(wrappingLabelWithString: "")
    private let clockLabel = InvitationCardView.line()
    /// An update's former values (`Cal.previousLines`), one grey line each.
    private let previous = FillStackView()
    private let locationLabel = InvitationCardView.line()
    private let joinButton = NSButton()
    private let peopleLabel = InvitationCardView.line(secondary: true)
    private let conflictSymbol = CalloutCard.symbolView()
    private let conflictLabel = InvitationCardView.line()
    private let dayStrip = InvitationDayStripView()
    private let stateLabel = InvitationCardView.line(secondary: true)

    private let addToLabel = NSTextField(labelWithString: "")
    private let targetPopup = NSPopUpButton(frame: .zero, pullsDown: false)
    private let targetLabel = NSTextField(labelWithString: "")
    private let acceptButton = NSButton()
    private let tentativeButton = NSButton()
    private let declineButton = NSComboButton()
    private let proposeButton = NSButton()
    private let removeButton = NSButton()
    private let spinner = Spinner(size: 16)

    private let checkSymbol = CalloutCard.symbolView()
    private let stripLabel = InvitationCardView.line()
    private let changeButton = NSPopUpButton(frame: .zero, pullsDown: true)
    private let showButton = NSButton()
    /// Show in Calendar beside the state line (the user's own event).
    private let stateShowButton = NSButton()
    private var stateRow: NSStackView!

    private let notes = FillStackView()
    private let errorLabel = InvitationCardView.line()

    private var titleRow: NSStackView!
    private var locationRow: NSStackView!
    private var conflictRow: NSStackView!
    private var actionsRow: NSStackView!
    private var stripRow: NSStackView!
    private var cancelRow: NSStackView!
    private var column: FillStackView!

    private var columnAfterTile: NSLayoutConstraint!
    private var columnAtStart: NSLayoutConstraint!
    private var tileHeight: NSLayoutConstraint!

    private var proposal: NSPopover?
    /// The targets the popup shows, in its order.
    private var targetIDs: [CalendarID] = []

    /// Around the card inside the view (the host's insets).
    private let margins: NSEdgeInsets

    init(margins: NSEdgeInsets) {
        self.margins = margins
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        build()
        isHidden = true
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    private static func line(secondary: Bool = false) -> NSTextField {
        let l = NSTextField(wrappingLabelWithString: "")
        l.font = secondary ? Typo.caption : Typo.body
        l.textColor = secondary ? Tint.secondary : .labelColor
        l.isSelectable = true
        l.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        return l
    }

    private static func row(_ views: [NSView], spacing: CGFloat = 8) -> NSStackView {
        let rest = NSView()
        rest.translatesAutoresizingMaskIntoConstraints = false
        rest.setContentHuggingPriority(NSLayoutConstraint.Priority(1), for: .horizontal)
        rest.setContentCompressionResistancePriority(NSLayoutConstraint.Priority(1), for: .horizontal)
        let s = NSStackView(views: views + [rest])
        s.orientation = .horizontal
        s.alignment = .centerY
        s.spacing = spacing
        s.setHuggingPriority(.defaultLow, for: .horizontal)
        return s
    }

    private func button(_ b: NSButton, _ title: String, _ action: Selector) {
        b.title = title
        b.bezelStyle = .push
        b.controlSize = .regular
        b.target = self
        b.action = action
        b.setContentHuggingPriority(.required, for: .horizontal)
        b.setContentCompressionResistancePriority(.required, for: .horizontal)
    }

    private func build() {
        pill.font = Typo.caption
        titleLabel.font = Typo.heading
        titleLabel.isSelectable = true
        titleLabel.maximumNumberOfLines = 2
        titleLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        titleRow = Self.row([pill, titleLabel])
        titleRow.alignment = .firstBaseline

        joinButton.isBordered = false
        joinButton.setButtonType(.momentaryChange)
        joinButton.attributedTitle = NSAttributedString(
            string: Cal.joinVideoCall, attributes: [.font: Typo.body, .foregroundColor: NSColor.linkColor])
        joinButton.target = self
        joinButton.action = #selector(joinClicked)
        joinButton.setContentHuggingPriority(.required, for: .horizontal)
        joinButton.setContentCompressionResistancePriority(.required, for: .horizontal)
        locationRow = Self.row([locationLabel, joinButton])
        locationRow.alignment = .firstBaseline

        conflictLabel.font = Typo.caption
        conflictRow = Self.row([conflictSymbol, conflictLabel], spacing: 6)

        addToLabel.stringValue = Cal.addTo
        addToLabel.textColor = Tint.secondary
        addToLabel.setContentHuggingPriority(.required, for: .horizontal)
        targetPopup.target = self
        targetPopup.action = #selector(targetChosen)
        targetPopup.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        targetLabel.lineBreakMode = .byTruncatingTail
        targetLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        button(acceptButton, Cal.responseAction(.accepted), #selector(acceptClicked))
        acceptButton.bezelColor = .controlAccentColor
        button(tentativeButton, Cal.responseAction(.tentative), #selector(tentativeClicked))
        declineButton.title = Cal.responseAction(.declined)
        declineButton.style = .split
        declineButton.target = self
        declineButton.action = #selector(declineClicked)
        let declineMenu = NSMenu()
        let without = NSMenuItem(title: Cal.declineWithoutReply, action: #selector(declineWithoutReply), keyEquivalent: "")
        without.target = self
        declineMenu.addItem(without)
        declineButton.menu = declineMenu
        declineButton.setContentHuggingPriority(.required, for: .horizontal)
        declineButton.setContentCompressionResistancePriority(.required, for: .horizontal)
        actionsRow = Self.row([addToLabel, targetPopup, targetLabel, acceptButton, tentativeButton, declineButton, spinner])
        // The buttons go to the end: the popup takes the width that is left.
        actionsRow.setCustomSpacing(16, after: targetLabel)

        button(proposeButton, Cal.proposeNewTime, #selector(proposeClicked))
        proposeButton.bezelStyle = .accessoryBarAction
        proposeButton.controlSize = .small

        checkSymbol.image = NSImage(systemSymbolName: "checkmark.circle.fill", accessibilityDescription: nil)
        checkSymbol.contentTintColor = .systemGreen
        changeButton.pullsDown = true
        changeButton.bezelStyle = .push
        changeButton.setContentHuggingPriority(.required, for: .horizontal)
        button(showButton, Cal.showInCalendar, #selector(showClicked))
        stripRow = Self.row([checkSymbol, stripLabel, changeButton, showButton])
        button(stateShowButton, Cal.showInCalendar, #selector(showClicked))
        stateRow = Self.row([stateLabel, stateShowButton])
        stateRow.alignment = .firstBaseline
        stripLabel.font = Typo.bodyMedium

        button(removeButton, Cal.removeFromCalendar, #selector(removeClicked))
        cancelRow = Self.row([removeButton])

        notes.spacing = 2
        previous.spacing = 2
        errorLabel.textColor = .systemRed
        errorLabel.font = Typo.caption

        column = FillStackView(fillingViews: [
            titleRow, clockLabel, previous, locationRow, peopleLabel, conflictRow, dayStrip, stateRow,
            actionsRow, stripRow, cancelRow, notes, proposeButtonRow(), errorLabel,
        ])
        column.spacing = 4
        column.setCustomSpacing(8, after: dayStrip)
        column.setCustomSpacing(6, after: conflictRow)

        // The tile at the start, the column to the card's trailing inset;
        // the card is at least as tall as the tile. Without a tile (a
        // reply's one line) the column starts at the leading inset.
        let outer = NSView()
        outer.translatesAutoresizingMaskIntoConstraints = false
        column.translatesAutoresizingMaskIntoConstraints = false
        outer.addSubview(tile)
        outer.addSubview(column)
        columnAfterTile = column.leadingAnchor.constraint(equalTo: tile.trailingAnchor, constant: 12)
        columnAtStart = column.leadingAnchor.constraint(equalTo: outer.leadingAnchor)
        tileHeight = outer.bottomAnchor.constraint(greaterThanOrEqualTo: tile.bottomAnchor)
        let fit = outer.bottomAnchor.constraint(equalTo: column.bottomAnchor)
        fit.priority = .defaultHigh
        NSLayoutConstraint.activate([
            tile.topAnchor.constraint(equalTo: outer.topAnchor),
            tile.leadingAnchor.constraint(equalTo: outer.leadingAnchor),
            column.topAnchor.constraint(equalTo: outer.topAnchor),
            column.trailingAnchor.constraint(equalTo: outer.trailingAnchor),
            outer.bottomAnchor.constraint(greaterThanOrEqualTo: column.bottomAnchor),
            fit, columnAfterTile, tileHeight,
        ])
        CalloutCard.install(outer, in: self, margins: margins)
        setAccessibilityElement(true)
        setAccessibilityRole(.group)
    }

    private var proposeRow: NSStackView?

    private func proposeButtonRow() -> NSStackView {
        let r = Self.row([proposeButton])
        proposeRow = r
        return r
    }

    // MARK: Showing

    /// Shows the card of `controller` (nil: hidden). Call `render` again
    /// after each of its changes (its `onChange` belongs to the host).
    func show(_ controller: InvitationController?) {
        if controller !== self.controller {
            proposal?.close()
            proposal = nil
        }
        self.controller = controller
        render()
    }

    func render() {
        guard let c = controller, c.mode != .none else {
            isHidden = true
            return
        }
        isHidden = false
        let mode = c.mode
        let card = c.card
        let full = mode == .full
        let cancelled = mode == .cancelled
        let reply = mode == .reply

        setAccessibilityLabel(c.title)
        InvitationPill.show(c.pill, in: pill, emphasized: false)
        // A reply's card is its one line beside the pill.
        let title = reply ? c.replyCard : c.title
        titleLabel.font = reply ? Typo.body : Typo.heading
        titleLabel.stringValue = title
        titleLabel.isHidden = title.isEmpty
        if cancelled {
            titleLabel.attributedStringValue = NSAttributedString(string: c.title, attributes: [
                .font: Typo.heading, .foregroundColor: NSColor.labelColor,
                .strikethroughStyle: NSUnderlineStyle.single.rawValue,
            ])
        }
        let tileValue = reply ? nil : c.dateTile
        tile.show(tileValue, colour: InvitationPill.colours(c.pill == .none ? .invitation : c.pill).text)
        let hasTile = tileValue != nil
        // The one going off first: both on at once would conflict.
        (hasTile ? columnAtStart : columnAfterTile).isActive = false
        (hasTile ? columnAfterTile : columnAtStart).isActive = true
        tileHeight.isActive = hasTile

        set(clockLabel, reply ? "" : Cal.cardClockLine(card, env: c.env))
        fill(previous, full ? Cal.previousLines(card, env: c.env) : [])
        locationLabel.stringValue = full ? c.location : ""
        locationLabel.isHidden = locationLabel.stringValue.isEmpty
        joinButton.isHidden = !full || (card.videoCall?.url ?? "").isEmpty
        if !c.videoHost.isEmpty {
            joinButton.toolTip = c.videoHost
        }
        locationRow.isHidden = locationLabel.isHidden && joinButton.isHidden
        set(peopleLabel, full ? c.peopleLine : "")

        let live = full && c.showsResponses
        let conflicts = live || (full && c.showsStateStrip)
        conflictRow.isHidden = !conflicts
        if conflicts {
            let has = !card.conflicts.isEmpty
            conflictLabel.stringValue = c.conflictLine
            conflictLabel.textColor = has ? .systemOrange : Tint.secondary
            conflictSymbol.image = NSImage(
                systemSymbolName: has ? "exclamationmark.triangle.fill" : "checkmark", accessibilityDescription: nil)
            conflictSymbol.contentTintColor = has ? .systemOrange : Tint.secondary
        }
        dayStrip.show(conflicts ? Cal.dayStripOf(card, env: c.env) : nil)

        // A line for an invitation that cannot be answered (outdated, or
        // not from here); a cancellation's comes as the state line too.
        set(stateLabel, reply ? "" : c.stateLine)
        // "You organise this event." with Show in Calendar on its row.
        stateShowButton.isHidden = stateLabel.isHidden || c.showsStateStrip || !Cal.showsInCalendar(card)
        stateRow.isHidden = stateLabel.isHidden

        actionsRow.isHidden = !live
        if live {
            renderTargets(c)
        }
        for b in [acceptButton, tentativeButton] {
            b.isEnabled = !c.busy
        }
        declineButton.isEnabled = !c.busy
        if c.busy {
            spinner.start()
        } else {
            spinner.stop()
        }
        spinner.isHidden = !c.busy

        stripRow.isHidden = !(full && c.showsStateStrip)
        if !stripRow.isHidden {
            stripLabel.stringValue = c.stateStrip
            renderChangeMenu(c)
            changeButton.isHidden = !card.canRespond
            changeButton.isEnabled = !c.busy
            showButton.isHidden = !Cal.showsInCalendar(card)
        }

        cancelRow.isHidden = !cancelled || card.eventId == nil || c.cancelApplied
        removeButton.isEnabled = !c.busy

        notes.arrangedSubviews.forEach {
            notes.removeArrangedSubview($0)
            $0.removeFromSuperview()
        }
        if live {
            for line in c.replyNote {
                let l = Self.line(secondary: true)
                l.stringValue = line
                if card.organizerMismatch, line == c.replyNote.last {
                    l.textColor = .systemOrange
                }
                notes.addArrangedSubview(l)
            }
        }
        notes.isHidden = notes.arrangedSubviews.isEmpty
        proposeRow?.isHidden = !(full && c.showsProposeNewTime)
        proposeButton.isEnabled = !c.busy

        set(errorLabel, c.error)
        needsLayout = true
    }

    /// `stack` holds one grey label per line (hidden for none).
    private func fill(_ stack: FillStackView, _ lines: [String]) {
        let have = stack.arrangedSubviews.compactMap { ($0 as? NSTextField)?.stringValue }
        if have != lines {
            stack.arrangedSubviews.forEach {
                stack.removeArrangedSubview($0)
                $0.removeFromSuperview()
            }
            for line in lines {
                let l = Self.line(secondary: true)
                l.stringValue = line
                stack.addArrangedSubview(l)
            }
        }
        stack.isHidden = lines.isEmpty
    }

    private func set(_ l: NSTextField, _ text: String) {
        l.stringValue = text
        l.isHidden = text.isEmpty
    }

    private func renderTargets(_ c: InvitationController) {
        let choices = c.targets
        addToLabel.isHidden = choices.isEmpty
        if c.targetIsPopup {
            targetPopup.isHidden = false
            targetLabel.isHidden = true
            let ids = choices.map(\.id)
            if ids != targetIDs || targetPopup.numberOfItems != ids.count {
                targetPopup.removeAllItems()
                for t in choices {
                    // The title is set on the item: no menu-title parsing.
                    let item = NSMenuItem(title: t.title, action: nil, keyEquivalent: "")
                    targetPopup.menu?.addItem(item)
                }
                targetIDs = ids
            }
            if let t = c.target, let i = ids.firstIndex(of: t) {
                targetPopup.selectItem(at: i)
            }
            targetPopup.isEnabled = !c.busy
        } else {
            targetPopup.isHidden = true
            let t = choices.first(where: { $0.id == c.target }) ?? choices.first
            targetLabel.stringValue = t?.title ?? ""
            targetLabel.isHidden = t == nil
        }
    }

    private func renderChangeMenu(_ c: InvitationController) {
        let menu = NSMenu()
        // The pull-down's first item is its title.
        menu.addItem(NSMenuItem(title: Cal.changeResponse, action: nil, keyEquivalent: ""))
        for (i, r) in Cal.responseChoices.enumerated() {
            let item = NSMenuItem(title: Cal.responseAction(r), action: #selector(changeChosen(_:)), keyEquivalent: "")
            item.target = self
            item.tag = i
            item.state = c.card.response == r ? .on : .off
            menu.addItem(item)
        }
        menu.addItem(.separator())
        let without = NSMenuItem(title: Cal.declineWithoutReply, action: #selector(changeChosen(_:)), keyEquivalent: "")
        without.target = self
        without.tag = -1
        menu.addItem(without)
        changeButton.menu = menu
    }

    // MARK: Actions

    @objc private func joinClicked() { controller?.joinVideoCall() }

    @objc private func targetChosen() {
        let i = targetPopup.indexOfSelectedItem
        guard i >= 0, i < targetIDs.count else { return }
        controller?.setTarget(targetIDs[i])
    }

    @objc private func acceptClicked() { controller?.respond(.accepted) }
    @objc private func tentativeClicked() { controller?.respond(.tentative) }
    @objc private func declineClicked() { controller?.respond(.declined) }
    @objc private func declineWithoutReply() { controller?.respond(.declined, withoutReply: true) }

    @objc private func changeChosen(_ sender: NSMenuItem) {
        if sender.tag < 0 {
            controller?.respond(.declined, withoutReply: true)
        } else if sender.tag < Cal.responseChoices.count {
            controller?.respond(Cal.responseChoices[sender.tag])
        }
    }

    @objc private func showClicked() { controller?.showInCalendar() }
    @objc private func removeClicked() { controller?.applyCancel() }

    /// Propose New Time…: a small popover with the start and the end; the
    /// answer goes as Tentative with the proposal.
    @objc private func proposeClicked() {
        guard let c = controller, let start = c.card.start, let end = c.card.end else { return }
        let vc = InvitationProposalViewController(start: start, end: end, zone: c.env.zone)
        let popover = NSPopover()
        popover.behavior = .transient
        popover.contentViewController = vc
        vc.onPropose = { [weak self, weak popover] s, e in
            popover?.close()
            self?.controller?.proposeNewTime(start: s, end: e)
        }
        vc.onCancel = { [weak popover] in popover?.close() }
        proposal = popover
        popover.show(relativeTo: proposeButton.bounds, of: proposeButton, preferredEdge: .maxY)
    }
}

/// The date tile at the start of the card: the weekday's abbreviation on a
/// band in the pill's colour, the day's number large, the month's
/// abbreviation under it.
@MainActor
private final class InvitationDateTile: NSView {
    private let weekday = NSTextField(labelWithString: "")
    private let day = NSTextField(labelWithString: "")
    private let month = NSTextField(labelWithString: "")
    private let band = NSView()
    private var colour: NSColor = .systemBlue
    static let width: CGFloat = 52

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        wantsLayer = true
        layer?.cornerRadius = 8
        layer?.cornerCurve = .continuous
        layer?.masksToBounds = true
        layer?.borderWidth = 1
        band.wantsLayer = true
        band.translatesAutoresizingMaskIntoConstraints = false
        weekday.font = .systemFont(ofSize: Typo.captionSize, weight: .semibold)
        weekday.textColor = .white
        weekday.alignment = .center
        day.font = .monospacedDigitSystemFont(ofSize: 24, weight: .semibold)
        day.alignment = .center
        month.font = Typo.caption
        month.textColor = Tint.secondary
        month.alignment = .center
        for l in [weekday, day, month] {
            l.translatesAutoresizingMaskIntoConstraints = false
            l.lineBreakMode = .byClipping
            l.isSelectable = false
        }
        addSubview(band)
        band.addSubview(weekday)
        addSubview(day)
        addSubview(month)
        NSLayoutConstraint.activate([
            widthAnchor.constraint(equalToConstant: Self.width),
            band.topAnchor.constraint(equalTo: topAnchor),
            band.leadingAnchor.constraint(equalTo: leadingAnchor),
            band.trailingAnchor.constraint(equalTo: trailingAnchor),
            weekday.topAnchor.constraint(equalTo: band.topAnchor, constant: 2),
            weekday.bottomAnchor.constraint(equalTo: band.bottomAnchor, constant: -2),
            weekday.leadingAnchor.constraint(equalTo: band.leadingAnchor, constant: 2),
            weekday.trailingAnchor.constraint(equalTo: band.trailingAnchor, constant: -2),
            day.topAnchor.constraint(equalTo: band.bottomAnchor, constant: 2),
            day.leadingAnchor.constraint(equalTo: leadingAnchor),
            day.trailingAnchor.constraint(equalTo: trailingAnchor),
            month.topAnchor.constraint(equalTo: day.bottomAnchor),
            month.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 2),
            month.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -2),
            bottomAnchor.constraint(equalTo: month.bottomAnchor, constant: 6),
        ])
        setAccessibilityElement(false)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    func show(_ t: Cal.DateTile?, colour: NSColor) {
        isHidden = t == nil
        guard let t else { return }
        weekday.stringValue = t.weekday
        day.stringValue = t.day
        month.stringValue = t.month
        self.colour = colour
        needsDisplay = true
    }

    override var wantsUpdateLayer: Bool { true }

    override func updateLayer() {
        layer?.backgroundColor = NSColor.controlBackgroundColor.cgColor
        layer?.borderColor = NSColor.separatorColor.cgColor
        band.layer?.backgroundColor = colour.cgColor
    }
}

/// The popover of Propose New Time…: Starts and Ends, Cancel and Propose.
@MainActor
private final class InvitationProposalViewController: NSViewController {
    var onPropose: (@MainActor (Date, Date) -> Void)?
    var onCancel: (@MainActor () -> Void)?
    private let startPicker = NSDatePicker()
    private let endPicker = NSDatePicker()
    private let proposeButton = NSButton()
    private let start: Date
    private let end: Date
    private let zone: TimeZone

    init(start: Date, end: Date, zone: TimeZone) {
        self.start = start
        self.end = end
        self.zone = zone
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func loadView() {
        let heading = NSTextField(labelWithString: Cal.proposeNewTime)
        heading.font = Typo.heading
        for (p, value) in [(startPicker, start), (endPicker, end)] {
            p.datePickerStyle = .textFieldAndStepper
            p.datePickerElements = [.yearMonthDay, .hourMinute]
            p.timeZone = zone
            p.dateValue = value
            p.target = self
            p.action = #selector(changed)
        }
        let grid = NSGridView(views: [
            [NSTextField(labelWithString: Cal.fieldLabel(.start)), startPicker],
            [NSTextField(labelWithString: Cal.fieldLabel(.end)), endPicker],
        ])
        grid.column(at: 0).xPlacement = .trailing
        grid.rowAlignment = .firstBaseline
        grid.rowSpacing = 6
        grid.columnSpacing = 8
        let cancel = NSButton(title: Cal.cancel, target: self, action: #selector(cancelClicked))
        cancel.keyEquivalent = "\u{1b}"
        proposeButton.title = Cal.propose
        proposeButton.bezelStyle = .push
        proposeButton.target = self
        proposeButton.action = #selector(proposeClicked)
        proposeButton.keyEquivalent = "\r"
        let buttons = NSStackView(views: [cancel, proposeButton])
        buttons.spacing = 8
        let stack = NSStackView(views: [heading, grid, buttons])
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 10
        stack.edgeInsets = NSEdgeInsets(top: 12, left: 14, bottom: 12, right: 14)
        buttons.setHuggingPriority(.defaultLow, for: .horizontal)
        stack.setCustomSpacing(14, after: grid)
        view = stack
        changed()
    }

    @objc private func changed() {
        proposeButton.isEnabled = endPicker.dateValue > startPicker.dateValue
    }

    @objc private func cancelClicked() { onCancel?() }

    @objc private func proposeClicked() {
        guard endPicker.dateValue > startPicker.dateValue else { return }
        onPropose?(startPicker.dateValue, endPicker.dateValue)
    }
}
