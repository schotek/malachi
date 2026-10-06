// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/DaemonBoardSourceTests.swift: the
// board from the daemon against a fake daemon (loading, notifications with
// their debounce, one board.list at a time, dropped stale replies,
// optimistic writes and their revert, the phases and the retries, the
// conversations cached by version); Go: ui/internal/board
// daemon_source_test.go, whose Go-only TestDaemonConvert and
// TestDaemonAccountInfo end the file. The two controller cases
// (controllerDepartureAndRevert, controllerRetriesAFailedConversation-
// AfterAReconnect) come with the port of BoardController.
//
// Swift's ManualClock is a FakeTimeProvider: a wait is seen to be armed by
// advancing the clock to just before it ends (nothing happens) and then to
// its end. The fake daemon holds replies until the test releases them, and
// "nothing happened" is checked once everything is idle (Quiescence); while
// a reply is held on purpose, the test waits for a condition on the UI
// thread instead.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Microsoft.Extensions.Time.Testing;
using Xunit;
using static Malachi.Core.Boards.Board;

namespace Malachi.Core.Tests.Boards;

public sealed class DaemonBoardSourceTests
{
    private static readonly DateTimeOffset T0 = DateTimeOffset.FromUnixTimeSeconds(1_790_000_000);
    private static readonly TimeSpan Ms = TimeSpan.FromMilliseconds(1);

    private static BoardCase WireCase(string n, string state = BoardState.You, long version = 1) => new()
    {
        Id = $"c_{n}",
        AccountId = "acc_1",
        ThreadId = $"t_{n}",
        RuleState = state,
        RuleReason = BoardReason.YouAddressed,
        Visibility = BoardVisibility.Live,
        Subject = $"Subject {n}",
        Person = new Address { Name = $"Person {n}", Email = $"{n}@example.invalid" },
        Date = T0,
        Snippet = "",
        Unread = false,
        HasAttachments = false,
        MessageCount = 2,
        ReplyMessageId = $"m_{n}",
        ReplyFolderId = "f_inbox",
        LatestMessageId = $"m_{n}",
        CanArchive = true,
        Version = version,
    };

    private static BoardCaseId Id(string n) => new($"c_{n}");

    private static BoardCommitment Commitment(string state = BoardCommitmentState.Open) => new()
    {
        Id = "k_1",
        CaseId = "c_1",
        AccountId = "acc_1",
        MessageId = "m_1",
        Text = "Send it",
        Quote = "I will send it",
        State = state,
        At = T0,
    };

    private static RpcException Daemon(int code, string message) => new(new RpcError { Code = code, Message = message });

    [Fact]
    public async Task LoadsTheBoardAndTheAccounts()
    {
        await using var h = await Harness.StartAsync();
        await h.StartedAsync();
        var s = await h.SnapshotAsync();
        Assert.Equal(Phase.Ready, s.Phase);
        Assert.Equal([new AccountInfo("acc_1", "Work", "IMAP")], s.Accounts); // enabled only
        Assert.Equal([Id("1"), Id("2")], s.Cases.Select(c => c.Id));
        var c = Assert.IsType<Case>(await h.CAsync("1"));
        Assert.True(c.Person == "Person 1" && c.Subject == "Subject 1" && c.RuleState == State.You);
        Assert.True(c.RuleReason == BoardReason.YouAddressed && c.Thread == "t_1" && c.CanArchive && c.Version == 1);
        Assert.True(c.Reply == new ReplyTarget("m_1", "f_inbox") && c.LatestMessage == "m_1");
        Assert.True(c.Messages is null && c.Visibility == Visibility.Live);
        Assert.True(s.Triage == new Triage { Queue = 4, AnnotatedToday = 2 } && !s.Annotated);
    }

    /// <summary>Every published snapshot also goes to OnSnapshot (the application's triage); the last run carries its trigger and start.</summary>
    [Fact]
    public async Task SnapshotsReachTheTriage()
    {
        await using var h = await Harness.StartAsync();
        var started = T0.AddMinutes(-10);
        h.Script.LastRun = new BoardRun
        {
            At = started,
            EndedAt = T0,
            Trigger = BoardTrigger.Auto,
            Source = "claude-code",
            Annotated = 3,
            Error = BoardRunError.Timeout,
        };
        var seen = new List<Snapshot>();
        await h.Ui.RunAsync(() => h.Source.OnSnapshot = seen.Add);
        await h.StartedAsync();
        var snapshot = await h.SnapshotAsync();
        Assert.True(seen.Count > 0 && seen[^1].Equals(snapshot));
        Assert.Equal(seen.Count, h.Reports);
        var run = Assert.IsType<Run>(snapshot.Run);
        Assert.True(run.Trigger == "auto" && run.Started == started && run.Date == T0);
        Assert.True(run.Annotated == 3 && run.Error == "timeout" && !run.Running && run.Model == "claude-code");
    }

    [Fact]
    public async Task Phases()
    {
        await using var h = await Harness.StartAsync();
        h.Script.SetEnabled(true, ready: false);
        await h.StartedAsync();
        Assert.Equal(Phase.Preparing, (await h.SnapshotAsync()).Phase);
        h.Script.SetEnabled(false, ready: true);
        await h.RefreshAsync();
        var s = await h.SnapshotAsync();
        Assert.True(s.Phase == Phase.Off && s.Cases.Count == 0);
        // The board comes back: ready.
        h.Script.SetEnabled(true, ready: true);
        await h.RefreshAsync();
        s = await h.SnapshotAsync();
        Assert.True(s.Phase == Phase.Ready && s.Cases.Count == 2);
    }

    /// <summary>
    /// A list the daemon could not answer is phase failed, keeps the cases
    /// and is asked again after a back-off that grows; an answer ends it.
    /// </summary>
    [Fact]
    public async Task AFailedListIsAskedAgainLater()
    {
        await using var h = await Harness.StartAsync();
        await h.StartedAsync();
        h.Script.ListFailure = Daemon(ErrorCode.StorageError, "disk");
        await h.RefreshAsync();
        var s = await h.SnapshotAsync();
        Assert.True(s.Phase == Phase.Failed && s.Cases.Count == 2 && h.Errors.Count == 0);
        Assert.Equal(2, h.Script.Count(API.BoardList.Name));
        // The wait is 2 s.
        await h.AdvanceAsync(TimeSpan.FromSeconds(2) - Ms);
        Assert.Equal(2, h.Script.Count(API.BoardList.Name));
        await h.AdvanceAsync(Ms);
        Assert.Equal(3, h.Script.Count(API.BoardList.Name));
        // Still failing: the next wait is longer.
        await h.AdvanceAsync(TimeSpan.FromSeconds(4) - Ms);
        Assert.Equal(3, h.Script.Count(API.BoardList.Name));
        await h.AdvanceAsync(Ms);
        Assert.Equal(4, h.Script.Count(API.BoardList.Name));
        // Asked for now (the board shown again): the wait (8 s) is over at once.
        h.Script.ListFailure = null;
        await h.RefreshAsync();
        Assert.Equal(Phase.Ready, (await h.SnapshotAsync()).Phase);
        Assert.Equal(5, h.Script.Count(API.BoardList.Name));
        await h.AdvanceAsync(TimeSpan.FromSeconds(8));
        Assert.Equal(5, h.Script.Count(API.BoardList.Name));
        // The next failure starts the back-off over.
        h.Script.ListFailure = Daemon(ErrorCode.InternalError, "x");
        await h.RefreshAsync();
        Assert.Equal(Phase.Failed, (await h.SnapshotAsync()).Phase);
        await h.AdvanceAsync(TimeSpan.FromSeconds(2));
        Assert.Equal(7, h.Script.Count(API.BoardList.Name));
    }

    private static readonly int[] RetrySeconds = [2, 4, 8, 16, 32, 60, 60, 60];

    [Fact]
    public void RetryDelays() =>
        Assert.Equal(
            RetrySeconds.Select(s => TimeSpan.FromSeconds(s)),
            Enumerable.Range(1, 8).Select(DaemonBoardSource.RetryDelay));

    /// <summary>A backend without the board says so and is not asked again by itself; one that is not running is unavailable.</summary>
    [Fact]
    public async Task ABackendWithoutTheBoard()
    {
        await using var h = await Harness.StartAsync();
        await h.StartedAsync();
        h.Script.ListFailure = Daemon(ErrorCode.MethodNotFound, "board.list");
        await h.RefreshAsync();
        Assert.Equal(Phase.Unsupported, (await h.SnapshotAsync()).Phase);
        await h.AdvanceAsync(TimeSpan.FromMinutes(2));
        Assert.Equal(2, h.Script.Count(API.BoardList.Name));
        h.Client.Close();
        await h.RefreshAsync();
        Assert.Equal(Phase.Unavailable, (await h.SnapshotAsync()).Phase);
        await h.AdvanceAsync(TimeSpan.FromMinutes(2));
        Assert.Equal(2, h.Script.Count(API.BoardList.Name));
    }

    /// <summary>Without a daemon the board is unavailable; the connection coming loads it.</summary>
    [Fact]
    public async Task UnavailableDaemonAndReconnect()
    {
        await using var h = await Harness.StartAsync(connect: false);
        await h.StartedAsync();
        var s = await h.SnapshotAsync();
        Assert.True(s.Phase == Phase.Unavailable && s.Cases.Count == 0);
        await h.Client.ConnectAsync(TestContext.Current.CancellationToken);
        await h.Ui.RunAsync(() => h.Source.ConnectionChanged(connected: true));
        await h.IdleAsync();
        s = await h.SnapshotAsync();
        Assert.True(s.Phase == Phase.Ready && s.Accounts.Count == 1 && s.Cases.Count == 2);
        // The connection goes: the last snapshot stays.
        await h.Ui.RunAsync(() => h.Source.ConnectionChanged(connected: false));
        s = await h.SnapshotAsync();
        Assert.True(s.Phase == Phase.Unavailable && s.Cases.Count == 2);
    }

    /// <summary>Nothing loads before Start or after Stop: nothing is even on its way.</summary>
    [Fact]
    public async Task NothingLoadsOutsideStartAndStop()
    {
        await using var h = await Harness.StartAsync();
        await h.Ui.RunAsync(() =>
        {
            h.Source.BoardChanged();
            h.Source.AccountsChanged();
            h.Source.ConnectionChanged(connected: true);
            h.Source.Refresh();
        });
        Assert.True(await h.Ui.RunAsync(() => h.Source.IsIdle && h.Source.Phase == Phase.Loading));
        await h.AdvanceAsync(TimeSpan.FromSeconds(1));
        Assert.Equal(0, h.Script.Count(API.BoardList.Name));
        await h.StartedAsync();
        var lists = h.Script.Count(API.BoardList.Name);
        var accounts = h.Script.Count(API.AccountList.Name);
        Assert.True(lists == 1 && accounts == 1);
        await h.Ui.RunAsync(() =>
        {
            h.Source.Stop();
            h.Source.BoardChanged();
            h.Source.AccountsChanged();
            h.Source.ConnectionChanged(connected: true);
            h.Source.Refresh();
        });
        Assert.True(await h.Ui.RunAsync(() => h.Source.IsIdle));
        await h.AdvanceAsync(TimeSpan.FromSeconds(1));
        Assert.Equal(lists, h.Script.Count(API.BoardList.Name));
        Assert.Equal(accounts, h.Script.Count(API.AccountList.Name));
    }

    /// <summary>Notifications in a burst make one board.list after the debounce.</summary>
    [Fact]
    public async Task NotificationsAreDebounced()
    {
        await using var h = await Harness.StartAsync();
        await h.StartedAsync();
        h.Script.SetCases([WireCase("1"), WireCase("2", BoardState.Hot), WireCase("3", BoardState.Info)]);
        await h.Ui.RunAsync(() =>
        {
            for (var k = 0; k < 5; k++)
            {
                h.Source.BoardChanged(new BoardChangedNotification { AccountIds = ["acc_1"] });
            }
        });
        await h.IdleAsync();
        await h.AdvanceAsync(DaemonBoardSource.DefaultDebounce - Ms);
        Assert.Equal(1, h.Script.Count(API.BoardList.Name)); // not before the debounce
        await h.AdvanceAsync(Ms);
        Assert.Equal(3, (await h.SnapshotAsync()).Cases.Count);
        Assert.Equal(2, h.Script.Count(API.BoardList.Name));
    }

    /// <summary>
    /// A board.list asked for while one is on its way waits for it and runs
    /// once after it, however often it was asked for: a slow list under a
    /// stream of notifications still lands.
    /// </summary>
    [Fact]
    public async Task OneListAtATime()
    {
        await using var h = await Harness.StartAsync();
        await h.StartedAsync();
        h.Script.Lists.Hold(true);
        await h.Ui.RunAsync(h.Source.Refresh);
        await Eventually.Holds(() => h.Script.Lists.Waiting == 1);
        h.Script.SetCases([WireCase("1"), WireCase("2", BoardState.Hot), WireCase("3", BoardState.Info)]);
        for (var k = 0; k < 3; k++)
        {
            await h.Ui.RunAsync(() => h.Source.BoardChanged());
            await h.Ui.DrainAsync();
            h.Clock.Advance(DaemonBoardSource.DefaultDebounce);
            await h.Ui.DrainAsync();
        }
        await h.Ui.RunAsync(h.Source.Refresh);
        Assert.Equal(2, h.Script.Count(API.BoardList.Name)); // none started meanwhile
        // The held reply lands (the old cases), then one more list.
        h.Script.Lists.Hold(false);
        await h.IdleAsync();
        Assert.Equal(3, (await h.SnapshotAsync()).Cases.Count);
        Assert.Equal(3, h.Script.Count(API.BoardList.Name));
    }

    /// <summary>The reply of a list asked for before the connection went is dropped.</summary>
    [Fact]
    public async Task ALostConnectionDropsTheReplyOnItsWay()
    {
        await using var h = await Harness.StartAsync();
        await h.StartedAsync();
        h.Script.Lists.Hold(true);
        h.Script.SetCases([WireCase("1")]);
        await h.Ui.RunAsync(h.Source.Refresh);
        await Eventually.Holds(() => h.Script.Lists.Waiting == 1);
        await h.Ui.RunAsync(() => h.Source.ConnectionChanged(connected: false));
        Assert.Equal(Phase.Unavailable, (await h.SnapshotAsync()).Phase);
        h.Script.Lists.Hold(false);
        await h.IdleAsync();
        var s = await h.SnapshotAsync();
        Assert.True(s.Phase == Phase.Unavailable && s.Cases.Count == 2);
    }

    /// <summary>notify.accountsChanged lists the accounts and the board again.</summary>
    [Fact]
    public async Task AccountsChanged()
    {
        await using var h = await Harness.StartAsync();
        await h.StartedAsync();
        h.Script.AccountName = "Office";
        await h.Ui.RunAsync(h.Source.AccountsChanged);
        await h.IdleAsync();
        Assert.Equal(["Office"], (await h.SnapshotAsync()).Accounts.Select(a => a.Name));
        Assert.Equal(2, h.Script.Count(API.AccountList.Name));
        Assert.Equal(2, h.Script.Count(API.BoardList.Name));
    }

    /// <summary>A write shows at once, in one report, and the daemon's case follows.</summary>
    [Fact]
    public async Task OptimisticWrite()
    {
        await using var h = await Harness.StartAsync();
        await h.StartedAsync();
        h.Script.Writes.Hold(true);
        var (before, after) = await h.Ui.RunAsync(() =>
        {
            var b = h.Reports;
            h.Source.SetState(State.Them, Id("1"));
            return (b, h.Reports);
        });
        Assert.Equal(before + 1, after); // at once
        var c = await h.CAsync("1");
        Assert.True(c?.UserState == State.Them && c.Version == 1);
        h.Script.Writes.Hold(false);
        await h.IdleAsync();
        c = await h.CAsync("1");
        Assert.True(c?.UserState == State.Them && c.Version == 2 && h.Errors.Count == 0);
        // Done, then back.
        await h.Ui.RunAsync(() => h.Source.SetDone(true, Id("1")));
        Assert.True((await h.CAsync("1"))?.Done);
        await h.IdleAsync();
        c = await h.CAsync("1");
        Assert.True(c?.Visibility == Visibility.Done(T0) && c.Version == 3);
        await h.Ui.RunAsync(() => h.Source.SetDone(false, Id("1")));
        await h.IdleAsync();
        c = await h.CAsync("1");
        Assert.True(c?.Visibility == Visibility.Live && c.Version == 4);
        // Remind.
        var until = T0.AddDays(1);
        await h.Ui.RunAsync(() => h.Source.Remind(until, Id("2")));
        Assert.Equal(Visibility.Snoozed(until), (await h.CAsync("2"))?.Visibility);
        await h.IdleAsync();
        c = await h.CAsync("2");
        Assert.True(c?.Visibility == Visibility.Snoozed(until) && c.Version == 2);
    }

    /// <summary>A refused write is taken back and said; a list arriving while a write is under way keeps the write's change.</summary>
    [Fact]
    public async Task RefusedWriteIsReverted()
    {
        await using var h = await Harness.StartAsync();
        await h.StartedAsync();
        h.Script.WriteFailure = Daemon(ErrorCode.CaseNotFound, "internal words");
        Assert.True(await h.Ui.RunAsync(() =>
        {
            h.Source.SetDone(true, Id("1"));
            return h.Source.Snapshot.Cases.First(c => c.Id == Id("1")).Done;
        }));
        await h.IdleAsync();
        Assert.False((await h.CAsync("1"))?.Done);
        Assert.Equal(["Marking the case done failed: the case is no longer on the board."], h.Errors);
        h.Script.WriteFailure = null;
        h.Script.Writes.Hold(true);
        await h.Ui.RunAsync(() =>
        {
            h.Source.SetState(State.Info, Id("2"));
            h.Source.Refresh();
        });
        // The list answered (only the held write is left in the daemon).
        await h.UntilAsync(() => h.Script.Count(API.BoardList.Name) == 2 && h.Source.Snapshot.Accounts.Count == 1
            && h.Script.Writes.Waiting == 1 && h.Fake.InFlight == 1);
        await h.Ui.DrainAsync();
        Assert.Equal(State.Info, (await h.CAsync("2"))?.UserState); // still laid over the list
        h.Script.Writes.Hold(false);
        await h.IdleAsync();
        var c = await h.CAsync("2");
        Assert.True(c?.UserState == State.Info && c.Version == 2);
        // An unknown case is not written.
        var (reports, after, idle) = await h.Ui.RunAsync(() =>
        {
            var r = h.Reports;
            h.Source.SetDone(true, Id("nope"));
            return (r, h.Reports, h.Source.IsIdle);
        });
        Assert.True(idle && reports == after);
        Assert.Equal(1, h.Script.Count(API.BoardSetDone.Name));
    }

    /// <summary>A list asked for before a write's answer, and answered after it, keeps the newer case.</summary>
    [Fact]
    public async Task AListOlderThanTheWriteKeepsTheWritesCase()
    {
        await using var h = await Harness.StartAsync();
        await h.StartedAsync();
        h.Script.Lists.Hold(true);
        await h.Ui.RunAsync(h.Source.Refresh);
        await Eventually.Holds(() => h.Script.Lists.Waiting == 1); // it has version 1
        await h.Ui.RunAsync(() => h.Source.SetDone(true, Id("1")));
        await h.UntilAsync(() => h.Source.Snapshot.Cases.First(c => c.Id == Id("1")).Version == 2);
        h.Script.Lists.Hold(false);
        await h.IdleAsync();
        var c = await h.CAsync("1");
        Assert.True(c?.Version == 2 && c.Done);
    }

    /// <summary>The connection going during a write takes the change back and says why.</summary>
    [Fact]
    public async Task TheConnectionGoesDuringAWrite()
    {
        await using var h = await Harness.StartAsync();
        await h.StartedAsync();
        h.Script.Writes.Hold(true);
        await h.Ui.RunAsync(() => h.Source.SetDone(true, Id("1")));
        await Eventually.Holds(() => h.Script.Writes.Waiting == 1);
        h.Fake.CloseAll();
        await h.UntilAsync(() => h.Errors.Count > 0);
        Assert.False((await h.CAsync("1"))?.Done);
        Assert.Equal(["Marking the case done failed: the mail backend is not running."], h.Errors);
    }

    [Fact]
    public async Task ArchiveSaysWhatItDid()
    {
        await using var h = await Harness.StartAsync();
        await h.StartedAsync();
        await h.Ui.RunAsync(() => h.Source.Archive(Id("1")));
        Assert.True((await h.CAsync("1"))?.Done);
        await h.IdleAsync();
        Assert.Equal(["Archived 2 messages."], h.Notices);
        Assert.False((await h.CAsync("1"))?.CanArchive);
    }

    [Fact]
    public async Task DiscardDraft()
    {
        await using var h = await Harness.StartAsync();
        h.Script.SetCases([WireCase("1") with { Draft = new BoardDraft { DraftId = "d_1", Text = "Hi", Updated = T0 } }]);
        await h.StartedAsync();
        Assert.Equal(new DraftLink("d_1", "Hi"), (await h.CAsync("1"))?.Draft);
        await h.Ui.RunAsync(() => h.Source.DiscardDraft(Id("1")));
        Assert.Null((await h.CAsync("1"))?.Draft);
        await h.IdleAsync();
        var c = await h.CAsync("1");
        Assert.True(c is { Draft: null, Version: 2 });
    }

    /// <summary>
    /// The inline editor's Discard deletes the draft it edits: through the
    /// case while the case links it, alone when it no longer does, and a
    /// refusal is thrown (the editor says so), never a toast of the source.
    /// </summary>
    [Fact]
    public async Task DiscardTheEditorsDraft()
    {
        await using var h = await Harness.StartAsync();
        h.Script.SetCases([WireCase("1") with { Draft = new BoardDraft { DraftId = "d_1", Text = "Hi", Updated = T0 } }]);
        await h.StartedAsync();
        await h.Ui.InvokeAsync(() => h.Source.DiscardDraftAsync("d_1", "acc_1", Id("1")));
        var c = await h.CAsync("1");
        Assert.True(c is { Draft: null, Version: 2 });
        Assert.Equal(1, h.Script.Count(API.BoardDiscardDraft.Name));
        Assert.Empty(h.Script.Deleted);
    }

    [Fact]
    public async Task DiscardADraftTheCaseNoLongerLinks()
    {
        await using var h = await Harness.StartAsync();
        h.Script.SetCases([WireCase("1") with { Draft = new BoardDraft { DraftId = "d_2", Text = "Newer", Updated = T0 } }]);
        await h.StartedAsync();
        await h.Ui.InvokeAsync(() => h.Source.DiscardDraftAsync("d_1", "acc_1", Id("1")));
        Assert.Equal([new DraftDeleteParams { AccountId = "acc_1", DraftId = "d_1" }], h.Script.Deleted);
        Assert.Equal(0, h.Script.Count(API.BoardDiscardDraft.Name));
        Assert.True((await h.CAsync("1"))?.Draft?.Id == "d_2", "the case's own link stays");
    }

    [Fact]
    public async Task ARefusedDiscardIsThrownAndTheLinkComesBack()
    {
        await using var h = await Harness.StartAsync();
        h.Script.SetCases([WireCase("1") with { Draft = new BoardDraft { DraftId = "d_1", Text = "Hi", Updated = T0 } }]);
        await h.StartedAsync();
        h.Script.WriteFailure = Daemon(ErrorCode.StorageError, "disk");
        await Assert.ThrowsAsync<RpcException>(() => h.Ui.InvokeAsync(() => h.Source.DiscardDraftAsync("d_1", "acc_1", Id("1"))));
        Assert.True((await h.CAsync("1"))?.Draft?.Id == "d_1");
        Assert.True(h.Errors.Count == 0, "the editor says it");
        // Go: after Stop it is cancelled at once, nothing asked.
        await h.Ui.RunAsync(h.Source.Stop);
        await Assert.ThrowsAnyAsync<OperationCanceledException>(
            () => h.Ui.InvokeAsync(() => h.Source.DiscardDraftAsync("d_1", "acc_1", Id("1"))));
        Assert.Equal(1, h.Script.Count(API.BoardDiscardDraft.Name));
    }

    /// <summary>
    /// Unstar: board.unflag, nothing changed beforehand, then the board
    /// listed again so the rules' new state arrives without waiting for the
    /// notification.
    /// </summary>
    [Fact]
    public async Task UnflagListsTheBoardAgain()
    {
        await using var h = await Harness.StartAsync();
        h.Script.SetCases([WireCase("1"), WireCase("2", BoardState.Hot) with { RuleReason = BoardReason.HotFlagged }]);
        await h.StartedAsync();
        var lists = h.Script.Count(API.BoardList.Name);
        Assert.Equal(BoardReason.HotFlagged, (await h.CAsync("2"))?.RuleReason.Value);
        var c = await h.Ui.RunAsync(() =>
        {
            h.Source.Unflag(Id("2"));
            return h.Source.Snapshot.Cases.FirstOrDefault(x => x.Id == Id("2"));
        });
        Assert.True(c?.RuleReason == BoardReason.HotFlagged && c.RuleState == State.Hot, "nothing optimistic");
        await h.IdleAsync();
        Assert.Equal(lists + 1, h.Script.Count(API.BoardList.Name));
        Assert.Equal(1, h.Script.Count(API.BoardUnflag.Name));
        c = await h.CAsync("2");
        Assert.True(c?.RuleReason == BoardReason.YouAddressed && c.RuleState == State.You);
        Assert.Empty(h.Errors);
        // Refused: said, and nothing listed again.
        h.Script.WriteFailure = Daemon(ErrorCode.CaseNotFound, "x");
        await h.Ui.RunAsync(() => h.Source.Unflag(Id("1")));
        await h.IdleAsync();
        Assert.Equal(["Removing the star failed: the case is no longer on the board."], h.Errors);
        Assert.Equal(lists + 1, h.Script.Count(API.BoardList.Name));
        // An unknown case: nothing asked.
        await h.Ui.RunAsync(() => h.Source.Unflag(Id("9")));
        await h.IdleAsync();
        Assert.Equal(2, h.Script.Count(API.BoardUnflag.Name));
    }

    [Fact]
    public async Task Commitments()
    {
        await using var h = await Harness.StartAsync();
        h.Script.SetCommitments([Commitment()]);
        await h.StartedAsync();
        Assert.Equal([CommitmentState.Open], (await h.SnapshotAsync()).Commitments.Select(k => k.State));
        await h.Ui.RunAsync(() => h.Source.SetCommitmentDone(true, "k_1"));
        Assert.Equal([CommitmentState.Done], (await h.SnapshotAsync()).Commitments.Select(k => k.State));
        await h.IdleAsync();
        Assert.Equal([CommitmentState.Done], (await h.SnapshotAsync()).Commitments.Select(k => k.State));
        Assert.Empty(h.Errors);
        // Refused: back to done.
        h.Script.WriteFailure = Daemon(ErrorCode.StorageError, "x");
        var states = await h.Ui.RunAsync(() =>
        {
            h.Source.SetCommitmentDone(false, "k_1");
            return h.Source.Snapshot.Commitments.Select(k => k.State).ToList();
        });
        Assert.Equal([CommitmentState.Open], states);
        await h.IdleAsync();
        Assert.Equal([CommitmentState.Done], (await h.SnapshotAsync()).Commitments.Select(k => k.State));
        Assert.Equal(["Changing the promise failed: the mail backend could not save it."], h.Errors);
    }

    /// <summary>A promise ticked off stays ticked while a list asked for before the answer arrives; a list asked for later has the last word.</summary>
    [Fact]
    public async Task AListOlderThanTheTickKeepsThePromiseTicked()
    {
        await using var h = await Harness.StartAsync();
        h.Script.SetCommitments([Commitment()]);
        await h.StartedAsync();
        h.Script.Lists.Hold(true);
        await h.Ui.RunAsync(h.Source.Refresh);
        await Eventually.Holds(() => h.Script.Lists.Waiting == 1); // it has the promise open
        await h.Ui.RunAsync(() => h.Source.SetCommitmentDone(true, "k_1"));
        await Eventually.Holds(() => h.Script.Count(API.BoardSetCommitment.Name) == 1);
        h.Script.Lists.Hold(false);
        await h.IdleAsync();
        Assert.Equal([CommitmentState.Done], (await h.SnapshotAsync()).Commitments.Select(k => k.State));
        // The daemon reopens it (another client): the next list says so.
        h.Script.SetCommitments([Commitment(BoardCommitmentState.Open)]);
        await h.RefreshAsync();
        Assert.Equal([CommitmentState.Open], (await h.SnapshotAsync()).Commitments.Select(k => k.State));
    }

    /// <summary>The conversation is asked for once per version.</summary>
    [Fact]
    public async Task MessagesAreCachedByVersion()
    {
        await using var h = await Harness.StartAsync();
        await h.StartedAsync();
        await h.LoadAsync("1");
        var c = await h.CAsync("1");
        Assert.Equal(["version 1"], c?.Messages?.Select(m => m.Text) ?? []);
        Assert.Equal("Ann", c?.Messages?[0].From);
        Assert.True(await h.Ui.RunAsync(() =>
        {
            h.Source.LoadMessages(Id("1"));
            return h.Source.IsIdle;
        }));
        Assert.Equal(1, h.Script.Count(API.BoardGet.Name));
        // A new version: the old messages stay shown until the new arrive.
        h.Script.SetCases([WireCase("1", version: 5), WireCase("2", BoardState.Hot)]);
        await h.RefreshAsync();
        c = await h.CAsync("1");
        Assert.True(c?.Version == 5);
        Assert.Equal(["version 1"], c.Messages?.Select(m => m.Text) ?? []);
        await h.LoadAsync("1");
        Assert.Equal(["version 5"], (await h.CAsync("1"))?.Messages?.Select(m => m.Text) ?? []);
        Assert.Equal(2, h.Script.Count(API.BoardGet.Name));
        // A failure with nothing loaded says so; asking again retries.
        h.Script.GetFailure = Daemon(ErrorCode.StorageError, "x");
        c = await h.Ui.RunAsync(() =>
        {
            h.Source.LoadMessages(Id("2"));
            return h.Source.Snapshot.Cases.FirstOrDefault(x => x.Id == Id("2"));
        });
        Assert.True(c is { Messages: null, MessagesFailed: false }); // loading
        await h.IdleAsync();
        Assert.True((await h.CAsync("2"))?.MessagesFailed == true && h.Errors.Count == 0); // the detail says it; no toast
        h.Script.GetFailure = null;
        Assert.False(await h.Ui.RunAsync(() =>
        {
            h.Source.LoadMessages(Id("2"));
            return h.Source.Snapshot.Cases.First(x => x.Id == Id("2")).MessagesFailed;
        }));
        await h.IdleAsync();
        Assert.NotNull((await h.CAsync("2"))?.Messages);
    }

    /// <summary>The source keeps the conversations of the most recently opened cases only.</summary>
    [Fact]
    public async Task ConversationsAreBounded()
    {
        await using var h = await Harness.StartAsync();
        var n = DaemonBoardSource.ConversationsKept + 5;
        h.Script.SetCases([.. Enumerable.Range(1, n).Select(i => WireCase($"{i}"))]);
        await h.StartedAsync();
        for (var i = 1; i <= n; i++)
        {
            await h.LoadAsync($"{i}");
        }
        var kept = (await h.SnapshotAsync()).Cases.Where(c => c.Messages is not null).Select(c => c.Id);
        Assert.Equal(Enumerable.Range(6, n - 5).Select(i => Id($"{i}")), kept);
        // Opening a kept one again keeps it longest.
        await h.Ui.RunAsync(() =>
        {
            h.Source.LoadMessages(Id("6"));
            h.Source.LoadMessages(Id("1"));
        });
        await h.IdleAsync();
        Assert.True((await h.CAsync("6"))?.Messages is not null && (await h.CAsync("7"))?.Messages is null
            && (await h.CAsync("1"))?.Messages is not null);
    }

    /// <summary>A conversation whose case left the board while it loaded is not kept, and nothing waits for it.</summary>
    [Fact]
    public async Task AConversationOfACaseThatLeftIsDropped()
    {
        await using var h = await Harness.StartAsync();
        await h.StartedAsync();
        h.Script.Gets.Hold(true);
        await h.Ui.RunAsync(() => h.Source.LoadMessages(Id("2")));
        await Eventually.Holds(() => h.Script.Gets.Waiting == 1);
        h.Script.SetCases([WireCase("1")]);
        await h.Ui.RunAsync(h.Source.Refresh);
        // board.get is still held: the loading was pruned.
        await h.UntilAsync(() => h.Source.IsIdle && h.Script.Count(API.BoardList.Name) == 2);
        Assert.Single((await h.SnapshotAsync()).Cases);
        h.Script.SetCases([WireCase("1"), WireCase("2", BoardState.Hot)]);
        h.Script.Gets.Hold(false);
        await h.IdleAsync();
        await h.RefreshAsync();
        Assert.Null((await h.CAsync("2"))?.Messages);
        await h.LoadAsync("2");
        Assert.NotNull((await h.CAsync("2"))?.Messages);
        Assert.Equal(2, h.Script.Count(API.BoardGet.Name));
    }

    /// <summary>Go TestDaemonConvert: unknown states, a snoozed case, the issue, the annotation, the person, the times in UTC.</summary>
    [Fact]
    public void Convert()
    {
        var prague = TimeZoneInfo.FindSystemTimeZoneById("Europe/Prague");
        var remind = TimeZoneInfo.ConvertTime(T0, prague).AddHours(1);
        var w = WireCase("1", BoardState.Them, 3) with
        {
            UserState = new BoardState("later"), // unknown: automatic
            RuleState = "someday", // unknown: for reading
            Visibility = BoardVisibility.Snoozed,
            RemindAt = remind,
            Issue = new BoardIssue { Key = "K-1", Status = "Open", StatusCategory = IssueStatusCategory.InProgress },
            Annotation = new BoardAnnotation
            {
                State = BoardState.Hot,
                Title = "T",
                Summary = "",
                Why = "",
                Tasks = ["a"],
                Due = new BoardDue { At = remind, Quote = "by then", MessageId = "m_1" },
                Source = "s",
                At = remind,
                Stale = true,
            },
            Person = new Address { Email = "  x@example.invalid " },
        };
        var c = DaemonBoardSource.Convert(w);
        Assert.True(c.UserState is null && c.RuleState == State.Info);
        Assert.True(c.Visibility.RemindAt is { } until && until == remind && until.Offset == TimeSpan.Zero);
        Assert.True(c.Issue?.Style == JiraStatusStyle.InProgress && c.Person == "x@example.invalid");
        var a = Assert.IsType<Annotation>(c.Annotation);
        Assert.True(a.State == State.Hot && a.Due == remind && a.DueQuote == "by then" && a.DueMessage == "m_1" && a.Stale);
        // A snoozed case without a time is live; no reply message, no target.
        c = DaemonBoardSource.Convert(w with { RemindAt = null, ReplyMessageId = "" });
        Assert.True(c.Visibility.IsLive && c.Reply is null);
        // Commitments: a state this client does not know is not shown.
        Assert.Equal(CommitmentState.Closed, DaemonBoardSource.Convert(Commitment("kept")).State);
    }

    /// <summary>Go TestDaemonAccountInfo: the accounts' names and capsules as the sidebar shows them, and whether a reply can be written.</summary>
    [Fact]
    public void AccountInfoOfAnAccount()
    {
        static Account A(string id, AccountConfig config, IReadOnlyList<Capability>? caps = null) => new()
        {
            Id = id,
            Config = config,
            Enabled = true,
            State = new SyncState { AccountId = id, Status = SyncStatus.Idle },
            Capabilities = caps,
        };
        var mail = DaemonBoardSource.Convert(A("a", new AccountConfig { Name = "", Email = " me@example.invalid " }));
        Assert.Equal(new AccountInfo("a", "me@example.invalid", "IMAP", CanReply: true), mail);
        Assert.Equal("M365", DaemonBoardSource.Convert(A("g", new AccountConfig { Kind = AccountKind.Graph, Name = "Work", Email = "" })).Badge);
        var jira = DaemonBoardSource.Convert(A(
            "j", new AccountConfig { Kind = AccountKind.Jira, Name = "Issues", Email = "" },
            [Capability.Comment, Capability.Forward, Capability.Transition]));
        Assert.True(jira.Badge == "JIRA" && jira.CanReply);
        Assert.False(DaemonBoardSource.Convert(A("m", new AccountConfig { Name = "M", Email = "m@x" }, [Capability.Move])).CanReply);
    }

    /// <summary>A released or never closed hold.</summary>
    private sealed class Gate
    {
        private readonly Lock gate = new();
        private bool holding;
        private int waiting;
        private TaskCompletionSource released = new(TaskCreationOptions.RunContinuationsAsynchronously);

        /// <summary>Calls waiting at the gate.</summary>
        public int Waiting
        {
            get
            {
                lock (gate)
                {
                    return waiting;
                }
            }
        }

        /// <summary>Holds the replies from now on, or lets them through again and releases the ones held.</summary>
        public void Hold(bool on)
        {
            TaskCompletionSource? release = null;
            lock (gate)
            {
                if (on && !holding)
                {
                    holding = true;
                    released = new(TaskCreationOptions.RunContinuationsAsynchronously);
                }
                else if (!on)
                {
                    holding = false;
                    release = released;
                }
            }
            release?.TrySetResult();
        }

        public async Task PassAsync()
        {
            Task wait;
            lock (gate)
            {
                if (!holding)
                {
                    return;
                }
                waiting++;
                wait = released.Task;
            }
            await wait.WaitAsync(TimeSpan.FromSeconds(20));
            lock (gate)
            {
                waiting--;
            }
        }
    }

    /// <summary>The daemon's side of the board: its cases, and what each method did. Thread-safe: the handlers run on the fake's threads.</summary>
    private sealed class Script
    {
        private readonly Lock gate = new();
        private readonly List<string> calls = [];
        private readonly List<DraftDeleteParams> deleted = [];
        private List<BoardCase> cases = [WireCase("1"), WireCase("2", BoardState.Hot)];
        private List<BoardCommitment> commitments = [];
        private bool enabled = true;
        private bool ready = true;

        public Gate Lists { get; } = new();

        public Gate Writes { get; } = new();

        public Gate Gets { get; } = new();

        public string AccountName { get; set; } = "Work";

        public BoardRun? LastRun { get; set; }

        public RpcException? ListFailure { get; set; }

        public RpcException? WriteFailure { get; set; }

        public RpcException? GetFailure { get; set; }

        public IReadOnlyList<DraftDeleteParams> Deleted
        {
            get
            {
                lock (gate)
                {
                    return [.. deleted];
                }
            }
        }

        public void SetCases(IEnumerable<BoardCase> value)
        {
            lock (gate)
            {
                cases = [.. value];
            }
        }

        public void SetCommitments(IEnumerable<BoardCommitment> value)
        {
            lock (gate)
            {
                commitments = [.. value];
            }
        }

        public void SetEnabled(bool on, bool ready)
        {
            lock (gate)
            {
                enabled = on;
                this.ready = ready;
            }
        }

        public int Count(string method)
        {
            lock (gate)
            {
                return calls.Count(c => c == method);
            }
        }

        public void Install(FakeDaemon fake)
        {
            fake.On(API.BoardList.Name, (FakeDaemon.MethodHandler)(_ => ListAsync()));
            fake.On(API.BoardGet.Name, (FakeDaemon.MethodHandler)GetAsync);
            fake.On(API.AccountList.Name, (FakeDaemon.MethodHandler)(_ => Task.FromResult(AccountList())));
            fake.On(API.BoardSetState.Name, (FakeDaemon.MethodHandler)(async p =>
            {
                var q = JsonCoding.Decode<BoardSetStateParams>(p);
                var c = await WriteAsync(API.BoardSetState.Name, q.CaseId, c => c with { UserState = q.State });
                return JsonCoding.EncodeToString(new BoardSetStateResult { Case = c });
            }));
            fake.On(API.BoardSetDone.Name, (FakeDaemon.MethodHandler)(async p =>
            {
                var q = JsonCoding.Decode<BoardSetDoneParams>(p);
                var c = await WriteAsync(API.BoardSetDone.Name, q.CaseId, c => c with
                {
                    Visibility = q.Done ? BoardVisibility.Done : BoardVisibility.Live,
                    DoneAt = q.Done ? T0 : null,
                    RemindAt = null,
                });
                return JsonCoding.EncodeToString(new BoardSetDoneResult { Case = c });
            }));
            fake.On(API.BoardRemind.Name, (FakeDaemon.MethodHandler)(async p =>
            {
                var q = JsonCoding.Decode<BoardRemindParams>(p);
                var c = await WriteAsync(API.BoardRemind.Name, q.CaseId, c => c with
                {
                    Visibility = q.Until is null ? BoardVisibility.Live : BoardVisibility.Snoozed,
                    RemindAt = q.Until,
                });
                return JsonCoding.EncodeToString(new BoardRemindResult { Case = c });
            }));
            fake.On(API.BoardArchive.Name, (FakeDaemon.MethodHandler)(async p =>
            {
                var q = JsonCoding.Decode<BoardArchiveParams>(p);
                var c = await WriteAsync(API.BoardArchive.Name, q.CaseId, c => c with { Visibility = BoardVisibility.Done, CanArchive = false });
                return JsonCoding.EncodeToString(new BoardArchiveResult { Archived = 2, Case = c });
            }));
            fake.On(API.BoardDiscardDraft.Name, (FakeDaemon.MethodHandler)(async p =>
            {
                var q = JsonCoding.Decode<BoardDiscardDraftParams>(p);
                var c = await WriteAsync(API.BoardDiscardDraft.Name, q.CaseId, c => c with { Draft = null });
                return JsonCoding.EncodeToString(new BoardDiscardDraftResult { Case = c });
            }));
            fake.On(API.BoardUnflag.Name, (FakeDaemon.MethodHandler)(async p =>
            {
                var q = JsonCoding.Decode<BoardUnflagParams>(p);
                // The case as stored: the rules re-evaluate it later (the
                // board.list after the write shows what they made of it).
                var c = await WriteAsync(API.BoardUnflag.Name, q.CaseId, c => c);
                Unflagged(q.CaseId);
                return JsonCoding.EncodeToString(new BoardUnflagResult { Case = c, Unflagged = 2 });
            }));
            fake.On(API.BoardSetCommitment.Name, (FakeDaemon.MethodHandler)SetCommitmentAsync);
            fake.On(API.DraftDelete.Name, (FakeDaemon.MethodHandler)(p =>
            {
                lock (gate)
                {
                    calls.Add(API.DraftDelete.Name);
                    deleted.Add(JsonCoding.Decode<DraftDeleteParams>(p));
                }
                return Task.FromResult("{}");
            }));
        }

        private string AccountList()
        {
            string name;
            lock (gate)
            {
                calls.Add(API.AccountList.Name);
                name = AccountName;
            }
            Account A(string id, string n, string email, bool on) => new()
            {
                Id = id,
                Config = new AccountConfig { Name = n, Email = email },
                Enabled = on,
                State = new SyncState { AccountId = id, Status = SyncStatus.Idle },
            };
            return JsonCoding.EncodeToString(new AccountListResult
            {
                Accounts = [A("acc_1", name, "me@example.invalid", true), A("acc_2", "Paused", "p@example.invalid", false)],
            });
        }

        private async Task<string> ListAsync()
        {
            BoardListResult result;
            RpcException? failure;
            lock (gate)
            {
                calls.Add(API.BoardList.Name);
                // What the daemon has at the time of the call, sent when released.
                result = new BoardListResult
                {
                    Cases = enabled ? [.. cases] : [],
                    Commitments = [.. commitments],
                    Enabled = enabled,
                    Assistant = false,
                    Triage = new BoardTriage { LastRun = LastRun, AnnotatedTodayAuto = 2, Queue = 4 },
                    Ready = ready,
                };
                failure = ListFailure;
            }
            await Lists.PassAsync();
            if (failure is not null)
            {
                throw failure;
            }
            return JsonCoding.EncodeToString(result);
        }

        private async Task<string> GetAsync(string p)
        {
            BoardCase? c;
            RpcException? failure;
            var q = JsonCoding.Decode<BoardGetParams>(p);
            lock (gate)
            {
                calls.Add(API.BoardGet.Name);
                failure = GetFailure;
                c = cases.FirstOrDefault(x => x.Id == q.CaseId);
            }
            await Gets.PassAsync();
            if (failure is not null)
            {
                throw failure;
            }
            if (c is null)
            {
                throw Daemon(ErrorCode.CaseNotFound, "no case");
            }
            var m = new BoardMessage
            {
                Id = "m_a",
                FolderId = "f_inbox",
                From = new Address { Name = "Ann", Email = "ann@example.invalid" },
                Date = T0,
                Mine = false,
                Text = $"version {c.Version}",
            };
            return JsonCoding.EncodeToString(new BoardGetResult { Case = c, Messages = [m] });
        }

        // A write: changes case id (version + 1) and answers it.
        private async Task<BoardCase> WriteAsync(string method, BoardCaseId id, Func<BoardCase, BoardCase> change)
        {
            lock (gate)
            {
                calls.Add(method);
            }
            await Writes.PassAsync();
            lock (gate)
            {
                if (WriteFailure is { } failure)
                {
                    throw failure;
                }
                var i = cases.FindIndex(c => c.Id == id);
                if (i < 0)
                {
                    throw Daemon(ErrorCode.CaseNotFound, "no case");
                }
                cases[i] = change(cases[i]) with { Version = cases[i].Version + 1 };
                return cases[i];
            }
        }

        // What the rules make of an unflagged case: no longer hot.
        private void Unflagged(BoardCaseId id)
        {
            lock (gate)
            {
                var i = cases.FindIndex(c => c.Id == id);
                if (i >= 0)
                {
                    cases[i] = cases[i] with
                    {
                        RuleState = BoardState.You,
                        RuleReason = BoardReason.YouAddressed,
                        Version = cases[i].Version + 1,
                    };
                }
            }
        }

        private async Task<string> SetCommitmentAsync(string p)
        {
            lock (gate)
            {
                calls.Add(API.BoardSetCommitment.Name);
            }
            await Writes.PassAsync();
            var q = JsonCoding.Decode<BoardSetCommitmentParams>(p);
            lock (gate)
            {
                if (WriteFailure is { } failure)
                {
                    throw failure;
                }
                var i = commitments.FindIndex(k => k.Id == q.CommitmentId);
                if (i < 0)
                {
                    throw Daemon(ErrorCode.CaseNotFound, "no commitment");
                }
                commitments[i] = commitments[i] with { State = q.Done ? BoardCommitmentState.Done : BoardCommitmentState.Open };
                return JsonCoding.EncodeToString(new BoardSetCommitmentResult { Commitment = commitments[i] });
            }
        }
    }

    /// <summary>The fake daemon, its script, the clock, the UI thread and the source on it.</summary>
    private sealed class Harness : IAsyncDisposable
    {
        private readonly List<string> errors = [];
        private readonly List<string> notices = [];
        private int reports;

        private Harness()
        {
        }

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public FakeDaemon Fake { get; } = new();

        public Script Script { get; } = new();

        public FakeTimeProvider Clock { get; } = new(T0);

        public RpcClient Client { get; private set; } = null!;

        public DaemonBoardSource Source { get; private set; } = null!;

        public int Reports => Volatile.Read(ref reports);

        public IReadOnlyList<string> Errors
        {
            get
            {
                lock (errors)
                {
                    return [.. errors];
                }
            }
        }

        public IReadOnlyList<string> Notices
        {
            get
            {
                lock (notices)
                {
                    return [.. notices];
                }
            }
        }

        public static async Task<Harness> StartAsync(bool connect = true)
        {
            var h = new Harness();
            h.Script.Install(h.Fake);
            await h.Fake.StartAsync();
            h.Client = new RpcClient(h.Fake.Path, PortableKeyFilePolicy.Instance);
            if (connect)
            {
                await h.Client.ConnectAsync(TestContext.Current.CancellationToken);
            }
            h.Source = await h.Ui.RunAsync(() =>
            {
                var s = new DaemonBoardSource(h.Client, time: h.Clock, pending: h.Pending);
                s.OnChange = () => Interlocked.Increment(ref h.reports);
                s.OnError = e =>
                {
                    lock (h.errors)
                    {
                        h.errors.Add(e);
                    }
                };
                s.OnNotice = n =>
                {
                    lock (h.notices)
                    {
                        h.notices.Add(n);
                    }
                };
                return s;
            });
            return h;
        }

        /// <summary>Starts and waits for the board and the accounts.</summary>
        public async Task StartedAsync()
        {
            await Ui.RunAsync(Source.Start);
            await IdleAsync();
        }

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending, Fake);

        public async Task RefreshAsync()
        {
            await Ui.RunAsync(Source.Refresh);
            await IdleAsync();
        }

        public async Task LoadAsync(string n)
        {
            await Ui.RunAsync(() => Source.LoadMessages(Id(n)));
            await IdleAsync();
        }

        /// <summary>Moves the clock on, then waits until nothing is left to happen.</summary>
        public async Task AdvanceAsync(TimeSpan d)
        {
            await IdleAsync();
            Clock.Advance(d);
            await IdleAsync();
        }

        public Task<Snapshot> SnapshotAsync() => Ui.RunAsync(() => Source.Snapshot);

        public Task<Case?> CAsync(string n) => Ui.RunAsync(() => Source.Snapshot.Cases.FirstOrDefault(c => c.Id == Id(n)));

        /// <summary>Waits until <paramref name="condition"/> holds on the UI thread (Swift's waitUntil, for a reply held on purpose).</summary>
        public async Task UntilAsync(Func<bool> condition)
        {
            var deadline = DateTime.UtcNow + TimeSpan.FromSeconds(10);
            while (!await Ui.RunAsync(condition))
            {
                if (DateTime.UtcNow > deadline)
                {
                    throw new TimeoutException("the condition did not hold in time");
                }
                await Task.Delay(2);
            }
        }

        public async ValueTask DisposeAsync()
        {
            await Ui.RunAsync(Source.Dispose);
            Script.Lists.Hold(false);
            Script.Writes.Hold(false);
            Script.Gets.Hold(false);
            try
            {
                await Pending.IdleAsync().WaitAsync(TimeSpan.FromSeconds(10));
                await Fake.IdleAsync().WaitAsync(TimeSpan.FromSeconds(10));
            }
            catch (TimeoutException)
            {
                // Torn down all the same; the test's own asserts said what went wrong.
            }
            Client.Dispose();
            await Fake.DisposeAsync();
            Ui.Dispose();
        }
    }
}
