// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Board/BoardListViewController.swift
// (Item.commitment): a commitment under "From the Assistant" in the List
// style's ListView (Overview only, ViewModel.ShowsCommitmentsInList); the
// template is BoardCommitmentView.

using Malachi.Core.Boards;

namespace Malachi.App.Boards;

/// <summary>A commitment row of the board's list.</summary>
/// <param name="Commitment">The view model's commitment (its strings cleaned by Core).</param>
public sealed record BoardCommitmentItem(Board.CommitmentRow Commitment);
