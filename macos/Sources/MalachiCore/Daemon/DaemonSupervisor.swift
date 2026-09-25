// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Darwin
import Foundation

/// Starts and stops malachid, the Swift counterpart of ui/internal/daemon.
///
/// Process management only: it knows where the daemon binary is, which
/// socket, config and store to hand it and whether it is still alive. It
/// never speaks the protocol. A daemon that already answers on the socket
/// (`make run-backend`, a debugger, one left behind by a crash) is used as is
/// and never stopped: the supervisor only ever signals the process it
/// started itself.
public actor DaemonSupervisor {
    /// How the daemon is launched. Arguments go to `Process` as an array,
    /// so spaces in the paths need no quoting.
    public struct Launch: Sendable {
        public var executable: URL
        public var socket: String
        public var config: String
        public var store: String
        /// The bundled `malachi-keychain` (`Paths.keychainHelper`), which
        /// the daemon gets as its keyring; nil runs it without one.
        public var keychainHelper: URL?

        public init(executable: URL, socket: String, config: String, store: String, keychainHelper: URL? = nil) {
            self.executable = executable
            self.socket = socket
            self.config = config
            self.store = store
            self.keychainHelper = keychainHelper
        }
    }

    public enum SupervisorError: Error, Sendable, CustomStringConvertible {
        case noDaemon
        case backoff(failures: Int, retryIn: Duration)
        case exitedEarly(String)
        case startTimeout(socket: String)
        case stopping

        public var description: String {
            switch self {
            case .noDaemon:
                return "malachid not found beside the app or on PATH (\(DaemonSupervisor.daemonEnv)=none switches the automatic start off)"
            case .backoff(let failures, let retryIn):
                return "malachid exited \(failures) times in a row; next start in \(retryIn.components.seconds) s"
            case .exitedEarly(let what):
                return "malachid \(what) before opening its socket"
            case .startTimeout(let socket):
                return "malachid did not open \(socket) within \(DaemonSupervisor.startTimeout.components.seconds) s"
            case .stopping:
                return "the application is quitting"
            }
        }
    }

    /// `MALACHI_DAEMON`: a path, or `none` / empty to never spawn.
    public static let daemonEnv = "MALACHI_DAEMON"
    /// The daemon's keyring selection and the helper it runs
    /// (`secretservice|helper|none`; backend/internal/auth/helper).
    public static let keyringEnv = "MALACHI_KEYRING"
    public static let keyringHelperEnv = "MALACHI_KEYRING_HELPER"
    public static let startTimeout: Duration = .seconds(15)
    /// The daemon gives its syncers 10 s to log out.
    public static let stopTimeout: Duration = .seconds(15)
    public static let pollInterval: Duration = .milliseconds(100)
    public static let maxBackoff: Duration = .seconds(60)

    private let launch: Launch?
    private let socket: String
    private var process: Process?
    private var exited = false
    private var exitDescription = ""
    private var stopping = false
    private var failures = 0
    private var nextTry: ContinuousClock.Instant?
    /// How many times a daemon was started (for tests).
    public private(set) var spawns = 0

    /// - Parameters:
    ///   - launch: how to start a daemon, or nil to only ever adopt one.
    ///   - socket: where a daemon is expected to answer.
    public init(launch: Launch?, socket: String) {
        self.launch = launch
        self.socket = socket
    }

    /// Finds the daemon binary: `MALACHI_DAEMON` (a path; `none` or empty
    /// disables spawning and returns nil), else `malachid` beside the
    /// executable (Contents/MacOS in the bundle, .build/… under `swift run`),
    /// else on PATH. Throws `noDaemon` when nothing is found.
    public static func locate(
        besides executable: URL? = Bundle.main.executableURL,
        environment env: [String: String] = ProcessInfo.processInfo.environment
    ) throws -> URL? {
        if let v = env[daemonEnv] {
            return (v.isEmpty || v == "none") ? nil : URL(fileURLWithPath: v)
        }
        var candidates: [URL] = []
        if let dir = executable?.deletingLastPathComponent() {
            candidates.append(dir.appendingPathComponent("malachid"))
        }
        for dir in (env["PATH"] ?? "").split(separator: ":") where !dir.isEmpty {
            candidates.append(URL(fileURLWithPath: String(dir)).appendingPathComponent("malachid"))
        }
        for c in candidates where isExecutableFile(c) {
            return c
        }
        throw SupervisorError.noDaemon
    }

    private static func isExecutableFile(_ url: URL) -> Bool {
        guard let values = try? url.resourceValues(forKeys: [.isRegularFileKey]),
              values.isRegularFile == true else { return false }
        return FileManager.default.isExecutableFile(atPath: url.path)
    }

    /// Makes sure a daemon answers on the socket: adopts a running one, or
    /// starts ours and waits for its socket. Returns when the socket answers.
    public func ensure() async throws {
        if UnixSocketProbe.answers(socket) {
            return
        }
        guard let launch else {
            throw SupervisorError.noDaemon
        }
        if stopping {
            throw SupervisorError.stopping
        }
        if process != nil {
            if exited {
                noteExit()
            } else {
                // Started earlier and still coming up: it may just be slow.
                try await awaitSocket()
                return
            }
        }
        if let t = nextTry, t > .now {
            throw SupervisorError.backoff(failures: failures, retryIn: ContinuousClock.now.duration(to: t))
        }
        try spawn(launch)
        try await awaitSocket()
    }

    /// The daemon's environment: the app's, plus the keyring. There is no
    /// Secret Service on macOS, so the daemon gets the bundled
    /// `malachi-keychain` as its keyring helper (`MALACHI_KEYRING=helper`,
    /// `MALACHI_KEYRING_HELPER=<path>`); without one it runs with
    /// `MALACHI_KEYRING=none`, where adding an account with a password
    /// fails with keyringError. A `MALACHI_KEYRING` already in the
    /// environment wins, so a developer can still point the daemon
    /// elsewhere.
    public static func environment(base: [String: String], launch: Launch) -> [String: String] {
        var env = base
        guard env[keyringEnv] == nil else {
            return env
        }
        if let helper = launch.keychainHelper, isExecutableFile(helper) {
            env[keyringEnv] = "helper"
            env[keyringHelperEnv] = helper.path
        } else {
            env[keyringEnv] = "none"
        }
        return env
    }

    private func spawn(_ l: Launch) throws {
        let p = Process()
        p.executableURL = l.executable
        p.arguments = ["--socket", l.socket, "--config", l.config, "--store", l.store]
        p.environment = Self.environment(base: ProcessInfo.processInfo.environment, launch: l)
        // The daemon's logs go where the app's do. No process group of its
        // own: a Ctrl+C in the terminal reaches both, and the daemon shuts
        // down cleanly on SIGINT.
        p.standardOutput = FileHandle.standardError
        p.standardError = FileHandle.standardError
        p.terminationHandler = { [weak self] proc in
            let what = proc.terminationReason == .uncaughtSignal
                ? "died of signal \(proc.terminationStatus)"
                : "exited with status \(proc.terminationStatus)"
            Task { await self?.processExited(what) }
        }
        do {
            try p.run()
        } catch {
            failures += 1
            nextTry = .now + backoff()
            throw error
        }
        process = p
        exited = false
        spawns += 1
    }

    private func awaitSocket() async throws {
        let deadline = ContinuousClock.now + Self.startTimeout
        while true {
            if UnixSocketProbe.answers(socket) {
                failures = 0
                nextTry = nil
                return
            }
            if exited {
                let what = exitDescription
                noteExit()
                throw SupervisorError.exitedEarly(what)
            }
            if ContinuousClock.now >= deadline {
                throw SupervisorError.startTimeout(socket: socket)
            }
            try await Task.sleep(for: Self.pollInterval)
        }
    }

    /// Stops the daemon this supervisor started: SIGTERM, up to `stopTimeout`,
    /// then SIGKILL. Somebody else's daemon is left alone.
    public func stop() async {
        stopping = true
        guard let p = process, !exited, p.isRunning else { return }
        p.terminate()
        let deadline = ContinuousClock.now + Self.stopTimeout
        while !exited && ContinuousClock.now < deadline {
            try? await Task.sleep(for: Self.pollInterval)
        }
        if !exited {
            kill(p.processIdentifier, SIGKILL)
            p.waitUntilExit()
        }
    }

    private func processExited(_ what: String) {
        exited = true
        exitDescription = what
    }

    private func noteExit() {
        process = nil
        failures += 1
        nextTry = .now + backoff()
    }

    /// A single exit (a crash after hours of running) is retried at once;
    /// then 1 s doubling per further consecutive exit, capped.
    private func backoff() -> Duration {
        if failures <= 1 {
            return .zero
        }
        var d: Duration = .seconds(1)
        var i = 2
        while i < failures && d < Self.maxBackoff {
            d = d * 2
            i += 1
        }
        return min(d, Self.maxBackoff)
    }
}
