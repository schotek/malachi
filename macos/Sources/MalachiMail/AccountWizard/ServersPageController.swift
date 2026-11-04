// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The wizard's Servers page (account_wizard.blp `servers_page`): the
/// account name, the IMAP and SMTP endpoints, each with a "Pinned
/// Certificate" row and Forget while a certificate is pinned to it, and the
/// Test Connection button. The port follows the security choice through
/// `portForSecurityChange` unless the rows are being filled by the
/// controller.
@MainActor
final class ServersPageController: NSViewController, NSTextFieldDelegate {
    /// One endpoint's editors (wizard.go `serverRows`).
    @MainActor
    private final class EndpointRows {
        let kind: Endpoint
        let host = WizardEntryField()
        let port: PrefsSpinControl
        let security = NSPopUpButton(frame: .zero, pullsDown: false)
        let user = WizardEntryField()
        /// The pinned certificate's fingerprint with Forget; hidden
        /// without a pin.
        let pinRow: PreferenceRowView
        let forget = NSButton(title: "", target: nil, action: nil)
        var lastSecurity: Security

        init(kind: Endpoint, port defaultPort: Int, security: Security) {
            self.kind = kind
            port = PrefsSpinControl(min: 1, max: 65535, value: defaultPort, digits: 5)
            lastSecurity = security
            self.security.addItems(withTitles: [L10n.T("TLS"), L10n.T("STARTTLS"), L10n.T("None")])
            self.security.selectItem(at: indexOfSecurity(security))
            // TRANSLATORS: button: stop trusting the pinned certificate
            forget.title = wizardLabel("_Forget")
            forget.bezelStyle = .rounded
            // TRANSLATORS: row title on the Servers page
            pinRow = PreferenceRowView(title: L10n.T("Pinned Certificate"), subtitle: " ", trailing: forget)
            pinRow.setSubtitleSelectable()
        }

        func read() -> ServerFields {
            ServerFields(host: host.field.stringValue, port: port.value, security: securityAt(security.indexOfSelectedItem), username: user.field.stringValue)
        }

        func apply(_ sc: ServerConfig?) {
            guard let sc else { return }
            host.field.stringValue = sc.host
            port.value = sc.port
            security.selectItem(at: indexOfSecurity(sc.security))
            lastSecurity = sc.security
            user.field.stringValue = sc.username
        }

        var isEnabled: Bool {
            get { host.isEnabled }
            set {
                host.isEnabled = newValue
                port.isEnabled = newValue
                security.isEnabled = newValue
                user.isEnabled = newValue
                forget.isEnabled = newValue
            }
        }
    }

    private let wizard: WizardController
    private let nameEntry = WizardEntryField()
    private let imap = EndpointRows(kind: .imap, port: 993, security: .tls)
    private let smtp = EndpointRows(kind: .smtp, port: 587, security: .starttls)
    private let imapGroup = PreferencesGroupView(title: L10n.T("Incoming Mail (IMAP)"))
    private let smtpGroup = PreferencesGroupView(title: L10n.T("Outgoing Mail (SMTP)"))
    /// The fingerprints pinned now ("" for none), shown once the view loads.
    private var pins: (imap: String, smtp: String) = ("", "")
    private let testButton = NSButton(title: "", target: nil, action: nil)
    let cancelButton = WizardCancelButton()
    private let pageView = WizardPageView()
    /// The rows are being set programmatically: no port logic.
    private var applying = false

    init(wizard: WizardController) {
        self.wizard = wizard
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func loadView() {
        let accountGroup = PreferencesGroupView(title: L10n.T("Account"))
        accountGroup.setRows([PreferenceRowView(title: L10n.T("Account Name"), trailing: nameEntry, trailingFills: true)])
        imapGroup.setRows(rows(for: imap))
        smtpGroup.setRows(rows(for: smtp))
        imap.forget.target = self
        imap.forget.action = #selector(forgetClicked(_:))
        smtp.forget.target = self
        smtp.forget.action = #selector(forgetClicked(_:))
        showPins(imap: pins.imap, smtp: pins.smtp)

        nameEntry.field.delegate = self
        for rows in [imap, smtp] {
            rows.host.field.delegate = self
            rows.user.field.delegate = self
            rows.security.target = self
            rows.security.action = #selector(securityChanged(_:))
            rows.port.onChange = { [weak self] _ in self?.sync() }
        }

        let column = prefsColumn(spacing: 24, insets: NSEdgeInsets(top: 24, left: 12, bottom: 24, right: 12))
        for group in [accountGroup, imapGroup, smtpGroup] {
            prefsAddFilling(group, to: column)
        }
        let scroll = prefsScrollView(clampMaximum: 580, content: column)
        scroll.setContentHuggingPriority(.defaultLow, for: .vertical)

        testButton.title = wizardLabel("_Test Connection")
        testButton.bezelStyle = .rounded
        testButton.controlSize = .large
        testButton.keyEquivalent = "\r"
        testButton.target = self
        testButton.action = #selector(testClicked(_:))

        let stack = prefsColumn(spacing: 0)
        prefsAddFilling(scroll, to: stack)
        prefsAddFilling(wizardButtonBar([testButton], cancel: cancelButton), to: stack)
        pageView.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: pageView.topAnchor),
            stack.bottomAnchor.constraint(equalTo: pageView.bottomAnchor),
            stack.leadingAnchor.constraint(equalTo: pageView.leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: pageView.trailingAnchor),
        ])
        // Return in a row does nothing (the GTK rows have no activation);
        // the default button only answers when no row edits.
        pageView.returnHandler = { [weak self] _ in
            self?.sync()
            return true
        }
        view = pageView
    }

    private func rows(for rows: EndpointRows) -> [NSView] {
        [
            PreferenceRowView(title: L10n.T("Server"), trailing: rows.host, trailingFills: true),
            PreferenceRowView(title: L10n.T("Port"), trailing: rows.port),
            PreferenceRowView(title: L10n.T("Security"), trailing: rows.security),
            PreferenceRowView(title: L10n.T("User Name"), trailing: rows.user, trailingFills: true),
            rows.pinRow,
        ]
    }

    // MARK: Inputs from the controller

    /// Fills the rows without triggering the port logic (wizard.go `applyConfig`).
    func apply(_ cfg: AccountConfig) {
        applying = true
        nameEntry.field.stringValue = cfg.name
        imap.apply(cfg.imap)
        smtp.apply(cfg.smtp)
        applying = false
    }

    /// The certificates pinned to the endpoints (`WizardController.onPins`):
    /// a "Pinned Certificate" row with the fingerprint per pin.
    func showPins(imap imapPin: String, smtp smtpPin: String) {
        pins = (imapPin, smtpPin)
        // Before loadView the groups have no rows yet and this is a no-op;
        // loadView shows the pins again.
        for (rows, group, pin) in [(imap, imapGroup, imapPin), (smtp, smtpGroup, smtpPin)] {
            rows.pinRow.subtitle = pin.isEmpty ? " " : pin
            group.setRow(rows.pinRow, hidden: pin.isEmpty)
        }
    }

    func showProblems(_ p: ServerProblems) {
        imap.host.hasError = p.imapHost
        imap.user.hasError = p.imapUser
        smtp.host.hasError = p.smtpHost
        smtp.user.hasError = p.smtpUser
    }

    func setBusy(_ busy: Bool) {
        nameEntry.isEnabled = !busy
        imap.isEnabled = !busy
        smtp.isEnabled = !busy
        testButton.isEnabled = !busy
    }

    // MARK: Outputs to the controller

    private func sync() {
        wizard.setServers(name: nameEntry.field.stringValue, imap: imap.read(), smtp: smtp.read())
    }

    @objc private func testClicked(_ sender: Any?) {
        sync()
        wizard.testServers()
    }

    @objc private func forgetClicked(_ sender: NSButton) {
        wizard.forgetPin(sender === imap.forget ? .imap : .smtp)
    }

    @objc private func securityChanged(_ sender: NSPopUpButton) {
        guard !applying else { return }
        let rows = sender === imap.security ? imap : smtp
        let to = securityAt(sender.indexOfSelectedItem)
        rows.port.value = portForSecurityChange(rows.kind, port: rows.port.value, from: rows.lastSecurity, to: to)
        rows.lastSecurity = to
        sync()
    }

    // MARK: NSTextFieldDelegate

    func controlTextDidChange(_ obj: Foundation.Notification) {
        guard let field = obj.object as? NSTextField else { return }
        // Typing in a flagged row clears its flag, as the GTK rows do.
        for entry in [imap.host, imap.user, smtp.host, smtp.user] where entry.field === field {
            entry.hasError = false
        }
        sync()
    }

    func control(_ control: NSControl, textView: NSTextView, doCommandBy commandSelector: Selector) -> Bool {
        if commandSelector == #selector(NSResponder.insertNewline(_:)) {
            sync()
            return true
        }
        return false
    }
}
