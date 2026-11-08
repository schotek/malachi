// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/Integrations.swift; GTK:
// ui/internal/integrations/integrations.go (ShowsInAccounts,
// ShowsInIntegrations, Split, Page, FullOrder, MailIcon, JiraIcon,
// CalDAVIcon, EwayIcon, AIIcon, Section, LocalKind, Service, BadgeTexts,
// ClientID, Badges, Catalog, CatalogSection, Offered, Editor, Row, RowFor,
// ClaudeState, ChatGPTState, LocalRow, LocalKinds, LocalConnected,
// LocalRows, LocalRowFor, ClaudeAppTexts, ClaudeAppText, RegisterSwitch,
// RegisterSwitchFor, PageTexts, Texts).
//
// The view logic of the two account pages of the preferences: Accounts,
// which lists the mail accounts (kind imap, the empty kind, and graph),
// and Integrations, which lists every other account — the services beside
// the mail, today Jira (kind jira), CalDAV calendars (kind caldav),
// eWay-CRM (kind eway) and AI provider accounts (kind ai,
// docs/plans/ai-api.md D3); a kind added later lands there by itself — and
// the catalogue of services that can be added: one group, the services
// first and then the AI, which a capsule tells apart (docs/plans/ai-api.md
// §7.2). The AI section also names three
// integrations that are not accounts of the daemon but state of this client
// (D15): Claude Desktop (the bridge registered in Claude Desktop), Claude
// Code (the bridge registered in Claude Code, and the Claude Code the panel
// runs) and ChatGPT (Codex). Claude Desktop and Claude Code were one
// integration with one Register switch for both apps earlier (one
// shared switch was
// confusing); each now registers the bridge in its own app only
// (malachi-mcp install --client, ClientId). Each is in one
// place at a time: in the catalogue with Add… while it is not connected,
// and as a row after the daemon's accounts once it is (LocalRows; Offered
// says which catalogue entries remain); they are never in FullOrder. A
// catalogue entry's Key tells it from every other (three entries share the
// kind ai), as Swift's Service.key does (§15.6). Both pages show rows of the
// same shape (Row): the switch, the editor and the sentence of the remove
// confirmation depend on the kind. The order of the accounts is one list owned by the daemon; a
// page that reorders its own rows sends the whole list (FullOrder), mail
// accounts first, so account.reorder never has to guess where the other
// page's accounts go.
//
// Windows: Go's package is this static class and its types are nested
// with Go's names (Integrations.Page, Integrations.Service,
// Integrations.Editor, Integrations.Row, Integrations.PageTexts; Go's
// constants PageAccounts, EditorJira, … are the enums' members Accounts,
// Jira, …). Go's Translator is L10n, as in the other ports: every text is
// the GTK msgid, so RowFor, Catalog and Texts take no translator.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Compose;
using Malachi.Core.I18n;
using Malachi.Core.IssueTrackers;

namespace Malachi.Core.Model;

/// <summary>The two account pages of the preferences (Go's package <c>integrations</c>).</summary>
public static class Integrations
{
    /// <summary>integrations.MailIcon: a mail account's icon when its provider has none (GTK name).</summary>
    public const string MailIcon = "mail-unread-symbolic";

    /// <summary>integrations.JiraIcon: a Jira account's (Adwaita has no ticket).</summary>
    public const string JiraIcon = "checkbox-checked-symbolic";

    /// <summary>integrations.CalDAVIcon: a CalDAV account's.</summary>
    public const string CalDAVIcon = "x-office-calendar-symbolic";

    /// <summary>integrations.EwayIcon: an eWay-CRM account's (CrmRules.Icon; Adwaita has no CRM icon).</summary>
    public const string EwayIcon = Crm.CrmRules.Icon;

    /// <summary>
    /// integrations.AIIcon: the icon of an AI provider account and of the
    /// local AI integrations (the assistant's sparkle).
    /// </summary>
    public const string AiIcon = "malachi-assistant-symbolic";

    // The brands, never translated.
    private const string JiraName = "Jira";
    private const string ClaudeDesktopName = "Claude Desktop";
    private const string ClaudeCodeName = "Claude Code";
    private const string ChatGptName = "ChatGPT (Codex)";

    /// <summary>integrations.Section: a section of the catalogue: its order, not a heading of the page.</summary>
    public enum Section
    {
        /// <summary>SectionServices: Jira, CalDAV calendars, eWay-CRM.</summary>
        Services,

        /// <summary>SectionAI: the AI integrations (docs/plans/ai-api.md §7.2).</summary>
        Ai,
    }

    /// <summary>
    /// integrations.LocalKind: an integration that is state of this client,
    /// not an account of the daemon (docs/plans/ai-api.md D15).
    /// <see cref="None"/> marks a catalogue entry that adds an account.
    /// </summary>
    public enum LocalKind
    {
        /// <summary>A catalogue entry that adds an account.</summary>
        None,

        /// <summary>
        /// LocalClaudeDesktop: Claude Desktop, the bridge registered in it
        /// (its window: the Register switch and the row of a restart Claude
        /// Desktop has not done yet).
        /// </summary>
        ClaudeDesktop,

        /// <summary>
        /// LocalClaudeCode: Claude Code, the bridge registered in it and the
        /// Claude Code the panel runs (its window: the Register switch and
        /// the Claude Code row with Choose…, Sign In…, Get Claude Code…).
        /// </summary>
        ClaudeCode,

        /// <summary>ChatGPT through Codex.</summary>
        ChatGpt,
    }

    /// <summary>integrations.Page: one of the two account pages.</summary>
    public enum Page
    {
        /// <summary>PageAccounts: the mail accounts.</summary>
        Accounts,

        /// <summary>PageIntegrations: every other account.</summary>
        Integrations,
    }

    /// <summary>integrations.Editor: what a row's Edit button opens.</summary>
    public enum Editor
    {
        /// <summary>EditorNone: no Edit button (a kind this client does not know).</summary>
        None,

        /// <summary>EditorMailWizard: the mail account wizard in its edit mode.</summary>
        MailWizard,

        /// <summary>
        /// EditorJira: a Jira account's settings (the token assistant when a
        /// sign-in route asks for the token).
        /// </summary>
        Jira,

        /// <summary>
        /// EditorCalendarPage: the preferences switch to the Calendar page,
        /// where a CalDAV account's server, password, certificate and
        /// calendars are.
        /// </summary>
        CalendarPage,

        /// <summary>
        /// EditorEway: the eWay-CRM account assistant in its edit mode
        /// (EwayWizardController): the user name, the password, the name and
        /// the following of saved conversations; the service address stays.
        /// </summary>
        Eway,

        /// <summary>
        /// EditorAI: an AI provider account's dialog in its edit mode
        /// (AiAccountController): the name, the model, the key (Replace
        /// Key…), the server of an OpenAI-compatible one; the provider stays.
        /// </summary>
        Ai,

        /// <summary>
        /// EditorClaudeDesktop: the settings of Claude Desktop: its Register
        /// with Claude switch (and the pending restart).
        /// </summary>
        ClaudeDesktop,

        /// <summary>
        /// EditorClaudeCode: the settings of Claude Code: its Register with
        /// Claude switch, where Claude Code is, its sign-in.
        /// </summary>
        ClaudeCode,

        /// <summary>EditorChatGPT: the settings of ChatGPT (Codex): Continue with ChatGPT, where Codex is, the model.</summary>
        ChatGpt,
    }

    /// <summary>
    /// integrations.Service: one entry of the catalogue on the Integrations
    /// page: an account kind to add (<see cref="Kind"/>; for an AI provider
    /// account also <see cref="Provider"/>), or a local integration
    /// (<see cref="Local"/>, with a null <see cref="Kind"/>).
    /// </summary>
    public sealed record Service
    {
        /// <summary>The section of the catalogue it belongs to.</summary>
        public Section Section { get; init; } = Section.Services;

        /// <summary>The kind of account it adds; null for a local integration (Go's empty kind).</summary>
        public AccountKind? Kind { get; init; }

        /// <summary>The provider of an AI provider account; set only with the kind ai.</summary>
        public AiProvider? Provider { get; init; }

        /// <summary>The local integration; <see cref="LocalKind.None"/> with a <see cref="Kind"/>.</summary>
        public LocalKind Local { get; init; } = LocalKind.None;

        /// <summary>The service's name; Jira, eWay-CRM, Claude and ChatGPT are brands, not translated.</summary>
        public required string Name { get; init; }

        /// <summary>What it brings.</summary>
        public required string Description { get; init; }

        /// <summary>A GTK symbolic icon name (IconGlyphs maps it).</summary>
        public required string Icon { get; init; }

        /// <summary>The capsule after the name (<see cref="Badges"/>); "" for none.</summary>
        public string Badge { get; init; } = "";

        /// <summary>
        /// What tells this entry from every other of the catalogue (a
        /// button's identifier): the kind, or the local integration, and for
        /// an AI provider the provider too, since three entries share the
        /// kind ai (Swift <c>Service.key</c>): "ai:anthropic", or
        /// "claude-desktop", "claude-code" (<see cref="ClientId"/>),
        /// "chatgpt".
        /// </summary>
        public string Key
        {
            get
            {
                var key = Kind is { } kind
                    ? kind.Value ?? ""
                    : Local switch
                    {
                        LocalKind.ClaudeDesktop or LocalKind.ClaudeCode => ClientId(Local),
                        LocalKind.ChatGpt => "chatgpt",
                        _ => "",
                    };
                return Provider is { } p ? key + ":" + p.Value : key;
            }
        }
    }

    /// <summary>
    /// integrations.BadgeTexts: the capsules that tell the AI integrations
    /// apart: the local ones use apps installed
    /// on this computer, the provider accounts an API key over the network.
    /// The same capsule is on the row of Connected Services.
    /// </summary>
    /// <param name="ThisComputer">The capsule of the local AI integrations.</param>
    /// <param name="ApiKey">The capsule of the AI provider accounts.</param>
    public sealed record BadgeTexts(string ThisComputer, string ApiKey);

    /// <summary>
    /// integrations.ClaudeState: what this client knows of the Claude apps on
    /// this computer (the window fills it from its Assistant state): whether
    /// the bridge is registered in each app (the last malachi-mcp status:
    /// <see cref="Controllers.McpStatus.RegisteredIn"/>), and the Claude Code
    /// the panel runs — found, where, which version, and what
    /// <c>claude auth status</c> said (<see cref="SignInKnown"/> false while
    /// it did not answer).
    /// </summary>
    public sealed record ClaudeState
    {
        /// <summary>The bridge is registered in Claude Desktop.</summary>
        public bool DesktopRegistered { get; init; }

        /// <summary>The bridge is registered in Claude Code.</summary>
        public bool CodeRegistered { get; init; }

        /// <summary>
        /// The bridge is registered in at least one of the two apps: what the
        /// Assistant as a whole needs (assistant.Shown, NothingSetUp).
        /// </summary>
        public bool Registered => DesktopRegistered || CodeRegistered;

        /// <summary>Claude Code was found.</summary>
        public bool Present { get; init; }

        /// <summary>Where.</summary>
        public string Path { get; init; } = "";

        /// <summary>Which version.</summary>
        public string Version { get; init; } = "";

        /// <summary>claude auth status answered.</summary>
        public bool SignInKnown { get; init; }

        /// <summary>It said signed in.</summary>
        public bool SignedIn { get; init; }
    }

    /// <summary>
    /// integrations.ChatGPTState: what this client knows of ChatGPT (Codex):
    /// whether the user continued with ChatGPT (<see cref="Connected"/>, as
    /// <see cref="Account"/>), and whether Codex was found.
    /// </summary>
    public sealed record ChatGptState
    {
        /// <summary>The user continued with ChatGPT.</summary>
        public bool Connected { get; init; }

        /// <summary>The ChatGPT account it is connected as.</summary>
        public string Account { get; init; } = "";

        /// <summary>Codex was found.</summary>
        public bool CodexFound { get; init; }
    }

    /// <summary>
    /// integrations.LocalRow: the row of a local integration on the
    /// Integrations page, shown after the daemon's accounts: plain texts
    /// (nothing is markup), the icon, the "This computer" capsule, what its
    /// Edit button opens, whether it has a Remove button (unregister,
    /// disconnect), what that is called and the confirmation it asks first,
    /// and for a Claude app the client Remove unregisters the bridge from
    /// (<see cref="ClientId"/>; "" for ChatGPT).
    /// </summary>
    public sealed record LocalRow
    {
        /// <summary>Which local integration.</summary>
        public required LocalKind Kind { get; init; }

        /// <summary>Its name.</summary>
        public required string Title { get; init; }

        /// <summary>Its state.</summary>
        public required string Subtitle { get; init; }

        /// <summary>What its Edit button opens.</summary>
        public required Editor Editor { get; init; }

        /// <summary>A GTK symbolic icon name.</summary>
        public required string Icon { get; init; }

        /// <summary>The capsule after the name.</summary>
        public required string Badge { get; init; }

        /// <summary>Whether it has a Remove button.</summary>
        public required bool CanRemove { get; init; }

        /// <summary>The Remove button's label (Unregister, Disconnect).</summary>
        public required string RemoveLabel { get; init; }

        /// <summary>The heading of the confirmation Remove asks first.</summary>
        public required string RemoveHeading { get; init; }

        /// <summary>Its body.</summary>
        public required string RemoveBody { get; init; }

        /// <summary>The bridge's id of the Claude app Remove unregisters from (<see cref="ClientId"/>); "" for ChatGPT.</summary>
        public string ClientId { get; init; } = "";
    }

    /// <summary>
    /// integrations.ClaudeAppTexts: the texts of a Claude app's window (the
    /// Settings of its row and the Add… of its catalogue entry): the app's
    /// name as the window's title, and its Register with Claude switch, which
    /// registers the bridge in that app only (<c>--client</c> with
    /// <see cref="ClientId"/>).
    /// </summary>
    /// <param name="Title">The app's name (a brand): the window's title.</param>
    /// <param name="SwitchTitle">The Register switch's title.</param>
    /// <param name="SwitchSubtitle">Its subtitle.</param>
    /// <param name="NotInstalled">
    /// The switch's subtitle while the bridge reports the app as not present
    /// (<see cref="RegisterSwitchFor"/>), and the toast of a registration that
    /// failed because the app was not found.
    /// </param>
    public sealed record ClaudeAppTexts(string Title, string SwitchTitle, string SwitchSubtitle, string NotInstalled);

    /// <summary>
    /// integrations.RegisterSwitch: how a Claude app's Register switch shows
    /// the bridge's report for its app: on, whether it can be flipped, and
    /// its subtitle.
    /// </summary>
    /// <param name="On">The switch is on.</param>
    /// <param name="Sensitive">It can be flipped.</param>
    /// <param name="Subtitle">Its subtitle.</param>
    public sealed record RegisterSwitch(bool On, bool Sensitive, string Subtitle);

    /// <summary>
    /// integrations.Row: what an account's row on either page shows besides
    /// its status: plain texts (nothing is markup), the icon, whether it has
    /// the Enabled switch (account.setEnabled pauses the sync, or an eWay-CRM
    /// account's saving; CalDAV has no pause), what its Edit button opens and is called, and the body of its
    /// remove confirmation.
    /// </summary>
    public sealed record Row
    {
        /// <summary>The account.</summary>
        public required AccountId Id { get; init; }

        /// <summary>Its kind, the empty one resolved.</summary>
        public required AccountKind Kind { get; init; }

        /// <summary>The name, else the Jira site's host, else the address.</summary>
        public required string Title { get; init; }

        /// <summary>The host of the Jira site or the CalDAV server, the host and path of the eWay-CRM service, else the address.</summary>
        public required string Subtitle { get; init; }

        /// <summary>A GTK symbolic icon name.</summary>
        public required string Icon { get; init; }

        /// <summary>The capsule after the name (an AI provider account's "API key"); "" for none.</summary>
        public string Badge { get; init; } = "";

        /// <summary>Whether the row has the Enabled switch.</summary>
        public required bool OffersSwitch { get; init; }

        /// <summary>What the Edit button opens; <see cref="Editor.None"/> hides it.</summary>
        public required Editor Editor { get; init; }

        /// <summary>The Edit button's tooltip; "" without one.</summary>
        public required string EditLabel { get; init; }

        /// <summary>The body of "Remove this account?".</summary>
        public required string RemoveBody { get; init; }
    }

    /// <summary>integrations.PageTexts: the fixed texts of the Integrations page.</summary>
    /// <param name="Title">The page's title in the preferences' navigation.</param>
    /// <param name="Description">Under the title.</param>
    /// <param name="ConnectedTitle">The group of the added services.</param>
    /// <param name="AvailableTitle">The group of the catalogue.</param>
    /// <param name="EmptyTitle">The connected group's placeholder row.</param>
    /// <param name="EmptySubtitle">Its subtitle.</param>
    /// <param name="Add">A catalogue row's button.</param>
    /// <param name="Settings">A local row's Edit tooltip.</param>
    /// <param name="CalendarSettings">A CalDAV row's Edit tooltip.</param>
    /// <remarks>The confirmations of the local rows' Remove are each row's (<see cref="LocalRow.RemoveHeading"/>, <see cref="LocalRow.RemoveBody"/>).</remarks>
    public sealed record PageTexts(
        string Title,
        string Description,
        string ConnectedTitle,
        string AvailableTitle,
        string EmptyTitle,
        string EmptySubtitle,
        string Add,
        string Settings,
        string CalendarSettings);

    /// <summary>
    /// integrations.ClientID: the id of the Claude app of local kind
    /// <paramref name="kind"/> in the bridge's report and its
    /// <c>--client</c> flag (<see cref="Controllers.McpStatus.ClientDesktop"/>,
    /// <see cref="Controllers.McpStatus.ClientCode"/>); ""
    /// for a kind that is not a Claude app.
    /// </summary>
    public static string ClientId(LocalKind kind) => kind switch
    {
        LocalKind.ClaudeDesktop => "claude-desktop",
        LocalKind.ClaudeCode => "claude-code",
        _ => "",
    };

    /// <summary>
    /// integrations.ShowsInAccounts: an account the Accounts page lists, a
    /// mail account (kind imap, also the empty kind, or graph).
    /// </summary>
    public static bool ShowsInAccounts(Account a)
    {
        ArgumentNullException.ThrowIfNull(a);
        var kind = KindOf(a);
        return kind == AccountKind.Imap || kind == AccountKind.Graph;
    }

    /// <summary>
    /// integrations.ShowsInIntegrations: an account the Integrations page
    /// lists, every account that is not a mail account.
    /// </summary>
    public static bool ShowsInIntegrations(Account a) => !ShowsInAccounts(a);

    /// <summary>
    /// Whether <paramref name="page"/> lists <paramref name="a"/> (Windows:
    /// <see cref="ShowsInAccounts"/> or <see cref="ShowsInIntegrations"/> by
    /// page, the filter of AccountsPageController).
    /// </summary>
    public static bool ShowsIn(Page page, Account a) =>
        page == Page.Accounts ? ShowsInAccounts(a) : ShowsInIntegrations(a);

    /// <summary>
    /// integrations.Split: <paramref name="accounts"/> divided into the mail
    /// accounts and the integrations, each in the order of the input.
    /// </summary>
    public static (IReadOnlyList<Account> Mail, IReadOnlyList<Account> Integrations) Split(IEnumerable<Account> accounts)
    {
        ArgumentNullException.ThrowIfNull(accounts);
        var mail = new List<Account>();
        var other = new List<Account>();
        foreach (var a in accounts)
        {
            (ShowsInAccounts(a) ? mail : other).Add(a);
        }
        return (mail, other);
    }

    /// <summary>
    /// integrations.FullOrder: the order account.reorder gets when
    /// <paramref name="page"/> was reordered to <paramref name="order"/>: the
    /// mail accounts first, then the integrations. The reordered page's
    /// accounts follow <paramref name="order"/>; the other page's keep their
    /// order in <paramref name="all"/>. An id of <paramref name="order"/> that
    /// is not in <paramref name="all"/>, or that belongs to the other page, is
    /// ignored, a repeated one counts once, and an account of the page that
    /// <paramref name="order"/> omits follows the listed ones in its order in
    /// <paramref name="all"/>. The result names every account of
    /// <paramref name="all"/> exactly once.
    /// </summary>
    public static IReadOnlyList<AccountId> FullOrder(IEnumerable<Account> all, Page page, IEnumerable<AccountId>? order)
    {
        var (mail, other) = Split(all);
        var wanted = order is null ? new List<AccountId>() : new List<AccountId>(order);
        var output = new List<AccountId>(mail.Count + other.Count);
        Ordered(mail, page == Page.Accounts);
        Ordered(other, page == Page.Integrations);
        return output;

        void Ordered(IReadOnlyList<Account> group, bool reordered)
        {
            if (!reordered)
            {
                foreach (var a in group)
                {
                    output.Add(a.Id);
                }
                return;
            }
            var inGroup = new HashSet<AccountId>();
            foreach (var a in group)
            {
                inGroup.Add(a.Id);
            }
            var seen = new HashSet<AccountId>();
            foreach (var id in wanted)
            {
                if (inGroup.Contains(id) && seen.Add(id))
                {
                    output.Add(id);
                }
            }
            foreach (var a in group)
            {
                if (seen.Add(a.Id))
                {
                    output.Add(a.Id);
                }
            }
        }
    }

    /// <summary>integrations.Badges: the capsules' texts.</summary>
    public static BadgeTexts Badges() => new(
        // TRANSLATORS: capsule on an AI integration that uses the apps installed on this computer.
        ThisComputer: L10n.T("This computer"),
        // TRANSLATORS: capsule on an AI provider account that uses an API key.
        ApiKey: L10n.T("API key"));

    /// <summary>
    /// integrations.Catalog: the services that can be added, in the order the
    /// page lists them in its one group: the services first, then the AI
    /// section.
    /// </summary>
    public static IReadOnlyList<Service> Catalog() => [.. Services(), .. AiServices()];

    /// <summary>integrations.CatalogSection: the entries of one section of the catalogue, in its order.</summary>
    public static IReadOnlyList<Service> CatalogSection(Section section) =>
        [.. Catalog().Where(s => s.Section == section)];

    /// <summary>
    /// integrations.Offered: whether catalogue entry <paramref name="service"/>
    /// is shown with the local integrations in the states
    /// <paramref name="claude"/> and <paramref name="chatGpt"/>: an account
    /// kind always (another account can be added), a local integration only
    /// while it is not connected — connected, it is a row of Connected
    /// Services instead.
    /// </summary>
    public static bool Offered(Service service, ClaudeState claude, ChatGptState chatGpt)
    {
        ArgumentNullException.ThrowIfNull(service);
        return service.Local == LocalKind.None || !LocalConnected(service.Local, claude, chatGpt);
    }

    // integrations.services: the catalogue's first section.
    private static IReadOnlyList<Service> Services() =>
    [
        new Service
        {
            Kind = AccountKind.Jira,
            Name = JiraName,
            // TRANSLATORS: Preferences → Integrations: what a Jira
            // account brings ("issues" as in Jira's own word).
            Description = L10n.T("Issues read as mail, comments and status changes"),
            Icon = JiraIcon,
        },
        new Service
        {
            Kind = AccountKind.CalDAV,
            // TRANSLATORS: Preferences → Integrations: the name of the
            // service in the list of services that can be added.
            Name = L10n.T("CalDAV Calendar"),
            Description = L10n.T("Nextcloud, iCloud, Fastmail and other CalDAV servers"),
            Icon = CalDAVIcon,
        },
        new Service
        {
            Kind = AccountKind.Eway,
            Name = Crm.CrmRules.Brand,
            // TRANSLATORS: Preferences → Integrations: what an eWay-CRM
            // account brings ("deals" as eWay-CRM's sales opportunities).
            Description = L10n.T("Save messages to your CRM with their contacts, companies and deals"),
            Icon = EwayIcon,
        },
    ];

    // integrations.aiServices: the AI section of the catalogue
    // (docs/plans/ai-api.md §7.2): the three local integrations, then the
    // three provider kinds.
    private static IReadOnlyList<Service> AiServices()
    {
        var names = Assistant.ApiText();
        var badges = Badges();
        return
        [
            new Service
            {
                Section = Section.Ai,
                Local = LocalKind.ClaudeDesktop,
                Name = ClaudeDesktopName,
                // TRANSLATORS: Preferences → Integrations: what Claude Desktop on this computer brings once the bridge is registered in it.
                Description = L10n.T("Let Claude Desktop read your mail and prepare drafts through the MCP bridge"),
                Icon = AiIcon,
                Badge = badges.ThisComputer,
            },
            new Service
            {
                Section = Section.Ai,
                Local = LocalKind.ClaudeCode,
                Name = ClaudeCodeName,
                // TRANSLATORS: Preferences → Integrations: what Claude Code on this computer brings: the bridge registered in it, and the assistant in the app run with it.
                Description = L10n.T("Let Claude Code read your mail and prepare drafts, and run the assistant in the app with it"),
                Icon = AiIcon,
                Badge = badges.ThisComputer,
            },
            new Service
            {
                Section = Section.Ai,
                Local = LocalKind.ChatGpt,
                Name = ChatGptName,
                // TRANSLATORS: Preferences → Integrations: what ChatGPT (Codex) brings.
                Description = L10n.T("Run the assistant in the app through Codex, using your ChatGPT plan"),
                Icon = AiIcon,
                Badge = badges.ThisComputer,
            },
            new Service
            {
                Section = Section.Ai,
                Kind = AccountKind.Ai,
                Provider = AiProvider.Anthropic,
                Name = names.Anthropic,
                // TRANSLATORS: Preferences → Integrations: what an Anthropic API account brings.
                Description = L10n.T("Run the assistant in the app over the Anthropic API with your own API key"),
                Icon = AiIcon,
                Badge = badges.ApiKey,
            },
            new Service
            {
                Section = Section.Ai,
                Kind = AccountKind.Ai,
                Provider = AiProvider.OpenAi,
                Name = names.OpenAi,
                // TRANSLATORS: Preferences → Integrations: what an OpenAI API account brings.
                Description = L10n.T("Run the assistant in the app over the OpenAI API with your own API key"),
                Icon = AiIcon,
                Badge = badges.ApiKey,
            },
            new Service
            {
                Section = Section.Ai,
                Kind = AccountKind.Ai,
                Provider = AiProvider.OpenAiCompatible,
                Name = names.OpenAiCompatible,
                // TRANSLATORS: Preferences → Integrations: what an OpenAI-compatible server brings; the product names stay as they are.
                Description = L10n.T("Ollama, llama.cpp, LM Studio, vLLM and gateways such as OpenRouter: a model on your computer or a server of your choice"),
                Icon = AiIcon,
                Badge = badges.ApiKey,
            },
        ];
    }

    /// <summary>
    /// integrations.RowFor: the row of account <paramref name="a"/>. Title:
    /// the account name as the user typed it (nothing trimmed), else the Jira
    /// site's host, else the address. Subtitle: the host of the Jira site or
    /// of the CalDAV server, the host and path of the eWay-CRM service, the
    /// provider and model of an AI provider account ("Anthropic API ·
    /// claude-opus-5-5"; the title of one without a name is its provider),
    /// else — and when the title shows the host already — the address.
    /// </summary>
    public static Row RowFor(Account a)
    {
        ArgumentNullException.ThrowIfNull(a);
        var kind = KindOf(a);
        var title = TitleOf(a);
        var host = HostOf(a);
        var subtitle = host.Length > 0 && host != title ? host : a.Config.Email;
        string icon;
        Editor editor;
        string editLabel;
        string removeBody;
        var badge = "";
        var offersSwitch = true;
        switch (kind.Value)
        {
            case AccountKind.Imap:
            case AccountKind.Graph:
                (icon, editor, editLabel) = (MailIcon, Editor.MailWizard, L10n.T("Edit Account"));
                // TRANSLATORS: %s is the account's e-mail address.
                removeBody = L10n.T("%s will be removed from Malachi Mail. Mail on the server is not affected.", a.Config.Email);
                break;
            case AccountKind.Jira:
                (icon, editor, editLabel) = (JiraIcon, Editor.Jira, L10n.T("Edit Account"));
                // TRANSLATORS: %s is the account's name (or its site's host).
                removeBody = L10n.T("%s will be removed from Malachi Mail. Issues on the Jira site are not affected.", title);
                break;
            case AccountKind.CalDAV:
                (icon, editor, editLabel) = (CalDAVIcon, Editor.CalendarPage, CalendarSettings());
                offersSwitch = false;
                // TRANSLATORS: %s is the calendar account's name.
                removeBody = L10n.T("%s will be removed from Malachi Mail. Calendars on the server are not affected.", title);
                break;
            case AccountKind.Eway:
                (icon, editor, editLabel) = (EwayIcon, Editor.Eway, L10n.T("Edit Account"));
                // TRANSLATORS: %s is the eWay-CRM account's name.
                removeBody = L10n.T("%s will be removed from Malachi Mail. Messages saved in eWay-CRM stay there.", title);
                break;
            case AccountKind.Ai:
                (icon, editor, editLabel) = (AiIcon, Editor.Ai, L10n.T("Edit Account"));
                if (title.Length == 0)
                {
                    title = AiProviderName(a);
                }
                subtitle = AiSubtitle(a);
                badge = Badges().ApiKey;
                // TRANSLATORS: %s is the AI provider account's name; its API key leaves the keyring with it.
                removeBody = L10n.T("%s will be removed from Malachi Mail, and its API key with it.", title);
                break;
            default:
                (icon, editor, editLabel) = (MailIcon, Editor.None, "");
                // TRANSLATORS: %s is the account's name.
                removeBody = L10n.T("%s will be removed from Malachi Mail.", title);
                break;
        }
        return new Row
        {
            Id = a.Id,
            Kind = kind,
            Title = title,
            Subtitle = subtitle,
            Icon = icon,
            Badge = badge,
            OffersSwitch = offersSwitch,
            Editor = editor,
            EditLabel = editLabel,
            RemoveBody = removeBody,
        };
    }

    /// <summary>integrations.Texts: the Integrations page's texts.</summary>
    public static PageTexts Texts() => new(
        // TRANSLATORS: a page of the preferences: services beside the
        // mail, such as Jira, CalDAV calendars, eWay-CRM and AI providers.
        Title: L10n.T("Integrations"),
        Description: L10n.T("Services beside your mail: issue trackers, calendars, CRM and AI"),
        ConnectedTitle: L10n.T("Connected Services"),
        AvailableTitle: L10n.T("Available Services"),
        EmptyTitle: L10n.T("No integrations yet"),
        EmptySubtitle: L10n.T("Add one from the list below"),
        // TRANSLATORS: the button of a service in Preferences →
        // Integrations → Available Services.
        Add: L10n.T("Add…"),
        // TRANSLATORS: tooltip of the Edit button of a local integration (Claude Desktop, Claude Code, ChatGPT) in Preferences → Integrations.
        Settings: L10n.T("Settings"),
        CalendarSettings: CalendarSettings());

    /// <summary>integrations.LocalKinds: the local integrations in the order of the page.</summary>
    public static IReadOnlyList<LocalKind> LocalKinds { get; } = [LocalKind.ClaudeDesktop, LocalKind.ClaudeCode, LocalKind.ChatGpt];

    /// <summary>
    /// integrations.LocalConnected: whether local integration
    /// <paramref name="kind"/> is connected: the bridge registered in that
    /// Claude app, or ChatGPT continued with. A connected one has a row of
    /// Connected Services and leaves the catalogue (<see cref="Offered"/>);
    /// Remove on its row disconnects it.
    /// </summary>
    public static bool LocalConnected(LocalKind kind, ClaudeState claude, ChatGptState chatGpt)
    {
        ArgumentNullException.ThrowIfNull(claude);
        ArgumentNullException.ThrowIfNull(chatGpt);
        return kind switch
        {
            LocalKind.ClaudeDesktop => claude.DesktopRegistered,
            LocalKind.ClaudeCode => claude.CodeRegistered,
            LocalKind.ChatGpt => chatGpt.Connected,
            _ => false,
        };
    }

    /// <summary>
    /// integrations.LocalRows: the rows of the connected local integrations,
    /// in the order of <see cref="LocalKinds"/>, from <see cref="LocalRowFor"/>;
    /// none while nothing is connected.
    /// </summary>
    public static IReadOnlyList<LocalRow> LocalRows(ClaudeState claude, ChatGptState chatGpt)
    {
        var rows = new List<LocalRow>();
        foreach (var kind in LocalKinds)
        {
            if (LocalRowFor(kind, claude, chatGpt) is { Shown: true } r)
            {
                rows.Add(r.Row);
            }
        }
        return rows;
    }

    /// <summary>
    /// integrations.LocalRowFor: the row of local integration
    /// <paramref name="kind"/>, and whether the page shows it
    /// (<see cref="LocalConnected"/>; a view that keeps a widget per kind
    /// hides the row of one that is not connected); null for
    /// <see cref="LocalKind.None"/>. Claude Desktop says "MCP bridge
    /// registered". Claude Code says the same, then the Claude Code found
    /// (its version, else its path) or "Claude Code not found", and "signed
    /// in" or "not signed in" once known. ChatGPT (Codex) says "Connected as
    /// …" and "Codex not found" when it is missing. Remove is Unregister for
    /// a Claude app — the bridge removed from that app only
    /// (<see cref="LocalRow.ClientId"/>; Claude Desktop's goes through the
    /// "Restart Claude Desktop?" question like its switch, Claude Code's does
    /// not) — and Disconnect for ChatGPT; each asks
    /// <see cref="LocalRow.RemoveHeading"/> and <see cref="LocalRow.RemoveBody"/>
    /// first. CanRemove is the row's being shown.
    /// </summary>
    public static (LocalRow Row, bool Shown)? LocalRowFor(LocalKind kind, ClaudeState claude, ChatGptState chatGpt)
    {
        var shown = LocalConnected(kind, claude, chatGpt);
        var badge = Badges().ThisComputer;
        switch (kind)
        {
            case LocalKind.ClaudeDesktop:
            case LocalKind.ClaudeCode:
                {
                    var code = kind == LocalKind.ClaudeCode;
                    var title = code ? ClaudeCodeName : ClaudeDesktopName;
                    // TRANSLATORS: Preferences → Integrations, the row of Claude Desktop or Claude Code: the malachi-mcp bridge is registered in that app.
                    var parts = new List<string> { L10n.T("MCP bridge registered") };
                    if (code)
                    {
                        if (!claude.Present)
                        {
                            parts.Add(L10n.T("Claude Code not found"));
                        }
                        else if (claude.Version.Length > 0)
                        {
                            parts.Add(Jira.Clean(claude.Version));
                        }
                        else if (claude.Path.Length > 0)
                        {
                            parts.Add(Jira.Clean(claude.Path));
                        }
                        if (claude.Present && claude.SignInKnown)
                        {
                            // TRANSLATORS: Preferences → Integrations, the Claude Code row: whether Claude Code is signed in (lower case, after " · ").
                            parts.Add(claude.SignedIn ? L10n.T("signed in") : L10n.T("not signed in"));
                        }
                    }
                    return (new LocalRow
                    {
                        Kind = kind,
                        Title = title,
                        Subtitle = string.Join(" · ", parts),
                        Editor = code ? Editor.ClaudeCode : Editor.ClaudeDesktop,
                        Icon = AiIcon,
                        Badge = badge,
                        CanRemove = shown,
                        // TRANSLATORS: the Remove button of the Claude Desktop and Claude Code rows in Preferences → Integrations: it unregisters the bridge from that app.
                        RemoveLabel = L10n.T("Unregister"),
                        // TRANSLATORS: confirmation of the Unregister of the Claude Desktop or Claude Code row in Preferences → Integrations; %s is the app's name.
                        RemoveHeading = L10n.T("Unregister from %s?", title),
                        // TRANSLATORS: body of that confirmation; %s is the app's name (Claude Desktop, Claude Code).
                        RemoveBody = L10n.T("%s will no longer read your mail through Malachi Mail.", title),
                        ClientId = ClientId(kind),
                    }, shown);
                }
            case LocalKind.ChatGpt:
                {
                    // assistant.ChatGPTText.Connected (chatgpt.go).
                    var parts = new List<string> { L10n.T("Connected as %s", Jira.Clean(chatGpt.Account)) };
                    if (!chatGpt.CodexFound)
                    {
                        parts.Add(L10n.T("Codex not found"));
                    }
                    return (new LocalRow
                    {
                        Kind = LocalKind.ChatGpt,
                        Title = ChatGptName,
                        Subtitle = string.Join(" · ", parts),
                        Editor = Editor.ChatGpt,
                        Icon = AiIcon,
                        Badge = badge,
                        CanRemove = shown,
                        // assistant.ChatGPTText.Disconnect (chatgpt.go).
                        RemoveLabel = L10n.T("Disconnect"),
                        // TRANSLATORS: confirmation of the ChatGPT row's Disconnect in Preferences → Integrations.
                        RemoveHeading = L10n.T("Disconnect ChatGPT?"),
                        RemoveBody = L10n.T("Malachi Mail signs out of ChatGPT on this computer. You can connect it again any time."),
                    }, shown);
                }
            default:
                return null;
        }
    }

    /// <summary>
    /// integrations.ClaudeAppText: the texts of the window of Claude app
    /// <paramref name="kind"/> (<see cref="LocalKind.ClaudeDesktop"/>,
    /// <see cref="LocalKind.ClaudeCode"/>); null for another kind (Go's empty
    /// texts).
    /// </summary>
    public static ClaudeAppTexts? ClaudeAppText(LocalKind kind)
    {
        var (title, notInstalled) = kind switch
        {
            LocalKind.ClaudeDesktop => (ClaudeDesktopName, L10n.T("Claude Desktop is not installed")),
            LocalKind.ClaudeCode => (ClaudeCodeName, L10n.T("Claude Code is not installed, or has not been used in a terminal yet")),
            _ => ("", ""),
        };
        if (title.Length == 0)
        {
            return null;
        }
        return new ClaudeAppTexts(
            Title: title,
            // TRANSLATORS: the switch of the Claude Desktop and Claude Code dialogs in Preferences → Integrations.
            SwitchTitle: L10n.T("Register with Claude"),
            // TRANSLATORS: under the switch Register with Claude; %s is the app's name (Claude Desktop, Claude Code).
            SwitchSubtitle: L10n.T("Adds the malachi-mcp bridge to %s on this computer", title),
            NotInstalled: notInstalled);
    }

    /// <summary>
    /// integrations.RegisterSwitchFor: the Register switch of Claude app
    /// <paramref name="kind"/> for what the bridge last reported about that
    /// app (<paramref name="known"/> false while no report came: off and
    /// waiting): on while registered; flippable while registered or present;
    /// while the app is neither, insensitive with
    /// <see cref="ClaudeAppTexts.NotInstalled"/> as its subtitle instead of
    /// <see cref="ClaudeAppTexts.SwitchSubtitle"/>. A view keeps it
    /// insensitive while its own install or uninstall runs.
    /// </summary>
    public static RegisterSwitch RegisterSwitchFor(LocalKind kind, bool known, bool present, bool registered)
    {
        var t = ClaudeAppText(kind);
        var subtitle = t?.SwitchSubtitle ?? "";
        if (!known)
        {
            return new RegisterSwitch(On: false, Sensitive: false, Subtitle: subtitle);
        }
        var sensitive = registered || present;
        return new RegisterSwitch(registered, sensitive, sensitive ? subtitle : t?.NotInstalled ?? "");
    }

    // integrations.aiProvider: the name of an AI provider account's provider kind.
    private static string AiProviderName(Account a) =>
        Assistant.ProviderName(a.Config.Ai?.Provider ?? new AiProvider(""));

    // integrations.aiSubtitle: "<provider> · <model>" of an AI provider
    // account; the provider alone without a model. The model id is cleaned
    // as any string the daemon stores from the user's input.
    private static string AiSubtitle(Account a)
    {
        var s = AiProviderName(a);
        var m = Jira.Clean(a.Config.Ai?.Model ?? "");
        return m.Length > 0 ? s + " · " + m : s;
    }

    // calendarSettings: what a CalDAV row's Edit opens.
    private static string CalendarSettings() =>
        // TRANSLATORS: tooltip of a CalDAV account's Edit button in
        // Preferences → Integrations; it opens the Calendar page.
        L10n.T("Calendar Settings");

    // api.AccountConfig.Protocol: the kind, imap when absent or empty.
    private static AccountKind KindOf(Account a)
    {
        var kind = a.Config.ProtocolKind;
        return string.IsNullOrEmpty(kind.Value) ? new AccountKind(AccountKind.Imap) : kind;
    }

    // title: the name, the Jira site's host, the address.
    private static string TitleOf(Account a)
    {
        if (a.Config.Name.Length > 0)
        {
            return a.Config.Name;
        }
        var host = Jira.SiteHost(a.Config);
        return host.Length > 0 ? host : a.Config.Email;
    }

    /// <summary>
    /// integrations.host: the host a row's subtitle shows: a Jira account's
    /// site, a CalDAV account's server, an eWay-CRM account's service (with
    /// its path, which tells two instances on one host apart), the host of an
    /// AI provider account's effective base URL; "" for anything else.
    /// </summary>
    public static string HostOf(Account a)
    {
        ArgumentNullException.ThrowIfNull(a);
        var kind = KindOf(a);
        if (kind == AccountKind.Ai)
        {
            return a.Config.Ai is { } ai && UrlSyntax.Parse(ai.EffectiveBaseUrl.Trim()) is { } au
                ? Jira.Clean(CodePoints.ToLower(UrlSyntax.SplitHostPort(au.Rest.Host).Host))
                : "";
        }
        if (kind == AccountKind.Jira)
        {
            return Jira.SiteHost(a.Config);
        }
        if (kind == AccountKind.Eway)
        {
            return Crm.CrmRules.ServiceHost(a);
        }
        if (kind != AccountKind.CalDAV || a.Config.CalDAV is not { } caldav || UrlSyntax.Parse(caldav.ServerUrl.Trim()) is not { } u)
        {
            return "";
        }
        return Jira.Clean(CodePoints.ToLower(UrlSyntax.SplitHostPort(u.Rest.Host).Host));
    }
}
