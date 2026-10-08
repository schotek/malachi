// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Board/BoardTodayViewController.swift
// (render, syncSelection, rowClicked, activateSelectedRow, menu(forRow:),
// the table's selection, BoardTodayContainer.layout,
// BoardTodaySideView.configure); GTK: window/board_today.go (apply,
// todayItems, reflectSelection, renderDue, renderTodayTiles, wireRowMenu)
// and window/board_keys.go (Return in Today).
//
// The list mirrors the view model's Today page and reports the user's
// picks; selecting a case opens the page's sliding panel. Up and Down step
// through the cases, the commitments and "and N more" (which they only
// focus: it acts on a click or Return, as on macOS); Return selects the
// focused case or shows every case waiting for the user; Escape closes the
// panel. A render replaces only the rows that changed, in place, so the
// scroll position and the selection stay; the deadlines are rebuilt only
// when they changed.

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

/// <summary>The board's Today style.</summary>
public sealed partial class BoardTodayView : UserControl, IBoardStyleContent
{
    // BoardTodayMetrics: the side column beside the list from 760, 40 % of
    // the width between 280 and 440; under it below, at most 40 % of the
    // height.
    private const double Narrow = 760;
    private const double SideMinimum = 280;
    private const double SidePreferred = 440;

    private readonly ObservableCollection<object> items = [];
    private readonly List<BoardTodayTile> tiles = [];
    private IReadOnlyList<Board.Tile> shownTiles = [];
    private IReadOnlyList<Board.DueGroup>? shownDue;
    private string shownDueEmpty = "";
    private BoardActions? actions;
    private bool syncing;

    /// <summary>An empty page; <see cref="Attach"/> connects it.</summary>
    public BoardTodayView()
    {
        InitializeComponent();
        AutomationProperties.SetName(TodayList, Board.Text.StyleTitle(BoardStyle.Today));
        AutomationProperties.SetName(this, Board.Text.StyleTitle(BoardStyle.Today));
        DeadlinesText.Text = Board.Text.Deadlines;
        TodayList.ItemsSource = items;
        KeyDown += OnViewKeyDown;
    }

    /// <inheritdoc/>
    public UIElement View => this;

    /// <inheritdoc/>
    public ContentControl? DetailHost => null;

    private BoardController? Controller => actions?.Controller;

    /// <summary>Connects the page to the case actions and renders the view model.</summary>
    public void Attach(BoardActions boardActions)
    {
        ArgumentNullException.ThrowIfNull(boardActions);
        actions = boardActions;
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
        }
    }

    /// <inheritdoc/>
    public bool FocusContent()
    {
        if (TodayList.SelectedIndex >= 0)
        {
            return FocusRow(TodayList.SelectedIndex);
        }
        var first = Enumerable.Range(0, items.Count).FirstOrDefault(i => Stops(items[i]), -1);
        return first >= 0 ? FocusRow(first) : TodayList.Focus(FocusState.Programmatic);
    }

    // Rendering

    private void Render()
    {
        if (Controller is not { } controller)
        {
            return;
        }
        var v = controller.View;
        var today = v.Today;
        TitleText.Text = today.Title;
        PhraseText.Text = today.Phrase;
        AutomationProperties.SetName(TitleText, today.Title);
        RenderTiles(today.Tiles);
        var hadFocus = FocusIn(TodayList);
        Update(Items(v));
        SyncSelection();
        if (hadFocus && !FocusIn(TodayList))
        {
            // A replaced row took the keyboard with it.
            FocusContent();
        }
        RenderSide(today);
    }

    // BoardTodayViewController.render's rows (GTK todayItems): Hot (its
    // rows, or its column's empty text), Waiting for You (its top rows, "and
    // N more"), then the commitments under "From the Assistant".
    private static List<object> Items(Board.ViewModel v)
    {
        var today = v.Today;
        var next = new List<object>
        {
            new BoardTodaySectionItem(Board.State.Hot, Board.Text.StateName(Board.State.Hot), today.Hot.Count),
        };
        if (today.Hot.Count == 0)
        {
            next.Add(new BoardTodayTextItem(EmptyText(v, Board.State.Hot)));
        }
        else
        {
            next.AddRange(today.Hot.Select(r => (object)new BoardRowItem(r)));
        }
        next.Add(new BoardTodaySectionItem(Board.State.You, Board.Text.StateName(Board.State.You), today.You.Count + today.YouMore));
        if (today.You.Count == 0)
        {
            next.Add(new BoardTodayTextItem(EmptyText(v, Board.State.You)));
        }
        else
        {
            next.AddRange(today.You.Select(r => (object)new BoardRowItem(r)));
        }
        if (today.YouMore > 0)
        {
            next.Add(new BoardTodayMoreItem(today.YouMore));
        }
        if (today.Commitments.Count > 0)
        {
            next.Add(new BoardHeadingItem(Board.Text.FromAssistant, today.Commitments.Count));
            next.AddRange(today.Commitments.Select(c => (object)new BoardCommitmentItem(c)));
        }
        return next;
    }

    // The state's column's empty text, the same Columns shows.
    private static string EmptyText(Board.ViewModel v, Board.State state) =>
        v.Columns.FirstOrDefault(c => c.State == state)?.EmptyText ?? Board.Text.SectionEmpty;

    // Brings the list's rows to `next`, replacing, adding and removing only
    // what differs (the records compare by value).
    private void Update(List<object> next)
    {
        syncing = true;
        try
        {
            var common = Math.Min(items.Count, next.Count);
            for (var i = 0; i < common; i++)
            {
                if (!Equals(items[i], next[i]))
                {
                    items[i] = next[i];
                }
            }
            while (items.Count > next.Count)
            {
                items.RemoveAt(items.Count - 1);
            }
            for (var i = items.Count; i < next.Count; i++)
            {
                items.Add(next[i]);
            }
        }
        finally
        {
            syncing = false;
        }
    }

    // The count tiles, one star column each; rebuilt when their kinds change.
    private void RenderTiles(IReadOnlyList<Board.Tile> next)
    {
        if (next.SequenceEqual(shownTiles))
        {
            return;
        }
        var sameKinds = next.Count == shownTiles.Count
            && next.Zip(shownTiles).All(p => p.First.Kind == p.Second.Kind && p.First.State == p.Second.State);
        shownTiles = next;
        if (!sameKinds)
        {
            Tiles.Children.Clear();
            Tiles.ColumnDefinitions.Clear();
            tiles.Clear();
            for (var i = 0; i < next.Count; i++)
            {
                Tiles.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
                var tile = new BoardTodayTile();
                AutomationProperties.SetAutomationId(tile, "BoardTodayTile" + BoardTodayTile.KindName(next[i]));
                Grid.SetColumn(tile, i);
                Tiles.Children.Add(tile);
                tiles.Add(tile);
            }
        }
        for (var i = 0; i < next.Count; i++)
        {
            tiles[i].Show(next[i]);
        }
    }

    // BoardTodaySideView.configure (GTK renderDue): the deadlines, or the
    // line without one.
    private void RenderSide(Board.Today today)
    {
        if (shownDue is not null && shownDue.SequenceEqual(today.DueGroups) && shownDueEmpty == today.DueEmpty)
        {
            return;
        }
        shownDue = today.DueGroups;
        shownDueEmpty = today.DueEmpty;
        while (DueStack.Children.Count > 1)
        {
            DueStack.Children.RemoveAt(DueStack.Children.Count - 1);
        }
        if (today.DueGroups.Count == 0)
        {
            DueStack.Children.Add(new TextBlock { Text = today.DueEmpty, Style = (Style)Resources["DueEmptyStyle"] });
            return;
        }
        foreach (var group in today.DueGroups)
        {
            var caption = new TextBlock
            {
                Text = group.Title,
                Margin = new Thickness(0, 6, 0, 0),
                Style = (Style)Resources["DueCaptionStyle"],
            };
            DueStack.Children.Add(caption);
            foreach (var item in group.Items)
            {
                var view = new BoardDueItemView(item, group.Kind == Board.DueGroupKind.Overdue);
                view.Chosen += (_, id) => Controller?.Select(id);
                DueStack.Children.Add(view);
            }
        }
    }

    // Selection

    // syncSelection: the row of the controller's selection, without telling
    // the controller; a selected commitment stays while its case is the
    // selection.
    private void SyncSelection()
    {
        if (Controller is not { } controller)
        {
            return;
        }
        var selection = controller.View.Selection;
        if (selection is not null && BoardItems.CaseOf(TodayList.SelectedItem) == selection)
        {
            return;
        }
        var target = selection is { } id
            ? Enumerable.Range(0, items.Count).FirstOrDefault(i => items[i] is BoardRowItem r && r.Row.Id == id, -1)
            : -1;
        syncing = true;
        try
        {
            if (TodayList.SelectedIndex != target)
            {
                TodayList.SelectedIndex = target;
            }
        }
        finally
        {
            syncing = false;
        }
        if (target >= 0)
        {
            TodayList.ScrollIntoView(items[target]);
        }
    }

    private void OnSelectionChanged(object sender, SelectionChangedEventArgs e)
    {
        if (syncing || Controller is not { } controller)
        {
            return;
        }
        switch (TodayList.SelectedItem)
        {
            case BoardRowItem r:
                controller.Select(r.Row.Id);
                break;
            case BoardCommitmentItem c:
                controller.Select(c.Commitment.CaseId);
                break;
            case BoardTodayMoreItem:
                // A click on "and N more" (the arrows only focus it).
                controller.ShowWaitingForYou();
                break;
            case null:
                // Ctrl+click let go of the selected row: the panel closes with it.
                controller.Select(null);
                break;
            default:
                SyncSelection();
                break;
        }
    }

    // Keys

    // The arrows, Return and Escape without a modifier; the rest goes on to
    // the ListView.
    private void OnListKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (Modified())
        {
            return;
        }
        switch (e.Key)
        {
            case WinKey.Up:
                e.Handled = Step(-1);
                break;
            case WinKey.Down:
                e.Handled = Step(1);
                break;
            case WinKey.Enter:
                e.Handled = Activate(e.OriginalSource as DependencyObject);
                break;
        }
    }

    // Escape anywhere on the page (the list, a deadline) that nothing inside
    // took: the panel closes with the selection, as the page's
    // cancelOperation on macOS.
    private void OnViewKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key == WinKey.Escape && !Modified() && Controller is { } controller
            && controller.View is { ShowsPanel: true, Selection: not null })
        {
            e.Handled = true;
            controller.Select(null);
        }
    }

    // The previous or next stop from the focused row (else the selected
    // one): a case or a commitment is selected, "and N more" only focused.
    private bool Step(int delta)
    {
        var stops = Enumerable.Range(0, items.Count).Where(i => Stops(items[i])).ToList();
        if (stops.Count == 0)
        {
            return true;
        }
        var current = FocusedRow() ?? TodayList.SelectedIndex;
        int target;
        if (current < 0)
        {
            target = delta > 0 ? stops[0] : stops[^1];
        }
        else
        {
            target = delta > 0 ? stops.FirstOrDefault(i => i > current, -1) : stops.LastOrDefault(i => i < current, -1);
        }
        if (target < 0)
        {
            return true;
        }
        if (BoardItems.CaseOf(items[target]) is { } id)
        {
            syncing = true;
            try
            {
                TodayList.SelectedIndex = target;
            }
            finally
            {
                syncing = false;
            }
            FocusRow(target);
            Controller?.Select(id);
        }
        else
        {
            FocusRow(target);
        }
        return true;
    }

    // activateSelectedRow: Return on "and N more" shows every case waiting
    // for the user; on a case or a commitment selects it (the panel opens).
    private bool Activate(DependencyObject? source)
    {
        var container = Container(source);
        var row = container is not null ? TodayList.IndexFromContainer(container) : TodayList.SelectedIndex;
        if (row < 0 || row >= items.Count || Controller is not { } controller)
        {
            return false;
        }
        switch (items[row])
        {
            case BoardTodayMoreItem:
                controller.ShowWaitingForYou();
                return true;
            case var item when BoardItems.CaseOf(item) is { } id:
                if (TodayList.SelectedIndex != row)
                {
                    syncing = true;
                    try
                    {
                        TodayList.SelectedIndex = row;
                    }
                    finally
                    {
                        syncing = false;
                    }
                }
                controller.Select(id);
                return true;
            default:
                return false;
        }
    }

    // A row the arrows stop at.
    private static bool Stops(object item) => item is BoardTodayMoreItem || BoardItems.CaseOf(item) is not null;

    private int? FocusedRow()
    {
        if (XamlRoot is null || Container(FocusManager.GetFocusedElement(XamlRoot) as DependencyObject) is not { } c)
        {
            return null;
        }
        var row = TodayList.IndexFromContainer(c);
        return row >= 0 ? row : null;
    }

    private bool FocusRow(int row)
    {
        if (row < 0 || row >= items.Count)
        {
            return TodayList.Focus(FocusState.Keyboard);
        }
        TodayList.ScrollIntoView(items[row]);
        TodayList.UpdateLayout();
        return TodayList.ContainerFromIndex(row) is ListViewItem container
            ? container.Focus(FocusState.Keyboard)
            : TodayList.Focus(FocusState.Keyboard);
    }

    private static bool Modified() =>
        Down(WinKey.Control) || Down(WinKey.Menu) || Down(WinKey.Shift) || Down(WinKey.LeftWindows) || Down(WinKey.RightWindows);

    private static bool Down(WinKey key) =>
        InputKeyboardSource.GetKeyStateForCurrentThread(key).HasFlag(CoreVirtualKeyStates.Down);

    // Rows

    // The screen reader's names and the tick of a commitment; the headings
    // and the texts take neither a click nor the keyboard.
    private void OnContainerContentChanging(ListViewBase sender, ContainerContentChangingEventArgs args)
    {
        var stops = Stops(args.Item);
        args.ItemContainer.IsHitTestVisible = stops;
        args.ItemContainer.IsTabStop = stops;
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
            case BoardTodayMoreItem m:
                AutomationProperties.SetName(args.ItemContainer, m.Text);
                AutomationProperties.SetAutomationId(args.ItemContainer, "BoardTodayMore");
                break;
            case BoardTodaySectionItem s:
                AutomationProperties.SetName(args.ItemContainer, BoardStateHeading.Spoken(s.State, s.Title, s.Count));
                AutomationProperties.SetAutomationId(args.ItemContainer, "BoardTodaySection" + s.State);
                break;
            case BoardHeadingItem h:
                AutomationProperties.SetName(args.ItemContainer, BoardStateHeading.SpokenPromises(h.Title, h.Count));
                AutomationProperties.SetAutomationId(args.ItemContainer, "BoardCommitmentsHeading");
                break;
            case BoardTodayTextItem t:
                AutomationProperties.SetName(args.ItemContainer, t.Text);
                AutomationProperties.SetAutomationId(args.ItemContainer, "BoardTodayEmpty");
                break;
        }
    }

    // A right-click, Shift+F10 or the menu key on a case or a commitment:
    // the case is selected first (GTK's wireRowMenu), then its menu shows.
    private void OnContextRequested(UIElement sender, ContextRequestedEventArgs e)
    {
        if (actions is null || Controller is not { } controller)
        {
            return;
        }
        var container = Container(e.OriginalSource as DependencyObject);
        if (container is null || BoardItems.CaseOf(TodayList.ItemFromContainer(container)) is not { } id)
        {
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

    // The width

    // BoardTodayContainer.layout: the side column beside the list from 760
    // (40 % of the width, 280 to 440), else under it (at most 40 % of the
    // height, scrolling when it needs more).
    private void OnSizeChanged(object sender, SizeChangedEventArgs e)
    {
        var w = e.NewSize.Width;
        var h = e.NewSize.Height;
        if (w >= Narrow)
        {
            SideColumn.Width = new GridLength(Math.Min(SidePreferred, Math.Max(SideMinimum, Math.Round(w * 0.4))));
            SideRow.Height = new GridLength(0);
            Grid.SetColumn(Side, 1);
            Grid.SetRow(Side, 0);
            Side.MaxHeight = double.PositiveInfinity;
        }
        else
        {
            SideColumn.Width = new GridLength(0);
            SideRow.Height = GridLength.Auto;
            Grid.SetColumn(Side, 0);
            Grid.SetRow(Side, 1);
            Side.MaxHeight = Math.Round(h * 0.4);
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
}
