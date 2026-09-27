// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Wizard/Fields.swift (ServerProblems);
// GTK: ui/internal/accountwizard/fields.go (ServerProblems).

namespace Malachi.Core.Wizard;

/// <summary>accountwizard.ServerProblems: the server rows that cannot be sent.</summary>
public sealed record ServerProblems
{
    /// <summary>The IMAP host is empty.</summary>
    public bool ImapHost { get; init; }

    /// <summary>The IMAP user name is empty.</summary>
    public bool ImapUser { get; init; }

    /// <summary>The SMTP host is empty.</summary>
    public bool SmtpHost { get; init; }

    /// <summary>The SMTP user name is empty.</summary>
    public bool SmtpUser { get; init; }

    /// <summary>Whether anything is wrong.</summary>
    public bool Any => ImapHost || ImapUser || SmtpHost || SmtpUser;
}
