// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/API/AIAPI.swift; Go:
// backend/pkg/api/ai.go (all of it), the AI parts of types.go (AccountAI,
// AccountConfig.AI, CapabilityAssistant, AccountTestResult.AI) and
// errors.go (1113, 1207, 1511, 1512, 1513); contract: docs/api.md §2,
// §4.1, §4.18; design: docs/plans/ai-api.md.
//
// AI provider accounts (kind ai) and the assistant the daemon runs over
// them. An ai account names a model API (Anthropic, OpenAI, or any server
// with the OpenAI-compatible chat completions interface) and keeps its key
// in the keyring; the daemon builds the prompts (assistant.prompt gives the
// same ones to a client that runs Claude Code or Codex itself), calls the
// model, streams the answer to the connection that started the session and
// runs the model's tool calls through the MCP bridge. Nothing the model
// writes is trusted: the application shows it as plain text
// (TextBlock.Text), as it shows the bridge's.
//
// Windows: Swift's AI prefix is Ai (AiConfig, AiProvider, AiModel, …), as
// the Preferences page is AiPage. Go's AITestResult embeds
// EndpointTestResult and AssistantUsageDay embeds AssistantUsage: the
// embedded members are the record's own here, as they are on the wire, and
// Endpoint and Usage build the embedded value from them. Go's
// json.RawMessage members (PromptResult.JSONSchema,
// AssistantResult.Structured) are a JsonElement cloned so that it outlives
// the document it came from, as RpcError.Data. Go's omitempty strings and
// identifiers are nullable and read an empty string as absent; omitempty
// lists of params are nullable, the lists of results read null as empty;
// omitempty booleans and integers are left out when they are their zero
// value. Go's empty AssistantSessionSendResult and
// AssistantSessionStopResult are EmptyResult. The limits of ai.go are in
// API.Limits, its durations in AssistantLimits, the client's waits in
// RpcTimeouts.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

// MARK: - Accounts

/// <summary>api.AIProvider: the model API an <c>ai</c> account talks to.</summary>
[JsonConverter(typeof(StringWireValueConverter<AiProvider>))]
public readonly record struct AiProvider(string Value) : IWireEnumeration<AiProvider>
{
    /// <summary>The Anthropic Messages API at <see cref="AiConfig.AnthropicBaseUrl"/>.</summary>
    public const string Anthropic = "anthropic";

    /// <summary>The OpenAI Responses API at <see cref="AiConfig.OpenAiBaseUrl"/>.</summary>
    public const string OpenAi = "openai";

    /// <summary>
    /// A server with the OpenAI chat completions interface at
    /// <see cref="AiConfig.BaseUrl"/> (Ollama, llama.cpp, LM Studio, vLLM, a
    /// gateway); the key is optional.
    /// </summary>
    public const string OpenAiCompatible = "openaiCompatible";

    /// <summary>The three values, in the order of ai.go.</summary>
    public static IReadOnlyList<AiProvider> All { get; } = [Anthropic, OpenAi, OpenAiCompatible];

    /// <summary>api.AIProvider.Valid: one of the three values.</summary>
    public bool IsValid => All.Contains(this);

    /// <summary>The value of a wire string.</summary>
    public static implicit operator AiProvider(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>
/// api.AIConsent: the version of the consent text the user accepted and
/// when (set by the daemon on <c>account.add</c>/<c>account.update</c> with
/// a version).
/// </summary>
public sealed record AiConsent
{
    /// <summary>The version of the consent text; always written.</summary>
    [JsonPropertyName("version")]
    [JsonRequired]
    public int Version { get; init; } = API.Limits.AssistantConsentVersion;

    /// <summary>When the daemon recorded it; null when unknown.</summary>
    [JsonPropertyName("acceptedAt")]
    public DateTimeOffset? AcceptedAt { get; init; }
}

/// <summary>api.AIConfig: present only on an account of kind <c>ai</c>.</summary>
public sealed record AiConfig
{
    /// <summary>
    /// api.AnthropicBaseURL: the fixed base URL of <see cref="AiProvider.Anthropic"/>;
    /// <see cref="BaseUrl"/> is empty for it.
    /// </summary>
    public const string AnthropicBaseUrl = "https://api.anthropic.com";

    /// <summary>
    /// api.OpenAIBaseURL: the fixed base URL of <see cref="AiProvider.OpenAi"/>;
    /// <see cref="BaseUrl"/> is empty for it.
    /// </summary>
    public const string OpenAiBaseUrl = "https://api.openai.com/v1";

    /// <summary>The model API.</summary>
    [JsonPropertyName("provider")]
    public required AiProvider Provider { get; init; }

    /// <summary>
    /// The server's base URL for <see cref="AiProvider.OpenAiCompatible"/>
    /// ("…/v1"; normalised: no user info, query or fragment, no trailing
    /// slash; http allowed, a client warns outside loopback); null for the
    /// named providers.
    /// </summary>
    [JsonPropertyName("baseUrl")]
    public string? BaseUrl { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The model id the assistant uses; required, always written.</summary>
    [JsonPropertyName("model")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Model { get; init => field = value ?? ""; } = "";

    /// <summary>The model of the board's triage; null = <see cref="Model"/>.</summary>
    [JsonPropertyName("boardModel")]
    public string? BoardModel { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>
    /// The user's consent to sending mail to this provider (docs/api.md
    /// §4.18); null = not given, and no session starts.
    /// </summary>
    [JsonPropertyName("consent")]
    public AiConsent? Consent { get; init; }

    /// <summary>
    /// Pins the leaf certificate of <see cref="BaseUrl"/>'s host (https only),
    /// as <see cref="ServerConfig.CertificateSha256"/>.
    /// </summary>
    [JsonPropertyName("certificateSha256")]
    public string? CertificateSha256 { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>api.AIConfig.Consented: the current consent text was accepted.</summary>
    [JsonIgnore]
    public bool Consented => Consent is { } c && c.Version >= API.Limits.AssistantConsentVersion;

    /// <summary>
    /// api.AIConfig.EffectiveBaseURL: the base URL the daemon calls, the fixed
    /// one of a named provider, else <see cref="BaseUrl"/> ("" without one).
    /// </summary>
    [JsonIgnore]
    public string EffectiveBaseUrl => Provider.Value switch
    {
        AiProvider.Anthropic => AnthropicBaseUrl,
        AiProvider.OpenAi => OpenAiBaseUrl,
        _ => BaseUrl ?? "",
    };
}

/// <summary>api.AIModel: a model the provider lists (GET /v1/models).</summary>
public sealed record AiModel
{
    /// <summary>The model id.</summary>
    [JsonPropertyName("id")]
    public required string Id { get; init; }

    /// <summary>The provider's name for it; null when it has none.</summary>
    [JsonPropertyName("displayName")]
    public string? DisplayName { get; init => field = string.IsNullOrEmpty(value) ? null : value; }
}

/// <summary>
/// api.AITestResult: <c>account.test</c>'s outcome for an <c>ai</c> account,
/// the probe (<see cref="EndpointTestResult"/>'s members, flat on the wire:
/// Go embeds it) and the models it listed.
/// </summary>
public sealed record AiTestResult
{
    /// <summary>Whether the provider answered the probe.</summary>
    [JsonPropertyName("ok")]
    public required bool Ok { get; init; }

    /// <summary>Why it did not.</summary>
    [JsonPropertyName("error")]
    public RpcError? Error { get; init; }

    /// <summary>The probe's capabilities, scrubbed.</summary>
    [JsonPropertyName("capabilities")]
    public IReadOnlyList<string>? Capabilities { get; init; }

    /// <summary>Dial to ready, in milliseconds.</summary>
    [JsonPropertyName("latencyMs")]
    public required int LatencyMs { get; init; }

    /// <summary>The models the provider listed; absent or null reads as empty.</summary>
    [JsonPropertyName("models")]
    [JsonConverter(typeof(NullAsEmptyListConverter<AiModel>))]
    public IReadOnlyList<AiModel> Models { get; init => field = value ?? []; } = [];

    /// <summary>The probe as an <see cref="EndpointTestResult"/>, for code that reads every account's test the same way.</summary>
    [JsonIgnore]
    public EndpointTestResult Endpoint => new() { Ok = Ok, Error = Error, Capabilities = Capabilities, LatencyMs = LatencyMs };
}

// MARK: - Prompts

/// <summary>api.PromptPurpose: what an assistant session or prompt is for.</summary>
[JsonConverter(typeof(StringWireValueConverter<PromptPurpose>))]
public readonly record struct PromptPurpose(string Value) : IWireEnumeration<PromptPurpose>
{
    /// <summary>The assistant panel: a conversation with tools.</summary>
    public const string Panel = "panel";

    /// <summary>The compose window's rewrite of a passage, no tools.</summary>
    public const string Rewrite = "rewrite";

    /// <summary>The compose window's writing bar, no tools.</summary>
    public const string Write = "write";

    /// <summary>Search in the user's own words, no tools, a schema.</summary>
    public const string Search = "search";

    /// <summary>A triage run of the board, with tools.</summary>
    public const string Triage = "triage";

    /// <summary>A suggested reply of the board, with tools.</summary>
    public const string Reply = "reply";

    /// <summary>The six values, in the order of ai.go.</summary>
    public static IReadOnlyList<PromptPurpose> All { get; } = [Panel, Rewrite, Write, Search, Triage, Reply];

    /// <summary>api.PromptPurpose.Valid: one of the six values.</summary>
    public bool IsValid => All.Contains(this);

    /// <summary>The value of a wire string.</summary>
    public static implicit operator PromptPurpose(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.RewritePreset: what the rewrite does with the passage.</summary>
[JsonConverter(typeof(StringWireValueConverter<RewritePreset>))]
public readonly record struct RewritePreset(string Value) : IWireEnumeration<RewritePreset>
{
    /// <summary>More politely.</summary>
    public const string Politer = "politer";

    /// <summary>More briefly.</summary>
    public const string Shorter = "shorter";

    /// <summary>Spelling and grammar.</summary>
    public const string Fix = "fix";

    /// <summary>Into English (Go's RewriteToEnglish).</summary>
    public const string ToEnglish = "english";

    /// <summary><see cref="RewriteInput.Instruction"/> says what.</summary>
    public const string Custom = "custom";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator RewritePreset(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.WriteMode: the kind of compose window the writing bar writes for.</summary>
[JsonConverter(typeof(StringWireValueConverter<WriteMode>))]
public readonly record struct WriteMode(string Value) : IWireEnumeration<WriteMode>
{
    /// <summary>A new message.</summary>
    public const string New = "new";

    /// <summary>A reply.</summary>
    public const string Reply = "reply";

    /// <summary>A reply to all.</summary>
    public const string ReplyAll = "replyAll";

    /// <summary>A forward.</summary>
    public const string Forward = "forward";

    /// <summary>A draft opened again.</summary>
    public const string Edit = "edit";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator WriteMode(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>
/// api.PromptContext: what a panel conversation is about, the selected
/// messages of an account or a folder. <see cref="Added"/> marks a context
/// added to a running conversation.
/// </summary>
public sealed record PromptContext
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>
    /// The messages, at most <see cref="API.Limits.MaxPromptMessageIDs"/>;
    /// null (an empty list reads as null) for none.
    /// </summary>
    [JsonPropertyName("messageIds")]
    public IReadOnlyList<MessageId>? MessageIds { get; init => field = value is { Count: > 0 } ? value : null; }

    /// <summary>The folder; null for none.</summary>
    [JsonPropertyName("folderId")]
    public FolderId? FolderId { get; init => field = value is { Value.Length: > 0 } ? value : null; }

    /// <summary>The context is added to a running conversation; false is written as absent.</summary>
    [JsonPropertyName("added")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingDefault)]
    public bool Added { get; init; }
}

/// <summary>
/// api.RewriteInput: a compose rewrite, the preset (or
/// <see cref="Instruction"/> with <see cref="RewritePreset.Custom"/>) and
/// the passage.
/// </summary>
public sealed record RewriteInput
{
    /// <summary>What the rewrite does.</summary>
    [JsonPropertyName("preset")]
    public required RewritePreset Preset { get; init; }

    /// <summary>The user's instruction of a custom rewrite; null for none.</summary>
    [JsonPropertyName("instruction")]
    public string? Instruction { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The passage; always written.</summary>
    [JsonPropertyName("passage")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Passage { get; init => field = value ?? ""; } = "";
}

/// <summary>
/// api.WriteInput: the writing bar's request, the kind of window, the user's
/// optional instruction, the subject, the user's own text in the editor
/// and, for a reply or forward, the text of the prepared original.
/// </summary>
public sealed record WriteInput
{
    /// <summary>The kind of window.</summary>
    [JsonPropertyName("mode")]
    public required WriteMode Mode { get; init; }

    /// <summary>The user's instruction; null for none.</summary>
    [JsonPropertyName("instruction")]
    public string? Instruction { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The subject; null for none.</summary>
    [JsonPropertyName("subject")]
    public string? Subject { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The user's own text in the editor; null for none.</summary>
    [JsonPropertyName("own")]
    public string? Own { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The text of the prepared original of a reply or forward; null for none.</summary>
    [JsonPropertyName("quote")]
    public string? Quote { get; init => field = string.IsNullOrEmpty(value) ? null : value; }
}

/// <summary>api.SearchInput: the search in the user's own words.</summary>
public sealed record SearchInput
{
    /// <summary>The user's words.</summary>
    [JsonPropertyName("words")]
    public required string Words { get; init; }
}

/// <summary>
/// api.TriageInput: a triage of at most <see cref="MaxCases"/> cases;
/// <see cref="Drafts"/> lets the model make suggested replies (a manual run).
/// </summary>
public sealed record TriageInput
{
    /// <summary>The most cases the run takes.</summary>
    [JsonPropertyName("maxCases")]
    public required int MaxCases { get; init; }

    /// <summary>The model may make suggested replies; always written.</summary>
    [JsonPropertyName("drafts")]
    public required bool Drafts { get; init; }
}

/// <summary>
/// api.ReplyInput: a suggested reply to <see cref="MessageId"/> of a board
/// case of <see cref="AccountId"/>; <see cref="Others"/> the case's other
/// newest members (ids only), <see cref="Instruction"/> the user's optional
/// line, <see cref="FollowUp"/> the "suggested follow-up" variant (the user
/// waits for someone).
/// </summary>
public sealed record ReplyInput
{
    /// <summary>The case's account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The message the reply answers; the bridge is confined to it.</summary>
    [JsonPropertyName("messageId")]
    public required MessageId MessageId { get; init; }

    /// <summary>
    /// The case's other newest members, at most
    /// <see cref="API.Limits.MaxPromptMessageIDs"/>; null (an empty list reads
    /// as null) for none.
    /// </summary>
    [JsonPropertyName("others")]
    public IReadOnlyList<MessageId>? Others { get; init => field = value is { Count: > 0 } ? value : null; }

    /// <summary>The user's optional line; null for none.</summary>
    [JsonPropertyName("instruction")]
    public string? Instruction { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The suggested follow-up variant; false is written as absent.</summary>
    [JsonPropertyName("followUp")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingDefault)]
    public bool FollowUp { get; init; }
}

/// <summary>
/// api.PromptInput: the input of one turn, the user's text (panel) and the
/// context it is about, or the structured input of a one-shot purpose.
/// Exactly the members of the purpose are set.
/// </summary>
public sealed record PromptInput
{
    /// <summary>The user's text of a panel turn; null for none.</summary>
    [JsonPropertyName("text")]
    public string? Text { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>What a panel turn is about.</summary>
    [JsonPropertyName("context")]
    public PromptContext? Context { get; init; }

    /// <summary>A rewrite's input.</summary>
    [JsonPropertyName("rewrite")]
    public RewriteInput? Rewrite { get; init; }

    /// <summary>The writing bar's input.</summary>
    [JsonPropertyName("write")]
    public WriteInput? Write { get; init; }

    /// <summary>A search's input.</summary>
    [JsonPropertyName("search")]
    public SearchInput? Search { get; init; }

    /// <summary>A triage run's input.</summary>
    [JsonPropertyName("triage")]
    public TriageInput? Triage { get; init; }

    /// <summary>A suggested reply's input.</summary>
    [JsonPropertyName("reply")]
    public ReplyInput? Reply { get; init; }
}

/// <summary>
/// api.PromptResult: what the daemon built, the system prompt (with the
/// user's profile appended), the message of the turn when an input was
/// given, the bridge tools the model may call (bare names), the arguments
/// the bridge is started with, and the JSON schema of the answer (search).
/// </summary>
public sealed record PromptResult
{
    /// <summary>The system prompt; always written.</summary>
    [JsonPropertyName("systemPrompt")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string SystemPrompt { get; init => field = value ?? ""; } = "";

    /// <summary>The message of the turn; null without an input.</summary>
    [JsonPropertyName("message")]
    public string? Message { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The bridge tools the model may call, bare names; absent or null reads as empty.</summary>
    [JsonPropertyName("tools")]
    [JsonConverter(typeof(NullAsEmptyListConverter<string>))]
    public IReadOnlyList<string> Tools { get; init => field = value ?? []; } = [];

    /// <summary>The arguments the bridge is started with; absent or null reads as empty.</summary>
    [JsonPropertyName("bridgeArgs")]
    [JsonConverter(typeof(NullAsEmptyListConverter<string>))]
    public IReadOnlyList<string> BridgeArgs { get; init => field = value ?? []; } = [];

    /// <summary>The JSON schema of the answer (search), raw; a clone that owns its memory, null for none.</summary>
    [JsonPropertyName("jsonSchema")]
    public JsonElement? JsonSchema { get; init => field = RawJson.Own(value); }

    /// <summary>
    /// The schema as JSON text, "" without one (what a provider that runs the
    /// model itself hands on, Swift <c>jsonSchemaString</c>).
    /// </summary>
    [JsonIgnore]
    public string JsonSchemaText => JsonSchema is { } schema ? schema.GetRawText() : "";
}

/// <summary>
/// api.AssistantPromptParams: asks for the prompt of a purpose.
/// <see cref="Language"/> is the client's UI language (BCP 47) the model
/// answers in; <see cref="AccountId"/>, for a one-shot purpose, the account
/// whose profile note is appended; <see cref="Trigger"/> the triage run's
/// trigger and <see cref="RunId"/> its board run, which the bridge arguments
/// carry; <see cref="Input"/> the turn's input (the message is built only
/// with it).
/// </summary>
public sealed record AssistantPromptParams
{
    /// <summary>What the prompt is for.</summary>
    [JsonPropertyName("purpose")]
    public required PromptPurpose Purpose { get; init; }

    /// <summary>The client's UI language (BCP 47); always written.</summary>
    [JsonPropertyName("language")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Language { get; init => field = value ?? ""; } = "";

    /// <summary>The account whose profile note is appended; null for none.</summary>
    [JsonPropertyName("accountId")]
    public AccountId? AccountId { get; init => field = value is { Value.Length: > 0 } ? value : null; }

    /// <summary>The triage run's trigger; null for none.</summary>
    [JsonPropertyName("trigger")]
    public BoardTrigger? Trigger { get; init => field = value is { Value.Length: > 0 } ? value : null; }

    /// <summary>The triage run's board run (<c>board.runStart</c>); null for none.</summary>
    [JsonPropertyName("runId")]
    public string? RunId { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The turn's input; null builds the system prompt only.</summary>
    [JsonPropertyName("input")]
    public PromptInput? Input { get; init; }
}

// MARK: - Sessions

/// <summary>
/// api.AssistantSessionStartParams: opens a session over an <c>ai</c>
/// account. <see cref="Bridge"/> is the absolute path of the MCP bridge the
/// daemon starts for the tools (purposes with tools);
/// <see cref="NoteAccountId"/> the mail or Jira account whose profile note
/// is appended (absent = none); <see cref="RunId"/> the board run of a
/// triage session; <see cref="Input"/>, when given, is sent as the first
/// turn at once (a one-shot purpose always gives it);
/// <see cref="TimeoutSeconds"/> bounds one turn (0 = the purpose's default).
/// </summary>
public sealed record AssistantSessionStartParams
{
    /// <summary>The ai account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>What the session is for.</summary>
    [JsonPropertyName("purpose")]
    public required PromptPurpose Purpose { get; init; }

    /// <summary>The client's UI language (BCP 47); always written.</summary>
    [JsonPropertyName("language")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Language { get; init => field = value ?? ""; } = "";

    /// <summary>The absolute path of the MCP bridge; null for a purpose without tools.</summary>
    [JsonPropertyName("bridge")]
    public string? Bridge { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The mail or Jira account whose profile note is appended; null for none.</summary>
    [JsonPropertyName("noteAccountId")]
    public AccountId? NoteAccountId { get; init => field = value is { Value.Length: > 0 } ? value : null; }

    /// <summary>The triage run's trigger; null for none.</summary>
    [JsonPropertyName("trigger")]
    public BoardTrigger? Trigger { get; init => field = value is { Value.Length: > 0 } ? value : null; }

    /// <summary>The board run of a triage session; null for none.</summary>
    [JsonPropertyName("runId")]
    public string? RunId { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The first turn, sent at once; null for none.</summary>
    [JsonPropertyName("input")]
    public PromptInput? Input { get; init; }

    /// <summary>The bound of one turn in seconds; 0 (written as absent) is the purpose's default.</summary>
    [JsonPropertyName("timeoutSeconds")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingDefault)]
    public int TimeoutSeconds { get; init; }
}

/// <summary>api.AssistantSessionStartResult: the session and the tools the model has (bare names).</summary>
public sealed record AssistantSessionStartResult
{
    /// <summary>The session.</summary>
    [JsonPropertyName("sessionId")]
    public required string SessionId { get; init; }

    /// <summary>The tools the model has; absent or null reads as empty.</summary>
    [JsonPropertyName("tools")]
    [JsonConverter(typeof(NullAsEmptyListConverter<string>))]
    public IReadOnlyList<string> Tools { get; init => field = value ?? []; } = [];
}

/// <summary>
/// api.AssistantSessionSendParams: sends a turn; its result is
/// <see cref="EmptyResult"/> (Go's empty AssistantSessionSendResult).
/// </summary>
public sealed record AssistantSessionSendParams
{
    /// <summary>The session.</summary>
    [JsonPropertyName("sessionId")]
    public required string SessionId { get; init; }

    /// <summary>The turn's input.</summary>
    [JsonPropertyName("input")]
    public required PromptInput Input { get; init; }
}

/// <summary>
/// api.AssistantSessionStopParams: ends a session; its result is
/// <see cref="EmptyResult"/> (Go's empty AssistantSessionStopResult).
/// </summary>
public sealed record AssistantSessionStopParams
{
    /// <summary>The session.</summary>
    [JsonPropertyName("sessionId")]
    public required string SessionId { get; init; }
}

/// <summary>
/// api.AssistantProviderModelsParams: lists the models of an <c>ai</c>
/// account's provider; <see cref="Refresh"/> ignores the daemon's cache.
/// </summary>
public sealed record AssistantProviderModelsParams
{
    /// <summary>The ai account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>Ignore the daemon's cache; false is written as absent.</summary>
    [JsonPropertyName("refresh")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingDefault)]
    public bool Refresh { get; init; }
}

/// <summary>api.AssistantProviderModelsResult.</summary>
public sealed record AssistantProviderModelsResult
{
    /// <summary>The models; absent or null reads as empty.</summary>
    [JsonPropertyName("models")]
    [JsonConverter(typeof(NullAsEmptyListConverter<AiModel>))]
    public IReadOnlyList<AiModel> Models { get; init => field = value ?? []; } = [];
}

/// <summary>api.AssistantUsage: the token usage of a turn, a session or a day.</summary>
public sealed record AssistantUsage
{
    /// <summary>Input tokens.</summary>
    [JsonPropertyName("inputTokens")]
    public long InputTokens { get; init; }

    /// <summary>Output tokens.</summary>
    [JsonPropertyName("outputTokens")]
    public long OutputTokens { get; init; }

    /// <summary>Input tokens read from the provider's cache.</summary>
    [JsonPropertyName("cacheReadInputTokens")]
    public long CacheReadInputTokens { get; init; }

    /// <summary>Input tokens written to the provider's cache.</summary>
    [JsonPropertyName("cacheCreationInputTokens")]
    public long CacheCreationInputTokens { get; init; }

    /// <summary>api.AssistantUsage.Add: the sum of both.</summary>
    public AssistantUsage Add(AssistantUsage other)
    {
        ArgumentNullException.ThrowIfNull(other);
        return new()
        {
            InputTokens = InputTokens + other.InputTokens,
            OutputTokens = OutputTokens + other.OutputTokens,
            CacheReadInputTokens = CacheReadInputTokens + other.CacheReadInputTokens,
            CacheCreationInputTokens = CacheCreationInputTokens + other.CacheCreationInputTokens,
        };
    }
}

/// <summary>
/// api.AssistantUsageParams: the usage of the last <see cref="Days"/> local
/// days (0 = 1: today), of one <c>ai</c> account or of all.
/// </summary>
public sealed record AssistantUsageParams
{
    /// <summary>The ai account; null for all.</summary>
    [JsonPropertyName("accountId")]
    public AccountId? AccountId { get; init => field = value is { Value.Length: > 0 } ? value : null; }

    /// <summary>
    /// The local days, at most <see cref="API.Limits.MaxAssistantUsageDays"/>;
    /// 0 (written as absent) is today.
    /// </summary>
    [JsonPropertyName("days")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingDefault)]
    public int Days { get; init; }
}

/// <summary>
/// api.AssistantUsageDay: one account's usage on one local day
/// ("2026-11-07"), summed over its models. Go embeds AssistantUsage, so its
/// members lie flat beside <see cref="Day"/>, <see cref="AccountId"/>,
/// <see cref="Sessions"/> and <see cref="Turns"/>; <see cref="Usage"/>
/// builds it from them.
/// </summary>
public sealed record AssistantUsageDay
{
    /// <summary>The local day, "2026-11-07"; always written.</summary>
    [JsonPropertyName("day")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Day { get; init => field = value ?? ""; } = "";

    /// <summary>The ai account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>Input tokens.</summary>
    [JsonPropertyName("inputTokens")]
    public long InputTokens { get; init; }

    /// <summary>Output tokens.</summary>
    [JsonPropertyName("outputTokens")]
    public long OutputTokens { get; init; }

    /// <summary>Input tokens read from the provider's cache.</summary>
    [JsonPropertyName("cacheReadInputTokens")]
    public long CacheReadInputTokens { get; init; }

    /// <summary>Input tokens written to the provider's cache.</summary>
    [JsonPropertyName("cacheCreationInputTokens")]
    public long CacheCreationInputTokens { get; init; }

    /// <summary>The sessions of the day.</summary>
    [JsonPropertyName("sessions")]
    public int Sessions { get; init; }

    /// <summary>The turns of the day.</summary>
    [JsonPropertyName("turns")]
    public int Turns { get; init; }

    /// <summary>The embedded usage (Swift <c>usage</c>).</summary>
    [JsonIgnore]
    public AssistantUsage Usage => new()
    {
        InputTokens = InputTokens,
        OutputTokens = OutputTokens,
        CacheReadInputTokens = CacheReadInputTokens,
        CacheCreationInputTokens = CacheCreationInputTokens,
    };
}

/// <summary>api.AssistantUsageResult.</summary>
public sealed record AssistantUsageResult
{
    /// <summary>One entry per account and day; absent or null reads as empty.</summary>
    [JsonPropertyName("days")]
    [JsonConverter(typeof(NullAsEmptyListConverter<AssistantUsageDay>))]
    public IReadOnlyList<AssistantUsageDay> Days { get; init => field = value ?? []; } = [];
}

// MARK: - Events

/// <summary>api.AssistantEventKind: what a <c>notify.assistantEvent</c> carries.</summary>
[JsonConverter(typeof(StringWireValueConverter<AssistantEventKind>))]
public readonly record struct AssistantEventKind(string Value) : IWireEnumeration<AssistantEventKind>
{
    /// <summary>Starts a turn: <see cref="AssistantEvent.Tools"/> lists the tools the model has.</summary>
    public const string Init = "init";

    /// <summary>A piece of the answer as it streams (<see cref="AssistantEvent.Text"/>).</summary>
    public const string TextDelta = "textDelta";

    /// <summary>A whole block of the answer (<see cref="AssistantEvent.Text"/>), after its deltas.</summary>
    public const string Text = "text";

    /// <summary>The model called <see cref="AssistantEvent.Tool"/> (<see cref="AssistantEvent.Id"/> is the call's id).</summary>
    public const string ToolUse = "toolUse";

    /// <summary>
    /// The bridge answered the call <see cref="AssistantEvent.Id"/> with
    /// <see cref="AssistantEvent.Text"/>, the bridge's result verbatim (an
    /// error when <see cref="AssistantEvent.IsError"/>).
    /// </summary>
    public const string ToolResult = "toolResult";

    /// <summary>Ends a turn (<see cref="AssistantEvent.Result"/>).</summary>
    public const string Result = "result";

    /// <summary>
    /// The turn or the session failed (<see cref="AssistantEvent.Failure"/>);
    /// a session failure ends the session.
    /// </summary>
    public const string Failure = "failure";

    /// <summary>
    /// The tokens of the turn so far (<see cref="AssistantEvent.Usage"/>); the
    /// last one, right before <see cref="Result"/>, is the turn's whole usage.
    /// </summary>
    public const string Usage = "usage";

    /// <summary>The eight kinds, in the order of ai.go.</summary>
    public static IReadOnlyList<AssistantEventKind> All { get; } = [Init, TextDelta, Text, ToolUse, ToolResult, Result, Failure, Usage];

    /// <summary>The value of a wire string.</summary>
    public static implicit operator AssistantEventKind(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>
/// api.AssistantFailureClass: why a turn or session failed; the application
/// words it, the daemon's <see cref="AssistantFailure.Message"/> is not for
/// display.
/// </summary>
[JsonConverter(typeof(StringWireValueConverter<AssistantFailureClass>))]
public readonly record struct AssistantFailureClass(string Value) : IWireEnumeration<AssistantFailureClass>
{
    /// <summary>The key was refused (401).</summary>
    public const string Auth = "auth";

    /// <summary>The key may not use this (403).</summary>
    public const string Permission = "permission";

    /// <summary>Too many requests (429); <see cref="AssistantFailure.RetryAfter"/> when known.</summary>
    public const string RateLimit = "rateLimit";

    /// <summary>The provider is busy (529, 503).</summary>
    public const string Overloaded = "overloaded";

    /// <summary>The account has no credit or used up its quota; never retried.</summary>
    public const string Quota = "quota";

    /// <summary>The server has no such model or could not load it.</summary>
    public const string Model = "model";

    /// <summary>The provider refused the request (400).</summary>
    public const string InvalidRequest = "invalidRequest";

    /// <summary>The model cannot call tools.</summary>
    public const string ToolsUnsupported = "toolsUnsupported";

    /// <summary>No connection, timeout, TLS.</summary>
    public const string Network = "network";

    /// <summary>The provider's answer could not be read.</summary>
    public const string Protocol = "protocol";

    /// <summary>The history grew past <see cref="API.Limits.MaxAssistantHistoryBytes"/>.</summary>
    public const string ContextFull = "contextFull";

    /// <summary>The MCP bridge could not be started or died.</summary>
    public const string Bridge = "bridge";

    /// <summary>The turn exceeded its timeout.</summary>
    public const string Timeout = "timeout";

    /// <summary>The turn exceeded <see cref="API.Limits.MaxAssistantTurnTools"/> tool rounds.</summary>
    public const string Limit = "limit";

    /// <summary><c>assistant.sessionStop</c>, the connection closed, the daemon stops.</summary>
    public const string Stopped = "stopped";

    /// <summary>The fifteen classes, in the order of ai.go.</summary>
    public static IReadOnlyList<AssistantFailureClass> All { get; } =
    [
        Auth, Permission, RateLimit, Overloaded, Quota, Model, InvalidRequest, ToolsUnsupported,
        Network, Protocol, ContextFull, Bridge, Timeout, Limit, Stopped,
    ];

    /// <summary>The value of a wire string.</summary>
    public static implicit operator AssistantFailureClass(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>
/// api.AssistantResult: the end of a turn, whether the model finished
/// (<see cref="Success"/>; false after a failure or a refusal),
/// <see cref="Structured"/> the answer as raw JSON when a schema was asked
/// for and the answer is valid JSON, <see cref="Reason"/> the model's stop
/// reason as a word ("endTurn", "maxTokens", "refusal").
/// </summary>
public sealed record AssistantResult
{
    /// <summary>The model finished; always written.</summary>
    [JsonPropertyName("success")]
    public bool Success { get; init; }

    /// <summary>The structured answer, raw; a clone that owns its memory, null for none.</summary>
    [JsonPropertyName("structured")]
    public JsonElement? Structured { get; init => field = RawJson.Own(value); }

    /// <summary>The model's stop reason as a word; null for none.</summary>
    [JsonPropertyName("reason")]
    public string? Reason { get; init => field = string.IsNullOrEmpty(value) ? null : value; }
}

/// <summary>
/// api.AssistantFailure: the class of a failure, <see cref="RetryAfter"/>
/// seconds when the provider said so, and the daemon's technical message
/// (never shown).
/// </summary>
public sealed record AssistantFailure
{
    /// <summary>Why it failed.</summary>
    [JsonPropertyName("class")]
    public required AssistantFailureClass Class { get; init; }

    /// <summary>Seconds to wait before trying again; 0 (written as absent) when unknown.</summary>
    [JsonPropertyName("retryAfter")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingDefault)]
    public int RetryAfter { get; init; }

    /// <summary>The daemon's technical message, never shown; null for none.</summary>
    [JsonPropertyName("message")]
    public string? Message { get; init => field = string.IsNullOrEmpty(value) ? null : value; }
}

/// <summary>
/// api.AssistantEvent: the params of <c>notify.assistantEvent</c>, sent to
/// the session's connection only. Every text is the model's or the bridge's
/// and shown as plain text only.
/// </summary>
public sealed record AssistantEvent
{
    /// <summary>The session; always written.</summary>
    [JsonPropertyName("sessionId")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string SessionId { get; init => field = value ?? ""; } = "";

    /// <summary>What it carries.</summary>
    [JsonPropertyName("kind")]
    public required AssistantEventKind Kind { get; init; }

    /// <summary>The answer's text or the tool's result; null for none.</summary>
    [JsonPropertyName("text")]
    public string? Text { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The tools of an init event; absent or null reads as empty.</summary>
    [JsonPropertyName("tools")]
    [JsonConverter(typeof(NullAsEmptyListConverter<string>))]
    public IReadOnlyList<string> Tools { get; init => field = value ?? []; } = [];

    /// <summary>The tool a toolUse event names; null for none.</summary>
    [JsonPropertyName("tool")]
    public string? Tool { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The tool call's id; null for none.</summary>
    [JsonPropertyName("id")]
    public string? Id { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The tool result is an error; false is written as absent.</summary>
    [JsonPropertyName("isError")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingDefault)]
    public bool IsError { get; init; }

    /// <summary>The end of the turn.</summary>
    [JsonPropertyName("result")]
    public AssistantResult? Result { get; init; }

    /// <summary>Why the turn or session failed.</summary>
    [JsonPropertyName("failure")]
    public AssistantFailure? Failure { get; init; }

    /// <summary>The tokens of the turn so far.</summary>
    [JsonPropertyName("usage")]
    public AssistantUsage? Usage { get; init; }
}

/// <summary>
/// api.AssistantUsageChangedNotification: an <c>ai</c> account's usage of
/// today changed; the application runs <c>assistant.usage</c> again.
/// </summary>
public sealed record AssistantUsageChangedNotification
{
    /// <summary>The ai account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }
}

// MARK: - Durations

/// <summary>
/// The durations of ai.go (the integer limits are in
/// <see cref="API.Limits"/>); the client's waits for the assistant's
/// methods are <see cref="Malachi.Core.Platform.RpcTimeouts.AssistantPrompt"/>,
/// <see cref="Malachi.Core.Platform.RpcTimeouts.AssistantSessionStart"/> and
/// <see cref="Malachi.Core.Platform.RpcTimeouts.AssistantModels"/>.
/// </summary>
public static class AssistantLimits
{
    /// <summary>api.AssistantSessionIdle: a session nobody wrote to ends.</summary>
    public static readonly TimeSpan SessionIdle = TimeSpan.FromMinutes(30);

    /// <summary>api.DefaultAssistantTimeout: one turn of a panel session.</summary>
    public static readonly TimeSpan DefaultTimeout = TimeSpan.FromMinutes(5);

    /// <summary>api.MaxAssistantTimeout: the longest a triage session may ask for.</summary>
    public static readonly TimeSpan MaxTimeout = TimeSpan.FromMinutes(15);
}

// A json.RawMessage member's element that survives the document it was read
// from; an undefined or null element is no value (Swift RawJSON).
internal static class RawJson
{
    public static JsonElement? Own(JsonElement? value) =>
        value is { } v && v.ValueKind is not (JsonValueKind.Undefined or JsonValueKind.Null) ? v.Clone() : null;
}
