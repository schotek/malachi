// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/assistant search.go and its texts in assistant.go: the
// search in the user's own words (the In App target only). What the user
// typed into the search field ("invoices from Jana in March") goes to the
// user's assistant, which answers with a query in Malachi Mail's search
// syntax (backend/internal/search/query.go, docs/api.md search.query); the
// application puts it into the field and searches as if it had been typed.
// It is a one-shot request (`AssistantRequest`, `SearchConversion`) of
// purpose search: the daemon builds the system prompt, the message of
// `SearchInput` and the schema of the answer (docs/api.md §4.18), no
// bridge, no tool, and the answer comes in the result event's
// structured_output, read with `parseSearchQuery`. Only the typed words go
// to the model, no mail.
//
// Byte for byte like the Go package; the texts at the end go through L10n.

import Foundation

extension Assistant {
    /// assistant.MaxSearchQueryBytes: the longest query `parseSearchQuery`
    /// returns, in bytes: the daemon's cap (`API.Limits`,
    /// docs/api.md search.query).
    public static let maxSearchQueryBytes = 1024

    /// assistant.ParseSearchQuery: the query of the search's answer, the
    /// result event's structured_output (`Event.structured`): the string
    /// member "query" of a JSON object (the last one when it is there
    /// twice) as one line, every run of white space and control characters
    /// one space, trimmed and cut to at most `maxSearchQueryBytes` bytes at
    /// a character boundary. nil (Go's false) when the answer is not a JSON
    /// object, has no "query", or its "query" is not a string or leaves
    /// nothing. The JSON is read as Go's encoding/json reads it
    /// (`GoJSON`).
    public static func parseSearchQuery(_ structured: Data) -> String? {
        let b = [UInt8](structured)
        let r = trimSpace(b, 0, b.count)
        guard !r.isEmpty, GoJSON.valid(b, r), let o = GoJSON.Object(b, r),
              let s = GoJSON.string(b, o.members["query"]) else { return nil }
        var out: [UInt8] = []
        var space = false
        for u in s.unicodeScalars {
            let c = u.value
            if isSpace(c) || isControl(c) {
                space = !out.isEmpty
                continue
            }
            if space {
                out.append(0x20)
                space = false
            }
            appendUTF8(c, to: &out)
        }
        if out.count > maxSearchQueryBytes {
            var cut = maxSearchQueryBytes
            while cut > 0, out[cut] & 0xC0 == 0x80 {
                cut -= 1
            }
            out.removeSubrange(cut...)
            while out.last == 0x20 {
                out.removeLast()
            }
        }
        return out.isEmpty ? nil : String(decoding: out, as: UTF8.self)
    }

    // MARK: Texts

    /// assistant.SearchStrings: the fixed texts of the search in the
    /// user's own words: the item of the search field's menu (also ⌥↩ in
    /// the field) and the field's placeholder while the words are
    /// converted. A failure is `searchFailedText`.
    public struct SearchStrings: Sendable, Equatable {
        public var ownWords: String
        public var converting: String

        public init(ownWords: String, converting: String) {
            self.ownWords = ownWords
            self.converting = converting
        }
    }

    /// assistant.SearchTexts: the fixed texts of the search in the user's
    /// own words, translated.
    public static func searchTexts() -> SearchStrings {
        SearchStrings(
            // TRANSLATORS: An item of the search field's menu: the assistant turns what was typed into a search.
            ownWords: L10n.T("Search in Your Own Words"),
            // TRANSLATORS: The search field's placeholder while the assistant turns the typed words into a search.
            converting: L10n.T("Converting the search…")
        )
    }

    /// assistant.SearchFailedText: the toast when the words could not be
    /// turned into a search (the words stay in the field). `reason` is
    /// technical (Claude Code not found or not signed in, the result's
    /// text, stderr, a timeout) and shown as data, as `stoppedText` shows
    /// it: its first non-empty line without control characters, at most
    /// 400 bytes; "unknown" when nothing is left.
    public static func searchFailedText(_ reason: String) -> String {
        // TRANSLATORS: %s is a technical reason.
        L10n.T("The search could not be converted: %s", firstLine(reason, limit: maxReason))
    }
}
