// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/API/Jira.swift; Go:
// backend/pkg/api/types.go (JiraConfig, SpaceRef, StatusRef, the account
// methods' Jira types, "Issues"); contract: docs/api.md §3, §4.1
// (account.detectSite, account.listSpaces), §4.3, §4.4, §4.5 (comment
// drafts), §4.12 (issue.transitions, issue.transition), §5
// (notify.messagesChanged).
//
// Issue-tracker (Jira) accounts. Everything that came from the site
// (summaries, names, statuses, titles) is hostile input like mail: display
// it as plain text.
//
// Windows: Swift keeps MessageIssue's issue as `info` and flattens it in
// its coding, as Go embeds IssueInfo; here the issue's members are
// MessageIssue's own, as they are on the wire, and Info builds the
// IssueInfo from them. The optional lists are nullable and left out when
// null (Go's omitempty leaves out an empty one; an empty list reads the
// same to Go), and JiraConfig.Spaces is always written, as in Go.

using System;
using System.Collections.Generic;
using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

/// <summary>api.SpaceRef: a Jira space (project) the account synchronises.</summary>
public sealed record SpaceRef
{
    /// <summary>The space's id on the site.</summary>
    [JsonPropertyName("id")]
    public required string Id { get; init; }

    /// <summary>Its key ("ITSD").</summary>
    [JsonPropertyName("key")]
    public required string Key { get; init; }

    /// <summary>Display only; the daemon may refresh it.</summary>
    [JsonPropertyName("name")]
    public string? Name { get; init; }
}

/// <summary>api.StatusRef: an issue status by id, the name for display only.</summary>
public sealed record StatusRef
{
    /// <summary>The status's id on the site.</summary>
    [JsonPropertyName("id")]
    public required string Id { get; init; }

    /// <summary>Display only.</summary>
    [JsonPropertyName("name")]
    public string? Name { get; init; }
}

/// <summary>
/// api.JiraConfig: the <c>jira</c> block of an account of kind <c>jira</c>
/// (its <c>imap</c>, <c>smtp</c>, <c>graph</c> and <c>oauth2</c> are null).
/// The zero value of every member is the default. Every Go member is here,
/// so that an <c>account.update</c> built from a listed account never drops
/// one.
/// </summary>
public sealed record JiraConfig
{
    /// <summary>
    /// Normalised by the daemon: https (http only for loopback or a Data
    /// Center the user typed http for), no userinfo, query or fragment, no
    /// trailing slash; a Data Center may carry a context path.
    /// </summary>
    [JsonPropertyName("siteUrl")]
    public required string SiteUrl { get; init; }

    /// <summary>cloud or datacenter.</summary>
    [JsonPropertyName("deployment")]
    public required JiraDeployment Deployment { get; init; }

    /// <summary>Cloud only: the tenant UUID; enables the gateway route (scoped tokens).</summary>
    [JsonPropertyName("cloudId")]
    public string? CloudId { get; init; }

    /// <summary>Cloud: the Atlassian e-mail for Basic auth (required); Data Center: null.</summary>
    [JsonPropertyName("login")]
    public string? Login { get; init; }

    /// <summary>1 to <see cref="API.Limits.MaxJiraSpaces"/>; always written.</summary>
    [JsonPropertyName("spaces")]
    [JsonConverter(typeof(NullAsEmptyListConverter<SpaceRef>))]
    public IReadOnlyList<SpaceRef> Spaces { get; init => field = value ?? []; } = [];

    /// <summary>
    /// The account's own retention window: issues updated within it are
    /// kept, open issues assigned to the user whatever their age. Null or 0
    /// is <see cref="API.Limits.DefaultJiraOfflineDays"/>; at most
    /// <see cref="API.Limits.MaxJiraOfflineDays"/>.
    /// </summary>
    [JsonPropertyName("offlineDays")]
    public int? OfflineDays { get; init; }

    /// <summary>
    /// Only the issues the user reports, is assigned, watches or updated
    /// recently, instead of every issue of the spaces.
    /// </summary>
    [JsonPropertyName("onlyMine")]
    public bool? OnlyMine { get; init; }

    /// <summary>Status and assignee changes are not listed.</summary>
    [JsonPropertyName("hideEvents")]
    public bool? HideEvents { get; init; }

    /// <summary>Null or empty: all three virtual folders are shown.</summary>
    [JsonPropertyName("disabledFolders")]
    public IReadOnlyList<VirtualFolder>? DisabledFolders { get; init; }

    /// <summary>Null or empty: the statuses of category <c>done</c>.</summary>
    [JsonPropertyName("closedStatuses")]
    public IReadOnlyList<StatusRef>? ClosedStatuses { get; init; }

    /// <summary>Null is <see cref="NotificationMailMode.Sync"/>.</summary>
    [JsonPropertyName("notificationMail")]
    public NotificationMailMode? NotificationMail { get; init; }

    /// <summary>
    /// "addr@host" or "@host"; null or empty: "@&lt;site host&gt;" on cloud,
    /// none on Data Center.
    /// </summary>
    [JsonPropertyName("notificationSenders")]
    public IReadOnlyList<string>? NotificationSenders { get; init; }

    /// <summary>
    /// Display names of bots whose comments relay someone else's, which
    /// the daemon re-attributes to the author they name.
    /// </summary>
    [JsonPropertyName("botNames")]
    public IReadOnlyList<string>? BotNames { get; init; }

    /// <summary>
    /// RE2 patterns, each matched against a whole trimmed line of a relayed
    /// comment; matching lines are removed.
    /// </summary>
    [JsonPropertyName("metadataFilters")]
    public IReadOnlyList<string>? MetadataFilters { get; init; }

    /// <summary>Line prefixes that introduce the original author in a relayed comment.</summary>
    [JsonPropertyName("authorPrefixes")]
    public IReadOnlyList<string>? AuthorPrefixes { get; init; }
}

/// <summary>
/// api.IssueInfo: the issue a message or a thread of a jira account belongs
/// to. Every string but <see cref="Key"/> and <see cref="Url"/> is untrusted
/// display text from the site.
/// </summary>
public sealed record IssueInfo
{
    /// <summary>"ITSD-42".</summary>
    [JsonPropertyName("key")]
    public required string Key { get; init; }

    /// <summary>&lt;siteUrl&gt;/browse/&lt;key&gt;, http(s) only; still checked before opening.</summary>
    [JsonPropertyName("url")]
    public required string Url { get; init; }

    /// <summary>The summary.</summary>
    [JsonPropertyName("summary")]
    public required string Summary { get; init; }

    /// <summary>The status's name.</summary>
    [JsonPropertyName("status")]
    public required string Status { get; init; }

    /// <summary>Null (or empty) when the daemon does not know it.</summary>
    [JsonPropertyName("statusCategory")]
    public IssueStatusCategory? StatusCategory { get; init; }

    /// <summary>The issue type.</summary>
    [JsonPropertyName("type")]
    public string? Type { get; init; }

    /// <summary>The priority.</summary>
    [JsonPropertyName("priority")]
    public string? Priority { get; init; }

    /// <summary>Display name; null = unassigned.</summary>
    [JsonPropertyName("assignee")]
    public string? Assignee { get; init; }

    /// <summary>Display name.</summary>
    [JsonPropertyName("reporter")]
    public string? Reporter { get; init; }

    /// <summary>Assigned to the account's user.</summary>
    [JsonPropertyName("assignedToMe")]
    public bool? AssignedToMe { get; init; }

    /// <summary>Watched by the account's user.</summary>
    [JsonPropertyName("watching")]
    public bool? Watching { get; init; }

    /// <summary><c>[public, internal]</c> on a service-desk request, else null or empty.</summary>
    [JsonPropertyName("commentVisibilities")]
    public IReadOnlyList<CommentVisibility>? CommentVisibilities { get; init; }
}

/// <summary>api.IssueChange: one field an event row changed; an empty side is null.</summary>
public sealed record IssueChange
{
    /// <summary>status or assignee.</summary>
    [JsonPropertyName("field")]
    public required IssueField Field { get; init; }

    /// <summary>The value before; null for none.</summary>
    [JsonPropertyName("from")]
    public string? From { get; init; }

    /// <summary>The value after; null for none (unassigned).</summary>
    [JsonPropertyName("to")]
    public string? To { get; init; }
}

/// <summary>
/// api.MessageIssue: <see cref="MessageSummary.Issue"/>, the issue (Go
/// embeds <see cref="IssueInfo"/>: its members are this record's own, as on
/// the wire) and what the message is of it.
/// </summary>
public sealed record MessageIssue
{
    /// <summary>IssueInfo.Key.</summary>
    [JsonPropertyName("key")]
    public required string Key { get; init; }

    /// <summary>IssueInfo.Url.</summary>
    [JsonPropertyName("url")]
    public required string Url { get; init; }

    /// <summary>IssueInfo.Summary.</summary>
    [JsonPropertyName("summary")]
    public required string Summary { get; init; }

    /// <summary>IssueInfo.Status.</summary>
    [JsonPropertyName("status")]
    public required string Status { get; init; }

    /// <summary>IssueInfo.StatusCategory.</summary>
    [JsonPropertyName("statusCategory")]
    public IssueStatusCategory? StatusCategory { get; init; }

    /// <summary>IssueInfo.Type.</summary>
    [JsonPropertyName("type")]
    public string? Type { get; init; }

    /// <summary>IssueInfo.Priority.</summary>
    [JsonPropertyName("priority")]
    public string? Priority { get; init; }

    /// <summary>IssueInfo.Assignee.</summary>
    [JsonPropertyName("assignee")]
    public string? Assignee { get; init; }

    /// <summary>IssueInfo.Reporter.</summary>
    [JsonPropertyName("reporter")]
    public string? Reporter { get; init; }

    /// <summary>IssueInfo.AssignedToMe.</summary>
    [JsonPropertyName("assignedToMe")]
    public bool? AssignedToMe { get; init; }

    /// <summary>IssueInfo.Watching.</summary>
    [JsonPropertyName("watching")]
    public bool? Watching { get; init; }

    /// <summary>IssueInfo.CommentVisibilities.</summary>
    [JsonPropertyName("commentVisibilities")]
    public IReadOnlyList<CommentVisibility>? CommentVisibilities { get; init; }

    /// <summary>description, comment or event.</summary>
    [JsonPropertyName("item")]
    public required IssueItemKind Item { get; init; }

    /// <summary>A comment's visibility on a service-desk request; null = public.</summary>
    [JsonPropertyName("visibility")]
    public CommentVisibility? Visibility { get; init; }

    /// <summary>
    /// Item <c>event</c> only: what changed. The UI builds the sentence from
    /// these; the body and snippet are language-neutral values.
    /// </summary>
    [JsonPropertyName("changes")]
    public IReadOnlyList<IssueChange>? Changes { get; init; }

    /// <summary>The bot that relayed a re-attributed comment (display name).</summary>
    [JsonPropertyName("via")]
    public string? Via { get; init; }

    /// <summary>The comment was edited after it was posted.</summary>
    [JsonPropertyName("edited")]
    public bool? Edited { get; init; }

    /// <summary>
    /// The account's own user wrote the item on the site (never set with
    /// <see cref="Via"/>: a relayed comment is someone else's).
    /// </summary>
    [JsonPropertyName("mine")]
    public bool? Mine { get; init; }

    /// <summary>The issue (Swift <c>info</c>).</summary>
    [JsonIgnore]
    public IssueInfo Info => new()
    {
        Key = Key,
        Url = Url,
        Summary = Summary,
        Status = Status,
        StatusCategory = StatusCategory,
        Type = Type,
        Priority = Priority,
        Assignee = Assignee,
        Reporter = Reporter,
        AssignedToMe = AssignedToMe,
        Watching = Watching,
        CommentVisibilities = CommentVisibilities,
    };

    /// <summary>
    /// The projection of <paramref name="info"/> as the item
    /// <paramref name="item"/> (Swift <c>MessageIssue(info:item:)</c>); the
    /// item's own members are set with <c>with</c>.
    /// </summary>
    public static MessageIssue Of(IssueInfo info, IssueItemKind item)
    {
        ArgumentNullException.ThrowIfNull(info);
        return new()
        {
            Key = info.Key,
            Url = info.Url,
            Summary = info.Summary,
            Status = info.Status,
            StatusCategory = info.StatusCategory,
            Type = info.Type,
            Priority = info.Priority,
            Assignee = info.Assignee,
            Reporter = info.Reporter,
            AssignedToMe = info.AssignedToMe,
            Watching = info.Watching,
            CommentVisibilities = info.CommentVisibilities,
            Item = item,
        };
    }
}

/// <summary>
/// api.AccountDetectSiteParams: "acme.atlassian.net" or a full URL; https
/// is assumed.
/// </summary>
public sealed record AccountDetectSiteParams
{
    /// <summary>What the user typed.</summary>
    [JsonPropertyName("url")]
    public required string Url { get; init; }
}

/// <summary>
/// api.AccountDetectSiteResult: what the address turned out to be. Nothing
/// is stored or authenticated.
/// </summary>
public sealed record AccountDetectSiteResult
{
    /// <summary><c>jira</c>.</summary>
    [JsonPropertyName("kind")]
    public required AccountKind Kind { get; init; }

    /// <summary>Normalised; goes into <see cref="JiraConfig.SiteUrl"/> as it is.</summary>
    [JsonPropertyName("siteUrl")]
    public required string SiteUrl { get; init; }

    /// <summary>cloud or datacenter.</summary>
    [JsonPropertyName("deployment")]
    public required JiraDeployment Deployment { get; init; }

    /// <summary>Cloud only.</summary>
    [JsonPropertyName("cloudId")]
    public string? CloudId { get; init; }

    /// <summary>Untrusted display text.</summary>
    [JsonPropertyName("title")]
    public string? Title { get; init; }

    /// <summary>Untrusted display text.</summary>
    [JsonPropertyName("version")]
    public string? Version { get; init; }
}

/// <summary>
/// api.AccountListSpacesParams: <see cref="Config"/> of kind jira with its
/// connection members (spaces may be empty). With <see cref="AccountId"/>
/// and no password the stored token of that account is used.
/// <see cref="Counts"/> estimates the issues updated within
/// <c>config.jira.offlineDays</c>.
/// </summary>
public sealed record AccountListSpacesParams
{
    /// <summary>The account being edited, if any.</summary>
    [JsonPropertyName("accountId")]
    public AccountId? AccountId { get; init; }

    /// <summary>The account's configuration.</summary>
    [JsonPropertyName("config")]
    public required AccountConfig Config { get; init; }

    /// <summary>The token as the password; <c>{}</c> for the stored one.</summary>
    [JsonPropertyName("credentials")]
    [JsonRequired]
    public Credentials Credentials { get; init; } = new();

    /// <summary>Whether to estimate each space's issues.</summary>
    [JsonPropertyName("counts")]
    public bool? Counts { get; init; }
}

/// <summary>api.Space: a space (project) the token can see.</summary>
public sealed record Space
{
    /// <summary>The space's id on the site.</summary>
    [JsonPropertyName("id")]
    public required string Id { get; init; }

    /// <summary>Untrusted display text.</summary>
    [JsonPropertyName("key")]
    public required string Key { get; init; }

    /// <summary>Untrusted display text.</summary>
    [JsonPropertyName("name")]
    public required string Name { get; init; }

    /// <summary>A Jira Service Management project (internal comments exist).</summary>
    [JsonPropertyName("serviceDesk")]
    public bool? ServiceDesk { get; init; }

    /// <summary>Estimated issues in the window; -1 = not counted.</summary>
    [JsonPropertyName("issues")]
    [JsonRequired]
    public int Issues { get; init; } = -1;
}

/// <summary>api.IssueStatus: a status of the site, for <see cref="JiraConfig.ClosedStatuses"/>.</summary>
public sealed record IssueStatus
{
    /// <summary>The status's id on the site.</summary>
    [JsonPropertyName("id")]
    public required string Id { get; init; }

    /// <summary>Untrusted display text.</summary>
    [JsonPropertyName("name")]
    public required string Name { get; init; }

    /// <summary>Its category.</summary>
    [JsonPropertyName("category")]
    public required IssueStatusCategory Category { get; init; }
}

/// <summary>
/// api.SiteUser: the user the token belongs to. <see cref="Email"/> may be
/// hidden by the site (Data Center, privacy settings).
/// </summary>
public sealed record SiteUser
{
    /// <summary>Untrusted display text.</summary>
    [JsonPropertyName("name")]
    public required string Name { get; init; }

    /// <summary>Null when the site does not reveal it.</summary>
    [JsonPropertyName("email")]
    public string? Email { get; init; }
}

/// <summary>api.AccountListSpacesResult: <see cref="Spaces"/> in name order, at most 1000.</summary>
public sealed record AccountListSpacesResult
{
    /// <summary>The user the token signs in as.</summary>
    [JsonPropertyName("user")]
    public required SiteUser User { get; init; }

    /// <summary>The spaces.</summary>
    [JsonPropertyName("spaces")]
    [JsonConverter(typeof(NullAsEmptyListConverter<Space>))]
    public IReadOnlyList<Space> Spaces { get; init => field = value ?? []; } = [];

    /// <summary>The site's statuses.</summary>
    [JsonPropertyName("statuses")]
    [JsonConverter(typeof(NullAsEmptyListConverter<IssueStatus>))]
    public IReadOnlyList<IssueStatus> Statuses { get; init => field = value ?? []; } = [];
}

/// <summary>
/// api.DraftComment: <see cref="Draft.Comment"/> of a comment draft on an
/// issue. In <c>draft.save</c> only <see cref="Visibility"/> is read.
/// </summary>
public sealed record DraftComment
{
    /// <summary>The issue the comment goes to.</summary>
    [JsonPropertyName("issue")]
    public required IssueInfo Issue { get; init; }

    /// <summary>
    /// Empty = public; <c>internal</c> only when
    /// <see cref="IssueInfo.CommentVisibilities"/> allows it. Always written.
    /// </summary>
    [JsonPropertyName("visibility")]
    [JsonRequired]
    public CommentVisibility Visibility { get; init; } = "";
}

/// <summary>
/// api.IssueTransitionsParams (<c>issue.transitions</c>): the issue is named
/// by any message of it (the message's thread is the issue).
/// </summary>
public sealed record IssueTransitionsParams
{
    /// <summary>The jira account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>Any message of the issue.</summary>
    [JsonPropertyName("messageId")]
    public required MessageId MessageId { get; init; }
}

/// <summary>
/// api.IssueTransition: one status change the site offers the user on the
/// issue. <see cref="Name"/> and <see cref="To"/> are untrusted display
/// text from the site.
/// </summary>
public sealed record IssueTransition
{
    /// <summary>The transition's id.</summary>
    [JsonPropertyName("id")]
    public required string Id { get; init; }

    /// <summary>The transition's name, as the site's own status menu shows it.</summary>
    [JsonPropertyName("name")]
    public required string Name { get; init; }

    /// <summary>The name of the status it leads to.</summary>
    [JsonPropertyName("to")]
    public required string To { get; init; }

    /// <summary>The category of that status; null when the site does not say.</summary>
    [JsonPropertyName("toCategory")]
    public IssueStatusCategory? ToCategory { get; init; }

    /// <summary>
    /// The transition opens a screen on the site or has fields that must be
    /// filled: it cannot be performed here (<c>issue.transition</c> refuses
    /// it with invalidArgument); a client lists it disabled.
    /// </summary>
    [JsonPropertyName("needsInput")]
    public bool? NeedsInput { get; init; }
}

/// <summary>
/// api.IssueTransitionsResult: <see cref="Issue"/> as the daemon last
/// synchronised it, <see cref="Transitions"/> in the site's order, at most
/// <see cref="API.Limits.MaxIssueTransitions"/>.
/// </summary>
public sealed record IssueTransitionsResult
{
    /// <summary>The issue.</summary>
    [JsonPropertyName("issue")]
    public required IssueInfo Issue { get; init; }

    /// <summary>The transitions.</summary>
    [JsonPropertyName("transitions")]
    [JsonConverter(typeof(NullAsEmptyListConverter<IssueTransition>))]
    public IReadOnlyList<IssueTransition> Transitions { get; init => field = value ?? []; } = [];
}

/// <summary>
/// api.IssueTransitionParams (<c>issue.transition</c>):
/// <see cref="TransitionId"/> is the id of a transition without
/// <see cref="IssueTransition.NeedsInput"/>.
/// </summary>
public sealed record IssueTransitionParams
{
    /// <summary>The jira account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>Any message of the issue.</summary>
    [JsonPropertyName("messageId")]
    public required MessageId MessageId { get; init; }

    /// <summary>The transition to perform.</summary>
    [JsonPropertyName("transitionId")]
    public required string TransitionId { get; init; }
}

/// <summary>
/// api.IssueTransitionResult: the issue after the daemon refreshed it from
/// the site, or as last synchronised when the refresh did not finish in
/// time (the transition was performed all the same).
/// </summary>
public sealed record IssueTransitionResult
{
    /// <summary>The issue.</summary>
    [JsonPropertyName("issue")]
    public required IssueInfo Issue { get; init; }
}

/// <summary>
/// api.MessagesChangedNotification (<c>notify.messagesChanged</c>): messages
/// of the account's folders changed without arriving or being deleted:
/// hidden or shown again (a Jira notification mail hidden in a mail
/// account), or rebuilt in place under their ids (a Jira account's items
/// rendered with other settings, a comment edited or re-attributed, an
/// issue renamed). Clients showing those folders drop what they cached of
/// their messages and list them again. <see cref="FolderIds"/> empty = any
/// folder of the account.
/// </summary>
public sealed record MessagesChangedNotification
{
    /// <summary>The account (a Jira account, or a mail account whose notification mails were hidden).</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The folders; empty = any folder of the account.</summary>
    [JsonPropertyName("folderIds")]
    [JsonConverter(typeof(NullAsEmptyListConverter<FolderId>))]
    public IReadOnlyList<FolderId> FolderIds { get; init => field = value ?? []; } = [];
}
