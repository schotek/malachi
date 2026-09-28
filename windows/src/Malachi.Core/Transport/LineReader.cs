// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the handshake's reading in
// macos/Sources/MalachiCore/Transport/RPCClient.swift (handshakeBytes,
// handshakeLine, takeHandshakeLine); Go: backend/pkg/api/handshake.go
// (readLine over the caller's bufio.Reader), ui/internal/client/client.go
// (one reader for the handshake and readLoop).
//
// Pull-based, as Go's bufio.Reader: the handshake reads line by line, and
// the bytes it read beyond its last answer stay in Buffered for the read
// loop, which hands them to its LineFramer once the client is connected.

using System;
using System.IO;
using System.Threading;
using System.Threading.Tasks;

namespace Malachi.Core.Transport;

/// <summary>
/// The receiving side of one connection while the handshake reads it:
/// complete lines, each at most a cap long, taken from a buffer that the
/// stream fills.
/// </summary>
internal sealed class LineReader
{
    private readonly Stream stream;
    private readonly byte[] buffer;
    private int start;
    private int end;

    /// <summary>A reader of <paramref name="stream"/> for lines of at most <paramref name="maxLine"/> bytes.</summary>
    public LineReader(Stream stream, int maxLine)
    {
        this.stream = stream;
        MaxLine = maxLine;
        buffer = new byte[2 * maxLine];
    }

    /// <summary>The longest line, "\n" included.</summary>
    public int MaxLine { get; }

    /// <summary>What was read beyond the last line taken.</summary>
    public ReadOnlySpan<byte> Buffered => buffer.AsSpan(start, end - start);

    /// <summary>
    /// The next line without its "\n"; a "\r" stays and an empty line is a
    /// line, as for Go's handshake. Complete lines already read come first;
    /// only then is <paramref name="cancellationToken"/> looked at, so a line
    /// that arrived in time is still an answer. Throws
    /// <see cref="LineTooLongException"/> for a line over the cap, finished
    /// or not, and <see cref="EndOfStreamException"/> when the peer closed
    /// before a newline.
    /// </summary>
    public async Task<byte[]> ReadLineAsync(CancellationToken cancellationToken)
    {
        while (true)
        {
            var pending = buffer.AsSpan(start, end - start);
            var nl = pending.IndexOf((byte)'\n');
            if (nl >= 0)
            {
                if (nl + 1 > MaxLine)
                {
                    throw new LineTooLongException(MaxLine);
                }
                var line = pending[..nl].ToArray();
                start += nl + 1;
                return line;
            }
            if (pending.Length > MaxLine)
            {
                throw new LineTooLongException(MaxLine);
            }
            if (end == buffer.Length)
            {
                pending.CopyTo(buffer);
                end -= start;
                start = 0;
            }
            cancellationToken.ThrowIfCancellationRequested();
            var n = await stream.ReadAsync(buffer.AsMemory(end), cancellationToken).ConfigureAwait(false);
            if (n == 0)
            {
                throw new EndOfStreamException();
            }
            end += n;
        }
    }
}
