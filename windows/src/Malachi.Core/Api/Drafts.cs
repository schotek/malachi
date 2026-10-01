// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/API/Drafts.swift; Go:
// backend/pkg/api/types.go ("Drafts and sending"); contract: docs/api.md
// §4.3 message.send/outbox.retry, §4.5.
//
// The daemon owns every derived field: it sanitises HtmlBody on the way in,
// derives TextBody, assigns attachment metadata and sets UpdatedAt.

using System;
using System.Collections.Generic;
using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

/// <summary>
/// api.DraftAttachment: a file in the daemon's attachment store, created by
/// <c>attachment.import</c>. In <c>draft.save</c> params only
/// <see cref="Id"/> is read.
/// </summary>
public sealed record DraftAttachment
{
    /// <summary>The attachment in the store.</summary>
    [JsonPropertyName("id")]
    public required string Id { get; init; }

    /// <summary>Sanitised; never the raw client name.</summary>
    [JsonPropertyName("filename")]
    public required string Filename { get; init; }

    /// <summary>Detected from content, not taken from the client.</summary>
    [JsonPropertyName("contentType")]
    public required string ContentType { get; init; }

    /// <summary>In bytes.</summary>
    [JsonPropertyName("size")]
    public required long Size { get; init; }

    /// <summary>An image referenced from <c>htmlBody</c> as <c>cid:&lt;contentId&gt;</c>.</summary>
    [JsonPropertyName("inline")]
    public required bool Inline { get; init; }

    /// <summary>Daemon-assigned, without angle brackets.</summary>
    [JsonPropertyName("contentId")]
    public string? ContentId { get; init; }
}

/// <summary>
/// api.Draft: a message being composed. <see cref="Id"/> null on the first
/// save; <see cref="Version"/> is optimistic concurrency (<c>conflict</c> when
/// it differs from the stored one). <see cref="HtmlBody"/> in params is the
/// editor's HTML, treated as hostile and sanitised in compose mode;
/// <see cref="TextBody"/> is then derived. <see cref="InReplyTo"/> and
/// <see cref="Forwarding"/> are mutually exclusive. <see cref="Replaces"/> is
/// the Drafts message <c>draft.open</c> built the draft from;
/// <c>draft.save</c> takes it over (clients send it back unchanged).
/// </summary>
public sealed record Draft
{
    /// <summary>Null until the first save.</summary>
    [JsonPropertyName("id")]
    public DraftId? Id { get; init; }

    /// <summary>The sending account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The stored version this edit is based on.</summary>
    [JsonPropertyName("version")]
    [JsonRequired]
    public int Version { get; init; }

    /// <summary>The recipients.</summary>
    [JsonPropertyName("to")]
    [JsonConverter(typeof(NullAsEmptyListConverter<Address>))]
    public IReadOnlyList<Address> To { get; init => field = value ?? []; } = [];

    /// <summary>The Cc recipients.</summary>
    [JsonPropertyName("cc")]
    public IReadOnlyList<Address>? Cc { get; init; }

    /// <summary>The Bcc recipients.</summary>
    [JsonPropertyName("bcc")]
    public IReadOnlyList<Address>? Bcc { get; init; }

    /// <summary>The subject.</summary>
    [JsonPropertyName("subject")]
    [JsonRequired]
    public string Subject { get; init; } = "";

    /// <summary>The body as typed, or the text the daemon derived from the HTML.</summary>
    [JsonPropertyName("textBody")]
    [JsonRequired]
    public string TextBody { get; init; } = "";

    /// <summary>Null or empty = plain text.</summary>
    [JsonPropertyName("htmlBody")]
    public string? HtmlBody { get; init; }

    /// <summary>The message this answers (a local id).</summary>
    [JsonPropertyName("inReplyTo")]
    public MessageId? InReplyTo { get; init; }

    /// <summary>The message this forwards.</summary>
    [JsonPropertyName("forwarding")]
    public MessageId? Forwarding { get; init; }

    /// <summary>The attachments, by id in params.</summary>
    [JsonPropertyName("attachments")]
    public IReadOnlyList<DraftAttachment>? Attachments { get; init; }

    /// <summary>The Drafts message this draft takes over (<c>draft.open</c> → <c>draft.save</c> only).</summary>
    [JsonPropertyName("replaces")]
    public MessageId? Replaces { get; init; }

    /// <summary>
    /// Set on a comment draft of a jira account (<c>draft.create</c> reply);
    /// <c>draft.save</c> reads only its visibility. Sent back unchanged.
    /// </summary>
    [JsonPropertyName("comment")]
    public DraftComment? Comment { get; init; }

    /// <summary>
    /// Daemon-set; ignored in params (<c>DateTimeOffset.GoZero</c> in a
    /// <c>draft.create</c> result, and encoded so).
    /// </summary>
    [JsonPropertyName("updatedAt")]
    [JsonRequired]
    public DateTimeOffset UpdatedAt { get; init; } = DateTimeOffset.GoZero;
}

/// <summary>api.DraftSaveParams.</summary>
public sealed record DraftSaveParams
{
    /// <summary>The draft to store.</summary>
    [JsonPropertyName("draft")]
    public required Draft Draft { get; init; }
}

/// <summary>api.DraftSaveResult: what the daemon stored, which is what will be sent.</summary>
public sealed record DraftSaveResult
{
    /// <summary>The stored draft.</summary>
    [JsonPropertyName("draftId")]
    public required DraftId DraftId { get; init; }

    /// <summary>Its new version.</summary>
    [JsonPropertyName("version")]
    public required int Version { get; init; }

    /// <summary>The stored text body.</summary>
    [JsonPropertyName("textBody")]
    public required string TextBody { get; init; }

    /// <summary>The stored (sanitised) HTML body.</summary>
    [JsonPropertyName("htmlBody")]
    public string? HtmlBody { get; init; }

    /// <summary>What the sanitiser removed.</summary>
    [JsonPropertyName("blocked")]
    [JsonRequired]
    public BlockedContent Blocked { get; init; } = new();

    /// <summary>The attachments after reconciliation.</summary>
    [JsonPropertyName("attachments")]
    public IReadOnlyList<DraftAttachment>? Attachments { get; init; }
}

/// <summary>api.DraftListParams.</summary>
public sealed record DraftListParams
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>Which page.</summary>
    [JsonPropertyName("page")]
    [JsonRequired]
    public Page Page { get; init; } = new();
}

/// <summary>api.DraftListResult: newest <c>updatedAt</c> first, full bodies.</summary>
public sealed record DraftListResult
{
    /// <summary>The drafts of the page.</summary>
    [JsonPropertyName("drafts")]
    [JsonConverter(typeof(NullAsEmptyListConverter<Draft>))]
    public IReadOnlyList<Draft> Drafts { get; init => field = value ?? []; } = [];

    /// <summary>The next cursor and the total.</summary>
    [JsonPropertyName("page")]
    public required PageInfo Page { get; init; }
}

/// <summary>api.DraftDeleteParams.</summary>
public sealed record DraftDeleteParams
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The draft.</summary>
    [JsonPropertyName("draftId")]
    public required DraftId DraftId { get; init; }
}

/// <summary>
/// api.DraftCreateParams: an unsaved template computed by the daemon
/// (recipients, Re:/Fwd:, the quoted sanitised body, imported pictures, or a
/// parsed mailto:). <see cref="Attribution"/> is the client's line above the
/// quote in the user's language: plain text, LF-separated, at most
/// <see cref="API.Limits.MaxDraftAttributionBytes"/> and
/// <see cref="API.Limits.MaxDraftAttributionLines"/>.
/// </summary>
public sealed record DraftCreateParams
{
    /// <summary>The sending account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>new, reply, replyAll or forward.</summary>
    [JsonPropertyName("mode")]
    public required ComposeMode Mode { get; init; }

    /// <summary>Required unless <see cref="Mode"/> is <c>new</c>.</summary>
    [JsonPropertyName("messageId")]
    public MessageId? MessageId { get; init; }

    /// <summary><c>new</c> only.</summary>
    [JsonPropertyName("mailto")]
    public string? Mailto { get; init; }

    /// <summary>Ignored for <c>new</c>.</summary>
    [JsonPropertyName("attribution")]
    public string? Attribution { get; init; }

    /// <summary>
    /// <c>forward</c> only: the account of <see cref="MessageId"/> when it is
    /// not <see cref="AccountId"/> (a jira message forwarded from a mail
    /// account).
    /// </summary>
    [JsonPropertyName("messageAccountId")]
    public AccountId? MessageAccountId { get; init; }
}

/// <summary>
/// api.DraftCreateResult: <see cref="Draft"/> has no id and version 0;
/// nothing is persisted until the first <c>draft.save</c>.
/// <c>Draft.Attachments</c> lists what was imported for it (unbound until
/// then).
/// </summary>
public sealed record DraftCreateResult
{
    /// <summary>The template.</summary>
    [JsonPropertyName("draft")]
    public required Draft Draft { get; init; }

    /// <summary>How much of the original is quoted.</summary>
    [JsonPropertyName("quoted")]
    public required QuoteForm Quoted { get; init; }

    /// <summary>What the sanitiser removed from the quoted original; empty unless <see cref="Quoted"/> is <c>html</c>.</summary>
    [JsonPropertyName("blocked")]
    [JsonRequired]
    public BlockedContent Blocked { get; init; } = new();

    /// <summary>Parts of the original that were not imported (over a cap, unreadable, …).</summary>
    [JsonPropertyName("skipped")]
    public IReadOnlyList<Attachment>? Skipped { get; init; }
}

/// <summary>api.DraftOpenParams: a message of the account's Drafts folder to edit.</summary>
public sealed record DraftOpenParams
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The message of the Drafts folder.</summary>
    [JsonPropertyName("messageId")]
    public required MessageId MessageId { get; init; }
}

/// <summary>
/// api.DraftOpenResult: the saved draft the message is the copy of
/// (<c>Id</c> and <c>Version</c> set), or a draft built from the message —
/// unsaved, its attachments imported but unbound, <c>Replaces</c> set when
/// nothing was lost; a newer copy of a saved draft carries that draft's
/// <c>Id</c> and <c>Version</c>. Nothing is persisted by <c>draft.open</c>.
/// </summary>
public sealed record DraftOpenResult
{
    /// <summary>The draft to edit.</summary>
    [JsonPropertyName("draft")]
    public required Draft Draft { get; init; }

    /// <summary>What the sanitiser removed from the message's HTML.</summary>
    [JsonPropertyName("blocked")]
    [JsonRequired]
    public BlockedContent Blocked { get; init; } = new();

    /// <summary>Parts of the message that were not imported.</summary>
    [JsonPropertyName("skipped")]
    public IReadOnlyList<Attachment>? Skipped { get; init; }
}

/// <summary>
/// api.DraftMarkdownParams: text pasted into the compose editor
/// (<c>draft.markdown</c>): plain text, at most
/// <see cref="API.Limits.MaxDraftBodyBytes"/>, valid UTF-8.
/// </summary>
public sealed record DraftMarkdownParams
{
    /// <summary>The pasted text.</summary>
    [JsonPropertyName("text")]
    public required string Text { get; init; }
}

/// <summary>
/// api.DraftMarkdownResult: whether the text reads as Markdown and, when it
/// does, the text rendered as compose-mode sanitised HTML for the editor to
/// insert. <see cref="Markdown"/> false: the editor pastes the text as it is.
/// </summary>
public sealed record DraftMarkdownResult
{
    /// <summary>The text reads as Markdown.</summary>
    [JsonPropertyName("markdown")]
    public required bool Markdown { get; init; }

    /// <summary>The rendering, sanitised by the daemon; absent when <see cref="Markdown"/> is false.</summary>
    [JsonPropertyName("html")]
    public string? Html { get; init; }
}

/// <summary>
/// api.MessageSendParams: queues a saved draft. The result only confirms
/// enqueueing; the queued message lives in the outbox folder.
/// </summary>
public sealed record MessageSendParams
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The saved draft.</summary>
    [JsonPropertyName("draftId")]
    public required DraftId DraftId { get; init; }

    /// <summary>Its stored version.</summary>
    [JsonPropertyName("version")]
    public required int Version { get; init; }
}

/// <summary>api.MessageSendResult: the id of the queued message.</summary>
public sealed record MessageSendResult
{
    /// <summary>The message in the outbox folder.</summary>
    [JsonPropertyName("outboxId")]
    public required MessageId OutboxId { get; init; }
}

/// <summary>
/// api.OutboxRetryParams: re-queues a queued or failed outbox message for an
/// immediate attempt.
/// </summary>
public sealed record OutboxRetryParams
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The outbox message.</summary>
    [JsonPropertyName("messageId")]
    public required MessageId MessageId { get; init; }
}
