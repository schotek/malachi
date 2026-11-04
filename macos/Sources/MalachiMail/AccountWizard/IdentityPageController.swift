// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The wizard's first page (account_wizard.blp `identity_page`): the
/// description, the rows Your Name / E-mail Address / Password and the Next
/// button. The "Signed In on This Computer" group of the GTK page is never
/// shown: GNOME Online Accounts does not exist on macOS (a deviation in the
/// table of macos/README.md).
@MainActor
final class IdentityPageController: NSViewController, NSTextFieldDelegate {
    private let wizard: WizardController
    private let banner = WizardBannerView()
    private let nameEntry = WizardEntryField()
    private let emailEntry = WizardEntryField()
    private let passwordEntry = WizardEntryField(secure: true)
    private let group = PreferencesGroupView()
    private var passwordRow: PreferenceRowView?
    private let nextButton = NSButton(title: "", target: nil, action: nil)
    let cancelButton = WizardCancelButton()
    private let pageView = WizardPageView()

    init(wizard: WizardController) {
        self.wizard = wizard
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// The row the dialog focuses first (`focus-widget: email_row`).
    var initialFirstResponder: NSView { emailEntry.field }

    override func loadView() {
        let description = PrefsWrappingLabel(
            L10n.T("Enter your e-mail address and, unless the account signs in through your browser, its password. Malachi Mail will look up the server settings for you."),
            color: .secondaryLabelColor
        )

        let nameRow = PreferenceRowView(title: L10n.T("Your Name"), trailing: nameEntry, trailingFills: true)
        let emailRow = PreferenceRowView(title: L10n.T("E-mail Address"), trailing: emailEntry, trailingFills: true)
        let passwordRow = PreferenceRowView(title: wizard.passwordTitle, trailing: passwordEntry, trailingFills: true)
        self.passwordRow = passwordRow
        group.setRows([nameRow, emailRow, passwordRow])
        group.setRow(passwordRow, hidden: !wizard.passwordVisible)
        emailEntry.isEnabled = wizard.emailEditable

        for entry in [nameEntry, emailEntry, passwordEntry] {
            entry.field.delegate = self
        }

        let column = prefsColumn(spacing: 18, insets: NSEdgeInsets(top: 24, left: 24, bottom: 24, right: 24))
        prefsAddFilling(description, to: column)
        prefsAddFilling(group, to: column)
        let scroll = prefsScrollView(clampMaximum: 480, content: column)
        scroll.setContentHuggingPriority(.defaultLow, for: .vertical)

        nextButton.title = wizard.nextLabel
        nextButton.bezelStyle = .rounded
        nextButton.controlSize = .large
        nextButton.keyEquivalent = "\r"
        nextButton.target = self
        nextButton.action = #selector(nextClicked(_:))

        let stack = prefsColumn(spacing: 0)
        prefsAddFilling(banner, to: stack)
        prefsAddFilling(scroll, to: stack)
        prefsAddFilling(wizardButtonBar([nextButton], cancel: cancelButton), to: stack)
        pageView.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: pageView.topAnchor),
            stack.bottomAnchor.constraint(equalTo: pageView.bottomAnchor),
            stack.leadingAnchor.constraint(equalTo: pageView.leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: pageView.trailingAnchor),
        ])
        pageView.returnHandler = { [weak self] field in
            self?.handleReturn(in: field) ?? false
        }
        view = pageView
    }

    // MARK: Inputs from the controller

    func setBusy(_ busy: Bool) {
        nameEntry.isEnabled = !busy
        emailEntry.isEnabled = !busy && wizard.emailEditable
        passwordEntry.isEnabled = !busy
        nextButton.isEnabled = !busy
    }

    func showProblems(_ p: IdentityProblems, banner text: String?) {
        emailEntry.hasError = p.email
        passwordEntry.hasError = p.password
        banner.reveal(text)
    }

    func focus(_ field: WizardController.IdentityField) {
        let target: NSTextField
        switch field {
        case .email: target = emailEntry.field
        case .password: target = passwordEntry.field
        }
        view.window?.makeFirstResponder(target)
    }

    /// Shows fields the controller set (a linked account, the edited one).
    func setIdentity(_ id: Identity) {
        setText(nameEntry.field, id.displayName)
        setText(emailEntry.field, id.email)
        setText(passwordEntry.field, id.password)
    }

    private func setText(_ field: NSTextField, _ text: String) {
        if field.stringValue != text {
            field.stringValue = text
        }
    }

    // MARK: Outputs to the controller

    private func sync() {
        wizard.setIdentity(name: nameEntry.field.stringValue, email: emailEntry.field.stringValue, password: passwordEntry.field.stringValue)
    }

    @objc private func nextClicked(_ sender: Any?) {
        sync()
        wizard.next()
    }

    /// `entry-activated`: Return in the address goes to the password, in
    /// the password it is Next; the name row has no handler.
    private func handleReturn(in field: NSTextField) -> Bool {
        sync()
        if field === emailEntry.field {
            if wizard.passwordVisible {
                view.window?.makeFirstResponder(passwordEntry.field)
            } else {
                wizard.next()
            }
            return true
        }
        if field === passwordEntry.field {
            wizard.next()
            return true
        }
        return field === nameEntry.field
    }

    // MARK: NSTextFieldDelegate

    func controlTextDidChange(_ obj: Foundation.Notification) {
        sync()
    }

    func control(_ control: NSControl, textView: NSTextView, doCommandBy commandSelector: Selector) -> Bool {
        if commandSelector == #selector(NSResponder.insertNewline(_:)), let field = control as? NSTextField {
            return handleReturn(in: field)
        }
        return false
    }
}
