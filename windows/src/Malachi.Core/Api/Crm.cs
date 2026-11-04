// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/API/CRMAPI.swift; Go:
// backend/pkg/api/crm.go (all of it), the CRM parts of types.go
// (AccountEway, AccountConfig.Eway, CapabilityCrm, AccountTestResult.Eway,
// MessageSummary.CrmSaved, Message.Crm) and errors.go (1109, 1205, 1307);
// contract: docs/api.md §2, §4.1 (account.detectEway), §4.17; design:
// docs/eway.md.
//
// CRM accounts and saving mail to a CRM. The only CRM is eWay-CRM
// (AccountKind.Eway). Every name and detail of a CRM item is untrusted
// display text, cleaned by the daemon and shown as plain text only
// (TextBlock.Text). Every URL is built by the daemon from the account's
// stored service URL and an item GUID of fixed shape, never taken from the
// CRM or from mail.
//
// Windows: Go's GUID members are ItemGuid (CrmItem, CrmItemRef, as
// CrmRecord's own), because a member named Guid would read as the type
// System.Guid. The optional booleans Go leaves out when false (omitempty)
// are bool? with an Is… reader, as elsewhere in the mirror. The limits of
// crm.go are in API.Limits.

using System;
using System.Collections.Generic;
using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

/// <summary>
/// api.EwayConfig: the <c>eway</c> block of an account of kind <c>eway</c>
/// (its password is <see cref="Credentials.Password"/>).
/// </summary>
public sealed record EwayConfig
{
    /// <summary>
    /// The instance's web service address, normalised by
    /// <c>account.detectEway</c>: https, no user info, query or fragment, no
    /// trailing <c>/API.svc</c> and no trailing slash
    /// (<c>https://hosting.eway-crm.com/company</c>).
    /// </summary>
    [JsonPropertyName("serviceUrl")]
    public required string ServiceUrl { get; init; }

    /// <summary>The eWay-CRM user name the password belongs to; always written.</summary>
    [JsonPropertyName("username")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Username { get; init => field = value ?? ""; } = "";

    /// <summary>
    /// The eWay-CRM user's item GUID, set by the daemon after the first
    /// sign-in; clients leave it as they got it (and drop it when the user
    /// name changes).
    /// </summary>
    [JsonPropertyName("userGuid")]
    public string? UserGuid { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>
    /// Later messages of a conversation the user saved are saved by the
    /// daemon too, with the same links (docs/eway.md D22). Always written;
    /// the assistant sends true for a new account; absent reads false.
    /// </summary>
    [JsonPropertyName("followThreads")]
    public bool FollowThreads { get; init; }
}

/// <summary>api.CrmItemKind: what a saved message can be linked to.</summary>
[JsonConverter(typeof(StringWireValueConverter<CrmItemKind>))]
public readonly record struct CrmItemKind(string Value) : IWireEnumeration<CrmItemKind>
{
    /// <summary>A contact (a person).</summary>
    public const string Contact = "contact";

    /// <summary>A company.</summary>
    public const string Company = "company";

    /// <summary>A deal (eWay-CRM's Leads).</summary>
    public const string Deal = "deal";

    /// <summary>A project (eWay-CRM's Projects).</summary>
    public const string Project = "project";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator CrmItemKind(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.CrmReason: why <c>crm.lookup</c> or <c>crm.search</c> offers an item.</summary>
[JsonConverter(typeof(StringWireValueConverter<CrmReason>))]
public readonly record struct CrmReason(string Value) : IWireEnumeration<CrmReason>
{
    /// <summary>An address of the messages is the item's e-mail.</summary>
    public const string Address = "address";

    /// <summary>A company tracking the address's domain.</summary>
    public const string Domain = "domain";

    /// <summary>Related to a found contact (its company, deal or project).</summary>
    public const string Contact = "contact";

    /// <summary>Related to a found company (its deal or project).</summary>
    public const string Company = "company";

    /// <summary>A <c>crm.search</c> result.</summary>
    public const string Search = "search";

    /// <summary>Already linked to the saved message.</summary>
    public const string Saved = "saved";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator CrmReason(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>
/// api.CrmSignInReason: why an instance cannot be signed in to
/// (<see cref="ErrorCode.CrmSignInUnsupported"/>,
/// <see cref="CrmSignInUnsupportedData"/>).
/// </summary>
[JsonConverter(typeof(StringWireValueConverter<CrmSignInReason>))]
public readonly record struct CrmSignInReason(string Value) : IWireEnumeration<CrmSignInReason>
{
    /// <summary>The instance signs in through Microsoft Entra.</summary>
    public const string Microsoft = "microsoft";

    /// <summary>The instance's plan has no API access.</summary>
    public const string Plan = "plan";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator CrmSignInReason(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>
/// api.CrmItem: one item of the CRM. <see cref="Name"/> and
/// <see cref="Detail"/> are untrusted display text (Detail: a contact's
/// e-mail and company, a deal's or project's state). <see cref="Url"/> opens
/// the item in the CRM's web or desktop application.
/// </summary>
public sealed record CrmItem
{
    /// <summary>contact, company, deal or project.</summary>
    [JsonPropertyName("kind")]
    public required CrmItemKind Kind { get; init; }

    /// <summary>The item's GUID (Go <c>GUID</c>).</summary>
    [JsonPropertyName("guid")]
    public required string ItemGuid { get; init; }

    /// <summary>Untrusted display text.</summary>
    [JsonPropertyName("name")]
    public required string Name { get; init; }

    /// <summary>Untrusted display text; null when there is none.</summary>
    [JsonPropertyName("detail")]
    public string? Detail { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>Why it is offered; null for none.</summary>
    [JsonPropertyName("reason")]
    public CrmReason? Reason { get; init; }

    /// <summary>
    /// The item is the saved message's superior item (a deal or project,
    /// eWay-CRM's OUTLOOKPROJECT relation); in suggestions, the one deal or
    /// project the client pre-checks as superior. False is written as absent.
    /// </summary>
    [JsonPropertyName("superior")]
    public bool? Superior { get; init => field = value == true ? true : null; }

    /// <summary>Whether <see cref="Superior"/> is set.</summary>
    [JsonIgnore]
    public bool IsSuperior => Superior == true;

    /// <summary>The daemon's open-item link; always written.</summary>
    [JsonPropertyName("url")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Url { get; init => field = value ?? ""; } = "";
}

/// <summary>api.CrmItemRef: names an item in <c>crm.save</c>.</summary>
public sealed record CrmItemRef
{
    /// <summary>contact, company, deal or project.</summary>
    [JsonPropertyName("kind")]
    public required CrmItemKind Kind { get; init; }

    /// <summary>The item's GUID (Go <c>GUID</c>).</summary>
    [JsonPropertyName("guid")]
    public required string ItemGuid { get; init; }
}

/// <summary>
/// api.CrmRecord: a message saved in a CRM account: the CRM's e-mail item
/// and what it is linked to, as the daemon last read it from the server.
/// </summary>
public sealed record CrmRecord
{
    /// <summary>The eway account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The CRM's e-mail item.</summary>
    [JsonPropertyName("itemGuid")]
    public required string ItemGuid { get; init; }

    /// <summary>When it was saved.</summary>
    [JsonPropertyName("savedAt")]
    public required DateTimeOffset SavedAt { get; init; }

    /// <summary>When the daemon last read it from the CRM.</summary>
    [JsonPropertyName("checkedAt")]
    public required DateTimeOffset CheckedAt { get; init; }

    /// <summary>Visible to its owner only in the CRM.</summary>
    [JsonPropertyName("private")]
    public required bool Private { get; init; }

    /// <summary>
    /// The daemon saved the message by itself, following a saved
    /// conversation (<see cref="EwayConfig.FollowThreads"/>). False is
    /// written as absent.
    /// </summary>
    [JsonPropertyName("auto")]
    public bool? Auto { get; init => field = value == true ? true : null; }

    /// <summary>What it is linked to; absent or null reads as empty, always written.</summary>
    [JsonPropertyName("items")]
    [JsonConverter(typeof(NullAsEmptyListConverter<CrmItem>))]
    public IReadOnlyList<CrmItem> Items { get; init => field = value ?? []; } = [];

    /// <summary>The daemon's link to the saved e-mail item.</summary>
    [JsonPropertyName("url")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Url { get; init => field = value ?? ""; } = "";
}

/// <summary>
/// api.AccountDetectEwayParams: what the user typed or pasted as the
/// instance's address; nothing is stored or authenticated.
/// </summary>
public sealed record AccountDetectEwayParams
{
    /// <summary>The address as typed.</summary>
    [JsonPropertyName("serviceUrl")]
    public required string ServiceUrl { get; init; }
}

/// <summary>api.AccountDetectEwayResult: the instance.</summary>
public sealed record AccountDetectEwayResult
{
    /// <summary>Normalised, as <see cref="EwayConfig.ServiceUrl"/>.</summary>
    [JsonPropertyName("serviceUrl")]
    public required string ServiceUrl { get; init; }

    /// <summary>
    /// The instance signs in through Microsoft Entra, which this version
    /// cannot do; <c>account.add</c> would answer crmSignInUnsupported.
    /// </summary>
    [JsonPropertyName("microsoftSignIn")]
    public required bool MicrosoftSignIn { get; init; }

    /// <summary>Untrusted display text; null when unknown.</summary>
    [JsonPropertyName("version")]
    public string? Version { get; init => field = string.IsNullOrEmpty(value) ? null : value; }
}

/// <summary>api.CrmSignInUnsupportedData: the <c>data</c> of <see cref="ErrorCode.CrmSignInUnsupported"/>.</summary>
public sealed record CrmSignInUnsupportedData
{
    /// <summary>microsoft or plan.</summary>
    [JsonPropertyName("reason")]
    public required CrmSignInReason Reason { get; init; }
}

/// <summary>
/// api.MessagesNotDownloadedData: the <c>data</c> of
/// <see cref="ErrorCode.PartNotDownloaded"/> from <c>crm.save</c>: the
/// messages not whole on this device; <c>message.download</c> fetches each,
/// then the client calls <c>crm.save</c> again.
/// </summary>
public sealed record MessagesNotDownloadedData
{
    /// <summary>The messages; absent or null reads as empty.</summary>
    [JsonPropertyName("messageIds")]
    [JsonConverter(typeof(NullAsEmptyListConverter<MessageId>))]
    public IReadOnlyList<MessageId> MessageIds { get; init => field = value ?? []; } = [];
}

/// <summary>
/// api.CrmLookupParams: what the messages are linked to in the CRM and what
/// they could be linked to. A null <see cref="AccountId"/> is the first
/// enabled eway account in <c>account.list</c> order.
/// </summary>
public sealed record CrmLookupParams
{
    /// <summary>The eway account; null for the first enabled one.</summary>
    [JsonPropertyName("accountId")]
    public AccountId? AccountId { get; init; }

    /// <summary>The mail account of the messages.</summary>
    [JsonPropertyName("messageAccountId")]
    public required AccountId MessageAccountId { get; init; }

    /// <summary>1 to <see cref="API.Limits.MaxCrmMessages"/> messages.</summary>
    [JsonPropertyName("messageIds")]
    [JsonConverter(typeof(NullAsEmptyListConverter<MessageId>))]
    public IReadOnlyList<MessageId> MessageIds { get; init => field = value ?? []; } = [];
}

/// <summary>api.CrmSavedMessage: whether one message of the lookup is saved; a null record is not saved in that account.</summary>
public sealed record CrmSavedMessage
{
    /// <summary>The message.</summary>
    [JsonPropertyName("messageId")]
    public required MessageId MessageId { get; init; }

    /// <summary>Its record; null when it is not saved there (Go writes null).</summary>
    [JsonPropertyName("record")]
    public CrmRecord? Record { get; init; }
}

/// <summary>
/// api.CrmLookupResult: <see cref="Saved"/> has one entry per requested
/// message, in order. <see cref="Suggestions"/> are the items the addresses
/// of the messages lead to, ranked, at most
/// <see cref="API.Limits.MaxCrmSuggestions"/>; <see cref="Partial"/>: the
/// CRM did not answer every lookup within the daemon's budget.
/// </summary>
public sealed record CrmLookupResult
{
    /// <summary>The eway account that answered.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>One entry per requested message.</summary>
    [JsonPropertyName("saved")]
    [JsonConverter(typeof(NullAsEmptyListConverter<CrmSavedMessage>))]
    public IReadOnlyList<CrmSavedMessage> Saved { get; init => field = value ?? []; } = [];

    /// <summary>The ranked suggestions.</summary>
    [JsonPropertyName("suggestions")]
    [JsonConverter(typeof(NullAsEmptyListConverter<CrmItem>))]
    public IReadOnlyList<CrmItem> Suggestions { get; init => field = value ?? []; } = [];

    /// <summary>The CRM did not answer every lookup in time; false is written as absent.</summary>
    [JsonPropertyName("partial")]
    public bool? Partial { get; init => field = value == true ? true : null; }

    /// <summary>Whether <see cref="Partial"/> is set.</summary>
    [JsonIgnore]
    public bool IsPartial => Partial == true;
}

/// <summary>api.CrmSearchParams: searches the CRM's items by name; null <see cref="Kinds"/> is all.</summary>
public sealed record CrmSearchParams
{
    /// <summary>The eway account; null for the first enabled one.</summary>
    [JsonPropertyName("accountId")]
    public AccountId? AccountId { get; init; }

    /// <summary>
    /// <see cref="API.Limits.MinCrmQueryBytes"/> to
    /// <see cref="API.Limits.MaxCrmQueryBytes"/> bytes after trimming.
    /// </summary>
    [JsonPropertyName("query")]
    public required string Query { get; init; }

    /// <summary>The kinds to search; null for all.</summary>
    [JsonPropertyName("kinds")]
    public IReadOnlyList<CrmItemKind>? Kinds { get; init; }
}

/// <summary>
/// api.CrmSearchResult: at most <see cref="API.Limits.MaxCrmSearchResults"/>
/// items, <see cref="Truncated"/> when there were more.
/// </summary>
public sealed record CrmSearchResult
{
    /// <summary>The items found.</summary>
    [JsonPropertyName("items")]
    [JsonConverter(typeof(NullAsEmptyListConverter<CrmItem>))]
    public IReadOnlyList<CrmItem> Items { get; init => field = value ?? []; } = [];

    /// <summary>There were more; false is written as absent.</summary>
    [JsonPropertyName("truncated")]
    public bool? Truncated { get; init => field = value == true ? true : null; }
}

/// <summary>
/// api.CrmSaveParams: saves messages of one mail account to the CRM, or
/// changes the links of a saved one. <see cref="Link"/> items are added,
/// <see cref="Unlink"/> items removed (only with one message, already
/// saved); <see cref="Superior"/> is the deal or project linked as the
/// superior item (null: none added). <see cref="Private"/> makes the saved
/// item visible to the user only.
/// </summary>
public sealed record CrmSaveParams
{
    /// <summary>The eway account; null for the first enabled one.</summary>
    [JsonPropertyName("accountId")]
    public AccountId? AccountId { get; init; }

    /// <summary>The mail account of the messages.</summary>
    [JsonPropertyName("messageAccountId")]
    public required AccountId MessageAccountId { get; init; }

    /// <summary>1 to <see cref="API.Limits.MaxCrmMessages"/> messages.</summary>
    [JsonPropertyName("messageIds")]
    [JsonConverter(typeof(NullAsEmptyListConverter<MessageId>))]
    public IReadOnlyList<MessageId> MessageIds { get; init => field = value ?? []; } = [];

    /// <summary>Items to link; null for none.</summary>
    [JsonPropertyName("link")]
    public IReadOnlyList<CrmItemRef>? Link { get; init; }

    /// <summary>Items to unlink (one saved message only); null for none.</summary>
    [JsonPropertyName("unlink")]
    public IReadOnlyList<CrmItemRef>? Unlink { get; init; }

    /// <summary>The new superior item; null when none is added.</summary>
    [JsonPropertyName("superior")]
    public CrmItemRef? Superior { get; init; }

    /// <summary>Visible to the user only; false is written as absent.</summary>
    [JsonPropertyName("private")]
    public bool? Private { get; init => field = value == true ? true : null; }
}

/// <summary>
/// api.CrmSaveOutcome: the result for one message: <see cref="Record"/> on
/// success (with <see cref="Created"/> when this call created the CRM
/// item), <see cref="Error"/> otherwise.
/// </summary>
public sealed record CrmSaveOutcome
{
    /// <summary>The message.</summary>
    [JsonPropertyName("messageId")]
    public required MessageId MessageId { get; init; }

    /// <summary>Its record, on success.</summary>
    [JsonPropertyName("record")]
    public CrmRecord? Record { get; init; }

    /// <summary>This call created the CRM item; false is written as absent.</summary>
    [JsonPropertyName("created")]
    public bool? Created { get; init => field = value == true ? true : null; }

    /// <summary>Why it was not saved.</summary>
    [JsonPropertyName("error")]
    public RpcError? Error { get; init; }
}

/// <summary>api.CrmSaveResult: one outcome per requested message, in order.</summary>
public sealed record CrmSaveResult
{
    /// <summary>The outcomes.</summary>
    [JsonPropertyName("results")]
    [JsonConverter(typeof(NullAsEmptyListConverter<CrmSaveOutcome>))]
    public IReadOnlyList<CrmSaveOutcome> Results { get; init => field = value ?? []; } = [];
}

/// <summary>
/// api.CrmRemoveParams: deletes a saved message's item from the CRM (for
/// every user of the CRM) and the local record. Its result is
/// <see cref="EmptyResult"/> (Go's empty CrmRemoveResult).
/// </summary>
public sealed record CrmRemoveParams
{
    /// <summary>The eway account; null for the first enabled one.</summary>
    [JsonPropertyName("accountId")]
    public AccountId? AccountId { get; init; }

    /// <summary>The mail account of the message.</summary>
    [JsonPropertyName("messageAccountId")]
    public required AccountId MessageAccountId { get; init; }

    /// <summary>The saved message.</summary>
    [JsonPropertyName("messageId")]
    public required MessageId MessageId { get; init; }
}

/// <summary>
/// How long the client waits for the CRM's calls (Swift
/// <c>RPCTimeouts.crmLookup</c>, <c>crmSave</c>; docs/eway.md §2.7 "As built
/// (T6)"). <c>account.detectEway</c> takes <see cref="Malachi.Core.Platform.RpcTimeouts.DetectSite"/>
/// (15 s, eway_flow.go <c>detectEwayTimeout</c>), <c>crm.remove</c>
/// <see cref="Malachi.Core.Platform.RpcTimeouts.Remote"/>.
/// </summary>
public static class CrmTimeouts
{
    /// <summary>
    /// <c>crm.lookup</c> and <c>crm.search</c>: the daemon gives the CRM 15 s
    /// for a lookup and may wait for its sign-in first (docs/eway.md D9).
    /// </summary>
    public static readonly TimeSpan Lookup = TimeSpan.FromSeconds(30);

    /// <summary>
    /// <c>crm.save</c>: up to 20 messages saved one after the other, each
    /// upload allowed 2 minutes (docs/eway.md D16); the daemon has no budget
    /// of its own for the whole call.
    /// </summary>
    public static readonly TimeSpan Save = TimeSpan.FromMinutes(15);

    /// <summary>
    /// <c>account.test</c> of an eWay-CRM account: one sign-in, the daemon
    /// allows itself 45 s (eway_flow.go <c>ewayTestTimeout</c>).
    /// </summary>
    public static readonly TimeSpan Test = TimeSpan.FromSeconds(50);

    /// <summary>
    /// <c>account.add</c> and <c>account.update</c> of an eWay-CRM account:
    /// the sign-in again and the keyring (eway_flow.go <c>ewaySaveTimeout</c>).
    /// </summary>
    public static readonly TimeSpan Store = TimeSpan.FromSeconds(60);
}
