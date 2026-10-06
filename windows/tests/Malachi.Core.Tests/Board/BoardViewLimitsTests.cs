// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/BoardModelTests.swift (BoardViewTests:
// the cleaning in the view, the caps and the selection rules); GTK:
// ui/internal/board/model_test.go (TestHostileStringsAreCleanedInTheView
// through TestSelectionAfterDone).

using System;
using System.Collections.Generic;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.Board;
using Malachi.Core.IssueTrackers;
using Xunit;
using static Malachi.Core.Board.Board;
using B = Malachi.Core.Board.Board;
using F = Malachi.Core.Tests.Board.BoardFixture;

namespace Malachi.Core.Tests.Board;

public sealed class BoardViewLimitsTests
{
    private static int Utf8(string s) => Encoding.UTF8.GetByteCount(s);

    [Fact]
    public void HostileStringsAreCleanedInTheView()
    {
        const string evil = "\u202Eev\0il\r\n\u2028text\u200B";
        var ann = new Annotation
        {
            State = State.You,
            Title = evil,
            Summary = evil,
            Why = evil,
            Due = F.Day(15),
            DueQuote = evil,
            Tasks = [evil],
        };
        var c = new Case
        {
            Id = F.Id("c1"),
            Account = F.AccountA,
            Person = evil,
            Date = F.Ago(1),
            Subject = evil,
            Snippet = evil,
            Issue = new B.IssueInfo(evil, evil, JiraStatusStyle.Plain),
            RuleState = State.You,
            RuleReason = new BoardReason(evil),
            Annotation = ann,
            Draft = new DraftLink(new DraftId("d_1"), evil),
            Messages = [new CaseMessage { From = evil, Date = F.Ago(2), Text = evil }],
        };
        var k = new Commitment { Id = new("k"), CaseId = F.Id("c1"), Text = evil, Quote = evil };
        var v = F.View([c], annotated: true, commitments: [k]);
        var r = v.Sections[0].Rows[0];
        var d = Assert.IsType<Detail>(v.Detail);
        var strings = new List<string> { r.Person, r.Title, r.Snippet, r.IssueKey, r.IssueStatus, r.Spoken };
        strings.AddRange([d.Why, d.Person, d.Title, d.DueQuote, d.Summary, d.Draft, d.Issue!.Key, d.Issue.Status]);
        strings.AddRange(d.Tasks);
        strings.AddRange(d.Messages.Select(m => m.From));
        strings.AddRange(d.Messages.Select(m => m.Text));
        strings.AddRange(v.Commitments.SelectMany(x => new[] { x.Text, x.Quote, x.From }));
        strings.AddRange(v.Today.DueGroups.SelectMany(g => g.Items.SelectMany(i => new[] { i.Title, i.Person, i.Quote })));
        foreach (var s in strings)
        {
            Assert.NotEmpty(s);
            Assert.DoesNotContain(s.EnumerateRunes(), x => x.Value is 0x202E or 0 or 0x200B or 0x2028);
        }
        // NUL and the override vanish; CR LF and U+2028 are two line breaks in a block, spaces in a line.
        Assert.Equal("evil text", r.Title);
        Assert.Equal("evil\n\ntext", d.Summary);
        Assert.Equal("", d.Subject); // cleaned alike, so the title does not differ
    }

    [Fact]
    public void CapsAreHonouredInTheView()
    {
        var longText = new string('é', 5000); // 10 000 bytes
        string[] tasks = [.. Enumerable.Range(0, 30).Select(i => "task " + i), "   ", "\0"];
        var messages = Enumerable.Range(0, 60)
            .Select(i => new CaseMessage { From = "F", Date = F.Ago(100 - i), Text = "m" + i }).Reverse().ToList();
        var ann = new Annotation { State = State.You, Title = longText, Summary = longText, Tasks = tasks };
        var c = F.Mk("c1", subject: longText, snippet: longText, count: 60, annotation: ann, messages: messages, draft: longText);
        var v = F.View([c], annotated: true);
        var r = v.Sections[0].Rows[0];
        Assert.InRange(Utf8(r.Title), 251, 300);
        Assert.True(Utf8(r.Snippet) <= 400);
        var d = Assert.IsType<Detail>(v.Detail);
        Assert.True(Utf8(d.Title) <= 300);
        Assert.Equal("", d.Subject); // cleaned alike: no differing subject to show
        Assert.True(Utf8(d.Summary) <= 2000);
        Assert.True(Utf8(d.Draft) <= 4000);
        Assert.Equal(20, d.Tasks.Count);
        Assert.True(d.Tasks[0] == "task 0" && d.Tasks[^1] == "task 19");
        Assert.Equal(50, d.Messages.Count);
        Assert.True(d.Messages[0].Text == "m10" && d.Messages[^1].Text == "m59"); // the newest 50, oldest first
        Assert.Equal("Conversation · 60 messages", d.ConversationTitle);

        var big = F.Mk("c2", messages: [new CaseMessage { From = new string('x', 1000), Date = F.Ago(1), Text = new string('é', 10000) }]);
        var m = F.View([big]).Detail!.Messages[0];
        // board.get's cap (8000 bytes): a whole ordinary mail, no more.
        Assert.InRange(Utf8(m.Text), 7991, 8000);
        Assert.True(Utf8(m.From) <= 200);
    }

    [Fact]
    public void BigAccountAndBadgeAreCapped()
    {
        var s = new Snapshot
        {
            Accounts = [new AccountInfo(F.AccountA, new string('n', 1000), new string('b', 1000))],
            Cases = [F.Mk("c1")],
        };
        var v = View(s, new ViewState(), F.Now, F.Culture, F.Zone);
        Assert.Equal(120, Utf8(v.Accounts[1].Title));
        Assert.Equal(64, Utf8(v.Accounts[1].Badge));
    }

    [Fact]
    public void CommitmentsShownAreCapped()
    {
        Commitment[] ks = [.. Enumerable.Range(0, 250).Select(i => new Commitment { Id = new("k" + i), CaseId = F.Id("c1"), Text = "promise " + i })];
        var v = F.View([F.Mk("c1")], annotated: true, commitments: ks);
        Assert.True(v.Commitments.Count == 100 && v.Today.Commitments.Count == 100);
        Assert.True(v.Commitments[0].Id.Value == "k0" && v.Commitments[^1].Id.Value == "k99");
        // The tile counts them all.
        Assert.True(v.Today.Tiles[^1].Kind == TileKind.Commitments && v.Today.Tiles[^1].Count == 250);
    }

    [Fact]
    public void NewestMessagesWithoutSortingThemAll()
    {
        // Shuffled dates with ties: the same as a stable sort's last 50.
        var rng = new Random(7);
        foreach (var n in new[] { 0, 1, 49, 50, 51, 120 })
        {
            var ms = Enumerable.Range(0, n)
                .Select(i => new CaseMessage { From = "F", Date = F.Ago(rng.Next(20)), Text = "m" + i }).ToList();
            var want = ms.Select((m, i) => (m, i)).OrderBy(x => x.m.Date).ThenBy(x => x.i)
                .Select(x => x.m.Text).TakeLast(50).ToList();
            var got = F.View([F.Mk("c1", messages: ms)]).Detail!.Messages.Select(x => x.Text).ToList();
            Assert.Equal(want, got);
        }
        // A huge conversation, newest first as some sources send it.
        var huge = Enumerable.Range(0, 200_000)
            .Select(i => new CaseMessage { From = "F", Date = F.Ago(i / 60.0), Text = "m" + i }).ToList();
        var d = F.View([F.Mk("c1", messages: huge)]).Detail!;
        Assert.Equal(50, d.Messages.Count);
        Assert.True(d.Messages[0].Text == "m49" && d.Messages[^1].Text == "m0");
    }

    private static IReadOnlyList<string> TasksOf(IReadOnlyList<string> t) =>
        F.View([F.Mk("c1", annotation: new Annotation { State = State.You, Title = "T", Tasks = t })], annotated: true).Detail!.Tasks;

    private static List<string> Blanks(int n) => [.. Enumerable.Repeat(" \u200B ", n)];

    [Fact]
    public void TasksScanIsBounded()
    {
        Assert.Equal(["x"], TasksOf([.. Blanks(199), "x"])); // the 200th is still looked at
        Assert.Empty(TasksOf([.. Blanks(200), "x"])); // the 201st is not
        Assert.Equal(Enumerable.Range(0, 20).Select(i => "t" + i), TasksOf([.. Blanks(150), .. Enumerable.Range(0, 30).Select(i => "t" + i)]));
        Assert.Empty(TasksOf(Blanks(100_000)));
    }

    [Fact]
    public void ARepeatedCaseIdShowsTheFirstCaseOnly()
    {
        var first = F.Mk("c1", State.Hot, subject: "first");
        var again = F.Mk("c1", State.Them, hours: 0.5, subject: "again");
        var v = F.View([F.Mk("c2", hours: 2), first, again, F.Mk("c1", done: true)]);
        var ids = v.Sections.SelectMany(s => s.Rows).Select(r => r.Id.Value).ToList();
        Assert.Equal(["c1", "c2"], ids);
        Assert.Equal("first", v.Sections[0].Rows[0].Title);
        Assert.Equal(["c1", "c2"], v.Columns.SelectMany(c => c.Rows).Select(r => r.Id.Value));
        Assert.Equal([2, 1, 1, 0, 0, 0], v.Nav.Select(n => n.Count));
        Assert.Equal([2, 2, 0], v.Accounts.Select(a => a.Count));
        Assert.True(v.Detail?.Id == F.Id("c1") && v.Detail?.Title == "first");
        var selected = F.View([first, again], configure: x => x with { Style = BoardStyle.Columns, Selection = F.Id("c1") });
        Assert.True(selected.Detail?.Title == "first" && selected.Columns[2].Rows.Count == 0);
    }

    // Selection

    private static Snapshot SnapshotOf(IReadOnlyList<Case> cases) => new() { Accounts = F.Accounts, Cases = cases };

    private static Case[] Sample =>
    [
        F.Mk("c1", State.Hot, hours: 4), F.Mk("c2", State.You, hours: 3), F.Mk("c3", State.You, hours: 2),
        F.Mk("c4", State.Them, hours: 1), F.Mk("d1", State.Info, hours: 6, done: true),
        F.Mk("d2", State.Info, hours: 5, done: true), F.Mk("b1", State.You, account: F.AccountB, hours: 7),
    ];

    [Fact]
    public void ResolveSelectionInTheList()
    {
        var s = SnapshotOf(Sample);
        string? Resolve(Func<ViewState, ViewState> configure) => ResolveSelection(s, configure(new ViewState()))?.Value;
        // Inline detail, nothing selected: the first row.
        Assert.Equal("c1", Resolve(v => v));
        // A shown case stays.
        Assert.Equal("c3", Resolve(v => v with { Selection = F.Id("c3") }));
        // Outside the filter: the first row of the filter.
        Assert.Equal("c3", Resolve(v => v with { Filter = Filter.Of(State.You), Selection = F.Id("c1") }));
        Assert.Equal("c3", Resolve(v => v with { Filter = Filter.Of(State.You), Selection = F.Id("c3") }));
        // Done is its own list.
        Assert.Equal("d2", Resolve(v => v with { Filter = Filter.Done }));
        Assert.Equal("d1", Resolve(v => v with { Filter = Filter.Done, Selection = F.Id("d1") }));
        Assert.Equal("c1", Resolve(v => v with { Selection = F.Id("d1") }));
        // Outside the account scope.
        Assert.Equal("b1", Resolve(v => v with { Account = F.AccountB, Selection = F.Id("c1") }));
        Assert.Null(Resolve(v => v with { Account = F.AccountB, Filter = Filter.Of(State.Hot) }));
        // Unknown.
        Assert.Equal("c1", Resolve(v => v with { Selection = F.Id("zz") }));
        // No inline detail: nothing selects itself, a valid selection stays.
        Assert.Null(Resolve(v => v with { InlineDetail = false }));
        Assert.Equal("c2", Resolve(v => v with { InlineDetail = false, Selection = F.Id("c2") }));
        Assert.Null(Resolve(v => v with { InlineDetail = false, Selection = F.Id("zz") }));
        // Empty list.
        Assert.Null(ResolveSelection(Snapshot.Empty, new ViewState()));
    }

    [Theory]
    [InlineData(BoardStyle.Columns)]
    [InlineData(BoardStyle.Today)]
    public void ResolveSelectionInColumnsAndToday(BoardStyle style)
    {
        var s = SnapshotOf(Sample);
        string? Resolve(Func<ViewState, ViewState> configure) =>
            ResolveSelection(s, configure(new ViewState { Style = style }))?.Value;
        // Never selects by itself, with or without inline detail.
        Assert.Null(Resolve(v => v));
        Assert.Null(Resolve(v => v with { InlineDetail = false }));
        // Any live case, whatever the filter.
        Assert.Equal("c4", Resolve(v => v with { Selection = F.Id("c4") }));
        Assert.Equal("c4", Resolve(v => v with { Filter = Filter.Of(State.Hot), Selection = F.Id("c4") }));
        Assert.Equal("c4", Resolve(v => v with { Filter = Filter.Done, Selection = F.Id("c4") }));
        // A done case is not on the board here.
        Assert.Null(Resolve(v => v with { Selection = F.Id("d1") }));
        Assert.Null(Resolve(v => v with { Filter = Filter.Done, Selection = F.Id("d1") }));
        // The account scope.
        Assert.Null(Resolve(v => v with { Account = F.AccountB, Selection = F.Id("c4") }));
        Assert.Equal("b1", Resolve(v => v with { Account = F.AccountB, Selection = F.Id("b1") }));
    }

    [Fact]
    public void ViewSelectionIsTheResolvedOne()
    {
        var v = F.View(Sample, configure: x => x with { Selection = F.Id("zz") });
        Assert.True(v.Selection == F.Id("c1") && v.Detail?.Id == F.Id("c1"));
        var col = F.View(Sample, configure: x => x with { Style = BoardStyle.Columns, Selection = F.Id("c4") });
        Assert.True(col.Selection == F.Id("c4") && col.Detail?.Id == F.Id("c4") && col.ShowsPanel);
    }

    [Fact]
    public void SelectionAfterTheCaseLeaves()
    {
        var s = SnapshotOf(Sample);
        string? After(string id, Func<ViewState, ViewState>? configure = null) =>
            SelectionAfterDone(F.Id(id), s, configure?.Invoke(new ViewState()) ?? new ViewState())?.Value;
        // List rows: c1 hot, c3 c2 you (newest first), b1 you, c4 them.
        var shown = View(s, new ViewState(), F.Now, F.Culture, F.Zone).Sections.SelectMany(x => x.Rows).Select(r => r.Id.Value);
        Assert.Equal(["c1", "c3", "c2", "b1", "c4"], shown);
        Assert.Equal("c3", After("c1")); // the first: the next
        Assert.Equal("c2", After("c3")); // the middle: the next
        Assert.Equal("b1", After("c4")); // the last: the previous
        Assert.Null(After("zz"));
        Assert.Null(After("d1")); // not in the list
        // Under a filter the filter's rows count.
        Assert.Equal("b1", After("c2", v => v with { Filter = Filter.Of(State.You) }));
        Assert.Equal("c2", After("b1", v => v with { Filter = Filter.Of(State.You) }));
        Assert.Null(After("c1", v => v with { Filter = Filter.Of(State.You) }));
        // Done list: reopening.
        Assert.Equal("d1", After("d2", v => v with { Filter = Filter.Done }));
        Assert.Equal("d2", After("d1", v => v with { Filter = Filter.Done }));
        // Account scope.
        Assert.Null(After("b1", v => v with { Account = F.AccountB })); // the only row
        Assert.Null(After("c1", v => v with { Account = F.AccountB }));
        // Columns and Today select nothing.
        foreach (var style in new[] { BoardStyle.Columns, BoardStyle.Today })
        {
            Assert.Null(After("c1", v => v with { Style = style }));
            Assert.Null(After("c3", v => v with { Style = style }));
        }
    }
}
