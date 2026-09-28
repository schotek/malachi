// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/API/Identifiers.swift; Go:
// backend/pkg/api/types.go ("Identifiers").
//
// The opaque identifiers of backend/pkg/api/types.go. Each is its own type so
// that a folder id cannot be passed where a message id is expected; on the
// wire every one is a bare string. A Go field whose empty string means
// "none" (threadId, parentId, Draft.id) is nullable here, as it is an
// Optional in Swift. Swift's ExpressibleByStringLiteral is the implicit
// conversion from string: a string converts to any of these types, but no
// identifier converts to another.

using System;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

/// <summary>
/// A string-valued wire type (Swift <c>StringWireValue</c>): a value over
/// <see cref="Value"/>, written and read as the bare string, built from a
/// string by the implicit conversion. The base of both the identifiers and
/// the extensible enums. <c>ToString</c> returns the wire string.
/// </summary>
public interface IStringWireValue<TSelf>
    where TSelf : struct, IStringWireValue<TSelf>
{
    /// <summary>The wire string (Swift <c>rawValue</c>).</summary>
    string Value { get; }

    /// <summary>The value of a wire string (Swift <c>init(rawValue:)</c>).</summary>
    static abstract implicit operator TSelf(string value);
}

/// <summary>
/// An identifier the daemon minted (Swift <c>OpaqueID</c>): opaque, compared
/// by value, never parsed.
/// </summary>
public interface IOpaqueId<TSelf> : IStringWireValue<TSelf>
    where TSelf : struct, IOpaqueId<TSelf>;

/// <summary>
/// Reads and writes a <see cref="IStringWireValue{TSelf}"/> as the bare JSON
/// string. Any string decodes, a value this client does not know included
/// (docs/api.md §6); anything else, null among it, is a decoding error, as
/// in Swift.
/// </summary>
public sealed class StringWireValueConverter<T> : JsonConverter<T>
    where T : struct, IStringWireValue<T>
{
    /// <inheritdoc/>
    public override T Read(ref Utf8JsonReader reader, Type typeToConvert, JsonSerializerOptions options)
    {
        if (reader.TokenType != JsonTokenType.String)
        {
            throw new JsonException($"expected a string for {typeof(T).Name}, not {reader.TokenType}");
        }
        return reader.GetString()!;
    }

    /// <inheritdoc/>
    public override void Write(Utf8JsonWriter writer, T value, JsonSerializerOptions options)
    {
        ArgumentNullException.ThrowIfNull(writer);
        writer.WriteStringValue(value.Value ?? "");
    }
}

/// <summary>api.AccountID: a configured account. Stable across restarts.</summary>
[JsonConverter(typeof(StringWireValueConverter<AccountId>))]
public readonly record struct AccountId(string Value) : IOpaqueId<AccountId>
{
    /// <summary>The identifier of a wire string.</summary>
    public static implicit operator AccountId(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.FolderID: a folder within an account. Not the IMAP mailbox name.</summary>
[JsonConverter(typeof(StringWireValueConverter<FolderId>))]
public readonly record struct FolderId(string Value) : IOpaqueId<FolderId>
{
    /// <summary>The identifier of a wire string.</summary>
    public static implicit operator FolderId(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>
/// api.MessageID: a message in the local store. Never the RFC 5322
/// Message-ID header, which is attacker-controlled and not unique.
/// </summary>
[JsonConverter(typeof(StringWireValueConverter<MessageId>))]
public readonly record struct MessageId(string Value) : IOpaqueId<MessageId>
{
    /// <summary>The identifier of a wire string.</summary>
    public static implicit operator MessageId(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.ThreadID: a conversation within an account.</summary>
[JsonConverter(typeof(StringWireValueConverter<ThreadId>))]
public readonly record struct ThreadId(string Value) : IOpaqueId<ThreadId>
{
    /// <summary>The identifier of a wire string.</summary>
    public static implicit operator ThreadId(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.DraftID: a locally stored draft.</summary>
[JsonConverter(typeof(StringWireValueConverter<DraftId>))]
public readonly record struct DraftId(string Value) : IOpaqueId<DraftId>
{
    /// <summary>The identifier of a wire string.</summary>
    public static implicit operator DraftId(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}
