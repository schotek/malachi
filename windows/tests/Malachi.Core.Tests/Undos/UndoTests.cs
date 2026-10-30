// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/undo/undo_test.go (TestRecord, TestFromBoard,
// TestCallsGroupByFolder, TestReselect, TestStack, TestStackLimit,
// TestForget, TestTexts); no Swift port exists yet. Beyond Go:
// ReselectKeys, the key choice of window/undo.go takeUndoReselect, and
// Board.UndoArchive's moves being Undo's Calls (board/source.go).

using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Model;
using Malachi.Core.Tests.Model;
using Malachi.Core.Undos;
using Xunit;

namespace Malachi.Core.Tests.Undos;

public sealed class UndoTests
{
    private static MessageSummary Summary(string id, string account, string folder, string? thread = null) =>
        MailModelTests.Summary(id) with
        {
            AccountId = account,
            FolderId = folder,
            ThreadId = thread is null ? (ThreadId?)null : new ThreadId(thread),
        };

    private static Undo.Moved M(string id, string from, string? thread = null) =>
        new(id, from, thread is null ? (ThreadId?)null : new ThreadId(thread));

    [Fact]
    public void Record()
    {
        MessageSummary[] list =
        [
            Summary("m1", "a", "inbox", "t1"),
            Summary("m2", "a", "trash"), // already there
            Summary("m3", "b", "inbox"), // another account
            Summary("m4", "a", "work"),
            Summary("m1", "a", "inbox"), // twice
            Summary("", "a", "inbox"),
            Summary("m5", "a", ""),
        ];
        var e = Undo.Record(Undo.Kind.Trash, "trash", list);
        Assert.NotNull(e);
        Assert.Equal(("a", "trash", Undo.Kind.Trash), (e.Account.Value, e.Target.Value, e.Kind));
        Assert.Equal([M("m1", "inbox", "t1"), M("m4", "work")], e.Moves);
        Assert.Equal(2, e.Count);
        Assert.Null(Undo.Record(Undo.Kind.Trash, "trash", [Summary("m2", "a", "trash")]));
        Assert.Null(Undo.Record(Undo.Kind.Archive, "arch", []));
    }

    [Fact]
    public void FromBoard()
    {
        var e = Undo.FromBoard(true, "a", "c_1",
        [
            new BoardMoved { MessageId = "m1", FromFolderId = "inbox" },
            new BoardMoved { MessageId = "", FromFolderId = "inbox" },
        ]);
        Assert.NotNull(e);
        Assert.Equal(Undo.Kind.BoardTrash, e.Kind);
        Assert.Equal("c_1", e.Case.Value);
        Assert.Single(e.Moves);
        Assert.True(e.Kind.IsBoard());
        Assert.Equal(Undo.Kind.BoardArchive, Undo.FromBoard(false, "a", "c_1", [new BoardMoved { MessageId = "m1", FromFolderId = "inbox" }])?.Kind);
        Assert.Null(Undo.FromBoard(false, "a", "c_1", null));
        Assert.Null(Undo.FromBoard(false, "a", "c_1", []));
        Assert.False(Undo.Kind.Trash.IsBoard());
        Assert.False(Undo.Kind.Move.IsBoard());
    }

    [Fact]
    public void CallsGroupByFolder()
    {
        var e = new Undo.Entry
        {
            Account = "a",
            Moves = [M("m1", "inbox"), M("m2", "work"), M("m3", "inbox"), M("m1", "inbox"), M("", "inbox"), M("m4", "")],
        };
        var got = Undo.Calls(e);
        Assert.Equal(2, got.Count);
        Assert.Equal(("a", "inbox"), (got[0].AccountId.Value, got[0].TargetFolderId.Value));
        Assert.Equal(["m1", "m3"], got[0].MessageIds.Select(id => id.Value));
        Assert.Equal(("a", "work"), (got[1].AccountId.Value, got[1].TargetFolderId.Value));
        Assert.Equal(["m2"], got[1].MessageIds.Select(id => id.Value));
        Assert.Empty(Undo.Calls(new Undo.Entry()));
    }

    [Fact]
    public void BoardUndoArchiveMovesAreUndosCalls()
    {
        BoardMoved[] moved =
        [
            new() { MessageId = "m1", FromFolderId = "inbox" },
            new() { MessageId = "m2", FromFolderId = "work" },
            new() { MessageId = "m3", FromFolderId = "inbox" },
        ];
        var board = Board.UndoArchive(moved, "a", "c_1");
        var undo = Undo.Calls(Undo.FromBoard(false, "a", "c_1", moved)!);
        Assert.Equal(undo.Count, board.Moves.Count);
        for (var i = 0; i < undo.Count; i++)
        {
            Assert.Equal(undo[i].TargetFolderId, board.Moves[i].TargetFolderId);
            Assert.Equal(undo[i].MessageIds, board.Moves[i].MessageIds);
        }
        Assert.Empty(Board.UndoArchive(null, "a", "c_1").Moves);
    }

    [Fact]
    public void Reselect()
    {
        var e = new Undo.Entry
        {
            Account = "a",
            Moves = [M("m1", "inbox", "t1"), M("m2", "work"), M("m3", "inbox"), M("m1", "inbox", "t1")],
        };
        Assert.Equal([M("m1", "inbox", "t1"), M("m3", "inbox")], Undo.Reselect(e, "a", "inbox"));
        Assert.Empty(Undo.Reselect(e, "b", "inbox"));
        Assert.Empty(Undo.Reselect(e, "a", "trash"));
        Assert.Empty(Undo.Reselect(e, "a", ""));
    }

    [Fact]
    public void ReselectKeysPickTheRowsTheListShows()
    {
        Undo.Moved[] back = [M("m1", "inbox", "t1"), M("m2", "inbox", "t2"), M("m3", "inbox", "t2"), M("m4", "inbox"), M("m5", "inbox")];
        var listed = new HashSet<ListKey>
        {
            new(Message: "m1"), // a flat row
            new("t2"), // a folded conversation of m2 and m3
            new("t1", "m1"),
            new(Message: "m4"),
        };
        Assert.Equal(
            [new ListKey(Message: "m1"), new ListKey("t2"), new ListKey(Message: "m4")],
            Undo.ReselectKeys(back, listed.Contains));
        // An expanded conversation: the member's row before the conversation's.
        Assert.Equal(
            [new ListKey("t1", "m1")],
            Undo.ReselectKeys([M("m1", "inbox", "t1")], new HashSet<ListKey> { new("t1", "m1"), new("t1") }.Contains));
        Assert.Empty(Undo.ReselectKeys([M("m9", "inbox")], listed.Contains));
        Assert.Empty(Undo.ReselectKeys([], listed.Contains));
    }

    [Fact]
    public void History()
    {
        var s = new Undo.History();
        Assert.Null(s.Pop());
        Assert.Null(s.Peek());
        var a = s.Push(new Undo.Entry { Account = "a", Target = "trash" });
        var b = s.Push(new Undo.Entry { Account = "a", Target = "arch" });
        var c = s.Push(new Undo.Entry { Account = "b", Target = "trash" });
        Assert.NotEqual(a.Id, b.Id);
        Assert.NotEqual(b.Id, c.Id);
        Assert.Equal(3, s.Count);
        Assert.Equal(c.Id, s.Peek()?.Id);
        Assert.Equal(3, s.Count);
        Assert.Equal(a.Id, s.Take(a.Id)?.Id);
        Assert.Equal(2, s.Count);
        Assert.Null(s.Take(a.Id));
        Assert.Equal(c.Id, s.Pop()?.Id);
        Assert.Equal(b.Id, s.Pop()?.Id);
    }

    [Fact]
    public void HistoryLimit()
    {
        var s = new Undo.History();
        var first = s.Push(new Undo.Entry());
        for (var i = 0; i < Undo.Limit; i++)
        {
            s.Push(new Undo.Entry());
        }
        Assert.Equal(Undo.Limit, s.Count);
        Assert.Null(s.Take(first.Id));
    }

    [Fact]
    public void Forget()
    {
        var s = new Undo.History();
        s.Push(new Undo.Entry { Account = "a", Target = "trash" });
        s.Push(new Undo.Entry { Account = "a", Target = "arch" });
        s.Push(new Undo.Entry { Account = "b", Target = "trash" });
        s.ForgetFolder("a", "trash");
        Assert.Equal(2, s.Count);
        s.Forget("b");
        var e = s.Pop();
        Assert.Equal(0, s.Count);
        Assert.Equal("arch", e?.Target.Value);
    }

    [Fact]
    public void Texts()
    {
        var one = new Undo.Entry { Moves = [M("m1", "inbox")] };
        var two = new Undo.Entry { Moves = [M("m1", "inbox"), M("m2", "inbox")] };
        (Undo.Kind Kind, string Name, Undo.Entry E, string Want)[] cases =
        [
            (Undo.Kind.Trash, "", one, "Moved 1 message to Trash."),
            (Undo.Kind.BoardTrash, "", two, "Moved 2 messages to Trash."),
            (Undo.Kind.Archive, "", one, "Archived 1 message."),
            (Undo.Kind.BoardArchive, "", two, "Archived 2 messages."),
            (Undo.Kind.Junk, "", two, "Moved 2 messages to Junk."),
            (Undo.Kind.Move, "Work", one, "Moved 1 message to “Work”."),
            (Undo.Kind.Move, "", two, "Moved 2 messages."),
        ];
        foreach (var c in cases)
        {
            Assert.Equal(c.Want, Undo.Done(c.E with { Kind = c.Kind, TargetName = c.Name }));
        }
        Assert.Equal("Undo", Undo.Button(""));
        Assert.Equal("Undo (Ctrl+Z)", Undo.Button("Ctrl+Z"));
        Assert.Equal("Moved 2 messages back.", Undo.Undone(two));
        Assert.Equal("Moved 1 message back.", Undo.Undone(one));
        Assert.Equal("Could not move the messages back.", Undo.Failed());
        Assert.Equal("Nothing to undo.", Undo.Nothing());
    }
}
