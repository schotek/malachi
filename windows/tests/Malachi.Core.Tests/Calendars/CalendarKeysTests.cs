// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarKeysTests.swift; GTK:
// ui/internal/calendar/keys_test.go (TestKeyFor, TestEscapeFor,
// TestSelectionOrder), with the Windows-only cases of Press.FromVirtualKey:
// Shift stands for ⌥ and ⌃, so New Event is Ctrl+Shift+N and the views
// Ctrl+Shift+1 to 4.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Presentation;
using Xunit;
using static Malachi.Core.Calendars.Cal;
using F = Malachi.Core.Tests.Calendars.CalFixture;
using Mode = Malachi.Core.Boards.Board.Mode;

namespace Malachi.Core.Tests.Calendars;

public sealed class CalendarKeysTests
{
    private static readonly KeyContext InCalendar = new(Mode.Calendar);
    private static readonly KeyContext WithSelection = new(Mode.Calendar, Selected: true);
    private static readonly KeyContext InMail = new(Mode.Mail);
    private static readonly KeyContext InTextField = new(Mode.Calendar, InText: true);

    private static readonly Dictionary<string, (Press Press, KeyContext Context, KeyAction? Want)> KeyForCases = new()
    {
        ["⌘3 in Mail"] = (new(Rune: '3', Primary: true), InMail, KeyAction.ShowCalendar),
        ["⌘T"] = (new(Rune: 't', Primary: true), InCalendar, KeyAction.Today),
        ["⌘T upper case"] = (new(Rune: 'T', Primary: true), InCalendar, KeyAction.Today),
        ["⌘T in Mail"] = (new(Rune: 't', Primary: true), InMail, null),
        ["⌘←"] = (new(Special: Special.Left, Primary: true), InCalendar, KeyAction.Previous),
        ["⌘→"] = (new(Special: Special.Right, Primary: true), InCalendar, KeyAction.Next),
        ["⌘→ in the search field"] = (new(Special: Special.Right, Primary: true), InTextField, KeyAction.Next),
        ["⌃⌘1"] = (new(Rune: '1', Primary: true, Control: true), InCalendar, KeyAction.DayView),
        ["⌃⌘2"] = (new(Rune: '2', Primary: true, Control: true), InCalendar, KeyAction.WeekView),
        ["⌃⌘3"] = (new(Rune: '3', Primary: true, Control: true), InCalendar, KeyAction.MonthView),
        ["⌃⌘4"] = (new(Rune: '4', Primary: true, Control: true), InCalendar, KeyAction.AgendaView),
        ["⌃⌘4 in Mail"] = (new(Rune: '4', Primary: true, Control: true), InMail, null),
        ["Ctrl+Shift+3 elsewhere"] = (new(Rune: '3', Primary: true, Shift: true, ShiftModifier: true), InCalendar, KeyAction.MonthView),
        ["⇧⌘3 on macOS"] = (new(Rune: '3', Primary: true, Shift: true), InCalendar, null),
        ["⌥⌘N"] = (new(Rune: 'n', Primary: true, Alt: true), InMail, KeyAction.NewEvent),
        ["Ctrl+Shift+N elsewhere"] = (new(Rune: 'n', Primary: true, Shift: true, ShiftModifier: true), InMail, KeyAction.NewEvent),
        ["Ctrl+Alt+N elsewhere"] = (new(Rune: 'n', Primary: true, Alt: true, ShiftModifier: true), InMail, null),
        ["⌘N is New Message"] = (new(Rune: 'n', Primary: true), InCalendar, null),
        ["Return with a selection"] = (new(Special: Special.Return), WithSelection, KeyAction.OpenDetail),
        ["Return without one"] = (new(Special: Special.Return), InCalendar, null),
        ["⌫ with a selection"] = (new(Special: Special.Delete), WithSelection, KeyAction.Remove),
        ["⌫ in text"] = (new(Special: Special.Delete), new(Mode.Calendar, InText: true, Selected: true), null),
        ["Escape"] = (new(Special: Special.Escape), InCalendar, KeyAction.Escape),
        ["↑"] = (new(Special: Special.Up), InCalendar, KeyAction.SelectPrevious),
        ["←"] = (new(Special: Special.Left), InCalendar, KeyAction.SelectPrevious),
        ["↓"] = (new(Special: Special.Down), InCalendar, KeyAction.SelectNext),
        ["→ in text"] = (new(Special: Special.Right), InTextField, null),
        ["x"] = (new(Rune: 'x'), InCalendar, null),
    };

    public static TheoryData<string> KeyForNames => [.. KeyForCases.Keys];

    [Theory]
    [MemberData(nameof(KeyForNames))]
    public void KeyForCase(string name)
    {
        var (press, ctx, want) = KeyForCases[name];
        Assert.Equal(want, KeyFor(press, ctx));
    }

    [Fact]
    public void EveryKeyButEscapeHasATitle()
    {
        foreach (var k in CalendarKeys())
        {
            Assert.True(k.Title.Length > 0 || k.Action == KeyAction.Escape, $"key {k.Action} has no title");
        }
        Assert.Equal("Calendar", KeysGroup);
        Assert.Equal("Show Calendar", ShowCalendarKey);
        Assert.Equal(
            ["Show Calendar", "Today", "Previous", "Next", "Day", "Week", "Month", "Agenda", "New Event", "Show Event Details",
                "Delete or Remove Event", "", "Select Previous Event", "Select Previous Event", "Select Next Event", "Select Next Event"],
            CalendarKeys().Select(k => k.Title));
    }

    // Windows-only: the press of a KeyDown, from its virtual key and the
    // modifiers (Ctrl, Shift, Alt or a Windows key).
    [Theory]
    [InlineData("Ctrl+3", 0x33, true, false, false, Mode.Mail, false, KeyAction.ShowCalendar)]
    [InlineData("Ctrl+T", 0x54, true, false, false, Mode.Calendar, false, KeyAction.Today)]
    [InlineData("Ctrl+T in Mail", 0x54, true, false, false, Mode.Mail, false, null)]
    [InlineData("Ctrl+Shift+T", 0x54, true, true, false, Mode.Calendar, false, null)]
    [InlineData("Ctrl+Left", 0x25, true, false, false, Mode.Calendar, false, KeyAction.Previous)]
    [InlineData("Ctrl+Right", 0x27, true, false, false, Mode.Calendar, false, KeyAction.Next)]
    [InlineData("Ctrl+Shift+1", 0x31, true, true, false, Mode.Calendar, false, KeyAction.DayView)]
    [InlineData("Ctrl+Shift+2", 0x32, true, true, false, Mode.Calendar, false, KeyAction.WeekView)]
    [InlineData("Ctrl+Shift+3", 0x33, true, true, false, Mode.Calendar, false, KeyAction.MonthView)]
    [InlineData("Ctrl+Shift+4", 0x34, true, true, false, Mode.Calendar, false, KeyAction.AgendaView)]
    [InlineData("Ctrl+Shift+3 in Mail", 0x33, true, true, false, Mode.Mail, false, null)]
    [InlineData("Ctrl+Shift+N", 0x4E, true, true, false, Mode.Board, false, KeyAction.NewEvent)]
    [InlineData("Ctrl+Alt+N", 0x4E, true, false, true, Mode.Calendar, false, null)]
    [InlineData("Ctrl+Shift+Alt+N", 0x4E, true, true, true, Mode.Calendar, false, null)]
    [InlineData("Ctrl+N", 0x4E, true, false, false, Mode.Calendar, false, null)]
    [InlineData("Ctrl+numpad 3", 0x63, true, false, false, Mode.Mail, false, null)]
    [InlineData("Enter", VirtualKey.Enter, false, false, false, Mode.Calendar, true, KeyAction.OpenDetail)]
    [InlineData("Delete", VirtualKey.Delete, false, false, false, Mode.Calendar, true, KeyAction.Remove)]
    [InlineData("Backspace", 0x08, false, false, false, Mode.Calendar, true, KeyAction.Remove)]
    [InlineData("Escape", VirtualKey.Escape, false, false, false, Mode.Calendar, false, KeyAction.Escape)]
    [InlineData("Up", VirtualKey.Up, false, false, false, Mode.Calendar, false, KeyAction.SelectPrevious)]
    [InlineData("Left", 0x25, false, false, false, Mode.Calendar, false, KeyAction.SelectPrevious)]
    [InlineData("Down", VirtualKey.Down, false, false, false, Mode.Calendar, false, KeyAction.SelectNext)]
    [InlineData("Shift+Down", VirtualKey.Down, false, true, false, Mode.Calendar, false, null)]
    [InlineData("F5", 0x74, false, false, false, Mode.Calendar, false, null)]
    public void FromVirtualKeyCase(string name, int vk, bool ctrl, bool shift, bool alt, Mode mode, bool selected, KeyAction? want)
    {
        var press = Press.FromVirtualKey(vk, ctrl, shift, alt);
        Assert.True(press.ShiftModifier, name);
        Assert.Equal(want, KeyFor(press, new KeyContext(mode, Selected: selected)));
    }

    // Windows-only: ShortcutMap's Ctrl+Shift+N is the calendar's New Event
    // in every mode and in a text field, so the window's CommandRouter runs
    // it and the page leaves it alone (one editor, not two).
    [Fact]
    public void NewEventIsShortcutMapsChordInEveryMode()
    {
        var chord = KeyChord.CtrlShift(VirtualKey.N);
        var m = chord.Modifiers;
        var press = Press.FromVirtualKey(chord.Key, m.HasFlag(KeyModifiers.Control), m.HasFlag(KeyModifiers.Shift), m.HasFlag(KeyModifiers.Alt));
        foreach (var mode in Enum.GetValues<Mode>())
        {
            Assert.Equal(KeyAction.NewEvent, KeyFor(press, new KeyContext(mode, InText: true)));
        }
        Assert.Equal(ShortcutCommand.NewEvent, ShortcutMap.Resolve(chord, new ShortcutContext(WindowKind.Main)));
    }

    [Theory]
    [InlineData(0x41, 'a', Special.None)]
    [InlineData(0x5A, 'z', Special.None)]
    [InlineData(0x30, '0', Special.None)]
    [InlineData(0x39, '9', Special.None)]
    [InlineData(0x0D, '\0', Special.Return)]
    [InlineData(0x2E, '\0', Special.Delete)]
    [InlineData(0x08, '\0', Special.Delete)]
    [InlineData(0x1B, '\0', Special.Escape)]
    [InlineData(0x25, '\0', Special.Left)]
    [InlineData(0x26, '\0', Special.Up)]
    [InlineData(0x27, '\0', Special.Right)]
    [InlineData(0x28, '\0', Special.Down)]
    [InlineData(0x60, '\0', Special.None)]
    public void FromVirtualKeyNamesTheKey(int vk, char rune, Special special)
    {
        var press = Press.FromVirtualKey(vk, ctrl: true, shift: true, alt: false);
        Assert.Equal(new Press(rune, special, Primary: true, Shift: true, ShiftModifier: true), press);
    }

    [Theory]
    [InlineData(true, true, EscapeTarget.ClosePopover)]
    [InlineData(true, false, EscapeTarget.ClosePopover)]
    [InlineData(false, true, EscapeTarget.ClearSearch)]
    [InlineData(false, false, EscapeTarget.Nothing)]
    public void EscapeForCases(bool popover, bool search, EscapeTarget want) => Assert.Equal(want, EscapeFor(popover, search));

    private static readonly IReadOnlyList<string> Order = SelectionOrder(
        [
            F.Timed("b", F.At(2026, 10, 14, 10), F.At(2026, 10, 14, 11)),
            F.Timed("a", F.At(2026, 10, 14, 9), F.At(2026, 10, 14, 10)),
            F.AllDayOcc("allday", F.At(2026, 10, 14), 1),
            F.Timed("c", F.At(2026, 10, 13, 18), F.At(2026, 10, 13, 19)),
            F.Timed("a", F.At(2026, 10, 14, 9), F.At(2026, 10, 14, 10)),
            F.Occ() with { EventId = new CalendarEventId("broken") },
        ],
        F.Prague);

    [Fact]
    public void SelectionOrderOfTheShown() => Assert.Equal(["c|", "allday|", "a|", "b|"], Order);

    [Theory]
    [InlineData("a|", 1, "b|")]
    [InlineData("a|", -1, "allday|")]
    [InlineData("b|", 1, "b|")]
    [InlineData("c|", -1, "c|")]
    [InlineData("", 1, "c|")]
    [InlineData("", -1, "b|")]
    [InlineData("gone", 1, "c|")]
    [InlineData(null, -1, "b|")]
    public void MoveSelectionCases(string? current, int delta, string want) => Assert.Equal(want, MoveSelection(Order, current, delta));

    [Fact]
    public void MoveSelectionInAnEmptyOrder() => Assert.Equal("", MoveSelection([], "a", 1));
}
