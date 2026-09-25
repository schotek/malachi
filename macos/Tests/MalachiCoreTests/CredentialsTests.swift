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
        #expect(Credentials().description == "Credentials(password: nil, oauthSession: nil)")
        // Still encodes for the daemon.
        let data = try? JSONEncoder().encode(c)
        #expect(data.map { String(decoding: $0, as: UTF8.self) }?.contains("hunter2") == true)
    }

    @Test func printingHidesTheSession() {
        let c = Credentials(oauthSession: "s_secret42")
        for text in [c.description, c.debugDescription, String(describing: c), String(reflecting: c), "\(c)"] {
            #expect(!text.contains("s_secret42"), "leaked in \(text)")
            #expect(text.contains("oauthSession: <set>"))
        }
        #expect(c.description == "Credentials(password: nil, oauthSession: <set>)")
        let data = try? JSONEncoder().encode(c)
        #expect(data.map { String(decoding: $0, as: UTF8.self) }?.contains("s_secret42") == true)
    }
}
