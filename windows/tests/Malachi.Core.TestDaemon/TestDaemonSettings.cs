// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The stand-in daemon's switches, the counterpart of fakeEnv and the modes
// of ui/internal/daemon/daemon_test.go (fakeDaemon) and of the scripts of
// macos/Tests/MalachiCoreTests/SupervisorTests.swift. The supervisor passes
// the environment it is given, so a test selects the behaviour of the
// process it starts without touching its own environment. The tests
// reference this assembly for these names and find the program beside
// themselves (Program).

using System;
using System.IO;

namespace Malachi.Core.TestDaemon;

/// <summary>The environment variables the stand-in daemon reads, and its modes.</summary>
public static class TestDaemonSettings
{
    /// <summary>
    /// The mode: <see cref="Listen"/> (the default), <see cref="Slow"/>,
    /// <see cref="Exit"/> or <see cref="Deaf"/>.
    /// </summary>
    public const string ModeEnv = "MALACHI_TEST_DAEMON";

    /// <summary>
    /// Milliseconds before <see cref="Slow"/> listens (400 by default) or
    /// <see cref="Exit"/> exits (0).
    /// </summary>
    public const string DelayEnv = "MALACHI_TEST_DAEMON_DELAY_MS";

    /// <summary>The status <see cref="Exit"/> exits with (1 by default).</summary>
    public const string ExitCodeEnv = "MALACHI_TEST_DAEMON_EXIT_CODE";

    /// <summary>
    /// "1": the end of stdin is a stop request as well, the one stop
    /// request a test host can send portably (no signals in .NET).
    /// </summary>
    public const string StopOnEofEnv = "MALACHI_TEST_DAEMON_STOP_ON_EOF";

    /// <summary>
    /// A handle value (decimal) of the parent's: the daemon writes
    /// <see cref="ProbeMarker"/> to it, which reaches the parent only when
    /// the handle was inherited.
    /// </summary>
    public const string ProbeHandleEnv = "MALACHI_TEST_DAEMON_PROBE_HANDLE";

    /// <summary>What goes to the probe handle.</summary>
    public const string ProbeMarker = "inherited";

    /// <summary>
    /// Milliseconds the output of an <see cref="Exit"/> daemon stays open
    /// after it exits: it leaves a copy of itself behind that shares its
    /// stdout and stderr and exits that much later (a program the daemon
    /// started that holds the pipe). The daemon names the copy in
    /// <see cref="HolderLine"/>.
    /// </summary>
    public const string HoldOutputEnv = "MALACHI_TEST_DAEMON_HOLD_OUTPUT_MS";

    /// <summary>The start of the line that gives the process ID of the copy holding the output.</summary>
    public const string HolderLine = "testdaemon: output held by pid ";

    /// <summary>Listens on <c>--socket</c> and exits cleanly on a stop request (daemon_test.go "listen").</summary>
    public const string Listen = "listen";

    /// <summary>As <see cref="Listen"/> after a delay ("slow").</summary>
    public const string Slow = "slow";

    /// <summary>Exits at once, or after the delay, with the exit status ("exit").</summary>
    public const string Exit = "exit";

    /// <summary>Listens and ignores every stop request ("deaf"); only a kill ends it.</summary>
    public const string Deaf = "deaf";

    /// <summary>The line the daemon writes to stderr when it shuts down, as malachid logs "shutting down".</summary>
    public const string ShuttingDown = "testdaemon: shutting down";

    /// <summary>The program beside the calling test assembly.</summary>
    public static string ExecutablePath =>
        Path.Combine(AppContext.BaseDirectory, "Malachi.Core.TestDaemon" + (OperatingSystem.IsWindows() ? ".exe" : ""));
}
