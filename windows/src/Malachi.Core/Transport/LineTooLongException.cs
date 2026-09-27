// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Transport/LineFramer.swift (LineTooLong).

using System;

namespace Malachi.Core.Transport;

/// <summary>
/// A <see cref="LineFramer"/> was fed more unterminated data than its cap
/// (Swift <c>LineFramer.LineTooLong</c>).
/// </summary>
public sealed class LineTooLongException : Exception
{
    /// <summary>The cap that was exceeded.</summary>
    public LineTooLongException(int limit)
        : base($"a line is longer than {limit} bytes")
    {
        Limit = limit;
    }

    /// <summary>A plain exception, for the conventions of the type; the limit is 0.</summary>
    public LineTooLongException()
    {
    }

    /// <summary>A plain exception with a message, for the conventions of the type; the limit is 0.</summary>
    public LineTooLongException(string message)
        : base(message)
    {
    }

    /// <summary>A plain exception with a message and a cause, for the conventions of the type; the limit is 0.</summary>
    public LineTooLongException(string message, Exception innerException)
        : base(message, innerException)
    {
    }

    /// <summary>The cap, in bytes.</summary>
    public int Limit { get; }
}
