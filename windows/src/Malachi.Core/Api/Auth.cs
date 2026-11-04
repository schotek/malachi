// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/API/Auth.swift; Go:
// backend/pkg/api/types.go (SystemHelloParams, SystemHelloResult,
// SystemAuthenticateParams), backend/pkg/api/auth.go (KeyPath,
// ParseKeyFile, ParseAuthHex, DaemonProof, ClientProof).
//
// The connection handshake of docs/api.md §1.4: the params and result of
// system.hello and system.authenticate, and what a client needs to take
// part (the key file's path and format, the nonces, the proofs). The
// transport runs the exchange; its key-file reader reads the key. RpcAuth is
// public, where Swift's RPCAuth is internal to its module, because the test
// daemon of Malachi.Core.Tests proves itself with it too.

using System;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

/// <summary>
/// api.SystemHelloParams: the first line of every connection. The method
/// name, this parameter and <see cref="SystemHelloResult.ProtocolVersion"/>
/// never change, so that every client can tell every daemon's protocol.
/// </summary>
public sealed record SystemHelloParams
{
    /// <summary>64 lowercase hex digits: 32 random bytes, new for every connection.</summary>
    [JsonPropertyName("clientNonce")]
    public required string ClientNonce { get; init; }
}

/// <summary>
/// api.SystemHelloResult. <see cref="ProtocolVersion"/> is compared before
/// anything else, and before the key file is read.
/// </summary>
public sealed record SystemHelloResult
{
    /// <summary>The daemon's protocol version (api.ProtocolVersion).</summary>
    [JsonPropertyName("protocolVersion")]
    public required int ProtocolVersion { get; init; }

    /// <summary>64 lowercase hex digits.</summary>
    [JsonPropertyName("daemonNonce")]
    public required string DaemonNonce { get; init; }

    /// <summary>The daemon's proof as 64 lowercase hex digits.</summary>
    [JsonPropertyName("daemonProof")]
    public required string DaemonProof { get; init; }
}

/// <summary>
/// api.SystemAuthenticateParams: the second line of every connection. Its
/// result is empty (<see cref="EmptyResult"/>): the answer itself says that
/// the connection is usable.
/// </summary>
public sealed record SystemAuthenticateParams
{
    /// <summary>The client's proof as 64 lowercase hex digits.</summary>
    [JsonPropertyName("clientProof")]
    public required string ClientProof { get; init; }
}

/// <summary>
/// The client's side of the handshake's cryptography (api.KeyPath,
/// ParseKeyFile, ParseAuthHex, DaemonProof, ClientProof). Keys, nonces and
/// proofs are 32-byte arrays; nothing here keeps one. Nothing here logs, and
/// no error or text carries a key, a nonce or a proof.
/// </summary>
public static class RpcAuth
{
    /// <summary>The key file is the socket's path with this suffix (api.KeyFileSuffix).</summary>
    public const string KeyFileSuffix = ".key";

    /// <summary>
    /// The exact size of a key file: 64 lowercase hex digits and "\n"
    /// (api.KeyFileSize).
    /// </summary>
    public const int KeyFileSize = 65;

    /// <summary>
    /// The longest line the client reads during the handshake, "\n" included
    /// (api maxHandshakeLine). The answers are a few hundred bytes.
    /// </summary>
    public const int MaxHandshakeLine = 64 << 10;

    /// <summary>
    /// How many notifications the client skips before the system.hello answer
    /// (api maxHandshakeNotifications): a daemon of protocol 1 broadcasts them
    /// to every connection, and its methodNotFound must still be reached to
    /// report the mismatch.
    /// </summary>
    public const int MaxSkippedNotifications = 8;

    /// <summary>
    /// Starts every proof's message: it keeps these MACs apart from any other
    /// use of a key and names the construction's version.
    /// </summary>
    public const string Label = "malachi-rpc-auth-v1";

    /// <summary>
    /// The role of the daemon's proof: a proof made for one side is never
    /// valid for the other, so a peer cannot reflect the proof it was sent.
    /// </summary>
    public const string RoleDaemon = "daemon";

    /// <summary>The role of the client's proof.</summary>
    public const string RoleClient = "client";

    /// <summary>
    /// The key file that belongs to the socket at <paramref name="socket"/>
    /// (api.KeyPath): beside it, in the socket's private directory.
    /// </summary>
    public static string KeyPath(string socket) => socket + KeyFileSuffix;

    /// <summary>32 fresh bytes from the system's cryptographic generator: a nonce, or a key in tests.</summary>
    public static byte[] NewNonce() => RandomNumberGenerator.GetBytes(32);

    /// <summary>
    /// The key in the content of a key file: exactly 64 lowercase hex digits
    /// and one "\n", nothing before, between or after (api.ParseKeyFile);
    /// null for anything else.
    /// </summary>
    public static byte[]? ParseKey(ReadOnlySpan<byte> content)
    {
        if (content.Length != KeyFileSize || content[^1] != (byte)'\n')
        {
            return null;
        }
        return DecodeLowerHex32(content[..(KeyFileSize - 1)]);
    }

    /// <summary>
    /// A nonce or a proof from the wire: exactly 64 lowercase hex digits, no
    /// prefix, no white space, no upper case (api.ParseAuthHex). It works on
    /// the UTF-8 bytes, so a character of several bytes is never a digit.
    /// </summary>
    public static byte[]? DecodeHex32(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        return DecodeLowerHex32(Encoding.UTF8.GetBytes(text));
    }

    /// <summary>The bytes as lowercase hex digits, their form on the wire.</summary>
    public static string Hex(ReadOnlySpan<byte> bytes) => Convert.ToHexStringLower(bytes);

    /// <summary>
    /// The daemon's proof on a connection (api.DaemonProof): HMAC-SHA256
    /// under the key of <c>Message(RoleDaemon, …)</c>.
    /// </summary>
    public static byte[] DaemonProof(ReadOnlySpan<byte> key, ReadOnlySpan<byte> clientNonce, ReadOnlySpan<byte> daemonNonce) =>
        HMACSHA256.HashData(key, Message(RoleDaemon, clientNonce, daemonNonce));

    /// <summary>
    /// The client's proof on a connection (api.ClientProof): as the daemon's,
    /// with the role "client".
    /// </summary>
    public static byte[] ClientProof(ReadOnlySpan<byte> key, ReadOnlySpan<byte> clientNonce, ReadOnlySpan<byte> daemonNonce) =>
        HMACSHA256.HashData(key, Message(RoleClient, clientNonce, daemonNonce));

    /// <summary>
    /// Whether <paramref name="proof"/> is the daemon's proof for these nonces
    /// under the key, compared in constant time.
    /// </summary>
    public static bool IsValidDaemonProof(
        ReadOnlySpan<byte> proof, ReadOnlySpan<byte> key, ReadOnlySpan<byte> clientNonce, ReadOnlySpan<byte> daemonNonce) =>
        CryptographicOperations.FixedTimeEquals(proof, DaemonProof(key, clientNonce, daemonNonce));

    /// <summary>
    /// The 91 bytes a proof authenticates (docs/api.md §1.4): the label, 0x00,
    /// the role, 0x00, then both nonces as raw bytes, not hex.
    /// </summary>
    public static byte[] Message(string role, ReadOnlySpan<byte> clientNonce, ReadOnlySpan<byte> daemonNonce)
    {
        ArgumentNullException.ThrowIfNull(role);
        var label = Encoding.ASCII.GetBytes(Label);
        var roleBytes = Encoding.ASCII.GetBytes(role);
        var m = new byte[label.Length + 1 + roleBytes.Length + 1 + clientNonce.Length + daemonNonce.Length];
        var at = 0;
        label.CopyTo(m, at);
        at += label.Length;
        m[at++] = 0;
        roleBytes.CopyTo(m, at);
        at += roleBytes.Length;
        m[at++] = 0;
        clientNonce.CopyTo(m.AsSpan(at));
        at += clientNonce.Length;
        daemonNonce.CopyTo(m.AsSpan(at));
        return m;
    }

    // Exactly 64 lowercase hex digits as 32 bytes, null otherwise.
    private static byte[]? DecodeLowerHex32(ReadOnlySpan<byte> digits)
    {
        if (digits.Length != 64)
        {
            return null;
        }
        var result = new byte[32];
        for (var i = 0; i < result.Length; i++)
        {
            if (Nibble(digits[2 * i]) is not { } hi || Nibble(digits[(2 * i) + 1]) is not { } lo)
            {
                return null;
            }
            result[i] = (byte)((hi << 4) | lo);
        }
        return result;
    }

    private static int? Nibble(byte c) => c switch
    {
        >= (byte)'0' and <= (byte)'9' => c - '0',
        >= (byte)'a' and <= (byte)'f' => c - 'a' + 10,
        _ => null,
    };
}
