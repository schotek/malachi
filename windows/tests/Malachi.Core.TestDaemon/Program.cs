// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of fakeDaemon in ui/internal/daemon/daemon_test.go and of the
// stand-in scripts of macos/Tests/MalachiCoreTests/SupervisorTests.swift:
// a program that behaves like malachid as a process, for the supervisor
// and process host tests (docs/windows-port.md §12). It binds the socket
// given as --socket, writes a key file beside it as the daemon does,
// accepts and drops every connection, and on a stop request (CTRL_BREAK or
// CTRL_C on Windows, which .NET reports as SIGQUIT and SIGINT; SIGTERM and
// SIGINT elsewhere; CTRL_CLOSE; the end of stdin when asked) says "shutting
// down", removes the socket and the key and exits 0. What it writes is
// UTF-8, as the daemon's log is, and says what it was given (arguments,
// the keyring and D-Bus variables, whether stdin was closed), so that the
// tests can check what the host passed. Its modes come from the
// environment (TestDaemonSettings).

using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Net.Sockets;
using System.Runtime.InteropServices;
using System.Text;
using System.Text.Encodings.Web;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Microsoft.Win32.SafeHandles;

namespace Malachi.Core.TestDaemon;

internal static class Program
{
    private static readonly Lock WriteGate = new();
    private static readonly Stream StandardOutput = Console.OpenStandardOutput();
    private static readonly Stream StandardError = Console.OpenStandardError();

    private static int Main(string[] args)
    {
        var mode = Environment.GetEnvironmentVariable(TestDaemonSettings.ModeEnv) is { Length: > 0 } m ? m : TestDaemonSettings.Listen;
        var socket = Argument(args, "--socket");
        Out(string.Format(CultureInfo.InvariantCulture, "testdaemon: mode={0} pid={1} args={2}", mode, Environment.ProcessId, Json(args)));
        Out("testdaemon: env " + Variable("MALACHI_KEYRING") + " " + Variable("MALACHI_KEYRING_HELPER") + " "
            + Variable("DBUS_SESSION_BUS_ADDRESS"));
        Probe();

        var stop = new TaskCompletionSource<string>(TaskCreationOptions.RunContinuationsAsynchronously);
        var deaf = mode == TestDaemonSettings.Deaf;
        var registrations = new List<PosixSignalRegistration>();
        foreach (var signal in new[] { PosixSignal.SIGINT, PosixSignal.SIGQUIT, PosixSignal.SIGTERM, PosixSignal.SIGHUP })
        {
            registrations.Add(PosixSignalRegistration.Create(signal, context =>
            {
                context.Cancel = true;
                Err("testdaemon: got " + context.Signal + (deaf ? ", ignored" : ""));
                if (!deaf)
                {
                    stop.TrySetResult(context.Signal.ToString());
                }
            }));
        }
        var stopOnEof = Environment.GetEnvironmentVariable(TestDaemonSettings.StopOnEofEnv) == "1";
        _ = Task.Run(() => WatchStdin(stop, stopOnEof && !deaf));

        switch (mode)
        {
            case TestDaemonSettings.Exit:
                Thread.Sleep(Delay(0));
                return Environment.GetEnvironmentVariable(TestDaemonSettings.ExitCodeEnv) is { Length: > 0 } code
                    ? int.Parse(code, CultureInfo.InvariantCulture)
                    : 1;
            case TestDaemonSettings.Slow:
                Thread.Sleep(Delay(400));
                break;
            case TestDaemonSettings.Listen:
            case TestDaemonSettings.Deaf:
                break;
            default:
                Err("testdaemon: unknown mode " + mode);
                return 5;
        }
        if (socket is null)
        {
            Err("testdaemon: no --socket");
            return 2;
        }
        using var listener = new Socket(AddressFamily.Unix, SocketType.Stream, ProtocolType.Unspecified);
        try
        {
            listener.Bind(new UnixDomainSocketEndPoint(socket));
            listener.Listen(8);
        }
        catch (SocketException e)
        {
            Err("testdaemon: listen " + socket + ": " + e.SocketErrorCode);
            return 2;
        }
        var key = socket + ".key";
        File.WriteAllText(key, new string('0', 64) + "\n");
        Err("testdaemon: listening on " + socket + " (žluťoučký kůň)");
        _ = Task.Run(() => Accept(listener));

        var reason = stop.Task.GetAwaiter().GetResult();
        Err(TestDaemonSettings.ShuttingDown + " reason=" + reason);
        listener.Close();
        TryDelete(socket);
        TryDelete(key);
        foreach (var registration in registrations)
        {
            registration.Dispose();
        }
        return 0;
    }

    private static async Task Accept(Socket listener)
    {
        while (true)
        {
            try
            {
                using var connection = await listener.AcceptAsync().ConfigureAwait(false);
            }
            catch (Exception e) when (e is SocketException or ObjectDisposedException)
            {
                return;
            }
        }
    }

    private static async Task WatchStdin(TaskCompletionSource<string> stop, bool stopOnEof)
    {
        try
        {
            using var stdin = Console.OpenStandardInput();
            var buffer = new byte[256];
            while (await stdin.ReadAsync(buffer).ConfigureAwait(false) > 0)
            {
            }
        }
        catch (Exception e) when (e is IOException or ObjectDisposedException or NotSupportedException)
        {
        }
        Err("testdaemon: stdin closed");
        if (stopOnEof)
        {
            stop.TrySetResult("stdin");
        }
    }

    // Writes the marker to a handle value the parent named: it arrives
    // only when the handle was inherited.
    private static void Probe()
    {
        var value = Environment.GetEnvironmentVariable(TestDaemonSettings.ProbeHandleEnv);
        if (string.IsNullOrEmpty(value) || !OperatingSystem.IsWindows())
        {
            return;
        }
        string outcome;
        try
        {
            using var handle = new SafeFileHandle((nint)long.Parse(value, CultureInfo.InvariantCulture), ownsHandle: false);
            using var stream = new FileStream(handle, FileAccess.Write, bufferSize: 0);
            stream.Write(Encoding.UTF8.GetBytes(TestDaemonSettings.ProbeMarker));
            outcome = "written";
        }
        catch (Exception e) when (e is IOException or ArgumentException or UnauthorizedAccessException or NotSupportedException)
        {
            outcome = "refused (" + e.GetType().Name + ")";
        }
        Out("testdaemon: probe handle " + value + " " + outcome);
    }

    private static string? Argument(string[] args, string name)
    {
        for (var i = 0; i + 1 < args.Length; i++)
        {
            if (args[i] == name)
            {
                return args[i + 1];
            }
        }
        return null;
    }

    private static int Delay(int fallback) =>
        Environment.GetEnvironmentVariable(TestDaemonSettings.DelayEnv) is { Length: > 0 } delay
            ? int.Parse(delay, CultureInfo.InvariantCulture)
            : fallback;

    private static string Variable(string name) =>
        name + "=" + (Environment.GetEnvironmentVariable(name) ?? "<unset>");

    private static string Json(string[] args)
    {
        using var buffer = new MemoryStream();
        using (var writer = new Utf8JsonWriter(buffer, new JsonWriterOptions { Encoder = JavaScriptEncoder.UnsafeRelaxedJsonEscaping }))
        {
            writer.WriteStartArray();
            foreach (var arg in args)
            {
                writer.WriteStringValue(arg);
            }
            writer.WriteEndArray();
        }
        return Encoding.UTF8.GetString(buffer.ToArray());
    }

    private static void TryDelete(string path)
    {
        try
        {
            File.Delete(path);
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException)
        {
        }
    }

    private static void Out(string line) => Write(StandardOutput, line);

    private static void Err(string line) => Write(StandardError, line);

    // UTF-8 bytes, whatever the console's code page, as Go writes them.
    private static void Write(Stream stream, string line)
    {
        lock (WriteGate)
        {
            try
            {
                stream.Write(Encoding.UTF8.GetBytes(line + "\n"));
                stream.Flush();
            }
            catch (IOException)
            {
                // Nobody reads any more.
            }
        }
    }
}
