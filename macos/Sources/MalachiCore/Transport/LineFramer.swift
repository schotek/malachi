// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// Splits a byte stream into newline-delimited frames, the daemon's framing
/// (docs/api.md §1: one JSON object per line, terminated by "\n", no
/// Content-Length). A trailing "\r" is dropped, empty lines are skipped, and
/// a line longer than the daemon's own cap is refused instead of buffered
/// without bound.
public struct LineFramer: Sendable {
    public struct LineTooLong: Error, Sendable, Equatable {
        public let limit: Int
    }

    /// The daemon's server-side cap (internal/rpc maxLineBytes).
    public static let defaultMaxLine = 32 << 20

    private var buffer = Data()
    private var scanned = 0 // leading bytes of buffer known to hold no newline
    private let maxLine: Int

    public init(maxLine: Int = LineFramer.defaultMaxLine) {
        self.maxLine = maxLine
    }

    /// Appends a chunk and returns every line it completes, without the
    /// terminator. Throws `LineTooLong` (and drops the buffer) when the
    /// unterminated data exceeds the cap.
    public mutating func append(_ chunk: Data) throws -> [Data] {
        buffer.append(chunk)
        var lines: [Data] = []
        while let nl = buffer[(buffer.startIndex + scanned)...].firstIndex(of: 0x0A) {
            var line = buffer[buffer.startIndex..<nl]
            if line.last == 0x0D {
                line = line.dropLast()
            }
            if !line.isEmpty {
                lines.append(Data(line)) // re-based copy; the slice indices die with the buffer
            }
            buffer.removeSubrange(buffer.startIndex...nl)
            scanned = 0
        }
        scanned = buffer.count
        if buffer.count > maxLine {
            buffer.removeAll()
            scanned = 0
            throw LineTooLong(limit: maxLine)
        }
        return lines
    }

    /// Bytes waiting for their newline.
    public var pendingBytes: Int { buffer.count }
}
