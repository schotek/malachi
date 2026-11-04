// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Transport/JSONRPC.swift (RPCError) and
// macos/Sources/MalachiCore/API/ErrorCode.swift (RPCError.attachmentTooBig);
// Go: backend/pkg/api/errors.go (Error). The error object is part of the
// contract as much as of the transport: it appears inside results
// (EndpointTestResult.Error, OutboxInfo.Error, SyncState.Error), so it lives
// with the API types. Swift's generic JSONValue for error.data is a
// JsonElement here, cloned so that it outlives the document it came from.

using System;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

/// <summary>
/// The JSON-RPC error object (api.Error). <see cref="Code"/> is the stable
/// enumeration of docs/api.md §2; <see cref="Message"/> is human-readable and
/// never matched on; <see cref="Data"/> is optional, method-specific detail.
/// The same shape appears inside results (<c>EndpointTestResult.Error</c>,
/// <c>OutboxInfo.Error</c>, <c>SyncState.Error</c>).
/// </summary>
/// <remarks>
/// Equality compares <see cref="Data"/> by its JSON content, as Swift's
/// <c>JSONValue</c> does. A missing or null <c>message</c> reads as empty
/// (Go's client keeps such an error; Swift refuses the whole line, which
/// would leave a call waiting for its timeout, or fail a whole result for
/// one account's state).
/// </remarks>
public sealed record RpcError
{
    /// <summary>The stable code.</summary>
    [JsonPropertyName("code")]
    public required ErrorCode Code { get; init; }

    /// <summary>Technical English, unstable; never matched on, never shown but as a detail.</summary>
    [JsonPropertyName("message")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Message { get; init => field = value ?? ""; } = "";

    /// <summary>Method-specific detail, when the daemon sends any; a clone that owns its memory.</summary>
    [JsonPropertyName("data")]
    public JsonElement? Data { get; init => field = Own(value); }

    /// <summary>
    /// The structured detail of an <c>attachmentTooBig</c> error; null for any
    /// other code or when <see cref="Data"/> has not the documented shape.
    /// </summary>
    [JsonIgnore]
    public SizeLimit? AttachmentTooBig
    {
        get
        {
            if (Code != ErrorCode.AttachmentTooBig || Data is not { ValueKind: JsonValueKind.Object } data
                || !data.TryGetProperty("limit", out var limit) || IntValue(limit) is not { } l)
            {
                return null;
            }
            return new SizeLimit(l, data.TryGetProperty("size", out var size) ? IntValue(size) : null);
        }
    }

    /// <inheritdoc/>
    public bool Equals(RpcError? other) =>
        other is not null && Code == other.Code && string.Equals(Message, other.Message, StringComparison.Ordinal)
        && (Data, other.Data) switch
        {
            (null, null) => true,
            ({ } a, { } b) => JsonElement.DeepEquals(a, b),
            _ => false,
        };

    /// <inheritdoc/>
    public override int GetHashCode() => HashCode.Combine(Code, StringComparer.Ordinal.GetHashCode(Message), Data?.ValueKind);

    /// <summary>Swift's description: the message and the code's number.</summary>
    public override string ToString() => $"{Message} ({Code.Value})";

    // A data element that survives the document it was read from; an
    // undefined element is no data.
    private static JsonElement? Own(JsonElement? value) =>
        value is { } v && v.ValueKind != JsonValueKind.Undefined ? v.Clone() : null;

    // Swift's JSONValue.intValue: a number that is an integer exactly (2.0
    // included) and fits.
    private static long? IntValue(JsonElement e)
    {
        if (e.ValueKind != JsonValueKind.Number || !e.TryGetDouble(out var n)
            || Math.Round(n) != n || n < -9223372036854775808.0 || n >= 9223372036854775808.0)
        {
            return null;
        }
        return (long)n;
    }
}
