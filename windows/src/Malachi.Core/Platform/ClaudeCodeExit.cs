// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Platform/ClaudeCodeProcess.swift
// (ClaudeCodeProcess.Exit, description); GTK:
// ui/internal/assistantpanel/process.go (Exit, Description). Windows has no
// signals: a process ended by TerminateProcess (the kill after the grace)
// exits with -1 and a crash with its NTSTATUS, which ExitStatus words
// ("claude was killed", "claude died with status 0xC0000005") where Swift
// and Go say "claude died of signal N".

using System;
using Malachi.Core.Daemon;

namespace Malachi.Core.Platform;

/// <summary>
/// How a <see cref="ClaudeCodeProcess"/> ended: its exit code and the
/// first line of its stderr.
/// </summary>
/// <param name="Status">
/// The exit code: -1 (<see cref="ExitStatus.Killed"/>) when it was killed,
/// an NTSTATUS such as 0xC0000005 as a negative number when it crashed.
/// </param>
/// <param name="Reason">
/// The first line of its stderr, at most <see cref="ClaudeCodeProcess.ReasonLimit"/>
/// bytes; "" without one.
/// </param>
public sealed record ClaudeCodeExit(int Status, string Reason)
{
    /// <summary>
    /// The reason for the transcript: the first line of stderr, else the
    /// status in words ("claude exited with status 0"; technical, English,
    /// like other error details).
    /// </summary>
    public string Description => Reason.Length > 0 ? Reason : "claude " + ExitStatus.Describe(Status);

    /// <inheritdoc/>
    public override string ToString() => Description;
}
