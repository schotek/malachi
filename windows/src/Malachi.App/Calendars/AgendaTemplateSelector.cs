// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: the agenda's ListView holds two kinds of lines, as
// macos/Sources/MalachiMail/Calendar/CalendarAgendaViewController.swift's
// table view(for:row:) picks AgendaGroupCell or AgendaItemCell.

using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Calendars;

/// <summary>Picks a day heading's or an occurrence's template.</summary>
public sealed partial class AgendaTemplateSelector : DataTemplateSelector
{
    /// <summary>The template of a day's heading.</summary>
    public DataTemplate? HeadingTemplate { get; set; }

    /// <summary>The template of an occurrence's line.</summary>
    public DataTemplate? ItemTemplate { get; set; }

    /// <inheritdoc/>
    protected override DataTemplate? SelectTemplateCore(object item) =>
        item is AgendaEntry { IsHeading: true } ? HeadingTemplate : ItemTemplate;

    /// <inheritdoc/>
    protected override DataTemplate? SelectTemplateCore(object item, DependencyObject container) => SelectTemplateCore(item);
}
