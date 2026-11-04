// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Calendar/CalendarAgendaViewController.swift
// (update(controller:), select, scrolled's paging, clicked, view(for:))
// and CalendarSearchController.swift (active, entries); GTK: the
// calendar's views follow (docs/calendar.md §6.4).
//
// The Agenda view, the Unanswered filter and the search results in one
// list. The Agenda and the filter are Core's agenda (a heading line per
// day, Cal.Agenda; the Agenda always starts at today when switched to,
// Cal.SwitchAnchor); the results are one list under "Results" in the
// daemon's order, no day groups (they are not one range of days), each
// row's day before its time (docs/calendar.md §5.4 As built). The lines
// are applied by key (KeyedListSync), so a next page keeps the scroll
// position, and the selection follows the controller's. Scrolling near
// the end of the Agenda asks for the next 30 days (LoadMoreAgenda); while
// the end is in view without scrolling the list asks on after every page
// that added lines, and stops at one that added none (Windows: macOS asks
// only when the user scrolls). A click on a line selects it and opens its
// detail; a heading takes no click and no selection.

using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using System.Globalization;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Windows.UI;
using Windows.UI.Text;

namespace Malachi.App.Calendars;

/// <summary>The Agenda view, the Unanswered filter and the search results (macOS <c>CalendarAgendaViewController</c>).</summary>
public sealed partial class CalendarAgendaView : UserControl, ICalendarView
{
    // How near the end a scroll asks for the next page (macOS: 200 pt).
    private const double PageMargin = 200;

    private readonly ObservableCollection<AgendaEntry> entries = [];
    private CalendarController? controller;
    private ScrollViewer? scroller;
    private bool syncing;
    private bool paging;
    private bool pageRequested;
    private int countAtRequest = -1;

    /// <summary>An empty list; <see cref="Update"/> fills it.</summary>
    public CalendarAgendaView()
    {
        InitializeComponent();
        List.ItemsSource = entries;
        List.ContainerContentChanging += OnContainerContentChanging;
        List.Loaded += (_, _) => FindScroller();
    }

    /// <summary>The dot's brush of a line (x:Bind).</summary>
    public static SolidColorBrush DotBrush(Color color) => new(color);

    /// <summary>A title struck through for a declined or cancelled occurrence (x:Bind).</summary>
    public static TextDecorations Decorations(bool struck) => struck ? TextDecorations.Strikethrough : TextDecorations.None;

    /// <inheritdoc/>
    public void Update(CalendarController controller)
    {
        ArgumentNullException.ThrowIfNull(controller);
        this.controller = controller;
        var env = controller.Env;
        var snapshot = controller.Snapshot;
        var next = controller.SearchResults is { } results
            ? SearchEntries(results, env, snapshot)
            : AgendaEntries(controller.Agenda ?? [], env, snapshot);
        paging = controller.View == Cal.View.Agenda && controller.Filter == CalendarController.ContentFilter.None
            && controller.SearchResults is null;
        HeadingText.Text = controller.ListHeading;
        HeadingText.Visibility = controller.ListHeading.Length == 0 ? Visibility.Collapsed : Visibility.Visible;
        EmptyText.Text = controller.EmptyListText;
        EmptyText.Visibility = controller.EmptyListText.Length == 0 || controller.Loading ? Visibility.Collapsed : Visibility.Visible;
        if (!entries.SequenceEqual(next))
        {
            syncing = true;
            try
            {
                KeyedListSync.Apply(entries, next, e => e.Key);
            }
            finally
            {
                syncing = false;
            }
            pageRequested = false;
        }
        else if (!controller.Loading)
        {
            pageRequested = false;
        }
        Select(controller.Selection);
        if (paging)
        {
            DispatcherQueue.TryEnqueue(DispatcherQueuePriority.Low, () => CheckEnd(byScroll: false));
        }
    }

    /// <inheritdoc/>
    public FrameworkElement? ElementFor(string key)
    {
        var i = IndexOf(key);
        return i < 0 ? null : List.ContainerFromIndex(i) as FrameworkElement;
    }

    /// <inheritdoc/>
    public bool FocusSelection()
    {
        var i = controller?.Selection is { } key ? IndexOf(key) : -1;
        if (i < 0)
        {
            // Nothing selected: the list itself (the arrows then select).
            return List.Focus(FocusState.Programmatic);
        }
        List.ScrollIntoView(entries[i]);
        return List.ContainerFromIndex(i) is Control c && c.Focus(FocusState.Keyboard);
    }

    /// <inheritdoc/>
    protected override AutomationPeer OnCreateAutomationPeer() => new CalendarRegionPeer(this, nameof(CalendarAgendaView));

    // The Agenda's and the Unanswered filter's lines: a heading per day,
    // then the day's occurrences.
    private static List<AgendaEntry> AgendaEntries(IReadOnlyList<Cal.AgendaDay> agenda, Cal.Env env, Cal.Snapshot snapshot)
    {
        var output = new List<AgendaEntry>();
        var seen = new HashSet<string>(StringComparer.Ordinal);
        foreach (var day in agenda)
        {
            var heading = AgendaEntry.DayHeading(day);
            if (!seen.Add(heading.Key))
            {
                continue;
            }
            output.Add(heading);
            var dayKey = day.Day.ToUnixTimeSeconds().ToString(CultureInfo.InvariantCulture);
            foreach (var it in day.Items)
            {
                var key = Unique(seen, Cal.OccurrenceKey(it.Occurrence) + "@" + dayKey);
                output.Add(AgendaEntry.Item(key, it, Cal.ItemTime(it, env), CalendarPalette.Of(it.Occurrence, snapshot), env));
            }
        }
        return output;
    }

    // The results: no day groups, each row's day before its time
    // ("Tomorrow · 10:00–10:30"), in the daemon's order.
    private static List<AgendaEntry> SearchEntries(IReadOnlyList<Occurrence> results, Cal.Env env, Cal.Snapshot snapshot)
    {
        var now = TimeProvider.System.GetUtcNow();
        var output = new List<AgendaEntry>();
        var seen = new HashSet<string>(StringComparer.Ordinal);
        foreach (var o in results)
        {
            if (o is null || Cal.Span(o, env.Zone) is not (var s, var e))
            {
                continue;
            }
            var item = new Cal.AgendaItem(o, s, e, 1, 1, Cal.StateOf(o));
            var day = Cal.DayHeading(Cal.StartOfDay(s, env.Zone), now, env);
            var key = Unique(seen, Cal.OccurrenceKey(o));
            output.Add(AgendaEntry.Item(key, item, Cal.Text.Dot(day, Cal.ItemTime(item, env)), CalendarPalette.Of(o, snapshot), env));
        }
        return output;
    }

    // A key not yet in the list (an occurrence the daemon lists twice).
    private static string Unique(HashSet<string> seen, string key)
    {
        var k = key;
        for (var n = 2; !seen.Add(k); n++)
        {
            k = key + "#" + n.ToString(CultureInfo.InvariantCulture);
        }
        return k;
    }

    private int IndexOf(string key)
    {
        for (var i = 0; i < entries.Count; i++)
        {
            if (entries[i].Occurrence is { } o && Cal.OccurrenceKey(o) == key)
            {
                return i;
            }
        }
        return -1;
    }

    // The list's selection is the controller's (by key, with this
    // view's handler quiet while it applies).
    private void Select(string? key)
    {
        var i = key is null ? -1 : IndexOf(key);
        var item = i < 0 ? null : entries[i];
        if (ReferenceEquals(List.SelectedItem, item))
        {
            return;
        }
        syncing = true;
        try
        {
            List.SelectedItem = item;
        }
        finally
        {
            syncing = false;
        }
    }

    private void OnSelectionChanged(object sender, SelectionChangedEventArgs e)
    {
        if (syncing || controller is not { } c)
        {
            return;
        }
        if (List.SelectedItem is AgendaEntry { Occurrence: { } o })
        {
            c.Select(Cal.OccurrenceKey(o));
            return;
        }
        // A heading takes no selection.
        Select(c.Selection);
    }

    private void OnItemClick(object sender, ItemClickEventArgs e)
    {
        if (e.ClickedItem is AgendaEntry { Occurrence: { } o })
        {
            controller?.OpenDetail(o);
        }
    }

    // A line's name and AutomationId; a heading is no stop and no target.
    private static void OnContainerContentChanging(ListViewBase sender, ContainerContentChangingEventArgs args)
    {
        if (args.Item is not AgendaEntry entry || args.ItemContainer is not { } container)
        {
            return;
        }
        AutomationProperties.SetName(container, entry.Label);
        AutomationProperties.SetAutomationId(
            container, entry.Occurrence is { } o ? "CalendarAgendaRow:" + Cal.OccurrenceKey(o) : "CalendarAgendaDay");
        AutomationProperties.SetHeadingLevel(container, entry.IsHeading ? AutomationHeadingLevel.Level2 : AutomationHeadingLevel.None);
        container.IsHitTestVisible = !entry.IsHeading;
        container.IsTabStop = !entry.IsHeading;
    }

    // The list's own scroll viewer, once its template is applied.
    private void FindScroller()
    {
        if (scroller is not null)
        {
            return;
        }
        scroller = Descendant<ScrollViewer>(List);
        if (scroller is not null)
        {
            scroller.ViewChanged += (_, _) => CheckEnd(byScroll: true);
        }
    }

    // The end of the Agenda in view: its next page. Without a scroll only
    // after a page that added lines, so an empty period ends the asking.
    private void CheckEnd(bool byScroll)
    {
        FindScroller();
        if (!paging || pageRequested || controller is null || scroller is null || entries.Count == 0)
        {
            return;
        }
        if (scroller.VerticalOffset + scroller.ViewportHeight < scroller.ExtentHeight - PageMargin)
        {
            return;
        }
        if (!byScroll && entries.Count == countAtRequest)
        {
            return;
        }
        pageRequested = true;
        countAtRequest = entries.Count;
        controller.LoadMoreAgenda();
    }

    private static T? Descendant<T>(DependencyObject root)
        where T : DependencyObject
    {
        for (var i = 0; i < VisualTreeHelper.GetChildrenCount(root); i++)
        {
            var child = VisualTreeHelper.GetChild(root, i);
            if (child is T found)
            {
                return found;
            }
            if (Descendant<T>(child) is { } deeper)
            {
                return deeper;
            }
        }
        return null;
    }
}
