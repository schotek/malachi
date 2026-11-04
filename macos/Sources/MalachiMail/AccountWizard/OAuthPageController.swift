// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The wizard's browser sign-in page (account_wizard.blp `oauth_page`, the
/// three children of `oauth_stack`): the prompt with the provider's button,
/// the wait while the browser is open, and the notice that no OAuth client
/// is configured. The buttons sit under the status page as in the
/// Blueprint; the page's Cancel in the bottom bar closes the sheet as on the
/// other pages (the GTK dialog's close button), except while waiting, when
/// the wait's own Cancel is the one on the page and Escape still closes the
/// sheet (cancelling the sign-in). The flow is `WizardController`'s
/// (oauth.go); this only shows `OAuthView`.
@MainActor
final class OAuthPageController: NSViewController {
    private let wizard: WizardController
    private let prompt: WizardStatusPageView
    private let waiting: WizardStatusPageView
    private let unavailable: WizardStatusPageView
    private let signInButton = NSButton(title: "", target: nil, action: nil)
    private let reopenButton = NSButton(title: "", target: nil, action: nil)
    private let cancelSignInButton = NSButton(title: "", target: nil, action: nil)
    private let passwordButton = NSButton(title: "", target: nil, action: nil)
    private var current: WizardController.OAuthView?
    let cancelButton = WizardCancelButton()

    init(wizard: WizardController) {
        self.wizard = wizard
        signInButton.keyEquivalent = "\r"
        reopenButton.title = L10n.T("Open the Browser Again")
        cancelSignInButton.title = wizardLabel("_Cancel")
        passwordButton.title = L10n.T("Use an App Password Instead")
        prompt = WizardStatusPageView(
            illustration: .symbol(wizardSymbolName("web-browser-symbolic")), title: L10n.T("Sign In Through Your Browser"),
            description: nil, child: OAuthPageController.buttonColumn([signInButton])
        )
        waiting = WizardStatusPageView(
            illustration: .spinner, title: L10n.T("Waiting for the sign-in in your browser…"),
            description: L10n.T("Finish signing in there; this page continues by itself."),
            child: OAuthPageController.buttonColumn([reopenButton, cancelSignInButton])
        )
        unavailable = WizardStatusPageView(
            illustration: .symbol(wizardSymbolName("dialog-warning-symbolic")), title: L10n.T("No Sign-In Client Configured"),
            description: nil, child: OAuthPageController.buttonColumn([passwordButton])
        )
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// The Blueprint's button box: vertical, spacing 12, centred.
    private static func buttonColumn(_ buttons: [NSButton]) -> NSView {
        let column = NSStackView(views: buttons)
        column.orientation = .vertical
        column.alignment = .centerX
        column.spacing = 12
        column.translatesAutoresizingMaskIntoConstraints = false
        return column
    }

    override func loadView() {
        let actions: [(NSButton, Selector)] = [
            (signInButton, #selector(signInClicked(_:))),
            (reopenButton, #selector(reopenClicked(_:))),
            (cancelSignInButton, #selector(cancelSignInClicked(_:))),
            (passwordButton, #selector(passwordClicked(_:))),
        ]
        for (button, action) in actions {
            button.bezelStyle = .rounded
            button.controlSize = .large
            button.target = self
            button.action = action
        }

        let pages = NSView()
        pages.translatesAutoresizingMaskIntoConstraints = false
        for page in [prompt, waiting, unavailable] {
            pages.addSubview(page)
            NSLayoutConstraint.activate([
                page.topAnchor.constraint(equalTo: pages.topAnchor),
                page.bottomAnchor.constraint(equalTo: pages.bottomAnchor),
                page.leadingAnchor.constraint(equalTo: pages.leadingAnchor),
                page.trailingAnchor.constraint(equalTo: pages.trailingAnchor),
            ])
        }
        pages.setContentHuggingPriority(.defaultLow, for: .vertical)
        let stack = prefsColumn(spacing: 0)
        prefsAddFilling(pages, to: stack)
        let bar = wizardButtonBar([], cancel: cancelButton)
        // The bar keeps its height while its Cancel is hidden.
        bar.detachesHiddenViews = false
        prefsAddFilling(bar, to: stack)
        let root = WizardPageView()
        root.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: root.topAnchor),
            stack.bottomAnchor.constraint(equalTo: root.bottomAnchor),
            stack.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: root.trailingAnchor),
        ])
        view = root
        apply(current ?? wizard.oauthView)
    }

    // MARK: Inputs from the controller

    /// Shows one child of the stack; only the visible page's suggested
    /// button takes Return.
    func show(_ content: WizardController.OAuthView) {
        current = content
        guard isViewLoaded else { return }
        apply(content)
    }

    private func apply(_ content: WizardController.OAuthView?) {
        var visible = prompt
        signInButton.keyEquivalent = ""
        passwordButton.keyEquivalent = ""
        switch content {
        case .prompt(let description, let label)?:
            prompt.descriptionText = description
            signInButton.title = label
            signInButton.keyEquivalent = "\r"
        case .waiting?:
            visible = waiting
        case .unavailable(let description, let offered)?:
            visible = unavailable
            unavailable.descriptionText = description
            passwordButton.isHidden = !offered
            if offered {
                passwordButton.keyEquivalent = "\r"
            }
        case nil:
            break
        }
        for page in [prompt, waiting, unavailable] {
            page.isHidden = page !== visible
        }
        // One Cancel at a time: while waiting, the page's cancels the
        // sign-in; the sheet's comes back with the prompt.
        cancelButton.isHidden = visible === waiting
        waiting.setSpinning(visible === waiting)
        view.window?.recalculateKeyViewLoop()
    }

    /// The prompt's button waits for account.oauthStart
    /// (`WizardController.onOAuthStarting`).
    func setStarting(_ starting: Bool) {
        signInButton.isEnabled = !starting
    }

    // MARK: Outputs to the controller

    @objc private func signInClicked(_ sender: Any?) { wizard.signInWithProvider() }
    @objc private func reopenClicked(_ sender: Any?) { wizard.reopenBrowser() }
    @objc private func cancelSignInClicked(_ sender: Any?) { wizard.cancelOAuth() }
    @objc private func passwordClicked(_ sender: Any?) { wizard.useAppPassword() }
}
