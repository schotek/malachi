// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Transport/RPCClient.swift and of the
// typed call of macos/Sources/MalachiCore/API/API.swift (RPCClient.call);
// GTK: ui/internal/client/client.go (Client: Connect, Call, Close,
// readLoop).
//
// Swift's actor over push-based Network.framework callbacks becomes the
// shape of Go's client: a lock, pull-based reads, one reader shared by the
// handshake and the read loop (docs/windows-port.md §5). The public surface
// is Swift's: State, the Notifications and States streams (unbounded, in
// the daemon's order; channels for AsyncStream), the StateChanged and
// NotificationReceived events (setStateHandler, setNotificationHandler),
// ConnectAsync (connect), CallAsync (call), Close (close). A call awaits a
// TaskCompletionSource that completes its continuation elsewhere than on the
// read loop, so no consumer can stall the loop; its timeout runs on the
// TimeProvider; its caller's cancellation is an OperationCanceledException
// (Swift's CancellationError).

using System;
using System.Collections.Generic;
using System.Diagnostics.CodeAnalysis;
using System.IO;
using System.Net.Sockets;
using System.Threading;
using System.Threading.Channels;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Platform;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Transport;

/// <summary>
/// A JSON-RPC 2.0 client for the malachid unix socket, the counterpart of
/// ui/internal/client. It is transport only: it sends requests, matches
/// answers by id and forwards notifications. Reconnecting is the caller's
/// job; <see cref="ConnectAsync"/> is single-shot. Safe for use from any
/// thread.
/// </summary>
/// <remarks>
/// <para>
/// Every connection is authenticated before it is used (docs/api.md §1.4,
/// the port of api.ClientHandshake): <see cref="ConnectAsync"/> runs the
/// handshake after the dial, and <see cref="State"/> stays
/// <see cref="RpcClientState.Connecting"/> meanwhile, so
/// <see cref="CallAsync{TParams, TResult}(RpcMethod{TParams, TResult}, TParams, CancellationToken)"/>
/// refuses with <see cref="ClientErrorKind.NotConnected"/> until both ends
/// proved that they hold the daemon's key. The key file is read afresh on
/// every connection and never kept.
/// </para>
/// <para>
/// State changes and notifications are written to <see cref="States"/> and
/// <see cref="Notifications"/> and raised as <see cref="StateChanged"/> and
/// <see cref="NotificationReceived"/> in the order they happen, under the
/// client's lock: a handler must not block, and must not wait for another
/// thread that uses this client.
/// </para>
/// </remarks>
public sealed partial class RpcClient : IDisposable
{
    /// <summary>How long a dial may take once something listens on the socket.</summary>
    public static readonly TimeSpan ConnectTimeout = TimeSpan.FromSeconds(5);

    private const int ReceiveBufferSize = 64 << 10;

    private readonly Lock gate = new();
    private readonly SemaphoreSlim connectGate = new(1, 1);
    private readonly Channel<RpcNotification> notifications =
        Channel.CreateUnbounded<RpcNotification>(new UnboundedChannelOptions { SingleReader = true });
    private readonly Channel<RpcClientState> states =
        Channel.CreateUnbounded<RpcClientState>(new UnboundedChannelOptions { SingleReader = true });
    private readonly Dictionary<long, TaskCompletionSource<byte[]>> pending = [];
    private readonly TimeSpan handshakeTimeout;
    private readonly IKeyFilePolicy keyFilePolicy;
    private readonly TimeProvider time;
    private readonly ILogger logger;
    private Connection? connection;
    private RpcClientState state = new RpcClientState.Disconnected(null);
    private long nextId = 1;
    private bool disposed;

    /// <summary>A client of the daemon listening on <paramref name="socketPath"/>.</summary>
    /// <param name="socketPath">The daemon's socket; its key file lies beside it (<see cref="RpcAuth.KeyPath"/>).</param>
    /// <param name="handshakeTimeout">Bounds the whole handshake of each connection (<see cref="RpcTimeouts.Handshake"/>); tests shorten it.</param>
    /// <param name="keyFilePolicy">How the key file is opened and checked; the Go clients' rule when null.</param>
    /// <param name="timeProvider">The clock of every timeout.</param>
    /// <param name="logger">Receives method names, codes and reasons, never a key, a nonce, a proof or mail data.</param>
    public RpcClient(
        string socketPath,
        TimeSpan? handshakeTimeout = null,
        IKeyFilePolicy? keyFilePolicy = null,
        TimeProvider? timeProvider = null,
        ILogger<RpcClient>? logger = null)
    {
        ArgumentException.ThrowIfNullOrEmpty(socketPath);
        SocketPath = socketPath;
        this.handshakeTimeout = handshakeTimeout ?? RpcTimeouts.Handshake;
        this.keyFilePolicy = keyFilePolicy ?? PortableKeyFilePolicy.Instance;
        time = timeProvider ?? TimeProvider.System;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
    }

    /// <summary>
    /// Every state change, in order, after <see cref="State"/> changed; raised
    /// under the client's lock (see the remarks of the class).
    /// </summary>
    public event EventHandler<RpcClientState>? StateChanged;

    /// <summary>
    /// Every notification, in the order the daemon sent it; raised on the
    /// read loop under the client's lock (see the remarks of the class).
    /// </summary>
    public event EventHandler<RpcNotification>? NotificationReceived;

    /// <summary>The daemon's socket.</summary>
    public string SocketPath { get; }

    /// <summary>The current state.</summary>
    public RpcClientState State
    {
        get
        {
            lock (gate)
            {
                return state;
            }
        }
    }

    /// <summary>
    /// Every notification, in the order the daemon sent it (a newMessage
    /// never overtakes the syncState that follows it). One consumer only;
    /// buffered while nobody reads. Completed by <see cref="Dispose"/>.
    /// </summary>
    public ChannelReader<RpcNotification> Notifications => notifications.Reader;

    /// <summary>Every state transition, in order; one consumer only. Completed by <see cref="Dispose"/>.</summary>
    public ChannelReader<RpcClientState> States => states.Reader;

    /// <summary>
    /// Dials the socket and authenticates the connection. Returns once it is
    /// usable; throws <see cref="RpcClientException"/> when nothing answers or
    /// the connection breaks, and <see cref="HandshakeException"/> for a
    /// daemon that cannot or must not be used. A connected client returns at
    /// once; concurrent calls take turns. Cancelling ends the attempt with an
    /// <see cref="OperationCanceledException"/> and leaves the client
    /// disconnected.
    /// </summary>
    public async Task ConnectAsync(CancellationToken cancellationToken = default)
    {
        ObjectDisposedException.ThrowIf(disposed, this);
        await connectGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            await ConnectOnceAsync(cancellationToken).ConfigureAwait(false);
        }
        finally
        {
            connectGate.Release();
        }
    }

    /// <summary>
    /// Performs one call with the method's own timeout. A daemon error
    /// arrives as <see cref="RpcException"/>; a lost connection as
    /// <see cref="RpcClientException"/> <see cref="ClientErrorKind.Disconnected"/>;
    /// silence as <see cref="ClientErrorKind.Timeout"/>. Cancelling fails the
    /// call with an <see cref="OperationCanceledException"/>; the request
    /// itself is not recalled, and a late answer is dropped.
    /// </summary>
    public Task<TResult> CallAsync<TParams, TResult>(
        RpcMethod<TParams, TResult> method, TParams parameters, CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(method);
        return CallAsync(method, parameters, method.Timeout, cancellationToken);
    }

    /// <summary>
    /// Performs one call with <paramref name="timeout"/> in place of the
    /// method's own (<see cref="Timeout.InfiniteTimeSpan"/> waits for ever).
    /// </summary>
    public async Task<TResult> CallAsync<TParams, TResult>(
        RpcMethod<TParams, TResult> method, TParams parameters, TimeSpan timeout, CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(method);
        ArgumentNullException.ThrowIfNull(parameters);
        // An already cancelled caller sends nothing.
        cancellationToken.ThrowIfCancellationRequested();
        var answer = new TaskCompletionSource<byte[]>(TaskCreationOptions.RunContinuationsAsynchronously);
        Connection conn;
        long id;
        lock (gate)
        {
            if (connection is not { } c || state is not RpcClientState.Connected)
            {
                throw new RpcClientException(ClientError.NotConnected);
            }
            conn = c;
            id = nextId++;
            pending.Add(id, answer);
        }
        byte[] line;
        try
        {
            line = JsonRpc.EncodeRequest(id, method.Name, parameters, method.ParamsInfo);
        }
        catch
        {
            Forget(id);
            throw;
        }
        using var timer = timeout == Timeout.InfiniteTimeSpan ? null : new CancellationTokenSource(timeout, time);
        using var onTimeout = timer?.Token.UnsafeRegister(_ => Fail(id, new RpcClientException(ClientError.Timeout(method.Name))), null);
        using var onCancel = cancellationToken.UnsafeRegister(_ => Fail(id, new OperationCanceledException(cancellationToken)), null);
        _ = SendAsync(conn, id, line);
        var raw = await answer.Task.ConfigureAwait(false);
        return JsonRpc.ReadResult(raw, method.ResultInfo);
    }

    /// <summary>Drops the connection; pending calls fail with <see cref="ClientErrorKind.Disconnected"/>.</summary>
    public void Close()
    {
        lock (gate)
        {
            if (connection is { } conn)
            {
                Teardown(conn, null);
            }
            else
            {
                SetState(new RpcClientState.Disconnected(null));
            }
        }
    }

    /// <summary>Closes the connection and completes <see cref="Notifications"/> and <see cref="States"/>.</summary>
    public void Dispose()
    {
        lock (gate)
        {
            if (disposed)
            {
                return;
            }
            disposed = true;
        }
        Close();
        notifications.Writer.TryComplete();
        states.Writer.TryComplete();
    }

    private async Task ConnectOnceAsync(CancellationToken cancellationToken)
    {
        lock (gate)
        {
            if (state is RpcClientState.Connected)
            {
                return;
            }
        }
        Close();
        // A probe first: a local daemon that is simply not running must be
        // reported at once, not after a dial's timeout.
        if (!UnixSocketProbe.Answers(SocketPath))
        {
            var reason = $"nothing listens on {SocketPath}";
            lock (gate)
            {
                SetState(new RpcClientState.Disconnected(reason));
            }
            throw new RpcClientException(ClientError.Transport(reason));
        }
        cancellationToken.ThrowIfCancellationRequested();
        var socket = new Socket(AddressFamily.Unix, SocketType.Stream, ProtocolType.Unspecified);
        var conn = new Connection(socket);
        lock (gate)
        {
            if (disposed)
            {
                conn.Dispose();
                throw new ObjectDisposedException(nameof(RpcClient));
            }
            connection = conn;
            SetState(new RpcClientState.Connecting());
        }
        await DialAsync(conn, cancellationToken).ConfigureAwait(false);
        // The socket is open; the connection is usable only once both ends
        // proved that they hold the daemon's key. The state stays Connecting.
        await HandshakeAsync(conn, cancellationToken).ConfigureAwait(false);
        lock (gate)
        {
            if (connection != conn)
            {
                throw new RpcClientException(ClientError.Disconnected);
            }
            SetState(new RpcClientState.Connected());
        }
        // What the daemon sent after the system.authenticate answer is in the
        // reader: the loop delivers it after Connected, in the daemon's order.
        _ = Task.Run(() => ReadLoopAsync(conn), CancellationToken.None);
    }

    private async Task DialAsync(Connection conn, CancellationToken cancellationToken)
    {
        using var deadline = new CancellationTokenSource(ConnectTimeout, time);
        using var linked = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken, deadline.Token, conn.Closed);
        try
        {
            await conn.Socket.ConnectAsync(new UnixDomainSocketEndPoint(SocketPath), linked.Token).ConfigureAwait(false);
            conn.Start();
        }
        catch (Exception e) when (e is OperationCanceledException or SocketException or ObjectDisposedException or IOException)
        {
            if (conn.IsClosed)
            {
                throw new RpcClientException(ClientError.Disconnected, e);
            }
            if (cancellationToken.IsCancellationRequested)
            {
                Abandon(conn, null);
                throw new OperationCanceledException(null, e, cancellationToken);
            }
            if (deadline.IsCancellationRequested)
            {
                Abandon(conn, $"connecting to {SocketPath} timed out");
                throw new RpcClientException(ClientError.Timeout("connect"), e);
            }
            var reason = Connection.Describe(e);
            Abandon(conn, reason);
            throw new RpcClientException(ClientError.Transport(reason), e);
        }
    }

    // A failed write fails its call; the read loop sees the broken
    // connection by itself.
    [SuppressMessage("Design", "CA1031", Justification = "Every failure of a write belongs to its call, which is failed with it.")]
    private async Task SendAsync(Connection conn, long id, byte[] line)
    {
        try
        {
            await conn.WriteLineAsync(line, () => IsPending(id)).ConfigureAwait(false);
        }
        catch (Exception e)
        {
            Fail(id, conn.IsClosed
                ? new RpcClientException(ClientError.Disconnected, e)
                : new RpcClientException(ClientError.Transport(Connection.Describe(e)), e));
        }
    }

    /// <summary>
    /// Reads the connection until it ends: what the handshake left in the
    /// reader first, then the socket, framed at the daemon's cap.
    /// </summary>
    private async Task ReadLoopAsync(Connection conn)
    {
        var framer = new LineFramer();
        var buffer = new byte[ReceiveBufferSize];
        string? reason;
        try
        {
            Dispatch(conn, framer.Append(conn.Reader.Buffered));
            while (true)
            {
                var n = await conn.Stream.ReadAsync(buffer, conn.Closed).ConfigureAwait(false);
                if (n == 0)
                {
                    reason = "connection closed by malachid";
                    break;
                }
                Dispatch(conn, framer.Append(buffer.AsSpan(0, n)));
            }
        }
        catch (LineTooLongException)
        {
            reason = $"a line from malachid exceeds {LineFramer.DefaultMaxLine} bytes";
        }
        catch (Exception e) when (e is IOException or SocketException or ObjectDisposedException or OperationCanceledException)
        {
            if (conn.IsClosed)
            {
                return;
            }
            reason = Connection.Describe(e);
        }
        lock (gate)
        {
            Teardown(conn, reason);
        }
    }

    private void Dispatch(Connection conn, IReadOnlyList<byte[]> lines)
    {
        foreach (var line in lines)
        {
            if (!Envelope.TryParse(line, out var envelope))
            {
                continue; // a malformed line from the daemon is ignored, as the GTK client does
            }
            if (envelope.Id is { } id)
            {
                TaskCompletionSource<byte[]>? answer;
                lock (gate)
                {
                    pending.Remove(id, out answer);
                }
                if (envelope.Error is { } error)
                {
                    answer?.TrySetException(new RpcException(error));
                }
                else
                {
                    answer?.TrySetResult(line);
                }
            }
            else if (envelope.IsNotification)
            {
                var n = new RpcNotification(envelope.Method!, line);
                lock (gate)
                {
                    if (connection != conn)
                    {
                        return;
                    }
                    notifications.Writer.TryWrite(n);
                    Raise(NotificationReceived, n);
                }
            }
        }
    }

    private bool IsPending(long id)
    {
        lock (gate)
        {
            return pending.ContainsKey(id);
        }
    }

    private void Forget(long id)
    {
        lock (gate)
        {
            pending.Remove(id);
        }
    }

    private void Fail(long id, Exception error)
    {
        TaskCompletionSource<byte[]>? answer;
        lock (gate)
        {
            pending.Remove(id, out answer);
        }
        answer?.TrySetException(error);
    }

    /// <summary>Tears a failed attempt down, unless something else did already.</summary>
    private void Abandon(Connection conn, string? reason)
    {
        lock (gate)
        {
            Teardown(conn, reason);
        }
    }

    /// <summary>
    /// Drops <paramref name="conn"/> if it is still the current connection:
    /// pending calls fail with <see cref="ClientErrorKind.Disconnected"/>, and
    /// the state becomes <see cref="RpcClientState.Disconnected"/> with the
    /// reason. Called under the lock.
    /// </summary>
    [SuppressMessage("Reliability", "CA2000", Justification = "The connection is disposed here; the reference is only compared.")]
    private void Teardown(Connection conn, string? reason)
    {
        if (connection != conn)
        {
            return;
        }
        connection = null;
        conn.Dispose();
        if (reason is not null)
        {
            LogEnded(logger, reason);
        }
        foreach (var answer in pending.Values)
        {
            answer.TrySetException(new RpcClientException(ClientError.Disconnected));
        }
        pending.Clear();
        SetState(new RpcClientState.Disconnected(reason));
    }

    /// <summary>Records and announces a state change; nothing for the same state. Called under the lock.</summary>
    private void SetState(RpcClientState next)
    {
        if (next == state)
        {
            return;
        }
        state = next;
        states.Writer.TryWrite(next);
        Raise(StateChanged, next);
    }

    /// <summary>
    /// Calls every handler of an event; one that throws is logged and does
    /// not reach the transport, whose state it would otherwise leave half
    /// changed.
    /// </summary>
    [SuppressMessage("Design", "CA1031", Justification = "A consumer's handler must not break the transport.")]
    private void Raise<T>(EventHandler<T>? handlers, T value)
    {
        if (handlers is null)
        {
            return;
        }
        foreach (var handler in handlers.GetInvocationList())
        {
            try
            {
                ((EventHandler<T>)handler).Invoke(this, value);
            }
            catch (Exception e)
            {
                LogHandlerFailed(logger, e);
            }
        }
    }

    // The reasons are fixed text around the socket's path; no key, nonce,
    // proof or mail data reaches the log.
    [LoggerMessage(Level = LogLevel.Debug, Message = "Connection to malachid ended: {Reason}")]
    private static partial void LogEnded(ILogger logger, string reason);

    [LoggerMessage(Level = LogLevel.Error, Message = "A handler of the RPC client failed")]
    private static partial void LogHandlerFailed(ILogger logger, Exception error);
}
