// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantSearch.swift
// (Assistant.SearchStrings); GTK: ui/internal/assistant/assistant.go
// (SearchStrings).

namespace Malachi.Core.Assistants;

/// <summary>
/// The fixed texts of the search in the user's own words
/// (<see cref="Assistant.SearchTexts"/>): the item of the search box's menu
/// and the box's placeholder while the words are converted. A failure is
/// <see cref="Assistant.SearchFailedText"/>.
/// </summary>
public sealed record SearchStrings
{
    /// <summary>The item that turns what was typed into a search.</summary>
    public required string OwnWords { get; init; }

    /// <summary>The placeholder while the words are converted.</summary>
    public required string Converting { get; init; }
}
