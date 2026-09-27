// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// What the supervisor tests share: a scratch directory with a short socket
// path (Go: filepath.Join(t.TempDir(), "rpc.sock"); Swift: tempSocket), a
// supervisor over the stand-in daemon in a mode (daemon_test.go
// newSupervisor, which also stops it when the test ends), a listener that
// plays a daemon started by someone else, programs in a directory
// (SupervisorTests.swift script), and the probe as a plain call.

using System;
using System.Collections.Generic;
using System.IO;
using System.Net.Sockets;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Daemon;
using Malachi.Core.TestDaemon;
using Malachi.Core.Tests.Platform;

namespace Malachi.Core.Tests.Daemon;

/// <summary>A scratch directory, the supervisors made in it and their daemons; stopped and removed on dispose.</summary>
internal sealed class DaemonFixture : IAsyncDisposable
{
    private readonly TemporaryDirectory directory = new();
    private readonly List<DaemonSupervisor> supervisors = [];
    private readonly List<Socket> listeners = [];

    /// <summary>The scratch directory.</summary>
    public string Path => directory.Path;

    /// <summary>A socket path in the scratch directory.</summary>
    public string Socket => System.IO.Path.Combine(directory.Path, "rpc.sock");

    /// <summary>The host the supervisors of <see cref="Supervisor"/> start processes with.</summary>
    public TestProcessHost Host { get; } = new();

    /// <summary>Whether something answers on <paramref name="socket"/> (daemon_test.go answers).</summary>
    public static bool Answers(string socket) =>
        DaemonSupervisor.AnswersAsync(socket, CancellationToken.None).AsTask().GetAwaiter().GetResult();

    /// <summary>
    /// This process's environment with <paramref name="extra"/> set: what a
    /// supervisor passes on to the stand-in daemon.
    /// </summary>
    public static Dictionary<string, string?> Environment(params (string Name, string Value)[] extra)
    {
        var env = new Dictionary<string, string?>(OperatingSystem.IsWindows() ? StringComparer.OrdinalIgnoreCase : StringComparer.Ordinal);
        foreach (System.Collections.DictionaryEntry entry in System.Environment.GetEnvironmentVariables())
        {
            env[(string)entry.Key] = (string?)entry.Value;
        }
        foreach (var (name, value) in extra)
        {
            env[name] = value;
        }
        return env;
    }

    /// <summary>
    /// A supervisor of the stand-in daemon in <paramref name="mode"/> on
    /// <see cref="Socket"/>, stopped when the fixture goes.
    /// </summary>
    public DaemonSupervisor Supervisor(
        string mode,
        TimeSpan? startTimeout = null,
        TimeSpan? stopTimeout = null,
        params (string Name, string Value)[] extra)
    {
        var supervisor = new DaemonSupervisor(Launch(TestDaemonSettings.ExecutablePath), Socket, Host)
        {
            BaseEnvironment = Environment([(TestDaemonSettings.ModeEnv, mode), .. extra]),
            StartTimeout = startTimeout ?? DaemonSupervisor.DefaultStartTimeout,
            StopTimeout = stopTimeout ?? DaemonSupervisor.DefaultStopTimeout,
        };
        supervisors.Add(supervisor);
        return supervisor;
    }

    /// <summary>A supervisor made by the caller, stopped when the fixture goes.</summary>
    public DaemonSupervisor Track(DaemonSupervisor supervisor)
    {
        supervisors.Add(supervisor);
        return supervisor;
    }

    /// <summary>How to start <paramref name="executable"/> on <see cref="Socket"/>, with config and store in the scratch directory.</summary>
    public DaemonLaunch Launch(string executable, string? keyringHelper = null) => new()
    {
        Executable = executable,
        Socket = Socket,
        Config = System.IO.Path.Combine(directory.Path, "c.toml"),
        Store = System.IO.Path.Combine(directory.Path, "s.db"),
        KeyringHelper = keyringHelper,
    };

    /// <summary>A listener on <paramref name="socket"/> that accepts nothing: a daemon somebody else started.</summary>
    public void Listen(string socket)
    {
        var listener = new Socket(AddressFamily.Unix, SocketType.Stream, ProtocolType.Unspecified);
        listener.Bind(new UnixDomainSocketEndPoint(socket));
        listener.Listen(8);
        listeners.Add(listener);
    }

    /// <summary>
    /// An empty file <paramref name="name"/> in <paramref name="subdirectory"/>
    /// of the scratch directory that counts as a program (an execute bit
    /// where the system has them); returns its path.
    /// </summary>
    public string Program(string subdirectory, string name)
    {
        var dir = System.IO.Path.Combine(directory.Path, subdirectory);
        Directory.CreateDirectory(dir);
        var path = System.IO.Path.Combine(dir, name);
        File.WriteAllBytes(path, []);
        if (!OperatingSystem.IsWindows())
        {
            File.SetUnixFileMode(path, UnixFileMode.UserRead | UnixFileMode.UserWrite | UnixFileMode.UserExecute);
        }
        return path;
    }

    public async ValueTask DisposeAsync()
    {
        foreach (var supervisor in supervisors)
        {
            await supervisor.StopAsync();
            supervisor.Dispose();
        }
        foreach (var listener in listeners)
        {
            listener.Dispose();
        }
        directory.Dispose();
    }
}
