// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/assistant api.go and apitext.go: the provider of the
// assistant in the app is the setting `assistant-provider`, a string
// (docs/plans/ai-api.md D14): `providerClaude` for the user's Claude Code,
// `providerChatGPT` for Codex with the user's ChatGPT plan, or the id of an
// AI provider account of the daemon (kind `ai`, docs/api.md §4.1), over
// which the daemon runs the assistant itself (§4.18). The board's triage
// uses the same provider (its model apart: `AIConfig.boardModel`). An
// empty, unknown or removed value reads as Claude (`normalizeProvider`,
// `sourceOf`). The texts of that choice and of the AI provider accounts
// (`apiText`), where an account sends the mail (`destination`), and the
// sentences of a daemon session's failures (`apiFailureText`) are here
// too, with the GTK msgids as their keys.

import Foundation

extension Assistant {
    /// assistant.ProviderClaude: the user's Claude Code.
    public static let providerClaude = "claude"
    /// assistant.ProviderChatGPT: Codex with the user's ChatGPT plan.
    public static let providerChatGPT = "chatgpt"
    /// Starts every account id of the daemon ("acc_" + 32 hex digits).
    private static let accountPrefix = "acc_"

    /// assistant.ProviderSource: which of the three forms a provider
    /// setting is.
    public enum ProviderSource: Sendable, Equatable {
        case claude
        case chatgpt
        case account
    }

    /// assistant.IsAccountProvider: a setting that names an AI provider
    /// account (an id of the daemon).
    public static func isAccountProvider(_ id: String) -> Bool {
        id.hasPrefix(accountPrefix) && id.utf8.count > accountPrefix.utf8.count
    }

    /// assistant.AccountOf: the account a setting names; nil for the local
    /// providers.
    public static func accountOf(_ id: String) -> AccountID? {
        isAccountProvider(id) ? AccountID(id) : nil
    }

    /// assistant.NormalizeProvider: the stored form of a provider setting:
    /// "chatgpt" and an account id pass, anything else is "claude".
    public static func normalizeProvider(_ id: String) -> String {
        if id == providerChatGPT || isAccountProvider(id) { return id }
        return providerClaude
    }

    /// assistant.SourceOf: the source of a provider setting.
    public static func sourceOf(_ id: String) -> ProviderSource {
        switch normalizeProvider(id) {
        case providerChatGPT: return .chatgpt
        case providerClaude: return .claude
        default: return .account
        }
    }

    /// assistant.ProviderChoiceState: what the client knows of a local
    /// provider for the Assistant Uses row: whether it can run now, and
    /// why not (a sentence shown with the greyed entry; "" when
    /// `available`).
    public struct ProviderChoiceState: Sendable, Equatable {
        public var available: Bool
        public var problem: String

        public init(available: Bool = false, problem: String = "") {
            self.available = available
            self.problem = problem
        }
    }

    /// assistant.ProviderChoice: one entry of the Assistant Uses row, its
    /// setting value, its name, whether it can be chosen, and why not.
    public struct ProviderChoice: Sendable, Equatable {
        public var id: String
        public var name: String
        public var available: Bool
        public var problem: String

        public init(id: String, name: String, available: Bool = false, problem: String = "") {
            self.id = id
            self.name = name
            self.available = available
            self.problem = problem
        }
    }

    /// assistant.ProviderChoices: the entries of the Assistant Uses row of
    /// Settings → AI (docs/plans/ai-api.md §7.3): Claude Code, ChatGPT,
    /// then every AI provider account in the order of `accounts` by name
    /// (an account without a name by its provider). An account that is
    /// switched off, that has no current consent, or whose key the
    /// provider refused is listed with its `problem` and not available;
    /// other accounts are available. Accounts that are not AI provider
    /// accounts are skipped.
    public static func providerChoices(claude: ProviderChoiceState, chatgpt: ProviderChoiceState, accounts: [Account]) -> [ProviderChoice] {
        let x = apiText()
        var out = [
            ProviderChoice(id: providerClaude, name: x.claudeCode, available: claude.available, problem: claude.problem),
            ProviderChoice(id: providerChatGPT, name: x.chatGPT, available: chatgpt.available, problem: chatgpt.problem),
        ]
        for a in accounts where a.config.protocolKind == .ai {
            var c = ProviderChoice(id: a.id.rawValue, name: a.config.name.trimmingCharacters(in: .whitespacesAndNewlines), available: true)
            if c.name.isEmpty {
                c.name = x.providerName(of: a)
            }
            if !a.enabled {
                c.available = false
                c.problem = x.accountOff
            } else if a.config.ai?.consented != true {
                c.available = false
                c.problem = x.accountNoConsent
            } else if a.state.status == .authRequired {
                c.available = false
                c.problem = x.accountKeyRefused
            }
            out.append(c)
        }
        return out
    }

    /// assistant.APIStrings: the texts of the provider choice and of the
    /// AI provider accounts shared by the clients.
    public struct APIStrings: Sendable, Equatable {
        /// The names of the three provider kinds (`providerName`) and of
        /// the two local providers in the Assistant Uses row.
        public var anthropic: String
        public var openAI: String
        public var openAICompatible: String
        public var claudeCode: String
        public var chatGPT: String
        /// The Assistant Uses row of Settings → AI and its line below.
        public var usesRow: String
        public var usesSubtitle: String
        /// Why an account cannot be chosen (`ProviderChoice.problem`), and
        /// the Assistant Uses row's entry for an account that was removed.
        public var accountOff: String
        public var accountNoConsent: String
        public var accountKeyRefused: String
        public var accountMissing: String
        /// The Model row of Settings → AI and of the board's group; its
        /// line for an AI provider account (the model is the account's and
        /// is saved to it).
        public var modelRow: String
        public var modelSubtitle: String
        public var boardModelSubtitle: String
        /// The Mac App Store build's dialog (D17), opened by Add… of a
        /// local integration: this version cannot run Claude Code or
        /// Codex; `storeAddAPI` its button that adds an AI provider account
        /// instead (the website button is `WebsiteFeatures.open`).
        public var storeDialogBody: String
        public var storeAddAPI: String
        /// Under Show the Assistant Menu of Settings → AI: the bridge is
        /// registered in Claude under Integrations now.
        public var registerInIntegrations: String
        /// The assistant panel's footer over an AI provider account; %s is
        /// `destination`.
        public var footer: String
        /// The board's consent over an AI provider account (the board's
        /// `triageConsentBody` speaks of Claude Code); %s is `destination`.
        /// The heading is `Board.Text.triageConsentHeading`.
        public var boardConsentBody: String
        /// What the board's triage and the panel say instead of Claude
        /// Code's missing executable and sign-in: the account is switched
        /// off, the key was refused, and the button that opens
        /// Integrations instead of Get Claude Code… / Sign In….
        public var boardAccountOff: String
        public var boardKeyRefused: String
        public var setUpInIntegrations: String
        /// The first entry of the board's Model row over an AI provider
        /// account: the account's model.
        public var boardModelDefault: String
        /// The line of an AI provider account's row in Integrations: %s is
        /// the number of tokens the daemon's assistant used through it
        /// today (the client groups its digits).
        public var usageToday: String

        /// assistant.APIStrings.name: the name of a provider kind; an
        /// unknown one is the OpenAI-compatible server.
        public func name(of p: AIProvider) -> String {
            switch p {
            case .anthropic: return anthropic
            case .openai: return openAI
            default: return openAICompatible
            }
        }

        /// assistant.APIStrings.providerName: the name of an account's
        /// provider kind.
        public func providerName(of a: Account) -> String {
            guard let ai = a.config.ai else { return openAICompatible }
            return name(of: ai.provider)
        }
    }

    /// assistant.APIText: the texts, translated.
    public static func apiText() -> APIStrings {
        APIStrings(
            // TRANSLATORS: the kind of an AI provider account: the Anthropic Messages API with the user's own key.
            anthropic: L10n.T("Anthropic API"),
            // TRANSLATORS: the kind of an AI provider account: the OpenAI API with the user's own key.
            openAI: L10n.T("OpenAI API"),
            // TRANSLATORS: the kind of an AI provider account: any server with the OpenAI chat completions interface (Ollama, llama.cpp, LM Studio, vLLM, a gateway).
            openAICompatible: L10n.T("OpenAI-compatible server"),
            // The names of the local providers are not translated (api.go).
            claudeCode: "Claude Code",
            chatGPT: "ChatGPT (Codex)",
            // TRANSLATORS: row of Settings → AI: which provider the assistant in the app runs on (Claude Code, ChatGPT, or an AI provider account).
            usesRow: L10n.T("Assistant Uses"),
            usesSubtitle: L10n.T("Claude Code or ChatGPT on this computer, or an AI provider added in Integrations"),
            // TRANSLATORS: why an AI provider account cannot be chosen: its switch in Integrations is off.
            accountOff: L10n.T("Switched off in Integrations"),
            // TRANSLATORS: why an AI provider account cannot be chosen: the user did not allow sending mail to it yet.
            accountNoConsent: L10n.T("Sending mail to it is not allowed yet"),
            // TRANSLATORS: why an AI provider account cannot be chosen: the provider refused its API key.
            accountKeyRefused: L10n.T("The provider refused the API key"),
            // TRANSLATORS: the Assistant Uses row shows this when the chosen AI provider account no longer exists.
            accountMissing: L10n.T("Removed account"),
            modelRow: L10n.T("Model"),
            // TRANSLATORS: under the Model row of Settings → AI when the assistant runs on an AI provider account.
            modelSubtitle: L10n.T("The model the account uses; saved to the account"),
            // TRANSLATORS: under the Model row of the board's group when the triage runs on an AI provider account.
            boardModelSubtitle: L10n.T("The model of the board's triage; empty uses the account's model"),
            // TRANSLATORS: dialog of Malachi Mail from the Mac App Store, opened by Add… of Claude or ChatGPT in Integrations; malachimail.app is the website and stays as it is.
            storeDialogBody: L10n.T("This version from the App Store cannot run Claude Code or Codex. Use Malachi Mail from malachimail.app, or add an AI provider through its API."),
            // TRANSLATORS: button of that dialog: it opens the catalogue entry that adds an Anthropic API account.
            storeAddAPI: L10n.T("Add Anthropic API…"),
            // TRANSLATORS: under Show the Assistant Menu in Settings → AI while the bridge is not registered in Claude; Integrations is the preferences page.
            registerInIntegrations: L10n.T("Register with Claude under Integrations first"),
            // TRANSLATORS: footer of the assistant panel; %s is where the mail goes (Anthropic, OpenAI, or the server's host).
            footer: L10n.T("Mail you ask about is sent to %s"),
            // TRANSLATORS: the board's consent over an AI provider account; %s is where the mail goes (Anthropic, OpenAI, or the server's host).
            boardConsentBody: L10n.T("The assistant reads the conversations on the board that need notes, and any other mail and attachments it needs to understand them, and sends their text to %s with your API key. It adds titles, summaries, tasks, deadlines and suggested replies to the board, and a triage you start yourself may also write replies, which stay on the board in Malachi Mail, not in your Drafts folder, until you send them. It cannot send, move or delete mail, and messages may contain instructions from their senders that it is told not to follow. Which accounts it triages, and whether it runs at all, you choose in Settings."),
            // TRANSLATORS: why the assistant cannot run: the chosen AI provider account's switch in Integrations is off.
            boardAccountOff: L10n.T("The AI account is switched off. Turn it on under Integrations in the preferences."),
            // TRANSLATORS: why the assistant cannot run: the provider refused the AI provider account's API key.
            boardKeyRefused: L10n.T("The provider refused the API key. Replace it under Integrations in the preferences."),
            // TRANSLATORS: button that opens the AI provider account's settings in Integrations (instead of Get Claude Code… / Sign In…).
            setUpInIntegrations: L10n.T("Set Up in Integrations…"),
            // TRANSLATORS: entry of the board's Model row in Settings → AI over an AI provider account: no model of its own, the account's.
            boardModelDefault: L10n.T("The account's model"),
            // TRANSLATORS: line of an AI provider account's row in Integrations; %s is a number of tokens with its digits grouped.
            usageToday: L10n.T("%s tokens today")
        )
    }

    /// assistant.Destination: where an AI provider account sends the mail,
    /// for the panel's footer and the consents: Anthropic, OpenAI, or the
    /// host of an OpenAI-compatible server (the provider's name when the
    /// address has no host).
    public static func destination(_ a: Account) -> String {
        guard let ai = a.config.ai else { return apiText().openAICompatible }
        switch ai.provider {
        case .anthropic: return "Anthropic"
        case .openai: return "OpenAI"
        default: break
        }
        let trimmed = ai.baseUrl.trimmingCharacters(in: .whitespacesAndNewlines)
        if let u = URL(string: trimmed), let host = u.host, !host.isEmpty {
            // Go's url.Host carries the port.
            if let port = u.port { return host + ":" + String(port) }
            return host
        }
        return apiText().openAICompatible
    }

    /// assistant.ProviderName: the name of a provider kind; an unknown one
    /// is named as the OpenAI-compatible server (the daemon accepts no
    /// other).
    public static func providerName(_ p: AIProvider) -> String {
        apiText().name(of: p)
    }

    // MARK: Failures of a daemon session (apitext.go)

    /// assistant.APIFailureStrings: the sentences of a daemon session's
    /// failures, one for each `AssistantFailureClass`, shown where a
    /// provider's failure is shown (the panel's error line, the compose
    /// window's popover, the board), never the daemon's technical message.
    public struct APIFailureStrings: Sendable, Equatable {
        /// The account's consent was not given.
        public var consentRequired: String
        /// The key was refused; the key may not use this.
        public var auth: String
        public var permission: String
        /// Too many requests; the provider is busy.
        public var rateLimit: String
        public var overloaded: String
        /// The account at the provider has no credit or used up its quota
        /// (the key tests fine: listing the models costs nothing).
        public var quota: String
        /// The server has no such model or could not load it.
        public var model: String
        /// The provider refused the request.
        public var invalidRequest: String
        /// The model cannot call tools; the Malachi Mail tools could not be
        /// started.
        public var toolsUnsupported: String
        public var bridge: String
        /// No connection; the answer could not be read.
        public var network: String
        public var `protocol`: String
        /// The conversation grew too long.
        public var contextFull: String
        /// No answer in time; too many tool calls in one turn.
        public var timeout: String
        public var limit: String
        /// The session ended (stopped, the connection closed).
        public var stopped: String
    }

    /// assistant.APIFailureTexts: the sentences, translated.
    public static func apiFailureTexts() -> APIFailureStrings {
        APIFailureStrings(
            // TRANSLATORS: An error line of the assistant: the user has not allowed sending mail to the AI account's provider yet.
            consentRequired: L10n.T("Allow sending mail to this provider under AI in the preferences first."),
            // TRANSLATORS: An error line of the assistant: the provider refused the API key.
            auth: L10n.T("The provider refused the API key. Check it under AI in the preferences."),
            // TRANSLATORS: An error line of the assistant: the API key may not use this model.
            permission: L10n.T("The API key may not use this model."),
            // TRANSLATORS: An error line of the assistant: the provider's rate limit was reached.
            rateLimit: L10n.T("The provider is receiving too many requests. Try again in a moment."),
            // TRANSLATORS: An error line of the assistant: the provider is overloaded.
            overloaded: L10n.T("The provider is busy right now. Try again in a moment."),
            // TRANSLATORS: An error line of the assistant: the account at the AI provider has no credit (prepaid) or used up its quota; the user tops it up on the provider's website, not in Malachi Mail.
            quota: L10n.T("The account at the provider has no credit or has used up its quota. Top it up with the provider, then try again."),
            // TRANSLATORS: An error line of the assistant: the AI server has no such model or could not load it (a local server, where the model did not fit in memory); the user picks another model under Integrations in the preferences or loads it on the server.
            model: L10n.T("The server could not load the model. Load it on the server, or choose another model under Integrations in the preferences."),
            // TRANSLATORS: An error line of the assistant: the provider rejected the request.
            invalidRequest: L10n.T("The provider refused the request."),
            // TRANSLATORS: An error line of the assistant: the chosen model cannot call tools, which the assistant needs to read mail.
            toolsUnsupported: L10n.T("This model cannot use the Malachi Mail tools. Choose another model under AI in the preferences."),
            // TRANSLATORS: An error line of the assistant: the MCP bridge (the Malachi Mail tools) could not be started.
            bridge: L10n.T("The Malachi Mail tools could not be started."),
            // TRANSLATORS: An error line of the assistant: the provider could not be reached.
            network: L10n.T("The provider could not be reached."),
            // TRANSLATORS: An error line of the assistant: the provider's answer could not be read.
            protocol: L10n.T("The provider's answer could not be read."),
            // TRANSLATORS: An error line of the assistant: the conversation grew too long for the model.
            contextFull: L10n.T("The conversation is too long for the model. Start a new one."),
            // TRANSLATORS: An error line of the assistant: the model did not answer in time.
            timeout: L10n.T("The model did not answer in time."),
            // TRANSLATORS: An error line of the assistant: the model called too many tools in one turn.
            limit: L10n.T("The model made too many tool calls in one turn."),
            // TRANSLATORS: An error line of the assistant: the session ended.
            stopped: L10n.T("The assistant's session ended.")
        )
    }

    /// assistant.APIFailurePrefix: starts every reason a daemon session's
    /// failure class becomes: "api_" and the class ("api_auth").
    public static let apiFailurePrefix = "api_"

    /// assistant.APIFailureText: the sentence of a reason "api_<class>";
    /// nil for any other reason, and for a class without a sentence.
    public static func apiFailureText(_ reason: String) -> String? {
        guard reason.hasPrefix(apiFailurePrefix) else { return nil }
        let t = apiFailureTexts()
        switch String(reason.dropFirst(apiFailurePrefix.count)) {
        case "consent_required": return t.consentRequired
        case "auth": return t.auth
        case "permission": return t.permission
        case "rateLimit": return t.rateLimit
        case "overloaded": return t.overloaded
        case "invalidRequest": return t.invalidRequest
        case "quota": return t.quota
        case "model": return t.model
        case "toolsUnsupported": return t.toolsUnsupported
        case "bridge": return t.bridge
        case "network": return t.network
        case "protocol": return t.`protocol`
        case "contextFull": return t.contextFull
        case "timeout": return t.timeout
        case "limit": return t.limit
        case "stopped": return t.stopped
        default: return nil
        }
    }
}

extension Assistant {
    /// assistant.NothingSetUp: the assistant has nothing to run on — no
    /// entry of Assistant Uses is available and the bridge is known not
    /// to be registered with any Claude app, so the Assistant menu cannot
    /// hand mail to Claude Desktop or Claude Code either. Settings → AI
    /// then greys the Assistant group out, says where an assistant comes
    /// from (`usesSubtitle`) and offers Set Up in Integrations…. False
    /// while the registration is not known.
    public static func nothingSetUp(_ choices: [ProviderChoice], registered: Bool, known: Bool) -> Bool {
        guard known, !registered else { return false }
        return !choices.contains { $0.available }
    }
}
