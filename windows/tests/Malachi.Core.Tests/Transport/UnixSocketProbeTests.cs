// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of the port of macos/Sources/MalachiCore/Transport/UnixSocketProbe.swift
// (the Swift suite exercises it through RPCClientTests and SupervisorTests):
// the AF_UNIX facts of docs/windows-port.md §5, measured on Windows, hold on
// Linux as well.

using System;
using System.Collections.Generic;
using System.IO;
using System.Net.Sockets;
using System.Text;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Xunit;

namespace Malachi.Core.Tests.Transport;

public sealed class UnixSocketProbeTests
{
    [Fact]
    public void ThePathLimitIs107Bytes()
    {
        var dir = Path.GetTempPath();
        var fits = Pad(dir, UnixSocketProbe.MaxPathBytes);
        UnixSocketProbe.Check(fits);
        var tooLong = Pad(dir, UnixSocketProbe.MaxPathBytes + 1);
        var e = Assert.Throws<SocketPathTooLongException>(() => UnixSocketProbe.Check(tooLong));
        Assert.Equal(UnixSocketProbe.MaxPathBytes + 1, e.Bytes);
        Assert.Contains("MALACHI_SOCKET", e.Message, StringComparison.Ordinal);
        Assert.False(UnixSocketProbe.Answers(tooLong));
        // Bytes, not characters: two-byte letters count twice.
        var czech = Path.Combine(dir, new string('ř', 60) + ".sock");
        Assert.True(Encoding.UTF8.GetByteCount(czech) > UnixSocketProbe.MaxPathBytes);
        Assert.Throws<SocketPathTooLongException>(() => UnixSocketProbe.Check(czech));
        // .NET agrees: 107 bytes make an endpoint, 108 do not.
        _ = new UnixDomainSocketEndPoint(fits);
        Assert.ThrowsAny<ArgumentException>(() => new UnixDomainSocketEndPoint(tooLong));
    }

    [Fact]
    public void NothingListensOnAMissingOrStaleSocket()
    {
        var path = FakeDaemon.NewSocketPath();
        Assert.False(UnixSocketProbe.Answers(path));
        Assert.False(UnixSocketProbe.Answers(Path.Combine(Path.GetTempPath(), "m-no-such-dir", "rpc.sock")));
        Assert.False(UnixSocketProbe.Answers(""));
        // A file left behind (a crashed daemon's socket, or anything else).
        File.WriteAllText(path, "x");
        try
        {
            Assert.False(UnixSocketProbe.Answers(path));
        }
        finally
        {
            File.Delete(path);
        }
    }

    [Fact]
    public void AListenerAnswersEvenWithAFullBacklog()
    {
        var path = FakeDaemon.NewSocketPath();
        using var listener = new Socket(AddressFamily.Unix, SocketType.Stream, ProtocolType.Unspecified);
        listener.Bind(new UnixDomainSocketEndPoint(path));
        listener.Listen(1);
        Assert.True(UnixSocketProbe.Answers(path));
        // Nobody accepts: the backlog fills, and the listener is still there.
        var waiting = new List<Socket>();
        try
        {
            for (var i = 0; i < 16; i++)
            {
                var s = new Socket(AddressFamily.Unix, SocketType.Stream, ProtocolType.Unspecified) { Blocking = false };
                waiting.Add(s);
                try
                {
                    s.Connect(new UnixDomainSocketEndPoint(path));
                }
                catch (SocketException)
                {
                }
            }
            Assert.True(UnixSocketProbe.Answers(path));
        }
        finally
        {
            foreach (var s in waiting)
            {
                s.Dispose();
            }
        }
    }

    [Fact]
    public void BusyAndInProgressCountAsAListener()
    {
        Assert.True(UnixSocketProbe.IsListening(SocketError.WouldBlock));
        Assert.True(UnixSocketProbe.IsListening(SocketError.InProgress));
        Assert.True(UnixSocketProbe.IsListening(SocketError.NoBufferSpaceAvailable));
        Assert.True(UnixSocketProbe.IsListening(SocketError.TimedOut));
        Assert.False(UnixSocketProbe.IsListening(SocketError.ConnectionRefused));
        Assert.False(UnixSocketProbe.IsListening(SocketError.NetworkDown));
        Assert.False(UnixSocketProbe.IsListening(SocketError.AddressNotAvailable));
    }

    // A path in dir of exactly bytes UTF-8 bytes.
    private static string Pad(string dir, int bytes)
    {
        var start = Path.Combine(dir, "m-");
        return start + new string('x', bytes - Encoding.UTF8.GetByteCount(start));
    }
}
