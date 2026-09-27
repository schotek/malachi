// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/FramingTests.swift.

using System.Collections.Generic;
using System.Linq;
using System.Text;
using Malachi.Core.Transport;
using Xunit;

namespace Malachi.Core.Tests.Transport;

public sealed class FramingTests
{
    [Fact]
    public void OneLinePerChunk() => Assert.Equal(["a", "b"], Lines(["a\n", "b\n"]));

    [Fact]
    public void TwoLinesInOneChunk() => Assert.Equal(["{\"a\":1}", "{\"b\":2}"], Lines(["{\"a\":1}\n{\"b\":2}\n"]));

    [Fact]
    public void LineSplitAcrossChunks() => Assert.Equal(["{\"a\":1}", "{\"b\":2}"], Lines(["{\"a\"", ":", "1}\n{\"b", "\":2}\n"]));

    [Fact]
    public void CrlfAndEmptyLines() => Assert.Equal(["a", "b"], Lines(["a\r\n\n\r\nb\n"]));

    [Fact]
    public void IncompleteTailWaits()
    {
        var f = new LineFramer();
        Assert.Empty(f.Append("abc"u8));
        Assert.Equal(3, f.PendingBytes);
        Assert.Equal(["abc"], Text(f.Append("\n"u8)));
        Assert.Equal(0, f.PendingBytes);
    }

    [Fact]
    public void TooLongLineIsRefusedAndBufferReset()
    {
        var f = new LineFramer(maxLine: 8);
        Assert.Empty(f.Append("12345678"u8)); // exactly at the cap passes
        var e = Assert.Throws<LineTooLongException>(() => f.Append("9"u8));
        Assert.Equal(8, e.Limit);
        Assert.Equal(0, f.PendingBytes);
        Assert.Equal(["ok"], Text(f.Append("ok\n"u8)));
    }

    // Beyond Swift: a buffer that grows and moves its tail keeps the lines.
    [Fact]
    public void ManySmallChunksAndALargeLine()
    {
        var f = new LineFramer();
        var big = new string('x', 300_000);
        var input = $"first\n{big}\nlast\n";
        var lines = new List<string>();
        for (var i = 0; i < input.Length; i += 777)
        {
            lines.AddRange(Text(f.Append(Encoding.UTF8.GetBytes(input.Substring(i, System.Math.Min(777, input.Length - i))))));
        }
        Assert.Equal(["first", big, "last"], lines);
        Assert.Equal(0, f.PendingBytes);
    }

    private static List<string> Lines(string[] chunks, int maxLine = LineFramer.DefaultMaxLine)
    {
        var f = new LineFramer(maxLine);
        var @out = new List<string>();
        foreach (var c in chunks)
        {
            @out.AddRange(Text(f.Append(Encoding.UTF8.GetBytes(c))));
        }
        return @out;
    }

    private static List<string> Text(IReadOnlyList<byte[]> lines) => [.. lines.Select(l => Encoding.UTF8.GetString(l))];
}
