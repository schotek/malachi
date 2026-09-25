// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/accountwizard/fields.go: the field logic of the account
// wizard. The backend discovers, tests and validates; this only checks
// field syntax, fills port defaults and assembles the configuration.

import Foundation

/// accountwizard.Endpoint: the IMAP or SMTP side of the settings.
public enum Endpoint: Sendable, Hashable, CaseIterable {
    case imap
    case smtp
}

/// accountwizard.Identity: what the first page collects.
public struct Identity: Sendable, Equatable {
    public var displayName: String
    public var email: String
    public var password: String

    public init(displayName: String = "", email: String = "", password: String = "") {
        self.displayName = displayName
        self.email = email
        self.password = password
    }
}

/// accountwizard.ServerFields: the rows of one endpoint on the Servers page,
/// and the certificate pinned to it (not a row: the wizard sets it from the
/// pin trusted for this host and port, `CertTrust.keepPin`). "" is no pin.
public struct ServerFields: Sendable, Equatable {
    public var host: String
    public var port: Int
    public var security: Security
    public var username: String
    public var certificateSha256: String

    public init(host: String = "", port: Int = 0, security: Security = .tls, username: String = "", certificateSha256: String = "") {
        self.host = host
        self.port = port
        self.security = security
        self.username = username
        self.certificateSha256 = certificateSha256
    }
}

/// accountwizard.securityChoices: the order of the "Security" choices in
/// account_wizard.blp.
public let securityChoices: [Security] = [.tls, .starttls, .none]

/// accountwizard.DefaultPort: the conventional port for a security mode.
public func defaultPort(_ kind: Endpoint, _ security: Security) -> Int {
    switch kind {
    case .imap:
        return security == .tls ? 993 : 143
    case .smtp:
        return security == .tls ? 465 : 587
    }
}

/// accountwizard.PortForSecurityChange: keeps a custom port and only swaps
/// the default of the previous mode for the default of the new one.
public func portForSecurityChange(_ kind: Endpoint, port: Int, from: Security, to: Security) -> Int {
    if from == to {
        return port
    }
    if port == 0 || port == defaultPort(kind, from) {
        return defaultPort(kind, to)
    }
    return port
}

/// accountwizard.indexOfSecurity: the row of a mode in `securityChoices`,
/// 0 for an unknown one.
public func indexOfSecurity(_ security: Security) -> Int {
    securityChoices.firstIndex(of: security) ?? 0
}

/// accountwizard.securityAt: the mode of a row, TLS out of range.
public func securityAt(_ index: Int) -> Security {
    if index >= 0, index < securityChoices.count {
        return securityChoices[index]
    }
    return .tls
}

/// accountwizard.ValidateEmail: trims and accepts only a bare address: no
/// display name, no comments. The backend validates authoritatively; this
/// is immediate feedback. nil when refused.
public func validateEmail(_ s: String) -> String? {
    let trimmed = s.trimmingCharacters(in: .whitespacesAndNewlines)
    if trimmed.isEmpty {
        return nil
    }
    guard let a = AddressList.parseAddress(trimmed), a.name == nil, a.address == trimmed else {
        return nil
    }
    return trimmed
}

/// accountwizard.Domain: the lower-cased part after the last '@', or "".
public func domain(_ email: String) -> String {
    let utf8 = email.utf8
    guard let i = utf8.lastIndex(of: UInt8(ascii: "@")) else { return "" }
    let after = utf8.index(after: i)
    if after == utf8.endIndex {
        return ""
    }
    return String(decoding: utf8[after...], as: UTF8.self).lowercased()
}

/// accountwizard.SuggestAccountName: the default account name: the
/// address's domain.
public func suggestAccountName(_ email: String) -> String {
    let d = domain(email)
    return d.isEmpty ? email : d
}

/// accountwizard.GuessConfig: the fallback when discovery finds nothing.
public func guessConfig(_ email: String) -> AccountConfig {
    let (imap, smtp) = guessServers(email)
    return AccountConfig(name: "", email: email, kind: .imap, imap: imap, smtp: smtp)
}

private func guessServers(_ email: String) -> (imap: ServerConfig, smtp: ServerConfig) {
    let d = domain(email)
    return (
        ServerConfig(host: "imap." + d, port: 993, security: .tls, username: email, authMethod: .password),
        ServerConfig(host: "smtp." + d, port: 587, security: .starttls, username: email, authMethod: .password)
    )
}

/// accountwizard.MergeIdentity: copies what the identity page knows into
/// a discovered or guessed IMAP configuration. Missing endpoints are
/// filled from the guess so the Servers page always has both.
public func mergeIdentity(_ config: AccountConfig, _ id: Identity) -> AccountConfig {
    var cfg = config
    cfg.email = id.email
    cfg.displayName = nonEmpty(id.displayName.trimmingCharacters(in: .whitespacesAndNewlines))
    if cfg.name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
        cfg.name = suggestAccountName(id.email)
    }
    let guess = guessServers(id.email)
    cfg.kind = .imap
    cfg.graph = nil
    var imap = cfg.imap ?? guess.imap
    var smtp = cfg.smtp ?? guess.smtp
    if imap.username.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
        imap.username = id.email
    }
    imap.authMethod = .password
    if smtp.username.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
        smtp.username = id.email
    }
    smtp.authMethod = .password
    cfg.imap = imap
    cfg.smtp = smtp
    return cfg
}

/// accountwizard.IdentityProblems: the identity fields that cannot be sent.
public struct IdentityProblems: Sendable, Equatable {
    public var email: Bool
    public var password: Bool

    public init(email: Bool = false, password: Bool = false) {
        self.email = email
        self.password = password
    }

    /// Whether anything is wrong.
    public var any: Bool { email || password }
}

/// accountwizard.ValidateIdentity: checks address syntax and, when
/// required, a non-empty password (editing an account may keep the
/// stored one).
public func validateIdentity(_ id: Identity, passwordRequired: Bool) -> IdentityProblems {
    IdentityProblems(email: validateEmail(id.email) == nil, password: passwordRequired && id.password.isEmpty)
}

/// accountwizard.ServerProblems: the server rows that cannot be sent.
public struct ServerProblems: Sendable, Equatable {
    public var imapHost: Bool
    public var imapUser: Bool
    public var smtpHost: Bool
    public var smtpUser: Bool

    public init(imapHost: Bool = false, imapUser: Bool = false, smtpHost: Bool = false, smtpUser: Bool = false) {
        self.imapHost = imapHost
        self.imapUser = imapUser
        self.smtpHost = smtpHost
        self.smtpUser = smtpUser
    }

    /// Whether anything is wrong.
    public var any: Bool { imapHost || imapUser || smtpHost || smtpUser }
}

/// accountwizard.ValidateServers: flags empty hosts and user names. Ports
/// are bounded by the steppers; everything else is the backend's call.
public func validateServers(imap: ServerFields, smtp: ServerFields) -> ServerProblems {
    ServerProblems(
        imapHost: imap.host.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
        imapUser: imap.username.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
        smtpHost: smtp.host.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
        smtpUser: smtp.username.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    )
}

/// accountwizard.BuildConfig: assembles the wire configuration from the
/// rows and their pins. It is the one place that sets the authentication
/// method.
public func buildConfig(identity id: Identity, name: String, imap: ServerFields, smtp: ServerFields) -> AccountConfig {
    let email = id.email.trimmingCharacters(in: .whitespacesAndNewlines)
    var accountName = name.trimmingCharacters(in: .whitespacesAndNewlines)
    if accountName.isEmpty {
        accountName = suggestAccountName(email)
    }
    return AccountConfig(
        name: accountName,
        email: email,
        displayName: nonEmpty(id.displayName.trimmingCharacters(in: .whitespacesAndNewlines)),
        kind: .imap,
        imap: serverConfig(imap),
        smtp: serverConfig(smtp)
    )
}

private func serverConfig(_ f: ServerFields) -> ServerConfig {
    ServerConfig(
        host: f.host.trimmingCharacters(in: .whitespacesAndNewlines),
        port: f.port,
        security: f.security,
        username: f.username.trimmingCharacters(in: .whitespacesAndNewlines),
        authMethod: .password,
        certificateSha256: nonEmpty(f.certificateSha256)
    )
}

/// accountwizard.credentialsFor: the secret of the identity page; an empty
/// password is no password (`account.update` then keeps the stored one).
public func credentialsFor(_ id: Identity) -> Credentials {
    Credentials(password: nonEmpty(id.password))
}

/// Go's "" sentinel as an Optional.
private func nonEmpty(_ s: String) -> String? {
    s.isEmpty ? nil : s
}
