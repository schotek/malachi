// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// Translates the strftime patterns the GTK UI keeps as msgids
/// (ui/internal/widget/format.go: `%H:%M`, `%-d %b`, `%Y-%m-%d`,
/// `%a, %-d %b %Y at %H:%M`, and their translations) into the Unicode date
/// format patterns `DateFormatter` uses. Literal text is always quoted, so a
/// translator's "at"/"v"/"o'clock" can never be mistaken for a field.
public enum Strftime {
    public static func toDateFormat(_ strftime: String) -> String {
        var out = ""
        var literal = ""
        func flush() {
            guard !literal.isEmpty else { return }
            out.append("'")
            out.append(literal.replacingOccurrences(of: "'", with: "''"))
            out.append("'")
            literal = ""
        }
        var it = strftime.makeIterator()
        while let c = it.next() {
            guard c == "%" else {
                literal.append(c)
                continue
            }
            guard var d = it.next() else {
                literal.append("%")
                break
            }
            var noPad = false
            if d == "-" {
                noPad = true
                guard let next = it.next() else {
                    literal.append("%-")
                    break
                }
                d = next
            }
            if let field = field(d, noPad: noPad) {
                flush()
                out.append(field)
            } else if d == "%" {
                literal.append("%")
            } else {
                // Not a directive we know: keep it as text rather than drop it.
                literal.append(noPad ? "%-\(d)" : "%\(d)")
            }
        }
        flush()
        return out
    }

    /// A formatter for `strftime` in `locale`, with the current time zone.
    public static func formatter(_ strftime: String, locale: Locale) -> DateFormatter {
        let f = DateFormatter()
        f.locale = locale
        f.dateFormat = toDateFormat(strftime)
        return f
    }

    private static func field(_ d: Character, noPad: Bool) -> String? {
        switch d {
        case "a": return "EEE"
        case "A": return "EEEE"
        case "b", "h": return "MMM"
        case "B": return "MMMM"
        case "d": return noPad ? "d" : "dd"
        case "e": return "d"
        case "m": return noPad ? "M" : "MM"
        case "Y": return "yyyy"
        case "y": return "yy"
        case "H": return noPad ? "H" : "HH"
        case "I": return noPad ? "h" : "hh"
        case "M": return noPad ? "m" : "mm"
        case "S": return noPad ? "s" : "ss"
        case "p": return "a"
        case "Z": return "zzz"
        case "z": return "Z"
        default: return nil
        }
    }
}
