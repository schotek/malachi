// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: what a new-message toast carries back when it is
// clicked (docs/windows-port.md §10), the counterpart of the userInfo of
// macos/Sources/MalachiMail/Notifications/NotificationService.swift
// (accountId, messageId) and of GTK's default action app.show. The toast
// gets the pairs through AppNotificationBuilder.AddArgument, which escapes
// "%", ";" and "=" in keys and values (%25, %3B, %3D) and joins them as
// "key=value;key=value"; a click hands them back already split
// (AppNotificationActivatedEventArgs.Arguments) and as that string
// (Argument), which Parse also reads.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Presentation;

namespace Malachi.Platform.Windows.Notifications;

/// <summary>The arguments of a new-message toast.</summary>
public static class NotificationArguments
{
    /// <summary>The key of what a click does.</summary>
    public const string ActionKey = "action";

    /// <summary>The only action: open the message (the main window without one).</summary>
    public const string OpenAction = "open";

    /// <summary>The key of the message's account.</summary>
    public const string AccountKey = "account";

    /// <summary>The key of the message.</summary>
    public const string MessageKey = "message";

    /// <summary>The pairs a toast of <paramref name="n"/> carries, in order.</summary>
    public static IReadOnlyList<KeyValuePair<string, string>> For(DesktopNotification n)
    {
        ArgumentNullException.ThrowIfNull(n);
        return
        [
            new(ActionKey, OpenAction),
            new(AccountKey, n.AccountId.Value ?? ""),
            new(MessageKey, n.MessageId.Value ?? ""),
        ];
    }

    /// <summary>
    /// The activation of a click with <paramref name="arguments"/>, the
    /// pairs as the platform split them. The ids are kept when present and
    /// not empty; a click without them shows the main window.
    /// </summary>
    public static NotificationActivation Parse(IEnumerable<KeyValuePair<string, string>>? arguments)
    {
        string? account = null;
        string? message = null;
        foreach (var (key, value) in arguments ?? [])
        {
            if (string.IsNullOrEmpty(value))
            {
                continue;
            }
            if (string.Equals(key, AccountKey, StringComparison.Ordinal))
            {
                account = value;
            }
            else if (string.Equals(key, MessageKey, StringComparison.Ordinal))
            {
                message = value;
            }
        }
        AccountId? accountId = account is null ? default(AccountId?) : new AccountId(account);
        MessageId? messageId = message is null ? default(MessageId?) : new MessageId(message);
        return new NotificationActivation(accountId, messageId);
    }

    /// <summary>
    /// The activation of a click whose arguments came as one string,
    /// <c>key=value;key=value</c> with <c>%</c>, <c>;</c> and <c>=</c>
    /// escaped as AppNotificationBuilder escapes them.
    /// </summary>
    public static NotificationActivation Parse(string? argument) => Parse(Split(argument));

    /// <summary>The pairs of an argument string, unescaped; a part without "=" has an empty value.</summary>
    public static IReadOnlyList<KeyValuePair<string, string>> Split(string? argument)
    {
        var pairs = new List<KeyValuePair<string, string>>();
        if (string.IsNullOrEmpty(argument))
        {
            return pairs;
        }
        foreach (var part in argument.Split(';'))
        {
            if (part.Length == 0)
            {
                continue;
            }
            var eq = part.IndexOf('=', StringComparison.Ordinal);
            var key = eq < 0 ? part : part[..eq];
            var value = eq < 0 ? "" : part[(eq + 1)..];
            pairs.Add(new(Unescape(key), Unescape(value)));
        }
        return pairs;
    }

    // %25, %3B and %3D back to %, ; and =; any other % sequence stays as it is.
    private static string Unescape(string s)
    {
        if (!s.Contains('%', StringComparison.Ordinal))
        {
            return s;
        }
        var sb = new System.Text.StringBuilder(s.Length);
        for (var i = 0; i < s.Length; i++)
        {
            if (s[i] == '%' && i + 2 < s.Length)
            {
                var code = s.AsSpan(i + 1, 2);
                char? c = code.Equals("25", StringComparison.Ordinal) ? '%'
                    : code.Equals("3B", StringComparison.OrdinalIgnoreCase) ? ';'
                    : code.Equals("3D", StringComparison.OrdinalIgnoreCase) ? '='
                    : null;
                if (c is { } decoded)
                {
                    sb.Append(decoded);
                    i += 2;
                    continue;
                }
            }
            sb.Append(s[i]);
        }
        return sb.ToString();
    }
}
