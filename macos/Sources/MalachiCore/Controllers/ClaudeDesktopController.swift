// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// Claude Desktop around a change of "Register with Claude" (the MCP switch
/// of Settings → AI, `MCPRegistrationController`). Claude Desktop reads its
/// MCP servers only when it starts and, while it runs, rewrites its
/// configuration file (`claude_desktop_config.json`, which holds its own
/// preferences too) from memory many times a day, so an entry that
/// `malachi-mcp install` writes, or `uninstall` removes, while it runs is
/// undone at its next write (docs/mcp.md). Claude Code does not do this.
///
/// So a flip of the switch while Claude Desktop runs and the bridge's
/// status has it as a present client (`offersRestart`) asks first
/// (`Assistant.restartTexts()`). *Restart Claude Desktop* asks it to quit,
/// waits until it has terminated (at most `quitTimeout`), writes the change
/// and starts it again; when it did not quit in time a toast says so and
/// the change is written anyway and stays pending. *Later* writes the
/// change now and keeps it pending. A pending change (`pending`: the
/// registration Claude Desktop still has to get) has a row under the switch
/// whose *Restart* is the same restart (`restartPending`), and is written
/// once more as soon as Claude Desktop quits by itself (`terminated`, from
/// the platform's notification): then the write sticks, and its next start
/// loads it. Pending lives for the application's run only.
///
/// The writes go one at a time, whether through the page's registration
/// (so its switch and row follow) or this controller's own (`writeOwn`,
/// the termination's: an `MCPRegistrationController` without toasts, as
/// `AssistantController` has one); `busy` while one runs or a restart
/// waits. Every status a write reports goes to `onStatus`, which the
/// application hands to `AssistantController.apply`.
///
/// No AppKit here: whether Claude Desktop runs, quitting and starting it
/// are injected (`Platform`: `NSRunningApplication` and `NSWorkspace` in
/// the application, fakes in the tests), and so is the question. GTK has
/// no equivalent yet: this client leads, the GTK page and the Windows
/// client follow with the same texts (ui/internal/assistant
/// `RestartTexts`).
@MainActor
public final class ClaudeDesktopController {
    /// What the application does to Claude Desktop.
    public struct Platform {
        /// Whether Claude Desktop runs now.
        public var isRunning: @MainActor () -> Bool
        /// Asks Claude Desktop to quit and waits until it has terminated,
        /// at most `timeout`; true when it has (or did not run at all).
        public var quit: @MainActor (_ timeout: Duration) async -> Bool
        /// Starts Claude Desktop.
        public var launch: @MainActor () -> Void

        public init(
            isRunning: @escaping @MainActor () -> Bool, quit: @escaping @MainActor (_ timeout: Duration) async -> Bool,
            launch: @escaping @MainActor () -> Void
        ) {
            self.isRunning = isRunning
            self.quit = quit
            self.launch = launch
        }
    }

    /// Runs `malachi-mcp install` (true) or `uninstall` (false) and returns
    /// the status the bridge reported afterwards, nil when the call failed
    /// (`MCPRegistrationController.change`).
    public typealias Write = @MainActor (_ registered: Bool) async -> MCPStatus?

    /// What the user answered to "Restart Claude Desktop?".
    public enum Answer: Sendable, Equatable {
        case restart, later
    }

    /// Claude Desktop's bundle identifier.
    public nonisolated static let bundleIdentifier = "com.anthropic.claudefordesktop"
    /// How long a restart waits for Claude Desktop to quit.
    public nonisolated static let defaultQuitTimeout: Duration = .seconds(20)

    /// The registration Claude Desktop still has to get: true registered,
    /// false unregistered, nil nothing pending.
    public private(set) var pending: Bool?
    /// A write runs or a restart waits: the switch and the pending row's
    /// Restart wait too.
    public private(set) var busy = false
    /// Nothing runs and nothing is reported afterwards.
    public private(set) var closed = false

    /// Called when `pending` or `busy` changed; the AI page sets it while
    /// it is open.
    public var onChange: (@MainActor () -> Void)?
    /// Called with the text of a toast (Claude Desktop did not quit); the
    /// AI page sets it while it is open, otherwise it is only logged.
    public var onToast: (@MainActor (String) -> Void)?
    /// Called with every status a write reported.
    public var onStatus: (@MainActor (MCPStatus) -> Void)?

    private let platform: Platform
    private let quitTimeout: Duration
    private let own: Write
    private var ownRegistration: MCPRegistrationController?
    /// A restart runs: the termination it causes is its own.
    private var restarting = false
    /// One operation at a time: whether one runs, and who waits.
    private var running = false
    private var waiting: [CheckedContinuation<Void, Never>] = []
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "mcp")

    /// - Parameters:
    ///   - platform: Claude Desktop, as the application sees it.
    ///   - quitTimeout: how long a restart waits for Claude Desktop to quit.
    ///   - write: this controller's own write, for the termination and
    ///     `writeOwn`.
    public init(platform: Platform, quitTimeout: Duration = ClaudeDesktopController.defaultQuitTimeout, write: @escaping Write) {
        self.platform = platform
        self.quitTimeout = quitTimeout
        own = write
    }

    /// With an `MCPRegistrationController` of its own over `bridge`
    /// (`Paths.mcpBridge`; nil when there is none beside the application)
    /// as its write: no toasts, a failure is logged.
    public convenience init(
        bridge: String?, platform: Platform, runner: BridgeRunner = BridgeRunner(),
        timeout: Duration = MCPRegistrationController.defaultTimeout
    ) {
        let registration = MCPRegistrationController(bridge: bridge, runner: runner, timeout: timeout)
        self.init(platform: platform) { await registration.change(registered: $0) }
        ownRegistration = registration
    }

    /// Stops: nothing runs or is reported afterwards, late answers are
    /// dropped, whoever waits for a write goes on without it.
    public func close() {
        closed = true
        ownRegistration?.close()
        onChange = nil
        onToast = nil
        onStatus = nil
        let released = waiting
        waiting = []
        running = false
        for w in released {
            w.resume()
        }
    }

    // MARK: The switch

    /// Whether a change of the registration offers the restart first:
    /// Claude Desktop runs, and `status` (the page's last from the bridge)
    /// has it as a present client.
    public func offersRestart(_ status: MCPStatus?) -> Bool {
        guard !closed,
              status?.clients.contains(where: { $0.id == Assistant.Target.desktop.clientID && $0.present }) == true
        else {
            return false
        }
        return platform.isRunning()
    }

    /// The AI page's switch flipped to `want`. `status` is the page's last
    /// status, `write` the page's own write (its switch and row follow it).
    /// Without the offer (`offersRestart`) the change is written at once;
    /// otherwise `ask` decides: the restart, or the write now with the
    /// change pending while Claude Desktop runs.
    public func change(registered want: Bool, status: MCPStatus?, ask: @MainActor () async -> Answer, write: Write) async {
        guard !closed else { return }
        guard offersRestart(status) else {
            await serialized { await writeNow(want, write, later: false) }
            return
        }
        let answer = await ask()
        guard !closed else { return }
        switch answer {
        case .restart:
            await serialized { await restart(want, write) }
        case .later:
            await serialized { await writeNow(want, write, later: true) }
        }
    }

    /// The pending row's *Restart*: the restart with the pending state,
    /// through `write` (the page's). Nothing when nothing is pending by
    /// the time it runs.
    public func restartPending(write: Write) async {
        await serialized {
            guard let want = pending else { return }
            await restart(want, write)
        }
    }

    /// A write through this controller's own registration: for a page
    /// whose registration closed with its window while a restart waited.
    /// Not serialised itself, it runs inside the operation that calls it.
    public func writeOwn(registered want: Bool) async -> MCPStatus? {
        guard !closed else { return nil }
        return await own(want)
    }

    // MARK: Claude Desktop quit

    /// Claude Desktop has terminated (the platform's notification): a
    /// pending change is written once more now that it does not run, then
    /// no longer pending. The termination of a restart is the restart's
    /// own; nothing pending, nothing to do.
    public func terminated() {
        guard !closed, !restarting, pending != nil else { return }
        Task { [weak self] in
            await self?.reapply()
        }
    }

    private func reapply() async {
        await serialized {
            // A restart queued before may have written it already, or
            // Claude Desktop runs again: then the write would not stick.
            guard let want = pending, !platform.isRunning() else { return }
            guard let s = await own(want) else {
                log.warning("writing the MCP registration again after Claude Desktop quit failed; it stays pending")
                return
            }
            guard !closed else { return }
            onStatus?(s)
            setPending(nil)
        }
    }

    // MARK: Internals

    /// Writes `want` now. While Claude Desktop runs, `later` keeps the
    /// change pending (it overwrites the change); once it does not run the
    /// change sticks and nothing is pending.
    private func writeNow(_ want: Bool, _ write: Write, later: Bool) async {
        guard let s = await write(want), !closed else { return }
        onStatus?(s)
        if !platform.isRunning() {
            setPending(nil)
        } else if later {
            setPending(want)
        }
    }

    /// Quit, wait, write, start. When Claude Desktop did not quit in time
    /// the change is written anyway and stays pending; a failed write
    /// leaves `pending` as it was, and a Claude Desktop that quit is
    /// started again either way.
    private func restart(_ want: Bool, _ write: Write) async {
        restarting = true
        defer { restarting = false }
        let quit = await platform.quit(quitTimeout)
        guard !closed else { return }
        if !quit {
            log.warning("Claude Desktop did not quit within \(self.quitTimeout, privacy: .public); writing the MCP registration anyway")
            onToast?(Assistant.restartTexts().notQuit)
        }
        if let s = await write(want) {
            guard !closed else { return }
            onStatus?(s)
            setPending(quit ? nil : want)
        }
        guard !closed, quit else { return }
        platform.launch()
    }

    /// Runs `op` once no other operation runs, `busy` meanwhile; nothing
    /// once closed.
    private func serialized(_ op: () async -> Void) async {
        if running {
            await withCheckedContinuation { (cont: CheckedContinuation<Void, Never>) in
                waiting.append(cont)
            }
        } else {
            running = true
        }
        defer { handOver() }
        guard !closed else { return }
        setBusy(true)
        await op()
    }

    /// The next waiting operation runs, or none is busy.
    private func handOver() {
        guard !closed else { return }
        if waiting.isEmpty {
            running = false
            setBusy(false)
        } else {
            waiting.removeFirst().resume()
        }
    }

    private func setPending(_ p: Bool?) {
        guard p != pending else { return }
        pending = p
        onChange?()
    }

    private func setBusy(_ b: Bool) {
        guard b != busy else { return }
        busy = b
        onChange?()
    }
}
