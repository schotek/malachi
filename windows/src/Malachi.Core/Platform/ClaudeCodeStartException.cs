// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Platform/ClaudeCodeProcess.swift
// (ClaudeCodeProcess.StartError and its description; the texts kept: they
// are the technical reasons of "The assistant stopped: %s"); GTK:
// ui/internal/assistantpanel/process.go (Start's errors, errStarted).

using System;

namespace Malachi.Core.Platform;

/// <summary>A <see cref="ClaudeCodeProcess"/> that did not start; <see cref="Failure"/> says why.</summary>
public sealed class ClaudeCodeStartException : Exception
{
    /// <summary>Creates the exception with the default message.</summary>
    public ClaudeCodeStartException()
    {
    }

    /// <summary>Creates the exception with <paramref name="message"/>.</summary>
    public ClaudeCodeStartException(string message)
        : base(message)
    {
    }

    /// <summary>Creates the exception with <paramref name="message"/> and its cause.</summary>
    public ClaudeCodeStartException(string message, Exception innerException)
        : base(message, innerException)
    {
    }

    private ClaudeCodeStartException(ClaudeCodeStartFailure failure, string message, Exception? innerException = null)
        : base(message, innerException)
    {
        Failure = failure;
    }

    /// <summary>Which failure.</summary>
    public ClaudeCodeStartFailure Failure { get; }

    /// <summary>The executable could not be started, for the reason <paramref name="why"/>.</summary>
    public static ClaudeCodeStartException Launch(string why, Exception? innerException = null) =>
        new(ClaudeCodeStartFailure.Launch, "claude could not be started: " + why, innerException);

    /// <summary>The process was started already.</summary>
    public static ClaudeCodeStartException AlreadyStarted() =>
        new(ClaudeCodeStartFailure.AlreadyStarted, "claude was started already");
}
