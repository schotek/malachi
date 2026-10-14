// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the header kinds of macos/Sources/MalachiMail/Board/BoardListViewController.swift
// (Item.header with BoardPalette.accent(SectionKind), Item.commitmentsHeader).

namespace Malachi.App.Boards;

/// <summary>What a group of the board's list holds: its header's colour follows.</summary>
public enum BoardListGroupKind
{
    /// <summary>The live cases of a state: the state's colour.</summary>
    State,

    /// <summary>The snoozed or the done cases: grey.</summary>
    Plain,

    /// <summary>"From the Assistant": the commitments, in the assistant's colour.</summary>
    Commitments,
}
