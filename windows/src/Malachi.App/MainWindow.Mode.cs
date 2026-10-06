// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MainWindow/MainWindowController+Mode.swift
// (setMode, viewsMail, applyTitle, setWindowMode, command(_:), ModeSwitch)
// and the window's part of MainWindowController+Board.swift (the board's
// actions, its keyboard on the way back); GTK: window/board.go
// (setupBoardMode, setMode, applyMessageAccels, messageAccelsAllowed).
//
// The main window's two modes (Core's Board.Mode): Mail shows the panes
// (AssistantSplit), Board the board's page (BoardView) in their place; the
// status line stays. The hidden mode keeps its state: the mail's folder,
// selection, scroll positions, reader, search and the assistant's
// transcript are there on the way back, and so is the board's style. The
// switch is the two-segment control at the start of the title bar (icons,
// the modes' names as tooltips); the sidebar's primary menu and the
// board's "…" menu have Mail and Board too; no key switches. The mode is
// not remembered (Board.InitialMode). While the board shows:
//
// - the mail's commands stand still (Board.Allows through AppCommand.Gate):
//   the single keys of the list (A, J, U, S, Delete) and the reply keys
//   act on no message the user cannot see; Check for New Mail and New
//   Message stay; F10 opens the board's "…" menu;
// - the title bar's search box hides (Find… is the mail's);
// - the toasts lie over the board (BoardPage.ToastLayer);
// - the window does not count as looking at the folder (ViewsMail), so the
//   folder's desktop notifications stay; back in Mail an active window
//   withdraws them, as becoming active does;
// - what reaches the window from elsewhere decides with Board.ModeFor: the
//   status line's Outbox and the assistant panel bring Mail back.

using System;
using Malachi.App.Boards;
using Malachi.App.Commands;
using Malachi.App.Shell;
using Malachi.Core.Boards;
using Malachi.Core.Controllers;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App;

/// <summary>The main window's Mail and Board modes.</summary>
public sealed partial class MainWindow
{
    private Board.Mode mode = Board.InitialMode;
    private Integration? boardIntegration;

    // The caption of Mail ("<folder> – Malachi Mail"), kept while the board shows.
    private string mailCaption = Core.AppIdentity.DisplayName;

    /// <summary>What the window shows: the mail or the board.</summary>
    public Board.Mode Mode => mode;

    /// <summary>
    /// Whether the user looks at the selected folder (Board.ViewsMail): the
    /// window is the active one and shows the mail.
    /// </summary>
    public bool ViewsMail => Board.ViewsMail(mode, state.IsMainWindowActive);

    /// <summary>
    /// Shows <paramref name="next"/>; the current one changes nothing. The
    /// board takes the keyboard on entry; Mail gives it back to the list.
    /// </summary>
    public void SetMode(Board.Mode next)
    {
        if (next == mode || (next == Board.Mode.Board && BoardView.Controller is null))
        {
            SyncModeSwitch();
            return;
        }
        mode = next;
        if (next == Board.Mode.Board)
        {
            // A Show in Mail still waiting selects nothing in the hidden panes.
            boardIntegration?.LeftMail();
            var controller = BoardView.Controller!;
            // The first entry of the run opens the default style (Settings →
            // General → Board), later ones the user's last.
            controller.BoardWillShow();
            // The daemon's board is listed from the first entry on.
            boardIntegration?.StartBoard();
            AssistantSplit.Visibility = Visibility.Collapsed;
            BoardView.Visibility = Visibility.Visible;
            SearchArea.Visibility = Visibility.Collapsed;
            AppTitleBar.IsPaneToggleButtonVisible = false;
            AppTitleBar.IsBackButtonVisible = false;
            MoveToasts(BoardView.ToastLayer);
            BoardView.ApplyAll();
            // The dates may have moved on (a new day); a board that could not
            // be listed is asked for again.
            controller.BoardShown();
            Title = new ListHeading(Board.Text.BoardName, "").Caption;
            DispatcherQueue.TryEnqueue(() => BoardView.FocusContent());
        }
        else
        {
            AssistantSplit.Visibility = Visibility.Visible;
            BoardView.Visibility = Visibility.Collapsed;
            SearchArea.Visibility = Visibility.Visible;
            MoveToasts(MessagePane);
            ApplyLayout();
            Title = mailCaption;
            DispatcherQueue.TryEnqueue(ListPane.FocusList);
            // The user looks at the folder again: its notifications go, as
            // when the window becomes active.
            if (state.IsMainWindowActive && boardIntegration is { } integration && !integration.Mailbox.Scope.IsClosed)
            {
                integration.Mailbox.WithdrawViewedNotifications();
            }
        }
        BoardView.ShowMode(mode);
        Commands.RefreshAll();
        SyncModeSwitch();
    }

    /// <summary>
    /// A request from elsewhere in the application reached the window
    /// (Board.ModeFor): what shows the mail in the window brings Mail back.
    /// </summary>
    public void ApplyModeRequest(Board.Request request) => SetMode(Board.ModeFor(request, mode));

    /// <summary>Show in Mail found the message's row: a narrow window shows the message.</summary>
    internal void ShowRevealedMessage()
    {
        if (layout.Mode == PaneMode.Narrow)
        {
            Navigate(layout.MessageChosen);
        }
    }

    // After InitializePanes: the switch, the mode commands, the gates of
    // the mail's commands, F10.
    private void InitializeMode()
    {
        Commands.ShowMail.Handler = () => SetMode(Board.Mode.Mail);
        Commands.ShowBoard.Handler = () => SetMode(Board.Mode.Board);
        // A segment's click asks for its mode; the switch then shows the
        // mode the window is in (a refused change, the same mode again).
        ModeMailButton.Click += (_, _) =>
        {
            Commands.ShowMail.TryExecute();
            SyncModeSwitch();
        };
        ModeBoardButton.Click += (_, _) =>
        {
            Commands.ShowBoard.TryExecute();
            SyncModeSwitch();
        };
        Func<bool> messageAction = () => Board.Allows(Board.Command.MessageAction, mode);
        Func<bool> mailView = () => Board.Allows(Board.Command.MailView, mode);
        foreach (var c in (AppCommand[])[
            Commands.Reply, Commands.ReplyAll, Commands.Forward, Commands.Trash, Commands.Archive, Commands.Junk,
            Commands.MarkRead, Commands.MarkUnread, Commands.ToggleFlag, Commands.ChangeStatus])
        {
            c.Gate = messageAction;
        }
        foreach (var c in (AppCommand[])[Commands.Search, Commands.LoadImages, Commands.TrustSender])
        {
            c.Gate = mailView;
        }
        // F10: the sidebar's primary menu in Mail, the board's "…" in Board.
        Commands.MainMenu.Handler = () =>
        {
            if (mode == Board.Mode.Board)
            {
                BoardView.ShowMenu();
            }
            else
            {
                SidebarPane.ShowPrimaryMenu();
            }
        };
        Commands.MainMenu.CanExecute = () => mode == Board.Mode.Board || PaneSplit.IsPaneOpen;
        SyncModeSwitch();
    }

    // The Integration exists: the board's page over its controller, the
    // case actions with their ways into the mail.
    private void AttachBoard(Integration integration)
    {
        boardIntegration = integration;
        var actions = integration.MakeBoardActions();
        actions.OnToast = text => Toasts.Show(text);
        integration.BoardController.ToastRequested += (_, text) => Toasts.Show(text);
        BoardView.Attach(actions, Commands);
        BoardView.ShowMode(mode);
    }

    // The toast overlay over the visible mode: the message pane's content in
    // Mail (window.blp's toast_overlay), the whole board in Board.
    private void MoveToasts(Panel host)
    {
        if (ReferenceEquals(ToastsHost.Parent, host))
        {
            return;
        }
        if (ToastsHost.Parent is Panel old)
        {
            old.Children.Remove(ToastsHost);
        }
        host.Children.Add(ToastsHost);
    }

    private void SyncModeSwitch()
    {
        ModeMailButton.IsChecked = mode == Board.Mode.Mail;
        ModeBoardButton.IsChecked = mode == Board.Mode.Board;
    }
}
