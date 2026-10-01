// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/LoadedMessage.swift (LoadedMessage);
// GTK: ui/internal/window/message_view.go (loadedMessage, complete,
// bodySettled, size, unsubscribing). LoadedCache, the pane's text helpers
// (LoadedMessageText) and the quoted-text button (QuotedTextOffer) are the
// other types of that Swift file. The two variants of the body (with and
// without its quoted history: showQuoted, store) are Swift-first; GTK has
// them in message_view.go too.

using System;
using System.Text;
using Malachi.Core.Api;

namespace Malachi.Core.Model;

/// <summary>
/// What fetching produced for one message (message_view.go
/// <c>loadedMessage</c>): the full header view, the body, or the error that
/// prevented the body. A failed <c>message.get</c> is only logged (the
/// summary headers stay) and retried the next time the message is shown; a
/// failed body is retried the same way. A class, because the pane, a
/// message window and the compose prefill share one entry and see each
/// other's halves arrive. UI-thread-affine, as every controller's state
/// (docs/windows-port.md §7.1): no locks.
/// </summary>
public sealed class LoadedMessage
{
    /// <summary>The full header view (<c>message.get</c>).</summary>
    public Message? Msg { get; set; }

    /// <summary>
    /// The body on display (<c>message.body</c>): without the quoted history
    /// (<see cref="MessageBodyParams.TrimQuoted"/>), or whole while
    /// <see cref="QuotedShown"/>.
    /// </summary>
    public MessageBodyResult? Body { get; set; }

    /// <summary>The <c>message.body</c> failure.</summary>
    public Exception? Err { get; set; }

    /// <summary>
    /// The quoted history is shown: <see cref="Body"/> is the whole body,
    /// asked for without trimQuoted (the view's Show Quoted Text,
    /// <see cref="QuotedReveal"/>). Off by default: a body comes trimmed.
    /// </summary>
    public bool QuotedShown { get; private set; }

    /// <summary>
    /// The other variant of the body, kept for switching back without asking
    /// the daemon again; dropped when <see cref="Body"/> is replaced under
    /// another remote-content policy (the remote images, the pictures), which
    /// it would not have.
    /// </summary>
    public MessageBodyResult? OtherBody { get; set; }

    /// <summary>
    /// A <c>message.body</c> for the other variant is in flight (a switch
    /// while the request for the variant shown before ran).
    /// </summary>
    public bool FetchingOther { get; set; }

    /// <summary>
    /// The remote-content policy the variant switched to is asked with when
    /// it has to be fetched (<see cref="RemoteBar.PicturesPolicy"/> of the
    /// body shown before the switch: images the user loaded stay loaded).
    /// </summary>
    public RemoteContentPolicy? SwitchPolicy { get; set; }

    /// <summary>Insertion order in <see cref="LoadedCache"/>.</summary>
    public ulong Seq { get; set; }

    /// <summary>
    /// The account the message belongs to, from the summary it was fetched
    /// for (or its full message): <see cref="LoadedCache.RemoveAll(AccountId)"/>
    /// finds the entries of an account whose messages the daemon rebuilt
    /// (notify.messagesChanged). Null for an entry made without a summary.
    /// </summary>
    public AccountId? AccountId
    {
        get => field ?? Msg?.Summary.AccountId;
        set;
    }

    /// <summary>
    /// The header half is in flight; a second fetch for the same id while one
    /// runs joins instead of asking the daemon twice.
    /// </summary>
    public bool Getting { get; set; }

    /// <summary>The body half is in flight.</summary>
    public bool Fetching { get; set; }

    /// <summary>
    /// Set from the moment the user asks for the remote images (Load Images,
    /// Always From This Sender) until the daemon has answered; the bar shows
    /// it instead of the buttons (<see cref="RemoteBar.RemoteBarStateFor"/>).
    /// </summary>
    public bool LoadingImages { get; set; }

    /// <summary>
    /// Set from the moment the user asks for the pictures kept on the mail
    /// server (Download Pictures) until the message was downloaded and its
    /// body asked for again; the pictures bar shows it instead of the button
    /// (<see cref="RemoteBar.PicturesBarStateFor"/>).
    /// </summary>
    public bool LoadingPictures { get; set; }

    /// <summary>
    /// Set once a picture of the body went missing and the body was asked
    /// for again, until the next download of the message
    /// (<see cref="RemoteBar.RecheckPictures"/>): never more than once in
    /// between.
    /// </summary>
    public bool PicturesRechecked { get; set; }

    /// <summary>
    /// Set from the click on the bulk strip's button until
    /// <c>message.unsubscribe</c> has answered (message_view.go
    /// <c>loadedMessage.unsubscribing</c>); the button waits.
    /// </summary>
    public bool Unsubscribing { get; set; }

    /// <summary>Nothing is left to fetch.</summary>
    public bool Complete => Msg is not null && Body is not null;

    /// <summary>The body half has an answer (content or error).</summary>
    public bool BodySettled => Body is not null || Err is not null;

    /// <summary>
    /// What the entry costs the cache, in UTF-8 bytes: its bodies, both
    /// variants (an HTML body carries its inlined pictures).
    /// </summary>
    public int Size => BodySize(Body) + BodySize(OtherBody);

    /// <summary>
    /// Shows the body with its quoted history (<paramref name="on"/>) or
    /// without: the two variants trade places (<see cref="Body"/>,
    /// <see cref="OtherBody"/>, and their requests in flight), and a body
    /// error belongs to the variant left. True when anything changed;
    /// <see cref="Body"/> is then null when the variant has yet to be
    /// fetched.
    /// </summary>
    public bool ShowQuoted(bool on)
    {
        if (on == QuotedShown)
        {
            return false;
        }
        if (OtherBody is null)
        {
            SwitchPolicy = RemoteBar.PicturesPolicy(this);
        }
        QuotedShown = on;
        (Body, OtherBody) = (OtherBody, Body);
        (Fetching, FetchingOther) = (FetchingOther, Fetching);
        Err = null;
        return true;
    }

    /// <summary>
    /// Stores a <c>message.body</c> answer asked for the variant
    /// <paramref name="quoted"/> (with the quoted history or not): as
    /// <see cref="Body"/> when that variant is still shown, else as
    /// <see cref="OtherBody"/>. <paramref name="replacing"/> (an answer under
    /// another remote-content policy) drops the other variant shown before.
    /// </summary>
    public void Store(MessageBodyResult res, bool quoted, bool replacing = false)
    {
        ArgumentNullException.ThrowIfNull(res);
        if (quoted == QuotedShown)
        {
            Body = res;
            Err = null;
            if (replacing)
            {
                OtherBody = null;
            }
        }
        else
        {
            OtherBody = res;
        }
    }

    private static int BodySize(MessageBodyResult? body) =>
        body is null ? 0 : Encoding.UTF8.GetByteCount(body.Html ?? "") + Encoding.UTF8.GetByteCount(body.Text);
}
