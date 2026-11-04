// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Settings/Settings.swift (Settings.AssistantTarget);
// GTK: ui/internal/assistant (Target), the gschema enum
// io.github.schotek.Malachi.AssistantTarget.

namespace Malachi.Core.Settings;

/// <summary>
/// Where the Assistant opens Claude: Claude Desktop, Claude Code in a
/// terminal, or the panel in the app; stored as its gschema nick.
/// </summary>
public enum AssistantTarget
{
    /// <summary><c>desktop</c>.</summary>
    Desktop,

    /// <summary><c>code</c>.</summary>
    Code,

    /// <summary><c>app</c>: the panel in the main window, which runs Claude Code itself.</summary>
    App,
}
