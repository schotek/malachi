// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The assistant panel's process and locator (the In App target of
// ui/internal/assistant) against stand-in `claude` scripts: canned
// stream-json per stdin line, early exits, stderr, a process that ignores
// SIGTERM. No real Claude Code is ever run here.

private struct Timeout: Error, CustomStringConvertible {
    let line: UInt
    var description: String { "timed out waiting at line \(line)" }
}

@MainActor
func waitFor(_ timeout: Duration = .seconds(10), line: UInt = #line, _ cond: @MainActor () -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while !cond() {
        if ContinuousClock.now > deadline { throw Timeout(line: line) }
        try await Task.sleep(for: .milliseconds(10))
    }
}

/// A fresh directory for one test's scripts and records.
func assistantScratchDir() throws -> URL {
    let dir = FileManager.default.temporaryDirectory
        .appendingPathComponent("malachi-claude-\(UUID().uuidString.prefix(8))", isDirectory: true)
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    // The real path (/private/var/…), as `pwd -P` prints it.
    guard let real = realpath(dir.path, nil) else { return dir }
    defer { free(real) }
    return URL(fileURLWithPath: String(cString: real), isDirectory: true)
}

/// Writes an executable `#!/bin/sh` script.
@discardableResult
func writeScript(_ url: URL, _ body: String) throws -> String {
    try Data(("#!/bin/sh\n" + body + "\n").utf8).write(to: url)
    try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: url.path)
    return url.path
}

/// What a process reported.
@MainActor
private final class Recorded {
    var events: [Assistant.Event] = []
    var exits: [ClaudeCodeProcess.Exit] = []

    func attach(_ p: ClaudeCodeProcess) {
        p.onEvents = { [weak self] in self?.events += $0 }
        p.onExit = { [weak self] in self?.exits.append($0) }
    }

    var results: Int { events.filter { $0.kind == .result }.count }
}

private let initLine = #"{"type":"system","subtype":"init","mcp_servers":[{"name":"malachi","status":"connected"}],"tools":[]}"#

@MainActor
@Suite(.serialized) struct ClaudeCodeProcessTests {
    private func make(_ body: String, killGrace: Duration = .milliseconds(300)) throws -> (ClaudeCodeProcess, URL, Recorded) {
        try make(killGrace: killGrace) { _ in body }
    }

    /// `body` gets the test's directory.
    private func make(killGrace: Duration = .milliseconds(300), _ body: (URL) -> String) throws -> (ClaudeCodeProcess, URL, Recorded) {
        let dir = try assistantScratchDir()
        let exe = try writeScript(dir.appendingPathComponent("claude"), body(dir))
        let p = ClaudeCodeProcess(
            executable: exe, arguments: ["-p", "--verbose"], environment: ["HOME": dir.path, "PATH": "/usr/bin:/bin"],
            directory: dir, killGrace: killGrace)
        let r = Recorded()
        r.attach(p)
        return (p, dir, r)
    }

    /// Each stdin line is a turn: its events arrive in order, one process
    /// keeps them all, and the end is reported once.
    @Test func turnsArriveInOrder() async throws {
        let (p, dir, r) = try make { dir in """
            t=0
            while IFS= read -r line; do
              t=$((t+1))
              printf '%s\\n' "$line" >> '\(dir.path)/stdin'
              echo '\(initLine)'
              echo '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"turn '$t'"}}}'
              echo 'not json at all'
              echo '{"type":"assistant","message":{"content":[{"type":"text","text":"turn '$t' done"}]}}'
              echo '{"type":"result","subtype":"success","is_error":false,"result":"ok '$t'"}'
            done
            """
        }
        try p.start()
        #expect(p.running)
        for i in 1...3 {
            #expect(p.send(Assistant.userMessage("question \(i)")))
        }
        try await waitFor { r.results == 3 }
        let texts = r.events.filter { $0.kind == .text }.map(\.text)
        #expect(texts == ["turn 1 done", "turn 2 done", "turn 3 done"])
        let kinds = r.events.map(\.kind)
        #expect(Array(kinds.prefix(4)) == [.systemInit, .textDelta, .text, .result])
        let stdin = try String(contentsOf: dir.appendingPathComponent("stdin"), encoding: .utf8)
        #expect(stdin.split(separator: "\n").count == 3)
        #expect(stdin.contains("question 2"))
        #expect(r.exits.isEmpty)

        p.terminate()
        try await waitFor { !r.exits.isEmpty }
        try await Task.sleep(for: .milliseconds(100))
        #expect(r.exits.count == 1)
        #expect(!p.running)
        #expect(!p.send(Assistant.userMessage("late")))
        p.terminate() // no second report
        try await Task.sleep(for: .milliseconds(100))
        #expect(r.exits.count == 1)
    }

    /// An early exit reports the status and stderr's first line, after the
    /// events it printed.
    @Test func earlyExitReportsStderr() async throws {
        let (p, _, r) = try make("""
            echo '\(initLine)'
            echo 'Error: Invalid API key · Please run /login' >&2
            echo 'second line' >&2
            exit 3
            """)
        try p.start()
        try await waitFor { !r.exits.isEmpty }
        #expect(r.exits == [ClaudeCodeProcess.Exit(status: 3, reason: "Error: Invalid API key · Please run /login")])
        #expect(r.events.map(\.kind) == [.systemInit])
        #expect(p.ended?.status == 3)
        #expect(!p.send(Data("x".utf8)))
    }

    /// Many lines and then the exit: every event comes first.
    @Test func everyEventBeforeTheExit() async throws {
        let (p, _, r) = try make("""
            i=0
            while [ $i -lt 200 ]; do
              i=$((i+1))
              echo '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"'$i' "}}}'
            done
            """)
        try p.start()
        try await waitFor { !r.exits.isEmpty }
        #expect(r.events.count == 200)
        #expect(r.events.map(\.text) == (1...200).map { "\($0) " })
        #expect(r.exits == [ClaudeCodeProcess.Exit(status: 0, reason: "")])
        #expect(r.exits[0].description == "claude exited with status 0")
    }

    /// stderr is kept bounded, the reason is its first line cut at 200
    /// bytes.
    @Test func stderrIsBounded() async throws {
        let (p, _, r) = try make("""
            i=0
            while [ $i -lt 2000 ]; do i=$((i+1)); printf 'xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx' >&2; done
            exit 1
            """)
        try p.start()
        try await waitFor { !r.exits.isEmpty }
        #expect(r.exits[0].status == 1)
        #expect(r.exits[0].reason.utf8.count == ClaudeCodeProcess.reasonLimit)
    }

    /// A process that ignores SIGTERM is killed after the grace, and its
    /// end is still reported once.
    @Test func sigkillAfterTheGrace() async throws {
        let (p, _, r) = try make("""
            trap '' TERM
            echo '\(initLine)'
            while :; do sleep 0.1; done
            """)
        try p.start()
        try await waitFor { r.events.count == 1 }
        let started = ContinuousClock.now
        p.terminate()
        try await waitFor { !r.exits.isEmpty }
        #expect(r.exits[0].status == -9)
        #expect(ContinuousClock.now - started >= .milliseconds(250))
        try await Task.sleep(for: .milliseconds(200))
        #expect(r.exits.count == 1)
    }

    /// SIGTERM ends a process waiting for its next turn.
    @Test func sigtermEndsAWaitingProcess() async throws {
        let (p, _, r) = try make("""
            echo '\(initLine)'
            while IFS= read -r line; do :; done
            sleep 5
            """)
        try p.start()
        try await waitFor { r.events.count == 1 }
        p.terminate()
        try await waitFor(.seconds(3)) { !r.exits.isEmpty }
        // stdin closed first: the loop may end before the signal lands.
        #expect(r.exits[0].status == -15 || r.exits[0].status == 0)
    }

    /// The environment is exactly the one given, the working directory the
    /// private one.
    @Test func environmentAndDirectory() async throws {
        let dir = try assistantScratchDir()
        let exe = try writeScript(dir.appendingPathComponent("claude"), """
            env > '\(dir.path)/env'
            pwd -P > '\(dir.path)/cwd'
            """)
        let work = dir.appendingPathComponent("work", isDirectory: true)
        try FileManager.default.createDirectory(at: work, withIntermediateDirectories: true)
        let env = Assistant.childEnvironment(
            ["HOME": dir.path, "ANTHROPIC_API_KEY": "sk-test", "CLAUDECODE": "1", "LANG": "cs_CZ.UTF-8"], claudePath: exe)
        let p = ClaudeCodeProcess(executable: exe, arguments: [], environment: env, directory: work)
        let r = Recorded()
        r.attach(p)
        try p.start()
        try await waitFor { !r.exits.isEmpty }
        let seen = try String(contentsOf: dir.appendingPathComponent("env"), encoding: .utf8)
        #expect(seen.contains("HOME=\(dir.path)\n"))
        #expect(seen.contains("LANG=cs_CZ.UTF-8\n"))
        #expect(seen.contains("PATH=\(dir.path):/usr/bin:/bin:/usr/sbin:/sbin\n"))
        #expect(!seen.contains("ANTHROPIC"))
        #expect(!seen.contains("CLAUDECODE"))
        let cwd = try String(contentsOf: dir.appendingPathComponent("cwd"), encoding: .utf8)
        #expect(cwd.trimmingCharacters(in: .whitespacesAndNewlines) == work.path)
    }

    @Test func launchFailureThrows() throws {
        let dir = try assistantScratchDir()
        let p = ClaudeCodeProcess(
            executable: dir.appendingPathComponent("missing").path, arguments: [], environment: [:], directory: dir)
        #expect(throws: ClaudeCodeProcess.StartError.self) { try p.start() }
        #expect(!p.running)
        #expect(!p.send(Data("x".utf8)))
    }
}

@MainActor
@Suite(.serialized) struct ClaudeCodeLocatorTests {
    /// Only paths inside the test's directory count, so a Claude Code
    /// installed on this computer never answers.
    private func locator(_ dir: URL, _ settings: Settings, env: [String: String]? = nil) -> ClaudeCodeLocator {
        let prefix = dir.path + "/"
        return ClaudeCodeLocator(
            settings: settings, environment: env ?? ["HOME": dir.path, "PATH": ""], timeout: .seconds(5), directory: dir,
            usable: { $0.hasPrefix(prefix) && ClaudeCodeLocator.isExecutableFile($0) })
    }

    @Test func theSettingComesFirst() throws {
        let scratch = ScratchSettings()
        let dir = try assistantScratchDir()
        let home = dir.appendingPathComponent("home", isDirectory: true)
        try FileManager.default.createDirectory(at: home.appendingPathComponent(".local/bin"), withIntermediateDirectories: true)
        let native = try writeScript(home.appendingPathComponent(".local/bin/claude"), "exit 0")
        let chosen = try writeScript(dir.appendingPathComponent("my-claude"), "exit 0")
        let l = locator(dir, scratch.settings, env: ["HOME": home.path, "PATH": ""])
        #expect(l.locate() == native)
        scratch.settings.assistantClaudePath = chosen
        #expect(l.locate() == chosen)
        #expect(l.candidates().first == chosen)
        // A chosen path that is not an executable file falls back.
        let plain = dir.appendingPathComponent("not-executable")
        try Data("x".utf8).write(to: plain)
        scratch.settings.assistantClaudePath = plain.path
        #expect(l.locate() == native)
        scratch.settings.assistantClaudePath = dir.path
        #expect(l.locate() == native)
        scratch.settings.assistantClaudePath = "relative/claude"
        #expect(l.locate() == native)
    }

    @Test func linksCountByTheirTargetAndKeepTheirPath() throws {
        let scratch = ScratchSettings()
        let dir = try assistantScratchDir()
        let nvmBin = dir.appendingPathComponent("home/.nvm/versions/node/v20.19.0/bin", isDirectory: true)
        let lib = dir.appendingPathComponent("home/.nvm/versions/node/v20.19.0/lib", isDirectory: true)
        try FileManager.default.createDirectory(at: nvmBin, withIntermediateDirectories: true)
        try FileManager.default.createDirectory(at: lib, withIntermediateDirectories: true)
        let target = try writeScript(lib.appendingPathComponent("cli.js"), "exit 0")
        let link = nvmBin.appendingPathComponent("claude")
        try FileManager.default.createSymbolicLink(atPath: link.path, withDestinationPath: target)
        let l = locator(dir, scratch.settings, env: ["HOME": dir.appendingPathComponent("home").path, "PATH": ""])
        #expect(l.locate() == link.path)
        // A dangling link is nothing.
        try FileManager.default.removeItem(atPath: target)
        #expect(l.locate() == nil)
    }

    @Test func nothingFound() async throws {
        let scratch = ScratchSettings()
        let dir = try assistantScratchDir()
        let l = locator(dir, scratch.settings)
        #expect(l.locate() == nil)
        #expect(await l.version() == nil)
        #expect(await l.signedIn() == nil)
    }

    /// `--version` and `auth status --json` run once each and are kept
    /// until `refresh()`.
    @Test func versionAndSignInAreAskedOnce() async throws {
        let scratch = ScratchSettings()
        let dir = try assistantScratchDir()
        let state = dir.appendingPathComponent("logged-in")
        try Data("true".utf8).write(to: state)
        let exe = try writeScript(dir.appendingPathComponent("claude"), """
            echo "$*" >> '\(dir.path)/calls'
            case "$1" in
            --version) echo '2.1.178 (Claude Code)'; echo 'more'; exit 0;;
            auth) printf '{"loggedIn": %s, "authMethod": "claude.ai", "email": "me@example.invalid"}\\n' "$(cat '\(state.path)')"; exit 0;;
            esac
            exit 2
            """)
        scratch.settings.assistantClaudePath = exe
        let l = locator(dir, scratch.settings)
        #expect(await l.version() == "2.1.178 (Claude Code)")
        #expect(await l.version() == "2.1.178 (Claude Code)")
        #expect(await l.signedIn() == true)
        #expect(await l.signedIn() == true)
        func calls() throws -> [String] {
            try String(contentsOf: dir.appendingPathComponent("calls"), encoding: .utf8).split(separator: "\n").map(String.init)
        }
        #expect(try calls() == ["--version", "auth status --json"])
        try Data("false".utf8).write(to: state)
        #expect(await l.signedIn() == true, "kept")
        l.refresh()
        #expect(await l.signedIn() == false)
        #expect(try calls().count == 3)
    }

    @Test func failedRunsAreUnknown() async throws {
        let scratch = ScratchSettings()
        let dir = try assistantScratchDir()
        let exe = try writeScript(dir.appendingPathComponent("claude"), """
            case "$1" in
            --version) echo 'broken' >&2; exit 1;;
            auth) echo 'Not logged in'; exit 1;;
            esac
            """)
        scratch.settings.assistantClaudePath = exe
        let l = locator(dir, scratch.settings)
        #expect(await l.version() == nil)
        #expect(await l.signedIn() == nil)

        // Signed out may exit non-zero: the JSON counts.
        let out = try writeScript(dir.appendingPathComponent("claude-out"), """
            echo '{"loggedIn": false}'
            exit 1
            """)
        scratch.settings.assistantClaudePath = out
        #expect(await l.signedIn() == false)
    }
}
