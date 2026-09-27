// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CollapseStateTests.swift, the
// counterpart of ui/internal/window/collapse_test.go (every test; the Swift
// suite ports all of them): folding parts of the sidebar away, which rows
// survive, what the badges then say, and what is written to and read back
// from the settings. Swift's ScratchSettings (a throwaway UserDefaults
// suite) is a SettingsStore over an InMemorySettingsBackend here.
// CloneIsIndependent is Windows-only: the state is a class here.

using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Malachi.Core.Settings;
using Xunit;
using static Malachi.Core.Tests.Model.FolderTreeTests;
using static Malachi.Core.Tests.Model.MailModelTests;
using FolderMap = System.Collections.Generic.Dictionary<Malachi.Core.Api.AccountId, System.Collections.Generic.IReadOnlyList<Malachi.Core.Api.Folder>>;

namespace Malachi.Core.Tests.Model;

public sealed class CollapseStateTests
{
    /// <summary>
    /// One account whose tree is INBOX; Work -> Work/Bugs -> Work/Bugs/Old;
    /// Zulu, with unread counts that make a roll-up visible
    /// (collapse_test.go <c>nestedFolders</c>).
    /// </summary>
    internal static Folder[] NestedFolders() =>
    [
        TestFolder("in", "INBOX", FolderRole.Inbox, unread: 1),
        TestFolder("work", "Work", unread: 2),
        TestFolder("bugs", "Work/Bugs", parent: "work", unread: 4),
        TestFolder("old", "Work/Bugs/Old", parent: "bugs", unread: 8),
        TestFolder("zulu", "Zulu", unread: 16),
    ];

    internal static (Account[] Accounts, FolderMap Folders) NestedAccount() =>
        ([TestAccount("a")], new FolderMap { ["a"] = NestedFolders() });

    /// <summary>Finds a row by folder id (collapse_test.go <c>entryByID</c>).</summary>
    internal static FolderEntry? EntryById(IEnumerable<FolderEntry> entries, string id) =>
        entries.FirstOrDefault(e => !e.Header && e.Folder?.Id.Value == id);

    /// <summary>A throwaway settings store (Swift <c>ScratchSettings</c>).</summary>
    internal static SettingsStore Scratch() => new(new InMemorySettingsBackend(), null);

    [Fact]
    public void FolderTreeExpandedByDefault()
    {
        var (accounts, folders) = NestedAccount();
        var entries = FolderTree.SortFolders(accounts, folders, new CollapseState(), new FavouriteState());

        Assert.Equal(["in@0", "work@0", "bugs@1", "old@2", "zulu@0"], Ids(entries));
        var work = EntryById(entries, "work");
        Assert.NotNull(work);
        Assert.True(work.HasChildren);
        Assert.False(work.Collapsed);
        Assert.True(work.Badge == 2, "expanded Work badge shows its own 2");
        // Every row of a nested account reserves the arrow column so the titles
        // line up, including the leaves.
        foreach (var e in entries)
        {
            Assert.True(e.Nested, $"{e.Folder?.Id}: Nested = false in a nested account");
        }
        var zulu = EntryById(entries, "zulu");
        Assert.NotNull(zulu);
        Assert.False(zulu.HasChildren, "Zulu has no children but claims to");
    }

    [Fact]
    public void FolderTreeCollapsedHidesWholeSubtree()
    {
        var (accounts, folders) = NestedAccount();
        var c = new CollapseState();
        c.SetFolder(new FolderKey("a", "work"), true);
        var entries = FolderTree.SortFolders(accounts, folders, c, new FavouriteState());

        // Both the child and the grandchild go, not just the child.
        Assert.Equal(["in@0", "work@0", "zulu@0"], Ids(entries));
        var work = EntryById(entries, "work");
        Assert.NotNull(work);
        Assert.True(work.Collapsed);
        Assert.True(work.HasChildren);
        // 2 of its own plus 4 and 8 from the two hidden descendants.
        Assert.Equal(14, work.Badge);
        // Siblings are untouched.
        Assert.Equal(1, EntryById(entries, "in")?.Badge);
    }

    [Fact]
    public void FolderTreeCollapsedInnerNode()
    {
        var (accounts, folders) = NestedAccount();
        var c = new CollapseState();
        c.SetFolder(new FolderKey("a", "bugs"), true);
        var entries = FolderTree.SortFolders(accounts, folders, c, new FavouriteState());

        Assert.Equal(["in@0", "work@0", "bugs@1", "zulu@0"], Ids(entries));
        Assert.True(EntryById(entries, "bugs")?.Badge == 12, "collapsed Work/Bugs badge is 4+8");
        // The expanded ancestor keeps counting only itself: its child is visible
        // and carries the rest.
        Assert.Equal(2, EntryById(entries, "work")?.Badge);
    }

    [Fact]
    public void FolderTreeCollapsingALeafDoesNothing()
    {
        var (accounts, folders) = NestedAccount();
        var c = new CollapseState();
        // A stale entry for a folder that has no children any more must not
        // remove it from the list or change its badge.
        c.SetFolder(new FolderKey("a", "zulu"), true);
        var entries = FolderTree.SortFolders(accounts, folders, c, new FavouriteState());

        Assert.Equal(["in@0", "work@0", "bugs@1", "old@2", "zulu@0"], Ids(entries));
        var zulu = EntryById(entries, "zulu");
        Assert.NotNull(zulu);
        Assert.False(zulu.Collapsed);
        Assert.Equal(16, zulu.Badge);
    }

    [Fact]
    public void SortFoldersCollapsedAccount()
    {
        Account[] accounts = [TestAccount("a"), TestAccount("b")];
        var folders = new FolderMap
        {
            ["a"] = NestedFolders(),
            ["b"] = [TestFolder("in-b", "INBOX", FolderRole.Inbox)],
        };
        var c = new CollapseState();
        c.SetAccount("a", true);
        var entries = FolderTree.SortFolders(accounts, folders, c, new FavouriteState());

        // The header stays, its whole tree goes, the other account is untouched.
        Assert.Equal(["#a", "#b", "in-b@0"], Ids(entries));
        Assert.True(entries[0].Collapsed && entries[0].HasChildren, "collapsed account header must be foldable and folded");
        Assert.False(entries[1].Collapsed, "the other account must stay expanded");
    }

    [Fact]
    public void SortFoldersSingleAccountIgnoresAccountFold()
    {
        // With one account there is no header, so there is nothing to click and
        // a stored fold must not blank the sidebar.
        var (accounts, folders) = NestedAccount();
        var c = new CollapseState();
        c.SetAccount("a", true);
        Assert.Equal(5, FolderTree.SortFolders(accounts, folders, c, new FavouriteState()).Count);
    }

    [Fact]
    public void FolderTreeFlatAccountReservesNoArrow()
    {
        Account[] accounts = [TestAccount("a")];
        var folders = new FolderMap
        {
            ["a"] = [TestFolder("in", "INBOX", FolderRole.Inbox), TestFolder("zulu", "Zulu")],
        };
        foreach (var e in FolderTree.SortFolders(accounts, folders, new CollapseState(), new FavouriteState()))
        {
            Assert.True(!e.Nested && !e.HasChildren, $"{e.Folder?.Id}: want no arrow in a flat account");
        }
    }

    [Fact]
    public void FolderTreeCollapsedParentCycleStaysFlat()
    {
        // Two folders pointing at each other are unreachable from any root; the
        // orphan sweep lists them flat and folding does not apply.
        Account[] accounts = [TestAccount("a")];
        var folders = new FolderMap
        {
            ["a"] = [TestFolder("x", "X/One", parent: "y", unread: 3), TestFolder("y", "X/Two", parent: "x")],
        };
        var c = new CollapseState();
        c.SetFolder(new FolderKey("a", "x"), true);
        var entries = FolderTree.SortFolders(accounts, folders, c, new FavouriteState());

        Assert.True(entries.Count == 2, "want both folders listed");
        foreach (var e in entries)
        {
            Assert.False(e.Collapsed, $"{e.Folder?.Id}: an unreachable folder must not be folded");
        }
        Assert.True(EntryById(entries, "x")?.Badge == 3, "X/One badge is its own 3");
    }

    [Fact]
    public void RefreshBadgesFollowsUnread()
    {
        var (accounts, folders) = NestedAccount();
        var m = new MailModel(accounts, folders);
        m.Collapsed.SetFolder(new FolderKey("a", "work"), true);
        m.RebuildEntries();

        // A new message lands in the hidden grandchild: the visible ancestor's
        // badge has to move even though the folder itself has no row.
        m.AdjustCounts(new FolderKey("a", "old"), 1, 1);
        Assert.True(EntryById(m.Entries, "work")?.Badge == 15, "Work badge after the hidden grandchild gained one");
    }

    [Fact]
    public void CollapseStateRoundTrip()
    {
        using var s = Scratch();
        var c = new CollapseState();
        c.SetFolder(new FolderKey("acc_1", "f_1"), true);
        c.SetFolder(new FolderKey("acc_2", "f_2"), true);
        c.SetAccount("acc_2", true);
        c.Save(s, [TestAccount("acc_1"), TestAccount("acc_2")]);

        var got = CollapseState.Load(s);
        Assert.True(got.FolderCollapsed(new FolderKey("acc_1", "f_1")));
        Assert.True(got.FolderCollapsed(new FolderKey("acc_2", "f_2")));
        Assert.True(got.AccountCollapsed("acc_2"));
        Assert.False(got.AccountCollapsed("acc_1"));

        // Unfolding removes the entry rather than storing a false.
        got.SetFolder(new FolderKey("acc_1", "f_1"), false);
        got.Save(s, [TestAccount("acc_1"), TestAccount("acc_2")]);
        Assert.Single(s.CollapsedFolders);
    }

    [Fact]
    public void CollapseStatePrunesRemovedAccounts()
    {
        using var s = Scratch();
        var c = new CollapseState();
        c.SetFolder(new FolderKey("acc_gone", "f_1"), true);
        c.SetFolder(new FolderKey("acc_1", "f_2"), true);
        c.SetAccount("acc_gone", true);

        c.Save(s, [TestAccount("acc_1")]);
        Assert.Equal(["acc_1/f_2"], s.CollapsedFolders);
        Assert.Empty(s.CollapsedAccounts);

        // An empty account list means "not loaded yet" and must prune nothing.
        c.Save(s, []);
        Assert.True(s.CollapsedFolders.Count == 2, "both kept when no accounts are known");
    }

    [Fact]
    public void CollapseStateStoredSorted()
    {
        // Set order is arbitrary; a stable stored value keeps a no-op save from
        // looking like a change to the other windows listening for it.
        using var s = Scratch();
        var c = new CollapseState();
        foreach (var id in new[] { "f_3", "f_1", "f_2" })
        {
            c.SetFolder(new FolderKey("acc_1", id), true);
        }
        c.Save(s, [TestAccount("acc_1")]);
        Assert.Equal(["acc_1/f_1", "acc_1/f_2", "acc_1/f_3"], s.CollapsedFolders);
    }

    [Fact]
    public void DecodeFolderKeyRejectsJunk()
    {
        foreach (var input in new[] { "", "/", "acc_1", "acc_1/", "/f_1" })
        {
            Assert.True(FolderKey.Decode(input) is null, $"FolderKey.Decode({input}) accepted a malformed entry");
        }
        var k = FolderKey.Decode("acc_1/f_1");
        Assert.Equal("acc_1", k?.Account.Value);
        Assert.Equal("f_1", k?.Folder.Value);
        Assert.Equal("acc_1/f_1", k?.Encoded);
    }

    [Fact]
    public void LoadCollapseSkipsJunk()
    {
        using var s = Scratch();
        s.CollapsedFolders = ["acc_1/f_1", "nonsense", "", "acc_2/f_2"];
        s.CollapsedAccounts = ["acc_3", "  "];

        var c = CollapseState.Load(s);
        Assert.True(c.Folders.Count == 2, "want the two well-formed entries");
        Assert.Single(c.Accounts);
        Assert.True(c.AccountCollapsed("acc_3"));
    }

    [Fact]
    public void FolderListedIgnoresFolds()
    {
        var (accounts, folders) = NestedAccount();
        var m = new MailModel(accounts, folders);
        m.Collapsed.SetFolder(new FolderKey("a", "work"), true);
        m.RebuildEntries();

        // Folded out of sight is still listed, so the selection stays put.
        Assert.True(m.FolderListed(new FolderKey("a", "old")), "a folder hidden by a fold must still count as listed");
        Assert.False(m.FolderListed(new FolderKey("a", "nope")), "an unknown folder must not count as listed");
        Assert.False(m.FolderListed(null), "the zero key must not count as listed");

        // An empty outbox is hidden outright, and a selection there must move.
        m.Folders["a"] = [.. m.Folders["a"], TestFolder("out", "Outbox", FolderRole.Outbox)];
        Assert.False(m.FolderListed(new FolderKey("a", "out")), "an empty outbox is not listed");

        m.Accounts = Replace(m.Accounts, 0, a => a with { Enabled = false });
        Assert.False(m.FolderListed(new FolderKey("a", "in")), "a disabled account's folders are not listed");
    }

    // Windows-only: Swift copies the struct on assignment; Clone is the copy
    // here, and changing either side must leave the other alone.
    [Fact]
    public void CloneIsIndependent()
    {
        var c = new CollapseState();
        c.SetFolder(new FolderKey("acc_1", "f_1"), true);
        var copy = c.Clone();
        c.SetAccount("acc_1", true);
        copy.SetFolder(new FolderKey("acc_1", "f_1"), false);
        Assert.True(c.FolderCollapsed(new FolderKey("acc_1", "f_1")));
        Assert.False(copy.AccountCollapsed("acc_1"));
        Assert.Empty(copy.Folders);
    }
}
