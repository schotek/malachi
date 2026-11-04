// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The assistant's sign-in page (`Jira.credentialFields`): Jira Cloud
/// asks for the Atlassian account's e-mail address and an API token, with
/// "Create API Token…" opening id.atlassian.com; Data Center for a personal
/// access token alone. Next lists the spaces with them (account.listSpaces,
/// the sign-in test); when a Jira account is edited the address is fixed
/// and Save stores the new token (account.update).
@MainActor
final class JiraCredentialsPageController: NSViewController, NSTextFieldDelegate, JiraWizardPage {
    private let wizard: JiraWizardController
    private let banner = WizardBannerView()
    private let help = PrefsWrappingLabel("", color: .secondaryLabelColor)
    private let helpButton = NSButton(title: "", target: nil, action: nil)
    private let loginEntry = WizardEntryField()
    private let tokenEntry = WizardEntryField(secure: true)
    private let group = PreferencesGroupView()
    private let loginRow: PreferenceRowView
    private let tokenRow: PreferenceRowView
    private let nextButton = NSButton(title: "", target: nil, action: nil)
    private let progress = JiraWizardProgress()
    let cancelButton = WizardCancelButton()
    private let pageView = WizardPageView()
    private var fields: Jira.CredentialPage
    private var busy = false

    init(wizard: JiraWizardController) {
        self.wizard = wizard
        fields = wizard.credentialPage
        loginRow = PreferenceRowView(title: fields.loginLabel, trailing: loginEntry, trailingFills: true)
        tokenRow = PreferenceRowView(title: fields.tokenLabel, trailing: tokenEntry, trailingFills: true)
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// The address while it is still to type, the token otherwise.
    var initialFirstResponder: NSView? {
        fields.showsLogin && wizard.loginEditable && loginEntry.field.stringValue.isEmpty ? loginEntry.field : tokenEntry.field
    }

    override func loadView() {
        group.setRows([loginRow, tokenRow])
        for entry in [loginEntry, tokenEntry] {
            entry.field.delegate = self
        }
        helpButton.bezelStyle = .rounded
        helpButton.target = self
        helpButton.action = #selector(helpClicked(_:))
        let helpRow = NSStackView(views: [helpButton])
        helpRow.orientation = .horizontal
        helpRow.alignment = .centerY

        let column = prefsColumn(spacing: 12, insets: NSEdgeInsets(top: 24, left: 24, bottom: 24, right: 24))
        prefsAddFilling(help, to: column)
        prefsAddFilling(helpRow, to: column)
        column.setCustomSpacing(18, after: helpRow)
        prefsAddFilling(group, to: column)
        let scroll = prefsScrollView(clampMaximum: 480, content: column)

        nextButton.target = self
        nextButton.action = #selector(nextClicked(_:))
        jiraWizardLayout(pageView, banner: banner, content: scroll, button: nextButton, progress: progress, cancel: cancelButton)
        pageView.returnHandler = { [weak self] field in
            self?.handleReturn(in: field) ?? false
        }
        view = pageView
        apply()
    }

    // MARK: Inputs from the controller

    /// The page for the site's deployment; the address comes from the
    /// controller (the edited account's, or what was typed before).
    func show(_ page: Jira.CredentialPage) {
        fields = page
        apply()
    }

    private func apply() {
        guard isViewLoaded else { return }
        help.stringValue = fields.help
        help.isHidden = fields.help.isEmpty
        helpButton.title = fields.helpButton
        helpButton.superview?.isHidden = fields.helpButton.isEmpty
        loginRow.title = fields.loginLabel
        tokenRow.title = fields.tokenLabel
        group.setRow(loginRow, hidden: !fields.showsLogin)
        if loginEntry.field.stringValue != wizard.login {
            loginEntry.field.stringValue = wizard.login
        }
        nextButton.title = wizard.nextLabel(.credentials)
        applyEnabled()
    }

    private func applyEnabled() {
        loginEntry.isEnabled = !busy && wizard.loginEditable
        tokenEntry.isEnabled = !busy
        helpButton.isEnabled = !busy
        nextButton.isEnabled = !busy
    }

    func setBusy(_ progressText: String?) {
        busy = progressText != nil
        progress.show(progressText)
        applyEnabled()
    }

    func showBanner(_ text: String?) {
        banner.reveal(text)
    }

    func showProblems(_ problems: Set<JiraWizardController.Field>) {
        loginEntry.hasError = problems.contains(.login)
        tokenEntry.hasError = problems.contains(.token)
    }

    func view(for field: JiraWizardController.Field) -> NSView? {
        switch field {
        case .login: return loginEntry.field
        case .token: return tokenEntry.field
        default: return nil
        }
    }

    // MARK: Outputs to the controller

    private func sync() {
        wizard.setCredentials(login: loginEntry.field.stringValue, token: tokenEntry.field.stringValue)
    }

    @objc private func nextClicked(_ sender: Any?) {
        sync()
        wizard.next()
    }

    @objc private func helpClicked(_ sender: Any?) {
        wizard.openTokenHelp()
    }

    /// Return in the address goes to the token, in the token it is Next.
    private func handleReturn(in field: NSTextField) -> Bool {
        sync()
        if field === loginEntry.field {
            view.window?.makeFirstResponder(tokenEntry.field)
            return true
        }
        if field === tokenEntry.field {
            wizard.next()
            return true
        }
        return false
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
