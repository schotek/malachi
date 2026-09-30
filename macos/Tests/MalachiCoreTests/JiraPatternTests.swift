// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// ui/internal/jira/settings_test.go TestPatternError: `Jira.patternError`
// reads a pattern as Go's regexp/syntax does. The table is the Go test's,
// where regexp.Compile itself answers; the cases after it are this
// reading's own.

/// settings_test.go `patternCases`: patterns and what regexp/syntax says
/// of them ("" for a valid one).
private let patternCases: [(pattern: String, reason: String)] = [
    (#"^Remote comment create date:.*$"#, ""),
    (#"abc"#, ""),
    (#"a|b"#, ""),
    (#"(a|b)*c+?"#, ""),
    (#"příliš žluťoučký"#, ""),
    (#"[a-z]+"#, ""),
    (#"[]a]"#, ""),
    (#"[^]a]"#, ""),
    (#"[a-]"#, ""),
    (#"[-a]"#, ""),
    (#"[a\-z]"#, ""),
    (#"[á-ž]"#, ""),
    (#"[ž-á]"#, "invalid character class range"),
    (#"[z-a]"#, "invalid character class range"),
    (#"[a"#, "missing closing ]"),
    (#"["#, "missing closing ]"),
    (#"[]"#, "missing closing ]"),
    (#"[^]"#, "missing closing ]"),
    (#"[a-"#, "missing closing ]"),
    (#"[a\"#, "trailing backslash at end of expression"),
    (#"[a-\d]"#, "invalid escape sequence"),
    (#"[\d-z]"#, ""),
    (#"[\b]"#, "invalid escape sequence"),
    (#"[\x41-\x5a]"#, ""),
    (#"[a-z&&[^b]]"#, ""),
    (#"[[:alpha:]]"#, ""),
    (#"[[:^alpha:]]"#, ""),
    (#"[[:word:][:xdigit:]]"#, ""),
    (#"[[:foo:]]"#, "invalid character class range"),
    (#"[[:alpha:]"#, "missing closing ]"),
    (#"[[=a=]]"#, ""),
    (#"\d+\s\w\D\S\W"#, ""),
    (#"\pL+"#, ""),
    (#"\PL"#, ""),
    (#"\p{Greek}"#, ""),
    (#"\p{^Greek}"#, ""),
    (#"\p{Lu}"#, ""),
    (#"[\pL\d]"#, ""),
    (#"\p{Foo}"#, "invalid character class range"),
    (#"[\p{Foo}]"#, "invalid character class range"),
    (#"\p{L"#, "invalid character class range"),
    (#"\pX"#, "invalid character class range"),
    (#"(?i)abc"#, ""),
    (#"(?i:abc)"#, ""),
    (#"(?i-s:abc)"#, ""),
    (#"(?U)a*"#, ""),
    (#"(?m)^a$"#, ""),
    (#"(?s)."#, ""),
    (#"(?i)(?-i)"#, ""),
    (#"(?P<name>a)"#, ""),
    (#"(?<name>a)"#, ""),
    (#"(?P<na me>a)"#, "invalid named capture"),
    (#"(?P<>a)"#, "invalid named capture"),
    (#"(?P<n"#, "invalid named capture"),
    (#"(?<=a)"#, "invalid named capture"),
    (#"(?<!a)"#, "invalid named capture"),
    (#"(?=a)"#, "invalid or unsupported Perl syntax"),
    (#"(?!a)"#, "invalid or unsupported Perl syntax"),
    (#"(?>a)"#, "invalid or unsupported Perl syntax"),
    (#"(?#c)"#, "invalid or unsupported Perl syntax"),
    (#"(?'n'a)"#, "invalid or unsupported Perl syntax"),
    (#"(?x)a"#, "invalid or unsupported Perl syntax"),
    (#"(?i"#, "invalid or unsupported Perl syntax"),
    (#"(?"#, "invalid or unsupported Perl syntax"),
    (#"(?-)"#, "invalid or unsupported Perl syntax"),
    (#"(?i-)"#, "invalid or unsupported Perl syntax"),
    (#"(?--i)"#, "invalid or unsupported Perl syntax"),
    (#"("#, "missing closing )"),
    (#"(a"#, "missing closing )"),
    (#"((a)"#, "missing closing )"),
    (#")"#, "unexpected )"),
    (#"a)"#, "unexpected )"),
    (#"(a))"#, "unexpected )"),
    (#"()"#, ""),
    (#"(?:)"#, ""),
    (#"(a|b|)"#, ""),
    (#"a||b"#, ""),
    (#"|"#, ""),
    (#"*"#, "missing argument to repetition operator"),
    (#"+a"#, "missing argument to repetition operator"),
    (#"?"#, "missing argument to repetition operator"),
    (#"{2}"#, "missing argument to repetition operator"),
    (#"|*"#, "missing argument to repetition operator"),
    (#"a|*"#, "missing argument to repetition operator"),
    (#"(*)"#, "missing argument to repetition operator"),
    (#"(?i)*"#, "missing argument to repetition operator"),
    (#"(|a)*"#, ""),
    (#"(?:)*"#, ""),
    (#"^*"#, ""),
    (#"$*"#, ""),
    (#"\b*"#, ""),
    (#"a**"#, "invalid nested repetition operator"),
    (#"a*+"#, "invalid nested repetition operator"),
    (#"a++"#, "invalid nested repetition operator"),
    (#"a???"#, "invalid nested repetition operator"),
    (#"a{2}{3}"#, "invalid nested repetition operator"),
    (#"a{2}*"#, "invalid nested repetition operator"),
    (#"a{2}+"#, "invalid nested repetition operator"),
    (#"a*?"#, ""),
    (#"a??"#, ""),
    (#"a{2,3}?"#, ""),
    (#"a{2}"#, ""),
    (#"a{2,}"#, ""),
    (#"a{2,3}"#, ""),
    (#"a{0}"#, ""),
    (#"a{01}"#, ""),
    (#"a{,3}"#, ""),
    (#"a{}"#, ""),
    (#"a{2"#, ""),
    (#"x{2}{"#, ""),
    (#"a{1000}"#, ""),
    (#"a{1001}"#, "invalid repeat count"),
    (#"a{3,2}"#, "invalid repeat count"),
    (#"a{100000000000}"#, "invalid repeat count"),
    (#"(a{2}){3}"#, ""),
    (#"(a{100}){10}"#, ""),
    (#"(a{100}){11}"#, "invalid repeat count"),
    (#"((a{10}){10}){10}"#, ""),
    (#"((a{10}){10}){11}"#, "invalid repeat count"),
    (#"(a{1000}){0}"#, ""),
    (#"(a{1000}b{1000}c{1000}d{1000}){1000}"#, "expression too large"),
    (#"\"#, "trailing backslash at end of expression"),
    (#"a\"#, "trailing backslash at end of expression"),
    (#"\."#, ""),
    (#"\_"#, ""),
    (#"\-"#, ""),
    (#"\q"#, "invalid escape sequence"),
    (#"\e"#, "invalid escape sequence"),
    (#"\h"#, "invalid escape sequence"),
    (#"\1"#, "invalid escape sequence"),
    (#"\8"#, "invalid escape sequence"),
    (#"\12"#, ""),
    (#"\0"#, ""),
    (#"\07"#, ""),
    (#"\x41"#, ""),
    (#"\x4"#, "invalid escape sequence"),
    (#"\x"#, "invalid escape sequence"),
    (#"\xg1"#, "invalid escape sequence"),
    (#"\x{41}"#, ""),
    (#"\x{10FFFF}"#, ""),
    (#"\x{110000}"#, "invalid escape sequence"),
    (#"\x{}"#, "invalid escape sequence"),
    (#"\x{4g}"#, "invalid escape sequence"),
    (#"\a\f\n\r\t\v"#, ""),
    (#"\A\z"#, ""),
    (#"\b\B"#, ""),
    (#"\Z"#, "invalid escape sequence"),
    (#"\C"#, "invalid escape sequence"),
    (#"\G"#, "invalid escape sequence"),
    (#"\cA"#, "invalid escape sequence"),
    (#"\k<n>"#, "invalid escape sequence"),
    (#"\N{x}"#, "invalid escape sequence"),
    (#"\E"#, "invalid escape sequence"),
    (#"\Q.*\E"#, ""),
    (#"\Q.*"#, ""),
    (#"\Qa\Eb*"#, ""),
    (#"\Q(\E)"#, "unexpected )"),
    (#"[\Q]\E]"#, "invalid escape sequence"),
]

struct JiraPatternTests {
    @Test func patternError() {
        #expect(patternCases.count == 158)
        for c in patternCases {
            #expect(Jira.patternError(c.pattern) == c.reason, "patternError(\(c.pattern.debugDescription))")
        }
        #expect(Jira.patternError(Jira.suggestedMetadataFilter) == "", "the suggested pattern")
        // A pattern as long as an entry may be, of groups nested as deep
        // as that allows.
        let half = API.Limits.maxJiraPatternBytes / 2
        let deep = String(repeating: "(", count: half) + String(repeating: ")", count: half)
        #expect(Jira.patternError(deep) == "", "deep nesting")
        #expect(Jira.patternError(String(deep.dropFirst())) == "unexpected )", "deep nesting, one too many")
    }

    /// The names of Unicode classes are looked up without regard to case,
    /// spaces, hyphens and underscores (parse.go `canonicalName`), the
    /// scripts of several words too, which Go 1.26 misses.
    @Test func unicodeClassNames() {
        for name in ["L", "l", "Lu", "LC", "Letter", "letter", "Greek", "greek", " greek ", "Any", "ASCII", "Assigned",
                     "Old_Italic", "olditalic", "Old-Italic", "SignWriting", "^Cyrillic", "Uppercase_Letter", "punct"] {
            #expect(Jira.patternError(#"\p{"# + name + "}") == "", "\\p{\(name)}")
            #expect(Jira.patternError(#"[\P{"# + name + "}]") == "", "[\\P{\(name)}]")
        }
        for name in ["", "^", "Foo", "Gree", "Greeks", "L u x", "Ž"] {
            #expect(Jira.patternError(#"\p{"# + name + "}") == "invalid character class range", "\\p{\(name)}")
        }
        #expect(Jira.patternError(#"\p"#) == "invalid character class range")
        #expect(Jira.patternError(#"\pž"#) == "invalid character class range")
        #expect(Jira.patternError(#"\p{Greek"#) == "invalid character class range")
    }

    /// Hostile input: long, deep and repetitive patterns are answered, not
    /// followed into the ground.
    @Test func hostilePatterns() {
        let open = String(repeating: "(", count: 100_000)
        #expect(Jira.patternError(open) == "missing closing )")
        let nested = open + String(repeating: ")", count: 100_000)
        #expect(Jira.patternError(nested) == "expression nests too deeply")
        #expect(Jira.patternError(String(repeating: "a", count: 200_000)) == "")
        #expect(Jira.patternError(String(repeating: "a*", count: 50_000)) == "")
        #expect(Jira.patternError(String(repeating: "[", count: 50_000)) == "missing closing ]")
        #expect(Jira.patternError(String(repeating: "a|", count: 50_000)) == "")
        #expect(Jira.patternError(String(repeating: #"\"#, count: 50_001)) == "trailing backslash at end of expression")
        #expect(Jira.patternError("(((a{10}){10}){10}){10}") == "invalid repeat count")
        #expect(Jira.patternError("\u{0}[\u{0}-\u{1F}]") == "", "control characters are characters")
        #expect(Jira.patternError("😀+[😀-😂]") == "")
        #expect(Jira.patternError("[😂-😀]") == "invalid character class range")
    }
}
