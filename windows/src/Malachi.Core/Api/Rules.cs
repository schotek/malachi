// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Go: backend/pkg/api/rules.go; contract: docs/api.md §4.21, §5
// (notify.rulesChanged), §2 (1112 ruleNotFound); design:
// docs/plans/rules.md. Written from the Go types; the macOS client mirrors
// them in MalachiCore/API/RulesAPI.swift at the same time.
//
// Mail rules: conditions over a message and actions on it. The daemon
// stores the rules, evaluates the enabled ones, in their order, on the mail
// it stores in the inbox of a mail account, and runs them on stored mail by
// hand (rules.apply, local actions only). Every string of a rule can come
// from mail (a prefilled sender, a list id) and is shown as plain text only
// (TextBlock.Text, TextBox.Text).
//
// Windows: Go's Rule embeds RuleInput; the record Rule derives from the
// record RuleInput, which is not sealed for it (as Calendar.cs's Occurrence
// and CalendarEvent). Go's omitempty strings and identifiers are nullable
// here and read an empty string as absent; a Go slice that may be nil is a
// list that reads null as empty. RulesListParams is EmptyParams,
// RulesDeleteResult EmptyResult, and RulesChangedNotification (no params)
// is DaemonNotification.RulesChanged.

using System;
using System.Collections.Generic;
using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

/// <summary>api.RuleMatch: how a rule's conditions combine.</summary>
[JsonConverter(typeof(StringWireValueConverter<RuleMatch>))]
public readonly record struct RuleMatch(string Value) : IWireEnumeration<RuleMatch>
{
    /// <summary>Every condition holds.</summary>
    public const string All = "all";

    /// <summary>At least one condition holds.</summary>
    public const string Any = "any";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator RuleMatch(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.RuleField: what a condition looks at.</summary>
[JsonConverter(typeof(StringWireValueConverter<RuleField>))]
public readonly record struct RuleField(string Value) : IWireEnumeration<RuleField>
{
    /// <summary>The From addresses (name and address).</summary>
    public const string From = "from";

    /// <summary>The To addresses.</summary>
    public const string To = "to";

    /// <summary>The Cc addresses.</summary>
    public const string Cc = "cc";

    /// <summary>The To and Cc addresses.</summary>
    public const string ToOrCc = "toOrCc";

    /// <summary>The subject.</summary>
    public const string Subject = "subject";

    /// <summary>The plain text of the message, its first <see cref="RuleLimits.MaxRuleBodyBytes"/>.</summary>
    public const string Body = "body";

    /// <summary>The List-Id identifier (<see cref="BulkInfo"/>'s list id).</summary>
    public const string ListId = "listId";

    /// <summary>The bulk classification: is and isNot with <see cref="RuleValues.BulkAny"/> or a <see cref="BulkKind"/>.</summary>
    public const string Bulk = "bulk";

    /// <summary>Operator is with the value "true" or "false".</summary>
    public const string HasAttachments = "hasAttachments";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator RuleField(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.RuleOperator: how a condition compares (case ignored after NFC normalisation).</summary>
[JsonConverter(typeof(StringWireValueConverter<RuleOperator>))]
public readonly record struct RuleOperator(string Value) : IWireEnumeration<RuleOperator>
{
    /// <summary>api.RuleOpContains.</summary>
    public const string Contains = "contains";

    /// <summary>api.RuleOpNotContains.</summary>
    public const string NotContains = "notContains";

    /// <summary>api.RuleOpIs.</summary>
    public const string Is = "is";

    /// <summary>api.RuleOpIsNot.</summary>
    public const string IsNot = "isNot";

    /// <summary>api.RuleOpStartsWith.</summary>
    public const string StartsWith = "startsWith";

    /// <summary>api.RuleOpEndsWith.</summary>
    public const string EndsWith = "endsWith";

    /// <summary>The address's domain is the value or a subdomain of it (address fields only).</summary>
    public const string DomainIs = "domainIs";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator RuleOperator(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.RuleActionKind: what an action does.</summary>
[JsonConverter(typeof(StringWireValueConverter<RuleActionKind>))]
public readonly record struct RuleActionKind(string Value) : IWireEnumeration<RuleActionKind>
{
    /// <summary>Moves the message to a folder of the rule's account.</summary>
    public const string Move = "move";

    /// <summary>Marks the message read.</summary>
    public const string MarkRead = "markRead";

    /// <summary>Stars the message.</summary>
    public const string Flag = "flag";

    /// <summary>Moves the message to the account's trash.</summary>
    public const string Trash = "trash";

    /// <summary>Moves the message to the account's junk folder.</summary>
    public const string Junk = "junk";

    /// <summary>Sends the message, attached whole, to one address (outgoing).</summary>
    public const string Forward = "forward";

    /// <summary>Answers the sender with a text (outgoing).</summary>
    public const string AutoReply = "autoReply";

    /// <summary>api.RuleActionKind.IsOutgoing: the action sends mail.</summary>
    public bool IsOutgoing => Value is Forward or AutoReply;

    /// <summary>The value of a wire string.</summary>
    public static implicit operator RuleActionKind(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.RuleProblem: why the daemon stopped a rule (it is disabled then).</summary>
[JsonConverter(typeof(StringWireValueConverter<RuleProblem>))]
public readonly record struct RuleProblem(string Value) : IWireEnumeration<RuleProblem>
{
    /// <summary>The folder of a move action is gone.</summary>
    public const string FolderGone = "folderGone";

    /// <summary>An outgoing action could not be queued.</summary>
    public const string SendFailed = "sendFailed";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator RuleProblem(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.RuleCondition: one condition of a rule.</summary>
public sealed record RuleCondition
{
    /// <summary>What it looks at.</summary>
    [JsonPropertyName("field")]
    public required RuleField Field { get; init; }

    /// <summary>How it compares.</summary>
    [JsonPropertyName("op")]
    public required RuleOperator Op { get; init; }

    /// <summary>The value compared with; for bulk and hasAttachments one of their fixed values.</summary>
    [JsonPropertyName("value")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Value { get; init => field = value ?? ""; } = "";
}

/// <summary>api.RuleAction: one action of a rule; only the members of its kind are set.</summary>
public sealed record RuleAction
{
    /// <summary>What it does.</summary>
    [JsonPropertyName("kind")]
    public required RuleActionKind Kind { get; init; }

    /// <summary>The target of move; null for any other kind.</summary>
    [JsonPropertyName("folderId")]
    public FolderId? FolderId { get; init => field = value is { Value.Length: > 0 } ? value : null; }

    /// <summary>The one address of forward; null for any other kind.</summary>
    [JsonPropertyName("to")]
    public string? To { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The subject of autoReply; null: "Re: " and the message's subject.</summary>
    [JsonPropertyName("subject")]
    public string? Subject { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The plain text of autoReply.</summary>
    [JsonPropertyName("text")]
    public string? Text { get; init => field = string.IsNullOrEmpty(value) ? null : value; }
}

/// <summary>api.RuleInput: a rule as the user edits it.</summary>
public record RuleInput
{
    /// <summary>The name, 1 to <see cref="RuleLimits.MaxRuleNameBytes"/> bytes.</summary>
    [JsonPropertyName("name")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Name { get; init => field = value ?? ""; } = "";

    /// <summary>The rule runs.</summary>
    [JsonPropertyName("enabled")]
    public bool Enabled { get; init; }

    /// <summary>The one mail account the rule is for; null: every enabled mail account (no move action then).</summary>
    [JsonPropertyName("accountId")]
    public AccountId? AccountId { get; init => field = value is { Value.Length: > 0 } ? value : null; }

    /// <summary>How the conditions combine.</summary>
    [JsonPropertyName("match")]
    public RuleMatch Match { get; init; } = RuleMatch.All;

    /// <summary>The conditions.</summary>
    [JsonPropertyName("conditions")]
    [JsonConverter(typeof(NullAsEmptyListConverter<RuleCondition>))]
    public IReadOnlyList<RuleCondition> Conditions { get; init => field = value ?? []; } = [];

    /// <summary>The actions.</summary>
    [JsonPropertyName("actions")]
    [JsonConverter(typeof(NullAsEmptyListConverter<RuleAction>))]
    public IReadOnlyList<RuleAction> Actions { get; init => field = value ?? []; } = [];

    /// <summary>No later rule runs on a message this rule matched.</summary>
    [JsonPropertyName("stop")]
    public bool Stop { get; init; }
}

/// <summary>api.Rule: a stored rule.</summary>
public sealed record Rule : RuleInput
{
    /// <summary>The rule's id, "r_…".</summary>
    [JsonPropertyName("id")]
    public required string Id { get; init; }

    /// <summary>An action sends mail (forward, autoReply).</summary>
    [JsonPropertyName("outgoing")]
    public bool Outgoing { get; init; }

    /// <summary>Why the daemon disabled the rule; null when it did not.</summary>
    [JsonPropertyName("problem")]
    public RuleProblem? Problem { get; init => field = value is { Value.Length: > 0 } ? value : null; }

    /// <summary>When the rule was created.</summary>
    [JsonPropertyName("createdAt")]
    public DateTimeOffset CreatedAt { get; init; }

    /// <summary>When it was last changed.</summary>
    [JsonPropertyName("updatedAt")]
    public DateTimeOffset UpdatedAt { get; init; }

    /// <summary>The messages the rule acted on (automatically and by rules.apply).</summary>
    [JsonPropertyName("applied")]
    public int Applied { get; init; }

    /// <summary>The latest of them; null when it never acted.</summary>
    [JsonPropertyName("lastAppliedAt")]
    public DateTimeOffset? LastAppliedAt { get; init; }

    /// <summary>The outgoing actions the protections refused (bulk or automatic mail, own address, limits).</summary>
    [JsonPropertyName("skipped")]
    public int Skipped { get; init; }
}

/// <summary>The fixed values of rule conditions (api.RuleBulkAny).</summary>
public static class RuleValues
{
    /// <summary>api.RuleBulkAny: the value of a bulk condition that holds for every kind.</summary>
    public const string BulkAny = "any";
}

/// <summary>The limits of rules the daemon enforces (api.MaxRule*, RuleOutgoingPerHour).</summary>
public static class RuleLimits
{
    /// <summary>The most rules (api.MaxRules).</summary>
    public const int MaxRules = 200;

    /// <summary>The most conditions of a rule.</summary>
    public const int MaxRuleConditions = 20;

    /// <summary>The most actions of a rule.</summary>
    public const int MaxRuleActions = 10;

    /// <summary>A name, in bytes.</summary>
    public const int MaxRuleNameBytes = 200;

    /// <summary>A value, a forward address and a subject, in bytes.</summary>
    public const int MaxRuleValueBytes = 500;

    /// <summary>An automatic reply's text, in bytes.</summary>
    public const int MaxRuleReplyBytes = 8192;

    /// <summary>The first bytes of the plain text a body condition reads (64 * 1024).</summary>
    public const int MaxRuleBodyBytes = 64 * 1024;

    /// <summary><c>rules.test</c>: the most messages it lists.</summary>
    public const int MaxRuleTestMessages = 50;

    /// <summary>What one rule sends in an hour.</summary>
    public const int RuleOutgoingPerHour = 20;

    /// <summary>api.RuleReplyInterval: how long autoReply leaves a sender alone after answering it (RFC 3834).</summary>
    public static readonly TimeSpan RuleReplyInterval = TimeSpan.FromDays(4);
}

/// <summary>
/// api.RuleInvalidData: <c>error.data</c> of invalidArgument for a rule the
/// daemon refuses: the field ("name", "accountId", "conditions", "actions",
/// "confirmOutgoing") and, for conditions and actions, the index.
/// </summary>
public sealed record RuleInvalidData
{
    /// <summary>The refused field.</summary>
    [JsonPropertyName("field")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Field { get; init => field = value ?? ""; } = "";

    /// <summary>The row of a condition or an action.</summary>
    [JsonPropertyName("index")]
    public int Index { get; init; }
}

/// <summary>api.RulesListResult: every rule in its order.</summary>
public sealed record RulesListResult
{
    /// <summary>The rules; never null.</summary>
    [JsonPropertyName("rules")]
    [JsonConverter(typeof(NullAsEmptyListConverter<Rule>))]
    public IReadOnlyList<Rule> Rules { get; init => field = value ?? []; } = [];
}

/// <summary>
/// api.RulesCreateParams: adds a rule at the end. <see cref="ConfirmOutgoing"/>
/// is required when the rule is enabled with an outgoing action: the client
/// asked the user.
/// </summary>
public sealed record RulesCreateParams
{
    /// <summary>The rule.</summary>
    [JsonPropertyName("rule")]
    public required RuleInput Rule { get; init; }

    /// <summary>The user was asked (omitted when false).</summary>
    [JsonPropertyName("confirmOutgoing")]
    public bool? ConfirmOutgoing { get; init => field = value == true ? true : null; }
}

/// <summary>api.RulesCreateResult: the new rule.</summary>
public sealed record RulesCreateResult
{
    /// <summary>The rule.</summary>
    [JsonPropertyName("rule")]
    public required Rule Rule { get; init; }
}

/// <summary>api.RulesUpdateParams: replaces a rule's input.</summary>
public sealed record RulesUpdateParams
{
    /// <summary>The rule.</summary>
    [JsonPropertyName("id")]
    public required string Id { get; init; }

    /// <summary>Its new input.</summary>
    [JsonPropertyName("rule")]
    public required RuleInput Rule { get; init; }

    /// <summary>
    /// The user was asked: required when the updated rule is enabled with an
    /// outgoing action and was not before, or an outgoing action's To,
    /// Subject or Text changed (omitted when false).
    /// </summary>
    [JsonPropertyName("confirmOutgoing")]
    public bool? ConfirmOutgoing { get; init => field = value == true ? true : null; }
}

/// <summary>api.RulesUpdateResult: the rule after the update.</summary>
public sealed record RulesUpdateResult
{
    /// <summary>The rule.</summary>
    [JsonPropertyName("rule")]
    public required Rule Rule { get; init; }
}

/// <summary>api.RulesDeleteParams: names the rule to delete.</summary>
public sealed record RulesDeleteParams
{
    /// <summary>The rule.</summary>
    [JsonPropertyName("id")]
    public required string Id { get; init; }
}

/// <summary>api.RulesReorderParams: the whole new order, every rule's id once.</summary>
public sealed record RulesReorderParams
{
    /// <summary>The ids.</summary>
    [JsonPropertyName("ids")]
    [JsonConverter(typeof(NullAsEmptyListConverter<string>))]
    public IReadOnlyList<string> Ids { get; init => field = value ?? []; } = [];
}

/// <summary>api.RulesReorderResult: every rule in the new order.</summary>
public sealed record RulesReorderResult
{
    /// <summary>The rules; never null.</summary>
    [JsonPropertyName("rules")]
    [JsonConverter(typeof(NullAsEmptyListConverter<Rule>))]
    public IReadOnlyList<Rule> Rules { get; init => field = value ?? []; } = [];
}

/// <summary>api.RulesTestParams: which stored messages of a folder a rule, as edited and not saved, matches.</summary>
public sealed record RulesTestParams
{
    /// <summary>The rule as edited.</summary>
    [JsonPropertyName("rule")]
    public required RuleInput Rule { get; init; }

    /// <summary>The folder's account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The folder.</summary>
    [JsonPropertyName("folderId")]
    public required FolderId FolderId { get; init; }
}

/// <summary>api.RulesTestResult: how many stored messages of the folder match and the newest of them.</summary>
public sealed record RulesTestResult
{
    /// <summary>How many match.</summary>
    [JsonPropertyName("count")]
    public int Count { get; init; }

    /// <summary>The newest <see cref="RuleLimits.MaxRuleTestMessages"/> of them, newest first.</summary>
    [JsonPropertyName("messages")]
    [JsonConverter(typeof(NullAsEmptyListConverter<MessageSummary>))]
    public IReadOnlyList<MessageSummary> Messages { get; init => field = value ?? []; } = [];
}

/// <summary>
/// api.RulesApplyParams: runs rules by hand on stored mail, a folder's
/// messages (<see cref="FolderId"/>) or <see cref="MessageIds"/> of one
/// account, never both. Only the local actions run.
/// </summary>
public sealed record RulesApplyParams
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The folder; null when messages are given.</summary>
    [JsonPropertyName("folderId")]
    public FolderId? FolderId { get; init => field = value is { Value.Length: > 0 } ? value : null; }

    /// <summary>The messages; empty when a folder is given.</summary>
    [JsonPropertyName("messageIds")]
    [JsonConverter(typeof(NullAsEmptyListConverter<MessageId>))]
    public IReadOnlyList<MessageId> MessageIds { get; init => field = value ?? []; } = [];

    /// <summary>The rules to run; empty: every enabled rule of the account.</summary>
    [JsonPropertyName("ruleIds")]
    [JsonConverter(typeof(NullAsEmptyListConverter<string>))]
    public IReadOnlyList<string> RuleIds { get; init => field = value ?? []; } = [];
}

/// <summary>api.RulesApplyResult: counts what rules.apply did.</summary>
public sealed record RulesApplyResult
{
    /// <summary>The messages looked at.</summary>
    [JsonPropertyName("examined")]
    public int Examined { get; init; }

    /// <summary>The messages a rule matched.</summary>
    [JsonPropertyName("matched")]
    public int Matched { get; init; }

    /// <summary>The messages an action changed.</summary>
    [JsonPropertyName("changed")]
    public int Changed { get; init; }

    /// <summary>The outgoing actions left out.</summary>
    [JsonPropertyName("skippedOutgoing")]
    public int SkippedOutgoing { get; init; }
}
