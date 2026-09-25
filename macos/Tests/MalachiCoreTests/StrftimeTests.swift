// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// 2026-09-02 15:04 in Prague, a Wednesday.
private let prague = TimeZone(identifier: "Europe/Prague")!
private let fixed: Date = {
    var cal = Calendar(identifier: .gregorian)
    cal.timeZone = prague
    return cal.date(from: DateComponents(year: 2026, month: 9, day: 2, hour: 15, minute: 4))!
}()

private func render(_ strftime: String, _ locale: String) -> String {
    let f = Strftime.formatter(strftime, locale: Locale(identifier: locale))
    f.timeZone = prague
    return f.string(from: fixed)
}

struct StrftimeTests {
    @Test(arguments: [
        ("%H:%M", "HH':'mm"),
        ("%-d %b", "d' 'MMM"),
        ("%Y-%m-%d", "yyyy'-'MM'-'dd"),
        ("%a, %-d %b %Y at %H:%M", "EEE', 'd' 'MMM' 'yyyy' at 'HH':'mm"),
        ("%a %-d. %-m. %Y v %H:%M", "EEE' 'd'. 'M'. 'yyyy' v 'HH':'mm"),
        ("%A %B %e %I:%M %p", "EEEE' 'MMMM' 'd' 'hh':'mm' 'a"),
        ("%-I%p", "ha"),
        ("100%% at %-H", "'100% at 'H"),
        ("o'clock %H", "'o''clock 'HH"),
        ("%y %S %-S %-M", "yy' 'ss' 's' 'm"),
        ("%Q", "'%Q'"),
        ("plain", "'plain'"),
        ("", ""),
    ])
    func toDateFormat(_ strftime: String, _ want: String) {
        #expect(Strftime.toDateFormat(strftime) == want)
    }

    // The four msgids of ui/internal/widget/format.go, in English.
    @Test func englishMsgids() {
        #expect(render("%H:%M", "en_US") == "15:04")
        #expect(render("%-d %b", "en_US") == "2 Sep")
        #expect(render("%Y-%m-%d", "en_US") == "2026-09-02")
        #expect(render("%a, %-d %b %Y at %H:%M", "en_US") == "Wed, 2 Sep 2026 at 15:04")
    }

    // The Czech translations of po/cs.po.
    @Test func czechMsgids() {
        #expect(render("%H:%M", "cs_CZ") == "15:04")
        #expect(render("%-d. %-m.", "cs_CZ") == "2. 9.")
        #expect(render("%-d. %-m. %Y", "cs_CZ") == "2. 9. 2026")
        #expect(render("%a %-d. %-m. %Y v %H:%M", "cs_CZ") == "st 2. 9. 2026 v 15:04")
    }

    @Test func literalsAreNeverFields() {
        // "at" and "v" must not be read as pattern letters.
        #expect(render("at %H", "en_US") == "at 15")
        #expect(render("v %H", "cs_CZ") == "v 15")
        #expect(render("100%%", "en_US") == "100%")
    }

    @Test func twelveHourClock() {
        #expect(render("%I:%M %p", "en_US") == "03:04 PM")
        #expect(render("%-I %p", "en_US") == "3 PM")
        #expect(render("%d/%m/%y", "en_US") == "02/09/26")
    }
}
