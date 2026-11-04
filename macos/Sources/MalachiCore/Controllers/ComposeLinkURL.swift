// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// compose.go `insertLink`: what the link popover accepts. Go runs
/// `url.Parse` and requires an http, https or mailto scheme; the same
/// reading of the string (`URLSyntax`) is applied here so a link the GTK UI
/// refuses is refused too. The result is the string handed to `createLink`:
/// the scheme lower-cased as `url.Parse` stores it, the rest as typed
/// (Go's `URL.String()` would also percent-escape a path; the editor copes
/// with the raw form). nil when refused.
public func composeLinkURL(_ raw: String) -> String? {
    let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
    let bytes = Array(trimmed.utf8)[...]
    let (u, fragment, hasFragment) = URLSyntax.cut(bytes, UInt8(ascii: "#"))
    guard !URLSyntax.hasControlByte(u), let (scheme, rest) = URLSyntax.scheme(of: u),
          URLSyntax.parseRest(rest, scheme: scheme) != nil else {
        return nil
    }
    if !fragment.isEmpty, URLSyntax.unescape(fragment, mode: .path) == nil {
        return nil
    }
    guard scheme == "http" || scheme == "https" || scheme == "mailto" else {
        return nil
    }
    var out = scheme + ":" + String(decoding: rest, as: UTF8.self)
    if hasFragment {
        out += "#" + String(decoding: fragment, as: UTF8.self)
    }
    return out
}
