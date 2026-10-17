// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Calendar/CalendarToolbar.swift
// (update, setAssistant(visible:), todayClicked, stepClicked, viewChosen,
// assistantClicked); GTK: the calendar's bar follows (docs/calendar.md
// §6.4).
//
// The calendar's controls. Today, ‹ › and the views act on the controller
// (Today, Step, SetView, which leaves the Unanswered filter); the sidebar
// toggle and ✦ ask the page; New Event is the window's command
// (Commands.NewEvent, bound by the page). The views' segment always shows
// the controller's view (a click on the shown one changes nothing). Below
// CalendarMetrics.BarCompactWidth the segments become a menu of the same
// four views, since a WinUI bar has no overflow (macOS's overflow gives
// way with ✦, the search field and then the segments).

using System;
using Malachi.Core.Assistants;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;

namespace Malachi.App.Calendars;

/// <summary>The calendar's controls in the page's bar (macOS <c>CalendarToolbar</c>).</summary>
public sealed partial class CalendarBar : UserControl
{
    private CalendarController? controller;
    private bool syncing;

    /// <summary>The bar's controls; <see cref="Update"/> follows the controller.</summary>
    public CalendarBar()
    {
        InitializeComponent();
        TodayButton.Content = Cal.Text.Today;
        ToolTipService.SetToolTip(TodayButton, Cal.Text.Today);
        foreach (var (button, text) in new[] { (PreviousButton, Cal.Text.Previous), (NextButton, Cal.Text.Next) })
        {
            AutomationProperties.SetName(button, text);
            ToolTipService.SetToolTip(button, text);
        }
        foreach (var (button, view) in Segments)
        {
            button.Tag = view;
            button.Content = Cal.Text.ViewTitle(view);
            ViewChooser.Items.Add(Cal.Text.ViewTitle(view));
        }
        AutomationProperties.SetName(ViewChooser, Cal.Text.ViewTitle(Cal.DefaultView));
        AutomationProperties.SetName(NewEventButton, Cal.Text.NewEvent);
        ToolTipService.SetToolTip(NewEventButton, Cal.Text.NewEvent);
        var assistant = Assistant.Texts().Assistant;
        AutomationProperties.SetName(AssistantButton, assistant);
        ToolTipService.SetToolTip(AssistantButton, assistant);
        SetSidebarShown(true);
    }

    /// <summary>The sidebar toggle: the page shows or folds its sidebar.</summary>
    public event EventHandler? SidebarToggleRequested;

    /// <summary>✦: the page shows or folds the assistant panel.</summary>
    public event EventHandler? AssistantToggleRequested;

    /// <summary>"+" (New Event), bound to the window's command by the page.</summary>
    public Button NewEvent => NewEventButton;

    // The views' segments in the toolbar's order.
    private (ToggleButton Button, Cal.View View)[] Segments =>
    [
        (ViewDayButton, Cal.View.Day),
        (ViewWeekButton, Cal.View.Week),
        (ViewMonthButton, Cal.View.Month),
        (ViewAgendaButton, Cal.View.Agenda),
    ];

    /// <summary>Brings the bar in step with the controller: the title and subtitle, the view shown.</summary>
    public void Update(CalendarController controller)
    {
        ArgumentNullException.ThrowIfNull(controller);
        this.controller = controller;
        TitleText.Text = controller.Title;
        SubtitleText.Text = controller.Subtitle;
        SubtitleText.Visibility = controller.Subtitle.Length == 0 ? Visibility.Collapsed : Visibility.Visible;
        SyncViews();
    }

    /// <summary>The sidebar toggle's text for the sidebar's state.</summary>
    public void SetSidebarShown(bool shown)
    {
        // Windows-only strings: macOS's View ▸ Show/Hide Sidebar are macOS-only, GTK has none.
        var text = shown ? "Hide Sidebar" : "Show Sidebar";
        AutomationProperties.SetName(SidebarButton, text);
        ToolTipService.SetToolTip(SidebarButton, text);
    }

    /// <summary>✦: shown while the panel exists (the Assistant shown, In App chosen), checked while it shows here.</summary>
    public void SetAssistant(bool visible, bool open)
    {
        AssistantButton.Visibility = visible ? Visibility.Visible : Visibility.Collapsed;
        AssistantButton.IsChecked = visible && open;
    }

    /// <inheritdoc/>
    protected override AutomationPeer OnCreateAutomationPeer() => new CalendarRegionPeer(this, nameof(CalendarBar));

    // The segments and the menu show the controller's view.
    private void SyncViews()
    {
        var view = controller?.View ?? Cal.DefaultView;
        syncing = true;
        try
        {
            var i = 0;
            foreach (var (button, v) in Segments)
            {
                button.IsChecked = v == view;
                if (v == view)
                {
                    ViewChooser.SelectedIndex = i;
                }
                i++;
            }
        }
        finally
        {
            syncing = false;
        }
    }

    private void OnTodayClick(object sender, RoutedEventArgs e) => controller?.Today();

    private void OnPreviousClick(object sender, RoutedEventArgs e) => controller?.Step(-1);

    private void OnNextClick(object sender, RoutedEventArgs e) => controller?.Step(1);

    private void OnViewClick(object sender, RoutedEventArgs e)
    {
        if (sender is ToggleButton { Tag: Cal.View view })
        {
            controller?.SetView(view);
        }
        SyncViews();
    }

    private void OnViewChosen(object sender, SelectionChangedEventArgs e)
    {
        if (syncing || ViewChooser.SelectedIndex < 0 || ViewChooser.SelectedIndex >= Cal.Views.Count)
        {
            return;
        }
        controller?.SetView(Cal.Views[ViewChooser.SelectedIndex]);
        SyncViews();
    }

    private void OnSidebarClick(object sender, RoutedEventArgs e) => SidebarToggleRequested?.Invoke(this, EventArgs.Empty);

    private void OnAssistantClick(object sender, RoutedEventArgs e) => AssistantToggleRequested?.Invoke(this, EventArgs.Empty);

    // Below the compact width the segments become a menu.
    private void OnBarSizeChanged(object sender, SizeChangedEventArgs e)
    {
        var compact = e.NewSize.Width < CalendarMetrics.BarCompactWidth;
        ViewSwitch.Visibility = compact ? Visibility.Collapsed : Visibility.Visible;
        ViewChooser.Visibility = compact ? Visibility.Visible : Visibility.Collapsed;
    }
}
