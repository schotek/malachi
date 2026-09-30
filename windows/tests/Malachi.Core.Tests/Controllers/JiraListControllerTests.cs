// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/JiraListTests.swift (JiraListTests),
// the counterpart of the list cases of ui/internal/window/jira_list_test.go:
// the message list of a Jira account over MailFixture. Its folders are
// always listed as conversations, one per issue, its outbox stays flat, the
// grouping setting neither changes nor reloads it, a pass of the account
// reloads its virtual folder, and the rows carry the issue. The rules
// without a daemon are Model/JiraRowTests.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Model;
using Xunit;
using static Malachi.Core.Tests.Model.MailModelTests;

namespace Malachi.Core.Tests.Controllers;

public sealed class JiraListControllerTests
{
    private static readonly AccountId MailAccount = "a";
    private static readonly AccountId JiraAccount = "j";
    private static readonly FolderKey Inbox = new("a", "in");
    private static readonly FolderKey Web = new("j", "web");
    private static readonly FolderKey Assigned = new("j", "assigned");
    private static readonly FolderKey JiraOutbox = new("j", "out");

    // 2026-09-01T10:00:00Z.
    private static readonly DateTimeOffset Base = DateTimeOffset.FromUnixTimeSeconds(1_788_256_800);

    private const MessageActionKind Everything =
        MessageActionKind.Reply | MessageActionKind.ReplyAll | MessageActionKind.Forward | MessageActionKind.Trash
        | MessageActionKind.Move | MessageActionKind.Archive | MessageActionKind.Junk;

    [Fact]
    public async Task JiraFolderIsListedAsConversationsWithTheSettingOff()
    {
        await using var h = await StartAsync();
        // The mail Inbox, first: flat, as the setting says.
        Assert.False(h.Mailbox.Model.Grouped);
        Assert.Equal(["in"], h.Fixture.ListRequests.Select(p => p.FolderId.Value));
        Assert.Empty(h.Fixture.ThreadListRequests);

        await SelectAsync(h, Web);
        Assert.True(h.Mailbox.Model.Grouped);
        Assert.True(h.Mailbox.Model.AlwaysGrouped(Web));
        Assert.Equal(["web"], h.Fixture.ThreadListRequests.Select(p => p.FolderId.Value));
        Assert.Single(h.Fixture.ListRequests); // no message.list for a Jira folder
        Assert.Equal(["T:issue-WEB-1", "T:issue-WEB-2"], MailboxControllerListTests.Ids(h.List.Rows));
        Assert.Equal("WEB-1", h.List.Rows[0].Summary?.Issue?.Key);
        // Nothing selected: the actions are those of the listed account.
        Assert.Null(h.List.SelectedKey);
        Assert.Equal(Everything, h.List.ActionFlags.Unsupported);

        // A virtual folder of the account too.
        await SelectAsync(h, Assigned);
        Assert.True(h.Mailbox.Model.Grouped);
        Assert.Equal(["web", "assigned"], h.Fixture.ThreadListRequests.Select(p => p.FolderId.Value));

        // Back in the mail account: flat again.
        await SelectAsync(h, Inbox);
        Assert.False(h.Mailbox.Model.Grouped);
        Assert.False(h.Mailbox.Model.AlwaysGrouped(Inbox));
        Assert.Equal(["in", "in"], h.Fixture.ListRequests.Select(p => p.FolderId.Value));
        Assert.Equal(["m2", "m1"], MailboxControllerListTests.Ids(h.List.Rows));
        Assert.Equal(ActionFlags.None, h.List.ActionFlags);
    }

    [Fact]
    public async Task JiraOutboxStaysFlat()
    {
        await using var h = await StartAsync();
        // The outbox is listed once something is in it.
        h.Fixture.SetMessages([Item("o1", 1, "WEB-1") with { FolderId = JiraOutbox.Folder }], JiraAccount, JiraOutbox.Folder);
        h.Fixture.SetFolders(JiraFolders(outboxTotal: 1), JiraAccount);
        await h.On(h.Mailbox.LoadAccounts);
        await h.IdleAsync();
        Assert.Equal(1, h.Mailbox.Model.Folder(JiraOutbox)?.Total);
        Assert.True(h.Mailbox.Model.AlwaysGrouped(JiraOutbox)); // the account is always grouped…
        await SelectAsync(h, JiraOutbox);
        Assert.False(h.Mailbox.Model.Grouped); // …but its outbox is not
        Assert.Equal("out", h.Fixture.ListRequests[^1].FolderId.Value);
        Assert.Empty(h.Fixture.ThreadListRequests);
    }

    [Fact]
    public async Task TogglingTheSettingKeepsTheJiraList()
    {
        var hints = new List<SelectionHint>();
        await using var h = await StartAsync(wire: h => h.List.RowsChanged += (_, u) => hints.Add(u.Hint));
        await SelectAsync(h, Web);
        var rows = h.List.Rows;
        var lists = h.Fixture.ThreadListRequests.Count;
        await h.On(() =>
        {
            h.List.Select(rows[1].Key);
            hints.Clear();
            h.Settings.GroupByConversation = true;
            h.Settings.GroupByConversation = false;
        });
        Assert.True(h.Mailbox.Model.Grouped);
        Assert.Equal(rows.Select(r => r.Key), h.List.Rows.Select(r => r.Key)); // the rows stay
        Assert.DoesNotContain(SelectionHint.Clear, hints); // the list is not emptied
        Assert.Equal(rows[1].Key, h.List.SelectedKey);
        await h.IdleAsync();
        Assert.Equal(lists, h.Fixture.ThreadListRequests.Count); // nothing to ask again

        // In the mail account the setting still switches the mode.
        await SelectAsync(h, Inbox);
        await h.On(() => h.Settings.GroupByConversation = true);
        Assert.True(h.Mailbox.Model.Grouped);
        await h.IdleAsync();
        Assert.Equal("in", h.Fixture.ThreadListRequests[^1].FolderId.Value);
    }

    [Fact]
    public async Task ASyncOfTheAccountReloadsItsVirtualFolder()
    {
        await using var h = await StartAsync();
        await SelectAsync(h, Assigned);
        var lists = h.Fixture.ThreadListRequests.Count;

        // A pass over the space WEB: the virtual folder shows its issues.
        await h.On(() =>
        {
            h.Mailbox.HandleSyncState(new SyncState { AccountId = JiraAccount, Status = SyncStatus.Syncing, FolderId = "web" });
            h.Mailbox.HandleSyncState(new SyncState { AccountId = JiraAccount, Status = SyncStatus.Idle, FolderId = "web" });
        });
        await h.IdleAsync();
        Assert.Equal(lists + 1, h.Fixture.ThreadListRequests.Count);
        Assert.Equal("assigned", h.Fixture.ThreadListRequests[^1].FolderId.Value);

        // A space is not reloaded for a pass over another folder.
        await SelectAsync(h, Web);
        var now = h.Fixture.ThreadListRequests.Count;
        await h.On(() =>
        {
            h.Mailbox.HandleSyncState(new SyncState { AccountId = JiraAccount, Status = SyncStatus.Syncing, FolderId = "assigned" });
            h.Mailbox.HandleSyncState(new SyncState { AccountId = JiraAccount, Status = SyncStatus.Idle, FolderId = "assigned" });
        });
        await h.IdleAsync();
        Assert.Equal(now, h.Fixture.ThreadListRequests.Count);
    }

    [Fact]
    public async Task RowsCarryTheIssue()
    {
        await using var h = await StartAsync();
        await SelectAsync(h, Web);
        // WEB-2's latest member is its status change.
        var t2 = h.List.Rows.First(r => r.Key.Thread == "issue-WEB-2").Summary!;
        var issue = MailModel.SummaryThread(t2, expanded: false, loading: false).Issue!;
        Assert.Equal(("WEB-2", "Summary of WEB-2", "To Do", JiraStatusStyle.Todo), (issue.Key, issue.Summary, issue.Status, issue.StatusStyle));
        Assert.True(issue.Event);
        Assert.Equal("Status: To Do → In Progress", issue.EventText);
        Assert.False(issue.Unread);

        // The mail rows have none.
        await SelectAsync(h, Inbox);
        Assert.Null(await h.On(() => h.Mailbox.Model.RowMessage(h.List.Rows[0].Message).Issue));
    }

    private static Account JiraAccountFixture() => new()
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

    private static Folder[] JiraFolders(int outboxTotal = 0) =>
    [
        TestFolder("assigned", "Assigned to Me", name: "Assigned to Me") with { AccountId = JiraAccount, Virtual = VirtualFolder.AssignedToMe },
        TestFolder("web", "WEB", name: "Web") with { AccountId = JiraAccount },
        TestFolder("out", "Outbox", FolderRole.Outbox, total: outboxTotal) with { AccountId = JiraAccount },
    ];

    private static IssueInfo Info(string key) => new()
    {
        Key = key,
        Url = "https://acme.atlassian.net/browse/" + key,
        Summary = "Summary of " + key,
        Status = "To Do",
        StatusCategory = IssueStatusCategory.Todo,
    };

    // A message of issue key (its thread) dated hours after Base.
    private static MessageSummary Item(string id, int hours, string key, IssueItemKind? kind = null, IssueChange[]? changes = null, params Flag[] flags) => new()
    {
        Id = id,
        AccountId = JiraAccount,
        FolderId = Web.Folder,
        ThreadId = "issue-" + key,
        From = [new Address { Name = "Jana Dvořáková", Email = "" }],
        Subject = key + ": Summary of " + key,
        Date = Base.AddHours(hours),
        Snippet = "p-" + id,
        Flags = flags,
        HasAttachments = false,
        Size = 0,
        Issue = MessageIssue.Of(Info(key), kind ?? IssueItemKind.Comment) with { Changes = changes ?? [] },
    };

    // Two issues: WEB-1 with a description and a comment, WEB-2 with a
    // description and a status change (the latest member).
    private static MessageSummary[] Items() =>
    [
        Item("w1d", 1, "WEB-1", IssueItemKind.Description, flags: Flag.Seen),
        Item("w1c", 4, "WEB-1"),
        Item("w2d", 2, "WEB-2", IssueItemKind.Description, flags: Flag.Seen),
        Item("w2e", 3, "WEB-2", IssueItemKind.Event, [new IssueChange { Field = IssueField.Status, From = "To Do", To = "In Progress" }], Flag.Seen),
    ];

    private static MessageSummary MailMessage(string id, int hours) => new()
    {
        Id = id,
        AccountId = MailAccount,
        FolderId = Inbox.Folder,
        ThreadId = "t1",
        From = [new Address { Name = "Petr", Email = "petr@example.invalid" }],
        Subject = "s-" + id,
        Date = Base.AddHours(hours),
        Snippet = "p-" + id,
        Flags = [Flag.Seen],
        HasAttachments = false,
        Size = 0,
    };

    private static async Task SelectAsync(MailboxControllerHarness h, FolderKey k)
    {
        await h.On(() => h.Mailbox.SelectFolder(k, fav: false));
        await h.IdleAsync();
        Assert.Equal(k, await h.On(() => h.Mailbox.Model.ListFolder));
    }

    // A fixture with a mail account (first, so its Inbox is the initial
    // folder) and a Jira account, a connected client and the two halves,
    // the grouping setting off.
    private static async Task<MailboxControllerHarness> StartAsync(Action<MailboxControllerHarness>? wire = null)
    {
        var h = await MailboxControllerHarness.StartAsync(
            f =>
            {
                f.SetAccounts([TestAccount("a", email: "a@example.invalid"), JiraAccountFixture()]);
                f.SetFolders([TestFolder("in", "INBOX", FolderRole.Inbox) with { AccountId = MailAccount }], MailAccount);
                f.SetFolders(JiraFolders(), JiraAccount);
                f.SetMessages([MailMessage("m1", 1), MailMessage("m2", 2)], MailAccount, Inbox.Folder);
                f.SetMessages(Items(), JiraAccount, Web.Folder);
                f.SetMessages([.. Items().Where(s => s.Issue?.Info.Key == "WEB-1")], JiraAccount, Assigned.Folder);
            },
            withList: true,
            wire: wire,
            prepare: s => s.GroupByConversation = false);
        await h.IdleAsync();
        Assert.Equal(Inbox, await h.On(() => h.Mailbox.Model.ListFolder));
        return h;
    }
}
