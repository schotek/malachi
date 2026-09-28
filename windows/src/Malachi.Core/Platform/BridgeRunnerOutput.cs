// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Platform/BridgeRunner.swift (the tuple
// run returns: stdout, stderr, status); GTK: ui/internal/mcpsetup/
// mcpsetup.go (run: the two buffers and the exit status).

using System;
using Malachi.Core.Daemon;

namespace Malachi.Core.Platform;

/// <summary>What one run of the bridge printed and how it exited.</summary>
public sealed record BridgeRunnerOutput
{
    /// <summary>Its stdout, the first <see cref="BridgeRunner.MaxOutput"/> bytes.</summary>
    public required ReadOnlyMemory<byte> Stdout { get; init; }

    /// <summary>Its stderr, the first <see cref="BridgeRunner.MaxOutput"/> bytes.</summary>
    public required ReadOnlyMemory<byte> Stderr { get; init; }

    /// <summary>
    /// The exit code: -1 (<see cref="ExitStatus.Killed"/>) when the process
    /// was ended by TerminateProcess, an NTSTATUS such as 0xC0000005 as a
    /// negative number when it crashed; <see cref="ExitStatus.Describe"/>
    /// words it.
    /// </summary>
    public required int Status { get; init; }
}
