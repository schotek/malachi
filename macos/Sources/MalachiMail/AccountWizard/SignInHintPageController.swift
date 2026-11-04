// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The wizard's page for an address of a provider whose sign-in belongs to
/// GNOME Online Accounts (account_wizard.blp `goa_hint_page`): the hint the
/// controller delivers and "Use the Browser Instead" when the daemon offers
/// its own sign-in. GNOME Online Accounts does not exist on macOS, so the
/// Open Online Accounts / Check Again buttons are left out (a deviation in
/// the table of macos/README.md); the page itself is normally unreachable
/// here, because a daemon without GNOME Online Accounts answers
/// account.discover with its own sign-in as the primary config.
@MainActor
final class SignInHintPageController: NSViewController {
    private let wizard: WizardController
    private let page: WizardStatusPageView
    private let browserButton = NSButton(title: "", target: nil, action: nil)
    let cancelButton = WizardCancelButton()

    init(wizard: WizardController) {
        self.wizard = wizard
        browserButton.title = L10n.T("Use the Browser Instead")
        browserButton.isHidden = true
        let column = NSStackView(views: [browserButton])
        column.orientation = .vertical
        column.alignment = .centerX
        column.spacing = 12
        column.translatesAutoresizingMaskIntoConstraints = false
        page = WizardStatusPageView(
            illustration: .symbol(wizardSymbolName("system-users-symbolic")),
            title: L10n.T("Sign In Through GNOME Settings"), description: nil, child: column
        )
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func loadView() {
        browserButton.bezelStyle = .rounded
        browserButton.controlSize = .large
        browserButton.target = self
        browserButton.action = #selector(browserClicked(_:))
        let root = WizardPageView()
        let stack = prefsColumn(spacing: 0)
        prefsAddFilling(page, to: stack)
        prefsAddFilling(wizardButtonBar([], cancel: cancelButton), to: stack)
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

    /// The hint text (linked.go `showGOAHint`) and whether the browser
    /// sign-in is offered instead.
    func show(hint: String, browser: Bool) {
        page.descriptionText = hint
        browserButton.isHidden = !browser
    }

    // MARK: Outputs to the controller

    @objc private func browserClicked(_ sender: Any?) {
        wizard.useBrowser()
    }
}
