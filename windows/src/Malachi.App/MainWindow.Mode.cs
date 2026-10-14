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
// board's "…" menu have Mail and Board too, and Ctrl+1 and Ctrl+2 switch
// (Board.BoardKeys; ShortcutMap's, so they reach the window from a
// WebView2 that has the keyboard too). The window opens in Mail and
// settles its start with Board.StartDecision (GTK window.go decideStart):
// once the daemon's board preferences are known, in the mode Open at Launch
// says (board-start-mode, board-last-mode, the board turned on), unless
// the user switched or clicked or typed in Mail first, and in Mail when
// Board.StartWait passes without them. Until then nothing is written to
// board-last-mode (a Last Used Board survives a daemon that never
// answers); from then on every switch writes it, and a start settled at
// once writes the mode it opened in (GTK modeShown). With the board turned off (Settings →
// General → Show the Board) the switch hides, Board is disabled and the
// window shows Mail. While the board shows:
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
using System.Diagnostics;
using Malachi.App.Boards;
using Malachi.App.Commands;
using Malachi.App.Shell;
using Malachi.Core.Boards;
using Malachi.Core.Controllers;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;

namespace Malachi.App;

/// <summary>The main window's Mail and Board modes.</summary>
public sealed partial class MainWindow
{
    private Board.Mode mode = Board.InitialMode;
    private Integration? boardIntegration;

    // The daemon's board preferences say the board is on (unknown: on).
    private bool boardEnabled = true;

    // The mode of the launch is decided (Board.StartDecision): the
    // preferences no longer move the window.
    private bool startModeDecided;

    // Board.StartDecision's inputs while the start is open: when the window
    // opened, whether the user switched or acted in Mail, the bound's timer
    // and the Mail page's watchers (removed once decided).
    private long startBegan;
    private bool startSwitched;
    private bool startInteracted;
    private Microsoft.UI.Dispatching.DispatcherQueueTimer? startTimer;
    private PointerEventHandler? startPointer;
    private KeyEventHandler? startKey;
    private BoardObserverToken? boardEnabledToken;

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
        if (next == mode || (next == Board.Mode.Board && (BoardView.Controller is null || !boardEnabled)))
        {
            SyncModeSwitch();
            return;
        }
        if (!startModeDecided)
        {
            // A switch before the start is decided is the user's own (the
            // decision's own switch comes after it is decided).
            startSwitched = true;
            DecideStart(timedOut: false);
        }
        mode = next;
        WriteLastMode();
        // The board's conversation cards and inline reply follow the mode.
        SyncBoardParts();
        if (next == Board.Mode.Board)
        {
            // A Show in Mail still waiting selects nothing in the hidden panes.
            boardIntegration?.LeftMail();
            var controller = BoardView.Controller!;
            // Board View (Settings → General → Board) until the user picks a
            // style in this run, and the saved account filter the first time.
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
        Commands.ShowBoard.CanExecute = () => boardEnabled;
        // Ctrl+1 and Ctrl+2 are ShortcutMap's (the router: root accelerators,
        // and the WebView2 keys while a page has the keyboard).
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
        BeginStart();
    }

    // The start of the window (GTK setupModeMemory): decided at once when
    // Open at Launch is Mail whatever the preferences say, which writes the
    // mode shown; else the Mail page's clicks and keys and the StartWait
    // bound may settle it.
    private void BeginStart()
    {
        startBegan = Stopwatch.GetTimestamp();
        DecideStart(timedOut: false);
        if (startModeDecided)
        {
            WriteLastMode();
            return;
        }
        startPointer = (_, _) => MailActed();
        // After the key: the decision may switch modes, which must not happen
        // under the key's own handling.
        startKey = (_, _) => DispatcherQueue.TryEnqueue(MailActed);
        AssistantSplit.AddHandler(UIElement.PointerPressedEvent, startPointer, handledEventsToo: true);
        AssistantSplit.AddHandler(UIElement.KeyDownEvent, startKey, handledEventsToo: true);
        var timer = DispatcherQueue.CreateTimer();
        timer.Interval = Board.StartWait;
        timer.IsRepeating = false;
        timer.Tick += (t, _) =>
        {
            t.Stop();
            DecideStart(timedOut: true);
        };
        startTimer = timer;
        timer.Start();
        Closed += (_, _) =>
        {
            startTimer?.Stop();
            startTimer = null;
        };
    }

    // A click or a key in Mail (the folders, the list, the reader's chrome)
    // settles the start in Mail (Board.StartDecision).
    private void MailActed()
    {
        if (!startModeDecided && mode == Board.Mode.Mail)
        {
            startInteracted = true;
            DecideStart(timedOut: false);
        }
    }

    // Board.StartDecision again (the preferences arrived, the user switched
    // or acted in Mail, timedOut: the StartWait bound fired); once decided
    // the window opens the Board if the decision says so. The preferences
    // count as known only once the board is attached (SetMode needs its
    // controller).
    private void DecideStart(bool timedOut)
    {
        if (startModeDecided)
        {
            return;
        }
        var prefs = state.BoardPreferences.Preferences;
        var known = prefs is not null && boardIntegration is not null;
        var waited = Stopwatch.GetElapsedTime(startBegan);
        if (timedOut && waited < Board.StartWait)
        {
            waited = Board.StartWait;
        }
        var s = state.Settings;
        var (start, decided) = Board.StartDecision(
            s.BoardStartMode, Board.ParseMode(s.BoardLastMode), known, prefs?.Enabled ?? true, startSwitched, startInteracted, waited);
        if (!decided)
        {
            return;
        }
        startModeDecided = true;
        startTimer?.Stop();
        startTimer = null;
        if (startPointer is not null)
        {
            AssistantSplit.RemoveHandler(UIElement.PointerPressedEvent, startPointer);
            startPointer = null;
        }
        if (startKey is not null)
        {
            AssistantSplit.RemoveHandler(UIElement.KeyDownEvent, startKey);
            startKey = null;
        }
        if (start == Board.Mode.Board && mode == Board.Mode.Mail)
        {
            SetMode(start);
        }
    }

    // Open at Launch's Last Used reads it (GTK modeShown).
    private void WriteLastMode()
    {
        if (state.Settings.BoardLastMode != mode.Nick)
        {
            state.Settings.BoardLastMode = mode.Nick;
        }
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
        AttachBoardParts(integration, actions);
        BoardView.ShowMode(mode);
        // Show the Board, and Open at Launch once the preferences are known.
        boardEnabledToken = state.BoardPreferences.Observe(BoardPreferencesChanged);
        Closed += (_, _) =>
        {
            boardEnabledToken?.Cancel();
            boardEnabledToken = null;
        };
        BoardPreferencesChanged();
    }

    // The daemon's board preferences changed or came: the board on or off
    // (window.go's mode memory and the hidden switch), and the mode of the
    // launch the first time they are known.
    private void BoardPreferencesChanged()
    {
        var prefs = state.BoardPreferences.Preferences;
        var enabled = prefs?.Enabled ?? true;
        if (enabled != boardEnabled)
        {
            boardEnabled = enabled;
            ModeSwitch.Visibility = enabled ? Visibility.Visible : Visibility.Collapsed;
            if (!enabled && mode == Board.Mode.Board)
            {
                SetMode(Board.Mode.Mail);
            }
            Commands.ShowBoard.Refresh();
        }
        DecideStart(timedOut: false);
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
