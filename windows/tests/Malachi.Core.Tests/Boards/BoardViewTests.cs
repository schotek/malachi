// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/BoardModelTests.swift
// (BoardViewTests); GTK: ui/internal/board/model_test.go (TestOrderIs…
// through TestSelectionAfterDone, with Go's DueOverdue checks).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.IssueTrackers;
using Xunit;
using static Malachi.Core.Boards.Board;
using BText = Malachi.Core.Boards.Board.Text;
using F = Malachi.Core.Tests.Boards.BoardFixture;

namespace Malachi.Core.Tests.Boards;

public sealed class BoardViewTests
{
    // Ordering and sections

    [Fact]
    public void OrderIsStateThenNewestThenId()
    {
        Case[] cases =
        [
            F.Mk("c1", State.You, hours: 5), F.Mk("c2", State.Hot, hours: 9), F.Mk("c3", State.You, hours: 1),
            F.Mk("c4", State.Info, hours: 3), F.Mk("c6", State.Them, hours: 2), F.Mk("c5", State.Them, hours: 2),
        ];
        var v = F.View(cases);
        Assert.Equal(
            ["state(Hot)", "state(You)", "state(Them)", "state(Info)"],
            v.Sections.Select(F.KindOf));
        Assert.Equal([["c2"], ["c3", "c1"], ["c5", "c6"], ["c4"]], v.Sections.Select(s => F.Ids(s.Rows)));
        Assert.Equal(["Hot", "Waiting for You", "Waiting for Them", "For Your Information"], v.Sections.Select(s => s.Title));
    }

    [Fact]
    public void SectionsPerFilter()
    {
        Case[] cases =
        [
            F.Mk("c1", State.You), F.Mk("c2", State.Hot), F.Mk("c3", State.Info, hours: 2, done: true),
            F.Mk("c4", State.Them, hours: 4, done: true), F.Mk("c5", State.Them, hours: 3),
        ];
        var all = F.View(cases);
        // info is done: no empty section
        Assert.Equal(["state(Hot)", "state(You)", "state(Them)"], all.Sections.Select(F.KindOf));
        var you = F.View(cases, configure: v => v with { Filter = Filter.Of(State.You) });
        Assert.Equal(["state(You)"], you.Sections.Select(F.KindOf));
        Assert.Equal(["c1"], F.Ids(you.Sections[0].Rows));
        var none = F.View(cases, configure: v => v with { Filter = Filter.Of(State.Info) });
        Assert.Empty(none.Sections);
        Assert.Equal("Nothing here.", none.SectionsEmptyText);
        var done = F.View(cases, configure: v => v with { Filter = Filter.Done });
        Assert.Equal(["done"], done.Sections.Select(F.KindOf));
        Assert.Equal("Done", done.Sections[0].Title);
        Assert.Equal(["c3", "c4"], F.Ids(done.Sections[0].Rows)); // newest first
        var emptyDone = F.View([F.Mk("c1")], configure: v => v with { Filter = Filter.Done });
        Assert.Empty(emptyDone.Sections);
    }

    [Fact]
    public void AnnotationDecidesTheSectionOnlyWhenAnnotated()
    {
        var ann = new Annotation { State = State.Hot, Title = "Burning" };
        Case[] cases = [F.Mk("c1", State.Info, annotation: ann)];
        Assert.Equal(["state(Hot)"], F.View(cases, annotated: true).Sections.Select(F.KindOf));
        Assert.Equal(["state(Info)"], F.View(cases, annotated: false).Sections.Select(F.KindOf));
    }

    [Fact]
    public void ColumnsAreAlwaysFourWhateverTheFilter()
    {
        Case[] cases = [F.Mk("c1", State.You), F.Mk("c2", State.Hot, done: true)];
        foreach (var filter in new[] { Filter.All, Filter.Of(State.Hot), Filter.Done })
        {
            var v = F.View(cases, configure: x => x with { Filter = filter, Style = BoardStyle.Columns });
            Assert.Equal(States, v.Columns.Select(c => c.State));
            Assert.Equal([[], ["c1"], [], []], v.Columns.Select(c => F.Ids(c.Rows)));
            Assert.Equal(["Nothing burning.", "Empty.", "Empty.", "Empty."], v.Columns.Select(c => c.EmptyText));
            Assert.Equal(States.Select(BText.StateName), v.Columns.Select(c => c.Title));
        }
    }

    // Scope, counts

    [Fact]
    public void AccountScopeAndCounts()
    {
        Case[] cases =
        [
            F.Mk("c1", State.Hot), F.Mk("c2", State.You, account: F.AccountB), F.Mk("c3", State.You, account: F.AccountB, hours: 2),
            F.Mk("c4", State.Info, account: F.AccountB, done: true), F.Mk("c5", State.Them, done: true),
        ];
        var all = F.View(cases);
        Assert.Equal([3, 1, 2, 0, 0, 0, 2], all.Nav.Select(n => n.Count));
        Assert.Equal(["Overview", "Hot", "Waiting for You", "Waiting for Them", "For Your Information", "Snoozed", "Done"], all.Nav.Select(n => n.Title));
        Assert.Equal([null, State.Hot, State.You, State.Them, State.Info, null, null], all.Nav.Select(n => n.Dot));
        Assert.Equal([true, false, false, false, false, false, false], all.Nav.Select(n => n.Selected));
        Assert.Equal(["All Accounts", "Alpha", "Beta"], all.Accounts.Select(a => a.Title));
        Assert.Equal(["", "IMAP", "JIRA"], all.Accounts.Select(a => a.Badge));
        Assert.Equal(["All Accounts", "Alpha (IMAP)", "Beta (JIRA)"], all.Accounts.Select(a => a.Label));
        Assert.Equal([3, 1, 2], all.Accounts.Select(a => a.Count));
        Assert.Equal([true, false, false], all.Accounts.Select(a => a.Selected));
        Assert.Equal("All Accounts · 3 cases", all.Subtitle);

        // The account filter narrows the cases; the account list keeps its own counts.
        var b = F.View(cases, configure: v => v with { Account = F.AccountB, Filter = Filter.Done });
        Assert.Equal([2, 0, 2, 0, 0, 0, 1], b.Nav.Select(n => n.Count));
        Assert.Equal([false, false, false, false, false, false, true], b.Nav.Select(n => n.Selected));
        Assert.Equal([3, 1, 2], b.Accounts.Select(a => a.Count));
        Assert.Equal([false, false, true], b.Accounts.Select(a => a.Selected));
        Assert.Equal("Beta", b.AccountTitle);
        Assert.Equal("Beta · 2 cases", b.Subtitle);
        Assert.Equal(["c2", "c3"], F.Ids(b.Columns[1].Rows));
        Assert.Equal(["c4"], F.Ids(b.Sections[0].Rows));
        Assert.Equal([0, 2, 0, 0], b.Today.Tiles.Take(4).Select(t => t.Count));
    }

    [Fact]
    public void IsEmpty()
    {
        Assert.True(F.View([]).IsEmpty);
        Assert.False(F.View([F.Mk("c1")]).IsEmpty);
        // Done cases still count: the board is not empty, the list is.
        Assert.False(F.View([F.Mk("c1", done: true)]).IsEmpty);
        // In the account scope.
        Assert.True(F.View([F.Mk("c1")], configure: v => v with { Account = F.AccountB }).IsEmpty);
        Assert.False(F.View([F.Mk("c1", done: true)], configure: v => v with { Account = F.AccountA }).IsEmpty);
        // Whatever the filter.
        Assert.False(F.View([F.Mk("c1")], configure: v => v with { Filter = Filter.Done }).IsEmpty);
    }

    [Fact]
    public void PluralTexts()
    {
        Assert.Equal("All Accounts · 0 cases", F.View([]).Subtitle);
        Assert.Equal("All Accounts · 1 case", F.View([F.Mk("c1")]).Subtitle);
        Assert.Equal("All Accounts · 2 cases", F.View([F.Mk("c1"), F.Mk("c2")]).Subtitle);
        Assert.Equal("1 message", BText.MessageCount(1));
        Assert.Equal("2 messages", BText.MessageCount(2));
        Assert.Equal("0 messages", BText.MessageCount(0));
        var d = F.View([F.Mk("c1", count: 1), F.Mk("c2", hours: 2, count: 7)], configure: v => v with { Selection = F.Id("c2") }).Detail;
        Assert.Equal("Conversation · 7 messages", d?.ConversationTitle);
        Assert.Equal("Conversation · 1 message", F.View([F.Mk("c1", count: 1)]).Detail?.ConversationTitle);
        // A count below one still means one message.
        Assert.Equal("Conversation · 1 message", F.View([F.Mk("c1", count: 0)]).Detail?.ConversationTitle);
        Assert.Equal("Nothing needs you today.", BText.TodoPhrase(0));
        Assert.Equal("1 thing needs you today.", BText.TodoPhrase(1));
        Assert.Equal("2 things need you today.", BText.TodoPhrase(2));
        Assert.Equal("and 2 more", BText.AndMore(2));
    }

    // Rows

    [Fact]
    public void RowFields()
    {
        var c = new Case
        {
            Id = F.Id("c1"),
            Account = F.AccountB,
            Person = "Ada",
            Date = F.Ago(2),
            Subject = "DEMO-1: Subject",
            Snippet = "snip",
            Unread = true,
            HasAttachments = true,
            MessageCount = 3,
            Issue = new Board.IssueInfo("DEMO-1", "To Do", JiraStatusStyle.Todo),
            RuleState = State.You,
        };
        var r = F.View([c]).Sections[0].Rows[0];
        Assert.True(r.Id == F.Id("c1") && r.State == State.You && r.Person == "Ada");
        Assert.True(r.Title == "DEMO-1: Subject" && r.Snippet == "snip" && r.Account == "Beta");
        Assert.True(r.IssueKey == "DEMO-1" && r.IssueStatus == "To Do" && r.IssueStyle == JiraStatusStyle.Todo);
        Assert.True(r.Attachments && r.Unread && r.CountText == "3" && r.Due == "" && !r.DueOverdue);
        Assert.NotEmpty(r.Time);
        Assert.Equal("Waiting for You. Ada. DEMO-1: Subject. DEMO-1, To Do. 3 messages. Has attachments. Unread.", r.Spoken);
        var plain = F.View([F.Mk("c2")]).Sections[0].Rows[0];
        Assert.True(plain.IssueKey == "" && plain.IssueStyle == JiraStatusStyle.Plain && plain.CountText == "" && !plain.Unread && !plain.Attachments);
        Assert.DoesNotContain("message", plain.Spoken, StringComparison.Ordinal);
    }

    // Annotated and not

    private static Case AnnotatedCase() => F.Mk(
        "c1", State.You, subject: "Raw subject", snippet: "raw snippet",
        annotation: new Annotation
        {
            State = State.Hot,
            Title = "Assistant title",
            Summary = "Assistant summary",
            Why = "Assistant why",
            Due = F.Day(16, 10),
            DueQuote = "by tomorrow",
            Tasks = ["t1", "  ", "t2"],
        },
        draft: "Hi,\n\nreply");

    [Fact]
    public void WithAnnotations()
    {
        var v = F.View([AnnotatedCase()], annotated: true);
        var r = v.Sections[0].Rows[0];
        Assert.Equal("Assistant title", r.Title);
        Assert.Equal("Assistant summary", r.Snippet);
        Assert.Equal("Tomorrow", r.Due);
        Assert.False(r.DueOverdue);
        Assert.True(r.TitleIsAssistant && r.SnippetIsAssistant && r.MarksAssistant);
        var d = Assert.IsType<Detail>(v.Detail);
        Assert.Equal(State.Hot, d.State);
        Assert.Equal(StateSource.AssistantChanged(State.You), d.Source);
        Assert.True(d.Title == "Assistant title" && d.Subject == "Raw subject");
        Assert.True(d.Summary == "Assistant summary" && d.Why == "Assistant why" && d.WhyIsAssistant);
        Assert.True(d.Due == "Tomorrow" && d.DueQuote == "by tomorrow");
        Assert.Equal(["t1", "t2"], d.Tasks);
        Assert.Equal("Hi,\n\nreply", d.Draft);
        Assert.Equal("Hot", d.StateTitle);
        Assert.Contains("The rules suggested: Waiting for You.", d.SourceText, StringComparison.Ordinal);
        Assert.Equal("Assistant: Assistant title", d.SpokenTitle);
    }

    [Fact]
    public void WithoutAnnotations()
    {
        var v = F.View([AnnotatedCase()], annotated: false);
        var r = v.Sections[0].Rows[0];
        Assert.Equal(State.You, r.State);
        Assert.Equal("Raw subject", r.Title);
        Assert.Equal("raw snippet", r.Snippet);
        Assert.Equal("", r.Due);
        Assert.False(r.MarksAssistant);
        var d = Assert.IsType<Detail>(v.Detail);
        Assert.True(d.State == State.You && d.Source == StateSource.AssistantOff);
        Assert.Equal("Raw subject", d.Title);
        Assert.Equal("", d.Subject);
        Assert.Equal("", d.Summary);
        Assert.True(d.Due == "" && d.DueQuote == "");
        Assert.Empty(d.Tasks);
        // The draft is a real draft of the account: it stays without the assistant.
        Assert.True(d.Draft == "Hi,\n\nreply" && d.DraftId == new DraftId("d_c1"));
        Assert.Equal(BText.ReasonUnknown, d.Why); // "rule c1" is no code this client knows
        Assert.False(d.WhyIsAssistant);
        Assert.Empty(v.Today.DueGroups);
        Assert.Equal(4, v.Today.Tiles.Count);
        Assert.Equal("Sorted by the daemon’s rules · assistant off", v.StatusLine);
    }

    private static Detail? DetailOf(string title, string subject) =>
        F.View([F.Mk("c1", subject: subject, annotation: new Annotation { State = State.You, Title = title })], annotated: true).Detail;

    [Fact]
    public void SubjectShownOnlyWhenTheTitleDiffers()
    {
        Assert.Equal("", DetailOf("Same", "Same")?.Subject);
        Assert.Equal("", DetailOf("Same", "  Same \n")?.Subject); // cleaned first
        Assert.Equal("Same", DetailOf("Other", "Same")?.Subject);
        // An empty title falls back to the subject, and then the two agree.
        Assert.Equal("Same", DetailOf("", "Same")?.Title);
        Assert.Equal("", DetailOf("", "Same")?.Subject);
        Assert.Equal("Same", DetailOf("\u202E", "Same")?.Title);
    }

    [Fact]
    public void EmptySubjectAndSnippetFallbacks()
    {
        var v = F.View([F.Mk("c1", subject: " \n ", snippet: "snip")]);
        Assert.Equal("(No subject)", v.Sections[0].Rows[0].Title);
        // An annotation with an empty summary leaves the case snippet in the row.
        var a = new Annotation { State = State.You, Title = "T", Summary = " " };
        var r = F.View([F.Mk("c1", snippet: "snip", annotation: a)], annotated: true).Sections[0].Rows[0];
        Assert.Equal("snip", r.Snippet);
        Assert.False(r.SnippetIsAssistant);
        Assert.Equal("", F.View([F.Mk("c1", snippet: "snip", annotation: a)], annotated: true).Detail?.Summary);
    }

    [Fact]
    public void WhyFallsBackToTheRulesReason()
    {
        var a = new Annotation { State = State.You, Title = "T", Why = "" };
        Assert.Equal(BText.ReasonUnknown, F.View([F.Mk("c1", annotation: a)], annotated: true).Detail?.Why);
        var known = F.Mk("c1", annotation: a) with { RuleReason = BoardReason.YouAddressed };
        Assert.Equal(BText.Reason(BoardReason.YouAddressed), F.View([known], annotated: true).Detail?.Why);
        var b = new Annotation { State = State.You, Title = "T", Why = "Mine" };
        Assert.Equal("Mine", F.View([F.Mk("c1", annotation: b)], annotated: true).Detail?.Why);
    }

    [Fact]
    public void DueQuoteOnlyWithADueDate()
    {
        var a = new Annotation { State = State.You, Title = "T", DueQuote = "quote" };
        Assert.Equal("", F.View([F.Mk("c1", annotation: a)], annotated: true).Detail?.DueQuote);
    }

    [Fact]
    public void DetailMessagesAndIssue()
    {
        CaseMessage[] m =
        [
            new() { From = "Bob", Date = F.Ago(2), Text = "second" },
            new() { From = "Ann", Date = F.Ago(5), Text = "first", Mine = false },
            new() { From = "Me", Date = F.Ago(1), Text = "third", Mine = true },
        ];
        var c = F.Mk("c1", count: 3, messages: m, issue: new Board.IssueInfo("DEMO-2", "Done", JiraStatusStyle.Done));
        var d = Assert.IsType<Detail>(F.View([c]).Detail);
        Assert.Equal(["first", "second", "third"], d.Messages.Select(x => x.Text)); // oldest first
        Assert.Equal(["Ann", "Bob", "You"], d.Messages.Select(x => x.From)); // the user's own are "You"
        Assert.Equal([false, false, true], d.Messages.Select(x => x.Mine));
        Assert.Equal(new Board.IssueInfo("DEMO-2", "Done", JiraStatusStyle.Done), d.Issue);
        Assert.True(d.Account == "Alpha" && d.Person == "P c1" && d.Time.Length > 0 && !d.IsDone);
        Assert.True(F.View([F.Mk("c1", done: true)], configure: v => v with { Filter = Filter.Done }).Detail?.IsDone);
    }

    [Fact]
    public void ConversationLoadingAndFailed()
    {
        var loading = Assert.IsType<Detail>(F.View([F.Mk("c1", messagesNull: true)]).Detail);
        Assert.True(loading.MessagesLoading && !loading.MessagesRetry);
        Assert.Equal(BText.MessagesLoading, loading.MessagesNote);
        var failed = Assert.IsType<Detail>(F.View([F.Mk("c1", messagesNull: true) with { MessagesFailed = true }]).Detail);
        Assert.True(!failed.MessagesLoading && failed.MessagesRetry);
        Assert.Equal(BText.MessagesFailed, failed.MessagesNote);
        var loaded = Assert.IsType<Detail>(F.View([F.Mk("c1")]).Detail);
        Assert.True(!loaded.MessagesLoading && !loaded.MessagesRetry && loaded.MessagesNote == "");
    }

    // Commitments

    [Fact]
    public void Commitments()
    {
        Case[] cases =
        [
            F.Mk("c1", State.Hot, annotation: new Annotation { State = State.Hot, Title = "Titled" }),
            F.Mk("c2", State.You, account: F.AccountB), F.Mk("c3", State.Info, done: true),
        ];
        Commitment[] ks =
        [
            new() { Id = new("k1"), CaseId = F.Id("c1"), Text = "Do it", Quote = "I will", Due = F.Day(20, 9) },
            new() { Id = new("k2"), CaseId = F.Id("c2"), Text = "Other account" },
            new() { Id = new("k3"), CaseId = F.Id("c3"), Text = "On a done case" },
            new() { Id = new("k4"), CaseId = F.Id("missing"), Text = "Unknown case" },
            new() { Id = new("k5"), CaseId = F.Id("c1"), Text = "Next year", Due = new DateTimeOffset(2027, 1, 3, 9, 0, 0, TimeSpan.Zero) },
        ];
        var on = F.View(cases, annotated: true, commitments: ks);
        Assert.Equal(["k1", "k2", "k5"], on.Commitments.Select(k => k.Id.Value));
        Assert.True(on.Commitments[0].From == "Titled" && on.Commitments[0].CaseId == F.Id("c1") && on.Commitments[0].FromIsAssistant);
        Assert.True(on.Commitments[0].Text == "Do it" && on.Commitments[0].Quote == "I will");
        Assert.Equal("20 Oct", on.Commitments[0].Due);
        Assert.True(on.Commitments[1].Due == "" && on.Commitments[1].From == "Subject c2");
        Assert.Equal("2027-01-03", on.Commitments[2].Due);
        Assert.True(on.ShowsCommitmentsInList);
        Assert.Equal(on.Commitments, on.Today.Commitments);
        Assert.Equal(new Tile(TileKind.Commitments, State.Hot, 3, "Promised") { ToolTip = "Promised: 3 promises" }, on.Today.Tiles[^1]);

        // In the account scope.
        var b = F.View(cases, annotated: true, commitments: ks, configure: v => v with { Account = F.AccountB });
        Assert.Equal(["k2"], b.Commitments.Select(k => k.Id.Value));

        // The list shows them under Overview only.
        var you = F.View(cases, annotated: true, commitments: ks, configure: v => v with { Filter = Filter.Of(State.You) });
        Assert.False(you.ShowsCommitmentsInList);
        Assert.NotEmpty(you.Commitments);

        // Not annotated: none, and no tile.
        var off = F.View(cases, annotated: false, commitments: ks);
        Assert.True(off.Commitments.Count == 0 && off.Today.Commitments.Count == 0 && !off.ShowsCommitmentsInList);
        Assert.Equal(Enumerable.Repeat(TileKind.State, 4), off.Today.Tiles.Select(t => t.Kind));
        Assert.Equal(States, off.Today.Tiles.Select(t => t.State));

        // No commitment, nothing to show.
        var none = F.View(cases, annotated: true);
        Assert.True(none.Commitments.Count == 0 && !none.ShowsCommitmentsInList);
        Assert.Equal(0, none.Today.Tiles[^1].Count);
    }

    // Due groups

    private static Annotation Ann(DateTimeOffset? due, string quote = "") =>
        new() { State = State.You, Title = "T", Due = due, DueQuote = quote };

    [Fact]
    public void DueGroups()
    {
        Case[] cases =
        [
            F.Mk("c1", annotation: Ann(F.Day(13, 9), quote: "q1")),
            F.Mk("c2", annotation: Ann(F.Day(15, 23, 59))),
            F.Mk("c3", annotation: Ann(F.Day(16, 0, 30))),
            F.Mk("c4", annotation: Ann(F.Day(22, 23))), // +7 days
            F.Mk("c5", annotation: Ann(F.Day(23, 0))), // +8 days
            F.Mk("c6", annotation: Ann(F.Day(20, 9))),
            F.Mk("c7", annotation: Ann(F.Day(18, 9))),
            F.Mk("c8", annotation: Ann(F.Day(15, 9)), done: true), // done: hidden
            F.Mk("c9", account: F.AccountB, annotation: Ann(F.Day(15, 8))), // other account
            F.Mk("c10", annotation: Ann(null)),
        ];
        var v = F.View(cases, annotated: true, configure: x => x with { Account = F.AccountA });
        var groups = v.Today.DueGroups;
        Assert.Equal(Enum.GetValues<DueGroupKind>(), groups.Select(g => g.Kind));
        Assert.Equal(["Overdue", "Today", "Tomorrow", "Next 7 Days", "Later"], groups.Select(g => g.Title));
        Assert.Equal([["c1"], ["c2"], ["c3"], ["c7", "c6", "c4"], ["c5"]], groups.Select(g => g.Items.Select(i => i.CaseId.Value).ToArray()));
        Assert.True(groups[0].Items[0].Quote == "q1" && groups[0].Items[0].Label == "13 Oct");
        Assert.True(groups[1].Items[0].Label == "Today" && groups[2].Items[0].Label == "Tomorrow");
        Assert.True(groups[0].Items[0].Person == "P c1" && groups[0].Items[0].Title == "T");

        // Go: only the overdue row says so.
        foreach (var row in v.Sections.SelectMany(s => s.Rows))
        {
            Assert.True(row.DueOverdue == (row.Id == F.Id("c1")), $"case {row.Id.Value}");
        }

        // Without the account filter the other account's case joins its group, sorted by date.
        var all = F.View(cases, annotated: true).Today.DueGroups;
        Assert.Equal(["c9", "c2"], all[1].Items.Select(i => i.CaseId.Value));

        // Empty groups are left out.
        var one = F.View([F.Mk("c1", annotation: Ann(F.Day(15, 10)))], annotated: true).Today.DueGroups;
        Assert.Equal([DueGroupKind.Today], one.Select(g => g.Kind));
        Assert.Empty(F.View([F.Mk("c1")], annotated: true).Today.DueGroups);

        // Switched off: none, even with the annotation there.
        Assert.Empty(F.View(cases, annotated: false).Today.DueGroups);
        Assert.Equal(BText.DueEmpty, F.View(cases, annotated: true).Today.DueEmpty);
    }

    public static TheoryData<int, DateTimeOffset, DateTimeOffset, DueGroupKind> Boundaries => new()
    {
        { 0, F.Day(15, 0, 0, 10), F.Day(14, 23, 59, 59), DueGroupKind.Overdue }, // just before midnight
        { 1, F.Day(15, 0, 0, 10), F.Day(15, 0, 0, 0), DueGroupKind.Today }, // earlier today still counts as today
        { 2, F.Day(15, 23, 59, 30), F.Day(15, 23, 59, 59), DueGroupKind.Today },
        { 3, F.Day(15, 23, 59, 30), F.Day(16, 0, 0, 10), DueGroupKind.Tomorrow }, // seconds away, yet tomorrow
        { 4, F.Day(15, 12), F.Day(16, 23, 59, 59), DueGroupKind.Tomorrow },
        { 5, F.Day(15, 12), F.Day(17, 0, 0, 0), DueGroupKind.ThisWeek },
        { 6, F.Day(15, 12), F.Day(22, 23, 59, 59), DueGroupKind.ThisWeek }, // +7 days
        { 7, F.Day(15, 12), F.Day(23, 0, 0, 0), DueGroupKind.Later }, // +8 days
        { 8, F.Day(15, 12), F.Day(14, 12), DueGroupKind.Overdue },
    };

    [Theory]
    [MemberData(nameof(Boundaries))]
    public void DueGroupBoundaries(int i, DateTimeOffset now, DateTimeOffset due, DueGroupKind want)
    {
        Assert.True(DueGroupOf(due, now, F.Zone) == want, $"case {i}");
    }

    [Fact]
    public void DueGroupAcrossTheMonthAndTimeZones()
    {
        // The month end: 31 Oct to 1 Nov is one calendar day.
        var nov1 = new DateTimeOffset(2026, 11, 1, 9, 0, 0, TimeSpan.Zero);
        Assert.Equal(DueGroupKind.Tomorrow, DueGroupOf(nov1, F.Day(31, 23), F.Zone));
        // A time zone moves midnight: 23:30 UTC is already tomorrow in Prague.
        var prague = TimeZoneInfo.FindSystemTimeZoneById("Europe/Prague");
        var late = F.Day(15, 23, 30);
        var due = F.Day(16, 10);
        Assert.Equal(DueGroupKind.Tomorrow, DueGroupOf(due, late, F.Zone));
        Assert.Equal(DueGroupKind.Today, DueGroupOf(due, late, prague));
    }

    // Panel and Today

    public static TheoryData<BoardStyle, bool, string?, bool> PanelCases => new()
    {
        { BoardStyle.List, true, "c1", false },
        { BoardStyle.List, true, null, false },
        { BoardStyle.List, false, "c1", true },
        { BoardStyle.List, false, null, false },
        { BoardStyle.Columns, true, "c1", true },
        { BoardStyle.Columns, false, "c1", true },
        { BoardStyle.Columns, true, null, false },
        { BoardStyle.Today, true, "c1", true },
        { BoardStyle.Today, true, null, false },
    };

    [Theory]
    [MemberData(nameof(PanelCases))]
    public void ShowsPanel(BoardStyle style, bool inline, string? sel, bool want)
    {
        Case[] cases = [F.Mk("c1"), F.Mk("c2", State.Hot)];
        var v = F.View(cases, configure: x => x with
        {
            Style = style,
            InlineDetail = inline,
            Selection = sel is null ? (BoardCaseId?)null : F.Id(sel),
        });
        Assert.Equal(want, v.ShowsPanel);
    }

    [Fact]
    public void TodayPage()
    {
        var cases = new List<Case> { F.Mk("h1", State.Hot, hours: 1), F.Mk("h2", State.Hot, hours: 2) };
        for (var i = 1; i <= 7; i++)
        {
            cases.Add(F.Mk("y" + i, State.You, hours: i));
        }
        cases.Add(F.Mk("t1", State.Them));
        cases.Add(F.Mk("i1", State.Info, done: true));
        var t = F.View(cases).Today;
        Assert.Equal("Today", t.Title);
        Assert.Equal([2, 7, 1, 0], t.Tiles.Select(x => x.Count));
        Assert.Equal(["Hot", "Waiting for You", "Waiting for Them", "For Your Information"], t.Tiles.Select(x => x.Title));
        Assert.Equal(["h1", "h2"], F.Ids(t.Hot));
        Assert.Equal(["y1", "y2", "y3", "y4", "y5"], F.Ids(t.You));
        Assert.Equal(2, t.YouMore);
        Assert.Equal("9 things need you today.", t.Phrase);
        Assert.Equal(["Hot: 2 cases", "Waiting for You: 7 cases", "Waiting for Them: 1 case", "For Your Information: 0 cases"], t.Tiles.Select(x => x.ToolTip));

        var few = F.View([F.Mk("y1"), F.Mk("y2")]).Today;
        Assert.True(few.YouMore == 0 && few.You.Count == 2);
        Assert.Equal("2 things need you today.", few.Phrase);

        var exactly = F.View([.. Enumerable.Range(1, 5).Select(i => F.Mk("y" + i, hours: i))]).Today;
        Assert.True(exactly.YouMore == 0 && exactly.You.Count == 5);

        Assert.Equal("1 thing needs you today.", F.View([F.Mk("y1")]).Today.Phrase);
        Assert.Equal("Nothing needs you today.", F.View([F.Mk("t1", State.Them)]).Today.Phrase);
        Assert.Equal("Nothing needs you today.", F.View([]).Today.Phrase);

        // The state decides, so an annotation can move a case into the top list.
        var a = new Annotation { State = State.Hot, Title = "t" };
        var moved = F.View([F.Mk("y1", State.You, annotation: a)], annotated: true).Today;
        Assert.True(F.Ids(moved.Hot).SequenceEqual(["y1"]) && moved.You.Count == 0);
        Assert.Equal("1 thing needs you today.", moved.Phrase);
    }

    [Fact]
    public void ViewSaysWhetherTheAssistantIsOn()
    {
        Assert.True(F.View([F.Mk("c1")], annotated: true).AssistantOn);
        Assert.False(F.View([F.Mk("c1")], annotated: false).AssistantOn);
    }

    [Fact]
    public void SnoozedRowSpeaksItsRemindTime()
    {
        var at = F.Now.AddHours(20);
        var c = F.Mk("c1", visibility: Visibility.Snoozed(at));
        var v = F.View([c], configure: x => x with { Filter = Filter.Snoozed });
        var r = v.Sections.SelectMany(s => s.Rows).First(x => x.Id == F.Id("c1"));
        Assert.Equal("Tomorrow at 08:00", r.Remind);
        Assert.Contains(BText.SpokenRemind(r.Remind) + ".", r.Spoken, StringComparison.Ordinal);
        Assert.Equal("snoozed", F.KindOf(v.Sections[0]));
        var d = Assert.IsType<Detail>(v.Detail);
        Assert.True(d.IsSnoozed && d.RemindText == "Back on the board: Tomorrow at 08:00");
    }

    [Fact]
    public void StatusLine()
    {
        var s = new Snapshot
        {
            Cases = [F.Mk("c1")],
            Annotated = true,
            Run = new Run { Model = "Claude", Date = F.Now, Note = "3 sorted" },
        };
        var v = View(s, new ViewState(), F.Now, F.Culture, F.Zone);
        Assert.Equal("Sorted by rules · refined by the assistant (Claude) · 3 sorted", v.StatusLine);
        var bare = s with { Run = null };
        Assert.Equal("Sorted by rules · refined by the assistant", View(bare, new ViewState(), F.Now, F.Culture, F.Zone).StatusLine);
    }

    [Fact]
    public void TheSameInputGivesAnEqualView()
    {
        Case[] cases = [AnnotatedCase(), F.Mk("c2", State.Them, hours: 3)];
        Assert.Equal(F.View(cases, annotated: true), F.View(cases, annotated: true));
        Assert.NotEqual(F.View(cases, annotated: true), F.View(cases, annotated: false));
    }
}
