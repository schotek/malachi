// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

// DEVELOPMENT AID, not a feature (MALACHI_START, see
// MainWindowController+DevStart.swift): the mail rules' steps, meant for
// MALACHI_RULES_SAMPLES=1 (`SampleRulesCaller`, no daemon).
//
//   `prefs-rules` opens Settings → Rules and dumps the page: its frame,
//   each row's frame and texts (name, conditions, actions, account and
//   status, problem) and whether it sends mail.
//   `rules-editor` opens the editor of the sample "Newsletters" rule (else
//   the first rule, else a new one) as a sheet on the main window and dumps
//   the sheet's frame, its controls and the problems shown, then adds a
//   Forward to action and dumps the action rows again. Nothing is saved.
// Each prints `LAYOUT:` lines and `LAYOUT: shot <step> window <n>`; the
// windows stay open for a `shot=` step.
@MainActor
enum DevelopmentRules {
    static func step(_ step: String, in main: MainWindowController) -> Bool {
        switch step {
        case "prefs-rules": prefs(main)
        case "rules-editor": editor(main)
        default: return false
        }
        return true
    }

    private static func say(_ line: String) {
        FileHandle.standardError.write(Data((line + "\n").utf8))
    }

    private static func r(_ v: NSView) -> String {
        let f = v.convert(v.bounds, to: nil)
        return String(format: "(%.0f,%.0f %.0fx%.0f)", f.minX, f.minY, f.width, f.height)
    }

    private static func all<T: NSView>(_ type: T.Type, under v: NSView) -> [T] {
        var found: [T] = []
        func walk(_ v: NSView) {
            if let t = v as? T {
                found.append(t)
            }
            v.subviews.forEach(walk)
        }
        walk(v)
        return found
    }

    private static func later(_ seconds: Double, _ f: @escaping @MainActor () -> Void) {
        DispatchQueue.main.asyncAfter(deadline: .now() + seconds) {
            MainActor.assumeIsolated { f() }
        }
    }

    private static func prefs(_ main: MainWindowController) {
        let prefs = Integration.showPreferences(main.state)
        prefs.select(.rules)
        guard let w = prefs.window else {
            say("LAYOUT: prefs-rules no Settings window")
            return
        }
        later(1.0) {
            let pane = prefs.rulesPane
            say("LAYOUT: prefs-rules window \(w.windowNumber) frame \(NSStringFromRect(w.frame)) rows \(pane.rows.count)"
                + " samples \(main.state.rules.samples)")
            for cell in all(RuleRowCell.self, under: pane.view) {
                let texts = all(NSTextField.self, under: cell).map(\.stringValue).filter { !$0.isEmpty }
                say("LAYOUT: prefs-rules row \(r(cell)) texts \(texts)")
            }
            for row in pane.rows {
                say("LAYOUT: prefs-rules rule '\(row.title)' enabled \(row.enabled) outgoing \(row.outgoing)"
                    + " problem \(!row.problem.isEmpty)")
            }
            say("LAYOUT: shot prefs-rules window \(w.windowNumber)")
        }
    }

    private static func editor(_ main: MainWindowController) {
        let rules = main.state.rules!
        let c = rules.controller
        guard let window = main.window else { return }
        if let r = c.rule(SampleRulesCaller.newsletters) ?? c.rules.first {
            rules.editRule(r.id, on: window)
        } else {
            rules.addRule(account: c.mailAccounts.first?.id, on: window)
        }
        later(1.0) {
            guard let e = rules.openEditors.first, let sheet = e.window, let content = sheet.contentView else {
                say("LAYOUT: rules-editor no sheet")
                return
            }
            dump(e, sheet, content, "open")
            e.editor.addAction(.forward)
            later(0.5) {
                dump(e, sheet, content, "forward added")
                say("LAYOUT: shot rules-editor window \(main.window?.windowNumber ?? 0)")
            }
        }
    }

    private static func dump(_ e: RuleEditorWindowController, _ sheet: NSWindow, _ content: NSView, _ phase: String) {
        let f = e.editor.form
        say("LAYOUT: rules-editor \(phase) sheet \(NSStringFromRect(sheet.frame)) title '\(sheet.title)'"
            + " conditions \(f.conditions.count) actions \(f.actions.map(\.kind.rawValue)) canSave \(e.editor.canSave)")
        for p in all(NSPopUpButton.self, under: content) where !p.isHiddenOrHasHiddenAncestor {
            say("LAYOUT: rules-editor popup '\(p.titleOfSelectedItem ?? "")' items \(p.numberOfItems) \(r(p))")
        }
        for t in all(NSTextField.self, under: content) where !t.isHiddenOrHasHiddenAncestor && !t.stringValue.isEmpty {
            say("LAYOUT: rules-editor text '\(t.stringValue)' \(r(t))")
        }
        for p in e.editor.problems {
            say("LAYOUT: rules-editor problem \(p.field) \(p.index) '\(Rules.problemText(p))'")
        }
    }
}
