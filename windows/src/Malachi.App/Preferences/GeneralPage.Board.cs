// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the Board group of
// macos/Sources/MalachiMail/Preferences/GeneralPaneViewController.swift; GTK:
// ui/internal/window/preferences_board.go (bindBoard, windowOf,
// withWindow, boardWindowsDebounce).
//
// Preferences → General → Board: Show the Board (the daemon's board
// preferences: off also turns automatic triage off in the same write, so
// nothing runs on its own), Board View (board-default-style: Last Used,
// List, Columns, Today) and Open at Launch (board-start-mode: Mail, Board,
// Last Used); and Keep cases for, how long each state keeps a case (1 to
// 365 days, written 600 ms after the last step, the last step written as
// the window closes). The daemon's rows are insensitive until its
// preferences are known, the windows also while the board is off. Writes
// are optimistic through the application's BoardPreferencesController: a
// refused one is taken back and toasted by the application. Every text is
// Core's (Board.Text).

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Controllers;
using Malachi.Core.Settings;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Preferences;

public sealed partial class GeneralPage
{
    // How long the windows' rows wait after the last step before the daemon
    // is told (preferences_board.go boardWindowsDebounce).
    private static readonly TimeSpan BoardWindowsDebounce = TimeSpan.FromMilliseconds(600);

    private BoardObserverToken? boardPrefsToken;
    private DispatcherQueueTimer? boardWindowsTimer;
    private bool syncingBoard;
    private bool boardClosed;

    private BoardPreferencesController BoardPrefs => state.BoardPreferences;

    // The four rows in Board.States' order (Hot, You, Them, Info).
    private IReadOnlyList<NumberBox> BoardWindowBoxes => [BoardWindowHotBox, BoardWindowYouBox, BoardWindowThemBox, BoardWindowInfoBox];

    private IReadOnlyList<CommunityToolkit.WinUI.Controls.SettingsCard> BoardWindowRows => [BoardWindowHotRow, BoardWindowYouRow, BoardWindowThemRow, BoardWindowInfoRow];

    private void InitializeBoardGroup(SettingBindings bindings)
    {
        var s = state.Settings;
        BoardHeading.Text = Board.Text.BoardName;
        BoardShowRow.Header = Board.Text.ShowBoardSetting;
        BoardShowRow.Description = Board.Text.ShowBoardSettingSubtitle;
        AutomationProperties.SetName(BoardShowSwitch, Board.Text.ShowBoardSetting);
        BoardStyleRow.Header = Board.Text.DefaultStyleSetting;
        AutomationProperties.SetName(BoardStyleBox, Board.Text.DefaultStyleSetting);
        foreach (var d in Board.DefaultStyles)
        {
            BoardStyleBox.Items.Add(new ComboBoxItem { Content = Board.Text.DefaultStyleTitle(d) });
        }
        BoardStartRow.Header = Board.Text.StartModeSetting;
        AutomationProperties.SetName(BoardStartBox, Board.Text.StartModeSetting);
        foreach (var m in Board.StartModes)
        {
            BoardStartBox.Items.Add(new ComboBoxItem { Content = Board.Text.StartModeTitle(m) });
        }
        bindings.Choice(BoardStyleBox, SettingsKey.BoardDefaultStyle, Board.DefaultStyles, () => s.BoardDefaultStyle, v => s.BoardDefaultStyle = v);
        bindings.Choice(BoardStartBox, SettingsKey.BoardStartMode, Board.StartModes, () => s.BoardStartMode, v => s.BoardStartMode = v);

        BoardWindowsHeading.Text = Board.Text.WindowsSetting;
        BoardWindowsDescription.Text = Board.Text.WindowsSettingSubtitle;
        for (var i = 0; i < Board.States.Count; i++)
        {
            var name = Board.Text.StateName(Board.States[i]);
            BoardWindowRows[i].Header = name;
            AutomationProperties.SetName(BoardWindowBoxes[i], name);
            BoardWindowBoxes[i].ValueChanged += OnBoardWindowChanged;
        }

        boardPrefsToken = BoardPrefs.Observe(UpdateBoardGroup);
        if (BoardPrefs.Preferences is null)
        {
            BoardPrefs.Load();
        }
        UpdateBoardGroup();
    }

    // The window closed: the last step of the windows is still written.
    private void CloseBoardGroup()
    {
        if (boardWindowsTimer is { } timer)
        {
            timer.Stop();
            boardWindowsTimer = null;
            WriteBoardWindows(final: true);
        }
        boardClosed = true;
        boardPrefsToken?.Cancel();
        boardPrefsToken = null;
    }

    // The rows from the daemon's preferences (preferences_board.go update):
    // a row the user is stepping keeps its value until it is written.
    private void UpdateBoardGroup()
    {
        if (boardClosed)
        {
            return;
        }
        var prefs = BoardPrefs.Preferences;
        var known = prefs is not null;
        var enabled = prefs is { } current && current.Enabled;
        syncingBoard = true;
        try
        {
            BoardShowRow.IsEnabled = known;
            BoardShowSwitch.IsOn = !known || enabled;
            var windows = prefs is { } p && BoardPreferencesController.ValidWindows(p.Windows)
                ? p.Windows
                : BoardPreferencesController.DefaultWindows;
            for (var i = 0; i < Board.States.Count; i++)
            {
                var box = BoardWindowBoxes[i];
                box.IsEnabled = enabled;
                var n = WindowOf(windows, Board.States[i]);
                if (boardWindowsTimer is null && (double.IsNaN(box.Value) || (int)box.Value != n))
                {
                    box.Value = n;
                }
                BoardWindowRows[i].Description = Board.Text.Days(DaysOf(box));
            }
        }
        finally
        {
            syncingBoard = false;
        }
    }

    private void OnBoardShowToggled(object sender, RoutedEventArgs e)
    {
        if (syncingBoard || boardClosed || BoardPrefs.Preferences is null)
        {
            return;
        }
        if (BoardShowSwitch.IsOn)
        {
            BoardPrefs.SetEnabled(true);
            return;
        }
        // Turned off, nothing runs on its own either: one write.
        BoardPrefs.Update(p => p with { Enabled = false, AutoTriage = false });
    }

    private void OnBoardWindowChanged(NumberBox sender, NumberBoxValueChangedEventArgs args)
    {
        var i = IndexOfBox(sender);
        if (i >= 0)
        {
            BoardWindowRows[i].Description = Board.Text.Days(DaysOf(sender));
        }
        if (syncingBoard || boardClosed)
        {
            return;
        }
        boardWindowsTimer?.Stop();
        var timer = DispatcherQueue.CreateTimer();
        timer.Interval = BoardWindowsDebounce;
        timer.IsRepeating = false;
        timer.Tick += (t, _) =>
        {
            t.Stop();
            if (ReferenceEquals(boardWindowsTimer, t))
            {
                boardWindowsTimer = null;
                WriteBoardWindows(final: false);
            }
        };
        boardWindowsTimer = timer;
        timer.Start();
    }

    // Tells the daemon what the rows show (preferences_board.go write);
    // final: the window is closing, and the last step still goes.
    private void WriteBoardWindows(bool final)
    {
        if (BoardPrefs.Preferences is not { } prefs || (boardClosed && !final))
        {
            return;
        }
        var w = BoardPreferencesController.ValidWindows(prefs.Windows) ? prefs.Windows : BoardPreferencesController.DefaultWindows;
        for (var i = 0; i < Board.States.Count; i++)
        {
            w = WithWindow(w, Board.States[i], DaysOf(BoardWindowBoxes[i]));
        }
        if (w == prefs.Windows || !BoardPrefs.SetWindows(w))
        {
            UpdateBoardGroup();
        }
    }

    private int IndexOfBox(NumberBox box)
    {
        var boxes = BoardWindowBoxes;
        for (var i = 0; i < boxes.Count; i++)
        {
            if (ReferenceEquals(boxes[i], box))
            {
                return i;
            }
        }
        return -1;
    }

    // A row's whole days within the daemon's range (an empty box is a day).
    private static int DaysOf(NumberBox box) =>
        double.IsNaN(box.Value) ? 1 : Math.Clamp((int)Math.Round(box.Value), 1, BoardLimits.MaxBoardWindowDays);

    private static int WindowOf(BoardWindows w, Board.State st) => st switch
    {
        Board.State.Hot => w.Hot,
        Board.State.You => w.You,
        Board.State.Them => w.Them,
        _ => w.Info,
    };

    private static BoardWindows WithWindow(BoardWindows w, Board.State st, int n) => st switch
    {
        Board.State.Hot => w with { Hot = n },
        Board.State.You => w with { You = n },
        Board.State.Them => w with { Them = n },
        _ => w with { Info = n },
    };
}
