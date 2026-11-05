// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import Darwin
import MalachiCore

/// The App Store build's way to run the bridge's setup subcommands
/// (`MCPRegistrationController.setupLauncher`; docs/plans/mac-app-store.md
/// §3.2, §8.3). There malachi-mcp lives in its own bundle with a sandbox of
/// its own, which a child of the sandboxed app cannot have (macOS ends it
/// in `_libsecinit_appsandbox`), and LaunchServices drops the arguments a
/// sandboxed app passes. So the app writes the command line as a request
/// into the app group, `mcp-setup-request.json`, where the bundle's
/// `LSEnvironment` (`MALACHI_MCP_SETUP_REQUEST`) points the bridge, starts
/// the bundle without arguments, and reads the report the bridge writes
/// beside it (`--output`). The command line carries:
///
/// - `--claude-desktop-config` and `--claude-code-config`: the files in the
///   user's real home (`$HOME` of a sandboxed process is its container);
///   the bridge's temporary exceptions allow exactly these;
/// - `--socket`: the daemon's socket in the group, which the registered
///   entry hands to the bridge Claude starts;
/// - `--command`: the bundle's executable, what Claude runs.
///
/// One request at a time: the request file has a fixed name.
enum BridgeSetupLauncher {
    /// The request file, at the top of the app group's container; the
    /// helper's Info.plist names the same path (`mcp-Info.plist.in`).
    static let requestName = "mcp-setup-request.json"

    /// Installs the launcher for the App Store build; nothing for the DMG.
    static func install(paths: Paths) {
        guard Distribution.current == .appStore,
              let group = Distribution.current.groupContainer else { return }
        let socket = paths.socket
        let queue = SetupQueue()
        MCPRegistrationController.setupLauncher = { bridge, subcommand, timeout in
            try await queue.run {
                try await run(bridge: bridge, subcommand: subcommand, timeout: timeout, group: group, socket: socket)
            }
        }
    }

    /// The user's home directory from the account database: inside the
    /// sandbox `NSHomeDirectory()` and `$HOME` are the app's container.
    static func realHome() -> String {
        guard let pw = getpwuid(getuid()), let dir = pw.pointee.pw_dir else { return NSHomeDirectory() }
        return String(cString: dir)
    }

    /// The bundle around `executable`
    /// (`…/Malachi Mail MCP.app/Contents/MacOS/malachi-mcp`).
    static func bundle(of executable: String) -> URL {
        URL(fileURLWithPath: executable).deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
    }

    static func arguments(subcommand: String, output: String, socket: String, home: String, command: String) -> [String] {
        [
            subcommand, "--output", output, "--socket", socket,
            "--claude-desktop-config", home + "/Library/Application Support/Claude/claude_desktop_config.json",
            "--claude-code-config", home + "/.claude.json",
            "--command", command,
        ]
    }

    private static func run(
        bridge: String, subcommand: String, timeout: Duration, group: URL, socket: String
    ) async throws -> (stdout: Data, stderr: Data, status: Int32) {
        let fm = FileManager.default
        let request = group.appendingPathComponent(requestName)
        let output = group.appendingPathComponent("mcp-setup-\(UUID().uuidString).json")
        defer {
            try? fm.removeItem(at: output)
            try? fm.removeItem(at: request)
        }
        let args = arguments(subcommand: subcommand, output: output.path, socket: socket, home: realHome(), command: bridge)
        let body = try JSONSerialization.data(withJSONObject: ["args": args])
        do {
            try body.write(to: request, options: .atomic)
        } catch {
            throw BridgeRunner.RunError.launch("cannot write the setup request: \(error.localizedDescription)")
        }
        let configuration = NSWorkspace.OpenConfiguration()
        configuration.createsNewApplicationInstance = true
        configuration.activates = false
        configuration.addsToRecentItems = false
        configuration.promptsUserIfNeeded = false
        do {
            _ = try await NSWorkspace.shared.openApplication(at: bundle(of: bridge), configuration: configuration)
        } catch {
            // The bridge is no Cocoa application and never checks in, so
            // LaunchServices may report a failure for a process that runs;
            // the report file decides.
        }
        let deadline = ContinuousClock.now + timeout
        while ContinuousClock.now < deadline {
            if let data = try? Data(contentsOf: output) {
                return decode(data)
            }
            try await Task.sleep(for: .milliseconds(100))
        }
        throw BridgeRunner.RunError.timeout(timeout)
    }

    /// The report as `BridgeRunner.run` would have returned it: the JSON on
    /// stdout, or a failure's message as stderr's line and status 1.
    static func decode(_ data: Data) -> (stdout: Data, stderr: Data, status: Int32) {
        struct Failure: Decodable { let error: String }
        if let failure = try? JSONDecoder().decode(Failure.self, from: data) {
            return (Data(), Data((failure.error + "\n").utf8), 1)
        }
        return (data, Data(), 0)
    }
}

/// Runs the setup calls one after another: the request file has one name.
private actor SetupQueue {
    private var tail: Task<Void, Never>?

    func run<T: Sendable>(_ body: @escaping @Sendable () async throws -> T) async throws -> T {
        let previous = tail
        let task = Task<T, Error> {
            await previous?.value
            return try await body()
        }
        tail = Task { _ = try? await task.value }
        return try await task.value
    }
}
