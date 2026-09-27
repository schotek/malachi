// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Wizard/CertTrust.swift; GTK:
// ui/internal/certtrust/certtrust.go (Problem, Category, CategoryOf,
// NormalizeReason, Details, cleanCertificate, cleanText, cleanList,
// FormatFingerprint, Pinnable, Trustable, SameCertificate, FromSyncState,
// KeepPin, ServerName, IssuedTo).
//
// The certificate of a failed TLS connection, cleaned for display, and the
// rules for trusting it by pinning its SHA-256 fingerprint to an IMAP/SMTP
// endpoint (docs/security.md §7): ServerConfig.CertificateSha256, never with
// security none or authMethod oauth2, and a changed host or port drops the
// pin. No texts: the sentences are the wizard's and the window's. Strings
// are compared as Go compares them: ordinally, case folded by
// ToLowerInvariant, with no canonical equivalence (a composed and a
// decomposed name differ).

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Text;
using Malachi.Core.Api;

namespace Malachi.Core.Wizard;

/// <summary>The certtrust package: its Go names map 1:1 (<c>certtrust.Details</c> → <c>CertTrust.Details</c>).</summary>
public static class CertTrust
{
    /// <summary>certtrust.maxTextBytes: the longest certificate string shown, in UTF-8 bytes (the daemon's own cap).</summary>
    public const int MaxText = 128;

    /// <summary>certtrust.maxListItems: the most names of a certificate shown.</summary>
    public const int MaxList = 8;

    /// <summary>certtrust.Category: what kind of TLS failure a reason is.</summary>
    public enum Category
    {
        /// <summary>
        /// The connection could not be secured before a certificate was
        /// judged: handshake, starttlsUnavailable, tlsRequired.
        /// </summary>
        Connection,

        /// <summary>
        /// The certificate was refused: untrusted, hostnameMismatch, expired,
        /// notYetValid, invalid, other, and any unknown reason.
        /// </summary>
        Certificate,

        /// <summary>Not the pinned certificate: pinMismatch.</summary>
        Changed,
    }

    /// <summary>certtrust.CategoryOf: sorts a reason; unknown reasons count as <c>other</c>.</summary>
    public static Category CategoryOf(TlsErrorReason reason) => NormalizeReason(reason).Value switch
    {
        TlsErrorReason.Handshake or TlsErrorReason.StarttlsUnavailable or TlsErrorReason.TlsRequired => Category.Connection,
        TlsErrorReason.PinMismatch => Category.Changed,
        _ => Category.Certificate,
    };

    /// <summary>
    /// certtrust.NormalizeReason: <paramref name="r"/> when it is a reason of
    /// docs/api.md §2, <c>other</c> for anything else, the empty reason
    /// included.
    /// </summary>
    public static TlsErrorReason NormalizeReason(TlsErrorReason r) =>
        TlsErrorReason.All.Contains(r) ? r : TlsErrorReason.Other;

    /// <summary>
    /// certtrust.Details: the TLS details of <paramref name="e"/> with every
    /// string cleaned again for display (the daemon's cleaning is not relied
    /// on): control and format characters removed, trimmed, at most
    /// <see cref="MaxText"/> bytes, lists of at most <see cref="MaxList"/>
    /// non-empty entries. A certificate whose fingerprint is not 64 hex digits
    /// is dropped whole; so is such an expected fingerprint. Null when
    /// <paramref name="e"/> is not a <c>tlsError</c>, has no details or no
    /// reason.
    /// </summary>
    public static Problem? Details(RpcError? e)
    {
        if (Tls.TlsErrorData(e) is not { } raw || string.IsNullOrEmpty(raw.Reason.Value))
        {
            return null;
        }
        return new Problem
        {
            Reason = NormalizeReason(raw.Reason),
            Cert = CleanCertificate(raw.Certificate),
            Expected = Tls.NormalizeCertificateSha256(raw.ExpectedSha256 ?? "") ?? "",
        };
    }

    /// <summary>
    /// certtrust.cleanText: <paramref name="s"/> without the replacement
    /// character (what invalid UTF-8 or a lone surrogate became), control
    /// characters (Cc, newlines included), format characters (Cf, such as
    /// U+202E, which reverses what follows) and the line and paragraph
    /// separators (Zl, Zp), trimmed of White_Space (strings.TrimSpace) and cut
    /// to <see cref="MaxText"/> bytes on a character boundary, then trimmed
    /// again.
    /// </summary>
    public static string CleanText(string s)
    {
        ArgumentNullException.ThrowIfNull(s);
        var kept = new StringBuilder(s.Length);
        foreach (var r in s.EnumerateRunes())
        {
            if (r.Value == 0xFFFD)
            {
                continue;
            }
            switch (Rune.GetUnicodeCategory(r))
            {
                case UnicodeCategory.Control or UnicodeCategory.Format
                    or UnicodeCategory.LineSeparator or UnicodeCategory.ParagraphSeparator:
                    continue;
                default:
                    kept.Append(r.ToString());
                    break;
            }
        }
        var output = kept.ToString().Trim();
        if (Encoding.UTF8.GetByteCount(output) <= MaxText)
        {
            return output;
        }
        var cut = new StringBuilder();
        var bytes = 0;
        foreach (var r in output.EnumerateRunes())
        {
            if (bytes + r.Utf8SequenceLength > MaxText)
            {
                break;
            }
            bytes += r.Utf8SequenceLength;
            cut.Append(r.ToString());
        }
        return cut.ToString().Trim();
    }

    /// <summary>certtrust.cleanList: the cleaned, non-empty entries, at most <see cref="MaxList"/> of them.</summary>
    public static IReadOnlyList<string> CleanList(IEnumerable<string> list)
    {
        ArgumentNullException.ThrowIfNull(list);
        var output = new List<string>();
        foreach (var s in list)
        {
            if (output.Count == MaxList)
            {
                break;
            }
            var c = CleanText(s ?? "");
            if (c.Length > 0)
            {
                output.Add(c);
            }
        }
        return output;
    }

    /// <summary>
    /// certtrust.FormatFingerprint: a SHA-256 fingerprint as upper-case hex in
    /// 16 groups of four joined by spaces ("AB12 CD34 …"), as it is compared
    /// by eye; "" for anything that is not a fingerprint.
    /// </summary>
    public static string FormatFingerprint(string hex)
    {
        ArgumentNullException.ThrowIfNull(hex);
        if (Tls.NormalizeCertificateSha256(hex) is not { } norm)
        {
            return "";
        }
        var upper = norm.ToUpperInvariant();
        var b = new StringBuilder(upper.Length + 15);
        for (var i = 0; i < upper.Length; i += 4)
        {
            if (i > 0)
            {
                b.Append(' ');
            }
            b.Append(upper, i, 4);
        }
        return b.ToString();
    }

    /// <summary>
    /// certtrust.Pinnable: the endpoint may carry a pin at all: TLS or
    /// STARTTLS with a password (never plaintext, and the tokens of an oauth2
    /// endpoint never go to a pinned server).
    /// </summary>
    public static bool Pinnable(ServerConfig sc)
    {
        ArgumentNullException.ThrowIfNull(sc);
        return (sc.Security == Security.Tls || sc.Security == Security.Starttls) && sc.AuthMethod == AuthMethod.Password;
    }

    /// <summary>
    /// certtrust.Trustable: the problem of a failed endpoint of
    /// <c>account.test</c> whose certificate the user may trust: a certificate
    /// was presented and judged (not a connection failure), the endpoint may
    /// carry a pin, and it does not pin this very certificate already. A
    /// pinMismatch offers the new certificate. Null otherwise (and for Graph,
    /// which the wizard never asks about).
    /// </summary>
    public static Problem? Trustable(EndpointTestResult? res, ServerConfig sc)
    {
        ArgumentNullException.ThrowIfNull(sc);
        if (res is null || res.Ok || Details(res.Error) is not { Cert: { } cert } p || p.Category == Category.Connection
            || !Pinnable(sc))
        {
            return null;
        }
        return cert.Sha256 == Tls.NormalizeCertificateSha256(sc.CertificateSha256 ?? "") ? null : p;
    }

    /// <summary>
    /// certtrust.SameCertificate: both endpoints presented one and the same
    /// certificate (a mail bridge serving IMAP and SMTP): one confirmation
    /// pins both.
    /// </summary>
    public static bool SameCertificate(CertificateInfo? a, CertificateInfo? b) =>
        a is not null && b is not null
        && Tls.NormalizeCertificateSha256(a.Sha256) is { } x && Tls.NormalizeCertificateSha256(b.Sha256) is { } y
        && x == y;

    /// <summary>
    /// certtrust.FromSyncState: the certificate problem of an account: it is
    /// offline or failed and its last error is a <c>tlsError</c> about the
    /// certificate (with or without the certificate itself). Null for
    /// anything else, a connection failure included (that stays "Offline").
    /// </summary>
    public static Problem? FromSyncState(SyncState s)
    {
        ArgumentNullException.ThrowIfNull(s);
        if ((s.Status != SyncStatus.Offline && s.Status != SyncStatus.Error) || Details(s.Error) is not { } p
            || p.Category == Category.Connection)
        {
            return null;
        }
        return p;
    }

    /// <summary>
    /// certtrust.KeepPin: <paramref name="old"/>'s pin for
    /// <paramref name="cur"/> only while the host (trimmed, ignoring case, as
    /// DNS does) and the port are those it was trusted for and
    /// <paramref name="cur"/> may carry a pin; "" otherwise. The wizard keeps
    /// the configuration a pin was set for, so a changed host drops the pin
    /// and typing the old one again restores it; a pin never moves to another
    /// server.
    /// </summary>
    public static string KeepPin(ServerConfig old, ServerConfig cur)
    {
        ArgumentNullException.ThrowIfNull(old);
        ArgumentNullException.ThrowIfNull(cur);
        if (Tls.NormalizeCertificateSha256(old.CertificateSha256 ?? "") is not { } pin || !Pinnable(cur) || old.Port != cur.Port
            || FoldKey(old.Host.Trim()) != FoldKey(cur.Host.Trim()))
        {
            return "";
        }
        return pin;
    }

    /// <summary>
    /// certtrust.ServerName: "host:port" for the texts, an IPv6 literal in
    /// brackets (net.JoinHostPort).
    /// </summary>
    public static string ServerName(ServerConfig sc)
    {
        ArgumentNullException.ThrowIfNull(sc);
        var h = sc.Host.Trim();
        var port = sc.Port.ToString(CultureInfo.InvariantCulture);
        return h.Contains(':', StringComparison.Ordinal) ? "[" + h + "]:" + port : h + ":" + port;
    }

    /// <summary>
    /// certtrust.IssuedTo: whom the certificate was issued to: the subject on
    /// the first line, the DNS names and then the IP addresses on the second
    /// (joined with ", ", duplicates and the subject itself left out, ignoring
    /// case); "" when there is nothing.
    /// </summary>
    public static string IssuedTo(CertificateInfo c)
    {
        ArgumentNullException.ThrowIfNull(c);
        var lines = new List<string>();
        var subject = CleanText(c.Subject ?? "");
        if (subject.Length > 0)
        {
            lines.Add(subject);
        }
        var seen = new HashSet<string>(StringComparer.Ordinal) { FoldKey(subject) };
        var names = new List<string>();
        foreach (var n in CleanList(c.DnsNames).Concat(CleanList(c.IpAddresses)))
        {
            if (seen.Add(FoldKey(n)))
            {
                names.Add(n);
            }
        }
        if (names.Count > 0)
        {
            lines.Add(string.Join(", ", names));
        }
        return string.Join("\n", lines);
    }

    // certtrust.cleanCertificate: null without a valid fingerprint.
    private static CertificateInfo? CleanCertificate(CertificateInfo? c)
    {
        if (c is null || Tls.NormalizeCertificateSha256(c.Sha256) is not { } sum)
        {
            return null;
        }
        return new CertificateInfo
        {
            Sha256 = sum,
            Subject = NonEmpty(CleanText(c.Subject ?? "")),
            Issuer = NonEmpty(CleanText(c.Issuer ?? "")),
            DnsNames = CleanList(c.DnsNames),
            IpAddresses = CleanList(c.IpAddresses),
            NotBefore = c.NotBefore,
            NotAfter = c.NotAfter,
            SelfSigned = c.SelfSigned,
        };
    }

    // The key Go compares names by (strings.ToLower, strings.EqualFold):
    // case folded, no canonical equivalence.
    private static string FoldKey(string s) => s.ToLowerInvariant();

    // Go's "" as null.
    private static string? NonEmpty(string s) => s.Length == 0 ? null : s;

    /// <summary>
    /// certtrust.Problem: the cleaned details of a <c>tlsError</c>. Equality
    /// compares the certificate's lists by value, as Swift's == does.
    /// </summary>
    public sealed record Problem
    {
        /// <summary>Why the connection failed; a reason this client does not know is <c>other</c>.</summary>
        public required TlsErrorReason Reason { get; init; }

        /// <summary>
        /// The certificate the server presented; null when there was none or
        /// its fingerprint was not 64 hex digits.
        /// </summary>
        public CertificateInfo? Cert { get; init; }

        /// <summary>pinMismatch: the pinned fingerprint (64 lowercase hex); "" otherwise.</summary>
        public string Expected { get; init => field = value ?? ""; } = "";

        /// <summary>certtrust.Problem.Category.</summary>
        public Category Category => CategoryOf(Reason);

        /// <inheritdoc/>
        public bool Equals(Problem? other) =>
            other is not null && Reason == other.Reason && string.Equals(Expected, other.Expected, StringComparison.Ordinal)
            && SameInfo(Cert, other.Cert);

        /// <inheritdoc/>
        public override int GetHashCode() => HashCode.Combine(Reason, Expected, Cert?.Sha256);

        private static bool SameInfo(CertificateInfo? a, CertificateInfo? b) =>
            (a, b) switch
            {
                (null, null) => true,
                ({ } x, { } y) => x == (y with { DnsNames = x.DnsNames, IpAddresses = x.IpAddresses })
                    && x.DnsNames.SequenceEqual(y.DnsNames) && x.IpAddresses.SequenceEqual(y.IpAddresses),
                _ => false,
            };
    }
}
