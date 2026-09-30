// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import os

/// Claude Desktop as AppKit sees it, for `ClaudeDesktopController`
/// (`platform`): whether it runs (`NSRunningApplication` by its bundle
/// identifier), quitting it (`NSRunningApplication.terminate()`, the
/// ordinary quit request an app gets from the Dock's Quit, then polling
/// `isTerminated` until the timeout), starting it (`NSWorkspace
/// .openApplication` at the URL LaunchServices has for the bundle
/// identifier, without bringing it to the front), and its termination
/// (`NSWorkspace.didTerminateApplicationNotification`, `onTerminate`).
///
/// Nothing is forced: an app that does not quit in time is left running
/// and the controller says so. Nothing here reads or writes Claude
/// Desktop's configuration; `malachi-mcp` does that. The application owns
/// one for its whole run (`AppState`).
@MainActor
final class ClaudeDesktopService: NSObject {
    /// How often a quit looks whether Claude Desktop has terminated.
    static let pollInterval: Duration = .milliseconds(200)
    /// How long a quit waits before it brings Claude Desktop to the front:
    /// it may ask before it quits (sessions still running), and that
    /// question must not wait unseen behind Malachi Mail.
    static let revealAfter: Duration = .milliseconds(1500)

    /// Called when Claude Desktop terminated, whoever quit it.
    var onTerminate: (@MainActor () -> Void)?

    private nonisolated static let log = Logger(subsystem: "io.github.schotek.Malachi", category: "mcp")

    override init() {
        super.init()
        // A selector observer unregisters itself when the service goes;
        // NSWorkspace posts on the main thread.
        NSWorkspace.shared.notificationCenter.addObserver(
            self, selector: #selector(applicationTerminated(_:)),
            name: NSWorkspace.didTerminateApplicationNotification, object: nil)
    }

    /// The closures `ClaudeDesktopController` runs.
    var platform: ClaudeDesktopController.Platform {
        ClaudeDesktopController.Platform(
            isRunning: { !Self.running().isEmpty },
            quit: { await Self.quit(timeout: $0) },
            launch: { Self.launch() })
    }

    @objc private func applicationTerminated(_ note: Foundation.Notification) {
        let app = note.userInfo?[NSWorkspace.applicationUserInfoKey] as? NSRunningApplication
        guard app?.bundleIdentifier == ClaudeDesktopController.bundleIdentifier else { return }
        onTerminate?()
    }

    /// The running instances of Claude Desktop.
    static func running() -> [NSRunningApplication] {
        NSRunningApplication.runningApplications(withBundleIdentifier: ClaudeDesktopController.bundleIdentifier)
            .filter { !$0.isTerminated }
    }

    /// Asks every instance to quit and waits until all have terminated, at
    /// most `timeout`; true when they have (or none ran).
    static func quit(timeout: Duration) async -> Bool {
        let apps = running()
        guard !apps.isEmpty else { return true }
        for app in apps {
            // False: already quitting, or the request was refused; the wait
            // decides either way.
            if !app.terminate() {
                log.info("Claude Desktop (pid \(app.processIdentifier, privacy: .public)) did not accept the quit request")
            }
        }
        let start = ContinuousClock.now
        let deadline = start + timeout
        var revealed = false
        while apps.contains(where: { !$0.isTerminated }) {
            guard ContinuousClock.now < deadline else {
                log.warning("Claude Desktop did not quit within \(timeout, privacy: .public)")
                return false
            }
            if !revealed, ContinuousClock.now >= start + revealAfter {
                revealed = true
                for app in apps where !app.isTerminated {
                    app.activate(options: [])
                }
            }
            try? await Task.sleep(for: pollInterval)
        }
        return true
    }

    /// Starts Claude Desktop in the background, where LaunchServices has
    /// it; a failure is logged.
    static func launch() {
        guard let url = NSWorkspace.shared.urlForApplication(withBundleIdentifier: ClaudeDesktopController.bundleIdentifier) else {
            log.warning("Claude Desktop is not installed where LaunchServices can find it; not started again")
            return
        }
        let configuration = NSWorkspace.OpenConfiguration()
        configuration.activates = false
        NSWorkspace.shared.openApplication(at: url, configuration: configuration) { _, error in
            guard let error else { return }
            let ns = error as NSError
            log.warning("starting Claude Desktop: \(ns.domain, privacy: .public) \(ns.code, privacy: .public)")
        }
    }
}
