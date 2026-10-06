// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Board/BoardViews.swift
// (BoardSectionHeaderView.configure(column), configure(title:count:color:));
// GTK: newPane's header (window/board_columns.go) and
// boardTodaySectionHeader (window/board_today.go). The heading of a column
// and of a state's section on the Today page.

using System.Globalization;
using Malachi.Core.Boards;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Boards;

/// <summary>A state's heading: its dot, its title in its colour, its count.</summary>
public sealed partial class BoardStateHeading : UserControl
{
    /// <summary>The section shown.</summary>
    public static readonly DependencyProperty SectionProperty = DependencyProperty.Register(
        nameof(Section), typeof(BoardTodaySectionItem), typeof(BoardStateHeading), new PropertyMetadata(null, OnSectionChanged));

    /// <summary>An empty heading.</summary>
    public BoardStateHeading()
    {
        InitializeComponent();
    }

    /// <summary>The Today page's section shown (its template binds it).</summary>
    public BoardTodaySectionItem? Section
    {
        get => (BoardTodaySectionItem?)GetValue(SectionProperty);
        set => SetValue(SectionProperty, value);
    }

    /// <summary>
    /// Shows <paramref name="title"/> of <paramref name="state"/> with
    /// <paramref name="count"/> cases; a count of zero is hidden unless
    /// <paramref name="showsZero"/> (the Today page always counts, as GTK).
    /// </summary>
    public void Show(Board.State state, string title, int count, bool showsZero)
    {
        Dot.State = state;
        TitleText.Text = title;
        CountText.Text = count.ToString(CultureInfo.CurrentCulture);
        CountText.Visibility = count > 0 || showsZero ? Visibility.Visible : Visibility.Collapsed;
        AutomationProperties.SetName(this, Spoken(title, Board.Text.CaseCount(count)));
        VisualStateManager.GoToState(this, state.ToString(), false);
    }

    // A heading's name for the screen reader: "Hot, 3 cases", as the List's.
    // Windows-only string: the comma between a title and its spoken count.
    internal static string Spoken(string title, string count) => title + ", " + count;

    private static void OnSectionChanged(DependencyObject d, DependencyPropertyChangedEventArgs e)
    {
        if (e.NewValue is BoardTodaySectionItem s)
        {
            ((BoardStateHeading)d).Show(s.State, s.Title, s.Count, showsZero: true);
        }
    }
}
