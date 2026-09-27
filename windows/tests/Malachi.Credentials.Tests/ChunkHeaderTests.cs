// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows only, no Swift counterpart: the header a chunked value's main
// item carries (ChunkHeader).

using System;
using System.Buffers.Binary;
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
        var blob = ChunkHeader.For(value).Encode();
        Assert.Equal(ChunkHeader.Size, blob.Length);
        Assert.Equal(0xFF, blob[0]);
        Assert.Equal(1, blob[1]);
        Assert.Equal(3, BinaryPrimitives.ReadUInt16LittleEndian(blob.AsSpan(2)));
        Assert.Equal(6144u, BinaryPrimitives.ReadUInt32LittleEndian(blob.AsSpan(4)));
        Assert.Equal(SHA256.HashData(value), blob[8..]);
    }

    [Fact]
    public void DecodesWhatItEncodes()
    {
        var value = Filled(6144);
        Assert.True(ChunkHeader.TryDecode(ChunkHeader.For(value).Encode(), out var header, out var problem), problem);
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
        Assert.Throws<ArgumentOutOfRangeException>(() => ChunkHeader.For(new byte[length]));
    }

    [Theory]
    [InlineData("marker", "no header")]
    [InlineData("short", "no header")]
    [InlineData("version", "header version 2 is unknown")]
    [InlineData("longer", "the header has the wrong size")]
    [InlineData("shorter", "the header has the wrong size")]
    [InlineData("count", "the header's length and chunk count do not agree")]
    [InlineData("one item", "the header's length and chunk count do not agree")]
    [InlineData("too long", "the header's length and chunk count do not agree")]
    public void RefusesAHeaderItDidNotWrite(string damage, string expected)
    {
        var blob = ChunkHeader.For(Filled(6144)).Encode();
        blob = damage switch
        {
            "marker" => [0xFE, .. blob[1..]],
            "short" => blob[..1],
            "version" => [blob[0], 2, .. blob[2..]],
            "longer" => [.. blob, 0],
            "shorter" => blob[..^1],
            "count" => Patched(blob, b => BinaryPrimitives.WriteUInt16LittleEndian(b.AsSpan(2), 4)),
            "one item" => Patched(blob, b =>
            {
                BinaryPrimitives.WriteUInt16LittleEndian(b.AsSpan(2), 1);
                BinaryPrimitives.WriteUInt32LittleEndian(b.AsSpan(4), 2560);
            }),
            "too long" => Patched(blob, b =>
            {
                BinaryPrimitives.WriteUInt16LittleEndian(b.AsSpan(2), 17);
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
