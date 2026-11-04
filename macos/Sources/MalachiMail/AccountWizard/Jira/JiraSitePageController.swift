// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The assistant's first page: the explanation, the Site Address row and
/// the text under it (what is wrong with the address, or what the site
/// turned out to be: "Found Acme"), and Next, which looks the site up
/// (account.detectSite). The site's title is untrusted text, shown with
/// `stringValue`.
@MainActor
final class JiraSitePageController: NSViewController, NSTextFieldDelegate, JiraWizardPage {
    private let wizard: JiraWizardController
    private let banner = WizardBannerView()
    private let siteEntry = WizardEntryField()
    private let status = PrefsWrappingLabel("", size: 11, color: .secondaryLabelColor)
    private let nextButton = NSButton(title: "", target: nil, action: nil)
    private let progress = JiraWizardProgress()
    let cancelButton = WizardCancelButton()
    private let pageView = WizardPageView()
    /// What the field allows (`onSiteCheck`) and what the site is.
    private var ok = false
    private var problem = ""
    private var detected = ""
    private var busy = false

    init(wizard: JiraWizardController) {
        self.wizard = wizard
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    var initialFirstResponder: NSView? { siteEntry.field }

    override func loadView() {
        let texts = wizard.texts
        let description = PrefsWrappingLabel(texts.siteDescription, color: .secondaryLabelColor)
        siteEntry.field.placeholderString = Jira.sitePlaceholder
        siteEntry.field.stringValue = wizard.siteInput
        siteEntry.field.delegate = self
        let group = PreferencesGroupView()
        group.setRows([PreferenceRowView(title: texts.siteAddress, trailing: siteEntry, trailingFills: true)])

        let column = prefsColumn(spacing: 12, insets: NSEdgeInsets(top: 24, left: 24, bottom: 24, right: 24))
        prefsAddFilling(description, to: column)
        column.setCustomSpacing(18, after: description)
        prefsAddFilling(group, to: column)
        prefsAddFilling(status, to: column)
        let scroll = prefsScrollView(clampMaximum: 480, content: column)

        nextButton.title = wizard.nextLabel(.site)
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

    func showCheck(ok: Bool, problem: String) {
        self.ok = ok
        self.problem = problem
        apply()
    }

    func showDetected(_ text: String) {
        detected = text
        apply()
    }

    func setBusy(_ progressText: String?) {
        busy = progressText != nil
        progress.show(progressText)
        siteEntry.isEnabled = !busy
        apply()
    }

    func showBanner(_ text: String?) {
        banner.reveal(text)
    }

    func showProblems(_ fields: Set<JiraWizardController.Field>) {
        siteEntry.hasError = fields.contains(.site)
    }

    func view(for field: JiraWizardController.Field) -> NSView? {
        field == .site ? siteEntry.field : nil
    }

    /// The text under the field: the problem with the address first, else
    /// the site found; the button follows the field.
    private func apply() {
        guard isViewLoaded else { return }
        if !problem.isEmpty {
            status.stringValue = problem
            status.textColor = .systemRed
        } else {
            status.stringValue = detected
            status.textColor = .secondaryLabelColor
        }
        status.isHidden = status.stringValue.isEmpty
        nextButton.isEnabled = ok && !busy
    }

    // MARK: Outputs to the controller

    @objc private func nextClicked(_ sender: Any?) {
        wizard.setSite(siteEntry.field.stringValue)
        wizard.next()
    }

    private func handleReturn(in field: NSTextField) -> Bool {
        guard field === siteEntry.field else { return false }
        wizard.setSite(field.stringValue)
        wizard.next()
        return true
    }

    // MARK: NSTextFieldDelegate

    func controlTextDidChange(_ obj: Foundation.Notification) {
        wizard.setSite(siteEntry.field.stringValue)
    }

    func control(_ control: NSControl, textView: NSTextView, doCommandBy commandSelector: Selector) -> Bool {
        if commandSelector == #selector(NSResponder.insertNewline(_:)), let field = control as? NSTextField {
            return handleReturn(in: field)
        }
        return false
    }
}
