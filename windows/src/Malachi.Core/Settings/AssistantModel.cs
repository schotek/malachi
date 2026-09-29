// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Settings/Settings.swift (Settings.assistantModel);
// GTK: ui/internal/assistant (Model), the gschema enum
// io.github.schotek.Malachi.AssistantModel.

namespace Malachi.Core.Settings;

/// <summary>
/// The Claude model the assistant panel asks Claude Code for; stored as its
/// gschema nick, which is Claude Code's <c>--model</c> alias.
/// </summary>
public enum AssistantModel
{
    /// <summary><c>sonnet</c>, the default.</summary>
    Sonnet,

    /// <summary><c>haiku</c>.</summary>
    Haiku,

    /// <summary><c>opus</c>.</summary>
    Opus,
}
