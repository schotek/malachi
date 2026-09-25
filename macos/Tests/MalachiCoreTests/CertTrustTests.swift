// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The counterpart of ui/internal/certtrust/certtrust_test.go, plus the
// texts of the trust confirmation (accountwizard trust.go).

private let sumA = String(repeating: "ab", count: 32)
private let sumB = String(repeating: "0f", count: 32)

private let notBefore = Date(timeIntervalSince1970: 1_704_164_645) // 2024-01-02T03:04:05Z
private let notAfter = Date(timeIntervalSince1970: 2_335_316_645) // 2044-01-02T03:04:05Z

private let bridgeCert = CertificateInfo(
    sha256: sumA, subject: "127.0.0.1", issuer: "127.0.0.1", ipAddresses: ["127.0.0.1"],
    notBefore: notBefore, notAfter: notAfter, selfSigned: true)

private let imapTLS = ServerConfig(host: "bridge.tail.example", port: 1143, security: .starttls, username: "me", authMethod: .password)

// Invisible characters of hostile certificates, as scalar constants so the
// source stays readable.
private let rlo = String(UnicodeScalar(0x202E) ?? " ") // RIGHT-TO-LEFT OVERRIDE
private let zwsp = String(UnicodeScalar(0x200B) ?? " ") // ZERO WIDTH SPACE
private let lri = String(UnicodeScalar(0x2066) ?? " ") // LEFT-TO-RIGHT ISOLATE
private let lrm = String(UnicodeScalar(0x200E) ?? " ") // LEFT-TO-RIGHT MARK

/// `v` as the client holds it after the RPC: generic JSON.
func tlsJSON<T: Encodable>(_ v: T) throws -> JSONValue {
    try JSONCoding.decoder().decode(JSONValue.self, from: JSONCoding.encoder().encode(v))
}

/// A literal JSON text as generic JSON.
func rawJSON(_ s: String) throws -> JSONValue {
    try JSONCoding.decoder().decode(JSONValue.self, from: Data(s.utf8))
}

/// A tlsError carrying `data`.
func tlsError(_ data: JSONValue?) -> RPCError {
    RPCError(code: .tlsError, message: "x509: certificate signed by unknown authority", data: data)
}

/// A tlsError carrying these details.
func tlsError(_ d: TLSErrorData) throws -> RPCError {
    tlsError(try tlsJSON(d))
}

private func cert(_ c: CertificateInfo, _ change: (inout CertificateInfo) -> Void = { _ in }) -> CertificateInfo {
    var c = c
    change(&c)
    return c
}

@Suite struct CertTrustTests {
    @Test func details() throws {
        let cases: [(String, RPCError?, CertTrust.Problem?)] = [
            ("nil", nil, nil),
            ("other code", RPCError(code: .networkError, message: "x", data: try tlsJSON(TLSErrorData(reason: .untrusted))), nil),
            ("no data", tlsError(nil), nil),
            ("empty reason", try tlsError(TLSErrorData(reason: "", certificate: bridgeCert)), nil),
            ("map without reason", tlsError(try rawJSON(#"{"certificate":{"sha256":"\#(sumA)"}}"#)), nil),
            ("wrong shape", tlsError(.string("untrusted")), nil),
            ("untrusted with certificate", try tlsError(TLSErrorData(reason: .untrusted, certificate: bridgeCert)),
             CertTrust.Problem(reason: .untrusted, cert: bridgeCert)),
            ("handshake without certificate", try tlsError(TLSErrorData(reason: .handshake)), CertTrust.Problem(reason: .handshake)),
            ("unknown reason is other", try tlsError(TLSErrorData(reason: "quantumDecoherence", certificate: bridgeCert)),
             CertTrust.Problem(reason: .other, cert: bridgeCert)),
            ("pin mismatch keeps the expected fingerprint",
             try tlsError(TLSErrorData(reason: .pinMismatch, certificate: bridgeCert, expectedSha256: sumB.uppercased())),
             CertTrust.Problem(reason: .pinMismatch, cert: bridgeCert, expected: sumB)),
            ("invalid expected fingerprint is dropped", try tlsError(TLSErrorData(reason: .pinMismatch, expectedSha256: "nope")),
             CertTrust.Problem(reason: .pinMismatch)),
            ("certificate with a bad fingerprint is dropped",
             try tlsError(TLSErrorData(reason: .untrusted, certificate: CertificateInfo(sha256: "12:34", subject: "x", notBefore: .goZero, notAfter: .goZero))),
             CertTrust.Problem(reason: .untrusted)),
            ("certificate without a fingerprint is dropped",
             try tlsError(TLSErrorData(reason: .expired, certificate: CertificateInfo(sha256: "", subject: "x", notBefore: .goZero, notAfter: .goZero))),
             CertTrust.Problem(reason: .expired)),
            ("upper-case fingerprint is normalised",
             try tlsError(TLSErrorData(reason: .untrusted, certificate: CertificateInfo(sha256: sumA.uppercased(), notBefore: .goZero, notAfter: .goZero))),
             CertTrust.Problem(reason: .untrusted, cert: CertificateInfo(sha256: sumA, notBefore: .goZero, notAfter: .goZero))),
        ]
        for (name, e, want) in cases {
            #expect(CertTrust.details(e) == want, "\(name)")
        }
    }

    @Test func detailsFromAHandWrittenMap() throws {
        // As another daemon version might send it: members missing, one
        // unknown, dates as RFC 3339.
        let e = tlsError(try rawJSON(#"""
        {"reason":"pinMismatch",
         "certificate":{"sha256":"\#(sumB)","subject":"mail","notBefore":"2024-01-02T03:04:05Z","notAfter":"2044-01-02T03:04:05Z","selfSigned":true,"extra":1},
         "unknown":[1,2]}
        """#))
        let p = try #require(CertTrust.details(e))
        #expect(p.reason == .pinMismatch)
        let c = try #require(p.cert)
        #expect(c.sha256 == sumB && c.selfSigned && c.notAfter == notAfter && c.subject == "mail" && c.dnsNames.isEmpty)
        // Wrong types inside the certificate: no details at all rather than
        // a half-read certificate.
        #expect(CertTrust.details(tlsError(try rawJSON(#"{"reason":"untrusted","certificate":{"sha256":42}}"#))) == nil)
        // A certificate without dates is Go's zero time, not a failure.
        let bare = try #require(CertTrust.details(tlsError(try rawJSON(#"{"reason":"untrusted","certificate":{"sha256":"\#(sumA)"}}"#))))
        #expect(bare.cert?.notBefore.isGoZero == true && bare.cert?.selfSigned == false)
    }

    @Test func detailsCleanHostileCertificates() throws {
        let long = String(repeating: "ž", count: 100) // 200 bytes
        let hostile = CertificateInfo(
            sha256: sumA,
            subject: "  evil" + rlo + zwsp + "moc.knab\u{0}\u{1b}[31m\n ",
            issuer: "CA\r\nInjected: yes" + lri,
            dnsNames: ["a.example", "", lrm, "b\texample", "c", "d", "e", "f", "g", "h", "i", "j"],
            ipAddresses: ["127.0.0.1\u{85}", long],
            notBefore: notBefore, notAfter: notAfter)
        let p = try #require(CertTrust.details(try tlsError(TLSErrorData(reason: .other, certificate: hostile))))
        let c = try #require(p.cert)
        #expect(c.subject == "evilmoc.knab[31m")
        #expect(c.issuer == "CAInjected: yes")
        #expect(c.dnsNames == ["a.example", "bexample", "c", "d", "e", "f", "g", "h"])
        #expect(c.ipAddresses.count == 2 && c.ipAddresses.first == "127.0.0.1")
        let capped = c.ipAddresses.last ?? ""
        #expect(capped.utf8.count == 128 && long.hasPrefix(capped))
        let hidden: Set<UInt32> = [0x200B, 0x200E, 0x202E, 0x2066]
        for s in [c.subject ?? "", c.issuer ?? ""] + c.dnsNames + c.ipAddresses {
            #expect(!s.unicodeScalars.contains { $0.value < 0x20 || $0.value == 0x7F || (0x80..<0xA0).contains($0.value) || hidden.contains($0.value) },
                    "hidden character left in \(s.debugDescription)")
        }
    }

    @Test func cleanTextCapsOnACharacterBoundary() {
        let s = "a" + String(repeating: "€", count: 60) // 1 + 180 bytes
        let got = CertTrust.cleanText(s)
        #expect(got.utf8.count == 127 && s.hasPrefix(got))
        let invalid = String(decoding: Array("bad".utf8) + [0xFF] + Array("utf8".utf8), as: UTF8.self)
        #expect(CertTrust.cleanText(invalid) == "badutf8")
        // Trimmed again after the cut.
        #expect(CertTrust.cleanText(String(repeating: "x", count: 127) + " y") == String(repeating: "x", count: 127))
        // Line and paragraph separators (Zl, Zp) go too.
        #expect(CertTrust.cleanText("line\u{2028}para\u{2029}") == "linepara")
    }

    @Test func category() {
        let cases: [TLSErrorReason: CertTrust.Category] = [
            .untrusted: .certificate, .hostnameMismatch: .certificate, .expired: .certificate,
            .notYetValid: .certificate, .invalid: .certificate, .other: .certificate,
            "somethingNew": .certificate, "": .certificate,
            .pinMismatch: .changed,
            .handshake: .connection, .starttlsUnavailable: .connection, .tlsRequired: .connection,
        ]
        for (r, want) in cases {
            #expect(CertTrust.categoryOf(r) == want, "\(r)")
        }
        #expect(CertTrust.normalizeReason("somethingNew") == .other && CertTrust.normalizeReason(.expired) == .expired)
    }

    @Test func formatFingerprint() {
        let want = Array(repeating: "ABAB", count: 16).joined(separator: " ")
        for input in [sumA, sumA.uppercased(), "AB:" + String(repeating: "AB:", count: 30) + "AB"] {
            #expect(CertTrust.formatFingerprint(input) == want, "\(input)")
        }
        let groups = CertTrust.formatFingerprint("0123456789abcdef" + String(repeating: "0", count: 48))
        #expect(groups.hasPrefix("0123 4567 89AB CDEF 0000") && groups.count == 64 + 15)
        for input in ["", "abc", sumA + "00", String(repeating: "zz", count: 32), rlo + sumA] {
            #expect(CertTrust.formatFingerprint(input) == "", "\(input.debugDescription) accepted")
        }
    }

    @Test func trustable() throws {
        func failed(_ e: RPCError?) -> EndpointTestResult { EndpointTestResult(ok: false, error: e, latencyMs: 0) }
        func with(_ change: (inout ServerConfig) -> Void) -> ServerConfig {
            var sc = imapTLS
            change(&sc)
            return sc
        }
        func reason(_ r: TLSErrorReason) throws -> EndpointTestResult {
            failed(try tlsError(TLSErrorData(reason: r, certificate: bridgeCert)))
        }
        let untrusted = try reason(.untrusted)
        let cases: [(String, EndpointTestResult?, ServerConfig, Bool)] = [
            ("untrusted, starttls, password", untrusted, imapTLS, true),
            ("implicit TLS", untrusted, with { $0.security = .tls }, true),
            ("hostname mismatch", try reason(.hostnameMismatch), imapTLS, true),
            ("expired", try reason(.expired), imapTLS, true),
            ("not yet valid", try reason(.notYetValid), imapTLS, true),
            ("invalid", try reason(.invalid), imapTLS, true),
            ("other (macOS not standards compliant)", try reason(.other), imapTLS, true),
            ("unknown reason", try reason("fancy"), imapTLS, true),
            ("pin mismatch: trust the new certificate",
             failed(try tlsError(TLSErrorData(reason: .pinMismatch, certificate: bridgeCert, expectedSha256: sumB))),
             with { $0.certificateSha256 = sumB }, true),
            ("already pinned to this certificate", untrusted, with { $0.certificateSha256 = sumA.uppercased() }, false),
            ("nil result", nil, imapTLS, false),
            ("ok result", EndpointTestResult(ok: true, error: untrusted.error, latencyMs: 1), imapTLS, false),
            ("no error", failed(nil), imapTLS, false),
            ("other error code", failed(RPCError(code: .networkError, message: "x", data: try tlsJSON(TLSErrorData(reason: .untrusted, certificate: bridgeCert)))), imapTLS, false),
            ("tlsError without details", failed(tlsError(nil)), imapTLS, false),
            ("no certificate", failed(try tlsError(TLSErrorData(reason: .untrusted))), imapTLS, false),
            ("certificate with a bad fingerprint",
             failed(try tlsError(TLSErrorData(reason: .untrusted, certificate: CertificateInfo(sha256: "ab", notBefore: .goZero, notAfter: .goZero)))), imapTLS, false),
            ("handshake", try reason(.handshake), imapTLS, false),
            ("starttls unavailable", try reason(.starttlsUnavailable), imapTLS, false),
            ("tls required", try reason(.tlsRequired), imapTLS, false),
            ("security none", untrusted, with { $0.security = .none }, false),
            ("security empty", untrusted, with { $0.security = "" }, false),
            ("oauth2", untrusted, with { $0.authMethod = .oauth2 }, false),
            ("auth method empty", untrusted, with { $0.authMethod = "" }, false),
        ]
        for (name, res, sc, want) in cases {
            let p = CertTrust.trustable(res, sc)
            #expect((p != nil) == want, "\(name)")
            if let p {
                #expect(p.cert?.sha256 == sumA, "\(name)")
            }
        }
    }

    @Test func sameCertificate() {
        let b = cert(bridgeCert) { $0.sha256 = sumA.uppercased() }
        let other = cert(bridgeCert) { $0.sha256 = sumB }
        let invalid = CertificateInfo(sha256: "x", notBefore: .goZero, notAfter: .goZero)
        let empty = CertificateInfo(sha256: "", notBefore: .goZero, notAfter: .goZero)
        let cases: [(String, CertificateInfo?, CertificateInfo?, Bool)] = [
            ("same", bridgeCert, b, true),
            ("itself", bridgeCert, bridgeCert, true),
            ("different", bridgeCert, other, false),
            ("nil", bridgeCert, nil, false),
            ("both nil", nil, nil, false),
            ("both invalid", invalid, invalid, false),
            ("both empty", empty, empty, false),
        ]
        for (name, x, y, want) in cases {
            #expect(CertTrust.sameCertificate(x, y) == want, "\(name)")
        }
    }

    @Test func fromSyncState() throws {
        let untrusted = try tlsError(TLSErrorData(reason: .untrusted, certificate: bridgeCert))
        func st(_ status: SyncStatus, _ e: RPCError?) -> SyncState {
            SyncState(accountId: "a", status: status, error: e)
        }
        let cases: [(String, SyncState, CertTrust.Category?)] = [
            ("offline untrusted", st(.offline, untrusted), .certificate),
            ("error status counts too", st(.error, untrusted), .certificate),
            ("without certificate", st(.offline, try tlsError(TLSErrorData(reason: .expired))), .certificate),
            ("unknown reason", st(.offline, try tlsError(TLSErrorData(reason: "later"))), .certificate),
            ("pin mismatch", st(.offline, try tlsError(TLSErrorData(reason: .pinMismatch, certificate: bridgeCert, expectedSha256: sumB))), .changed),
            ("handshake is offline", st(.offline, try tlsError(TLSErrorData(reason: .handshake))), nil),
            ("starttls unavailable is offline", st(.offline, try tlsError(TLSErrorData(reason: .starttlsUnavailable))), nil),
            ("tls required is offline", st(.offline, try tlsError(TLSErrorData(reason: .tlsRequired))), nil),
            ("tlsError without details", st(.offline, tlsError(nil)), nil),
            ("network error", st(.offline, RPCError(code: .networkError, message: "x")), nil),
            ("no error", st(.offline, nil), nil),
            // Connected again: the pass runs while error still holds the
            // last failure.
            ("syncing", st(.syncing, untrusted), nil),
            ("idle", st(.idle, untrusted), nil),
            ("disabled", st(.disabled, untrusted), nil),
            ("auth required", st(.authRequired, untrusted), nil),
        ]
        for (name, s, want) in cases {
            #expect(CertTrust.fromSyncState(s)?.category == want, "\(name)")
        }
    }

    @Test func keepPin() {
        var old = imapTLS
        old.certificateSha256 = sumA
        func with(_ change: (inout ServerConfig) -> Void) -> ServerConfig {
            var sc = imapTLS
            change(&sc)
            return sc
        }
        let cases: [(String, ServerConfig, ServerConfig, String)] = [
            ("unchanged", old, imapTLS, sumA),
            ("user name changed", old, with { $0.username = "other" }, sumA),
            ("security tls", old, with { $0.security = .tls }, sumA),
            ("host case and spaces", old, with { $0.host = "  Bridge.Tail.EXAMPLE " }, sumA),
            ("new config already has another pin", old, with { $0.certificateSha256 = sumB }, sumA),
            ("upper-case pin is normalised", with { $0.certificateSha256 = sumA.uppercased() }, imapTLS, sumA),
            ("host changed", old, with { $0.host = "bridge2.tail.example" }, ""),
            ("port changed", old, with { $0.port = 993 }, ""),
            ("security none", old, with { $0.security = .none }, ""),
            ("oauth2", old, with { $0.authMethod = .oauth2 }, ""),
            ("no pin", imapTLS, imapTLS, ""),
            ("invalid pin", with { $0.certificateSha256 = "abc" }, imapTLS, ""),
            // Compared as Go compares: a decomposed name is another name.
            ("composed vs decomposed host", with { $0.host = "caf\u{E9}.example"; $0.certificateSha256 = sumA },
             with { $0.host = "cafe\u{301}.example" }, ""),
            ("same composed host, other case", with { $0.host = "caf\u{E9}.example"; $0.certificateSha256 = sumA },
             with { $0.host = "CAF\u{C9}.EXAMPLE" }, sumA),
        ]
        for (name, o, cur, want) in cases {
            #expect(CertTrust.keepPin(o, cur) == want, "\(name)")
        }
    }

    @Test func serverName() {
        let cases: [(ServerConfig, String)] = [
            (ServerConfig(host: "imap.example", port: 993, security: .tls, username: "", authMethod: .password), "imap.example:993"),
            (ServerConfig(host: " 100.64.0.1 ", port: 1143, security: .tls, username: "", authMethod: .password), "100.64.0.1:1143"),
            (ServerConfig(host: "fd7a:115c::1", port: 1025, security: .tls, username: "", authMethod: .password), "[fd7a:115c::1]:1025"),
        ]
        for (sc, want) in cases {
            #expect(CertTrust.serverName(sc) == want)
        }
    }

    @Test func issuedTo() {
        func c(subject: String? = nil, dns: [String] = [], ips: [String] = []) -> CertificateInfo {
            CertificateInfo(sha256: sumA, subject: subject, dnsNames: dns, ipAddresses: ips, notBefore: .goZero, notAfter: .goZero)
        }
        let cases: [(String, CertificateInfo, String)] = [
            ("bridge: subject is the only name", bridgeCert, "127.0.0.1"),
            ("names after the subject", c(subject: "mail.example", dns: ["MAIL.example", "imap.example", "imap.example"], ips: ["10.0.0.1"]),
             "mail.example\nimap.example, 10.0.0.1"),
            ("no subject", c(dns: ["a.example", "b.example"]), "a.example, b.example"),
            ("nothing", c(), ""),
            ("hostile", c(subject: "x" + rlo + "\ny", dns: [zwsp, "z\u{0}"]), "xy\nz"),
            // Duplicates are found as Go finds them: literally, ignoring case.
            ("composed and decomposed names both stay", c(subject: "caf\u{E9}", dns: ["cafe\u{301}", "CAF\u{C9}"]),
             "caf\u{E9}\ncafe\u{301}"),
        ]
        for (name, cert, want) in cases {
            #expect(CertTrust.issuedTo(cert) == want, "\(name)")
        }
    }

    // MARK: The confirmation (accountwizard trust.go)

    @Test func trustPromptNamesTheServers() {
        let one = trustPrompt(bridgeCert, servers: ["bridge.tail.example:1143"], now: notBefore)
        #expect(one.heading == "Trust This Certificate?")
        #expect(one.confirmLabel == "_Trust")
        #expect(one.body.contains("accept exactly this certificate for bridge.tail.example:1143 and no other"))
        let two = trustPrompt(bridgeCert, servers: ["h:1143", "h:1025"], now: notBefore)
        #expect(two.body.contains("for h:1143 and h:1025 and no other"))
    }

    /// A pinned certificate that changed: its own heading and warning,
    /// the fingerprint trusted before right after the new one.
    @Test func trustPromptForAChangedCertificate() {
        let p = trustPrompt(bridgeCert, servers: ["h:1143", "h:1025"], changed: true, previous: sumB, now: notBefore)
        #expect(p.heading == "Trust the New Certificate?")
        #expect(p.confirmLabel == "_Trust")
        #expect(p.body == "The certificate of h:1143 and h:1025 has changed since you trusted it. If you did not renew it yourself, someone may be intercepting the connection. Trust the new certificate only if you know why it changed.")
        #expect(p.details.prefix(2) == [
            CertificateDetail(label: "SHA-256 fingerprint", value: Array(repeating: "ABAB", count: 16).joined(separator: " "), monospaced: true),
            CertificateDetail(label: "Previously trusted", value: Array(repeating: "0F0F", count: 16).joined(separator: " "), monospaced: true),
        ])
        // Without a known earlier fingerprint the line is left out.
        let unknown = trustPrompt(bridgeCert, servers: ["h:1143"], changed: true, previous: "", now: notBefore)
        #expect(unknown.heading == "Trust the New Certificate?")
        #expect(!unknown.details.contains { $0.label == "Previously trusted" })
    }

    @Test func certificateDetailsInOrder() {
        let now = Date(timeIntervalSince1970: 1_790_000_000) // 2026
        // Noon UTC, so the dates are the same in every time zone.
        let noon = cert(bridgeCert) {
            $0.notBefore = Date(timeIntervalSince1970: 1_704_196_800) // 2024-01-02T12:00:00Z
            $0.notAfter = Date(timeIntervalSince1970: 2_335_348_800) // 2044-01-02T12:00:00Z
        }
        // The fingerprint first: nothing the certificate says can pose as it.
        let d = certificateDetails(noon, now: now)
        #expect(d.map(\.label) == ["SHA-256 fingerprint", "Issued to", "Issued by", "Valid", "Self-signed"])
        #expect(d[0].value == Array(repeating: "ABAB", count: 16).joined(separator: " ") && d[0].monospaced)
        #expect(d[1].value == "127.0.0.1")
        #expect(d[2].value == "127.0.0.1")
        #expect(d[3].value == "2024-01-02 to 2044-01-02")
        #expect(d[4].value.isEmpty)
        #expect(d.filter(\.monospaced).count == 1)
        let changed = certificateDetails(noon, previous: sumB.uppercased(), now: now)
        #expect(changed.map(\.label) == ["SHA-256 fingerprint", "Previously trusted", "Issued to", "Issued by", "Valid", "Self-signed"])
        #expect(changed[1].monospaced && changed[1].value == Array(repeating: "0F0F", count: 16).joined(separator: " "))
        #expect(certificateDetails(noon, previous: "nope", now: now).count == 5, "an invalid earlier fingerprint is left out")

        // Empty values and unset dates are left out; not self-signed, no line.
        let bare = CertificateInfo(sha256: sumB, notBefore: .goZero, notAfter: notAfter)
        #expect(certificateDetails(bare, now: now).map(\.label) == ["SHA-256 fingerprint"])
    }
}
