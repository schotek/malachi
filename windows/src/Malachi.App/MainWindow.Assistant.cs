// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MainWindow/MainSplitViewController.swift
// (the inspector: the assistant panel folded first), MainToolbar.swift (the
// Assistant's items) and MainWindowController.swift (showAssistant); GTK:
// ui/internal/window/assistant.go (bindAssistantButton, syncAssistantActions)
// and assistant_panel.go (syncShown, updateToggle, reveal, followSelection).
// The main window's half of the Assistant (ui/internal/assistant): the ✦
// button of the message pane with the Assistant menu (the main window's has
// Summarize Unread in This Folder), shown while the Assistant is, and the
// assistant panel on the right (AssistantSplit) with its toggle beside the ✦
// button, shown while In App is chosen; a panel that may no longer be shown
// folds. With In App chosen the ✦ button has no menu and opens the panel
// (Assistant.ButtonOpensPanel). The panel follows the list's selection. Its
// width and whether it is a pane or an overlay follow the window's width
// (Core's PaneLayout).
//
// Windows differences: the panel is not resized by dragging (GTK's
// Adw.OverlaySplitView neither; macOS keeps a dragged width), and an overlay
// panel is closed by a click outside it or Escape, as WinUI's SplitView
// does (GTK's overlay closes on a click outside as well). An open inline
// panel takes its width from the three panes, whose layout (the sidebar
// folded below PaneLayout.SidebarBreakpoint) follows what it leaves them:
// GTK's breakpoints follow the window, and its panes' minimum widths keep
// the message pane's buttons whole, where WinUI would cut them off.

using System;
using Malachi.App.Assistants;
using Malachi.App.Preferences;
using Malachi.App.Reader;
using Malachi.App.Shell;
using Malachi.Core.Assistants;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App;

/// <summary>The main window's Assistant: its menu and its panel.</summary>
public sealed partial class MainWindow
{
    private AssistantActions? assistantActions;
    private AssistantPanelHost? assistantHost;

    // The ✦ button's menu, its flyout while the button is a menu
    // (Assistant.ButtonOpensPanel says when it opens the panel instead).
    private AssistantMenu? assistantMenu;

    // The panel's pane state is being set from code, not by the toggle.
    private bool assistantSyncing;

    /// <summary>The Assistant's actions of the main window and its message windows (null before the Integration).</summary>
    internal AssistantActions? AssistantActions => assistantActions;

    /// <summary>
    /// The Integration and the reader exist: the ✦ menu, the panel and its
    /// conversation, following the application's Assistant state.
    /// </summary>
    internal void AttachAssistant(Integration integration, ReaderServices reader)
    {
        var actions = new AssistantActions(state, integration.List, reader);
        assistantActions = actions;
        var host = new AssistantPanelHost(state, this, integration, reader, AssistantPanelView);
        assistantHost = host;
        actions.Panel = host;

        var texts = Assistant.Texts();
        var menu = new AssistantMenu(
            state,
            canAsk: () => integration.List.SelectedRow is { } row && !integration.List.Mailbox.Model.InOutbox(row.Message),
            ask: a => actions.Ask(a, this),
            setUp: () => PreferencesWindow.Show(state)?.ShowAi(),
            canSummarizeUnread: () => actions.CanSummarizeUnread,
            summarizeUnread: () => actions.SummarizeUnread(this));
        assistantMenu = menu;
        var button = MessageCommands.Assistant;
        button.Click += (_, _) => OnAssistantButtonClick();
        ToolTipService.SetToolTip(button, texts.Assistant);
        AutomationProperties.SetName(button, texts.Assistant);

        var toggle = MessageCommands.AssistantPanel;
        toggle.Checked += (_, _) => SetAssistantOpen(true);
        toggle.Unchecked += (_, _) => SetAssistantOpen(false);

        integration.List.SelectedMessageChanged += (_, _) => host.FollowSelection();
        integration.List.SelectionCleared += (_, _) => host.FollowSelection();
        // The quick action Summarize Unread in This Folder acts on the
        // sidebar's folder under the Assistant menu item's condition, and
        // follows it wherever the list's heading does (a folder selected,
        // the folders known) and with the search.
        AssistantPanelView.UnreadFolderAvailable = () => actions.CanSummarizeUnread;
        AssistantPanelView.SummarizeUnreadRequested = actions.SummarizeUnreadInPanel;
        integration.List.Mailbox.ListTitleChanged += (_, _) => AssistantPanelView.UpdateQuickActions();
        integration.List.SearchBarChanged += (_, _) => AssistantPanelView.UpdateQuickActions();
        AssistantPanelView.UpdateQuickActions();
        state.Assistant.Changed += (_, _) => SyncAssistant();
        SetupOwnWords();
        // The application asks again whenever it becomes active (macOS
        // AppDelegate): a Claude app may have been installed or registered.
        Activated += (_, e) =>
        {
            if (e.WindowActivationState != WindowActivationState.Deactivated)
            {
                state.Assistant.Refresh();
            }
        };
        Closed += (_, _) => host.Dispose();
        SyncAssistant();
        UpdateAssistantToggle();
        host.FollowSelection();
    }

    /// <summary>Unfolds the assistant panel, the main window brought forward (assistant_panel.go reveal).</summary>
    internal void RevealAssistant()
    {
        state.ShowMainWindow();
        // The panel is the mail's: the board gives way (Board.ModeFor).
        ApplyModeRequest(Core.Boards.Board.Request.RevealAssistant);
        SetAssistantOpen(true);
    }

    // The ✦ button with In App chosen (Assistant.ButtonOpensPanel; GTK
    // openAssistantPanel): the panel unfolds and its question field, under
    // the quick actions, takes the keyboard; an open panel stays open and
    // only gets the keyboard (its toggle folds it). The state is asked for
    // again, as the menu asks when it opens. With a Claude app the button
    // has its flyout, and the click only opens it.
    private void OnAssistantButtonClick()
    {
        if (MessageCommands.Assistant.Flyout is not null || !state.Assistant.PanelShown)
        {
            return;
        }
        state.Assistant.Refresh();
        RevealAssistant();
        // After the panel has unfolded.
        DispatcherQueue.TryEnqueue(Microsoft.UI.Dispatching.DispatcherQueuePriority.Low, AssistantPanelView.FocusInput);
    }

    // The ✦ button while the Assistant is shown, the panel's toggle while the
    // panel may be; a panel that may not be folds. The ✦ button is the menu,
    // or the panel's opener while In App is chosen.
    private void SyncAssistant()
    {
        var assistant = state.Assistant;
        MessageCommands.Assistant.Visibility = assistant.Shown ? Visibility.Visible : Visibility.Collapsed;
        var opensPanel = Assistant.ButtonOpensPanel(assistant.Settings.AssistantTarget, hasPanel: true);
        MessageCommands.Assistant.Flyout = opensPanel ? null : assistantMenu?.Flyout;
        var panel = assistant.PanelShown;
        MessageCommands.AssistantPanel.Visibility = panel ? Visibility.Visible : Visibility.Collapsed;
        if (!panel)
        {
            SetAssistantOpen(false);
        }
        // The message pane's buttons changed, and with them what it needs.
        ApplyLayout();
    }

    private void SetAssistantOpen(bool open)
    {
        if (open && !state.Assistant.PanelShown)
        {
            open = false;
        }
        if (assistantSyncing)
        {
            return;
        }
        assistantSyncing = true;
        AssistantSplit.IsPaneOpen = open;
        MessageCommands.AssistantPanel.IsChecked = open;
        assistantSyncing = false;
        UpdateAssistantToggle();
        if (Root.ActualWidth > 0)
        {
            layout.Resize(LayoutWidth(Root.ActualWidth));
        }
        ApplyLayout();
    }

    // The width the three panes are laid out for: the window's, less an
    // open inline panel.
    private double LayoutWidth(double window) =>
        AssistantSplit.IsPaneOpen && PaneLayout.AssistantInline(window) ? window - PaneLayout.AssistantWidth(window) : window;

    // An overlay panel closed by a click outside it or Escape.
    private void OnAssistantPaneClosed(SplitView sender, object args)
    {
        if (!assistantSyncing)
        {
            SetAssistantOpen(false);
        }
    }

    private void UpdateAssistantToggle()
    {
        var t = Assistant.PanelTexts();
        var text = AssistantSplit.IsPaneOpen ? t.Hide : t.Show;
        ToolTipService.SetToolTip(MessageCommands.AssistantPanel, text);
        AutomationProperties.SetName(MessageCommands.AssistantPanel, text);
    }

    // The panel's place for the window's width: a pane of its own beside
    // the others wider than PaneLayout.AssistantBreakpoint, otherwise an
    // overlay; its width a share of the window's.
    private void ApplyAssistantLayout()
    {
        var width = Root.ActualWidth;
        if (width <= 0)
        {
            return;
        }
        var inline = PaneLayout.AssistantInline(width);
        AssistantSplit.DisplayMode = inline ? SplitViewDisplayMode.Inline : SplitViewDisplayMode.Overlay;
        AssistantSplit.OpenPaneLength = PaneLayout.AssistantWidth(width);
    }
}
