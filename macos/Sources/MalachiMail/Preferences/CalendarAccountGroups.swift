// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// `#rrggbb` as a colour; anything else the secondary label's.
func prefsCalendarColor(_ hex: String) -> NSColor {
    var s = hex.trimmingCharacters(in: .whitespaces)
    if s.hasPrefix("#") {
        s.removeFirst()
    }
    guard s.count == 6, let v = UInt32(s, radix: 16) else { return .secondaryLabelColor }
    return NSColor(
        srgbRed: CGFloat((v >> 16) & 0xff) / 255, green: CGFloat((v >> 8) & 0xff) / 255,
        blue: CGFloat(v & 0xff) / 255, alpha: 1)
}

/// The colour square of a calendar row (12 pt, rounded).
@MainActor
final class PrefsColorSwatch: NSView {
    var color: NSColor = .secondaryLabelColor {
        didSet { needsDisplay = true }
    }

    init(_ color: NSColor) {
        self.color = color
        super.init(frame: NSRect(x: 0, y: 0, width: 12, height: 12))
        translatesAutoresizingMaskIntoConstraints = false
        widthAnchor.constraint(equalToConstant: 12).isActive = true
        heightAnchor.constraint(equalToConstant: 12).isActive = true
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func draw(_ dirtyRect: NSRect) {
        color.setFill()
        NSBezierPath(roundedRect: bounds, xRadius: 3, yRadius: 3).fill()
    }
}

/// A calendar of a list: checkbox and colour square before the name, the
/// note ("default", "read only", "shared · %s") at the end. Everything is
/// the server's plain text.
@MainActor
func prefsCalendarRow(
    name: String, color: String, note: String, checked: Bool, canToggle: Bool, target: AnyObject, action: Selector, tag: Int
) -> PreferenceRowView {
    let check = NSButton(checkboxWithTitle: "", target: target, action: action)
    check.state = checked ? .on : .off
    check.isEnabled = canToggle
    check.tag = tag
    check.setAccessibilityLabel(name)
    let prefix = NSStackView(views: [check, PrefsColorSwatch(prefsCalendarColor(color))])
    prefix.orientation = .horizontal
    prefix.alignment = .centerY
    prefix.spacing = 8
    // A stack view has no intrinsic width: it shrinks to its checkbox and
    // square, so the name starts right after them.
    prefix.setHuggingPriority(.required, for: .horizontal)
    let shrink = prefix.widthAnchor.constraint(equalToConstant: 0)
    shrink.priority = NSLayoutConstraint.Priority(NSLayoutConstraint.Priority.defaultLow.rawValue + 1)
    shrink.isActive = true
    let noteLabel = NSTextField(labelWithString: note)
    noteLabel.textColor = .secondaryLabelColor
    noteLabel.font = .systemFont(ofSize: 12)
    noteLabel.lineBreakMode = .byTruncatingTail
    return PreferenceRowView(title: name, prefix: prefix, trailing: note.isEmpty ? nil : noteLabel)
}

/// One account's section of Settings → Calendar (docs/calendar.md §5.8)
/// over `CalendarAccountController`: a heading with the account's name and
/// kind capsule (M365, CALDAV); for a CalDAV account the Account group
/// (Server, Username, Replace Password…, the mail account for invitations,
/// the certificate with Forget for a pin, Remove Account…); the Calendar
/// group (the sync switch of a Graph account with the consent's Sign In…,
/// the calendars, Default Calendar for New Events, Keep Events); and, for
/// a Graph account, the Invitations group (the card, tentative, After
/// Answering; a CalDAV account receives no mail). The calendar rows are rebuilt only when what they list changes.
@MainActor
final class CalendarAccountGroups: NSObject {
    let controller: CalendarAccountController
    private let window: @MainActor () -> NSWindow?

    let accountGroup = PreferencesGroupView(title: L10n.T("Account"))
    let calendarGroup = PreferencesGroupView(title: Cal.Prefs.accountsGroup)
    let invitationsGroup = PreferencesGroupView(title: Cal.Prefs.invitationsGroup)
    private let header = NSStackView()
    private let nameLabel = NSTextField(labelWithString: "")
    private let kindBadge = PillLabel()

    var views: [NSView] { [header, accountGroup, calendarGroup, invitationsGroup] }

    // Calendar
    private let syncSwitch = NSSwitch()
    private let signInButton = NSButton(title: Cal.Text.signIn, target: nil, action: nil)
    private let defaultPopup = NSPopUpButton(frame: .zero, pullsDown: false)
    private let keepPopup = NSPopUpButton(frame: .zero, pullsDown: false)
    private var calendarShape: [String] = []
    private var shownRows: [CalendarAccountController.CalendarRow] = []
    private var shownDefaults: [CalendarAccountController.CalendarRow] = []
    // Invitations
    private let cardSwitch = NSSwitch()
    private let tentativeSwitch = NSSwitch()
    private let afterPopup = NSPopUpButton(frame: .zero, pullsDown: false)
    // Account (CalDAV)
    private let serverLabel = NSTextField(labelWithString: "")
    private let usernameLabel = NSTextField(labelWithString: "")
    private let passwordButton = NSButton(title: "", target: nil, action: nil)
    private let mailPopup = NSPopUpButton(frame: .zero, pullsDown: false)
    private let certificateLabel = NSTextField(labelWithString: "")
    private let forgetButton = NSButton(title: wizardLabel("_Forget"), target: nil, action: nil)
    private let removeButton = NSButton(title: "", target: nil, action: nil)
    private var shownMailAccounts: [CalDAVWizardController.MailAccount] = []

    /// Remove Account… of a CalDAV account (the pane confirms).
    var onRemove: (@MainActor (Account) -> Void)?

    /// The consent's re-sign-in (`account.oauthStart` with `calendar: true`).
    var onSignIn: (@MainActor (AccountID) -> Void)? {
        get { controller.onSignIn }
        set { controller.onSignIn = newValue }
    }

    init(source: any CalendarDataSource, account: Account, window: @escaping @MainActor () -> NSWindow?) {
        controller = CalendarAccountController(source: source, accountId: account.id)
        self.window = window
        super.init()
        build()
        controller.onChange = { [weak self] in self?.render() }
        controller.onQuestion = { [weak self] q in
            await self?.ask(q) ?? .cancel
        }
    }

    func start() {
        controller.start()
        render()
    }

    func stop() {
        controller.stop()
    }

    // MARK: Building

    private func build() {
        nameLabel.font = .systemFont(ofSize: 15, weight: .semibold)
        nameLabel.lineBreakMode = .byTruncatingTail
        nameLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        kindBadge.font = Typo.sidebarKindBadge
        kindBadge.textColor = .secondaryLabelColor
        header.setViews([nameLabel, kindBadge], in: .leading)
        header.orientation = .horizontal
        header.alignment = .centerY
        header.spacing = 8
        header.detachesHiddenViews = true

        for (control, sel) in [
            (syncSwitch, #selector(syncChanged(_:))), (cardSwitch, #selector(cardChanged(_:))),
            (tentativeSwitch, #selector(tentativeChanged(_:))),
        ] as [(NSSwitch, Selector)] {
            control.target = self
            control.action = sel
        }
        for (popup, sel) in [
            (defaultPopup, #selector(defaultChanged(_:))), (keepPopup, #selector(keepChanged(_:))),
            (afterPopup, #selector(afterChanged(_:))), (mailPopup, #selector(mailChanged(_:))),
        ] as [(NSPopUpButton, Selector)] {
            popup.target = self
            popup.action = sel
        }
        signInButton.bezelStyle = .rounded
        signInButton.target = self
        signInButton.action = #selector(signInClicked(_:))
        keepPopup.addItems(withTitles: controller.keepChoices.map(Cal.Prefs.keepTitle))
        afterPopup.addItems(withTitles: Cal.Prefs.afterAnswers.map { Cal.Prefs.afterAnswerTitle($0) })

        invitationsGroup.setRows([
            PreferenceRowView(title: Cal.Prefs.cardSetting, trailing: cardSwitch),
            PreferenceRowView(title: Cal.Prefs.tentativeSetting, subtitle: Cal.Prefs.tentativeSubtitle, trailing: tentativeSwitch),
            PreferenceRowView(title: Cal.Prefs.afterAnswerSetting, trailing: afterPopup),
        ])

        for label in [serverLabel, usernameLabel, certificateLabel] {
            label.textColor = .secondaryLabelColor
            label.lineBreakMode = .byTruncatingMiddle
            label.isSelectable = true
        }
        passwordButton.title = Cal.Prefs.replacePassword
        passwordButton.bezelStyle = .rounded
        passwordButton.target = self
        passwordButton.action = #selector(replacePasswordClicked(_:))
        forgetButton.bezelStyle = .rounded
        forgetButton.target = self
        forgetButton.action = #selector(forgetClicked(_:))
        removeButton.title = Cal.Prefs.removeAccount
        removeButton.bezelStyle = .rounded
        removeButton.contentTintColor = .systemRed
        removeButton.target = self
        removeButton.action = #selector(removeClicked(_:))
        let removeBar = NSStackView()
        removeBar.orientation = .horizontal
        removeBar.edgeInsets = NSEdgeInsets(top: 8, left: 12, bottom: 8, right: 12)
        removeBar.addView(removeButton, in: .trailing)
        let certificate = NSStackView(views: [certificateLabel, forgetButton])
        certificate.orientation = .horizontal
        certificate.alignment = .centerY
        certificate.spacing = 8
        accountGroup.setRows([
            PreferenceRowView(title: Cal.CalDAVWizard.fieldLabel(.server), trailing: serverLabel),
            PreferenceRowView(title: Cal.CalDAVWizard.fieldLabel(.username), trailing: usernameLabel),
            PreferenceRowView(title: Cal.CalDAVWizard.fieldLabel(.password), subtitle: Cal.CalDAVWizard.passwordNote, trailing: passwordButton),
            PreferenceRowView(title: Cal.Prefs.mailAccountSetting, trailing: mailPopup),
            PreferenceRowView(title: Cal.CalDAVWizard.certificateLabel, trailing: certificate),
            removeBar,
        ])
    }

    // MARK: Rendering

    func render() {
        let c = controller
        let loaded = c.account != nil
        accountGroup.isHidden = !(loaded && c.isCalDAV)
        calendarGroup.isHidden = !(loaded && c.hasCalendarCapability)
        // The invitation settings are a mail account's; a CalDAV account
        // receives no mail (preferences.go `PreferencesLayout`).
        invitationsGroup.isHidden = !(loaded && c.isGraph)
        guard loaded, let account = c.account else { return }
        nameLabel.stringValue = accountRowTitle(account)
        let badge = accountHeaderBadge(account)
        kindBadge.stringValue = badge
        kindBadge.isHidden = badge.isEmpty

        renderCalendar()

        cardSwitch.state = c.invitationCard ? .on : .off
        tentativeSwitch.state = c.showUnanswered ? .on : .off
        afterPopup.selectItem(at: Cal.Prefs.afterAnswers.firstIndex(of: c.afterAnswer) ?? 0)

        if c.isCalDAV {
            serverLabel.stringValue = c.serverHost
            usernameLabel.stringValue = c.username
            certificateLabel.stringValue = c.certificateText
            forgetButton.isHidden = !c.certificatePinned
            if shownMailAccounts != c.mailAccounts || mailPopup.numberOfItems == 0 {
                shownMailAccounts = c.mailAccounts
                mailPopup.removeAllItems()
                mailPopup.addItems(withTitles: [Cal.Prefs.noMailAccount] + shownMailAccounts.map(\.name))
            }
            let i = c.mailAccountId.flatMap { id in shownMailAccounts.firstIndex { $0.id == id } }
            mailPopup.selectItem(at: i.map { $0 + 1 } ?? 0)
            accountGroup.descriptionText = c.error
        }
    }

    private func renderCalendar() {
        let c = controller
        let rows = c.syncOn ? c.rows : []
        let defaults = c.syncOn ? c.defaultChoices : []
        let consent = c.isGraph && c.syncOn && c.consentRequired
        // What the group lists; the switches' and popups' values follow below.
        let shape = ["\(c.showsSyncSwitch)", "\(c.syncOn)", "\(consent)"]
            + rows.map { "\($0.remoteId ?? "")\u{1f}\($0.name)\u{1f}\($0.color)\u{1f}\($0.note)\u{1f}\($0.enabled)" }
            + defaults.map { "d\($0.remoteId ?? "")\u{1f}\($0.name)" }
        if shape != calendarShape {
            calendarShape = shape
            shownRows = rows
            shownDefaults = defaults
            var list: [NSView] = []
            if c.showsSyncSwitch {
                list.append(PreferenceRowView(title: Cal.Prefs.syncSetting, trailing: syncSwitch))
            }
            if consent {
                list.append(PreferenceRowView(title: Cal.Text.consentLine, subtitle: Cal.Prefs.syncConsentNote, trailing: signInButton))
            }
            for (i, r) in rows.enumerated() {
                list.append(prefsCalendarRow(
                    name: r.name, color: r.color, note: r.note, checked: r.enabled, canToggle: r.canToggle,
                    target: self, action: #selector(calendarToggled(_:)), tag: i))
            }
            if c.syncOn {
                defaultPopup.removeAllItems()
                defaultPopup.addItems(withTitles: defaults.map(\.name))
                if !defaults.isEmpty {
                    list.append(PreferenceRowView(title: Cal.Prefs.defaultCalendarSetting, trailing: defaultPopup))
                }
                list.append(PreferenceRowView(title: Cal.Prefs.keepSetting, trailing: keepPopup))
            }
            calendarGroup.setRows(list)
        }
        syncSwitch.state = c.syncOn ? .on : .off
        let d = shownDefaults.firstIndex { $0.isDefault } ?? -1
        defaultPopup.selectItem(at: d)
        keepPopup.selectItem(at: c.keepChoices.firstIndex(of: c.keepDays) ?? -1)
        // A refusal shows once: above the Account group of a CalDAV account.
        calendarGroup.descriptionText = c.isCalDAV ? "" : c.error
    }

    // MARK: Actions

    @objc private func removeClicked(_ sender: Any?) {
        if let a = controller.account {
            onRemove?(a)
        }
    }

    @objc private func syncChanged(_ sender: Any?) {
        controller.setSync(syncSwitch.state == .on)
    }

    @objc private func signInClicked(_ sender: Any?) {
        controller.signIn()
    }

    @objc private func calendarToggled(_ sender: NSButton) {
        guard shownRows.indices.contains(sender.tag), let id = shownRows[sender.tag].remoteId else { return }
        controller.setEnabled(sender.state == .on, remoteId: id)
    }

    @objc private func defaultChanged(_ sender: Any?) {
        let i = defaultPopup.indexOfSelectedItem
        guard shownDefaults.indices.contains(i) else { return }
        controller.setDefaultCalendar(shownDefaults[i].remoteId)
    }

    @objc private func keepChanged(_ sender: Any?) {
        let i = keepPopup.indexOfSelectedItem
        guard controller.keepChoices.indices.contains(i) else { return }
        controller.setKeepDays(controller.keepChoices[i])
    }

    @objc private func cardChanged(_ sender: Any?) {
        controller.setInvitationCard(cardSwitch.state == .on)
    }

    @objc private func tentativeChanged(_ sender: Any?) {
        controller.setShowUnanswered(tentativeSwitch.state == .on)
    }

    @objc private func afterChanged(_ sender: Any?) {
        let i = afterPopup.indexOfSelectedItem
        guard Cal.Prefs.afterAnswers.indices.contains(i) else { return }
        controller.setAfterAnswer(Cal.Prefs.afterAnswers[i])
    }

    @objc private func mailChanged(_ sender: Any?) {
        let i = mailPopup.indexOfSelectedItem - 1
        controller.setMailAccount(shownMailAccounts.indices.contains(i) ? shownMailAccounts[i].id : nil)
    }

    @objc private func forgetClicked(_ sender: Any?) {
        controller.forgetCertificate()
    }

    /// The new password in a sheet; it goes to the keyring with
    /// account.update and is not kept.
    @objc private func replacePasswordClicked(_ sender: Any?) {
        guard let window = window() else { return }
        let alert = NSAlert()
        alert.messageText = Cal.Prefs.replacePasswordTitle
        alert.informativeText = Cal.CalDAVWizard.passwordNote
        alert.addButton(withTitle: L10n.T("Replace"))
        alert.addButton(withTitle: wizardLabel("_Cancel"))
        let field = NSSecureTextField(frame: NSRect(x: 0, y: 0, width: 260, height: 22))
        field.placeholderString = Cal.CalDAVWizard.fieldLabel(.password)
        alert.accessoryView = field
        alert.window.initialFirstResponder = field
        alert.beginSheetModal(for: window) { [weak self] response in
            MainActor.assumeIsolated {
                guard response == .alertFirstButtonReturn else { return }
                self?.controller.replacePassword(field.stringValue)
                field.stringValue = ""
            }
        }
    }

    /// The consent explanation: Sign In… or Cancel.
    private func ask(_ q: Cal.Question) async -> Cal.Answer {
        guard let window = window() else { return .cancel }
        let alert = NSAlert()
        alert.messageText = q.heading
        alert.informativeText = q.body
        alert.addButton(withTitle: q.confirm)
        alert.addButton(withTitle: wizardLabel("_Cancel"))
        return await withCheckedContinuation { cont in
            alert.beginSheetModal(for: window) { response in
                cont.resume(returning: response == .alertFirstButtonReturn ? .confirm : .cancel)
            }
        }
    }
}
