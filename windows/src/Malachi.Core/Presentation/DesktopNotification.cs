// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of what macos/Sources/MalachiMail/Notifications/NotificationService.swift
// (post) hands UNUserNotificationCenter: the title and body of
// notificationText, the account as threadIdentifier, the account and message
// ids as userInfo, "message-<id>" as the identifier; GTK:
// ui/internal/window/notify.go (notifyNewMessage: the same texts,
// SendNotification("message-"+id), default action app.show). Windows-only:
// a toast's Tag and Group hold at most 64 characters, so an identifier that
// is longer (the daemon's ids are opaque) keeps its start and ends in a hash
// of the whole, which stays distinct and stable.

using System;
using System.Security.Cryptography;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.Model;

namespace Malachi.Core.Presentation;

/// <summary>
/// One desktop notification of a new message, as the platform shows it:
/// plain text only (the sender and the subject are hostile input, trimmed and
/// capped by <see cref="NotificationText"/>), never markup.
/// </summary>
public sealed record DesktopNotification
{
    /// <summary>The longest <see cref="Tag"/> and <see cref="Group"/> a Windows toast accepts.</summary>
    public const int MaxIdentifierLength = 64;

    // What an identifier over the limit ends with: "~" and 16 hex digits of
    // its SHA-256 (64 bits: no two ids of one mailbox meet).
    private const int HashDigits = 16;

    /// <summary>The sender's display name, or "New message".</summary>
    public required string Title { get; init; }

    /// <summary>The subject, or "(No subject)".</summary>
    public required string Body { get; init; }

    /// <summary>
    /// The account's notifications are grouped under it (Swift
    /// <c>threadIdentifier</c>); at most <see cref="MaxIdentifierLength"/> characters.
    /// </summary>
    public required string Group { get; init; }

    /// <summary>
    /// <c>message-&lt;id&gt;</c>, the notification's identity (notify.go's
    /// notification id); at most <see cref="MaxIdentifierLength"/> characters.
    /// </summary>
    public required string Tag { get; init; }

    /// <summary>The account, handed back when the notification is clicked.</summary>
    public required AccountId AccountId { get; init; }

    /// <summary>The message, handed back when the notification is clicked.</summary>
    public required MessageId MessageId { get; init; }

    /// <summary>The notification of <paramref name="n"/> (notify.go <c>notifyNewMessage</c>).</summary>
    public static DesktopNotification For(NewMessageNotification n)
    {
        ArgumentNullException.ThrowIfNull(n);
        var (title, body) = NotificationText.Of(n);
        return new DesktopNotification
        {
            Title = title,
            Body = body,
            Group = Identifier(n.AccountId.Value ?? ""),
            Tag = TagOf(n.Message.Id),
            AccountId = n.AccountId,
            MessageId = n.Message.Id,
        };
    }

    /// <summary>
    /// The <see cref="Tag"/> of message <paramref name="id"/>'s notification:
    /// notified.go's <c>notificationID</c> made to fit
    /// (<see cref="Identifier"/>), which is what withdraws it again.
    /// </summary>
    public static string TagOf(MessageId id) => Identifier(NotifiedMessages.NotificationId(id));

    /// <summary>
    /// <paramref name="s"/> when it fits <see cref="MaxIdentifierLength"/>
    /// characters; otherwise as much of its start as fits before "~" and 16
    /// hex digits of the SHA-256 of all of it, never cut inside a surrogate
    /// pair.
    /// </summary>
    public static string Identifier(string s)
    {
        ArgumentNullException.ThrowIfNull(s);
        if (s.Length <= MaxIdentifierLength)
        {
            return s;
        }
        var keep = MaxIdentifierLength - HashDigits - 1;
        if (char.IsHighSurrogate(s[keep - 1]))
        {
            keep--;
        }
        var hash = SHA256.HashData(Encoding.UTF8.GetBytes(s));
        var hex = Convert.ToHexStringLower(hash.AsSpan(0, HashDigits / 2));
        return string.Concat(s.AsSpan(0, keep), "~", hex);
    }
}
