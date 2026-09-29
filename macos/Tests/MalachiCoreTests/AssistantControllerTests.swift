// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The application's cache of what the Assistant menu (ui/internal/assistant)
// may use: the handlers from an injected lookup, the registration from a
// stand-in malachi-mcp (the fake-bridge approach of MCPRegistrationTests).
// The Go counterpart is ui/internal/window assistant_test.go.

private struct Timeout: Error {}

@MainActor
private func waitUntil(_ timeout: Duration = .seconds(5), _ cond: @MainActor () -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while !cond() {
        if ContinuousClock.now > deadline { throw Timeout() }
        try await Task.sleep(for: .milliseconds(10))
    }
}

/// What `malachi-mcp status --json` prints for the two clients.
private func statusJSON(desktop: Bool, code: Bool) -> String {
    """
    {"command": "/Applications/Malachi Mail.app/Contents/MacOS/malachi-mcp", "clients": [{"id": "claude-desktop", "name": "Claude Desktop", "present": true, "registered": \(desktop)}, {"id": "claude-code", "name": "Claude Code", "present": true, "registered": \(code)}]}
    """
}

private func status(desktop: Bool, code: Bool) throws -> MCPStatus {
    try JSONDecoder().decode(MCPStatus.self, from: Data(statusJSON(desktop: desktop, code: code).utf8))
}

/// A stand-in `malachi-mcp` whose `status --json` runs `body`; every
/// invocation's arguments are appended to `calls`.
private struct StatusBridge {
    let path: String
    private let log: URL

    init(_ body: String) throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("malachi-assistant-\(UUID().uuidString.prefix(8))", isDirectory: true)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        log = dir.appendingPathComponent("calls")
        let url = dir.appendingPathComponent("malachi-mcp")
        let script = """
            #!/bin/sh
            printf '%s\\n' "$*" >> '\(log.path)'
            [ "$1 $2" = "status --json" ] || { echo "unexpected $*" >&2; exit 2; }
            \(body)
            """
        try Data(script.utf8).write(to: url)
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: url.path)
        path = url.path
    }

    /// Prints `json`.
    init(printing json: String) throws {
        try self.init("printf '%s\\n' '\(json)'")
    }

    var calls: [String] {
        guard let text = try? String(contentsOf: log, encoding: .utf8) else { return [] }
        return text.split(separator: "\n").map(String.init)
    }
}

/// A pick as "target ok", for comparing.
private func picked(_ p: (target: Assistant.Target, ok: Bool)) -> String {
    "\(p.target.rawValue) \(p.ok)"
}

/// The handler lookup: the schemes asked, the answers scripted.
@MainActor
private final class Handlers {
    var installed: Set<String>
    var asked: [String] = []

    init(_ installed: Set<String>) {
        self.installed = installed
    }

    func lookup(_ scheme: String) -> Bool {
        asked.append(scheme)
        return installed.contains(scheme)
    }
}

@MainActor
@Suite(.serialized) struct AssistantControllerTests {
    private func make(
        bridge: String?, installed: Set<String>, scratch: ScratchSettings
    ) -> (AssistantController, Handlers) {
        let h = Handlers(installed)
        let c = AssistantController(bridge: bridge, settings: scratch.settings) { h.lookup($0) }
        return (c, h)
    }

    @Test func nothingIsKnownBeforeTheFirstRefresh() throws {
        let scratch = ScratchSettings()
        let bridge = try StatusBridge(printing: statusJSON(desktop: true, code: true))
        let (c, h) = make(bridge: bridge.path, installed: ["claude", "claude-cli"], scratch: scratch)
        #expect(c.availability(.desktop) == Assistant.Availability())
        #expect(c.availability(.code) == Assistant.Availability())
        #expect(picked(c.pick(needsBridge: true)) == "desktop false")
        #expect(picked(c.pick(needsBridge: false)) == "desktop false")
        #expect(c.problem(.desktop) == "Claude Desktop is not installed")
        #expect(h.asked.isEmpty)
        #expect(bridge.calls.isEmpty)
    }

    @Test func refreshLooksUpTheHandlersAtOnceAndTheStatusInTheBackground() async throws {
        let scratch = ScratchSettings()
        let bridge = try StatusBridge(printing: statusJSON(desktop: true, code: false))
        let (c, h) = make(bridge: bridge.path, installed: ["claude"], scratch: scratch)
        var changes = 0
        let token = c.onChange { changes += 1 }
        c.refresh()
        #expect(h.asked == ["claude", "claude-cli"])
        #expect(changes == 1, "the handlers changed")
        // Not registered until the bridge answered: a file can go, mail not.
        #expect(c.availability(.desktop) == Assistant.Availability(handler: true, registered: false))
        #expect(picked(c.pick(needsBridge: false)) == "desktop true")
        #expect(picked(c.pick(needsBridge: true)) == "desktop false")
        #expect(c.problem(.desktop) == "Turn on Register with Claude so that Claude can read your mail")

        try await waitUntil { c.status != nil }
        #expect(changes == 2)
        #expect(bridge.calls == ["status --json"])
        #expect(c.availability(.desktop) == Assistant.Availability(handler: true, registered: true))
        #expect(c.availability(.code) == Assistant.Availability(handler: false, registered: false))
        #expect(picked(c.pick(needsBridge: true)) == "desktop true")
        #expect(c.problem(.desktop) == "")
        #expect(c.problem(.code) == "Claude Code is not installed, or has not been used in a terminal yet")

        // The same answer again changes nothing.
        c.refresh()
        try await waitUntil { bridge.calls.count == 2 }
        try await Task.sleep(for: .milliseconds(100))
        #expect(changes == 2)
        token.cancel()
    }

    @Test func pickFollowsThePreferenceWithoutFallingBack() async throws {
        let scratch = ScratchSettings()
        let bridge = try StatusBridge(printing: statusJSON(desktop: true, code: true))
        let (c, h) = make(bridge: bridge.path, installed: ["claude", "claude-cli"], scratch: scratch)
        c.refresh()
        try await waitUntil { c.status != nil }
        #expect(picked(c.pick(needsBridge: true)) == "desktop true")
        scratch.settings.assistantTarget = .code
        #expect(picked(c.pick(needsBridge: true)) == "code true")

        // Claude Code's handler goes away (the menu opens again): still
        // Claude Code, not usable, and the menu says why; Claude Desktop is
        // not opened instead.
        h.installed = ["claude"]
        c.refreshHandlers()
        #expect(picked(c.pick(needsBridge: true)) == "code false")
        #expect(picked(c.pick(needsBridge: false)) == "code false")
        #expect(c.problem(c.pick(needsBridge: true).target) == "Claude Code is not installed, or has not been used in a terminal yet")
        // Neither: the preference, not usable.
        h.installed = []
        c.refreshHandlers()
        #expect(picked(c.pick(needsBridge: true)) == "code false")
        #expect(picked(c.pick(needsBridge: false)) == "code false")
        // Back to Claude Desktop, which is not installed either now.
        scratch.settings.assistantTarget = .desktop
        #expect(picked(c.pick(needsBridge: true)) == "desktop false")
        #expect(c.problem(.desktop) == "Claude Desktop is not installed")
    }

    @Test func aStatusFromTheSettingsCountsAtOnce() throws {
        let scratch = ScratchSettings()
        let bridge = try StatusBridge(printing: statusJSON(desktop: false, code: false))
        let (c, _) = make(bridge: bridge.path, installed: ["claude", "claude-cli"], scratch: scratch)
        c.refreshHandlers()
        var changes = 0
        _ = c.onChange { changes += 1 }
        c.apply(try status(desktop: true, code: true))
        #expect(changes == 1)
        #expect(picked(c.pick(needsBridge: true)) == "desktop true")
        #expect(c.availability(.code).registered)
        c.apply(try status(desktop: true, code: true))
        #expect(changes == 1, "an unchanged status is no change")
        c.apply(try status(desktop: false, code: false))
        #expect(changes == 2)
        #expect(picked(c.pick(needsBridge: true)) == "desktop false")
        #expect(bridge.calls.isEmpty, "nothing was run")
    }

    @Test func shownNeedsThePreferenceAndTheBridge() throws {
        let scratch = ScratchSettings()
        let (c, _) = make(bridge: nil, installed: ["claude"], scratch: scratch)
        var changes = 0
        let token = c.onChange { changes += 1 }
        // No status yet: not registered, so not shown, whatever the
        // preference says.
        #expect(scratch.settings.assistantMenu)
        #expect(!c.registered)
        #expect(!c.shown)
        c.apply(try status(desktop: false, code: true))
        #expect(c.registered, "one client is enough")
        #expect(c.shown)
        #expect(changes == 1)
        // The preference is reported too, so the menus follow one source.
        scratch.settings.assistantMenu = false
        #expect(!c.shown)
        #expect(changes == 2)
        scratch.settings.assistantMenu = true
        #expect(c.shown)
        #expect(changes == 3)
        // The bridge unregistered: hidden again, the preference kept.
        c.apply(try status(desktop: false, code: false))
        #expect(!c.shown)
        #expect(scratch.settings.assistantMenu)
        #expect(changes == 4)
        // Closed: a preference change is no longer reported.
        c.close()
        scratch.settings.assistantMenu = false
        #expect(changes == 4)
        token.cancel()
    }

    @Test func aFailedStatusKeepsTheLastKnownOne() async throws {
        let scratch = ScratchSettings()
        // Answers the first time, fails afterwards.
        let flag = FileManager.default.temporaryDirectory.appendingPathComponent("malachi-assistant-flag-\(UUID().uuidString.prefix(8))")
        let bridge = try StatusBridge("[ -e '\(flag.path)' ] && { echo 'read config: permission denied' >&2; exit 1; }; touch '\(flag.path)'; printf '%s\\n' '\(statusJSON(desktop: true, code: false))'")
        let (c, _) = make(bridge: bridge.path, installed: ["claude"], scratch: scratch)
        c.refresh()
        try await waitUntil { c.status != nil }
        #expect(picked(c.pick(needsBridge: true)) == "desktop true")
        c.refresh()
        try await waitUntil { bridge.calls.count == 2 }
        try await Task.sleep(for: .milliseconds(200))
        #expect(c.availability(.desktop).registered)
        #expect(picked(c.pick(needsBridge: true)) == "desktop true")
    }

    @Test func unknownClientsAndGarbageCountAsNotRegistered() async throws {
        let scratch = ScratchSettings()
        let other = """
            {"command": "/x/malachi-mcp", "clients": [{"id": "claude-web", "name": "Claude", "present": true, "registered": true}]}
            """
        let bridge = try StatusBridge(printing: other)
        let (c, _) = make(bridge: bridge.path, installed: ["claude", "claude-cli"], scratch: scratch)
        c.refresh()
        try await waitUntil { c.status != nil }
        #expect(!c.availability(.desktop).registered)
        #expect(!c.availability(.code).registered)
        #expect(picked(c.pick(needsBridge: true)) == "desktop false")
        // An unknown target reads as Claude Desktop.
        #expect(c.availability(Assistant.Target("x")) == c.availability(.desktop))

        let garbage = try StatusBridge("echo garbage")
        let (g, _) = make(bridge: garbage.path, installed: ["claude"], scratch: scratch)
        g.refresh()
        try await waitUntil { garbage.calls.count == 1 }
        try await Task.sleep(for: .milliseconds(200))
        #expect(g.status == nil)
        #expect(picked(g.pick(needsBridge: true)) == "desktop false")
        #expect(picked(g.pick(needsBridge: false)) == "desktop true")
    }

    @Test func withoutABridgeOnlyTheFileHandOffWorks() async throws {
        let scratch = ScratchSettings()
        let (c, h) = make(bridge: nil, installed: ["claude-cli"], scratch: scratch)
        c.refresh()
        c.refresh()
        try await Task.sleep(for: .milliseconds(50))
        #expect(c.status == nil)
        #expect(h.asked == ["claude", "claude-cli", "claude", "claude-cli"])
        // Claude Desktop is preferred and not installed: nothing, not even
        // the file, goes to Claude Code instead.
        #expect(picked(c.pick(needsBridge: true)) == "desktop false")
        #expect(picked(c.pick(needsBridge: false)) == "desktop false")
        scratch.settings.assistantTarget = .code
        #expect(picked(c.pick(needsBridge: true)) == "code false")
        #expect(picked(c.pick(needsBridge: false)) == "code true")
        #expect(c.problem(.code) == "Turn on Register with Claude so that Claude can read your mail")
    }

    @Test func aRefreshDuringACallIsSkipped() async throws {
        let scratch = ScratchSettings()
        let bridge = try StatusBridge("sleep 0.3; printf '%s\\n' '\(statusJSON(desktop: true, code: true))'")
        let (c, _) = make(bridge: bridge.path, installed: ["claude"], scratch: scratch)
        c.refresh()
        c.refresh()
        c.refresh()
        try await waitUntil { c.status != nil }
        #expect(bridge.calls == ["status --json"])
    }

    @Test func closeDropsLateRepliesAndObservers() async throws {
        let scratch = ScratchSettings()
        let bridge = try StatusBridge("sleep 0.3; printf '%s\\n' '\(statusJSON(desktop: true, code: true))'")
        let (c, h) = make(bridge: bridge.path, installed: ["claude"], scratch: scratch)
        var changes = 0
        _ = c.onChange { changes += 1 }
        c.refresh()
        #expect(changes == 1)
        c.close()
        try await Task.sleep(for: .milliseconds(600))
        #expect(c.status == nil)
        #expect(changes == 1)
        c.refresh()
        c.apply(try status(desktop: true, code: true))
        #expect(c.status == nil)
        #expect(h.asked.count == 2)
        #expect(bridge.calls.count == 1)
    }

    /// The In App target: its "handler" is Claude Code found with the
    /// bridge beside the application, its registration any client's; the
    /// panel exists while the Assistant is shown and In App chosen.
    @Test func theAppTargetNeedsClaudeCodeAndTheBridge() async throws {
        let scratch = ScratchSettings()
        let dir = try assistantScratchDir()
        let claude = try writeScript(dir.appendingPathComponent("claude"), "exit 0")
        let bridge = try StatusBridge(printing: statusJSON(desktop: false, code: true))
        let prefix = dir.path + "/"
        let locator = ClaudeCodeLocator(
            settings: scratch.settings, environment: ["HOME": dir.path, "PATH": ""],
            usable: { $0.hasPrefix(prefix) && ClaudeCodeLocator.isExecutableFile($0) })
        let h = Handlers(["claude"])
        let c = AssistantController(bridge: bridge.path, settings: scratch.settings, locator: locator) { h.lookup($0) }
        scratch.settings.assistantTarget = .app
        c.refresh()
        #expect(h.asked == ["claude", "claude-cli"], "the panel has no link to look up")
        #expect(c.availability(.app) == Assistant.Availability())
        #expect(c.problem(.app) == "Claude Code was not found on this computer")
        #expect(picked(c.pick(needsBridge: true)) == "app false")

        scratch.settings.assistantClaudePath = claude
        c.refresh()
        try await waitUntil { c.status != nil }
        #expect(c.availability(.app) == Assistant.Availability(handler: true, registered: true))
        #expect(picked(c.pick(needsBridge: true)) == "app true")
        #expect(c.problem(.app) == "")
        #expect(c.shown && c.panelShown)
        scratch.settings.assistantTarget = .code
        #expect(!c.panelShown)
        scratch.settings.assistantTarget = .app
        scratch.settings.assistantMenu = false
        #expect(!c.panelShown)
        scratch.settings.assistantMenu = true

        // Registered nowhere: the panel is gone with the Assistant.
        c.apply(try status(desktop: false, code: false))
        #expect(c.availability(.app) == Assistant.Availability(handler: true, registered: false))
        #expect(!c.shown && !c.panelShown)

        // No bridge beside the application: never available.
        let bare = AssistantController(bridge: nil, settings: scratch.settings, locator: locator) { h.lookup($0) }
        bare.refreshHandlers()
        #expect(!bare.availability(.app).handler)
    }

    @Test func aCancelledObserverIsNotCalled() {
        let scratch = ScratchSettings()
        let (c, h) = make(bridge: nil, installed: [], scratch: scratch)
        var a = 0
        var b = 0
        let ta = c.onChange { a += 1 }
        _ = c.onChange { b += 1 }
        h.installed = ["claude"]
        c.refreshHandlers()
        ta.cancel()
        h.installed = ["claude", "claude-cli"]
        c.refreshHandlers()
        #expect(a == 1)
        #expect(b == 2)
    }
}
