// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Wizard/Fields.swift (Identity); GTK:
// ui/internal/accountwizard/fields.go (Identity). Printing one (a log line,
// an assertion) never shows the password.

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

    /// <summary>The fields, the password only as whether it is set.</summary>
    public override string ToString() =>
        "Identity(displayName: " + DisplayName + ", email: " + Email + ", password: " + (Password.Length == 0 ? "\"\"" : "<redacted>") + ")";
}
