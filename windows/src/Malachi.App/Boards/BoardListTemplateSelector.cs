// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: the List style's ListView holds two kinds of items, as
// macos/Sources/MalachiMail/Board/BoardListViewController.swift's table
// view(for:row:) picks BoardCaseContentView or BoardCommitmentContentView.

using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Boards;

/// <summary>Picks a case row's or a commitment's template.</summary>
public sealed partial class BoardListTemplateSelector : DataTemplateSelector
{
    /// <summary>The template of a <see cref="BoardRowItem"/>.</summary>
    public DataTemplate? RowTemplate { get; set; }

    /// <summary>The template of a <see cref="BoardCommitmentItem"/>.</summary>
    public DataTemplate? CommitmentTemplate { get; set; }

    /// <inheritdoc/>
    protected override DataTemplate? SelectTemplateCore(object item) =>
        item is BoardCommitmentItem ? CommitmentTemplate : RowTemplate;

    /// <inheritdoc/>
    protected override DataTemplate? SelectTemplateCore(object item, DependencyObject container) => SelectTemplateCore(item);
}
