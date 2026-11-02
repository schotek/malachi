// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package signature

import (
	"fmt"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Every text of the package, so that the template and the ports see them
// in one place. "S/MIME", "OpenPGP" and "Autocrypt" are names and stay as
// they are in every language; a signer's name, address, fingerprint and a
// date are inserted with fmt.Sprintf, never concatenated.

// badgeText is the sentence of the header's badge; signer is SignerLabel
// of the summary ("" when it has none).
func badgeText(s *api.SignatureSummary, signer string, tr Translator) string {
	switch s.Status {
	case api.SignatureValid, api.SignatureValidSeen:
		if signer == "" {
			return tr.T("Valid signature")
		}
		// TRANSLATORS: the header's badge of a signed message; %s is the
		// signer's name and address from the certificate or key.
		return fmt.Sprintf(tr.T("Signed by %s"), signer)
	case api.SignatureMismatch:
		if signer == "" {
			return tr.T("Signed by someone else")
		}
		// TRANSLATORS: the signature is valid but belongs to another address
		// than the sender's; %s is the signer's name and address.
		return fmt.Sprintf(tr.T("Signed by someone else: %s"), signer)
	case api.SignatureUntrusted:
		if signer == "" {
			return tr.T("Untrusted signature")
		}
		if s.Kind == api.SignaturePGP {
			return fmt.Sprintf(tr.T("Signed by %s, but the key had expired"), signer)
		}
		return fmt.Sprintf(tr.T("Signed by %s, but the certificate is not trusted"), signer)
	case api.SignatureWeak:
		if signer == "" {
			return tr.T("Weak signature")
		}
		return fmt.Sprintf(tr.T("Signed by %s with a weak algorithm"), signer)
	case api.SignatureRevoked:
		if s.Kind == api.SignaturePGP {
			return tr.T("Signed with a revoked key")
		}
		return tr.T("Signed with a revoked certificate")
	case api.SignatureInvalid:
		return tr.T("Invalid signature")
	case api.SignatureUnknownKey:
		return tr.T("Signed with an unknown OpenPGP key")
	case api.SignatureUnsupported:
		if hasNote(s.Notes, api.SignatureNoteInlinePGP) {
			return tr.T("Inline OpenPGP signature, not verified")
		}
		return tr.T("Signature not verified")
	}
	return tr.T("Signature could not be verified")
}

// StatusTitle is the short name of a verdict: the details dialog's
// heading and the tooltip of the list's icon. An unknown status reads as
// api.SignatureError.
func StatusTitle(kind api.SignatureKind, status api.SignatureStatus, tr Translator) string {
	switch status {
	case api.SignatureValid:
		return tr.T("Valid signature")
	case api.SignatureValidSeen:
		// TRANSLATORS: an OpenPGP signature that matches, made with a key
		// found automatically that the user has not confirmed yet.
		return tr.T("Valid signature, key not confirmed")
	case api.SignatureMismatch:
		return tr.T("Signed by someone else")
	case api.SignatureUntrusted:
		return tr.T("Untrusted signature")
	case api.SignatureWeak:
		return tr.T("Weak signature")
	case api.SignatureInvalid:
		return tr.T("Invalid signature")
	case api.SignatureRevoked:
		if kind == api.SignaturePGP {
			return tr.T("Revoked key")
		}
		return tr.T("Revoked certificate")
	case api.SignatureUnknownKey:
		return tr.T("Unknown key")
	case api.SignatureUnsupported:
		return tr.T("Signature not verified")
	}
	return tr.T("Signature could not be verified")
}

// explanation is the paragraph under the details dialog's heading; from
// is the message's From address ("" unknown).
func explanation(s *api.SignatureSummary, from string, tr Translator) string {
	switch s.Status {
	case api.SignatureValid:
		if s.Kind == api.SignaturePGP {
			return tr.T("The signature matches the message, and you confirmed the key.")
		}
		return tr.T("The signature matches the message, and the certificate was issued by an authority this computer trusts.")
	case api.SignatureValidSeen:
		return tr.T("The signature matches the message. The key was found automatically: compare its fingerprint with the sender to confirm it.")
	case api.SignatureMismatch:
		if s.Signer != nil && s.Signer.Email != "" && from != "" {
			// TRANSLATORS: the first %s is the address of the certificate or
			// key, the second the sender's address of the message.
			return fmt.Sprintf(tr.T("The signature is valid, but it belongs to %s, not to the sender %s."), clean(s.Signer.Email), clean(from))
		}
		return tr.T("The signature is valid, but it does not belong to the sender's address.")
	case api.SignatureUntrusted:
		if s.Kind == api.SignaturePGP {
			return tr.T("The signature matches the message, but the key had expired when the message arrived.")
		}
		return tr.T("The signature matches the message, but the certificate cannot be trusted.")
	case api.SignatureWeak:
		return tr.T("The signature matches the message, but it uses an algorithm that is no longer considered safe.")
	case api.SignatureInvalid:
		return tr.T("The message may have been changed after it was signed. Do not trust its content.")
	case api.SignatureRevoked:
		if s.Kind == api.SignaturePGP {
			return tr.T("The key was revoked before the message arrived. Do not trust its content.")
		}
		return tr.T("The certificate was revoked before the message arrived. Do not trust its content.")
	case api.SignatureUnknownKey:
		return tr.T("The message is signed with an OpenPGP key that Malachi Mail does not have. Import the sender's key to verify it.")
	case api.SignatureUnsupported:
		return tr.T("Malachi Mail cannot verify this kind of signature.")
	}
	return tr.T("Malachi Mail could not verify the signature.")
}

// caveat is the line under a valid verdict's details.
func caveat(tr Translator) string {
	return tr.T("A valid signature tells who sent the message, not that its content is true or safe.")
}

// NoteText is the sentence of a note ("" for a note this client does not
// know, which is not shown).
func NoteText(kind api.SignatureKind, n api.SignatureNote, tr Translator) string {
	pgp := kind == api.SignaturePGP
	switch n {
	case api.SignatureNoteExpiredSince:
		if pgp {
			return tr.T("The key has expired since the message arrived.")
		}
		return tr.T("The certificate has expired since the message arrived.")
	case api.SignatureNoteRevocationUnchecked:
		return tr.T("Whether the certificate was revoked has not been checked.")
	case api.SignatureNoteRevocationGood:
		return tr.T("The certificate was not revoked when last checked.")
	case api.SignatureNoteRevokedAfter:
		if pgp {
			return tr.T("The key was revoked after the message arrived.")
		}
		return tr.T("The certificate was revoked after the message arrived.")
	case api.SignatureNotePartialFooter:
		return tr.T("A footer added after signing, such as a mailing list's, is not covered by the signature.")
	case api.SignatureNoteProtectedHeadersDiffer:
		return tr.T("The signed headers differ from the ones shown.")
	case api.SignatureNoteInlinePGP:
		return tr.T("Inline OpenPGP signatures are not verified: they do not cover attachments or formatting.")
	case api.SignatureNoteKeyChanged:
		return tr.T("This sender used a different key before.")
	case api.SignatureNoteOpaque:
		return tr.T("The signed content was unpacked from the signature.")
	}
	return ""
}

// ReasonText is the sentence of a reason ("" for none, internal and an
// unknown reason: the explanation says enough).
func ReasonText(kind api.SignatureKind, r api.SignatureReason, tr Translator) string {
	switch r {
	case api.SignatureReasonDigestMismatch:
		return tr.T("The message was changed after it was signed.")
	case api.SignatureReasonBadSignature:
		return tr.T("The signature does not match the signer's certificate or key.")
	case api.SignatureReasonMalformed:
		return tr.T("The signature is damaged.")
	case api.SignatureReasonMD5:
		return tr.T("The signature uses MD5, which is broken.")
	case api.SignatureReasonKeyUsage:
		return tr.T("The certificate is not meant for signing e-mail.")
	case api.SignatureReasonUnknownIssuer:
		return tr.T("The certificate was not issued by an authority this computer trusts.")
	case api.SignatureReasonSelfSigned:
		return tr.T("The certificate is self-signed.")
	case api.SignatureReasonNotYetValid:
		return tr.T("The certificate was not valid yet when the message arrived.")
	case api.SignatureReasonExpired:
		if kind == api.SignaturePGP {
			return tr.T("The key had expired when the message arrived.")
		}
		return tr.T("The certificate had expired when the message arrived.")
	case api.SignatureReasonSHA1:
		return tr.T("The signature uses SHA-1, which is no longer considered safe.")
	case api.SignatureReasonShortKey:
		return tr.T("The signing key is shorter than 2048 bits.")
	case api.SignatureReasonAlgorithm:
		return tr.T("The signature uses an algorithm Malachi Mail does not support.")
	case api.SignatureReasonMultipleSigners:
		return tr.T("The message has more than one signer, which Malachi Mail does not support.")
	case api.SignatureReasonRebuilt:
		return tr.T("The mail server changed the message, so its signature cannot be checked.")
	case api.SignatureReasonFormat:
		return tr.T("Malachi Mail does not support this kind of signed message.")
	case api.SignatureReasonLimit:
		return tr.T("The signature is too large or too complex to check.")
	}
	return ""
}

// rowLabel is the label of a row of the details.
func rowLabel(k RowKey, tr Translator) string {
	switch k {
	case RowSigner:
		// TRANSLATORS: label of the details row with the signer's name and address
		return tr.C("signature", "Signed by")
	case RowKind:
		// TRANSLATORS: label of the row that says S/MIME or OpenPGP
		return tr.C("signature", "Signature type")
	case RowReason:
		// TRANSLATORS: label of the row that says why the signature is not valid
		return tr.C("signature", "Problem")
	case RowSignedAt:
		// TRANSLATORS: label of the signing time; the signer writes it, so
		// it is only a claim
		return tr.C("signature", "Signed, as stated by the signer")
	case RowArrivedAt:
		// TRANSLATORS: label of the time the message arrived
		return tr.C("signature", "Arrived")
	case RowAlgorithm:
		return tr.C("signature", "Algorithm")
	case RowCertFingerprint:
		return tr.C("certificate", "SHA-256 fingerprint")
	case RowKeyFingerprint:
		return tr.C("signature", "Key fingerprint")
	case RowKeyID:
		return tr.C("signature", "Key ID")
	case RowKeyTrust:
		// TRANSLATORS: label of the row that says whether the OpenPGP key is confirmed
		return tr.C("signature", "Key")
	case RowKeySource:
		return tr.C("signature", "Key source")
	case RowFirstSeen:
		// TRANSLATORS: label of the date the OpenPGP key was first seen
		return tr.C("signature", "First seen")
	case RowUserIDs:
		return tr.C("signature", "User IDs")
	case RowPreviousKey:
		// TRANSLATORS: label of the fingerprint of the key the sender used before
		return tr.C("signature", "Previous key")
	case RowExpired:
		return tr.C("signature", "Expired")
	case RowRevoked:
		return tr.C("signature", "Revoked")
	case RowRevocation:
		// TRANSLATORS: label of the row with the result of the revocation check
		return tr.C("signature", "Revocation")
	case RowIssuedTo:
		return tr.C("certificate", "Issued to")
	case RowIssuedBy:
		return tr.C("certificate", "Issued by")
	case RowValidity:
		return tr.C("certificate", "Valid")
	case RowEmails:
		// TRANSLATORS: label of the e-mail addresses a certificate names
		return tr.C("signature", "E-mail addresses")
	}
	return ""
}

// validity is a certificate's validity period.
func validity(from, to string, tr Translator) string {
	return fmt.Sprintf(tr.C("certificate", "%s to %s"), from, to)
}

// keyTrustText is the value of RowKeyTrust.
func keyTrustText(t api.PGPKeyTrust, tr Translator) string {
	switch t {
	case api.PGPKeyVerified:
		return tr.T("Confirmed by you")
	case api.PGPKeySeen:
		return tr.T("Found automatically, not confirmed")
	}
	return tr.T("Not in Malachi Mail")
}

// keySourceText is the value of RowKeySource ("" unknown).
func keySourceText(s api.PGPKeySource, tr Translator) string {
	switch s {
	case api.PGPSourceAutocrypt:
		// TRANSLATORS: Autocrypt is a name; the key came in a message's header
		return tr.T("Autocrypt header of a message")
	case api.PGPSourceAttached:
		return tr.T("Attached to a message")
	case api.PGPSourceImport:
		return tr.T("Imported by you")
	}
	return ""
}

// revocationText is the value of RowRevocation.
func revocationText(r api.SMIMERevocation, date func(time.Time) string, tr Translator) string {
	switch r.State {
	case api.RevocationGood:
		if r.CheckedAt != nil {
			// TRANSLATORS: %s is the date and time of the check
			return fmt.Sprintf(tr.T("Not revoked (checked %s)"), date(*r.CheckedAt))
		}
	case api.RevocationRevoked:
		if r.RevokedAt != nil {
			// TRANSLATORS: %s is the date the certificate was revoked
			return fmt.Sprintf(tr.T("Revoked on %s"), date(*r.RevokedAt))
		}
		return tr.C("revocation", "Revoked")
	case api.RevocationUnknown:
		if r.CheckedAt != nil {
			// TRANSLATORS: the issuer does not know the certificate; %s is the
			// date and time of the check
			return fmt.Sprintf(tr.T("Unknown to the issuer (checked %s)"), date(*r.CheckedAt))
		}
	}
	return tr.T("Not checked")
}

// sectionTitle is the heading of a section of the details.
func sectionTitle(k SectionKey, tr Translator) string {
	switch k {
	case SectionSignerCertificate:
		return tr.T("Signer's Certificate")
	case SectionIntermediate:
		return tr.T("Intermediate Certificate")
	case SectionRoot:
		return tr.T("Root Certificate")
	case SectionSignedHeaders:
		return tr.T("Signed Headers")
	}
	return ""
}

// headerName is the label of a signed header ("" unknown).
func headerName(n api.ProtectedHeaderName, tr Translator) string {
	switch n {
	case api.ProtectedSubject:
		return tr.C("signed header", "Subject")
	case api.ProtectedFrom:
		return tr.C("signed header", "From")
	case api.ProtectedTo:
		return tr.C("signed header", "To")
	case api.ProtectedCc:
		return tr.C("signed header", "Cc")
	case api.ProtectedReplyTo:
		return tr.C("signed header", "Reply-To")
	case api.ProtectedDate:
		return tr.C("signed header", "Date")
	}
	return ""
}

// DialogTitle is the title of the details dialog (popover, flyout).
func DialogTitle(tr Translator) string { return tr.T("Signature Details") }

// BadgeTooltip is the tooltip of the header's badge, which opens the
// details.
func BadgeTooltip(tr Translator) string { return tr.T("Show Signature Details") }

// CloseLabel is the label of the dialog's close button (where the
// platform's dialog has none of its own).
func CloseLabel(tr Translator) string { return tr.T("Close") }

// buttonLabel is the label of a details button; busy while it runs.
func buttonLabel(a Action, again, busy bool, tr Translator) string {
	switch a {
	case ActionCheckRevocation:
		switch {
		case busy:
			// TRANSLATORS: the button while the revocation check runs
			return tr.T("Checking…")
		case again:
			return tr.T("Check Revocation Again")
		}
		// TRANSLATORS: button: ask the certificate's issuer whether the
		// certificate was revoked
		return tr.T("Check Revocation")
	case ActionImportKey:
		return tr.T("Import Key")
	case ActionConfirmKey:
		return tr.T("Confirm Key…")
	case ActionUnconfirmKey:
		// TRANSLATORS: menu item: the key is no longer marked as confirmed
		return tr.T("Remove Confirmation")
	case ActionRemoveKey:
		return tr.T("Remove Key…")
	}
	return ""
}

// RevocationCaption is the line under Check Revocation.
func RevocationCaption(tr Translator) string {
	return tr.T("Checking tells the certificate's issuer that the certificate is in use.")
}

// ConfirmKeyQuestion is the question of Confirm Key…: heading, body
// (the dialog shows the formatted fingerprint under it, in monospace) and
// the buttons.
func ConfirmKeyQuestion(tr Translator) Question {
	return Question{
		Heading: tr.T("Confirm This Key?"),
		Body:    tr.T("Compare this fingerprint with the one the sender gives you in person or in another way you trust."),
		Cancel:  tr.T("_Cancel"),
		// TRANSLATORS: the button that marks the OpenPGP key as confirmed
		Accept: tr.T("_Confirm Key"),
	}
}

// RemoveKeyQuestion is the question of Remove Key….
func RemoveKeyQuestion(tr Translator) Question {
	return Question{
		Heading:     tr.T("Remove This Key?"),
		Body:        tr.T("Messages signed with it will show an unknown key until it is imported again."),
		Cancel:      tr.T("_Cancel"),
		Accept:      tr.T("_Remove"),
		Destructive: true,
	}
}

// KeyConfirmedText, ConfirmationRemovedText and KeyRemovedText are the
// toasts after pgp.keyVerify and pgp.keyRemove.
func KeyConfirmedText(tr Translator) string        { return tr.T("Key confirmed") }
func ConfirmationRemovedText(tr Translator) string { return tr.T("Confirmation removed") }
func KeyRemovedText(tr Translator) string          { return tr.T("Key removed") }

// RevocationFailedText is the error of message.signatureRevocation, by
// the class in its data (RevocationFailure; "" or another error: the
// general sentence).
func RevocationFailedText(reason api.RevocationFailure, tr Translator) string {
	switch reason {
	case api.RevocationFailNetwork:
		return tr.T("Could not reach the certificate's issuer.")
	case api.RevocationFailTimeout:
		return tr.T("The certificate's issuer did not answer in time.")
	case api.RevocationFailServer:
		return tr.T("The certificate's issuer could not answer.")
	case api.RevocationFailResponse:
		return tr.T("The issuer's answer could not be verified.")
	case api.RevocationFailAddress:
		return tr.T("The certificate names an address Malachi Mail does not contact.")
	}
	return tr.T("Could not check whether the certificate was revoked.")
}

// ImportFailedText is the error of pgp.keyImport, by the class in its
// data (PGPImportFailure; "" or another error: the general sentence).
func ImportFailedText(reason api.PGPImportFailure, tr Translator) string {
	switch reason {
	case api.PGPImportNoKey:
		return tr.T("This is not an OpenPGP public key.")
	case api.PGPImportMalformed:
		return tr.T("The key cannot be read.")
	case api.PGPImportSecretKey:
		return tr.T("This is a secret key. Import only the public key: Malachi Mail never stores secret keys.")
	case api.PGPImportTooLarge:
		return tr.T("The key data is too large.")
	case api.PGPImportTooMany:
		return tr.T("Malachi Mail cannot store more keys. Remove keys you no longer need.")
	}
	return tr.T("The key could not be imported.")
}

// ImportedText is the toast after pgp.keyImport.
func ImportedText(r api.PGPKeyImportResult, tr Translator) string {
	switch {
	case r.Added > 0:
		return fmt.Sprintf(tr.N("%d key imported", "%d keys imported", r.Added), r.Added)
	case r.Updated > 0:
		return fmt.Sprintf(tr.N("%d key updated", "%d keys updated", r.Updated), r.Updated)
	}
	return tr.T("The key was already imported.")
}

// ImportDialog are the texts of the Import OpenPGP Key dialog: a text
// area for a pasted key (its placeholder is ArmorPlaceholder, not
// translated) and a file chooser.
type ImportDialog struct {
	Title, Body, ChooseFile, FileFilter, Cancel, Import string
}

// ImportDialogTexts are the texts of the import dialog.
func ImportDialogTexts(tr Translator) ImportDialog {
	return ImportDialog{
		Title:      tr.T("Import OpenPGP Key"),
		Body:       tr.T("Paste a public key, or choose a file with one."),
		ChooseFile: tr.T("Choose File…"),
		// TRANSLATORS: name of the file type filter of the file chooser
		FileFilter: tr.T("OpenPGP keys"),
		Cancel:     tr.T("_Cancel"),
		Import:     tr.T("_Import"),
	}
}

// SecurityPage are the texts of Preferences → Security.
type SecurityPage struct {
	// Title is the page's title (and its tab, sidebar item or navigation
	// item); Group the heading of the key list with its Description;
	// Empty the list's placeholder; Import the button that opens the
	// import dialog; LoadFailed the line when pgp.keyList failed.
	Title, Group, Description, Empty, Import, LoadFailed string
}

// SecurityPageTexts are the texts of Preferences → Security.
func SecurityPageTexts(tr Translator) SecurityPage {
	return SecurityPage{
		// TRANSLATORS: the Preferences page with the OpenPGP keys
		Title:       tr.C("preferences page", "Security"),
		Group:       tr.T("OpenPGP Keys"),
		Description: tr.T("Keys of people whose signed mail you receive. Malachi Mail finds them in your mail and never looks them up online."),
		Empty:       tr.T("No OpenPGP Keys"),
		Import:      tr.T("Import Key…"),
		LoadFailed:  tr.T("Could not load the keys."),
	}
}

// keyTrustPill is the pill of a key row's trust.
func keyTrustPill(t api.PGPKeyTrust, tr Translator) string {
	if t == api.PGPKeyVerified {
		return tr.C("key trust", "Confirmed")
	}
	return tr.C("key trust", "Not confirmed")
}

// keyStatePill is the pill of a key that cannot verify anything new
// ("" for a usable key).
func keyStatePill(revoked, expired bool, tr Translator) string {
	switch {
	case revoked:
		return tr.C("key state", "Revoked")
	case expired:
		return tr.C("key state", "Expired")
	}
	return ""
}

// keyOrigin is the line of a key row that says where it came from and
// when; date formats FirstSeen.
func keyOrigin(s api.PGPKeySource, when string, tr Translator) string {
	switch s {
	case api.PGPSourceAutocrypt:
		// TRANSLATORS: %s is a date
		return fmt.Sprintf(tr.T("Found in a message header on %s"), when)
	case api.PGPSourceAttached:
		// TRANSLATORS: %s is a date
		return fmt.Sprintf(tr.T("Imported from a message on %s"), when)
	}
	// TRANSLATORS: %s is a date
	return fmt.Sprintf(tr.T("Imported on %s"), when)
}
