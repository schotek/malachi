// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/BoardControllerTests.swift; GTK:
// ui/internal/board/controller_test.go and fixes_test.go (the controller's
// part: TestControllerArchiveOffersUndo, TestBoardViewLastUsed,
// TestSavedAccountFilter). The board's controller over an
// in-memory source: what the user looks at, what they decide about a case,
// and which Changes the page is told about. Swift's styleOnShowRule and
// styleNicks are in Boards/BoardTests.cs already (the rule is the model's).
// Swift's clock closure is a FakeTimeProvider; its calendar the fixture's
// UTC and invariant culture.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Controllers;
using Microsoft.Extensions.Time.Testing;
using Xunit;
using static Malachi.Core.Boards.Board;
using Changes = Malachi.Core.Controllers.BoardController.Changes;
using F = Malachi.Core.Tests.Boards.BoardFixture;

namespace Malachi.Core.Tests.Controllers;

public sealed class BoardControllerTests
{
    // Rows in the list under Overview: c1 hot, c3 c2 you (c3 is newer), c4 them, c5 info.
    private static List<Case> SampleCases() =>
    [
        F.Mk("c1", State.Hot, hours: 4), F.Mk("c2", State.You, hours: 3), F.Mk("c3", State.You, hours: 2),
        F.Mk("c4", State.Them, hours: 1), F.Mk("c5", State.Info, account: F.AccountB, hours: 5),
        F.Mk("d1", State.Info, hours: 6, done: true), F.Mk("d2", State.Info, hours: 7, done: true),
    ];

    private static BoardController Controller(
        IBoardSource source, FakeTimeProvider? clock = null, Func<DefaultStyle>? defaultStyle = null,
        Func<BoardStyle>? lastStyle = null, Func<string?>? savedAccount = null) =>
        new(source, clock ?? new FakeTimeProvider(F.Now), F.Culture, F.Zone, defaultStyle, lastStyle, savedAccount);

    private static (BoardController C, InMemoryBoardSource Source, List<Changes> Log) Make(
        IReadOnlyList<Case>? cases = null, bool annotated = false, FakeTimeProvider? clock = null)
    {
        var source = new InMemoryBoardSource(new Snapshot { Accounts = F.Accounts, Cases = cases ?? SampleCases(), Annotated = annotated });
        var c = Controller(source, clock);
        var log = new List<Changes>();
        c.Changed += (_, x) => log.Add(x);
        return (c, source, log);
    }

    private static string[] Rows(BoardController c) => [.. c.View.Sections.SelectMany(s => s.Rows).Select(r => r.Id.Value)];

    private static Snapshot Without(Snapshot s, string id) => s with { Cases = [.. s.Cases.Where(c => c.Id != F.Id(id))] };

    private static Snapshot With(Snapshot s, Case c) => s with { Cases = [.. s.Cases, c] };

    // Initial state

    [Fact]
    public void InitialState()
    {
        var (c, source, log) = Make();
        Assert.True(c.State.Style == BoardStyle.List && c.State.Filter == Filter.All && c.State.Account is null);
        Assert.True(c.State.InlineDetail && !c.State.RevealsWhy);
        Assert.Equal(F.Id("c1"), c.State.Selection); // the list selects its first row
        Assert.True(c.View.Selection == F.Id("c1") && c.View.Detail?.Id == F.Id("c1"));
        Assert.False(c.View.ShowsPanel);
        Assert.Equal(["c1", "c3", "c2", "c4", "c5"], Rows(c));
        Assert.NotNull(source.OnChange);
        Assert.Empty(log); // nothing is reported at construction
    }

    [Fact]
    public void EmptyBoard()
    {
        var (c, _, _) = Make([]);
        Assert.True(c.View.IsEmpty && c.State.Selection is null && c.View.Detail is null);
    }

    [Fact]
    public void ChangesBits() =>
        Assert.Equal([1, 2, 4, 8], new[] { Changes.Content, Changes.Selection, Changes.Style, Changes.Filters }.Select(x => (int)x));

    // Style

    [Fact]
    public void SetStyle()
    {
        var (c, _, log) = Make();
        c.Select(F.Id("c1")); // picked by the user: it stays, now in the panel
        log.Clear();
        c.SetStyle(BoardStyle.Columns);
        Assert.True(c.State.Style == BoardStyle.Columns && c.State.Selection == F.Id("c1") && c.View.ShowsPanel);
        Assert.Equal([Changes.Style | Changes.Selection], log);
        c.SetStyle(BoardStyle.Columns);
        Assert.Single(log); // the same style: silent
        c.SetStyle(BoardStyle.Today);
        Assert.Equal([Changes.Style | Changes.Selection, Changes.Style], log);
        c.SetStyle(BoardStyle.List); // back beside the list
        Assert.True(c.State.Selection == F.Id("c1") && !c.View.ShowsPanel);
        Assert.Equal(Changes.Style | Changes.Selection, log[^1]);
    }

    [Theory]
    [InlineData(BoardStyle.Columns)]
    [InlineData(BoardStyle.Today)]
    public void LeavingTheListKeepsTheSelection(BoardStyle style)
    {
        var (c, _, log) = Make();
        c.Select(F.Id("c3"));
        log.Clear();
        c.SetStyle(style);
        Assert.Equal(F.Id("c3"), c.State.Selection);
        Assert.True(c.View.ShowsPanel && c.View.Detail?.Id == F.Id("c3")); // the panel shows c3
        Assert.Equal([Changes.Style | Changes.Selection], log);
    }

    [Fact]
    public void LeavingTheDoneListDropsADoneSelection()
    {
        var (c, _, _) = Make();
        c.SetFilter(Filter.Done);
        Assert.Equal(F.Id("d1"), c.State.Selection);
        c.SetStyle(BoardStyle.Columns); // Columns show only live cases
        Assert.Null(c.State.Selection);
    }

    [Fact]
    public void EnteringTheListWithoutInlineDetailSelectsNothing()
    {
        var (c, _, log) = Make();
        c.SetInlineDetail(false);
        c.SetStyle(BoardStyle.Columns);
        c.Select(null);
        log.Clear();
        c.SetStyle(BoardStyle.List);
        Assert.Null(c.State.Selection);
        Assert.Equal([Changes.Style], log);
    }

    [Fact]
    public void StyleChangeKeepsWhyOfTheSameCase()
    {
        var (c, _, _) = Make();
        c.Select(F.Id("c1"));
        c.ToggleWhy();
        Assert.True(c.State.RevealsWhy);
        c.SetStyle(BoardStyle.Columns);
        Assert.True(c.State.RevealsWhy); // the case stayed
        c.Select(F.Id("c2"));
        Assert.False(c.State.RevealsWhy); // another case
    }

    /// <summary>
    /// The List's automatic first row is not kept by a style switch or a
    /// narrowing (no panel slides in by itself); an explicit pick or a live
    /// reply pane is (Go TestAutoSelectedRowIsNotKept).
    /// </summary>
    [Fact]
    public void AutoSelectedRowIsNotKept()
    {
        var (c, _, _) = Make();
        Assert.Equal(F.Id("c1"), c.State.Selection); // automatic
        Assert.False(c.KeepsSelection());
        c.SetStyle(BoardStyle.Columns);
        Assert.True(c.State.Selection is null && !c.View.ShowsPanel); // no panel for the automatic row
        c.SetStyle(BoardStyle.List);

        var (c2, _, _) = Make();
        c2.SetInlineDetail(false);
        Assert.True(c2.State.Selection is null && !c2.View.ShowsPanel); // narrowing opened no panel

        var (c3, _, _) = Make();
        c3.PaneLive = id => id == F.Id("c1");
        Assert.True(c3.KeepsSelection());
        c3.SetStyle(BoardStyle.Columns);
        Assert.True(c3.State.Selection == F.Id("c1") && c3.View.ShowsPanel); // a live pane keeps its case
    }

    // Filters

    [Fact]
    public void SetFilter()
    {
        var (c, _, log) = Make();
        c.SetFilter(Filter.Of(State.You));
        Assert.Equal(Filter.Of(State.You), c.State.Filter);
        Assert.Equal(F.Id("c3"), c.State.Selection); // the first row of the filter
        Assert.Equal([Changes.Filters | Changes.Selection | Changes.Content], log);
        c.SetFilter(Filter.Of(State.You));
        Assert.Single(log);
        c.SetFilter(Filter.Done);
        Assert.Equal(F.Id("d1"), c.State.Selection);
        Assert.Equal(["d1", "d2"], Rows(c));
        c.SetFilter(Filter.Of(State.Info)); // c5 is the live info case
        Assert.Equal(F.Id("c5"), c.State.Selection);
    }

    [Fact]
    public void FilterWithNothingSelectsNothing()
    {
        var (c, _, _) = Make([F.Mk("c1", State.You)]);
        c.SetFilter(Filter.Of(State.Hot));
        Assert.True(c.State.Selection is null && c.View.Detail is null && c.View.Sections.Count == 0);
    }

    [Fact]
    public void FilterInColumnsLeavesNoSelection()
    {
        var (c, _, log) = Make();
        c.SetStyle(BoardStyle.Columns);
        c.Select(null);
        log.Clear();
        c.SetFilter(Filter.Of(State.Hot));
        Assert.Null(c.State.Selection);
        Assert.Equal([Changes.Filters | Changes.Content], log); // the list behind it changed, the columns did not
        Assert.Equal(4, c.View.Columns.Count);
    }

    [Fact]
    public void SetAccount()
    {
        var (c, _, log) = Make();
        c.SetAccount(F.AccountB);
        Assert.Equal(F.AccountB, c.State.Account);
        Assert.True(Rows(c).SequenceEqual(["c5"]) && c.State.Selection == F.Id("c5"));
        Assert.Equal("Beta · 1 case", c.View.Subtitle);
        Assert.Equal([Changes.Filters | Changes.Selection | Changes.Content], log);
        c.SetAccount(F.AccountB);
        Assert.Single(log);
        c.SetAccount(null);
        Assert.Equal(F.Id("c1"), c.State.Selection);
        Assert.Equal(2, log.Count);
    }

    [Fact]
    public void FilterChangeResetsWhy()
    {
        var (c, _, _) = Make();
        c.ToggleWhy();
        c.SetFilter(Filter.Of(State.You));
        Assert.False(c.State.RevealsWhy);
        c.ToggleWhy();
        c.SetAccount(F.AccountB);
        Assert.False(c.State.RevealsWhy);
    }

    // Selection

    [Fact]
    public void Select()
    {
        var (c, _, log) = Make();
        c.Select(F.Id("c3"));
        Assert.True(c.State.Selection == F.Id("c3") && c.View.Detail?.Id == F.Id("c3"));
        Assert.Equal([Changes.Selection], log); // another case's detail is no content change
        c.Select(F.Id("c3"));
        Assert.Single(log);
        // A case that is not shown resolves like nothing: the first row.
        c.Select(F.Id("nope"));
        Assert.Equal(F.Id("c1"), c.State.Selection);
        c.Select(F.Id("d1")); // done, and the list shows live cases
        Assert.Equal(F.Id("c1"), c.State.Selection);
        c.Select(null);
        Assert.Equal(F.Id("c1"), c.State.Selection);
        Assert.Equal([Changes.Selection, Changes.Selection], log);
    }

    [Fact]
    public void SelectInColumnsOpensThePanel()
    {
        var (c, _, log) = Make();
        c.SetStyle(BoardStyle.Columns);
        log.Clear();
        c.Select(F.Id("c4"));
        Assert.True(c.State.Selection == F.Id("c4") && c.View.ShowsPanel);
        Assert.Equal([Changes.Selection], log);
        c.Select(null);
        Assert.True(c.State.Selection is null && !c.View.ShowsPanel);
        Assert.Equal([Changes.Selection, Changes.Selection], log);
        c.Select(null);
        Assert.Equal(2, log.Count);
        c.Select(F.Id("d1")); // done: not on the board
        Assert.Null(c.State.Selection);
    }

    [Fact]
    public void ToggleWhyAndItsReset()
    {
        var (c, _, log) = Make();
        c.ToggleWhy();
        Assert.True(c.State.RevealsWhy && c.View.Detail is not null);
        Assert.Equal([Changes.Selection], log);
        c.Select(F.Id("c1")); // the same case: stays open
        Assert.True(c.State.RevealsWhy && log.Count == 1);
        c.Select(F.Id("c2"));
        Assert.False(c.State.RevealsWhy);
        Assert.Equal([Changes.Selection, Changes.Selection], log);
        c.ToggleWhy();
        c.ToggleWhy();
        Assert.True(!c.State.RevealsWhy && log.Count == 4);
    }

    [Fact]
    public void ToggleWhyNeedsASelection()
    {
        var (c, _, log) = Make();
        c.SetStyle(BoardStyle.Columns);
        c.Select(null);
        log.Clear();
        c.ToggleWhy();
        Assert.True(!c.State.RevealsWhy && log.Count == 0);
    }

    [Fact]
    public void SetInlineDetail()
    {
        var (c, _, log) = Make();
        c.SetInlineDetail(true);
        Assert.Empty(log);
        c.Select(F.Id("c1"));
        log.Clear();
        c.SetInlineDetail(false); // narrow: the selected case moves to the panel
        Assert.True(!c.State.InlineDetail && c.State.Selection == F.Id("c1") && c.View.ShowsPanel);
        Assert.Equal([Changes.Selection], log);
        c.Select(F.Id("c2"));
        Assert.True(c.View.ShowsPanel);
        c.SetInlineDetail(true); // wide again: beside the list, the selection stays
        Assert.True(c.State.Selection == F.Id("c2") && !c.View.ShowsPanel);
        Assert.Equal([Changes.Selection, Changes.Selection, Changes.Selection], log);
        c.SetInlineDetail(false);
        c.Select(null); // the panel closed
        c.SetInlineDetail(true); // nothing selected: the first row
        Assert.Equal(F.Id("c1"), c.State.Selection);
    }

    [Fact]
    public void ShowWaitingForYou()
    {
        var (c, _, log) = Make();
        c.SetAccount(F.AccountA);
        c.SetStyle(BoardStyle.Columns);
        log.Clear();
        c.ShowWaitingForYou();
        Assert.True(c.State.Style == BoardStyle.List && c.State.Filter == Filter.Of(State.You));
        Assert.Equal(F.AccountA, c.State.Account); // kept
        Assert.Equal(F.Id("c3"), c.State.Selection);
        Assert.Equal([Changes.Style | Changes.Filters | Changes.Selection | Changes.Content], log);
        // Already there: the selection stays and nothing is reported.
        c.Select(F.Id("c2"));
        log.Clear();
        c.ShowWaitingForYou();
        Assert.True(c.State.Selection == F.Id("c2") && log.Count == 0);
    }

    // What the user decides

    [Fact]
    public void SetStateMovesTheCase()
    {
        var (c, source, log) = Make();
        c.SetState(State.Them, F.Id("c3"));
        Assert.Equal(State.Them, source.Snapshot.FindCase(F.Id("c3"))?.UserState);
        Assert.Equal(["state(Hot)", "state(You)", "state(Them)", "state(Info)"], c.View.Sections.Select(F.KindOf));
        Assert.Equal(["c1", "c2", "c4", "c3", "c5"], Rows(c));
        Assert.Equal([5, 1, 1, 2, 1, 0, 2], c.View.Nav.Select(n => n.Count));
        Assert.Equal(F.Id("c1"), c.State.Selection);
        Assert.Equal([Changes.Content], log);
        c.SetState(State.Them, F.Id("c3")); // nothing changes
        c.SetState(State.Hot, F.Id("missing"));
        Assert.Single(log);
    }

    [Fact]
    public void MovingTheSelectedCaseKeepsItSelectedWhileItIsShown()
    {
        var (c, _, log) = Make();
        c.Select(F.Id("c3"));
        log.Clear();
        c.SetState(State.Info, F.Id("c3"));
        Assert.Equal(F.Id("c3"), c.State.Selection);
        Assert.True(c.View.Detail?.State == State.Info && c.View.Detail?.Source == StateSource.User);
        Assert.Equal([Changes.Content], log); // the same case's changed detail is content
    }

    [Fact]
    public void MovingTheSelectedCaseOutOfTheFilterSelectsTheNextRow()
    {
        var (c, _, log) = Make();
        c.SetFilter(Filter.Of(State.You));
        Assert.Equal(F.Id("c3"), c.State.Selection);
        log.Clear();
        c.SetState(State.Hot, F.Id("c3"));
        Assert.True(Rows(c).SequenceEqual(["c2"]) && c.State.Selection == F.Id("c2"));
        Assert.Equal([Changes.Selection | Changes.Content], log);
        c.SetState(State.Them, F.Id("c2")); // the last row: nothing is left
        Assert.True(c.State.Selection is null && c.View.Detail is null);
    }

    [Fact]
    public void SetStateBackToAutomatic()
    {
        var ann = new Annotation { State = State.Hot, Title = "T" };
        var (c, source, _) = Make([F.Mk("c1", State.You, annotation: ann)], annotated: true);
        State? User() => source.Snapshot.Cases[0].UserState;
        // The automatic state is the assistant's (hot), not the rules' (you).
        c.SetState(State.Them, F.Id("c1"));
        Assert.Equal(State.Them, User());
        c.SetState(State.Hot, F.Id("c1")); // the same as automatic: back to automatic
        Assert.True(User() is null && c.View.Detail?.Source == StateSource.AssistantChanged(State.You));
        c.SetState(State.You, F.Id("c1")); // the rules' state is not automatic here: the user's choice
        Assert.True(User() == State.You && c.View.Detail?.Source == StateSource.User);
        c.SetState(State.Hot, F.Id("c1"));
        Assert.Null(User());

        // Without annotations the rules decide.
        var (d, src2, _) = Make([F.Mk("c1", State.You, annotation: ann)], annotated: false);
        d.SetState(State.Them, F.Id("c1"));
        d.SetState(State.You, F.Id("c1"));
        Assert.Null(src2.Snapshot.Cases[0].UserState);
        d.SetState(State.Hot, F.Id("c1"));
        Assert.Equal(State.Hot, src2.Snapshot.Cases[0].UserState);
    }

    [Fact]
    public void MarkDoneInTheListSelectsTheNextRow()
    {
        var (c, source, log) = Make();
        c.Select(F.Id("c3"));
        log.Clear();
        c.MarkDone(F.Id("c3"));
        Assert.True(source.Snapshot.FindCase(F.Id("c3"))?.Done);
        Assert.Equal(["c1", "c2", "c4", "c5"], Rows(c));
        Assert.Equal(F.Id("c2"), c.State.Selection); // the row that followed
        Assert.Equal([Changes.Selection | Changes.Content], log);
        c.Select(F.Id("c5"));
        c.MarkDone(F.Id("c5")); // the last row: the previous one
        Assert.Equal(F.Id("c4"), c.State.Selection);
        c.Select(F.Id("c1"));
        c.MarkDone(F.Id("c1")); // the first row: the next
        Assert.Equal(F.Id("c2"), c.State.Selection);
    }

    [Fact]
    public void MarkDoneTheOnlyRow()
    {
        var (c, _, _) = Make([F.Mk("c1")]);
        c.MarkDone(F.Id("c1"));
        Assert.True(c.State.Selection is null && c.View.Detail is null && c.View.Sections.Count == 0);
        Assert.False(c.View.IsEmpty); // a done case still counts
    }

    [Fact]
    public void MarkDoneUnderAFilter()
    {
        var (c, _, _) = Make();
        c.SetFilter(Filter.Of(State.You));
        c.MarkDone(F.Id("c3"));
        Assert.Equal(F.Id("c2"), c.State.Selection);
        c.MarkDone(F.Id("c2"));
        Assert.Null(c.State.Selection);
    }

    [Theory]
    [InlineData(BoardStyle.Columns)]
    [InlineData(BoardStyle.Today)]
    public void MarkDoneInColumnsAndTodayClearsTheSelection(BoardStyle style)
    {
        var (c, _, log) = Make();
        c.SetStyle(style);
        c.Select(F.Id("c2"));
        Assert.True(c.View.ShowsPanel);
        log.Clear();
        c.MarkDone(F.Id("c2"));
        Assert.True(c.State.Selection is null && !c.View.ShowsPanel && c.View.Detail is null);
        Assert.Equal([Changes.Selection | Changes.Content], log);
    }

    [Fact]
    public void MarkDoneAnotherCaseKeepsTheSelection()
    {
        var (c, _, log) = Make();
        c.MarkDone(F.Id("c4"));
        Assert.Equal(F.Id("c1"), c.State.Selection);
        Assert.Equal([Changes.Content], log);
        c.MarkDone(F.Id("c4")); // already done: silent
        c.MarkDone(F.Id("missing"));
        Assert.Single(log);
    }

    [Fact]
    public void Reopen()
    {
        var (c, source, log) = Make();
        c.SetFilter(Filter.Done);
        Assert.Equal(F.Id("d1"), c.State.Selection);
        log.Clear();
        c.Reopen(F.Id("d1"));
        Assert.False(source.Snapshot.FindCase(F.Id("d1"))?.Done);
        Assert.True(Rows(c).SequenceEqual(["d2"]) && c.State.Selection == F.Id("d2"));
        Assert.Equal([Changes.Selection | Changes.Content], log);
        c.Reopen(F.Id("d2"));
        Assert.True(c.State.Selection is null && c.View.Sections.Count == 0);
        // The reopened cases are back on the board.
        c.SetFilter(Filter.All);
        Assert.True(Rows(c).Contains("d1") && Rows(c).Contains("d2"));
    }

    [Fact]
    public void ReopenFromAnotherViewKeepsTheSelection()
    {
        var (c, _, log) = Make();
        c.Reopen(F.Id("d1")); // from the list under Overview: a new row appears
        Assert.Equal(F.Id("c1"), c.State.Selection);
        Assert.Equal([Changes.Content], log);
    }

    [Fact]
    public void DiscardDraft()
    {
        var ann = new Annotation { State = State.You, Title = "T" };
        var (c, source, log) = Make([F.Mk("c1", annotation: ann, draft: "Hi,\n\nbye"), F.Mk("c2", State.You, hours: 2)], annotated: true);
        Assert.Equal("Hi,\n\nbye", c.View.Detail?.Draft);
        c.DiscardDraft(F.Id("c1"));
        Assert.True(source.Snapshot.Cases[0].Draft is null && source.Snapshot.Cases[0].Annotation == ann);
        Assert.True(c.View.Detail?.Draft == "" && c.View.Detail?.Title == "T");
        Assert.Equal([Changes.Content], log);
        c.DiscardDraft(F.Id("c1")); // nothing left to discard
        c.DiscardDraft(F.Id("c2")); // no draft
        c.DiscardDraft(F.Id("missing"));
        Assert.Single(log);
    }

    // Refresh and the source changing from outside

    [Fact]
    public void RefreshWithoutChangeIsSilent()
    {
        var (c, _, log) = Make();
        var before = c.View;
        c.Refresh();
        Assert.True(c.View == before && log.Count == 0);
    }

    [Fact]
    public void RefreshFollowsTheClock()
    {
        var clock = new FakeTimeProvider(F.Now);
        var ann = new Annotation { State = State.You, Title = "T", Due = F.Day(16, 10) };
        var (c, _, log) = Make([F.Mk("c1", annotation: ann)], annotated: true, clock: clock);
        Assert.Equal("Tomorrow", c.View.Sections[0].Rows[0].Due);
        Assert.Equal([DueGroupKind.Tomorrow], c.View.Today.DueGroups.Select(g => g.Kind));
        clock.SetUtcNow(F.Day(16, 9)); // a new day moves the deadline
        c.Refresh();
        Assert.Equal("Today", c.View.Sections[0].Rows[0].Due);
        Assert.Equal([DueGroupKind.Today], c.View.Today.DueGroups.Select(g => g.Kind));
        Assert.Equal([Changes.Content], log);
    }

    [Fact]
    public void ASourceChangeFromOutside()
    {
        var (c, source, log) = Make();
        // A new, newer hot case: content only, the selection stays.
        var s = With(source.Snapshot, F.Mk("c6", State.Hot, hours: 0.5));
        source.Replace(s);
        Assert.True(Rows(c)[0] == "c6" && c.State.Selection == F.Id("c1"));
        Assert.Equal([Changes.Content], log);
        // The same snapshot again: silent.
        source.Replace(s);
        Assert.Single(log);
        // The selected case disappears: the first row.
        source.Replace(Without(s, "c1"));
        Assert.Equal(F.Id("c6"), c.State.Selection);
        Assert.Equal(Changes.Selection | Changes.Content, log[^1]);
    }

    [Fact]
    public void AWriteStraightToTheSourceIsFollowed()
    {
        var (c, source, log) = Make();
        source.SetDone(true, F.Id("c1")); // not through the controller
        Assert.True(c.State.Selection == F.Id("c3") && log.SequenceEqual([Changes.Selection | Changes.Content]));
        source.SetState(State.Them, F.Id("c2"));
        Assert.Equal([4, 0, 1, 2, 1, 0, 3], c.View.Nav.Select(n => n.Count));
    }

    [Fact]
    public void ReplaceCanEmptyTheBoard()
    {
        var (c, source, log) = Make();
        c.Select(F.Id("c3"));
        log.Clear();
        source.Replace(Snapshot.Empty);
        Assert.True(c.View.IsEmpty && c.State.Selection is null && c.View.Detail is null);
        Assert.Equal([Changes.Selection | Changes.Content], log);
        // Data arriving later fills it again.
        source.Replace(new Snapshot { Accounts = F.Accounts, Cases = SampleCases() });
        Assert.True(!c.View.IsEmpty && c.State.Selection == F.Id("c1"));
    }

    [Fact]
    public void AWriteToAnUnknownCaseCallsNoOne()
    {
        var (c, _, log) = Make();
        c.SetState(State.Hot, F.Id("nope"));
        c.MarkDone(F.Id("nope"));
        c.Reopen(F.Id("nope"));
        c.DiscardDraft(F.Id("nope"));
        Assert.True(log.Count == 0 && c.State.Selection == F.Id("c1"));
    }

    // The departure after a write applies once

    /// <summary>
    /// The selected case moved where it stays shown, then dropped by the
    /// source: the rule for any outside change, not the neighbour noted
    /// before the move.
    /// </summary>
    [Theory]
    [InlineData(BoardStyle.List, true, "c1")]
    [InlineData(BoardStyle.List, false, null)]
    [InlineData(BoardStyle.Columns, true, null)]
    [InlineData(BoardStyle.Today, true, null)]
    public void ADepartureDoesNotOutliveTheReportOfItsWrite(BoardStyle style, bool inline, string? want)
    {
        var (c, source, _) = Make();
        c.SetStyle(style);
        c.SetInlineDetail(inline);
        c.Select(F.Id("c3"));
        c.SetState(State.Info, F.Id("c3")); // still shown
        Assert.Equal(F.Id("c3"), c.State.Selection);
        source.Replace(Without(source.Snapshot, "c3"));
        Assert.Equal(want, c.State.Selection?.Value);
    }

    [Fact]
    public void AWriteThatChangesNothingNotesNoDeparture()
    {
        var (c, source, log) = Make();
        c.Select(F.Id("c3"));
        log.Clear();
        c.SetState(State.You, F.Id("c3")); // its automatic state already: nothing to write
        c.MarkDone(F.Id("d1")); // done already
        c.Reopen(F.Id("c3")); // not done
        Assert.Empty(log);
        source.Replace(Without(source.Snapshot, "c3"));
        Assert.Equal(F.Id("c1"), c.State.Selection); // the first row, not c2
    }

    [Fact]
    public void ASourceThatReportsLater()
    {
        // Nothing in between: the departure waits for the report.
        {
            var source = new LateSource(new Snapshot { Accounts = F.Accounts, Cases = SampleCases() });
            var c = Controller(source);
            c.Select(F.Id("c3"));
            c.MarkDone(F.Id("c3"));
            Assert.Equal(F.Id("c3"), c.State.Selection); // not reported yet
            source.Flush();
            Assert.True(c.State.Selection == F.Id("c2") && !Rows(c).Contains("c3"));
        }
        // The user selects another case meanwhile: their choice wins.
        {
            var source = new LateSource(new Snapshot { Accounts = F.Accounts, Cases = SampleCases() });
            var c = Controller(source);
            c.Select(F.Id("c3"));
            c.MarkDone(F.Id("c3"));
            c.Select(F.Id("c4"));
            source.Flush();
            Assert.Equal(F.Id("c4"), c.State.Selection);
            // And a later report removing c4 follows the rule for outside changes.
            source.Snapshot = Without(source.Snapshot, "c4");
            source.Flush();
            Assert.Equal(F.Id("c1"), c.State.Selection);
        }
    }

    [Fact]
    public void MarkDoneInTheNarrowList()
    {
        var (c, source, log) = Make();
        c.SetInlineDetail(false);
        c.Select(F.Id("c3"));
        Assert.True(c.View.ShowsPanel);
        log.Clear();
        c.MarkDone(F.Id("c3"));
        Assert.True(c.State.Selection == F.Id("c2") && c.View.ShowsPanel); // the panel moves to the next row
        Assert.Equal([Changes.Selection | Changes.Content], log);
        // An unrelated change later keeps it; dropping it selects nothing (no first row here).
        var s = With(source.Snapshot, F.Mk("c6", State.Hot, hours: 0.5));
        source.Replace(s);
        Assert.Equal(F.Id("c2"), c.State.Selection);
        source.Replace(Without(s, "c2"));
        Assert.True(c.State.Selection is null && !c.View.ShowsPanel);
    }

    // An account filter whose account goes away

    [Fact]
    public void AVanishedAccountFallsBackToAll()
    {
        var (c, source, log) = Make();
        c.SetAccount(F.AccountB);
        Assert.True(c.State.Account == F.AccountB && Rows(c).SequenceEqual(["c5"]));
        log.Clear();
        var s = source.Snapshot;
        source.Replace(s with
        {
            Accounts = [.. s.Accounts.Where(a => a.Id != F.AccountB)],
            Cases = [.. s.Cases.Where(k => k.Account != F.AccountB)],
        });
        Assert.Null(c.State.Account);
        Assert.True(Rows(c).SequenceEqual(["c1", "c3", "c2", "c4"]) && c.State.Selection == F.Id("c1"));
        Assert.All(c.View.Accounts, a => Assert.NotEqual(F.AccountB, a.Filter));
        Assert.True(c.View.Accounts[0].Selected && c.View.AccountTitle == "All Accounts");
        Assert.Equal([Changes.Filters | Changes.Selection | Changes.Content], log);
        // Choosing an account that is not there is no change.
        c.SetAccount(F.AccountB);
        Assert.True(c.State.Account is null && log.Count == 1);
    }

    // Calls from inside Changed

    [Fact]
    public void AListenerThatSelectsGetsItsChangeAfterwards()
    {
        var source = new InMemoryBoardSource(new Snapshot { Accounts = F.Accounts, Cases = SampleCases() });
        var c = Controller(source);
        c.Select(F.Id("c1"));
        var log = new List<(Changes Changes, string? Selection)>();
        var nested = false;
        c.Changed += (_, changes) =>
        {
            log.Add((changes, c.View.Selection?.Value));
            Assert.Equal(c.State.Selection, c.View.Selection);
            if (!nested)
            {
                nested = true;
                c.Select(F.Id("c4"));
                // Not yet delivered: this call is still the outer one.
                Assert.Single(log);
            }
        };
        c.SetStyle(BoardStyle.Columns);
        Assert.Equal(2, log.Count);
        Assert.True(log[0].Changes == (Changes.Style | Changes.Selection) && log[0].Selection == "c1"); // the case stays, in the panel
        Assert.True(log[1].Changes == Changes.Selection && log[1].Selection == "c4");
        Assert.Equal(F.Id("c4"), c.State.Selection);
    }

    [Fact]
    public void AListenerThatMarksDoneGetsItsChangeAfterwards()
    {
        var source = new InMemoryBoardSource(new Snapshot { Accounts = F.Accounts, Cases = SampleCases() });
        var c = Controller(source);
        var log = new List<(Changes Changes, string? Selection, string[] Rows)>();
        var nested = false;
        c.Changed += (_, changes) =>
        {
            log.Add((changes, c.View.Selection?.Value, Rows(c)));
            if (!nested)
            {
                nested = true;
                c.MarkDone(F.Id("c3"));
                Assert.Single(log);
            }
        };
        c.Select(F.Id("c3"));
        Assert.Equal(2, log.Count);
        Assert.True(log[0].Changes == Changes.Selection && log[0].Selection == "c3" && log[0].Rows.Contains("c3"));
        Assert.True(log[1].Changes == (Changes.Selection | Changes.Content) && log[1].Selection == "c2" && !log[1].Rows.Contains("c3"));
    }

    [Fact]
    public void DummySources()
    {
        var on = InMemoryBoardSource.Dummy(true, F.Now, F.Zone);
        Assert.Equal(SampleSnapshot(F.Now, F.Zone), on.Snapshot);
        var off = InMemoryBoardSource.Dummy(false, F.Now, F.Zone);
        Assert.Equal(Snapshot.Empty, off.Snapshot);
        var c = Controller(on);
        Assert.True(c.State.Selection is not null && c.View.Detail is not null);
    }

    // Remind, archive, promises, the conversation, toasts

    [Fact]
    public void RemindTakesTheCaseOffLikeDone()
    {
        var (c, source, log) = Make();
        c.Select(F.Id("c3"));
        log.Clear();
        var until = F.Day(16, 9);
        c.Remind(F.Id("c3"), until);
        Assert.Equal(Visibility.Snoozed(until), source.Snapshot.FindCase(F.Id("c3"))?.Visibility);
        Assert.Equal(["c1", "c2", "c4", "c5"], Rows(c));
        Assert.Equal(F.Id("c2"), c.State.Selection);
        Assert.Equal([Changes.Selection | Changes.Content], log);
        // The same remind again changes nothing.
        c.Remind(F.Id("c3"), until);
        Assert.Single(log);
        // Under Done, ending the remind puts it back and moves on.
        c.SetFilter(Filter.Done);
        c.Select(F.Id("c3"));
        c.Remind(F.Id("c3"), null);
        Assert.Equal(Visibility.Live, source.Snapshot.FindCase(F.Id("c3"))?.Visibility);
        Assert.Equal(F.Id("d1"), c.State.Selection);
    }

    [Fact]
    public void RemindAPresetFromTheController()
    {
        var (c, _, _) = Make();
        Assert.Equal(RemindPresets(F.Now, F.Culture, F.Zone), c.RemindPresets());
    }

    [Fact]
    public void ArchiveMarksDoneAndToasts()
    {
        var cases = SampleCases();
        cases[2] = cases[2] with { CanArchive = true }; // c3
        var (c, source, _) = Make(cases);
        var toasts = new List<string>();
        c.ToastRequested += (_, t) => toasts.Add(t);
        c.Select(F.Id("c3"));
        c.Archive(F.Id("c3"));
        Assert.True(source.Snapshot.FindCase(F.Id("c3"))?.Done);
        Assert.Equal(F.Id("c2"), c.State.Selection);
        Assert.Equal(["Archived 1 message."], toasts);
        c.Archive(F.Id("c2"));
        Assert.Equal("Marked as done. This account has no archive.", toasts[^1]);
    }

    [Fact]
    public void SetCommitmentDone()
    {
        var k = new Commitment { Id = new BoardCommitmentId("k1"), CaseId = F.Id("c1"), Text = "Promise" };
        var source = new InMemoryBoardSource(new Snapshot { Accounts = F.Accounts, Cases = SampleCases(), Commitments = [k], Annotated = true });
        var c = Controller(source);
        Assert.Equal(["k1"], c.View.Commitments.Select(x => x.Id.Value));
        c.SetCommitmentDone(new BoardCommitmentId("k1"), done: true);
        Assert.True(source.Snapshot.Commitments[0].State == CommitmentState.Done && c.View.Commitments.Count == 0);
        c.SetCommitmentDone(new BoardCommitmentId("k1"), done: false);
        Assert.Equal(["k1"], c.View.Commitments.Select(x => x.Id.Value));
    }

    /// <summary>Selecting a case asks for its conversation once per case and version.</summary>
    [Fact]
    public void SelectionLoadsTheConversation()
    {
        var cases = SampleCases().Select(k => k with { Messages = null }).ToList();
        var source = new LateSource(new Snapshot { Accounts = F.Accounts, Cases = cases });
        var c = Controller(source);
        Assert.Equal([F.Id("c1")], source.Loads); // the list's first row, at once
        Assert.True(c.View.Detail?.MessagesLoading);
        c.Select(F.Id("c3"));
        c.Refresh();
        Assert.Equal([F.Id("c1"), F.Id("c3")], source.Loads);
        // A new version of the selected case asks again.
        source.Change(2, k => k with { Version = 2 });
        source.Flush();
        Assert.Equal([F.Id("c1"), F.Id("c3"), F.Id("c3")], source.Loads);
        // Back to c1: asked again (the source knows whether it has it).
        c.Select(F.Id("c1"));
        Assert.True(source.Loads[^1] == F.Id("c1") && source.Loads.Count == 4);
        // Nothing selected asks for nothing.
        c.SetStyle(BoardStyle.Columns);
        Assert.Equal(4, source.Loads.Count);
    }

    [Fact]
    public void SourceToastsReachThePage()
    {
        var (c, source, _) = Make();
        var toasts = new List<string>();
        c.ToastRequested += (_, t) => toasts.Add(t);
        source.OnError?.Invoke("bad");
        source.OnNotice?.Invoke("good");
        Assert.Equal(["bad", "good"], toasts);
    }

    [Fact]
    public void PhaseAndTriageInTheView()
    {
        var source = new InMemoryBoardSource(new Snapshot
        {
            Accounts = F.Accounts,
            Phase = Phase.Preparing,
            Triage = new Triage { Queue = 3, AnnotatedToday = 1 },
        });
        var c = Controller(source);
        Assert.True(c.Phase == Phase.Preparing && c.View.Phase == Phase.Preparing);
        Assert.Equal(Board.Text.EmptyTitleOf(Phase.Preparing), c.View.EmptyTitle);
        Assert.Equal(3, c.View.Triage.Queue);
        source.Replace(source.Snapshot with { Phase = Phase.Ready });
        Assert.True(c.View.Phase == Phase.Ready && c.View.EmptyTitle == Board.Text.EmptyTitle);
    }

    /// <summary>
    /// A conversation that could not be loaded is asked for again when the
    /// case is selected again, when the board comes back from a failure, and
    /// from the detail's Try Again; not on every report.
    /// </summary>
    [Fact]
    public void AFailedConversationIsAskedForAgain()
    {
        var cases = SampleCases().Select(k => k with { Messages = null }).ToList();
        var source = new LateSource(new Snapshot { Accounts = F.Accounts, Cases = cases });
        var c = Controller(source);
        Assert.Equal([F.Id("c1")], source.Loads);
        // The load fails.
        source.Change(0, k => k with { MessagesFailed = true });
        source.Flush();
        Assert.True(c.View.Detail?.MessagesRetry == true && c.View.Detail?.MessagesNote == Board.Text.MessagesFailed);
        Assert.Single(source.Loads); // not by itself
        // Selected again: asked again.
        c.Select(F.Id("c1"));
        Assert.Equal([F.Id("c1"), F.Id("c1")], source.Loads);
        source.Flush();
        Assert.Equal(2, source.Loads.Count);
        // Try Again.
        c.RetryMessages();
        Assert.Equal(3, source.Loads.Count);
        // The connection goes and comes back with the same version: asked
        // again once the board is back.
        source.Snapshot = source.Snapshot with { Phase = Phase.Unavailable };
        source.Flush();
        Assert.Equal(3, source.Loads.Count);
        source.Snapshot = source.Snapshot with { Phase = Phase.Ready };
        source.Flush();
        Assert.Equal(4, source.Loads.Count);
        source.Flush();
        Assert.Equal(4, source.Loads.Count);
        // Loaded: Try Again and selecting it again ask for nothing more.
        source.Change(0, k => k with { MessagesFailed = false, Messages = [] });
        source.Flush();
        Assert.False(c.View.Detail?.MessagesRetry);
        c.RetryMessages();
        c.Select(F.Id("c1"));
        Assert.Equal(4, source.Loads.Count);
    }

    /// <summary>Entering the board while it could not be listed asks for it again.</summary>
    [Fact]
    public void BoardShownRetriesAFailedBoard()
    {
        var source = new LateSource(new Snapshot { Accounts = F.Accounts, Cases = SampleCases() });
        var c = Controller(source);
        c.BoardShown();
        Assert.Equal(0, source.Refreshes);
        foreach (var phase in new[] { Phase.Unavailable, Phase.Failed, Phase.Unsupported })
        {
            source.Snapshot = source.Snapshot with { Phase = phase };
            source.Flush();
            var before = source.Refreshes;
            c.BoardShown();
            Assert.Equal(before + 1, source.Refreshes);
        }
    }

    // The default style (Preferences → General → Board)

    /// <summary>The style of the first show is the setting's, read at that moment.</summary>
    [Fact]
    public void DefaultStyleAtFirstShow()
    {
        var setting = DefaultStyle.Columns;
        var source = new InMemoryBoardSource(new Snapshot { Accounts = F.Accounts, Cases = SampleCases() });
        var c = Controller(source, defaultStyle: () => setting);
        var log = new List<Changes>();
        c.Changed += (_, x) => log.Add(x);
        // Until the board shows it holds the List.
        Assert.Equal(BoardStyle.List, c.State.Style);
        Assert.False(c.HasShown);
        // Changed before the first show: that one counts.
        setting = DefaultStyle.Today;
        c.BoardWillShow();
        Assert.True(c.HasShown);
        Assert.Equal(BoardStyle.Today, c.State.Style);
        Assert.Contains(log, x => x.HasFlag(Changes.Style));
        // The default List changes nothing.
        var list = Make();
        list.C.BoardWillShow();
        Assert.Equal(BoardStyle.List, list.C.State.Style);
        Assert.Empty(list.Log);
        // An unknown stored nick is Last Used, whose style the List is by default.
        var odd = Controller(
            new InMemoryBoardSource(new Snapshot { Accounts = F.Accounts, Cases = SampleCases() }),
            defaultStyle: () => ParseDefaultStyle("grid"));
        odd.BoardWillShow();
        Assert.Equal(BoardStyle.List, odd.State.Style);
    }

    /// <summary>
    /// After the first show the style is the user's: leaving and entering
    /// again keeps it, and so does a setting changed meanwhile.
    /// </summary>
    [Fact]
    public void LaterShowsKeepTheUsersStyle()
    {
        var setting = DefaultStyle.Columns;
        var source = new InMemoryBoardSource(new Snapshot { Accounts = F.Accounts, Cases = SampleCases() });
        var c = Controller(source, defaultStyle: () => setting);
        c.BoardWillShow();
        c.BoardShown();
        Assert.Equal(BoardStyle.Columns, c.State.Style);
        c.SetStyle(BoardStyle.Today);
        // Back from Mail.
        c.BoardWillShow();
        c.BoardShown();
        Assert.Equal(BoardStyle.Today, c.State.Style);
        // The setting changes after the user picked a style: the style stays.
        setting = DefaultStyle.List;
        c.BoardWillShow();
        Assert.Equal(BoardStyle.Today, c.State.Style);
        setting = DefaultStyle.Columns;
        c.BoardWillShow();
        Assert.Equal(BoardStyle.Today, c.State.Style);
        // The user's own choice still works.
        c.SetStyle(BoardStyle.List);
        c.BoardWillShow();
        Assert.Equal(BoardStyle.List, c.State.Style);
    }

    /// <summary>
    /// Board View's Last Used takes the style used last; a chosen one applies
    /// until the user picks a style in the run.
    /// </summary>
    [Fact]
    public void BoardViewLastUsed()
    {
        var def = DefaultStyle.Last;
        var last = BoardStyle.Today;
        var c = Controller(
            new InMemoryBoardSource(new Snapshot { Accounts = F.Accounts, Cases = SampleCases() }),
            defaultStyle: () => def, lastStyle: () => last);
        c.BoardWillShow();
        Assert.Equal(BoardStyle.Today, c.State.Style); // last used
        // Not picked yet: a new Board View applies at the next show.
        def = DefaultStyle.Columns;
        c.BoardWillShow();
        Assert.Equal(BoardStyle.Columns, c.State.Style);
        c.SetStyle(BoardStyle.List);
        def = DefaultStyle.Today;
        c.BoardWillShow();
        Assert.Equal(BoardStyle.List, c.State.Style); // picked
    }

    /// <summary>The saved account filter applies at the first show, once the accounts are known; an account gone since is every account.</summary>
    [Fact]
    public void SavedAccountFilter()
    {
        var source = new InMemoryBoardSource(new Snapshot { Cases = SampleCases(), Phase = Phase.Loading });
        var c = Controller(source, savedAccount: () => F.AccountB.Value);
        c.BoardWillShow();
        Assert.Null(c.State.Account); // the accounts are not known yet
        source.Replace(source.Snapshot with { Accounts = F.Accounts, Phase = Phase.Ready });
        Assert.Equal(F.AccountB, c.State.Account); // applied once known
        c.SetAccount(null);
        c.BoardWillShow();
        Assert.Null(c.State.Account); // not again

        var gone = Controller(
            new InMemoryBoardSource(new Snapshot { Accounts = F.Accounts, Cases = SampleCases() }), savedAccount: () => "gone");
        gone.BoardWillShow();
        Assert.Null(gone.State.Account);
    }

    /// <summary>Archive offers Undo through ArchiveDone; Undo puts the case back; without a listener the text is a toast.</summary>
    [Fact]
    public void ArchiveOffersUndo()
    {
        var (c, source, _) = Make();
        var got = new List<ArchiveOutcome>();
        c.ArchiveDone += (_, o) => got.Add(o);
        c.Archive(F.Id("c1"));
        var outcome = Assert.Single(got);
        Assert.True(outcome.Case == F.Id("c1") && outcome.UndoLabel == "Undo" && outcome.Text.Length > 0);
        Assert.True(source.Snapshot.FindCase(F.Id("c1"))?.Done);
        c.UndoArchive(outcome); // the samples move nothing: only back on the board
        Assert.False(source.Snapshot.FindCase(F.Id("c1"))?.Done);

        var (c2, _, _) = Make();
        var toasts = new List<string>();
        c2.ToastRequested += (_, t) => toasts.Add(t);
        c2.Archive(F.Id("c1"));
        Assert.Single(toasts);
    }

    /// <summary>
    /// A source that holds the user's writes until <see cref="Flush"/>, as a
    /// daemon-backed one reports them when the daemon answers.
    /// </summary>
    private sealed class LateSource(Snapshot snapshot) : IBoardSource
    {
        private readonly List<Func<Snapshot, Snapshot>> queued = [];

        public Snapshot Snapshot { get; set; } = snapshot;

        public Action? OnChange { get; set; }

        public Action<string>? OnError { get; set; }

        public Action<string>? OnNotice { get; set; }

        public Action<ArchiveOutcome>? OnArchived { get; set; }

        /// <summary>The cases whose conversation was asked for, in order.</summary>
        public List<BoardCaseId> Loads { get; } = [];

        /// <summary>How many times the data was asked for anew.</summary>
        public int Refreshes { get; private set; }

        public void SetState(State? state, BoardCaseId id) => Queue(id, c => c with { UserState = state });

        public void SetDone(bool done, BoardCaseId id) => Queue(id, c => c.WithDone(done));

        public void Remind(DateTimeOffset? until, BoardCaseId id) =>
            Queue(id, c => c with { Visibility = until is { } u ? Visibility.Snoozed(u) : Visibility.Live });

        public void Archive(BoardCaseId id) => SetDone(true, id);

        public void SetCommitmentDone(bool done, BoardCommitmentId id)
        {
        }

        public void DiscardDraft(BoardCaseId id)
        {
        }

        public void Unflag(BoardCaseId id)
        {
        }

        public void LoadMessages(BoardCaseId id) => Loads.Add(id);

        public void Refresh() => Refreshes++;

        /// <summary>Changes the case at <paramref name="index"/> in the snapshot, unreported.</summary>
        public void Change(int index, Func<Case, Case> change)
        {
            var cases = Snapshot.Cases.ToList();
            cases[index] = change(cases[index]);
            Snapshot = Snapshot with { Cases = cases };
        }

        public void Flush()
        {
            foreach (var q in queued)
            {
                Snapshot = q(Snapshot);
            }
            queued.Clear();
            OnChange?.Invoke();
        }

        private void Queue(BoardCaseId id, Func<Case, Case> change) =>
            queued.Add(s => s with { Cases = [.. s.Cases.Select(c => c.Id == id ? change(c) : c)] });
    }
}
