// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Board/BoardListViewController.swift
// (apply, render, items(for:), syncSelection, layoutPanes, the table's
// selection, Return and context menu); GTK: window/board_list.go
// (renderList, syncListSelection, caseIDAt, the row-selected handler, the
// secondary click's showCaseContextMenu) and the Adw.Breakpoints of
// board_page.blp. All state is the controller's: the list only mirrors
// its view model and reports the user's picks.
//
// The sections are the ListView's groups (BoardListGroup), so a header is
// never selected and the arrow keys pass over it. The groups and their
// items are updated in place when the sections or commitments changed
// (GTK's renderList keyed by case id): a row that did not change keeps its
// container, the scroll position stays, and the keyboard goes back to the
// selected row should an update take it. A selection change selects the
// row without touching anything else. The fold decisions follow the width (900: the side pane, 640
// or the list's and the detail's minimums: the detail) and reach the
// controller after the layout pass, as macOS defers them.

using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Controllers;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Data;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using WinKey = Windows.System.VirtualKey;

namespace Malachi.App.Boards;

/// <summary>The board's List style.</summary>
public sealed partial class BoardListView : UserControl, IBoardStyleContent
{
    /// <summary>The side pane's width.</summary>
    public const double NavWidth = 240;

    /// <summary>The list's minimum beside the detail (BoardListViewController.listMinimum).</summary>
    public const double ListMinimum = 372;

    /// <summary>The list's default width.</summary>
    public const double ListDefault = 400;

    /// <summary>The list's maximum beside the detail.</summary>
    public const double ListMaximum = 520;

    /// <summary>The detail's minimum beside the list.</summary>
    public const double DetailMinimum = 266;

    private const double HideNavBelow = 900;
    private const double HideDetailBelow = 640;

    private readonly CollectionViewSource source = new() { IsSourceGrouped = true };
    private readonly ObservableCollection<BoardListGroup> groups = [];
    private BoardActions? actions;
    private IReadOnlyList<Board.Section> shownSections = [];
    private IReadOnlyList<Board.CommitmentRow> shownCommitments = [];
    private bool shownCommitmentsInList;
    private bool built;
    private bool syncing;
    private bool inline = true;
    private bool watchingRoot;
    private double listWidth = ListDefault;

    /// <summary>An empty list; <see cref="Attach"/> connects it.</summary>
    public BoardListView()
    {
        InitializeComponent();
        source.Source = groups;
        CaseList.ItemsSource = source.View;
        AutomationProperties.SetName(CaseList, Board.Text.StyleTitle(BoardStyle.List));
        NoSelection.Show("view-grid", Board.Text.NoSelectionTitle, Board.Text.NoSelectionBody);
    }

    /// <summary>Return in the list: the page gives the detail the keyboard.</summary>
    public event EventHandler? DetailFocusRequested;

    /// <inheritdoc/>
    public UIElement View => this;

    /// <inheritdoc/>
    public ContentControl? DetailHost => inline ? DetailSlot : null;

    /// <summary>Whether the detail is beside the list, as the layout last decided.</summary>
    public bool ShowsDetailInline => inline;

    private BoardController? Controller => actions?.Controller;

    /// <summary>Connects the list to the case actions and renders the view model.</summary>
    public void Attach(BoardActions boardActions)
    {
        ArgumentNullException.ThrowIfNull(boardActions);
        actions = boardActions;
        Nav.Attach(boardActions.Controller);
        Render();
    }

    /// <inheritdoc/>
    public void Apply(BoardController.Changes changes)
    {
        if ((changes & (BoardController.Changes.Content | BoardController.Changes.Filters)) != 0)
        {
            Render();
        }
        else if ((changes & BoardController.Changes.Selection) != 0)
        {
            SyncSelection();
            UpdateNoSelection();
        }
    }

    /// <inheritdoc/>
    public bool FocusContent()
    {
        if (CaseList.SelectedItem is { } item && CaseList.ContainerFromItem(item) is ListViewItem container)
        {
            return container.Focus(FocusState.Programmatic);
        }
        return CaseList.Focus(FocusState.Programmatic);
    }

    // Rendering

    private void Render()
    {
        if (Controller is not { } controller)
        {
            return;
        }
        var model = controller.View;
        Nav.Render(model);
        var empty = model.Sections.Count == 0;
        EmptyText.Text = model.SectionsEmptyText;
        EmptyText.Visibility = empty ? Visibility.Visible : Visibility.Collapsed;
        CaseList.Visibility = empty ? Visibility.Collapsed : Visibility.Visible;
        if (!built || !model.Sections.SequenceEqual(shownSections) || !model.Commitments.SequenceEqual(shownCommitments)
            || model.ShowsCommitmentsInList != shownCommitmentsInList)
        {
            built = true;
            shownSections = model.Sections;
            shownCommitments = model.Commitments;
            shownCommitmentsInList = model.ShowsCommitmentsInList;
            Rebuild(model);
        }
        SyncSelection();
        UpdateNoSelection();
    }

    // BoardListViewController.items(for:): the commitments under Overview
    // first, then each section with its rows; the groups shown are made so
    // in place (BoardListGroup.Sync).
    private void Rebuild(Board.ViewModel model)
    {
        var next = new List<(string Key, BoardListGroupKind Kind, Board.State State, string Title, string Spoken, List<object> Items)>();
        if (model.ShowsCommitmentsInList && model.Commitments.Count > 0)
        {
            var n = model.Commitments.Count;
            next.Add(("commitments", BoardListGroupKind.Commitments, Board.State.Info, Board.Text.FromAssistant,
                Spoken(Board.TileKind.Commitments, Board.State.Info, Board.Text.FromAssistant, n),
                model.Commitments.Select(c => (object)new BoardCommitmentItem(c)).ToList()));
        }
        foreach (var section in model.Sections)
        {
            var kind = section.Kind == Board.SectionKind.State ? BoardListGroupKind.State : BoardListGroupKind.Plain;
            next.Add((section.Kind + ":" + section.State, kind, section.State, section.Title,
                Spoken(Board.TileKind.State, section.State, section.Title, section.Rows.Count),
                section.Rows.Select(r => (object)new BoardRowItem(r)).ToList()));
        }
        var hadFocus = FocusIn(CaseList);
        syncing = true;
        try
        {
            for (var i = 0; i < next.Count; i++)
            {
                var want = next[i];
                var at = -1;
                for (var j = i; j < groups.Count; j++)
                {
                    if (groups[j].Key == want.Key)
                    {
                        at = j;
                        break;
                    }
                }
                BoardListGroup group;
                if (at < 0)
                {
                    group = new BoardListGroup(want.Key, want.Kind, want.State, want.Title, want.Spoken);
                    group.Sync(want.Items, KeyOf);
                    groups.Insert(i, group);
                    continue;
                }
                if (at != i)
                {
                    // Not ObservableCollection.Move: a grouped
                    // CollectionViewSource is not trusted to follow a Move
                    // of a group; a remove and an insert it follows always,
                    // and the focus comes back below.
                    group = groups[at];
                    groups.RemoveAt(at);
                    groups.Insert(i, group);
                }
                group = groups[i];
                group.Update(want.Kind, want.State, want.Title, want.Spoken);
                group.Sync(want.Items, KeyOf);
            }
            while (groups.Count > next.Count)
            {
                groups.RemoveAt(groups.Count - 1);
            }
        }
        finally
        {
            syncing = false;
        }
        if (hadFocus && !FocusIn(CaseList))
        {
            // A replaced row took the keyboard with it: back to the selected row.
            DispatcherQueue.TryEnqueue(() =>
            {
                if (!FocusIn(CaseList))
                {
                    SyncSelection();
                    FocusContent();
                }
            });
        }
    }

    // An item's identity across updates: a case's row, a commitment.
    private static string KeyOf(object item) => item switch
    {
        BoardRowItem r => "r:" + r.Row.Id.Value,
        BoardCommitmentItem c => "c:" + c.Commitment.Id.Value,
        _ => "",
    };

    // A header's name for the screen reader: "Hot: 3 cases", "From the
    // Assistant: 2 promises" (Board.Text.TileToolTip, the tiles' sentence).
    private static string Spoken(Board.TileKind kind, Board.State state, string title, int count) =>
        Board.Text.TileToolTip(new Board.Tile(kind, state, count, title));

    // Whether the keyboard is in container.
    private bool FocusIn(DependencyObject container)
    {
        if (XamlRoot is null)
        {
            return false;
        }
        for (var d = FocusManager.GetFocusedElement(XamlRoot) as DependencyObject; d is not null; d = VisualTreeHelper.GetParent(d))
        {
            if (ReferenceEquals(d, container))
            {
                return true;
            }
        }
        return false;
    }

    // Selects the row of the controller's selection without telling the
    // controller and brings it into view. A selected commitment stays while
    // its case is the selection (as in macOS), so the arrows walk the
    // commitments.
    private void SyncSelection()
    {
        if (Controller is not { } controller)
        {
            return;
        }
        var selection = controller.View.Selection;
        if (CaseList.SelectedItem is BoardCommitmentItem c && c.Commitment.CaseId == selection)
        {
            return;
        }
        var row = selection is { } id ? Items().OfType<BoardRowItem>().FirstOrDefault(r => r.Row.Id == id) : null;
        syncing = true;
        try
        {
            if (!ReferenceEquals(CaseList.SelectedItem, row))
            {
                CaseList.SelectedItem = row;
            }
        }
        finally
        {
            syncing = false;
        }
        if (row is not null)
        {
            CaseList.ScrollIntoView(row);
        }
    }

    private IEnumerable<object> Items() => groups.SelectMany(g => g);

    // The status page beside the list while no case is selected.
    private void UpdateNoSelection()
    {
        var has = Controller?.View is { Detail: not null, Selection: not null };
        NoSelection.Visibility = has ? Visibility.Collapsed : Visibility.Visible;
        DetailSlot.Visibility = has ? Visibility.Visible : Visibility.Collapsed;
    }

    // The width

    // The width the window gives the board (the page spans the window): the
    // list's own grid is no measure, its columns' minimums hold it wider
    // than the window until the folds below take them away.
    private void OnSizeChanged(object sender, SizeChangedEventArgs e)
    {
        if (XamlRoot is { } root && !watchingRoot)
        {
            watchingRoot = true;
            root.Changed += (_, _) => Layout(root.Size.Width);
        }
        Layout(XamlRoot?.Size.Width ?? e.NewSize.Width);
    }

    // BoardListViewController.layoutPanes: folds the side pane below 900 and
    // the detail where list and detail no longer fit; the controller hears
    // of the detail's fold after this layout pass.
    private void Layout(double width)
    {
        if (width <= 0)
        {
            return;
        }
        var navOpen = width >= HideNavBelow;
        NavColumn.Width = new GridLength(navOpen ? NavWidth : 0);
        Nav.Visibility = navOpen ? Visibility.Visible : Visibility.Collapsed;
        var room = width - (navOpen ? NavWidth : 0);
        var next = width >= HideDetailBelow && room >= ListMinimum + DetailMinimum;
        if (next != inline)
        {
            if (!next)
            {
                // The list's width beside the detail, for when it comes back.
                listWidth = Math.Clamp(ListColumn.ActualWidth, ListMinimum, ListMaximum);
            }
            inline = next;
        }
        if (inline)
        {
            ListColumn.MinWidth = ListMinimum;
            // The list keeps its width while the detail keeps its minimum beside it.
            var most = Math.Max(ListMinimum, Math.Min(ListMaximum, room - DetailMinimum));
            if (ListColumn.Width.IsStar || ListColumn.Width.Value > most)
            {
                ListColumn.Width = new GridLength(Math.Min(listWidth, most));
            }
            ListColumn.MaxWidth = most;
            DetailColumn.MinWidth = DetailMinimum;
            DetailColumn.Width = new GridLength(1, GridUnitType.Star);
            DetailPane.Visibility = Visibility.Visible;
            ListSplitter.Visibility = Visibility.Visible;
        }
        else
        {
            ListColumn.MinWidth = 0;
            ListColumn.MaxWidth = double.PositiveInfinity;
            ListColumn.Width = new GridLength(1, GridUnitType.Star);
            DetailColumn.MinWidth = 0;
            DetailColumn.Width = new GridLength(0);
            DetailPane.Visibility = Visibility.Collapsed;
            ListSplitter.Visibility = Visibility.Collapsed;
        }
        if (Controller is { } controller && controller.State.InlineDetail != inline)
        {
            var want = inline;
            DispatcherQueue.TryEnqueue(() =>
            {
                if (Controller is { } c && c.State.Style == BoardStyle.List && c.State.InlineDetail != want && want == inline)
                {
                    c.SetInlineDetail(want);
                }
            });
        }
    }

    // The user's picks

    private void OnSelectionChanged(object sender, SelectionChangedEventArgs e)
    {
        if (syncing || Controller is not { } controller)
        {
            return;
        }
        switch (CaseList.SelectedItem)
        {
            case BoardRowItem r:
                controller.Select(r.Row.Id);
                break;
            case BoardCommitmentItem c:
                controller.Select(c.Commitment.CaseId);
                break;
            default:
                // Beside the detail the list always shows its case, so the
                // row comes back; the narrow List closes the panel.
                if (inline)
                {
                    SyncSelection();
                }
                else
                {
                    controller.Select(null);
                }
                break;
        }
    }

    // Return: to the detail beside the list (the panel has its own way in).
    private void OnListKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key == WinKey.Enter && inline && CaseList.SelectedItem is not null)
        {
            e.Handled = true;
            DetailFocusRequested?.Invoke(this, EventArgs.Empty);
        }
    }

    // The screen reader's name of a row and the tick of a commitment.
    private void OnContainerContentChanging(ListViewBase sender, ContainerContentChangingEventArgs args)
    {
        switch (args.Item)
        {
            case BoardRowItem r:
                AutomationProperties.SetName(args.ItemContainer, r.Row.Spoken);
                AutomationProperties.SetAutomationId(args.ItemContainer, "BoardCase");
                break;
            case BoardCommitmentItem c:
                AutomationProperties.SetName(args.ItemContainer, c.Commitment.Text);
                AutomationProperties.SetAutomationId(args.ItemContainer, "BoardCommitment");
                if (args.ItemContainer.ContentTemplateRoot is BoardCommitmentView view)
                {
                    view.OnTick = (id, done) => Controller?.SetCommitmentDone(id, done);
                }
                break;
        }
    }

    // A right-click, Shift+F10 or the menu key on a row: the case is
    // selected first (GTK), then its menu shows where it was asked for.
    private void OnContextRequested(UIElement sender, ContextRequestedEventArgs e)
    {
        if (actions is null || Controller is not { } controller)
        {
            return;
        }
        var container = Container(e.OriginalSource as DependencyObject);
        if (container is null || CaseList.ItemFromContainer(container) is not { } item)
        {
            return;
        }
        BoardCaseId id;
        switch (item)
        {
            case BoardRowItem r:
                id = r.Row.Id;
                break;
            case BoardCommitmentItem c:
                id = c.Commitment.CaseId;
                break;
            default:
                return;
        }
        e.Handled = true;
        controller.Select(id);
        var menu = BoardCaseMenu.ContextMenu(id, actions);
        if (e.TryGetPosition(container, out var at))
        {
            menu.ShowAt(container, new FlyoutShowOptions { Position = at });
        }
        else
        {
            menu.ShowAt(container);
        }
    }

    private static ListViewItem? Container(DependencyObject? d)
    {
        for (; d is not null; d = VisualTreeHelper.GetParent(d))
        {
            if (d is ListViewItem item)
            {
                return item;
            }
        }
        return null;
    }
}
