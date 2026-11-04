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
            if d == "O" {
                // %OB: the standalone month name (the nominative in Czech,
                // "říjen 2026"), DateFormatter's LLLL.
                guard let next = it.next() else {
                    literal.append("%O")
                    break
                }
                if next == "B" {
                    flush()
                    out.append("LLLL")
                } else {
                    literal.append("%O\(next)")
                }
                continue
            }
            var noPad = false
            var consumed = "%"
            if d == "-" {
                noPad = true
                consumed += "-"
                guard let next = it.next() else {
                    literal.append(consumed)
                    break
                }
                d = next
            }
            // A C length modifier ("%ld", "%-lld") that a catalogue
            // generator may have added: the directive is the letter after it.
            var lengths = 0
            var ended = false
            while d == "l" && lengths < 2 {
                lengths += 1
                consumed += "l"
                guard let next = it.next() else {
                    ended = true
                    break
                }
                d = next
            }
            if ended {
                literal.append(consumed)
                break
            }
            if let field = field(d, noPad: noPad) {
                flush()
                out.append(field)
            } else if d == "%" {
                literal.append("%")
            } else {
                // Not a directive we know: keep it as text rather than drop it.
                literal.append(consumed + String(d))
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
