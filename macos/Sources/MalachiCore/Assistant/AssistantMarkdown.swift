// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/assistant markdown.go, the In App target: the small Markdown
// subset the panel shows the model's answers in (the system prompt asks
// for it). An answer is hostile input like mail: it may quote a message
// verbatim. So this is a linear scanner over a fixed set of constructs: no
// HTML ever, no nesting beyond bold and italic around text, links only to
// http and https, every control character but the newline and the tab
// dropped. The client turns the blocks into its own attributed text;
// nothing here knows about rendering.
//
// Linear: every opening marker finds its closing one through a cursor over
// the closing positions of its kind, which only moves forward (markers are
// met left to right, and one kind never nests in itself), so a megabyte of
// asterisks or brackets costs what a megabyte of letters does. The port
// works on the UTF-8 bytes, as Go does, with Go's character classes
// (AssistantUnicode.swift), and `isWebURL` follows Go's url.Parse (as a
// module on go 1.25 runs it: the last colon of a host starts its port).
// One step beyond the Go source: the target of a "[text](…)" is read
// through cursors too (`URLScan`), because many link openers that share
// one closing parenthesis would each read the whole target again.
//
// This file holds no translatable text.

import Foundation

extension Assistant {
    /// assistant.BlockKind.
    public enum BlockKind: Sendable, Equatable {
        case paragraph, heading, bullet, numbered, code
    }

    /// assistant.Span: a run of text in one style. `link` is an http or
    /// https URL, "" for none; `text` is what is shown.
    public struct Span: Sendable, Equatable {
        public var text: String
        public var bold = false
        public var italic = false
        public var code = false
        public var link = ""

        public init(text: String, bold: Bool = false, italic: Bool = false, code: Bool = false, link: String = "") {
            self.text = text
            self.bold = bold
            self.italic = italic
            self.code = code
            self.link = link
        }
    }

    /// assistant.Block: a paragraph, a heading, a list item or a code
    /// block. `level` is a heading's level (1–3) or a list item's nesting
    /// (0 to `maxListLevel`), 0 otherwise; `number` a numbered item's
    /// number as written, 0 otherwise; `spans` the block's text (a code
    /// block has one code span with its lines, nothing parsed inside, and
    /// none when it is empty).
    public struct Block: Sendable, Equatable {
        public var kind: BlockKind
        public var level = 0
        public var number = 0
        public var spans: [Span]

        public init(kind: BlockKind, level: Int = 0, number: Int = 0, spans: [Span]) {
            self.kind = kind
            self.level = level
            self.number = number
            self.spans = spans
        }
    }

    /// maxListLevel: the deepest list nesting kept; deeper items stay at
    /// it.
    static let maxListLevel = 3

    /// assistant.Markdown: the blocks of `text`. Lines are separated by
    /// "\n", "\r\n" or "\r". A blank line ends a paragraph or list item; a
    /// paragraph's lines stay separate lines ("\n" in the text), and a
    /// line that starts nothing continues the open paragraph or list item.
    /// After at most three spaces: "# ", "## " and "### " start a heading;
    /// a line of three backticks a code block up to the next such line (or
    /// the end: an answer that is still streaming). After any indentation:
    /// "- " and "* " start a bullet, 1 to 9 digits and ". " a numbered
    /// item, nested by indentation (an item indented more than the one
    /// before goes one level deeper, one indented as an earlier one returns
    /// to its level). A heading or list marker with no text after it is
    /// text. Inline: **bold**, *italic* and _italic_ (an underscore only at
    /// a word's edges, so create_draft stays as it is), `code`,
    /// [text](http(s)://…) and bare http(s):// URLs at a word's start. A
    /// marker without its closing half, and a link to anything but http or
    /// https, stays literal text.
    ///
    /// A table (a paragraph's line of cells between "|", a delimiter row of
    /// as many ---, :---, ---: or :---: cells, then rows) is read as one
    /// bullet per row, for the narrow panel: its first cell in bold, then
    /// every other cell that is not empty on a line of its own as "header:
    /// cell". The pipes at a row's edges are optional, "\|" is a pipe
    /// inside a cell, cells beyond the header's are dropped and missing ones
    /// are empty; a blank line, a line without a pipe, a heading or a fence
    /// ends the table, and a table without rows shows nothing. The header
    /// labels cost at most what the rows themselves do: once they would
    /// outweigh the rows so far, the cells go without them.
    public static func markdown(_ text: String) -> [Block] {
        let s = cleanMarkdownText(text)
        return s.withUnsafeBufferPointer { p in
            var parser = MarkdownParser(s: p)
            var start = 0
            var i = 0
            while i < p.count {
                if p[i] == 0x0A {
                    parser.line(start, i)
                    start = i + 1
                }
                i += 1
            }
            parser.line(start, p.count)
            parser.end()
            return parser.blocks
        }
    }

    /// The spans of one block's text (Go's inlineSpans), for the tests.
    static func inline(_ text: String) -> [Span] {
        cleanMarkdownText(text).withUnsafeBufferPointer { inlineSpans($0) }
    }

    /// cleanText: "\r\n" and "\r" become "\n", every control character but
    /// "\n" and "\t" is dropped (a Swift string has no invalid UTF-8 for
    /// Go's U+FFFD to replace).
    static func cleanMarkdownText(_ text: String) -> [UInt8] {
        var b = Array(text.utf8)
        let n = b.withUnsafeMutableBufferPointer { p -> Int in
            var w = 0
            var i = 0
            while i < p.count {
                let c = p[i]
                switch c {
                case 0x0D:
                    p[w] = 0x0A
                    w += 1
                    if i + 1 < p.count, p[i + 1] == 0x0A {
                        i += 1
                    }
                case 0x0A, 0x09:
                    p[w] = c
                    w += 1
                case 0x00..<0x20, 0x7F:
                    break
                case 0xC2 where i + 1 < p.count && p[i + 1] >= 0x80 && p[i + 1] <= 0x9F:
                    i += 1 // a C1 control
                default:
                    p[w] = c
                    w += 1
                }
                i += 1
            }
            return w
        }
        b.removeSubrange(n...)
        return b
    }

    // MARK: Inline

    /// inlineSpans: the spans of a block's text.
    static func inlineSpans(_ s: UnsafeBufferPointer<UInt8>) -> [Span] {
        guard !s.isEmpty else { return [] }
        let inl = Inliner(s)
        inl.run(0, s.count, false, false)
        inl.closeSpan()
        return inl.spans
    }

    /// hasWebScheme: whether s[i..<hi] starts with http:// or https://, in
    /// any case.
    static func hasWebScheme(_ s: UnsafeBufferPointer<UInt8>, _ i: Int, _ hi: Int) -> Bool {
        func at(_ k: Int, _ c: UInt8) -> Bool {
            guard i + k < hi else { return false }
            let b = s[i + k]
            return b == c || (c >= 0x61 && c <= 0x7A && b == c - 0x20)
        }
        guard at(0, 0x68), at(1, 0x74), at(2, 0x74), at(3, 0x70) else { return false } // http
        if at(4, 0x3A) {
            return at(5, 0x2F) && at(6, 0x2F) // ://
        }
        return at(4, 0x73) && at(5, 0x3A) && at(6, 0x2F) && at(7, 0x2F) // s://
    }

    /// isWebURL for a whole string (the tests' check of every link).
    static func isWebURL(_ u: String) -> Bool {
        Array(u.utf8).withUnsafeBufferPointer { b in
            var scan = URLScan()
            return isWebURL(b, 0, b.count, &scan)
        }
    }

    /// isWebURL: whether s[lo..<hi] is an absolute http or https URL with
    /// a host and nothing a URL does not carry literally (no white space,
    /// control characters, quotes, angle brackets, backticks or
    /// backslashes), as Go's url.Parse reads it.
    static func isWebURL(_ s: UnsafeBufferPointer<UInt8>, _ lo: Int, _ hi: Int, _ scan: inout URLScan) -> Bool {
        guard hasWebScheme(s, lo, hi), scan.bad.next(s, lo) >= hi else { return false }
        return goURLHostname(s, lo, hi, &scan)
    }
}

// MARK: - Blocks

/// mdParser: reads the lines of Markdown, which are ranges of `s`.
private struct MarkdownParser {
    let s: UnsafeBufferPointer<UInt8>
    var blocks: [Assistant.Block] = []
    /// inCode while a code block is open; code its lines.
    var inCode = false
    var code: [Range<Int>] = []
    /// The open paragraph or list item, if any, and its lines (trimmed).
    var open: Assistant.Block?
    var lines: [Range<Int>] = []
    /// The indentations of the open list's levels, outermost first.
    var indents: [Int] = []
    /// The open table's header cells, nil when no table is open; budget
    /// what the rows so far leave for the header labels, in bytes.
    var header: [[UInt8]]?
    var budget = 0

    init(s: UnsafeBufferPointer<UInt8>) {
        self.s = s
    }

    mutating func line(_ lo: Int, _ hi: Int) {
        // splitIndent: the columns of the leading spaces and tabs (a tab to
        // the next multiple of 4); the rest starts at r.
        var indent = 0
        var r = lo
        scan: while r < hi {
            switch s[r] {
            case 0x20:
                indent += 1
            case 0x09:
                indent += 4 - indent % 4
            default:
                break scan
            }
            r += 1
        }
        if inCode {
            if indent <= 3, isFence(r, hi) {
                closeCode()
                return
            }
            code.append(lo..<hi)
            return
        }
        if header != nil {
            let fenceOrHeading = indent <= 3 && (isFence(r, hi) || heading(r, hi) != nil)
            if !fenceOrHeading, hasPipe(r, hi) {
                tableRow(r, hi)
                return
            }
            header = nil
        }
        if r == hi { // nothing but spaces and tabs
            flush()
            return
        }
        if indent <= 3, isFence(r, hi) {
            flush()
            indents = []
            inCode = true
            return
        }
        if indent <= 3, let (level, text) = heading(r, hi) {
            flush()
            indents = []
            blocks.append(Assistant.Block(
                kind: .heading, level: level, spans: Assistant.inlineSpans(UnsafeBufferPointer(rebasing: s[text]))))
            return
        }
        if indent <= 3, open?.kind == .paragraph, startTable(r, hi) {
            return
        }
        if let (kind, number, text) = listItem(r, hi) {
            flush()
            open = Assistant.Block(kind: kind, level: listLevel(indent), number: number, spans: [])
            lines = [text]
            return
        }
        let text = trimST(r, hi)
        if open != nil {
            lines.append(text)
            return
        }
        indents = []
        open = Assistant.Block(kind: .paragraph, spans: [])
        lines = [text]
    }

    /// startTable: reads s[r..<hi] as a delimiter row under the open
    /// paragraph's last line; when that line is a header of as many cells,
    /// it leaves the paragraph (the rest of it is flushed) and a table
    /// opens.
    mutating func startTable(_ r: Int, _ hi: Int) -> Bool {
        guard let last = lines.last, hasPipe(r, hi), hasPipe(last.lowerBound, last.upperBound) else { return false }
        let delimiter = tableCells(r, hi)
        guard delimiter.allSatisfy(Self.isDelimiterCell) else { return false }
        let cells = tableCells(last.lowerBound, last.upperBound)
        guard cells.count == delimiter.count else { return false }
        lines.removeLast()
        if !lines.isEmpty {
            flush()
        }
        open = nil
        lines = []
        indents = []
        header = cells
        budget = 0
        return true
    }

    /// tableRow: a row of the open table as a bullet: the first cell in
    /// bold, then "header: cell" for every other cell that is not empty,
    /// each on its own line. A row with no text adds nothing.
    mutating func tableRow(_ r: Int, _ hi: Int) {
        guard let header else { return }
        budget += hi - r
        let cells = tableCells(r, hi)
        var title: [Assistant.Span] = []
        var fields: [[UInt8]] = []
        var i = 0
        while i < cells.count, i < header.count {
            let c = cells[i]
            if c.isEmpty {
                // An empty cell says nothing.
            } else if i == 0 {
                title = Self.spans(c)
                for k in title.indices {
                    title[k].bold = true
                }
            } else if !header[i].isEmpty, header[i].count + 2 <= budget {
                budget -= header[i].count + 2
                fields.append(header[i] + Array(": ".utf8) + c)
            } else {
                fields.append(c)
            }
            i += 1
        }
        var spans = title
        if !fields.isEmpty {
            var text: [UInt8] = title.isEmpty ? [] : [0x0A]
            for (k, f) in fields.enumerated() {
                if k > 0 {
                    text.append(0x0A)
                }
                text.append(contentsOf: f)
            }
            spans += Self.spans(text)
        }
        if !spans.isEmpty {
            blocks.append(Assistant.Block(kind: .bullet, spans: spans))
        }
    }

    /// hasPipe: whether s[lo..<hi] has a "|" that is not escaped, as
    /// `tableCells` reads it.
    func hasPipe(_ lo: Int, _ hi: Int) -> Bool {
        var i = lo
        while i < hi {
            if s[i] == 0x5C, i + 1 < hi, s[i + 1] == 0x7C {
                i += 2
                continue
            }
            if s[i] == 0x7C {
                return true
            }
            i += 1
        }
        return false
    }

    /// tableCells: the cells of a table row, the text between its unescaped
    /// pipes, one pipe at each edge dropped, "\|" read as "|", every cell
    /// trimmed of spaces and tabs.
    func tableCells(_ lo: Int, _ hi: Int) -> [[UInt8]] {
        let t = trimST(lo, hi)
        var lo = t.lowerBound
        var hi = t.upperBound
        if lo < hi, s[lo] == 0x7C {
            lo += 1
        }
        if hi > lo, s[hi - 1] == 0x7C, !(hi - lo >= 2 && s[hi - 2] == 0x5C) {
            hi -= 1
        }
        var cells: [[UInt8]] = []
        var cell: [UInt8] = []
        var i = lo
        while i < hi {
            if s[i] == 0x5C, i + 1 < hi, s[i + 1] == 0x7C {
                cell.append(0x7C)
                i += 2
                continue
            }
            if s[i] == 0x7C {
                cells.append(Self.trimCell(cell))
                cell = []
            } else {
                cell.append(s[i])
            }
            i += 1
        }
        cells.append(Self.trimCell(cell))
        return cells
    }

    /// strings.Trim(cell, " \t").
    static func trimCell(_ b: [UInt8]) -> [UInt8] {
        var lo = 0
        var hi = b.count
        while lo < hi, b[lo] == 0x20 || b[lo] == 0x09 {
            lo += 1
        }
        while hi > lo, b[hi - 1] == 0x20 || b[hi - 1] == 0x09 {
            hi -= 1
        }
        return Array(b[lo..<hi])
    }

    /// delimiterCell: whether c is a delimiter row's cell: dashes, with a
    /// colon at either end or both.
    static func isDelimiterCell(_ c: [UInt8]) -> Bool {
        var lo = 0
        var hi = c.count
        if lo < hi, c[lo] == 0x3A {
            lo += 1
        }
        if hi > lo, c[hi - 1] == 0x3A {
            hi -= 1
        }
        return lo < hi && c[lo..<hi].allSatisfy { $0 == 0x2D }
    }

    /// The spans of a cell's or a row's text.
    static func spans(_ b: [UInt8]) -> [Assistant.Span] {
        b.withUnsafeBufferPointer { Assistant.inlineSpans($0) }
    }

    /// Ends the open paragraph or list item.
    mutating func flush() {
        guard var b = open else { return }
        if lines.count == 1 {
            b.spans = Assistant.inlineSpans(UnsafeBufferPointer(rebasing: s[lines[0]]))
        } else {
            b.spans = joined(lines).withUnsafeBufferPointer { Assistant.inlineSpans($0) }
        }
        blocks.append(b)
        open = nil
        lines = []
    }

    mutating func closeCode() {
        var b = Assistant.Block(kind: .code, spans: [])
        let text = joined(code)
        if !text.isEmpty {
            b.spans = [Assistant.Span(text: String(decoding: text, as: UTF8.self), code: true)]
        }
        blocks.append(b)
        inCode = false
        code = []
    }

    mutating func end() {
        if inCode {
            closeCode()
        }
        flush()
    }

    /// The lines joined with "\n".
    func joined(_ ranges: [Range<Int>]) -> [UInt8] {
        var out: [UInt8] = []
        out.reserveCapacity(ranges.reduce(0) { $0 + $1.count + 1 })
        for (k, r) in ranges.enumerated() {
            if k > 0 {
                out.append(0x0A)
            }
            out.append(contentsOf: UnsafeBufferPointer(rebasing: s[r]))
        }
        return out
    }

    /// listLevel: the level of a list item indented by `indent` columns.
    mutating func listLevel(_ indent: Int) -> Int {
        while let last = indents.last, last > indent {
            indents.removeLast()
        }
        if indents.last.map({ $0 < indent }) ?? true {
            indents.append(indent)
        }
        return min(indents.count - 1, Assistant.maxListLevel)
    }

    /// A line of three backticks from r on.
    func isFence(_ r: Int, _ hi: Int) -> Bool {
        hi - r >= 3 && s[r] == 0x60 && s[r + 1] == 0x60 && s[r + 2] == 0x60
    }

    /// strings.Trim(s[lo..<hi], " \t").
    func trimST(_ lo: Int, _ hi: Int) -> Range<Int> {
        var lo = lo
        var hi = hi
        while lo < hi, s[lo] == 0x20 || s[lo] == 0x09 {
            lo += 1
        }
        while hi > lo, s[hi - 1] == 0x20 || s[hi - 1] == 0x09 {
            hi -= 1
        }
        return lo..<hi
    }

    /// heading: "#", "##" or "###", a space or a tab and a text.
    func heading(_ r: Int, _ hi: Int) -> (Int, Range<Int>)? {
        var n = 0
        while r + n < hi, s[r + n] == 0x23 {
            n += 1
        }
        guard n >= 1, n <= 3, r + n < hi, s[r + n] == 0x20 || s[r + n] == 0x09 else { return nil }
        let text = trimST(r + n, hi)
        return text.isEmpty ? nil : (n, text)
    }

    /// listItem: "- ", "* " or 1 to 9 digits and ". ", and a text.
    func listItem(_ r: Int, _ hi: Int) -> (Assistant.BlockKind, Int, Range<Int>)? {
        if hi - r >= 2, s[r] == 0x2D || s[r] == 0x2A, s[r + 1] == 0x20 {
            let text = trimST(r + 2, hi)
            return text.isEmpty ? nil : (.bullet, 0, text)
        }
        var n = 0
        var digits = 0
        while r + digits < hi, s[r + digits] >= 0x30, s[r + digits] <= 0x39 {
            if digits == 9 {
                return nil
            }
            n = n * 10 + Int(s[r + digits] - 0x30)
            digits += 1
        }
        guard digits > 0, r + digits + 1 < hi, s[r + digits] == 0x2E, s[r + digits + 1] == 0x20 else { return nil }
        let text = trimST(r + digits + 2, hi)
        return text.isEmpty ? nil : (.numbered, n, text)
    }
}

// MARK: - Inline

/// closers: the positions of one kind of closing marker in a block's text,
/// ascending, with a cursor that only moves forward.
private struct Closers {
    var pos: [Int] = []
    var k = 0

    /// The first position at or after q; -1 when there is none.
    /// Successive calls must not ask for a smaller q.
    mutating func next(_ q: Int) -> Int {
        while k < pos.count, pos[k] < q {
            k += 1
        }
        return k < pos.count ? pos[k] : -1
    }
}

/// inliner: reads the inline constructs of one block's text.
private final class Inliner {
    let s: UnsafeBufferPointer<UInt8>
    let n: Int
    /// The closing markers: `, **, *, _, ] and ); opens are the "(".
    var ticks = Closers(), bolds = Closers(), stars = Closers(), unders = Closers(), brackets = Closers(),
        parens = Closers(), opens = Closers()
    /// The cached target of the last "](": the ] it follows, the ) that
    /// ends it (-1 for none) and whether the URL between is a web URL.
    var linkAt = -1
    var linkEnd = -1
    var linkOK = false
    /// The last newline before nlScan.
    var nlScan = 0
    var nlLast = -1
    /// The cursors of the link targets and of the bare URLs.
    var linkScan = Assistant.URLScan()
    var bareScan = Assistant.URLScan()
    /// The finished spans; the open one's style, and its text in buf.
    var spans: [Assistant.Span] = []
    var cur = Assistant.Span(text: "")
    var buf: [UInt8] = []
    var has = false

    init(_ s: UnsafeBufferPointer<UInt8>) {
        self.s = s
        n = s.count
        var j = 0
        while j < n {
            switch s[j] {
            case 0x60: // `
                ticks.pos.append(j)
            case 0x2A: // *
                let next = j + 1 < n ? s[j + 1] : 0
                if next == 0x2A, j > 0, !spaceBefore(j) {
                    bolds.pos.append(j)
                }
                if j > 0, s[j - 1] != 0x2A, next != 0x2A, !spaceBefore(j) {
                    stars.pos.append(j)
                }
            case 0x5F: // _
                if j > 0, s[j - 1] != 0x5F, !spaceBefore(j), j + 1 == n || (s[j + 1] != 0x5F && !wordAt(j + 1)) {
                    unders.pos.append(j)
                }
            case 0x5D: // ]
                brackets.pos.append(j)
            case 0x29: // )
                parens.pos.append(j)
            case 0x28: // (
                opens.pos.append(j)
            default:
                break
            }
            j += 1
        }
    }

    /// run: reads s[lo..<hi] with the style bold and italic around it.
    func run(_ lo: Int, _ hi: Int, _ bold: Bool, _ italic: Bool) {
        var lit = lo
        var i = lo
        while i < hi {
            var next = -1
            switch s[i] {
            case 0x60: // `
                let j = ticks.next(i + 1)
                if j > i + 1, j < hi {
                    text(lit, i, bold, italic)
                    add(i + 1, j, bold, italic, code: true, link: "")
                    next = j + 1
                }
            case 0x2A: // *
                if i + 1 < hi, s[i + 1] == 0x2A {
                    if !bold, opensAt(i + 2, hi) {
                        let j = bolds.next(i + 3)
                        if j >= 0, j + 2 <= hi {
                            text(lit, i, bold, italic)
                            run(i + 2, j, true, italic)
                            next = j + 2
                        }
                    }
                    if next < 0 {
                        i += 2 // a literal "**" stays a pair
                        continue
                    }
                } else if !italic, opensAt(i + 1, hi) {
                    let j = stars.next(i + 2)
                    if j >= 0, j < hi {
                        text(lit, i, bold, italic)
                        run(i + 1, j, bold, true)
                        next = j + 1
                    }
                }
            case 0x5F: // _
                if !italic, i + 1 < hi, s[i + 1] != 0x5F, opensAt(i + 1, hi), !wordBefore(i) {
                    let j = unders.next(i + 2)
                    if j >= 0, j < hi {
                        text(lit, i, bold, italic)
                        run(i + 1, j, bold, true)
                        next = j + 1
                    }
                }
            case 0x5B: // [
                if let (j, k) = link(i, hi) {
                    text(lit, i, bold, italic)
                    add(i + 1, j, bold, italic, code: false, link: string(j + 2, k))
                    next = k + 1
                }
            case 0x68, 0x48: // h H
                if !wordBefore(i), Assistant.hasWebScheme(s, i, hi) {
                    let (end, u) = bareURL(i, hi)
                    guard let u else {
                        i = end // not a URL: literal text, never read again
                        continue
                    }
                    text(lit, i, bold, italic)
                    add(i, u, bold, italic, code: false, link: string(i, u))
                    next = u
                }
            default:
                break
            }
            if next >= 0 {
                lit = next
                i = next
            } else {
                i += 1
            }
        }
        text(lit, hi, bold, italic)
    }

    /// link: reads "[text](url)" at i within hi: the positions of "](" and
    /// ")", when it is a link (text non-empty and on one line, url a web
    /// URL without a "(" of its own, as in Go: every later "](" that ends at
    /// the same ")" puts its "(" inside this target, so the targets read as
    /// URLs never overlap).
    func link(_ i: Int, _ hi: Int) -> (Int, Int)? {
        let j = brackets.next(i + 1)
        guard j > i + 1, j + 1 < hi, s[j + 1] == 0x28 else { return nil }
        if j != linkAt {
            linkAt = j
            linkEnd = parens.next(j + 2)
            let open = opens.next(j + 2)
            linkOK = linkEnd >= 0 && (open < 0 || open > linkEnd) && Assistant.isWebURL(s, j + 2, linkEnd, &linkScan)
        }
        guard linkOK, linkEnd < hi, newlineBefore(j) <= i else { return nil }
        return (j, linkEnd)
    }

    /// newlineBefore: the position of the last "\n" before j, -1 when
    /// there is none. Successive calls must not ask for a smaller j.
    func newlineBefore(_ j: Int) -> Int {
        while nlScan < j {
            if s[nlScan] == 0x0A {
                nlLast = nlScan
            }
            nlScan += 1
        }
        return nlLast
    }

    /// bareURL: reads the bare URL at i within hi: where its run of URL
    /// characters ends (white space, a control character or one of
    /// <>"`[]{}|\^ end it), and the end of the URL without trailing
    /// punctuation (nil when that is not a web URL).
    func bareURL(_ i: Int, _ hi: Int) -> (Int, Int?) {
        var end = i
        var opens = 0
        var closes = 0
        scan: while end < hi {
            let c = s[end]
            if c < 0x80 {
                switch c {
                case 0x3C, 0x3E, 0x22, 0x60, 0x5B, 0x5D, 0x7B, 0x7D, 0x7C, 0x5C, 0x5E: // <>"`[]{}|\^
                    break scan
                case 0x28:
                    opens += 1
                case 0x29:
                    closes += 1
                default:
                    break
                }
            }
            let (r, w) = Assistant.decodeRune(s, end, n)
            if Assistant.isSpace(r) || Assistant.isControl(r) {
                break
            }
            end += w
        }
        var j = end
        while j > i {
            switch s[j - 1] {
            case 0x2E, 0x2C, 0x3B, 0x3A, 0x21, 0x3F, 0x27, 0x2A, 0x5F: // .,;:!?'*_
                j -= 1
                continue
            case 0x29 where closes > opens:
                closes -= 1
                j -= 1
                continue
            default:
                break
            }
            break
        }
        return (end, Assistant.isWebURL(s, i, j, &bareScan) ? j : nil)
    }

    func string(_ lo: Int, _ hi: Int) -> String {
        String(decoding: UnsafeBufferPointer(rebasing: s[lo..<hi]), as: UTF8.self)
    }

    /// text: literal text in the style bold and italic.
    func text(_ lo: Int, _ hi: Int, _ bold: Bool, _ italic: Bool) {
        add(lo, hi, bold, italic, code: false, link: "")
    }

    /// add: s[lo..<hi] in a style, merged into the span before it when
    /// their styles are the same; empty text adds nothing.
    func add(_ lo: Int, _ hi: Int, _ bold: Bool, _ italic: Bool, code: Bool, link: String) {
        guard hi > lo else { return }
        if !(has && cur.bold == bold && cur.italic == italic && cur.code == code && cur.link == link) {
            closeSpan()
            cur = Assistant.Span(text: "", bold: bold, italic: italic, code: code, link: link)
            has = true
        }
        buf.append(contentsOf: UnsafeBufferPointer(rebasing: s[lo..<hi]))
    }

    func closeSpan() {
        guard has else { return }
        cur.text = String(decoding: buf, as: UTF8.self)
        spans.append(cur)
        buf.removeAll(keepingCapacity: true)
        has = false
    }

    /// opensAt: whether an opening marker can end before k: k is within
    /// hi and no space follows the marker.
    func opensAt(_ k: Int, _ hi: Int) -> Bool {
        guard k < hi else { return false }
        let c = s[k]
        if c < 0x80 {
            return !(c == 0x20 || (c >= 0x09 && c <= 0x0D))
        }
        return !Assistant.isSpace(Assistant.decodeRune(s, k, n).0)
    }

    /// spaceBefore: whether the character before j is a space.
    func spaceBefore(_ j: Int) -> Bool {
        let c = s[j - 1]
        if c < 0x80 {
            return c == 0x20 || (c >= 0x09 && c <= 0x0D)
        }
        return Assistant.isSpace(Assistant.decodeLastRune(s, 0, j).0)
    }

    /// wordBefore: whether a letter or digit comes right before i.
    func wordBefore(_ i: Int) -> Bool {
        guard i > 0 else { return false }
        let c = s[i - 1]
        if c < 0x80 {
            return Self.asciiWord(c)
        }
        let r = Assistant.decodeLastRune(s, 0, i).0
        return Assistant.isLetter(r) || Assistant.isDigit(r)
    }

    /// wordAt: whether a letter or digit starts at i.
    func wordAt(_ i: Int) -> Bool {
        guard i < n else { return false }
        let c = s[i]
        if c < 0x80 {
            return Self.asciiWord(c)
        }
        let r = Assistant.decodeRune(s, i, n).0
        return Assistant.isLetter(r) || Assistant.isDigit(r)
    }

    static func asciiWord(_ c: UInt8) -> Bool {
        (c >= 0x30 && c <= 0x39) || (c >= 0x41 && c <= 0x5A) || (c >= 0x61 && c <= 0x7A)
    }
}

// MARK: - Go's url.Parse, as far as isWebURL asks

extension Assistant {
    /// Cursors over one text for the URLs read in it: each answers "the
    /// first position at or after q" of one kind and remembers the answer,
    /// so that URLs read left to right, overlapping or not, cost one pass.
    /// Each cursor serves one kind of question, whose positions grow from
    /// one URL to the next; any order is still answered correctly, only
    /// more slowly.
    struct URLScan {
        /// A character isWebURL refuses: white space, a control character,
        /// `"`, `<`, `>`, `` ` `` or `\`.
        var bad = Finder(kind: .bad)
        var hash = Finder(kind: .byte(0x23))
        var question = Finder(kind: .byte(0x3F))
        var slash = Finder(kind: .byte(0x2F))
        /// A "%" without two hex digits after it, from the path and from
        /// the fragment.
        var pathPercent = Finder(kind: .badPercent)
        var fragmentPercent = Finder(kind: .badPercent)
    }

    struct Finder {
        enum Kind: Equatable {
            case bad
            case byte(UInt8)
            case badPercent
        }

        let kind: Kind
        /// Nothing of the kind in [from, at); `at` is one, or the end.
        var from = Int.max
        var at = Int.max

        init(kind: Kind) {
            self.kind = kind
        }

        /// The first position at or after q (on a character boundary)
        /// that is of the kind; the text's length when there is none.
        mutating func next(_ s: UnsafeBufferPointer<UInt8>, _ q: Int) -> Int {
            if q >= from, q <= at {
                return at
            }
            var i = q
            while i < s.count {
                if i >= from, i <= at {
                    i = at
                    break
                }
                let (hit, w) = matches(s, i)
                if hit {
                    break
                }
                i += w
            }
            from = q
            at = i
            return i
        }

        private func matches(_ s: UnsafeBufferPointer<UInt8>, _ i: Int) -> (Bool, Int) {
            let c = s[i]
            switch kind {
            case .byte(let b):
                return (c == b, 1)
            case .badPercent:
                guard c == 0x25 else { return (false, 1) }
                return (!(i + 2 < s.count && isHexDigit(s[i + 1]) && isHexDigit(s[i + 2])), 1)
            case .bad:
                if c < 0x80 {
                    return (c <= 0x20 || c == 0x7F || c == 0x22 || c == 0x3C || c == 0x3E || c == 0x60 || c == 0x5C, 1)
                }
                let (r, w) = Assistant.decodeRune(s, i, s.count)
                return (isSpace(r) || isControl(r), w)
            }
        }
    }

    static func isHexDigit(_ c: UInt8) -> Bool {
        (c >= 0x30 && c <= 0x39) || (c >= 0x41 && c <= 0x46) || (c >= 0x61 && c <= 0x66)
    }

    private static func hexValue(_ c: UInt8) -> UInt8 {
        switch c {
        case 0x30...0x39: return c - 0x30
        case 0x41...0x46: return c - 0x41 + 10
        default: return c - 0x61 + 10
        }
    }

    /// shouldEscape(c, encodeHost) == false: what an ASCII byte of a host
    /// may be: a letter, a digit or one of !$&'()*+,;=:[]<>"-_.~
    private static func hostByte(_ c: UInt8) -> Bool {
        switch c {
        case 0x30...0x39, 0x41...0x5A, 0x61...0x7A,
             0x21, 0x24, 0x26, 0x27, 0x28, 0x29, 0x2A, 0x2B, 0x2C, 0x3B, 0x3D, 0x3A, 0x5B, 0x5D, 0x3C, 0x3E, 0x22,
             0x2D, 0x5F, 0x2E, 0x7E:
            return true
        default:
            return false
        }
    }

    /// validUserinfo: a letter, a digit or one of -._:~!$&'()*+,;=%@
    private static func userinfoByte(_ c: UInt8) -> Bool {
        switch c {
        case 0x30...0x39, 0x41...0x5A, 0x61...0x7A,
             0x2D, 0x2E, 0x5F, 0x3A, 0x7E, 0x21, 0x24, 0x26, 0x27, 0x28, 0x29, 0x2A, 0x2B, 0x2C, 0x3B, 0x3D, 0x25, 0x40:
            return true
        default:
            return false
        }
    }

    /// Whether url.Parse reads s[lo..<hi] (an http or https URL without
    /// the characters isWebURL refuses) and its Hostname() is not empty.
    static func goURLHostname(_ s: UnsafeBufferPointer<UInt8>, _ lo: Int, _ hi: Int, _ scan: inout URLScan) -> Bool {
        // Parse: "#fragment" cut off first, its escapes checked last.
        let hash = min(scan.hash.next(s, lo), hi)
        // parse: the scheme ("http" or "https") and its ":", then the
        // query cut off at the first "?", unchecked.
        var restStart = lo
        while s[restStart] != 0x3A {
            restStart += 1
        }
        restStart += 1
        let restEnd = min(scan.question.next(s, restStart), hash)
        // "//authority/path": the authority ends at the first "/".
        let authStart = restStart + 2
        guard authStart <= restEnd else { return false }
        let authEnd = min(scan.slash.next(s, authStart), restEnd)
        guard let host = parseAuthority(s, authStart, authEnd) else { return false }
        // setPath and setFragment: only the escapes can fail.
        guard escapesOK(s, authEnd, restEnd, &scan.pathPercent) else { return false }
        if hash < hi, !escapesOK(s, hash + 1, hi, &scan.fragmentPercent) {
            return false
        }
        return !hostname(host).isEmpty
    }

    /// unescape of s[lo..<hi] in a mode without character rules (path,
    /// fragment): every "%" has two hex digits after it within the range.
    private static func escapesOK(
        _ s: UnsafeBufferPointer<UInt8>, _ lo: Int, _ hi: Int, _ badPercent: inout Finder
    ) -> Bool {
        guard lo < hi else { return true }
        if badPercent.next(s, lo) < hi {
            return false
        }
        // A "%" in the last two bytes lacks its digits within the range.
        if s[hi - 1] == 0x25 || (hi - 2 >= lo && s[hi - 2] == 0x25) {
            return false
        }
        return true
    }

    /// Escapes checked directly (the userinfo, which is short).
    private static func escapesOK(_ s: UnsafeBufferPointer<UInt8>, _ lo: Int, _ hi: Int) -> Bool {
        var i = lo
        while i < hi {
            if s[i] == 0x25 {
                guard i + 2 < hi, isHexDigit(s[i + 1]), isHexDigit(s[i + 2]) else { return false }
                i += 3
            } else {
                i += 1
            }
        }
        return true
    }

    /// The last position of `c` in s[lo..<hi].
    private static func lastIndex(_ s: UnsafeBufferPointer<UInt8>, _ c: UInt8, _ lo: Int, _ hi: Int) -> Int? {
        var k = hi - 1
        while k >= lo {
            if s[k] == c {
                return k
            }
            k -= 1
        }
        return nil
    }

    /// parseAuthority: the host (unescaped), nil on an error of the host or
    /// the userinfo.
    private static func parseAuthority(_ s: UnsafeBufferPointer<UInt8>, _ lo: Int, _ hi: Int) -> [UInt8]? {
        let at = lastIndex(s, 0x40, lo, hi)
        guard let host = parseHost(s, at.map { $0 + 1 } ?? lo, hi) else { return nil }
        guard let at else { return host }
        // validUserinfo (ASCII only), then the escapes of the user and the
        // password.
        var i = lo
        while i < at {
            guard userinfoByte(s[i]) else { return nil }
            i += 1
        }
        var colon = lo
        while colon < at, s[colon] != 0x3A {
            colon += 1
        }
        if colon < at {
            guard escapesOK(s, lo, colon), escapesOK(s, colon + 1, at) else { return nil }
        } else {
            guard escapesOK(s, lo, at) else { return nil }
        }
        return host
    }

    /// parseHost: the host (unescaped), nil when url.Parse refuses it.
    private static func parseHost(_ s: UnsafeBufferPointer<UInt8>, _ lo: Int, _ hi: Int) -> [UInt8]? {
        let lastOpen = lastIndex(s, 0x5B, lo, hi)
        if let lastOpen, lastOpen > lo {
            return nil // invalid IP-literal
        }
        if lastOpen == lo {
            guard let close = lastIndex(s, 0x5D, lo, hi) else { return nil }
            guard validOptionalPort(s, close + 1, hi) else { return nil }
            let name = (lo + 1)..<close
            var zone = name.lowerBound
            while zone + 2 < name.upperBound, !(s[zone] == 0x25 && s[zone + 1] == 0x32 && s[zone + 2] == 0x35) {
                zone += 1
            }
            var unescaped: [UInt8]
            if zone + 2 < name.upperBound {
                guard let host = unescapeHost(s, name.lowerBound, zone, zone: false),
                      let z = unescapeHost(s, zone, name.upperBound, zone: true) else { return nil }
                unescaped = host + z
            } else {
                guard let host = unescapeHost(s, name.lowerBound, name.upperBound, zone: false) else { return nil }
                unescaped = host
            }
            guard ipv6Literal(unescaped) else { return nil }
            unescaped.insert(0x5B, at: 0)
            unescaped.append(0x5D)
            unescaped.append(contentsOf: UnsafeBufferPointer(rebasing: s[(close + 1)..<hi]))
            return unescaped
        }
        // Not strict about colons (urlstrictcolons=0, a go 1.25 module):
        // the last one starts the port.
        if let colon = lastIndex(s, 0x3A, lo, hi), !validOptionalPort(s, colon, hi) {
            return nil
        }
        return unescapeHost(s, lo, hi, zone: false)
    }

    /// validOptionalPort: "" or ":" and digits.
    private static func validOptionalPort(_ s: UnsafeBufferPointer<UInt8>, _ lo: Int, _ hi: Int) -> Bool {
        guard lo < hi else { return true }
        guard s[lo] == 0x3A else { return false }
        var i = lo + 1
        while i < hi {
            guard s[i] >= 0x30, s[i] <= 0x39 else { return false }
            i += 1
        }
        return true
    }

    /// unescape in the modes encodeHost and encodeZone: a "%" needs two hex
    /// digits and may stand only for a byte from 0x80 up (the host), or
    /// for a host byte or a space (the zone), "%25" always; an ASCII byte
    /// must be a host byte.
    private static func unescapeHost(_ s: UnsafeBufferPointer<UInt8>, _ lo: Int, _ hi: Int, zone: Bool) -> [UInt8]? {
        var out: [UInt8] = []
        var i = lo
        while i < hi {
            let c = s[i]
            if c == 0x25 {
                guard i + 2 < hi, isHexDigit(s[i + 1]), isHexDigit(s[i + 2]) else { return nil }
                let v = hexValue(s[i + 1]) << 4 | hexValue(s[i + 2])
                let pct25 = s[i + 1] == 0x32 && s[i + 2] == 0x35
                if !zone, hexValue(s[i + 1]) < 8, !pct25 {
                    return nil
                }
                if zone, !pct25, v != 0x20, !(v < 0x80 && hostByte(v)) {
                    return nil
                }
                out.append(v)
                i += 3
                continue
            }
            if c < 0x80, !hostByte(c) {
                return nil
            }
            out.append(c)
            i += 1
        }
        return out
    }

    /// Hostname: the host without a valid ":port" and without the brackets
    /// of an IPv6 literal.
    private static func hostname(_ host: [UInt8]) -> [UInt8] {
        var h = host
        if let colon = h.lastIndex(of: 0x3A),
           h[(colon + 1)...].allSatisfy({ $0 >= 0x30 && $0 <= 0x39 }) {
            h = Array(h[..<colon])
        }
        if h.count >= 2, h.first == 0x5B, h.last == 0x5D {
            h = Array(h[1..<(h.count - 1)])
        }
        return h
    }

    /// netip.ParseAddr accepting only IPv6 (with an optional zone): what an
    /// IP literal in brackets must be.
    private static func ipv6Literal(_ a: [UInt8]) -> Bool {
        for c in a {
            switch c {
            case 0x2E: return false // IPv4, or nothing
            case 0x3A: return parseIPv6(a)
            case 0x25: return false
            default: continue
            }
        }
        return false
    }

    /// netip's parseIPv6: whether `input` is an IPv6 address, an embedded
    /// IPv4 and a zone allowed.
    private static func parseIPv6(_ input: [UInt8]) -> Bool {
        var s = input[...]
        if let pct = input.firstIndex(of: 0x25) {
            guard pct + 1 < input.count else { return false } // an empty zone
            s = input[..<pct]
        }
        var ellipsis = -1
        if s.count >= 2, s[s.startIndex] == 0x3A, s[s.startIndex + 1] == 0x3A {
            ellipsis = 0
            s = s.dropFirst(2)
            if s.isEmpty {
                return true
            }
        }
        var i = 0
        while i < 16 {
            var off = 0
            var acc: UInt32 = 0
            while off < s.count {
                let c = s[s.startIndex + off]
                guard isHexDigit(c) else { break }
                acc = acc << 4 + UInt32(hexValue(c))
                if off > 3 || acc > 0xFFFF {
                    return false
                }
                off += 1
            }
            if off == 0 {
                return false
            }
            if off < s.count, s[s.startIndex + off] == 0x2E {
                if ellipsis < 0, i != 12 {
                    return false
                }
                if i + 4 > 16 {
                    return false
                }
                guard ipv4Fields(Array(s)) else { return false }
                s = s[s.endIndex...]
                i += 4
                break
            }
            i += 2
            s = s.dropFirst(off)
            if s.isEmpty {
                break
            }
            guard s[s.startIndex] == 0x3A, s.count > 1 else { return false }
            s = s.dropFirst()
            if s[s.startIndex] == 0x3A {
                if ellipsis >= 0 {
                    return false
                }
                ellipsis = i
                s = s.dropFirst()
                if s.isEmpty {
                    break
                }
            }
        }
        if !s.isEmpty {
            return false
        }
        if i < 16 {
            return ellipsis >= 0
        }
        return ellipsis < 0
    }

    /// netip's parseIPv4Fields: four decimal fields 0–255 without leading
    /// zeros.
    private static func ipv4Fields(_ s: [UInt8]) -> Bool {
        var val = 0
        var pos = 0
        var digLen = 0
        for (i, c) in s.enumerated() {
            if c >= 0x30, c <= 0x39 {
                if digLen == 1, val == 0 {
                    return false
                }
                val = val * 10 + Int(c - 0x30)
                digLen += 1
                if val > 255 {
                    return false
                }
            } else if c == 0x2E {
                if i == 0 || i == s.count - 1 || s[i - 1] == 0x2E {
                    return false
                }
                if pos == 3 {
                    return false
                }
                pos += 1
                val = 0
                digLen = 0
            } else {
                return false
            }
        }
        return pos >= 3
    }
}
