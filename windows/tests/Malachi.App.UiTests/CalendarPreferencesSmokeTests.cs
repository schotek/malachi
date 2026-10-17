// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The smoke tests of Preferences → Calendar and the Add CalDAV Calendar
// window (docs/calendar.md §5.8, docs/windows-port.md §12) over the
// published app with the calendar's samples (MALACHI_CALENDAR_SAMPLES=1,
// CalendarSmokeFixture): the Calendar page shows the group Display and a
// section per sample account with the calendar capability (Work, M365, with
// its sync switch and the Invitations rows; Gmail; Nextcloud, CALDAV, with
// its server and Remove Account), the Accounts page lists no CalDAV account
// and its "+" menu offers no Add CalDAV Calendar… (docs/calendar.md §5.8,
// as macOS); the Calendar page's Add CalDAV Calendar… opens the window,
// which Cancel closes, and Discover Calendars over the samples is refused
// with the preview's "The preview cannot do that." in the window's error
// bar. Elements are found by AutomationId (the names follow the user's
// language); the accounts by the samples' ids, which are data.

using System;
using System.Linq;
using System.Windows.Automation;
using Malachi.Core.Calendars;
using Xunit;

namespace Malachi.App.UiTests;

/// <summary>Preferences → Calendar and the Add CalDAV Calendar window over the calendar's samples.</summary>
[Collection(OneAppAtATime.Name)]
public sealed class CalendarPreferencesSmokeTests(CalendarSmokeFixture fixture) : IClassFixture<CalendarSmokeFixture>
{
    // The X key: types a character into the password box when UI
    // Automation cannot set it.
    private const byte VkX = 0x58;

    private AppSession App
    {
        get
        {
            Assert.SkipWhen(fixture.SkipReason is not null, fixture.SkipReason ?? "");
            return fixture.Session;
        }
    }

    // The Escape key: closes an open menu.
    private const byte VkEscape = 0x1B;

    // The main window in any mode (its title bar's switch).
    private AutomationElement Main => Uia.WindowWith(App.ProcessId, "ModeMail", AppSession.StartTimeout);

    [Fact]
    public void TheCalendarPageShowsDisplayAndTheSampleAccounts()
    {
        var preferences = OpenPreferences();
        try
        {
            Uia.Select(Uia.Find(preferences, "CalendarPage"));
            var page = Uia.Find(preferences, "PrefsCalendar");
            Assert.NotNull(Uia.Find(page, "PrefsCalendarDisplay"));
            var weekStart = Uia.Find(page, "PrefsCalendarWeekStart");
            Uia.WaitFor(() => weekStart.Current.IsEnabled, "the group Display to know the preferences");
            Assert.NotNull(Uia.Find(page, "PrefsCalendarDuration"));
            Assert.NotNull(Uia.Find(page, "PrefsCalendarAlert"));
            Assert.NotNull(Uia.Find(page, "PrefsCalendarDeclined"));
            Assert.NotNull(Uia.Find(page, "PrefsCalendarWorkStart"));
            Assert.NotNull(Uia.Find(page, "PrefsCalendarWorkEnd"));
            Assert.NotNull(Uia.Find(page, "PrefsCalendarDay:MO"));

            // A Microsoft 365 account: its sync switch and the Invitations rows, no Remove Account.
            var work = Uia.Find(page, "PrefsCalendarAccount:" + Cal.SampleWork.Value);
            Assert.Equal("M365", Uia.Find(work, "PrefsCalendarAccountKind").Current.Name);
            Assert.NotNull(Uia.Find(work, "PrefsCalendarSync"));
            Assert.NotNull(Uia.Find(work, "PrefsCalendarCard"));
            Assert.NotNull(Uia.Find(work, "PrefsCalendarKeep"));
            Assert.NotNull(Uia.Find(work, "PrefsCalendarCalendar:0"));
            Assert.Null(Uia.TryFind(work, "PrefsCalendarRemove"));

            Assert.NotNull(Uia.Find(page, "PrefsCalendarAccount:" + Cal.SampleGmail.Value));

            // A CalDAV account: its connection and Remove Account, no sync switch, no Invitations.
            var nextcloud = Uia.Find(page, "PrefsCalendarAccount:" + Cal.SampleNextcloud.Value);
            Assert.Equal("CALDAV", Uia.Find(nextcloud, "PrefsCalendarAccountKind").Current.Name);
            Assert.NotNull(Uia.Find(nextcloud, "PrefsCalendarAccountName"));
            Assert.NotNull(Uia.Find(nextcloud, "PrefsCalendarServer"));
            Assert.NotNull(Uia.Find(nextcloud, "PrefsCalendarReplacePassword"));
            Assert.NotNull(Uia.Find(nextcloud, "PrefsCalendarMailAccount"));
            Assert.NotNull(Uia.Find(nextcloud, "PrefsCalendarRemove"));
            Assert.Null(Uia.TryFind(nextcloud, "PrefsCalendarSync"));
            Assert.Null(Uia.TryFind(nextcloud, "PrefsCalendarCard"));

            // Accounts with calendars: no empty state.
            Assert.Null(Uia.TryFind(page, "PrefsCalendarEmpty"));
            Assert.NotNull(Uia.Find(page, "PrefsCalendarAddCalDAV"));
        }
        finally
        {
            ClosePreferences(preferences);
        }
    }

    [Fact]
    public void TheAccountsPageOffersNoCalDAVAndTheCalendarPageOpensItsWindow()
    {
        var preferences = OpenPreferences();
        try
        {
            Uia.Select(Uia.Find(preferences, "AccountsPage"));
            var list = Uia.Find(preferences, "AccountList");
            var add = Uia.Find(preferences, "AddAccount");
            Uia.WaitFor(() => add.Current.IsEnabled, "the Accounts page to load");
            // The sample Nextcloud is the calendar's: never a row of this page.
            Assert.DoesNotContain(
                Uia.All(list, ControlType.ListItem).Cast<AutomationElement>(),
                row => row.Current.Name.Contains("Nextcloud", StringComparison.Ordinal));

            // The "+" menu: the two assistants and no Add CalDAV Calendar… item.
            Uia.Invoke(add);
            Assert.NotNull(Uia.Find(preferences, "AddMailAccount"));
            var jira = Uia.Find(preferences, "AddJiraAccount");
            Assert.Null(Uia.TryFind(preferences, "AddCalDAVCalendar"));
            Uia.PressKey(jira, App.ProcessId, VkEscape);
            Uia.WaitFor(() => Uia.TryFind(preferences, "AddJiraAccount") is null, "Escape to close the menu");

            // The window is the Calendar page's.
            Uia.Select(Uia.Find(preferences, "CalendarPage"));
            Uia.Invoke(Uia.Find(preferences, "PrefsCalendarAddCalDAV"));
            var wizard = Uia.WindowWith(App.ProcessId, "CalDAVWizard");
            Assert.NotNull(Uia.Find(wizard, "CalDAVName"));
            Assert.NotNull(Uia.Find(wizard, "CalDAVServer"));
            Assert.NotNull(Uia.Find(wizard, "CalDAVUsername"));
            Assert.NotNull(Uia.Find(wizard, "CalDAVPassword"));
            Assert.False(Uia.Find(wizard, "CalDAVDiscover").Current.IsEnabled);
            Assert.False(Uia.Find(wizard, "CalDAVAdd").Current.IsEnabled);
            Uia.Invoke(Uia.Find(wizard, "CalDAVCancel"));
            Uia.WaitGone(wizard, what: "the Add CalDAV Calendar window");
        }
        finally
        {
            ClosePreferences(preferences);
        }
    }

    [Fact]
    public void DiscoverOverTheSamplesSaysThePreviewCannotDoThat()
    {
        var preferences = OpenPreferences();
        try
        {
            Uia.Select(Uia.Find(preferences, "CalendarPage"));
            Uia.Invoke(Uia.Find(preferences, "PrefsCalendarAddCalDAV"));
            var wizard = Uia.WindowWith(App.ProcessId, "CalDAVWizard");
            try
            {
                var discover = Uia.Find(wizard, "CalDAVDiscover");
                Assert.False(discover.Current.IsEnabled);
                Uia.SetValue(Uia.Find(wizard, "CalDAVServer"), "cloud.example.org");
                Uia.SetValue(Uia.Find(wizard, "CalDAVUsername"), "me");
                TypePassword(Uia.Find(wizard, "CalDAVPassword"));
                Uia.WaitFor(() => discover.Current.IsEnabled, "Discover Calendars to be offered");
                Uia.Invoke(discover);
                // The samples refuse every call: "The preview cannot do that."
                Assert.NotNull(Uia.Find(wizard, "CalDAVError"));
                Assert.Null(Uia.TryFind(wizard, "CalDAVCalendars"));
                Assert.False(Uia.Find(wizard, "CalDAVAdd").Current.IsEnabled);
            }
            finally
            {
                if (!Uia.IsGone(wizard))
                {
                    Uia.Invoke(Uia.Find(wizard, "CalDAVCancel"));
                    Uia.WaitGone(wizard, what: "the Add CalDAV Calendar window");
                }
            }
        }
        finally
        {
            ClosePreferences(preferences);
        }
    }

    // The Preferences from the primary menu, which the mail's sidebar holds.
    private AutomationElement OpenPreferences()
    {
        var main = Main;
        if (Uia.TryFind(main, "MainMenuButton") is null)
        {
            Uia.Toggle(Uia.Find(main, "ModeMail"));
            Uia.Find(main, "MainMenuButton");
        }
        App.MenuItem("MenuPreferences");
        return Uia.WindowWith(App.ProcessId, "PreferencesNavigation");
    }

    private static void ClosePreferences(AutomationElement preferences)
    {
        if (!Uia.IsGone(preferences))
        {
            Uia.Close(preferences);
            Uia.WaitGone(preferences, what: "the Preferences");
        }
    }

    // A password box's text: its value when UI Automation may set it, else a
    // key typed into it.
    private void TypePassword(AutomationElement field)
    {
        if (field.TryGetCurrentPattern(ValuePattern.Pattern, out var pattern) && pattern is ValuePattern value && !value.Current.IsReadOnly)
        {
            try
            {
                value.SetValue("secret");
                return;
            }
            catch (InvalidOperationException)
            {
                // A password box that refuses it: typed below.
            }
        }
        Uia.PressKey(field, App.ProcessId, VkX);
    }
}
