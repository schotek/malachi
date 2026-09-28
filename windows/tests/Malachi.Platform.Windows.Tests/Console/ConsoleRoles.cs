// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The console cases of docs/windows-port.md §5 (INPUT-SPIKES.md §4) need
// processes whose consoles differ from the test's, so the test binary
// doubles as them, as Go's test binaries double as the daemon and the
// bridge (daemon_test.go, mcpsetup_test.go TestMain): with
// MALACHI_TEST_CONSOLE_ROLE set, a module initializer plays the role
// before the test platform starts, and exits.
//
// "terminal" stands in for the terminal: a process with a console of its
// own (the test starts it without a window), or without one at all when it
// frees it first (the Start menu). It starts "app" as a terminal starts a
// program: on its console, the standard handles not inherited (as the
// measured runs found them from a shell), or piped to it (a launcher that
// captures). It can type Ctrl+C once the app is ready. "app" plays
// MalachiMail.exe: it starts without a console (a WinUI app), makes the
// console attachment of Main, starts the daemon through DaemonProcessHost,
// waits for its socket, stops it, and reports what happened as JSON.

using System;
using System.Collections;
using System.Collections.Concurrent;
using System.Collections.Generic;
using System.Diagnostics;
using System.Globalization;
using System.IO;
using System.Linq;
using System.Runtime.CompilerServices;
using System.Text;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Daemon;
using Malachi.Core.TestDaemon;
using Malachi.Platform.Windows.Consoles;
using Malachi.Platform.Windows.Processes;
using Malachi.Platform.Windows.Tests.Transport;
using Windows.Win32;
using Windows.Win32.Foundation;
using Windows.Win32.System.Console;

namespace Malachi.Platform.Windows.Tests.Consoles;

/// <summary>The roles the test binary plays for the console tests.</summary>
internal static class ConsoleRoles
{
    /// <summary>Which role: "terminal" or "app".</summary>
    public const string RoleEnv = "MALACHI_TEST_CONSOLE_ROLE";

    /// <summary>Where the app writes its report; the scenario's files lie beside it.</summary>
    public const string ReportEnv = "MALACHI_TEST_CONSOLE_REPORT";

    /// <summary>The daemon's socket.</summary>
    public const string SocketEnv = "MALACHI_TEST_CONSOLE_SOCKET";

    /// <summary>The daemon to start: malachid.exe, or the stand-in when unset.</summary>
    public const string DaemonEnv = "MALACHI_TEST_CONSOLE_DAEMON";

    /// <summary>"1": the terminal has no console (it frees its own first).</summary>
    public const string NoConsoleEnv = "MALACHI_TEST_CONSOLE_NONE";

    /// <summary>"1": the terminal pipes the app's stdout and stderr and keeps what arrives.</summary>
    public const string CaptureEnv = "MALACHI_TEST_CONSOLE_CAPTURE";

    /// <summary>"1": the terminal types Ctrl+C once the app is ready.</summary>
    public const string CtrlCEnv = "MALACHI_TEST_CONSOLE_CTRL_C";

    /// <summary>"1": the daemon gets a console of its own even though the app has one.</summary>
    public const string OwnConsoleEnv = "MALACHI_TEST_CONSOLE_OWN";

    private const string ParentEnv = "MALACHI_TEST_CONSOLE_PARENT";

    // How long the app waits for its daemon's socket: the tests' limit for
    // the real daemon (RealDaemon), which the stand-in never comes near.
    // The waits around it grow with it, so that the innermost one fails
    // first and says why.
    private static readonly TimeSpan SocketLimit = RealDaemon.StartLimit;

    // How long the terminal waits for the app to be ready for its Ctrl+C
    // (the app's start and its daemon's socket), and for the app to exit
    // (then also the Ctrl+C, the stop and a kill); how long a test waits
    // for the terminal.
    private static readonly TimeSpan ReadyLimit = SocketLimit + TimeSpan.FromSeconds(30);
    private static readonly TimeSpan AppLimit = SocketLimit + TimeSpan.FromSeconds(60);
    private static readonly TimeSpan ScenarioLimit = AppLimit + TimeSpan.FromSeconds(30);

    /// <summary>The test binary, which plays the roles.</summary>
    public static string TestExecutable => Path.Combine(AppContext.BaseDirectory, "Malachi.Platform.Windows.Tests.exe");

    // The role runs inside the test assembly's module initializer, before
    // the test platform's Main. Code of this assembly called on another
    // thread meanwhile can wait for the initializer: a DataReceived handler
    // here hung the terminal for ever. So everything that runs elsewhere (the
    // console handler, the output pump, the stream readers) is the
    // library's or Malachi.Platform.Windows's.
    [ModuleInitializer]
    internal static void PlayRole()
    {
        var role = Environment.GetEnvironmentVariable(RoleEnv);
        if (string.IsNullOrEmpty(role))
        {
            return;
        }
        int status;
        try
        {
            status = role switch
            {
                "terminal" => Terminal(),
                "app" => App(),
                _ => 90,
            };
        }
        catch (Exception e)
        {
            File.WriteAllText(Variable(ReportEnv) + ".error", e.ToString());
            status = 91;
        }
        Environment.Exit(status);
    }

    /// <summary>
    /// Runs one scenario in <paramref name="directory"/> (short: the socket
    /// lies there) and returns the app's report; <paramref name="settings"/>
    /// are the variables above.
    /// </summary>
    public static JsonElement Run(string directory, params (string Name, string Value)[] settings)
    {
        var report = Path.Combine(directory, "report.json");
        var start = new ProcessStartInfo(TestExecutable)
        {
            UseShellExecute = false,
            CreateNoWindow = true,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
        };
        start.Environment[RoleEnv] = "terminal";
        start.Environment[ReportEnv] = report;
        start.Environment[SocketEnv] = Path.Combine(directory, "rpc.sock");
        foreach (var (name, value) in settings)
        {
            start.Environment[name] = value;
        }
        using var terminal = Process.Start(start) ?? throw new InvalidOperationException("the terminal did not start");
        var stdout = terminal.StandardOutput.ReadToEndAsync();
        var stderr = terminal.StandardError.ReadToEndAsync();
        if (!terminal.WaitForExit(ScenarioLimit))
        {
            terminal.Kill(entireProcessTree: true);
            throw new TimeoutException("the console scenario did not finish");
        }
        var error = File.Exists(report + ".error") ? File.ReadAllText(report + ".error") : "";
        if (terminal.ExitCode != 0 || !File.Exists(report))
        {
            throw new InvalidOperationException(string.Format(
                CultureInfo.InvariantCulture,
                "the console scenario failed with {0}: {1}{2}{3}",
                terminal.ExitCode,
                error,
                stdout.GetAwaiter().GetResult(),
                stderr.GetAwaiter().GetResult()));
        }
        using var document = JsonDocument.Parse(File.ReadAllBytes(report));
        return document.RootElement.Clone();
    }

    // The terminal: starts the app on its console (or without one), types
    // Ctrl+C when asked, waits for the app.
    private static int Terminal()
    {
        var noConsole = Variable(NoConsoleEnv, "") == "1";
        var capture = Variable(CaptureEnv, "") == "1";
        if (noConsole)
        {
            PInvoke.FreeConsole();
        }
        // A shell hands a program its console, not the pipes this process
        // got from the test.
        foreach (var which in new[] { STD_HANDLE.STD_INPUT_HANDLE, STD_HANDLE.STD_OUTPUT_HANDLE, STD_HANDLE.STD_ERROR_HANDLE })
        {
            var handle = PInvoke.GetStdHandle(which);
            if (handle != HANDLE.Null && (nint)handle != -1)
            {
                PInvoke.SetHandleInformation(handle, (uint)HANDLE_FLAGS.HANDLE_FLAG_INHERIT, 0);
            }
        }
        var start = new ProcessStartInfo(Environment.ProcessPath!)
        {
            UseShellExecute = false,
            CreateNoWindow = noConsole,
            RedirectStandardOutput = capture,
            RedirectStandardError = capture,
        };
        if (capture)
        {
            start.StandardOutputEncoding = new UTF8Encoding(false);
            start.StandardErrorEncoding = new UTF8Encoding(false);
        }
        start.Environment[RoleEnv] = "app";
        start.Environment[ParentEnv] = Environment.ProcessId.ToString(CultureInfo.InvariantCulture);
        using var app = Process.Start(start) ?? throw new InvalidOperationException("the app did not start");
        // Read by the library's own tasks: no code of this assembly may run
        // on another thread while its module initializer, this role, runs.
        var stdout = capture ? app.StandardOutput.ReadToEndAsync() : null;
        var stderr = capture ? app.StandardError.ReadToEndAsync() : null;
        var report = Variable(ReportEnv);
        if (Variable(CtrlCEnv, "") == "1")
        {
            var deadline = Environment.TickCount64 + (long)ReadyLimit.TotalMilliseconds;
            while (!File.Exists(report + ".ready") && !app.HasExited && Environment.TickCount64 < deadline)
            {
                Thread.Sleep(20);
            }
            // Ctrl+C typed into the terminal: every process on the console
            // gets it, except those that ignore it (this one, and a process
            // group of its own).
            PInvoke.SetConsoleCtrlHandler(null, true);
            PInvoke.GenerateConsoleCtrlEvent(PInvoke.CTRL_C_EVENT, 0);
        }
        if (!app.WaitForExit(AppLimit))
        {
            app.Kill(entireProcessTree: true);
            return 3;
        }
        if (stdout is not null && stderr is not null)
        {
            File.WriteAllText(report + ".terminal", stdout.GetAwaiter().GetResult() + stderr.GetAwaiter().GetResult());
        }
        return app.ExitCode;
    }

    // The app: MalachiMail.exe's console handling and daemon.
    private static int App()
    {
        var report = Variable(ReportEnv);
        var directory = Path.GetDirectoryName(report)!;
        var socket = Variable(SocketEnv);
        var parent = uint.Parse(Variable(ParentEnv), CultureInfo.InvariantCulture);
        var result = new Dictionary<string, object?>();

        // A WinUI app has no console of its own.
        PInvoke.FreeConsole();
        // The handler runs on a thread of the system; a queue of the
        // library takes the events (see PlayRole).
        var controls = new ConcurrentQueue<ConsoleControl>();
        var attachment = ConsoleAttachment.Initialize(controls.Enqueue);
        result["attached"] = attachment.IsAttached;
        result["terminal"] = attachment.Terminal is null ? "none" : attachment.Terminal.IsConsole ? "console" : "pipe";
        attachment.Terminal?.WriteLine("app: attached (žluťoučký kůň)");

        using var log = new RotatingLogFile(Path.Combine(directory, "daemon.log"));
        var host = new DaemonProcessHost(attachment, log) { ForceOwnConsole = Variable(OwnConsoleEnv, "") == "1" };
        var environment = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
        foreach (DictionaryEntry entry in Environment.GetEnvironmentVariables())
        {
            environment[(string)entry.Key] = (string)entry.Value!;
        }
        environment["MALACHI_KEYRING"] = "none";
        environment[DaemonSupervisor.DBusEnv] = "disabled:";
        foreach (var name in new[] { RoleEnv, ReportEnv, SocketEnv, ParentEnv, CtrlCEnv, OwnConsoleEnv, CaptureEnv, NoConsoleEnv, DaemonEnv })
        {
            environment.Remove(name);
        }
        var executable = Variable(DaemonEnv, "") is { Length: > 0 } real ? real : TestDaemonSettings.ExecutablePath;
        var daemon = (DaemonProcess)host.Start(new DaemonStartInfo
        {
            Executable = executable,
            Arguments = ["--socket", socket, "--config", Path.Combine(directory, "config.toml"), "--store", Path.Combine(directory, "store.db")],
            Environment = environment,
        });
        try
        {
            Scenario(daemon, attachment, controls, socket, report, parent, result);
        }
        finally
        {
            // Whatever failed, the daemon does not outlive the app: once the
            // app has gone, nothing ties it to the test any more.
            if (!daemon.Exited.IsCompleted)
            {
                daemon.Kill();
                daemon.Exited.Wait(TimeSpan.FromSeconds(15));
            }
            daemon.Dispose();
        }
        attachment.ShutdownCompleted();
        Write(report, result);
        return 0;
    }

    // What the app does with its daemon; the report goes into result.
    private static void Scenario(
        DaemonProcess daemon,
        ConsoleAttachment attachment,
        ConcurrentQueue<ConsoleControl> controls,
        string socket,
        string report,
        uint parent,
        Dictionary<string, object?> result)
    {
        result["daemonPid"] = daemon.Id;
        result["answered"] = WaitForSocket(socket, daemon, SocketLimit);
        result["sharesConsole"] = ConsoleAttachment.ConsoleProcesses().Contains((uint)daemon.Id);

        if (Variable(CtrlCEnv, "") == "1")
        {
            File.WriteAllText(report + ".ready", "");
            var waited = Stopwatch.StartNew();
            ConsoleControl got;
            while (!controls.TryDequeue(out got) && waited.Elapsed < TimeSpan.FromSeconds(20))
            {
                Thread.Sleep(20);
            }
            result["control"] = waited.Elapsed < TimeSpan.FromSeconds(20) ? got.ToString() : "none";
            // The daemon, in its own process group, is spared.
            Thread.Sleep(500);
            result["daemonAliveAfterCtrlC"] = !daemon.Exited.IsCompleted && Answers(socket);
        }

        var clock = Stopwatch.StartNew();
        result["stopDelivered"] = daemon.RequestStop();
        result["stopPath"] = daemon.LastStopPath.ToString();
        var exited = daemon.Exited.Wait(TimeSpan.FromSeconds(15));
        if (!exited)
        {
            daemon.Kill();
            daemon.Exited.Wait();
        }
        result["stopMs"] = clock.ElapsedMilliseconds;
        result["killed"] = !exited;
        result["exit"] = daemon.Exited.Result;
        result["socketLeft"] = File.Exists(socket);
        result["keyLeft"] = File.Exists(socket + ".key");
        // Back on the terminal's console after a stop that left it.
        result["onTerminalConsole"] = ConsoleAttachment.ConsoleProcesses().Contains(parent);
        attachment.Terminal?.WriteLine("app: still writing after the stop");
        result["terminalAfter"] = attachment.Terminal is null ? "none" : attachment.Terminal.IsConsole ? "console" : "pipe";
    }

    private static bool WaitForSocket(string socket, DaemonProcess daemon, TimeSpan limit)
    {
        var deadline = Stopwatch.StartNew();
        while (deadline.Elapsed < limit && !daemon.Exited.IsCompleted)
        {
            if (Answers(socket))
            {
                return true;
            }
            Thread.Sleep(50);
        }
        return false;
    }

    private static bool Answers(string socket) =>
        DaemonSupervisor.AnswersAsync(socket, CancellationToken.None).AsTask().GetAwaiter().GetResult();

    private static void Write(string path, Dictionary<string, object?> result)
    {
        using var buffer = new MemoryStream();
        using (var writer = new Utf8JsonWriter(buffer))
        {
            writer.WriteStartObject();
            foreach (var (name, value) in result)
            {
                switch (value)
                {
                    case bool b:
                        writer.WriteBoolean(name, b);
                        break;
                    case int i:
                        writer.WriteNumber(name, i);
                        break;
                    case long l:
                        writer.WriteNumber(name, l);
                        break;
                    default:
                        writer.WriteString(name, value?.ToString());
                        break;
                }
            }
            writer.WriteEndObject();
        }
        File.WriteAllBytes(path, buffer.ToArray());
    }

    private static string Variable(string name, string? fallback = null) =>
        Environment.GetEnvironmentVariable(name) is { Length: > 0 } value
            ? value
            : fallback ?? throw new InvalidOperationException(name + " is not set");
}
