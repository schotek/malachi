// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Transport/JSONRPC.swift (Envelope); Go:
// backend/pkg/api/jsonrpc.go (Envelope, IsResponse, IsNotification),
// ui/internal/client/client.go (readLoop's classification).

using System;
using System.Text.Json;
using Malachi.Core.Api;

namespace Malachi.Core.Transport;

/// <summary>
/// The first reading of an incoming line (Swift <c>Envelope</c>): its id,
/// its method and its error, enough to tell an answer from a notification.
/// The <c>result</c> or <c>params</c> is read later, by whoever knows its
/// type (<see cref="JsonRpc"/>).
/// </summary>
public readonly record struct Envelope
{
    /// <summary>The id when it is an integer; the client sends nothing else.</summary>
    public long? Id { get; init; }

    /// <summary>Whether an id is there and not null, whatever its type.</summary>
    public bool HasId { get; init; }

    /// <summary>The method of a notification (or of a request, which a daemon never sends).</summary>
    public string? Method { get; init; }

    /// <summary>The error object of a failed call.</summary>
    public RpcError? Error { get; init; }

    /// <summary>An answer to a call: an integer id.</summary>
    public bool IsResponse => Id is not null;

    /// <summary>A notification: a method and no id, or a null one (Go's rule: the method is not empty).</summary>
    public bool IsNotification => !HasId && !string.IsNullOrEmpty(Method);

    /// <summary>
    /// Reads the members that classify <paramref name="line"/>. False for a
    /// line that is not a JSON object, or whose <c>method</c> is not a string
    /// (or not UTF-8) or whose <c>error</c> is not an error object: such a
    /// line is ignored, as the GTK and Swift clients ignore it.
    /// </summary>
    public static bool TryParse(ReadOnlySpan<byte> line, out Envelope envelope)
    {
        envelope = default;
        try
        {
            var reader = new Utf8JsonReader(line);
            if (!reader.Read() || reader.TokenType != JsonTokenType.StartObject)
            {
                return false;
            }
            long? id = null;
            var hasId = false;
            string? method = null;
            RpcError? error = null;
            while (reader.Read() && reader.TokenType == JsonTokenType.PropertyName)
            {
                if (reader.ValueTextEquals("id"u8))
                {
                    reader.Read();
                    hasId = reader.TokenType != JsonTokenType.Null;
                    id = reader.TokenType == JsonTokenType.Number && reader.TryGetInt64(out var n) ? n : null;
                    reader.Skip();
                }
                else if (reader.ValueTextEquals("method"u8))
                {
                    reader.Read();
                    switch (reader.TokenType)
                    {
                        case JsonTokenType.String:
                            method = reader.GetString();
                            break;
                        case JsonTokenType.Null:
                            method = null;
                            break;
                        default:
                            return false;
                    }
                }
                else if (reader.ValueTextEquals("error"u8))
                {
                    reader.Read();
                    switch (reader.TokenType)
                    {
                        case JsonTokenType.StartObject:
                            error = JsonSerializer.Deserialize(ref reader, ApiJsonContext.Wire.RpcError);
                            break;
                        case JsonTokenType.Null:
                            error = null;
                            break;
                        default:
                            return false;
                    }
                }
                else
                {
                    reader.Read();
                    reader.Skip();
                }
            }
            if (reader.TokenType != JsonTokenType.EndObject || reader.Read())
            {
                return false;
            }
            envelope = new Envelope { Id = id, HasId = hasId, Method = method, Error = error };
            return true;
        }
        catch (JsonException)
        {
            return false;
        }
        catch (InvalidOperationException)
        {
            // A method that is not UTF-8, which the reader will not transcode.
            return false;
        }
    }
}
