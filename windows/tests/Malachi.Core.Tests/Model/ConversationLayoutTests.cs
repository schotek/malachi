// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/window/conversation_layout_test.go (TestConvSpanDistance,
// TestLiveWindowIsTwoScreensEachWay, TestWebViewsGoToTheNearestHTMLCardsUpToTheCap,
// TestAnchoredTopKeepsTheItemAndStaysInTheDocument,
// TestPageTopStepsAViewportLessTheOverlap, TestConvRails*,
// TestConvColumnKeepsToTheClampWithTheGutterInIt, TestWebHeight*,
// TestPicturesArrived, TestClampText, TestConvDisplayOrder), itself a port
// of the macOS client's ConversationLayoutTests.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Xunit;
using static Malachi.Core.Model.ConversationLayout;

namespace Malachi.Core.Tests.Model;

public sealed class ConversationLayoutTests
{
    // 2026-09-01T09:00:00Z.
    private static readonly DateTimeOffset T0 = new(2026, 9, 1, 9, 0, 0, TimeSpan.Zero);

    private static readonly Account RailAccount = new()
    {
        Id = "a1",
        Config = new AccountConfig { Name = "Work", Email = "petr@acme.example" },
        Enabled = true,
        State = new SyncState { AccountId = "a1", Status = SyncStatus.Idle },
    };

    private static readonly IssueInfo RailIssue = new()
    {
        Key = "WEB-12",
        Url = "https://acme.atlassian.net/browse/WEB-12",
        Summary = "Footer overlaps the form",
        Status = "To Do",
        StatusCategory = IssueStatusCategory.Todo,
    };

    [Fact]
    public void SpanDistance()
    {
        var s = Span.Of(100, 200);
        Assert.Equal(0, s.Distance(Span.Of(150, 400)));
        Assert.Equal(0, s.Distance(Span.Of(200, 300))); // touching counts as overlapping
        Assert.Equal(50, s.Distance(Span.Of(250, 300)));
        Assert.Equal(60, s.Distance(Span.Of(0, 40)));
        Assert.Equal(10, Span.Of(10, 5).Max); // an inverted span is empty at its top
    }

    [Fact]
    public void LiveWindowIsTwoScreensEachWay()
    {
        // 40 items of 100 in a viewport of 300 at 1000..1300: near is
        // 400..1900 (two screens each way), items 3 to 19 (touching counts).
        var frames = Stacked(40, 100);
        var live = LiveCards(frames, new bool[40], Span.Of(1000, 1300), LiveScreens, MaxLiveWebViews);
        Assert.Equal(Range(3, 19), Sorted(live.Near));
        Assert.Empty(live.Web); // no HTML, no web view
        // At the top.
        Assert.Equal(Range(0, 9), Sorted(LiveCards(frames, null, Span.Of(0, 300), LiveScreens, MaxLiveWebViews).Near));
        // Screens as asked.
        Assert.Equal(Range(9, 13), Sorted(LiveCards(frames, null, Span.Of(1000, 1300), 0, MaxLiveWebViews).Near));
    }

    [Fact]
    public void WebViewsGoToTheNearestHtmlCardsUpToTheCap()
    {
        var frames = Stacked(40, 100);
        var html = Enumerable.Repeat(true, 40).ToArray();
        var live = LiveCards(frames, html, Span.Of(1000, 1300), LiveScreens, MaxLiveWebViews);
        // The visible ones (9–13, touching counts) first, then the nearest by
        // distance, the earlier one on a tie.
        Assert.Equal([7, 8, 9, 10, 11, 12, 13, 14], Sorted(live.Web));
        Assert.All(live.Web, i => Assert.Contains(i, live.Near));

        // Only HTML cards; a cap of two.
        var mixed = new bool[40];
        mixed[2] = mixed[11] = mixed[30] = true;
        Assert.Equal([11], Sorted(LiveCards(frames, mixed, Span.Of(1000, 1300), LiveScreens, 2).Web)); // 2 and 30 are not near
        Assert.Empty(LiveCards(frames, mixed, Span.Of(1000, 1300), LiveScreens, 0).Web); // a cap of none
        var empty = LiveCards([], null, Span.Of(0, 300), LiveScreens, MaxLiveWebViews);
        Assert.Empty(empty.Near);
        Assert.Empty(empty.Web);
    }

    [Theory]
    // The item that was 30 below the viewport's top now starts at 500.
    [InlineData(500, 30, 2000, 400, 530)]
    // Clamped to the end and to the start.
    [InlineData(1900, 0, 2000, 400, 1600)]
    [InlineData(10, -50, 2000, 400, 0)]
    // A document shorter than the viewport stays at the top.
    [InlineData(200, 0, 300, 400, 0)]
    public void AnchoredTopKeepsTheItemAndStaysInTheDocument(double itemTop, double offset, double doc, double viewport, double want) =>
        Assert.Equal(want, AnchoredTop(itemTop, offset, doc, viewport));

    [Theory]
    [InlineData(0, false, 40, 360)]
    [InlineData(360, true, 40, 0)]
    [InlineData(1500, false, 40, 1600)]
    [InlineData(100, true, 40, 0)]
    // An overlap as large as the viewport still moves half a page.
    [InlineData(0, false, 400, 200)]
    public void PageTopStepsAViewportLessTheOverlap(double from, bool up, double overlap, double want) =>
        Assert.Equal(want, PageTop(from, up, 400, 2000, overlap));

    [Fact]
    public void RailsMessagesHaveAvatarsEventsAndTheOlderRowDots()
    {
        var status = MessageIssue.Of(RailIssue, IssueItemKind.Event) with
        {
            Changes = [new IssueChange { Field = IssueField.Status, From = "To Do", To = "Done" }],
            Mine = true,
        };
        var m = Conversation.Build(RailThread(7), [
            RailMember("d", 0, issue: MessageIssue.Of(RailIssue, IssueItemKind.Description)),
            RailMember("c1", 10, issue: MessageIssue.Of(RailIssue, IssueItemKind.Comment) with { Mine = true }),
            RailMember("e1", 20, issue: status),
            RailMember("c2", 30, issue: MessageIssue.Of(RailIssue, IssueItemKind.Comment) with { Via = "Issue Sync", Mine = true }),
        ], RailAccount);
        Assert.Equal(
            [ConversationItemKind.Truncated, ConversationItemKind.Message, ConversationItemKind.Message, ConversationItemKind.Event, ConversationItemKind.Message],
            m.Items.Select(i => i.Kind));
        Rail[] want =
        [
            // The line starts at the first mark and ends at the last.
            new(RailMarker.Dot, Below: true),
            new(RailMarker.Avatar, Above: true, Below: true),
            new(RailMarker.Avatar, Accent: true, Above: true, Below: true),
            // The user's own change: a dot is never tinted.
            new(RailMarker.Dot, Above: true, Below: true),
            // A relayed comment is never the user's.
            new(RailMarker.Avatar, Above: true),
        ];
        Assert.Equal(want, Rails(m.Items));
    }

    [Fact]
    public void RailsOwnMailIsTinted()
    {
        MessageSummary[] members = [RailMember("m1", 0), RailMember("m2", 10, from: "Petr@acme.example"), RailMember("m3", 20)];
        var m = Conversation.Build(RailThread(3), members, RailAccount);
        Assert.Equal(
            [new Rail(RailMarker.Avatar, Below: true), new Rail(RailMarker.Avatar, Accent: true, Above: true, Below: true), new Rail(RailMarker.Avatar, Above: true)],
            Rails(m.Items));
        // Without the account's address nothing is.
        var plain = Conversation.Build(RailThread(3), members[..2], RailAccount with { Config = new AccountConfig { Name = "", Email = "" } });
        Assert.All(Rails(plain.Items), r => Assert.False(r.Accent));
        Assert.Equal(ConversationItemKind.Truncated, plain.Items[0].Kind); // the third member is left out
    }

    [Fact]
    public void RailsOneItemHasNoLine()
    {
        var only = new ConversationItem { Kind = ConversationItemKind.Message, Message = RailMember("m1", 0), Mine = true };
        Assert.Equal([new Rail(RailMarker.Avatar, Accent: true)], Rails([only]));
        Assert.Empty(Rails([]));
        var two = Rails([only, new ConversationItem { Kind = ConversationItemKind.Event, Message = RailMember("e1", 1) }]);
        Assert.Equal([new Rail(RailMarker.Avatar, Accent: true, Below: true), new Rail(RailMarker.Dot, Above: true)], two);
    }

    [Fact]
    public void ColumnKeepsToTheClampWithTheGutterInIt()
    {
        const double beside = (2 * SideInset) + Avatar + GutterGap;
        Assert.Equal(600 - beside, CardWidth(600));
        Assert.Equal(MaxWidth - beside, CardWidth(MaxWidth));
        Assert.Equal(MaxWidth - beside, CardWidth(1600)); // the gutter is inside the 900
        Assert.Equal(0, CardWidth(40));
        Assert.Equal(0, CardWidth(0));
        // The short date while the header is narrower than the threshold.
        const double edge = CompactHeader + (2 * CardPaddingH) + beside;
        Assert.True(CompactDates(edge - 1), "one below the edge: compact");
        Assert.False(CompactDates(edge), "at the edge: full dates");
        Assert.True(CompactDates(300), "the pane at its narrowest: compact");
        Assert.False(CompactDates(2000), "a wide pane: full dates");
    }

    [Fact]
    public void WebHeightFollowsTheDocumentAtTheZoom()
    {
        var g = new WebHeightGovernor(1.25);
        Assert.True(g.Fits); // nothing reported: the wheel goes on
        Assert.Equal(500, g.Report(400, false));
        Assert.Equal((500.0, 500.0, true), (g.Applied, g.Content, g.Fits));
        Assert.Null(g.Report(400, false)); // the same height again
        Assert.Equal(376, g.Report(300.2, false)); // pixels round up
        Assert.Equal(1, g.Report(0, false)); // never below one pixel
        Assert.Null(g.Report(double.NaN, false));
        Assert.Null(g.Report(double.PositiveInfinity, false));
        Assert.Null(g.Report(-5, false));
    }

    [Fact]
    public void WebHeightIsCappedAndScrollsInside()
    {
        var g = new WebHeightGovernor(1);
        Assert.Equal(WebHeightGovernor.DefaultMaxHeight, g.Report(9000, false));
        Assert.False(g.Fits); // beyond the cap the card scrolls inside
        Assert.Equal(800, g.Report(800, false));
        Assert.True(g.Fits); // back under the cap
    }

    [Fact]
    public void WebHeightFreezesContentThatGrowsWithTheView()
    {
        // A 100vh document reports the view's height plus the column's
        // padding every time the view grows.
        var g = new WebHeightGovernor(1);
        Assert.Equal(156, g.Report(156, false));
        Assert.Equal(192, g.Report(192, true));
        Assert.Equal(228, g.Report(228, true));
        Assert.Equal(264, g.Report(264, true));
        Assert.Null(g.Report(300, true)); // the fourth growth in a row freezes
        Assert.Equal((true, 264.0, false), (g.Frozen, g.Applied, g.Fits));
        Assert.Null(g.Report(100, false)); // frozen until something changes

        // A new width measures again.
        g.WidthChanged();
        Assert.False(g.Frozen);
        Assert.Equal(100, g.Report(100, false));

        // Growth that the view's own growth did not cause (pictures
        // arriving) never freezes, and resets the count.
        var p = new WebHeightGovernor(1);
        double[] heights = [200, 260, 330, 420, 500, 610, 700];
        for (var i = 0; i < heights.Length; i++)
        {
            Assert.Equal(heights[i], p.Report(heights[i], i % 3 == 2));
        }
        Assert.False(p.Frozen);

        // A shrink in answer to the view resets the count too.
        var s = new WebHeightGovernor(1);
        s.Report(100, false);
        s.Report(200, true);
        s.Report(300, true);
        Assert.Equal(250, s.Report(250, true));
        Assert.Equal(350, s.Report(350, true));
        Assert.False(s.Frozen);
    }

    [Fact]
    public void WebHeightZoomAndNewDocumentsStartOver()
    {
        var g = new WebHeightGovernor(1);
        g.Report(400, false);
        Assert.Equal(600, g.SetZoom(1.5)); // the last height at the new zoom at once
        Assert.Null(g.SetZoom(1.5)); // the same zoom again
        Assert.Equal(400, g.SetZoom(0)); // zero is 100 %
        g.Report(100, false);
        foreach (var h in new double[] { 136, 172, 208, 244 })
        {
            g.Report(h, true);
        }
        Assert.True(g.Frozen);
        // The last report (the frozen document's own height) at the new
        // zoom: a zoom measures again.
        Assert.Equal(488, g.SetZoom(2));
        Assert.False(g.Frozen);

        g.Reset();
        Assert.Null(g.Content);
        Assert.False(g.Frozen);
        Assert.Equal(488, g.Applied); // the view keeps its height until the new document reports
        Assert.Equal(100, g.Report(50, false));

        Assert.Null(new WebHeightGovernor(1).SetZoom(2)); // nothing reported yet
    }

    [Fact]
    public void PicturesArrivedReloadsOnlyForThePicturesOfTheSameMessage()
    {
        var before = Body("m1", remote: 2);
        var now = Body("m1");
        Assert.True(PicturesArrived(before, now));
        Assert.False(PicturesArrived(null, now)); // the first body
        Assert.False(PicturesArrived(before, before)); // the same answer
        Assert.False(PicturesArrived(now, Body("m1"))); // no pictures
        Assert.False(PicturesArrived(before, Body("m2"))); // another message
    }

    [Fact]
    public void ClampTextCutsByCharacters()
    {
        Assert.Equal("https://example.org/", ClampText("https://example.org/", 512));
        Assert.Equal("žluť", ClampText("žluťoučký", 4));
        Assert.Equal("a", ClampText("ab", 1));
        // A character outside the BMP is one, as Go's rune.
        Assert.Equal("a\U0001F600", ClampText("a\U0001F600b", 2));
    }

    public static TheoryData<string, ConversationItem[], string[], int, bool> DisplayCases => new()
    {
        { "mail: the first message on top, folded", [Msg("a"), Msg("b"), Msg("c")], ["a", "c", "b"], 0, true },
        {
            "issue: the description on top",
            [IssueItem("desc", IssueItemKind.Description), Event("e1"), IssueItem("c1", IssueItemKind.Comment), Event("e2")],
            ["desc", "e2", "c1", "e1"], 0, true
        },
        {
            "issue with only status changes: the description stays open",
            [IssueItem("desc", IssueItemKind.Description), Event("e1"), Event("e2")],
            ["desc", "e2", "e1"], 0, false
        },
        { "cut mail: no first message, older ones last", [Truncated, Msg("x"), Msg("y")], ["y", "x", "…"], -1, false },
        {
            "cut issue: the description when it is there",
            [Truncated, IssueItem("desc", IssueItemKind.Description), IssueItem("c1", IssueItemKind.Comment)],
            ["desc", "c1", "…"], 0, true
        },
        { "an event first is no opening", [Event("e"), Msg("m")], ["m", "e"], -1, false },
        { "a message and the user's reply: the message stays open", [Msg("a"), Sent("r")], ["a", "r"], 0, false },
        { "the user's reply opened it: folded", [Sent("r"), Msg("a")], ["r", "a"], 0, true },
        { "replies among messages", [Msg("a"), Sent("r1"), Msg("b"), Sent("r2")], ["a", "r2", "b", "r1"], 0, true },
        { "nothing", [], [], -1, false },
    };

    // The pane shows what opened the conversation first, then the rest
    // newest first and the row of older members last; the model keeps its
    // own order.
    [Theory]
    [MemberData(nameof(DisplayCases))]
    public void DisplayOrderShowsTheOpeningFirstThenNewestFirst(string name, ConversationItem[] items, string[] want, int root, bool folded)
    {
        var before = items.ToArray();
        var d = DisplayOrder(items);
        Assert.True(want.SequenceEqual(d.Items.Select(ShownId)), $"{name}: order {string.Join(",", d.Items.Select(ShownId))}");
        Assert.Equal((root, folded), (d.Root, d.RootFolded));
        Assert.Equal(before, items); // the model's items are not changed
        // Every card has its start (Conversation.DefaultFolds): a sent card
        // folded, any other but the opening one open.
        foreach (var item in d.Items)
        {
            var id = item.Id ?? new MessageId("");
            var has = d.Folded.TryGetValue(id, out var cardFolded);
            Assert.True(has == (item.Kind == ConversationItemKind.Message), $"{name}: {id.Value} has a fold {has}");
            if (!has)
            {
                continue;
            }
            if (id == d.Opening)
            {
                Assert.True(cardFolded == folded, $"{name}: the opening card folded {cardFolded}");
            }
            else
            {
                Assert.True(cardFolded == item.Sent, $"{name}: {id.Value} folded {cardFolded}");
            }
        }
    }

    // The timeline follows the order shown: from the opening card down to
    // the row of older members.
    [Fact]
    public void TheTimelineFollowsTheOrderShown()
    {
        var rails = Rails(DisplayOrder([Truncated, Msg("x"), Msg("y")]).Items);
        Assert.Equal(new Rail(RailMarker.Avatar, Below: true), rails[0]);
        Assert.Equal(new Rail(RailMarker.Dot, Above: true), rails[^1]);
    }

    private static readonly ConversationItem Truncated = new() { Kind = ConversationItemKind.Truncated, Text = "2 earlier messages are not shown" };

    private static ConversationItem Msg(string id) => new() { Kind = ConversationItemKind.Message, Message = RailMember(id, 0) };

    private static ConversationItem Event(string id) => new() { Kind = ConversationItemKind.Event, Message = RailMember(id, 0) };

    private static ConversationItem Sent(string id) => Msg(id) with { Sent = true };

    private static ConversationItem IssueItem(string id, string kind) =>
        Msg(id) with { Message = RailMember(id, 0, issue: MessageIssue.Of(RailIssue, kind)) };

    private static string ShownId(ConversationItem it) => it.Kind == ConversationItemKind.Truncated ? "…" : it.Id!.Value.Value;

    private static Span[] Stacked(int n, double height) =>
        [.. Enumerable.Range(0, n).Select(i => Span.Of(i * height, (i + 1) * height))];

    private static List<int> Sorted(IReadOnlySet<int> set) => [.. set.Order()];

    private static List<int> Range(int from, int to) => [.. Enumerable.Range(from, to - from + 1)];

    private static MessageSummary RailMember(string id, int min, string from = "jana@acme.example", MessageIssue? issue = null) => new()
    {
        Id = id,
        AccountId = "a1",
        FolderId = "f1",
        ThreadId = "t1",
        From = [new Address { Name = "Jana Dvořáková", Email = from }],
        Subject = "Quarterly report",
        Date = T0.AddMinutes(min),
        Snippet = "",
        Flags = [Flag.Seen],
        HasAttachments = false,
        Size = 0,
        Issue = issue,
    };

    private static ThreadSummary RailThread(int count) => new()
    {
        Id = "t1",
        AccountId = "a1",
        Subject = "Quarterly report",
        MessageCount = count,
        UnreadCount = 0,
        LatestDate = T0,
        Latest = RailMember("latest", 0),
        Snippet = "",
        HasAttachments = false,
    };

    private static MessageBodyResult Body(string id, int? remote = null) => new()
    {
        MessageId = id,
        BodyState = BodyState.Fetched,
        HasHtml = true,
        Html = "<img>",
        Text = "",
        RemoteContent = RemoteContentPolicy.Block,
        SanitizerVersion = "1",
        RemotePictures = remote,
    };
}
