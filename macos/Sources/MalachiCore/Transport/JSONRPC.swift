// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Wire shapes of the daemon's JSON-RPC 2.0 contract (docs/api.md §1–§2).
// Decoding is done in two passes: `Envelope` classifies a line (a response
// has an id, a notification a method and no id), and the caller decodes the
// `result` or `params` once it knows the type. `error.data` is kept as a
// generic `JSONValue`; `RPCError.attachmentTooBig` reads the one shape the
// contract defines for it.

import Foundation

/// The JSON-RPC error object (api.Error). `code` is the stable enumeration
/// of docs/api.md §2; `message` is human-readable and never matched on;
/// `data` is optional, method-specific detail. The same shape appears inside
/// results (`EndpointTestResult.error`, `OutboxInfo.error`, `SyncState.error`).
public struct RPCError: Error, Codable, Sendable, Equatable {
    public let code: ErrorCode
    public let message: String
    public let data: JSONValue?

    public init(code: ErrorCode, message: String, data: JSONValue? = nil) {
        self.code = code
        self.message = message
        self.data = data
    }
}

extension RPCError: CustomStringConvertible {
    public var description: String { "\(message) (\(code.rawValue))" }
}

/// An arbitrary JSON value, for the parts of the contract that are not typed
/// (`error.data`). Numbers are kept as `Double`, as JSON has no integers.
public indirect enum JSONValue: Codable, Sendable, Equatable {
    case null
    case bool(Bool)
    case number(Double)
    case string(String)
    case array([JSONValue])
    case object([String: JSONValue])

    public init(from decoder: any Decoder) throws {
        let c = try decoder.singleValueContainer()
        if c.decodeNil() {
            self = .null
        } else if let b = try? c.decode(Bool.self) {
            self = .bool(b)
        } else if let n = try? c.decode(Double.self) {
            self = .number(n)
        } else if let s = try? c.decode(String.self) {
            self = .string(s)
        } else if let a = try? c.decode([JSONValue].self) {
            self = .array(a)
        } else if let o = try? c.decode([String: JSONValue].self) {
            self = .object(o)
        } else {
            throw DecodingError.dataCorruptedError(in: c, debugDescription: "not a JSON value")
        }
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.singleValueContainer()
        switch self {
        case .null: try c.encodeNil()
        case .bool(let b): try c.encode(b)
        case .number(let n): try c.encode(n)
        case .string(let s): try c.encode(s)
        case .array(let a): try c.encode(a)
        case .object(let o): try c.encode(o)
        }
    }

    /// The members of an object, nil for anything else.
    public var objectValue: [String: JSONValue]? {
        if case .object(let o) = self { return o }
        return nil
    }

    /// The elements of an array, nil for anything else.
    public var arrayValue: [JSONValue]? {
        if case .array(let a) = self { return a }
        return nil
    }

    public var stringValue: String? {
        if case .string(let s) = self { return s }
        return nil
    }

    public var boolValue: Bool? {
        if case .bool(let b) = self { return b }
        return nil
    }

    public var doubleValue: Double? {
        if case .number(let n) = self { return n }
        return nil
    }

    /// The number as an integer when it is one exactly (and fits).
    public var intValue: Int? {
        guard case .number(let n) = self, n.rounded() == n,
              n >= Double(Int.min), n <= Double(Int.max) else { return nil }
        return Int(n)
    }

    /// A member of an object, nil when this is not an object or has no such key.
    public subscript(key: String) -> JSONValue? {
        objectValue?[key]
    }
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

    public init(method: String, line: Data) {
        self.method = method
        self.line = line
    }

    public func params<P: Decodable>(_ type: P.Type) throws -> P {
        try JSONCoding.decoder().decode(ParamsEnvelope<P>.self, from: line).params
    }
}

/// Encodes as `{}`, for methods that take no parameters.
public struct EmptyParams: Codable, Sendable, Equatable {
    public init() {}
}

/// The coders every wire exchange uses: fresh per use (cheap, and no shared
/// mutable state to reason about under strict concurrency), dates as the
/// contract's RFC 3339 (`RFC3339`), binary as base64, keys verbatim.
public enum JSONCoding {
    public static func encoder() -> JSONEncoder {
        let e = JSONEncoder()
        e.dateEncodingStrategy = .custom { date, encoder in
            var c = encoder.singleValueContainer()
            try c.encode(RFC3339.format(date))
        }
        return e
    }

    public static func decoder() -> JSONDecoder {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .custom { decoder in
            let c = try decoder.singleValueContainer()
            let s = try c.decode(String.self)
            guard let date = RFC3339.parse(s) else {
                throw DecodingError.dataCorruptedError(in: c, debugDescription: "not an RFC 3339 date")
            }
            return date
        }
        return d
    }
}
