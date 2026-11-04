// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

// DEVELOPMENT AID, not a feature (MALACHI_START, see
// MainWindowController+DevStart.swift): the signature steps, which need no
// daemon (the details are invented, `SignatureController.seed`).
//
//   `signature-popover` opens the details popover of a valid S/MIME
//   signature (certificate chain, Check Revocation) under the main window's
//   top, dumps its frame and texts. `signature-popover:pgp` does the same
//   for an OpenPGP signature made with a key found automatically (Import
//   Key from an attachment, Confirm Key…). `signature-popover:bad` an
//   invalid one. `prefs-security` opens Settings → Security and dumps its
//   rows (empty without a daemon).
// Each prints `LAYOUT:` lines and `LAYOUT: shot <step> window <n>`.
@MainActor
enum DevelopmentSignature {
    static func step(_ step: String, in main: MainWindowController) -> Bool {
        switch step {
        case "signature-popover": popover(main, sample(.smime))
        case "signature-popover:pgp": popover(main, sample(.pgp))
        case "signature-popover:bad": popover(main, sample(.bad))
        case "prefs-security": prefs(main)
        default: return false
        }
        return true
    }

    private static func say(_ line: String) {
        FileHandle.standardError.write(Data((line + "\n").utf8))
    }

    private static func r(_ v: NSView) -> String {
        let f = v.convert(v.bounds, to: nil)
        return String(format: "(%.0f,%.0f %.0fx%.0f)", f.minX, f.minY, f.width, f.height)
    }

    private static func all<T: NSView>(_ type: T.Type, under v: NSView) -> [T] {
        var found: [T] = []
        func walk(_ v: NSView) {
            if let t = v as? T {
                found.append(t)
            }
            v.subviews.forEach(walk)
        }
        walk(v)
        return found
    }

    private enum Sample { case smime, pgp, bad }

    private static let fp64 = String(repeating: "ab12cd34", count: 8)
    private static let fp40 = "0123456789abcdef0123456789abcdef01234567"

    private static func sample(_ s: Sample) -> SignatureDetails {
        let arrived = Date(timeIntervalSince1970: 1_790_769_600)
        let later = arrived.addingTimeInterval(86400 * 300)
        switch s {
        case .smime:
            let jan = SignatureSigner(name: "Jan Sample", email: "jan@example.org", fingerprint: fp64)
            return SignatureDetails(
                summary: SignatureSummary(kind: .smime, status: .valid, signer: jan, notes: [.revocationUnchecked], partId: "2"),
                messageId: "m_sample", signedAt: arrived.addingTimeInterval(-3600), arrivedAt: arrived,
                algorithm: "RSA 2048 / SHA-256",
                smime: SMIMEDetails(
                    chain: [
                        SignatureCertificate(
                            sha256: fp64, subject: "CN=Jan Sample", issuer: "CN=Example CA 2", emails: ["jan@example.org"],
                            notBefore: arrived, notAfter: later),
                        SignatureCertificate(
                            sha256: fp64, subject: "CN=Example CA 2", issuer: "CN=Example Root", notBefore: arrived, notAfter: later),
                        SignatureCertificate(
                            sha256: fp64, subject: "CN=Example Root", issuer: "CN=Example Root", notBefore: arrived,
                            notAfter: later, systemRoot: true, selfSigned: true),
                    ],
                    revocation: SMIMERevocation(state: .unchecked, checkable: true)))
        case .pgp:
            let jan = SignatureSigner(name: "Jan Sample", email: "jan@example.org", fingerprint: fp40)
            return SignatureDetails(
                summary: SignatureSummary(kind: .pgp, status: .validSeen, signer: jan, notes: [.keyChanged], partId: "2"),
                messageId: "m_sample", signedAt: arrived.addingTimeInterval(-3600), arrivedAt: arrived,
                algorithm: "Ed25519 / SHA-512",
                pgp: PGPDetails(
                    keyTrust: .seen, keyId: "89abcdef01234567", primaryFingerprint: fp40,
                    userIds: ["Jan Sample <jan@example.org>"], keySource: .autocrypt, firstSeen: arrived.addingTimeInterval(-86400 * 2),
                    previousFingerprint: String(repeating: "ef", count: 20),
                    importable: PGPKeyOffer(fingerprint: fp40, partId: "3")))
        case .bad:
            return SignatureDetails(
                summary: SignatureSummary(
                    kind: .smime, status: .invalid, signer: SignatureSigner(name: "Jan Sample", email: "jan@example.org")),
                messageId: "m_sample", reason: .digestMismatch, arrivedAt: arrived, algorithm: "RSA 2048 / SHA-256")
        }
    }

    private static func popover(_ main: MainWindowController, _ d: SignatureDetails) {
        guard let w = main.window, let content = w.contentView else { return }
        SignatureDetailsPopover.close()
        let s = MessageSummary(
            id: d.messageId, accountId: "a1", folderId: "f1", from: [Address(name: "Jan Sample", address: "jan@example.org")],
            subject: "Sample", date: d.arrivedAt, snippet: "", flags: [], hasAttachments: false, size: 0)
        let anchor = NSRect(x: content.bounds.midX, y: content.bounds.maxY - 80, width: 1, height: 1)
        let probe = NSView(frame: anchor)
        content.addSubview(probe)
        SignatureDetailsPopover.show(s, summary: d.summary, relativeTo: probe, state: main.state, window: w, seed: d)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.8) {
            MainActor.assumeIsolated {
                if let v = SignatureDetailsPopover.shownView {
                    say("LAYOUT: signature-popover \(d.kind.rawValue) frame \(r(v)) (width 380, at most 520 high)")
                    for t in all(NSTextField.self, under: v) where !t.stringValue.isEmpty {
                        say("LAYOUT: signature-popover text \(r(t)) '\(t.stringValue)'")
                    }
                    for b in all(NSButton.self, under: v) {
                        say("LAYOUT: signature-popover button \(r(b)) '\(b.title)' enabled \(b.isEnabled)")
                    }
                } else {
                    say("LAYOUT: signature-popover not shown")
                }
                probe.removeFromSuperview()
                say("LAYOUT: shot signature-popover window \(w.windowNumber)")
            }
        }
    }

    private static func prefs(_ main: MainWindowController) {
        let prefs = Integration.showPreferences(main.state)
        prefs.select(.security)
        guard let w = prefs.window else {
            say("LAYOUT: prefs-security no Settings window")
            return
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.0) {
            MainActor.assumeIsolated {
                if let v = prefs.securityPane.view as NSView? {
                    for t in all(NSTextField.self, under: v) where !t.stringValue.isEmpty {
                        say("LAYOUT: prefs-security text \(r(t)) '\(t.stringValue)'")
                    }
                }
                say("LAYOUT: shot prefs-security window \(w.windowNumber)")
            }
        }
    }
}
