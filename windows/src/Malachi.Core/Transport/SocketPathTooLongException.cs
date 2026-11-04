// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Transport/UnixSocketProbe.swift (PathTooLong).

using System;

namespace Malachi.Core.Transport;

/// <summary>
/// The socket path does not fit a unix socket address
/// (<see cref="UnixSocketProbe.MaxPathBytes"/>); the message tells how to
/// choose another one.
/// </summary>
public sealed class SocketPathTooLongException : Exception
{
    /// <summary>The path and its length in UTF-8 bytes.</summary>
    public SocketPathTooLongException(string path, int bytes)
        : base($"socket path is {bytes} bytes, Windows allows {UnixSocketProbe.MaxPathBytes}: {path} (set MALACHI_SOCKET to a shorter path)")
    {
        Path = path;
        Bytes = bytes;
    }

    /// <summary>A plain exception, for the conventions of the type.</summary>
    public SocketPathTooLongException()
    {
        Path = "";
    }

    /// <summary>A plain exception with a message, for the conventions of the type.</summary>
    public SocketPathTooLongException(string message)
        : base(message)
    {
        Path = "";
    }

    /// <summary>A plain exception with a message and a cause, for the conventions of the type.</summary>
    public SocketPathTooLongException(string message, Exception innerException)
        : base(message, innerException)
    {
        Path = "";
    }

    /// <summary>The socket path.</summary>
    public string Path { get; }

    /// <summary>Its length in UTF-8 bytes.</summary>
    public int Bytes { get; }
}
