// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// A process host without processes, for the tests of the supervisor's
// state machine on a fake clock (SupervisorStateTests): each start hands
// out a FakeProcess the test ends when it likes.

using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Daemon;

namespace Malachi.Core.Tests.Daemon;

/// <summary>Records starts and hands out fake processes.</summary>
internal sealed class FakeProcessHost : IDaemonProcessHost
{
    private int nextId = 1000;

    /// <summary>What each start was asked for.</summary>
    public List<DaemonStartInfo> Starts { get; } = [];

    /// <summary>The processes handed out.</summary>
    public List<FakeProcess> Started { get; } = [];

    /// <summary>When set, the next start throws it.</summary>
    public Exception? Fails { get; set; }

    /// <summary>The exit code a new process ends with at once, or null to keep it running.</summary>
    public int? ExitAtOnce { get; set; }

    /// <summary>Whether a new process exits cleanly when asked to stop.</summary>
    public bool ExitOnStop { get; set; } = true;

    public IDaemonProcess Start(DaemonStartInfo startInfo)
    {
        Starts.Add(startInfo);
        if (Fails is { } failure)
        {
            Fails = null;
            throw failure;
        }
        var process = new FakeProcess(Interlocked.Increment(ref nextId), ExitOnStop);
        if (ExitAtOnce is { } code)
        {
            process.ExitWith(code);
        }
        Started.Add(process);
        return process;
    }
}
