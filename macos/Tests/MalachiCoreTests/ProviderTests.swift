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
        let daemonGoogle = AccountConfig(
            name: "", email: "", imap: oauth, smtp: oauth, oauth2: OAuth2Config(source: .daemon, provider: .google))
        let daemonGraph = AccountConfig(
            name: "", email: "", kind: .graph, oauth2: OAuth2Config(source: .daemon, provider: .office365),
            graph: GraphConfig(source: .daemon))
        let bareGraph = AccountConfig(name: "", email: "", kind: .graph)
        let daemonOffice = AccountConfig(
            name: "", email: "", imap: oauth, smtp: oauth, oauth2: OAuth2Config(source: .daemon, provider: .office365))
        let own = AccountConfig(name: "", email: "", imap: oauth, oauth2: OAuth2Config(provider: .office365))
        let password = AccountConfig(
            name: "", email: "", imap: ServerConfig(host: "", port: 0, security: .tls, username: "", authMethod: .password))
        let cases: [(String, AccountConfig, LinkedProvider?, SignInKind)] = [
            ("graph", graph, .microsoft365, .goa),
            ("google", google, .google, .goa),
            ("daemon google", daemonGoogle, .google, .oauth),
            ("daemon graph", daemonGraph, .microsoft365, .oauth),
            ("graph without a source", bareGraph, .microsoft365, .oauth),
            ("daemon office365 over imap", daemonOffice, .microsoft365, .oauth),
            ("oauth2 block without a source", own, .microsoft365, .oauth),
            ("password", password, nil, .password),
        ]
        for (name, cfg, provider, kind) in cases {
            #expect(accountProvider(cfg) == provider, Comment(rawValue: name))
            #expect(signInKind(cfg) == kind, Comment(rawValue: name))
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
