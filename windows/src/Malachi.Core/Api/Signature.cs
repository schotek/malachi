// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Go: backend/pkg/api/signature.go (all of it), the signature parts of
// types.go (MessageSummary.Signature, Message.Signature) and errors.go
// (1510 revocationFailed); contract: docs/api.md §4.21, §5
// (notify.signaturesChanged); design: docs/plans/signatures.md. Written
// from the Go types beside the Swift mirror, which was written at the same
// time.
//
// Signatures of incoming mail: the daemon verifies an S/MIME or OpenPGP/MIME
// signature when it stores a message and hands the verdict to the client with
// the summary (the list's icon) and with message.get (the header's badge);
// message.signature returns the details of the dialog. A verdict is never an
// error. Every string that comes from a certificate or a key (names,
// addresses, distinguished names, user IDs, header values) is untrusted
// display text, cleaned by the daemon and shown as plain text only
// (TextBlock.Text). Every enumeration is open: an unknown status reads as
// error, an unknown note, reason, state or source is ignored.
//
// Windows: Go's PGP and SMIME prefixes are Pgp and Smime (PgpKey, PgpDetails,
// SmimeDetails, …), the casing .NET gives acronyms of more than two letters;
// Go's SignatureDetails embeds SignatureSummary, here it derives from it, so
// the members sit in one JSON object as encoding/json writes them. The
// optional booleans Go leaves out when false (omitempty) are plain bool
// where a reader only reads them. The limits of signature.go are in
// API.Limits.

using System;
using System.Collections.Generic;
using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

/// <summary>api.SignatureKind: the format of a signature.</summary>
[JsonConverter(typeof(StringWireValueConverter<SignatureKind>))]
public readonly record struct SignatureKind(string Value) : IWireEnumeration<SignatureKind>
{
    /// <summary>S/MIME (CMS SignedData), clear or opaque.</summary>
    public const string Smime = "smime";

    /// <summary>OpenPGP/MIME (RFC 3156), or inline (never verified).</summary>
    public const string Pgp = "pgp";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator SignatureKind(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.SignatureStatus: the verdict of a message's signature.</summary>
[JsonConverter(typeof(StringWireValueConverter<SignatureStatus>))]
public readonly record struct SignatureStatus(string Value) : IWireEnumeration<SignatureStatus>
{
    /// <summary>The signature does not match the content, its structure is broken, it uses MD5, or the certificate is not for e-mail. Red.</summary>
    public const string Invalid = "invalid";

    /// <summary>The certificate or key was revoked before the message arrived. Red.</summary>
    public const string Revoked = "revoked";

    /// <summary>The signature matches, but its identity is not the From address. Yellow.</summary>
    public const string Mismatch = "mismatch";

    /// <summary>The chain leads to no system root, or the certificate or key was not valid at arrival. Yellow.</summary>
    public const string Untrusted = "untrusted";

    /// <summary>OpenPGP, the key is not stored. Grey.</summary>
    public const string UnknownKey = "unknownKey";

    /// <summary>Matches and trusted, but SHA-1 or a short RSA key. Yellow.</summary>
    public const string Weak = "weak";

    /// <summary>An algorithm or shape the daemon does not verify. Grey.</summary>
    public const string Unsupported = "unsupported";

    /// <summary>The verifier failed. Grey.</summary>
    public const string Error = "error";

    /// <summary>Matches, trusted, and the identity is the From address. Green.</summary>
    public const string Valid = "valid";

    /// <summary>OpenPGP, matches and the identity is the From address, but the key is only seen. Green without the check mark.</summary>
    public const string ValidSeen = "validSeen";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator SignatureStatus(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.SignatureNote: adds to a verdict without changing it.</summary>
[JsonConverter(typeof(StringWireValueConverter<SignatureNote>))]
public readonly record struct SignatureNote(string Value) : IWireEnumeration<SignatureNote>
{
    /// <summary>The certificate or key has expired since the message arrived.</summary>
    public const string ExpiredSince = "expiredSince";

    /// <summary>S/MIME, no revocation check has been made.</summary>
    public const string RevocationUnchecked = "revocationUnchecked";

    /// <summary>S/MIME, the last check found the certificate not revoked.</summary>
    public const string RevocationGood = "revocationGood";

    /// <summary>The certificate or key was revoked after the message arrived.</summary>
    public const string RevokedAfter = "revokedAfter";

    /// <summary>A text part after the signed part (a mailing list's footer) is not covered.</summary>
    public const string PartialFooter = "partialFooter";

    /// <summary>The signed part's protected headers differ from the outer ones.</summary>
    public const string ProtectedHeadersDiffer = "protectedHeadersDiffer";

    /// <summary>An inline OpenPGP signature; nothing was verified.</summary>
    public const string InlinePgp = "inlinePGP";

    /// <summary>OpenPGP, another key was seen for the signer's address before.</summary>
    public const string KeyChanged = "keyChanged";

    /// <summary>S/MIME signed-data; the daemon unwrapped the content.</summary>
    public const string Opaque = "opaque";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator SignatureNote(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.SignatureReason: why a verdict is not valid.</summary>
[JsonConverter(typeof(StringWireValueConverter<SignatureReason>))]
public readonly record struct SignatureReason(string Value) : IWireEnumeration<SignatureReason>
{
    /// <summary>The content changed after signing.</summary>
    public const string DigestMismatch = "digestMismatch";

    /// <summary>The signature does not verify with the key.</summary>
    public const string BadSignature = "badSignature";

    /// <summary>The signature's structure is broken.</summary>
    public const string Malformed = "malformed";

    /// <summary>Signed with MD5.</summary>
    public const string Md5 = "md5";

    /// <summary>The certificate is not for e-mail signing.</summary>
    public const string KeyUsage = "keyUsage";

    /// <summary>The chain leads to no system root.</summary>
    public const string UnknownIssuer = "unknownIssuer";

    /// <summary>A self-signed certificate.</summary>
    public const string SelfSigned = "selfSigned";

    /// <summary>The certificate was not valid yet at arrival.</summary>
    public const string NotYetValid = "notYetValid";

    /// <summary>The certificate or key had expired at arrival.</summary>
    public const string Expired = "expired";

    /// <summary>SHA-1 digest.</summary>
    public const string Sha1 = "sha1";

    /// <summary>RSA shorter than 2048 bits.</summary>
    public const string ShortKey = "shortKey";

    /// <summary>DSA or another algorithm not verified.</summary>
    public const string Algorithm = "algorithm";

    /// <summary>More than one signer.</summary>
    public const string MultipleSigners = "multipleSigners";

    /// <summary>The server rebuilt the MIME (Exchange via Graph).</summary>
    public const string Rebuilt = "rebuilt";

    /// <summary>Another shape not verified.</summary>
    public const string Format = "format";

    /// <summary>Over a limit of the verifier.</summary>
    public const string Limit = "limit";

    /// <summary>The verifier failed.</summary>
    public const string Internal = "internal";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator SignatureReason(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.ProtectedHeaderName: a protected header (RFC 9788) that differs from the outer one.</summary>
[JsonConverter(typeof(StringWireValueConverter<ProtectedHeaderName>))]
public readonly record struct ProtectedHeaderName(string Value) : IWireEnumeration<ProtectedHeaderName>
{
    /// <summary>Subject.</summary>
    public const string Subject = "subject";

    /// <summary>From.</summary>
    public const string From = "from";

    /// <summary>To.</summary>
    public const string To = "to";

    /// <summary>Cc.</summary>
    public const string Cc = "cc";

    /// <summary>Reply-To.</summary>
    public const string ReplyTo = "replyTo";

    /// <summary>Date.</summary>
    public const string Date = "date";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator ProtectedHeaderName(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.RevocationState: what is known of a certificate's revocation.</summary>
[JsonConverter(typeof(StringWireValueConverter<RevocationState>))]
public readonly record struct RevocationState(string Value) : IWireEnumeration<RevocationState>
{
    /// <summary>Never checked.</summary>
    public const string Unchecked = "unchecked";

    /// <summary>Not revoked when checked.</summary>
    public const string Good = "good";

    /// <summary>Revoked.</summary>
    public const string Revoked = "revoked";

    /// <summary>The responder does not know the certificate.</summary>
    public const string Unknown = "unknown";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator RevocationState(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.RevocationSource: where the last check got its answer.</summary>
[JsonConverter(typeof(StringWireValueConverter<RevocationSource>))]
public readonly record struct RevocationSource(string Value) : IWireEnumeration<RevocationSource>
{
    /// <summary>An OCSP responder.</summary>
    public const string Ocsp = "ocsp";

    /// <summary>A certificate revocation list.</summary>
    public const string Crl = "crl";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator RevocationSource(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.PGPKeyTrust: how far an OpenPGP key is trusted.</summary>
[JsonConverter(typeof(StringWireValueConverter<PgpKeyTrust>))]
public readonly record struct PgpKeyTrust(string Value) : IWireEnumeration<PgpKeyTrust>
{
    /// <summary>The key is not stored (details only).</summary>
    public const string Unknown = "unknown";

    /// <summary>Found automatically or imported, not confirmed.</summary>
    public const string Seen = "seen";

    /// <summary>The user confirmed its fingerprint.</summary>
    public const string Verified = "verified";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator PgpKeyTrust(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.PGPKeySource: where a stored key came from.</summary>
[JsonConverter(typeof(StringWireValueConverter<PgpKeySource>))]
public readonly record struct PgpKeySource(string Value) : IWireEnumeration<PgpKeySource>
{
    /// <summary>An Autocrypt header of received mail.</summary>
    public const string Autocrypt = "autocrypt";

    /// <summary>A key attached to a message, imported on request.</summary>
    public const string Attached = "attached";

    /// <summary>Imported in Preferences (pasted or a file).</summary>
    public const string Import = "import";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator PgpKeySource(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.RevocationFailure: the class of a failed revocation check.</summary>
[JsonConverter(typeof(StringWireValueConverter<RevocationFailure>))]
public readonly record struct RevocationFailure(string Value) : IWireEnumeration<RevocationFailure>
{
    /// <summary>Could not connect.</summary>
    public const string Network = "network";

    /// <summary>No answer within api.RevocationTimeout.</summary>
    public const string Timeout = "timeout";

    /// <summary>The responder answered an error.</summary>
    public const string Server = "server";

    /// <summary>An answer that is unsigned, for another certificate, too large or unreadable.</summary>
    public const string Response = "response";

    /// <summary>The URL is not one the daemon contacts.</summary>
    public const string Address = "address";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator RevocationFailure(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.PGPImportFailure: why <c>pgp.keyImport</c> refused the data.</summary>
[JsonConverter(typeof(StringWireValueConverter<PgpImportFailure>))]
public readonly record struct PgpImportFailure(string Value) : IWireEnumeration<PgpImportFailure>
{
    /// <summary>The data holds no public key.</summary>
    public const string NoKey = "noKey";

    /// <summary>The data cannot be read.</summary>
    public const string Malformed = "malformed";

    /// <summary>It holds a secret key; nothing is stored.</summary>
    public const string SecretKey = "secretKey";

    /// <summary>Over the key size or key count limit.</summary>
    public const string TooLarge = "tooLarge";

    /// <summary>The stored keys' limit is reached.</summary>
    public const string TooMany = "tooMany";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator PgpImportFailure(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>
/// api.MessageSignature: <see cref="MessageSummary.Signature"/>, what the
/// list's icon needs. Absent for a message without a signature that is the
/// verdict of the whole message, and while the verdict is pending.
/// </summary>
public sealed record MessageSignature
{
    /// <summary>smime or pgp.</summary>
    [JsonPropertyName("kind")]
    public required SignatureKind Kind { get; init; }

    /// <summary>The verdict.</summary>
    [JsonPropertyName("status")]
    public required SignatureStatus Status { get; init; }
}

/// <summary>
/// api.SignatureSigner: who signed, from the certificate (S/MIME) or the
/// key's user ID that matched (OpenPGP), never from the message's headers.
/// Untrusted display text.
/// </summary>
public sealed record SignatureSigner
{
    /// <summary>The name; null when there is none.</summary>
    [JsonPropertyName("name")]
    public string? Name { get; init; }

    /// <summary>The address; null when there is none.</summary>
    [JsonPropertyName("email")]
    public string? Email { get; init; }

    /// <summary>
    /// The SHA-256 of the signer's certificate (64 lowercase hex digits) or
    /// the fingerprint of the signing key's primary key (40 or 64); null when
    /// the key is unknown.
    /// </summary>
    [JsonPropertyName("fingerprint")]
    public string? Fingerprint { get; init; }
}

/// <summary>
/// api.SignatureSummary: <see cref="Message.Signature"/> (message.get,
/// message.download), what the header's badge needs. It takes the place of
/// <see cref="MessageSummary.Signature"/> on the wire.
/// </summary>
public record SignatureSummary
{
    /// <summary>smime or pgp.</summary>
    [JsonPropertyName("kind")]
    public required SignatureKind Kind { get; init; }

    /// <summary>The verdict.</summary>
    [JsonPropertyName("status")]
    public required SignatureStatus Status { get; init; }

    /// <summary>Present for valid, validSeen, mismatch, untrusted, weak and revoked.</summary>
    [JsonPropertyName("signer")]
    public SignatureSigner? Signer { get; init; }

    /// <summary>The notes; absent or null reads as empty.</summary>
    [JsonPropertyName("notes")]
    [JsonConverter(typeof(NullAsEmptyListConverter<SignatureNote>))]
    public IReadOnlyList<SignatureNote> Notes { get; init => field = value ?? []; } = [];

    /// <summary>
    /// The detached signature part of a multipart/signed message: clients do
    /// not show it among the attachments. Null for opaque S/MIME and inline
    /// OpenPGP.
    /// </summary>
    [JsonPropertyName("partId")]
    public string? PartId { get; init => field = string.IsNullOrEmpty(value) ? null : value; }
}

/// <summary>api.ProtectedHeader: a signed header whose value differs from the outer one. Untrusted text, one line.</summary>
public sealed record ProtectedHeader
{
    /// <summary>The header.</summary>
    [JsonPropertyName("name")]
    public required ProtectedHeaderName Name { get; init; }

    /// <summary>The signed value.</summary>
    [JsonPropertyName("value")]
    public required string Value { get; init; }
}

/// <summary>
/// api.SignatureDetails: what <c>message.signature</c> returns for the
/// details dialog: the summary plus everything the dialog shows.
/// </summary>
public sealed record SignatureDetails : SignatureSummary
{
    /// <summary>The message.</summary>
    [JsonPropertyName("messageId")]
    public required MessageId MessageId { get; init; }

    /// <summary>Why the verdict is not valid; null for valid, validSeen, mismatch, unknownKey and revoked.</summary>
    [JsonPropertyName("reason")]
    public SignatureReason? Reason { get; init; }

    /// <summary>The signing time the signer declared: the signer's claim, never used to judge validity.</summary>
    [JsonPropertyName("signedAt")]
    public DateTimeOffset? SignedAt { get; init; }

    /// <summary>When the message arrived (internal date): validity is judged at this time.</summary>
    [JsonPropertyName("arrivedAt")]
    public DateTimeOffset ArrivedAt { get; init; }

    /// <summary>A language-neutral technical description, public key then digest ("RSA 2048 / SHA-256"); shown as it is.</summary>
    [JsonPropertyName("algorithm")]
    public string? Algorithm { get; init; }

    /// <summary>When the certificate or key expired.</summary>
    [JsonPropertyName("expiredAt")]
    public DateTimeOffset? ExpiredAt { get; init; }

    /// <summary>When the certificate or key was revoked.</summary>
    [JsonPropertyName("revokedAt")]
    public DateTimeOffset? RevokedAt { get; init; }

    /// <summary>The signed headers that differ from the outer ones, at most <see cref="API.Limits.MaxProtectedHeaders"/>.</summary>
    [JsonPropertyName("protectedHeaders")]
    [JsonConverter(typeof(NullAsEmptyListConverter<ProtectedHeader>))]
    public IReadOnlyList<ProtectedHeader> ProtectedHeaders { get; init => field = value ?? []; } = [];

    /// <summary>Set for kind smime.</summary>
    [JsonPropertyName("smime")]
    public SmimeDetails? Smime { get; init; }

    /// <summary>Set for kind pgp.</summary>
    [JsonPropertyName("pgp")]
    public PgpDetails? Pgp { get; init; }
}

/// <summary>api.SignatureCertificate: one certificate of an S/MIME chain. Every string is untrusted text.</summary>
public sealed record SignatureCertificate
{
    /// <summary>64 lowercase hex digits of the DER encoding.</summary>
    [JsonPropertyName("sha256")]
    public required string Sha256 { get; init; }

    /// <summary>The subject.</summary>
    [JsonPropertyName("subject")]
    public string? Subject { get; init; }

    /// <summary>The issuer.</summary>
    [JsonPropertyName("issuer")]
    public string? Issuer { get; init; }

    /// <summary>The e-mail addresses (rfc822Name, else emailAddress of the DN).</summary>
    [JsonPropertyName("emails")]
    [JsonConverter(typeof(NullAsEmptyListConverter<string>))]
    public IReadOnlyList<string> Emails { get; init => field = value ?? []; } = [];

    /// <summary>Valid from.</summary>
    [JsonPropertyName("notBefore")]
    public DateTimeOffset NotBefore { get; init; }

    /// <summary>Valid to.</summary>
    [JsonPropertyName("notAfter")]
    public DateTimeOffset NotAfter { get; init; }

    /// <summary>The certificate is a root of the system's trust store.</summary>
    [JsonPropertyName("systemRoot")]
    public bool SystemRoot { get; init; }

    /// <summary>The certificate is self-signed.</summary>
    [JsonPropertyName("selfSigned")]
    public bool SelfSigned { get; init; }
}

/// <summary>api.SMIMERevocation: the revocation state of the signer's certificate.</summary>
public sealed record SmimeRevocation
{
    /// <summary>What is known.</summary>
    [JsonPropertyName("state")]
    public required RevocationState State { get; init; }

    /// <summary>When it was last checked.</summary>
    [JsonPropertyName("checkedAt")]
    public DateTimeOffset? CheckedAt { get; init; }

    /// <summary>When it was revoked.</summary>
    [JsonPropertyName("revokedAt")]
    public DateTimeOffset? RevokedAt { get; init; }

    /// <summary>Where the last check got its answer.</summary>
    [JsonPropertyName("source")]
    public RevocationSource? Source { get; init; }

    /// <summary>The certificate names an http or https OCSP responder or CRL point, so <c>message.signatureRevocation</c> can check.</summary>
    [JsonPropertyName("checkable")]
    public bool Checkable { get; init; }
}

/// <summary>api.SMIMEDetails: the S/MIME parts of <see cref="SignatureDetails"/>.</summary>
public sealed record SmimeDetails
{
    /// <summary>The signer's certificate first, then each issuer as far as it was found, at most <see cref="API.Limits.MaxSignatureChain"/>; empty without the signer's certificate.</summary>
    [JsonPropertyName("chain")]
    [JsonConverter(typeof(NullAsEmptyListConverter<SignatureCertificate>))]
    public IReadOnlyList<SignatureCertificate> Chain { get; init => field = value ?? []; } = [];

    /// <summary>The signer's certificate's revocation state.</summary>
    [JsonPropertyName("revocation")]
    public required SmimeRevocation Revocation { get; init; }
}

/// <summary>api.PGPKeyOffer: a key the message carries that is not stored; passed to <c>pgp.keyImport</c> as it is.</summary>
public sealed record PgpKeyOffer
{
    /// <summary>The offered key's fingerprint.</summary>
    [JsonPropertyName("fingerprint")]
    public required string Fingerprint { get; init; }

    /// <summary>The attached key's part; exactly one of this and <see cref="Autocrypt"/> is set.</summary>
    [JsonPropertyName("partId")]
    public string? PartId { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The message's Autocrypt header.</summary>
    [JsonPropertyName("autocrypt")]
    public bool Autocrypt { get; init; }
}

/// <summary>api.PGPDetails: the OpenPGP parts of <see cref="SignatureDetails"/>.</summary>
public sealed record PgpDetails
{
    /// <summary>How far the key is trusted.</summary>
    [JsonPropertyName("keyTrust")]
    public required PgpKeyTrust KeyTrust { get; init; }

    /// <summary>The signing (sub)key's 16 lowercase hex digits.</summary>
    [JsonPropertyName("keyId")]
    public string? KeyId { get; init; }

    /// <summary>The stored key's primary fingerprint (what <c>pgp.keyVerify</c> takes); null for an unknown key.</summary>
    [JsonPropertyName("primaryFingerprint")]
    public string? PrimaryFingerprint { get; init; }

    /// <summary>The key's user IDs, untrusted text.</summary>
    [JsonPropertyName("userIds")]
    [JsonConverter(typeof(NullAsEmptyListConverter<string>))]
    public IReadOnlyList<string> UserIds { get; init => field = value ?? []; } = [];

    /// <summary>Where the stored key came from.</summary>
    [JsonPropertyName("keySource")]
    public PgpKeySource? KeySource { get; init; }

    /// <summary>When the key was first seen.</summary>
    [JsonPropertyName("firstSeen")]
    public DateTimeOffset? FirstSeen { get; init; }

    /// <summary>The key seen earlier for the signer's address.</summary>
    [JsonPropertyName("previousFingerprint")]
    public string? PreviousFingerprint { get; init; }

    /// <summary>A key of the message that is not stored yet.</summary>
    [JsonPropertyName("importable")]
    public PgpKeyOffer? Importable { get; init; }
}

/// <summary>api.PGPKey: a stored public key (never a secret one).</summary>
public sealed record PgpKey
{
    /// <summary>The primary key's fingerprint, 40 or 64 lowercase hex digits.</summary>
    [JsonPropertyName("fingerprint")]
    public required string Fingerprint { get; init; }

    /// <summary>The user IDs, untrusted text.</summary>
    [JsonPropertyName("userIds")]
    [JsonConverter(typeof(NullAsEmptyListConverter<string>))]
    public IReadOnlyList<string> UserIds { get; init => field = value ?? []; } = [];

    /// <summary>Their addresses.</summary>
    [JsonPropertyName("emails")]
    [JsonConverter(typeof(NullAsEmptyListConverter<string>))]
    public IReadOnlyList<string> Emails { get; init => field = value ?? []; } = [];

    /// <summary>seen or verified.</summary>
    [JsonPropertyName("trust")]
    public required PgpKeyTrust Trust { get; init; }

    /// <summary>Where it came from.</summary>
    [JsonPropertyName("source")]
    public required PgpKeySource Source { get; init; }

    /// <summary>When it was first seen.</summary>
    [JsonPropertyName("firstSeen")]
    public DateTimeOffset FirstSeen { get; init; }

    /// <summary>When the key was created.</summary>
    [JsonPropertyName("createdAt")]
    public DateTimeOffset CreatedAt { get; init; }

    /// <summary>When it expires.</summary>
    [JsonPropertyName("expiresAt")]
    public DateTimeOffset? ExpiresAt { get; init; }

    /// <summary>When it was revoked.</summary>
    [JsonPropertyName("revokedAt")]
    public DateTimeOffset? RevokedAt { get; init; }

    /// <summary>Describes the primary key ("Ed25519", "RSA 4096").</summary>
    [JsonPropertyName("algorithm")]
    public string? Algorithm { get; init; }
}

/// <summary>api.MessageSignatureParams.</summary>
public sealed record MessageSignatureParams
{
    /// <summary>The message.</summary>
    [JsonPropertyName("messageId")]
    public required MessageId MessageId { get; init; }
}

/// <summary>
/// api.MessageSignatureResult: the result of <c>message.signature</c> and
/// <c>message.signatureRevocation</c>. Null for a message without a verdict.
/// </summary>
public sealed record MessageSignatureResult
{
    /// <summary>The details.</summary>
    [JsonPropertyName("signature")]
    public SignatureDetails? Signature { get; init; }
}

/// <summary>api.MessageSignatureRevocationParams.</summary>
public sealed record MessageSignatureRevocationParams
{
    /// <summary>The message.</summary>
    [JsonPropertyName("messageId")]
    public required MessageId MessageId { get; init; }
}

/// <summary>api.RevocationFailedData: the <c>data</c> of <see cref="ErrorCode.RevocationFailed"/>.</summary>
public sealed record RevocationFailedData
{
    /// <summary>The class of the failure.</summary>
    [JsonPropertyName("reason")]
    public required RevocationFailure Reason { get; init; }
}

/// <summary>
/// api.PGPKeyImportParams: exactly one of <see cref="MessageId"/> with
/// <see cref="PartId"/> (an attached key), <see cref="MessageId"/> with
/// <see cref="Autocrypt"/>, <see cref="Armored"/> or <see cref="Data"/>.
/// </summary>
public sealed record PgpKeyImportParams
{
    /// <summary>The message of an attached or Autocrypt key.</summary>
    [JsonPropertyName("messageId")]
    public MessageId? MessageId { get; init; }

    /// <summary>The attached key's part.</summary>
    [JsonPropertyName("partId")]
    public string? PartId { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The message's Autocrypt header; false is written as absent.</summary>
    [JsonPropertyName("autocrypt")]
    public bool? Autocrypt { get; init => field = value == true ? true : null; }

    /// <summary>ASCII armour.</summary>
    [JsonPropertyName("armored")]
    public string? Armored { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>A binary key (base64 on the wire).</summary>
    [JsonPropertyName("data")]
    public byte[]? Data { get; init => field = value is { Length: > 0 } ? value : null; }
}

/// <summary>api.PGPKeyImportResult: the keys of the data as stored after the import.</summary>
public sealed record PgpKeyImportResult
{
    /// <summary>The keys.</summary>
    [JsonPropertyName("keys")]
    [JsonConverter(typeof(NullAsEmptyListConverter<PgpKey>))]
    public IReadOnlyList<PgpKey> Keys { get; init => field = value ?? []; } = [];

    /// <summary>Keys that were not stored.</summary>
    [JsonPropertyName("added")]
    public int Added { get; init; }

    /// <summary>Stored keys that gained something.</summary>
    [JsonPropertyName("updated")]
    public int Updated { get; init; }
}

/// <summary>api.PGPImportErrorData: the <c>data</c> of invalidArgument from <c>pgp.keyImport</c> when the data was refused.</summary>
public sealed record PgpImportErrorData
{
    /// <summary>Why it was refused.</summary>
    [JsonPropertyName("reason")]
    public required PgpImportFailure Reason { get; init; }
}

/// <summary>api.PGPKeyVerifyParams: <see cref="Verified"/> true confirms the key, false takes the confirmation back.</summary>
public sealed record PgpKeyVerifyParams
{
    /// <summary>The stored key's fingerprint.</summary>
    [JsonPropertyName("fingerprint")]
    public required string Fingerprint { get; init; }

    /// <summary>Confirmed or not; always written.</summary>
    [JsonPropertyName("verified")]
    public bool Verified { get; init; }
}

/// <summary>api.PGPKeyVerifyResult.</summary>
public sealed record PgpKeyVerifyResult
{
    /// <summary>The key.</summary>
    [JsonPropertyName("key")]
    public required PgpKey Key { get; init; }
}

/// <summary>api.PGPKeyListResult: every stored key, sorted by the first e-mail address, then the fingerprint.</summary>
public sealed record PgpKeyListResult
{
    /// <summary>The keys.</summary>
    [JsonPropertyName("keys")]
    [JsonConverter(typeof(NullAsEmptyListConverter<PgpKey>))]
    public IReadOnlyList<PgpKey> Keys { get; init => field = value ?? []; } = [];
}

/// <summary>api.PGPKeyRemoveParams.</summary>
public sealed record PgpKeyRemoveParams
{
    /// <summary>The stored key's fingerprint.</summary>
    [JsonPropertyName("fingerprint")]
    public required string Fingerprint { get; init; }
}
