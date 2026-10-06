// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the source's part of macos/Sources/MalachiMail/MainWindow/
// MainWindowController+Triage.swift (wireTriage: the snapshots for the
// triage, a list again after a run; startBoardForAutoTriage); GTK:
// window/board_triage.go (BoardChanged, SetOnRefresh) and
// board_auto_start.go (watchBoardAutoStart, boardAutoStart).
//
// What the application's triage needs of the main window's board source:
// every snapshot of the daemon's board (its queue, counts, last run and
// usage of the last 24 hours, which the schedule and the strip read), and
// the board listed again after a run's board.runEnd and whenever
// Preferences → AI comes up. While the triage wants the board's data
// (automatic triage on, consented to and able to run) the daemon's board
// source starts at once, without a first entry into Board: the schedule
// learns the queue only from its snapshots. The start waits for the end of
// the notification that asked for it (wiring the source can publish
// another change), and the samples (MALACHI_BOARD_SAMPLES) never reach the
// triage.

using Malachi.Core.Boards;

namespace Malachi.App.Shell;

/// <summary>The board's triage of the Integration.</summary>
public sealed partial class Integration
{
    private BoardObserverToken? triageToken;
    private bool triageStartQueued;

    // After WireBoard: the triage reads the daemon's snapshots and lists the
    // board again through the source.
    private void WireTriage()
    {
        if (boardDaemonSource is not { } source)
        {
            return;
        }
        var triage = state.BoardTriage;
        source.OnSnapshot = triage.BoardChanged;
        triage.RefreshRequested += OnTriageRefreshRequested;
        triageToken = triage.Observe(StartBoardForTriage);
        StartBoardForTriage();
    }

    private void OnTriageRefreshRequested(object? sender, System.EventArgs e) => StartedBoardSource?.Refresh();

    // The triage wants the board's data: the daemon's board is listed from
    // now on, as after an entry into Board.
    private void StartBoardForTriage()
    {
        if (triageStartQueued || boardStarted || !state.BoardTriage.WantsBoardData)
        {
            return;
        }
        triageStartQueued = true;
        if (!state.Dispatcher.TryEnqueue(() =>
        {
            triageStartQueued = false;
            if (!state.IsStopping && state.BoardTriage.WantsBoardData)
            {
                StartBoard();
            }
        }))
        {
            triageStartQueued = false;
        }
    }

    private void UnwireTriage()
    {
        triageToken?.Cancel();
        triageToken = null;
        if (boardDaemonSource is { } source)
        {
            source.OnSnapshot = null;
        }
        state.BoardTriage.RefreshRequested -= OnTriageRefreshRequested;
    }
}
