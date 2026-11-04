// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/toolbar/variant.go (Variant, GTK, Windows, Omits);
// the functions over a variant are Toolbar.Variant.cs. Go's Variant is
// ToolbarVariant (Variant is a Visual Basic keyword, CA1716), its field
// Default is Defaults; the zero Variant is Zero.
//
// A client's difference from the catalog (decision T12,
// docs/plans/toolbar-customization.md §17): the items it lacks, the
// sections it lets an item stand in besides the catalog's, and its own
// default arrangement per toolbar, so that an untouched toolbar looks as
// the client's did before customization. The stored arrangement is shared
// by every client.

using System;
using System.Collections.Generic;
using System.Linq;
using static Malachi.Core.Toolbars.Toolbar;

namespace Malachi.Core.Toolbars;

/// <summary>toolbar.Variant: what a client lacks, allows besides the catalog and has as its defaults.</summary>
public sealed class ToolbarVariant
{
    /// <summary>The zero Variant: the catalog itself, macOS's.</summary>
    public static ToolbarVariant Zero { get; } = new();

    /// <summary>
    /// toolbar.GTK: no message filter and no sidebar toggle in Mail and the
    /// Board's List; Mail's search may stand in the list's header; the
    /// defaults are GTK's header bars. Do not modify.
    /// </summary>
    public static ToolbarVariant Gtk { get; } = new()
    {
        Omit = new Dictionary<Bar, IReadOnlyList<string>>
        {
            [Bar.Mail] = [Filter, ToggleSidebar],
            [Bar.BoardList] = [ToggleSidebar],
        },
        Extra = new Dictionary<Bar, IReadOnlyDictionary<string, IReadOnlyList<Section>>>
        {
            [Bar.Mail] = new Dictionary<string, IReadOnlyList<Section>>(StringComparer.Ordinal) { [Search] = [Section.List] },
        },
        Defaults = new Dictionary<Bar, IReadOnlyList<string>>
        {
            [Bar.Mail] =
            [
                Mode, NewMessage, FlexibleSpace,
                Separator(Section.List), Refresh, FlexibleSpace, Search,
                Separator(Section.Message), Reply, ReplyAll, Forward, FlexibleSpace,
                Archive, Trash, Space, Crm, Space, Star, Assistant, MoreActions,
            ],
            [Bar.BoardList] =
            [
                Mode, BoardStyle, FlexibleSpace,
                Separator(Section.List), BoardTriage, BoardAccount,
                Separator(Section.Detail), BoardDone, BoardRemind, BoardArchive, BoardTrash, FlexibleSpace, BoardReply,
            ],
            [Bar.BoardFlow] =
            [
                Mode, BoardStyle, FlexibleSpace, BoardTriage, BoardAccount,
            ],
            [Bar.Calendar] =
            [
                Mode,
                Separator(Section.Main), ToggleSidebar, CalendarToday, CalendarStep, CalendarView, FlexibleSpace,
                CalendarSearch, CalendarNewEvent, CalendarAssistant,
            ],
        },
    };

    /// <summary>
    /// toolbar.Windows: every item (the mode switch, Mail's search and filter
    /// and the Calendar's search stand in fixed places and count only by
    /// being in the layout); Mail's search may stand in the list section as
    /// GTK's does; the defaults are today's bars: the sidebar's row (New
    /// Message), the list's header (Check for New Mail; the filter row),
    /// MessageCommandBar, BoardPage.Bar, BoardDetailView's actions and
    /// CalendarBar. Do not modify.
    /// </summary>
    public static ToolbarVariant Windows { get; } = new()
    {
        Extra = new Dictionary<Bar, IReadOnlyDictionary<string, IReadOnlyList<Section>>>
        {
            [Bar.Mail] = new Dictionary<string, IReadOnlyList<Section>>(StringComparer.Ordinal) { [Search] = [Section.List] },
        },
        Defaults = new Dictionary<Bar, IReadOnlyList<string>>
        {
            [Bar.Mail] =
            [
                Mode, NewMessage,
                Separator(Section.List), Refresh, Filter,
                Separator(Section.Message), Reply, ReplyAll, Forward, FlexibleSpace,
                Archive, Trash, Space, Crm, Space, Star, Assistant, MoreActions, Search,
            ],
            [Bar.BoardList] =
            [
                Mode, BoardStyle, FlexibleSpace,
                Separator(Section.List), BoardAccount, BoardTriage,
                Separator(Section.Detail), BoardDone, BoardRemind, BoardArchive, BoardTrash, FlexibleSpace, BoardReply,
            ],
            [Bar.BoardFlow] =
            [
                Mode, BoardStyle, FlexibleSpace, BoardAccount, BoardTriage,
            ],
            [Bar.Calendar] =
            [
                Mode,
                Separator(Section.Main), ToggleSidebar, CalendarToday, CalendarStep, CalendarView, FlexibleSpace,
                CalendarSearch, CalendarNewEvent, CalendarAssistant,
            ],
        },
    };

    /// <summary>Variant.Omit: the items of each toolbar the client has no widget for.</summary>
    public IReadOnlyDictionary<Bar, IReadOnlyList<string>> Omit { get; init; } = new Dictionary<Bar, IReadOnlyList<string>>();

    /// <summary>Variant.Extra: sections an item may stand in besides the catalog's (the home stays the catalog's).</summary>
    public IReadOnlyDictionary<Bar, IReadOnlyDictionary<string, IReadOnlyList<Section>>> Extra { get; init; } =
        new Dictionary<Bar, IReadOnlyDictionary<string, IReadOnlyList<Section>>>();

    /// <summary>Variant.Default: the client's default arrangement of a toolbar; a missing one is Toolbar.DefaultLayout without the omitted items.</summary>
    public IReadOnlyDictionary<Bar, IReadOnlyList<string>> Defaults { get; init; } = new Dictionary<Bar, IReadOnlyList<string>>();

    /// <summary>Variant.Omits: whether the client has no <paramref name="id"/> in <paramref name="bar"/>.</summary>
    public bool Omits(Bar bar, string id) =>
        Omit.TryGetValue(bar, out var ids) && ids.Contains(id, StringComparer.Ordinal);

    /// <summary>The extra sections of <paramref name="id"/> in <paramref name="bar"/>, empty for none.</summary>
    public IReadOnlyList<Section> ExtraSections(Bar bar, string id)
    {
        if (Extra.TryGetValue(bar, out var m) && m.TryGetValue(id, out var secs))
        {
            return secs;
        }
        return [];
    }
}
