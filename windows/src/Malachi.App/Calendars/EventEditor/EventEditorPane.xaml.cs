// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/EventEditor/EventEditorViewController.swift
// (buildFields, render, fillAlerts, swatch, controlTextDidChange,
// textDidChange, calendarChosen, allDayToggled, repeatChosen, alertChosen,
// nextSlotClicked, deleteClicked, cancelClicked, primaryClicked,
// reloadClicked, save, attachFiles, cleanup); GTK: the event editor follows
// (docs/calendar.md §6.4), the rules are ui/internal/calendar/editor.go
// through EventEditorController.
//
// The form of a New Event / Edit Event window over its
// EventEditorController (docs/calendar.md §5.6, M7): the view writes every
// edit to the controller and shows the controller after every Changed. What
// a programmatic change of a control raises is dropped while the form is
// shown (rendering) or is the value the controller has (a TextBox's
// TextChanged comes later). The dates and times (EventEditorPane.Dates.cs),
// Add Video Call ▾ (EventEditorPane.Video.cs) and the attendees with their
// availability (EventEditorPane.Attendees.cs) are partial files.
//
// Windows: Notes grows to fill the height, the form scrolling once the
// window is shorter than it (the form's smallest height is the scroller's
// viewport); Ctrl+Enter in Notes is the primary, as everywhere else in the
// window (a multi-line TextBox would type a line break); an Attach that
// picked an item without a file-system path says "Only local files can be
// attached", as the compose window does.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.App.Compose;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.Presentation;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;
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

/// <summary>The form of an event editor window (artboard 5).</summary>
public sealed partial class EventEditorPane : UserControl
{
    private AppState? state;
    private EventEditorController? controller;
    private ILogger logger = NullLogger.Instance;

    // The form is being shown: what the controls raise meanwhile is not an edit.
    private bool rendering;
    private bool closed;

    // What the Calendar and Alert popups and the chips show now.
    private List<CalendarId> calendarIds = [];
    private List<string> calendarTitles = [];
    private List<int> alertValues = [];
    private bool alertsAllDay;
    private IReadOnlyList<string> chipIds = [];

    /// <summary>An empty form; <see cref="Attach"/> gives it its controller.</summary>
    public EventEditorPane()
    {
        InitializeComponent();
        TitleBox.PlaceholderText = Cal.TitlePlaceholder;
        AutomationProperties.SetName(TitleBox, Cal.TitlePlaceholder);
        CalendarLabel.Text = Cal.FieldLabel(Cal.Field.Calendar);
        AllDayLabel.Text = Cal.FieldLabel(Cal.Field.AllDay);
        StartLabel.Text = Cal.FieldLabel(Cal.Field.Start);
        EndLabel.Text = Cal.FieldLabel(Cal.Field.End);
        RepeatLabel.Text = Cal.FieldLabel(Cal.Field.Repeat);
        LocationLabel.Text = Cal.FieldLabel(Cal.Field.Location);
        AttendeesLabel.Text = Cal.FieldLabel(Cal.Field.Attendees);
        AvailabilityLabel.Text = Cal.FieldLabel(Cal.Field.Availability);
        AlertLabel.Text = Cal.FieldLabel(Cal.Field.Alert);
        NotesLabel.Text = Cal.FieldLabel(Cal.Field.Notes);
        AutomationProperties.SetName(CalendarBox, Cal.FieldLabel(Cal.Field.Calendar));
        AutomationProperties.SetName(AllDaySwitch, Cal.FieldLabel(Cal.Field.AllDay));
        AutomationProperties.SetName(StartDate, Cal.FieldLabel(Cal.Field.Start));
        AutomationProperties.SetName(StartTime, Cal.FieldLabel(Cal.Field.Start));
        AutomationProperties.SetName(EndDate, Cal.FieldLabel(Cal.Field.End));
        AutomationProperties.SetName(EndTime, Cal.FieldLabel(Cal.Field.End));
        AutomationProperties.SetName(RepeatBox, Cal.FieldLabel(Cal.Field.Repeat));
        AutomationProperties.SetName(LocationBox, Cal.FieldLabel(Cal.Field.Location));
        AutomationProperties.SetLabeledBy(AttendeesBox, AttendeesLabel);
        AutomationProperties.SetName(AlertBox, Cal.FieldLabel(Cal.Field.Alert));
        AutomationProperties.SetName(NotesBox, Cal.FieldLabel(Cal.Field.Notes));
        // The switch alone: its label stands left of it.
        AllDaySwitch.OnContent = null;
        AllDaySwitch.OffContent = null;
        foreach (var c in EventEditorController.RepeatChoices)
        {
            RepeatBox.Items.Add(Cal.RepeatTitle(c));
        }
        NextSlotButton.Content = Cal.NextFreeSlotButton;
        ReloadButton.Content = Cal.Text.ReloadEvent;
        CancelButton.Content = Cal.Cancel;
        ZoneButton.Click += OnZoneClick;
        // Notes takes the height the window leaves; the form scrolls once
        // it needs more.
        Scroller.SizeChanged += (_, _) => Form.MinHeight = Math.Max(0, Scroller.ViewportHeight);
    }

    /// <summary>Cancel: the window's close request (it asks when there are changes).</summary>
    public event EventHandler? CancelRequested;

    /// <summary>Reload Event after a conflict: the window opens the event again in its place.</summary>
    public event EventHandler? ReloadRequested;

    /// <summary>The window the form is in (its dialogs, the file dialog).</summary>
    internal Window? HostWindow { get; set; }

    /// <summary>Shows a transient message over the window.</summary>
    internal Action<string>? ToastHandler { get; set; }

    /// <summary>Whether a popup of the form is open (Escape and Ctrl+W close it, not the window).</summary>
    public bool PopupOpen =>
        suggestions?.IsVisible == true || ZoneFlyout.IsOpen || RepeatFlyout.IsOpen || VideoMenu.IsOpen || CalendarBox.IsDropDownOpen
        || RepeatBox.IsDropDownOpen || AlertBox.IsDropDownOpen;

    // The account the calendar popup's choice belongs to.
    private AccountId? CurrentAccount =>
        controller is { } c ? c.Calendars.FirstOrDefault(x => x.Id == c.Input.CalendarId)?.AccountId : null;

    /// <inheritdoc/>
    protected override AutomationPeer OnCreateAutomationPeer() => new FrameworkElementAutomationPeer(this);

    /// <summary>Shows <paramref name="editor"/> and sends the edits to it.</summary>
    internal void Attach(AppState appState, EventEditorController editor)
    {
        ArgumentNullException.ThrowIfNull(appState);
        ArgumentNullException.ThrowIfNull(editor);
        state = appState;
        controller = editor;
        logger = appState.Logs.CreateLogger<EventEditorPane>();
        rendering = true;
        TitleBox.Text = editor.Input.Title;
        LocationBox.Text = editor.Input.Location;
        NotesBox.Text = editor.Input.Notes;
        rendering = false;
        WireAttendees(appState, editor);
        Chips.RemoveRequested += (_, id) => controller?.RemoveAttachment(id);
        editor.Changed += (_, _) => Render();
        Render();
    }

    /// <summary>The keyboard to the title (the window opened).</summary>
    public void FocusTitle() => TitleBox.Focus(FocusState.Programmatic);

    /// <summary>
    /// The primary button, Ctrl+Enter and Ctrl+S (the window checks Ctrl+S):
    /// a typed, unfinished attendee counts as the field would commit it.
    /// </summary>
    public void Save()
    {
        if (closed || controller is not { } c || c.Saving)
        {
            return;
        }
        SyncAttendees();
        _ = c.SaveAsync();
    }

    /// <summary>Attach: the files picked in the open dialog, imported under the calendar's account.</summary>
    public void AttachFiles() => _ = AttachFilesAsync();

    /// <summary>The window really closes: the popups go, nothing arrives late.</summary>
    public void Close()
    {
        if (closed)
        {
            return;
        }
        closed = true;
        AttendeesBox.Freeze();
        suggestions?.Dispose();
        ZoneFlyout.Hide();
        RepeatFlyout.Hide();
    }

    private static Visibility Show(bool on) => on ? Visibility.Visible : Visibility.Collapsed;

    // render: the controller's state into the controls.
    private void Render()
    {
        if (closed || controller is not { } c)
        {
            return;
        }
        rendering = true;
        try
        {
            var input = c.Input;
            if (!string.Equals(TitleBox.Text, input.Title, StringComparison.Ordinal))
            {
                TitleBox.Text = input.Title;
            }
            RenderCalendars(c);
            AllDaySwitch.IsOn = input.AllDay;
            RenderDates(c);
            var repeat = EventEditorController.RepeatChoices.ToList().IndexOf(c.RepeatChoice);
            if (RepeatBox.SelectedIndex != repeat)
            {
                RepeatBox.SelectedIndex = repeat;
            }
            RenderAlerts(c);
            if (!string.Equals(LocationBox.Text, input.Location, StringComparison.Ordinal))
            {
                LocationBox.Text = input.Location;
            }
            RenderVideo(c);
            RenderAvailability(c);
            if (!string.Equals(NotesBox.Text, input.Notes, StringComparison.Ordinal))
            {
                NotesBox.Text = input.Notes;
            }
            var ids = c.Attachments.Select(a => a.Id).ToList();
            if (!ids.SequenceEqual(chipIds, StringComparer.Ordinal))
            {
                chipIds = ids;
                Chips.Show([.. c.Attachments.Select(ComposeAttachmentChip.For)]);
            }

            var errors = c.ProblemTexts.ToList();
            if (c.Error.Length > 0)
            {
                errors.Add(c.Error);
            }
            ErrorText.Text = string.Join("\n", errors);
            ErrorRow.Visibility = Show(errors.Count > 0);
            ReloadButton.Visibility = Show(c.ShowsReloadEvent);

            FooterText.Text = string.Join("\n", c.Footer.Select(CalendarDisplay.Clean));
            PrimaryButton.Content = c.PrimaryLabel;
            PrimaryButton.IsEnabled = !c.Saving;
            // Windows-only string: the key name beside the label, as macOS adds " (⌘↩)".
            ToolTipService.SetToolTip(PrimaryButton, c.PrimaryLabel + " (Ctrl+Enter)");
            if (c.DeleteTitle is { } delete)
            {
                DeleteButton.Content = delete;
                DeleteButton.Visibility = Visibility.Visible;
            }
            else
            {
                DeleteButton.Visibility = Visibility.Collapsed;
            }
            DeleteButton.IsEnabled = !c.Saving;
        }
        finally
        {
            rendering = false;
        }
    }

    // The Calendar popup: the writable calendars with their colours, made
    // again only when they change.
    private void RenderCalendars(EventEditorController c)
    {
        var choices = c.Calendars;
        var ids = choices.Select(x => x.Id).ToList();
        var titles = choices.Select(x => x.Title).ToList();
        if (!ids.SequenceEqual(calendarIds) || !titles.SequenceEqual(calendarTitles, StringComparer.Ordinal))
        {
            calendarIds = ids;
            calendarTitles = titles;
            CalendarBox.Items.Clear();
            foreach (var choice in choices)
            {
                var title = CalendarDisplay.Clean(choice.Title);
                var line = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8 };
                line.Children.Add(new Border
                {
                    Width = 10,
                    Height = 10,
                    CornerRadius = new CornerRadius(2.5),
                    VerticalAlignment = VerticalAlignment.Center,
                    Background = new SolidColorBrush(CalendarPalette.Parse(choice.Color)),
                });
                line.Children.Add(new TextBlock { Text = title, MaxLines = 1, TextTrimming = TextTrimming.CharacterEllipsis });
                var item = new ComboBoxItem { Content = line };
                AutomationProperties.SetName(item, title);
                CalendarBox.Items.Add(item);
            }
        }
        var i = c.Input.CalendarId is { } id ? ids.IndexOf(id) : -1;
        if (CalendarBox.SelectedIndex != i)
        {
            CalendarBox.SelectedIndex = i;
        }
        CalendarBox.IsEnabled = choices.Count > 1;
    }

    // fillAlerts: the Alert popup's items for the All Day state (day-based
    // at 9:00 for an all-day event), plus a stored value outside the list.
    private void RenderAlerts(EventEditorController c)
    {
        if (alertsAllDay != c.Input.AllDay || !alertValues.Contains(c.Alert) || AlertBox.Items.Count == 0)
        {
            alertsAllDay = c.Input.AllDay;
            alertValues = [.. c.AlertChoices];
            if (!alertValues.Contains(c.Alert))
            {
                alertValues.Add(c.Alert);
            }
            AlertBox.Items.Clear();
            foreach (var m in alertValues)
            {
                AlertBox.Items.Add(c.AlertTitle(m));
            }
        }
        var i = alertValues.IndexOf(c.Alert);
        if (AlertBox.SelectedIndex != i)
        {
            AlertBox.SelectedIndex = i;
        }
    }

    private void Toast(string text) => ToastHandler?.Invoke(text);

    private void OnTitleChanged(object sender, TextChangedEventArgs e)
    {
        if (!rendering && !closed)
        {
            controller?.SetTitle(TitleBox.Text);
        }
    }

    private void OnLocationChanged(object sender, TextChangedEventArgs e)
    {
        if (!rendering && !closed)
        {
            controller?.SetLocation(LocationBox.Text);
        }
    }

    private void OnNotesChanged(object sender, TextChangedEventArgs e)
    {
        if (!rendering && !closed)
        {
            controller?.SetNotes(NotesBox.Text);
        }
    }

    private static bool IsDown(WinUIKey key) =>
        InputKeyboardSource.GetKeyStateForCurrentThread(key).HasFlag(CoreVirtualKeyStates.Down);

    // Ctrl+Enter in Notes is the primary, not a line break.
    private void OnNotesPreviewKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key == WinUIKey.Enter && IsDown(WinUIKey.Control) && !IsDown(WinUIKey.Shift) && !IsDown(WinUIKey.Menu))
        {
            e.Handled = true;
            Save();
        }
    }

    private void OnCalendarChosen(object sender, SelectionChangedEventArgs e)
    {
        if (rendering || controller is not { } c)
        {
            return;
        }
        var i = CalendarBox.SelectedIndex;
        if (i >= 0 && i < calendarIds.Count)
        {
            c.SetCalendar(calendarIds[i]);
        }
    }

    private void OnAllDayToggled(object sender, RoutedEventArgs e)
    {
        if (!rendering)
        {
            controller?.SetAllDay(AllDaySwitch.IsOn);
        }
    }

    // repeatChosen: Custom… opens the flyout; the popup shows the rule in
    // force until Done applies a new one.
    private void OnRepeatChosen(object sender, SelectionChangedEventArgs e)
    {
        if (rendering || controller is not { } c)
        {
            return;
        }
        var i = RepeatBox.SelectedIndex;
        var choices = EventEditorController.RepeatChoices;
        if (i < 0 || i >= choices.Count)
        {
            return;
        }
        var choice = choices[i];
        if (choice != Cal.Repeat.Custom)
        {
            c.SetRepeat(choice);
            return;
        }
        Render();
        var input = c.Input;
        var view = new CustomRepeatView(c.Recurrence, input.Start, input.Zone, c.Env);
        view.Applied += (_, r) =>
        {
            controller?.SetRecurrence(r);
            RepeatFlyout.Hide();
        };
        RepeatFlyout.Content = view;
        RepeatFlyout.Opened += FocusRepeat;
        RepeatFlyout.ShowAt(RepeatBox);

        void FocusRepeat(object? s, object args)
        {
            RepeatFlyout.Opened -= FocusRepeat;
            view.FocusInterval();
        }
    }

    private void OnRepeatFlyoutClosed(object sender, object e) => Render();

    private void OnAlertChosen(object sender, SelectionChangedEventArgs e)
    {
        if (rendering || controller is not { } c)
        {
            return;
        }
        var i = AlertBox.SelectedIndex;
        if (i >= 0 && i < alertValues.Count)
        {
            c.SetAlert(alertValues[i]);
        }
    }

    private void OnNextSlotClick(object sender, RoutedEventArgs e) => controller?.NextFreeSlot();

    private void OnDeleteClick(object sender, RoutedEventArgs e)
    {
        if (controller is { } c)
        {
            _ = c.DeleteAsync();
        }
    }

    private void OnCancelClick(object sender, RoutedEventArgs e) => CancelRequested?.Invoke(this, EventArgs.Empty);

    private void OnPrimaryClick(object sender, RoutedEventArgs e) => Save();

    private void OnReloadClick(object sender, RoutedEventArgs e) => ReloadRequested?.Invoke(this, EventArgs.Empty);

    // attachFiles: the open dialog over the window, each file imported.
    private async Task AttachFilesAsync()
    {
        if (closed || controller is not { } c || HostWindow is not { } window)
        {
            return;
        }
        IReadOnlyList<string?>? picked;
        try
        {
            picked = await ComposeFileDialog.OpenAsync(WindowPresenter.Handle(window), Cal.Attach, multiple: true);
        }
        catch (Exception e) when (e is System.Runtime.InteropServices.COMException or InvalidCastException or UnauthorizedAccessException)
        {
            LogPickerFailed(logger, e);
            return;
        }
        if (picked is null || closed)
        {
            return;
        }
        foreach (var path in picked)
        {
            if (path is null)
            {
                // An item the shell cannot name by a path (a phone, a virtual folder).
                Toast(L10n.T("Only local files can be attached"));
                continue;
            }
            c.Attach(path);
        }
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "the file dialog of an event editor failed")]
    private static partial void LogPickerFailed(ILogger logger, Exception error);
}
