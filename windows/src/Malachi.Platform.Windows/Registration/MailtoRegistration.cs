// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: Malachi Mail as a mail app Windows offers for mailto:
// links (docs/windows-port.md §10), the counterpart of GTK's desktop file
// (MimeType=x-scheme-handler/mailto; Exec=malachi %u) and of the URL type of
// the macOS Info.plist. An unpackaged app registers itself per user, in
// HKCU, with the three parts measured in APP-SPIKES §7:
//   Software\Classes\io.github.schotek.Malachi.mailto   the ProgID: "URL
//       Protocol", its icon, shell\open\command "<exe>" "%1", and
//       Application\ApplicationName, without which Windows lists the exe's
//       name;
//   Software\Clients\Mail\Malachi Mail\Capabilities      the application's
//       capabilities: its name, description and icon, URLAssociations
//       mailto = the ProgID;
//   Software\RegisteredApplications "Malachi Mail"        points at them,
//       and names the app for ms-settings:defaultapps?registeredAppUser=.
// then SHChangeNotify(SHCNE_ASSOCCHANGED). Windows does not let an app make
// itself the default (the UserChoice hash); the user picks it in Settings →
// Default apps (SystemSettings.DefaultAppsUri). The registration is written
// at start when it is missing or stale (the app folder moved, a new
// version, another language for the description); Unregister is for an
// uninstaller. The registry root is injectable: the tests work under a key
// of their own and tell no shell.

using System;
using System.Collections.Generic;
using System.IO;
using Malachi.Core;
using Malachi.Core.I18n;
using Microsoft.Win32;

namespace Malachi.Platform.Windows.Registration;

/// <summary>The per-user registration of Malachi Mail as a <c>mailto:</c> handler.</summary>
/// <remarks>
/// <see cref="EnsureRegistered"/>, <see cref="Register"/> and
/// <see cref="Unregister"/> throw what the registry throws
/// (<see cref="UnauthorizedAccessException"/>, <see cref="IOException"/>,
/// <see cref="System.Security.SecurityException"/>).
/// </remarks>
public sealed class MailtoRegistration
{
    /// <summary>The ProgID of Malachi Mail's <c>mailto:</c> links.</summary>
    public const string ProgId = AppIdentity.AppId + ".mailto";

    /// <summary>The name Windows shows, and the value under RegisteredApplications.</summary>
    public const string ApplicationName = AppIdentity.DisplayName;

    /// <summary>The ProgID's key under the root.</summary>
    public const string ProgIdPath = @"Software\Classes\" + ProgId;

    /// <summary>The application's client key under the root.</summary>
    public const string ClientPath = @"Software\Clients\Mail\" + ApplicationName;

    /// <summary>The capabilities under the root, as RegisteredApplications names them.</summary>
    public const string CapabilitiesPath = ClientPath + @"\Capabilities";

    /// <summary>The list of registered applications under the root.</summary>
    public const string RegisteredApplicationsPath = @"Software\RegisteredApplications";

    /// <summary>The protocol.</summary>
    public const string Protocol = "mailto";

    private readonly RegistryKey root;
    private readonly Action notifyShell;
    private readonly Func<string, string?> defaultProgId;

    /// <summary>The registration of <paramref name="executablePath"/> for the current user.</summary>
    public MailtoRegistration(string executablePath)
        : this(Registry.CurrentUser, executablePath, ShellAssociations.NotifyChanged, ShellAssociations.DefaultProgId)
    {
    }

    /// <summary>
    /// The registration under <paramref name="root"/> (HKEY_CURRENT_USER, or
    /// a test key). <paramref name="notifyShell"/> runs after every change
    /// (nothing when null); <paramref name="defaultProgId"/> answers which
    /// ProgID handles a protocol by default (none when null).
    /// </summary>
    public MailtoRegistration(RegistryKey root, string executablePath, Action? notifyShell = null, Func<string, string?>? defaultProgId = null)
    {
        ArgumentNullException.ThrowIfNull(root);
        ArgumentException.ThrowIfNullOrEmpty(executablePath);
        this.root = root;
        ExecutablePath = Path.GetFullPath(executablePath);
        this.notifyShell = notifyShell ?? (() => { });
        this.defaultProgId = defaultProgId ?? (_ => null);
    }

    /// <summary>The executable the links open.</summary>
    public string ExecutablePath { get; }

    /// <summary>What <c>mailto:</c> runs: the quoted executable and the quoted link.</summary>
    public string OpenCommand => Quote(ExecutablePath) + " \"%1\"";

    /// <summary>
    /// The description Windows shows under the name: the desktop file's
    /// comment, in the current language.
    /// </summary>
    public static string Description => L10n.T("Read and send email");

    /// <summary>
    /// Every value of the registration: the key under the root, the value's
    /// name ("" for the default value) and its data.
    /// </summary>
    public IReadOnlyList<(string Key, string Name, string Value)> Values
    {
        get
        {
            var icon = Quote(ExecutablePath) + ",0";
            var description = Description;
            return
            [
                (ProgIdPath, "", ApplicationName),
                (ProgIdPath, "URL Protocol", ""),
                (ProgIdPath + @"\DefaultIcon", "", icon),
                (ProgIdPath + @"\shell\open\command", "", OpenCommand),
                (ProgIdPath + @"\Application", "ApplicationName", ApplicationName),
                (ProgIdPath + @"\Application", "ApplicationDescription", description),
                (ProgIdPath + @"\Application", "ApplicationIcon", icon),
                (ClientPath, "", ApplicationName),
                (ClientPath + @"\DefaultIcon", "", icon),
                (ClientPath + @"\shell\open\command", "", Quote(ExecutablePath)),
                (CapabilitiesPath, "ApplicationName", ApplicationName),
                (CapabilitiesPath, "ApplicationDescription", description),
                (CapabilitiesPath, "ApplicationIcon", icon),
                (CapabilitiesPath + @"\URLAssociations", Protocol, ProgId),
                (CapabilitiesPath + @"\StartMenu", "Mail", ApplicationName),
                (RegisteredApplicationsPath, ApplicationName, CapabilitiesPath),
            ];
        }
    }

    /// <summary>Whether every value of the registration is there with this executable's data.</summary>
    public bool IsCurrent
    {
        get
        {
            foreach (var (key, name, value) in Values)
            {
                using var k = root.OpenSubKey(key);
                // GetValueKind throws for a missing value: the value first.
                if (k?.GetValue(name, null, RegistryValueOptions.DoNotExpandEnvironmentNames) is not string s ||
                    k.GetValueKind(name) != RegistryValueKind.String || !string.Equals(s, value, StringComparison.Ordinal))
                {
                    return false;
                }
            }
            return true;
        }
    }

    /// <summary>Whether any part of the registration is there, current or not.</summary>
    public bool Exists
    {
        get
        {
            using var progId = root.OpenSubKey(ProgIdPath);
            using var client = root.OpenSubKey(ClientPath);
            using var registered = root.OpenSubKey(RegisteredApplicationsPath);
            return progId is not null || client is not null || registered?.GetValue(ApplicationName) is not null;
        }
    }

    /// <summary>
    /// Whether the user chose Malachi Mail for <c>mailto:</c> links in
    /// Settings → Default apps.
    /// </summary>
    public bool IsDefault => string.Equals(defaultProgId(Protocol), ProgId, StringComparison.OrdinalIgnoreCase);

    /// <summary>
    /// Writes the registration when it is missing or stale; true when it
    /// was written. The start of the app calls it.
    /// </summary>
    public bool EnsureRegistered()
    {
        if (IsCurrent)
        {
            return false;
        }
        Register();
        return true;
    }

    /// <summary>
    /// Writes the registration afresh (what an older version or another
    /// copy wrote under its keys goes first) and tells the shell.
    /// </summary>
    public void Register()
    {
        DeleteKeys();
        foreach (var (key, name, value) in Values)
        {
            using var k = root.CreateSubKey(key, writable: true);
            k.SetValue(name, value, RegistryValueKind.String);
        }
        notifyShell();
    }

    /// <summary>
    /// Removes the registration, for an uninstaller; true when there was
    /// one. Windows forgets the handler, and a default that pointed at it
    /// falls back to Windows' choice.
    /// </summary>
    public bool Unregister()
    {
        if (!Exists)
        {
            return false;
        }
        DeleteKeys();
        using (var registered = root.OpenSubKey(RegisteredApplicationsPath, writable: true))
        {
            registered?.DeleteValue(ApplicationName, throwOnMissingValue: false);
        }
        notifyShell();
        return true;
    }

    private void DeleteKeys()
    {
        root.DeleteSubKeyTree(ProgIdPath, throwOnMissingSubKey: false);
        root.DeleteSubKeyTree(ClientPath, throwOnMissingSubKey: false);
    }

    private static string Quote(string path) => "\"" + path + "\"";
}
