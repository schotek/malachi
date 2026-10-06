// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The emulated writes of InMemoryBoardSource (BoardSource.swift,
// ui/internal/board/source.go InMemorySource); Swift and Go test them only
// through the board controller (BoardControllerTests,
// controller_test.go), which comes with the controllers.

using System.Collections.Generic;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Xunit;
using static Malachi.Core.Boards.Board;
using F = Malachi.Core.Tests.Boards.BoardFixture;

namespace Malachi.Core.Tests.Boards;

public sealed class InMemoryBoardSourceTests
{
    private static (InMemoryBoardSource Source, List<string> Log) Make(params Case[] cases)
    {
        var source = new InMemoryBoardSource(new Snapshot
        {
            Accounts = F.Accounts,
            Cases = cases,
            Commitments = [new Commitment { Id = new("k1"), CaseId = F.Id("c1"), Text = "Do it" }],
        });
        var log = new List<string>();
        source.OnChange = () => log.Add("change");
        source.OnNotice = s => log.Add("notice " + s);
        source.OnError = s => log.Add("error " + s);
        return (source, log);
    }

    [Fact]
    public void WritesChangeTheCaseAndReportIt()
    {
        var (source, log) = Make(F.Mk("c1", State.You), F.Mk("c2", State.Hot, messagesNull: true, draft: "d"));
        var before = source.Snapshot;
        source.SetState(State.Them, F.Id("c1"));
        Assert.Equal(State.Them, source.Snapshot.FindCase(F.Id("c1"))!.UserState);
        Assert.Null(before.FindCase(F.Id("c1"))!.UserState); // a snapshot handed out stays as it was
        source.SetState(null, F.Id("c1"));
        Assert.Null(source.Snapshot.FindCase(F.Id("c1"))!.UserState);
        source.SetDone(true, F.Id("c1"));
        Assert.True(source.Snapshot.FindCase(F.Id("c1"))!.Done);
        source.SetDone(false, F.Id("c1"));
        Assert.True(source.Snapshot.FindCase(F.Id("c1"))!.Visibility.IsLive);
        var until = F.Now.AddDays(1);
        source.Remind(until, F.Id("c1"));
        Assert.Equal(until, source.Snapshot.FindCase(F.Id("c1"))!.Visibility.RemindAt);
        source.Remind(null, F.Id("c1"));
        Assert.True(source.Snapshot.FindCase(F.Id("c1"))!.Visibility.IsLive);
        source.DiscardDraft(F.Id("c2"));
        Assert.Null(source.Snapshot.FindCase(F.Id("c2"))!.Draft);
        source.LoadMessages(F.Id("c2"));
        Assert.Empty(source.Snapshot.FindCase(F.Id("c2"))!.Messages!);
        source.SetCommitmentDone(true, new BoardCommitmentId("k1"));
        Assert.Equal(CommitmentState.Done, source.Snapshot.Commitments[0].State);
        Assert.Equal(9, log.Count);
        Assert.All(log, l => Assert.Equal("change", l));
    }

    [Fact]
    public void WritesThatChangeNothingCallNoOne()
    {
        var done = F.Mk("c2", done: true);
        var (source, log) = Make(F.Mk("c1"), done);
        source.SetState(null, F.Id("c1"));
        source.SetDone(false, F.Id("c1"));
        source.Remind(null, F.Id("c1")); // not snoozed
        source.Remind(null, F.Id("c2")); // done stays done
        source.DiscardDraft(F.Id("c1"));
        source.LoadMessages(F.Id("c1")); // loaded already
        source.SetState(State.Hot, F.Id("zz"));
        source.SetCommitmentDone(false, new BoardCommitmentId("k1"));
        source.SetCommitmentDone(true, new BoardCommitmentId("zz"));
        source.Archive(F.Id("zz"));
        source.Unflag(F.Id("zz"));
        source.Replace(source.Snapshot with { });
        source.Refresh();
        Assert.Empty(log);
        Assert.True(source.Snapshot.FindCase(F.Id("c2"))!.Done);
    }

    [Fact]
    public void ArchiveSaysWhatItDid()
    {
        var (source, log) = Make(F.Mk("c1", count: 3) with { CanArchive = true }, F.Mk("c2"));
        source.Archive(F.Id("c1"));
        source.Archive(F.Id("c2"));
        Assert.Equal(
            ["change", "notice Archived 3 messages.", "change", "notice Marked as done. This account has no archive."], log);
        Assert.True(source.Snapshot.FindCase(F.Id("c1"))!.Done);
        Assert.False(source.Snapshot.FindCase(F.Id("c1"))!.CanArchive);
    }

    [Fact]
    public void UnflagIsAPlaceholder()
    {
        var (source, log) = Make(F.Mk("c1"));
        source.Unflag(F.Id("c1"));
        Assert.Equal(["notice " + Board.Text.Later], log);
    }

    [Fact]
    public async Task DiscardOfTheInlineEditorDropsTheLink()
    {
        var (source, log) = Make(F.Mk("c1", draft: "text"));
        IBoardSource s = source;
        await s.DiscardDraftAsync(new DraftId("d_c1"), F.AccountA, F.Id("c1"));
        Assert.Null(source.Snapshot.FindCase(F.Id("c1"))!.Draft);
        Assert.Equal(["change"], log);
        Assert.Equal(Phase.Ready, s.Phase);
    }

    [Fact]
    public void ReplaceReportsANewSnapshot()
    {
        var (source, log) = Make(F.Mk("c1"));
        source.Replace(source.Snapshot with { Phase = Phase.Failed });
        Assert.Equal(["change"], log);
        Assert.Equal(Phase.Failed, source.Snapshot.Phase);
    }
}
