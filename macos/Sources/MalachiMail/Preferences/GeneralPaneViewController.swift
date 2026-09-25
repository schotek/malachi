// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The General page of the settings (preferences.blp `general_page`,
/// ui/internal/window/preferences.go): every group and row of the
/// Blueprint plus the macOS-only Keyboard group (deviation D14). The
/// UI-only rows are bound two-way to `Settings`; "Launch at Login" goes
/// through `LoginItemService` (the Background portal's place, `SMAppService`
/// being authoritative); the Mail group is the daemon's, through
/// `MailPreferencesController` (config.get / config.set), and stays
/// insensitive until the daemon answered.
///
/// The pane needs the settings, the client and a toast sink; they come
/// with `init` or later through `configure`, and the bindings start once
/// both they and the view are there. Everything is undone when the window
/// closes (the GTK dialog's `closed`), which the pane notices itself.
@MainActor
final class GeneralPaneViewController: PreferencesPaneViewController {
    // Startup
    let launchAtLogin = NSSwitch()
    let runInBackground = NSSwitch()
    // Reading
    let markReadDelay = PrefsSpinControl(min: 0, max: Settings.markReadDelayMax, value: 2)
    // Deleting
    let confirmDelete = NSSwitch()
    // Notifications
    let desktopNotifications = NSSwitch()
    let notificationSound = NSSwitch()
    // Keyboard (macOS only)
    let commandR = NSPopUpButton(frame: .zero, pullsDown: false)
    // Mail (the daemon's; insensitive until config.get answers)
    let mailGroup = PreferencesGroupView(title: L10n.T("Mail"), description: L10n.T("Stored by the mail daemon."))
    let checkInterval = NSPopUpButton(frame: .zero, pullsDown: false)
    let remoteImages = NSPopUpButton(frame: .zero, pullsDown: false)
    let offlineDays = NSPopUpButton(frame: .zero, pullsDown: false)

    /// The ⌘R pop-up's items, in order.
    static let commandRChoices: [Settings.CommandR] = [.reply, .refresh]

    /// The Mail group's controller; nil until `configure` gave a client.
    private(set) var mail: MailPreferencesController?
    /// The window closed (or the lead said so): bindings undone, late
    /// replies dropped.
    var closed: Bool {
        get { bindings.closed }
        set {
            if newValue {
                bindings.close()
            }
        }
    }

    private var settings: Settings?
    private var client: RPCClient?
    private var toast: (@MainActor (String) -> Void)?
    private let loginItems = LoginItemService()
    private let bindings = PreferenceBindingSet()
    private var launchAtLoginRow: PreferenceRowView?
    private var bound = false
    /// The login switch is being set from the service, not by the user.
    private var revertingLogin = false
    /// The Mail pop-ups are being set from the controller, not by the user.
    private var syncingMail = false

    init(settings: Settings? = nil, client: RPCClient? = nil, toast: (@MainActor (String) -> Void)? = nil) {
        self.settings = settings
        self.client = client
        self.toast = toast
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Supplies what the pane binds to; may be called before or after the
    /// view loaded. A second call after the bindings started is ignored.
    func configure(settings: Settings, client: RPCClient, toast: @escaping @MainActor (String) -> Void) {
        guard !bound else { return }
        self.settings = settings
        self.client = client
        self.toast = toast
        bindIfReady()
    }

    override func loadView() {
        super.loadView()

        let startup = PreferencesGroupView(title: L10n.T("Startup"))
        let loginRow = PreferenceRowView(title: L10n.T("Launch at Login"), subtitle: L10n.T("Start hidden in the background when you log in"), trailing: launchAtLogin)
        launchAtLoginRow = loginRow
        startup.setRows([
            loginRow,
            PreferenceRowView(title: L10n.T("Run in Background"), subtitle: L10n.T("Closing the window keeps Malachi Mail running for notifications"), trailing: runInBackground),
        ])
        addGroup(startup)

        let reading = PreferencesGroupView(title: L10n.T("Reading"))
        reading.setRows([
            PreferenceRowView(
                title: L10n.T("Mark as Read After"),
                subtitle: L10n.T("Seconds a message must be shown before it is marked as read; 0 marks it immediately"),
                trailing: markReadDelay
            ),
        ])
        addGroup(reading)

        let deleting = PreferencesGroupView(title: L10n.T("Deleting"))
        confirmDelete.state = .on
        deleting.setRows([
            PreferenceRowView(title: L10n.T("Confirm Before Deleting"), subtitle: L10n.T("Ask before moving a message to Trash"), trailing: confirmDelete),
        ])
        addGroup(deleting)

        let notifications = PreferencesGroupView(title: L10n.T("Notifications"))
        desktopNotifications.state = .on
        notifications.setRows([
            PreferenceRowView(title: L10n.T("Show Desktop Notifications"), trailing: desktopNotifications),
            PreferenceRowView(title: L10n.T("Play Sound"), subtitle: L10n.T("Play the system new-mail sound"), trailing: notificationSound),
        ])
        addGroup(notifications)

        let keyboard = PreferencesGroupView(title: "Keyboard") // macOS-only string
        commandR.addItems(withTitles: [
            "Reply (as in Mail)", // macOS-only string
            "Check for New Mail (as on Linux)", // macOS-only string
        ])
        keyboard.setRows([
            PreferenceRowView(title: "\u{2318}R", trailing: commandR), // macOS-only string
        ])
        addGroup(keyboard)

        checkInterval.addItems(withTitles: [L10n.T("Manually"), L10n.T("Every 5 minutes"), L10n.T("Every 15 minutes"), L10n.T("Every 30 minutes")])
        checkInterval.selectItem(at: 1)
        remoteImages.addItems(withTitles: [L10n.T("Never"), L10n.T("From Known Senders"), L10n.T("Always")])
        offlineDays.addItems(withTitles: [L10n.T("1 week"), L10n.T("1 month"), L10n.T("3 months"), L10n.T("1 year"), L10n.T("Everything")])
        mailGroup.setRows([
            PreferenceRowView(title: L10n.T("Check for New Mail"), trailing: checkInterval),
            PreferenceRowView(title: L10n.T("Load Remote Images"), trailing: remoteImages),
            PreferenceRowView(title: L10n.T("Keep Mail Offline For"), subtitle: L10n.T("Older messages stay on the server and are not shown"), trailing: offlineDays),
        ])
        mailGroup.isEnabled = false
        addGroup(mailGroup)
    }

    override func viewDidLoad() {
        super.viewDidLoad()
        bindIfReady()
    }

    /// The bindings end with the window; the login switch shows what the
    /// service says every time the page comes up.
    override func viewWillAppear() {
        super.viewWillAppear()
        bindings.watch(view.window)
        refreshLaunchAtLogin()
    }

    // MARK: Bindings

    private func bindIfReady() {
        guard !bound, isViewLoaded, !closed, let settings else { return }
        bound = true

        bindings.add(.bind(runInBackground, to: settings, .runInBackground, \.runInBackground))
        bindings.add(.bind(markReadDelay, to: settings, .markReadDelay, \.markReadDelay))
        bindings.add(.bind(confirmDelete, to: settings, .confirmDelete, \.confirmDelete))
        bindings.add(.bind(desktopNotifications, to: settings, .desktopNotifications, \.desktopNotifications))
        bindings.add(.bind(notificationSound, to: settings, .notificationSound, \.notificationSound))
        bindings.add(.bind(commandR, to: settings, .commandR, choices: GeneralPaneViewController.commandRChoices, \.commandR))

        launchAtLogin.target = self
        launchAtLogin.action = #selector(launchAtLoginChanged(_:))
        refreshLaunchAtLogin()

        if let client {
            bindMail(client)
        }
        bindings.onClose = { [weak self] in
            self?.mail?.close()
        }
    }

    // MARK: Launch at Login (preferences.go `bindLaunchAtLogin`)

    /// The service is authoritative: the switch and the mirror key follow
    /// its status.
    private func refreshLaunchAtLogin() {
        guard bound else { return }
        let enabled = loginItems.isEnabled
        setLoginSwitch(enabled)
        settings?.launchAtLogin = enabled
    }

    private func setLoginSwitch(_ on: Bool) {
        revertingLogin = true
        launchAtLogin.state = on ? .on : .off
        revertingLogin = false
    }

    /// The row is insensitive during the call; the switch (and the mirror
    /// key) only stay flipped when the service granted the change. A
    /// refusal reverts the switch and explains in a toast; a request that
    /// macOS wants the user to approve also opens System Settings.
    @objc private func launchAtLoginChanged(_ sender: Any?) {
        guard !revertingLogin else { return }
        let want = launchAtLogin.state == .on
        launchAtLoginRow?.isEnabled = false
        let outcome: Result<LoginItemService.Outcome, any Error>
        do {
            outcome = .success(try loginItems.set(want))
        } catch {
            outcome = .failure(error)
        }
        guard !closed else { return }
        launchAtLoginRow?.isEnabled = true
        switch outcome {
        case .success(.granted):
            settings?.launchAtLogin = want
        case .success(.requiresApproval):
            setLoginSwitch(!want)
            toast?(L10n.T("Autostart was not granted"))
            loginItems.openSystemSettings()
        case .success(.notGranted):
            setLoginSwitch(!want)
            toast?(L10n.T("Autostart was not granted"))
        case .failure(let error):
            // Typical of an ad-hoc signed or unbundled build: the service
            // refuses to register it.
            setLoginSwitch(!want)
            // macOS-only string
            toast?(String(format: "Launch at Login could not be changed: %@", error.localizedDescription))
        }
    }

    // MARK: Mail (preferences.go `bindMail`)

    private func bindMail(_ client: RPCClient) {
        let mail = MailPreferencesController(client: client)
        self.mail = mail
        mail.onEnabled = { [weak self] on in
            self?.mailGroup.isEnabled = on
        }
        mail.onPreferences = { [weak self] p in
            self?.renderMail(p)
        }
        mail.onDescription = { [weak self] text in
            self?.mailGroup.descriptionText = text
        }
        mail.onToast = { [weak self] text in
            self?.toast?(text)
        }
        for popup in [checkInterval, remoteImages, offlineDays] {
            popup.target = self
            popup.action = #selector(mailChanged(_:))
        }
        mail.load()
    }

    private func renderMail(_ p: Preferences?) {
        guard let p else { return }
        let sel = MailPreferencesController.MailSelection(p)
        syncingMail = true
        checkInterval.selectItem(at: sel.interval)
        remoteImages.selectItem(at: sel.remoteContent)
        offlineDays.selectItem(at: sel.retention)
        syncingMail = false
    }

    @objc private func mailChanged(_ sender: Any?) {
        guard !syncingMail, let mail else { return }
        if sender as AnyObject === checkInterval {
            mail.selectInterval(at: checkInterval.indexOfSelectedItem)
        } else if sender as AnyObject === remoteImages {
            mail.selectRemoteContent(at: remoteImages.indexOfSelectedItem)
        } else if sender as AnyObject === offlineDays {
            mail.selectRetention(at: offlineDays.indexOfSelectedItem)
        }
    }
}
