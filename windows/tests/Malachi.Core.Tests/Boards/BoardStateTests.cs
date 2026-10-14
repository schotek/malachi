// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/BoardModelTests.swift
// (BoardStateTests); GTK: ui/internal/board/model_test.go (TestStateOf,
// TestStateSourceOf).

using Xunit;
using static Malachi.Core.Boards.Board;
using F = Malachi.Core.Tests.Boards.BoardFixture;

namespace Malachi.Core.Tests.Boards;

public sealed class BoardStateTests
{
    public static TheoryData<State?, State?, bool, State> StateCases => new()
    {
        { null, null, false, State.Info },
        { null, null, true, State.Info },
        { null, State.Hot, true, State.Hot },
        { null, State.Info, true, State.Info }, // the annotation agrees with the rules
        { null, State.Hot, false, State.Info }, // annotations switched off
        { State.Them, null, true, State.Them },
        { State.Them, State.Hot, true, State.Them }, // the user wins over the assistant
        { State.Them, State.Hot, false, State.Them },
        { State.Info, State.Info, true, State.Info },
    };

    [Theory]
    [MemberData(nameof(StateCases))]
    public void StateOfCase(State? user, State? annotation, bool annotated, State want)
    {
        var c = F.Mk("x", State.Info, user: user) with
        {
            Annotation = annotation is null ? null : new Annotation { State = annotation, Title = "t" },
        };
        Assert.Equal(want, StateOf(c, annotated));
    }

    [Fact]
    public void AnAnnotationWithoutAStateLeavesTheRules()
    {
        var c = F.Mk("x", State.Them) with { Annotation = new Annotation { Title = "t" } };
        Assert.Equal(State.Them, StateOf(c, true));
        Assert.Equal(StateSource.AssistantKept, StateSourceOf(c, true));
    }

    [Fact]
    public void AStaleAnnotationCountsAsNone()
    {
        var c = F.Mk("x", State.Them) with { Annotation = new Annotation { State = State.Hot, Title = "t", Stale = true } };
        Assert.Null(AnnotationOf(c, true));
        Assert.Equal(State.Them, StateOf(c, true));
        Assert.Equal(StateSource.Rules, StateSourceOf(c, true));
    }

    public static TheoryData<State?, State?, bool, StateSourceKind, State?> SourceCases => new()
    {
        { null, null, false, StateSourceKind.AssistantOff, null },
        { null, State.Hot, false, StateSourceKind.AssistantOff, null },
        { null, null, true, StateSourceKind.Rules, null },
        { null, State.You, true, StateSourceKind.AssistantKept, null },
        { null, State.Hot, true, StateSourceKind.AssistantChanged, State.You },
        { State.Them, null, true, StateSourceKind.User, null },
        { State.Them, State.Hot, true, StateSourceKind.User, null },
        { State.Them, State.Hot, false, StateSourceKind.User, null },
        { State.Them, null, false, StateSourceKind.User, null },
    };

    [Theory]
    [MemberData(nameof(SourceCases))]
    public void StateSourceOfCase(State? user, State? annotation, bool annotated, StateSourceKind kind, State? from)
    {
        var c = F.Mk("x", State.You, user: user) with
        {
            Annotation = annotation is null ? null : new Annotation { State = annotation, Title = "t" },
        };
        Assert.Equal(new StateSource(kind, from), StateSourceOf(c, annotated));
    }
}
