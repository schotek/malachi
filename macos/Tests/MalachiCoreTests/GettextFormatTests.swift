// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

struct GettextFormatTests {
    @Test(arguments: [
        ("%s", "%@"),
        ("%d", "%ld"),
        ("%1$s and %2$d", "%1$@ and %2$ld"),
        ("Connected to malachid %s (pid %d)", "Connected to malachid %@ (pid %ld)"),
        ("Protocol mismatch: UI %d, backend %d", "Protocol mismatch: UI %ld, backend %ld"),
        ("%.1f MiB", "%.1f MiB"),
        ("%.0f KiB", "%.0f KiB"),
        ("%f", "%f"),
        ("100%%", "100%%"),
        ("%@", "%@"),
        ("%ld", "%ld"),
        ("%5d|%-3s", "%5ld|%-3@"),
        ("no directives", "no directives"),
        ("trailing %", "trailing %"),
        ("", ""),
    ])
    func converts(_ src: String, _ want: String) {
        #expect(GettextFormat.toFoundation(src) == want)
    }

    @Test func isIdempotent() {
        let once = GettextFormat.toFoundation("%s %d %1$s %2$d %.1f %% %@ %ld")
        #expect(once == "%@ %ld %1$@ %2$ld %.1f %% %@ %ld")
        #expect(GettextFormat.toFoundation(once) == once)
    }

    @Test func convertedPatternsFormat() {
        let s = String(format: GettextFormat.toFoundation("Connected to malachid %s (pid %d)"), arguments: ["1.2", 42])
        #expect(s == "Connected to malachid 1.2 (pid 42)")
        let positional = String(format: GettextFormat.toFoundation("%2$s then %1$d"), arguments: [7, "x"])
        #expect(positional == "x then 7")
    }
}
