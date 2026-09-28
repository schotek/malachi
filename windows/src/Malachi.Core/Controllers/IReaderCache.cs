// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only seam: what the reader asks of the loaded-message cache
// (macos/Sources/MalachiMail/MessageView/MessageViewController.swift and
// Windows/EmbeddedWindowController.swift use MessageCache directly: loaded,
// fetch, fetchEmbedded; GTK: message_view.go fetchMessage and embedded.go
// fetchEmbedded on the Window). MessageCache implements it; the reader's
// tests answer it without a daemon.

using System;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Model;

namespace Malachi.Core.Controllers;

/// <summary>The loaded messages, as the reader and the attachment actions see them.</summary>
public interface IReaderCache
{
    /// <summary>The cached entry of <paramref name="id"/>, if any, in whatever state it is.</summary>
    LoadedMessage? Loaded(MessageId id);

    /// <summary>
    /// Runs <c>message.get</c> and <c>message.body</c> for
    /// <paramref name="s"/> as far as the cache lacks them and calls
    /// <paramref name="done"/> on the UI thread after every answer, at once
    /// for a complete entry (message_view.go <c>fetchMessage</c>).
    /// </summary>
    void Fetch(MessageSummary s, Action<LoadedMessage> done);

    /// <summary><c>message.part</c> for one attachment (attachments.go <c>fetchAttachment</c>).</summary>
    Task<MessagePartResult> FetchAttachmentAsync(
        AccountId accountId, MessageId messageId, string partId, CancellationToken cancellationToken = default);

    /// <summary><c>message.embedded</c> for one part (embedded.go <c>fetchEmbedded</c>).</summary>
    Task<MessageEmbeddedResult> FetchEmbeddedAsync(
        AccountId accountId, MessageId messageId, string partId, RemoteContentPolicy? remote = null, CancellationToken cancellationToken = default);
}
