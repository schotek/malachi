// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MainWindow/MainWindowController+Mode.swift
// (setMode, setupModeMemory, boardEnabledChanged, viewsMail, applyTitle,
// setWindowMode, command(_:), ModeSwitch) and the window's part of
// MainWindowController+Board.swift (the board's actions, its keyboard on
// the way back); GTK: window/board.go (setupBoardMode, setMode,
// applyMessageAccels, messageAccelsAllowed).
//
// The main window's three modes (Core's Board.Mode): Mail shows the panes
// (AssistantSplit), Board the board's page (BoardView) and Calendar the
// calendar's page (CalendarView, MainWindow.Calendar.cs) in their place;
// the status line stays. The hidden modes keep their state: the mail's
// folder, selection, scroll positions, reader, search and the assistant's
// transcript are there on the way back, and so are the board's style and
// the calendar's query. The switch is the three-segment control at the
// start of the title bar (icons, the modes' names as tooltips,
// Board.ModeTexts); the sidebar's primary menu, the board's and the
// calendar's "…" menus have Mail, Board and Calendar too, and Ctrl+1,
// Ctrl+2 and Ctrl+3 switch (Board.BoardKeys, the calendar's
// ShowCalendarKey; ShortcutMap's, so they reach the window from a WebView2
// that has the keyboard too; a VirtualKey is the key whatever the layout,
// as macOS's Board.modeKeyDigit). The window opens in Mail and settles its
// start with Board.StartDecision (GTK window.go decideStart): a Calendar
// start (or Last Used with the Calendar last) at once, a Board start once
// the daemon's board preferences are known, in the mode Open at Launch
// says (board-start-mode, board-last-mode, the board turned on), unless
// the user switched or clicked or typed in Mail first, and in Mail when
// Board.StartWait passes without them. Until then nothing is written to
// board-last-mode (a Last Used Board survives a daemon that never
// answers); from then on every switch writes it, and a start settled at
// once writes the mode it opened in (GTK modeShown). With the board turned
// off (Settings → General → Show the Board) the switch stays for Mail and
// Calendar with its Board segment disabled (macOS As built, docs/calendar.md
// §5.3), Board is disabled and a window showing it goes to Mail. While the
// board or the calendar shows:
//
// - the mail's commands stand still (Board.Allows through AppCommand.Gate):
//   the single keys of the list (A, J, U, S, Delete) and the reply keys
//   act on no message the user cannot see; Check for New Mail and New
//   Message stay, and New Event in the Calendar; F10 opens the page's "…"
//   menu;
// - the title bar's search box hides in Board (Find… is the mail's) and
//   searches the events in the Calendar, where it says so and keeps its
//   own text (MainWindow.Calendar.cs);
// - the toasts lie over the page (BoardPage.ToastLayer, CalendarPage.ToastLayer);
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
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Input;

namespace Malachi.App;

/// <summary>The main window's Mail, Board and Calendar modes.</summary>
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

    /// <summary>What the window shows: the mail, the board or the calendar.</summary>
    public Board.Mode Mode => mode;

    /// <summary>
    /// Whether the user looks at the selected folder (Board.ViewsMail): the
    /// window is the active one and shows the mail.
    /// </summary>
    public bool ViewsMail => Board.ViewsMail(mode, state.IsMainWindowActive);

    /// <summary>
    /// Shows <paramref name="next"/>; the current one changes nothing. The
    /// board and the calendar take the keyboard on entry; Mail gives it back
    /// to the list.
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
        var previous = mode;
        switch (previous)
        {
            case Board.Mode.Mail:
                // A Show in Mail still waiting selects nothing in the hidden
                // panes; a search in the user's own words ends (the box is
                // the mail's).
                boardIntegration?.LeftMail();
                CancelOwnWords();
                break;
            case Board.Mode.Board:
                break;
            case Board.Mode.Calendar:
                LeaveCalendar();
                break;
        }
        mode = next;
        WriteLastMode();
        // The board's conversation cards and inline reply follow the mode:
        // shown in Board only, settled and suspended in Mail and Calendar.
        SyncBoardParts();
        switch (next)
        {
            case Board.Mode.Board:
                ShowBoardMode();
                break;
            case Board.Mode.Calendar:
                EnterCalendar();
                break;
            case Board.Mode.Mail:
                ShowMailMode();
                break;
        }
        BoardView.ShowMode(mode);
        CalendarView.ShowMode(mode);
        Commands.RefreshAll();
        SyncModeSwitch();
        SyncOwnWordsMode();
    }

    // SetMode's part for the board.
    private void ShowBoardMode()
    {
        var controller = BoardView.Controller!;
        // Board View (Settings → General → Board) until the user picks a
        // style in this run, and the saved account filter the first time.
        controller.BoardWillShow();
        // The daemon's board is listed from the first entry on.
        boardIntegration?.StartBoard();
        AssistantSplit.Visibility = Visibility.Collapsed;
        CalendarView.Visibility = Visibility.Collapsed;
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

    // SetMode's part for the mail.
    private void ShowMailMode()
    {
        AssistantSplit.Visibility = Visibility.Visible;
        BoardView.Visibility = Visibility.Collapsed;
        CalendarView.Visibility = Visibility.Collapsed;
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
        Commands.ShowCalendar.Handler = () => SetMode(Board.Mode.Calendar);
        // Ctrl+1, Ctrl+2 and Ctrl+3 are ShortcutMap's (the router: root
        // accelerators, and the WebView2 keys while a page has the keyboard).
        // A segment's click asks for its mode; the switch then shows the
        // mode the window is in (a refused change, the same mode again). Its
        // name and tooltip are the mode's (macOS ModeSwitch).
        var texts = Board.Texts();
        foreach (var (button, segment, command) in ModeSegments)
        {
            var name = texts.Name(segment);
            AutomationProperties.SetName(button, name);
            ToolTipService.SetToolTip(button, name);
            button.Click += (_, _) =>
            {
                command.TryExecute();
                SyncModeSwitch();
            };
        }
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
        // New Event: Mail and the Calendar (its handler is
        // MainWindow.Calendar.cs's NewEvent).
        Commands.NewEvent.Gate = () => Board.Allows(Board.Command.NewEvent, mode);
        // Undo of a move of mail (Ctrl+Z; window/undo.go undoAccelAllowed):
        // Mail and Board, never the Calendar. Its handler is the actions'
        // (MainWindow.Panes.cs).
        Commands.Undo.Gate = () => mode != Board.Mode.Calendar;
        // F10: the sidebar's primary menu in Mail, the page's "…" in Board
        // and Calendar.
        Commands.MainMenu.Handler = () =>
        {
            switch (mode)
            {
                case Board.Mode.Board:
                    BoardView.ShowMenu();
                    break;
                case Board.Mode.Calendar:
                    CalendarView.ShowMenu();
                    break;
                case Board.Mode.Mail:
                    SidebarPane.ShowPrimaryMenu();
                    break;
            }
        };
        Commands.MainMenu.CanExecute = () => mode switch
        {
            Board.Mode.Board or Board.Mode.Calendar => true,
            // While the sidebar that holds its button is shown.
            Board.Mode.Mail => PaneSplit.IsPaneOpen,
            _ => false,
        };
        InitializeCalendar();
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
        switch (start)
        {
            case Board.Mode.Board:
            case Board.Mode.Calendar:
                // Never away from a mode the user chose meanwhile.
                if (mode == Board.Mode.Mail)
                {
                    SetMode(start);
                }
                break;
            case Board.Mode.Mail:
                break;
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
    // (window.go's mode memory; the switch keeps Mail and Calendar with its
    // Board segment disabled, macOS boardEnabledChanged), and the mode of
    // the launch the first time they are known.
    private void BoardPreferencesChanged()
    {
        var prefs = state.BoardPreferences.Preferences;
        var enabled = prefs?.Enabled ?? true;
        if (enabled != boardEnabled)
        {
            boardEnabled = enabled;
            if (!enabled && mode == Board.Mode.Board)
            {
                SetMode(Board.Mode.Mail);
            }
            Commands.ShowBoard.Refresh();
            SyncModeSwitch();
        }
        DecideStart(timedOut: false);
    }

    // Where the keyboard goes when nothing in the window has it (the first
    // show, past the search box): the sidebar's first tab stop in Mail, the
    // page in Board and Calendar.
    private UIElement ModeFocusRoot => mode switch
    {
        Board.Mode.Board => BoardView,
        Board.Mode.Calendar => CalendarView,
        Board.Mode.Mail => SidebarPane,
        _ => SidebarPane,
    };

    // The toast overlay over the visible mode: the message pane's content in
    // Mail (window.blp's toast_overlay), the whole page in Board and Calendar.
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

    // The switch's segments: the button, its mode and the command it runs.
    private (ToggleButton Button, Board.Mode Mode, AppCommand Command)[] ModeSegments =>
    [
        (ModeMailButton, Board.Mode.Mail, Commands.ShowMail),
        (ModeBoardButton, Board.Mode.Board, Commands.ShowBoard),
        (ModeCalendarButton, Board.Mode.Calendar, Commands.ShowCalendar),
    ];

    // The segment of the mode shown is checked; Board's is disabled while the
    // board is off (the switch stays for Mail and Calendar).
    private void SyncModeSwitch()
    {
        foreach (var (button, segment, _) in ModeSegments)
        {
            button.IsChecked = mode == segment;
        }
        ModeBoardButton.IsEnabled = boardEnabled;
    }
}
