// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows only, no Swift counterpart: the Keychain holds a value of any
// size, a generic credential at most CRED_MAX_CREDENTIAL_BLOB_SIZE (2560)
// bytes. A longer value is split into chunks, and this is the header its
// main item carries instead of the value (docs/windows-port.md §10).

using System;
using System.Buffers.Binary;
using System.Diagnostics.CodeAnalysis;
using System.Globalization;
using System.Security.Cryptography;
using Windows.Win32;

namespace Malachi.Credentials;

/// <summary>
/// The main item of a chunked value: <see cref="Count"/> chunks of
/// <see cref="ChunkSize"/> bytes (the last one shorter) in slot
/// <see cref="Slot"/> hold the value's <see cref="Length"/> bytes, whose
/// SHA-256 is <see cref="Sha256"/>.
/// </summary>
/// <remarks>
/// <para>
/// The blob is <see cref="Size"/> bytes: <see cref="Marker"/>,
/// <see cref="Version"/>, the slot, the count, the length (uint32,
/// little-endian) and the SHA-256. The marker is a byte no UTF-8 text
/// begins with, and every stored value is UTF-8, so a header can never be
/// mistaken for a value of up to <see cref="ChunkSize"/> bytes stored as it
/// is.
/// </para>
/// <para>
/// The chunks of a value take one of two slots of <see cref="MaxChunks"/>
/// numbers each: slot 0 is <c>#1..#16</c>, slot 1 <c>#17..#32</c>. A value
/// replacing a chunked one goes into the slot its header does not name, so
/// the previous value stays whole until the new header replaces the old one.
/// </para>
/// </remarks>
internal readonly struct ChunkHeader
{
    /// <summary>The most a generic credential holds (CRED_MAX_CREDENTIAL_BLOB_SIZE).</summary>
    public const int ChunkSize = (int)PInvoke.CRED_MAX_CREDENTIAL_BLOB_SIZE;

    /// <summary>
    /// The most chunks a value may take: 40 KiB, twenty times a large
    /// Microsoft refresh token. It also bounds what a damaged header can make
    /// get read. (The get answer of a value without JSON escapes is then well
    /// inside the 64 KiB the daemon reads; <see cref="CredentialStore.Set"/>
    /// refuses any value whose answer would not fit.)
    /// </summary>
    public const int MaxChunks = 16;

    /// <summary>The slots chunks alternate between.</summary>
    public const int Slots = 2;

    /// <summary>The longest value the store takes.</summary>
    public const int MaxLength = MaxChunks * ChunkSize;

    /// <summary>The size of the blob.</summary>
    public const int Size = 40;

    /// <summary>The first byte: never the first byte of UTF-8 text.</summary>
    public const byte Marker = 0xFF;

    /// <summary>The format of the blob.</summary>
    public const byte Version = 1;

    private ChunkHeader(int slot, int count, int length, byte[] sha256)
    {
        Slot = slot;
        Count = count;
        Length = length;
        Sha256 = sha256;
    }

    /// <summary>Which numbers the chunks have: 0 for <c>#1..</c>, 1 for <c>#17..</c>.</summary>
    public int Slot { get; }

    /// <summary>How many chunks hold the value (2 to <see cref="MaxChunks"/>).</summary>
    public int Count { get; }

    /// <summary>The value's length in bytes.</summary>
    public int Length { get; }

    /// <summary>The SHA-256 of the value.</summary>
    public ReadOnlyMemory<byte> Sha256 { get; }

    /// <summary>
    /// Whether a stored blob claims to be a header rather than a value. Only
    /// the marker is looked at; <see cref="TryDecode"/> decides whether it
    /// is a good one.
    /// </summary>
    public static bool IsHeader(ReadOnlySpan<byte> blob) => !blob.IsEmpty && blob[0] == Marker;

    /// <summary>How many chunks a value of the given length needs; 0 when it fits one item.</summary>
    public static int ChunksFor(int length) => length <= ChunkSize ? 0 : (length + ChunkSize - 1) / ChunkSize;

    /// <summary>
    /// The header of a value longer than <see cref="ChunkSize"/> and at most
    /// <see cref="MaxLength"/> bytes whose chunks go into <paramref name="slot"/>.
    /// </summary>
    public static ChunkHeader For(ReadOnlySpan<byte> value, int slot)
    {
        if (value.Length is <= ChunkSize or > MaxLength)
        {
            throw new ArgumentOutOfRangeException(nameof(value), value.Length, "a chunked value is longer than one item and at most MaxLength bytes");
        }
        if (slot is < 0 or >= Slots)
        {
            throw new ArgumentOutOfRangeException(nameof(slot), slot, "a slot is 0 or 1");
        }
        return new ChunkHeader(slot, ChunksFor(value.Length), value.Length, SHA256.HashData(value));
    }

    /// <summary>
    /// Reads a stored header. False, with the problem in fixed words, for
    /// anything but a header of this format that describes a value this store
    /// could have written.
    /// </summary>
    public static bool TryDecode(ReadOnlySpan<byte> blob, out ChunkHeader header, [NotNullWhen(false)] out string? problem)
    {
        header = default;
        if (blob.Length < 2 || blob[0] != Marker)
        {
            problem = "no header";
            return false;
        }
        if (blob[1] != Version)
        {
            problem = string.Create(CultureInfo.InvariantCulture, $"header version {blob[1]} is unknown");
            return false;
        }
        if (blob.Length != Size)
        {
            problem = "the header has the wrong size";
            return false;
        }
        if (blob[2] >= Slots)
        {
            problem = string.Create(CultureInfo.InvariantCulture, $"header slot {blob[2]} is unknown");
            return false;
        }
        int count = blob[3];
        var length = BinaryPrimitives.ReadUInt32LittleEndian(blob[4..]);
        if (length is <= ChunkSize or > MaxLength || count != ChunksFor((int)length))
        {
            problem = "the header's length and chunk count do not agree";
            return false;
        }
        header = new ChunkHeader(blob[2], count, (int)length, blob[8..Size].ToArray());
        problem = null;
        return true;
    }

    /// <summary>The blob of this header.</summary>
    public byte[] Encode()
    {
        var blob = new byte[Size];
        blob[0] = Marker;
        blob[1] = Version;
        blob[2] = (byte)Slot;
        blob[3] = (byte)Count;
        BinaryPrimitives.WriteUInt32LittleEndian(blob.AsSpan(4), (uint)Length);
        Sha256.Span.CopyTo(blob.AsSpan(8));
        return blob;
    }

    /// <summary>The number in the name of chunk <paramref name="index"/> (1-based): <c>#&lt;number&gt;</c>.</summary>
    public int ChunkNumber(int index) => (Slot * MaxChunks) + index;

    /// <summary>Whether <c>#&lt;number&gt;</c> is one of this header's chunks.</summary>
    public bool HasChunk(int number) => number > Slot * MaxChunks && number <= (Slot * MaxChunks) + Count;

    /// <summary>How many bytes chunk <paramref name="index"/> (1-based) holds.</summary>
    public int ChunkLength(int index) => index < Count ? ChunkSize : Length - (ChunkSize * (Count - 1));

    /// <summary>Whether the value's SHA-256 is the one this header recorded.</summary>
    public bool Matches(ReadOnlySpan<byte> value) =>
        value.Length == Length && CryptographicOperations.FixedTimeEquals(SHA256.HashData(value), Sha256.Span);
}
