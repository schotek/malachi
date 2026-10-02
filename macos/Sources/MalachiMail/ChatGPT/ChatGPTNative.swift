// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import Security
import CryptoKit
import Darwin
import MalachiCore

@MainActor final class ChatGPTKeychain: ChatGPTCredentialStore {
    private let directory: URL
    private let service: String
    init(directory: URL) {
        self.directory = directory
        let identity = SHA256.hash(data: Data(directory.standardizedFileURL.path.utf8)).map { String(format: "%02x", $0) }.joined()
        service = "io.github.schotek.Malachi.chatgpt." + identity
    }
    private var query: [String: Any] { [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service, kSecAttrAccount as String: "siwc-grant"] }
    func acquire() async throws -> any ChatGPTCredentialLease {
        guard let fd = try CodexPrivateFiles.openFile(directory: directory, name: "grant.lock", create: true) else { throw ChatGPTFailure("chatgpt_storage") }
        do {
            while flock(fd, LOCK_EX | LOCK_NB) != 0 { guard errno == EWOULDBLOCK else { throw ChatGPTFailure("chatgpt_storage") }; try await Task.sleep(for: .milliseconds(40)) }
        } catch { Darwin.close(fd); throw error }
        return Lease(fd)
    }
    private final class Lease: ChatGPTCredentialLease {
        private var fd: Int32
        init(_ fd: Int32) { self.fd = fd }
        func release() { if fd >= 0 { flock(fd, LOCK_UN); Darwin.close(fd); fd = -1 } }
    }
    func registration() throws -> ChatGPTRegistration? {
        guard let data = try CodexPrivateFiles.read(directory: directory, name: "registration.json") else { return nil }
        return try JSONDecoder().decode(ChatGPTRegistration.self, from: data)
    }
    func saveRegistration(_ value: ChatGPTRegistration) throws {
        try CodexPrivateFiles.write(JSONEncoder().encode(value), directory: directory, name: "registration.json")
    }
    func tokens() throws -> ChatGPTTokens? {
        var request = query; request[kSecReturnData as String] = true; request[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(request as CFDictionary, &result)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = result as? Data else { throw ChatGPTFailure("chatgpt_keychain") }
        return try JSONDecoder().decode(ChatGPTTokens.self, from: data)
    }
    func saveTokens(_ value: ChatGPTTokens) throws {
        let data = try JSONEncoder().encode(value)
        let update = [kSecValueData as String: data] as CFDictionary
        var status = SecItemUpdate(query as CFDictionary, update)
        if status == errSecItemNotFound {
            var create = query; create[kSecValueData as String] = data
            create[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
            status = SecItemAdd(create as CFDictionary, nil)
        }
        guard status == errSecSuccess else { throw ChatGPTFailure("chatgpt_keychain") }
    }
    func deleteTokens() throws {
        let status = SecItemDelete(query as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else { throw ChatGPTFailure("chatgpt_keychain") }
    }
}

@MainActor final class ChatGPTAppleSignature: ChatGPTSignatureValidator {
    func verify(signingInput: Data, signature: Data, modulus: Data, exponent: Data) throws {
        guard modulus.count >= 256, modulus.count <= 1024, !exponent.isEmpty, exponent.count <= 8 else { throw ChatGPTFailure("chatgpt_invalid_identity") }
        // ASN.1 encodes the public key only; all signature crypto is Apple's.
        func der(_ tag: UInt8, _ bytes: Data) -> Data {
            var length = bytes.count, encoding = Data()
            if length < 128 { encoding.append(UInt8(length)) }
            else { var digits = [UInt8](); while length > 0 { digits.insert(UInt8(length & 255), at: 0); length >>= 8 }; encoding.append(0x80 | UInt8(digits.count)); encoding.append(contentsOf: digits) }
            return Data([tag]) + encoding + bytes
        }
        func integer(_ value: Data) -> Data { der(2, value.first.map { $0 & 0x80 != 0 } == true ? Data([0]) + value : value) }
        let encoded = der(0x30, integer(modulus) + integer(exponent))
        let attributes = [kSecAttrKeyType as String: kSecAttrKeyTypeRSA, kSecAttrKeyClass as String: kSecAttrKeyClassPublic] as CFDictionary
        var error: Unmanaged<CFError>?
        guard let key = SecKeyCreateWithData(encoded as CFData, attributes, &error),
              SecKeyIsAlgorithmSupported(key, .verify, .rsaSignatureMessagePKCS1v15SHA256),
              SecKeyVerifySignature(key, .rsaSignatureMessagePKCS1v15SHA256, signingInput as CFData, signature as CFData, &error) else { throw ChatGPTFailure("chatgpt_invalid_identity") }
    }
}

@MainActor final class ChatGPTSystemBrowser: ChatGPTBrowser {
    private var listener: ChatGPTLoopback?
    private var pending: CheckedContinuation<(URL, URL), Error>?
    private var timer: Task<Void, Never>?
    func authorize(state: String, makeURL: (URL) throws -> URL) async throws -> (URL, URL) {
        cancel()
        let server = ChatGPTLoopback(); listener = server
        try await server.start { [weak self] request, response in
            guard let self, let pending = self.pending, request.method == "GET", request.body.isEmpty,
                  let redirect = URL(string: "http://127.0.0.1:\(server.port)/auth/callback"),
                  let url = URL(string: "http://127.0.0.1:\(server.port)" + request.target),
                  (try? ChatGPTOAuth.callback(url, redirect: redirect, state: state)) != nil else {
                try await response.header(status: 400, type: "text/plain"); return
            }
            self.pending = nil; self.timer?.cancel()
            try? await response.header(status: 200, type: "text/plain; charset=utf-8")
            // macOS-only string: browser completion is not application UI.
            try? await response.write(Data("You can return to Malachi Mail.".utf8))
            pending.resume(returning: (url, redirect))
            server.close(); self.listener = nil
        }
        let redirect = URL(string: "http://127.0.0.1:\(server.port)/auth/callback")!
        let url = try makeURL(redirect)
        return try await withCheckedThrowingContinuation { c in
            pending = c
            timer = Task { @MainActor [weak self] in try? await Task.sleep(for: .seconds(300)); if !Task.isCancelled { self?.cancel() } }
            if !NSWorkspace.shared.open(url) { cancel() }
        }
    }
    func cancel() {
        timer?.cancel(); timer = nil; listener?.close(); listener = nil
        let c = pending; pending = nil; c?.resume(throwing: CancellationError())
    }
}
