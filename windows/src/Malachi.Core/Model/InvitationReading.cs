// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/InvitationReading.swift
// (invitationPill, invitationReading, showsInvitationCard); Swift-first, no
// GTK file yet (docs/calendar.md §5.7, "As built (M8)"). Its extension of
// Cal (cardClockLine, DayStrip, dayStripOf) is
// Calendars/CalendarInvitationReading.cs.
//
// What Mail shows of a message with a calendar part: the list row's pill
// (MessageSummary.Invitation), and whether the reading pane, a message
// window or a conversation card shows the invitation card
// (Message.Invitation of message.get, the account's InvitationCard switch).
// Swift's free functions are the static Pill, Read and ShowsInvitationCard;
// Pill(ListRow) is the Windows overload for a row (its message, on a
// conversation row the newest folder member).

using System;
using Malachi.Core.Api;
using Malachi.Core.Calendars;

namespace Malachi.Core.Model;

/// <summary>The invitation part of a message on display (Swift's InvitationReading.swift).</summary>
public static class InvitationReading
{
    /// <summary>
    /// invitationPill: the pill of a list row's message, whatever its state
    /// (<see cref="Cal.PillKind"/>, D29: an invitation answered elsewhere
    /// keeps its pill).
    /// </summary>
    public static Cal.InvitationPill Pill(MessageSummary s)
    {
        ArgumentNullException.ThrowIfNull(s);
        return Cal.PillKind(s.Invitation);
    }

    /// <summary>The pill of a list row: that of the message the row shows (on a conversation row its newest folder member).</summary>
    public static Cal.InvitationPill Pill(ListRow row)
    {
        ArgumentNullException.ThrowIfNull(row);
        return Pill(row.Message);
    }

    /// <summary>
    /// invitationReading: the invitation card a message on display shows:
    /// the card of message.get when <see cref="Cal.CardModeOf"/> gives one
    /// (<paramref name="cardOn"/>: the account's "Show the Invitation
    /// Card"); null before message.get answered, for a message without a
    /// calendar part, for an unreadable part (state invalid: the message
    /// reads as usual) and for kind other.
    /// </summary>
    public static InvitationCard? Read(Message? m, bool cardOn) =>
        m?.Invitation is { } c && Cal.CardModeOf(c, cardOn) != Cal.CardMode.None ? c : null;

    /// <summary>
    /// showsInvitationCard: whether account <paramref name="a"/> shows
    /// invitation cards (<see cref="CalendarConfig.ShowsInvitationCard"/>; an
    /// account without a calendar part, or not known, shows them).
    /// </summary>
    public static bool ShowsInvitationCard(Account? a) => a?.Config.Calendar?.ShowsInvitationCard ?? true;
}
