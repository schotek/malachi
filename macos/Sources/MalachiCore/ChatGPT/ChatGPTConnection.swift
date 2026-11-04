// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import CryptoKit

public struct ChatGPTRegistration: Codable, Sendable {
    public var hostID: String = "urn:uuid:" + UUID().uuidString
    public var clientID: String?
    public var connectionID: String?
    public var subject: String?
    public var email: String?
    public init() {}
}
public struct ChatGPTTokens: Codable, Sendable {
    public var connectionID: String
    public var accessToken: String
    public var refreshToken: String
    public var idToken: String
    public var scope: String
    public var expiresAt: Date
}
@MainActor public protocol ChatGPTCredentialLease: AnyObject { func release() }
@MainActor public protocol ChatGPTCredentialStore {
    func acquire() async throws -> any ChatGPTCredentialLease
    func registration() throws -> ChatGPTRegistration?
    func saveRegistration(_ value: ChatGPTRegistration) throws
    func tokens() throws -> ChatGPTTokens?
    func saveTokens(_ value: ChatGPTTokens) throws
    func deleteTokens() throws
}
@MainActor public protocol ChatGPTBrowser {
    func authorize(state: String, makeURL: (URL) throws -> URL) async throws -> (URL, URL)
    func cancel()
}
/// Implemented with Apple Security's SecKeyVerifySignature, never JWT decoding alone.
@MainActor public protocol ChatGPTSignatureValidator {
    func verify(signingInput: Data, signature: Data, modulus: Data, exponent: Data) throws
}
public enum ChatGPTOAuth {
    public static let issuer = "https://auth.openai.com"
    public static let resource = "https://api.openai.com/v1"
    public static let scope = "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"
    public static let dynamicClient = "dynamic_agent_client"
    public static func random() -> String { var rng = SystemRandomNumberGenerator(); return base64url(Data((0..<32).map { _ in UInt8.random(in: 0...255, using: &rng) })) }
    public static func base64url(_ data: Data) -> String { data.base64EncodedString().replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "") }
    public static func decode(_ text: String) throws -> Data {
        guard text.utf8.allSatisfy({ (65...90).contains($0) || (97...122).contains($0) || (48...57).contains($0) || $0 == 45 || $0 == 95 }) else { throw ChatGPTFailure("chatgpt_invalid_identity") }
        let padded = text.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/") + String(repeating: "=", count: (4 - text.count % 4) % 4)
        guard let data = Data(base64Encoded: padded) else { throw ChatGPTFailure("chatgpt_invalid_identity") }; return data
    }
    public static func hasPlanScope(_ text: String) -> Bool { Set(scope.split(separator: " ")).isSubset(of: Set(text.split(separator: " "))) }
    public static func authorize(_ registration: ChatGPTRegistration, redirect: URL, state: String, nonce: String, verifier: String) throws -> URL {
        guard redirect.scheme == "http", redirect.host == "127.0.0.1", redirect.path == "/auth/callback", redirect.query == nil, redirect.fragment == nil, redirect.user == nil else { throw ChatGPTFailure("chatgpt_invalid_callback") }
        var fields = ["client_id": registration.clientID ?? dynamicClient, "ext_agent_host_id": registration.hostID,
                      "response_type": "code", "redirect_uri": redirect.absoluteString, "scope": scope, "resource": resource,
                      "state": state, "nonce": nonce, "code_challenge_method": "S256", "code_challenge": base64url(Data(SHA256.hash(data: Data(verifier.utf8))))]
        if registration.clientID == nil { fields["agent_name_hint"] = "Malachi Mail" }
        else { fields["login_hint"] = registration.email }
        var url = URLComponents(string: issuer + "/api/accounts/authorize")!
        url.queryItems = fields.sorted { $0.key < $1.key }.map { URLQueryItem(name: $0.key, value: $0.value) }
        return url.url!
    }
    public static func callback(_ url: URL, redirect: URL, state: String) throws -> [String: String] {
        guard url.scheme == redirect.scheme, url.host == redirect.host, url.port == redirect.port, url.path == redirect.path, url.fragment == nil, url.user == nil else { throw ChatGPTFailure("chatgpt_invalid_callback") }
        var fields: [String: String] = [:]
        for item in URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? [] {
            guard fields[item.name] == nil else { throw ChatGPTFailure("chatgpt_invalid_callback") }
            fields[item.name] = item.value ?? ""
        }
        guard let received = fields["state"], received.utf8.count == state.utf8.count else { throw ChatGPTFailure("chatgpt_invalid_callback") }
        var different: UInt8 = 0
        for (a, b) in zip(received.utf8, state.utf8) { different |= a ^ b }
        guard different == 0 else { throw ChatGPTFailure("chatgpt_invalid_callback") }
        return fields
    }
    public static func trustedEndpoint(_ value: String?) throws -> URL {
        guard let value, let url = URL(string: value), url.scheme == "https", url.host == "auth.openai.com", url.port == nil || url.port == 443, url.user == nil, url.password == nil, url.fragment == nil else { throw ChatGPTFailure("chatgpt_invalid_endpoint") }
        return url
    }
}

/// Disable redirects for token requests as well as inference; no cookies or disk cache.
final class ChatGPTNoRedirect: NSObject, URLSessionTaskDelegate, @unchecked Sendable {
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) { completionHandler(nil) }
}
@MainActor public final class ChatGPTConnection {
    public private(set) var connected = false
    public private(set) var connecting = false
    public private(set) var email = ""
    public var onChange: (() -> Void)?
    public var onInvalidated: (() -> Void)?
    private let store: any ChatGPTCredentialStore
    private let browser: any ChatGPTBrowser
    private let validator: any ChatGPTSignatureValidator
    public typealias Transport = @MainActor (URL, [String: String]?) async throws -> (Data, Int)
    private let transport: Transport?
    private let session: URLSession
    private var busy = false
    private var generation = 0
    public init(store: any ChatGPTCredentialStore, browser: any ChatGPTBrowser, validator: any ChatGPTSignatureValidator, transport: Transport? = nil) {
        self.store = store; self.browser = browser; self.validator = validator; self.transport = transport
        let config = URLSessionConfiguration.ephemeral; config.httpCookieStorage = nil; config.urlCache = nil
        session = URLSession(configuration: config, delegate: ChatGPTNoRedirect(), delegateQueue: nil)
    }
    private func lock() async throws -> any ChatGPTCredentialLease {
        while busy { try await Task.sleep(for: .milliseconds(20)) }
        busy = true
        do { return try await store.acquire() } catch { busy = false; throw error }
    }
    private func registration() throws -> ChatGPTRegistration {
        if let value = try store.registration() { return value }
        let value = ChatGPTRegistration(); try store.saveRegistration(value); return value
    }
    public func load() async throws {
        let lease = try await lock(); defer { lease.release(); busy = false }
        let registration = try registration(); let token = try store.tokens()
        connected = token != nil && !(registration.clientID ?? "").isEmpty && !(registration.subject ?? "").isEmpty && !(registration.connectionID ?? "").isEmpty && token?.connectionID == registration.connectionID && ChatGPTOAuth.hasPlanScope(token?.scope ?? "")
        email = registration.email ?? ""; onChange?()
    }
    public func cancel() { generation += 1; browser.cancel(); onInvalidated?() }
    public func signIn() async throws {
        cancel(); let my = generation
        let lease = try await lock(); defer { lease.release(); busy = false; connecting = false; onChange?() }
        guard my == generation else { throw CancellationError() }
        connecting = true; onChange?()
        var registration = try registration()
        let state = ChatGPTOAuth.random(), nonce = ChatGPTOAuth.random(), verifier = ChatGPTOAuth.random()
        let (callback, redirect) = try await browser.authorize(state: state) { redirect in
            try ChatGPTOAuth.authorize(registration, redirect: redirect, state: state, nonce: nonce, verifier: verifier)
        }
        let fields = try ChatGPTOAuth.callback(callback, redirect: redirect, state: state)
        guard fields["error"] == nil, let code = fields["code"], !code.isEmpty,
              let client = registration.clientID ?? fields["client_id"], !client.isEmpty, client.count <= 512, client != ChatGPTOAuth.dynamicClient,
              registration.clientID == nil || fields["client_id"] == nil || fields["client_id"] == registration.clientID else { throw ChatGPTFailure("chatgpt_invalid_response") }
        registration.clientID = client; try store.saveRegistration(registration)
        let response = try await exchange(["grant_type": "authorization_code", "client_id": client, "code": code,
                                           "code_verifier": verifier, "redirect_uri": redirect.absoluteString, "resource": ChatGPTOAuth.resource])
        guard let id = response["id_token"] as? String, let refresh = response["refresh_token"] as? String, !refresh.isEmpty,
              let scope = response["scope"] as? String, ChatGPTOAuth.hasPlanScope(scope) else { throw ChatGPTFailure("chatgpt_permission_denied") }
        let identity = try await validate(id, client: client, nonce: nonce, subject: registration.subject)
        guard my == generation else { throw CancellationError() }
        registration.subject = identity["sub"] as? String; registration.email = identity["email"] as? String
        registration.connectionID = registration.connectionID ?? UUID().uuidString
        try store.saveRegistration(registration)
        try store.saveTokens(ChatGPTTokens(connectionID: registration.connectionID!, accessToken: response["access_token"] as! String,
            refreshToken: refresh, idToken: id, scope: scope, expiresAt: Date().addingTimeInterval(response["expires_in"] as! Double)))
        connected = true; email = registration.email ?? ""
    }
    public func accessToken() async throws -> String {
        let my = generation
        let lease = try await lock(); defer { lease.release(); busy = false }
        let registration = try registration()
        guard var tokens = try store.tokens(), let client = registration.clientID, let subject = registration.subject,
              tokens.connectionID == registration.connectionID, ChatGPTOAuth.hasPlanScope(tokens.scope) else { throw ChatGPTFailure("chatgpt_not_connected") }
        if tokens.expiresAt > Date().addingTimeInterval(120) { return tokens.accessToken }
        do {
            let response = try await exchange(["grant_type": "refresh_token", "client_id": client, "refresh_token": tokens.refreshToken, "resource": ChatGPTOAuth.resource])
            let scope = response["scope"] as? String ?? tokens.scope
            guard ChatGPTOAuth.hasPlanScope(scope) else { throw ChatGPTFailure("chatgpt_permission_denied") }
            if let id = response["id_token"] as? String { _ = try await validate(id, client: client, nonce: nil, subject: subject); tokens.idToken = id }
            guard generation == my else { throw CancellationError() }
            tokens.accessToken = response["access_token"] as! String; tokens.refreshToken = response["refresh_token"] as? String ?? tokens.refreshToken
            tokens.scope = scope; tokens.expiresAt = Date().addingTimeInterval(response["expires_in"] as! Double)
            try store.saveTokens(tokens)
            return tokens.accessToken
        } catch let error as ChatGPTFailure {
            if error.code != "chatgpt_network" { try store.deleteTokens(); connected = false; cancel(); onChange?() }
            throw error
        }
    }
    public func rejected(_ token: String, status: Int) async {
        guard status == 401 else { return }
        do {
            let lease = try await lock(); defer { lease.release(); busy = false }
            if try store.tokens()?.accessToken == token { try store.deleteTokens(); connected = false; cancel(); onChange?() }
        } catch { connected = false; cancel(); onChange?() }
    }
    public func disconnect() async throws -> Bool {
        cancel()
        let lease = try await lock(); defer { lease.release(); busy = false }
        let registration: ChatGPTRegistration
        let tokens: ChatGPTTokens?
        do { registration = try self.registration(); tokens = try store.tokens() }
        catch {
            connected = false; onChange?()
            try store.deleteTokens()
            throw ChatGPTFailure("chatgpt_storage")
        }
        // Clear local credentials before waiting for an optional remote revocation.
        try store.deleteTokens(); connected = false; onChange?()
        guard let tokens, let client = registration.clientID else { return true }
        do {
            let discovery = try await get(URL(string: ChatGPTOAuth.issuer + "/.well-known/openid-configuration")!)
            guard discovery["issuer"] as? String == ChatGPTOAuth.issuer else { return false }
            let endpoint = try ChatGPTOAuth.trustedEndpoint(discovery["revocation_endpoint"] as? String)
            let (_, status) = try await request(endpoint, fields: ["token": tokens.refreshToken, "token_type_hint": "refresh_token", "client_id": client])
            return status == 200
        } catch { return false }
    }
    private func exchange(_ fields: [String: String]) async throws -> [String: Any] {
        let (data, status) = try await request(URL(string: ChatGPTOAuth.issuer + "/api/accounts/oauth/token")!, fields: fields)
        guard let root = try JSONSerialization.jsonObject(with: data) as? [String: Any] else { throw ChatGPTFailure("chatgpt_invalid_response") }
        guard (200..<300).contains(status) else { throw ChatGPTFailure(root["error"] as? String == "invalid_grant" ? "chatgpt_reconnect_required" : status >= 500 ? "chatgpt_network" : "chatgpt_permission_denied") }
        guard let access = root["access_token"] as? String, !access.isEmpty, !access.unicodeScalars.contains(where: { CharacterSet.whitespacesAndNewlines.contains($0) }), (root["token_type"] as? String)?.lowercased() == "bearer",
              let seconds = root["expires_in"] as? Double, seconds > 0, seconds <= 604800 else { throw ChatGPTFailure("chatgpt_invalid_response") }
        return root
    }
    private func get(_ url: URL) async throws -> [String: Any] {
        let (data, status) = try await request(url)
        guard status == 200, let root = try JSONSerialization.jsonObject(with: data) as? [String: Any] else { throw ChatGPTFailure("chatgpt_network") }; return root
    }
    private func request(_ url: URL, fields: [String: String]? = nil) async throws -> (Data, Int) {
        if let transport { return try await transport(url, fields) }
        var request = URLRequest(url: url, timeoutInterval: 20)
        if let fields {
            request.httpMethod = "POST"; request.setValue("application/x-www-form-urlencoded", forHTTPHeaderField: "Content-Type")
            let allowed = CharacterSet.alphanumerics.union(CharacterSet(charactersIn: "-._~"))
            request.httpBody = Data(fields.sorted { $0.key < $1.key }.map { $0.key.addingPercentEncoding(withAllowedCharacters: allowed)! + "=" + $0.value.addingPercentEncoding(withAllowedCharacters: allowed)! }.joined(separator: "&").utf8)
        }
        let (bytes, response) = try await session.bytes(for: request)
        guard let http = response as? HTTPURLResponse else { throw ChatGPTFailure("chatgpt_network") }
        var data = Data()
        for try await byte in bytes { guard data.count < 256 * 1024 else { throw ChatGPTFailure("chatgpt_response_limit") }; data.append(byte) }
        return (data, http.statusCode)
    }
    private func validate(_ token: String, client: String, nonce: String?, subject: String?) async throws -> [String: Any] {
        guard token.utf8.count <= 65536 else { throw ChatGPTFailure("chatgpt_invalid_identity") }
        let parts = token.split(separator: ".", omittingEmptySubsequences: false).map(String.init)
        guard parts.count == 3, let header = try JSONSerialization.jsonObject(with: ChatGPTOAuth.decode(parts[0])) as? [String: Any],
              header["alg"] as? String == "RS256", header["crit"] == nil, let kid = header["kid"] as? String else { throw ChatGPTFailure("chatgpt_invalid_identity") }
        let discovery = try await get(URL(string: ChatGPTOAuth.issuer + "/.well-known/openid-configuration")!)
        guard discovery["issuer"] as? String == ChatGPTOAuth.issuer else { throw ChatGPTFailure("chatgpt_invalid_identity") }
        let jwks = try await get(ChatGPTOAuth.trustedEndpoint(discovery["jwks_uri"] as? String))
        let keys = (jwks["keys"] as? [[String: Any]] ?? []).filter { $0["kid"] as? String == kid && $0["kty"] as? String == "RSA" }
        guard keys.count == 1, let key = keys.first, key["use"] == nil || key["use"] as? String == "sig",
              key["alg"] == nil || key["alg"] as? String == "RS256", let n = key["n"] as? String, let e = key["e"] as? String else { throw ChatGPTFailure("chatgpt_invalid_identity") }
        try validator.verify(signingInput: Data((parts[0] + "." + parts[1]).utf8), signature: ChatGPTOAuth.decode(parts[2]), modulus: ChatGPTOAuth.decode(n), exponent: ChatGPTOAuth.decode(e))
        guard let claims = try JSONSerialization.jsonObject(with: ChatGPTOAuth.decode(parts[1])) as? [String: Any] else { throw ChatGPTFailure("chatgpt_invalid_identity") }
        let audience = (claims["aud"] as? [String]) ?? (claims["aud"] as? String).map { [$0] } ?? []
        let now = Date().timeIntervalSince1970
        guard claims["iss"] as? String == ChatGPTOAuth.issuer, audience.contains(client),
              let exp = claims["exp"] as? Double, let iat = claims["iat"] as? Double, exp > now - 60, iat <= now + 60, iat < exp,
              let sub = claims["sub"] as? String, !sub.isEmpty, nonce == nil || claims["nonce"] as? String == nonce,
              subject == nil || subject == sub else { throw ChatGPTFailure("chatgpt_invalid_identity") }
        if let before = claims["nbf"] as? Double, before > now + 60 || before >= exp { throw ChatGPTFailure("chatgpt_invalid_identity") }
        if audience.count > 1 || claims["azp"] != nil { guard claims["azp"] as? String == client else { throw ChatGPTFailure("chatgpt_invalid_identity") } }
        return claims
    }
}
