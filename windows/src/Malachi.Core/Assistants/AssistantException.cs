// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the thrown errors of macos/Sources/MalachiCore/Assistant/
// (Assistant.Failure, EventError, RewriteError, SearchError, each
// CustomStringConvertible with Go's text); GTK: the errors of
// ui/internal/assistant. The message is Go's error text, technical and
// English: callers log it or show it as a reason inside a translated
// sentence (Assistant.StoppedText, SearchFailedText), never alone.

using System;

namespace Malachi.Core.Assistants;

/// <summary>An error of <see cref="Assistant"/>: its <see cref="Kind"/> and Go's text as the message.</summary>
public sealed class AssistantException : Exception
{
    /// <summary>An error of <paramref name="kind"/> with Go's text <paramref name="message"/>.</summary>
    public AssistantException(AssistantError kind, string message)
        : base(message)
    {
        Kind = kind;
    }

    /// <summary>An error of the unspecified kind <see cref="AssistantError.Malformed"/>; for serialisers and analyzers.</summary>
    public AssistantException()
        : this(AssistantError.Malformed, "assistant: error")
    {
    }

    /// <summary>An error of the kind <see cref="AssistantError.Malformed"/> with <paramref name="message"/>.</summary>
    public AssistantException(string message)
        : this(AssistantError.Malformed, message)
    {
    }

    /// <summary>An error of the kind <see cref="AssistantError.Malformed"/> with <paramref name="message"/> and its cause.</summary>
    public AssistantException(string message, Exception innerException)
        : base(message, innerException)
    {
        Kind = AssistantError.Malformed;
    }

    /// <summary>What went wrong, as Go's <c>errors.Is</c> tells it.</summary>
    public AssistantError Kind { get; }
}
