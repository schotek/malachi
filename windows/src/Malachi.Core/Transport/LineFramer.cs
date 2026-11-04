// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Transport/LineFramer.swift; Go:
// backend/internal/rpc (maxLineBytes), ui/internal/client/client.go
// (readLoop's bufio framing).
//
// A class where Swift has a mutating struct: RpcClient's read loop and
// the test daemon each own one and never share it.

using System;
using System.Collections.Generic;

namespace Malachi.Core.Transport;

/// <summary>
/// Splits a byte stream into newline-delimited frames, the daemon's framing
/// (docs/api.md §1: one JSON object per line, terminated by "\n", no
/// Content-Length). A trailing "\r" is dropped, empty lines are skipped, and
/// a line longer than the daemon's own cap is refused instead of buffered
/// without bound. Not thread-safe: one reader feeds it.
/// </summary>
public sealed class LineFramer
{
    /// <summary>The daemon's server-side cap (internal/rpc maxLineBytes): 32 MiB.</summary>
    public const int DefaultMaxLine = 32 << 20;

    private byte[] buffer = [];
    private int start;   // the first byte not taken yet
    private int end;     // one past the last byte received
    private int scanned; // bytes after start known to hold no newline

    /// <summary>A framer that refuses unterminated data beyond <paramref name="maxLine"/> bytes.</summary>
    public LineFramer(int maxLine = DefaultMaxLine)
    {
        ArgumentOutOfRangeException.ThrowIfNegativeOrZero(maxLine);
        MaxLine = maxLine;
    }

    /// <summary>The cap on bytes waiting for their newline.</summary>
    public int MaxLine { get; }

    /// <summary>Bytes waiting for their newline.</summary>
    public int PendingBytes => end - start;

    /// <summary>
    /// Appends a chunk and returns every line it completes, without the
    /// terminator, each a copy of its own. Throws
    /// <see cref="LineTooLongException"/> (and drops the buffer) when the
    /// unterminated data exceeds <see cref="MaxLine"/>; the lines the chunk
    /// completed before it are dropped with it, as Swift's are.
    /// </summary>
    public IReadOnlyList<byte[]> Append(ReadOnlySpan<byte> chunk)
    {
        Store(chunk);
        List<byte[]>? lines = null;
        while (true)
        {
            var nl = buffer.AsSpan(start + scanned, end - start - scanned).IndexOf((byte)'\n');
            if (nl < 0)
            {
                break;
            }
            var line = buffer.AsSpan(start, scanned + nl);
            if (!line.IsEmpty && line[^1] == (byte)'\r')
            {
                line = line[..^1];
            }
            if (!line.IsEmpty)
            {
                (lines ??= []).Add(line.ToArray());
            }
            start += scanned + nl + 1;
            scanned = 0;
        }
        scanned = end - start;
        if (end - start > MaxLine)
        {
            buffer = [];
            start = end = scanned = 0;
            throw new LineTooLongException(MaxLine);
        }
        if (start == end)
        {
            start = end = 0;
        }
        return lines ?? (IReadOnlyList<byte[]>)[];
    }

    // Appends chunk behind the pending bytes, moving them to the front or
    // growing the buffer when the tail has no room.
    private void Store(ReadOnlySpan<byte> chunk)
    {
        if (chunk.IsEmpty)
        {
            return;
        }
        var pending = end - start;
        if (buffer.Length - end < chunk.Length)
        {
            if (buffer.Length - pending >= chunk.Length && pending <= buffer.Length / 2)
            {
                buffer.AsSpan(start, pending).CopyTo(buffer);
            }
            else
            {
                var grown = new byte[Math.Max(buffer.Length * 2, pending + chunk.Length)];
                buffer.AsSpan(start, pending).CopyTo(grown);
                buffer = grown;
            }
            start = 0;
            end = pending;
        }
        chunk.CopyTo(buffer.AsSpan(end));
        end += chunk.Length;
    }
}
