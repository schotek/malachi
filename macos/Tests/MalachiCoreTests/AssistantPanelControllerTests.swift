// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The assistant panel's state machine (the In App target of
// ui/internal/assistant) against a stand-in `claude`: a script that
// answers `--version` and `auth status --json`, runs a scripted `auth
// login`, records every start, its arguments, environment and stdin, and
// prints a canned turn (stream-json lines, or a shell snippet) per stdin
// line. The Go counterpart is
// ui/internal/assistantpanel controller_test.go, the same fake.

/// One turn of the fake: JSON lines, then an optional shell snippet (a
/// `sleep` that keeps the turn open, an exit).
struct FakeTurn {
    var lines: [String]
    var shell = ""
}

/// The stand-in `claude` and what it recorded.
struct FakeClaude {
    let dir: URL
    let path: String

    /// `auth login` is counted in `logins`, records its environment in
    /// `loginEnv` and runs `login.sh` when there is one (`login(_:)`:
    /// `signsIn`, or the test's own), else it ends with status 0.
    ///
    /// - Parameters:
    ///   - loggedIn: what `auth status --json` says ("true", "false")
    ///     until a sign-in leaves the file `signed-in`.
    ///   - onStart: shell run at every start of a conversation, with `$n`
    ///     the start's number (to fail the first, say).
    ///   - turns: the turns in order, counted over every start (the
    ///     second question of a new process is turn 2); the last repeats.
    init(loggedIn: String = "true", onStart: String = "", turns: [FakeTurn]) throws {
        dir = try assistantScratchDir()
        for (i, t) in turns.enumerated() {
            let body = "cat <<'MALACHI_EOF'\n" + t.lines.joined(separator: "\n") + "\nMALACHI_EOF\n" + t.shell + "\n"
            try Data(body.utf8).write(to: dir.appendingPathComponent("turn\(i + 1).sh"))
        }
        let d = dir.path
        path = try writeScript(dir.appendingPathComponent("claude"), """
            D='\(d)'
            case "$1" in
            --version) echo '2.1.178 (Claude Code)'; exit 0;;
            auth)
              if [ "$2" = login ]; then
                echo login >> "$D/logins"
                env > "$D/login-env"
                if [ -f "$D/login.sh" ]; then . "$D/login.sh"; fi
                exit 0
              fi
              if [ -f "$D/signed-in" ]; then echo '{"loggedIn": true}'; else echo '{"loggedIn": \(loggedIn)}'; fi
              exit 0;;
            esac
            echo start >> "$D/starts"
            n=$(wc -l < "$D/starts" | tr -d ' ')
            : > "$D/args"
            for a in "$@"; do printf '%s\\n' "$a" >> "$D/args"; done
            env > "$D/env"
            pwd -P > "$D/cwd"
            \(onStart)
            last=\(turns.count)
            while IFS= read -r line; do
              printf '%s\\n' "$line" >> "$D/stdin"
              k=$(wc -l < "$D/stdin" | tr -d ' ')
              [ $k -gt $last ] && k=$last
              . "$D/turn$k.sh"
            done
            """)
    }

    private func read(_ name: String) -> String {
        (try? String(contentsOf: dir.appendingPathComponent(name), encoding: .utf8)) ?? ""
    }

    /// What `auth login` does, as shell (`$D` is the fake's directory).
    func login(_ shell: String) throws {
        try Data((shell + "\n").utf8).write(to: dir.appendingPathComponent("login.sh"))
    }

    var logins: Int { read("logins").split(separator: "\n").count }
    /// The environment of the last `auth login`.
    var loginEnv: String { read("login-env") }
    var starts: Int { read("starts").split(separator: "\n").count }
    var args: [String] { read("args").split(separator: "\n", omittingEmptySubsequences: false).dropLast().map(String.init) }
    var env: String { read("env") }
    var cwd: String { read("cwd").trimmingCharacters(in: .whitespacesAndNewlines) }

    /// The text of every turn written to stdin.
    var prompts: [String] {
        read("stdin").split(separator: "\n").compactMap { line in
            guard let obj = try? JSONSerialization.jsonObject(with: Data(line.utf8)) as? [String: Any],
                  let message = obj["message"] as? [String: Any],
                  let content = message["content"] as? [[String: Any]] else { return nil }
            return content.first?["text"] as? String
        }
    }
}

// Canned stream-json.
let fakeInit = #"{"type":"system","subtype":"init","mcp_servers":[{"name":"malachi","status":"connected"}],"tools":["mcp__malachi__read_message"],"model":"claude-sonnet"}"#
let fakeInitFailed = #"{"type":"system","subtype":"init","mcp_servers":[{"name":"malachi","status":"failed"}],"tools":[]}"#
func fakeDelta(_ t: String) -> String {
    #"{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"\#(t)"}}}"#
}
func fakeText(_ t: String) -> String {
    #"{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"\#(t)"}]}}"#
}
func fakeToolUse(_ id: String, _ tool: String) -> String {
    #"{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"\#(id)","name":"mcp__malachi__\#(tool)","input":{}}]}}"#
}
func fakeToolResult(_ id: String, _ text: String, error: Bool = false) -> String {
    #"{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"\#(id)","is_error":\#(error),"content":[{"type":"text","text":"\#(text)"}]}]}}"#
}
/// The message Claude Code writes itself when the API refused the turn; the
/// result repeats its text.
func fakeFailure(_ failure: String, _ text: String) -> String {
    #"{"type":"assistant","message":{"model":"<synthetic>","role":"assistant","content":[{"type":"text","text":"\#(text)"}]},"error":"\#(failure)"}"#
}
func fakeResult(_ text: String = "done", success: Bool = true) -> String {
    #"{"type":"result","subtype":"success","is_error":\#(!success),"result":"\#(text)","total_cost_usd":0.01}"#
}

/// A plain answer: init, two deltas, the whole text, the result.
func answerTurn(_ text: String) -> FakeTurn {
    let half = text.count / 2
    return FakeTurn(lines: [
        fakeInit, fakeDelta(String(text.prefix(half))), fakeDelta(String(text.dropFirst(half))), fakeText(text), fakeResult(),
    ])
}

/// A sign-in that works: `auth status` says loggedIn from then on.
let signsIn = #": > "$D/signed-in""#

private typealias C = AssistantPanelController.Content

/// The panel's lines of Claude Code's sign-in.
private let signedOut = "Claude Code is not signed in"
private let waiting = "Waiting for the sign-in in your browser…"

@MainActor
private final class PanelHarness {
    let scratch = ScratchSettings()
    let fake: FakeClaude
    let panel: AssistantPanelController
    let work: URL
    var consentAsked = 0
    var consentAnswer = true
    var restored: [String] = []
    var focused = 0
    var changes: [AssistantPanelController.Change] = []
    var opened: [Assistant.DraftRef] = []

    init(fake: FakeClaude, consent: Bool = true, bridge: String? = "/Applications/Malachi Mail.app/Contents/MacOS/malachi-mcp") throws {
        self.fake = fake
        scratch.settings.assistantClaudePath = fake.path
        scratch.settings.assistantConsent = consent
        work = fake.dir.appendingPathComponent("work", isDirectory: true)
        let prefix = fake.dir.path + "/"
        let locator = ClaudeCodeLocator(
            settings: scratch.settings,
            environment: [
                "HOME": fake.dir.path, "DISPLAY": ":7", "ANTHROPIC_API_KEY": "sk-never", "MALACHI_SOCKET": "/tmp/s.sock",
            ],
            timeout: .seconds(5),
            usable: { $0.hasPrefix(prefix) && ClaudeCodeLocator.isExecutableFile($0) })
        panel = AssistantPanelController(
            settings: scratch.settings, locator: locator, bridge: bridge, socket: "/tmp/malachi-test.sock", directory: work,
            environment: ["HOME": fake.dir.path, "LANG": "cs_CZ.UTF-8", "ANTHROPIC_API_KEY": "sk-never"],
            killGrace: .milliseconds(300))
        panel.today = { "2026-09-29" }
        panel.language = { "Czech" }
        panel.consent = { [unowned self] in
            self.consentAsked += 1
            return self.consentAnswer
        }
        panel.onRestoreInput = { [unowned self] in self.restored.append($0) }
        panel.onFocusInput = { [unowned self] in self.focused += 1 }
        panel.onChange = { [unowned self] in self.changes.append($0) }
        panel.openDraft = { [unowned self] in self.opened.append($0) }
    }

    var contents: [C] { panel.items.map(\.content) }

    func idle() async throws {
        try await waitFor { self.panel.phase == .idle }
    }

    /// Waits until the turn under way ended.
    func turn() async throws {
        try await waitFor { self.panel.phase != .idle }
        try await idle()
    }

    func stop() {
        panel.close()
    }
}

private let one = AssistantPanelController.Context(selection: Assistant.Selection(accountID: "a", messageIDs: ["m1"]))

/// One message of account "a" as the panel's context.
private func message(_ id: String, subject: String = "", thread: String = "") -> AssistantPanelController.Context {
    AssistantPanelController.Context(
        selection: Assistant.Selection(accountID: "a", messageIDs: [id]), subject: subject, threadID: thread)
}

/// A conversation of account "a" as the panel's context: `ids` newest
/// first, only the newest when `partial`.
private func folded(
    _ ids: [String], count: Int, partial: Bool = false, subject: String = "", thread: String = ""
) -> AssistantPanelController.Context {
    AssistantPanelController.Context(
        selection: Assistant.Selection(accountID: "a", messageIDs: ids), count: count, partial: partial,
        subject: subject, threadID: thread)
}

@MainActor
@Suite(.serialized) struct AssistantPanelControllerTests {
    /// Summarize on the selected message: consent asked once and kept, the
    /// question, the tool line, the streamed answer replaced by the whole
    /// text; one process with the command line of `Assistant.args`, the
    /// child's environment and the private directory.
    @Test func summarizeRunsATurn() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [
            fakeInit, fakeToolUse("t1", "read_message"), fakeToolResult("t1", "From: someone"),
            fakeDelta("Hello **wor"), fakeDelta("ld**"), fakeText("Hello **world**"), fakeResult(),
        ])])
        let h = try PanelHarness(fake: fake, consent: false)
        defer { h.stop() }
        h.panel.setContext(one)
        #expect(h.panel.canRunActions)
        h.panel.run(.summarize)
        #expect(h.panel.phase == .preparing)
        #expect(!h.panel.canRunActions)
        try await h.turn()
        #expect(h.consentAsked == 1)
        #expect(h.scratch.settings.assistantConsent)
        #expect(h.contents == [
            .user(label: "Summarize", text: ""),
            .activity(label: "Reading a message…", done: true),
            .assistant(text: "Hello **world**", streaming: false),
        ])
        #expect(fake.starts == 1)
        #expect(fake.prompts == [try Assistant.prompt(.app, .summarize, one.selection)])
        let args = fake.args
        #expect(args == Assistant.args(Assistant.Options(
            bridge: "/Applications/Malachi Mail.app/Contents/MacOS/malachi-mcp", socket: "/tmp/malachi-test.sock",
            model: .sonnet, systemPrompt: Assistant.systemPrompt(language: "Czech", today: "2026-09-29"))))
        #expect(fake.env.contains("LANG=cs_CZ.UTF-8"))
        #expect(!fake.env.contains("ANTHROPIC"))
        #expect(fake.env.contains("PATH=\(fake.dir.path):/usr/bin:/bin:/usr/sbin:/sbin"))
        #expect(fake.cwd == h.work.path)
        let mode = try FileManager.default.attributesOfItem(atPath: h.work.path)[.posixPermissions] as? Int
        #expect(mode == 0o700)

        // A follow-up goes to the same process as it is: Summarize's prompt
        // named the message already.
        #expect(h.panel.submit("  And what is still open?  "))
        try await h.turn()
        #expect(fake.starts == 1)
        #expect(fake.prompts.count == 2)
        #expect(fake.prompts[1] == "And what is still open?")
        #expect(h.consentAsked == 1)
        #expect(Array(h.contents.suffix(3)) == [
            .user(label: "", text: "And what is still open?"),
            .activity(label: "Reading a message…", done: true),
            .assistant(text: "Hello **world**", streaming: false),
        ])
    }

    /// Without a context a question goes as it is; empty text is refused.
    @Test func freeQuestionWithoutContext() async throws {
        let fake = try FakeClaude(turns: [answerTurn("Two unread.")])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        #expect(!h.panel.submit("   "))
        #expect(h.panel.contextLabel == "All mail")
        #expect(!h.panel.canRunActions)
        h.panel.run(.summarize) // needs a context
        #expect(h.panel.phase == .idle)
        #expect(h.panel.submit("How many unread?"))
        #expect(!h.panel.submit("again")) // one at a time
        try await h.turn()
        #expect(fake.prompts == ["How many unread?"])
        #expect(h.consentAsked == 0)
        #expect(h.contents.last == .assistant(text: "Two unread.", streaming: false))
    }

    /// Consent declined: nothing is sent or started, the text goes back.
    @Test func consentDeclined() async throws {
        let fake = try FakeClaude(turns: [answerTurn("x")])
        let h = try PanelHarness(fake: fake, consent: false)
        defer { h.stop() }
        h.consentAnswer = false
        #expect(h.panel.submit("Summarize my week"))
        try await waitFor { h.consentAsked == 1 && h.panel.phase == .idle }
        #expect(h.restored == ["Summarize my week"])
        #expect(h.panel.items.isEmpty)
        #expect(!h.scratch.settings.assistantConsent)
        try await Task.sleep(for: .milliseconds(100))
        #expect(fake.starts == 0)
    }

    /// Draft a Reply… waits for the words; the draft card opens through the
    /// application.
    @Test func draftReplyAndOpenDraft() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [
            fakeInit, fakeToolUse("t1", "read_message"), fakeToolResult("t1", "…"),
            fakeToolUse("t2", "create_draft"),
            fakeToolResult("t2", #"draft d_9 (version 1) stored in account a; it is NOT sent.\n\nTo: x"#),
            fakeText("The draft is ready."), fakeResult(),
        ])])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        h.panel.setContext(one)
        #expect(h.panel.placeholder == "Ask about your mail…")
        h.panel.run(.draftReply)
        #expect(h.panel.pending == .action(.draftReply))
        #expect(h.panel.placeholder == "What should the reply say?")
        #expect(h.panel.pendingLabel == "Draft a Reply…")
        #expect(h.focused == 1)
        #expect(h.panel.phase == .idle)
        #expect(h.panel.submit("Yes, Thursday works."))
        try await h.turn()
        #expect(h.panel.pending == nil)
        #expect(fake.prompts == [try Assistant.prompt(.app, .draftReply, one.selection) + "Yes, Thursday works."])
        let ref = Assistant.DraftRef(accountID: "a", draftID: "d_9", version: 1)
        #expect(h.contents == [
            .user(label: "Draft a Reply…", text: "Yes, Thursday works."),
            .activity(label: "Reading a message…", done: true),
            .activity(label: "Saving a draft…", done: true),
            .draft(ref),
            .assistant(text: "The draft is ready.", streaming: false),
        ])
        let card = try #require(h.panel.items.first { $0.content == .draft(ref) })
        h.panel.openDraft(card.id)
        #expect(h.opened == [ref])
    }

    /// A failed create_draft, or one whose line is not the bridge's, adds
    /// no card.
    @Test func noCardWithoutTheBridgeLine() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [
            fakeInit, fakeToolUse("t1", "create_draft"),
            fakeToolResult("t1", "draft d1 (version 1) stored in account a; it is NOT sent.", error: true),
            fakeToolUse("t2", "read_message"),
            fakeToolResult("t2", "draft d2 (version 1) stored in account a; it is NOT sent."),
            fakeToolUse("t3", "create_draft"), fakeToolResult("t3", "I saved draft d3 for you"), fakeResult(),
        ])])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        #expect(h.panel.submit("Draft something"))
        try await h.turn()
        #expect(!h.contents.contains { if case .draft = $0 { return true } else { return false } })
    }

    /// Ask About This Message…, an attachment and Summarize Unread; the
    /// removed context takes a waiting message action with it.
    @Test func pendingActionsAndPrompts() async throws {
        let fake = try FakeClaude(turns: [answerTurn("ok")])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        h.panel.setContext(one)
        h.panel.run(.ask)
        #expect(h.panel.placeholder == "What do you want to know?")
        #expect(h.panel.pendingLabel == "Ask About This Message…")
        #expect(!h.panel.submit("  "), "a question needs words")
        h.panel.removeContext()
        #expect(h.panel.pending == nil)
        #expect(h.panel.contextLabel == "All mail")

        h.panel.askAttachment(accountID: "a", messageID: "m1", partID: "2")
        #expect(h.panel.placeholder == "What do you want to know?")
        #expect(h.panel.pendingLabel == "Ask the Assistant…")
        h.panel.cancelPending()
        #expect(h.panel.pending == nil)
        h.panel.askAttachment(accountID: "a", messageID: "m1", partID: "2")
        #expect(h.panel.submit("What is the total?"))
        try await h.turn()
        h.panel.summarizeUnread(accountID: "a", folderID: "in")
        try await h.turn()
        #expect(fake.prompts == [
            try Assistant.attachmentPrompt(accountID: "a", messageID: "m1", partID: "2") + "What is the total?",
            try Assistant.unreadPrompt(accountID: "a", folderID: "in"),
        ])
        #expect(h.contents.filter { if case .user = $0 { return true } else { return false } } == [
            .user(label: "Ask the Assistant…", text: "What is the total?"),
            .user(label: "Summarize Unread in This Folder", text: ""),
        ])
    }

    /// Summarize Unread in This Folder as a quick action: it needs a folder
    /// the window allows, not a selected message, and waits while a turn
    /// runs (TestCanSummarizeUnread).
    @Test func canSummarizeUnread() async throws {
        let fake = try FakeClaude(turns: [answerTurn("ok")])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        #expect(h.panel.canSummarizeUnread(folder: true))
        #expect(!h.panel.canRunActions)
        #expect(!h.panel.canSummarizeUnread(folder: false))
        h.panel.summarizeUnread(accountID: "a", folderID: "in")
        #expect(!h.panel.canSummarizeUnread(folder: true))
        try await h.turn()
        #expect(h.panel.canSummarizeUnread(folder: true))
        h.panel.close()
        #expect(!h.panel.canSummarizeUnread(folder: true))
    }

    /// The context follows the selection; a removed context comes back
    /// with the next selection; the chip's text.
    @Test func contextFollowsTheSelection() throws {
        let fake = try FakeClaude(turns: [answerTurn("ok")])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        var states = 0
        h.panel.onState = { states += 1 }
        #expect(h.panel.contextLabel == "All mail")
        h.panel.setContext(one)
        #expect(h.panel.contextLabel == "Selected message")
        let conv = AssistantPanelController.Context(
            selection: Assistant.Selection(accountID: "a", messageIDs: ["m3"]), count: 3, partial: true)
        h.panel.setContext(conv)
        #expect(h.panel.contextLabel == "Selected conversation (3 messages)")
        h.panel.removeContext()
        #expect(h.panel.effectiveContext == nil && h.panel.contextLabel == "All mail")
        h.panel.setContext(conv)
        #expect(h.panel.effectiveContext == conv)
        h.panel.setContext(nil)
        #expect(h.panel.contextLabel == "All mail")
        #expect(states == 5)
    }

    /// A folded conversation's members are asked for when the question is
    /// sent, once; without an answer the newest message alone goes.
    @Test func partialContextIsResolved() async throws {
        let fake = try FakeClaude(turns: [answerTurn("ok")])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        let conv = AssistantPanelController.Context(
            selection: Assistant.Selection(accountID: "a", messageIDs: ["m3"]), count: 3, partial: true)
        var asked: [AssistantPanelController.Context] = []
        h.panel.resolveContext = { c, done in
            asked.append(c)
            done(Assistant.Selection(accountID: "a", messageIDs: ["m3", "m2", "m1"]))
        }
        h.panel.setContext(conv)
        h.panel.run(.tasks)
        try await h.turn()
        #expect(asked == [conv])
        let members = Assistant.Selection(accountID: "a", messageIDs: ["m3", "m2", "m1"])
        #expect(fake.prompts == [try Assistant.prompt(.app, .tasks, members)])
        // The pinned conversation keeps its members.
        #expect(h.panel.pinned.first?.context?.selection == members)
        #expect(h.panel.pinned.first?.context?.partial == false)
        h.panel.run(.summarize)
        try await h.turn()
        #expect(fake.prompts.last == (try Assistant.prompt(.app, .summarize, members)))
        #expect(asked == [conv])

        h.panel.newConversation()
        h.panel.resolveTimeout = .milliseconds(100)
        h.panel.resolveContext = { _, _ in } // never answers
        h.panel.run(.summarize)
        try await h.turn()
        #expect(fake.prompts.last == (try Assistant.prompt(.app, .summarize, conv.selection)))
    }

    /// No Claude Code: an error with Get Claude Code… and Try Again,
    /// nothing started.
    @Test func claudeNotFound() async throws {
        let fake = try FakeClaude(turns: [answerTurn("ok")])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        try FileManager.default.removeItem(atPath: fake.path)
        #expect(h.panel.submit("Hello"))
        try await h.turn()
        #expect(h.contents == [
            .user(label: "", text: "Hello"),
            .error("Claude Code was not found on this computer", retry: true, offer: .install),
        ])
    }

    /// Signed out: an error with Sign In…, which runs Claude Code's sign-in
    /// and then sends the same question, without a second bubble.
    @Test func notSignedInSignsIn() async throws {
        let fake = try FakeClaude(loggedIn: "false", turns: [answerTurn("Hi there")])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        #expect(h.panel.submit("Hello"))
        try await h.turn()
        #expect(h.contents == [.user(label: "", text: "Hello"), .error(signedOut, retry: false, offer: .signIn)])
        #expect(fake.starts == 0 && fake.logins == 0)
        // Try Again is not what the line offers.
        h.panel.retry(h.panel.items[1].id)
        #expect(h.panel.phase == .idle, "Try Again ran on a line that offers Sign In…")
        try fake.login(signsIn)
        h.panel.signIn(h.panel.items[1].id)
        try await h.turn()
        #expect(h.contents == [
            .user(label: "", text: "Hello"),
            .error(signedOut, retry: false),
            .activity(label: waiting, done: true),
            .assistant(text: "Hi there", streaming: false),
        ])
        #expect(fake.logins == 1 && fake.starts == 1)
        #expect(fake.prompts == ["Hello"])
        // The sign-in opens the browser: it has the desktop session, and
        // still nothing of a surrounding Claude or of Malachi Mail.
        let env = fake.loginEnv
        #expect(env.contains("DISPLAY=:7\n"))
        #expect(!env.contains("ANTHROPIC") && !env.contains("MALACHI"), "sign-in environment:\n\(env)")
        // A button used up does nothing more.
        h.panel.signIn(h.panel.items[1].id)
        #expect(h.panel.phase == .idle && fake.logins == 1, "a second Sign In… on the same line ran")
    }

    /// A sign-in that ends badly: its reason, and Sign In… again.
    @Test func signInFails() async throws {
        let fake = try FakeClaude(loggedIn: "false", turns: [answerTurn("Hi there")])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        #expect(h.panel.submit("Hello"))
        try await h.turn()
        try fake.login("""
            echo 'Login failed: the browser said no' >&2
            echo 'more' >&2
            exit 1
            """)
        h.panel.signIn(h.panel.items[1].id)
        try await h.turn()
        #expect(h.contents == [
            .user(label: "", text: "Hello"),
            .error(signedOut, retry: false),
            .activity(label: waiting, done: true),
            .error("The sign-in failed: Login failed: the browser said no", retry: false, offer: .signIn),
        ])
        #expect(fake.starts == 0)
        // Once more, and it works.
        try fake.login(signsIn)
        h.panel.signIn(h.panel.items[3].id)
        try await h.turn()
        #expect(h.contents.last == .assistant(text: "Hi there", streaming: false))
        #expect(fake.logins == 2)
    }

    /// The browser brings no answer in time: the sign-in is ended.
    @Test func signInTimesOut() async throws {
        let fake = try FakeClaude(loggedIn: "false", turns: [answerTurn("Hi there")])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        h.panel.locator.signInTimeout = .milliseconds(300)
        #expect(h.panel.submit("Hello"))
        try await h.turn()
        try fake.login("exec sleep 30")
        h.panel.signIn(h.panel.items[1].id)
        try await h.turn()
        #expect(h.contents.last == .error("The sign-in took too long; try again", retry: false, offer: .signIn))
        #expect(!h.panel.locator.signingIn)
    }

    /// Stop ends a sign-in like any turn; one the settings start takes its
    /// place.
    @Test func signInStoppedAndReplaced() async throws {
        let fake = try FakeClaude(loggedIn: "false", turns: [answerTurn("Hi there")])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        #expect(h.panel.submit("Hello"))
        try await h.turn()
        try fake.login("exec sleep 30")
        h.panel.signIn(h.panel.items[1].id)
        try await waitFor { h.panel.locator.signingIn }
        #expect(h.contents == [
            .user(label: "", text: "Hello"),
            .error(signedOut, retry: false),
            .activity(label: waiting, done: false),
        ])
        h.panel.stop()
        try await Task.sleep(for: .milliseconds(100))
        #expect(h.contents == [
            .user(label: "", text: "Hello"),
            .error(signedOut, retry: false),
            .activity(label: waiting, done: true),
            .note("The conversation was stopped"),
        ])
        #expect(!h.panel.locator.signingIn && h.panel.phase == .idle)

        // The same question again: signed out, Sign In…, and the settings'
        // sign-in takes over while the browser is open.
        h.panel.newConversation()
        #expect(h.panel.submit("Hello"))
        try await h.turn()
        h.panel.signIn(h.panel.items[1].id)
        try await waitFor { h.panel.locator.signingIn }
        try fake.login(signsIn)
        let settings = h.panel.locator.startSignIn()
        try await h.turn()
        #expect(await settings.result == .done)
        #expect(h.contents == [
            .user(label: "", text: "Hello"),
            .error(signedOut, retry: false),
            .activity(label: waiting, done: true),
            .error(signedOut, retry: false, offer: .signIn),
        ])
    }

    /// The API refuses the sign-in although auth status says loggedIn
    /// (expired, revoked): Claude Code's own message is no answer, the line
    /// offers Sign In…, and the question goes to a new process after it.
    @Test func refusedSignInOffersSignIn() async throws {
        let refused = "Failed to authenticate. API Error: 401"
        let fake = try FakeClaude(turns: [
            FakeTurn(lines: [fakeInit, fakeFailure("authentication_failed", refused), fakeResult(refused, success: false)]),
            answerTurn("Hi there"),
        ])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        #expect(h.panel.submit("Hello"))
        try await h.turn()
        #expect(h.contents == [.user(label: "", text: "Hello"), .error(signedOut, retry: false, offer: .signIn)])
        #expect(h.panel.process == nil)
        try fake.login(signsIn)
        h.panel.signIn(h.panel.items[1].id)
        try await h.turn()
        #expect(h.contents == [
            .user(label: "", text: "Hello"),
            .error(signedOut, retry: false),
            .activity(label: waiting, done: true),
            .assistant(text: "Hi there", streaming: false),
        ])
        #expect(fake.logins == 1 && fake.starts == 2)
        #expect(fake.prompts == ["Hello", "Hello"])
    }

    /// TestWaiting: the panel waits where nothing in the transcript shows
    /// the work: from the question until a tool is at work, from its result
    /// until the answer streams, and after a text block until the result;
    /// never while idle. (Not named `waiting`, which would hide the sign-in
    /// line's text in the other tests.)
    @Test func waitsWhereNothingShowsTheWork() async throws {
        let fake = try FakeClaude(turns: [
            FakeTurn(lines: [
                fakeInit, fakeToolUse("t1", "read_message"), fakeToolResult("t1", "From: someone"),
                fakeDelta("Hi"), fakeText("Hi"), fakeResult(),
            ]),
        ])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        var seen = [h.panel.waiting]
        let record = {
            let w = h.panel.waiting
            if seen.last != w {
                seen.append(w)
            }
        }
        h.panel.onChange = { _ in record() }
        h.panel.onState = { record() }
        #expect(h.panel.submit("Hello"))
        try await h.turn()
        #expect(seen == [false, true, false, true, false, true, false])
    }

    /// TestRefreshFailedOffersRetryAndSignIn: Claude Code could not refresh
    /// its sign-in (another Claude Code held the refresh lock): its words
    /// are said whole once, the line offers Try Again and Sign In…, and
    /// either goes to a new process and takes both buttons.
    @Test func refreshFailedOffersRetryAndSignIn() async throws {
        // Claude Code 2.1.284, 215 bytes.
        let words = "Failed to refresh OAuth token: another Claude Code process is refreshing it or exited mid-refresh. This is usually transient; retry in a minute, and if it persists close other Claude Code processes or sign in again."
        let stopped = "The assistant stopped: " + words
        let refresh = FakeTurn(lines: [fakeInit, fakeFailure("server_error", words), fakeResult(words, success: false)])
        let fake = try FakeClaude(turns: [refresh, answerTurn("Hi there"), refresh, answerTurn("Signed in")])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        #expect(h.panel.submit("Hello"))
        try await h.turn()
        #expect(h.contents == [.user(label: "", text: "Hello"), .error(stopped, retry: true, offer: .signIn)])
        #expect(h.panel.process == nil)
        h.panel.retry(h.panel.items[1].id)
        try await h.turn()
        #expect(h.contents == [
            .user(label: "", text: "Hello"),
            .error(stopped, retry: false),
            .assistant(text: "Hi there", streaming: false),
        ])
        #expect(fake.starts == 2)

        #expect(h.panel.submit("Again"))
        try await h.turn()
        try fake.login(signsIn)
        h.panel.signIn(h.panel.items[4].id)
        try await h.turn()
        #expect(Array(h.contents[3...]) == [
            .user(label: "", text: "Again"),
            .error(stopped, retry: false),
            .activity(label: waiting, done: true),
            .assistant(text: "Signed in", streaming: false),
        ])
        #expect(fake.logins == 1 && fake.starts == 3)
        #expect(fake.prompts == ["Hello", "Hello", "Again", "Again"])
    }

    /// Another refusal of the API is said once, by the result.
    @Test func refusedTurnIsSaidOnce() async throws {
        let limit = "API Error: Rate limit reached"
        let fake = try FakeClaude(turns: [
            FakeTurn(lines: [fakeInit, fakeFailure("rate_limit", limit), fakeResult(limit, success: false)]),
        ])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        #expect(h.panel.submit("Hello"))
        try await h.turn()
        #expect(h.contents == [
            .user(label: "", text: "Hello"),
            .error("The assistant stopped: " + limit, retry: true),
        ])
    }

    /// No bridge beside the application: the tools are missing.
    @Test func noBridge() async throws {
        let fake = try FakeClaude(turns: [answerTurn("ok")])
        let h = try PanelHarness(fake: fake, bridge: nil)
        defer { h.stop() }
        #expect(h.panel.submit("Hello"))
        try await h.turn()
        #expect(h.contents.last == .error("The Malachi Mail tools are not available to the assistant", retry: false))
        #expect(fake.starts == 0)
    }

    /// The bridge not connected in Claude Code: the conversation ends, the
    /// next question starts a new process.
    @Test func bridgeNotConnected() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [fakeInitFailed, fakeText("I cannot"), fakeResult()]), answerTurn("fine")])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        #expect(h.panel.submit("Hello"))
        try await h.turn()
        #expect(h.contents == [
            .user(label: "", text: "Hello"),
            .error("The Malachi Mail tools are not available to the assistant", retry: false),
        ])
        #expect(h.panel.process == nil)
        try await waitFor { fake.starts == 1 }
        #expect(h.panel.submit("Again"))
        try await h.turn()
        #expect(fake.starts == 2)
    }

    /// Stop during a turn: the note, the streamed text kept and closed,
    /// the process gone; the next question starts a new one.
    @Test func stopEndsTheTurn() async throws {
        let fake = try FakeClaude(turns: [
            FakeTurn(lines: [fakeInit, fakeToolUse("t1", "search_messages"), fakeDelta("Looking")], shell: "sleep 5"),
            answerTurn("Fresh start"),
        ])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        #expect(h.panel.submit("Find the invoice"))
        try await waitFor { h.contents.last == .assistant(text: "Looking", streaming: true) }
        #expect(h.panel.phase == .running)
        h.panel.stop()
        #expect(h.panel.phase == .idle)
        #expect(h.panel.process == nil)
        #expect(h.contents == [
            .user(label: "", text: "Find the invoice"),
            .activity(label: "Searching mail…", done: true),
            .assistant(text: "Looking", streaming: false),
            .note("The conversation was stopped"),
        ])
        #expect(h.panel.submit("Once more"))
        try await h.turn()
        #expect(fake.starts == 2)
        #expect(h.contents.last == .assistant(text: "Fresh start", streaming: false))
    }

    /// New Conversation: the transcript empties, the process ends, the
    /// next question starts a new one.
    @Test func newConversation() async throws {
        let fake = try FakeClaude(turns: [answerTurn("One"), answerTurn("Two")])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        #expect(h.panel.submit("First"))
        try await h.turn()
        let p = try #require(h.panel.process)
        #expect(h.panel.isPinned)
        h.panel.newConversation()
        #expect(h.panel.items.isEmpty)
        #expect(!h.panel.isPinned)
        #expect(h.changes.last == .reset)
        #expect(h.panel.process == nil)
        try await waitFor { !p.running }
        #expect(h.panel.submit("Second"))
        try await h.turn()
        #expect(fake.starts == 2)
        #expect(h.contents == [.user(label: "", text: "Second"), .assistant(text: "Two", streaming: false)])
    }

    /// The process ending during a turn: an error with its stderr, Try
    /// Again starts a new process.
    @Test func exitDuringATurn() async throws {
        let fake = try FakeClaude(
            onStart: #"if [ "$n" = 1 ]; then read -r line; echo 'Error: boom' >&2; exit 1; fi"#, turns: [answerTurn("Recovered")])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        #expect(h.panel.submit("Hello"))
        try await h.turn()
        #expect(h.contents == [.user(label: "", text: "Hello"), .error("The assistant stopped: Error: boom", retry: true)])
        #expect(h.panel.process == nil)
        h.panel.retry(h.panel.items[1].id)
        try await h.turn()
        #expect(fake.starts == 2)
        #expect(h.contents.last == .assistant(text: "Recovered", streaming: false))
    }

    /// A result that is not a success is said; the process stays for the
    /// next question.
    @Test func failedResult() async throws {
        let fake = try FakeClaude(turns: [
            FakeTurn(lines: [fakeInit, fakeResult("API Error: 529 Overloaded", success: false)]), answerTurn("Better"),
        ])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        #expect(h.panel.submit("Hello"))
        try await h.turn()
        #expect(h.contents.last == .error("The assistant stopped: API Error: 529 Overloaded", retry: true))
        #expect(h.panel.process?.running == true)
        #expect(h.panel.submit("Hello again"))
        try await h.turn()
        #expect(fake.starts == 1)
        // The older error no longer offers Try Again.
        #expect(h.contents[1] == .error("The assistant stopped: API Error: 529 Overloaded", retry: false))
    }

    /// The model setting reaches the command line of the next conversation;
    /// the board's triage model (`board-triage-model`) does not.
    @Test func modelSetting() async throws {
        let fake = try FakeClaude(turns: [answerTurn("ok")])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        h.scratch.settings.assistantModel = .opus
        h.scratch.settings.boardTriageModel = .haiku
        #expect(h.panel.subtitle == "Claude Code · Opus")
        #expect(h.panel.submit("Hello"))
        try await h.turn()
        let args = fake.args
        #expect(args[try #require(args.firstIndex(of: "--model")) + 1] == "opus")
    }

    // MARK: A conversation keeps its context

    /// The conversation's first question pins the chip's context, whatever
    /// kind of question it is: a quick action, a waiting action's words, a
    /// free question, a menu's action, Summarize Unread. Later selections
    /// change neither the chip nor what the conversation is about.
    @Test func firstQuestionPinsTheContext() async throws {
        let fake = try FakeClaude(turns: [answerTurn("ok")])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        let m1 = message("m1", subject: "Invoice 42", thread: "t1")
        let m2 = message("m2", subject: "Lunch", thread: "t2")
        let sends: [(String, @MainActor () -> Void, String)] = [
            ("quick action", { h.panel.run(.summarize) }, try Assistant.prompt(.app, .summarize, m1.selection)),
            ("waiting action", {
                h.panel.run(.draftReply)
                _ = h.panel.submit("Yes.")
            }, try Assistant.prompt(.app, .draftReply, m1.selection) + "Yes."),
            ("free question", { _ = h.panel.submit("What now?") },
             "Context: the user has selected message m1 in account a.\n\nWhat now?"),
            ("menu action", { h.panel.run(.tasks, on: m1) }, try Assistant.prompt(.app, .tasks, m1.selection)),
            ("unread", { h.panel.summarizeUnread(accountID: "a", folderID: "in") },
             try Assistant.unreadPrompt(accountID: "a", folderID: "in")),
        ]
        for (name, send, prompt) in sends {
            h.panel.newConversation()
            // The menu's action takes its own message, whatever the chip shows.
            h.panel.setContext(name == "menu action" ? m2 : m1)
            #expect(!h.panel.isPinned, "\(name)")
            #expect(h.panel.contextLabel == "Selected message", "\(name)")
            send()
            try await h.turn()
            #expect(fake.prompts.last == prompt, "\(name)")
            #expect(h.panel.pinned.map(\.context) == [m1], "\(name)")
            #expect(h.panel.contextLabel == "Conversation about: Invoice 42", "\(name)")
            #expect(!h.panel.anotherSelected, "\(name)")
            h.panel.setContext(m2)
            #expect(h.panel.contextLabel == "Conversation about: Invoice 42", "\(name)")
            #expect(h.panel.anotherSelected, "\(name)")
            #expect(h.panel.pinned.map(\.context) == [m1], "\(name)")
            h.panel.removeContext() // no remove button while pinned
            #expect(!h.panel.contextRemoved, "\(name)")
        }
    }

    /// The chip once a conversation keeps its context.
    @Test func pinnedChipLabels() {
        typealias Ctx = AssistantPanelController.Context
        let m1 = message("m1", subject: "Invoice 42")
        let cases: [(String, [Ctx?], String)] = [
            ("one message", [m1], "Conversation about: Invoice 42"),
            ("no subject", [message("m1")], "Selected message"),
            ("a blank subject", [message("m1", subject: " \n\t")], "Selected message"),
            ("a subject on two lines", [message("m1", subject: "Invoice\r\n42")], "Conversation about: Invoice 42"),
            ("all mail", [nil], "All mail"),
            ("a conversation", [folded(["m3", "m2", "m1"], count: 3, subject: "Trip")], "Conversation about: Trip"),
            ("a conversation without a subject", [folded(["m3", "m2", "m1"], count: 3)], "Conversation about 3 messages"),
            ("a folded conversation", [folded(["m3"], count: 3, partial: true, subject: "Trip")], "Conversation about: Trip"),
            ("two messages", [m1, message("m2", subject: "Lunch")], "Conversation about 2 messages"),
            ("a conversation and a message", [folded(["m3", "m2"], count: 2), message("m4")], "Conversation about 3 messages"),
            ("members not known yet", [folded(["m3"], count: 3, partial: true), message("m9")], "Conversation about 4 messages"),
            ("a message counted once", [folded(["m2", "m1"], count: 2), m1], "Conversation about 2 messages"),
            ("all mail and a message", [nil, m1], "Conversation about: Invoice 42"),
            ("all mail and two messages", [nil, m1, message("m2")], "Conversation about 2 messages"),
            ("the same id in two accounts", [m1, Ctx(selection: Assistant.Selection(accountID: "b", messageIDs: ["m1"]))],
             "Conversation about 2 messages"),
        ]
        for (name, contexts, want) in cases {
            #expect(AssistantPanelController.pinnedLabel(contexts) == want, "\(name)")
        }
    }

    /// The bar "Another message is selected": only while the conversation
    /// keeps its context and the selection is part of none of it; a folded
    /// conversation is part of a pinned context when they share a message
    /// or, being a conversation, the thread.
    @Test func anotherSelectedBar() async throws {
        let fake = try FakeClaude(turns: [answerTurn("ok")])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        let m1 = message("m1", subject: "Invoice", thread: "t1")
        h.panel.setContext(message("m2", thread: "t2"))
        #expect(!h.panel.anotherSelected, "nothing is pinned before the first question")
        h.panel.setContext(m1)
        #expect(h.panel.submit("Who sent it?"))
        try await h.turn()
        var states = 0
        h.panel.onState = { states += 1 }
        let cases: [(String, AssistantPanelController.Context?, Bool)] = [
            ("another message", message("m2", thread: "t2"), true),
            ("the pinned message", m1, false),
            ("no selection", nil, false),
            ("another message of the same thread", message("m6", thread: "t1"), true),
            ("a folded conversation of the thread", folded(["m5"], count: 3, partial: true, thread: "t1"), false),
            ("a conversation with the message", folded(["m7", "m1"], count: 2), false),
            ("another folded conversation", folded(["m8"], count: 2, partial: true, thread: "t3"), true),
            ("the same id in another account",
             AssistantPanelController.Context(selection: Assistant.Selection(accountID: "b", messageIDs: ["m1"])), true),
        ]
        for (name, c, want) in cases {
            h.panel.setContext(c)
            #expect(h.panel.anotherSelected == want, "\(name)")
            #expect(h.panel.contextLabel == "Conversation about: Invoice", "\(name)")
        }
        #expect(states == cases.count)
        h.panel.newConversation()
        #expect(!h.panel.anotherSelected)
        #expect(h.panel.contextLabel == "Selected message")
    }

    /// Add to Conversation: the selection joins the conversation, the bar
    /// goes, and the next free question tells the model once.
    @Test func addToConversation() async throws {
        let fake = try FakeClaude(turns: [answerTurn("ok")])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        let m1 = message("m1", subject: "Invoice", thread: "t1")
        let m2 = message("m2", subject: "Lunch", thread: "t2")
        h.panel.setContext(m1)
        h.panel.run(.summarize)
        try await h.turn()
        h.panel.addSelection() // nothing: the selection is what the conversation is about
        #expect(h.panel.pinned.count == 1)
        h.panel.setContext(m2)
        #expect(h.panel.anotherSelected)
        h.panel.addSelection()
        #expect(!h.panel.anotherSelected)
        #expect(h.panel.pinned.map(\.context) == [m1, m2])
        #expect(h.panel.pinned.map(\.announced) == [true, false])
        #expect(h.panel.contextLabel == "Conversation about 2 messages")
        #expect(h.panel.submit("Which is older?"))
        try await h.turn()
        #expect(fake.prompts.last
            == "Context: the user has also selected message m2 in account a; questions from now on may be about it too.\n\nWhich is older?")
        #expect(h.panel.pinned.map(\.announced) == [true, true])
        #expect(h.panel.submit("And the total?"))
        try await h.turn()
        #expect(fake.prompts.last == "And the total?")
        #expect(fake.starts == 1)
        h.panel.setContext(m1)
        #expect(!h.panel.anotherSelected)
    }

    /// An added folded conversation's members are asked for at once, and
    /// the model hears of all of them.
    @Test func addedConversationIsResolved() async throws {
        let fake = try FakeClaude(turns: [answerTurn("ok")])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        var asked: [AssistantPanelController.Context] = []
        h.panel.resolveContext = { c, done in
            asked.append(c)
            done(Assistant.Selection(accountID: "a", messageIDs: ["m9", "m8", "m7"]))
        }
        h.panel.setContext(message("m1", subject: "Invoice"))
        #expect(h.panel.submit("Who sent it?"))
        try await h.turn()
        let conv = folded(["m9"], count: 3, partial: true, subject: "Trip", thread: "t9")
        h.panel.setContext(conv)
        #expect(h.panel.anotherSelected)
        h.panel.addSelection()
        #expect(h.panel.contextLabel == "Conversation about 4 messages")
        try await waitFor { h.panel.pinned.last?.context?.partial == false }
        #expect(asked == [conv])
        #expect(h.panel.contextLabel == "Conversation about 4 messages")
        #expect(!h.panel.anotherSelected)
        #expect(h.panel.submit("When do we leave?"))
        try await h.turn()
        #expect(fake.prompts.last
            == "Context: the user has also selected a conversation with messages m9, m8, m7 (newest first) in account a; questions from now on may be about it too.\n\nWhen do we leave?")
        #expect(asked.count == 1)
    }

    /// The bar's New Conversation: nothing is pinned any more, the chip
    /// follows the selection, and the next question starts over.
    @Test func newConversationFromTheBar() async throws {
        let fake = try FakeClaude(turns: [answerTurn("ok")])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        let m2 = message("m2", subject: "Lunch")
        h.panel.setContext(message("m1", subject: "Invoice"))
        #expect(h.panel.submit("First"))
        try await h.turn()
        h.panel.setContext(m2)
        #expect(h.panel.anotherSelected)
        h.panel.newConversation()
        #expect(!h.panel.isPinned && h.panel.pinned.isEmpty && h.panel.items.isEmpty)
        #expect(!h.panel.anotherSelected)
        #expect(h.panel.contextLabel == "Selected message")
        #expect(h.panel.canRunActions)
        #expect(h.panel.submit("Second"))
        try await h.turn()
        #expect(fake.starts == 2)
        #expect(fake.prompts.last == "Context: the user has selected message m2 in account a.\n\nSecond")
        #expect(h.panel.pinned.map(\.context) == [m2])
        #expect(h.panel.contextLabel == "Conversation about: Lunch")
    }

    /// A menu's action on a message that is part of no pinned context adds
    /// it and names it in its own prompt, so no line of context goes with
    /// the next question; on a pinned message it only runs. An attachment's
    /// question does the same for its message.
    @Test func menuActionOnAnotherMessageAddsIt() async throws {
        let fake = try FakeClaude(turns: [answerTurn("ok")])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        let m1 = message("m1", subject: "Invoice", thread: "t1")
        let m2 = message("m2", subject: "Lunch", thread: "t2")
        let m4 = message("m4", subject: "Visit", thread: "t4")
        h.panel.setContext(m1)
        #expect(h.panel.submit("Who sent it?"))
        try await h.turn()
        #expect(fake.prompts.last == "Context: the user has selected message m1 in account a.\n\nWho sent it?")

        h.panel.setContext(m2)
        h.panel.run(.summarize, on: m2)
        try await h.turn()
        #expect(fake.prompts.last == (try Assistant.prompt(.app, .summarize, m2.selection)))
        #expect(h.panel.pinned.map(\.context) == [m1, m2])
        #expect(h.panel.pinned.map(\.announced) == [true, true])
        #expect(!h.panel.anotherSelected)
        #expect(h.panel.submit("Anything urgent?"))
        try await h.turn()
        #expect(fake.prompts.last == "Anything urgent?")

        h.panel.run(.tasks, on: m1)
        try await h.turn()
        #expect(fake.prompts.last == (try Assistant.prompt(.app, .tasks, m1.selection)))
        #expect(h.panel.pinned.count == 2)

        h.panel.askAttachment(accountID: "a", messageID: "m3", partID: "2", subject: "Scan", threadID: "t3")
        #expect(h.panel.pinned.count == 3)
        #expect(h.panel.contextLabel == "Conversation about 3 messages")
        #expect(h.panel.submit("What is it?"))
        try await h.turn()
        #expect(fake.prompts.last
            == (try Assistant.attachmentPrompt(accountID: "a", messageID: "m3", partID: "2")) + "What is it?")
        #expect(h.panel.submit("And the date?"))
        try await h.turn()
        #expect(fake.prompts.last == "And the date?")

        // A waiting action stays on its message when the selection moves on.
        h.panel.setContext(m4)
        h.panel.run(.draftReply, on: m4)
        #expect(h.panel.pending == .action(.draftReply))
        #expect(h.panel.pinned.count == 4)
        #expect(!h.panel.anotherSelected)
        h.panel.setContext(m1)
        h.panel.setContext(nil)
        #expect(h.panel.pending == .action(.draftReply))
        #expect(h.panel.submit("Fine"))
        try await h.turn()
        #expect(fake.prompts.last == (try Assistant.prompt(.app, .draftReply, m4.selection)) + "Fine")
        #expect(fake.starts == 1)
    }

    /// The quick actions act on the newest pinned context, not on the
    /// selection; with only all mail pinned there is nothing to act on.
    @Test func quickActionsUseTheNewestPinnedContext() async throws {
        let fake = try FakeClaude(turns: [answerTurn("ok")])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        let m1 = message("m1", subject: "Invoice", thread: "t1")
        let m2 = message("m2", subject: "Lunch", thread: "t2")
        h.panel.setContext(m1)
        #expect(h.panel.submit("Who sent it?"))
        try await h.turn()
        h.panel.setContext(m2)
        h.panel.addSelection()
        h.panel.setContext(message("m3", thread: "t3"))
        #expect(h.panel.anotherSelected && h.panel.canRunActions)
        h.panel.run(.tasks)
        try await h.turn()
        #expect(fake.prompts.last == (try Assistant.prompt(.app, .tasks, m2.selection)))
        h.panel.run(.draftReply)
        h.panel.setContext(nil)
        #expect(h.panel.pending == .action(.draftReply))
        #expect(h.panel.submit("ok"))
        try await h.turn()
        #expect(fake.prompts.last == (try Assistant.prompt(.app, .draftReply, m2.selection)) + "ok")

        h.panel.newConversation()
        #expect(h.panel.submit("How many unread?"))
        try await h.turn()
        #expect(fake.prompts.last == "How many unread?")
        #expect(h.panel.pinned.map(\.context) == [nil])
        #expect(h.panel.contextLabel == "All mail")
        #expect(!h.panel.canRunActions)
        h.panel.setContext(m1)
        #expect(h.panel.anotherSelected)
        h.panel.addSelection()
        #expect(h.panel.canRunActions)
        #expect(h.panel.contextLabel == "Conversation about: Invoice")
        #expect(h.panel.submit("From whom?"))
        try await h.turn()
        #expect(fake.prompts.last
            == "Context: the user has also selected message m1 in account a; questions from now on may be about it too.\n\nFrom whom?")
    }

    /// After Stop the next question starts a new Claude Code, which knows
    /// nothing of the conversation: its contexts are told again.
    @Test func aNewProcessIsToldTheContextAgain() async throws {
        let fake = try FakeClaude(turns: [
            answerTurn("Summary"),
            FakeTurn(lines: [fakeInit, fakeDelta("Thinking")], shell: "sleep 5"),
            answerTurn("Again"),
        ])
        let h = try PanelHarness(fake: fake)
        defer { h.stop() }
        h.panel.setContext(message("m1", subject: "Invoice"))
        h.panel.run(.summarize)
        try await h.turn()
        h.panel.setContext(message("m2", subject: "Lunch"))
        h.panel.addSelection()
        #expect(h.panel.submit("Which is older?"))
        try await waitFor { h.contents.last == .assistant(text: "Thinking", streaming: true) }
        #expect(fake.prompts.last
            == "Context: the user has also selected message m2 in account a; questions from now on may be about it too.\n\nWhich is older?")
        h.panel.stop()
        #expect(h.panel.pinned.count == 2)
        #expect(h.panel.contextLabel == "Conversation about 2 messages")
        #expect(h.panel.submit("Once more"))
        try await h.turn()
        #expect(fake.starts == 2)
        #expect(fake.prompts.last == "Context: the user has selected message m1 in account a.\n"
            + "Context: the user has also selected message m2 in account a; questions from now on may be about it too.\n\nOnce more")
    }
}
