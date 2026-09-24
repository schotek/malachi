// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

@Suite struct FramingTests {
    private func lines(_ chunks: [String], maxLine: Int = LineFramer.defaultMaxLine) throws -> [String] {
        var f = LineFramer(maxLine: maxLine)
        var out: [String] = []
        for c in chunks {
            out += try f.append(Data(c.utf8)).map { String(decoding: $0, as: UTF8.self) }
        }
        return out
    }

    @Test func oneLinePerChunk() throws {
        #expect(try lines(["a\n", "b\n"]) == ["a", "b"])
    }

    @Test func twoLinesInOneChunk() throws {
        #expect(try lines(["{\"a\":1}\n{\"b\":2}\n"]) == ["{\"a\":1}", "{\"b\":2}"])
    }

    @Test func lineSplitAcrossChunks() throws {
        #expect(try lines(["{\"a\"", ":", "1}\n{\"b", "\":2}\n"]) == ["{\"a\":1}", "{\"b\":2}"])
    }

    @Test func crlfAndEmptyLines() throws {
        #expect(try lines(["a\r\n\n\r\nb\n"]) == ["a", "b"])
    }

    @Test func incompleteTailWaits() throws {
        var f = LineFramer()
        #expect(try f.append(Data("abc".utf8)).isEmpty)
        #expect(f.pendingBytes == 3)
        #expect(try f.append(Data("\n".utf8)).map { String(decoding: $0, as: UTF8.self) } == ["abc"])
        #expect(f.pendingBytes == 0)
    }

    @Test func tooLongLineIsRefusedAndBufferReset() throws {
        var f = LineFramer(maxLine: 8)
        #expect(try f.append(Data("12345678".utf8)).isEmpty) // exactly at the cap passes
        #expect(throws: LineFramer.LineTooLong(limit: 8)) { try f.append(Data("9".utf8)) }
        #expect(f.pendingBytes == 0)
        #expect(try f.append(Data("ok\n".utf8)).map { String(decoding: $0, as: UTF8.self) } == ["ok"])
    }
}
