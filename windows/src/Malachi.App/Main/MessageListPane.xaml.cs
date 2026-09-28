// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageList/MessageListViewController.swift
// (the callbacks it installs, the banners, apply(rows:hint:), refreshRows,
// syncSelection, applyAppearance, show(state), showLoadMore, focus(key),
// rowDoubleClicked, activateSelected, foldSelected, the scroll edge) and
// SearchScopeBar.swift; GTK: ui/internal/window/window.go (the list's
// row-selected and row-activated handlers, refreshListTitle,
// showConnectionState's banner), messages.go (showListState, showLoadMore,
// applyListAppearance), threads.go (syncRows, addThreadShortcuts) and
// search.go (the scope toggles, the filter hidden while searching,
// refreshSearchScope's tooltips).
//
// The view mirrors ListController.Rows by key into MessageRows updated in
// place (KeyedListSync's view overload) and selects SelectedKey after every
// apply with its own handler suppressed; a click goes back as Select, and
// never a deselection the collection caused (docs/windows-port.md §7.5).
// Paging is Core's (MailboxController.Paging.cs): the view reports the
// viewport after every change of the rows' extent or the pane's size and on
// every scroll, and shows Load More only while LoadMoreRetry says so.
//
// Windows addition (windows/README.md): a context menu on a message offers
// the actions of the message pane's header (a right click selects the row
// first, as Windows lists do, without showing the message in a folded
// window, where the list stays in sight under the menu; a click on the row
// selected so shows it there, as a click on any other row does).
//
// The keyboard goes to the list on request (the end of a search, the back
// button): the request waits until rows are shown and their containers are
// realised, since after a search the folder's rows come with the daemon's
// reply, and a container that is not there yet cannot take the focus. It
// lapses when the focus is placed, when the user selects a row or takes the
// keyboard elsewhere, when a new search starts, and when the list ends on a
// status page instead of rows.

using System;
using System.Collections.ObjectModel;
using System.ComponentModel;
using System.Linq;
using Malachi.App.Commands;
using Malachi.App.Localization;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Malachi.Core.Settings;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using WinKey = Windows.System.VirtualKey;

namespace Malachi.App.Main;

/// <summary>The message list pane.</summary>
public sealed partial class MessageListPane : UserControl
{
    private static readonly MessageFilter[] Filters = [MessageFilter.All, MessageFilter.Unread, MessageFilter.Flagged];
    private static readonly SearchScope[] Scopes = [SearchScope.Folder, SearchScope.Account, SearchScope.All];

    // Layout passes a waiting focus request survives without its container.
    private const int MaxFocusAttempts = 8;

    private readonly Button retryButton;
    private ListController? list;
    private MailboxController? mailbox;
    private SettingsStore? settings;
    private WindowCommands? commands;
    private ScrollViewer? scroller;
    private bool reselecting;
    private bool settingFilter;
    private bool selectingForMenu;
    private bool focusPending;
    private bool focusWaitsForLayout;
    private int focusAttempts;
    private DependencyObject? focusFrom;

    /// <summary>An empty list; <see cref="Attach"/> connects it.</summary>
    public MessageListPane()
    {
        InitializeComponent();
        NameSelectorList(FilterBar);
        NameSelectorList(ScopeBar);
        MessageList.AddHandler(TappedEvent, new TappedEventHandler(OnRowTapped), handledEventsToo: true);
        // list_retry_button: a pill under the status page's texts.
        retryButton = new Button
        {
            Content = L10n.T("Try Again"),
            HorizontalAlignment = HorizontalAlignment.Center,
            CornerRadius = new CornerRadius(16),
            Padding = new Thickness(20, 6, 20, 6),
        };
        Microsoft.UI.Xaml.Automation.AutomationProperties.SetAutomationId(retryButton, "ListRetryButton");
        retryButton.Click += (_, _) => list?.Retry();
        ListStatusPage.Child = retryButton;
        LoadMoreButton.Click += (_, _) => list?.RetryLoadMore();
        // The list's viewer exists once its template is applied, which a
        // list collapsed under a status page has not had yet.
        MessageList.Loaded += (_, _) => HookScroller();
        MessageList.SizeChanged += (_, _) =>
        {
            HookScroller();
            ReportViewport();
        };
    }

    /// <summary>The user selected a message (window.go onMessageRowSelected: innerSplit.SetShowContent).</summary>
    public event EventHandler? MessageChosen;

    /// <summary>The sign-in banner's button (sync.go onAuthBannerButton).</summary>
    public event EventHandler? AuthBannerInvoked;

    /// <summary>The certificate banner's Edit Account… (sync.go onCertBannerButton).</summary>
    public event EventHandler? CertBannerInvoked;

    /// <summary>The backend banner's Retry (window.go reconnect).</summary>
    public event EventHandler? ReconnectRequested;

    /// <summary>The rows, in the list's order.</summary>
    public ObservableCollection<MessageRow> Rows { get; } = [];

    /// <summary>
    /// Installs the view-facing callbacks of the list half, the mailbox's
    /// title and the sync controller's banners, and shows what they hold
    /// already.
    /// </summary>
    public void Attach(ListController listController, SyncController sync, WindowCommands windowCommands)
    {
        ArgumentNullException.ThrowIfNull(listController);
        ArgumentNullException.ThrowIfNull(sync);
        ArgumentNullException.ThrowIfNull(windowCommands);
        list = listController;
        mailbox = listController.Mailbox;
        settings = listController.Settings;
        commands = windowCommands;
        CommandBinding.Bind(RefreshButton, windowCommands.CheckForNewMail);
        BackendRetryButton.Click += (_, _) => ReconnectRequested?.Invoke(this, EventArgs.Empty);
        AuthBannerButton.Click += (_, _) => AuthBannerInvoked?.Invoke(this, EventArgs.Empty);
        CertBannerButton.Click += (_, _) => CertBannerInvoked?.Invoke(this, EventArgs.Empty);

        listController.RowsChanged += (_, _) => ApplyRows();
        listController.RowsRefreshed += (_, _) => ApplyRows();
        listController.ListStateChanged += (_, state) => ShowState(state);
        listController.LoadMoreChanged += (_, _) => ShowLoadMore();
        listController.SelectionCleared += (_, _) => SyncSelection();
        listController.SearchBarChanged += (_, bar) => ShowScope(bar);
        listController.FocusRow += (_, key) => FocusRow(key);
        listController.PropertyChanged += OnListPropertyChanged;
        mailbox.ListTitleChanged += (_, heading) => ShowHeading(heading);
        sync.AuthBannerChanged += (_, banner) => ShowBanner(AuthBanner, AuthBannerButton, banner);
        sync.CertBannerChanged += (_, banner) => ShowBanner(CertBanner, null, banner);
        foreach (var key in (SettingsKey[])[SettingsKey.Density, SettingsKey.ShowPreviewLine, SettingsKey.ShowAvatars, SettingsKey.MonochromeAvatars])
        {
            settings.OnChange(key, ApplyRows);
        }

        // What the controllers hold already.
        Reveal(BackendBanner, false);
        Reveal(AuthBanner, false);
        Reveal(CertBanner, false);
        ShowHeading(mailbox.ListHeading);
        ApplyRows();
        ShowState(listController.ListState);
        ShowLoadMore();
        ShowScope(listController.SearchBar);
        ShowFilter();
    }

    /// <summary>
    /// The connection changed (window.go showConnectionState, after the
    /// mailbox): the backend banner shows while there is no daemon, hides on
    /// a connection or a daemon of another protocol (the status line names
    /// it), and an attempt underway leaves it as it is, so it does not blink
    /// with the retry loop.
    /// </summary>
    public void ShowConnectionState(ConnectionState state)
    {
        switch (state)
        {
            case ConnectionState.Unavailable or ConnectionState.Stopping:
                Reveal(BackendBanner, true);
                break;
            case ConnectionState.Connected or ConnectionState.ProtocolMismatch or ConnectionState.InfoFailed:
                Reveal(BackendBanner, false);
                break;
        }
    }

    /// <summary>
    /// Puts the keyboard on the selected row, or on the first (search.go
    /// onSearchModeChanged: messageList.GrabFocus); while the rows are not
    /// shown yet (the folder reloading after a search), as soon as they are.
    /// </summary>
    public void FocusList()
    {
        focusPending = true;
        focusFrom = XamlRoot is { } root ? FocusManager.GetFocusedElement(root) as DependencyObject : null;
        TryFocusPending();
    }

    /// <summary>A request of <see cref="FocusList"/> still waiting lapses (the search box took the keyboard again).</summary>
    public void CancelFocusList() => EndFocusRequest();

    /// <summary>A conversation row's fold arrow (threads.go toggleThread).</summary>
    internal void ToggleThread(MessageRow row)
    {
        if (row.IsThread && row.Key.Thread is { } tid)
        {
            list?.ToggleThread(tid);
        }
    }

    // window.go refreshListTitle: the folder and its counts, or Search and
    // the number of results.
    private void ShowHeading(ListHeading heading)
    {
        ListTitleText.Text = heading.Title;
        ListSubtitleText.Text = heading.Subtitle;
        ListSubtitleText.Visibility = heading.Subtitle.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
    }

    // sync.go showAuthRequired / hideAuthBanner, refreshCertBanner. The
    // title is the account and the reason, plain text; the sign-in banner's
    // button says what it does for the account (a null button keeps the
    // label the XAML gives: the certificate banner's "_Edit Account…", with
    // its access key).
    private static void ShowBanner(InfoBar bar, Button? button, SyncBanner banner)
    {
        if (banner.IsHidden)
        {
            Reveal(bar, false);
            return;
        }
        bar.Message = banner.Title ?? "";
        if (button is not null && banner.Button is { } label)
        {
            button.Content = label;
        }
        Reveal(bar, true);
    }

    // Adw.Banner's revealed: a closed banner takes no room, its margin
    // included.
    private static void Reveal(InfoBar bar, bool open)
    {
        bar.IsOpen = open;
        bar.Margin = open ? new Thickness(6, 6, 6, 0) : default;
    }

    // MessageListViewController.apply(rows:hint:): the rows by key, updated
    // in place; then the hairline of the last row and the selection.
    private void ApplyRows()
    {
        if (list is not { } l || settings is null || mailbox is null)
        {
            return;
        }
        var look = RowAppearance.From(settings, l.SearchActive, mailbox.Model.Grouped);
        var now = TimeProvider.System.GetUtcNow();
        reselecting = true;
        try
        {
            KeyedListSync.Apply(Rows, l.Rows, r => r.Key, v => v.Key, r => new MessageRow(r.Key), (v, r) => v.Update(r, l.RowMessage(r.Message), look, now));
            for (var i = 0; i < Rows.Count; i++)
            {
                Rows[i].IsLast = i == Rows.Count - 1;
            }
        }
        finally
        {
            reselecting = false;
        }
        SyncSelection();
        TryFocusPending();
        // After the layout: the rows' extent moved (fillPane).
        DispatcherQueue.TryEnqueue(Microsoft.UI.Dispatching.DispatcherQueuePriority.Low, ReportViewport);
    }

    // syncSelection: the controller's SelectedKey, without re-entering Select.
    private void SyncSelection()
    {
        if (list is null)
        {
            return;
        }
        var wanted = list.SelectedKey is { } key ? Rows.FirstOrDefault(r => r.Key == key) : null;
        if (ReferenceEquals(MessageList.SelectedItem, wanted))
        {
            return;
        }
        reselecting = true;
        try
        {
            MessageList.SelectedItem = wanted;
        }
        finally
        {
            reselecting = false;
        }
    }

    // messages.go showListState (window.blp list_stack).
    private void ShowState(ListState state)
    {
        if (state is ListState.Status page)
        {
            // An empty icon is the Loading… page, which has none.
            ListStatusPage.Show(page.Icon, page.Title, page.Description);
            retryButton.Visibility = page.Retry ? Visibility.Visible : Visibility.Collapsed;
            ListStatusPage.Visibility = Visibility.Visible;
            MessagesPage.Visibility = Visibility.Collapsed;
            if (page.Icon.Length > 0)
            {
                // No rows to come: the keyboard stays where it is.
                EndFocusRequest();
            }
            return;
        }
        ListStatusPage.Visibility = Visibility.Collapsed;
        MessagesPage.Visibility = Visibility.Visible;
        TryFocusPending();
    }

    // FocusList's request, once rows are shown: the selected row's
    // container, or the first's, realised by a layout pass. A container not
    // there yet, or refusing the focus, keeps the request for the next pass
    // (a few at most).
    private void TryFocusPending()
    {
        if (!focusPending || Rows.Count == 0 || MessagesPage.Visibility != Visibility.Visible || Visibility != Visibility.Visible)
        {
            return;
        }
        if (!FocusStillWanted() || ++focusAttempts > MaxFocusAttempts)
        {
            EndFocusRequest();
            return;
        }
        var index = Math.Clamp(MessageList.SelectedIndex, 0, Rows.Count - 1);
        MessageList.UpdateLayout();
        if (MessageList.ContainerFromIndex(index) is not ListViewItem)
        {
            MessageList.ScrollIntoView(Rows[index]);
            MessageList.UpdateLayout();
        }
        if (MessageList.ContainerFromIndex(index) is ListViewItem item && item.Focus(FocusState.Keyboard))
        {
            EndFocusRequest();
            return;
        }
        if (!focusWaitsForLayout)
        {
            focusWaitsForLayout = true;
            MessageList.LayoutUpdated += OnLayoutForFocus;
        }
    }

    private void OnLayoutForFocus(object? sender, object e)
    {
        MessageList.LayoutUpdated -= OnLayoutForFocus;
        focusWaitsForLayout = false;
        // Not from inside the layout pass.
        DispatcherQueue.TryEnqueue(TryFocusPending);
    }

    // The keyboard is still where it was asked from (or nowhere, or already
    // in the list): the user has not taken it elsewhere meanwhile.
    private bool FocusStillWanted()
    {
        if (XamlRoot is not { } root)
        {
            return false;
        }
        var now = FocusManager.GetFocusedElement(root) as DependencyObject;
        if (now is null || ReferenceEquals(now, focusFrom))
        {
            return true;
        }
        for (var d = now; d is not null; d = VisualTreeHelper.GetParent(d))
        {
            if (ReferenceEquals(d, this))
            {
                return true;
            }
        }
        return false;
    }

    private void EndFocusRequest()
    {
        focusPending = false;
        focusFrom = null;
        focusAttempts = 0;
        if (focusWaitsForLayout)
        {
            MessageList.LayoutUpdated -= OnLayoutForFocus;
            focusWaitsForLayout = false;
        }
    }

    // messages.go showLoadMore, the macOS way: the spinner while a page
    // comes, Load More only to retry one that failed, the search note.
    private void ShowLoadMore()
    {
        if (list is not { } l)
        {
            return;
        }
        var state = l.LoadMoreState;
        LoadMoreSpinner.IsActive = state.Spinner;
        LoadMoreSpinner.Visibility = state.Spinner ? Visibility.Visible : Visibility.Collapsed;
        LoadMoreButton.Visibility = l.LoadMoreRetry ? Visibility.Visible : Visibility.Collapsed;
        LoadMoreBox.Visibility = state.Spinner || l.LoadMoreRetry ? Visibility.Visible : Visibility.Collapsed;
        SearchNote.Text = state.Note;
        SearchNote.Visibility = state.Note.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
    }

    // search.go refreshSearchScope (SearchScopeBar.show): the scope, Folder
    // and Account only with a selected folder, the tooltips naming what each
    // searches; null hides the bar.
    private void ShowScope(SearchBarState? bar)
    {
        if (bar is null)
        {
            ScopeBar.Visibility = Visibility.Collapsed;
            return;
        }
        ScopeBar.Visibility = Visibility.Visible;
        ScopeFolder.IsEnabled = bar.NarrowEnabled;
        ScopeAccount.IsEnabled = bar.NarrowEnabled;
        ToolTipService.SetToolTip(ScopeFolder, bar.FolderTooltip);
        ToolTipService.SetToolTip(ScopeAccount, bar.AccountTooltip.Length > 0 ? bar.AccountTooltip : null);
        ToolTipService.SetToolTip(ScopeAll, bar.AllTooltip);
        var item = ScopeBar.Items[Math.Max(Array.IndexOf(Scopes, bar.Scope), 0)];
        if (!ReferenceEquals(ScopeBar.SelectedItem, item))
        {
            settingFilter = true;
            try
            {
                ScopeBar.SelectedItem = item;
            }
            finally
            {
                settingFilter = false;
            }
        }
    }

    private void OnScopeChanged(SelectorBar sender, SelectorBarSelectionChangedEventArgs args)
    {
        if (settingFilter || list is null || sender.SelectedItem is not { } item)
        {
            return;
        }
        var i = sender.Items.IndexOf(item);
        if (i >= 0)
        {
            list.SetSearchScope(Scopes[i]);
        }
    }

    // window.blp message_filter: the list's filter, hidden while searching.
    private void ShowFilter()
    {
        if (list is not { } l)
        {
            return;
        }
        FilterBar.Visibility = l.SearchActive ? Visibility.Collapsed : Visibility.Visible;
        var item = FilterBar.Items[Math.Max(Array.IndexOf(Filters, l.ListFilter), 0)];
        if (!ReferenceEquals(FilterBar.SelectedItem, item))
        {
            settingFilter = true;
            try
            {
                FilterBar.SelectedItem = item;
            }
            finally
            {
                settingFilter = false;
            }
        }
    }

    private void OnFilterChanged(SelectorBar sender, SelectorBarSelectionChangedEventArgs args)
    {
        if (settingFilter || list is null || sender.SelectedItem is not { } item)
        {
            return;
        }
        var i = sender.Items.IndexOf(item);
        if (i >= 0)
        {
            list.SetListFilter(Filters[i]);
        }
    }

    private void OnListPropertyChanged(object? sender, PropertyChangedEventArgs e)
    {
        switch (e.PropertyName)
        {
            case nameof(ListController.ListFilter):
                ShowFilter();
                break;
            case nameof(ListController.SearchActive):
                if (list?.SearchActive == true)
                {
                    // The search box keeps the keyboard.
                    EndFocusRequest();
                }
                ShowFilter();
                // A search result's excerpt always shows.
                ApplyRows();
                break;
            case nameof(ListController.LoadMoreRetry):
                ShowLoadMore();
                break;
        }
    }

    // The row the user selected (window.go row-selected →
    // onMessageRowSelected); a deselection the collection caused is not
    // the user's and never goes back.
    private void OnSelectionChanged(object sender, SelectionChangedEventArgs e)
    {
        if (reselecting || list is null)
        {
            return;
        }
        EndFocusRequest();
        var row = MessageList.SelectedItem as MessageRow;
        list.Select(row?.Key);
        if (row is not null && !selectingForMenu)
        {
            MessageChosen?.Invoke(this, EventArgs.Empty);
        }
    }

    // Windows addition: a click on the row already selected shows it too,
    // which a folded window needs for a row a context menu selected (or one
    // left selected by the back button); a click that selects shows it
    // through OnSelectionChanged, and a second MessageChosen is harmless.
    private void OnRowTapped(object sender, TappedRoutedEventArgs e)
    {
        if (RowAt(e.OriginalSource as DependencyObject, out var inButton) is { } row && !inButton && ReferenceEquals(MessageList.SelectedItem, row))
        {
            MessageChosen?.Invoke(this, EventArgs.Empty);
        }
    }

    // search.go selectFirstResult (MessageListViewController.focus): the
    // row is selected, shown and given the keyboard.
    private void FocusRow(ListKey key)
    {
        if (Rows.FirstOrDefault(r => r.Key == key) is not { } row)
        {
            return;
        }
        MessageList.SelectedItem = row;
        MessageList.ScrollIntoView(row);
        MessageList.UpdateLayout();
        if (MessageList.ContainerFromItem(row) is ListViewItem item)
        {
            item.Focus(FocusState.Keyboard);
        }
    }

    // MessageListTableView.keyDown: Enter opens the selected row (a
    // conversation folds instead), Left folds its conversation, Right
    // unfolds a conversation row; a key with nothing to do goes on to the
    // list (threads.go addThreadShortcuts).
    private void OnListKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (list is not { } l || MessageList.SelectedItem is not MessageRow row)
        {
            return;
        }
        switch (e.Key)
        {
            case WinKey.Enter:
                l.Activate(row.Key);
                e.Handled = true;
                break;
            case WinKey.Left when row.Key.Thread is { } tid:
                e.Handled = l.SetThreadExpanded(tid, false);
                break;
            case WinKey.Right when row.Key.Thread is { } tid && !row.IsMember:
                e.Handled = l.SetThreadExpanded(tid, true);
                break;
        }
    }

    // rowDoubleClicked (window.go row-activated); a double click on the
    // fold arrow is the arrow's.
    private void OnDoubleTapped(object sender, DoubleTappedRoutedEventArgs e)
    {
        if (list is null || RowAt(e.OriginalSource as DependencyObject, out var inButton) is not { } row || inButton)
        {
            return;
        }
        list.Activate(row.Key);
        e.Handled = true;
    }

    private MessageRow? RowAt(DependencyObject? source, out bool inButton)
    {
        inButton = false;
        while (source is not null and not ListViewItem)
        {
            inButton |= source is ButtonBase;
            source = VisualTreeHelper.GetParent(source);
        }
        return source is ListViewItem item ? MessageList.ItemFromContainer(item) as MessageRow : null;
    }

    // Windows-only: the message pane's actions as a context menu.
    private void OnContextRequested(UIElement sender, ContextRequestedEventArgs e)
    {
        if (commands is not { } c || RowAt(e.OriginalSource as DependencyObject, out _) is not { } row)
        {
            return;
        }
        if (!ReferenceEquals(MessageList.SelectedItem, row))
        {
            // The actions act on the selection, so the row is selected; a
            // folded window stays on the list, under the menu.
            selectingForMenu = true;
            try
            {
                MessageList.SelectedItem = row;
            }
            finally
            {
                selectingForMenu = false;
            }
        }
        var flags = c.Flags;
        var menu = new MenuFlyout();
        void Add(AppCommand command, string label, bool mnemonic = false)
        {
            var item = CommandBinding.Item(command);
            if (mnemonic)
            {
                MnemonicLabel.Apply(item, label);
            }
            else
            {
                item.Text = label;
            }
            menu.Items.Add(item);
        }
        Add(c.Reply, L10n.T("Reply"));
        Add(c.ReplyAll, L10n.T("Reply All"));
        Add(c.Forward, L10n.T("Forward"));
        menu.Items.Add(new MenuFlyoutSeparator());
        Add(c.MarkUnread, L10n.T("Mark as _Unread"), mnemonic: true);
        Add(c.MarkRead, L10n.T("Mark as _Read"), mnemonic: true);
        Add(c.ToggleFlag, flags.Flagged ? L10n.T("Unstar") : L10n.T("Star"));
        menu.Items.Add(new MenuFlyoutSeparator());
        Add(c.Archive, L10n.T("Archive"));
        Add(c.Junk, L10n.T("Mark as Junk"));
        Add(c.Trash, Outbox.TrashTooltip(flags.Outbox));
        e.Handled = true;
        if (MessageList.ContainerFromItem(row) is not ListViewItem container)
        {
            return;
        }
        if (e.TryGetPosition(container, out var point))
        {
            menu.ShowAt(container, new FlyoutShowOptions { Position = point });
        }
        else
        {
            menu.ShowAt(container);
        }
    }

    // The scroll edge (window.go ConnectEdgeReached, clipBoundsChanged): the
    // list's viewer, and every change of its extent, viewport or offset.
    private void HookScroller()
    {
        if (scroller is not null)
        {
            return;
        }
        scroller = FindDescendant<ScrollViewer>(MessageList);
        if (scroller is null)
        {
            return;
        }
        scroller.ViewChanged += (_, _) => ReportViewport();
        scroller.RegisterPropertyChangedCallback(ScrollViewer.ExtentHeightProperty, (_, _) => ReportViewport());
        scroller.RegisterPropertyChangedCallback(ScrollViewer.ViewportHeightProperty, (_, _) => ReportViewport());
        ReportViewport();
    }

    private void ReportViewport()
    {
        if (list is null || scroller is null || MessagesPage.Visibility != Visibility.Visible)
        {
            return;
        }
        var scrollable = scroller.ExtentHeight > scroller.ViewportHeight + 0.5;
        var atEnd = scroller.VerticalOffset >= scroller.ScrollableHeight - 1;
        list.ViewportChanged(scrollable, atEnd);
    }

    // Windows-only: what UI Automation announces of a SelectorBar is the
    // ItemsView of its template, a list without a name. The bar's name and
    // id go to that list once the template is applied (a bar collapsed at
    // first gets them when it is first laid out); the bar itself is Raw in
    // the XAML, so the list is not announced twice.
    private static void NameSelectorList(SelectorBar bar)
    {
        void Apply()
        {
            if (FindDescendant<ItemsView>(bar) is { } view)
            {
                AutomationProperties.SetName(view, AutomationProperties.GetName(bar));
                AutomationProperties.SetAutomationId(view, AutomationProperties.GetAutomationId(bar));
            }
        }
        bar.Loaded += (_, _) => Apply();
        bar.SizeChanged += (_, _) => Apply();
    }

    private static T? FindDescendant<T>(DependencyObject root)
        where T : DependencyObject
    {
        var count = VisualTreeHelper.GetChildrenCount(root);
        for (var i = 0; i < count; i++)
        {
            var child = VisualTreeHelper.GetChild(root, i);
            if (child is T found)
            {
                return found;
            }
            if (FindDescendant<T>(child) is { } deeper)
            {
                return deeper;
            }
        }
        return null;
    }
}
