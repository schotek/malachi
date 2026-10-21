// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/API/AssistantProfileAPI.swift; Go:
// backend/pkg/api/assistant.go; contract: docs/api.md §4.18, §5
// (notify.assistantProfileChanged); design: docs/plans/assistant-profile.md.
//
// The assistant profile: what the user wrote about themselves for an
// assistant, the grammatical gender they write in, a note for each mail and
// Jira account, and the notes an assistant proposed. Every string is plain
// text the daemon cleaned; a proposal's text is an assistant's, so it is
// shown as plain text only (TextBlock.Text). The limits are in API.Limits.

using System;
using System.Collections.Generic;
using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

/// <summary>api.ProfileGender: the grammatical gender the user writes in.</summary>
[JsonConverter(typeof(StringWireValueConverter<ProfileGender>))]
public readonly record struct ProfileGender(string Value) : IWireEnumeration<ProfileGender>
{
    /// <summary>The user does not say.</summary>
    public const string Unset = "unset";

    /// <summary>Masculine grammatical forms.</summary>
    public const string Masculine = "masculine";

    /// <summary>Feminine grammatical forms.</summary>
    public const string Feminine = "feminine";

    /// <summary>Forms without grammatical gender where possible.</summary>
    public const string Neutral = "neutral";

    /// <summary>The four values, in the order Preferences offers them.</summary>
    public static IReadOnlyList<ProfileGender> All { get; } = [Unset, Masculine, Feminine, Neutral];

    /// <summary>The value of a wire string.</summary>
    public static implicit operator ProfileGender(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.ProfileSource: where a proposal came from.</summary>
[JsonConverter(typeof(StringWireValueConverter<ProfileSource>))]
public readonly record struct ProfileSource(string Value) : IWireEnumeration<ProfileSource>
{
    /// <summary>The assistant panel of Malachi Mail.</summary>
    public const string Panel = "panel";

    /// <summary>Another MCP client (Claude Desktop, Claude Code).</summary>
    public const string Mcp = "mcp";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator ProfileSource(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.ProfileAccountNote: the note of one account.</summary>
public sealed record ProfileAccountNote
{
    /// <summary>A note of this account.</summary>
    public ProfileAccountNote(AccountId accountId, string note)
    {
        AccountId = accountId;
        Note = note;
    }

    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public AccountId AccountId { get; init; }

    /// <summary>The note ("" in a write removes it).</summary>
    [JsonPropertyName("note")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Note { get; init => field = value ?? ""; } = "";
}

/// <summary>
/// api.AssistantProfile: the stored profile. <see cref="Accounts"/> lists only
/// accounts that exist and have a note, in <c>account.list</c> order.
/// </summary>
public sealed record AssistantProfile
{
    /// <summary>What the user wrote about themselves.</summary>
    [JsonPropertyName("text")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Text { get; init => field = value ?? ""; } = "";

    /// <summary>The gender the user writes in.</summary>
    [JsonPropertyName("gender")]
    public ProfileGender Gender { get; init; } = ProfileGender.Unset;

    /// <summary>The notes of the accounts.</summary>
    [JsonPropertyName("accounts")]
    [JsonConverter(typeof(NullAsEmptyListConverter<ProfileAccountNote>))]
    public IReadOnlyList<ProfileAccountNote> Accounts { get; init => field = value ?? []; } = [];

    /// <summary>
    /// The MCP bridge gives the profile to MCP clients other than Malachi Mail
    /// itself. Default true (absent reads true).
    /// </summary>
    [JsonPropertyName("shareWithMcp")]
    public bool ShareWithMcp { get; init; } = true;

    /// <summary>When the profile last changed; null: never.</summary>
    [JsonPropertyName("updatedAt")]
    public DateTimeOffset? UpdatedAt { get; init; }
}

/// <summary>
/// api.ProfileProposal: a note an assistant proposed; part of the profile only
/// once the user accepts it.
/// </summary>
public sealed record ProfileProposal
{
    /// <summary>The proposal's id ("p_…").</summary>
    [JsonPropertyName("id")]
    public required string Id { get; init; }

    /// <summary>The note, an assistant's plain text.</summary>
    [JsonPropertyName("text")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Text { get; init => field = value ?? ""; } = "";

    /// <summary>A note for this account; null: the global text.</summary>
    [JsonPropertyName("accountId")]
    public AccountId? AccountId { get; init; }

    /// <summary>Where it came from.</summary>
    [JsonPropertyName("source")]
    public ProfileSource Source { get; init; } = ProfileSource.Panel;

    /// <summary>When it was proposed.</summary>
    [JsonPropertyName("createdAt")]
    public DateTimeOffset? CreatedAt { get; init; }
}

/// <summary>api.ProfileLimits: the limits, in characters, for a client's counters.</summary>
public sealed record ProfileLimits
{
    /// <summary><see cref="AssistantProfile.Text"/>.</summary>
    [JsonPropertyName("text")]
    public int Text { get; init; } = API.Limits.MaxProfileText;

    /// <summary><see cref="ProfileAccountNote.Note"/>.</summary>
    [JsonPropertyName("accountNote")]
    public int AccountNote { get; init; } = API.Limits.MaxProfileAccountNote;

    /// <summary><see cref="ProfileProposal.Text"/>.</summary>
    [JsonPropertyName("proposal")]
    public int Proposal { get; init; } = API.Limits.MaxProfileProposal;
}

/// <summary>The result of <c>assistant.profileGet</c>.</summary>
public sealed record AssistantProfileGetResult
{
    /// <summary>The profile.</summary>
    [JsonPropertyName("profile")]
    public AssistantProfile Profile { get; init; } = new();

    /// <summary>The pending proposals, oldest first.</summary>
    [JsonPropertyName("proposals")]
    [JsonConverter(typeof(NullAsEmptyListConverter<ProfileProposal>))]
    public IReadOnlyList<ProfileProposal> Proposals { get; init => field = value ?? []; } = [];

    /// <summary>The daemon's limits; null from a daemon that sends none.</summary>
    [JsonPropertyName("limits")]
    public ProfileLimits? Limits { get; init; }
}

/// <summary>
/// api.AssistantProfileSetParams: the fields it carries change, absent (null)
/// ones stay. <see cref="Accounts"/> sets the note of each account it names
/// ("" removes it).
/// </summary>
public sealed record AssistantProfileSetParams
{
    /// <summary>The text.</summary>
    [JsonPropertyName("text")]
    public string? Text { get; init; }

    /// <summary>The gender.</summary>
    [JsonPropertyName("gender")]
    public ProfileGender? Gender { get; init; }

    /// <summary>The notes of the accounts it names.</summary>
    [JsonPropertyName("accounts")]
    public IReadOnlyList<ProfileAccountNote>? Accounts { get; init; }

    /// <summary>Sharing with MCP clients.</summary>
    [JsonPropertyName("shareWithMcp")]
    public bool? ShareWithMcp { get; init; }
}

/// <summary>The result of <c>assistant.profileSet</c>.</summary>
public sealed record AssistantProfileSetResult
{
    /// <summary>The profile as stored.</summary>
    [JsonPropertyName("profile")]
    public AssistantProfile Profile { get; init; } = new();
}

/// <summary>The params of <c>assistant.profilePropose</c> (the bridge's; clients do not call it).</summary>
public sealed record AssistantProfileProposeParams
{
    /// <summary>The note.</summary>
    [JsonPropertyName("text")]
    public required string Text { get; init; }

    /// <summary>A note for this account; null: the global text.</summary>
    [JsonPropertyName("accountId")]
    public AccountId? AccountId { get; init; }

    /// <summary>Where it comes from.</summary>
    [JsonPropertyName("source")]
    public required ProfileSource Source { get; init; }
}

/// <summary>The result of <c>assistant.profilePropose</c>.</summary>
public sealed record AssistantProfileProposeResult
{
    /// <summary>The stored (or the equal pending) proposal.</summary>
    [JsonPropertyName("proposal")]
    public required ProfileProposal Proposal { get; init; }
}

/// <summary>
/// api.AssistantProfileResolveParams: accepts (with <see cref="Text"/>, the
/// user's edit, or as proposed) or dismisses a proposal.
/// </summary>
public sealed record AssistantProfileResolveParams
{
    /// <summary>The proposal.</summary>
    [JsonPropertyName("id")]
    public required string Id { get; init; }

    /// <summary>Accept (true) or dismiss.</summary>
    [JsonPropertyName("accept")]
    public bool Accept { get; init; }

    /// <summary>With <see cref="Accept"/>: the user's edit of the text.</summary>
    [JsonPropertyName("text")]
    public string? Text { get; init; }
}

/// <summary>The result of <c>assistant.profileResolve</c>.</summary>
public sealed record AssistantProfileResolveResult
{
    /// <summary>The profile as stored.</summary>
    [JsonPropertyName("profile")]
    public AssistantProfile Profile { get; init; } = new();

    /// <summary>The proposals still pending.</summary>
    [JsonPropertyName("proposals")]
    [JsonConverter(typeof(NullAsEmptyListConverter<ProfileProposal>))]
    public IReadOnlyList<ProfileProposal> Proposals { get; init => field = value ?? []; } = [];
}
