// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Calendar/CalendarPageViewController.swift
// (CalendarPageViewController: fold(width:), toggleSidebar,
// showAssistant, hideAssistant, releaseAssistant, assistantShown;
// CalendarContentViewController: update(controller:view:), view(for:));
// GTK: the calendar's page follows (docs/calendar.md §6.4).
//
// The page's content. What shows (macOS update(controller:view:)): the
// agenda list for the search results and the Unanswered filter in place of
// any view, else nothing while the page's empty state shows (Core's
// EmptyKind; the empty state lies in EmptyHost), else the controller's
// view (Week and Day share CalendarWeekView). The sidebar is 240 wide
// (Windows: not dragged; macOS's split view lets it go 200 to 320). Below
// 360 of content the sidebar folds, then the assistant panel (the board's
// List does the same); a sidebar folded by the width comes back once there
// is room, one the user folded stays folded. The window's assistant panel
// moves into AssistantHost while ✦ shows it here (D28) and back to the
// mail's split view when the window leaves the Calendar
// (MainWindow.CalendarPage.cs).

using System;
using System.Linq;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Calendars;

/// <summary>The calendar page's sidebar, views and assistant slot.</summary>
public sealed partial class CalendarContentView : UserControl
{
    private ICalendarView? current;
    private bool sidebarShown = true;

    // The sidebar folded because the page was too narrow for it (not by the
    // user): it comes back when there is room again.
    private bool sidebarAutoCollapsed;
    private bool assistantOpen;

    /// <summary>An empty content; <see cref="Update"/> fills it.</summary>
    public CalendarContentView()
    {
        InitializeComponent();
    }

    /// <summary>The sidebar folded or came back, or the panel folded (the bar's toggle and ✦ follow).</summary>
    public event EventHandler? LayoutChanged;

    /// <summary>The sidebar.</summary>
    public CalendarSidebar SidebarView => Sidebar;

    /// <summary>Where the page's empty state lies, over the views.</summary>
    public Panel EmptyHost => EmptyHostGrid;

    /// <summary>Whether the sidebar shows.</summary>
    public bool SidebarShown => sidebarShown;

    /// <summary>Whether the window's assistant panel is here and unfolded.</summary>
    public bool AssistantShown => assistantOpen && AssistantHostGrid.Children.Count > 0;

    /// <summary>The view shown (none while the empty state shows).</summary>
    internal ICalendarView? Current => current;

    /// <summary>Whether the page's empty state shows for <paramref name="controller"/>: no results, no filter, and Core says why there is nothing.</summary>
    public static bool ShowsEmpty(CalendarController controller)
    {
        ArgumentNullException.ThrowIfNull(controller);
        return controller.SearchResults is null && controller.Filter == CalendarController.ContentFilter.None
            && controller.EmptyKind is not null;
    }

    /// <summary>Reads the controller (after its Changed): the sidebar, then what shows and its view.</summary>
    public void Update(CalendarController controller)
    {
        ArgumentNullException.ThrowIfNull(controller);
        Sidebar.Update(controller);
        ICalendarView? next;
        if (controller.SearchResults is not null || controller.Filter == CalendarController.ContentFilter.Unanswered)
        {
            next = Agenda;
        }
        else if (ShowsEmpty(controller))
        {
            next = null;
        }
        else
        {
            next = controller.View switch
            {
                Cal.View.Month => Month,
                Cal.View.Agenda => Agenda,
                _ => Week,
            };
        }
        current = next;
        Week.Visibility = ReferenceEquals(next, Week) ? Visibility.Visible : Visibility.Collapsed;
        Month.Visibility = ReferenceEquals(next, Month) ? Visibility.Visible : Visibility.Collapsed;
        Agenda.Visibility = ReferenceEquals(next, Agenda) ? Visibility.Visible : Visibility.Collapsed;
        next?.Update(controller);
    }

    /// <summary>The element of occurrence <paramref name="key"/> in what shows.</summary>
    public FrameworkElement? ElementFor(string key) => current?.ElementFor(key);

    /// <summary>Gives the keyboard to the view: its selection, else its first occurrence.</summary>
    public bool FocusView() => current?.FocusSelection() == true;

    /// <summary>Whether <paramref name="element"/> lies in the views (not the sidebar, not the panel).</summary>
    public bool InViews(DependencyObject? element)
    {
        for (var d = element; d is not null; d = Microsoft.UI.Xaml.Media.VisualTreeHelper.GetParent(d))
        {
            if (ReferenceEquals(d, ViewsHost))
            {
                return true;
            }
            if (ReferenceEquals(d, this))
            {
                return false;
            }
        }
        return false;
    }

    /// <summary>Closes the Month's "+N more" flyouts (Escape's first step).</summary>
    public void CloseFlyouts() => Month.CloseFlyouts();

    /// <summary>The bar's sidebar toggle: the user's choice from then on.</summary>
    public void ToggleSidebar()
    {
        sidebarShown = !sidebarShown;
        sidebarAutoCollapsed = false;
        Apply();
    }

    /// <summary>Takes the window's assistant panel in and unfolds it.</summary>
    public void ShowAssistant(UIElement panel)
    {
        ArgumentNullException.ThrowIfNull(panel);
        if (!AssistantHostGrid.Children.Contains(panel))
        {
            AssistantHostGrid.Children.Clear();
            AssistantHostGrid.Children.Add(panel);
        }
        assistantOpen = true;
        Fold(ActualWidth);
        Apply();
    }

    /// <summary>Folds the panel (it stays here until <see cref="ReleaseAssistant"/>).</summary>
    public void HideAssistant()
    {
        if (!assistantOpen)
        {
            return;
        }
        assistantOpen = false;
        Apply();
    }

    /// <summary>Gives the panel back (the window returns it to the mail's split view); the slot folds.</summary>
    public UIElement? ReleaseAssistant()
    {
        assistantOpen = false;
        var panel = AssistantHostGrid.Children.FirstOrDefault();
        AssistantHostGrid.Children.Clear();
        Apply();
        return panel;
    }

    private void OnSizeChanged(object sender, SizeChangedEventArgs e)
    {
        Fold(e.NewSize.Width);
        Apply();
    }

    // Folds the sidebar, then the assistant panel, when the content would
    // get less than its minimum; a sidebar folded by the width comes back
    // at its width once there is room (macOS fold(width:)).
    private void Fold(double width)
    {
        if (width <= 0)
        {
            return;
        }
        var assistant = assistantOpen ? PaneLayout.AssistantMinimum + 1 : 0;
        if (sidebarShown && width < CalendarMetrics.SidebarMinimum + 1 + CalendarMetrics.ContentMinimum + assistant)
        {
            sidebarShown = false;
            sidebarAutoCollapsed = true;
        }
        else if (!sidebarShown && sidebarAutoCollapsed
            && width >= CalendarMetrics.SidebarWidth + 1 + CalendarMetrics.ContentMinimum + assistant)
        {
            sidebarShown = true;
            sidebarAutoCollapsed = false;
        }
        if (assistantOpen && width < CalendarMetrics.ContentMinimum + assistant)
        {
            assistantOpen = false;
        }
    }

    private void Apply()
    {
        var sidebarBefore = SidebarColumn.Width.Value > 0;
        var assistantBefore = AssistantSlot.Visibility == Visibility.Visible;
        SidebarColumn.Width = new GridLength(sidebarShown ? CalendarMetrics.SidebarWidth : 0);
        Sidebar.Visibility = sidebarShown ? Visibility.Visible : Visibility.Collapsed;
        var showAssistant = AssistantShown;
        AssistantColumn.Width = new GridLength(showAssistant ? PaneLayout.AssistantWidth(Math.Max(ActualWidth, PaneLayout.AssistantMinimum)) : 0);
        AssistantSlot.Visibility = showAssistant ? Visibility.Visible : Visibility.Collapsed;
        if (sidebarBefore != sidebarShown || assistantBefore != showAssistant)
        {
            LayoutChanged?.Invoke(this, EventArgs.Empty);
        }
    }
}
