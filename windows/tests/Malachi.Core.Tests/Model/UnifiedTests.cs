// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/unified/unified_test.go (TestAccountsAndShown,
// TestInboxesAndCounts, TestKey, TestParams, TestGroupByAccount; the
// catalogue test belongs to po/), with the Windows-only pieces of the item:
// the sidebar row (FolderTree.SortFolders, SidebarRow), the model's
// ListCovers and ThreadScope.

using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Xunit;
using FolderMap = System.Collections.Generic.Dictionary<Malachi.Core.Api.AccountId, System.Collections.Generic.IReadOnlyList<Malachi.Core.Api.Folder>>;

namespace Malachi.Core.Tests.Model;

public sealed class UnifiedTests
{
    private static Account Acc(string id, string? kind, bool enabled) => new()
    {
        Id = id,
        Enabled = enabled,
        Config = new AccountConfig { Name = id, Email = id + "@example.com", Kind = kind is null ? null : (AccountKind?)new AccountKind(kind) },
        State = new SyncState { AccountId = id, Status = SyncStatus.Idle },
    };

    private static Folder Inbox(string acc, string id, int unread, int total) => new()
    {
        Id = id,
        AccountId = acc,
        Name = id,
        Path = id,
        Role = FolderRole.Inbox,
        Subscribed = true,
        Selectable = true,
        Synced = true,
        Unread = unread,
        Total = total,
    };

    private static readonly Account[] Sample =
    [
        Acc("jira", AccountKind.Jira, true),
        Acc("b", AccountKind.Graph, true),
        Acc("off", AccountKind.Imap, false),
        Acc("cal", AccountKind.CalDAV, true),
        Acc("a", null, true), // an empty kind is imap
        Acc("crm", AccountKind.Eway, true),
    ];

    private static FolderMap SampleFolders() => new()
    {
        ["a"] = [Inbox("a", "a-sent", 9, 0) with { Role = FolderRole.Sent }, Inbox("a", "a-in", 2, 10)],
        ["b"] = [Inbox("b", "b-in", 3, 5), Inbox("b", "b-arch", 7, 7) with { Synced = false }],
        ["off"] = [Inbox("off", "off-in", 4, 4)],
        ["jira"] = [Inbox("jira", "j-v", 1, 0) with { Virtual = new VirtualFolder(VirtualFolder.Open) }],
    };

    [Fact]
    public void AccountsAndShown()
    {
        Assert.Equal<AccountId>(["b", "a"], Unified.Accounts(Sample));
        Assert.True(Unified.Shown(Sample, true));
        Assert.False(Unified.Shown(Sample, false));
        Account[] one = [Acc("a", AccountKind.Imap, true), Acc("off", AccountKind.Graph, false), Acc("j", AccountKind.Jira, true)];
        Assert.False(Unified.Shown(one, true), "one mail account: not shown");
        Assert.False(Unified.Shown([], true), "no account: not shown");
    }

    [Fact]
    public void InboxesAndCounts()
    {
        var got = Unified.Inboxes(Sample, SampleFolders());
        Assert.Equal(["b-in", "a-in"], got.Select(f => f.Id.Value));
        Assert.Equal(5, Unified.Unread(got));
        Assert.Equal(15, Unified.Total(got));
        Assert.Equal(0, Unified.Unread([]));
        Assert.Equal(0, Unified.Total([]));
        Assert.True(Unified.Covers(true, "a", "a-in", got));
        Assert.False(Unified.Covers(false, "a", "a-in", got));
        Assert.False(Unified.Covers(true, "a", "a-sent", got) || Unified.Covers(true, "b", "a-in", got) || Unified.Covers(true, "off", "off-in", got));
        Assert.True(Unified.CoversAny(true, "b", null, got));
        Assert.False(Unified.CoversAny(true, "off", null, got) || Unified.CoversAny(false, "b", null, got));
        Assert.True(Unified.CoversAny(true, "a", ["a-sent", "a-in"], got));
        Assert.False(Unified.CoversAny(true, "a", ["a-sent"], got));
    }

    [Fact]
    public void KeyIsTheSentinel()
    {
        Assert.True(Unified.Key.IsUnified);
        Assert.True(Unified.IsKey("", Unified.KeyFolder));
        foreach (var k in new[] { default(FolderKey), new FolderKey("a", Unified.KeyFolder), new FolderKey("", "f"), new FolderKey("a", "a-in") })
        {
            Assert.False(k.IsUnified, $"{k} is not the key");
        }
        Assert.StartsWith("@", Unified.KeyFolder, System.StringComparison.Ordinal);
        // Never a stored entry: a favourite or a fold cannot decode it.
        Assert.Null(FolderKey.Decode(Unified.Key.Encoded));
    }

    [Fact]
    public void ParamsNameTheRoleAndNoFolder()
    {
        var m = Unified.MessageParams(MessageFilter.Unread, "c");
        Assert.Equal(
            new MessageListParams
            {
                AccountId = "",
                FolderId = "",
                Unified = FolderRole.Inbox,
                Page = new Page { Limit = API.Limits.DefaultPageLimit, Cursor = "c" },
                Sort = SortOrder.DateDesc,
                Filter = MessageFilter.Unread,
            },
            m);
        var th = Unified.ThreadParams(MessageFilter.All, "");
        Assert.Equal(FolderRole.Inbox, th.Unified);
        Assert.Equal("", th.AccountId.Value);
        Assert.Equal("", th.FolderId.Value);
        Assert.Null(th.Page.Cursor);
        Assert.Equal(API.Limits.DefaultPageLimit, th.Page.Limit);
        Assert.Equal(SortOrder.DateDesc, th.Sort);
    }

    private sealed record Row(string Id, string Acc);

    [Fact]
    public void GroupByAccountKeepsOrders()
    {
        Row[] rows = [new("1", "b"), new("2", "a"), new("3", "b"), new("4", "a"), new("5", "c")];
        AccountId Of(Row r) => r.Acc;
        var got = Unified.GroupByAccount(rows, Of);
        Assert.Equal<AccountId>(["b", "a", "c"], got.Select(g => g.Account));
        Assert.Equal(["1", "3"], got[0].Items.Select(r => r.Id));
        Assert.Equal(["2", "4"], got[1].Items.Select(r => r.Id));
        Assert.Equal(["5"], got[2].Items.Select(r => r.Id));
        Assert.Empty(Unified.GroupByAccount(System.Array.Empty<Row>(), Of));
        Assert.True(Unified.Mixed(rows, Of));
        Assert.False(Unified.Mixed(rows[..1], Of));
        Assert.False(Unified.Mixed([new Row("1", "a"), new Row("2", "a")], Of));
        Assert.False(Unified.Mixed(System.Array.Empty<Row>(), Of));
    }

    [Fact]
    public void TextsAreTheGtkMsgids()
    {
        Assert.Equal("All Inboxes", Unified.Title);
        Assert.Equal("Show All Inboxes", Unified.PreferenceTitle);
        Assert.Equal("In the sidebar when you have more than one mail account", Unified.PreferenceSubtitle);
    }

    [Fact]
    public void TheSidebarHasTheItemFirstInTheFavouritesSection()
    {
        // unified_test.go TestUnifiedEntryHeadsTheFavouritesSection and
        // TestUnifiedEntryComesBeforeThePins.
        Account[] accounts = [Acc("a", AccountKind.Imap, true), Acc("b", AccountKind.Graph, true)];
        var folders = new FolderMap { ["a"] = [Inbox("a", "a-in", 2, 10)], ["b"] = [Inbox("b", "b-in", 3, 5)] };
        var on = FolderTree.SortFolders(accounts, folders, new CollapseState(), new FavouriteState(), unifiedInbox: true);
        // The section exists for the item alone: its heading, then the item.
        Assert.Equal(["#favourites", "*@unified:inbox@0", "#a", "a-in@0", "#b", "b-in@0"], FolderTreeTests.Ids(on));
        Assert.True(on[0].Header && on[0].Favourite && on[0].HasChildren && !on[0].Collapsed);
        Assert.True(on[1].IsUnified && on[1].Favourite);
        Assert.Equal(5, on[1].Badge);
        Assert.Equal(0, on[1].Depth);
        Assert.Equal(Unified.Key, on[1].Key);
        Assert.False(on[1].Header);
        Assert.Equal(on.Count, on.Select(SidebarKey.Of).Distinct().Count());
        var off = FolderTree.SortFolders(accounts, folders, new CollapseState(), new FavouriteState());
        Assert.DoesNotContain(off, e => e.IsUnified);
        Assert.DoesNotContain(off, e => e.Header && e.Favourite);
        // One mail account: no item, whatever the setting.
        var one = FolderTree.SortFolders([accounts[0]], folders, new CollapseState(), new FavouriteState(), unifiedInbox: true);
        Assert.DoesNotContain(one, e => e.IsUnified);
        // The pins follow the item.
        var fav = new FavouriteState();
        fav.Toggle(new FolderKey("b", "b-in"));
        var withFavourites = FolderTree.SortFolders(accounts, folders, new CollapseState(), fav, unifiedInbox: true);
        Assert.Equal(["#favourites", "*@unified:inbox@0", "*b-in@0"], FolderTreeTests.Ids(withFavourites.Take(3)));
        // Folded, the section is its heading alone; the pinned folder is
        // still in the tree and the item stays listed.
        var collapsed = new CollapseState();
        collapsed.SetFavourites(true);
        var folded = FolderTree.SortFolders(accounts, folders, collapsed, fav, unifiedInbox: true);
        Assert.Equal(["#favourites", "#a", "a-in@0", "#b", "b-in@0"], FolderTreeTests.Ids(folded));
        Assert.True(folded[0].Collapsed && folded[0].HasChildren);
        var foldedModel = new MailModel(accounts, folders, collapsed: collapsed) { UnifiedInbox = true };
        foldedModel.RebuildEntries();
        Assert.True(foldedModel.FolderListed(Unified.Key));
        // It is never the initial folder.
        var model = new MailModel(accounts, folders) { UnifiedInbox = true };
        model.RebuildEntries();
        Assert.Equal(new FolderKey("a", "a-in"), model.InitialFolder());
    }

    [Fact]
    public void TheRowShowsTitleIconAndBadgeAndCannotBePinned()
    {
        var row = new SidebarRow(SidebarKey.ForFolder(Unified.Key, false));
        row.Update(new FolderEntry { IsUnified = true, Favourite = true, Badge = 5 }, several: true);
        Assert.Equal(SidebarRowKind.Unified, row.Kind);
        Assert.False(row.IsHeading);
        Assert.Equal("All Inboxes", row.Title);
        Assert.Equal("5", row.Badge);
        Assert.True(row.Selectable);
        Assert.False(row.HasStar);
        Assert.False(row.TwistyShown);
        Assert.Equal(Unified.Key, row.Folder);
        Assert.Equal(FolderTree.RoleIcon(FolderRole.Inbox), row.Icon);
        // The blank line keeps the row as tall as the pinned folders' rows.
        Assert.Equal(" ", row.Subtitle);
        Assert.Equal("All Inboxes, 5", row.ToString());
        row.Update(new FolderEntry { IsUnified = true, Favourite = true, Badge = 0 }, several: false);
        Assert.Equal("", row.Badge);
        Assert.Equal("", row.Subtitle);
    }

    [Fact]
    public void TheModelListCoversItsInboxes()
    {
        Account[] accounts = [Acc("a", AccountKind.Imap, true), Acc("b", AccountKind.Graph, true)];
        var folders = new FolderMap
        {
            ["a"] = [Inbox("a", "a-in", 2, 10), Inbox("a", "a-sent", 0, 1) with { Role = FolderRole.Sent }],
            ["b"] = [Inbox("b", "b-in", 3, 5)],
        };
        var model = new MailModel(accounts, folders) { UnifiedInbox = true };
        Assert.True(model.UnifiedShown);
        model.ListFolder = new FolderKey("a", "a-in");
        Assert.True(model.ListCovers(new FolderKey("a", "a-in")));
        Assert.False(model.ListCovers(new FolderKey("b", "b-in")));
        model.ListFolder = Unified.Key;
        Assert.True(model.ListIsUnified);
        Assert.Equal(FolderRole.Inbox, model.FolderRole(Unified.Key));
        Assert.True(model.ListCovers(new FolderKey("b", "b-in")));
        Assert.True(model.ListCovers(new FolderKey("a", "a-in")));
        Assert.False(model.ListCovers(new FolderKey("a", "a-sent")));
        // The item stays listed while the sidebar has it.
        Assert.True(model.FolderListed(Unified.Key));
        model.UnifiedInbox = false;
        Assert.False(model.FolderListed(Unified.Key));
        // The badge follows the counts.
        model.UnifiedInbox = true;
        model.RebuildEntries();
        Assert.Equal(5, model.Entries[0].Badge);
        model.AdjustCounts(new FolderKey("b", "b-in"), -2, 0);
        Assert.Equal(3, model.Entries[0].Badge);
    }

    [Fact]
    public void ThreadIdsAreScopedByAccount()
    {
        var a = ThreadScope.Scoped("acc1", "conv");
        var b = ThreadScope.Scoped("acc2", "conv");
        Assert.NotEqual(a, b);
        Assert.Equal(new ThreadId("conv"), ThreadScope.Raw(a));
        Assert.Equal(new ThreadId("plain"), ThreadScope.Raw(new ThreadId("plain")));
        var s = new MessageSummary
        {
            Id = "m1",
            AccountId = "acc1",
            FolderId = "in",
            ThreadId = new ThreadId("conv"),
            Subject = "",
            Snippet = "",
            Date = System.DateTimeOffset.UnixEpoch,
            HasAttachments = false,
            Size = 0,
        };
        var scoped = ThreadScope.ScopeMessage(s);
        Assert.Equal(a, scoped.ThreadId);
        Assert.Equal(scoped, ThreadScope.ScopeMessage(scoped));
    }
}
