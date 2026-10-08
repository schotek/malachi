// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

@Suite struct CodexPolicyTests {
    /// The gate before a session starts (Go `toolPolicy`, C#
    /// `PolicyAllows`): exactly three bridge argument shapes, tools a subset
    /// of the shape's, fail closed for anything else.
    @Test func policyGateAcceptsOnlyTheThreeShapes() throws {
        func tools(_ args: [String], _ allowed: [String]) -> AssistantRequest.Tools {
            AssistantRequest.Tools(bridge: "/b", socket: "/s", bridgeArgs: args, allowed: allowed)
        }
        let triage = ["--allow-triage", "--triage-run", "run_1", "--triage-max", "40"]
        // Accepted.
        try CodexPolicy.check(nil)
        try CodexPolicy.check(tools([], Assistant.allowedTools))
        try CodexPolicy.check(tools([], ["mcp__malachi__read_message"]))
        try CodexPolicy.check(tools(["--reply-only", "m_1"], Assistant.suggestReplyTools))
        try CodexPolicy.check(tools(triage, Assistant.triageTools))
        try CodexPolicy.check(tools(triage, Assistant.triageTools(drafts: false)))
        try CodexPolicy.check(tools(Assistant.triageBridgeArgs(runID: "r", maxCases: 200), Assistant.triageTools))
        // Refused: the mutating tiers, in any place.
        let refused: [(String, AssistantRequest.Tools)] = [
            ("allow-modify", tools(["--allow-modify"], Assistant.allowedTools)),
            ("allow-send", tools(["--allow-send"], Assistant.allowedTools)),
            ("modify after triage", tools(triage + ["--allow-modify"], Assistant.triageTools)),
            ("send after reply-only", tools(["--reply-only", "m_1", "--allow-send"], Assistant.suggestReplyTools)),
            ("modify as the run id", tools(["--allow-triage", "--triage-run", "--allow-modify", "--triage-max", "4"], Assistant.triageTools)),
            ("send as the message id", tools(["--reply-only", "--allow-send"], Assistant.suggestReplyTools)),
            // Shapes out of order or incomplete.
            ("reordered", tools(["--triage-run", "r", "--allow-triage", "--triage-max", "4"], Assistant.triageTools)),
            ("no max", tools(["--allow-triage", "--triage-run", "r"], Assistant.triageTools)),
            ("max 0", tools(["--allow-triage", "--triage-run", "r", "--triage-max", "0"], Assistant.triageTools)),
            ("max 201", tools(["--allow-triage", "--triage-run", "r", "--triage-max", "201"], Assistant.triageTools)),
            ("max signed", tools(["--allow-triage", "--triage-run", "r", "--triage-max", "+4"], Assistant.triageTools)),
            ("max spaced", tools(["--allow-triage", "--triage-run", "r", "--triage-max", " 4"], Assistant.triageTools)),
            ("id with a space", tools(["--reply-only", "m 1"], Assistant.suggestReplyTools)),
            ("id with a line break", tools(["--reply-only", "m\n1"], Assistant.suggestReplyTools)),
            ("empty id", tools(["--reply-only", ""], Assistant.suggestReplyTools)),
            ("long id", tools(["--reply-only", String(repeating: "a", count: 513)], Assistant.suggestReplyTools)),
            ("unknown flag", tools(["--socket", "/x"], Assistant.allowedTools)),
            // Tools beyond the shape's set, unprefixed, or twice.
            ("send tool", tools([], ["mcp__malachi__send_message"])),
            ("triage tool in the panel", tools([], ["mcp__malachi__annotate_case"])),
            ("search in a reply", tools(["--reply-only", "m_1"], ["mcp__malachi__search_messages"])),
            ("unprefixed", tools([], ["read_message"])),
            ("twice", tools([], ["mcp__malachi__read_message", "mcp__malachi__read_message"])),
            ("a shell", tools([], ["Bash"])),
        ]
        for (name, t) in refused {
            #expect(throws: ChatGPTFailure.self, "\(name)") { try CodexPolicy.check(t) }
        }
    }

    @Test func catalogIsExactAndHostedToolsAreRemoved() throws {
        let policy = CodexPolicy(allowed: ["mcp__malachi__read_message"])
        let bytes = Data(#"{"model":"test","tools":[{"type":"shell"},{"type":"namespace","name":"malachi","tools":[{"type":"function","name":"read_message","parameters":{}},{"type":"function","name":"send_message"}]}],"store":true,"stream":false,"tool_choice":"required"}"#.utf8)
        let result = try JSONSerialization.jsonObject(with: policy.filterRequest(bytes)) as! [String: Any]
        let namespaces = result["tools"] as! [[String: Any]]
        #expect(namespaces.count == 1)
        #expect((namespaces[0]["tools"] as! [[String: Any]]).count == 1)
        #expect(result["store"] as? Bool == false)
        #expect(result["stream"] as? Bool == true)
        #expect(result["parallel_tool_calls"] as? Bool == false)
        #expect(result["tool_choice"] == nil)
    }
    @Test func requiredCatalogCannotDisappearOrDuplicate() {
        let policy = CodexPolicy(allowed: ["read_message"])
        #expect(throws: (any Error).self) { try policy.filterRequest(Data(#"{"tools":[]}"#.utf8)) }
        #expect(throws: (any Error).self) { try policy.filterRequest(Data(#"{"tools":[{"type":"namespace","name":"malachi","tools":[{"type":"function","name":"read_message"},{"type":"function","name":"read_message"}]}]}"#.utf8)) }
    }
    @Test func toolFreeRejectsHistoryAndMalformedAdditionalTools() throws {
        let policy = CodexPolicy(allowed: [])
        for json in [#"{"previous_response_id":"remote"}"#, #"{"conversation":"remote"}"#, #"{"input":[{"type":"additional_tools","tools":{}}]}"#] {
            #expect(throws: (any Error).self) { try policy.filterRequest(Data(json.utf8)) }
        }
        let data = try policy.filterRequest(Data(#"{"tools":[{"type":"web_search"},{"type":"function","name":"shell"}]}"#.utf8))
        let result = try JSONSerialization.jsonObject(with: data) as! [String: Any]
        #expect((result["tools"] as? [Any])?.isEmpty == true)
    }
    @Test func additionalCatalogIsFilteredAndRemovedFromHistory() throws {
        let policy = CodexPolicy(allowed: ["read_message", "create_draft"])
        let bytes = Data(#"{"tools":[{"type":"namespace","name":"malachi","tools":[{"type":"function","name":"read_message"}]}],"input":[{"type":"message","role":"user","content":"fixture"},{"type":"additional_tools","tools":[null,{"type":"tool_search"},{"type":"namespace","name":"other","tools":[{"type":"function","name":"create_draft"}]},{"type":"namespace","name":"malachi","tools":[null,{"type":"function","name":"create_draft"},{"type":"function","name":"send_message"}]}]},{"type":"function_call_output","call_id":"fixture","output":"safe"}]}"#.utf8)
        let data = try policy.filterRequest(bytes)
        let result = try JSONSerialization.jsonObject(with: data) as! [String: Any]
        let input = result["input"] as! [[String: Any]]
        #expect(input.count == 2)
        #expect(input[0]["type"] as? String == "message")
        #expect(input[1]["type"] as? String == "function_call_output")
        let namespaces = result["tools"] as! [[String: Any]]
        #expect(namespaces.count == 1)
        #expect(namespaces[0]["name"] as? String == "malachi")
        #expect((namespaces[0]["tools"] as! [[String: Any]]).compactMap { $0["name"] as? String } == ["read_message", "create_draft"])
        let json = String(decoding: data, as: UTF8.self)
        for forbidden in ["additional_tools", "tool_search", "send_message"] { #expect(!json.contains(forbidden)) }
    }
    @Test func additionalCatalogCannotOmitOrDuplicateRequiredTools() {
        let policy = CodexPolicy(allowed: ["read_message", "create_draft"])
        for extra in ["null", "{}", "[]", #"[{"type":"namespace","name":"malachi","tools":[{"type":"function","name":"read_message"}]}]"#] {
            let json = #"{"tools":[{"type":"namespace","name":"malachi","tools":[{"type":"function","name":"read_message"}]}],"input":[{"type":"additional_tools","tools":"# + extra + "}]}"
            #expect(throws: (any Error).self) { try policy.filterRequest(Data(json.utf8)) }
        }
        #expect(throws: (any Error).self) { try policy.filterRequest(Data(#"{"input":[{"type":"additional_tools"}]}"#.utf8)) }
        let noTools = CodexPolicy(allowed: [])
        #expect((try? noTools.filterRequest(Data(#"{"input":[{"type":"additional_tools","tools":[]}]}"#.utf8))) != nil)
    }
    @Test func responseCallsAreValidatedBeforeForwarding() throws {
        let policy = CodexPolicy(allowed: ["annotate_case"])
        try policy.validateEvent(#"{"item":{"type":"function_call","namespace":"malachi","name":"annotate_case"}}"#)
        for item in [#"{"type":"function_call","name":"annotate_case"}"#, #"{"type":"function_call","namespace":"evil","name":"annotate_case"}"#, #"{"type":"function_call","namespace":"malachi","name":"create_draft"}"#, #"{"type":"web_search_call"}"#, #"{"type":"local_shell_call"}"#] {
            #expect(throws: (any Error).self) { try policy.validateEvent("{\"item\":" + item + "}") }
            #expect(throws: (any Error).self) { try policy.validateEvent("{\"response\":{\"output\":[" + item + "]}}") }
        }
    }
    @Test func missingMimeRequiresValidatedResponsesFrame() throws {
        let policy = CodexPolicy(allowed: [])
        #expect(try CodexPolicy.isMissingStreamType(nil))
        #expect(try !CodexPolicy.isMissingStreamType("Text/Event-Stream; charset=utf-8"))
        for mime in ["application/json", "text/html", ""] {
            #expect(throws: (any Error).self) { try CodexPolicy.isMissingStreamType(mime) }
        }
        try policy.validateInitialStreamEvent(#"{"type":"response.created","response":{"output":[]}}"#)
        for data in ["{broken}", "<html>hostile</html>", #"{"type":"message"}"#, "[DONE]", #"{"type":"response.output_item.added","item":{"type":"function_call","namespace":"other","name":"exec_command"}}"#] {
            #expect(throws: (any Error).self) { try policy.validateInitialStreamEvent(data) }
        }
    }
    @Test func childEnvironmentDoesNotInheritCredentials() {
        let env = CodexPolicy.environment(["HOME": "/synthetic", "PATH": "/usr/bin", "OPENAI_API_KEY": "canary", "CODEX_HOME": "/user/profile", "MALACHI_CODEX_GATE_CREDENTIAL": "canary", "HTTP_PROXY": "hostile", "ANTHROPIC_API_KEY": "canary", "DYLD_INSERT_LIBRARIES": "hostile"])
        #expect(env == ["HOME": "/synthetic", "PATH": "/usr/bin"])
        let args = CodexPolicy.arguments(baseURL: "http://127.0.0.1:1/test/v1", home: URL(fileURLWithPath: "/private/test"))
        #expect(args.contains("history.persistence=\"none\""))
        #expect(args.contains("features.shell_tool=false"))
        #expect(args.contains("features.code_mode.direct_only_tool_namespaces=[\"malachi\"]"))
        #expect(args.contains("model_providers.malachi_chatgpt.stream_max_retries=0"))
    }
    @Test func oauthRejectsMismatchedDuplicateCallbacks() throws {
        let redirect = URL(string: "http://127.0.0.1:9999/auth/callback")!
        let result = try ChatGPTOAuth.callback(URL(string: redirect.absoluteString + "?state=expected&code=one")!, redirect: redirect, state: "expected")
        #expect(result["code"] == "one")
        for suffix in ["?state=wrong", "?state=expected&state=expected", "?state=expected#fragment"] {
            #expect(throws: (any Error).self) { try ChatGPTOAuth.callback(URL(string: redirect.absoluteString + suffix)!, redirect: redirect, state: "expected") }
        }
        #expect(!ChatGPTOAuth.hasPlanScope("openid profile email offline_access"))
        #expect(ChatGPTOAuth.hasPlanScope(ChatGPTOAuth.scope))
        #expect(throws: (any Error).self) { try ChatGPTOAuth.trustedEndpoint("https://evil.example/keys") }
        #expect(throws: (any Error).self) { try ChatGPTOAuth.trustedEndpoint("http://auth.openai.com/keys") }
    }
}

@MainActor private final class MemoryCredentials: ChatGPTCredentialStore {
    var metadata: ChatGPTRegistration?
    var value: ChatGPTTokens?
    var writes = 0
    final class Lease: ChatGPTCredentialLease { func release() {} }
    func acquire() async throws -> any ChatGPTCredentialLease { Lease() }
    func registration() throws -> ChatGPTRegistration? { metadata }
    func saveRegistration(_ value: ChatGPTRegistration) throws { metadata = value }
    func tokens() throws -> ChatGPTTokens? { value }
    func saveTokens(_ value: ChatGPTTokens) throws { self.value = value; writes += 1 }
    func deleteTokens() throws { value = nil }
}
@MainActor private final class SyntheticBrowser: ChatGPTBrowser {
    var nonce = ""
    func authorize(state: String, makeURL: (URL) throws -> URL) async throws -> (URL, URL) {
        let redirect = URL(string: "http://127.0.0.1:9999/auth/callback")!
        let url = try makeURL(redirect)
        nonce = URLComponents(url: url, resolvingAgainstBaseURL: false)!.queryItems!.first { $0.name == "nonce" }!.value!
        return (URL(string: redirect.absoluteString + "?state=" + state + "&code=synthetic&client_id=issued")!, redirect)
    }
    func cancel() {}
}
@MainActor private final class SignatureFixture: ChatGPTSignatureValidator {
    var reject = false
    var verified = 0
    func verify(signingInput: Data, signature: Data, modulus: Data, exponent: Data) throws {
        verified += 1
        if reject { throw ChatGPTFailure("chatgpt_invalid_identity") }
    }
}
@MainActor @Suite(.serialized) struct ChatGPTConnectionTests {
    private func fixture(tamper: String? = nil) -> (ChatGPTConnection, MemoryCredentials, SyntheticBrowser, SignatureFixture) {
        let store = MemoryCredentials(), browser = SyntheticBrowser(), signature = SignatureFixture()
        let connection = ChatGPTConnection(store: store, browser: browser, validator: signature) { url, fields in
            func json(_ value: [String: Any]) throws -> (Data, Int) { (try JSONSerialization.data(withJSONObject: value), 200) }
            if url.path == "/.well-known/openid-configuration" {
                return try json(["issuer": ChatGPTOAuth.issuer, "jwks_uri": ChatGPTOAuth.issuer + "/keys", "revocation_endpoint": ChatGPTOAuth.issuer + "/revoke"])
            }
            if url.path == "/keys" { return try json(["keys": [["kid": "fixture", "kty": "RSA", "n": "AQ", "e": "AQAB"]]]) }
            if url.path == "/revoke" { return (Data(), 500) }
            let header = ChatGPTOAuth.base64url(try JSONSerialization.data(withJSONObject: ["alg": "RS256", "kid": "fixture"]))
            var claims: [String: Any] = ["iss": ChatGPTOAuth.issuer, "aud": "issued", "sub": "subject", "email": "test@example.invalid", "nonce": browser.nonce, "iat": Date().timeIntervalSince1970, "exp": Date().addingTimeInterval(3600).timeIntervalSince1970]
            if let tamper { if tamper == "exp" { claims[tamper] = 1 } else { claims[tamper] = "wrong" } }
            let payload = ChatGPTOAuth.base64url(try JSONSerialization.data(withJSONObject: claims))
            return try json(["access_token": fields?["grant_type"] == "refresh_token" ? "rotated" : "access", "token_type": "Bearer", "refresh_token": "refresh", "id_token": header + "." + payload + ".AQ", "scope": ChatGPTOAuth.scope, "expires_in": 3600])
        }
        return (connection, store, browser, signature)
    }
    @Test func signatureIsRequiredBeforeCredentialsAreStored() async throws {
        let (connection, store, _, verifier) = fixture()
        verifier.reject = true
        do { try await connection.signIn(); Issue.record("invalid signature accepted") } catch {}
        #expect(store.value == nil)
        #expect(verifier.verified == 1)
        #expect(!connection.connected)
    }
    @Test func signedIdentityStillRequiresIssuerAudienceNonceLifetimeAndSubject() async {
        for claim in ["iss", "aud", "nonce", "exp", "sub"] {
            let (connection, store, _, _) = fixture(tamper: claim)
            if claim == "sub" { var metadata = ChatGPTRegistration(); metadata.subject = "expected"; store.metadata = metadata }
            do { try await connection.signIn(); Issue.record("invalid identity claim accepted: \(claim)") } catch {}
            #expect(store.value == nil)
        }
    }
    @Test func refreshRotatesOnceAcrossConcurrentRequestsAndDisconnectClearsLocally() async throws {
        let (connection, store, _, _) = fixture()
        try await connection.signIn()
        #expect(connection.connected)
        store.value?.expiresAt = Date.distantPast
        async let a = connection.accessToken()
        async let b = connection.accessToken()
        let result = try await (a, b)
        #expect(result.0 == "rotated" && result.1 == "rotated")
        #expect(store.writes == 2)
        let confirmed = try await connection.disconnect()
        #expect(!confirmed)
        #expect(store.value == nil && !connection.connected)
        #expect(store.metadata?.hostID.hasPrefix("urn:uuid:") == true)
    }
    @Test func oldInferenceRejectionDoesNotEraseRotatedCredential() async throws {
        let (connection, store, _, _) = fixture()
        try await connection.signIn()
        store.value?.expiresAt = Date.distantPast
        _ = try await connection.accessToken()
        await connection.rejected("access", status: 401)
        #expect(store.value?.accessToken == "rotated")
        await connection.rejected("rotated", status: 403)
        #expect(store.value != nil)
        await connection.rejected("rotated", status: 401)
        #expect(store.value == nil)
    }
    @Test func independentConsentAndDefaults() {
        let name = "io.github.schotek.Malachi.provider-test-" + UUID().uuidString
        let defaults = UserDefaults(suiteName: name)!
        defer { defaults.removePersistentDomain(forName: name) }
        let settings = Settings(defaults: defaults)
        settings.assistantConsent = true; settings.boardTriageConsent = true
        settings.assistantProvider = .chatgpt
        #expect(!settings.selectedAssistantConsent && !settings.selectedBoardConsent)
        settings.selectedBoardConsent = true
        #expect(!settings.selectedAssistantConsent)
        settings.assistantProvider = .claude
        #expect(settings.selectedAssistantConsent && settings.selectedBoardConsent)
    }
}

@MainActor private final class SyntheticSession: AssistantSession {
    var running = true
    var onEvents: (([Assistant.Event]) -> Void)?
    var onExit: ((String) -> Void)?
    var input: String?
    func submit(_ input: String) async throws { self.input = input }
    func terminate() { running = false }
}
@MainActor private final class SyntheticProvider: AssistantProvider {
    var available = true
    var connected = true
    var hasConsent = true
    var spec: AssistantSessionSpec?
    let session = SyntheticSession()
    func acceptConsent() { hasConsent = true }
    func start(_ spec: AssistantSessionSpec) async throws -> any AssistantSession { self.spec = spec; return session }
}
@MainActor @Suite(.serialized) struct AssistantProviderLifecycleTests {
    @Test func oneShotCarriesNoToolsAndLateResultAfterSwitchIsIgnored() async {
        let name = "io.github.schotek.Malachi.provider-test-" + UUID().uuidString
        let defaults = UserDefaults(suiteName: name)!
        defer { defaults.removePersistentDomain(forName: name) }
        let settings = Settings(defaults: defaults); settings.assistantProvider = .chatgpt
        let request = AssistantRequest(settings: settings, locator: ClaudeCodeLocator(settings: settings))
        let provider = SyntheticProvider(); request.provider = { provider }
        var answered = false
        request.start(systemPrompt: "synthetic", message: "synthetic") { _ in answered = true }
        for _ in 0..<100 where provider.session.input == nil { await Task.yield() }
        #expect(provider.session.input == "synthetic")
        #expect(provider.spec?.tools == nil)
        let late = provider.session.onEvents
        settings.assistantProvider = .claude
        #expect(!provider.session.running)
        var result = Assistant.Event(kind: .result); result.success = true; result.resultText = "stale"
        late?([result])
        #expect(!answered)
    }
    /// A provider's failure codes are the board's classes: the plan's usage
    /// limit, a lapsed connection (the sign-in offer), Codex missing.
    @Test func providerFailureClasses() {
        #expect(AssistantRequest.providerFailure("codex_not_found") == .notFound)
        for code in ["chatgpt_not_connected", "chatgpt_reconnect_required", "chatgpt_consent_required",
                     "chatgpt_permission_denied", "chatgpt_identity_mismatch"] {
            #expect(AssistantRequest.providerFailure(code) == .notSignedIn, "\(code)")
        }
        #expect(AssistantRequest.providerFailure("chatgpt_usage_limit") == .limit("chatgpt_usage_limit"))
        #expect(AssistantRequest.providerFailure("chatgpt_turn_failed") == .stopped("chatgpt_turn_failed"))
        #expect(AssistantRequest.Failure.limit("x").reason == "the assistant’s usage limit was reached")
    }

    @Test func aFailedResultMapsToItsClass() async {
        let name = "io.github.schotek.Malachi.provider-test-" + UUID().uuidString
        let defaults = UserDefaults(suiteName: name)!
        defer { defaults.removePersistentDomain(forName: name) }
        let settings = Settings(defaults: defaults); settings.assistantProvider = .chatgpt
        let request = AssistantRequest(settings: settings, locator: ClaudeCodeLocator(settings: settings))
        let provider = SyntheticProvider(); request.provider = { provider }
        var outcome: AssistantRequest.Outcome?
        request.start(systemPrompt: "synthetic", message: "synthetic") { outcome = $0 }
        for _ in 0..<100 where provider.session.input == nil { await Task.yield() }
        var result = Assistant.Event(kind: .result); result.success = false; result.resultText = "chatgpt_usage_limit"
        provider.session.onEvents?([result])
        #expect(outcome == .failed(.limit("chatgpt_usage_limit")))
    }

    /// Audit row 4: only a change that concerns the provider in effect ends
    /// a request under way.
    @Test func onlyTheActiveProvidersKeysCancel() async {
        let name = "io.github.schotek.Malachi.provider-test-" + UUID().uuidString
        let defaults = UserDefaults(suiteName: name)!
        defer { defaults.removePersistentDomain(forName: name) }
        let settings = Settings(defaults: defaults)
        // The pure rule.
        settings.assistantProvider = .claude
        for key in [Settings.Key.assistantCodexPath, .assistantChatGPTModel, .boardTriageChatGPTModel,
                    .assistantChatGPTConsentVersion, .boardTriageChatGPTConsentVersion, .assistantTarget, .assistantModel] {
            #expect(!AssistantRequest.providerChangeConcernsActive(key, settings: settings), "\(key)")
        }
        #expect(AssistantRequest.providerChangeConcernsActive(.assistantProvider, settings: settings))
        settings.assistantProvider = .chatgpt
        for key in [Settings.Key.assistantProvider, .assistantCodexPath] {
            #expect(AssistantRequest.providerChangeConcernsActive(key, settings: settings), "\(key)")
        }
        // A model applies from the next request: never a cancel.
        for key in [Settings.Key.assistantChatGPTModel, .boardTriageChatGPTModel] {
            #expect(!AssistantRequest.providerChangeConcernsActive(key, settings: settings), "\(key)")
        }
        settings.assistantChatGPTConsentVersion = 1
        #expect(!AssistantRequest.providerChangeConcernsActive(.assistantChatGPTConsentVersion, settings: settings))
        settings.assistantChatGPTConsentVersion = 0
        #expect(AssistantRequest.providerChangeConcernsActive(.assistantChatGPTConsentVersion, settings: settings))
        // A request of ChatGPT: the Claude model leaves it, the Codex path ends it.
        settings.assistantChatGPTConsentVersion = 1
        let request = AssistantRequest(settings: settings, locator: ClaudeCodeLocator(settings: settings))
        let provider = SyntheticProvider(); request.provider = { provider }
        request.start(systemPrompt: "synthetic", message: "synthetic") { _ in }
        for _ in 0..<100 where provider.session.input == nil { await Task.yield() }
        settings.assistantModel = .opus
        settings.boardTriageModel = .haiku
        #expect(request.running && provider.session.running)
        // Its own models neither: they apply from the next request.
        settings.assistantChatGPTModel = "next-model"
        settings.boardTriageChatGPTModel = "next-triage-model"
        #expect(request.running && provider.session.running)
        settings.assistantCodexPath = "/elsewhere/codex"
        #expect(!request.running && !provider.session.running)
    }

    @Test func boardUsesItsOwnConsentModelAndRestrictedBridge() async {
        let name = "io.github.schotek.Malachi.provider-test-" + UUID().uuidString
        let defaults = UserDefaults(suiteName: name)!
        defer { defaults.removePersistentDomain(forName: name) }
        let settings = Settings(defaults: defaults); settings.assistantProvider = .chatgpt; settings.selectedBoardConsent = true
        settings.assistantChatGPTModel = "panel-model"
        let request = AssistantRequest(settings: settings, locator: ClaudeCodeLocator(settings: settings))
        let provider = SyntheticProvider(); provider.hasConsent = false; request.provider = { provider }
        request.usesBoardConsent = true; request.providerModelID = { "" }
        let tools = AssistantRequest.Tools(bridge: "/synthetic/bridge", socket: "/synthetic/socket", bridgeArgs: ["--triage-run", "run1"], allowed: ["mcp__malachi__annotate_case"])
        request.start(systemPrompt: "synthetic", message: "synthetic", tools: tools) { _ in }
        for _ in 0..<100 where provider.session.input == nil { await Task.yield() }
        #expect(provider.spec?.boardConsent == true)
        #expect(provider.spec?.modelID == "")
        #expect(provider.spec?.tools == tools)
        #expect(!settings.selectedAssistantConsent)
        request.cancel()
    }
}
