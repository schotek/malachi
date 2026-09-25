// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/compose/prefill.go: reply, reply-all and forward parameters
// built by the UI itself (the fallback when draft.create cannot be asked),
// the attribution line handed to draft.create, and the window parameters
// made from a draft.create result.

import Foundation

/// compose.FromDraft: turns a draft.create result into window parameters.
/// A draft without HTML (the backend could not quote formatted) shows its
/// text.
public func fromDraft(kind: ComposeKind, draft d: Draft, blocked: BlockedContent) -> ComposeParams {
    var p = ComposeParams(
        kind: kind,
        accountID: d.accountId.rawValue.isEmpty ? nil : d.accountId,
        to: d.to,
        cc: d.cc ?? [],
        bcc: d.bcc ?? [],
        subject: d.subject,
        bodyHTML: d.htmlBody ?? "",
        inReplyTo: d.inReplyTo,
        forwarding: d.forwarding,
        attachments: d.attachments ?? [],
        blocked: blocked
    )
    if p.bodyHTML.isEmpty {
        p.bodyHTML = escapeText(d.textBody)
    }
    return p
}

/// compose.Prefill: builds the reply / reply-all / forward parameters from
/// `source` on the UI's own: the fallback when draft.create cannot be
/// asked (no backend), quoting the plain text only. The normal path is
/// draft.create, which quotes the original formatted, with its pictures.
public func prefill(kind: ComposeKind, source src: ComposeSource, self selfAddress: Address) -> ComposeParams {
    var p = ComposeParams(kind: kind)
    switch kind {
    case .reply:
        p.to = dedupeAddresses(replyTargets(src), exclude: [])
        p.subject = replySubject(src.subject)
        p.bodyHTML = quoteHTML(kind: kind, source: src)
        p.inReplyTo = nonEmptyID(src.id)
    case .replyAll:
        p.to = dedupeAddresses(replyTargets(src), exclude: [])
        let exclude = [selfAddress] + p.to
        p.cc = dedupeAddresses(src.to + src.cc, exclude: exclude)
        p.subject = replySubject(src.subject)
        p.bodyHTML = quoteHTML(kind: kind, source: src)
        p.inReplyTo = nonEmptyID(src.id)
    case .forward:
        p.subject = forwardSubject(src.subject)
        p.bodyHTML = forwardHTML(kind: kind, source: src)
        p.forwarding = nonEmptyID(src.id)
    case .new:
        break
    }
    return p
}

/// compose.Attribution: the line above a quote in the user's language:
/// "On <date>, <sender> wrote:" for a reply, the header block of a
/// forwarded message. Plain text, lines separated by "\n", nothing
/// escaped: it is what draft.create is handed (the backend escapes it) and
/// what the fallback quote escapes itself. Empty for a new message. Cut to
/// the backend's cap (`API.Limits.maxDraftAttributionBytes`) with a
/// trailing ellipsis on a UTF-8 boundary.
public func attribution(kind: ComposeKind, source src: ComposeSource) -> String {
    var s: String
    let date = sourceDate(src)
    switch kind {
    case .reply, .replyAll:
        let names = displayNames(src.from)
        if let date {
            // TRANSLATORS: quote header; %s are the date and the sender.
            s = L10n.T("On %s, %s wrote:", prefillFormatDateTime(date), names)
        } else {
            // TRANSLATORS: quote header without a date; %s is the sender.
            s = L10n.T("%s wrote:", names)
        }
    case .forward:
        var lines = [
            L10n.T("---------- Forwarded message ----------"),
            L10n.T("From: %s", formatAll(src.from)),
        ]
        if let date {
            lines.append(L10n.T("Date: %s", prefillFormatDateTime(date)))
        }
        lines.append(L10n.T("Subject: %s", src.subject))
        if !src.to.isEmpty {
            lines.append(L10n.T("To: %s", formatAll(src.to)))
        }
        s = lines.joined(separator: "\n")
    case .new:
        return ""
    }
    // The backend's cap; a message to hundreds of people has a To: line
    // that would break it.
    let cap = API.Limits.maxDraftAttributionBytes
    if s.utf8.count > cap {
        let bytes = Array(s.utf8)
        var n = cap - ellipsis.utf8.count
        // Back off to a UTF-8 boundary: never cut inside a scalar.
        while n > 0, bytes[n] & 0xC0 == 0x80 {
            n -= 1
        }
        s = String(decoding: bytes[..<n], as: UTF8.self) + ellipsis
    }
    return s
}

/// compose.ReplySubject: "Re: " + subject without existing prefixes
/// (idempotent). The prefixes are deliberately not translated: other
/// clients only recognise the English forms when threading and
/// de-duplicating them.
public func replySubject(_ s: String) -> String {
    "Re: " + stripPrefixes(s)
}

/// compose.ForwardSubject: "Fwd: " + subject without existing prefixes.
public func forwardSubject(_ s: String) -> String {
    "Fwd: " + stripPrefixes(s)
}

/// compose.escapeText: escapes plain text for HTML (as html.EscapeString:
/// `<>&'"`) and turns newlines into <br>.
public func escapeText(_ s: String) -> String {
    let text = s.replacingOccurrences(of: "\r\n", with: "\n")
    var out = String.UnicodeScalarView()
    for u in text.unicodeScalars {
        switch u {
        case "&": out.append(contentsOf: "&amp;".unicodeScalars)
        case "'": out.append(contentsOf: "&#39;".unicodeScalars)
        case "<": out.append(contentsOf: "&lt;".unicodeScalars)
        case ">": out.append(contentsOf: "&gt;".unicodeScalars)
        case "\"": out.append(contentsOf: "&#34;".unicodeScalars)
        case "\n": out.append(contentsOf: "<br>".unicodeScalars)
        default: out.append(u)
        }
    }
    return String(out)
}

/// compose.dedupeAddresses: keeps the first occurrence of each address
/// (case-insensitively) that is not in `exclude`.
public func dedupeAddresses(_ input: [Address], exclude: [Address]) -> [Address] {
    var seen = Set<String>()
    for a in exclude {
        seen.insert(addressKey(a))
    }
    var out: [Address] = []
    for a in input {
        let key = addressKey(a)
        if key.isEmpty || seen.contains(key) {
            continue
        }
        seen.insert(key)
        out.append(a)
    }
    return out
}

/// compose.replyTargets: where a reply goes: Reply-To when the sender set
/// one, otherwise From.
func replyTargets(_ src: ComposeSource) -> [Address] {
    src.replyTo.isEmpty ? src.from : src.replyTo
}

/// compose.quoteHTML: the original's text as a cite block under the
/// attribution and an empty paragraph for the answer, the layout
/// draft.create produces. Everything from the source is escaped.
func quoteHTML(kind: ComposeKind, source src: ComposeSource) -> String {
    "<p><br></p><div>" + escapeText(attribution(kind: kind, source: src)) + "</div><blockquote type=\"cite\">"
        + escapeText(src.text) + "</blockquote>"
}

/// compose.forwardHTML: the forwarded-message header block and the text.
func forwardHTML(kind: ComposeKind, source src: ComposeSource) -> String {
    "<p><br></p><div>" + escapeText(attribution(kind: kind, source: src)) + "</div><br>" + escapeText(src.text)
}

/// compose.subjectPrefix: `(?i)^\s*(re|fwd?|aw|wg)\s*:\s*`, with RE2's
/// ASCII `\s`. Built per use: `Regex` is not `Sendable`.
private var subjectPrefix: Regex<(Substring, Substring)> {
    /^[\t\n\f\r ]*(re|fwd?|aw|wg)[\t\n\f\r ]*:[\t\n\f\r ]*/.ignoresCase()
}

/// compose.stripPrefixes: removes any number of Re:/Fwd:-style prefixes.
private func stripPrefixes(_ subject: String) -> String {
    let prefix = subjectPrefix
    var s = Substring(subject)
    while let m = s.prefixMatch(of: prefix) {
        s = s[m.range.upperBound...]
    }
    return s.trimmingCharacters(in: .whitespacesAndNewlines)
}

private let ellipsis = "\u{2026}"

private func nonEmptyID(_ id: MessageID) -> MessageID? {
    id.rawValue.isEmpty ? nil : id
}

/// The source's date, nil for none (Go's zero time counts as none).
private func sourceDate(_ src: ComposeSource) -> Date? {
    guard let date = src.date, !date.isGoZero else { return nil }
    return date
}

private func addressKey(_ a: Address) -> String {
    a.address.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
}

/// compose.displayNames: widget.DisplayName of each, comma-separated.
private func displayNames(_ list: [Address]) -> String {
    list.map(displayName).joined(separator: ", ")
}

/// compose.formatAll: widget.FormatAddress of each, comma-separated.
private func formatAll(_ list: [Address]) -> String {
    list.map(formatAddress).joined(separator: ", ")
}

/// widget.FormatDateTime for the quote header, in the catalogue's
/// language so day and month names match the sentence around them.
private func prefillFormatDateTime(_ date: Date) -> String {
    formatDateTime(date, locale: Locale(identifier: L10n.catalogue.language))
}
