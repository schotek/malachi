// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantAPI.swift
// (Assistant.APIStrings, name(of:), providerName(of:)); GTK:
// ui/internal/assistant/api.go (APIStrings, name, providerName). The texts of
// the provider choice and of the AI provider accounts (kind ai) shared by the
// clients; Assistant.ApiText fills it.

using Malachi.Core.Api;

namespace Malachi.Core.Assistants;

/// <summary>
/// The texts of the provider choice and of the AI provider accounts shared by
/// the clients (<see cref="Assistant.ApiText"/>). Plain texts; nothing is
/// markup.
/// </summary>
public sealed record ApiStrings
{
    /// <summary>The name of the provider kind <see cref="AiProvider.Anthropic"/>.</summary>
    public required string Anthropic { get; init; }

    /// <summary>The name of the provider kind <see cref="AiProvider.OpenAi"/>.</summary>
    public required string OpenAi { get; init; }

    /// <summary>The name of the provider kind <see cref="AiProvider.OpenAiCompatible"/>.</summary>
    public required string OpenAiCompatible { get; init; }

    /// <summary>The name of the user's Claude Code in the Assistant Uses row (a brand, not translated).</summary>
    public required string ClaudeCode { get; init; }

    /// <summary>The name of ChatGPT (Codex) in the Assistant Uses row (a brand, not translated).</summary>
    public required string ChatGpt { get; init; }

    /// <summary>The Assistant Uses row of Settings → AI.</summary>
    public required string UsesRow { get; init; }

    /// <summary>The line under the Assistant Uses row.</summary>
    public required string UsesSubtitle { get; init; }

    /// <summary>Why an account cannot be chosen: its switch in Integrations is off.</summary>
    public required string AccountOff { get; init; }

    /// <summary>Why an account cannot be chosen: sending mail to it is not allowed yet.</summary>
    public required string AccountNoConsent { get; init; }

    /// <summary>Why an account cannot be chosen: the provider refused its key.</summary>
    public required string AccountKeyRefused { get; init; }

    /// <summary>The Assistant Uses row's entry for an account that was removed.</summary>
    public required string AccountMissing { get; init; }

    /// <summary>The Model row of Settings → AI and of the board's group.</summary>
    public required string ModelRow { get; init; }

    /// <summary>The Model row's line for an AI provider account (the model is the account's and is saved to it).</summary>
    public required string ModelSubtitle { get; init; }

    /// <summary>The line of the board's Model row for an AI provider account.</summary>
    public required string BoardModelSubtitle { get; init; }

    /// <summary>The Mac App Store build's dialog (D17); Windows never shows it.</summary>
    public required string StoreDialogBody { get; init; }

    /// <summary>That dialog's button that adds an Anthropic API account.</summary>
    public required string StoreAddApi { get; init; }

    /// <summary>Under Show the Assistant Menu: the bridge is registered in Claude under Integrations now.</summary>
    public required string RegisterInIntegrations { get; init; }

    /// <summary>The assistant panel's footer over an AI provider account; %s is <see cref="Assistant.Destination"/>.</summary>
    public required string Footer { get; init; }

    /// <summary>The board's consent over an AI provider account; %s is <see cref="Assistant.Destination"/>.</summary>
    public required string BoardConsentBody { get; init; }

    /// <summary>What the board and the panel say for an AI provider account that is switched off.</summary>
    public required string BoardAccountOff { get; init; }

    /// <summary>What the board and the panel say for an AI provider account whose key was refused.</summary>
    public required string BoardKeyRefused { get; init; }

    /// <summary>The button that opens the account's settings in Integrations.</summary>
    public required string SetUpInIntegrations { get; init; }

    /// <summary>The first entry of the board's Model row over an AI provider account: the account's model.</summary>
    public required string BoardModelDefault { get; init; }

    /// <summary>
    /// The line of an AI provider account's row in Integrations: %s is the
    /// number of tokens the daemon's assistant used through it today (the
    /// client groups its digits).
    /// </summary>
    public required string UsageToday { get; init; }

    /// <summary>
    /// assistant.APIStrings.name: the name of a provider kind; an unknown one
    /// is the OpenAI-compatible server.
    /// </summary>
    public string NameOf(AiProvider p) => p.Value switch
    {
        AiProvider.Anthropic => Anthropic,
        AiProvider.OpenAi => OpenAi,
        _ => OpenAiCompatible,
    };

    /// <summary>assistant.APIStrings.providerName: the name of an account's provider kind.</summary>
    public string ProviderNameOf(Account a)
    {
        System.ArgumentNullException.ThrowIfNull(a);
        return a.Config.Ai is { } ai ? NameOf(ai.Provider) : OpenAiCompatible;
    }
}
