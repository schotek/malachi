// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Wizard/Results.swift (Outcome); GTK:
// ui/internal/accountwizard/results.go (Outcome).

namespace Malachi.Core.Wizard;

/// <summary>accountwizard.Outcome: what the test page shows and which buttons it offers.</summary>
public enum Outcome
{
    /// <summary>Every endpoint answered.</summary>
    Ok,

    /// <summary>Credentials rejected somewhere: back to the identity page.</summary>
    AuthFailed,

    /// <summary>Anything else: retry, edit, or add anyway.</summary>
    Failed,
}
