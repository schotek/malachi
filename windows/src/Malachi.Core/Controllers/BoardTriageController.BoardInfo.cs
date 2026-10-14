// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/BoardTriageController.swift
// (BoardTriageController.BoardInfo); GTK: ui/internal/boardtriage/
// controller.go (BoardInfo).

using System;
using Malachi.Core.Api;
using Malachi.Core.Boards;

namespace Malachi.Core.Controllers;

public sealed partial class BoardTriageController
{
    /// <summary>What the board reports about the triage.</summary>
    public sealed record BoardInfo
    {
        /// <summary>A board.list has arrived.</summary>
        public bool Known { get; init; }

        /// <summary>The board's assistant preference as board.list reported it.</summary>
        public bool AssistantOn { get; init; }

        /// <summary>Cases waiting for the assistant (0 while it is off).</summary>
        public int Queue { get; init; }

        /// <summary>Cases automatic runs annotated today (as of <see cref="CountedAt"/>).</summary>
        public int AnnotatedToday { get; init; }

        /// <summary>When <see cref="AnnotatedToday"/> was reported; null before.</summary>
        public DateTimeOffset? CountedAt { get; init; }

        /// <summary>The daemon's last run.</summary>
        public Board.Run? LastRun { get; init; }

        /// <summary>The tokens triage runs used in the last 24 hours; null when none reported any.</summary>
        public BoardUsageTotal? Usage24h { get; init; }
    }
}
