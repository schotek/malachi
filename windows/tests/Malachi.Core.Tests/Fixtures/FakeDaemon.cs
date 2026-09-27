// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/Fixtures/FakeDaemon.swift; Go:
// ui/internal/client/handshake_test.go (fakeDaemon: the ended connections),
// backend/pkg/api/handshake_test.go (the scripted daemon of HandshakeMode.Raw),
// backend/internal/rpc (the daemon's side of docs/api.md §1.4).
//
// Swift's actor over NWListener is a lock-protected class over a real
// AF_UNIX socket (System.Net.Sockets) at a short path in the temporary
// directory: sun_path takes 107 UTF-8 bytes on Windows and Linux, and the
// test directories are longer than that. The key file is written through a
// temporary file and File.Move(overwrite), the daemon's atomic replace,
// retried while a reader holds the old file as the daemon retries it;
// Windows has no 0600, and the temporary directory's ACL (the user, SYSTEM,
// Administrators) is what the Windows key-file policy accepts. Additions
// for the C# tests: EndedCount/WaitForEndedAsync (Go's waitEnded: once the
// client closed a connection, nothing more can come on it), InFlight and
// IdleAsync (Quiescence), Pushed.

using System;
using System.Collections.Generic;
using System.IO;
using System.Net.Sockets;
using System.Text;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Transport;

namespace Malachi.Core.Tests.Fixtures;

/// <summary>
/// An in-process stand-in for malachid: a unix-socket listener speaking the
/// same newline-delimited JSON-RPC, handshake included (docs/api.md §1.4).
/// </summary>
/// <remarks>
/// <para>
/// <see cref="StartAsync"/> writes a fresh key beside the socket
/// (<see cref="KeyPath"/>), as the daemon does at every start, and the
/// daemon's side of the handshake is answered here, line by line and in
/// order: <c>system.hello</c>, then <c>system.authenticate</c>. Those lines
/// are recorded in <see cref="Handshakes"/>, never in <see cref="Calls"/>, so
/// a test counting calls sees only what the client asked once it was
/// connected. Before a connection is authenticated every other request is
/// refused with 1005 <c>unauthenticated</c> and the connection is closed;
/// notifications go to authenticated connections only.
/// <see cref="SetHandshake"/> turns the daemon's side into another daemon's,
/// or into a script (<see cref="HandshakeMode"/>); <see cref="Restart"/> and
/// <see cref="RotateKey"/> give it a new key.
/// </para>
/// <para>
/// Answers come from per-method handlers registered with <c>On</c>, which get
/// the params object as JSON ("" when absent) and return the result JSON or
/// throw an <see cref="RpcException"/>. A method nobody registered goes to
/// the generic <see cref="Handler"/> of the constructor, whose default
/// answers methodNotFound (-32601). Calls are served concurrently, as the
/// daemon does.
/// </para>
/// </remarks>
internal sealed class FakeDaemon : IAsyncDisposable
{
    /// <summary>The generic handler: the method and the whole request line.</summary>
    public delegate Task<FakeAnswer> Handler(string method, byte[] line);

    /// <summary>A method's handler: the params JSON ("" when absent) to the result JSON; throws <see cref="RpcException"/> to fail.</summary>
    public delegate Task<string> MethodHandler(string paramsJson);

    /// <summary>JSON-RPC's own code for an unknown method (docs/api.md §2).</summary>
    public static readonly ErrorCode MethodNotFoundCode = ErrorCode.MethodNotFound;

    /// <summary>JSON-RPC's own code for a handler that threw something else.</summary>
    public static readonly ErrorCode InternalErrorCode = ErrorCode.InternalError;

    /// <summary>The default generic handler: methodNotFound.</summary>
    public static readonly Handler MethodNotFound = (method, _) =>
        Task.FromResult(FakeAnswer.Failure(new RpcError { Code = MethodNotFoundCode, Message = $"unknown method {method}" }));

    /// <summary>The pauses between the attempts to replace the key file (backend/internal/fsretry Waits).</summary>
    private static readonly TimeSpan[] RenameWaits =
    [
        TimeSpan.FromMilliseconds(20), TimeSpan.FromMilliseconds(40), TimeSpan.FromMilliseconds(80), TimeSpan.FromMilliseconds(160),
        TimeSpan.FromMilliseconds(200), TimeSpan.FromMilliseconds(200), TimeSpan.FromMilliseconds(200), TimeSpan.FromMilliseconds(200),
        TimeSpan.FromMilliseconds(200),
    ];

    private readonly Lock gate = new();
    private readonly Handler handler;
    private readonly Dictionary<string, MethodHandler> methods = [];
    private readonly List<Peer> peers = [];
    private readonly List<string> calls = [];
    private readonly List<string> handshakes = [];
    private readonly List<string> received = [];
    private readonly List<string> receivedLines = [];
    private readonly List<string> secrets = [];
    private readonly CancellationTokenSource stopping = new();
    private Socket? listener;
    private Task? acceptLoop;
    private byte[] authKey = [];
    private HandshakeMode handshake = new HandshakeMode.Normal();
    private int notificationsBeforeHello;
    private string? notificationWithAuthenticateAnswer;
    private int accepted;
    private int ended;
    private int inFlight;
    private int pushed;
    private TaskCompletionSource? idle;
    private TaskCompletionSource endedChanged = NewSignal();

    /// <summary>
    /// A daemon on <paramref name="path"/>, or on a new short socket path in
    /// the temporary directory; <see cref="StartAsync"/> makes it listen.
    /// </summary>
    public FakeDaemon(Handler? handler = null, string? path = null)
    {
        // Short and unique: sun_path has 107 bytes.
        Path = path ?? NewSocketPath();
        if (Encoding.UTF8.GetByteCount(Path) > UnixSocketProbe.MaxPathBytes)
        {
            throw new InvalidOperationException($"the temporary directory makes the socket path {Path} too long");
        }
        KeyPath = RpcAuth.KeyPath(Path);
        this.handler = handler ?? MethodNotFound;
    }

    /// <summary>A new short socket path in the temporary directory, which nothing uses.</summary>
    public static string NewSocketPath() =>
        System.IO.Path.Combine(System.IO.Path.GetTempPath(), $"m-{Guid.NewGuid().ToString("N")[..8]}.sock");

    /// <summary>The socket path to dial.</summary>
    public string Path { get; }

    /// <summary>The key file beside the socket (docs/api.md §1.4).</summary>
    public string KeyPath { get; }

    /// <summary>Connections accepted so far.</summary>
    public int Accepted
    {
        get
        {
            lock (gate)
            {
                return accepted;
            }
        }
    }

    /// <summary>Connections that ended so far: the client closed them, or the daemon did.</summary>
    public int EndedCount
    {
        get
        {
            lock (gate)
            {
                return ended;
            }
        }
    }

    /// <summary>Every method served, in order; the handshake's lines are not.</summary>
    public IReadOnlyList<string> Calls => Snapshot(calls);

    /// <summary>
    /// What the handshake saw, in order: the method of every line a
    /// connection sent before it was authenticated ("" for a line without
    /// one), and every later <c>system.hello</c> or <c>system.authenticate</c>.
    /// </summary>
    public IReadOnlyList<string> Handshakes => Snapshot(handshakes);

    /// <summary>The method of every line as it arrived, handshake and calls alike.</summary>
    public IReadOnlyList<string> Received => Snapshot(received);

    /// <summary>Every line as it arrived, without its "\n" (Go's fakeDaemon.lines).</summary>
    public IReadOnlyList<string> ReceivedLines => Snapshot(receivedLines);

    /// <summary>
    /// Every key, nonce and proof of this daemon so far, as lowercase hex:
    /// what no error text may show (api TestHandshakeErrorsCarryNoSecrets).
    /// </summary>
    public IReadOnlyList<string> Secrets => Snapshot(secrets);

    /// <summary>Calls being served: read, not yet answered.</summary>
    public int InFlight
    {
        get
        {
            lock (gate)
            {
                return inFlight;
            }
        }
    }

    /// <summary>Notifications written to authenticated connections so far.</summary>
    public int Pushed
    {
        get
        {
            lock (gate)
            {
                return pushed;
            }
        }
    }

    /// <summary>The key in the key file, which the handshake proves with.</summary>
    public byte[] Key
    {
        get
        {
            lock (gate)
            {
                return (byte[])authKey.Clone();
            }
        }
    }

    /// <summary>
    /// Registers the answer for <paramref name="method"/>; consulted before
    /// the generic handler. Registering again replaces the earlier handler.
    /// </summary>
    public void On(string method, MethodHandler methodHandler)
    {
        lock (gate)
        {
            methods[method] = methodHandler;
        }
    }

    /// <summary>Registers a synchronous answer for <paramref name="method"/>.</summary>
    public void On(string method, Func<string, string> answer) =>
        On(method, p => Task.FromResult(answer(p)));

    /// <summary>
    /// Switches the daemon's side of the handshake for the connections
    /// accepted from now on.
    /// </summary>
    public void SetHandshake(HandshakeMode mode)
    {
        lock (gate)
        {
            handshake = mode;
        }
    }

    /// <summary>
    /// Sends <paramref name="n"/> notifications (<c>notify.early</c>) right
    /// before each <c>system.hello</c> answer, in the same write, as a daemon
    /// of protocol 1 broadcasting would.
    /// </summary>
    public void SetNotificationsBeforeHello(int n)
    {
        lock (gate)
        {
            notificationsBeforeHello = n;
        }
    }

    /// <summary>
    /// Sends a notification of this method in the same write as each
    /// <c>system.authenticate</c> answer, right after it; null sends none.
    /// </summary>
    public void SetNotificationWithAuthenticateAnswer(string? method)
    {
        lock (gate)
        {
            notificationWithAuthenticateAnswer = method;
        }
    }

    /// <summary>Writes the key file, then listens; returns once the socket is bound.</summary>
    public Task StartAsync()
    {
        RotateKey();
        TryDelete(Path);
        var socket = new Socket(AddressFamily.Unix, SocketType.Stream, ProtocolType.Unspecified);
        socket.Bind(new UnixDomainSocketEndPoint(Path));
        socket.Listen(64);
        listener = socket;
        acceptLoop = Task.Run(AcceptLoopAsync);
        return Task.CompletedTask;
    }

    /// <summary>
    /// Writes a new key to <see cref="KeyPath"/> (replaced atomically, as the
    /// daemon does); later handshakes prove with it, authenticated
    /// connections stay.
    /// </summary>
    public void RotateKey()
    {
        var fresh = RpcAuth.NewNonce();
        var tmp = $"{KeyPath}.tmp-{Guid.NewGuid().ToString("N")[..8]}";
        try
        {
            File.WriteAllBytes(tmp, Encoding.ASCII.GetBytes(RpcAuth.Hex(fresh) + "\n"));
            ReplaceKeyFile(tmp, KeyPath);
        }
        catch
        {
            TryDelete(tmp);
            throw;
        }
        lock (gate)
        {
            authKey = fresh;
            secrets.Add(RpcAuth.Hex(fresh));
        }
    }

    /// <summary>The daemon restarted: every connection is gone and the key file holds a new key.</summary>
    public void Restart()
    {
        CloseAll();
        RotateKey();
    }

    /// <summary>Pushes a notification to every authenticated client, as the daemon does.</summary>
    public async Task PushNotificationAsync(string method, string paramsJson)
    {
        var line = Notification(method, paramsJson);
        List<Peer> targets = [];
        lock (gate)
        {
            foreach (var p in peers)
            {
                if (p.State is PeerState.Authenticated)
                {
                    targets.Add(p);
                    pushed++;
                }
            }
        }
        foreach (var p in targets)
        {
            await SendAsync(p, line).ConfigureAwait(false);
        }
    }

    /// <summary>
    /// Writes <paramref name="data"/> as it is to every authenticated client:
    /// a line no C# string can carry (bytes that are not UTF-8), or a line
    /// cut short. Not counted in <see cref="Pushed"/>.
    /// </summary>
    public async Task PushRawAsync(byte[] data)
    {
        List<Peer> targets = [];
        lock (gate)
        {
            targets.AddRange(peers.FindAll(p => p.State is PeerState.Authenticated));
        }
        foreach (var p in targets)
        {
            await SendAsync(p, data).ConfigureAwait(false);
        }
    }

    /// <summary>Closes every client connection (the daemon went away).</summary>
    public void CloseAll()
    {
        List<Peer> all;
        lock (gate)
        {
            all = [.. peers];
        }
        foreach (var p in all)
        {
            Forget(p);
        }
    }

    /// <summary>Stops listening, closes every connection, removes the socket and the key file.</summary>
    public async Task StopAsync()
    {
        stopping.Cancel();
        CloseAll();
        listener?.Dispose();
        if (acceptLoop is { } loop)
        {
            try
            {
                await loop.ConfigureAwait(false);
            }
            catch (Exception e) when (e is OperationCanceledException or SocketException or ObjectDisposedException)
            {
            }
        }
        TryDelete(Path);
        TryDelete(KeyPath);
    }

    public ValueTask DisposeAsync() => new(StopAsync());

    /// <summary>Completes when no call is being served.</summary>
    public Task IdleAsync()
    {
        lock (gate)
        {
            return idle is null ? Task.CompletedTask : idle.Task;
        }
    }

    /// <summary>
    /// Waits until <paramref name="count"/> connections have ended: whatever
    /// the client sent on them has arrived, and nothing more can.
    /// </summary>
    public async Task WaitForEndedAsync(int count, TimeSpan? timeout = null)
    {
        var deadline = DateTime.UtcNow + (timeout ?? TimeSpan.FromSeconds(10));
        while (true)
        {
            Task changed;
            lock (gate)
            {
                if (ended >= count)
                {
                    return;
                }
                changed = endedChanged.Task;
            }
            var left = deadline - DateTime.UtcNow;
            if (left <= TimeSpan.Zero)
            {
                throw new TimeoutException($"{EndedCount} of {count} connections ended");
            }
            await changed.WaitAsync(left).ConfigureAwait(false);
        }
    }

    /// <summary>The system.hello result as the daemon writes it.</summary>
    public static string HelloResult(int version, byte[] daemonNonce, string proof) =>
        $$"""{"protocolVersion":{{version}},"daemonNonce":"{{RpcAuth.Hex(daemonNonce)}}","daemonProof":"{{proof}}"}""";

    /// <summary>The <c>params</c> member of a request line as JSON on its own, or "" when the request has none.</summary>
    public static string ParamsJson(byte[] line)
    {
        try
        {
            using var document = JsonDocument.Parse(line);
            return document.RootElement.ValueKind == JsonValueKind.Object && document.RootElement.TryGetProperty("params", out var p)
                ? p.GetRawText()
                : "";
        }
        catch (JsonException)
        {
            return "";
        }
    }

    /// <summary>The <c>method</c> of a line, "" when it has none.</summary>
    public static string MethodOf(byte[] line)
    {
        try
        {
            using var document = JsonDocument.Parse(line);
            return document.RootElement.ValueKind == JsonValueKind.Object
                && document.RootElement.TryGetProperty("method", out var m) && m.ValueKind == JsonValueKind.String
                ? m.GetString()!
                : "";
        }
        catch (JsonException)
        {
            return "";
        }
    }

    /// <summary>An answer line with the result JSON.</summary>
    public static byte[] Response(long id, string resultJson) =>
        Encoding.UTF8.GetBytes($$"""{"jsonrpc":"2.0","id":{{id}},"result":{{resultJson}}}""" + "\n");

    /// <summary>An error answer line.</summary>
    public static byte[] Response(long id, RpcError error) =>
        Encoding.UTF8.GetBytes($$"""{"jsonrpc":"2.0","id":{{id}},"error":{{JsonCoding.EncodeToString(error)}}}""" + "\n");

    /// <summary>A notification line.</summary>
    public static byte[] Notification(string method, string paramsJson) =>
        Encoding.UTF8.GetBytes($$"""{"jsonrpc":"2.0","method":"{{method}}","params":{{paramsJson}}}""" + "\n");

    private static TaskCompletionSource NewSignal() => new(TaskCreationOptions.RunContinuationsAsynchronously);

    /// <summary>
    /// Moves the new key file over the old one as the daemon's writeKeyFile
    /// does (fsretry.Rename): Windows refuses to replace a file while a
    /// reader holds it, even one that shares delete (docs/windows-port.md
    /// §5), so a client reading the key right then holds the move up for a
    /// moment. The same waits as fsretry.Waits; the last failure is thrown.
    /// </summary>
    private static void ReplaceKeyFile(string from, string to)
    {
        for (var attempt = 0; ; attempt++)
        {
            try
            {
                File.Move(from, to, overwrite: true);
                return;
            }
            catch (Exception e) when ((e is IOException or UnauthorizedAccessException) && attempt < RenameWaits.Length)
            {
                Thread.Sleep(RenameWaits[attempt]);
            }
        }
    }

    private static void TryDelete(string path)
    {
        try
        {
            File.Delete(path);
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException)
        {
        }
    }

    private IReadOnlyList<string> Snapshot(List<string> list)
    {
        lock (gate)
        {
            return [.. list];
        }
    }

    private async Task AcceptLoopAsync()
    {
        var socket = listener!;
        while (!stopping.IsCancellationRequested)
        {
            Socket conn;
            try
            {
                conn = await socket.AcceptAsync(stopping.Token).ConfigureAwait(false);
            }
            catch (Exception e) when (e is OperationCanceledException or SocketException or ObjectDisposedException)
            {
                return;
            }
            Peer peer;
            lock (gate)
            {
                // A daemon of protocol 1 has no handshake: it serves at once.
                peer = new Peer(conn, handshake, handshake is HandshakeMode.OldDaemon ? new PeerState.Authenticated() : new PeerState.New());
                peers.Add(peer);
                accepted++;
            }
            if (peer.Mode is HandshakeMode.Hangup)
            {
                Forget(peer);
                continue;
            }
            _ = Task.Run(() => ReadLoopAsync(peer));
        }
    }

    private async Task ReadLoopAsync(Peer peer)
    {
        var framer = new LineFramer();
        var buffer = new byte[64 << 10];
        try
        {
            while (true)
            {
                var n = await peer.Socket.ReceiveAsync(buffer, SocketFlags.None, peer.Closed.Token).ConfigureAwait(false);
                if (n == 0)
                {
                    break;
                }
                foreach (var line in framer.Append(buffer.AsSpan(0, n)))
                {
                    var method = MethodOf(line);
                    PeerState state;
                    lock (gate)
                    {
                        received.Add(method);
                        receivedLines.Add(Encoding.UTF8.GetString(line));
                        state = peer.State;
                    }
                    switch (state)
                    {
                        case PeerState.Authenticated when method != API.SystemHello.Name && method != API.SystemAuthenticate.Name:
                            // Calls are served concurrently, as the daemon does
                            // once a connection is authenticated.
                            BeginServing();
                            _ = Task.Run(() => ServeAsync(peer, line));
                            break;
                        case PeerState.Done:
                            break;
                        default:
                            // The handshake is answered in order, line by line,
                            // in the read loop, as the daemon does.
                            await HandshakeLineAsync(peer, line, method).ConfigureAwait(false);
                            if (peer.IsForgotten)
                            {
                                return;
                            }
                            break;
                    }
                }
            }
        }
        catch (Exception e) when (e is OperationCanceledException or SocketException or ObjectDisposedException or LineTooLongException)
        {
        }
        finally
        {
            Forget(peer);
        }
    }

    // A line of a connection that is not authenticated, or a handshake method
    // on one that is: docs/api.md §1.4's table, the daemon's side, in the mode
    // the connection was accepted in.
    private async Task HandshakeLineAsync(Peer peer, byte[] line, string method)
    {
        lock (gate)
        {
            handshakes.Add(method);
        }
        if (IncomingRequest.Parse(line) is not { } req)
        {
            // Not a request with an id: closed without an answer.
            Forget(peer);
            return;
        }
        var mode = peer.Mode;
        switch (peer.State)
        {
            case PeerState.Authenticated:
                if (mode is HandshakeMode.OldDaemon && req.Method == API.SystemHello.Name)
                {
                    var @out = new List<byte>(EarlyNotifications());
                    @out.AddRange(Response(req.Id, new RpcError { Code = ErrorCode.MethodNotFound, Message = "unknown method \"system.hello\"" }));
                    await SendAsync(peer, [.. @out]).ConfigureAwait(false);
                }
                else
                {
                    await SendAsync(peer, Response(req.Id, new RpcError { Code = ErrorCode.InvalidRequest, Message = "already authenticated" })).ConfigureAwait(false);
                }
                break;
            case PeerState.New:
                if (req.Method != API.SystemHello.Name || ParamString(line, "clientNonce") is not { } nonceText
                    || RpcAuth.DecodeHex32(nonceText) is not { } clientNonce)
                {
                    await RefuseAsync(peer, req.Id).ConfigureAwait(false);
                    return;
                }
                await AnswerHelloAsync(peer, req.Id, clientNonce, mode).ConfigureAwait(false);
                break;
            case PeerState.HelloAnswered answered:
                byte[] key;
                lock (gate)
                {
                    key = authKey;
                }
                if (req.Method != API.SystemAuthenticate.Name || ParamString(line, "clientProof") is not { } proofText
                    || RpcAuth.DecodeHex32(proofText) is not { } proof
                    || !proof.AsSpan().SequenceEqual(RpcAuth.ClientProof(key, answered.ClientNonce, answered.DaemonNonce)))
                {
                    await RefuseAsync(peer, req.Id).ConfigureAwait(false);
                    return;
                }
                switch (mode)
                {
                    case HandshakeMode.RejectClient:
                        await RefuseAsync(peer, req.Id).ConfigureAwait(false);
                        break;
                    case HandshakeMode.Raw raw:
                        SetState(peer, new PeerState.Done());
                        var scripted = raw.Script.Authenticate(RpcAuth.Hex(answered.ClientNonce)) ?? "";
                        await WriteAsync(peer, raw.Script.Encode(scripted), raw.Script.CloseAfterAuthenticate).ConfigureAwait(false);
                        break;
                    default:
                        // Authenticated in the same step as the answer is
                        // written, with the connection's writes held: no
                        // notification can go out before it.
                        await peer.WriteGate.WaitAsync().ConfigureAwait(false);
                        try
                        {
                            byte[] answer;
                            lock (gate)
                            {
                                peer.State = new PeerState.Authenticated();
                                var bytes = new List<byte>(Response(req.Id, "{}"));
                                if (notificationWithAuthenticateAnswer is { } m)
                                {
                                    bytes.AddRange(Notification(m, "{}"));
                                }
                                answer = [.. bytes];
                            }
                            await SendHeldAsync(peer, answer).ConfigureAwait(false);
                        }
                        finally
                        {
                            peer.WriteGate.Release();
                        }
                        break;
                }
                break;
            default:
                break;
        }
    }

    private async Task AnswerHelloAsync(Peer peer, long id, byte[] clientNonce, HandshakeMode mode)
    {
        var daemonNonce = RpcAuth.NewNonce();
        byte[] key;
        lock (gate)
        {
            key = authKey;
        }
        var rightProof = RpcAuth.DaemonProof(key, clientNonce, daemonNonce);
        var clientProof = RpcAuth.ClientProof(key, clientNonce, daemonNonce);
        lock (gate)
        {
            secrets.AddRange([RpcAuth.Hex(clientNonce), RpcAuth.Hex(daemonNonce), RpcAuth.Hex(rightProof), RpcAuth.Hex(clientProof)]);
        }
        var version = API.ProtocolVersion;
        var proof = RpcAuth.Hex(rightProof);
        switch (mode)
        {
            case HandshakeMode.ProtocolVersion v:
                version = v.Version;
                break;
            case HandshakeMode.WrongProof:
                var otherKey = RpcAuth.NewNonce();
                proof = RpcAuth.Hex(RpcAuth.DaemonProof(otherKey, clientNonce, daemonNonce));
                lock (gate)
                {
                    secrets.AddRange([RpcAuth.Hex(otherKey), proof]);
                }
                break;
            case HandshakeMode.MalformedProof:
                proof = proof[..^1];
                break;
            case HandshakeMode.Silent:
                return;
            case HandshakeMode.Raw raw:
                SetState(peer, new PeerState.HelloAnswered(clientNonce, daemonNonce));
                var right = HelloResult(version, daemonNonce, proof);
                var scripted = raw.Script.Hello(new HelloContext(right, clientNonce, daemonNonce, key)) ?? "";
                await WriteAsync(peer, raw.Script.Encode(scripted), raw.Script.CloseAfterHello).ConfigureAwait(false);
                return;
            default:
                break;
        }
        SetState(peer, new PeerState.HelloAnswered(clientNonce, daemonNonce));
        var @out = new List<byte>(EarlyNotifications());
        @out.AddRange(Response(id, HelloResult(version, daemonNonce, proof)));
        await SendAsync(peer, [.. @out]).ConfigureAwait(false);
    }

    // The notifications SetNotificationsBeforeHello asked for.
    private byte[] EarlyNotifications()
    {
        int n;
        lock (gate)
        {
            n = notificationsBeforeHello;
        }
        var @out = new List<byte>();
        for (var i = 0; i < n; i++)
        {
            @out.AddRange(Notification("notify.early", "{}"));
        }
        return [.. @out];
    }

    // Refuses a request before authentication, as the daemon does: 1005 with
    // its id, then the connection is closed (here its write side, so the
    // answer is delivered first).
    private Task RefuseAsync(Peer peer, long id) =>
        WriteAsync(peer, Response(id, new RpcError { Code = ErrorCode.Unauthenticated, Message = "unauthenticated" }), close: true);

    // Writes data (nothing when empty) and, when asked, closes the
    // connection's write side after it; a closed connection is done.
    private async Task WriteAsync(Peer peer, byte[] data, bool close)
    {
        if (close)
        {
            SetState(peer, new PeerState.Done());
        }
        if (data.Length > 0)
        {
            await SendAsync(peer, data).ConfigureAwait(false);
        }
        if (close)
        {
            await peer.WriteGate.WaitAsync().ConfigureAwait(false);
            try
            {
                peer.Socket.Shutdown(SocketShutdown.Send);
            }
            catch (Exception e) when (e is SocketException or ObjectDisposedException)
            {
            }
            finally
            {
                peer.WriteGate.Release();
            }
        }
    }

    private async Task ServeAsync(Peer peer, byte[] line)
    {
        try
        {
            if (IncomingRequest.Parse(line) is not { } req)
            {
                return;
            }
            MethodHandler? method;
            lock (gate)
            {
                calls.Add(req.Method);
                methods.TryGetValue(req.Method, out method);
            }
            FakeAnswer outcome;
            try
            {
                outcome = method is not null
                    ? FakeAnswer.Result(await method(ParamsJson(line)).ConfigureAwait(false))
                    : await handler(req.Method, line).ConfigureAwait(false);
            }
            catch (RpcException e)
            {
                outcome = FakeAnswer.Failure(e.Error);
            }
            catch (OperationCanceledException)
            {
                // A handler that waits for ever, ended with its test.
                return;
            }
            catch (Exception e)
            {
                outcome = FakeAnswer.Failure(new RpcError { Code = InternalErrorCode, Message = e.Message });
            }
            await SendAsync(peer, outcome.Error is { } error ? Response(req.Id, error) : Response(req.Id, outcome.ResultJson ?? "{}")).ConfigureAwait(false);
        }
        finally
        {
            EndServing();
        }
    }

    private void BeginServing()
    {
        lock (gate)
        {
            inFlight++;
            idle ??= NewSignal();
        }
    }

    private void EndServing()
    {
        TaskCompletionSource? nowIdle = null;
        lock (gate)
        {
            if (--inFlight == 0)
            {
                nowIdle = idle;
                idle = null;
            }
        }
        nowIdle?.TrySetResult();
    }

    // Writes data after the connection's earlier writes, whole.
    private static async Task SendAsync(Peer peer, byte[] data)
    {
        await peer.WriteGate.WaitAsync().ConfigureAwait(false);
        try
        {
            await SendHeldAsync(peer, data).ConfigureAwait(false);
        }
        finally
        {
            peer.WriteGate.Release();
        }
    }

    // Writes data while the caller holds the connection's write gate.
    private static async Task SendHeldAsync(Peer peer, byte[] data)
    {
        try
        {
            if (peer.IsForgotten)
            {
                return;
            }
            var sent = 0;
            while (sent < data.Length)
            {
                sent += await peer.Socket.SendAsync(data.AsMemory(sent), SocketFlags.None).ConfigureAwait(false);
            }
        }
        catch (Exception e) when (e is SocketException or ObjectDisposedException)
        {
            // The client went away; its read loop forgets the connection.
        }
    }

    private void SetState(Peer peer, PeerState state)
    {
        lock (gate)
        {
            peer.State = state;
        }
    }

    private void Forget(Peer peer)
    {
        TaskCompletionSource signal;
        lock (gate)
        {
            if (!peers.Remove(peer))
            {
                return;
            }
            ended++;
            signal = endedChanged;
            endedChanged = NewSignal();
        }
        peer.Close();
        signal.TrySetResult();
    }

    // A string member of the request's params, or null.
    private static string? ParamString(byte[] line, string name)
    {
        try
        {
            using var document = JsonDocument.Parse(line);
            return document.RootElement.TryGetProperty("params", out var p) && p.ValueKind == JsonValueKind.Object
                && p.TryGetProperty(name, out var v) && v.ValueKind == JsonValueKind.String
                ? v.GetString()
                : null;
        }
        catch (JsonException)
        {
            return null;
        }
    }

    /// <summary>A request with an integer id and a string method; null for any other line.</summary>
    private sealed record IncomingRequest(long Id, string Method)
    {
        public static IncomingRequest? Parse(byte[] line)
        {
            try
            {
                using var document = JsonDocument.Parse(line);
                var root = document.RootElement;
                return root.ValueKind == JsonValueKind.Object
                    && root.TryGetProperty("id", out var id) && id.ValueKind == JsonValueKind.Number && id.TryGetInt64(out var n)
                    && root.TryGetProperty("method", out var m) && m.ValueKind == JsonValueKind.String
                    ? new IncomingRequest(n, m.GetString()!)
                    : null;
            }
            catch (JsonException)
            {
                return null;
            }
        }
    }

    /// <summary>A connection's progress through the handshake.</summary>
    private abstract record PeerState
    {
        public sealed record New : PeerState;

        public sealed record HelloAnswered(byte[] ClientNonce, byte[] DaemonNonce) : PeerState;

        public sealed record Authenticated : PeerState;

        /// <summary>Refused, or its script has run: what the client still sends is recorded and ignored.</summary>
        public sealed record Done : PeerState;
    }

    /// <summary>One accepted connection, with the handshake mode it was accepted in.</summary>
    private sealed class Peer(Socket socket, HandshakeMode mode, PeerState state)
    {
        private int forgotten;

        public Socket Socket { get; } = socket;

        public HandshakeMode Mode { get; } = mode;

        public PeerState State { get; set; } = state;

        public SemaphoreSlim WriteGate { get; } = new(1, 1);

        public CancellationTokenSource Closed { get; } = new();

        public bool IsForgotten => Volatile.Read(ref forgotten) != 0;

        public void Close()
        {
            if (Interlocked.Exchange(ref forgotten, 1) != 0)
            {
                return;
            }
            Closed.Cancel();
            Socket.Dispose();
        }
    }
}
