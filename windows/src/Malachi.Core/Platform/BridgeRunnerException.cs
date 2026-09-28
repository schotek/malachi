// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Platform/BridgeRunner.swift (RunError
// and its description, the texts kept: the AI page shows them after "The
// MCP bridge could not be registered: "); GTK: ui/internal/mcpsetup/
// mcpsetup.go (run).

using System;
using System.Globalization;

namespace Malachi.Core.Platform;

/// <summary>A <see cref="BridgeRunner"/> run that gave no output; <see cref="Failure"/> says why.</summary>
public sealed class BridgeRunnerException : Exception
{
    /// <summary>Creates the exception with the default message.</summary>
    public BridgeRunnerException()
    {
    }

    /// <summary>Creates the exception with <paramref name="message"/>.</summary>
    public BridgeRunnerException(string message)
        : base(message)
    {
    }

    /// <summary>Creates the exception with <paramref name="message"/> and its cause.</summary>
    public BridgeRunnerException(string message, Exception innerException)
        : base(message, innerException)
    {
    }

    private BridgeRunnerException(BridgeRunnerFailure failure, string message, Exception? innerException = null)
        : base(message, innerException)
    {
        Failure = failure;
    }

    /// <summary>Which failure.</summary>
    public BridgeRunnerFailure Failure { get; }

    /// <summary>For <see cref="BridgeRunnerFailure.Timeout"/>: the limit it outlived.</summary>
    public TimeSpan Timeout { get; private init; }

    /// <summary>The executable could not be started, for the reason <paramref name="why"/>.</summary>
    public static BridgeRunnerException Launch(string why, Exception? innerException = null) =>
        new(BridgeRunnerFailure.Launch, "malachi-mcp could not be started: " + why, innerException);

    /// <summary>The process did not exit within <paramref name="limit"/> and was killed.</summary>
    public static BridgeRunnerException TimedOut(TimeSpan limit) => new(
        BridgeRunnerFailure.Timeout,
        string.Format(
            CultureInfo.InvariantCulture,
            "malachi-mcp did not finish within {0} s",
            limit.TotalSeconds.ToString("0.###", CultureInfo.InvariantCulture)))
    {
        Timeout = limit,
    };
}
