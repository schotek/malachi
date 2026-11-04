// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the per-connection state of
// macos/Sources/MalachiCore/Transport/RPCClient.swift (connection,
// generation); GTK: ui/internal/client/client.go (conn, r).
//
// Swift compares a generation counter after every await; here each dial is
// an object, and a callback of an old connection finds that it is no longer
// the client's current one.

using System;
using System.IO;
using System.Net.Sockets;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;

namespace Malachi.Core.Transport;

/// <summary>One dialled socket of an <see cref="RpcClient"/>, from the dial to its teardown.</summary>
internal sealed class Connection : IDisposable
{
    private readonly SemaphoreSlim writeGate = new(1, 1);
    private readonly CancellationTokenSource closed = new();
    private NetworkStream? stream;
    private LineReader? reader;
    private int disposed;

    public Connection(Socket socket)
    {
        Socket = socket;
    }

    public Socket Socket { get; }

    /// <summary>Reads and writes the socket once it is connected (<see cref="Start"/>); owns it.</summary>
    public NetworkStream Stream => stream ?? throw new InvalidOperationException("the connection is not started");

    /// <summary>The handshake's reader; the read loop takes over what it holds.</summary>
    public LineReader Reader => reader ?? throw new InvalidOperationException("the connection is not started");

    /// <summary>Cancelled when the connection is torn down: every pending read and write ends.</summary>
    public CancellationToken Closed => closed.Token;

    /// <summary>Whether the connection was torn down.</summary>
    public bool IsClosed => Volatile.Read(ref disposed) != 0;

    /// <summary>Wraps the connected socket for reading and writing.</summary>
    public void Start()
    {
        stream = new NetworkStream(Socket, ownsSocket: true);
        reader = new LineReader(stream, RpcAuth.MaxHandshakeLine);
    }

    /// <summary>
    /// Writes one whole line, after the lines of earlier writers: concurrent
    /// writes to a socket may interleave, and a line cut short would break
    /// every later one, so a write is never cancelled once it started.
    /// <paramref name="stillWanted"/> is asked once the line's turn has come;
    /// false sends nothing (the call was cancelled or timed out meanwhile).
    /// </summary>
    public async Task WriteLineAsync(byte[] line, Func<bool> stillWanted)
    {
        await writeGate.WaitAsync(Closed).ConfigureAwait(false);
        try
        {
            if (stillWanted())
            {
                await Stream.WriteAsync(line, CancellationToken.None).ConfigureAwait(false);
            }
        }
        finally
        {
            writeGate.Release();
        }
    }

    public void Dispose()
    {
        if (Interlocked.Exchange(ref disposed, 1) != 0)
        {
            return;
        }
        try
        {
            closed.Cancel();
        }
        catch (AggregateException)
        {
            // A callback of a pending operation threw; the connection goes
            // all the same.
        }
        try
        {
            Socket.Shutdown(SocketShutdown.Both);
        }
        catch (SocketException)
        {
        }
        catch (ObjectDisposedException)
        {
        }
        if (stream is { } s)
        {
            s.Dispose();
        }
        else
        {
            Socket.Dispose();
        }
    }

    /// <summary>
    /// A short, content-free description of a socket failure for the state's
    /// reason: the error's name in fixed English, never the system's
    /// localised message.
    /// </summary>
    public static string Describe(Exception error)
    {
        for (var e = error; e is not null; e = e.InnerException)
        {
            if (e is SocketException s)
            {
                return s.SocketErrorCode switch
                {
                    SocketError.ConnectionRefused => "connection refused",
                    SocketError.ConnectionReset => "connection reset by malachid",
                    SocketError.ConnectionAborted => "connection aborted",
                    SocketError.Shutdown => "the connection was shut down",
                    SocketError.NetworkDown => "the socket's directory does not exist",
                    SocketError.AccessDenied => "access denied",
                    SocketError.AddressNotAvailable => "the socket does not exist",
                    SocketError.TimedOut => "timed out",
                    _ => $"socket error {s.SocketErrorCode}",
                };
            }
        }
        return error switch
        {
            EndOfStreamException => "connection closed by malachid",
            ObjectDisposedException => "the connection was closed",
            IOException io => $"I/O error 0x{io.HResult:x8}",
            _ => error.GetType().Name,
        };
    }
}
