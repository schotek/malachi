// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// ui/internal/accountwizard/fields_test.go.
struct WizardFieldsTests {
    @Test(arguments: [
        (Endpoint.imap, Security.tls, 993), (.imap, .starttls, 143), (.imap, .none, 143),
        (.smtp, .tls, 465), (.smtp, .starttls, 587), (.smtp, .none, 587),
    ])
    func defaultPorts(_ kind: Endpoint, _ security: Security, _ want: Int) {
        #expect(defaultPort(kind, security) == want)
    }

    @Test func portForSecurityChanges() {
        #expect(portForSecurityChange(.imap, port: 993, from: .tls, to: .starttls) == 143, "default swap")
        #expect(portForSecurityChange(.imap, port: 9993, from: .tls, to: .starttls) == 9993, "custom kept")
        #expect(portForSecurityChange(.smtp, port: 587, from: .starttls, to: .starttls) == 587, "same mode")
        #expect(portForSecurityChange(.smtp, port: 0, from: .tls, to: .none) == 587, "zero port")
    }

    @Test func securityChoicesRoundTrip() {
        #expect(securityChoices == [.tls, .starttls, .none])
        for (i, s) in securityChoices.enumerated() {
            #expect(indexOfSecurity(s) == i)
            #expect(securityAt(i) == s)
        }
        #expect(securityAt(99) == .tls)
        #expect(securityAt(-1) == .tls)
        #expect(indexOfSecurity(Security(rawValue: "bogus")) == 0)
    }

    @Test func validateEmails() {
        let cases: [String: Bool] = [
            "me@example.org": true, "  me@example.org ": true,
            "Name <me@example.org>": false, "me@": false, "@example.org": false, "": false, "a b@example.org": false,
            "<me@example.org>": false,
        ]
        for (input, ok) in cases {
            #expect((validateEmail(input) != nil) == ok, "ValidateEmail(\(input))")
        }
        #expect(validateEmail("  me@example.org ") == "me@example.org")
    }

    @Test func guessAndMerge() throws {
        let g = guessConfig("me@Example.org")
        let imap = try #require(g.imap)
        let smtp = try #require(g.smtp)
        #expect(imap.host == "imap.example.org")
        #expect(imap.port == 993)
        #expect(imap.security == .tls)
        #expect(smtp.host == "smtp.example.org")
        #expect(smtp.port == 587)
        #expect(smtp.security == .starttls)
        #expect(imap.username == "me@Example.org")
        #expect(smtp.authMethod == .password)
        #expect(g.kind == .imap)
        #expect(g.email == "me@Example.org")
        #expect(suggestAccountName("me@Example.org") == "example.org")
        #expect(suggestAccountName("nope") == "nope")
        #expect(domain("me@") == "")
        #expect(domain("a@b@C.example") == "c.example")

        let discovered = AccountConfig(
            name: "  ", email: "",
            imap: ServerConfig(host: "imap.x.org", port: 993, security: .tls, username: "", authMethod: .oauth2),
            smtp: ServerConfig(host: "smtp.x.org", port: 587, security: .starttls, username: "custom", authMethod: .oauth2),
            graph: GraphConfig(source: .goa, goaAccountId: "x")
        )
        let m = mergeIdentity(discovered, Identity(displayName: " Me ", email: "me@x.org", password: "p"))
        #expect(m.name == "x.org")
        #expect(m.email == "me@x.org")
        #expect(m.displayName == "Me")
        #expect(m.kind == .imap)
        #expect(m.graph == nil)
        #expect(m.imap?.username == "me@x.org")
        #expect(m.imap?.host == "imap.x.org")
        #expect(m.smtp?.username == "custom")
        #expect(m.imap?.authMethod == .password)
        #expect(m.smtp?.authMethod == .password)
        // The input is not modified, and missing endpoints come from the guess.
        #expect(discovered.imap?.username == "")
        let filled = mergeIdentity(AccountConfig(name: "Named", email: ""), Identity(email: "me@x.org"))
        #expect(filled.name == "Named")
        #expect(filled.displayName == nil)
        #expect(filled.imap?.host == "imap.x.org")
        #expect(filled.smtp?.host == "smtp.x.org")
        #expect(filled.smtp?.username == "me@x.org")
    }

    @Test func validateAndBuild() throws {
        let p = validateIdentity(Identity(email: "bad", password: ""), passwordRequired: true)
        #expect(p.email)
        #expect(p.password)
        #expect(p.any)
        #expect(!validateIdentity(Identity(email: "me@x.org", password: "p"), passwordRequired: true).any)
        #expect(!validateIdentity(Identity(email: "me@x.org"), passwordRequired: false).any)

        let sp = validateServers(imap: ServerFields(host: " ", username: "u"), smtp: ServerFields(host: "smtp.x.org", username: ""))
        #expect(sp.imapHost)
        #expect(!sp.imapUser)
        #expect(!sp.smtpHost)
        #expect(sp.smtpUser)
        #expect(sp.any)
        #expect(!ServerProblems().any)

        let cfg = buildConfig(
            identity: Identity(displayName: "Me", email: " me@x.org ", password: "p"), name: "",
            imap: ServerFields(host: " imap.x.org ", port: 993, security: .tls, username: "me@x.org"),
            smtp: ServerFields(host: "smtp.x.org", port: 587, security: .starttls, username: "me@x.org")
        )
        #expect(cfg.name == "x.org")
        #expect(cfg.email == "me@x.org")
        #expect(cfg.displayName == "Me")
        #expect(cfg.kind == .imap)
        #expect(cfg.imap?.host == "imap.x.org")
        #expect(cfg.imap?.authMethod == .password)
        #expect(cfg.smtp?.port == 587)
        #expect(cfg.smtp?.authMethod == .password)
        #expect(cfg.oauth2 == nil)
        #expect(cfg.graph == nil)
        #expect(credentialsFor(Identity(password: "p")).password == "p")
        #expect(credentialsFor(Identity()).password == nil)
    }

    @Test func linkedConfigAndMatch() {
        let graph = AccountConfig(name: "Me@Contoso.example", email: "Me@Contoso.example", kind: .graph,
                                  graph: GraphConfig(source: .goa, goaAccountId: "account_1_0"))
        let google = AccountConfig(
            name: "Work", email: "me@gmail.example",
            imap: ServerConfig(host: "imap.gmail.example", port: 993, security: .tls, username: "me@gmail.example", authMethod: .oauth2),
            smtp: ServerConfig(host: "smtp.gmail.example", port: 465, security: .tls, username: "me@gmail.example", authMethod: .oauth2),
            oauth2: OAuth2Config(source: .goa, goaAccountId: "account_2_0", provider: .google)
        )
        let hint = AccountConfig(name: "", email: "me@gmail.example", oauth2: OAuth2Config(source: .goa, provider: .google))
        #expect(linkedAccountID(graph) == "account_1_0")
        #expect(linkedAccountID(google) == "account_2_0")
        #expect(linkedAccountID(hint) == nil)
        #expect(linkedAccountID(AccountConfig(name: "", email: "")) == nil)
        #expect(linkedAccountID(AccountConfig(name: "", email: "", graph: GraphConfig(source: .goa, goaAccountId: ""))) == nil)
        #expect(linkedAccountID(AccountConfig(name: "", email: "", oauth2: OAuth2Config(goaAccountId: "x", provider: .google))) == nil)

        // The identity page adds the display name; a name the daemon left at
        // the address becomes the suggested one, a chosen name stays.
        let cfg = withIdentity(graph, Identity(displayName: " Me ", email: " Me@Contoso.example ", password: "ignored"))
        #expect(cfg.name == "contoso.example")
        #expect(cfg.displayName == "Me")
        #expect(cfg.email == "Me@Contoso.example")
        #expect(cfg.graph?.goaAccountId == "account_1_0")
        let named = withIdentity(google, Identity())
        #expect(named.name == "Work")
        #expect(named.displayName == nil)

        let linked = [LinkedAccount(provider: .microsoft365, email: "Me@Contoso.example", goaAccountId: "account_1_0", configured: false, attentionNeeded: false)]
        #expect(linkedMatch(linked, email: " me@contoso.EXAMPLE ")?.goaAccountId == "account_1_0")
        #expect(linkedMatch(linked, email: "other@contoso.example") == nil)

        #expect(goaHintText(providerName: "Google").hasPrefix("This address belongs to a Google account."))
        #expect(goaHintText(providerName: nil).hasPrefix("This address belongs to a Microsoft 365 account."))
        #expect(goaHintText(providerName: "").contains("Microsoft 365"))
    }
}
