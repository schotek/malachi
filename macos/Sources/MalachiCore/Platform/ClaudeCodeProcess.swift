// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Darwin
import Foundation
import os

/// One conversation of the assistant panel: a long-lived `claude -p` with
/// stream-json on both sides (ui/internal/assistant `Args`). Each
/// `send` writes one turn to its stdin (`Assistant.userMessage` and a
/// newline); stdin stays open while the conversation lives. Its stdout is
/// read continuously, cut into lines by the transport's `LineFramer`,
/// every line parsed with `Assistant.parseEvents` off the main actor, and
/// the events are delivered on the main actor in the order of the lines
/// (`onEvents`, one batch per chunk read). A line that is not JSON is
/// logged and skipped. Its stderr is kept, at most `stderrLimit` bytes,
/// for the reason of an early exit (its first line, `reasonLimit` bytes).
///
/// `terminate()` closes stdin and sends SIGTERM, and SIGKILL after
/// `killGrace` when the process is still there. Its end is reported once,
/// after every event of its stdout (`onExit`), whether it exited by itself,
/// died or was terminated. The environment and the working directory are
/// the caller's (`Assistant.childEnv`, the private directory). Writing to
/// a process that has gone never raises SIGPIPE: the pipe is set to report
/// the error instead, and the write is dropped (the exit reports the rest).
@MainActor
public final class ClaudeCodeProcess {
    /// How the process ended: the exit status, or the negated signal
    /// number (-15 for SIGTERM); and the first line of its stderr.
    public struct Exit: Sendable, Equatable {
        public var status: Int32
        public var reason: String

        public init(status: Int32, reason: String) {
            self.status = status
            self.reason = reason
        }

        /// The reason for the transcript: stderr's first line, else the
        /// status in words (technical, English, like other error details).
        public var description: String {
            if !reason.isEmpty {
                return reason
            }
            return status < 0 ? "claude died of signal \(-status)" : "claude exited with status \(status)"
        }
    }

    public enum StartError: Error, CustomStringConvertible {
        case launch(String)
        case alreadyStarted

        public var description: String {
            switch self {
            case .launch(let why): return "claude could not be started: \(why)"
            case .alreadyStarted: return "claude was started already"
            }
        }
    }

    /// The most of stderr that is kept.
    public nonisolated static let stderrLimit = 64 << 10
    /// The reason of an early exit: stderr's first line, cut here.
    public nonisolated static let reasonLimit = 200
    /// SIGTERM to SIGKILL.
    public nonisolated static let defaultKillGrace: Duration = .seconds(2)
    /// How long stdout and stderr are read after the exit (a child of
    /// Claude Code that inherited them would keep them open).
    nonisolated static let eofGrace: Duration = .milliseconds(500)
    /// The longest stdout line; a longer one is dropped (a tool result is
    /// capped far below this by the bridge).
    nonisolated static let maxLine = 16 << 20

    /// The events of each chunk of stdout, in order.
    public var onEvents: (@MainActor ([Assistant.Event]) -> Void)?
    /// The end of the process, once, after the last events.
    public var onExit: (@MainActor (Exit) -> Void)?

    /// Started and not yet reported as ended.
    public private(set) var running = false
    /// How it ended, once it did.
    public private(set) var ended: Exit?

    private let executable: String
    private let arguments: [String]
    private let environment: [String: String]
    private let directory: URL
    private let killGrace: Duration
    private var process: Process?
    private var input: StdinWriter?
    private var state: ChildState?
    private var consumer: Task<Void, Never>?
    private var terminating = false
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "assistant")

    public init(
        executable: String, arguments: [String], environment: [String: String], directory: URL,
        killGrace: Duration = ClaudeCodeProcess.defaultKillGrace
    ) {
        self.executable = executable
        self.arguments = arguments
        self.environment = environment
        self.directory = directory
        self.killGrace = killGrace
    }

    /// Starts the process; throws when it cannot be started (the reason is
    /// technical).
    public func start() throws {
        guard process == nil else { throw StartError.alreadyStarted }
        let p = Process()
        p.executableURL = URL(fileURLWithPath: executable)
        p.arguments = arguments
        p.environment = environment
        p.currentDirectoryURL = directory
        let stdin = Pipe()
        let stdout = Pipe()
        let stderr = Pipe()
        p.standardInput = stdin
        p.standardOutput = stdout
        p.standardError = stderr
        let state = ChildState()
        p.terminationHandler = { proc in
            state.exited(proc.terminationReason == .uncaughtSignal ? -proc.terminationStatus : proc.terminationStatus)
        }
        do {
            try p.run()
        } catch {
            throw StartError.launch(error.localizedDescription)
        }
        process = p
        self.state = state
        running = true
        // A write to a pipe whose reader is gone reports EPIPE instead of
        // raising SIGPIPE, which would end the application.
        _ = fcntl(stdin.fileHandleForWriting.fileDescriptor, F_SETNOSIGPIPE, 1)
        input = StdinWriter(stdin.fileHandleForWriting)

        let (stream, continuation) = AsyncStream<Output>.makeStream()
        let outFD = stdout.fileHandleForReading.fileDescriptor
        let errFD = stderr.fileHandleForReading.fileDescriptor
        let log = log
        Task.detached {
            async let pumped: Void = Self.pump(outFD, state, continuation, log)
            async let errors: Data = Self.drain(errFD, state)
            let status = await state.waitForExit()
            Task.detached {
                try? await Task.sleep(for: Self.eofGrace)
                state.stopDrains()
            }
            await pumped
            let err = await errors
            // The pipes own the descriptors the readers used.
            withExtendedLifetime((stdout, stderr)) {}
            continuation.yield(.exit(status, err))
            continuation.finish()
        }
        consumer = Task { @MainActor [weak self] in
            for await output in stream {
                guard let self else { continue }
                switch output {
                case .events(let events):
                    self.onEvents?(events)
                case .exit(let status, let err):
                    self.finished(status, err)
                }
            }
        }
    }

    /// Writes one turn; false when the process is not running (or being
    /// terminated). The write happens off the main actor, in order.
    @discardableResult
    public func send(_ line: Data) -> Bool {
        guard running, !terminating, let input else { return false }
        var data = line
        data.append(0x0A)
        input.write(data)
        return true
    }

    /// Ends the conversation now: stdin closed, SIGTERM, SIGKILL after the
    /// grace. The end is still reported through `onExit`.
    public func terminate() {
        guard running, !terminating, let process, let state else { return }
        terminating = true
        input?.close()
        let pid = process.processIdentifier
        state.signal(SIGTERM, to: pid)
        let grace = killGrace
        Task.detached {
            try? await Task.sleep(for: grace)
            state.signal(SIGKILL, to: pid)
        }
    }

    private func finished(_ status: Int32, _ stderr: Data) {
        guard running else { return }
        running = false
        input?.close()
        let e = Exit(status: status, reason: MCPRegistrationController.firstLine(stderr, limit: Self.reasonLimit))
        ended = e
        onExit?(e)
    }

    // MARK: Reading

    fileprivate enum Output: Sendable {
        case events([Assistant.Event])
        case exit(Int32, Data)
    }

    /// Reads stdout to EOF (or until the state says stop), cutting lines
    /// and parsing them, and yields each chunk's events.
    private nonisolated static func pump(
        _ fd: Int32, _ state: ChildState, _ out: AsyncStream<Output>.Continuation, _ log: Logger
    ) async {
        await withCheckedContinuation { (done: CheckedContinuation<Void, Never>) in
            DispatchQueue.global(qos: .userInitiated).async {
                var framer = LineFramer(maxLine: maxLine)
                readLoop(fd, state) { chunk in
                    let lines: [Data]
                    do {
                        lines = try framer.append(chunk)
                    } catch {
                        log.warning("claude: a stdout line over \(maxLine, privacy: .public) bytes was dropped")
                        return
                    }
                    var events: [Assistant.Event] = []
                    for line in lines {
                        do {
                            events += try Assistant.parseEvents(line)
                        } catch {
                            log.warning("claude: \(String(describing: error), privacy: .public)")
                        }
                    }
                    if !events.isEmpty {
                        out.yield(.events(events))
                    }
                }
                done.resume()
            }
        }
    }

    /// Reads stderr to EOF (or until the state says stop), keeping the
    /// first `stderrLimit` bytes.
    private nonisolated static func drain(_ fd: Int32, _ state: ChildState) async -> Data {
        await withCheckedContinuation { (done: CheckedContinuation<Data, Never>) in
            DispatchQueue.global(qos: .utility).async {
                var kept = Data()
                readLoop(fd, state) { chunk in
                    let room = stderrLimit - kept.count
                    if room > 0 {
                        kept.append(chunk.prefix(room))
                    }
                }
                done.resume(returning: kept)
            }
        }
    }

    /// The blocking read loop both readers share: `poll` with a short
    /// timeout, so a stop request is noticed without EOF.
    private nonisolated static func readLoop(_ fd: Int32, _ state: ChildState, _ chunk: (Data) -> Void) {
        var buf = [UInt8](repeating: 0, count: 64 << 10)
        var pfd = pollfd(fd: fd, events: Int16(POLLIN), revents: 0)
        while true {
            pfd.revents = 0
            let ready = poll(&pfd, 1, 50)
            if ready < 0 {
                if errno == EINTR { continue }
                return
            }
            if ready == 0 {
                if state.drainsStopped { return }
                continue
            }
            let n = Darwin.read(fd, &buf, buf.count)
            if n < 0 {
                if errno == EINTR || errno == EAGAIN { continue }
                return
            }
            if n == 0 {
                return // EOF
            }
            chunk(Data(buf[0..<n]))
        }
    }
}

/// The write end of the child's stdin: every write and the close run on
/// one serial queue, so a close never overtakes a write and a write never
/// touches a closed descriptor.
private final class StdinWriter: @unchecked Sendable {
    private let queue = DispatchQueue(label: "io.github.schotek.Malachi.claude-stdin")
    private let handle: FileHandle
    private var closed = false

    init(_ handle: FileHandle) {
        self.handle = handle
    }

    func write(_ data: Data) {
        queue.async { [self] in
            guard !closed else { return }
            let fd = handle.fileDescriptor
            data.withUnsafeBytes { raw in
                guard var p = raw.baseAddress else { return }
                var left = raw.count
                while left > 0 {
                    let n = Darwin.write(fd, p, left)
                    if n < 0 {
                        if errno == EINTR || errno == EAGAIN { continue }
                        return // EPIPE: the process is gone; its exit says why
                    }
                    left -= n
                    p += n
                }
            }
        }
    }

    func close() {
        queue.async { [self] in
            guard !closed else { return }
            closed = true
            try? handle.close()
        }
    }
}

/// What the termination handler, the readers and the signals share: the
/// exit status and the stop flag, under a lock.
private final class ChildState: @unchecked Sendable {
    private let lock = NSLock()
    private var status: Int32?
    private var stopped = false
    private var waiters: [CheckedContinuation<Int32, Never>] = []

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

    /// Sends `sig` unless the process has exited (a reaped pid may be
    /// somebody else's by now).
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
