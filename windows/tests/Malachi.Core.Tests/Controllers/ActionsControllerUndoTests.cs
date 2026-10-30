// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Undo of moves over the actions controller (ActionsController.Undo.cs;
// GTK ui/internal/window/undo.go recordUndo, recordBoardUndo,
// showUndoToast, undoLast, runUndo, afterUndo, takeUndoReselect, and the
// recording halves of actions.go and context_menus.go), exercised against
// MailFixture through ActionsControllerHarness. The GTK window has no
// tests of these (only undoAccelAllowed's, which ShortcutMapTests port);
// no Swift port exists yet. The toasts with a button go to a recording
// IActionToasts.

using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Controllers;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Malachi.Core.Tests.Model;
using Malachi.Core.Undos;
using Xunit;
using static Malachi.Core.Tests.Controllers.ActionsControllerHarness;

namespace Malachi.Core.Tests.Controllers;

public sealed class ActionsControllerUndoTests
{
    [Fact]
    public async Task TrashThenCtrlZMovesBackAndSelectsTheRow()
    {
        await using var h = await StartAsync(messages: In(Inbox, Msg("m1", 1), Msg("m2", 2), Msg("m3", 3)), confirmDelete: false);
        var toasts = Install(h);

        await h.Run(() => h.Actions.Trash(["m2"], ""));
        await h.IdleAsync();
        Assert.Equal(["m3", "m1"], Ids(h.List.Rows));
        var shown = Assert.Single(toasts.Shown);
        Assert.Equal("Moved 1 message to Trash.", shown.Text);
        Assert.Equal(ActionsController.UndoToastSeconds, shown.Seconds);
        Assert.Equal("Undo (Ctrl+Z)", shown.Button?.Label);
        Assert.Equal(1, h.Actions.UndoHistory.Count);
        Assert.Empty(h.Log.Toasts);

        await h.Run(h.Actions.UndoLast);
        await h.IdleAsync();
        var back = h.Fixture.MoveRequests[^1];
        Assert.Equal(("a", "in"), (back.AccountId.Value, back.TargetFolderId.Value));
        Assert.Equal(["m2"], back.MessageIds.Select(id => id.Value));
        Assert.Equal(["Moved 1 message back."], h.Log.Toasts);
        Assert.Contains(shown, toasts.Dismissed);
        Assert.Equal(["a"], h.Mailbox.FoldersLoaded.Select(a => a.Value));
        Assert.Equal(1, h.List.Reloads);
        Assert.Equal(["m3", "m2", "m1"], Ids(h.List.Rows));
        Assert.Equal([new ListKey(Message: "m2")], h.List.SelectedKeys);
        Assert.Equal(0, h.Actions.UndoHistory.Count);

        // Nothing left.
        await h.Run(h.Actions.UndoLast);
        await h.IdleAsync();
        Assert.Equal("Nothing to undo.", h.Log.Toasts[^1]);
    }

    [Fact]
    public async Task TheToastsButtonTakesBackItsOwnMove()
    {
        await using var h = await StartAsync(messages: In(Inbox, Msg("m1", 1), Msg("m2", 2)));
        var toasts = Install(h);

        await h.Run(() => h.Actions.Archive(["m1"]));
        await h.IdleAsync();
        await h.Run(() => h.Actions.MoveTo(["m2"], ArchiveFolder.Folder));
        await h.IdleAsync();
        Assert.Equal(["Archived 1 message.", "Moved 1 message to “arch”."], toasts.Shown.Select(t => t.Text));
        // The second move's toast took the first one's place.
        Assert.Contains(toasts.Shown[0], toasts.Dismissed);
        Assert.Equal(2, h.Actions.UndoHistory.Count);

        // The first toast's button still takes back the archive, not the newest move.
        await h.Run(() => toasts.Shown[0].Button!.Clicked());
        await h.IdleAsync();
        var back = h.Fixture.MoveRequests[^1];
        Assert.Equal(["m1"], back.MessageIds.Select(id => id.Value));
        Assert.Equal("in", back.TargetFolderId.Value);
        Assert.Equal(1, h.Actions.UndoHistory.Count);
        Assert.Equal("Moved 1 message back.", h.Log.Toasts[^1]);

        // Clicked again: gone already, nothing moves.
        var moves = h.Fixture.MoveRequests.Count;
        await h.Run(() => toasts.Shown[0].Button!.Clicked());
        await h.IdleAsync();
        Assert.Equal(moves, h.Fixture.MoveRequests.Count);
    }

    [Fact]
    public async Task UndoSelectsTheConversationRow()
    {
        await using var h = await StartAsync(messages: In(Inbox, ThreadedMessages()), grouped: true, confirmDelete: false);
        Install(h);
        await h.Run(() =>
        {
            h.List.Select(new ListKey(Thread: "t3"));
            h.List.SelectedIds((_, ids) => h.Actions.Trash(ids, ""));
        });
        await h.IdleAsync();
        Assert.DoesNotContain("T:t3", Ids(h.List.Rows));
        Assert.Equal(1, h.Actions.UndoHistory.Count);

        await h.Run(h.Actions.UndoLast);
        await h.IdleAsync();
        var back = h.Fixture.MoveRequests[^1];
        Assert.Equal(["c1", "c2"], back.MessageIds.Select(id => id.Value).Order());
        Assert.Contains("T:t3", Ids(h.List.Rows));
        Assert.Equal([new ListKey(Thread: "t3")], h.List.SelectedKeys);
    }

    [Fact]
    public async Task WhatCannotBeTakenBackIsNotRecorded()
    {
        Folder[] folders =
        [
            .. TestFolders(),
            MailModelTests.TestFolder("all", "All Mail", FolderRole.All, synced: false),
        ];
        await using var h = await StartAsync(
            folders: folders,
            messages: new Dictionary<FolderKey, MessageSummary[]>
            {
                [Inbox] = [Msg("m1", 1), Msg("m2", 2)],
                [Trash] = [Msg("t1", 3) with { FolderId = Trash.Folder }],
            },
            confirmDelete: false);
        var toasts = Install(h);

        // A move to a folder the daemon does not keep.
        await h.Run(() => h.Actions.MoveTo(["m1"], "all"));
        await h.IdleAsync();
        Assert.Single(h.Fixture.MoveRequests);
        Assert.Equal(0, h.Actions.UndoHistory.Count);

        // A refused move.
        h.Fixture.Fail(API.MessageMove.Name, Error(ErrorCode.StorageError, "disk"));
        await h.Run(() => h.Actions.Archive(["m2"]));
        await h.IdleAsync();
        Assert.Equal(0, h.Actions.UndoHistory.Count);
        h.Fixture.Succeed(API.MessageMove.Name);

        // A message expunged from Trash.
        await h.SelectAsync(Trash);
        await h.Run(() => h.Actions.Trash(["t1"], ""));
        await h.IdleAsync();
        Assert.Single(h.Fixture.DeleteRequests);
        Assert.Equal(0, h.Actions.UndoHistory.Count);
        Assert.Empty(toasts.Shown);
    }

    [Fact]
    public async Task EmptyingTheTrashForgetsTheMovesIntoIt()
    {
        await using var h = await StartAsync(messages: In(Inbox, Msg("m1", 1), Msg("m2", 2)), confirmDelete: false);
        Install(h);
        await h.Run(() => h.Actions.Trash(["m1"], ""));
        await h.IdleAsync();
        await h.Run(() => h.Actions.Archive(["m2"]));
        await h.IdleAsync();
        Assert.Equal(2, h.Actions.UndoHistory.Count);

        await h.Run(() => h.Actions.EmptyFolder(Trash));
        await h.IdleAsync();
        Assert.Single(h.Fixture.FolderEmptyRequests);
        Assert.Equal(1, h.Actions.UndoHistory.Count);
        Assert.Equal(Undo.Kind.Archive, h.Actions.UndoHistory.Peek()?.Kind);
    }

    [Fact]
    public async Task AFailedUndoSaysSoAndLeavesTheHistory()
    {
        await using var h = await StartAsync(messages: In(Inbox, Msg("m1", 1)), confirmDelete: false);
        Install(h);
        await h.Run(() => h.Actions.Trash(["m1"], ""));
        await h.IdleAsync();

        h.Fixture.Fail(API.MessageMove.Name, Error(ErrorCode.StorageError, "disk"));
        await h.Run(h.Actions.UndoLast);
        await h.IdleAsync();
        Assert.Equal(["Moving the message failed"], h.Log.Toasts);
        Assert.Equal(0, h.Actions.UndoHistory.Count);
        // The counts are read again all the same.
        Assert.Equal(["a"], h.Mailbox.FoldersLoaded.Select(a => a.Value));
    }

    [Fact]
    public async Task TheBoardsMovesGoBackThroughTheBoard()
    {
        await using var h = await StartAsync(messages: In(Inbox, Msg("m1", 1)));
        var toasts = Install(h);
        var undone = new List<Undo.Entry>();
        await h.Run(() =>
        {
            h.Actions.UndoBoard = undone.Add;
            h.Actions.RecordBoardUndo(new Board.ArchiveOutcome
            {
                Case = "c_1",
                Account = Acc,
                Moved = [new BoardMoved { MessageId = "m9", FromFolderId = "in" }],
                Text = "Archived 1 message.",
                UndoLabel = "Undo",
            });
            // Without moved messages: the plain toast, nothing recorded.
            h.Actions.RecordBoardUndo(new Board.ArchiveOutcome { Case = "c_2", Account = Acc, Text = "Done.", Trash = true });
        });
        await h.IdleAsync();
        Assert.Equal("Archived 1 message.", Assert.Single(toasts.Shown).Text);
        Assert.Equal(["Done."], h.Log.Toasts);
        Assert.Equal(1, h.Actions.UndoHistory.Count);
        Assert.Equal("arch", h.Actions.UndoHistory.Peek()?.Target.Value);

        await h.Run(h.Actions.UndoLast);
        await h.IdleAsync();
        var e = Assert.Single(undone);
        Assert.Equal((Undo.Kind.BoardArchive, "c_1"), (e.Kind, e.Case.Value));
        Assert.Equal([new Undo.Moved("m9", "in")], e.Moves);
        Assert.Empty(h.Fixture.MoveRequests); // the board moves them
    }

    // The recording overlay, installed on the actions.
    private static RecordingToasts Install(ActionsControllerHarness h)
    {
        var toasts = new RecordingToasts();
        h.Actions.ActionToasts = toasts;
        return toasts;
    }

    private static Dictionary<FolderKey, MessageSummary[]> In(FolderKey k, params MessageSummary[] list) => new() { [k] = list };

    private static RpcError Error(int code, string message) => new() { Code = code, Message = message };

    /// <summary>The toasts with a button the actions showed and took away, in order.</summary>
    private sealed class RecordingToasts : IActionToasts
    {
        public List<Toast> Shown { get; } = [];

        public List<Toast> Dismissed { get; } = [];

        public Toast Show(Toast toast)
        {
            Shown.Add(toast);
            return toast;
        }

        public void Dismiss(Toast toast) => Dismissed.Add(toast);
    }
}
