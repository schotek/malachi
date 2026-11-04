// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/EventEditor/EventEditorPopovers.swift
// (EventEditorTimeZoneViewController: all, filter, controlTextDidChange,
// control(_:textView:doCommandBy:), rowClicked); GTK: the event editor
// follows (docs/calendar.md §6.4).
//
// Windows: the zones are the system's (TimeZoneInfo.GetSystemTimeZones,
// Windows ids) by their IANA names (Cal.IanaZoneName), what the API's zone
// names are; a zone with no IANA counterpart keeps its own id, and two
// Windows zones of one IANA name are listed once. The search is
// case-insensitive and a space stands for the names' underscore ("new
// york" finds America/New_York), as on macOS.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Calendars;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using WinUIKey = Windows.System.VirtualKey;

namespace Malachi.App.Calendars;

/// <summary>The time-zone link's flyout of the event editor.</summary>
public sealed partial class TimeZonePicker : UserControl
{
    // Every zone by its IANA name, in order (read once).
    private static readonly Lazy<IReadOnlyList<KeyValuePair<string, TimeZoneInfo>>> All = new(Load);

    private readonly string current;
    private List<KeyValuePair<string, TimeZoneInfo>> shown = [];

    /// <summary>The list with <paramref name="currentZone"/> (an IANA name) selected.</summary>
    public TimeZonePicker(string currentZone)
    {
        ArgumentNullException.ThrowIfNull(currentZone);
        current = currentZone;
        InitializeComponent();
        SearchBox.PlaceholderText = Cal.ZoneSearch;
        AutomationProperties.SetName(SearchBox, Cal.ZoneSearch);
        Filter("");
    }

    /// <summary>A zone was picked.</summary>
    public event EventHandler<TimeZoneInfo>? Picked;

    /// <summary>The keyboard to the search field (the flyout opened).</summary>
    public void FocusSearch() => SearchBox.Focus(FocusState.Programmatic);

    private static IReadOnlyList<KeyValuePair<string, TimeZoneInfo>> Load()
    {
        var byName = new SortedDictionary<string, TimeZoneInfo>(StringComparer.Ordinal);
        foreach (var z in TimeZoneInfo.GetSystemTimeZones())
        {
            byName.TryAdd(Cal.IanaZoneName(z), z);
        }
        return [.. byName];
    }

    // Case-insensitive; "new york" finds America/New_York.
    private void Filter(string text)
    {
        var q = text.Trim().Replace(' ', '_');
        shown = q.Length == 0
            ? [.. All.Value]
            : [.. All.Value.Where(z => z.Key.Contains(q, StringComparison.OrdinalIgnoreCase))];
        ZoneList.ItemsSource = shown.Select(z => z.Key.Replace('_', ' ')).ToList();
        var i = shown.FindIndex(z => string.Equals(z.Key, current, StringComparison.Ordinal));
        if (i >= 0)
        {
            ZoneList.SelectedIndex = i;
            ZoneList.ScrollIntoView(ZoneList.Items[i]);
        }
    }

    private void OnSearchChanged(object sender, TextChangedEventArgs e) => Filter(SearchBox.Text);

    // Enter in the field picks the first match.
    private void OnSearchKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key != WinUIKey.Enter || shown.Count == 0)
        {
            return;
        }
        e.Handled = true;
        Picked?.Invoke(this, shown[0].Value);
    }

    private void OnZoneClick(object sender, ItemClickEventArgs e)
    {
        var i = ZoneList.Items.IndexOf(e.ClickedItem);
        if (i >= 0 && i < shown.Count)
        {
            Picked?.Invoke(this, shown[i].Value);
        }
    }
}
