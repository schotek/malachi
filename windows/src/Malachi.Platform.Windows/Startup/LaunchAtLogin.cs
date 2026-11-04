// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/LoginItemService.swift (status,
// isEnabled, set, openSystemSettings) and of the Background portal request
// of ui/internal/background (RequestAutostart), for an unpackaged app:
// the value "Malachi Mail" = "\"<exe>\" --background" under
// HKCU\Software\Microsoft\Windows\CurrentVersion\Run starts the app hidden
// at sign-in (GTK's --gapplication-service, macOS's login item launch), and
// the user's own switch in Windows Settings (StartupApproved\Run) decides
// whether Windows runs it. As on macOS the service is authoritative: the
// launch-at-login key only mirrors Status (MirrorInto, read when Preferences
// opens), and a switch the user turned off in Windows Settings is shown as
// such and never overwritten (docs/windows-port.md §8, §10). The registry
// root is injectable: the tests work under a key of their own.

using System;
using System.IO;
using Malachi.Core;
using Malachi.Core.I18n;
using Malachi.Core.Settings;
using Malachi.Platform.Windows.Registration;
using Microsoft.Win32;

namespace Malachi.Platform.Windows.Startup;

/// <summary>Launch at login through the current user's Run key.</summary>
/// <remarks>
/// Every member reads the registry afresh. <see cref="Set"/> and
/// <see cref="RepairMovedExecutable"/> throw what the registry throws
/// (<see cref="UnauthorizedAccessException"/>, <see cref="IOException"/>,
/// <see cref="System.Security.SecurityException"/>); the caller shows it,
/// as macOS shows what <c>SMAppService</c> throws.
/// </remarks>
public sealed class LaunchAtLogin
{
    /// <summary>The Run key under HKEY_CURRENT_USER.</summary>
    public const string RunKeyPath = @"Software\Microsoft\Windows\CurrentVersion\Run";

    /// <summary>Where Windows Settings and Task Manager keep the user's switch, under HKEY_CURRENT_USER.</summary>
    public const string ApprovedKeyPath = @"Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run";

    /// <summary>
    /// The name of the Run value and of the user's switch under
    /// StartupApproved\Run. Settings → Apps → Startup and Task Manager's
    /// Startup tab do not show it: they show the executable's
    /// FileDescription (the app's assembly title).
    /// </summary>
    public const string ValueName = AppIdentity.DisplayName;

    /// <summary>The argument that starts the app hidden, in the background.</summary>
    public const string BackgroundArgument = "--background";

    private readonly RegistryKey root;
    private readonly string runPath;
    private readonly string approvedPath;
    private readonly Func<string, bool> openUri;

    /// <summary>Launch at login of <paramref name="executablePath"/> for the current user.</summary>
    public LaunchAtLogin(string executablePath)
        : this(Registry.CurrentUser, executablePath)
    {
    }

    /// <summary>
    /// Launch at login under <paramref name="root"/> (HKEY_CURRENT_USER, or
    /// a test key) with the Run and approval keys at the given paths below
    /// it; <paramref name="openUri"/> opens a Settings page
    /// (<see cref="SystemSettings.Open"/> when null).
    /// </summary>
    public LaunchAtLogin(
        RegistryKey root, string executablePath, string runPath = RunKeyPath, string approvedPath = ApprovedKeyPath, Func<string, bool>? openUri = null)
    {
        ArgumentNullException.ThrowIfNull(root);
        ArgumentException.ThrowIfNullOrEmpty(executablePath);
        ArgumentException.ThrowIfNullOrEmpty(runPath);
        ArgumentException.ThrowIfNullOrEmpty(approvedPath);
        this.root = root;
        ExecutablePath = Path.GetFullPath(executablePath);
        this.runPath = runPath;
        this.approvedPath = approvedPath;
        this.openUri = openUri ?? SystemSettings.Open;
    }

    /// <summary>The executable the Run value starts.</summary>
    public string ExecutablePath { get; }

    /// <summary>The Run value this app writes: the quoted executable and <see cref="BackgroundArgument"/>.</summary>
    public string Command => "\"" + ExecutablePath + "\" " + BackgroundArgument;

    /// <summary>The Run value as it is now; null when there is none.</summary>
    public string? RegisteredCommand
    {
        get
        {
            using var run = root.OpenSubKey(runPath);
            return run?.GetValue(ValueName) as string;
        }
    }

    /// <summary>What Windows will do at sign-in (Swift <c>status</c>).</summary>
    public LaunchAtLoginStatus Status
    {
        get
        {
            if (RegisteredCommand is null)
            {
                return LaunchAtLoginStatus.NotRegistered;
            }
            using var approved = root.OpenSubKey(approvedPath);
            var approval = StartupApproval.Decode(approved?.GetValue(ValueName));
            return approval is { Enabled: false } ? LaunchAtLoginStatus.DisabledByUser : LaunchAtLoginStatus.Enabled;
        }
    }

    /// <summary>Whether the app starts at sign-in (Swift <c>isEnabled</c>).</summary>
    public bool IsEnabled => Status == LaunchAtLoginStatus.Enabled;

    /// <summary>
    /// Writes or removes the Run value and reports what Windows says
    /// afterwards (Swift <c>set</c>). The user's switch in Windows Settings
    /// is never touched: turning on an entry the user turned off there
    /// writes the value and answers <see cref="LaunchAtLoginOutcome.RequiresApproval"/>.
    /// </summary>
    public LaunchAtLoginOutcome Set(bool on)
    {
        if (on)
        {
            using var run = root.CreateSubKey(runPath, writable: true);
            run.SetValue(ValueName, Command, RegistryValueKind.String);
        }
        else
        {
            using var run = root.OpenSubKey(runPath, writable: true);
            run?.DeleteValue(ValueName, throwOnMissingValue: false);
        }
        var now = Status;
        if (on && now == LaunchAtLoginStatus.DisabledByUser)
        {
            return LaunchAtLoginOutcome.RequiresApproval;
        }
        return (now == LaunchAtLoginStatus.Enabled) == on ? LaunchAtLoginOutcome.Granted : LaunchAtLoginOutcome.NotGranted;
    }

    /// <summary>
    /// Points the Run value at this executable when it names a copy that no
    /// longer exists (the app folder moved) or this copy with another
    /// command line; another existing copy's entry is left alone. True when
    /// the value was rewritten. The user's switch is kept: it belongs to the
    /// value's name.
    /// </summary>
    public bool RepairMovedExecutable()
    {
        var registered = RegisteredCommand;
        if (registered is null || string.Equals(registered, Command, StringComparison.Ordinal))
        {
            return false;
        }
        var exe = ExecutableOf(registered);
        var ours = exe is not null && string.Equals(Normalize(exe), ExecutablePath, StringComparison.OrdinalIgnoreCase);
        if (!ours && exe is not null && File.Exists(exe))
        {
            return false;
        }
        using var run = root.CreateSubKey(runPath, writable: true);
        run.SetValue(ValueName, Command, RegistryValueKind.String);
        return true;
    }

    /// <summary>
    /// Sets the <c>launch-at-login</c> key to <see cref="IsEnabled"/>, which
    /// it only mirrors (Swift <c>refreshLaunchAtLogin</c>), and returns it.
    /// Call it on the UI thread.
    /// </summary>
    public bool MirrorInto(SettingsStore settings)
    {
        ArgumentNullException.ThrowIfNull(settings);
        var on = IsEnabled;
        settings.LaunchAtLogin = on;
        return on;
    }

    /// <summary>
    /// Opens Settings → Apps → Startup, where the user turns the entry on
    /// again after turning it off there (Swift <c>openSystemSettings</c>).
    /// False when Windows could not open it.
    /// </summary>
    public bool OpenSystemSettings() => openUri(SystemSettings.StartupAppsUri);

    /// <summary>
    /// What the Launch at Login row says under its title for
    /// <paramref name="status"/>: that the user turned it off in Windows
    /// Settings (where only they can turn it on again); null otherwise.
    /// </summary>
    public static string? StatusText(LaunchAtLoginStatus status) =>
        status == LaunchAtLoginStatus.DisabledByUser ? "Turned off in Windows Settings" : null; // Windows-only string

    /// <summary>
    /// The toast for an outcome that leaves the switch where it was
    /// (preferences.go <c>bindLaunchAtLogin</c>: "Autostart was not
    /// granted"); null when it was granted.
    /// </summary>
    public static string? RefusalText(LaunchAtLoginOutcome outcome) =>
        outcome == LaunchAtLoginOutcome.Granted ? null : L10n.T("Autostart was not granted");

    /// <summary>
    /// The toast for a request the registry refused, with its reason (the
    /// counterpart of macOS's own "Launch at Login could not be changed").
    /// </summary>
    public static string FailureText(Exception error)
    {
        ArgumentNullException.ThrowIfNull(error);
        return L10n.T("Launch at Login could not be changed: %s", error.Message); // Windows-only string
    }

    /// <summary>
    /// The program a Run command starts: the quoted first token, or the
    /// command up to its first space; null when there is none.
    /// </summary>
    public static string? ExecutableOf(string command)
    {
        ArgumentNullException.ThrowIfNull(command);
        var s = command.TrimStart();
        if (s.Length == 0)
        {
            return null;
        }
        if (s[0] == '"')
        {
            var end = s.IndexOf('"', 1);
            var quoted = end < 0 ? s[1..] : s[1..end];
            return quoted.Length == 0 ? null : quoted;
        }
        var space = s.IndexOf(' ', StringComparison.Ordinal);
        return space < 0 ? s : s[..space];
    }

    private static string Normalize(string path)
    {
        try
        {
            return Path.GetFullPath(path);
        }
        catch (Exception e) when (e is ArgumentException or NotSupportedException or PathTooLongException)
        {
            return path;
        }
    }
}
