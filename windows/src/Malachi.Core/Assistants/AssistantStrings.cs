// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/Assistant.swift
// (Assistant.Strings); GTK: ui/internal/assistant/assistant.go (Strings).

namespace Malachi.Core.Assistants;

/// <summary>The fixed texts of the Assistant menu and its settings (<see cref="Assistant.Texts"/>).</summary>
public sealed record AssistantStrings
{
    /// <summary>The menu's title.</summary>
    public required string Assistant { get; init; }

    /// <summary>The heading above the choice of the target.</summary>
    public required string OpenIn { get; init; }

    /// <summary>The item that opens Preferences → AI when no target can run the message actions.</summary>
    public required string SetUp { get; init; }

    /// <summary>The item in an attachment's menu that hands the file over.</summary>
    public required string AskFile { get; init; }

    /// <summary>The settings switch of the key <c>assistant-menu</c>.</summary>
    public required string ShowMenu { get; init; }

    /// <summary>The text of the settings group.</summary>
    public required string Description { get; init; }

    /// <summary>
    /// Why the switch cannot be turned on while the bridge is not registered
    /// (<see cref="Malachi.Core.Assistants.Assistant.Shown"/>), under it.
    /// </summary>
    public required string RegisterFirst { get; init; }
}
