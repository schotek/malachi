// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the Board's toolbars of macos/Sources/MalachiMail/Board/BoardToolbar.swift
// (listItems, flowItems) as the user arranged them
// (docs/plans/toolbar-customization.md W.2, W.5, Q-W6); GTK: the board's
// header bars of board_page.blp in the GTK port. The page's bar draws the
// List style's toolbar-board-list (its Folders and Cases sections) or, in
// Columns and Today, toolbar-board-flow; the detail's row draws the
// List's Case Detail section, inline and in the panel (it is one view), and
// the Windows default in Columns and Today, where the panel's row stays as
// it is, as on macOS. The window hands both layouts over (ApplyToolbar,
// MainWindow.Toolbars.cs); the style picks. Contacts and the sidebar toggle
// are made here: the sidebar toggle folds the List's side pane of states
// and accounts.

using System.Collections.Generic;
using Malachi.App.Commands;
using Malachi.App.Main;
using Malachi.App.Toolbars;
using Malachi.Core.Boards;
using Malachi.Core.Toolbars;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Boards;

public sealed partial class BoardPage
{
    private readonly Button contactsButton = ToolbarButtons.Make("x-office-address-book-symbolic", Toolbar.ItemLabel(Toolbar.Bar.BoardList, Toolbar.Contacts), "BoardContacts");
    private readonly Button sidebarButton = ToolbarButtons.Make("sidebar-show-symbolic", Toolbar.ItemLabel(Toolbar.Bar.BoardList, Toolbar.ToggleSidebar), "BoardSidebarToggle");

    // The runs the window gave last: the List's bar, Columns' and Today's
    // bar, the List's detail row; the Windows defaults until then.
    private IReadOnlyList<Toolbar.Shown> listBarRun = DefaultRun(Toolbar.Bar.BoardList, Toolbar.Section.Sidebar, Toolbar.Section.List);
    private IReadOnlyList<Toolbar.Shown> flowBarRun = DefaultRun(Toolbar.Bar.BoardFlow, Toolbar.Section.Main);
    private IReadOnlyList<Toolbar.Shown> listDetailRun = DefaultRun(Toolbar.Bar.BoardList, Toolbar.Section.Detail);

    /// <summary>The bar (a right click on an empty spot offers Customize Toolbar…).</summary>
    internal FrameworkElement ToolbarBar => Bar;

    /// <summary>The toolbar the bar draws now: the List's, or Columns' and Today's.</summary>
    internal Toolbar.Bar ShownToolbar => (Controller?.State.Style ?? BoardStyle.List) == BoardStyle.List ? Toolbar.Bar.BoardList : Toolbar.Bar.BoardFlow;

    /// <summary>The bar's icon buttons for the display mode (the style switch, the filter and Triage keep their own text).</summary>
    internal ToolbarLabels Labels { get; } = new();

    /// <summary>
    /// The user's layouts of the board's toolbars, for the features at hand
    /// (Toolbar.PresentFor): <paramref name="list"/> of toolbar-board-list,
    /// <paramref name="flow"/> of toolbar-board-flow.
    /// </summary>
    internal void ApplyToolbar(IReadOnlyList<Toolbar.Shown> list, IReadOnlyList<Toolbar.Shown> flow)
    {
        listBarRun = list.SectionRuns(Toolbar.Bar.BoardList, Toolbar.Section.Sidebar, Toolbar.Section.List);
        listDetailRun = list.SectionRun(Toolbar.Bar.BoardList, Toolbar.Section.Detail);
        flowBarRun = flow.SectionRun(Toolbar.Bar.BoardFlow, Toolbar.Section.Main);
        ApplyToolbarForStyle(Controller?.State.Style ?? BoardStyle.List);
    }

    private static IReadOnlyList<Toolbar.Shown> DefaultRun(Toolbar.Bar bar, params Toolbar.Section[] sections) =>
        Toolbar.PresentFor(ToolbarVariant.Windows, bar, Toolbar.DefaultFor(ToolbarVariant.Windows, bar), Toolbar.Features.None)
            .SectionRuns(bar, sections);

    private void InitializeToolbar()
    {
        // The bar is clipped when too narrow, as before (Q-W3's » is not built).
        BarStrip.FitsItems = false;
        BarStrip.Add(Toolbar.Contacts, contactsButton);
        BarStrip.Add(Toolbar.ToggleSidebar, sidebarButton);
        Labels.Register(contactsButton, () => Toolbar.ItemLabel(Toolbar.Bar.BoardList, Toolbar.Contacts));
        Labels.Register(sidebarButton, () => Toolbar.ItemLabel(Toolbar.Bar.BoardList, Toolbar.ToggleSidebar));
        sidebarButton.Click += (_, _) =>
        {
            if (current is BoardListView list)
            {
                list.ToggleNav();
            }
        };
        ApplyToolbarForStyle(BoardStyle.List);
    }

    private void AttachToolbar(WindowCommands commands) => CommandBinding.Bind(contactsButton, commands.Contacts);

    // The style's toolbar on the bar, the List's detail row in the List
    // (Q-W6), the default row in Columns and Today.
    private void ApplyToolbarForStyle(BoardStyle style)
    {
        var listStyle = style == BoardStyle.List;
        BarStrip.Bar = listStyle ? Toolbar.Bar.BoardList : Toolbar.Bar.BoardFlow;
        BarStrip.Apply(listStyle ? listBarRun : flowBarRun);
        // The sidebar toggle folds the List's side pane, which only the List has.
        sidebarButton.IsEnabled = listStyle;
        detail.ApplyToolbar(listStyle ? listDetailRun : DefaultRun(Toolbar.Bar.BoardList, Toolbar.Section.Detail));
    }
}
