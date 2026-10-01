// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/API/Messages.swift; Go:
// backend/pkg/api/types.go ("Messages"); contract: docs/api.md §3, §4.3.
//
// Everything here that came from a message is hostile input: display it as
// plain text.

using System;
using System.Collections.Generic;
using System.Text.Json;
using System.Text.Json.Serialization;
using System.Text.Json.Serialization.Metadata;

namespace Malachi.Core.Api;

/// <summary>
/// api.Address: a parsed RFC 5322 mailbox. Both fields are
/// attacker-controlled display data; never interpret them as markup.
/// </summary>
public sealed record Address
{
    /// <summary>The display name, when there is one.</summary>
    [JsonPropertyName("name")]
    public string? Name { get; init; }

    /// <summary>
    /// The address (Swift <c>address</c>, the wire's <c>"address"</c>): C#
    /// allows no member named like its type.
    /// </summary>
    [JsonPropertyName("address")]
    public required string Email { get; init; }
}

/// <summary>api.OutboxInfo: the delivery state of a message in the outbox folder.</summary>
public sealed record OutboxInfo
{
    /// <summary>queued, sending, sent or failed.</summary>
    [JsonPropertyName("state")]
    public required OutboxState State { get; init; }

    /// <summary>Delivery attempts so far.</summary>
    [JsonPropertyName("attempts")]
    public required int Attempts { get; init; }

    /// <summary>Set while queued after a transient failure.</summary>
    [JsonPropertyName("nextAttemptAt")]
    public DateTimeOffset? NextAttemptAt { get; init; }

    /// <summary>The last failure; absent before the first attempt and after a success.</summary>
    [JsonPropertyName("error")]
    public RpcError? Error { get; init; }
}

/// <summary>
/// api.MessageSummary: the list-view projection of a message. Never contains
/// body content beyond <see cref="Snippet"/>, plain text derived by the
/// daemon.
/// </summary>
public sealed record MessageSummary
{
    /// <summary>The message.</summary>
    [JsonPropertyName("id")]
    public required MessageId Id { get; init; }

    /// <summary>Its account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>Its folder.</summary>
    [JsonPropertyName("folderId")]
    public required FolderId FolderId { get; init; }

    /// <summary>
    /// The conversation; null only for a message an older daemon stored that
    /// has not been linked yet.
    /// </summary>
    [JsonPropertyName("threadId")]
    public ThreadId? ThreadId { get; init; }

    /// <summary>The senders.</summary>
    [JsonPropertyName("from")]
    [JsonConverter(typeof(NullAsEmptyListConverter<Address>))]
    public IReadOnlyList<Address> From { get; init => field = value ?? []; } = [];

    /// <summary>The recipients; absent when there are none.</summary>
    [JsonPropertyName("to")]
    public IReadOnlyList<Address>? To { get; init; }

    /// <summary>The subject.</summary>
    [JsonPropertyName("subject")]
    public required string Subject { get; init; }

    /// <summary>Best-effort when the header is garbage.</summary>
    [JsonPropertyName("date")]
    public required DateTimeOffset Date { get; init; }

    /// <summary>Plain text, at most about 200 characters.</summary>
    [JsonPropertyName("snippet")]
    public required string Snippet { get; init; }

    /// <summary>The flags.</summary>
    [JsonPropertyName("flags")]
    [JsonConverter(typeof(NullAsEmptyListConverter<Flag>))]
    public IReadOnlyList<Flag> Flags { get; init => field = value ?? []; } = [];

    /// <summary>Whether it carries attachments.</summary>
    [JsonPropertyName("hasAttachments")]
    public required bool HasAttachments { get; init; }

    /// <summary>In bytes.</summary>
    [JsonPropertyName("size")]
    public required long Size { get; init; }

    /// <summary>Present only for a message in the account's outbox folder.</summary>
    [JsonPropertyName("outbox")]
    public OutboxInfo? Outbox { get; init; }

    /// <summary>
    /// Present only for a message of a jira account: the issue and what the
    /// message is of it.
    /// </summary>
    [JsonPropertyName("issue")]
    public MessageIssue? Issue { get; init; }

    /// <summary>
    /// Present only for a message the daemon classified as bulk mail from its
    /// headers (newsletter, mailing list, automated); absent for personal
    /// mail, issue-tracker items and rows not classified yet.
    /// </summary>
    [JsonPropertyName("bulk")]
    public BulkInfo? Bulk { get; init; }
}

/// <summary>
/// api.BulkInfo: classifies a bulk message. Every string is cleaned by the
/// daemon (no control or bidi characters) but still comes from the mail:
/// display it as plain text.
/// </summary>
public sealed record BulkInfo
{
    /// <summary>newsletter, list or automated.</summary>
    [JsonPropertyName("kind")]
    public required BulkKind Kind { get; init; }

    /// <summary>
    /// The List-Id identifier, lower case, without the angle brackets and the
    /// phrase; set for a list and, when the header exists, for a newsletter.
    /// </summary>
    [JsonPropertyName("listId")]
    public string? ListId { get; init; }

    /// <summary>The lower-case domain of the From address, for display.</summary>
    [JsonPropertyName("domain")]
    public string? Domain { get; init; }
}

/// <summary>
/// api.UnsubscribeOffer: the best unsubscribe method of a message. Present
/// only when the List-Unsubscribe header offers a method the daemon can use,
/// and never for a message in the junk folder.
/// </summary>
public sealed record UnsubscribeOffer
{
    /// <summary>oneClick, mailto or url.</summary>
    [JsonPropertyName("method")]
    public required UnsubscribeMethod Method { get; init; }

    /// <summary>What the confirmation shows: the host of the URL, or the mailto address.</summary>
    [JsonPropertyName("target")]
    public required string Target { get; init; }

    /// <summary>The https page to open; only for the url method.</summary>
    [JsonPropertyName("url")]
    public string? Url { get; init; }

    /// <summary>
    /// Set when the user already unsubscribed from this list or sender
    /// through <c>message.unsubscribe</c> (oneClick or mailto).
    /// </summary>
    [JsonPropertyName("unsubscribedAt")]
    public DateTimeOffset? UnsubscribedAt { get; init; }
}

/// <summary>
/// api.Attachment: a MIME part of a message; metadata only, the bytes come
/// through <c>message.part</c>. <see cref="Filename"/> is sanitised by the
/// daemon.
/// </summary>
public sealed record Attachment
{
    /// <summary>The part number (<c>2.1</c>); empty for a part of an attached message.</summary>
    [JsonPropertyName("partId")]
    public required string PartId { get; init; }

    /// <summary>Sanitised: no path separators, no control characters.</summary>
    [JsonPropertyName("filename")]
    public required string Filename { get; init; }

    /// <summary>The MIME type.</summary>
    [JsonPropertyName("contentType")]
    public required string ContentType { get; init; }

    /// <summary>In bytes.</summary>
    [JsonPropertyName("size")]
    public required long Size { get; init; }

    /// <summary>Referenced from the HTML body via cid:.</summary>
    [JsonPropertyName("inline")]
    public required bool Inline { get; init; }

    /// <summary>The Content-ID, when there is one.</summary>
    [JsonPropertyName("contentId")]
    public string? ContentId { get; init; }

    /// <summary>
    /// The part's data is not stored on this device, only on the mail server
    /// (<c>Preferences.attachmentOfflineDays</c>,
    /// <c>Preferences.neverStoreAttachments</c>); <c>message.download</c>
    /// fetches it. Set only once the body is fetched; name, type and size are
    /// those of the original part. Absent means false
    /// (<see cref="IsRemote"/>). Under <c>neverStoreAttachments</c> it stays
    /// set after the download, which the daemon holds in memory only.
    /// </summary>
    [JsonPropertyName("remote")]
    public bool? Remote { get; init; }

    /// <summary><see cref="Remote"/> as Go reads it: absent is false.</summary>
    [JsonIgnore]
    public bool IsRemote => Remote == true;
}

/// <summary>
/// api.Message: the full header view (<c>message.get</c>). Go embeds
/// <c>MessageSummary</c>, so on the wire its fields sit beside these; here
/// they are <see cref="Summary"/>, and <see cref="MessageConverter"/>
/// flattens.
/// </summary>
[JsonConverter(typeof(MessageConverter))]
public sealed record Message
{
    /// <summary>The list-view fields.</summary>
    public required MessageSummary Summary { get; init; }

    /// <summary>The Cc recipients.</summary>
    public IReadOnlyList<Address>? Cc { get; init; }

    /// <summary>The Bcc recipients (a message of one's own).</summary>
    public IReadOnlyList<Address>? Bcc { get; init; }

    /// <summary>The Reply-To addresses.</summary>
    public IReadOnlyList<Address>? ReplyTo { get; init; }

    /// <summary>The Message-ID header, display only.</summary>
    public string? RfcMessageId { get; init; }

    /// <summary>The In-Reply-To header.</summary>
    public string? InReplyTo { get; init; }

    /// <summary>The References header's ids.</summary>
    public IReadOnlyList<string>? References { get; init; }

    /// <summary>The attachments; absent or null reads as empty, always encoded.</summary>
    public IReadOnlyList<Attachment> Attachments { get; init => field = value ?? []; } = [];

    /// <summary>
    /// A curated subset chosen by the daemon (List-Unsubscribe, Precedence,
    /// Auto-Submitted, …); never the raw header block.
    /// </summary>
    public IReadOnlyDictionary<string, string>? Headers { get; init; }

    /// <summary>
    /// The unsubscribe offer of a bulk message (<c>message.get</c> only);
    /// absent when the headers offer no usable method, in the junk folder
    /// and for issue-tracker accounts.
    /// </summary>
    public UnsubscribeOffer? Unsubscribe { get; init; }
}

/// <summary>
/// The coding of <see cref="Message"/> (Swift's <c>init(from:)</c> and
/// <c>encode(to:)</c> of Message): the summary's members and the message's
/// own in one JSON object, never a nested <c>summary</c>.
/// </summary>
public sealed class MessageConverter : JsonConverter<Message>
{
    private const string CcKey = "cc";
    private const string BccKey = "bcc";
    private const string ReplyToKey = "replyTo";
    private const string RfcMessageIdKey = "rfcMessageId";
    private const string InReplyToKey = "inReplyTo";
    private const string ReferencesKey = "references";
    private const string AttachmentsKey = "attachments";
    private const string HeadersKey = "headers";
    private const string UnsubscribeKey = "unsubscribe";

    /// <inheritdoc/>
    public override Message Read(ref Utf8JsonReader reader, Type typeToConvert, JsonSerializerOptions options)
    {
        if (reader.TokenType != JsonTokenType.StartObject)
        {
            throw new JsonException($"expected a message object, not {reader.TokenType}");
        }
        var root = JsonElement.ParseValue(ref reader);
        var summary = root.Deserialize(Info<MessageSummary>(options))
            ?? throw new JsonException("a message without its summary");
        return new Message
        {
            Summary = summary,
            Cc = Member<IReadOnlyList<Address>>(root, CcKey, options),
            Bcc = Member<IReadOnlyList<Address>>(root, BccKey, options),
            ReplyTo = Member<IReadOnlyList<Address>>(root, ReplyToKey, options),
            RfcMessageId = Member<string>(root, RfcMessageIdKey, options),
            InReplyTo = Member<string>(root, InReplyToKey, options),
            References = Member<IReadOnlyList<string>>(root, ReferencesKey, options),
            Attachments = Member<IReadOnlyList<Attachment>>(root, AttachmentsKey, options) ?? [],
            Headers = Member<IReadOnlyDictionary<string, string>>(root, HeadersKey, options),
            Unsubscribe = Member<UnsubscribeOffer>(root, UnsubscribeKey, options),
        };
    }

    /// <inheritdoc/>
    public override void Write(Utf8JsonWriter writer, Message value, JsonSerializerOptions options)
    {
        ArgumentNullException.ThrowIfNull(writer);
        ArgumentNullException.ThrowIfNull(value);
        writer.WriteStartObject();
        foreach (var member in JsonSerializer.SerializeToElement(value.Summary, Info<MessageSummary>(options)).EnumerateObject())
        {
            member.WriteTo(writer);
        }
        WriteMember(writer, CcKey, value.Cc, options);
        WriteMember(writer, BccKey, value.Bcc, options);
        WriteMember(writer, ReplyToKey, value.ReplyTo, options);
        WriteMember(writer, RfcMessageIdKey, value.RfcMessageId, options);
        WriteMember(writer, InReplyToKey, value.InReplyTo, options);
        WriteMember(writer, ReferencesKey, value.References, options);
        WriteMember(writer, AttachmentsKey, value.Attachments ?? [], options);
        WriteMember(writer, HeadersKey, value.Headers, options);
        WriteMember(writer, UnsubscribeKey, value.Unsubscribe, options);
        writer.WriteEndObject();
    }

    // decodeIfPresent: absent and null are both nil.
    private static T? Member<T>(JsonElement root, string name, JsonSerializerOptions options)
        where T : class =>
        root.TryGetProperty(name, out var value) && value.ValueKind != JsonValueKind.Null
            ? value.Deserialize(Info<T>(options))
            : null;

    // encodeIfPresent: nil is left out.
    private static void WriteMember<T>(Utf8JsonWriter writer, string name, T? value, JsonSerializerOptions options)
        where T : class
    {
        if (value is null)
        {
            return;
        }
        writer.WritePropertyName(name);
        JsonSerializer.Serialize(writer, value, Info<T>(options));
    }

    private static JsonTypeInfo<T> Info<T>(JsonSerializerOptions options) => (JsonTypeInfo<T>)options.GetTypeInfo(typeof(T));
}

/// <summary>
/// api.MessageListParams. <see cref="UnreadOnly"/> is the deprecated spelling
/// of <c>Filter = MessageFilter.Unread</c>, honoured only while
/// <see cref="Filter"/> is absent.
/// </summary>
public sealed record MessageListParams
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The folder.</summary>
    [JsonPropertyName("folderId")]
    public required FolderId FolderId { get; init; }

    /// <summary>Which page.</summary>
    [JsonPropertyName("page")]
    [JsonRequired]
    public Page Page { get; init; } = new();

    /// <summary>dateDesc (the default) or dateAsc.</summary>
    [JsonPropertyName("sort")]
    public SortOrder? Sort { get; init; }

    /// <summary>all (the default), unread or flagged.</summary>
    [JsonPropertyName("filter")]
    public MessageFilter? Filter { get; init; }

    /// <summary>Deprecated: use <see cref="Filter"/>.</summary>
    [JsonPropertyName("unreadOnly")]
    public bool? UnreadOnly { get; init; }
}

/// <summary>api.MessageListResult.</summary>
public sealed record MessageListResult
{
    /// <summary>The messages of the page.</summary>
    [JsonPropertyName("messages")]
    [JsonConverter(typeof(NullAsEmptyListConverter<MessageSummary>))]
    public IReadOnlyList<MessageSummary> Messages { get; init => field = value ?? []; } = [];

    /// <summary>The next cursor and the total.</summary>
    [JsonPropertyName("page")]
    public required PageInfo Page { get; init; }
}

/// <summary>api.MessageGetParams.</summary>
public sealed record MessageGetParams
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The message.</summary>
    [JsonPropertyName("messageId")]
    public required MessageId MessageId { get; init; }
}

/// <summary>api.MessageGetResult.</summary>
public sealed record MessageGetResult
{
    /// <summary>The message's headers.</summary>
    [JsonPropertyName("message")]
    public required Message Message { get; init; }
}

/// <summary>
/// api.MessageBodyParams. <see cref="RemoteContent"/> overrides the stored
/// preference for this call only: <c>block</c> or <c>allow</c>;
/// <c>knownSenders</c> is invalidArgument. <see cref="TrimQuoted"/> asks for
/// the body without the quoted history under the new text (the result's
/// <see cref="MessageBodyResult.QuotedTrimmed"/> says whether any was cut);
/// null, never false, asks for the whole body, so the key is left out.
/// </summary>
public sealed record MessageBodyParams
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The message.</summary>
    [JsonPropertyName("messageId")]
    public required MessageId MessageId { get; init; }

    /// <summary>The per-call override; null uses the stored preference.</summary>
    [JsonPropertyName("remoteContent")]
    public RemoteContentPolicy? RemoteContent { get; init; }

    /// <summary>Cut the quoted history off; false is stored as null.</summary>
    [JsonPropertyName("trimQuoted")]
    public bool? TrimQuoted { get; init => field = value == true ? true : null; }
}

/// <summary>
/// api.BlockedContent: what the sanitiser removed or neutralised, so the UI
/// can show an honest "N remote images blocked".
/// </summary>
public sealed record BlockedContent
{
    /// <summary>Remote images.</summary>
    [JsonPropertyName("remoteImages")]
    [JsonRequired]
    public int RemoteImages { get; init; }

    /// <summary>Remote style sheets.</summary>
    [JsonPropertyName("remoteStyles")]
    [JsonRequired]
    public int RemoteStyles { get; init; }

    /// <summary>Remote fonts.</summary>
    [JsonPropertyName("remoteFonts")]
    [JsonRequired]
    public int RemoteFonts { get; init; }

    /// <summary>Scripts.</summary>
    [JsonPropertyName("scripts")]
    [JsonRequired]
    public int Scripts { get; init; }

    /// <summary>Forms and controls.</summary>
    [JsonPropertyName("forms")]
    [JsonRequired]
    public int Forms { get; init; }

    /// <summary>Event-handler attributes.</summary>
    [JsonPropertyName("eventHandlers")]
    [JsonRequired]
    public int EventHandlers { get; init; }

    /// <summary>javascript:, data:text/html, vbscript:, …</summary>
    [JsonPropertyName("dangerousUrls")]
    [JsonRequired]
    public int DangerousUrls { get; init; }

    /// <summary>Frames.</summary>
    [JsonPropertyName("embeddedFrames")]
    [JsonRequired]
    public int EmbeddedFrames { get; init; }

    /// <summary>Heuristically detected 1×1 remote images.</summary>
    [JsonPropertyName("trackingPixels")]
    [JsonRequired]
    public int TrackingPixels { get; init; }

    /// <summary>Nothing was removed.</summary>
    [JsonIgnore]
    public bool IsEmpty => this == new BlockedContent();
}

/// <summary>
/// api.Link: a hyperlink of the body with its real destination, so the UI
/// can show where a link goes rather than what it says.
/// </summary>
public sealed record Link
{
    /// <summary>What the link says.</summary>
    [JsonPropertyName("text")]
    public required string Text { get; init; }

    /// <summary>Normalised absolute URL; only http(s) and mailto survive.</summary>
    [JsonPropertyName("href")]
    public required string Href { get; init; }
}

/// <summary>
/// api.MessageBodyResult: the renderable content of a message.
/// <see cref="Html"/> is always the sanitiser's output, a fragment for the
/// webview's body with cid: images rewritten to
/// <c>malachi-cid:&lt;accountId&gt;/&lt;messageId&gt;/&lt;partId&gt;</c>; it is
/// still rendered only in a script-disabled webview under a strict CSP
/// (docs/security.md §3.2).
/// </summary>
public sealed record MessageBodyResult
{
    /// <summary>The message.</summary>
    [JsonPropertyName("messageId")]
    public required MessageId MessageId { get; init; }

    /// <summary>Whether the daemon holds the content.</summary>
    [JsonPropertyName("bodyState")]
    public required BodyState BodyState { get; init; }

    /// <summary>Whether the message has an HTML part.</summary>
    [JsonPropertyName("hasHtml")]
    public required bool HasHtml { get; init; }

    /// <summary>Absent when <see cref="HasHtml"/> is false or <see cref="HtmlWithheld"/>.</summary>
    [JsonPropertyName("html")]
    public string? Html { get; init; }

    /// <summary>The HTML part could not be shown safely; <see cref="Text"/> is still served.</summary>
    [JsonPropertyName("htmlWithheld")]
    public bool? HtmlWithheld { get; init; }

    /// <summary>The plain text alternative, or text derived from the HTML.</summary>
    [JsonPropertyName("text")]
    public required string Text { get; init; }

    /// <summary>What the sanitiser removed.</summary>
    [JsonPropertyName("blocked")]
    [JsonRequired]
    public BlockedContent Blocked { get; init; } = new();

    /// <summary>The body's links with their real destinations.</summary>
    [JsonPropertyName("links")]
    [JsonConverter(typeof(NullAsEmptyListConverter<Link>))]
    public IReadOnlyList<Link> Links { get; init => field = value ?? []; } = [];

    /// <summary>Content-IDs whose cid: references survived, to their part ids.</summary>
    [JsonPropertyName("inlineParts")]
    public IReadOnlyDictionary<string, string>? InlineParts { get; init; }

    /// <summary>
    /// How many pictures of <see cref="InlineParts"/> are kept on the mail
    /// server only and not available on this device now
    /// (<c>Preferences.neverStoreAttachments</c> leaves those of 100 KiB and
    /// more there): <c>message.part</c> answers partNotDownloaded for them
    /// until <c>message.download</c> has fetched the message, after which the
    /// body is asked for again. Absent means 0
    /// (<see cref="RemotePictureCount"/>).
    /// </summary>
    [JsonPropertyName("remotePictures")]
    public int? RemotePictures { get; init; }

    /// <summary><see cref="RemotePictures"/> as Go reads it: absent, or below 0, is 0.</summary>
    [JsonIgnore]
    public int RemotePictureCount => Math.Max(RemotePictures ?? 0, 0);

    /// <summary>
    /// The policy that was applied: <c>block</c> or <c>allow</c>, never
    /// <c>knownSenders</c>. A client offers to load images only under
    /// <c>block</c>.
    /// </summary>
    [JsonPropertyName("remoteContent")]
    public required RemoteContentPolicy RemoteContent { get; init; }

    /// <summary>The sanitiser ruleset that produced <see cref="Html"/>.</summary>
    [JsonPropertyName("sanitizerVersion")]
    public required string SanitizerVersion { get; init; }

    /// <summary>
    /// Asked with <see cref="MessageBodyParams.TrimQuoted"/>: the quoted
    /// history was cut, and everything above describes the body without it.
    /// Absent (an older daemon, or nothing to cut) is false
    /// (<see cref="IsQuotedTrimmed"/>).
    /// </summary>
    [JsonPropertyName("quotedTrimmed")]
    public bool? QuotedTrimmed { get; init; }

    /// <summary><see cref="QuotedTrimmed"/> as Go reads it: absent is false.</summary>
    [JsonIgnore]
    public bool IsQuotedTrimmed => QuotedTrimmed == true;
}

/// <summary>
/// api.MessagePartParams: one MIME part by the <see cref="PartId"/> an
/// <see cref="Attachment"/> carries and a <c>malachi-cid:</c> URL ends with.
/// </summary>
public sealed record MessagePartParams
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The message.</summary>
    [JsonPropertyName("messageId")]
    public required MessageId MessageId { get; init; }

    /// <summary>The part number.</summary>
    [JsonPropertyName("partId")]
    public required string PartId { get; init; }
}

/// <summary>api.MessagePartResult: the decoded part, at most <see cref="API.Limits.MaxAttachmentDataBytes"/>.</summary>
public sealed record MessagePartResult
{
    /// <summary>The part number.</summary>
    [JsonPropertyName("partId")]
    public required string PartId { get; init; }

    /// <summary>The MIME type.</summary>
    [JsonPropertyName("contentType")]
    public required string ContentType { get; init; }

    /// <summary>Sanitised, as in <see cref="Attachment"/>.</summary>
    [JsonPropertyName("filename")]
    public required string Filename { get; init; }

    /// <summary>In bytes.</summary>
    [JsonPropertyName("size")]
    public required long Size { get; init; }

    /// <summary>The content; base64 on the wire.</summary>
    [JsonPropertyName("data")]
    public required byte[] Data { get; init; }
}

/// <summary>
/// api.MessageEmbeddedParams: an attached message (message/rfc822 or *.eml)
/// of a stored message. <see cref="RemoteContent"/> is resolved for the
/// senders of the containing message, not the attached one.
/// </summary>
public sealed record MessageEmbeddedParams
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The containing message.</summary>
    [JsonPropertyName("messageId")]
    public required MessageId MessageId { get; init; }

    /// <summary>The attached message's part.</summary>
    [JsonPropertyName("partId")]
    public required string PartId { get; init; }

    /// <summary>The per-call override; null uses the stored preference.</summary>
    [JsonPropertyName("remoteContent")]
    public RemoteContentPolicy? RemoteContent { get; init; }
}

/// <summary>
/// api.MessageEmbeddedResult: the attached message rendered read-only.
/// <c>Message.Summary.Id</c>, <c>AccountId</c> and <c>FolderId</c> are the
/// containing message's; its cid: pictures are inlined as data: URIs (so
/// <c>Body.InlineParts</c> is empty) and its attachments carry no
/// <c>PartId</c>.
/// </summary>
public sealed record MessageEmbeddedResult
{
    /// <summary>The attached message's part.</summary>
    [JsonPropertyName("partId")]
    public required string PartId { get; init; }

    /// <summary>Its headers.</summary>
    [JsonPropertyName("message")]
    public required Message Message { get; init; }

    /// <summary>Its body.</summary>
    [JsonPropertyName("body")]
    public required MessageBodyResult Body { get; init; }
}

/// <summary>
/// api.MessageDownloadParams: a stored message whose missing content (the
/// attachments kept on the server, or a body not downloaded yet) the daemon
/// fetches from the mail server now.
/// </summary>
public sealed record MessageDownloadParams
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The message.</summary>
    [JsonPropertyName("messageId")]
    public required MessageId MessageId { get; init; }
}

/// <summary>
/// api.MessageDownloadResult: the message as <c>message.get</c> reports it
/// after the download, no attachment <see cref="Attachment.Remote"/> any
/// more, except under <c>Preferences.neverStoreAttachments</c>, where the
/// parts stay remote and are served from the daemon's memory while it holds
/// the message. Part ids may differ from before on Microsoft 365 accounts,
/// whose server rebuilds the MIME: a client replaces the message it shows
/// with this one.
/// </summary>
public sealed record MessageDownloadResult
{
    /// <summary>The message after the download.</summary>
    [JsonPropertyName("message")]
    public required Message Message { get; init; }
}

/// <summary>
/// api.MessageUnsubscribeParams: names the message whose unsubscribe offer to
/// use. The daemon re-reads the headers from the stored message; the client
/// sends no URL or address.
/// </summary>
public sealed record MessageUnsubscribeParams
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The message.</summary>
    [JsonPropertyName("messageId")]
    public required MessageId MessageId { get; init; }

    /// <summary>
    /// Absent for the offer's own method; <c>mailto</c> for the message's
    /// mailto: alternative after an <c>unverified</c> answer (the user
    /// confirmed it); any other value is invalidArgument.
    /// </summary>
    [JsonPropertyName("method")]
    public UnsubscribeMethod? Method { get; init; }
}

/// <summary>api.MessageUnsubscribeResult: what <c>message.unsubscribe</c> did. A one-click URL is never handed out.</summary>
public sealed record MessageUnsubscribeResult
{
    /// <summary>unsubscribed, queued or openUrl.</summary>
    [JsonPropertyName("outcome")]
    public required UnsubscribeOutcome Outcome { get; init; }

    /// <summary>The https page for openUrl (a web-page offer only).</summary>
    [JsonPropertyName("url")]
    public string? Url { get; init; }

    /// <summary>
    /// The address of the mailto: alternative for <c>unverified</c>; absent
    /// when the message offers none.
    /// </summary>
    [JsonPropertyName("mailto")]
    public string? Mailto { get; init; }

    /// <summary>Set for unsubscribed and queued.</summary>
    [JsonPropertyName("unsubscribedAt")]
    public DateTimeOffset? UnsubscribedAt { get; init; }
}

/// <summary>
/// api.MessageFlagParams: at most <see cref="API.Limits.MaxMessageIdsPerCall"/>
/// ids; <c>deleted</c> in either list is invalidArgument (use
/// <c>message.delete</c>).
/// </summary>
public sealed record MessageFlagParams
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The messages.</summary>
    [JsonPropertyName("messageIds")]
    public required IReadOnlyList<MessageId> MessageIds { get; init; }

    /// <summary>The flags to set.</summary>
    [JsonPropertyName("set")]
    public IReadOnlyList<Flag>? Set { get; init; }

    /// <summary>The flags to clear.</summary>
    [JsonPropertyName("clear")]
    public IReadOnlyList<Flag>? Clear { get; init; }
}

/// <summary>api.MessageMoveParams.</summary>
public sealed record MessageMoveParams
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The messages.</summary>
    [JsonPropertyName("messageIds")]
    public required IReadOnlyList<MessageId> MessageIds { get; init; }

    /// <summary>The folder they move to.</summary>
    [JsonPropertyName("targetFolderId")]
    public required FolderId TargetFolderId { get; init; }
}

/// <summary>api.MessageDeleteParams. <see cref="Permanent"/> bypasses the Trash folder.</summary>
public sealed record MessageDeleteParams
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The messages.</summary>
    [JsonPropertyName("messageIds")]
    public required IReadOnlyList<MessageId> MessageIds { get; init; }

    /// <summary>Expunge instead of moving to Trash.</summary>
    [JsonPropertyName("permanent")]
    public bool? Permanent { get; init; }
}
