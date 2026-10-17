// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The event detail's and the event editor's smoke tests (docs/windows-port.md
// §12, docs/calendar.md §5.5 and §5.6) over the published app with the
// calendar's sample data (MALACHI_CALENDAR_SAMPLES=1, InMemoryCalendarSource
// over CalendarSamples; CalendarSmokeFixture): a click on a block opens its
// detail flyout with the event's title, Edit there opens the Edit Event
// window with the title filled, New Event (Ctrl+Shift+N) opens an empty New
// Event window that Cancel closes, a real double click on empty time opens
// it too, and a write over the samples (Save in the editor, an answer in the
// detail) says "The preview cannot do that." (in the app's language, from
// the app's own catalogue). The New Event window comes up in front of the
// main window with the keyboard in its title (the owner's finding of
// 2026-10-17: the double click's window came up behind, the release of the
// second press taking the activation back). Events are found by their
// sample titles, which are data, never translated; everything else by
// AutomationId.

using System;
using System.Linq;
using System.Windows.Automation;
using Malachi.Core.I18n;
using Xunit;

namespace Malachi.App.UiTests;

/// <summary>The event detail and the editor over the sample data.</summary>
[Collection(OneAppAtATime.Name)]
public sealed class CalendarDetailSmokeTests(CalendarSmokeFixture fixture) : IClassFixture<CalendarSmokeFixture>
{
    // Sample titles (CalendarSampleData.cs): an invitation the user accepted
    // (Work, with a video call and notes), and an event of the user's own
    // calendar that can be edited (Gmail, Personal). Both are this week's.
    private const string Planning = "Q4 planning";
    private const string Supplier = "Call with the supplier";

    // What a write over the samples says (Cal.Text.PreviewOnly's msgid).
    private const string PreviewOnly = "The preview cannot do that.";

    private const byte VkControl = 0x11;
    private const byte VkShift = 0x10;
    private const byte VkEscape = 0x1B;
    private const byte VkN = 0x4E;

    // How long the double click's second press is held: longer than the
    // New Event window takes to show.
    private static readonly TimeSpan SecondPressHeld = TimeSpan.FromSeconds(1);

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

    [Fact]
    public void AClickOnABlockOpensItsDetail()
    {
        var main = ShowWeek();
        Uia.Invoke(Block(main, Planning));
        var title = Uia.Find(main, "EventDetailTitle");
        Uia.WaitFor(() => title.Current.Name == Planning, "the detail to show the event's title");
        // The rows of an accepted invitation with a video call.
        Assert.NotNull(Uia.Find(main, "EventDetailTime"));
        Assert.NotNull(Uia.Find(main, "EventDetailJoin"));
        Assert.NotNull(Uia.Find(main, "EventDetailResponse"));
        Assert.NotNull(Uia.Find(main, "EventDetailRemove"));
        CloseDetail(main);
    }

    [Fact]
    public void EditInTheDetailOpensTheEditorWithTheTitle()
    {
        var main = ShowWeek();
        Uia.Invoke(Block(main, Supplier));
        Uia.Invoke(Uia.Find(main, "EventDetailEdit"));
        var editor = Uia.WindowWith(App.ProcessId, "EventEditorTitle");
        var title = Uia.Find(editor, "EventEditorTitle");
        Uia.WaitFor(() => Uia.Value(title) == Supplier, "the editor to show the event's title");
        // Edit Event has Delete at the footer's left; the detail closed.
        Assert.NotNull(Uia.Find(editor, "EventEditorDelete"));
        Uia.WaitFor(() => Uia.TryFind(main, "EventDetailTitle") is null, "the detail to close");
        // Nothing changed: Cancel closes without a question.
        Uia.Invoke(Uia.Find(editor, "EventEditorCancel"));
        Uia.WaitGone(editor, what: "the editor");
    }

    [Fact]
    public void NewEventOpensAnEmptyEditorThatCancelCloses()
    {
        var main = ShowWeek();
        PressNewEvent(Uia.Find(main, "CalendarToday"));
        var editor = Uia.WindowWith(App.ProcessId, "EventEditorTitle");
        AssertInFront(editor, main);
        Assert.Equal("", Uia.Value(Uia.Find(editor, "EventEditorTitle")));
        Assert.NotNull(Uia.Find(editor, "EventEditorCalendar"));
        Assert.NotNull(Uia.Find(editor, "EventEditorAllDay"));
        Assert.NotNull(Uia.Find(editor, "EventEditorStartDate"));
        Assert.NotNull(Uia.Find(editor, "EventEditorSave"));
        // New Event has no Delete.
        Assert.Null(Uia.TryFind(editor, "EventEditorDelete"));
        Uia.Invoke(Uia.Find(editor, "EventEditorCancel"));
        Uia.WaitGone(editor, what: "the editor");
    }

    [Fact]
    public void ADoubleClickOnEmptyTimeOpensTheEditorInFront()
    {
        var main = ShowWeek();
        Uia.DoubleClick(EmptySlot(main), SecondPressHeld);
        var editor = Uia.WindowWith(App.ProcessId, "EventEditorTitle");
        AssertInFront(editor, main);
        Assert.Equal("", Uia.Value(Uia.Find(editor, "EventEditorTitle")));
        Uia.Invoke(Uia.Find(editor, "EventEditorCancel"));
        Uia.WaitGone(editor, what: "the editor");
    }

    [Fact]
    public void SavingOverTheSamplesSaysThePreviewCannotDoThat()
    {
        var main = ShowWeek();
        PressNewEvent(Uia.Find(main, "CalendarToday"));
        var editor = Uia.WindowWith(App.ProcessId, "EventEditorTitle");
        Uia.SetValue(Uia.Find(editor, "EventEditorTitle"), "Smoke test event");
        Uia.Invoke(Uia.Find(editor, "EventEditorSave"));
        var error = Uia.Find(editor, "EventEditorError");
        Uia.WaitFor(() => IsPreviewOnly(error.Current.Name), "the preview's refusal");

        // The window has changes: closing asks, and Discard lets it go
        // (the ContentDialog's secondary button).
        Uia.Invoke(Uia.Find(editor, "EventEditorCancel"));
        Uia.Invoke(Uia.Find(editor, "SecondaryButton"));
        Uia.WaitGone(editor, what: "the editor");
    }

    [Fact]
    public void AnAnswerInTheDetailOverTheSamplesSaysThePreviewCannotDoThat()
    {
        var main = ShowWeek();
        Uia.Invoke(Block(main, Planning));
        Uia.Invoke(Uia.Find(main, "EventDetailResponse"));
        Choose(Uia.Find(main, "EventDetailTentative"));
        var error = Uia.Find(main, "EventDetailError");
        Uia.WaitFor(() => IsPreviewOnly(error.Current.Name), "the preview's refusal");
        CloseDetail(main);
    }

    // The main window in Calendar mode, in the Week view of today.
    private AutomationElement ShowWeek()
    {
        var main = Main;
        if (Uia.TryFind(main, "CalendarBar") is null)
        {
            Uia.Toggle(Uia.Find(main, "ModeCalendar"));
        }
        Uia.Find(main, "CalendarBar");
        var week = Uia.Find(main, "CalendarViewWeek");
        if (((TogglePattern)week.GetCurrentPattern(TogglePattern.Pattern)).Current.ToggleState != ToggleState.On)
        {
            Uia.Toggle(week);
        }
        Uia.Invoke(Uia.Find(main, "CalendarToday"));
        Uia.Find(main, "CalendarWeekView");
        return main;
    }

    // The week's block of the event titled title (CalendarBlock:<occurrence key>).
    private static AutomationElement Block(AutomationElement main, string title) =>
        Uia.Until(
            () => Uia.Find(main, "CalendarWeekView")
                .FindAll(TreeScope.Descendants, new PropertyCondition(AutomationElement.ControlTypeProperty, ControlType.Button))
                .Cast<AutomationElement>()
                .FirstOrDefault(b => b.Current.AutomationId.StartsWith("CalendarBlock:", StringComparison.Ordinal)
                    && b.Current.Name.Contains(title, StringComparison.Ordinal)),
            null,
            "the block of " + title);

    // A point of empty time in the week's grid, in screen coordinates: in a
    // day column (the hour axis left out), from the viewport's bottom up, the
    // first that lies on no block (UIA's FromPoint names the island, not the
    // grid).
    private static System.Windows.Point EmptySlot(AutomationElement main)
    {
        var week = Uia.Find(main, "CalendarWeekView");
        var viewport = Uia.Find(week, "Scroller").Current.BoundingRectangle;
        var blocks = Uia.All(week, ControlType.Button).Cast<AutomationElement>()
            .Select(b => b.Current.BoundingRectangle)
            .Where(r => !r.IsEmpty)
            .ToList();
        foreach (var fy in new[] { 0.9, 0.75, 0.6, 0.45, 0.3, 0.15 })
        {
            for (var day = 0; day < 7; day++)
            {
                var p = new System.Windows.Point(viewport.Left + (viewport.Width * (0.12 + (day * 0.125))), viewport.Top + (viewport.Height * fy));
                if (!blocks.Any(r => p.X >= r.Left - 6 && p.X <= r.Right + 6 && p.Y >= r.Top - 6 && p.Y <= r.Bottom + 6))
                {
                    return p;
                }
            }
        }
        throw new InvalidOperationException("no empty time in the week's viewport");
    }

    // The editor is the active window with the keyboard in its title, above
    // the main window, on the main window's display with its title bar in
    // the work area. Not necessarily centred: new editor windows cascade
    // from the first one (EventEditorManager.Place), and the fixture's other
    // tests open some before.
    private static void AssertInFront(AutomationElement editor, AutomationElement main)
    {
        var handle = (nint)editor.Current.NativeWindowHandle;
        var mainHandle = (nint)main.Current.NativeWindowHandle;
        var title = Uia.Find(editor, "EventEditorTitle");
        Uia.WaitFor(
            () => NativeMethods.GetForegroundWindow() == handle && title.Current.HasKeyboardFocus,
            "the editor in front with the keyboard in its title");
        Assert.True(NativeMethods.IsAbove(handle, mainHandle), "the editor lies above the main window");
        Assert.Equal(NativeMethods.MonitorFromWindow(mainHandle, NativeMethods.MonitorDefaultToNearest), NativeMethods.MonitorFromWindow(handle, NativeMethods.MonitorDefaultToNearest));
        var area = NativeMethods.WorkAreaOf(mainHandle);
        var frame = editor.Current.BoundingRectangle;
        Assert.InRange(frame.Left, area.Left, area.Right - 1.0);
        Assert.InRange(frame.Top, area.Top, area.Bottom - 1.0);
    }

    // Escape in the detail closes it.
    private void CloseDetail(AutomationElement main)
    {
        if (Uia.TryFind(main, "EventDetailTitle") is null)
        {
            return;
        }
        Uia.PressKey(Uia.Find(main, "EventDetail"), App.ProcessId, VkEscape);
        Uia.WaitFor(() => Uia.TryFind(main, "EventDetailTitle") is null, "Escape to close the detail");
    }

    // A menu item: invoked, or toggled where it is a toggle item.
    private static void Choose(AutomationElement item)
    {
        if (item.TryGetCurrentPattern(InvokePattern.Pattern, out var invoke))
        {
            ((InvokePattern)invoke).Invoke();
            return;
        }
        Uia.Toggle(item);
    }

    // The preview's sentence in English or in the app's language.
    private static bool IsPreviewOnly(string text)
    {
        if (text == PreviewOnly)
        {
            return true;
        }
        var app = Catalogue.Default(appDirectory: UiEnvironment.AppFolder);
        return text == app.Translate(PreviewOnly);
    }

    // Ctrl+Shift+N (New Event), the keyboard focus on element.
    private void PressNewEvent(AutomationElement element)
    {
        element.SetFocus();
        Uia.WaitFor(
            () =>
            {
                NativeMethods.GetWindowThreadProcessId(NativeMethods.GetForegroundWindow(), out var owner);
                return owner == App.ProcessId && element.Current.HasKeyboardFocus;
            },
            "the keyboard focus in the app");
        NativeMethods.KeybdEvent(VkControl, 0, 0, 0);
        NativeMethods.KeybdEvent(VkShift, 0, 0, 0);
        NativeMethods.KeybdEvent(VkN, 0, 0, 0);
        NativeMethods.KeybdEvent(VkN, 0, NativeMethods.KeyEventKeyUp, 0);
        NativeMethods.KeybdEvent(VkShift, 0, NativeMethods.KeyEventKeyUp, 0);
        NativeMethods.KeybdEvent(VkControl, 0, NativeMethods.KeyEventKeyUp, 0);
    }
}
