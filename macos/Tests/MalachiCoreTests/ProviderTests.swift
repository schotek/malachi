// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// The counterpart of ui/internal/widget/provider_test.go.
@Suite struct ProviderTests {
    @Test func accountProviderTest() {
        let graph = AccountConfig(name: "", email: "", kind: .graph, graph: GraphConfig(source: .goa, goaAccountId: "account_1_0"))
        let oauth = ServerConfig(host: "", port: 0, security: .tls, username: "", authMethod: .oauth2)
        let google = AccountConfig(
            name: "", email: "", imap: oauth, smtp: oauth,
            oauth2: OAuth2Config(source: .goa, goaAccountId: "account_2_0", provider: .google)
        )
        let own = AccountConfig(name: "", email: "", imap: oauth, oauth2: OAuth2Config(provider: .office365))
        let password = AccountConfig(
            name: "", email: "", imap: ServerConfig(host: "", port: 0, security: .tls, username: "", authMethod: .password))
        let cases: [(String, AccountConfig, LinkedProvider?)] = [
            ("graph", graph, .microsoft365), ("google", google, .google), ("own flow", own, nil), ("password", password, nil),
        ]
        for (name, cfg, provider) in cases {
            #expect(accountProvider(cfg) == provider, Comment(rawValue: name))
            #expect(goaOwned(cfg) == (provider != nil), Comment(rawValue: name))
        }
        #expect(providerName(.microsoft365) == "Microsoft 365")
        #expect(providerName(.google) == "Google")
        #expect(providerName(nil) == "")
        #expect(providerIconName(.google) == "goa-account-google-symbolic")
        #expect(providerIconName(.microsoft365) == "goa-account-ms365-symbolic")
        #expect(providerIconName("x") == nil)
        #expect(providerIcon(nil) == "mail-unread-symbolic")
        #expect(providerIcon(.google) == "goa-account-google-symbolic")
    }
}
