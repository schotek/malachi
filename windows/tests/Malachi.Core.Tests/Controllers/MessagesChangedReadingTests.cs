// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/MessagesChangedTests.swift
// (MessagesChangedReadingTests), the counterpart of the refreshShown cases
// of ui/internal/window/notify_test.go: a Jira account's messages rebuilt
// in place (notify.messagesChanged, docs/api.md §5) are dropped from the
// message cache and fetched again by the pane, a single message and a
// conversation's cards alike, and a shown virtual folder follows its
// space. The notifications are handed to the controller as the app routes
// them.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Model;
using Xunit;
using static Malachi.Core.Tests.Model.MailModelTests;
using Change = Malachi.Core.Controllers.ConversationController.Change;

namespace Malachi.Core.Tests.Controllers;

public sealed class MessagesChangedReadingTests
{
    private static readonly AccountId JiraAccount = "j";
    private static readonly FolderKey Web = new("j", "web");
    private static readonly FolderKey Assigned = new("j", "assigned");

    // 2026-09-01T10:00:00Z.
    private static readonly DateTimeOffset Base = DateTimeOffset.FromUnixTimeSeconds(1_788_256_800);

    // The single-message view: the cache drops the account's messages and
    // the shown one is fetched again, rebuilt.
    [Fact]
    public async Task TheShownMessageIsFetchedAgain()
    {
        await using var r = await Reading.StartAsync();
        var h = r.H;
        h.Fixture.SetBody("w2d", Body("w2d", "with the bot's header line"));
        await r.SelectAsync(Web);
        await h.On(() => h.List.Select(new ListKey("issue-WEB-2", "w2d")));
        await h.IdleAsync();
        Assert.True(r.Cache.Loaded("w2d")?.Complete);
        Assert.Equal(["w2d"], r.Shown.Select(id => id.Value));
        Assert.Equal(1, h.Fixture.CallCount(API.MessageBody.Name));
        Assert.Equal("with the bot's header line", r.Cache.Loaded("w2d")?.Body?.Text);
        Assert.Equal(JiraAccount, r.Cache.Loaded("w2d")?.AccountId);
        var lists = h.Fixture.ThreadListRequests.Count;

        // The pass rebuilt the message in place: the pane shows it again and
        // the cache fetches it afresh; the folder is listed again.
        h.Fixture.SetBody("w2d", Body("w2d", "cleaned"));
        await PushAsync(h, JiraAccount, "web", "assigned");
        Assert.Equal("cleaned", r.Cache.Loaded("w2d")?.Body?.Text);
        Assert.Equal(["w2d", "w2d"], r.Shown.Select(id => id.Value));
        Assert.Equal(2, h.Fixture.CallCount(API.MessageBody.Name));
        Assert.Equal(2, h.Fixture.CallCount(API.MessageGet.Name)); // the headers too: a re-attributed comment changes them
        Assert.Equal(lists + 1, h.Fixture.ThreadListRequests.Count);
        Assert.Equal(new ListKey("issue-WEB-2", "w2d"), h.List.SelectedKey); // the selection stays

        // Another account's notification leaves the Jira entry alone, and
        // one for another folder of the account drops the cache but shows
        // nothing again (nothing there changed).
        await PushAsync(h, "a", "in");
        Assert.Equal("cleaned", r.Cache.Loaded("w2d")?.Body?.Text);
        Assert.Equal(2, r.Shown.Count);
        await PushAsync(h, JiraAccount, "assigned");
        Assert.Null(r.Cache.Loaded("w2d")); // the account's entries go
        Assert.Equal(2, r.Shown.Count); // the shown folder is not named
        Assert.Equal(2, h.Fixture.CallCount(API.MessageBody.Name));
    }

    // The conversation view: the held bodies go, the pane asks again and
    // gets the rebuilt ones, the members come back through one thread.get
    // with their new senders.
    [Fact]
    public async Task TheShownConversationIsFetchedAgain()
    {
        await using var r = await Reading.StartAsync();
        var h = r.H;
        h.Fixture.SetBody("w1c", Body("w1c", "ITSD-9 Eva Horáková added comment"));
        await r.SelectAsync(Web);
        await h.On(() => h.List.Select(new ListKey(Thread: "issue-WEB-1")));
        await h.IdleAsync();
        Assert.Equal([Change.Loading, Change.Opened], r.Changes);
        Assert.Equal(["Petr Svoboda", "Petr Svoboda"], r.Conversation.Model!.Items.Select(it => it.Sender));
        // The pane asks for the card near the viewport.
        await h.On(() => r.Conversation.NeedsBody("w1c"));
        await h.IdleAsync();
        Assert.Equal(1, h.Fixture.CallCount(API.MessageBody.Name));
        Assert.Equal("ITSD-9 Eva Horáková added comment", r.Conversation.Loaded["w1c"].Body?.Text);
        var gets = h.Fixture.ThreadGetRequests.Count;
        var lists = h.Fixture.ThreadListRequests.Count;

        // The pass re-attributed the comment and cleaned its body.
        h.Fixture.SetMessages(Items(comment: "Eva Horáková"), JiraAccount, Web.Folder);
        h.Fixture.SetBody("w1c", Body("w1c", "cleaned"));
        await h.On(() =>
        {
            h.Mailbox.HandleNotification(Changed(JiraAccount, "web", "assigned"));
            Assert.Equal(Change.Updated, r.Changes[2]); // the pane asks for the bodies again
            Assert.Empty(r.Conversation.Loaded); // the held entries went
            Assert.Null(r.Cache.Loaded("w1c")); // the cache's too
            Assert.True(r.Conversation.Thread == "issue-WEB-1" && r.Conversation.Model is not null); // the cards stay up meanwhile
        });
        await h.IdleAsync();
        // The members follow the reload: one thread.get, the new sender.
        Assert.Equal(["Petr Svoboda", "Eva Horáková"], r.Conversation.Model!.Items.Select(it => it.Sender));
        Assert.Equal(gets + 1, h.Fixture.ThreadGetRequests.Count);
        Assert.Equal(lists + 1, h.Fixture.ThreadListRequests.Count);
        Assert.Equal(Change.Updated, r.Changes[^1]);
        // The pane asks again, as it does after Updated: the body is fetched afresh.
        await h.On(() => r.Conversation.NeedsBody("w1c"));
        await h.IdleAsync();
        Assert.Equal("cleaned", r.Conversation.Loaded["w1c"].Body?.Text);
        Assert.Equal(2, h.Fixture.CallCount(API.MessageBody.Name));
        Assert.Equal(new ListKey(Thread: "issue-WEB-1"), h.List.SelectedKey);
    }

    // A virtual folder holds copies of every space's issues: shown, it is
    // refreshed for a notification that names only the space folder.
    [Fact]
    public async Task AVirtualFolderFollowsItsSpace()
    {
        await using var r = await Reading.StartAsync();
        var h = r.H;
        await r.SelectAsync(Assigned);
        await h.On(() => h.List.Select(new ListKey(Thread: "issue-WEB-1")));
        await h.IdleAsync();
        await h.On(() => r.Conversation.NeedsBody("w1c"));
        await h.IdleAsync();
        Assert.NotNull(r.Conversation.Loaded["w1c"].Body);
        var lists = h.Fixture.ThreadListRequests.Count;
        var gets = h.Fixture.ThreadGetRequests.Count;

        h.Fixture.SetMessages([.. Items(comment: "Eva Horáková").Where(s => s.ThreadId == "issue-WEB-1")], JiraAccount, Assigned.Folder);
        await PushAsync(h, JiraAccount, "web");
        Assert.Equal(["Petr Svoboda", "Eva Horáková"], r.Conversation.Model!.Items.Select(it => it.Sender));
        Assert.Empty(r.Conversation.Loaded);
        Assert.Equal(lists + 1, h.Fixture.ThreadListRequests.Count);
        Assert.Equal("assigned", h.Fixture.ThreadListRequests[^1].FolderId.Value);
        Assert.Equal(gets + 1, h.Fixture.ThreadGetRequests.Count);
    }

    // The cache's account-scoped eviction (LoadedCache.RemoveAll).
    [Fact]
    public void EvictionIsPerAccount()
    {
        var c = new LoadedCache();
        c.Store("x", new LoadedMessage { AccountId = "a" });
        c.Store("y", new LoadedMessage { AccountId = "j" });
        c.Store("z", new LoadedMessage());
        var w = new LoadedMessage
        {
            Msg = new Message
            {
                Summary = new MessageSummary
                {
                    Id = "w",
                    AccountId = "j",
                    FolderId = "web",
                    ThreadId = "t",
                    From = [],
                    Subject = "",
                    Date = Base,
                    Snippet = "",
                    Flags = [],
                    HasAttachments = false,
                    Size = 0,
                },
            },
        };
        Assert.Equal("j", w.AccountId?.Value); // the account of a cached message.get
        c.Store("w", w);
        Assert.Equal(["w", "y", "z"], c.RemoveAll(new AccountId("j")).Select(id => id.Value)); // the account's, and those of no known account
        Assert.True(c["x"] is not null && c["y"] is null && c["z"] is null && c["w"] is null);
        Assert.Empty(c.RemoveAll(new AccountId("j")));
    }

    private static IssueInfo Info(string key) => new()
    {
        Key = key,
        Url = "https://acme.atlassian.net/browse/" + key,
        Summary = "Summary of " + key,
        Status = "To Do",
        StatusCategory = IssueStatusCategory.Todo,
    };

    // A message of issue key (its thread) dated hours after Base.
    private static MessageSummary Item(string id, int hours, string key, IssueItemKind? kind = null, string from = "Petr Svoboda") => new()
    {
        Id = id,
        AccountId = JiraAccount,
        FolderId = Web.Folder,
        ThreadId = "issue-" + key,
        From = [new Address { Name = from, Email = "" }],
        Subject = key + ": Summary of " + key,
        Date = Base.AddHours(hours),
        Snippet = "p-" + id,
        Flags = [Flag.Seen],
        HasAttachments = false,
        Size = 0,
        Issue = MessageIssue.Of(Info(key), kind ?? IssueItemKind.Comment),
    };

    // WEB-1: a description and a comment (a conversation row); WEB-2: a
    // description alone (a plain row, the single-message view).
    private static MessageSummary[] Items(string comment = "Petr Svoboda") =>
    [
        Item("w1d", 1, "WEB-1", IssueItemKind.Description),
        Item("w1c", 4, "WEB-1", from: comment),
        Item("w2d", 2, "WEB-2", IssueItemKind.Description),
    ];

    private static MessageBodyResult Body(string id, string text) => new()
    {
        MessageId = id,
        BodyState = BodyState.Fetched,
        HasHtml = false,
        Text = text,
        Blocked = new BlockedContent(),
        RemoteContent = RemoteContentPolicy.Block,
        SanitizerVersion = "1",
    };

    private static DaemonNotification.MessagesChanged Changed(AccountId account, params string[] folders) =>
        new DaemonNotification.MessagesChanged(new MessagesChangedNotification { AccountId = account, FolderIds = [.. folders.Select(f => new FolderId(f))] });

    private static async Task PushAsync(MailboxControllerHarness h, AccountId account, params string[] folders)
    {
        await h.On(() => h.Mailbox.HandleNotification(Changed(account, folders)));
        await h.IdleAsync();
    }

    // A fixture with a mail account (first, so its Inbox is the initial
    // folder) and a Jira account with a space folder and a virtual one, the
    // list controller, the message cache and the conversation controller,
    // wired as the app wires the reading pane: the selected row shows its
    // conversation, or fetches its message as the single-message view does;
    // notify.messagesChanged lets the cache go of the account's messages.
    private sealed class Reading : IAsyncDisposable
    {
        private Reading(MailboxControllerHarness h) => H = h;

        public MailboxControllerHarness H { get; }

        public MessageCache Cache { get; private set; } = null!;

        public ConversationController Conversation { get; private set; } = null!;

        public List<Change> Changes { get; } = [];

        // The messages the single-message view was asked to show, in order.
        public List<MessageId> Shown { get; } = [];

        public static async Task<Reading> StartAsync()
        {
            Reading? r = null;
            var jira = new Account
            {
                Id = JiraAccount,
                Config = new AccountConfig
                {
                    Name = "Acme Jira",
                    Email = "jana@acme.example",
                    Kind = AccountKind.Jira,
                    Jira = new JiraConfig { SiteUrl = "https://acme.atlassian.net", Deployment = JiraDeployment.Cloud },
                },
                Enabled = true,
                State = new SyncState { AccountId = JiraAccount, Status = SyncStatus.Idle },
                Capabilities = [],
            };
            var h = await MailboxControllerHarness.StartAsync(
                f =>
                {
                    f.SetAccounts([TestAccount("a", email: "a@example.invalid"), jira]);
                    f.SetFolders([TestFolder("in", "INBOX", FolderRole.Inbox) with { AccountId = "a" }], "a");
                    f.SetFolders(
                    [
                        TestFolder("assigned", "Assigned to Me", name: "Assigned to Me") with { AccountId = JiraAccount, Virtual = VirtualFolder.AssignedToMe },
                        TestFolder("web", "WEB", name: "Web") with { AccountId = JiraAccount },
                    ],
                    JiraAccount);
                    f.SetMessages(
                    [
                        new MessageSummary
                        {
                            Id = "m1",
                            AccountId = "a",
                            FolderId = "in",
                            ThreadId = "t1",
                            From = [new Address { Name = "Petr", Email = "petr@example.invalid" }],
                            Subject = "s-m1",
                            Date = Base,
                            Snippet = "",
                            Flags = [Flag.Seen],
                            HasAttachments = false,
                            Size = 0,
                        },
                    ],
                    "a",
                    "in");
                    f.SetMessages(Items(), JiraAccount, Web.Folder);
                    f.SetMessages([.. Items().Where(s => s.ThreadId == "issue-WEB-1")], JiraAccount, Assigned.Folder);
                },
                withList: true,
                wire: h =>
                {
                    r = new Reading(h);
                    r.Cache = new MessageCache(h.Client, h.Toasts.Add, pending: h.Pending);
                    r.Conversation = new ConversationController(h.List, r.Cache);
                    h.List.SelectedRowChanged += (_, row) =>
                    {
                        if (r.Conversation.Show(row) || row is null)
                        {
                            return;
                        }
                        // The single-message view: the pane fetches the row's message.
                        r.Shown.Add(row.Message.Id);
                        r.Cache.Fetch(row.Message, _ => { });
                    };
                    r.Conversation.Changed += (_, c) => r.Changes.Add(c);
                    h.Mailbox.MessagesChanged += (_, n) => r.Cache.Evict(n.AccountId);
                },
                prepare: s => s.MarkReadDelay = 0);
            await h.IdleAsync();
            Assert.Equal(new FolderKey("a", "in"), await h.On(() => h.Mailbox.Model.ListFolder));
            return r!;
        }

        public async Task SelectAsync(FolderKey k)
        {
            await H.On(() => H.Mailbox.SelectFolder(k, fav: false));
            await H.IdleAsync();
            Assert.Equal(k, await H.On(() => H.Mailbox.Model.ListFolder));
        }

        public async ValueTask DisposeAsync()
        {
            await H.On(() =>
            {
                Conversation.Dispose();
                Cache.Dispose();
            });
            await H.DisposeAsync();
        }
    }
}
