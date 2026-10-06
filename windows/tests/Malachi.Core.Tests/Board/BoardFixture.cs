// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/BoardModelTests.swift (BoardFixture);
// GTK: ui/internal/board/model_test.go (the fixture). Pure and
// deterministic: a fixed now, UTC and the invariant culture (Swift's
// en_US_POSIX). The catalogue is English in tests, so msgids come back
// verbatim.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Board;
using static Malachi.Core.Board.Board;
using B = Malachi.Core.Board.Board;

namespace Malachi.Core.Tests.Board;

/// <summary>Fixtures shared by the board's tests.</summary>
internal static class BoardFixture
{
    public static readonly TimeZoneInfo Zone = TimeZoneInfo.Utc;

    public static readonly CultureInfo Culture = CultureInfo.InvariantCulture;

    /// <summary>A day of October 2026 (the 15th is a Thursday), UTC.</summary>
    public static DateTimeOffset Day(int d, int h = 12, int m = 0, int s = 0) => new(2026, 10, d, h, m, s, TimeSpan.Zero);

    public static readonly DateTimeOffset Now = Day(15, 12);

    public static DateTimeOffset Ago(double hours) => Now.AddHours(-hours);

    public static readonly AccountId AccountA = new("a");

    public static readonly AccountId AccountB = new("b");

    public static readonly IReadOnlyList<AccountInfo> Accounts =
    [
        new(AccountA, "Alpha", "IMAP"),
        new(AccountB, "Beta", "JIRA"),
    ];

    public static BoardCaseId Id(string s) => new(s);

    /// <summary>A case: "P n" from "Subject n", <paramref name="hours"/> ago, its messages loaded (none).</summary>
    public static Case Mk(
        string n, State state = State.You, AccountId? account = null, double hours = 1, string? subject = null,
        string snippet = "", int count = 1, Annotation? annotation = null, State? user = null, bool done = false,
        IReadOnlyList<CaseMessage>? messages = null, bool messagesNull = false, B.IssueInfo? issue = null,
        Visibility? visibility = null, string? draft = null, long version = 0) => new()
        {
            Id = Id(n),
            Account = account ?? AccountA,
            Person = "P " + n,
            Date = Ago(hours),
            Subject = subject ?? "Subject " + n,
            Snippet = snippet,
            MessageCount = count,
            Issue = issue,
            RuleState = state,
            RuleReason = new BoardReason("rule " + n),
            Annotation = annotation,
            UserState = user,
            Visibility = visibility ?? (done ? Visibility.Done() : Visibility.Live),
            Draft = draft is null ? null : new DraftLink(new DraftId("d_" + n), draft),
            Messages = messagesNull ? null : messages ?? [],
            Version = version,
        };

    /// <summary>The view of <paramref name="cases"/> over the two accounts, a run of model "M".</summary>
    public static ViewModel View(
        IReadOnlyList<Case> cases, bool annotated = false, IReadOnlyList<Commitment>? commitments = null,
        Func<ViewState, ViewState>? configure = null)
    {
        var v = configure?.Invoke(new ViewState()) ?? new ViewState();
        var s = new Snapshot
        {
            Accounts = Accounts,
            Cases = cases,
            Commitments = commitments ?? [],
            Annotated = annotated,
            Run = new Run { Model = "M", Date = Now },
        };
        return B.View(s, v, Now, Culture, Zone);
    }

    public static string[] Ids(IEnumerable<Row> rows) => [.. rows.Select(r => r.Id.Value)];

    /// <summary>A section's kind as Swift writes it: "state(Hot)", "snoozed", "done".</summary>
    public static string KindOf(Section s) => s.Kind switch
    {
        SectionKind.State => "state(" + s.State + ")",
        SectionKind.Snoozed => "snoozed",
        _ => "done",
    };
}
