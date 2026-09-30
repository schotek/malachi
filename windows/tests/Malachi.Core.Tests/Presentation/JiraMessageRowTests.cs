// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The issue half of MessageRow: ui/internal/widget/message_row.go's
// applyIssue (no Go test of its own; the GTK row is checked by hand, macOS
// keeps it in AppKit's MessageCellView). A row of a Jira account shows the
// issue's key, its summary for the subject, the status and Internal pills;
// an event row is its actor and the change, without a preview or the
// subject line under its conversation; a conversation whose latest member
// is an event previews the change; a mail row shows none of it.

using System;
using Malachi.Core.Api;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Malachi.Core.Tests.Model;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class JiraMessageRowTests
{
    private static readonly RowAppearance Look = new(Compact: false, ShowPreview: true, ShowAvatars: true, Monochrome: false, Grouped: true);

    [Fact]
    public void AnIssueCommentShowsItsKeySummaryAndPills()
    {
        var s = JiraRowTests.Item("c1", 1, "ITSD-7", issue: JiraRowTests.Issue("ITSD-7", "Done", IssueStatusCategory.Done));
        s = s with { Issue = s.Issue! with { Visibility = CommentVisibility.Internal } };
        var row = Row(Plain(s));
        Assert.Equal(("ITSD-7", "Summary of ITSD-7"), (row.IssueKey, row.Subject));
        Assert.Equal(("Done", JiraStatusStyle.Done, "Internal"), (row.StatusText, row.StatusStyle, row.InternalText));
        Assert.False(row.IsEventRow);
        Assert.True(row.SubjectLineShown && row.ShowPreview);
        Assert.Equal("", row.EventText);
        Assert.True(row.Unread);
        Assert.Equal(string.Join(", ", "Jana Dvořáková", "ITSD-7", "Summary of ITSD-7", "Done", "Internal", "Unread", row.DateText, "p-c1"), row.ToString());
    }

    [Fact]
    public void AnEventRowIsItsActorAndTheChange()
    {
        var s = JiraRowTests.Item("e1", 2, "ITSD-7", IssueItemKind.Event, changes: [new IssueChange { Field = IssueField.Assignee, From = "", To = "Petr Svoboda" }]);
        var row = Row(Plain(s));
        Assert.True(row.IsEventRow);
        Assert.Equal("→ Assignee: Unassigned → Petr Svoboda", row.EventText);
        Assert.Equal("Assignee: Unassigned → Petr Svoboda", row.EventTooltip);
        Assert.False(row.ShowPreview);
        Assert.False(row.Unread); // an event is never unread
        Assert.True(row.SubjectLineShown);
        Assert.Equal("Jana Dvořáková", row.Sender);

        // Under its conversation: one line.
        var member = Row(Plain(s, member: true));
        Assert.True(member.IsEventRow && !member.SubjectLineShown);
        Assert.Equal(string.Join(", ", "Jana Dvořáková", "Assignee: Unassigned → Petr Svoboda", member.DateText), member.ToString());
    }

    [Fact]
    public void AConversationWhoseLatestIsAnEventPreviewsTheChange()
    {
        var items = JiraRowTests.Items();
        var t = new ThreadSummary
        {
            Id = "issue-WEB-2",
            AccountId = "j",
            Subject = items[3].Subject,
            Participants = items[3].From,
            MessageCount = 2,
            UnreadCount = 0,
            LatestDate = items[3].Date,
            Latest = items[3],
            Snippet = items[3].Snippet,
            Flags = [Flag.Seen],
            HasAttachments = false,
            FolderIds = ["web"],
            Issue = JiraRowTests.Issue("WEB-2"),
        };
        var r = new ListRow { Key = new ListKey(Thread: t.Id), Thread = true, Message = t.Latest, Summary = t };
        var row = new MessageRow(r.Key);
        row.Update(r, MailModel.SummaryMessage(t.Latest), Look, t.LatestDate);
        Assert.True(row.IsThread);
        Assert.False(row.IsEventRow);
        Assert.Equal(("WEB-2", "Summary of WEB-2", "To Do"), (row.IssueKey, row.Subject, row.StatusText));
        Assert.Equal("Status: To Do → In Progress", row.Preview);
        Assert.True(row.ShowPreview && row.SubjectLineShown);
    }

    [Fact]
    public void AMailRowHasNoIssueParts()
    {
        var jira = JiraRowTests.Item("c1", 1, "ITSD-7");
        var row = Row(Plain(jira));
        var mail = MailModelTests.Summary("m1") with { Subject = "Hello" };
        row.Update(Plain(mail), MailModel.SummaryMessage(mail), Look, mail.Date);
        Assert.Equal(("", "Hello", "", JiraStatusStyle.Plain, ""), (row.IssueKey, row.Subject, row.StatusText, row.StatusStyle, row.InternalText));
        Assert.False(row.IsEventRow);
        Assert.True(row.SubjectLineShown && row.ShowPreview);
    }

    private static MessageRow Row(ListRow r)
    {
        var row = new MessageRow(r.Key);
        row.Update(r, MailModel.SummaryMessage(r.Message), Look, r.Message.Date);
        return row;
    }

    private static ListRow Plain(MessageSummary s, bool member = false) =>
        new() { Key = new ListKey(s.ThreadId, s.Id), Message = s, Member = member };
}
