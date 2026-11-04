// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantMarkdown.swift
// (Assistant.BlockKind); GTK: ui/internal/assistant/markdown.go (BlockKind).

namespace Malachi.Core.Assistants;

/// <summary>What a <see cref="MarkdownBlock"/> is.</summary>
public enum MarkdownBlockKind
{
    /// <summary>A paragraph: lines of text up to a blank line.</summary>
    Paragraph,

    /// <summary>A heading of level 1 to 3.</summary>
    Heading,

    /// <summary>A bullet item: "- " or "* ".</summary>
    Bullet,

    /// <summary>A numbered item: 1 to 9 digits and ". ".</summary>
    Numbered,

    /// <summary>A code block between two lines of three backticks.</summary>
    Code,
}
