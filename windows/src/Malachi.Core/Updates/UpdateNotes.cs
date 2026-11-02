// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/updates/updates.go (Notes); Swift:
// macos/Sources/MalachiCore/Updates/ (written at the same time).

using System.Collections.Generic;
using Malachi.Core.Assistants;

namespace Malachi.Core.Updates;

/// <summary>The What's New dialog (updates.Notes); the notes are Markdown blocks, never markup.</summary>
public sealed record UpdateNotes
{
    /// <summary>The heading.</summary>
    public required string Title { get; init; }

    /// <summary>The release date.</summary>
    public required string Subtitle { get; init; }

    /// <summary>The release notes as written (Markdown).</summary>
    public required string Body { get; init; }

    /// <summary>
    /// The notes read as Markdown (Assistant.Markdown: headings, lists,
    /// bold, italic, code, never HTML), every link as plain text: the way
    /// out of the dialog is More Information.
    /// </summary>
    public IReadOnlyList<MarkdownBlock> Blocks { get; init; } = [];

    /// <summary>The Download button's label.</summary>
    public required string Download { get; init; }

    /// <summary>The package; "" hides Download.</summary>
    public string Url { get; init; } = "";

    /// <summary>The More Information button's label.</summary>
    public required string MoreInfo { get; init; }

    /// <summary>UpdateCheck.NewsUrl; "" hides More Information.</summary>
    public string InfoUrl { get; init; } = "";

    /// <summary>The Close button's label.</summary>
    public required string Close { get; init; }
}
