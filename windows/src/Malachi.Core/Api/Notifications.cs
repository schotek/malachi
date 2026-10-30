// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/API/Notifications.swift; Go:
// backend/pkg/api/types.go ("Notifications (backend → client)";
// DraftsChangedNotification a Windows addition ahead of Swift) and
// board.go (BoardChangedNotification, a Windows addition ahead of Swift);
// contract: docs/api.md §5. notify.contactsChanged
// (ContactsChangedNotification, ContactsBook.cs) is a case here, where Swift
// decodes it beside DaemonNotification (ContactsChangedNotification.decode).
//
// Notifications, daemon to client. Best-effort: a client that misses one
// resynchronises through sync.status, folder.list and message.list.

using System;
using System.Collections.Generic;
using System.Text.Json;
using System.Text.Json.Serialization;
using System.Text.Json.Serialization.Metadata;

namespace Malachi.Core.Api;

/// <summary>
/// api.NewMessageNotification: one message that arrived after a folder's
/// initial synchronisation, once its body is stored.
/// </summary>
public sealed record NewMessageNotification
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The folder.</summary>
    [JsonPropertyName("folderId")]
    public required FolderId FolderId { get; init; }

    /// <summary>The new message, complete, with its thread.</summary>
    [JsonPropertyName("message")]
    public required MessageSummary Message { get; init; }
}

/// <summary>api.SyncStateNotification.</summary>
public sealed record SyncStateNotification
{
    /// <summary>The account's new state.</summary>
    [JsonPropertyName("state")]
    public required SyncState State { get; init; }
}

/// <summary>
/// api.AuthRequiredNotification: the daemon cannot proceed without the user
/// (wrong password, expired token, missing keyring). <see cref="Message"/>
/// is technical English, not for display verbatim.
/// </summary>
public sealed record AuthRequiredNotification
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>authRequired, authFailed or keyringError.</summary>
    [JsonPropertyName("reason")]
    public required ErrorCode Reason { get; init; }

    /// <summary>Technical English.</summary>
    [JsonPropertyName("message")]
    public required string Message { get; init; }

    /// <summary>Set for an OAuth2 flow the daemon runs itself: open it in the browser.</summary>
    [JsonPropertyName("authUrl")]
    public string? AuthUrl { get; init; }
}

/// <summary>api.AccountsChangedNotification: no payload; clients re-run <c>account.list</c>.</summary>
public sealed record AccountsChangedNotification;

/// <summary>
/// api.DraftsChangedNotification (<c>notify.draftsChanged</c>): drafts of the
/// account that reply to a stored message were saved, deleted, sent, or
/// linked to a board case or unlinked, from any client (the MCP bridge
/// too): what <see cref="ThreadSummary.DraftCount"/> and thread.get's drafts
/// return for <see cref="ThreadIds"/> may differ now. <see cref="ThreadIds"/>
/// empty = any thread of the account. Clients showing one of those threads
/// ask thread.get again.
/// </summary>
public sealed record DraftsChangedNotification
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The threads; empty = any thread of the account.</summary>
    [JsonPropertyName("threadIds")]
    [JsonConverter(typeof(NullAsEmptyListConverter<ThreadId>))]
    public IReadOnlyList<ThreadId> ThreadIds { get; init => field = value ?? []; } = [];
}

/// <summary>
/// A decoded notification. <see cref="Unknown"/> carries the method of one
/// this client does not know (a newer daemon); the raw notification is still
/// available from the transport for a caller that wants it.
/// </summary>
public abstract record DaemonNotification
{
    // Only the cases below derive from it.
    private DaemonNotification()
    {
    }

    /// <summary><c>notify.newMessage</c>.</summary>
    /// <param name="Payload">The account, the folder and the message.</param>
    public sealed record NewMessage(NewMessageNotification Payload) : DaemonNotification;

    /// <summary><c>notify.syncState</c>.</summary>
    /// <param name="State">The account's new state.</param>
    public sealed record SyncState(Api.SyncState State) : DaemonNotification;

    /// <summary><c>notify.authRequired</c>.</summary>
    /// <param name="Payload">The account, the reason and the sign-in URL, if any.</param>
    public sealed record AuthRequired(AuthRequiredNotification Payload) : DaemonNotification;

    /// <summary><c>notify.accountsChanged</c>.</summary>
    public sealed record AccountsChanged : DaemonNotification;

    /// <summary>
    /// <c>notify.messagesChanged</c>: messages of an account were hidden,
    /// shown again or rebuilt in place without arriving or leaving
    /// (<see cref="MessagesChangedNotification"/>, Jira.cs).
    /// </summary>
    /// <param name="Payload">The account and its folders (empty = any).</param>
    public sealed record MessagesChanged(MessagesChangedNotification Payload) : DaemonNotification;

    /// <summary>
    /// <c>notify.boardChanged</c>: what <c>board.list</c> returns changed
    /// (<see cref="BoardChangedNotification"/>, Board.cs); absent params read
    /// as any account.
    /// </summary>
    /// <param name="Payload">The accounts (empty = any).</param>
    public sealed record BoardChanged(BoardChangedNotification Payload) : DaemonNotification;

    /// <summary>
    /// <c>notify.draftsChanged</c>: the drafts that reply to threads of an
    /// account changed (<see cref="DraftsChangedNotification"/>).
    /// </summary>
    /// <param name="Payload">The account and its threads (empty = any).</param>
    public sealed record DraftsChanged(DraftsChangedNotification Payload) : DaemonNotification;

    /// <summary>
    /// <c>notify.calendarChanged</c>: stored calendars or occurrences changed
    /// (<see cref="CalendarChangedNotification"/>, Calendar.cs); absent params
    /// read as any account.
    /// </summary>
    /// <param name="Payload">The accounts, calendars and span that changed.</param>
    public sealed record CalendarChanged(CalendarChangedNotification Payload) : DaemonNotification;

    /// <summary>
    /// <c>notify.calendarState</c>: an account's calendar synchronisation state
    /// changed (<see cref="CalendarStateNotification"/>, Calendar.cs).
    /// </summary>
    /// <param name="State">The account's new calendar state.</param>
    public sealed record CalendarState(CalendarSyncState State) : DaemonNotification;

    /// <summary>
    /// <c>notify.eventAlert</c>: an event alert is due, or was dismissed
    /// (<see cref="EventAlertNotification"/>, Calendar.cs).
    /// </summary>
    /// <param name="Payload">The alert.</param>
    public sealed record EventAlert(EventAlertNotification Payload) : DaemonNotification;

    /// <summary>
    /// <c>notify.invitationsChanged</c>: the unanswered invitations of some
    /// accounts changed (<see cref="InvitationsChangedNotification"/>,
    /// Calendar.cs); absent params read as any account.
    /// </summary>
    /// <param name="Payload">The accounts (empty = any).</param>
    public sealed record InvitationsChanged(InvitationsChangedNotification Payload) : DaemonNotification;

    /// <summary>
    /// <c>notify.assistantProfileChanged</c>: the assistant profile or its
    /// pending proposals changed (AssistantProfile.cs); no params, the
    /// application's <c>ProfileStore</c> reads it again.
    /// </summary>
    public sealed record AssistantProfileChanged : DaemonNotification;

    /// <summary>
    /// <c>notify.contactsChanged</c>: the contacts of some books changed
    /// (<see cref="ContactsChangedNotification"/>, ContactsBook.cs); absent
    /// params read as any book.
    /// </summary>
    /// <param name="Payload">The account and its books (empty = any).</param>
    public sealed record ContactsChanged(ContactsChangedNotification Payload) : DaemonNotification;

    /// <summary>A notification of a newer daemon.</summary>
    /// <param name="Method">Its method.</param>
    public sealed record Unknown(string Method) : DaemonNotification;

    /// <summary>
    /// Decodes a raw notification, the whole JSON-RPC line as the transport
    /// read it, by its method (Swift <c>init(_ raw: RPCNotification)</c>):
    /// the params are read from the line only for the methods that have them.
    /// Throws a <see cref="JsonException"/> when a known method carries params
    /// of the wrong shape, or none.
    /// </summary>
    public static DaemonNotification Decode(string method, ReadOnlySpan<byte> line)
    {
        ArgumentNullException.ThrowIfNull(method);
        if (!HasParams(method))
        {
            return Decode(method, (JsonElement?)null);
        }
        var reader = new Utf8JsonReader(line);
        var root = JsonElement.ParseValue(ref reader);
        if (root.ValueKind != JsonValueKind.Object)
        {
            throw new JsonException($"{method}: the notification is not a JSON object");
        }
        return Decode(method, root.TryGetProperty("params", out var parameters) ? parameters : null);
    }

    /// <summary>
    /// Decodes a notification by its method from its params, for a transport
    /// that has taken the line apart already; null params are absent ones.
    /// </summary>
    public static DaemonNotification Decode(string method, JsonElement? parameters)
    {
        ArgumentNullException.ThrowIfNull(method);
        return method switch
        {
            API.Notify.NewMessage => new NewMessage(Params(method, parameters, ApiJsonContext.Wire.NewMessageNotification)),
            API.Notify.SyncState => new SyncState(Params(method, parameters, ApiJsonContext.Wire.SyncStateNotification).State),
            API.Notify.AuthRequired => new AuthRequired(Params(method, parameters, ApiJsonContext.Wire.AuthRequiredNotification)),
            API.Notify.AccountsChanged => new AccountsChanged(),
            API.Notify.MessagesChanged => new MessagesChanged(Params(method, parameters, ApiJsonContext.Wire.MessagesChangedNotification)),
            API.Notify.BoardChanged => new BoardChanged(BoardChangedParams(method, parameters)),
            API.Notify.DraftsChanged => new DraftsChanged(Params(method, parameters, ApiJsonContext.Wire.DraftsChangedNotification)),
            API.Notify.CalendarChanged => new CalendarChanged(OptionalParams(method, parameters, ApiJsonContext.Wire.CalendarChangedNotification)),
            API.Notify.CalendarState => new CalendarState(Params(method, parameters, ApiJsonContext.Wire.CalendarStateNotification).State),
            API.Notify.EventAlert => new EventAlert(Params(method, parameters, ApiJsonContext.Wire.EventAlertNotification)),
            API.Notify.InvitationsChanged => new InvitationsChanged(OptionalParams(method, parameters, ApiJsonContext.Wire.InvitationsChangedNotification)),
            API.Notify.AssistantProfileChanged => new AssistantProfileChanged(),
            API.Notify.ContactsChanged => new ContactsChanged(OptionalParams(method, parameters, ApiJsonContext.Wire.ContactsChangedNotification)),
            _ => new Unknown(method),
        };
    }

    private static bool HasParams(string method) =>
        method is API.Notify.NewMessage or API.Notify.SyncState or API.Notify.AuthRequired or API.Notify.MessagesChanged
            or API.Notify.BoardChanged or API.Notify.DraftsChanged or API.Notify.CalendarChanged or API.Notify.CalendarState
            or API.Notify.EventAlert or API.Notify.InvitationsChanged or API.Notify.ContactsChanged;

    // notify.boardChanged's params are all optional: absent or null = any account.
    private static BoardChangedNotification BoardChangedParams(string method, JsonElement? parameters) =>
        parameters is null or { ValueKind: JsonValueKind.Null or JsonValueKind.Undefined }
            ? new BoardChangedNotification()
            : Params(method, parameters, ApiJsonContext.Wire.BoardChangedNotification);

    // Params that are all optional (notify.calendarChanged,
    // notify.invitationsChanged, notify.contactsChanged): absent or null =
    // every member left out.
    private static T OptionalParams<T>(string method, JsonElement? parameters, JsonTypeInfo<T> info)
        where T : class, new() =>
        parameters is null or { ValueKind: JsonValueKind.Null or JsonValueKind.Undefined }
            ? new T()
            : Params(method, parameters, info);

    private static T Params<T>(string method, JsonElement? parameters, JsonTypeInfo<T> info)
        where T : class
    {
        if (parameters is not { ValueKind: not (JsonValueKind.Null or JsonValueKind.Undefined) } p)
        {
            throw new JsonException($"{method} without params");
        }
        return p.Deserialize(info) ?? throw new JsonException($"{method} without params");
    }
}
