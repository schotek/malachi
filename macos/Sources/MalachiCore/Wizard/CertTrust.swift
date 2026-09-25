// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/certtrust: the certificate of a failed TLS connection,
// cleaned for display, and the rules for trusting it by pinning its
// SHA-256 fingerprint to an IMAP/SMTP endpoint (docs/security.md §7). No
// texts: the sentences are the wizard's and the window's.

import Foundation

/// The certtrust package: a namespace, so the Go names map 1:1
/// (`certtrust.Details` → `CertTrust.details`).
public enum CertTrust {
    /// certtrust.maxText: the longest certificate string shown, in UTF-8
    /// bytes (the daemon's own cap).
    public static let maxText = 128

    /// certtrust.maxList: the most names of a certificate shown.
    public static let maxList = 8

    /// certtrust.Problem: the cleaned details of a `tlsError`.
    public struct Problem: Sendable, Equatable {
        /// A reason this client does not know is `other`.
        public var reason: TLSErrorReason
        /// The certificate the server presented; nil when there was none
        /// or its fingerprint was not 64 hex digits.
        public var cert: CertificateInfo?
        /// pinMismatch: the pinned fingerprint (64 lowercase hex); "" otherwise.
        public var expected: String

        public init(reason: TLSErrorReason, cert: CertificateInfo? = nil, expected: String = "") {
            self.reason = reason
            self.cert = cert
            self.expected = expected
        }

        /// certtrust.Problem.Category.
        public var category: Category { CertTrust.categoryOf(reason) }
    }

    /// certtrust.Category: what kind of TLS failure a reason is.
    public enum Category: Sendable, Equatable {
        /// The connection could not be secured before a certificate was
        /// judged: handshake, starttlsUnavailable, tlsRequired.
        case connection
        /// The certificate was refused: untrusted, hostnameMismatch,
        /// expired, notYetValid, invalid, other, and any unknown reason.
        case certificate
        /// Not the pinned certificate: pinMismatch.
        case changed
    }

    /// certtrust.CategoryOf: sorts a reason; unknown reasons count as
    /// `other`.
    public static func categoryOf(_ reason: TLSErrorReason) -> Category {
        switch normalizeReason(reason) {
        case .handshake, .starttlsUnavailable, .tlsRequired:
            return .connection
        case .pinMismatch:
            return .changed
        default:
            return .certificate
        }
    }

    /// certtrust.NormalizeReason: `r` when it is a reason of docs/api.md §2,
    /// `other` for anything else, the empty reason included.
    public static func normalizeReason(_ r: TLSErrorReason) -> TLSErrorReason {
        TLSErrorReason.all.contains(r) ? r : .other
    }

    /// certtrust.Details: the TLS details of `e` with every string cleaned
    /// again for display (the daemon's cleaning is not relied on): control
    /// and format characters removed, trimmed, at most `maxText` bytes,
    /// lists of at most `maxList` non-empty entries. A certificate whose
    /// fingerprint is not 64 hex digits is dropped whole; so is such an
    /// expected fingerprint. nil when `e` is not a `tlsError`, has no
    /// details or no reason.
    public static func details(_ e: RPCError?) -> Problem? {
        guard let raw = tlsErrorData(e), !raw.reason.rawValue.isEmpty else { return nil }
        var p = Problem(reason: normalizeReason(raw.reason), cert: cleanCertificate(raw.certificate))
        p.expected = normalizeCertificateSHA256(raw.expectedSha256 ?? "") ?? ""
        return p
    }

    /// certtrust.cleanCertificate: nil without a valid fingerprint.
    private static func cleanCertificate(_ c: CertificateInfo?) -> CertificateInfo? {
        guard let c, let sum = normalizeCertificateSHA256(c.sha256) else { return nil }
        return CertificateInfo(
            sha256: sum,
            subject: nonEmpty(cleanText(c.subject ?? "")),
            issuer: nonEmpty(cleanText(c.issuer ?? "")),
            dnsNames: cleanList(c.dnsNames),
            ipAddresses: cleanList(c.ipAddresses),
            notBefore: c.notBefore,
            notAfter: c.notAfter,
            selfSigned: c.selfSigned
        )
    }

    /// certtrust.cleanText: `s` without the replacement character (what
    /// invalid UTF-8 became), control characters (Cc, newlines included),
    /// format characters (Cf, such as U+202E, which reverses what follows)
    /// and the line and paragraph separators (Zl, Zp), trimmed of
    /// White_Space (strings.TrimSpace) and cut to `maxText` bytes on a
    /// character boundary, then trimmed again.
    public static func cleanText(_ s: String) -> String {
        var kept = String.UnicodeScalarView()
        for c in s.unicodeScalars {
            switch c.properties.generalCategory {
            case .control, .format, .lineSeparator, .paragraphSeparator:
                continue
            default:
                if c.value != 0xFFFD {
                    kept.append(c)
                }
            }
        }
        let out = trimSpace(String(kept))
        if out.utf8.count <= maxText {
            return out
        }
        var cut = String.UnicodeScalarView()
        var bytes = 0
        for c in out.unicodeScalars {
            let n = String(c).utf8.count
            if bytes + n > maxText {
                break
            }
            bytes += n
            cut.append(c)
        }
        return trimSpace(String(cut))
    }

    /// certtrust.cleanList: the cleaned, non-empty entries, at most
    /// `maxList` of them.
    public static func cleanList(_ list: [String]) -> [String] {
        var out: [String] = []
        for s in list {
            let c = cleanText(s)
            if c.isEmpty {
                continue
            }
            out.append(c)
            if out.count == maxList {
                break
            }
        }
        return out
    }

    /// certtrust.FormatFingerprint: a SHA-256 fingerprint as upper-case hex
    /// in 16 groups of four joined by spaces ("AB12 CD34 …"), as it is
    /// compared by eye; "" for anything that is not a fingerprint.
    public static func formatFingerprint(_ hex: String) -> String {
        guard let norm = normalizeCertificateSHA256(hex) else { return "" }
        var out = ""
        for (i, c) in norm.uppercased().enumerated() {
            if i > 0, i % 4 == 0 {
                out.append(" ")
            }
            out.append(c)
        }
        return out
    }

    /// certtrust.Pinnable: the endpoint may carry a pin at all: TLS or
    /// STARTTLS with a password (never plaintext, and the tokens of an
    /// oauth2 endpoint never go to a pinned server).
    public static func pinnable(_ sc: ServerConfig) -> Bool {
        (sc.security == .tls || sc.security == .starttls) && sc.authMethod == .password
    }

    /// certtrust.Trustable: the problem of a failed endpoint of
    /// `account.test` whose certificate the user may trust: a certificate
    /// was presented and judged (not a connection failure), the endpoint
    /// may carry a pin, and it does not pin this very certificate already.
    /// A pinMismatch offers the new certificate. nil otherwise (and for
    /// Graph, which the wizard never asks about).
    public static func trustable(_ res: EndpointTestResult?, _ sc: ServerConfig) -> Problem? {
        guard let res, !res.ok, let p = details(res.error), let cert = p.cert, p.category != .connection,
              pinnable(sc) else { return nil }
        if cert.sha256 == normalizeCertificateSHA256(sc.certificateSha256 ?? "") {
            return nil
        }
        return p
    }

    /// certtrust.SameCertificate: both endpoints presented one and the same
    /// certificate (a mail bridge serving IMAP and SMTP): one confirmation
    /// pins both.
    public static func sameCertificate(_ a: CertificateInfo?, _ b: CertificateInfo?) -> Bool {
        guard let a, let b, let x = normalizeCertificateSHA256(a.sha256), let y = normalizeCertificateSHA256(b.sha256) else {
            return false
        }
        return x == y
    }

    /// certtrust.FromSyncState: the certificate problem of an account: it is
    /// offline or failed and its last error is a `tlsError` about the
    /// certificate (with or without the certificate itself). nil for
    /// anything else, a connection failure included (that stays
    /// "Offline").
    public static func fromSyncState(_ s: SyncState) -> Problem? {
        guard s.status == .offline || s.status == .error, let p = details(s.error), p.category != .connection else {
            return nil
        }
        return p
    }

    /// certtrust.KeepPin: `old`'s pin for `new` only while the host
    /// (trimmed, ignoring case, as DNS does) and the port are those it was
    /// trusted for and `new` may carry a pin; "" otherwise. The wizard keeps
    /// the configuration a pin was set for, so a changed host drops the pin
    /// and typing the old one again restores it; a pin never moves to
    /// another server.
    public static func keepPin(_ old: ServerConfig, _ cur: ServerConfig) -> String {
        guard let pin = normalizeCertificateSHA256(old.certificateSha256 ?? ""), pinnable(cur), old.port == cur.port,
              foldKey(trimSpace(old.host)) == foldKey(trimSpace(cur.host)) else { return "" }
        return pin
    }

    /// The case-folded scalars of `s`, compared literally as Go compares
    /// strings (strings.EqualFold, strings.ToLower keys): no canonical
    /// equivalence, so a composed and a decomposed name differ there and
    /// here, which Swift's `==` on String would not.
    static func foldKey(_ s: String) -> [Unicode.Scalar] {
        Array(s.lowercased().unicodeScalars)
    }

    /// certtrust.ServerName: "host:port" for the texts, an IPv6 literal in
    /// brackets (net.JoinHostPort).
    public static func serverName(_ sc: ServerConfig) -> String {
        let h = trimSpace(sc.host)
        if h.contains(":") {
            return "[" + h + "]:" + String(sc.port)
        }
        return h + ":" + String(sc.port)
    }

    /// certtrust.IssuedTo: whom the certificate was issued to: the subject
    /// on the first line, the DNS names and then the IP addresses on the
    /// second (joined with ", ", duplicates and the subject itself left out,
    /// ignoring case); "" when there is nothing.
    public static func issuedTo(_ c: CertificateInfo) -> String {
        var lines: [String] = []
        let subject = cleanText(c.subject ?? "")
        if !subject.isEmpty {
            lines.append(subject)
        }
        var seen: Set<[Unicode.Scalar]> = [foldKey(subject)]
        var names: [String] = []
        for n in cleanList(c.dnsNames) + cleanList(c.ipAddresses) {
            let key = foldKey(n)
            if seen.contains(key) {
                continue
            }
            seen.insert(key)
            names.append(n)
        }
        if !names.isEmpty {
            lines.append(names.joined(separator: ", "))
        }
        return lines.joined(separator: "\n")
    }

    /// Go's "" as nil.
    private static func nonEmpty(_ s: String) -> String? {
        s.isEmpty ? nil : s
    }

    /// strings.TrimSpace: only White_Space goes.
    private static func trimSpace(_ s: String) -> String {
        let scalars = s.unicodeScalars
        guard let first = scalars.firstIndex(where: { !$0.properties.isWhitespace }),
              let last = scalars.lastIndex(where: { !$0.properties.isWhitespace }) else { return "" }
        return String(scalars[first...last])
    }
}
