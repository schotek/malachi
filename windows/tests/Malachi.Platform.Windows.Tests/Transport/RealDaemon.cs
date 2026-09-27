// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only test helper: the real malachid.exe on a throwaway socket,
// configuration and store, the way docs/windows-port.md §1 starts it
// (--socket --config --store, the keyring switched off, D-Bus disabled).
// The binary comes from MALACHI_TEST_DAEMON or build\malachid.exe of the
// repository (make windows, build.ps1 go); without one the tests skip.

using System;
using System.Diagnostics;
using System.IO;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Transport;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Transport;

/// <summary>A running malachid.exe of its own, killed and cleaned up on dispose.</summary>
internal sealed class RealDaemon : IDisposable
{
    private readonly StringBuilder log = new();
    private Process? process;

    private RealDaemon(string executable, string socketDir, string dataDir)
    {
        Executable = executable;
        SocketDir = socketDir;
        DataDir = dataDir;
        Socket = Path.Combine(socketDir, "rpc.sock");
        Store = Path.Combine(dataDir, "store.db");
    }

    public string Executable { get; }

    /// <summary>A short directory in %TEMP%: the socket path stays far below 107 bytes.</summary>
    public string SocketDir { get; }

    public string DataDir { get; }

    public string Socket { get; }

    public string Store { get; }

    public int Pid => process?.Id ?? 0;

    /// <summary>What the daemon logged so far, for a failing test's message.</summary>
    public string Log
    {
        get
        {
            lock (log)
            {
                return log.ToString();
            }
        }
    }

    /// <summary>The daemon to test against, or a skipped test without one.</summary>
    public static RealDaemon CreateOrSkip()
    {
        var executable = Environment.GetEnvironmentVariable("MALACHI_TEST_DAEMON");
        if (string.IsNullOrEmpty(executable))
        {
            executable = RepositoryRoot() is { } root ? Path.Combine(root, "build", "malachid.exe") : null;
        }
        if (executable is null || !File.Exists(executable))
        {
            Assert.Skip("no malachid.exe: build it (make windows, windows\\build.ps1 go) or set MALACHI_TEST_DAEMON");
        }
        var id = Guid.NewGuid().ToString("N")[..8];
        var socketDir = Path.Combine(Path.GetTempPath(), "md-" + id);
        var dataDir = Path.Combine(Path.GetTempPath(), "malachi-test-daemon-" + id);
        Directory.CreateDirectory(socketDir);
        Directory.CreateDirectory(dataDir);
        return new RealDaemon(executable, socketDir, dataDir);
    }

    /// <summary>
    /// Starts the daemon and waits until its socket answers and a key file is
    /// there. After a crash that may still be the old run's: the daemon
    /// listens before it writes its key, and makes sure of the key before it
    /// answers <c>system.hello</c>.
    /// </summary>
    public async Task StartAsync(CancellationToken cancellationToken)
    {
        var start = new ProcessStartInfo(Executable)
        {
            UseShellExecute = false,
            CreateNoWindow = true,
            RedirectStandardInput = true,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
        };
        foreach (var argument in new[] { "--socket", Socket, "--config", Path.Combine(DataDir, "config.toml"), "--store", Store })
        {
            start.ArgumentList.Add(argument);
        }
        start.Environment["MALACHI_SOCKET"] = Socket;
        start.Environment["MALACHI_KEYRING"] = "none";
        start.Environment["MALACHI_LOG_LEVEL"] = "debug";
        start.Environment["DBUS_SESSION_BUS_ADDRESS"] = "disabled:";
        process?.Dispose();
        var started = Process.Start(start) ?? throw new InvalidOperationException("malachid did not start");
        process = started;
        started.OutputDataReceived += (_, e) => Append(e.Data);
        started.ErrorDataReceived += (_, e) => Append(e.Data);
        started.BeginOutputReadLine();
        started.BeginErrorReadLine();
        started.StandardInput.Close();

        var deadline = DateTime.UtcNow + TimeSpan.FromSeconds(15);
        while (!(UnixSocketProbe.Answers(Socket) && File.Exists(RpcAuth.KeyPath(Socket))))
        {
            if (started.HasExited)
            {
                throw new InvalidOperationException($"malachid exited with {started.ExitCode}:\n{Log}");
            }
            if (DateTime.UtcNow > deadline)
            {
                throw new TimeoutException($"malachid did not listen in 15 s:\n{Log}");
            }
            await Task.Delay(50, cancellationToken);
        }
    }

    /// <summary>Kills the daemon, as a crash would: its socket and key file stay behind.</summary>
    public void Kill()
    {
        if (process is { HasExited: false } p)
        {
            p.Kill();
            p.WaitForExit(10_000);
        }
    }

    public void Dispose()
    {
        Kill();
        process?.Dispose();
        foreach (var dir in new[] { SocketDir, DataDir })
        {
            try
            {
                Directory.Delete(dir, recursive: true);
            }
            catch (Exception e) when (e is IOException or UnauthorizedAccessException)
            {
                // A file still held; the temporary directory is swept by others.
            }
        }
    }

    private void Append(string? line)
    {
        if (line is null)
        {
            return;
        }
        lock (log)
        {
            log.AppendLine(line);
        }
    }

    // The repository's root: the directory with go.work above the tests.
    private static string? RepositoryRoot()
    {
        for (var dir = new DirectoryInfo(AppContext.BaseDirectory); dir is not null; dir = dir.Parent)
        {
            if (File.Exists(Path.Combine(dir.FullName, "go.work")))
            {
                return dir.FullName;
            }
        }
        return null;
    }
}
