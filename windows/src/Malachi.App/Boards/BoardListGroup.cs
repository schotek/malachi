// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the section items of macos/Sources/MalachiMail/Board/BoardListViewController.swift
// (Item.header, Item.commitmentsHeader); GTK: boardSectionHeader in
// window/board_list.go. A group of the List style's ListView: its header
// (a section's title and count, or "From the Assistant" over the
// commitments) and its items (BoardRowItem, BoardCommitmentItem). The
// ListView's grouping keeps the headers out of the selection and of the
// arrow keys, as AppKit's shouldSelectRow and GTK's unselectable rows do.

using System.Collections.Generic;
using Malachi.Core.Boards;

namespace Malachi.App.Boards;

/// <summary>A section of the board's list, or its commitments, with the header's texts.</summary>
public sealed class BoardListGroup : List<object>
{
    /// <summary>A group of <paramref name="items"/> under <paramref name="title"/>.</summary>
    public BoardListGroup(BoardListGroupKind kind, Board.State state, string title, string spoken, IEnumerable<object> items)
        : base(items)
    {
        Kind = kind;
        State = state;
        Title = title;
        Spoken = spoken;
    }

    /// <summary>What the group holds, for the header's colour.</summary>
    public BoardListGroupKind Kind { get; }

    /// <summary>The state of a state's section.</summary>
    public Board.State State { get; }

    /// <summary>The header's title.</summary>
    public string Title { get; }

    /// <summary>The header's count.</summary>
    public string CountText => Count.ToString(System.Globalization.CultureInfo.CurrentCulture);

    /// <summary>What the screen reader says for the header ("Hot, 3 cases").</summary>
    public string Spoken { get; }
}
