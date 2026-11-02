// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import "time"

// Signatures of incoming mail (docs/api.md §4.21; the design is
// docs/plans/signatures.md, phase 1: verifying S/MIME and OpenPGP/MIME
// signatures, nothing is signed, decrypted or encrypted). The daemon
// verifies a message's signature when it stores the message, keeps the
// verdict and hands it to clients with the summary (the list's icon) and
// with message.get (the header's badge); message.signature returns the
// details for a dialog. A verdict is never an error.
//
// Every string of these types that comes from a certificate or a key
// (names, addresses, distinguished names, user IDs, header values) is
// untrusted display text: the daemon removes control, format, bidi and
// invisible characters and caps it at MaxSignatureTextBytes, and clients
// show it as plain text only. Every enum is open: a client treats an
// unknown SignatureStatus as SignatureError, and ignores an unknown
// SignatureNote, SignatureReason, RevocationState or PGPKeySource.

// ---------------------------------------------------------------------------
// Limits. Exceeding an input limit is invalidArgument. The Windows and
// Swift mirrors check these values.
// ---------------------------------------------------------------------------

const (
	// MaxSignatureTextBytes caps every string from a certificate or key.
	MaxSignatureTextBytes = 256
	// MaxSignatureChain caps SMIMEDetails.Chain (signer first).
	MaxSignatureChain = 16
	// MaxSignatureEmails caps SignatureCertificate.Emails and PGPKey.Emails.
	MaxSignatureEmails = 16
	// MaxSignatureUserIDs caps PGPDetails.UserIDs and PGPKey.UserIDs.
	MaxSignatureUserIDs = 32
	// MaxProtectedHeaders caps SignatureDetails.ProtectedHeaders.
	MaxProtectedHeaders = 6

	// MaxPGPKeyBytes caps the key data of pgp.keyImport (Armored, Data, or
	// the message part it names).
	MaxPGPKeyBytes = 1 << 20
	// MaxPGPImportKeys caps the keys one pgp.keyImport may carry.
	MaxPGPImportKeys = 20
	// MaxPGPStoredKeys caps the stored keys; an import over it is refused
	// (PGPImportTooMany) and Autocrypt stops adding keys.
	MaxPGPStoredKeys = 5000

	// MaxRevocationResponseBytes caps an OCSP response or a CRL.
	MaxRevocationResponseBytes = 1 << 20
)

// RevocationTimeout bounds the whole of one revocation check (OCSP, then
// the CRL); clients give message.signatureRevocation a longer timeout
// (20 s).
const RevocationTimeout = 15 * time.Second

// RevocationCheckInterval is the shortest time between two checks of one
// certificate; message.signatureRevocation within it returns the stored
// state without asking the network.
const RevocationCheckInterval = time.Minute

// ---------------------------------------------------------------------------
// Verdict
// ---------------------------------------------------------------------------

// SignatureKind is the format of a signature.
type SignatureKind string

const (
	SignatureSMIME SignatureKind = "smime" // S/MIME (CMS SignedData), clear or opaque
	SignaturePGP   SignatureKind = "pgp"   // OpenPGP/MIME (RFC 3156), or inline (never verified)
)

// SignatureStatus is the verdict of a message's signature (the first of
// the plan's §3 order that applies).
type SignatureStatus string

const (
	// SignatureInvalid: the signature does not match the content, its
	// structure is broken, it uses MD5, or the certificate is not for
	// signing e-mail. Red.
	SignatureInvalid SignatureStatus = "invalid"
	// SignatureRevoked: S/MIME, a revocation check found the certificate
	// revoked before the message arrived; OpenPGP, the key was revoked
	// before it arrived. Red.
	SignatureRevoked SignatureStatus = "revoked"
	// SignatureMismatch: the signature matches, but its identity (the
	// certificate's or key's address) is not the From address. Yellow.
	SignatureMismatch SignatureStatus = "mismatch"
	// SignatureUntrusted: S/MIME, the chain leads to no system root or the
	// certificate was not valid when the message arrived; OpenPGP, the key
	// had expired when it arrived. Yellow.
	SignatureUntrusted SignatureStatus = "untrusted"
	// SignatureUnknownKey: OpenPGP, the key is not stored. Grey.
	SignatureUnknownKey SignatureStatus = "unknownKey"
	// SignatureWeak: matches and trusted, but SHA-1 or an RSA key shorter
	// than 2048 bits. Yellow.
	SignatureWeak SignatureStatus = "weak"
	// SignatureUnsupported: an algorithm or shape the daemon does not
	// verify (DSA, several signers, inline OpenPGP, a message the server
	// rebuilt). Grey.
	SignatureUnsupported SignatureStatus = "unsupported"
	// SignatureError: the verifier failed (a limit, damaged data). Grey.
	SignatureError SignatureStatus = "error"
	// SignatureValid: matches, trusted (S/MIME a system root, OpenPGP a
	// key the user confirmed) and the identity is the From address. Green.
	SignatureValid SignatureStatus = "valid"
	// SignatureValidSeen: OpenPGP, matches and the identity is the From
	// address, but the key was only found automatically (seen, TOFU).
	// Green without the check mark.
	SignatureValidSeen SignatureStatus = "validSeen"
)

// SignatureNote adds to a verdict without changing it.
type SignatureNote string

const (
	// SignatureNoteExpiredSince: the certificate or key has expired since
	// the message arrived (SignatureDetails.ExpiredAt); the verdict holds.
	SignatureNoteExpiredSince SignatureNote = "expiredSince"
	// SignatureNoteRevocationUnchecked: S/MIME, no revocation check has
	// been made for the signer's certificate.
	SignatureNoteRevocationUnchecked SignatureNote = "revocationUnchecked"
	// SignatureNoteRevocationGood: S/MIME, the last check found the
	// certificate not revoked (SMIMERevocation.CheckedAt).
	SignatureNoteRevocationGood SignatureNote = "revocationGood"
	// SignatureNoteRevokedAfter: the certificate or key was revoked after
	// the message arrived (SignatureDetails.RevokedAt); the verdict holds.
	SignatureNoteRevokedAfter SignatureNote = "revokedAfter"
	// SignatureNotePartialFooter: a text/plain part after the signed part
	// (a mailing list's footer) is not covered by the signature.
	SignatureNotePartialFooter SignatureNote = "partialFooter"
	// SignatureNoteProtectedHeadersDiffer: the signed part carries
	// protected headers (RFC 9788) that differ from the outer ones
	// (SignatureDetails.ProtectedHeaders); the outer ones are shown.
	SignatureNoteProtectedHeadersDiffer SignatureNote = "protectedHeadersDiffer"
	// SignatureNoteInlinePGP: an inline OpenPGP signature (status
	// unsupported); nothing was verified.
	SignatureNoteInlinePGP SignatureNote = "inlinePGP"
	// SignatureNoteKeyChanged: OpenPGP, another key was seen for the
	// signer's address before (TOFU warning; PGPDetails.PreviousFingerprint).
	SignatureNoteKeyChanged SignatureNote = "keyChanged"
	// SignatureNoteOpaque: S/MIME signed-data; the daemon unwrapped the
	// content, which is what the client shows.
	SignatureNoteOpaque SignatureNote = "opaque"
)

// SignatureReason says why a verdict is not valid (SignatureDetails.Reason;
// absent for valid, validSeen, mismatch, unknownKey and revoked, whose
// status says it).
type SignatureReason string

const (
	// Status invalid.
	SignatureReasonDigestMismatch SignatureReason = "digestMismatch" // the content changed after signing
	SignatureReasonBadSignature   SignatureReason = "badSignature"   // the signature does not verify with the key
	SignatureReasonMalformed      SignatureReason = "malformed"      // the signature's structure is broken
	SignatureReasonMD5            SignatureReason = "md5"            // signed with MD5
	SignatureReasonKeyUsage       SignatureReason = "keyUsage"       // certificate not for e-mail signing (EKU, keyUsage)
	// Status untrusted.
	SignatureReasonUnknownIssuer SignatureReason = "unknownIssuer" // the chain leads to no system root
	SignatureReasonSelfSigned    SignatureReason = "selfSigned"    // a self-signed certificate
	SignatureReasonNotYetValid   SignatureReason = "notYetValid"   // the certificate was not valid yet at arrival
	SignatureReasonExpired       SignatureReason = "expired"       // the certificate or key had expired at arrival
	// Status weak.
	SignatureReasonSHA1     SignatureReason = "sha1"     // SHA-1 digest
	SignatureReasonShortKey SignatureReason = "shortKey" // RSA shorter than 2048 bits
	// Status unsupported (inline OpenPGP has the note, no reason).
	SignatureReasonAlgorithm       SignatureReason = "algorithm"       // DSA or another algorithm not verified
	SignatureReasonMultipleSigners SignatureReason = "multipleSigners" // more than one signer
	SignatureReasonRebuilt         SignatureReason = "rebuilt"         // the server rebuilt the MIME (Exchange via Graph)
	SignatureReasonFormat          SignatureReason = "format"          // another shape not verified
	// Status error.
	SignatureReasonLimit    SignatureReason = "limit"    // over a limit of the verifier
	SignatureReasonInternal SignatureReason = "internal" // the verifier failed
)

// MessageSignature is MessageSummary.Signature: what the list's icon
// needs. Absent for a message without a signature that is the verdict of
// the whole message, and while the verdict is pending.
type MessageSignature struct {
	Kind   SignatureKind   `json:"kind"`
	Status SignatureStatus `json:"status"`
}

// SignatureSigner is who signed: from the certificate (S/MIME) or the key's
// user ID that matched (OpenPGP), never from the message's headers.
type SignatureSigner struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	// Fingerprint is the SHA-256 of the signer's certificate (S/MIME, 64
	// lowercase hex digits) or the fingerprint of the signing key's primary
	// key (OpenPGP, 40 or 64 lowercase hex digits). Absent when the key is
	// unknown (PGPDetails.KeyID names it then).
	Fingerprint string `json:"fingerprint,omitempty"`
}

// SignatureSummary is Message.Signature (message.get, message.download):
// what the header's badge needs. It takes the place of
// MessageSummary.Signature on the wire.
type SignatureSummary struct {
	Kind   SignatureKind   `json:"kind"`
	Status SignatureStatus `json:"status"`
	// Signer is present for valid, validSeen, mismatch, untrusted, weak
	// and revoked; it may be present for the others.
	Signer *SignatureSigner `json:"signer,omitempty"`
	Notes  []SignatureNote  `json:"notes,omitempty"`
	// PartID is the detached signature part (application/pkcs7-signature,
	// application/pgp-signature) of a multipart/signed message: clients do
	// not show it among the attachments (plan S15). Absent for opaque
	// S/MIME and inline OpenPGP.
	PartID string `json:"partId,omitempty"`
}

// ProtectedHeaderName names a protected header (RFC 9788) that differs
// from the outer one.
type ProtectedHeaderName string

const (
	ProtectedSubject ProtectedHeaderName = "subject"
	ProtectedFrom    ProtectedHeaderName = "from"
	ProtectedTo      ProtectedHeaderName = "to"
	ProtectedCc      ProtectedHeaderName = "cc"
	ProtectedReplyTo ProtectedHeaderName = "replyTo"
	ProtectedDate    ProtectedHeaderName = "date"
)

// ProtectedHeader is a signed header whose value differs from the outer
// one; Value is the signed one (untrusted text, one line).
type ProtectedHeader struct {
	Name  ProtectedHeaderName `json:"name"`
	Value string              `json:"value"`
}

// SignatureDetails is what message.signature returns for the details
// dialog: the summary plus everything the dialog shows.
type SignatureDetails struct {
	SignatureSummary
	MessageID MessageID `json:"messageId"`
	// Reason says why the verdict is not valid (SignatureReason).
	Reason SignatureReason `json:"reason,omitempty"`
	// SignedAt is the signing time the signer declared (S/MIME
	// signingTime, OpenPGP signature creation time): the signer's claim,
	// never used to judge validity.
	SignedAt *time.Time `json:"signedAt,omitempty"`
	// ArrivedAt is when the message arrived (internal date): validity is
	// judged at this time.
	ArrivedAt time.Time `json:"arrivedAt"`
	// Algorithm is a language-neutral technical description, public key
	// then digest: "RSA 2048 / SHA-256", "ECDSA P-256 / SHA-256",
	// "Ed25519 / SHA-512". Shown as it is.
	Algorithm string `json:"algorithm,omitempty"`
	// ExpiredAt is when the certificate or key expired (with
	// SignatureNoteExpiredSince or SignatureReasonExpired).
	ExpiredAt *time.Time `json:"expiredAt,omitempty"`
	// RevokedAt is when the certificate or key was revoked (status revoked
	// or SignatureNoteRevokedAfter).
	RevokedAt *time.Time `json:"revokedAt,omitempty"`
	// ProtectedHeaders lists the signed headers that differ from the outer
	// ones (SignatureNoteProtectedHeadersDiffer), at most
	// MaxProtectedHeaders.
	ProtectedHeaders []ProtectedHeader `json:"protectedHeaders,omitempty"`
	// SMIME is set for Kind smime, PGP for Kind pgp.
	SMIME *SMIMEDetails `json:"smime,omitempty"`
	PGP   *PGPDetails   `json:"pgp,omitempty"`
}

// SignatureCertificate is one certificate of an S/MIME chain. Every string
// is untrusted text from the certificate.
type SignatureCertificate struct {
	SHA256    string    `json:"sha256"` // 64 lowercase hex digits of the DER encoding
	Subject   string    `json:"subject,omitempty"`
	Issuer    string    `json:"issuer,omitempty"`
	Emails    []string  `json:"emails,omitempty"` // rfc822Name, else emailAddress of the DN
	NotBefore time.Time `json:"notBefore"`
	NotAfter  time.Time `json:"notAfter"`
	// SystemRoot: the certificate is a root of the system's trust store.
	SystemRoot bool `json:"systemRoot,omitempty"`
	SelfSigned bool `json:"selfSigned,omitempty"`
}

// RevocationState is what is known of a certificate's revocation.
type RevocationState string

const (
	RevocationUnchecked RevocationState = "unchecked" // never checked
	RevocationGood      RevocationState = "good"      // not revoked when checked
	RevocationRevoked   RevocationState = "revoked"   // revoked (RevokedAt)
	RevocationUnknown   RevocationState = "unknown"   // the responder does not know the certificate
)

// RevocationSource is where the last check got its answer.
type RevocationSource string

const (
	RevocationOCSP RevocationSource = "ocsp"
	RevocationCRL  RevocationSource = "crl"
)

// SMIMERevocation is the revocation state of the signer's certificate.
type SMIMERevocation struct {
	State     RevocationState  `json:"state"`
	CheckedAt *time.Time       `json:"checkedAt,omitempty"`
	RevokedAt *time.Time       `json:"revokedAt,omitempty"`
	Source    RevocationSource `json:"source,omitempty"`
	// Checkable: the certificate names an http or https OCSP responder or
	// CRL distribution point, so message.signatureRevocation can check.
	Checkable bool `json:"checkable"`
}

// SMIMEDetails are the S/MIME parts of SignatureDetails.
type SMIMEDetails struct {
	// Chain is the signer's certificate first, then each issuer as far as
	// it was found (in the message or the system's roots), at most
	// MaxSignatureChain; empty when the signer's certificate is missing.
	Chain      []SignatureCertificate `json:"chain"`
	Revocation SMIMERevocation        `json:"revocation"`
}

// PGPKeyTrust is how far an OpenPGP key is trusted.
type PGPKeyTrust string

const (
	// PGPKeyUnknown: the key is not stored (SignatureDetails only).
	PGPKeyUnknown PGPKeyTrust = "unknown"
	// PGPKeySeen: found automatically or imported, not confirmed (TOFU).
	PGPKeySeen PGPKeyTrust = "seen"
	// PGPKeyVerified: the user confirmed its fingerprint.
	PGPKeyVerified PGPKeyTrust = "verified"
)

// PGPKeySource is where a stored key came from.
type PGPKeySource string

const (
	PGPSourceAutocrypt PGPKeySource = "autocrypt" // an Autocrypt header of received mail
	PGPSourceAttached  PGPKeySource = "attached"  // a key attached to a message, imported on request
	PGPSourceImport    PGPKeySource = "import"    // imported in Preferences (pasted or a file)
)

// PGPKeyOffer is a key the message carries that is not stored, or another
// key for the signer's address (TOFU: the daemon never replaces one by
// itself). The client passes it to pgp.keyImport as it is.
type PGPKeyOffer struct {
	Fingerprint string `json:"fingerprint"`
	// PartID names the attached key (application/pgp-keys, *.asc);
	// Autocrypt names the message's Autocrypt header. Exactly one is set.
	PartID    string `json:"partId,omitempty"`
	Autocrypt bool   `json:"autocrypt,omitempty"`
}

// PGPDetails are the OpenPGP parts of SignatureDetails.
type PGPDetails struct {
	KeyTrust PGPKeyTrust `json:"keyTrust"`
	// KeyID is the signing (sub)key's 16 lowercase hex digits, from the
	// signature; always set when the signature could be read.
	KeyID string `json:"keyId,omitempty"`
	// PrimaryFingerprint is the stored key's primary fingerprint; absent
	// for keyTrust unknown. It is what pgp.keyVerify takes.
	PrimaryFingerprint string       `json:"primaryFingerprint,omitempty"`
	UserIDs            []string     `json:"userIds,omitempty"`
	KeySource          PGPKeySource `json:"keySource,omitempty"`
	FirstSeen          *time.Time   `json:"firstSeen,omitempty"`
	// PreviousFingerprint is the key seen earlier for the signer's address
	// (SignatureNoteKeyChanged).
	PreviousFingerprint string `json:"previousFingerprint,omitempty"`
	// Importable is a key of the message that is not stored yet (plan
	// §5): *Import Key* passes it to pgp.keyImport.
	Importable *PGPKeyOffer `json:"importable,omitempty"`
}

// ---------------------------------------------------------------------------
// OpenPGP keys
// ---------------------------------------------------------------------------

// PGPKey is a stored public key (never a secret one).
type PGPKey struct {
	// Fingerprint is the primary key's, 40 (v4) or 64 (v6) lowercase hex
	// digits.
	Fingerprint string `json:"fingerprint"`
	// UserIDs are the key's user IDs ("Jan Novák <jan@example.org>"),
	// untrusted text, at most MaxSignatureUserIDs; Emails their addresses,
	// the domain lower case.
	UserIDs   []string     `json:"userIds"`
	Emails    []string     `json:"emails"`
	Trust     PGPKeyTrust  `json:"trust"` // seen or verified
	Source    PGPKeySource `json:"source"`
	FirstSeen time.Time    `json:"firstSeen"`
	CreatedAt time.Time    `json:"createdAt"`
	ExpiresAt *time.Time   `json:"expiresAt,omitempty"`
	RevokedAt *time.Time   `json:"revokedAt,omitempty"`
	// Algorithm describes the primary key ("Ed25519", "RSA 4096").
	Algorithm string `json:"algorithm,omitempty"`
}

// ---------------------------------------------------------------------------
// Methods
// ---------------------------------------------------------------------------

type MessageSignatureParams struct {
	MessageID MessageID `json:"messageId"`
}

// MessageSignatureResult is the result of message.signature and
// message.signatureRevocation. Signature is null for a message without a
// verdict (no signature of the whole message, or not verified yet).
type MessageSignatureResult struct {
	Signature *SignatureDetails `json:"signature"`
}

type MessageSignatureRevocationParams struct {
	MessageID MessageID `json:"messageId"`
}

// RevocationFailure is the class of a failed revocation check
// (RevocationFailedData).
type RevocationFailure string

const (
	RevocationFailNetwork  RevocationFailure = "network"  // could not connect
	RevocationFailTimeout  RevocationFailure = "timeout"  // no answer within RevocationTimeout
	RevocationFailServer   RevocationFailure = "server"   // the responder answered an error (HTTP, OCSP status)
	RevocationFailResponse RevocationFailure = "response" // an answer that is unsigned, for another certificate, too large or unreadable
	RevocationFailAddress  RevocationFailure = "address"  // the URL is not one the daemon contacts (not public, a redirect)
)

// RevocationFailedData is Error.Data of CodeRevocationFailed.
type RevocationFailedData struct {
	Reason RevocationFailure `json:"reason"`
}

// PGPKeyImportParams names the key data: exactly one of MessageID with
// PartID (an attached key), MessageID with Autocrypt (the message's
// Autocrypt header), Armored (ASCII armour) or Data (binary).
type PGPKeyImportParams struct {
	MessageID MessageID `json:"messageId,omitempty"`
	PartID    string    `json:"partId,omitempty"`
	Autocrypt bool      `json:"autocrypt,omitempty"`
	Armored   string    `json:"armored,omitempty"`
	Data      []byte    `json:"data,omitempty"` // base64 on the wire
}

// PGPKeyImportResult lists the keys of the data as stored after the
// import. Added counts keys that were not stored, Updated stored keys that
// gained something (a user ID, a revocation, a new expiry); a key that
// was stored unchanged counts in neither.
type PGPKeyImportResult struct {
	Keys    []PGPKey `json:"keys"`
	Added   int      `json:"added"`
	Updated int      `json:"updated"`
}

// PGPImportFailure is why pgp.keyImport refused the data
// (PGPImportErrorData, with invalidArgument).
type PGPImportFailure string

const (
	PGPImportNoKey     PGPImportFailure = "noKey"     // the data holds no public key
	PGPImportMalformed PGPImportFailure = "malformed" // the data cannot be read
	PGPImportSecretKey PGPImportFailure = "secretKey" // it holds a secret key; nothing is stored
	PGPImportTooLarge  PGPImportFailure = "tooLarge"  // over MaxPGPKeyBytes or MaxPGPImportKeys
	PGPImportTooMany   PGPImportFailure = "tooMany"   // MaxPGPStoredKeys reached
)

// PGPImportErrorData is Error.Data of invalidArgument from pgp.keyImport
// when the data was refused (not for a malformed request).
type PGPImportErrorData struct {
	Reason PGPImportFailure `json:"reason"`
}

type PGPKeyVerifyParams struct {
	Fingerprint string `json:"fingerprint"`
	// Verified: true confirms the key, false takes the confirmation back
	// (the key is seen again).
	Verified bool `json:"verified"`
}

type PGPKeyVerifyResult struct {
	Key PGPKey `json:"key"`
}

type PGPKeyListParams struct{}

// PGPKeyListResult lists every stored key, sorted by the first e-mail
// address, then the fingerprint.
type PGPKeyListResult struct {
	Keys []PGPKey `json:"keys"`
}

type PGPKeyRemoveParams struct {
	Fingerprint string `json:"fingerprint"`
}

type PGPKeyRemoveResult struct{}

// ---------------------------------------------------------------------------
// Notifications
// ---------------------------------------------------------------------------

// SignaturesChangedNotification: trust changed (a key imported, confirmed
// or removed, a revocation check, another set of system roots). Clients
// call message.get for the messages they show and message.signature for
// an open details dialog; the rows whose verdict changed arrive as
// notify.messagesChanged.
type SignaturesChangedNotification struct{}
