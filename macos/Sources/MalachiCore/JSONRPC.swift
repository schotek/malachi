// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Wire shapes of the daemon's JSON-RPC 2.0 contract (docs/api.md §1–§2).
// Decoding is done in two passes: `Envelope` classifies a line (a response
// has an id, a notification a method and no id), and the caller decodes the
// `result` or `params` once it knows the type. `error.data` is not decoded
// yet; add a JSON value type when a method needs it (attachmentTooBig).

import Foundation

/// The JSON-RPC error object. `code` is the stable enumeration of
/// docs/api.md §2; `message` is human-readable and never matched on.
public struct RPCError: Error, Codable, Sendable, Equatable {
    public let code: Int
    public let message: String

    public init(code: Int, message: String) {
        self.code = code
        self.message = message
    }
}

extension RPCError: CustomStringConvertible {
    public var description: String { "\(message) (\(code))" }
}

/// A request as the client sends it: id echoed by the daemon, params always
/// an object (`EmptyParams` for methods without any).
struct Request<P: Encodable>: Encodable {
    var jsonrpc = "2.0"
    let id: Int
    let method: String
    let params: P
}

/// First decoding pass over an incoming line.
struct Envelope: Decodable {
    let id: Int?
    let method: String?
    let error: RPCError?

    var isResponse: Bool { id != nil }
    var isNotification: Bool { id == nil && method != nil }
}

struct ResultEnvelope<R: Decodable>: Decodable {
    let result: R
}

struct ParamsEnvelope<P: Decodable>: Decodable {
    let params: P
}

/// A server-initiated notification (docs/api.md §5). The params are decoded
/// on demand from the whole line, so the client needs no generic JSON type.
/// (Named to stay clear of Foundation.Notification.)
public struct RPCNotification: Sendable {
    public let method: String
    public let line: Data

    public func params<P: Decodable>(_ type: P.Type) throws -> P {
        try JSONCoding.decoder().decode(ParamsEnvelope<P>.self, from: line).params
    }
}

/// Encodes as `{}`, for methods that take no parameters.
public struct EmptyParams: Codable, Sendable {
    public init() {}
}

enum JSONCoding {
    // Fresh coders per use: cheap, and no shared mutable state to reason about
    // under strict concurrency.
    static func encoder() -> JSONEncoder { JSONEncoder() }
    static func decoder() -> JSONDecoder { JSONDecoder() }
}
