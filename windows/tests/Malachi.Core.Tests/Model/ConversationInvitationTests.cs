// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/conversation/invitation_test.go and
// macos/Tests/MalachiCoreTests/ConversationInvitationTests.swift (English
// catalogue): an answer to an invitation is a compact row of the
// conversation. The fixtures are ConversationTests'.

using Malachi.Core.Api;
using Malachi.Core.Model;
using Xunit;
using static Malachi.Core.Tests.Model.ConversationTests;

namespace Malachi.Core.Tests.Model;

public sealed class ConversationInvitationTests
{
    // An answer to an invitation in conversation t1 (invReply).
    private static MessageSummary Reply(string id, int min, bool seen, Address from, EventResponse? r, string folder = "f1") =>
        Mail(id, min, seen) with
        {
            FolderId = folder,
            From = [from],
            To = [],
            Snippet = "",
            HasAttachments = true,
            Invitation = new InvitationSummary
            {
                Method = InvitationMethod.Reply,
                Kind = InvitationKind.Reply,
                State = InvitationState.Processed,
                Title = "Quarterly report",
                ReplyFrom = from,
                ReplyResponse = r,
            },
        };

    private static MessageSummary Invite() => Mail("m1", 0, true) with
    {
        Invitation = new InvitationSummary
        {
            Method = InvitationMethod.Request,
            Kind = InvitationKind.Invitation,
            State = InvitationState.Processed,
            Title = "Quarterly report",
        },
    };

    [Theory]
    [InlineData(EventResponse.Accepted, "Invitation accepted")]
    [InlineData(EventResponse.Tentative, "Invitation tentatively accepted")]
    [InlineData(EventResponse.Declined, "Invitation declined")]
    [InlineData(EventResponse.NeedsAction, "Replied to the invitation")]
    [InlineData("", "Replied to the invitation")]
    public void ReplyLine(string r, string want)
    {
        Assert.Equal(want, Conversation.InvitationReplyLine(new EventResponse(r)));
        Assert.Equal("Replied to the invitation", Conversation.InvitationReplyLine(null));
    }

    [Fact]
    public void IsInvitationReply()
    {
        Assert.False(Conversation.IsInvitationReply(Invite()));
        Assert.False(Conversation.IsInvitationReply(Mail("m2", 0, true)));
        Assert.False(Conversation.IsInvitationReply(null));
        Assert.True(Conversation.IsInvitationReply(Reply("r", 0, true, Jana, null)));
    }

    // An answer is a compact row in both layouts: an event with its
    // sentence and sender, no card actions; the user's own answer in Sent is
    // a sent row; an attendee's unread answer is marked read.
    [Fact]
    public void Rows()
    {
        var mine = Reply("s1", 15, true, Petr, EventResponse.Tentative, "fs");
        var m = Conversation.Build(MailThread(1), [Invite()], MailAccount, [mine]);
        Assert.Equal(2, m.Items.Count);
        var r = m.Items[1];
        Assert.Equal(
            (ConversationItemKind.Event, true, false, true, "Petr Svoboda"),
            (r.Kind, r.Sent, r.Unread, r.Mine, r.Sender));
        Assert.Equal(["Invitation tentatively accepted"], r.EventLines);
        Assert.Equal("Invitation tentatively accepted", r.EventText);
        var a = Conversation.CardActions(MailAccount, mine, composeAccount: true);
        Assert.False(a.Reply || a.ReplyAll || a.Forward);
        var rows = Conversation.ChatRows(m, []);
        Assert.Equal(2, rows.Count);
        Assert.Equal((ChatRowKind.Event, ChatSide.Center), (rows[1].Kind, rows[1].Side));
        Assert.Null(m.MarkRead);

        var theirs = Reply("r1", 20, false, Jana, EventResponse.Accepted);
        m = Conversation.Build(MailThread(2), [Invite(), theirs], MailAccount);
        var it = m.Items[1];
        Assert.Equal((ConversationItemKind.Event, true, false), (it.Kind, it.Unread, it.Mine));
        Assert.Equal(["Invitation accepted"], it.EventLines);
        Assert.Equal("r1", m.MarkRead?.Value);
        // A newer card that is read: nothing to mark, the answer stays as it is.
        m = Conversation.Merge(m, Mail("m2", 30, true), MailAccount);
        Assert.Null(m.MarkRead);
    }
}
