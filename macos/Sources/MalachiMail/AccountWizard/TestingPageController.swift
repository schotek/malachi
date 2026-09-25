// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The wizard's Connection Test page (account_wizard.blp `testing_page`):
/// a progress status page while a call runs, then the results with one row
/// per endpoint and the buttons the outcome allows.
@MainActor
final class TestingPageController: NSViewController {
    private let wizard: WizardController
    private let progress: WizardStatusPageView
    private let results: WizardStatusPageView
    private let resultsGroup = PreferencesGroupView()
    private let imapIcon = NSImageView()
    private let smtpIcon = NSImageView()
    private let graphIcon = NSImageView()
    private let imapRow: PreferenceRowView
    private let smtpRow: PreferenceRowView
    private let graphRow: PreferenceRowView
    private let editButton = NSButton(title: "", target: nil, action: nil)
    private let retryButton = NSButton(title: "", target: nil, action: nil)
    private let addAnywayButton = NSButton(title: "", target: nil, action: nil)
    private let addButton = NSButton(title: "", target: nil, action: nil)
    let cancelButton = WizardCancelButton()

    init(wizard: WizardController) {
        self.wizard = wizard
        progress = WizardStatusPageView(illustration: .spinner, title: L10n.T("Testing Connection…"), description: L10n.T("This can take a moment."))
        for icon in [imapIcon, smtpIcon, graphIcon] {
            icon.imageScaling = .scaleNone
            icon.contentTintColor = .secondaryLabelColor
            icon.widthAnchor.constraint(equalToConstant: 20).isActive = true
        }
        imapRow = PreferenceRowView(title: L10n.T("Incoming (IMAP)"), subtitle: " ", prefix: imapIcon)
        smtpRow = PreferenceRowView(title: L10n.T("Outgoing (SMTP)"), subtitle: " ", prefix: smtpIcon)
        graphRow = PreferenceRowView(title: L10n.T("Microsoft 365"), subtitle: " ", prefix: graphIcon)
        resultsGroup.setRows([imapRow, smtpRow, graphRow])
        let clamp = PrefsClampView(maximum: 480, child: resultsGroup)
        results = WizardStatusPageView(illustration: .symbol("exclamationmark.triangle"), title: "", description: nil, child: clamp)
        super.init(nibName: nil, bundle: nil)
        resultsGroup.setRow(graphRow, hidden: true)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func loadView() {
        let pages = NSView()
        pages.translatesAutoresizingMaskIntoConstraints = false
        for page in [progress, results] {
            pages.addSubview(page)
            NSLayoutConstraint.activate([
                page.topAnchor.constraint(equalTo: pages.topAnchor),
                page.bottomAnchor.constraint(equalTo: pages.bottomAnchor),
                page.leadingAnchor.constraint(equalTo: pages.leadingAnchor),
                page.trailingAnchor.constraint(equalTo: pages.trailingAnchor),
            ])
        }
        results.isHidden = true

        editButton.title = wizardLabel("_Edit Servers")
        retryButton.title = wizardLabel("_Retry")
        addAnywayButton.title = wizard.addAnywayLabel
        addButton.title = wizard.addLabel
        addButton.keyEquivalent = "\r"
        let actions: [(NSButton, Selector)] = [
            (editButton, #selector(editClicked(_:))),
            (retryButton, #selector(retryClicked(_:))),
            (addAnywayButton, #selector(addAnywayClicked(_:))),
            (addButton, #selector(addClicked(_:))),
        ]
        for (button, action) in actions {
            button.bezelStyle = .rounded
            button.controlSize = .large
            button.target = self
            button.action = action
            button.isHidden = true
        }

        pages.setContentHuggingPriority(.defaultLow, for: .vertical)
        let stack = prefsColumn(spacing: 0)
        prefsAddFilling(pages, to: stack)
        prefsAddFilling(wizardButtonBar([editButton, retryButton, addAnywayButton, addButton], cancel: cancelButton), to: stack)
        let root = WizardPageView()
        root.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: root.topAnchor),
            stack.bottomAnchor.constraint(equalTo: root.bottomAnchor),
            stack.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: root.trailingAnchor),
        ])
        view = root
    }

    // MARK: Inputs from the controller

    func show(_ content: WizardController.TestingView) {
        switch content {
        case .progress(let title):
            progress.title = title
            progress.setSpinning(true)
            progress.isHidden = false
            results.isHidden = true
            showButtons(.none)
        case .results(let v):
            results.symbolName = wizardSymbolName(v.icon)
            results.title = v.title
            results.descriptionText = v.description ?? ""
            apply(v.imap, to: imapRow, icon: imapIcon)
            apply(v.smtp, to: smtpRow, icon: smtpIcon)
            apply(v.graph, to: graphRow, icon: graphIcon)
            showButtons(v.buttons)
            progress.setSpinning(false)
            progress.isHidden = true
            results.isHidden = false
        }
    }

    private func apply(_ row: WizardController.EndpointRow?, to view: PreferenceRowView, icon: NSImageView) {
        resultsGroup.setRow(view, hidden: row == nil)
        guard let row else { return }
        view.subtitle = row.text
        icon.image = wizardSymbol(wizardSymbolName(row.icon), pointSize: 15)
        icon.contentTintColor = row.icon == "emblem-ok-symbolic" ? .systemGreen
            : row.icon == "dialog-error-symbolic" ? .systemRed
            : .secondaryLabelColor
    }

    private func showButtons(_ b: WizardController.WizardButtons) {
        editButton.isHidden = !b.edit
        retryButton.isHidden = !b.retry
        addAnywayButton.isHidden = !b.addAnyway
        addButton.isHidden = !b.add
    }

    // MARK: Outputs to the controller

    @objc private func editClicked(_ sender: Any?) { wizard.edit() }
    @objc private func retryClicked(_ sender: Any?) { wizard.retry() }
    @objc private func addAnywayClicked(_ sender: Any?) { wizard.addAnyway() }
    @objc private func addClicked(_ sender: Any?) { wizard.add() }
}
