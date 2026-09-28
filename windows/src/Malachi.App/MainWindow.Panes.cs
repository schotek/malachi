// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MainWindow/MainSplitViewController.swift
// (the breakpoints, the pane widths kept from a wide layout),
// MainToolbar.swift (the search field: the pause, Return, the end of a
// search) and App/Integration.swift (install(sidebar:list:message:
// statusBar:), wireStatusBar, the banners' buttons, wireActions' main-window
// half, list.onActivateDraft); GTK: ui/data/ui/window.blp (the
// Adw.Breakpoints of outer_split and inner_split) and ui/internal/window/
// window.go (the SetShowContent calls of the row-selected handlers,
// registerActions), search.go (startSearch) and status.go (showOutbox). The
// main window's panes: the sidebar, the list, the message page's header bar
// and the status line (Main/), wired to the Integration's controllers once
// it exists, and the adaptive layout of docs/windows-port.md §11.1:
//
// - wider than 900, the sidebar is the SplitView's inline pane and the
//   panes are side by side, their widths dragged with the toolkit's sizers
//   (pointer only, as GTK and macOS have no keyboard handle) and kept in
//   folder-pane-width and message-list-width (only from this layout);
// - at 900 or less the sidebar is an overlay opened from the title bar's
//   pane button, closed by a folder chosen, a click outside or Escape;
// - at 600 or less list and message are one stack: choosing a message shows
//   it, the title bar's back button returns to the list, as GTK's collapsed
//   inner_split navigates.
//
// Core's PaneLayout decides; this applies it. The search box of the title
// bar drives the list's search (ListController.SearchFieldChanged: the
// 300 ms pause is Core's): Enter selects the first result, Escape empties
// the box and ends the search, and the keyboard goes to the list.

using System;
using System.Collections.Generic;
using Malachi.App.Main;
using Malachi.App.Shell;
using Malachi.Core.Controllers;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using WinKey = Windows.System.VirtualKey;

namespace Malachi.App;

/// <summary>The main window's panes and their layout.</summary>
public sealed partial class MainWindow
{
    private readonly PaneLayout layout = new();
    private readonly List<IDisposable> paneTokens = [];
    private ListController? list;
    private bool laidOut;

    // Before the Integration: the layout follows the window's width.
    private void InitializePanes()
    {
        Root.SizeChanged += (_, e) => OnWidthChanged(e.NewSize.Width);
        ListSplitter.IsTabStop = false;
        SidebarSizer.IsTabStop = false;
        ListSplitter.ManipulationCompleted += (_, _) => SavePaneWidths();
        SidebarSizer.ManipulationCompleted += (_, _) => SavePaneWidths();
        SidebarPane.FolderChosen += (_, _) => Navigate(layout.FolderChosen);
        ListPane.MessageChosen += (_, _) =>
        {
            if (layout.Mode == PaneMode.Narrow)
            {
                Navigate(layout.MessageChosen);
            }
        };
        SearchBox.TextChanged += OnSearchTextChanged;
        SearchBox.QuerySubmitted += OnSearchSubmitted;
        SearchBox.AddHandler(UIElement.PreviewKeyDownEvent, new KeyEventHandler(OnSearchKeyDown), handledEventsToo: true);
        Closed += (_, _) =>
        {
            foreach (var t in paneTokens)
            {
                t.Dispose();
            }
            paneTokens.Clear();
        };
    }

    // Integration.swift: the panes plug into the controllers.
    private void AttachPanes(Integration integration)
    {
        list = integration.List;
        new SelectionActions(integration.Actions, integration.List, () => this).Install(Commands);
        SidebarPane.Attach(integration.Mailbox, Commands);
        ListPane.Attach(integration.List, integration.Sync, Commands);
        MessageCommands.Attach(Commands);
        StatusLine.Attach(integration.Sync, integration.Mailbox);

        var repair = new AccountRepair(state, integration.Mailbox, integration.Sync, text => Toasts.Show(text));
        ListPane.AuthBannerInvoked += (_, _) => repair.AuthBannerButton();
        ListPane.CertBannerInvoked += (_, _) => repair.CertBannerButton();
        ListPane.ReconnectRequested += (_, _) => state.Connection.ReconnectNow();
        StatusLine.ActionRequested += (_, st) => repair.StatusAction(st);
        // status.go showOutbox: the account's outbox, and the list it shows.
        StatusLine.OutboxRequested += (_, acc) =>
        {
            if (integration.Mailbox.ShowOutbox(acc))
            {
                Navigate(layout.FolderChosen);
            }
        };
        // window.go row-activated: a draft opens in the compose window.
        integration.List.ActivateDraft += (_, s) => integration.Actions.OpenDraft(s.Id);
        // After the mailbox's own handler: the backend banner.
        paneTokens.Add(state.Notifications.AddConnectionState(ListPane.ShowConnectionState));
        ListPane.ShowConnectionState(state.Notifications.ConnectionState);
    }

    // search.go startSearch (win.search, Ctrl+F): a folded window shows the
    // list, and the box takes the keyboard with its text selected.
    private void BeginSearch()
    {
        Navigate(layout.ShowList);
        SearchBox.Focus(FocusState.Keyboard);
        if (FindDescendant<TextBox>(SearchBox) is { } box)
        {
            box.SelectAll();
        }
    }

    private void OnSearchTextChanged(AutoSuggestBox sender, AutoSuggestBoxTextChangedEventArgs args)
    {
        if (args.Reason == AutoSuggestionBoxTextChangeReason.UserInput)
        {
            list?.SearchFieldChanged(sender.Text);
        }
    }

    // Return: the first result, without waiting for the pause.
    private void OnSearchSubmitted(AutoSuggestBox sender, AutoSuggestBoxQuerySubmittedEventArgs args) =>
        list?.SearchFieldReturn(args.QueryText ?? "");

    // Escape empties the box and ends the search; the list takes the
    // keyboard (search.go onSearchModeChanged).
    private void OnSearchKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key != WinKey.Escape || list is null)
        {
            return;
        }
        e.Handled = true;
        SearchBox.Text = "";
        list.SearchFieldEnded();
        ListPane.FocusList();
    }

    private void OnPaneToggleRequested(TitleBar sender, object args)
    {
        layout.ToggleSidebar();
        ApplyLayout();
        if (layout.SidebarOverlay)
        {
            SidebarPane.FocusList();
        }
    }

    private void OnBackRequested(TitleBar sender, object args)
    {
        Navigate(layout.ShowList);
        ListPane.FocusList();
    }

    // A click outside the overlay, or Escape.
    private void OnPaneClosed(SplitView sender, object args)
    {
        if (layout.SidebarOverlay)
        {
            layout.CloseSidebar();
            ApplyLayout();
        }
    }

    private void Navigate(Action step)
    {
        step();
        ApplyLayout();
    }

    private void OnWidthChanged(double width)
    {
        if (width <= 0)
        {
            return;
        }
        laidOut = true;
        layout.Resize(width);
        ApplyLayout();
    }

    // Core's PaneLayout on the controls: the SplitView's mode, the columns,
    // the sizers and the title bar's buttons.
    private void ApplyLayout()
    {
        if (!laidOut)
        {
            return;
        }
        var widths = PaneLayout.Widths(layout.Mode, Root.ActualWidth, state.Settings.FolderPaneWidth, state.Settings.MessageListWidth);
        var inline = layout.SidebarInline;
        PaneSplit.DisplayMode = inline ? SplitViewDisplayMode.Inline : SplitViewDisplayMode.Overlay;
        PaneSplit.OpenPaneLength = widths.Sidebar;
        PaneSplit.IsPaneOpen = inline || layout.SidebarOverlay;
        OverlayBackground.Visibility = inline ? Visibility.Collapsed : Visibility.Visible;
        SidebarSizer.Visibility = inline ? Visibility.Visible : Visibility.Collapsed;
        ContentLayer.BorderThickness = inline ? new Thickness(1, 1, 0, 0) : new Thickness(0, 1, 0, 0);
        ContentLayer.CornerRadius = inline ? new CornerRadius(8, 0, 0, 0) : new CornerRadius(0);

        if (layout.Mode == PaneMode.Narrow)
        {
            ListColumn.MinWidth = 0;
            ListColumn.MaxWidth = double.PositiveInfinity;
            MessageColumn.MinWidth = 0;
            ListColumn.Width = layout.ListVisible ? new GridLength(1, GridUnitType.Star) : new GridLength(0);
            MessageColumn.Width = layout.MessageVisible ? new GridLength(1, GridUnitType.Star) : new GridLength(0);
            ListSplitter.Visibility = Visibility.Collapsed;
            ListDivider.Visibility = Visibility.Collapsed;
        }
        else
        {
            ListColumn.MinWidth = PaneLayout.ListMinimum;
            ListColumn.MaxWidth = PaneLayout.ListMaximum;
            MessageColumn.MinWidth = PaneLayout.MessageMinimum;
            ListColumn.Width = new GridLength(widths.List);
            MessageColumn.Width = new GridLength(1, GridUnitType.Star);
            ListSplitter.Visibility = Visibility.Visible;
            ListDivider.Visibility = Visibility.Visible;
        }
        ListPane.Visibility = layout.ListVisible ? Visibility.Visible : Visibility.Collapsed;
        MessagePane.Visibility = layout.MessageVisible ? Visibility.Visible : Visibility.Collapsed;
        AppTitleBar.IsPaneToggleButtonVisible = layout.PaneToggleVisible;
        AppTitleBar.IsBackButtonVisible = layout.BackVisible;
        // The narrow title bar has the back and pane buttons beside the box.
        SearchBox.MinWidth = layout.Mode == PaneMode.Narrow ? 120 : 240;
    }

    // MainSplitViewController.rememberPaneWidths: only from the wide layout.
    private void SavePaneWidths()
    {
        if (!laidOut || !layout.KeepsWidths)
        {
            return;
        }
        state.Settings.FolderPaneWidth = PaneLayout.ClampSidebar((int)Math.Round(PaneSplit.OpenPaneLength));
        state.Settings.MessageListWidth = PaneLayout.ClampList((int)Math.Round(ListColumn.ActualWidth));
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
