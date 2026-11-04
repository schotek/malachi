// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Calendar/CalendarKeys.swift (with the
// KeyContext(mode:) of Board.swift); GTK: ui/internal/calendar/keys.go
// (KeyAction, Special, Key, Press, matches, KeyContext, CalendarKeys,
// KeysGroup, ShowCalendarKey, OpenDetailKey, RemoveKey, SelectPreviousKey,
// SelectNextKey, needsSelection, KeyFor, EscapeTarget, EscapeFor,
// SelectionOrder, MoveSelection).
//
// The calendar's keys, the part that needs no WinUI: which shortcuts there
// are, with their texts for the shortcuts list and the menus, what a key
// does in which context, where Escape goes, and how the arrows move the
// selection. Every client binds the same keys with its own modifiers:
// Primary is Ctrl here; Alt (⌥ on macOS) and Control (⌃ on macOS) are
// both Shift on Windows, as on Linux (Ctrl+Alt is AltGr), which a Press
// says with ShiftModifier: New Event is Ctrl+Shift+N, the views
// Ctrl+Shift+1 to 4. Press.FromVirtualKey (Windows-only) builds the press
// of a KeyDown from its virtual key, so the window's handler stays thin.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.I18n;
using Malachi.Core.Presentation;

namespace Malachi.Core.Calendars;

public static partial class Cal
{
    /// <summary>keys.go <c>KeyAction</c>: what a key of the calendar does.</summary>
    public enum KeyAction
    {
        /// <summary>Switches the main window to the Calendar (Primary+3, in every mode; Primary+1 and 2 are the board's).</summary>
        ShowCalendar,

        /// <summary>Shows today (Primary+T).</summary>
        Today,

        /// <summary>‹ (Primary+←).</summary>
        Previous,

        /// <summary>› (Primary+→).</summary>
        Next,

        /// <summary>The Day view (Primary+Control+1).</summary>
        DayView,

        /// <summary>The Week view (Primary+Control+2).</summary>
        WeekView,

        /// <summary>The Month view (Primary+Control+3).</summary>
        MonthView,

        /// <summary>The Agenda (Primary+Control+4).</summary>
        AgendaView,

        /// <summary>Opens the editor (Primary+Alt+N; Primary+N stays New Message).</summary>
        NewEvent,

        /// <summary>Opens the selected event's detail (Return).</summary>
        OpenDetail,

        /// <summary>Deletes or removes the selected event, with its question (Delete, Backspace).</summary>
        Remove,

        /// <summary>Escape (<see cref="EscapeFor"/> says what it does).</summary>
        Escape,

        /// <summary>Moves the selection to the previous occurrence in time order (← ↑).</summary>
        SelectPrevious,

        /// <summary>Moves the selection to the next occurrence in time order (→ ↓).</summary>
        SelectNext,
    }

    /// <summary>keys.go <c>Special</c>: a key without a character.</summary>
    public enum Special
    {
        /// <summary>A key with a character (<see cref="Press.Rune"/>).</summary>
        None,

        /// <summary>←.</summary>
        Left,

        /// <summary>→.</summary>
        Right,

        /// <summary>↑.</summary>
        Up,

        /// <summary>↓.</summary>
        Down,

        /// <summary>Return (Enter).</summary>
        Return,

        /// <summary>⌫ on macOS, Delete (and Backspace) elsewhere.</summary>
        Delete,

        /// <summary>Escape.</summary>
        Escape,
    }

    /// <summary>keys.go <c>Key</c>: a shortcut of the calendar.</summary>
    public sealed record Key
    {
        /// <summary>What it does.</summary>
        public required KeyAction Action { get; init; }

        /// <summary>The key, lower case ('t', 'n', '1'…'4'), or '\0' with <see cref="Special"/>.</summary>
        public char Rune { get; init; }

        /// <summary>A key without a character, or <see cref="Cal.Special.None"/> with <see cref="Rune"/>.</summary>
        public Special Special { get; init; }

        /// <summary>With the primary modifier (Ctrl).</summary>
        public bool Primary { get; init; }

        /// <summary>With Alt (⌥ on macOS, Shift on Windows).</summary>
        public bool Alt { get; init; }

        /// <summary>With Control (⌃ on macOS, Shift on Windows).</summary>
        public bool Control { get; init; }

        /// <summary>Only in Calendar mode; the mode key and New Event work everywhere.</summary>
        public bool CalendarOnly { get; init; }

        /// <summary>Not while the keyboard is in a text field (the search field, an editor).</summary>
        public bool OutsideText { get; init; }

        /// <summary>The shortcut's text in the shortcuts list and menus ("" for Escape).</summary>
        public string Title { get; init; } = "";
    }

    /// <summary>
    /// keys.go <c>Press</c>: a key as a client received it. For the digits
    /// the client passes the physical number-row key (the board's rule;
    /// a virtual key is that). macOS passes ⌥ as <paramref name="Alt"/> and
    /// ⌃ as <paramref name="Control"/>; Windows (and Linux) set
    /// <paramref name="ShiftModifier"/> and pass Shift, which then stands
    /// for a key's Alt or Control; their own Alt key is never a calendar
    /// modifier, so a press with it matches no key.
    /// </summary>
    /// <param name="Rune">The key's character (any case), or '\0' with <paramref name="Special"/>.</param>
    /// <param name="Special">A key without a character.</param>
    /// <param name="Primary">Ctrl (⌘ on macOS) is held.</param>
    /// <param name="Alt">Alt (⌥) is held.</param>
    /// <param name="Control">⌃ is held (macOS only).</param>
    /// <param name="Shift">Shift is held.</param>
    /// <param name="ShiftModifier">Shift stands for Alt and Control (Windows, Linux).</param>
    public readonly record struct Press(
        char Rune = '\0',
        Special Special = Special.None,
        bool Primary = false,
        bool Alt = false,
        bool Control = false,
        bool Shift = false,
        bool ShiftModifier = false)
    {
        // winuser.h codes the Presentation.VirtualKey list lacks.
        private const int VkBack = 0x08;
        private const int VkLeft = 0x25;
        private const int VkRight = 0x27;

        /// <summary>
        /// The press of a Windows KeyDown (Windows-only): the virtual key
        /// (WinUI's <c>VirtualKey</c>, a WM_KEYDOWN's wParam) with the
        /// modifiers held, Shift as the calendar's Alt and Control. Letters
        /// and the number row's digits are their characters (the key that
        /// types them on any layout); Backspace is Delete, as GTK binds it.
        /// <paramref name="alt"/> is Alt or a Windows key held: neither is a
        /// calendar modifier, so the press matches no key.
        /// </summary>
        public static Press FromVirtualKey(int virtualKey, bool ctrl, bool shift, bool alt)
        {
            var rune = virtualKey switch
            {
                >= 0x30 and <= 0x39 => (char)virtualKey,
                >= VirtualKey.A and <= 0x5A => (char)(virtualKey - VirtualKey.A + 'a'),
                _ => '\0',
            };
            var special = virtualKey switch
            {
                VkLeft => Special.Left,
                VkRight => Special.Right,
                VirtualKey.Up => Special.Up,
                VirtualKey.Down => Special.Down,
                VirtualKey.Enter => Special.Return,
                VirtualKey.Delete or VkBack => Special.Delete,
                VirtualKey.Escape => Special.Escape,
                _ => Special.None,
            };
            return new Press(rune, special, Primary: ctrl, Alt: alt, Control: false, Shift: shift, ShiftModifier: true);
        }

        // keys.go matches: a press of k's key (r, lower case) with k's modifiers.
        internal bool Matches(Key k, char r)
        {
            if (k.Rune != r || k.Special != Special || k.Primary != Primary)
            {
                return false;
            }
            if (ShiftModifier)
            {
                return !Alt && !Control && (k.Alt || k.Control) == Shift;
            }
            return !Shift && k.Alt == Alt && k.Control == Control;
        }
    }

    /// <summary>keys.go <c>KeyContext</c>: the state a key acts in.</summary>
    /// <param name="Mode">The main window's mode.</param>
    /// <param name="InText">The keyboard is in a text field.</param>
    /// <param name="Selected">An occurrence is selected.</param>
    public readonly record struct KeyContext(Board.Mode Mode, bool InText = false, bool Selected = false)
    {
        /// <summary>The window shows the calendar (Swift's <c>calendarMode</c>, Go's <c>Mode == ModeCalendar</c>).</summary>
        public bool CalendarMode => Mode == Board.Mode.Calendar;
    }

    /// <summary>keys.go <c>CalendarKeys</c>: the calendar's shortcuts in the shortcuts list's order.</summary>
    public static IReadOnlyList<Key> CalendarKeys() =>
    [
        new() { Action = KeyAction.ShowCalendar, Rune = '3', Primary = true, Title = ShowCalendarKey },
        new() { Action = KeyAction.Today, Rune = 't', Primary = true, CalendarOnly = true, Title = Text.Today },
        new() { Action = KeyAction.Previous, Special = Special.Left, Primary = true, CalendarOnly = true, Title = Text.Previous },
        new() { Action = KeyAction.Next, Special = Special.Right, Primary = true, CalendarOnly = true, Title = Text.Next },
        new() { Action = KeyAction.DayView, Rune = '1', Primary = true, Control = true, CalendarOnly = true, Title = Text.ViewTitle(View.Day) },
        new() { Action = KeyAction.WeekView, Rune = '2', Primary = true, Control = true, CalendarOnly = true, Title = Text.ViewTitle(View.Week) },
        new() { Action = KeyAction.MonthView, Rune = '3', Primary = true, Control = true, CalendarOnly = true, Title = Text.ViewTitle(View.Month) },
        new() { Action = KeyAction.AgendaView, Rune = '4', Primary = true, Control = true, CalendarOnly = true, Title = Text.ViewTitle(View.Agenda) },
        new() { Action = KeyAction.NewEvent, Rune = 'n', Primary = true, Alt = true, Title = Text.NewEvent },
        new() { Action = KeyAction.OpenDetail, Special = Special.Return, CalendarOnly = true, OutsideText = true, Title = OpenDetailKey },
        new() { Action = KeyAction.Remove, Special = Special.Delete, CalendarOnly = true, OutsideText = true, Title = RemoveKey },
        new() { Action = KeyAction.Escape, Special = Special.Escape, CalendarOnly = true },
        new() { Action = KeyAction.SelectPrevious, Special = Special.Left, CalendarOnly = true, OutsideText = true, Title = SelectPreviousKey },
        new() { Action = KeyAction.SelectPrevious, Special = Special.Up, CalendarOnly = true, OutsideText = true, Title = SelectPreviousKey },
        new() { Action = KeyAction.SelectNext, Special = Special.Right, CalendarOnly = true, OutsideText = true, Title = SelectNextKey },
        new() { Action = KeyAction.SelectNext, Special = Special.Down, CalendarOnly = true, OutsideText = true, Title = SelectNextKey },
    ];

    /// <summary>keys.go <c>KeysGroup</c>: heads the calendar's group in the shortcuts list.</summary>
    public static string KeysGroup => Text.ModeName;

    /// <summary>keys.go <c>ShowCalendarKey</c>: the shortcuts list's text of Primary+3.</summary>
    public static string ShowCalendarKey =>
        // TRANSLATORS: the shortcuts window: the key that shows the calendar in
        // the main window.
        L10n.T("Show Calendar");

    /// <summary>keys.go <c>OpenDetailKey</c>: the shortcuts list's text of Return.</summary>
    public static string OpenDetailKey => L10n.T("Show Event Details");

    /// <summary>keys.go <c>RemoveKey</c>: the shortcuts list's text of Delete.</summary>
    public static string RemoveKey => L10n.T("Delete or Remove Event");

    /// <summary>keys.go <c>SelectPreviousKey</c>: the shortcuts list's text of ← and ↑.</summary>
    public static string SelectPreviousKey => L10n.T("Select Previous Event");

    /// <summary>keys.go <c>SelectNextKey</c>: the shortcuts list's text of → and ↓.</summary>
    public static string SelectNextKey => L10n.T("Select Next Event");

    // keys.go needsSelection: the actions that act on the selected occurrence.
    private static bool NeedsSelection(KeyAction a) => a is KeyAction.OpenDetail or KeyAction.Remove;

    /// <summary>
    /// keys.go <c>KeyFor</c>: the action of a key press in
    /// <paramref name="ctx"/>; null when the key is not the calendar's
    /// there. A key that acts on the selection needs one.
    /// </summary>
    public static KeyAction? KeyFor(Press p, KeyContext ctx)
    {
        var r = p.Rune;
        if (r is >= 'A' and <= 'Z')
        {
            r = (char)(r + ('a' - 'A'));
        }
        foreach (var k in CalendarKeys())
        {
            if (!p.Matches(k, r))
            {
                continue;
            }
            if ((k.CalendarOnly && !ctx.CalendarMode) || (k.OutsideText && ctx.InText) || (NeedsSelection(k.Action) && !ctx.Selected))
            {
                return null;
            }
            return k.Action;
        }
        return null;
    }

    /// <summary>keys.go <c>EscapeTarget</c>: what Escape does in Calendar mode.</summary>
    public enum EscapeTarget
    {
        /// <summary>Nothing is open.</summary>
        Nothing,

        /// <summary>Closes the detail popover (or another popup).</summary>
        ClosePopover,

        /// <summary>Clears the search field and returns to the view.</summary>
        ClearSearch,
    }

    /// <summary>keys.go <c>EscapeFor</c>: where Escape goes: an open popover closes first, then the search is cleared.</summary>
    public static EscapeTarget EscapeFor(bool popoverOpen, bool searching) =>
        popoverOpen ? EscapeTarget.ClosePopover : searching ? EscapeTarget.ClearSearch : EscapeTarget.Nothing;

    /// <summary>
    /// keys.go <c>SelectionOrder</c>: the order the arrows walk: the shown
    /// occurrences' keys (<see cref="OccurrenceKey"/>) by day, all-day first
    /// on their day, by start, then end, then key; duplicates (an occurrence
    /// in two cells) once; one without a usable time is left out.
    /// </summary>
    public static IReadOnlyList<string> SelectionOrder(IReadOnlyList<Occurrence> occurrences, TimeZoneInfo zone)
    {
        ArgumentNullException.ThrowIfNull(occurrences);
        var entries = new List<(string Key, DateTimeOffset Day, bool AllDay, DateTimeOffset Start, DateTimeOffset End)>();
        var seen = new HashSet<string>(StringComparer.Ordinal);
        foreach (var o in occurrences)
        {
            if (o is null)
            {
                continue;
            }
            var k = OccurrenceKey(o);
            if (Span(o, zone) is not (var s, var e) || !seen.Add(k))
            {
                continue;
            }
            entries.Add((k, StartOfDay(s, zone), o.AllDay, s, e));
        }
        return StableSorted(entries, (a, b) =>
        {
            var c = Compare(a.Day, b.Day);
            if (c != 0)
            {
                return c;
            }
            if (a.AllDay != b.AllDay)
            {
                return a.AllDay ? -1 : 1;
            }
            return Or(Compare(a.Start, b.Start), Compare(a.End, b.End), CompareBytes(a.Key, b.Key));
        }).Select(x => x.Key).ToList();
    }

    /// <summary>
    /// keys.go <c>MoveSelection</c>: the key after <paramref name="current"/>
    /// in <paramref name="order"/> by <paramref name="delta"/> (±1), clamped
    /// to the ends; with no current one (or one not in the order) the first
    /// for a step forward and the last for a step back; "" for an empty
    /// order.
    /// </summary>
    public static string MoveSelection(IReadOnlyList<string> order, string? current, int delta)
    {
        ArgumentNullException.ThrowIfNull(order);
        if (order.Count == 0)
        {
            return "";
        }
        var i = -1;
        for (var j = 0; j < order.Count; j++)
        {
            if (string.Equals(order[j], current, StringComparison.Ordinal))
            {
                i = j;
                break;
            }
        }
        if (i < 0)
        {
            return delta < 0 ? order[^1] : order[0];
        }
        return order[Math.Clamp(i + delta, 0, order.Count - 1)];
    }
}
