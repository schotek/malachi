// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/BoardModelTests.swift
// (BoardSampleTests); GTK: ui/internal/board/model_test.go
// (TestSamplesEveryAddressEndsInInvalid, TestSamplesCoverTheStatesAndShapes,
// TestSamplesViewShowsEverything, TestSamplesDueQuotesCiteTheMessages,
// TestSamplesStableForAFixedNow).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Boards;
using Xunit;
using static Malachi.Core.Boards.Board;
using F = Malachi.Core.Tests.Boards.BoardFixture;

namespace Malachi.Core.Tests.Boards;

public sealed class BoardSampleTests
{
    private readonly Snapshot snap = SampleSnapshot(F.Now, F.Zone);

    private List<string> Strings()
    {
        var output = new List<string>();
        foreach (var a in snap.Accounts)
        {
            output.AddRange([a.Name, a.Badge]);
        }
        foreach (var c in snap.Cases)
        {
            output.AddRange([c.Person, c.Subject, c.Snippet, Board.Text.Reason(c.RuleReason), c.Draft?.Text ?? ""]);
            output.AddRange([c.Issue?.Key ?? "", c.Issue?.Status ?? ""]);
            if (c.Annotation is { } a)
            {
                output.AddRange([a.Title, a.Summary, a.Why, a.DueQuote, .. a.Tasks]);
            }
            foreach (var m in c.Messages ?? [])
            {
                output.AddRange([m.From, m.Text]);
            }
        }
        foreach (var k in snap.Commitments)
        {
            output.AddRange([k.Text, k.Quote]);
        }
        output.AddRange([snap.Run?.Model ?? "", snap.Run?.Note ?? ""]);
        return output;
    }

    [Fact]
    public void EveryAddressEndsInInvalid()
    {
        var found = 0;
        foreach (var s in Strings())
        {
            foreach (var token in s.Split((char[]?)null, StringSplitOptions.RemoveEmptyEntries).Where(t => t.Contains('@', StringComparison.Ordinal)))
            {
                found++;
                var t = token.Trim().TrimEnd('.', ',', ';', ':', '!', '?', ')').TrimStart('(', '<');
                Assert.True(t.EndsWith(".invalid", StringComparison.Ordinal), token);
            }
        }
        Assert.True(found >= 1); // the samples do use an address, so the check has teeth
        // No web address either.
        Assert.DoesNotContain(Strings(), s => s.Contains("http", StringComparison.Ordinal) || s.Contains("www.", StringComparison.Ordinal));
    }

    [Fact]
    public void CoversTheStatesAndShapes()
    {
        Assert.Equal(States.ToHashSet(), snap.Cases.Select(c => StateOf(c, true)).ToHashSet());
        Assert.True(snap.Annotated);
        Assert.Equal(4, snap.Accounts.Count);
        Assert.Equal(4, snap.Accounts.Select(a => a.Id).Distinct().Count());
        Assert.Equal(snap.Cases.Count, snap.Cases.Select(c => c.Id).Distinct().Count());
        var ids = snap.Accounts.Select(a => a.Id).ToHashSet();
        Assert.All(snap.Cases, c => Assert.Contains(c.Account, ids));
        Assert.Equal(2, snap.Cases.Count(c => c.Done));
        var jira = snap.Cases.Where(c => c.Issue is not null).ToList();
        Assert.NotEmpty(jira);
        Assert.All(jira, c => Assert.StartsWith("DEMO-", c.Issue!.Key, StringComparison.Ordinal));
        Assert.Contains(snap.Cases, c => c.Annotation is null); // one the assistant has not looked at
        Assert.Contains(snap.Cases, c => c.Draft is not null);
        // Every reason is a code this client knows.
        Assert.All(snap.Cases, c => Assert.Contains(c.RuleReason, KnownReasons));
        Assert.All(snap.Cases, c => Assert.NotNull(c.Messages)); // loaded: the samples need no daemon
        Assert.Contains(snap.Cases, c => c.Annotation?.Tasks.Count > 0);
        Assert.Contains(snap.Cases, c => c.UserState is not null);
        Assert.Contains(snap.Cases, c => StateSourceOf(c, true) == StateSource.AssistantKept);
        Assert.Contains(snap.Cases, c => StateSourceOf(c, true).Kind == StateSourceKind.AssistantChanged);
        Assert.Contains(snap.Cases, c => c.Unread);
        Assert.Contains(snap.Cases, c => c.HasAttachments);
        // A reminder that came due and a first message from a new contact (Go samples 22 and 23).
        Assert.Contains(snap.Cases, c => c.Reminded && c.Id.Value == "sample-22");
        Assert.Contains(snap.Cases, c => c.NewContact && c.Id.Value == "sample-23");
        Assert.All(snap.Cases, c => Assert.True(c.Date <= F.Now));
    }

    [Fact]
    public void ViewShowsEverything()
    {
        var v = View(snap, new ViewState(), F.Now, F.Culture, F.Zone);
        Assert.False(v.IsEmpty);
        Assert.Equal(Enum.GetValues<DueGroupKind>(), v.Today.DueGroups.Select(g => g.Kind));
        Assert.True(v.Commitments.Count >= 2);
        Assert.DoesNotContain(v.Commitments, k => k.CaseId.Value == "sample-21"); // on a done case
        Assert.True(v.Today.YouMore >= 1 && v.Today.You.Count == YouTopCount);
        Assert.All(v.Columns, c => Assert.NotEmpty(c.Rows));
        Assert.Equal(4, v.Sections.Count);
        Assert.NotNull(v.Detail);
        var done = View(snap, new ViewState { Filter = Filter.Done }, F.Now, F.Culture, F.Zone);
        Assert.Equal(2, done.Sections[0].Rows.Count);
        Assert.Contains("Claude", v.StatusLine, StringComparison.Ordinal);
    }

    /// <summary>A due quote cites the conversation word for word: it is a part of one of the case's messages.</summary>
    [Fact]
    public void DueQuotesCiteTheMessages()
    {
        var checkedQuotes = 0;
        foreach (var c in snap.Cases)
        {
            if (c.Annotation is not { DueQuote.Length: > 0 } a || c.Messages is not { Count: > 0 } ms)
            {
                continue;
            }
            Assert.True(ms.Any(m => m.Text.Contains(a.DueQuote, StringComparison.Ordinal)), $"{c.Id.Value}: {a.DueQuote}");
            checkedQuotes++;
        }
        Assert.True(checkedQuotes >= 4);
        // No weekday names in what has a deadline: the dates are relative.
        string[] weekdays = ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"];
        foreach (var c in snap.Cases.Where(c => c.Annotation?.Due is not null))
        {
            var a = c.Annotation!;
            string[] texts = [c.Snippet, a.Summary, a.Why, a.DueQuote, c.Draft?.Text ?? "", .. (c.Messages ?? []).Select(m => m.Text)];
            Assert.False(texts.Any(t => weekdays.Any(w => t.Contains(w, StringComparison.Ordinal))), c.Id.Value);
        }
    }

    [Fact]
    public void StableForAFixedNow()
    {
        var again = SampleSnapshot(F.Now, F.Zone);
        Assert.Equal(snap, again);
        // Relative to now: a day later, the same shape with later dates.
        var later = SampleSnapshot(F.Now.AddDays(1), F.Zone);
        Assert.NotEqual(snap, later);
        Assert.Equal(snap.Cases.Select(c => c.Id), later.Cases.Select(c => c.Id));
    }

    [Fact]
    public void TheDummySourceHoldsTheSamplesOrNothing()
    {
        Assert.Equal(snap, InMemoryBoardSource.Dummy(true, F.Now, F.Zone).Snapshot);
        Assert.Equal(Snapshot.Empty, InMemoryBoardSource.Dummy(false, F.Now, F.Zone).Snapshot);
    }
}
