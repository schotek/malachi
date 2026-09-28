// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Daemon/DaemonSupervisor.swift
// (DaemonSupervisor) and of the probe of Transport/UnixSocketProbe.swift
// (answers); GTK: ui/internal/daemon/daemon.go (Supervisor, Locate,
// answers). The state machine and its numbers are Swift's and Go's: probe
// every 100 ms, 15 s for the socket after a start, 15 s for a clean exit
// before the kill, a restart after one exit at once and then after 1 s
// doubling to 60 s, a daemon that already answers adopted and never
// stopped. The process itself is the platform's (IDaemonProcessHost:
// CTRL_BREAK instead of SIGTERM on Windows), and its Exited task is the
// record of its exit. Ensure calls are serialised as in Go (a second caller
// waits for the first spawn instead of racing it); the Swift actor gives
// the same result. Like a call into the Swift actor, Ensure and Stop leave
// the caller's thread before they do anything: the probe's connect,
// BeforeStart and CreateProcess are synchronous, and the caller is the UI
// thread. The probe counts a busy listener as there (a full backlog, a
// connect that outlasts the probe's 500 ms): that is Swift's rule
// (UnixSocketProbe: EINPROGRESS and EAGAIN answer), which §5 of
// docs/windows-port.md keeps; Go's answers counts only a completed
// connect. Windows additions: the daemon's environment also
// disables D-Bus (DBUS_SESSION_BUS_ADDRESS=disabled:, docs/windows-port.md
// §1), and BeginStopping lets the console handler stop restarts before the
// daemon, which got the terminal's CTRL_CLOSE too, is seen to exit (§5
// Console). Locate looks for malachid.exe and skips PATH entries that are
// not fully qualified, as Go's LookPath refuses relative results.

using System;
using System.Collections.Generic;
using System.IO;
using System.Net.Sockets;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Daemon;

/// <summary>
/// Starts and stops malachid. Process management only: it knows where the
/// daemon binary is, which socket, config and store to hand it and whether
/// it is still alive; it never speaks the protocol. A daemon that already
/// answers on the socket (<c>make run-backend</c>, a debugger, one left
/// behind by a crash) is used as is and never stopped: the supervisor only
/// ever stops the process it started itself. Safe for use from any thread.
/// </summary>
public sealed partial class DaemonSupervisor : IDisposable
{
    /// <summary><c>MALACHI_DAEMON</c>: a path, or <c>none</c> / empty to never start one.</summary>
    public const string DaemonEnv = "MALACHI_DAEMON";

    /// <summary>The daemon's keyring selection (<c>secretservice|helper|none</c>; backend/internal/auth/helper).</summary>
    public const string KeyringEnv = "MALACHI_KEYRING";

    /// <summary>The helper the daemon runs for <c>MALACHI_KEYRING=helper</c>.</summary>
    public const string KeyringHelperEnv = "MALACHI_KEYRING_HELPER";

    /// <summary>
    /// The D-Bus session bus address. Set to <c>disabled:</c>, the Secret
    /// Service, GNOME Online Accounts and EDS paths fail at once instead of
    /// searching PATH for <c>dbus-launch</c> on every call.
    /// </summary>
    public const string DBusEnv = "DBUS_SESSION_BUS_ADDRESS";

    /// <summary>The daemon's executable name.</summary>
    public const string ExecutableName = "malachid.exe";

    /// <summary>How long a start waits for the socket.</summary>
    public static readonly TimeSpan DefaultStartTimeout = TimeSpan.FromSeconds(15);

    /// <summary>How long a stop waits for a clean exit; the daemon gives its syncers 10 s to log out.</summary>
    public static readonly TimeSpan DefaultStopTimeout = TimeSpan.FromSeconds(15);

    /// <summary>How often the socket is probed while waiting.</summary>
    public static readonly TimeSpan DefaultPollInterval = TimeSpan.FromMilliseconds(100);

    /// <summary>The longest pause between restarts of a daemon that keeps exiting.</summary>
    public static readonly TimeSpan DefaultMaxBackoff = TimeSpan.FromSeconds(60);

    // How long one probe may take (daemon.go answers: DialTimeout). An I/O
    // timeout, deliberately on the real clock rather than Time: AnswersAsync
    // is static, and a supervisor on a fake clock is given a fake Probe.
    private static readonly TimeSpan ProbeTimeout = TimeSpan.FromMilliseconds(500);

    private readonly DaemonLaunch? launch;
    private readonly string socket;
    private readonly IDaemonProcessHost? host;
    private readonly SemaphoreSlim ensureGate = new(1, 1);
    private readonly Lock gate = new();

    // Guarded by gate. The process is the one started last, until its exit
    // is noted; its Exited task says whether it has exited.
    private IDaemonProcess? process;
    private bool stopping;
    private int failures;
    private long? nextTry;
    private int spawns;

    /// <summary>
    /// A supervisor for the daemon <paramref name="launch"/> describes, or
    /// one that only ever adopts a daemon when it is null.
    /// </summary>
    /// <param name="launch">How to start a daemon, or null to only ever adopt one.</param>
    /// <param name="socket">Where a daemon is expected to answer.</param>
    /// <param name="host">Starts the process; required with a launch.</param>
    public DaemonSupervisor(DaemonLaunch? launch, string socket, IDaemonProcessHost? host = null)
    {
        ArgumentNullException.ThrowIfNull(socket);
        if (launch is not null && host is null)
        {
            throw new ArgumentNullException(nameof(host), "a launch needs a process host");
        }
        this.launch = launch;
        this.socket = socket;
        this.host = host;
    }

    /// <summary>The clock of the waits and the backoff.</summary>
    public TimeProvider Time { get; init; } = TimeProvider.System;

    /// <summary>Where the supervisor's own lines go (never the daemon's output).</summary>
    public ILogger Logger { get; init; } = NullLogger.Instance;

    /// <summary>
    /// The environment the daemon's is made from
    /// (<see cref="Environment"/>); null for this process's, read at each start.
    /// </summary>
    public IReadOnlyDictionary<string, string?>? BaseEnvironment { get; init; }

    /// <summary>
    /// Whether something answers on a socket; <see cref="AnswersAsync"/>
    /// unless replaced.
    /// </summary>
    public Func<string, CancellationToken, ValueTask<bool>> Probe { get; init; } = AnswersAsync;

    /// <summary>
    /// Runs before every start, for instance
    /// <see cref="Paths.EnsureSocketDirectory"/>; an exception counts as a
    /// start that failed.
    /// </summary>
    public Action? BeforeStart { get; init; }

    /// <summary>How long a start waits for the socket (<see cref="DefaultStartTimeout"/>).</summary>
    public TimeSpan StartTimeout { get; init; } = DefaultStartTimeout;

    /// <summary>How long a stop waits for a clean exit (<see cref="DefaultStopTimeout"/>).</summary>
    public TimeSpan StopTimeout { get; init; } = DefaultStopTimeout;

    /// <summary>How often the socket is probed while waiting (<see cref="DefaultPollInterval"/>).</summary>
    public TimeSpan PollInterval { get; init; } = DefaultPollInterval;

    /// <summary>The longest pause between restarts (<see cref="DefaultMaxBackoff"/>).</summary>
    public TimeSpan MaxBackoff { get; init; } = DefaultMaxBackoff;

    /// <summary>How many times a daemon was started (for tests).</summary>
    public int Spawns
    {
        get
        {
            lock (gate)
            {
                return spawns;
            }
        }
    }

    /// <summary>Whether a process this supervisor started is alive (daemon.go Running).</summary>
    public bool Running
    {
        get
        {
            lock (gate)
            {
                return process is not null && !process.Exited.IsCompleted;
            }
        }
    }

    /// <summary>
    /// Finds the daemon in this process's environment, beside this app's
    /// executable (<see cref="Locate(string?, IReadOnlyDictionary{string, string?})"/>).
    /// </summary>
    public static string? Locate() => Locate(AppContext.BaseDirectory, ProcessEnvironment.Current());

    /// <summary>
    /// Finds the daemon binary: <c>MALACHI_DAEMON</c> (a path; <c>none</c>
    /// or empty disables starting it and returns null), else
    /// <c>malachid.exe</c> in <paramref name="baseDirectory"/> (the app's
    /// folder, or its build output under F5), else on <c>PATH</c>. Throws
    /// <see cref="DaemonSupervisorException"/> (<see cref="DaemonSupervisorFailure.NoDaemon"/>)
    /// when nothing is found.
    /// </summary>
    public static string? Locate(string? baseDirectory, IReadOnlyDictionary<string, string?> environment)
    {
        ArgumentNullException.ThrowIfNull(environment);
        if (ProcessEnvironment.TryGet(environment, DaemonEnv, out var configured))
        {
            return configured.Length == 0 || configured == "none" ? null : configured;
        }
        var candidates = new List<string>();
        if (!string.IsNullOrEmpty(baseDirectory))
        {
            candidates.Add(Path.Combine(baseDirectory, ExecutableName));
        }
        foreach (var entry in (ProcessEnvironment.NonEmpty(environment, "PATH") ?? "").Split(Path.PathSeparator))
        {
            var directory = entry.Trim().Trim('"');
            if (directory.Length > 0 && Path.IsPathFullyQualified(directory))
            {
                candidates.Add(Path.Combine(directory, ExecutableName));
            }
        }
        foreach (var candidate in candidates)
        {
            if (Paths.IsProgram(candidate))
            {
                return candidate;
            }
        }
        throw DaemonSupervisorException.NoDaemon();
    }

    /// <summary>
    /// The daemon's environment: <paramref name="base"/> (the app's), plus
    /// the keyring and D-Bus. There is no Secret Service on Windows, so the
    /// daemon gets the bundled <c>malachi-credentials.exe</c> as its keyring
    /// helper (<c>MALACHI_KEYRING=helper</c>,
    /// <c>MALACHI_KEYRING_HELPER=&lt;absolute path&gt;</c>); without one it
    /// runs with <c>MALACHI_KEYRING=none</c>, where adding an account with a
    /// password fails with keyringError. A <c>MALACHI_KEYRING</c> already in
    /// the environment wins, so a developer can still point the daemon
    /// elsewhere; so does a <c>DBUS_SESSION_BUS_ADDRESS</c>, which is
    /// otherwise <c>disabled:</c>. Unlike Swift's, it adds no
    /// <c>MALACHI_DEFAULT_COMPRESS_STORE</c> or
    /// <c>MALACHI_DEFAULT_ATTACHMENT_OFFLINE_DAYS</c> (the macOS app's 1 and
    /// 30, for a Mac's often small disk): as with the GTK UI, the daemon's
    /// own defaults apply until the user changes the preferences, and values
    /// the environment has pass through (docs/windows-port.md §5).
    /// </summary>
    public static IReadOnlyDictionary<string, string> Environment(IReadOnlyDictionary<string, string?> @base, DaemonLaunch launch)
    {
        ArgumentNullException.ThrowIfNull(@base);
        ArgumentNullException.ThrowIfNull(launch);
        var env = ProcessEnvironment.Copy(@base);
        env.TryAdd(DBusEnv, "disabled:");
        if (env.ContainsKey(KeyringEnv))
        {
            return env;
        }
        if (launch.KeyringHelper is { } helper && Paths.IsProgram(helper))
        {
            env[KeyringEnv] = "helper";
            env[KeyringHelperEnv] = Path.GetFullPath(helper);
        }
        else
        {
            env[KeyringEnv] = "none";
            env.Remove(KeyringHelperEnv);
        }
        return env;
    }

    /// <summary>
    /// True when something listens on <paramref name="socket"/> (the probe
    /// of daemon.go <c>answers</c> and UnixSocketProbe.answers). A missing
    /// or dead socket is refused at once; a listener whose backlog is full
    /// exists and counts, as does one that takes longer than half a second
    /// (Swift's rule; Go's counts only a completed connect). The half second
    /// is on the real clock.
    /// </summary>
    public static async ValueTask<bool> AnswersAsync(string socket, CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(socket);
        if (socket.Length == 0 || Encoding.UTF8.GetByteCount(socket) > Paths.MaxSocketPathBytes)
        {
            return false;
        }
        UnixDomainSocketEndPoint endpoint;
        try
        {
            endpoint = new UnixDomainSocketEndPoint(socket);
        }
        catch (ArgumentException)
        {
            return false;
        }
        using var probe = new Socket(AddressFamily.Unix, SocketType.Stream, ProtocolType.Unspecified);
        using var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        timeout.CancelAfter(ProbeTimeout);
        try
        {
            await probe.ConnectAsync(endpoint, timeout.Token).ConfigureAwait(false);
            return true;
        }
        catch (OperationCanceledException) when (!cancellationToken.IsCancellationRequested)
        {
            return true; // busy, but there
        }
        catch (SocketException e)
        {
            return e.SocketErrorCode is SocketError.WouldBlock or SocketError.IOPending or SocketError.InProgress
                or SocketError.NoBufferSpaceAvailable or SocketError.TimedOut;
        }
    }

    /// <summary>
    /// Makes sure a daemon answers on the socket: adopts a running one, or
    /// starts ours and waits for its socket. Returns when the socket
    /// answers; throws <see cref="DaemonSupervisorException"/> otherwise.
    /// A daemon that exits before answering counts as a failure, and the
    /// next start is delayed (<see cref="DaemonSupervisorFailure.Backoff"/>,
    /// at once). Returns to the caller before any of the work (the probe,
    /// <see cref="BeforeStart"/>, the start), which runs on the thread pool
    /// as a call into the Swift actor runs off the main actor.
    /// </summary>
    public async Task EnsureAsync(CancellationToken cancellationToken = default)
    {
        await Task.CompletedTask.ConfigureAwait(ConfigureAwaitOptions.ForceYielding);
        await ensureGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            await EnsureLockedAsync(cancellationToken).ConfigureAwait(false);
        }
        finally
        {
            ensureGate.Release();
        }
    }

    /// <summary>
    /// Marks the supervisor as stopping without touching the process:
    /// nothing is started any more. The console handler calls it first on
    /// CTRL_CLOSE, which the daemon gets as well, so that its exit is not
    /// taken for a crash and restarted; <see cref="StopAsync"/> follows.
    /// </summary>
    public void BeginStopping()
    {
        lock (gate)
        {
            stopping = true;
        }
    }

    /// <summary>
    /// Stops the daemon this supervisor started: a clean stop request, up
    /// to <see cref="StopTimeout"/>, then a kill. Somebody else's daemon is
    /// left alone. Nothing is started afterwards. Like
    /// <see cref="EnsureAsync"/>, it returns to the caller before the stop
    /// request (which moves between consoles on Windows) is made.
    /// </summary>
    public async Task StopAsync()
    {
        await Task.CompletedTask.ConfigureAwait(ConfigureAwaitOptions.ForceYielding);
        IDaemonProcess? running;
        lock (gate)
        {
            stopping = true;
            running = process is { Exited.IsCompleted: false } ? process : null;
        }
        if (running is null)
        {
            return;
        }
        LogStopping(Logger, running.Id);
        if (!running.RequestStop())
        {
            LogStopNotDelivered(Logger, running.Id);
        }
        try
        {
            await running.Exited.WaitAsync(StopTimeout, Time).ConfigureAwait(false);
        }
        catch (TimeoutException)
        {
            LogKilling(Logger, running.Id, StopTimeout);
            running.Kill();
            await running.Exited.ConfigureAwait(false);
        }
    }

    /// <summary>
    /// Releases what is held for a daemon that has exited; one that still
    /// runs is left running. Call it after <see cref="StopAsync"/>.
    /// </summary>
    public void Dispose()
    {
        IDaemonProcess? done = null;
        lock (gate)
        {
            if (process is { Exited.IsCompleted: true })
            {
                done = process;
                process = null;
            }
        }
        done?.Dispose();
        ensureGate.Dispose();
    }

    private async Task EnsureLockedAsync(CancellationToken cancellationToken)
    {
        if (await Probe(socket, cancellationToken).ConfigureAwait(false))
        {
            return;
        }
        if (launch is null)
        {
            throw DaemonSupervisorException.NoDaemon();
        }
        lock (gate)
        {
            if (stopping)
            {
                throw DaemonSupervisorException.Stopping();
            }
            // One started earlier and still coming up may just be slow: wait
            // for it. One that exited is a failure.
            if (process is { Exited.IsCompleted: true })
            {
                NoteExit();
            }
            if (process is null)
            {
                if (nextTry is { } next && Time.GetTimestamp() < next)
                {
                    throw DaemonSupervisorException.Backoff(failures, Time.GetElapsedTime(Time.GetTimestamp(), next));
                }
                Spawn(launch);
            }
        }
        await AwaitSocketAsync(cancellationToken).ConfigureAwait(false);
    }

    // Called with gate held.
    private void Spawn(DaemonLaunch l)
    {
        IDaemonProcess started;
        try
        {
            BeforeStart?.Invoke();
            started = host!.Start(new DaemonStartInfo
            {
                Executable = l.Executable,
                Arguments = ["--socket", l.Socket, "--config", l.Config, "--store", l.Store],
                Environment = Environment(BaseEnvironment ?? ProcessEnvironment.Current(), l),
            });
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            failures++;
            nextTry = Time.GetTimestamp() + Ticks(Backoff());
            LogStartFailed(Logger, e.Message);
            throw;
        }
        process = started;
        spawns++;
        LogStarted(Logger, started.Id, socket);
        _ = ReportExitAsync(started);
    }

    // daemon.go spawn's waiter: an exit nobody asked for is an error.
    private async Task ReportExitAsync(IDaemonProcess watched)
    {
        var code = await watched.Exited.ConfigureAwait(false);
        bool quitting;
        lock (gate)
        {
            quitting = stopping;
        }
        if (!quitting)
        {
            LogExited(Logger, watched.Id, ExitStatus.Describe(code));
        }
    }

    private async Task AwaitSocketAsync(CancellationToken cancellationToken)
    {
        var start = Time.GetTimestamp();
        while (true)
        {
            if (await Probe(socket, cancellationToken).ConfigureAwait(false))
            {
                lock (gate)
                {
                    failures = 0;
                    nextTry = null;
                }
                return;
            }
            Task<int> exit;
            lock (gate)
            {
                if (process is null)
                {
                    // Stop's kill was noted by a concurrent caller; nothing
                    // of ours runs any more.
                    throw DaemonSupervisorException.ExitedEarly("exited");
                }
                exit = process.Exited;
                if (exit.IsCompleted)
                {
                    NoteExit();
                    throw DaemonSupervisorException.ExitedEarly(ExitStatus.Describe(exit.Result));
                }
            }
            if (Time.GetElapsedTime(start) >= StartTimeout)
            {
                throw DaemonSupervisorException.StartTimeout(socket, StartTimeout);
            }
            // The next probe after the interval, or at once when it exits.
            await Task.WhenAny(Task.Delay(PollInterval, Time, cancellationToken), exit).ConfigureAwait(false);
            cancellationToken.ThrowIfCancellationRequested();
        }
    }

    // Called with gate held after the process has exited.
    private void NoteExit()
    {
        var gone = process;
        process = null;
        failures++;
        nextTry = Time.GetTimestamp() + Ticks(Backoff());
        gone?.Dispose();
    }

    // A single exit (a crash after hours of running) is retried at once;
    // then 1 s doubling per further consecutive exit, capped. Called with
    // gate held.
    private TimeSpan Backoff()
    {
        if (failures <= 1)
        {
            return TimeSpan.Zero;
        }
        var d = TimeSpan.FromSeconds(1);
        for (var i = 2; i < failures && d < MaxBackoff; i++)
        {
            d *= 2;
        }
        return d < MaxBackoff ? d : MaxBackoff;
    }

    private long Ticks(TimeSpan span) => (long)(span.TotalSeconds * Time.TimestampFrequency);

    [LoggerMessage(Level = LogLevel.Information, Message = "started malachid (pid {Pid}) on {Socket}")]
    private static partial void LogStarted(ILogger logger, int pid, string socket);

    [LoggerMessage(Level = LogLevel.Error, Message = "could not start malachid: {Reason}")]
    private static partial void LogStartFailed(ILogger logger, string reason);

    [LoggerMessage(Level = LogLevel.Error, Message = "malachid (pid {Pid}) {Exit}")]
    private static partial void LogExited(ILogger logger, int pid, string exit);

    [LoggerMessage(Level = LogLevel.Information, Message = "stopping malachid (pid {Pid})")]
    private static partial void LogStopping(ILogger logger, int pid);

    [LoggerMessage(Level = LogLevel.Warning, Message = "could not ask malachid (pid {Pid}) to stop")]
    private static partial void LogStopNotDelivered(ILogger logger, int pid);

    [LoggerMessage(Level = LogLevel.Warning, Message = "malachid (pid {Pid}) did not exit within {Timeout}; killing it")]
    private static partial void LogKilling(ILogger logger, int pid, TimeSpan timeout);
}
