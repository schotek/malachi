// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the rules of the compose window's header card that macOS keeps in
// AppKit: macos/Sources/MalachiMail/Compose/ComposeWindowController.swift
// (setAccounts, fromLocked, updateTitle) and ComposeHeaderView.swift
// (setCcBccVisible); GTK: ui/internal/compose/compose.go (setAccounts,
// fromFactory's label, fromLocked, account, setCcBccVisible, updateTitle).
// A presentation class of docs/windows-port.md §7.4, so that the rules have
// their tests; the header card (Malachi.App Compose/ComposeHeader) applies
// them.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.I18n;
using Malachi.Core.Text;

namespace Malachi.Core.Presentation;

/// <summary>The From row, the Cc/Bcc button and the window title of a compose window.</summary>
public static class ComposeHeaderRules
{
    /// <summary>
    /// fromFactory's label: the account's display name (its name when it has
    /// none) and address, "Name &lt;address&gt;", as plain text
    /// (<see cref="Format.FormatAddress"/>: the name isolated).
    /// </summary>
    public static string FromLabel(Account account)
    {
        ArgumentNullException.ThrowIfNull(account);
        var name = string.IsNullOrEmpty(account.Config.DisplayName) ? account.Config.Name : account.Config.DisplayName;
        return Format.FormatAddress(new Address { Name = name, Email = account.Config.Email });
    }

    /// <summary>
    /// setAccounts' choice: the identity the user picked
    /// (<paramref name="chosen"/>) while it is listed, otherwise the account
    /// the window was opened for (<paramref name="openedFor"/>), otherwise
    /// the first. <c>Found</c> says whether the one looked for is listed.
    /// </summary>
    public static (int Index, bool Found) FromSelection(IReadOnlyList<Account> accounts, AccountId? chosen, AccountId? openedFor)
    {
        ArgumentNullException.ThrowIfNull(accounts);
        var wanted = chosen ?? openedFor;
        for (var i = 0; i < accounts.Count; i++)
        {
            if (wanted is { } id && accounts[i].Id == id)
            {
                return (i, true);
            }
        }
        return (0, false);
    }

    /// <summary>
    /// fromLocked: a reply or a forward (a draft of one reopened from Drafts
    /// included) goes out from the account the original is in: its quoted
    /// pictures and forwarded files were copied into that account, and the
    /// reply belongs to that mailbox's conversation.
    /// </summary>
    public static bool FromLocked(ComposeParams p)
    {
        ArgumentNullException.ThrowIfNull(p);
        return p.InReplyTo is not null || p.Forwarding is not null;
    }

    /// <summary>
    /// setAccounts' sensitivity: the From row takes a choice only with more
    /// than one identity, and never for a locked window whose account is
    /// listed.
    /// </summary>
    public static bool FromEnabled(int accounts, bool locked, bool found) => accounts > 1 && !(locked && found);

    /// <summary>
    /// setCcBccVisible: the Cc/Bcc button stays only while one of the two
    /// lines is still hidden.
    /// </summary>
    public static bool CcBccButtonVisible(bool ccShown, bool bccShown) => !ccShown || !bccShown;

    /// <summary>
    /// updateTitle: the subject, trimmed, or "New Message". Windows-only:
    /// cleaned for display first (<see cref="DisplayText.Clean"/>), as a
    /// reply's subject is the original sender's text; the Subject row itself
    /// keeps what was typed or prefilled.
    /// </summary>
    public static string WindowTitle(string subject)
    {
        ArgumentNullException.ThrowIfNull(subject);
        var s = DisplayText.Clean(subject).Trim();
        return s.Length > 0 ? s : L10n.T("New Message");
    }
}
