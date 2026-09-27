// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/API/TLS.swift; Go: backend/pkg/api/tls.go
// (TLSErrorReason, CertificateInfo, TLSErrorData, TLSErrorDataOf,
// NormalizeCertificateSHA256); contract: docs/api.md §2 tlsError, §4.1
// ServerConfig.certificateSha256.
//
// Swift's free functions tlsErrorData(_:) and normalizeCertificateSHA256(_:)
// are the static methods of Tls.

using System;
using System.Collections.Generic;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

/// <summary>
/// api.TLSErrorReason: why a TLS connection to an IMAP/SMTP endpoint failed
/// (the <c>reason</c> of a <c>tlsError</c>'s data). A value a newer daemon
/// adds decodes as itself; the wizard's certificate rules make it
/// <c>other</c>, as the contract asks.
/// </summary>
[JsonConverter(typeof(StringWireValueConverter<TlsErrorReason>))]
public readonly record struct TlsErrorReason(string Value) : IWireEnumeration<TlsErrorReason>
{
    /// <summary>The issuer is not trusted (self-signed or a private CA).</summary>
    public const string Untrusted = "untrusted";

    /// <summary>The certificate is for another name.</summary>
    public const string HostnameMismatch = "hostnameMismatch";

    /// <summary>Past notAfter.</summary>
    public const string Expired = "expired";

    /// <summary>Before notBefore.</summary>
    public const string NotYetValid = "notYetValid";

    /// <summary>Otherwise malformed or not allowed for a server.</summary>
    public const string Invalid = "invalid";

    /// <summary>Refused by the system verifier for another reason (macOS "not standards compliant").</summary>
    public const string Other = "other";

    /// <summary>The protocol failed before a certificate was judged.</summary>
    public const string Handshake = "handshake";

    /// <summary>STARTTLS is configured but not offered or refused.</summary>
    public const string StarttlsUnavailable = "starttlsUnavailable";

    /// <summary>The server demands TLS before login.</summary>
    public const string TlsRequired = "tlsRequired";

    /// <summary>Not the certificate pinned in <see cref="ServerConfig.CertificateSha256"/>.</summary>
    public const string PinMismatch = "pinMismatch";

    /// <summary>Every reason of the contract (protocol version 2).</summary>
    public static IReadOnlyList<TlsErrorReason> All { get; } =
    [
        Untrusted, HostnameMismatch, Expired, NotYetValid, Invalid, Other,
        Handshake, StarttlsUnavailable, TlsRequired, PinMismatch,
    ];

    /// <summary>The value of a wire string.</summary>
    public static implicit operator TlsErrorReason(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>
/// api.CertificateInfo: the server's leaf certificate. Every string is
/// untrusted text from the server (the daemon removes control and format
/// characters and caps each at 128 bytes, lists at 8 entries; the client
/// cleans them again before showing them). Decoded as Go's encoding/json
/// would: a missing or null member is its zero value, a mistyped one fails
/// the whole certificate.
/// </summary>
public sealed record CertificateInfo
{
    /// <summary>64 lowercase hex digits of the SHA-256 of the DER encoding.</summary>
    [JsonPropertyName("sha256")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Sha256 { get; init => field = value ?? ""; } = "";

    /// <summary>The subject.</summary>
    [JsonPropertyName("subject")]
    public string? Subject { get; init; }

    /// <summary>The issuer.</summary>
    [JsonPropertyName("issuer")]
    public string? Issuer { get; init; }

    /// <summary>The DNS names the certificate is for.</summary>
    [JsonPropertyName("dnsNames")]
    [JsonConverter(typeof(NullAsEmptyListConverter<string>))]
    public IReadOnlyList<string> DnsNames { get; init => field = value ?? []; } = [];

    /// <summary>The IP addresses the certificate is for.</summary>
    [JsonPropertyName("ipAddresses")]
    [JsonConverter(typeof(NullAsEmptyListConverter<string>))]
    public IReadOnlyList<string> IpAddresses { get; init => field = value ?? []; } = [];

    /// <summary>The start of its validity.</summary>
    [JsonPropertyName("notBefore")]
    [JsonConverter(typeof(NullAsGoZeroConverter))]
    public DateTimeOffset NotBefore { get; init; } = DateTimeOffset.GoZero;

    /// <summary>The end of its validity.</summary>
    [JsonPropertyName("notAfter")]
    [JsonConverter(typeof(NullAsGoZeroConverter))]
    public DateTimeOffset NotAfter { get; init; } = DateTimeOffset.GoZero;

    /// <summary>Issued by itself.</summary>
    [JsonPropertyName("selfSigned")]
    [JsonConverter(typeof(NullAsFalseConverter))]
    public bool SelfSigned { get; init; }
}

/// <summary>
/// api.TLSErrorData: <c>error.data</c> of a <c>tlsError</c> from an IMAP/SMTP
/// endpoint. <see cref="Certificate"/> is set when the server presented one,
/// <see cref="ExpectedSha256"/> only for <c>pinMismatch</c>.
/// </summary>
public sealed record TlsErrorData
{
    /// <summary>Why the connection failed.</summary>
    [JsonPropertyName("reason")]
    public required TlsErrorReason Reason { get; init; }

    /// <summary>The server's certificate, when it presented one.</summary>
    [JsonPropertyName("certificate")]
    public CertificateInfo? Certificate { get; init; }

    /// <summary>The pinned fingerprint, for <c>pinMismatch</c>.</summary>
    [JsonPropertyName("expectedSha256")]
    public string? ExpectedSha256 { get; init; }
}

/// <summary>The functions of TLS.swift (tls.go).</summary>
public static class Tls
{
    /// <summary>
    /// api.TLSErrorDataOf (Swift <c>tlsErrorData(_:)</c>): the TLS details of
    /// <paramref name="e"/>, decoded from its data; null when it is not a
    /// <c>tlsError</c>, carries no details or details of another shape (no
    /// <c>reason</c>). The strings are as the daemon sent them; what is shown
    /// goes through the wizard's certificate rules.
    /// </summary>
    public static TlsErrorData? TlsErrorData(RpcError? e)
    {
        if (e is null || e.Code != ErrorCode.TlsError || e.Data is not { ValueKind: JsonValueKind.Object } data)
        {
            return null;
        }
        try
        {
            var d = data.Deserialize(ApiJsonContext.Wire.TlsErrorData);
            return d is null || string.IsNullOrEmpty(d.Reason.Value) ? null : d;
        }
        catch (JsonException)
        {
            return null;
        }
    }

    /// <summary>
    /// api.NormalizeCertificateSHA256: a SHA-256 fingerprint as 64 hex digits,
    /// optionally separated by colons or spaces and in any case, as 64
    /// lowercase hex digits; null for anything else.
    /// </summary>
    public static string? NormalizeCertificateSha256(string s)
    {
        ArgumentNullException.ThrowIfNull(s);
        var output = new StringBuilder(64);
        foreach (var c in s)
        {
            switch (c)
            {
                case ':' or ' ':
                    continue;
                case (>= '0' and <= '9') or (>= 'a' and <= 'f'):
                    output.Append(c);
                    break;
                case >= 'A' and <= 'F':
                    output.Append((char)(c + ('a' - 'A')));
                    break;
                default:
                    return null;
            }
        }
        return output.Length == 64 ? output.ToString() : null;
    }
}
