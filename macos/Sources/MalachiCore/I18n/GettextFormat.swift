// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// Converts the printf-style formats of the gettext catalogues (the GTK UI
/// is Go, its msgids use `%s` and `%d`) to what `String(format:)` expects.
///
/// `%s` becomes `%@` and `%d` becomes `%ld`, positional forms included
/// (`%2$s` → `%2$@`); flags, width and precision are kept. Everything else
/// (`%f`, `%.1f`, `%%`, and the Foundation forms `%@`, `%ld` themselves) is
/// left alone, so the conversion is idempotent: applying it to an already
/// converted string is harmless. The same rules live in
/// macos/scripts/po2strings.py, which converts catalogue values at build
/// time; the Swift side converts only the English fallback.
public enum GettextFormat {
    public static func toFoundation(_ s: String) -> String {
        guard s.contains("%") else { return s }
        var out = ""
        out.reserveCapacity(s.utf8.count + 8)
        let chars = Array(s)
        var i = 0
        while i < chars.count {
            let c = chars[i]
            guard c == "%" else {
                out.append(c)
                i += 1
                continue
            }
            // %[N$][flags][width][.precision][length]conversion
            var j = i + 1
            var prefix = "%"
            // Positional argument: digits followed by '$'.
            var k = j
            while k < chars.count, chars[k].isASCII, chars[k].isNumber { k += 1 }
            if k > j, k < chars.count, chars[k] == "$" {
                prefix.append(contentsOf: chars[j...k])
                j = k + 1
            }
            while j < chars.count, "-+ 0#'".contains(chars[j]) {
                prefix.append(chars[j])
                j += 1
            }
            while j < chars.count, (chars[j].isASCII && chars[j].isNumber) || chars[j] == "*" {
                prefix.append(chars[j])
                j += 1
            }
            if j < chars.count, chars[j] == "." {
                prefix.append(".")
                j += 1
                while j < chars.count, chars[j].isASCII, chars[j].isNumber {
                    prefix.append(chars[j])
                    j += 1
                }
            }
            var hasLength = false
            while j < chars.count, "hlqLzjt".contains(chars[j]) {
                prefix.append(chars[j])
                hasLength = true
                j += 1
            }
            guard j < chars.count else {
                // A dangling '%' at the end: copy verbatim.
                out.append(contentsOf: chars[i...])
                break
            }
            let conv = chars[j]
            switch (hasLength, conv) {
            case (false, "s"):
                out.append(prefix + "@")
            case (false, "d"):
                out.append(prefix + "ld")
            default:
                out.append(contentsOf: chars[i...j])
            }
            i = j + 1
        }
        return out
    }
}
