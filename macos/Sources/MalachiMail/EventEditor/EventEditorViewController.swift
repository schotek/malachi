// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The form of a New Event / Edit Event window (docs/calendar.md §5.6,
/// artboard 5) over `EventEditorController`: a label column (110 pt, right
/// aligned) and the fields — Title, Calendar with All Day, Starts and Ends
/// with the time-zone link, Repeat (Custom… a popover), Location with Add
/// Video Call ▾, Attendees (the compose window's `RecipientTokenField` and
/// `RecipientSuggestionsController`, unchanged), Availability (only when
/// the calendar's account answers free/busy), Alert, Notes (plain text) and
/// the attachment chips — then the inline errors and the footer (the note
/// of who sends the invitations, Cancel, the primary button). Every string
/// from the server is plain text (`stringValue`, plain `NSTextView`).
@MainActor
final class EventEditorViewController: NSViewController, NSTextFieldDelegate, NSTextViewDelegate, NSPopoverDelegate {
    static let labelWidth: CGFloat = 110
    static let notesHeight: CGFloat = 72

    let state: AppState
    let controller: EventEditorController

    /// Cancel (the window asks when there are changes).
    var onCancel: (@MainActor () -> Void)?
    /// Reload after a conflict (the manager reopens the event).
    var onReload: (@MainActor () -> Void)?

    let titleField = NSTextField()
    private let calendarPopup = NSPopUpButton(frame: .zero, pullsDown: false)
    private let allDaySwitch = NSSwitch()
    private let startDate = NSDatePicker()
    private let startTime = NSDatePicker()
    private let endDate = NSDatePicker()
    private let endTime = NSDatePicker()
    private let zoneButton = NSButton(title: "", target: nil, action: nil)
    private let repeatPopup = NSPopUpButton(frame: .zero, pullsDown: false)
    private let locationField = NSTextField()
    private let videoButton = NSPopUpButton(frame: .zero, pullsDown: true)
    private let attendeeField = RecipientTokenField()
    private var suggestions: RecipientSuggestionsController?
    private let availabilityView = AvailabilityView()
    private let summaryLabel = NSTextField(wrappingLabelWithString: "")
    private let nextSlotButton = NSButton(title: Cal.nextFreeSlotButton, target: nil, action: nil)
    private let freeSlotLabel = NSTextField(labelWithString: "")
    private let alertPopup = NSPopUpButton(frame: .zero, pullsDown: false)
    private let notesView = NSTextView()
    private let chips = AttachmentChipsView()
    private let errorLabel = NSTextField(wrappingLabelWithString: "")
    private let reloadButton = NSButton(title: "", target: nil, action: nil)
    private let errorRow = NSStackView()
    private let footerLabel = NSTextField(wrappingLabelWithString: "")
    private let cancelButton = NSButton(title: Cal.cancel, target: nil, action: nil)
    /// Edit mode only, when the event can go: Delete / Remove from Calendar.
    private let deleteButton = NSButton(title: "", target: nil, action: nil)
    private let primaryButton = NSButton(title: "", target: nil, action: nil)

    private var grid = NSGridView()
    private var availabilityRow: NSGridRow?
    /// Notes and the chips under the grid: Notes takes the height left.
    private let notesBlock = NSView()
    private var calendarIDs: [CalendarID] = []
    private var calendarTitles: [String] = []
    private var alertValues: [Int] = []
    private var alertsAllDay = false
    private var zonePopover: NSPopover?
    private var repeatPopover: NSPopover?
    private var videoMenuKey = ""

    init(state: AppState, controller: EventEditorController) {
        self.state = state
        self.controller = controller
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// No popover or suggestion list is showing (Escape closes the window).
    var popupsHidden: Bool {
        zonePopover?.isShown != true && repeatPopover?.isShown != true && suggestions?.isVisible != true
    }

    // MARK: Building

    override func loadView() {
        let root = NSView(frame: NSRect(origin: .zero, size: EventEditorWindowController.defaultSize))
        buildFields()
        buildGrid()

        let document = FlippedView()
        document.translatesAutoresizingMaskIntoConstraints = false
        document.addSubview(grid)
        document.addSubview(notesBlock)
        let scroll = NSScrollView()
        scroll.translatesAutoresizingMaskIntoConstraints = false
        scroll.hasVerticalScroller = true
        scroll.autohidesScrollers = true
        scroll.drawsBackground = false
        scroll.documentView = document
        NSLayoutConstraint.activate([
            grid.topAnchor.constraint(equalTo: document.topAnchor, constant: 20),
            grid.leadingAnchor.constraint(equalTo: document.leadingAnchor, constant: 20),
            grid.trailingAnchor.constraint(equalTo: document.trailingAnchor, constant: -24),
            notesBlock.topAnchor.constraint(equalTo: grid.bottomAnchor, constant: grid.rowSpacing),
            notesBlock.leadingAnchor.constraint(equalTo: grid.leadingAnchor),
            notesBlock.trailingAnchor.constraint(equalTo: grid.trailingAnchor),
            document.bottomAnchor.constraint(equalTo: notesBlock.bottomAnchor, constant: 16),
            document.widthAnchor.constraint(equalTo: scroll.contentView.widthAnchor),
            // At least the visible height (Notes grows into it), more when
            // the form needs it (then it scrolls).
            document.heightAnchor.constraint(greaterThanOrEqualTo: scroll.contentView.heightAnchor),
            {
                let c = document.heightAnchor.constraint(equalTo: scroll.contentView.heightAnchor)
                c.priority = .defaultLow
                return c
            }(),
            document.topAnchor.constraint(equalTo: scroll.contentView.topAnchor),
            document.leadingAnchor.constraint(equalTo: scroll.contentView.leadingAnchor),
        ])

        let separator = NSBox()
        separator.boxType = .separator
        separator.translatesAutoresizingMaskIntoConstraints = false

        errorLabel.textColor = .systemRed
        errorLabel.font = Typo.body
        reloadButton.title = Cal.Text.reloadEvent
        reloadButton.bezelStyle = .rounded
        reloadButton.controlSize = .small
        reloadButton.target = self
        reloadButton.action = #selector(reloadClicked(_:))
        errorRow.orientation = .horizontal
        errorRow.alignment = .centerY
        errorRow.spacing = 10
        errorRow.addArrangedSubview(errorLabel)
        errorRow.addArrangedSubview(reloadButton)
        errorRow.isHidden = true

        footerLabel.textColor = .secondaryLabelColor
        footerLabel.font = .systemFont(ofSize: 11)
        footerLabel.maximumNumberOfLines = 2
        footerLabel.lineBreakMode = .byTruncatingTail
        footerLabel.cell?.truncatesLastVisibleLine = true
        footerLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        footerLabel.setContentHuggingPriority(.init(1), for: .horizontal)
        cancelButton.bezelStyle = .rounded
        cancelButton.target = self
        cancelButton.action = #selector(cancelClicked(_:))
        primaryButton.bezelStyle = .rounded
        primaryButton.keyEquivalent = "\r"
        // The default button (Return); ⌘↩ is the menu bar's Send.
        primaryButton.keyEquivalentModifierMask = []
        primaryButton.target = self
        primaryButton.action = #selector(primaryClicked(_:))
        for b in [cancelButton, primaryButton] {
            b.setContentHuggingPriority(.required, for: .horizontal)
            b.setContentCompressionResistancePriority(.required, for: .horizontal)
        }
        deleteButton.bezelStyle = .rounded
        deleteButton.hasDestructiveAction = true
        deleteButton.target = self
        deleteButton.action = #selector(deleteClicked(_:))
        deleteButton.setContentHuggingPriority(.required, for: .horizontal)
        deleteButton.setContentCompressionResistancePriority(.required, for: .horizontal)
        let bottom = NSStackView(views: [deleteButton, footerLabel, cancelButton, primaryButton])
        bottom.setCustomSpacing(16, after: deleteButton)
        bottom.orientation = .horizontal
        bottom.alignment = .centerY
        bottom.spacing = 8
        bottom.distribution = .fill
        bottom.setCustomSpacing(16, after: footerLabel)
        let footer = NSStackView(views: [errorRow, bottom])
        footer.orientation = .vertical
        footer.alignment = .leading
        footer.spacing = 8
        footer.edgeInsets = NSEdgeInsets(top: 10, left: 20, bottom: 14, right: 20)
        footer.translatesAutoresizingMaskIntoConstraints = false
        bottom.widthAnchor.constraint(equalTo: footer.widthAnchor, constant: -40).isActive = true

        root.addSubview(scroll)
        root.addSubview(separator)
        root.addSubview(footer)
        NSLayoutConstraint.activate([
            scroll.topAnchor.constraint(equalTo: root.safeAreaLayoutGuide.topAnchor),
            scroll.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            scroll.trailingAnchor.constraint(equalTo: root.trailingAnchor),
            separator.topAnchor.constraint(equalTo: scroll.bottomAnchor),
            separator.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            separator.trailingAnchor.constraint(equalTo: root.trailingAnchor),
            footer.topAnchor.constraint(equalTo: separator.bottomAnchor),
            footer.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            footer.trailingAnchor.constraint(equalTo: root.trailingAnchor),
            footer.bottomAnchor.constraint(equalTo: root.bottomAnchor),
        ])
        view = root

        controller.onChange = { [weak self] in self?.render() }
        render()
    }

    private func buildFields() {
        titleField.font = .systemFont(ofSize: 15)
        titleField.placeholderString = Cal.titlePlaceholder
        titleField.stringValue = controller.input.title
        titleField.delegate = self
        titleField.bezelStyle = .roundedBezel
        titleField.setAccessibilityLabel(Cal.titlePlaceholder)

        calendarPopup.target = self
        calendarPopup.action = #selector(calendarChosen(_:))
        calendarPopup.setAccessibilityLabel(Cal.fieldLabel(.calendar))
        allDaySwitch.target = self
        allDaySwitch.action = #selector(allDayToggled(_:))
        allDaySwitch.setAccessibilityLabel(Cal.fieldLabel(.allDay))

        for (p, elements) in [
            (startDate, NSDatePicker.ElementFlags.yearMonthDay), (endDate, .yearMonthDay),
            (startTime, .hourMinute), (endTime, .hourMinute),
        ] {
            p.datePickerStyle = .textField
            p.datePickerElements = elements
            p.isBezeled = true
            p.target = self
            p.action = #selector(dateChanged(_:))
            var c = Foundation.Calendar(identifier: .gregorian)
            c.timeZone = controller.input.zone
            p.calendar = c
            p.timeZone = controller.input.zone
        }
        startDate.setAccessibilityLabel(Cal.fieldLabel(.start))
        endDate.setAccessibilityLabel(Cal.fieldLabel(.end))
        zoneButton.isBordered = false
        zoneButton.contentTintColor = .linkColor
        zoneButton.font = Typo.caption
        zoneButton.target = self
        zoneButton.action = #selector(showZones(_:))

        repeatPopup.removeAllItems()
        for c in controller.repeatChoices {
            repeatPopup.addItem(withTitle: Cal.repeatTitle(c))
        }
        repeatPopup.target = self
        repeatPopup.action = #selector(repeatChosen(_:))
        repeatPopup.setAccessibilityLabel(Cal.fieldLabel(.repeat))

        locationField.stringValue = controller.input.location
        locationField.delegate = self
        locationField.bezelStyle = .roundedBezel
        locationField.setAccessibilityLabel(Cal.fieldLabel(.location))
        videoButton.target = self
        videoButton.action = #selector(videoChosen(_:))
        (videoButton.cell as? NSPopUpButtonCell)?.arrowPosition = .arrowAtBottom
        videoButton.setContentHuggingPriority(.required, for: .horizontal)

        attendeeField.setAccessibilityLabel(Cal.fieldLabel(.attendees))
        attendeeField.stringValue = controller.attendees.text
        let s = RecipientSuggestionsController(field: attendeeField, client: state.client) { [weak self] in
            guard let self else { return AccountID("") }
            return self.currentAccount ?? AccountID("")
        }
        s.onChanged = { [weak self] in self?.attendeesEdited() }
        suggestions = s

        summaryLabel.font = Typo.caption
        summaryLabel.textColor = .secondaryLabelColor
        nextSlotButton.isBordered = false
        nextSlotButton.contentTintColor = .linkColor
        nextSlotButton.font = Typo.caption
        nextSlotButton.target = self
        nextSlotButton.action = #selector(nextSlotClicked(_:))
        freeSlotLabel.font = Typo.caption
        freeSlotLabel.textColor = .secondaryLabelColor
        freeSlotLabel.lineBreakMode = .byTruncatingTail

        fillAlerts()
        alertPopup.target = self
        alertPopup.action = #selector(alertChosen(_:))
        alertPopup.setAccessibilityLabel(Cal.fieldLabel(.alert))

        notesView.isRichText = false
        notesView.importsGraphics = false
        notesView.allowsUndo = true
        notesView.isAutomaticQuoteSubstitutionEnabled = false
        notesView.isAutomaticLinkDetectionEnabled = false
        notesView.isAutomaticDataDetectionEnabled = false
        notesView.font = Typo.body
        notesView.textContainerInset = NSSize(width: 4, height: 4)
        notesView.string = controller.input.notes
        notesView.delegate = self
        notesView.setAccessibilityLabel(Cal.fieldLabel(.notes))

        chips.onRemove = { [weak self] id in self?.controller.removeAttachment(id) }
    }

    private func buildGrid() {
        func label(_ f: Cal.Field?) -> NSView {
            guard let f else { return NSGridCell.emptyContentView }
            let l = NSTextField(labelWithString: Cal.fieldLabel(f))
            l.font = Typo.body
            l.textColor = .secondaryLabelColor
            l.alignment = .right
            return l
        }
        func line(_ views: [NSView], spacer: Bool = false, fill: Bool = false) -> NSStackView {
            var all = views
            if spacer, let last = all.popLast() {
                let gap = NSView()
                gap.setContentHuggingPriority(.init(1), for: .horizontal)
                all += [gap, last]
            }
            let s = NSStackView(views: all)
            s.orientation = .horizontal
            s.alignment = .centerY
            s.spacing = 8
            if fill || spacer {
                s.distribution = .fill
            }
            return s
        }
        let allDayLabel = NSTextField(labelWithString: Cal.fieldLabel(.allDay))
        allDayLabel.font = Typo.body
        allDayLabel.textColor = .secondaryLabelColor
        calendarPopup.setContentHuggingPriority(.defaultHigh, for: .horizontal)
        let calendarLine = line([calendarPopup, allDayLabel, allDaySwitch])
        let gap = NSView()
        gap.setContentHuggingPriority(.init(1), for: .horizontal)
        calendarLine.insertArrangedSubview(gap, at: 1)
        calendarLine.distribution = .fill
        for v in [allDayLabel, allDaySwitch, startDate, startTime, endDate, endTime, zoneButton] as [NSView] {
            v.setContentHuggingPriority(.required, for: .horizontal)
        }
        locationField.setContentHuggingPriority(.defaultLow, for: .horizontal)

        let attendeeBox = EventEditorFieldFrame(content: attendeeField)

        let notesScroll = NSScrollView()
        notesScroll.documentView = notesView
        notesScroll.hasVerticalScroller = true
        notesScroll.autohidesScrollers = true
        notesScroll.borderType = .bezelBorder
        notesScroll.translatesAutoresizingMaskIntoConstraints = false
        notesScroll.heightAnchor.constraint(greaterThanOrEqualToConstant: Self.notesHeight).isActive = true
        let notesMin = notesScroll.heightAnchor.constraint(equalToConstant: Self.notesHeight)
        notesMin.priority = .init(1)
        notesMin.isActive = true
        notesView.frame = NSRect(x: 0, y: 0, width: 500, height: Self.notesHeight)
        notesView.minSize = NSSize(width: 0, height: Self.notesHeight)
        notesView.maxSize = NSSize(width: CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
        notesView.autoresizingMask = [.width]
        notesView.isVerticallyResizable = true
        notesView.isHorizontallyResizable = false
        notesView.textContainer?.widthTracksTextView = true

        let availability = NSStackView(views: [availabilityView, summaryLabel, line([nextSlotButton, freeSlotLabel])])
        availability.orientation = .vertical
        availability.alignment = .leading
        availability.spacing = 4
        availabilityView.widthAnchor.constraint(equalTo: availability.widthAnchor).isActive = true

        grid = NSGridView(views: [
            [label(nil), titleField],
            [label(.calendar), calendarLine],
            [label(.start), line([startDate, startTime, zoneButton], spacer: true)],
            [label(.end), line([endDate, endTime])],
            [label(.repeat), line([repeatPopup])],
            [label(.location), line([locationField, videoButton], fill: true)],
            [label(.attendees), attendeeBox],
            [label(.availability), availability],
            [label(.alert), line([alertPopup])],
        ])
        grid.translatesAutoresizingMaskIntoConstraints = false
        grid.rowSpacing = 10
        grid.columnSpacing = 10
        grid.column(at: 0).width = Self.labelWidth
        grid.column(at: 0).xPlacement = .trailing
        grid.column(at: 1).xPlacement = .fill
        for i in 0..<grid.numberOfRows {
            grid.row(at: i).yPlacement = .center
        }
        // The tall rows keep their label level with the first line.
        for i in [6, 7] {
            grid.row(at: i).yPlacement = .top
            grid.cell(atColumnIndex: 0, rowIndex: i).yPlacement = .top
        }
        grid.row(at: 0).bottomPadding = 4
        availabilityRow = grid.row(at: 7)
        let notesLabel = label(.notes)
        notesLabel.translatesAutoresizingMaskIntoConstraints = false
        let fieldColumn = NSStackView(views: [notesScroll, chips])
        fieldColumn.orientation = .vertical
        fieldColumn.alignment = .leading
        fieldColumn.spacing = 6
        fieldColumn.distribution = .fill
        fieldColumn.translatesAutoresizingMaskIntoConstraints = false
        fieldColumn.setHuggingPriority(.init(1), for: .vertical)
        notesBlock.translatesAutoresizingMaskIntoConstraints = false
        notesBlock.addSubview(notesLabel)
        notesBlock.addSubview(fieldColumn)
        NSLayoutConstraint.activate([
            notesLabel.trailingAnchor.constraint(equalTo: notesBlock.leadingAnchor, constant: Self.labelWidth),
            notesLabel.leadingAnchor.constraint(greaterThanOrEqualTo: notesBlock.leadingAnchor),
            notesLabel.topAnchor.constraint(equalTo: notesBlock.topAnchor, constant: 3),
            fieldColumn.leadingAnchor.constraint(equalTo: notesBlock.leadingAnchor, constant: Self.labelWidth + grid.columnSpacing),
            fieldColumn.trailingAnchor.constraint(equalTo: notesBlock.trailingAnchor),
            fieldColumn.topAnchor.constraint(equalTo: notesBlock.topAnchor),
            fieldColumn.bottomAnchor.constraint(equalTo: notesBlock.bottomAnchor),
            notesScroll.widthAnchor.constraint(equalTo: fieldColumn.widthAnchor),
            chips.widthAnchor.constraint(equalTo: fieldColumn.widthAnchor),
        ])
    }

    // MARK: Showing the state

    private var currentAccount: AccountID? {
        controller.calendars.first { $0.id == controller.input.calendarId }?.accountId
    }

    private func render() {
        let input = controller.input

        let choices = controller.calendars
        if choices.map(\.id) != calendarIDs || choices.map(\.title) != calendarTitles {
            calendarIDs = choices.map(\.id)
            calendarTitles = choices.map(\.title)
            calendarPopup.removeAllItems()
            for c in choices {
                calendarPopup.addItem(withTitle: c.title)
                calendarPopup.lastItem?.image = Self.swatch(CalendarColor.color(c.color))
            }
        }
        if let id = input.calendarId, let i = calendarIDs.firstIndex(of: id) {
            calendarPopup.selectItem(at: i)
        }
        calendarPopup.isEnabled = choices.count > 1

        allDaySwitch.state = input.allDay ? .on : .off
        for p in [startDate, startTime, endDate, endTime] where p.timeZone?.identifier != input.zone.identifier {
            var c = Foundation.Calendar(identifier: .gregorian)
            c.timeZone = input.zone
            p.calendar = c
            p.timeZone = input.zone
        }
        set(startDate, input.start)
        set(startTime, input.start)
        // An all-day event's end is the day after its last: Ends shows the last.
        let shownEnd = input.allDay ? Cal.addDays(Cal.startOfDay(input.end, input.zone), -1, input.zone) : input.end
        set(endDate, shownEnd)
        set(endTime, input.end)
        startTime.isHidden = input.allDay
        endTime.isHidden = input.allDay
        zoneButton.isHidden = input.allDay
        zoneButton.title = controller.timeZoneLink

        if let i = controller.repeatChoices.firstIndex(of: controller.repeatChoice) {
            repeatPopup.selectItem(at: i)
        }
        if alertsAllDay != input.allDay || !alertValues.contains(controller.alert) {
            fillAlerts()
        }
        if let i = alertValues.firstIndex(of: controller.alert) {
            alertPopup.selectItem(at: i)
        }
        renderVideo()

        let shows = controller.showsAvailability
        availabilityRow?.isHidden = !shows
        if shows {
            let day = controller.availabilityDay
            availabilityView.content = AvailabilityView.Content(
                rows: controller.availabilityRows, from: day.from, to: day.to, start: input.start, end: input.end,
                env: controller.env)
            summaryLabel.stringValue = controller.availabilitySummary.joined(separator: "\n")
            summaryLabel.isHidden = summaryLabel.stringValue.isEmpty
        }
        freeSlotLabel.stringValue = controller.freeSlotText
        nextSlotButton.isEnabled = !input.allDay

        let ids = controller.attachments.map(\.id)
        if ids != chips.ids {
            chips.removeAll()
            for a in controller.attachments {
                chips.add(a)
            }
        }

        var errors = controller.problemTexts
        if !controller.error.isEmpty {
            errors.append(controller.error)
        }
        errorLabel.stringValue = errors.joined(separator: "\n")
        errorRow.isHidden = errors.isEmpty
        var conflict = false
        if case .edit = controller.mode {
            conflict = controller.error == Cal.Text.errorText(.conflict)
        }
        reloadButton.isHidden = !conflict

        footerLabel.stringValue = controller.footer.joined(separator: "\n")
        primaryButton.title = controller.primaryLabel
        primaryButton.isEnabled = !controller.saving
        if let t = controller.deleteTitle {
            deleteButton.title = t
            deleteButton.isHidden = false
        } else {
            deleteButton.isHidden = true
        }
        deleteButton.isEnabled = !controller.saving
        primaryButton.toolTip = controller.primaryLabel + " (⌘↩)"
        view.window?.toolbar?.validateVisibleItems()
    }

    /// The Alert popup's items for the current All Day state (day-based at
    /// 9:00 for an all-day event), plus a stored value outside the list.
    private func fillAlerts() {
        alertsAllDay = controller.input.allDay
        alertValues = controller.alertChoices
        if !alertValues.contains(controller.alert) {
            alertValues.append(controller.alert)
        }
        alertPopup.removeAllItems()
        for m in alertValues {
            alertPopup.addItem(withTitle: controller.alertTitle(m))
        }
    }

    /// The Alert popup's items and selection (the `event-editor` DevStart step).
    var alertSummary: String {
        let items = alertValues.map { "\($0)=\(controller.alertTitle($0))" }.joined(separator: ", ")
        return "allDay \(controller.input.allDay) items [\(items)] selected '\(alertPopup.titleOfSelectedItem ?? "")'"
    }

    private func set(_ p: NSDatePicker, _ t: Date) {
        if p.dateValue != t {
            p.dateValue = t
        }
    }

    /// Add Video Call ▾: the calendar's providers, Paste Link…, and Remove
    /// once there is one; the title says what is chosen.
    private func renderVideo() {
        let providers = controller.videoProviders
        let v = controller.videoCall
        let key = "\(providers)|\(String(describing: v))"
        guard key != videoMenuKey else { return }
        videoMenuKey = key
        let title: String
        switch v?.mode {
        case .create?: title = Cal.videoProviderName(v?.provider ?? "")
        case .link?: title = URLComponents(string: v?.url ?? "")?.host ?? Cal.addVideoCall
        default: title = Cal.addVideoCall
        }
        videoButton.removeAllItems()
        videoButton.addItem(withTitle: title)
        for p in providers {
            videoButton.addItem(withTitle: Cal.videoProviderName(p))
            videoButton.lastItem?.representedObject = p
            videoButton.lastItem?.state = v?.mode == .create && v?.provider == p ? .on : .off
        }
        if !providers.isEmpty {
            videoButton.menu?.addItem(.separator())
        }
        videoButton.addItem(withTitle: Cal.pasteLink)
        videoButton.lastItem?.tag = 1
        videoButton.lastItem?.state = v?.mode == .link ? .on : .off
        if v != nil {
            videoButton.menu?.addItem(.separator())
            videoButton.addItem(withTitle: L10n.T("Remove"))
            videoButton.lastItem?.tag = 2
        }
        videoButton.toolTip = v?.mode == .link ? v?.url : nil
    }

    /// A small square of the calendar's colour for the Calendar popup.
    private static func swatch(_ color: NSColor) -> NSImage {
        let img = NSImage(size: NSSize(width: 10, height: 10), flipped: false) { r in
            color.setFill()
            NSBezierPath(roundedRect: r, xRadius: 2.5, yRadius: 2.5).fill()
            return true
        }
        img.isTemplate = false
        return img
    }

    // MARK: Editing

    func controlTextDidChange(_ obj: Foundation.Notification) {
        switch obj.object as AnyObject? {
        case let f where f === titleField: controller.setTitle(titleField.stringValue)
        case let f where f === locationField: controller.setLocation(locationField.stringValue)
        default: break
        }
    }

    func textDidChange(_ notification: Foundation.Notification) {
        controller.setNotes(notesView.string)
    }

    /// The field's badges and typed text into the controller's model, as
    /// they resolve (what a commit would make of the typed text).
    private func attendeesEdited() {
        let r = attendeeField.resolved()
        controller.editAttendees { m in
            var t = RecipientTokens()
            for a in r.addresses {
                t.add(a)
            }
            for raw in r.invalid {
                t.setPending(raw)
                t.commit()
            }
            m = t
        }
    }

    @objc private func calendarChosen(_ sender: Any?) {
        let i = calendarPopup.indexOfSelectedItem
        guard calendarIDs.indices.contains(i) else { return }
        controller.setCalendar(calendarIDs[i])
    }

    @objc private func allDayToggled(_ sender: Any?) {
        controller.setAllDay(allDaySwitch.state == .on)
    }

    /// The day of a date picker with the time of a time picker, in the
    /// editor's zone.
    private func combine(_ day: NSDatePicker, _ time: NSDatePicker) -> Date {
        let z = controller.input.zone
        let d = Cal.parts(day.dateValue, z)
        let t = Cal.parts(time.dateValue, z)
        return Cal.date(d.year, d.month, d.day, t.hour, t.minute, in: z)
    }

    @objc private func dateChanged(_ sender: Any?) {
        let input = controller.input
        let z = input.zone
        if sender as AnyObject? === startDate || sender as AnyObject? === startTime {
            if sender as AnyObject? === startDate {
                let p = Cal.parts(input.start, z)
                let d = Cal.parts(startDate.dateValue, z)
                controller.setStart(Cal.date(d.year, d.month, d.day, input.allDay ? 0 : p.hour, input.allDay ? 0 : p.minute, in: z))
            } else {
                controller.setStart(combine(startDate, startTime))
            }
        } else if input.allDay {
            controller.setEnd(Cal.addDays(Cal.startOfDay(endDate.dateValue, z), 1, z))
        } else if sender as AnyObject? === endDate {
            let p = Cal.parts(input.end, z)
            let d = Cal.parts(endDate.dateValue, z)
            controller.setEnd(Cal.date(d.year, d.month, d.day, p.hour, p.minute, in: z))
        } else {
            controller.setEnd(combine(endDate, endTime))
        }
        render()
    }

    @objc private func showZones(_ sender: Any?) {
        if let p = zonePopover, p.isShown {
            p.performClose(sender)
            return
        }
        let vc = EventEditorTimeZoneViewController(current: controller.input.zone.identifier)
        let popover = NSPopover()
        popover.behavior = .transient
        popover.contentViewController = vc
        popover.delegate = self
        vc.onPick = { [weak self, weak popover] z in
            self?.controller.setZone(z)
            popover?.performClose(nil)
        }
        zonePopover = popover
        popover.show(relativeTo: zoneButton.bounds, of: zoneButton, preferredEdge: .maxY)
    }

    @objc private func repeatChosen(_ sender: Any?) {
        let i = repeatPopup.indexOfSelectedItem
        guard controller.repeatChoices.indices.contains(i) else { return }
        let c = controller.repeatChoices[i]
        guard c == .custom else {
            controller.setRepeat(c)
            return
        }
        // The popup shows the rule in force until Done applies a new one.
        render()
        let input = controller.input
        let vc = EventEditorRepeatViewController(
            current: controller.recurrence, start: input.start, zone: input.zone, env: controller.env)
        let popover = NSPopover()
        popover.behavior = .transient
        popover.contentViewController = vc
        popover.delegate = self
        vc.onApply = { [weak self, weak popover] r in
            self?.controller.setRecurrence(r)
            popover?.performClose(nil)
        }
        repeatPopover = popover
        popover.show(relativeTo: repeatPopup.bounds, of: repeatPopup, preferredEdge: .maxY)
    }

    @objc private func alertChosen(_ sender: Any?) {
        let i = alertPopup.indexOfSelectedItem
        guard alertValues.indices.contains(i) else { return }
        controller.setAlert(alertValues[i])
    }

    @objc private func videoChosen(_ sender: Any?) {
        guard let item = videoButton.selectedItem else { return }
        defer { videoMenuKey = "" ; renderVideo() }
        if let p = item.representedObject as? String {
            controller.setVideoCall(VideoCallInput(mode: .create, provider: p))
        } else if item.tag == 1 {
            askLink()
        } else if item.tag == 2 {
            controller.setVideoCall(nil)
        }
    }

    /// Paste Link…: an https link (the pasteboard's, when it holds one).
    private func askLink() {
        guard let window = view.window else { return }
        let field = NSTextField(frame: NSRect(x: 0, y: 0, width: 300, height: 24))
        // The scheme the link must have; not a sentence.
        let scheme = "https://"
        field.placeholderString = scheme
        if let s = NSPasteboard.general.string(forType: .string)?.trimmingCharacters(in: .whitespacesAndNewlines),
            s.lowercased().hasPrefix("https://"), !s.contains("\n")
        {
            field.stringValue = s
        } else if controller.videoCall?.mode == .link {
            field.stringValue = controller.videoCall?.url ?? ""
        }
        let alert = NSAlert()
        alert.messageText = Cal.addVideoCall
        alert.accessoryView = field
        let ok = alert.addButton(withTitle: L10n.T("Done"))
        ok.keyEquivalent = "\r"
        let cancel = alert.addButton(withTitle: Cal.cancel)
        cancel.keyEquivalent = "\u{1b}"
        alert.window.initialFirstResponder = field
        alert.beginSheetModal(for: window) { [weak self] response in
            MainActor.assumeIsolated {
                guard response == .alertFirstButtonReturn, let self else { return }
                if !self.controller.setVideoCall(VideoCallInput(mode: .link, url: field.stringValue)) {
                    NSSound.beep()
                }
                self.videoMenuKey = ""
                self.renderVideo()
            }
        }
    }

    @objc private func nextSlotClicked(_ sender: Any?) {
        controller.nextFreeSlot()
    }

    @objc private func deleteClicked(_ sender: Any?) {
        Task { [weak self] in
            await self?.controller.delete()
        }
    }

    /// The footer's buttons, left to right (the `event-editor` DevStart step).
    var footerButtons: String {
        [deleteButton, cancelButton, primaryButton].filter { !$0.isHidden }.map {
            let x = $0.convert($0.bounds, to: nil).minX
            return String(format: "'%@' x %.0f", $0.title, x)
        }.joined(separator: ", ")
    }

    @objc private func cancelClicked(_ sender: Any?) {
        onCancel?()
    }

    @objc private func primaryClicked(_ sender: Any?) {
        save()
    }

    @objc private func reloadClicked(_ sender: Any?) {
        onReload?()
    }

    /// The primary button as the window's default (Return, the accent look).
    var defaultButtonCell: NSButtonCell? { primaryButton.cell as? NSButtonCell }

    /// The primary button, ⌘↩ and ⌘S (the window checks ⌘S).
    func save() {
        guard !controller.saving else { return }
        // A typed, unfinished attendee counts as the field would commit it.
        attendeesEdited()
        Task { [weak self] in
            await self?.controller.save()
        }
    }

    /// Attach: files picked in the open panel, imported under the
    /// calendar's account.
    func attachFiles() {
        guard let window = view.window else { return }
        let panel = NSOpenPanel()
        panel.title = Cal.attach
        panel.canChooseFiles = true
        panel.canChooseDirectories = false
        panel.allowsMultipleSelection = true
        Task { [weak self] in
            let response = await panel.beginSheetModal(for: window)
            guard let self, response == .OK else { return }
            for url in panel.urls where url.isFileURL {
                self.controller.attach(path: url.path)
            }
        }
    }

    func popoverDidClose(_ notification: Foundation.Notification) {
        guard let p = notification.object as? NSPopover else { return }
        if p === zonePopover {
            zonePopover = nil
        } else if p === repeatPopover {
            repeatPopover = nil
            render()
        }
    }

    /// The window really closes.
    func cleanup() {
        zonePopover?.close()
        repeatPopover?.close()
        suggestions?.cleanup()
        controller.onChange = nil
    }
}

/// The rounded frame of a text field around a view that has none (the
/// attendee row), in the system's colours.
@MainActor
final class EventEditorFieldFrame: NSView {
    init(content: NSView) {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        content.translatesAutoresizingMaskIntoConstraints = false
        addSubview(content)
        NSLayoutConstraint.activate([
            content.topAnchor.constraint(equalTo: topAnchor, constant: 1),
            bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: 1),
            content.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 6),
            trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: 6),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func draw(_ dirtyRect: NSRect) {
        let path = NSBezierPath(roundedRect: bounds.insetBy(dx: 0.5, dy: 0.5), xRadius: 5, yRadius: 5)
        NSColor.textBackgroundColor.setFill()
        path.fill()
        NSColor.separatorColor.setStroke()
        path.lineWidth = 1
        path.stroke()
    }
}

/// A document view whose origin is the top left (the form grows down).
private final class FlippedView: NSView {
    override var isFlipped: Bool { true }
}
