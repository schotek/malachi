// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of the Jira half of ReaderController: ui/internal/window/
// message_view.go (renderHeaders' card and subject, renderEvent, applyIssue,
// showMessage's readsWithoutBody) and issue_card.go (show, setBusy), which
// GTK and macOS check by hand. A message of a Jira account gets the card
// and its issue's summary as the subject, an event is its changes without
// a body fetch, a transition's refreshed issue reaches the card at once,
// and the spinner follows the running transition of the card's issue.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Presentation;
using Malachi.Core.Tests.Model;
using Microsoft.Extensions.Time.Testing;
using Xunit;
using static Malachi.Core.Tests.Presentation.ReaderFixtures;

namespace Malachi.Core.Tests.Presentation;

public sealed class ReaderControllerIssueTests
{
    private const string Site = "https://acme.atlassian.net";

    private static readonly AccountId Acc = ReaderFixtures.Account;

    private readonly FakeTimeProvider clock = new();
    private readonly FakeReaderCache cache = new();
    private readonly HashSet<(AccountId, string)> busy = [];

    [Fact]
    public void ACommentHasTheCardAndTheSummaryAsItsSubject()
    {
        var r = Make();
        var s = Comment("c1");
        r.Show(s);
        var card = r.IssueCard!;
        Assert.Equal(("ITSD-42", "In Progress", JiraStatusStyle.InProgress), (card.Card.Key, card.Card.Status, card.Card.StatusStyle));
        Assert.True(card.Openable); // a link to its own site
        Assert.True(card.Menu); // the account changes statuses
        Assert.False(card.Busy);
        Assert.Equal(Acc, card.Account);
        Assert.Equal("The printer on the third floor", r.Subject);
        Assert.Equal(1, cache.FetchCalls); // a comment has a body

        // The full message wins, as it arrives.
        cache.Entry("c1").Msg = Message(s with { Issue = s.Issue! with { Summary = "The printer is fixed" } });
        cache.Settle("c1");
        Assert.Equal("The printer is fixed", r.Subject);
    }

    [Fact]
    public void WithoutTheCapabilityThePillIsPlainAndAKeyOfAnotherSiteOpensNothing()
    {
        var r = Make(transitions: false);
        r.Show(Comment("c1") with { Issue = Comment("c1").Issue! with { Url = "https://evil.example/browse/ITSD-42" } });
        Assert.False(r.IssueCard!.Menu);
        Assert.False(r.IssueCard.Openable);
    }

    [Fact]
    public void AnEventIsItsChangesWithoutAFetch()
    {
        var r = Make();
        var s = Comment("e1") with
        {
            Issue = Comment("e1").Issue! with
            {
                Item = IssueItemKind.Event,
                Changes = [new IssueChange { Field = IssueField.Status, From = "To Do", To = "In Progress" }],
            },
        };
        r.Show(s);
        Assert.Equal(0, cache.FetchCalls);
        Assert.Equal(ReaderBodyPage.Text, r.BodyPage);
        Assert.Equal("Status: To Do → In Progress", r.BodyText);
        Assert.NotNull(r.IssueCard);
        clock.Advance(TimeSpan.FromSeconds(5));
        Assert.Equal(ReaderBodyPage.Text, r.BodyPage); // no spinner
    }

    [Fact]
    public void MailHasNoCard()
    {
        var r = Make();
        r.Show(Comment("c1"));
        Assert.NotNull(r.IssueCard);
        r.Show(Summary("m1", "Plans"));
        Assert.Null(r.IssueCard);
        Assert.Equal("Plans", r.Subject);
        r.Show(Comment("c1"));
        r.Clear();
        Assert.Null(r.IssueCard);
    }

    [Fact]
    public void ATransitionReachesTheCardAtOnce()
    {
        var r = Make();
        r.Show(Comment("c1"));
        var done = JiraRowTests.Issue("ITSD-42", "Done", IssueStatusCategory.Done) with { Summary = "The printer is fixed" };
        // Another account's issue, and another issue, change nothing.
        r.ApplyIssue("other", done);
        r.ApplyIssue(Acc, done with { Key = "ITSD-7" });
        Assert.Equal("In Progress", r.IssueCard!.Card.Status);
        r.ApplyIssue(Acc, done);
        Assert.Equal(("Done", JiraStatusStyle.Done), (r.IssueCard!.Card.Status, r.IssueCard.Card.StatusStyle));
        Assert.Equal("The printer is fixed", r.Subject);
        Assert.Equal("Internal", r.IssueCard.Card.InternalLabel); // the comment's own members stay
    }

    [Fact]
    public void TheSpinnerFollowsTheTransitionOfTheCardsIssue()
    {
        var r = Make();
        r.Show(Comment("c1"));
        busy.Add((Acc, "ITSD-7"));
        r.IssueBusyChanged(Acc, "ITSD-7");
        Assert.False(r.IssueCard!.Busy); // another issue's
        busy.Add((Acc, "ITSD-42"));
        r.IssueBusyChanged(Acc, "ITSD-42");
        Assert.True(r.IssueCard!.Busy);
        busy.Clear();
        r.IssueBusyChanged(Acc, "ITSD-42");
        Assert.False(r.IssueCard!.Busy);

        // A card shown while a transition runs shows it at once.
        busy.Add((Acc, "ITSD-42"));
        r.Show(Summary("m1"));
        r.Show(Comment("c1"));
        Assert.True(r.IssueCard!.Busy);
    }

    [Fact]
    public void AnAttachedMessageHasNoCard()
    {
        var r = Make(ReaderMode.Embedded);
        var s = Comment("c1");
        r.ShowEmbedded(Summary("m1"), Attachment("2", "note.eml", "message/rfc822"), new MessageEmbeddedResult
        {
            Message = Message(s),
            Body = TextBody("c1"),
            PartId = "2",
        });
        Assert.Null(r.IssueCard);
    }

    private ReaderController Make(ReaderMode mode = ReaderMode.Pane, bool transitions = true) => new(mode, cache, timeProvider: clock)
    {
        IssueSite = a => a == Acc ? Site : "",
        CanTransition = _ => transitions,
        IssueBusy = (a, k) => busy.Contains((a, k)),
    };

    // An internal comment on the service-desk request ITSD-42.
    private static MessageSummary Comment(string id) => Summary(id, "ITSD-42: The printer on the third floor") with
    {
        Issue = MessageIssue.Of(
            new IssueInfo
            {
                Key = "ITSD-42",
                Url = Site + "/browse/ITSD-42",
                Summary = "The printer on the third floor",
                Status = "In Progress",
                StatusCategory = IssueStatusCategory.InProgress,
            },
            IssueItemKind.Comment) with
        { Visibility = CommentVisibility.Internal },
    };
}
