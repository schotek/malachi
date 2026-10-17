// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the MainWindowController extension of
// macos/Sources/MalachiMail/Calendar/EventDetailPopover.swift
// (showInvitationInMail, over the board's boardMail.reveal); GTK: the
// calendar's window code follows (docs/calendar.md §6.4), the reveal is
// window/board_show_in_mail.go's.
//
// Show Invitation in Mail of the event detail (docs/calendar.md §5.5): the
// window goes to Mail and the list selects the invitation in its current
// folder, as the board's Show in Mail does (message.get first, then the
// folder and its first page, then the row), or the message opens in its own
// window when the list cannot show it (Integration.Board.cs). A later Show
// in Mail or Show Invitation in Mail overtakes this one.

using System;
using Malachi.Core.Api;

namespace Malachi.App.Shell;

/// <summary>Show Invitation in Mail of the Integration.</summary>
public sealed partial class Integration
{
    /// <summary>
    /// Mail with <paramref name="message"/> (an event's invitation) selected
    /// in its folder, or in its own window (macOS <c>showInvitationInMail</c>).
    /// </summary>
    public void ShowInvitationInMail(MessageRef message)
    {
        ArgumentNullException.ThrowIfNull(message);
        var generation = ++revealGeneration;
        _ = ShowInMailAsync(message.AccountId, message.MessageId, null, null, generation, BoardController.State.Selection);
    }
}
