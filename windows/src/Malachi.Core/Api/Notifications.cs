// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/API/Notifications.swift; Go:
// backend/pkg/api/types.go ("Notifications (backend → client)"); contract:
// docs/api.md §5.
//
// Notifications, daemon to client. Best-effort: a client that misses one
// resynchronises through sync.status, folder.list and message.list.

using System;
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
            _ => new Unknown(method),
        };
    }

    private static bool HasParams(string method) =>
        method is API.Notify.NewMessage or API.Notify.SyncState or API.Notify.AuthRequired;

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
