// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the exit wording of macos/Sources/MalachiCore/Daemon/
// DaemonSupervisor.swift (the termination handler: "exited with status N",
// "died of signal N") and Controllers/MCPRegistrationController.swift
// (exitDescription); GTK: ui/internal/daemon/daemon.go (the ExitError text
// in await). Windows has no signals: a process ended by TerminateProcess,
// which is what Process.Kill and the supervisor's kill do, exits with
// 0xFFFFFFFF (-1), and one that crashed exits with its NTSTATUS
// (0xC0000005, 0xC000013A, ...), which reads only in hex.

using System.Globalization;

namespace Malachi.Core.Daemon;

/// <summary>How a process ended, in the words of the log and of the errors that name it.</summary>
public static class ExitStatus
{
    /// <summary>
    /// The exit code TerminateProcess leaves when Process.Kill or the
    /// supervisor ends a process (0xFFFFFFFF).
    /// </summary>
    public const int Killed = -1;

    /// <summary>
    /// What happened to a process that exited with <paramref name="code"/>,
    /// without the program's name: "exited with status 3", "was killed"
    /// (<see cref="Killed"/>), "died with status 0xC0000005" (any other
    /// code with the top bit set: an NTSTATUS, a crash or an unhandled
    /// console event).
    /// </summary>
    public static string Describe(int code) => code switch
    {
        Killed => "was killed",
        < 0 => "died with status 0x" + ((uint)code).ToString("X8", CultureInfo.InvariantCulture),
        _ => "exited with status " + code.ToString(CultureInfo.InvariantCulture),
    };
}
