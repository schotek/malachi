// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/AIPaneViewController.swift
// (bindAssistant, followApplication, updateAssistantGroup); GTK:
// ui/internal/window/preferences.go (assistantGroupFor, bindAssistant). The
// Assistant group (ui/internal/assistant): Assistant Uses (AiPage.ChatGpt.cs),
// Show the Assistant Menu (assistant-menu), Open In (assistant-target) and,
// while In App is chosen, Model by the provider in effect: Claude's fixed
// list (assistant-model) or ChatGPT's catalogue (assistant-chatgpt-model);
// an AI provider account has none here, its models are chosen on its card in
// Integrations. With Claude Code the menu needs
// the bridge registered in a Claude client: without it both first rows are
// insensitive and the switch says to register in Integrations; while nothing
// is known they are insensitive and show the setting. ChatGPT and an AI
// provider account need no registration with In App. The bridge's status is
// asked whenever the page comes up (McpRegistrationController, its switch now
// in the Claude window of Integrations) and goes to the application's
// AssistantController, whose status the page shows. Register with Claude,
// the Claude Code row and the ChatGPT rows moved to the windows of the local
// integrations (ClaudeIntegrationWindow, ChatGptIntegrationWindow;
// docs/plans/ai-api.md §7.3).
//
// Windows differences: Open In lists all three targets (GTK lists Claude
// Desktop insensitive).

using System;
using CommunityToolkit.WinUI.Controls;
using Malachi.App.Shell;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers;
using Malachi.Core.Settings;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Preferences;

/// <summary>The AI page of the preferences.</summary>
[System.Diagnostics.CodeAnalysis.SuppressMessage("Design", "CA1001", Justification = "The window closes the controller (Close).")]
public sealed partial class AiPage : UserControl
{
    private readonly AppState state;
    private readonly Func<Window?> window;
    private readonly McpRegistrationController registration;

    // The switch is being set from the page, not by the user.
    private bool syncing;
    private bool closed;

    /// <summary>The page of a preferences window over <paramref name="state"/>.</summary>
    internal AiPage(AppState state, SettingBindings bindings, Func<Window?> window)
    {
        InitializeComponent();
        this.state = state;
        this.window = window;
        // The bridge's status only: the switch is the Claude window's.
        registration = new McpRegistrationController(state.Paths.McpBridge, logger: state.Logs.CreateLogger<McpRegistrationController>());
        registration.RegisteredChanged += (_, _) =>
        {
            // The Assistant menus know at once (and this page's group).
            if (registration.Status is { } status)
            {
                state.Assistant.Apply(status);
            }
            UpdateAssistantGroup();
        };
        if (state.Assistant.Status is { } known)
        {
            registration.Adopt(known);
        }

        // The Assistant group.
        var texts = Assistant.Texts();
        AssistantHeading.Text = texts.Assistant;
        AssistantDescription.Text = texts.Description;
        AssistantMenuRow.Header = texts.ShowMenu;
        AutomationProperties.SetName(AssistantMenuSwitch, texts.ShowMenu);
        AssistantTargetRow.Header = texts.OpenIn;
        AutomationProperties.SetName(AssistantTargetBox, texts.OpenIn);
        foreach (var t in Assistant.Targets)
        {
            AssistantTargetBox.Items.Add(new ComboBoxItem { Content = Assistant.TargetName(t) });
        }
        var panel = Assistant.PanelTexts();
        AssistantModelRow.Header = panel.Model;
        AutomationProperties.SetName(AssistantModelBox, panel.Model);
        foreach (var m in Assistant.Models)
        {
            AssistantModelBox.Items.Add(new ComboBoxItem { Content = Assistant.ModelName(m) });
        }
        bindings.Choice(AssistantTargetBox, SettingsKey.AssistantTarget, Assistant.Targets, () => state.Settings.AssistantTarget, v => state.Settings.AssistantTarget = v);
        bindings.Choice(AssistantModelBox, SettingsKey.AssistantModel, Assistant.Models, () => state.Settings.AssistantModel, v => state.Settings.AssistantModel = v);
        // Assistant Uses and Model over ChatGPT (AiPage.ChatGpt.cs).
        InitializeProvider();
        // The board's triage (AiPage.Board.cs).
        InitializeBoardGroup(bindings);
        // About You (AiPage.Profile.cs).
        InitializeProfileGroup();
        state.Assistant.Changed += OnAssistantChanged;
        UpdateAssistantGroup();
    }

    /// <summary>
    /// The page came up: the bridge is asked for its status (every time, as
    /// on macOS), and Claude Code and the Claude apps' handlers are looked up
    /// again (installed or signed in meanwhile).
    /// </summary>
    public void Refresh()
    {
        registration.Load();
        state.ClaudeCode.Refresh();
        state.Assistant.RefreshHandlers();
        RefreshProvider();
        UpdateAssistantGroup();
        RefreshBoardGroup();
        RefreshProfileGroup();
    }

    /// <summary>The window closed: a status run ends.</summary>
    public void Close()
    {
        closed = true;
        registration.Close();
        // The controllers are the application's: they go on without the page.
        state.Assistant.Changed -= OnAssistantChanged;
        CloseBoardGroup();
        CloseProfileGroup();
        CloseProvider();
    }

    // A status the application learned (at launch, when a window became
    // active, when a menu opened, in the Claude window) is this page's too;
    // the accounts it lists are Assistant Uses' entries.
    private void OnAssistantChanged(object? sender, EventArgs e)
    {
        if (closed)
        {
            return;
        }
        if (state.Assistant.Status is { } known)
        {
            registration.Adopt(known);
        }
        UpdateAssistantGroup();
        QueueProviderUpdate();
        UpdateBoardGroup();
    }

    // The Assistant group (preferences.go assistantGroupFor, bindAssistant)

    private bool BridgeRegistered => state.Assistant.Status is not null ? state.Assistant.Registered : registration.IsRegistered;

    private bool BridgeKnown => state.Assistant.Status is not null || registration.Status is not null;

    private void UpdateAssistantGroup()
    {
        if (closed)
        {
            return;
        }
        var settings = state.Settings;
        var source = state.Assistant.Source;
        var local = source == AssistantProviderSource.Claude;
        var app = settings.AssistantTarget == AssistantTarget.App;
        var registered = BridgeRegistered;
        var known = BridgeKnown;
        // assistantGroupFor: registered, the switch shows the key and Open In
        // the problem; known not to be registered, both rows are insensitive
        // and the switch is off and says why; not known yet, both rows are
        // insensitive and the switch shows the key.
        var sensitive = registered;
        var on = registered ? Assistant.Shown(settings.AssistantMenu, true) : !known && settings.AssistantMenu;
        var registerFirst = !registered && known;
        var targetSubtitle = registered ? state.Assistant.Problem(settings.AssistantTarget) : "";
        // ChatGPT and an AI provider account need no registration with In App.
        if (!local && app)
        {
            sensitive = true;
            on = settings.AssistantMenu;
            registerFirst = false;
        }
        AssistantDescription.Text = source == AssistantProviderSource.ChatGpt ? Assistants.ChatGptText.Description : Assistant.Texts().Description;
        AssistantMenuRow.IsEnabled = sensitive;
        AssistantTargetRow.IsEnabled = sensitive || !local;
        syncing = true;
        AssistantMenuSwitch.IsOn = on;
        syncing = false;
        Describe(AssistantMenuRow, registerFirst ? Assistant.ApiText().RegisterInIntegrations : "");
        Describe(AssistantTargetRow, targetSubtitle);
        // An AI provider account chooses its models on its own card in
        // Integrations.
        AssistantModelRow.Visibility = app && source == AssistantProviderSource.Claude ? Visibility.Visible : Visibility.Collapsed;
        AssistantChatGptModelRow.Visibility = app && source == AssistantProviderSource.ChatGpt ? Visibility.Visible : Visibility.Collapsed;
        AssistantModelRow.IsEnabled = sensitive || !local;
        // Nothing to run on: the whole group is grey, Assistant Uses says
        // where an assistant comes from (UpdateProviderChoices) and the
        // heading offers the way.
        var nothing = Assistant.NothingSetUp(state.Assistant.ProviderChoices(), registered, known);
        if (nothing)
        {
            AssistantMenuRow.IsEnabled = false;
            AssistantTargetRow.IsEnabled = false;
            AssistantModelRow.IsEnabled = false;
            AssistantChatGptModelRow.IsEnabled = false;
            Describe(AssistantMenuRow, "");
            Describe(AssistantTargetRow, "");
        }
        else
        {
            AssistantChatGptModelRow.IsEnabled = true;
        }
        AssistantProviderRow.IsEnabled = !nothing;
        AssistantSetUpButton.Content = Assistant.ApiText().SetUpInIntegrations;
        AssistantSetUpButton.Visibility = nothing ? Visibility.Visible : Visibility.Collapsed;
    }

    // Set Up in Integrations…: the Integrations page of this window.
    private void OnAssistantSetUpClick(object sender, RoutedEventArgs e) => state.Hooks.OpenIntegrations?.Invoke(null);

    // The switch writes assistant-menu while the bridge is registered (or
    // without it for ChatGPT and an AI provider account with In App).
    private void OnAssistantMenuToggled(object sender, RoutedEventArgs e)
    {
        if (syncing)
        {
            return;
        }
        if (!BridgeRegistered && !(state.Settings.AssistantTarget == AssistantTarget.App && state.Assistant.Source != AssistantProviderSource.Claude))
        {
            UpdateAssistantGroup();
            return;
        }
        state.Settings.AssistantMenu = AssistantMenuSwitch.IsOn;
    }

    // A row's description line, or none at all for "".
    private static void Describe(SettingsCard row, string text)
    {
        if (text.Length == 0)
        {
            row.ClearValue(SettingsCard.DescriptionProperty);
        }
        else
        {
            row.Description = text;
        }
    }
}
