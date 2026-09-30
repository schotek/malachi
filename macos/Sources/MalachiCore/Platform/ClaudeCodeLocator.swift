// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Darwin
import Foundation
import os

/// Finds the user's Claude Code for the assistant panel (the In App target
/// of ui/internal/assistant; GTK ui/internal/assistantpanel `Locator`) and
/// asks it two things: its version and whether it is signed in;
/// `startSignIn()` runs Claude Code's own sign-in. Malachi Mail never reads
/// a credential and never shows the account: `claude auth status --json` is
/// read for its `loggedIn` only, and `claude auth login` does the signing
/// in.
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
///
/// `startSignIn()` runs Claude Code's own sign-in for the located
/// executable (`claude auth login`: `Assistant.signInArgs` with
/// `Assistant.signInEnv`, in the panel's private directory). Claude Code
/// opens the browser, the user signs in to Claude there, and Claude Code
/// stores the sign-in itself. Malachi Mail only waits for the process to
/// end: it sees no credential, and what the process prints is neither
/// shown nor logged (its stdout is not even read: the address it names
/// belongs to the sign-in; only stderr's first line is the reason of a
/// failure, and the log gets the kind of the outcome alone). The answers
/// kept for `signedIn()` are forgotten when it ends, so the next question
/// asks afresh. One sign-in at a time, for the panel and the settings
/// alike: a new one takes the place of the one under way, which ends as
/// `.cancelled`, as does one that `cancelSignIn` ends; a cancelled or
/// timed-out process gets SIGTERM, and SIGKILL after `signInGrace`.
/// `onSignInChange` reports when one starts or ends (`signingIn`).
@MainActor
public final class ClaudeCodeLocator {
    public nonisolated static let defaultTimeout: Duration = .seconds(10)
    /// How long the browser is waited for in a sign-in.
    public nonisolated static let defaultSignInTimeout: Duration = .seconds(600)
    /// SIGTERM to SIGKILL for a sign-in that is cancelled or out of time.
    nonisolated static let signInGrace: Duration = .seconds(2)
    /// The most of the version line that is kept.
    nonisolated static let versionLimit = 100

    /// How Claude Code's sign-in ended (GTK `SignInResult`).
    public enum SignInResult: Sendable, Equatable {
        /// SignInDone: `claude auth login` ended with status 0.
        case done
        /// SignInFailed: it ended badly, or could not run; the reason is
        /// technical (stderr's first line, else the exit status in words).
        case failed(reason: String)
        /// SignInTimedOut: the browser brought no answer within the timeout.
        case timedOut
        /// SignInCancelled: it was cancelled, or another sign-in took its
        /// place.
        case cancelled
        /// SignInNotFound: there is no Claude Code to sign in.
        case notFound
    }

    /// One sign-in (`startSignIn`): how it ended, and what `cancelSignIn`
    /// names to end this one and no other.
    public struct SignInRun: Sendable {
        /// Its number; never used twice.
        public let id: Int
        fileprivate let task: Task<SignInResult, Never>

        /// How it ended, once it did.
        public var result: SignInResult {
            get async { await task.value }
        }
    }

    /// Removes a handler installed by `onSignInChange`; dropping the token
    /// does not.
    @MainActor
    public final class SignInToken {
        private weak var owner: ClaudeCodeLocator?
        private let id: Int

        fileprivate init(owner: ClaudeCodeLocator, id: Int) {
            self.owner = owner
            self.id = id
        }

        public func cancel() {
            owner?.watchers[id] = nil
        }
    }

    public let settings: Settings
    private let environment: [String: String]
    private let runner: BridgeRunner
    private let timeout: Duration
    private let directory: URL?
    private let usable: @Sendable (String) -> Bool
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "assistant")
    /// How long a sign-in waits for the browser (tests shorten it).
    public var signInTimeout: Duration

    private var versions: [String: Task<String?, Never>] = [:]
    private var signIns: [String: Task<Bool?, Never>] = [:]

    /// The sign-in under way: its number, its process and where its end is
    /// reported.
    private struct ActiveSignIn {
        let id: Int
        let child: SignInChild
        let gate: AsyncStream<SignInResult>.Continuation
    }

    private var current: ActiveSignIn?
    private var nextSignIn = 0
    /// Who hears when a sign-in starts or ends.
    fileprivate var watchers: [Int: @MainActor () -> Void] = [:]
    private var nextWatcher = 0

    /// - Parameters:
    ///   - settings: where `assistant-claude-path` is read.
    ///   - environment: the application's environment (HOME, PATH, and
    ///     what `Assistant.childEnv` keeps for the child).
    ///   - runner, timeout: how `claude` is run.
    ///   - directory: the working directory of the runs (the panel's
    ///     private directory); nil leaves the application's.
    ///   - usable: whether a candidate is the one (tests keep it to their
    ///     own directory).
    ///   - signInTimeout: how long a sign-in waits for the browser.
    public init(
        settings: Settings, environment: [String: String] = ProcessInfo.processInfo.environment,
        runner: BridgeRunner = BridgeRunner(), timeout: Duration = ClaudeCodeLocator.defaultTimeout, directory: URL? = nil,
        usable: @escaping @Sendable (String) -> Bool = ClaudeCodeLocator.isExecutableFile,
        signInTimeout: Duration = ClaudeCodeLocator.defaultSignInTimeout
    ) {
        self.settings = settings
        self.environment = environment
        self.runner = runner
        self.timeout = timeout
        self.directory = directory
        self.usable = usable
        self.signInTimeout = signInTimeout
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

    // MARK: Signing in

    /// Whether a sign-in is under way.
    public var signingIn: Bool {
        current != nil
    }

    /// Starts Claude Code's own sign-in for the located executable (see the
    /// type's comment) and returns at once; the run's `result` says how it
    /// ended. A sign-in under way ends first, as `.cancelled`; without a
    /// Claude Code the run is over already, as `.notFound`.
    @discardableResult
    public func startSignIn() -> SignInRun {
        cancelSignIn()
        let id = nextSignIn
        nextSignIn += 1
        guard let path = locate() else {
            return SignInRun(id: id, task: Task { SignInResult.notFound })
        }
        let env = Assistant.signInEnvironment(environment, claudePath: path)
        let directory = directory.flatMap { Self.ensureDirectory($0) ? $0 : nil }
        let timeout = signInTimeout
        let child = SignInChild(grace: Self.signInGrace)
        let (stream, gate) = AsyncStream<SignInResult>.makeStream()
        current = ActiveSignIn(id: id, child: child, gate: gate)
        signInChanged()
        let log = log
        Task.detached {
            let result = await Self.runSignIn(path, environment: env, directory: directory, timeout: timeout, child: child)
            if result != .done {
                // The kind of the outcome alone: stderr may name the account.
                let outcome = Self.outcomeName(result)
                log.warning("claude auth login: \(outcome, privacy: .public)")
            }
            await self.signInEnded(id, result)
        }
        // The first thing the gate gets: the process's end, or the
        // cancellation, which does not wait for the process to go.
        let task = Task<SignInResult, Never> {
            for await result in stream {
                return result
            }
            return .cancelled
        }
        return SignInRun(id: id, task: task)
    }

    /// Claude Code's own sign-in, awaited: `startSignIn()` and its result.
    public func signIn() async -> SignInResult {
        await startSignIn().result
    }

    /// Ends the sign-in `run` when it is the one under way; the cancel of
    /// a sign-in that is over, or that another took the place of, ends no
    /// other.
    public func cancelSignIn(_ run: SignInRun) {
        guard current?.id == run.id else { return }
        cancelSignIn()
    }

    /// Ends the sign-in under way, which is reported as `.cancelled`;
    /// nothing without one.
    public func cancelSignIn() {
        guard let run = current else { return }
        current = nil
        run.child.end(.cancelled)
        refresh()
        signInChanged()
        run.gate.yield(.cancelled)
        run.gate.finish()
    }

    /// Calls `f` whenever a sign-in starts or ends (`signingIn`), whoever
    /// started it, until the token is cancelled.
    public func onSignInChange(_ f: @escaping @MainActor () -> Void) -> SignInToken {
        let id = nextWatcher
        nextWatcher += 1
        watchers[id] = f
        return SignInToken(owner: self, id: id)
    }

    /// The process of sign-in `id` ended: reported unless the sign-in was
    /// cancelled meanwhile (and reported then).
    private func signInEnded(_ id: Int, _ result: SignInResult) {
        guard let run = current, run.id == id else { return }
        current = nil
        refresh()
        signInChanged()
        run.gate.yield(result)
        run.gate.finish()
    }

    private func signInChanged() {
        // By number: a handler may cancel its own token, or another's.
        for id in watchers.keys.sorted() {
            watchers[id]?()
        }
    }

    /// The outcome's kind for the log; never the reason.
    private nonisolated static func outcomeName(_ result: SignInResult) -> String {
        switch result {
        case .done: return "done"
        case .failed: return "failed"
        case .timedOut: return "timed out"
        case .cancelled: return "cancelled"
        case .notFound: return "not found"
        }
    }

    /// Runs `claude auth login` and waits for it: stdin closed, stdout not
    /// read, stderr kept for the reason of a failure (its first line, as
    /// `ClaudeCodeProcess.Exit` words it). `child` ends it from outside
    /// (a cancellation) and after `timeout`.
    private nonisolated static func runSignIn(
        _ path: String, environment: [String: String], directory: URL?, timeout: Duration, child: SignInChild
    ) async -> SignInResult {
        // Cancelled before it could start: nothing is run.
        guard child.how == .byItself else { return .cancelled }
        let process = Process()
        process.executableURL = URL(fileURLWithPath: path)
        process.arguments = Assistant.signInArgs
        process.environment = environment
        if let directory {
            process.currentDirectoryURL = directory
        }
        process.standardInput = FileHandle.nullDevice
        // The address it prints names the sign-in's session: never read.
        process.standardOutput = FileHandle.nullDevice
        let err = Pipe()
        process.standardError = err
        process.terminationHandler = { p in
            child.exited(p.terminationReason == .uncaughtSignal ? -p.terminationStatus : p.terminationStatus)
        }
        do {
            try process.run()
        } catch {
            return .failed(reason: ClaudeCodeProcess.StartError.launch(error.localizedDescription).description)
        }
        child.started(process.processIdentifier)
        let errFD = err.fileHandleForReading.fileDescriptor
        async let stderr = Self.drain(errFD, child)
        let timer = Task {
            try await Task.sleep(for: timeout)
            child.end(.timedOut)
        }
        let status = await child.waitForExit()
        timer.cancel()
        // A browser it started may hold its stderr open past the exit.
        Task {
            try? await Task.sleep(for: BridgeRunner.eofGrace)
            child.stopDrains()
        }
        let collected = await stderr
        // The pipe owns the descriptor the drain read.
        withExtendedLifetime((process, err)) {}
        switch child.how {
        case .cancelled: return .cancelled
        case .timedOut: return .timedOut
        case .byItself: break
        }
        guard status != 0 else { return .done }
        let reason = MCPRegistrationController.firstLine(collected, limit: ClaudeCodeProcess.reasonLimit)
        return .failed(reason: ClaudeCodeProcess.Exit(status: status, reason: reason).description)
    }

    /// Reads the sign-in's stderr to EOF (or until `child` says stop) on a
    /// global queue, keeping the first `ClaudeCodeProcess.stderrLimit`
    /// bytes.
    private nonisolated static func drain(_ fd: Int32, _ child: SignInChild) async -> Data {
        await withCheckedContinuation { (done: CheckedContinuation<Data, Never>) in
            DispatchQueue.global(qos: .utility).async {
                var kept = Data()
                var buf = [UInt8](repeating: 0, count: 16 << 10)
                var pfd = pollfd(fd: fd, events: Int16(POLLIN), revents: 0)
                while true {
                    pfd.revents = 0
                    let ready = poll(&pfd, 1, 50)
                    if ready < 0 {
                        if errno == EINTR { continue }
                        break
                    }
                    if ready == 0 {
                        if child.drainsStopped { break }
                        continue
                    }
                    let n = Darwin.read(fd, &buf, buf.count)
                    if n < 0 {
                        if errno == EINTR || errno == EAGAIN { continue }
                        break
                    }
                    if n == 0 {
                        break // EOF
                    }
                    let room = ClaudeCodeProcess.stderrLimit - kept.count
                    if room > 0 {
                        kept.append(contentsOf: buf[0..<min(n, room)])
                    }
                }
                done.resume(returning: kept)
            }
        }
    }

    // MARK: Running claude

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

/// What a sign-in's termination handler, its timeout, its cancellation and
/// the reader of its stderr share: the process id, the exit status, how it
/// was ended and the stop flag, under a lock.
private final class SignInChild: @unchecked Sendable {
    /// How the sign-in came to its end.
    enum End: Sendable {
        /// The process ended by itself (or still runs).
        case byItself
        /// It was cancelled, or another sign-in took its place.
        case cancelled
        /// The browser brought no answer in time.
        case timedOut
    }

    private let lock = NSLock()
    private let grace: Duration
    private var pid: pid_t?
    private var status: Int32?
    private var ended = End.byItself
    private var stopped = false
    private var waiters: [CheckedContinuation<Int32, Never>] = []

    /// - Parameter grace: SIGTERM to SIGKILL.
    init(grace: Duration) {
        self.grace = grace
    }

    /// How it was ended; `.byItself` while nothing ended it.
    var how: End {
        lock.withLock { ended }
    }

    /// The process runs: an end asked for before it did reaches it now.
    func started(_ p: pid_t) {
        let pending = lock.withLock { () -> Bool in
            pid = p
            return ended != .byItself && status == nil
        }
        if pending {
            terminate()
        }
    }

    /// Ends the sign-in as `how` (cancelled, out of time) unless it is over
    /// or was ended already: SIGTERM now, SIGKILL after the grace when the
    /// process is still there.
    func end(_ how: End) {
        let first = lock.withLock { () -> Bool in
            guard ended == .byItself, status == nil else { return false }
            ended = how
            return true
        }
        if first {
            terminate()
        }
    }

    private func terminate() {
        send(SIGTERM)
        let grace = grace
        Task.detached {
            try? await Task.sleep(for: grace)
            self.send(SIGKILL)
        }
    }

    /// Sends `sig` unless the process has not started or has exited (a
    /// reaped pid may be somebody else's by now).
    private func send(_ sig: Int32) {
        lock.withLock {
            guard status == nil, let pid = pid else { return }
            _ = kill(pid, sig)
        }
    }

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

    func stopDrains() {
        lock.withLock { stopped = true }
    }

    var drainsStopped: Bool {
        lock.withLock { stopped }
    }
}
