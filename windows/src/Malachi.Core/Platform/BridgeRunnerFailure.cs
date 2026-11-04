// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Platform/BridgeRunner.swift (RunError's
// cases); GTK: ui/internal/mcpsetup/mcpsetup.go (run: a start error, the
// context's deadline).

namespace Malachi.Core.Platform;

/// <summary>Why a <see cref="BridgeRunner"/> run gave no output.</summary>
public enum BridgeRunnerFailure
{
    /// <summary>The executable could not be started (missing, not a program; RunError.launch).</summary>
    Launch,

    /// <summary>The process did not exit within the timeout and was killed (RunError.timeout).</summary>
    Timeout,
}
