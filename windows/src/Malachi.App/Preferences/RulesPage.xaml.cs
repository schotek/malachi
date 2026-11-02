// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/RulesPaneViewController.swift
// (the cells' callbacks, the table's drag and drop, moveSelected);
// GTK: ui/internal/window/rules_page.go (docs/plans/rules.md R14, T2).
// Preferences → Rules over Core's RulesController (the application's, in
// AppState.Rules): the rules as last listed, asked again whenever the page
// comes up and on every notify.rulesChanged (AppRules). This renders its
// rows with KeyedListSync's view overload, re-selects by key after every
// apply, and hands the clicks, the switch, the drags and the keys back
// (docs/windows-port.md §7.5).
//
// Windows: as the account lists (AccountListSection) a click selects a row
// and the switch is the switch alone, because Ctrl+Up and Ctrl+Down move the
// selected one; the keys act only while the list has the focus (the list's
// PreviewKeyDown asks first, the window's MoveUp and MoveDown commands take
// them elsewhere in the list). A row is dragged by its handle only. The
// controller shows a move at once and puts it back when the daemon refuses
// it; the switch is put back to the row's state when a question was
// declined or the call failed. A failure is a toast of the sentence the
// controller made. Remove asks the destructive question of the dialog
// service; the outgoing question of a switch is the Turn On / Cancel one.

using System;
using System.Collections.ObjectModel;
using System.Linq;
using System.Threading.Tasks;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Contacts;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Rules;
using Malachi.Core.Text;
using Microsoft.UI.Input;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using Windows.ApplicationModel.DataTransfer;
using VirtualKey = Windows.System.VirtualKey;

namespace Malachi.App.Preferences;

/// <summary>Preferences → Rules.</summary>
public sealed partial class RulesPage : UserControl
{
    private readonly AppState state;
    private readonly Window window;
    private readonly IToasts toasts;
    private readonly RulesController controller;

    // The list is being made to show the controller's rows: its selection changes are not the user's.
    private bool syncing;

    // A switch is being put back to the row's state.
    private bool reverting;

    // The last press in the list was on a row's drag handle.
    private bool pressedOnHandle;

    // The row being dragged (from DragItemsStarting to DragItemsCompleted), and where it was.
    private RuleRowView? dragged;
    private int draggedFrom = -1;

    private string? selectedId;
    private bool closed;

    /// <summary>The page of <paramref name="window"/>, its toasts in <paramref name="toasts"/>.</summary>
    public RulesPage(AppState state, Window window, IToasts toasts)
    {
        ArgumentNullException.ThrowIfNull(state);
        this.state = state;
        this.window = window;
        this.toasts = toasts;
        controller = state.Rules.Controller;
        InitializeComponent();
        DescriptionText.Text = RulesText.PageDescription;
        AddButton.Content = RulesText.AddRule;
        EmptyTitleText.Text = RulesText.EmptyTitle;
        EmptyDescriptionText.Text = RulesText.EmptyDescription;
        controller.Changed += OnControllerChanged;
        RuleList.AddHandler(PointerPressedEvent, new PointerEventHandler(OnListPointerPressed), handledEventsToo: true);
        // A ListView takes Ctrl+Up and Ctrl+Down for moving its focus before
        // the window's accelerators see them (measured): the list asks first.
        RuleList.PreviewKeyDown += OnListPreviewKeyDown;
        Apply();
    }

    /// <summary>The rows the list shows.</summary>
    public ObservableCollection<RuleRowView> Rows { get; } = [];

    /// <summary>Whether Ctrl+Up and Ctrl+Down move a row now: a row is selected and the list has the keyboard focus.</summary>
    public bool CanMove => selectedId is not null && ListHasFocus();

    /// <summary>The rules anew (the page came up).</summary>
    public void Refresh() => controller.Refresh();

    /// <summary>Moves the selected row by <paramref name="delta"/> (Ctrl+Up, Ctrl+Down).</summary>
    public void MoveSelected(int delta) => _ = MoveSelectedAsync(delta);

    /// <summary>The window closes: no change is taken any more.</summary>
    public void Close()
    {
        if (closed)
        {
            return;
        }
        closed = true;
        controller.Changed -= OnControllerChanged;
    }

    private void OnControllerChanged(object? sender, EventArgs e) => Apply();

    // The names a row shows for an account and a folder, as the main window's mailbox knows them.
    private RuleNames Names() => new(Account: AccountName, Folder: FolderName);

    private string AccountName(AccountId id) =>
        state.Rules.MailAccounts.FirstOrDefault(a => a.Id == id) is { } a ? ContactBook.AccountName(a) : "";

    private string FolderName(AccountId account, FolderId folder)
    {
        if (state.Integration is not { } integration || !integration.Mailbox.Model.Folders.TryGetValue(account, out var known))
        {
            return "";
        }
        return known.FirstOrDefault(f => f.Id == folder) is { } f ? FolderTree.FolderTitle(f) : "";
    }

    private void Apply()
    {
        if (closed)
        {
            return;
        }
        ErrorText.Text = controller.Error;
        ErrorText.Visibility = controller.Error.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        var rows = RulesText.Rows(controller.Rules, Names(), t => Format.FormatDateTime(t));
        syncing = true;
        try
        {
            KeyedListSync.Apply(Rows, rows, r => r.Id, v => v.Id, r => new RuleRowView(r), (v, r) => v.Update(r));
            // The key is the truth: a moved row may have lost its selection.
            var selected = selectedId is { } id ? Rows.FirstOrDefault(v => v.Id == id) : null;
            if (!ReferenceEquals(RuleList.SelectedItem, selected))
            {
                RuleList.SelectedItem = selected;
            }
            if (selected is null)
            {
                selectedId = null;
            }
        }
        finally
        {
            syncing = false;
        }
        var empty = Rows.Count == 0 && controller.Loaded;
        EmptyState.Visibility = empty ? Visibility.Visible : Visibility.Collapsed;
        RuleList.Visibility = Rows.Count > 0 ? Visibility.Visible : Visibility.Collapsed;
    }

    private void OnSelectionChanged(object sender, SelectionChangedEventArgs e)
    {
        // A drag moves its row out of the collection and back in.
        if (syncing || (dragged is not null && e.AddedItems.Count == 0))
        {
            return;
        }
        selectedId = (RuleList.SelectedItem as RuleRowView)?.Id;
    }

    private void OnAddClick(object sender, RoutedEventArgs e) => state.Rules.AddRule(window);

    private void OnEditClick(object sender, RoutedEventArgs e)
    {
        if ((sender as FrameworkElement)?.DataContext is RuleRowView row)
        {
            state.Rules.EditRule(window, row.Id);
        }
    }

    private async void OnRemoveClick(object sender, RoutedEventArgs e)
    {
        if ((sender as FrameworkElement)?.DataContext is not RuleRowView row)
        {
            return;
        }
        var result = await controller.DeleteAsync(
            row.Id, q => state.Alerts.ConfirmDestructiveAsync(window, q.Heading, q.Body, q.Confirm));
        Report(result);
    }

    // The switch: the controller asks the outgoing question when a rule that
    // sends mail is turned on; a switch it did not flip goes back.
    private async void OnRowToggled(object sender, RoutedEventArgs e)
    {
        if (reverting || sender is not ToggleSwitch toggle || toggle.DataContext is not RuleRowView row || row.Applying)
        {
            return;
        }
        var on = toggle.IsOn;
        var result = await controller.SetEnabledAsync(
            row.Id, on, q => state.Alerts.ConfirmAsync(window, q.Heading, q.Body, q.Confirm, L10n.T("Cancel")));
        // The container may have gone to another row meanwhile.
        if (!closed && ReferenceEquals(toggle.DataContext, row) && toggle.IsOn != row.IsOn)
        {
            reverting = true;
            toggle.IsOn = row.IsOn;
            reverting = false;
        }
        Report(result);
    }

    private void Report(RuleResult result)
    {
        if (!closed && result.Kind == RuleResultKind.Failed && result.Text.Length > 0)
        {
            toasts.Show(result.Text);
        }
    }

    // ----- moving a rule -----

    private async Task MoveSelectedAsync(int delta)
    {
        if (selectedId is not { } id)
        {
            return;
        }
        var from = Rows.ToList().FindIndex(v => v.Id == id);
        var to = from + delta;
        if (from < 0 || to < 0 || to >= Rows.Count)
        {
            return;
        }
        var result = await controller.MoveAsync(from, to);
        FocusRow(id);
        Report(result);
    }

    // The moved row takes the focus again: holding Ctrl+Down keeps walking it down.
    private void FocusRow(string id) => DispatcherQueue.TryEnqueue(() =>
    {
        if (Rows.FirstOrDefault(v => v.Id == id) is { } row && RuleList.ContainerFromItem(row) is ListViewItem item)
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
            if (ReferenceEquals(e, RuleList))
            {
                return true;
            }
        }
        return false;
    }

    // Ctrl+Up and Ctrl+Down on the list: the selected row moves; at an end of
    // the list the key goes on to the list.
    private void OnListPreviewKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key is not (VirtualKey.Up or VirtualKey.Down) || !IsOnlyControlDown() || selectedId is not { } id)
        {
            return;
        }
        var from = Rows.ToList().FindIndex(v => v.Id == id);
        var to = from + (e.Key == VirtualKey.Up ? -1 : 1);
        if (from < 0 || to < 0 || to >= Rows.Count)
        {
            return;
        }
        e.Handled = true;
        _ = MoveSelectedAsync(to - from);
    }

    private static bool IsOnlyControlDown()
    {
        static bool Down(VirtualKey key) =>
            InputKeyboardSource.GetKeyStateForCurrentThread(key).HasFlag(Windows.UI.Core.CoreVirtualKeyStates.Down);
        return Down(VirtualKey.Control) && !Down(VirtualKey.Shift) && !Down(VirtualKey.Menu) && !Down(VirtualKey.LeftWindows) && !Down(VirtualKey.RightWindows);
    }

    // Where a press in the list landed: a drag may start only from a row's handle.
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
        var row = e.Items.Count == 1 ? e.Items[0] as RuleRowView : null;
        if (!pressedOnHandle || row is null)
        {
            e.Cancel = true;
            return;
        }
        dragged = row;
        draggedFrom = Rows.IndexOf(row);
        e.Data.RequestedOperation = DataPackageOperation.Move;
    }

    // The list moved the row in its collection; the controller saves the
    // order (or puts the rows back when the daemon refuses it).
    private async void OnDragItemsCompleted(ListViewBase sender, DragItemsCompletedEventArgs args)
    {
        var row = dragged;
        var from = draggedFrom;
        dragged = null;
        draggedFrom = -1;
        pressedOnHandle = false;
        if (row is null || from < 0 || args.DropResult != DataPackageOperation.Move)
        {
            return;
        }
        var to = Rows.IndexOf(row);
        if (to < 0)
        {
            return;
        }
        // The list has the new order already; the controller starts from the one before.
        var result = await controller.MoveAsync(from, to);
        Report(result);
    }
}
