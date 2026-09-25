// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The compose manager (ui/internal/compose/manager.go) over a fake daemon
// and fake windows, and the link popover's URL check (compose.go
// `insertLink`).

private struct Timeout: Error {}

@MainActor
private func waitUntil(_ timeout: Duration = .seconds(5), _ cond: @MainActor () async throws -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while try await !cond() {
        if ContinuousClock.now > deadline { throw Timeout() }
        try await Task.sleep(for: .milliseconds(10))
    }
}

/// What `account.list` answers.
private actor Script {
    var accounts: [Account] = []
    var fails = false

    func set(accounts: [Account]) { self.accounts = accounts }
    func set(fails: Bool) { self.fails = fails }

    func list(_ params: Data) throws -> Data {
        if fails {
            throw RPCError(code: .storageError, message: "disk")
        }
        return try JSONCoding.encoder().encode(AccountListResult(accounts: accounts))
    }
}

/// A compose window as the manager sees it.
@MainActor
private final class FakeHandle: ComposeWindowHandle {
    let params: ComposeParams
    var accounts: [[Account]] = []
    var placeholders: [Bool] = []
    var toasts: [String] = []

    init(params: ComposeParams) {
        self.params = params
    }

    func setAccounts(_ accounts: [Account], placeholder: Bool) {
        self.accounts.append(accounts)
        placeholders.append(placeholder)
    }

    func toast(_ text: String) {
        toasts.append(text)
    }

    /// As the window does: the saved draft it was opened with, or the
    /// Drafts message it takes over.
    func edits(_ d: Draft) -> Bool {
        (d.id != nil && params.draftID == d.id) || (d.replaces != nil && params.replaces == d.replaces)
    }
}

@MainActor
private final class Harness {
    let fake: FakeDaemon
    let script = Script()
    let client: RPCClient
    let scratch = ScratchSettings()
    let compose: ComposeController
    var handles: [FakeHandle] = []

    init(accounts: [Account] = [testAccount("acc1", email: "one@example.invalid", displayName: "One")]) async throws {
        fake = try FakeDaemon()
        let script = script
        await script.set(accounts: accounts)
        await fake.on(API.AccountList.name) { try await script.list($0) }
        try await fake.start()
        client = RPCClient(socketPath: fake.path)
        try await client.connect()
        compose = ComposeController(client: client, settings: scratch.settings)
        compose.makeWindow = { [unowned self] p in
            let h = FakeHandle(params: p)
            self.handles.append(h)
            return h
        }
    }

    func listCalls() async -> Int {
        await fake.calls.filter { $0 == API.AccountList.name }.count
    }

    func stop() async {
        await client.close()
        await fake.stop()
    }
}

@MainActor
@Suite(.serialized) struct ComposeControllerTests {
    @Test func accountsAreFetchedOnTheFirstWindowOnly() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        #expect(h.compose.placeholder)
        #expect(h.compose.accounts == ComposeController.placeholderAccounts)
        #expect(await h.listCalls() == 0)

        h.compose.open(ComposeParams(kind: .new, subject: "x"))
        #expect(h.handles.count == 1)
        #expect(h.handles[0].params.subject == "x")
        #expect(h.compose.openWindows.count == 1)
        try await waitUntil { !h.compose.placeholder }
        #expect(await h.listCalls() == 1)
        #expect(h.compose.accounts.map(\.id) == ["acc1"])
        #expect(h.handles[0].accounts.map { $0.map(\.id) } == [["acc1"]])
        #expect(h.handles[0].placeholders == [false])
        #expect(h.compose.selfAddress == Address(name: "One", address: "one@example.invalid"))

        // The second window uses what is cached.
        h.compose.open(ComposeParams(kind: .new))
        try await Task.sleep(for: .milliseconds(30))
        #expect(await h.listCalls() == 1)
        #expect(h.handles.count == 2)
        #expect(h.handles[1].accounts.isEmpty, "nothing pushed: the window read the cache when it was made")

        h.compose.remove(h.handles[0])
        #expect(h.compose.openWindows.count == 1)
        #expect(h.compose.openWindows[0] === h.handles[1])
    }

    @Test func invalidateRefetchesWhileAWindowIsOpen() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.compose.open(ComposeParams(kind: .new))
        try await waitUntil { await h.listCalls() == 1 && !h.compose.placeholder }

        await h.script.set(accounts: [
            testAccount("acc1", email: "one@example.invalid"), testAccount("acc2", email: "two@example.invalid"),
        ])
        h.compose.invalidate()
        try await waitUntil { h.handles[0].accounts.count == 2 }
        #expect(await h.listCalls() == 2)
        #expect(h.handles[0].accounts[1].map(\.id) == ["acc1", "acc2"])
        #expect(h.compose.accounts.count == 2)

        // Without a window nothing is asked until the next one opens.
        h.compose.remove(h.handles[0])
        h.compose.invalidate()
        try await Task.sleep(for: .milliseconds(30))
        #expect(await h.listCalls() == 2)
        h.compose.open(ComposeParams(kind: .new))
        try await waitUntil { await h.listCalls() == 3 }
    }

    @Test func noAccountsMeansThePlaceholderIdentity() async throws {
        let h = try await Harness(accounts: [])
        defer { Task { await h.stop() } }
        h.compose.open(ComposeParams(kind: .new))
        try await waitUntil { !h.handles[0].placeholders.isEmpty }
        #expect(h.compose.placeholder)
        #expect(h.compose.accounts == ComposeController.placeholderAccounts)
        #expect(h.compose.selfAddress == Address(name: "Malachi User", address: "me@example.invalid"))
        #expect(h.handles[0].placeholders == [true])
        #expect(h.handles[0].accounts == [ComposeController.placeholderAccounts])
    }

    @Test func aFailedListIsAskedAgainForTheNextWindow() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        await h.script.set(fails: true)
        h.compose.open(ComposeParams(kind: .new))
        try await waitUntil { await h.listCalls() == 1 }
        try await Task.sleep(for: .milliseconds(30))
        #expect(h.compose.placeholder)
        #expect(h.handles[0].accounts.isEmpty)
        await h.script.set(fails: false)
        h.compose.open(ComposeParams(kind: .new))
        try await waitUntil { !h.compose.placeholder }
        #expect(await h.listCalls() == 2)
        // Both windows hear about it.
        #expect(h.handles[0].accounts.count == 1)
        #expect(h.handles[1].accounts.count == 1)
    }

    @Test func blockedContentOfTheTemplateIsSaidOnOpen() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.compose.open(ComposeParams(kind: .reply, blocked: BlockedContent(forms: 1)))
        #expect(h.handles[0].toasts == ["1 unsafe element was removed from the message"])
        h.compose.open(ComposeParams(kind: .reply))
        #expect(h.handles[1].toasts.isEmpty)
    }

    /// Manager.FindDraft: the window editing the draft draft.open
    /// answered with, by its id or by the Drafts message it takes over.
    @Test func findDraftByIdOrReplacedMessage() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.compose.open(ComposeParams(kind: .new))
        h.compose.open(ComposeParams(kind: .edit, draftID: "d_1", version: 2))
        h.compose.open(ComposeParams(kind: .edit, replaces: "m_9"))
        #expect(h.compose.findDraft(Draft(id: "d_1", accountId: "a")) === h.handles[1])
        #expect(h.compose.findDraft(Draft(accountId: "a", replaces: "m_9")) === h.handles[2])
        #expect(h.compose.findDraft(Draft(id: "d_2", accountId: "a")) == nil)
        #expect(h.compose.findDraft(Draft(accountId: "a")) == nil, "a new window edits nothing")
    }

    @Test func openWithoutAFactoryDoesNothing() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.compose.makeWindow = nil
        h.compose.open(ComposeParams(kind: .new))
        try await Task.sleep(for: .milliseconds(30))
        #expect(h.handles.isEmpty)
        #expect(await h.listCalls() == 0)
    }
}

@Suite struct ComposeLinkURLTests {
    @Test func acceptsWebAndMailLinks() {
        #expect(composeLinkURL("https://example.com/a?b=c") == "https://example.com/a?b=c")
        #expect(composeLinkURL("  HTTP://Example.com/x  ") == "http://Example.com/x")
        #expect(composeLinkURL("mailto:alice@example.org") == "mailto:alice@example.org")
        #expect(composeLinkURL("https://example.com/#frag") == "https://example.com/#frag")
        #expect(composeLinkURL("https://example.com/%C3%A9") == "https://example.com/%C3%A9")
    }

    @Test func refusesWhatGoRefuses() {
        #expect(composeLinkURL("") == nil)
        #expect(composeLinkURL("example.com") == nil, "no scheme")
        #expect(composeLinkURL("ftp://example.com") == nil)
        #expect(composeLinkURL("javascript:alert(1)") == nil)
        #expect(composeLinkURL("https://ex ample.com/") == nil, "a space in the host")
        #expect(composeLinkURL("https://example.com/%zz") == nil, "a bad escape")
        #expect(composeLinkURL("https://example.com:port/") == nil, "a bad port")
        #expect(composeLinkURL("http://a\u{7}b") == nil, "a control character")
        #expect(composeLinkURL("https://example.com/#%zz") == nil, "a bad escape in the fragment")
    }
}
