// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/HTML/CIDRegistry.swift (the thrown
// InlineImageError and its description); GTK: ui/internal/editor/cid.go
// (checkInline's error texts, which the messages keep).

using System;

namespace Malachi.Core.Html;

/// <summary>A picture the <c>cid:</c> scheme refuses to serve; <see cref="Error"/> says why.</summary>
public sealed class InlineImageException : Exception
{
    /// <summary>A refusal of the given kind, with Go's text.</summary>
    public InlineImageException(InlineImageError error)
        : base(Describe(error))
    {
        Error = error;
    }

    /// <summary>A refusal (required by the exception pattern).</summary>
    public InlineImageException()
        : this(InlineImageError.NotAPicture)
    {
    }

    /// <summary>A refusal with a message of its own (required by the exception pattern).</summary>
    public InlineImageException(string message)
        : base(message)
    {
        Error = InlineImageError.NotAPicture;
    }

    /// <summary>A refusal with a message and a cause (required by the exception pattern).</summary>
    public InlineImageException(string message, Exception innerException)
        : base(message, innerException)
    {
        Error = InlineImageError.NotAPicture;
    }

    /// <summary>Why the picture was refused.</summary>
    public InlineImageError Error { get; }

    /// <summary>checkInline's error text for <paramref name="error"/>.</summary>
    public static string Describe(InlineImageError error) => error switch
    {
        InlineImageError.Empty => "inline image is empty",
        InlineImageError.TooBig => "inline image too big",
        _ => "inline image is not a picture",
    };
}
