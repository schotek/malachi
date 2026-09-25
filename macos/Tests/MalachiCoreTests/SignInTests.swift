// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// The counterpart of ui/internal/signin/signin_test.go and the text
/// helpers of ui/internal/accountwizard/oauth.go.
@Suite struct SignInTests {
    private static let oauthServer = ServerConfig(host: "imap.gmail.com", port: 993, security: .tls, username: "me@gmail.com", authMethod: .oauth2)
    private static let oauthSMTP = ServerConfig(host: "smtp.gmail.com", port: 465, security: .tls, username: "me@gmail.com", authMethod: .oauth2)
    private static let passwordIMAP = ServerConfig(host: "imap.gmail.com", port: 993, security: .tls, username: "me@gmail.com", authMethod: .password)
    private static let passwordSMTP = ServerConfig(host: "smtp.gmail.com", port: 465, security: .tls, username: "me@gmail.com", authMethod: .password)

    static let goaLinked = AccountConfig(
        name: "me@gmail.com", email: "me@gmail.com", imap: oauthServer, smtp: oauthSMTP,
        oauth2: OAuth2Config(source: .goa, goaAccountId: "account_1", provider: .google))
    static let goaHint = AccountConfig(
        name: "me@gmail.com", email: "me@gmail.com", imap: oauthServer, smtp: oauthSMTP,
        oauth2: OAuth2Config(source: .goa, provider: .google))
    static let daemonGoogle = AccountConfig(
        name: "me@gmail.com", email: "me@gmail.com", imap: oauthServer, smtp: oauthSMTP,
        oauth2: OAuth2Config(source: .daemon, provider: .google))
    static let appPassword = AccountConfig(name: "me@gmail.com", email: "me@gmail.com", imap: passwordIMAP, smtp: passwordSMTP)
    static let graphHint = AccountConfig(name: "me@contoso.com", email: "me@contoso.com", kind: .graph, graph: GraphConfig(source: .goa))
    static let daemonGraph = AccountConfig(
        name: "me@contoso.com", email: "me@contoso.com", kind: .graph,
        oauth2: OAuth2Config(source: .daemon, provider: .office365), graph: GraphConfig(source: .daemon))
    static let ispdb = AccountConfig(
        name: "example.org", email: "me@example.org",
        imap: ServerConfig(host: "imap.example.org", port: 993, security: .tls, username: "me@example.org", authMethod: .password),
        smtp: ServerConfig(host: "smtp.example.org", port: 587, security: .starttls, username: "me@example.org", authMethod: .password))

    private func discovered(_ cfg: AccountConfig?, _ alternatives: [AccountConfig] = []) -> Result<AccountDiscoverResult, any Error> {
        .success(AccountDiscoverResult(config: cfg, source: cfg == nil ? .none : .provider, alternatives: alternatives))
    }

    @Test func classifyDiscoveryTable() {
        // A half-password alternative is not an app-password account.
        var mixed = Self.appPassword
        mixed.smtp?.authMethod = .oauth2
        let cases: [(String, Result<AccountDiscoverResult, any Error>, Discovery)] = [
            ("failure", .failure(RPCError(code: .networkError, message: "x")), Discovery(path: .password)),
            ("nothing found", discovered(nil), Discovery(path: .password)),
            ("ispdb", discovered(Self.ispdb), Discovery(path: .password, config: Self.ispdb)),
            ("signed in through GNOME Online Accounts", discovered(Self.goaLinked, [Self.daemonGoogle]),
             Discovery(path: .goa, config: Self.goaLinked, provider: .google)),
            ("GNOME Online Accounts hint with both ways out", discovered(Self.goaHint, [Self.daemonGoogle, Self.appPassword]),
             Discovery(path: .goaHint, config: Self.goaHint, oauthAlt: Self.daemonGoogle, passwordAlt: Self.appPassword, provider: .google)),
            ("GNOME Online Accounts hint without alternatives", discovered(Self.graphHint),
             Discovery(path: .goaHint, config: Self.graphHint, provider: .microsoft365)),
            ("Microsoft 365 hint with the browser", discovered(Self.graphHint, [Self.daemonGraph]),
             Discovery(path: .goaHint, config: Self.graphHint, oauthAlt: Self.daemonGraph, provider: .microsoft365)),
            ("daemon Google with the app password", discovered(Self.daemonGoogle, [Self.appPassword]),
             Discovery(path: .oauth, config: Self.daemonGoogle, passwordAlt: Self.appPassword, provider: .google)),
            ("daemon Google, only a half-password alternative", discovered(Self.daemonGoogle, [mixed]),
             Discovery(path: .oauth, config: Self.daemonGoogle, provider: .google)),
            ("daemon Microsoft 365", discovered(Self.daemonGraph),
             Discovery(path: .oauth, config: Self.daemonGraph, provider: .microsoft365)),
        ]
        for (name, outcome, want) in cases {
            #expect(classifyDiscovery(outcome) == want, Comment(rawValue: name))
        }
    }

    @Test func providerLabels() {
        #expect(oauthProviderLabel(.google, email: "me@gmail.com") == "Google")
        #expect(oauthProviderLabel(.microsoft365, email: "me@contoso.com") == "Microsoft 365")
        #expect(oauthProviderLabel("yahoo", email: "me@yahoo.example") == "yahoo.example", "an unknown provider is named by the domain")
        #expect(oauthProviderLabel(nil, email: "Me@Mail.Example") == "mail.example")
    }

    @Test func promptTexts() {
        #expect(oauthPromptText("Google") == "Your browser will open so you can sign in to Google. Malachi Mail never sees your password; it only receives permission to read and send your mail.")
        #expect(oauthSignInLabel("Microsoft 365") == "_Sign In with Microsoft 365")
        #expect(oauthUnavailableText("Google") == "No OAuth client is configured for Google on this computer. Add a client ID to the mail backend's configuration and try again.")
    }

    @Test func errorTexts() {
        func e(_ code: ErrorCode, _ data: JSONValue? = nil) -> RPCError {
            RPCError(code: code, message: "technical", data: data)
        }
        #expect(oauthErrorText("Google", e(.cancelled)) == "The sign-in was cancelled")
        #expect(oauthErrorText("Microsoft 365", e(.authFailed)) == "The sign-in with Microsoft 365 was refused")
        #expect(oauthErrorText("Google", e(.serverTimeout)) == "The sign-in took too long; try again")
        #expect(oauthErrorText("Google", RPCClient.ClientError.timeout(method: "account.oauthWait")) == "The sign-in took too long; try again")
        #expect(oauthErrorText("Google", CancellationError()) == "The sign-in took too long; try again")
        #expect(oauthErrorText("Google", e(.invalidArgument, .object(["signedInAs": .string(" other@gmail.com ")])))
                == "The browser signed in to other@gmail.com, not to this address")
        #expect(oauthErrorText("Google", e(.invalidArgument)) == "Signing in was rejected: technical")
        // Control and format characters, by code point: a right-to-left
        // override, a zero-width space, a byte order mark, a soft hyphen and
        // a C1 control.
        func with(_ scalar: UInt32, _ before: String, _ after: String) -> JSONValue {
            .string(before + String(Character(Unicode.Scalar(scalar)!)) + after)
        }
        let rejected: [JSONValue] = [
            .string(""), .string("   "), .number(1), .string("a\u{7}b@example.org"),
            .string(String(repeating: "a", count: 250) + "@x.org"),
            with(0x202E, "a@b.example", "moc.elgoog"), with(0x200B, "", "a@b.example"),
            with(0xFEFF, "a@b.example", ""), with(0x00AD, "a", "b@c.example"), with(0x0085, "a", "b@c.example"),
        ]
        for bad in rejected {
            #expect(oauthErrorText("Google", e(.invalidArgument, .object(["signedInAs": bad]))) == "Signing in was rejected: technical", "\(bad)")
        }
        #expect(oauthErrorText("Google", e(.invalidArgument, .object(["signedInAs": with(0x00E9, "jan", "@b.example")])))
                == "The browser signed in to jan\u{E9}@b.example, not to this address")
        #expect(oauthErrorText("Google", e(.networkError)) == "Signing in failed: the server could not be reached")
        #expect(oauthErrorText("Google", RPCClient.ClientError.disconnected) == "Signing in needs a running mail backend")
    }

    @Test func browserPageTexts() {
        #expect(browserPage() == OAuthBrowserPage(
            successTitle: "Signed in", successText: "You can close this tab and return to Malachi Mail.",
            failureTitle: "Sign-in failed", failureText: "Return to Malachi Mail and try again."))
    }

    @Test func browserURLs() {
        let cases: [(String, Bool)] = [
            ("https://accounts.google.com/o/oauth2/v2/auth?client_id=x&state=y", true),
            ("https://login.microsoftonline.com/common/oauth2/v2.0/authorize?x=1", true),
            ("HTTPS://accounts.google.com/", true),
            ("http://accounts.google.com/", false),
            ("https:accounts.google.com", false),
            ("https://", false),
            ("https://user:pw@accounts.google.com/", false),
            ("https://user@accounts.google.com/", false),
            ("javascript:alert(1)", false),
            ("file:///etc/passwd", false),
            ("", false),
        ]
        for (url, want) in cases {
            #expect(isBrowserURL(url) == want, Comment(rawValue: url))
        }
        #expect(refusedBrowserURLText() == "The link could not be opened: not an https address")
    }

    @Test func signInProblems() {
        func endpoint(_ code: ErrorCode?) -> EndpointTestResult {
            EndpointTestResult(ok: code == nil, error: code.map { RPCError(code: $0, message: "x") }, latencyMs: 1)
        }
        let cases: [(String, Result<AccountTestResult, any Error>, Bool)] = [
            ("all fine", .success(AccountTestResult(imap: endpoint(nil), smtp: endpoint(nil))), false),
            ("imap refused the token", .success(AccountTestResult(imap: endpoint(.authFailed), smtp: endpoint(nil))), true),
            ("no sign-in stored", .success(AccountTestResult(graph: endpoint(.authRequired))), true),
            ("smtp needs a sign-in", .success(AccountTestResult(imap: endpoint(nil), smtp: endpoint(.authRequired))), true),
            ("network", .success(AccountTestResult(imap: endpoint(.networkError), smtp: endpoint(nil))), false),
            ("call said authRequired", .failure(RPCError(code: .authRequired, message: "x")), true),
            ("call said authFailed", .failure(RPCError(code: .authFailed, message: "x")), true),
            ("call timed out", .failure(RPCClient.ClientError.timeout(method: "account.oauthWait")), false),
            ("call failed otherwise", .failure(RPCError(code: .oauthClientMissing, message: "x")), false),
        ]
        for (name, outcome, want) in cases {
            #expect(isSignInProblem(outcome) == want, Comment(rawValue: name))
        }
    }
}
