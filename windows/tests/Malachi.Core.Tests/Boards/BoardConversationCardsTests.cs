// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/BoardConversationCardsTests.swift:
// what a card shows, the live web view limit and which card gives way,
// which refreshes touch the cards, and the scroll compensation for a card
// whose height changes above the viewport. The Go-only cases of
// ui/internal/board/conversation_cards_test.go follow at the end (the
// promoted newest, its cached HTML and the limit, the bounds, the owned
// lists, more compensation rows).

using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Xunit;
using static Malachi.Core.Boards.Board;
using Cards = Malachi.Core.Boards.Board.ConversationCards;
using Member = Malachi.Core.Boards.Board.ConversationCards.Member;

namespace Malachi.Core.Tests.Boards;

public sealed class BoardConversationCardsTests
{
    private static readonly BoardCaseId CaseA = new("case-a");
    private static readonly BoardCaseId CaseB = new("case-b");

    private static Member M(int n, string? text = null) => new($"m{n}", text ?? $"text {n}");

    private static Cards.Key Key(int[] ns, BoardCaseId? c = null) => new(c ?? CaseA, [.. ns.Select(n => M(n))]);

    private static Cards.Change.Members Kept(Dictionary<int, int> kept, params int[] reload) => new(kept, reload);

    [Fact]
    public void FirstCaseResetsNewestOpenOthersFolded()
    {
        var cards = new Cards();
        Assert.Equal(new Cards.Change.Reset(), cards.Apply(Key([1, 2, 3])));
        Assert.Equal(2, cards.Newest);
        Assert.True(cards.IsFolded(0) && cards.IsFolded(1) && !cards.IsFolded(2));
        Assert.Equal(Cards.Shows.Folded, cards.ShowsOf(0, live: true));
        Assert.Equal(Cards.Shows.Text, cards.ShowsOf(2, live: true));
        Assert.True(!cards.Foldable(2) && cards.Foldable(0));
        // Only the open card with an id asks.
        Assert.True(!cards.NeedsBody(0) && cards.NeedsBody(2));
    }

    [Fact]
    public void SameKeyChangesNothing()
    {
        var cards = new Cards();
        cards.Apply(Key([1, 2]));
        cards.Asked(1);
        cards.Answered(1, html: true);
        var before = cards.Clone();
        Assert.Equal(new Cards.Change.None(), cards.Apply(Key([1, 2])));
        Assert.Equal(before, cards);
        Assert.Equal(Cards.Shows.Web, cards.ShowsOf(1, live: true));
    }

    [Fact]
    public void KeyIgnoresTheRestOfTheDetail()
    {
        var a = DetailOf("", "09:00", new MessageCard("m1", "Ann", "09:00", "Hi", false));
        var b = DetailOf("Autosaved draft", "Yesterday", new MessageCard("m1", "Ann B.", "Yesterday", "Hi", true));
        Assert.Equal(Cards.KeyOf(a), Cards.KeyOf(b));
    }

    // A detail of case "case" with the members a key does not read filled in.
    private static Detail DetailOf(string draft, string time, params MessageCard[] messages) => new()
    {
        Id = "case",
        AccountId = "a1",
        State = State.You,
        StateTitle = "Waiting for You",
        Source = StateSource.Rules,
        Why = "",
        SourceText = "",
        Account = "Work",
        Person = "Ann",
        Time = time,
        Title = "Hi",
        SpokenTitle = "Hi",
        ConversationTitle = "",
        Draft = draft,
        Messages = messages,
    };

    [Fact]
    public void OtherCaseResets()
    {
        var cards = new Cards();
        cards.Apply(Key([1, 2]));
        cards.SetFolded(0, false);
        Assert.Equal(new Cards.Change.Reset(), cards.Apply(Key([1, 2], CaseB)));
        Assert.True(cards.IsFolded(0));
        Assert.Equal(Cards.Body.Unknown, cards.BodyOf(1));
    }

    [Fact]
    public void NewMessageKeepsTheRestAndOpensTheNewest()
    {
        var cards = new Cards();
        cards.Apply(Key([1, 2]));
        cards.Asked(1);
        cards.Answered(1, html: true);
        cards.SetFolded(0, false);
        Assert.Equal(Kept(new() { [0] = 0, [1] = 1 }), cards.Apply(Key([1, 2, 3])));
        // Kept as they were; the new newest opens and asks.
        Assert.True(!cards.IsFolded(0) && !cards.IsFolded(1) && !cards.IsFolded(2));
        Assert.Equal(Cards.Body.Html, cards.BodyOf(1));
        Assert.True(cards.NeedsBody(2));
    }

    [Fact]
    public void ChangedExcerptKeepsTheCardAndAsksToReloadAnOpenOne()
    {
        var cards = new Cards();
        cards.Apply(Key([1, 2, 3]));
        cards.SetFolded(0, false);
        cards.Asked(0);
        cards.Answered(0, html: true);
        cards.Asked(2);
        cards.Answered(2, html: true);
        var rebuilt = new Cards.Key(CaseA, [M(1, "edited"), M(2, "edited"), M(3)]);
        // 0 open with HTML: reloaded in place; 1 folded: new body later.
        Assert.Equal(Kept(new() { [0] = 0, [1] = 1, [2] = 2 }, 0), cards.Apply(rebuilt));
        Assert.True(!cards.IsFolded(0) && cards.IsFolded(1) && !cards.IsFolded(2));
        Assert.Equal("edited", cards.Members[0].Text);
        // The old body stays on show until the new one arrives.
        Assert.Equal(Cards.Body.Html, cards.BodyOf(0));
        Assert.Equal(Cards.Shows.Web, cards.ShowsOf(0, live: true));
        Assert.False(cards.NeedsBody(0));
        Assert.Equal(Cards.Body.Unknown, cards.BodyOf(1));
        // Opened later, the folded one asks.
        cards.SetFolded(1, false);
        Assert.True(cards.NeedsBody(1));
        // Its recency was kept: 0 is still the older of the opened.
        Assert.Equal([0, 2], cards.WebCards);
    }

    [Fact]
    public void ChangedExcerptOfAnInFlightBodyAsksAgain()
    {
        var cards = new Cards();
        cards.Apply(Key([1, 2]));
        cards.Asked(1);
        var rebuilt = new Cards.Key(CaseA, [M(1), M(2, "edited")]);
        Assert.Equal(Kept(new() { [0] = 0, [1] = 1 }), cards.Apply(rebuilt));
        Assert.Equal(Cards.Body.Unknown, cards.BodyOf(1));
        Assert.True(cards.NeedsBody(1));
    }

    [Fact]
    public void ChangedExcerptWithANewMemberAndARemovedOne()
    {
        var cards = new Cards();
        cards.Apply(Key([1, 2, 3]));
        // 2 is gone, 1 edited, 4 is new and the newest.
        var next = new Cards.Key(CaseA, [M(1, "edited"), M(3), M(4)]);
        Assert.Equal(Kept(new() { [0] = 0, [1] = 2 }), cards.Apply(next));
        Assert.True(cards.IsFolded(0) && !cards.IsFolded(2));
        Assert.True(cards.NeedsBody(2));
    }

    [Fact]
    public void RemovedMemberGoesAndRepeatedTextsKeepTheirOwnCards()
    {
        var cards = new Cards();
        var s = new Member(null, "same");
        cards.Apply(new Cards.Key(CaseA, [s, s, M(3)]));
        cards.SetFolded(1, false);
        Assert.Equal(Kept(new() { [0] = 0, [1] = 1 }), cards.Apply(new Cards.Key(CaseA, [s, s])));
        Assert.True(cards.IsFolded(0));
        // The second, opened before, is now the newest and open.
        Assert.False(cards.IsFolded(1));
    }

    [Fact]
    public void SamplesNeverAskAndStayText()
    {
        var cards = new Cards();
        cards.Apply(new Cards.Key(CaseA, [new Member(null, "a"), new Member(null, "b")]));
        Assert.False(cards.NeedsBody(1));
        Assert.Equal(Cards.Shows.Text, cards.ShowsOf(1, live: true));
        Assert.False(cards.Arrow(0, isLong: false, canFetch: true));
        Assert.True(cards.Arrow(0, isLong: true, canFetch: true));
    }

    [Fact]
    public void ArrowRules()
    {
        var cards = new Cards();
        cards.Apply(Key([1, 2]));
        Assert.True(cards.Arrow(0, isLong: false, canFetch: true));
        Assert.False(cards.Arrow(0, isLong: null, canFetch: false));
        Assert.True(cards.Arrow(0, isLong: true, canFetch: false));
        // The newest never folds.
        Assert.False(cards.Arrow(1, isLong: true, canFetch: true));
    }

    [Fact]
    public void BodyOutcomes()
    {
        var cards = new Cards();
        cards.Apply(Key([1]));
        Assert.True(cards.NeedsBody(0));
        cards.Asked(0);
        Assert.False(cards.NeedsBody(0));
        Assert.Equal(Cards.Body.Asked, cards.BodyOf(0));
        Assert.Equal(Cards.Shows.Text, cards.ShowsOf(0, live: true));
        // No HTML part, withheld, failed, gone: the excerpt.
        cards.Answered(0, html: false);
        Assert.Equal(Cards.Shows.Text, cards.ShowsOf(0, live: true));
        cards.Answered(0, html: true);
        Assert.Equal(Cards.Shows.Web, cards.ShowsOf(0, live: true));
        // Not live (hidden, out of a window): the excerpt.
        Assert.Equal(Cards.Shows.Text, cards.ShowsOf(0, live: false));
        // Asked twice changes nothing once answered.
        cards.Asked(0);
        Assert.Equal(Cards.Body.Html, cards.BodyOf(0));
    }

    [Fact]
    public void FoldedCardHoldsNoWebView()
    {
        var cards = new Cards();
        cards.Apply(Key([1, 2]));
        cards.SetFolded(0, false);
        cards.Asked(0);
        cards.Answered(0, html: true);
        Assert.Equal(Cards.Shows.Web, cards.ShowsOf(0, live: true));
        cards.SetFolded(0, true);
        Assert.Equal(Cards.Shows.Folded, cards.ShowsOf(0, live: true));
        Assert.Empty(cards.WebCards);
        // Opened again: the body is known, no second ask.
        cards.SetFolded(0, false);
        Assert.False(cards.NeedsBody(0));
        Assert.Equal(Cards.Shows.Web, cards.ShowsOf(0, live: true));
    }

    [Fact]
    public void NewestCannotBeFolded()
    {
        var cards = new Cards();
        cards.Apply(Key([1, 2]));
        Assert.Empty(cards.SetFolded(1, true));
        Assert.False(cards.IsFolded(1));
    }

    /// <summary>Six members; all opened with HTML in the order 0, 1, 2, 3, 4.</summary>
    private static (Cards Cards, List<int> Gave) OpenAll(int limit = 4)
    {
        var cards = new Cards(limit);
        cards.Apply(Key([1, 2, 3, 4, 5, 6]));
        var gave = new List<int>();
        cards.Asked(5);
        gave.AddRange(cards.Answered(5, html: true));
        for (var i = 0; i < 5; i++)
        {
            gave.AddRange(cards.SetFolded(i, false));
            cards.Asked(i);
            gave.AddRange(cards.Answered(i, html: true));
        }
        return (cards, gave);
    }

    [Fact]
    public void LimitFoldsTheLeastRecentlyOpened()
    {
        var (cards, gave) = OpenAll();
        // The newest (5) and 0, 1, 2 fill the four; 3's HTML folds 0, 4's folds 1.
        Assert.Equal([0, 1], gave);
        Assert.Equal([2, 3, 4, 5], cards.WebCards);
        Assert.True(cards.IsFolded(0) && cards.IsFolded(1));
    }

    [Fact]
    public void LimitSparesTheNewestAndTheCardJustOpened()
    {
        var cards = new Cards(maxLive: 1);
        cards.Apply(Key([1, 2, 3]));
        cards.Asked(2);
        Assert.Empty(cards.Answered(2, html: true));
        cards.SetFolded(0, false);
        cards.Asked(0);
        // Over the limit, but the newest and the card just opened stay.
        Assert.Empty(cards.Answered(0, html: true));
        Assert.Equal([0, 2], cards.WebCards);
        // Opening another: the earlier opened one gives way.
        cards.SetFolded(1, false);
        cards.Asked(1);
        Assert.Equal([0], cards.Answered(1, html: true));
    }

    [Fact]
    public void ReopeningMakesACardTheMostRecent()
    {
        var cards = new Cards(maxLive: 3);
        cards.Apply(Key([1, 2, 3, 4]));
        cards.Asked(3);
        cards.Answered(3, html: true);
        foreach (var i in new[] { 0, 1 })
        {
            cards.SetFolded(i, false);
            cards.Asked(i);
            cards.Answered(i, html: true);
        }
        // 0 folded and opened again: 1 is now the least recent.
        cards.SetFolded(0, true);
        cards.SetFolded(0, false);
        cards.SetFolded(2, false);
        cards.Asked(2);
        Assert.Equal([1], cards.Answered(2, html: true));
    }

    [Fact]
    public void TextCardsDoNotCount()
    {
        var cards = new Cards(maxLive: 1);
        cards.Apply(Key([1, 2, 3]));
        cards.Asked(2);
        cards.Answered(2, html: false);
        cards.SetFolded(0, false);
        cards.Asked(0);
        Assert.Empty(cards.Answered(0, html: true));
        cards.SetFolded(1, false);
        cards.Asked(1);
        Assert.Empty(cards.Answered(1, html: false));
        Assert.Equal([0], cards.WebCards);
    }

    [Fact]
    public void CardThatCannotShowHtmlFreesItsSlot()
    {
        var cards = new Cards(maxLive: 2);
        cards.Apply(Key([1, 2, 3, 4]));
        cards.Asked(3);
        cards.Answered(3, html: true);
        cards.SetFolded(0, false);
        cards.Asked(0);
        cards.Answered(0, html: true);
        Assert.Equal([0, 3], cards.WebCards);
        // 0's web view is unavailable: it no longer counts.
        Assert.Empty(cards.Answered(0, html: false));
        Assert.Equal([3], cards.WebCards);
        Assert.Equal(Cards.Shows.Text, cards.ShowsOf(0, live: true));
        // Another opens with HTML: nothing folds back.
        cards.SetFolded(1, false);
        cards.Asked(1);
        Assert.Empty(cards.Answered(1, html: true));
        Assert.True(!cards.IsFolded(0) && !cards.IsFolded(1));
        Assert.Equal([1, 3], cards.WebCards);
    }

    [Fact]
    public void EnforceWithNoCandidatesFoldsNothing()
    {
        var cards = new Cards(maxLive: 1);
        cards.Apply(Key([1, 2]));
        cards.Asked(1);
        cards.Answered(1, html: true);
        cards.SetFolded(0, false);
        cards.Asked(0);
        // Newest and the protected card are both spared.
        Assert.Empty(cards.Answered(0, html: true));
        Assert.Equal([0, 1], cards.WebCards);
    }

    [Theory]
    // A card above the viewport grew by 200: the viewport follows.
    [InlineData(500, 400, 200, 2000, 600, 700)]
    // Ending exactly at the viewport's top counts as above.
    [InlineData(500, 500, -100, 2000, 600, 400)]
    // In view or below: nothing above it moves.
    [InlineData(500, 800, 200, 2000, 600, 500)]
    // Clamped to the document.
    [InlineData(500, 300, 800, 1000, 600, 400)]
    [InlineData(50, 20, -300, 1000, 600, 0)]
    // Go's rows: the half-point tolerance, and a document shorter than the viewport.
    [InlineData(500, 500.5, 200, 2000, 600, 700)]
    [InlineData(500, 500.6, 200, 2000, 600, 500)]
    [InlineData(50, 20, 50, 200, 600, 0)]
    public void Compensation(double top, double bottom, double delta, double document, double viewport, double want) =>
        Assert.Equal(want, Cards.CompensatedTop(top, bottom, delta, document, viewport));

    // Go-only cases (conversation_cards_test.go).

    /// <summary>Removing the newest promotes a folded card: it opens and asks (TestConversationCardsRemovedNewestOpensPromotedCard).</summary>
    [Fact]
    public void RemovedNewestOpensPromotedCard()
    {
        var cards = new Cards();
        cards.Apply(Key([1, 2, 3]));
        Assert.Equal(Kept(new() { [0] = 0, [1] = 1 }), cards.Apply(Key([1, 2])));
        Assert.True(!cards.IsFolded(1) && !cards.Foldable(1) && cards.NeedsBody(1));
        Assert.Equal(Cards.Shows.Text, cards.ShowsOf(1, live: true));
    }

    /// <summary>A promoted newest with cached HTML counts against the limit at once (TestConversationCardsPromotedCachedHTMLRespectsLimit).</summary>
    [Fact]
    public void PromotedCachedHtmlRespectsLimit()
    {
        var cards = new Cards(2);
        cards.Apply(Key([1, 2, 3, 4]));
        foreach (var i in new[] { 0, 1, 2 })
        {
            cards.SetFolded(i, false);
            cards.Answered(i, html: true);
        }
        // 0 was evicted with its body cached; newest 3 has only an excerpt.
        Assert.Equal([1, 2], cards.WebCards);
        Assert.True(cards.IsFolded(0), "fixture: oldest was not folded");
        Assert.Equal(Kept(new() { [0] = 1, [1] = 2, [2] = 0 }), cards.Apply(Key([2, 3, 1])));
        Assert.Equal([1, 2], cards.WebCards);
        Assert.True(!cards.IsFolded(2) && !cards.NeedsBody(2), "the promoted newest reuses its cached HTML");
    }

    /// <summary>Indices out of range change nothing; the members are the model's own (TestConversationCardsBoundsAndOwnedSlices).</summary>
    [Fact]
    public void BoundsAndOwnedLists()
    {
        var cards = new Cards(-1);
        Assert.True(cards.MaxLive == 1 && cards.Newest == -1);
        foreach (var i in new[] { -1, 0, 5 })
        {
            cards.Asked(i);
            Assert.Empty(cards.Answered(i, html: true));
            Assert.Empty(cards.SetFolded(i, true));
            Assert.True(!cards.IsFolded(i) && !cards.Foldable(i) && !cards.NeedsBody(i));
            Assert.Equal(Cards.Body.Unknown, cards.BodyOf(i));
            Assert.Equal(Cards.Shows.Text, cards.ShowsOf(i, live: true));
        }
        var members = new List<Member> { M(1), M(2) };
        cards.Apply(new Cards.Key(CaseA, members));
        members[0] = M(1, "mutated");
        Assert.Equal("text 1", cards.Members[0].Text);
        Assert.Equal(1, cards.IndexOf("m2"));
        Assert.Null(cards.IndexOf("gone"));
    }
}
