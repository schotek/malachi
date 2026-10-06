// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the board's part of macos/Sources/MalachiMail/App/AppState.swift
// (boardPreferences, made once for the application and loaded when the
// daemon is there); GTK: ui/internal/boardtriage (the preferences the
// triage reads). The board's own preferences in the daemon (board.preferences:
// the assistant, the automatic triage, the windows), asked for again after
// every reconnection. The triage's port (stage 5) adds the triage, its
// scheduler and the suggested reply here; the board controller belongs to
// the main window (Integration.Board.cs).

using Malachi.Core.Boards;
using Malachi.Core.Controllers;
using Microsoft.Extensions.Logging;

namespace Malachi.App.Shell;

/// <summary>The board's application-wide state.</summary>
public sealed partial class AppState
{
    private System.IDisposable? boardConnection;

    /// <summary>The board's preferences in the daemon (board.preferences).</summary>
    public BoardPreferencesController BoardPreferences { get; private set; } = null!;

    /// <summary>
    /// Whether the user looks at the selected folder (Board.ViewsMail): the
    /// main window is active and shows the mail. An active window showing the
    /// board does not count, so the folder's desktop notifications stay.
    /// </summary>
    public bool MainWindowViewsMail => MainWindow is { } w && Board.ViewsMail(w.Mode, IsMainWindowActive);

    // The preferences, loaded as soon as the daemon is there and again
    // after every reconnection (BoardPreferencesController.ConnectionChanged).
    private void InitializeBoard()
    {
        BoardPreferences = new BoardPreferencesController(Client, Logs.CreateLogger<BoardPreferencesController>());
        boardConnection = Notifications.AddConnectionState(s =>
            BoardPreferences.ConnectionChanged(s is ConnectionState.Connected or ConnectionState.InfoFailed));
        if (Notifications.ConnectionState is ConnectionState.Connected)
        {
            BoardPreferences.Load();
        }
    }

    private void CloseBoard()
    {
        boardConnection?.Dispose();
        boardConnection = null;
        BoardPreferences.Dispose();
    }
}
