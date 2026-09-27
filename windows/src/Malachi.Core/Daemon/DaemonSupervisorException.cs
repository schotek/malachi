// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Daemon/DaemonSupervisor.swift
// (SupervisorError and its description, the texts kept); GTK:
// ui/internal/daemon/daemon.go (the errors of Locate and Ensure). The
// status line shows the message (ConnectionController describe).

using System;
using System.Globalization;

namespace Malachi.Core.Daemon;

/// <summary>A failure of <see cref="DaemonSupervisor"/>; <see cref="Failure"/> says which.</summary>
public sealed class DaemonSupervisorException : Exception
{
    /// <summary>Creates the exception with the default message.</summary>
    public DaemonSupervisorException()
    {
    }

    /// <summary>Creates the exception with <paramref name="message"/>.</summary>
    public DaemonSupervisorException(string message)
        : base(message)
    {
    }

    /// <summary>Creates the exception with <paramref name="message"/> and its cause.</summary>
    public DaemonSupervisorException(string message, Exception innerException)
        : base(message, innerException)
    {
    }

    private DaemonSupervisorException(DaemonSupervisorFailure failure, string message)
        : base(message)
    {
        Failure = failure;
    }

    /// <summary>Which failure.</summary>
    public DaemonSupervisorFailure Failure { get; }

    /// <summary>For <see cref="DaemonSupervisorFailure.Backoff"/>: the exits in a row.</summary>
    public int Failures { get; private init; }

    /// <summary>For <see cref="DaemonSupervisorFailure.Backoff"/>: how long until the next start.</summary>
    public TimeSpan RetryIn { get; private init; }

    /// <summary>
    /// For <see cref="DaemonSupervisorFailure.ExitedEarly"/>: how the daemon
    /// ended (<see cref="ExitStatus.Describe"/>).
    /// </summary>
    public string? Exit { get; private init; }

    /// <summary>For <see cref="DaemonSupervisorFailure.StartTimeout"/>: the socket that stayed silent.</summary>
    public string? Socket { get; private init; }

    /// <summary>Nothing answers and there is no daemon to start.</summary>
    public static DaemonSupervisorException NoDaemon() => new(
        DaemonSupervisorFailure.NoDaemon,
        "malachid not found beside the app or on PATH (" + DaemonSupervisor.DaemonEnv + "=none switches the automatic start off)");

    /// <summary>The daemon exited <paramref name="failures"/> times in a row; the next start is in <paramref name="retryIn"/>.</summary>
    public static DaemonSupervisorException Backoff(int failures, TimeSpan retryIn) => new(
        DaemonSupervisorFailure.Backoff,
        string.Format(
            CultureInfo.InvariantCulture,
            "malachid exited {0} times in a row; next start in {1} s",
            failures,
            (long)retryIn.TotalSeconds))
    {
        Failures = failures,
        RetryIn = retryIn,
    };

    /// <summary>The daemon ended (<paramref name="exit"/>, as <see cref="ExitStatus.Describe"/> says) before its socket answered.</summary>
    public static DaemonSupervisorException ExitedEarly(string exit) => new(
        DaemonSupervisorFailure.ExitedEarly,
        "malachid " + exit + " before opening its socket")
    {
        Exit = exit,
    };

    /// <summary>The daemon did not open <paramref name="socket"/> within <paramref name="timeout"/>.</summary>
    public static DaemonSupervisorException StartTimeout(string socket, TimeSpan timeout) => new(
        DaemonSupervisorFailure.StartTimeout,
        string.Format(
            CultureInfo.InvariantCulture,
            "malachid did not open {0} within {1} s",
            socket,
            timeout.TotalSeconds.ToString("0.###", CultureInfo.InvariantCulture)))
    {
        Socket = socket,
    };

    /// <summary>The application is quitting.</summary>
    public static DaemonSupervisorException Stopping() => new(
        DaemonSupervisorFailure.Stopping,
        "the application is quitting");
}
