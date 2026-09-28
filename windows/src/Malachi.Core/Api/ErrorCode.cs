// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/API/ErrorCode.swift; Go:
// backend/pkg/api/errors.go (ErrorCode, codeNames). RPCError.attachmentTooBig,
// which the Swift file adds as an extension, is RpcError.AttachmentTooBig.

using System;
using System.Collections.Generic;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

/// <summary>
/// api.ErrorCode: the numeric error enumeration of docs/api.md §2. Codes are
/// stable, never renumbered, only appended; an unknown one still decodes.
/// The named codes are const ints, so that a switch over <see cref="Value"/>
/// can name them (case ErrorCode.AuthFailed:); an int converts to an
/// ErrorCode implicitly (Swift's integer literals).
/// </summary>
[JsonConverter(typeof(ErrorCodeConverter))]
public readonly record struct ErrorCode(int Value)
{
    // JSON-RPC 2.0 reserved codes.

    /// <summary>The line was not valid JSON.</summary>
    public const int ParseError = -32700;

    /// <summary>Missing <c>jsonrpc</c>/<c>method</c>, wrong version.</summary>
    public const int InvalidRequest = -32600;

    /// <summary>No such method.</summary>
    public const int MethodNotFound = -32601;

    /// <summary>The params did not decode into the method's type.</summary>
    public const int InvalidParams = -32602;

    /// <summary>An unexpected failure; details are logged, not returned.</summary>
    public const int InternalError = -32603;

    // 1000–1099: general.

    /// <summary>The method is a stub.</summary>
    public const int NotImplemented = 1000;

    /// <summary>The params decoded but are semantically wrong.</summary>
    public const int InvalidArgument = 1001;

    /// <summary>Optimistic-concurrency conflict (drafts, sending).</summary>
    public const int Conflict = 1002;

    /// <summary>The request was cancelled by shutdown.</summary>
    public const int Cancelled = 1003;

    /// <summary>Daemon busy or shutting down.</summary>
    public const int Unavailable = 1004;

    /// <summary>
    /// The RPC connection has not completed the handshake (docs/api.md §1.4),
    /// or the handshake failed; the daemon closes the connection after this
    /// answer. Not about mail accounts, whose sign-in problems are the 1200s.
    /// </summary>
    public const int Unauthenticated = 1005;

    // 1100–1199: not found.

    /// <summary>No such account.</summary>
    public const int AccountNotFound = 1100;

    /// <summary>No such folder.</summary>
    public const int FolderNotFound = 1101;

    /// <summary>No such message.</summary>
    public const int MessageNotFound = 1102;

    /// <summary>No such thread.</summary>
    public const int ThreadNotFound = 1103;

    /// <summary>No such draft.</summary>
    public const int DraftNotFound = 1104;

    /// <summary>Unknown id, another account's, or bound to a different draft.</summary>
    public const int AttachmentNotFound = 1105;

    // 1200–1299: authentication.

    /// <summary>Credentials missing or token expired; see notify.authRequired.</summary>
    public const int AuthRequired = 1200;

    /// <summary>The server rejected the credentials.</summary>
    public const int AuthFailed = 1201;

    /// <summary>Secret storage unavailable.</summary>
    public const int KeyringError = 1202;

    /// <summary>
    /// The daemon's own sign-in needs an OAuth client id for the provider and
    /// none is configured.
    /// </summary>
    public const int OAuthClientMissing = 1203;

    // 1300–1399: network and remote servers.

    /// <summary>The daemon is in offline mode.</summary>
    public const int Offline = 1300;

    /// <summary>The connection failed.</summary>
    public const int NetworkError = 1301;

    /// <summary>An IMAP/SMTP server returned an error.</summary>
    public const int ServerError = 1302;

    /// <summary>Certificate, handshake or STARTTLS problem; <c>data</c> is a TlsErrorData.</summary>
    public const int TlsError = 1303;

    /// <summary>No answer in time.</summary>
    public const int ServerTimeout = 1304;

    /// <summary>
    /// The mail server no longer has the message (another client deleted or
    /// moved it); the local copy goes with the next sync.
    /// </summary>
    public const int MessageGone = 1305;

    // 1400–1499: local storage.

    /// <summary>SQLite failure.</summary>
    public const int StorageError = 1400;

    /// <summary>The store schema could not be upgraded.</summary>
    public const int MigrationFailed = 1401;

    // 1500–1599: content.

    /// <summary>MIME unparsable even leniently.</summary>
    public const int MalformedMessage = 1500;

    /// <summary>The sanitiser refused the input; the body is withheld.</summary>
    public const int SanitizeFailed = 1501;

    /// <summary>Over a documented limit; <c>data</c> is <c>{"limit": n, "size": n}</c>.</summary>
    public const int AttachmentTooBig = 1502;

    /// <summary><c>message.part</c> named a part the message does not have.</summary>
    public const int PartNotFound = 1503;

    /// <summary>
    /// The part's data is not stored on this device
    /// (<see cref="Attachment.Remote"/>); <c>message.download</c> fetches it.
    /// </summary>
    public const int PartNotDownloaded = 1504;

    private static readonly Dictionary<int, string> Names = new()
    {
        [ParseError] = "parseError",
        [InvalidRequest] = "invalidRequest",
        [MethodNotFound] = "methodNotFound",
        [InvalidParams] = "invalidParams",
        [InternalError] = "internalError",
        [NotImplemented] = "notImplemented",
        [InvalidArgument] = "invalidArgument",
        [Conflict] = "conflict",
        [Cancelled] = "cancelled",
        [Unavailable] = "unavailable",
        [Unauthenticated] = "unauthenticated",
        [AccountNotFound] = "accountNotFound",
        [FolderNotFound] = "folderNotFound",
        [MessageNotFound] = "messageNotFound",
        [ThreadNotFound] = "threadNotFound",
        [DraftNotFound] = "draftNotFound",
        [AttachmentNotFound] = "attachmentNotFound",
        [AuthRequired] = "authRequired",
        [AuthFailed] = "authFailed",
        [KeyringError] = "keyringError",
        [OAuthClientMissing] = "oauthClientMissing",
        [Offline] = "offline",
        [NetworkError] = "networkError",
        [ServerError] = "serverError",
        [TlsError] = "tlsError",
        [ServerTimeout] = "serverTimeout",
        [MessageGone] = "messageGone",
        [StorageError] = "storageError",
        [MigrationFailed] = "migrationFailed",
        [MalformedMessage] = "malformedMessage",
        [SanitizeFailed] = "sanitizeFailed",
        [AttachmentTooBig] = "attachmentTooBig",
        [PartNotFound] = "partNotFound",
        [PartNotDownloaded] = "partNotDownloaded",
    };

    /// <summary>
    /// Every code of the contract (protocol version 2), in the order of
    /// errors.go.
    /// </summary>
    public static IReadOnlyList<ErrorCode> All { get; } =
    [
        ParseError, InvalidRequest, MethodNotFound, InvalidParams, InternalError,
        NotImplemented, InvalidArgument, Conflict, Cancelled, Unavailable, Unauthenticated,
        AccountNotFound, FolderNotFound, MessageNotFound, ThreadNotFound, DraftNotFound, AttachmentNotFound,
        AuthRequired, AuthFailed, KeyringError, OAuthClientMissing,
        Offline, NetworkError, ServerError, TlsError, ServerTimeout, MessageGone,
        StorageError, MigrationFailed,
        MalformedMessage, SanitizeFailed, AttachmentTooBig, PartNotFound, PartNotDownloaded,
    ];

    /// <summary>
    /// The stable symbolic name (api.ErrorCode.String): <c>"attachmentTooBig"</c>,
    /// or <c>"unknown(1234)"</c> for a code this client does not know.
    /// </summary>
    public string Name => Names.TryGetValue(Value, out var name) ? name : $"unknown({Value})";

    /// <summary>The code of a number.</summary>
    public static implicit operator ErrorCode(int value) => new(value);

    /// <summary>The name, as Swift's description.</summary>
    public override string ToString() => Name;
}

/// <summary>Reads and writes an <see cref="ErrorCode"/> as its JSON number.</summary>
public sealed class ErrorCodeConverter : JsonConverter<ErrorCode>
{
    /// <inheritdoc/>
    public override ErrorCode Read(ref Utf8JsonReader reader, Type typeToConvert, JsonSerializerOptions options)
    {
        if (reader.TokenType != JsonTokenType.Number || !reader.TryGetInt32(out var code))
        {
            throw new JsonException($"expected an integer error code, not {reader.TokenType}");
        }
        return new ErrorCode(code);
    }

    /// <inheritdoc/>
    public override void Write(Utf8JsonWriter writer, ErrorCode value, JsonSerializerOptions options)
    {
        ArgumentNullException.ThrowIfNull(writer);
        writer.WriteNumberValue(value.Value);
    }
}

/// <summary>
/// The <c>data</c> of an <c>attachmentTooBig</c> error: the cap that was
/// exceeded and, where the daemon knows it, the offending size
/// (<c>message.part</c> reports the limit only).
/// </summary>
/// <param name="Limit">The cap in bytes.</param>
/// <param name="Size">The offending size in bytes, when the daemon reports it.</param>
public readonly record struct SizeLimit(long Limit, long? Size = null);
