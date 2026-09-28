// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/AccountsPaneViewController.swift
// (the view side: addClicked, the cells' callbacks, the table's drag and
// drop, moveSelected), AccountRowCell.swift (toggled, the buttons) and
// AccountsTableView.swift (mouseDownOnHandle, the ⌥⌘ keys); GTK:
// ui/internal/window/accounts_page.go (bindAccounts, the row's handlers,
// editAccount, signInAccount, presentEdit, removeAccount) and
// accounts_reorder.go (addRowReorder: the handle's drag, the drop, the
// Ctrl+Up / Ctrl+Down shortcuts). The page's logic is Core's
// AccountsPageController; this renders its rows with KeyedListSync's view
// overload, re-selects by key after every apply, and hands the clicks,
// the switch, the drags and the keys back (docs/windows-port.md §7.5).
//
// Windows: a click selects the row and Enabled is the switch alone (M21),
// because Ctrl+Up and Ctrl+Down move the selected row; the keys act only
// while the list has the focus, as GTK's row shortcuts and the macOS
// table's keys do: the list's PreviewKeyDown asks first (a ListView moves
// its focus with them before any accelerator sees them, measured), the
// window's MoveUp and MoveDown commands take them elsewhere in the list. A
// row is dragged by its handle only (DragItemsStarting refuses any other
// press), with the ListView's own insertion gap; foreign data dropped on
// the list is not a reorder and does nothing.

using System.Collections.ObjectModel;
using System.Linq;
using Malachi.App.Shell;
using Malachi.App.Wizard;
using Malachi.Core.Api;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Presentation;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Input;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using Windows.ApplicationModel.DataTransfer;
using VirtualKey = Windows.System.VirtualKey;

namespace Malachi.App.Preferences;

/// <summary>The Accounts page of the preferences.</summary>
public sealed partial class AccountsPage : UserControl
{
    private readonly AppState state;
    private readonly Window window;
    private readonly ILogger logger;

    // The list is being made to show the controller's rows: its selection
    // changes are not the user's.
    private bool syncing;

    // A switch is being put back to the row's state.
    private bool reverting;

    // The last press in the list was on a row's drag handle.
    private bool pressedOnHandle;

    // The row being dragged.
    private AccountRowView? dragged;

    /// <summary>The page of the preferences window <paramref name="window"/>.</summary>
    public AccountsPage(AppState state, Window window, IToasts toasts)
    {
        this.state = state;
        this.window = window;
        logger = state.Logs.CreateLogger<AccountsPage>();
        Controller = new AccountsPageController(state.Client, state.Logs.CreateLogger<AccountsPageController>());
        InitializeComponent();
        Controller.RowsChanged += (_, rows) => Apply(rows);
        Controller.ToastRequested += (_, text) => toasts.Show(text);
        Controller.FocusRequested += (_, id) => FocusRow(id);
        Controller.PropertyChanged += (_, e) =>
        {
            if (e.PropertyName == nameof(AccountsPageController.IsEnabled))
            {
                Apply(Controller.Rows);
            }
        };
        AccountList.AddHandler(PointerPressedEvent, new PointerEventHandler(OnListPointerPressed), handledEventsToo: true);
        // A ListView takes Ctrl+Up and Ctrl+Down for moving its focus before
        // the window's accelerators see them (measured): the list asks first.
        AccountList.PreviewKeyDown += OnListPreviewKeyDown;
        Controller.Load();
    }

    /// <summary>The page's logic.</summary>
    public AccountsPageController Controller { get; }

    /// <summary>The rows the list shows.</summary>
    public ObservableCollection<AccountRowView> Rows { get; } = [];

    /// <summary>
    /// Whether Ctrl+Up and Ctrl+Down move a row now: a row is selected and
    /// the list has the keyboard focus (GTK's shortcuts are the row's).
    /// </summary>
    public bool CanMove => Controller.SelectedId is not null && Controller.IsEnabled && ListHasFocus();

    /// <summary>Moves the selected row by <paramref name="delta"/> (Ctrl+Up, Ctrl+Down).</summary>
    public void MoveSelected(int delta) => Controller.MoveSelected(delta);

    /// <summary>The window closed: the page's calls are dropped.</summary>
    public void Close() => Controller.Close();

    private void Apply(System.Collections.Generic.IReadOnlyList<AccountRow> rows)
    {
        syncing = true;
        try
        {
            var group = Controller.IsEnabled;
            KeyedListSync.Apply(
                Rows, rows, r => r.Id, v => v.Id,
                r => new AccountRowView(r, group),
                (v, r) => v.Update(r, group));
            // The key is the truth: a moved row may have lost its selection.
            var selected = Controller.SelectedId is { } id ? Rows.FirstOrDefault(v => v.Id == id) : null;
            if (!ReferenceEquals(AccountList.SelectedItem, selected))
            {
                AccountList.SelectedItem = selected;
            }
        }
        finally
        {
            syncing = false;
        }
    }

    // The moved row takes the focus again (accounts_reorder.go: holding
    // Ctrl+Down keeps walking it down), once the list has laid it out.
    private void FocusRow(AccountId id) => DispatcherQueue.TryEnqueue(() =>
    {
        if (Rows.FirstOrDefault(v => v.Id == id) is { } row && AccountList.ContainerFromItem(row) is ListViewItem item)
        {
            item.Focus(FocusState.Keyboard);
        }
    });

    private bool ListHasFocus()
    {
        if (XamlRoot is null)
        {
            return false;
        }
        for (var e = FocusManager.GetFocusedElement(XamlRoot) as DependencyObject; e is not null; e = VisualTreeHelper.GetParent(e))
        {
            if (ReferenceEquals(e, AccountList))
            {
                return true;
            }
        }
        return false;
    }

    private void OnSelectionChanged(object sender, SelectionChangedEventArgs e)
    {
        if (!syncing)
        {
            Controller.Select((AccountList.SelectedItem as AccountRowView)?.Id);
        }
    }

    private void OnAddClick(object sender, RoutedEventArgs e) =>
        AccountWizardWindow.Show(state, window, done: (_, config) => Controller.AccountAdded(config));

    private void OnRowToggled(object sender, RoutedEventArgs e)
    {
        if (reverting || sender is not ToggleSwitch toggle || toggle.DataContext is not AccountRowView row || row.Applying)
        {
            return;
        }
        Controller.SetEnabled(row.Id, toggle.IsOn);
        // The page shows the switch as asked while the call runs; a flip it
        // refused goes back.
        if (toggle.IsOn != row.IsOn)
        {
            reverting = true;
            toggle.IsOn = row.IsOn;
            reverting = false;
        }
    }

    private void OnSignInClick(object sender, RoutedEventArgs e) => OpenWizard(sender, signIn: true);

    private void OnEditClick(object sender, RoutedEventArgs e) => OpenWizard(sender, signIn: false);

    // accounts_page.go editAccount / signInAccount / presentEdit: the
    // wizard over this window; once it saved, the page reloads and says so.
    private void OpenWizard(object sender, bool signIn)
    {
        if ((sender as FrameworkElement)?.DataContext is not AccountRowView row || Controller.Account(row.Id) is not { } account)
        {
            return;
        }
        AccountWizardWindow.Show(state, window, account, signIn, done: (_, config) => Controller.AccountSaved(config));
    }

    private void OnRemoveClick(object sender, RoutedEventArgs e)
    {
        if ((sender as FrameworkElement)?.DataContext is not AccountRowView row)
        {
            return;
        }
        Controller.Remove(row.Id, async p =>
            await state.Alerts.ConfirmDestructiveExtraAsync(window, p.Heading, p.Body, p.ConfirmLabel, p.ExtraLabel, p.ExtraDefault));
    }

    // Ctrl+Up and Ctrl+Down on the list (accounts_reorder.go's row
    // shortcuts): the selected row moves; at an end of the list the key
    // goes on to the list, as GTK's shortcut does not swallow it there.
    private void OnListPreviewKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key is not (VirtualKey.Up or VirtualKey.Down) || !IsOnlyControlDown())
        {
            return;
        }
        if (Controller.IsEnabled && Controller.MoveSelected(e.Key == VirtualKey.Up ? -1 : 1))
        {
            e.Handled = true;
        }
    }

    private static bool IsOnlyControlDown()
    {
        static bool Down(VirtualKey key) =>
            InputKeyboardSource.GetKeyStateForCurrentThread(key).HasFlag(Windows.UI.Core.CoreVirtualKeyStates.Down);
        return Down(VirtualKey.Control) && !Down(VirtualKey.Shift) && !Down(VirtualKey.Menu) && !Down(VirtualKey.LeftWindows) && !Down(VirtualKey.RightWindows);
    }

    // Where a press in the list landed: a drag may start only from a
    // row's handle (the GTK drag source's onHandle).
    private void OnListPointerPressed(object sender, PointerRoutedEventArgs e)
    {
        pressedOnHandle = false;
        for (var d = e.OriginalSource as DependencyObject; d is not null && d is not ListViewItem; d = VisualTreeHelper.GetParent(d))
        {
            if (d is FrameworkElement { Tag: "DragHandle" })
            {
                pressedOnHandle = true;
                return;
            }
        }
    }

    private void OnDragItemsStarting(object sender, DragItemsStartingEventArgs e)
    {
        var row = e.Items.Count == 1 ? e.Items[0] as AccountRowView : null;
        if (!pressedOnHandle || row is null || !row.IsInteractive)
        {
            e.Cancel = true;
            return;
        }
        dragged = row;
        e.Data.RequestedOperation = DataPackageOperation.Move;
    }

    // The list moved the row in its collection; the controller saves the
    // order (or puts the rows back when it cannot).
    private void OnDragItemsCompleted(ListViewBase sender, DragItemsCompletedEventArgs args)
    {
        var row = dragged;
        dragged = null;
        pressedOnHandle = false;
        if (row is null || args.DropResult != DataPackageOperation.Move)
        {
            return;
        }
        var to = Rows.IndexOf(row);
        if (to >= 0 && !Controller.MoveTo(row.Id, to))
        {
            LogReorderRefused(logger);
        }
    }

    [LoggerMessage(Level = LogLevel.Debug, Message = "a dragged account row was not moved")]
    private static partial void LogReorderRefused(ILogger logger);
}
