// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The part of macos/Sources/MalachiCore/Controllers/MessageCache.swift that
// ActionsController.swift uses (loaded, summary, refetch, loadImages,
// beginLoadingImages, fetchRemoteImages, imagesDone); GTK:
// ui/internal/window/message_view.go (loaded, summary), outbox.go
// (refetchMessage) and remote.go (loadRemoteImages, fetchRemoteImages,
// imagesDone). See IActionsMailbox for why the actions name the members
// they use.

using System;
using Malachi.Core.Api;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Model;

namespace Malachi.Core.Controllers;

/// <summary>
/// The loaded-message cache as <see cref="ActionsController"/> sees it:
/// what is cached of a message, a fresh <c>message.get</c>, and the remote
/// images of a body. UI-thread-affine.
/// </summary>
public interface IActionsCache
{
    /// <summary>The cached entry of <paramref name="id"/>, if any, in whatever state it is.</summary>
    LoadedMessage? Loaded(MessageId id);

    /// <summary>
    /// The summary of the cached full message of <paramref name="id"/> (the
    /// second half of message_view.go <c>summary</c>, for a message window
    /// outliving the folder it was opened from).
    /// </summary>
    MessageSummary? Summary(MessageId id);

    /// <summary>
    /// Forgets the cached <c>message.get</c> result of <paramref name="s"/>
    /// (unless one is in flight, which then serves) and fetches again; the
    /// body stays cached (outbox.go <c>refetchMessage</c>).
    /// <paramref name="done"/> runs after every answer, with the entry so far.
    /// </summary>
    void Refetch(MessageSummary s, Action<LoadedMessage> done);

    /// <summary>
    /// Fetches the body of <paramref name="s"/> again with remote images
    /// allowed for this one call (remote.go <c>loadRemoteImages</c>); the bar
    /// shows the wait from the click on. A request already running is left
    /// alone and <paramref name="done"/> is not called; a failure was toasted
    /// and the bar put back before <paramref name="done"/> hears of it.
    /// </summary>
    void LoadImages(MessageSummary s, Action<Outcome<LoadedMessage>> done);

    /// <summary>
    /// Marks the entry of <paramref name="id"/> as waiting for its remote
    /// images and redraws the bar; null when a request is already running.
    /// </summary>
    LoadedMessage? BeginLoadingImages(MessageId id);

    /// <summary>
    /// The <c>message.body</c> call under allow for a request the bar already
    /// shows as loading (<see cref="BeginLoadingImages"/>); it ends the
    /// request either way (remote.go <c>fetchRemoteImages</c>).
    /// </summary>
    void FetchRemoteImages(MessageSummary s, LoadedMessage lm, Action<Outcome<LoadedMessage>> done);

    /// <summary>Ends a request for the images without a new body: the bar offers them again (remote.go <c>imagesDone</c>).</summary>
    void ImagesDone(MessageId id, LoadedMessage lm);
}
