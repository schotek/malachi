// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

// DEVELOPMENT AID, not a feature (MALACHI_START, see
// MainWindowController+DevStart.swift): the toolbar customization's steps
// (docs/plans/toolbar-customization.md, M8). They act on the toolbar the
// window shows (Mail, the Board's List or Columns/Today, the Calendar), so
// a `mail`, `board:list`, `board:columns` or `calendar:week` step comes
// first. Only from macOS 15; nothing on macOS 14.
//
//   `toolbar=<tokens>` stores an arrangement (comma-separated model tokens,
//   `|list` and so on for the separators, `space`, `flexibleSpace`) for the
//   shown toolbar; the toolbar is built again from it
//   (`ToolbarCustomization.reloadIfChanged`). `toolbar=` stores the default.
//   `toolbar-display=<icon|icon-and-label|label>` sets the display mode of
//   every toolbar.
//   `toolbar-palette` opens AppKit's customization palette on the window;
//   `toolbar-palette-end` ends its sheet, as Done does, and the window
//   saves what the toolbar holds.
// Each dumps the toolbar's items (`LAYOUT: toolbar item <id> <frame>
// hidden <n>`), its separators' places, the stored arrangement and the
// display mode.
@MainActor
enum DevelopmentToolbar {
    static func step(_ step: String, in main: MainWindowController) -> Bool {
        if step.hasPrefix("toolbar=") {
            arrange(String(step.dropFirst("toolbar=".count)), main)
        } else if step.hasPrefix("toolbar-display=") {
            main.state.settings.toolbarDisplayMode = Toolbar.parseDisplayMode(String(step.dropFirst("toolbar-display=".count)))
            later { dump("toolbar-display", main) }
        } else if step == "toolbar-palette" {
            main.window?.runToolbarCustomizationPalette(nil)
            later { dump("toolbar-palette", main) }
        } else if step == "toolbar-palette-end" {
            if let sheet = main.window?.attachedSheet, let content = sheet.contentView {
                // The palette's texts with their frames (the default set's row
                // squeezes items to its width).
                func texts(_ v: NSView) {
                    if let t = v as? NSTextField, !t.stringValue.isEmpty, !t.isHiddenOrHasHiddenAncestor {
                        let r = t.convert(t.bounds, to: nil)
                        say(String(format: "LAYOUT: palette text '%@' x %.0f y %.0f w %.0f fits %d", t.stringValue,
                                   r.minX, r.minY, r.width, t.intrinsicContentSize.width <= r.width + 0.5 ? 1 : 0))
                    }
                    v.subviews.forEach(texts)
                }
                texts(content)
                // The default set's row: every view below its caption.
                let caption = content.frame.height * 0.45
                func row(_ v: NSView, _ depth: Int) {
                    let r = v.convert(v.bounds, to: nil)
                    if r.maxY < caption, r.minY > 50, depth > 0 {
                        var text = ""
                        if let b = v as? NSButton { text = b.title }
                        if let t = v as? NSTextField { text = t.stringValue }
                        if let l = v.layer?.sublayers?.compactMap({ ($0 as? CATextLayer)?.string as? String }).first { text += l }
                        let label = (v.value(forKey: "accessibilityLabel") as? String) ?? ""
                        say(String(format: "LAYOUT: palette row %@ x %.0f..%.0f y %.0f text '%@' ax '%@'",
                                   String(describing: type(of: v)), r.minX, r.maxX, r.minY, text, label))
                        // The default set is one drawn image: written out
                        // to look at (MALACHI_START_TOOLBAR_SHOT=<png path>).
                        if let path = ProcessInfo.processInfo.environment["MALACHI_START_TOOLBAR_SHOT"],
                           label == "default set of toolbar items",
                           let rep = v.bitmapImageRepForCachingDisplay(in: v.bounds)
                        {
                            v.cacheDisplay(in: v.bounds, to: rep)
                            try? rep.representation(using: .png, properties: [:])?.write(to: URL(fileURLWithPath: path))
                            say("LAYOUT: palette row written to \(path)")
                        }
                    }
                    if depth < 12 {
                        v.subviews.forEach { row($0, depth + 1) }
                    }
                }
                row(content, 0)
            }
            if let window = main.window, let sheet = window.attachedSheet {
                window.endSheet(sheet)
            } else {
                say("LAYOUT: toolbar-palette-end: no sheet")
            }
            later { dump("toolbar-palette-end", main) }
        } else {
            return false
        }
        return true
    }

    private static func arrange(_ spec: String, _ main: MainWindowController) {
        guard let c = main.currentToolbarCustomization else {
            say("LAYOUT: toolbar= no customizable toolbar in front")
            return
        }
        let tokens = spec.split(separator: ",").map { Toolbar.ID(String($0)) }
        main.state.settings.setToolbarLayout(c.bar, tokens.isEmpty ? Toolbar.defaultLayout(c.bar) : tokens)
        later { dump("toolbar=", main) }
    }

    /// After AppKit laid the toolbar out.
    private static func later(_ f: @escaping @MainActor () -> Void) {
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
            MainActor.assumeIsolated(f)
        }
    }

    private static func dump(_ step: String, _ main: MainWindowController) {
        guard let window = main.window, let tb = window.toolbar else { return }
        let c = main.currentToolbarCustomization
        say("LAYOUT: \(step) toolbar \(tb.identifier) customizable \(tb.allowsUserCustomization ? 1 : 0)"
            + " palette \(tb.customizationPaletteIsRunning ? 1 : 0) display \(tb.displayMode.rawValue)"
            + " stored-display \(main.state.settings.toolbarDisplayMode.rawValue)")
        if let c {
            say("LAYOUT: \(step) stored \(Toolbar.tokens(main.state.settings.toolbarLayout(c.bar)).joined(separator: ","))")
        }
        for item in tb.items {
            let frame = item.view.map { v in
                v.window == nil ? "off-window" : NSStringFromRect(v.convert(v.bounds, to: nil))
            } ?? "no view"
            var hidden = 0
            if #available(macOS 15, *) {
                hidden = item.isHidden ? 1 : 0
            }
            let tracking = item is NSTrackingSeparatorToolbarItem ? " (tracking separator)" : ""
            say("LAYOUT: toolbar item \(item.itemIdentifier.rawValue) \(frame) visible \(item.isVisible ? 1 : 0)"
                + " hidden \(hidden)\(tracking)")
        }
    }

    private static func say(_ line: String) {
        FileHandle.standardError.write(Data((line + "\n").utf8))
    }
}
