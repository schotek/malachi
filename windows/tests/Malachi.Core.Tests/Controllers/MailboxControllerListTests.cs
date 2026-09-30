// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/MailboxControllerListTests.swift
// (the MailboxControllerListTests suite, every test; its second suite,
// ListSearchTests, is MailboxControllerListSearchTests.cs): the
// message-list half of the main window over the list controller
// (ui/internal/window/messages.go, threads.go, the list part of folders.go
// onNewMessage and the selection handling of window.go and actions.go),
// exercised against MailFixture. The GTK window code has no tests of these
// flows.
//
// What changes: the tests wait for quiescence instead of polling, and the
// fixture's delays and the mark-as-read delay run on a FakeTimeProvider
// (Swift shortens the delay's tick to 20 ms and sleeps). The controllers
// live on the tests' UI thread, which goes on working after a call
// returns, so what Swift reads right after a call is read in the same UI
// turn here. The last two tests are Windows-only: the rows as keyed
// snapshots with the selection as a key, and a flag change applied in place
// (docs/windows-port.md §7.5).

using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using System.Collections.Specialized;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Model;
using Xunit;
using static Malachi.Core.Tests.Model.MailModelTests;

namespace Malachi.Core.Tests.Controllers;

public sealed class MailboxControllerListTests
{
    internal static readonly AccountId Acc = "a";
    internal static readonly FolderKey Inbox = new("a", "in");
    internal static readonly FolderKey Trash = new("a", "trash");
    internal static readonly FolderKey Empty = new("a", "empty");
    internal static readonly FolderKey Outbox = new("a", "out");

    /// <summary>2026-09-01T10:00:00Z.</summary>
    internal static readonly DateTimeOffset Base = new(2026, 9, 1, 10, 0, 0, TimeSpan.Zero);

    [Fact]
    public async Task FlatListingPagesFiftyAtATime()
    {
        var all = Enumerable.Range(1, 120).Select(i => Msg($"m{i}", i)).ToArray();
        var log = new ListLog();
        await using var h = await StartAsync(log, messages: new() { [Inbox] = all }, connect: false);
        // Nothing selected at first, then the page loads.
        Assert.Equal(
            new ListState.Status("folder-symbolic", "Select a folder", "Choose a folder in the sidebar to see its messages.", false),
            h.List.ListState);
        await h.ConnectAsync();
        await h.IdleAsync();
        Assert.Equal(new ListState.Messages(), h.List.ListState);
        Assert.Contains(new ListState.Status("", "Loading…", "", false), log.States);
        Assert.Equal(50, h.List.Rows.Count);
        Assert.Equal("m120", h.List.Rows[0].Message.Id.Value);
        Assert.Equal("m71", h.List.Rows[^1].Message.Id.Value);
        Assert.Equal(120, h.Mailbox.Model.Total);
        Assert.Equal("50", h.Mailbox.Model.NextCursor);
        Assert.Equal(new LoadMoreState { Button = true }, h.List.LoadMoreState);
        var first = h.Fixture.ListRequests[0];
        Assert.Equal(
            new MessageListParams { AccountId = "a", FolderId = "in", Page = new Page { Limit = 50 }, Sort = SortOrder.DateDesc, Filter = MessageFilter.All },
            first);
        Assert.Single(h.Fixture.ListRequests);
        Assert.Null(h.List.SelectedKey);
        Assert.Empty(log.Selections);

        // The second page appends; the spinner shows while it is fetched.
        Assert.Equal(new LoadMoreState { Spinner = true }, await h.On(() =>
        {
            h.List.LoadMore();
            return h.List.LoadMoreState;
        }));
        await h.IdleAsync();
        Assert.Equal(100, h.List.Rows.Count);
        Assert.Equal("50", h.Fixture.ListRequests[^1].Page.Cursor);
        Assert.Equal(new LoadMoreState { Button = true }, h.List.LoadMoreState);
        Assert.Equal("m70", h.List.Rows[50].Message.Id.Value);

        // A message notified meanwhile sits at the top of the list and in
        // the third page too: the page skips it.
        var late = Msg("m15", 15);
        await h.On(() => h.Mailbox.HandleNewMessage(new NewMessageNotification { AccountId = "a", FolderId = "in", Message = late }));
        Assert.Equal(101, h.List.Rows.Count);
        Assert.Equal("m15", h.List.Rows[0].Message.Id.Value);
        Assert.Equal(121, h.Mailbox.Model.Total);
        await h.On(h.List.LoadMore);
        await h.IdleAsync();
        Assert.Equal(120, h.List.Rows.Count);
        Assert.Equal(120, h.List.Rows.Select(r => r.Message.Id).Distinct().Count());
        Assert.Equal(120, h.Mailbox.Model.Total);
        Assert.Null(h.Mailbox.Model.NextCursor);
        Assert.Equal(new LoadMoreState(), h.List.LoadMoreState);

        // The last page is shown: nothing more to ask for.
        await h.On(h.List.LoadMore);
        await h.IdleAsync();
        Assert.Equal(3, h.Fixture.ListRequests.Count);
        Assert.Empty(h.Toasts);

        // A failed page keeps the cursor and the footer offers a retry.
        await h.On(() =>
        {
            h.Mailbox.Model.NextCursor = "100";
            h.List.ShowLoadMore();
        });
        h.Fixture.Fail(API.MessageList.Name, new RpcError { Code = ErrorCode.ServerError, Message = "500" });
        await h.On(h.List.LoadMore);
        await h.IdleAsync();
        Assert.Equal(["Loading more messages failed: the server returned an error"], h.Toasts);
        Assert.Equal(new LoadMoreState { Button = true }, h.List.LoadMoreState);
        Assert.Equal(120, h.List.Rows.Count);
    }

    [Fact]
    public async Task FilterChangeRepagesFromTheStartAndPersistsAcrossFolders()
    {
        var all = Enumerable.Range(1, 10).Select(i => Msg($"m{i}", i, flags: i is 2 or 5 or 8 ? [] : [Flag.Seen])).ToArray();
        var log = new ListLog();
        await using var h = await StartAsync(log, messages: new() { [Inbox] = all, [Trash] = [Msg("t1", 1, flags: [Flag.Seen])] });
        await h.IdleAsync();
        Assert.Equal(10, h.List.Rows.Count);
        await h.On(() => h.List.Select(new ListKey(Message: "m9")));
        Assert.Equal(["m9"], Ids(log.Selections));

        // The rows and the selection go at once; the folder is paged again
        // with the new filter.
        var (rows, hint, key, announced, state) = await h.On(() =>
        {
            h.List.SetListFilter(MessageFilter.Unread);
            return (h.List.Rows.Count, log.RowEvents[^1], h.List.SelectedKey, log.Selections[^1], h.List.ListState);
        });
        Assert.Equal(0, rows);
        Assert.Equal(SelectionHint.Clear, hint);
        Assert.Null(key);
        Assert.Null(announced);
        Assert.Equal(new ListState.Status("", "Loading…", "", false), state);
        await h.IdleAsync();
        Assert.Equal(["m8", "m5", "m2"], Ids(h.List.Rows));
        var req = h.Fixture.ListRequests[^1];
        Assert.Equal(MessageFilter.Unread, req.Filter?.Value);
        Assert.Null(req.Page.Cursor);
        Assert.Equal(2, h.Fixture.ListRequests.Count);

        // An unchanged filter (the filter bar's own write-back) is a no-op;
        // an empty one is "all".
        await h.On(() => h.List.SetListFilter(MessageFilter.Unread));
        await h.IdleAsync();
        Assert.Equal(2, h.Fixture.ListRequests.Count);
        await h.On(() => h.List.SetListFilter(new MessageFilter("")));
        await h.IdleAsync();
        Assert.Equal(10, h.List.Rows.Count);
        Assert.Equal(MessageFilter.All, h.Fixture.ListRequests[^1].Filter?.Value);

        // No flagged messages: the page says so.
        await h.On(() => h.List.SetListFilter(MessageFilter.Flagged));
        await h.IdleAsync();
        Assert.Equal(
            new ListState.Status("starred-symbolic", "No Flagged Messages", "No message in this folder carries a flag.", false),
            h.List.ListState);

        // The filter is global: another folder is listed under it.
        await h.On(() => h.Mailbox.SelectFolder(Trash, fav: false));
        await h.IdleAsync();
        Assert.Equal("trash", h.Fixture.ListRequests[^1].FolderId.Value);
        Assert.Equal(MessageFilter.Flagged, h.Fixture.ListRequests[^1].Filter?.Value);
        Assert.IsType<ListState.Status>(h.List.ListState);
    }

    [Fact]
    public async Task StatusPagesFollowThePrecedenceTable()
    {
        // Only a container: nothing to select.
        await using (var bare = await StartAsync(new ListLog(), folders: [TestFolder("c", "c", selectable: false)]))
        {
            await bare.IdleAsync();
            Assert.Single(bare.Mailbox.Model.Entries);
            Assert.Null(bare.Mailbox.Model.Selected);
            Assert.Equal(
                new ListState.Status("folder-symbolic", "Select a folder", "Choose a folder in the sidebar to see its messages.", false),
                bare.List.ListState);
            Assert.Empty(bare.Fixture.ListRequests);
        }

        // A folder the daemon never downloads is not asked for.
        var allMail = TestFolder("all", "All Mail", FolderRole.All, synced: false);
        var log = new ListLog();
        await using var h = await StartAsync(log, folders: [.. TestFolders(), allMail], messages: new() { [Inbox] = [Msg("m1", 1)] }, connect: false);
        h.Fixture.Fail(API.MessageList.Name, new RpcError { Code = ErrorCode.NetworkError, Message = "down" });
        await h.ConnectAsync();
        // The first load fails: an error page with Try Again.
        await h.IdleAsync();
        Assert.NotNull(h.Mailbox.Model.ListErr);
        Assert.Equal(
            new ListState.Status("dialog-warning-symbolic", "Messages Unavailable", "Loading messages failed: the server could not be reached", true),
            h.List.ListState);
        Assert.Equal(new LoadMoreState(), h.List.LoadMoreState);
        Assert.True(h.Toasts.Count == 0, "the first failure is a page, not a toast");

        // Try Again.
        h.Fixture.Succeed(API.MessageList.Name);
        await h.On(h.List.Retry);
        await h.IdleAsync();
        Assert.Single(h.List.Rows);

        // A failed reload of the same folder keeps the rows and toasts once.
        h.Fixture.Fail(API.MessageList.Name, new RpcError { Code = ErrorCode.NetworkError, Message = "down" });
        var (kept, keptState) = await h.On(() =>
        {
            h.Mailbox.ReloadMessages!();
            return (h.List.Rows.Count, h.List.ListState);
        });
        Assert.True(kept == 1, "a same-folder reload keeps the rows until the reply");
        Assert.Equal(new ListState.Messages(), keptState);
        await h.IdleAsync();
        Assert.Equal(["Loading messages failed: the server could not be reached"], h.Toasts);
        Assert.Single(h.List.Rows);
        Assert.Equal(new ListState.Messages(), h.List.ListState);
        h.Fixture.Succeed(API.MessageList.Name);

        // Not synchronised.
        var (unsyncedRows, unsynced) = await h.On(() =>
        {
            h.Mailbox.SelectFolder(new FolderKey("a", "all"), fav: false);
            return (h.List.Rows.Count, h.List.ListState);
        });
        Assert.Equal(0, unsyncedRows);
        Assert.Equal(
            new ListState.Status(
                "folder-download-symbolic",
                "Not Synchronised",
                "Messages moved here are archived on the server; the folder itself is not downloaded.",
                false),
            unsynced);
        await h.IdleAsync();
        var requests = h.Fixture.ListRequests.Count;

        // Loading, then empty for the filter.
        var delayed = h.DelayedAnswers;
        h.Fixture.Delay(API.MessageList.Name, TimeSpan.FromMilliseconds(150));
        Assert.Equal(new ListState.Status("", "Loading…", "", false), await h.On(() =>
        {
            h.Mailbox.SelectFolder(Empty, fav: false);
            return h.List.ListState;
        }));
        await h.DelayedAsync(delayed + 1);
        await h.AdvanceAsync(TimeSpan.FromMilliseconds(150));
        Assert.Equal(requests + 1, h.Fixture.ListRequests.Count);
        Assert.Equal(new ListState.Status("mail-unread-symbolic", "No Messages", "This folder is empty.", false), h.List.ListState);
        h.Fixture.Delay(API.MessageList.Name, TimeSpan.Zero);
        await h.On(() => h.List.SetListFilter(MessageFilter.Unread));
        await h.IdleAsync();
        Assert.Equal(
            new ListState.Status("mail-read-symbolic", "No Unread Messages", "Everything in this folder has been read.", false),
            h.List.ListState);
        await h.On(() => h.List.SetListFilter(MessageFilter.Flagged));
        await h.IdleAsync();
        Assert.Equal(
            new ListState.Status("starred-symbolic", "No Flagged Messages", "No message in this folder carries a flag.", false),
            h.List.ListState);
    }

    [Fact]
    public async Task GroupedListingFoldsAndFetchesMembers()
    {
        var log = new ListLog();
        await using var h = await StartAsync(log, messages: new() { [Inbox] = ThreadedMessages() }, grouped: true);
        await h.IdleAsync();
        Assert.True(h.Mailbox.Model.Grouped);
        Assert.Empty(h.Fixture.ListRequests);
        Assert.Equal(
            new ThreadListParams { AccountId = "a", FolderId = "in", Page = new Page { Limit = 50 }, Sort = SortOrder.DateDesc, Filter = MessageFilter.All },
            h.Fixture.ThreadListRequests[0]);
        Assert.Equal(["T:t3", "b1", "T:t1"], Ids(h.List.Rows));
        var t3 = h.List.Rows[0];
        Assert.Equal("c2", t3.Message.Id.Value);
        Assert.Equal(2, t3.Summary?.MessageCount);
        Assert.Equal(1, t3.Summary?.UnreadCount);
        Assert.True(!t3.Expanded && !t3.Loading);
        Assert.Equal(new ListKey("t2", "b1"), h.List.Rows[1].Key);
        Assert.True(!h.List.Rows[1].Thread && !h.List.Rows[1].Member);

        // Unfolding asks for the members; the row spins meanwhile.
        Assert.True(await h.On(() =>
        {
            h.List.ToggleThread("t1");
            return h.List.Rows[2].Expanded && h.List.Rows[2].Loading;
        }));
        await h.IdleAsync();
        Assert.Equal(6, h.List.Rows.Count);
        Assert.Equal([new ThreadGetParams { AccountId = "a", ThreadId = "t1", FolderId = "in" }], h.Fixture.ThreadGetRequests);
        Assert.Equal(["T:t3", "b1", "T:t1", "a1", "a2", "a3"], Ids(h.List.Rows));
        Assert.False(h.List.Rows[2].Loading);
        Assert.True(h.List.Rows[3].Member && h.List.Rows[4].Member && h.List.Rows[5].Member);
        Assert.Equal(new ListKey("t1", "a1"), h.List.Rows[3].Key);

        // Folding and unfolding again uses what is known.
        await h.On(() => h.List.ToggleThread("t1"));
        Assert.Equal(["T:t3", "b1", "T:t1"], Ids(h.List.Rows));
        Assert.False(await h.On(() => h.List.SetThreadExpanded("t1", false)), "already folded: the key passes");
        Assert.True(await h.On(() => h.List.SetThreadExpanded("t1", true)));
        Assert.Equal(6, h.List.Rows.Count);
        await h.IdleAsync();
        Assert.Single(h.Fixture.ThreadGetRequests);

        // Activation folds a conversation row and opens a message row.
        await h.On(() => h.List.Activate(new ListKey("t1")));
        Assert.Equal(3, h.List.Rows.Count);
        await h.On(() => h.List.Activate(new ListKey("t2", "b1")));
        Assert.Equal(["b1"], log.Activations.Select(id => id.Value));

        // A failed fetch folds the row back and says why.
        h.Fixture.Fail(API.ThreadGet.Name, new RpcError { Code = ErrorCode.StorageError, Message = "disk" });
        Assert.True(await h.On(() =>
        {
            h.List.ToggleThread("t3");
            return h.List.Rows[0].Loading;
        }));
        await h.IdleAsync();
        Assert.Equal(["Loading the conversation failed"], h.Toasts);
        Assert.True(!h.List.Rows[0].Expanded && !h.List.Rows[0].Loading);
        Assert.Equal(3, h.List.Rows.Count);
    }

    [Fact]
    public async Task ReloadKeepsFetchedMembersOfTheSameShape()
    {
        var log = new ListLog();
        await using var h = await StartAsync(log, messages: new() { [Inbox] = ThreadedMessages() }, grouped: true);
        await h.IdleAsync();
        await h.On(() => h.List.ToggleThread("t1"));
        await h.IdleAsync();
        Assert.Equal(6, h.List.Rows.Count);
        await h.On(() => h.List.Select(new ListKey("t1", "a2")));
        Assert.Equal("a2", log.Selections[^1]?.Value);

        // A reload of the same folder (a sync finished) keeps the members
        // and the selection; no second thread.get.
        Assert.True(await h.On(() =>
        {
            h.Mailbox.ReloadMessages!();
            return h.List.Rows.Count;
        }) == 6, "the rows stay until the reply");
        await h.IdleAsync();
        Assert.Equal(2, h.Fixture.ThreadListRequests.Count);
        Assert.Equal(6, h.List.Rows.Count);
        Assert.True(h.List.Rows[2].Expanded && !h.List.Rows[2].Loading);
        Assert.Single(h.Fixture.ThreadGetRequests);
        Assert.Equal(new ListKey("t1", "a2"), h.List.SelectedKey);
        Assert.True(log.Selections.Count == 1, "a kept selection is not announced again");

        // The conversation changed shape: the members are fetched again.
        h.Fixture.AddMessage(Msg("a4", 7, from: "gina", thread: "t1"));
        await h.On(() => h.Mailbox.ReloadMessages!());
        await h.IdleAsync();
        Assert.Equal(2, h.Fixture.ThreadGetRequests.Count);
        Assert.Equal(7, h.List.Rows.Count);
        Assert.Equal(["T:t1", "a1", "a2", "a3", "a4", "T:t3", "b1"], Ids(h.List.Rows));
        // While the members were on their way the member row was gone: its
        // conversation row took the selection over (and the pane showed the
        // newest member); it keeps it once the members are back.
        Assert.Equal(new ListKey("t1"), h.List.SelectedKey);
        Assert.Equal(["a2", "a4"], Ids(log.Selections));
    }

    [Fact]
    public async Task NewMessagesJoinTheList()
    {
        var log = new ListLog();
        await using var h = await StartAsync(log, messages: new() { [Inbox] = [Msg("m1", 1, flags: [Flag.Seen]), Msg("m2", 2, flags: [Flag.Seen])] });
        await h.IdleAsync();
        await h.On(() => h.List.Select(new ListKey(Message: "m2")));

        // Flat: prepended, the selection untouched, the badge adjusted by
        // the folder half. The fixture gets it too, for the reloads below.
        h.Fixture.AddMessage(Msg("m3", 3));
        await h.On(() => h.Mailbox.HandleNewMessage(New(Msg("m3", 3))));
        Assert.Equal(["m3", "m2", "m1"], Ids(h.List.Rows));
        Assert.Equal(new ListKey(Message: "m2"), h.List.SelectedKey);
        Assert.Equal(["m2"], Ids(log.Selections));
        Assert.Equal(3, h.Mailbox.Model.Total);
        Assert.Equal(1, h.Mailbox.Model.Folder(Inbox)?.Unread);

        // Not for another folder; not twice.
        await h.On(() =>
        {
            h.Mailbox.HandleNewMessage(new NewMessageNotification { AccountId = "a", FolderId = "trash", Message = Msg("t1", 3) });
            h.Mailbox.HandleNewMessage(New(Msg("m3", 3)));
        });
        Assert.Equal(3, h.List.Rows.Count);

        // Not when the filter would not list it.
        await h.On(() => h.List.SetListFilter(MessageFilter.Unread));
        await h.IdleAsync();
        Assert.Equal(["m3"], Ids(h.List.Rows));
        await h.On(() => h.Mailbox.HandleNewMessage(New(Msg("m4", 4, flags: [Flag.Seen]))));
        Assert.Equal(["m3"], Ids(h.List.Rows));
        h.Fixture.AddMessage(Msg("m5", 5));
        await h.On(() => h.Mailbox.HandleNewMessage(New(Msg("m5", 5))));
        Assert.Equal(["m5", "m3"], Ids(h.List.Rows));

        // Not while the page is loading: the reply will include it.
        var delayed = h.DelayedAnswers;
        h.Fixture.Delay(API.MessageList.Name, TimeSpan.FromMilliseconds(150));
        var (loading, rows) = await h.On(() =>
        {
            h.List.SetListFilter(MessageFilter.All);
            var wasLoading = h.Mailbox.Model.Loading;
            h.Mailbox.HandleNewMessage(New(Msg("m6", 6)));
            return (wasLoading, h.List.Rows.Count);
        });
        Assert.True(loading);
        Assert.Equal(0, rows);
        await h.DelayedAsync(delayed + 1);
        await h.AdvanceAsync(TimeSpan.FromMilliseconds(150));
        Assert.True(Ids(h.List.Rows).SequenceEqual(["m5", "m3", "m2", "m1"]), "the fixture never got m6; the list did not invent it");
        h.Fixture.Delay(API.MessageList.Name, TimeSpan.Zero);

        // Grouped: into the conversation row (moved to the top), a new row
        // for an unlisted conversation, a reload without a thread id.
        await h.On(() =>
        {
            h.Settings.GroupByConversation = true;
        });
        await h.IdleAsync();
        h.Fixture.SetMessages(ThreadedMessages(), Acc, Inbox.Folder);
        await h.On(() => h.Mailbox.ReloadMessages!());
        await h.IdleAsync();
        Assert.Equal(["T:t3", "b1", "T:t1"], Ids(h.List.Rows));
        await h.On(() => h.Mailbox.HandleNewMessage(New(Msg("a4", 7, thread: "t1"))));
        Assert.Equal(["T:t1", "T:t3", "b1"], Ids(h.List.Rows));
        Assert.Equal(4, h.List.Rows[0].Summary?.MessageCount);
        Assert.Equal("a4", h.List.Rows[0].Message.Id.Value);
        await h.On(() => h.Mailbox.HandleNewMessage(New(Msg("d1", 8, thread: "t4"))));
        Assert.Equal(["d1", "T:t1", "T:t3", "b1"], Ids(h.List.Rows));
        Assert.Equal(new ListKey("t4", "d1"), h.List.Rows[0].Key);
        var lists = h.Fixture.ThreadListRequests.Count;
        Assert.True(await h.On(() =>
        {
            h.Mailbox.HandleNewMessage(New(Msg("z1", 9)));
            return h.Mailbox.Model.Loading;
        }));
        await h.IdleAsync();
        Assert.Equal(lists + 1, h.Fixture.ThreadListRequests.Count);
        Assert.True(Ids(h.List.Rows).SequenceEqual(["T:t3", "b1", "T:t1"]), "reloaded from the fixture, which has no z1");
    }

    [Fact]
    public async Task RemoveRowsSelectsTheNeighbourAndRestores()
    {
        var log = new ListLog();
        await using var h = await StartAsync(log, messages: new() { [Inbox] = [.. Enumerable.Range(1, 5).Select(i => Msg($"m{i}", i, flags: [Flag.Seen]))] });
        await h.IdleAsync();
        Assert.Equal(["m5", "m4", "m3", "m2", "m1"], Ids(h.List.Rows));
        await h.On(() => h.List.Select(new ListKey(Message: "m4")));
        Assert.Equal(["m4"], Ids(log.Selections));

        // The selected row goes: the row now at its place takes over.
        var restore = await h.On(() => h.List.RemoveRows(["m4"]));
        Assert.Equal(["m5", "m3", "m2", "m1"], Ids(h.List.Rows));
        Assert.Equal(SelectionHint.Neighbour, log.RowEvents[^1]);
        Assert.Equal(new ListKey(Message: "m3"), h.List.SelectedKey);
        Assert.Equal(["m4", "m3"], Ids(log.Selections));
        Assert.Equal(4, h.Mailbox.Model.Total);

        // Put back where it was; the selection stays where it went.
        await h.On(restore);
        Assert.Equal(["m5", "m4", "m3", "m2", "m1"], Ids(h.List.Rows));
        Assert.Equal(new ListKey(Message: "m3"), h.List.SelectedKey);
        Assert.Equal(2, log.Selections.Count);
        Assert.Equal(5, h.Mailbox.Model.Total);

        // A row that is not selected goes quietly; the last row hands the
        // selection to the new last one.
        await h.On(() => h.List.RemoveRows(["m5"]));
        Assert.Equal(new ListKey(Message: "m3"), h.List.SelectedKey);
        Assert.Equal(2, log.Selections.Count);
        await h.On(() =>
        {
            h.List.Select(new ListKey(Message: "m1"));
            h.List.RemoveRows(["m1"]);
        });
        Assert.Equal(new ListKey(Message: "m2"), h.List.SelectedKey);
        Assert.Equal("m2", log.Selections[^1]?.Value);

        // The list ran empty: the pane is cleared.
        await h.On(() => h.List.RemoveRows(["m4", "m3", "m2"]));
        Assert.Empty(h.List.Rows);
        Assert.Null(h.List.SelectedKey);
        Assert.Null(log.Selections[^1]);
        Assert.Equal(1, log.Cleared);
        Assert.Equal(new ListState.Status("mail-unread-symbolic", "No Messages", "This folder is empty.", false), h.List.ListState);

        // A restore after the list moved on does nothing.
        var stale = await h.On(() => h.List.RemoveRows([]));
        await h.On(() => h.Mailbox.ReloadMessages!());
        await h.IdleAsync();
        Assert.Equal(5, h.List.Rows.Count);
        await h.On(stale);
        var restoreLate = await h.On(() => h.List.RemoveRows(["m5"]));
        Assert.Equal(4, h.List.Rows.Count);
        await h.On(h.List.LoadMessages);
        await h.IdleAsync();
        await h.On(restoreLate);
        Assert.True(h.List.Rows.Count == 5, "the reload brought m5 back; the stale restore did not double it");
    }

    [Fact]
    public async Task RemoveRowsInGroupedModeEditsTheConversation()
    {
        var log = new ListLog();
        await using var h = await StartAsync(log, messages: new() { [Inbox] = ThreadedMessages() }, grouped: true);
        await h.IdleAsync();
        await h.On(() => h.List.ToggleThread("t1"));
        await h.IdleAsync();
        Assert.Equal(6, h.List.Rows.Count);
        await h.On(() => h.List.Select(new ListKey("t1")));
        Assert.True(Ids(log.Selections).SequenceEqual(["a3"]), "a conversation row shows its newest member");

        // A member goes: the conversation is recomputed in place.
        var restore = await h.On(() => h.List.RemoveRows(["a3"]));
        Assert.Equal(["T:t3", "b1", "T:t1", "a1", "a2"], Ids(h.List.Rows));
        Assert.Equal(2, h.List.Rows[2].Summary?.MessageCount);
        Assert.Equal(0, h.List.Rows[2].Summary?.UnreadCount);
        Assert.Equal("a2", h.List.Rows[2].Message.Id.Value);
        Assert.Equal(new ListKey("t1"), h.List.SelectedKey);
        Assert.Single(log.Selections);
        await h.On(restore);
        Assert.Equal(["T:t3", "b1", "T:t1", "a1", "a2", "a3"], Ids(h.List.Rows));
        Assert.Equal(3, h.List.Rows[2].Summary?.MessageCount);

        // Every member goes: the row goes, the neighbour takes over.
        var restoreAll = await h.On(() => h.List.RemoveRows(["a1", "a2", "a3"]));
        Assert.Equal(["T:t3", "b1"], Ids(h.List.Rows));
        Assert.Equal(new ListKey("t2", "b1"), h.List.SelectedKey);
        Assert.Equal("b1", log.Selections[^1]?.Value);
        await h.On(restoreAll);
        Assert.Equal(["T:t3", "b1", "T:t1", "a1", "a2", "a3"], Ids(h.List.Rows));

        // A conversation whose members are not known is loaded again.
        var lists = h.Fixture.ThreadListRequests.Count;
        var (noop, loading) = await h.On(() =>
        {
            var r = h.List.RemoveRows(["c2"]);
            return (r, h.Mailbox.Model.Loading);
        });
        Assert.True(loading);
        await h.IdleAsync();
        Assert.Equal(lists + 1, h.Fixture.ThreadListRequests.Count);
        await h.On(noop);
        Assert.Equal(6, h.List.Rows.Count);
    }

    [Fact]
    public async Task SelectedIdsFetchesMembersFirst()
    {
        var log = new ListLog();
        await using var h = await StartAsync(log, messages: new() { [Inbox] = ThreadedMessages() }, grouped: true);
        await h.IdleAsync();
        await h.On(() => h.List.Select(new ListKey("t1")));
        Assert.True(h.List.ActionFlags.On && h.List.ActionFlags.MarkRead && h.List.ActionFlags.MarkUnread);

        var asked = await h.On(() =>
        {
            h.List.SelectedIds((row, ids) =>
            {
                log.Ids.Add(ids);
                log.IdsFromThreads.Add(row.Thread);
            });
            return log.Ids.Count;
        });
        Assert.True(asked == 0, "the members are asked for first");
        await h.IdleAsync();
        Assert.Equal(["a1", "a2", "a3"], Assert.Single(log.Ids).Select(id => id.Value));
        Assert.Equal([true], log.IdsFromThreads);
        Assert.Single(h.Fixture.ThreadGetRequests);
        Assert.True(h.List.Rows.Count == 3, "fetching members for an action does not unfold the row");

        // Known members: at once, no round trip.
        Assert.Equal(2, await h.On(() =>
        {
            h.List.SelectedIds((_, ids) => log.Ids.Add(ids));
            return log.Ids.Count;
        }));
        await h.IdleAsync();
        Assert.Single(h.Fixture.ThreadGetRequests);

        // A plain row: itself.
        await h.On(() =>
        {
            h.List.Select(new ListKey("t2", "b1"));
            h.List.SelectedIds((row, ids) =>
            {
                log.Ids.Add(ids);
                log.IdsFromThreads.Add(row.Thread);
            });
        });
        Assert.Equal(["b1"], log.Ids[^1].Select(id => id.Value));
        Assert.False(log.IdsFromThreads[^1]);
        Assert.Equal("s-a3", ListController.RowSubject(h.List.Rows[2]));
        Assert.True(ListController.FlagTarget(h.List.Rows[2]));
        Assert.Equal("s-b1", ListController.RowSubject(h.List.Rows[1]));

        // The selection moved on before the members arrived: nothing runs.
        var delayed = h.DelayedAnswers;
        h.Fixture.Delay(API.ThreadGet.Name, TimeSpan.FromMilliseconds(100));
        await h.On(() =>
        {
            h.List.Select(new ListKey("t3"));
            h.List.SelectedIds((_, ids) => log.Ids.Add(ids));
            h.List.Select(new ListKey("t2", "b1"));
        });
        await h.DelayedAsync(delayed + 1);
        await h.AdvanceAsync(TimeSpan.FromMilliseconds(100));
        Assert.Equal(2, h.Fixture.ThreadGetRequests.Count);
        Assert.Equal(3, log.Ids.Count);
    }

    [Fact]
    public async Task MarkReadTimerFollowsTheDelay()
    {
        var log = new ListLog();
        await using var h = await StartAsync(log, messages: new()
        {
            [Inbox] = [Msg("m1", 1), Msg("m2", 2), Msg("m3", 3), Msg("m4", 4, flags: [Flag.Seen])],
            [Outbox] = [Msg("o1", 5)],
        });
        await h.IdleAsync();
        var second = TimeSpan.FromSeconds(1);

        // Delay 0: at once; a read message never.
        await h.On(() =>
        {
            h.Settings.MarkReadDelay = 0;
            h.List.Select(new ListKey(Message: "m4"));
        });
        Assert.Empty(log.Marks);
        Assert.Equal(ActionRules.MessageActionState(h.List.Rows[0], h.Mailbox.Model), h.List.ActionFlags);
        Assert.True(h.List.ActionFlags.MarkUnread && !h.List.ActionFlags.MarkRead);
        await h.On(() => h.List.Select(new ListKey(Message: "m3")));
        Assert.Equal(["m3"], Ids(log.Marks));
        Assert.True(h.List.ActionFlags.MarkRead && !h.List.ActionFlags.MarkUnread);

        // Delay 1 (a second): after the delay, unless the selection moved.
        await h.On(() =>
        {
            h.Settings.MarkReadDelay = 1;
            h.List.Select(new ListKey(Message: "m2"));
        });
        Assert.Equal(["m3"], Ids(log.Marks));
        await h.AdvanceAsync(second - TimeSpan.FromMilliseconds(1));
        Assert.Equal(["m3"], Ids(log.Marks));
        await h.AdvanceAsync(TimeSpan.FromMilliseconds(1));
        Assert.Equal(["m3", "m2"], Ids(log.Marks));
        await h.On(() =>
        {
            h.List.Select(new ListKey(Message: "m1"));
            h.List.Select(new ListKey(Message: "m2"));
        });
        await h.AdvanceAsync(second);
        Assert.True(Ids(log.Marks).SequenceEqual(["m3", "m2", "m2"]), "m1 was left before its timer fired");

        // Clearing the selection cancels the timer.
        await h.On(() =>
        {
            h.List.Select(new ListKey(Message: "m1"));
            h.List.Select(null);
        });
        Assert.Null(log.Selections[^1]);
        Assert.Equal(ActionFlags.None, h.List.ActionFlags);
        await h.AdvanceAsync(second);
        Assert.Equal(3, log.Marks.Count);

        // The flags applied by the actions refresh the rows and the actions.
        await h.On(() => h.List.Select(new ListKey(Message: "m1")));
        Assert.Equal(["m1"], Ids(await h.On(() => h.List.ApplyFlags(["m1", "m4"], setFlags: [Flag.Seen]))));
        Assert.Equal([new ListKey(Message: "m1")], log.Refreshed[^1]);
        Assert.Equal(["seen"], h.List.RowFor(new ListKey(Message: "m1"))!.Message.Flags.Select(f => f.Value));
        Assert.True(h.List.ActionFlags.MarkUnread && !h.List.ActionFlags.MarkRead);
        await h.On(() => h.List.Select(null));

        // An outbox message is never marked read.
        await h.On(() =>
        {
            h.Settings.MarkReadDelay = 0;
            h.Mailbox.SelectFolder(Outbox, fav: false);
        });
        await h.IdleAsync();
        Assert.True(h.List.InOutbox);
        await h.On(() => h.List.Select(new ListKey(Message: "o1")));
        Assert.Equal(3, log.Marks.Count);
        Assert.True(h.List.ActionFlags.Outbox && !h.List.ActionFlags.Star);
    }

    [Fact]
    public async Task OutboxIsNeverGrouped()
    {
        var queued = Msg("o1", 5) with { Outbox = new OutboxInfo { State = OutboxState.Queued, Attempts = 0 } };
        var log = new ListLog();
        await using var h = await StartAsync(log, messages: new() { [Inbox] = ThreadedMessages(), [Outbox] = [queued] }, grouped: true);
        await h.IdleAsync();
        Assert.True(h.Mailbox.Model.Grouped);
        Assert.Equal(FolderRole.Inbox, h.List.FolderRole.Value);

        await h.On(() => h.Mailbox.SelectFolder(Outbox, fav: false));
        await h.IdleAsync();
        Assert.False(h.Mailbox.Model.Grouped);
        Assert.True(h.List.InOutbox);
        Assert.Single(h.Fixture.ListRequests);
        Assert.Single(h.Fixture.ThreadListRequests);
        Assert.Equal(["o1"], Ids(h.List.Rows));
        Assert.Equal(new ListKey(Message: "o1"), h.List.Rows[0].Key);

        await h.On(() => h.Mailbox.SelectFolder(Inbox, fav: false));
        await h.IdleAsync();
        Assert.True(h.Mailbox.Model.Grouped);
        Assert.Equal(2, h.Fixture.ThreadListRequests.Count);

        // Turning grouping off reloads the folder flat.
        var (grouped, rows) = await h.On(() =>
        {
            h.Settings.GroupByConversation = false;
            return (h.Mailbox.Model.Grouped, h.List.Rows.Count);
        });
        Assert.False(grouped);
        Assert.True(rows == 0, "a mode switch empties the list at once");
        await h.IdleAsync();
        Assert.Equal(["c2", "b1", "c1", "a3", "a2", "a1"], Ids(h.List.Rows));
        Assert.Equal(2, h.Fixture.ListRequests.Count);
    }

    [Fact]
    public async Task DisconnectFoldsAConversationWaitingForMembers()
    {
        var log = new ListLog();
        await using var h = await StartAsync(log, messages: new() { [Inbox] = ThreadedMessages() }, grouped: true);
        await h.IdleAsync();
        await StartWaitingForMembersAsync(h);

        // The backend went away: the row folds back, the late reply is
        // dropped, no toast. The folder half's BumpAll clears the loading
        // flags (model.go); the list half only redraws the footer from them.
        var (expanded, loading, rows, loadingMore) = await h.On(() =>
        {
            h.Mailbox.HandleConnection(new ConnectionState.Unavailable("gone"));
            return (h.List.Rows[2].Expanded, h.List.Rows[2].Loading, h.List.Rows.Count, h.Mailbox.Model.LoadingMore);
        });
        Assert.True(!expanded && !loading);
        Assert.Equal(3, rows);
        Assert.False(loadingMore);
        Assert.False(await h.On(() =>
        {
            h.List.HandleConnection(new ConnectionState.Unavailable("gone"));
            return h.List.LoadMoreState.Spinner;
        }));
        await h.AdvanceAsync(TimeSpan.FromMilliseconds(200));
        Assert.Equal(3, h.List.Rows.Count);
        Assert.Empty(h.Toasts);
        Assert.DoesNotContain(new ThreadId("t1"), h.Mailbox.Model.Expanded);
        Assert.False(h.Mailbox.Model.Members[new ThreadId("t1")].Fetching);

        // Unfolding again asks anew once the backend is back.
        h.Fixture.Delay(API.ThreadGet.Name, TimeSpan.Zero);
        await h.On(() => h.Mailbox.HandleConnection(new ConnectionState.Connected(MailboxControllerHarness.Info)));
        await h.IdleAsync();
        await h.On(() => h.List.ToggleThread("t1"));
        await h.IdleAsync();
        Assert.Equal(6, h.List.Rows.Count);
        Assert.Equal(2, h.Fixture.ThreadGetRequests.Count);
    }

    /// <summary>
    /// A daemon of another protocol version leaves no connection either: the
    /// list folds back and stops its spinners as for a lost backend, and
    /// nothing is listed anew.
    /// </summary>
    [Fact]
    public async Task ProtocolMismatchFoldsAConversationWaitingForMembers()
    {
        var log = new ListLog();
        await using var h = await StartAsync(log, messages: new() { [Inbox] = ThreadedMessages() }, grouped: true);
        await h.IdleAsync();
        await StartWaitingForMembersAsync(h);

        var accountLists = h.Fixture.CallCount(API.AccountList.Name);
        var (expanded, loading, loadingMore, spinner) = await h.On(() =>
        {
            h.Mailbox.HandleConnection(new ConnectionState.ProtocolMismatch(1));
            h.List.HandleConnection(new ConnectionState.ProtocolMismatch(1));
            return (h.List.Rows[2].Expanded, h.List.Rows[2].Loading, h.Mailbox.Model.LoadingMore, h.List.LoadMoreState.Spinner);
        });
        Assert.True(!expanded && !loading);
        Assert.False(loadingMore);
        Assert.False(spinner);
        await h.AdvanceAsync(TimeSpan.FromMilliseconds(200));
        Assert.Equal(3, h.List.Rows.Count);
        Assert.Empty(h.Toasts);
        Assert.True(h.Fixture.CallCount(API.AccountList.Name) == accountLists, "nothing is loaded anew");
    }

    /// <summary>
    /// Windows-only (docs/windows-port.md §7.5): every change of the rows is
    /// a new snapshot, keyed and applicable to a collection by key, and
    /// <see cref="ListController.SelectedKey"/> is current when
    /// <see cref="ListController.RowsChanged"/> arrives, so a view can apply
    /// the rows and then select the key; the properties a view binds to
    /// announce their changes.
    /// </summary>
    [Fact]
    public async Task RowsAreKeyedSnapshotsWithTheSelectionAsAKey()
    {
        var log = new ListLog();
        await using var h = await StartAsync(log, messages: new() { [Inbox] = [.. Enumerable.Range(1, 5).Select(i => Msg($"m{i}", i, flags: [Flag.Seen]))] });
        await h.IdleAsync();
        var view = new ObservableCollection<ListRow>();
        var selectedInView = new List<ListKey?>();
        var changed = new List<string>();
        await h.On(() =>
        {
            KeyedListSync.Apply(view, h.List.Rows, r => r.Key);
            h.List.RowsChanged += (_, u) =>
            {
                KeyedListSync.Apply(view, u.Rows, r => r.Key);
                selectedInView.Add(h.List.SelectedKey is { } k && view.Any(r => r.Key == k) ? k : null);
            };
            h.List.PropertyChanged += (_, e) => changed.Add(e.PropertyName!);
        });

        await h.On(() => h.List.Select(new ListKey(Message: "m3")));
        Assert.Equal([nameof(ListController.SelectedKey), nameof(ListController.ActionFlags)], changed);
        changed.Clear();
        await h.On(() => h.List.RemoveRows(["m3"]));
        Assert.Equal(h.List.Rows.Select(r => r.Key), view.Select(r => r.Key));
        Assert.Equal([new ListKey(Message: "m2")], selectedInView);
        Assert.Contains(nameof(ListController.Rows), changed);
        Assert.Contains(nameof(ListController.SelectedKey), changed);

        // A new message moves every row down by one: the view inserts it and
        // keeps the others, and the selection is still there by its key.
        var before = view.ToList();
        await h.On(() => h.Mailbox.HandleNewMessage(New(Msg("m9", 9, flags: [Flag.Seen]))));
        Assert.Equal(h.List.Rows.Select(r => r.Key), view.Select(r => r.Key));
        Assert.Equal(new ListKey(Message: "m9"), view[0].Key);
        Assert.All(before, r => Assert.Contains(r, view));
        Assert.Equal(new ListKey(Message: "m2"), selectedInView[^1]);

        // Another folder: the hint is Clear, and the snapshot empty.
        changed.Clear();
        var hints = log.RowEvents.Count;
        await h.On(() => h.Mailbox.SelectFolder(Trash, fav: false));
        await h.IdleAsync();
        Assert.Contains(SelectionHint.Clear, log.RowEvents.Skip(hints));
        Assert.Empty(view);
        Assert.Null(h.List.SelectedKey);
        Assert.Contains(nameof(ListController.FolderRole), changed);
        Assert.Contains(nameof(ListController.ListState), changed);
    }

    /// <summary>
    /// Windows-only (docs/windows-port.md §7.5): a flag that changes on the
    /// selected row (mark-as-read after every selection) is a new snapshot
    /// with a new record under the same key, announced as
    /// <see cref="ListController.RowsRefreshed"/> in flat mode and as
    /// <see cref="ListController.RowsChanged"/> in grouped mode. Applied
    /// through the view overload of <see cref="KeyedListSync"/>, the
    /// selected row keeps its view object, updated in place, and the
    /// collection raises nothing, so a ListView keeps its selection; the
    /// record overload would replace the row, which a WinUI selector takes
    /// for a removal and an insertion.
    /// </summary>
    [Fact]
    public async Task FlagChangesUpdateTheSelectedRowInPlace()
    {
        // Flat.
        await using (var h = await StartAsync(new ListLog(), messages: new() { [Inbox] = [.. Enumerable.Range(1, 5).Select(i => Msg($"m{i}", i))] }))
        {
            await h.IdleAsync();
            var (view, actions) = await ViewOfAsync(h);
            await h.On(() => h.List.Select(new ListKey(Message: "m3")));
            var selected = view.Single(v => v.Key == h.List.SelectedKey);

            var refreshed = new List<IReadOnlyList<ListKey>>();
            await h.On(() =>
            {
                h.List.RowsRefreshed += (_, keys) => refreshed.Add(keys);
                h.List.ApplyFlags(["m3"], setFlags: [Flag.Seen]);
            });
            Assert.Equal([new ListKey(Message: "m3")], Assert.Single(refreshed));
            Assert.Empty(actions);
            Assert.Equal(new ListKey(Message: "m3"), h.List.SelectedKey);
            Assert.Same(selected, view.Single(v => v.Key == h.List.SelectedKey));
            Assert.Equal([Flag.Seen], selected.Row.Message.Flags);
            Assert.Equal(1, await RecordsReplacedAsync(h, flag: "m2"));
        }

        // Grouped: the conversation row's aggregates move.
        await using (var h = await StartAsync(new ListLog(), messages: new() { [Inbox] = ThreadedMessages() }, grouped: true))
        {
            await h.IdleAsync();
            var (view, actions) = await ViewOfAsync(h);
            await h.On(() => h.List.Select(new ListKey("t3")));
            var selected = view.Single(v => v.Key == h.List.SelectedKey);
            Assert.Equal(1, selected.Row.Summary?.UnreadCount);

            Assert.Equal(["c2"], Ids(await h.On(() => h.List.ApplyFlags(["c2"], setFlags: [Flag.Seen]))));
            Assert.Empty(actions);
            Assert.Equal(new ListKey("t3"), h.List.SelectedKey);
            Assert.Same(selected, view.Single(v => v.Key == h.List.SelectedKey));
            Assert.Equal(0, selected.Row.Summary?.UnreadCount);
            Assert.Equal(1, await RecordsReplacedAsync(h, flag: "a3"));
        }

        // The view a WinUI list would keep: row view models applied in
        // place on every announcement of the rows, and what the collection
        // raised since the selection.
        static async Task<(ObservableCollection<RowView> View, List<NotifyCollectionChangedAction> Actions)> ViewOfAsync(MailboxControllerHarness h)
        {
            var view = new ObservableCollection<RowView>();
            var actions = new List<NotifyCollectionChangedAction>();
            await h.On(() =>
            {
                Apply(view, h.List.Rows);
                h.List.RowsChanged += (_, u) => Apply(view, u.Rows);
                h.List.RowsRefreshed += (_, _) => Apply(view, h.List.Rows);
                h.List.SelectedMessageChanged += (_, _) => actions.Clear();
                view.CollectionChanged += (_, e) => actions.Add(e.Action);
            });
            return (view, actions);
        }

        static void Apply(ObservableCollection<RowView> view, IReadOnlyList<ListRow> rows) =>
            KeyedListSync.Apply(view, rows, r => r.Key, v => v.Key, r => new RowView(r), (v, r) => v.Row = r);

        // What the record overload does to a flag change of one message:
        // the collection of records replaces its row.
        static async Task<int> RecordsReplacedAsync(MailboxControllerHarness h, MessageId flag)
        {
            var changes = await h.On(() =>
            {
                var records = new ObservableCollection<ListRow>();
                KeyedListSync.Apply(records, h.List.Rows, r => r.Key);
                h.List.ApplyFlags([flag], setFlags: [Flag.Seen]);
                return KeyedListSync.Apply(records, h.List.Rows, r => r.Key);
            });
            Assert.True(changes.KeptStructure);
            return changes.Updated;
        }
    }

    /// <summary>A message dated <paramref name="hours"/> after <see cref="Base"/>; unread unless <paramref name="flags"/> say otherwise.</summary>
    internal static MessageSummary Msg(string id, int hours, string from = "alice", string? thread = null, Flag[]? flags = null) => new()
    {
        Id = id,
        AccountId = Acc,
        FolderId = Inbox.Folder,
        ThreadId = thread is null ? (ThreadId?)null : new ThreadId(thread),
        From = [new Address { Name = from, Email = from + "@example.invalid" }],
        Subject = "s-" + id,
        Date = Base.AddHours(hours),
        Snippet = "p-" + id,
        Flags = flags ?? [],
        HasAttachments = false,
        Size = 0,
    };

    internal static Folder[] TestFolders() =>
    [
        TestFolder("in", "INBOX", FolderRole.Inbox),
        TestFolder("trash", "Trash", FolderRole.Trash),
        TestFolder("empty", "Empty"),
        TestFolder("out", "Outbox", FolderRole.Outbox),
    ];

    /// <summary>
    /// The conversations of the grouped tests: t1 with three members, t2 with
    /// one, t3 with two; t3 is the newest.
    /// </summary>
    internal static MessageSummary[] ThreadedMessages() =>
    [
        Msg("a1", 1, from: "bob", thread: "t1", flags: [Flag.Seen]),
        Msg("a2", 2, from: "alice", thread: "t1", flags: [Flag.Seen]),
        Msg("a3", 3, from: "carol", thread: "t1"),
        Msg("b1", 5, from: "dave", thread: "t2", flags: [Flag.Seen]),
        Msg("c1", 4, from: "erin", thread: "t3", flags: [Flag.Seen]),
        Msg("c2", 6, from: "frank", thread: "t3"),
    ];

    /// <summary>The rows as the Swift tests name them: "T:" and the thread for a conversation row, the message id otherwise.</summary>
    internal static List<string> Ids(IEnumerable<ListRow> rows) =>
        [.. rows.Select(r => r.Thread ? "T:" + r.Key.Thread?.Value : r.Message.Id.Value)];

    internal static List<string?> Ids(IEnumerable<MessageId?> ids) => [.. ids.Select(id => id?.Value)];

    internal static List<string> Ids(IEnumerable<MessageId> ids) => [.. ids.Select(id => id.Value)];

    internal static NewMessageNotification New(MessageSummary s) => new() { AccountId = Acc, FolderId = Inbox.Folder, Message = s };

    /// <summary>A fixture, a connected client, the folder controller and the list controller over a throwaway settings store.</summary>
    internal static Task<MailboxControllerHarness> StartAsync(
        ListLog log,
        Folder[]? folders = null,
        Dictionary<FolderKey, MessageSummary[]>? messages = null,
        bool grouped = false,
        bool connect = true) =>
        MailboxControllerHarness.StartAsync(
            f =>
            {
                f.SetAccounts([TestAccount("a", email: "a@example.invalid")]);
                f.SetFolders(folders ?? TestFolders(), Acc);
                foreach (var (k, list) in messages ?? new())
                {
                    f.SetMessages(list, k.Account, k.Folder);
                }
            },
            withList: true,
            wire: h => Wire(h, log),
            connect: connect,
            prepare: s => s.GroupByConversation = grouped);

    // A page of more conversations asked for (the footer spins) and a
    // conversation unfolded whose members wait on the clock.
    private static async Task StartWaitingForMembersAsync(MailboxControllerHarness h)
    {
        Assert.True(await h.On(() =>
        {
            h.Mailbox.Model.NextCursor = "50";
            h.List.ShowLoadMore();
            h.List.LoadMore();
            return h.List.LoadMoreState.Spinner;
        }));
        var delayed = h.DelayedAnswers;
        h.Fixture.Delay(API.ThreadGet.Name, TimeSpan.FromMilliseconds(200));
        Assert.True(await h.On(() =>
        {
            h.List.ToggleThread("t1");
            return h.List.Rows[2].Loading;
        }));
        await h.DelayedAsync(delayed + 1);
    }

    // What the controllers emitted, in order (Swift's ListLog and the
    // callbacks the Harness installs).
    private static void Wire(MailboxControllerHarness h, ListLog log)
    {
        var list = h.List;
        list.RowsChanged += (_, u) => log.RowEvents.Add(u.Hint);
        list.ListStateChanged += (_, s) => log.States.Add(s);
        list.LoadMoreChanged += (_, s) => log.LoadMore.Add(s);
        list.SelectedMessageChanged += (_, m) => log.Selections.Add(m?.Id);
        list.ActivateMessage += (_, m) => log.Activations.Add(m.Id);
        list.ActionFlagsChanged += (_, f) => log.Flags.Add(f);
        list.MarkRead += (_, id) => log.Marks.Add(id);
        list.RowsRefreshed += (_, keys) => log.Refreshed.Add(keys);
        list.SelectionCleared += (_, _) => log.Cleared++;
    }

    /// <summary>A list row's view model, updated in place (docs/windows-port.md §7.5).</summary>
    private sealed class RowView(ListRow row)
    {
        public ListKey Key { get; } = row.Key;

        public ListRow Row { get; set; } = row;
    }

    /// <summary>What the controllers emitted, in order.</summary>
    internal sealed class ListLog
    {
        public List<SelectionHint> RowEvents { get; } = [];

        public List<ListState> States { get; } = [];

        public List<LoadMoreState> LoadMore { get; } = [];

        public List<MessageId?> Selections { get; } = [];

        public List<MessageId> Activations { get; } = [];

        public List<ActionFlags> Flags { get; } = [];

        public List<MessageId> Marks { get; } = [];

        public List<IReadOnlyList<ListKey>> Refreshed { get; } = [];

        public int Cleared { get; set; }

        /// <summary>What <see cref="ListController.SelectedIds"/> handed over.</summary>
        public List<IReadOnlyList<MessageId>> Ids { get; } = [];

        /// <summary>Whether each of <see cref="Ids"/> came with a conversation row.</summary>
        public List<bool> IdsFromThreads { get; } = [];
    }
}
