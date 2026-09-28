// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Wizard/Fields.swift (IdentityProblems);
// GTK: ui/internal/accountwizard/fields.go (IdentityProblems).

namespace Malachi.Core.Wizard;

/// <summary>accountwizard.IdentityProblems: the identity fields that cannot be sent.</summary>
public sealed record IdentityProblems
{
    /// <summary>The address is not a bare address.</summary>
    public bool Email { get; init; }

    /// <summary>A password is required and empty.</summary>
    public bool Password { get; init; }

    /// <summary>Whether anything is wrong.</summary>
    public bool Any => Email || Password;
}
