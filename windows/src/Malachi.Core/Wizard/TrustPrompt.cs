// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Wizard/TrustPrompt.swift (TrustPrompt,
// trustPrompt, certificateDetails); GTK: ui/internal/accountwizard/trust.go
// (onTrust's texts, certificateDetails).
//
// The "Trust This Certificate?" confirmation of the account wizard: the
// texts and the certificate's details as plain strings the dialog shows as
// selectable, never interpreted, text. Swift's trustPrompt(...) is
// TrustPrompt.Create; the dates are formatted against the given now (the
// caller's TimeProvider), in the given culture and zone (the current ones
// when null).

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Text;

namespace Malachi.Core.Wizard;

/// <summary>The confirmation before a certificate is pinned: a destructive dialog with the details between the body and the buttons.</summary>
public sealed record TrustPrompt
{
    /// <summary>The heading.</summary>
    public required string Heading { get; init; }

    /// <summary>The body, naming the servers.</summary>
    public required string Body { get; init; }

    /// <summary>The certificate's details, in the order of the dialog.</summary>
    public IReadOnlyList<CertificateDetail> Details { get; init => field = value ?? []; } = [];

    /// <summary>The destructive button's label, with its mnemonic marker.</summary>
    public required string ConfirmLabel { get; init; }

    /// <summary>
    /// The confirmation for pinning <paramref name="cert"/> to
    /// <paramref name="servers"/> (<see cref="CertTrust.ServerName"/>, one, or
    /// two with IMAP first): the servers are named in the body, so the user
    /// sees what exactly will accept this certificate and nothing else.
    /// <paramref name="changed"/> (a target's certificate is not the one it
    /// pins, <see cref="CertTrust.Category.Changed"/>) asks about the new
    /// certificate instead, with a warning and the fingerprint trusted before
    /// (<paramref name="previous"/>, 64 hex digits, "" when unknown).
    /// </summary>
    public static TrustPrompt Create(
        CertificateInfo cert, IReadOnlyList<string> servers, DateTimeOffset now, bool changed = false, string previous = "",
        CultureInfo? culture = null, TimeZoneInfo? timeZone = null)
    {
        ArgumentNullException.ThrowIfNull(cert);
        ArgumentNullException.ThrowIfNull(servers);
        string named = servers.Count >= 2
            // TRANSLATORS: joins two servers ("host:port")
            ? L10n.T("%s and %s", servers[0], servers[1])
            : servers.Count == 1 ? servers[0] : "";
        if (changed)
        {
            return new TrustPrompt
            {
                // TRANSLATORS: dialog heading, the pinned certificate of a server has changed
                Heading = L10n.T("Trust the New Certificate?"),
                // TRANSLATORS: %s is one server ("host:port") or two joined by "%s and %s"
                Body = L10n.T(
                    "The certificate of %s has changed since you trusted it. If you did not renew it yourself, someone may be intercepting the connection. Trust the new certificate only if you know why it changed.",
                    named),
                Details = CertificateDetails(cert, now, previous, culture, timeZone),
                ConfirmLabel = L10n.T("_Trust"),
            };
        }
        return new TrustPrompt
        {
            // TRANSLATORS: dialog heading
            Heading = L10n.T("Trust This Certificate?"),
            // TRANSLATORS: %s is one server ("host:port") or two joined by "%s and %s"
            Body = L10n.T(
                "Only trust a certificate you expect, for example the one of a mail bridge on your own computer. Malachi Mail will then accept exactly this certificate for %s and no other, even if it is self-signed, issued for another name or expired.",
                named),
            Details = CertificateDetails(cert, now, "", culture, timeZone),
            // TRANSLATORS: destructive confirm button
            ConfirmLabel = L10n.T("_Trust"),
        };
    }

    /// <summary>
    /// trust.go certificateDetails: the certificate's details in the order of
    /// the dialog: the fingerprint first (nothing the server writes into its
    /// certificate can pose as it), the fingerprint trusted before
    /// (<paramref name="previous"/>, for a changed certificate), whom it was
    /// issued to (<see cref="CertTrust.IssuedTo"/>), by whom, the validity
    /// (left out when either date is unset), and "Self-signed" on a line of
    /// its own when it is. A line whose value is empty is left out.
    /// </summary>
    public static IReadOnlyList<CertificateDetail> CertificateDetails(
        CertificateInfo cert, DateTimeOffset now, string previous = "", CultureInfo? culture = null, TimeZoneInfo? timeZone = null)
    {
        ArgumentNullException.ThrowIfNull(cert);
        ArgumentNullException.ThrowIfNull(previous);
        var output = new List<CertificateDetail>();
        var fingerprint = CertTrust.FormatFingerprint(cert.Sha256);
        if (fingerprint.Length > 0)
        {
            output.Add(new CertificateDetail(L10n.C("certificate", "SHA-256 fingerprint"), fingerprint, Monospaced: true));
        }
        var before = CertTrust.FormatFingerprint(previous);
        if (before.Length > 0)
        {
            // TRANSLATORS: label of the fingerprint of the certificate that was pinned before
            output.Add(new CertificateDetail(L10n.C("certificate", "Previously trusted"), before, Monospaced: true));
        }
        var issuedTo = CertTrust.IssuedTo(cert);
        if (issuedTo.Length > 0)
        {
            output.Add(new CertificateDetail(L10n.C("certificate", "Issued to"), issuedTo));
        }
        if (!string.IsNullOrEmpty(cert.Issuer))
        {
            output.Add(new CertificateDetail(L10n.C("certificate", "Issued by"), cert.Issuer));
        }
        if (!cert.NotBefore.IsGoZero && !cert.NotAfter.IsGoZero)
        {
            // TRANSLATORS: validity period: two dates
            var period = L10n.Format(
                L10n.C("certificate", "%s to %s"),
                Format.FormatDate(cert.NotBefore, now, culture, timeZone),
                Format.FormatDate(cert.NotAfter, now, culture, timeZone));
            // TRANSLATORS: label of the validity period
            output.Add(new CertificateDetail(L10n.C("certificate", "Valid"), period));
        }
        if (cert.SelfSigned)
        {
            output.Add(new CertificateDetail(L10n.C("certificate", "Self-signed"), ""));
        }
        return output;
    }

    /// <inheritdoc/>
    public bool Equals(TrustPrompt? other) =>
        other is not null && string.Equals(Heading, other.Heading, StringComparison.Ordinal)
        && string.Equals(Body, other.Body, StringComparison.Ordinal) && Details.SequenceEqual(other.Details)
        && string.Equals(ConfirmLabel, other.ConfirmLabel, StringComparison.Ordinal);

    /// <inheritdoc/>
    public override int GetHashCode() => HashCode.Combine(Heading, Body, Details.Count, ConfirmLabel);
}
