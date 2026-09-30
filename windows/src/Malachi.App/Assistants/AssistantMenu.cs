// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Assistant/AssistantMenu.swift; GTK:
// ui/internal/window/assistant.go (assistantMenu, bindAssistantButton). The
// Assistant menu of the ✦ button of the main window's message pane and of a
// message window: the four message actions on what the window shows,
// Summarize Unread in This Folder (the main window's only), "Open In" with
// Claude Desktop, Claude Code and In App (Experimental) as a choice (a
// target whose links nothing handles, or In App while Claude Code is not
// found, cannot be chosen), and, while the chosen target cannot run the
// message actions, a disabled item that says why above "Set Up the
// Assistant…". Opening it asks the application's AssistantController to
// refresh (the handlers at once, the bridge in the background) and rebuilds
// the items from the last known state.
//
// Windows differences: "Open In" is a submenu of radio items (a WinUI menu
// has no section headers; GTK and macOS head a section with it), and the
// items are rebuilt at every opening (WinUI's menu has no pull-down
// button's title to lose).

using System;
using Malachi.App.Shell;
using Malachi.Core.Assistants;
using Malachi.Core.Settings;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;

namespace Malachi.App.Assistants;

/// <summary>One Assistant menu (a ✦ button's flyout).</summary>
internal sealed class AssistantMenu
{
    private readonly AppState state;
    private readonly Func<bool> canAsk;
    private readonly Action<AssistantAction> ask;
    private readonly Func<bool>? canSummarizeUnread;
    private readonly Action? summarizeUnread;
    private readonly Action setUp;

    /// <summary>
    /// A menu whose message actions run <paramref name="ask"/> while
    /// <paramref name="canAsk"/> (a message not in the Outbox is shown), with
    /// Summarize Unread in This Folder when <paramref name="summarizeUnread"/>
    /// is given; "Set Up the Assistant…" runs <paramref name="setUp"/>.
    /// </summary>
    public AssistantMenu(
        AppState state,
        Func<bool> canAsk,
        Action<AssistantAction> ask,
        Action setUp,
        Func<bool>? canSummarizeUnread = null,
        Action? summarizeUnread = null)
    {
        this.state = state;
        this.canAsk = canAsk;
        this.ask = ask;
        this.setUp = setUp;
        this.canSummarizeUnread = canSummarizeUnread;
        this.summarizeUnread = summarizeUnread;
        Flyout = new MenuFlyout { Placement = FlyoutPlacementMode.BottomEdgeAlignedRight };
        Flyout.Opening += (_, _) =>
        {
            state.Assistant.Refresh();
            Fill();
        };
        Fill();
    }

    /// <summary>The menu, for a button's Flyout.</summary>
    public MenuFlyout Flyout { get; }

    private void Fill()
    {
        var assistant = state.Assistant;
        var texts = Assistant.Texts();
        var (target, ok) = assistant.Pick(needsBridge: true);
        Flyout.Items.Clear();
        foreach (var a in Assistant.MessageActions)
        {
            var item = new MenuFlyoutItem { Text = Assistant.Label(a), IsEnabled = ok && canAsk() };
            item.Click += (_, _) => ask(a);
            Flyout.Items.Add(item);
        }
        if (summarizeUnread is { } unread)
        {
            Flyout.Items.Add(new MenuFlyoutSeparator());
            var item = new MenuFlyoutItem
            {
                Text = Assistant.Label(AssistantAction.Unread),
                IsEnabled = ok && (canSummarizeUnread?.Invoke() ?? true),
            };
            item.Click += (_, _) => unread();
            Flyout.Items.Add(item);
        }
        Flyout.Items.Add(new MenuFlyoutSeparator());
        var openIn = new MenuFlyoutSubItem { Text = texts.OpenIn };
        var chosen = state.Settings.AssistantTarget;
        foreach (var t in Assistant.Targets)
        {
            var item = new RadioMenuFlyoutItem
            {
                Text = Assistant.TargetName(t),
                GroupName = "assistant-target",
                IsChecked = t == chosen,
                IsEnabled = t == chosen || assistant.Availability(t).Handler,
            };
            item.Click += (_, _) => state.Settings.AssistantTarget = t;
            openIn.Items.Add(item);
        }
        Flyout.Items.Add(openIn);
        if (!ok)
        {
            Flyout.Items.Add(new MenuFlyoutSeparator());
            Flyout.Items.Add(new MenuFlyoutItem { Text = assistant.Problem(target), IsEnabled = false });
            var item = new MenuFlyoutItem { Text = texts.SetUp };
            item.Click += (_, _) => setUp();
            Flyout.Items.Add(item);
        }
    }
}
