// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import os

/// Application lifecycle, the counterpart of ui/main.go: translations,
/// settings, the attachment sweep, the paths, the daemon, the connection,
/// the menu bar and the main window; on quit stop the daemon we started.
/// The application-level actions (`app.*` in GTK) live here too, at the
/// end of the responder chain, so they work from any window.
@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    private(set) var state: AppState?
    private var mainWindow: MainWindowController?
    private var integration: Integration?
    private var appearance: AppearanceController?
    private var commandRToken: Settings.ChangeToken?
    /// `mailto:` URLs AppKit delivered before the shell was wired (the
    /// open-URL event may arrive before `applicationDidFinishLaunching`
    /// returns); opened as soon as `hooks.openMailto` exists.
    private var pendingMailto: [URL] = []
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "app")

    func applicationDidFinishLaunching(_ notification: Foundation.Notification) {
        // Translations first: every string built from here on is localised.
        L10n.catalogue = Catalogue.default()
        // Settings() registers the gschema defaults with UserDefaults.standard.
        let settings = Settings()
        // Attachments a previous run wrote for opening (docs/security.md §8).
        OpenDir.default.removeAll()

        let paths = Paths.resolve()
        do {
            try paths.ensureDirectories()
        } catch {
            log.error("data directory: \(String(describing: error), privacy: .public)")
        }
        do {
            try UnixSocketProbe.check(paths.socket)
        } catch {
            log.error("socket path: \(String(describing: error), privacy: .public)")
        }

        // The daemon is ours to run: nothing on the desktop starts malachid.
        // One that already answers on the socket is used and left alone.
        var launch: DaemonSupervisor.Launch?
        do {
            if let exe = try DaemonSupervisor.locate() {
                launch = DaemonSupervisor.Launch(
                    executable: exe, socket: paths.socket, config: paths.config, store: paths.store,
                    keychainHelper: paths.keychainHelper)
            }
        } catch {
            log.warning("not starting malachid; expecting one to be started by other means: \(String(describing: error), privacy: .public)")
        }
        let supervisor = DaemonSupervisor(launch: launch, socket: paths.socket)
        let client = RPCClient(socketPath: paths.socket)
        let connection = ConnectionController(client: client, supervisor: supervisor)

        let state = AppState(client: client, connection: connection, settings: settings, paths: paths)
        self.state = state
        appearance = AppearanceController(settings: settings)

        NSApp.mainMenu = MainMenu.build(state)
        commandRToken = settings.onChange(.commandR) {
            MainMenu.apply(commandR: settings.commandR)
        }

        let wc = MainWindowController(state: state)
        mainWindow = wc
        state.mainWindow = wc
        // The sidebar, the settings window, the wizard and the notifications
        // plug into the shell before the connection reports anything.
        integration = Integration(state: state, mainWindow: wc)
        openPendingMailto()
        // A login-item launch starts hidden (preferences.blp: "Start hidden
        // in the background when you log in"); the Dock, a notification or
        // a reopen brings the window. Only the launch Apple event decides:
        // a terminal start always shows the window.
        if LoginItemService.launchedAsLoginItem {
            log.info("started hidden as a login item")
        } else {
            wc.showWindow(nil)
            NSApp.activate()
        }

        connection.start()
    }

    /// Quitting stops the daemon this app started (up to 15 s while its
    /// syncers log out), off the main thread so the windows stay
    /// responsive; "Run in Background" keeps everything alive by hiding
    /// the window instead.
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        guard let state else {
            OpenDir.default.removeAll()
            return .terminateNow
        }
        Task { @MainActor in
            await state.connection.stop()
            OpenDir.default.removeAll()
            sender.reply(toApplicationShouldTerminate: true)
        }
        return .terminateLater
    }

    /// Closing the last window quits unless "Run in Background" is on
    /// (read each time: the setting changes live).
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        !(state?.settings.runInBackground ?? false)
    }

    /// The Dock icon, or a second launch: the (possibly hidden) main window.
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        showMainWindow()
        return false
    }

    /// `mailto:` URLs (CFBundleURLTypes) open a compose window (the GTK
    /// `open` signal); one that arrives before the shell is wired waits
    /// for it. Anything else is logged.
    func application(_ application: NSApplication, open urls: [URL]) {
        for url in urls {
            guard url.scheme?.lowercased() == "mailto" else {
                log.warning("ignoring non-mailto URL with scheme \(url.scheme ?? "", privacy: .public)")
                continue
            }
            pendingMailto.append(url)
        }
        openPendingMailto()
    }

    /// Hands the queued `mailto:` URLs to compose, once it exists.
    private func openPendingMailto() {
        guard let open = state?.hooks.openMailto else { return }
        let urls = pendingMailto
        pendingMailto = []
        for url in urls {
            open(url)
        }
    }

    /// Presents the main window and activates the application (the GTK
    /// `app.show` action: the Dock, a notification, a second launch).
    func showMainWindow() {
        if let state {
            state.showMainWindow()
        } else {
            NSApp.activate()
        }
    }

    // MARK: Application actions (ui/main.go `addActions`)

    @objc func newMessage(_ sender: Any?) {
        state?.hooks.composeNew?()
    }

    @objc func addAccount(_ sender: Any?) {
        state?.hooks.addAccount?(NSApp.keyWindow)
    }

    @objc func showPreferences(_ sender: Any?) {
        state?.hooks.openPreferences?()
    }

    @objc func showAbout(_ sender: Any?) {
        // Built in code from fixed text, never from mail: the one place an
        // attributed string is made by hand. macOS-only strings.
        let credits = NSMutableAttributedString(string: "A native mail client.\n", attributes: [
            .font: NSFont.systemFont(ofSize: NSFont.smallSystemFontSize),
            .foregroundColor: NSColor.labelColor,
        ])
        if let site = URL(string: "https://github.com/schotek/malachi") {
            credits.append(NSAttributedString(string: site.absoluteString, attributes: [
                .font: NSFont.systemFont(ofSize: NSFont.smallSystemFontSize),
                .link: site,
            ]))
        }
        NSApp.orderFrontStandardAboutPanel(options: [
            .applicationName: MainMenu.appName,
            .applicationVersion: Version.full,
            .version: "",
            .credits: credits,
        ])
    }

    @objc func openHelp(_ sender: Any?) {
        guard let url = MainMenu.helpURL else { return }
        NSWorkspace.shared.open(url)
    }
}

extension AppDelegate: NSUserInterfaceValidations {
    /// The application actions are enabled once their hook is wired.
    func validateUserInterfaceItem(_ item: any NSValidatedUserInterfaceItem) -> Bool {
        guard let action = item.action else { return false }
        switch action {
        case Action.newMessage: return state?.hooks.composeNew != nil
        case Action.addAccount: return state?.hooks.addAccount != nil
        case Action.showPreferences: return state?.hooks.openPreferences != nil
        default: return true
        }
    }
}

extension AppDelegate: MalachiActions {}
