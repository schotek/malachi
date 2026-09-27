// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Transport/RPCClient.swift (the
// handshake: handshake, exchangeProofs, sendHandshake, handshakeAnswer,
// helloMember, HandshakeLine); Go: backend/pkg/api/handshake.go
// (ClientHandshake, answer, readLine, ioError, handshakeLine).
//
// Go reads through the caller's bufio.Reader with a deadline on the
// connection; here the connection's LineReader reads, and the deadline is a
// CancellationTokenSource on the TimeProvider. Where Swift and Go differ,
// Go decides (an id must be the text 1 exactly; a member of the wrong type
// breaks the line).

using System;
using System.IO;
using System.Net.Sockets;
using System.Security.Cryptography;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;

namespace Malachi.Core.Transport;

public sealed partial class RpcClient
{
    /// <summary>
    /// Authenticates <paramref name="conn"/> (docs/api.md §1.4), or tears it
    /// down and throws. The whole exchange is bounded by the handshake
    /// timeout; past it nothing more is read from the socket or sent.
    /// </summary>
    private async Task HandshakeAsync(Connection conn, CancellationToken cancellationToken)
    {
        using var deadline = new CancellationTokenSource(handshakeTimeout, time);
        using var linked = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken, deadline.Token, conn.Closed);
        try
        {
            await ExchangeProofsAsync(conn, deadline.Token, linked.Token).ConfigureAwait(false);
        }
        catch (HandshakeException e)
        {
            Abandon(conn, e.Message);
            throw;
        }
        catch (LineTooLongException e)
        {
            throw Refuse(conn, HandshakeError.Malformed("a line is longer than 64 KiB"), e);
        }
        catch (Exception e) when (e is OperationCanceledException or IOException or SocketException or ObjectDisposedException)
        {
            if (conn.IsClosed)
            {
                // Close() or a newer ConnectAsync tore it down.
                throw new RpcClientException(ClientError.Disconnected, e);
            }
            if (cancellationToken.IsCancellationRequested)
            {
                Abandon(conn, null);
                throw new OperationCanceledException(null, e, cancellationToken);
            }
            if (deadline.IsCancellationRequested)
            {
                throw Refuse(conn, HandshakeError.TimedOut, e);
            }
            // The connection broke: a plain error, as Go's, handled like any
            // dropped connection.
            var reason = e is EndOfStreamException ? "connection closed by malachid" : Connection.Describe(e);
            Abandon(conn, reason);
            throw new RpcClientException(ClientError.Transport(reason), e);
        }
    }

    private HandshakeException Refuse(Connection conn, HandshakeError error, Exception cause)
    {
        Abandon(conn, error.ToString());
        return new HandshakeException(error, cause);
    }

    /// <summary>
    /// The two exchanges, in the order api.ClientHandshake has them: hello,
    /// the version before anything else, the key file read afresh, the
    /// daemon's proof, then the client's. On any failure nothing more is
    /// sent; the caller tears the connection down.
    /// </summary>
    private async Task ExchangeProofsAsync(Connection conn, CancellationToken deadline, CancellationToken token)
    {
        var clientNonce = RpcAuth.NewNonce();
        await SendHandshakeAsync(
            conn,
            JsonRpc.EncodeRequest(1, API.SystemHello.Name, new SystemHelloParams { ClientNonce = RpcAuth.Hex(clientNonce) }, API.SystemHello.ParamsInfo),
            deadline,
            token).ConfigureAwait(false);
        var hello = await HandshakeAnswerAsync(conn, 1, RpcAuth.MaxSkippedNotifications, token).ConfigureAwait(false);
        if (hello.Error is { } refused)
        {
            // A daemon of protocol 1 does not know system.hello.
            throw new HandshakeException(refused == ErrorCode.MethodNotFound
                ? HandshakeError.ProtocolMismatch(1)
                : HandshakeError.Rejected(refused));
        }
        var result = hello.Result;

        // protocolVersion first and on its own: it is the one member of the
        // result that every protocol version keeps, whatever the others are.
        if (ProtocolVersionOf(result) is not { } version)
        {
            throw Malformed("the system.hello result has no valid protocolVersion");
        }
        if (version != API.ProtocolVersion)
        {
            throw new HandshakeException(HandshakeError.ProtocolMismatch(version));
        }
        if (HelloMember(result, "daemonNonce") is not { } nonceText || HelloMember(result, "daemonProof") is not { } proofText)
        {
            throw Malformed("the system.hello result does not decode");
        }
        var daemonNonce = RpcAuth.DecodeHex32(nonceText) ?? throw Malformed("daemonNonce is not 64 lowercase hex digits");
        var daemonProof = RpcAuth.DecodeHex32(proofText) ?? throw Malformed("daemonProof is not 64 lowercase hex digits");

        // Only now, and on every connection: a restarted daemon has a new key.
        byte[] key;
        try
        {
            key = await DaemonKey.ReadAsync(RpcAuth.KeyPath(SocketPath), keyFilePolicy, time, token).ConfigureAwait(false);
        }
        catch (KeyUnavailableException e)
        {
            throw new HandshakeException(HandshakeError.KeyUnavailable(e.Reason), e);
        }
        byte[] proof;
        try
        {
            if (!RpcAuth.IsValidDaemonProof(daemonProof, key, clientNonce, daemonNonce))
            {
                throw new HandshakeException(HandshakeError.DaemonUnproven);
            }
            proof = RpcAuth.ClientProof(key, clientNonce, daemonNonce);
        }
        finally
        {
            CryptographicOperations.ZeroMemory(key);
        }

        var authenticate = JsonRpc.EncodeRequest(
            2, API.SystemAuthenticate.Name, new SystemAuthenticateParams { ClientProof = RpcAuth.Hex(proof) }, API.SystemAuthenticate.ParamsInfo);
        CryptographicOperations.ZeroMemory(proof);
        await SendHandshakeAsync(conn, authenticate, deadline, token).ConfigureAwait(false);
        var done = await HandshakeAnswerAsync(conn, 2, 0, token).ConfigureAwait(false);
        if (done.Error is { } rejected)
        {
            throw new HandshakeException(HandshakeError.Rejected(rejected));
        }
        if (done.Result.ValueKind != JsonValueKind.Object)
        {
            throw Malformed("the system.authenticate result is not an object");
        }
    }

    /// <summary>
    /// Sends one handshake line, the private way in while calls are still
    /// refused. Nothing is written past the deadline, as a Go connection past
    /// its deadline writes nothing, even when an answer read in time is still
    /// being worked through.
    /// </summary>
    private static async Task SendHandshakeAsync(Connection conn, byte[] line, CancellationToken deadline, CancellationToken token)
    {
        if (deadline.IsCancellationRequested)
        {
            throw new HandshakeException(HandshakeError.TimedOut);
        }
        await conn.Stream.WriteAsync(line, token).ConfigureAwait(false);
    }

    /// <summary>
    /// The daemon's answer to the handshake request <paramref name="id"/>,
    /// skipping at most <paramref name="skip"/> notifications before it (api
    /// <c>answer</c>). Every other line breaks the protocol.
    /// </summary>
    private static async Task<HandshakeAnswer> HandshakeAnswerAsync(Connection conn, int id, int skip, CancellationToken token)
    {
        var want = id.ToString(System.Globalization.CultureInfo.InvariantCulture);
        while (true)
        {
            var raw = await conn.Reader.ReadLineAsync(token).ConfigureAwait(false);
            var m = HandshakeLine.Parse(raw) ?? throw Malformed("a line is not a JSON-RPC 2.0 message");
            if (m.IsNotification)
            {
                if (skip == 0)
                {
                    throw Malformed("an unexpected notification");
                }
                skip--;
                continue;
            }
            if (!string.IsNullOrEmpty(m.Method))
            {
                throw Malformed("a request instead of an answer");
            }
            if (m.Id != want)
            {
                throw Malformed("an answer without the request's id");
            }
            if (m.ErrorCode is { } code)
            {
                if (m.Result is not null)
                {
                    throw Malformed("an answer with both a result and an error");
                }
                return new HandshakeAnswer(default, code);
            }
            if (m.Result is not { ValueKind: not JsonValueKind.Null } result)
            {
                throw Malformed("an answer without a result");
            }
            return new HandshakeAnswer(result, null);
        }
    }

    /// <summary>
    /// The result's <c>protocolVersion</c> as Go decodes it into an int of a
    /// struct: an object's member that is an integer (not 2.0, not "2"),
    /// and above 0; null otherwise.
    /// </summary>
    private static int? ProtocolVersionOf(JsonElement result)
    {
        if (result.ValueKind != JsonValueKind.Object)
        {
            return null;
        }
        int? version = 0;
        foreach (var member in result.EnumerateObject())
        {
            // The last of repeated members wins, as for Go.
            if (member.NameEquals("protocolVersion"u8))
            {
                version = member.Value.ValueKind switch
                {
                    JsonValueKind.Null => 0,
                    JsonValueKind.Number when member.Value.TryGetInt32(out var v) => v,
                    _ => null,
                };
            }
        }
        return version > 0 ? version : null;
    }

    /// <summary>
    /// A string member of the system.hello result as Go decodes it into a
    /// string field: absent or null is "", a string is itself, anything else
    /// does not decode (null).
    /// </summary>
    private static string? HelloMember(JsonElement result, string name)
    {
        var value = "";
        foreach (var member in result.EnumerateObject())
        {
            if (member.NameEquals(name))
            {
                switch (member.Value.ValueKind)
                {
                    case JsonValueKind.Null:
                        value = "";
                        break;
                    case JsonValueKind.String:
                        value = member.Value.GetString()!;
                        break;
                    default:
                        return null;
                }
            }
        }
        return value;
    }

    private static HandshakeException Malformed(string detail) => new(HandshakeError.Malformed(detail));

    /// <summary>A handshake answer: its result, or its error's code.</summary>
    private readonly record struct HandshakeAnswer(JsonElement Result, ErrorCode? Error);

    /// <summary>
    /// One line from the daemon during the handshake, as api.ClientHandshake
    /// reads it (its handshakeLine): the members that tell an answer from a
    /// notification and a result from an error. A member of the wrong type
    /// fails the reading, as it fails Go's.
    /// </summary>
    private sealed record HandshakeLine
    {
        /// <summary>The id's JSON text; null when absent or null.</summary>
        public string? Id { get; private init; }

        public string? Method { get; private init; }

        /// <summary>The result as it came, JSON null included; null when absent.</summary>
        public JsonElement? Result { get; private init; }

        /// <summary>The error's code (0 when missing, as Go leaves it); null without an error object.</summary>
        public ErrorCode? ErrorCode { get; private init; }

        /// <summary>A notification: a method and no id, or a null one.</summary>
        public bool IsNotification => Id is null && !string.IsNullOrEmpty(Method);

        /// <summary>The line read as Go reads it; null for one that is not a JSON-RPC 2.0 message.</summary>
        public static HandshakeLine? Parse(byte[] raw)
        {
            JsonDocument document;
            try
            {
                document = JsonDocument.Parse(raw);
            }
            catch (JsonException)
            {
                return null;
            }
            using (document)
            {
                var root = document.RootElement;
                if (root.ValueKind != JsonValueKind.Object)
                {
                    return null;
                }
                string? jsonrpc = null;
                string? id = null;
                string? method = null;
                JsonElement? result = null;
                ErrorCode? errorCode = null;
                foreach (var member in root.EnumerateObject())
                {
                    var value = member.Value;
                    if (member.NameEquals("jsonrpc"u8))
                    {
                        if (!StringOrNull(value, out jsonrpc))
                        {
                            return null;
                        }
                    }
                    else if (member.NameEquals("id"u8))
                    {
                        id = value.ValueKind == JsonValueKind.Null ? null : value.GetRawText();
                    }
                    else if (member.NameEquals("method"u8))
                    {
                        if (!StringOrNull(value, out method))
                        {
                            return null;
                        }
                    }
                    else if (member.NameEquals("result"u8))
                    {
                        result = value.Clone();
                    }
                    else if (member.NameEquals("error"u8))
                    {
                        if (!ReadError(value, out errorCode))
                        {
                            return null;
                        }
                    }
                }
                if (jsonrpc != JsonRpc.Version)
                {
                    return null;
                }
                return new HandshakeLine { Id = id, Method = method, Result = result, ErrorCode = errorCode };
            }
        }

        // A string member, or null (Go leaves the field empty); false for any
        // other type.
        private static bool StringOrNull(JsonElement value, out string? text)
        {
            text = value.ValueKind == JsonValueKind.String ? value.GetString() : null;
            return value.ValueKind is JsonValueKind.String or JsonValueKind.Null;
        }

        // api.Error as far as the handshake reads it: the code (0 when
        // missing), and a message that is a string if it is there.
        private static bool ReadError(JsonElement value, out ErrorCode? code)
        {
            code = null;
            if (value.ValueKind == JsonValueKind.Null)
            {
                return true;
            }
            if (value.ValueKind != JsonValueKind.Object)
            {
                return false;
            }
            var n = 0;
            foreach (var member in value.EnumerateObject())
            {
                if (member.NameEquals("code"u8))
                {
                    if (member.Value.ValueKind == JsonValueKind.Null)
                    {
                        n = 0;
                    }
                    else if (member.Value.ValueKind != JsonValueKind.Number || !member.Value.TryGetInt32(out n))
                    {
                        return false;
                    }
                }
                else if (member.NameEquals("message"u8) && member.Value.ValueKind is not (JsonValueKind.String or JsonValueKind.Null))
                {
                    return false;
                }
            }
            code = n;
            return true;
        }
    }
}
