// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/AIPaneViewController.swift
// (bindIfReady, setSwitch, registerChanged, the status on every
// appearance); GTK: ui/internal/window/preferences.go (bindMCP). The
// switch shows what the bundled malachi-mcp.exe last confirmed, through
// McpRegistrationController (status, install, uninstall with --json and
// the Windows paths, docs/windows-port.md §10); the row is insensitive
// until the bridge answered and while a call runs. A missing bridge or a
// status the bridge did not give goes into the group's description, as in
// GTK (U2, U3), a later status that answers puts the page's own text back;
// a failed install or uninstall flips the switch back with a toast.

using Malachi.App.Shell;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Preferences;

/// <summary>The AI page of the preferences.</summary>
[System.Diagnostics.CodeAnalysis.SuppressMessage("Design", "CA1001", Justification = "The window closes the controller (Close).")]
public sealed partial class AiPage : UserControl
{
    private readonly McpRegistrationController registration;

    // The switch is being set from the controller, not by the user.
    private bool syncing;

    /// <summary>The page of a preferences window over <paramref name="state"/>.</summary>
    internal AiPage(AppState state, IToasts toasts)
    {
        InitializeComponent();
        registration = new McpRegistrationController(state.Paths.McpBridge, logger: state.Logs.CreateLogger<McpRegistrationController>());
        registration.EnabledChanged += (_, on) => RegisterRow.IsEnabled = on;
        registration.RegisteredChanged += (_, on) => SetSwitch(on);
        registration.ToastRequested += (_, text) => toasts.Show(text);
        registration.DescriptionChanged += (_, text) => McpDescription.Text = text
            ?? L10n.T("Lets AI assistants read your mail and prepare drafts through the Model Context Protocol.");
        RegisterRow.IsEnabled = registration.IsEnabled;
        SetSwitch(registration.IsRegistered);
    }

    /// <summary>The page came up: the bridge is asked for its status (every time, as on macOS).</summary>
    public void Refresh() => registration.Load();

    /// <summary>The window closed: a status run ends, an install or uninstall runs to its end unseen.</summary>
    public void Close() => registration.Close();

    private void SetSwitch(bool on)
    {
        syncing = true;
        RegisterSwitch.IsOn = on;
        syncing = false;
    }

    private void OnRegisterToggled(object sender, RoutedEventArgs e)
    {
        if (!syncing)
        {
            registration.SetRegistered(RegisterSwitch.IsOn);
        }
    }
}
