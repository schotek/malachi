// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Board/BoardColumnsViewController.swift
// (render, reflectSelection, locate, reveal, choose, step, sideways, the
// tables' selection and context menus) and BoardColumnTableView.swift (the
// keys); GTK: window/board_columns.go (apply, reflectSelection,
// indexOfCase, columnItems, wireCardMenu) and window/board_keys.go
// (sideways over board.SidewaysTarget, here Board.SidewaysTarget).
//
// One selection across the four ListViews, the controller's: the list that
// holds the selected case selects its row, the others none; a selected
// commitment stays the selected row while its case is the selection. The
// columns mirror the view model and report the user's picks; selecting a
// case opens the page's sliding panel (Board.ViewModel.ShowsPanel), so
// Return only selects the focused card. Up and Down step through the
// column's selectable rows (past the heading and the placeholder), Left
// and Right go to the neighbouring column at the same position, clamped,
// passing over empty columns; Escape closes the panel. A render replaces
// only the rows that changed, in place, so the scroll position and the
// selection stay.

using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Controllers;
using Microsoft.UI.Input;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using Windows.UI.Core;
using WinKey = Windows.System.VirtualKey;

namespace Malachi.App.Boards;

/// <summary>The board's Columns style.</summary>
public sealed partial class BoardColumnsView : UserControl, IBoardStyleContent
{
    /// <summary>
    /// A column's minimum width: narrower, the page scrolls sideways. GTK's
    /// boardColumnMinWidth, which matches macOS's minColumnWidth (200)
    /// closely enough.
    /// </summary>
    public const double ColumnMinimum = 240;

    // ColumnsGrid's padding and spacing (BoardColumnsView.xaml).
    private const double Gap = 12;
    private const double EdgePadding = 20;

    private readonly List<Pane> panes = [];
    private BoardActions? actions;
    private bool syncing;

    /// <summary>The four empty columns; <see cref="Attach"/> connects them.</summary>
    public BoardColumnsView()
    {
        InitializeComponent();
        AutomationProperties.SetName(this, Board.Text.StyleTitle(BoardStyle.Columns));
        foreach (var state in Board.States)
        {
            panes.Add(MakePane(state, panes.Count));
        }
        KeyDown += OnViewKeyDown;
    }

    /// <inheritdoc/>
    public UIElement View => this;

    /// <inheritdoc/>
    public ContentControl? DetailHost => null;

    private BoardController? Controller => actions?.Controller;

    /// <summary>Connects the columns to the case actions and renders the view model.</summary>
    public void Attach(BoardActions boardActions)
    {
        ArgumentNullException.ThrowIfNull(boardActions);
        actions = boardActions;
        Render();
    }

    /// <inheritdoc/>
    public void Apply(BoardController.Changes changes)
    {
        if ((changes & (BoardController.Changes.Content | BoardController.Changes.Filters | BoardController.Changes.Style)) != 0)
        {
            Render();
        }
        else if ((changes & BoardController.Changes.Selection) != 0)
        {
            ReflectSelection();
        }
    }

    /// <inheritdoc/>
    public bool FocusContent()
    {
        if (panes.FirstOrDefault(p => p.List.SelectedIndex >= 0) is { } held)
        {
            return FocusRow(held, held.List.SelectedIndex);
        }
        if (panes.FirstOrDefault(p => p.SelectableRows().Any()) is { } first)
        {
            return FocusRow(first, first.SelectableRows().First());
        }
        return panes.Count > 0 && panes[0].List.Focus(FocusState.Programmatic);
    }

    // The view

    // makePane: the heading over the column's ListView, in column `index` of
    // the grid.
    private Pane MakePane(Board.State state, int index)
    {
        ColumnsGrid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star), MinWidth = ColumnMinimum });
        var container = new Grid();
        container.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        container.RowDefinitions.Add(new RowDefinition { Height = new GridLength(1, GridUnitType.Star) });
        Grid.SetColumn(container, index);

        var heading = new BoardStateHeading { Margin = new Thickness(6, 0, 6, 8) };
        container.Children.Add(heading);

        var list = new ListView
        {
            SelectionMode = ListViewSelectionMode.Single,
            SingleSelectionFollowsFocus = false,
            ItemTemplateSelector = (DataTemplateSelector)Resources["ItemSelector"],
            ItemContainerStyle = (Style)Resources["CardContainerStyle"],
            Padding = new Thickness(0, 0, 0, 8),
        };
        AutomationProperties.SetAutomationId(list, "BoardColumn" + state);
        Grid.SetRow(list, 1);
        container.Children.Add(list);
        ColumnsGrid.Children.Add(container);

        var pane = new Pane(state, container, heading, list);
        list.ItemsSource = pane.Items;
        list.SelectionChanged += (_, _) => OnSelectionChanged(pane);
        list.PreviewKeyDown += (_, e) => OnKeyDown(pane, e);
        list.ContainerContentChanging += OnContainerContentChanging;
        list.ContextRequested += (_, e) => OnContextRequested(pane, e);
        return pane;
    }

    // The columns share the page's width, unless four minimum columns need
    // more: then the grid is wider and the page scrolls sideways.
    private void OnOuterSizeChanged(object sender, SizeChangedEventArgs e) =>
        ColumnsGrid.Width = Math.Max(e.NewSize.Width, (ColumnMinimum * panes.Count) + (Gap * Math.Max(0, panes.Count - 1)) + EdgePadding);

    // Content

    // render: each column's heading and rows from the view model; the rows
    // that changed are replaced in place, then the selection is shown.
    private void Render()
    {
        if (Controller is not { } controller)
        {
            return;
        }
        var v = controller.View;
        var hadFocus = panes.Any(p => FocusIn(p.List));
        for (var i = 0; i < v.Columns.Count && i < panes.Count; i++)
        {
            var column = v.Columns[i];
            var pane = panes[i];
            pane.Heading.Show(column.State, column.Title, column.Rows.Count, showsZero: false);
            AutomationProperties.SetName(pane.List, column.Title);
            var items = new List<object>();
            if (column.Rows.Count == 0)
            {
                items.Add(new BoardPlaceholderItem(column.EmptyText));
            }
            else
            {
                items.AddRange(column.Rows.Select(r => (object)new BoardRowItem(r)));
            }
            if (column.State == Board.State.Hot && v.Commitments.Count > 0)
            {
                items.Add(new BoardHeadingItem(Board.Text.FromAssistant, v.Commitments.Count));
                items.AddRange(v.Commitments.Select(c => (object)new BoardCommitmentItem(c)));
            }
            Update(pane, items);
        }
        ReflectSelection();
        if (hadFocus && !panes.Any(p => FocusIn(p.List)))
        {
            // A replaced row took the keyboard with it.
            FocusContent();
        }
    }

    // Brings pane's rows to `items`, replacing, adding and removing only
    // what differs (the records compare by value).
    private void Update(Pane pane, List<object> items)
    {
        syncing = true;
        try
        {
            var rows = pane.Items;
            var common = Math.Min(rows.Count, items.Count);
            for (var i = 0; i < common; i++)
            {
                if (!Equals(rows[i], items[i]))
                {
                    rows[i] = items[i];
                }
            }
            while (rows.Count > items.Count)
            {
                rows.RemoveAt(rows.Count - 1);
            }
            for (var i = rows.Count; i < items.Count; i++)
            {
                rows.Add(items[i]);
            }
        }
        finally
        {
            syncing = false;
        }
    }

    // Selection

    // reflectSelection: a list that shows the case already keeps it,
    // otherwise its card (else a commitment of it) is selected and brought
    // into view; every other list selects nothing. While the keyboard is in
    // a column it follows the selection.
    private void ReflectSelection()
    {
        if (Controller is not { } controller)
        {
            return;
        }
        Pane? holder = null;
        syncing = true;
        try
        {
            if (controller.View.Selection is { } id)
            {
                holder = panes.FirstOrDefault(p => p.SelectedCase == id);
                if (holder is null && Locate(id) is ({ } pane, var row))
                {
                    pane.List.SelectedIndex = row;
                    Reveal(pane, row);
                    holder = pane;
                }
            }
            foreach (var pane in panes)
            {
                if (!ReferenceEquals(pane, holder) && pane.List.SelectedIndex >= 0)
                {
                    pane.List.SelectedIndex = -1;
                }
            }
        }
        finally
        {
            syncing = false;
        }
        if (holder is not null && !FocusIn(holder.List) && panes.Any(p => FocusIn(p.List)))
        {
            FocusRow(holder, holder.List.SelectedIndex);
        }
    }

    // The card of case `id`, else a commitment of it.
    private (Pane? Pane, int Row) Locate(BoardCaseId id)
    {
        foreach (var pane in panes)
        {
            for (var i = 0; i < pane.Items.Count; i++)
            {
                if (pane.Items[i] is BoardRowItem r && r.Row.Id == id)
                {
                    return (pane, i);
                }
            }
        }
        foreach (var pane in panes)
        {
            for (var i = 0; i < pane.Items.Count; i++)
            {
                if (BoardItems.CaseOf(pane.Items[i]) == id)
                {
                    return (pane, i);
                }
            }
        }
        return (null, -1);
    }

    // The row into view in its column, the column into view on a page too
    // narrow for all four.
    private static void Reveal(Pane pane, int row)
    {
        if (row >= 0 && row < pane.Items.Count)
        {
            pane.List.ScrollIntoView(pane.Items[row]);
        }
        pane.Container.StartBringIntoView(new BringIntoViewOptions { AnimationDesired = false });
    }

    // choose: the user picked `row` of `pane` (a key or a click): the other
    // lists let go, and the controller selects the case.
    private void Choose(Pane pane, int row, bool focus)
    {
        if (Controller is not { } controller || row < 0 || row >= pane.Items.Count
            || BoardItems.CaseOf(pane.Items[row]) is not { } id)
        {
            return;
        }
        syncing = true;
        try
        {
            if (pane.List.SelectedIndex != row)
            {
                pane.List.SelectedIndex = row;
            }
            foreach (var other in panes)
            {
                if (!ReferenceEquals(other, pane) && other.List.SelectedIndex >= 0)
                {
                    other.List.SelectedIndex = -1;
                }
            }
        }
        finally
        {
            syncing = false;
        }
        Reveal(pane, row);
        if (focus)
        {
            FocusRow(pane, row);
        }
        controller.Select(id);
    }

    private static bool FocusRow(Pane pane, int row)
    {
        if (row < 0 || row >= pane.Items.Count)
        {
            return pane.List.Focus(FocusState.Keyboard);
        }
        pane.List.ScrollIntoView(pane.Items[row]);
        pane.List.UpdateLayout();
        return pane.List.ContainerFromIndex(row) is ListViewItem container
            ? container.Focus(FocusState.Keyboard)
            : pane.List.Focus(FocusState.Keyboard);
    }

    private void OnSelectionChanged(Pane pane)
    {
        if (syncing || Controller is not { } controller)
        {
            return;
        }
        var row = pane.List.SelectedIndex;
        if (row >= 0 && BoardItems.CaseOf(pane.List.SelectedItem) is not null)
        {
            Choose(pane, row, focus: false);
        }
        else if (row >= 0)
        {
            // A heading or a placeholder is never selected.
            ReflectSelection();
        }
        else if (!panes.Any(p => p.List.SelectedIndex >= 0))
        {
            // Ctrl+click let go of the selected card.
            controller.Select(null);
        }
    }

    // Keys

    // BoardColumnTableView.keyDown: the arrows, Return and Escape without a
    // modifier; the rest goes on to the ListView.
    private void OnKeyDown(Pane pane, KeyRoutedEventArgs e)
    {
        if (Modified())
        {
            return;
        }
        switch (e.Key)
        {
            case WinKey.Up:
                e.Handled = Step(pane, -1);
                break;
            case WinKey.Down:
                e.Handled = Step(pane, 1);
                break;
            case WinKey.Left:
                e.Handled = Sideways(pane, -1);
                break;
            case WinKey.Right:
                e.Handled = Sideways(pane, 1);
                break;
            case WinKey.Enter:
                e.Handled = Activate(pane, e.OriginalSource as DependencyObject);
                break;
        }
    }

    // Escape in the columns that nothing inside took: the panel closes with
    // the selection, as the page's cancelOperation on macOS.
    private void OnViewKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key == WinKey.Escape && !Modified() && Controller is { } controller
            && controller.View is { ShowsPanel: true, Selection: not null })
        {
            e.Handled = true;
            controller.Select(null);
        }
    }

    // step: the previous or next selectable row of the column; at its end
    // the selection stays. Without a selection Down takes the first row and
    // Up the last.
    private bool Step(Pane pane, int delta)
    {
        var rows = pane.SelectableRows().ToList();
        if (rows.Count == 0)
        {
            return true;
        }
        var current = pane.List.SelectedIndex;
        int? target = current < 0
            ? (delta > 0 ? rows[0] : rows[^1])
            : delta > 0 ? rows.Where(r => r > current).Select(r => (int?)r).FirstOrDefault()
            : rows.Where(r => r < current).Select(r => (int?)r).LastOrDefault();
        if (target is { } t)
        {
            Choose(pane, t, focus: true);
        }
        return true;
    }

    // sideways over Board.SidewaysTarget: the neighbouring column's row at
    // the same position among its selectable rows, clamped; empty columns
    // are passed over; at the edge nothing moves, and the key is consumed.
    private bool Sideways(Pane pane, int delta)
    {
        var from = panes.IndexOf(pane);
        if (from < 0)
        {
            return false;
        }
        var counts = panes.Select(p => p.SelectableRows().Count()).ToList();
        var position = pane.SelectableRows().ToList().IndexOf(pane.List.SelectedIndex);
        if (Board.SidewaysTarget(counts, from, position, delta, out var column, out var row))
        {
            var target = panes[column];
            Choose(target, target.SelectableRows().ElementAt(row), focus: true);
        }
        return true;
    }

    // Return on a card: its case is selected (the panel opens on it); on the
    // selected card the panel is open already.
    private bool Activate(Pane pane, DependencyObject? source)
    {
        var container = Container(source);
        var row = container is not null ? pane.List.IndexFromContainer(container) : pane.List.SelectedIndex;
        if (row < 0 || row >= pane.Items.Count || BoardItems.CaseOf(pane.Items[row]) is not { } id)
        {
            return false;
        }
        if (pane.List.SelectedIndex != row)
        {
            Choose(pane, row, focus: false);
        }
        else
        {
            Controller?.Select(id);
        }
        return true;
    }

    private static bool Modified() =>
        Down(WinKey.Control) || Down(WinKey.Menu) || Down(WinKey.Shift) || Down(WinKey.LeftWindows) || Down(WinKey.RightWindows);

    private static bool Down(WinKey key) =>
        InputKeyboardSource.GetKeyStateForCurrentThread(key).HasFlag(CoreVirtualKeyStates.Down);

    // Rows

    // The screen reader's name of a card, the tick of a commitment; the
    // heading and the placeholder take neither a click nor the keyboard.
    private void OnContainerContentChanging(ListViewBase sender, ContainerContentChangingEventArgs args)
    {
        var selectable = BoardItems.CaseOf(args.Item) is not null;
        args.ItemContainer.IsHitTestVisible = selectable;
        args.ItemContainer.IsTabStop = selectable;
        switch (args.Item)
        {
            case BoardRowItem r:
                AutomationProperties.SetName(args.ItemContainer, r.Row.Spoken);
                AutomationProperties.SetAutomationId(args.ItemContainer, "BoardCase");
                break;
            case BoardCommitmentItem c:
                AutomationProperties.SetName(args.ItemContainer, c.Commitment.Text);
                AutomationProperties.SetAutomationId(args.ItemContainer, "BoardCommitment");
                if (args.ItemContainer.ContentTemplateRoot is Border { Child: BoardCommitmentView view })
                {
                    view.OnTick = (id, done) => Controller?.SetCommitmentDone(id, done);
                }
                break;
            case BoardPlaceholderItem p:
                AutomationProperties.SetName(args.ItemContainer, p.Text);
                AutomationProperties.SetAutomationId(args.ItemContainer, "BoardColumnPlaceholder");
                break;
            case BoardHeadingItem h:
                AutomationProperties.SetName(args.ItemContainer, BoardStateHeading.SpokenPromises(h.Title, h.Count));
                AutomationProperties.SetAutomationId(args.ItemContainer, "BoardCommitmentsHeading");
                break;
        }
    }

    // A right-click, Shift+F10 or the menu key on a card: the case is
    // selected first (GTK's wireCardMenu), then its menu shows where it was
    // asked for.
    private void OnContextRequested(Pane pane, ContextRequestedEventArgs e)
    {
        if (actions is null)
        {
            return;
        }
        var container = Container(e.OriginalSource as DependencyObject);
        if (container is null)
        {
            return;
        }
        var row = pane.List.IndexFromContainer(container);
        if (row < 0 || row >= pane.Items.Count || BoardItems.CaseOf(pane.Items[row]) is not { } id)
        {
            return;
        }
        e.Handled = true;
        Choose(pane, row, focus: false);
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

    // One column: its view, its heading, its list and the rows it shows.
    private sealed class Pane(Board.State state, Grid container, BoardStateHeading heading, ListView list)
    {
        public Board.State State { get; } = state;

        public Grid Container { get; } = container;

        public BoardStateHeading Heading { get; } = heading;

        public ListView List { get; } = list;

        public ObservableCollection<object> Items { get; } = [];

        // The case of the selected row, if any.
        public BoardCaseId? SelectedCase => BoardItems.CaseOf(List.SelectedItem);

        // The indexes of the rows the user can select: cards and commitments.
        public IEnumerable<int> SelectableRows() =>
            Enumerable.Range(0, Items.Count).Where(i => BoardItems.CaseOf(Items[i]) is not null);
    }
}
