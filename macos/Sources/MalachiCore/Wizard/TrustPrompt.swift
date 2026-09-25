// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The "Trust This Certificate?" confirmation of the account wizard
// (accountwizard trust.go): the texts and the certificate's details, as
// plain strings the alert shows as selectable, never interpreted text.

import Foundation

/// One line of the certificate's details: a label and its value. Every
/// value is untrusted text from the server (cleaned by `CertTrust.details`).
public struct CertificateDetail: Sendable, Equatable {
    public var label: String
    /// "" for a line that is only a label ("Self-signed").
    public var value: String
    /// The fingerprint, compared by eye: monospaced.
    public var monospaced: Bool

    public init(label: String, value: String, monospaced: Bool = false) {
        self.label = label
        self.value = value
        self.monospaced = monospaced
    }
}

/// The confirmation before a certificate is pinned: a destructive dialog
/// with the details between the body and the buttons.
public struct TrustPrompt: Sendable, Equatable {
    public var heading: String
    public var body: String
    public var details: [CertificateDetail]
    /// The destructive button's label, with its mnemonic marker.
    public var confirmLabel: String

    public init(heading: String, body: String, details: [CertificateDetail], confirmLabel: String) {
        self.heading = heading
        self.body = body
        self.details = details
        self.confirmLabel = confirmLabel
    }
}

/// The confirmation for pinning `cert` to `servers` (`CertTrust.serverName`,
/// one, or two with IMAP first): the servers are named in the body, so the
/// user sees what exactly will accept this certificate and nothing else.
/// `changed` (a target's certificate is not the one it pins,
/// `CertTrust.Category.changed`) asks about the new certificate instead,
/// with a warning and the fingerprint trusted before (`previous`, 64 hex
/// digits, "" when unknown).
public func trustPrompt(
    _ cert: CertificateInfo, servers: [String], changed: Bool = false, previous: String = "", now: Date = Date()
) -> TrustPrompt {
    let named: String
    if servers.count >= 2 {
        // TRANSLATORS: joins two servers ("host:port")
        named = L10n.T("%s and %s", servers[0], servers[1])
    } else {
        named = servers.first ?? ""
    }
    if changed {
        return TrustPrompt(
            // TRANSLATORS: dialog heading, the pinned certificate of a server has changed
            heading: L10n.T("Trust the New Certificate?"),
            // TRANSLATORS: %s is one server ("host:port") or two joined by "%s and %s"
            body: L10n.T(
                "The certificate of %s has changed since you trusted it. If you did not renew it yourself, someone may be intercepting the connection. Trust the new certificate only if you know why it changed.",
                named),
            details: certificateDetails(cert, previous: previous, now: now),
            confirmLabel: L10n.T("_Trust")
        )
    }
    return TrustPrompt(
        // TRANSLATORS: dialog heading
        heading: L10n.T("Trust This Certificate?"),
        // TRANSLATORS: %s is one server ("host:port") or two joined by "%s and %s"
        body: L10n.T(
            "Only trust a certificate you expect, for example the one of a mail bridge on your own computer. Malachi Mail will then accept exactly this certificate for %s and no other, even if it is self-signed, issued for another name or expired.",
            named),
        details: certificateDetails(cert, now: now),
        // TRANSLATORS: destructive confirm button
        confirmLabel: L10n.T("_Trust")
    )
}

/// The certificate's details in the order of the dialog: the fingerprint
/// first (nothing the server writes into its certificate can pose as it),
/// the fingerprint trusted before (`previous`, for a changed certificate),
/// whom it was issued to (`CertTrust.issuedTo`), by whom, the validity
/// (left out when either date is unset), and "Self-signed" on a line of
/// its own when it is. A line whose value is empty is left out.
public func certificateDetails(_ cert: CertificateInfo, previous: String = "", now: Date = Date()) -> [CertificateDetail] {
    var out: [CertificateDetail] = []
    let fingerprint = CertTrust.formatFingerprint(cert.sha256)
    if !fingerprint.isEmpty {
        out.append(CertificateDetail(label: L10n.C("certificate", "SHA-256 fingerprint"), value: fingerprint, monospaced: true))
    }
    let before = CertTrust.formatFingerprint(previous)
    if !before.isEmpty {
        // TRANSLATORS: label of the fingerprint of the certificate that was pinned before
        out.append(CertificateDetail(label: L10n.C("certificate", "Previously trusted"), value: before, monospaced: true))
    }
    let issuedTo = CertTrust.issuedTo(cert)
    if !issuedTo.isEmpty {
        out.append(CertificateDetail(label: L10n.C("certificate", "Issued to"), value: issuedTo))
    }
    if let issuer = cert.issuer, !issuer.isEmpty {
        out.append(CertificateDetail(label: L10n.C("certificate", "Issued by"), value: issuer))
    }
    if !cert.notBefore.isGoZero, !cert.notAfter.isGoZero {
        // TRANSLATORS: validity period: two dates
        let period = L10n.format(
            L10n.C("certificate", "%s to %s"), [formatDate(cert.notBefore, now: now), formatDate(cert.notAfter, now: now)])
        // TRANSLATORS: label of the validity period
        out.append(CertificateDetail(label: L10n.C("certificate", "Valid"), value: period))
    }
    if cert.selfSigned {
        out.append(CertificateDetail(label: L10n.C("certificate", "Self-signed"), value: ""))
    }
    return out
}
