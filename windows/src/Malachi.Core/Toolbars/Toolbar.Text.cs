// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/toolbar/text.go (GroupTitle, CustomizeTitle,
// DialogTitle, DisplayModeTitle, DisplayModes, DisplayModeLabel, BarTitle,
// SectionTitle, ItemLabel, UnavailableSubtitle, AddItemTitle, RemoveTitle,
// RestoreTitle, RestoreQuestion, RestoreBody, RestoreCancel,
// StripMnemonic): the texts of the toolbar editor and of the buttons'
// labels in the Icons and Text and Text Only display modes, with the GTK
// msgids. Go's Translator is L10n here.

using System.Collections.Generic;
using System.Text;
using Malachi.Core.I18n;

namespace Malachi.Core.Toolbars;

public static partial class Toolbar
{
    /// <summary>toolbar.DisplayModes: the display mode's choices in the order the row offers them.</summary>
    public static IReadOnlyList<DisplayMode> DisplayModes { get; } = [DisplayMode.Icon, DisplayMode.IconAndLabel, DisplayMode.Label];

    /// <summary>toolbar.GroupTitle: the Toolbar group in Preferences → General (T13).</summary>
    public static string GroupTitle() =>
        // TRANSLATORS: a group in Preferences → General with the toolbar's display mode and its editor.
        L10n.T("Toolbar");

    /// <summary>toolbar.CustomizeTitle: the group's button that opens the editor, and the item of a toolbar's context menu that does (T15).</summary>
    public static string CustomizeTitle() =>
        // TRANSLATORS: a button in Preferences and a context menu item of a toolbar; it opens the toolbar editor.
        L10n.T("Customize Toolbar…");

    /// <summary>toolbar.DialogTitle: the title of the editor's dialog.</summary>
    public static string DialogTitle() =>
        // TRANSLATORS: the title of the dialog where the user arranges a toolbar's items.
        L10n.T("Customize Toolbar");

    /// <summary>toolbar.DisplayModeTitle: the row with the display mode.</summary>
    public static string DisplayModeTitle() =>
        // TRANSLATORS: a row in Preferences: whether the toolbar's buttons show icons, text or both.
        L10n.T("Toolbar Display");

    /// <summary>toolbar.DisplayModeLabel: the choice <paramref name="m"/> of the display mode row.</summary>
    public static string DisplayModeLabel(DisplayMode m) => m switch
    {
        // TRANSLATORS: a choice of the Toolbar Display row: buttons with an icon and a label.
        DisplayMode.IconAndLabel => L10n.T("Icons and Text"),
        // TRANSLATORS: a choice of the Toolbar Display row: buttons with a label and no icon.
        DisplayMode.Label => L10n.T("Text Only"),
        // TRANSLATORS: a choice of the Toolbar Display row: buttons with an icon and no label.
        _ => L10n.T("Icons Only"),
    };

    /// <summary>toolbar.BarTitle: <paramref name="bar"/>'s name in the editor's bar chooser.</summary>
    public static string BarTitle(Bar bar) => bar switch
    {
        // TRANSLATORS: the toolbar of the Board in its List style, in the toolbar editor.
        Bar.BoardList => L10n.T("Board – List"),
        // TRANSLATORS: the toolbar of the Board in its Columns and Today styles, in the toolbar editor.
        Bar.BoardFlow => L10n.T("Board – Columns and Today"),
        Bar.Calendar => L10n.T("Calendar"),
        _ => L10n.T("Mail"),
    };

    /// <summary>toolbar.SectionTitle: the heading of section <paramref name="s"/> of <paramref name="bar"/> in the editor.</summary>
    public static string SectionTitle(Bar bar, Section s) => s switch
    {
        Section.Sidebar => bar == Bar.Mail ? L10n.T("Folders") : SidebarText(),
        // TRANSLATORS: the part of the Board's toolbar over its list of cases, in the toolbar editor.
        Section.List => bar == Bar.Mail ? L10n.T("Message List") : L10n.T("Cases"),
        Section.Message => L10n.T("Message"),
        // TRANSLATORS: the part of the Board's toolbar over the shown case, in the toolbar editor.
        Section.Detail => L10n.T("Case Detail"),
        _ => bar == Bar.Calendar ? L10n.T("Calendar") : L10n.T("Board"),
    };

    /// <summary>
    /// toolbar.ItemLabel: <paramref name="id"/>'s label in the editor (and of
    /// a button in the Icons and Text and Text Only display modes); an
    /// unknown id is itself.
    /// </summary>
    public static string ItemLabel(Bar bar, string id)
    {
        _ = bar;
        switch (id)
        {
            case Space:
                // TRANSLATORS: a fixed gap between toolbar buttons, in the toolbar editor.
                return L10n.T("Space");
            case FlexibleSpace:
                // TRANSLATORS: a gap that takes the toolbar's free room, in the toolbar editor.
                return L10n.T("Flexible Space");
            case ToggleSidebar:
                return SidebarText();
            case Mode:
                // TRANSLATORS: the toolbar's switch between Mail, Board and Calendar, in the toolbar editor.
                return L10n.T("Mode Switch");
            case NewMessage:
                return L10n.T("New Message");
            case Contacts:
                return L10n.T("Contacts");
            case Filter:
                // TRANSLATORS: the toolbar item that filters the message list (All, Unread, Flagged), in the toolbar editor.
                return L10n.T("Filter");
            case Refresh:
                return L10n.T("Check for New Mail");
            case Reply or BoardReply:
                return L10n.T("Reply");
            case ReplyAll:
                return L10n.T("Reply All");
            case Forward:
                return L10n.T("Forward");
            case Archive or BoardArchive:
                return L10n.T("Archive");
            case Trash:
                return L10n.T("Move to Trash");
            case Crm:
                return L10n.T("Save to eWay-CRM…");
            case Star:
                return L10n.T("Star");
            case Assistant or CalendarAssistant:
                return L10n.T("Assistant");
            case MoreActions:
                return L10n.T("More Actions");
            case Search:
                return L10n.T("Search");
            case MarkRead:
                // One toggling button (O3), named as its first state.
                return StripMnemonic(L10n.T("Mark as _Read"));
            case Junk:
                return L10n.T("Mark as Junk");
            case MoveTo:
                return L10n.T("Move To");
            case Print:
                return L10n.T("Print");
            case Undo:
                return L10n.T("Undo");
            case LoadPictures:
                return StripMnemonic(L10n.T("Load _Images"));
            case BoardStyle:
                // TRANSLATORS: the Board's switch between its List, Columns and Today styles, in the toolbar editor.
                return L10n.T("Board Style");
            case BoardAccount:
                return L10n.C("account filter", "All Accounts");
            case BoardTriage:
                return L10n.T("✦ Triage");
            case BoardDone:
                return L10n.T("Done");
            case BoardRemind:
                return L10n.T("Remind…");
            case BoardTrash:
                return L10n.T("Delete");
            case CalendarToday:
                return L10n.T("Today");
            case CalendarStep:
                // TRANSLATORS: the Calendar's pair of buttons that step to the previous and next day, week or month, in the toolbar editor.
                return L10n.T("Previous and Next");
            case CalendarView:
                // TRANSLATORS: the Calendar's switch between Day, Week, Month and Agenda, in the toolbar editor.
                return L10n.T("Calendar View");
            case CalendarSearch:
                return L10n.T("Search Events");
            case CalendarNewEvent:
                return L10n.T("New Event");
        }
        return id;
    }

    /// <summary>toolbar.UnavailableSubtitle: the subtitle of an item whose feature is missing (T7).</summary>
    public static string UnavailableSubtitle() =>
        // TRANSLATORS: the subtitle of a toolbar item in the editor whose feature is off at the moment (e.g. no eWay-CRM account).
        L10n.T("Hidden while unavailable");

    /// <summary>toolbar.AddItemTitle: the editor's button that offers the items to add.</summary>
    public static string AddItemTitle() =>
        // TRANSLATORS: a button of the toolbar editor that opens the list of items to add.
        L10n.T("Add Item…");

    /// <summary>toolbar.RemoveTitle: the editor's button that takes an item off the toolbar.</summary>
    public static string RemoveTitle() => L10n.T("Remove");

    /// <summary>toolbar.RestoreTitle: the editor's Restore Defaults button and the confirmation's button.</summary>
    public static string RestoreTitle() =>
        // TRANSLATORS: a button of the toolbar editor that gives the chosen toolbar its default items back.
        L10n.T("Restore Defaults");

    /// <summary>toolbar.RestoreQuestion: the confirmation's heading.</summary>
    public static string RestoreQuestion() =>
        // TRANSLATORS: the question before the toolbar editor restores the default toolbar.
        L10n.T("Restore the Default Toolbar?");

    /// <summary>toolbar.RestoreBody: the confirmation's text for <paramref name="bar"/>.</summary>
    public static string RestoreBody(Bar bar) =>
        // TRANSLATORS: %s is the toolbar's name, e.g. "Mail" or "Board – List".
        L10n.T("Your changes to the “%s” toolbar will be lost.", BarTitle(bar));

    /// <summary>toolbar.RestoreCancel: the confirmation's Cancel.</summary>
    public static string RestoreCancel() => L10n.T("Cancel");

    /// <summary>toolbar.StripMnemonic: the mnemonic underscores out of a menu label ("__" stands for "_").</summary>
    public static string StripMnemonic(string s)
    {
        if (s is null || !s.Contains('_', System.StringComparison.Ordinal))
        {
            return s ?? "";
        }
        var b = new StringBuilder(s.Length);
        for (var i = 0; i < s.Length; i++)
        {
            if (s[i] == '_')
            {
                if (i + 1 < s.Length && s[i + 1] == '_')
                {
                    b.Append('_');
                    i++;
                }
                continue;
            }
            b.Append(s[i]);
        }
        return b.ToString();
    }

    // The sidebar toggle and the part of a toolbar over the sidebar.
    private static string SidebarText() =>
        // TRANSLATORS: in the toolbar editor, the button that shows or hides the sidebar and the part of a toolbar over the sidebar.
        L10n.T("Sidebar");
}
