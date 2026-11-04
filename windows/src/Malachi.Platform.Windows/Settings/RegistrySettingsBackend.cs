// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The Windows store of the settings (docs/windows-port.md §0, §8); the
// counterpart of UserDefaults with its KVO observer under
// macos/Sources/MalachiCore/Settings/Settings.swift (DefaultsObserver) and
// of GSettings with its "changed" signal under ui/internal/settings/store.go
// (Open, ConnectChanged).
//
// HKCU\Software\io.github.schotek.Malachi holds the gschema keys under their
// gschema names: REG_DWORD for b and i (i read as a signed 32-bit number),
// REG_SZ for s (enum nicks too), REG_MULTI_SZ for as. A value of another type
// reads as unset (the default applies). A watcher thread waits on
// RegNotifyChangeKeyValue, re-arms it before it reads, diffs every value
// against a snapshot and raises Changed per changed name, so a reg add or a
// second instance reaches the running app as gsettings set does on GNOME;
// the snapshot is updated under the same lock by every write of this
// backend, so its own writes are never reported back. A deleted key (reg
// delete) is created again and every value it held reads as changed.

using System;
using System.Collections.Generic;
using System.Diagnostics.CodeAnalysis;
using System.IO;
using System.Linq;
using System.Security;
using System.Threading;
using Malachi.Core;
using Malachi.Core.Settings;
using Microsoft.Extensions.Logging;
using Microsoft.Win32;
using Windows.Win32;
using Windows.Win32.Foundation;
using Windows.Win32.System.Registry;

namespace Malachi.Platform.Windows.Settings;

/// <summary>Settings in the current user's registry, with change notification.</summary>
public sealed partial class RegistrySettingsBackend : ISettingsBackend
{
    /// <summary>The app's key under HKEY_CURRENT_USER.</summary>
    public const string DefaultPath = @"Software\" + AppIdentity.AppId;

    private const REG_NOTIFY_FILTER Filter =
        REG_NOTIFY_FILTER.REG_NOTIFY_CHANGE_NAME | REG_NOTIFY_FILTER.REG_NOTIFY_CHANGE_LAST_SET;

    private static readonly TimeSpan StopTimeout = TimeSpan.FromSeconds(10);

    private readonly Lock gate = new();
    private readonly string path;
    private readonly ILogger? logger;
    private readonly AutoResetEvent changed = new(false);
    private readonly ManualResetEvent stop = new(false);
    private readonly Thread watcher;
    private RegistryKey key;
    private Dictionary<string, object> snapshot;
    private int disposed;

    /// <summary>
    /// Opens (creating it if needed) <paramref name="path"/> under
    /// HKEY_CURRENT_USER and starts watching it. Throws when the key cannot
    /// be opened; <see cref="Open"/> falls back to memory instead.
    /// </summary>
    public RegistrySettingsBackend(string path = DefaultPath, ILogger? logger = null)
    {
        ArgumentException.ThrowIfNullOrEmpty(path);
        this.path = path;
        this.logger = logger;
        key = OpenKey(path);
        snapshot = ReadAll();
        watcher = new Thread(Watch) { IsBackground = true, Name = "Malachi settings watcher" };
        watcher.Start();
    }

    /// <inheritdoc/>
    public event EventHandler<SettingsChangedEventArgs>? Changed;

    /// <inheritdoc/>
    public bool IsPersistent => true;

    /// <summary>The key's path under HKEY_CURRENT_USER.</summary>
    public string Path => path;

    /// <summary>
    /// The registry backend, or an in-memory one when the key cannot be
    /// opened (logged): the app keeps working and nothing persists, as the
    /// GTK UI does without its schema (settings.Open).
    /// </summary>
    public static ISettingsBackend Open(string path = DefaultPath, ILogger? logger = null)
    {
        try
        {
            return new RegistrySettingsBackend(path, logger);
        }
        catch (Exception e) when (e is ArgumentException or IOException or UnauthorizedAccessException or SecurityException)
        {
            if (logger is not null)
            {
                LogFallback(logger, path, e.Message);
            }
            return new InMemorySettingsBackend();
        }
    }

    /// <inheritdoc/>
    public bool TryGetBoolean(string key, out bool value)
    {
        if (Read(key) is int dword)
        {
            value = dword != 0;
            return true;
        }
        value = false;
        return false;
    }

    /// <inheritdoc/>
    public bool TryGetInt32(string key, out int value)
    {
        if (Read(key) is int dword)
        {
            value = dword;
            return true;
        }
        value = 0;
        return false;
    }

    /// <inheritdoc/>
    public bool TryGetString(string key, [NotNullWhen(true)] out string? value)
    {
        value = Read(key) as string;
        return value is not null;
    }

    /// <inheritdoc/>
    public bool TryGetStringList(string key, [NotNullWhen(true)] out IReadOnlyList<string>? value)
    {
        value = Read(key) is string[] list ? list : null;
        return value is not null;
    }

    /// <inheritdoc/>
    public void SetBoolean(string key, bool value) => Write(key, value ? 1 : 0, RegistryValueKind.DWord);

    /// <inheritdoc/>
    public void SetInt32(string key, int value) => Write(key, value, RegistryValueKind.DWord);

    /// <inheritdoc/>
    public void SetString(string key, string value)
    {
        ArgumentNullException.ThrowIfNull(value);
        Write(key, value, RegistryValueKind.String);
    }

    /// <inheritdoc/>
    public void SetStringList(string key, IReadOnlyList<string> value)
    {
        ArgumentNullException.ThrowIfNull(value);
        Write(key, value.ToArray(), RegistryValueKind.MultiString);
    }

    /// <summary>Stops the watcher thread and closes the key; later reads see the defaults.</summary>
    public void Dispose()
    {
        if (Interlocked.Exchange(ref disposed, 1) != 0)
        {
            return;
        }
        stop.Set();
        var stopped = watcher.Join(StopTimeout);
        if (!stopped && logger is not null)
        {
            LogWatcherStuck(logger, path);
        }
        lock (gate)
        {
            key.Dispose();
        }
        // A watcher that did not stop (a handler without a synchronization
        // context never returned) keeps its events: disposing them under it
        // would crash the process.
        if (stopped)
        {
            changed.Dispose();
            stop.Dispose();
        }
    }

    private static RegistryKey OpenKey(string path) =>
        Registry.CurrentUser.CreateSubKey(path, writable: true)
        ?? throw new IOException($"HKEY_CURRENT_USER\\{path} could not be opened");

    // A stored value, or null when it is unset, unreadable or the backend
    // is closed. REG_DWORD reads as int, REG_SZ as string, REG_MULTI_SZ as
    // string[] (a fresh array per read).
    private object? Read(string name)
    {
        ArgumentNullException.ThrowIfNull(name);
        lock (gate)
        {
            if (disposed != 0)
            {
                return null;
            }
            try
            {
                return key.GetValue(name, null, RegistryValueOptions.DoNotExpandEnvironmentNames);
            }
            catch (Exception e) when (e is IOException or UnauthorizedAccessException or SecurityException)
            {
                return null;
            }
        }
    }

    private void Write(string name, object data, RegistryValueKind kind)
    {
        ArgumentNullException.ThrowIfNull(name);
        lock (gate)
        {
            if (disposed != 0)
            {
                return;
            }
            for (var attempt = 0; ; attempt++)
            {
                try
                {
                    key.SetValue(name, data, kind);
                    // What the watcher will read back, so it does not report it.
                    snapshot[name] = data is string[] list ? (string[])list.Clone() : data;
                    return;
                }
                catch (IOException) when (attempt == 0)
                {
                    // Deleted from outside (reg delete): create it again.
                    Reopen();
                }
                catch (Exception e) when (e is IOException or UnauthorizedAccessException or SecurityException)
                {
                    if (logger is not null)
                    {
                        LogWriteFailed(logger, name, e.Message);
                    }
                    return;
                }
            }
        }
    }

    // Under the lock.
    private void Reopen()
    {
        try
        {
            key.Dispose();
            key = OpenKey(path);
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException or SecurityException)
        {
            if (logger is not null)
            {
                LogReopenFailed(logger, path, e.Message);
            }
        }
    }

    // Every named value, under the lock. A key deleted from outside is
    // created again (empty); a value that vanishes while it is read is left
    // out, the next notification reports it.
    private Dictionary<string, object> ReadAll()
    {
        var result = new Dictionary<string, object>(StringComparer.OrdinalIgnoreCase);
        string[] names;
        try
        {
            names = key.GetValueNames();
        }
        catch (IOException)
        {
            Reopen();
            try
            {
                names = key.GetValueNames();
            }
            catch (Exception e) when (e is IOException or UnauthorizedAccessException or SecurityException)
            {
                return result;
            }
        }
        catch (Exception e) when (e is UnauthorizedAccessException or SecurityException or ObjectDisposedException)
        {
            return result;
        }
        foreach (var name in names)
        {
            if (name.Length == 0)
            {
                continue;
            }
            try
            {
                var data = key.GetValue(name, null, RegistryValueOptions.DoNotExpandEnvironmentNames);
                if (data is not null)
                {
                    result[name] = data;
                }
            }
            catch (Exception e) when (e is IOException or UnauthorizedAccessException or SecurityException)
            {
                // Gone meanwhile.
            }
        }
        return result;
    }

    private void Watch()
    {
        WaitHandle[] handles = [stop, changed];
        Register();
        // A change made between the constructor's snapshot and the first
        // registration is found by the diff.
        Process();
        while (WaitHandle.WaitAny(handles) == 1)
        {
            // Re-arm before reading, so that nothing written from now on is missed.
            Register();
            Process();
        }
    }

    private void Register()
    {
        lock (gate)
        {
            for (var attempt = 0; attempt < 2; attempt++)
            {
                if (disposed != 0)
                {
                    return;
                }
                var result = PInvoke.RegNotifyChangeKeyValue(key.Handle, false, Filter, changed.SafeWaitHandle, true);
                if (result == WIN32_ERROR.NO_ERROR)
                {
                    return;
                }
                if (result != WIN32_ERROR.ERROR_KEY_DELETED || attempt > 0)
                {
                    if (logger is not null)
                    {
                        LogWatchFailed(logger, path, (uint)result);
                    }
                    return;
                }
                Reopen();
            }
        }
    }

    private void Process()
    {
        List<string> names;
        lock (gate)
        {
            if (disposed != 0)
            {
                return;
            }
            var current = ReadAll();
            names = [];
            foreach (var (name, data) in current)
            {
                if (!snapshot.TryGetValue(name, out var old) || !SameData(old, data))
                {
                    names.Add(name);
                }
            }
            foreach (var name in snapshot.Keys)
            {
                if (!current.ContainsKey(name))
                {
                    names.Add(name);
                }
            }
            snapshot = current;
        }
        names.Sort(StringComparer.Ordinal);
        foreach (var name in names)
        {
            try
            {
                Changed?.Invoke(this, new SettingsChangedEventArgs(name));
            }
            catch (Exception e) when (e is not OutOfMemoryException)
            {
                // A handler that runs here (a store without a synchronization
                // context) must not end the watcher.
                if (logger is not null)
                {
                    LogHandlerFailed(logger, e, name);
                }
            }
        }
    }

    private static bool SameData(object a, object b) => (a, b) switch
    {
        (string[] x, string[] y) => x.SequenceEqual(y, StringComparer.Ordinal),
        (byte[] x, byte[] y) => x.AsSpan().SequenceEqual(y),
        (string x, string y) => string.Equals(x, y, StringComparison.Ordinal),
        _ => a.GetType() == b.GetType() && a.Equals(b),
    };

    [LoggerMessage(Level = LogLevel.Warning, Message = "Settings key {Path} cannot be opened, preferences will not persist: {Reason}")]
    private static partial void LogFallback(ILogger logger, string path, string reason);

    [LoggerMessage(Level = LogLevel.Warning, Message = "Setting {Name} not stored: {Reason}")]
    private static partial void LogWriteFailed(ILogger logger, string name, string reason);

    [LoggerMessage(Level = LogLevel.Warning, Message = "Settings key {Path} cannot be opened again: {Reason}")]
    private static partial void LogReopenFailed(ILogger logger, string path, string reason);

    [LoggerMessage(Level = LogLevel.Warning, Message = "Settings key {Path} cannot be watched (error {Code}); outside changes are not seen")]
    private static partial void LogWatchFailed(ILogger logger, string path, uint code);

    [LoggerMessage(Level = LogLevel.Warning, Message = "The settings watcher of {Path} did not stop")]
    private static partial void LogWatcherStuck(ILogger logger, string path);

    [LoggerMessage(Level = LogLevel.Error, Message = "A handler of setting {Name} failed")]
    private static partial void LogHandlerFailed(ILogger logger, Exception exception, string name);
}
