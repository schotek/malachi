// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the table of macos/Sources/MalachiMail/Compose/
// RecipientSuggestionsController.swift (reloadData, selectRowIndexes,
// scrollRowToVisible, rowClicked); GTK: ui/internal/compose/suggest.go
// (show's list, move's SelectRow, the list's row-activated). The content
// of the recipient popup; RecipientSuggestions drives it.

using System;
using System.Collections.Generic;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Compose;

/// <summary>The rows of the recipient popup.</summary>
public sealed partial class SuggestionList : UserControl
{
    /// <summary>An empty list.</summary>
    public SuggestionList()
    {
        InitializeComponent();
        // A row reads as its name and address (the record would read as its
        // type).
        Rows.ContainerContentChanging += (_, args) =>
        {
            if (args.Item is SuggestionRow row)
            {
                // Windows-only string: the name and the address, joined for Narrator.
                AutomationProperties.SetName(args.ItemContainer, row.Secondary.Length > 0 ? row.Primary + ", " + row.Secondary : row.Primary);
            }
        };
    }

    /// <summary>A row was clicked, with its index (rowClicked, row-activated).</summary>
    public event EventHandler<int>? RowClicked;

    /// <summary>Shows <paramref name="rows"/> with <paramref name="selected"/> selected and in view.</summary>
    public void Show(IReadOnlyList<SuggestionRow> rows, int selected)
    {
        ArgumentNullException.ThrowIfNull(rows);
        if (!ReferenceEquals(Rows.ItemsSource, rows))
        {
            Rows.ItemsSource = rows;
        }
        Select(selected);
    }

    /// <summary>Selects <paramref name="index"/> and scrolls it into view.</summary>
    public void Select(int index)
    {
        Rows.SelectedIndex = index;
        if (index >= 0 && index < Rows.Items.Count)
        {
            Rows.ScrollIntoView(Rows.Items[index]);
        }
    }

    private void OnItemClick(object sender, ItemClickEventArgs e)
    {
        var index = Rows.Items.IndexOf(e.ClickedItem);
        if (index >= 0)
        {
            RowClicked?.Invoke(this, index);
        }
    }
}
