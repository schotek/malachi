// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: the Columns style's and the Today page's ListViews hold
// several kinds of rows, as the tables of
// macos/Sources/MalachiMail/Board/BoardColumnsViewController.swift and
// BoardTodayViewController.swift pick a view per Item.

using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Boards;

/// <summary>Picks the template of a row of the board's columns or Today page.</summary>
public sealed partial class BoardItemTemplateSelector : DataTemplateSelector
{
    /// <summary>A case (<see cref="BoardRowItem"/>).</summary>
    public DataTemplate? RowTemplate { get; set; }

    /// <summary>A commitment (<see cref="BoardCommitmentItem"/>).</summary>
    public DataTemplate? CommitmentTemplate { get; set; }

    /// <summary>An empty column's card (<see cref="BoardPlaceholderItem"/>).</summary>
    public DataTemplate? PlaceholderTemplate { get; set; }

    /// <summary>"From the Assistant" (<see cref="BoardHeadingItem"/>).</summary>
    public DataTemplate? HeadingTemplate { get; set; }

    /// <summary>A state's section of the Today page (<see cref="BoardTodaySectionItem"/>).</summary>
    public DataTemplate? SectionTemplate { get; set; }

    /// <summary>An empty section's text (<see cref="BoardTodayTextItem"/>).</summary>
    public DataTemplate? TextTemplate { get; set; }

    /// <summary>"and N more" (<see cref="BoardTodayMoreItem"/>).</summary>
    public DataTemplate? MoreTemplate { get; set; }

    /// <inheritdoc/>
    protected override DataTemplate? SelectTemplateCore(object item) => item switch
    {
        BoardRowItem => RowTemplate,
        BoardCommitmentItem => CommitmentTemplate,
        BoardPlaceholderItem => PlaceholderTemplate,
        BoardHeadingItem => HeadingTemplate,
        BoardTodaySectionItem => SectionTemplate,
        BoardTodayTextItem => TextTemplate,
        BoardTodayMoreItem => MoreTemplate,
        _ => null,
    };

    /// <inheritdoc/>
    protected override DataTemplate? SelectTemplateCore(object item, DependencyObject container) => SelectTemplateCore(item);
}
