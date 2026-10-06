// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/BoardModelTests.swift
// (BoardInlineReplyDetailTests); GTK: ui/internal/board/model_test.go
// (TestDraftIDWithoutText, TestUnstarOnlyForTheStar). Swift's texts check
// is BoardTextTests.InlineReply.

using Malachi.Core.Api;
using Malachi.Core.Board;
using Xunit;
using static Malachi.Core.Board.Board;
using B = Malachi.Core.Board.Board;
using F = Malachi.Core.Tests.Board.BoardFixture;

namespace Malachi.Core.Tests.Board;

public sealed class BoardInlineReplyDetailTests
{
    [Fact]
    public void DraftIdWithoutText()
    {
        // An empty draft is still the case's suggested reply: edited inline.
        var d = Assert.IsType<Detail>(F.View([F.Mk("c1", draft: "")]).Detail);
        Assert.True(d.DraftId == new DraftId("d_c1") && d.Draft == "");
        var none = Assert.IsType<Detail>(F.View([F.Mk("c1")]).Detail);
        Assert.Null(none.DraftId);
    }

    private static Detail DetailOf(BoardReason reason, bool done = false, State? user = null)
    {
        var c = F.Mk("c1", State.Hot, user: user, done: done) with { RuleReason = reason };
        return Assert.IsType<Detail>(F.View([c], configure: v => done ? v with { Filter = Filter.Done } : v).Detail);
    }

    [Fact]
    public void UnstarOnlyForTheStar()
    {
        Assert.True(DetailOf(BoardReason.HotFlagged).CanUnstar);
        Assert.True(DetailOf(BoardReason.HotFlagged, user: State.Info).CanUnstar, "whatever the user's state");
        Assert.False(DetailOf(BoardReason.HotFlagged, done: true).CanUnstar);
        Assert.False(DetailOf(BoardReason.HotImportant).CanUnstar);
        Assert.False(DetailOf(BoardReason.YouAddressed).CanUnstar);
    }

    [Fact]
    public void StaleNotesStayButDoNotCount()
    {
        var a = new Annotation { State = State.Hot, Title = "Old", Summary = "Old summary", Stale = true };
        var c = F.Mk("c1", State.You, annotation: a, draft: "kept");
        var on = Assert.IsType<Detail>(F.View([c], annotated: true).Detail);
        Assert.Equal(B.Text.StaleNotes, on.StaleNote);
        Assert.True(on.State == State.You && on.Title == "Subject c1" && on.Summary == "");
        Assert.True(on.Draft == "kept" && on.DraftId == new DraftId("d_c1"));
        var off = Assert.IsType<Detail>(F.View([c], annotated: false).Detail);
        Assert.Equal("", off.StaleNote);
    }
}
