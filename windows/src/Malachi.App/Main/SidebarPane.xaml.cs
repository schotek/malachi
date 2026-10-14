// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Sidebar/FolderSidebarViewController.swift
// (the callbacks it installs, rebuildRows, refreshBadges, show(status),
// highlightFolderRow, the selection and the folds); GTK:
// ui/internal/window/folders.go (rebuildFolderList, updateFolderRow,
// showFolderStatus, highlightFolderRow), window.go (the folder list's
// row-selected handler), collapse.go (addFolderShortcuts: Left and Right
// on a folder with children) and favourites.go (the star). The view mirrors
// MailboxController.Entries by key into SidebarRows updated in place
// (KeyedListSync's view overload) and selects SelectedEntryKey after every
// apply with its own handler suppressed (docs/windows-port.md §7.5); a
// click goes back as SelectFolder. Headings and containers cannot be
// selected: a selection that lands on one (the keyboard moves the
// selection with the focus) is put back.
//
// Windows additions (windows/README.md): Left and Right fold an account
// heading too (its arrow is not a tab stop in a ListView's row), and a
// context menu on a folder or heading offers what its row already does
// (Add to / Remove from Favourites, Expand / Collapse). A row chosen by
// click or Enter is FolderChosen, which closes the folded sidebar's
// overlay (window.go: outerSplit.SetShowContent).

using System;
using System.Collections.ObjectModel;
using System.Linq;
using Malachi.App.Commands;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.I18n;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using WinKey = Windows.System.VirtualKey;

namespace Malachi.App.Main;

/// <summary>The folder sidebar.</summary>
public sealed partial class SidebarPane : UserControl
{
    private MailboxController? mailbox;
    private bool reselecting;

    /// <summary>An empty sidebar; <see cref="Attach"/> connects it.</summary>
    public SidebarPane()
    {
        InitializeComponent();
    }

    /// <summary>The user chose a folder with a click or Enter.</summary>
    public event EventHandler? FolderChosen;

    /// <summary>The rows, in the mailbox's order.</summary>
    public ObservableCollection<SidebarRow> Rows { get; } = [];

    /// <summary>The folder list, for the keyboard (the overlay puts the focus there).</summary>
    public ListView List => FolderList;

    /// <summary>
    /// Installs the sidebar-facing callbacks of <paramref name="controller"/>
    /// and shows what it holds already; <paramref name="commands"/> are the
    /// main window's (New Message, the primary menu).
    /// </summary>
    public void Attach(MailboxController controller, WindowCommands commands)
    {
        ArgumentNullException.ThrowIfNull(controller);
        ArgumentNullException.ThrowIfNull(commands);
        mailbox = controller;
        CommandBinding.Bind(MenuMail, commands.ShowMail);
        CommandBinding.Bind(MenuBoard, commands.ShowBoard);
        // The sidebar shows in Mail only: its menu checks Mail.
        PrimaryMenu.Opening += (_, _) =>
        {
            MenuMail.IsChecked = true;
            MenuBoard.IsChecked = false;
        };
        CommandBinding.Bind(NewMessageButton, commands.NewMessage);
        CommandBinding.Bind(MenuNewMessage, commands.NewMessage);
        CommandBinding.Bind(MenuAddAccount, commands.AddAccount);
        CommandBinding.Bind(MenuAddJiraAccount, commands.AddJiraAccount);
        CommandBinding.Bind(MenuPreferences, commands.Preferences);
        CommandBinding.Bind(MenuAbout, commands.About);
        CommandBinding.Bind(MenuQuit, commands.Quit);
        controller.EntriesChanged += (_, _) => ApplyEntries();
        controller.BadgesChanged += (_, _) => ApplyEntries();
        controller.FolderStatusChanged += (_, status) => ShowStatus(status);
        controller.SelectionChanged += (_, _) => SyncSelection();
        ApplyEntries();
        ShowStatus(controller.SidebarStatus);
    }

    /// <summary>
    /// Opens the primary menu under its button, as F10 does in GTK
    /// (gtk_window_activate_menubar: the primary MenuButton pops up). The
    /// caller knows whether the sidebar is shown; a menu already open stays.
    /// </summary>
    public void ShowPrimaryMenu()
    {
        if (!PrimaryMenu.IsOpen)
        {
            PrimaryMenu.ShowAt(MainMenuButton);
        }
    }

    /// <summary>A heading's or a folder's fold arrow (collapse.go toggleAccount, toggleFolder).</summary>
    internal void ToggleFold(SidebarRow row)
    {
        if (mailbox is null || !row.TwistyActive)
        {
            return;
        }
        if (row.Kind == SidebarRowKind.AccountHeading && row.Account is { } acc)
        {
            mailbox.ToggleAccount(acc);
        }
        else if (row.Folder is { } k)
        {
            mailbox.ToggleFolder(k);
        }
    }

    /// <summary>A folder's star (favourites.go toggleFavourite).</summary>
    internal void ToggleFavourite(SidebarRow row)
    {
        if (mailbox is not null && row.HasStar && row.Folder is { } k)
        {
            mailbox.ToggleFavourite(k);
        }
    }

    /// <summary>Puts the keyboard on the selected row, or the first one.</summary>
    public void FocusList()
    {
        if (FolderList.Visibility != Visibility.Visible)
        {
            return;
        }
        var index = FolderList.SelectedIndex >= 0 ? FolderList.SelectedIndex : 0;
        if (FolderList.ContainerFromIndex(index) is ListViewItem item)
        {
            item.Focus(FocusState.Keyboard);
        }
        else
        {
            FolderList.Focus(FocusState.Keyboard);
        }
    }

    // folders.go rebuildFolderList / updateFolderRow: the rows by key, then
    // the highlight. With several accounts a pinned Inbox says whose it is.
    private void ApplyEntries()
    {
        if (mailbox is not { } m)
        {
            return;
        }
        var several = m.Model.EnabledAccounts.Count >= 2;
        reselecting = true;
        try
        {
            KeyedListSync.Apply(Rows, m.Entries, SidebarKey.Of, r => r.Key, e => new SidebarRow(SidebarKey.Of(e)), (r, e) => r.Update(e, several));
        }
        finally
        {
            reselecting = false;
        }
        SyncSelection();
    }

    // folders.go highlightFolderRow: the mailbox's SelectedEntryKey, without
    // re-entering the selection handler; none clears the highlight.
    private void SyncSelection()
    {
        if (mailbox is not { } m)
        {
            return;
        }
        var wanted = m.SelectedEntryKey is { } key ? Rows.FirstOrDefault(r => r.Key == key) : null;
        if (ReferenceEquals(FolderList.SelectedItem, wanted))
        {
            return;
        }
        reselecting = true;
        try
        {
            FolderList.SelectedItem = wanted;
        }
        finally
        {
            reselecting = false;
        }
    }

    // folders.go showFolderStatus (window.blp folder_stack).
    private void ShowStatus(SidebarStatus status)
    {
        if (status is SidebarStatus.Status page)
        {
            FolderStatusPage.Show(page.Icon, page.Title, page.Description);
            FolderStatusPage.Visibility = Visibility.Visible;
            FolderList.Visibility = Visibility.Collapsed;
            return;
        }
        FolderStatusPage.Visibility = Visibility.Collapsed;
        FolderList.Visibility = Visibility.Visible;
    }

    // window.go's row-selected handler: a folder the user selected becomes
    // the current one; a heading or a container does not take the
    // selection. An emptied selection changes nothing (GTK ignores a nil row).
    private void OnSelectionChanged(object sender, SelectionChangedEventArgs e)
    {
        if (reselecting || mailbox is not { } m)
        {
            return;
        }
        if (FolderList.SelectedItem is SidebarRow { Selectable: true, Folder: { } k } row)
        {
            m.SelectFolder(k, row.Key.Favourite);
            return;
        }
        if (FolderList.SelectedItem is not null)
        {
            SyncSelection();
        }
    }

    private void OnItemClick(object sender, ItemClickEventArgs e)
    {
        if (e.ClickedItem is SidebarRow { Selectable: true })
        {
            FolderChosen?.Invoke(this, EventArgs.Empty);
        }
    }

    // collapse.go addFolderShortcuts: Left folds, Right unfolds the focused
    // row; nothing to do lets the key through for the list's own navigation.
    private void OnListKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key is not (WinKey.Left or WinKey.Right) || mailbox is not { } m || FocusedRow() is not { TwistyActive: true } row)
        {
            return;
        }
        var collapse = e.Key == WinKey.Left;
        if (row.Kind == SidebarRowKind.AccountHeading && row.Account is { } acc)
        {
            e.Handled = m.SetAccountCollapsed(acc, collapse);
        }
        else if (row.Folder is { } k)
        {
            e.Handled = m.SetFolderCollapsed(k, collapse);
        }
    }

    private SidebarRow? FocusedRow()
    {
        var focused = FocusManager.GetFocusedElement(XamlRoot) as DependencyObject;
        while (focused is not null and not ListViewItem)
        {
            focused = VisualTreeHelper.GetParent(focused);
        }
        return focused is ListViewItem item ? FolderList.ItemFromContainer(item) as SidebarRow : null;
    }

    // Windows-only: the row's own actions as a context menu (right click,
    // Shift+F10, the menu key).
    private void OnContextRequested(UIElement sender, ContextRequestedEventArgs e)
    {
        var source = e.OriginalSource as DependencyObject;
        while (source is not null and not SelectorItem)
        {
            source = VisualTreeHelper.GetParent(source);
        }
        if (source is not ListViewItem item || FolderList.ItemFromContainer(item) is not SidebarRow row)
        {
            return;
        }
        var menu = new MenuFlyout();
        if (row.HasStar)
        {
            var star = new MenuFlyoutItem { Text = row.StarTooltip };
            star.Click += (_, _) => ToggleFavourite(row);
            menu.Items.Add(star);
        }
        if (row.TwistyActive)
        {
            var fold = new MenuFlyoutItem { Text = row.Collapsed ? L10n.T("Expand") : L10n.T("Collapse") };
            fold.Click += (_, _) => ToggleFold(row);
            menu.Items.Add(fold);
        }
        if (menu.Items.Count == 0)
        {
            return;
        }
        e.Handled = true;
        if (e.TryGetPosition(item, out var point))
        {
            menu.ShowAt(item, new FlyoutShowOptions { Position = point });
        }
        else
        {
            menu.ShowAt(item);
        }
    }
}
