// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/plans/toolbar-customization.md T13, §17.2): one
// line of the toolbar editor's list, a section's heading or one of its rows
// (ToolbarEditor.Row), with what the template shows. The list is flat, so
// that its own drag and drop and the keys move a row across a heading
// (ToolbarEditorView maps a position back to the layout's index).

using Malachi.Core.Toolbars;
using Microsoft.UI.Xaml;

namespace Malachi.App.Toolbars;

/// <summary>A heading or a row of the toolbar editor.</summary>
public sealed class ToolbarEditorEntry
{
    /// <summary>A section's heading; <paramref name="index"/> is its separator's index in the layout, -1 for the first section.</summary>
    internal ToolbarEditorEntry(Toolbar.Section section, string title, int index)
    {
        IsHeader = true;
        Section = section;
        Title = title;
        Index = index;
        Id = "";
        Subtitle = "";
    }

    /// <summary>A row of <paramref name="section"/>.</summary>
    internal ToolbarEditorEntry(Toolbar.Section section, ToolbarEditor.Row row, string title, string subtitle)
    {
        Section = section;
        Index = row.Index;
        Id = row.Id;
        Title = title;
        Subtitle = subtitle;
        IsFixed = row.Fixed;
    }

    /// <summary>Whether this is a section's heading.</summary>
    public bool IsHeader { get; }

    /// <summary>The section it heads or stands in.</summary>
    public Toolbar.Section Section { get; }

    /// <summary>The row's index in the layout (the heading's separator, -1 for the first section).</summary>
    public int Index { get; }

    /// <summary>The item's id, "" for a heading.</summary>
    public string Id { get; }

    /// <summary>The heading or the item's label (Toolbar.SectionTitle, Toolbar.ItemLabel).</summary>
    public string Title { get; }

    /// <summary>"Hidden while unavailable" for an item whose feature is missing, else "".</summary>
    public string Subtitle { get; }

    /// <summary>Whether the row can neither move nor go (the mode switch).</summary>
    public bool IsFixed { get; }

    /// <summary>The heading's text shows.</summary>
    public Visibility HeaderVisibility => IsHeader ? Visibility.Visible : Visibility.Collapsed;

    /// <summary>The row's parts show.</summary>
    public Visibility RowVisibility => IsHeader ? Visibility.Collapsed : Visibility.Visible;

    /// <summary>The drag handle shows (a row that moves).</summary>
    public Visibility HandleVisibility => IsHeader || IsFixed ? Visibility.Collapsed : Visibility.Visible;

    /// <summary>The subtitle shows.</summary>
    public Visibility SubtitleVisibility => Subtitle.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
}
