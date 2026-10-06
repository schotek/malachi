// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the board's part of macos/Sources/MalachiCore/API/BoardAPI.swift
// (extension API.Limits); Go: backend/pkg/api/board.go (the MaxBoard*,
// MinBoard* and DefaultBoard* constants). A class of its own beside
// API.Limits, which follows types.go (Board.cs says why).

namespace Malachi.Core.Api;

/// <summary>The limits and defaults of the board the daemon enforces (api.MaxBoard*, DefaultBoard*).</summary>
public static class BoardLimits
{
    /// <summary><c>board.list</c>: the most cases.</summary>
    public const int MaxBoardCases = 1000;

    /// <summary><c>board.get</c>: the newest members.</summary>
    public const int MaxBoardMessages = 50;

    /// <summary><see cref="BoardMessage.Text"/>, in bytes.</summary>
    public const int MaxBoardMessageTextBytes = 8000;

    /// <summary><see cref="BoardDraft.Text"/>, in bytes.</summary>
    public const int MaxBoardDraftTextBytes = 4000;

    /// <summary><c>board.queue</c>'s default limit.</summary>
    public const int DefaultBoardQueueLimit = 3;

    /// <summary><c>board.queue</c>'s largest limit.</summary>
    public const int MaxBoardQueueLimit = 5;

    /// <summary>An annotation's title, in bytes.</summary>
    public const int MaxBoardTitleBytes = 300;

    /// <summary>An annotation's why, in bytes.</summary>
    public const int MaxBoardWhyBytes = 400;

    /// <summary>An annotation's summary, in bytes.</summary>
    public const int MaxBoardSummaryBytes = 2000;

    /// <summary>An annotation's tasks.</summary>
    public const int MaxBoardTasks = 10;

    /// <summary>One task, in bytes.</summary>
    public const int MaxBoardTaskBytes = 300;

    /// <summary>A commitment's text, in bytes.</summary>
    public const int MaxBoardCommitmentTextBytes = 300;

    /// <summary>A source, in bytes.</summary>
    public const int MaxBoardSourceBytes = 64;

    /// <summary>A quote, at least, in bytes.</summary>
    public const int MinBoardQuoteBytes = 10;

    /// <summary>A quote, at most, in bytes.</summary>
    public const int MaxBoardQuoteBytes = 300;

    /// <summary><see cref="BoardWindows"/>: 1 to this many days.</summary>
    public const int MaxBoardWindowDays = 365;

    /// <summary><see cref="BoardUsage"/>: each counter 0 to this; the daemon stores a larger one as this.</summary>
    public const long MaxBoardUsageTokens = 1_000_000_000_000;

    /// <summary><c>board.remind</c>: <c>until</c> at most now plus this many days.</summary>
    public const int MaxBoardRemindDays = 365;

    /// <summary>The default window of hot cases, in days.</summary>
    public const int DefaultBoardHotDays = 90;

    /// <summary>The default window of cases waiting for the user, in days.</summary>
    public const int DefaultBoardYouDays = 30;

    /// <summary>The default window of cases waiting for others, in days.</summary>
    public const int DefaultBoardThemDays = 30;

    /// <summary>The default window of cases for reading, in days.</summary>
    public const int DefaultBoardInfoDays = 14;

    /// <summary><see cref="BoardPreferences.AutoTriageMinutes"/>, at least.</summary>
    public const int MinBoardAutoTriageMinutes = 5;

    /// <summary><see cref="BoardPreferences.AutoTriageMinutes"/>, at most.</summary>
    public const int MaxBoardAutoTriageMinutes = 1440;

    /// <summary><see cref="BoardPreferences.AutoTriageMinutes"/> by default.</summary>
    public const int DefaultBoardAutoTriageMinutes = 30;

    /// <summary><see cref="BoardPreferences.AutoTriageDailyCases"/>, at most.</summary>
    public const int MaxBoardAutoTriageDailyCases = 1000;

    /// <summary><see cref="BoardPreferences.AutoTriageDailyCases"/> by default.</summary>
    public const int DefaultBoardAutoTriageDailyCases = 60;
}
