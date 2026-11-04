// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Bulk/BulkMail.swift (BulkMail.Strip);
// GTK: ui/internal/bulkmail/bulkmail.go (Strip).

namespace Malachi.Core.Bulk;

/// <summary>
/// The bar above the message body (bulkmail.Strip): the default value is
/// "no strip". Its texts are from the mail (a domain, a list id): plain text
/// only.
/// </summary>
public sealed record BulkStrip
{
    /// <summary>What the strip is; <see cref="BulkStripKind.None"/> hides it.</summary>
    public BulkStripKind Kind { get; init; }

    /// <summary>The sentence, plain text, never markup.</summary>
    public string Text { get; init; } = "";

    /// <summary>The label of the button, with its mnemonic; "" for no button.</summary>
    public string Action { get; init; } = "";

    /// <summary>The strip asks for the warning look.</summary>
    public bool Warning { get; init; }

    /// <summary>Whether there is a strip to show (bulkmail.Strip.Visible).</summary>
    public bool Visible => Kind != BulkStripKind.None;
}
