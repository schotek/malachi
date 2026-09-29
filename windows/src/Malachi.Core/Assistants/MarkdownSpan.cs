// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantMarkdown.swift
// (Assistant.Span); GTK: ui/internal/assistant/markdown.go (Span).

namespace Malachi.Core.Assistants;

/// <summary>
/// A run of text in one style (assistant.Span). <see cref="Link"/> is an http
/// or https URL, "" for none; <see cref="Text"/> is what is shown.
/// </summary>
public sealed record MarkdownSpan
{
    /// <summary>What is shown.</summary>
    public required string Text { get; init; }

    /// <summary>**bold**.</summary>
    public bool Bold { get; init; }

    /// <summary>*italic* or _italic_.</summary>
    public bool Italic { get; init; }

    /// <summary>`code`, or the text of a code block.</summary>
    public bool Code { get; init; }

    /// <summary>The http or https URL the text links to; "" for none.</summary>
    public string Link { get; init => field = value ?? ""; } = "";
}
