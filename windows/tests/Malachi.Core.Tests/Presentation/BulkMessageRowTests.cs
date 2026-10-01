// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The bulk half of the list rows: ui/internal/window/model.go
// (summaryMessage's Tag), thread_model.go (summaryThread's Tag) and
// ui/internal/widget/message_row.go (MessageRow.tag, applyIssue's
// SetBulkPill; no Go test of its own, the GTK row is checked by hand). A
// bulk message has a neutral tag after its subject, a conversation the tag
// of its latest member, a search result its message's, and a row of an
// issue never one.

using System;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Malachi.Core.Tests.Model;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class BulkMessageRowTests
{
    private static readonly RowAppearance Look = new(Compact: false, ShowPreview: true, ShowAvatars: true, Monochrome: false, Grouped: true);

    private static MessageSummary Mail(string id, BulkKind? kind) =>
        MailModelTests.Summary(id) with { Bulk = kind is { } k ? new BulkInfo { Kind = k, Domain = "shop.example" } : null };

    [Theory]
    [InlineData(BulkKind.Newsletter, "Bulk")]
    [InlineData(BulkKind.List, "Mailing List")]
    [InlineData(BulkKind.Automated, "Automated")]
    [InlineData("weird", "")]
    public void AMessageRowCarriesTheTagOfItsKind(string kind, string tag)
    {
        var s = Mail("m1", kind);
        Assert.Equal(tag, MailModel.SummaryMessage(s).Tag);
        var row = Row(s);
        Assert.Equal(tag, row.BulkText);
    }

    [Fact]
    public void PersonalMailHasNoTag()
    {
        var s = Mail("m1", null);
        Assert.Equal("", MailModel.SummaryMessage(s).Tag);
        Assert.Equal("", Row(s).BulkText);
    }

    [Fact]
    public void AConversationShowsTheTagOfItsLatestMember()
    {
        var latest = Mail("m2", BulkKind.List);
        var t = ThreadModelTests.Thr("t1", 3, 1, latest);
        Assert.Equal("Mailing List", MailModel.SummaryThread(t, expanded: false, loading: false).Tag);
        var r = new ListRow { Key = new ListKey(Thread: t.Id), Thread = true, Message = t.Latest, Summary = t };
        var row = new MessageRow(r.Key);
        row.Update(r, MailModel.SummaryMessage(t.Latest), Look, t.LatestDate);
        Assert.True(row.IsThread);
        Assert.Equal("Mailing List", row.BulkText);

        // An older member's tag does not count: the latest decides.
        var personal = ThreadModelTests.Thr("t2", 2, 0, Mail("m3", null));
        Assert.Equal("", MailModel.SummaryThread(personal, expanded: false, loading: false).Tag);
    }

    [Fact]
    public void TheTagChangesWithTheRowAndNeverShowsOnAnIssue()
    {
        var key = new ListKey("t", "m1");
        var row = new MessageRow(key);
        var bulk = Mail("m1", BulkKind.Newsletter);
        row.Update(Plain(bulk), MailModel.SummaryMessage(bulk), Look, bulk.Date);
        Assert.Equal("Bulk", row.BulkText);
        Assert.Contains("Bulk", row.ToString(), StringComparison.Ordinal);

        // The same row key shows another message: the tag follows.
        var mail = Mail("m1", null);
        row.Update(Plain(mail), MailModel.SummaryMessage(mail), Look, mail.Date);
        Assert.Equal("", row.BulkText);

        // A row of an issue hides the tag it was given (message_row.go SetBulkPill(..., issue == nil)).
        var item = JiraRowTests.Item("c1", 1, "ITSD-7") with { Bulk = new BulkInfo { Kind = BulkKind.Automated } };
        var issueRow = new MessageRow(new ListKey(item.ThreadId, item.Id));
        issueRow.Update(Plain(item), MailModel.SummaryMessage(item), Look, item.Date);
        Assert.Equal("Automated", MailModel.SummaryMessage(item).Tag);
        Assert.Equal("", issueRow.BulkText);
    }

    private static MessageRow Row(MessageSummary s)
    {
        var row = new MessageRow(new ListKey(s.ThreadId, s.Id));
        row.Update(Plain(s), MailModel.SummaryMessage(s), Look, s.Date);
        return row;
    }

    private static ListRow Plain(MessageSummary s) => new() { Key = new ListKey(s.ThreadId, s.Id), Message = s };
}
