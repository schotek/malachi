// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// One MCP client the bridge knows about (`malachi-mcp status --json`,
/// docs/mcp.md): Claude Desktop or Claude Code, whether it is installed on
/// this computer, whether Malachi Mail is in its MCP configuration, that
/// configuration file, and the command registered there when it is not
/// this bridge. Unknown keys are ignored, so a newer bridge still decodes.
public struct MCPClient: Codable, Equatable, Sendable {
    public var id: String
    public var name: String
    public var present: Bool
    public var registered: Bool
    public var path: String?
    public var other: String?

    public init(id: String, name: String, present: Bool, registered: Bool, path: String? = nil, other: String? = nil) {
        self.id = id
        self.name = name
        self.present = present
        self.registered = registered
        self.path = path
        self.other = other
    }
}

/// What `malachi-mcp status`, `install` and `uninstall` print with
/// `--json`: the bridge's own absolute path and the clients.
public struct MCPStatus: Codable, Equatable, Sendable {
    public var command: String
    @NullAsEmpty public var clients: [MCPClient]

    public init(command: String, clients: [MCPClient]) {
        self.command = command
        self.clients = clients
    }

    /// Registered as the settings see it: with at least one client.
    public var isRegistered: Bool {
        clients.contains { $0.registered }
    }
}

/// The MCP group of the AI page of the settings (preferences.blp
/// `ai_page`, ui/internal/window/preferences.go `bindMCP`) without the
/// widgets: one switch, "Register with Claude", that adds the bundled
/// `malachi-mcp` bridge to the MCP configuration of Claude Desktop and
/// Claude Code, or takes it out, through the bridge's own setup
/// subcommands (`malachi-mcp status | install | uninstall --json`;
/// docs/mcp.md). The application never touches those files itself.
///
/// The switch is on when at least one client reports the bridge as
/// registered. A state not known yet is never shown as "off" for long: the
/// page hands over the application's last status (`adopt`), which is
/// shown at once, and so is every newer one the application learns while
/// no call of the page runs. The row is insensitive while the state is not
/// known and while an install or uninstall runs; a status check of a known
/// state leaves it sensitive. A failed install or uninstall shows a toast
/// (the bridge's one-line reason, or that no Claude app is installed) and
/// the switch goes back to the last state the bridge confirmed; a failed
/// status check has no sentence of its own, as in GTK: it is logged, the
/// last known state stays, and the check is repeated after
/// `statusRetryDelays` (a Claude app may be rewriting its file just then;
/// GTK asks again only when the page comes up, macOS leads here).
/// Without a bridge beside the application (`Paths.mcpBridge`
/// nil) the row stays insensitive and a toast says so, once. A status
/// asked while a call runs is skipped (that call's answer is the newer
/// status); the reply of a call a newer one overtook, and every reply after
/// `close()`, is dropped.
@MainActor
public final class MCPRegistrationController {
    /// The bridge's setup subcommands.
    public enum Command: String, Sendable {
        case status, install, uninstall
    }

    /// How long one bridge call may take.
    public nonisolated static let defaultTimeout: Duration = .seconds(15)
    /// The pauses before the automatic repeats of a failed status check.
    public nonisolated static let defaultStatusRetryDelays: [Duration] = [.seconds(1), .seconds(2), .seconds(4)]
    /// The most of the bridge's stderr that is kept: its first line, cut at
    /// this many bytes.
    public nonisolated static let reasonLimit = 200
    /// How `install` says that neither Claude app is installed (the start
    /// of its one-line reason on stderr, compared case-insensitively).
    nonisolated static let noClaudeAppPrefix = "no claude app found"

    /// The last status the bridge reported; nil until the first answered.
    public private(set) var status: MCPStatus?
    /// What the switch shows: the last state the bridge confirmed.
    public private(set) var isRegistered = false
    /// The row's sensitivity: a bridge is there, its status is known and no
    /// install or uninstall is in flight.
    public private(set) var isEnabled = false
    /// The window closed: late replies are dropped.
    public private(set) var closed = false

    /// Called with the state to show after every answered call: the
    /// bridge's status, or the previous state again when the call failed
    /// (so the switch reverts).
    public var onRegistered: (@MainActor (Bool) -> Void)?
    /// Called on every change of the row's sensitivity.
    public var onEnabled: (@MainActor (Bool) -> Void)?
    /// Called with the text of a toast.
    public var onToast: (@MainActor (String) -> Void)?

    private let bridge: String?
    private let runner: BridgeRunner
    private let timeout: Duration
    private let statusRetryDelays: [Duration]
    /// The automatic repeats of a failed status check used so far; back to
    /// none after any answered call.
    private var statusRetries = 0
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "mcp")
    /// Bumped by every call; the reply of an older one is dropped.
    private var op = 0
    private var inFlight = false
    private var reportedMissing = false

    /// - Parameters:
    ///   - bridge: the path of `malachi-mcp` (`Paths.mcpBridge`), or nil
    ///     when there is none beside the application.
    ///   - runner: how the bridge is run; the default runs the real thing.
    ///   - timeout: how long one call may take.
    ///   - statusRetryDelays: the pauses before repeating a failed status
    ///     check, one repeat each.
    public init(
        bridge: String?, runner: BridgeRunner = BridgeRunner(), timeout: Duration = MCPRegistrationController.defaultTimeout,
        statusRetryDelays: [Duration] = MCPRegistrationController.defaultStatusRetryDelays
    ) {
        self.bridge = bridge
        self.runner = runner
        self.timeout = timeout
        self.statusRetryDelays = statusRetryDelays
    }

    /// Drops every reply still in flight; nothing is emitted afterwards.
    public func close() {
        closed = true
    }

    // MARK: Loading

    /// Asks `status`; the page calls it whenever it comes up. Skipped while
    /// a call runs (its answer is the status). Without a bridge the row
    /// stays insensitive and the toast is shown once.
    public func load() {
        guard !closed else { return }
        guard let bridge else {
            reportMissing()
            return
        }
        guard !inFlight else { return }
        run(.status, bridge)
    }

    /// Takes a status reported elsewhere (the application's last, which
    /// `AssistantController` keeps): shown at once and the row sensitive,
    /// so the switch never shows "off" only because this page has not
    /// asked yet, and it follows what the application learns later. Nothing
    /// while a call runs (its answer is newer), without a bridge, or when it
    /// is the status already shown.
    public func adopt(_ s: MCPStatus) {
        guard !closed, bridge != nil, !inFlight, s != status else { return }
        status = s
        isRegistered = s.isRegistered
        statusRetries = 0
        setEnabled(true)
        onRegistered?(isRegistered)
    }

    // MARK: Changes

    /// Runs `install` or `uninstall` and shows what the bridge reports
    /// afterwards; on failure the switch goes back and a toast says why.
    public func set(registered want: Bool) {
        guard !closed else { return }
        guard let bridge else {
            reportMissing()
            onRegistered?(isRegistered)
            return
        }
        run(want ? .install : .uninstall, bridge)
    }

    /// `set(registered:)` that returns once the bridge answered, after the
    /// callbacks: the status the bridge reported, or nil when the call
    /// failed (its toast was shown), there is no bridge, a newer call
    /// overtook it or the controller closed. `ClaudeDesktopController`
    /// awaits it between quitting Claude Desktop and starting it again.
    public func change(registered want: Bool) async -> MCPStatus? {
        guard !closed else { return nil }
        guard let bridge else {
            reportMissing()
            onRegistered?(isRegistered)
            return nil
        }
        return await withCheckedContinuation { (cont: CheckedContinuation<MCPStatus?, Never>) in
            run(want ? .install : .uninstall, bridge) { cont.resume(returning: $0) }
        }
    }

    // MARK: Internals

    /// Runs `command`; `done` gets its status, or nil when it yielded none
    /// or its reply was dropped, exactly once.
    private func run(_ command: Command, _ bridge: String, done: (@MainActor (MCPStatus?) -> Void)? = nil) {
        op += 1
        let my = op
        inFlight = true
        // A status check of a known state keeps the row sensitive: flipping
        // the switch meanwhile overtakes it.
        if command != .status || status == nil {
            setEnabled(false)
        }
        let runner = runner
        let timeout = timeout
        Task { [weak self] in
            let outcome = await Self.invoke(runner, bridge, command, timeout)
            guard let self, !self.closed, my == self.op else {
                done?(nil)
                return
            }
            self.inFlight = false
            var answer: MCPStatus?
            switch outcome {
            case .success(let s):
                self.status = s
                self.isRegistered = s.isRegistered
                self.statusRetries = 0
                answer = s
            case .failure(let f):
                self.log.warning("malachi-mcp \(command.rawValue, privacy: .public): \(f.reason, privacy: .private)")
                guard command != .status else {
                    // No sentence of its own (preferences.go `bindMCP`): the
                    // last known state stays (the row sensitive when there
                    // is one), and the check is repeated shortly.
                    if self.status != nil {
                        self.setEnabled(true)
                    }
                    self.retryStatus(after: my)
                    done?(nil)
                    return
                }
                self.onToast?(f.toast(registering: command == .install))
            }
            self.setEnabled(true)
            self.onRegistered?(self.isRegistered)
            done?(answer)
        }
    }

    /// Repeats a failed status check after the next of
    /// `statusRetryDelays`, unless a newer call came meanwhile (its answer
    /// is newer) or the repeats are used up (the page's next appearance
    /// asks again).
    private func retryStatus(after my: Int) {
        guard statusRetries < statusRetryDelays.count else { return }
        let delay = statusRetryDelays[statusRetries]
        statusRetries += 1
        Task { [weak self] in
            try? await Task.sleep(for: delay)
            guard let self, !self.closed, !self.inFlight, self.op == my else { return }
            self.load()
        }
    }

    /// No bridge beside the application: the row stays insensitive and
    /// the toast is shown once.
    private func reportMissing() {
        guard !reportedMissing else { return }
        reportedMissing = true
        onEnabled?(false)
        onToast?(L10n.T("The MCP bridge (malachi-mcp) was not found"))
    }

    private func setEnabled(_ on: Bool) {
        guard on != isEnabled else { return }
        isEnabled = on
        onEnabled?(on)
    }

    /// Runs one subcommand and reads its status, off the main actor.
    private nonisolated static func invoke(_ runner: BridgeRunner, _ bridge: String, _ command: Command, _ timeout: Duration) async -> Result<MCPStatus, MCPCallFailure> {
        let output: (stdout: Data, stderr: Data, status: Int32)
        do {
            output = try await runner.run(bridge, [command.rawValue, "--json"], timeout: timeout)
        } catch {
            return .failure(.run(String(describing: error)))
        }
        let reason = firstLine(output.stderr)
        guard output.status == 0 else {
            if reason.lowercased().hasPrefix(noClaudeAppPrefix) {
                return .failure(.noClaudeApp)
            }
            return .failure(.failed(reason.isEmpty ? exitDescription(output.status) : reason))
        }
        do {
            return .success(try JSONDecoder().decode(MCPStatus.self, from: output.stdout))
        } catch {
            return .failure(.failed("unexpected output from malachi-mcp \(command.rawValue)"))
        }
    }

    /// The first line of the bridge's stderr, cut at `limit` bytes on a
    /// character boundary, trimmed.
    nonisolated static func firstLine(_ data: Data, limit: Int = reasonLimit) -> String {
        var line = data.prefix { $0 != UInt8(ascii: "\n") }
        if line.count > limit {
            line = line.prefix(limit)
            // Never in the middle of a multi-byte character.
            while let last = line.last, last & 0xC0 == 0x80 {
                line = line.dropLast()
            }
            if let last = line.last, last >= 0xC0 {
                line = line.dropLast()
            }
        }
        return String(decoding: line, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
    }

    nonisolated static func exitDescription(_ status: Int32) -> String {
        status < 0 ? "malachi-mcp died of signal \(-status)" : "malachi-mcp exited with status \(status)"
    }
}

/// Why a bridge call yielded no status.
enum MCPCallFailure: Error, Equatable {
    /// `install` found neither Claude app.
    case noClaudeApp
    /// A non-zero exit with the bridge's reason (or the exit status when it
    /// gave none), a crash, or output that is not a status.
    case failed(String)
    /// The bridge could not be started, or did not finish in time.
    case run(String)

    /// The technical detail for the log and the toast.
    var reason: String {
        switch self {
        case .noClaudeApp:
            return "no Claude app found"
        case .failed(let r), .run(let r):
            return r
        }
    }

    /// The toast for a failed install (`registering`) or uninstall. The
    /// msgids are the GTK page's.
    func toast(registering: Bool) -> String {
        switch self {
        case .noClaudeApp:
            return L10n.T("No Claude app was found on this computer")
        case .failed, .run:
            return registering
                ? L10n.T("The MCP bridge could not be registered: %s", reason)
                : L10n.T("The MCP bridge could not be unregistered: %s", reason)
        }
    }
}
