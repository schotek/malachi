// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Wizard/Fields.swift (Identity); GTK:
// ui/internal/accountwizard/fields.go (Identity). Printing one (a log line,
// an assertion) never shows a value (docs/windows-port.md §3.1: addresses
// and secrets are never logged).

using System.Diagnostics;

namespace Malachi.Core.Wizard;

/// <summary>accountwizard.Identity: what the first page collects.</summary>
[DebuggerDisplay("{ToString(),nq}")]
public sealed record Identity
{
    /// <summary>The sender's name.</summary>
    public string DisplayName { get; init => field = value ?? ""; } = "";

    /// <summary>The address.</summary>
    public string Email { get; init => field = value ?? ""; } = "";

    /// <summary>The password; "" keeps a stored one when editing.</summary>
    public string Password { get; init => field = value ?? ""; } = "";

    /// <summary>Whether each field is set, never a value: names and addresses are not logged either.</summary>
    public override string ToString() =>
        "Identity(displayName: " + Mark(DisplayName) + ", email: " + Mark(Email) + ", password: " + Mark(Password) + ")";

    private static string Mark(string s) => s.Length == 0 ? "\"\"" : "<redacted>";
}
