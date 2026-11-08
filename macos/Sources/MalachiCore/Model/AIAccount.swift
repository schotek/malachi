// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/aiaccount text.go, failure.go and the address and model
// rules of controller.go: the texts of the dialog that adds or edits an AI
// provider account (kind `ai`, docs/api.md §4.1; the design is
// docs/plans/ai-api.md §7.1), the consent it asks before the account is
// saved (D7, Q17), the fields it flags, how a failed call is worded by its
// error code, and the daemon's rules for a server address and a model id.
// Pure: no RPC, no AppKit; `AIAccountController` is the dialog over it.
// Texts go through `L10n` with the GTK msgids.

import Darwin
import Foundation

public enum AIAccount {
    // MARK: Limits (docs/api.md §4.1)

    /// aiaccount.maxModelBytes: the daemon's limit of a model id.
    public static let maxModelBytes = 256
    /// aiaccount.maxBaseURLBytes: the daemon's limit of `baseUrl`.
    public static let maxBaseURLBytes = 2048

    // MARK: Texts (text.go)

    /// aiaccount.Strings: the dialog's texts. Every one is plain text
    /// (nothing is markup); the buttons carry mnemonics.
    public struct Strings: Sendable, Equatable {
        /// The dialog's title when adding, when editing; the read-only row
        /// naming the kind.
        public var addTitle: String
        public var editTitle: String
        public var providerLabel: String
        public var nameLabel: String
        /// The address row of an OpenAI-compatible server and the line
        /// under it (`addressHint`).
        public var serverLabel: String
        public var serverHint: String
        /// The key row: `keyLabel` when adding, `keyOptionalLabel` for a
        /// server that may have none, `newKeyLabel` while a stored key is
        /// being replaced; `keyStored` and `replaceKey` are the row of a
        /// stored key, `keepKey` takes the replacement back.
        public var keyLabel: String
        public var keyOptionalLabel: String
        public var newKeyLabel: String
        public var keyStored: String
        public var replaceKey: String
        public var keepKey: String
        /// The model rows: a text with the provider's models to pick from.
        public var modelLabel: String
        public var modelHint: String
        public var boardModelLabel: String
        public var boardModelHint: String
        /// The certificate pin of an https server (docs/plans/ai-api.md D8).
        public var pinLabel: String
        public var pinForget: String
        /// Test lists the provider's models; `testing` is its progress;
        /// `connectedNoModels` the status without a model (`connected(_:)`
        /// with the number).
        public var test: String
        public var testing: String
        public var connectedNoModels: String
        /// Add, Save and their progress texts.
        public var add: String
        public var save: String
        public var adding: String
        public var saving: String
        /// Problems of the fields.
        public var nameMissing: String
        public var serverMissing: String
        public var serverInvalid: String
        public var pinNeedsHTTPS: String
        public var keyMissing: String
        public var modelMissing: String
        public var modelInvalid: String
        public var consentMissing: String
        /// Failures of the calls (`failureOf`).
        public var keyRefused: String
        public var unreachable: String
        public var timeout: String
        public var insecure: String
        public var serverError: String

        /// aiaccount.Strings.Connected: the status after a test that
        /// listed `n` models.
        public func connected(_ n: Int) -> String {
            if n <= 0 {
                return connectedNoModels
            }
            // TRANSLATORS: status of the AI provider dialog after a test; %d is the number of models the provider listed.
            return L10n.N("Connected · %d model", "Connected · %d models", n)
        }
    }

    /// aiaccount.Texts: the dialog's texts, translated.
    public static func texts() -> Strings {
        let x = Assistant.apiText()
        return Strings(
            // TRANSLATORS: title of the dialog that adds an AI provider account (Anthropic API, OpenAI API, an OpenAI-compatible server).
            addTitle: L10n.T("Add AI Provider"),
            editTitle: L10n.T("Edit Account"),
            // TRANSLATORS: the read-only row of the AI provider dialog naming the kind of provider.
            providerLabel: L10n.T("Provider"),
            nameLabel: L10n.T("Account Name"),
            // TRANSLATORS: the base URL of an OpenAI-compatible server ("…/v1").
            serverLabel: L10n.T("Server Address"),
            serverHint: addressHint(),
            keyLabel: L10n.T("API Key"),
            // TRANSLATORS: the key row of an OpenAI-compatible server, which may need none.
            keyOptionalLabel: L10n.T("API Key (optional)"),
            newKeyLabel: L10n.T("New API Key"),
            // TRANSLATORS: the key row of an existing AI provider account: the key is in the keyring and never shown.
            keyStored: L10n.T("Key stored"),
            // TRANSLATORS: button of the key row of an existing AI provider account.
            replaceKey: L10n.T("Replace Key…"),
            // TRANSLATORS: button that takes a key replacement back and keeps the stored key.
            keepKey: L10n.T("Keep the Stored Key"),
            modelLabel: x.modelRow,
            // TRANSLATORS: under the model row of the AI provider dialog; the list comes from Test.
            modelHint: L10n.T("Choose one of the provider's models or type its id"),
            // TRANSLATORS: the model of the board's triage, an optional second model of the AI provider account.
            boardModelLabel: L10n.T("Board Model"),
            boardModelHint: x.boardModelSubtitle,
            // TRANSLATORS: row of the AI provider dialog showing the pinned certificate of an https server.
            pinLabel: L10n.T("Trusted Certificate"),
            // TRANSLATORS: button that drops the pinned certificate.
            pinForget: L10n.T("Forget"),
            // TRANSLATORS: button of the AI provider dialog: asks the provider for its models with the key.
            test: L10n.T("_Test"),
            // TRANSLATORS: progressive form for the RPC error text
            testing: L10n.T("Testing the connection"),
            // TRANSLATORS: status of the AI provider dialog after a test that listed no models.
            connectedNoModels: L10n.T("Connected"),
            add: L10n.T("_Add Account"),
            save: L10n.T("_Save"),
            adding: L10n.T("Adding the account"),
            saving: L10n.T("Saving the account"),
            nameMissing: L10n.T("Enter a name for the account"),
            // TRANSLATORS: the address example stays as it is.
            serverMissing: L10n.T("Enter the server's address, such as http://localhost:11434/v1"),
            serverInvalid: L10n.T("This is not a web address"),
            pinNeedsHTTPS: L10n.T("A certificate can be pinned only for an https address"),
            keyMissing: L10n.T("Enter the API key"),
            modelMissing: L10n.T("Enter or choose a model"),
            modelInvalid: L10n.T("This is not a model id"),
            // TRANSLATORS: Save of the AI provider dialog with the consent check box off.
            consentMissing: L10n.T("Allow sending mail to the provider first"),
            keyRefused: x.accountKeyRefused,
            unreachable: L10n.T("The provider could not be reached"),
            timeout: L10n.T("The provider did not answer in time"),
            insecure: L10n.T("The connection to the provider is not secure"),
            serverError: L10n.T("The provider answered with an error"))
    }

    /// aiaccount.HTTPWarning: the warning under an http address outside a
    /// loopback address (docs/plans/ai-api.md D8); `host` is the address's
    /// host.
    public static func httpWarning(host: String) -> String {
        // TRANSLATORS: %s is the host of an OpenAI-compatible server reached over plain http.
        L10n.T("The API key and your mail travel unencrypted over http to %s.", host)
    }

    /// aiaccount.AddressExample: one example of an OpenAI-compatible
    /// server's address: a local server or a gateway (docs/plans/ai-api.md
    /// Q18).
    public struct AddressExample: Sendable, Equatable {
        public var name: String
        public var url: String

        public init(name: String, url: String) {
            self.name = name
            self.url = url
        }
    }

    /// aiaccount.AddressExamples: the examples the hint under the address
    /// field names; nothing is prefilled.
    public static let addressExamples: [AddressExample] = [
        AddressExample(name: "Ollama", url: "http://localhost:11434/v1"),
        AddressExample(name: "llama.cpp", url: "http://127.0.0.1:9931/v1"),
        AddressExample(name: "LM Studio", url: "http://localhost:1234/v1"),
        AddressExample(name: "vLLM", url: "http://localhost:8000/v1"),
        AddressExample(name: "OpenRouter", url: "https://openrouter.ai/api/v1"),
        AddressExample(name: "Groq", url: "https://api.groq.com/openai/v1"),
        AddressExample(name: "Mistral", url: "https://api.mistral.ai/v1"),
        AddressExample(name: "Together", url: "https://api.together.ai/v1"),
    ]

    /// aiaccount.AddressHint: the line under the address field: the
    /// examples, one per line after the sentence.
    public static func addressHint() -> String {
        // TRANSLATORS: under the address field of an OpenAI-compatible server; the examples follow, one per line.
        var out = L10n.T("The server's base URL, usually ending in /v1. For example:")
        for e in addressExamples {
            out += "\n" + e.name + "  " + e.url
        }
        return out
    }

    /// aiaccount.ConsentStrings: the consent the dialog asks before the
    /// account is saved (docs/plans/ai-api.md D7, Q17): the heading, the
    /// body (one common paragraph on what leaves, then one on where it
    /// goes, by the kind of provider) and the check box.
    public struct ConsentStrings: Sendable, Equatable {
        public var heading: String
        public var common: String
        public var specific: String
        public var checkbox: String

        /// aiaccount.ConsentStrings.Body: the two paragraphs.
        public var body: String { common + "\n\n" + specific }
    }

    /// aiaccount.ConsentTexts: the consent of a provider kind.
    public static func consentTexts(for p: AIProvider) -> ConsentStrings {
        var c = ConsentStrings(
            heading: "",
            common: L10n.T("The assistant sends the prompt, the text you write, the mail it reads for you and that mail's attachments to the provider. It can read mail and prepare drafts; it cannot send, delete or move messages."),
            specific: "",
            // TRANSLATORS: check box of the AI provider dialog; Save needs it on.
            checkbox: L10n.T("Allow Malachi Mail to send mail to this provider"))
        switch p {
        case .anthropic:
            c.heading = L10n.T("Send Mail to Anthropic?")
            c.specific = L10n.T("They go to Anthropic under its API terms, which do not use data sent through the API for training by default.")
        case .openai:
            c.heading = L10n.T("Send Mail to OpenAI?")
            c.specific = L10n.T("They go to OpenAI under its API terms, which do not use data sent through the API for training by default.")
        default:
            // TRANSLATORS: consent heading of an OpenAI-compatible server the user set the address of.
            c.heading = L10n.T("Send Mail to Your Server?")
            c.specific = L10n.T("They go to the server at the address you set. Over http they travel unencrypted.")
        }
        return c
    }

    // MARK: Fields and failures (failure.go)

    /// aiaccount.Field: a field of the dialog that can be flagged and
    /// focused.
    public enum Field: Sendable, Equatable {
        case none
        case name
        case server
        case key
        case model
        case boardModel
        case consent
    }

    /// aiaccount.Failure: what the dialog shows after a failed call: the
    /// banner and the field to flag. An empty `banner` means the client's
    /// general sentence for the error (`rpcErrorText`) with the step's
    /// action.
    public struct Failure: Sendable, Equatable {
        public var banner = ""
        public var field: Field = .none

        public init(banner: String = "", field: Field = .none) {
            self.banner = banner
            self.field = field
        }
    }

    /// aiaccount.FailureOf: words a failed account.test, account.add or
    /// account.update of an AI provider account by the error's code
    /// (docs/api.md §2): a refused key, an unreachable or slow provider, an
    /// insecure connection (the TLS details are the trust offer's), a
    /// provider error. Anything else (invalidArgument, keyringError, …)
    /// gets the general sentence.
    public static func failureOf(_ error: (any Error)?) -> Failure {
        guard let e = error as? RPCError else { return Failure() }
        let s = texts()
        switch e.code {
        case .authFailed:
            return Failure(banner: s.keyRefused, field: .key)
        case .authRequired:
            return Failure(banner: s.keyMissing, field: .key)
        case .networkError, .offline:
            return Failure(banner: s.unreachable, field: .server)
        case .serverTimeout:
            return Failure(banner: s.timeout, field: .server)
        case .tlsError:
            return Failure(banner: s.insecure, field: .server)
        case .serverError:
            return Failure(banner: s.serverError)
        case .assistantConsentRequired:
            return Failure(banner: s.consentMissing, field: .consent)
        default:
            return Failure()
        }
    }

    // MARK: The address and model rules of the daemon (controller.go)

    /// aiaccount.NormalizeBaseURL: the server address as the daemon stores
    /// it: http or https, a host, no user info, query or fragment, the host
    /// lower case, no trailing slash, at most 2048 bytes; nil for anything
    /// else (an empty address, another scheme, a space or control
    /// character, no host). Reads the address as Go's url.Parse does
    /// (`URLSyntax`), so an address normalises here as in the GTK UI.
    public static func normalizeBaseURL(_ raw: String) -> String? {
        let s = Jira.trimSpace(raw)
        guard !s.isEmpty, s.utf8.count <= maxBaseURLBytes else { return nil }
        for r in s.unicodeScalars {
            if r.properties.isWhitespace || r == "\\" {
                return nil
            }
            switch r.properties.generalCategory {
            case .control, .format:
                return nil
            default:
                break
            }
        }
        guard let u = parse(s) else { return nil }
        let scheme = u.scheme.lowercased()
        guard scheme == "http" || scheme == "https", !u.opaque else { return nil }
        let (hostname, port) = Jira.splitHostPort(u.host)
        guard !hostname.isEmpty else { return nil }
        var host = hostname.lowercased()
        if host.contains(":") {
            host = "[" + host + "]"
        }
        if !port.isEmpty {
            host += ":" + port
        }
        var path = u.escapedPath
        while path.hasSuffix("/") {
            path.removeLast()
        }
        let out = scheme + "://" + host + path
        guard out.utf8.count <= maxBaseURLBytes else { return nil }
        return out
    }

    /// aiaccount.hostOf: the host of an address as typed; "" when it is
    /// none.
    public static func hostOf(_ raw: String) -> String {
        guard let u = normalizeBaseURL(raw), let p = parse(u) else { return "" }
        return Jira.splitHostPort(p.host).host
    }

    /// aiaccount.IsLoopback: a host that is this computer: localhost, a
    /// name under .localhost, or a loopback IP address (127.0.0.0/8, ::1).
    public static func isLoopback(_ host: String) -> Bool {
        var h = host.lowercased()
        if h.hasSuffix(".") {
            h.removeLast()
        }
        if h == "localhost" || h.hasSuffix(".localhost") {
            return true
        }
        if h.hasPrefix("["), h.hasSuffix("]") {
            h = String(h.dropFirst().dropLast())
        }
        var v4 = in_addr()
        if inet_pton(AF_INET, h, &v4) == 1 {
            // net.IP.IsLoopback: 127.0.0.0/8 (the address is in network
            // byte order: its first byte is the lowest in memory).
            return withUnsafeBytes(of: &v4) { $0.first == 127 }
        }
        var v6 = in6_addr()
        if inet_pton(AF_INET6, h, &v6) == 1 {
            return withUnsafeBytes(of: &v6) { bytes -> Bool in
                let b = Array(bytes)
                if b == [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1] {
                    return true
                }
                // An IPv4-mapped address (::ffff:127.0.0.1) is its IPv4.
                return b[0..<10].allSatisfy { $0 == 0 } && b[10] == 0xFF && b[11] == 0xFF && b[12] == 127
            }
        }
        return false
    }

    /// aiaccount.modelCheck.
    public enum ModelCheck: Sendable, Equatable {
        case ok
        case missing
        case invalid
    }

    /// aiaccount.checkModel: a model id as the daemon checks it: not
    /// empty, at most 256 bytes, no control characters.
    public static func checkModel(_ raw: String) -> ModelCheck {
        let s = Jira.trimSpace(raw)
        if s.isEmpty {
            return .missing
        }
        if s.utf8.count > maxModelBytes {
            return .invalid
        }
        for r in s.unicodeScalars where r.properties.generalCategory == .control {
            return .invalid
        }
        return .ok
    }

    // MARK: url.Parse for the address rules

    /// What `normalizeBaseURL` reads of Go's url.URL: the scheme, whether
    /// the URL is opaque, URL.Host and URL.EscapedPath.
    struct ParsedAddress: Sendable, Equatable {
        var scheme: String
        var opaque: Bool
        var host: String
        var escapedPath: String
    }

    /// url.Parse of an address, nil where Go reports an error
    /// (`Jira.parseURL` with the path kept).
    static func parse(_ raw: String) -> ParsedAddress? {
        let bytes = Array(raw.utf8)[...]
        let (u, fragment, _) = URLSyntax.cut(bytes, UInt8(ascii: "#"))
        guard !URLSyntax.hasControlByte(u), let (scheme, rest) = URLSyntax.scheme(of: u),
              let parsed = URLSyntax.parseRest(rest, scheme: scheme) else { return nil }
        if !fragment.isEmpty, URLSyntax.unescape(fragment, mode: .path) == nil {
            return nil
        }
        if !parsed.opaque.isEmpty {
            return ParsedAddress(scheme: scheme, opaque: true, host: "", escapedPath: "")
        }
        return ParsedAddress(scheme: scheme, opaque: false, host: parsed.host, escapedPath: escapedPath(of: rest, scheme: scheme))
    }

    /// url.URL.EscapedPath of the part after the scheme: the path as typed
    /// when it is a valid encoding of itself, else the decoded path
    /// escaped again as Go's escape(path, encodePath) does.
    private static func escapedPath(of rest: ArraySlice<UInt8>, scheme: String) -> String {
        let question = UInt8(ascii: "?")
        let slash = UInt8(ascii: "/")
        var r = rest
        if r.last == question, r.filter({ $0 == question }).count == 1 {
            r = r.dropLast()
        } else {
            r = URLSyntax.cut(r, question).before
        }
        if (!scheme.isEmpty || !r.starts(with: [slash, slash, slash])), r.starts(with: [slash, slash]) {
            let authority = r.dropFirst(2)
            if let i = authority.firstIndex(of: slash) {
                r = authority[i...]
            } else {
                r = authority[authority.endIndex...]
            }
        }
        guard let path = URLSyntax.unescape(r, mode: .path) else { return "" }
        if validEncodedPath(r) {
            return String(decoding: r, as: UTF8.self)
        }
        return escapePath(path)
    }

    /// url.shouldEscape(c, encodePath).
    private static func shouldEscapeInPath(_ c: UInt8) -> Bool {
        switch c {
        case UInt8(ascii: "a")...UInt8(ascii: "z"), UInt8(ascii: "A")...UInt8(ascii: "Z"), UInt8(ascii: "0")...UInt8(ascii: "9"):
            return false
        case UInt8(ascii: "-"), UInt8(ascii: "_"), UInt8(ascii: "."), UInt8(ascii: "~"):
            return false
        case UInt8(ascii: "$"), UInt8(ascii: "&"), UInt8(ascii: "+"), UInt8(ascii: ","), UInt8(ascii: "/"), UInt8(ascii: ":"),
             UInt8(ascii: ";"), UInt8(ascii: "="), UInt8(ascii: "@"):
            return false
        default:
            return true
        }
    }

    /// url.validEncoded(s, encodePath).
    private static func validEncodedPath(_ s: ArraySlice<UInt8>) -> Bool {
        for c in s {
            switch c {
            case UInt8(ascii: "!"), UInt8(ascii: "$"), UInt8(ascii: "&"), UInt8(ascii: "'"), UInt8(ascii: "("), UInt8(ascii: ")"),
                 UInt8(ascii: "*"), UInt8(ascii: "+"), UInt8(ascii: ","), UInt8(ascii: ";"), UInt8(ascii: "="), UInt8(ascii: ":"),
                 UInt8(ascii: "@"), UInt8(ascii: "["), UInt8(ascii: "]"), UInt8(ascii: "%"):
                continue
            default:
                if shouldEscapeInPath(c) {
                    return false
                }
            }
        }
        return true
    }

    /// url.escape(s, encodePath).
    private static func escapePath(_ s: String) -> String {
        let hex = Array("0123456789ABCDEF".utf8)
        var out: [UInt8] = []
        for c in s.utf8 {
            if shouldEscapeInPath(c) {
                out.append(UInt8(ascii: "%"))
                out.append(hex[Int(c >> 4)])
                out.append(hex[Int(c & 15)])
            } else {
                out.append(c)
            }
        }
        return String(decoding: out, as: UTF8.self)
    }
}
