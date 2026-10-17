// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/CalendarAccountGroups.swift
// (prefsCalendarRow); GTK: none yet (Swift-first, docs/calendar.md §5.8).
// A calendar of a list in Preferences → Calendar and in the Add CalDAV
// Calendar window: a card whose header is the check box, labelled with the
// colour square and the name (a click on the name toggles it, and a screen
// reader reads the name), and whose end is the note ("default", "read
// only", "shared · %s"). The name and the note are the server's plain text:
// TextBlock.Text only, cleaned for display. A row is updated in place while
// its list keeps its calendars, so the keyboard stays on its check box.

using System;
using CommunityToolkit.WinUI.Controls;
using Malachi.Core.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Preferences;

/// <summary>A calendar's card with its check box.</summary>
internal sealed class CalendarRowView
{
    private readonly Border swatch;
    private readonly TextBlock name;
    private readonly TextBlock note;

    /// <summary>
    /// A row; <paramref name="resources"/> holds the swatch's style,
    /// <paramref name="automationId"/> names the check box for UI Automation.
    /// </summary>
    public CalendarRowView(FrameworkElement resources, string automationId)
    {
        ArgumentNullException.ThrowIfNull(resources);
        swatch = CalendarSwatch.Make(resources, null);
        name = new TextBlock
        {
            VerticalAlignment = VerticalAlignment.Center,
            TextTrimming = TextTrimming.CharacterEllipsis,
            TextWrapping = TextWrapping.NoWrap,
            MaxLines = 1,
        };
        var label = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8 };
        label.Children.Add(swatch);
        label.Children.Add(name);
        Box = new CheckBox { Content = label, MinWidth = 0 };
        AutomationProperties.SetAutomationId(Box, automationId);
        note = new TextBlock
        {
            VerticalAlignment = VerticalAlignment.Center,
            TextTrimming = TextTrimming.CharacterEllipsis,
            MaxLines = 1,
        };
        if (Application.Current.Resources.TryGetValue("DimCaptionLabelStyle", out var dim) && dim is Style d)
        {
            note.Style = d;
        }
        Card = new SettingsCard { Header = Box, Content = note };
    }

    /// <summary>The card to put in the list.</summary>
    public SettingsCard Card { get; }

    /// <summary>The check box.</summary>
    public CheckBox Box { get; }

    /// <summary>Shows <paramref name="calendarName"/>, its <paramref name="color"/> and its <paramref name="calendarNote"/> ("" none).</summary>
    public void Update(string calendarName, string color, string calendarNote)
    {
        var shown = DisplayText.Clean(calendarName);
        name.Text = shown;
        AutomationProperties.SetName(Box, shown);
        CalendarSwatch.Paint(swatch, color);
        var n = DisplayText.Clean(calendarNote);
        note.Text = n;
        note.Visibility = n.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        AutomationProperties.SetHelpText(Box, n);
    }
}
