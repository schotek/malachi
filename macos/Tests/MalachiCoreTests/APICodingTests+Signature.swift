// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// The signature wire types (docs/api.md §4.22, backend/pkg/api/
/// signature.go) with invented data: the verdict in a summary and in
/// message.get, the details of both formats with optional fields absent and
/// present, params encoding, the error data, the limits and the methods.
@Suite struct APICodingSignatureTests {
    private func decode<T: Decodable>(_ type: T.Type, _ s: String) throws -> T {
        try JSONCoding.decoder().decode(type, from: json(s))
    }

    private func encodeObject<T: Encodable>(_ v: T) throws -> [String: Any] {
        let data = try JSONCoding.encoder().encode(v)
        return try #require(JSONSerialization.jsonObject(with: data) as? [String: Any])
    }

    private func roundTrip<T: Codable & Equatable>(_ v: T) throws -> T {
        try JSONCoding.decoder().decode(T.self, from: JSONCoding.encoder().encode(v))
    }

    private let fp = String(repeating: "ab12cd34", count: 8)

    private static let summaryHead = #"{"id":"m_1","accountId":"a_1","folderId":"f_1","from":[{"address":"jan@example.org"}],"subject":"s","date":"2026-11-02T08:00:00Z","snippet":"","flags":[],"hasAttachments":false,"size":10"#

    @Test func theListCarriesTheKindAndTheStatus() throws {
        let s = try decode(MessageSummary.self, Self.summaryHead + #","signature":{"kind":"pgp","status":"validSeen"}}"#)
        #expect(s.signature == MessageSignature(kind: .pgp, status: .validSeen))
        let none = try decode(MessageSummary.self, Self.summaryHead + "}")
        #expect(none.signature == nil)
        // A status of a newer daemon decodes as itself; it reads as an error.
        let newer = try decode(MessageSummary.self, Self.summaryHead + #","signature":{"kind":"smime","status":"quantum"}}"#)
        #expect(newer.signature?.status == "quantum")
        #expect(SignatureView.severity(of: "quantum") == .neutral)
        #expect(try roundTrip(s) == s)
    }

    @Test func messageGetCarriesTheSummaryInTheSameKey() throws {
        let m = try decode(Message.self, Self.summaryHead + #","attachments":[],"signature":{"kind":"smime","status":"valid","signer":{"name":"Jan","email":"jan@example.org","fingerprint":"\#(fp)"},"notes":["opaque"],"partId":"2"}}"#)
        #expect(m.signature == SignatureSummary(
            kind: .smime, status: .valid, signer: SignatureSigner(name: "Jan", email: "jan@example.org", fingerprint: fp),
            notes: [.opaque], partId: "2"))
        #expect(m.summary.signature == nil, "the key holds the summary here, not the list's")
        let again = try roundTrip(m)
        #expect(again == m)
        let plain = try decode(Message.self, Self.summaryHead + #","attachments":[]}"#)
        #expect(plain.signature == nil)
        let obj = try encodeObject(plain)
        #expect(obj["signature"] == nil)
    }

    @Test func smimeDetails() throws {
        let d = try decode(SignatureDetails.self, #"""
        {"kind":"smime","status":"valid","signer":{"name":"Jan","email":"jan@example.org","fingerprint":"\#(fp)"},
         "notes":["revocationUnchecked"],"partId":"2","messageId":"m_1","signedAt":"2026-11-02T07:00:00Z",
         "arrivedAt":"2026-11-02T08:00:00Z","algorithm":"RSA 2048 / SHA-256",
         "protectedHeaders":[{"name":"subject","value":"Real"}],
         "smime":{"chain":[{"sha256":"\#(fp)","subject":"CN=Jan","issuer":"CN=CA","emails":["jan@example.org"],
                            "notBefore":"2026-01-01T00:00:00Z","notAfter":"2027-01-01T00:00:00Z"},
                           {"sha256":"\#(fp)","notBefore":"2026-01-01T00:00:00Z","notAfter":"2027-01-01T00:00:00Z",
                            "systemRoot":true,"selfSigned":true}],
                  "revocation":{"state":"unchecked","checkable":true}}}
        """#)
        #expect(d.kind == .smime && d.status == .valid && d.signer?.email == "jan@example.org" && d.notes == [.revocationUnchecked])
        #expect(d.summary.partId == "2" && d.messageId == "m_1" && d.reason == nil && d.pgp == nil)
        #expect(d.signedAt != nil && d.arrivedAt == RFC3339.parse("2026-11-02T08:00:00Z"))
        #expect(d.protectedHeaders == [ProtectedHeader(name: .subject, value: "Real")])
        let sm = try #require(d.smime)
        #expect(sm.chain.count == 2 && sm.chain[0].emails == ["jan@example.org"] && !sm.chain[0].systemRoot)
        #expect(sm.chain[1].systemRoot && sm.chain[1].selfSigned && sm.chain[1].subject == nil && sm.chain[1].emails.isEmpty)
        #expect(sm.revocation == SMIMERevocation(state: .unchecked, checkable: true))
        #expect(try roundTrip(d) == d)
    }

    @Test func pgpDetailsAndTheOffer() throws {
        let d = try decode(SignatureDetails.self, #"""
        {"kind":"pgp","status":"unknownKey","messageId":"m_2","arrivedAt":"2026-11-02T08:00:00Z","reason":"algorithm",
         "pgp":{"keyTrust":"unknown","keyId":"89abcdef01234567","importable":{"fingerprint":"0123456789abcdef0123456789abcdef01234567","partId":"3"}}}
        """#)
        #expect(d.kind == .pgp && d.status == .unknownKey && d.reason == .algorithm && d.smime == nil && d.signer == nil)
        let p = try #require(d.pgp)
        #expect(p.keyTrust == .unknown && p.keyId == "89abcdef01234567" && p.userIds.isEmpty && p.primaryFingerprint == nil)
        #expect(p.importable == PGPKeyOffer(fingerprint: "0123456789abcdef0123456789abcdef01234567", partId: "3"))
        #expect(p.importable?.autocrypt == false)
        let auto = try decode(PGPKeyOffer.self, #"{"fingerprint":"x","autocrypt":true}"#)
        #expect(auto.autocrypt && auto.partId == nil)
        #expect(try roundTrip(d) == d)
    }

    @Test func nullResult() throws {
        #expect(try decode(MessageSignatureResult.self, #"{"signature":null}"#).signature == nil)
        #expect(try decode(MessageSignatureResult.self, "{}").signature == nil)
    }

    @Test func keys() throws {
        let k = try decode(PGPKey.self, #"""
        {"fingerprint":"0123456789abcdef0123456789abcdef01234567","userIds":null,"emails":["jan@example.org"],
         "trust":"verified","source":"import","firstSeen":"2026-11-01T00:00:00Z","createdAt":"2025-01-01T00:00:00Z",
         "revokedAt":"2026-12-01T00:00:00Z","algorithm":"Ed25519"}
        """#)
        #expect(k.userIds.isEmpty && k.emails == ["jan@example.org"] && k.trust == .verified && k.source == .import)
        #expect(k.expiresAt == nil && k.revokedAt != nil && k.algorithm == "Ed25519")
        #expect(try decode(PGPKeyListResult.self, #"{"keys":null}"#).keys.isEmpty)
        let r = try decode(PGPKeyImportResult.self, #"{"keys":[{"fingerprint":"x","userIds":[],"emails":[],"trust":"seen","source":"autocrypt","firstSeen":"2026-11-01T00:00:00Z","createdAt":"2025-01-01T00:00:00Z"}],"added":1,"updated":0}"#)
        #expect(r.keys.count == 1 && r.added == 1 && r.updated == 0)
        #expect(try decode(PGPKeyVerifyResult.self, #"{"key":{"fingerprint":"x","userIds":[],"emails":[],"trust":"verified","source":"import","firstSeen":"2026-11-01T00:00:00Z","createdAt":"2025-01-01T00:00:00Z"}}"#).key.trust == .verified)
    }

    @Test func paramsEncodeOnlyWhatIsSet() throws {
        let part = try encodeObject(PGPKeyImportParams(messageId: "m_1", partId: "3"))
        #expect(part.keys.sorted() == ["messageId", "partId"])
        let auto = try encodeObject(PGPKeyImportParams(messageId: "m_1", autocrypt: true))
        #expect(auto.keys.sorted() == ["autocrypt", "messageId"] && auto["autocrypt"] as? Bool == true)
        let armored = try encodeObject(PGPKeyImportParams(armored: "-----BEGIN PGP PUBLIC KEY BLOCK-----"))
        #expect(armored.keys.sorted() == ["armored"])
        let binary = try encodeObject(PGPKeyImportParams(data: Data([0x99, 0x01])))
        #expect(binary.keys.sorted() == ["data"] && binary["data"] as? String == "mQE=", "base64 on the wire")
        let verify = try encodeObject(PGPKeyVerifyParams(fingerprint: "x", verified: false))
        #expect(verify["verified"] as? Bool == false, "false is sent")
        #expect(try encodeObject(PGPKeyRemoveParams(fingerprint: "x")).keys.sorted() == ["fingerprint"])
        #expect(try encodeObject(MessageSignatureParams(messageId: "m_1")).keys.sorted() == ["messageId"])
    }

    @Test func errorData() throws {
        let revocation = try decode(RPCError.self, #"{"code":1510,"message":"x","data":{"reason":"timeout"}}"#)
        #expect(revocation.code == .revocationFailed && revocation.revocationFailedData == RevocationFailedData(reason: .timeout))
        #expect(SignatureView.revocationFailure(of: revocation) == .timeout)
        #expect(SignatureView.importFailure(of: revocation) == "", "another code")
        let imp = try decode(RPCError.self, #"{"code":1001,"message":"x","data":{"reason":"secretKey"}}"#)
        #expect(imp.pgpImportErrorData == PGPImportErrorData(reason: .secretKey) && SignatureView.importFailure(of: imp) == .secretKey)
        #expect(SignatureView.revocationFailure(of: imp) == "")
        for bad in [
            RPCError(code: .revocationFailed, message: "x"),
            RPCError(code: .revocationFailed, message: "x", data: .string("x")),
            RPCError(code: .revocationFailed, message: "x", data: .array([])),
        ] {
            #expect(SignatureView.revocationFailure(of: bad) == "")
        }
        #expect(SignatureView.revocationFailure(of: nil) == "")
        #expect(SignatureView.importFailure(of: RPCError(code: .messageNotFound, message: "x", data: .object(["reason": .string("secretKey")]))) == "")
        #expect(ErrorCode.revocationFailed.rawValue == 1510 && ErrorCode.revocationFailed.name == "revocationFailed")
    }

    @Test func limitsAndTimeouts() {
        #expect(API.Limits.maxSignatureTextBytes == 256 && API.Limits.maxSignatureChain == 16)
        #expect(API.Limits.maxSignatureEmails == 16 && API.Limits.maxSignatureUserIDs == 32 && API.Limits.maxProtectedHeaders == 6)
        #expect(API.Limits.maxPGPKeyBytes == 1 << 20 && API.Limits.maxPGPImportKeys == 20 && API.Limits.maxPGPStoredKeys == 5000)
        #expect(API.Limits.maxRevocationResponseBytes == 1 << 20)
        #expect(API.Limits.revocationTimeout == 15 && API.Limits.revocationCheckInterval == 60)
        // The daemon's budget is 15 s; the client waits 20.
        #expect(RPCTimeouts.revocation == .seconds(20) && API.MessageSignatureRevocation.timeout == .seconds(20))
    }

    @Test func methodsAndNotification() {
        #expect(API.signatureMethods.map { $0.name } == [
            "message.signature", "message.signatureRevocation", "pgp.keyImport", "pgp.keyVerify", "pgp.keyList", "pgp.keyRemove",
        ])
        #expect(Array(API.allMethods.suffix(6)) == API.signatureMethods.map { $0.name })
        #expect(API.Notify.signaturesChanged == "notify.signaturesChanged")
        #expect(API.allNotifications.last == "notify.signaturesChanged")
    }
}
