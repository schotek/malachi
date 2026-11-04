// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The pure half of malachi-keychain: the protocol the daemon's
// internal/auth/helper speaks, parsed and validated without touching the
// Keychain, so that it can be tested without prompts.
import Foundation

/// Exit statuses of the helper protocol (backend/internal/auth/helper).
public enum HelperExit: Int32, Error, Sendable, Equatable {
    case ok = 0
    case failure = 1
    case notFound = 2
    case badRequest = 3
}

/// What the daemon asks for: argv[1].
public enum Operation: String, Sendable, CaseIterable {
    case get, set, delete
}

/// The one JSON line on stdin: `{"account":"…","key":"…"}`, plus `"value"`
/// for set. Values never appear in argv or the environment.
public struct Request: Codable, Equatable, Sendable {
    public var account: String
    public var key: String
    public var value: String?

    /// The Keychain service every item is filed under.
    public static let service = "io.github.schotek.Malachi"
    /// More than this on stdin is not a request.
    public static let maxInput = 1 << 20
    /// Account ids and keys are opaque identifiers of the daemon
    /// (`acc_…`, `password`, `oauth2.refresh_token`); anything else is
    /// rejected before it can reach a Keychain attribute.
    public static let maxIdentifier = 128

    public init(account: String, key: String, value: String? = nil) {
        self.account = account
        self.key = key
        self.value = value
    }

    /// `kSecAttrAccount`: one item per account and key.
    public var itemAccount: String { "\(account)/\(key)" }
    /// What Keychain Access shows; it names the account id and key only.
    public var label: String { "Malachi Mail: \(account) (\(key))" }

    /// Validates the operation and decodes the request; every failure is
    /// `badRequest`, without a word about the data.
    public static func parse(op: String, data: Data) -> Result<Request, HelperExit> {
        guard let operation = Operation(rawValue: op) else {
            return .failure(.badRequest)
        }
        guard data.count <= maxInput else {
            return .failure(.badRequest)
        }
        guard let request = try? JSONDecoder().decode(Request.self, from: data) else {
            return .failure(.badRequest)
        }
        guard isIdentifier(request.account), isIdentifier(request.key) else {
            return .failure(.badRequest)
        }
        if operation == .set && request.value == nil {
            return .failure(.badRequest)
        }
        return .success(request)
    }

    /// `^[A-Za-z0-9._-]{1,128}$`
    static func isIdentifier(_ s: String) -> Bool {
        let scalars = s.unicodeScalars
        guard !scalars.isEmpty, scalars.count <= maxIdentifier else { return false }
        return scalars.allSatisfy { c in
            switch c {
            case "A"..."Z", "a"..."z", "0"..."9", ".", "_", "-":
                return true
            default:
                return false
            }
        }
    }

    /// The `get` answer: one JSON line `{"value":"…"}`. JSON escaping keeps
    /// a value with line breaks on one line.
    public static func valueLine(_ value: String) -> String {
        // A [String: String] cannot fail to encode; the fallback is never hit.
        let data = (try? JSONEncoder().encode(["value": value])) ?? Data("{}".utf8)
        return String(decoding: data, as: UTF8.self) + "\n"
    }
}
