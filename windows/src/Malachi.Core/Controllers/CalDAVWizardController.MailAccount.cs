// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/CalDAVWizardController.swift
// (MailAccount); Swift-first, no GTK controller yet (the rules are
// ui/internal/calendar/caldav.go, Cal.CalDAVWizard).

using Malachi.Core.Api;

namespace Malachi.Core.Controllers;

/// <content>The mail-account popup's choices.</content>
public sealed partial class CalDAVWizardController
{
    /// <summary>
    /// A choice of the mail account popup (invitations of a server that
    /// leaves them to the client): an account that can send, its name cleaned.
    /// </summary>
    /// <param name="Id">The account.</param>
    /// <param name="Name">Its name, cleaned (plain text).</param>
    public sealed record MailAccount(AccountId Id, string Name);
}
