// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the window's part of macos/Sources/MalachiMail/Board/BoardPageViewController.swift
// and MainWindowController+Board.swift (the reply editor host and the
// conversation block made for the daemon's board, suspended with the page,
// the window's Send and Save Draft reaching the inline editor) and of
// AppDelegate.swift's quit (BoardReplyEditorHost.finishAll, "Quit without
// saving a reply?"); GTK: window/board.go (setupBoardMode's replies and
// conversation), board_reply_editor.go (suspendBoardReplies,
// FinishBoardReplies, ResumeBoardReplies, CloseBoardReplies) and
// board_quit.go.
//
// Over the daemon's board the detail's conversation is the HTML cards
// (BoardConversationBlock over Mail's reader services) and its reply slot
// the inline editor with Suggest Reply (BoardReplyEditorHost); the samples
// keep their excerpts and their static block. Both follow what the user
// sees: Mail mode and a hidden window detach the cards (every web view goes)
// and retire the live reply pane (it is saved first, never dropped); the
// board in sight again brings them back. Ctrl+Enter and Ctrl+S while the
// inline editor's page has the keyboard come to this window's router (no
// XAML key event fires in a WebView2) and run the pane's Send and Save
// Draft; elsewhere in the pane its own scoped accelerators do.

using System;
using System.Threading.Tasks;
using Malachi.App.Boards;
using Malachi.App.Shell;
using Malachi.Core.Boards;
using Malachi.Core.Controllers;

namespace Malachi.App;

/// <summary>The main window's suggested replies and conversation cards on the board.</summary>
public sealed partial class MainWindow
{
    private BoardReplyEditorHost? boardReplies;
    private BoardConversationBlock? boardConversation;
    private bool boardPartsShown;

    /// <summary>
    /// The application quits: the board's replies settle (at most
    /// <see cref="BoardReplyController.EndWait"/>) while the connection
    /// stands; one that could not be saved or sent asks "Quit without saving
    /// a reply?" (Cancel keeps the app running and stops nothing). Then the
    /// suggested reply under way stops and deletes a draft it did not link
    /// yet. False abandons the Quit.
    /// </summary>
    public async Task<bool> FinishBoardRepliesForQuitAsync()
    {
        if (boardReplies is { } host && !await host.FinishAllAsync(BoardReplyController.EndWait))
        {
            var quit = await state.Alerts.ConfirmDestructiveAsync(
                this, Board.Text.QuitUnsavedHeading, Board.Text.QuitUnsavedBody, Board.Text.QuitAnyway);
            if (!quit)
            {
                // The user stays: the page shows its reply again.
                SyncBoardParts(force: true);
                return false;
            }
        }
        await state.BoardReply.CancelAndCleanUpAsync();
        return true;
    }

    // After the page is attached: the parts of the daemon's board, the
    // window's Send and Save Draft for the inline editor.
    private void AttachBoardParts(Integration integration, BoardActions actions)
    {
        var detail = BoardView.Detail;
        detail.HostWindow = this;
        if (integration.BoardSamples)
        {
            return;
        }
        if (integration.Reader is { } reader)
        {
            boardConversation = new BoardConversationBlock(detail, reader.Services);
            detail.ConversationPart = boardConversation;
        }
        var host = new BoardReplyEditorHost(actions, detail, state, integration.Compose, Toasts, () => this);
        boardReplies = host;
        actions.InlineReply = host;
        detail.ReplyPart = host.Slot;

        var c = Commands;
        c.Send.Handler = () => boardReplies?.EditorPane?.Send();
        c.Send.CanExecute = () => boardReplies?.EditorPane is { SendEnabled: true };
        c.SaveDraft.Handler = () => boardReplies?.EditorPane?.SaveDraft();
        c.SaveDraft.CanExecute = () => boardReplies?.EditorPane is { IsComment: false };

        ShownChanged += (_, _) => SyncBoardParts();
        Closed += (_, _) =>
        {
            boardReplies?.Dispose();
            boardConversation?.Close();
        };
        boardPartsShown = true;
        SyncBoardParts(force: true);
    }

    // The parts are in sight while the window shows the board.
    private void SyncBoardParts(bool force = false)
    {
        var shows = mode == Board.Mode.Board && AppWindow.IsVisible;
        if (!force && shows == boardPartsShown)
        {
            return;
        }
        boardPartsShown = shows;
        if (shows)
        {
            boardConversation?.Attach();
            boardReplies?.Resume();
        }
        else
        {
            boardConversation?.Detach();
            boardReplies?.Suspend();
        }
    }
}
