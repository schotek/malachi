// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Platform/ClaudeCodeProcess.swift
// (ClaudeCodeProcess.StartError's cases); GTK:
// ui/internal/assistantpanel/process.go (Start's errors, errStarted).

namespace Malachi.Core.Platform;

/// <summary>Why <see cref="ClaudeCodeProcess.Start"/> started nothing.</summary>
public enum ClaudeCodeStartFailure
{
    /// <summary>The executable could not be started (missing, not a program; StartError.launch).</summary>
    Launch,

    /// <summary>The process was started already (StartError.alreadyStarted).</summary>
    AlreadyStarted,
}
