// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Compose/Mailto.swift (the thrown
// MailtoError); GTK: ui/internal/compose/mailto.go ("not a mailto: URI", the
// url.Parse error). The message never carries the URI.

using System;

namespace Malachi.Core.Compose;

/// <summary>A string <see cref="Mailto.ParseMailto"/> refused; <see cref="Error"/> says why.</summary>
public sealed class MailtoException : FormatException
{
    /// <summary>A refusal of the given kind.</summary>
    public MailtoException(MailtoError error)
        : base(error == MailtoError.NotMailto ? "not a mailto: URI" : "invalid URI")
    {
        Error = error;
    }

    /// <summary>A refusal for another reason (required by the exception pattern).</summary>
    public MailtoException()
        : this(MailtoError.InvalidUri)
    {
    }

    /// <summary>A refusal with a message of its own (required by the exception pattern).</summary>
    public MailtoException(string message)
        : base(message)
    {
        Error = MailtoError.InvalidUri;
    }

    /// <summary>A refusal with a message and a cause (required by the exception pattern).</summary>
    public MailtoException(string message, Exception innerException)
        : base(message, innerException)
    {
        Error = MailtoError.InvalidUri;
    }

    /// <summary>Why the string was refused.</summary>
    public MailtoError Error { get; }
}
