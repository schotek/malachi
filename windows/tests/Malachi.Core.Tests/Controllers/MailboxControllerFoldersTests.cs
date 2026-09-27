// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/MailboxControllerFoldersTests.swift
// (every test): the folder half of the main window over the controller
// (ui/internal/window/folders.go, collapse.go, favourites.go and the
// sidebar parts of notify.go and window.go), exercised against MailFixture.
// The GTK window code has no tests of these flows.
//
// What changes: the tests wait for quiescence instead of polling and
// sleeping, and the fixture's delays run on a FakeTimeProvider. The status
// line is the IMailboxSync double (MailboxControllerTestSync), whose footer
// is SyncController's (so Swift's footer assertions stay), while Swift's
// assertions on the connection half of the line and on the sign-in
// banner's texts become assertions on the connection and the notification
// the mailbox handed over (those texts are SyncController's and are tested
// with it). The last tests are Windows-only: the sidebar as keyed
// snapshots, the highlighted row as a key, current when the rows arrive,
// and a badge applied in place (docs/windows-port.md §7.5).

using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using System.Collections.Specialized;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Model;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Xunit;
using static Malachi.Core.Tests.Model.CollapseStateTests;
using static Malachi.Core.Tests.Model.MailModelTests;
using FolderMap = System.Collections.Generic.Dictionary<Malachi.Core.Api.AccountId, System.Collections.Generic.IReadOnlyList<Malachi.Core.Api.Folder>>;

namespace Malachi.Core.Tests.Controllers;

public sealed class MailboxControllerFoldersTests
{
    private static readonly FolderKey Inbox1 = new("acc1", "inbox");

    [Fact]
    public async Task LoadsAccountsAndFoldersAndSelectsTheInbox()
    {
        var (accounts, folders) = TestAccounts();
        var log = new Log();
        await using var h = await StartAsync(log, accounts, folders);
        await h.IdleAsync();
        Assert.Equal(new SidebarStatus.Status("", "Loading…", ""), log.Statuses[0]);
        Assert.Equal(1, log.Rebuilds);

        // Two enabled accounts: a header each, the disabled one left out.
        var entries = h.Mailbox.Model.Entries;
        Assert.Equal(2, entries.Count(e => e.Header));
        Assert.Equal(["inbox", "trash", "alpha", "sub1", "deep", "sub2", "container", "leaf", "zeta", "in2"], FolderIds(entries));
        Assert.True(h.Mailbox.HasAccounts);
        Assert.Equal(1, log.AccountsLoaded);
        Assert.Equal(new SidebarStatus.Folders(), log.Statuses[^1]);
        Assert.Equal(new SidebarStatus.Folders(), h.Mailbox.SidebarStatus);

        // The first Inbox is selected, announced, highlighted and listed once.
        Assert.Equal(Inbox1, h.Mailbox.Model.Selected);
        Assert.Equal([Inbox1], log.Selections);
        Assert.Equal(Inbox1, log.Highlights[^1].Key);
        Assert.Equal(1, log.Reloads);
        Assert.Equal("Inbox", h.Mailbox.SelectedFolderTitle);
        // folder.list once per enabled account, after account.list.
        Assert.Equal(1, h.Fixture.CallCount(API.AccountList.Name));
        Assert.Equal(2, h.Fixture.CallCount(API.FolderList.Name));
        Assert.Equal(1, h.Fixture.CallCount(API.SyncStatus.Name));
        Assert.Empty(h.Toasts);
        // The footer follows the account.list states (all idle), and the
        // line knows the connection.
        Assert.Equal("Up to date", h.Sync.Footer.Text);
        Assert.Equal(new ConnectionState.Connected(MailboxControllerHarness.Info), h.Sync.Connection);
        // The window title: the Inbox, without counts (it has no total).
        Assert.Equal(new ListHeading("Inbox", ""), log.Titles[^1]);
    }

    [Fact]
    public async Task SingleAccountHasNoHeadersUntilAFavourite()
    {
        var (accounts, folders) = NestedAccount();
        var log = new Log();
        await using var h = await StartAsync(log, accounts, folders);
        await h.IdleAsync();
        Assert.Equal(1, log.Rebuilds);
        Assert.DoesNotContain(h.Mailbox.Model.Entries, e => e.Header);
        Assert.Equal(["in", "work", "bugs", "old", "zulu"], FolderIds(h.Mailbox.Model.Entries));
        Assert.Equal(new FolderKey("a", "in"), h.Mailbox.Model.Selected);

        // Pinning a folder adds the Favourites section and, above the tree,
        // the account's heading.
        await h.On(() => h.Mailbox.ToggleFavourite(new FolderKey("a", "zulu")));
        var entries = h.Mailbox.Model.Entries;
        Assert.Equal(8, entries.Count);
        Assert.True(entries[0].Header && entries[0].Favourite);
        Assert.True(entries[1].Favourite && entries[1].Folder?.Id == "zulu");
        Assert.True(entries[2].Header && entries[2].Account?.Id == "a");
        Assert.Equal(["a/zulu"], h.Settings.FavouriteFolders);
        // Pinning is not navigating: the selection and the list stayed.
        Assert.Equal(new FolderKey("a", "in"), h.Mailbox.Model.Selected);
        Assert.Equal(1, log.Reloads);

        await h.On(() => h.Mailbox.ToggleFavourite(new FolderKey("a", "zulu")));
        Assert.Equal(5, h.Mailbox.Model.Entries.Count);
        Assert.Empty(h.Settings.FavouriteFolders);
    }

    [Fact]
    public async Task FolderListFailureShowsTheStatusPage()
    {
        var (accounts, folders) = NestedAccount();
        var log = new Log();
        await using var h = await StartAsync(log, accounts, folders, connect: false);
        h.Fixture.Fail(API.FolderList.Name, new RpcError { Code = ErrorCode.NetworkError, Message = "down" });
        await h.ConnectAsync();
        await h.IdleAsync();
        Assert.Equal(1, log.Rebuilds);
        Assert.Empty(h.Mailbox.Model.Entries);
        Assert.Equal(
            new SidebarStatus.Status("dialog-warning-symbolic", "Folders Unavailable", "Loading folders failed: the server could not be reached"),
            log.Statuses[^1]);
        Assert.Null(h.Mailbox.Model.Selected);
        Assert.True(log.Reloads == 0, "nothing was selected, nothing to clear");
        Assert.True(h.Toasts.Count == 0, "a folder.list failure is a status page, not a toast");

        // A later success replaces the page; the last good list is kept
        // over a transient failure after that.
        h.Fixture.Succeed(API.FolderList.Name);
        await h.On(() => h.Mailbox.LoadFolders("a", h.Mailbox.Model.FoldersGen));
        await h.IdleAsync();
        Assert.Equal(2, log.Rebuilds);
        Assert.Equal(new SidebarStatus.Folders(), log.Statuses[^1]);
        Assert.Equal(new FolderKey("a", "in"), h.Mailbox.Model.Selected);
        h.Fixture.Fail(API.FolderList.Name, new RpcError { Code = ErrorCode.ServerError, Message = "500" });
        await h.On(() => h.Mailbox.LoadFolders("a", h.Mailbox.Model.FoldersGen));
        await h.IdleAsync();
        Assert.Equal(3, log.Rebuilds);
        Assert.Equal(["in", "work", "bugs", "old", "zulu"], FolderIds(h.Mailbox.Model.Entries));
        Assert.True(h.Mailbox.Model.FolderErr.ContainsKey("a"));
        Assert.Equal(new SidebarStatus.Folders(), log.Statuses[^1]);
    }

    [Fact]
    public async Task AccountListFailureShowsTheStatusPage()
    {
        var log = new Log();
        await using var h = await StartAsync(log, [], new FolderMap(), connect: false);
        h.Fixture.Fail(API.AccountList.Name, new RpcError { Code = ErrorCode.InternalError, Message = "boom" });
        await h.ConnectAsync();
        await h.IdleAsync();
        Assert.Equal(
            [
                new SidebarStatus.Status("", "Loading…", ""),
                new SidebarStatus.Status("dialog-warning-symbolic", "Folders Unavailable", "Loading folders failed"),
            ],
            log.Statuses);
        Assert.Equal(0, log.Rebuilds);
        Assert.False(h.Mailbox.HasAccounts);
    }

    [Fact]
    public async Task EmptySidebarStatuses()
    {
        // No accounts at all.
        var noneLog = new Log();
        await using (var none = await StartAsync(noneLog, [], new FolderMap()))
        {
            await none.IdleAsync();
            Assert.Equal(1, noneLog.Rebuilds);
            Assert.Equal(
                new SidebarStatus.Status("system-users-symbolic", "No Accounts", "Add a mail account in Preferences to see its folders here."),
                noneLog.Statuses[^1]);
            Assert.Equal(0, noneLog.Reloads);
            Assert.True(none.Sync.Footer.Text.Length == 0, "no enabled account: no sync line");
        }

        // Accounts, none enabled.
        var pausedLog = new Log();
        await using (var paused = await StartAsync(pausedLog, [TestAccount("a", enabled: false)], new FolderMap()))
        {
            await paused.IdleAsync();
            Assert.Equal(1, pausedLog.Rebuilds);
            Assert.Equal(
                new SidebarStatus.Status("system-users-symbolic", "No Enabled Accounts", "Enable an account in Preferences to see its folders here."),
                pausedLog.Statuses[^1]);
            Assert.Equal(0, paused.Fixture.CallCount(API.FolderList.Name));
        }

        // An enabled account that has not synchronised yet.
        var freshLog = new Log();
        await using (var fresh = await StartAsync(freshLog, [TestAccount("a")], new FolderMap { ["a"] = [] }))
        {
            await fresh.IdleAsync();
            Assert.Equal(1, freshLog.Rebuilds);
            Assert.Equal(
                new SidebarStatus.Status("folder-symbolic", "No Folders Yet", "Folders appear after the first synchronisation."),
                freshLog.Statuses[^1]);
        }
    }

    [Fact]
    public async Task AccountsChangedReloadsEverything()
    {
        var (accounts, folders) = TestAccounts();
        var log = new Log();
        await using var h = await StartAsync(log, accounts, folders);
        await h.IdleAsync();
        Assert.Equal(1, log.Rebuilds);

        // The banner is up for an account that is then removed; the mailbox
        // hands the status line the listed account with the notification
        // (Swift: "No password is stored for two@example.invalid").
        await h.On(() => h.Mailbox.HandleAuthRequired(new AuthRequiredNotification { AccountId = "acc2", Reason = ErrorCode.AuthRequired, Message = "x" }));
        Assert.Equal("acc2", h.Sync.Banners[^1].Account);
        Assert.Equal("acc2", h.Sync.Banners[^1].Known?.Id);
        Assert.Equal(ErrorCode.AuthRequired, h.Sync.Banners[^1].Notification!.Reason.Value);

        h.Fixture.SetAccounts([accounts[0]]);
        await h.On(h.Mailbox.HandleAccountsChanged);
        await h.IdleAsync();
        Assert.Equal(2, log.Rebuilds);
        Assert.True(h.Sync.Banners[^1].Account is null, "accountsChanged hides the banner");
        Assert.Single(h.Mailbox.Model.Accounts);
        Assert.True(!h.Mailbox.Model.Entries.Any(e => e.Header), "one account: no header");
        Assert.Equal(Inbox1, h.Mailbox.Model.Selected);
        Assert.True(log.Reloads == 1, "the selected folder survived; the list is not reloaded");
        Assert.Equal(2, h.Fixture.CallCount(API.AccountList.Name));
        Assert.Equal(2, log.AccountsLoaded);

        // The selected account switched off: the selection moves to the
        // initial folder of what is left.
        var a2 = accounts[1] with { Enabled = true };
        h.Fixture.SetAccounts([TestAccount("acc1", enabled: false, email: "one@example.invalid"), a2]);
        await h.On(h.Mailbox.HandleAccountsChanged);
        await h.IdleAsync();
        Assert.Equal(3, log.Rebuilds);
        Assert.Equal(new FolderKey("acc2", "in2"), h.Mailbox.Model.Selected);
        Assert.Equal(2, log.Reloads);
        Assert.Equal(new FolderKey("acc2", "in2"), log.Selections[^1]);
    }

    [Fact]
    public async Task SyncFinishedReloadsFoldersAndTheSelectedList()
    {
        var (accounts, folders) = TestAccounts();
        var log = new Log();
        await using var h = await StartAsync(log, accounts, folders);
        await h.IdleAsync();
        var folderLists = h.Fixture.CallCount(API.FolderList.Name);

        // Syncing → idle for the selected folder: folders and list reload.
        await h.On(() => h.Mailbox.HandleSyncState(new SyncState { AccountId = "acc1", Status = SyncStatus.Syncing, FolderId = "inbox" }));
        Assert.Equal(("Syncing Inbox…", true), h.Sync.Footer);
        Assert.Equal(1, log.Reloads);
        await h.On(() => h.Mailbox.HandleSyncState(new SyncState { AccountId = "acc1", Status = SyncStatus.Idle, FolderId = "inbox" }));
        await h.IdleAsync();
        Assert.Equal(2, log.Rebuilds);
        Assert.Equal(folderLists + 1, h.Fixture.CallCount(API.FolderList.Name));
        Assert.Equal(2, log.Reloads);
        Assert.Equal("Up to date", h.Sync.Footer.Text);

        // Another folder of the account: folders reload, the list does not.
        await h.On(() =>
        {
            h.Mailbox.HandleSyncState(new SyncState { AccountId = "acc1", Status = SyncStatus.Syncing, FolderId = "zeta" });
            h.Mailbox.HandleSyncState(new SyncState { AccountId = "acc1", Status = SyncStatus.Idle, FolderId = "zeta" });
        });
        await h.IdleAsync();
        Assert.Equal(3, log.Rebuilds);
        Assert.Equal(2, log.Reloads);

        // The whole account: the list reloads too.
        await h.On(() =>
        {
            h.Mailbox.HandleSyncState(new SyncState { AccountId = "acc1", Status = SyncStatus.Syncing });
            h.Mailbox.HandleSyncState(new SyncState { AccountId = "acc1", Status = SyncStatus.Idle });
        });
        await h.IdleAsync();
        Assert.Equal(4, log.Rebuilds);
        Assert.Equal(3, log.Reloads);

        // Another account, or idle without a preceding syncing: nothing.
        await h.On(() =>
        {
            h.Mailbox.HandleSyncState(new SyncState { AccountId = "acc2", Status = SyncStatus.Syncing });
            h.Mailbox.HandleSyncState(new SyncState { AccountId = "acc2", Status = SyncStatus.Idle });
            h.Mailbox.HandleSyncState(new SyncState { AccountId = "acc1", Status = SyncStatus.Idle });
        });
        await h.IdleAsync();
        Assert.Equal(5, log.Rebuilds);
        Assert.Equal(3, log.Reloads);

        // The outbox count moved: folders reload and the outbox views refresh.
        await h.On(() => h.Mailbox.HandleSyncState(new SyncState { AccountId = "acc1", Status = SyncStatus.Idle, PendingOutbox = 1 }));
        await h.IdleAsync();
        Assert.Equal(["acc1"], log.OutboxRefreshes);
        Assert.Equal(6, log.Rebuilds);
        Assert.Equal(("Sending 1 message…", true), h.Sync.Footer);
    }

    [Fact]
    public async Task DeliveredOutboxMessagesAreToasted()
    {
        var outbox = TestFolder("out", "Outbox", FolderRole.Outbox, total: 2);
        var inbox = TestFolder("in", "INBOX", FolderRole.Inbox);
        var log = new Log();
        await using var h = await StartAsync(log, [TestAccount("a")], new FolderMap { ["a"] = [inbox, outbox] });
        await h.IdleAsync();
        Assert.Equal(1, log.Rebuilds);
        Assert.Equal(["in", "out"], FolderIds(h.Mailbox.Model.Entries));

        // Both delivered: the outbox empties, its row goes, a toast says so.
        h.Fixture.SetFolders([inbox, TestFolder("out", "Outbox", FolderRole.Outbox, total: 0)], "a");
        await h.On(() =>
        {
            h.Mailbox.HandleSyncState(new SyncState { AccountId = "a", Status = SyncStatus.Idle, PendingOutbox = 0 });
            h.Mailbox.HandleSyncState(new SyncState { AccountId = "a", Status = SyncStatus.Idle, PendingOutbox = 2 });
            h.Mailbox.HandleSyncState(new SyncState { AccountId = "a", Status = SyncStatus.Idle, PendingOutbox = 0 });
        });
        await h.IdleAsync();
        Assert.Equal(2, log.OutboxRefreshes.Count);
        Assert.Equal(["2 messages sent"], h.Toasts);
        Assert.Equal(["in"], FolderIds(h.Mailbox.Model.Entries));

        // A cancelled send is not a delivery.
        h.Fixture.SetFolders([inbox, TestFolder("out", "Outbox", FolderRole.Outbox, total: 1)], "a");
        await h.On(() => h.Mailbox.OnOutboxChanged("a"));
        await h.IdleAsync();
        Assert.Equal(3, log.OutboxRefreshes.Count);
        h.Fixture.SetFolders([inbox, TestFolder("out", "Outbox", FolderRole.Outbox, total: 0)], "a");
        await h.On(() =>
        {
            h.Mailbox.NoteOutboxCancelled("a");
            h.Mailbox.OnOutboxChanged("a");
        });
        await h.IdleAsync();
        Assert.Equal(4, log.OutboxRefreshes.Count);
        Assert.Equal(["2 messages sent"], h.Toasts);
    }

    [Fact]
    public async Task TriggerSyncSendsTheSelectedFolder()
    {
        var (accounts, folders) = TestAccounts();
        var log = new Log();
        await using var h = await StartAsync(log, accounts, folders);
        await h.IdleAsync();

        // The line checks at once; the daemon's notify.syncState (or the
        // status line's own fallback, tested with SyncController) takes over.
        Assert.Equal(("Checking for new mail…", true), await h.On(() =>
        {
            h.Mailbox.TriggerSync();
            return h.Sync.Footer;
        }));
        await h.IdleAsync();
        Assert.Empty(h.Toasts);
        Assert.Equal([new SyncTriggerParams { AccountId = "acc1", FolderId = "inbox" }], h.Fixture.Triggers);

        // Nothing selected: every account.
        h.Fixture.SetAccounts([]);
        await h.On(h.Mailbox.HandleAccountsChanged);
        await h.IdleAsync();
        Assert.Equal(2, log.Rebuilds);
        Assert.Null(h.Mailbox.Model.Selected);
        await h.On(h.Mailbox.TriggerSync);
        await h.IdleAsync();
        Assert.Equal(2, h.Fixture.Triggers.Count);
        Assert.Equal(new SyncTriggerParams(), h.Fixture.Triggers[^1]);

        // A refused trigger: the line is recomputed and a toast explains.
        h.Fixture.Fail(API.SyncTrigger.Name, new RpcError { Code = ErrorCode.NotImplemented, Message = "no syncer" });
        Assert.True(await h.On(() =>
        {
            h.Mailbox.TriggerSync();
            return h.Sync.Footer.Spinning;
        }));
        await h.IdleAsync();
        Assert.Equal(["Checking for new mail is not available yet"], h.Toasts);
        Assert.True(h.Sync.Footer == ("", false), "no enabled account: empty line");
        Assert.Equal(3, h.Sync.Checking);
    }

    [Fact]
    public async Task FoldsPersistAndFollowExternalChanges()
    {
        var (accounts, folders) = TestAccounts();
        var log = new Log();
        await using var h = await StartAsync(log, accounts, folders);
        await h.IdleAsync();
        var alpha = new FolderKey("acc1", "alpha");

        await h.On(() => h.Mailbox.ToggleFolder(alpha));
        Assert.Equal(["acc1/alpha"], h.Settings.CollapsedFolders);
        Assert.Equal(["inbox", "trash", "alpha", "container", "leaf", "zeta", "in2"], FolderIds(h.Mailbox.Model.Entries));
        Assert.True(log.Rebuilds == 2, "our own write must not rebuild twice");
        Assert.True(h.Mailbox.Model.Collapsed.FolderCollapsed(alpha));

        // (Windows: the answer says whether Left or Right did anything, as
        // collapse.go addFolderShortcuts does.)
        Assert.False(await h.On(() => h.Mailbox.SetFolderCollapsed(alpha, true)));
        Assert.True(log.Rebuilds == 2, "already collapsed: a no-op");
        Assert.True(await h.On(() => h.Mailbox.SetFolderCollapsed(alpha, false)));
        Assert.Equal(3, log.Rebuilds);
        Assert.Empty(h.Settings.CollapsedFolders);

        await h.On(() => h.Mailbox.ToggleAccount("acc2"));
        Assert.Equal(["acc2"], h.Settings.CollapsedAccounts);
        Assert.Equal("zeta", FolderIds(h.Mailbox.Model.Entries)[^1]);
        Assert.True(h.Mailbox.Model.Entries[^1].Header, "a folded account leaves its header behind");
        Assert.False(await h.On(() => h.Mailbox.SetAccountCollapsed("acc2", true)), "already folded");
        Assert.Equal(4, log.Rebuilds);

        // Another window folded something: the sidebar follows.
        await h.On(() =>
        {
            h.Settings.CollapsedFolders = ["acc1/alpha", "acc1/bogus"];
            h.Settings.CollapsedAccounts = [];
        });
        Assert.True(h.Mailbox.Model.Collapsed.FolderCollapsed(alpha));
        Assert.False(h.Mailbox.Model.Collapsed.AccountCollapsed("acc2"));
        Assert.Equal(["inbox", "trash", "alpha", "container", "leaf", "zeta", "in2"], FolderIds(h.Mailbox.Model.Entries));
        Assert.Equal(6, log.Rebuilds);

        // Closed: external changes are ignored.
        await h.On(() =>
        {
            h.Mailbox.Close();
            h.Settings.CollapsedFolders = [];
        });
        Assert.Equal(6, log.Rebuilds);
    }

    [Fact]
    public async Task FavouritesPersistAndFollowExternalChanges()
    {
        var (accounts, folders) = TestAccounts();
        var log = new Log();
        await using var h = await StartAsync(log, accounts, folders);
        await h.IdleAsync();
        var in2 = new FolderKey("acc2", "in2");

        await h.On(() => h.Mailbox.ToggleFavourite(in2));
        Assert.Equal(["acc2/in2"], h.Settings.FavouriteFolders);
        var entries = h.Mailbox.Model.Entries;
        Assert.True(entries[0].Header && entries[0].Favourite);
        Assert.True(entries[1].Favourite && entries[1].Key == in2);
        Assert.True(entries[^1].Starred, "the tree row is starred too");
        Assert.Equal(2, log.Rebuilds);

        // Clicking the row in the Favourites section remembers that side.
        await h.On(() => h.Mailbox.SelectFolder(in2, fav: true));
        Assert.True(h.Mailbox.Model.SelectedFav);
        Assert.Equal(in2, log.Highlights[^1].Key);
        Assert.True(log.Highlights[^1].Favourite);
        Assert.Equal(2, log.Reloads);
        // Re-selecting the listed folder only re-highlights it.
        await h.On(() => h.Mailbox.SelectFolder(in2, fav: false));
        Assert.Equal(2, log.Reloads);
        Assert.False(log.Highlights[^1].Favourite);
        Assert.Equal(2, log.Selections.Count);

        await h.On(() =>
        {
            h.Settings.FavouriteFolders = ["acc1/inbox", "garbage"];
        });
        Assert.True(h.Mailbox.Model.Favourites.Has(Inbox1));
        Assert.False(h.Mailbox.Model.Favourites.Has(in2));
        Assert.Equal(Inbox1, h.Mailbox.Model.Entries[1].Key);
        Assert.True(h.Mailbox.Model.Selected == in2, "unpinning is not navigating");
        Assert.Equal(3, log.Rebuilds);
    }

    [Fact]
    public async Task FoldingKeepsASelectionOutOfSight()
    {
        var (accounts, folders) = NestedAccount();
        var log = new Log();
        await using var h = await StartAsync(log, accounts, folders);
        await h.IdleAsync();
        var old = new FolderKey("a", "old");
        var work = new FolderKey("a", "work");

        await h.On(() => h.Mailbox.SelectFolder(old, fav: false));
        Assert.Equal(2, log.Reloads);
        Assert.Equal("old", h.Mailbox.SelectedFolderTitle);
        await h.On(() => h.Mailbox.ToggleFolder(work));
        Assert.Equal(["in", "work", "zulu"], FolderIds(h.Mailbox.Model.Entries));
        Assert.True(h.Mailbox.Model.Selected == old, "folding must not move the selection");
        Assert.True(log.Reloads == 2, "nor reload the list");
        Assert.Equal(old, log.Highlights[^1].Key);
        // The folded row's badge counts what it hides.
        Assert.Equal(14, EntryById(h.Mailbox.Model.Entries, "work")?.Badge);

        // The folder gone from the server: the initial folder takes over.
        h.Fixture.SetFolders([.. NestedFolders().Take(3)], "a");
        await h.On(() => h.Mailbox.LoadFolders("a", h.Mailbox.Model.FoldersGen));
        await h.IdleAsync();
        Assert.Equal(3, log.Rebuilds);
        Assert.Equal(new FolderKey("a", "in"), h.Mailbox.Model.Selected);
        Assert.Equal(3, log.Reloads);

        // Only containers left: nothing selected, the list cleared.
        h.Fixture.SetFolders([TestFolder("c", "c", selectable: false)], "a");
        await h.On(() => h.Mailbox.LoadFolders("a", h.Mailbox.Model.FoldersGen));
        await h.IdleAsync();
        Assert.Equal(4, log.Rebuilds);
        Assert.Null(h.Mailbox.Model.Selected);
        Assert.True(log.Selections[^1] is null, "the cleared selection is announced as null");
        Assert.Equal(4, log.Reloads);
        Assert.Equal("Messages", h.Mailbox.SelectedFolderTitle);
    }

    [Fact]
    public async Task NewMessageAdjustsTheBadgeOnce()
    {
        var (accounts, folders) = TestAccounts();
        var log = new Log();
        await using var h = await StartAsync(log, accounts, folders);
        await h.IdleAsync();
        Assert.Equal(2, EntryById(h.Mailbox.Model.Entries, "inbox")?.Badge);

        // For another folder than the listed one: only the counts move, the
        // total always, the badge only for an unseen message.
        var zeta = new FolderKey("acc1", "zeta");
        await h.On(() => h.Mailbox.HandleNewMessage(new NewMessageNotification { AccountId = "acc1", FolderId = "zeta", Message = Summary("m1") }));
        Assert.Equal(1, EntryById(h.Mailbox.Model.Entries, "zeta")?.Badge);
        Assert.Equal(1, h.Mailbox.Model.Folder(zeta)?.Unread);
        Assert.Equal(1, h.Mailbox.Model.Folder(zeta)?.Total);
        Assert.Equal(1, log.BadgeRefreshes);
        Assert.Empty(log.ListInserts);
        await h.On(() => h.Mailbox.HandleNewMessage(new NewMessageNotification { AccountId = "acc1", FolderId = "zeta", Message = Summary("m2", Flag.Seen) }));
        Assert.Equal(1, EntryById(h.Mailbox.Model.Entries, "zeta")?.Badge);
        Assert.True(h.Mailbox.Model.Folder(zeta)?.Total == 2, "a read message counts in the total");
        Assert.Equal(2, log.BadgeRefreshes);

        // For the listed folder the list gets it first (with the ids filled
        // in); once it holds the message a second delivery changes nothing.
        await h.On(() =>
        {
            h.Mailbox.OnNewMessageForList = n =>
            {
                log.ListInserts.Add(n);
                h.Mailbox.Model.InsertMessage(0, n.Message);
            };
        });
        var s = Summary("m3");
        await h.On(() => h.Mailbox.HandleNewMessage(new NewMessageNotification { AccountId = "acc1", FolderId = "inbox", Message = s }));
        Assert.Single(log.ListInserts);
        Assert.Equal("acc1", log.ListInserts[^1].Message.AccountId);
        Assert.Equal("inbox", log.ListInserts[^1].Message.FolderId);
        Assert.Equal(3, EntryById(h.Mailbox.Model.Entries, "inbox")?.Badge);
        await h.On(() => h.Mailbox.HandleNewMessage(new NewMessageNotification { AccountId = "acc1", FolderId = "inbox", Message = s }));
        Assert.Single(log.ListInserts);
        Assert.Equal(3, EntryById(h.Mailbox.Model.Entries, "inbox")?.Badge);
        Assert.Equal(3, log.BadgeRefreshes);
        Assert.Equal(1, h.Mailbox.Model.Folder(Inbox1)?.Total);

        // The actions' own adjustments go through the same refresh.
        await h.On(() => h.Mailbox.AdjustCounts(Inbox1, -3, 0));
        Assert.Equal(0, EntryById(h.Mailbox.Model.Entries, "inbox")?.Badge);
        Assert.Equal(4, log.BadgeRefreshes);
        Assert.Equal(1, h.Mailbox.Model.Folder(Inbox1)?.Total);
    }

    [Fact]
    public async Task NotificationsArriveThroughTheSocket()
    {
        var (accounts, folders) = TestAccounts();
        var log = new Log();
        await using var h = await StartAsync(log, accounts, folders);
        await h.IdleAsync();
        var pumped = 0;
        // Routes the daemon's notifications into the controller, as the app
        // does from the connection controller.
        await h.On(() =>
        {
            _ = PumpAsync();
        });
        async Task PumpAsync()
        {
            await foreach (var raw in h.Client.Notifications.ReadAllAsync())
            {
                DaemonNotification? n = null;
                try
                {
                    n = raw.Decode();
                }
                catch (System.Text.Json.JsonException)
                {
                }
                if (n is not null)
                {
                    h.Mailbox.HandleNotification(n);
                }
                Interlocked.Increment(ref pumped);
            }
        }
        Task Settled() => h.IdleAsync(() => Volatile.Read(ref pumped) == h.Fixture.Daemon.Pushed);

        await h.Fixture.PushAsync(new DaemonNotification.SyncState(new SyncState { AccountId = "acc2", Status = SyncStatus.Syncing, FolderId = "in2", Progress = 10 }));
        await Settled();
        Assert.Equal("Syncing Inbox… 10 %", h.Sync.Footer.Text);
        await h.Fixture.PushAsync(new DaemonNotification.NewMessage(new NewMessageNotification { AccountId = "acc2", FolderId = "in2", Message = Summary("n1") }));
        await Settled();
        Assert.Equal(1, EntryById(h.Mailbox.Model.Entries, "in2")?.Badge);
        await h.Fixture.PushAsync(new DaemonNotification.AuthRequired(new AuthRequiredNotification { AccountId = "acc1", Reason = ErrorCode.KeyringError, Message = "x" }));
        await Settled();
        // Swift: "The system keyring is unavailable; one@example.invalid
        // cannot sign in", "Open Preferences" (SyncController's texts).
        Assert.Single(h.Sync.Banners);
        Assert.Equal("acc1", h.Sync.Banners[^1].Known?.Id);
        Assert.Equal(ErrorCode.KeyringError, h.Sync.Banners[^1].Notification!.Reason.Value);
        await h.Fixture.PushAsync(new DaemonNotification.Unknown("notify.future"));
        await h.Fixture.PushAsync(new DaemonNotification.AccountsChanged());
        await Settled();
        Assert.Equal(2, log.Rebuilds);
        Assert.Null(h.Sync.Banners[^1].Account);
        Assert.Equal(2, h.Fixture.CallCount(API.AccountList.Name));
    }

    [Fact]
    public async Task BackendUnavailableDropsLateReplies()
    {
        var (accounts, folders) = TestAccounts();
        var log = new Log();
        await using var h = await StartAsync(log, accounts, folders, connect: false);
        h.Fixture.Delay(API.FolderList.Name, TimeSpan.FromMilliseconds(200));
        await h.ConnectAsync();
        // account.list answered; both folder.list answers wait on the clock.
        await h.DelayedAsync(2);
        Assert.Equal(1, await h.On(() => log.AccountsLoaded));
        var gen = await h.On(() => h.Mailbox.Model.FoldersGen);
        await h.On(() =>
        {
            h.Mailbox.Model.Grouped = true;
            h.Mailbox.HandleConnection(new ConnectionState.Unavailable("gone"));
        });
        Assert.Equal(gen + 1, h.Mailbox.Model.FoldersGen);
        Assert.Equal(1, log.Collapses);
        Assert.Equal(new ConnectionState.Unavailable("gone"), h.Sync.Connection);
        await h.AdvanceAsync(TimeSpan.FromMilliseconds(400));
        Assert.True(log.Rebuilds == 0, "the folder.list replies of the dead connection were dropped");
        Assert.Empty(h.Mailbox.Model.Folders);
        Assert.Equal(new SidebarStatus.Status("", "Loading…", ""), log.Statuses[^1]);

        // The reconnect loads again, as the GTK window does on Connected; a
        // failed system.info still loads.
        h.Fixture.Delay(API.FolderList.Name, TimeSpan.Zero);
        await h.On(() => h.Mailbox.HandleConnection(new ConnectionState.InfoFailed("x")));
        await h.IdleAsync();
        Assert.Equal(1, log.Rebuilds);
        Assert.Equal(Inbox1, h.Mailbox.Model.Selected);
    }

    /// <summary>
    /// A daemon of another protocol version is no connection (the handshake
    /// refused it): nothing is loaded, late replies are dropped as when the
    /// backend went away, and the line names the mismatch. The client is
    /// connected here all the same, so a load would reach the daemon.
    /// </summary>
    [Fact]
    public async Task ProtocolMismatchLoadsNothing()
    {
        var (accounts, folders) = TestAccounts();
        var log = new Log();
        await using var h = await StartAsync(log, accounts, folders, connect: false);
        await h.Client.ConnectAsync(TestContext.Current.CancellationToken);
        var gen = await h.On(() => h.Mailbox.Model.FoldersGen);
        await h.On(() =>
        {
            h.Mailbox.Model.Grouped = true;
            h.Mailbox.HandleConnection(new ConnectionState.ProtocolMismatch(1));
        });
        Assert.Equal(gen + 1, h.Mailbox.Model.FoldersGen);
        Assert.Equal(1, log.Collapses);
        Assert.Equal(new ConnectionState.ProtocolMismatch(1), h.Sync.Connection);
        await h.IdleAsync();
        Assert.True(h.Fixture.CallCount(API.AccountList.Name) == 0, "account.list is not asked");
        Assert.True(h.Fixture.CallCount(API.SyncStatus.Name) == 0, "sync.status is not asked");
        Assert.True(log.AccountsLoaded == 0 && log.Rebuilds == 0);
    }

    /// <summary>
    /// window.go <c>refreshListTitle</c>: the selected folder's name over its
    /// counts, from every place the selection or the cached counts change
    /// (selectFolder in both branches, updateFolderRow, rebuildFolderList).
    /// </summary>
    [Fact]
    public async Task ListTitleFollowsTheSelectionAndTheCounts()
    {
        var inbox = TestFolder("in", "INBOX", FolderRole.Inbox, unread: 2, total: 10);
        var archive = TestFolder("arch", "Archive", FolderRole.Archive, total: 3);
        var outbox = TestFolder("out", "Outbox", FolderRole.Outbox, total: 1);
        var log = new Log();
        await using var h = await StartAsync(log, [TestAccount("a")], new FolderMap { ["a"] = [inbox, archive, outbox] });
        await h.IdleAsync();
        Assert.Equal(new ListHeading("Inbox", "2 unread of 10"), log.Titles[^1]);
        Assert.Equal("2 unread of 10", h.Mailbox.SelectedFolderSubtitle);

        var arch = new FolderKey("a", "arch");
        await h.On(() => h.Mailbox.SelectFolder(arch, fav: false));
        Assert.Equal(new ListHeading("Archive", "3 messages"), log.Titles[^1]);
        // Selecting it again only re-highlights, and says the title again.
        var said = log.Titles.Count;
        await h.On(() => h.Mailbox.SelectFolder(arch, fav: false));
        Assert.Equal(said + 1, log.Titles.Count);
        Assert.Equal(new ListHeading("Archive", "3 messages"), log.Titles[^1]);

        // A new message, and the actions' bookkeeping.
        await h.On(() => h.Mailbox.HandleNewMessage(new NewMessageNotification { AccountId = "a", FolderId = "arch", Message = Summary("n1") }));
        Assert.Equal(new ListHeading("Archive", "1 unread of 4"), log.Titles[^1]);
        await h.On(() => h.Mailbox.MoveCounts(arch, new FolderKey("a", "in"), 1, 2));
        Assert.Equal(new ListHeading("Archive", "2 messages"), log.Titles[^1]);
        Assert.Equal(12, h.Mailbox.Model.Folder(new FolderKey("a", "in"))?.Total);

        // The outbox counts what is to be sent.
        await h.On(() => h.Mailbox.SelectFolder(new FolderKey("a", "out"), fav: false));
        Assert.Equal(new ListHeading("Outbox", "1 message"), log.Titles[^1]);

        // Nothing selected any more: "Messages" and no counts.
        h.Fixture.SetAccounts([]);
        await h.On(h.Mailbox.HandleAccountsChanged);
        await h.IdleAsync();
        Assert.Equal(2, log.Rebuilds);
        Assert.Equal(new ListHeading("Messages", ""), log.Titles[^1]);
        Assert.Equal("", h.Mailbox.SelectedFolderSubtitle);
        Assert.Equal(log.Titles[^1], h.Mailbox.ListHeading);
    }

    /// <summary>
    /// sync.go <c>applySyncState</c>: a change of failedOutbox reloads the
    /// outbox as a change of pendingOutbox does; an account's first state is
    /// no move (its folders came with the accounts).
    /// </summary>
    [Fact]
    public async Task FailedOutboxChangesReloadTheOutbox()
    {
        var inbox = TestFolder("in", "INBOX", FolderRole.Inbox);
        var outbox = TestFolder("out", "Outbox", FolderRole.Outbox, total: 1);
        var log = new Log();
        await using var h = await StartAsync(log, [TestAccount("a")], new FolderMap { ["a"] = [inbox, outbox] });
        await h.IdleAsync();

        await h.On(() => h.Mailbox.HandleSyncState(new SyncState { AccountId = "a", Status = SyncStatus.Idle, FailedOutbox = 1 }));
        Assert.Equal(("1 message not sent", false), h.Sync.Footer);
        await h.IdleAsync();
        Assert.True(log.OutboxRefreshes.Count == 0, "the first state is no move");

        await h.On(() => h.Mailbox.HandleSyncState(new SyncState { AccountId = "a", Status = SyncStatus.Idle, FailedOutbox = 2 }));
        await h.IdleAsync();
        Assert.Equal(["a"], log.OutboxRefreshes);
        Assert.Equal("2 messages not sent", h.Sync.Footer.Text);

        // Unchanged counts: nothing.
        await h.On(() => h.Mailbox.HandleSyncState(new SyncState { AccountId = "a", Status = SyncStatus.Syncing, FailedOutbox = 2 }));
        await h.IdleAsync();
        Assert.Equal(["a"], log.OutboxRefreshes);
        // A failed message dropped: pending stays 0, failed moves.
        await h.On(() => h.Mailbox.HandleSyncState(new SyncState { AccountId = "a", Status = SyncStatus.Syncing, FailedOutbox = 0 }));
        await h.IdleAsync();
        Assert.Equal(["a", "a"], log.OutboxRefreshes);
    }

    /// <summary>
    /// sync.go <c>triggerAccountSync</c>: Check and Try Again in the status
    /// popover ask for one whole account.
    /// </summary>
    [Fact]
    public async Task TriggerSyncForOneAccount()
    {
        var (accounts, folders) = TestAccounts();
        var log = new Log();
        await using var h = await StartAsync(log, accounts, folders);
        await h.IdleAsync();

        Assert.Equal(("Checking for new mail…", true), await h.On(() =>
        {
            h.Mailbox.TriggerSync("acc2");
            return h.Sync.Footer;
        }));
        await h.IdleAsync();
        Assert.Equal([new SyncTriggerParams { AccountId = "acc2" }], h.Fixture.Triggers);
    }

    /// <summary>
    /// status.go <c>showOutbox</c>: the popover's link to unsent messages
    /// selects the account's outbox like a click on its tree row; a paused
    /// account's outbox, or a missing one, is not shown.
    /// </summary>
    [Fact]
    public async Task ShowOutboxSelectsTheOutbox()
    {
        var inbox = TestFolder("in", "INBOX", FolderRole.Inbox);
        var outbox = TestFolder("out", "Outbox", FolderRole.Outbox, total: 1);
        Account[] accounts = [TestAccount("a"), TestAccount("p", enabled: false), TestAccount("n")];
        var log = new Log();
        await using var h = await StartAsync(
            log, accounts, new FolderMap { ["a"] = [inbox, outbox], ["p"] = [outbox], ["n"] = [TestFolder("in2", "INBOX", FolderRole.Inbox)] });
        await h.IdleAsync();
        Assert.Equal(new FolderKey("a", "in"), h.Mailbox.Model.Selected);
        await h.On(() => h.Mailbox.SelectFolder(new FolderKey("a", "in"), fav: true));

        Assert.True(await h.On(() => h.Mailbox.ShowOutbox("a")));
        Assert.Equal(new FolderKey("a", "out"), h.Mailbox.Model.Selected);
        Assert.True(!h.Mailbox.Model.SelectedFav, "the tree's row, not a pinned one");
        Assert.Equal(2, log.Reloads);

        Assert.True(!await h.On(() => h.Mailbox.ShowOutbox("p")), "paused");
        Assert.True(!await h.On(() => h.Mailbox.ShowOutbox("n")), "no outbox");
        Assert.True(!await h.On(() => h.Mailbox.ShowOutbox("zzz")), "unknown account");
        Assert.Equal(new FolderKey("a", "out"), h.Mailbox.Model.Selected);
    }

    /// <summary>
    /// Windows-only (docs/windows-port.md §7.5): the sidebar is published as
    /// snapshots whose rows keep their keys, and the highlighted row is a key
    /// that follows the side clicked last, falls back to the other row of a
    /// pinned folder, and is none while the folder is folded out of sight;
    /// the observable properties announce every change.
    /// </summary>
    [Fact]
    public async Task SidebarIsKeyedAndItsHighlightIsAKey()
    {
        var (accounts, folders) = NestedAccount();
        var log = new Log();
        await using var h = await StartAsync(log, accounts, folders);
        await h.IdleAsync();
        var changed = new List<string>();
        await h.On(() => h.Mailbox.PropertyChanged += (_, e) => changed.Add(e.PropertyName!));
        var inKey = new FolderKey("a", "in");
        var old = new FolderKey("a", "old");

        Assert.Same(h.Mailbox.Model.Entries, h.Mailbox.Entries);
        Assert.Equal(SidebarKey.ForFolder(inKey, false), h.Mailbox.SelectedEntryKey);
        Assert.Equal(h.Mailbox.Entries.Count, h.Mailbox.Entries.Select(SidebarKey.Of).Distinct().Count());

        // Pinned and clicked in the section: the section's row; the keys of
        // the tree's rows stay as they were.
        var before = h.Mailbox.Entries.Select(SidebarKey.Of).ToHashSet();
        await h.On(() =>
        {
            h.Mailbox.ToggleFavourite(inKey);
            h.Mailbox.SelectFolder(inKey, fav: true);
        });
        var keys = h.Mailbox.Entries.Select(SidebarKey.Of).ToList();
        Assert.Equal(keys.Count, keys.Distinct().Count());
        Assert.Subset(keys.ToHashSet(), before);
        Assert.Contains(new SidebarKey(true, true, null, null), keys);
        Assert.Contains(new SidebarKey(true, false, "a", null), keys);
        Assert.Equal(SidebarKey.ForFolder(inKey, true), h.Mailbox.SelectedEntryKey);
        Assert.Contains(nameof(MailboxController.Entries), changed);
        Assert.Contains(nameof(MailboxController.SelectedEntryKey), changed);

        // Unpinned: the tree's row stands in.
        await h.On(() => h.Mailbox.ToggleFavourite(inKey));
        Assert.Equal(SidebarKey.ForFolder(inKey, false), h.Mailbox.SelectedEntryKey);

        // Folded out of sight: no row carries the selection, which stays.
        await h.On(() => h.Mailbox.SelectFolder(old, fav: false));
        Assert.Equal(SidebarKey.ForFolder(old, false), h.Mailbox.SelectedEntryKey);
        await h.On(() => h.Mailbox.ToggleFolder(new FolderKey("a", "work")));
        Assert.Null(h.Mailbox.SelectedEntryKey);
        Assert.Equal(old, h.Mailbox.Model.Selected);
        await h.On(() => h.Mailbox.ToggleFolder(new FolderKey("a", "work")));
        Assert.Equal(SidebarKey.ForFolder(old, false), h.Mailbox.SelectedEntryKey);

        // A badge moved: a new snapshot, the same keys.
        var snapshot = h.Mailbox.Entries;
        changed.Clear();
        await h.On(() => h.Mailbox.AdjustCounts(old, 0, 3));
        Assert.NotSame(snapshot, h.Mailbox.Entries);
        Assert.Equal(snapshot.Select(SidebarKey.Of), h.Mailbox.Entries.Select(SidebarKey.Of));
        Assert.Equal([nameof(MailboxController.Entries), nameof(MailboxController.ListHeading)], changed);
        Assert.Equal(new ListHeading("old", "8 unread of 3"), h.Mailbox.ListHeading);
    }

    /// <summary>
    /// Windows-only (docs/windows-port.md §7.5): when
    /// <see cref="MailboxController.EntriesChanged"/> arrives,
    /// <see cref="MailboxController.SelectedEntryKey"/> is already the row
    /// the rebuild leaves highlighted, listed in the new
    /// <see cref="MailboxController.Entries"/> (or null), as
    /// <see cref="ListController.SelectedKey"/> is with the rows; the
    /// announcements of the selection follow as before.
    /// </summary>
    [Fact]
    public async Task EntriesArriveWithTheirHighlight()
    {
        var (accounts, folders) = NestedAccount();
        var log = new Log();
        await using var h = await StartAsync(log, accounts, folders);
        await h.IdleAsync();
        var seen = new List<SidebarKey?>();
        await h.On(() => h.Mailbox.EntriesChanged += (_, _) =>
        {
            var k = h.Mailbox.SelectedEntryKey;
            Assert.True(k is null || h.Mailbox.Entries.Any(e => SidebarKey.Of(e) == k), $"{k} is not listed");
            seen.Add(k);
        });
        var inKey = new FolderKey("a", "in");
        var zulu = new FolderKey("a", "zulu");
        var old = new FolderKey("a", "old");

        // Unpinned while its row in the section carries the highlight: the
        // tree's row carries it by the time the rows arrive.
        await h.On(() =>
        {
            h.Mailbox.ToggleFavourite(inKey);
            h.Mailbox.SelectFolder(inKey, fav: true);
        });
        var highlights = log.Highlights.Count;
        await h.On(() => h.Mailbox.ToggleFavourite(inKey));
        Assert.Equal(SidebarKey.ForFolder(inKey, false), seen[^1]);
        Assert.Equal(seen[^1], h.Mailbox.SelectedEntryKey);
        Assert.Equal(new FolderSelection(inKey, true), Assert.Single(log.Highlights.Skip(highlights)));

        // Folded out of sight: none.
        await h.On(() => h.Mailbox.SelectFolder(old, fav: false));
        await h.On(() => h.Mailbox.ToggleFolder(new FolderKey("a", "work")));
        Assert.Null(seen[^1]);
        await h.On(() => h.Mailbox.ToggleFolder(new FolderKey("a", "work")));
        Assert.Equal(SidebarKey.ForFolder(old, false), seen[^1]);

        // The selected folder went away: the initial folder's row, which
        // the rebuild then selects.
        await h.On(() => h.Mailbox.SelectFolder(zulu, fav: false));
        var selections = log.Selections.Count;
        h.Fixture.SetFolders([.. NestedFolders().Where(f => f.Id != zulu.Folder)], "a");
        await h.On(() => h.Mailbox.LoadFolders("a", h.Mailbox.Model.FoldersGen));
        await h.IdleAsync();
        Assert.Equal(SidebarKey.ForFolder(inKey, false), seen[^1]);
        Assert.Equal(seen[^1], h.Mailbox.SelectedEntryKey);
        Assert.Equal([inKey], log.Selections.Skip(selections));
        Assert.Equal(new FolderSelection(inKey, false), log.Highlights[^1]);

        // No folder left: none, and the selection is cleared.
        h.Fixture.SetFolders([], "a");
        await h.On(() => h.Mailbox.LoadFolders("a", h.Mailbox.Model.FoldersGen));
        await h.IdleAsync();
        Assert.Null(seen[^1]);
        Assert.Null(h.Mailbox.Model.Selected);
        Assert.Equal(new FolderSelection(null, false), log.Highlights[^1]);
    }

    /// <summary>
    /// Windows-only (docs/windows-port.md §7.5): a badge that moves (a new
    /// message, a mark-as-read) is a new snapshot of new records under the
    /// same keys. Applied through the view overload of
    /// <see cref="KeyedListSync"/>, the highlighted row keeps its view
    /// object, updated in place, and the collection raises nothing, so a
    /// ListView keeps its selection; the record overload would replace the
    /// row, which a WinUI selector takes for a removal and an insertion.
    /// </summary>
    [Fact]
    public async Task BadgesUpdateTheHighlightedRowInPlace()
    {
        var (accounts, folders) = NestedAccount();
        var log = new Log();
        await using var h = await StartAsync(log, accounts, folders);
        await h.IdleAsync();
        var inKey = new FolderKey("a", "in");
        var view = new ObservableCollection<EntryView>();
        var records = new ObservableCollection<FolderEntry>();
        var actions = new List<NotifyCollectionChangedAction>();
        EntryView? selected = null;
        await h.On(() =>
        {
            Apply(view, h.Mailbox.Entries);
            KeyedListSync.Apply(records, h.Mailbox.Entries, SidebarKey.Of);
            selected = view.Single(v => v.Key == h.Mailbox.SelectedEntryKey);
            view.CollectionChanged += (_, e) => actions.Add(e.Action);
            h.Mailbox.BadgesChanged += (_, _) => Apply(view, h.Mailbox.Entries);
        });
        Assert.Equal(SidebarKey.ForFolder(inKey, false), selected!.Key);
        Assert.Equal(1, selected.Entry.Badge);

        await h.On(() => h.Mailbox.HandleNewMessage(new NewMessageNotification { AccountId = "a", FolderId = "in", Message = Summary("n1") }));
        Assert.Empty(actions);
        Assert.Same(selected, view.Single(v => v.Key == h.Mailbox.SelectedEntryKey));
        Assert.Equal(2, selected.Entry.Badge);
        Assert.Equal(1, log.BadgeRefreshes);

        // What the record overload would have done to the same change.
        var replaced = await h.On(() => KeyedListSync.Apply(records, h.Mailbox.Entries, SidebarKey.Of));
        Assert.Equal(new KeyedListChanges(0, 0, 0, 1), replaced);

        static void Apply(ObservableCollection<EntryView> target, IReadOnlyList<FolderEntry> entries) =>
            KeyedListSync.Apply(target, entries, SidebarKey.Of, v => v.Key, e => new EntryView(e), (v, e) => v.Entry = e);
    }

    /// <summary>
    /// Windows-only: the window's <c>callThen</c> (actions.go; Swift
    /// <c>call</c>), which the actions reach through the mailbox: a success
    /// goes to its handler, a failure is toasted in the words of
    /// <c>rpcErrorText</c> and handed on; once the mailbox closed, neither.
    /// </summary>
    [Fact]
    public async Task CallToastsAFailureAndHandsItOn()
    {
        var (accounts, folders) = TestAccounts();
        var log = new Log();
        await using var h = await StartAsync(log, accounts, folders);
        await h.IdleAsync();
        var errors = new List<Exception>();
        var answers = new List<ConfigGetResult>();
        void Ask() => h.Mailbox.Call(API.ConfigGet, new EmptyParams(), "Loading the settings", errors.Add, answers.Add);

        await h.On(Ask);
        await h.IdleAsync();
        Assert.Equal(30, Assert.Single(answers).Preferences.OfflineDays);
        Assert.Empty(errors);
        Assert.Empty(h.Toasts);

        h.Fixture.Fail(API.ConfigGet.Name, new RpcError { Code = ErrorCode.StorageError, Message = "disk" });
        await h.On(Ask);
        await h.IdleAsync();
        Assert.Single(answers);
        Assert.Equal(ErrorCode.StorageError, Assert.IsType<RpcException>(Assert.Single(errors)).Code.Value);
        Assert.Equal(["Loading the settings failed"], h.Toasts);

        await h.On(() =>
        {
            Ask();
            h.Mailbox.Close();
        });
        await h.IdleAsync();
        Assert.Single(errors);
        Assert.Single(h.Toasts);
        Assert.True(h.Sync.Closed, "the mailbox closes its status line");
    }

    private static List<string> FolderIds(IEnumerable<FolderEntry> entries) =>
        [.. entries.Where(e => !e.Header && e.Folder is not null).Select(e => e.Folder!.Id.Value)];

    private static Task<MailboxControllerHarness> StartAsync(
        Log log, IReadOnlyList<Account> accounts, IReadOnlyDictionary<AccountId, IReadOnlyList<Folder>> folders, bool connect = true) =>
        MailboxControllerHarness.StartAsync(
            f =>
            {
                f.SetAccounts(accounts);
                f.SetFolders(folders);
            },
            wire: h => Wire(h, log),
            connect: connect);

    // What the controller emitted, in order (Swift's Log and the callbacks
    // the Harness installs).
    private static void Wire(MailboxControllerHarness h, Log log)
    {
        var mailbox = h.Mailbox;
        mailbox.EntriesChanged += (_, _) => log.Rebuilds++;
        mailbox.BadgesChanged += (_, _) => log.BadgeRefreshes++;
        mailbox.FolderStatusChanged += (_, s) => log.Statuses.Add(s);
        mailbox.SelectionChanged += (_, s) => log.Highlights.Add(s);
        mailbox.FolderSelected += (_, s) => log.Selections.Add(s.Key);
        mailbox.ReloadMessages = () =>
        {
            // The list half's loadMessages, as far as the folder half sees it.
            mailbox.Model.ListFolder = mailbox.Model.Selected;
            log.Reloads++;
        };
        mailbox.AccountsLoaded += (_, _) => log.AccountsLoaded++;
        mailbox.OnNewMessageForList = n => log.ListInserts.Add(n);
        mailbox.RefreshOutboxViews = a => log.OutboxRefreshes.Add(a);
        mailbox.CollapseLoading = () => log.Collapses++;
        mailbox.ListTitleChanged += (_, t) => log.Titles.Add(t);
    }

    /// <summary>A sidebar row's view model, updated in place (docs/windows-port.md §7.5).</summary>
    private sealed class EntryView(FolderEntry entry)
    {
        public SidebarKey Key { get; } = SidebarKey.Of(entry);

        public FolderEntry Entry { get; set; } = entry;
    }

    /// <summary>What the controller emitted, in order.</summary>
    private sealed class Log
    {
        public List<SidebarStatus> Statuses { get; } = [];

        public int Rebuilds { get; set; }

        public int BadgeRefreshes { get; set; }

        public List<FolderSelection> Highlights { get; } = [];

        public List<FolderKey?> Selections { get; } = [];

        public int Reloads { get; set; }

        public int AccountsLoaded { get; set; }

        public List<NewMessageNotification> ListInserts { get; } = [];

        public List<AccountId> OutboxRefreshes { get; } = [];

        public int Collapses { get; set; }

        /// <summary>The title and subtitle per ListTitleChanged.</summary>
        public List<ListHeading> Titles { get; } = [];
    }
}
