// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Board/BoardPageViewController.swift
// (apply, showStyle, content(for:), updateEmpty, updatePanel, closePanel,
// focusContent) and the board's items of BoardToolbar.swift (the style
// switch, the account filter, Triage); GTK: window/board.go (applyAll,
// onChange), board_list.go (wire's style switch and account filter,
// renderHeader, renderStack) and board_panel.go (renderPanel, move: the
// one detail view between the List's pane and the panel).
//
// The page is the board controller's only listener: it hands every change
// to the style shown, the detail and its own parts, then raises Changed
// for the window (the title bar, the commands). One view per style, made
// on first use and kept, so a style shown again finds its scroll position.
// The one detail view lives beside the List's list while the List has room
// for it, in the sliding panel otherwise. Every text is the view model's.

using System;
using System.Collections.Generic;
using Malachi.App.Commands;
using Malachi.App.Main;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Controllers;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;

namespace Malachi.App.Boards;

/// <summary>The main window's Board mode.</summary>
public sealed partial class BoardPage : UserControl
{
    private readonly Dictionary<BoardStyle, IBoardStyleContent> styles = [];
    private readonly List<AccountId?> accountFilterIds = [];
    private readonly BoardDetailView detail = new();
    private BoardActions? actions;
    private IBoardStyleContent? current;

    // Where the detail is: a view has one parent, and a ContentControl's
    // content does not name it as its Parent.
    private ContentControl detailHost;
    private IReadOnlyList<Board.AccountItem> shownAccounts = [];
    private bool syncing;

    /// <summary>An empty page; <see cref="Attach"/> connects it.</summary>
    public BoardPage()
    {
        InitializeComponent();
        StyleListButton.Content = Board.Text.StyleTitle(BoardStyle.List);
        StyleColumnsButton.Content = Board.Text.StyleTitle(BoardStyle.Columns);
        StyleTodayButton.Content = Board.Text.StyleTitle(BoardStyle.Today);
        StyleListButton.Tag = BoardStyle.List;
        StyleColumnsButton.Tag = BoardStyle.Columns;
        StyleTodayButton.Tag = BoardStyle.Today;
        TitleText.Text = Board.Text.BoardName;
        TriageButton.Content = Board.Text.Triage;
        AutomationProperties.SetName(AccountFilter, Board.Text.AccountsCaption);
        detail.CloseRequested += (_, _) => ClosePanel();
        Panel.CloseRequested += (_, _) => ClosePanel();
        detailHost = Panel.DetailHost;
        detailHost.Content = detail;
    }

    /// <summary>After the page applied a change of the controller: the window follows (its commands, its focus).</summary>
    public event EventHandler<BoardController.Changes>? Changed;

    /// <summary>The board controller, once attached.</summary>
    public BoardController? Controller => actions?.Controller;

    /// <summary>The case actions, once attached.</summary>
    public BoardActions? Actions => actions;

    /// <summary>The one detail view (the List's pane or the panel shows it): its slots are the conversation's and the reply's.</summary>
    public BoardDetailView Detail => detail;

    /// <summary>✦ Triage in the bar, wired by the window (MainWindow.Triage.cs).</summary>
    public Button BoardTriageButton => TriageButton;

    /// <summary>The triage's status under the bar, filled by the window (MainWindow.Triage.cs).</summary>
    public ContentControl BoardTriageStrip => TriageStrip;

    /// <summary>Where the window's toasts lie while the board shows.</summary>
    public Panel ToastLayer => ToastLayerGrid;

    /// <summary>
    /// Connects the page: the board's actions (its controller and source)
    /// and the window's commands for the "…" menu.
    /// </summary>
    public void Attach(BoardActions boardActions, WindowCommands commands)
    {
        ArgumentNullException.ThrowIfNull(boardActions);
        ArgumentNullException.ThrowIfNull(commands);
        actions = boardActions;
        CommandBinding.Bind(MenuMail, commands.ShowMail);
        CommandBinding.Bind(MenuBoard, commands.ShowBoard);
        CommandBinding.Bind(MenuAddAccount, commands.AddAccount);
        CommandBinding.Bind(MenuPreferences, commands.Preferences);
        CommandBinding.Bind(MenuQuit, commands.Quit);
        detail.Attach(boardActions);
        boardActions.Controller.Changed += (_, changes) => Apply(changes);
        ShowStyle(boardActions.Controller.State.Style);
        ApplyAll();
    }

    /// <summary>
    /// The board shows again (the window entered Board mode): every part
    /// renders the view model as it is now.
    /// </summary>
    public void ApplyAll() => Apply(
        BoardController.Changes.Content | BoardController.Changes.Selection
        | BoardController.Changes.Style | BoardController.Changes.Filters);

    /// <summary>Gives the keyboard to the board: the panel's detail while it shows, else the current style.</summary>
    public bool FocusContent()
    {
        if (Panel.IsShown && detail.FocusContent())
        {
            return true;
        }
        return current?.FocusContent() == true || StyleButton(Controller?.State.Style ?? BoardStyle.List).Focus(FocusState.Programmatic);
    }

    /// <summary>The page's "…" menu (F10 while the board shows).</summary>
    public void ShowMenu()
    {
        if (!PageMenu.IsOpen)
        {
            PageMenu.ShowAt(MoreButton);
        }
    }

    /// <summary>The window's mode, for the menu's Mail and Board.</summary>
    public void ShowMode(Board.Mode mode)
    {
        MenuMail.IsChecked = mode == Board.Mode.Mail;
        MenuBoard.IsChecked = mode == Board.Mode.Board;
    }

    // Changes

    private void Apply(BoardController.Changes changes)
    {
        if (Controller is not { } controller)
        {
            return;
        }
        var model = controller.View;
        if ((changes & BoardController.Changes.Style) != 0)
        {
            ShowStyle(controller.State.Style);
        }
        current?.Apply(changes);
        PlaceDetail(model);
        detail.Apply(changes);
        RenderBar(model, controller.State);
        UpdateEmpty(model);
        UpdatePanel(model);
        Changed?.Invoke(this, changes);
    }

    private void ShowStyle(BoardStyle style)
    {
        var next = StyleContent(style);
        if (ReferenceEquals(next, current))
        {
            return;
        }
        var hadFocus = FocusIn(StyleHost);
        current = next;
        StyleHost.Content = next.View;
        // A style just put in renders the whole view model.
        next.Apply(BoardController.Changes.Content | BoardController.Changes.Selection | BoardController.Changes.Filters);
        if (hadFocus)
        {
            DispatcherQueue.TryEnqueue(() => next.FocusContent());
        }
    }

    private IBoardStyleContent StyleContent(BoardStyle style)
    {
        if (styles.TryGetValue(style, out var made))
        {
            return made;
        }
        switch (style)
        {
            case BoardStyle.Columns:
                var columns = new BoardColumnsView();
                if (actions is not null)
                {
                    columns.Attach(actions);
                }
                made = columns;
                break;
            case BoardStyle.Today:
                var today = new BoardTodayView();
                if (actions is not null)
                {
                    today.Attach(actions);
                }
                made = today;
                break;
            default:
                var list = new BoardListView();
                if (actions is not null)
                {
                    list.Attach(actions);
                }
                list.DetailFocusRequested += (_, _) => detail.FocusContent();
                made = list;
                break;
        }
        styles[style] = made;
        return made;
    }

    // GTK's boardPanel.move: the detail beside the List's list while the
    // List has room for it, in the panel otherwise. A view has one parent,
    // so it leaves the old host first.
    private void PlaceDetail(Board.ViewModel model)
    {
        var host = !model.ShowsPanel && current?.DetailHost is { } inline ? inline : Panel.DetailHost;
        if (ReferenceEquals(detailHost, host))
        {
            return;
        }
        var hadFocus = FocusIn(detail);
        detailHost.Content = null;
        detailHost = host;
        host.Content = detail;
        detail.InPanel = ReferenceEquals(host, Panel.DetailHost);
        if (hadFocus)
        {
            DispatcherQueue.TryEnqueue(() => detail.FocusContent());
        }
    }

    // The bar: the style's segment, the scope, the account filter.
    private void RenderBar(Board.ViewModel model, Board.ViewState state)
    {
        StyleListButton.IsChecked = state.Style == BoardStyle.List;
        StyleColumnsButton.IsChecked = state.Style == BoardStyle.Columns;
        StyleTodayButton.IsChecked = state.Style == BoardStyle.Today;
        SubtitleText.Text = model.Subtitle;
        syncing = true;
        try
        {
            if (!SameAccounts(model.Accounts))
            {
                shownAccounts = model.Accounts;
                AccountFilter.Items.Clear();
                accountFilterIds.Clear();
                foreach (var a in model.Accounts)
                {
                    accountFilterIds.Add(a.Filter);
                    // The kind in brackets after the name, as GTK's drop-down
                    // (renderHeader); both are plain text.
                    AccountFilter.Items.Add(a.Badge.Length == 0 ? a.Title : a.Title + " (" + a.Badge + ")"); // Windows-only string
                }
            }
            var selected = -1;
            for (var i = 0; i < model.Accounts.Count; i++)
            {
                if (model.Accounts[i].Selected)
                {
                    selected = i;
                }
            }
            AccountFilter.SelectedIndex = selected;
        }
        finally
        {
            syncing = false;
        }
    }

    private bool SameAccounts(IReadOnlyList<Board.AccountItem> next)
    {
        if (next.Count != shownAccounts.Count)
        {
            return false;
        }
        for (var i = 0; i < next.Count; i++)
        {
            // The selection and the counts change the items' records, not their texts.
            if (next[i].Filter != shownAccounts[i].Filter || next[i].Title != shownAccounts[i].Title || next[i].Badge != shownAccounts[i].Badge)
            {
                return false;
            }
        }
        return true;
    }

    // The status page in place of the style while the account scope has no
    // case at all; the notice above the cases while they are partial or old.
    private void UpdateEmpty(Board.ViewModel model)
    {
        var empty = model.IsEmpty;
        if (empty)
        {
            var spinning = model.Phase is Board.Phase.Loading or Board.Phase.Preparing;
            EmptyPage.Show(spinning ? "" : "view-grid", model.EmptyTitle, model.EmptyBody);
        }
        if (empty && FocusIn(StyleHost))
        {
            // The keyboard stays in the board, not with the window.
            StyleButton(Controller?.State.Style ?? BoardStyle.List).Focus(FocusState.Programmatic);
        }
        EmptyPage.Visibility = empty ? Visibility.Visible : Visibility.Collapsed;
        StyleHost.Visibility = empty ? Visibility.Collapsed : Visibility.Visible;
        Notice.Message = model.Notice;
        Notice.IsOpen = !empty && model.Notice.Length > 0;
    }

    // Slides the panel in or out after ShowsPanel; the keyboard leaves a
    // panel that slides out for the style.
    private void UpdatePanel(Board.ViewModel model)
    {
        var shows = model.ShowsPanel && !model.IsEmpty;
        if (shows == Panel.IsShown)
        {
            return;
        }
        if (!shows && FocusIn(Panel))
        {
            current?.FocusContent();
        }
        Panel.Fit(ContentHost.ActualWidth);
        Panel.SetShown(shows);
        AutomationProperties.SetName(Panel, model.Detail?.Title ?? "");
    }

    // The panel's Close and Escape: nothing selected, the keyboard back in the style.
    private void ClosePanel()
    {
        if (Controller is not { } controller || !Panel.IsShown)
        {
            return;
        }
        controller.Select(null);
        current?.FocusContent();
    }

    private ToggleButton StyleButton(BoardStyle style) => style switch
    {
        BoardStyle.Columns => StyleColumnsButton,
        BoardStyle.Today => StyleTodayButton,
        _ => StyleListButton,
    };

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

    // The user's picks

    private void OnStyleClick(object sender, RoutedEventArgs e)
    {
        if (Controller is not { } controller || sender is not ToggleButton { Tag: BoardStyle style })
        {
            return;
        }
        // The segment always shows the current style: the same one again, or
        // a refused one, changes nothing in the controller.
        controller.SetStyle(style);
        RenderBar(controller.View, controller.State);
    }

    private void OnAccountFilterChanged(object sender, SelectionChangedEventArgs e)
    {
        if (syncing || Controller is not { } controller)
        {
            return;
        }
        var i = AccountFilter.SelectedIndex;
        if (i >= 0 && i < accountFilterIds.Count)
        {
            controller.SetAccount(accountFilterIds[i]);
        }
    }

    private void OnContentSizeChanged(object sender, SizeChangedEventArgs e) => Panel.Fit(e.NewSize.Width);
}
