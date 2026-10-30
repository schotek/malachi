// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The Add CalDAV Calendar sheet (docs/calendar.md §5.8, artboard 7;
/// 560×560) over `CalDAVWizardController`: the header with the calendar
/// symbol, title and subtitle; Account Name, Server Address, Username and
/// Password (the password goes to the daemon's keyring with account.add and
/// is never read back); Discover Calendars (account.discoverCalDAV, then
/// account.listCalDAVCalendars); the found calendars with checkboxes,
/// colour, name and "default" / "read only" under the status line ("Found
/// through /.well-known/caldav · 4 calendars"); the Certificate row
/// ("Verified (R11)", "Trusted by you"; on a `tlsError` Trust Certificate…,
/// which confirms with the fingerprint through the account assistant's
/// prompt and runs discovery again with the pin); the mail account for
/// invitations when the server leaves them to the client; the footer note;
/// Cancel / Add (account.add). Everything from the server is plain text.
@MainActor
final class CalDAVSheetController: NSWindowController, NSTextFieldDelegate {
    /// The open sheets, kept alive while they are up.
    private static var active: [ObjectIdentifier: CalDAVSheetController] = [:]

    let wizard: CalDAVWizardController
    private var onDone: (@MainActor (AccountID) -> Void)?

    private let nameEntry = WizardEntryField()
    private let serverEntry = WizardEntryField()
    private let usernameEntry = WizardEntryField()
    private let passwordEntry = WizardEntryField(secure: true)
    private let discoverButton = NSButton(title: Cal.CalDAVWizard.discoverButton, target: nil, action: nil)
    private let progress = JiraWizardProgress()
    /// The error under Discover Calendars (`caldav-sheet` prints its frames).
    let banner = CalDAVBannerView()
    private let serverChanged = PrefsWrappingLabel("", size: 12, color: .secondaryLabelColor)
    private let calendarsGroup = PreferencesGroupView(title: Cal.CalDAVWizard.calendarsHeading, description: "")
    /// The server's address books (docs/plans/address-book.md W3 ⑤): Sync
    /// Contacts and the books found; which of them show is chosen in
    /// Settings → Contacts once the account is there.
    private let addressBooksGroup = PreferencesGroupView(title: ContactBook.addressBooksHeading)
    private let contactsSwitch = NSSwitch()
    private var shownAddressBooks: [String] = []
    private let certificateGroup = PreferencesGroupView()
    private let certificateLabel = NSTextField(labelWithString: "")
    private let trustButton = NSButton(title: Cal.CalDAVWizard.trustButton, target: nil, action: nil)
    private let mailGroup = PreferencesGroupView()
    private let mailPopup = NSPopUpButton(frame: .zero, pullsDown: false)
    private let addButton = NSButton(title: Cal.CalDAVWizard.addButton, target: nil, action: nil)
    private let cancelButton = WizardCancelButton()
    private let pageView = WizardPageView()
    private var shownCalendars: [CalDAVWizardController.ListedCalendar] = []
    private var shownMailAccounts: [CalDAVWizardController.MailAccount] = []

    /// Opens the sheet on `parent` over `source` (a window's calendar
    /// source: its snapshot names the mail accounts). `confirmTrust` is the
    /// account assistant's "Trust This Certificate?"; `onDone` runs after
    /// account.add succeeded, before the sheet closes.
    static func present(
        from parent: NSWindow, source: any CalendarDataSource, confirmTrust: @escaping WizardConfirmTrust,
        onDone: @escaping @MainActor (AccountID) -> Void
    ) {
        let c = CalDAVSheetController(source: source, confirmTrust: confirmTrust)
        c.onDone = onDone
        guard let sheet = c.window else { return }
        active[ObjectIdentifier(c)] = c
        parent.beginSheet(sheet) { _ in
            MainActor.assumeIsolated {
                c.wizard.cancel()
                active[ObjectIdentifier(c)] = nil
            }
        }
        c.wizard.start()
        sheet.makeFirstResponder(c.serverEntry.field)
    }

    private init(source: any CalendarDataSource, confirmTrust: @escaping WizardConfirmTrust) {
        wizard = CalDAVWizardController(source: source)
        let window = WizardSheetWindow(
            contentRect: NSRect(x: 0, y: 0, width: 560, height: 560),
            styleMask: [.titled, .fullSizeContentView],
            backing: .buffered, defer: false
        )
        window.title = ContactBook.wizardTitle
        window.titleVisibility = .hidden
        window.titlebarAppearsTransparent = true
        window.isReleasedWhenClosed = false
        window.autorecalculatesKeyViewLoop = true
        super.init(window: window)
        window.contentView = buildContent()
        window.onCancel = { [weak self] in self?.dismiss() }
        cancelButton.onCancel = { [weak self] in self?.dismiss() }
        wizard.onChange = { [weak self] in self?.render() }
        wizard.onConfirmTrust = { [weak window] prompt in
            await confirmTrust(window, prompt)
        }
        wizard.onDone = { [weak self] id in
            guard let self else { return }
            self.onDone?(id)
            self.dismiss()
        }
        render()
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Closes the sheet; late replies are dropped from now on.
    func dismiss() {
        wizard.cancel()
        guard let window else { return }
        if let parent = window.sheetParent {
            parent.endSheet(window)
        } else {
            window.close()
        }
    }

    // MARK: Building

    private func buildContent() -> NSView {
        let icon = NSImageView()
        icon.image = wizardSymbol("calendar", pointSize: 34)
        icon.contentTintColor = .controlAccentColor
        icon.imageScaling = .scaleNone
        icon.setContentHuggingPriority(.required, for: .horizontal)
        let title = NSTextField(labelWithString: ContactBook.wizardTitle)
        title.font = .systemFont(ofSize: 17, weight: .bold)
        let subtitle = PrefsWrappingLabel(Cal.CalDAVWizard.subtitle, size: 12, color: .secondaryLabelColor)
        let titles = prefsColumn(spacing: 2)
        prefsAddFilling(title, to: titles)
        prefsAddFilling(subtitle, to: titles)
        let header = NSStackView(views: [icon, titles])
        header.orientation = .horizontal
        header.alignment = .centerY
        header.spacing = 14
        titles.setContentHuggingPriority(.defaultLow, for: .horizontal)
        subtitle.setContentHuggingPriority(.defaultLow, for: .horizontal)
        subtitle.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)

        serverEntry.field.placeholderString = Cal.CalDAVWizard.serverPlaceholder
        // One label column (right-aligned, 130 pt) and the fields at one x;
        // the password note is a caption under its field.
        func label(_ f: Cal.CalDAVWizard.Field) -> NSTextField {
            let l = NSTextField(labelWithString: Cal.CalDAVWizard.fieldLabel(f))
            l.alignment = .right
            l.lineBreakMode = .byTruncatingTail
            return l
        }
        let note = PrefsWrappingLabel(Cal.CalDAVWizard.passwordNote, size: 11, color: .secondaryLabelColor)
        let fields = NSGridView(views: [
            [label(.name), nameEntry],
            [label(.server), serverEntry],
            [label(.username), usernameEntry],
            [label(.password), passwordEntry],
            [NSGridCell.emptyContentView, note],
        ])
        fields.translatesAutoresizingMaskIntoConstraints = false
        fields.rowSpacing = 10
        fields.columnSpacing = 12
        fields.rowAlignment = .firstBaseline
        fields.column(at: 0).xPlacement = .trailing
        fields.column(at: 0).width = 130
        fields.column(at: 1).xPlacement = .fill
        fields.row(at: 4).topPadding = -4
        for entry in [nameEntry, serverEntry, usernameEntry, passwordEntry] {
            entry.field.delegate = self
        }

        discoverButton.bezelStyle = .rounded
        discoverButton.target = self
        discoverButton.action = #selector(discoverClicked(_:))
        let discoverRow = NSStackView(views: [discoverButton, progress])
        discoverRow.orientation = .horizontal
        discoverRow.alignment = .centerY
        discoverRow.spacing = 10

        certificateLabel.textColor = .secondaryLabelColor
        certificateLabel.lineBreakMode = .byTruncatingTail
        trustButton.bezelStyle = .rounded
        trustButton.target = self
        trustButton.action = #selector(trustClicked(_:))
        let certificate = NSStackView(views: [certificateLabel, trustButton])
        certificate.orientation = .horizontal
        certificate.alignment = .centerY
        certificate.spacing = 8
        certificateGroup.setRows([PreferenceRowView(title: Cal.CalDAVWizard.certificateLabel, trailing: certificate)])

        mailPopup.target = self
        mailPopup.action = #selector(mailChanged(_:))
        mailGroup.setRows([PreferenceRowView(title: Cal.Prefs.mailAccountSetting, trailing: mailPopup)])

        let footer = PrefsWrappingLabel(Cal.CalDAVWizard.footer, size: 11, color: .secondaryLabelColor)

        let column = prefsColumn(spacing: 14, insets: NSEdgeInsets(top: 28, left: 24, bottom: 16, right: 24))
        contactsSwitch.target = self
        contactsSwitch.action = #selector(contactsToggled(_:))
        contactsSwitch.setAccessibilityLabel(ContactBook.syncContacts)
        addressBooksGroup.isHidden = true
        for v in [header, fields, discoverRow, banner, serverChanged, certificateGroup, calendarsGroup, addressBooksGroup, mailGroup, footer] as [NSView] {
            prefsAddFilling(v, to: column)
        }
        column.setCustomSpacing(20, after: header)
        // The subtitle uses the width up to the sheet's trailing inset.
        titles.trailingAnchor.constraint(equalTo: header.trailingAnchor).isActive = true
        let scroll = prefsScrollView(clampMaximum: 512, content: column)

        addButton.bezelStyle = .rounded
        addButton.controlSize = .large
        addButton.keyEquivalent = "\r"
        addButton.target = self
        addButton.action = #selector(addClicked(_:))
        let bar = wizardButtonBar([addButton], cancel: cancelButton)
        bar.edgeInsets = NSEdgeInsets(top: 12, left: 20, bottom: 16, right: 20)

        let root = pageView
        for v in [scroll, bar] as [NSView] {
            v.translatesAutoresizingMaskIntoConstraints = false
            root.addSubview(v)
        }
        NSLayoutConstraint.activate([
            scroll.topAnchor.constraint(equalTo: root.topAnchor),
            scroll.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            scroll.trailingAnchor.constraint(equalTo: root.trailingAnchor),
            scroll.bottomAnchor.constraint(equalTo: bar.topAnchor),
            bar.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            bar.trailingAnchor.constraint(equalTo: root.trailingAnchor),
            bar.bottomAnchor.constraint(equalTo: root.bottomAnchor),
        ])
        root.returnHandler = { [weak self] _ in
            self?.handleReturn() ?? false
        }
        return root
    }

    // MARK: Rendering

    private func render() {
        let w = wizard.wizard
        let busy = !w.progress.isEmpty
        for entry in [nameEntry, serverEntry, usernameEntry, passwordEntry] {
            entry.isEnabled = w.fieldsEnabled
        }
        // The server's display name fills an empty Account Name.
        if nameEntry.field.currentEditor() == nil, nameEntry.field.stringValue != w.name {
            nameEntry.field.stringValue = w.name
        }
        if w.phase == .added {
            passwordEntry.field.stringValue = ""
        }
        discoverButton.isEnabled = w.canDiscover
        progress.show(busy ? w.progress : nil)
        let error = wizard.errorText
        banner.reveal(error.isEmpty ? nil : error)
        serverEntry.hasError = w.error?.code == .invalidArgument
        passwordEntry.hasError = w.error?.code == .authFailed || w.error?.code == .authRequired
        serverChanged.stringValue = w.serverChanged
        serverChanged.isHidden = w.serverChanged.isEmpty

        let cert = w.certificateText
        let offer = w.trustOffer != nil
        certificateLabel.stringValue = cert
        certificateLabel.isHidden = cert.isEmpty
        trustButton.isHidden = !offer
        certificateGroup.isHidden = cert.isEmpty && !offer

        let listed = wizard.listedCalendars
        calendarsGroup.isHidden = w.listed == nil
        calendarsGroup.descriptionText = w.foundLine
        if listed.map(\.href) != shownCalendars.map(\.href) || listed.map(\.name) != shownCalendars.map(\.name) {
            calendarsGroup.setRows(listed.enumerated().map { i, c in
                prefsCalendarRow(
                    name: c.name, color: c.color, note: c.note, checked: c.selected, canToggle: true,
                    target: self, action: #selector(calendarToggled(_:)), tag: i)
            })
        }
        shownCalendars = listed
        setChecks(in: calendarsGroup, listed.map(\.selected), enabled: w.phase == .listed)

        let books = wizard.listedAddressBooks
        addressBooksGroup.isHidden = books.isEmpty
        if books != shownAddressBooks {
            shownAddressBooks = books
            var rows: [NSView] = [PreferenceRowView(title: ContactBook.syncContacts, trailing: contactsSwitch)]
            for name in books {
                let icon = NSImageView(image: NSImage(systemSymbolName: "book.closed", accessibilityDescription: nil) ?? NSImage())
                icon.contentTintColor = .secondaryLabelColor
                rows.append(PreferenceRowView(title: name, prefix: icon))
            }
            addressBooksGroup.setRows(rows)
        }
        contactsSwitch.state = w.contacts ? .on : .off
        contactsSwitch.isEnabled = w.phase == .listed

        mailGroup.isHidden = !w.showsMailAccount
        let accounts = wizard.mailAccounts
        if accounts != shownMailAccounts || mailPopup.numberOfItems == 0 {
            shownMailAccounts = accounts
            mailPopup.removeAllItems()
            mailPopup.addItems(withTitles: [Cal.Prefs.noMailAccount] + accounts.map(\.name))
        }
        let i = w.mailAccount.flatMap { id in accounts.firstIndex { $0.id == id } }
        mailPopup.selectItem(at: i.map { $0 + 1 } ?? 0)
        mailPopup.isEnabled = w.fieldsEnabled

        addButton.isEnabled = w.canAdd
    }

    /// The listed checkboxes' states, by their tags.
    private func setChecks(in view: NSView, _ states: [Bool], enabled: Bool) {
        for sub in view.subviews {
            if let b = sub as? NSButton, b.action == #selector(calendarToggled(_:)), states.indices.contains(b.tag) {
                b.state = states[b.tag] ? .on : .off
                b.isEnabled = enabled
            } else {
                setChecks(in: sub, states, enabled: enabled)
            }
        }
    }

    // MARK: Actions

    @objc private func contactsToggled(_ sender: NSSwitch) {
        wizard.setContacts(sender.state == .on)
    }

    private func sync() {
        wizard.edit(
            name: nameEntry.field.stringValue, server: serverEntry.field.stringValue,
            username: usernameEntry.field.stringValue, password: passwordEntry.field.stringValue)
    }

    @objc private func discoverClicked(_ sender: Any?) {
        sync()
        wizard.discover()
    }

    @objc private func trustClicked(_ sender: Any?) {
        wizard.trust()
    }

    @objc private func addClicked(_ sender: Any?) {
        sync()
        wizard.add()
    }

    @objc private func calendarToggled(_ sender: NSButton) {
        guard shownCalendars.indices.contains(sender.tag) else { return }
        wizard.toggle(shownCalendars[sender.tag].href, sender.state == .on)
    }

    @objc private func mailChanged(_ sender: Any?) {
        let i = mailPopup.indexOfSelectedItem - 1
        wizard.setMailAccount(shownMailAccounts.indices.contains(i) ? shownMailAccounts[i].id : nil)
    }

    /// Return in a field: Discover Calendars until the calendars are
    /// listed, then Add.
    private func handleReturn() -> Bool {
        sync()
        let w = wizard.wizard
        if w.phase == .listed && w.canAdd {
            wizard.add()
        } else if w.canDiscover {
            wizard.discover()
        }
        return true
    }

    // MARK: NSTextFieldDelegate

    func controlTextDidChange(_ obj: Foundation.Notification) {
        sync()
    }
}
