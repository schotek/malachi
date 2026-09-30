// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/ThreadModelTests.swift, the
// counterpart of ui/internal/window/thread_model_test.go (every test; the
// Swift suite ports all of them). RemovedMembersSnapshotIsIndependent is
// Windows-only: the undo of a removal restores records a C# caller could
// otherwise have aliased.

using System;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Xunit;
using static Malachi.Core.Tests.Model.MailModelTests;

namespace Malachi.Core.Tests.Model;

public sealed class ThreadModelTests
{
    /// <summary>2026-09-01T10:00:00Z (thread_model_test.go <c>threadBase</c>).</summary>
    internal static readonly DateTimeOffset ThreadBase = DateTimeOffset.FromUnixTimeSeconds(1_788_256_800);

    /// <summary>
    /// A folder member of conversation <paramref name="tid"/>, dated
    /// <paramref name="n"/> hours after <see cref="ThreadBase"/>, from the
    /// given sender (thread_model_test.go <c>member</c>). A null
    /// <paramref name="tid"/> is a message an older daemon has not linked yet.
    /// </summary>
    internal static MessageSummary Member(string id, string? tid, int n, string from, params Flag[] flags) => new()
    {
        Id = id,
        AccountId = "acc",
        FolderId = "f_inbox",
        ThreadId = tid is null ? null : (ThreadId?)new ThreadId(tid),
        From = [new Address { Name = from, Email = from + "@example.invalid" }],
        Subject = "s-" + id,
        Date = ThreadBase.AddHours(n),
        Snippet = "p-" + id,
        Flags = flags,
        HasAttachments = false,
        Size = 0,
    };

    /// <summary>
    /// A conversation summary over <paramref name="latest"/> and
    /// <paramref name="count"/> members, of which <paramref name="unread"/>
    /// are unread (thread_model_test.go <c>thr</c>).
    /// </summary>
    internal static ThreadSummary Thr(string tid, int count, int unread, MessageSummary latest, params Flag[] flags) => new()
    {
        Id = tid,
        AccountId = "acc",
        Subject = latest.Subject,
        Participants = latest.From,
        MessageCount = count,
        UnreadCount = unread,
        LatestDate = latest.Date,
        Latest = latest,
        Snippet = latest.Snippet,
        Flags = flags,
        HasAttachments = latest.HasAttachments,
        FolderIds = ["f_inbox"],
    };

    internal static MailModel GroupedModel(params ThreadSummary[] threads)
    {
        var m = new MailModel(grouped: true, listFilter: MessageFilter.All);
        m.SetThreads(threads, new PageInfo { Total = threads.Length });
        return m;
    }

    internal static ListKey[] Keys(MailModel m) => [.. m.Rows.Select(r => r.Key)];

    [Fact]
    public void RebuildRowsCollapsedAndExpanded()
    {
        var a2 = Member("a2", "t_a", 2, "bob");
        var b1 = Member("b1", "t_b", 1, "carol");
        var m = GroupedModel(Thr("t_a", 2, 1, a2), Thr("t_b", 1, 0, b1, Flag.Seen));
        ListKey[] want = [new("t_a"), new("t_b", "b1")];
        Assert.Equal(want, Keys(m));
        var row0 = m.Rows[0];
        Assert.True(row0.Thread);
        Assert.Equal("a2", row0.Message.Id.Value);
        Assert.Equal(2, row0.Summary?.MessageCount);
        Assert.False(row0.Expanded);
        Assert.False(row0.Loading);
        var row1 = m.Rows[1];
        Assert.False(row1.Thread);
        Assert.False(row1.Member);
        Assert.Equal("b1", row1.Message.Id.Value);

        // Unfolded before the members are known: the row spins, no members.
        m.SetExpanded("t_a", true);
        Assert.Equal(want, Keys(m));
        Assert.True(m.Rows[0].Loading);
        Assert.True(m.Rows[0].Expanded);
        var a1 = Member("a1", "t_a", 1, "alice");
        m.SetMembers("t_a", Thr("t_a", 2, 1, a2), [a1, a2]);
        want = [new("t_a"), new("t_a", "a1"), new("t_a", "a2"), new("t_b", "b1")];
        Assert.Equal(want, Keys(m));
        Assert.False(m.Rows[0].Loading);
        Assert.True(m.Rows[1].Member);
        var found = Assert.NotNull(m.Message("a1"));
        Assert.Equal("a1", found.Summary.Id.Value);
        Assert.Equal(1, found.Index);
        Assert.Equal(3, m.RowIndexOf(new ListKey("t_b", "b1")));
        Assert.Equal(-1, m.RowIndexOf(new ListKey(Message: "zz")));
        Assert.Equal(4, m.RowCount);
        m.SetExpanded("t_a", false);
        Assert.Equal(2, m.Rows.Count);
        Assert.False(m.Rows[0].Expanded);
        // A folded member is still known, just without a row.
        var folded = Assert.NotNull(m.Message("a1"));
        Assert.Equal(-1, folded.Index);
        Assert.Equal(new ListKey("t_a", "a1"), m.KeyFor("a1"));
    }

    [Fact]
    public void SetThreadsKeepsCache()
    {
        var a2 = Member("a2", "t_a", 2, "bob");
        var a1 = Member("a1", "t_a", 1, "alice");
        var m = GroupedModel(Thr("t_a", 2, 1, a2));
        m.SetExpanded("t_a", true);
        m.SetMembers("t_a", Thr("t_a", 2, 1, a2), [a1, a2]);

        // Same shape: the members survive the reload, the row stays open.
        m.SetThreads([Thr("t_a", 2, 1, a2)], new PageInfo { Total = 1 });
        Assert.True(m.Members["t_a"].Complete);
        Assert.Equal(3, m.Rows.Count);
        Assert.False(m.Rows[0].Loading);
        // A reply arrived meanwhile: the members are asked for again.
        var a3 = Member("a3", "t_a", 3, "carol");
        m.SetThreads([Thr("t_a", 3, 2, a3)], new PageInfo { Total = 1 });
        Assert.False(m.Members["t_a"].Complete);
        Assert.Single(m.Rows);
        Assert.True(m.Rows[0].Loading);
        // Duplicates are ignored.
        m.SetThreads([Thr("t_a", 3, 2, a3), Thr("t_a", 3, 2, a3)], new PageInfo { Total = 1 });
        Assert.True(m.Threads.Count == 1, "duplicate thread listed");
        var added = m.AppendThreads([Thr("t_a", 3, 2, a3), Thr("t_b", 1, 0, Member("b1", "t_b", 0, "dave"))], new PageInfo { Total = 0 });
        Assert.Equal(1, added);
        Assert.Equal(2, m.Rows.Count);
    }

    /// <summary>
    /// A Jira conversation whose issue moved without a new member (the
    /// account shows no events): the reload drops the members it had, so the
    /// row shows the issue as the listing has it now.
    /// </summary>
    [Fact]
    public void SetThreadsIssueMoved()
    {
        static ThreadSummary Listed(string status, string snippet)
        {
            // Fresh lists every time, as every listing decodes its own.
            var s = Member("m1", "t_m", 1, "petr") with
            {
                Snippet = snippet,
                Issue = new MessageIssue
                {
                    Key = "MOB-1",
                    Url = "",
                    Summary = "",
                    Status = status,
                    CommentVisibilities = [CommentVisibility.Public],
                    Item = IssueItemKind.Description,
                },
            };
            return Thr("t_m", 1, 0, s) with { Issue = s.Issue.Info };
        }
        var m = GroupedModel(Listed("In Progress", "before"));
        // The same issue listed again: the members stay (the row keeps the
        // member it had, not the listing's).
        m.SetThreads([Listed("In Progress", "again")], new PageInfo { Total = 1 });
        Assert.Equal("before", m.Rows[0].Message.Snippet);
        m.SetThreads([Listed("To Do", "after")], new PageInfo { Total = 1 });
        Assert.Equal("To Do", m.Rows[0].Message.Issue?.Status);
        Assert.Equal("after", m.Rows[0].Message.Snippet);
    }

    [Fact]
    public void SetMembersEmptyDropsThread()
    {
        var a2 = Member("a2", "t_a", 2, "bob");
        var m = GroupedModel(Thr("t_a", 2, 0, a2), Thr("t_b", 1, 0, Member("b1", "t_b", 1, "carol")));
        m.SetMembers("t_a", Thr("t_a", 2, 0, a2), []);
        Assert.Single(m.Threads);
        Assert.Equal("t_b", m.Threads[0].Id.Value);
        Assert.Equal(1, m.Total);
        Assert.Single(m.Rows);
    }

    [Fact]
    public void ApplyNewMessageExistingThread()
    {
        var a2 = Member("a2", "t_a", 2, "bob");
        var b1 = Member("b1", "t_b", 5, "carol");
        var m = GroupedModel(Thr("t_b", 1, 0, b1, Flag.Seen), Thr("t_a", 2, 1, a2));
        m.SetExpanded("t_a", true);
        m.SetMembers("t_a", Thr("t_a", 2, 1, a2), [Member("a1", "t_a", 1, "alice"), a2]);

        // A known address, capitalised: no new participant.
        var a3 = Member("a3", "t_a", 9, "Alice") with { HasAttachments = true };
        Assert.True(m.ApplyNewMessage(a3, MessageFilter.All, new ListKey()), "rejected");
        Assert.True(m.Threads[0].Id.Value == "t_a", "thread not moved to the top");
        var th = m.Threads[0];
        Assert.Equal(3, th.MessageCount);
        Assert.Equal(2, th.UnreadCount);
        Assert.Equal(a3.Date, th.LatestDate);
        Assert.Equal("a3", th.Latest.Id.Value);
        Assert.Equal("p-a3", th.Snippet);
        Assert.True(th.HasAttachments);
        Assert.Equal(2, th.Participants.Count);
        Assert.Equal("Alice", th.Participants[0].Name);
        Assert.Equal("bob", th.Participants[^1].Name);
        var mem = m.Members["t_a"];
        Assert.Equal(3, mem.List.Count);
        Assert.Equal("a3", mem.List[2].Id.Value);
        Assert.True(mem.Complete);
        ListKey[] want = [new("t_a"), new("t_a", "a1"), new("t_a", "a2"), new("t_a", "a3"), new("t_b", "b1")];
        Assert.Equal(want, Keys(m));
        // Delivered twice: nothing changes.
        Assert.True(m.ApplyNewMessage(a3, MessageFilter.All, new ListKey()));
        Assert.True(m.Threads[0].MessageCount == 3, "duplicate counted");

        // A reply to the selected single-message row unfolds it, so the row
        // the user reads stays.
        var b2 = Member("b2", "t_b", 10, "dave");
        Assert.True(m.ApplyNewMessage(b2, MessageFilter.All, new ListKey("t_b", "b1")), "rejected");
        Assert.Contains(new ThreadId("t_b"), m.Expanded);
        Assert.Equal("t_b", m.Threads[0].Id.Value);
        Assert.True(m.RowIndexOf(new ListKey("t_b", "b1")) == 1, "selected singleton not unfolded");
    }

    [Fact]
    public void ApplyNewMessageNewThread()
    {
        var m = GroupedModel(Thr("t_a", 1, 0, Member("a1", "t_a", 1, "alice", Flag.Seen), Flag.Seen));
        var c1 = Member("c1", "t_c", 3, "carol");
        Assert.True(m.ApplyNewMessage(c1, MessageFilter.All, new ListKey()));
        Assert.Equal("t_c", m.Threads[0].Id.Value);
        Assert.Equal(2, m.Total);
        var th = m.Threads[0];
        Assert.Equal(1, th.MessageCount);
        Assert.Equal(1, th.UnreadCount);
        Assert.Equal("c1", th.Latest.Id.Value);
        Assert.Single(th.Participants);
        Assert.True(m.Members["t_c"].Complete);
        // Under the unread filter a read arrival is not listed.
        var d1 = Member("d1", "t_d", 4, "dave", Flag.Seen);
        Assert.True(m.ApplyNewMessage(d1, MessageFilter.Unread, new ListKey()));
        Assert.True(m.Threads.Count == 2, "seen message listed under unread");
        // No thread id: the caller reloads.
        var e1 = Member("e1", null, 5, "eve");
        Assert.False(m.ApplyNewMessage(e1, MessageFilter.All, new ListKey()), "accepted a message without thread id");
    }

    [Fact]
    public void ApplyFlagsAggregates()
    {
        var a2 = Member("a2", "t_a", 2, "bob");
        var m = GroupedModel(Thr("t_a", 2, 2, a2));
        // Members not known: the change lands on the known one and the counts follow.
        var changed = m.ApplyFlags(["a2"], set: [Flag.Seen]);
        Assert.Single(changed);
        Assert.Equal(1, m.Threads[0].UnreadCount);
        Assert.True(FolderTree.HasFlag(m.Threads[0].Latest.Flags, Flag.Seen));
        Assert.True(FolderTree.HasFlag(m.Threads[0].Flags, Flag.Seen));
        Assert.True(m.ApplyFlags(["a2"], set: [Flag.Seen]).Count == 0, "unchanged flag reported");
        // Members known: the union is exact.
        var a1 = Member("a1", "t_a", 1, "alice", Flag.Flagged);
        m.SetExpanded("t_a", true);
        m.SetMembers("t_a", Thr("t_a", 2, 1, m.Threads[0].Latest, Flag.Flagged, Flag.Seen), [a1, m.Threads[0].Latest]);
        changed = m.ApplyFlags(["a1", "a2"], clear: [Flag.Flagged, Flag.Seen]);
        Assert.Equal(2, changed.Count);
        var th = m.Threads[0];
        Assert.Equal(2, th.UnreadCount);
        Assert.False(FolderTree.HasFlag(th.Flags, Flag.Flagged));
        Assert.False(FolderTree.HasFlag(th.Flags, Flag.Seen));
        Assert.True(MailModel.FlagTarget(m.Rows[0]), "flagTarget of an unflagged conversation should flag");
        m.ApplyFlags(["a1"], set: [Flag.Flagged]);
        Assert.False(MailModel.FlagTarget(m.Rows[0]), "flagTarget of a flagged conversation should unflag");
        Assert.True(FolderTree.HasFlag(m.Threads[0].Flags, Flag.Flagged));
        Assert.Equal(["a1", "a2"], m.RowIds(m.Rows[0])!.Select(id => id.Value));
        Assert.Equal(["a1"], m.RowIds(m.Rows[1])!.Select(id => id.Value));
    }

    [Fact]
    public void RemoveMessagesAndRestore()
    {
        var a2 = Member("a2", "t_a", 2, "bob");
        var a1 = Member("a1", "t_a", 1, "alice");
        var b1 = Member("b1", "t_b", 0, "carol");
        var m = GroupedModel(Thr("t_a", 2, 2, a2), Thr("t_b", 1, 1, b1));
        // Not all known: the caller has to reload.
        Assert.True(m.RemoveMessages(["a2"]) is null, "removed from an incomplete conversation");
        m.SetExpanded("t_a", true);
        m.SetMembers("t_a", Thr("t_a", 2, 2, a2), [a1, a2]);

        // One member goes: the conversation shrinks to a single row.
        var r = m.RemoveMessages(["a2"]);
        Assert.NotNull(r);
        Assert.Equal(2, m.Threads.Count);
        Assert.Equal(1, m.Threads[0].MessageCount);
        Assert.Equal("a1", m.Threads[0].Latest.Id.Value);
        Assert.Equal([new ListKey("t_a", "a1"), new ListKey("t_b", "b1")], Keys(m));
        m.RestoreRemoval(r);
        Assert.Equal(2, m.Threads[0].MessageCount);
        Assert.Equal(4, m.Rows.Count);
        Assert.Contains(new ThreadId("t_a"), m.Expanded);

        // The whole conversation goes, and comes back at its place.
        r = m.RemoveMessages(["a1", "a2"]);
        Assert.NotNull(r);
        Assert.Single(m.Threads);
        Assert.Equal("t_b", m.Threads[0].Id.Value);
        Assert.Equal(1, m.Total);
        Assert.Equal(-1, m.RowIndexOf(new ListKey("t_a")));
        m.RestoreRemoval(r);
        Assert.Equal(2, m.Threads.Count);
        Assert.Equal("t_a", m.Threads[0].Id.Value);
        Assert.Equal(2, m.Total);
        Assert.Equal(new ListKey("t_a"), m.Rows[0].Key);
        Assert.True(m.Members["t_a"].Complete);
        Assert.True(m.MemberOf.ContainsKey("a2"), "member index not restored");
    }

    [Fact]
    public void CollapseLoadingTest()
    {
        var m = GroupedModel(Thr("t_a", 2, 0, Member("a2", "t_a", 2, "bob")), Thr("t_b", 3, 0, Member("b3", "t_b", 3, "carol")));
        m.SetExpanded("t_a", true);
        m.SetExpanded("t_b", true);
        m.Members["t_a"] = m.Members["t_a"] with { Fetching = true };
        m.SetMembers(
            "t_b",
            Thr("t_b", 3, 0, Member("b3", "t_b", 3, "carol")),
            [Member("b1", "t_b", 1, "x"), Member("b2", "t_b", 2, "y"), Member("b3", "t_b", 3, "carol")]);
        m.CollapseLoading();
        Assert.DoesNotContain(new ThreadId("t_a"), m.Expanded);
        Assert.Contains(new ThreadId("t_b"), m.Expanded);
        Assert.False(m.Members["t_a"].Fetching);
        Assert.Equal(5, m.Rows.Count);
    }

    [Fact]
    public void FlatModeAccessors()
    {
        var m = new MailModel();
        m.SetMessages([Summary("a"), Summary("b")], new PageInfo { Total = 2 });
        Assert.Equal(2, m.RowCount);
        Assert.Equal(1, m.RowIndexOf(new ListKey(Message: "b")));
        Assert.Equal(new ListKey(Message: "a"), m.KeyFor("a"));
        var r = m.RowAt(1);
        Assert.NotNull(r);
        Assert.False(r.Thread);
        Assert.Equal("b", r.Message.Id.Value);
        Assert.Equal(new ListKey(Message: "b"), r.Key);
        Assert.Equal(["b"], m.RowIds(r)!.Select(id => id.Value));
        var changed = m.ApplyFlags(["a", "zz"], set: [Flag.Seen]);
        Assert.Equal(["a"], changed.Select(id => id.Value));
        Assert.True(FolderTree.HasFlag(m.Messages[0].Flags, Flag.Seen));
        var o = new OutboxInfo { State = OutboxState.Queued, Attempts = 0 };
        m.SetOutbox("b", o);
        Assert.Equal(o, m.Messages[1].Outbox);
        m.ClearMessages();
        Assert.Equal(0, m.RowCount);
        Assert.Empty(m.Rows);
    }

    [Fact]
    public void SummaryThreadTest()
    {
        var a2 = Member("a2", "t_a", 2, "bob") with { HasAttachments = true };
        var th = Thr("t_a", 3, 1, a2, Flag.Flagged);
        var got = MailModel.SummaryThread(th, expanded: true, loading: false);
        Assert.Equal(3, got.Count);
        Assert.Equal(1, got.Unread);
        Assert.True(got.Flagged);
        Assert.True(got.HasAttachments);
        Assert.True(got.Expanded);
        Assert.False(got.Loading);
        Assert.Equal("s-a2", got.Subject);
        Assert.Equal("p-a2", got.Snippet);
        Assert.Equal(a2.Date, got.Date);
        Assert.Single(got.Participants);
    }

    // Windows-only: Swift's ThreadSnapshot is a deep copy for free; here the
    // undo must restore the members as they were even after the model went
    // on changing them (a flag change after the removal).
    [Fact]
    public void RemovedMembersSnapshotIsIndependent()
    {
        var a2 = Member("a2", "t_a", 2, "bob");
        var a1 = Member("a1", "t_a", 1, "alice");
        var a3 = Member("a3", "t_a", 3, "carol");
        var m = GroupedModel(Thr("t_a", 3, 3, a3));
        m.SetMembers("t_a", Thr("t_a", 3, 3, a3), [a1, a2, a3]);
        var r = m.RemoveMessages(["a3"]);
        Assert.NotNull(r);
        m.ApplyFlags(["a1"], set: [Flag.Seen]);
        Assert.Empty(r.Threads[0].Members.List[0].Flags);
        m.RestoreRemoval(r);
        Assert.Equal(["a1", "a2", "a3"], m.Members["t_a"].List.Select(s => s.Id.Value));
        Assert.Empty(m.Members["t_a"].List[0].Flags);
        Assert.Equal(3, m.Threads[0].UnreadCount);
    }
}
