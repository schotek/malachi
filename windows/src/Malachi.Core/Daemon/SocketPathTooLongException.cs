// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Transport/UnixSocketProbe.swift
// (PathTooLong). Windows allows 107 UTF-8 bytes in an AF_UNIX path (the
// 108-byte sun_path with its NUL), the daemon's own limit
// (backend/internal/rpc/server.go), where macOS allows 103.

using System;
using System.Globalization;

namespace Malachi.Core.Daemon;

/// <summary>
/// A socket path longer than an AF_UNIX address holds; the message names
/// <c>MALACHI_SOCKET</c>, which sets a shorter one.
/// </summary>
public sealed class SocketPathTooLongException : Exception
{
    /// <summary>Creates the exception with the default message.</summary>
    public SocketPathTooLongException()
    {
    }

    /// <summary>Creates the exception with <paramref name="message"/>.</summary>
    public SocketPathTooLongException(string message)
        : base(message)
    {
    }

    /// <summary>Creates the exception with <paramref name="message"/> and its cause.</summary>
    public SocketPathTooLongException(string message, Exception innerException)
        : base(message, innerException)
    {
    }

    /// <summary>Creates the exception for <paramref name="path"/>, which is <paramref name="bytes"/> UTF-8 bytes long.</summary>
    public SocketPathTooLongException(string path, int bytes)
        : base(string.Format(
            CultureInfo.InvariantCulture,
            "socket path is {0} bytes, Windows allows {1}: {2} (set MALACHI_SOCKET to a shorter path)",
            bytes,
            Paths.MaxSocketPathBytes,
            path))
    {
        Path = path;
        Bytes = bytes;
    }

    /// <summary>The path that is too long.</summary>
    public string Path { get; } = "";

    /// <summary>Its length in UTF-8 bytes.</summary>
    public int Bytes { get; }
}
