// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/FolderTreeTests.swift, the
// counterpart of ui/internal/window/folders_test.go (every test; the Swift
// suite ports all of them): the sidebar layout the window relies on, where
// ordering, depth and header placement must be exact. SortSiblingsGoOrder
// and DeepChainDoesNotRecurse are Windows-only: they pin what the C# port
// had to do on purpose (Go's case folding and byte order, a walk without
// recursion over a hostile server's folder chain).

using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Xunit;
using static Malachi.Core.Tests.Model.MailModelTests;
using FolderMap = System.Collections.Generic.Dictionary<Malachi.Core.Api.AccountId, System.Collections.Generic.IReadOnlyList<Malachi.Core.Api.Folder>>;

namespace Malachi.Core.Tests.Model;

public sealed class FolderTreeTests
{
    /// <summary>
    /// Renders entries compactly (folders_test.go <c>ids</c>): "#acc" for an
    /// account heading, "#favourites" for the Favourites heading, "id@depth"
    /// for a tree row and "*id@depth" for a row of the Favourites section.
    /// </summary>
    internal static string[] Ids(IEnumerable<FolderEntry> entries) =>
    [
        .. entries.Select(e =>
        {
            if (e.Header && e.Favourite)
            {
                return "#favourites";
            }
            if (e.Header)
            {
                return "#" + (e.Account?.Id.Value ?? "");
            }
            var id = e.Folder?.Id.Value ?? "";
            return e.Favourite ? $"*{id}@{e.Depth}" : $"{id}@{e.Depth}";
        }),
    ];

    [Fact]
    public void SortFoldersHeadersOnlyForEnabled()
    {
        Account[] accounts = [TestAccount("a"), TestAccount("b", enabled: false)];
        var folders = new FolderMap
        {
            ["a"] = [TestFolder("in", "INBOX", FolderRole.Inbox)],
            ["b"] = [TestFolder("in-b", "INBOX", FolderRole.Inbox)],
        };
        // One enabled account: no header, the disabled one is absent entirely.
        Assert.Equal(["in@0"], Ids(FolderTree.SortFolders(accounts, folders, new CollapseState(), new FavouriteState())));
        accounts[1] = accounts[1] with { Enabled = true };
        Assert.Equal(["#a", "in@0", "#b", "in-b@0"], Ids(FolderTree.SortFolders(accounts, folders, new CollapseState(), new FavouriteState())));
    }

    [Fact]
    public void SortFoldersEmptyAccountKeepsHeader()
    {
        // An account before its first sync lists no folders; its header still
        // appears so the user sees the account is there.
        Account[] accounts = [TestAccount("a"), TestAccount("b")];
        var folders = new FolderMap
        {
            ["b"] = [TestFolder("in-b", "INBOX", FolderRole.Inbox)],
        };
        Assert.Equal(["#a", "#b", "in-b@0"], Ids(FolderTree.SortFolders(accounts, folders, new CollapseState(), new FavouriteState())));
        var m = new MailModel(accounts, folders);
        m.RebuildEntries();
        Assert.True(m.InitialFolder() == new FolderKey("b", "in-b"), "initialFolder skipped the header");
    }

    [Fact]
    public void SortFoldersOrdering()
    {
        Account[] accounts = [TestAccount("a")];
        var folders = new FolderMap
        {
            ["a"] =
            [
                TestFolder("b", "beta"),
                TestFolder("A", "Alpha"),
                TestFolder("sent", "Sent", FolderRole.Sent),
                TestFolder("all", "All Mail", FolderRole.All),
                TestFolder("junk", "Junk", FolderRole.Junk),
                TestFolder("in", "zzz/INBOX", FolderRole.Inbox),
                TestFolder("drafts", "Drafts", FolderRole.Drafts),
                TestFolder("arch", "Archive", FolderRole.Archive),
                TestFolder("trash", "Trash", FolderRole.Trash),
                // Total: an empty outbox is not listed (visibleFolders).
                TestFolder("out", "Outbox", FolderRole.Outbox, total: 1),
            ],
        };
        // Roles in rank order regardless of path, then plain folders by
        // case-insensitive path.
        string[] want = ["in@0", "drafts@0", "sent@0", "arch@0", "junk@0", "trash@0", "out@0", "all@0", "A@0", "b@0"];
        Assert.Equal(want, Ids(FolderTree.SortFolders(accounts, folders, new CollapseState(), new FavouriteState())));
    }

    [Fact]
    public void SortFoldersChildrenFollowParent()
    {
        Account[] accounts = [TestAccount("a")];
        var folders = new FolderMap
        {
            ["a"] =
            [
                // Children listed before their parent and out of order.
                TestFolder("p-b", "Projects/b", parent: "p"),
                TestFolder("p-a-x", "Projects/a/x", parent: "p-a"),
                TestFolder("p-a", "Projects/a", parent: "p"),
                TestFolder("p", "Projects", selectable: false),
                TestFolder("in", "INBOX", FolderRole.Inbox),
                // A child with a role sorts before its plain siblings.
                TestFolder("p-junk", "Projects/zz", FolderRole.Junk, parent: "p"),
            ],
        };
        string[] want = ["in@0", "p@0", "p-junk@1", "p-a@1", "p-a-x@2", "p-b@1"];
        Assert.Equal(want, Ids(FolderTree.SortFolders(accounts, folders, new CollapseState(), new FavouriteState())));
    }

    [Fact]
    public void SortFoldersDepthCap()
    {
        // A chain deeper than maxFolderDepth: the part below the cap is listed
        // flat afterwards, indented by its path, and nothing is lost.
        const int n = FolderTree.MaxFolderDepth + 5;
        var list = new List<Folder>();
        var path = "";
        for (var i = 0; i < n; i++)
        {
            if (path.Length > 0)
            {
                path += "/";
            }
            path += $"f{i}";
            list.Add(TestFolder($"f{i}", path, parent: i > 0 ? $"f{i - 1}" : null));
        }
        var entries = FolderTree.SortFolders([TestAccount("a")], new FolderMap { ["a"] = list }, new CollapseState(), new FavouriteState());
        Assert.Equal(n, entries.Count);
        var seen = new HashSet<string>();
        for (var i = 0; i < entries.Count; i++)
        {
            var e = entries[i];
            Assert.True(e.Folder is not null, $"entry {i} has no folder");
            Assert.True(seen.Add(e.Folder.Id.Value), $"entry {i} duplicated");
            Assert.True(e.Depth >= 0 && e.Depth <= n, $"entry {i} depth {e.Depth} out of range");
        }
        Assert.Equal("f0", entries[0].Folder?.Id.Value);
        Assert.Equal(0, entries[0].Depth);
        Assert.Equal(FolderTree.MaxFolderDepth, entries[FolderTree.MaxFolderDepth].Depth);
        var orphan = entries[FolderTree.MaxFolderDepth + 1];
        Assert.Equal("f33", orphan.Folder?.Id.Value);
        Assert.Equal(33, orphan.Depth);
    }

    [Fact]
    public void SortFoldersIgnoresFoldersOfUnknownAccounts()
    {
        Account[] accounts = [TestAccount("a")];
        var folders = new FolderMap
        {
            ["a"] = [TestFolder("in", "INBOX", FolderRole.Inbox)],
            ["ghost"] = [TestFolder("g", "INBOX", FolderRole.Inbox)],
        };
        Assert.Equal(["in@0"], Ids(FolderTree.SortFolders(accounts, folders, new CollapseState(), new FavouriteState())));
    }

    [Fact]
    public void FolderCountsTextTest()
    {
        (string Name, Folder F, string Want)[] cases =
        [
            ("empty", TestFolder("in", "INBOX", FolderRole.Inbox), ""),
            ("unsynced", TestFolder("all", "All Mail", FolderRole.All, synced: false), ""),
            ("inconsistent unread without total", TestFolder("f", "f", unread: 3), ""),
            ("one read message", TestFolder("f", "f", total: 1), "1 message"),
            ("only read messages", TestFolder("f", "f", total: 1234), "1234 messages"),
            ("one unread", TestFolder("f", "f", unread: 1, total: 1234), "1 unread of 1234"),
            ("several unread", TestFolder("f", "f", unread: 12, total: 1234), "12 unread of 1234"),
            ("all unread", TestFolder("f", "f", unread: 2, total: 2), "2 unread of 2"),
            // The outbox holds what is to be sent, not mail to read.
            ("outbox", TestFolder("out", "Outbox", FolderRole.Outbox, unread: 1, total: 2), "2 messages"),
            ("outbox of one", TestFolder("out", "Outbox", FolderRole.Outbox, total: 1), "1 message"),
        ];
        foreach (var (name, f, want) in cases)
        {
            var got = FolderTree.FolderCountsText(f);
            Assert.True(got == want, $"{name}: folderCountsText = {got}");
        }
    }

    // Windows-only: siblings are ordered as Go orders them, case folded rune
    // by rune and compared by code point (UTF-8 byte order), not by UTF-16
    // units, and equal keys keep the server's order.
    [Fact]
    public void SortSiblingsGoOrder()
    {
        Folder[] list =
        [
            TestFolder("private-use", ""),
            TestFolder("emoji", "\U0001F600"),
            TestFolder("upper", "ÉCOLE"),
            TestFolder("lower", "école"),
            TestFolder("z", "Z"),
        ];
        // U+1F600 is above U+E000 by code point, below it by UTF-16 unit.
        Assert.Equal(
            ["z", "upper", "lower", "private-use", "emoji"],
            FolderTree.SortSiblings(list).Select(f => f.Id.Value));
    }

    // Windows-only: a hostile server's chain far below the depth cap, folded
    // at its top, must not exhaust the stack of the walks that have no cap.
    [Fact]
    public void DeepChainDoesNotRecurse()
    {
        const int n = 200_000;
        var list = new List<Folder>(n);
        for (var i = 0; i < n; i++)
        {
            list.Add(TestFolder($"f{i}", $"f{i}", parent: i > 0 ? $"f{i - 1}" : null, unread: 1));
        }
        var collapsed = new CollapseState();
        collapsed.SetFolder(new FolderKey("a", "f0"), true);
        var entries = FolderTree.SortFolders([TestAccount("a")], new FolderMap { ["a"] = list }, collapsed, new FavouriteState());
        var root = Assert.Single(entries);
        Assert.Equal(n, root.Badge);
    }
}
