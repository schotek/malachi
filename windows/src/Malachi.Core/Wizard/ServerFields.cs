// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Wizard/Fields.swift (ServerFields); GTK:
// ui/internal/accountwizard/fields.go (ServerFields).

using Malachi.Core.Api;

namespace Malachi.Core.Wizard;

/// <summary>
/// accountwizard.ServerFields: the rows of one endpoint on the Servers page,
/// and the certificate pinned to it (not a row: the wizard sets it from the
/// pin trusted for this host and port, <see cref="CertTrust.KeepPin"/>).
/// "" is no pin.
/// </summary>
public sealed record ServerFields
{
    /// <summary>The server's name or address.</summary>
    public string Host { get; init => field = value ?? ""; } = "";

    /// <summary>The port; 0 before one is chosen.</summary>
    public int Port { get; init; }

    /// <summary>tls, starttls or none.</summary>
    public Security Security { get; init; } = Security.Tls;

    /// <summary>The login name.</summary>
    public string Username { get; init => field = value ?? ""; } = "";

    /// <summary>The pinned certificate's fingerprint; "" for none.</summary>
    public string CertificateSha256 { get; init => field = value ?? ""; } = "";
}
