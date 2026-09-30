// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Platform/ClaudeCodeProcess.swift
// (ClaudeCodeProcess, StdinWriter, ChildState, pump, drain, readLoop);
// GTK: ui/internal/assistantpanel/process.go (Process, stdinWriter,
// limitedBuffer, readLines, deliver).
//
// Windows differences:
// - There is no SIGTERM. Terminate closes stdin, which ends claude -p at the
//   end of its input, and KillGrace later kills the whole process tree when
//   claude is still there (Process.Kill(entireProcessTree), which ends the
//   malachi-mcp bridge Claude Code started too). A killed process exits
//   with -1 where Swift reports -15 or -9, a crash with its NTSTATUS
//   (ClaudeCodeExit words both with ExitStatus).
// - The child is a System.Diagnostics.Process started at the SpawnGate, as
//   BridgeRunner starts the bridge (.NET hands a child every inheritable
//   handle of the app), without a window (CreateNoWindow: a console program
//   would flash one), its environment cleared and filled from the caller's
//   dictionary. stdin, stdout and stderr are raw byte streams: UTF-8
//   without a preamble is named for all three and only their base streams
//   are used, so the console's code page never touches a turn.
// - A write to a process that is gone fails with an IOException (there is
//   no SIGPIPE to keep from ending the app) and is dropped with every write
//   queued after it; the exit reports the rest.
// - The events are delivered on the SynchronizationContext of the thread
//   that called Start (the UI thread; Swift's main actor), one batch per
//   chunk read, in order, and the exit after the last of them; without a
//   context they are delivered on the reader's own thread, still in order.
// - The kill grace and the EOF grace run on the injected TimeProvider.
// - A stdout line over MaxLine is dropped whole, as Go's readLines drops
//   it: the rest of it up to its newline is skipped (Swift reads that tail
//   as a line of its own, which then fails as JSON), and the lines around
//   it are kept (LineFramer drops the lines a chunk completed before it
//   overflows, so stdout is fed to it one line at a time). A last line
//   without its newline is dropped at the end, as Swift's framer drops it.
// - Swift's onEvents and onExit callbacks are the events EventsReceived and
//   Exited; a handler that throws is logged, never ends the reader.

using System;
using System.Buffers;
using System.Collections.Generic;
using System.ComponentModel;
using System.Diagnostics;
using System.IO;
using System.Text;
using System.Threading;
using System.Threading.Channels;
using System.Threading.Tasks;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers;
using Malachi.Core.Daemon;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Platform;

/// <summary>
/// One conversation of the assistant panel: a long-lived <c>claude -p</c>
/// with stream-json on both sides (<see cref="Assistant.Args"/>). Each
/// <see cref="Send"/> writes one turn to its stdin
/// (<see cref="Assistant.UserMessage"/> and a newline); stdin stays open
/// while the conversation lives. Its stdout is read continuously, cut into
/// lines by the transport's <see cref="LineFramer"/>, every line parsed with
/// <see cref="Assistant.ParseEvents"/> off the UI thread, and the events are
/// delivered on the UI thread in the order of the lines
/// (<see cref="EventsReceived"/>, one batch per chunk read). A line that is
/// not JSON is logged and skipped. Its stderr is kept, at most
/// <see cref="StderrLimit"/> bytes, for the reason of an early exit (its
/// first line, <see cref="ReasonLimit"/> bytes).
/// </summary>
/// <remarks>
/// <para>
/// A one-shot request writes one turn and closes stdin
/// (<see cref="CloseInput"/>); Claude Code then ends after its answer.
/// </para>
/// <para>
/// <see cref="Terminate"/> closes stdin and kills the process with every
/// process it started after the kill grace when it is still there. Its end
/// is reported once, after every event of its stdout
/// (<see cref="Exited"/>), whether it exited by itself, died or was killed.
/// The environment and the working directory are the caller's
/// (<see cref="Assistant.ChildEnvironment"/>, the private directory).
/// Create it, and call it, on the UI thread.
/// </para>
/// </remarks>
public sealed partial class ClaudeCodeProcess
{
    /// <summary>The most of stderr that is kept.</summary>
    public const int StderrLimit = 64 << 10;

    /// <summary>The reason of an early exit: stderr's first line, cut at this many bytes (<see cref="Assistant.StoppedText"/> shows as much).</summary>
    public const int ReasonLimit = 400;

    /// <summary>
    /// The longest stdout line; a longer one is dropped (a tool result is
    /// capped far below this by the bridge).
    /// </summary>
    public const int MaxLine = 16 << 20;

    /// <summary>From the end of stdin to the kill (Swift: SIGTERM to SIGKILL).</summary>
    public static readonly TimeSpan DefaultKillGrace = TimeSpan.FromSeconds(2);

    /// <summary>
    /// How long stdout and stderr are read after the exit (a child of
    /// Claude Code that inherited them would keep them open).
    /// </summary>
    public static readonly TimeSpan EofGrace = TimeSpan.FromMilliseconds(500);

    private static readonly UTF8Encoding Utf8 = new(encoderShouldEmitUTF8Identifier: false);

    private readonly string executable;
    private readonly IReadOnlyList<string> arguments;
    private readonly IReadOnlyDictionary<string, string> environment;
    private readonly string workingDirectory;
    private readonly TimeSpan killGrace;
    private readonly TimeProvider time;
    private readonly ILogger logger;

    // What the UI thread, the watcher and the kill timer share: whether the
    // process exited (no kill after it), and the timer.
    private readonly Lock gate = new();

    private Process? process;
    private StdinWriter? input;
    private SynchronizationContext? context;
    private ITimer? killer;
    private bool exited;
    private bool terminating;

    // CloseInput was called: nothing more is sent.
    private bool inputClosed;

    /// <summary>A conversation that <see cref="Start"/> starts.</summary>
    /// <param name="executable">The claude executable (<see cref="ClaudeCodeLocator.Locate"/>).</param>
    /// <param name="arguments">Its arguments (<see cref="Assistant.Args"/>).</param>
    /// <param name="environment">Its whole environment (<see cref="Assistant.ChildEnvironment"/>).</param>
    /// <param name="workingDirectory">Its working directory, which exists (the private one).</param>
    /// <param name="killGrace">From the end of stdin to the kill (<see cref="DefaultKillGrace"/>).</param>
    /// <param name="time">The clock of the kill and of the EOF grace; the system's when null.</param>
    /// <param name="logger">Receives the kinds of the lines that were dropped, never a line.</param>
    public ClaudeCodeProcess(
        string executable,
        IReadOnlyList<string> arguments,
        IReadOnlyDictionary<string, string> environment,
        string workingDirectory,
        TimeSpan? killGrace = null,
        TimeProvider? time = null,
        ILogger<ClaudeCodeProcess>? logger = null)
    {
        ArgumentException.ThrowIfNullOrEmpty(executable);
        ArgumentNullException.ThrowIfNull(arguments);
        ArgumentNullException.ThrowIfNull(environment);
        ArgumentNullException.ThrowIfNull(workingDirectory);
        this.executable = executable;
        this.arguments = arguments;
        this.environment = environment;
        this.workingDirectory = workingDirectory;
        this.killGrace = killGrace ?? DefaultKillGrace;
        this.time = time ?? TimeProvider.System;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
    }

    /// <summary>The events of each chunk of stdout, in order (Swift <c>onEvents</c>).</summary>
    public event EventHandler<IReadOnlyList<AssistantEvent>>? EventsReceived;

    /// <summary>The end of the process, once, after the last events (Swift <c>onExit</c>).</summary>
    public event EventHandler<ClaudeCodeExit>? Exited;

    /// <summary>Started and not yet reported as ended.</summary>
    public bool Running { get; private set; }

    /// <summary>How it ended, once it did; null before.</summary>
    public ClaudeCodeExit? Ended { get; private set; }

    /// <summary>
    /// Starts the process; throws a <see cref="ClaudeCodeStartException"/>
    /// when it cannot be started (the reason is technical).
    /// </summary>
    public void Start()
    {
        if (process is not null)
        {
            throw ClaudeCodeStartException.AlreadyStarted();
        }
        var start = new ProcessStartInfo(executable)
        {
            UseShellExecute = false,
            CreateNoWindow = true,
            RedirectStandardInput = true,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            StandardInputEncoding = Utf8,
            StandardOutputEncoding = Utf8,
            StandardErrorEncoding = Utf8,
            WorkingDirectory = workingDirectory,
        };
        foreach (var argument in arguments)
        {
            start.ArgumentList.Add(argument);
        }
        start.Environment.Clear();
        foreach (var (name, value) in environment)
        {
            start.Environment[name] = value;
        }
        var p = Launch(start);
        process = p;
        context = SynchronizationContext.Current;
        Running = true;
        input = new StdinWriter(p.StandardInput);
        var stdout = p.StandardOutput.BaseStream;
        var stderr = p.StandardError.BaseStream;
        _ = Task.Run(() => WatchAsync(p, stdout, stderr));
    }

    /// <summary>
    /// Writes one turn and a newline; false when the process is not running
    /// (or is being terminated, or its input was closed). The write happens
    /// off the UI thread, in order, and never blocks the caller.
    /// </summary>
    public bool Send(byte[] line)
    {
        ArgumentNullException.ThrowIfNull(line);
        if (!Running || terminating || inputClosed || input is null)
        {
            return false;
        }
        var data = new byte[line.Length + 1];
        line.CopyTo(data, 0);
        data[^1] = (byte)'\n';
        input.Write(data);
        return true;
    }

    /// <summary>
    /// Closes stdin after the turns written so far: Claude Code answers them
    /// and ends (a one-shot request). Nothing can be sent afterwards; the end
    /// is reported through <see cref="Exited"/>.
    /// </summary>
    public void CloseInput()
    {
        if (!Running || inputClosed || input is null)
        {
            return;
        }
        inputClosed = true;
        input.Close();
    }

    /// <summary>
    /// Ends the conversation now: stdin closed, and the process with every
    /// process it started killed after the grace when it is still there.
    /// The end is still reported through <see cref="Exited"/>.
    /// </summary>
    public void Terminate()
    {
        if (!Running || terminating || process is null)
        {
            return;
        }
        terminating = true;
        input?.Close();
        lock (gate)
        {
            if (!exited)
            {
                killer = time.CreateTimer(static state => ((ClaudeCodeProcess)state!).Kill(), this, killGrace, Timeout.InfiniteTimeSpan);
            }
        }
    }

    // Process.Start at the spawn gate (BridgeRunner.Start).
    private static Process Launch(ProcessStartInfo start)
    {
        try
        {
            using (SpawnGate.Enter())
            {
                return Process.Start(start) ?? throw ClaudeCodeStartException.Launch("no process was started");
            }
        }
        catch (Win32Exception e)
        {
            // The system's reason alone; .NET's message repeats the path
            // and the working directory.
            throw ClaudeCodeStartException.Launch(new Win32Exception(e.NativeErrorCode).Message, e);
        }
    }

    // The kill after the grace, unless the process exited meanwhile (its
    // handle is still held, so the id cannot be somebody else's; the lock
    // keeps the kill from racing the watcher's disposal).
    private void Kill()
    {
        lock (gate)
        {
            if (exited || process is not { } p)
            {
                return;
            }
            try
            {
                p.Kill(entireProcessTree: true);
            }
            catch (Exception e) when (e is InvalidOperationException or Win32Exception or AggregateException or NotSupportedException)
            {
                // It exited meanwhile, or a child could not be ended: the
                // watcher reports the exit all the same.
            }
        }
    }

    // Waits for the exit, then for the readers (at most EofGrace longer),
    // and delivers the end after the last events. The end is delivered
    // whatever happens here: a conversation that never ends would keep the
    // panel busy for ever.
    private async Task WatchAsync(Process p, Stream stdout, Stream stderr)
    {
        var status = ExitStatus.Killed;
        var err = Array.Empty<byte>();
        try
        {
            using var drains = new CancellationTokenSource(Timeout.InfiniteTimeSpan, time);
            var reading = ReadStdoutAsync(stdout, drains.Token);
            var draining = DrainStderrAsync(stderr, drains.Token);
            await p.WaitForExitAsync(CancellationToken.None).ConfigureAwait(false);
            lock (gate)
            {
                exited = true;
                status = p.ExitCode;
                killer?.Dispose();
                killer = null;
            }
            drains.CancelAfter(EofGrace);
            await reading.ConfigureAwait(false);
            err = await draining.ConfigureAwait(false);
            // Process.Dispose leaves the streams whose readers were handed out.
            await stdout.DisposeAsync().ConfigureAwait(false);
            await stderr.DisposeAsync().ConfigureAwait(false);
            p.Dispose();
        }
#pragma warning disable CA1031 // Logged, and the end is reported all the same.
        catch (Exception e)
#pragma warning restore CA1031
        {
            LogWatchFailed(logger, e);
        }
        Deliver(() => Finished(status, err));
    }

    // Reads stdout to EOF (or until told to stop), cutting lines and
    // parsing them, and delivers each chunk's events.
    private async Task ReadStdoutAsync(Stream stdout, CancellationToken stop)
    {
        var lines = new StdoutLines(this);
        var buffer = ArrayPool<byte>.Shared.Rent(64 << 10);
        try
        {
            while (true)
            {
                int n;
                try
                {
                    n = await stdout.ReadAsync(buffer, stop).ConfigureAwait(false);
                }
                catch (Exception e) when (e is OperationCanceledException or IOException or ObjectDisposedException)
                {
                    return;
                }
                if (n == 0)
                {
                    return; // EOF
                }
                var events = lines.Append(buffer.AsSpan(0, n));
                if (events.Count > 0)
                {
                    Deliver(() => EventsReceived?.Invoke(this, events));
                }
            }
        }
        finally
        {
            ArrayPool<byte>.Shared.Return(buffer);
        }
    }

    // Reads stderr to EOF (or until told to stop), keeping the first
    // StderrLimit bytes.
    private static async Task<byte[]> DrainStderrAsync(Stream stderr, CancellationToken stop)
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
                    n = await stderr.ReadAsync(buffer, stop).ConfigureAwait(false);
                }
                catch (Exception e) when (e is OperationCanceledException or IOException or ObjectDisposedException)
                {
                    break;
                }
                if (n == 0)
                {
                    break; // EOF
                }
                var room = StderrLimit - (int)kept.Length;
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

    // Runs action on the context Start was called on, after what was
    // delivered before; on the reader's thread without one. A handler that
    // throws is logged: it must not end the reader or lose the exit.
    private void Deliver(Action action)
    {
        if (context is { } c)
        {
            c.Post(static state => ((Action)state!)(), (Action)Guarded);
        }
        else
        {
            Guarded();
        }

        void Guarded()
        {
            try
            {
                action();
            }
#pragma warning disable CA1031 // A handler of the view must not end the reader.
            catch (Exception e)
#pragma warning restore CA1031
            {
                LogHandlerFailed(logger, e);
            }
        }
    }

    private void Finished(int status, byte[] stderr)
    {
        if (!Running)
        {
            return;
        }
        Running = false;
        input?.Close();
        var e = new ClaudeCodeExit(status, McpRegistrationController.FirstLine(stderr, ReasonLimit));
        Ended = e;
        Exited?.Invoke(this, e);
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "claude: a stdout line over {Limit} bytes was dropped")]
    private static partial void LogLineDropped(ILogger logger, int limit);

    // The line may be mail or model text: only the kind of the error is logged.
    [LoggerMessage(Level = LogLevel.Warning, Message = "claude: a stdout line was not read: {Kind}")]
    private static partial void LogLineUnread(ILogger logger, AssistantError kind);

    [LoggerMessage(Level = LogLevel.Error, Message = "claude: a handler of the process failed")]
    private static partial void LogHandlerFailed(ILogger logger, Exception error);

    [LoggerMessage(Level = LogLevel.Error, Message = "claude: waiting for the process failed")]
    private static partial void LogWatchFailed(ILogger logger, Exception error);

    /// <summary>
    /// The lines of stdout (Go's readLines): a line at a time into the
    /// framer, a line over <see cref="MaxLine"/> dropped up to its newline,
    /// every other line parsed. The reader's alone.
    /// </summary>
    private sealed class StdoutLines(ClaudeCodeProcess owner)
    {
        private readonly LineFramer framer = new(MaxLine);

        // The rest of an overlong line is being skipped up to its newline.
        private bool dropping;

        public List<AssistantEvent> Append(ReadOnlySpan<byte> chunk)
        {
            var events = new List<AssistantEvent>();
            while (!chunk.IsEmpty)
            {
                var newline = chunk.IndexOf((byte)'\n');
                var piece = newline < 0 ? chunk : chunk[..(newline + 1)];
                chunk = newline < 0 ? default : chunk[(newline + 1)..];
                if (dropping)
                {
                    dropping = newline < 0;
                    continue;
                }
                IReadOnlyList<byte[]> lines;
                try
                {
                    lines = framer.Append(piece);
                }
                catch (LineTooLongException)
                {
                    // Only a piece without its newline overflows: the framer
                    // takes a line out before it counts what is left.
                    LogLineDropped(owner.logger, MaxLine);
                    dropping = true;
                    continue;
                }
                foreach (var line in lines)
                {
                    if (line.Length > MaxLine)
                    {
                        LogLineDropped(owner.logger, MaxLine);
                        continue;
                    }
                    try
                    {
                        events.AddRange(Assistant.ParseEvents(line));
                    }
                    catch (AssistantException e)
                    {
                        LogLineUnread(owner.logger, e.Kind);
                    }
                }
            }
            return events;
        }
    }

    /// <summary>
    /// The write end of the child's stdin: every write and the close run on
    /// one background task, in order, so a close never overtakes a write, a
    /// write never touches a closed stream, and a child that does not read
    /// never blocks the UI thread.
    /// </summary>
    private sealed class StdinWriter
    {
        private readonly Channel<byte[]> queue = Channel.CreateUnbounded<byte[]>(new UnboundedChannelOptions { SingleReader = true });

        public StdinWriter(StreamWriter stdin)
        {
            _ = Task.Run(() => PumpAsync(stdin));
        }

        // Dropped once the writer is closed or the process is gone.
        public void Write(byte[] data) => queue.Writer.TryWrite(data);

        // Closes stdin once the writes before it are done.
        public void Close() => queue.Writer.TryComplete();

        private async Task PumpAsync(StreamWriter stdin)
        {
            var stream = stdin.BaseStream;
            try
            {
                await foreach (var data in queue.Reader.ReadAllAsync().ConfigureAwait(false))
                {
                    try
                    {
                        await stream.WriteAsync(data).ConfigureAwait(false);
                        await stream.FlushAsync().ConfigureAwait(false);
                    }
                    catch (Exception e) when (e is IOException or ObjectDisposedException)
                    {
                        // The process is gone; its exit says why.
                        queue.Writer.TryComplete();
                        break;
                    }
                }
            }
            finally
            {
                try
                {
                    await stdin.DisposeAsync().ConfigureAwait(false);
                }
                catch (Exception e) when (e is IOException or ObjectDisposedException)
                {
                    // The pipe is broken already.
                }
            }
        }
    }
}
