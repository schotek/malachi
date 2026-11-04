// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Transport/JSONRPC.swift (Request,
// ResultEnvelope, ParamsEnvelope); Go: backend/pkg/api/jsonrpc.go (Request,
// Response). RPCError, EmptyParams and JSONCoding live in Malachi.Core.Api
// (RpcError, EmptyParams, JsonCoding); the error data that Swift keeps as a
// JSONValue is RpcError.Data.
//
// Swift decodes a line twice (Envelope, then ResultEnvelope); here a result
// is read straight out of the line with a Utf8JsonReader, so that a 30 MiB
// body is not parsed into a document first.

using System;
using System.Buffers;
using System.Text.Json;
using System.Text.Json.Serialization.Metadata;
using Malachi.Core.Api;

namespace Malachi.Core.Transport;

/// <summary>
/// The wire shapes of the daemon's JSON-RPC 2.0 contract (docs/api.md §1–§2)
/// that the transport writes and reads: a request line, and the
/// <c>result</c> or <c>params</c> of a line read on demand.
/// </summary>
public static class JsonRpc
{
    /// <summary>The <c>jsonrpc</c> member of every message.</summary>
    public const string Version = "2.0";

    /// <summary>
    /// A request as the client sends it, with its "\n": <c>jsonrpc</c>,
    /// <c>id</c>, <c>method</c>, <c>params</c> in this order (Swift
    /// <c>Request</c>). The id comes back verbatim in the answer; the params
    /// are always an object (<see cref="EmptyParams"/> for a method without
    /// any). JSON never holds a raw newline, so the line is one frame.
    /// </summary>
    public static byte[] EncodeRequest<TParams>(long id, string method, TParams parameters, JsonTypeInfo<TParams> paramsInfo)
    {
        ArgumentNullException.ThrowIfNull(method);
        ArgumentNullException.ThrowIfNull(parameters);
        ArgumentNullException.ThrowIfNull(paramsInfo);
        var buffer = new ArrayBufferWriter<byte>(256);
        using (var writer = new Utf8JsonWriter(buffer, JsonCoding.WriterOptions))
        {
            writer.WriteStartObject();
            writer.WriteString("jsonrpc"u8, Version);
            writer.WriteNumber("id"u8, id);
            writer.WriteString("method"u8, method);
            writer.WritePropertyName("params"u8);
            JsonSerializer.Serialize(writer, parameters, paramsInfo);
            writer.WriteEndObject();
        }
        buffer.Write("\n"u8);
        return buffer.WrittenSpan.ToArray();
    }

    /// <summary>
    /// The <c>result</c> of an answer line (Swift <c>ResultEnvelope</c>). A
    /// line without one, or with <c>null</c>, is a decoding error, as a
    /// missing non-optional member is to Swift's decoder.
    /// </summary>
    public static T ReadResult<T>(ReadOnlySpan<byte> line, JsonTypeInfo<T> resultInfo) => ReadMember(line, "result"u8, "result", resultInfo);

    /// <summary>The <c>params</c> of a notification line (Swift <c>ParamsEnvelope</c>), under the same rules.</summary>
    public static T ReadParams<T>(ReadOnlySpan<byte> line, JsonTypeInfo<T> paramsInfo) => ReadMember(line, "params"u8, "params", paramsInfo);

    private static T ReadMember<T>(ReadOnlySpan<byte> line, ReadOnlySpan<byte> name, string label, JsonTypeInfo<T> info)
    {
        ArgumentNullException.ThrowIfNull(info);
        var reader = new Utf8JsonReader(line);
        if (!reader.Read() || reader.TokenType != JsonTokenType.StartObject)
        {
            throw new JsonException("the line is not a JSON object");
        }
        var found = false;
        T? value = default;
        while (reader.Read() && reader.TokenType == JsonTokenType.PropertyName)
        {
            var match = reader.ValueTextEquals(name);
            reader.Read();
            if (match)
            {
                // The last of repeated members wins, as for Go and Swift.
                found = true;
                value = reader.TokenType == JsonTokenType.Null ? default : JsonSerializer.Deserialize(ref reader, info);
            }
            else
            {
                reader.Skip();
            }
        }
        if (!found)
        {
            throw new JsonException($"the line has no {label}");
        }
        if (value is null)
        {
            throw new JsonException($"the {label} is null where a {typeof(T).Name} was expected");
        }
        return value;
    }
}
