// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// One run of the published app for a test: MalachiMail.exe from the app
// folder build.ps1 app assembles, with a temporary folder of its own for
// the data (MALACHI_DATA_DIR: config.toml, the store, the logs, the WebView2
// data) and the socket (MALACHI_SOCKET, a short path for AF_UNIX's 107
// bytes), the bundled daemon (MALACHI_DAEMON cleared) and the fake keyring
// helper beside the tests (no Credential Manager). The app's terminal log
// is its stderr, a pipe read here, so nothing reaches the test's console.
// Disposing it quits the app if it still runs (the primary menu's Quit),
// kills what is left after a failure (the app and the daemon it started)
// and removes the folder.
//
// What the session does not isolate: the preferences live in the user's
// HKCU\Software\io.github.schotek.Malachi, which the app reads, and where a
// Quit writes back the window's geometry it read at the start.

using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Linq;
using System.Text;
using System.Threading;
using System.Windows.Automation;

namespace Malachi.App.UiTests;

/// <summary>The app, started for a test.</summary>
internal sealed class AppSession : IDisposable
{
    /// <summary>How long the app may take to show its main window (a cold WebView2 start).</summary>
    public static readonly TimeSpan StartTimeout = TimeSpan.FromSeconds(60);

    /// <summary>How long a Quit may take (the daemon's stop included).</summary>
    public static readonly TimeSpan QuitTimeout = TimeSpan.FromSeconds(30);

    private readonly StringBuilder terminal = new();
    private readonly Process process;
    private bool disposed;

    private AppSession(string folder, Process process)
    {
        Folder = folder;
        this.process = process;
    }

    /// <summary>The session's temporary folder.</summary>
    public string Folder { get; }

    /// <summary>MALACHI_DATA_DIR.</summary>
    public string DataDir => Path.Combine(Folder, "data");

    /// <summary>MALACHI_SOCKET.</summary>
    public string Socket => Path.Combine(Folder, "s.sock");

    /// <summary>The key file beside the socket.</summary>
    public string KeyFile => Socket + ".key";

    /// <summary>The fake keyring's store.</summary>
    public string KeyringFile => Path.Combine(Folder, "keyring.json");

    /// <summary>The app's process id.</summary>
    public int ProcessId => process.Id;

    /// <summary>Whether the app has exited.</summary>
    public bool HasExited => process.HasExited;

    /// <summary>The main window, found by its New Message button once it is shown.</summary>
    public AutomationElement MainWindow => Uia.WindowWith(ProcessId, "NewMessageButton", StartTimeout);

    /// <summary>What the app wrote to its terminal (its log lines), for a failure's message.</summary>
    public string Terminal
    {
        get
        {
            lock (terminal)
            {
                return terminal.ToString();
            }
        }
    }

    /// <summary>
    /// Starts the app on a fresh data folder; <paramref name="extra"/> adds
    /// environment variables (the devmail suite's).
    /// </summary>
    public static AppSession Start(IReadOnlyDictionary<string, string>? extra = null)
    {
        // Short: the socket's path must stay within AF_UNIX's 107 bytes.
        var folder = Path.Combine(Path.GetTempPath(), "mui-" + Guid.NewGuid().ToString("N")[..8]);
        Directory.CreateDirectory(folder);
        var start = new ProcessStartInfo(UiEnvironment.AppExecutable)
        {
            UseShellExecute = false,
            WorkingDirectory = UiEnvironment.AppFolder,
            RedirectStandardError = true,
            RedirectStandardOutput = true,
        };
        var env = start.Environment;
        env["MALACHI_DATA_DIR"] = Path.Combine(folder, "data");
        env["MALACHI_SOCKET"] = Path.Combine(folder, "s.sock");
        env["MALACHI_KEYRING"] = "helper";
        env["MALACHI_KEYRING_HELPER"] = UiEnvironment.FakeKeyring;
        env[UiEnvironment.FakeKeyringFileEnv] = Path.Combine(folder, "keyring.json");
        env.Remove("MALACHI_DAEMON");
        env.Remove("MALACHI_LOCALE_DIR");
        foreach (var (key, value) in extra ?? new Dictionary<string, string>())
        {
            env[key] = value;
        }
        var process = Process.Start(start) ?? throw new InvalidOperationException("MalachiMail.exe did not start");
        var session = new AppSession(folder, process);
        process.ErrorDataReceived += (_, e) => session.Append(e.Data);
        process.OutputDataReceived += (_, e) => session.Append(e.Data);
        process.BeginErrorReadLine();
        process.BeginOutputReadLine();
        return session;
    }

    /// <summary>The daemon this app started, once it runs; null when there is none (yet).</summary>
    public Process? Daemon()
    {
        foreach (var id in ProcessTree.Children(ProcessId, "malachid.exe"))
        {
            try
            {
                var daemon = Process.GetProcessById(id);
                // Opened now, so that its exit code can be read once it has exited.
                _ = daemon.SafeHandle;
                return daemon;
            }
            catch (Exception e) when (e is ArgumentException or InvalidOperationException or System.ComponentModel.Win32Exception)
            {
                // Gone meanwhile.
            }
        }
        return null;
    }

    /// <summary>Waits for the daemon this app started and its key file (the daemon listens).</summary>
    public Process WaitForDaemon() =>
        Uia.Until(() => File.Exists(KeyFile) ? Daemon() : null, StartTimeout, "the daemon's socket and key");

    /// <summary>Opens the main window's primary menu and invokes its item <paramref name="automationId"/>.</summary>
    public void MenuItem(string automationId)
    {
        var main = MainWindow;
        Uia.Invoke(Uia.Find(main, "MainMenuButton"));
        // The flyout is part of the window's tree while it is open.
        Uia.Invoke(Uia.Find(main, automationId));
    }

    /// <summary>Waits for the app to exit, up to <paramref name="timeout"/>; true when it did.</summary>
    public bool WaitForExit(TimeSpan timeout) => process.WaitForExit((int)timeout.TotalMilliseconds);

    /// <summary>The app's exit code, once it exited.</summary>
    public int ExitCode => process.ExitCode;

    /// <summary>Quits the app (if it runs), kills what is left, removes the folder.</summary>
    public void Dispose()
    {
        if (disposed)
        {
            return;
        }
        disposed = true;
        var daemon = process.HasExited ? null : Daemon();
        if (!process.HasExited)
        {
            try
            {
                MenuItem("MenuQuit");
                WaitForExit(QuitTimeout);
            }
            catch (Exception e) when (e is TimeoutException or ElementNotAvailableException or InvalidOperationException)
            {
                // A window left open by a failed test may hold a question: killed below.
            }
        }
        Kill(process);
        if (daemon is not null)
        {
            // Stopped by the Quit; after a kill of the app, left behind.
            daemon.WaitForExit(5000);
            Kill(daemon);
            daemon.Dispose();
        }
        process.Dispose();
        RemoveFolder();
    }

    private static void Kill(Process p)
    {
        try
        {
            if (!p.HasExited)
            {
                p.Kill();
                p.WaitForExit(5000);
            }
        }
        catch (Exception e) when (e is InvalidOperationException or System.ComponentModel.Win32Exception)
        {
            // Exited meanwhile.
        }
    }

    private void Append(string? line)
    {
        if (line is null)
        {
            return;
        }
        lock (terminal)
        {
            terminal.AppendLine(line);
        }
    }

    // WebView2's browser processes hold their data for some seconds after
    // the app has gone (measured: more than five).
    private void RemoveFolder()
    {
        for (var attempt = 0; attempt < 80 && Directory.Exists(Folder); attempt++)
        {
            try
            {
                Directory.Delete(Folder, recursive: true);
            }
            catch (Exception e) when (e is IOException or UnauthorizedAccessException)
            {
                Thread.Sleep(250);
            }
        }
    }
}
