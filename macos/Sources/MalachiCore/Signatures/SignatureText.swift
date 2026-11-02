// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// ui/internal/signature text.go: every text of the signatures, with the
// GTK msgids as keys (none is new on macOS). "S/MIME", "OpenPGP" and
// "Autocrypt" are names and stay as they are in every language; a signer's
// name, address, fingerprint and a date are inserted with the format
// directive of the msgid, never concatenated.

extension SignatureView {
    /// text.go `badgeText`: the sentence of the header's badge; `signer` is
    /// `signerLabel` of the summary ("" when it has none).
    static func badgeText(_ s: SignatureSummary, _ signer: String) -> String {
        switch s.status {
        case .valid, .validSeen:
            if signer.isEmpty {
                return L10n.T("Valid signature")
            }
            // TRANSLATORS: the header's badge of a signed message; %s is the
            // signer's name and address from the certificate or key.
            return L10n.T("Signed by %s", signer)
        case .mismatch:
            if signer.isEmpty {
                return L10n.T("Signed by someone else")
            }
            // TRANSLATORS: the signature is valid but belongs to another address
            // than the sender's; %s is the signer's name and address.
            return L10n.T("Signed by someone else: %s", signer)
        case .untrusted:
            if signer.isEmpty {
                return L10n.T("Untrusted signature")
            }
            if s.kind == .pgp {
                return L10n.T("Signed by %s, but the key had expired", signer)
            }
            return L10n.T("Signed by %s, but the certificate is not trusted", signer)
        case .weak:
            if signer.isEmpty {
                return L10n.T("Weak signature")
            }
            return L10n.T("Signed by %s with a weak algorithm", signer)
        case .revoked:
            if s.kind == .pgp {
                return L10n.T("Signed with a revoked key")
            }
            return L10n.T("Signed with a revoked certificate")
        case .invalid:
            return L10n.T("Invalid signature")
        case .unknownKey:
            return L10n.T("Signed with an unknown OpenPGP key")
        case .unsupported:
            if hasNote(s.notes, .inlinePGP) {
                return L10n.T("Inline OpenPGP signature, not verified")
            }
            return L10n.T("Signature not verified")
        default:
            return L10n.T("Signature could not be verified")
        }
    }

    /// signature.StatusTitle: the short name of a verdict, the details
    /// heading and the tooltip of the list's icon. An unknown status reads
    /// as `.error`.
    public static func statusTitle(_ kind: SignatureKind, _ status: SignatureStatus) -> String {
        switch status {
        case .valid:
            return L10n.T("Valid signature")
        case .validSeen:
            // TRANSLATORS: an OpenPGP signature that matches, made with a key
            // found automatically that the user has not confirmed yet.
            return L10n.T("Valid signature, key not confirmed")
        case .mismatch:
            return L10n.T("Signed by someone else")
        case .untrusted:
            return L10n.T("Untrusted signature")
        case .weak:
            return L10n.T("Weak signature")
        case .invalid:
            return L10n.T("Invalid signature")
        case .revoked:
            if kind == .pgp {
                return L10n.T("Revoked key")
            }
            return L10n.T("Revoked certificate")
        case .unknownKey:
            return L10n.T("Unknown key")
        case .unsupported:
            return L10n.T("Signature not verified")
        default:
            return L10n.T("Signature could not be verified")
        }
    }

    /// text.go `explanation`: the paragraph under the details heading;
    /// `from` is the message's From address ("" unknown).
    static func explanation(_ s: SignatureSummary, from: String) -> String {
        switch s.status {
        case .valid:
            if s.kind == .pgp {
                return L10n.T("The signature matches the message, and you confirmed the key.")
            }
            return L10n.T("The signature matches the message, and the certificate was issued by an authority this computer trusts.")
        case .validSeen:
            return L10n.T("The signature matches the message. The key was found automatically: compare its fingerprint with the sender to confirm it.")
        case .mismatch:
            if let email = s.signer?.email, !email.isEmpty, !from.isEmpty {
                // TRANSLATORS: the first %s is the address of the certificate or
                // key, the second the sender's address of the message.
                return L10n.T("The signature is valid, but it belongs to %s, not to the sender %s.", clean(email), clean(from))
            }
            return L10n.T("The signature is valid, but it does not belong to the sender's address.")
        case .untrusted:
            if s.kind == .pgp {
                return L10n.T("The signature matches the message, but the key had expired when the message arrived.")
            }
            return L10n.T("The signature matches the message, but the certificate cannot be trusted.")
        case .weak:
            return L10n.T("The signature matches the message, but it uses an algorithm that is no longer considered safe.")
        case .invalid:
            return L10n.T("The message may have been changed after it was signed. Do not trust its content.")
        case .revoked:
            if s.kind == .pgp {
                return L10n.T("The key was revoked before the message arrived. Do not trust its content.")
            }
            return L10n.T("The certificate was revoked before the message arrived. Do not trust its content.")
        case .unknownKey:
            return L10n.T("The message is signed with an OpenPGP key that Malachi Mail does not have. Import the sender's key to verify it.")
        case .unsupported:
            return L10n.T("Malachi Mail cannot verify this kind of signature.")
        default:
            return L10n.T("Malachi Mail could not verify the signature.")
        }
    }

    /// text.go `caveat`: the line under a valid verdict's details.
    static var caveat: String {
        L10n.T("A valid signature tells who sent the message, not that its content is true or safe.")
    }

    /// signature.NoteText: the sentence of a note ("" for a note this
    /// client does not know, which is not shown).
    public static func noteText(_ kind: SignatureKind, _ n: SignatureNote) -> String {
        let pgp = kind == .pgp
        switch n {
        case .expiredSince:
            if pgp {
                return L10n.T("The key has expired since the message arrived.")
            }
            return L10n.T("The certificate has expired since the message arrived.")
        case .revocationUnchecked:
            return L10n.T("Whether the certificate was revoked has not been checked.")
        case .revocationGood:
            return L10n.T("The certificate was not revoked when last checked.")
        case .revokedAfter:
            if pgp {
                return L10n.T("The key was revoked after the message arrived.")
            }
            return L10n.T("The certificate was revoked after the message arrived.")
        case .partialFooter:
            return L10n.T("A footer added after signing, such as a mailing list's, is not covered by the signature.")
        case .protectedHeadersDiffer:
            return L10n.T("The signed headers differ from the ones shown.")
        case .inlinePGP:
            return L10n.T("Inline OpenPGP signatures are not verified: they do not cover attachments or formatting.")
        case .keyChanged:
            return L10n.T("This sender used a different key before.")
        case .opaque:
            return L10n.T("The signed content was unpacked from the signature.")
        default:
            return ""
        }
    }

    /// signature.ReasonText: the sentence of a reason ("" for none,
    /// internal and an unknown reason: the explanation says enough).
    public static func reasonText(_ kind: SignatureKind, _ r: SignatureReason?) -> String {
        guard let r else { return "" }
        switch r {
        case .digestMismatch:
            return L10n.T("The message was changed after it was signed.")
        case .badSignature:
            return L10n.T("The signature does not match the signer's certificate or key.")
        case .malformed:
            return L10n.T("The signature is damaged.")
        case .md5:
            return L10n.T("The signature uses MD5, which is broken.")
        case .keyUsage:
            return L10n.T("The certificate is not meant for signing e-mail.")
        case .unknownIssuer:
            return L10n.T("The certificate was not issued by an authority this computer trusts.")
        case .selfSigned:
            return L10n.T("The certificate is self-signed.")
        case .notYetValid:
            return L10n.T("The certificate was not valid yet when the message arrived.")
        case .expired:
            if kind == .pgp {
                return L10n.T("The key had expired when the message arrived.")
            }
            return L10n.T("The certificate had expired when the message arrived.")
        case .sha1:
            return L10n.T("The signature uses SHA-1, which is no longer considered safe.")
        case .shortKey:
            return L10n.T("The signing key is shorter than 2048 bits.")
        case .algorithm:
            return L10n.T("The signature uses an algorithm Malachi Mail does not support.")
        case .multipleSigners:
            return L10n.T("The message has more than one signer, which Malachi Mail does not support.")
        case .rebuilt:
            return L10n.T("The mail server changed the message, so its signature cannot be checked.")
        case .format:
            return L10n.T("Malachi Mail does not support this kind of signed message.")
        case .limit:
            return L10n.T("The signature is too large or too complex to check.")
        default:
            return ""
        }
    }

    /// text.go `rowLabel`: the label of a row of the details.
    static func rowLabel(_ k: RowKey) -> String {
        switch k {
        case .signer:
            // TRANSLATORS: label of the details row with the signer's name and address
            return L10n.C("signature", "Signed by")
        case .kind:
            // TRANSLATORS: label of the row that says S/MIME or OpenPGP
            return L10n.C("signature", "Signature type")
        case .reason:
            // TRANSLATORS: label of the row that says why the signature is not valid
            return L10n.C("signature", "Problem")
        case .signedAt:
            // TRANSLATORS: label of the signing time; the signer writes it, so
            // it is only a claim
            return L10n.C("signature", "Signed, as stated by the signer")
        case .arrivedAt:
            // TRANSLATORS: label of the time the message arrived
            return L10n.C("signature", "Arrived")
        case .algorithm:
            return L10n.C("signature", "Algorithm")
        case .certFingerprint:
            return L10n.C("certificate", "SHA-256 fingerprint")
        case .keyFingerprint:
            return L10n.C("signature", "Key fingerprint")
        case .keyID:
            return L10n.C("signature", "Key ID")
        case .keyTrust:
            // TRANSLATORS: label of the row that says whether the OpenPGP key is confirmed
            return L10n.C("signature", "Key")
        case .keySource:
            return L10n.C("signature", "Key source")
        case .firstSeen:
            // TRANSLATORS: label of the date the OpenPGP key was first seen
            return L10n.C("signature", "First seen")
        case .userIDs:
            return L10n.C("signature", "User IDs")
        case .previousKey:
            // TRANSLATORS: label of the fingerprint of the key the sender used before
            return L10n.C("signature", "Previous key")
        case .expired:
            return L10n.C("signature", "Expired")
        case .revoked:
            return L10n.C("signature", "Revoked")
        case .revocation:
            // TRANSLATORS: label of the row with the result of the revocation check
            return L10n.C("signature", "Revocation")
        case .issuedTo:
            return L10n.C("certificate", "Issued to")
        case .issuedBy:
            return L10n.C("certificate", "Issued by")
        case .validity:
            return L10n.C("certificate", "Valid")
        case .emails:
            // TRANSLATORS: label of the e-mail addresses a certificate names
            return L10n.C("signature", "E-mail addresses")
        case .signedHeader:
            return ""
        }
    }

    /// text.go `validity`: a certificate's validity period.
    static func validity(_ from: String, _ to: String) -> String {
        L10n.format(L10n.C("certificate", "%s to %s"), [from, to])
    }

    /// text.go `keyTrustText`: the value of the key row.
    static func keyTrustText(_ t: PGPKeyTrust) -> String {
        switch t {
        case .verified:
            return L10n.T("Confirmed by you")
        case .seen:
            return L10n.T("Found automatically, not confirmed")
        default:
            return L10n.T("Not in Malachi Mail")
        }
    }

    /// text.go `keySourceText`: the value of the key source row ("" unknown).
    static func keySourceText(_ s: PGPKeySource?) -> String {
        guard let s else { return "" }
        switch s {
        case .autocrypt:
            // TRANSLATORS: Autocrypt is a name; the key came in a message's header
            return L10n.T("Autocrypt header of a message")
        case .attached:
            return L10n.T("Attached to a message")
        case .import:
            return L10n.T("Imported by you")
        default:
            return ""
        }
    }

    /// text.go `revocationText`: the value of the revocation row.
    static func revocationText(_ r: SMIMERevocation, date: (Date) -> String) -> String {
        switch r.state {
        case .good:
            if let at = r.checkedAt {
                // TRANSLATORS: %s is the date and time of the check
                return L10n.T("Not revoked (checked %s)", date(at))
            }
        case .revoked:
            if let at = r.revokedAt {
                // TRANSLATORS: %s is the date the certificate was revoked
                return L10n.T("Revoked on %s", date(at))
            }
            return L10n.C("revocation", "Revoked")
        case .unknown:
            if let at = r.checkedAt {
                // TRANSLATORS: the issuer does not know the certificate; %s is the
                // date and time of the check
                return L10n.T("Unknown to the issuer (checked %s)", date(at))
            }
        default:
            break
        }
        return L10n.T("Not checked")
    }

    /// text.go `sectionTitle`: the heading of a section of the details.
    static func sectionTitle(_ k: SectionKey) -> String {
        switch k {
        case .signerCertificate: return L10n.T("Signer's Certificate")
        case .intermediate: return L10n.T("Intermediate Certificate")
        case .root: return L10n.T("Root Certificate")
        case .signedHeaders: return L10n.T("Signed Headers")
        }
    }

    /// text.go `headerName`: the label of a signed header ("" unknown).
    static func headerName(_ n: ProtectedHeaderName) -> String {
        switch n {
        case .subject: return L10n.C("signed header", "Subject")
        case .from: return L10n.C("signed header", "From")
        case .to: return L10n.C("signed header", "To")
        case .cc: return L10n.C("signed header", "Cc")
        case .replyTo: return L10n.C("signed header", "Reply-To")
        case .date: return L10n.C("signed header", "Date")
        default: return ""
        }
    }

    /// signature.DialogTitle: the title of the details popover.
    public static var dialogTitle: String { L10n.T("Signature Details") }

    /// signature.BadgeTooltip: the tooltip of the header's badge, which opens
    /// the details.
    public static var badgeTooltip: String { L10n.T("Show Signature Details") }

    /// signature.CloseLabel: the label of the dialog's close button.
    public static var closeLabel: String { L10n.T("Close") }

    /// text.go `buttonLabel`: the label of a details button; `busy` while it
    /// runs.
    static func buttonLabel(_ a: Action, again: Bool, busy: Bool) -> String {
        switch a {
        case .checkRevocation:
            if busy {
                // TRANSLATORS: the button while the revocation check runs
                return L10n.T("Checking…")
            }
            if again {
                return L10n.T("Check Revocation Again")
            }
            // TRANSLATORS: button: ask the certificate's issuer whether the
            // certificate was revoked
            return L10n.T("Check Revocation")
        case .importKey:
            return L10n.T("Import Key")
        case .confirmKey:
            return L10n.T("Confirm Key…")
        case .unconfirmKey:
            // TRANSLATORS: menu item: the key is no longer marked as confirmed
            return L10n.T("Remove Confirmation")
        case .removeKey:
            return L10n.T("Remove Key…")
        case .none:
            return ""
        }
    }

    /// signature.RevocationCaption: the line under Check Revocation.
    static var revocationCaption: String {
        L10n.T("Checking tells the certificate's issuer that the certificate is in use.")
    }

    /// signature.ConfirmKeyQuestion: the question of Confirm Key… (the
    /// popover's sheet shows the formatted fingerprint under the body, in
    /// monospace).
    public static func confirmKeyQuestion() -> Question {
        Question(
            heading: L10n.T("Confirm This Key?"),
            body: L10n.T("Compare this fingerprint with the one the sender gives you in person or in another way you trust."),
            cancel: L10n.T("_Cancel"),
            // TRANSLATORS: the button that marks the OpenPGP key as confirmed
            accept: L10n.T("_Confirm Key"))
    }

    /// signature.RemoveKeyQuestion: the question of Remove Key….
    public static func removeKeyQuestion() -> Question {
        Question(
            heading: L10n.T("Remove This Key?"),
            body: L10n.T("Messages signed with it will show an unknown key until it is imported again."),
            cancel: L10n.T("_Cancel"), accept: L10n.T("_Remove"), destructive: true)
    }

    /// The toasts after pgp.keyVerify and pgp.keyRemove.
    public static var keyConfirmedText: String { L10n.T("Key confirmed") }
    public static var confirmationRemovedText: String { L10n.T("Confirmation removed") }
    public static var keyRemovedText: String { L10n.T("Key removed") }

    /// signature.RevocationFailedText: the error of
    /// message.signatureRevocation, by the class in its data ("" or another
    /// error: the general sentence).
    public static func revocationFailedText(_ reason: RevocationFailure) -> String {
        switch reason {
        case .network:
            return L10n.T("Could not reach the certificate's issuer.")
        case .timeout:
            return L10n.T("The certificate's issuer did not answer in time.")
        case .server:
            return L10n.T("The certificate's issuer could not answer.")
        case .response:
            return L10n.T("The issuer's answer could not be verified.")
        case .address:
            return L10n.T("The certificate names an address Malachi Mail does not contact.")
        default:
            return L10n.T("Could not check whether the certificate was revoked.")
        }
    }

    /// signature.ImportFailedText: the error of pgp.keyImport, by the class
    /// in its data ("" or another error: the general sentence).
    public static func importFailedText(_ reason: PGPImportFailure) -> String {
        switch reason {
        case .noKey:
            return L10n.T("This is not an OpenPGP public key.")
        case .malformed:
            return L10n.T("The key cannot be read.")
        case .secretKey:
            return L10n.T("This is a secret key. Import only the public key: Malachi Mail never stores secret keys.")
        case .tooLarge:
            return L10n.T("The key data is too large.")
        case .tooMany:
            return L10n.T("Malachi Mail cannot store more keys. Remove keys you no longer need.")
        default:
            return L10n.T("The key could not be imported.")
        }
    }

    /// signature.ImportedText: the toast after pgp.keyImport.
    public static func importedText(_ r: PGPKeyImportResult) -> String {
        if r.added > 0 {
            return L10n.N("%d key imported", "%d keys imported", r.added)
        }
        if r.updated > 0 {
            return L10n.N("%d key updated", "%d keys updated", r.updated)
        }
        return L10n.T("The key was already imported.")
    }

    /// signature.ImportDialog: the texts of the Import OpenPGP Key sheet: a
    /// text area for a pasted key (its placeholder is `armorHeader`,
    /// not translated) and a file chooser.
    public struct ImportDialog: Sendable, Equatable {
        public var title: String
        public var body: String
        public var chooseFile: String
        public var fileFilter: String
        public var cancel: String
        public var `import`: String
    }

    public static func importDialogTexts() -> ImportDialog {
        ImportDialog(
            title: L10n.T("Import OpenPGP Key"),
            body: L10n.T("Paste a public key, or choose a file with one."),
            chooseFile: L10n.T("Choose File…"),
            // TRANSLATORS: name of the file type filter of the file chooser
            fileFilter: L10n.T("OpenPGP keys"),
            cancel: L10n.T("_Cancel"), import: L10n.T("_Import"))
    }

    /// signature.SecurityPage: the texts of Settings → Security.
    public struct SecurityPage: Sendable, Equatable {
        /// The pane's title (and its toolbar item); `group` the heading of
        /// the key list with its `description`; `empty` the list's
        /// placeholder; `importKey` the button that opens the import
        /// sheet; `loadFailed` the line when pgp.keyList failed.
        public var title: String
        public var group: String
        public var description: String
        public var empty: String
        public var importKey: String
        public var loadFailed: String
    }

    public static func securityPageTexts() -> SecurityPage {
        SecurityPage(
            // TRANSLATORS: the Preferences page with the OpenPGP keys
            title: L10n.C("preferences page", "Security"),
            group: L10n.T("OpenPGP Keys"),
            description: L10n.T("Keys of people whose signed mail you receive. Malachi Mail finds them in your mail and never looks them up online."),
            empty: L10n.T("No OpenPGP Keys"),
            importKey: L10n.T("Import Key…"),
            loadFailed: L10n.T("Could not load the keys."))
    }

    /// text.go `keyTrustPill`: the pill of a key row's trust.
    static func keyTrustPill(_ t: PGPKeyTrust) -> String {
        if t == .verified {
            return L10n.C("key trust", "Confirmed")
        }
        return L10n.C("key trust", "Not confirmed")
    }

    /// text.go `keyStatePill`: the pill of a key that cannot verify
    /// anything new ("" for a usable key).
    static func keyStatePill(revoked: Bool, expired: Bool) -> String {
        if revoked {
            return L10n.C("key state", "Revoked")
        }
        if expired {
            return L10n.C("key state", "Expired")
        }
        return ""
    }

    /// text.go `keyOrigin`: the line of a key row that says where it came
    /// from and when; `when` is the first-seen date.
    static func keyOrigin(_ s: PGPKeySource, _ when: String) -> String {
        switch s {
        case .autocrypt:
            // TRANSLATORS: %s is a date
            return L10n.T("Found in a message header on %s", when)
        case .attached:
            // TRANSLATORS: %s is a date
            return L10n.T("Imported from a message on %s", when)
        default:
            // TRANSLATORS: %s is a date
            return L10n.T("Imported on %s", when)
        }
    }
}
