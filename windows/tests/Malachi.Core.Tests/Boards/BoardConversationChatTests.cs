// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/board/conversation_chat_test.go (TestChatRowsSidesAndOrder,
// TestChatOpens, TestChatToggleTexts, TestDetailMessagesSenderAndDate).
// The catalogue is English, so the texts are the msgids.

using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Xunit;
using static Malachi.Core.Boards.Board;
using BText = Malachi.Core.Boards.Board.Text;
using F = Malachi.Core.Tests.Boards.BoardFixture;

namespace Malachi.Core.Tests.Boards;

public sealed class BoardConversationChatTests
{
    // Members "ma", "mb", … with the given sides.
    private static List<MessageCard> Cards(params bool[] mine) =>
        [.. mine.Select((m, i) => new MessageCard(new MessageId("m" + (char)('a' + i)), "", "", "", m))];

    // The bubbles 0, 1, … with the given sides, in order.
    private static ChatBubble[] Want(params bool[] right) => [.. right.Select((r, i) => new ChatBubble(i, r))];

    [Fact]
    public void ChatRowsSidesAndOrder()
    {
        var cases = new List<(string Name, List<MessageCard> Messages, ChatBubble[] Want)>
        {
            ("empty", Cards(), Want()),
            ("mixed", Cards(false, true, false, true), Want(false, true, false, true)),
            ("all mine", Cards(true, true), Want(true, true)),
            ("all others", Cards(false, false, false), Want(false, false, false)),
            ("samples", new List<MessageCard> { new(null, "Ann", "", "", false), new(null, "You", "", "", true) }, Want(false, true)),
        };
        foreach (var (name, messages, want) in cases)
        {
            Assert.True(want.SequenceEqual(ChatRows(messages)), name);
        }
    }

    [Fact]
    public void ChatRowsOfNone() => Assert.Empty(ChatRows(null));

    [Fact]
    public void ChatOpensOnlyAMemberWithAnId()
    {
        Assert.True(ChatOpens(new MessageCard(new MessageId("m1"), "Ann", "", "", false)), "a member with an id opens");
        Assert.True(ChatOpens(new MessageCard(new MessageId("m1"), "You", "", "", true)), "the user's own opens");
        Assert.False(ChatOpens(new MessageCard(null, "Ann", "", "sample", false)), "a sample opens nothing");
        Assert.False(ChatOpens(new MessageCard(new MessageId(""), "Ann", "", "", false)), "an empty id opens nothing");
    }

    [Fact]
    public void ChatToggleTexts()
    {
        Assert.Equal("Show as Chat", BText.ChatToggleLabel(false));
        Assert.Equal("Show as Thread", BText.ChatToggleLabel(true));
        Assert.Equal("Others on the left, your messages on the right; double-click opens the message", BText.ChatToggleTooltip(false));
        Assert.Equal("Back to the cards: the messages as cards, oldest first, the newest open", BText.ChatToggleTooltip(true));
        // The label is Mail's, the tooltip on goes back to the board's cards.
        Assert.Equal(Malachi.Core.Model.Conversation.ChatToggleLabel(true), BText.ChatToggleLabel(true));
        Assert.Equal(Malachi.Core.Model.Conversation.ChatToggleTooltip(false), BText.ChatToggleTooltip(false));
    }

    [Fact]
    public void DetailMessagesSenderAndDate()
    {
        var ann = F.Ago(5);
        var me = F.Ago(1);
        var c = F.Mk("c1", messages:
        [
            new CaseMessage { Id = new MessageId("m1"), From = "Ann\u200B Example", Address = "ann@example.com", Date = ann, Text = "hi" },
            new CaseMessage { Id = new MessageId("m2"), From = "Me Myself", Date = me, Text = "hello", Mine = true },
        ]);
        var d = Assert.IsType<Detail>(F.View([c]).Detail);
        Assert.Equal(2, d.Messages.Count);
        var (theirs, mine) = (d.Messages[0], d.Messages[1]);
        Assert.Equal("Ann Example", theirs.From);
        Assert.Equal("Ann Example", theirs.Sender);
        Assert.Equal("ann@example.com", theirs.Address);
        var wire = new Malachi.Core.Api.BoardMessage
        {
            Id = new MessageId("m9"), FolderId = new FolderId("f_1"), From = new Malachi.Core.Api.Address { Name = "A", Email = "a@b.cz" }, Date = ann, Mine = false, Text = "",
        };
        Assert.Equal("a@b.cz", Malachi.Core.Boards.DaemonBoardSource.Convert(wire).Address);
        Assert.Equal(BText.You, mine.From);
        Assert.Equal("Me Myself", mine.Sender);
        Assert.Equal(ann, theirs.Date);
        Assert.Equal(me, mine.Date);
        Assert.True(theirs.When.Length > 0 && mine.When.Length > 0, "the cards' short dates stay");
        var rows = ChatRows(d.Messages);
        Assert.True(rows[1].Right && !rows[0].Right, "sides");
    }

    // ChatBodies: a near member with an id asks once per case, only with the
    // preference on; a sample never asks; another case or a reset starts over.
    [Fact]
    public void ChatBodiesAskOncePerCaseOnlyWithTheSetting()
    {
        var bodies = new ChatBodies();
        var a = new MessageCard(new MessageId("ma"), "Ann", "", "Hi", false);
        var sample = new MessageCard(null, "Ann", "", "Hi", false);
        var c1 = new BoardCaseId("c_1");
        Assert.False(bodies.NeedsBody(c1, a, setting: false, near: true), "the setting off never fetches");
        Assert.False(bodies.NeedsBody(c1, a, setting: true, near: false), "far");
        Assert.False(bodies.NeedsBody(c1, sample, setting: true, near: true), "a sample");
        Assert.True(bodies.NeedsBody(c1, a, setting: true, near: true));
        bodies.Asked(a.Id!.Value);
        Assert.True(bodies.WasAsked(a.Id));
        Assert.False(bodies.WasAsked(null));
        Assert.False(bodies.NeedsBody(c1, a, setting: true, near: true), "asked once");
        // Another case forgets.
        var c2 = new BoardCaseId("c_2");
        Assert.True(bodies.NeedsBody(c2, a, setting: true, near: true));
        Assert.False(bodies.WasAsked(a.Id));
        bodies.Asked(a.Id!.Value);
        bodies.Reset();
        Assert.Null(bodies.CaseId);
        Assert.False(bodies.WasAsked(a.Id));
    }
}
