// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/assistant events.go, the In App target: what Claude Code
// writes on stdout with `--output-format stream-json` (one JSON object per
// line) as the few events the panel shows, and the draft a `create_draft`
// result names. The lines carry mail content and model output: they are
// parsed defensively, a field of an unexpected type reads as its zero
// value, unknown types are ignored and nothing is interpreted beyond the
// fields named below.
//
// The shapes (Claude Code 2.1.178): system/init (tools, mcp_servers
// [{name, status}], model, session_id, …), system/status and others,
// rate_limit_event, stream_event (event content_block_delta with delta
// text_delta {text}; thinking_delta and signature_delta ignored),
// assistant (message.content: thinking, text {text}, tool_use {id, name,
// input}; error when Claude Code wrote the message itself because the API
// refused the turn: authentication_failed, billing_error, rate_limit,
// invalid_request, server_error, unknown), user (message.content:
// tool_result {tool_use_id, is_error, content: a string or [{type: text,
// text}]}) and result (subtype success or error_*, is_error, result,
// structured_output, permission_denials [{tool_name}], total_cost_usd,
// usage).
//
// The JSON is read the way Go's encoding/json reads it into
// `map[string]json.RawMessage` (`GoJSON` below), not with
// JSONSerialization: the same validity (json.Valid, nesting up to 10 000),
// the last of duplicate keys wins, a member of another type reads as its
// zero value (a number is never a Bool, `true` never a number), bad UTF-8
// in a string becomes U+FFFD, and `structured_output` stays the raw bytes
// of the line. This file holds no translatable text.

import Foundation

extension Assistant {
    /// toolPrefix: the prefix of the bridge's tools in Claude Code (the MCP
    /// server is called "malachi" in the --mcp-config of `args`).
    static let bridgeToolPrefix = "mcp__malachi__"

    /// bridgeServer: the name of the bridge's MCP server in --mcp-config
    /// and in the init event's mcp_servers.
    static let bridgeServer = "malachi"

    /// authenticationFailed: the `failure` of a turn the API refused
    /// because Claude Code is not signed in, or its sign-in is no longer
    /// accepted.
    static let authenticationFailed = "authentication_failed"

    /// assistant.Event: one thing the panel reacts to; only the fields of
    /// its kind are set.
    public struct Event: Sendable, Equatable {
        /// assistant.EventKind.
        public enum Kind: Sendable, Equatable {
            /// EventOther: anything else (status, rate limits, a delta that
            /// is not text, a type this client does not know).
            case other
            /// EventInit: `system/init`, at the start of every turn.
            case systemInit
            /// EventTextDelta: a piece of the answer as it streams.
            case textDelta
            /// EventText: one whole text block of an `assistant` message,
            /// authoritative over the deltas that preceded it.
            case text
            /// EventToolUse: a tool is called.
            case toolUse
            /// EventToolResult: a tool answered.
            case toolResult
            /// EventResult: the turn is over.
            case result
            /// EventFailure: an `assistant` message Claude Code wrote
            /// itself: the API refused the turn. Its text is no answer; the
            /// result that follows repeats it.
            case failure
        }

        public var kind: Kind
        /// systemInit: whether the MCP server "malachi" reported
        /// "connected", and the names of the tools Claude Code offers, as
        /// reported.
        public var bridgeConnected = false
        public var tools: [String] = []
        /// textDelta and text: the text.
        public var text = ""
        /// toolUse: the tool without the `mcp__malachi__` prefix (another
        /// tool keeps its name) and the call's id; toolResult: the id of
        /// the call it answers.
        public var tool = ""
        public var toolUseID = ""
        /// toolResult: whether the tool failed, and its text blocks joined
        /// with "\n" (or its string content); result: the turn's result
        /// text, and for a failure without one its subtype
        /// ("error_max_turns", …).
        public var isError = false
        public var resultText = ""
        /// result: subtype "success" and not `is_error`; the tools the
        /// permission mode denied (prefix stripped), in order; the cost as
        /// Claude Code reports it (logged, never shown); `structured_output`
        /// as the raw JSON of the line, nil when absent or null.
        public var success = false
        public var denied: [String] = []
        public var costUSD: Double = 0
        public var structured: Data?
        /// failure: what Claude Code calls the failure, its message's
        /// `error` ("authentication_failed", "rate_limit", …).
        public var failure = ""

        public init(kind: Kind) {
            self.kind = kind
        }

        /// Event.NotSignedIn: whether the event is the failure of a turn
        /// for want of a sign-in the API accepts: Claude Code is signed
        /// out, or its sign-in has expired or was revoked (`claude auth
        /// status` may still say loggedIn then). The name is compared byte
        /// for byte.
        public var notSignedIn: Bool {
            kind == .failure && failure.utf8.elementsEqual(Assistant.authenticationFailed.utf8)
        }
    }

    /// Why a line is not an event (Go's errMalformed and errNotObject).
    public enum EventError: Error, Equatable, CustomStringConvertible {
        case malformed
        case notAnObject

        public var description: String {
            switch self {
            case .malformed: return "assistant: a stream-json line that is not JSON"
            case .notAnObject: return "assistant: a stream-json line that is not an object"
            }
        }
    }

    /// assistant.ParseEvents: one stdout line (without its newline): one
    /// event per text or tool_use block of an `assistant` message and per
    /// tool_result block of a `user` message, in order (thinking and other
    /// blocks yield nothing, so such a message may yield none), one `failure`
    /// alone for an `assistant` message with an error, one
    /// `systemInit` for system/init, one `textDelta` for a text delta, one
    /// `result` for a result, and one `other` for any other line. Throws
    /// when the line (without surrounding white space) is not JSON or not
    /// a JSON object.
    public static func parseEvents(_ line: Data) throws -> [Event] {
        let b = [UInt8](line)
        let trimmed = trimSpace(b, 0, b.count)
        guard GoJSON.valid(b, trimmed) else {
            throw EventError.malformed
        }
        guard b[trimmed.lowerBound] == UInt8(ascii: "{"), let o = GoJSON.Object(b, trimmed) else {
            throw EventError.notAnObject
        }
        switch o.str("type") {
        case "system":
            if o.str("subtype") == "init" {
                return [parseInit(o)]
            }
        case "stream_event":
            let ev = o.obj("event")
            let delta = ev?.obj("delta")
            if ev?.str("type") == "content_block_delta", delta?.str("type") == "text_delta" {
                var e = Event(kind: .textDelta)
                e.text = delta?.str("text") ?? ""
                return [e]
            }
        case "assistant":
            return parseAssistant(o)
        case "user":
            return parseUser(o)
        case "result":
            return [parseResult(o)]
        default:
            break
        }
        return [Event(kind: .other)]
    }

    private static func parseInit(_ o: GoJSON.Object) -> Event {
        var e = Event(kind: .systemInit)
        for t in o.array("tools") {
            if let s = GoJSON.string(o.b, t) {
                e.tools.append(s)
            }
        }
        for s in o.array("mcp_servers") {
            let srv = GoJSON.Object(o.b, s)
            if srv?.str("name") == bridgeServer, srv?.str("status") == "connected" {
                e.bridgeConnected = true
            }
        }
        return e
    }

    private static func parseAssistant(_ o: GoJSON.Object) -> [Event] {
        let failure = o.str("error")
        if !failure.isEmpty {
            var e = Event(kind: .failure)
            e.failure = failure
            return [e]
        }
        var out: [Event] = []
        for raw in o.obj("message")?.array("content") ?? [] {
            let block = GoJSON.Object(o.b, raw)
            switch block?.str("type") ?? "" {
            case "text":
                var e = Event(kind: .text)
                e.text = block?.str("text") ?? ""
                out.append(e)
            case "tool_use":
                var e = Event(kind: .toolUse)
                e.tool = stripBridgePrefix(block?.str("name") ?? "")
                e.toolUseID = block?.str("id") ?? ""
                out.append(e)
            default:
                continue // thinking, redacted thinking, anything newer
            }
        }
        return out
    }

    private static func parseUser(_ o: GoJSON.Object) -> [Event] {
        var out: [Event] = []
        for raw in o.obj("message")?.array("content") ?? [] {
            guard let block = GoJSON.Object(o.b, raw), block.str("type") == "tool_result" else { continue }
            var e = Event(kind: .toolResult)
            e.toolUseID = block.str("tool_use_id")
            e.isError = block.boolean("is_error")
            e.resultText = resultText(block, block.members["content"])
            out.append(e)
        }
        return out
    }

    /// A tool_result's content: a string, or the text blocks of an array
    /// joined with "\n" (images and other blocks left out; a text block
    /// whose text is not a string counts as "").
    private static func resultText(_ o: GoJSON.Object, _ content: Range<Int>?) -> String {
        if let s = GoJSON.string(o.b, content) {
            return s
        }
        var texts: [String] = []
        for raw in GoJSON.array(o.b, content) {
            let block = GoJSON.Object(o.b, raw)
            if block?.str("type") == "text" {
                texts.append(block?.str("text") ?? "")
            }
        }
        return texts.joined(separator: "\n")
    }

    private static func parseResult(_ o: GoJSON.Object) -> Event {
        let subtype = o.str("subtype")
        var e = Event(kind: .result)
        e.isError = o.boolean("is_error")
        e.resultText = o.str("result")
        e.costUSD = o.number("total_cost_usd")
        e.success = subtype == "success" && !e.isError
        if !e.success, e.resultText.isEmpty {
            e.resultText = subtype
        }
        for raw in o.array("permission_denials") {
            let name = GoJSON.Object(o.b, raw)?.str("tool_name") ?? ""
            if !name.isEmpty {
                e.denied.append(stripBridgePrefix(name))
            }
        }
        if let raw = o.members["structured_output"] {
            let r = trimSpace(o.b, raw.lowerBound, raw.upperBound)
            if !r.isEmpty, !o.b[r].elementsEqual("null".utf8) {
                e.structured = Data(o.b[r])
            }
        }
        return e
    }

    /// strings.TrimPrefix(name, toolPrefix), byte for byte.
    static func stripBridgePrefix(_ name: String) -> String {
        guard name.utf8.starts(with: bridgeToolPrefix.utf8) else { return name }
        return String(decoding: name.utf8.dropFirst(bridgeToolPrefix.utf8.count), as: UTF8.self)
    }

    // MARK: Drafts

    /// assistant.DraftRef: a draft the bridge stored for the panel.
    public struct DraftRef: Sendable, Equatable, Hashable {
        public var accountID: String
        public var draftID: String
        public var version: Int

        public init(accountID: String, draftID: String, version: Int) {
            self.accountID = accountID
            self.draftID = draftID
            self.version = version
        }
    }

    /// The parts of the head of a create_draft result,
    /// backend/cmd/malachi-mcp/tools_write.go:
    /// "draft <id> (version <n>) stored in account <acc>; it is NOT sent."
    private static let draftHead = Array("draft ".utf8)
    private static let draftVersion = Array(" (version ".utf8)
    private static let draftAccount = Array(") stored in account ".utf8)
    private static let draftTail = Array("; it is NOT sent.".utf8)

    /// assistant.ParseDraftResult: the draft a create_draft tool result
    /// names (the panel asks only for the results of its create_draft
    /// calls, by tool use id). Only the result's first line counts (up to
    /// the first "\n"), and only when it starts exactly with the head the
    /// bridge itself writes, "draft <id> (version <n>) stored in account
    /// <acc>; it is NOT sent.", followed by the end of the line or a space
    /// (the bridge goes on with a sentence about sending). That line is the
    /// bridge's own text, outside the fence in which it quotes mail, so
    /// mail cannot forge it; the ids are non-empty and hold no white space
    /// or control characters, the version is 1 to 9 digits. The
    /// application still looks the draft up with draft.list before it
    /// offers to open it (`ActionsController.openSavedDraft`).
    public static func parseDraftResult(_ text: String) -> DraftRef? {
        let b = Array(text.utf8)
        let lineEnd = b.firstIndex(of: 0x0A) ?? b.count
        guard lineEnd >= draftHead.count, b[0..<draftHead.count].elementsEqual(draftHead) else { return nil }
        let idStart = draftHead.count
        guard let idEnd = firstIndex(of: draftVersion, in: b, from: idStart, to: lineEnd) else { return nil }
        let digitsStart = idEnd + draftVersion.count
        guard let digitsEnd = firstIndex(of: draftAccount, in: b, from: digitsStart, to: lineEnd) else { return nil }
        let accStart = digitsEnd + draftAccount.count
        guard let accEnd = firstIndex(of: draftTail, in: b, from: accStart, to: lineEnd) else { return nil }
        let restStart = accEnd + draftTail.count
        let digits = b[digitsStart..<digitsEnd]
        guard validID(b, idStart, idEnd), validID(b, accStart, accEnd),
              (1...9).contains(digits.count), digits.allSatisfy({ (0x30...0x39).contains($0) }),
              restStart == lineEnd || b[restStart] == 0x20,
              let version = Int(String(decoding: digits, as: UTF8.self)) else { return nil }
        return DraftRef(
            accountID: String(decoding: b[accStart..<accEnd], as: UTF8.self),
            draftID: String(decoding: b[idStart..<idEnd], as: UTF8.self), version: version)
    }

    /// An id of the bridge's head: non-empty, no white space or control
    /// characters (Go's unicode.IsSpace and unicode.IsControl).
    private static func validID(_ b: [UInt8], _ lo: Int, _ hi: Int) -> Bool {
        guard hi > lo else { return false }
        var i = lo
        while i < hi {
            let (r, w) = decodeRune(b, i, hi)
            if isSpace(r) || isControl(r) {
                return false
            }
            i += w
        }
        return true
    }
}

// MARK: - Go's encoding/json, the reading half

/// JSON read as Go's encoding/json reads it into `json.RawMessage` and
/// `map[string]json.RawMessage`: a value is a byte range of the line, read
/// by type only when asked for. Validation is iterative, so a deeply nested
/// line never grows the stack of the thread that reads Claude Code's
/// output.
enum GoJSON {
    /// encoding/json's maxNestingDepth.
    static let maxDepth = 10000

    private static func isWS(_ c: UInt8) -> Bool {
        c == 0x20 || c == 0x09 || c == 0x0A || c == 0x0D
    }

    private static func isHex(_ c: UInt8) -> Bool {
        (0x30...0x39).contains(c) || (0x41...0x46).contains(c) || (0x61...0x66).contains(c)
    }

    private static func hexValue(_ c: UInt8) -> UInt32 {
        switch c {
        case 0x30...0x39: return UInt32(c - 0x30)
        case 0x41...0x46: return UInt32(c - 0x41 + 10)
        default: return UInt32(c - 0x61 + 10)
        }
    }

    /// json.Valid of b[r]: exactly one JSON value, white space around it
    /// allowed.
    static func valid(_ b: [UInt8], _ r: Range<Int>) -> Bool {
        var i = r.lowerBound
        let end = r.upperBound
        var stack: [Bool] = [] // true: an object
        func ws() {
            while i < end, isWS(b[i]) {
                i += 1
            }
        }
        // expectValue: a value comes next; otherwise a key (in an object)
        // or what follows a value.
        enum Next { case value, key, afterValue }
        var next = Next.value
        while true {
            switch next {
            case .value:
                ws()
                guard i < end else { return false }
                switch b[i] {
                case UInt8(ascii: "{"):
                    stack.append(true)
                    guard stack.count <= maxDepth else { return false }
                    i += 1
                    ws()
                    if i < end, b[i] == UInt8(ascii: "}") {
                        stack.removeLast()
                        i += 1
                        next = .afterValue
                    } else {
                        next = .key
                    }
                case UInt8(ascii: "["):
                    stack.append(false)
                    guard stack.count <= maxDepth else { return false }
                    i += 1
                    ws()
                    if i < end, b[i] == UInt8(ascii: "]") {
                        stack.removeLast()
                        i += 1
                        next = .afterValue
                    } else {
                        next = .value
                    }
                case UInt8(ascii: "\""):
                    guard let e = stringEnd(b, i, end) else { return false }
                    i = e
                    next = .afterValue
                case UInt8(ascii: "-"), UInt8(ascii: "0")...UInt8(ascii: "9"):
                    guard let e = numberEnd(b, i, end) else { return false }
                    i = e
                    next = .afterValue
                case UInt8(ascii: "t"):
                    guard literal(b, i, end, "true") else { return false }
                    i += 4
                    next = .afterValue
                case UInt8(ascii: "f"):
                    guard literal(b, i, end, "false") else { return false }
                    i += 5
                    next = .afterValue
                case UInt8(ascii: "n"):
                    guard literal(b, i, end, "null") else { return false }
                    i += 4
                    next = .afterValue
                default:
                    return false
                }
            case .key:
                ws()
                guard i < end, b[i] == UInt8(ascii: "\""), let e = stringEnd(b, i, end) else { return false }
                i = e
                ws()
                guard i < end, b[i] == UInt8(ascii: ":") else { return false }
                i += 1
                next = .value
            case .afterValue:
                ws()
                guard let object = stack.last else { return i == end }
                guard i < end else { return false }
                let c = b[i]
                i += 1
                if c == UInt8(ascii: ",") {
                    next = object ? .key : .value
                } else if c == (object ? UInt8(ascii: "}") : UInt8(ascii: "]")) {
                    stack.removeLast()
                    next = .afterValue
                } else {
                    return false
                }
            }
        }
    }

    private static func literal(_ b: [UInt8], _ i: Int, _ end: Int, _ word: StaticString) -> Bool {
        let n = word.utf8CodeUnitCount
        guard end - i >= n else { return false }
        return word.withUTF8Buffer { w in
            b[i..<(i + n)].elementsEqual(w)
        }
    }

    /// The end of the string starting at the quote b[i] (after its closing
    /// quote); nil when it is not a valid string (an unescaped control
    /// byte, a bad escape, no closing quote). Bytes from 0x20 up, bad UTF-8
    /// included, stand for themselves, as in Go's scanner.
    private static func stringEnd(_ b: [UInt8], _ start: Int, _ end: Int) -> Int? {
        var i = start + 1
        while i < end {
            let c = b[i]
            if c == UInt8(ascii: "\"") {
                return i + 1
            }
            if c == UInt8(ascii: "\\") {
                guard i + 1 < end else { return nil }
                switch b[i + 1] {
                case UInt8(ascii: "\""), UInt8(ascii: "\\"), UInt8(ascii: "/"), UInt8(ascii: "b"), UInt8(ascii: "f"),
                     UInt8(ascii: "n"), UInt8(ascii: "r"), UInt8(ascii: "t"):
                    i += 2
                case UInt8(ascii: "u"):
                    guard i + 5 < end, isHex(b[i + 2]), isHex(b[i + 3]), isHex(b[i + 4]), isHex(b[i + 5]) else {
                        return nil
                    }
                    i += 6
                default:
                    return nil
                }
                continue
            }
            if c < 0x20 {
                return nil
            }
            i += 1
        }
        return nil
    }

    /// The end of the number starting at b[i]: -?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?
    private static func numberEnd(_ b: [UInt8], _ start: Int, _ end: Int) -> Int? {
        var i = start
        func digit() -> Bool { i < end && (0x30...0x39).contains(b[i]) }
        if b[i] == UInt8(ascii: "-") {
            i += 1
        }
        guard digit() else { return nil }
        if b[i] == UInt8(ascii: "0") {
            i += 1
        } else {
            while digit() { i += 1 }
        }
        if i < end, b[i] == UInt8(ascii: ".") {
            i += 1
            guard digit() else { return nil }
            while digit() { i += 1 }
        }
        if i < end, b[i] == UInt8(ascii: "e") || b[i] == UInt8(ascii: "E") {
            i += 1
            if i < end, b[i] == UInt8(ascii: "+") || b[i] == UInt8(ascii: "-") {
                i += 1
            }
            guard digit() else { return nil }
            while digit() { i += 1 }
        }
        return i
    }

    /// The end of the (valid) value that starts at b[i].
    private static func skipValue(_ b: [UInt8], _ start: Int, _ end: Int) -> Int {
        var i = start
        switch b[i] {
        case UInt8(ascii: "\""):
            return stringEnd(b, i, end) ?? end
        case UInt8(ascii: "{"), UInt8(ascii: "["):
            var depth = 0
            while i < end {
                switch b[i] {
                case UInt8(ascii: "\""):
                    i = stringEnd(b, i, end) ?? end
                    continue
                case UInt8(ascii: "{"), UInt8(ascii: "["):
                    depth += 1
                case UInt8(ascii: "}"), UInt8(ascii: "]"):
                    depth -= 1
                    if depth == 0 {
                        return i + 1
                    }
                default:
                    break
                }
                i += 1
            }
            return end
        default:
            while i < end {
                let c = b[i]
                if isWS(c) || c == UInt8(ascii: ",") || c == UInt8(ascii: "}") || c == UInt8(ascii: "]") {
                    break
                }
                i += 1
            }
            return i
        }
    }

    /// The first non-white-space position of a (valid) raw value.
    private static func first(_ b: [UInt8], _ r: Range<Int>) -> Int? {
        var i = r.lowerBound
        while i < r.upperBound, isWS(b[i]) {
            i += 1
        }
        return i < r.upperBound ? i : nil
    }

    /// A raw value as a string, nil when it is not one (null is not):
    /// escapes decoded, a lone surrogate and bad UTF-8 as U+FFFD, as Go's
    /// unquote does.
    static func string(_ b: [UInt8], _ r: Range<Int>?) -> String? {
        guard let r, let start = first(b, r), b[start] == UInt8(ascii: "\"") else { return nil }
        let end = (stringEnd(b, start, r.upperBound) ?? r.upperBound) - 1
        var out: [UInt8] = []
        out.reserveCapacity(end - start)
        func put(_ scalar: UInt32) {
            Assistant.appendUTF8(scalar, to: &out)
        }
        func u4(_ at: Int) -> UInt32? {
            guard at + 5 < end, b[at] == UInt8(ascii: "\\"), b[at + 1] == UInt8(ascii: "u"),
                  isHex(b[at + 2]), isHex(b[at + 3]), isHex(b[at + 4]), isHex(b[at + 5]) else { return nil }
            return hexValue(b[at + 2]) << 12 | hexValue(b[at + 3]) << 8 | hexValue(b[at + 4]) << 4 | hexValue(b[at + 5])
        }
        var i = start + 1
        while i < end {
            let c = b[i]
            if c == UInt8(ascii: "\\") {
                switch b[i + 1] {
                case UInt8(ascii: "b"): out.append(0x08)
                case UInt8(ascii: "f"): out.append(0x0C)
                case UInt8(ascii: "n"): out.append(0x0A)
                case UInt8(ascii: "r"): out.append(0x0D)
                case UInt8(ascii: "t"): out.append(0x09)
                case UInt8(ascii: "u"):
                    var rr = u4(i) ?? 0xFFFD
                    i += 6
                    if (0xD800...0xDFFF).contains(rr) {
                        if let rr1 = u4(i), (0xD800..<0xDC00).contains(rr), (0xDC00..<0xE000).contains(rr1) {
                            rr = 0x10000 + ((rr - 0xD800) << 10 | (rr1 - 0xDC00))
                            i += 6
                        } else {
                            rr = 0xFFFD
                        }
                    }
                    put(rr)
                    continue
                default: out.append(b[i + 1]) // " \ /
                }
                i += 2
                continue
            }
            if c < 0x80 {
                out.append(c)
                i += 1
                continue
            }
            let (rr, w) = Assistant.decodeRune(b, i, end)
            if rr == 0xFFFD, w == 1 {
                put(0xFFFD)
            } else {
                out.append(contentsOf: b[i..<(i + w)])
            }
            i += w
        }
        return String(decoding: out, as: UTF8.self)
    }

    /// A raw value as an array's elements; empty when it is not an array.
    static func array(_ b: [UInt8], _ r: Range<Int>?) -> [Range<Int>] {
        guard let r, var i = first(b, r), b[i] == UInt8(ascii: "[") else { return [] }
        var out: [Range<Int>] = []
        i += 1
        while i < r.upperBound {
            while i < r.upperBound, isWS(b[i]) || b[i] == UInt8(ascii: ",") {
                i += 1
            }
            guard i < r.upperBound, b[i] != UInt8(ascii: "]") else { break }
            let e = skipValue(b, i, r.upperBound)
            out.append(i..<e)
            i = e
        }
        return out
    }

    /// A raw value as a Bool: true for `true` only.
    static func boolean(_ b: [UInt8], _ r: Range<Int>?) -> Bool {
        guard let r, let i = first(b, r) else { return false }
        return literal(b, i, r.upperBound, "true")
    }

    /// A raw value as a float64: a number that fits, else 0.
    static func number(_ b: [UInt8], _ r: Range<Int>?) -> Double {
        guard let r, let i = first(b, r), b[i] == UInt8(ascii: "-") || (0x30...0x39).contains(b[i]) else { return 0 }
        let e = numberEnd(b, i, r.upperBound) ?? i
        guard let d = Double(String(decoding: b[i..<e], as: UTF8.self)), d.isFinite else { return 0 }
        return d
    }

    /// A raw JSON object, its members by key (the last of duplicates).
    struct Object {
        let b: [UInt8]
        let members: [String: Range<Int>]

        /// nil when `r` is missing or not an object (null included).
        init?(_ b: [UInt8], _ r: Range<Int>?) {
            guard let r, var i = GoJSON.first(b, r), b[i] == UInt8(ascii: "{") else { return nil }
            self.b = b
            var members: [String: Range<Int>] = [:]
            i += 1
            while i < r.upperBound {
                while i < r.upperBound, isWS(b[i]) || b[i] == UInt8(ascii: ",") {
                    i += 1
                }
                guard i < r.upperBound, b[i] == UInt8(ascii: "\"") else { break }
                let keyEnd = stringEnd(b, i, r.upperBound) ?? r.upperBound
                let key = GoJSON.string(b, i..<keyEnd) ?? ""
                i = keyEnd
                while i < r.upperBound, isWS(b[i]) || b[i] == UInt8(ascii: ":") {
                    i += 1
                }
                guard i < r.upperBound else { break }
                let e = skipValue(b, i, r.upperBound)
                members[key] = i..<e
                i = e
            }
            self.members = members
        }

        func str(_ key: String) -> String { GoJSON.string(b, members[key]) ?? "" }
        func obj(_ key: String) -> Object? { Object(b, members[key]) }
        func array(_ key: String) -> [Range<Int>] { GoJSON.array(b, members[key]) }
        func boolean(_ key: String) -> Bool { GoJSON.boolean(b, members[key]) }
        func number(_ key: String) -> Double { GoJSON.number(b, members[key]) }
    }
}
