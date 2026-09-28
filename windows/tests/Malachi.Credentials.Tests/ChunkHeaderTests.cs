// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows only, no Swift counterpart: the header a chunked value's main
// item carries (ChunkHeader), and the two slots its chunks alternate
// between.

using System;
using System.Buffers.Binary;
using System.Linq;
using System.Security.Cryptography;
using System.Text.Unicode;
using Xunit;

namespace Malachi.Credentials.Tests;

public sealed class ChunkHeaderTests
{
    [Fact]
    public void AChunkIsWhatOneCredentialHolds()
    {
        Assert.Equal(2560, ChunkHeader.ChunkSize);
        Assert.Equal(16, ChunkHeader.MaxChunks);
        Assert.Equal(2, ChunkHeader.Slots);
        Assert.Equal(40960, ChunkHeader.MaxLength);
    }

    [Theory]
    [InlineData(0, 0)]
    [InlineData(1, 0)]
    [InlineData(2560, 0)]
    [InlineData(2561, 2)]
    [InlineData(5120, 2)]
    [InlineData(5121, 3)]
    [InlineData(6144, 3)]
    [InlineData(40960, 16)]
    public void ChunksForCountsWholeChunks(int length, int chunks)
    {
        Assert.Equal(chunks, ChunkHeader.ChunksFor(length));
    }

    [Fact]
    public void EncodesTheLayoutItDocuments()
    {
        var value = Filled(6144);
        var blob = ChunkHeader.For(value, 1).Encode();
        Assert.Equal(ChunkHeader.Size, blob.Length);
        Assert.Equal(0xFF, blob[0]);
        Assert.Equal(1, blob[1]);
        Assert.Equal(1, blob[2]);
        Assert.Equal(3, blob[3]);
        Assert.Equal(6144u, BinaryPrimitives.ReadUInt32LittleEndian(blob.AsSpan(4)));
        Assert.Equal(SHA256.HashData(value), blob[8..]);
        Assert.Equal(0, ChunkHeader.For(value, 0).Encode()[2]);
    }

    [Fact]
    public void DecodesWhatItEncodes()
    {
        var value = Filled(6144);
        Assert.True(ChunkHeader.TryDecode(ChunkHeader.For(value, 1).Encode(), out var header, out var problem), problem);
        Assert.Equal(1, header.Slot);
        Assert.Equal(3, header.Count);
        Assert.Equal(6144, header.Length);
        int[] lengths = [2560, 2560, 1024];
        Assert.Equal(lengths, new[] { header.ChunkLength(1), header.ChunkLength(2), header.ChunkLength(3) });
        Assert.True(header.Matches(value));
        value[100] ^= 1;
        Assert.False(header.Matches(value));
        Assert.False(header.Matches(value.AsSpan(1)));
    }

    [Theory]
    [InlineData(2560)]
    [InlineData(0)]
    [InlineData(40961)]
    public void DescribesOnlyValuesThatNeedChunks(int length)
    {
        Assert.Throws<ArgumentOutOfRangeException>(() => ChunkHeader.For(new byte[length], 0));
    }

    [Theory]
    [InlineData(-1)]
    [InlineData(2)]
    public void HasOnlyTwoSlots(int slot)
    {
        Assert.Throws<ArgumentOutOfRangeException>(() => ChunkHeader.For(Filled(6144), slot));
    }

    [Theory]
    [InlineData(0, 6144, new[] { 1, 2, 3 })]
    [InlineData(1, 6144, new[] { 17, 18, 19 })]
    [InlineData(0, 40960, new[] { 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16 })]
    [InlineData(1, 40960, new[] { 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32 })]
    public void TheSlotsNumberTheirChunksApart(int slot, int length, int[] numbers)
    {
        var header = ChunkHeader.For(Filled(length), slot);
        Assert.Equal(numbers, Enumerable.Range(1, header.Count).Select(header.ChunkNumber));
        for (var number = -1; number <= 40; number++)
        {
            Assert.Equal(numbers.Contains(number), header.HasChunk(number));
        }
    }

    [Theory]
    [InlineData("marker", "no header")]
    [InlineData("short", "no header")]
    [InlineData("version", "header version 2 is unknown")]
    [InlineData("longer", "the header has the wrong size")]
    [InlineData("shorter", "the header has the wrong size")]
    [InlineData("slot", "header slot 2 is unknown")]
    [InlineData("count", "the header's length and chunk count do not agree")]
    [InlineData("one item", "the header's length and chunk count do not agree")]
    [InlineData("too long", "the header's length and chunk count do not agree")]
    public void RefusesAHeaderItDidNotWrite(string damage, string expected)
    {
        var blob = ChunkHeader.For(Filled(6144), 0).Encode();
        blob = damage switch
        {
            "marker" => [0xFE, .. blob[1..]],
            "short" => blob[..1],
            "version" => [blob[0], 2, .. blob[2..]],
            "longer" => [.. blob, 0],
            "shorter" => blob[..^1],
            "slot" => Patched(blob, b => b[2] = 2),
            "count" => Patched(blob, b => b[3] = 4),
            "one item" => Patched(blob, b =>
            {
                b[3] = 1;
                BinaryPrimitives.WriteUInt32LittleEndian(b.AsSpan(4), 2560);
            }),
            "too long" => Patched(blob, b =>
            {
                b[3] = 17;
                BinaryPrimitives.WriteUInt32LittleEndian(b.AsSpan(4), 40961);
            }),
            _ => throw new ArgumentOutOfRangeException(nameof(damage)),
        };
        Assert.False(ChunkHeader.TryDecode(blob, out _, out var problem));
        Assert.Equal(expected, problem);
    }

    [Fact]
    public void NoUtf8TextBeginsWithTheMarker()
    {
        // What keeps a header apart from a stored value: every value is UTF-8.
        for (var next = 0; next < 256; next++)
        {
            Assert.False(Utf8.IsValid([ChunkHeader.Marker, (byte)next]));
        }
        Assert.True(ChunkHeader.IsHeader([ChunkHeader.Marker]));
        Assert.False(ChunkHeader.IsHeader([]));
        Assert.False(ChunkHeader.IsHeader("hunter2"u8));
    }

    private static byte[] Filled(int length)
    {
        var value = new byte[length];
        for (var i = 0; i < length; i++)
        {
            value[i] = (byte)('a' + (i % 26));
        }
        return value;
    }

    private static byte[] Patched(byte[] blob, Action<byte[]> patch)
    {
        patch(blob);
        return blob;
    }
}
