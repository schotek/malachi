// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/AIPaneViewController.swift
// (bindIfReady, setSwitch, registerChanged, the status on every
// appearance, bindClaudeDesktop, restartClaudeDesktop, bindAssistant,
// followApplication, updateAssistantGroup, showClaudeCode,
// claudeCodeState, claudePathChanged, chooseClaudeCode); GTK:
// ui/internal/window/preferences.go (bindMCP, assistantGroupFor,
// bindAssistant, bindClaudeCode, claudeCodeState). The switch shows what
// the bundled malachi-mcp.exe last confirmed, through McpRegistrationController
// (status, install, uninstall with --json and the Windows paths,
// docs/windows-port.md §10); the application's last status is shown at once
// (Adopt) and so is every newer one while no call of the page runs, and a
// failed status check is repeated after 1, 2 and 4 s. A missing bridge, or
// a status the bridge did not give when the repeats are used up and nothing
// is known, goes into the group's description, as in GTK (U2, U3); a failed
// install or uninstall flips the switch back with a toast.
//
// A flip while Claude Desktop runs as a present client asks "Restart Claude
// Desktop?" first (the application's ClaudeDesktopController, macOS's
// offer): Restart quits it, writes the change and starts it again; Later
// writes now and leaves the change pending, with the row Claude Desktop
// under the switch and its Restart until Claude Desktop quits.
//
// The Assistant group (ui/internal/assistant): Show the Assistant Menu
// (assistant-menu), Open In (assistant-target) and, while In App is chosen,
// Claude Code (the claude.exe the panel runs, Choose…) and Model
// (assistant-model). Without the bridge registered in any Claude client
// both first rows are insensitive and the switch says why; while nothing is
// known they are insensitive and show the setting.
//
// Windows differences: Choose… offers .exe files only (the panel runs only
// a claude.exe, windows/README.md); Open In lists all three targets (GTK
// lists Claude Desktop insensitive).

using System;
using CommunityToolkit.WinUI.Controls;
using Malachi.App.Compose;
using Malachi.App.Shell;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.Platform;
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
    private readonly IToasts toasts;
    private readonly Func<Window?> window;
    private readonly McpRegistrationController registration;

    // The switch is being set from the controller, not by the user.
    private bool syncing;

    // "Restart Claude Desktop?" is up or its change runs: the switch shows
    // the user's choice until it is written, whatever the application
    // learns meanwhile.
    private bool asking;

    // Bumped by every look at Claude Code: a late answer is dropped.
    private int claudeCodeGen;
    private bool closed;

    /// <summary>The page of a preferences window over <paramref name="state"/>.</summary>
    internal AiPage(AppState state, SettingBindings bindings, IToasts toasts, Func<Window?> window)
    {
        InitializeComponent();
        this.state = state;
        this.toasts = toasts;
        this.window = window;
        registration = new McpRegistrationController(state.Paths.McpBridge, logger: state.Logs.CreateLogger<McpRegistrationController>());
        registration.EnabledChanged += (_, _) => UpdateRegisterRow();
        registration.RegisteredChanged += (_, on) =>
        {
            SetSwitch(on);
            // The Assistant menus know at once (and this page's group).
            if (registration.Status is { } status)
            {
                state.Assistant.Apply(status);
            }
            UpdateAssistantGroup();
        };
        registration.ToastRequested += (_, text) => toasts.Show(text);
        registration.DescriptionChanged += (_, text) => McpDescription.Text = text
            ?? L10n.T("Lets AI assistants read your mail and prepare drafts through the Model Context Protocol.");
        SetSwitch(registration.IsRegistered);
        // The application's last status, at once: the switch does not show
        // "off" only because this page has not asked yet.
        if (state.Assistant.Status is { } known)
        {
            registration.Adopt(known);
        }

        // Claude Desktop around a change (the application's controller).
        var restart = Assistant.RestartTexts();
        PendingRow.Header = Assistant.TargetName(AssistantTarget.Desktop);
        PendingRow.Description = restart.Pending;
        RestartButton.Content = restart.RestartNow;
        state.ClaudeDesktop.Changed += OnClaudeDesktopChanged;
        state.ClaudeDesktop.ToastRequested += OnClaudeDesktopToast;

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
        ClaudeCodeRow.Header = Assistant.TargetName(AssistantTarget.Code);
        ClaudeChooseButton.Content = panel.Choose;
        AssistantModelRow.Header = panel.Model;
        AutomationProperties.SetName(AssistantModelBox, panel.Model);
        foreach (var m in Assistant.Models)
        {
            AssistantModelBox.Items.Add(new ComboBoxItem { Content = Assistant.ModelName(m) });
        }
        bindings.Choice(AssistantTargetBox, SettingsKey.AssistantTarget, Assistant.Targets, () => state.Settings.AssistantTarget, v => state.Settings.AssistantTarget = v);
        bindings.Choice(AssistantModelBox, SettingsKey.AssistantModel, Assistant.Models, () => state.Settings.AssistantModel, v => state.Settings.AssistantModel = v);
        state.Assistant.Changed += OnAssistantChanged;
        UpdateRegisterRow();
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
        UpdateAssistantGroup();
    }

    /// <summary>The window closed: a status run ends, an install or uninstall runs to its end unseen.</summary>
    public void Close()
    {
        closed = true;
        registration.Close();
        // The controllers are the application's: they go on without the page.
        state.ClaudeDesktop.Changed -= OnClaudeDesktopChanged;
        state.ClaudeDesktop.ToastRequested -= OnClaudeDesktopToast;
        state.Assistant.Changed -= OnAssistantChanged;
    }

    // The MCP switch

    private void SetSwitch(bool on)
    {
        if (asking)
        {
            return;
        }
        syncing = true;
        RegisterSwitch.IsOn = on;
        syncing = false;
    }

    // The row waits while the state is not known, a change runs or Claude
    // Desktop's restart waits.
    private void UpdateRegisterRow()
    {
        RegisterRow.IsEnabled = registration.IsEnabled && !state.ClaudeDesktop.IsBusy && !asking;
        RestartButton.IsEnabled = !state.ClaudeDesktop.IsBusy && !asking;
    }

    private async void OnRegisterToggled(object sender, RoutedEventArgs e)
    {
        if (syncing || asking)
        {
            return;
        }
        var want = RegisterSwitch.IsOn;
        asking = true;
        UpdateRegisterRow();
        try
        {
            await state.ClaudeDesktop.ChangeAsync(want, registration.Status, AskRestartAsync, registration.ChangeAsync);
        }
        finally
        {
            asking = false;
        }
        if (closed)
        {
            return;
        }
        SetSwitch(registration.IsRegistered);
        UpdateRegisterRow();
        UpdateAssistantGroup();
    }

    // "Restart Claude Desktop?": Restart Claude Desktop (the default) or Later.
    private async System.Threading.Tasks.Task<ClaudeDesktopController.Answer> AskRestartAsync()
    {
        var t = Assistant.RestartTexts();
        var restart = await state.Alerts.ConfirmAsync(window(), t.Heading, t.Body, t.Restart, t.Later);
        return restart ? ClaudeDesktopController.Answer.Restart : ClaudeDesktopController.Answer.Later;
    }

    // The pending row's Restart.
    private async void OnRestartClick(object sender, RoutedEventArgs e)
    {
        if (state.ClaudeDesktop.IsBusy || asking)
        {
            return;
        }
        await state.ClaudeDesktop.RestartPendingAsync(registration.ChangeAsync);
    }

    private void OnClaudeDesktopChanged(object? sender, EventArgs e)
    {
        PendingRow.Visibility = state.ClaudeDesktop.Pending is null ? Visibility.Collapsed : Visibility.Visible;
        UpdateRegisterRow();
    }

    private void OnClaudeDesktopToast(object? sender, string text) => toasts.Show(text);

    // A status the application learned (at launch, when a window became
    // active, when a menu opened, after Claude Desktop's restart) is shown by
    // the switch too, so both follow one state; not while the question is up
    // or a change runs.
    private void OnAssistantChanged(object? sender, EventArgs e)
    {
        if (!asking && !state.ClaudeDesktop.IsBusy && state.Assistant.Status is { } known)
        {
            registration.Adopt(known);
        }
        UpdateAssistantGroup();
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
        var registered = BridgeRegistered;
        var unknown = !registered && !BridgeKnown;
        AssistantMenuRow.IsEnabled = registered;
        AssistantTargetRow.IsEnabled = registered;
        var on = unknown ? settings.AssistantMenu : Assistant.Shown(settings.AssistantMenu, registered);
        syncing = true;
        AssistantMenuSwitch.IsOn = on;
        syncing = false;
        Describe(AssistantMenuRow, registered || unknown ? "" : Assistant.Texts().RegisterFirst);
        // In App: the Claude Code row says what is wrong with it.
        var app = settings.AssistantTarget == AssistantTarget.App;
        var problem = registered && !app ? state.Assistant.Problem(settings.AssistantTarget) : "";
        Describe(AssistantTargetRow, problem);
        ClaudeCodeRow.Visibility = app ? Visibility.Visible : Visibility.Collapsed;
        AssistantModelRow.Visibility = app ? Visibility.Visible : Visibility.Collapsed;
        ClaudeCodeRow.IsEnabled = registered;
        AssistantModelRow.IsEnabled = registered;
        if (app)
        {
            ShowClaudeCode();
        }
    }

    // The switch writes assistant-menu while the bridge is registered.
    private void OnAssistantMenuToggled(object sender, RoutedEventArgs e)
    {
        if (syncing)
        {
            return;
        }
        if (!BridgeRegistered)
        {
            UpdateAssistantGroup();
            return;
        }
        state.Settings.AssistantMenu = AssistantMenuSwitch.IsOn;
    }

    // Claude Code (the In App target)

    // The row's text: the claude.exe the panel runs, its version and whether
    // it is signed in (asked once, then kept by the locator until the page
    // comes up again or the path changes), or that none was found.
    private async void ShowClaudeCode()
    {
        var my = ++claudeCodeGen;
        var locator = state.ClaudeCode;
        if (locator.Locate() is not { } path)
        {
            ClaudeCodeState.Text = Assistant.Problem(AssistantTarget.App, new AssistantAvailability());
            return;
        }
        if (!ClaudeCodeState.Text.StartsWith(path, StringComparison.Ordinal))
        {
            ClaudeCodeState.Text = path;
        }
        var version = await locator.VersionAsync();
        var signedIn = await locator.SignedInAsync();
        if (closed || my != claudeCodeGen)
        {
            return;
        }
        ClaudeCodeState.Text = ClaudeCodeStateText(path, version, signedIn);
    }

    /// <summary>"path · version · Signed in"; what is not known is left out (preferences.go claudeCodeState).</summary>
    internal static string ClaudeCodeStateText(string path, string? version, bool? signedIn)
    {
        var t = Assistant.PanelTexts();
        var parts = new System.Collections.Generic.List<string> { path };
        if (version is { Length: > 0 })
        {
            parts.Add(version);
        }
        if (signedIn is { } s)
        {
            parts.Add(s ? t.SignedIn : t.NotSignedInShort);
        }
        return string.Join(" · ", parts);
    }

    // Choose…: the claude.exe the panel should run. The one found
    // automatically stores nothing, so choosing it goes back to looking in
    // the usual places.
    private async void OnChooseClaudeClick(object sender, RoutedEventArgs e)
    {
        if (window() is not { } w)
        {
            return;
        }
        var chosen = await ComposeFileDialog.OpenAsync(
            WindowPresenter.Handle(w), Assistant.TargetName(AssistantTarget.Code), multiple: false, (ClaudeFileName, "*.exe"));
        if (closed || chosen is not { Count: > 0 } || chosen[0] is not { } path || !ClaudeCodeLocator.IsExecutableFile(path))
        {
            return;
        }
        var value = string.Equals(path, state.ClaudeCode.AutomaticPath(), StringComparison.OrdinalIgnoreCase) ? "" : path;
        state.ClaudeCode.Refresh();
        if (state.Settings.AssistantClaudePath == value)
        {
            state.Assistant.RefreshHandlers();
            UpdateAssistantGroup();
            return;
        }
        // The change handlers look again (AssistantController follows the key).
        state.Settings.AssistantClaudePath = value;
    }

    // The name of the dialog's filter: the file the panel runs (a name, not
    // translated).
    private const string ClaudeFileName = "claude.exe";

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
