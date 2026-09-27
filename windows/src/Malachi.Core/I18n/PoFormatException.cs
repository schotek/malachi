// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/scripts/po2strings.py (POError).

using System;

namespace Malachi.Core.I18n;

/// <summary>A malformed catalogue; the message names the file and line.</summary>
public sealed class PoFormatException : Exception
{
    /// <summary>Creates the exception with the default message.</summary>
    public PoFormatException()
    {
    }

    /// <summary>Creates the exception with <paramref name="message"/>.</summary>
    public PoFormatException(string message)
        : base(message)
    {
    }

    /// <summary>Creates the exception with <paramref name="message"/> and its cause.</summary>
    public PoFormatException(string message, Exception innerException)
        : base(message, innerException)
    {
    }
}
