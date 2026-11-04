// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Transport/JSONRPC.swift (JSONCoding).

using System;
using System.Text.Json;
using System.Text.Json.Serialization.Metadata;

namespace Malachi.Core.Api;

/// <summary>
/// The coding every wire exchange uses (Swift <c>JSONCoding</c>): the options
/// of <see cref="ApiJsonContext.Wire"/>, for a value whose type is known only
/// as a type parameter. A type the context does not declare fails with
/// <see cref="NotSupportedException"/>; nothing falls back to reflection.
/// </summary>
public static class JsonCoding
{
    /// <summary>The contract's options (<see cref="ApiJsonContext"/>).</summary>
    public static JsonSerializerOptions Options => ApiJsonContext.Wire.Options;

    /// <summary>
    /// The options of a writer that puts contract values into a larger JSON
    /// text, a request line: a value serialized into a writer is escaped as
    /// the writer escapes, not as <see cref="Options"/> would.
    /// </summary>
    public static JsonWriterOptions WriterOptions => new() { Encoder = Options.Encoder };

    /// <summary>The metadata of <typeparamref name="T"/> under the contract's options.</summary>
    public static JsonTypeInfo<T> TypeInfo<T>() => (JsonTypeInfo<T>)Options.GetTypeInfo(typeof(T));

    /// <summary>The value as UTF-8 JSON.</summary>
    public static byte[] Encode<T>(T value) => JsonSerializer.SerializeToUtf8Bytes(value, TypeInfo<T>());

    /// <summary>The value as a JSON string.</summary>
    public static string EncodeToString<T>(T value) => JsonSerializer.Serialize(value, TypeInfo<T>());

    /// <summary>
    /// A value from UTF-8 JSON. A JSON <c>null</c> is a decoding error, as
    /// for Swift's decoder, and so is anything the contract refuses.
    /// </summary>
    public static T Decode<T>(ReadOnlySpan<byte> json)
    {
        var value = JsonSerializer.Deserialize(json, TypeInfo<T>());
        if (value is null)
        {
            throw new JsonException($"null where a {typeof(T).Name} was expected");
        }
        return value;
    }

    /// <inheritdoc cref="Decode{T}(ReadOnlySpan{byte})"/>
    public static T Decode<T>(string json)
    {
        ArgumentNullException.ThrowIfNull(json);
        var value = JsonSerializer.Deserialize(json, TypeInfo<T>());
        if (value is null)
        {
            throw new JsonException($"null where a {typeof(T).Name} was expected");
        }
        return value;
    }
}
