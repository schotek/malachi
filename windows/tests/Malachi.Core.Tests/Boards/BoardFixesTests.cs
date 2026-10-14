// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/board/fixes_test.go (the model's part; the
// controller's is in Controllers/BoardControllerTests.cs, the provider's
// swapped texts have no counterpart here: the Windows client names its
// provider where it builds the text): the board's fixes of 2026-10-08 —
// reminded cases, new contacts, the user's decision, the byline, the Today
// phrase, Undo of Archive, the keys, Escape and the follow-up.

using System;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Xunit;
using static Malachi.Core.Boards.Board;
using BText = Malachi.Core.Boards.Board.Text;
using F = Malachi.Core.Tests.Boards.BoardFixture;

namespace Malachi.Core.Tests.Boards;

public sealed class BoardFixesTests
{
    private static Case Reminded(Case c) => c with { RemindedAt = F.Ago(2) };

    private static Case Reason(Case c, string reason) => c with { RuleReason = new BoardReason(reason) };

    [Fact]
    public void RemindedComesFirstInItsState()
    {
        Case[] cases =
        [
            F.Mk("y1", State.You, hours: 1), Reminded(F.Mk("y2", State.You, hours: 30)),
            F.Mk("h1", State.Hot, hours: 1), F.Mk("y3", State.You, hours: 5),
        ];
        var v = F.View(cases);
        Assert.Equal(["h1", "y2", "y1", "y3"], v.Sections.SelectMany(x => F.Ids(x.Rows)));
        Assert.Equal(["y2", "y1", "y3"], F.Ids(v.Columns[1].Rows));
        Assert.Equal(["y2", "y1", "y3"], F.Ids(v.Today.You));
        var r = v.Columns[1].Rows[0];
        Assert.True(r.Reminded);
        Assert.Equal(["Reminded"], r.Badges);
        Assert.StartsWith("Waiting for You. Reminded.", r.Spoken, StringComparison.Ordinal);
        Assert.False(v.Columns[1].Rows[1].Reminded);
        Assert.Empty(v.Columns[1].Rows[1].Badges);

        var d = Assert.IsType<Detail>(F.View(cases, configure: x => x with { Selection = F.Id("y2") }).Detail);
        Assert.True(d.Reminded);
        Assert.Equal(["A reminder you set has come due."], d.WhyNotes);
        // A snoozed or done case is not reminded, whatever the field says.
        Assert.False(Reminded(F.Mk("s", visibility: Visibility.Snoozed(F.Now.AddHours(1)))).Reminded);
        Assert.False(Reminded(F.Mk("d", done: true)).Reminded);
    }

    [Fact]
    public void NewContactBadgeAndReason()
    {
        var v = F.View([Reason(F.Mk("n1"), BoardReason.YouNewContact)]);
        var r = v.Sections[0].Rows[0];
        Assert.True(r.NewContact);
        Assert.Equal(["New contact"], r.Badges);
        Assert.Equal("The newest message is addressed to you by someone you have never written to.", v.Detail?.Why);
        var both = F.View([Reminded(Reason(F.Mk("n2"), BoardReason.YouNewContact))]);
        Assert.Equal(["Reminded", "New contact"], both.Detail?.Badges);
        Assert.NotEqual(BText.Reason(BoardReason.InfoUnknownSender), BText.Reason(BoardReason.YouNewContact));
    }

    [Fact]
    public void TheUsersDecisionKeepsIt()
    {
        Assert.Equal(["Your decision keeps it on the board."], F.View([F.Mk("u1", State.Info, user: State.Hot)]).Detail?.WhyNotes);
        Assert.Empty(F.View([F.Mk("r1", State.Hot)]).Detail!.WhyNotes);
    }

    [Fact]
    public void DetailByline()
    {
        var d = F.View([F.Mk("c1")]).Detail!;
        Assert.Equal(BText.PersonAndTime(d.Person, d.Time), d.Byline);
    }

    /// <summary>
    /// The Today phrase counts what is new since yesterday's midnight, due
    /// today, or back from a reminder; hot and waiting for you only.
    /// </summary>
    [Fact]
    public void TodayPhraseCountsOnlyWhatNeedsYouToday()
    {
        Case Due(Case c, DateTimeOffset at) => c with { Annotation = new Annotation { Title = "", Due = at } };
        Case[] cases =
        [
            F.Mk("new", State.You, hours: 1), // today
            F.Mk("yesterday", State.Hot, hours: 35), // the 14th, 01:00
            F.Mk("old", State.You, hours: 40), // the 13th, 20:00
            Due(F.Mk("old due today", State.You, hours: 100), F.Day(15, 18)),
            Due(F.Mk("old due tomorrow", State.You, hours: 100), F.Day(16, 9)),
            Reminded(F.Mk("old reminded", State.Hot, hours: 100)),
            F.Mk("them new", State.Them, hours: 1),
            F.Mk("info new", State.Info, hours: 1),
        ];
        Assert.Equal("4 things need you today.", F.View(cases, annotated: true).Today.Phrase);
        // Without the assistant its deadlines do not count.
        Assert.Equal("3 things need you today.", F.View(cases).Today.Phrase);
        Assert.Equal("Nothing needs you today.", F.View([F.Mk("old", State.You, hours: 100)]).Today.Phrase);
    }

    [Fact]
    public void UndoArchiveCalls()
    {
        BoardMoved M(string m, string f) => new() { MessageId = new MessageId(m), FromFolderId = new FolderId(f) };
        var got = UndoArchive(
            [M("m1", "inbox"), M("m2", "work"), M("m3", "inbox"), M("m1", "inbox"), M("", "inbox"), M("m4", "")],
            new AccountId("acc"), new BoardCaseId("c_1"));
        Assert.Equal(2, got.Moves.Count);
        Assert.Equal(new AccountId("acc"), got.Moves[0].AccountId);
        Assert.Equal(new FolderId("inbox"), got.Moves[0].TargetFolderId);
        Assert.Equal([new MessageId("m1"), new MessageId("m3")], got.Moves[0].MessageIds);
        Assert.Equal(new FolderId("work"), got.Moves[1].TargetFolderId);
        Assert.Equal([new MessageId("m2")], got.Moves[1].MessageIds);
        Assert.Equal(new BoardSetDoneParams { CaseId = new BoardCaseId("c_1"), Done = false }, got.Reopen);
        Assert.Empty(UndoArchive(null, new AccountId("acc"), new BoardCaseId("c_1")).Moves);
    }

    [Fact]
    public void Keys()
    {
        var keys = BoardKeys();
        Assert.Equal("12edr", new string([.. keys.Select(k => k.Rune)]));
        Assert.All(keys, k => Assert.NotEmpty(k.Title));
        Assert.Equal("Board", KeysGroup);
    }

    [Theory]
    [InlineData('1', true, false, false, Mode.Board, KeyAction.ShowMail)]
    [InlineData('2', true, false, true, Mode.Mail, KeyAction.ShowBoard)]
    [InlineData('2', false, false, false, Mode.Board, null)]
    [InlineData('e', false, false, false, Mode.Board, KeyAction.Archive)]
    [InlineData('E', false, false, false, Mode.Board, KeyAction.Archive)]
    [InlineData('d', false, false, false, Mode.Board, KeyAction.Done)]
    [InlineData('r', false, false, false, Mode.Board, KeyAction.Remind)]
    [InlineData('e', false, false, true, Mode.Board, null)] // typing
    [InlineData('e', false, false, false, Mode.Mail, null)] // the mail's own key
    [InlineData('e', true, false, false, Mode.Board, null)] // Ctrl+E is not Archive
    [InlineData('e', false, true, false, Mode.Board, null)] // Shift/Alt+E neither
    [InlineData('x', false, false, false, Mode.Board, null)]
    public void KeyForRule(char key, bool primary, bool other, bool inText, Mode mode, KeyAction? want) =>
        Assert.Equal(want, KeyFor(key, primary, other, inText, mode));

    [Theory]
    [InlineData(true, true, true, EscapeTarget.ClosePopup)]
    [InlineData(false, true, false, EscapeTarget.ClosePopup)]
    [InlineData(true, false, true, EscapeTarget.FocusStatePill)]
    [InlineData(true, false, false, EscapeTarget.FocusStatePill)]
    [InlineData(false, false, true, EscapeTarget.ClosePanel)]
    [InlineData(false, false, false, EscapeTarget.Nothing)]
    public void EscapeInTwoSteps(bool editor, bool popup, bool panel, EscapeTarget want) =>
        Assert.Equal(want, EscapeFor(editor, popup, panel));

    [Fact]
    public void FollowUp()
    {
        Assert.True(IsFollowUp(Reason(F.Mk("t", State.Them), BoardReason.ThemReplied), annotated: false));
        Assert.True(IsFollowUp(Reason(F.Mk("t", State.Them), BoardReason.ThemAsked), annotated: false));
        Assert.False(IsFollowUp(Reason(F.Mk("y", State.You), BoardReason.YouAddressed), annotated: false));
        // The state shown decides: a them.replied case the user moved to You
        // is no follow-up; a case the user shows as Them is one.
        var moved = Reason(F.Mk("m", State.Them), BoardReason.ThemReplied) with { UserState = State.You };
        Assert.False(IsFollowUp(moved, annotated: true));
        var kept = Reason(F.Mk("k", State.You), BoardReason.YouAddressed) with { UserState = State.Them };
        Assert.True(IsFollowUp(kept, annotated: true));
        // An annotation counts only when annotated and not stale.
        var ann = Reason(F.Mk("a", State.You), BoardReason.YouAddressed) with { Annotation = new Annotation { Title = "", State = State.Them } };
        Assert.True(IsFollowUp(ann, annotated: true));
        Assert.False(IsFollowUp(ann, annotated: false));
        Assert.False(IsFollowUp(ann with { Annotation = ann.Annotation! with { Stale = true } }, annotated: true));
        var inputs = new SuggestReplyInputs
        {
            Offered = true,
            Available = true,
            ClaudeFound = true,
            SignedIn = true,
            State = new SuggestReplyState.Idle(),
            CaseId = new BoardCaseId("c"),
        };
        Assert.Equal("✦ Suggest Reply", SuggestReplyViewOf(inputs).Title);
        Assert.Equal("✦ Suggest Follow-up", SuggestReplyViewOf(inputs with { FollowUp = true }).Title);
    }
}
