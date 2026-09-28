// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/GeneralPaneViewController.swift
// (bindIfReady, refreshLaunchAtLogin, launchAtLoginChanged, bindMail,
// renderMail, mailChanged); GTK: ui/internal/window/preferences.go
// (NewPreferences' bindings, bindLaunchAtLogin, bindMail). The UI-only
// rows are bound two-way to the settings; the Mail group is the daemon's,
// through MailPreferencesController (config.get, config.set), and stays
// insensitive until the daemon answered.
//
// Launch at Login is the Run value (PlatformServices.LaunchAtLogin), which
// is authoritative as the Background portal and SMAppService are: the
// switch and the mirror key follow its status whenever the page comes up,
// and a change the registry or the user's choice in Windows Settings
// refuses flips the switch back with GTK's "Autostart was not granted".
// Where the user turned the entry off in Windows Settings, the row says so
// and links to Settings → Apps → Startup, the only place it can be turned
// on again; the app never overwrites that choice (docs/windows-port.md
// §10). The call is a registry write, done at once, so the row does not
// wait as GTK's does for the portal. The Keyboard group is the ctrl-r
// setting (macOS's ⌘R group) and Default Mail App opens Settings → Apps →
// Default apps (Windows-only groups, windows/README.md).

using System;
using System.IO;
using System.Security;
using Malachi.App.Platform;
using Malachi.App.Shell;
using Malachi.Core.Controllers;
using Malachi.Core.Settings;
using Malachi.Platform.Windows.Startup;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using DaemonPreferences = Malachi.Core.Api.Preferences;

namespace Malachi.App.Preferences;

/// <summary>The General page of the preferences.</summary>
[System.Diagnostics.CodeAnalysis.SuppressMessage("Design", "CA1001", Justification = "The window closes the controller (Close).")]
public sealed partial class GeneralPage : UserControl
{
    private readonly AppState state;
    private readonly IToasts toasts;
    private readonly ILogger logger;
    private readonly MailPreferencesController mail;

    // The login switch is being set from the Run value, not by the user.
    private bool revertingLogin;

    // The Mail group's combos are being set from the controller.
    private bool syncingMail;

    /// <summary>The page of a preferences window over <paramref name="state"/>.</summary>
    internal GeneralPage(AppState state, SettingBindings bindings, IToasts toasts)
    {
        this.state = state;
        this.toasts = toasts;
        logger = state.Logs.CreateLogger<GeneralPage>();
        InitializeComponent();

        var s = state.Settings;
        bindings.Toggle(RunInBackgroundSwitch, SettingsKey.RunInBackground, () => s.RunInBackground, v => s.RunInBackground = v);
        bindings.Number(MarkReadDelayBox, SettingsKey.MarkReadDelay, () => s.MarkReadDelay, v => s.MarkReadDelay = v);
        bindings.Toggle(ConfirmDeleteSwitch, SettingsKey.ConfirmDelete, () => s.ConfirmDelete, v => s.ConfirmDelete = v);
        bindings.Toggle(DesktopNotificationsSwitch, SettingsKey.DesktopNotifications, () => s.DesktopNotifications, v => s.DesktopNotifications = v);
        bindings.Toggle(NotificationSoundSwitch, SettingsKey.NotificationSound, () => s.NotificationSound, v => s.NotificationSound = v);
        bindings.Choice(CtrlRBox, SettingsKey.CtrlR, [CtrlR.Reply, CtrlR.Refresh], () => s.CtrlR, v => s.CtrlR = v);

        // GTK builds every page at once: the Mail group asks the daemon now.
        mail = new MailPreferencesController(state.Client, state.Logs.CreateLogger<MailPreferencesController>());
        mail.EnabledChanged += (_, on) => SetMailEnabled(on);
        mail.PreferencesChanged += (_, p) => RenderMail(p);
        mail.DescriptionChanged += (_, text) => MailDescription.Text = text;
        mail.ToastRequested += (_, text) => toasts.Show(text);
        SetMailEnabled(false);
        mail.Load();
        Refresh();
    }

    /// <summary>
    /// Reads again what Windows decides: the Run value and the user's
    /// switch in Settings, and the default mail app (the page came up, or
    /// the window was activated after a visit to Settings).
    /// </summary>
    public void Refresh()
    {
        RefreshLaunchAtLogin();
        RefreshDefaultApp();
    }

    /// <summary>The window closed: late replies of the daemon are dropped.</summary>
    public void Close() => mail.Close();

    // GeneralPaneViewController.refreshLaunchAtLogin: the Run value is
    // authoritative; the switch and the mirror key follow its status.
    private void RefreshLaunchAtLogin()
    {
        var status = LaunchAtLoginStatus.NotRegistered;
        try
        {
            status = PlatformServices.LaunchAtLogin.Status;
            state.Settings.LaunchAtLogin = status == LaunchAtLoginStatus.Enabled;
        }
        catch (Exception e) when (e is UnauthorizedAccessException or IOException or SecurityException)
        {
            LogRegistry(logger, "launch at login", e.GetType().Name);
        }
        SetLoginSwitch(status == LaunchAtLoginStatus.Enabled);
        ShowApproval(status);
    }

    private void SetLoginSwitch(bool on)
    {
        revertingLogin = true;
        LaunchAtLoginSwitch.IsOn = on;
        revertingLogin = false;
    }

    // Turned off in Windows Settings: said under the row, with the link to
    // the page where it can be turned on again.
    private void ShowApproval(LaunchAtLoginStatus status)
    {
        var text = LaunchAtLogin.StatusText(status);
        StartupApprovalText.Text = text ?? "";
        StartupApproval.Visibility = text is null ? Visibility.Collapsed : Visibility.Visible;
    }

    // preferences.go bindLaunchAtLogin, GeneralPaneViewController
    // launchAtLoginChanged: the switch, and the mirror key, stay flipped
    // only when Windows reports the change.
    private void OnLaunchAtLoginToggled(object sender, RoutedEventArgs e)
    {
        if (revertingLogin)
        {
            return;
        }
        var want = LaunchAtLoginSwitch.IsOn;
        try
        {
            var outcome = PlatformServices.LaunchAtLogin.Set(want);
            if (LaunchAtLogin.RefusalText(outcome) is { } refused)
            {
                SetLoginSwitch(!want);
                toasts.Show(refused);
            }
            else
            {
                state.Settings.LaunchAtLogin = want;
            }
            LogLaunchAtLogin(logger, want, outcome);
        }
        catch (Exception error) when (error is UnauthorizedAccessException or IOException or SecurityException)
        {
            SetLoginSwitch(!want);
            toasts.Show(LaunchAtLogin.FailureText(error));
            LogRegistry(logger, "launch at login", error.GetType().Name);
        }
        var status = LaunchAtLoginStatus.NotRegistered;
        try
        {
            status = PlatformServices.LaunchAtLogin.Status;
        }
        catch (Exception error) when (error is UnauthorizedAccessException or IOException or SecurityException)
        {
            LogRegistry(logger, "launch at login", error.GetType().Name);
        }
        ShowApproval(status);
    }

    private void OnStartupSettingsClick(object sender, RoutedEventArgs e)
    {
        if (!PlatformServices.LaunchAtLogin.OpenSystemSettings())
        {
            // Windows-only string: Settings could not be opened.
            toasts.Show("Windows Settings could not be opened");
        }
    }

    // What Windows opens mailto: links with (Windows-only strings).
    private void RefreshDefaultApp()
    {
        string text;
        try
        {
            var mailto = PlatformServices.Mailto;
            text = mailto.IsDefault
                // Windows-only string
                ? "Malachi Mail is the default app for e-mail links"
                : mailto.Exists
                    // Windows-only string
                    ? "Another app opens e-mail links; choose Malachi Mail in Default apps"
                    // Windows-only string
                    : "Malachi Mail is not registered with Windows as a mail app yet";
        }
        catch (Exception e) when (e is UnauthorizedAccessException or IOException or SecurityException)
        {
            LogRegistry(logger, "mailto: registration", e.GetType().Name);
            text = "";
        }
        DefaultAppsRow.Description = text;
    }

    private void OnDefaultAppsClick(object sender, RoutedEventArgs e)
    {
        if (!PlatformServices.OpenDefaultApps())
        {
            // Windows-only string: Settings could not be opened.
            toasts.Show("Windows Settings could not be opened");
        }
    }

    // The Mail group's sensitivity: loaded, and no save in flight.
    private void SetMailEnabled(bool on)
    {
        CheckIntervalRow.IsEnabled = on;
        RemoteImagesRow.IsEnabled = on;
        OfflineDaysRow.IsEnabled = on;
    }

    private void RenderMail(DaemonPreferences? p)
    {
        if (p is null)
        {
            return;
        }
        var selection = new MailPreferencesController.MailSelection(p);
        syncingMail = true;
        CheckIntervalBox.SelectedIndex = selection.Interval;
        RemoteImagesBox.SelectedIndex = selection.RemoteContent;
        OfflineDaysBox.SelectedIndex = selection.Retention;
        syncingMail = false;
    }

    private void OnMailChanged(object sender, SelectionChangedEventArgs e)
    {
        if (syncingMail || mail is null)
        {
            return;
        }
        if (ReferenceEquals(sender, CheckIntervalBox))
        {
            mail.SelectInterval(CheckIntervalBox.SelectedIndex);
        }
        else if (ReferenceEquals(sender, RemoteImagesBox))
        {
            mail.SelectRemoteContent(RemoteImagesBox.SelectedIndex);
        }
        else if (ReferenceEquals(sender, OfflineDaysBox))
        {
            mail.SelectRetention(OfflineDaysBox.SelectedIndex);
        }
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "launch at login {Want}: {Outcome}")]
    private static partial void LogLaunchAtLogin(ILogger logger, bool want, LaunchAtLoginOutcome outcome);

    [LoggerMessage(Level = LogLevel.Warning, Message = "preferences: {What}: {Failure}")]
    private static partial void LogRegistry(ILogger logger, string what, string failure);
}
