// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Signatures of incoming mail and the OpenPGP key store (docs/api.md §4.21,
// §5 notify.signaturesChanged; backend/pkg/api/signature.go; the design is
// docs/plans/signatures.md, phase 1: the daemon verifies S/MIME and
// OpenPGP/MIME signatures, nothing is signed, decrypted or encrypted). A
// verdict is never an error. Every string that comes from a certificate or a
// key (names, addresses, distinguished names, user IDs, signed header
// values) is untrusted display text: show it with `stringValue` only. Every
// enum is open: an unknown `SignatureStatus` reads as `.error`, and an
// unknown note, reason, revocation state or key source is ignored.

import Foundation

// MARK: Limits

extension API.Limits {
    /// Every string from a certificate or a key, in bytes.
    public static let maxSignatureTextBytes = 256
    public static let maxSignatureChain = 16
    public static let maxSignatureEmails = 16
    public static let maxSignatureUserIDs = 32
    public static let maxProtectedHeaders = 6
    /// The key data of pgp.keyImport.
    public static let maxPGPKeyBytes = 1 << 20
    public static let maxPGPImportKeys = 20
    public static let maxPGPStoredKeys = 5000
    public static let maxRevocationResponseBytes = 1 << 20
    /// api.RevocationTimeout: the daemon's whole check; the client waits
    /// `RPCTimeouts.revocation` (20 s).
    public static let revocationTimeout: TimeInterval = 15
    /// api.RevocationCheckInterval: within it the stored state comes back.
    public static let revocationCheckInterval: TimeInterval = 60
}

// MARK: Verdict

/// api.SignatureKind: the format of a signature.
public struct SignatureKind: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// S/MIME (CMS SignedData), clear or opaque.
    public static let smime: SignatureKind = "smime"
    /// OpenPGP/MIME (RFC 3156), or inline (never verified).
    public static let pgp: SignatureKind = "pgp"
}

/// api.SignatureStatus: the verdict of a message's signature.
public struct SignatureStatus: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let invalid: SignatureStatus = "invalid"
    public static let revoked: SignatureStatus = "revoked"
    public static let mismatch: SignatureStatus = "mismatch"
    public static let untrusted: SignatureStatus = "untrusted"
    public static let unknownKey: SignatureStatus = "unknownKey"
    public static let weak: SignatureStatus = "weak"
    public static let unsupported: SignatureStatus = "unsupported"
    public static let error: SignatureStatus = "error"
    public static let valid: SignatureStatus = "valid"
    public static let validSeen: SignatureStatus = "validSeen"
}

/// api.SignatureNote: adds to a verdict without changing it.
public struct SignatureNote: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let expiredSince: SignatureNote = "expiredSince"
    public static let revocationUnchecked: SignatureNote = "revocationUnchecked"
    public static let revocationGood: SignatureNote = "revocationGood"
    public static let revokedAfter: SignatureNote = "revokedAfter"
    public static let partialFooter: SignatureNote = "partialFooter"
    public static let protectedHeadersDiffer: SignatureNote = "protectedHeadersDiffer"
    public static let inlinePGP: SignatureNote = "inlinePGP"
    public static let keyChanged: SignatureNote = "keyChanged"
    public static let opaque: SignatureNote = "opaque"
}

/// api.SignatureReason: why a verdict is not valid.
public struct SignatureReason: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    // invalid
    public static let digestMismatch: SignatureReason = "digestMismatch"
    public static let badSignature: SignatureReason = "badSignature"
    public static let malformed: SignatureReason = "malformed"
    public static let md5: SignatureReason = "md5"
    public static let keyUsage: SignatureReason = "keyUsage"
    // untrusted
    public static let unknownIssuer: SignatureReason = "unknownIssuer"
    public static let selfSigned: SignatureReason = "selfSigned"
    public static let notYetValid: SignatureReason = "notYetValid"
    public static let expired: SignatureReason = "expired"
    // weak
    public static let sha1: SignatureReason = "sha1"
    public static let shortKey: SignatureReason = "shortKey"
    // unsupported
    public static let algorithm: SignatureReason = "algorithm"
    public static let multipleSigners: SignatureReason = "multipleSigners"
    public static let rebuilt: SignatureReason = "rebuilt"
    public static let format: SignatureReason = "format"
    // error
    public static let limit: SignatureReason = "limit"
    public static let `internal`: SignatureReason = "internal"
}

/// api.MessageSignature: `MessageSummary.signature`, what the list's icon
/// needs. Absent for a message without a signature that is the verdict of
/// the whole message, and while the verdict is pending.
public struct MessageSignature: Codable, Sendable, Equatable {
    public var kind: SignatureKind
    public var status: SignatureStatus

    public init(kind: SignatureKind, status: SignatureStatus) {
        self.kind = kind
        self.status = status
    }
}

/// api.SignatureSigner: who signed, from the certificate or the key's user
/// ID that matched, never from the message's headers.
public struct SignatureSigner: Codable, Sendable, Equatable {
    public var name: String?
    public var email: String?
    /// The SHA-256 of the signer's certificate (S/MIME, 64 lower-case hex
    /// digits) or the primary fingerprint of the signing key (OpenPGP, 40
    /// or 64); absent when the key is unknown.
    public var fingerprint: String?

    public init(name: String? = nil, email: String? = nil, fingerprint: String? = nil) {
        self.name = name
        self.email = email
        self.fingerprint = fingerprint
    }
}

/// api.SignatureSummary: `Message.signature` (message.get,
/// message.download), what the header's badge needs; it takes the place of
/// `MessageSummary.signature` on the wire.
public struct SignatureSummary: Codable, Sendable, Equatable {
    public var kind: SignatureKind
    public var status: SignatureStatus
    public var signer: SignatureSigner?
    @NullAsEmpty public var notes: [SignatureNote]
    /// The detached signature part of a multipart/signed message: no chip
    /// for it (plan S15). Absent for opaque S/MIME and inline OpenPGP.
    public var partId: String?

    public init(
        kind: SignatureKind, status: SignatureStatus, signer: SignatureSigner? = nil,
        notes: [SignatureNote] = [], partId: String? = nil
    ) {
        self.kind = kind
        self.status = status
        self.signer = signer
        self.notes = notes
        self.partId = partId
    }
}

/// api.ProtectedHeaderName: a protected header (RFC 9788) that differs
/// from the outer one.
public struct ProtectedHeaderName: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let subject: ProtectedHeaderName = "subject"
    public static let from: ProtectedHeaderName = "from"
    public static let to: ProtectedHeaderName = "to"
    public static let cc: ProtectedHeaderName = "cc"
    public static let replyTo: ProtectedHeaderName = "replyTo"
    public static let date: ProtectedHeaderName = "date"
}

/// api.ProtectedHeader: a signed header whose value differs from the outer
/// one; `value` is the signed one (untrusted text, one line).
public struct ProtectedHeader: Codable, Sendable, Equatable {
    public var name: ProtectedHeaderName
    public var value: String

    public init(name: ProtectedHeaderName, value: String) {
        self.name = name
        self.value = value
    }
}

// MARK: Details

/// api.RevocationState: what is known of a certificate's revocation.
public struct RevocationState: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let unchecked: RevocationState = "unchecked"
    public static let good: RevocationState = "good"
    public static let revoked: RevocationState = "revoked"
    public static let unknown: RevocationState = "unknown"
}

/// api.RevocationSource: where the last check got its answer.
public struct RevocationSource: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let ocsp: RevocationSource = "ocsp"
    public static let crl: RevocationSource = "crl"
}

/// api.SignatureCertificate: one certificate of an S/MIME chain; every
/// string is untrusted text from the certificate.
public struct SignatureCertificate: Codable, Sendable, Equatable {
    /// 64 lower-case hex digits of the DER encoding.
    public var sha256: String
    public var subject: String?
    public var issuer: String?
    @NullAsEmpty public var emails: [String]
    public var notBefore: Date
    public var notAfter: Date
    /// The certificate is a root of the system's trust store.
    @DefaultFalse public var systemRoot: Bool
    @DefaultFalse public var selfSigned: Bool

    public init(
        sha256: String, subject: String? = nil, issuer: String? = nil, emails: [String] = [],
        notBefore: Date, notAfter: Date, systemRoot: Bool = false, selfSigned: Bool = false
    ) {
        self.sha256 = sha256
        self.subject = subject
        self.issuer = issuer
        self.emails = emails
        self.notBefore = notBefore
        self.notAfter = notAfter
        self.systemRoot = systemRoot
        self.selfSigned = selfSigned
    }
}

/// api.SMIMERevocation: the revocation state of the signer's certificate.
public struct SMIMERevocation: Codable, Sendable, Equatable {
    public var state: RevocationState
    public var checkedAt: Date?
    public var revokedAt: Date?
    public var source: RevocationSource?
    /// The certificate names an http or https OCSP responder or CRL
    /// distribution point, so message.signatureRevocation can check.
    @DefaultFalse public var checkable: Bool

    public init(
        state: RevocationState, checkedAt: Date? = nil, revokedAt: Date? = nil, source: RevocationSource? = nil,
        checkable: Bool = false
    ) {
        self.state = state
        self.checkedAt = checkedAt
        self.revokedAt = revokedAt
        self.source = source
        self.checkable = checkable
    }
}

/// api.SMIMEDetails: the S/MIME parts of `SignatureDetails`.
public struct SMIMEDetails: Codable, Sendable, Equatable {
    /// The signer's certificate first, then each issuer as far as it was
    /// found, at most `API.Limits.maxSignatureChain`.
    @NullAsEmpty public var chain: [SignatureCertificate]
    public var revocation: SMIMERevocation

    public init(chain: [SignatureCertificate] = [], revocation: SMIMERevocation) {
        self.chain = chain
        self.revocation = revocation
    }
}

/// api.PGPKeyTrust: how far an OpenPGP key is trusted.
public struct PGPKeyTrust: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// The key is not stored (`SignatureDetails` only).
    public static let unknown: PGPKeyTrust = "unknown"
    /// Found automatically or imported, not confirmed (TOFU).
    public static let seen: PGPKeyTrust = "seen"
    /// The user confirmed its fingerprint.
    public static let verified: PGPKeyTrust = "verified"
}

/// api.PGPKeySource: where a stored key came from.
public struct PGPKeySource: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let autocrypt: PGPKeySource = "autocrypt"
    public static let attached: PGPKeySource = "attached"
    public static let `import`: PGPKeySource = "import"
}

/// api.PGPKeyOffer: a key the message carries that is not stored, or
/// another key for the signer's address. Exactly one of `partId` and
/// `autocrypt` is set; the client passes it to pgp.keyImport as it is.
public struct PGPKeyOffer: Codable, Sendable, Equatable {
    public var fingerprint: String
    public var partId: String?
    @DefaultFalse public var autocrypt: Bool

    public init(fingerprint: String, partId: String? = nil, autocrypt: Bool = false) {
        self.fingerprint = fingerprint
        self.partId = partId
        self.autocrypt = autocrypt
    }
}

/// api.PGPDetails: the OpenPGP parts of `SignatureDetails`.
public struct PGPDetails: Codable, Sendable, Equatable {
    public var keyTrust: PGPKeyTrust
    /// The signing (sub)key's 16 lower-case hex digits.
    public var keyId: String?
    /// The stored key's primary fingerprint; what pgp.keyVerify takes.
    public var primaryFingerprint: String?
    @NullAsEmpty public var userIds: [String]
    public var keySource: PGPKeySource?
    public var firstSeen: Date?
    public var previousFingerprint: String?
    public var importable: PGPKeyOffer?

    public init(
        keyTrust: PGPKeyTrust, keyId: String? = nil, primaryFingerprint: String? = nil, userIds: [String] = [],
        keySource: PGPKeySource? = nil, firstSeen: Date? = nil, previousFingerprint: String? = nil,
        importable: PGPKeyOffer? = nil
    ) {
        self.keyTrust = keyTrust
        self.keyId = keyId
        self.primaryFingerprint = primaryFingerprint
        self.userIds = userIds
        self.keySource = keySource
        self.firstSeen = firstSeen
        self.previousFingerprint = previousFingerprint
        self.importable = importable
    }
}

/// api.SignatureDetails: what message.signature returns for the details
/// dialog. Go embeds `SignatureSummary`, so on the wire its fields sit
/// beside these; here they are `summary`, and the coding flattens.
public struct SignatureDetails: Codable, Sendable, Equatable {
    public var summary: SignatureSummary
    public var messageId: MessageID
    public var reason: SignatureReason?
    /// The signing time the signer declared: a claim, never used to judge.
    public var signedAt: Date?
    /// When the message arrived; validity is judged at this time.
    public var arrivedAt: Date
    /// "RSA 2048 / SHA-256": shown as it is.
    public var algorithm: String?
    public var expiredAt: Date?
    public var revokedAt: Date?
    @NullAsEmpty public var protectedHeaders: [ProtectedHeader]
    public var smime: SMIMEDetails?
    public var pgp: PGPDetails?

    public var kind: SignatureKind { summary.kind }
    public var status: SignatureStatus { summary.status }
    public var signer: SignatureSigner? { summary.signer }
    public var notes: [SignatureNote] { summary.notes }

    public init(
        summary: SignatureSummary, messageId: MessageID, reason: SignatureReason? = nil, signedAt: Date? = nil,
        arrivedAt: Date, algorithm: String? = nil, expiredAt: Date? = nil, revokedAt: Date? = nil,
        protectedHeaders: [ProtectedHeader] = [], smime: SMIMEDetails? = nil, pgp: PGPDetails? = nil
    ) {
        self.summary = summary
        self.messageId = messageId
        self.reason = reason
        self.signedAt = signedAt
        self.arrivedAt = arrivedAt
        self.algorithm = algorithm
        self.expiredAt = expiredAt
        self.revokedAt = revokedAt
        self.protectedHeaders = protectedHeaders
        self.smime = smime
        self.pgp = pgp
    }

    private enum CodingKeys: String, CodingKey {
        case messageId, reason, signedAt, arrivedAt, algorithm, expiredAt, revokedAt, protectedHeaders, smime, pgp
    }

    public init(from decoder: any Decoder) throws {
        summary = try SignatureSummary(from: decoder)
        let c = try decoder.container(keyedBy: CodingKeys.self)
        messageId = try c.decode(MessageID.self, forKey: .messageId)
        reason = try c.decodeIfPresent(SignatureReason.self, forKey: .reason)
        signedAt = try c.decodeIfPresent(Date.self, forKey: .signedAt)
        arrivedAt = try c.decode(Date.self, forKey: .arrivedAt)
        algorithm = try c.decodeIfPresent(String.self, forKey: .algorithm)
        expiredAt = try c.decodeIfPresent(Date.self, forKey: .expiredAt)
        revokedAt = try c.decodeIfPresent(Date.self, forKey: .revokedAt)
        protectedHeaders = try c.decodeIfPresent([ProtectedHeader].self, forKey: .protectedHeaders) ?? []
        smime = try c.decodeIfPresent(SMIMEDetails.self, forKey: .smime)
        pgp = try c.decodeIfPresent(PGPDetails.self, forKey: .pgp)
    }

    public func encode(to encoder: any Encoder) throws {
        try summary.encode(to: encoder)
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(messageId, forKey: .messageId)
        try c.encodeIfPresent(reason, forKey: .reason)
        try c.encodeIfPresent(signedAt, forKey: .signedAt)
        try c.encode(arrivedAt, forKey: .arrivedAt)
        try c.encodeIfPresent(algorithm, forKey: .algorithm)
        try c.encodeIfPresent(expiredAt, forKey: .expiredAt)
        try c.encodeIfPresent(revokedAt, forKey: .revokedAt)
        if !protectedHeaders.isEmpty {
            try c.encode(protectedHeaders, forKey: .protectedHeaders)
        }
        try c.encodeIfPresent(smime, forKey: .smime)
        try c.encodeIfPresent(pgp, forKey: .pgp)
    }
}

// MARK: OpenPGP keys

/// api.PGPKey: a stored public key (never a secret one).
public struct PGPKey: Codable, Sendable, Equatable {
    /// The primary key's, 40 (v4) or 64 (v6) lower-case hex digits.
    public var fingerprint: String
    @NullAsEmpty public var userIds: [String]
    @NullAsEmpty public var emails: [String]
    /// `seen` or `verified`.
    public var trust: PGPKeyTrust
    public var source: PGPKeySource
    public var firstSeen: Date
    public var createdAt: Date
    public var expiresAt: Date?
    public var revokedAt: Date?
    /// "Ed25519", "RSA 4096".
    public var algorithm: String?

    public init(
        fingerprint: String, userIds: [String] = [], emails: [String] = [], trust: PGPKeyTrust,
        source: PGPKeySource, firstSeen: Date, createdAt: Date = .goZero, expiresAt: Date? = nil,
        revokedAt: Date? = nil, algorithm: String? = nil
    ) {
        self.fingerprint = fingerprint
        self.userIds = userIds
        self.emails = emails
        self.trust = trust
        self.source = source
        self.firstSeen = firstSeen
        self.createdAt = createdAt
        self.expiresAt = expiresAt
        self.revokedAt = revokedAt
        self.algorithm = algorithm
    }
}

// MARK: Methods (§4.21)

/// api.MessageSignatureParams, also the params of message.signatureRevocation.
public struct MessageSignatureParams: Codable, Sendable, Equatable {
    public var messageId: MessageID

    public init(messageId: MessageID) {
        self.messageId = messageId
    }
}

/// api.MessageSignatureResult: `signature` is null for a message without a
/// verdict (no signature of the whole message, or not verified yet).
public struct MessageSignatureResult: Codable, Sendable, Equatable {
    public var signature: SignatureDetails?

    public init(signature: SignatureDetails? = nil) {
        self.signature = signature
    }
}

/// api.RevocationFailure: the class of a failed revocation check.
public struct RevocationFailure: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let network: RevocationFailure = "network"
    public static let timeout: RevocationFailure = "timeout"
    public static let server: RevocationFailure = "server"
    public static let response: RevocationFailure = "response"
    public static let address: RevocationFailure = "address"
}

/// api.RevocationFailedData: `error.data` of `revocationFailed`.
public struct RevocationFailedData: Codable, Sendable, Equatable {
    public var reason: RevocationFailure

    public init(reason: RevocationFailure) {
        self.reason = reason
    }
}

/// api.PGPImportFailure: why pgp.keyImport refused the data.
public struct PGPImportFailure: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let noKey: PGPImportFailure = "noKey"
    public static let malformed: PGPImportFailure = "malformed"
    public static let secretKey: PGPImportFailure = "secretKey"
    public static let tooLarge: PGPImportFailure = "tooLarge"
    public static let tooMany: PGPImportFailure = "tooMany"
}

/// api.PGPImportErrorData: `error.data` of `invalidArgument` from
/// pgp.keyImport when the data was refused.
public struct PGPImportErrorData: Codable, Sendable, Equatable {
    public var reason: PGPImportFailure

    public init(reason: PGPImportFailure) {
        self.reason = reason
    }
}

/// api.PGPKeyImportParams: exactly one of `messageId` with `partId` (an
/// attached key), `messageId` with `autocrypt` (the message's Autocrypt
/// header), `armored` (ASCII armour) or `data` (binary, base64 on the wire).
public struct PGPKeyImportParams: Codable, Sendable, Equatable {
    public var messageId: MessageID?
    public var partId: String?
    public var autocrypt: Bool?
    public var armored: String?
    public var data: Data?

    public init(
        messageId: MessageID? = nil, partId: String? = nil, autocrypt: Bool? = nil, armored: String? = nil,
        data: Data? = nil
    ) {
        self.messageId = messageId
        self.partId = partId
        self.autocrypt = autocrypt
        self.armored = armored
        self.data = data
    }
}

/// api.PGPKeyImportResult: the keys of the data as stored after the import.
public struct PGPKeyImportResult: Codable, Sendable, Equatable {
    @NullAsEmpty public var keys: [PGPKey]
    public var added: Int
    public var updated: Int

    public init(keys: [PGPKey] = [], added: Int = 0, updated: Int = 0) {
        self.keys = keys
        self.added = added
        self.updated = updated
    }
}

/// api.PGPKeyVerifyParams: `verified` true confirms the key, false takes the
/// confirmation back.
public struct PGPKeyVerifyParams: Codable, Sendable, Equatable {
    public var fingerprint: String
    public var verified: Bool

    public init(fingerprint: String, verified: Bool) {
        self.fingerprint = fingerprint
        self.verified = verified
    }
}

public struct PGPKeyVerifyResult: Codable, Sendable, Equatable {
    public var key: PGPKey

    public init(key: PGPKey) {
        self.key = key
    }
}

/// api.PGPKeyListResult: every stored key, by the first address, then the
/// fingerprint.
public struct PGPKeyListResult: Codable, Sendable, Equatable {
    @NullAsEmpty public var keys: [PGPKey]

    public init(keys: [PGPKey] = []) {
        self.keys = keys
    }
}

public struct PGPKeyRemoveParams: Codable, Sendable, Equatable {
    public var fingerprint: String

    public init(fingerprint: String) {
        self.fingerprint = fingerprint
    }
}

extension API {
    /// The verdict's details, or null.
    public enum MessageSignatureGet: RPCMethod {
        public typealias Params = MessageSignatureParams
        public typealias Result = MessageSignatureResult
        public static let name = "message.signature"
    }

    /// Asks the certificate's issuer (OCSP, then the CRL); the daemon's
    /// budget is 15 s, the client waits 20.
    public enum MessageSignatureRevocation: RPCMethod {
        public typealias Params = MessageSignatureParams
        public typealias Result = MessageSignatureResult
        public static let name = "message.signatureRevocation"
        public static let timeout = RPCTimeouts.revocation
    }

    public enum PGPKeyImport: RPCMethod {
        public typealias Params = PGPKeyImportParams
        public typealias Result = PGPKeyImportResult
        public static let name = "pgp.keyImport"
        public static let timeout = RPCTimeouts.remote
    }

    public enum PGPKeyVerify: RPCMethod {
        public typealias Params = PGPKeyVerifyParams
        public typealias Result = PGPKeyVerifyResult
        public static let name = "pgp.keyVerify"
    }

    public enum PGPKeyList: RPCMethod {
        public typealias Params = EmptyParams
        public typealias Result = PGPKeyListResult
        public static let name = "pgp.keyList"
    }

    public enum PGPKeyRemove: RPCMethod {
        public typealias Params = PGPKeyRemoveParams
        public typealias Result = EmptyResult
        public static let name = "pgp.keyRemove"
    }

    /// The signature methods, which end methods.go's table after update.
    static let signatureMethods: [any RPCMethod.Type] = [
        MessageSignatureGet.self, MessageSignatureRevocation.self,
        PGPKeyImport.self, PGPKeyVerify.self, PGPKeyList.self, PGPKeyRemove.self,
    ]
}

extension RPCError {
    /// The data of a `revocationFailed` error; nil for any other code or
    /// data of another shape.
    public var revocationFailedData: RevocationFailedData? {
        guard code == .revocationFailed else { return nil }
        return signatureData(RevocationFailedData.self)
    }

    /// The data of an `invalidArgument` that pgp.keyImport answered for
    /// refused data; nil for any other code or data of another shape.
    public var pgpImportErrorData: PGPImportErrorData? {
        guard code == .invalidArgument else { return nil }
        return signatureData(PGPImportErrorData.self)
    }

    private func signatureData<T: Decodable>(_ type: T.Type) -> T? {
        guard let data, data.objectValue != nil, let raw = try? JSONCoding.encoder().encode(data) else { return nil }
        return try? JSONCoding.decoder().decode(type, from: raw)
    }
}
