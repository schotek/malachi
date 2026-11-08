// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// The view logic of the two account pages of the settings, a 1:1 port of
/// ui/internal/integrations: Accounts, which lists the mail accounts (kind
/// imap, also the empty kind, and graph), and Integrations, which lists
/// every other account — the services beside the mail, today Jira, CalDAV
/// calendars, eWay-CRM and AI provider accounts (kind `ai`,
/// docs/plans/ai-api.md D3); a kind added later lands there by itself —
/// and the catalogue of services that can be added: one group, the
/// services first and then the AI, which a capsule tells apart
/// (docs/plans/ai-api.md §7.2).
///
/// The AI section also names three integrations that are not accounts of
/// the daemon but state of this client (D15): Claude Desktop (the bridge
/// registered in Claude Desktop), Claude Code (the bridge registered in
/// Claude Code, and the Claude Code the panel runs) and ChatGPT (Codex).
/// Claude Desktop and Claude Code were one integration with one Register
/// switch for both apps earlier; each now
/// registers the bridge in its own app only (`malachi-mcp install
/// --client`, `clientID`). Each is in one place at a time: in the catalogue
/// with Add… while it is not connected, and as a row after the daemon's
/// accounts once it is (`localRows`, from the state the window has;
/// `offered` says which catalogue entries remain); they are never in
/// `fullOrder`.
///
/// Both pages show rows of the same shape (`Row`): the switch, the editor
/// and the sentence of the remove confirmation depend on the kind. The
/// order of the accounts is one list owned by the daemon; a page that
/// reorders its own rows sends the whole list (`fullOrder`), mail accounts
/// first, so account.reorder never has to guess where the other page's
/// accounts go. Texts go through `L10n` with the GTK msgids.
public enum Integrations {
    /// integrations.ShowsInAccounts: a mail account (imap, the empty kind,
    /// graph).
    public static func showsInAccounts(_ a: Account) -> Bool {
        let kind = a.config.protocolKind
        return kind == .imap || kind == .graph
    }

    /// integrations.ShowsInIntegrations: every account that is not a mail
    /// account.
    public static func showsInIntegrations(_ a: Account) -> Bool {
        !showsInAccounts(a)
    }

    /// integrations.Split: the mail accounts and the integrations, each in
    /// the order of `accounts`.
    public static func split(_ accounts: [Account]) -> (mail: [Account], integrations: [Account]) {
        var mail: [Account] = []
        var other: [Account] = []
        for a in accounts {
            if showsInAccounts(a) {
                mail.append(a)
            } else {
                other.append(a)
            }
        }
        return (mail, other)
    }

    /// integrations.Page: one of the two account pages.
    public enum Page: Sendable, Equatable {
        case accounts
        case integrations
    }

    /// integrations.FullOrder: the order account.reorder gets when `page`
    /// was reordered to `order`: the mail accounts first, then the
    /// integrations. The reordered page's accounts follow `order`; the
    /// other page's keep their order in `all`. An id of `order` that is not
    /// in `all`, or that belongs to the other page, is ignored, a repeated
    /// one counts once, and an account of the page that `order` omits
    /// follows the listed ones in its order in `all`. The result names
    /// every account of `all` exactly once.
    public static func fullOrder(_ all: [Account], page: Page, order: [AccountID]) -> [AccountID] {
        let (mail, other) = split(all)
        func ordered(_ group: [Account], reordered: Bool) -> [AccountID] {
            guard reordered else { return group.map(\.id) }
            let inGroup = Set(group.map(\.id))
            var seen = Set<AccountID>()
            var ids: [AccountID] = []
            for id in order where inGroup.contains(id) && !seen.contains(id) {
                seen.insert(id)
                ids.append(id)
            }
            for a in group where !seen.contains(a.id) {
                seen.insert(a.id)
                ids.append(a.id)
            }
            return ids
        }
        return ordered(mail, reordered: page == .accounts) + ordered(other, reordered: page == .integrations)
    }

    // Icons of the kinds: the GTK symbolic icon names, which the AppKit
    // side maps to SF Symbols. A mail account's row shows the envelope.
    public static let mailIcon = "mail-unread-symbolic"
    public static let jiraIcon = "checkbox-checked-symbolic"
    public static let calDAVIcon = "x-office-calendar-symbolic"
    /// integrations.EwayIcon (`CRM.icon`; Adwaita has no CRM icon).
    public static let ewayIcon = CRM.icon
    /// integrations.AIIcon: the icon of an AI provider account and of the
    /// local AI integrations (the assistant's sparkle).
    public static let aiIcon = "malachi-assistant-symbolic"

    /// integrations.Section: a section of the catalogue: its order, not a
    /// heading of the page.
    public enum Section: Sendable, Equatable {
        /// Jira, CalDAV calendars, eWay-CRM.
        case services
        /// The AI integrations (docs/plans/ai-api.md §7.2).
        case ai
    }

    /// integrations.LocalKind: an integration that is state of this
    /// client, not an account of the daemon (docs/plans/ai-api.md D15).
    /// `none` marks a catalogue entry that adds an account.
    public enum LocalKind: Sendable, Equatable {
        case none
        /// Claude Desktop: the bridge registered in it (its sheet: the
        /// Register switch and the row of a restart Claude Desktop has not
        /// done yet).
        case claudeDesktop
        /// Claude Code: the bridge registered in it and the Claude Code the
        /// panel runs (its sheet: the Register switch and the Claude Code
        /// row with Choose…, Sign In…, Get Claude Code…).
        case claudeCode
        /// ChatGPT through Codex.
        case chatgpt
    }

    /// integrations.ClientID: the id of the Claude app of a local kind in
    /// the bridge's report and its `--client` flag
    /// (`Assistant.Target.clientID`); nil for a kind that is not a Claude
    /// app (Go's "").
    public static func clientID(_ kind: LocalKind) -> String? {
        switch kind {
        case .claudeDesktop: return "claude-desktop"
        case .claudeCode: return "claude-code"
        case .chatgpt, .none: return nil
        }
    }

    /// integrations.Service: one entry of the catalogue: an account kind to
    /// add (`kind`; for an AI provider account also `provider`), or a local
    /// integration (`local`, with a nil `kind`).
    public struct Service: Sendable, Equatable {
        /// integrations.Service.Key: what tells this entry from every
        /// other of the catalogue (a button's identifier): the kind, or the
        /// local integration ("claude-desktop", "claude-code", "chatgpt"),
        /// and for an AI provider the provider too, since three entries
        /// share the kind `ai` ("ai:anthropic").
        public var key: String {
            let base: String
            if let kind {
                base = kind.rawValue
            } else {
                switch local {
                case .claudeDesktop, .claudeCode: base = Integrations.clientID(local) ?? ""
                case .chatgpt: base = "chatgpt"
                case .none: base = ""
                }
            }
            guard let provider else { return base }
            return base + ":" + provider.rawValue
        }

        public var section: Section
        /// nil for a local integration (Go's empty kind).
        public var kind: AccountKind?
        /// Set only with `kind` `.ai`.
        public var provider: AIProvider?
        /// Set only with a nil `kind`.
        public var local: LocalKind
        /// The service's name; Jira, eWay-CRM, Claude and ChatGPT are
        /// brands, not translated.
        public var name: String
        public var description: String
        /// The GTK symbolic icon name.
        public var icon: String
        /// The capsule after the name (`badges`); "" for none.
        public var badge: String

        public init(
            section: Section = .services, kind: AccountKind? = nil, provider: AIProvider? = nil, local: LocalKind = .none,
            name: String, description: String, icon: String, badge: String = ""
        ) {
            self.section = section
            self.kind = kind
            self.provider = provider
            self.local = local
            self.name = name
            self.description = description
            self.icon = icon
            self.badge = badge
        }
    }

    /// integrations.BadgeTexts: the capsules that tell the AI integrations
    /// apart: the local ones use apps
    /// installed on this computer, the provider accounts an API key over
    /// the network. The same capsule is on the row of Connected Services.
    public struct BadgeTexts: Sendable, Equatable {
        public var thisComputer: String
        public var apiKey: String
    }

    /// integrations.Badges: the capsules' texts.
    public static func badges() -> BadgeTexts {
        BadgeTexts(
            // TRANSLATORS: capsule on an AI integration that uses the apps installed on this computer (Claude Code, Claude Desktop, Codex).
            thisComputer: L10n.T("This computer"),
            // TRANSLATORS: capsule on an AI provider account that uses an API key.
            apiKey: L10n.T("API key"))
    }

    /// integrations.Catalog: the services that can be added, in the order
    /// the page lists them in its one group: the services first, then the
    /// AI section.
    public static func catalog() -> [Service] {
        services() + aiServices()
    }

    /// integrations.CatalogSection: the entries of one section of the
    /// catalogue, in its order.
    public static func catalogSection(_ s: Section) -> [Service] {
        catalog().filter { $0.section == s }
    }

    /// integrations.Offered: whether catalogue entry `s` is shown with the
    /// local integrations in the states `claude` and `chatgpt`: an account
    /// kind always (another account can be added), a local integration
    /// only while it is not connected — connected, it is a row of Connected
    /// Services instead.
    public static func offered(_ s: Service, claude: ClaudeState, chatgpt: ChatGPTState) -> Bool {
        s.local == .none || !localConnected(s.local, claude: claude, chatgpt: chatgpt)
    }

    /// integrations.aiServices: the AI section of the catalogue
    /// (docs/plans/ai-api.md §7.2): the three local integrations, then the
    /// three provider kinds.
    static func aiServices() -> [Service] {
        let names = Assistant.apiText()
        let badges = badges()
        return [
            Service(
                section: .ai, local: .claudeDesktop, name: claudeDesktopName,
                // TRANSLATORS: Preferences → Integrations: what Claude Desktop on this computer brings once the bridge is registered in it.
                description: L10n.T("Let Claude Desktop read your mail and prepare drafts through the MCP bridge"),
                icon: aiIcon, badge: badges.thisComputer),
            Service(
                section: .ai, local: .claudeCode, name: claudeCodeName,
                // TRANSLATORS: Preferences → Integrations: what Claude Code on this computer brings: the bridge registered in it, and the assistant in the app run with it.
                description: L10n.T("Let Claude Code read your mail and prepare drafts, and run the assistant in the app with it"),
                icon: aiIcon, badge: badges.thisComputer),
            Service(
                section: .ai, local: .chatgpt, name: chatGPTName,
                // TRANSLATORS: Preferences → Integrations: what ChatGPT (Codex) brings.
                description: L10n.T("Run the assistant in the app through Codex, using your ChatGPT plan"),
                icon: aiIcon, badge: badges.thisComputer),
            Service(
                section: .ai, kind: .ai, provider: .anthropic, name: names.anthropic,
                // TRANSLATORS: Preferences → Integrations: what an Anthropic API account brings.
                description: L10n.T("Run the assistant in the app over the Anthropic API with your own API key"),
                icon: aiIcon, badge: badges.apiKey),
            Service(
                section: .ai, kind: .ai, provider: .openai, name: names.openAI,
                // TRANSLATORS: Preferences → Integrations: what an OpenAI API account brings.
                description: L10n.T("Run the assistant in the app over the OpenAI API with your own API key"),
                icon: aiIcon, badge: badges.apiKey),
            Service(
                section: .ai, kind: .ai, provider: .openaiCompatible, name: names.openAICompatible,
                // TRANSLATORS: Preferences → Integrations: what an OpenAI-compatible server brings; the product names stay as they are.
                description: L10n.T("Ollama, llama.cpp, LM Studio, vLLM and gateways such as OpenRouter: a model on your computer or a server of your choice"),
                icon: aiIcon, badge: badges.apiKey),
        ]
    }

    /// integrations.services: the catalogue's first section.
    static func services() -> [Service] {
        [
            Service(
                section: .services, kind: .jira, name: jiraName,
                description: L10n.T("Issues read as mail, comments and status changes"), icon: jiraIcon),
            Service(
                section: .services, kind: .caldav, name: L10n.T("CalDAV Calendar"),
                description: L10n.T("Nextcloud, iCloud, Fastmail and other CalDAV servers"), icon: calDAVIcon),
            Service(
                section: .services, kind: .eway, name: CRM.brand,
                // TRANSLATORS: Preferences → Integrations: what an eWay-CRM
                // account brings ("deals" as eWay-CRM's sales opportunities).
                description: L10n.T("Save messages to your CRM with their contacts, companies and deals"),
                icon: ewayIcon),
        ]
    }

    /// The brands, never translated.
    static let jiraName = "Jira"
    static let claudeDesktopName = "Claude Desktop"
    static let claudeCodeName = "Claude Code"
    static let chatGPTName = "ChatGPT (Codex)"

    /// integrations.Editor: what a row's Edit button opens.
    public enum Editor: Sendable, Equatable {
        /// No Edit button (a kind this client does not know).
        case none
        /// The mail account assistant in its edit mode.
        case mailWizard
        /// A Jira account's settings.
        case jira
        /// The Calendar page of the settings, where a CalDAV account's
        /// server, password, certificate and calendars are.
        case calendarPage
        /// The eWay-CRM account assistant in its edit mode
        /// (`EWayWizardController`): the user name, the password and the
        /// name; the service address stays.
        case eway
        /// An AI provider account's dialog in its edit mode
        /// (`AIAccountController`): the name, the model, the key (Replace
        /// Key…), the server of an OpenAI-compatible one; the provider
        /// stays.
        case ai
        /// The settings of Claude Desktop: its Register with Claude switch
        /// (and the pending restart).
        case claudeDesktop
        /// The settings of Claude Code: its Register with Claude switch,
        /// where Claude Code is, its sign-in.
        case claudeCode
        /// The settings of ChatGPT (Codex): Continue with ChatGPT, where
        /// Codex is, the model.
        case chatgpt
    }

    /// integrations.Row: what an account's row on either page shows besides
    /// its status. Plain texts; nothing is markup.
    public struct Row: Sendable, Equatable {
        public var id: AccountID
        public var kind: AccountKind
        public var title: String
        public var subtitle: String
        public var icon: String
        /// The capsule after the name (an AI provider account's "API
        /// key"); "" for none.
        public var badge = ""
        /// The Enabled switch (account.setEnabled pauses the sync, or an
        /// eWay-CRM account's saving; CalDAV has no pause).
        public var offersSwitch: Bool
        public var editor: Editor
        /// The Edit button's tooltip.
        public var editLabel: String
        public var removeBody: String
    }

    /// integrations.RowFor. Title: the account name as the user typed it
    /// (nothing trimmed), else the Jira site's host, else the address (an
    /// AI provider account without a name: its provider). Subtitle: the
    /// host of the Jira site or of the CalDAV server, the host and path of
    /// the eWay-CRM service, the provider and model of an AI provider
    /// account ("Anthropic API · claude-opus-5-5"), else — and when the
    /// title shows the host already — the address.
    public static func rowFor(_ a: Account) -> Row {
        let kind = a.config.protocolKind
        let t = title(a)
        var r = Row(
            id: a.id, kind: kind, title: t, subtitle: subtitle(a), icon: mailIcon, offersSwitch: true,
            editor: .none, editLabel: "", removeBody: "")
        switch kind {
        case .imap, .graph:
            r.editor = .mailWizard
            r.editLabel = L10n.T("Edit Account")
            // TRANSLATORS: %s is the account's e-mail address.
            r.removeBody = L10n.T("%s will be removed from Malachi Mail. Mail on the server is not affected.", a.config.email)
        case .jira:
            r.icon = jiraIcon
            r.editor = .jira
            r.editLabel = L10n.T("Edit Account")
            // TRANSLATORS: %s is the account's name (or its site's host).
            r.removeBody = L10n.T("%s will be removed from Malachi Mail. Issues on the Jira site are not affected.", t)
        case .caldav:
            r.icon = calDAVIcon
            r.editor = .calendarPage
            r.editLabel = calendarSettings
            r.offersSwitch = false
            // TRANSLATORS: %s is the calendar account's name.
            r.removeBody = L10n.T("%s will be removed from Malachi Mail. Calendars on the server are not affected.", t)
        case .eway:
            r.icon = ewayIcon
            r.editor = .eway
            r.editLabel = L10n.T("Edit Account")
            // TRANSLATORS: %s is the eWay-CRM account's name.
            r.removeBody = L10n.T("%s will be removed from Malachi Mail. Messages saved in eWay-CRM stay there.", t)
        case .ai:
            r.icon = aiIcon
            r.editor = .ai
            r.editLabel = L10n.T("Edit Account")
            if r.title.isEmpty {
                r.title = aiProvider(a)
            }
            r.subtitle = aiSubtitle(a)
            r.badge = badges().apiKey
            // TRANSLATORS: %s is the AI provider account's name; its API key leaves the keyring with it.
            r.removeBody = L10n.T("%s will be removed from Malachi Mail, and its API key with it.", r.title)
        default:
            // TRANSLATORS: %s is the account's name.
            r.removeBody = L10n.T("%s will be removed from Malachi Mail.", t)
        }
        return r
    }

    /// The name of the row: the name, the Jira site's host, the address.
    static func title(_ a: Account) -> String {
        if !a.config.name.isEmpty {
            return a.config.name
        }
        let h = Jira.siteHost(a.config)
        if !h.isEmpty {
            return h
        }
        return a.config.email
    }

    /// The line under the name: the host, unless the title shows it
    /// already; else the address.
    static func subtitle(_ a: Account) -> String {
        let h = host(a)
        return !h.isEmpty && h != title(a) ? h : a.config.email
    }

    /// integrations.aiProvider: the name of an AI provider account's
    /// provider kind.
    static func aiProvider(_ a: Account) -> String {
        guard let ai = a.config.ai else { return Assistant.providerName(AIProvider(rawValue: "")) }
        return Assistant.providerName(ai.provider)
    }

    /// integrations.aiSubtitle: "<provider> · <model>" of an AI provider
    /// account; the provider alone without a model. The model id is
    /// cleaned as any string the daemon stores from the user's input.
    static func aiSubtitle(_ a: Account) -> String {
        var s = aiProvider(a)
        guard let ai = a.config.ai else { return s }
        let m = Jira.clean(ai.model)
        if !m.isEmpty {
            s += " · " + m
        }
        return s
    }

    /// The host the subtitle shows: a Jira account's site, a CalDAV
    /// account's server, an eWay-CRM account's service (with its path,
    /// which tells two instances on one host apart), the host of an AI
    /// provider account's effective base URL; empty for anything else.
    static func host(_ a: Account) -> String {
        switch a.config.protocolKind {
        case .ai:
            guard let ai = a.config.ai, let u = Jira.parseURL(Jira.trimSpace(ai.effectiveBaseURL)) else { return "" }
            return Jira.clean(u.hostname.lowercased())
        case .jira:
            return Jira.siteHost(a.config)
        case .caldav:
            guard let c = a.config.caldav, let u = Jira.parseURL(Jira.trimSpace(c.serverUrl)) else { return "" }
            return Jira.clean(u.hostname.lowercased())
        case .eway:
            return CRM.serviceHost(a)
        default:
            return ""
        }
    }

    // MARK: Local integrations (D15)

    /// integrations.ClaudeState: what this client knows of the Claude apps
    /// on this computer (the window fills it from its Assistant state):
    /// whether the bridge is registered in each app (the last malachi-mcp
    /// status: `MCPStatus.registered(in:)`), and the Claude Code the panel
    /// runs — found, where, which version, and what claude auth status
    /// said (`signInKnown` false while it did not answer).
    public struct ClaudeState: Sendable, Equatable {
        public var desktopRegistered = false
        public var codeRegistered = false
        public var present = false
        public var path = ""
        public var version = ""
        public var signInKnown = false
        public var signedIn = false

        /// integrations.ClaudeState.Registered: the bridge is registered in
        /// at least one of the two apps, what the Assistant as a whole
        /// needs (`Assistant.shown`, `nothingSetUp`).
        public var registered: Bool { desktopRegistered || codeRegistered }

        public init(
            desktopRegistered: Bool = false, codeRegistered: Bool = false, present: Bool = false, path: String = "",
            version: String = "", signInKnown: Bool = false, signedIn: Bool = false
        ) {
            self.desktopRegistered = desktopRegistered
            self.codeRegistered = codeRegistered
            self.present = present
            self.path = path
            self.version = version
            self.signInKnown = signInKnown
            self.signedIn = signedIn
        }
    }

    /// integrations.ChatGPTState: what this client knows of ChatGPT
    /// (Codex): whether the user continued with ChatGPT (`connected`, as
    /// `account`), and whether Codex was found.
    public struct ChatGPTState: Sendable, Equatable {
        public var connected = false
        public var account = ""
        public var codexFound = false

        public init(connected: Bool = false, account: String = "", codexFound: Bool = false) {
            self.connected = connected
            self.account = account
            self.codexFound = codexFound
        }
    }

    /// integrations.LocalRow: the row of a local integration on the
    /// Integrations page, shown after the daemon's accounts: plain texts
    /// (nothing is markup), the icon, the "This computer" capsule, what its
    /// Edit button opens, whether it has a Remove button (unregister,
    /// disconnect), what that is called and the confirmation it asks first,
    /// and for a Claude app the client Remove unregisters the bridge from
    /// (`clientID`; nil for ChatGPT).
    public struct LocalRow: Sendable, Equatable {
        public var kind: LocalKind
        public var title: String
        public var subtitle: String
        public var editor: Editor
        public var icon: String
        public var badge: String
        public var canRemove: Bool
        public var removeLabel: String
        public var removeHeading: String
        public var removeBody: String
        public var clientID: String?
    }

    /// integrations.LocalKinds: the local integrations in the order of the
    /// page.
    public static let localKinds: [LocalKind] = [.claudeDesktop, .claudeCode, .chatgpt]

    /// integrations.LocalConnected: whether local integration `kind` is
    /// connected: the bridge registered in that Claude app, or ChatGPT
    /// continued with. A connected one has a row of Connected Services and
    /// leaves the catalogue (`offered`); Remove on its row disconnects it.
    public static func localConnected(_ kind: LocalKind, claude: ClaudeState, chatgpt: ChatGPTState) -> Bool {
        switch kind {
        case .claudeDesktop: return claude.desktopRegistered
        case .claudeCode: return claude.codeRegistered
        case .chatgpt: return chatgpt.connected
        case .none: return false
        }
    }

    /// integrations.LocalRows: the rows of the connected local
    /// integrations, in the order of `localKinds`, from `localRowFor`; none
    /// while nothing is connected.
    public static func localRows(claude: ClaudeState, chatgpt: ChatGPTState) -> [LocalRow] {
        var rows: [LocalRow] = []
        for kind in localKinds {
            if let r = localRowFor(kind, claude: claude, chatgpt: chatgpt), r.shown {
                rows.append(r.row)
            }
        }
        return rows
    }

    /// integrations.LocalRowFor: the row of local integration `kind`, and
    /// whether the page shows it (`localConnected`; a view that keeps a
    /// widget per kind hides the row of one that is not connected); nil for
    /// `.none`.
    ///
    /// Claude Desktop says "MCP bridge registered". Claude Code says the
    /// same, then the Claude Code found (its version, else its path) or
    /// "Claude Code not found", and "signed in" or "not signed in" once
    /// known. ChatGPT (Codex) says "Connected as …" and "Codex not found"
    /// when it is missing. Remove is Unregister for a Claude app — the
    /// bridge removed from that app only (`clientID`; Claude Desktop's goes
    /// through the "Restart Claude Desktop?" question like its switch,
    /// Claude Code's does not) — and Disconnect for ChatGPT; each asks
    /// `removeHeading` and `removeBody` first. `canRemove` is the row's
    /// being shown.
    public static func localRowFor(_ kind: LocalKind, claude: ClaudeState, chatgpt: ChatGPTState) -> (row: LocalRow, shown: Bool)? {
        let shown = localConnected(kind, claude: claude, chatgpt: chatgpt)
        let badge = badges().thisComputer
        switch kind {
        case .claudeDesktop, .claudeCode:
            // TRANSLATORS: Preferences → Integrations, the row of Claude Desktop or Claude Code: the malachi-mcp bridge is registered in that app.
            var parts = [L10n.T("MCP bridge registered")]
            var row = LocalRow(
                kind: kind, title: claudeDesktopName, subtitle: "", editor: .claudeDesktop, icon: aiIcon, badge: badge,
                canRemove: shown,
                // TRANSLATORS: the Remove button of the Claude Desktop and Claude Code rows in Preferences → Integrations: it unregisters the bridge from that app.
                removeLabel: L10n.T("Unregister"), removeHeading: "", removeBody: "", clientID: clientID(kind))
            if kind == .claudeCode {
                row.title = claudeCodeName
                row.editor = .claudeCode
                if !claude.present {
                    parts.append(L10n.T("Claude Code not found"))
                } else if !claude.version.isEmpty {
                    parts.append(Jira.clean(claude.version))
                } else if !claude.path.isEmpty {
                    parts.append(Jira.clean(claude.path))
                }
                if claude.present, claude.signInKnown {
                    if claude.signedIn {
                        // TRANSLATORS: Preferences → Integrations, the Claude Code row: Claude Code is signed in (lower case, after " · ").
                        parts.append(L10n.T("signed in"))
                    } else {
                        // TRANSLATORS: Preferences → Integrations, the Claude Code row: Claude Code is not signed in (lower case, after " · ").
                        parts.append(L10n.T("not signed in"))
                    }
                }
            }
            row.subtitle = parts.joined(separator: " · ")
            // TRANSLATORS: confirmation of the Unregister of the Claude Desktop or Claude Code row in Preferences → Integrations; %s is the app's name.
            row.removeHeading = L10n.T("Unregister from %s?", row.title)
            // TRANSLATORS: body of that confirmation; %s is the app's name (Claude Desktop, Claude Code).
            row.removeBody = L10n.T("%s will no longer read your mail through Malachi Mail.", row.title)
            return (row, shown)
        case .chatgpt:
            // assistant.ChatGPTText.Connected (chatgpt.go).
            var parts = [L10n.T("Connected as %s", Jira.clean(chatgpt.account))]
            if !chatgpt.codexFound {
                parts.append(L10n.T("Codex not found"))
            }
            let row = LocalRow(
                kind: .chatgpt, title: chatGPTName, subtitle: parts.joined(separator: " · "),
                editor: .chatgpt, icon: aiIcon, badge: badge, canRemove: shown,
                // assistant.ChatGPTText.Disconnect (chatgpt.go).
                removeLabel: L10n.T("Disconnect"),
                // TRANSLATORS: confirmation of the ChatGPT row's Disconnect in Preferences → Integrations.
                removeHeading: L10n.T("Disconnect ChatGPT?"),
                removeBody: L10n.T("Malachi Mail signs out of ChatGPT on this computer. You can connect it again any time."),
                clientID: nil)
            return (row, shown)
        case .none:
            return nil
        }
    }

    /// integrations.ClaudeAppTexts: the texts of a Claude app's sheet (the
    /// Settings of its row and the Add… of its catalogue entry): the app's
    /// name as the sheet's title, and its Register with Claude switch,
    /// which registers the bridge in that app only.
    public struct ClaudeAppTexts: Sendable, Equatable {
        /// The app's name (a brand): the sheet's title.
        public var title = ""
        /// The Register switch's.
        public var switchTitle = ""
        public var switchSubtitle = ""
        /// The switch's subtitle while the bridge reports the app as not
        /// present (`registerSwitchFor`), and the toast of a registration
        /// that failed because the app was not found.
        public var notInstalled = ""

        public init(title: String = "", switchTitle: String = "", switchSubtitle: String = "", notInstalled: String = "") {
            self.title = title
            self.switchTitle = switchTitle
            self.switchSubtitle = switchSubtitle
            self.notInstalled = notInstalled
        }
    }

    /// integrations.ClaudeAppText: the texts of the sheet of Claude app
    /// `kind` (`.claudeDesktop`, `.claudeCode`); empty for another kind.
    public static func claudeAppText(_ kind: LocalKind) -> ClaudeAppTexts {
        var t = ClaudeAppTexts()
        switch kind {
        case .claudeDesktop:
            t.title = claudeDesktopName
            t.notInstalled = L10n.T("Claude Desktop is not installed")
        case .claudeCode:
            t.title = claudeCodeName
            t.notInstalled = L10n.T("Claude Code is not installed, or has not been used in a terminal yet")
        case .chatgpt, .none:
            return ClaudeAppTexts()
        }
        // TRANSLATORS: the switch of the Claude Desktop and Claude Code dialogs in Preferences → Integrations.
        t.switchTitle = L10n.T("Register with Claude")
        // TRANSLATORS: under the switch Register with Claude; %s is the app's name (Claude Desktop, Claude Code).
        t.switchSubtitle = L10n.T("Adds the malachi-mcp bridge to %s on this computer", t.title)
        return t
    }

    /// integrations.RegisterSwitch: how a Claude app's Register switch
    /// shows the bridge's report for its app: on, whether it can be
    /// flipped, and its subtitle.
    public struct RegisterSwitch: Sendable, Equatable {
        public var on = false
        public var sensitive = false
        public var subtitle = ""

        public init(on: Bool = false, sensitive: Bool = false, subtitle: String = "") {
            self.on = on
            self.sensitive = sensitive
            self.subtitle = subtitle
        }
    }

    /// integrations.RegisterSwitchFor: the Register switch of Claude app
    /// `kind` for what the bridge last reported about that app (`known`
    /// false while no report came: off and waiting): on while registered;
    /// flippable while registered or present; while the app is neither,
    /// insensitive with `notInstalled` as its subtitle instead of
    /// `switchSubtitle`. A view keeps it insensitive while its own install
    /// or uninstall runs.
    public static func registerSwitchFor(_ kind: LocalKind, known: Bool, present: Bool, registered: Bool) -> RegisterSwitch {
        let t = claudeAppText(kind)
        var sw = RegisterSwitch(subtitle: t.switchSubtitle)
        guard known else { return sw }
        sw.on = registered
        sw.sensitive = registered || present
        if !sw.sensitive {
            sw.subtitle = t.notInstalled
        }
        return sw
    }

    /// integrations.PageTexts: the fixed texts of the Integrations page.
    public struct PageTexts: Sendable, Equatable {
        /// The page's title in the settings' toolbar.
        public var title: String
        /// Under the title.
        public var description: String
        /// The group of the added services.
        public var connectedTitle: String
        /// The group of the catalogue.
        public var availableTitle: String
        /// The connected group's placeholder row.
        public var emptyTitle: String
        public var emptySubtitle: String
        /// A catalogue row's button.
        public var add: String
        /// A local row's Edit tooltip.
        public var settings: String
        /// A CalDAV row's Edit tooltip.
        public var calendarSettings: String
        // The confirmations of the local rows' Remove are each row's
        // (`LocalRow.removeHeading`, `removeBody`).
    }

    /// integrations.Texts.
    public static func texts() -> PageTexts {
        PageTexts(
            title: L10n.T("Integrations"),
            description: L10n.T("Services beside your mail: issue trackers, calendars, CRM and AI"),
            connectedTitle: L10n.T("Connected Services"),
            availableTitle: L10n.T("Available Services"),
            emptyTitle: L10n.T("No integrations yet"),
            emptySubtitle: L10n.T("Add one from the list below"),
            add: L10n.T("Add…"),
            // TRANSLATORS: tooltip of the Edit button of a local integration (Claude Desktop, Claude Code, ChatGPT) in Preferences → Integrations.
            settings: L10n.T("Settings"),
            calendarSettings: calendarSettings)
    }

    /// What a CalDAV row's Edit opens.
    static var calendarSettings: String {
        L10n.T("Calendar Settings")
    }
}
