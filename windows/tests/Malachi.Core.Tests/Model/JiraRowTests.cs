// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/JiraListTests.swift (JiraRowTests),
// the counterpart of ui/internal/window/jira_list_test.go
// (TestJiraMessageRowProjection, TestJiraSearchRowsCarryTheIssue,
// TestJiraApplyNewMessageCarriesTheIssue,
// TestJiraAlwaysGroupedFollowsTheAccountKind): the row projections of a
// Jira account's messages and the model's changes in place, without a
// daemon. The rows carry the issue, an event never shows as unread, and a
// notified message brings the issue as it is now. The list over a daemon
// (the rest of JiraListTests) is in the controller tests.

using System;
using Malachi.Core.Api;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Model;
using Xunit;
using static Malachi.Core.Tests.Model.MailModelTests;

namespace Malachi.Core.Tests.Model;

public sealed class JiraRowTests
{
    private static readonly AccountId JiraId = "j";
    private static readonly FolderKey Web = new("j", "web");

    // 2026-09-01T10:00:00Z.
    private static readonly DateTimeOffset Base = DateTimeOffset.FromUnixTimeSeconds(1_788_256_800);

    internal static Account JiraAccount() => new()
    {
        Id = JiraId,
        Config = new AccountConfig
        {
            Name = "Acme Jira",
            Email = "jana@acme.example",
            Kind = AccountKind.Jira,
            Jira = new JiraConfig { SiteUrl = "https://acme.atlassian.net", Deployment = JiraDeployment.Cloud },
        },
        Enabled = true,
        State = new SyncState { AccountId = JiraId, Status = SyncStatus.Idle },
        Capabilities = [],
    };

    internal static IssueInfo Issue(string key, string status = "To Do", IssueStatusCategory? category = null) => new()
    {
        Key = key,
        Url = "https://acme.atlassian.net/browse/" + key,
        Summary = "Summary of " + key,
        Status = status,
        StatusCategory = category ?? IssueStatusCategory.Todo,
    };

    // A message of issue key (its thread) dated hours after Base.
    internal static MessageSummary Item(
        string id, int hours, string key, IssueItemKind? kind = null, IssueInfo? issue = null,
        IssueChange[]? changes = null, params Flag[] flags)
    {
        var info = issue ?? Issue(key);
        return new MessageSummary
        {
            Id = id,
            AccountId = JiraId,
            FolderId = Web.Folder,
            ThreadId = "issue-" + key,
            From = [new Address { Name = "Jana Dvořáková", Email = "" }],
            Subject = key + ": " + info.Summary,
            Date = Base.AddHours(hours),
            Snippet = "p-" + id,
            Flags = flags,
            HasAttachments = false,
            Size = 0,
            Issue = MessageIssue.Of(info, kind ?? IssueItemKind.Comment) with { Changes = changes ?? [] },
        };
    }

    // Two issues: WEB-1 with a description and a comment, WEB-2 with a
    // description and a status change (the latest member).
    internal static MessageSummary[] Items() =>
    [
        Item("w1d", 1, "WEB-1", IssueItemKind.Description, flags: Flag.Seen),
        Item("w1c", 4, "WEB-1"),
        Item("w2d", 2, "WEB-2", IssueItemKind.Description, flags: Flag.Seen),
        Item("w2e", 3, "WEB-2", IssueItemKind.Event, changes: [new IssueChange { Field = IssueField.Status, From = "To Do", To = "In Progress" }], flags: Flag.Seen),
    ];

    [Fact]
    public void MessageRowProjection()
    {
        var row = MailModel.SummaryMessage(Item("c1", 1, "ITSD-7", issue: Issue("ITSD-7", "Done", IssueStatusCategory.Done)));
        var r = row.Issue!;
        Assert.Equal(("ITSD-7", "Summary of ITSD-7", "Done", JiraStatusStyle.Done, false), (r.Key, r.Summary, r.Status, r.StatusStyle, r.Event));
        Assert.True(row.Unread && r.Unread, "an unseen comment is unread");

        // An event is never unread, whatever its flags.
        var er = MailModel.SummaryMessage(Item("e1", 2, "ITSD-7", IssueItemKind.Event, changes: [new IssueChange { Field = IssueField.Assignee, From = "", To = "Jana Dvořáková" }]));
        Assert.False(er.Unread);
        Assert.True(er.Issue?.Event);
        Assert.Equal("Assignee: Unassigned → Jana Dvořáková", er.Issue?.EventText);

        // An internal service-desk comment has the badge.
        var note = Item("i1", 3, "ITSD-7", flags: Flag.Seen);
        note = note with { Issue = note.Issue! with { Visibility = CommentVisibility.Internal } };
        Assert.True(MailModel.SummaryMessage(note).Issue?.Internal);
        Assert.Equal("Internal", MailModel.SummaryMessage(note).Issue?.InternalLabel);

        // A mail message has no issue and keeps its unread state.
        var mail = Summary("m1", Flag.Seen);
        Assert.Null(MailModel.SummaryMessage(mail).Issue);
        Assert.False(MailModel.SummaryMessage(mail).Unread);
    }

    [Fact]
    public void SearchRowsCarryTheIssue()
    {
        var model = new MailModel();
        var s = Item("c1", 1, "WEB-3");
        model.Search.Active = true;
        model.SetSearchResults(new SearchQueryResult
        {
            Results = [new SearchResult { Message = s, Snippet = "a hit", Ranges = [], Score = 1 }],
            Page = new PageInfo { Total = 1 },
        });
        var row = model.RowMessage(s);
        Assert.Equal("WEB-3", row.Issue?.Key);
        Assert.Equal("a hit", row.Snippet);
    }

    [Fact]
    public void ApplyNewMessageCarriesTheIssue()
    {
        var model = new MailModel(grouped: true) { ListFolder = Web };
        var items = Items();
        var t1 = new ThreadSummary
        {
            Id = "issue-WEB-1",
            AccountId = JiraId,
            Subject = items[1].Subject,
            Participants = items[1].From,
            MessageCount = 2,
            UnreadCount = 1,
            LatestDate = items[1].Date,
            Latest = items[1],
            Snippet = items[1].Snippet,
            Flags = [],
            HasAttachments = false,
            FolderIds = ["web"],
            Issue = Issue("WEB-1"),
        };
        model.SetThreads([t1], new PageInfo { Total = 1 });

        // A new comment comes with the issue as it is now: the row follows.
        var moved = Issue("WEB-1", "In Progress", IssueStatusCategory.InProgress);
        model.ApplyNewMessage(Item("w1n", 6, "WEB-1", issue: moved), MessageFilter.All, new ListKey());
        var t = model.Threads[0];
        Assert.Equal(moved, t.Issue); // the fields, all scalars
        Assert.Equal((3, 2), (t.MessageCount, t.UnreadCount));
        Assert.Equal("In Progress", MailModel.SummaryThread(t, expanded: false, loading: false).Issue?.Status);

        // An event, even one delivered unseen, is never unread.
        var done = Issue("WEB-1", "Done", IssueStatusCategory.Done);
        var ev = Item("w1e", 7, "WEB-1", IssueItemKind.Event, done, [new IssueChange { Field = IssueField.Status, From = "In Progress", To = "Done" }]);
        model.ApplyNewMessage(ev, MessageFilter.All, new ListKey());
        t = model.Threads[0];
        Assert.Equal(2, t.UnreadCount);
        Assert.Equal(IssueStatusCategory.Done, t.Issue?.StatusCategory);
        Assert.True(MailModel.SummaryThread(t, expanded: false, loading: false).Issue?.Event);

        // A new conversation starts with the issue of its first message.
        model.ApplyNewMessage(Item("w9", 8, "WEB-9"), MessageFilter.All, new ListKey());
        var t9 = model.Threads[0];
        Assert.Equal(("issue-WEB-9", "WEB-9", 1), (t9.Id.Value, t9.Issue?.Key, t9.UnreadCount));
        var unseenEvent = Item("w8e", 9, "WEB-8", IssueItemKind.Event, changes: [new IssueChange { Field = IssueField.Status, From = "A", To = "B" }]);
        model.ApplyNewMessage(unseenEvent, MessageFilter.All, new ListKey());
        Assert.Equal(0, model.Threads[0].UnreadCount);

        // A message without an issue leaves the conversation's alone.
        var plain = new MessageSummary
        {
            Id = "p1",
            AccountId = JiraId,
            FolderId = "web",
            ThreadId = "issue-WEB-9",
            From = [],
            Subject = "x",
            Date = Base.AddHours(10),
            Snippet = "",
            Flags = [Flag.Seen],
            HasAttachments = false,
            Size = 0,
        };
        model.ApplyNewMessage(plain, MessageFilter.All, new ListKey());
        Assert.Equal("WEB-9", Array.Find([.. model.Threads], x => x.Id == "issue-WEB-9")?.Issue?.Key);
    }

    [Fact]
    public void AlwaysGroupedFollowsTheAccountKind()
    {
        var model = new MailModel([TestAccount("a"), JiraAccount()]);
        Assert.True(model.AlwaysGrouped(new FolderKey("j", "anything")));
        Assert.False(model.AlwaysGrouped(new FolderKey("a", "in")));
        Assert.False(model.AlwaysGrouped(new FolderKey("gone", "in")));
        Assert.False(model.AlwaysGrouped(null));
        model.Accounts = [];
        Assert.False(model.AlwaysGrouped(Web));
    }

    [Fact]
    public void CountsUnread()
    {
        Assert.True(MailModel.CountsUnread(Item("c", 1, "WEB-1")));
        Assert.False(MailModel.CountsUnread(Item("c", 1, "WEB-1", flags: Flag.Seen)));
        Assert.False(MailModel.CountsUnread(Item("e", 1, "WEB-1", IssueItemKind.Event)));
        Assert.True(MailModel.CountsUnread(Summary("m")));
    }
}
