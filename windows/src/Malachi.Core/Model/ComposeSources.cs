// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ComposeSource.swift
// (composeSource, composeWhat, composeFallbackText); GTK:
// ui/internal/window/compose_open.go (composeSource, composeWhat,
// composeFallbackText). The class is not named ComposeSource, which is the
// record of Malachi.Core.Compose (Compose/ComposeParams.swift).

using System;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.I18n;
using Malachi.Core.Text;
using Malachi.Core.Transport;

namespace Malachi.Core.Model;

/// <summary>
/// What the pane hands to a reply or forward, the pure parts. The backend
/// builds the template (<c>draft.create</c>); only when it cannot answer
/// does the compose window open from what the pane knows.
/// </summary>
public static class ComposeSources
{
    /// <summary>
    /// What the pane knows about the message (compose_open.go
    /// <c>composeSource</c>): the summary, bettered by the full headers and
    /// the text when they were loaded. A body that is not fetched contributes
    /// no text; Go's zero date becomes null.
    /// </summary>
    public static ComposeSource ComposeSource(MessageSummary s, Message? m, MessageBodyResult? b)
    {
        ArgumentNullException.ThrowIfNull(s);
        var src = new ComposeSource
        {
            Id = s.Id,
            AccountId = s.AccountId,
            From = s.From,
            To = s.To ?? [],
            Subject = s.Subject,
            Date = s.Date.IsGoZero ? null : s.Date,
        };
        if (m is not null)
        {
            src = src with
            {
                From = m.Summary.From,
                ReplyTo = m.ReplyTo ?? [],
                To = m.Summary.To ?? [],
                Cc = m.Cc ?? [],
                Subject = m.Summary.Subject,
                Date = m.Summary.Date.IsGoZero ? null : m.Summary.Date,
            };
        }
        if (b is not null && b.BodyState == BodyState.Fetched)
        {
            src = src with { Text = b.Text };
        }
        return src;
    }

    /// <summary>
    /// <see cref="ComposeSource(MessageSummary, Message?, MessageBodyResult?)"/>
    /// over a cache entry, the summary alone while nothing is loaded.
    /// </summary>
    public static ComposeSource ComposeSource(MessageSummary s, LoadedMessage? lm) => ComposeSource(s, lm?.Msg, lm?.Body);

    /// <summary>Names the action for an error toast (compose_open.go <c>composeWhat</c>).</summary>
    public static string ComposeWhat(ComposeKind kind) =>
        kind == ComposeKind.Forward ? L10n.T("Preparing the forwarded message") : L10n.T("Preparing the reply");

    /// <summary>
    /// The toast shown when <c>draft.create</c> failed and the window opens
    /// with the plain quote instead (compose_open.go
    /// <c>composeFallbackText</c>): nothing when there is no backend to ask
    /// or it does not offer the call yet (the fallback is the normal course
    /// then), the usual sentence otherwise.
    /// </summary>
    public static string ComposeFallbackText(string what, Exception? error)
    {
        switch (error)
        {
            case RpcClientException { Error.Kind: ClientErrorKind.NotConnected or ClientErrorKind.Disconnected }:
            case RpcException { Code.Value: ErrorCode.NotImplemented }:
                return "";
            default:
                return RpcErrorText.Text(what, error);
        }
    }
}
