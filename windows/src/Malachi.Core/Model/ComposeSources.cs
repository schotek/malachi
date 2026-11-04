// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ComposeSource.swift
// (composeSource, composeWhat, composeFallbackText, forwardNeedsDownload,
// replyNeedsDownload, askForwardWithout); GTK:
// ui/internal/window/compose_open.go (the same names). The class is not
// named ComposeSource, which is the record of Malachi.Core.Compose
// (Compose/ComposeParams.swift).

using System;
using System.Linq;
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

    /// <summary>
    /// Whether a forward of the loaded message downloads it first
    /// (compose_open.go <c>forwardNeedsDownload</c>): <c>draft.create</c>
    /// imports only what is stored, so a body not downloaded yet, or any part
    /// kept on the mail server (an attachment, or a picture the HTML shows,
    /// which <c>Preferences.neverStoreAttachments</c> leaves there from
    /// 100 KiB), is fetched before the template is asked for. Without the
    /// full message in the cache nothing is known, so it downloads as well:
    /// <c>message.download</c> answers at once when nothing is missing.
    /// </summary>
    public static bool ForwardNeedsDownload(LoadedMessage? lm)
    {
        if (lm?.Msg is not { } m)
        {
            return true;
        }
        if (lm.Body?.BodyState == BodyState.Pending)
        {
            return true;
        }
        return m.Attachments.Any(a => a.IsRemote);
    }

    /// <summary>
    /// Whether a reply (or reply to all) of the loaded message downloads it
    /// first (compose_open.go <c>replyNeedsDownload</c>): the quote copies the
    /// pictures the original shows, and <c>draft.create</c> imports only what
    /// the daemon has, so when the body on display counts pictures kept on
    /// the mail server only (<see cref="MessageBodyResult.RemotePictures"/>;
    /// <c>Preferences.neverStoreAttachments</c> leaves the large ones there)
    /// they are fetched before the template is asked for. The count is the
    /// daemon's: a part on the server with a Content-ID is no reason by itself
    /// (Outlook and Apple Mail give ordinary attachments one, and a quote
    /// never copies those), nor is a picture the daemon holds in memory from
    /// an earlier download. A failed download is not asked about: the reply
    /// goes on, and the compose window says what <c>draft.create</c> left out
    /// (<see cref="ComposeParams.Skipped"/>). Without a body in the cache
    /// nothing is known and nothing is downloaded.
    /// </summary>
    public static bool ReplyNeedsDownload(LoadedMessage? lm) => RemoteBar.RemotePictures(lm?.Body) > 0;

    /// <summary>
    /// Whether a failed download before a forward asks "Forward Without
    /// Attachments?" (compose_open.go <c>askForwardWithout</c>). The forward
    /// goes on at once with what the daemon has where asking would change
    /// nothing: without a daemon to ask (not connected, or the connection
    /// lost; <c>draft.create</c> fails the same way and the window opens from
    /// what the pane knows), for a daemon without <c>message.download</c>
    /// (methodNotFound, notImplemented), as before attachments on demand, and
    /// for a message over the daemon's cap, which can never be downloaded
    /// (attachmentTooBig; <c>draft.create</c> lists what it could not take).
    /// </summary>
    public static bool AskForwardWithout(Exception? error)
    {
        switch (error)
        {
            case null:
            case RpcClientException { Error.Kind: ClientErrorKind.NotConnected or ClientErrorKind.Disconnected }:
            case RpcException { Code.Value: ErrorCode.AttachmentTooBig }:
                return false;
            default:
                return !Download.MethodUnsupported(error);
        }
    }
}
