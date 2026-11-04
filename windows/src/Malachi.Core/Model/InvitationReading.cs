// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/InvitationReading.swift
// (invitationPill, invitationPreview, invitationReading, showsInvitationCard);
// GTK invitationPreview is ui/internal/window/model.go; the rest is
// Swift-first, no GTK file yet (docs/calendar.md §5.7, "As built (M8)"). Its
// extension of Cal (cardClockLine, DayStrip, dayStripOf) is
// Calendars/CalendarInvitationReading.cs.
//
// What Mail shows of a message with a calendar part: the list row's pill
// (MessageSummary.Invitation) and preview line, and whether the reading
// pane, a message window or a conversation card shows the invitation card
// (Message.Invitation of message.get, the account's InvitationCard switch).
// Swift's free functions are the static Pill, Preview, Read and
// ShowsInvitationCard; Pill(ListRow) is the Windows overload for a row (its
// message, on a conversation row the newest folder member).

using System;
using System.Globalization;
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
    /// invitationPreview: the preview line of a list row whose message (a
    /// conversation row: its newest member) carries an invitation with a
    /// pill: the event's time for an invitation, an update or a cancellation
    /// (<see cref="Cal.SummaryTimeLine"/>), the answer for a reply
    /// (<see cref="Conversation.InvitationReplyLine"/>), instead of the
    /// body's first words, which a meeting invitation fills with boilerplate;
    /// <paramref name="snippet"/> otherwise, and when the summary has no
    /// time. <paramref name="env"/> null: the local zone and culture.
    /// </summary>
    public static string Preview(MessageSummary s, string snippet, Cal.Env? env = null)
    {
        ArgumentNullException.ThrowIfNull(s);
        if (s.Invitation is not { } inv)
        {
            return snippet;
        }
        switch (Cal.PillKind(inv))
        {
            case Cal.InvitationPill.None:
                return snippet;
            case Cal.InvitationPill.Reply:
                return Conversation.InvitationReplyLine(inv.ReplyResponse);
        }
        var line = Cal.SummaryTimeLine(inv, env ?? Cal.Env.ForCulture(TimeZoneInfo.Local, CultureInfo.CurrentCulture));
        return line.Length > 0 ? line : snippet;
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
