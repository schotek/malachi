// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/MailModelTests.swift, the counterpart
// of ui/internal/window/model_test.go (every test; the Swift suite ports all
// of them). The helpers below are shared by the other model suites, as
// their Go and Swift originals are. Swift's errTest is an RPCError, which is
// an Error there; the C# RpcError is a record, not an exception, so the
// model's ListErr gets an ordinary exception. CloneIsASnapshot is
// Windows-only: MailModel is a class here, and Clone stands in for the copy
// a Swift assignment makes.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Xunit;
using FolderMap = System.Collections.Generic.Dictionary<Malachi.Core.Api.AccountId, System.Collections.Generic.IReadOnlyList<Malachi.Core.Api.Folder>>;

namespace Malachi.Core.Tests.Model;

public sealed class MailModelTests
{
    /// <summary>The error of the tests (model_test.go <c>errTest</c>).</summary>
    internal static readonly Exception ErrTest = new InvalidOperationException("test");

    /// <summary>A list summary with the given id and flags (model_test.go <c>summary</c>).</summary>
    internal static MessageSummary Summary(string id, params Flag[] flags) => new()
    {
        Id = id,
        AccountId = "",
        FolderId = "",
        From = [],
        Subject = "s-" + id,
        Date = DateTimeOffset.GoZero,
        Snippet = "",
        Flags = flags,
        HasAttachments = false,
        Size = 0,
    };

    /// <summary>A folder of the tests; the name defaults to the id.</summary>
    internal static Folder TestFolder(
        string id,
        string path,
        FolderRole? role = null,
        string? parent = null,
        string? name = null,
        bool selectable = true,
        bool synced = true,
        int unread = 0,
        int total = 0) => new()
        {
            Id = id,
            AccountId = "",
            ParentId = parent is null ? null : (FolderId?)new FolderId(parent),
            Name = name ?? id,
            Path = path,
            Role = role ?? FolderRole.None,
            Subscribed = true,
            Selectable = selectable,
            Synced = synced,
            Unread = unread,
            Total = total,
        };

    /// <summary>An account of the tests.</summary>
    internal static Account TestAccount(
        string id,
        bool enabled = true,
        string name = "",
        string email = "",
        string? displayName = null,
        SyncState? state = null) => new()
        {
            Id = id,
            Config = new AccountConfig { Name = name, Email = email, DisplayName = displayName },
            Enabled = enabled,
            State = state ?? new SyncState { AccountId = id, Status = SyncStatus.Idle },
        };

    /// <summary>
    /// Three accounts, one disabled, with a nested tree on the first
    /// (model_test.go <c>testAccounts</c>).
    /// </summary>
    internal static (Account[] Accounts, FolderMap Folders) TestAccounts()
    {
        Account[] accounts =
        [
            TestAccount("acc1", email: "one@example.invalid"),
            TestAccount("acc2", email: "two@example.invalid"),
            TestAccount("acc3", enabled: false, email: "off@example.invalid"),
        ];
        var folders = new FolderMap
        {
            ["acc1"] =
            [
                TestFolder("zeta", "zeta"),
                TestFolder("trash", "Trash", FolderRole.Trash, name: "Trash"),
                TestFolder("inbox", "INBOX", FolderRole.Inbox, name: "INBOX", unread: 2),
                TestFolder("alpha", "Alpha", name: "Alpha"),
                TestFolder("sub2", "Alpha/b", parent: "alpha", name: "b"),
                TestFolder("sub1", "Alpha/A", parent: "alpha", name: "A"),
                TestFolder("deep", "Alpha/A/deep", parent: "sub1"),
                TestFolder("container", "Container", name: "Container", selectable: false),
                TestFolder("leaf", "Container/leaf", parent: "container"),
            ],
            ["acc2"] = [TestFolder("in2", "INBOX", FolderRole.Inbox, name: "INBOX")],
            ["acc3"] = [TestFolder("in3", "INBOX", FolderRole.Inbox, name: "INBOX")],
        };
        return (accounts, folders);
    }

    /// <summary>
    /// <paramref name="list"/> with element <paramref name="i"/> changed: the
    /// counterpart of Swift's in-place <c>list[i].x = …</c> on a value.
    /// </summary>
    internal static T[] Replace<T>(IReadOnlyList<T> list, int i, Func<T, T> change)
    {
        T[] copy = [.. list];
        copy[i] = change(copy[i]);
        return copy;
    }

    [Fact]
    public void HasFlagTest()
    {
        Flag[] flags = [Flag.Seen, Flag.Flagged];
        Assert.True(FolderTree.HasFlag(flags, Flag.Flagged));
        Assert.False(FolderTree.HasFlag(flags, Flag.Junk));
        Assert.False(FolderTree.HasFlag([], Flag.Seen));
    }

    [Fact]
    public void MatchesFilterTest()
    {
        var seen = Summary("a", Flag.Seen);
        var seenFlagged = Summary("b", Flag.Seen, Flag.Flagged);
        var unread = Summary("c");
        var unreadFlagged = Summary("d", Flag.Flagged);
        (MessageFilter Filter, Dictionary<string, bool> Want)[] cases =
        [
            (new MessageFilter(""), new() { ["a"] = true, ["b"] = true, ["c"] = true, ["d"] = true }),
            (MessageFilter.All, new() { ["a"] = true, ["b"] = true, ["c"] = true, ["d"] = true }),
            (MessageFilter.Unread, new() { ["a"] = false, ["b"] = false, ["c"] = true, ["d"] = true }),
            (MessageFilter.Flagged, new() { ["a"] = false, ["b"] = true, ["c"] = false, ["d"] = true }),
        ];
        foreach (var (filter, want) in cases)
        {
            foreach (var s in new[] { seen, seenFlagged, unread, unreadFlagged })
            {
                Assert.True(FolderTree.MatchesFilter(s, filter) == want[s.Id.Value], $"matchesFilter({s.Id}, {filter})");
            }
        }
    }

    [Fact]
    public void SummaryMessageTest()
    {
        var date = DateTimeOffset.FromUnixTimeSeconds(1_788_429_600); // 2026-09-03T10:00:00Z
        var s = new MessageSummary
        {
            Id = "m",
            AccountId = "",
            FolderId = "",
            From = [new Address { Name = "Alice", Email = "alice@example.invalid" }],
            Subject = "Hi",
            Date = date,
            Snippet = "snip",
            Flags = [Flag.Flagged],
            HasAttachments = true,
            Size = 0,
        };
        var m = MailModel.SummaryMessage(s);
        Assert.Single(m.From);
        Assert.Equal("Alice", m.From[0].Name);
        Assert.Equal("Hi", m.Subject);
        Assert.Equal("snip", m.Snippet);
        Assert.Equal(date, m.Date);
        Assert.True(m.Unread);
        Assert.True(m.Flagged);
        Assert.True(m.HasAttachments);
        s = s with { Flags = [Flag.Seen], From = [] };
        m = MailModel.SummaryMessage(s);
        Assert.False(m.Unread);
        Assert.False(m.Flagged);
        Assert.Empty(m.From);
    }

    [Fact]
    public void SetAppendMessages()
    {
        var m = new MailModel();
        m.SetMessages([Summary("a"), Summary("b"), Summary("a")], new PageInfo { NextCursor = "c1", Total = 10 });
        Assert.Equal(2, m.Messages.Count);
        Assert.Equal(0, m.Index["a"]);
        Assert.Equal(1, m.Index["b"]);
        Assert.Equal("c1", m.NextCursor);
        Assert.Equal(10, m.Total);
        var added = m.AppendMessages([Summary("b"), Summary("c")], new PageInfo { Total = 10 });
        Assert.Equal(1, added);
        Assert.Equal(3, m.Messages.Count);
        Assert.Equal(2, m.Index["c"]);
        Assert.Null(m.NextCursor);
        var c = Assert.NotNull(m.Message("c"));
        Assert.Equal(2, c.Index);
        Assert.Equal("c", c.Summary.Id.Value);
        Assert.Null(m.MessageAt(3));
        Assert.Equal("b", m.MessageAt(1)?.Id.Value);
        // Replacing the page resets the index and error.
        m.ListErr = ErrTest;
        m.SetMessages([], new PageInfo { Total = -1 });
        Assert.Empty(m.Messages);
        Assert.Empty(m.Index);
        Assert.Null(m.ListErr);
        Assert.Equal(-1, m.Total);
    }

    [Fact]
    public void InsertRemoveMessage()
    {
        var m = new MailModel();
        Assert.True(m.InsertMessage(5, Summary("a")));
        Assert.Single(m.Messages);
        Assert.Equal(0, m.Index["a"]);
        m.Total = 1;
        Assert.True(m.InsertMessage(0, Summary("b")));
        Assert.Equal("b", m.Messages[0].Id.Value);
        Assert.Equal(1, m.Index["a"]);
        Assert.Equal(2, m.Total);
        Assert.False(m.InsertMessage(0, Summary("a")), "duplicate insert accepted");
        m.InsertMessage(-1, Summary("c"));
        Assert.Equal("c", m.Messages[0].Id.Value);
        Assert.Equal(1, m.Index["b"]);
        Assert.Equal(2, m.Index["a"]);

        var removed = Assert.NotNull(m.RemoveMessage("b"));
        Assert.Equal(1, removed.Index);
        Assert.Equal("b", removed.Summary.Id.Value);
        Assert.Equal(2, m.Messages.Count);
        Assert.Equal(1, m.Index["a"]);
        Assert.Equal(2, m.Total);
        Assert.False(m.Index.ContainsKey("b"), "removed id still indexed");
        Assert.True(m.RemoveMessage("b") is null, "second remove succeeded");
        m.Total = -1;
        m.RemoveMessage("a");
        Assert.True(m.Total == -1, "unknown total was decremented");
    }

    [Fact]
    public void UpdateFlagsTest()
    {
        var m = new MailModel();
        m.SetMessages([Summary("a", Flag.Seen)], new PageInfo { Total = 0 });
        Assert.False(m.UpdateFlags("a", set: [Flag.Seen]), "no-op reported a change");
        Assert.True(m.UpdateFlags("a", set: [Flag.Flagged], clear: [Flag.Seen]), "change not reported");
        var s = Assert.NotNull(m.Message("a")).Summary;
        Assert.False(FolderTree.HasFlag(s.Flags, Flag.Seen));
        Assert.True(FolderTree.HasFlag(s.Flags, Flag.Flagged));
        Assert.Single(s.Flags);
        Assert.False(m.UpdateFlags("zz", set: [Flag.Seen]), "unknown id reported a change");
    }

    [Fact]
    public void SortFoldersTest()
    {
        var (accounts, folders) = TestAccounts();
        var entries = FolderTree.SortFolders(accounts, folders, new CollapseState(), new FavouriteState());

        (string? Id, bool Header, int Depth)[] want =
        [
            (null, true, 0),
            ("inbox", false, 0), ("trash", false, 0),
            ("alpha", false, 0), ("sub1", false, 1), ("deep", false, 2), ("sub2", false, 1),
            ("container", false, 0), ("leaf", false, 1),
            ("zeta", false, 0),
            (null, true, 0),
            ("in2", false, 0),
        ];
        Assert.Equal(want, entries.Select(e => (e.Folder?.Id.Value, e.Header, e.Depth)));
        Assert.Equal("acc1", entries[0].Account?.Id.Value);
        Assert.Equal("acc2", entries[10].Account?.Id.Value);

        // A single enabled account gets no header row.
        var single = FolderTree.SortFolders([accounts[0]], folders, new CollapseState(), new FavouriteState());
        Assert.Equal(9, single.Count);
        Assert.False(single[0].Header);
        Assert.Empty(FolderTree.SortFolders([], new FolderMap(), new CollapseState(), new FavouriteState()));
    }

    [Fact]
    public void SortFoldersCycleGuard()
    {
        Account[] accounts = [TestAccount("a")];
        var folders = new FolderMap
        {
            ["a"] =
            [
                TestFolder("x", "p/x", parent: "y"),
                TestFolder("y", "p/q/y", parent: "x"),
                TestFolder("self", "self", parent: "self"),
                TestFolder("orphan", "gone/o", parent: "missing", name: "o"),
            ],
        };
        var entries = FolderTree.SortFolders(accounts, folders, new CollapseState(), new FavouriteState());
        Assert.Equal(4, entries.Count);
        var depths = new Dictionary<string, int>();
        foreach (var e in entries)
        {
            if (e.Folder is { } f)
            {
                depths[f.Id.Value] = e.Depth;
            }
        }
        // Self-parent and missing parent are roots; the cycle falls back to
        // the path depth.
        Assert.Equal(0, depths["self"]);
        Assert.Equal(0, depths["orphan"]);
        Assert.Equal(1, depths["x"]);
        Assert.Equal(2, depths["y"]);
        Assert.Equal("orphan", entries[0].Folder?.Id.Value);
        Assert.Equal("self", entries[1].Folder?.Id.Value);
    }

    [Fact]
    public void RoleRankAndIcon()
    {
        FolderRole[] roles =
        [
            FolderRole.Inbox, FolderRole.Drafts, FolderRole.Sent, FolderRole.Archive,
            FolderRole.Junk, FolderRole.Trash, FolderRole.Outbox, FolderRole.All,
        ];
        for (var i = 1; i < roles.Length; i++)
        {
            Assert.True(FolderTree.RoleRank(roles[i - 1]) < FolderTree.RoleRank(roles[i]), $"{roles[i - 1]} should sort before {roles[i]}");
        }
        Assert.True(FolderTree.RoleRank(FolderRole.None) > FolderTree.RoleRank(FolderRole.All));
        Assert.Equal(FolderTree.RoleRank(FolderRole.None), FolderTree.RoleRank(new FolderRole("bogus")));
        var icons = new Dictionary<FolderRole, string>
        {
            [FolderRole.Inbox] = "mail-unread-symbolic",
            [FolderRole.Drafts] = "document-edit-symbolic",
            [FolderRole.Sent] = "mail-send-symbolic",
            [FolderRole.Trash] = "user-trash-symbolic",
            [FolderRole.Junk] = "mail-mark-junk-symbolic",
            [FolderRole.Archive] = "folder-download-symbolic",
            [FolderRole.Outbox] = "mail-send-symbolic",
            [FolderRole.All] = "folder-symbolic",
            [FolderRole.None] = "folder-symbolic",
            ["bogus"] = "folder-symbolic",
        };
        foreach (var (role, want) in icons)
        {
            Assert.True(FolderTree.RoleIcon(role) == want, $"roleIcon({role})");
        }
    }

    [Fact]
    public void ModelFolders()
    {
        var (accounts, folders) = TestAccounts();
        var m = new MailModel(accounts, folders);
        m.RebuildEntries();

        var k = Assert.NotNull(m.InitialFolder());
        Assert.Equal(new FolderKey("acc1", "inbox"), k);
        Assert.Equal(2, m.Folder(k)?.Unread);
        Assert.Null(m.Folder(new FolderKey("acc1", "nope")));
        Assert.Equal("trash", m.FolderByRole("acc1", FolderRole.Trash)?.Id.Value);
        Assert.Null(m.FolderByRole("acc1", FolderRole.Archive));
        Assert.Equal("two@example.invalid", m.Account("acc2")?.Config.Email);
        Assert.Null(m.Account("acc9"));
        var enabled = m.EnabledAccounts;
        Assert.Equal(2, enabled.Count);
        Assert.Equal("acc1", enabled[0].Id.Value);
        Assert.Equal("acc2", enabled[^1].Id.Value);

        m.AdjustCounts(k, -5, 0);
        Assert.True(m.Folder(k)?.Unread == 0, "floor");
        m.AdjustCounts(k, 3, 0);
        Assert.Equal(3, m.Folder(k)?.Unread);
        foreach (var e in m.Entries.Where(e => !e.Header && e.Folder?.Id.Value == "inbox"))
        {
            Assert.True(e.Folder?.Unread == 3, "entry not updated");
        }
        m.AdjustCounts(new FolderKey("acc1", "nope"), 1, 1); // no crash
    }

    [Fact]
    public void AdjustCountsTest()
    {
        var m = CountsModel();
        var inbox = new FolderKey("a", "in");
        m.AdjustCounts(inbox, 1, 1);
        var c = Counts(m, "in");
        Assert.True(c is (3, 11, true), $"up: {c}");
        // Both counts stop at zero, each on its own.
        m.AdjustCounts(inbox, -5, -1);
        c = Counts(m, "in");
        Assert.True(c is (0, 10, true), $"unread floor: {c}");
        m.AdjustCounts(inbox, 0, -50);
        c = Counts(m, "in");
        Assert.True(c is (0, 0, true), $"total floor: {c}");
    }

    [Fact]
    public void MoveCountsTest()
    {
        var src = new FolderKey("a", "in");
        var trash = new FolderKey("a", "trash");
        (string Name, FolderKey From, FolderKey? Target, int Unread, int N, int SrcU, int SrcN, string DstId, int DstU, int DstN)[] cases =
        [
            ("to trash", src, trash, 1, 3, 1, 7, "trash", 1, 4),
            // Read messages still move the totals.
            ("read only", src, trash, 0, 2, 2, 8, "trash", 0, 3),
            // Leaving the store: only the source changes.
            ("expunged", src, null, 1, 1, 1, 9, "trash", 0, 1),
            // All Mail is never downloaded: it counts nothing, before or after.
            ("to an unsynced folder", src, new FolderKey("a", "all"), 2, 2, 0, 8, "all", 0, 0),
            // The outbox keeps its total (and so its row) until it is reloaded.
            ("from the outbox", new FolderKey("a", "out"), null, 0, 1, 0, 1, "trash", 0, 1),
        ];
        var fresh = CountsModel();
        foreach (var (name, from, target, unread, n, srcU, srcN, dstId, dstU, dstN) in cases)
        {
            var m = CountsModel();
            m.MoveCounts(from, target, unread, n);
            var s = Counts(m, from.Folder.Value);
            Assert.True(s.Unread == srcU && s.Total == srcN && s.EntryAgrees, $"{name}: source {s}, want {srcU}/{srcN}");
            var d = Counts(m, dstId);
            Assert.True(d.Unread == dstU && d.Total == dstN && d.EntryAgrees, $"{name}: {dstId} {d}, want {dstU}/{dstN}");
            // The undo of a failed move puts everything back.
            m.MoveCounts(from, target, -unread, -n);
            foreach (var id in new[] { "in", "trash", "out", "all" })
            {
                var got = Counts(m, id);
                var want = Counts(fresh, id);
                Assert.True(got.Unread == want.Unread && got.Total == want.Total, $"{name}: undo left {id} at {got}, want {want}");
            }
        }
        // The outbox row stays listed while the move is pending.
        var model = CountsModel();
        model.MoveCounts(new FolderKey("a", "out"), null, 0, 1);
        Assert.True(model.FolderListed(new FolderKey("a", "out")), "the outbox row went with its last message");
    }

    [Fact]
    public void OutboxKeyTest()
    {
        var m = CountsModel();
        Assert.True(m.OutboxKey("a") == new FolderKey("a", "out"), "enabled");
        // A paused account's folders are not shown (nor, normally, loaded).
        Assert.True(m.OutboxKey("p") is null, "paused");
        Assert.True(m.OutboxKey("zzz") is null, "unknown account");
        m.Folders["a"] = [.. m.Folders["a"].Take(2)];
        Assert.True(m.OutboxKey("a") is null, "no outbox");
    }

    [Fact]
    public void VisibleFoldersTest()
    {
        Folder[] list =
        [
            TestFolder("in", "INBOX", FolderRole.Inbox),
            TestFolder("trash", "Trash", FolderRole.Trash),
            TestFolder("out", "Outbox", FolderRole.Outbox),
        ];
        // An empty outbox is hidden; an empty Trash (or Inbox) is not.
        var got = FolderTree.VisibleFolders(list);
        Assert.Equal(["in", "trash"], got.Select(f => f.Id.Value));
        list[2] = list[2] with { Total = 1 };
        got = FolderTree.VisibleFolders(list);
        Assert.Equal(["in", "trash", "out"], got.Select(f => f.Id.Value));
        Assert.Empty(FolderTree.VisibleFolders([]));

        // The sidebar entries follow, while the model still knows the folder.
        var m = new MailModel(
            [TestAccount("a")],
            new FolderMap { ["a"] = [list[0], list[1], TestFolder("out", "Outbox", FolderRole.Outbox)] });
        m.RebuildEntries();
        Assert.Equal(["in@0", "trash@0"], FolderTreeTests.Ids(m.Entries));
        Assert.True(m.FolderByRole("a", FolderRole.Outbox)?.Id.Value == "out", "hidden outbox not found by role");
        Assert.Equal(FolderRole.Outbox, m.FolderRole(new FolderKey("a", "out")).Value);
        Assert.Equal(FolderRole.None, m.FolderRole(new FolderKey("a", "nope")).Value);
        m.Folders["a"] = Replace(m.Folders["a"], 2, f => f with { Total = 2 });
        m.RebuildEntries();
        Assert.Equal(["in@0", "trash@0", "out@0"], FolderTreeTests.Ids(m.Entries));
    }

    [Fact]
    public void InitialFolderFallbacks()
    {
        var m = new MailModel(
            [TestAccount("a")],
            new FolderMap { ["a"] = [TestFolder("c", "c", selectable: false), TestFolder("b", "b")] });
        m.RebuildEntries();
        Assert.True(m.InitialFolder()?.Folder.Value == "b", "first selectable");
        m.Folders["a"] = Replace(m.Folders["a"], 1, f => f with { Selectable = false });
        m.RebuildEntries();
        Assert.True(m.InitialFolder() is null, "nothing selectable but a folder was returned");
        Assert.True(new MailModel().InitialFolder() is null, "empty model returned a folder");
    }

    [Fact]
    public void Generations()
    {
        var m = new MailModel();
        Assert.Equal(1UL, m.BumpList());
        Assert.Equal(2UL, m.BumpList());
        Assert.Equal(1UL, m.BumpBody());
        Assert.Equal(1UL, m.BumpFolders());
        Assert.Equal(2UL, m.ListGen);
        m.Loading = true;
        m.LoadingMore = true;
        m.BumpAll();
        Assert.Equal(3UL, m.ListGen);
        Assert.Equal(2UL, m.BodyGen);
        Assert.Equal(2UL, m.FoldersGen);
        Assert.False(m.Loading);
        Assert.False(m.LoadingMore);
    }

    [Fact]
    public void ClearMessagesTest()
    {
        var m = new MailModel();
        m.SetMessages([Summary("a")], new PageInfo { NextCursor = "c", Total = 3 });
        m.ListErr = ErrTest;
        var gen = m.ListGen;
        m.ClearMessages();
        Assert.Empty(m.Messages);
        Assert.Empty(m.Index);
        Assert.Null(m.NextCursor);
        Assert.Equal(-1, m.Total);
        Assert.Null(m.ListErr);
        Assert.True(m.ListGen == gen, "clearMessages must not touch the generation");
    }

    [Fact]
    public void AccountLabelTest()
    {
        var a = TestAccount("a", name: " Work ", email: "me@example.invalid");
        Assert.Equal("Work", FolderTree.AccountLabel(a));
        a = a with { Config = a.Config with { Name = "  " } };
        Assert.Equal("me@example.invalid", FolderTree.AccountLabel(a));
    }

    [Fact]
    public void SelfAddressTest()
    {
        var a = TestAccount("a", email: "me@example.invalid", displayName: "Me");
        Assert.Equal(new Address { Name = "Me", Email = "me@example.invalid" }, FolderTree.SelfAddress(a));
    }

    // Windows-only: Swift's `let m = mailbox.model` is a copy; Clone is what
    // gives a C# caller one, and the snapshot must not move with the model.
    [Fact]
    public void CloneIsASnapshot()
    {
        var (accounts, folders) = TestAccounts();
        var m = new MailModel(accounts, folders, collapsed: new CollapseState(), favourites: new FavouriteState());
        m.RebuildEntries();
        m.SetMessages([Summary("a"), Summary("b")], new PageInfo { Total = 2 });
        m.Search.Hits["a"] = new SearchHit("x", []);
        var entries = m.Entries;
        var snap = m.Clone();

        m.UpdateFlags("a", set: [Flag.Seen]);
        m.InsertMessage(0, Summary("c"));
        m.AdjustCounts(new FolderKey("acc1", "inbox"), 5, 5);
        m.Collapsed.SetFolder(new FolderKey("acc1", "alpha"), true);
        m.Favourites.Set(new FolderKey("acc1", "zeta"), true);
        m.Search.Hits.Clear();
        m.BumpList();

        Assert.Equal(["a", "b"], snap.Messages.Select(s => s.Id.Value));
        Assert.Empty(snap.Messages[0].Flags);
        Assert.Equal(0, snap.Index["a"]);
        Assert.Equal(2, snap.Folder(new FolderKey("acc1", "inbox"))?.Unread);
        Assert.Same(entries, snap.Entries);
        Assert.Equal(2, entries.Single(e => e.Folder?.Id.Value == "inbox").Badge);
        Assert.Empty(snap.Collapsed.Folders);
        Assert.True(snap.Favourites.IsEmpty);
        Assert.Single(snap.Search.Hits);
        Assert.Equal(0UL, snap.ListGen);
    }

    // One account with an inbox, a trash, an outbox and an All Mail the
    // daemon never downloads, and a paused one (model_test.go countsModel).
    private static MailModel CountsModel()
    {
        Account[] accounts =
        [
            TestAccount("a", email: "a@example.invalid"),
            TestAccount("p", enabled: false, email: "p@example.invalid"),
        ];
        var folders = new FolderMap
        {
            ["a"] =
            [
                TestFolder("in", "INBOX", FolderRole.Inbox, unread: 2, total: 10),
                TestFolder("trash", "Trash", FolderRole.Trash, unread: 0, total: 1),
                TestFolder("out", "Outbox", FolderRole.Outbox, total: 1),
                TestFolder("all", "All Mail", FolderRole.All, synced: false),
            ],
            ["p"] = [TestFolder("pout", "Outbox", FolderRole.Outbox, total: 2)],
        };
        var m = new MailModel(accounts, folders);
        m.RebuildEntries();
        return m;
    }

    // The cached unread and total of folder id of account "a", and whether
    // the sidebar entry agrees (model_test.go counts).
    private static (int Unread, int Total, bool EntryAgrees) Counts(MailModel m, string id)
    {
        var f = m.Folder(new FolderKey("a", id));
        var agrees = true;
        foreach (var e in m.Entries.Where(e => !e.Header && e.Account?.Id.Value == "a" && e.Folder?.Id.Value == id))
        {
            agrees = e.Folder?.Unread == f?.Unread && e.Folder?.Total == f?.Total && e.Badge == f?.Unread;
        }
        return (f?.Unread ?? -1, f?.Total ?? -1, agrees);
    }
}
