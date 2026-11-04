// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// Restarts the application (macOS only): a detached shell waits for this
/// process to end and opens the bundle again, then the app quits the
/// usual way (`applicationShouldTerminate` stops the daemon; the new
/// instance starts its own). The shell gives up after a minute, so a
/// quit that never happens does not reopen the app later.
@MainActor
enum Relaunch {
    /// Whether a restart is possible: only from an application bundle (a
    /// bare executable, as under `swift run`, has nothing to open).
    static var available: Bool {
        Bundle.main.bundleURL.pathExtension == "app"
    }

    static func now() {
        guard available else { return }
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/bin/sh")
        p.arguments = [
            "-c",
            "i=0; while kill -0 \"$1\" 2>/dev/null; do i=$((i+1)); [ $i -gt 300 ] && exit 0; sleep 0.2; done; exec /usr/bin/open \"$2\"",
            "sh", String(ProcessInfo.processInfo.processIdentifier), Bundle.main.bundleURL.path,
        ]
        p.standardInput = FileHandle.nullDevice
        p.standardOutput = FileHandle.nullDevice
        p.standardError = FileHandle.nullDevice
        do {
            try p.run()
        } catch {
            return
        }
        NSApp.terminate(nil)
    }
}
