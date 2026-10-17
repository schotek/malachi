// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The calendar page's smoke tests (docs/windows-port.md §12, docs/calendar.md
// §6.4's verification) over the published app with the calendar's sample
// data (MALACHI_CALENDAR_SAMPLES=1, InMemoryCalendarSource over
// CalendarSamples; CalendarSmokeFixture): Ctrl+3 and the title bar's switch
// into the Calendar, its bar and sidebar, the four views, Today and ‹ ›
// moving the title, a click on a block selecting it, the sidebar's
// Unanswered row showing the agenda and a view leaving it, and the title
// bar's box searching the events (Escape there ends the search). Events
// are found by their sample titles, which are data, never translated;
// everything else by AutomationId. The other calendar views' smoke tests
// share the fixture.

using System;
using System.Linq;
using System.Windows.Automation;
using Xunit;

namespace Malachi.App.UiTests;

/// <summary>The calendar page over the sample data.</summary>
[Collection(OneAppAtATime.Name)]
public sealed class CalendarSmokeTests(CalendarSmokeFixture fixture) : IClassFixture<CalendarSmokeFixture>
{
    // A sample title (CalendarSampleData.cs): the recurring standup of every weekday.
    private const string Standup = "Standup";

    private const byte VkControl = 0x11;
    private const byte VkEscape = 0x1B;
    private const byte VkThree = 0x33;

    private AppSession App
    {
        get
        {
            Assert.SkipWhen(fixture.SkipReason is not null, fixture.SkipReason ?? "");
            return fixture.Session;
        }
    }

    // The main window in any mode (AppSession.MainWindow's New Message is
    // the mail's sidebar, hidden while the calendar shows).
    private AutomationElement Main => Uia.WindowWith(App.ProcessId, "ModeMail", AppSession.StartTimeout);

    [Fact]
    public void CtrlThreeShowsTheCalendar()
    {
        var main = Main;
        ShowMail(main);
        PressChord(Uia.Find(main, "ModeMail"), App.ProcessId, VkControl, VkThree);
        Uia.Find(main, "CalendarBar");
        Assert.Equal(ToggleState.On, ToggleOf(Uia.Find(main, "ModeCalendar")));
        Uia.WaitFor(() => Uia.TryFind(main, "MainMenuButton") is null, "the mail's sidebar to hide");
    }

    [Fact]
    public void TheSwitchShowsThePageWithItsSidebar()
    {
        var main = ShowCalendar();
        Assert.NotNull(Uia.Find(main, "CalendarSidebar"));
        Assert.NotNull(Uia.Find(main, "CalendarMiniMonth"));
        Assert.NotNull(Uia.Find(main, "CalendarUnanswered"));
        Assert.NotNull(Uia.Find(main, "CalendarToday"));
        Assert.NotNull(Uia.Find(main, "CalendarNewEvent"));
        // The search box stays: it searches the events here.
        Assert.NotNull(Uia.Find(main, "SearchBox"));
    }

    [Fact]
    public void TheFourViewsAppear()
    {
        var main = ShowCalendar();
        SetView(main, "CalendarViewDay");
        Uia.Find(main, "CalendarWeekView");
        SetView(main, "CalendarViewMonth");
        Uia.Find(main, "CalendarMonthView");
        Uia.WaitFor(() => Uia.TryFind(main, "CalendarWeekView") is null, "the week view to give way to the month");
        SetView(main, "CalendarViewAgenda");
        Uia.Find(main, "CalendarAgendaView");
        SetView(main, "CalendarViewWeek");
        Uia.Find(main, "CalendarWeekView");
        Uia.WaitFor(() => Uia.TryFind(main, "CalendarAgendaView") is null, "the agenda to give way to the week");
    }

    [Fact]
    public void TodayAndTheArrowsChangeTheTitle()
    {
        var main = ShowCalendar();
        SetView(main, "CalendarViewDay");
        Uia.Invoke(Uia.Find(main, "CalendarToday"));
        var today = Title(main);
        Uia.Invoke(Uia.Find(main, "CalendarNext"));
        Uia.WaitFor(() => Title(main) != today, "› to show the next day");
        Uia.Invoke(Uia.Find(main, "CalendarPrevious"));
        Uia.WaitFor(() => Title(main) == today, "‹ to show today again");
        Uia.Invoke(Uia.Find(main, "CalendarPrevious"));
        Uia.Invoke(Uia.Find(main, "CalendarPrevious"));
        Uia.WaitFor(() => Title(main) != today, "‹ to show an earlier day");
        Uia.Invoke(Uia.Find(main, "CalendarToday"));
        Uia.WaitFor(() => Title(main) == today, "Today to show today");
        SetView(main, "CalendarViewWeek");
    }

    [Fact]
    public void AClickOnABlockSelectsIt()
    {
        var main = ShowCalendar();
        SetView(main, "CalendarViewWeek");
        Uia.Invoke(Uia.Find(main, "CalendarToday"));
        var week = Uia.Find(main, "CalendarWeekView");
        var block = Uia.Until(() => Blocks(week).FirstOrDefault(b => b.Current.Name.Contains(Standup, StringComparison.Ordinal)), null, "a block of the week");
        Uia.Invoke(block);
        Uia.WaitFor(() => IsSelected(block), "the block to be selected");
        // One selection: another block clicked takes it.
        var other = Blocks(week).FirstOrDefault(b => b.Current.AutomationId != block.Current.AutomationId);
        if (other is not null)
        {
            Uia.Invoke(other);
            Uia.WaitFor(() => IsSelected(other) && !IsSelected(block), "the other block to take the selection");
        }
    }

    [Fact]
    public void TheUnansweredRowShowsTheAgenda()
    {
        var main = ShowCalendar();
        SetView(main, "CalendarViewWeek");
        Uia.Invoke(Uia.Find(main, "CalendarUnanswered"));
        Uia.Find(main, "CalendarAgendaView");
        Assert.NotNull(Uia.Find(main, "CalendarListHeading"));
        Uia.WaitFor(() => Uia.TryFind(main, "CalendarWeekView") is null, "the agenda to take the week's place");
        // A view chosen in the bar leaves the filter (the shown view's
        // segment too: its click is a choice all the same).
        Uia.Toggle(Uia.Find(main, "CalendarViewWeek"));
        Uia.Find(main, "CalendarWeekView");
        Uia.WaitFor(() => Uia.TryFind(main, "CalendarListHeading") is null, "the filter's heading to go");
    }

    [Fact]
    public void TheSearchBoxFiltersTheEvents()
    {
        var main = ShowCalendar();
        SetView(main, "CalendarViewWeek");
        var box = Edit(Uia.Find(main, "SearchBox"));
        Type(box, App.ProcessId, "standup");
        // The results under their heading, in place of the view.
        Uia.Find(main, "CalendarListHeading");
        var list = Uia.Find(main, "CalendarAgendaList");
        Uia.WaitFor(() => Rows(list).Length > 0, "the search's results");
        Assert.All(Rows(list), r => Assert.Contains(Standup, r.Current.Name, StringComparison.Ordinal));

        Uia.PressKey(box, App.ProcessId, VkEscape);
        Uia.WaitFor(() => Uia.TryFind(main, "CalendarListHeading") is null, "Escape to end the search");
        Uia.Find(main, "CalendarWeekView");
    }

    // The main window in Calendar mode, entered through the title bar's switch.
    private AutomationElement ShowCalendar()
    {
        var main = Main;
        if (Uia.TryFind(main, "CalendarBar") is null)
        {
            Uia.Toggle(Uia.Find(main, "ModeCalendar"));
        }
        Uia.Find(main, "CalendarBar");
        return main;
    }

    // Back to Mail through the title bar's switch.
    private static void ShowMail(AutomationElement main)
    {
        if (Uia.TryFind(main, "CalendarBar") is not null || Uia.TryFind(main, "BoardStyleList") is not null)
        {
            Uia.Toggle(Uia.Find(main, "ModeMail"));
        }
        Uia.Find(main, "MainMenuButton");
    }

    // A view's segment of the bar (a toggle button; at a narrow width the bar's menu is used instead).
    private static void SetView(AutomationElement main, string automationId)
    {
        var button = Uia.Find(main, automationId);
        if (ToggleOf(button) != ToggleState.On)
        {
            Uia.Toggle(button);
        }
        Uia.WaitFor(() => ToggleOf(Uia.Find(main, automationId)) == ToggleState.On, automationId + " to be the view");
    }

    private static string Title(AutomationElement main) => Uia.Find(main, "CalendarTitle").Current.Name;

    private static ToggleState ToggleOf(AutomationElement element) =>
        ((TogglePattern)element.GetCurrentPattern(TogglePattern.Pattern)).Current.ToggleState;

    private static bool IsSelected(AutomationElement element) =>
        ((SelectionItemPattern)element.GetCurrentPattern(SelectionItemPattern.Pattern)).Current.IsSelected;

    // The week's blocks (CalendarBlock:<occurrence key>).
    private static AutomationElement[] Blocks(AutomationElement week) =>
        [.. week.FindAll(TreeScope.Descendants, new PropertyCondition(AutomationElement.ControlTypeProperty, ControlType.Button))
            .Cast<AutomationElement>()
            .Where(b => b.Current.AutomationId.StartsWith("CalendarBlock:", StringComparison.Ordinal))];

    // The agenda's occurrence lines (CalendarAgendaRow:<occurrence key>).
    private static AutomationElement[] Rows(AutomationElement list) =>
        [.. list.FindAll(TreeScope.Descendants, Condition.TrueCondition)
            .Cast<AutomationElement>()
            .Where(r => r.Current.AutomationId.StartsWith("CalendarAgendaRow:", StringComparison.Ordinal))];

    // The search box's text field (the box itself, or the edit inside it).
    private static AutomationElement Edit(AutomationElement box) =>
        box.Current.ControlType == ControlType.Edit
            ? box
            : box.FindFirst(TreeScope.Descendants, new PropertyCondition(AutomationElement.ControlTypeProperty, ControlType.Edit)) ?? box;

    // Types lower-case letters into a field, as a user does (the box's
    // search follows typing only, not a programmatic value).
    private static void Type(AutomationElement field, int processId, string letters)
    {
        Focus(field, processId);
        foreach (var c in letters)
        {
            var key = (byte)char.ToUpperInvariant(c);
            NativeMethods.KeybdEvent(key, 0, 0, 0);
            NativeMethods.KeybdEvent(key, 0, NativeMethods.KeyEventKeyUp, 0);
        }
    }

    // A key with a modifier (Ctrl+3), the keyboard focus on element.
    private static void PressChord(AutomationElement element, int processId, byte modifier, byte key)
    {
        Focus(element, processId);
        NativeMethods.KeybdEvent(modifier, 0, 0, 0);
        NativeMethods.KeybdEvent(key, 0, 0, 0);
        NativeMethods.KeybdEvent(key, 0, NativeMethods.KeyEventKeyUp, 0);
        NativeMethods.KeybdEvent(modifier, 0, NativeMethods.KeyEventKeyUp, 0);
    }

    // The keyboard focus on element, in the app's window in the foreground.
    private static void Focus(AutomationElement element, int processId)
    {
        element.SetFocus();
        Uia.WaitFor(
            () =>
            {
                NativeMethods.GetWindowThreadProcessId(NativeMethods.GetForegroundWindow(), out var owner);
                return owner == processId && element.Current.HasKeyboardFocus;
            },
            "the keyboard focus in the app");
    }
}
