// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Calendar/EventDetailViewController.swift
// (update, rebuild, timeLines, responsePullDown, notesView, footer, the
// actions, handleKey) and EventDetailViews.swift (EventDetailNotesView's
// links); GTK: the event detail follows (docs/calendar.md §6.4), the rows
// and the actions are ui/internal/calendar/detail.go and keys.go (Detail,
// KeyFor).
//
// The content of the event detail flyout over its EventDetailController:
// the view reads the controller after every Changed and sends the clicks
// back. Windows: the elements are fixed in the XAML and shown or hidden by
// the detail (macOS builds its stack again), so a button keeps the
// keyboard while a write runs; only the attendees and the notes' links are
// made again, when they change. The notes are a plain TextBlock and the
// daemon's links are buttons under it (macOS makes clickable ranges of the
// text view), each going to the controller's OpenLink and so through
// "Open This Link?". The view takes the keyboard when the flyout opens:
// Escape closes it, Return is Edit and Delete (or Backspace) Remove
// (Cal.KeyFor with an occurrence selected), except in a text the user is
// selecting.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.App.Resources;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Boards;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Core.Model;
using Microsoft.UI.Input;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using Windows.UI.Core;
using WinUIKey = Windows.System.VirtualKey;

namespace Malachi.App.Calendars;

/// <summary>The content of the event detail flyout (artboard 3).</summary>
public sealed partial class EventDetailView : UserControl
{
    private EventDetailController? controller;
    private Func<Cal.Snapshot>? snapshot;
    private Func<bool>? canAsk;

    // What the attendee list and the notes' links show now.
    private IReadOnlyList<Cal.AttendeeRow> shownAttendees = [];
    private IReadOnlyList<string> shownLinks = [];

    /// <summary>An empty detail; <see cref="Attach"/> gives it its controller.</summary>
    public EventDetailView()
    {
        InitializeComponent();
        JoinButton.Content = Cal.JoinVideoCall;
        InvitationButton.Content = Cal.ShowInvitation;
        EditButton.Content = Cal.Edit;
        AskItem.Text = Cal.AskAboutEvent;
        FindTimeItem.Text = Cal.FindAnotherTime;
        var assistant = Assistant.Texts().Assistant;
        AssistantIcon.Glyph = Icons.Glyph("malachi-assistant");
        AutomationProperties.SetName(AssistantButton, assistant);
        ToolTipService.SetToolTip(AssistantButton, assistant);
        KeyDown += OnKeyDown;
    }

    /// <summary>Escape: the flyout closes.</summary>
    public event EventHandler? CloseRequested;

    /// <summary>
    /// Shows <paramref name="detail"/>: <paramref name="snapshotOf"/> gives the
    /// calendars' colours and the accounts' kinds, <paramref name="assistantPanel"/>
    /// whether Ask about This Event is offered (the assistant panel exists).
    /// </summary>
    internal void Attach(EventDetailController detail, Func<Cal.Snapshot> snapshotOf, Func<bool> assistantPanel)
    {
        ArgumentNullException.ThrowIfNull(detail);
        ArgumentNullException.ThrowIfNull(snapshotOf);
        ArgumentNullException.ThrowIfNull(assistantPanel);
        controller = detail;
        snapshot = snapshotOf;
        canAsk = assistantPanel;
        Update();
    }

    /// <inheritdoc/>
    protected override AutomationPeer OnCreateAutomationPeer() => new FrameworkElementAutomationPeer(this);

    /// <summary>Puts the keyboard on the detail (Escape, Return, Delete).</summary>
    public void TakeKeyboard() => Focus(FocusState.Programmatic);

    /// <summary>After every change of the controller (its Changed).</summary>
    public void Update()
    {
        if (controller is not { } c || snapshot is not { } snap)
        {
            return;
        }
        var d = c.Detail;
        Occurrence? o = c.Event ?? c.Occurrence;
        var s = snap();
        var color = o?.CalendarId is { } cal ? s.FindCalendar(cal)?.Color : null;
        var badge = o is not null && s.FindAccount(o.AccountId) is { } account ? FolderTree.AccountHeaderBadge(account) : "";
        var ask = canAsk?.Invoke() ?? false;

        Swatch.Background = new SolidColorBrush(CalendarPalette.Parse(color));
        KindText.Text = badge;
        KindBadge.Visibility = Show(badge.Length > 0);
        LoadingRing.IsActive = c.Loading && d is null;
        LoadingRing.Visibility = Show(c.Loading && d is null);
        ShowError(c.Error);

        if (d is null)
        {
            // Until the event arrives: the occurrence's title and time.
            Header.Visibility = Show(o is not null);
            TitleText.Visibility = Show(o is not null);
            TimeRow.Visibility = Show(o is not null);
            CalendarLine.Text = "";
            if (o is not null)
            {
                TitleText.Text = CalendarDisplay.Title(o.Title);
                ShowTime(TimeLines(o, Cal.TimeLine(o, c.Env)));
            }
            foreach (var row in new FrameworkElement[] { RepeatRow, LocationRow, VideoRow, PeopleRow, NotesRow, InvitationRow, Footer })
            {
                row.Visibility = Visibility.Collapsed;
            }
            return;
        }

        Header.Visibility = Visibility.Visible;
        CalendarLine.Text = CalendarDisplay.Clean(d.CalendarLine);
        TitleText.Visibility = Visibility.Visible;
        TitleText.Text = CalendarDisplay.Clean(d.Title);
        if (o is not null)
        {
            ShowTime(TimeLines(o, d.TimeLine));
        }
        else
        {
            ShowTime([d.TimeLine]);
        }
        TimeRow.Visibility = Show(TimeFirst.Text.Length > 0);

        RepeatText.Text = CalendarDisplay.Clean(d.Recurrence);
        RepeatRow.Visibility = Show(d.Recurrence.Length > 0);
        LocationText.Text = CalendarDisplay.Clean(d.Location);
        LocationRow.Visibility = Show(d.Location.Length > 0);
        VideoHost.Text = CalendarDisplay.Clean(d.VideoHost);
        VideoHost.Visibility = Show(d.VideoHost.Length > 0);
        VideoRow.Visibility = Show(d.VideoCall.Length > 0);

        ShowPeople(c, d);

        // Core's block cleaning (Board.CleanBlock) keeps the line breaks that
        // DisplayText.Clean would turn into spaces.
        NotesText.Text = d.Notes;
        NotesRow.Visibility = Show(d.Notes.Length > 0);
        ShowLinks(d.Links);
        InvitationRow.Visibility = Show(d.ShowInvitation);

        Footer.Visibility = Visibility.Visible;
        EditButton.Visibility = Show(d.CanEdit);
        EditButton.IsEnabled = d.CanEdit && !c.Busy;
        RemoveButton.Content = d.RemoveTitle;
        RemoveButton.Visibility = Show(d.CanRemove);
        RemoveButton.IsEnabled = d.CanRemove && !c.Busy;
        // The panel exists only with the Assistant shown and In App chosen;
        // Find Another Time only for an event that can be edited.
        AskItem.Visibility = Show(ask);
        FindTimeItem.Visibility = Show(d.CanEdit);
        AssistantButton.Visibility = Show(ask || d.CanEdit);
        AssistantButton.IsEnabled = !c.Busy;
    }

    private static Visibility Show(bool on) => on ? Visibility.Visible : Visibility.Collapsed;

    // The people line, the attendees (made again only when they change),
    // Show All and "You: …".
    private void ShowPeople(EventDetailController c, Cal.Detail d)
    {
        PeopleLine.Text = CalendarDisplay.Clean(d.PeopleLine);
        PeopleLine.Visibility = Show(d.PeopleLine.Length > 0);
        var attendees = c.ShownAttendees;
        if (!attendees.SequenceEqual(shownAttendees))
        {
            shownAttendees = attendees;
            AttendeeList.Children.Clear();
            foreach (var a in attendees)
            {
                AttendeeList.Children.Add(new EventAttendeeRow(a));
            }
        }
        AttendeeList.Visibility = Show(attendees.Count > 0);
        var showAll = c.ShowAllAttendees;
        ShowAllButton.Content = showAll;
        ShowAllButton.Visibility = Show(showAll.Length > 0);
        ResponseButton.Content = Cal.MyResponseText(d.MyResponse);
        ResponseButton.Visibility = Show(d.CanRespond);
        ResponseButton.IsEnabled = d.CanRespond && !c.Busy;
        PeopleRow.Visibility = Show(d.PeopleLine.Length > 0 || d.CanRespond);
    }

    // The daemon's links, one button each under the notes.
    private void ShowLinks(IReadOnlyList<string> links)
    {
        var shown = links.Where(l => l.Length > 0).ToList();
        if (shown.SequenceEqual(shownLinks, StringComparer.Ordinal))
        {
            return;
        }
        shownLinks = shown;
        NotesLinks.Children.Clear();
        foreach (var link in shown)
        {
            var button = new HyperlinkButton
            {
                Padding = new Thickness(0),
                FontSize = 12,
                Content = new TextBlock { Text = link, MaxLines = 1, TextTrimming = TextTrimming.CharacterEllipsis, TextWrapping = TextWrapping.NoWrap },
            };
            AutomationProperties.SetAutomationId(button, "EventDetailLink");
            AutomationProperties.SetName(button, link);
            ToolTipService.SetToolTip(button, link);
            var url = link;
            button.Click += (_, _) => controller?.OpenLink(url);
            NotesLinks.Children.Add(button);
        }
    }

    private void ShowTime(IReadOnlyList<string> lines)
    {
        TimeFirst.Text = lines.Count > 0 ? CalendarDisplay.Clean(lines[0]) : "";
        var second = lines.Count > 1 ? CalendarDisplay.Clean(lines[1]) : "";
        TimeSecond.Text = second;
        TimeSecond.Visibility = Show(second.Length > 0);
    }

    private void ShowError(string text)
    {
        ErrorText.Text = text;
        ErrorText.Visibility = Show(text.Length > 0);
    }

    // timeLines: a timed event within one day as its date and
    // "10:00–11:30 · 1 h 30 min", anything else as the detail's one line.
    private IReadOnlyList<string> TimeLines(Occurrence o, string fallback)
    {
        var env = controller!.Env;
        if (o.AllDay || Cal.Span(o, env.Zone) is not { } span)
        {
            return [fallback];
        }
        var (s, e) = span;
        if (s != e && Cal.StartOfDay(s, env.Zone) != Cal.StartOfDay(e.AddSeconds(-1), env.Zone))
        {
            return [fallback];
        }
        var times = Cal.Text.Dot(Cal.Text.TimeRangeText(s, e, env), Cal.DurationText(e - s));
        return [Cal.Text.LongDateText(s, env), times];
    }

    // "You: …" ▾: Accept, Tentative, Decline, then Decline Without Reply.
    private void OnResponseMenuOpening(object? sender, object e)
    {
        ResponseMenu.Items.Clear();
        if (controller?.Detail is not { } d)
        {
            return;
        }
        foreach (var choice in EventDetailController.ResponseChoices)
        {
            var item = new ToggleMenuFlyoutItem { Text = Cal.ResponseAction(choice), IsChecked = choice == d.MyResponse };
            AutomationProperties.SetAutomationId(item, ResponseAutomationId(choice));
            var response = choice;
            item.Click += (_, _) => controller?.Respond(response);
            ResponseMenu.Items.Add(item);
        }
        ResponseMenu.Items.Add(new MenuFlyoutSeparator());
        var without = new MenuFlyoutItem { Text = Cal.DeclineWithoutReply };
        AutomationProperties.SetAutomationId(without, "EventDetailDeclineWithoutReply");
        without.Click += (_, _) => controller?.Respond(EventResponse.Declined, withoutReply: true);
        ResponseMenu.Items.Add(without);
    }

    private static string ResponseAutomationId(EventResponse r) => r.Value switch
    {
        EventResponse.Tentative => "EventDetailTentative",
        EventResponse.Declined => "EventDetailDecline",
        _ => "EventDetailAccept",
    };

    private void OnJoinClick(object sender, RoutedEventArgs e) => controller?.JoinVideoCall();

    private void OnShowAllClick(object sender, RoutedEventArgs e) => controller?.ExpandAttendees();

    private void OnShowInvitationClick(object sender, RoutedEventArgs e) => controller?.ShowInvitation();

    private void OnEditClick(object sender, RoutedEventArgs e) => controller?.Edit();

    private void OnRemoveClick(object sender, RoutedEventArgs e) => controller?.Remove();

    private void OnAskClick(object sender, RoutedEventArgs e) => controller?.AskAssistant();

    private void OnFindAnotherTimeClick(object sender, RoutedEventArgs e) => controller?.FindAnotherTime();

    private static bool IsDown(WinUIKey key) =>
        InputKeyboardSource.GetKeyStateForCurrentThread(key).HasFlag(CoreVirtualKeyStates.Down);

    // handleKey: Escape closes, Return is Edit, Delete Remove (a key a
    // focused button took never gets here).
    private void OnKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Handled || controller is not { } c)
        {
            return;
        }
        var focused = XamlRoot is { } root ? FocusManager.GetFocusedElement(root) : null;
        var inText = focused is TextBox or TextBlock or RichTextBlock;
        var press = Cal.Press.FromVirtualKey((int)e.Key, IsDown(WinUIKey.Control), IsDown(WinUIKey.Shift), IsDown(WinUIKey.Menu));
        switch (Cal.KeyFor(press, new Cal.KeyContext(Board.Mode.Calendar, inText, Selected: true)))
        {
            case Cal.KeyAction.Escape:
                CloseRequested?.Invoke(this, EventArgs.Empty);
                e.Handled = true;
                break;
            case Cal.KeyAction.OpenDetail when c.Detail?.CanEdit == true && !c.Busy:
                c.Edit();
                e.Handled = true;
                break;
            case Cal.KeyAction.Remove when c.Detail?.CanRemove == true && !c.Busy:
                c.Remove();
                e.Handled = true;
                break;
        }
    }
}
