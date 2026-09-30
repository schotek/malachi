// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Daemon/Paths.swift (Paths) and of the
// path check of Transport/UnixSocketProbe.swift (check); GTK:
// ui/internal/client (DefaultSocketPath), ui/internal/mcpsetup/mcpsetup.go
// (Locate: the bridge beside the executable). The socket keeps the daemon's
// own default (backend/internal/config ResolvePaths, api.SocketBase, the
// same rule as malachi-mcp's defaultSocketPath), so the app, malachi-mcp,
// .mcp.json and make run-backend agree without any variable; like Go's
// os.UserHomeDir, the home is %USERPROFILE%, never HOME (which Git Bash
// sets). Configuration, store and logs go to %LOCALAPPDATA%\Malachi Mail
// (docs/windows-port.md §1), or to MALACHI_DATA_DIR, a Windows-only
// variable, for tests and agents. The preferences' registry key is
// HKCU\Software\io.github.schotek.Malachi, or the one the other
// Windows-only variable, MALACHI_SETTINGS_KEY, names for a test (§8): only
// a name of the app's own family is taken, so that a mistyped value cannot
// point the app's writes at another program's key. The bundled programs
// are found beside the app's executable: malachi-mcp.exe, and
// malachi-credentials.exe, the keyring helper that stands in for macOS's
// malachi-keychain. The assistant's working directory (Swift's
// ClaudeCodeLocator.defaultDirectory, in the Caches directory there) is in
// the data directory with the rest. Directories are made private by
// IPrivateDirectoryFactory (a protected DACL) instead of mode 0700.

using System;
using System.Collections.Generic;
using System.IO;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.Platform;

namespace Malachi.Core.Daemon;

/// <summary>Where the daemon's socket, its data and the bundled programs are.</summary>
public sealed record Paths
{
    /// <summary>
    /// The longest AF_UNIX path Windows takes, in UTF-8 bytes: the 108-byte
    /// <c>sun_path</c> with its NUL, the daemon's own limit.
    /// </summary>
    public const int MaxSocketPathBytes = 107;

    /// <summary>The data directory's name under %LOCALAPPDATA%.</summary>
    public const string DataDirectoryName = AppIdentity.DisplayName;

    /// <summary>The MCP bridge beside the app.</summary>
    public const string McpBridgeName = "malachi-mcp.exe";

    /// <summary>The keyring helper beside the app (MALACHI_KEYRING=helper).</summary>
    public const string KeyringHelperName = "malachi-credentials.exe";

    /// <summary>The daemon's log in <see cref="LogDir"/>.</summary>
    public const string DaemonLogName = "malachid.log";

    /// <summary>The variable that names another registry key for the preferences.</summary>
    public const string SettingsKeyVariable = "MALACHI_SETTINGS_KEY";

    /// <summary>The preferences' key under HKEY_CURRENT_USER, the gschema's path on Windows.</summary>
    public const string DefaultSettingsKey = SettingsKeyParent + AppIdentity.AppId;

    private const string SettingsKeyParent = @"Software\";

    /// <summary>
    /// <c>MALACHI_SOCKET</c>, else <c>%XDG_RUNTIME_DIR%\malachi\rpc.sock</c>,
    /// else <c>(%XDG_CACHE_HOME% or %USERPROFILE%\.cache)\malachi\run\rpc.sock</c>:
    /// exactly the daemon's, the GTK client's and the MCP bridge's
    /// resolution. The daemon's connection key lies beside it
    /// (<see cref="KeyFile"/>) and is read on every connection.
    /// </summary>
    public required string Socket { get; init; }

    /// <summary>
    /// Whether <see cref="Socket"/> came from <c>MALACHI_SOCKET</c>: its
    /// directory is then the user's, and is created private only when it
    /// is missing, never changed (<see cref="EnsureSocketDirectory"/>).
    /// </summary>
    public bool SocketFromEnvironment { get; init; }

    /// <summary><c>MALACHI_DATA_DIR</c>, else <c>%LOCALAPPDATA%\Malachi Mail</c>.</summary>
    public required string DataDir { get; init; }

    /// <summary>
    /// The preferences' key under HKEY_CURRENT_USER:
    /// <c>Software\</c> and the name <c>MALACHI_SETTINGS_KEY</c> gives when
    /// that is the app id itself or the app id, a dot and at least one of
    /// the ASCII letters, digits, <c>.</c>, <c>_</c> and <c>-</c> (a test's
    /// key of its own, <c>io.github.schotek.Malachi.UiTests.&lt;guid&gt;</c>);
    /// else <see cref="DefaultSettingsKey"/>.
    /// </summary>
    public string SettingsKey { get; init; } = DefaultSettingsKey;

    /// <summary>
    /// A <c>MALACHI_SETTINGS_KEY</c> that was set but ignored for not
    /// naming a key of the app's family, for the log; null otherwise.
    /// </summary>
    public string? IgnoredSettingsKey { get; init; }

    /// <summary><c>malachi-mcp.exe</c> beside the app, null when it is not there.</summary>
    public string? McpBridge { get; init; }

    /// <summary>
    /// <c>malachi-credentials.exe</c> beside the app: the daemon's keyring
    /// helper (<c>MALACHI_KEYRING=helper</c>). Null when it is not there,
    /// and the daemon then runs without a keyring.
    /// </summary>
    public string? KeyringHelper { get; init; }

    /// <summary><c>config.toml</c> in the data directory (<c>--config</c>).</summary>
    public string Config => Path.Combine(DataDir, "config.toml");

    /// <summary><c>store.db</c> in the data directory (<c>--store</c>).</summary>
    public string Store => Path.Combine(DataDir, "store.db");

    /// <summary>The logs of the app and the daemon.</summary>
    public string LogDir => Path.Combine(DataDir, "logs");

    /// <summary>The daemon's log file.</summary>
    public string DaemonLog => Path.Combine(LogDir, DaemonLogName);

    /// <summary>
    /// The working directory of the user's Claude Code for the assistant
    /// panel and its one-shot requests, empty and private
    /// (<c>%LOCALAPPDATA%\Malachi Mail\assistant</c>, or under
    /// <c>MALACHI_DATA_DIR</c>; Swift's <c>ClaudeCodeLocator.defaultDirectory</c>
    /// in the Caches directory, GTK's <c>~/.cache/malachi/assistant</c>).
    /// Created private on demand, by whoever runs Claude Code there.
    /// </summary>
    public string AssistantDir => Path.Combine(DataDir, "assistant");

    /// <summary>The daemon's connection key beside the socket (api.KeyPath).</summary>
    public string KeyFile => RpcAuth.KeyPath(Socket);

    /// <summary>The paths of this process: its environment and the directory of the app.</summary>
    public static Paths Resolve() => Resolve(ProcessEnvironment.Current(), AppContext.BaseDirectory);

    /// <summary>
    /// The paths for <paramref name="environment"/>, with the bundled
    /// programs looked for in <paramref name="baseDirectory"/> (none when
    /// it is null).
    /// </summary>
    public static Paths Resolve(IReadOnlyDictionary<string, string?> environment, string? baseDirectory)
    {
        ArgumentNullException.ThrowIfNull(environment);
        var fromEnvironment = ProcessEnvironment.NonEmpty(environment, "MALACHI_SOCKET");
        string socket;
        if (fromEnvironment is not null)
        {
            socket = fromEnvironment;
        }
        else if (SocketBase(environment) is { } runtime)
        {
            socket = Join(runtime, "malachi", "rpc.sock");
        }
        else
        {
            var cache = ProcessEnvironment.NonEmpty(environment, "XDG_CACHE_HOME") ?? Join(Home(environment), ".cache");
            socket = Join(cache, "malachi", "run", "rpc.sock");
        }
        var dataDir = ProcessEnvironment.NonEmpty(environment, "MALACHI_DATA_DIR")
            ?? Path.Combine(
                ProcessEnvironment.NonEmpty(environment, "LOCALAPPDATA")
                    ?? Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
                DataDirectoryName);
        var settingsKey = ProcessEnvironment.NonEmpty(environment, SettingsKeyVariable);
        var settingsKeyTaken = settingsKey is not null && IsSettingsKeyName(settingsKey);
        return new Paths
        {
            Socket = socket,
            SocketFromEnvironment = fromEnvironment is not null,
            DataDir = dataDir,
            SettingsKey = settingsKeyTaken ? SettingsKeyParent + settingsKey : DefaultSettingsKey,
            IgnoredSettingsKey = settingsKeyTaken ? null : settingsKey,
            McpBridge = Beside(baseDirectory, McpBridgeName),
            KeyringHelper = Beside(baseDirectory, KeyringHelperName),
        };
    }

    /// <summary>
    /// Refuses a socket path longer than <see cref="MaxSocketPathBytes"/>
    /// UTF-8 bytes with a <see cref="SocketPathTooLongException"/>; the
    /// daemon would fail later with a bare "invalid argument", so the app
    /// checks first (UnixSocketProbe.check).
    /// </summary>
    public static void CheckSocket(string socket)
    {
        ArgumentNullException.ThrowIfNull(socket);
        var bytes = Encoding.UTF8.GetByteCount(socket);
        if (bytes > MaxSocketPathBytes)
        {
            throw new SocketPathTooLongException(socket, bytes);
        }
    }

    /// <summary>
    /// Creates the data directory, private, when it is missing (an existing
    /// one is left as it is), its logs directory, and the socket's directory
    /// (<see cref="EnsureSocketDirectory"/>). The daemon would create them
    /// too, without the protected DACL; an early, clear error beats a late
    /// one.
    /// </summary>
    public void EnsureDirectories(IPrivateDirectoryFactory directories)
    {
        ArgumentNullException.ThrowIfNull(directories);
        CreatePrivateIfMissing(directories, Path.GetFullPath(DataDir));
        Directory.CreateDirectory(Path.GetFullPath(LogDir));
        EnsureSocketDirectory(directories);
    }

    /// <summary>
    /// Makes the socket's directory private before a daemon is started in
    /// it, so the socket and the key file the daemon writes there inherit
    /// a DACL for the user and SYSTEM alone. The default directory
    /// (<c>...\malachi\run</c>) is the app's and is made private again if
    /// something widened it; a directory named by <c>MALACHI_SOCKET</c> is
    /// created private when it is missing and otherwise left alone.
    /// </summary>
    public void EnsureSocketDirectory(IPrivateDirectoryFactory directories)
    {
        ArgumentNullException.ThrowIfNull(directories);
        var directory = Path.GetDirectoryName(Path.GetFullPath(Socket));
        if (string.IsNullOrEmpty(directory))
        {
            return;
        }
        if (SocketFromEnvironment)
        {
            CreatePrivateIfMissing(directories, directory);
        }
        else
        {
            directories.Ensure(directory);
        }
    }

    /// <summary>
    /// Whether <paramref name="path"/> is a program: a file (a directory of
    /// that name does not count), with an execute bit where the system has
    /// them.
    /// </summary>
    internal static bool IsProgram(string path)
    {
        if (!File.Exists(path))
        {
            return false;
        }
        if (OperatingSystem.IsWindows())
        {
            return true;
        }
        const UnixFileMode anyExecute = UnixFileMode.UserExecute | UnixFileMode.GroupExecute | UnixFileMode.OtherExecute;
        return (File.GetUnixFileMode(path) & anyExecute) != 0;
    }

    // api.SocketBase: the session's runtime directory, or inside Flatpak the
    // application's own runtime directory; null without one.
    private static string? SocketBase(IReadOnlyDictionary<string, string?> environment)
    {
        var runtime = ProcessEnvironment.NonEmpty(environment, "XDG_RUNTIME_DIR");
        if (runtime is null)
        {
            return null;
        }
        var flatpak = ProcessEnvironment.NonEmpty(environment, "FLATPAK_ID");
        return flatpak is null ? runtime : Join(runtime, "app", flatpak);
    }

    // MALACHI_SETTINGS_KEY's rule: the app id itself, or the app id, a dot
    // and ASCII letters, digits, '.', '_' and '-', compared exactly (the
    // registry's own comparison ignores case, but a re-cased id is a
    // mistake, not a request). Nothing else can come out of it: no
    // backslash, so no other key's subkey or parent, and no space or
    // control character that would make a lookalike name.
    private static bool IsSettingsKeyName(string name)
    {
        const string prefix = AppIdentity.AppId + ".";
        if (name == AppIdentity.AppId)
        {
            return true;
        }
        if (name.Length == prefix.Length || !name.StartsWith(prefix, StringComparison.Ordinal))
        {
            return false;
        }
        foreach (var c in name.AsSpan(prefix.Length))
        {
            if (!char.IsAsciiLetterOrDigit(c) && c is not ('.' or '_' or '-'))
            {
                return false;
            }
        }
        return true;
    }

    // Go's os.UserHomeDir on Windows.
    private static string Home(IReadOnlyDictionary<string, string?> environment) =>
        ProcessEnvironment.NonEmpty(environment, "USERPROFILE")
            ?? Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);

    // filepath.Join: the parts with the system's separator, an absolute
    // result cleaned as Go cleans it (separators, "." and "..").
    private static string Join(params string[] parts)
    {
        var joined = Path.Combine(parts);
        return Path.IsPathFullyQualified(joined) ? Path.GetFullPath(joined) : joined;
    }

    private static string? Beside(string? directory, string name)
    {
        if (string.IsNullOrEmpty(directory))
        {
            return null;
        }
        var path = Path.Combine(directory, name);
        return IsProgram(path) ? path : null;
    }

    private static void CreatePrivateIfMissing(IPrivateDirectoryFactory directories, string directory)
    {
        if (Directory.Exists(directory))
        {
            return;
        }
        var parent = Path.GetDirectoryName(directory);
        if (!string.IsNullOrEmpty(parent))
        {
            Directory.CreateDirectory(parent);
        }
        try
        {
            directories.CreateNew(directory);
        }
        catch (IOException) when (Directory.Exists(directory))
        {
            // Created meanwhile by someone else (the daemon): theirs now.
        }
    }
}
