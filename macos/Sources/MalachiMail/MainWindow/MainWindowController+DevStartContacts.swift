// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

// DEVELOPMENT AID, not a feature (MALACHI_START, see
// MainWindowController+DevStart.swift): the Contacts window's steps, meant
// for MALACHI_CONTACTS_SAMPLES=1 (`SampleContactsCaller`, no daemon).
//
//   `contacts` opens the Contacts window, selects Jana Nováková and dumps
//   the window, the three columns, the toolbar, the sidebar's rows, the
//   list's rows and the card (W1).
//   `contacts:edit` opens her form (W4), dumps the fields and the buttons,
//   adds a phone row and dumps again.
//   `contacts:new` opens New Contact with its address book choice.
//   `contacts:narrow` makes the window 700 and then 500 pt wide with her
//   selected, and dumps what folded (W5 d).
//   `contacts:add` opens Add to Contacts… over the main window for an
//   invented sender (W4 ④) and dumps it.
//   `prefs-contacts` opens Settings → Contacts and dumps its groups (W3).
// Each prints `LAYOUT:` lines and `LAYOUT: shot <step> window <n>`; the
// windows stay open for a `shot=` step.
@MainActor
enum DevelopmentContacts {
    static func step(_ step: String, in main: MainWindowController) -> Bool {
        switch step {
        case "contacts": window(main, then: card)
        case "contacts:edit": window(main, then: edit)
        case "contacts:new": window(main, then: newContact)
        case "contacts:narrow": window(main, then: narrow)
        case "contacts:add": add(main)
        case "prefs-contacts": prefs(main)
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

    /// The window with Jana selected once the samples are listed.
    private static func window(_ main: MainWindowController, then: @escaping @MainActor (ContactsWindowController) -> Void) {
        let w = main.state.contacts.showWindow()
        if w.window?.frame.width ?? 0 < 900 {
            w.window?.setContentSize(NSSize(width: 980, height: 640))
        }
        later(0.6) {
            w.list.select(SampleContactsCaller.jana)
            later(0.6) { then(w) }
        }
    }

    private static func columns(_ w: ContactsWindowController, _ step: String) {
        guard let win = w.window else { return }
        say("LAYOUT: \(step) window \(win.windowNumber) frame \(NSStringFromRect(win.frame)) title '\(win.title)' subtitle '\(win.subtitle)'")
        let items = w.split.splitViewItems
        let names = ["sidebar", "list", "detail"]
        for (i, item) in items.enumerated() {
            say("LAYOUT: \(step) \(names[i]) \(r(item.viewController.view)) collapsed \(item.isCollapsed)")
        }
        let toolbar = win.toolbar?.items.map { "\($0.itemIdentifier.rawValue)" }.joined(separator: ", ") ?? ""
        say("LAYOUT: \(step) toolbar [\(toolbar)]")
    }

    private static func card(_ w: ContactsWindowController) {
        columns(w, "contacts")
        say("LAYOUT: contacts sidebar rows \(w.list.items.map { "\($0.kind):\($0.title):\($0.badge):\($0.count)" })")
        let rows = w.list.rows
        say("LAYOUT: contacts list rows \(rows.count) headings \(rows.filter(\.isHeading).map(\.heading)) first '\(rows.first(where: { !$0.isHeading })?.title ?? "")'")
        if let d = w.detail.detail {
            say("LAYOUT: contacts card '\(d.title)' subtitle '\(d.subtitle)' rows \(d.rows.map { "\($0.label)=\($0.value)" })")
        }
        say("LAYOUT: contacts source '\(w.detail.sourceLine)' status '\(w.list.status)'")
        let detail = w.detailPane.view
        for b in all(NSButton.self, under: detail) where !b.title.isEmpty && !b.isHidden {
            say("LAYOUT: contacts card button '\(b.title)' \(r(b))")
        }
        if let avatar = all(ContactInitialsView.self, under: detail).first {
            say("LAYOUT: contacts card avatar \(r(avatar)) (wireframe 72)")
        }
        say("LAYOUT: shot contacts window \(w.window?.windowNumber ?? 0)")
    }

    private static func edit(_ w: ContactsWindowController) {
        w.detail.beginEdit()
        later(0.4) {
            columns(w, "contacts:edit")
            dumpForm(w, "contacts:edit")
            if let add = all(NSButton.self, under: w.detailPane.view).first(where: { $0.title == ContactBook.addRowText(.phone) }) {
                add.performClick(nil)
            }
            later(0.4) {
                dumpForm(w, "contacts:edit after Add Phone")
                say("LAYOUT: contacts:edit dirty \(w.detail.dirty)")
                say("LAYOUT: shot contacts:edit window \(w.window?.windowNumber ?? 0)")
            }
        }
    }

    private static func newContact(_ w: ContactsWindowController) {
        w.detail.cancelEdit()
        w.newContact(nil)
        later(0.5) {
            dumpForm(w, "contacts:new")
            say("LAYOUT: shot contacts:new window \(w.window?.windowNumber ?? 0)")
        }
    }

    private static func dumpForm(_ w: ContactsWindowController, _ step: String) {
        let v = w.detailPane.view
        let fields = all(NSTextField.self, under: v).filter { $0.isEditable && !$0.isHidden }
        say("LAYOUT: \(step) fields \(fields.count) first \(fields.first.map(r) ?? "-") '\(fields.first?.stringValue ?? "")'")
        for f in fields.prefix(12) {
            say("LAYOUT: \(step) field '\(f.identifier?.rawValue ?? "")' \(r(f)) '\(f.stringValue)' placeholder '\(f.placeholderString ?? "")'")
        }
        for p in all(NSPopUpButton.self, under: v) where !p.isHidden {
            say("LAYOUT: \(step) popup '\(p.titleOfSelectedItem ?? "")' \(r(p)) items \(p.numberOfItems)")
        }
        for b in all(NSButton.self, under: v) where !b.title.isEmpty && !b.isHidden && !(b is NSPopUpButton) {
            say("LAYOUT: \(step) button '\(b.title)' \(r(b)) enabled \(b.isEnabled)")
        }
    }

    private static func narrow(_ w: ContactsWindowController) {
        w.window?.setContentSize(NSSize(width: 700, height: 600))
        later(0.4) {
            columns(w, "contacts:narrow 700")
            w.window?.setContentSize(NSSize(width: 500, height: 600))
            later(0.4) {
                columns(w, "contacts:narrow 500")
                let back = all(NSButton.self, under: w.detailPane.view).first { $0.title == ContactBook.windowTitle }
                say("LAYOUT: contacts:narrow 500 back '\(back?.title ?? "-")' visible \(back.map { !$0.isHiddenOrHasHiddenAncestor } ?? false)")
                say("LAYOUT: shot contacts:narrow window \(w.window?.windowNumber ?? 0)")
            }
        }
    }

    private static func add(_ main: MainWindowController) {
        let contacts: AppContacts = main.state.contacts
        contacts.refresh()
        later(0.6) {
            guard let anchor = main.window?.contentView else { return }
            let a = Address(name: "Petr Svoboda", address: "petr.svoboda@example.com")
            AddToContactsPopover.show(address: a, account: SampleContactsCaller.work, contacts: contacts, relativeTo: anchor)
            later(0.5) {
                guard let pw = NSApp.windows.first(where: { $0.contentViewController is AddToContactsPopover }) else {
                    say("LAYOUT: contacts:add no popover")
                    return
                }
                say("LAYOUT: contacts:add popover \(NSStringFromRect(pw.frame))")
                for f in all(NSTextField.self, under: pw.contentView!) where !f.stringValue.isEmpty {
                    say("LAYOUT: contacts:add text '\(f.stringValue)' \(r(f))")
                }
                for p in all(NSPopUpButton.self, under: pw.contentView!) {
                    say("LAYOUT: contacts:add book '\(p.titleOfSelectedItem ?? "")' items \(p.numberOfItems)")
                }
                say("LAYOUT: shot contacts:add window \(pw.windowNumber)")
            }
        }
    }

    private static func prefs(_ main: MainWindowController) {
        let prefs = Integration.showPreferences(main.state)
        prefs.select(.contacts)
        guard let w = prefs.window else {
            say("LAYOUT: prefs-contacts no Settings window")
            return
        }
        later(1.0) {
            let pane = prefs.contactsPane
            say("LAYOUT: prefs-contacts window \(w.windowNumber) frame \(NSStringFromRect(w.frame)) sections \(pane.sectionViews.count)")
            for s in pane.sectionViews {
                let texts = all(NSTextField.self, under: s.view).map(\.stringValue).filter { !$0.isEmpty }
                say("LAYOUT: prefs-contacts section '\(s.name)' \(r(s.view)) texts \(texts)")
            }
            say("LAYOUT: shot prefs-contacts window \(w.windowNumber)")
        }
    }
}
