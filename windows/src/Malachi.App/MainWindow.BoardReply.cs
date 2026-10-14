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
    /// The user quits: the board's replies settle (at most
    /// <see cref="BoardReplyController.EndWait"/>) while the connection
    /// stands; one that could not be saved or sent asks "Quit without saving
    /// a reply?" ("Quit with a reply still sending?" when only a send is
    /// unanswered; Cancel keeps the app running and stops nothing, and the
    /// sequence's Abandoned shows the replies again). The suggested reply
    /// under way is left alone here: it ends past the point of no return
    /// (QuitSteps.EndBoardReply). False abandons the Quit.
    /// </summary>
    public async Task<bool> FinishBoardRepliesForQuitAsync()
    {
        if (boardReplies is { } host && !await host.FinishAllAsync(BoardReplyController.EndWait))
        {
            return await state.Alerts.ConfirmDestructiveAsync(
                this, Board.Text.QuitHeading(host.HasUnsaved, host.HasSending), Board.Text.QuitUnsavedBody, Board.Text.QuitAnyway);
        }
        return true;
    }

    /// <summary>
    /// The session ends: the board's replies settle without a question;
    /// what cannot be saved in time is lost with the session (the sequence
    /// bounds the wait).
    /// </summary>
    public async Task SettleBoardRepliesAsync()
    {
        if (boardReplies is { } host)
        {
            _ = await host.FinishAllAsync(BoardReplyController.EndWait);
        }
    }

    /// <summary>A Quit the user abandoned: the board's parts show again as they should.</summary>
    public void QuitAbandoned() => SyncBoardParts(force: true);

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
        // The page's Escape asks where the keyboard is (Board.EscapeFor).
        BoardView.ReplyHasKeyboard = () => boardReplies?.KeyboardInPane == true;
        BoardView.ReplyPopupOpen = () => boardReplies?.PanePopupOpen == true;

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
