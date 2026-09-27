// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The process FakeProcessHost hands out: it exits when the test says so,
// and records the stop requests and the kill.

using System.Threading.Tasks;
using Malachi.Core.Daemon;

namespace Malachi.Core.Tests.Daemon;

/// <summary>A process that exits when the test says so.</summary>
internal sealed class FakeProcess(int id, bool exitOnStop) : IDaemonProcess
{
    private readonly TaskCompletionSource<int> exit = new(TaskCreationOptions.RunContinuationsAsynchronously);

    public int Id { get; } = id;

    public Task<int> Exited => exit.Task;

    public int StopRequests { get; private set; }

    public bool Killed { get; private set; }

    public bool Disposed { get; private set; }

    public void ExitWith(int code) => exit.TrySetResult(code);

    public bool RequestStop()
    {
        StopRequests++;
        if (exitOnStop)
        {
            ExitWith(0);
        }
        return true;
    }

    public void Kill()
    {
        Killed = true;
        ExitWith(ExitStatus.Killed);
    }

    public void Dispose() => Disposed = true;
}
