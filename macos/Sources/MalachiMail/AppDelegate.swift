// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// Application lifecycle, the counterpart of ui/main.go: resolve the paths,
/// find the daemon, open the window, connect; on quit stop the daemon we
/// started.
@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    private var window: MainWindowController?
    private var supervisor: DaemonSupervisor?
    private var client: RPCClient?

    func applicationDidFinishLaunching(_ notification: Foundation.Notification) {
        NSApp.mainMenu = MainMenu.build()

        let paths = Paths.resolve()
        let wc = MainWindowController(paths: paths)
        window = wc
        wc.showWindow(nil)
        NSApp.activate()

        do {
            try paths.ensureDirectories()
            try UnixSocketProbe.check(paths.socket)
        } catch {
            // Nothing can work without a usable socket path or data directory.
            wc.show(.unavailable("\(error)"))
            return
        }

        var launch: DaemonSupervisor.Launch?
        do {
            if let exe = try DaemonSupervisor.locate() {
                launch = DaemonSupervisor.Launch(executable: exe, socket: paths.socket, config: paths.config, store: paths.store)
            }
        } catch {
            // No daemon to start: keep connecting, one may be started by other means.
            wc.show(.unavailable("\(error)"))
        }
        let sup = DaemonSupervisor(launch: launch, socket: paths.socket)
        supervisor = sup
        let c = RPCClient(socketPath: paths.socket)
        client = c
        wc.attach(client: c, supervisor: sup)
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        true
    }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        guard let supervisor else {
            return .terminateNow
        }
        // Stopping the daemon can take up to 15 s (its syncers log out first);
        // finish it off the main thread so the window stays responsive.
        window?.stopReconnecting()
        let client = client
        Task { @MainActor in
            await client?.close()
            await supervisor.stop()
            sender.reply(toApplicationShouldTerminate: true)
        }
        return .terminateLater
    }
}
