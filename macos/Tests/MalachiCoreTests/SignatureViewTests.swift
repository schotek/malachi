// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// ui/internal/signature/signature_test.go (English catalogue: the msgids
// come back verbatim). The Go po_test.go only checks the template against
// the msgids the package asks for; the macOS catalogues are generated from
// po/ and the msgid coverage is `check-strings.py`'s business.

private let arrived = RFC3339.parse("2026-11-02T08:00:00Z") ?? .goZero
private let fp40 = "0123456789abcdef0123456789abcdef01234567"
private let fp64 = "ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34"

/// time.Format("2006-01-02") in UTC.
private func day(_ d: Date) -> String {
    var cal = Calendar(identifier: .gregorian)
    cal.timeZone = TimeZone(identifier: "UTC") ?? .gmt
    let c = cal.dateComponents([.year, .month, .day], from: d)
    return String(format: "%04d-%02d-%02d", c.year ?? 0, c.month ?? 0, c.day ?? 0)
}

private func hours(_ n: Double) -> Date { arrived.addingTimeInterval(n * 3600) }

private let allStatuses: [SignatureStatus] = [
    .invalid, .revoked, .mismatch, .untrusted, .unknownKey, .weak, .unsupported, .error, .valid, .validSeen,
]

private func keys(_ rows: [SignatureView.Row]) -> [SignatureView.RowKey] { rows.map(\.key) }

@Suite struct SignatureViewTests {
    @Test func severityIconStyle() {
        typealias V = SignatureView
        let cases: [(SignatureStatus, V.Severity, String, String)] = [
            (.valid, .good, "security-high-symbolic", "success"),
            (.validSeen, .goodSeen, "security-medium-symbolic", "success"),
            (.mismatch, .warning, "dialog-warning-symbolic", "warning"),
            (.untrusted, .warning, "dialog-warning-symbolic", "warning"),
            (.weak, .warning, "dialog-warning-symbolic", "warning"),
            (.invalid, .bad, "dialog-error-symbolic", "error"),
            (.revoked, .bad, "dialog-error-symbolic", "error"),
            (.unknownKey, .neutral, "security-low-symbolic", "dim-label"),
            (.unsupported, .neutral, "security-low-symbolic", "dim-label"),
            (.error, .neutral, "security-low-symbolic", "dim-label"),
            ("somethingNew", .neutral, "security-low-symbolic", "dim-label"),
        ]
        for (status, sev, icon, style) in cases {
            let got = V.severity(of: status)
            #expect(got == sev && V.icon(of: got) == icon && V.style(of: got) == style, "\(status)")
        }
        #expect(V.icon(of: .none) == "" && V.style(of: .none) == "")
        #expect(V.iconPage == "security-high-symbolic")
    }

    @Test func symbolsAreTheDocumentedOnes() {
        typealias V = SignatureView
        #expect(V.symbolName(V.iconGood) == "checkmark.shield.fill")
        #expect(V.symbolName(V.iconGoodSeen) == "shield.lefthalf.filled")
        #expect(V.symbolName(V.iconWarning) == "exclamationmark.triangle.fill")
        #expect(V.symbolName(V.iconBad) == "xmark.octagon.fill")
        #expect(V.symbolName(V.iconNeutral) == "shield")
        #expect(V.symbolName("unknown") == "shield")
    }

    @Test func listIcon() {
        #expect(SignatureView.listIcon(for: nil) == nil)
        let want: Set<SignatureStatus> = [.valid, .validSeen, .invalid, .revoked, .mismatch]
        for s in allStatuses + ["somethingNew"] {
            let li = SignatureView.listIcon(for: MessageSignature(kind: .smime, status: s))
            #expect((li != nil) == want.contains(s), "\(s)")
            if let li {
                let sev = SignatureView.severity(of: s)
                #expect(li.icon == SignatureView.icon(of: sev) && li.style == SignatureView.style(of: sev))
                #expect(li.tooltip == SignatureView.statusTitle(.smime, s))
            }
        }
    }

    @Test func signerLabel() {
        let u200b = "\u{200B}", rlo = "\u{202E}"
        let cases: [(SignatureSigner?, String)] = [
            (nil, ""),
            (SignatureSigner(), ""),
            (SignatureSigner(name: "Jan Novák", email: "jan@example.org"), "Jan Novák <jan@example.org>"),
            (SignatureSigner(email: "jan@example.org"), "jan@example.org"),
            (SignatureSigner(name: "Jan Novák"), "Jan Novák"),
            (SignatureSigner(name: "JAN@example.org", email: "jan@example.org"), "jan@example.org"),
            // Hostile: bidi override, zero-width, newline, control.
            (SignatureSigner(name: "Jan\(rlo) kávoN\(u200b)\nX\u{07}", email: " jan@example.org "), "Jan kávoN X <jan@example.org>"),
        ]
        for (s, want) in cases {
            #expect(SignatureView.signerLabel(s) == want, "\(String(describing: s))")
        }
    }

    @Test func cleanCapsOnScalarBoundary() {
        let long = String(repeating: "ž", count: 200) // 400 bytes
        let got = SignatureView.clean(long)
        #expect(got.utf8.count == 256 && long.hasPrefix(got))
        #expect(SignatureView.clean("\u{2028}a\u{2029}b\u{E000}") == "a b", "separators and private use")
        #expect(SignatureView.clean("a\u{FFFD}b") == "ab")
    }

    @Test func badge() {
        #expect(SignatureView.badge(for: nil) == SignatureView.Badge())
        let jan = SignatureSigner(name: "Jan", email: "jan@example.org")
        let cases: [(SignatureKind, SignatureStatus, SignatureSigner?, [SignatureNote], String)] = [
            (.smime, .valid, jan, [], "Signed by Jan <jan@example.org>"),
            (.pgp, .validSeen, jan, [], "Signed by Jan <jan@example.org>"),
            (.smime, .valid, nil, [], "Valid signature"),
            (.smime, .mismatch, jan, [], "Signed by someone else: Jan <jan@example.org>"),
            (.smime, .mismatch, nil, [], "Signed by someone else"),
            (.smime, .untrusted, jan, [], "Signed by Jan <jan@example.org>, but the certificate is not trusted"),
            (.pgp, .untrusted, jan, [], "Signed by Jan <jan@example.org>, but the key had expired"),
            (.pgp, .untrusted, nil, [], "Untrusted signature"),
            (.smime, .weak, jan, [], "Signed by Jan <jan@example.org> with a weak algorithm"),
            (.smime, .weak, nil, [], "Weak signature"),
            (.smime, .revoked, jan, [], "Signed with a revoked certificate"),
            (.pgp, .revoked, jan, [], "Signed with a revoked key"),
            (.smime, .invalid, jan, [], "Invalid signature"),
            (.pgp, .unknownKey, nil, [], "Signed with an unknown OpenPGP key"),
            (.pgp, .unsupported, nil, [.inlinePGP], "Inline OpenPGP signature, not verified"),
            (.smime, .unsupported, nil, [], "Signature not verified"),
            (.smime, .error, nil, [], "Signature could not be verified"),
            (.smime, "somethingNew", jan, [], "Signature could not be verified"),
        ]
        for (kind, status, signer, notes, want) in cases {
            let b = SignatureView.badge(for: SignatureSummary(kind: kind, status: status, signer: signer, notes: notes))
            let icon = SignatureView.icon(of: SignatureView.severity(of: status))
            #expect(b.visible && b.text == want && b.tooltip == "Show Signature Details" && b.icon == icon, "\(kind) \(status)")
        }
    }

    @Test func titlesAndExplanationsCoverEveryStatus() {
        for k in [SignatureKind.smime, .pgp] {
            for s in allStatuses {
                #expect(!SignatureView.statusTitle(k, s).isEmpty, "\(k) \(s)")
                #expect(!SignatureView.explanation(SignatureSummary(kind: k, status: s), from: "").isEmpty, "\(k) \(s)")
            }
        }
        let m = SignatureSummary(kind: .smime, status: .mismatch, signer: SignatureSigner(email: "boss@evil.example"))
        #expect(SignatureView.explanation(m, from: "boss@example.org")
            == "The signature is valid, but it belongs to boss@evil.example, not to the sender boss@example.org.")
        #expect(SignatureView.explanation(m, from: "") == "The signature is valid, but it does not belong to the sender's address.")
    }

    @Test func notesAndReasons() {
        let notes: [SignatureNote] = [
            .expiredSince, .revocationUnchecked, .revocationGood, .revokedAfter, .partialFooter,
            .protectedHeadersDiffer, .inlinePGP, .keyChanged, .opaque,
        ]
        for k in [SignatureKind.smime, .pgp] {
            for n in notes {
                #expect(!SignatureView.noteText(k, n).isEmpty, "\(k) \(n)")
            }
        }
        #expect(SignatureView.noteText(.pgp, .expiredSince) != SignatureView.noteText(.smime, .expiredSince))
        #expect(SignatureView.noteText(.smime, "unknownNote") == "")
        let reasons: [SignatureReason] = [
            .digestMismatch, .badSignature, .malformed, .md5, .keyUsage, .unknownIssuer, .selfSigned, .notYetValid, .expired,
            .sha1, .shortKey, .algorithm, .multipleSigners, .rebuilt, .format, .limit,
        ]
        for r in reasons {
            #expect(!SignatureView.reasonText(.smime, r).isEmpty, "\(r)")
        }
        for r: SignatureReason? in [nil, "", .internal, "unknownReason"] {
            #expect(SignatureView.reasonText(.smime, r) == "", "\(String(describing: r))")
        }
    }

    @Test func tiles() {
        let atts = [
            Attachment(partId: "1.2", filename: "a.pdf", contentType: "application/pdf", size: 1, inline: false),
            Attachment(partId: "2", filename: "smime.p7s", contentType: "application/pkcs7-signature", size: 1, inline: false),
        ]
        #expect(SignatureView.tileAttachments(atts, nil) == atts)
        let sig = SignatureSummary(kind: .smime, status: .valid, partId: "2")
        #expect(SignatureView.tileAttachments(atts, sig).map(\.partId) == ["1.2"])
        // Opaque S/MIME and inline OpenPGP have no part: nothing is hidden,
        // and a part without an id is never taken for it.
        let noPart = SignatureView.tileAttachments(
            atts + [Attachment(partId: "", filename: "", contentType: "", size: 0, inline: false)], SignatureSummary(kind: .smime, status: .valid))
        #expect(noPart.count == 3)
        // A signature part of another verdict's message (a forwarded signed
        // message) stays.
        let other = Attachment(partId: "3.2", filename: "signature.asc", contentType: "application/pgp-signature", size: 1, inline: false)
        #expect(!SignatureView.hidesPart(other, sig))
    }

    @Test func chipPlanHidesTheSignaturePart() {
        let atts = [
            Attachment(partId: "1.2", filename: "a.pdf", contentType: "application/pdf", size: 1, inline: false),
            Attachment(partId: "2", filename: "smime.p7s", contentType: "application/pkcs7-signature", size: 1, inline: false),
        ]
        func message(_ sig: SignatureSummary?) -> Message {
            let s = MessageSummary(
                id: "m1", accountId: "a1", folderId: "f1", from: [], subject: "", date: .goZero, snippet: "", flags: [],
                hasAttachments: true, size: 0)
            return Message(summary: s, attachments: atts, signature: sig)
        }
        #expect(ChipPlan(message(nil), nil).chips.map(\.attachment.partId) == ["1.2", "2"])
        #expect(ChipPlan(message(SignatureSummary(kind: .smime, status: .valid, partId: "2")), nil).chips.map(\.attachment.partId) == ["1.2"])
    }

    @Test func formatFingerprint() {
        let cases: [String: String] = [
            "": "",
            "0123456789abcdef": "0123 4567 89AB CDEF",
            fp40: "0123 4567 89AB CDEF 0123 4567 89AB CDEF 0123 4567",
            fp64: "AB12 CD34 AB12 CD34 AB12 CD34 AB12 CD34 AB12 CD34 AB12 CD34 AB12 CD34 AB12 CD34",
            "0123456789abcdeg": "",
            "0123": "",
            "0123456789ABCDEF0123456789ABCDEF0123456": "",
        ]
        for (input, want) in cases {
            #expect(SignatureView.formatFingerprint(input) == want, "\(input)")
        }
    }

    @Test func detailsSMIME() throws {
        let signed = hours(-1), expired = hours(24)
        var d = SignatureDetails(
            summary: SignatureSummary(
                kind: .smime, status: .valid, signer: SignatureSigner(name: "Jan", email: "jan@example.org", fingerprint: fp64),
                notes: [.revocationUnchecked, .expiredSince, "unknownNote"], partId: "2"),
            messageId: "m_1", signedAt: signed, arrivedAt: arrived, algorithm: "RSA 2048 / SHA-256", expiredAt: expired,
            protectedHeaders: [
                ProtectedHeader(name: .subject, value: "Real\u{202E} subject"), ProtectedHeader(name: "unknown", value: "x"),
            ],
            smime: SMIMEDetails(
                chain: [
                    SignatureCertificate(
                        sha256: fp64, subject: "CN=Jan", issuer: "CN=CA 2", emails: ["jan@example.org"], notBefore: arrived, notAfter: expired),
                    SignatureCertificate(sha256: fp64, subject: "CN=CA 2", issuer: "CN=Root", notBefore: arrived, notAfter: expired),
                    SignatureCertificate(
                        sha256: fp64, subject: "CN=Root", issuer: "CN=Root", notBefore: arrived, notAfter: expired,
                        systemRoot: true, selfSigned: true),
                ],
                revocation: SMIMERevocation(state: .unchecked, checkable: true)))
        let got = SignatureView.details(d, from: "jan@example.org", busy: .none, date: day)
        #expect(got.title == "Valid signature" && got.severity == .good && !got.caveat.isEmpty)
        #expect(keys(got.rows) == [.signer, .kind, .signedAt, .arrivedAt, .algorithm, .revocation, .expired])
        #expect(got.rows[0].value == "Jan <jan@example.org>" && got.rows[1].value == "S/MIME" && got.rows[5].value == "Not checked")
        #expect(got.notes == ["The certificate has expired since the message arrived."])
        #expect(got.sections.map(\.key) == [.signerCertificate, .intermediate, .root, .signedHeaders])
        let signer = got.sections[0]
        #expect(keys(signer.rows) == [.issuedTo, .emails, .issuedBy, .validity, .certFingerprint])
        #expect(signer.rows[3].value == "2026-11-02 to 2026-11-03" && signer.rows[4].monospace)
        let hs = got.sections[3].rows
        #expect(hs.count == 1 && hs[0].label == "Subject" && hs[0].value == "Real subject")
        #expect(got.buttons == [SignatureView.Button(action: .checkRevocation, label: "Check Revocation", enabled: true)])
        #expect(!got.revocationCaption.isEmpty)
        // While it runs, and once checked.
        let busy = SignatureView.details(d, from: "", busy: .checkRevocation, date: day)
        #expect(busy.buttons[0] == SignatureView.Button(action: .checkRevocation, label: "Checking…", enabled: false))
        d.smime?.revocation = SMIMERevocation(state: .good, checkedAt: hours(1), checkable: true)
        let again = SignatureView.details(d, from: "", busy: .none, date: day)
        #expect(again.buttons[0].label == "Check Revocation Again" && again.rows[5].value == "Not revoked (checked 2026-11-02)")
        // Without a responder, or without the certificate, no button.
        d.smime?.revocation.checkable = false
        #expect(SignatureView.details(d, from: "", busy: .none, date: day).buttons.isEmpty)
        d.smime?.revocation.checkable = true
        d.smime?.chain = []
        let none = SignatureView.details(d, from: "", busy: .none, date: day)
        #expect(none.buttons.isEmpty && none.sections.count == 1)
    }

    @Test func revocationValues() {
        let at = arrived
        let cases: [(SMIMERevocation, String)] = [
            (SMIMERevocation(state: .unchecked), "Not checked"),
            (SMIMERevocation(state: .good, checkedAt: at), "Not revoked (checked 2026-11-02)"),
            (SMIMERevocation(state: .good), "Not checked"),
            (SMIMERevocation(state: .revoked, revokedAt: at), "Revoked on 2026-11-02"),
            (SMIMERevocation(state: .revoked), "Revoked"),
            (SMIMERevocation(state: .unknown, checkedAt: at), "Unknown to the issuer (checked 2026-11-02)"),
            (SMIMERevocation(state: "new"), "Not checked"),
        ]
        for (r, want) in cases {
            #expect(SignatureView.revocationText(r, date: day) == want, "\(r)")
        }
    }

    @Test func detailsPGP() {
        let seen = hours(-48)
        var d = SignatureDetails(
            summary: SignatureSummary(
                kind: .pgp, status: .validSeen, signer: SignatureSigner(name: "Jan", email: "jan@example.org", fingerprint: fp40),
                notes: [.keyChanged]),
            messageId: "m_2", arrivedAt: arrived,
            pgp: PGPDetails(
                keyTrust: .seen, keyId: "89abcdef01234567", primaryFingerprint: fp40,
                userIds: ["Jan <jan@example.org>", "", "Jan (work) <jan@work.example>"], keySource: .autocrypt, firstSeen: seen,
                previousFingerprint: String(repeating: "ef", count: 20),
                importable: PGPKeyOffer(fingerprint: fp40, autocrypt: true)))
        var got = SignatureView.details(d, from: "", busy: .none, date: day)
        #expect(keys(got.rows) == [.signer, .kind, .arrivedAt, .keyTrust, .keyFingerprint, .keySource, .firstSeen, .userIDs, .previousKey])
        #expect(got.rows[3].value == "Found automatically, not confirmed")
        #expect(got.rows[7].value == "Jan <jan@example.org>\nJan (work) <jan@work.example>")
        #expect(got.rows[5].value == "Autocrypt header of a message")
        #expect(got.notes == ["This sender used a different key before."])
        #expect(got.buttons.map(\.action) == [.importKey, .confirmKey])
        #expect(got.keyFingerprint == SignatureView.formatFingerprint(fp40) && got.revocationCaption == "")
        #expect(SignatureView.importParams(d) == PGPKeyImportParams(messageId: "m_2", autocrypt: true))
        #expect(SignatureView.confirmParams(d) == PGPKeyVerifyParams(fingerprint: fp40, verified: true))

        // An unknown key: the key ID, Import Key from an attachment, no Confirm.
        let unknown = SignatureDetails(
            summary: SignatureSummary(kind: .pgp, status: .unknownKey), messageId: "m_3", arrivedAt: arrived,
            pgp: PGPDetails(keyTrust: .unknown, keyId: "89abcdef01234567", importable: PGPKeyOffer(fingerprint: fp40, partId: "3")))
        got = SignatureView.details(unknown, from: "", busy: .importKey, date: day)
        #expect(keys(got.rows) == [.kind, .arrivedAt, .keyTrust, .keyID] && got.rows[2].value == "Not in Malachi Mail")
        #expect(got.buttons == [SignatureView.Button(action: .importKey, label: "Import Key", enabled: false)] && got.caveat == "")
        #expect(SignatureView.importParams(unknown) == PGPKeyImportParams(messageId: "m_3", partId: "3"))

        // A confirmed key, a revoked one, an offer naming both sources.
        d.pgp?.keyTrust = .verified
        #expect(SignatureView.confirmParams(d) == nil)
        d.pgp?.keyTrust = .seen
        d.summary.status = .revoked
        #expect(SignatureView.confirmParams(d) == nil)
        d.pgp?.importable = PGPKeyOffer(fingerprint: "", partId: "3", autocrypt: true)
        #expect(SignatureView.importParams(d) == nil)
        d.revokedAt = hours(-1)
        let last = keys(SignatureView.details(d, from: "", busy: .none, date: day).rows).last
        #expect(last == .revoked)
        #expect(SignatureView.details(nil, from: "", busy: .none, date: day) == SignatureView.Details())
    }

    @Test func errors() {
        // Over the wire `data` is a decoded JSON object.
        let wire = RPCError(code: .revocationFailed, message: "x", data: .object(["reason": .string("timeout")]))
        #expect(SignatureView.revocationFailure(of: wire) == .timeout)
        #expect(SignatureView.importFailure(of: RPCError(code: .invalidArgument, message: "x", data: .object(["reason": .string("secretKey")]))) == .secretKey)
        #expect(SignatureView.importFailure(of: RPCError(code: .messageNotFound, message: "x", data: .object(["reason": .string("secretKey")]))) == "")
        for e in [nil, RPCError(code: .offline, message: "x"), RPCError(code: .revocationFailed, message: "x"), RPCError(code: .revocationFailed, message: "x", data: .string("x"))] as [RPCError?] {
            #expect(SignatureView.revocationFailure(of: e) == "")
        }
        for r: RevocationFailure in ["", .network, .timeout, .server, .response, .address] {
            #expect(!SignatureView.revocationFailedText(r).isEmpty, "\(r)")
        }
        for r: PGPImportFailure in ["", .noKey, .malformed, .secretKey, .tooLarge, .tooMany] {
            #expect(!SignatureView.importFailedText(r).isEmpty, "\(r)")
        }
    }

    @Test func importParamsAndText() {
        let armor = SignatureView.armorHeader
        #expect(SignatureView.pastedParams("  \n") == nil)
        #expect(SignatureView.pastedParams("\n" + armor + "\nabc\n")?.armored == armor + "\nabc")
        #expect(SignatureView.pastedParams(String(repeating: "a", count: API.Limits.maxPGPKeyBytes + 1)) == nil)
        let file = SignatureView.fileParams(Data((armor + "\n").utf8))
        #expect(file?.armored != nil && file?.data == nil)
        let binary = SignatureView.fileParams(Data([0x99, 0x01]))
        #expect(binary?.armored == nil && binary?.data?.count == 2)
        #expect(SignatureView.fileParams(Data()) == nil)
        #expect(SignatureView.fileParams(Data(count: API.Limits.maxPGPKeyBytes + 1)) == nil)
        let cases: [(PGPKeyImportResult, String)] = [
            (PGPKeyImportResult(added: 1), "1 key imported"),
            (PGPKeyImportResult(added: 3, updated: 1), "3 keys imported"),
            (PGPKeyImportResult(updated: 1), "1 key updated"),
            (PGPKeyImportResult(updated: 2), "2 keys updated"),
            (PGPKeyImportResult(), "The key was already imported."),
        ]
        for (r, want) in cases {
            #expect(SignatureView.importedText(r) == want)
        }
    }

    @Test func keyRows() {
        let now = arrived
        let past = hours(-1), future = hours(1)
        let list = [
            PGPKey(fingerprint: fp64, userIds: ["zoe <zoe@example.org>"], trust: .verified, source: .import, firstSeen: now),
            PGPKey(fingerprint: fp40, emails: ["Adam@example.org"], trust: .seen, source: .autocrypt, firstSeen: now, expiresAt: past),
            PGPKey(
                fingerprint: String(repeating: "ab", count: 20), trust: .seen, source: .attached, firstSeen: now,
                expiresAt: future, revokedAt: past),
        ]
        let rows = SignatureView.keyRows(list, now: now, date: day)
        #expect(rows.map(\.title) == ["ABAB ABAB ABAB ABAB ABAB ABAB ABAB ABAB ABAB ABAB", "Adam@example.org", "zoe <zoe@example.org>"])
        let revoked = rows[0], adam = rows[1], zoe = rows[2]
        #expect(zoe.trustPill == "Confirmed" && zoe.verified && zoe.statePill == "" && zoe.origin == "Imported on 2026-11-02")
        #expect(zoe.actions.map(\.action) == [.unconfirmKey, .removeKey])
        #expect(adam.trustPill == "Not confirmed" && adam.statePill == "Expired")
        #expect(adam.origin == "Found in a message header on 2026-11-02")
        #expect(adam.actions[0].action == .confirmKey && adam.subtitle == SignatureView.formatFingerprint(fp40) && adam.fingerprint == fp40)
        #expect(revoked.statePill == "Revoked" && revoked.origin == "Imported from a message on 2026-11-02")
        #expect(revoked.actions.map(\.action) == [.removeKey])
    }

    @Test func questionsAndPage() {
        let c = SignatureView.confirmKeyQuestion()
        #expect(c.accept == "_Confirm Key" && c.cancel == "_Cancel" && !c.destructive)
        let r = SignatureView.removeKeyQuestion()
        #expect(r.accept == "_Remove" && r.destructive)
        let p = SignatureView.securityPageTexts()
        #expect(p.title == "Security" && p.group == "OpenPGP Keys" && p.importKey == "Import Key…")
        let d = SignatureView.importDialogTexts()
        #expect(d.import == "_Import" && d.chooseFile == "Choose File…")
        #expect(SignatureView.dialogTitle == "Signature Details" && SignatureView.closeLabel == "Close")
    }

    @Test func keyActionFailures() {
        #expect(SignatureView.keyVerifyFailedText(verified: true) == "The key could not be confirmed.")
        #expect(SignatureView.keyVerifyFailedText(verified: false) == "The confirmation could not be removed.")
        #expect(SignatureView.keyRemoveFailedText == "The key could not be removed.")
    }
}
