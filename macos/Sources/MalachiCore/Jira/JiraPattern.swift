// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/jira/settings.go `PatternError`: whether a pattern of the
// "Hidden Lines" list is a regular expression of the daemon, and if not,
// why. The daemon's expressions are RE2 (Go's regexp), and the Go client
// simply asks regexp.Compile. Nothing on this side speaks RE2
// (NSRegularExpression is ICU: it takes look-around and back-references,
// which RE2 refuses, and refuses some of what RE2 takes), so this file
// follows the parser of Go's regexp/syntax (parse.go of Go 1.26, flags
// syntax.Perl) as far as it decides whether a pattern is valid, and
// answers with the same reasons in the same words. Nothing is matched
// here; the pattern is only read.
//
// The daemon checks every pattern again when the account is saved, so
// this is immediate feedback, as the other checks of the page. Where this
// reading is more lenient than regexp/syntax, the daemon's answer is what
// the user gets on Save:
//
//   - A Unicode class is looked up by its name without regard to case,
//     spaces, hyphens and underscores, in the tables of Unicode 15 with
//     the long names of the categories ("\p{Letter}"). Older versions of
//     Go know only the exact short names, and Go 1.26 misses the scripts
//     of several words ("\p{Old_Italic}").
//   - regexp/syntax refuses an expression that is too large or nests too
//     deep by what it happened to allocate while parsing; here the
//     expression itself is measured. For a pattern as long as an entry may
//     be (512 bytes) both agree on whether it is valid; for a pattern with
//     repetitions out of bounds they may name different reasons of the two
//     ("expression too large", "invalid repeat count").

import Foundation

extension Jira {
    /// jira.PatternError: why `pattern` is not a regular expression of the
    /// daemon (RE2), the reason in the words of Go's regexp/syntax
    /// ("missing closing )"), technical English; "" for a valid pattern.
    public static func patternError(_ pattern: String) -> String {
        var parser = PatternParser(pattern)
        return parser.check()?.rawValue ?? ""
    }
}

/// syntax.ErrorCode: why a pattern was refused, in the words of
/// regexp/syntax.
enum PatternProblem: String, Error, Sendable, Equatable {
    case internalError = "regexp/syntax: internal error"
    case invalidCharRange = "invalid character class range"
    case invalidEscape = "invalid escape sequence"
    case invalidNamedCapture = "invalid named capture"
    case invalidPerlOp = "invalid or unsupported Perl syntax"
    case invalidRepeatOp = "invalid nested repetition operator"
    case invalidRepeatSize = "invalid repeat count"
    case missingBracket = "missing closing ]"
    case missingParen = "missing closing )"
    case missingRepeatArgument = "missing argument to repetition operator"
    case trailingBackslash = "trailing backslash at end of expression"
    case unexpectedParen = "unexpected )"
    case nestingDepth = "expression nests too deeply"
    case large = "expression too large"
}

/// What the parser keeps of a parsed piece: enough to tell what a
/// repetition repeats, how large the compiled expression would be and how
/// deep it nests.
private final class PatternNode {
    enum Op {
        /// One character: a literal, a class, "." (parse.go `isCharClass`).
        case single
        /// An anchor, a word boundary, the empty match.
        case other
        case capture, star, plus, quest, `repeat`, concat, alternate
        /// The pseudo-operators of the stack: "(" and "|".
        case leftParen, verticalBar
    }

    let op: Op
    let subs: [PatternNode]
    /// Of `repeat`: the bounds, `max` -1 for none.
    let min: Int
    let max: Int
    /// Of `leftParen`: the group captures.
    let capturing: Bool
    /// parse.go `calcSize`, `calcHeight`.
    let size: Int
    let height: Int

    init(_ op: Op, subs: [PatternNode] = [], min: Int = 0, max: Int = 0, capturing: Bool = false) {
        self.op = op
        self.subs = subs
        self.min = min
        self.max = max
        self.capturing = capturing
        let sum = subs.reduce(0) { $0 + $1.size }
        var size: Int
        switch op {
        case .capture, .star:
            size = 2 + sum
        case .plus, .quest:
            size = 1 + sum
        case .concat:
            size = sum
        case .alternate:
            size = sum + Swift.max(subs.count - 1, 0)
        case .repeat:
            if max == -1 {
                size = min == 0 ? 2 + sum : 1 + min * sum
            } else {
                size = max * sum + (max - min)
            }
        default:
            size = 1
        }
        self.size = Swift.max(1, size)
        height = 1 + (subs.map(\.height).max() ?? 0)
    }

    var isPseudo: Bool { op == .leftParen || op == .verticalBar }
}

/// parse.go `parser`, reading the pattern's UTF-8 as Go does.
private struct PatternParser {
    /// parse.go `maxSize`, `maxHeight` and the most a repetition repeats.
    private static let maxSize = (128 << 20) / 40
    private static let maxHeight = 1000
    private static let maxRepeat = 1000

    private let s: [UInt8]
    /// Where the rest of the pattern starts (Go's `t`).
    private var i = 0
    private var stack: [PatternNode] = []

    init(_ pattern: String) {
        s = Array(pattern.utf8)
    }

    /// syntax.Parse, for its error alone.
    mutating func check() -> PatternProblem? {
        do {
            try parse()
            return nil
        } catch let problem as PatternProblem {
            return problem
        } catch {
            return .internalError
        }
    }

    private mutating func parse() throws {
        var lastRepeat = false
        while i < s.count {
            var repeated = false
            switch s[i] {
            case UInt8(ascii: "("):
                if i + 1 < s.count, s[i + 1] == UInt8(ascii: "?") {
                    // Flag changes and non-capturing groups.
                    try parsePerlFlags()
                } else {
                    try push(PatternNode(.leftParen, capturing: true))
                    i += 1
                }
            case UInt8(ascii: "|"):
                try parseVerticalBar()
                i += 1
            case UInt8(ascii: ")"):
                try parseRightParen()
                i += 1
            case UInt8(ascii: "^"), UInt8(ascii: "$"):
                try push(PatternNode(.other))
                i += 1
            case UInt8(ascii: "."):
                try push(PatternNode(.single))
                i += 1
            case UInt8(ascii: "["):
                try parseClass()
            case UInt8(ascii: "*"), UInt8(ascii: "+"), UInt8(ascii: "?"):
                let op: PatternNode.Op
                switch s[i] {
                case UInt8(ascii: "*"): op = .star
                case UInt8(ascii: "+"): op = .plus
                default: op = .quest
                }
                i += 1
                try repeatTop(op, min: 0, max: 0, lastRepeat: lastRepeat)
                repeated = true
            case UInt8(ascii: "{"):
                guard let r = parseRepeat(at: i) else {
                    // A repeat that cannot be parsed: "{" is a literal.
                    try push(PatternNode(.single))
                    i += 1
                    break
                }
                if r.min < 0 || r.min > Self.maxRepeat || r.max > Self.maxRepeat || (r.max >= 0 && r.min > r.max) {
                    throw PatternProblem.invalidRepeatSize
                }
                i = r.next
                try repeatTop(.repeat, min: r.min, max: r.max, lastRepeat: lastRepeat)
                repeated = true
            case UInt8(ascii: "\\"):
                try parseBackslash()
            default:
                i += rune(at: i).size
                try push(PatternNode(.single))
            }
            lastRepeat = repeated
        }

        try concat()
        if swapVerticalBar() {
            stack.removeLast()
        }
        try alternate()
        if stack.count != 1 {
            throw PatternProblem.missingParen
        }
    }

    // MARK: Runes

    /// utf8.DecodeRuneInString at `at`; past the end the replacement
    /// character of size 0, as Go decodes the empty string. The bytes are
    /// a String's, so they are valid UTF-8.
    private func rune(at: Int) -> (value: UInt32, size: Int) {
        guard at < s.count else { return (0xFFFD, 0) }
        let b = s[at]
        let size: Int
        var value: UInt32
        switch b {
        case 0..<0x80:
            return (UInt32(b), 1)
        case 0xC0..<0xE0:
            size = 2
            value = UInt32(b & 0x1F)
        case 0xE0..<0xF0:
            size = 3
            value = UInt32(b & 0x0F)
        case 0xF0..<0xF8:
            size = 4
            value = UInt32(b & 0x07)
        default:
            return (0xFFFD, 1)
        }
        guard at + size <= s.count else { return (0xFFFD, 1) }
        for k in 1..<size {
            value = value << 6 | UInt32(s[at + k] & 0x3F)
        }
        return (value, size)
    }

    /// strings.Index of `needle` in the pattern from `from` on.
    private func index(of needle: [UInt8], from: Int) -> Int? {
        guard !needle.isEmpty, from <= s.count - needle.count else { return nil }
        for k in from...(s.count - needle.count) where Array(s[k..<(k + needle.count)]) == needle {
            return k
        }
        return nil
    }

    private static func isAlnum(_ c: UInt32) -> Bool {
        (0x30...0x39).contains(c) || (0x41...0x5A).contains(c) || (0x61...0x7A).contains(c)
    }

    private static func isOctal(_ b: UInt8) -> Bool {
        b >= UInt8(ascii: "0") && b <= UInt8(ascii: "7")
    }

    private static func isDigit(_ b: UInt8) -> Bool {
        b >= UInt8(ascii: "0") && b <= UInt8(ascii: "9")
    }

    /// parse.go `unhex`: -1 for a character that is no hexadecimal digit.
    private static func unhex(_ c: UInt32) -> Int {
        switch c {
        case 0x30...0x39: return Int(c) - 0x30
        case 0x61...0x66: return Int(c) - 0x61 + 10
        case 0x41...0x46: return Int(c) - 0x41 + 10
        default: return -1
        }
    }

    // MARK: The stack

    /// parse.go `push` with `checkLimits`.
    private mutating func push(_ node: PatternNode) throws {
        try checkLimits(node)
        stack.append(node)
    }

    private func checkLimits(_ node: PatternNode) throws {
        if node.size > Self.maxSize {
            throw PatternProblem.large
        }
        if node.height > Self.maxHeight {
            throw PatternProblem.nestingDepth
        }
    }

    /// parse.go `repeat`: the top of the stack, repeated. The "?" of a
    /// non-greedy operator goes with it.
    private mutating func repeatTop(_ op: PatternNode.Op, min: Int, max: Int, lastRepeat: Bool) throws {
        if i < s.count, s[i] == UInt8(ascii: "?") {
            i += 1
        }
        if lastRepeat {
            // Perl does not stack repetition operators: a** is an error,
            // and a++ means something RE2 does not have.
            throw PatternProblem.invalidRepeatOp
        }
        guard let sub = stack.last, !sub.isPseudo else {
            throw PatternProblem.missingRepeatArgument
        }
        let node = PatternNode(op, subs: [sub], min: min, max: max)
        stack[stack.count - 1] = node
        try checkLimits(node)
        if op == .repeat, min >= 2 || max >= 2, !Self.repeatIsValid(node, Self.maxRepeat) {
            throw PatternProblem.invalidRepeatSize
        }
    }

    /// parse.go `repeatIsValid`: the repetition with those inside it makes
    /// at most `n` copies of the innermost piece.
    private static func repeatIsValid(_ node: PatternNode, _ n: Int) -> Bool {
        var n = n
        if node.op == .repeat {
            var m = node.max
            if m == 0 {
                return true
            }
            if m < 0 {
                m = node.min
            }
            if m > n {
                return false
            }
            if m > 0 {
                n /= m
            }
        }
        return node.subs.allSatisfy { repeatIsValid($0, n) }
    }

    /// The pieces above the topmost "(" or "|", taken off the stack.
    private mutating func popPieces() -> [PatternNode] {
        var k = stack.count
        while k > 0, !stack[k - 1].isPseudo {
            k -= 1
        }
        let subs = Array(stack[k...])
        stack.removeSubrange(k...)
        return subs
    }

    /// parse.go `concat`.
    private mutating func concat() throws {
        let subs = popPieces()
        try push(subs.isEmpty ? PatternNode(.other) : Self.collapse(subs, .concat))
    }

    /// parse.go `alternate`.
    private mutating func alternate() throws {
        let subs = popPieces()
        try push(subs.isEmpty ? PatternNode(.other) : Self.collapse(subs, .alternate))
    }

    /// parse.go `collapse`: never a concatenation of a concatenation, or
    /// an alternation of an alternation.
    private static func collapse(_ subs: [PatternNode], _ op: PatternNode.Op) -> PatternNode {
        if subs.count == 1 {
            return subs[0]
        }
        var flat: [PatternNode] = []
        for sub in subs {
            if sub.op == op {
                flat.append(contentsOf: sub.subs)
            } else {
                flat.append(sub)
            }
        }
        return PatternNode(op, subs: flat)
    }

    /// parse.go `parseVerticalBar`.
    private mutating func parseVerticalBar() throws {
        try concat()
        if !swapVerticalBar() {
            try push(PatternNode(.verticalBar))
        }
    }

    /// parse.go `swapVerticalBar`: the piece on top goes below the "|"
    /// under it; two single characters around a "|" become one class.
    private mutating func swapVerticalBar() -> Bool {
        let n = stack.count
        if n >= 3, stack[n - 2].op == .verticalBar, stack[n - 1].op == .single, stack[n - 3].op == .single {
            stack.removeLast()
            return true
        }
        if n >= 2, stack[n - 2].op == .verticalBar {
            stack.swapAt(n - 2, n - 1)
            return true
        }
        return false
    }

    /// parse.go `parseRightParen`.
    private mutating func parseRightParen() throws {
        try concat()
        if swapVerticalBar() {
            stack.removeLast()
        }
        try alternate()
        let n = stack.count
        guard n >= 2 else { throw PatternProblem.unexpectedParen }
        let inner = stack[n - 1]
        let paren = stack[n - 2]
        stack.removeLast(2)
        guard paren.op == .leftParen else { throw PatternProblem.unexpectedParen }
        try push(paren.capturing ? PatternNode(.capture, subs: [inner]) : inner)
    }

    // MARK: Repetitions

    /// parse.go `parseInt`: a decimal number without leading zeros; -1
    /// for one that is too big.
    private func parseInt(at: Int) -> (value: Int, next: Int)? {
        guard at < s.count, Self.isDigit(s[at]) else { return nil }
        if at + 1 < s.count, s[at] == UInt8(ascii: "0"), Self.isDigit(s[at + 1]) {
            return nil
        }
        var k = at
        var n = 0
        while k < s.count, Self.isDigit(s[k]) {
            if n >= 0 {
                n = n >= 100_000_000 ? -1 : n * 10 + Int(s[k] - UInt8(ascii: "0"))
            }
            k += 1
        }
        return (n, k)
    }

    /// parse.go `parseRepeat`: {min}, {min,} (max -1) or {min,max} at
    /// `at`; nil when the text is not of that form. A number too big makes
    /// min -1.
    private func parseRepeat(at: Int) -> (min: Int, max: Int, next: Int)? {
        guard at < s.count, s[at] == UInt8(ascii: "{"), let first = parseInt(at: at + 1) else { return nil }
        var min = first.value
        var max = min
        var k = first.next
        guard k < s.count else { return nil }
        if s[k] == UInt8(ascii: ",") {
            k += 1
            guard k < s.count else { return nil }
            if s[k] == UInt8(ascii: "}") {
                max = -1
            } else {
                guard let second = parseInt(at: k) else { return nil }
                max = second.value
                k = second.next
                if max < 0 {
                    min = -1
                }
            }
        }
        guard k < s.count, s[k] == UInt8(ascii: "}") else { return nil }
        return (min, max, k + 1)
    }

    // MARK: Groups

    /// parse.go `parsePerlFlags`: a named group, a non-capturing group or
    /// flags, at "(?".
    private mutating func parsePerlFlags() throws {
        let t = i
        let length = s.count - t
        let startsWithP = length > 4 && s[t + 2] == UInt8(ascii: "P") && s[t + 3] == UInt8(ascii: "<")
        let startsWithName = length > 3 && s[t + 2] == UInt8(ascii: "<")
        if startsWithP || startsWithName {
            let nameStart = t + (startsWithName ? 3 : 4)
            guard let end = index(of: [UInt8(ascii: ">")], from: t) else {
                throw PatternProblem.invalidNamedCapture
            }
            guard end > nameStart, s[nameStart..<end].allSatisfy({ $0 == UInt8(ascii: "_") || Self.isAlnum(UInt32($0)) }) else {
                throw PatternProblem.invalidNamedCapture
            }
            try push(PatternNode(.leftParen, capturing: true))
            i = end + 1
            return
        }

        var k = t + 2
        var negated = false
        var sawFlag = false
        loop: while k < s.count {
            let c = rune(at: k)
            k += c.size
            switch c.value {
            case 0x69, 0x6D, 0x73, 0x55: // i, m, s, U
                sawFlag = true
            case 0x2D: // -
                if negated {
                    break loop
                }
                negated = true
                sawFlag = false
            case 0x3A, 0x29: // : and )
                if negated, !sawFlag {
                    break loop
                }
                if c.value == 0x3A {
                    try push(PatternNode(.leftParen))
                }
                i = k
                return
            default:
                break loop
            }
        }
        throw PatternProblem.invalidPerlOp
    }

    // MARK: Escapes

    /// The "\" of the main loop: an anchor, a quoted text, a class or an
    /// escaped character.
    private mutating func parseBackslash() throws {
        if i + 1 < s.count {
            switch s[i + 1] {
            case UInt8(ascii: "A"), UInt8(ascii: "b"), UInt8(ascii: "B"), UInt8(ascii: "z"):
                try push(PatternNode(.other))
                i += 2
                return
            case UInt8(ascii: "C"):
                // Any byte: not supported.
                throw PatternProblem.invalidEscape
            case UInt8(ascii: "Q"):
                // \Q ... \E: always literals.
                let start = i + 2
                let end = index(of: [UInt8(ascii: "\\"), UInt8(ascii: "E")], from: start)
                var k = start
                while k < (end ?? s.count) {
                    k += Swift.max(rune(at: k).size, 1)
                    try push(PatternNode(.single))
                }
                i = end.map { $0 + 2 } ?? s.count
                return
            default:
                break
            }
        }
        if let next = try parseUnicodeClass(at: i) {
            i = next
            try push(PatternNode(.single))
            return
        }
        if let next = parsePerlClass(at: i) {
            i = next
            try push(PatternNode(.single))
            return
        }
        i = try parseEscape(at: i).next
        try push(PatternNode(.single))
    }

    /// parse.go `parseEscape`: the character an escape at `at` stands for.
    private func parseEscape(at: Int) throws -> (value: UInt32, next: Int) {
        var t = at + 1
        guard t < s.count else { throw PatternProblem.trailingBackslash }
        let c = rune(at: t)
        t += c.size
        switch c.value {
        case 0x31...0x37: // 1 to 7
            // A single digit is a back-reference: not supported.
            guard t < s.count, Self.isOctal(s[t]) else { break }
            return octal(first: c.value, at: t)
        case 0x30:
            return octal(first: c.value, at: t)
        case 0x78: // x
            guard t < s.count else { break }
            var d = rune(at: t)
            t += d.size
            if d.value == 0x7B { // {
                // Hexadecimal digits in braces, at least one.
                var digits = 0
                var r = 0
                while true {
                    guard t < s.count else { throw PatternProblem.invalidEscape }
                    d = rune(at: t)
                    t += d.size
                    if d.value == 0x7D { // }
                        break
                    }
                    let v = Self.unhex(d.value)
                    guard v >= 0 else { throw PatternProblem.invalidEscape }
                    r = r * 16 + v
                    guard r <= 0x10FFFF else { throw PatternProblem.invalidEscape }
                    digits += 1
                }
                guard digits > 0 else { throw PatternProblem.invalidEscape }
                return (UInt32(r), t)
            }
            // Two hexadecimal digits.
            let x = Self.unhex(d.value)
            let e = rune(at: t)
            t += e.size
            let y = Self.unhex(e.value)
            guard x >= 0, y >= 0 else { break }
            return (UInt32(x * 16 + y), t)
        case 0x61: return (0x07, t) // \a
        case 0x66: return (0x0C, t) // \f
        case 0x6E: return (0x0A, t) // \n
        case 0x72: return (0x0D, t) // \r
        case 0x74: return (0x09, t) // \t
        case 0x76: return (0x0B, t) // \v
        default:
            if c.value < 0x80, !Self.isAlnum(c.value) {
                // An escaped character that is no letter or digit is itself.
                return (c.value, t)
            }
        }
        throw PatternProblem.invalidEscape
    }

    /// Up to three octal digits, the first one read already.
    private func octal(first: UInt32, at: Int) -> (value: UInt32, next: Int) {
        var r = first - 0x30
        var t = at
        var digits = 1
        while digits < 3, t < s.count, Self.isOctal(s[t]) {
            r = r * 8 + UInt32(s[t] - UInt8(ascii: "0"))
            t += 1
            digits += 1
        }
        return (r, t)
    }

    // MARK: Classes

    /// parse.go `parsePerlClassEscape`: \d, \s, \w and their negations at
    /// `at`; nil when there is none.
    private func parsePerlClass(at: Int) -> Int? {
        guard at + 1 < s.count, s[at] == UInt8(ascii: "\\") else { return nil }
        switch s[at + 1] {
        case UInt8(ascii: "d"), UInt8(ascii: "D"), UInt8(ascii: "s"), UInt8(ascii: "S"), UInt8(ascii: "w"), UInt8(ascii: "W"):
            return at + 2
        default:
            return nil
        }
    }

    /// parse.go `parseUnicodeClass`: \pL, \p{Greek} and their negations
    /// at `at`; nil when there is none.
    private func parseUnicodeClass(at: Int) throws -> Int? {
        guard at + 1 < s.count, s[at] == UInt8(ascii: "\\"),
              s[at + 1] == UInt8(ascii: "p") || s[at + 1] == UInt8(ascii: "P") else { return nil }
        var t = at + 2
        let c = rune(at: t)
        t += c.size
        var name: ArraySlice<UInt8>
        if c.value != 0x7B { // {
            // A name of one letter.
            name = s[(at + 2)..<t]
        } else {
            guard let end = index(of: [UInt8(ascii: "}")], from: at) else {
                throw PatternProblem.invalidCharRange
            }
            name = s[(at + 3)..<end]
            t = end + 1
        }
        // \p{^Han} is \P{Han}.
        if name.first == UInt8(ascii: "^") {
            name = name.dropFirst()
        }
        guard Self.unicodeClasses.contains(Self.canonicalName(name)) else {
            throw PatternProblem.invalidCharRange
        }
        return t
    }

    /// parse.go `canonicalName`: the name a Unicode class is looked up by:
    /// an upper-case letter first, then lower case, without underscores,
    /// hyphens and spaces.
    private static func canonicalName(_ name: ArraySlice<UInt8>) -> String {
        var out: [UInt8] = []
        var first = true
        for var c in name {
            if c == UInt8(ascii: "_") || c == UInt8(ascii: "-") || c == UInt8(ascii: " ") {
                continue
            }
            if first {
                if c >= UInt8(ascii: "a"), c <= UInt8(ascii: "z") {
                    c -= 0x20
                }
                first = false
            } else if c >= UInt8(ascii: "A"), c <= UInt8(ascii: "Z") {
                c += 0x20
            }
            out.append(c)
        }
        return String(decoding: out, as: UTF8.self)
    }

    /// parse.go `parseClass`: a class in brackets at the "[".
    private mutating func parseClass() throws {
        var t = i + 1
        if t < s.count, s[t] == UInt8(ascii: "^") {
            t += 1
        }
        // "]" is an ordinary character as the first one of the class.
        var first = true
        while t >= s.count || s[t] != UInt8(ascii: "]") || first {
            first = false
            // A POSIX class, [:alnum:].
            if s.count - t > 2, s[t] == UInt8(ascii: "["), s[t + 1] == UInt8(ascii: ":"),
               let end = index(of: [UInt8(ascii: ":"), UInt8(ascii: "]")], from: t + 2) {
                let name = String(decoding: s[t..<(end + 2)], as: UTF8.self)
                guard Self.posixClasses.contains(name) else {
                    throw PatternProblem.invalidCharRange
                }
                t = end + 2
                continue
            }
            if let next = try parseUnicodeClass(at: t) {
                t = next
                continue
            }
            if let next = parsePerlClass(at: t) {
                t = next
                continue
            }
            // A character or a range.
            let lo = try parseClassChar(at: t)
            t = lo.next
            // [a-] is "a" or "-".
            if s.count - t >= 2, s[t] == UInt8(ascii: "-"), s[t + 1] != UInt8(ascii: "]") {
                let hi = try parseClassChar(at: t + 1)
                t = hi.next
                if hi.value < lo.value {
                    throw PatternProblem.invalidCharRange
                }
            }
        }
        i = t + 1
        try push(PatternNode(.single))
    }

    /// parse.go `parseClassChar`: a character of a class at `at`.
    private func parseClassChar(at: Int) throws -> (value: UInt32, next: Int) {
        guard at < s.count else { throw PatternProblem.missingBracket }
        if s[at] == UInt8(ascii: "\\") {
            return try parseEscape(at: at)
        }
        let c = rune(at: at)
        return (c.value, at + c.size)
    }

    // MARK: Tables

    /// perl_groups.go `posixGroup`.
    private static let posixClasses: Set<String> = {
        let names = [
            "alnum", "alpha", "ascii", "blank", "cntrl", "digit", "graph", "lower", "print", "punct", "space", "upper",
            "word", "xdigit",
        ]
        return Set(names.flatMap { ["[:\($0):]", "[:^\($0):]"] })
    }()

    /// The names of parse.go `unicodeTable` in their canonical form: Any,
    /// Assigned and ASCII, and of Go's package unicode (Unicode 15.0) the
    /// categories, their long names and the scripts.
    private static let unicodeClasses: Set<String> = [
        "Adlam", "Ahom", "Anatolianhieroglyphs", "Any", "Arabic", "Armenian", "Ascii", "Assigned", "Avestan",
        "Balinese", "Bamum", "Bassavah", "Batak", "Bengali", "Bhaiksuki", "Bopomofo", "Brahmi", "Braille", "Buginese",
        "Buhid", "C", "Canadianaboriginal", "Carian", "Casedletter", "Caucasianalbanian", "Cc", "Cf", "Chakma",
        "Cham", "Cherokee", "Chorasmian", "Closepunctuation", "Cn", "Cntrl", "Co", "Combiningmark", "Common",
        "Connectorpunctuation", "Control", "Coptic", "Cs", "Cuneiform", "Currencysymbol", "Cypriot", "Cyprominoan",
        "Cyrillic", "Dashpunctuation", "Decimalnumber", "Deseret", "Devanagari", "Digit", "Divesakuru", "Dogra",
        "Duployan", "Egyptianhieroglyphs", "Elbasan", "Elymaic", "Enclosingmark", "Ethiopic", "Finalpunctuation",
        "Format", "Georgian", "Glagolitic", "Gothic", "Grantha", "Greek", "Gujarati", "Gunjalagondi", "Gurmukhi",
        "Han", "Hangul", "Hanifirohingya", "Hanunoo", "Hatran", "Hebrew", "Hiragana", "Imperialaramaic", "Inherited",
        "Initialpunctuation", "Inscriptionalpahlavi", "Inscriptionalparthian", "Javanese", "Kaithi", "Kannada",
        "Katakana", "Kawi", "Kayahli", "Kharoshthi", "Khitansmallscript", "Khmer", "Khojki", "Khudawadi", "L", "Lao",
        "Latin", "Lc", "Lepcha", "Letter", "Letternumber", "Limbu", "Lineara", "Linearb", "Lineseparator", "Lisu",
        "Ll", "Lm", "Lo", "Lowercaseletter", "Lt", "Lu", "Lycian", "Lydian", "M", "Mahajani", "Makasar", "Malayalam",
        "Mandaic", "Manichaean", "Marchen", "Mark", "Masaramgondi", "Mathsymbol", "Mc", "Me", "Medefaidrin",
        "Meeteimayek", "Mendekikakui", "Meroiticcursive", "Meroitichieroglyphs", "Miao", "Mn", "Modi",
        "Modifierletter", "Modifiersymbol", "Mongolian", "Mro", "Multani", "Myanmar", "N", "Nabataean", "Nagmundari",
        "Nandinagari", "Nd", "Newa", "Newtailue", "Nko", "Nl", "No", "Nonspacingmark", "Number", "Nushu",
        "Nyiakengpuachuehmong", "Ogham", "Olchiki", "Oldhungarian", "Olditalic", "Oldnortharabian", "Oldpermic",
        "Oldpersian", "Oldsogdian", "Oldsoutharabian", "Oldturkic", "Olduyghur", "Openpunctuation", "Oriya", "Osage",
        "Osmanya", "Other", "Otherletter", "Othernumber", "Otherpunctuation", "Othersymbol", "P", "Pahawhhmong",
        "Palmyrene", "Paragraphseparator", "Paucinhau", "Pc", "Pd", "Pe", "Pf", "Phagspa", "Phoenician", "Pi", "Po",
        "Privateuse", "Ps", "Psalterpahlavi", "Punct", "Punctuation", "Rejang", "Runic", "S", "Samaritan",
        "Saurashtra", "Sc", "Separator", "Sharada", "Shavian", "Siddham", "Signwriting", "Sinhala", "Sk", "Sm", "So",
        "Sogdian", "Sorasompeng", "Soyombo", "Spaceseparator", "Spacingmark", "Sundanese", "Surrogate", "Sylotinagri",
        "Symbol", "Syriac", "Tagalog", "Tagbanwa", "Taile", "Taitham", "Taiviet", "Takri", "Tamil", "Tangsa",
        "Tangut", "Telugu", "Thaana", "Thai", "Tibetan", "Tifinagh", "Tirhuta", "Titlecaseletter", "Toto", "Ugaritic",
        "Unassigned", "Uppercaseletter", "Vai", "Vithkuqi", "Wancho", "Warangciti", "Yezidi", "Yi", "Z",
        "Zanabazarsquare", "Zl", "Zp", "Zs",
    ]
}
