// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the section items of macos/Sources/MalachiMail/Board/BoardListViewController.swift
// (Item.header, Item.commitmentsHeader); GTK: boardSectionHeader in
// window/board_list.go. A group of the List style's ListView: its header
// (a section's title and count, or "From the Assistant" over the
// commitments) and its items (BoardRowItem, BoardCommitmentItem). The
// ListView's grouping keeps the headers out of the selection and of the
// arrow keys, as AppKit's shouldSelectRow and GTK's unselectable rows do.
//
// The group stays the same object while its section is shown (Key): the
// list updates its items in place (Sync) and its header (Update), so a
// change replaces only the rows that changed, as GTK's renderList keyed by
// case id, and the selected row keeps its container and the keyboard.

using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using System.ComponentModel;
using Malachi.Core.Boards;

namespace Malachi.App.Boards;

/// <summary>A section of the board's list, or its commitments.</summary>
public sealed class BoardListGroup : ObservableCollection<object>
{
    /// <summary>An empty group under <paramref name="title"/>, known by <paramref name="key"/>.</summary>
    public BoardListGroup(string key, BoardListGroupKind kind, Board.State state, string title, string spoken)
    {
        Key = key;
        Kind = kind;
        State = state;
        Title = title;
        Spoken = spoken;
    }

    /// <summary>Which section it is, kept across updates ("state:Hot", "Snoozed:Hot", "commitments").</summary>
    public string Key { get; }

    /// <summary>What the group holds, for the header's colour.</summary>
    public BoardListGroupKind Kind { get; private set; }

    /// <summary>The state of a state's section.</summary>
    public Board.State State { get; private set; }

    /// <summary>The header's title.</summary>
    public string Title { get; private set; }

    /// <summary>The header's count.</summary>
    public string CountText => Count.ToString(System.Globalization.CultureInfo.CurrentCulture);

    /// <summary>What the screen reader says for the header ("Hot: 3 cases").</summary>
    public string Spoken { get; private set; }

    /// <summary>The header's texts anew; the header hears of it (PropertyChanged) when they differ.</summary>
    public void Update(BoardListGroupKind kind, Board.State state, string title, string spoken)
    {
        if (kind == Kind && state == State && title == Title && spoken == Spoken)
        {
            return;
        }
        Kind = kind;
        State = state;
        Title = title;
        Spoken = spoken;
        OnPropertyChanged(new PropertyChangedEventArgs(nameof(Title)));
    }

    /// <summary>
    /// Makes the items <paramref name="next"/> in place: an item of the same
    /// key (<paramref name="keyOf"/>) is moved rather than removed and added,
    /// and replaced only when it differs; the rest are inserted or removed.
    /// </summary>
    public void Sync(IReadOnlyList<object> next, Func<object, string> keyOf)
    {
        ArgumentNullException.ThrowIfNull(next);
        ArgumentNullException.ThrowIfNull(keyOf);
        for (var i = 0; i < next.Count; i++)
        {
            var key = keyOf(next[i]);
            var at = -1;
            for (var j = i; j < Count; j++)
            {
                if (keyOf(this[j]) == key)
                {
                    at = j;
                    break;
                }
            }
            if (at < 0)
            {
                Insert(i, next[i]);
                continue;
            }
            if (at != i)
            {
                // A remove and an insert, not Move: the grouped
                // CollectionViewSource over these groups is not trusted to
                // follow a Move (the list view restores focus and selection
                // after the rebuild).
                RemoveAt(at);
                Insert(i, next[i]);
                continue;
            }
            if (!Equals(this[i], next[i]))
            {
                this[i] = next[i];
            }
        }
        while (Count > next.Count)
        {
            RemoveAt(Count - 1);
        }
    }
}
