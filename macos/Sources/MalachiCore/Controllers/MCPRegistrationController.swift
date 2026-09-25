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
/// registered. The row is insensitive before the first status and while a
/// call runs; a failed install or uninstall shows a toast (the bridge's
/// one-line reason, or that no Claude app is installed) and the switch goes
/// back to the last state the bridge confirmed; a failed status check has
/// no sentence of its own, as in GTK: it is logged and the row simply
/// stays insensitive until the page comes up again and asks once more.
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
    /// call is in flight.
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
    public init(bridge: String?, runner: BridgeRunner = BridgeRunner(), timeout: Duration = MCPRegistrationController.defaultTimeout) {
        self.bridge = bridge
        self.runner = runner
        self.timeout = timeout
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

    // MARK: Internals

    private func run(_ command: Command, _ bridge: String) {
        op += 1
        let my = op
        inFlight = true
        setEnabled(false)
        let runner = runner
        let timeout = timeout
        Task { [weak self] in
            let outcome = await Self.invoke(runner, bridge, command, timeout)
            guard let self, !self.closed, my == self.op else { return }
            self.inFlight = false
            switch outcome {
            case .success(let s):
                self.status = s
                self.isRegistered = s.isRegistered
            case .failure(let f):
                self.log.warning("malachi-mcp \(command.rawValue, privacy: .public): \(f.reason, privacy: .private)")
                guard command != .status else {
                    // No sentence of its own (preferences.go `bindMCP`): the
                    // row simply stays insensitive; the next time the page
                    // comes up it asks again.
                    return
                }
                self.onToast?(f.toast(registering: command == .install))
            }
            self.setEnabled(true)
            self.onRegistered?(self.isRegistered)
        }
    }

    /// No bridge beside the application: the row stays insensitive and
    /// the toast is shown once.
    private func reportMissing() {
        guard !reportedMissing else { return }
        reportedMissing = true
        onEnabled?(false)
        onToast?(L10n.T("The MCP bridge (malachi-mcp) was not found next to the application"))
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
