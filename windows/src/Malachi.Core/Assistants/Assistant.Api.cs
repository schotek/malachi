// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/assistant/api.go (ProviderClaude, ProviderChatGPT,
// IsAccountProvider, AccountOf, NormalizeProvider, SourceOf,
// ProviderChoices, APIText, Destination, ProviderName) and apitext.go
// (APIFailureTexts, APIFailurePrefix, APIFailureText); macOS:
// macos/Sources/MalachiCore/Assistant/AssistantAPI.swift. The provider of
// the assistant in the app is the setting assistant-provider, a string
// (docs/plans/ai-api.md D14): ProviderClaude for the user's Claude Code,
// ProviderChatGpt for Codex with the user's ChatGPT plan, or the id of an AI
// provider account of the daemon (kind ai, docs/api.md §4.1), over which the
// daemon runs the assistant itself (§4.18). An empty, unknown or removed
// value reads as Claude. The texts of that choice and of the AI provider
// accounts (ApiText), where an account sends the mail (Destination), and the
// sentences of a daemon session's failures (ApiFailureText) are here too,
// with the GTK msgids. Destination reads the server's address as Go's
// url.Parse does (UrlSyntax), as GTK does, where Swift asks Foundation's URL.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.I18n;

namespace Malachi.Core.Assistants;

public static partial class Assistant
{
    /// <summary>assistant.ProviderClaude: the user's Claude Code.</summary>
    public const string ProviderClaude = "claude";

    /// <summary>assistant.ProviderChatGPT: Codex with the user's ChatGPT plan.</summary>
    public const string ProviderChatGpt = "chatgpt";

    // Starts every account id of the daemon ("acc_" + 32 hex digits).
    private const string AccountPrefix = "acc_";

    /// <summary>
    /// assistant.IsAccountProvider: a setting that names an AI provider
    /// account (an id of the daemon).
    /// </summary>
    public static bool IsAccountProvider(string? id) =>
        id is not null && id.StartsWith(AccountPrefix, StringComparison.Ordinal) && id.Length > AccountPrefix.Length;

    /// <summary>
    /// assistant.AccountOf: the account a setting names; null for the local
    /// providers.
    /// </summary>
    public static AccountId? AccountOf(string? id) => IsAccountProvider(id) ? new AccountId(id!) : (AccountId?)null;

    /// <summary>
    /// assistant.NormalizeProvider: the stored form of a provider setting:
    /// "chatgpt" and an account id pass, anything else is "claude".
    /// </summary>
    public static string NormalizeProvider(string? id) =>
        id == ProviderChatGpt || IsAccountProvider(id) ? id! : ProviderClaude;

    /// <summary>assistant.SourceOf: the source of a provider setting.</summary>
    public static AssistantProviderSource SourceOf(string? id) => NormalizeProvider(id) switch
    {
        ProviderChatGpt => AssistantProviderSource.ChatGpt,
        ProviderClaude => AssistantProviderSource.Claude,
        _ => AssistantProviderSource.Account,
    };

    /// <summary>
    /// assistant.ProviderChoices: the entries of the Assistant Uses row of
    /// Settings → AI (docs/plans/ai-api.md §7.3): Claude Code, ChatGPT, then
    /// every AI provider account in the order of <paramref name="accounts"/>
    /// by name (an account without a name by its provider). An account that
    /// is switched off, that has no current consent, or whose key the
    /// provider refused is listed with its problem and not available; other
    /// accounts are available. Accounts that are not AI provider accounts are
    /// skipped.
    /// </summary>
    public static IReadOnlyList<ProviderChoice> ProviderChoices(
        ProviderChoiceState claude, ProviderChoiceState chatGpt, IEnumerable<Account> accounts)
    {
        ArgumentNullException.ThrowIfNull(claude);
        ArgumentNullException.ThrowIfNull(chatGpt);
        ArgumentNullException.ThrowIfNull(accounts);
        var x = ApiText();
        var output = new List<ProviderChoice>
        {
            new(ProviderClaude, x.ClaudeCode, claude.Available, claude.Problem),
            new(ProviderChatGpt, x.ChatGpt, chatGpt.Available, chatGpt.Problem),
        };
        foreach (var a in accounts)
        {
            if (a.Config.ProtocolKind != AccountKind.Ai)
            {
                continue;
            }
            var name = a.Config.Name.Trim();
            if (name.Length == 0)
            {
                name = x.ProviderNameOf(a);
            }
            var c = new ProviderChoice(a.Id.Value, name, true);
            if (!a.Enabled)
            {
                c = c with { Available = false, Problem = x.AccountOff };
            }
            else if (a.Config.Ai is not { Consented: true })
            {
                c = c with { Available = false, Problem = x.AccountNoConsent };
            }
            else if (a.State.Status == SyncStatus.AuthRequired)
            {
                c = c with { Available = false, Problem = x.AccountKeyRefused };
            }
            output.Add(c);
        }
        return output;
    }

    /// <summary>assistant.APIText: the texts of the provider choice and of the AI provider accounts, translated.</summary>
    public static ApiStrings ApiText() => new()
    {
        // TRANSLATORS: the kind of an AI provider account: the Anthropic Messages API with the user's own key.
        Anthropic = L10n.T("Anthropic API"),
        // TRANSLATORS: the kind of an AI provider account: the OpenAI API with the user's own key.
        OpenAi = L10n.T("OpenAI API"),
        // TRANSLATORS: the kind of an AI provider account: any server with the OpenAI chat completions interface.
        OpenAiCompatible = L10n.T("OpenAI-compatible server"),
        // The names of the local providers are not translated (api.go).
        ClaudeCode = ClaudeCodeName,
        ChatGpt = ChatGptCodexName,
        // TRANSLATORS: row of Settings → AI: which provider the assistant in the app runs on.
        UsesRow = L10n.T("Assistant Uses"),
        UsesSubtitle = L10n.T("Claude Code or ChatGPT on this computer, or an AI provider added in Integrations"),
        // TRANSLATORS: why an AI provider account cannot be chosen: its switch in Integrations is off.
        AccountOff = L10n.T("Switched off in Integrations"),
        // TRANSLATORS: why an AI provider account cannot be chosen: the user did not allow sending mail to it yet.
        AccountNoConsent = L10n.T("Sending mail to it is not allowed yet"),
        // TRANSLATORS: why an AI provider account cannot be chosen: the provider refused its API key.
        AccountKeyRefused = L10n.T("The provider refused the API key"),
        // TRANSLATORS: the Assistant Uses row shows this when the chosen AI provider account no longer exists.
        AccountMissing = L10n.T("Removed account"),
        ModelRow = L10n.T("Model"),
        // TRANSLATORS: under the Model row of Settings → AI when the assistant runs on an AI provider account.
        ModelSubtitle = L10n.T("The model the account uses; saved to the account"),
        // TRANSLATORS: under the Model row of the board's group when the triage runs on an AI provider account.
        BoardModelSubtitle = L10n.T("The model of the board's triage; empty uses the account's model"),
        // TRANSLATORS: the Mac App Store build's dialog; Windows never shows it.
        StoreDialogBody = L10n.T("This version from the App Store cannot run Claude Code or Codex. Use Malachi Mail from malachimail.app, or add an AI provider through its API."),
        StoreAddApi = L10n.T("Add Anthropic API…"),
        // TRANSLATORS: under Show the Assistant Menu in Settings → AI while the bridge is not registered in Claude.
        RegisterInIntegrations = L10n.T("Register with Claude under Integrations first"),
        // TRANSLATORS: footer of the assistant panel; %s is where the mail goes (Anthropic, OpenAI, or the server's host).
        Footer = L10n.T("Mail you ask about is sent to %s"),
        // TRANSLATORS: the board's consent over an AI provider account; %s is where the mail goes.
        BoardConsentBody = L10n.T("The assistant reads the conversations on the board that need notes, and any other mail and attachments it needs to understand them, and sends their text to %s with your API key. It adds titles, summaries, tasks, deadlines and suggested replies to the board, and a triage you start yourself may also write replies, which stay on the board in Malachi Mail, not in your Drafts folder, until you send them. It cannot send, move or delete mail, and messages may contain instructions from their senders that it is told not to follow. Which accounts it triages, and whether it runs at all, you choose in Settings."),
        // TRANSLATORS: why the assistant cannot run: the chosen AI provider account's switch in Integrations is off.
        BoardAccountOff = L10n.T("The AI account is switched off. Turn it on under Integrations in the preferences."),
        // TRANSLATORS: why the assistant cannot run: the provider refused the AI provider account's API key.
        BoardKeyRefused = L10n.T("The provider refused the API key. Replace it under Integrations in the preferences."),
        // TRANSLATORS: button that opens the AI provider account's settings in Integrations.
        SetUpInIntegrations = L10n.T("Set Up in Integrations…"),
        // TRANSLATORS: entry of the board's Model row over an AI provider account: the account's model.
        BoardModelDefault = L10n.T("The account's model"),
        // TRANSLATORS: line of an AI provider account's row in Integrations; %s is a number of tokens with its digits grouped.
        UsageToday = L10n.T("%s tokens today"),
    };

    // The brands of the local providers in the Assistant Uses row.
    private const string ClaudeCodeName = "Claude Code";
    private const string ChatGptCodexName = "ChatGPT (Codex)";

    /// <summary>
    /// assistant.Destination: where an AI provider account sends the mail,
    /// for the panel's footer and the consents: Anthropic, OpenAI, or the
    /// host of an OpenAI-compatible server (with its port, as Go's url.Host;
    /// the provider's name when the address has no host).
    /// </summary>
    public static string Destination(Account a)
    {
        ArgumentNullException.ThrowIfNull(a);
        if (a.Config.Ai is not { } ai)
        {
            return ApiText().OpenAiCompatible;
        }
        switch (ai.Provider.Value)
        {
            case AiProvider.Anthropic:
                return "Anthropic";
            case AiProvider.OpenAi:
                return "OpenAI";
        }
        if (UrlSyntax.Parse((ai.BaseUrl ?? "").Trim()) is { Rest.Host: { Length: > 0 } host })
        {
            return host;
        }
        return ApiText().OpenAiCompatible;
    }

    /// <summary>
    /// assistant.ProviderName: the name of a provider kind; an unknown one is
    /// named as the OpenAI-compatible server (the daemon accepts no other).
    /// </summary>
    public static string ProviderName(AiProvider p) => ApiText().NameOf(p);

    /// <summary>assistant.APIFailureTexts: the sentences of a daemon session's failures, translated.</summary>
    public static ApiFailureStrings ApiFailureTexts() => new()
    {
        // TRANSLATORS: An error line of the assistant: the user has not allowed sending mail to the AI account's provider yet.
        ConsentRequired = L10n.T("Allow sending mail to this provider under AI in the preferences first."),
        // TRANSLATORS: An error line of the assistant: the provider refused the API key.
        Auth = L10n.T("The provider refused the API key. Check it under AI in the preferences."),
        // TRANSLATORS: An error line of the assistant: the API key may not use this model.
        Permission = L10n.T("The API key may not use this model."),
        // TRANSLATORS: An error line of the assistant: the provider's rate limit was reached.
        RateLimit = L10n.T("The provider is receiving too many requests. Try again in a moment."),
        // TRANSLATORS: An error line of the assistant: the provider is overloaded.
        Overloaded = L10n.T("The provider is busy right now. Try again in a moment."),
        // TRANSLATORS: An error line of the assistant: the account at the AI provider has no credit or used up its quota.
        Quota = L10n.T("The account at the provider has no credit or has used up its quota. Top it up with the provider, then try again."),
        // TRANSLATORS: An error line of the assistant: the AI server has no such model or could not load it.
        Model = L10n.T("The server could not load the model. Load it on the server, or choose another model under Integrations in the preferences."),
        // TRANSLATORS: An error line of the assistant: the provider rejected the request.
        InvalidRequest = L10n.T("The provider refused the request."),
        // TRANSLATORS: An error line of the assistant: the chosen model cannot call tools, which the assistant needs to read mail.
        ToolsUnsupported = L10n.T("This model cannot use the Malachi Mail tools. Choose another model under AI in the preferences."),
        // TRANSLATORS: An error line of the assistant: the MCP bridge (the Malachi Mail tools) could not be started.
        Bridge = L10n.T("The Malachi Mail tools could not be started."),
        // TRANSLATORS: An error line of the assistant: the provider could not be reached.
        Network = L10n.T("The provider could not be reached."),
        // TRANSLATORS: An error line of the assistant: the provider's answer could not be read.
        Protocol = L10n.T("The provider's answer could not be read."),
        // TRANSLATORS: An error line of the assistant: the conversation grew too long for the model.
        ContextFull = L10n.T("The conversation is too long for the model. Start a new one."),
        // TRANSLATORS: An error line of the assistant: the model did not answer in time.
        Timeout = L10n.T("The model did not answer in time."),
        // TRANSLATORS: An error line of the assistant: the model called too many tools in one turn.
        Limit = L10n.T("The model made too many tool calls in one turn."),
        // TRANSLATORS: An error line of the assistant: the session ended.
        Stopped = L10n.T("The assistant's session ended."),
    };

    /// <summary>
    /// assistant.APIFailurePrefix: starts every reason a daemon session's
    /// failure class becomes: "api_" and the class ("api_auth").
    /// </summary>
    public const string ApiFailurePrefix = "api_";

    /// <summary>
    /// assistant.APIFailureText: the sentence of a reason "api_&lt;class&gt;"
    /// (or "api_consent_required"); null for any other reason, and for a
    /// class without a sentence.
    /// </summary>
    public static string? ApiFailureText(string? reason)
    {
        if (reason is null || !reason.StartsWith(ApiFailurePrefix, StringComparison.Ordinal))
        {
            return null;
        }
        var t = ApiFailureTexts();
        return reason[ApiFailurePrefix.Length..] switch
        {
            "consent_required" => t.ConsentRequired,
            AssistantFailureClass.Auth => t.Auth,
            AssistantFailureClass.Permission => t.Permission,
            AssistantFailureClass.RateLimit => t.RateLimit,
            AssistantFailureClass.Overloaded => t.Overloaded,
            AssistantFailureClass.InvalidRequest => t.InvalidRequest,
            AssistantFailureClass.Quota => t.Quota,
            AssistantFailureClass.Model => t.Model,
            AssistantFailureClass.ToolsUnsupported => t.ToolsUnsupported,
            AssistantFailureClass.Bridge => t.Bridge,
            AssistantFailureClass.Network => t.Network,
            AssistantFailureClass.Protocol => t.Protocol,
            AssistantFailureClass.ContextFull => t.ContextFull,
            AssistantFailureClass.Timeout => t.Timeout,
            AssistantFailureClass.Limit => t.Limit,
            AssistantFailureClass.Stopped => t.Stopped,
            _ => null,
        };
    }

    /// <summary>
    /// assistant.NothingSetUp: the assistant has nothing to run on: no entry
    /// of Assistant Uses is available and the bridge is known not to be
    /// registered with any Claude app, so the Assistant menu cannot hand mail
    /// to Claude Desktop or Claude Code either. Preferences → AI then greys
    /// the Assistant group out, says where an assistant comes from
    /// (<see cref="ApiStrings.UsesSubtitle"/>) and offers Set Up in
    /// Integrations…. False while the
    /// registration is not known.
    /// </summary>
    public static bool NothingSetUp(IReadOnlyList<ProviderChoice> choices, bool registered, bool known)
    {
        ArgumentNullException.ThrowIfNull(choices);
        if (!known || registered)
        {
            return false;
        }
        foreach (var c in choices)
        {
            if (c.Available)
            {
                return false;
            }
        }
        return true;
    }
}
