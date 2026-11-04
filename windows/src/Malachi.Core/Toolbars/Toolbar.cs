// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/toolbar/toolbar.go (Bar, Bars, Key, DisplayModeKey,
// DisplayMode, ParseDisplayMode, the ids, Section, Separator, IsSeparator,
// IsSpace, Feature, the priorities, Item, the catalogs, the sections, the
// defaults, Catalog, Sections, Lookup, Layout, Default, Tokens, Equal,
// Stored, MaxTokens, Normalize, SectionAt, CanInsert, Immovable, Features,
// Allowed, Shown, Present, MissingActions, SearchItem, SearchIndex);
// macOS: MalachiCore/Toolbar/Toolbar.swift. The variants (variant.go) are
// Toolbar.Variant.cs, Fit (fit.go) Toolbar.Fit.cs, the editor's texts
// (text.go) Toolbar.Text.cs; ui/internal/toolbaredit is ToolbarEditor.
//
// The user's arrangement of the main window's toolbars
// (docs/plans/toolbar-customization.md, decisions T1–T15): the catalog of
// each toolbar, the default arrangement, the sections an item may stand in,
// how a stored token list becomes a valid arrangement, what a toolbar shows
// of it for the features at hand, and which actions More Actions adds. Pure
// logic, no widgets.
//
// Windows: the namespace is Toolbars, as Boards is (a namespace Toolbar
// would hide the class). An id is a string (Go's ID, Swift's
// Toolbar.ID), a layout an IReadOnlyList<string> (Go's Layout; its Equal is
// LayoutEqual, its Tokens ToArray). Go's Default(bar) is DefaultLayout, as
// in Swift (Default is a Visual Basic keyword, CA1716). Bar and Section are
// enums whose nicks (Nick) are Go's strings.

using System;
using System.Collections.Generic;
using System.Linq;

namespace Malachi.Core.Toolbars;

/// <summary>ui/internal/toolbar: the catalog, defaults and rules of the main window's toolbars.</summary>
public static partial class Toolbar
{
    /// <summary>toolbar.DisplayModeKey: the settings key of the display mode, one for every toolbar (O4).</summary>
    public const string DisplayModeKey = "toolbar-display-mode";

    /// <summary>toolbar.MaxTokens: what Normalize reads of a stored list at most.</summary>
    public const int MaxTokens = 128;

    // The priorities of an item when the toolbar runs out of room, as
    // AppKit's NSToolbarItem.VisibilityPriority: higher stays longer (T5).

    /// <summary>toolbar.PriorityUser: never gives way.</summary>
    public const int PriorityUser = 2000;

    /// <summary>toolbar.PriorityHigh.</summary>
    public const int PriorityHigh = 1000;

    /// <summary>toolbar.PriorityStandard.</summary>
    public const int PriorityStandard = 0;

    /// <summary>toolbar.PrioritySearch.</summary>
    public const int PrioritySearch = -500;

    /// <summary>toolbar.PriorityLow.</summary>
    public const int PriorityLow = -1000;

    // The toolbar's own items. A space may stand any number of times.

    /// <summary>toolbar.Space: a fixed gap.</summary>
    public const string Space = "space";

    /// <summary>toolbar.FlexibleSpace: a gap that takes the free room.</summary>
    public const string FlexibleSpace = "flexibleSpace";

    /// <summary>toolbar.ToggleSidebar.</summary>
    public const string ToggleSidebar = "toggleSidebar";

    // The items of the Mail toolbar.

    /// <summary>toolbar.Mode: the Mail/Board/Calendar switch (fixed).</summary>
    public const string Mode = "mode";

    /// <summary>toolbar.NewMessage.</summary>
    public const string NewMessage = "newMessage";

    /// <summary>toolbar.Contacts.</summary>
    public const string Contacts = "contacts";

    /// <summary>toolbar.Filter.</summary>
    public const string Filter = "filter";

    /// <summary>toolbar.Refresh.</summary>
    public const string Refresh = "refresh";

    /// <summary>toolbar.Reply.</summary>
    public const string Reply = "reply";

    /// <summary>toolbar.ReplyAll.</summary>
    public const string ReplyAll = "replyAll";

    /// <summary>toolbar.Forward.</summary>
    public const string Forward = "forward";

    /// <summary>toolbar.Archive.</summary>
    public const string Archive = "archive";

    /// <summary>toolbar.Trash.</summary>
    public const string Trash = "trash";

    /// <summary>toolbar.CRM.</summary>
    public const string Crm = "crm";

    /// <summary>toolbar.Star.</summary>
    public const string Star = "star";

    /// <summary>toolbar.Assistant.</summary>
    public const string Assistant = "assistant";

    /// <summary>toolbar.MoreActions.</summary>
    public const string MoreActions = "moreActions";

    /// <summary>toolbar.Search.</summary>
    public const string Search = "search";

    /// <summary>toolbar.MarkRead: one toggling button (O3).</summary>
    public const string MarkRead = "markRead";

    /// <summary>toolbar.Junk.</summary>
    public const string Junk = "junk";

    /// <summary>toolbar.MoveTo.</summary>
    public const string MoveTo = "moveTo";

    /// <summary>toolbar.Print.</summary>
    public const string Print = "print";

    /// <summary>toolbar.Undo.</summary>
    public const string Undo = "undo";

    /// <summary>toolbar.LoadPictures.</summary>
    public const string LoadPictures = "loadPictures";

    // The items of the Board's toolbars.

    /// <summary>toolbar.BoardStyle.</summary>
    public const string BoardStyle = "boardStyle";

    /// <summary>toolbar.BoardAccount.</summary>
    public const string BoardAccount = "boardAccount";

    /// <summary>toolbar.BoardTriage.</summary>
    public const string BoardTriage = "boardTriage";

    /// <summary>toolbar.BoardDone.</summary>
    public const string BoardDone = "boardDone";

    /// <summary>toolbar.BoardRemind.</summary>
    public const string BoardRemind = "boardRemind";

    /// <summary>toolbar.BoardArchive.</summary>
    public const string BoardArchive = "boardArchive";

    /// <summary>toolbar.BoardTrash.</summary>
    public const string BoardTrash = "boardTrash";

    /// <summary>toolbar.BoardReply.</summary>
    public const string BoardReply = "boardReply";

    // The items of the Calendar's toolbar.

    /// <summary>toolbar.CalendarToday.</summary>
    public const string CalendarToday = "calendarToday";

    /// <summary>toolbar.CalendarStep.</summary>
    public const string CalendarStep = "calendarStep";

    /// <summary>toolbar.CalendarView.</summary>
    public const string CalendarView = "calendarView";

    /// <summary>toolbar.CalendarSearch.</summary>
    public const string CalendarSearch = "calendarSearch";

    /// <summary>toolbar.CalendarNewEvent.</summary>
    public const string CalendarNewEvent = "calendarNewEvent";

    /// <summary>toolbar.CalendarAssistant.</summary>
    public const string CalendarAssistant = "calendarAssistant";

    /// <summary>toolbar.Bar: one of the main window's customizable toolbars.</summary>
    public enum Bar
    {
        /// <summary>The Mail mode's ("mail").</summary>
        Mail,

        /// <summary>The Board's in the List style ("board-list").</summary>
        BoardList,

        /// <summary>The Board's in the Columns and Today styles ("board-flow").</summary>
        BoardFlow,

        /// <summary>The Calendar's ("calendar").</summary>
        Calendar,
    }

    /// <summary>toolbar.Section: a part of a toolbar between two separators; a toolbar without separators is Main.</summary>
    public enum Section
    {
        /// <summary>"sidebar".</summary>
        Sidebar,

        /// <summary>"list".</summary>
        List,

        /// <summary>"message".</summary>
        Message,

        /// <summary>"detail".</summary>
        Detail,

        /// <summary>"main".</summary>
        Main,
    }

    /// <summary>toolbar.Feature: what a conditional item needs to be shown (T7).</summary>
    public enum Feature
    {
        /// <summary>An eWay-CRM account exists.</summary>
        Crm,

        /// <summary>The Assistant menu is on (assistant-menu and the registration in Claude).</summary>
        Assistant,

        /// <summary>The board's triage can run (the In App target).</summary>
        Triage,

        /// <summary>Printing exists in this client.</summary>
        Print,
    }

    /// <summary>toolbar.DisplayMode: what the toolbar's items show (the gschema enum ToolbarDisplayMode).</summary>
    public enum DisplayMode
    {
        /// <summary>"icon": icons only.</summary>
        Icon,

        /// <summary>"icon-and-label".</summary>
        IconAndLabel,

        /// <summary>"label": text only.</summary>
        Label,
    }

    /// <summary>toolbar.Bars: the customizable toolbars in the order of the modes.</summary>
    public static IReadOnlyList<Bar> Bars { get; } = [Bar.Mail, Bar.BoardList, Bar.BoardFlow, Bar.Calendar];

    private static readonly Section[] MailNav = [Section.List, Section.Sidebar];
    private static readonly Section[] MailMessage = [Section.Message];
    private static readonly Section[] BoardNav = [Section.List, Section.Sidebar];
    private static readonly Section[] CalNav = [Section.Main, Section.Sidebar];
    private static readonly Section[] Detail = [Section.Detail];
    private static readonly Section[] MainOnly = [Section.Main];

    private static readonly Dictionary<Bar, Item[]> Catalogs = new()
    {
        [Bar.Mail] =
        [
            new(Mode, [Section.Sidebar], Fixed: true, Priority: PriorityUser),
            new(ToggleSidebar, MailNav, Priority: PriorityHigh),
            new(NewMessage, MailNav),
            new(Contacts, MailNav, Priority: PriorityLow),
            new(Filter, MailNav),
            new(Refresh, MailNav),
            Msg(Reply, true, PriorityStandard, null),
            Msg(ReplyAll, true, PriorityStandard, null),
            Msg(Forward, true, PriorityStandard, null),
            Msg(Archive, true, PriorityStandard, null),
            Msg(Trash, true, PriorityStandard, null),
            Msg(Crm, true, PriorityStandard, Feature.Crm),
            Msg(Star, true, PriorityStandard, null),
            Msg(Assistant, false, PriorityStandard, Feature.Assistant),
            Msg(MoreActions, false, PriorityHigh, null),
            Msg(Search, false, PriorityStandard, null),
            // The palette's items (T10) are in the menus already, and an
            // untouched toolbar keeps its More Actions as it was: not InMore.
            Msg(MarkRead, false, PriorityLow, null),
            Msg(Junk, false, PriorityLow, null),
            Msg(MoveTo, false, PriorityLow, null),
            Msg(Print, false, PriorityLow, Feature.Print),
            Msg(Undo, false, PriorityLow, null),
            Msg(LoadPictures, false, PriorityLow, null),
        ],
        [Bar.BoardList] =
        [
            new(Mode, [Section.Sidebar], Fixed: true, Priority: PriorityUser),
            new(ToggleSidebar, BoardNav, Priority: PriorityHigh),
            new(BoardStyle, BoardNav, Priority: PriorityHigh),
            new(Contacts, BoardNav, Priority: PriorityLow),
            new(BoardAccount, BoardNav),
            new(BoardTriage, BoardNav, Feature: Feature.Triage, Priority: PriorityLow),
            new(BoardDone, Detail, Priority: PriorityHigh),
            new(BoardRemind, Detail, Priority: PriorityLow),
            new(BoardArchive, Detail, Priority: PriorityLow),
            new(BoardTrash, Detail, Priority: PriorityLow),
            new(BoardReply, Detail),
        ],
        [Bar.BoardFlow] =
        [
            new(Mode, MainOnly, Fixed: true, Priority: PriorityUser),
            new(BoardStyle, MainOnly, Priority: PriorityHigh),
            new(Contacts, MainOnly, Priority: PriorityLow),
            new(BoardAccount, MainOnly),
            new(BoardTriage, MainOnly, Feature: Feature.Triage, Priority: PriorityLow),
        ],
        [Bar.Calendar] =
        [
            new(Mode, [Section.Sidebar], Fixed: true, Priority: PriorityUser),
            new(ToggleSidebar, CalNav, Priority: PriorityHigh),
            new(CalendarToday, CalNav, Priority: PriorityHigh),
            new(CalendarStep, CalNav, Priority: PriorityHigh),
            new(CalendarView, CalNav),
            new(CalendarSearch, CalNav, Priority: PrioritySearch),
            new(Contacts, CalNav),
            new(CalendarNewEvent, CalNav, Priority: PriorityHigh),
            new(CalendarAssistant, CalNav, Feature: Feature.Assistant, Priority: PriorityLow),
            new(Print, CalNav, Feature: Feature.Print, Priority: PriorityLow),
        ],
    };

    // The sections of each toolbar in order; every one but the first starts
    // with its separator.
    private static readonly Dictionary<Bar, Section[]> SectionsOf = new()
    {
        [Bar.Mail] = [Section.Sidebar, Section.List, Section.Message],
        [Bar.BoardList] = [Section.Sidebar, Section.List, Section.Detail],
        [Bar.BoardFlow] = [Section.Main],
        [Bar.Calendar] = [Section.Sidebar, Section.Main],
    };

    // The default arrangements, the macOS toolbars as they were before
    // customization; the new items of T10 are in the palette only.
    private static readonly Dictionary<Bar, string[]> Defaults = new()
    {
        [Bar.Mail] =
        [
            Mode, FlexibleSpace,
            Separator(Section.List), ToggleSidebar, NewMessage, Contacts, Filter, Refresh, FlexibleSpace,
            Separator(Section.Message), Reply, ReplyAll, Forward, FlexibleSpace,
            Archive, Trash, Space, Crm, Space, Star, Assistant, MoreActions, Search,
        ],
        [Bar.BoardList] =
        [
            Mode, FlexibleSpace,
            Separator(Section.List), ToggleSidebar, BoardStyle, Contacts, FlexibleSpace, BoardAccount, BoardTriage,
            Separator(Section.Detail), BoardDone, BoardRemind, BoardArchive, BoardTrash, FlexibleSpace, BoardReply,
        ],
        [Bar.BoardFlow] =
        [
            Mode, BoardStyle, Contacts, FlexibleSpace, BoardAccount, BoardTriage,
        ],
        [Bar.Calendar] =
        [
            Mode, FlexibleSpace,
            Separator(Section.Main), ToggleSidebar, CalendarToday, CalendarStep, CalendarView, FlexibleSpace,
            CalendarSearch, Contacts, CalendarNewEvent, CalendarAssistant,
        ],
    };

    /// <summary>The nick of <paramref name="bar"/> (Go's string): mail, board-list, board-flow, calendar.</summary>
    public static string Nick(Bar bar) => bar switch
    {
        Bar.BoardList => "board-list",
        Bar.BoardFlow => "board-flow",
        Bar.Calendar => "calendar",
        _ => "mail",
    };

    /// <summary>The nick of <paramref name="s"/> (Go's string): sidebar, list, message, detail, main.</summary>
    public static string Nick(Section s) => s switch
    {
        Section.Sidebar => "sidebar",
        Section.List => "list",
        Section.Message => "message",
        Section.Detail => "detail",
        _ => "main",
    };

    /// <summary>The nick of <paramref name="m"/>, the gschema's: icon, icon-and-label, label.</summary>
    public static string Nick(DisplayMode m) => m switch
    {
        DisplayMode.IconAndLabel => "icon-and-label",
        DisplayMode.Label => "label",
        _ => "icon",
    };

    /// <summary>toolbar.Key: the settings key (gschema, type as) of <paramref name="bar"/>'s arrangement.</summary>
    public static string Key(Bar bar) => "toolbar-" + Nick(bar);

    /// <summary>toolbar.ParseDisplayMode: the key's nick; anything else is Icon.</summary>
    public static DisplayMode ParseDisplayMode(string? s) => s switch
    {
        "icon-and-label" => DisplayMode.IconAndLabel,
        "label" => DisplayMode.Label,
        _ => DisplayMode.Icon,
    };

    /// <summary>toolbar.Separator: the token of the separator that starts <paramref name="s"/>.</summary>
    public static string Separator(Section s) => "|" + Nick(s);

    /// <summary>toolbar.IsSeparator: whether <paramref name="id"/> is a section's separator.</summary>
    public static bool IsSeparator(string id) => id is not null && id.StartsWith('|');

    /// <summary>toolbar.IsSpace: whether <paramref name="id"/> is a space, which may stand anywhere and any number of times.</summary>
    public static bool IsSpace(string id) => id is Space or FlexibleSpace;

    /// <summary>The section a separator's token names, false for none of <paramref name="bar"/>'s.</summary>
    public static bool TryParseSeparator(Bar bar, string token, out Section section)
    {
        section = Section.Main;
        if (!IsSeparator(token))
        {
            return false;
        }
        var nick = token[1..];
        foreach (var s in SectionsOf[bar])
        {
            if (string.Equals(Nick(s), nick, StringComparison.Ordinal))
            {
                section = s;
                return true;
            }
        }
        return false;
    }

    /// <summary>toolbar.Catalog: every item <paramref name="bar"/> can hold, spaces and separators aside, in the palette's order.</summary>
    public static IReadOnlyList<Item> Catalog(Bar bar) => [.. Catalogs[bar]];

    /// <summary>toolbar.Sections: <paramref name="bar"/>'s sections in order.</summary>
    public static IReadOnlyList<Section> Sections(Bar bar) => [.. SectionsOf[bar]];

    /// <summary>toolbar.Lookup: <paramref name="id"/>'s entry in <paramref name="bar"/>'s catalog, null for none.</summary>
    public static Item? Lookup(Bar bar, string id)
    {
        foreach (var it in Catalogs[bar])
        {
            if (string.Equals(it.Id, id, StringComparison.Ordinal))
            {
                return it;
            }
        }
        return null;
    }

    /// <summary>toolbar.Default: <paramref name="bar"/>'s default arrangement (macOS's).</summary>
    public static IReadOnlyList<string> DefaultLayout(Bar bar) => [.. Defaults[bar]];

    /// <summary>Layout.Equal: whether two layouts are the same.</summary>
    public static bool LayoutEqual(IReadOnlyList<string> a, IReadOnlyList<string> b)
    {
        ArgumentNullException.ThrowIfNull(a);
        ArgumentNullException.ThrowIfNull(b);
        if (a.Count != b.Count)
        {
            return false;
        }
        for (var i = 0; i < a.Count; i++)
        {
            if (!string.Equals(a[i], b[i], StringComparison.Ordinal))
            {
                return false;
            }
        }
        return true;
    }

    /// <summary>
    /// toolbar.Stored: what the settings key keeps for <paramref name="l"/>:
    /// nothing for <paramref name="bar"/>'s default, so an untouched toolbar
    /// follows the default of later versions.
    /// </summary>
    public static IReadOnlyList<string> Stored(Bar bar, IReadOnlyList<string> l)
    {
        var n = Normalize(bar, l);
        if (LayoutEqual(n, DefaultLayout(bar)))
        {
            return [];
        }
        return n;
    }

    /// <summary>toolbar.Normalize: NormalizeFor with the zero variant (macOS).</summary>
    public static IReadOnlyList<string> Normalize(Bar bar, IReadOnlyList<string>? stored) => NormalizeFor(ToolbarVariant.Zero, bar, stored);

    /// <summary>
    /// toolbar.SectionAt: the section of the item at index <paramref name="i"/>
    /// of <paramref name="l"/> (a separator belongs to the section it starts).
    /// </summary>
    public static Section SectionAt(Bar bar, IReadOnlyList<string> l, int i)
    {
        ArgumentNullException.ThrowIfNull(l);
        var secs = SectionsOf[bar];
        if (secs.Length == 0)
        {
            return Section.Main;
        }
        var s = secs[0];
        for (var j = 0; j <= i && j < l.Count; j++)
        {
            if (TryParseSeparator(bar, l[j], out var x))
            {
                s = x;
            }
        }
        return s;
    }

    /// <summary>toolbar.CanInsert: CanInsertFor with the zero variant.</summary>
    public static bool CanInsert(Bar bar, string id, Section s) => CanInsertFor(ToolbarVariant.Zero, bar, id, s);

    /// <summary>toolbar.Immovable: ImmovableFor with the zero variant.</summary>
    public static IReadOnlyList<string> Immovable(Bar bar) => ImmovableFor(ToolbarVariant.Zero, bar);

    /// <summary>toolbar.Allowed: AllowedFor with the zero variant.</summary>
    public static IReadOnlyList<string> Allowed(Bar bar, Features f) => AllowedFor(ToolbarVariant.Zero, bar, f);

    /// <summary>toolbar.Present: PresentFor with the zero variant.</summary>
    public static IReadOnlyList<Shown> Present(Bar bar, IReadOnlyList<string> l, Features f) => PresentFor(ToolbarVariant.Zero, bar, l, f);

    /// <summary>toolbar.MissingActions: MissingActionsFor with the zero variant.</summary>
    public static IReadOnlyList<string> MissingActions(Bar bar, IReadOnlyList<string> l, Features f) =>
        MissingActionsFor(ToolbarVariant.Zero, bar, l, f);

    /// <summary>toolbar.SearchItem: <paramref name="bar"/>'s search field, "" for a toolbar without one.</summary>
    public static string SearchItem(Bar bar) => bar switch
    {
        Bar.Mail => Search,
        Bar.Calendar => CalendarSearch,
        _ => "",
    };

    /// <summary>
    /// toolbar.SearchIndex: where Ctrl+F puts <paramref name="bar"/>'s search
    /// field into <paramref name="l"/> for the time of a search while the
    /// user took it off (O5): the end of its home section; -1 while
    /// <paramref name="l"/> has it or the bar has none.
    /// </summary>
    public static int SearchIndex(Bar bar, IReadOnlyList<string> l)
    {
        ArgumentNullException.ThrowIfNull(l);
        var id = SearchItem(bar);
        if (id.Length == 0 || l.Contains(id, StringComparer.Ordinal))
        {
            return -1;
        }
        var home = Lookup(bar, id)!.Home;
        for (var i = 0; i < l.Count; i++)
        {
            if (IsSeparator(l[i]) && SectionAfter(bar, home, SectionAt(bar, l, i)))
            {
                return i;
            }
        }
        return l.Count;
    }

    // msg: a message action of the Mail toolbar.
    private static Item Msg(string id, bool inMore, int priority, Feature? feature) =>
        new(id, MailMessage, Feature: feature, InMore: inMore, Priority: priority);

    // sectionAfter: whether b comes after a in bar's order.
    private static bool SectionAfter(Bar bar, Section a, Section b)
    {
        var secs = SectionsOf[bar];
        return Array.IndexOf(secs, b) > Array.IndexOf(secs, a);
    }

    /// <summary>
    /// toolbar.Item: an entry of a toolbar's catalog. <paramref name="Sections"/>
    /// are where it may stand, the first its home; <paramref name="Fixed"/>:
    /// the user can neither remove nor move it (T4); <paramref name="Feature"/>
    /// is what it needs to be shown, null for nothing; <paramref name="InMore"/>:
    /// an action More Actions offers while the toolbar does not show it (T8).
    /// </summary>
    public sealed record Item(
        string Id,
        IReadOnlyList<Section> Sections,
        bool Fixed = false,
        Feature? Feature = null,
        bool InMore = false,
        int Priority = PriorityStandard)
    {
        /// <summary>Item.Home: the section Normalize puts the item in.</summary>
        public Section Home => Sections[0];

        /// <summary>Whether the item may stand in <paramref name="s"/>.</summary>
        public bool Allows(Section s) => Sections.Contains(s);
    }

    /// <summary>
    /// toolbar.Features: what the window has at the moment: the features that
    /// exist (T7), separators left out while their section's items still show,
    /// and sections left out with their items (the Board List's detail while
    /// no case is shown).
    /// </summary>
    public sealed record Features
    {
        /// <summary>No feature, nothing hidden.</summary>
        public static Features None { get; } = new();

        /// <summary>The features that exist.</summary>
        public IReadOnlySet<Feature> Have { get; init; } = new HashSet<Feature>();

        /// <summary>Separators the toolbar leaves out while their section's items still show.</summary>
        public IReadOnlySet<string> HiddenSeparators { get; init; } = new HashSet<string>(StringComparer.Ordinal);

        /// <summary>Sections left out with their items.</summary>
        public IReadOnlySet<Section> HiddenSections { get; init; } = new HashSet<Section>();

        /// <summary>Features with <paramref name="have"/>, nothing hidden.</summary>
        public static Features With(params Feature[] have) => new() { Have = new HashSet<Feature>(have) };
    }

    /// <summary>toolbar.Shown: an entry of what the toolbar holds; Hidden: it keeps its place but is not shown, its feature missing (T7).</summary>
    public readonly record struct Shown(string Id, bool Hidden = false);
}
