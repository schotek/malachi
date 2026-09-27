// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Platform/BridgeRunner.swift
// (BridgeRunner); GTK: ui/internal/mcpsetup/mcpsetup.go (run, Timeout,
// waitDelay). Windows differences: the process gets no window
// (CreateNoWindow: a console program would flash one), and a run that
// outlives its timeout is killed at once with every process it started
// (Kill(entireProcessTree)): there is no SIGTERM to send first, and the
// bridge's subcommands only read and write a few small files. As in Swift,
// the pipes are drained concurrently into bounded buffers (the rest is read
// and dropped, so the child never blocks on a full pipe), and a child of
// the bridge that inherited the pipes cannot hold the result back: the
// drains give up EofGrace after the exit. The caller's cancellation, which
// Swift has no counterpart of (Go's context), kills the run the same way.

using System;
using System.Buffers;
using System.Collections.Generic;
using System.ComponentModel;
using System.Diagnostics;
using System.IO;
using System.Threading;
using System.Threading.Tasks;

namespace Malachi.Core.Platform;

/// <summary>
/// Runs the bundled <c>malachi-mcp</c> for one of its setup subcommands
/// (<c>status</c>, <c>install</c>, <c>uninstall</c>, each with
/// <c>--json</c>; docs/mcp.md) and hands back what it printed: the JSON
/// object on stdout, the one-line reason on stderr, the exit status.
/// Nothing here interprets the output; the MCP registration controller
/// does. The bridge is a client of the daemon like the app, so it runs
/// with the app's environment (the socket default and <c>MALACHI_SOCKET</c>
/// agree).
/// </summary>
public sealed class BridgeRunner
{
    /// <summary>The most that is kept of each of stdout and stderr.</summary>
    public const int MaxOutput = 1 << 20;

    /// <summary>How long one bridge call may take (mcpsetup.Timeout).</summary>
    public static readonly TimeSpan DefaultTimeout = TimeSpan.FromSeconds(15);

    /// <summary>How long the drains wait for EOF after the exit.</summary>
    public static readonly TimeSpan EofGrace = TimeSpan.FromMilliseconds(500);

    /// <summary>A runner on <paramref name="time"/>, the system's clock when null.</summary>
    public BridgeRunner(TimeProvider? time = null)
    {
        Time = time ?? TimeProvider.System;
    }

    /// <summary>The clock of the timeout and the EOF grace.</summary>
    public TimeProvider Time { get; }

    /// <summary>
    /// Runs <paramref name="executable"/> with <paramref name="arguments"/>
    /// and waits for it. Throws <see cref="BridgeRunnerException"/> when it
    /// cannot be started or outlives <paramref name="timeout"/> (then it is
    /// killed with its children), and <see cref="OperationCanceledException"/>
    /// when <paramref name="cancellationToken"/> ends it first.
    /// </summary>
    public async Task<BridgeRunnerOutput> RunAsync(
        string executable, IReadOnlyList<string> arguments, TimeSpan timeout, CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrEmpty(executable);
        ArgumentNullException.ThrowIfNull(arguments);
        var start = new ProcessStartInfo(executable)
        {
            UseShellExecute = false,
            CreateNoWindow = true,
            RedirectStandardInput = true,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
        };
        foreach (var argument in arguments)
        {
            start.ArgumentList.Add(argument);
        }
        cancellationToken.ThrowIfCancellationRequested();
        Process process;
        try
        {
            process = Process.Start(start) ?? throw BridgeRunnerException.Launch("no process was started");
        }
        catch (Win32Exception e)
        {
            // The system's reason alone; .NET's message repeats the path
            // and the working directory.
            throw BridgeRunnerException.Launch(new Win32Exception(e.NativeErrorCode).Message, e);
        }
        using (process)
        {
            try
            {
                process.StandardInput.Close();
            }
            catch (IOException)
            {
                // It exited before its stdin was closed.
            }
            using var drains = new CancellationTokenSource(System.Threading.Timeout.InfiniteTimeSpan, Time);
            var stdout = DrainAsync(process.StandardOutput.BaseStream, drains.Token);
            var stderr = DrainAsync(process.StandardError.BaseStream, drains.Token);

            using var limit = new CancellationTokenSource(timeout, Time);
            using var either = CancellationTokenSource.CreateLinkedTokenSource(limit.Token, cancellationToken);
            var timedOut = false;
            try
            {
                await process.WaitForExitAsync(either.Token).ConfigureAwait(false);
            }
            catch (OperationCanceledException)
            {
                timedOut = !cancellationToken.IsCancellationRequested;
                KillTree(process);
                await process.WaitForExitAsync(CancellationToken.None).ConfigureAwait(false);
            }
            // The pipes end at EOF with the exit, or EofGrace after it when
            // a child of the bridge still holds them.
            drains.CancelAfter(EofGrace);
            var collectedOut = await stdout.ConfigureAwait(false);
            var collectedErr = await stderr.ConfigureAwait(false);
            cancellationToken.ThrowIfCancellationRequested();
            if (timedOut)
            {
                throw BridgeRunnerException.TimedOut(timeout);
            }
            return new BridgeRunnerOutput
            {
                Stdout = collectedOut,
                Stderr = collectedErr,
                Status = process.ExitCode,
            };
        }
    }

    private static void KillTree(Process process)
    {
        try
        {
            process.Kill(entireProcessTree: true);
        }
        catch (Exception e) when (e is InvalidOperationException or Win32Exception or AggregateException or NotSupportedException)
        {
            // It exited meanwhile, or a child could not be ended: the wait
            // and the EOF grace below still end the run.
        }
    }

    // Reads the stream to EOF, or until the token says stop, keeping the
    // first MaxOutput bytes.
    private static async Task<byte[]> DrainAsync(Stream stream, CancellationToken cancellationToken)
    {
        var kept = new MemoryStream();
        var buffer = ArrayPool<byte>.Shared.Rent(64 << 10);
        try
        {
            while (true)
            {
                int n;
                try
                {
                    n = await stream.ReadAsync(buffer, cancellationToken).ConfigureAwait(false);
                }
                catch (OperationCanceledException)
                {
                    break;
                }
                catch (IOException)
                {
                    break;
                }
                if (n == 0)
                {
                    break; // EOF
                }
                var room = MaxOutput - (int)kept.Length;
                if (room > 0)
                {
                    kept.Write(buffer, 0, Math.Min(n, room));
                }
            }
        }
        finally
        {
            ArrayPool<byte>.Shared.Return(buffer);
        }
        return kept.ToArray();
    }
}
