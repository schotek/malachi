// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// What the App Store build leaves out: the user's own Claude Code and
/// Codex (docs/plans/mac-app-store.md §3.3). Settings the DMG stored read
/// as off there and stay as they were; nothing is looked up or run.
@MainActor @Suite struct DistributionAgentsTests {
    /// A throwaway defaults domain, removed by `done`.
    private func settings(_ d: Distribution) -> (Settings, () -> Void) {
        let name = "io.github.schotek.Malachi.distribution-test-" + UUID().uuidString
        let defaults = UserDefaults(suiteName: name)!
        return (Settings(defaults: defaults, distribution: d), { defaults.removePersistentDomain(forName: name) })
    }

    @Test func onlyTheDirectBuildRunsLocalAgents() {
        #expect(Distribution.direct.allowsLocalAgents)
        #expect(!Distribution.appStore.allowsLocalAgents)
    }

    @Test func onlyTheDirectBuildHandsFilesToClaude() {
        #expect(Distribution.direct.handsFilesToClaude)
        #expect(!Distribution.appStore.handsFilesToClaude)
    }

    @Test func theAppStoreBuildHasNoInAppTarget() {
        #expect(Assistant.targets(for: .direct) == [.desktop, .code, .app])
        #expect(Assistant.targets(for: .appStore) == [.desktop, .code])
    }

    @Test func inAppStoredByTheDMGReadsAsClaudeDesktopAndStays() {
        let (s, done) = settings(.appStore)
        defer { done() }
        s.defaults.set("inapp", forKey: Settings.Key.assistantTarget.rawValue)
        s.defaults.set("chatgpt", forKey: Settings.Key.assistantProvider.rawValue)
        #expect(s.assistantTarget == .desktop)
        #expect(s.assistantProvider == .claude)
        // Never written: a return to the DMG finds them as they were.
        s.assistantTarget = .app
        s.assistantProvider = .chatgpt
        #expect(s.defaults.string(forKey: Settings.Key.assistantTarget.rawValue) == "inapp")
        #expect(s.defaults.string(forKey: Settings.Key.assistantProvider.rawValue) == "chatgpt")
        // The other targets are chosen as usual.
        s.assistantTarget = .code
        #expect(s.assistantTarget == .code)
    }

    @Test func theDirectBuildKeepsInAppAndChatGPT() {
        let (s, done) = settings(.direct)
        defer { done() }
        s.assistantTarget = .app
        s.assistantProvider = .chatgpt
        #expect(s.assistantTarget == .app)
        #expect(s.assistantProvider == .chatgpt)
    }

    @Test func theAppStoreBuildFindsNoClaudeCode() {
        let (s, done) = settings(.appStore)
        defer { done() }
        // Even with a usable candidate: the locator never looks.
        let locator = ClaudeCodeLocator(settings: s, environment: ["HOME": "/nonexistent", "PATH": "/bin"], usable: { _ in true })
        #expect(locator.locate() == nil)
        let (d, doneDirect) = settings(.direct)
        defer { doneDirect() }
        let direct = ClaudeCodeLocator(settings: d, environment: ["HOME": "/nonexistent", "PATH": "/bin"], usable: { _ in true })
        #expect(direct.locate() != nil)
    }

    @Test func theAppStoreBuildFindsNoCodex() {
        let (s, done) = settings(.appStore)
        defer { done() }
        s.assistantCodexPath = "/bin/ls"
        let codex = CodexProvider(connection: ChatGPTConnection(store: NoCredentials(), browser: NoBrowser(), validator: NoValidator()),
                                  settings: s, directory: FileManager.default.temporaryDirectory)
        #expect(codex.executable == nil)
        #expect(!codex.available)
    }
}

@MainActor private final class NoCredentials: ChatGPTCredentialStore {
    final class Lease: ChatGPTCredentialLease { func release() {} }
    func acquire() async throws -> any ChatGPTCredentialLease { Lease() }
    func registration() throws -> ChatGPTRegistration? { nil }
    func saveRegistration(_ value: ChatGPTRegistration) throws {}
    func tokens() throws -> ChatGPTTokens? { nil }
    func saveTokens(_ value: ChatGPTTokens) throws {}
    func deleteTokens() throws {}
}
@MainActor private final class NoBrowser: ChatGPTBrowser {
    func authorize(state: String, makeURL: (URL) throws -> URL) async throws -> (URL, URL) { throw ChatGPTFailure("test") }
    func cancel() {}
}
@MainActor private final class NoValidator: ChatGPTSignatureValidator {
    func verify(signingInput: Data, signature: Data, modulus: Data, exponent: Data) throws {}
}
