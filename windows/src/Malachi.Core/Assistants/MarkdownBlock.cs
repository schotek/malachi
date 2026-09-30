// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantMarkdown.swift
// (Assistant.Block, Equatable); GTK: ui/internal/assistant/markdown.go
// (Block). A record compares an IReadOnlyList by reference, so Equals
// compares the spans by value, as Swift's arrays (and Go's reflect.DeepEqual
// in the tests) do; the printed form lists them for the same reason.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Text;

namespace Malachi.Core.Assistants;

/// <summary>
/// A paragraph, a heading, a list item or a code block of an answer
/// (assistant.Block, from <see cref="Assistant.Markdown"/>).
/// </summary>
public sealed record MarkdownBlock
{
    /// <summary>What the block is.</summary>
    public required MarkdownBlockKind Kind { get; init; }

    /// <summary>A heading's level (1–3) or a list item's nesting (0 to 3); 0 otherwise.</summary>
    public int Level { get; init; }

    /// <summary>A numbered item's number as written; 0 otherwise.</summary>
    public int Number { get; init; }

    /// <summary>
    /// The block's text: a code block has one code span with its lines,
    /// nothing parsed inside, and none when it is empty.
    /// </summary>
    public IReadOnlyList<MarkdownSpan> Spans { get; init => field = value ?? []; } = [];

    /// <summary>Whether both are the same kind, level and number with the same spans, in order.</summary>
    public bool Equals(MarkdownBlock? other) =>
        other is not null && Kind == other.Kind && Level == other.Level && Number == other.Number
        && Spans.SequenceEqual(other.Spans);

    /// <inheritdoc/>
    public override int GetHashCode() => HashCode.Combine(Kind, Level, Number, Spans.Count);

    private bool PrintMembers(StringBuilder builder)
    {
        builder.Append("Kind = ").Append(Kind).Append(", Level = ").Append(Level).Append(", Number = ").Append(Number)
            .Append(", Spans = [").AppendJoin(", ", Spans).Append(']');
        return true;
    }
}
