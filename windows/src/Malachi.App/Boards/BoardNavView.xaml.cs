// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Board/BoardNavView.swift (configure,
// BoardNavRow); GTK: renderNav, boardNavRow, boardAccountRow,
// onNavRowSelected (window/board_list.go). Two short lists of rows made
// from the view model (Board.NavItem, Board.AccountItem: cleaned texts,
// counts) and selected as it says; a row the user picks goes to the
// controller, which answers with a new view model.

using System;
using System.Collections.Generic;
using System.Globalization;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Controllers;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Boards;

/// <summary>The List style's navigation column: the filters and the accounts.</summary>
public sealed partial class BoardNavView : UserControl
{
    private readonly List<Board.Filter> filters = [];
    private readonly List<AccountId?> accounts = [];
    private BoardController? controller;
    private IReadOnlyList<Board.NavItem> shownNav = [];
    private IReadOnlyList<Board.AccountItem> shownAccounts = [];
    private bool syncing;

    /// <summary>An empty column; <see cref="Attach"/> connects it.</summary>
    public BoardNavView()
    {
        InitializeComponent();
        BoardCaption.Text = Board.Text.BoardName;
        AccountsCaption.Text = Board.Text.AccountsCaption;
        AutomationProperties.SetName(FilterList, Board.Text.BoardName);
        AutomationProperties.SetName(AccountList, Board.Text.AccountsCaption);
    }

    /// <summary>Connects the column to <paramref name="board"/>.</summary>
    public void Attach(BoardController board)
    {
        controller = board;
        Render(board.View);
    }

    /// <summary>Puts the keyboard on the selected filter.</summary>
    public bool FocusContent() =>
        FilterList.ContainerFromIndex(Math.Max(FilterList.SelectedIndex, 0)) is ListViewItem item && item.Focus(FocusState.Programmatic);

    /// <summary>Rebuilds the rows from <paramref name="model"/> when they changed.</summary>
    public void Render(Board.ViewModel model)
    {
        ArgumentNullException.ThrowIfNull(model);
        if (!SameItems(model.Nav, shownNav) || !SameItems(model.Accounts, shownAccounts))
        {
            shownNav = model.Nav;
            shownAccounts = model.Accounts;
            syncing = true;
            try
            {
                Fill(model);
            }
            finally
            {
                syncing = false;
            }
        }
    }

    private void Fill(Board.ViewModel model)
    {
        FilterList.Items.Clear();
        filters.Clear();
        var selected = -1;
        foreach (var n in model.Nav)
        {
            if (n.Selected)
            {
                selected = filters.Count;
            }
            filters.Add(n.Filter);
            FilterList.Items.Add(Row(n.Title, n.Count, n.Dot, "", NavId(n.Filter)));
        }
        FilterList.SelectedIndex = selected;

        AccountList.Items.Clear();
        accounts.Clear();
        selected = -1;
        foreach (var a in model.Accounts)
        {
            if (a.Selected)
            {
                selected = accounts.Count;
            }
            var id = a.Filter is null ? "BoardNavAccountAll" : "BoardNavAccount" + accounts.Count.ToString(CultureInfo.InvariantCulture);
            accounts.Add(a.Filter);
            AccountList.Items.Add(Row(a.Title, a.Count, null, a.Badge, id));
        }
        AccountList.SelectedIndex = selected;
    }

    // BoardNav<State>: BoardNavAll (Overview), BoardNavHot, …, BoardNavDone.
    private static string NavId(Board.Filter f) => f.Kind switch
    {
        Board.FilterKind.State => "BoardNav" + f.State,
        Board.FilterKind.Done => "BoardNavDone",
        _ => "BoardNavAll",
    };

    // A row: the state's dot (an empty place for Overview and Done, so the
    // titles line up), the title, the account's kind and the count.
    private static ListViewItem Row(string title, int count, Board.State? dot, string badge, string automationId)
    {
        var grid = new Grid { ColumnSpacing = 8 };
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        var swatch = new BoardStateSwatch
        {
            Width = 10,
            Height = 10,
            Corners = new CornerRadius(5),
            VerticalAlignment = VerticalAlignment.Center,
            Opacity = dot is null ? 0 : 1,
        };
        if (dot is { } s)
        {
            swatch.State = s;
        }
        grid.Children.Add(swatch);
        var label = new TextBlock
        {
            Text = title,
            VerticalAlignment = VerticalAlignment.Center,
            TextTrimming = TextTrimming.CharacterEllipsis,
            TextWrapping = TextWrapping.NoWrap,
        };
        Grid.SetColumn(label, 1);
        grid.Children.Add(label);
        if (badge.Length > 0)
        {
            var capsule = new Border
            {
                Style = (Style)Application.Current.Resources["KindBadgeBorderStyle"],
                Child = new TextBlock { Text = badge, Style = (Style)Application.Current.Resources["KindBadgeTextStyle"] },
            };
            ToolTipService.SetToolTip(capsule, badge);
            Grid.SetColumn(capsule, 2);
            grid.Children.Add(capsule);
        }
        if (count > 0)
        {
            var number = new TextBlock
            {
                Text = count.ToString(CultureInfo.CurrentCulture),
                VerticalAlignment = VerticalAlignment.Center,
                Style = (Style)Application.Current.Resources["BoardCountTextStyle"],
            };
            Grid.SetColumn(number, 3);
            grid.Children.Add(number);
        }
        var item = new ListViewItem { Content = grid };
        AutomationProperties.SetAutomationId(item, automationId);
        // Windows-only string: "Hot, 3", as macOS's radio rows say it.
        AutomationProperties.SetName(item, count > 0 ? title + ", " + count.ToString(CultureInfo.CurrentCulture) : title);
        return item;
    }

    private static bool SameItems<T>(IReadOnlyList<T> a, IReadOnlyList<T> b)
    {
        if (a.Count != b.Count)
        {
            return false;
        }
        for (var i = 0; i < a.Count; i++)
        {
            if (!EqualityComparer<T>.Default.Equals(a[i], b[i]))
            {
                return false;
            }
        }
        return true;
    }

    private void OnFilterChanged(object sender, SelectionChangedEventArgs e)
    {
        if (syncing || controller is null)
        {
            return;
        }
        var i = FilterList.SelectedIndex;
        if (i >= 0 && i < filters.Count)
        {
            controller.SetFilter(filters[i]);
        }
    }

    private void OnAccountChanged(object sender, SelectionChangedEventArgs e)
    {
        if (syncing || controller is null)
        {
            return;
        }
        var i = AccountList.SelectedIndex;
        if (i >= 0 && i < accounts.Count)
        {
            controller.SetAccount(accounts[i]);
        }
    }
}
