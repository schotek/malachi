// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// TLS error details and certificate fingerprints (docs/api.md §2 tlsError,
// §4.1 ServerConfig.certificateSha256; tls.go).

import Foundation

/// api.TLSErrorReason: why a TLS connection to an IMAP/SMTP endpoint
/// failed (the `reason` of a `tlsError`'s data). A value a newer daemon
/// adds decodes as itself; `CertTrust.normalizeReason` makes it `other`,
/// as the contract asks.
public struct TLSErrorReason: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// The issuer is not trusted (self-signed or a private CA).
    public static let untrusted: TLSErrorReason = "untrusted"
    /// The certificate is for another name.
    public static let hostnameMismatch: TLSErrorReason = "hostnameMismatch"
    /// Past notAfter.
    public static let expired: TLSErrorReason = "expired"
    /// Before notBefore.
    public static let notYetValid: TLSErrorReason = "notYetValid"
    /// Otherwise malformed or not allowed for a server.
    public static let invalid: TLSErrorReason = "invalid"
    /// Refused by the system verifier for another reason (macOS "not
    /// standards compliant").
    public static let other: TLSErrorReason = "other"
    /// The protocol failed before a certificate was judged.
    public static let handshake: TLSErrorReason = "handshake"
    /// STARTTLS is configured but not offered or refused.
    public static let starttlsUnavailable: TLSErrorReason = "starttlsUnavailable"
    /// The server demands TLS before login.
    public static let tlsRequired: TLSErrorReason = "tlsRequired"
    /// Not the certificate pinned in `ServerConfig.certificateSha256`.
    public static let pinMismatch: TLSErrorReason = "pinMismatch"

    /// Every reason of the contract (protocol version 2).
    public static let all: [TLSErrorReason] = [
        .untrusted, .hostnameMismatch, .expired, .notYetValid, .invalid, .other,
        .handshake, .starttlsUnavailable, .tlsRequired, .pinMismatch,
    ]
}

/// api.CertificateInfo: the server's leaf certificate. Every string is
/// untrusted text from the server (the daemon removes control and format
/// characters and caps each at 128 bytes, lists at 8 entries; the client
/// cleans them again, `CertTrust.details`). Decoded as Go's encoding/json
/// would: a missing member is its zero value, a mistyped one fails the
/// whole certificate.
public struct CertificateInfo: Codable, Sendable, Equatable {
    /// 64 lowercase hex digits of the SHA-256 of the DER encoding.
    public var sha256: String
    public var subject: String?
    public var issuer: String?
    @NullAsEmpty public var dnsNames: [String]
    @NullAsEmpty public var ipAddresses: [String]
    public var notBefore: Date
    public var notAfter: Date
    public var selfSigned: Bool

    public init(
        sha256: String, subject: String? = nil, issuer: String? = nil, dnsNames: [String] = [],
        ipAddresses: [String] = [], notBefore: Date, notAfter: Date, selfSigned: Bool = false
    ) {
        self.sha256 = sha256
        self.subject = subject
        self.issuer = issuer
        self.dnsNames = dnsNames
        self.ipAddresses = ipAddresses
        self.notBefore = notBefore
        self.notAfter = notAfter
        self.selfSigned = selfSigned
    }

    private enum CodingKeys: String, CodingKey {
        case sha256, subject, issuer, dnsNames, ipAddresses, notBefore, notAfter, selfSigned
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        sha256 = try c.decodeIfPresent(String.self, forKey: .sha256) ?? ""
        subject = try c.decodeIfPresent(String.self, forKey: .subject)
        issuer = try c.decodeIfPresent(String.self, forKey: .issuer)
        dnsNames = try c.decodeIfPresent([String].self, forKey: .dnsNames) ?? []
        ipAddresses = try c.decodeIfPresent([String].self, forKey: .ipAddresses) ?? []
        notBefore = try c.decodeIfPresent(Date.self, forKey: .notBefore) ?? .goZero
        notAfter = try c.decodeIfPresent(Date.self, forKey: .notAfter) ?? .goZero
        selfSigned = try c.decodeIfPresent(Bool.self, forKey: .selfSigned) ?? false
    }
}

/// api.TLSErrorData: `error.data` of a `tlsError` from an IMAP/SMTP
/// endpoint. `certificate` is set when the server presented one,
/// `expectedSha256` only for `pinMismatch`.
public struct TLSErrorData: Codable, Sendable, Equatable {
    public var reason: TLSErrorReason
    public var certificate: CertificateInfo?
    public var expectedSha256: String?

    public init(reason: TLSErrorReason, certificate: CertificateInfo? = nil, expectedSha256: String? = nil) {
        self.reason = reason
        self.certificate = certificate
        self.expectedSha256 = expectedSha256
    }
}

/// api.TLSErrorDataOf: the TLS details of `e`, decoded from its `data`;
/// nil when `e` is not a `tlsError`, carries no details or details of
/// another shape (no `reason`). The strings are as the daemon sent them;
/// what is shown goes through `CertTrust.details`.
public func tlsErrorData(_ e: RPCError?) -> TLSErrorData? {
    guard let e, e.code == .tlsError, let data = e.data, data.objectValue != nil else { return nil }
    guard let raw = try? JSONCoding.encoder().encode(data),
          let d = try? JSONCoding.decoder().decode(TLSErrorData.self, from: raw),
          !d.reason.rawValue.isEmpty else {
        return nil
    }
    return d
}

/// api.NormalizeCertificateSHA256: a SHA-256 fingerprint as 64 hex
/// digits, optionally separated by colons or spaces and in any case, as 64
/// lowercase hex digits; nil for anything else.
public func normalizeCertificateSHA256(_ s: String) -> String? {
    var out = ""
    out.reserveCapacity(64)
    for c in s.unicodeScalars {
        switch c {
        case ":", " ":
            continue
        case "0"..."9", "a"..."f":
            out.unicodeScalars.append(c)
        case "A"..."F":
            out.unicodeScalars.append(Unicode.Scalar(c.value + 0x20) ?? c)
        default:
            return nil
        }
    }
    return out.utf8.count == 64 ? out : nil
}
