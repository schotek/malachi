// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/GeneralPaneViewController.swift
// (bindIfReady, refreshLaunchAtLogin, launchAtLoginChanged, bindMail,
// renderMail, mailChanged, bindStorage); GTK:
// ui/internal/window/preferences.go (NewPreferences' bindings,
// bindLaunchAtLogin, bindMail, bindStorage) and preferences_board.go
// (bindBoard; GeneralPage.Board.cs). The UI-only rows are bound
// two-way to the settings; the Mail group is the daemon's, through
// MailPreferencesController (config.get, config.set), and stays
// insensitive until the daemon answered. Keep Attachments Offline For,
// Never Store Attachments and Compress Stored Mail show only when the
// daemon reports them, the first insensitive while the daemon confirms
// that no attachment is stored; Disk Space Used shows what system.storage
// says (StorageUsageController: now, after every confirmed change and
// every 5 s while the window is open), hidden for a daemon without it.
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
using CommunityToolkit.WinUI.Controls;
using Malachi.App.Platform;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Model;
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
    private readonly StorageUsageController storage;

    // The login switch is being set from the Run value, not by the user.
    private bool revertingLogin;

    // The Mail group's rows are being set from the controller.
    private bool syncingMail;

    // The Mail group's sensitivity, and whether the confirmed preferences
    // let Keep Attachments Offline For apply (not while no attachment is
    // stored): that row is sensitive only with both.
    private bool mailEnabled;
    private bool attachmentDaysApply = true;

    /// <summary>The page of a preferences window over <paramref name="state"/>.</summary>
    internal GeneralPage(AppState state, SettingBindings bindings, IToasts toasts)
    {
        this.state = state;
        this.toasts = toasts;
        logger = state.Logs.CreateLogger<GeneralPage>();
        InitializeComponent();

        var s = state.Settings;
        bindings.Toggle(RunInBackgroundSwitch, SettingsKey.RunInBackground, () => s.RunInBackground, v => s.RunInBackground = v);
        // preferences_board.go bindBoard: Board View, Open at Launch, Show
        // the Board and the windows of the states.
        InitializeBoardGroup(bindings);
        bindings.Number(MarkReadDelayBox, SettingsKey.MarkReadDelay, () => s.MarkReadDelay, v => s.MarkReadDelay = v);
        bindings.Toggle(ConfirmDeleteSwitch, SettingsKey.ConfirmDelete, () => s.ConfirmDelete, v => s.ConfirmDelete = v);
        bindings.Toggle(DesktopNotificationsSwitch, SettingsKey.DesktopNotifications, () => s.DesktopNotifications, v => s.DesktopNotifications = v);
        bindings.Toggle(NotificationSoundSwitch, SettingsKey.NotificationSound, () => s.NotificationSound, v => s.NotificationSound = v);
        bindings.Choice(CtrlRBox, SettingsKey.CtrlR, [CtrlR.Reply, CtrlR.Refresh], () => s.CtrlR, v => s.CtrlR = v);

        // GTK builds every page at once: the Mail group asks the daemon now,
        // the disk space first (preferences.go binds it before the Mail group).
        storage = new StorageUsageController(state.Client, logger: state.Logs.CreateLogger<StorageUsageController>());
        storage.UsageChanged += (_, usage) => RenderStorage(usage);
        // The value stays what it was; the next answer puts the details back.
        storage.Failed += (_, text) => StorageRow.Description = text;
        storage.Unsupported += (_, _) => StorageRow.Visibility = Visibility.Collapsed;
        mail = new MailPreferencesController(state.Client, state.Logs.CreateLogger<MailPreferencesController>());
        mail.EnabledChanged += (_, on) => SetMailEnabled(on);
        mail.PreferencesChanged += (_, p) => RenderMail(p);
        mail.DescriptionChanged += (_, text) => MailDescription.Text = text;
        mail.ToastRequested += (_, text) => toasts.Show(text);
        // Compression or the attachments changed what the store holds, or
        // is about to in the background (preferences.go refreshStorage).
        mail.Saved += (_, _) => storage.Refresh();
        SetMailEnabled(false);
        storage.Start();
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

    /// <summary>The window closed: late replies of the daemon are dropped, the disk space is not asked for any more.</summary>
    public void Close()
    {
        CloseBoardGroup();
        mail.Close();
        storage.Close();
    }

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

    // The Mail group's sensitivity: loaded, and no save in flight (GTK's
    // mail_group, whose rows include Disk Space Used).
    private void SetMailEnabled(bool on)
    {
        mailEnabled = on;
        CheckIntervalRow.IsEnabled = on;
        RemoteImagesRow.IsEnabled = on;
        OfflineDaysRow.IsEnabled = on;
        AttachmentDaysRow.IsEnabled = on && attachmentDaysApply;
        NeverStoreRow.IsEnabled = on;
        CompressStoreRow.IsEnabled = on;
        StorageRow.IsEnabled = on;
    }

    // preferences.go apply: the values the daemon confirmed, into the rows;
    // a row whose field the daemon does not report is hidden. Keep
    // Attachments Offline For is insensitive while the confirmed set stores
    // no attachment, so a failed save reverts that too; it keeps its value.
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
        if (selection.Attachments is { } attachments)
        {
            AttachmentDaysBox.SelectedIndex = attachments;
        }
        if (selection.NeverStore is { } neverStore)
        {
            NeverStoreSwitch.IsOn = neverStore;
        }
        if (selection.Compress is { } compress)
        {
            CompressStoreSwitch.IsOn = compress;
        }
        syncingMail = false;
        AttachmentDaysRow.Visibility = selection.Attachments is null ? Visibility.Collapsed : Visibility.Visible;
        NeverStoreRow.Visibility = selection.NeverStore is null ? Visibility.Collapsed : Visibility.Visible;
        CompressStoreRow.Visibility = selection.Compress is null ? Visibility.Collapsed : Visibility.Visible;
        attachmentDaysApply = PreferenceChoices.AttachmentDaysApply(p);
        AttachmentDaysRow.IsEnabled = mailEnabled && attachmentDaysApply;
    }

    // preferences.go bindStorage: the total as the value, what compression
    // saves and what stays on the server as the description, none when
    // there is nothing to say.
    private void RenderStorage(SystemStorageResult usage)
    {
        var (value, details) = StorageUsage.StorageTexts(usage);
        StorageValue.Text = value;
        if (details.Length == 0)
        {
            // No description line at all, as the card has before the first answer.
            StorageRow.ClearValue(SettingsCard.DescriptionProperty);
        }
        else
        {
            StorageRow.Description = details;
        }
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
        else if (ReferenceEquals(sender, AttachmentDaysBox))
        {
            mail.SelectAttachmentDays(AttachmentDaysBox.SelectedIndex);
        }
    }

    private void OnMailToggled(object sender, RoutedEventArgs e)
    {
        if (syncingMail || mail is null)
        {
            return;
        }
        if (ReferenceEquals(sender, NeverStoreSwitch))
        {
            mail.SetNeverStoreAttachments(NeverStoreSwitch.IsOn);
        }
        else if (ReferenceEquals(sender, CompressStoreSwitch))
        {
            mail.SetCompressStore(CompressStoreSwitch.IsOn);
        }
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "launch at login {Want}: {Outcome}")]
    private static partial void LogLaunchAtLogin(ILogger logger, bool want, LaunchAtLoginOutcome outcome);

    [LoggerMessage(Level = LogLevel.Warning, Message = "preferences: {What}: {Failure}")]
    private static partial void LogRegistry(ILogger logger, string what, string failure);
}
