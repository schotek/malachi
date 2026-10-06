// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the Item enums of macos/Sources/MalachiMail/Board/BoardColumnsViewController.swift
// (placeholder, assistantHeading) and BoardTodayViewController.swift
// (section, text, more, commitmentsHeading); GTK: columnItem
// (window/board_columns.go) and todayItem (window/board_today.go). The rows
// of the Columns style's and the Today page's ListViews besides the cases
// (BoardRowItem) and the commitments (BoardCommitmentItem). Records, so a
// render compares them and replaces only the rows that changed.

using Malachi.Core.Api;
using Malachi.Core.Boards;

namespace Malachi.App.Boards;

/// <summary>An empty column's dashed card ("Nothing burning.", "Empty."); never selected.</summary>
/// <param name="Text">The view model's text.</param>
public sealed record BoardPlaceholderItem(string Text);

/// <summary>"✦ From the Assistant" over the commitments; never selected.</summary>
/// <param name="Title">The heading (<see cref="Board.Text.FromAssistant"/>).</param>
/// <param name="Count">The commitments under it.</param>
public sealed record BoardHeadingItem(string Title, int Count)
{
    /// <summary>The count beside it.</summary>
    public string CountText => Count.ToString(System.Globalization.CultureInfo.CurrentCulture);
}

/// <summary>A state's section on the Today page (Hot, Waiting for You); never selected.</summary>
/// <param name="State">The state.</param>
/// <param name="Title">Its name.</param>
/// <param name="Count">The cases in it, also those beyond the page's top.</param>
public sealed record BoardTodaySectionItem(Board.State State, string Title, int Count);

/// <summary>An empty section's text on the Today page; never selected.</summary>
/// <param name="Text">The view model's text.</param>
public sealed record BoardTodayTextItem(string Text);

/// <summary>"and N more" under the top of what waits for the user: Return or a click shows them all.</summary>
/// <param name="Count">The cases beyond the top.</param>
public sealed record BoardTodayMoreItem(int Count)
{
    /// <summary>The row's text.</summary>
    public string Text => Board.Text.AndMore(Count);
}

/// <summary>The case an item of the board's columns or Today page selects.</summary>
internal static class BoardItems
{
    /// <summary>The case of a card, a row or a commitment; null for a row that is not selectable.</summary>
    public static BoardCaseId? CaseOf(object? item) => item switch
    {
        BoardRowItem r => r.Row.Id,
        BoardCommitmentItem c => c.Commitment.CaseId,
        _ => (BoardCaseId?)null,
    };
}
