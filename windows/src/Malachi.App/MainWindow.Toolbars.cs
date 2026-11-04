// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MainWindow/MainToolbar.swift (the
// arrangement read from the settings, the search field put in for a search
// while the user took it off, O5), BoardToolbar.swift and
// CalendarToolbar.swift as the user arranged them; GTK: the header bars of
// window.go in the GTK port (docs/plans/toolbar-customization.md §17).
// Windows (W.2, W.4, W.5, W.8): the main window's toolbars are several
// bars, each drawing its section of Toolbar.PresentFor for the Windows
// variant: Mail's Folders section is the sidebar's row, its Message List
// section the list's header, its Message section MessageCommandBar; the
// Board's bar draws the List's Folders and Cases sections or Columns' and
// Today's toolbar, its detail's row the List's Case Detail; the Calendar's
// bar its toolbar. The mode switch, Mail's search and the Calendar's search
// stand where they stand (the title bar) and count only by being in the
// layout: without them the search box hides in that mode, and Ctrl+F (or
// the Calendar's key) shows it for the search until it is empty and loses
// the keyboard (O5); Mail's filter is the row under the list's header,
// shown while the layout has it. The bars follow their keys whatever
// changes them (the editor, another process), and the display mode
// (toolbar-display-mode) every icon button. A right click on an empty spot
// of a bar opens the editor on that bar (T15). Mail's sidebar toggle folds
// the wide layout's sidebar (the narrow layouts' title bar button stays).

using System.Collections.Generic;
using System.Linq;
using Malachi.App.Main;
using Malachi.App.Toolbars;
using Malachi.Core.Boards;
using Malachi.Core.Presentation;
using Malachi.Core.Settings;
using Malachi.Core.Toolbars;
using Microsoft.UI.Xaml;

namespace Malachi.App;

/// <summary>The main window's customizable toolbars.</summary>
public sealed partial class MainWindow
{
    private readonly List<SettingsChangeToken> toolbarTokens = [];
    private readonly List<ToolbarLabels> toolbarLabels = [];

    // The layouts have the title bar's search (Mail, Calendar), and a
    // search shows it for its time although they do not (O5).
    private bool mailSearchOnBar = true;
    private bool calendarSearchOnBar = true;
    private bool temporarySearch;

    // The toolbar's sidebar toggle folded the wide layout's sidebar.
    private bool mailSidebarFolded;

    // After InitializeMode: the bars' extra buttons, the keys, the menus.
    private void InitializeToolbars()
    {
        var c = Commands;
        // The items of Mail's Folders and Message List sections that one of
        // the two bars has of its own: the other gets a button of its own.
        var sidebarLabels = new ToolbarLabels();
        var listLabels = new ToolbarLabels();
        sidebarLabels.Register(SidebarPane.NewMessage, () => Toolbar.ItemLabel(Toolbar.Bar.Mail, Toolbar.NewMessage));
        listLabels.Register(ListPane.Refresh, () => Toolbar.ItemLabel(Toolbar.Bar.Mail, Toolbar.Refresh));
        foreach (var (strip, labels, prefix) in new[] { (SidebarPane.ToolbarStrip, sidebarLabels, "Sidebar"), (ListPane.ToolbarStrip, listLabels, "List") })
        {
            var contacts = ToolbarButtons.Make("x-office-address-book-symbolic", Toolbar.ItemLabel(Toolbar.Bar.Mail, Toolbar.Contacts), prefix + "ContactsButton");
            CommandBinding.Bind(contacts, c.Contacts);
            strip.Add(Toolbar.Contacts, contacts);
            labels.Register(contacts, () => Toolbar.ItemLabel(Toolbar.Bar.Mail, Toolbar.Contacts));
            var toggle = ToolbarButtons.Make("sidebar-show-symbolic", Toolbar.ItemLabel(Toolbar.Bar.Mail, Toolbar.ToggleSidebar), prefix + "SidebarToggle");
            toggle.Click += (_, _) => ToggleMailSidebar();
            strip.Add(Toolbar.ToggleSidebar, toggle);
            labels.Register(toggle, () => Toolbar.ItemLabel(Toolbar.Bar.Mail, Toolbar.ToggleSidebar));
        }
        var sidebarRefresh = ToolbarButtons.Make("view-refresh-symbolic", Toolbar.ItemLabel(Toolbar.Bar.Mail, Toolbar.Refresh), "SidebarRefreshButton");
        CommandBinding.Bind(sidebarRefresh, c.CheckForNewMail);
        SidebarPane.ToolbarStrip.Add(Toolbar.Refresh, sidebarRefresh);
        sidebarLabels.Register(sidebarRefresh, () => Toolbar.ItemLabel(Toolbar.Bar.Mail, Toolbar.Refresh));
        var listNew = ToolbarButtons.Make("mail-message-new-symbolic", Toolbar.ItemLabel(Toolbar.Bar.Mail, Toolbar.NewMessage), "ListNewMessageButton");
        CommandBinding.Bind(listNew, c.NewMessage);
        ListPane.ToolbarStrip.Add(Toolbar.NewMessage, listNew);
        listLabels.Register(listNew, () => Toolbar.ItemLabel(Toolbar.Bar.Mail, Toolbar.NewMessage));
        SidebarPane.ToolbarStrip.FitsItems = false;
        ListPane.ToolbarStrip.FitsItems = false;
        toolbarLabels.AddRange([sidebarLabels, listLabels, MessageCommands.Labels, BoardView.Labels]);

        // More Actions and Move To (M6, T8).
        MessageCommands.MissingActions = () => Toolbar.MissingActionsFor(
            ToolbarVariant.Windows, Toolbar.Bar.Mail, state.Settings.ToolbarLayout(Toolbar.Bar.Mail), ToolbarFeatures.Of(state));
        MessageCommands.FillMoveTargets = ListPane.FillSelectionMoveTargets;
        MessageCommands.CrmSaveRequested += (_, _) => crm?.Save();
        // What gave way changes the pane's minimum width only by its
        // natural width, which counts it in already.

        // T15: a right click on an empty spot of a bar.
        ToolbarStrip.WireCustomizeMenu(SidebarPane.ToolbarBar, () => Toolbar.Bar.Mail, ShowToolbarEditor);
        ToolbarStrip.WireCustomizeMenu(ListPane.ToolbarBar, () => Toolbar.Bar.Mail, ShowToolbarEditor);
        ToolbarStrip.WireCustomizeMenu(MessageCommands.Bar, () => Toolbar.Bar.Mail, ShowToolbarEditor);
        ToolbarStrip.WireCustomizeMenu(BoardView.ToolbarBar, () => BoardView.ShownToolbar, ShowToolbarEditor);
        ToolbarStrip.WireCustomizeMenu(BoardView.Detail.ToolbarBar, () => Toolbar.Bar.BoardList, ShowToolbarEditor);
        ToolbarStrip.WireCustomizeMenu(CalendarView.ToolbarBar, () => Toolbar.Bar.Calendar, ShowToolbarEditor);

        // The keys, whatever changes them.
        foreach (var bar in Toolbar.Bars)
        {
            toolbarTokens.Add(state.Settings.OnChange(SettingsStore.ToolbarKey(bar), ApplyToolbars));
        }
        toolbarTokens.Add(state.Settings.OnChange(SettingsKey.ToolbarDisplayMode, ApplyDisplayMode));
        // A search the layout has no box for: the box goes once it is empty
        // and has lost the keyboard.
        SearchBox.LostFocus += (_, _) =>
        {
            if (temporarySearch && SearchBox.Text.Length == 0)
            {
                temporarySearch = false;
                SyncSearchArea();
            }
        };
        Closed += (_, _) =>
        {
            foreach (var t in toolbarTokens)
            {
                t.Cancel();
            }
            toolbarTokens.Clear();
        };
        ApplyToolbars();
        ApplyDisplayMode();
    }

    /// <summary>Opens the Customize Toolbar dialog on <paramref name="bar"/> over this window (T15).</summary>
    internal void ShowToolbarEditor(Toolbar.Bar bar) => ToolbarEditorView.Show(state, this, bar);

    // Every bar draws its part of the user's layouts.
    private void ApplyToolbars()
    {
        var s = state.Settings;
        var f = ToolbarFeatures.Of(state);
        var mail = s.ToolbarLayout(Toolbar.Bar.Mail);
        var shown = Toolbar.PresentFor(ToolbarVariant.Windows, Toolbar.Bar.Mail, mail, f);
        SidebarPane.ToolbarStrip.Apply(shown.SectionRun(Toolbar.Bar.Mail, Toolbar.Section.Sidebar));
        ListPane.ToolbarStrip.Apply(shown.SectionRun(Toolbar.Bar.Mail, Toolbar.Section.List));
        MessageCommands.ApplyToolbar(shown.SectionRun(Toolbar.Bar.Mail, Toolbar.Section.Message));
        mailSearchOnBar = mail.Contains(Toolbar.Search);
        ListPane.SetFilterShown(mail.Contains(Toolbar.Filter));

        BoardView.ApplyToolbar(
            Toolbar.PresentFor(ToolbarVariant.Windows, Toolbar.Bar.BoardList, s.ToolbarLayout(Toolbar.Bar.BoardList), f),
            Toolbar.PresentFor(ToolbarVariant.Windows, Toolbar.Bar.BoardFlow, s.ToolbarLayout(Toolbar.Bar.BoardFlow), f));

        var calendar = s.ToolbarLayout(Toolbar.Bar.Calendar);
        CalendarView.ApplyToolbar(Toolbar.PresentFor(ToolbarVariant.Windows, Toolbar.Bar.Calendar, calendar, f));
        calendarSearchOnBar = calendar.Contains(Toolbar.CalendarSearch);

        SyncSearchArea();
        // The message pane keeps what its buttons need.
        ApplyLayout();
    }

    private void ApplyDisplayMode()
    {
        var m = state.Settings.ToolbarDisplayMode;
        foreach (var labels in toolbarLabels)
        {
            labels.Apply(m);
        }
        CalendarView.ApplyDisplayMode(m);
        ApplyLayout();
    }

    // The title bar's search box: in Mail and the Calendar while the
    // layout has it or a search runs without it (O5), never in the Board.
    // Its area keeps its room (a star column of its own) so that the mode
    // switch beside it stays where it is in every mode; only the box and
    // the own-words button go.
    private void SyncSearchArea()
    {
        var on = mode switch
        {
            Board.Mode.Mail => mailSearchOnBar || temporarySearch,
            Board.Mode.Calendar => calendarSearchOnBar || temporarySearch,
            _ => false,
        };
        // The title bar centres its content by the width it asks for: a
        // hidden box keeps the width the area had with it.
        if (!on && SearchArea.ActualWidth > 0 && SearchBox.Visibility == Visibility.Visible)
        {
            searchAreaWidth = SearchArea.ActualWidth;
        }
        SearchArea.MinWidth = on ? 0 : searchAreaWidth;
        SearchBox.Visibility = on ? Visibility.Visible : Visibility.Collapsed;
        SyncOwnWords();
    }

    // The search area's width while its box showed (the box's own minimum
    // until it has been laid out once).
    private double searchAreaWidth = 240;

    // Ctrl+F without a search in the layout: the box shows for the search
    // and is laid out before it takes the keyboard.
    private void RevealSearchForTyping()
    {
        if (SearchBox.Visibility == Visibility.Visible)
        {
            return;
        }
        temporarySearch = true;
        SyncSearchArea();
        SearchArea.UpdateLayout();
    }

    // The toolbar's sidebar toggle (toggleSidebar): the narrow layouts'
    // overlay as the title bar's pane button opens it, the wide layout's
    // sidebar folded or shown again.
    private void ToggleMailSidebar()
    {
        if (layout.Mode == PaneMode.Wide)
        {
            mailSidebarFolded = !mailSidebarFolded;
            ApplyLayout();
            return;
        }
        layout.ToggleSidebar();
        ApplyLayout();
        if (layout.SidebarOverlay)
        {
            SidebarPane.FocusList();
        }
    }
}
