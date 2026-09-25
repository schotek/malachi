// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The MCP group of the AI page against a stand-in malachi-mcp
// (ui/internal/window/preferences.go `bindMCP`): a shell script that
// answers status / install / uninstall as scripted and logs its arguments.

private struct Timeout: Error {}

@MainActor
private func waitUntil(_ timeout: Duration = .seconds(5), _ cond: @MainActor () async -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while await !cond() {
        if ContinuousClock.now > deadline { throw Timeout() }
        try await Task.sleep(for: .milliseconds(10))
    }
}

private let bridgeCommand = "/Applications/Malachi Mail.app/Contents/MacOS/malachi-mcp"

/// The JSON the bridge prints: Claude Desktop registered as asked, Claude
/// Code present but never registered.
private func statusJSON(registered: Bool) -> String {
    """
    {"command": "\(bridgeCommand)", "clients": [{"id": "claude-desktop", "name": "Claude Desktop", "present": true, "registered": \(registered), "path": "/Users/u/Library/Application Support/Claude/claude_desktop_config.json"}, {"id": "claude-code", "name": "Claude Code", "present": true, "registered": false, "path": "/Users/u/.claude.json"}]}
    """
}

/// A script body that prints `json` and exits 0.
private func prints(_ json: String) -> String {
    "printf '%s\\n' '\(json)'"
}

/// A script body that fails with `reason` on stderr, as the bridge does.
private func fails(_ reason: String) -> String {
    "echo '\(reason)' >&2; exit 1"
}

/// A stand-in `malachi-mcp` in a fresh temp dir: `#!/bin/sh`, one `case`
/// arm per subcommand, every invocation's arguments appended to `calls`.
private struct FakeBridge {
    let path: String
    private let log: URL

    init(status: String, install: String = "exit 9", uninstall: String = "exit 9") throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("malachi-mcp-\(UUID().uuidString.prefix(8))", isDirectory: true)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        log = dir.appendingPathComponent("calls")
        let url = dir.appendingPathComponent("malachi-mcp")
        let script = """
            #!/bin/sh
            printf '%s\\n' "$*" >> '\(log.path)'
            [ "$2" = "--json" ] || { echo "expected --json, got $2" >&2; exit 2; }
            case "$1" in
              status) \(status) ;;
              install) \(install) ;;
              uninstall) \(uninstall) ;;
              *) echo "unknown command $1" >&2; exit 2 ;;
            esac
            """
        try Data(script.utf8).write(to: url)
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: url.path)
        path = url.path
    }

    /// The arguments of every invocation so far, one line each.
    var calls: [String] {
        guard let text = try? String(contentsOf: log, encoding: .utf8) else { return [] }
        return text.split(separator: "\n").map(String.init)
    }
}

/// Collects what the controller emits.
@MainActor
private final class Recorder {
    var registered: [Bool] = []
    var enabled: [Bool] = []
    var toasts: [String] = []

    func attach(_ c: MCPRegistrationController) {
        c.onRegistered = { [weak self] in self?.registered.append($0) }
        c.onEnabled = { [weak self] in self?.enabled.append($0) }
        c.onToast = { [weak self] in self?.toasts.append($0) }
    }
}

@MainActor
private func makeController(_ bridge: FakeBridge, timeout: Duration = .seconds(15)) -> (MCPRegistrationController, Recorder) {
    let c = MCPRegistrationController(bridge: bridge.path, timeout: timeout)
    let rec = Recorder()
    rec.attach(c)
    return (c, rec)
}

/// A controller whose first status has answered.
@MainActor
private func loadedController(_ bridge: FakeBridge, timeout: Duration = .seconds(15)) async throws -> (MCPRegistrationController, Recorder) {
    let (c, rec) = makeController(bridge, timeout: timeout)
    c.load()
    try await waitUntil { c.isEnabled }
    return (c, rec)
}

@MainActor
@Suite(.serialized) struct MCPRegistrationTests {
    @Test func statusDecodesWhatTheBridgePrints() throws {
        let s = try JSONDecoder().decode(MCPStatus.self, from: Data(statusJSON(registered: true).utf8))
        #expect(s.command == bridgeCommand)
        #expect(s.clients.map(\.id) == ["claude-desktop", "claude-code"])
        #expect(s.clients[0].registered && s.clients[0].present)
        #expect(s.clients[0].path?.hasSuffix("claude_desktop_config.json") == true)
        #expect(s.clients[0].other == nil)
        #expect(s.isRegistered)
        let off = try JSONDecoder().decode(MCPStatus.self, from: Data(statusJSON(registered: false).utf8))
        #expect(!off.isRegistered)

        // Tolerant: unknown keys, an absent path, `other`, a null client list.
        let odd = """
            {"command": "/x/malachi-mcp", "version": "0.2", "clients": [{"id": "claude-code", "name": "Claude Code", "present": false, "registered": false, "other": "/old/malachi-mcp", "extra": 1}]}
            """
        let o = try JSONDecoder().decode(MCPStatus.self, from: Data(odd.utf8))
        #expect(o.clients.count == 1)
        #expect(o.clients.first?.path == nil)
        #expect(o.clients.first?.other == "/old/malachi-mcp")
        #expect(!o.isRegistered)
        let none = try JSONDecoder().decode(MCPStatus.self, from: Data(#"{"command": "/x", "clients": null}"#.utf8))
        #expect(none.clients.isEmpty)
        #expect(!none.isRegistered)
    }

    @Test func loadShowsTheStatusAndEnablesTheRow() async throws {
        let bridge = try FakeBridge(status: prints(statusJSON(registered: true)))
        let (c, rec) = makeController(bridge)
        #expect(!c.isEnabled)
        #expect(!c.isRegistered)
        #expect(c.status == nil)
        c.load()
        try await waitUntil { c.isEnabled }
        #expect(c.isRegistered)
        #expect(c.status?.command == bridgeCommand)
        #expect(rec.registered == [true])
        #expect(rec.enabled == [true])
        #expect(rec.toasts.isEmpty)
        #expect(bridge.calls == ["status --json"])

        // Asked again (the page came up again): a fresh status.
        c.load()
        #expect(!c.isEnabled)
        try await waitUntil { c.isEnabled }
        #expect(bridge.calls == ["status --json", "status --json"])
        #expect(rec.enabled == [true, false, true])
        #expect(rec.registered == [true, true])
    }

    @Test func aFailedStatusIsOnlyLoggedAndTheRowStaysInsensitive() async throws {
        // Fails the first time, answers the second (the page came up again).
        let flag = FileManager.default.temporaryDirectory.appendingPathComponent("malachi-mcp-flag-\(UUID().uuidString.prefix(8))")
        let bridge = try FakeBridge(status: "[ -e '\(flag.path)' ] && \(prints(statusJSON(registered: true))) || { touch '\(flag.path)'; \(fails("read config: permission denied")); }")
        let (c, rec) = makeController(bridge)
        c.load()
        try await waitUntil { bridge.calls.count == 1 }
        try await Task.sleep(for: .milliseconds(300))
        #expect(!c.isEnabled, "no sentence of its own: the row simply stays insensitive")
        #expect(!c.isRegistered)
        #expect(c.status == nil)
        #expect(rec.toasts.isEmpty)
        #expect(rec.registered.isEmpty)
        #expect(rec.enabled.isEmpty)

        c.load()
        try await waitUntil { c.isEnabled }
        #expect(c.isRegistered)
        #expect(rec.registered == [true])
        #expect(rec.enabled == [true])
        #expect(bridge.calls == ["status --json", "status --json"])
    }

    @Test func setRegistersThroughInstall() async throws {
        let bridge = try FakeBridge(status: prints(statusJSON(registered: false)), install: prints(statusJSON(registered: true)))
        let (c, rec) = try await loadedController(bridge)
        #expect(!c.isRegistered)
        c.set(registered: true)
        #expect(!c.isEnabled, "insensitive while the call runs")
        try await waitUntil { c.isEnabled }
        #expect(c.isRegistered)
        #expect(c.status?.isRegistered == true)
        #expect(rec.registered == [false, true])
        #expect(rec.enabled == [true, false, true])
        #expect(rec.toasts.isEmpty)
        #expect(bridge.calls == ["status --json", "install --json"])
    }

    @Test func setUnregistersThroughUninstall() async throws {
        let bridge = try FakeBridge(status: prints(statusJSON(registered: true)), uninstall: prints(statusJSON(registered: false)))
        let (c, rec) = try await loadedController(bridge)
        #expect(c.isRegistered)
        c.set(registered: false)
        try await waitUntil { c.isEnabled }
        #expect(!c.isRegistered)
        #expect(rec.registered == [true, false])
        #expect(rec.toasts.isEmpty)
        #expect(bridge.calls == ["status --json", "uninstall --json"])
    }

    @Test func installWithoutAClaudeAppToastsAndTheSwitchStaysOff() async throws {
        let bridge = try FakeBridge(
            status: prints(statusJSON(registered: false)),
            install: fails("no Claude app found on this computer (looked for Claude Desktop and Claude Code)")
        )
        let (c, rec) = try await loadedController(bridge)
        c.set(registered: true)
        try await waitUntil { c.isEnabled }
        #expect(rec.toasts == ["No Claude app was found on this computer"])
        #expect(!c.isRegistered)
        #expect(rec.registered == [false, false], "rendered again so the switch reverts")
        #expect(rec.enabled == [true, false, true])
    }

    @Test func aFailedCallToastsTheBridgesReasonAndReverts() async throws {
        let bridge = try FakeBridge(
            status: prints(statusJSON(registered: true)),
            install: fails("write /Users/u/.claude.json: permission denied"),
            uninstall: fails("write /Users/u/.claude.json: permission denied")
        )
        let (c, rec) = try await loadedController(bridge)
        c.set(registered: false)
        try await waitUntil { c.isEnabled }
        #expect(rec.toasts == ["The MCP bridge could not be unregistered: write /Users/u/.claude.json: permission denied"])
        #expect(c.isRegistered, "the last confirmed state stays")
        #expect(rec.registered == [true, true])

        c.set(registered: true)
        try await waitUntil { rec.toasts.count == 2 }
        #expect(rec.toasts[1] == "The MCP bridge could not be registered: write /Users/u/.claude.json: permission denied")
        #expect(c.isRegistered)
        #expect(bridge.calls == ["status --json", "uninstall --json", "install --json"])
    }

    @Test func aSilentFailureNamesTheExitStatus() async throws {
        let bridge = try FakeBridge(status: prints(statusJSON(registered: false)), install: "exit 3")
        let (c, rec) = try await loadedController(bridge)
        c.set(registered: true)
        try await waitUntil { c.isEnabled }
        #expect(rec.toasts == ["The MCP bridge could not be registered: malachi-mcp exited with status 3"])
        #expect(!c.isRegistered)
    }

    @Test func unparsableOutputToastsAndReverts() async throws {
        let bridge = try FakeBridge(status: prints(statusJSON(registered: false)), install: "echo garbage")
        let (c, rec) = try await loadedController(bridge)
        c.set(registered: true)
        try await waitUntil { c.isEnabled }
        #expect(rec.toasts.count == 1)
        #expect(rec.toasts.first?.hasPrefix("The MCP bridge could not be registered: ") == true)
        #expect(!c.isRegistered)
        #expect(rec.registered == [false, false])
        #expect(c.status?.isRegistered == false, "the last good status stays")
    }

    @Test func aHungBridgeTimesOut() async throws {
        // A plain `sleep`, not `exec sleep`: the shell dies of SIGTERM and
        // the orphaned sleep keeps the pipes open, the harder case.
        let bridge = try FakeBridge(status: prints(statusJSON(registered: false)), install: "sleep 30")
        let (c, rec) = try await loadedController(bridge, timeout: .seconds(1))
        let start = ContinuousClock.now
        c.set(registered: true)
        try await waitUntil(.seconds(10)) { c.isEnabled }
        #expect(ContinuousClock.now - start < .seconds(5), "the bridge was killed, not waited for")
        #expect(rec.toasts.count == 1)
        #expect(rec.toasts.first?.hasPrefix("The MCP bridge could not be registered: ") == true)
        #expect(rec.toasts.first?.contains("did not finish within 1 s") == true)
        #expect(!c.isRegistered)
        #expect(rec.registered == [false, false])
    }

    @Test func withoutABridgeTheRowStaysInsensitiveAndSaysSoOnce() async throws {
        let c = MCPRegistrationController(bridge: nil)
        let rec = Recorder()
        rec.attach(c)
        c.load()
        c.load()
        try await Task.sleep(for: .milliseconds(50))
        #expect(!c.isEnabled)
        #expect(rec.enabled == [false])
        #expect(rec.toasts == ["The MCP bridge (malachi-mcp) was not found next to the application"])
        // A flip that somehow got through goes back, without a second toast.
        c.set(registered: true)
        #expect(rec.registered == [false])
        #expect(rec.toasts.count == 1)
        #expect(!c.isRegistered)
    }

    @Test func aStaleReplyDoesNotOverwriteANewerOne() async throws {
        let bridge = try FakeBridge(
            status: prints(statusJSON(registered: false)),
            install: "sleep 0.5; \(prints(statusJSON(registered: true)))",
            uninstall: prints(statusJSON(registered: false))
        )
        let (c, rec) = try await loadedController(bridge)
        c.set(registered: true) // slow
        c.set(registered: false) // fast, answers first
        try await waitUntil { c.isEnabled }
        #expect(!c.isRegistered)
        let renders = rec.registered.count
        let toggles = rec.enabled.count

        // The slow reply arrives and is dropped.
        try await Task.sleep(for: .milliseconds(800))
        #expect(!c.isRegistered)
        #expect(rec.registered.count == renders)
        #expect(rec.enabled.count == toggles)
        #expect(rec.toasts.isEmpty)
    }

    @Test func aStatusAskedDuringACallIsSkipped() async throws {
        let bridge = try FakeBridge(status: prints(statusJSON(registered: false)), install: "sleep 0.3; \(prints(statusJSON(registered: true)))")
        let (c, _) = try await loadedController(bridge)
        c.set(registered: true)
        c.load() // the page came up again meanwhile
        try await waitUntil { c.isEnabled }
        #expect(c.isRegistered, "the install's answer is the status")
        #expect(bridge.calls == ["status --json", "install --json"])
    }

    @Test func closeDropsLateReplies() async throws {
        let bridge = try FakeBridge(status: prints(statusJSON(registered: false)), install: "sleep 0.3; \(prints(statusJSON(registered: true)))")
        let (c, rec) = try await loadedController(bridge)
        c.set(registered: true)
        #expect(!c.isEnabled)
        c.close()
        #expect(c.closed)
        let renders = rec.registered.count
        let toggles = rec.enabled.count
        try await Task.sleep(for: .milliseconds(600))
        #expect(bridge.calls == ["status --json", "install --json"], "the request itself went out")
        #expect(rec.registered.count == renders)
        #expect(rec.enabled.count == toggles)
        #expect(rec.toasts.isEmpty)
        // Nothing starts after close either.
        c.load()
        c.set(registered: false)
        try await Task.sleep(for: .milliseconds(100))
        #expect(bridge.calls.count == 2)
    }

    @Test func theReasonIsTheFirstLineOfStderrBounded() {
        #expect(MCPRegistrationController.firstLine(Data("  first line \r\nsecond\n".utf8)) == "first line")
        #expect(MCPRegistrationController.firstLine(Data()) == "")
        let long = String(repeating: "x", count: 300)
        #expect(MCPRegistrationController.firstLine(Data(long.utf8)).count == 200)
        // Cut on a character boundary: 199 ASCII bytes, then a two-byte
        // character that would be split.
        let eAcute = String(Character(Unicode.Scalar(UInt8(0xE9))))
        #expect(eAcute.utf8.count == 2)
        let edge = String(repeating: "a", count: 199) + eAcute + "tail"
        #expect(MCPRegistrationController.firstLine(Data(edge.utf8)) == String(repeating: "a", count: 199))
        let fits = String(repeating: "a", count: 198) + eAcute
        #expect(MCPRegistrationController.firstLine(Data(fits.utf8)) == fits)
        #expect(MCPRegistrationController.exitDescription(3) == "malachi-mcp exited with status 3")
        #expect(MCPRegistrationController.exitDescription(-9) == "malachi-mcp died of signal 9")
    }
}

// The process runner underneath, against the same scripts.
@Suite(.serialized) struct BridgeRunnerTests {
    @Test func collectsBothStreamsAndTheExitStatus() async throws {
        let bridge = try FakeBridge(status: "echo out; echo err >&2; exit 3")
        let r = try await BridgeRunner().run(bridge.path, ["status", "--json"], timeout: .seconds(5))
        #expect(String(decoding: r.stdout, as: UTF8.self) == "out\n")
        #expect(String(decoding: r.stderr, as: UTF8.self) == "err\n")
        #expect(r.status == 3)
        #expect(bridge.calls == ["status --json"])
    }

    @Test func outputIsCapped() async throws {
        // 3 MiB of x: the first MiB is kept, the rest read and dropped.
        let bridge = try FakeBridge(status: "head -c 3145728 /dev/zero | tr '\\0' x")
        let r = try await BridgeRunner().run(bridge.path, ["status", "--json"], timeout: .seconds(20))
        #expect(r.stdout.count == BridgeRunner.maxOutput)
        #expect(r.stdout.allSatisfy { $0 == UInt8(ascii: "x") })
        #expect(r.status == 0)
    }

    @Test func aSignalIsReportedNegative() async throws {
        let bridge = try FakeBridge(status: "kill -9 $$")
        let r = try await BridgeRunner().run(bridge.path, ["status", "--json"], timeout: .seconds(5))
        #expect(r.status == -9)
    }

    @Test func aMissingExecutableThrows() async {
        await #expect(throws: BridgeRunner.RunError.self) {
            try await BridgeRunner().run("/nonexistent/malachi-mcp", ["status", "--json"], timeout: .seconds(5))
        }
    }

    @Test func aTimeoutKillsTheProcess() async throws {
        let bridge = try FakeBridge(status: "exec sleep 30")
        let start = ContinuousClock.now
        await #expect(throws: BridgeRunner.RunError.self) {
            try await BridgeRunner().run(bridge.path, ["status", "--json"], timeout: .milliseconds(300))
        }
        #expect(ContinuousClock.now - start < .seconds(3))
    }
}
