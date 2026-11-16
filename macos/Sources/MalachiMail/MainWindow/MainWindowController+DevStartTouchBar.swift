// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

// DEVELOPMENT AID, not a feature (MALACHI_START, see
// MainWindowController+DevStart.swift): the Touch Bar's step
// (docs/plans/touch-bar.md §7). `touchbar` prints the main window's bar as
// the window's mode and selection make it, one line per item (`LAYOUT:
// touchbar <identifier> enabled <0|1> title '…' label '…' action …`, the
// groups' buttons indented, the mode switch's segment). It asks the window
// for the bar, so it works on a Mac without a Touch Bar too: what it shows
// is the bar's content, not how the hardware draws it. Combine it with
// `mail`, `board:list`, `calendar:week`, `folder=` and `row=` steps to
// walk the states of §4.1.
@MainActor
enum DevelopmentTouchBar {
    static func step(_ step: String, in main: MainWindowController) -> Bool {
        guard step == "touchbar" else { return false }
        // A step that changes the board's style or selection from code
        // sends the window no event, so no `windowDidUpdate`: the bar is
        // brought in step before it is printed.
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
            MainActor.assumeIsolated {
                main.refreshTouchBar()
                guard let bar = main.touchBar else {
                    say("LAYOUT: touchbar none")
                    return
                }
                for line in main.touchBars.describe(bar) {
                    say("LAYOUT: touchbar \(line)")
                }
            }
        }
        return true
    }

    private static func say(_ line: String) {
        FileHandle.standardError.write(Data((line + "\n").utf8))
    }
}
