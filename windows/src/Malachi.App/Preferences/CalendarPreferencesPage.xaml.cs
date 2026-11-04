// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/CalendarPaneViewController.swift
// (bind, snapshotChanged, renderSections, addCalDAVClicked,
// signInForCalendar, renderDisplay, fill, displayChanged, workDayToggled;
// removeAccount is the section's, CalendarAccountSection); GTK: none yet
// (Swift-first, the structure and texts are ui/internal/calendar/
// preferences.go, Core's Cal.Prefs).
//
// Preferences → Calendar (docs/calendar.md §5.8, "As built (M9)") over the
// Preferences window's own calendar source (AppState.StartedCalendarSourceFor
// the window, bound the first time the page comes up, as macOS binds on its
// first appearance; started then, stopped and disposed with the window):
//
// - Display: the daemon's calendar.preferences through
//   CalendarPreferencesController, usable once the source has data. A
//   choice shows at once and is written; the controller takes a refused
//   write back and says why under the heading, and refuses some changes
//   itself without a word (the last working day stays on, a start not
//   before the end), so every handler draws the group again after it.
// - A section per account with the calendar capability, in the accounts'
//   order, as Cal.Prefs.PreferencesLayout lists them over the source's
//   snapshot; a section is kept while its account stays and made or
//   dropped as accounts come and go (after Add CalDAV Calendar… the window's
//   snapshot is asked anew and the new account's section appears), so the
//   keyboard stays where it was. When the section that had the keyboard
//   goes (Remove Account), it moves to Add CalDAV Calendar….
// - The empty state "No calendars yet." with its advice, once the source
//   has data and no account has the capability.
// - Add CalDAV Calendar…: the modal window over the Preferences
//   (AppState.ShowAddCalDAVCalendar, over this window's source).
//
// The consent's Sign In… asks account.oauthStart {accountId, calendar:
// true} (CalendarAccountController.RequestSignInUrlAsync) and opens the page
// in the browser, as the mail's re-sign-in does (Main/AccountRepair.cs); a
// failure is a toast over the Preferences.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;

namespace Malachi.App.Preferences;

/// <summary>The Calendar page of the preferences.</summary>
[System.Diagnostics.CodeAnalysis.SuppressMessage("Design", "CA1001", Justification = "The window closes the page's controllers (Close).")]
public sealed partial class CalendarPreferencesPage : UserControl
{
    private readonly AppState state;
    private readonly Window window;
    private readonly IToasts toasts;

    // The sections by account, kept while their accounts stay.
    private readonly Dictionary<AccountId, CalendarAccountSection> sections = [];

    // The working days' toggles, in WorkingDayChoices' order.
    private readonly List<ToggleButton> dayButtons = [];

    // The choices each popup lists now.
    private IReadOnlyList<CalendarPreferencesController.Choice<WeekStart>> weekStarts = [];
    private IReadOnlyList<CalendarPreferencesController.Choice<int>> durations = [];
    private IReadOnlyList<CalendarPreferencesController.Choice<int>> alerts = [];
    private IReadOnlyList<CalendarPreferencesController.Choice<string>> starts = [];
    private IReadOnlyList<CalendarPreferencesController.Choice<string>> ends = [];

    private ICalendarDataSource? source;
    private CalendarPreferencesController? preferences;
    private BoardObserverToken? token;

    // The widgets are being set from the controller: their events are not the user's.
    private bool syncing;
    private bool closed;

    /// <summary>The page of the preferences window <paramref name="window"/>.</summary>
    internal CalendarPreferencesPage(AppState state, Window window, IToasts toasts)
    {
        this.state = state;
        this.window = window;
        this.toasts = toasts;
        InitializeComponent();
        DisplayHeading.Text = Cal.Prefs.DisplayGroup;
        AutomationProperties.SetName(DisplayGroup, Cal.Prefs.DisplayGroup);
        Label(WeekStartRow, WeekStartBox, Cal.Prefs.WeekStartSetting);
        Label(DurationRow, DurationBox, Cal.Prefs.DurationSetting);
        Label(AlertRow, AlertBox, Cal.Prefs.AlertSetting);
        Label(DeclinedRow, DeclinedSwitch, Cal.Prefs.DeclinedSetting);
        WorkingHoursRow.Header = Cal.Prefs.WorkingHoursSetting;
        WorkingHoursRow.Description = Cal.Prefs.WorkingHoursSubtitle;
        AutomationProperties.SetName(WorkStartBox, Cal.Prefs.WorkingHoursSetting);
        AutomationProperties.SetName(WorkEndBox, Cal.Prefs.WorkingHoursSetting);
        EmptyTitle.Text = Cal.Prefs.NoCalendarsTitle;
        EmptyBody.Text = Cal.Prefs.NoCalendarsBody;
        AddCalDAVButton.Content = Cal.Prefs.AddCalDAV;
    }

    /// <summary>
    /// The page came up (the window's navigation): bound to the window's
    /// calendar source the first time, then drawn from what it knows.
    /// </summary>
    public void Refresh()
    {
        if (closed)
        {
            return;
        }
        Bind();
        SnapshotChanged();
    }

    /// <summary>The window closed: everything stops, late answers are dropped.</summary>
    public void Close()
    {
        if (closed)
        {
            return;
        }
        closed = true;
        token?.Cancel();
        token = null;
        preferences?.Dispose();
        foreach (var section in sections.Values)
        {
            section.Stop();
        }
        sections.Clear();
    }

    private static void Label(CommunityToolkit.WinUI.Controls.SettingsCard row, Control control, string title)
    {
        row.Header = title;
        AutomationProperties.SetName(control, title);
    }

    // bind: the window's source, the Display group's controller and the day toggles.
    private void Bind()
    {
        if (source is not null)
        {
            return;
        }
        var s = state.StartedCalendarSourceFor(window);
        source = s;
        var p = new CalendarPreferencesController(s, AppState.CalendarEnv());
        preferences = p;
        p.Changed += (_, _) => RenderDisplay();
        p.Start();
        foreach (var day in p.WorkingDayChoices)
        {
            var button = new ToggleButton { Content = day.Title, Tag = day.Value, MinWidth = 44 };
            AutomationProperties.SetName(button, day.Title);
            AutomationProperties.SetAutomationId(button, "PrefsCalendarDay:" + day.Value);
            button.Click += OnDayClick;
            dayButtons.Add(button);
            WorkingDays.Children.Add(button);
        }
        token = s.Observe(change =>
        {
            if (change is Cal.SourceChange.SnapshotChanged)
            {
                SnapshotChanged();
            }
        });
    }

    private void SnapshotChanged()
    {
        if (closed)
        {
            return;
        }
        RenderDisplay();
        RenderSections();
    }

    // Accounts

    // A section per account with the calendar capability, in the accounts'
    // order (PreferencesLayout); kept while its account stays.
    private void RenderSections()
    {
        if (source is not { } s)
        {
            return;
        }
        var snap = s.Snapshot;
        var layout = Cal.Prefs.PreferencesLayout(snap.Accounts);
        var accounts = layout.Where(x => x.Kind == Cal.PrefsSectionKind.Account && x.AccountId is not null).ToList();
        EmptyGroup.Visibility = layout.Any(x => x.Kind == Cal.PrefsSectionKind.Empty) && snap.Phase != Cal.Phase.Loading
            ? Visibility.Visible
            : Visibility.Collapsed;
        var ids = accounts.Select(x => x.AccountId!.Value).ToList();
        var focusLost = false;
        foreach (var (id, section) in sections.ToList())
        {
            if (ids.Contains(id))
            {
                continue;
            }
            focusLost |= HasFocus(section);
            section.Stop();
            SectionsHost.Children.Remove(section);
            sections.Remove(id);
        }
        foreach (var x in accounts)
        {
            if (sections.TryGetValue(x.AccountId!.Value, out var kept))
            {
                kept.Apply(x);
                continue;
            }
            var section = new CalendarAccountSection(state, window, s, x);
            section.Controller.SignInRequested += (_, _) => _ = SignInAsync(section.Controller);
            section.Controller.AddCalDAVRequested += (_, _) => AddCalDAV();
            section.Controller.ToastRequested += (_, text) => toasts.Show(text);
            sections[x.AccountId!.Value] = section;
            section.Start();
        }
        // New sections go in at their places; only a section that moved is
        // taken out and put back (the accounts were reordered).
        var children = SectionsHost.Children;
        for (var i = 0; i < ids.Count; i++)
        {
            var wanted = sections[ids[i]];
            if (i < children.Count && ReferenceEquals(children[i], wanted))
            {
                continue;
            }
            var at = children.IndexOf(wanted);
            if (at >= 0)
            {
                focusLost |= HasFocus(wanted);
                children.RemoveAt(at);
            }
            children.Insert(i, wanted);
        }
        if (focusLost)
        {
            AddCalDAVButton.Focus(FocusState.Programmatic);
        }
    }

    // Whether the keyboard is inside element.
    private bool HasFocus(DependencyObject element)
    {
        if (XamlRoot is null)
        {
            return false;
        }
        for (var e = FocusManager.GetFocusedElement(XamlRoot) as DependencyObject; e is not null; e = VisualTreeHelper.GetParent(e))
        {
            if (ReferenceEquals(e, element))
            {
                return true;
            }
        }
        return false;
    }

    private void OnAddCalDAVClick(object sender, RoutedEventArgs e) => AddCalDAV();

    // addCalDAVClicked: the window over the Preferences and this window's
    // source; it asks the source anew once it added the account.
    private void AddCalDAV()
    {
        if (!closed)
        {
            state.ShowAddCalDAVCalendar(window, null);
        }
    }

    // signInForCalendar: the sign-in's page in the browser, or why not.
    private async Task SignInAsync(CalendarAccountController controller)
    {
        var outcome = await controller.RequestSignInUrlAsync();
        if (closed)
        {
            return;
        }
        switch (outcome)
        {
            case SignInUrl.Open open:
                try
                {
                    await state.Launcher.OpenUrlAsync(open.Url, WindowPresenter.Handle(window));
                }
                catch (Exception e) when (e is ArgumentException or InvalidOperationException or System.ComponentModel.Win32Exception)
                {
                    var detail = e is ArgumentException ? "not an https address" : e.Message; // Windows-only string (the technical detail, as in GTK)
                    // TRANSLATORS: %s is a technical error message.
                    toasts.Show(L10n.T("The link could not be opened: %s", detail));
                }
                break;
            case SignInUrl.Failed failed:
                toasts.Show(failed.Text);
                break;
        }
    }

    // Display (calendar.preferences)

    private void RenderDisplay()
    {
        if (closed || preferences is not { } c)
        {
            return;
        }
        syncing = true;
        try
        {
            // Usable once the source has data (the samples' at once).
            var phase = c.Source.Snapshot.Phase;
            var usable = phase is not (Cal.Phase.Loading or Cal.Phase.Unsupported);
            foreach (var row in (CommunityToolkit.WinUI.Controls.SettingsCard[])[WeekStartRow, DurationRow, AlertRow, DeclinedRow, WorkingHoursRow, WorkingDaysRow])
            {
                row.IsEnabled = usable;
            }
            DisplayError.Text = c.Error;
            DisplayError.Visibility = c.Error.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
            DisplaySaving.IsActive = c.Saving;
            DisplaySaving.Visibility = c.Saving ? Visibility.Visible : Visibility.Collapsed;
            var p = c.Preferences;
            weekStarts = c.WeekStartChoices;
            Fill(WeekStartBox, weekStarts.Select(x => x.Title), IndexWhere(weekStarts, x => x.Value == p.WeekStart));
            durations = c.DurationChoices;
            Fill(DurationBox, durations.Select(x => x.Title), IndexWhere(durations, x => x.Value == p.DefaultDurationMinutes));
            alerts = c.AlertChoices;
            Fill(AlertBox, alerts.Select(x => x.Title), IndexWhere(alerts, x => x.Value == p.DefaultAlertMinutes));
            DeclinedSwitch.IsOn = p.ShowDeclined;
            starts = c.WorkingTimeChoices(p.WorkingHours.Start);
            ends = c.WorkingTimeChoices(p.WorkingHours.End);
            Fill(WorkStartBox, starts.Select(x => x.Title), IndexWhere(starts, x => x.Value == p.WorkingHours.Start));
            Fill(WorkEndBox, ends.Select(x => x.Title), IndexWhere(ends, x => x.Value == p.WorkingHours.End));
            var days = c.WorkingDayChoices;
            for (var i = 0; i < dayButtons.Count && i < days.Count; i++)
            {
                dayButtons[i].IsChecked = c.IsWorkingDay(days[i].Value);
            }
            WorkingDaysText.Text = c.WorkingHoursText;
        }
        finally
        {
            syncing = false;
        }
    }

    private static int IndexWhere<T>(IReadOnlyList<T> list, Func<T, bool> match)
    {
        for (var i = 0; i < list.Count; i++)
        {
            if (match(list[i]))
            {
                return i;
            }
        }
        return -1;
    }

    // fill: a popup's titles (replaced only when they changed) and selection.
    private static void Fill(ComboBox box, IEnumerable<string> titles, int index)
    {
        var wanted = titles.ToList();
        var shown = box.Items.OfType<ComboBoxItem>().Select(i => i.Content as string ?? "").ToList();
        if (!shown.SequenceEqual(wanted))
        {
            box.Items.Clear();
            foreach (var t in wanted)
            {
                // A string content is shown by a TextBlock's Text, never parsed.
                box.Items.Add(new ComboBoxItem { Content = t });
            }
        }
        box.SelectedIndex = index;
    }

    // displayChanged: the chosen value, then the group again (a refused
    // choice, an end before the start, shows the stored one).
    private void Pick<T>(ComboBox box, IReadOnlyList<CalendarPreferencesController.Choice<T>> choices, Action<T> set)
    {
        if (syncing || preferences is null)
        {
            return;
        }
        var i = box.SelectedIndex;
        if (i >= 0 && i < choices.Count)
        {
            set(choices[i].Value);
        }
        RenderDisplay();
    }

    private void OnWeekStartChanged(object sender, SelectionChangedEventArgs e) =>
        Pick(WeekStartBox, weekStarts, v => preferences?.SetWeekStart(v));

    private void OnDurationChanged(object sender, SelectionChangedEventArgs e) =>
        Pick(DurationBox, durations, v => preferences?.SetDefaultDuration(v));

    private void OnAlertChanged(object sender, SelectionChangedEventArgs e) =>
        Pick(AlertBox, alerts, v => preferences?.SetDefaultAlert(v));

    private void OnWorkStartChanged(object sender, SelectionChangedEventArgs e) =>
        Pick(WorkStartBox, starts, v => preferences?.SetWorkingStart(v));

    private void OnWorkEndChanged(object sender, SelectionChangedEventArgs e) =>
        Pick(WorkEndBox, ends, v => preferences?.SetWorkingEnd(v));

    private void OnDeclinedToggled(object sender, RoutedEventArgs e)
    {
        if (syncing || preferences is not { } c)
        {
            return;
        }
        c.SetShowDeclined(DeclinedSwitch.IsOn);
        RenderDisplay();
    }

    // workDayToggled: the day on or off; the last working day stays on.
    private void OnDayClick(object sender, RoutedEventArgs e)
    {
        if (syncing || preferences is not { } c || sender is not ToggleButton { Tag: string code } button)
        {
            return;
        }
        c.SetWorkingDay(code, button.IsChecked == true);
        RenderDisplay();
    }
}
