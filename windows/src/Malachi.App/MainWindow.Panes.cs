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
// the box and ends the search, and the keyboard goes to the list as soon as
// the folder's rows are back (MessageListPane.FocusList). The window's
// first focus goes to the sidebar's first tab stop (GTK's first focusable
// widget, the sidebar header's New Message), not to the search box, which
// WinUI would pick as the first tab stop now that the title bar is none: the
// letters of the single-key shortcuts would type there. F10 opens the
// sidebar's primary menu while the sidebar is shown (GTK: window.blp's
// primary MenuButton, which F10 opens only while it is mapped).

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
        Root.GettingFocus += OnFirstFocus;
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
        // F10: the primary menu, while the sidebar that holds its button is
        // shown (inline, or its overlay open); GTK's F10 passes over a menu
        // button that is not mapped, as in a collapsed window's content page.
        Commands.MainMenu.Handler = SidebarPane.ShowPrimaryMenu;
        Commands.MainMenu.CanExecute = () => PaneSplit.IsPaneOpen;
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
            // The outbox shows in the list: the board gives way (Board.ModeFor).
            ApplyModeRequest(Core.Boards.Board.Request.ShowOutbox);
            if (integration.Mailbox.ShowOutbox(acc))
            {
                Navigate(layout.FolderChosen);
            }
        };
        // After the mailbox's own handler: the backend banner. (A draft's
        // activation, window.go row-activated, is the reader's: ReaderHub.)
        paneTokens.Add(state.Notifications.AddConnectionState(ListPane.ShowConnectionState));
        ListPane.ShowConnectionState(state.Notifications.ConnectionState);
    }

    // search.go startSearch (win.search, Ctrl+F): a folded window shows the
    // list, and the box takes the keyboard with its text selected.
    private void BeginSearch()
    {
        Navigate(layout.ShowList);
        ListPane.CancelFocusList();
        SearchBox.Focus(FocusState.Keyboard);
        if (FindDescendant<TextBox>(SearchBox) is { } box)
        {
            box.SelectAll();
        }
    }

    // The window's first focus, which WinUI gives itself (programmatic,
    // nothing focused before; measured: it reports the keyboard as its
    // device), goes past the search box to the sidebar. A click or Ctrl+F
    // first is the user's and stays.
    private void OnFirstFocus(UIElement sender, GettingFocusEventArgs args)
    {
        Root.GettingFocus -= OnFirstFocus;
        if (args.OldFocusedElement is null && args.FocusState == FocusState.Programmatic
            && IsWithin(args.NewFocusedElement, SearchBox)
            && FocusManager.FindFirstFocusableElement(SidebarPane) is { } first)
        {
            args.TrySetNewFocusedElement(first);
        }
    }

    private static bool IsWithin(DependencyObject? element, DependencyObject ancestor)
    {
        for (var d = element; d is not null; d = VisualTreeHelper.GetParent(d))
        {
            if (ReferenceEquals(d, ancestor))
            {
                return true;
            }
        }
        return false;
    }

    private void OnSearchTextChanged(AutoSuggestBox sender, AutoSuggestBoxTextChangedEventArgs args)
    {
        if (args.Reason == AutoSuggestionBoxTextChangeReason.UserInput)
        {
            list?.SearchFieldChanged(sender.Text);
        }
    }

    // Return: the first result, without waiting for the pause.
    private void OnSearchSubmitted(AutoSuggestBox sender, AutoSuggestBoxQuerySubmittedEventArgs args)
    {
        if (!converting)
        {
            list?.SearchFieldReturn(args.QueryText ?? "");
        }
    }

    // Escape empties the box and ends the search; the list takes the
    // keyboard (search.go onSearchModeChanged).
    private void OnSearchKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key != WinKey.Escape || list is null)
        {
            return;
        }
        e.Handled = true;
        CancelOwnWords();
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
        layout.Resize(LayoutWidth(width));
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
        ApplyAssistantLayout();
        // The panes share what an open inline assistant panel leaves them.
        var content = Root.ActualWidth;
        if (AssistantSplit.IsPaneOpen && AssistantSplit.DisplayMode == SplitViewDisplayMode.Inline)
        {
            content -= AssistantSplit.OpenPaneLength;
        }
        // The message pane keeps what its buttons need (GTK's minimum width).
        var messageMinimum = Math.Max(PaneLayout.MessageMinimum, MessageCommands.MinimumWidth);
        var widths = PaneLayout.Widths(layout.Mode, content, state.Settings.FolderPaneWidth, state.Settings.MessageListWidth, messageMinimum);
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
            MessageColumn.MinWidth = messageMinimum;
            ListColumn.Width = new GridLength(widths.List);
            MessageColumn.Width = new GridLength(1, GridUnitType.Star);
            ListSplitter.Visibility = Visibility.Visible;
            ListDivider.Visibility = Visibility.Visible;
        }
        ListPane.Visibility = layout.ListVisible ? Visibility.Visible : Visibility.Collapsed;
        MessagePane.Visibility = layout.MessageVisible ? Visibility.Visible : Visibility.Collapsed;
        AppTitleBar.IsPaneToggleButtonVisible = layout.PaneToggleVisible && mode == Core.Boards.Board.Mode.Mail;
        AppTitleBar.IsBackButtonVisible = layout.BackVisible && mode == Core.Boards.Board.Mode.Mail;
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
