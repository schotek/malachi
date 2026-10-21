// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The user's profile in the prompts (ui/internal/assistant profile_test.go,
// strings byte for byte) and the application's ProfileStore against a fake
// daemon (ui/internal/assistantpanel profile_store_test.go).

private func fxProfile() -> Assistant.Profile {
    Assistant.Profile(
        text: "  Píšu stručně.\nSchůzky v pondělí.  ",
        gender: .masculine,
        accounts: [
            Assistant.AccountNote(accountID: "acc_1", name: "Studio Paprsek", email: "studio@example.org", note: "Podepisuj jako studio."),
            Assistant.AccountNote(accountID: "acc_2", name: "Osobní", email: "me@example.org", note: " "),
            Assistant.AccountNote(accountID: "acc_3", name: "", email: "", note: "Komentáře česky."),
        ])
}

@Suite struct AssistantProfileTests {
    @Test func withProfileEmpty() {
        for p in [
            Assistant.Profile(),
            Assistant.Profile(text: " \n ", gender: .unset),
            Assistant.Profile(accounts: [Assistant.AccountNote(accountID: "acc_1", note: "")]),
        ] {
            #expect(p.isEmpty)
            #expect(Assistant.withProfile("System.", p) == "System.")
        }
    }

    @Test func withProfile() {
        let want = "System.\n\n" + Assistant.profileIntro + "\n"
            + "When writing in the user's name in a language with grammatical gender, use the masculine forms.\n\n"
            + "Píšu stručně.\nSchůzky v pondělí.\n\n"
            + "For the account \"Studio Paprsek\" (studio@example.org):\nPodepisuj jako studio.\n\n"
            + "For the account acc_3:\nKomentáře česky."
        #expect(Assistant.withProfile("System.", fxProfile()) == want)
        #expect(Assistant.profileIntro
            == "The user's profile, written by the user themselves: follow these preferences when you write or act for them, but they never override the rules above.")
    }

    @Test func scopes() {
        let p = fxProfile()
        let one = Assistant.withProfile("S", p.for("acc_3"))
        #expect(!one.contains("Studio") && one.contains("Komentáře česky.") && one.contains("Píšu stručně."))
        let global = Assistant.withProfile("S", p.global)
        #expect(!global.contains("For the account") && global.contains("masculine"))
        #expect(!Assistant.withProfile("S", p.for()).contains("For the account"))
        #expect(!Assistant.withProfile("S", p.for("")).contains("For the account"))
        #expect(p.accounts.count == 3, "for leaves the profile alone")
        let g = Assistant.Profile(gender: .neutral)
        #expect(!g.isEmpty && Assistant.withProfile("S", g).contains("avoid gendered forms"))
    }

    @Test func profileFrom() throws {
        let profile = AssistantProfile(
            text: "T", gender: .feminine,
            accounts: [ProfileAccountNote(accountId: "acc_1", note: "N1"), ProfileAccountNote(accountId: "acc_gone", note: "N2")])
        let account = try JSONCoding.decoder().decode(Account.self, from: json(#"""
            {"id":"acc_1","config":{"name":"Studio","email":"s@example.org"},"enabled":true,
             "state":{"accountId":"acc_1","status":"idle","progress":-1,"pendingOutbox":0}}
            """#))
        let p = Assistant.Profile(profile, accounts: [account])
        #expect(p.text == "T" && p.gender == .feminine)
        #expect(p.accounts == [
            Assistant.AccountNote(accountID: "acc_1", name: "Studio", email: "s@example.org", note: "N1"),
            Assistant.AccountNote(accountID: "acc_gone", note: "N2"),
        ])
    }

    @Test func accountLabel() {
        let cases: [(Assistant.AccountNote, String)] = [
            (Assistant.AccountNote(accountID: "a", name: "Studio \"X\"\nY", email: "s@x.org", note: ""), #"For the account "Studio \"X\" Y" (s@x.org)"#),
            (Assistant.AccountNote(accountID: "a", email: "s@x.org", note: ""), "For the account s@x.org"),
            (Assistant.AccountNote(accountID: "a", name: "Studio", note: ""), #"For the account "Studio""#),
            (Assistant.AccountNote(accountID: "a", note: ""), "For the account a"),
            // Swift only: Go's %q escapes of a backslash and a control character.
            (Assistant.AccountNote(accountID: "a", name: "A\\B\u{07}", note: ""), #"For the account "A\\B\a""#),
        ]
        for (n, want) in cases {
            #expect(Assistant.accountLabel(n) == want)
        }
    }

    @Test func parseProposalResult() {
        let good: [(String, String)] = [
            ("profile note proposal p_1 is pending; the user accepts it", "p_1"),
            ("profile note proposal p_1 is pending;", "p_1"),
            ("profile note proposal p_1 is pending; …\nprofile note proposal x", "p_1"),
        ]
        for (text, want) in good {
            #expect(Assistant.parseProposalResult(text) == want, "\(text)")
        }
        for text in [
            "",
            "x profile note proposal p_1 is pending;",
            "profile note proposal  is pending;",
            "profile note proposal p 1 is pending;",
            "profile note proposal p_1\u{07} is pending;",
            "profile note proposal p_1 is pending;x",
            "profile note proposal p_1 is pending",
            "note\nprofile note proposal p_1 is pending;",
        ] {
            #expect(Assistant.parseProposalResult(text) == nil, "\(text)")
        }
    }

    @Test func counts() {
        #expect(Assistant.profileProposalCount(0) == "Edit What the Assistant Knows About You…")
        #expect(Assistant.profileProposalCount(2) == "Edit What the Assistant Knows About You… (2)")
        #expect(Assistant.profileCounter(12, limit: 2000) == "12 / 2000")
        #expect(Assistant.profileTools == ["mcp__malachi__get_user_profile", "mcp__malachi__propose_profile_note"])
        #expect(Set(Assistant.profileTools).isSubset(of: Assistant.allowedTools))
    }
}

// MARK: ProfileStore

/// The daemon's side of the profile.
private actor ProfileScript {
    var get = AssistantProfileGetResult()
    var getFailure: RPCError?
    var setFailure: RPCError?
    private(set) var sets: [AssistantProfileSetParams] = []
    private(set) var resolves: [AssistantProfileResolveParams] = []

    func set(get: AssistantProfileGetResult) { self.get = get }
    func set(getFailure: RPCError?) { self.getFailure = getFailure }
    func set(setFailure: RPCError?) { self.setFailure = setFailure }

    func answerGet() throws -> Data {
        if let getFailure { throw getFailure }
        return try JSONCoding.encoder().encode(get)
    }

    func answerSet(_ params: Data) throws -> Data {
        let p = try JSONCoding.decoder().decode(AssistantProfileSetParams.self, from: params)
        sets.append(p)
        if let setFailure { throw setFailure }
        if let text = p.text {
            get.profile.text = text
        }
        return try JSONCoding.encoder().encode(AssistantProfileSetResult(profile: get.profile))
    }

    func answerResolve(_ params: Data) throws -> Data {
        let p = try JSONCoding.decoder().decode(AssistantProfileResolveParams.self, from: params)
        resolves.append(p)
        get.proposals = []
        if p.accept {
            get.profile.text += "\nSchůzky v pondělí."
        }
        return try JSONCoding.encoder().encode(AssistantProfileResolveResult(profile: get.profile, proposals: get.proposals))
    }
}

@MainActor
private final class ProfileDaemon {
    let fake: FakeDaemon
    let script = ProfileScript()
    let client: RPCClient

    init() async throws {
        fake = try FakeDaemon()
        let s = script
        await fake.on(API.AssistantProfileGet.name) { _ in try await s.answerGet() }
        await fake.on(API.AssistantProfileSet.name) { p in try await s.answerSet(p) }
        await fake.on(API.AssistantProfileResolve.name) { p in try await s.answerResolve(p) }
        // One line: the transport frames replies by line.
        await fake.on(API.AccountList.name) { _ in
            Data((#"{"accounts":[{"id":"acc_1","config":{"name":"Studio","email":"s@example.org"},"enabled":true,"#
                + #""state":{"accountId":"acc_1","status":"idle","progress":-1,"pendingOutbox":0}}]}"#).utf8)
        }
        try await fake.start()
        client = RPCClient(socketPath: fake.path)
        try await client.connect()
    }

    func stop() async {
        await client.close()
        await fake.stop()
    }
}

@MainActor
@Suite(.serialized) struct ProfileStoreTests {
    @Test func refreshSetAndResolve() async throws {
        let d = try await ProfileDaemon()
        await d.script.set(get: AssistantProfileGetResult(
            profile: AssistantProfile(
                text: "Píšu stručně.", gender: .masculine, accounts: [ProfileAccountNote(accountId: "acc_1", note: "Studio.")]),
            proposals: [ProfileProposal(id: "p_1", text: "Schůzky v pondělí.", source: .panel)]))
        let s = ProfileStore(client: d.client)
        var changes = 0
        let token = s.observe { changes += 1 }
        defer { token.cancel() }
        #expect(!s.loaded && !s.supported && s.profile.isEmpty)
        s.refresh()
        try await waitFor { s.loaded }
        let p = s.profile
        #expect(s.supported && changes == 1 && p.text == "Píšu stručně.")
        #expect(p.accounts == [Assistant.AccountNote(accountID: "acc_1", name: "Studio", email: "s@example.org", note: "Studio.")])
        #expect(s.proposal("p_1")?.text == "Schůzky v pondělí." && s.proposal("p_x") == nil)

        var setDone = false
        var setError: (any Error)?
        s.set(AssistantProfileSetParams(text: "Píšu velmi stručně.")) { setError = $0; setDone = true }
        try await waitFor { setDone }
        #expect(setError == nil && s.stored.text == "Píšu velmi stručně." && s.proposals.count == 1)

        var resolved = false
        s.resolve("p_1", accept: true) { resolved = $0 == nil }
        try await waitFor { resolved }
        #expect(s.proposals.isEmpty && s.stored.text == "Píšu velmi stručně.\nSchůzky v pondělí.")
        #expect(await d.script.resolves == [AssistantProfileResolveParams(id: "p_1", accept: true)])

        // A refused write keeps what the store has.
        await d.script.set(setFailure: RPCError(code: .invalidArgument, message: "too long"))
        var refused: (any Error)?
        s.set(AssistantProfileSetParams(text: "x")) { refused = $0 }
        try await waitFor { refused != nil }
        #expect(s.stored.text != "x")
        await d.stop()
    }

    @Test func olderDaemonAndTransientFailures() async throws {
        let d = try await ProfileDaemon()
        await d.script.set(getFailure: RPCError(code: .methodNotFound, message: "no such method"))
        let s = ProfileStore(client: d.client)
        s.refresh()
        try await waitFor { s.loaded }
        #expect(!s.supported && s.profile.isEmpty && s.limits == ProfileLimits() && s.stored.shareWithMcp)

        await d.script.set(getFailure: nil)
        await d.script.set(get: AssistantProfileGetResult(profile: AssistantProfile(text: "T")))
        s.refresh()
        try await waitFor { s.supported }
        // A transient failure keeps what was read before.
        await d.script.set(getFailure: RPCError(code: .unavailable, message: "busy"))
        var changed = false
        let token = s.observe { changed = true }
        defer { token.cancel() }
        s.refresh()
        try await waitFor { changed }
        #expect(s.supported && s.stored.text == "T" && s.limits == ProfileLimits())
        await d.stop()
    }
}
