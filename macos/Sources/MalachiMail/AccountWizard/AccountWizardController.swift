// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import os

/// The "Add Account" / "Edit Account" wizard as a sheet (account_wizard.blp,
/// 520×640): a 44 pt header with Back, the page title and Close, and the
/// pages of `WizardController` swapped below it with a slide. The flow,
/// the strings and the RPC calls live in the controller; this class only
/// shows what it delivers and feeds the fields back.
@MainActor
final class AccountWizardController: NSWindowController {
    /// The open wizards, kept alive while their sheets are up.
    private static var active: [ObjectIdentifier: AccountWizardController] = [:]

    let wizard: WizardController
    private let root: WizardRootViewController
    private var onDone: (@MainActor (AccountID, AccountConfig) -> Void)?
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "accountwizard")

    /// Opens the wizard as a sheet on `parent`. `editing` changes an
    /// existing account (opens on the Servers page). `onDone` runs after
    /// account.add / account.update succeeded, before the sheet closes.
    static func present(
        from parent: NSWindow, client: RPCClient, editing: Account? = nil,
        onDone: @escaping @MainActor (AccountID, AccountConfig) -> Void
    ) {
        let c = AccountWizardController(client: client, editing: editing)
        c.onDone = onDone
        guard let sheet = c.window else { return }
        active[ObjectIdentifier(c)] = c
        parent.beginSheet(sheet) { _ in
            MainActor.assumeIsolated {
                c.wizard.close()
                active[ObjectIdentifier(c)] = nil
            }
        }
        c.wizard.start()
    }

    private init(client: RPCClient, editing: Account?) {
        let wizard = WizardController(client: client, editing: editing)
        self.wizard = wizard
        root = WizardRootViewController(wizard: wizard)
        let window = WizardSheetWindow(
            contentRect: NSRect(x: 0, y: 0, width: 520, height: 640),
            styleMask: [.titled, .fullSizeContentView],
            backing: .buffered, defer: false
        )
        window.title = wizard.title
        window.titleVisibility = .hidden
        window.titlebarAppearsTransparent = true
        window.isReleasedWhenClosed = false
        window.contentViewController = root
        window.initialFirstResponder = root.initialFirstResponder
        super.init(window: window)
        root.onClose = { [weak self] in self?.dismiss() }
        window.onCancel = { [weak self] in self?.dismiss() }
        wizard.onDone = { [weak self] id, cfg in
            guard let self else { return }
            self.onDone?(id, cfg)
            self.dismiss()
        }
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Closes the sheet; late replies are dropped from now on.
    func dismiss() {
        wizard.close()
        guard let window else { return }
        if let parent = window.sheetParent {
            parent.endSheet(window)
        } else {
            window.close()
        }
    }
}

/// The sheet's window: Escape closes it even when nothing in it is the
/// first responder.
@MainActor
final class WizardSheetWindow: NSWindow {
    var onCancel: (() -> Void)?

    override func cancelOperation(_ sender: Any?) {
        onCancel?()
    }
}

/// The header and the page container of the wizard; the pages are its
/// child view controllers.
@MainActor
final class WizardRootViewController: NSViewController {
    /// The header's height (Adw.HeaderBar).
    static let headerHeight: CGFloat = 44

    let wizard: WizardController
    let identity: IdentityPageController
    let servers: ServersPageController
    let signIn = SignInHintPageController()
    let testing: TestingPageController

    private let backButton = NSButton(image: wizardSymbol("chevron.left", pointSize: 14, weight: .semibold), target: nil, action: nil)
    private let closeButton = NSButton(image: wizardSymbol("xmark", pointSize: 13, weight: .semibold), target: nil, action: nil)
    private let titleLabel = NSTextField(labelWithString: "")
    private let pagesHost = NSView()
    private let toasts = WizardToastPresenter()
    private var visible: WizardController.WizardPage = .identity
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "accountwizard")

    /// The close button or Escape.
    var onClose: (() -> Void)?

    var initialFirstResponder: NSView { identity.initialFirstResponder }

    init(wizard: WizardController) {
        self.wizard = wizard
        identity = IdentityPageController(wizard: wizard)
        servers = ServersPageController(wizard: wizard)
        testing = TestingPageController(wizard: wizard)
        super.init(nibName: nil, bundle: nil)
        wire()
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func loadView() {
        // The window takes its content size from this frame.
        let root = NSView(frame: NSRect(x: 0, y: 0, width: 520, height: 640))
        preferredContentSize = root.frame.size

        let header = NSView()
        header.translatesAutoresizingMaskIntoConstraints = false
        titleLabel.font = .systemFont(ofSize: 13, weight: .bold)
        titleLabel.alignment = .center
        titleLabel.lineBreakMode = .byTruncatingTail
        titleLabel.translatesAutoresizingMaskIntoConstraints = false
        for button in [backButton, closeButton] {
            button.isBordered = false
            button.bezelStyle = .accessoryBarAction
            button.imagePosition = .imageOnly
            button.setButtonType(.momentaryPushIn)
            button.translatesAutoresizingMaskIntoConstraints = false
            button.target = self
            button.refusesFirstResponder = true
        }
        backButton.action = #selector(backClicked(_:))
        backButton.toolTip = "Back" // macOS-only string (libadwaita's own in GTK)
        closeButton.action = #selector(closeClicked(_:))
        closeButton.toolTip = "Close" // macOS-only string (libadwaita's own in GTK)
        header.addSubview(backButton)
        header.addSubview(titleLabel)
        header.addSubview(closeButton)
        let divider = PrefsDivider()
        header.addSubview(divider)

        pagesHost.translatesAutoresizingMaskIntoConstraints = false
        root.addSubview(header)
        root.addSubview(pagesHost)
        NSLayoutConstraint.activate([
            header.topAnchor.constraint(equalTo: root.topAnchor),
            header.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            header.trailingAnchor.constraint(equalTo: root.trailingAnchor),
            header.heightAnchor.constraint(equalToConstant: WizardRootViewController.headerHeight),
            backButton.leadingAnchor.constraint(equalTo: header.leadingAnchor, constant: 8),
            backButton.centerYAnchor.constraint(equalTo: header.centerYAnchor),
            backButton.widthAnchor.constraint(equalToConstant: 28),
            backButton.heightAnchor.constraint(equalToConstant: 28),
            closeButton.trailingAnchor.constraint(equalTo: header.trailingAnchor, constant: -8),
            closeButton.centerYAnchor.constraint(equalTo: header.centerYAnchor),
            closeButton.widthAnchor.constraint(equalToConstant: 28),
            closeButton.heightAnchor.constraint(equalToConstant: 28),
            titleLabel.centerXAnchor.constraint(equalTo: header.centerXAnchor),
            titleLabel.centerYAnchor.constraint(equalTo: header.centerYAnchor),
            titleLabel.leadingAnchor.constraint(greaterThanOrEqualTo: backButton.trailingAnchor, constant: 8),
            titleLabel.trailingAnchor.constraint(lessThanOrEqualTo: closeButton.leadingAnchor, constant: -8),
            divider.leadingAnchor.constraint(equalTo: header.leadingAnchor),
            divider.trailingAnchor.constraint(equalTo: header.trailingAnchor),
            divider.bottomAnchor.constraint(equalTo: header.bottomAnchor),
            pagesHost.topAnchor.constraint(equalTo: header.bottomAnchor),
            pagesHost.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            pagesHost.trailingAnchor.constraint(equalTo: root.trailingAnchor),
            pagesHost.bottomAnchor.constraint(equalTo: root.bottomAnchor),
        ])
        view = root
        for page in [identity, servers, signIn, testing] as [NSViewController] {
            addChild(page)
        }
        // The pages are laid out by frame inside the host so that
        // `transition(from:to:)` can swap them.
        pagesHost.frame = NSRect(x: 0, y: 0, width: 520, height: 640 - WizardRootViewController.headerHeight)
        install(pageController(for: wizard.pages.last ?? .identity))
        visible = wizard.pages.last ?? .identity
        applyHeader(wizard.pages)
        toasts.attach(to: root)
    }

    private func install(_ page: NSViewController) {
        page.view.frame = pagesHost.bounds
        page.view.autoresizingMask = [.width, .height]
        pagesHost.addSubview(page.view)
    }

    private func pageController(for page: WizardController.WizardPage) -> NSViewController {
        switch page {
        case .identity: return identity
        case .servers: return servers
        case .goa: return signIn
        case .testing: return testing
        }
    }

    private func title(for page: WizardController.WizardPage) -> String {
        switch page {
        case .identity: return wizard.title
        case .servers: return L10n.T("Server Settings")
        case .goa: return L10n.T("Sign In")
        case .testing: return L10n.T("Connection Test")
        }
    }

    // MARK: Wiring

    private func wire() {
        wizard.onPages = { [weak self] pages in self?.showStack(pages) }
        wizard.onBusy = { [weak self] busy in
            self?.identity.setBusy(busy)
            self?.servers.setBusy(busy)
        }
        wizard.onIdentityProblems = { [weak self] p, banner in self?.identity.showProblems(p, banner: banner) }
        wizard.onServerProblems = { [weak self] p in self?.servers.showProblems(p) }
        wizard.onIdentity = { [weak self] id in self?.identity.setIdentity(id) }
        wizard.onApplyConfig = { [weak self] cfg in self?.servers.apply(cfg) }
        wizard.onLinked = { _ in
            // GNOME Online Accounts does not exist on macOS; the group stays hidden.
        }
        wizard.onGOAHint = { [weak self] _ in
            self?.log.debug("sign-in hint page shown")
        }
        wizard.onTesting = { [weak self] content in self?.testing.show(content) }
        wizard.onFocus = { [weak self] field in self?.identity.focus(field) }
        wizard.onToast = { [weak self] text in self?.toasts.show(text) }
    }

    /// Mirrors the navigation view: the last page is visible, Back pops.
    private func showStack(_ pages: [WizardController.WizardPage]) {
        guard isViewLoaded, let top = pages.last else { return }
        applyHeader(pages)
        guard top != visible else { return }
        let from = pageController(for: visible)
        let to = pageController(for: top)
        let forward = top.rank > visible.rank
        visible = top
        to.view.frame = pagesHost.bounds
        to.view.autoresizingMask = [.width, .height]
        transition(from: from, to: to, options: forward ? .slideForward : .slideBackward, completionHandler: nil)
        if top == .identity {
            view.window?.makeFirstResponder(identity.initialFirstResponder)
        }
    }

    private func applyHeader(_ pages: [WizardController.WizardPage]) {
        backButton.isHidden = pages.count <= 1
        titleLabel.stringValue = title(for: pages.last ?? .identity)
    }

    @objc private func backClicked(_ sender: Any?) {
        wizard.back()
    }

    @objc private func closeClicked(_ sender: Any?) {
        onClose?()
    }

    override func cancelOperation(_ sender: Any?) {
        onClose?()
    }
}
