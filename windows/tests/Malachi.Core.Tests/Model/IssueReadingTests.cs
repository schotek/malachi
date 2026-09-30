// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/JiraReaderTests.swift, the
// counterpart of ui/internal/window/issue_reading_test.go
// (TestReadIssueMailHasNone, TestReadIssueComment,
// TestReadIssueTheFullMessageWins,
// TestReadIssueEmptySummaryFallsBackToTheSubject,
// TestReadIssueTheKeyOpensOnlyTheAccountsSite,
// TestReadIssueAnEventIsItsChanges): what the reading pane shows of a Jira
// message.

using System;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Model;
using Malachi.Core.Tests.IssueTrackers;
using Xunit;

namespace Malachi.Core.Tests.Model;

public sealed class IssueReadingTests
{
    private const string Site = "https://acme.atlassian.net";

    private static IssueInfo Info(string key, string summary, string url = "") => new()
    {
        Key = key,
        Url = url.Length == 0 ? Site + "/browse/" + key : url,
        Summary = summary,
        Status = "In Progress",
        StatusCategory = IssueStatusCategory.InProgress,
        Priority = "High",
        Assignee = "Jana Dvořáková",
    };

    private static MessageSummary Message(MessageIssue? issue, string subject) => new()
    {
        Id = "m1",
        AccountId = "j",
        FolderId = "f",
        Subject = subject,
        Date = DateTimeOffset.UnixEpoch,
        Snippet = "",
        Flags = [Flag.Seen],
        HasAttachments = false,
        Size = 0,
        Issue = issue,
    };

    [Fact]
    public void MailHasNone()
    {
        var s = Message(null, "Hello");
        Assert.Null(IssueReading.Read(s, null, Site));
        Assert.False(IssueReading.ReadsWithoutBody(s));
    }

    [Fact]
    public void Comment()
    {
        var item = MessageIssue.Of(Info("ITSD-42", "VPN drops every 10 minutes"), IssueItemKind.Comment) with
        {
            Visibility = CommentVisibility.Internal,
            Via = "Issue Sync",
            Edited = true,
        };
        var r = IssueReading.Read(Message(item, "ITSD-42: VPN drops every 10 minutes"), null, Site)!;
        Assert.Equal("VPN drops every 10 minutes", r.Subject); // the summary, the key is on the card
        var c = r.Card;
        Assert.Equal(("ITSD-42", "In Progress", JiraStatusStyle.InProgress), (c.Key, c.Status, c.StatusStyle));
        Assert.Equal((true, "Internal", "via Issue Sync", "Edited"), (c.Internal, c.InternalLabel, c.Via, c.Edited));
        Assert.Equal(["Assignee", "Priority", "Type", "Reporter"], c.Rows.Select(row => row.Label));
        Assert.Equal(["Jana Dvořáková", "High", "None", "None"], c.Rows.Select(row => row.Value));
        Assert.True(r.Openable); // the key opens its own site's issue
        Assert.Null(r.EventBody);
        Assert.False(IssueReading.ReadsWithoutBody(Message(item, "")));
    }

    [Fact]
    public void TheFullMessageWins()
    {
        var old = MessageIssue.Of(Info("ITSD-42", "Old summary"), IssueItemKind.Description);
        var fresh = MessageIssue.Of(Info("ITSD-42", "New summary"), IssueItemKind.Description);
        var full = new Message { Summary = Message(fresh, "ITSD-42: New summary") };
        Assert.Equal("New summary", IssueReading.Read(Message(old, "ITSD-42: Old summary"), full, Site)?.Subject);
        // The issue only in the full message still counts.
        Assert.Equal("New summary", IssueReading.Read(Message(null, ""), full, Site)?.Subject);
    }

    [Fact]
    public void EmptySummaryFallsBackToTheSubject()
    {
        var item = MessageIssue.Of(Info("ITSD-42", " " + JiraTests.Rlo + " "), IssueItemKind.Description);
        var r = IssueReading.Read(Message(item, "ITSD-42"), null, Site)!;
        Assert.Equal("", r.Card.Summary);
        Assert.Equal("ITSD-42", r.Subject);
    }

    [Theory]
    [InlineData("own site", Site + "/browse/ITSD-42", true)]
    [InlineData("another site", "https://evil.example/browse/ITSD-42", false)]
    [InlineData("user info", "https://acme.atlassian.net@evil.example/browse/ITSD-42", false)]
    [InlineData("http on an https site", "http://acme.atlassian.net/browse/ITSD-42", false)]
    [InlineData("javascript", "javascript:alert(1)", false)]
    public void TheKeyOpensOnlyTheAccountsSite(string name, string url, bool want)
    {
        var item = MessageIssue.Of(Info("ITSD-42", "S", url), IssueItemKind.Comment);
        Assert.True(want == IssueReading.Read(Message(item, ""), null, Site)!.Openable, name);
    }

    [Fact]
    public void NothingOpensWithoutAUrlOrASite()
    {
        var empty = MessageIssue.Of(new IssueInfo { Key = "ITSD-42", Url = "", Summary = "", Status = "" }, IssueItemKind.Comment);
        Assert.False(IssueReading.Read(Message(empty, ""), null, Site)!.Openable);
        var item = MessageIssue.Of(Info("ITSD-42", "S"), IssueItemKind.Comment);
        Assert.False(IssueReading.Read(Message(item, ""), null, "")!.Openable); // the account is gone
    }

    [Fact]
    public void AnEventIsItsChanges()
    {
        var item = MessageIssue.Of(Info("ITSD-42", "S"), IssueItemKind.Event) with
        {
            Changes =
            [
                new IssueChange { Field = IssueField.Status, From = "To Do", To = "In Progress" },
                new IssueChange { Field = "resolution", To = "Fixed" },
                new IssueChange { Field = IssueField.Assignee, From = "Jana Dvořáková" },
            ],
        };
        var s = Message(item, "");
        Assert.True(IssueReading.ReadsWithoutBody(s));
        Assert.Equal("Status: To Do → In Progress\nAssignee: Jana Dvořáková → Unassigned", IssueReading.Read(s, null, Site)!.EventBody);
        // An event of changes this client does not know: an empty body.
        var unknown = MessageIssue.Of(Info("ITSD-42", "S"), IssueItemKind.Event) with { Changes = [new IssueChange { Field = "labels", To = "x" }] };
        Assert.Equal("", IssueReading.Read(Message(unknown, ""), null, Site)!.EventBody);
    }
}
