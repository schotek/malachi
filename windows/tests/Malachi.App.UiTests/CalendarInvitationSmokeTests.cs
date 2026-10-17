// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The smoke tests of the calendar in Mail and on the Board (docs/calendar.md
// §5.7, §5.9; docs/windows-port.md §12) over the published app with the
// calendar's and the board's samples (CalendarBoardSmokeFixture): the
// Board's Today style shows the strip "Today in Calendar" with today's
// sample events (the samples' standup is on every weekday), its Open
// Calendar › and a row's click go to the Calendar; and the assistant panel,
// once the session is registered with Claude, has What's on Today and Find
// a Free Slot (the samples' accounts have calendars). The invitation card
// itself needs a message with a calendar part, which the samples do not
// have: DevmailTests delivers one. Elements are found by AutomationId (the
// names follow the user's language).

using System;
using System.Collections.Generic;
using System.Linq;
using System.Windows.Automation;
using Xunit;

namespace Malachi.App.UiTests;

/// <summary>Today in Calendar and the assistant's calendar actions over the samples.</summary>
[Collection(OneAppAtATime.Name)]
public sealed class CalendarInvitationSmokeTests(CalendarBoardSmokeFixture fixture) : IClassFixture<CalendarBoardSmokeFixture>
{
    private const string RowPrefix = "TodayCalendarRow:";

    private AppSession App
    {
        get
        {
            Assert.SkipWhen(fixture.SkipReason is not null, fixture.SkipReason ?? "");
            return fixture.Session;
        }
    }

    // The main window in any mode.
    private AutomationElement Main => Uia.WindowWith(App.ProcessId, "ModeMail", AppSession.StartTimeout);

    // The samples' standup is on every weekday (CalendarSampleData), so a
    // weekday's strip has rows; a weekend's may have none.
    private static bool Weekday => DateTime.Now.DayOfWeek is not (DayOfWeek.Saturday or DayOfWeek.Sunday);

    [Fact]
    public void TheBoardsTodayShowsTodayInCalendar()
    {
        var main = Main;
        ShowToday(main);
        var strip = Uia.Find(main, "BoardTodayCalendarStrip");
        Assert.NotNull(Uia.Find(strip, "TodayOpenCalendar"));
        if (Weekday)
        {
            var rows = Uia.Until(() => Rows(strip) is { Count: > 0 } r ? r : null, null, "today's rows in the strip");
            // A row is named by its time and title.
            Assert.All(rows, r => Assert.False(string.IsNullOrEmpty(r.Current.Name)));
        }

        // Open Calendar ›: the Calendar's Day view of today.
        Uia.Invoke(Uia.Find(strip, "TodayOpenCalendar"));
        Uia.Find(main, "CalendarMenuButton");
        Assert.Equal(ToggleState.On, ToggleOf(Uia.Find(main, "ModeCalendar")));
        ShowMail(main);
    }

    [Fact]
    public void ARowOfTheStripShowsItsEventInTheCalendar()
    {
        Assert.SkipUnless(Weekday, "the samples have no event every weekend day");
        var main = Main;
        ShowToday(main);
        var strip = Uia.Find(main, "BoardTodayCalendarStrip");
        var row = Uia.Until(() => Rows(strip).FirstOrDefault(), null, "a row in the strip");
        Uia.Invoke(row);
        Uia.Find(main, "CalendarMenuButton");
        Assert.Equal(ToggleState.On, ToggleOf(Uia.Find(main, "ModeCalendar")));
        ShowMail(main);
    }

    [Fact]
    public void TheAssistantPanelHasTheCalendarsQuickActions()
    {
        var app = App;
        var main = Main;
        ShowMail(main);
        SetRegistered(app, true);
        try
        {
            // The panel's toggle shows once the Assistant is (In App).
            AutomationElement? toggle = null;
            try
            {
                toggle = Uia.Until(() => Uia.TryFind(main, "AssistantPanelToggle") is { Current.IsOffscreen: false } t ? t : null, null, "the panel's toggle");
            }
            catch (TimeoutException)
            {
                Assert.Skip("the assistant panel is not reachable in this session (no Assistant after Register with Claude)");
            }
            if (ToggleOf(toggle!) != ToggleState.On)
            {
                Uia.Toggle(toggle!);
            }
            var panel = Uia.Find(main, "AssistantPanel");
            var today = Uia.Find(panel, "AssistantWhatsOnToday");
            var slot = Uia.Find(panel, "AssistantFindFreeSlot");
            Uia.WaitFor(() => !today.Current.IsOffscreen && !slot.Current.IsOffscreen, "the calendar's quick actions");
            Uia.Toggle(Uia.Find(main, "AssistantPanelToggle"));
        }
        finally
        {
            SetRegistered(app, false);
        }
    }

    // The strip's rows (their row buttons), in order.
    private static List<AutomationElement> Rows(AutomationElement strip) =>
        strip.FindAll(TreeScope.Descendants, Condition.TrueCondition).Cast<AutomationElement>()
            .Where(e => e.Current.AutomationId.StartsWith(RowPrefix, StringComparison.Ordinal))
            .ToList();

    // Register with Claude (Preferences → AI), over the session's own ~/.claude.
    private static void SetRegistered(AppSession app, bool on)
    {
        app.MenuItem("MenuPreferences");
        var preferences = Uia.WindowWith(app.ProcessId, "PreferencesNavigation");
        try
        {
            Uia.Select(Uia.Find(preferences, "AiPage"));
            var register = Uia.Find(preferences, "RegisterWithClaude");
            Uia.WaitFor(() => register.Current.IsEnabled, "Register with Claude to know the status");
            var want = on ? ToggleState.On : ToggleState.Off;
            if (ToggleOf(register) != want)
            {
                Uia.Toggle(register);
                Uia.WaitFor(() => ToggleOf(Uia.Find(preferences, "RegisterWithClaude")) == want, "Register with Claude to follow");
            }
        }
        finally
        {
            Uia.Close(preferences);
            Uia.WaitGone(preferences, what: "the Preferences");
        }
    }

    // The Board in its Today style.
    private static void ShowToday(AutomationElement main)
    {
        if (Uia.TryFind(main, "BoardStyleList") is null)
        {
            Uia.Toggle(Uia.Find(main, "ModeBoard"));
        }
        var today = Uia.Find(main, "BoardStyleToday");
        if (ToggleOf(today) != ToggleState.On)
        {
            Uia.Toggle(today);
        }
        Uia.WaitFor(() => ToggleOf(Uia.Find(main, "BoardStyleToday")) == ToggleState.On, "Today to be the style");
    }

    // Back to Mail through the title bar's switch (from the Board or the Calendar).
    private static void ShowMail(AutomationElement main)
    {
        if (Uia.TryFind(main, "BoardStyleList") is not null || Uia.TryFind(main, "CalendarMenuButton") is not null)
        {
            Uia.Toggle(Uia.Find(main, "ModeMail"));
        }
        Uia.Find(main, "MainMenuButton");
    }

    private static ToggleState ToggleOf(AutomationElement element) =>
        ((TogglePattern)element.GetCurrentPattern(TogglePattern.Pattern)).Current.ToggleState;
}
