// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Darwin
import Foundation

/// Runs the bundled `malachi-mcp` for one of its setup subcommands
/// (`status`, `install`, `uninstall`, each with `--json`; docs/mcp.md) and
/// hands back what it printed: the JSON object on stdout, the one-line
/// reason on stderr, the exit status. Nothing here interprets the output;
/// `MCPRegistrationController` does. The bridge is a client of the daemon
/// like the app, so it runs with the app's environment (the socket default
/// and `MALACHI_SOCKET` agree).
///
/// `run` is a plain async function, so it executes off the main actor: the
/// process is a `Foundation.Process` with pipes, the exit comes through its
/// termination handler, and both pipes are drained on a global queue into
/// bounded buffers (`maxOutput` each; the rest is read and dropped, so the
/// child never blocks on a full pipe). A run that outlives `timeout` gets
/// SIGTERM, then SIGKILL after `killGrace`, and throws `.timeout`. A child
/// of the bridge that inherited the pipes (a test script's `sleep`; the
/// real bridge has none) would keep them open past the exit, so the drains
/// give up `eofGrace` after the exit at the latest.
public struct BridgeRunner: Sendable {
    public enum RunError: Error, Sendable, CustomStringConvertible {
        /// The executable could not be started (missing, not executable).
        case launch(String)
        /// The process did not exit within the timeout and was killed.
        case timeout(Duration)

        public var description: String {
            switch self {
            case .launch(let why):
                return "malachi-mcp could not be started: \(why)"
            case .timeout(let limit):
                return "malachi-mcp did not finish within \(limit.components.seconds) s"
            }
        }
    }

    /// The most that is kept of each of stdout and stderr.
    public static let maxOutput = 1 << 20
    /// SIGTERM to SIGKILL after a timeout.
    public static let killGrace: Duration = .seconds(2)
    /// How long the drains wait for EOF after the exit.
    public static let eofGrace: Duration = .milliseconds(500)

    public init() {}

    /// Runs `executable` with `args` and waits for it. The status is the
    /// exit status, or the negated signal number when the process died of
    /// a signal (-9 for SIGKILL).
    public func run(_ executable: String, _ args: [String], timeout: Duration) async throws -> (stdout: Data, stderr: Data, status: Int32) {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: executable)
        process.arguments = args
        process.environment = ProcessInfo.processInfo.environment
        process.standardInput = FileHandle.nullDevice
        let out = Pipe()
        let err = Pipe()
        process.standardOutput = out
        process.standardError = err
        let state = RunState()
        process.terminationHandler = { p in
            state.exited(p.terminationReason == .uncaughtSignal ? -p.terminationStatus : p.terminationStatus)
        }
        do {
            try process.run()
        } catch {
            throw RunError.launch(error.localizedDescription)
        }
        // `run` closed the parent's copies of the write ends, so the reads
        // end at EOF with the exit, or `eofGrace` after it.
        let outFD = out.fileHandleForReading.fileDescriptor
        let errFD = err.fileHandleForReading.fileDescriptor
        async let stdout = Self.drain(outFD, state)
        async let stderr = Self.drain(errFD, state)

        let pid = process.processIdentifier
        let killer = Task {
            try await Task.sleep(for: timeout)
            guard state.markTimedOut() else { return }
            state.signal(SIGTERM, to: pid)
            try await Task.sleep(for: Self.killGrace)
            state.signal(SIGKILL, to: pid)
        }
        let status = await state.waitForExit()
        killer.cancel()
        Task {
            try? await Task.sleep(for: Self.eofGrace)
            state.stopDrains()
        }
        let collectedOut = await stdout
        let collectedErr = await stderr
        // The pipes own the descriptors the drains read; keep them until
        // the drains are done.
        withExtendedLifetime((out, err)) {}
        if state.didTimeOut {
            throw RunError.timeout(timeout)
        }
        return (collectedOut, collectedErr, status)
    }

    // MARK: Internals

    /// Reads `fd` to EOF (or until the state says stop) on a global queue,
    /// keeping the first `maxOutput` bytes.
    private static func drain(_ fd: Int32, _ state: RunState) async -> Data {
        await withCheckedContinuation { continuation in
            DispatchQueue.global(qos: .userInitiated).async {
                continuation.resume(returning: readBounded(fd, state))
            }
        }
    }

    private static func readBounded(_ fd: Int32, _ state: RunState) -> Data {
        var out = Data()
        var buf = [UInt8](repeating: 0, count: 64 << 10)
        var pfd = pollfd(fd: fd, events: Int16(POLLIN), revents: 0)
        while true {
            pfd.revents = 0
            let ready = poll(&pfd, 1, 50)
            if ready < 0 {
                if errno == EINTR { continue }
                return out
            }
            if ready == 0 {
                if state.drainsStopped { return out }
                continue
            }
            let n = read(fd, &buf, buf.count)
            if n < 0 {
                if errno == EINTR { continue }
                return out
            }
            if n == 0 {
                return out // EOF
            }
            let room = maxOutput - out.count
            if room > 0 {
                out.append(contentsOf: buf[0..<min(n, room)])
            }
        }
    }
}

/// What one run shares between the termination handler, the killer task
/// and the drains: the exit status and the two flags, under a lock.
private final class RunState: @unchecked Sendable {
    private let lock = NSLock()
    private var status: Int32?
    private var timedOut = false
    private var stopped = false
    private var waiters: [CheckedContinuation<Int32, Never>] = []

    /// The termination handler's report.
    func exited(_ s: Int32) {
        lock.lock()
        status = s
        let resumable = waiters
        waiters = []
        lock.unlock()
        for c in resumable {
            c.resume(returning: s)
        }
    }

    /// The exit status, as soon as the process exited.
    func waitForExit() async -> Int32 {
        await withCheckedContinuation { continuation in
            lock.lock()
            if let s = status {
                lock.unlock()
                continuation.resume(returning: s)
                return
            }
            waiters.append(continuation)
            lock.unlock()
        }
    }

    /// Marks the timeout unless the process already exited.
    func markTimedOut() -> Bool {
        lock.withLock {
            guard status == nil else { return false }
            timedOut = true
            return true
        }
    }

    var didTimeOut: Bool {
        lock.withLock { timedOut }
    }

    /// Sends `sig` to `pid` unless the process already exited (a reaped
    /// pid may already be somebody else's).
    func signal(_ sig: Int32, to pid: pid_t) {
        lock.withLock {
            guard status == nil else { return }
            _ = kill(pid, sig)
        }
    }

    func stopDrains() {
        lock.withLock { stopped = true }
    }

    var drainsStopped: Bool {
        lock.withLock { stopped }
    }
}
