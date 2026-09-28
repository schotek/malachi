// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The tests of SidebarRow: the rows ui/internal/window/folders.go builds
// (newHeaderRow, newTwisty, newFolderRow with its badge and indent,
// rebuildFolderList's subtitle for a pinned folder) and favourites.go's
// star, over the entries FolderTree.SortFolders makes.

using System.Collections.Generic;
using System.ComponentModel;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Xunit;
using static Malachi.Core.Tests.Model.MailModelTests;
using FolderMap = System.Collections.Generic.Dictionary<Malachi.Core.Api.AccountId, System.Collections.Generic.IReadOnlyList<Malachi.Core.Api.Folder>>;

namespace Malachi.Core.Tests.Presentation;

public sealed class SidebarRowTests
{
    private static SidebarRow Row(FolderEntry e, bool several = false)
    {
        var row = new SidebarRow(SidebarKey.Of(e));
        row.Update(e, several);
        return row;
    }

    private static IReadOnlyList<FolderEntry> Entries(FavouriteState? favourites = null, CollapseState? collapsed = null)
    {
        var (accounts, folders) = TestAccounts();
        return FolderTree.SortFolders(accounts, folders, collapsed ?? new CollapseState(), favourites ?? new FavouriteState());
    }

    [Fact]
    public void AnAccountHeadingFolds()
    {
        var heading = Entries().First(e => e.Header);
        var row = Row(heading);
        Assert.Equal(SidebarRowKind.AccountHeading, row.Kind);
        Assert.True(row.IsHeading);
        Assert.Equal("one@example.invalid", row.Title);
        Assert.True(row.TwistyShown);
        Assert.True(row.TwistyActive);
        Assert.Equal("pan-down-symbolic", row.TwistyIcon);
        Assert.Equal("Collapse", row.TwistyTooltip);
        Assert.False(row.Selectable);
        Assert.False(row.HasStar);
        Assert.Equal(new AccountId("acc1"), row.Account);
        Assert.Null(row.Folder);

        var collapsed = new CollapseState();
        collapsed.ToggleAccount("acc1");
        var folded = Row(Entries(collapsed: collapsed).First(e => e.Header));
        Assert.True(folded.Collapsed);
        Assert.Equal("pan-end-symbolic", folded.TwistyIcon);
        Assert.Equal("Expand", folded.TwistyTooltip);
    }

    [Fact]
    public void TheFavouritesHeadingKeepsTheArrowsPlaceButDoesNotFold()
    {
        var favourites = new FavouriteState();
        favourites.Toggle(new FolderKey("acc1", "inbox"));
        var entries = Entries(favourites);
        var row = Row(entries[0]);
        Assert.Equal(SidebarRowKind.FavouritesHeading, row.Kind);
        Assert.Equal("Favourites", row.Title);
        Assert.True(row.TwistyShown);
        Assert.False(row.TwistyActive);
        Assert.Equal("", row.TwistyTooltip);
        Assert.Null(row.Account);
    }

    [Fact]
    public void AFolderRowShowsItsTitleIconBadgeAndIndent()
    {
        var entries = Entries();
        var inbox = Row(entries.First(e => e.Folder?.Id.Value == "inbox"));
        Assert.Equal(SidebarRowKind.Folder, inbox.Kind);
        // The role's localised name, whatever the server calls it.
        Assert.Equal("Inbox", inbox.Title);
        Assert.Equal("INBOX", inbox.Tooltip);
        Assert.Equal("mail-unread-symbolic", inbox.Icon);
        Assert.Equal("2", inbox.Badge);
        Assert.Equal(0, inbox.Indent);
        Assert.True(inbox.Selectable);
        Assert.False(inbox.Dimmed);
        Assert.Equal(new FolderKey("acc1", "inbox"), inbox.Folder);
        Assert.Equal("Inbox, 2", inbox.ToString());

        var deep = Row(entries.First(e => e.Folder?.Id.Value == "deep"));
        Assert.Equal(2 * SidebarRow.IndentPerLevel, deep.Indent);
        Assert.Equal("", deep.Badge);
        Assert.Equal("folder-symbolic", deep.Icon);
    }

    [Fact]
    public void TheArrowColumnIsThereOnlyInANestedAccount()
    {
        var entries = Entries();
        // acc1 has parents: every row keeps the column, only a parent's arrow folds.
        var alpha = Row(entries.First(e => e.Folder?.Id.Value == "alpha"));
        Assert.True(alpha.TwistyShown);
        Assert.True(alpha.TwistyActive);
        var zeta = Row(entries.First(e => e.Folder?.Id.Value == "zeta"));
        Assert.True(zeta.TwistyShown);
        Assert.False(zeta.TwistyActive);
        // acc2 is flat: no column at all.
        var in2 = Row(entries.First(e => e.Folder?.Id.Value == "in2"));
        Assert.False(in2.TwistyShown);
        Assert.False(in2.TwistyActive);
    }

    [Fact]
    public void AContainerIsDimmedAndHasNoStar()
    {
        var container = Row(Entries().First(e => e.Folder?.Id.Value == "container"));
        Assert.False(container.Selectable);
        Assert.True(container.Dimmed);
        Assert.False(container.HasStar);
        Assert.False(container.StarPinned);
    }

    [Fact]
    public void TheStarSaysWhetherTheFolderIsPinned()
    {
        var favourites = new FavouriteState();
        favourites.Toggle(new FolderKey("acc1", "inbox"));
        var entries = Entries(favourites);
        var inTree = Row(entries.First(e => !e.Favourite && e.Folder?.Id.Value == "inbox"));
        Assert.True(inTree.HasStar);
        Assert.True(inTree.Starred);
        Assert.Equal("starred-symbolic", inTree.StarIcon);
        Assert.Equal("Remove from Favourites", inTree.StarTooltip);
        // In the tree a filled star stays in sight; in the section it waits.
        Assert.True(inTree.StarPinned);
        var inSection = Row(entries.First(e => e.Favourite && !e.Header));
        Assert.True(inSection.Starred);
        Assert.False(inSection.StarPinned);
        Assert.Equal(new SidebarKey(false, true, "acc1", "inbox"), inSection.Key);

        var zeta = Row(entries.First(e => e.Folder?.Id.Value == "zeta"));
        Assert.False(zeta.Starred);
        Assert.Equal("non-starred-symbolic", zeta.StarIcon);
        Assert.Equal("Add to Favourites", zeta.StarTooltip);
        Assert.False(zeta.StarPinned);
    }

    [Fact]
    public void APinnedFolderSaysWhoseItIsWithSeveralAccounts()
    {
        var favourites = new FavouriteState();
        favourites.Toggle(new FolderKey("acc1", "inbox"));
        var pinned = Entries(favourites).First(e => e.Favourite && !e.Header);
        Assert.Equal("one@example.invalid", Row(pinned, several: true).Subtitle);
        Assert.Equal("", Row(pinned, several: false).Subtitle);
        Assert.Equal("Inbox, one@example.invalid, 2", Row(pinned, several: true).ToString());
    }

    [Fact]
    public void AnUpdateRaisesOnlyWhatChanged()
    {
        var accounts = new[] { TestAccount("a") };
        var folders = new FolderMap { ["a"] = [TestFolder("in", "INBOX", FolderRole.Inbox, unread: 1)] };
        var e = FolderTree.SortFolders(accounts, folders, new CollapseState(), new FavouriteState())[0];
        var row = Row(e);
        var changed = new List<string?>();
        row.PropertyChanged += (_, args) => changed.Add(args.PropertyName);
        row.Update(e with { Badge = 3 }, false);
        Assert.Equal(["Badge"], changed);
        Assert.Equal("3", row.Badge);
    }
}
