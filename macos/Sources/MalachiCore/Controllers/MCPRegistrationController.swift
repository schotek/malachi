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

    /// Registered with at least one client: what the Assistant as a whole
    /// needs (`Assistant.shown`).
    public var isRegistered: Bool {
        clients.contains { $0.registered }
    }

    /// The client ids of the bridge's report (`MCPClient.id`), which
    /// `--client` of install and uninstall takes (mcpsetup.ClientDesktop,
    /// ClientCode; `Integrations.clientID`).
    public static let clientDesktop = "claude-desktop"
    public static let clientCode = "claude-code"

    /// mcpsetup.Status.Client: the report's entry for client `id`, nil when
    /// the report has none.
    public func client(_ id: String) -> MCPClient? {
        clients.first { $0.id == id }
    }

    /// mcpsetup.Status.RegisteredIn: whether the bridge is registered with
    /// client `id`, what that app's own Register switch shows; false for a
    /// client the report does not list.
    public func registered(in id: String) -> Bool {
        client(id)?.registered ?? false
    }

    /// The status a test build goes by (`TestBuild`): a client where
    /// another copy of Malachi Mail registered its bridge (`other`, the
    /// stable app) counts as registered, since that bridge serves the same
    /// daemon and store.
    public func countingOtherCopies() -> MCPStatus {
        var s = self
        for i in s.clients.indices where !(s.clients[i].other ?? "").isEmpty {
            s.clients[i].registered = true
        }
        return s
    }
}

/// The MCP switch of a Claude app's sheet (ui/internal/window/
/// claude_integration.go `bindMCP`) without the widgets: "Register with
/// Claude", which adds the bundled `malachi-mcp` bridge to the MCP
/// configuration of one Claude app (`client`: Claude Desktop or Claude
/// Code, `--client` of the bridge; mcpsetup.Set), or takes it out, through
/// the bridge's own setup subcommands (`malachi-mcp status | install |
/// uninstall --json [--client ID]`; docs/mcp.md). The application never
/// touches those files itself. Without a `client` (the application's
/// status, the development steps) install and uninstall go to every
/// Claude app present, and a call may name its own client
/// (`change(registered:in:)`, an Integrations row's Unregister).
///
/// The switch is on when the bridge reports it as registered in `client`
/// (in at least one client without one). A state not known yet is never shown as "off" for long: the
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
/// GTK preferences.go `bindMCP` repeats it after the same pauses).
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
    /// What the switch shows: the last state the bridge confirmed for
    /// `client` (for any client without one).
    public private(set) var isRegistered = false
    /// Whether the last status has `client` as present (any client without
    /// one): with `isRegistered` what `Integrations.registerSwitchFor`
    /// needs. False until a status is known.
    public var isPresent: Bool {
        guard let status else { return false }
        guard let client else { return status.clients.contains { $0.present } }
        return status.client(client)?.present ?? false
    }

    /// The Claude app this controller registers the bridge in
    /// (`MCPStatus.clientDesktop`, `clientCode`); nil for every app present.
    public let client: String?
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
    /// A test build (`TestBuild`): the registration of another copy
    /// counts (`MCPStatus.countingOtherCopies`), and install and uninstall
    /// are refused, so the switch is never sensitive.
    public let readOnly: Bool
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
    ///   - client: the Claude app the switch is for; nil for every app.
    ///   - runner: how the bridge is run; the default runs the real thing.
    ///   - timeout: how long one call may take.
    ///   - statusRetryDelays: the pauses before repeating a failed status
    ///     check, one repeat each.
    public init(
        bridge: String?, client: String? = nil, runner: BridgeRunner = BridgeRunner(),
        timeout: Duration = MCPRegistrationController.defaultTimeout,
        statusRetryDelays: [Duration] = MCPRegistrationController.defaultStatusRetryDelays,
        readOnly: Bool = TestBuild.isActive
    ) {
        self.bridge = bridge
        self.client = client
        self.readOnly = readOnly
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
        let s = readOnly ? s.countingOtherCopies() : s
        guard !closed, bridge != nil, !inFlight, s != status else { return }
        status = s
        isRegistered = shown(s)
        statusRetries = 0
        setEnabled(true)
        onRegistered?(isRegistered)
    }

    // MARK: Changes

    /// Runs `install` or `uninstall` for `client` (nil: this controller's
    /// own) and shows what the bridge reports afterwards; on failure the
    /// switch goes back and a toast says why.
    public func set(registered want: Bool, in client: String? = nil) {
        guard !closed else { return }
        if readOnly {
            refuseWrite()
            return
        }
        guard let bridge else {
            reportMissing()
            onRegistered?(isRegistered)
            return
        }
        run(want ? .install : .uninstall, bridge, client: client ?? self.client)
    }

    /// `set(registered:in:)` that returns once the bridge answered, after the
    /// callbacks: the status the bridge reported, or nil when the call
    /// failed (its toast was shown), there is no bridge, a newer call
    /// overtook it or the controller closed. `ClaudeDesktopController`
    /// awaits it between quitting Claude Desktop and starting it again.
    public func change(registered want: Bool, in client: String? = nil) async -> MCPStatus? {
        guard !closed else { return nil }
        if readOnly {
            refuseWrite()
            return nil
        }
        guard let bridge else {
            reportMissing()
            onRegistered?(isRegistered)
            return nil
        }
        return await withCheckedContinuation { (cont: CheckedContinuation<MCPStatus?, Never>) in
            run(want ? .install : .uninstall, bridge, client: client ?? self.client) { cont.resume(returning: $0) }
        }
    }

    // MARK: Internals

    /// What the switch shows of `s`: registered in `client`, in any client
    /// without one.
    private func shown(_ s: MCPStatus) -> Bool {
        guard let client else { return s.isRegistered }
        return s.registered(in: client)
    }

    /// Runs `command` (install and uninstall with `--client` when `client`
    /// is set); `done` gets its status, or nil when it yielded none or its
    /// reply was dropped, exactly once.
    private func run(
        _ command: Command, _ bridge: String, client: String? = nil, done: (@MainActor (MCPStatus?) -> Void)? = nil
    ) {
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
            let outcome = await Self.invoke(runner, bridge, Self.arguments(command, client: client), timeout)
            guard let self, !self.closed, my == self.op else {
                done?(nil)
                return
            }
            self.inFlight = false
            var answer: MCPStatus?
            switch outcome {
            case .success(let reported):
                let s = self.readOnly ? reported.countingOtherCopies() : reported
                self.status = s
                self.isRegistered = self.shown(s)
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
                self.onToast?(f.toast(registering: command == .install, client: client))
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

    /// A test build changes no registration: the toast says where to,
    /// and the switch goes back.
    private func refuseWrite() {
        onToast?(TestBuild.registrationNote)
        onRegistered?(isRegistered)
    }

    private func setEnabled(_ on: Bool) {
        let on = on && !readOnly
        guard on != isEnabled else { return }
        isEnabled = on
        onEnabled?(on)
    }

    /// How the App Store build runs a setup subcommand instead of as a
    /// child: there the bridge has a sandbox of its own, which a child of
    /// the sandboxed app cannot have, so the app starts it through
    /// LaunchServices and reads its report from a file
    /// (`BridgeSetupLauncher` in MalachiMail, docs/plans/mac-app-store.md).
    /// It gets the subcommand and its `--client` flags (`arguments` without
    /// `--json`; the launcher asks for the report its own way). The same
    /// shape as `BridgeRunner.run`: what stdout and stderr would have held
    /// and the exit status. Set once at launch; nil runs the bridge as a
    /// child (the DMG, development builds, tests).
    public typealias SetupLauncher = @Sendable (_ bridge: String, _ setup: [String], _ timeout: Duration) async throws
        -> (stdout: Data, stderr: Data, status: Int32)
    nonisolated(unsafe) public static var setupLauncher: SetupLauncher?

    /// The subcommand and, for install and uninstall of one app, its
    /// `--client` (mcpsetup's `run`); without `--json`.
    nonisolated static func arguments(_ command: Command, client: String?) -> [String] {
        guard command != .status, let client else { return [command.rawValue] }
        return [command.rawValue, "--client", client]
    }

    /// Runs one subcommand (`setup`: it and its flags, `arguments`) and
    /// reads its status, off the main actor.
    private nonisolated static func invoke(_ runner: BridgeRunner, _ bridge: String, _ setup: [String], _ timeout: Duration) async -> Result<MCPStatus, MCPCallFailure> {
        let output: (stdout: Data, stderr: Data, status: Int32)
        let command = setup.first ?? ""
        do {
            if let launch = setupLauncher {
                output = try await launch(bridge, setup, timeout)
            } else {
                // mcpsetup's order: the subcommand, --json, then the flags.
                output = try await runner.run(bridge, [command, "--json"] + setup.dropFirst(), timeout: timeout)
            }
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
            return .failure(.failed("unexpected output from malachi-mcp \(command)"))
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
    /// `install` found neither Claude app, or not the one `--client` named.
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

    /// The toast for a failed install (`registering`) or uninstall of
    /// `client` (nil: every app). The msgids are the GTK dialog's: an app
    /// that was not found says so with its sheet's `notInstalled`
    /// (claude_integration.go `bindMCP`); without a client the bridge's
    /// reason is shown as any other.
    func toast(registering: Bool, client: String? = nil) -> String {
        switch self {
        case .noClaudeApp:
            let kind = Integrations.localKinds.first { Integrations.clientID($0) == client && client != nil }
            if let kind {
                return Integrations.claudeAppText(kind).notInstalled
            }
            return registering
                ? L10n.T("The MCP bridge could not be registered: %s", reason)
                : L10n.T("The MCP bridge could not be unregistered: %s", reason)
        case .failed, .run:
            return registering
                ? L10n.T("The MCP bridge could not be registered: %s", reason)
                : L10n.T("The MCP bridge could not be unregistered: %s", reason)
        }
    }
}
