// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Bulk/BulkMail.swift (BulkMail.StripKind);
// GTK: ui/internal/bulkmail/bulkmail.go (StripKind).

namespace Malachi.Core.Bulk;

/// <summary>What the strip above a message is, which picks its icon (bulkmail.StripKind).</summary>
public enum BulkStripKind
{
    /// <summary>No strip is shown.</summary>
    None,

    /// <summary>Bulk mail (megaphone).</summary>
    Newsletter,

    /// <summary>A mailing list (people).</summary>
    List,

    /// <summary>An automated message (gear).</summary>
    Automated,

    /// <summary>Bulk mail in the junk folder (warning).</summary>
    Junk,

    /// <summary>The user already unsubscribed (check).</summary>
    Unsubscribed,
}
