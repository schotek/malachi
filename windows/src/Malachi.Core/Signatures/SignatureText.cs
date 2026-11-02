// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/signature/text.go (badgeText, StatusTitle,
// explanation, caveat, NoteText, ReasonText, rowLabel, validity,
// keyTrustText, keySourceText, revocationText, sectionTitle, headerName,
// DialogTitle, BadgeTooltip, CloseLabel, buttonLabel, RevocationCaption,
// ConfirmKeyQuestion, RemoveKeyQuestion, KeyConfirmedText,
// ConfirmationRemovedText, KeyRemovedText, RevocationFailedText,
// ImportFailedText, ImportedText, ImportDialogTexts, SecurityPageTexts,
// keyTrustPill, keyStatePill, keyOrigin).
//
// Every text of signed mail (docs/plans/signatures.md), with the GTK msgid
// as the key. "S/MIME", "OpenPGP" and "Autocrypt" are names and stay as they
// are in every language; a signer's name, address, fingerprint and a date
// are inserted with the pattern's %s, never concatenated. Go's Translator is
// L10n, as in the other ports.

using System;
using Malachi.Core.Api;
using Malachi.Core.I18n;

namespace Malachi.Core.Signatures;

/// <summary>ui/internal/signature (text.go): the texts of signed mail.</summary>
public static class SignatureText
{
    /// <summary>
    /// signature.badgeText: the sentence of the header's badge; <paramref name="signer"/>
    /// is <see cref="SignatureView.SignerLabel"/> of the summary ("" when it has none).
    /// </summary>
    public static string BadgeText(SignatureSummary s, string signer)
    {
        ArgumentNullException.ThrowIfNull(s);
        ArgumentNullException.ThrowIfNull(signer);
        var pgp = s.Kind.Value == SignatureKind.Pgp;
        switch (s.Status.Value)
        {
            case SignatureStatus.Valid or SignatureStatus.ValidSeen:
                if (signer.Length == 0)
                {
                    return L10n.T("Valid signature");
                }
                // TRANSLATORS: the header's badge of a signed message; %s is the
                // signer's name and address from the certificate or key.
                return L10n.T("Signed by %s", signer);
            case SignatureStatus.Mismatch:
                if (signer.Length == 0)
                {
                    return L10n.T("Signed by someone else");
                }
                // TRANSLATORS: the signature is valid but belongs to another address
                // than the sender's; %s is the signer's name and address.
                return L10n.T("Signed by someone else: %s", signer);
            case SignatureStatus.Untrusted:
                if (signer.Length == 0)
                {
                    return L10n.T("Untrusted signature");
                }
                return pgp
                    ? L10n.T("Signed by %s, but the key had expired", signer)
                    : L10n.T("Signed by %s, but the certificate is not trusted", signer);
            case SignatureStatus.Weak:
                if (signer.Length == 0)
                {
                    return L10n.T("Weak signature");
                }
                return L10n.T("Signed by %s with a weak algorithm", signer);
            case SignatureStatus.Revoked:
                return pgp ? L10n.T("Signed with a revoked key") : L10n.T("Signed with a revoked certificate");
            case SignatureStatus.Invalid:
                return L10n.T("Invalid signature");
            case SignatureStatus.UnknownKey:
                return L10n.T("Signed with an unknown OpenPGP key");
            case SignatureStatus.Unsupported:
                return SignatureView.HasNote(s.Notes, SignatureNote.InlinePgp)
                    ? L10n.T("Inline OpenPGP signature, not verified")
                    : L10n.T("Signature not verified");
        }
        return L10n.T("Signature could not be verified");
    }

    /// <summary>
    /// signature.StatusTitle: the short name of a verdict: the details
    /// dialog's heading and the tooltip of the list's icon. An unknown status
    /// reads as error.
    /// </summary>
    public static string StatusTitle(SignatureKind kind, SignatureStatus status)
    {
        switch (status.Value)
        {
            case SignatureStatus.Valid:
                return L10n.T("Valid signature");
            case SignatureStatus.ValidSeen:
                // TRANSLATORS: an OpenPGP signature that matches, made with a key
                // found automatically that the user has not confirmed yet.
                return L10n.T("Valid signature, key not confirmed");
            case SignatureStatus.Mismatch:
                return L10n.T("Signed by someone else");
            case SignatureStatus.Untrusted:
                return L10n.T("Untrusted signature");
            case SignatureStatus.Weak:
                return L10n.T("Weak signature");
            case SignatureStatus.Invalid:
                return L10n.T("Invalid signature");
            case SignatureStatus.Revoked:
                return kind.Value == SignatureKind.Pgp ? L10n.T("Revoked key") : L10n.T("Revoked certificate");
            case SignatureStatus.UnknownKey:
                return L10n.T("Unknown key");
            case SignatureStatus.Unsupported:
                return L10n.T("Signature not verified");
        }
        return L10n.T("Signature could not be verified");
    }

    /// <summary>
    /// signature.explanation: the paragraph under the details dialog's
    /// heading; <paramref name="from"/> is the message's From address ("" unknown).
    /// </summary>
    public static string Explanation(SignatureSummary s, string from)
    {
        ArgumentNullException.ThrowIfNull(s);
        ArgumentNullException.ThrowIfNull(from);
        var pgp = s.Kind.Value == SignatureKind.Pgp;
        switch (s.Status.Value)
        {
            case SignatureStatus.Valid:
                return pgp
                    ? L10n.T("The signature matches the message, and you confirmed the key.")
                    : L10n.T("The signature matches the message, and the certificate was issued by an authority this computer trusts.");
            case SignatureStatus.ValidSeen:
                return L10n.T("The signature matches the message. The key was found automatically: compare its fingerprint with the sender to confirm it.");
            case SignatureStatus.Mismatch:
                if (s.Signer is { Email: { Length: > 0 } email } && from.Length > 0)
                {
                    // TRANSLATORS: the first %s is the address of the certificate or
                    // key, the second the sender's address of the message.
                    return L10n.T("The signature is valid, but it belongs to %s, not to the sender %s.", SignatureView.Clean(email), SignatureView.Clean(from));
                }
                return L10n.T("The signature is valid, but it does not belong to the sender's address.");
            case SignatureStatus.Untrusted:
                return pgp
                    ? L10n.T("The signature matches the message, but the key had expired when the message arrived.")
                    : L10n.T("The signature matches the message, but the certificate cannot be trusted.");
            case SignatureStatus.Weak:
                return L10n.T("The signature matches the message, but it uses an algorithm that is no longer considered safe.");
            case SignatureStatus.Invalid:
                return L10n.T("The message may have been changed after it was signed. Do not trust its content.");
            case SignatureStatus.Revoked:
                return pgp
                    ? L10n.T("The key was revoked before the message arrived. Do not trust its content.")
                    : L10n.T("The certificate was revoked before the message arrived. Do not trust its content.");
            case SignatureStatus.UnknownKey:
                return L10n.T("The message is signed with an OpenPGP key that Malachi Mail does not have. Import the sender's key to verify it.");
            case SignatureStatus.Unsupported:
                return L10n.T("Malachi Mail cannot verify this kind of signature.");
        }
        return L10n.T("Malachi Mail could not verify the signature.");
    }

    /// <summary>signature.caveat: the line under a valid verdict's details.</summary>
    public static string Caveat() =>
        L10n.T("A valid signature tells who sent the message, not that its content is true or safe.");

    /// <summary>signature.NoteText: the sentence of a note ("" for a note this client does not know, which is not shown).</summary>
    public static string NoteText(SignatureKind kind, SignatureNote n)
    {
        var pgp = kind.Value == SignatureKind.Pgp;
        switch (n.Value)
        {
            case SignatureNote.ExpiredSince:
                return pgp
                    ? L10n.T("The key has expired since the message arrived.")
                    : L10n.T("The certificate has expired since the message arrived.");
            case SignatureNote.RevocationUnchecked:
                return L10n.T("Whether the certificate was revoked has not been checked.");
            case SignatureNote.RevocationGood:
                return L10n.T("The certificate was not revoked when last checked.");
            case SignatureNote.RevokedAfter:
                return pgp
                    ? L10n.T("The key was revoked after the message arrived.")
                    : L10n.T("The certificate was revoked after the message arrived.");
            case SignatureNote.PartialFooter:
                return L10n.T("A footer added after signing, such as a mailing list's, is not covered by the signature.");
            case SignatureNote.ProtectedHeadersDiffer:
                return L10n.T("The signed headers differ from the ones shown.");
            case SignatureNote.InlinePgp:
                return L10n.T("Inline OpenPGP signatures are not verified: they do not cover attachments or formatting.");
            case SignatureNote.KeyChanged:
                return L10n.T("This sender used a different key before.");
            case SignatureNote.Opaque:
                return L10n.T("The signed content was unpacked from the signature.");
        }
        return "";
    }

    /// <summary>
    /// signature.ReasonText: the sentence of a reason ("" for none, internal
    /// and an unknown reason: the explanation says enough).
    /// </summary>
    public static string ReasonText(SignatureKind kind, SignatureReason? reason)
    {
        switch (reason?.Value)
        {
            case SignatureReason.DigestMismatch:
                return L10n.T("The message was changed after it was signed.");
            case SignatureReason.BadSignature:
                return L10n.T("The signature does not match the signer's certificate or key.");
            case SignatureReason.Malformed:
                return L10n.T("The signature is damaged.");
            case SignatureReason.Md5:
                return L10n.T("The signature uses MD5, which is broken.");
            case SignatureReason.KeyUsage:
                return L10n.T("The certificate is not meant for signing e-mail.");
            case SignatureReason.UnknownIssuer:
                return L10n.T("The certificate was not issued by an authority this computer trusts.");
            case SignatureReason.SelfSigned:
                return L10n.T("The certificate is self-signed.");
            case SignatureReason.NotYetValid:
                return L10n.T("The certificate was not valid yet when the message arrived.");
            case SignatureReason.Expired:
                return kind.Value == SignatureKind.Pgp
                    ? L10n.T("The key had expired when the message arrived.")
                    : L10n.T("The certificate had expired when the message arrived.");
            case SignatureReason.Sha1:
                return L10n.T("The signature uses SHA-1, which is no longer considered safe.");
            case SignatureReason.ShortKey:
                return L10n.T("The signing key is shorter than 2048 bits.");
            case SignatureReason.Algorithm:
                return L10n.T("The signature uses an algorithm Malachi Mail does not support.");
            case SignatureReason.MultipleSigners:
                return L10n.T("The message has more than one signer, which Malachi Mail does not support.");
            case SignatureReason.Rebuilt:
                return L10n.T("The mail server changed the message, so its signature cannot be checked.");
            case SignatureReason.Format:
                return L10n.T("Malachi Mail does not support this kind of signed message.");
            case SignatureReason.Limit:
                return L10n.T("The signature is too large or too complex to check.");
        }
        return "";
    }

    /// <summary>signature.rowLabel: the label of a row of the details.</summary>
    public static string RowLabel(SignatureRowKey k)
    {
        switch (k)
        {
            case SignatureRowKey.Signer:
                // TRANSLATORS: label of the details row with the signer's name and address
                return L10n.C("signature", "Signed by");
            case SignatureRowKey.Kind:
                // TRANSLATORS: label of the row that says S/MIME or OpenPGP
                return L10n.C("signature", "Signature type");
            case SignatureRowKey.Reason:
                // TRANSLATORS: label of the row that says why the signature is not valid
                return L10n.C("signature", "Problem");
            case SignatureRowKey.SignedAt:
                // TRANSLATORS: label of the signing time; the signer writes it, so
                // it is only a claim
                return L10n.C("signature", "Signed, as stated by the signer");
            case SignatureRowKey.ArrivedAt:
                // TRANSLATORS: label of the time the message arrived
                return L10n.C("signature", "Arrived");
            case SignatureRowKey.Algorithm:
                return L10n.C("signature", "Algorithm");
            case SignatureRowKey.CertFingerprint:
                return L10n.C("certificate", "SHA-256 fingerprint");
            case SignatureRowKey.KeyFingerprint:
                return L10n.C("signature", "Key fingerprint");
            case SignatureRowKey.KeyId:
                return L10n.C("signature", "Key ID");
            case SignatureRowKey.KeyTrust:
                // TRANSLATORS: label of the row that says whether the OpenPGP key is confirmed
                return L10n.C("signature", "Key");
            case SignatureRowKey.KeySource:
                return L10n.C("signature", "Key source");
            case SignatureRowKey.FirstSeen:
                // TRANSLATORS: label of the date the OpenPGP key was first seen
                return L10n.C("signature", "First seen");
            case SignatureRowKey.UserIds:
                return L10n.C("signature", "User IDs");
            case SignatureRowKey.PreviousKey:
                // TRANSLATORS: label of the fingerprint of the key the sender used before
                return L10n.C("signature", "Previous key");
            case SignatureRowKey.Expired:
                return L10n.C("signature", "Expired");
            case SignatureRowKey.Revoked:
                return L10n.C("signature", "Revoked");
            case SignatureRowKey.Revocation:
                // TRANSLATORS: label of the row with the result of the revocation check
                return L10n.C("signature", "Revocation");
            case SignatureRowKey.IssuedTo:
                return L10n.C("certificate", "Issued to");
            case SignatureRowKey.IssuedBy:
                return L10n.C("certificate", "Issued by");
            case SignatureRowKey.Validity:
                return L10n.C("certificate", "Valid");
            case SignatureRowKey.Emails:
                // TRANSLATORS: label of the e-mail addresses a certificate names
                return L10n.C("signature", "E-mail addresses");
        }
        return "";
    }

    /// <summary>signature.validity: a certificate's validity period.</summary>
    public static string Validity(string from, string to) =>
        L10n.Format(L10n.C("certificate", "%s to %s"), from, to);

    /// <summary>signature.keyTrustText: the value of the Key row.</summary>
    public static string KeyTrustText(PgpKeyTrust trust)
    {
        switch (trust.Value)
        {
            case PgpKeyTrust.Verified:
                return L10n.T("Confirmed by you");
            case PgpKeyTrust.Seen:
                return L10n.T("Found automatically, not confirmed");
        }
        return L10n.T("Not in Malachi Mail");
    }

    /// <summary>signature.keySourceText: the value of the Key source row ("" unknown).</summary>
    public static string KeySourceText(PgpKeySource? source)
    {
        switch (source?.Value)
        {
            case PgpKeySource.Autocrypt:
                // TRANSLATORS: Autocrypt is a name; the key came in a message's header
                return L10n.T("Autocrypt header of a message");
            case PgpKeySource.Attached:
                return L10n.T("Attached to a message");
            case PgpKeySource.Import:
                return L10n.T("Imported by you");
        }
        return "";
    }

    /// <summary>signature.revocationText: the value of the Revocation row.</summary>
    public static string RevocationText(SmimeRevocation r, Func<DateTimeOffset, string> date)
    {
        ArgumentNullException.ThrowIfNull(r);
        ArgumentNullException.ThrowIfNull(date);
        switch (r.State.Value)
        {
            case RevocationState.Good:
                if (r.CheckedAt is { } good)
                {
                    // TRANSLATORS: %s is the date and time of the check
                    return L10n.T("Not revoked (checked %s)", date(good));
                }
                break;
            case RevocationState.Revoked:
                if (r.RevokedAt is { } revoked)
                {
                    // TRANSLATORS: %s is the date the certificate was revoked
                    return L10n.T("Revoked on %s", date(revoked));
                }
                return L10n.C("revocation", "Revoked");
            case RevocationState.Unknown:
                if (r.CheckedAt is { } unknown)
                {
                    // TRANSLATORS: the issuer does not know the certificate; %s is the
                    // date and time of the check
                    return L10n.T("Unknown to the issuer (checked %s)", date(unknown));
                }
                break;
        }
        return L10n.T("Not checked");
    }

    /// <summary>signature.sectionTitle: the heading of a section of the details.</summary>
    public static string SectionTitle(SignatureSectionKey k)
    {
        switch (k)
        {
            case SignatureSectionKey.SignerCertificate:
                return L10n.T("Signer's Certificate");
            case SignatureSectionKey.Intermediate:
                return L10n.T("Intermediate Certificate");
            case SignatureSectionKey.Root:
                return L10n.T("Root Certificate");
            case SignatureSectionKey.SignedHeaders:
                return L10n.T("Signed Headers");
        }
        return "";
    }

    /// <summary>signature.headerName: the label of a signed header ("" unknown).</summary>
    public static string HeaderName(ProtectedHeaderName n)
    {
        switch (n.Value)
        {
            case ProtectedHeaderName.Subject:
                return L10n.C("signed header", "Subject");
            case ProtectedHeaderName.From:
                return L10n.C("signed header", "From");
            case ProtectedHeaderName.To:
                return L10n.C("signed header", "To");
            case ProtectedHeaderName.Cc:
                return L10n.C("signed header", "Cc");
            case ProtectedHeaderName.ReplyTo:
                return L10n.C("signed header", "Reply-To");
            case ProtectedHeaderName.Date:
                return L10n.C("signed header", "Date");
        }
        return "";
    }

    /// <summary>signature.DialogTitle: the title of the details dialog (flyout).</summary>
    public static string DialogTitle() => L10n.T("Signature Details");

    /// <summary>signature.BadgeTooltip: the tooltip of the header's badge, which opens the details.</summary>
    public static string BadgeTooltip() => L10n.T("Show Signature Details");

    /// <summary>signature.CloseLabel: the label of the dialog's close button (where the platform's dialog has none of its own).</summary>
    public static string CloseLabel() => L10n.T("Close");

    /// <summary>signature.buttonLabel: the label of a details button; <paramref name="busy"/> while it runs.</summary>
    public static string ButtonLabel(SignatureAction a, bool again, bool busy)
    {
        switch (a)
        {
            case SignatureAction.CheckRevocation:
                if (busy)
                {
                    // TRANSLATORS: the button while the revocation check runs
                    return L10n.T("Checking…");
                }
                if (again)
                {
                    return L10n.T("Check Revocation Again");
                }
                // TRANSLATORS: button: ask the certificate's issuer whether the
                // certificate was revoked
                return L10n.T("Check Revocation");
            case SignatureAction.ImportKey:
                return L10n.T("Import Key");
            case SignatureAction.ConfirmKey:
                return L10n.T("Confirm Key…");
            case SignatureAction.UnconfirmKey:
                // TRANSLATORS: menu item: the key is no longer marked as confirmed
                return L10n.T("Remove Confirmation");
            case SignatureAction.RemoveKey:
                return L10n.T("Remove Key…");
        }
        return "";
    }

    /// <summary>signature.RevocationCaption: the line under Check Revocation.</summary>
    public static string RevocationCaption() =>
        L10n.T("Checking tells the certificate's issuer that the certificate is in use.");

    /// <summary>
    /// signature.ConfirmKeyQuestion: the question of Confirm Key…: heading,
    /// body (the dialog shows the formatted fingerprint under it, in
    /// monospace) and the buttons.
    /// </summary>
    public static SignatureQuestion ConfirmKeyQuestion() => new(
        L10n.T("Confirm This Key?"),
        L10n.T("Compare this fingerprint with the one the sender gives you in person or in another way you trust."),
        L10n.T("_Cancel"),
        // TRANSLATORS: the button that marks the OpenPGP key as confirmed
        L10n.T("_Confirm Key"));

    /// <summary>signature.RemoveKeyQuestion: the question of Remove Key….</summary>
    public static SignatureQuestion RemoveKeyQuestion() => new(
        L10n.T("Remove This Key?"),
        L10n.T("Messages signed with it will show an unknown key until it is imported again."),
        L10n.T("_Cancel"),
        L10n.T("_Remove"),
        Destructive: true);

    /// <summary>signature.KeyConfirmedText: the toast after <c>pgp.keyVerify</c> {verified: true}.</summary>
    public static string KeyConfirmedText() => L10n.T("Key confirmed");

    /// <summary>signature.ConfirmationRemovedText: the toast after <c>pgp.keyVerify</c> {verified: false}.</summary>
    public static string ConfirmationRemovedText() => L10n.T("Confirmation removed");

    /// <summary>signature.KeyRemovedText: the toast after <c>pgp.keyRemove</c>.</summary>
    public static string KeyRemovedText() => L10n.T("Key removed");

    /// <summary>
    /// signature.RevocationFailedText: the error of <c>message.signatureRevocation</c>,
    /// by the class in its data (null or another class: the general sentence).
    /// </summary>
    public static string RevocationFailedText(RevocationFailure? reason)
    {
        switch (reason?.Value)
        {
            case RevocationFailure.Network:
                return L10n.T("Could not reach the certificate's issuer.");
            case RevocationFailure.Timeout:
                return L10n.T("The certificate's issuer did not answer in time.");
            case RevocationFailure.Server:
                return L10n.T("The certificate's issuer could not answer.");
            case RevocationFailure.Response:
                return L10n.T("The issuer's answer could not be verified.");
            case RevocationFailure.Address:
                return L10n.T("The certificate names an address Malachi Mail does not contact.");
        }
        return L10n.T("Could not check whether the certificate was revoked.");
    }

    /// <summary>
    /// signature.ImportFailedText: the error of <c>pgp.keyImport</c>, by the
    /// class in its data (null or another class: the general sentence).
    /// </summary>
    public static string ImportFailedText(PgpImportFailure? reason)
    {
        switch (reason?.Value)
        {
            case PgpImportFailure.NoKey:
                return L10n.T("This is not an OpenPGP public key.");
            case PgpImportFailure.Malformed:
                return L10n.T("The key cannot be read.");
            case PgpImportFailure.SecretKey:
                return L10n.T("This is a secret key. Import only the public key: Malachi Mail never stores secret keys.");
            case PgpImportFailure.TooLarge:
                return L10n.T("The key data is too large.");
            case PgpImportFailure.TooMany:
                return L10n.T("Malachi Mail cannot store more keys. Remove keys you no longer need.");
        }
        return L10n.T("The key could not be imported.");
    }

    /// <summary>signature.ImportedText: the toast after <c>pgp.keyImport</c>.</summary>
    public static string ImportedText(PgpKeyImportResult r)
    {
        ArgumentNullException.ThrowIfNull(r);
        if (r.Added > 0)
        {
            return L10n.N("%d key imported", "%d keys imported", r.Added, r.Added);
        }
        if (r.Updated > 0)
        {
            return L10n.N("%d key updated", "%d keys updated", r.Updated, r.Updated);
        }
        return L10n.T("The key was already imported.");
    }

    /// <summary>
    /// signature.ImportDialogTexts: the texts of the Import OpenPGP Key dialog:
    /// a text area for a pasted key (its placeholder is
    /// <see cref="SignatureView.ArmorPlaceholder"/>, not translated) and a file
    /// chooser.
    /// </summary>
    public static SignatureImportDialog ImportDialogTexts() => new(
        L10n.T("Import OpenPGP Key"),
        L10n.T("Paste a public key, or choose a file with one."),
        L10n.T("Choose File…"),
        // TRANSLATORS: name of the file type filter of the file chooser
        L10n.T("OpenPGP keys"),
        L10n.T("_Cancel"),
        L10n.T("_Import"));

    /// <summary>signature.SecurityPageTexts: the texts of Preferences → Security.</summary>
    public static SignatureSecurityPage SecurityPageTexts() => new(
        // TRANSLATORS: the Preferences page with the OpenPGP keys
        L10n.C("preferences page", "Security"),
        L10n.T("OpenPGP Keys"),
        L10n.T("Keys of people whose signed mail you receive. Malachi Mail finds them in your mail and never looks them up online."),
        L10n.T("No OpenPGP Keys"),
        L10n.T("Import Key…"),
        L10n.T("Could not load the keys."));

    /// <summary>signature.keyTrustPill: the pill of a key row's trust.</summary>
    public static string KeyTrustPill(PgpKeyTrust trust) =>
        trust.Value == PgpKeyTrust.Verified ? L10n.C("key trust", "Confirmed") : L10n.C("key trust", "Not confirmed");

    /// <summary>signature.keyStatePill: the pill of a key that cannot verify anything new ("" for a usable key).</summary>
    public static string KeyStatePill(bool revoked, bool expired)
    {
        if (revoked)
        {
            return L10n.C("key state", "Revoked");
        }
        return expired ? L10n.C("key state", "Expired") : "";
    }

    /// <summary>
    /// signature.keyOrigin: the line of a key row that says where it came from
    /// and when; <paramref name="day"/> is the formatted first-seen date.
    /// </summary>
    public static string KeyOrigin(PgpKeySource source, string day)
    {
        switch (source.Value)
        {
            case PgpKeySource.Autocrypt:
                // TRANSLATORS: %s is a date
                return L10n.T("Found in a message header on %s", day);
            case PgpKeySource.Attached:
                // TRANSLATORS: %s is a date
                return L10n.T("Imported from a message on %s", day);
        }
        // TRANSLATORS: %s is a date
        return L10n.T("Imported on %s", day);
    }
}
