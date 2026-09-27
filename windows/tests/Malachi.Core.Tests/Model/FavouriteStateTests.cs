// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/FavouriteStateTests.swift, the
// counterpart of ui/internal/window/favourites_test.go (every test; the
// Swift suite ports all of them): the Favourites section and the pin state
// behind it. CloneIsIndependent is Windows-only: the state is a class here.

using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Xunit;
using static Malachi.Core.Tests.Model.CollapseStateTests;
using static Malachi.Core.Tests.Model.FolderTreeTests;
using static Malachi.Core.Tests.Model.MailModelTests;

namespace Malachi.Core.Tests.Model;

public sealed class FavouriteStateTests
{
    /// <summary>A state with the given folders pinned (favourites_test.go <c>pinned</c>).</summary>
    internal static FavouriteState Pinned(params FolderKey[] keys)
    {
        var f = new FavouriteState();
        foreach (var k in keys)
        {
            f.Set(k, true);
        }
        return f;
    }

    [Fact]
    public void FavouriteSectionFirstInTreeOrder()
    {
        var (accounts, folders) = TestAccounts();
        var f = Pinned(
            new FolderKey("acc2", "in2"),
            new FolderKey("acc1", "zeta"),
            new FolderKey("acc1", "deep"),
            new FolderKey("acc1", "inbox"));
        var entries = FolderTree.SortFolders(accounts, folders, new CollapseState(), f);

        // Accounts in list order, then role, then path: not the order pinned.
        string[] section = ["#favourites", "*inbox@0", "*deep@0", "*zeta@0", "*in2@0"];
        var plain = Ids(FolderTree.SortFolders(accounts, folders, new CollapseState(), new FavouriteState()));
        Assert.Equal([.. section, .. plain], Ids(entries));
        foreach (var e in entries.Skip(1).Take(4))
        {
            Assert.True(
                e.Depth == 0 && !e.HasChildren && !e.Nested && !e.Collapsed && e.Starred && e.Favourite,
                $"section row {e.Folder?.Id}: want depth 0, no children, starred");
            Assert.True(e.Account is not null, "section row carries no account");
        }
        Assert.Null(entries[0].Account);
        Assert.True(entries[0].Header);

        // The tree rows of pinned folders are starred, the others are not, and
        // none of them belongs to the section.
        var starred = new Dictionary<string, bool>();
        foreach (var e in entries.Skip(5).Where(e => !e.Header))
        {
            Assert.False(e.Favourite, $"tree row {e.Folder?.Id} marked as section row");
            if (e.Folder is { } folder)
            {
                starred[folder.Id.Value] = e.Starred;
            }
        }
        Assert.True(starred["inbox"]);
        Assert.True(starred["deep"]);
        Assert.True(starred["zeta"]);
        Assert.True(starred["in2"]);
        Assert.False(starred["trash"]);
        Assert.False(starred["alpha"]);
    }

    [Fact]
    public void FavouriteSectionGivesSingleAccountAHeader()
    {
        var (accounts, folders) = NestedAccount();
        var f = Pinned(new FolderKey("a", "bugs"));

        Assert.Equal(
            ["#favourites", "*bugs@0", "#a", "in@0", "work@0", "bugs@1", "old@2", "zulu@0"],
            Ids(FolderTree.SortFolders(accounts, folders, new CollapseState(), f)));

        // The section row shows the folder's own count even while the tree row
        // is collapsed and rolls its children up.
        var c = new CollapseState();
        c.SetFolder(new FolderKey("a", "bugs"), true);
        var entries = FolderTree.SortFolders(accounts, folders, c, f);
        Assert.Equal(4, entries[1].Badge);
        Assert.False(entries[1].Collapsed);
        var tree = EntryById(entries.Skip(2), "bugs");
        Assert.NotNull(tree);
        Assert.Equal(12, tree.Badge);
        Assert.True(tree.Collapsed);

        // With a heading the single account can be folded; the section stays.
        c.SetAccount("a", true);
        Assert.Equal(["#favourites", "*bugs@0", "#a"], Ids(FolderTree.SortFolders(accounts, folders, c, f)));
    }

    [Fact]
    public void FavouriteSectionSkipsWhatCannotShow()
    {
        var (accounts, folders) = TestAccounts();
        folders["acc1"] = [.. folders["acc1"], TestFolder("out", "Outbox", FolderRole.Outbox)];
        var f = Pinned(
            new FolderKey("acc1", "container"), // cannot be opened
            new FolderKey("acc3", "in3"), // account disabled
            new FolderKey("acc1", "gone"), // renamed on the server
            new FolderKey("acc1", "out")); // empty outbox is hidden
        var plain = Ids(FolderTree.SortFolders(accounts, folders, new CollapseState(), new FavouriteState()));
        Assert.Equal(plain, Ids(FolderTree.SortFolders(accounts, folders, new CollapseState(), f)));

        // An outbox with something in it is a folder like any other.
        folders["acc1"] = Replace(folders["acc1"], folders["acc1"].Count - 1, x => x with { Total = 1 });
        var got = Ids(FolderTree.SortFolders(accounts, folders, new CollapseState(), f));
        Assert.True(got.Length >= 2);
        Assert.Equal("#favourites", got[0]);
        Assert.True(got[1] == "*out@0", "want the outbox pinned");
    }

    [Fact]
    public void FavouriteBadgesFollowUnread()
    {
        var (accounts, folders) = NestedAccount();
        var m = new MailModel(accounts, folders, favourites: Pinned(new FolderKey("a", "zulu")));
        m.RebuildEntries();
        m.AdjustCounts(new FolderKey("a", "zulu"), -6, 0);

        var rows = 0;
        foreach (var e in m.Entries.Where(e => !e.Header && e.Folder?.Id.Value == "zulu"))
        {
            rows++;
            Assert.True(e.Badge == 10 && e.Folder?.Unread == 10, $"zulu row (favourite={e.Favourite}) badge {e.Badge}");
        }
        Assert.True(rows == 2, "zulu has the section's and the tree's row");
    }

    [Fact]
    public void InitialFolderPrefersTheTree()
    {
        var (accounts, folders) = TestAccounts();
        var m = new MailModel(accounts, folders, favourites: Pinned(new FolderKey("acc2", "in2")));
        m.RebuildEntries();
        Assert.True(m.InitialFolder() == new FolderKey("acc1", "inbox"), "want the first account's Inbox");

        // Every account folded away: only the section is left to choose from.
        m.Collapsed.SetAccount("acc1", true);
        m.Collapsed.SetAccount("acc2", true);
        m.RebuildEntries();
        Assert.True(m.InitialFolder() == new FolderKey("acc2", "in2"), "want the pinned Inbox");
    }

    [Fact]
    public void FavouriteStateRoundTrip()
    {
        using var s = Scratch();
        var f = Pinned(new FolderKey("acc_2", "f_2"), new FolderKey("acc_1", "f_1"));
        f.Save(s, [TestAccount("acc_1"), TestAccount("acc_2")]);

        // Stored sorted, so a no-op save is not a change for other windows.
        Assert.Equal(["acc_1/f_1", "acc_2/f_2"], s.FavouriteFolders);
        var got = FavouriteState.Load(s);
        Assert.Equal(2, got.Count);
        Assert.True(got.Has(new FolderKey("acc_1", "f_1")));
        Assert.True(got.Has(new FolderKey("acc_2", "f_2")));

        // Unpinning removes the entry rather than storing a false; pinning twice
        // changes nothing.
        got.Set(new FolderKey("acc_1", "f_1"), false);
        got.Set(new FolderKey("acc_2", "f_2"), true);
        got.Save(s, [TestAccount("acc_1"), TestAccount("acc_2")]);
        Assert.Equal(["acc_2/f_2"], s.FavouriteFolders);
    }

    [Fact]
    public void FavouriteStatePrunesRemovedAccounts()
    {
        using var s = Scratch();
        var f = Pinned(new FolderKey("acc_gone", "f_1"), new FolderKey("acc_1", "f_2"));

        f.Save(s, [TestAccount("acc_1")]);
        Assert.Equal(["acc_1/f_2"], s.FavouriteFolders);
        // An empty account list means "not loaded yet" and must prune nothing.
        f.Save(s, []);
        Assert.True(s.FavouriteFolders.Count == 2, "want both kept when no accounts are known");
    }

    [Fact]
    public void LoadFavouritesSkipsJunk()
    {
        using var s = Scratch();
        s.FavouriteFolders = ["acc_1/f_1", "", "/", "acc", "acc_1/", "acc_1/f_1", "acc_2/f_2"];
        var f = FavouriteState.Load(s);
        Assert.True(f.Count == 2, "want the two well-formed entries once each");
        Assert.True(f.Has(new FolderKey("acc_1", "f_1")));
        Assert.True(f.Has(new FolderKey("acc_2", "f_2")));

        // A model without a state (the default) reads as nothing pinned.
        var none = new FavouriteState();
        Assert.False(none.Has(new FolderKey("acc_1", "f_1")), "empty state reports a pin");
    }

    // Windows-only: Swift copies the struct on assignment; Clone is the copy
    // here, and changing either side must leave the other alone.
    [Fact]
    public void CloneIsIndependent()
    {
        var f = Pinned(new FolderKey("acc_1", "f_1"));
        var copy = f.Clone();
        f.Toggle(new FolderKey("acc_1", "f_2"));
        copy.Toggle(new FolderKey("acc_1", "f_1"));
        Assert.Equal(2, f.Count);
        Assert.True(copy.IsEmpty);
    }
}
