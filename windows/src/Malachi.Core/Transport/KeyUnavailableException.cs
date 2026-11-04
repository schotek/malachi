// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Transport/DaemonKey.swift (Unavailable).

using System;

namespace Malachi.Core.Transport;

/// <summary>
/// Why the key file cannot be used (Swift <c>DaemonKey.Unavailable</c>):
/// a <see cref="KeyFileReason"/> text, never anything of the file's
/// content. The handshake reports it as
/// <see cref="HandshakeError.KeyUnavailable(string)"/>.
/// </summary>
public sealed class KeyUnavailableException : Exception
{
    /// <summary>A plain exception, for the conventions of the type; the reason is empty.</summary>
    public KeyUnavailableException()
    {
        Reason = "";
    }

    /// <summary>The key file cannot be used for <paramref name="reason"/>.</summary>
    public KeyUnavailableException(string reason)
        : base(reason)
    {
        Reason = reason;
    }

    /// <summary>The same, with the exception that caused it.</summary>
    public KeyUnavailableException(string reason, Exception innerException)
        : base(reason, innerException)
    {
        Reason = reason;
    }

    /// <summary>The reason: fixed text around the path.</summary>
    public string Reason { get; }
}
