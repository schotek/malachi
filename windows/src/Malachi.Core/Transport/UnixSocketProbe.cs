// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Transport/UnixSocketProbe.swift; GTK:
// ui/internal/daemon/daemon.go (answers).
//
// The Darwin connect(2) of Swift is a non-blocking System.Net.Sockets
// connect here. AF_UNIX on Windows answers at once (docs/windows-port.md §5,
// measured): a missing or stale socket file is ConnectionRefused, a missing
// directory NetworkDown, a listener WouldBlock (the connect is in
// progress), a listener with a full backlog NoBufferSpaceAvailable.

using System;
using System.Net.Sockets;
using System.Text;

namespace Malachi.Core.Transport;

/// <summary>
/// Synchronous checks on the daemon's unix socket, the counterpart of the
/// GTK supervisor's <c>answers()</c> probe.
/// </summary>
public static class UnixSocketProbe
{
    /// <summary>
    /// The longest socket path in UTF-8 bytes: <c>sun_path</c> has 108 bytes
    /// with its terminating NUL on Windows and Linux alike (.NET refuses 108
    /// at <see cref="UnixDomainSocketEndPoint"/>, measured). The daemon checks
    /// the same limit, but late and with a bare error, so the app checks
    /// first.
    /// </summary>
    public const int MaxPathBytes = 107;

    /// <summary>
    /// Throws <see cref="SocketPathTooLongException"/>, whose message names
    /// <c>MALACHI_SOCKET</c>, when <paramref name="path"/> does not fit.
    /// </summary>
    public static void Check(string path)
    {
        ArgumentNullException.ThrowIfNull(path);
        var n = Encoding.UTF8.GetByteCount(path);
        if (n > MaxPathBytes)
        {
            throw new SocketPathTooLongException(path, n);
        }
    }

    /// <summary>
    /// True when something listens on the socket. The connect is
    /// non-blocking: a missing socket or a dead one is refused at once, and a
    /// listener, busy or not, answers that the connect is in progress or that
    /// its backlog is full instead of blocking. The connection is dropped
    /// right away; the daemon logs it at debug level as a client that left
    /// before the handshake.
    /// </summary>
    public static bool Answers(string path)
    {
        ArgumentNullException.ThrowIfNull(path);
        if (path.Length == 0 || Encoding.UTF8.GetByteCount(path) > MaxPathBytes)
        {
            return false;
        }
        UnixDomainSocketEndPoint endpoint;
        try
        {
            endpoint = new UnixDomainSocketEndPoint(path);
        }
        catch (ArgumentException)
        {
            return false;
        }
        using var socket = new Socket(AddressFamily.Unix, SocketType.Stream, ProtocolType.Unspecified) { Blocking = false };
        try
        {
            socket.Connect(endpoint);
            return true;
        }
        catch (SocketException e)
        {
            return IsListening(e.SocketErrorCode);
        }
    }

    /// <summary>
    /// Whether a failed non-blocking connect says that a listener is there:
    /// in progress (Swift's EINPROGRESS), busy (EAGAIN on Linux,
    /// NoBufferSpaceAvailable on Windows) or slow.
    /// </summary>
    public static bool IsListening(SocketError error) => error
        is SocketError.WouldBlock
        or SocketError.InProgress
        or SocketError.AlreadyInProgress
        or SocketError.NoBufferSpaceAvailable
        or SocketError.TimedOut;
}
