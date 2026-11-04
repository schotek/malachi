// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The connection handshake of docs/api.md §1.4, re-declared from
// backend/pkg/api (types.go, auth.go): the params and result of
// system.hello and system.authenticate, and what a client needs to take
// part (the key file's path and format, the nonces, the proofs).
// `RPCClient` runs the exchange in `connect()`; `DaemonKey` reads the key.

import CryptoKit
import Foundation

/// api.SystemHelloParams: the first line of every connection. The method
/// name, this parameter and `SystemHelloResult.protocolVersion` never
/// change, so that every client can tell every daemon's protocol.
public struct SystemHelloParams: Codable, Sendable, Equatable {
    /// 64 lowercase hex digits: 32 random bytes, new for every connection.
    public var clientNonce: String

    public init(clientNonce: String) {
        self.clientNonce = clientNonce
    }
}

/// api.SystemHelloResult. `protocolVersion` is compared before anything
/// else, and before the key file is read.
public struct SystemHelloResult: Codable, Sendable, Equatable {
    public var protocolVersion: Int
    /// 64 lowercase hex digits.
    public var daemonNonce: String
    /// The daemon's proof as 64 lowercase hex digits.
    public var daemonProof: String

    public init(protocolVersion: Int, daemonNonce: String, daemonProof: String) {
        self.protocolVersion = protocolVersion
        self.daemonNonce = daemonNonce
        self.daemonProof = daemonProof
    }
}

/// api.SystemAuthenticateParams: the second line of every connection. Its
/// result is empty (`EmptyResult`): the answer itself says that the
/// connection is usable.
public struct SystemAuthenticateParams: Codable, Sendable, Equatable {
    /// The client's proof as 64 lowercase hex digits.
    public var clientProof: String

    public init(clientProof: String) {
        self.clientProof = clientProof
    }
}

/// The client's side of the handshake's cryptography (api.KeyPath,
/// ParseKeyFile, ParseAuthHex, DaemonProof, ClientProof). Keys, nonces and
/// proofs are plain `Data` of 32 bytes; CryptoKit's types exist only inside
/// these functions, so nothing of them is ever stored or crosses an actor.
/// Nothing here logs, and no error or description carries a key, a nonce or
/// a proof.
enum RPCAuth {
    /// The key file is the socket's path with this suffix (api.KeyFileSuffix).
    static let keyFileSuffix = ".key"
    /// The exact size of a key file: 64 lowercase hex digits and "\n"
    /// (api.KeyFileSize).
    static let keyFileSize = 65
    /// The longest line the client reads during the handshake, "\n"
    /// included (api maxHandshakeLine). The answers are a few hundred bytes.
    static let maxHandshakeLine = 64 << 10
    /// How many notifications the client skips before the system.hello
    /// answer (api maxHandshakeNotifications): a daemon of protocol 1
    /// broadcasts them to every connection, and its methodNotFound must
    /// still be reached to report the mismatch.
    static let maxSkippedNotifications = 8

    /// Starts every proof's message: it keeps these MACs apart from any
    /// other use of a key and names the construction's version.
    static let label = "malachi-rpc-auth-v1"
    /// The roles of the two proofs: a proof made for one side is never
    /// valid for the other, so a peer cannot reflect the proof it was sent.
    static let roleDaemon = "daemon"
    static let roleClient = "client"

    /// The key file that belongs to the socket at `socket` (api.KeyPath):
    /// beside it, in the socket's private directory.
    static func keyPath(socket: String) -> String {
        socket + keyFileSuffix
    }

    /// 32 fresh random bytes from the system's cryptographic generator (a new
    /// CryptoKit key, taken apart at once): a nonce, or a key in tests.
    static func newNonce() -> Data {
        SymmetricKey(size: .bits256).withUnsafeBytes { Data($0) }
    }

    /// The key in the content of a key file: exactly 64 lowercase hex digits
    /// and one "\n", nothing before, between or after (api.ParseKeyFile);
    /// nil for anything else.
    static func parseKey(_ content: Data) -> Data? {
        guard content.count == keyFileSize, content.last == 0x0A else {
            return nil
        }
        return decodeLowerHex32(content.prefix(keyFileSize - 1))
    }

    /// A nonce or a proof from the wire: exactly 64 lowercase hex digits, no
    /// prefix, no white space, no upper case (api.ParseAuthHex). It works on
    /// the UTF-8 bytes, so a character of several bytes is never a digit.
    static func decodeHex32(_ text: String) -> Data? {
        decodeLowerHex32(Array(text.utf8))
    }

    /// The bytes as lowercase hex digits, their form on the wire.
    static func hex(_ bytes: Data) -> String {
        let digits = Array("0123456789abcdef".utf8)
        var out: [UInt8] = []
        out.reserveCapacity(bytes.count * 2)
        for b in bytes {
            out.append(digits[Int(b >> 4)])
            out.append(digits[Int(b & 0x0F)])
        }
        return String(decoding: out, as: UTF8.self)
    }

    /// The daemon's proof on a connection (api.DaemonProof): HMAC-SHA256
    /// under the key of `message(roleDaemon, …)`.
    static func daemonProof(key: Data, clientNonce: Data, daemonNonce: Data) -> Data {
        proof(key: key, message: message(roleDaemon, clientNonce, daemonNonce))
    }

    /// The client's proof on a connection (api.ClientProof): as the
    /// daemon's, with the role "client".
    static func clientProof(key: Data, clientNonce: Data, daemonNonce: Data) -> Data {
        proof(key: key, message: message(roleClient, clientNonce, daemonNonce))
    }

    /// Whether `proof` is the daemon's proof for these nonces under the key,
    /// compared in constant time (CryptoKit's HMAC verification).
    static func isValidDaemonProof(_ proof: Data, key: Data, clientNonce: Data, daemonNonce: Data) -> Bool {
        HMAC<SHA256>.isValidAuthenticationCode(
            proof, authenticating: message(roleDaemon, clientNonce, daemonNonce), using: SymmetricKey(data: key)
        )
    }

    /// The 91 bytes a proof authenticates (docs/api.md §1.4): the label,
    /// 0x00, the role, 0x00, then both nonces as raw bytes, not hex.
    static func message(_ role: String, _ clientNonce: Data, _ daemonNonce: Data) -> Data {
        var m = Data(label.utf8)
        m.append(0)
        m.append(contentsOf: role.utf8)
        m.append(0)
        m.append(clientNonce)
        m.append(daemonNonce)
        return m
    }

    private static func proof(key: Data, message: Data) -> Data {
        Data(HMAC<SHA256>.authenticationCode(for: message, using: SymmetricKey(data: key)))
    }

    /// Exactly 64 lowercase hex digits as 32 bytes, nil otherwise.
    private static func decodeLowerHex32<C: Collection>(_ digits: C) -> Data? where C.Element == UInt8 {
        guard digits.count == 64 else {
            return nil
        }
        var out = Data(capacity: 32)
        var it = digits.makeIterator()
        while let hi = it.next(), let lo = it.next() {
            guard let h = nibble(hi), let l = nibble(lo) else {
                return nil
            }
            out.append(h << 4 | l)
        }
        return out
    }

    private static func nibble(_ c: UInt8) -> UInt8? {
        switch c {
        case UInt8(ascii: "0")...UInt8(ascii: "9"):
            return c - UInt8(ascii: "0")
        case UInt8(ascii: "a")...UInt8(ascii: "f"):
            return c - UInt8(ascii: "a") + 10
        default:
            return nil
        }
    }
}
