// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Testing
@testable import MalachiCore

/// The rule `ChatGPTKeychain` uses to stay away from the login keychain in
/// an isolated instance. Pure environment logic; no keychain access.
@Suite struct PathsIsolationTests {
    @Test func dataDirMakesTheInstanceIsolated() {
        #expect(Paths.isIsolated(environment: ["MALACHI_DATA_DIR": "/tmp/x"]))
    }

    @Test func noOrEmptyDataDirIsTheDailyApp() {
        #expect(!Paths.isIsolated(environment: [:]))
        #expect(!Paths.isIsolated(environment: ["MALACHI_DATA_DIR": ""]))
        #expect(!Paths.isIsolated(environment: ["MALACHI_SOCKET": "/tmp/s"]))
    }
}
