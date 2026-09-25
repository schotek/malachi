// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// Caps the notification body in bytes; subjects are hostile input and
/// notification servers do not need a novel (ui/internal/window/notify.go
/// `notificationBodyMax`). The title (a sender's display name, hostile
/// too) is capped the same way.
public let notificationBodyMax = 200

/// The plain-text title and body of a new-message notification (notify.go
/// `notificationText`). Notification bodies are not markup, but the text is
/// still attacker-controlled: both lines are trimmed and capped.
public func notificationText(_ n: NewMessageNotification) -> (title: String, body: String) {
    var title = L10n.T("New message")
    if let first = n.message.from.first {
        let name = capped(displayName(first))
        if !name.isEmpty {
            title = name
        }
    }
    var body = n.message.subject.trimmingCharacters(in: .whitespacesAndNewlines)
    if body.isEmpty {
        body = L10n.T("(No subject)")
    }
    return (title, capped(body))
}

/// `s` cut to `notificationBodyMax` bytes on a character boundary, with an
/// ellipsis when anything was cut.
private func capped(_ s: String) -> String {
    guard s.utf8.count > notificationBodyMax else {
        return s
    }
    return validPrefix(s, bytes: notificationBodyMax) + "\u{2026}"
}

/// The first `bytes` bytes of `s` as a string, minus a UTF-8 sequence cut
/// in the middle (Go: `strings.ToValidUTF8(s[:n], "")`).
private func validPrefix(_ s: String, bytes: Int) -> String {
    var prefix = Array(s.utf8.prefix(bytes))
    // At most three continuation bytes can trail a broken sequence.
    for _ in 0..<4 {
        if let str = String(bytes: prefix, encoding: .utf8) {
            return str
        }
        guard !prefix.isEmpty else { break }
        prefix.removeLast()
    }
    return ""
}
