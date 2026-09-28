// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only tests of the process host of the supervisor
// (docs/windows-port.md §5, §12): the real malachid stopped cleanly with
// CTRL_BREAK in a private run directory (the log's "shutting down", exit 0,
// the socket and the key removed), the stand-in daemon stopped the same way
// and killed when it ignores the request, no stop request to one that has
// exited while its output drains, what the daemon gets (its
// arguments exactly, its environment, NUL as stdin, no handle of the app
// but its output pipe), the spawn gate its start holds against the
// bridge's, and what comes back (UTF-8 lines, the exit code after the last
// of them). The real daemon is MALACHI_TEST_MALACHID, else
// build\malachid.exe of the repository (make windows, build.ps1 go); the
// tests that need it are skipped without. The stop's path here is the one
// of the test process (a console or none); the console cases are
// ConsoleAttachmentTests.

using System;
using System.Collections;
using System.Collections.Generic;
using System.ComponentModel;
using System.Diagnostics;
using System.IO;
using System.IO.Pipes;
using System.Linq;
using System.Security.AccessControl;
using System.Security.Principal;
using System.Text;
using System.Text.Encodings.Web;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Daemon;
using Malachi.Core.Platform;
using Malachi.Core.TestDaemon;
using Malachi.Platform.Windows.Files;
using Malachi.Platform.Windows.Processes;
using Malachi.Platform.Windows.Tests.Files;
using Malachi.Platform.Windows.Tests.Transport;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Processes;

public sealed class DaemonProcessHostTests
{
    /// <summary>The real daemon, or a skip when it is not built.</summary>
    internal static string RealDaemonOrSkip()
    {
        var configured = Environment.GetEnvironmentVariable("MALACHI_TEST_MALACHID");
        if (!string.IsNullOrEmpty(configured))
        {
            Assert.True(File.Exists(configured), "MALACHI_TEST_MALACHID names no file: " + configured);
            return configured;
        }
        for (var dir = new DirectoryInfo(AppContext.BaseDirectory); dir is not null; dir = dir.Parent)
        {
            if (File.Exists(Path.Combine(dir.FullName, "go.work")))
            {
                var built = Path.Combine(dir.FullName, "build", "malachid.exe");
                if (File.Exists(built))
                {
                    return built;
                }
                break;
            }
        }
        Assert.Skip("malachid.exe is not built (make windows or build.ps1 go), and MALACHI_TEST_MALACHID names none");
        return "";
    }

    [Fact]
    public async Task TheRealDaemonStopsCleanlyInAPrivateRunDirectory()
    {
        var malachid = RealDaemonOrSkip();
        using var dir = new TestDirectory();
        var run = dir.Combine("run");
        var socket = Path.Combine(run, "rpc.sock");
        var directories = new PrivateDirectory();
        using var log = new RotatingLogFile(dir.Combine("logs", "malachid.log"));
        var host = new RecordingHost(new DaemonProcessHost(log: log));
        // The real daemon has the tests' limit to start (RealDaemon), not
        // the app's.
        using var supervisor = new DaemonSupervisor(Launch(malachid, dir, socket), socket, host)
        {
            BaseEnvironment = EnvironmentWith(("MALACHI_KEYRING", "none")),
            BeforeStart = () => directories.Ensure(run),
            StartTimeout = RealDaemon.StartLimit,
        };
        try
        {
            await supervisor.EnsureAsync(TestContext.Current.CancellationToken);
            Assert.True(Answers(socket));
            Assert.True(directories.IsPrivate(run), "the run directory is private");
            // The socket accepts as soon as it is bound, before malachid
            // writes the key beside it (rpc.Server.Listen); it logs
            // "listening" once the key is in place.
            await WaitForLineAsync(log, line => line.Contains("msg=listening", StringComparison.Ordinal));
            Assert.True(File.Exists(socket + ".key"), "the daemon wrote its key beside the socket");
            AssertOnlyUserAndSystem(socket + ".key");

            var clock = Stopwatch.StartNew();
            await supervisor.StopAsync();
            var daemon = host.Last!;
            Assert.True(clock.Elapsed < TimeSpan.FromSeconds(5), $"the stop took {clock.Elapsed}");
            Assert.Equal(0, await daemon.Exited);
            Assert.NotEqual(DaemonStopPath.Failed, daemon.LastStopPath);
        }
        finally
        {
            await supervisor.StopAsync();
        }
        Assert.Contains(LogLines(log), line => line.Contains("shutting down", StringComparison.Ordinal));
        Assert.False(File.Exists(socket), "the socket was left");
        Assert.False(File.Exists(socket + ".key"), "the key file was left");
    }

    [Fact]
    public async Task TheStandInStopsOnCtrlBreakAndSaysWhatItGot()
    {
        using var dir = new TestDirectory();
        var socket = dir.Combine("rpc.sock");
        using var log = new RotatingLogFile(dir.Combine("logs", "malachid.log"));
        var host = new RecordingHost(new DaemonProcessHost(log: log));
        var launch = Launch(TestDaemonSettings.ExecutablePath, dir, socket);
        using var supervisor = new DaemonSupervisor(launch, socket, host)
        {
            BaseEnvironment = EnvironmentWith((TestDaemonSettings.ModeEnv, TestDaemonSettings.Listen)),
        };
        try
        {
            await supervisor.EnsureAsync(TestContext.Current.CancellationToken);
            await supervisor.StopAsync();
        }
        finally
        {
            await supervisor.StopAsync();
        }
        var daemon = host.Last!;
        Assert.Equal(0, await daemon.Exited);
        Assert.NotEqual(DaemonStopPath.Failed, daemon.LastStopPath);
        Assert.False(File.Exists(socket));
        Assert.False(File.Exists(socket + ".key"));
        var lines = LogLines(log);
        Assert.Contains(TestDaemonSettings.ShuttingDown + " reason=SIGQUIT", lines);
        Assert.Contains("testdaemon: listening on " + socket + " (žluťoučký kůň)", lines);
        Assert.Contains("testdaemon: stdin closed", lines);
        Assert.Contains(
            "testdaemon: mode=listen pid=" + daemon.Id + " args=" + Json(["--socket", socket, "--config", launch.Config, "--store", launch.Store]),
            lines);
        Assert.Contains("testdaemon: env MALACHI_KEYRING=none MALACHI_KEYRING_HELPER=<unset> DBUS_SESSION_BUS_ADDRESS=disabled:", lines);
    }

    [Fact]
    public async Task ADeafDaemonIsKilledAfterTheStopTimeout()
    {
        using var dir = new TestDirectory();
        var socket = dir.Combine("rpc.sock");
        using var log = new RotatingLogFile(dir.Combine("logs", "malachid.log"));
        var host = new RecordingHost(new DaemonProcessHost(log: log));
        using var supervisor = new DaemonSupervisor(Launch(TestDaemonSettings.ExecutablePath, dir, socket), socket, host)
        {
            BaseEnvironment = EnvironmentWith((TestDaemonSettings.ModeEnv, TestDaemonSettings.Deaf)),
            StopTimeout = TimeSpan.FromMilliseconds(500),
        };
        await supervisor.EnsureAsync(TestContext.Current.CancellationToken);
        var clock = Stopwatch.StartNew();
        await supervisor.StopAsync();
        Assert.True(clock.Elapsed < TimeSpan.FromSeconds(5), $"the stop took {clock.Elapsed}");
        Assert.False(supervisor.Running, "the daemon survived the stop");
        var daemon = host.Last!;
        Assert.Equal(ExitStatus.Killed, await daemon.Exited);
        Assert.NotEqual(DaemonStopPath.Failed, daemon.LastStopPath);
        Assert.Contains("testdaemon: got SIGQUIT, ignored", LogLines(log));
    }

    [Fact]
    public async Task NoHandleOfTheAppIsInheritedButTheOutputPipe()
    {
        using var dir = new TestDirectory();
        // An inheritable handle of the app: Process.Start would hand it to
        // every child (the launcher's pipe of the measured hang).
        using var probe = new AnonymousPipeServerStream(PipeDirection.In, HandleInheritability.Inheritable);
        var value = probe.ClientSafePipeHandle.DangerousGetHandle().ToInt64().ToString(System.Globalization.CultureInfo.InvariantCulture);

        // The probe works: a child of Process.Start gets the handle.
        var leaky = new ProcessStartInfo(TestDaemonSettings.ExecutablePath)
        {
            UseShellExecute = false,
            CreateNoWindow = true,
            RedirectStandardInput = true,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
        };
        leaky.Environment[TestDaemonSettings.ModeEnv] = TestDaemonSettings.Exit;
        leaky.Environment[TestDaemonSettings.ExitCodeEnv] = "0";
        leaky.Environment[TestDaemonSettings.ProbeHandleEnv] = value;
        using (var child = Process.Start(leaky)!)
        {
            await child.WaitForExitAsync(TestContext.Current.CancellationToken);
        }

        // The daemon's host does not hand it on.
        using var log = new RotatingLogFile(dir.Combine("malachid.log"));
        var host = new DaemonProcessHost(log: log);
        var env = EnvironmentWith((TestDaemonSettings.ModeEnv, TestDaemonSettings.Exit), (TestDaemonSettings.ExitCodeEnv, "0"), (TestDaemonSettings.ProbeHandleEnv, value));
        using (var daemon = host.Start(new DaemonStartInfo { Executable = TestDaemonSettings.ExecutablePath, Arguments = [], Environment = Strict(env) }))
        {
            Assert.Equal(0, await daemon.Exited.WaitAsync(TimeSpan.FromSeconds(15), TestContext.Current.CancellationToken));
        }

        probe.DisposeLocalCopyOfClientHandle();
        var received = new StreamReader(probe, Encoding.UTF8).ReadToEnd();
        Assert.Equal(TestDaemonSettings.ProbeMarker, received);
        Assert.Contains(LogLines(log), line => line.StartsWith("testdaemon: probe handle " + value + " refused", StringComparison.Ordinal)
            || line == "testdaemon: probe handle " + value + " written");
    }

    [Fact]
    public async Task TheStartHoldsTheSpawnGate()
    {
        // NUL and the output pipe are inheritable while the daemon starts,
        // and the bridge's Process.Start (BridgeRunner) would hand them to
        // the bridge: the start waits for the gate the bridge's start
        // takes too, and lets it go after a start and after a failed one.
        using var dir = new TestDirectory();
        using var log = new RotatingLogFile(dir.Combine("malachid.log"));
        var host = new DaemonProcessHost(log: log);
        var start = new DaemonStartInfo
        {
            Executable = TestDaemonSettings.ExecutablePath,
            Arguments = [],
            Environment = Strict(EnvironmentWith((TestDaemonSettings.ModeEnv, TestDaemonSettings.Exit), (TestDaemonSettings.ExitCodeEnv, "0"))),
        };
        var cancellationToken = TestContext.Current.CancellationToken;
        using var held = new ManualResetEventSlim();
        using var release = new ManualResetEventSlim();
        var holder = new Thread(() =>
        {
            using (SpawnGate.Enter())
            {
                held.Set();
                release.Wait();
            }
        });
        holder.Start();
        Task<IDaemonProcess> started;
        try
        {
            held.Wait(cancellationToken);
            started = Task.Run(() => host.Start(start), cancellationToken);
            await Task.Delay(300, cancellationToken);
            Assert.False(started.IsCompleted, "the daemon started while the gate was held");
        }
        finally
        {
            release.Set();
            holder.Join();
        }
        using (var daemon = await started.WaitAsync(TimeSpan.FromSeconds(15), cancellationToken))
        {
            Assert.Equal(0, await daemon.Exited.WaitAsync(TimeSpan.FromSeconds(15), cancellationToken));
        }

        using (var daemon = host.Start(start))
        {
            Assert.False(SpawnGate.IsHeldByCurrentThread);
            Assert.Equal(0, await daemon.Exited.WaitAsync(TimeSpan.FromSeconds(15), cancellationToken));
        }
        Assert.Throws<Win32Exception>(() => host.Start(new DaemonStartInfo
        {
            Executable = dir.Combine("malachid.exe"),
            Arguments = [],
            Environment = new Dictionary<string, string>(),
        }));
        Assert.False(SpawnGate.IsHeldByCurrentThread);
    }

    [Fact]
    public async Task NoStopRequestGoesToADaemonThatHasExitedWhileItsOutputDrains()
    {
        // The exit is reported once the output has drained, or DrainGrace
        // after the exit; a program the daemon started can hold the pipe
        // that long. A stop in between (the app quitting right after the
        // daemon stopped by itself on CTRL_CLOSE) must not move an attached
        // app off its terminal's console for a process that is gone.
        using var dir = new TestDirectory();
        using var log = new RotatingLogFile(dir.Combine("malachid.log"));
        var host = new DaemonProcessHost(log: log);
        var env = EnvironmentWith(
            (TestDaemonSettings.ModeEnv, TestDaemonSettings.Exit),
            (TestDaemonSettings.ExitCodeEnv, "0"),
            (TestDaemonSettings.HoldOutputEnv, "4000"));
        using var daemon = (DaemonProcess)host.Start(new DaemonStartInfo
        {
            Executable = TestDaemonSettings.ExecutablePath,
            Arguments = [],
            Environment = Strict(env),
        });
        try
        {
            var clock = Stopwatch.StartNew();
            while (!daemon.HasExited && clock.Elapsed < TimeSpan.FromSeconds(15))
            {
                await Task.Delay(10, TestContext.Current.CancellationToken);
            }
            Assert.True(daemon.HasExited, "the stand-in did not exit");
            Assert.False(daemon.Exited.IsCompleted, "the exit waits for the last lines");
            Assert.False(daemon.RequestStop(), "no stop request goes to a process that has exited");
            Assert.Equal(DaemonStopPath.Exited, daemon.LastStopPath);
            Assert.Equal(0, await daemon.Exited.WaitAsync(TimeSpan.FromSeconds(15), TestContext.Current.CancellationToken));
        }
        finally
        {
            await Task.WhenAny(daemon.Exited, Task.Delay(TimeSpan.FromSeconds(15), CancellationToken.None));
            EndHolder(log);
        }
    }

    [Theory]
    [InlineData("plain")]
    [InlineData("two words")]
    [InlineData("")]
    [InlineData("quote\"inside")]
    [InlineData("\"quoted\"")]
    [InlineData(@"back\slash")]
    [InlineData(@"trailing\")]
    [InlineData(@"trailing\\")]
    [InlineData(@"before\""quote")]
    [InlineData("tab\there")]
    [InlineData("new\nline")]
    [InlineData("C:\\Users\\Vladislav Janeček\\AppData\\Local\\Malachi Mail\\store.db")]
    [InlineData("--socket=a b")]
    public async Task EveryArgumentArrivesAsItWasGiven(string argument)
    {
        using var dir = new TestDirectory();
        using var log = new RotatingLogFile(dir.Combine("malachid.log"));
        var host = new DaemonProcessHost(log: log);
        string[] arguments = ["--socket", dir.Combine("rpc.sock"), argument, "last"];
        var env = EnvironmentWith((TestDaemonSettings.ModeEnv, TestDaemonSettings.Exit), (TestDaemonSettings.ExitCodeEnv, "7"));
        using var daemon = host.Start(new DaemonStartInfo
        {
            Executable = TestDaemonSettings.ExecutablePath,
            Arguments = arguments,
            Environment = Strict(env),
        });
        // The exit is reported after the last line was passed on.
        Assert.Equal(7, await daemon.Exited.WaitAsync(TimeSpan.FromSeconds(15), TestContext.Current.CancellationToken));
        Assert.Contains("testdaemon: mode=exit pid=" + daemon.Id + " args=" + Json(arguments), LogLines(log));
    }

    [Fact]
    public async Task KillEndsTheDaemonWithMinusOne()
    {
        using var dir = new TestDirectory();
        var socket = dir.Combine("rpc.sock");
        var host = new DaemonProcessHost();
        using var daemon = host.Start(new DaemonStartInfo
        {
            Executable = TestDaemonSettings.ExecutablePath,
            Arguments = ["--socket", socket],
            Environment = Strict(EnvironmentWith((TestDaemonSettings.ModeEnv, TestDaemonSettings.Deaf))),
        });
        Assert.False(daemon.Exited.IsCompleted);
        daemon.Kill();
        Assert.Equal(ExitStatus.Killed, await daemon.Exited.WaitAsync(TimeSpan.FromSeconds(15), TestContext.Current.CancellationToken));
        Assert.False(daemon.RequestStop(), "no stop request goes to a process that has exited");
        daemon.Kill(); // nothing happens
    }

    [Fact]
    public void AMissingExecutableFailsWithTheSystemsReason()
    {
        using var dir = new TestDirectory();
        var missing = dir.Combine("malachid.exe");
        var e = Assert.Throws<Win32Exception>(() => new DaemonProcessHost().Start(new DaemonStartInfo
        {
            Executable = missing,
            Arguments = [],
            Environment = new Dictionary<string, string>(),
        }));
        Assert.Equal(2, e.NativeErrorCode); // ERROR_FILE_NOT_FOUND
        Assert.StartsWith("start " + missing + ": ", e.Message, StringComparison.Ordinal);
    }

    [Theory]
    [InlineData(new string[0], "\"C:\\app\\malachid.exe\"")]
    [InlineData(new[] { "--socket", @"C:\Users\u\.cache\malachi\run\rpc.sock" }, "\"C:\\app\\malachid.exe\" --socket C:\\Users\\u\\.cache\\malachi\\run\\rpc.sock")]
    [InlineData(new[] { "--store", @"C:\Users\u\AppData\Local\Malachi Mail\store.db" }, "\"C:\\app\\malachid.exe\" --store \"C:\\Users\\u\\AppData\\Local\\Malachi Mail\\store.db\"")]
    [InlineData(new[] { "" }, "\"C:\\app\\malachid.exe\" \"\"")]
    [InlineData(new[] { "a\"b" }, "\"C:\\app\\malachid.exe\" \"a\\\"b\"")]
    [InlineData(new[] { @"x\ y\" }, "\"C:\\app\\malachid.exe\" \"x\\ y\\\\\"")]
    [InlineData(new[] { @"a\\""b" }, "\"C:\\app\\malachid.exe\" \"a\\\\\\\\\\\"b\"")]
    public void TheCommandLineQuotesOnlyWhatNeedsIt(string[] arguments, string expected)
    {
        Assert.Equal(expected, ChildProcess.CommandLine(@"C:\app\malachid.exe", arguments));
    }

    [Fact]
    public void TheEnvironmentBlockIsSortedAndTerminated()
    {
        var env = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase)
        {
            ["zeta"] = "1",
            ["Path"] = @"C:\Windows",
            ["ALPHA"] = "a=b",
            ["=C:"] = @"C:\work",
            ["bad=name"] = "x",
            [""] = "empty",
            ["nul"] = "a\0b",
        };
        Assert.Equal("=C:=C:\\work\0ALPHA=a=b\0Path=C:\\Windows\0zeta=1\0\0", ChildProcess.EnvironmentBlock(env));
        Assert.Equal("\0\0", ChildProcess.EnvironmentBlock(new Dictionary<string, string>()));
    }

    private static DaemonLaunch Launch(string executable, TestDirectory dir, string socket) => new()
    {
        Executable = executable,
        Socket = socket,
        Config = dir.Combine("config.toml"),
        Store = dir.Combine("store.db"),
    };

    private static Dictionary<string, string?> EnvironmentWith(params (string Name, string Value)[] extra)
    {
        var env = new Dictionary<string, string?>(StringComparer.OrdinalIgnoreCase);
        foreach (DictionaryEntry entry in System.Environment.GetEnvironmentVariables())
        {
            env[(string)entry.Key] = (string?)entry.Value;
        }
        env.Remove("MALACHI_KEYRING");
        env.Remove("DBUS_SESSION_BUS_ADDRESS");
        foreach (var (name, value) in extra)
        {
            env[name] = value;
        }
        return env;
    }

    private static Dictionary<string, string> Strict(Dictionary<string, string?> env) =>
        env.Where(e => e.Value is not null).ToDictionary(e => e.Key, e => e.Value!, StringComparer.OrdinalIgnoreCase);

    // The log as somebody following it reads it: it is still open for writing.
    private static string[] LogLines(RotatingLogFile log)
    {
        using var stream = new FileStream(log.FilePath, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete);
        using var reader = new StreamReader(stream, Encoding.UTF8);
        return reader.ReadToEnd().Split('\n', StringSplitOptions.RemoveEmptyEntries).Select(line => line.TrimEnd('\r')).ToArray();
    }

    // Waits for a line of the log (the daemon's output reaches it through
    // the pump thread), one the real daemon writes while it starts: it has
    // the tests' limit for that (RealDaemon).
    private static async Task WaitForLineAsync(RotatingLogFile log, Func<string, bool> match)
    {
        var clock = Stopwatch.StartNew();
        while (!(File.Exists(log.FilePath) && LogLines(log).Any(match)))
        {
            Assert.True(clock.Elapsed < RealDaemon.StartLimit, "the line did not come: " + string.Join(" | ", File.Exists(log.FilePath) ? LogLines(log) : []));
            await Task.Delay(20, TestContext.Current.CancellationToken);
        }
    }

    // Ends the copy of the stand-in that held its output, if it still runs.
    private static void EndHolder(RotatingLogFile log)
    {
        if (!File.Exists(log.FilePath))
        {
            return;
        }
        foreach (var line in LogLines(log).Where(l => l.StartsWith(TestDaemonSettings.HolderLine, StringComparison.Ordinal)))
        {
            var pid = int.Parse(line.AsSpan(TestDaemonSettings.HolderLine.Length), System.Globalization.CultureInfo.InvariantCulture);
            try
            {
                using var holder = Process.GetProcessById(pid);
                if (holder.ProcessName == Path.GetFileNameWithoutExtension(TestDaemonSettings.ExecutablePath))
                {
                    holder.Kill();
                    holder.WaitForExit(TimeSpan.FromSeconds(5));
                }
            }
            catch (Exception e) when (e is ArgumentException or InvalidOperationException or Win32Exception)
            {
                // Gone already.
            }
        }
    }

    private static bool Answers(string socket) =>
        DaemonSupervisor.AnswersAsync(socket, CancellationToken.None).AsTask().GetAwaiter().GetResult();

    private static string Json(string[] arguments)
    {
        using var buffer = new MemoryStream();
        using (var writer = new Utf8JsonWriter(buffer, new JsonWriterOptions { Encoder = JavaScriptEncoder.UnsafeRelaxedJsonEscaping }))
        {
            writer.WriteStartArray();
            foreach (var argument in arguments)
            {
                writer.WriteStringValue(argument);
            }
            writer.WriteEndArray();
        }
        return Encoding.UTF8.GetString(buffer.ToArray());
    }

    // The key inherits the run directory's DACL: the user and SYSTEM, nobody else.
    private static void AssertOnlyUserAndSystem(string path)
    {
        using var identity = WindowsIdentity.GetCurrent();
        var allowed = new[] { identity.User!, new SecurityIdentifier(WellKnownSidType.LocalSystemSid, null) };
        var rules = new FileInfo(path).GetAccessControl().GetAccessRules(true, true, typeof(SecurityIdentifier));
        Assert.NotEmpty(rules.Cast<FileSystemAccessRule>());
        foreach (FileSystemAccessRule rule in rules)
        {
            Assert.Contains((SecurityIdentifier)rule.IdentityReference, allowed);
        }
    }

    // Keeps the process of the last start, for the exit code and the stop path.
    private sealed class RecordingHost(IDaemonProcessHost inner) : IDaemonProcessHost
    {
        public DaemonProcess? Last { get; private set; }

        public IDaemonProcess Start(DaemonStartInfo startInfo)
        {
            var process = inner.Start(startInfo);
            Last = (DaemonProcess)process;
            return process;
        }
    }
}
