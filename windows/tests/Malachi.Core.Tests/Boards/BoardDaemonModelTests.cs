// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/BoardDaemonModelTests.swift; Go:
// ui/internal/board/daemon_model_test.go (the same cases, Go's extra error
// cases in FailureTexts, Unflag in AMissingPromiseIsNamed and
// TestStateFromAPI as StatesMapBothWays over DaemonBoardSource).
//
// The board's model with what the daemon adds (docs/api.md §4.13): the
// reason codes and their texts, stale annotations, the linked draft,
// visibility (done, snoozed), commitments' states, the conversation loaded
// on demand, the phases and the failure texts.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Tests.Text;
using Xunit;
using static Malachi.Core.Boards.Board;
using BText = Malachi.Core.Boards.Board.Text;
using F = Malachi.Core.Tests.Boards.BoardFixture;

namespace Malachi.Core.Tests.Boards;

public sealed class BoardDaemonModelTests
{
    private static readonly DateTimeOffset Back = F.Day(16, 9);

    // Reason texts

    /// <summary>Every code of the contract has a text of its own; nothing else does.</summary>
    [Fact]
    public void EveryKnownCodeHasItsOwnText()
    {
        var seen = new HashSet<string>();
        foreach (var code in KnownReasons)
        {
            var t = BText.Reason(code);
            Assert.True(t.Length > 0 && t != BText.ReasonUnknown, code.Value);
            Assert.True(seen.Add(t), $"{code.Value} repeats another code's text");
        }
        Assert.Equal(17, KnownReasons.Count);
    }

    [Fact]
    public void UnknownCodesGetTheGenericText()
    {
        foreach (var code in new[] { "", "hot.someday", "HOT.IMPORTANT", "you.addressed ", "rule c1" })
        {
            Assert.Equal(BText.ReasonUnknown, BText.Reason(new BoardReason(code)));
        }
    }

    // Stale annotations

    private static Case StaleCase() => F.Mk(
        "c1", State.You, subject: "Raw subject",
        annotation: new Annotation
        {
            State = State.Hot,
            Title = "Old title",
            Summary = "Old summary",
            Why = "Old why",
            Due = F.Day(16, 10),
            DueQuote = "by tomorrow",
            Tasks = ["t1"],
            Stale = true,
        },
        draft: "Hi");

    /// <summary>A stale annotation counts for nothing; its draft stays.</summary>
    [Fact]
    public void StaleAnnotationCountsForNothing()
    {
        var c = StaleCase();
        Assert.Equal(State.You, StateOf(c, true));
        Assert.Equal(StateSource.Rules, StateSourceOf(c, true));
        Assert.Null(AnnotationOf(c, true));
        var v = F.View([c], annotated: true);
        var d = Assert.IsType<Detail>(v.Detail);
        Assert.True(d.Title == "Raw subject" && d.Summary == "" && d.Tasks.Count == 0 && d.Due == "" && d.DueQuote == "");
        Assert.Equal(BText.ReasonUnknown, d.Why);
        Assert.Equal(BText.StaleNotes, d.StaleNote);
        Assert.True(d.Draft == "Hi" && d.DraftId is not null);
        Assert.Empty(v.Today.DueGroups);
        Assert.Equal("", v.Sections[0].Rows[0].Due);
        // Without the assistant no note: nothing of it was shown anyway.
        Assert.Equal("", F.View([c], annotated: false).Detail?.StaleNote);
    }

    /// <summary>An annotation that leaves the state to the rules kept it.</summary>
    [Fact]
    public void AnnotationWithoutAState()
    {
        var c = F.Mk("c1", State.Them, annotation: new Annotation { Title = "T" });
        Assert.Equal(State.Them, StateOf(c, true));
        Assert.Equal(StateSource.AssistantKept, StateSourceOf(c, true));
        Assert.Equal("T", F.View([c], annotated: true).Detail?.Title);
    }

    // Visibility

    private static Case[] VisibilityCases() =>
    [
        F.Mk("l1", State.You, hours: 1), F.Mk("d1", State.Info, hours: 2, done: true),
        F.Mk("s1", State.You, hours: 3, visibility: Visibility.Snoozed(F.Day(20, 9))),
        F.Mk("s2", State.Hot, hours: 4, visibility: Visibility.Snoozed(Back)),
    ];

    /// <summary>
    /// Snoozed cases are off the board and listed under Snoozed, soonest back
    /// first; Done lists and counts only the done ones.
    /// </summary>
    [Fact]
    public void SnoozedHaveTheirOwnFilter()
    {
        var all = F.View(VisibilityCases());
        Assert.Equal(["l1"], all.Sections.SelectMany(s => F.Ids(s.Rows)));
        Assert.Equal(["l1"], all.Columns.SelectMany(s => F.Ids(s.Rows)));
        Assert.Equal(1, all.Nav[^1].Count);
        Assert.Equal(Filter.Done, all.Nav[^1].Filter);
        Assert.Equal(Filter.Snoozed, all.Nav[^2].Filter);
        Assert.Equal(2, all.Nav[^2].Count);
        Assert.Equal(1, all.Nav.First(n => n.Filter == Filter.All).Count);
        Assert.Equal(1, all.Accounts[0].Count); // live only
        var done = F.View(VisibilityCases(), configure: v => v with { Filter = Filter.Done });
        Assert.Equal([SectionKind.Done], done.Sections.Select(s => s.Kind));
        Assert.Equal(["d1"], F.Ids(done.Sections[0].Rows));
        Assert.Equal("", done.Sections[0].Rows[0].Remind);
        var snoozed = F.View(VisibilityCases(), configure: v => v with { Filter = Filter.Snoozed });
        Assert.Equal([SectionKind.Snoozed], snoozed.Sections.Select(s => s.Kind));
        Assert.Equal("Snoozed", snoozed.Sections[0].Title);
        Assert.Equal(["s2", "s1"], F.Ids(snoozed.Sections[0].Rows));
        Assert.Equal("Tomorrow at 09:00", snoozed.Sections[0].Rows[0].Remind);
        Assert.Equal(F.Id("s2"), snoozed.Selection);
        var d = Assert.IsType<Detail>(snoozed.Detail);
        Assert.True(d.IsSnoozed && !d.IsDone && d.RemindText == "Back on the board: Tomorrow at 09:00");
        // Only snoozed: not empty.
        Assert.False(F.View([F.Mk("s1", visibility: Visibility.Snoozed(Back))]).IsEmpty);
    }

    [Fact]
    public void SelectionAfterDoneWalksTheSnoozed()
    {
        var v = new ViewState { Filter = Filter.Snoozed };
        var s = new Snapshot { Accounts = F.Accounts, Cases = VisibilityCases() };
        Assert.Equal(F.Id("s1"), SelectionAfterDone(F.Id("s2"), s, v));
        Assert.Equal(F.Id("s2"), SelectionAfterDone(F.Id("s1"), s, v));
        Assert.Null(SelectionAfterDone(F.Id("d1"), s, v)); // not listed
    }

    [Fact]
    public void DoneIsTheVisibility()
    {
        var c = F.Mk("c1");
        Assert.True(!c.Done && c.Visibility == Visibility.Live);
        c = c.WithDone(true);
        Assert.Equal(Visibility.Done(), c.Visibility);
        c = c with { Visibility = Visibility.Done(F.Ago(1)) };
        c = c.WithDone(true); // stays as it is, date and all
        Assert.Equal(Visibility.Done(F.Ago(1)), c.Visibility);
        c = c with { Visibility = Visibility.Snoozed(Back) };
        Assert.False(c.Done);
        c = c.WithDone(false); // not done already: the remind stays
        Assert.Equal(Back, c.Visibility.RemindAt);
        c = c.WithDone(true);
        Assert.Equal(Visibility.Done(), c.Visibility);
    }

    // The detail

    [Fact]
    public void DetailCarriesTheTargets()
    {
        var c = F.Mk("c1") with
        {
            Thread = new ThreadId("t_9"),
            Reply = new ReplyTarget(new MessageId("m_5"), new FolderId("f_inbox")),
            LatestMessage = new MessageId("m_7"),
            CanArchive = true,
        };
        var d = Assert.IsType<Detail>(F.View([c]).Detail);
        Assert.True(d.AccountId == F.AccountA && d.Thread == new ThreadId("t_9") && d.LatestMessage == new MessageId("m_7"));
        Assert.Equal(new ReplyTarget(new MessageId("m_5"), new FolderId("f_inbox")), d.Reply);
        Assert.True(d.CanArchive);
        Assert.True(d.Draft == "" && d.DraftId is null);
    }

    /// <summary>
    /// The conversation: loading until it arrives, a note when it failed,
    /// the cards once there.
    /// </summary>
    [Fact]
    public void MessagesOnDemand()
    {
        var loading = Assert.IsType<Detail>(F.View([F.Mk("c1", messagesNull: true)]).Detail);
        Assert.True(loading.MessagesLoading && loading.MessagesNote == BText.MessagesLoading && loading.Messages.Count == 0);
        var failed = F.Mk("c1", messagesNull: true) with { MessagesFailed = true };
        var f = Assert.IsType<Detail>(F.View([failed]).Detail);
        Assert.True(!f.MessagesLoading && f.MessagesNote == BText.MessagesFailed && f.MessagesRetry);
        var m = new CaseMessage { Id = new MessageId("m_1"), From = "Ann", Date = F.Ago(2), Text = "hello" };
        var loaded = Assert.IsType<Detail>(F.View([F.Mk("c1", messages: [m])]).Detail);
        Assert.True(!loaded.MessagesLoading && loaded.MessagesNote == "");
        Assert.Equal([new MessageId("m_1")], loaded.Messages.Select(x => x.Id));
        var none = Assert.IsType<Detail>(F.View([F.Mk("c1", messages: [])]).Detail);
        Assert.True(!none.MessagesLoading && none.MessagesNote == "" && none.Messages.Count == 0);
    }

    /// <summary>Only open promises are shown.</summary>
    [Fact]
    public void OnlyOpenCommitments()
    {
        Commitment[] ks =
        [
            new() { Id = new BoardCommitmentId("k1"), CaseId = F.Id("c1"), Text = "Open" },
            new() { Id = new BoardCommitmentId("k2"), CaseId = F.Id("c1"), Text = "Ticked", State = CommitmentState.Done },
            new() { Id = new BoardCommitmentId("k3"), CaseId = F.Id("c1"), Text = "Closed", State = CommitmentState.Closed },
        ];
        var v = F.View([F.Mk("c1")], annotated: true, commitments: ks);
        Assert.Equal([new BoardCommitmentId("k1")], v.Commitments.Select(k => k.Id));
        var tile = v.Today.Tiles[^1];
        Assert.True(tile.Kind == TileKind.Commitments && tile.Count == 1 && tile.Title == BText.Commitments);
    }

    // Phases

    private static ViewModel PhaseView(Phase phase, bool truncated = false, params Case[] cases) =>
        Board.View(
            new Snapshot { Accounts = F.Accounts, Cases = cases, Phase = phase, Truncated = truncated }, new ViewState(),
            F.Now, F.Culture, F.Zone);

    [Fact]
    public void EmptyTextsFollowThePhase()
    {
        var ready = PhaseView(Phase.Ready);
        Assert.True(ready.Phase == Phase.Ready && ready.IsEmpty);
        Assert.True(ready.EmptyTitle == BText.EmptyTitle && ready.EmptyBody == BText.EmptyBody);
        Assert.Equal("", ready.Notice);
        var titles = new HashSet<string>();
        foreach (var p in new[] { Phase.Loading, Phase.Preparing, Phase.Ready, Phase.Unavailable, Phase.Off })
        {
            var v = PhaseView(p);
            Assert.True(v.Phase == p && v.EmptyTitle.Length > 0);
            titles.Add(v.EmptyTitle);
        }
        Assert.Equal(5, titles.Count);
        Assert.Equal("", PhaseView(Phase.Loading).EmptyBody);
    }

    /// <summary>The notice says when the cases shown are partial or old.</summary>
    [Fact]
    public void PhaseNotice()
    {
        var c = F.Mk("c1");
        Assert.Contains("first time", PhaseView(Phase.Preparing, false, c).Notice, StringComparison.Ordinal);
        Assert.Contains("not running", PhaseView(Phase.Unavailable, false, c).Notice, StringComparison.Ordinal);
        Assert.Equal("", PhaseView(Phase.Ready, false, c).Notice);
        Assert.Contains("1,000", PhaseView(Phase.Ready, true, c).Notice, StringComparison.Ordinal);
        Assert.Equal("", PhaseView(Phase.Off).Notice);
    }

    [Fact]
    public void TriageIsCarried()
    {
        var run = new Run { Model = "Claude", Date = F.Ago(1), Annotated = 4, Running = true };
        var s = new Snapshot { Run = run, Triage = new Triage { Queue = 7, AnnotatedToday = 12 } };
        var v = Board.View(s, new ViewState(), F.Now, F.Culture, F.Zone);
        Assert.True(v.Triage == new Triage { Queue = 7, AnnotatedToday = 12 } && v.Run == run);
    }

    // Failure texts

    /// <summary>What failed, and why when the error says; never the daemon's message.</summary>
    [Fact]
    public void FailureTexts()
    {
        const string secret = "SELECT * FROM cases -- internal detail";
        (Exception Error, string Want)[] cases =
        [
            (RpcErrorTextFailureException.NotConnected(), "Moving the case failed: the mail backend is not running."),
            (RpcErrorTextFailureException.Disconnected(), "Moving the case failed: the mail backend is not running."),
            (RpcErrorTextFailureException.Timeout("board.setState"), "Moving the case failed: the mail backend did not answer in time."),
            (new OperationCanceledException(), "Moving the case failed: the mail backend did not answer in time."),
            (Daemon(ErrorCode.CaseNotFound, secret), "Moving the case failed: the case is no longer on the board."),
            (Daemon(ErrorCode.InvalidArgument, secret), "Moving the case failed: the board did not accept it."),
            (Daemon(ErrorCode.MethodNotFound, secret), "Moving the case failed: this mail backend has no board."),
            (Daemon(ErrorCode.NotImplemented, secret), "Moving the case failed: this mail backend has no board."),
            (Daemon(ErrorCode.StorageError, secret), "Moving the case failed: the mail backend could not save it."),
            (Daemon(ErrorCode.DraftNotFound, secret), "Moving the case failed: the draft no longer exists."),
            (Daemon(ErrorCode.InternalError, secret), "Moving the case failed."),
            (new InvalidOperationException(secret), "Moving the case failed."),
        ];
        foreach (var (error, want) in cases)
        {
            var got = BText.Failed(BText.Action.Move, error);
            Assert.Equal(want, got);
            Assert.DoesNotContain("SELECT", got, StringComparison.Ordinal);
        }
        var seen = new HashSet<string>();
        foreach (var a in Enum.GetValues<BText.Action>())
        {
            Assert.True(seen.Add(BText.Failed(a, Daemon(ErrorCode.InternalError, ""))), a.ToString());
        }
    }

    [Fact]
    public void ArchivedTexts()
    {
        Assert.Equal("Archived 1 message.", BText.Archived(1, noArchive: false));
        Assert.Equal("Archived 3 messages.", BText.Archived(3, noArchive: false));
        Assert.Equal("Marked as done. No message was in the inbox.", BText.Archived(0, noArchive: false));
        Assert.Equal("Marked as done. This account has no archive.", BText.Archived(0, noArchive: true));
    }

    /// <summary>
    /// A board that could not be listed, and a backend without the board,
    /// say so in texts of their own, apart from a backend not running.
    /// </summary>
    [Fact]
    public void FailurePhasesHaveTheirOwnTexts()
    {
        Phase[] phases = [Phase.Unavailable, Phase.Failed, Phase.Unsupported];
        Assert.Equal(3, phases.Select(p => PhaseView(p).EmptyBody).Distinct().Count());
        Assert.Equal(3, phases.Select(p => PhaseView(p, false, F.Mk("c1")).Notice).Distinct().Count());
        Assert.Contains("could not be loaded", PhaseView(Phase.Failed, false, F.Mk("c1")).Notice, StringComparison.Ordinal);
        Assert.Contains("no board", PhaseView(Phase.Unsupported).EmptyBody, StringComparison.Ordinal);
        Phase[] every = [Phase.Loading, Phase.Preparing, Phase.Ready, Phase.Unavailable, Phase.Failed, Phase.Unsupported, Phase.Off];
        Assert.Equal(phases, every.Where(p => p.IsFailure));
    }

    /// <summary>board.setCommitment's caseNotFound is a promise that is gone.</summary>
    [Fact]
    public void AMissingPromiseIsNamed()
    {
        var e = Daemon(ErrorCode.CaseNotFound, "x");
        Assert.Equal("Changing the promise failed: the promise no longer exists.", BText.Failed(BText.Action.Commitment, e));
        Assert.Equal("Marking the case done failed: the case is no longer on the board.", BText.Failed(BText.Action.Done, e));
        Assert.Equal("Removing the star failed: the case is no longer on the board.", BText.Failed(BText.Action.Unflag, e));
    }

    // The assistant's mark: text the assistant wrote is marked as the
    // assistant's wherever it shows (docs/api.md §4.13), flags in the view
    // model, "Assistant:" for the screen reader.

    private static Case Annotated(string title, string summary = "", string why = "") => F.Mk(
        "c1", State.You, subject: "Raw subject", snippet: "Raw snippet",
        annotation: new Annotation { Title = title, Summary = summary, Why = why, Due = F.Day(16, 10), DueQuote = "by tomorrow" });

    [Fact]
    public void TheAssistantsTitleIsMarked()
    {
        var c = Annotated("Approve the budget", summary: "Anna asks", why: "She waits");
        var k = new Commitment { Id = new BoardCommitmentId("k1"), CaseId = c.Id, Text = "Send it" };
        var v = F.View([c], annotated: true, commitments: [k]);
        var row = v.Sections[0].Rows[0];
        Assert.True(row.Title == "Approve the budget" && row.TitleIsAssistant);
        Assert.True(row.Snippet == "Anna asks" && row.SnippetIsAssistant && row.MarksAssistant);
        Assert.Contains("Assistant: Approve the budget", row.Spoken, StringComparison.Ordinal);
        var d = Assert.IsType<Detail>(v.Detail);
        Assert.True(d.TitleIsAssistant && d.SpokenTitle == "Assistant: Approve the budget");
        Assert.True(d.Why == "She waits" && d.WhyIsAssistant);
        var due = v.Today.DueGroups[0].Items[0];
        Assert.True(due.TitleIsAssistant && due.SpokenTitle == "Assistant: Approve the budget");
        var commitment = v.Commitments[0];
        Assert.True(commitment.From == "Approve the budget" && commitment.FromIsAssistant);
        Assert.Equal("Assistant: Approve the budget", commitment.SpokenFrom);
    }

    /// <summary>The subject, the message's snippet and the rules' reason are not the assistant's.</summary>
    [Fact]
    public void TheDaemonsTextIsNot()
    {
        foreach (var (c, on) in new[] { (Annotated(""), true), (Annotated("Title", summary: "S", why: "W"), false) })
        {
            var v = F.View([c], annotated: on);
            var row = v.Sections[0].Rows[0];
            Assert.True(row.Title == "Raw subject" && !row.TitleIsAssistant);
            Assert.True(row.Spoken.Contains("Raw subject", StringComparison.Ordinal) && !row.Spoken.Contains("Assistant:", StringComparison.Ordinal));
            var d = Assert.IsType<Detail>(v.Detail);
            Assert.True(!d.TitleIsAssistant && d.SpokenTitle == "Raw subject" && !d.WhyIsAssistant);
            Assert.True(!row.SnippetIsAssistant && !row.MarksAssistant);
        }
        // A summary without a title marks the row, not the title.
        var marked = F.View([Annotated("", summary: "Anna asks")], annotated: true).Sections[0].Rows[0];
        Assert.True(!marked.TitleIsAssistant && marked.SnippetIsAssistant && marked.MarksAssistant);
    }

    /// <summary>Go's TestStateFromAPI: the four states map both ways; an unknown one is none (the case reads it as for reading).</summary>
    [Fact]
    public void StatesMapBothWays()
    {
        foreach (var s in States)
        {
            Assert.Equal(s, DaemonBoardSource.StateOf(DaemonBoardSource.WireState(s)));
        }
        Assert.Null(DaemonBoardSource.StateOf(new BoardState("later")));
        Assert.Null(DaemonBoardSource.StateOf(null));
    }

    private static RpcErrorTextFailureException Daemon(int code, string message) =>
        RpcErrorTextFailureException.Daemon(new ErrorCode(code), message);
}
