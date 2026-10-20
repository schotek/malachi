// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/QuickReplyController.swift
// (QuickReplyHost); GTK: ui/internal/quickreply/controller.go (Host). Go's
// Call is CallAsync with the timeout Go puts in its context; Swift's
// failed(_ error:) is Failed(failure), since Error is a keyword of Visual
// Basic (CA1716).

using System;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Model;

namespace Malachi.Core.Controllers;

/// <summary>
/// quickreply.Host: what <see cref="QuickReplyController"/> needs of the
/// window. Every member but <see cref="CallAsync"/> and <see cref="Post"/> is
/// called on the UI thread.
/// </summary>
public interface IQuickReplyHost
{
    /// <summary>
    /// One call to the daemon (the main window's client, as the mailbox's
    /// calls go), bounded by <paramref name="timeout"/>; a server-side error
    /// as <see cref="Transport.RpcException"/>. It may be awaited on any
    /// thread.
    /// </summary>
    Task<TResult> CallAsync<TParams, TResult>(RpcMethod<TParams, TResult> method, TParams parameters, TimeSpan timeout);

    /// <summary>
    /// Runs <paramref name="action"/> on the UI thread, after what was posted
    /// before; it may be called from any thread.
    /// </summary>
    void Post(Action action);

    /// <summary>
    /// The line above the quote of a reply in <paramref name="mode"/> to
    /// message <paramref name="id"/>, as the compose window computes it
    /// (<see cref="ActionsController.ReplyAttribution"/>); "" for none.
    /// </summary>
    string Attribution(QuickReplyMode mode, MessageId id);

    /// <summary>
    /// Runs before <c>draft.create</c> of a reply (never of a comment): the
    /// window downloads message <paramref name="id"/> first when its compose
    /// window would (<see cref="ComposeSources.ReplyNeedsDownload"/>:
    /// pictures of the quote kept on the mail server only), ignores the
    /// outcome as the window does, and calls <paramref name="done"/> on the
    /// UI thread (<see cref="ActionsController.PrepareReply"/>). Without a
    /// reason to download it calls <paramref name="done"/> at once.
    /// </summary>
    void BeforeCreate(MessageId id, Action done);

    /// <summary>
    /// Opens the compose window with a reply, reply all or forward
    /// (<paramref name="mode"/>) of message <paramref name="id"/> — a comment
    /// when the account comments — prepared by <c>draft.create</c> as the
    /// window always is, with <paramref name="ownHtml"/> (safe HTML:
    /// <see cref="Conversation.QuickReplyHtml"/>) in its editor as the user's
    /// own text, marked as an edit
    /// (<see cref="ActionsController.OpenCompose(Compose.ComposeKind, MessageId, object?, string, Action?)"/>).
    /// <paramref name="opened"/> is called on the UI thread once a window
    /// really opened, and never when none does (one is being prepared for the
    /// message already, the message is unknown, or a comment's
    /// <c>draft.create</c> failed).
    /// </summary>
    void OpenCompose(QuickReplyMode mode, MessageId id, string ownHtml, Action opened);

    /// <summary>
    /// A quick reply went to the outbox (<paramref name="comment"/>: a comment
    /// on an issue); the window shows the compose window's toast.
    /// </summary>
    void Sent(bool comment);

    /// <summary>
    /// Send did not go through (<paramref name="failure"/> from
    /// <c>draft.create</c>, <c>draft.save</c> or <c>message.send</c>); the
    /// window shows the compose window's toast for a failed send. The text
    /// stays in the bar.
    /// </summary>
    void Failed(Exception failure);
}
