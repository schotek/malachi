// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

// DEVELOPMENT AID, not a feature (MALACHI_START, see
// MainWindowController+DevStart.swift): the eWay-CRM steps.
//
//   `crm-dialog` opens the Save to eWay-CRM sheet on the main window over
//   invented suggestions (`CRMDialogSession.sample`, no daemon), dumps its
//   frame, the groups' rows, the check boxes, the markers and the buttons,
//   then checks another deal, makes it the superior item and dumps again,
//   and cancels.
//   `eway-wizard` opens the eWay-CRM account assistant on the main window,
//   types an http address (the refusal under the field) and then an https
//   one, dumps the page and closes it; nothing is sent to a daemon.
// Each prints `LAYOUT:` lines and `LAYOUT: shot <step> window <n>`.
@MainActor
enum DevelopmentCRM {
    static func step(_ step: String, in main: MainWindowController) -> Bool {
        switch step {
        case "crm-dialog": dialog(main)
        case "eway-wizard": wizard(main)
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
        for s in v.subviews {
            if let t = s as? T {
                found.append(t)
            }
            found += all(type, under: s)
        }
        return found
    }

    private static func dump(_ stage: String, _ sheet: NSWindow) {
        guard let root = sheet.contentView else { return }
        root.layoutSubtreeIfNeeded()
        say(String(format: "LAYOUT: crm-dialog %@ window %ld frame %@ (wireframe 520x600)",
                   stage, sheet.windowNumber, NSStringFromRect(sheet.frame)))
        for t in all(NSTextField.self, under: root) where !t.isHiddenOrHasHiddenAncestor && !t.stringValue.isEmpty {
            say("LAYOUT: crm-dialog \(stage) text \(r(t)) '\(t.stringValue)'")
        }
        for b in all(NSButton.self, under: root) where !b.isHiddenOrHasHiddenAncestor {
            say("LAYOUT: crm-dialog \(stage) button \(r(b)) '\(b.title)' state \(b.state.rawValue) enabled \(b.isEnabled)")
        }
    }

    private static func dialog(_ main: MainWindowController) {
        guard let w = main.window else { return }
        let session = CRMDialogSession.sample()
        CRMDialogController.present(session, on: w)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.8) {
            guard let sheet = w.attachedSheet else {
                say("LAYOUT: crm-dialog no sheet")
                return
            }
            dump("prefilled", sheet)
            say("LAYOUT: shot crm-dialog window \(w.windowNumber)")
            let deal = CrmItemRef(kind: .deal, guid: "00000000-0000-4000-8000-000000000006")
            session.toggle(deal, true)
            session.setSuperior(deal)
            dump("moved superior", sheet)
            say("LAYOUT: crm-dialog would save \(String(describing: session.pendingSave))")
            DispatchQueue.main.asyncAfter(deadline: .now() + 1.0) {
                session.cancel()
                say("LAYOUT: crm-dialog cancelled, sheet left \(w.attachedSheet != nil)")
            }
        }
    }

    private static func wizard(_ main: MainWindowController) {
        guard let w = main.window else { return }
        EWayWizardWindowController.present(from: w, client: main.state.client) { _, _ in }
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.8) {
            guard let sheet = w.attachedSheet, let c = sheet.windowController as? EWayWizardWindowController,
                  let root = sheet.contentView else {
                say("LAYOUT: eway-wizard no sheet")
                return
            }
            c.wizard.setService("http://hosting.eway-crm.com/acme")
            root.layoutSubtreeIfNeeded()
            for t in all(NSTextField.self, under: root) where !t.isHiddenOrHasHiddenAncestor && !t.stringValue.isEmpty {
                say("LAYOUT: eway-wizard http text \(r(t)) '\(t.stringValue)'")
            }
            c.wizard.setService("hosting.eway-crm.com/acme")
            root.layoutSubtreeIfNeeded()
            say(String(format: "LAYOUT: eway-wizard window %ld frame %@ (wireframe 520x560)",
                       sheet.windowNumber, NSStringFromRect(sheet.frame)))
            for t in all(NSTextField.self, under: root) where !t.isHiddenOrHasHiddenAncestor {
                say("LAYOUT: eway-wizard text \(r(t)) '\(t.stringValue)' editable \(t.isEditable)")
            }
            for b in all(NSButton.self, under: root) where !b.isHiddenOrHasHiddenAncestor && !b.title.isEmpty {
                say("LAYOUT: eway-wizard button \(r(b)) '\(b.title)' enabled \(b.isEnabled)")
            }
            say("LAYOUT: shot eway-wizard window \(w.windowNumber)")
            DispatchQueue.main.asyncAfter(deadline: .now() + 1.0) {
                c.dismiss()
                say("LAYOUT: eway-wizard closed, sheet left \(w.attachedSheet != nil)")
            }
        }
    }
}
