// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// `Credentials` is write-only (docs/security.md §6): printing one never
/// shows the password.
@Suite struct CredentialsTests {
    @Test func printingRedacts() {
        let c = Credentials(password: "hunter2")
        for text in [c.description, c.debugDescription, String(describing: c), String(reflecting: c), "\(c)"] {
            #expect(!text.contains("hunter2"), "leaked in \(text)")
            #expect(text.contains("<redacted>"))
        }
        #expect(Credentials().description == "Credentials(password: nil)")
        // Still encodes for the daemon.
        let data = try? JSONEncoder().encode(c)
        #expect(data.map { String(decoding: $0, as: UTF8.self) }?.contains("hunter2") == true)
    }
}
