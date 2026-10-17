// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The invitation half of the list rows: macos/Sources/MalachiMail/MessageList/MessageCellView.swift
// (applyInvitation over InvitationReading.invitationPill; no Swift test of
// the cell, docs/calendar.md §5.7). A message with a calendar part has its
// pill after the subject (Invitation, Update, Cancelled with the subject
// struck through, Reply), a conversation the pill of the message the row
// shows, and an unreadable part none.

using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Malachi.Core.Tests.Model;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class InvitationMessageRowTests
{
    private static readonly RowAppearance Look = new(Compact: false, ShowPreview: true, ShowAvatars: true, Monochrome: false, Grouped: true);

    [Theory]
    [InlineData(InvitationKind.Invitation, InvitationState.Unanswered, Cal.InvitationPill.Invitation, "Invitation", false)]
    [InlineData(InvitationKind.Invitation, InvitationState.Answered, Cal.InvitationPill.Invitation, "Invitation", false)]
    [InlineData(InvitationKind.Update, InvitationState.Unanswered, Cal.InvitationPill.Update, "Update", false)]
    [InlineData(InvitationKind.Cancelled, InvitationState.Cancelled, Cal.InvitationPill.Cancelled, "Cancelled", true)]
    [InlineData(InvitationKind.Reply, InvitationState.Processed, Cal.InvitationPill.Reply, "Reply", false)]
    [InlineData(InvitationKind.Invitation, InvitationState.Invalid, Cal.InvitationPill.None, "", false)]
    [InlineData("other", InvitationState.Processed, Cal.InvitationPill.None, "", false)]
    public void AMessageRowCarriesThePillOfItsCalendarPart(string kind, string state, Cal.InvitationPill pill, string text, bool struck)
    {
        var row = Row(Mail("m1", kind, state));
        Assert.Equal(pill, row.InvitationPill);
        Assert.Equal(text, row.InvitationText);
        Assert.Equal(struck, row.StrikesSubject);
        if (text.Length > 0)
        {
            Assert.Contains(text, row.ToString(), System.StringComparison.Ordinal);
        }
    }

    [Fact]
    public void MailWithoutACalendarPartHasNone()
    {
        var row = Row(MailModelTests.Summary("m1"));
        Assert.Equal(Cal.InvitationPill.None, row.InvitationPill);
        Assert.Equal("", row.InvitationText);
        Assert.False(row.StrikesSubject);
    }

    [Fact]
    public void AConversationShowsThePillOfTheMessageItsRowShowsAndFollowsIt()
    {
        var latest = Mail("m2", InvitationKind.Cancelled, InvitationState.Cancelled);
        var t = ThreadModelTests.Thr("t1", 3, 1, latest);
        var r = new ListRow { Key = new ListKey(Thread: t.Id), Thread = true, Message = t.Latest, Summary = t };
        var row = new MessageRow(r.Key);
        row.Update(r, MailModel.SummaryMessage(t.Latest), Look, t.LatestDate);
        Assert.True(row.IsThread);
        Assert.Equal("Cancelled", row.InvitationText);
        Assert.True(row.StrikesSubject);

        // The same key shows a newer message without a calendar part.
        var plain = MailModelTests.Summary("m3");
        var t2 = ThreadModelTests.Thr("t1", 4, 1, plain);
        row.Update(r with { Message = t2.Latest, Summary = t2 }, MailModel.SummaryMessage(t2.Latest), Look, t2.LatestDate);
        Assert.Equal("", row.InvitationText);
        Assert.False(row.StrikesSubject);
    }

    private static MessageSummary Mail(string id, string kind, string state) =>
        MailModelTests.Summary(id) with
        {
            Invitation = new InvitationSummary
            {
                Method = new InvitationMethod("REQUEST"),
                Kind = new InvitationKind(kind),
                State = new InvitationState(state),
                Title = "Q4 planning",
            },
        };

    private static MessageRow Row(MessageSummary s)
    {
        var row = new MessageRow(new ListKey(s.ThreadId, s.Id));
        row.Update(new ListRow { Key = new ListKey(s.ThreadId, s.Id), Message = s }, MailModel.SummaryMessage(s), Look, s.Date);
        return row;
    }
}
