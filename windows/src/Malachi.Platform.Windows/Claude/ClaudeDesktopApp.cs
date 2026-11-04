// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/ClaudeDesktopService.swift
// (running, quit, launch, the termination it reports); no GTK counterpart:
// the GTK app does not hand mail to Claude Desktop. What Core's
// ClaudeDesktopController asks of the platform around a change of
// "Register with Claude": Claude Desktop rewrites its configuration from
// memory while it runs, so the change is written while it does not.
//
// Windows differences. Claude Desktop is the MSIX package
// Claude_pzs8sxrjxfjjc (ClaudeDesktopPackage), so it runs when a claude.exe
// of that package family runs (GetPackageFamilyName): the name alone would
// also match Claude Code's claude.exe, and the package has other processes
// (RuntimeBroker) that outlive it. Closing its window only hides it to the
// notification area, so there is no quit request of the Dock's kind; the
// Restart Manager's shutdown (RmShutdown without RmForceShutdown: the
// session end Windows sends at sign-out, WM_QUERYENDSESSION and
// WM_ENDSESSION to its windows) is what an Electron app answers by
// quitting. Measured on Windows 11 26100 with GitHub Desktop, another
// Electron app (2026-09-29): gone after 20 s, and once after RmShutdown had
// given up at its own 30 s (ERROR_FAIL_SHUTDOWN) — so the caller's wait,
// not RmShutdown's answer, decides, and a quit that comes late is still
// noticed (WaitForExitAsync). Nothing is forced. The quit is asked of the
// root of each instance (the claude.exe whose parent is not one of the
// package's), the process with the windows; its helpers end with it. It
// starts again through its application user model id (the manifest's
// Application Id "Claude"), as the Start menu starts it, which brings its
// window to the front where macOS starts it in the background.
//
// Never run against the real Claude Desktop in tests: a session of Claude
// Code inside Claude Desktop would end with it.

using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.Linq;
using System.Runtime.InteropServices;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Platform;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;
using Windows.Win32;
using Windows.Win32.Foundation;
using Windows.Win32.System.Diagnostics.ToolHelp;
using Windows.Win32.System.RestartManager;
using Windows.Win32.System.Threading;
using Windows.Win32.UI.Shell;

namespace Malachi.Platform.Windows.Claude;

/// <summary>
/// Claude Desktop from the Microsoft Store as Windows sees it: whether it
/// runs, asking it to quit, starting it, waiting until it has quit.
/// </summary>
public sealed partial class ClaudeDesktopApp
{
    /// <summary>The manifest's Application Id of Claude Desktop.</summary>
    public const string ApplicationId = "Claude";

    /// <summary>How often a quit looks whether Claude Desktop has gone.</summary>
    public static readonly TimeSpan PollInterval = TimeSpan.FromMilliseconds(200);

    /// <summary>How often <see cref="WaitForExitAsync"/> looks.</summary>
    public static readonly TimeSpan WatchInterval = TimeSpan.FromSeconds(2);

    private const string ImageName = "claude.exe";

    private readonly TimeProvider time;
    private readonly ILogger logger;

    /// <summary>Claude Desktop of <paramref name="packageFamily"/> (the Store's by default).</summary>
    public ClaudeDesktopApp(
        string packageFamily = ClaudeDesktopPackage.ClaudePackageFamily, TimeProvider? time = null, ILogger<ClaudeDesktopApp>? logger = null)
    {
        ArgumentException.ThrowIfNullOrEmpty(packageFamily);
        PackageFamily = packageFamily;
        this.time = time ?? TimeProvider.System;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
    }

    /// <summary>The MSIX package family.</summary>
    public string PackageFamily { get; }

    /// <summary>The application user model id the Start menu starts it by.</summary>
    public string AppUserModelId => PackageFamily + "!" + ApplicationId;

    /// <summary>Whether a claude.exe of the package runs now.</summary>
    public bool IsRunning() => Processes().Count > 0;

    /// <summary>
    /// Asks every instance to quit (the Restart Manager's shutdown, on a
    /// thread of its own) and waits until none runs, at most
    /// <paramref name="timeout"/>; true when none does (or none ran).
    /// </summary>
    public async Task<bool> QuitAsync(TimeSpan timeout, CancellationToken cancellationToken = default)
    {
        var processes = Processes();
        if (processes.Count == 0)
        {
            return true;
        }
        var roots = Roots(processes);
        // RmShutdown blocks until the applications answered or its own
        // limit ran out; the wait below decides either way.
        _ = Task.Run(() => Shutdown(roots), CancellationToken.None);
        var start = time.GetTimestamp();
        while (IsRunning())
        {
            if (time.GetElapsedTime(start) >= timeout)
            {
                LogDidNotQuit(logger, timeout.TotalSeconds);
                return false;
            }
            await Task.Delay(PollInterval, time, cancellationToken).ConfigureAwait(false);
        }
        return true;
    }

    /// <summary>
    /// Waits until Claude Desktop does not run (at once when it does not),
    /// looking every <see cref="WatchInterval"/>: the counterpart of
    /// NSWorkspace's termination notification, for a change kept pending.
    /// </summary>
    public async Task WaitForExitAsync(CancellationToken cancellationToken)
    {
        while (IsRunning())
        {
            await Task.Delay(WatchInterval, time, cancellationToken).ConfigureAwait(false);
        }
    }

    /// <summary>Starts Claude Desktop as the Start menu does; false (and logged) when Windows refused.</summary>
    public bool Launch()
    {
        try
        {
            var manager = (IApplicationActivationManager)new ApplicationActivationManager();
            try
            {
                manager.ActivateApplication(AppUserModelId, null, ACTIVATEOPTIONS.AO_NONE, out _);
                return true;
            }
            finally
            {
                Marshal.ReleaseComObject(manager);
            }
        }
        catch (COMException e)
        {
            LogLaunchFailed(logger, e.HResult);
            return false;
        }
    }

    /// <summary>The package's claude.exe processes, each with its parent's id.</summary>
    internal IReadOnlyList<(int Id, int Parent)> Processes() =>
        Snapshot().Where(p => string.Equals(p.Name, ImageName, StringComparison.OrdinalIgnoreCase)
            && string.Equals(FamilyOf(p.Id), PackageFamily, StringComparison.Ordinal))
            .Select(p => (p.Id, p.Parent)).ToList();

    /// <summary>The processes of <paramref name="processes"/> whose parent is not among them: the root of each instance.</summary>
    internal static IReadOnlyList<int> Roots(IReadOnlyList<(int Id, int Parent)> processes)
    {
        var ids = processes.Select(p => p.Id).ToHashSet();
        return processes.Where(p => !ids.Contains(p.Parent)).Select(p => p.Id).ToList();
    }

    /// <summary>The package family of process <paramref name="id"/>; "" for a process of no package, null when it cannot be asked.</summary>
    internal static string? FamilyOf(int id)
    {
        using var handle = PInvoke.OpenProcess_SafeHandle(PROCESS_ACCESS_RIGHTS.PROCESS_QUERY_LIMITED_INFORMATION, false, (uint)id);
        if (handle.IsInvalid)
        {
            return null;
        }
        uint length = 0;
        var status = PInvoke.GetPackageFamilyName(handle, ref length, null);
        if (status == WIN32_ERROR.APPMODEL_ERROR_NO_PACKAGE)
        {
            return "";
        }
        if (status != WIN32_ERROR.ERROR_INSUFFICIENT_BUFFER || length == 0)
        {
            return null;
        }
        var buffer = new char[length];
        status = PInvoke.GetPackageFamilyName(handle, ref length, buffer);
        if (status != WIN32_ERROR.ERROR_SUCCESS)
        {
            return null;
        }
        var end = Array.IndexOf(buffer, '\0');
        return new string(buffer, 0, end < 0 ? buffer.Length : end);
    }

    // Every process of the system: its id, its parent's and its image name.
    private static unsafe List<(int Id, int Parent, string Name)> Snapshot()
    {
        var found = new List<(int, int, string)>();
        using var snapshot = PInvoke.CreateToolhelp32Snapshot_SafeHandle(CREATE_TOOLHELP_SNAPSHOT_FLAGS.TH32CS_SNAPPROCESS, 0);
        if (snapshot.IsInvalid)
        {
            return found;
        }
        var entry = new PROCESSENTRY32W { dwSize = (uint)sizeof(PROCESSENTRY32W) };
        if (!PInvoke.Process32FirstW(snapshot, ref entry))
        {
            return found;
        }
        do
        {
            found.Add(((int)entry.th32ProcessID, (int)entry.th32ParentProcessID, entry.szExeFile.ToString()));
        }
        while (PInvoke.Process32NextW(snapshot, ref entry));
        return found;
    }

    // The Restart Manager's shutdown of the roots, without force: the
    // session end their windows answer. Its result is only logged.
    private unsafe void Shutdown(IReadOnlyList<int> roots)
    {
        var processes = new List<RM_UNIQUE_PROCESS>();
        foreach (var id in roots)
        {
            using var handle = PInvoke.OpenProcess_SafeHandle(PROCESS_ACCESS_RIGHTS.PROCESS_QUERY_LIMITED_INFORMATION, false, (uint)id);
            if (handle.IsInvalid || !PInvoke.GetProcessTimes(handle, out var creation, out _, out _, out _))
            {
                continue;
            }
            processes.Add(new RM_UNIQUE_PROCESS { dwProcessId = (uint)id, ProcessStartTime = creation });
        }
        if (processes.Count == 0)
        {
            return;
        }
        Span<char> key = stackalloc char[(int)PInvoke.CCH_RM_SESSION_KEY + 1];
        var status = PInvoke.RmStartSession(out var session, key);
        if (status != WIN32_ERROR.ERROR_SUCCESS)
        {
            LogRestartManager(logger, "RmStartSession", (uint)status);
            return;
        }
        try
        {
            status = PInvoke.RmRegisterResources(session, default, processes.ToArray(), default);
            if (status != WIN32_ERROR.ERROR_SUCCESS)
            {
                LogRestartManager(logger, "RmRegisterResources", (uint)status);
                return;
            }
            status = PInvoke.RmShutdown(session, 0, null);
            if (status != WIN32_ERROR.ERROR_SUCCESS)
            {
                LogRestartManager(logger, "RmShutdown", (uint)status);
            }
        }
        finally
        {
            PInvoke.RmEndSession(session);
        }
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "Claude Desktop did not quit within {Seconds} s")]
    private static partial void LogDidNotQuit(ILogger logger, double seconds);

    [LoggerMessage(Level = LogLevel.Information, Message = "{Call} returned {Status}")]
    private static partial void LogRestartManager(ILogger logger, string call, uint status);

    [LoggerMessage(Level = LogLevel.Warning, Message = "starting Claude Desktop failed: 0x{HResult:X8}")]
    private static partial void LogLaunchFailed(ILogger logger, int hResult);
}
