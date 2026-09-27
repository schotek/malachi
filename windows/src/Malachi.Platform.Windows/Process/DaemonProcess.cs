// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the daemon's process as DaemonSupervisor sees it
// (IDaemonProcess), the counterpart of the Foundation.Process that
// macos/Sources/MalachiCore/Daemon/DaemonSupervisor.swift keeps
// (terminationHandler, terminate, SIGKILL) and of cmd in
// ui/internal/daemon/daemon.go (Wait, Signal, Kill). Its output is read
// line by line on a thread of its own (the pipe is synchronous) and handed
// on as UTF-8 text; its exit is a wait on the process handle, reported once
// the output has been passed on or DrainGrace after the exit, whichever
// comes first (a program the daemon started could hold the pipe). The stop
// request is CTRL_BREAK (ConsoleBreak), sent only while the handle says the
// process runs (not in the drain after its exit); the kill is
// TerminateProcess with the exit code Process.Kill leaves too (-1).

using System;
using System.IO;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Daemon;
using Malachi.Platform.Windows.Consoles;
using Microsoft.Win32.SafeHandles;
using Windows.Win32;
using Windows.Win32.Foundation;

namespace Malachi.Platform.Windows.Processes;

/// <summary>A running daemon started by <see cref="DaemonProcessHost"/>.</summary>
internal sealed class DaemonProcess : IDaemonProcess
{
    /// <summary>How long the exit waits for the last lines of output.</summary>
    public static readonly TimeSpan DrainGrace = TimeSpan.FromSeconds(2);

    private readonly ChildProcess child;
    private readonly Action<string> line;
    private readonly ConsoleAttachment? console;
    private readonly TimeProvider time;
    private readonly TaskCompletionSource<int> exited = new(TaskCreationOptions.RunContinuationsAsynchronously);
    private readonly TaskCompletionSource drained = new(TaskCreationOptions.RunContinuationsAsynchronously);
    private readonly ProcessWaitHandle waitHandle;
    private readonly RegisteredWaitHandle registration;
    private int disposed;

    public DaemonProcess(ChildProcess child, Action<string> line, ConsoleAttachment? console, TimeProvider time)
    {
        this.child = child;
        this.line = line;
        this.console = console;
        this.time = time;
        var pump = new Thread(Pump)
        {
            IsBackground = true,
            Name = "malachid output",
        };
        pump.Start();
        waitHandle = new ProcessWaitHandle(child.Handle);
        registration = ThreadPool.RegisterWaitForSingleObject(
            waitHandle, static (state, timedOut) => _ = ((DaemonProcess)state!).OnExitAsync(), this, Timeout.Infinite, executeOnlyOnce: true);
    }

    /// <inheritdoc/>
    public int Id => child.Id;

    /// <inheritdoc/>
    public Task<int> Exited => exited.Task;

    /// <summary>How the last stop request went (for tests and the log).</summary>
    public DaemonStopPath LastStopPath { get; private set; }

    /// <summary>
    /// Whether the process has ended, seen on its handle: up to
    /// <see cref="DrainGrace"/> before <see cref="Exited"/> completes. False
    /// once disposed without a known exit (the handle is gone).
    /// </summary>
    public bool HasExited
    {
        get
        {
            if (exited.Task.IsCompleted)
            {
                return true;
            }
            try
            {
                return PInvoke.WaitForSingleObject(child.Handle, 0) == WAIT_EVENT.WAIT_OBJECT_0;
            }
            catch (ObjectDisposedException)
            {
                return false;
            }
        }
    }

    /// <inheritdoc/>
    public bool RequestStop()
    {
        if (HasExited)
        {
            // Its output may still be draining; a stop request would find
            // no process, after an attached app had left its terminal's
            // console for it.
            LastStopPath = DaemonStopPath.Exited;
            return false;
        }
        LastStopPath = ConsoleBreak.Send(child.Id, console);
        return LastStopPath != DaemonStopPath.Failed;
    }

    /// <inheritdoc/>
    public void Kill()
    {
        if (exited.Task.IsCompleted || Volatile.Read(ref disposed) != 0)
        {
            return;
        }
        try
        {
            PInvoke.TerminateProcess(child.Handle, unchecked((uint)ExitStatus.Killed));
        }
        catch (ObjectDisposedException)
        {
            // Disposed meanwhile: nothing of ours runs any more.
        }
    }

    /// <summary>
    /// Stops watching the process and closes its handle; a process that
    /// still runs keeps running. The output is read until its end
    /// regardless.
    /// </summary>
    public void Dispose()
    {
        if (Interlocked.Exchange(ref disposed, 1) != 0)
        {
            return;
        }
        registration.Unregister(null);
        waitHandle.Dispose();
        child.Handle.Dispose();
    }

    private async Task OnExitAsync()
    {
        var code = ExitStatus.Killed;
        try
        {
            if (PInvoke.GetExitCodeProcess(child.Handle, out var exitCode))
            {
                code = unchecked((int)exitCode);
            }
        }
        catch (ObjectDisposedException)
        {
            // Disposed at the moment it exited: the kill's code stands in.
        }
        try
        {
            await drained.Task.WaitAsync(DrainGrace, time).ConfigureAwait(false);
        }
        catch (TimeoutException)
        {
            // Something the daemon started still holds the pipe.
        }
        exited.TrySetResult(code);
    }

    // Reads the pipe to its end. The stream owns the read end.
    private void Pump()
    {
        try
        {
            using var stream = new FileStream(child.Output, FileAccess.Read, bufferSize: 4096, isAsync: false);
            using var reader = new StreamReader(stream, new UTF8Encoding(false, false), detectEncodingFromByteOrderMarks: false);
            while (reader.ReadLine() is { } text)
            {
                try
                {
                    line(text);
                }
                catch (Exception e) when (e is not OutOfMemoryException)
                {
                    // A sink that fails loses the line, never the pipe.
                }
            }
        }
        catch (Exception e) when (e is IOException or ObjectDisposedException)
        {
            // The pipe broke: that is its end.
        }
        finally
        {
            drained.TrySetResult();
        }
    }

    // The process handle as a WaitHandle, without owning it.
    private sealed class ProcessWaitHandle : WaitHandle
    {
        public ProcessWaitHandle(SafeProcessHandle process)
        {
            SafeWaitHandle = new SafeWaitHandle(process.DangerousGetHandle(), ownsHandle: false);
        }
    }
}
