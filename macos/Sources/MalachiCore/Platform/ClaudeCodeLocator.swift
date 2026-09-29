// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Darwin
import Foundation
import os

/// Finds the user's Claude Code for the assistant panel (the In App target
/// of ui/internal/assistant) and asks it two things: its version and
/// whether it is signed in. Malachi Mail never signs in, never reads a
/// credential and never shows the account: `claude auth status --json` is
/// read for its `loggedIn` only.
///
/// `locate()` looks every time (a few `stat`s): the path of the
/// `assistant-claude-path` setting first, then `Assistant.candidatePaths`
/// (with the Node versions under `~/.nvm/versions/node`), and returns the
/// first that is an executable regular file (a symbolic link counts by its
/// target, but the link's own path is kept: an nvm `claude` is a link to a
/// Node script whose `node` sits beside the link). A GUI application gets
/// launchd's short PATH, hence the list.
///
/// `version()` (`claude --version`, first line) and `signedIn()` run the
/// executable once each through `BridgeRunner`, with `Assistant.childEnv`,
/// in the panel's private directory, bounded by `timeout`; their answers
/// are kept per path until `refresh()`. A run that fails answers nil (not
/// known), which the panel treats as "go ahead" and the settings as
/// nothing to show.
@MainActor
public final class ClaudeCodeLocator {
    public nonisolated static let defaultTimeout: Duration = .seconds(10)
    /// The most of the version line that is kept.
    nonisolated static let versionLimit = 100

    public let settings: Settings
    private let environment: [String: String]
    private let runner: BridgeRunner
    private let timeout: Duration
    private let directory: URL?
    private let usable: @Sendable (String) -> Bool
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "assistant")

    private var versions: [String: Task<String?, Never>] = [:]
    private var signIns: [String: Task<Bool?, Never>] = [:]

    /// - Parameters:
    ///   - settings: where `assistant-claude-path` is read.
    ///   - environment: the application's environment (HOME, PATH, and
    ///     what `Assistant.childEnv` keeps for the child).
    ///   - runner, timeout: how `claude` is run.
    ///   - directory: the working directory of the runs (the panel's
    ///     private directory); nil leaves the application's.
    ///   - usable: whether a candidate is the one (tests keep it to their
    ///     own directory).
    public init(
        settings: Settings, environment: [String: String] = ProcessInfo.processInfo.environment,
        runner: BridgeRunner = BridgeRunner(), timeout: Duration = ClaudeCodeLocator.defaultTimeout, directory: URL? = nil,
        usable: @escaping @Sendable (String) -> Bool = ClaudeCodeLocator.isExecutableFile
    ) {
        self.settings = settings
        self.environment = environment
        self.runner = runner
        self.timeout = timeout
        self.directory = directory
        self.usable = usable
    }

    private var home: String {
        if let h = environment["HOME"], !h.isEmpty {
            return h
        }
        return NSHomeDirectory()
    }

    /// Every path looked at, in order: the setting's, then the usual
    /// places.
    public func candidates() -> [String] {
        let home = home
        let nvm = (try? FileManager.default.contentsOfDirectory(atPath: home + "/.nvm/versions/node")) ?? []
        var list = Assistant.candidatePaths(home: home, pathEnv: environment["PATH"] ?? "", nvmVersions: nvm)
        let chosen = settings.assistantClaudePath
        if chosen.utf8.first == UInt8(ascii: "/") { // path.IsAbs
            let clean = Assistant.cleanPath(chosen)
            list.removeAll { $0 == clean }
            list.insert(clean, at: 0)
        }
        return list
    }

    /// The claude executable to run, nil when there is none.
    public func locate() -> String? {
        candidates().first(where: usable)
    }

    /// Whether `path` is (or links to) a regular file this user may run.
    public nonisolated static func isExecutableFile(_ path: String) -> Bool {
        var st = stat()
        guard stat(path, &st) == 0, (st.st_mode & S_IFMT) == S_IFREG else { return false }
        return access(path, X_OK) == 0
    }

    /// Forgets the versions and sign-in states asked so far.
    public func refresh() {
        versions = [:]
        signIns = [:]
    }

    /// `claude --version`'s first line for the located executable ("2.1.178
    /// (Claude Code)"), nil when there is none or it failed.
    public func version() async -> String? {
        guard let path = locate() else { return nil }
        if let known = versions[path] {
            return await known.value
        }
        let task = run(path, ["--version"]) { out, status in
            guard status == 0 else { return nil as String? }
            let line = MCPRegistrationController.firstLine(out, limit: Self.versionLimit)
            return line.isEmpty ? nil : line
        }
        versions[path] = task
        return await task.value
    }

    /// Whether the located Claude Code is signed in (`claude auth status
    /// --json`, its `loggedIn`); nil when there is none, the run failed or
    /// the output said nothing.
    public func signedIn() async -> Bool? {
        guard let path = locate() else { return nil }
        if let known = signIns[path] {
            return await known.value
        }
        let task = run(path, ["auth", "status", "--json"]) { out, _ in
            // The status may be non-zero when signed out; the JSON counts.
            struct Status: Decodable {
                var loggedIn: Bool?
            }
            return (try? JSONDecoder().decode(Status.self, from: out))?.loggedIn
        }
        signIns[path] = task
        return await task.value
    }

    /// Runs `claude` with `args` off the main actor and reads its stdout
    /// with `read`; nil when it could not run or timed out.
    private func run<T: Sendable>(
        _ path: String, _ args: [String], _ read: @escaping @Sendable (Data, Int32) -> T?
    ) -> Task<T?, Never> {
        let runner = runner
        let timeout = timeout
        let env = Assistant.childEnvironment(environment, claudePath: path)
        let directory = directory.flatMap { Self.ensureDirectory($0) ? $0 : nil }
        let log = log
        return Task.detached {
            do {
                let out = try await runner.run(path, args, timeout: timeout, environment: env, directory: directory)
                return read(out.stdout, out.status)
            } catch {
                log.warning("claude \(args.first ?? "", privacy: .public): \(String(describing: error), privacy: .public)")
                return nil
            }
        }
    }

    /// Creates the private directory (0700) when it is missing; false when
    /// it cannot be had.
    nonisolated static func ensureDirectory(_ url: URL) -> Bool {
        do {
            try FileManager.default.createDirectory(at: url, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
            return true
        } catch {
            return false
        }
    }

    /// `~/Library/Caches/Malachi Mail/assistant`: the working directory of
    /// Claude Code, empty and private (the panel creates it on demand).
    public nonisolated static var defaultDirectory: URL {
        let caches = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask).first
            ?? FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Caches", isDirectory: true)
        return caches.appendingPathComponent("Malachi Mail", isDirectory: true).appendingPathComponent("assistant", isDirectory: true)
    }
}
