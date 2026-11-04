// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/assistant/compose_write.go (WriteStrings); macOS sets
// these texts in macos/Sources/MalachiMail/Compose/ComposeAssistantBar.swift
// (setMode, the placeholder) and ComposePane.swift (makeQuoteSection).

namespace Malachi.Core.Assistants;

/// <summary>
/// The fixed texts of the compose window's assistant bar and of the panel
/// with the prepared original under the editor, for one kind of window
/// (<see cref="Assistant.WriteTexts"/>).
/// </summary>
public sealed record WriteStrings
{
    /// <summary>The placeholder of the bar's field.</summary>
    public required string Instruction { get; init; }

    /// <summary>
    /// The bar's button: Write a Reply, Write an Introduction (a forward) or
    /// Write a Message (a new message, a draft).
    /// </summary>
    public required string Write { get; init; }

    /// <summary>The heading of the panel with the prepared original.</summary>
    public required string Quoted { get; init; }

    /// <summary>The panel's checkbox: Include in Reply, or Include in Forward.</summary>
    public required string Include { get; init; }
}
