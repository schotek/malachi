// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The assistant of an eWay-CRM account as a sheet (docs/eway.md §2.4), the
/// size and shape of the Jira one: a 44 pt header with Back and the page
/// title, and the two pages of `EWayWizardController` swapped below it
/// with a slide — the web service address (account.detectEway) and the
/// sign-in (account.test, then account.add). Editing (Settings →
/// Integrations, the row's Edit) opens on the sign-in with the address
/// fixed. The flow, the texts and the calls live in the controller; this
/// only shows what it delivers and feeds the fields back. Opened only from
/// Settings → Integrations (the owner kept the other entry points as they
/// are).
@MainActor
final class EWayWizardWindowController: NSWindowController {
    private static var active: [ObjectIdentifier: EWayWizardWindowController] = [:]

    let wizard: EWayWizardController
    private let root: EWayWizardRootViewController
    private var onDone: (@MainActor (AccountID, AccountConfig) -> Void)?

    /// Opens the assistant as a sheet on `parent`; `editing` changes an
    /// existing eWay-CRM account. `onDone` runs after account.add /
    /// account.update succeeded, before the sheet closes.
    static func present(
        from parent: NSWindow, client: RPCClient, editing: Account? = nil,
        onDone: @escaping @MainActor (AccountID, AccountConfig) -> Void
    ) {
        let c = EWayWizardWindowController(client: client, editing: editing)
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
        let wizard = EWayWizardController(client: client, editing: editing)
        self.wizard = wizard
        root = EWayWizardRootViewController(wizard: wizard)
        let window = WizardSheetWindow(
            contentRect: NSRect(x: 0, y: 0, width: 520, height: 560),
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

/// What the root asks of each page.
@MainActor
protocol EWayWizardPage: NSViewController {
    var cancelButton: WizardCancelButton { get }
    var initialFirstResponder: NSView? { get }
    func setBusy(_ progress: String?)
    func showBanner(_ text: String?)
    func showProblem(_ field: CRM.WizardField)
    func view(for field: CRM.WizardField) -> NSView?
}

/// The header and the page container; the pages are its children.
@MainActor
final class EWayWizardRootViewController: NSViewController {
    static let size = NSSize(width: 520, height: 560)

    let wizard: EWayWizardController
    let service: EWayServicePageController
    let credentials: EWayCredentialsPageController

    private let backButton = NSButton(image: wizardSymbol("chevron.left", pointSize: 14, weight: .semibold), target: nil, action: nil)
    private let titleLabel = NSTextField(labelWithString: "")
    private let pagesHost = NSView()
    private var visible: CRM.WizardPage

    var onClose: (() -> Void)?

    var initialFirstResponder: NSView? { page(for: wizard.top).initialFirstResponder }

    init(wizard: EWayWizardController) {
        self.wizard = wizard
        service = EWayServicePageController(wizard: wizard)
        credentials = EWayCredentialsPageController(wizard: wizard)
        visible = wizard.top
        super.init(nibName: nil, bundle: nil)
        wire()
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    private var pages: [any EWayWizardPage] { [service, credentials] }

    override func loadView() {
        let root = NSView(frame: NSRect(origin: .zero, size: Self.size))
        preferredContentSize = Self.size
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
        addChild(service)
        addChild(credentials)
        pagesHost.frame = NSRect(
            x: 0, y: 0, width: Self.size.width, height: Self.size.height - WizardRootViewController.headerHeight)
        let top = page(for: visible)
        top.view.frame = pagesHost.bounds
        top.view.autoresizingMask = [.width, .height]
        pagesHost.addSubview(top.view)
        applyHeader()
    }

    override func viewDidAppear() {
        super.viewDidAppear()
        view.window?.recalculateKeyViewLoop()
    }

    private func page(for p: CRM.WizardPage) -> any EWayWizardPage {
        p == .service ? service : credentials
    }

    private func wire() {
        for p in pages {
            p.cancelButton.onCancel = { [weak self] in self?.onClose?() }
        }
        wizard.onPages = { [weak self] _ in self?.showStack() }
        wizard.onBusy = { [weak self] progress in
            self?.pages.forEach { $0.setBusy(progress) }
        }
        wizard.onServiceCheck = { [weak self] ok, problem in self?.service.showCheck(ok: ok, problem: problem) }
        wizard.onName = { [weak self] name in self?.credentials.showName(name) }
        wizard.onBanner = { [weak self] p, text in self?.page(for: p).showBanner(text) }
        wizard.onProblem = { [weak self] field in
            self?.pages.forEach { $0.showProblem(field) }
        }
        wizard.onFocus = { [weak self] field in
            guard let self else { return }
            for p in self.pages {
                if let target = p.view(for: field) {
                    self.view.window?.makeFirstResponder(target)
                    return
                }
            }
        }
    }

    private func showStack() {
        guard isViewLoaded else { return }
        applyHeader()
        let top = wizard.top
        guard top != visible else { return }
        let from = page(for: visible)
        let to = page(for: top)
        let forward = top.rawValue > visible.rawValue
        visible = top
        credentials.refresh()
        to.view.frame = pagesHost.bounds
        to.view.autoresizingMask = [.width, .height]
        transition(from: from, to: to, options: forward ? .slideForward : .slideBackward) {
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
        titleLabel.stringValue = wizard.isEditing ? wizard.title : wizard.pageTitle(wizard.top)
    }

    @objc private func backClicked(_ sender: Any?) {
        wizard.back()
    }

    override func cancelOperation(_ sender: Any?) {
        onClose?()
    }
}

/// The first page: the Web Service Address row, the hint under it (or what
/// is wrong with the address) and Next (account.detectEway).
@MainActor
final class EWayServicePageController: NSViewController, NSTextFieldDelegate, EWayWizardPage {
    private let wizard: EWayWizardController
    private let banner = WizardBannerView()
    private let entry = WizardEntryField()
    private let status = PrefsWrappingLabel("", size: 11, color: .secondaryLabelColor)
    private let nextButton = NSButton(title: "", target: nil, action: nil)
    private let progress = JiraWizardProgress()
    let cancelButton = WizardCancelButton()
    private let pageView = WizardPageView()
    private var ok = false
    private var problem = ""
    private var busy = false

    init(wizard: EWayWizardController) {
        self.wizard = wizard
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    var initialFirstResponder: NSView? { entry.field }

    override func loadView() {
        let texts = wizard.texts
        entry.field.stringValue = wizard.serviceInput
        entry.field.delegate = self
        let group = PreferencesGroupView()
        group.setRows([PreferenceRowView(title: texts.serviceLabel, trailing: entry, trailingFills: true)])
        let column = prefsColumn(spacing: 12, insets: NSEdgeInsets(top: 24, left: 24, bottom: 24, right: 24))
        prefsAddFilling(group, to: column)
        prefsAddFilling(status, to: column)
        let scroll = prefsScrollView(clampMaximum: 480, content: column)
        nextButton.title = mn(wizard.nextLabel(.service))
        nextButton.target = self
        nextButton.action = #selector(nextClicked(_:))
        jiraWizardLayout(pageView, banner: banner, content: scroll, button: nextButton, progress: progress, cancel: cancelButton)
        pageView.returnHandler = { [weak self] field in
            guard let self, field === self.entry.field else { return false }
            self.nextClicked(nil)
            return true
        }
        view = pageView
        apply()
    }

    func showCheck(ok: Bool, problem: String) {
        self.ok = ok
        self.problem = problem
        apply()
    }

    func setBusy(_ progressText: String?) {
        busy = progressText != nil
        progress.show(progressText)
        entry.isEnabled = !busy
        apply()
    }

    func showBanner(_ text: String?) {
        banner.reveal(text)
    }

    func showProblem(_ field: CRM.WizardField) {
        entry.hasError = field == .service
    }

    func view(for field: CRM.WizardField) -> NSView? {
        field == .service ? entry.field : nil
    }

    /// The text under the field: what is wrong with the address, else the
    /// hint; Next follows the field.
    private func apply() {
        guard isViewLoaded else { return }
        if problem.isEmpty {
            status.stringValue = wizard.texts.serviceHint
            status.textColor = .secondaryLabelColor
        } else {
            status.stringValue = problem
            status.textColor = .systemRed
        }
        nextButton.isEnabled = ok && !busy
    }

    @objc private func nextClicked(_ sender: Any?) {
        wizard.setService(entry.field.stringValue)
        wizard.next()
    }

    func controlTextDidChange(_ obj: Foundation.Notification) {
        wizard.setService(entry.field.stringValue)
    }

    func control(_ control: NSControl, textView: NSTextView, doCommandBy commandSelector: Selector) -> Bool {
        if commandSelector == #selector(NSResponder.insertNewline(_:)), control === entry.field {
            nextClicked(nil)
            return true
        }
        return false
    }
}

/// The sign-in page: the address (fixed, plain text), the account's name,
/// the user name and the password (optional when editing), the check box
/// "Also save later messages of saved conversations" with its hint, and Add
/// Account (or Save): account.test, then account.add / account.update.
@MainActor
final class EWayCredentialsPageController: NSViewController, NSTextFieldDelegate, EWayWizardPage {
    private let wizard: EWayWizardController
    private let banner = WizardBannerView()
    private let serviceLabel = NSTextField(labelWithString: "")
    private let nameEntry = WizardEntryField()
    private let userEntry = WizardEntryField()
    private let passwordEntry = WizardEntryField(secure: true)
    private let passwordRow: PreferenceRowView
    /// "Also save later messages of saved conversations" (docs/eway.md
    /// D22): checked for a new account, the account's value when editing.
    private let followBox = NSButton(checkboxWithTitle: "", target: nil, action: nil)
    private let followHint = PrefsWrappingLabel("", size: 11, color: .secondaryLabelColor)
    private let nextButton = NSButton(title: "", target: nil, action: nil)
    private let progress = JiraWizardProgress()
    let cancelButton = WizardCancelButton()
    private let pageView = WizardPageView()
    private var busy = false

    init(wizard: EWayWizardController) {
        self.wizard = wizard
        passwordRow = PreferenceRowView(title: wizard.passwordLabel, trailing: passwordEntry, trailingFills: true)
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    var initialFirstResponder: NSView? {
        userEntry.field.stringValue.isEmpty ? userEntry.field : passwordEntry.field
    }

    override func loadView() {
        let texts = wizard.texts
        serviceLabel.lineBreakMode = .byTruncatingMiddle
        serviceLabel.textColor = .secondaryLabelColor
        serviceLabel.isSelectable = true
        serviceLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        for e in [nameEntry, userEntry, passwordEntry] {
            e.field.delegate = self
        }
        nameEntry.field.stringValue = wizard.name
        userEntry.field.stringValue = wizard.user
        let group = PreferencesGroupView()
        group.setRows([
            PreferenceRowView(title: texts.serviceLabel, trailing: serviceLabel),
            PreferenceRowView(title: texts.nameLabel, trailing: nameEntry, trailingFills: true),
            PreferenceRowView(title: texts.userLabel, trailing: userEntry, trailingFills: true),
            passwordRow,
        ])
        followBox.title = texts.followThreads
        followBox.state = wizard.followThreads ? .on : .off
        followBox.target = self
        followBox.action = #selector(followToggled(_:))
        followHint.stringValue = texts.followThreadsHint
        // The hint under the box's title, past the box itself.
        let hint = NSStackView(views: [followHint])
        hint.edgeInsets = NSEdgeInsets(top: 0, left: 20, bottom: 0, right: 0)
        let follow = NSStackView(views: [followBox, hint])
        follow.orientation = .vertical
        follow.alignment = .leading
        follow.spacing = 2
        follow.edgeInsets = NSEdgeInsets(top: 0, left: 4, bottom: 0, right: 4)
        let column = prefsColumn(spacing: 12, insets: NSEdgeInsets(top: 24, left: 24, bottom: 24, right: 24))
        prefsAddFilling(group, to: column)
        column.setCustomSpacing(18, after: group)
        prefsAddFilling(follow, to: column)
        hint.widthAnchor.constraint(equalTo: follow.widthAnchor, constant: -8).isActive = true
        let scroll = prefsScrollView(clampMaximum: 480, content: column)
        nextButton.target = self
        nextButton.action = #selector(nextClicked(_:))
        jiraWizardLayout(pageView, banner: banner, content: scroll, button: nextButton, progress: progress, cancel: cancelButton)
        pageView.returnHandler = { [weak self] field in
            self?.handleReturn(in: field) ?? false
        }
        view = pageView
        refresh()
    }

    /// The address and the button as the controller has them now.
    func refresh() {
        guard isViewLoaded else { return }
        serviceLabel.stringValue = wizard.serviceURL
        serviceLabel.toolTip = wizard.serviceURL
        nextButton.title = mn(wizard.nextLabel(.credentials))
        passwordRow.title = wizard.passwordLabel
        applyEnabled()
    }

    func showName(_ name: String) {
        _ = view
        if nameEntry.field.stringValue != name {
            nameEntry.field.stringValue = name
        }
    }

    private func applyEnabled() {
        for e in [nameEntry, userEntry, passwordEntry] {
            e.isEnabled = !busy
        }
        followBox.isEnabled = !busy
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

    func showProblem(_ field: CRM.WizardField) {
        userEntry.hasError = field == .user
        passwordEntry.hasError = field == .password
    }

    func view(for field: CRM.WizardField) -> NSView? {
        switch field {
        case .user: return userEntry.field
        case .password: return passwordEntry.field
        default: return nil
        }
    }

    private func sync() {
        wizard.setName(nameEntry.field.stringValue)
        wizard.setCredentials(user: userEntry.field.stringValue, password: passwordEntry.field.stringValue)
    }

    @objc private func nextClicked(_ sender: Any?) {
        sync()
        wizard.next()
    }

    @objc private func followToggled(_ sender: NSButton) {
        wizard.setFollowThreads(sender.state == .on)
    }

    /// Return moves on to the next field; in the password it is the button.
    private func handleReturn(in field: NSTextField) -> Bool {
        sync()
        switch field {
        case nameEntry.field:
            view.window?.makeFirstResponder(userEntry.field)
        case userEntry.field:
            view.window?.makeFirstResponder(passwordEntry.field)
        case passwordEntry.field:
            wizard.next()
        default:
            return false
        }
        return true
    }

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
