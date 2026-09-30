// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The assistant of a Jira account as a sheet, the size of the mail
/// account wizard's (520×640): a 44 pt header with Back and the page
/// title, and the pages of `JiraWizardController` (site, credentials,
/// spaces) swapped below it with a slide. Adding opens on the site;
/// editing (the token) on the credentials. The flow, the texts and the RPC
/// calls live in the controller; this only shows what it delivers and
/// feeds the fields back. Everything from the site (its title, the spaces'
/// names) is shown as plain text.
@MainActor
final class JiraWizardWindowController: NSWindowController {
    /// The open assistants, kept alive while their sheets are up.
    private static var active: [ObjectIdentifier: JiraWizardWindowController] = [:]

    let wizard: JiraWizardController
    private let root: JiraWizardRootViewController
    private var onDone: (@MainActor (AccountID, AccountConfig) -> Void)?

    /// Opens the assistant as a sheet on `parent`. `editing` replaces the
    /// token of an existing Jira account; with `requestToken` the page
    /// says why one is needed (`authRequired`, `authFailed`,
    /// `JiraWizardController.requestToken`). `onDone` runs after
    /// account.add / account.update succeeded, before the sheet closes.
    static func present(
        from parent: NSWindow, client: RPCClient, editing: Account? = nil, requestToken: ErrorCode? = nil,
        onDone: @escaping @MainActor (AccountID, AccountConfig) -> Void
    ) {
        let c = JiraWizardWindowController(client: client, editing: editing, requestToken: requestToken)
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

    private init(client: RPCClient, editing: Account?, requestToken: ErrorCode?) {
        let wizard = JiraWizardController(client: client, editing: editing)
        if let reason = requestToken {
            wizard.requestToken(reason: reason)
        }
        self.wizard = wizard
        root = JiraWizardRootViewController(wizard: wizard)
        let window = WizardSheetWindow(
            contentRect: NSRect(x: 0, y: 0, width: 520, height: 640),
            styleMask: [.titled, .fullSizeContentView],
            backing: .buffered, defer: false
        )
        window.title = wizard.title
        window.titleVisibility = .hidden
        window.titlebarAppearsTransparent = true
        window.isReleasedWhenClosed = false
        window.autorecalculatesKeyViewLoop = true
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

/// What the root asks of each page of the assistant.
@MainActor
protocol JiraWizardPage: NSViewController {
    var cancelButton: WizardCancelButton { get }
    /// The field the page focuses when it appears.
    var initialFirstResponder: NSView? { get }
    /// A call runs (its progress text) or none (nil).
    func setBusy(_ progress: String?)
    func showBanner(_ text: String?)
    /// Flags the page's fields among `fields` and clears the others.
    func showProblems(_ fields: Set<JiraWizardController.Field>)
    /// The page's view of `field`, if it has one.
    func view(for field: JiraWizardController.Field) -> NSView?
}

/// The header and the page container of the assistant; the pages are its
/// child view controllers.
@MainActor
final class JiraWizardRootViewController: NSViewController {
    let wizard: JiraWizardController
    let site: JiraSitePageController
    let credentials: JiraCredentialsPageController
    let spaces: JiraSpacesPageController

    private let backButton = NSButton(image: wizardSymbol("chevron.left", pointSize: 14, weight: .semibold), target: nil, action: nil)
    private let titleLabel = NSTextField(labelWithString: "")
    private let pagesHost = NSView()
    private let toasts = WizardToastPresenter()
    private var visible: JiraWizardController.Page

    /// A page's Cancel button or Escape.
    var onClose: (() -> Void)?

    var initialFirstResponder: NSView? {
        page(for: wizard.pages.last ?? .site).initialFirstResponder
    }

    init(wizard: JiraWizardController) {
        self.wizard = wizard
        site = JiraSitePageController(wizard: wizard)
        credentials = JiraCredentialsPageController(wizard: wizard)
        spaces = JiraSpacesPageController(wizard: wizard)
        visible = wizard.pages.last ?? .site
        super.init(nibName: nil, bundle: nil)
        wire()
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func loadView() {
        let root = NSView(frame: NSRect(x: 0, y: 0, width: 520, height: 640))
        preferredContentSize = root.frame.size

        let header = NSView()
        header.translatesAutoresizingMaskIntoConstraints = false
        titleLabel.font = .systemFont(ofSize: 13, weight: .bold)
        titleLabel.alignment = .center
        titleLabel.lineBreakMode = .byTruncatingTail
        titleLabel.translatesAutoresizingMaskIntoConstraints = false
        backButton.isBordered = false
        backButton.bezelStyle = .accessoryBarAction
        backButton.imagePosition = .imageOnly
        backButton.setButtonType(.momentaryPushIn)
        backButton.translatesAutoresizingMaskIntoConstraints = false
        backButton.target = self
        backButton.action = #selector(backClicked(_:))
        backButton.refusesFirstResponder = true
        backButton.toolTip = "Back" // macOS-only string (libadwaita's own in GTK)
        header.addSubview(backButton)
        header.addSubview(titleLabel)
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
            titleLabel.centerXAnchor.constraint(equalTo: header.centerXAnchor),
            titleLabel.centerYAnchor.constraint(equalTo: header.centerYAnchor),
            titleLabel.leadingAnchor.constraint(greaterThanOrEqualTo: backButton.trailingAnchor, constant: 8),
            titleLabel.trailingAnchor.constraint(lessThanOrEqualTo: header.trailingAnchor, constant: -44),
            divider.leadingAnchor.constraint(equalTo: header.leadingAnchor),
            divider.trailingAnchor.constraint(equalTo: header.trailingAnchor),
            divider.bottomAnchor.constraint(equalTo: header.bottomAnchor),
            pagesHost.topAnchor.constraint(equalTo: header.bottomAnchor),
            pagesHost.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            pagesHost.trailingAnchor.constraint(equalTo: root.trailingAnchor),
            pagesHost.bottomAnchor.constraint(equalTo: root.bottomAnchor),
        ])
        view = root
        for p in [site, credentials, spaces] as [NSViewController] {
            addChild(p)
        }
        // The pages are laid out by frame inside the host so that
        // `transition(from:to:)` can swap them.
        pagesHost.frame = NSRect(x: 0, y: 0, width: 520, height: 640 - WizardRootViewController.headerHeight)
        let top = page(for: visible)
        top.view.frame = pagesHost.bounds
        top.view.autoresizingMask = [.width, .height]
        pagesHost.addSubview(top.view)
        applyHeader()
        toasts.attach(to: root)
    }

    override func viewDidAppear() {
        super.viewDidAppear()
        view.window?.recalculateKeyViewLoop()
    }

    private func page(for p: JiraWizardController.Page) -> any JiraWizardPage {
        switch p {
        case .site: return site
        case .credentials: return credentials
        case .spaces: return spaces
        }
    }

    // MARK: Wiring

    private func wire() {
        for p in [site, credentials, spaces] as [any JiraWizardPage] {
            p.cancelButton.onCancel = { [weak self] in self?.onClose?() }
        }
        wizard.onPages = { [weak self] _ in self?.showStack() }
        wizard.onBusy = { [weak self] progress in
            guard let self else { return }
            for p in [self.site, self.credentials, self.spaces] as [any JiraWizardPage] {
                p.setBusy(progress)
            }
        }
        wizard.onSiteCheck = { [weak self] ok, problem in self?.site.showCheck(ok: ok, problem: problem) }
        wizard.onDetected = { [weak self] text in self?.site.showDetected(text) }
        wizard.onCredentialPage = { [weak self] p in self?.credentials.show(p) }
        wizard.onSpaces = { [weak self] rows, selected in self?.spaces.show(rows: rows, selected: selected) }
        wizard.onSpacesProblem = { [weak self] problem in self?.spaces.showProblem(problem) }
        wizard.onEmailField = { [weak self] shown, email in self?.spaces.showEmail(shown: shown, email: email) }
        wizard.onBanner = { [weak self] p, text in self?.page(for: p).showBanner(text) }
        wizard.onProblems = { [weak self] fields in
            guard let self else { return }
            for p in [self.site, self.credentials, self.spaces] as [any JiraWizardPage] {
                p.showProblems(fields)
            }
        }
        wizard.onFocus = { [weak self] field in
            guard let self else { return }
            for p in [self.site, self.credentials, self.spaces] as [any JiraWizardPage] {
                if let target = p.view(for: field) {
                    self.view.window?.makeFirstResponder(target)
                    return
                }
            }
        }
        wizard.onOpenURL = { [weak self] url in
            openInBrowser(url) { text in self?.toasts.show(text) }
        }
    }

    /// Mirrors a navigation view: the last page is visible, Back pops.
    private func showStack() {
        guard isViewLoaded, let top = wizard.pages.last else { return }
        applyHeader()
        guard top != visible else { return }
        let from = page(for: visible)
        let to = page(for: top)
        let forward = top.rawValue > visible.rawValue
        visible = top
        to.view.frame = pagesHost.bounds
        to.view.autoresizingMask = [.width, .height]
        transition(from: from, to: to, options: forward ? .slideForward : .slideBackward) {
            // The completion runs on the main thread (AppKit), unannotated.
            MainActor.assumeIsolated { [weak self] in
                self?.view.window?.recalculateKeyViewLoop()
            }
        }
        if let first = to.initialFirstResponder {
            view.window?.makeFirstResponder(first)
        }
    }

    private func applyHeader() {
        backButton.isHidden = !wizard.canGoBack
        titleLabel.stringValue = wizard.pageTitle(wizard.pages.last ?? .site)
    }

    @objc private func backClicked(_ sender: Any?) {
        wizard.back()
    }

    override func cancelOperation(_ sender: Any?) {
        onClose?()
    }
}

/// A call under way at the start of a page's button bar: a small spinner
/// and its progress text ("Looking up the Jira site").
@MainActor
final class JiraWizardProgress: NSStackView {
    private let spinner = NSProgressIndicator()
    private let label = NSTextField(labelWithString: "")

    init() {
        super.init(frame: .zero)
        spinner.style = .spinning
        spinner.controlSize = .small
        spinner.isIndeterminate = true
        spinner.isDisplayedWhenStopped = false
        label.font = .systemFont(ofSize: 12)
        label.textColor = .secondaryLabelColor
        label.lineBreakMode = .byTruncatingTail
        label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        orientation = .horizontal
        alignment = .centerY
        spacing = 6
        addArrangedSubview(spinner)
        addArrangedSubview(label)
        isHidden = true
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    func show(_ progress: String?) {
        if let progress {
            label.stringValue = progress
            isHidden = false
            spinner.startAnimation(nil)
        } else {
            spinner.stopAnimation(nil)
            isHidden = true
        }
    }
}

/// Lays a page of the assistant out in `pageView`: the banner at the top,
/// the content filling the middle, the button bar (Cancel, the progress,
/// the page's button) at the bottom.
@MainActor
func jiraWizardLayout(
    _ pageView: WizardPageView, banner: WizardBannerView, content: NSView, button: NSButton,
    progress: JiraWizardProgress, cancel: WizardCancelButton
) {
    button.bezelStyle = .rounded
    button.controlSize = .large
    button.keyEquivalent = "\r"
    let bar = wizardButtonBar([button], cancel: cancel)
    bar.insertView(progress, at: 0, in: .trailing)
    content.setContentHuggingPriority(.defaultLow, for: .vertical)
    let stack = prefsColumn(spacing: 0)
    prefsAddFilling(banner, to: stack)
    prefsAddFilling(content, to: stack)
    prefsAddFilling(bar, to: stack)
    pageView.addSubview(stack)
    NSLayoutConstraint.activate([
        stack.topAnchor.constraint(equalTo: pageView.topAnchor),
        stack.bottomAnchor.constraint(equalTo: pageView.bottomAnchor),
        stack.leadingAnchor.constraint(equalTo: pageView.leadingAnchor),
        stack.trailingAnchor.constraint(equalTo: pageView.trailingAnchor),
    ])
}
