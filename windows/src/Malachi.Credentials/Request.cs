// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiKeychain/Request.swift (Request): the pure
// half of malachi-credentials, the protocol the daemon's
// internal/auth/helper speaks, parsed and validated without touching
// Credential Manager, so that it can be tested without it.
//
// One difference from Swift: the value stays UTF-8 bytes from stdin to the
// store and back and never becomes a string, so every buffer that held it
// can be zeroed (docs/windows-port.md §10). The request is therefore read
// with Utf8JsonReader, not decoded into a type, and the get answer is
// written by ValueLine rather than a JSON encoder, whose escaping would copy
// the value into pooled buffers nobody clears.

using System;
using System.Buffers;
using System.Diagnostics.CodeAnalysis;
using System.Security.Cryptography;
using System.Text.Json;
using System.Text.Unicode;

namespace Malachi.Credentials;

/// <summary>
/// The one JSON line on stdin: <c>{"account":"…","key":"…"}</c>, plus
/// <c>"value"</c> for set. Values never appear in argv or the environment.
/// </summary>
/// <param name="Account">The daemon's account id (<c>acc_…</c>).</param>
/// <param name="Key">What is stored for the account (<c>password</c>, <c>oauth2.refresh_token</c>).</param>
/// <param name="Value">The value as UTF-8, or null; <see cref="ZeroValue"/> wipes it.</param>
internal sealed record Request(string Account, string Key, byte[]? Value = null)
{
    /// <summary>The service every item is filed under, the first part of its target name.</summary>
    public const string Service = "io.github.schotek.Malachi";

    /// <summary>More than this on stdin is not a request.</summary>
    public const int MaxInput = 1 << 20;

    /// <summary>
    /// Account ids and keys are opaque identifiers of the daemon
    /// (<c>acc_…</c>, <c>password</c>, <c>oauth2.refresh_token</c>); anything
    /// else is rejected before it can reach a credential's name.
    /// </summary>
    public const int MaxIdentifier = 128;

    private static readonly SearchValues<char> IdentifierCharacters =
        SearchValues.Create("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-");

    /// <summary>
    /// The item's user name: one item per account and key (the Keychain
    /// item's account attribute on macOS).
    /// </summary>
    public string ItemAccount => $"{Account}/{Key}";

    /// <summary>The item's comment; it names the account id and key only.</summary>
    public string Label => $"Malachi Mail: {Account} ({Key})";

    /// <summary>
    /// Validates the operation and reads the request. False means
    /// <see cref="HelperExit.BadRequest"/>, and nothing about the data is
    /// said: an unknown operation, more than <see cref="MaxInput"/> bytes,
    /// anything but one JSON object (unknown members are skipped, a member
    /// named twice is refused), an account or key that is not an
    /// identifier, a value that is not a string or null, or set without a
    /// value.
    /// </summary>
    public static bool TryParse(string op, ReadOnlySpan<byte> data, [NotNullWhen(true)] out Request? request)
    {
        request = null;
        if (Operation.FromRawValue(op) is not { } operation || data.Length > MaxInput)
        {
            return false;
        }
        string? account = null;
        string? key = null;
        byte[]? value = null;
        try
        {
            if (!ReadObject(data, ref account, ref key, ref value)
                || account is null || key is null
                || !IsIdentifier(account) || !IsIdentifier(key)
                || (operation == Operation.Set && value is null))
            {
                return false;
            }
            request = new Request(account, key, value);
            value = null; // the request owns it now
            return true;
        }
        catch (JsonException)
        {
            return false;
        }
        catch (InvalidOperationException)
        {
            // Text that is not UTF-8, or an escape that is not UTF-16.
            return false;
        }
        finally
        {
            if (value is not null)
            {
                CryptographicOperations.ZeroMemory(value);
            }
        }
    }

    /// <summary><c>^[A-Za-z0-9._-]{1,128}$</c></summary>
    internal static bool IsIdentifier(string s) =>
        s.Length is >= 1 and <= MaxIdentifier && !s.AsSpan().ContainsAnyExcept(IdentifierCharacters);

    /// <summary>
    /// The get answer: one JSON line <c>{"value":"…"}</c>. JSON escaping keeps
    /// a value with line breaks on one line: the quotation mark, the backslash
    /// and U+0000 to U+001F are escaped (RFC 8259 §7), everything else stays
    /// the UTF-8 it was. The line is built in an array of exactly its size,
    /// the one copy of the value this makes, which the caller zeroes.
    /// </summary>
    /// <param name="value">The value; it must be valid UTF-8.</param>
    public static byte[] ValueLine(ReadOnlySpan<byte> value)
    {
        if (!Utf8.IsValid(value))
        {
            throw new ArgumentException("the value is not UTF-8", nameof(value));
        }
        ReadOnlySpan<byte> head = "{\"value\":\""u8;
        ReadOnlySpan<byte> tail = "\"}\n"u8;
        var length = head.Length + tail.Length;
        foreach (var b in value)
        {
            length += EscapedLength(b);
        }
        var line = new byte[length];
        head.CopyTo(line);
        var at = head.Length;
        foreach (var b in value)
        {
            at = Escape(b, line, at);
        }
        tail.CopyTo(line.AsSpan(at));
        return line;
    }

    /// <summary>Overwrites the value's bytes with zeros.</summary>
    public void ZeroValue()
    {
        if (Value is not null)
        {
            CryptographicOperations.ZeroMemory(Value);
        }
    }

    /// <summary>Equal account, key and value bytes (Swift's synthesized Equatable).</summary>
    public bool Equals(Request? other) =>
        other is not null
        && string.Equals(Account, other.Account, StringComparison.Ordinal)
        && string.Equals(Key, other.Key, StringComparison.Ordinal)
        && (Value is null ? other.Value is null : other.Value is not null && Value.AsSpan().SequenceEqual(other.Value));

    /// <inheritdoc/>
    public override int GetHashCode() => HashCode.Combine(Account, Key);

    // Reads the object's members; false when the shape is wrong. Whatever
    // value was read is in value, also when this throws, so the caller can
    // zero it.
    private static bool ReadObject(ReadOnlySpan<byte> data, ref string? account, ref string? key, ref byte[]? value)
    {
        var reader = new Utf8JsonReader(data);
        if (!reader.Read() || reader.TokenType != JsonTokenType.StartObject)
        {
            return false;
        }
        var sawValue = false;
        while (reader.Read() && reader.TokenType == JsonTokenType.PropertyName)
        {
            if (reader.ValueTextEquals("account"u8))
            {
                if (account is not null || !ReadString(ref reader, out account))
                {
                    return false;
                }
            }
            else if (reader.ValueTextEquals("key"u8))
            {
                if (key is not null || !ReadString(ref reader, out key))
                {
                    return false;
                }
            }
            else if (reader.ValueTextEquals("value"u8))
            {
                if (sawValue || !ReadValue(ref reader, ref value))
                {
                    return false;
                }
                sawValue = true;
            }
            else
            {
                reader.Skip();
            }
        }
        // The object must close at the top level and nothing but white space
        // may follow it (the reader throws on anything else).
        return reader.TokenType == JsonTokenType.EndObject && reader.CurrentDepth == 0 && !reader.Read();
    }

    private static bool ReadString(ref Utf8JsonReader reader, out string? s)
    {
        s = null;
        if (!reader.Read() || reader.TokenType != JsonTokenType.String)
        {
            return false;
        }
        s = reader.GetString();
        return s is not null;
    }

    // A JSON string, unescaped straight into a byte array (the input is one
    // span, so the token is in ValueSpan, at least as long as its unescaped
    // form), or null.
    private static bool ReadValue(ref Utf8JsonReader reader, ref byte[]? value)
    {
        if (!reader.Read())
        {
            return false;
        }
        if (reader.TokenType == JsonTokenType.Null)
        {
            return true;
        }
        if (reader.TokenType != JsonTokenType.String)
        {
            return false;
        }
        var buffer = new byte[reader.ValueSpan.Length];
        value = buffer;
        var written = reader.CopyString(buffer);
        if (written != buffer.Length)
        {
            value = buffer.AsSpan(0, written).ToArray();
            CryptographicOperations.ZeroMemory(buffer);
        }
        return Utf8.IsValid(value);
    }

    private static int EscapedLength(byte b) => b switch
    {
        (byte)'"' or (byte)'\\' or (byte)'\b' or (byte)'\f' or (byte)'\n' or (byte)'\r' or (byte)'\t' => 2,
        < 0x20 => 6,
        _ => 1,
    };

    private static int Escape(byte b, byte[] line, int at)
    {
        var shortForm = b switch
        {
            (byte)'"' => (byte)'"',
            (byte)'\\' => (byte)'\\',
            (byte)'\b' => (byte)'b',
            (byte)'\f' => (byte)'f',
            (byte)'\n' => (byte)'n',
            (byte)'\r' => (byte)'r',
            (byte)'\t' => (byte)'t',
            _ => (byte)0,
        };
        if (shortForm != 0)
        {
            line[at] = (byte)'\\';
            line[at + 1] = shortForm;
            return at + 2;
        }
        if (b < 0x20)
        {
            ReadOnlySpan<byte> hex = "0123456789abcdef"u8;
            line[at] = (byte)'\\';
            line[at + 1] = (byte)'u';
            line[at + 2] = (byte)'0';
            line[at + 3] = (byte)'0';
            line[at + 4] = hex[b >> 4];
            line[at + 5] = hex[b & 0xF];
            return at + 6;
        }
        line[at] = b;
        return at + 1;
    }
}
