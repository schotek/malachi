// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The Czech side of ui/internal/jira: the cases jira_test.go and
// wizard_test.go check with a fake catalogue (a context entry must win
// over the plain msgid), here against the catalogues `make locale`
// generates from po/, and the intent of po_test.go: the Swift port asks
// for exactly the msgids, contexts and plurals po/malachi.pot assigns to
// ui/internal/jira. The process-wide `L10n.catalogue` stays English (see
// LocalizationTests), so the port's own calls are read from its sources.

/// The generated catalogues (`MALACHI_LOCALE_DIR`, as `make test-macos`
/// exports it); nil skips the Czech tests.
private let jiraLocaleDir: URL? = {
    guard let dir = ProcessInfo.processInfo.environment[Catalogue.localeDirEnv], !dir.isEmpty else { return nil }
    let url = URL(fileURLWithPath: dir, isDirectory: true)
    return Catalogue.availableLanguages(in: url).contains("cs") ? url : nil
}()

private let jiraLocaleHint: Comment = "run `make -C macos locale` and export MALACHI_LOCALE_DIR"

/// The repository root, from this file's place in macos/Tests/MalachiCoreTests.
private let repoRoot = URL(fileURLWithPath: #filePath)
    .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()

private let templatePath = repoRoot.appendingPathComponent("po/malachi.pot")
private let portDir = repoRoot.appendingPathComponent("macos/Sources/MalachiCore/Jira", isDirectory: true)

private let haveSources = FileManager.default.fileExists(atPath: templatePath.path)
    && FileManager.default.fileExists(atPath: portDir.path)

/// po_test.go `msgKey`: a msgid with its context and plural ("" for none).
private struct MsgKey: Hashable, CustomStringConvertible {
    var ctx = ""
    var msgid = ""
    var plural = ""

    var description: String { "msgid \(msgid.debugDescription) (context \(ctx.debugDescription), plural \(plural.debugDescription))" }
}

/// A C-quoted string of a PO file or a Swift literal without its quotes,
/// unescaped (\" \\ \n \t).
private func unquote(_ s: Substring) -> String {
    var out = ""
    var escaped = false
    for c in s {
        if escaped {
            switch c {
            case "n": out.append("\n")
            case "t": out.append("\t")
            default: out.append(c)
            }
            escaped = false
        } else if c == "\\" {
            escaped = true
        } else {
            out.append(c)
        }
    }
    return out
}

/// po_test.go `template`: every entry of po/malachi.pot, and whether it
/// names a file of ui/internal/jira among its references.
private func template() throws -> [MsgKey: Bool] {
    let text = try String(contentsOf: templatePath, encoding: .utf8)
    var out: [MsgKey: Bool] = [:]
    var cur = MsgKey()
    var ours = false
    var field: WritableKeyPath<MsgKey, String>?
    func flush() {
        if !cur.msgid.isEmpty {
            out[cur] = ours
        }
        cur = MsgKey()
        ours = false
        field = nil
    }
    for line in text.split(separator: "\n", omittingEmptySubsequences: false) {
        let trimmed = line.trimmingCharacters(in: .whitespaces)
        if trimmed.isEmpty {
            flush()
        } else if line.hasPrefix("#:") {
            ours = ours || line.contains("ui/internal/jira/")
        } else if line.hasPrefix("\""), line.hasSuffix("\""), line.count >= 2, let field {
            cur[keyPath: field] += unquote(line.dropFirst().dropLast())
        } else if let space = line.firstIndex(of: " "), line[line.index(after: space)...].hasPrefix("\""), line.hasSuffix("\"") {
            let value = unquote(line[line.index(space, offsetBy: 2)..<line.index(before: line.endIndex)])
            switch line[..<space] {
            case "msgctxt": field = \.ctx
            case "msgid": field = \.msgid
            case "msgid_plural": field = \.plural
            default: field = nil
            }
            if let field {
                cur[keyPath: field] = value
            }
        }
    }
    flush()
    return out
}

/// What the Swift port translates: every `L10n.T("…")`, `L10n.N("…", "…"`
/// and `L10n.C("…", "…")` of its sources (po_test.go `exercise` with a
/// recorder; the sources stand in for the calls).
private func portKeys() throws -> Set<MsgKey> {
    let literal = #""((?:[^"\\]|\\.)*)""#
    let call = try NSRegularExpression(pattern: #"L10n\.([TNC])\(\s*"# + literal + #"(?:\s*,\s*"# + literal + ")?")
    var keys = Set<MsgKey>()
    let files = try FileManager.default.contentsOfDirectory(at: portDir, includingPropertiesForKeys: nil)
        .filter { $0.pathExtension == "swift" }
    for file in files {
        let src = try String(contentsOf: file, encoding: .utf8)
        let ns = src as NSString
        for m in call.matches(in: src, range: NSRange(location: 0, length: ns.length)) {
            let fn = ns.substring(with: m.range(at: 1))
            let first = unquote(Substring(ns.substring(with: m.range(at: 2))))
            let second = m.range(at: 3).location == NSNotFound ? nil : unquote(Substring(ns.substring(with: m.range(at: 3))))
            switch fn {
            case "C":
                keys.insert(MsgKey(ctx: first, msgid: second ?? ""))
            case "N":
                keys.insert(MsgKey(msgid: first, plural: second ?? ""))
            default:
                keys.insert(MsgKey(msgid: first))
            }
        }
    }
    return keys
}

struct JiraTranslationTests {
    private var cs: Catalogue { Catalogue.load(from: jiraLocaleDir!, languages: ["cs"]) }

    /// jira_test.go TestVirtualFolders, TestIssueCard and wizard_test.go
    /// TestWizardTexts in Czech: the context entry, never the plain msgid.
    @Test(.enabled(if: jiraLocaleDir != nil, jiraLocaleHint))
    func contextsPickTheJiraEntries() {
        let cs = cs
        #expect(cs.context("folder", "Assigned to Me") == "Přiřazené mně")
        #expect(cs.context("folder", "Watching") == "Sledované")
        #expect(cs.context("folder", "Open") == "Neuzavřené")
        #expect(cs.translate("Open") != "Neuzavřené", "the verb must not be the folder")
        #expect(cs.context("jira value", "None") == "Nezadáno")
        #expect(cs.translate("None") == "Žádné", "the plain None is another entry")
        #expect(cs.translate("Unassigned") == "Nepřiřazeno")
        #expect(cs.context("jira", "Spaces") == "Prostory")
        #expect(cs.context("jira", "Internal") == "Interní")
        #expect(cs.translate("Add _Jira Account…") == "Přidat účet _Jira…")
        #expect(cs.context("change list separator", "; ") == "; ")
    }

    @Test(.enabled(if: jiraLocaleDir != nil, jiraLocaleHint))
    func formattedSentences() {
        let cs = cs
        #expect(cs.translate("Status: %s → %s", ["K řešení", "Probíhá"]) == "Stav: K řešení → Probíhá")
        #expect(cs.translate("Assignee: %s → %s", ["Nepřiřazeno", "Jana Dvořáková"]) == "Řešitel: Nepřiřazeno → Jana Dvořáková")
        #expect(cs.translate("Found %s, version %s", ["Acme Jira", "9.12.4"]) == "Nalezeno: Acme Jira, verze 9.12.4")
        #expect(cs.translate("Found %s", ["Jira Cloud"]) == "Nalezeno: Jira Cloud")
        #expect(cs.translate("Open %s in the Browser", ["ITSD-42"]) == "Otevřít ITSD-42 v prohlížeči")
        #expect(cs.translate("Comment on %s", ["ITSD-42"]) == "Komentář k ITSD-42")
        #expect(cs.translate("The Jira site rejected the token of %s", ["Acme"]) == "Web Jira odmítl token účtu Acme")
        #expect(cs.translate("via %s", ["Issue Sync"]) == "přes Issue Sync")
    }

    @Test(.enabled(if: jiraLocaleDir != nil, jiraLocaleHint))
    func plurals() {
        let cs = cs
        #expect(cs.plural("about %d issue", "about %d issues", 1) == "přibližně 1 úkol")
        #expect(cs.plural("about %d issue", "about %d issues", 3) == "přibližně 3 úkoly")
        #expect(cs.plural("about %d issue", "about %d issues", 5) == "přibližně 5 úkolů")
        #expect(cs.plural("about %d issue", "about %d issues", 0) == "přibližně 0 úkolů")
        #expect(cs.plural("Select at most %d space", "Select at most %d spaces", API.Limits.maxJiraSpaces)
                    == "Vyberte nejvýš 200 prostorů")
    }

    /// Every msgid the port asks for has a Czech translation.
    @Test(.enabled(if: jiraLocaleDir != nil && haveSources, jiraLocaleHint))
    func everyMsgidOfThePortIsTranslated() throws {
        let cs = cs
        let keys = try portKeys()
        #expect(keys.count > 50, "found only \(keys.count) calls")
        for k in keys {
            if !k.plural.isEmpty {
                #expect(cs.pluralForms(k.msgid) != nil, "no Czech plural for \(k)")
            } else if !k.ctx.isEmpty {
                #expect(cs.string(Catalogue.contextKey(k.ctx, k.msgid)) != nil, "no Czech translation for \(k)")
            } else {
                #expect(cs.string(k.msgid) != nil, "no Czech translation for \(k)")
            }
        }
    }

    /// po_test.go TestMsgidsInTemplate for the port: every msgid it
    /// translates is in the template with its context and plural, and
    /// every entry that names ui/internal/jira is one it translates.
    @Test(.enabled(if: haveSources, "po/malachi.pot or the port's sources are not next to this file"))
    func portUsesTheMsgidsOfTheGoPackage() throws {
        let pot = try template()
        let used = try portKeys()
        #expect(pot.values.contains(true), "no entry of po/malachi.pot names ui/internal/jira")
        for k in used {
            #expect(pot[k] != nil, "po/malachi.pot lacks \(k)")
        }
        for (k, ours) in pot where ours {
            #expect(used.contains(k), "po/malachi.pot names ui/internal/jira for \(k), which the Swift port does not translate")
        }
    }
}
