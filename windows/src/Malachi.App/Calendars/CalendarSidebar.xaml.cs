// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Calendar/CalendarSidebarViewController.swift
// (update, rebuild, SidebarSectionHeading, SidebarRowButton,
// SidebarCalendarRow, ColorSquare, SidebarStateRow, the Add Calendar…
// menu); GTK: the calendar's sidebar follows (docs/calendar.md §6.4).
//
// Everything comes from CalendarController.Sidebar: the mini month (a day
// goes to it, the arrows change its month alone), the Unanswered row with
// its count (the controller's Unanswered filter, highlighted while it
// shows; a view chosen in the bar leaves it), per account a heading with
// the kind capsule (the mail sidebar's, FolderTree.AccountHeaderBadge, and
// CALDAV for a CalDAV account, as macOS's accountHeaderBadge) and its
// calendars, each with a checkbox (calendar.update visible through the
// controller) and its colour, and the account's state row with Sign In…
// (the re-sign-in with the calendar scopes). The rows are made again only
// when they change, not on every step through the weeks. Add Calendar…'s
// menu asks the window: Add CalDAV Calendar… (its window), "Sync a
// Microsoft 365 account's calendars…" (Preferences → Calendar).

using System;
using Malachi.App.Main;
using Malachi.App.Resources;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Core.Model;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;

namespace Malachi.App.Calendars;

/// <summary>The Calendar's sidebar (macOS <c>CalendarSidebarViewController</c>).</summary>
public sealed partial class CalendarSidebar : UserControl
{
    // The capsule of a CalDAV account (macOS accountHeaderBadge): a
    // protocol's name, never translated, as FolderTree's IMAP, GOOGLE, M365.
    private const string CalDAVBadge = "CALDAV";

    private CalendarController? controller;
    private CalendarController.SidebarModel? shown;
    private CalendarController.ContentFilter? shownFilter;

    /// <summary>An empty sidebar; <see cref="Update"/> fills it.</summary>
    public CalendarSidebar()
    {
        InitializeComponent();
        MiniMonth.DayPicked += (_, day) => controller?.GoTo(day);
        MiniMonth.Stepped += (_, dir) => controller?.StepMiniMonth(dir);
    }

    /// <summary>Add Calendar… ▸ Add CalDAV Calendar….</summary>
    public event EventHandler? AddCalDAVRequested;

    /// <summary>Add Calendar… ▸ Sync a Microsoft 365 account's calendars….</summary>
    public event EventHandler? SyncMicrosoftRequested;

    /// <summary>The Unanswered row (the smoke tests, the keyboard).</summary>
    internal Button? UnansweredRow { get; private set; }

    /// <summary>After the controller's Changed: the mini month, then the rows if they changed.</summary>
    public void Update(CalendarController controller)
    {
        ArgumentNullException.ThrowIfNull(controller);
        this.controller = controller;
        var s = controller.Sidebar;
        MiniMonth.Set(s.MiniMonth);
        AddCalendarText.Text = s.AddCalendar;
        AutomationProperties.SetName(AddCalendarButton, s.AddCalendar);
        AddCalDAVItem.Text = s.AddCalDAV;
        SyncMicrosoftItem.Text = s.SyncMicrosoft;
        // The mini month redraws by itself; the rows only when they change.
        var rows = s with { MiniMonth = shown?.MiniMonth ?? s.MiniMonth };
        if (!rows.Equals(shown) || shownFilter != controller.Filter)
        {
            shown = rows;
            shownFilter = controller.Filter;
            Rebuild(s, controller.Filter);
        }
    }

    /// <inheritdoc/>
    protected override AutomationPeer OnCreateAutomationPeer() => new CalendarRegionPeer(this, nameof(CalendarSidebar));

    private void Rebuild(CalendarController.SidebarModel s, CalendarController.ContentFilter filter)
    {
        Rows.Children.Clear();
        Rows.Children.Add(Heading(s.InvitationsTitle, "", top: 0));
        UnansweredRow = UnansweredButton(s, filter == CalendarController.ContentFilter.Unanswered);
        Rows.Children.Add(UnansweredRow);
        foreach (var a in s.Accounts)
        {
            Rows.Children.Add(Heading(a.Name, Badge(a), top: 10));
            foreach (var c in a.Calendars)
            {
                Rows.Children.Add(CalendarRow(c));
            }
            if (a.StateLine.Length > 0)
            {
                Rows.Children.Add(StateRow(a));
            }
        }
    }

    // The kind capsule of an account (CALDAV for a CalDAV account).
    private static string Badge(CalendarController.SidebarAccount a)
    {
        if (a.Account is { } acc)
        {
            return acc.Config.ProtocolKind == AccountKind.CalDAV ? CalDAVBadge : FolderTree.AccountHeaderBadge(acc);
        }
        return a.Source == CalendarSource.CalDAV ? CalDAVBadge : "";
    }

    // A section's or an account's heading, with the kind capsule after an account's.
    private static CapsulePanel Heading(string text, string badge, double top)
    {
        var label = new TextBlock { Style = CalendarStyles.Get("CalendarSidebarHeadingStyle"), Text = CalendarDisplay.Clean(text) };
        AutomationProperties.SetHeadingLevel(label, AutomationHeadingLevel.Level2);
        AutomationProperties.SetName(label, CalendarDisplay.Clean(badge.Length == 0 ? text : Cal.Text.Dot(text, badge)));
        var panel = new CapsulePanel { Margin = new Thickness(2, top, 0, 3) };
        panel.Children.Add(label);
        var capsule = new Border
        {
            Style = (Style)Application.Current.Resources["KindBadgeBorderStyle"],
            Visibility = badge.Length == 0 ? Visibility.Collapsed : Visibility.Visible,
            Child = new TextBlock { Style = (Style)Application.Current.Resources["KindBadgeTextStyle"], Text = badge },
        };
        AutomationProperties.SetAccessibilityView(capsule, Microsoft.UI.Xaml.Automation.Peers.AccessibilityView.Raw);
        panel.Children.Add(capsule);
        return panel;
    }

    // Unanswered with its count badge, highlighted while its filter shows.
    private Button UnansweredButton(CalendarController.SidebarModel s, bool selected)
    {
        var row = new Grid { ColumnSpacing = 8, Padding = new Thickness(4, 0, 4, 0), Height = 26 };
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        var icon = Icons.Create("mail-unread", 14);
        icon.Style = CalendarStyles.Get("CalendarAccentIconStyle");
        row.Children.Add(icon);
        var text = new TextBlock
        {
            Style = (Style)Application.Current.Resources["BodyTextBlockStyle"],
            VerticalAlignment = VerticalAlignment.Center,
            MaxLines = 1,
            TextTrimming = TextTrimming.CharacterEllipsis,
            Text = s.UnansweredTitle,
        };
        Grid.SetColumn(text, 1);
        row.Children.Add(text);
        if (s.Unanswered > 0)
        {
            var badge = new Border
            {
                Style = CalendarStyles.Get("CalendarCountBadgeStyle"),
                Child = new TextBlock
                {
                    Style = CalendarStyles.Get("CalendarCountBadgeTextStyle"),
                    Text = s.Unanswered.ToString(System.Globalization.CultureInfo.CurrentCulture),
                },
            };
            Grid.SetColumn(badge, 2);
            row.Children.Add(badge);
        }
        var holder = new Border { Style = CalendarStyles.Get(selected ? "CalendarSelectedRowStyle" : "CalendarRowStyle"), Child = row };
        var button = new Button
        {
            Style = (Style)Application.Current.Resources["SubtleButtonStyle"],
            Padding = new Thickness(0),
            HorizontalAlignment = HorizontalAlignment.Stretch,
            HorizontalContentAlignment = HorizontalAlignment.Stretch,
            Content = holder,
        };
        AutomationProperties.SetAutomationId(button, "CalendarUnanswered");
        AutomationProperties.SetName(
            button,
            s.Unanswered > 0 ? Cal.Text.Dot(s.UnansweredTitle, s.Unanswered.ToString(System.Globalization.CultureInfo.CurrentCulture)) : s.UnansweredTitle);
        button.Click += (_, _) => controller?.ShowUnanswered();
        return button;
    }

    // A calendar: its checkbox, its colour square, its name and its note.
    private Grid CalendarRow(CalendarController.SidebarCalendar c)
    {
        var name = CalendarDisplay.Clean(c.Name);
        var note = CalendarDisplay.Clean(c.Note);
        var row = new Grid { ColumnSpacing = 6, Height = 26, Margin = new Thickness(2, 0, 0, 0) };
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        var check = new CheckBox { MinWidth = 0, Padding = new Thickness(0), IsChecked = c.Visible, VerticalAlignment = VerticalAlignment.Center };
        AutomationProperties.SetName(check, name);
        AutomationProperties.SetAutomationId(check, "CalendarVisible:" + c.Id.Value);
        var id = c.Id;
        check.Checked += (_, _) => controller?.SetVisible(true, id);
        check.Unchecked += (_, _) => controller?.SetVisible(false, id);
        row.Children.Add(check);
        var square = new Border
        {
            Width = 10,
            Height = 10,
            CornerRadius = new CornerRadius(2.5),
            VerticalAlignment = VerticalAlignment.Center,
            Background = new SolidColorBrush(CalendarPalette.Parse(c.Color)),
        };
        Grid.SetColumn(square, 1);
        row.Children.Add(square);
        var label = new TextBlock
        {
            Style = (Style)Application.Current.Resources["BodyTextBlockStyle"],
            VerticalAlignment = VerticalAlignment.Center,
            MaxLines = 1,
            TextWrapping = TextWrapping.NoWrap,
            TextTrimming = TextTrimming.CharacterEllipsis,
            Text = name,
        };
        Grid.SetColumn(label, 2);
        row.Children.Add(label);
        if (note.Length > 0)
        {
            var hint = new TextBlock
            {
                Style = CalendarStyles.Get("CalendarDimTextStyle"),
                VerticalAlignment = VerticalAlignment.Center,
                MaxWidth = 90,
                TextTrimming = TextTrimming.CharacterEllipsis,
                Text = note,
            };
            Grid.SetColumn(hint, 3);
            row.Children.Add(hint);
        }
        ToolTipService.SetToolTip(row, note.Length == 0 ? name : Cal.Text.Dot(name, note));
        return row;
    }

    // An account's state ("Calendar access needs a new sign-in"), with Sign In… when offered.
    private StackPanel StateRow(CalendarController.SidebarAccount a)
    {
        var col = new StackPanel { Spacing = 4, Margin = new Thickness(4, 4, 0, 4) };
        var top = new Grid { ColumnSpacing = 5 };
        top.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        top.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        var icon = Icons.Create("dialog-warning", 12);
        icon.Style = CalendarStyles.Get("CalendarWarningIconStyle");
        icon.Margin = new Thickness(0, 2, 0, 0);
        top.Children.Add(icon);
        var text = new TextBlock
        {
            Style = CalendarStyles.Get("CalendarStateTextStyle"),
            Text = CalendarDisplay.Clean(a.StateLine),
        };
        Grid.SetColumn(text, 1);
        top.Children.Add(text);
        col.Children.Add(top);
        if (a.OffersSignIn)
        {
            var b = new Button { Content = Cal.Text.SignIn, Padding = new Thickness(8, 2, 8, 3) };
            AutomationProperties.SetAutomationId(b, "CalendarSignIn:" + a.AccountId.Value);
            var account = a.AccountId;
            b.Click += (_, _) => controller?.SignIn(account);
            col.Children.Add(b);
        }
        return col;
    }

    private void OnAddCalDAVClick(object sender, RoutedEventArgs e) => AddCalDAVRequested?.Invoke(this, EventArgs.Empty);

    private void OnSyncMicrosoftClick(object sender, RoutedEventArgs e) => SyncMicrosoftRequested?.Invoke(this, EventArgs.Empty);
}
